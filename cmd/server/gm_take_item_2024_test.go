package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// S-CALLA-9: the GM's narration uses a character's gear: POST /api/gm/take-item takes it out of the inventory and leaves it where
// the story put it; the player's my-turn lists it under left_behind with the pick-up request, and picking it up brings it back.
func TestPostgresGMTakeItemLeavesItWhereTheStoryPutIt(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg take gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Take", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg take player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Take %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgCall(t, "POST", "/api/gm/give-item", gm, map[string]interface{}{"campaign_id": lobby, "character_id": char, "item_name": "Blanket"})
	carries := func() bool {
		var inv string
		db.QueryRow(`SELECT COALESCE(inventory::text, '') FROM characters WHERE id = $1`, char).Scan(&inv)
		return strings.Contains(strings.ToLower(inv), "blanket")
	}
	if !carries() {
		t.Fatal("the blanket wasn't given")
	}
	// another player can't send it; an item the character doesn't carry is refused
	if c, _ := pgCall(t, "POST", "/api/gm/take-item", tok, map[string]interface{}{"character_id": char, "item": "Blanket"}); c < 400 {
		t.Errorf("a player took an item as the GM: %d", c)
	}
	if c, _ := pgCall(t, "POST", "/api/gm/take-item", gm, map[string]interface{}{"character_id": char, "item": "Warhammer of Nowhere"}); c < 400 {
		t.Errorf("an item not carried was taken: %d", c)
	}
	c, take := pgCall(t, "POST", "/api/gm/take-item", gm, map[string]interface{}{"character_id": char, "item": "Blanket", "where": "in the bedroll as a decoy"})
	if c != 200 || carries() {
		t.Fatalf("take-item: %d %v (still carried %v)", c, take, carries())
	}
	_, mt := pgCall(t, "GET", "/api/my-turn", tok, nil)
	lb, _ := json.Marshal(mt["left_behind"])
	if !strings.Contains(string(lb), "in the bedroll as a decoy") || !strings.Contains(string(lb), "equipment/pick-up") || !strings.Contains(string(lb), "no longer in your inventory") {
		t.Errorf("my-turn's left_behind: %s", lb)
	}
	if ev, _ := json.Marshal(mt["recent_events"]); !strings.Contains(string(ev), "in the bedroll as a decoy") {
		t.Errorf("the log doesn't say where it was left: %s", ev)
	}
	id := int(take["dropped_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/equipment/pick-up", char), tok, map[string]interface{}{"dropped_id": id}); c != 200 || !carries() {
		t.Errorf("pick-up: %d %v", c, j)
	}
	if _, mt = pgCall(t, "GET", "/api/my-turn", tok, nil); mt["left_behind"] != nil {
		t.Errorf("left_behind stays after the pick-up: %v", mt["left_behind"])
	}
}
