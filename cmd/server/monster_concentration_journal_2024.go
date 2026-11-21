package main

// A tracked cast's obligation must survive the gap between a committed HP or
// condition change and its later Concentration write. This journal joins the
// state owner's transaction in the existing application database. It does not
// decide a saving throw or replay a possibly completed step.
import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type concentrationJournal struct {
	ID, Event    string
	Owned, Fault bool
}

var concentrationJournals = struct {
	sync.Mutex
	byGoroutine map[uint64]map[int]concentrationJournal
}{byGoroutine: map[uint64]map[int]concentrationJournal{}}

func currentConcentrationJournal(lobby int) concentrationJournal {
	concentrationJournals.Lock()
	defer concentrationJournals.Unlock()
	return concentrationJournals.byGoroutine[goroutineID()][lobby]
}
func keepConcentrationJournal(lobby int, j concentrationJournal) {
	concentrationJournals.Lock()
	defer concentrationJournals.Unlock()
	gid := goroutineID()
	if concentrationJournals.byGoroutine[gid] == nil {
		concentrationJournals.byGoroutine[gid] = map[int]concentrationJournal{}
	}
	concentrationJournals.byGoroutine[gid][lobby] = j
}
func forgetConcentrationJournal(lobby int) {
	concentrationJournals.Lock()
	defer concentrationJournals.Unlock()
	gid := goroutineID()
	delete(concentrationJournals.byGoroutine[gid], lobby)
	if len(concentrationJournals.byGoroutine[gid]) == 0 {
		delete(concentrationJournals.byGoroutine, gid)
	}
}
func forgetRequestConcentrationJournals() {
	concentrationJournals.Lock()
	defer concentrationJournals.Unlock()
	delete(concentrationJournals.byGoroutine, goroutineID())
}
func requestHasDBFailure() bool {
	if w := watchOf(); w != nil {
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.errors) > 0
	}
	return false
}

// An operation may ignore only its own healthy running journal. Other requests,
// an interrupted operation, a pending choice and ready saved work still block.
func healthyCombatWorkID(lobby int) string {
	j := currentConcentrationJournal(lobby)
	if j.Fault || requestHasDBFailure() {
		return ""
	}
	return currentCombatFrames(lobby).Job
}
func hasBlockingCombatWork(lobby int) (bool, error) {
	var yes bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM combat_paused_work WHERE lobby_id=$1 AND status IN ('capturing','waiting','ready','running') AND id<>$2)`, lobby, healthyCombatWorkID(lobby)).Scan(&yes)
	return yes, err
}

// Invoked only through the caller's existing HP/condition transaction. If this
// insert/update fails, that triggering state change must roll back with it.
func journalMonsterConcentrationIn(tx *sql.Tx, lobby, target int, facts map[string]interface{}) error {
	if lobby <= 0 || target >= 0 {
		return nil
	}
	var spell, cast string
	err := tx.QueryRow(`SELECT spell,cast_token FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, lobby, target).Scan(&spell, &cast)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if spell == "" {
		return nil
	}
	scope := currentCombatFrames(lobby)
	j := currentConcentrationJournal(lobby)
	fresh := j.ID == "" && scope.Job == ""
	if j.ID == "" {
		j.ID = scope.Job
		j.Owned = fresh
	}
	if fresh {
		j.ID, err = newConcentrationToken()
		if err != nil {
			return err
		}
	}
	j.Event, err = newConcentrationToken()
	if err != nil {
		return err
	}
	facts["event_token"], facts["target_id"], facts["spell"], facts["cast_token"] = j.Event, target, spell, cast
	context, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	frames := scope.Frames
	if frames == nil {
		frames = []combatFrame{}
	}
	raw, err := json.Marshal(frames)
	if err != nil {
		return err
	}
	if fresh {
		_, err = tx.Exec(`INSERT INTO combat_paused_work(id,lobby_id,status,frames,context,review_token) VALUES($1,$2,'running',$3::jsonb,$4::jsonb,$5)`, j.ID, lobby, string(raw), string(context), j.Event)
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE combat_paused_work SET frames=$3::jsonb,context=$4::jsonb,review_token=$5 WHERE id=$1 AND lobby_id=$2 AND status='running'`, j.ID, lobby, string(raw), string(context), j.Event)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				err = fmt.Errorf("the earlier combat step is not ready for further automatic state changes")
			}
		}
	}
	if err != nil {
		return err
	}
	keepConcentrationJournal(lobby, j)
	noteCapturedCombatWork(lobby, j.ID)
	return nil
}

func rememberJournalConcentrationSave(lobby int, save game.SaveResult) error {
	j := currentConcentrationJournal(lobby)
	if j.ID == "" || j.Event == "" {
		return nil
	}
	raw, err := json.Marshal(save)
	if err != nil {
		return err
	}
	result, err := db.Exec(`UPDATE combat_paused_work SET context=jsonb_set(context,'{save_result}',$3::jsonb) WHERE id=$1 AND lobby_id=$2 AND context->>'event_token'=$4 AND status='running'`, j.ID, lobby, string(raw), j.Event)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("the damage event changed before its Concentration save was stored")
	}
	return nil
}
func markConcentrationJournalFault(lobby int) {
	j := currentConcentrationJournal(lobby)
	if j.ID == "" {
		return
	}
	j.Fault = true
	keepConcentrationJournal(lobby, j)
	// Later known frames (notably a character hit's tail) may have been armed
	// after the HP write. Preserve them if storage is available; the already-
	// committed running row remains the boundary even if this write also fails.
	scope := currentCombatFrames(lobby)
	if len(scope.Frames) > 0 {
		raw, err := json.Marshal(scope.Frames)
		if err == nil {
			_, err = db.Exec(`UPDATE combat_paused_work SET frames=$3::jsonb WHERE id=$1 AND lobby_id=$2 AND status='running'`, j.ID, lobby, string(raw))
		}
		if err != nil {
			log.Printf("persist interrupted Concentration frame: %v", err)
		}
	}
}

// A nested condition/HP helper cannot close the outer operation's obligation.
func directConcentrationJournalScope(lobby int) func() {
	root := currentConcentrationJournal(lobby).ID == ""
	return func() {
		if root {
			finishDirectConcentrationJournal(lobby)
		}
	}
}
func finishDirectConcentrationJournal(lobby int) {
	if watchOf() != nil {
		return
	}
	scope := currentCombatFrames(lobby)
	if len(scope.Frames) > 0 {
		return
	}
	j := currentConcentrationJournal(lobby)
	if !j.Owned {
		return
	}
	if !j.Fault {
		finishCapturedCombatWork(lobby, j.ID)
	}
	forgetConcentrationJournal(lobby)
}
