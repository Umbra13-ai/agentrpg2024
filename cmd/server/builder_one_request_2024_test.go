package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// The agent's builder report (owner, Nov 23), items 1 and 7: /finish takes the draft's fields (the backstory too) and creates the character
// in the same request; the builder's and setup's replies are short after the first (?view=full for the whole text, ?view=short any time).

// For every class and every background: the whole draft, answered as the suggestions would answer it, sent to /finish of an empty draft
// in one request, creates the character.
func TestPostgresFinishTakesTheWholeDraftForEveryClassAndBackground(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "builder one request")
	for _, class := range []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"} {
		for _, bg := range game.BackgroundSlugs() {
			// the answers: a scratch draft filled by its suggestions
			_, d := builderCall(t, tok, "POST", "/api/character-builder", map[string]interface{}{"class": class, "background": bg, "alignment": "neutral"})
			draftID := int(d["draft_id"].(float64))
			scratch := fmt.Sprintf("/api/character-builder/%d", draftID)
			for i := 0; i < 12; i++ {
				builderCall(t, tok, "POST", scratch, map[string]interface{}{"use_suggestion": true})
			}
			answers, _ := loadDraftBody(draftID)
			builderCall(t, tok, "DELETE", scratch, nil)
			delete(answers, "backstory")
			answers["name"] = fmt.Sprintf("One %s %s %d", class, bg, time.Now().UnixNano()%1e9)
			answers["backstory"] = "Raised on the road.\n\nLeft home to see the world."
			// one request: an empty draft, then /finish with every answer
			_, d = builderCall(t, tok, "POST", "/api/character-builder", map[string]interface{}{})
			path := fmt.Sprintf("/api/character-builder/%d", int(d["draft_id"].(float64)))
			c, res := builderCall(t, tok, "POST", path+"/finish", answers)
			if c != 200 || res["character_id"] == nil {
				t.Errorf("%s / %s: /finish with the whole draft: %d %v", class, bg, c, res["message"])
				continue
			}
			var backstory string
			db.QueryRow(`SELECT COALESCE(backstory,'') FROM characters WHERE id = $1`, int(res["character_id"].(float64))).Scan(&backstory)
			if !strings.Contains(backstory, "Raised on the road") {
				t.Errorf("%s / %s: the backstory sent to /finish is not the character's: %q", class, bg, backstory)
			}
		}
	}
}

// loadDraftBody: a draft's stored answers.
func loadDraftBody(id int) (map[string]interface{}, bool) {
	var raw []byte
	if db.QueryRow(`SELECT body FROM character_drafts WHERE id = $1`, id).Scan(&raw) != nil {
		return nil, false
	}
	b := map[string]interface{}{}
	return b, json.Unmarshal(raw, &b) == nil
}

// /finish with fields: a refused answer or an open step creates nothing (the valid answers are saved); an unknown field saves nothing; a
// finished draft takes no more fields. The views: the start is full, later replies short, ?view=full the whole text; setup's first read full.
func TestPostgresFinishFieldsAndGuideViews(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "builder views")
	c, d := builderCall(t, tok, "POST", "/api/character-builder", map[string]interface{}{"class": "bard"})
	if c != 200 || d["view"] != "full" {
		t.Fatalf("a draft's start is full: %d %v", c, d["view"])
	}
	path := fmt.Sprintf("/api/character-builder/%d", int(d["draft_id"].(float64)))
	if c, v := builderCall(t, tok, "POST", path+"/finish", map[string]interface{}{"backstory": "A short life so far."}); c != 400 || v["error"] != "draft_not_finished" || !strings.Contains(fmt.Sprint(v["message"]), "are saved") {
		t.Errorf("finish with an open step: %d %v %v", c, v["error"], v["message"])
	}
	_, v := builderCall(t, tok, "GET", path, nil)
	if v["view"] != "short" || strings.Contains(fmt.Sprint(v), "option_details") || !strings.Contains(fmt.Sprint(v["draft"]), "A short life so far.") {
		t.Errorf("a later read is short and the backstory /finish saved is in the draft: view %v", v["view"])
	}
	if _, full := builderCall(t, tok, "GET", path+"?view=full", nil); full["view"] != "full" || !strings.Contains(fmt.Sprint(full), "option_details") {
		t.Errorf("?view=full gives the whole text: %v", full["view"])
	}
	if c, v := builderCall(t, tok, "POST", path+"/finish", map[string]interface{}{"no_such_field": 1}); c != 400 || v["error"] != "unknown_field" {
		t.Errorf("finish with an unknown field: %d %v", c, v["error"])
	}
	if c, v := builderCall(t, tok, "POST", path+"/finish", map[string]interface{}{"class": "not-a-class"}); c == 200 || v["character_id"] != nil {
		t.Errorf("finish with a refused answer created a character: %d %v", c, v)
	}
	// filled and finished: the reply has the character; a second /finish with fields changes nothing
	for i := 0; i < 12; i++ {
		builderCall(t, tok, "POST", path, map[string]interface{}{"class": "bard", "use_suggestion": true, "alignment": "neutral"})
	}
	builderCall(t, tok, "POST", path, map[string]interface{}{"name": fmt.Sprintf("View Bard %d", time.Now().UnixNano()%1e9)})
	c, res := builderCall(t, tok, "POST", path+"/finish", nil)
	if c != 200 || res["character_id"] == nil {
		t.Fatalf("finish: %d %v", c, res)
	}
	if c, v := builderCall(t, tok, "POST", path+"/finish", map[string]interface{}{"backstory": "Again."}); c != 400 || v["error"] != "already_created" {
		t.Errorf("a finished draft takes no more fields: %d %v", c, v["error"])
	}
	setup := fmt.Sprintf("/api/characters/%d/setup", int(res["character_id"].(float64)))
	for i, want := range []string{"full", "short", "full", "short"} {
		q := map[int]string{2: "?view=full", 3: "?view=short"}[i]
		if _, s := builderCall(t, tok, "GET", setup+q, nil); s["view"] != want {
			t.Errorf("setup read %d%s: view %v, want %s", i+1, q, s["view"], want)
		}
	}
}
