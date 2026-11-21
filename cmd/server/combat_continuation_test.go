package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pauseStoreWork(t *testing.T, lobby int, status string, frames []combatFrame) string {
	t.Helper()
	id, err := newConcentrationToken()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(frames)
	if err != nil {
		t.Fatal(err)
	}
	pgExec(t, `INSERT INTO combat_paused_work(id,lobby_id,status,frames) VALUES($1,$2,$3,$4::jsonb)`, id, lobby, status, string(raw))
	return id
}
func TestPostgresCombatResumeScopeAndRecovery(t *testing.T) {
	f := reviewConcentrationSetup(t)
	addSpellEffect(f.camp, f.caster, 0, "Command", "commanded", "grovel", 1, "", 0)
	data, err := json.Marshal(spellStartTail{ID: f.caster, Name: "Review Focus Caster"})
	if err != nil {
		t.Fatal(err)
	}
	frames := []combatFrame{{Kind: "spell-turn-start-tail", Data: data}}
	id := pauseStoreWork(t, f.camp, "running", frames)
	body := map[string]interface{}{"campaign_id": f.camp, "resume_id": id}
	code, listed := pgCall(t, "GET", fmt.Sprintf("/api/gm/combat-resume?campaign_id=%d", f.camp), f.gm, nil)
	if code != 200 {
		t.Fatalf("list saved work: %d %v", code, listed)
	}
	work, ok := listed["work"].([]interface{})
	if !ok || len(work) != 1 || work[0].(map[string]interface{})["resume_id"] != id || work[0].(map[string]interface{})["status"] != "running" {
		t.Fatalf("saved work list: %v", listed)
	}
	if code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/combat-resume?campaign_id=%d", f.camp), f.player, nil); code != 403 || strings.Contains(fmt.Sprint(j), id) {
		t.Fatalf("player work list: %d %v", code, j)
	}

	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.player, body); code != 403 {
		t.Fatalf("player resume: %d %v", code, j)
	}
	other := reviewConcentrationSetup(t)
	foreign := map[string]interface{}{"campaign_id": other.camp, "resume_id": id}
	if code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/combat-resume?campaign_id=%d", other.camp), other.gm, nil); code != 200 || strings.Contains(fmt.Sprint(j), id) {
		t.Fatalf("another campaign's work list: %d %v", code, j)
	}

	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", other.gm, foreign); code != 409 || j["error"] != "work_not_found" {
		t.Fatalf("foreign work: %d %v", code, j)
	}
	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, body); code != 409 || j["error"] != "work_not_ready" || hasCondMarker(f.camp, f.caster, "prone") {
		t.Fatalf("interrupted work replayed: %d %v", code, j)
	}
	body["action"] = "reconcile"
	body["review_token"] = work[0].(map[string]interface{})["review_token"]
	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, body); code != 409 || j["error"] != "reconciliation_note_required" {
		t.Fatalf("silent reconciliation: %d %v", code, j)
	}
	foreign["action"] = "reconcile"
	foreign["review_token"] = body["review_token"]
	foreign["reconciliation_note"] = "This is not my campaign's work."
	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", other.gm, foreign); code != 409 || j["error"] != "work_not_interrupted" {
		t.Fatalf("foreign reconciliation: %d %v", code, j)
	}
	// Reconcile is an explicit acknowledgment after correction, never execution of
	// the uncertain step. A real correction uses the condition owner through GM.
	if code, j := pgCall(t, "POST", "/api/gm/conditions", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "condition": "prone", "source": "GM interrupted-turn correction"}); code != 200 {
		t.Fatalf("GM correction: %d %v", code, j)
	}
	if code, j := pgCall(t, "DELETE", "/api/gm/spell-effect", f.gm, map[string]interface{}{"campaign_id": f.camp, "target_id": f.caster, "spell": "Command"}); code != 200 {
		t.Fatalf("GM timer correction: %d %v", code, j)
	}
	body["reconciliation_note"] = "Reviewed the interrupted start of turn and applied Grovel's Prone condition with the GM condition editor and removed its consumed Command effect. The saved step must not repeat."
	before := monsterHPNow(f.camp, f.caster)
	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, body); code != 200 || j["replayed"] != false {
		t.Fatalf("reconciliation: %d %v", code, j)
	}
	var status, note string
	if err = db.QueryRow(`SELECT status,reconciliation_note FROM combat_paused_work WHERE id=$1 AND lobby_id=$2`, id, f.camp).Scan(&status, &note); err != nil || status != "reconciled" || note == "" || monsterHPNow(f.camp, f.caster) != before {
		t.Fatalf("reconciliation persistence: %s %s %v", status, note, err)
	}
	if paused, err := hasPausedCombatWork(f.camp); err != nil || paused {
		t.Fatalf("reconciled work still blocks: %v %v", paused, err)
	}
	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, body); code != 409 {
		t.Fatalf("duplicate reconciliation accepted: %d %v", code, j)
	}
	if code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/combat-resume?campaign_id=%d", f.camp), f.gm, nil); code != 200 || strings.Contains(fmt.Sprint(j), id) {
		t.Fatalf("reconciled work still listed as active: %d %v", code, j)
	}
	// An unrecognized compiled kind is refused before claiming or executing it.
	unknown := pauseStoreWork(t, f.camp, "ready", []combatFrame{{Kind: "unknown-operation", Data: json.RawMessage(`{}`)}})
	if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, map[string]interface{}{"campaign_id": f.camp, "resume_id": unknown}); code != 409 || j["error"] != "unsupported_work" {
		t.Fatalf("unknown operation: %d %v", code, j)
	}
}

