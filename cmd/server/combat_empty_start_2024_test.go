package main

import (
	"fmt"
	"testing"
)

// Staged test I-ALDEN-IMPORT-01: a fight started before any character is in the campaign (the GM mounts monsters first) is a started fight
// with an empty turn order. It used to be saved and then answered 502 (the reply read the first turn of an empty order), and combat/add then
// refused it as empty. Now the start answers 200 with no current turn, the fight reads as in combat, and the first monster added takes the turn.
func TestPostgresFightStartedWithNoCharacters(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "empty fight gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Empty Fight", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	base := fmt.Sprintf("/api/campaigns/%d/combat", lobby)
	if c, j := pgCall(t, "POST", base+"/next", gm, map[string]interface{}{}); c != 409 {
		t.Errorf("advancing with no fight: %d %v", c, j)
	}
	c, j := pgCall(t, "POST", base+"/start", gm, map[string]interface{}{})
	if c != 200 || j["current_turn"] != "" || j["next_step"] == nil {
		t.Fatalf("a fight started with no characters: %d %v", c, j)
	}
	if _, j := pgCall(t, "GET", base, gm, nil); j["in_combat"] != true {
		t.Errorf("the fight reads as over: %v", j)
	}
	if c, j := pgCall(t, "POST", base+"/next", gm, map[string]interface{}{}); c != 409 || j["error"] != "no_combatants" {
		t.Errorf("advancing an empty turn order: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", base+"/add", gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Goblin A", "monster_key": "goblin-warrior", "initiative": 10}, {"name": "Goblin B", "monster_key": "goblin-warrior", "initiative": 14}}})
	if c != 200 || j["current_turn"] != "Goblin B" {
		t.Fatalf("monsters added to the empty fight: %d %v", c, j)
	}
	if _, j := pgCall(t, "GET", base, gm, nil); fmt.Sprint(j["current_turn"]) != "Goblin B" || len(j["turn_order"].([]interface{})) != 2 {
		t.Errorf("the order after adding: %v", j)
	}
}
