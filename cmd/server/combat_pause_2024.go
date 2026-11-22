package main

// A pending GM decision is a boundary, not a failed saving throw whose effects
// may be assumed by the next command. The saved decision lives in Postgres; this
// file owns the common request/worker preflight and single-process execution
// exclusion. It does not hold a database connection while a command runs.
// Nested operations in one command reuse its lease. Reads and speech remain open.
import (
	"context"
	"net/http"
	"strings"
	"sync"
)

type campaignCommandLock struct {
	mu   sync.Mutex
	refs int
}

var campaignCommands = struct {
	sync.Mutex
	locks  map[int]*campaignCommandLock
	owners map[uint64]map[int]bool
}{locks: map[int]*campaignCommandLock{}, owners: map[uint64]map[int]bool{}}

func acquireCampaignCommand(lobby int) (func(), bool) {
	if lobby <= 0 {
		return func() {}, true
	}
	gid := goroutineID()
	campaignCommands.Lock()
	if campaignCommands.owners[gid][lobby] {
		campaignCommands.Unlock()
		return func() {}, true
	}
	lock := campaignCommands.locks[lobby]
	if lock == nil {
		lock = &campaignCommandLock{}
		campaignCommands.locks[lobby] = lock
	}
	lock.refs++
	if !lock.mu.TryLock() {
		lock.refs--
		campaignCommands.Unlock()
		return nil, false
	}
	if campaignCommands.owners[gid] == nil {
		campaignCommands.owners[gid] = map[int]bool{}
	}
	campaignCommands.owners[gid][lobby] = true
	campaignCommands.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			campaignCommands.Lock()
			defer campaignCommands.Unlock()
			delete(campaignCommands.owners[gid], lobby)
			if len(campaignCommands.owners[gid]) == 0 {
				delete(campaignCommands.owners, gid)
			}
			lock.mu.Unlock()
			lock.refs--
			if lock.refs == 0 {
				delete(campaignCommands.locks, lobby)
			}
		})
	}, true
}

type campaignRequestScope struct {
	release []func()
	lobbies map[int]bool
}
type campaignScopeKey struct{}

func withCampaignRequestScope(r *http.Request) (*http.Request, func()) {
	if r.Context().Value(campaignScopeKey{}) != nil {
		return r, func() {}
	}
	scope := &campaignRequestScope{lobbies: map[int]bool{}}
	return r.WithContext(context.WithValue(r.Context(), campaignScopeKey{}, scope)), func() {
		defer forgetRequestConcentrationJournals()
		for i := len(scope.release) - 1; i >= 0; i-- {
			scope.release[i]()
		}
	}
}

