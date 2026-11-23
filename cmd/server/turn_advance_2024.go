package main

// The one owner of a turn change in a fight (audit AUD-10, findings C-2 and B-10). The GM's combat/next, combat/skip, a narration
// with advance_turn, the timeout worker, the pending-turn walk and the removal of the creature whose turn it is all call
// advanceTurn, and only advanceTurn runs the turn hooks: the ending creature's (turnEndHooks) and the next one's (turnStartHooks),
// each exactly once, in the order of those tables.
//
// Before Nov 15 2026 the change was written four times, each path with its own subset of the hooks (a narrated advance never
// brought a Blinking character back from the Ethereal Plane, Sacred Weapon ran out only through combat/next, Reactions came back
// at a different moment on each path), and only skip guarded against two changes at once: two concurrent combat/next moved the
// turn once but ran its end-of-turn effects twice.
//
// A change runs in four steps:
//  1. claim: with the fight's row locked, the turn is checked (an expected turn, a timeout) and marked as being changed
//     (turn_claim). A second change of the same fight while the mark stands is refused (turn_change_in_progress), so a double
//     submit or the timeout worker racing the GM changes the turn once. A request that names the turn it means to end
//     (expected_turn: combat/next, combat/skip, narrate's advance_turn) is refused once that turn has ended (turn_already_moved,
//     with the turn it is now), so a retry that arrives after the first change finished doesn't end the next turn too.
//  2. the ending creature's hooks run (the turn order still points at it), then the caller's between step (Legendary Actions);
//  3. move: with the row locked again, the next creature that has a turn is chosen (dead characters and monsters at 0 Hit Points
//     have none; a Thief's Reflexes second turn is dropped after round 1) and the round, index and turn clock are written;
//  4. the next creature's hooks run, and the mark is cleared.
// The hooks read and write other rows, so they run outside any transaction (CLAUDE.md: never db inside a transaction); the mark,
// not a lock, keeps them from overlapping with another turn change. A mark older than two minutes (a crash) no longer stands (fightColumns).

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// turnRef is a turn as a caller saw it: advance only while it is still the current one.
type turnRef struct {
	Round, Index int
	StartedAt    time.Time // compared when set
	Named        bool      // the request named this turn (expected_turn): once it has moved the change is refused as turn_already_moved
}

// isCurrent is whether f is still at this turn.
func (e *turnRef) isCurrent(f *fight) bool {
	return f.Round == e.Round && f.Index == e.Index && (e.StartedAt.IsZero() || f.StartedAt.Equal(e.StartedAt))
}

// expectedTurn is the turn a request means to end (expected_turn on combat/next, combat/skip and narrate's advance_turn): the
// round and the turn's place in the turn order, as GET /api/campaigns/{id}/combat (round, current_turn_index), GET /api/gm/status
// (combat.round, combat.current_turn_index) and every turn change's reply (round, turn_index) show them. A retry of a turn change
// that already happened then answers turn_already_moved instead of ending the next creature's turn too.
type expectedTurn struct {
	Round     int  `json:"round"`
	TurnIndex *int `json:"turn_index"`
}

// namedTurn is a request's expected_turn as the turn change reads it: nil when it wasn't sent. An incomplete one is answered 400
// invalid_expected_turn here and ok is false.
func namedTurn(w http.ResponseWriter, lobby int, e *expectedTurn) (named *turnRef, ok bool) {
	if e == nil {
		return nil, true
	}
	if e.Round < 1 || e.TurnIndex == nil || *e.TurnIndex < 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_expected_turn",
			"message": fmt.Sprintf("expected_turn names the turn to end: {\"round\": 1 or more, \"turn_index\": 0 or more}, as GET /api/campaigns/%d/combat shows them (round, current_turn_index). Nothing was changed.", lobby)})
		return nil, false
	}
	return &turnRef{Round: e.Round, Index: *e.TurnIndex, Named: true}, true
}

