package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// The spell file's derived fields say what the text says (checked against the SRD 5.2.1 by an outside audit, Nov 23): a spell's saving_throw
// is a save its target makes against it, never a save the spell gives Advantage on (Haste, Beacon of Hope, Gaseous Form had one, and a cast
// rolled it at a willing ally), and a summoned creature's action keeps its whole name (Find Steed's Healing Touch was cut in two).
func TestSpellFileFieldsSayWhatTheTextSays(t *testing.T) {
	var spells []struct {
		Slug        string `json:"slug"`
		Description string `json:"description"`
		Save        string `json:"saving_throw"`
		Statblock   *struct {
			Sections []struct {
				Entries []struct{ Name, Text string } `json:"entries"`
			} `json:"sections"`
		} `json:"statblock"`
	}
	if err := json.Unmarshal(srd521SpellsJSON, &spells); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{"STR": "strength", "DEX": "dexterity", "CON": "constitution", "INT": "intelligence", "WIS": "wisdom", "CHA": "charisma"}
	forced := func(ability, text string) bool {
		a := regexp.QuoteMeta(ability)
		return regexp.MustCompile(`(make|makes|succeed on|succeeds on|fail|fails) (an? )?`+a+` saving throw`).MatchString(text) ||
			regexp.MustCompile(a+` saving throw\.? ?(on a|failure|success)`).MatchString(text) ||
			regexp.MustCompile(`must (succeed on|make) (an? )?`+a).MatchString(text)
	}
	for _, s := range spells {
		if s.Save != "" && !forced(names[s.Save], strings.ToLower(s.Description)) {
			t.Errorf("%s: saving_throw %s, but its text forces no such save (a benefit's Advantage is not a save against the spell)", s.Slug, s.Save)
		}
		if s.Statblock == nil {
			continue
		}
		for _, sec := range s.Statblock.Sections {
			for _, e := range sec.Entries {
				if strings.Count(e.Name, "(") != strings.Count(e.Name, ")") || (strings.TrimSpace(e.Text) == "" && e.Name != "") {
					t.Errorf("%s: the entry %q is cut: its name's parenthesis is open or it has no text", s.Slug, e.Name)
				}
			}
		}
	}
}
