package main

import (
	"strings"
	"testing"
)

func TestHitOrMissNote(t *testing.T) {
	desc := "Ranged Attack Roll: +10, range 120 ft. Hit: 15 (2d8 + 6) Bludgeoning damage. Hit or Miss: Earth explodes. Dexterity Saving Throw: DC 16, each creature in a 10-foot Emanation. Failure: 10 (3d6) Thunder damage."
	for _, hit := range []bool{true, false} {
		n := hitOrMissNote(desc, hit)
		if !strings.Contains(n, "Dexterity Saving Throw: DC 16") || !strings.Contains(n, "GM") {
			t.Fatalf("hit=%v: the effect is not passed to the GM: %q", hit, n)
		}
	}
	if hitOrMissNote("Melee Attack Roll: +5. Hit: 5 (1d6 + 2) Slashing damage.", true) != "" {
		t.Fatal("note without a Hit or Miss effect")
	}
}
