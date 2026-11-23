package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/docs"
)

var guideStatBlockRe = regexp.MustCompile("(?s)```(statblock|statblock-gm)\n(.*?)\n```")

// TestGuideMonsterExamplesRun: every stat block the GM guide shows is put through the reader and the report, so the guide cannot drift
// from what the server reads. A `statblock` runs top to bottom (it imports, reads with no note, and hands nothing to the GM); a
// `statblock-gm` is wording the server does not read (it imports, and every entry hands at least one sentence to the GM).
func TestGuideMonsterExamplesRun(t *testing.T) {
	found := guideStatBlockRe.FindAllStringSubmatch(docs.GMGuide, -1)
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
	if kinds["statblock"] < 1 || kinds["statblock-gm"] < 1 {
		t.Fatalf("the guide shows %v stat block examples: it needs one the server runs and one it hands to the GM", kinds)
	}
}
