package main

import (
	"fmt"
	"strings"
	"testing"
)

// Shared helpers of the monster review fixtures (reviewConcentrationSetup): a monster action through GM narration, a battle
// map with the monster at the left of one row, and an invented save target imported into the compendium and added to the fight.

// reviewMonsterAction sends one monster_action of the fixture's monster ("Review Focus Caster") through POST /api/gm/narrate.
func reviewMonsterAction(t *testing.T, f reviewConcentrationCase, action string, fields map[string]interface{}) map[string]interface{} {
	t.Helper()
	fields["monster"], fields["action"] = "Review Focus Caster", action
	code, reply := pgCall(t, "POST", "/api/gm/narrate", f.gm, map[string]interface{}{"campaign_id": f.camp, "narration": "Monster review check.", "monster_action": fields})
	if !narrateAnswered(code, reply) {
		t.Fatalf("%s: %d %v", action, code, reply)
	}
	return reply
}

// reviewMap puts the fixture's monster at x=0 of a 30-square row and every creature in positions at its x.
func reviewMap(t *testing.T, f reviewConcentrationCase, positions map[int]int) {
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

// reviewSaveTarget imports an invented monster with the given Constitution save, immunities and resistances (configure may change
// more) and adds it to the fixture's fight; it answers the new combat id.
func reviewSaveTarget(t *testing.T, f reviewConcentrationCase, name string, conSave int, immune, resistant string, configure ...func(*srdMonster2024)) int {
	t.Helper()
	target := compendiumFixture()
	target.Name = name
	target.HP = 500
	target.Resistances = resistant
	target.DmgImmune = immune
	target.Traits = nil
	ability := target.Abilities["con"]
	ability.Save = conSave
	target.Abilities["con"] = ability
	for _, apply := range configure {
		apply(&target)
	}
	code, j := pgCall(t, "POST", "/api/gm/compendium/monsters", f.gm, map[string]interface{}{"source_note": "Invented save control", "monsters": []srdMonster2024{target}})
	if code != 201 {
		t.Fatalf("import control: %d %v", code, j)
	}
	key := j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
			t.Errorf("remove control: %v", err)
		}
	})
	code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", f.camp), f.gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": name, "monster_key": key, "initiative": -50}}})
	if code != 200 {
		t.Fatalf("add control: %d %v", code, j)
	}
	return monsterIDsByKeyOrName(f.camp, name)[0]
}
