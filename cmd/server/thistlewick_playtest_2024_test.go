package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Thistlewick's playtest (a player agent, Nov 24): my-turn, short or full, opens with the scene (the GM's newest narration, the story,
// the newest events, the round) before the menu; a seat's window says it is the check-in time, not a gate on sending, and the message
// says the turn can be sent now; speech says where the words went instead of "the GM rules on what this does"; declare's example is
// generic.
func TestPostgresThistlewickPlaytest(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "thistle gm")
	_, player := pgAgent(t, "thistle player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Thistle", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Thistle Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The ferry creaks at the dock."})

	for _, view := range []string{"?view=short", ""} {
		r := httptest.NewRequest("GET", "/api/my-turn"+view, nil)
		r.Header.Set("Authorization", "Bearer "+player)
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		body := w.Body.String()
		scene, menu := strings.Index(body, "The ferry creaks"), strings.Index(body, `"now"`)
		if scene < 0 || menu < 0 || scene > menu || !strings.Contains(body[:menu], "The ferry creaks") || !strings.Contains(body[:menu], `"round"`) {
			t.Errorf("my-turn%s doesn't open with the scene before the menu: %.400s", view, body)
		}
	}
	_, mt := pgCall(t, "GET", "/api/my-turn?view=short", player, nil)
	round, _ := mt["round"].(map[string]interface{})
	window, _ := round["window"].(map[string]interface{})
	if window["means"] != windowMeans || round["your_turn"] != true {
		t.Errorf("the window says what it is: %v", round)
	}
	if msg := fmt.Sprint(round["message"]); window["open"] == false && !strings.Contains(msg, "You can send your turn now") {
		t.Errorf("the message before the check-in: %s", msg)
	}
	_, sp := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "speak", "description": "\"Eight silver? Fine.\""})
	if sp["gm_rules"] != nil || !strings.Contains(fmt.Sprint(sp["recorded"]), "campaign log") || sp["outcome"] != outcomeApplied {
		t.Errorf("speech: %v", sp)
	}
	// a submission's reply names the time the round says it resolves (I-THISTLE-2)
	_, sub := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "declare", "description": "I pay the ferryman and board"})
	_, mt = pgCall(t, "GET", "/api/my-turn?view=short", player, nil)
	round, _ = mt["round"].(map[string]interface{})
	if at, _ := round["resolves_at"].(string); at == "" || !strings.Contains(fmt.Sprint(sub["message"]), "resolves it at their next check-in") {
		t.Errorf("the submission's reply and the round: %v / %v", sub["message"], round)
	}
	// the GM is told its next and final check-in at every narration, not only when it resolves a round (I-THISTLE-8)
	_, nar := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The ferry pushes off."})
	if rem := fmt.Sprint(nar["reminder"]); !strings.Contains(rem, "your next check-in is") || !strings.Contains(rem, "final check-in is") {
		t.Errorf("narrate's reply names no check-in: %v", nar["reminder"])
	}
	// and a turn that waits says the latest the GM reads it
	final := time.Date(2026, 11, 24, 11, 55, 0, 0, time.UTC)
	if got := finalCheckInClause(final, true, time.Date(2026, 11, 24, 11, 10, 0, 0, time.UTC)); !strings.Contains(got, "no later than the round's final check-in (11:55 UTC)") {
		t.Errorf("the latest time: %q", got)
	}
	if finalCheckInClause(final, true, final) != "" || finalCheckInClause(final, false, time.Time{}) != "" {
		t.Error("a final check-in that is not later is named")
	}
	// examples name the party, never another story's people (I-THISTLE-5)
	for _, at := range actionTypes {
		for _, name := range []string{"Aldren", "Millie", "vestry", "children"} {
			if strings.Contains(at.Example, name) {
				t.Errorf("%s's example names %s: %s", at.Name, name, at.Example)
			}
		}
	}
	if got := withAllies(`{"description": "<ally> 8, <ally2> 7"}`, []string{"Wren", "Calla"}); got != `{"description": "Wren 8, Calla 7"}` {
		t.Errorf("withAllies: %s", got)
	}
	if d, _ := lookupActionType("declare"); strings.Contains(d.Example, "children") {
		t.Errorf("declare's example: %s", d.Example)
	}
}

// I-WREN-1: the heartbeat's turn_status.your_turn and my-turn's is_my_turn are one answer (yourTurn): outside a fight without the turn
// schedule, true until the character acts, false after it, true again once the GM narrates.
func TestPostgresHeartbeatAndMyTurnAgree(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "agree gm")
	_, player := pgAgent(t, "agree player")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Agree", "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	cp := fmt.Sprintf("/api/campaigns/%d", camp)
	pgCall(t, "PATCH", cp+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, j = pgCall(t, "POST", "/api/characters", player, pgRogue(fmt.Sprintf("Agree Hero %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", cp+"/join", player, map[string]interface{}{"character_id": char})
	pgFinish(t, player, char)
	pgCall(t, "POST", cp+"/ready", player, nil)
	pgCall(t, "POST", cp+"/ready", gm, nil)
	pgCall(t, "POST", cp+"/start", gm, nil)
	both := func() (bool, bool) {
		_, mt := pgCall(t, "GET", "/api/my-turn", player, nil)
		_, hb := pgCall(t, "GET", "/api/heartbeat", player, nil)
		heart := false
		list, _ := hb["campaigns_as_player"].([]interface{})
		for _, c := range list {
			if m, _ := c.(map[string]interface{}); m != nil && int(m["id"].(float64)) == camp {
				ts, _ := m["turn_status"].(map[string]interface{})
				heart = ts["your_turn"] == true
			}
		}
		return mt["is_my_turn"] == true, heart
	}
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The road forks."})
	if m, h := both(); !m || !h {
		t.Errorf("before acting: my-turn %v, heartbeat %v", m, h)
	}
	pgCall(t, "POST", "/api/action", player, map[string]interface{}{"action": "search", "description": "I look for tracks"})
	if m, h := both(); m || h {
		t.Errorf("after acting: my-turn %v, heartbeat %v", m, h)
	}
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "Tracks lead east."})
	if m, h := both(); !m || !h {
		t.Errorf("after the GM narrates: my-turn %v, heartbeat %v", m, h)
	}
}
