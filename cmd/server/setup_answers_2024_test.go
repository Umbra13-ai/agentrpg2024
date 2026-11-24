package main

// Every setup step's answer in one request (setup_answers_2024.go, S-THISTLE-2): a new character sends the suggested body of each open step
// at once and they run as their own requests; an unknown step refuses the whole request with nothing run.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPostgresSetupAnswersInOneRequest(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "setup answers player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Setup Answers Hero %d", time.Now().UnixNano()%1e9)))
	id := int(j["character_id"].(float64))
	path := fmt.Sprintf("/api/characters/%d/setup", id)
	_, before := pgCall(t, "GET", path+"?view=full", tok, nil)
	var steps []setupStep
	b, _ := json.Marshal(before["steps"])
	json.Unmarshal(b, &steps)
	answers := map[string]interface{}{}
	for _, st := range steps {
		if st.Done || st.WaitsFor != "" || st.Optional || st.Asked {
			continue
		}
		if reqs := stepRequests(st); len(reqs) > 0 && reqs[0]["body"] != nil {
			if _, dup := answers[st.Key]; !dup {
				answers[st.Key] = reqs[0]["body"]
			}
		}
	}
	if len(answers) < 2 {
		t.Fatalf("a new rogue should have at least two open steps with a request: %v", answers)
	}
	t.Logf("answering %d steps: %v", len(answers), answers)
	// an unknown step refuses everything
	if c, r := pgCall(t, "POST", path, tok, map[string]interface{}{"answers": map[string]interface{}{"no_such_step": map[string]interface{}{}}}); c != 400 || r["error"] != "unknown_setup_step" {
		t.Errorf("an unknown step: %d %v", c, r)
	}
	c, r := pgCall(t, "POST", path, tok, map[string]interface{}{"answers": answers})
	if c != 200 || r["success"] != true {
		t.Fatalf("the answers: %d %v", c, r)
	}
	results, _ := r["results"].([]interface{})
	if len(results) < len(answers) {
		t.Errorf("one result per answer at least: %d for %d", len(results), len(answers))
	}
	_, after := pgCall(t, "GET", path+"?view=full", tok, nil)
	b, _ = json.Marshal(after["steps"])
	json.Unmarshal(b, &steps)
	for _, st := range steps {
		if _, answered := answers[st.Key]; answered && !st.Done && st.Key != "gear" {
			t.Errorf("step %s answered but still open: %s", st.Key, st.Why)
		}
	}
	if fmt.Sprint(after["steps_left"]) == fmt.Sprint(before["steps_left"]) && !strings.Contains(fmt.Sprint(results), "status:200") {
		t.Errorf("nothing changed: %v -> %v", before["steps_left"], after["steps_left"])
	}
}