// turnHow is how a turn ends.
type turnHow struct {
	legendary []legendaryRequest
	request   *http.Request
	resume    *turnResume
	by        string                                // who or what ends it, for the note of a pending turn it closes ("the GM skipped the turn")
	expect    *turnRef                              // advance only while this is still the current turn (the timeout worker, the pending-turn walk, gmEndsTurn, expected_turn)
	allow     func(f *fight) (code, message string) // a check of the turn under the lock (skip: a player's turn that has timed out)
	remove    int                                   // this creature, whose turn it is, leaves the fight: no end-of-turn hooks; the next one starts
	result    string                                // how the turn ended, for the turn schedule's record (turn_results; "": read from the turn, endedHow)
}

// turnChange is what a turn change did; the hooks and the callers' replies read it.
type turnChange struct {
	Paused           bool
	Failure          string
	LegendaryResults []map[string]interface{}
	Lobby            int
	how              turnHow
	EndedID          int
	EndedName        string
	EndedRound       int
	EndedIndex       int
	EndedStartedAt   time.Time
	EndedSecond      bool                     // the ended turn is a Thief's Reflexes second turn
	Recorded         string                   // how the turn schedule recorded the ended turn ("" when it didn't: no schedule, or already recorded)
	RoundOpened      *roundRecord             // the turn schedule's round the change opened (the fight's round went up)
	Removed          []map[string]interface{} // the entries taken out (remove)
	Round, Index     int
	NextID           int
	NextName         string
	NewRound         bool
	Log              []string               // conditions and effects at the end and the start of the turn (public: also written to the action log)
	GMNotes          []string               // the GM's notices of the turn change: never written to the action log (turnEffectLines)
	SpellNotes       []string               // Blink, Regenerate, Aura of Life ... (moreTurnHooks)
	Survivor         map[string]interface{} // Champion level 18
	Message          string                 // why a change was refused
	Now              map[string]interface{} // a refusal because the turn has moved: the turn it is now (round, turn_index, current_turn, current_turn_id)
}

// window is the Legendary Action window the ended turn opens ("round:index").
func (t *turnChange) window() string { return fmt.Sprintf("%d:%d", t.EndedRound, t.EndedIndex) }

// turnHook is one rule that runs when a turn ends or starts.
type turnHook struct {
	name string
	run  func(t *turnChange)
}

// turnEndHooks run for the creature whose turn ends (not for one removed from the fight), in this order; turnStartHooks for the
// creature whose turn starts. A new rule that happens at the end or the start of a turn is one row here.
var turnEndHooks, turnStartHooks []turnHook

func characterTurn(id int) bool { return id > 0 && !isCompanionCombatID(id) }

