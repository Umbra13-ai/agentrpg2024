package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The trait table is the one owner of the traits the server applies: every row names a real SRD trait (or matches one by its
// wording), the GM's view says for each trait who applies it, and no handler looks a trait up any other way.
func TestMonsterTraitsHaveOneOwner(t *testing.T) {
	all := map[string]int{}
	applied := map[string]bool{}
	for _, m := range srdMonsters2024() {
		for _, tr := range m.Traits {
			all[traitName(tr.Name)]++
			if _, ok := traitRuleFor(tr); ok {
				applied[traitName(tr.Name)] = true
			}
		}
	}
	for _, r := range monsterTraitRules {
		if r.Name != "" && all[r.Name] == 0 {
			t.Errorf("monsterTraitRules names %q, which no SRD monster has", r.Name)
		}
		if r.Match != nil {
			found := false
			for _, m := range srdMonsters2024() {
				for _, tr := range m.Traits {
					found = found || r.Match.MatchString(tr.Text)
				}
			}
			if !found {
				t.Errorf("the wording rule %q matches no SRD trait", r.Does)
			}
		}
	}
	t.Logf("the server applies %d of the %d SRD trait names; the GM applies the rest", len(applied), len(all))

	wolf := traitsForGM("wolf")
	if len(wolf) == 0 || wolf[0]["server_applies"] != true || !strings.Contains(wolf[0]["server_does"].(string), "Advantage") {
		t.Errorf("the wolf's Pack Tactics in the GM's view: %v", wolf)
	}
	for _, e := range traitsForGM("crocodile") { // Hold Breath: the GM's
		if strings.HasPrefix(e["name"].(string), "Hold Breath") && e["server_applies"] != false {
			t.Errorf("Hold Breath marked as applied by the server: %v", e)
		}
	}

	names := map[string]bool{}
	for _, r := range monsterTraitRules {
		names[r.Name] = true
	}
	call := regexp.MustCompile(`monsterHasTrait\([^,]+,[^,]+,\s*"([^"]+)"\)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			for _, m := range call.FindAllStringSubmatch(line, -1) {
				if !names[m[1]] {
					t.Errorf("%s:%d asks monsterHasTrait for %q, which is not a row of monsterTraitRules", f, n+1, m[1])
				}
			}
			if strings.Contains(line, "traits::text") {
				t.Errorf("%s:%d searches the trait text in the database: use monsterHasTrait", f, n+1)
			}
		}
	}
}