func pendingConcentrationDecision(lobby int) (bool, error) {
	if db == nil || lobby <= 0 {
		return false, nil
	}
	var pending bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM monster_concentration WHERE lobby_id=$1 AND pending<>'')`, lobby).Scan(&pending)
	return pending, err
}

func pendingCombatDecision(lobby int) (bool, error) {
	if requestHasDBFailure() || currentConcentrationJournal(lobby).Fault {
		return true, nil
	}
	pending, err := pendingConcentrationDecision(lobby)
	if err != nil || pending || db == nil || lobby <= 0 {
		return pending, err
	}
	return hasBlockingCombatWork(lobby)
}

const combatPauseMessage = "Combat resolution is paused. No new game action was performed. The GM must settle pending decisions and resume saved work before dependent combat or time can continue."

// Explicit editors allow the GM to settle/correct the current state, not run new
// automatic game actions. A new Concentration cast by the sole pending caster
// also explicitly abandons that old cast, per the existing casting rule.
func requestSettlesCombatPause(r *http.Request, lobby int, body interface{}) bool {
	switch r.URL.Path {
	case "/api/gm/combat-resume", "/api/gm/monster-concentration", "/api/gm/legendary-resistance", "/api/gm/notices", "/api/gm/adjust-monster", "/api/gm/conditions", "/api/gm/spell-effect":
		return true
	}
	if strings.HasSuffix(r.URL.Path, "/combat/end") {
		return true
	}
	if effect, _, ok := effectOf(r.Method, r.URL.Path); ok && (effect.Why == whyTalk || effect.Why == whyNotes) {
		return true
	}
	if a, ok := body.(*actionRequest); ok && actionIsImmediate(a.Action) {
		return true
	}
	if work, err := hasPausedCombatWork(lobby); err != nil || work {
		return false
	}
	req, ok := body.(*gmNarrateRequest)
	if !ok || req.MonsterAction == nil {
		return false
	}
	a := req.MonsterAction
	if !strings.EqualFold(stripUsage(a.Action), "Spellcasting") || !damageApplies(a.ApplyDamage) {
		return false
	}
	spell, ok := srdSpellsMemory[normalizeSpellSlug(a.Use)]
	if !ok || !spell.Concentration {
		return false
	}
	ids := monsterIDsByKeyOrName(lobby, a.Monster)
	if len(ids) != 1 {
		return false
	}
	var total, other int
	err := db.QueryRow(`SELECT COUNT(*),COUNT(*) FILTER(WHERE monster_id<>$2) FROM monster_concentration WHERE lobby_id=$1 AND pending<>''`, lobby, ids[0]).Scan(&total, &other)
	return err == nil && total == 1 && other == 0
}

func gateCampaignRequest(w http.ResponseWriter, r *http.Request, lobby int, body interface{}) bool {
	if lobby <= 0 || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	if scope, ok := r.Context().Value(campaignScopeKey{}).(*campaignRequestScope); ok {
		release, ok := acquireCampaignCommand(lobby)
		if !ok {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "combat_operation_in_progress", "message": "Another command is resolving in this campaign. Nothing was changed; retry after it finishes."})
			return false
		}
		scope.release = append(scope.release, release)
		scope.lobbies[lobby] = true
	}
	pending, err := pendingCombatDecision(lobby)
	if answerDBFailure(w, err, "pending combat adjudication") {
		return false
	}
	if pending && !requestSettlesCombatPause(r, lobby, body) {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "combat_adjudication_pending", "message": combatPauseMessage})
		return false
	}
	return true
}

func completeGMRead(w http.ResponseWriter, r *http.Request, body interface{}, lobby int) bool {
	return checkCustomReferences(w, r, body) && gateCampaignRequest(w, r, lobby, body)
}

// Clock changes also have non-request callers. Keep the preflight in the clock
// owner, so a worker or helper cannot age the provisional branch while a GM
// decision or its continuation is outstanding.
func claimCampaignClock(lobby int) (func(), bool) {
	release, ok := acquireCampaignCommand(lobby)
	if !ok {
		return func() {}, false
	}
	pending, err := pendingCombatDecision(lobby)
	if err != nil || pending {
		release()
		if err != nil {
			concentrationFailure(lobby, "The campaign clock", err)
		} else {
			addGMNotice(lobby, "The campaign clock was not advanced: settle pending combat decisions and resume saved work first.")
		}
		return func() {}, false
	}
	return release, true
}

// Called by the existing held-reply/DB-error owner only on normal handler
// completion. Costs and action-log writes after a damage helper must finish too;
// a crash, panic or failed command must leave capturing work for reconciliation.
func noteCapturedCombatWork(lobby int, id string) {
	if wch := watchOf(); wch != nil {
		wch.mu.Lock()
		if wch.combatWork == nil {
			wch.combatWork = map[string]int{}
		}
		wch.combatWork[id] = lobby
		wch.mu.Unlock()
	}
}
func finishCapturedRequestWork(status int) {
	if status >= 500 {
		return
	}
	if wch := watchOf(); wch != nil {
		wch.mu.Lock()
		work := make(map[string]int, len(wch.combatWork))
		for id, lobby := range wch.combatWork {
			work[id] = lobby
		}
		wch.mu.Unlock()
		for id, lobby := range work {
			finishCapturedCombatWork(lobby, id)
		}
	}
}
