package main

import (
	"fmt"
	"strings"
	"testing"
)

func ironGolemEncounter(t *testing.T) reviewConcentrationCase {
	t.Helper()
	original := suppliedIronGolem(t)
	return reviewConcentrationSetup(t, func(m *srdMonster2024) { *m = original; m.Name = "Review Focus Caster" })
}

func ironGolemAction(t *testing.T, f reviewConcentrationCase, action string, fields map[string]interface{}) map[string]interface{} {
	t.Helper()
	fields["monster"], fields["action"] = "Review Focus Caster", action
	code, reply := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "Reference combat check.", "monster_action": fields})
	if !narrateAnswered(code, reply) {
		t.Fatalf("%s: %d %v", action, code, reply)
	}
	return reply
}

func ironGolemMap(t *testing.T, f reviewConcentrationCase, positions map[int]int) {
	t.Helper()
	path := fmt.Sprintf("/api/campaigns/%d/battle-map", f.camp)
	code, state := pgCall(t, "GET", path, f.gm, nil)
	if code != 200 && (code != 404 || state["error"] != "no_battle_map") {
		t.Fatalf("read map: %d %v", code, state)
	}
	tokens := []mapToken{{ID: f.caster, Name: "Review Focus Caster", Position: mapSquare{0, 0}, Size: 1}}
	for id, x := range positions {
		tokens = append(tokens, mapToken{ID: id, Name: creatureName(f.camp, id), Position: mapSquare{x, 0}, Size: 1})
	}
	body := map[string]interface{}{"map": encounterMap{Grid: []string{strings.Repeat(".", 30)}, Tokens: tokens}, "forced_movement": true}
	if rev, ok := state["revision"].(string); ok {
		body["confirmation"] = rev
	}
	if code, reply := pgCall(t, "PUT", path, f.gm, body); code != 200 {
		t.Fatalf("put map: %d %v", code, reply)
	}
}

func ironGolemHeroHP(t *testing.T, f reviewConcentrationCase) int {
	t.Helper()
	var hp int
	if err := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, f.char).Scan(&hp); err != nil {
		t.Fatal(err)
	}
	return hp
}

func TestPostgresReferenceIronGolemAttacksAndMultiattackChoice(t *testing.T) {
	f := ironGolemEncounter(t)
	ironGolemMap(t, f, map[int]int{f.char: 2}) // a 10-foot reach control
	target := fmt.Sprint(f.char)
	play := func(action string) map[string]interface{} {
		return ironGolemAction(t, f, action, map[string]interface{}{"target": target})
	}
	before := ironGolemHeroHP(t, f)
	choice := play("Multiattack")
	if ironGolemHeroHP(t, f) != before || !strings.Contains(fmt.Sprint(choice["monster_action_result"]), "choice between attacks") {
		t.Fatalf("unselected Multiattack guessed or applied a choice: %v", choice)
	}
	// Complete the documented GM workflow with all four ordered choices. The
	// server resolves exactly one attack each time, including legal misses.
	for _, pair := range [][2]string{{"Bladed Arm", "Bladed Arm"}, {"Bladed Arm", "Fiery Bolt"}, {"Fiery Bolt", "Bladed Arm"}, {"Fiery Bolt", "Fiery Bolt"}} {
		pgExec(t, `UPDATE characters SET hp=500,ac=1 WHERE id=$1`, f.char)
		for _, action := range pair {
			hp := ironGolemHeroHP(t, f)
			reply := play(action)
			receipt, ok := reply["monster_attack"].(map[string]interface{})
			damage := hp - ironGolemHeroHP(t, f)
			if !ok || asInt(receipt["attacks"]) != 1 || asInt(receipt["damage"]) != damage || !strings.Contains(fmt.Sprint(reply["monster_action_result"]), action) {
				t.Fatalf("incorrect attack receipt: %v (HP loss %d)", reply, damage)
			}
			result := fmt.Sprint(reply["monster_action_result"])
			_, rollText, found := strings.Cut(result, action+": ")
			var rollTotal int
			if n, _ := fmt.Sscan(rollText, &rollTotal); !found || n != 1 {
				t.Fatalf("missing %s roll: %s", action, result)
			}
			bonus := 10
			if action == "Bladed Arm" {
				bonus = 12
			}
			natural := rollTotal - bonus // AC 1 and this fixture has no attack adjustment
			if natural < 1 || natural > 20 || ((asInt(receipt["hits"]) == 0) != (natural == 1)) {
				t.Fatalf("incorrect outcome at AC 1: %s", result)
			}
			if natural == 1 {
				if damage != 0 {
					t.Fatalf("miss dealt %d damage", damage)
				}
				continue
			}
			minDamage, maxDamage := 8, 64
			if natural == 20 {
				minDamage, maxDamage = 16, 128
			}
			if action == "Bladed Arm" {
				minDamage, maxDamage = 13, 49
				if natural == 20 {
					minDamage, maxDamage = 19, 91
				}
				if !strings.Contains(strings.ToLower(result), "fire damage") {
					t.Fatalf("missing fire component: %s", result)
				}
			}
			if damage < minDamage || damage > maxDamage {
				t.Fatalf("%s dealt %d outside its independently checked range %d..%d", action, damage, minDamage, maxDamage)
			}
		}
	}
	for _, tc := range []struct {
		action string
		x      int
	}{{"Bladed Arm", 3}, {"Fiery Bolt", 25}} {
		ironGolemMap(t, f, map[int]int{f.char: tc.x})
		hp := ironGolemHeroHP(t, f)
		reply := play(tc.action)
		if !strings.Contains(strings.ToLower(fmt.Sprint(reply["monster_action_result"])), "beyond") || ironGolemHeroHP(t, f) != hp {
			t.Fatalf("%s resolved outside the listed range: %v", tc.action, reply)
		}
	}
}

