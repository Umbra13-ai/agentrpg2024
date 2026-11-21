package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A complete encounter using the HTTP router, real Postgres and the server's combat state.
// The creature is original test data; no copyrighted monster text is included.
func TestPostgresCustomCombatSimulation(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "simulation gm")
	_, player := pgAgent(t, "simulation player")
	call := func(method, path, token string, body interface{}) map[string]interface{} {
		t.Helper()
		status, j := pgCall(t, method, path, token, body)
		if status != 200 && status != 201 {
			t.Fatalf("%s %s: %d %v", method, path, status, j)
		}
		return j
	}
	camp := int(call("POST", "/api/campaigns", gm, map[string]interface{}{"name": "Compendium simulation", "max_players": 2})["campaign_id"].(float64))
	path := fmt.Sprintf("/api/campaigns/%d", camp)
	call("PATCH", path+"/schedule", gm, map[string]interface{}{"state": "off"})
	char := int(call("POST", "/api/characters", player, pgRogue(fmt.Sprintf("Sim Hero %d", time.Now().UnixNano()%1e9)))["character_id"].(float64))
	call("POST", path+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	call("POST", path+"/ready", player, nil)
	call("POST", path+"/ready", gm, nil)
	call("POST", path+"/start", gm, nil)

	creature := compendiumFixture()
	result := call("POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "Original simulation creature", "monsters": []srdMonster2024{creature}})
	key := result["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"].(string)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key); err != nil {
			t.Errorf("simulation cleanup: %v", err)
		}
	})

	call("POST", path+"/combat/start", gm, map[string]interface{}{})
	call("POST", path+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Warden Alpha", "monster_key": key, "initiative": 25},
		{"name": "Warden Beta", "monster_key": key, "initiative": 1},
	}})
	f, err := readFight(db, camp)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int{}
	for _, entry := range f.Order {
		name := sprintAny(entry["name"])
		if strings.HasPrefix(name, "Warden ") {
			ids[name] = asInt(entry["id"])
			if asInt(entry["hp"]) != 400 || asInt(entry["ac"]) != 22 {
				t.Fatalf("stats %v", entry)
			}
		}
	}
	if len(ids) != 2 || ids["Warden Alpha"] == ids["Warden Beta"] {
		t.Fatalf("combatants %v", ids)
	}
	t.Logf("Started combat with two independently tracked custom wardens: HP 400, AC 22, unique IDs %v", ids)
	hp := func(id int) int {
		f, e := readFight(db, camp)
		if e != nil {
			t.Fatal(e)
		}
		_, entry := f.entry(id)
		if entry == nil {
			t.Fatalf("missing combatant %d", id)
		}
		return asInt(entry["hp"])
	}
	adjust := func(id, n int, typ string) {
		call("POST", "/api/gm/adjust-monster", gm, map[string]interface{}{"campaign_id": camp, "target_id": id, "damage": n, "damage_type": typ, "apply_resistances": true, "note": "Simulation damage"})
	}
	a, b := ids["Warden Alpha"], ids["Warden Beta"]
	adjust(a, 40, "poison")
	if hp(a) != 400 {
		t.Fatalf("poison immunity: %d", hp(a))
	}
	adjust(a, 40, "fire")
	if hp(a) != 380 {
		t.Fatalf("fire resistance: %d", hp(a))
	}
	adjust(b, 30, "slashing")
	if hp(b) != 370 || hp(a) != 380 {
		t.Fatalf("independent damage: alpha %d beta %d", hp(a), hp(b))
	}
	t.Log("Damage application: poison 40 -> 0, fire 40 -> 20, Beta slashing 30 -> 30; instances remain independent")

	// Give the player a survivable test pool, but resolve attacks and turns via endpoints.
	pgExec(t, `UPDATE characters SET max_hp=1000,hp=1000,ac=1 WHERE id=$1`, char)
	heroHP := func() int {
		var n int
		if e := db.QueryRow(`SELECT hp FROM characters WHERE id=$1`, char).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	hit := false
	for i := 0; i < 12; i++ {
		before := heroHP()
		j := call("POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The warden attacks.", "monster_action": map[string]interface{}{"monster": "Warden Alpha", "action": "Slam", "target": fmt.Sprint(char)}})
		result := fmt.Sprint(j["monster_action_result"])
		if heroHP() < before {
			t.Logf("Imported Slam applied damage: hero HP %d -> %d; result %s", before, heroHP(), result)
			hit = true
			break
		}
	}
	if !hit {
		t.Fatal("12 imported attacks produced no damaging hit")
	}
	for i := 0; i < 4; i++ {
		j := call("POST", path+"/combat/next", gm, map[string]interface{}{})
		t.Logf("Advance turn %d: round=%v current=%v", i+1, j["round"], j["current_turn"])
	}
	f, err = readFight(db, camp)
	if err != nil || f.Round < 2 {
		t.Fatalf("round did not advance: %v %+v", err, f)
	}
	adjust(a, 1000, "slashing")
	adjust(b, 1000, "slashing")
	if hp(a) != 0 || hp(b) != 0 {
		t.Fatalf("defeat alpha %d beta %d", hp(a), hp(b))
	}
	call("POST", path+"/combat/end", gm, nil)
	f, err = readFight(db, camp)
	if err == nil {
		t.Fatal("combat remains active")
	}
	t.Log("Round advanced, both wardens defeated, combat ended successfully")
}
