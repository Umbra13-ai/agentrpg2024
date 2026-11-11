package main

import "testing"

func TestIsWeaponProficientCategoriesAndNames(t *testing.T) {
	cases := []struct {
		profs, weapon string
		want          bool
	}{
		{"simple, martial", "longsword", true},
		{"simple", "longsword", false},
		{"simple", "dagger", true},
		{"dagger, dart, sling, quarterstaff, light crossbow", "dagger", true},
		{"dagger, dart, sling, quarterstaff, light crossbow", "light_crossbow", true},
		{"dagger, dart, sling, quarterstaff, light crossbow", "crossbow-light", true}, // database spelling
		{"dagger, dart, sling, quarterstaff, light crossbow", "longsword", false},
		{"daggers, darts", "dagger", true}, // plurals
		{"hand crossbow", "hand_crossbow", true},
		{"", "dagger", false},
	}
	for _, c := range cases {
		if got := isWeaponProficient(c.profs, c.weapon); got != c.want {
			t.Errorf("isWeaponProficient(%q, %q) = %v, want %v", c.profs, c.weapon, got, c.want)
		}
	}
}

func TestIsArmorProficientFromClassTable(t *testing.T) {
	if !isArmorProficient("all armor, shields", "heavy") {
		t.Error("fighter should be proficient with heavy armor")
	}
	if isArmorProficient("light, medium, shields", "heavy") {
		t.Error("cleric without Life domain should not be proficient with heavy armor")
	}
	if !isArmorProficient("light, medium, shields", "shield") {
		t.Error("shields should match the shield category")
	}
}
