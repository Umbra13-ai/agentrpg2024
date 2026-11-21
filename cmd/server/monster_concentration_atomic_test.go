package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func concentrationDecisionToken(t *testing.T, f reviewConcentrationCase) string {
	t.Helper()
	var token string
	if err := db.QueryRow(`SELECT decision_token FROM monster_concentration WHERE lobby_id=$1 AND monster_id=$2`, f.camp, f.caster).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("pending decision has no token")
	}
	return token
}

// Both requests echo the same failure's token. A queued failure deliberately
// remains, so a duplicate must not spend another use against that next failure.
func TestPostgresConcentrationConcurrentDecision(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	left := legendaryResistanceLeft(f.camp, f.caster)
	gate := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			code, _ := pgCall(t, "POST", "/api/gm/monster-concentration", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster, "decision": "legendary_resistance", "decision_token": token})
			codes <- code
		}()
	}
	close(gate)
	wg.Wait()
	close(codes)
	successes, refused := 0, 0
	for code := range codes {
		if code == 200 {
			successes++
		} else if code == 400 || code == 409 {
			refused++
		} else {
			t.Fatalf("unexpected status %d", code)
		}
	}
	if successes != 1 || refused != 1 || legendaryResistanceLeft(f.camp, f.caster) != left-1 {
		t.Fatalf("double decision: successes=%d refusals=%d left=%d (was %d)", successes, refused, legendaryResistanceLeft(f.camp, f.caster), left)
	}
	next := concentrationDecisionToken(t, f)
	if next == token {
		t.Fatal("next failure reused the old token")
	}
	code, _ := pgCall(t, "POST", "/api/gm/monster-concentration", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster, "decision": "end", "decision_token": token})
	if code != 400 || monsterConcentrationOn(f.camp, f.caster) == "" {
		t.Fatal("a stale end decision ended the next failure")
	}
	code, _ = pgCall(t, "POST", "/api/gm/monster-concentration", f.gm, map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster, "decision": "end", "decision_token": next})
	if code != 200 || monsterConcentrationOn(f.camp, f.caster) != "" || f.held(f.char) != 0 {
		t.Fatal("current-token end did not clean up")
	}
}

