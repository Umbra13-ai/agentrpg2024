package main

import "testing"

// TestNaturalD20: the die that counted is read from every form the server writes a d20 in, and a line with no single readable die gives
// none (the page then shows the plain d20).
func TestNaturalD20(t *testing.T) {
	for line, want := range map[string]int{
		"Attack with Longsword: 17 to hit (d20 12 + 5). HIT":                             12,
		"Attack with Longsword: 22 to hit (d20s 17 and 4: 17 + 5) with Advantage (Help)": 17,
		"d20s 3 and 15 with Disadvantage (Poisoned): 3":                                  3,
		"Dexterity (Stealth) 19 (d20: 14 +5). Hidden":                                    14,
		"Wisdom save: d20(20) +2 = 22 vs DC 13":                                          20,
		"Strength save: d20(9,16→16) +3":                                                 16,
		"Natural 1! d20 1 + 4":                                                           1,
	} {
		if got, ok := naturalD20(line); !ok || got != want {
			t.Errorf("%q: got %d %v, want %d", line, got, ok, want)
		}
	}
	for _, line := range []string{
		"Cast Mage Hand! A spectral, floating hand appears",                    // no roll
		"Claw: 15 to hit (d20 10 + 5). HIT. Bite: 9 to hit (d20 4 + 5). MISS",  // two rolls (a Multiattack)
		"Halfling Luck: d20(1→14 Lucky) +2",                                    // a reroll
		"Glibness: d20(4→10) +3",                                               // a floor
		"Influence: 12 if Indifferent (d20s: 9; 14; 3, + 2 Charisma + 2 prof)", // several d20s, none kept
		"Bardic Inspiration: d8 = 5. Add 5 to the d20 of the failed test.",     // not a roll of the d20
	} {
		if n, ok := naturalD20(line); ok {
			t.Errorf("%q: read %d, want no single die", line, n)
		}
	}
	if u := d20FaceURL("Attack: 12 to hit (d20 7 + 5)"); u != "/assets/d20-7.webp" {
		t.Errorf("the face of a 7: %q", u)
	}
	if u := d20FaceURL("no roll here"); u != "" {
		t.Errorf("no roll: %q", u)
	}
}