func TestPostgresReferenceIronGolemPoisonBreath(t *testing.T) {
	f := ironGolemEncounter(t)
	steady := compendiumFixture()
	steady.Name = "Breath Save Control"
	steady.HP, steady.Resistances, steady.DmgImmune, steady.Traits = 500, "", "", nil
	con := steady.Abilities["con"]
	con.Save = 30
	steady.Abilities["con"] = con
	code, j := pgCall(t, "POST", "/api/gm/compendium/monsters", f.gm, map[string]interface{}{"source_note": "Invented breath save control", "monsters": []srdMonster2024{steady}})
	if code != 201 {
		t.Fatalf("import steady control: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
			t.Errorf("clean up imported control: %v", err)
		}
	})
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Steady inside cone", "monster_key": key, "initiative": -50}, {"name": "Steady out of cone", "monster_key": key, "initiative": -50}}})
	if code != 200 {
		t.Fatalf("add controls: %d %v", code, j)
	}
	inside := monsterIDsByKeyOrName(f.camp, "Steady inside cone")[0]
	outside := monsterIDsByKeyOrName(f.camp, "Steady out of cone")[0]
	// The fixture started as a Dwarf. The origin record controls poison
	// resistance independently of the displayed species name.
	pgExec(t, `UPDATE characters SET con=1,origin='{}'::jsonb WHERE id=$1`, f.char)
	if d := applyDamageResistance(f.char, 50, "poison"); d.FinalDamage != 50 {
		t.Fatalf("failed-save control must have no poison resistance: %+v", d)
	}
	failing := (&saveCtx{lobby: f.camp, saver: f.char, ability: "con", dc: 18}).gather()
	passing := (&saveCtx{lobby: f.camp, saver: inside, ability: "con", dc: 18}).gather()
	if failing.Judge(20).Success || !passing.Judge(1).Success {
		t.Fatal("fixture needs deterministic failed and passed Constitution saves")
	}
	ironGolemMap(t, f, map[int]int{f.char: 4, inside: 5, outside: 20})
	play := func() string {
		return fmt.Sprint(ironGolemAction(t, f, "Poison Breath (Recharge 6)", map[string]interface{}{"point": []int{10, 0}, "target_ids": []int{f.char, inside, outside}})["monster_action_result"])
	}
	before := ironGolemHeroHP(t, f)
	text := play()
	failedDamage := before - ironGolemHeroHP(t, f)
	passedDamage := 500 - monsterHPNow(f.camp, inside)
	if !strings.Contains(text, "Constitution saving throw") || !strings.Contains(text, "DC 18") || failedDamage < 10 || failedDamage > 100 || passedDamage != failedDamage/2 || monsterHPNow(f.camp, outside) != 500 {
		t.Fatalf("breath damage, half damage or cone exclusion failed: %q. failed %d, passed %d, outside HP %d", text, failedDamage, passedDamage, monsterHPNow(f.camp, outside))
	}
	before = ironGolemHeroHP(t, f)
	if text = play(); !strings.Contains(text, "hasn't recharged yet") || ironGolemHeroHP(t, f) != before {
		t.Fatalf("repeat of spent Poison Breath: %s", text)
	}
	// Advance real turns until the server rolls a six at this creature's
	// turn start. The loop samples game rolls, not retries of a failed test.
	// With five initiative entries the bound gives at least 100 opportunities.
	spent := func() bool { return containsStr(loadMonsterState(f.camp, f.caster).RechargeSpent, "Poison Breath") }
	path := fmt.Sprintf("/api/campaigns/%d/combat/next", f.camp)
	for turn := 0; turn < 500 && spent(); turn++ {
		if code, j := pgCall(t, "POST", path, f.gm, map[string]interface{}{}); code != 200 {
			t.Fatalf("advance to recharge: %d %v", code, j)
		}
		if !spent() && currentTurnID(f.camp) != f.caster {
			t.Fatal("Poison Breath recharged on a different creature's turn")
		}
	}
	if spent() {
		t.Fatal("observed no Poison Breath recharge within the bounded combat")
	}
	var sixes int
	if err := db.QueryRow(`SELECT count(*) FROM actions WHERE lobby_id=$1 AND action_type='condition' AND description LIKE $2`, f.camp, "%Poison Breath recharges (d6: 6)%").Scan(&sixes); err != nil || sixes != 1 {
		t.Fatalf("recharge did not record a single six: %d %v", sixes, err)
	}
	hp := ironGolemHeroHP(t, f)
	if text = play(); !strings.Contains(text, "Constitution saving throw") || ironGolemHeroHP(t, f) >= hp || !spent() {
		t.Fatalf("recharged breath did not resolve and spend its use: %s", text)
	}

}

