package main

import "testing"

func TestPlayerText(t *testing.T) {
	cases := map[string]string{
		"Ogre attacks Aldren. Greatclub: 11 vs AC 18 - MISS.":                                       "Ogre attacks Aldren. Greatclub: MISS.",
		"Goblin B attacks Brisa. Shortbow: 9 with Disadvantage vs AC 16 - MISS.":                     "Goblin B attacks Brisa. Shortbow: with Disadvantage - MISS.",
		"Rend: 22 vs AC 16 - HIT, 16 Slashing damage (Mage now has 356 HP); Rend: 8 vs AC 16 - MISS": "Rend: HIT, 16 Slashing damage (Mage now has 356 HP); Rend: MISS",
		"Rend: 7 with Disadvantage [Bane -1] vs AC 16 - MISS":                                        "Rend: with Disadvantage [Bane -1] - MISS",
		"Shortbow: 10 with Disadvantage (long range) vs AC 15 - MISS.":                               "Shortbow: with Disadvantage (long range) - MISS.",
		"Goblin 1 fails its DEX save (3 vs DC 18) and is outlined.":                                  "Goblin 1 fails its DEX save and is outlined.",
		"Goblin 1 saves (15 vs DC 18).":                                                              "Goblin 1 saves.",
		"Goblin 1: Constitution save 15 vs DC 14, success; Goblin 1 takes 16 poison damage.":         "Goblin 1: Constitution save, success; Goblin 1 takes 16 poison damage.",
		"Mage tries to grapple Goblin 1: Dexterity saving throw 14 vs DC 17, failure.":               "Mage tries to grapple Goblin 1: Dexterity saving throw, failure.",
		"Goblin 2 makes a Charisma save: 12 vs DC 15, failure: it disappears":                        "Goblin 2 makes a Charisma save, failure: it disappears",
		"Goblin 1 repeats the Wisdom saving throw at the end of its turn and fails (2): the paralyzed condition continues.": "Goblin 1 repeats the Wisdom saving throw at the end of its turn and fails: the paralyzed condition continues.",
		"Goblin 1 tries to escape: 15 (d20 13 + 2) vs DC 17, failure.":                               "Goblin 1 tries to escape: failure.",
		"Goblin 1 repeats the WIS save against Slow and succeeds (12): the spell ends on it.":         "Goblin 1 repeats the WIS save against Slow and succeeds: the spell ends on it.",
		// a player's own numbers stay
		"Attack with Longsword: 23 to hit. HIT! Goblin A takes 9 slashing damage.": "Attack with Longsword: 23 to hit. HIT! Goblin A takes 9 slashing damage.",
		"Dexterity saving throw: d20(5)+3 = 8 vs DC 15 → FAILURE [Bless +1]":       "Dexterity saving throw: d20(5)+3 = 8 vs DC 15 → FAILURE [Bless +1]",
		"Cast Faerie Fire! (DC 18)":                                                "Cast Faerie Fire! (DC 18)",
	}
	for in, want := range cases {
		if got := playerText(in); got != want {
			t.Errorf("playerText(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}
