package main

import (
	"fmt"
	"strings"
	"testing"
)

// Before the start: questions in the chat, a change of character (the new one takes the old one's seat), every player ready
// before the GM can start, and a full party no longer starts the campaign by itself.
func TestPostgresPlayersReadyBeforeStart(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg ready gm")
	_, aTok := pgAgent(t, "pg ready a")
	_, bTok := pgAgent(t, "pg ready b")
	mk := func(tok, name string) int {
		c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("%s %d", name, gm)))
		if c != 200 {
			t.Fatalf("character: %d %v", c, j)
		}
		return int(j["character_id"].(float64))
	}
	a1, a2, b1 := mk(aTok, "Pg Ready A1"), mk(aTok, "Pg Ready A2"), mk(bTok, "Pg Ready B1")
	c, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Ready %d", gm), "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	if !strings.Contains(fmt.Sprint(j["before_start"]), "/ready") {
		t.Errorf("the creation reply doesn't explain the ready flag: %v", j["before_start"])
	}
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), aTok, map[string]interface{}{"character_id": a1})
	if c != 200 || !strings.Contains(fmt.Sprint(j["before_start"]), "/ready") || j["next_steps"].(map[string]interface{})["set_ready"] == nil {
		t.Fatalf("join A1: %d %v", c, j)
	}
	seatA := j["seat"]
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), bTok, map[string]interface{}{"character_id": b1})
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d", camp), gmTok, nil)
	if j["status"] != "recruiting" {
		t.Fatalf("a full party started the campaign by itself: %v", j["status"])
	}
	// questions in the chat before the start
	if c, _ = pgCall(t, "POST", "/api/campaigns/messages", aTok, map[string]interface{}{"campaign_id": camp, "message": "Two healers?"}); c != 200 {
		t.Errorf("chat before the start: %d", c)
	}
	// A is ready, then changes characters: A2 takes A1's seat, and the ready is cleared
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), aTok, map[string]interface{}{"ready": true}); c != 200 || j["ready"] != true {
		t.Fatalf("ready: %d %v", c, j)
	}
	if played, _ := characterHasPlayed(a1); played {
		t.Error("saying ready counts as having played")
	}
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), aTok, map[string]interface{}{"character_id": a2})
	if c != 200 || j["replaced"] == nil || fmt.Sprint(j["seat"]) != fmt.Sprint(seatA) {
		t.Fatalf("change of character: %d %v (seat before %v)", c, j, seatA)
	}
	var a1Lobby, a2Lobby int
	db.QueryRow(`SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, a1).Scan(&a1Lobby)
	db.QueryRow(`SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, a2).Scan(&a2Lobby)
	if a1Lobby != 0 || a2Lobby != camp {
		t.Errorf("after the change: A1 in %d, A2 in %d", a1Lobby, a2Lobby)
	}
	// the start waits on everyone
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, nil)
	if c != 409 || j["error"] != "players_not_ready" || !strings.Contains(fmt.Sprint(j["message"]), "Pg Ready A2") || !strings.Contains(fmt.Sprint(j["message"]), "Pg Ready B1") {
		t.Fatalf("start before anyone is ready: %d %v", c, j)
	}
	_, j = pgCall(t, "GET", "/api/gm/status", gmTok, nil)
	if !strings.Contains(fmt.Sprint(j["gm_tasks"]), "Waiting on") {
		t.Errorf("gm/status doesn't say who the start waits on: %v", j["gm_tasks"])
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), bTok, nil)
	if j["error"] != "campaign_not_started" || j["before_start"] == nil || j["readiness"] == nil {
		t.Errorf("my-turn before the start: %v", j)
	}
	if c, _ = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), gmTok, nil); c != 403 {
		t.Errorf("the GM said ready: %d", c)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), aTok, nil)
	if c, _ = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, nil); c != 409 {
		t.Errorf("started with B not ready: %d", c)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), bTok, map[string]interface{}{"ready": true})
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, nil); c != 200 {
		t.Fatalf("start with everyone ready: %d %v", c, j)
	}
	// after the start: no more changes of character or ready flags
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), aTok, map[string]interface{}{"character_id": a1}); c != 409 || j["error"] != "one_character_per_player" {
		t.Errorf("a change of character after the start: %d %v", c, j)
	}
	if c, _ = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), aTok, nil); c != 409 {
		t.Errorf("ready after the start: %d", c)
	}
}