func TestPostgresReferenceIronGolemMagicResistance(t *testing.T) {
	f := ironGolemEncounter(t)
	source := compendiumFixture()
	source.Name = "Wave Probe"
	source.Actions = []srdMonsterEntry{
		{Name: "Spellcasting", Text: "The character casts one of the following spells, using Intelligence as the spellcasting ability (spell save DC 18): At Will: Shatter."},
		{Name: "Arcane Wave", Text: "This action is magical. Constitution Saving Throw: DC 18, one creature within 30 feet. Failure: 2 (1d4) Thunder damage. Success: Half damage."},
		{Name: "Mechanical Wave", Text: "This action is nonmagical. Constitution Saving Throw: DC 18, one creature within 30 feet. Failure: 2 (1d4) Thunder damage. Success: Half damage."},
	}
	code, j := pgCall(t, "POST", "/api/gm/compendium/monsters", f.gm, map[string]interface{}{"source_note": "Invented Magic Resistance controls", "monsters": []srdMonster2024{source}})
	if code != 201 {
		t.Fatalf("import resistance probe: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
			t.Errorf("clean up resistance probe: %v", err)
		}
	})
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": source.Name, "monster_key": key, "initiative": -50}}})
	if code != 200 {
		t.Fatalf("add resistance probe: %d %v", code, j)
	}
	code, j = pgCall(t, "POST", "/api/gm/companion", f.gm, map[string]interface{}{"campaign_id": f.camp, "character_id": f.char, "monster_key": f.key, "name": "Iron companion check"})
	if code != 200 {
		t.Fatalf("create companion check: %d %v", code, j)
	}
	companionID := companionIDBase + asInt(j["companion"].(map[string]interface{})["id"])

	for _, tc := range []struct {
		action, use string
		advantage   bool
	}{{"Spellcasting", "Shatter", true}, {"Arcane Wave", "", true}, {"Mechanical Wave", "", false}} {
		t.Run(tc.action, func(t *testing.T) {
			for _, target := range []int{f.caster, companionID} {
				code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{
					"campaign_id": f.camp, "narration": "Reference Magic Resistance check.",
					"monster_action": map[string]interface{}{"monster": source.Name, "action": tc.action,
						"use": tc.use, "target_ids": []int{target}, "confirm_targeting": true},
				})
				text := fmt.Sprint(j["monster_action_result"])
				if code != 200 || strings.Count(text, "Constitution saving throw") != 1 {
					t.Fatalf("expected one Constitution save for %d: %d %s", target, code, text)
				}
				if got := strings.Contains(text, "Magic Resistance"); got != tc.advantage {
					t.Fatalf("Magic Resistance on target %d: got %t, want %t: %s", target, got, tc.advantage, text)
				}
			}
		})
	}
}