// Owner-level double-submit control, deliberately outside the request lease.
// A row lock forces both callers to read ready before either may claim it; the
// conditional claim must permit exactly one handler invocation.
func TestPostgresCombatContinuationConcurrentClaim(t *testing.T) {
	f := reviewConcentrationSetup(t)
	kind := "test-concurrent-claim"
	var calls atomic.Int32
	combatResumeHandlers[kind] = func(int, json.RawMessage) (map[string]interface{}, error) {
		calls.Add(1)
		return map[string]interface{}{"finished": true}, nil
	}
	t.Cleanup(func() { delete(combatResumeHandlers, kind) })
	id := pauseStoreWork(t, f.camp, "ready", []combatFrame{{Kind: kind, Data: json.RawMessage(`{}`)}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT id FROM combat_paused_work WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		code string
		err  error
	}
	out := make(chan answer, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, code, err := resumeCombatWork(f.camp, id); out <- answer{code, err} }()
	}
	// Both statements must be waiting on our row, proving that both passed the
	// earlier SELECT. Poll the database lock state, with a bounded test deadline.
	waitForCombatClaims(t, id, 2)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(out)
	good, busy := 0, 0
	for a := range out {
		if a.err != nil {
			t.Fatal(a.err)
		}
		if a.code == "" {
			good++
		} else if a.code == "work_not_ready" {
			busy++
		} else {
			t.Fatalf("unexpected claim: %s", a.code)
		}
	}
	if good != 1 || busy != 1 || calls.Load() != 1 {
		t.Fatalf("claims: good=%d busy=%d invocations=%d", good, busy, calls.Load())
	}
	if _, code, err := resumeCombatWork(f.camp, id); err != nil || code != "" || calls.Load() != 1 {
		t.Fatalf("completed retry: %s %v invocations=%d", code, err, calls.Load())
	}
}

func waitForCombatClaims(t *testing.T, id string, want int) {
	t.Helper()
	// id is a server-generated random hex token; PostgreSQL exposes parameterized
	// text, so identify the shared UPDATE and count blocked backends in this DB.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE 'UPDATE combat_paused_work SET status=%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("the concurrent claims for %s did not reach the controlled lock", id))
}
