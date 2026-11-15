package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestNoOtherConditionWriter: condition_records is the one store of a creature's conditions and characters.conditions is the list
// made from it, both written only by the condition owner (condition_owner_2024.go) inside one locked transaction. Before Nov 15
// 2026, 23 whole-list writes in 11 files and 42 unlocked sync passes kept two stores in step, and parallel changes lost conditions
// in both (audit AUD-08); Topple and Grapple inserted their records past the owner's Immunity checks (AUD-38).
func TestNoOtherConditionWriter(t *testing.T) {
	const owner = "condition_owner_2024.go"
	records := regexp.MustCompile(`(?is)(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+condition_records\b`)
	update := regexp.MustCompile(`(?is)UPDATE\s+characters\b(.*?)\bWHERE\b`)
	assign := regexp.MustCompile(`(?i)(\bSET\s+|,\s*)(conditions|exhaustion_level)\s*=`)
	insert := regexp.MustCompile(`(?is)INSERT\s+INTO\s+characters\s*\(([^)]*)\)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == owner {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		line := func(at int) int { return strings.Count(src[:at], "\n") + 1 }
		for _, m := range records.FindAllStringIndex(src, -1) {
			t.Errorf("%s:%d writes condition_records itself: use the condition owner (%s)", f, line(m[0]), owner)
		}
		for _, m := range update.FindAllStringSubmatchIndex(src, -1) {
			if assign.MatchString(src[m[2]:m[3]]) {
				t.Errorf("%s:%d writes a character's conditions or Exhaustion itself: use the condition owner (%s)", f, line(m[0]), owner)
			}
		}
		for _, m := range insert.FindAllStringSubmatchIndex(src, -1) {
			if regexp.MustCompile(`(?i)\b(conditions|exhaustion_level)\b`).MatchString(src[m[2]:m[3]]) {
				t.Errorf("%s:%d creates a character with conditions: they are written by the condition owner (%s)", f, line(m[0]), owner)
			}
		}
	}
}

// TestEditorsUseTheConditionOwner: the admin page and the GM's "set" write any column generically; the condition columns go to the
// owner instead, so the list stays the records.
func TestEditorsUseTheConditionOwner(t *testing.T) {
	if !conditionColumns["conditions"] || !conditionColumns["exhaustion_level"] {
		t.Fatalf("conditionColumns = %v, want conditions and exhaustion_level", conditionColumns)
	}
	for f, call := range map[string]string{"admin_2024.go": "setConditionColumnIn(", "gm_character_edit.go": "setConditionColumn("} {
		b, _ := os.ReadFile(f)
		if !strings.Contains(string(b), "conditionColumns[") || !strings.Contains(string(b), call) {
			t.Errorf("%s writes columns generically and must send the condition columns to %s", f, call)
		}
	}
}

func TestConditionEntries(t *testing.T) {
	for in, want := range map[string][2]string{"charmed:12": {"charmed", "12"}, " Prone ": {"prone", ""}, "sanctuary:14": {"sanctuary", "14"},
		"sacred_weapon:2:100": {"sacred_weapon", "2:100"}, "invisible:one_with_shadows": {"invisible", "one_with_shadows"}, `"dodging"`: {"dodging", ""}} {
		if c, d := splitConditionEntry(in); c != want[0] || d != want[1] {
			t.Errorf("splitConditionEntry(%q) = %q, %q; want %q, %q", in, c, d, want[0], want[1])
		}
		if got := conditionBase(in); got != want[0] {
			t.Errorf("conditionBase(%q) = %q, want %q", in, got, want[0])
		}
	}
	for _, c := range []string{"poisoned", "charmed:3", "unconscious", "invisible"} {
		if !isRuleCondition(c) {
			t.Errorf("%s is a rules condition", c)
		}
	}
	for _, c := range []string{"raging", "reckless", "dodging", "hidden", "sanctuary:12", "sacred_weapon:2:100", "exhaustion:2"} {
		if isRuleCondition(c) {
			t.Errorf("%s is a marker, not a rules condition", c)
		}
	}
}

// TestConditionListFromRecords: the character's list is its records, each entry once in the order added, then its Exhaustion.
func TestConditionListFromRecords(t *testing.T) {
	recs := []conditionRecord{
		{Condition: "poisoned", Source: "Giant Spider"},
		{Condition: "raging"},
		{Condition: "poisoned", Source: "Potion of Poison", DurationRounds: 600}, // a second instance: the list shows it once
		{Condition: "charmed", Detail: "12"},
		{Condition: "sanctuary", Detail: "14"},
	}
	want := []string{"poisoned", "raging", "charmed:12", "sanctuary:14", "exhaustion:2"}
	if got := conditionList(recs, 2); !reflect.DeepEqual(got, want) {
		t.Errorf("conditionList = %v, want %v", got, want)
	}
	if got := conditionList(nil, 0); got == nil || len(got) != 0 {
		t.Errorf("no records, no Exhaustion: want an empty list ([]), got %#v", got)
	}
	if got := conditionList(nil, 9); !reflect.DeepEqual(got, []string{"exhaustion:6"}) {
		t.Errorf("Exhaustion stops at 6: got %v", got)
	}
}

// TestConditionImmunitiesAreOneTable: every source of condition Immunity is a row of conditionImmunities, which addCondition asks;
// no path asks a source of Immunity itself before applying a condition.
func TestConditionImmunitiesAreOneTable(t *testing.T) {
	if len(conditionImmunities) < 7 {
		t.Fatalf("conditionImmunities has %d rows: the stat block, spells and items, Heroism, auras, Fey Ancestry, Nature's Ward and Mindless Rage", len(conditionImmunities))
	}
	ask := regexp.MustCompile(`\b(buffConditionImmune|natureWardImmune|isImmuneToMagicalSleep|monsterConditionImmune|heroismImmune)\(`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "condition_owner_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, l := range strings.Split(string(b), "\n") {
			if ask.MatchString(l) && !strings.HasPrefix(strings.TrimSpace(l), "func ") && !strings.HasPrefix(strings.TrimSpace(l), "//") {
				t.Errorf("%s:%d asks a source of condition Immunity itself: apply the condition with addCondition (its Immune answer) or ask immuneToCondition", f, n+1)
			}
		}
	}
}
