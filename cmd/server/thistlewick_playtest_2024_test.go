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
		scene, menu := strings.Index(body, `"gm_says"`), strings.Index(body, `"now"`)
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
	if d, _ := lookupActionType("declare"); strings.Contains(d.Example, "children") {
		t.Errorf("declare's example: %s", d.Example)
	}
}
