package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// S-CALLA-9: the GM's narration uses a character's gear: POST /api/gm/take-item takes it out of the inventory and leaves it where
// the story put it; the player's my-turn lists it under left_behind; a pick-up is immediate and the GM can veto it.
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
	pick := func() (int, map[string]interface{}) {
		return pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/equipment/pick-up", char), tok, map[string]interface{}{"dropped_id": id})
	}
	ground := fmt.Sprintf("/api/campaigns/%d/ground/%d/", lobby, id)
	// a pick-up is immediate; the GM sees it and can veto it: the item goes back where it was
	if c, j := pick(); c != 200 || !carries() {
		t.Fatalf("pick-up: %d %v", c, j)
	}
	if _, st := pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", lobby), gm, nil); !strings.Contains(fmt.Sprint(st["recent_pick_ups"]), "veto") {
		t.Errorf("GM status doesn't list the pick-up: %v", st["recent_pick_ups"])
	}
	if c, _ := pgCall(t, "POST", ground+"veto", tok, nil); c < 400 {
		t.Errorf("a player vetoed: %d", c)
	}
	c, v := pgCall(t, "POST", ground+"veto", gm, map[string]interface{}{"note": "you are a mile away now"})
	if c != 200 || carries() {
		t.Fatalf("veto: %d %v (carried %v)", c, v, carries())
	}
	_, mt = pgCall(t, "GET", "/api/my-turn", tok, nil)
	if lb := fmt.Sprint(mt["left_behind"]); !strings.Contains(lb, "in the bedroll as a decoy") {
		t.Errorf("after the veto the item isn't back where it was: %s", lb)
	}
	if ev, _ := json.Marshal(mt["recent_events"]); !strings.Contains(string(ev), "you are a mile away now") {
		t.Errorf("the player isn't told why: %s", ev)
	}
	if c, _ := pgCall(t, "POST", ground+"veto", gm, nil); c != 404 && c != 409 {
		t.Errorf("a second veto of the same pick-up: %d", c)
	}
	id = int(v["dropped_id"].(float64))
	if c, j := pick(); c != 200 || !carries() {
		t.Fatalf("the pick-up after the veto: %d %v", c, j)
	}
	if _, mt = pgCall(t, "GET", "/api/my-turn", tok, nil); !strings.Contains(fmt.Sprint(mt["left_behind"]), "items:[]") {
		t.Errorf("left_behind after the pick-up (an empty list): %v", mt["left_behind"])
	}
	if inv := fmt.Sprint(mt["character"].(map[string]interface{})["inventory"]); !strings.Contains(inv, "Blanket") || !strings.Contains(inv, "/equipment") {
		t.Errorf("my-turn's inventory: %s", inv)
	}

	// I-CALLA-20: taken with no "where" (on the ground), then claimed off the ground by the GM: the player still sees it, gone, with the
	// GM's note and no pick-up; the claim is in the log; the GM's return makes it an item to pick up again
	c, take = pgCall(t, "POST", "/api/gm/take-item", gm, map[string]interface{}{"character_id": char, "item": "Blanket"})
	if c != 200 || carries() {
		t.Fatalf("take-item without where: %d %v", c, take)
	}
	id = int(take["dropped_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ground/%d/claim", lobby, id), gm, map[string]interface{}{"note": "the cultists burned it"}); c != 200 {
		t.Fatalf("claim: %d %v", c, j)
	}
	_, mt = pgCall(t, "GET", "/api/my-turn", tok, nil)
	lb, _ = json.Marshal(mt["left_behind"])
	if !strings.Contains(string(lb), `"gone":"the cultists burned it"`) || strings.Contains(string(lb), "equipment/pick-up") || !strings.Contains(string(lb), "Blanket") ||
		strings.Contains(string(lb), `"where"`) || !strings.Contains(string(lb), `"was_left":"on the ground"`) {
		t.Errorf("left_behind after the GM's claim: %s", lb)
	}
	if ev, _ := json.Marshal(mt["recent_events"]); !strings.Contains(string(ev), "is gone: the cultists burned it") {
		t.Errorf("the claim isn't in the log: %s", ev)
	}
	if c, j := pick(); c < 400 || carries() {
		t.Errorf("a claimed item was picked up: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ground/%d/return", lobby, id), gm, nil); c != 200 {
		t.Fatalf("return: %d %v", c, j)
	}
	_, mt = pgCall(t, "GET", "/api/my-turn", tok, nil)
	if lb, _ = json.Marshal(mt["left_behind"]); strings.Contains(string(lb), `"gone"`) || !strings.Contains(string(lb), "equipment/pick-up") {
		t.Errorf("left_behind after the GM's return: %s", lb)
	}
}
