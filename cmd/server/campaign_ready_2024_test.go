package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
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
	for _, want := range []string{"/ready", "start_without", "kick-character", "last resort", "idle", "validate each player's build"} {
		if !strings.Contains(fmt.Sprint(j["before_start"]), want) {
			t.Errorf("the creation reply's guidance doesn't mention %q: %v", want, j["before_start"])
		}
	}
	// I-GM-10: the opening scene before the start; turns wait for it
	if c, j = pgCall(t, "POST", "/api/gm/narrate", gmTok, map[string]interface{}{"campaign_id": camp, "narration": "Dawn over the cathedral."}); c != 200 {
		t.Errorf("the opening narration before the start: %d %v", c, j)
	}
	if c, j = pgCall(t, "POST", "/api/gm/narrate", gmTok, map[string]interface{}{"campaign_id": camp, "narration": "Next.", "advance_turn": true}); c != 409 || j["error"] != "campaign_not_active" {
		t.Errorf("advance_turn before the start: %d %v", c, j)
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
	pgFinish(t, aTok, a1)
	pgFinish(t, aTok, a2)
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
	// the start waits on the GM first, then on every player
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, nil)
	if c != 409 || j["error"] != "gm_not_ready" {
		t.Fatalf("start before the GM is ready: %d %v", c, j)
	}
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), gmTok, nil); c != 200 || j["gm"] != true {
		t.Fatalf("the GM's ready: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, nil)
	if c != 409 || j["error"] != "players_not_ready" || !strings.Contains(fmt.Sprint(j["message"]), "Pg Ready A2") || !strings.Contains(fmt.Sprint(j["message"]), "Pg Ready B1") {
		t.Fatalf("start before anyone is ready: %d %v", c, j)
	}
	_, j = pgCall(t, "GET", "/api/gm/status", gmTok, nil)
	if !strings.Contains(fmt.Sprint(j["gm_tasks"]), "Players not ready: Pg Ready A2") {
		t.Errorf("gm/status doesn't say who the start waits on: %v", j["gm_tasks"])
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), bTok, nil)
	if j["status"] != "campaign_not_started" || j["error"] != nil || j["before_start"] == nil || j["readiness"] == nil {
		t.Errorf("my-turn before the start: %v", j)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), aTok, nil)
	if c, _ = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, nil); c != 409 {
		t.Errorf("started with B not ready: %d", c)
	}
	// the GM's override for an idle player: only a player who isn't ready, and it is logged
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, map[string]interface{}{"start_without": []int{a2}}); c != 400 || j["error"] != "invalid_start_without" {
		t.Errorf("start_without a ready player: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", camp), gmTok, map[string]interface{}{"start_without": []int{b1}})
	if c != 200 || !strings.Contains(fmt.Sprint(j["started_without"]), "Pg Ready B1") || j["readiness_at_start"] == nil {
		t.Fatalf("start without the idle player: %d %v", c, j)
	}
	if played, _ := characterHasPlayed(b1); played {
		t.Error("being started without counts as having played")
	}
	// after the start: no more changes of character or ready flags
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), aTok, map[string]interface{}{"character_id": a1}); c != 409 || j["error"] != "one_character_per_player" {
		t.Errorf("a change of character after the start: %d %v", c, j)
	}
	if c, _ = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), aTok, nil); c != 409 {
		t.Errorf("ready after the start: %d", c)
	}
}

// Feedback log 49: a player whose character has required choices open can't say it is ready (409 setup_not_finished with the
// open choices); once they are made it can, and optional steps (shop, portrait) never hold it back.
func TestPostgresReadyWaitsForRequiredChoices(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg ready setup gm")
	_, tok := pgAgent(t, "pg ready setup player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Unfinished %d", time.Now().UnixNano()%1e9)))
	id := int(j["character_id"].(float64))
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Ready Setup %d", gm), "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id})
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), tok, nil); c != 409 || j["error"] != "setup_not_finished" || len(asList(j["open"])) == 0 {
		t.Fatalf("ready with choices open: %d %v", c, j)
	}
	pgFinish(t, tok, id)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), tok, nil); c != 200 || j["ready"] != true {
		t.Errorf("ready once finished (shop and portrait still optional): %d %v", c, j)
	}
}