func TestPostgresConcentrationResistanceWriteRollback(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	left := legendaryResistanceLeft(f.camp, f.caster)
	name := fmt.Sprintf("review_resistance_%d", f.camp)
	pgExec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected resistance failure'; END $$`, name))
	pgExec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON combat_monster_state FOR EACH ROW WHEN(OLD.lobby_id=%d AND OLD.monster_id=%d) EXECUTE FUNCTION %s()`, name, f.camp, f.caster, name))
	t.Cleanup(func() {
		pgExec(t, fmt.Sprintf(`DROP TRIGGER %s ON combat_monster_state`, name))
		pgExec(t, fmt.Sprintf(`DROP FUNCTION %s()`, name))
	})
	code, _ := f.decide(t, "legendary_resistance")
	if code != 500 || concentrationDecisionToken(t, f) != token || legendaryResistanceLeft(f.camp, f.caster) != left || f.held(f.char) == 0 {
		t.Fatalf("failed spend did not retain the original decision and effects: status=%d", code)
	}
}

func TestPostgresConcentrationLegacyQueueIsExplicit(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	pgExec(t, `UPDATE monster_concentration SET queue='30,30' WHERE lobby_id=$1 AND monster_id=$2`, f.camp, f.caster)
	left := legendaryResistanceLeft(f.camp, f.caster)
	code, j := f.decide(t, "legendary_resistance")
	if code != 400 || j["error"] != "queue_needs_adjudication" || legendaryResistanceLeft(f.camp, f.caster) != left || f.held(f.char) == 0 {
		t.Fatalf("legacy queue was lost or guessed: %d %v", code, j)
	}
	if code, j = f.decide(t, "end"); code != 200 || f.held(f.char) != 0 {
		t.Fatalf("explicit end unavailable: %d %v", code, j)
	}
}

func TestPostgresConcentrationCleanupRollback(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	name := fmt.Sprintf("review_condition_cleanup_%d", f.camp)
	pgExec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected cleanup failure'; END $$`, name))
	pgExec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON condition_records FOR EACH ROW WHEN(OLD.lobby_id=%d AND OLD.source_id=%d) EXECUTE FUNCTION %s()`, name, f.camp, f.caster, name))
	t.Cleanup(func() {
		pgExec(t, fmt.Sprintf(`DROP TRIGGER %s ON condition_records`, name))
		pgExec(t, fmt.Sprintf(`DROP FUNCTION %s()`, name))
	})
	code, _ := f.decide(t, "end")
	if code != 500 || concentrationDecisionToken(t, f) != token || f.held(f.char) == 0 {
		t.Fatalf("failed condition cleanup partially ended the cast: status=%d", code)
	}
}

func TestPostgresConcentrationShieldedAndBlockedCast(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	enemy := monsterIDsByKeyOrName(f.camp, "Opponent")[0]
	cast := func(target int) {
		t.Helper()
		code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "Shielded spell.", "monster_action": map[string]interface{}{"monster": "Review Focus Caster", "action": "Spellcasting", "use": "Hold Monster", "target_ids": []int{target}}})
		if code != 200 {
			t.Fatalf("cast: %d %v", code, j)
		}
	}
	// A field centered on another monster shields that target but not the caster.
	addSpellEffect(f.camp, enemy, enemy, "Antimagic Field", "antimagic_field", "", 100, "", 0)
	cast(enemy)
	if monsterConcentrationOn(f.camp, f.caster) != "Hold Monster" || f.held(f.char) != 0 || len(loadConditionRecords(f.camp, enemy)) != 0 {
		t.Fatal("shielded cast did not replace old concentration without affecting the shielded target")
	}
	before, err := readMonsterConcentration(db, f.camp, f.caster)
	if err != nil {
		t.Fatal(err)
	}
	// A caster inside the field cannot begin a new cast at all.
	addSpellEffect(f.camp, f.caster, f.caster, "Antimagic Field", "antimagic_field", "", 100, "", 0)
	cast(f.char)
	after, err := readMonsterConcentration(db, f.camp, f.caster)
	if err != nil {
		t.Fatal(err)
	}
	if after.CastToken != before.CastToken || f.held(f.char) != 0 {
		t.Fatal("a blocked caster replaced concentration or applied a condition")
	}
}

func TestPostgresConcentrationDecisionThroughResistanceRoute(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	left := legendaryResistanceLeft(f.camp, f.caster)
	body := map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster, "decision_token": token}
	code, j := pgCall(t, "POST", "/api/gm/legendary-resistance", f.gm, body)
	if code != 200 || legendaryResistanceLeft(f.camp, f.caster) != left-1 {
		t.Fatalf("alternate decision route: %d %v", code, j)
	}
	next := concentrationDecisionToken(t, f)
	code, j = pgCall(t, "POST", "/api/gm/legendary-resistance", f.gm, body)
	if code != 400 || legendaryResistanceLeft(f.camp, f.caster) != left-1 || concentrationDecisionToken(t, f) != next {
		t.Fatalf("alternate route reused an old decision: %d %v", code, j)
	}
}

func TestPostgresConcentrationMissingTokenSpendsNothing(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	left := legendaryResistanceLeft(f.camp, f.caster)
	for _, path := range []string{"/api/gm/monster-concentration", "/api/gm/legendary-resistance"} {
		body := map[string]interface{}{"campaign_id": f.camp, "combatant_id": f.caster}
		if path == "/api/gm/monster-concentration" {
			body["decision"] = "legendary_resistance"
		}
		code, j := pgCall(t, "POST", path, f.gm, body)
		if code != 400 || j["error"] != "decision_token_required" || concentrationDecisionToken(t, f) != token || legendaryResistanceLeft(f.camp, f.caster) != left {
			t.Fatalf("token-less decision was not refused without spending: %s %d %v", path, code, j)
		}
	}
}

// The GM can obtain the required token over its real private API. A random
// token is used as the audience marker, with a positive GM-delivery control.
func TestPostgresConcentrationPrivateTokenWorkflow(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/notices?campaign_id=%d", f.camp), f.gm, nil)
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if code != 200 || !strings.Contains(string(raw), token) {
		t.Fatalf("GM did not receive the pending token through notices: status=%d", code)
	}
	code, j = pgCall(t, "GET", fmt.Sprintf("/api/gm/notices?campaign_id=%d", f.camp), f.player, nil)
	raw, err = json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if code != 403 || strings.Contains(string(raw), token) {
		t.Fatal("player could read a private decision")
	}
	code, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", f.char), f.player, nil)
	raw, err = json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if code != 200 || strings.Contains(string(raw), token) {
		t.Fatalf("token leaked or player surface unavailable: status=%d", code)
	}
}

// The persistence owner is tested without the HTTP command lease too, so the
// route exclusion cannot hide a broken decision-token claim in the transaction.
func TestPostgresConcentrationConcurrentOwnerDecision(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	f.damage(t)
	f.damage(t)
	token := concentrationDecisionToken(t, f)
	gate := make(chan struct{})
	codes := make(chan string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, code := resolveMonsterConcentration(f.camp, f.caster, "legendary_resistance", token)
			codes <- code
		}()
	}
	close(gate)
	wg.Wait()
	close(codes)
	used, refused := 0, 0
	for code := range codes {
		if code == "" {
			used++
		} else if code == "nothing_pending" {
			refused++
		} else {
			t.Errorf("unexpected owner result %s", code)
		}
	}
	if used != 1 || refused != 1 || legendaryResistanceLeft(f.camp, f.caster) != 2 {
		t.Fatalf("owner double spend: used=%d refused=%d left=%d", used, refused, legendaryResistanceLeft(f.camp, f.caster))
	}
}
