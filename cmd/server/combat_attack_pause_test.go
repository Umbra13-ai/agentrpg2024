package main

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"testing"
)

func TestPostgresCharacterHitPausesBeforeMastery(t *testing.T) {
	f := reviewConcentrationSetup(t)
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	masteryUses.Store(f.char, masteryUse{Name: "Sap", Weapon: "Invented mace", Melee: true})
	hp := monsterHPNow(f.camp, f.caster)
	// This tests the resolution of an already-judged hit, not the attack roll.
	o := attackOutcome{Weapon: "mace", Total: 100, Natural: 10, Judged: true, Hit: true, Roll: game.AttackRoll{AC: 1}, Damage: 4, DamageType: "force"}
	_, _, err := resolveAttackOnMonster(f.camp, f.char, f.caster, o)
	if err != nil {
		t.Fatal(err)
	}
	sap := func() bool {
		for _, e := range combatEffectsOn(f.camp, f.caster) {
			if e.Kind == "sap" {
				return true
			}
		}
		return false
	}
	if monsterConcentrationPending(f.camp, f.caster) == "" || monsterHPNow(f.camp, f.caster) != hp-4 || sap() {
		t.Fatal("the hit's mastery resolved after Concentration became pending")
	}
	work := pauseWaitingWork(t, f.camp)
	if code, j := f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	pauseResume(t, f, work)
	if !sap() || monsterHPNow(f.camp, f.caster) != hp-4 {
		t.Fatal("mastery was lost or damage repeated on resume")
	}
	if _, ok := thisTurnHitOnMonster(f.camp, f.caster); !ok {
		t.Fatal("resume did not record the completed hit")
	}
	pauseResume(t, f, work)
	count := 0
	for _, e := range combatEffectsOn(f.camp, f.caster) {
		if e.Kind == "sap" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("mastery repeated: %d", count)
	}
}

