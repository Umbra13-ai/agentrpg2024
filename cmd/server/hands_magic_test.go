package main

import "testing"

// F215: a magic two-handed weapon takes both hands (a Shield went on beside a Sword of Sharpness (Greatsword)).
func TestTwoHandedMagicWeapons(t *testing.T) {
	for name, want := range map[string]bool{"Greatsword": true, "Sword of Sharpness (Greatsword)": true, "Longsword +3": false, "Dancing Sword (Rapier)": false} {
		if got := isTwoHandedWeapon(name); got != want {
			t.Errorf("isTwoHandedWeapon(%q) = %v, want %v", name, got, want)
		}
	}
}
