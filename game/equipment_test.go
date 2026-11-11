package game

import (
	"testing"
)

func TestArmorDonDoffTime(t *testing.T) {
	tests := []struct {
		armorType string
		wantDon   int
		wantDoff  int
	}{
		{"light", 1, 1},
		{"Light", 1, 1},
		{"LIGHT", 1, 1},
		{"medium", 5, 1},
		{"Medium", 5, 1},
		{"heavy", 10, 5},
		{"Heavy", 10, 5},
		{"shield", 0, 0},
		{"Shield", 0, 0},
		{"unknown", 1, 1}, // defaults to light
		{"", 1, 1},        // defaults to light
	}

	for _, tt := range tests {
		t.Run(tt.armorType, func(t *testing.T) {
			don, doff := ArmorDonDoffTime(tt.armorType)
			if don != tt.wantDon || doff != tt.wantDoff {
				t.Errorf("ArmorDonDoffTime(%q) = (%d, %d), want (%d, %d)",
					tt.armorType, don, doff, tt.wantDon, tt.wantDoff)
			}
		})
	}
}

func TestAmmoTypeForWeapon(t *testing.T) {
	tests := []struct {
		weapon string
		want   string
	}{
		{"shortbow", "arrows"},
		{"longbow", "arrows"},
		{"light_crossbow", "bolts"},
		{"heavy_crossbow", "bolts"},
		{"hand_crossbow", "bolts"},
		{"blowgun", "needles"},
		{"sling", "bullets"},
		{"longsword", ""}, // melee weapon
		{"dagger", ""},    // melee/thrown
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.weapon, func(t *testing.T) {
			got := AmmoTypeForWeapon(tt.weapon)
			if got != tt.want {
				t.Errorf("AmmoTypeForWeapon(%q) = %q, want %q", tt.weapon, got, tt.want)
			}
		})
	}
}

func TestParseProficiencyList(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"simple", []string{"simple"}},
		{"simple, martial", []string{"simple", "martial"}},
		{"light, medium, shields", []string{"light", "medium", "shields"}},
		{"  light  ,  medium  ", []string{"light", "medium"}},
		{"all armor, shields", []string{"all armor", "shields"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseProficiencyList(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("ParseProficiencyList(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("ParseProficiencyList(%q)[%d] = %q, want %q", tt.input, i, v, tt.want[i])
				}
			}
		})
	}
}

