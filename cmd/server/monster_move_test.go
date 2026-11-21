package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A monster token's move through the GM's map update is checked against its Speed (monster_move_2024.go): over the Speed, a Speed 0
// condition and a move within the Speed (no note); forced movement is not the monster's own.
func TestPostgresMonsterMoveSpeed(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "move gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Monster move speed", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, player := pgAgent(t, "move player")
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Move Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	var goblin srdMonster2024
	for _, m := range srdMonsters2024() {
		if m.Name == "Goblin Warrior" {
			goblin = m
		}
	}
	m := goblin
	m.Name = "Move Walker"
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "SRD Goblin Warrior variant for the monster move test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	if len(j["monsters"].([]interface{})) == 0 {
		t.Fatalf("import returned no monsters: %v", j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Move Walker", "monster_key": key, "initiative": 1}}})
	ids := monsterIDsByKeyOrName(camp, "Move Walker")
	if len(ids) != 1 {
		t.Fatalf("monster id: %v", ids)
	}
	id := ids[0]
	put := func(x int, forced bool) {
		t.Helper()
		_, st := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"............"}, Tokens: []mapToken{{ID: id, Name: "Move Walker", Position: mapSquare{x, 0}, Size: 1}}}, "forced_movement": forced}
		if rev, ok := st["revision"].(string); ok {
			req["confirmation"] = rev
		}
		if c, r := pgCall(t, "PUT", cp+"/battle-map", gm, req); c != 200 {
			t.Fatalf("put map: %d %v", c, r)
		}
	}
	notes := func() []string {
		var out []string
		rows, err := db.Query(`SELECT result FROM actions WHERE lobby_id=$1 AND action_type='monster_move' ORDER BY id`, camp)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}
	put(0, false) // first publish: nothing moved
	put(2, false) // 10 ft of a 30 ft Speed
	if n := notes(); len(n) != 0 {
		t.Fatalf("a move within the Speed was flagged: %v", n)
	}
	put(10, false) // 40 ft
	n := notes()
	if len(n) != 1 || !strings.Contains(n[0], "moved 40 ft") || !strings.Contains(n[0], "Speed is 30 ft") {
		t.Fatalf("a move over the Speed was not flagged: %v", n)
	}
	put(11, true) // pushed: not its own movement
	if n := notes(); len(n) != 1 {
		t.Fatalf("forced movement was checked against the Speed: %v", n)
	}
	addCondition(camp, id, condAdd{Condition: "speed_zero", Source: "Test", Rounds: 5})
	put(10, false)
	n = notes()
	if len(n) != 2 || !strings.Contains(n[1], "Speed is 0") {
		t.Fatalf("a move at Speed 0 was not flagged: %v", n)
	}
}
