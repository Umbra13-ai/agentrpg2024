package game

import (
	"testing"
)

func TestMatchesDamageType(t *testing.T) {
	tests := []struct {
		name            string
		damageType      string
		resistanceEntry string
		isMagical       bool
		isSilvered      bool
		expected        bool
	}{
		// Simple matches
		{"fire matches fire", "fire", "fire", false, false, true},
		{"fire doesn't match cold", "fire", "cold", false, false, false},
		{"case insensitive", "Fire", "fire", false, false, true},

		// Complex resistance strings
		{"slashing in bps list", "slashing", "bludgeoning, piercing, and slashing from nonmagical attacks", false, false, true},
		{"fire not in bps list", "fire", "bludgeoning, piercing, and slashing from nonmagical attacks", false, false, false},

		// Nonmagical bypass
		{"magical bypasses nonmagical resistance", "slashing", "bludgeoning, piercing, and slashing from nonmagical attacks", true, false, false},
		{"nonmagical blocked by nonmagical resistance", "slashing", "bludgeoning, piercing, and slashing from nonmagical attacks", false, false, true},

		// Silvered bypass
		{"silvered bypasses aren't silvered", "slashing", "bludgeoning, piercing, and slashing from nonmagical attacks that aren't silvered", false, true, false},
		{"non-silvered blocked", "slashing", "bludgeoning, piercing, and slashing from nonmagical attacks that aren't silvered", false, false, true},

		// Combined
		{"magical AND silvered bypasses both", "piercing", "bludgeoning, piercing, and slashing from nonmagical attacks that aren't silvered", true, true, false},
		{"magical bypasses nonmagical (silvered irrelevant)", "bludgeoning", "bludgeoning, piercing, and slashing from nonmagical attacks that aren't silvered", true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchesDamageType(tt.damageType, tt.resistanceEntry, tt.isMagical, tt.isSilvered)
			if result != tt.expected {
				t.Errorf("MatchesDamageType(%q, %q, %v, %v) = %v, want %v",
					tt.damageType, tt.resistanceEntry, tt.isMagical, tt.isSilvered, result, tt.expected)
			}
		})
	}
}

func TestDivineSmiteDice(t *testing.T) {
	tests := []struct {
		name            string
		slotLevel       int
		isUndeadOrFiend bool
		isCrit          bool
		expected        int
	}{
		{"1st level slot", 1, false, false, 2},
		{"2nd level slot", 2, false, false, 3},
		{"3rd level slot", 3, false, false, 4},
		{"4th level slot", 4, false, false, 5},
		{"5th level slot", 5, false, false, 6},
		{"9th level slot (no maximum)", 9, false, false, 10},

		// Undead/Fiend bonus
		{"1st level vs undead", 1, true, false, 3},
		{"5th level vs fiend", 5, true, false, 7},

		// Critical hits
		{"1st level crit", 1, false, true, 4},
		{"3rd level crit", 3, false, true, 8},
		{"5th level crit vs undead", 5, true, true, 14},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DivineSmiteDice(tt.slotLevel, tt.isUndeadOrFiend, tt.isCrit)
			if result != tt.expected {
				t.Errorf("DivineSmiteDice(%d, %v, %v) = %d, want %d",
					tt.slotLevel, tt.isUndeadOrFiend, tt.isCrit, result, tt.expected)
			}
		})
	}
}