func init() {
	turnEndHooks = []turnHook{
		{"an unresolved pending turn of the slot closes", func(t *turnChange) {
			if t.EndedID > 0 {
				closePendingTurns(t.Lobby, t.EndedID, t.EndedRound, "the turn ended without it ("+t.how.by+")")
			}
		}},
		{"the turn schedule records how the turn ended, once (turn_results)", recordTurnResult}, // schedule_walk_2024.go
		{"countercharm, Sacred Weapon and Holy Nimbus count down", func(t *turnChange) {
			if characterTurn(t.EndedID) {
				decrementCountercharm(t.EndedID)
				decrementSacredWeapon(t.EndedID)
				decrementHolyNimbus(t.EndedID)
			}
		}},
		{"conditions and effects at the end of the turn", func(t *turnChange) {
			l := endTurnEffects(t.Lobby, -1) // -1: the current turn, still the ending one
			t.Log, t.GMNotes = append(t.Log, l.Public...), append(t.GMNotes, l.GM...)
			t.Paused = l.Paused
			if l.Err != nil {
				t.Failure = l.Err.Error()
			}
		}},
		{"a spell's damage at the end of the target's next turn lands (Acid Arrow, Vitriolic Sphere)", func(t *turnChange) {
			t.Log = append(t.Log, laterDamageAtTurnEnd(t.Lobby, t.EndedID)...) // spell_later_damage_2024.go
		}},
		{"effects that last to the end of this creature's next turn end (Chill Touch, Starry Wisp, Guiding Bolt ...)", func(t *turnChange) {
			expireCombatEffectsAtTurnEnd(t.Lobby, t.EndedID) // combat_effects.go
		}},
		{"a creature that ends its turn inside an object takes its trait's damage (Incorporeal Movement)", func(t *turnChange) {
			if note := objectEndDamage(t.Lobby, t.EndedID); note != "" { // map_spaces_2024.go
				t.Log = append(t.Log, note)
			}
		}},
		{"the Legendary Action window after the turn opens", func(t *turnChange) {
			setLastTurnEnded(t.Lobby, t.window(), t.EndedID)
		}},
	}
	turnStartHooks = []turnHook{
		{"conditions and effects at the start of the turn", func(t *turnChange) {
			l := startTurnEffects(t.Lobby)
			t.Log, t.GMNotes = append(t.Log, l.Public...), append(t.GMNotes, l.GM...)
			t.Paused = l.Paused
			if l.Err != nil {
				t.Failure = l.Err.Error()
			}
		}},
		{"a monster's trait that acts on its death and is still pending (the backstop of monster_death_trigger_2024.go)", func(t *turnChange) {
			t.Log = append(t.Log, drainDeathTriggers()...)
		}},
		{"the turn's action, Bonus Action, Reaction, movement and once-per-turn features return", func(t *turnChange) {
			if characterTurn(t.NextID) {
				resetTurnFlags(t.NextID, characterSpeedOf(t.NextID)) // also ends a Dodge: "until the start of your next turn"
			}
		}},
		{"a dying character's Death Saving Throw, once, at the start of its turn (SRD 5.2.1; it takes no action)", func(t *turnChange) {
			if characterTurn(t.NextID) && needsDeathSave(t.NextID) {
				text, already := rollDeathSave(t.NextID) // death_save_2024.go
				if !already {
					t.Log = append(t.Log, fmt.Sprintf("%s: %s", t.NextName, text))
					recordGMAction(db, t.Lobby, "death_save", fmt.Sprintf("%s starts the turn at 0 Hit Points: the Death Saving Throw is rolled", t.NextName), text)
				}
			}
		}},
		{"spells at the turn change (Blink, Regenerate, Aura of Life, Cloak of Displacement)", func(t *turnChange) {
			ended := t.EndedID
			if t.how.remove != 0 {
				ended = 0 // the creature left the fight: its turn didn't end
			}
			t.SpellNotes = moreTurnHooks(t.Lobby, ended, t.NextID)
			for _, n := range t.SpellNotes {
				recordGMAction(db, t.Lobby, "spell_effect", n, n)
			}
		}},
		{"Survivor (Champion level 18)", func(t *turnChange) {
			if characterTurn(t.NextID) {
				t.Survivor = survivorRegen(t.NextID, t.NextName)
			}
		}},
		{"Multiattack Defense marks end", func(t *turnChange) { clearAllMultiattackDefenseHits(t.Lobby) }},
		{"Opportunity Attack chances end", func(t *turnChange) { clearOAWindows(t.Lobby) }},
		{"a new round of the fight opens the turn schedule's next round", func(t *turnChange) {
			if t.NewRound {
				t.RoundOpened = scheduleFightRound(t.Lobby, t.Round) // check_in_2024.go
			}
		}},
	}
}

// advanceTurn ends the current turn and starts the next one (see the top of this file). It answers the change and "" or a refusal
// code (turnRefusals; t.Message may say more).
type savedTurnHow struct {
	By        string
	Expect    *turnRef
	Remove    int
	Result    string
	Legendary []legendaryRequest
}
type turnResume struct {
	Change turnChange
	How    savedTurnHow
	Phase  string
	Next   int
}

