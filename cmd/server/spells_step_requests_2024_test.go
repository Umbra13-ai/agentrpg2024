package main

import (
	"fmt"
	"testing"
	"time"
)

// Playtest S-GM-14: a Wizard's spells step is two requests (the spellbook, then the prepared list) and the page showed one. The step
// lists every request in order with how many are left; Request stays the first.
func TestPostgresSpellsStepListsEveryRequest(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg spells requests")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Book %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgExec(t, `UPDATE characters SET class = 'wizard', level = 3, intl = 16, known_spells = '[]', prepared_spells = '[]' WHERE id = $1`, char)
	_, setup := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", char), tok, nil)
	var spells map[string]interface{}
	for _, s := range setup["steps"].([]interface{}) {
		if m := s.(map[string]interface{}); m["key"] == "spells" {
			spells = m
		}
	}
	if spells == nil {
		t.Fatalf("no spells step: %v", setup["steps"])
	}
	reqs, _ := spells["requests"].([]interface{})
	if len(reqs) != 2 || int(spells["requests_left"].(float64)) != 2 {
		t.Fatalf("a Wizard's spells step lists both requests: %v", spells)
	}
	first, second := reqs[0].(map[string]interface{}), reqs[1].(map[string]interface{})
	fr, sr := first["request"].(map[string]interface{}), second["request"].(map[string]interface{})
	if fr["method"] != "PUT" || sr["method"] != "POST" || fmt.Sprint(fr["path"]) != fmt.Sprint(spells["request"].(map[string]interface{})["path"]) {
		t.Errorf("the requests are the spellbook then the prepared list, the first as Request: %v", reqs)
	}
	// a class that only prepares has one request and no list of several
	pgExec(t, `UPDATE characters SET class = 'cleric', known_spells = '[]', prepared_spells = '[]' WHERE id = $1`, char)
	_, setup = pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", char), tok, nil)
	for _, s := range setup["steps"].([]interface{}) {
		if m := s.(map[string]interface{}); m["key"] == "spells" && m["requests"] != nil {
			t.Errorf("a Cleric's spells step is one request: %v", m)
		}
	}
}
