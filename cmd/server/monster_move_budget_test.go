package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// On its own turn a monster's token moves add up against its Speed (monster_move_2024.go): four 10 ft updates are 40 ft of a 30 ft Speed
// and the fourth is flagged, though no single update is over. The total starts again on its next turn. The note lists the stat block's other
// Speeds. It is still a note: the server does not know about Dash or Difficult Terrain, and nothing is refused. Invented Goblin Warrior
// variant with Speed "30 ft., Fly 60 ft.". The turn change goes through POST combat/next.
func TestPostgresMonsterMoveBudget(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "budget gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Monster move budget", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, player := pgAgent(t, "budget player")
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Budget Hero %d", time.Now().UnixNano()%1e9)))
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
	m.Name = "Budget Flyer"
	m.Speed = "30 ft., Fly 60 ft."
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"source_note": "invented variant for the move budget test", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	pgCall(t, "POST", cp+"/combat/start", gm, map[string]interface{}{})
	pgCall(t, "POST", cp+"/combat/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{{"name": "Budget Flyer", "monster_key": key, "initiative": 1}}})
	ids := monsterIDsByKeyOrName(camp, "Budget Flyer")
	if len(ids) != 1 {
		t.Fatalf("monster id: %v", ids)
	}
	id := ids[0]
	put := func(x int) {
		t.Helper()
		_, st := pgCall(t, "GET", cp+"/battle-map", gm, nil)
		req := map[string]interface{}{"map": encounterMap{Grid: []string{"..................."}, Tokens: []mapToken{{ID: id, Name: "Budget Flyer", Position: mapSquare{x, 0}, Size: 1}}}}
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
	toTurn := func(want func() bool) {
		t.Helper()
		for i := 0; i < 6 && !want(); i++ {
			if c, r := pgCall(t, "POST", cp+"/combat/next", gm, map[string]interface{}{}); c != 200 {
				t.Fatalf("combat/next: %d %v", c, r)
			}
		}
		if !want() {
			t.Fatal("could not reach the wanted turn")
		}
	}
	// outside its own turn the moves do not add up: three 10 ft updates on the hero's turn are not 30 ft of a budget, and none is over
	toTurn(func() bool { return currentTurnID(camp) == char })
	put(0)
	put(2)
	put(4)
	put(6)
	put(8)
	if n := notes(); len(n) != 0 {
		t.Fatalf("moves off its own turn were added up against its Speed: %v", n)
	}
	toTurn(func() bool { return currentTurnID(camp) == id })
	put(10) // 10 ft, then
	put(12) // 20, then
	put(14) // 30: at the Speed, no note
	if n := notes(); len(n) != 0 {
		t.Fatalf("a total at the Speed was flagged: %v", n)
	}
	put(16) // 40 of 30
	n := notes()
	if len(n) != 1 || !strings.Contains(n[0], "moved 10 ft (40 ft this turn so far)") || !strings.Contains(n[0], "Speed is 30 ft") || !strings.Contains(n[0], "Fly 60 ft") {
		t.Fatalf("the move that took the turn over its Speed was not flagged with the total and the other Speeds: %v", n)
	}
	// its next turn starts the count again
	toTurn(func() bool { return currentTurnID(camp) == char })
	toTurn(func() bool { return currentTurnID(camp) == id })
	put(18)
	if n := notes(); len(n) != 1 {
		t.Fatalf("the total was not reset on its next turn: %v", n)
	}
}