func init() {
	combatResumeHandlers["turn-advance"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var saved turnResume
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, err
		}
		if saved.Change.Lobby != lobby {
			return nil, fmt.Errorf("turn continuation campaign mismatch")
		}
		t, code := advanceTurn(lobby, turnHow{resume: &saved})
		if code == "combat_adjudication_pending" {
			return map[string]interface{}{"combat_paused": true}, nil
		}
		if code != "" {
			return nil, fmt.Errorf("turn continuation stopped: %s", code)
		}
		return t.reply(), nil
	}
}
func turnFrame(t *turnChange, phase string, next int) turnResume {
	return turnResume{Change: *t, How: savedTurnHow{t.how.by, t.how.expect, t.how.remove, t.how.result, t.how.legendary}, Phase: phase, Next: next}
}
func advanceTurn(lobby int, how turnHow) (*turnChange, string) {
	release, ok := acquireCampaignCommand(lobby)
	if !ok {
		return &turnChange{Lobby: lobby, how: how, Message: "Another command is resolving in this campaign."}, "combat_operation_in_progress"
	}
	defer release()
	pending, err := pendingConcentrationDecision(lobby)
	if err != nil {
		return &turnChange{Lobby: lobby, how: how}, "database_error"
	}
	if pending {
		return &turnChange{Lobby: lobby, how: how, Message: combatPauseMessage}, "combat_adjudication_pending"
	}
	var t *turnChange
	var token, code string
	phase, next := "end", 0
	if saved := how.resume; saved != nil {
		t = &saved.Change
		phase, next = saved.Phase, saved.Next
		t.how = turnHow{by: saved.How.By, expect: saved.How.Expect, remove: saved.How.Remove, result: saved.How.Result, legendary: saved.How.Legendary}
		expected := &turnRef{Round: t.EndedRound, Index: t.EndedIndex, Named: true}
		if phase == "start" {
			expected.Round, expected.Index = t.Round, t.Index
		}
		_, token, code = claimTurn(lobby, turnHow{expect: expected})
	} else {
		if active, e := hasBlockingCombatWork(lobby); e != nil {
			return &turnChange{Lobby: lobby, how: how}, "database_error"
		} else if active {
			return &turnChange{Lobby: lobby, how: how, Message: combatPauseMessage}, "combat_adjudication_pending"
		}
		t, token, code = claimTurn(lobby, how)
	}
	if code != "" {
		return t, code
	}
	defer releaseTurn(lobby, token)
	t.Paused = false
	t.Failure = ""
	runHooks := func(hooks []turnHook, kind string, from int) string {
		for i := from; i < len(hooks); i++ {
			restore, err := armCombatFrame(lobby, "turn-advance", turnFrame(t, kind, i+1))
			if err != nil {
				t.Message = err.Error()
				return "combat_resume_error"
			}
			hooks[i].run(t)
			restore()
			if t.Failure != "" {
				t.Message = "Turn effects could not be completed; check the GM notices."
				return "combat_resume_error"
			}
			if t.Paused || combatResolutionPaused(lobby) {
				t.Message = "The turn paused after completed effects. Resolve the GM decision, then resume saved combat work."
				return "combat_adjudication_pending"
			}
		}
		return ""
	}
	if phase == "end" {
		if t.how.remove == 0 {
			if code = runHooks(turnEndHooks, "end", next); code != "" {
				return t, code
			}
		}
		phase, next = "between", 0
	}
	if phase == "between" {
		for i := next; i < len(t.how.legendary); i++ {
			restore, err := armCombatFrame(lobby, "turn-advance", turnFrame(t, "between", i+1))
			if err != nil {
				return t, "combat_resume_error"
			}
			result, refusal, msg := takeLegendaryAction(t.how.request, lobby, t.how.legendary[i], t.window(), t.EndedID)
			if refusal != "" {
				result = map[string]interface{}{"combatant_id": t.how.legendary[i].CombatantID, "error": refusal, "message": msg}
			}
			t.LegendaryResults = append(t.LegendaryResults, result)
			restore()
			if combatResolutionPaused(lobby) {
				return t, "combat_adjudication_pending"
			}
		}
		if code = moveTurn(t, token); code != "" {
			return t, code
		}
		phase, next = "start", 0
	}
	if phase == "start" {
		if code = runHooks(turnStartHooks, "start", next); code != "" {
			return t, code
		}
	}
	return t, ""
}

