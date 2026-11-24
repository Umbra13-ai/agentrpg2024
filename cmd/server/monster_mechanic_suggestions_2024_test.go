package main

import (
	"fmt"
	"strings"
	"testing"
)

// The import report suggests a mechanic for a sentence no rule reads, only one that attaches to that kind of entry; every kind has hints.
func TestImportReportSuggestsMechanics(t *testing.T) {
	for _, k := range mechanicCatalog {
		if len(k.Hints) == 0 {
			t.Errorf("%s: no Hints", k.Kind)
		}
		for _, h := range k.Hints {
			if h != strings.ToLower(h) || strings.TrimSpace(h) == "" {
				t.Errorf("%s: hint %q is not lowercase words", k.Kind, h)
			}
		}
	}
	attack := srdMonsterEntry{Name: "Torch Swipe", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 6 (1d8 + 2) Fire damage. The target's clothes catch fire."}
	suggested := func(group string, e srdMonsterEntry) string {
		var kinds []string
		for _, g := range entryReport(group, e)["gm_resolves"].([]map[string]interface{}) {
			for _, s := range asMaps(g["suggest_mechanics"]) {
				kinds = append(kinds, fmt.Sprint(s["kind"]))
			}
		}
		return strings.Join(kinds, ",")
	}
	if got := suggested("action", attack); !strings.Contains(got, "burning") {
		t.Errorf("the attack's unread fire sentence: suggestions %q, want burning", got)
	}
	trait := srdMonsterEntry{Name: "Smoldering", Text: "The clothes of anyone who touches it catch fire."}
	if got := suggested("trait", trait); strings.Contains(got, "burning") {
		t.Errorf("burning attaches to a hit or a save, never a trait: %q", got)
	}
	read := srdMonsterEntry{Name: "Bite", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 6 (1d8 + 2) Piercing damage."}
	for _, g := range entryReport("action", read)["gm_resolves"].([]map[string]interface{}) {
		if g["suggest_mechanics"] != nil {
			t.Errorf("a sentence the server reads got a suggestion: %v", g)
		}
	}
}

func asMaps(v interface{}) []map[string]interface{} {
	m, _ := v.([]map[string]interface{})
	return m
}
