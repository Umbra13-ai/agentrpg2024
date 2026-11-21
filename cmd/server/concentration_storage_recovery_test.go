package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

func journalInterruptedTurn(t *testing.T) (reviewConcentrationCase, map[string]interface{}, int) {
	t.Helper()
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.Resistances = "" })
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	addSpellEffect(f.camp, f.caster, 0, "Command", "commanded", "grovel", 1, "", 0)
	startBurning(f.camp, f.caster, "invented interrupted-turn probe")
	pauseCasterTurn(t, f, true)
	f.rejectStorage(t, "UPDATE")
	hp := monsterHPNow(f.camp, f.caster)
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp), f.gm, map[string]interface{}{})
	if code != 500 || j["error"] != "database_error" {
		t.Fatalf("interruption: %d %v", code, j)
	}
	after := monsterHPNow(f.camp, f.caster)
	if after >= hp || after < hp-4 || hasCondMarker(f.camp, f.caster, "prone") {
		t.Fatal("damage control or dependent boundary failed")
	}
	code, j = pgCall(t, "GET", fmt.Sprintf("/api/gm/combat-resume?campaign_id=%d", f.camp), f.gm, nil)
	if code != 200 {
		t.Fatalf("read interruption: %d %v", code, j)
	}
	work, ok := j["work"].([]interface{})
	if !ok || len(work) != 1 {
		t.Fatalf("missing journal: %v", j)
	}
	entry := work[0].(map[string]interface{})
	if entry["status"] != "running" || entry["review_token"] == "" {
		t.Fatalf("interrupted status/token: %v", entry)
	}
	facts := entry["context"].(map[string]interface{})
	raw, err := json.Marshal(facts["save_result"])
	if err != nil {
		t.Fatal(err)
	}
	var save game.SaveResult
	if err = json.Unmarshal(raw, &save); err != nil {
		t.Fatal(err)
	}
	if save.Success || save.Save.Bonus != -10 || save.Save.DC != 10 || save.Total != save.Save.Judge(save.Natural).Total {
		t.Fatalf("original save facts were lost: %s", raw)
	}
	if asInt(facts["target_id"]) != f.caster || facts["cast_token"] == "" || asInt(facts["hp_before"]) != hp || asInt(facts["damage"]) != hp-after {
		t.Fatalf("wrong persisted event facts: %v", facts)
	}
	// Restore storage without deleting the fixture's cleanup trigger.
	pgExec(t, fmt.Sprintf(`ALTER TABLE monster_concentration DISABLE TRIGGER review_concentration_%d`, f.camp))
	return f, entry, after
}
func journalCastToken(t *testing.T, f reviewConcentrationCase) string {
	t.Helper()
	code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/monster-concentration?campaign_id=%d", f.camp), f.gm, nil)
	if code != 200 {
		t.Fatalf("read cast: %d %v", code, j)
	}
	casts, ok := j["casts"].([]interface{})
	if !ok || len(casts) != 1 {
		t.Fatalf("active cast list: %v", j)
	}
	cast := casts[0].(map[string]interface{})
	if asInt(cast["combatant_id"]) != f.caster {
		t.Fatalf("wrong cast: %v", cast)
	}
	token, ok := cast["cast_token"].(string)
	if !ok || token == "" {
		t.Fatalf("missing cast identity: %v", cast)
	}
	return token
}
func TestPostgresConcentrationStorageRecoveryWorkflow(t *testing.T) {
	for _, decision := range []string{"end_cast", "legendary_resistance"} {
		t.Run(decision, func(t *testing.T) {
			f, work, hp := journalInterruptedTurn(t)
			id := work["resume_id"].(string)
			for _, path := range []string{"combat-resume", "monster-concentration"} {
				if code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/%s?campaign_id=%d", path, f.camp), f.player, nil); code != 403 || strings.Contains(fmt.Sprint(j), id) {
					t.Fatalf("private recovery read: %d %v", code, j)
				}
			}
			other := reviewConcentrationSetup(t)
			if code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/combat-resume?campaign_id=%d", other.camp), other.gm, nil); code != 200 || strings.Contains(fmt.Sprint(j), id) {
				t.Fatalf("foreign journal: %d %v", code, j)
			}
			token := journalCastToken(t, f)
			body := map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster, "decision": "end_cast", "cast_token": "wrong-cast"}
			if code, j := pgCall(t, "POST", "/api/gm/monster-concentration", f.gm, body); code != 409 || j["error"] != "stale_cast_token" {
				t.Fatalf("stale cast: %d %v", code, j)
			}
			body["cast_token"] = token
			if code, j := pgCall(t, "POST", "/api/gm/monster-concentration", f.player, body); code != 403 {
				t.Fatalf("player cast edit: %d %v", code, j)
			}
			foreign := map[string]interface{}{"campaign_id": other.camp, "combatant_id": f.caster, "decision": "end_cast", "cast_token": token}
			if code, j := pgCall(t, "POST", "/api/gm/monster-concentration", other.gm, foreign); code != 409 || j["error"] != "stale_cast_token" {
				t.Fatalf("foreign cast edit: %d %v", code, j)
			}
			left := legendaryResistanceLeft(f.camp, f.caster)
			if decision == "end_cast" {
				if code, j := pgCall(t, "POST", "/api/gm/monster-concentration", f.gm, body); code != 200 {
					t.Fatalf("end observed cast: %d %v", code, j)
				}
				if monsterConcentrationOn(f.camp, f.caster) != "" || f.held(f.char) != 0 {
					t.Fatal("end did not remove recorded cast effects")
				}
			} else {
				if code, j := pgCall(t, "POST", "/api/gm/legendary-resistance", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster}); code != 200 {
					t.Fatalf("manual stored-failure correction: %d %v", code, j)
				}
				if monsterConcentrationOn(f.camp, f.caster) == "" || f.held(f.char) == 0 || legendaryResistanceLeft(f.camp, f.caster) != left-1 {
					t.Fatal("GM resistance correction did not retain cast/spend one")
				}
			}
			recover := map[string]interface{}{"campaign_id": f.camp, "resume_id": id, "action": "reconcile_and_resume", "reconciliation_note": "Reviewed persisted failed save and committed Burning damage; corrected Concentration with the GM cast/resistance tool. Costs and logs reviewed. Continue only the saved remainder."}
			if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, recover); code != 409 || j["error"] != "review_token_required" {
				t.Fatalf("missing review: %d %v", code, j)
			}
			recover["review_token"] = "wrong-review"
			if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, recover); code != 409 || j["error"] != "stale_review_token" {
				t.Fatalf("stale review: %d %v", code, j)
			}
			recover["review_token"] = work["review_token"]
			if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, recover); code != 200 {
				t.Fatalf("reviewed resume: %d %v", code, j)
			}
			if monsterHPNow(f.camp, f.caster) != hp || !hasCondMarker(f.camp, f.caster, "prone") {
				t.Fatal("recovery repeated damage or lost Command")
			}
			if paused, err := pendingCombatDecision(f.camp); err != nil || paused {
				t.Fatalf("recovery did not release boundary: %v %v", paused, err)
			}
			if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, recover); code != 409 {
				t.Fatalf("duplicate review accepted: %d %v", code, j)
			}
			if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.gm, map[string]interface{}{"campaign_id": f.camp, "resume_id": id}); code != 200 || monsterHPNow(f.camp, f.caster) != hp {
				t.Fatalf("completed retry: %d %v", code, j)
			}
			if legendaryResistanceLeft(f.camp, f.caster) != left-boolInt(decision == "legendary_resistance") {
				t.Fatal("recovery spent another resistance use")
			}
		})
	}
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// This is an owner-level race independent of the HTTP campaign lease. The row
// lock makes two callers observe one interrupted attempt before either claims.
func TestPostgresCombatReconciliationConcurrentReview(t *testing.T) {
	f := reviewConcentrationSetup(t)
	id := pauseStoreWork(t, f.camp, "running", []combatFrame{})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT id FROM combat_paused_work WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	results := make(chan string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, e := reconcileCombatWork(f.camp, id, "Reviewed/corrected uncertain step.", "legacy:"+id, true)
			if e != nil {
				results <- e.Error()
				return
			}
			if code == "" {
				successes.Add(1)
			}
			results <- code
		}()
	}
	waitForCombatClaims(t, id, 2)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(results)
	refused := 0
	for code := range results {
		if code == "stale_review_token" {
			refused++
		} else if code != "" {
			t.Fatalf("unexpected concurrent review: %s", code)
		}
	}
	if successes.Load() != 1 || refused != 1 {
		t.Fatalf("reviews: accepted=%d refused=%d", successes.Load(), refused)
	}
	// Simulate a new interrupted attempt on the same work. An old review cannot
	// acknowledge it even though the id and running status are the same again.
	pgExec(t, `UPDATE combat_paused_work SET status='running' WHERE id=$1`, id)
	if code, err := reconcileCombatWork(f.camp, id, "old retry", "legacy:"+id, true); err != nil || code != "stale_review_token" {
		t.Fatalf("old review acknowledged new interruption: %s %v", code, err)
	}
}