func newClaimToken() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// claimTurn checks the turn with the row locked and marks it as being changed.
func claimTurn(lobby int, how turnHow) (*turnChange, string, string) {
	tx, err := db.Begin()
	if err != nil {
		return &turnChange{Lobby: lobby, how: how}, "", "database_error"
	}
	defer tx.Rollback()
	t, f, code := checkTurn(lobby, how, func() (*fight, error) { return lockFight(tx, lobby) })
	if code != "" {
		return t, "", code
	}
	cur := f.current()
	token := newClaimToken()
	if _, err := tx.Exec(`UPDATE combat_state SET turn_claim = $1, turn_claimed_at = NOW() WHERE lobby_id = $2`, token, lobby); err != nil {
		return t, "", "database_error"
	}
	if err := tx.Commit(); err != nil {
		return t, "", "database_error"
	}
	t.EndedID, t.EndedName = asInt(cur["id"]), sprintAny(cur["name"])
	t.EndedRound, t.EndedIndex, t.EndedStartedAt = f.Round, f.Index, f.StartedAt
	t.EndedSecond = keyOf(cur).extra
	return t, token, ""
}

// precheckTurn is whether how could change the turn now, read without the lock: for a request that does something before its
// turn change (narrate records the narration and runs a monster action first), so that a turn change refused anyway refuses the
// whole request before anything is changed. The change itself still checks under the lock (claimTurn).
func precheckTurn(lobby int, how turnHow) (*turnChange, string) {
	t, _, code := checkTurn(lobby, how, func() (*fight, error) { return readFight(db, lobby) })
	return t, code
}

// checkTurn reads the fight with read and runs how.check on it: the change so far, the fight, and "" or a refusal code (with
// t.Message, and t.Now when the turn has moved).
func checkTurn(lobby int, how turnHow, read func() (*fight, error)) (*turnChange, *fight, string) {
	t := &turnChange{Lobby: lobby, how: how}
	f, err := read()
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return t, nil, "no_active_combat"
	case errors.Is(err, errBadOrder):
		return t, nil, "invalid_combat_turn"
	case err != nil:
		return t, nil, "database_error"
	}
	code, msg := how.check(f)
	if code == "" {
		return t, f, ""
	}
	t.Message = msg
	if cur := f.current(); cur != nil && (code == "turn_already_moved" || code == "turn_already_advanced") {
		t.Now = map[string]interface{}{"round": f.Round, "turn_index": f.Index, "current_turn": sprintAny(cur["name"]), "current_turn_id": asInt(cur["id"])}
	}
	return t, f, code
}

// check is whether this turn of f may change now: the turn order is usable, the turn is the one expected, no other change of it
// is under way, and the caller's own check passes. A code and a message when it may not.
func (how turnHow) check(f *fight) (string, string) {
	switch {
	case len(f.Order) == 0:
		return "no_combatants", ""
	case f.current() == nil:
		return "invalid_combat_turn", "" // kept for repair rather than guessing whose turn is consumed
	}
	if e := how.expect; e != nil && !e.isCurrent(f) {
		if e.Named { // the turn the request named (expected_turn) has ended: a retry of a change that already happened
			msg := fmt.Sprintf("The turn you named (round %d, turn_index %d) has already ended; nothing was changed.", e.Round, e.Index)
			if cur := f.current(); cur != nil {
				msg += fmt.Sprintf(" It is now %s's turn (round %d, turn_index %d): to end it, send expected_turn {\"round\": %d, \"turn_index\": %d}.",
					sprintAny(cur["name"]), f.Round, f.Index, f.Round, f.Index)
			}
			return "turn_already_moved", msg
		}
		return "turn_already_advanced", ""
	}
	if f.claimHeld {
		return "turn_change_in_progress", ""
	}
	if how.remove != 0 && asInt(f.current()["id"]) != how.remove {
		return "turn_already_advanced", ""
	}
	if how.allow != nil {
		return how.allow(f)
	}
	return "", ""
}