// Public route coverage retains random attack dice: a natural 1 can miss despite
// the fixture's high bonus, so a bounded retry is explicit. Save/pause assertions
// are deterministic once a hit happens.
func pauseCharacterAttackFixture(t *testing.T) (reviewConcentrationCase, string) {
	t.Helper()
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { m.AC = 1 })
	held := pauseAuraSource(t, f, "Attack held target")
	pgExec(t, `UPDATE characters SET species='Human' WHERE id=$1`, held)
	f.cast(t, held)
	pauseFailingConcentration(t, f)
	var equipped string
	if err := db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id=$1`, f.char).Scan(&equipped); err != nil {
		t.Fatal(err)
	}
	key, w, ok := findWeapon(equipped)
	if !ok || w.Mastery != "Vex" {
		t.Fatalf("fixture needs an equipped Vex weapon: %s %+v", equipped, w)
	}
	raw, err := json.Marshal([]string{key})
	if err != nil {
		t.Fatal(err)
	}
	pgExec(t, `UPDATE characters SET dex=100,weapon_masteries=$1::jsonb,action_used=false,attacks_remaining=NULL WHERE id=$2`, string(raw), f.char)
	_, err = changeFight(f.camp, func(fight *fight) error {
		for i, e := range fight.Order {
			if asInt(e["id"]) == f.char {
				fight.Index = i
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, equipped
}
func pauseCharacterAttack(t *testing.T, f reviewConcentrationCase, weapon string) (int, map[string]interface{}) {
	t.Helper()
	for i := 0; i < 40; i++ {
		pgExec(t, `UPDATE characters SET action_used=false,attacks_remaining=NULL WHERE id=$1`, f.char)
		code, j := pgCall(t, "POST", "/api/action", f.player, map[string]interface{}{"character_id": f.char, "action": "attack", "target_id": f.caster, "description": "I attack with my " + weapon})
		if monsterConcentrationPending(f.camp, f.caster) != "" {
			return code, j
		}
		if code != 200 {
			t.Fatalf("attack: %d %v", code, j)
		}
	}
	t.Fatal("no hit in 40 random attack attempts")
	return 0, nil
}
func TestPostgresCharacterHitPauseThroughActionRoute(t *testing.T) {
	f, weapon := pauseCharacterAttackFixture(t)
	hp := monsterHPNow(f.camp, f.caster)
	code, j := pauseCharacterAttack(t, f, weapon)
	if code != 200 || hp <= monsterHPNow(f.camp, f.caster) {
		t.Fatalf("damage: %d %v", code, j)
	}
	if _, ok := vexMarks.Load(f.char); ok {
		t.Fatal("Vex executed while the damage save was pending")
	}
	var used bool
	if err := db.QueryRow(`SELECT action_used FROM characters WHERE id=$1`, f.char).Scan(&used); err != nil || !used {
		t.Fatalf("attack cost not paid: %v %v", used, err)
	}
	after := monsterHPNow(f.camp, f.caster)
	work := pauseWaitingWork(t, f.camp)
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	pauseResume(t, f, work)
	v, ok := vexMarks.Load(f.char)
	if !ok || v.(vexMark).Target != f.caster || monsterHPNow(f.camp, f.caster) != after {
		t.Fatal("resume did not give the mastery without repeating the hit")
	}
	pauseResume(t, f, work)
	if err := db.QueryRow(`SELECT action_used FROM characters WHERE id=$1`, f.char).Scan(&used); err != nil || !used {
		t.Fatal("resume changed the already-spent action")
	}
}
func TestPostgresFailedAttackCostLeavesContinuationInterrupted(t *testing.T) {
	f, weapon := pauseCharacterAttackFixture(t)
	name := fmt.Sprintf("pause_attack_cost_%d", f.camp)
	// the cost fails only after a hit has opened its paused work: a miss (the helper rolls again) pays its cost as usual
	pgExec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF EXISTS (SELECT 1 FROM combat_paused_work WHERE lobby_id = %d) THEN RAISE EXCEPTION 'injected post-damage cost failure'; END IF;
		RETURN NEW; END $$`, name, f.camp))
	pgExec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE OF action_used ON characters FOR EACH ROW WHEN(OLD.id=%d AND NEW.action_used=true) EXECUTE FUNCTION %s()`, name, f.char, name))
	t.Cleanup(func() {
		pgExec(t, fmt.Sprintf(`DROP TRIGGER %s ON characters`, name))
		pgExec(t, fmt.Sprintf(`DROP FUNCTION %s()`, name))
	})
	code, j := pauseCharacterAttack(t, f, weapon)
	if code != 500 || j["error"] != "database_error" {
		t.Fatalf("failed cost was hidden: %d %v", code, j)
	}
	var work, status string
	if err := db.QueryRow(`SELECT id,status FROM combat_paused_work WHERE lobby_id=$1`, f.camp).Scan(&work, &status); err != nil || status != "capturing" {
		t.Fatalf("unfinished request became resumable: %s %v", status, err)
	}
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	if code, j = pgCall(t, "POST", "/api/gm/combat-resume", f.gm, map[string]interface{}{"campaign_id": f.camp, "resume_id": work}); code != 409 || j["error"] != "work_not_ready" {
		t.Fatalf("uncertain cost was silently skipped/replayed: %d %v", code, j)
	}
	if _, ok := vexMarks.Load(f.char); ok {
		t.Fatal("failed command executed its saved mastery")
	}
}

func TestPostgresMonsterMultiattackPausesPartsAndNextAttack(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Actions = append(m.Actions,
			srdMonsterEntry{Name: "Slam", Text: "Melee Attack Roll: Automatic hit, reach 5 ft. Hit: 4 Force damage plus 2 Radiant damage, and the target has the Frightened condition for 1 minute."},
			srdMonsterEntry{Name: "Multiattack", Text: "The creature makes two Slam attacks."})
	})
	f.cast(t, f.char)
	pauseFailingConcentration(t, f)
	code, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Sequence Attacker", "monster_key": f.key, "initiative": -200}}})
	if code != 200 {
		t.Fatalf("attacker: %d %v", code, j)
	}
	attacker := monsterIDsByKeyOrName(f.camp, "Sequence Attacker")
	if len(attacker) != 1 {
		t.Fatalf("attacker IDs: %v", attacker)
	}
	hp := monsterHPNow(f.camp, f.caster)
	code, j = pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "An invented sequence.", "monster_action": map[string]interface{}{"monster": fmt.Sprintf("#%d", attacker[0]), "action": "Multiattack", "target": fmt.Sprintf("#%d", f.caster)}})
	if code != 200 || monsterHPNow(f.camp, f.caster) != hp-4 || hasCondMarker(f.camp, f.caster, "frightened") {
		t.Fatalf("multiattack crossed the pause: %d %v hp=%d", code, j, monsterHPNow(f.camp, f.caster))
	}
	work := pauseWaitingWork(t, f.camp)
	if code, j = f.decide(t, "end"); code != 200 {
		t.Fatalf("end: %d %v", code, j)
	}
	pauseResume(t, f, work)
	if monsterHPNow(f.camp, f.caster) != hp-12 || !hasCondMarker(f.camp, f.caster, "frightened") {
		t.Fatalf("remaining hit/attack lost or repeated: %d", monsterHPNow(f.camp, f.caster))
	}
	pauseResume(t, f, work)
	if monsterHPNow(f.camp, f.caster) != hp-12 {
		t.Fatal("duplicate multiattack resume repeated damage")
	}
}