type journalRestartFixture struct {
	GM, Work, Review, Cast string
	Lobby, Caster, HP      int
}

func TestPostgresConcentrationJournalFreshProcess(t *testing.T) {
	testPostgres(t)
	if path := os.Getenv("JOURNAL_RESTART_FIXTURE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f journalRestartFixture
		if err = json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		if paused, err := pendingCombatDecision(f.Lobby); err != nil || !paused {
			t.Fatalf("cold process lost boundary: %v %v", paused, err)
		}
		if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.GM, map[string]interface{}{"campaign_id": f.Lobby, "resume_id": f.Work}); code != 409 || j["error"] != "work_not_ready" {
			t.Fatalf("cold automatic replay: %d %v", code, j)
		}
		if code, j := pgCall(t, "POST", "/api/gm/monster-concentration", f.GM, map[string]interface{}{"campaign_id": f.Lobby, "combatant_id": f.Caster, "decision": "end_cast", "cast_token": f.Cast}); code != 200 {
			t.Fatalf("cold settle: %d %v", code, j)
		}
		if code, j := pgCall(t, "POST", "/api/gm/combat-resume", f.GM, map[string]interface{}{"campaign_id": f.Lobby, "resume_id": f.Work, "action": "reconcile_and_resume", "review_token": f.Review, "reconciliation_note": "Reviewed committed damage, stored failed save and original costs/log; ended cast and continue known remainder."}); code != 200 {
			t.Fatalf("cold recovery: %d %v", code, j)
		}
		if monsterHPNow(f.Lobby, f.Caster) != f.HP || !hasCondMarker(f.Lobby, f.Caster, "prone") {
			t.Fatal("cold recovery repeated damage or lost remainder")
		}
		return
	}
	f, work, hp := journalInterruptedTurn(t)
	fixture := journalRestartFixture{f.gm, work["resume_id"].(string), work["review_token"].(string), journalCastToken(t, f), f.camp, f.caster, hp}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "journal-restart.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPostgresConcentrationJournalFreshProcess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "JOURNAL_RESTART_FIXTURE="+path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cold journal recovery: %v\n%s", err, output)
	}
	if paused, err := pendingCombatDecision(f.camp); err != nil || paused {
		t.Fatalf("cold recovery remained paused: %v %v", paused, err)
	}
}
