package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A registered trait must have evidence in the SRD or a representative
// invented imported trait. The GM's view and handlers use the same registry.
func TestMonsterTraitsHaveOneOwner(t *testing.T) {
	importedExamples := []srdMonsterEntry{
		{Name: "Agile", Text: "Its movement doesn't provoke Opportunity Attacks."},
		{Name: "Thunder Daze", Text: "If the creature takes Thunder damage, it has Disadvantage on attack rolls and ability checks until the end of its next turn."},
		// a saving throw aura at the end of the monster's turn (invented with a Monster Manual pattern: Manes Vaporspawn)
		{Name: "Choking Fumes", Text: "Constitution Saving Throw: DC 12, each creature in a 5-foot Emanation originating from the wretch at the end of the wretch's turn. Failure: The target has the Incapacitated condition until the end of its next turn. Success: The target is immune to this wretch's Choking Fumes for 24 hours."},
	}
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
	importedNames := map[string]bool{}
	for _, trait := range importedExamples {
		importedNames[trait.Name] = true
	}
	for _, r := range monsterTraitRules {
		if r.Name != "" && all[r.Name] == 0 && !importedNames[r.Name] {
			t.Errorf("monsterTraitRules names %q without an SRD or imported example", r.Name)
		}
		if r.Match != nil {
			found := false
			for _, tr := range importedExamples {
				found = found || r.Match.MatchString(tr.Text)
			}
			for _, m := range srdMonsters2024() {
				for _, tr := range m.Traits {
					found = found || r.Match.MatchString(tr.Text)
				}
			}
			if !found {
				t.Errorf("the wording rule %q matches no SRD or imported fixture trait", r.Does)
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

// The GM's list of a monster's automatic traits is the table's Aura rows (HANDOFF item 24: monster_features_2024.go named
// Aura of Authority and repeated the aura and Regeneration patterns for its list): a Hobgoblin Captain lists Aura of Authority, a
// Troll its Regeneration, a Goblin nothing.
func TestAuraTraitsComeFromTheTable(t *testing.T) {
	for key, want := range map[string]string{"hobgoblin-captain": "Aura of Authority", "troll": "Regeneration", "goblin-warrior": ""} {
		meta := monsterMetaFor(key)
		if meta == nil {
			t.Fatalf("no stat block for %s", key)
		}
		got := strings.Join(auraTraits(meta), ", ")
		if (want == "" && got != "") || !strings.Contains(got, want) {
			t.Errorf("%s: automatic traits %q, want %q", key, got, want)
		}
	}
}