// releaseTurn clears the mark of a turn change (only its own: a mark that expired and was taken over stays).
func releaseTurn(lobby int, token string) {
	db.Exec(`UPDATE combat_state SET turn_claim = '' WHERE lobby_id = $1 AND turn_claim = $2`, lobby, token)
}

// noTurnFor says which combatants have no turn: dead characters and companions that are gone are read here, before the move's
// transaction; a monster at 0 Hit Points is read from the locked turn order itself.
func noTurnFor(lobby int) func(order []map[string]interface{}) func(int) bool {
	down := map[int]bool{}
	regenFromZero := map[int]bool{} // a monster that keeps its turn at 0 Hit Points to regenerate (Troll: monsterFeaturesTurnStart)
	if f, err := readFight(db, lobby); err == nil {
		for _, e := range f.Order {
			id := asInt(e["id"])
			switch {
			case isCompanionCombatID(id):
				down[id] = companionGone(id)
			case id > 0:
				down[id] = combatCharacterIsDead(id)
			default:
				regenFromZero[id] = regeneratesFromZero(monsterMetaFor(sprintAny(e["monster_key"])))
			}
		}
	}
	return func(order []map[string]interface{}) func(int) bool {
		monsterDown := map[int]bool{}
		for _, e := range order {
			if id := asInt(e["id"]); id < 0 && asInt(e["hp"]) <= 0 && (!regenFromZero[id] || e["regen_dead"] == true) {
				monsterDown[id] = true
			}
		}
		return func(id int) bool {
			if id < 0 {
				return monsterDown[id]
			}
			return down[id]
		}
	}
}

