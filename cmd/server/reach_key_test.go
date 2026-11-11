package main

import "testing"

// F74: the battle map's reach and range checks read the held weapon the way the attack does (a "+N" or a named weapon used to count
// as an unarmed strike with a 5-foot reach).
func TestHeldWeaponKeyForMagicWeapons(t *testing.T) {
	cases := map[string]string{
		"Longsword +3":                    "longsword",
		"Maul +2":                         "maul",
		"Sword of Sharpness (Greatsword)": "greatsword",
		"Longbow":                         "longbow",
	}
	for held, want := range cases {
		if got := mapWeaponKey(0, held); got != want {
			t.Errorf("mapWeaponKey(%q) = %q, want %q", held, got, want)
		}
	}
}
