package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// What the GM is waiting on a player for (S-GM-12): the GM pins an ask; the player sees it in my-turn (before the start) and the
// heartbeat until the GM clears it, its reply noted; the GM's status shows it waiting, then replied; the GM clears it. Only the GM sets or
// clears, only for a character in the campaign; another player sees nothing of it.
func TestPostgresGMAsks(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg asks gm")
	_, aTok := pgAgent(t, "pg asks a")
	_, bTok := pgAgent(t, "pg asks b")
	_, strangerTok := pgAgent(t, "pg asks stranger")
	mk := func(tok, name string) int {
		c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("%s %d", name, time.Now().UnixNano()%1e9)))
		if c != 200 {
			t.Fatalf("character: %d %v", c, j)
		}
		return int(j["character_id"].(float64))
	}
	a, b, stranger := mk(aTok, "Pg Asks A"), mk(bTok, "Pg Asks B"), mk(strangerTok, "Pg Asks Stranger")
	_, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Asks %d", gm), "max_players": 3})
	camp := int(j["campaign_id"].(float64))
	if !strings.Contains(fmt.Sprint(j["before_start"]), "/asks") {
		t.Errorf("the GM's before-start guidance doesn't mention pinning an ask: %v", j["before_start"])
	}
	for tok, id := range map[string]int{aTok: a, bTok: b} {
		if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id}); c != 200 {
			t.Fatalf("join: %d %v", c, j)
		}
	}
	asks := fmt.Sprintf("/api/campaigns/%d/asks", camp)
	need := "Choose the group patron and name the soul you seek."

	// only the GM sets one, only for a character in the campaign, with words
	if c, j := pgCall(t, "POST", asks, aTok, map[string]interface{}{"character_id": b, "need": need}); c != 403 {
		t.Errorf("a player set an ask: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", asks, gmTok, map[string]interface{}{"character_id": stranger, "need": need}); c != 404 || j["error"] != "character_not_in_campaign" {
		t.Errorf("an ask for a character outside the campaign: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", asks, gmTok, map[string]interface{}{"character_id": a, "need": "  "}); c != 400 || j["error"] != "need_required" {
		t.Errorf("an empty ask: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", asks, gmTok, map[string]interface{}{"character_id": a, "need": "old question"}); c != 200 {
		t.Fatalf("set: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", asks, gmTok, map[string]interface{}{"character_id": a, "need": need}); c != 200 || j["need"] != need {
		t.Fatalf("replace: %d %v", c, j)
	}

	// the player sees it before the start (my-turn) and in the heartbeat; the other player doesn't
	askOf := func(j map[string]interface{}) string {
		m, _ := j["gm_asks_you"].(map[string]interface{})
		s, _ := m["need"].(string)
		return s
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), aTok, nil)
	if j["status"] != "campaign_not_started" || j["error"] != nil || askOf(j) != need {
		t.Errorf("the player's my-turn before the start: %v / %v", j["status"], j["gm_asks_you"])
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), bTok, nil)
	if j["gm_asks_you"] != nil {
		t.Errorf("another player sees the ask: %v", j["gm_asks_you"])
	}
	_, j = pgCall(t, "GET", "/api/heartbeat", aTok, nil)
	found := false
	for _, c := range asList(j["campaigns_as_player"]) {
		if m, _ := c.(map[string]interface{}); m != nil && asInt(m["id"]) == camp && askOf(m) == need {
			found = true
		}
	}
	if !found {
		t.Errorf("the heartbeat doesn't carry the ask: %v", j["campaigns_as_player"])
	}
	_, j = pgCall(t, "GET", asks, bTok, nil)
	if len(asList(j["asks"])) != 0 {
		t.Errorf("another player's GET shows asks: %v", j["asks"])
	}

	// the GM sees it waiting, then answered once the player posts in the chat
	gmStatus := func() (string, []interface{}) {
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", camp), gmTok, nil)
		return fmt.Sprint(j["gm_tasks"]), asList(j["player_asks"])
	}
	if tasks, list := gmStatus(); !strings.Contains(tasks, "Waiting on players") || len(list) != 1 {
		t.Errorf("GM status before the answer: %v / %v", tasks, list)
	}
	time.Sleep(10 * time.Millisecond)
	if c, _ := pgCall(t, "POST", "/api/campaigns/messages", aTok, map[string]interface{}{"campaign_id": camp, "message": "The Hellriders; I seek my brother."}); c != 200 {
		t.Fatalf("chat: %d", c)
	}
	if tasks, list := gmStatus(); !strings.Contains(tasks, "Replied") || len(list) != 1 || list[0].(map[string]interface{})["answered_by"] != "party chat" {
		t.Errorf("GM status after the answer: %v / %v", tasks, list)
	}
	// the GM runs the game (feedback log 57c): the ask stays, with the reply noted, until the GM clears it
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), aTok, nil)
	if m, _ := j["gm_asks_you"].(map[string]interface{}); m == nil || m["you_replied_at"] == nil {
		t.Errorf("the player's ask after replying: %v", j["gm_asks_you"])
	}

	// the GM clears it (here with the POST form a tool without DELETE sends: feedback log 40); clearing again is 404
	if c, j := pgCall(t, "POST", asks+"?_method=DELETE", gmTok, map[string]interface{}{"character_id": a}); c != 200 || j["need"] != nil {
		t.Errorf("clear: %d %v", c, j)
	}
	if c, j := pgCall(t, "DELETE", asks, gmTok, map[string]interface{}{"character_id": a}); c != 404 || j["error"] != "no_open_ask" {
		t.Errorf("clear again: %d %v", c, j)
	}
	if _, list := gmStatus(); len(list) != 0 {
		t.Errorf("a cleared ask still listed: %v", list)
	}
}
