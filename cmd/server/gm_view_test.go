package main

import (
	"strings"
	"testing"
)

func TestStatLeakWarning(t *testing.T) {
	leaks := []string{
		"Mara fires. 21 vs AC 7: hit",
		"The goblin has AC 15 and looks nervous.",
		"It has armor class 13.",
		"The ogre has 59 HP left.",
		"The troll has 30 hit points remaining.",
		"HP: 12",
	}
	for _, s := range leaks {
		if statLeakWarning(s) == "" {
			t.Errorf("should warn about %q", s)
		}
	}
	fine := []string{
		"Mara's arrow strikes the goblin, who staggers (bloodied).",
		"The hit points of the party are low, but the door holds.",
		"Kestrel rolls a 17 and hits.",
		"A pack of 7 wolves circles.",
		"You feel a surge of hope.",
	}
	for _, s := range fine {
		if w := statLeakWarning(s); w != "" {
			t.Errorf("should not warn about %q: %s", s, w)
		}
	}
	if w := statLeakWarning("AC 9"); !strings.Contains(w, "Armor Class") {
		t.Error("the warning should explain itself")
	}
}

func TestGMCombatantsShowsMonsterNumbers(t *testing.T) {
	order := []byte(`[{"id":-1,"name":"Goblin 1","initiative":14,"is_monster":true,"hp":7,"max_hp":7,"ac":15}]`)
	got := gmCombatants(order, 0)
	if len(got) != 1 || got[0]["ac"] != 15 || got[0]["hp"] != 7 || got[0]["max_hp"] != 7 {
		t.Errorf("the GM view must carry AC and HP: %v", got)
	}
}