// moveTurn writes the next turn: the next creature that has one, the round, the turn clock.
func moveTurn(t *turnChange, token string) string {
	skip := noTurnFor(t.Lobby)
	f, err := changeFight(t.Lobby, func(f *fight) error {
		if f.claim != token {
			return errTurnMoved // the fight restarted or ended during the change, or the mark expired and was taken over
		}
		from := f.Index + 1
		if t.how.remove != 0 {
			t.Removed, _ = f.remove(t.how.remove)
			from = f.Index
			if len(f.Order) == 0 {
				return errNotInFight
			}
		}
		f.nextTurn(from, skip(f.Order))
		f.restartClock = true
		return nil
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "no_active_combat"
	case errors.Is(err, errTurnMoved), errors.Is(err, errNotInFight):
		return "turn_already_advanced"
	case err != nil:
		return "database_error"
	}
	t.Round, t.Index = f.Round, f.Index
	t.NewRound = f.Round != t.EndedRound
	if cur := f.current(); cur != nil {
		t.NextID, t.NextName = asInt(cur["id"]), sprintAny(cur["name"])
	}
	return ""
}

// nextTurn moves f to the first creature from position from (len: the round is over) that has a turn. A Thief's Reflexes
// second turn is dropped once the first round is over ("two turns during the first round of any combat").
func (f *fight) nextTurn(from int, noTurn func(int) bool) {
	round := f.Round
	if from >= len(f.Order) {
		from, round = 0, round+1
	}
	if round >= 2 {
		kept := f.Order[:0:0]
		pos := from
		for i, e := range f.Order {
			if keyOf(e).extra {
				if i < from {
					pos--
				}
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) < len(f.Order) {
			f.Order, from = kept, pos
			if from >= len(f.Order) {
				from, round = 0, round+1
			}
		}
	}
	ids := make([]int, len(f.Order))
	for i, e := range f.Order {
		ids[i] = asInt(e["id"])
	}
	index, wraps := nextLivingCombatTurn(from, ids, noTurn)
	f.Index, f.Round = index, round+wraps
}

// combatTurnTimeout is when a turn may be skipped: the campaign's table rule combat_turn_timeout (4 hours by default). on is false
// when it is off (turns are never skipped). combat/skip, the timeout worker and the GM status countdown read it.
func combatTurnTimeout(lobby int) (limit time.Duration, on bool) {
	return timeoutRule(lobby, "combat_turn_timeout", 4*time.Hour)
}

// turnRefusals: the status and message of each refusal of a turn change.
var turnRefusals = map[string]struct {
	status  int
	message string
}{
	"no_active_combat":             {http.StatusConflict, "No fight is running in this campaign: POST /api/campaigns/%d/combat/start starts one."},
	"no_combatants":                {http.StatusConflict, "The turn order is empty: add combatants with POST /api/campaigns/%d/combat/add, or end the fight."},
	"invalid_combat_turn":          {http.StatusConflict, "The current turn points outside the turn order; nothing was changed. GET /api/campaigns/%d/combat shows the order."},
	"turn_already_advanced":        {http.StatusConflict, "That turn has already ended; nothing was changed. GET /api/campaigns/%d/combat shows whose turn it is."},
	"turn_already_moved":           {http.StatusConflict, "The turn named in expected_turn has already ended; nothing was changed. GET /api/campaigns/%d/combat shows whose turn it is."},
	"combat_adjudication_pending":  {http.StatusConflict, combatPauseMessage},
	"combat_operation_in_progress": {http.StatusConflict, "Another command is resolving in campaign %d."},
	"combat_resume_error":          {http.StatusInternalServerError, "Combat work could not be completed in campaign %d."},
	"turn_change_in_progress":      {http.StatusConflict, "Another request is changing this turn right now (it counts once); nothing more was changed. GET /api/campaigns/%d/combat shows whose turn it is."},
	"not_player_turn":              {http.StatusConflict, "It is a monster's or NPC's turn: only a player's turn is skipped. Resolve it and POST /api/campaigns/%d/combat/next."},
	"turn_not_timed_out":           {http.StatusConflict, "The turn hasn't timed out yet (table rule combat_turn_timeout); end it with POST /api/campaigns/%d/combat/next if it is done."},
	"database_error":               {http.StatusInternalServerError, "The database failed; nothing more was changed (campaign %d)."},
}

// answerTurnRefusal writes the reply to a refused turn change (extra fields added to it).
func answerTurnRefusal(w http.ResponseWriter, lobby int, t *turnChange, code string, extra ...map[string]interface{}) {
	r, ok := turnRefusals[code]
	if !ok {
		r.status, r.message = http.StatusConflict, "The turn wasn't changed (campaign %d)."
	}
	msg := fmt.Sprintf(r.message, lobby)
	if t != nil && t.Message != "" {
		msg = t.Message
	}
	out := map[string]interface{}{"error": code, "message": msg}
	if t != nil && code == "combat_adjudication_pending" {
		out["turn_resolution_paused"] = true
		out["completed_turn_effects"] = t.Log
	}
	if t != nil {
		for k, v := range t.Now { // the turn it is now (round, turn_index, current_turn, current_turn_id)
			out[k] = v
		}
	}
	for _, x := range extra {
		for k, v := range x {
			out[k] = v
		}
	}
	writeJSONStatus(w, r.status, out)
}

// reply is what every turn change answers: the new turn, the round, and what happened at the change.
func (t *turnChange) reply() map[string]interface{} {
	m := map[string]interface{}{"success": true, "round": t.Round, "current_turn": t.NextName, "turn_index": t.Index, "action_economy_reset": true}
	if len(t.LegendaryResults) > 0 {
		m["legendary_actions_taken"] = t.LegendaryResults
	}
	if t.NewRound {
		m["new_round"] = true
	}
	if t.RoundOpened != nil {
		m["round_complete"] = roundCompleteText(roundCombat, t.EndedRound, t.RoundOpened)
	}
	if len(t.SpellNotes) > 0 {
		m["spell_turn_effects"] = t.SpellNotes
	}
	if len(t.GMNotes) > 0 { // GM only: every route that returns reply() is GM-gated (TestPostgresMonsterEndAnchoredToSource, case D)
		m["gm_notices"] = t.GMNotes
	}
	if t.Survivor != nil {
		m["survivor_regen"] = t.Survivor
	}
	if opts, task := openLegendaryOptions(t.Lobby); len(opts) > 0 { // after the turn that ended (monster_features_2024.go)
		m["legendary_options"] = opts
		m["legendary_note"] = task
	}
	return m
}
