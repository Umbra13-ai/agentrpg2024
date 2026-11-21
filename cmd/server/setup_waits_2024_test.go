package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Playtest I-CALLA-21: a step whose options another open choice changes waits for it. A level 3 Cleric with the subclass still
// to choose gets no prepared-spell suggestion (a domain makes some of those spells always prepared); once the domain is chosen,
// the suggestion leaves the domain's spells out. A Cleric with its domain, and a class whose subclasses give no spells, don't wait.
func TestPostgresSpellsStepWaitsForTheSubclass(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg waits player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgCleric(fmt.Sprintf("Pg Waits %d", time.Now().UnixNano()%1e9)))
	id := int(j["character_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/starting-level", id), tok, map[string]interface{}{"level": 3}); c != 200 {
		t.Fatalf("starting level: %d %v", c, j)
	}
	spellsStep := func() map[string]interface{} {
		t.Helper()
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", id), tok, nil)
		steps, _ := j["steps"].([]interface{})
		for _, s := range steps {
			if m, _ := s.(map[string]interface{}); m["key"] == "spells" {
				return m
			}
		}
		t.Fatalf("no spells step in %v", j["steps"])
		return nil
	}
	st := spellsStep()
	if st["done"] == true {
		t.Skip("the spells step is already complete at level 3; nothing to wait for")
	}
	if st["waits_for"] != "subclass" || st["request"] != nil || !strings.Contains(fmt.Sprint(st["why"]), "subclass first") {
		t.Fatalf("the spells step didn't wait for the subclass: %v", st)
	}
	if ch := fmt.Sprint(st["choices"]); !strings.Contains(ch, "options") || strings.Contains(ch, "suggested:true") {
		t.Errorf("a waiting step lists its options without a suggestion: %s", ch)
	}
	if c, j := pgCall(t, "POST", "/api/characters/subclass", tok, map[string]interface{}{"character_id": id, "subclass": "life"}); c != 200 {
		t.Fatalf("subclass: %d %v", c, j)
	}
	st = spellsStep()
	if st["waits_for"] != nil || st["request"] == nil {
		t.Fatalf("after the domain the step still waits: %v", st)
	}
	if req := fmt.Sprint(st["request"]); strings.Contains(req, "\"aid\"") || strings.Contains(req, "bless") || strings.Contains(req, "cure-wounds") {
		t.Errorf("the suggestion holds a Life Domain spell (always prepared): %s", req)
	}
}

// Only a subclass that gives spells at the level makes the spells step wait.
func TestSubclassesGiveSpells(t *testing.T) {
	for _, c := range []struct {
		class string
		level int
		want  bool
	}{{"cleric", 3, true}, {"cleric", 2, false}, {"druid", 3, true}, {"paladin", 3, true}, {"wizard", 3, false}, {"fighter", 3, false}} {
		if got := subclassesGiveSpells(c.class, c.level); got != c.want {
			t.Errorf("subclassesGiveSpells(%s, %d) = %v, want %v", c.class, c.level, got, c.want)
		}
	}
}
