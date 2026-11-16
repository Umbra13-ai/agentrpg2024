package main

import (
	"fmt"
	"strings"
	"testing"
)

// I-GM-7: a campaign started with round 1 held for a session zero. Talk works, game actions are refused and spend nothing, every
// status says the table is held; only the GM opens play, and then round 1 opens and actions are submissions again.
func TestPostgresTableHeldBeforeRoundOne(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg hold gm")
	_, plTok := pgAgent(t, "pg hold player")
	_, j := pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Holder %d", gm)))
	char := int(j["character_id"].(float64))
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Hold %d", gm), "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), plTok, map[string]interface{}{"character_id": char}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, map[string]interface{}{"first_round": "later"}); c != 400 || j["error"] != "invalid_first_round" {
		t.Errorf("a wrong first_round: %d %v", c, j)
	}
	pgFinish(t, plTok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), plTok, nil) // every player and the GM are ready before the start
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), gmTok, nil)
	c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, map[string]interface{}{"first_round": "hold"})
	if c != 200 || j["first_round"] != "held" || j["round"] != nil {
		t.Fatalf("held start: %d %v", c, j)
	}

	// talk works; a game action is refused and nothing is saved
	if c, j = pgCall(t, "POST", "/api/action", plTok, map[string]interface{}{"action": "speak", "description": "Hello, all."}); c != 200 {
		t.Errorf("speech while held: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", "/api/action", plTok, map[string]interface{}{"action": "search", "description": "I search the hall."})
	if c != 409 || j["error"] != "table_held" {
		t.Errorf("a game action while held: %d %v", c, j)
	}
	var subs int
	db.QueryRow(`SELECT COUNT(*) FROM round_submissions s JOIN campaign_rounds r ON r.id = s.round_id WHERE r.lobby_id = $1`, camp).Scan(&subs)
	if subs != 0 {
		t.Errorf("a submission was saved while held: %d", subs)
	}
	// every status says so
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), plTok, nil)
	if j["is_my_turn"] != false || j["table_held"] == nil {
		t.Errorf("my-turn while held: is_my_turn %v, table_held %v", j["is_my_turn"], j["table_held"])
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/schedule", camp), plTok, nil)
	if !strings.Contains(fmt.Sprint(j), "holding the table") {
		t.Errorf("the schedule status doesn't say the table is held: %v", j["check_in"])
	}
	_, j = pgCall(t, "GET", "/api/gm/status", gmTok, nil)
	if j["table_held"] != true || !strings.Contains(fmt.Sprint(j["gm_tasks"]), "/round/open") {
		t.Errorf("gm/status while held: %v", j["gm_tasks"])
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d", camp), plTok, nil)
	if j["table_held"] != true {
		t.Errorf("the campaign doesn't show the hold")
	}

	// only the GM opens play; then round 1 is open and a game action is a submission
	if c, _ = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/round/open", camp), plTok, nil); c != 403 {
		t.Errorf("a player opened play: %d", c)
	}
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/round/open", camp), gmTok, nil)
	if c != 200 || j["held_before"] != true || j["round"] == nil {
		t.Fatalf("round/open: %d %v", c, j)
	}
	if c, j = pgCall(t, "POST", "/api/action", plTok, map[string]interface{}{"action": "search", "description": "I search the hall."}); c != 202 {
		t.Errorf("a game action after play opened: %d %v", c, j)
	}
	if _, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), plTok, nil); j["table_held"] != nil {
		t.Errorf("my-turn still says held: %v", j["table_held"])
	}
	// opening again is harmless
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/round/open", camp), gmTok, nil); c != 200 || j["held_before"] != false {
		t.Errorf("a second open: %d %v", c, j)
	}
}

// A fight the GM starts opens play after a held start.
func TestPostgresFightOpensHeldTable(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg hold gm2")
	_, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Hold Fight %d", gm), "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), gmTok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, map[string]interface{}{"first_round": "hold"})
	if !tableIsHeld(camp) {
		t.Fatal("not held after a held start")
	}
	scheduleFightStarts(camp)
	if tableIsHeld(camp) {
		t.Error("a fight's start didn't open play")
	}
}
