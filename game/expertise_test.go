package game

import (
	"strings"
	"testing"
)

func heldSet(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestSkillsTable(t *testing.T) {
	if len(Skills) != 18 {
		t.Fatalf("the SRD has 18 skills, the table has %d", len(Skills))
	}
	for _, spelling := range []string{"Sleight of Hand", "sleight_of_hand", "sleight-of-hand", "SLEIGHT  OF HAND", "sleightofhand"} {
		if n, ok := CanonSkill(spelling); !ok || n != "sleight of hand" {
			t.Errorf("CanonSkill(%q) = %q, %v", spelling, n, ok)
		}
	}
	if _, ok := CanonSkill("flying"); ok {
		t.Error("flying is not a skill")
	}
	if sk, _ := LookupSkill("animal-handling"); sk.Ability != "wis" || sk.AbilityName != "Wisdom" {
		t.Errorf("Animal Handling is a Wisdom skill, got %+v", sk)
	}
	if SkillSlug("Animal Handling") != "animal_handling" {
		t.Error("SkillSlug")
	}
}

// Every class, every level: the count follows the SRD class tables (Bard 2 and 9, Ranger 2 and 9, Rogue 1 and 6, Wizard 2).
func TestExpertiseCountEveryClassAndLevel(t *testing.T) {
	want := map[string]func(l int) int{
		"bard":   func(l int) int { return b2i(l >= 2)*2 + b2i(l >= 9)*2 },
		"ranger": func(l int) int { return b2i(l >= 2) + b2i(l >= 9)*2 },
		"rogue":  func(l int) int { return b2i(l >= 1)*2 + b2i(l >= 6)*2 },
		"wizard": func(l int) int { return b2i(l >= 2) },
	}
	for _, c := range []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"} {
		for l := 1; l <= 20; l++ {
			w := 0
			if f := want[c]; f != nil {
				w = f(l)
			}
			if got := ExpertiseAllowed(c, l); got != w {
				t.Errorf("%s level %d: Expertise in %d skills, want %d", c, l, got, w)
			}
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestCheckExpertise(t *testing.T) {
	held := heldSet("arcana", "insight", "perception", "stealth", "history", "sleight of hand")
	none := map[string]bool{}
	cases := []struct {
		name     string
		levels   map[string]int
		other    map[string]bool
		picks    []string
		complete bool
		code     string
	}{
		{"rogue 1 takes any two proficient skills", map[string]int{"rogue": 1}, none, []string{"Stealth", "sleight_of_hand"}, true, ""},
		{"rogue 1: a skill twice", map[string]int{"rogue": 1}, none, []string{"stealth", "Stealth"}, true, "duplicate_skill"},
		{"rogue 1: not proficient", map[string]int{"rogue": 1}, none, []string{"stealth", "athletics"}, true, "not_proficient"},
		{"rogue 1: not a skill", map[string]int{"rogue": 1}, none, []string{"stealth", "flying"}, true, "invalid_skill"},
		{"rogue 1: one of two, complete", map[string]int{"rogue": 1}, none, []string{"stealth"}, true, "invalid_count"},
		{"rogue 1: one of two, in progress", map[string]int{"rogue": 1}, none, []string{"stealth"}, false, ""},
		{"rogue 6 has four", map[string]int{"rogue": 6}, none, []string{"stealth", "arcana", "insight", "history"}, true, ""},
		{"bard 1 has none", map[string]int{"bard": 1}, none, []string{"stealth", "arcana"}, true, "no_expertise"},
		{"fighter has none", map[string]int{"fighter": 20}, none, []string{"stealth"}, true, "no_expertise"},
		{"ranger 2: Deft Explorer, one of any", map[string]int{"ranger": 2}, none, []string{"perception"}, true, ""},
		{"ranger 9: three", map[string]int{"ranger": 9}, none, []string{"perception", "stealth", "insight"}, true, ""},
		{"wizard 2: Scholar takes Arcana", map[string]int{"wizard": 2}, none, []string{"arcana"}, true, ""},
		{"wizard 2: Scholar refuses Insight (proficient, not on its list)", map[string]int{"wizard": 2}, none, []string{"insight"}, true, "skill_not_allowed"},
		{"wizard 2: Scholar refuses Religion (on its list, not proficient)", map[string]int{"wizard": 2}, none, []string{"religion"}, true, "not_proficient"},
		{"a skill with Expertise from another source can't be picked", map[string]int{"rogue": 1}, heldSet("stealth"), []string{"stealth", "arcana"}, true, "already_expertise"},
		{"wizard 2 / rogue 1: Scholar's pick must be on its list", map[string]int{"wizard": 2, "rogue": 1}, none, []string{"stealth", "insight", "perception"}, true, "skill_not_allowed"},
		{"wizard 2 / rogue 1: one Scholar skill and two others", map[string]int{"wizard": 2, "rogue": 1}, none, []string{"stealth", "insight", "history"}, true, ""},
	}
	for _, c := range cases {
		_, code, msg := CheckExpertise(c.levels, held, c.other, c.picks, c.complete)
		if code != c.code {
			t.Errorf("%s: code %q (%s), want %q", c.name, code, msg, c.code)
		}
		if code != "" && code != "no_expertise" && !strings.Contains(msg, "Your options:") {
			t.Errorf("%s: the refusal doesn't list the options: %s", c.name, msg)
		}
	}
}

func TestExpertiseOptionsAndSuggestion(t *testing.T) {
	held := heldSet("arcana", "insight", "perception", "stealth", "history", "sleight of hand", "persuasion")
	none := map[string]bool{}
	if got := strings.Join(ExpertiseOptions(map[string]int{"wizard": 5}, held, none), ","); got != "arcana,history" {
		t.Errorf("a Wizard's options are Scholar's skills it is proficient in, got %s", got)
	}
	if got := strings.Join(ExpertiseOptions(map[string]int{"rogue": 1}, held, heldSet("stealth")), ","); strings.Contains(got, "stealth") {
		t.Errorf("a skill with Expertise from another source isn't an option: %s", got)
	}
	// every class that gets Expertise, every level: the suggestion is accepted as a complete answer
	for _, g := range ExpertiseGrants {
		for l := g.Level; l <= 20; l++ {
			levels := map[string]int{g.Class: l}
			s := SuggestExpertise(levels, held, none, nil, []string{"stealth", "perception"})
			if _, code, msg := CheckExpertise(levels, held, none, s, true); code != "" {
				t.Errorf("%s level %d: the suggestion %v is refused: %s %s", g.Class, l, s, code, msg)
			}
		}
	}
	if s := SuggestExpertise(map[string]int{"rogue": 1}, held, none, nil, nil); strings.Join(s, ",") != "sleight of hand,stealth" {
		t.Errorf("a Rogue's suggestion is the SRD's recommendation, got %v", s)
	}
	if s := SuggestExpertise(map[string]int{"rogue": 6}, held, none, []string{"arcana", "insight"}, nil); s[0] != "arcana" || s[1] != "insight" || len(s) != 4 {
		t.Errorf("the picks a character has are kept, got %v", s)
	}
	if !strings.Contains(ExpertiseSummary(), "Wizard at level 2 (Scholar)") || !strings.Contains(ExpertiseSummary(), "Rogue at levels 1 and 6") {
		t.Errorf("summary: %s", ExpertiseSummary())
	}
}
