package main

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/docs"
)

var guideStatBlockRe = regexp.MustCompile("(?s)```(statblock|statblock-gm|statblock-template)\n(.*?)\n```")

// TestGuideMonsterExamplesRun: every stat block the monsters guide (docs/gm_monsters.md) shows is put through the reader and the report, so the guide cannot drift
// from what the server reads. A `statblock` runs top to bottom (it imports, reads with no note, and hands nothing to the GM); a
// `statblock-gm` is wording the server does not read (it imports, and every entry hands at least one sentence to the GM); the
// `statblock-template` is the layout to copy: it reads with no note and imports.
func TestGuideMonsterExamplesRun(t *testing.T) {
	found := guideStatBlockRe.FindAllStringSubmatch(docs.GMMonsters, -1)
	kinds := map[string]int{}
	for _, f := range found {
		kinds[f[1]]++
		m, notes := readStatBlockText(f[2])
		normalizeCustomMonster(&m)
		if len(notes) != 0 {
			t.Errorf("%s: the guide's example reads with notes %v", m.Name, notes)
		}
		if msg := validateCustomMonster(m); msg != "" {
			t.Errorf("%s: the guide's example does not import: %s", m.Name, msg)
		}
		if kinds[f[1]] > 0 && f[1] == "statblock-template" {
			continue // nothing more is asked of the template: it reads and it imports
		}
		if !strings.HasPrefix(m.Name, "Test ") { // the repository's mark of an invented monster
			t.Errorf("%s: a guide example is an invented monster, named \"Test ...\"", m.Name)
		}
		for _, r := range entryReports(metaFromMonster("guide", m)) {
			handed := len(r["gm_resolves"].([]map[string]interface{}))
			switch f[1] {
			case "statblock":
				if handed != 0 {
					t.Errorf("%s / %s: the guide says the server runs this entry, but it hands %d sentence(s) to the GM: %v", m.Name, r["name"], handed, r["gm_resolves"])
				}
			case "statblock-gm":
				if handed == 0 {
					t.Errorf("%s / %s: the guide says the server does not read this entry, but it hands nothing to the GM", m.Name, r["name"])
				}
			}
		}
		if f[1] == "statblock" && len(customMonsterWarnings(m)) > 2 {
			t.Errorf("%s: the guide's example draws warnings: %v", m.Name, customMonsterWarnings(m))
		}
	}
	if kinds["statblock"] < 1 || kinds["statblock-gm"] < 1 || kinds["statblock-template"] != 1 {
		t.Fatalf("the guide shows %v stat blocks: it needs one the server runs, one it hands to the GM and the template", kinds)
	}
}

// TestMonstersGuideIsServedAndLinked: the guide is served at /gm-monsters.md with this server's address, and the places a GM looks (the GM
// guide, the skill guide, the importer's own words) point to it.
func TestMonstersGuideIsServedAndLinked(t *testing.T) {
	w := httptest.NewRecorder()
	handleGMMonstersGuide(w, httptest.NewRequest("GET", "/gm-monsters.md", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(body, "# Agent RPG 2024: monsters from your own books") {
		t.Fatalf("the guide: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if strings.Contains(body, "YOUR-SERVER") {
		t.Error("the served guide still says YOUR-SERVER")
	}
	for name, doc := range map[string]string{"gm_guide.md": docs.GMGuide, "skill.md": docs.Skill} {
		if !strings.Contains(doc, "https://YOUR-SERVER/gm-monsters.md") {
			t.Errorf("%s does not point to the monsters guide", name)
		}
	}
	for _, section := range []string{"## What this does, and what it does not", "## The short way", "## The layout the reader expects", "## Reading the preview",
		"## Wording the server reads", "## Running it in play", "## Keeping your compendium", "## Limits and refusals", "## When it does not read as you meant"} {
		if !strings.Contains(docs.GMMonsters, section) {
			t.Errorf("the monsters guide lost its section %q", section)
		}
	}
}