func TestPostgresReferenceIronGolemConditionImmunities(t *testing.T) {
	f := ironGolemEncounter(t)
	source := compendiumFixture()
	source.Name = "Condition Probe"
	source.Actions = nil
	conditions := []string{"Charmed", "Frightened", "Paralyzed", "Petrified", "Poisoned", "Exhaustion"}
	for _, condition := range conditions {
		consequence := "The target has the " + condition + " condition for 1 minute."
		if condition == "Exhaustion" {
			consequence = "The target gains 1 Exhaustion level."
		}
		source.Actions = append(source.Actions, srdMonsterEntry{Name: condition + " Test", Text: "Constitution Saving Throw: DC 99, each target. Failure: " + consequence})
	}
	code, j := pgCall(t, "POST", "/api/gm/compendium/monsters", f.gm, map[string]interface{}{"source_note": "Invented condition resistance probes", "monsters": []srdMonster2024{source}})
	if code != 201 {
		t.Fatalf("import: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
			t.Errorf("clean up condition prober: %v", err)
		}
	})
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": source.Name, "monster_key": key, "initiative": -50}}})
	if code != 200 {
		t.Fatalf("add condition probe: %d %v", code, j)
	}
	code, j = pgCall(t, "POST", "/api/gm/companion", f.gm, map[string]interface{}{"campaign_id": f.camp, "character_id": f.char, "monster_key": f.key, "name": "Iron immunity control"})
	if code != 200 {
		t.Fatalf("create Iron companion: %d %v", code, j)
	}
	pet := companionIDBase + asInt(j["companion"].(map[string]interface{})["id"])
	for _, condition := range conditions {
		t.Run(condition, func(t *testing.T) {
			before := len(loadConditionRecords(f.camp, f.caster)) + len(loadConditionRecords(f.camp, pet))
			code, j := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "Reference condition immunity check.", "monster_action": map[string]interface{}{"monster": source.Name, "action": condition + " Test", "target_ids": []int{f.caster, pet}}})
			text := strings.ToLower(fmt.Sprint(j["monster_action_result"]))
			if code != 200 || strings.Count(text, "fails the constitution saving throw") != 2 {
				t.Fatalf("control: expected two failed saves: %d %s", code, text)
			}
			if len(loadConditionRecords(f.camp, f.caster))+len(loadConditionRecords(f.camp, pet)) != before {
				t.Fatalf("immune target gained %s: %s", condition, text)
			}
			message := "not " + strings.ToLower(condition)
			if condition == "Exhaustion" {
				message = "no exhaustion"
			}
			if strings.Count(text, message) != 2 {
				t.Fatalf("immunity wasn't applied to both creature kinds: %s", text)
			}
		})
	}
}
