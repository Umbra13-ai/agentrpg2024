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
		// S-CALLA-10: a DC and the module's own words
		"OOC: thieves' tools with a DC 10 Dexterity check, or a DC 20 Strength check to break the chain.",
		"The module gives two direct ways to free this chain.",
		"The adventure says the guard sleeps until dawn.",
		// S-CALLA-12: the server's own record numbers
		"As in narration 4709, the camp wakes.",
		"Millie's actual Search 5223 uses its recorded Perception result.",
		"Aldren actual5174 accepts the expense.",
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
		"The chain is old but thick; the lock looks crude.",
		"The DCs of Waterdeep hold their council at noon.",
		"In the second narration the camp wakes; the action of round 3 was brave.",
		"Millie's Search finds a loose stone.",
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
