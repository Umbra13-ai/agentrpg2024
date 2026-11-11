// Package game provides the core game rules (SRD 5.2.1).
// combat.go - damage resolution, attack modifiers, and combat calculations
package game

import (
	"strings"
)

// DamageModResult holds the result of damage resistance/immunity/vulnerability calculations.
type DamageModResult struct {
	FinalDamage     int
	Resistances     []string
	Immunities      []string
	Vulnerabilities []string
	WasHalved       bool
	WasDoubled      bool
	WasNegated      bool
}

// MatchesDamageType checks if a damage type matches a resistance/immunity/vulnerability string.
// Handles both simple ("fire") and complex ("bludgeoning, piercing, and slashing from nonmagical attacks") entries.
// Parameters:
//   - damageType: the type of damage being dealt (e.g., "fire", "slashing")
//   - resistanceEntry: the resistance/immunity string to check against
//   - isMagical: true if the damage source is magical (spells, +1 weapons, etc.)
//   - isSilvered: true if the weapon is silvered
// Returns true if the damage type matches and isn't bypassed by magical/silvered status.
func MatchesDamageType(damageType, resistanceEntry string, isMagical, isSilvered bool) bool {
	damageType = strings.ToLower(strings.TrimSpace(damageType))
	resistanceEntry = strings.ToLower(strings.TrimSpace(resistanceEntry))

	// Simple exact match (no conditional modifiers)
	if damageType == resistanceEntry {
		return true
	}

	// Check if damage type is contained in the entry (for complex strings)
	// e.g., "bludgeoning, piercing, and slashing from nonmagical attacks"
	// e.g., "bludgeoning, piercing, and slashing from nonmagical attacks that aren't silvered"
	if strings.Contains(resistanceEntry, damageType) {
		// Check for "nonmagical" condition
		// Common phrasings: "from nonmagical attacks", "from nonmagical weapons"
		isNonmagicalOnly := strings.Contains(resistanceEntry, "nonmagical")

		// Check for "silvered" exception
		// Common phrasings: "that aren't silvered", "not silvered", "except silver"
		hasSilveredExemption := strings.Contains(resistanceEntry, "aren't silvered") ||
			strings.Contains(resistanceEntry, "not silvered") ||
			strings.Contains(resistanceEntry, "except silver")

		// If resistance only applies to nonmagical attacks and this IS magical, resistance doesn't apply
		if isNonmagicalOnly && isMagical {
			return false
		}

		// If resistance has a silvered exemption and weapon IS silvered, resistance doesn't apply
		if hasSilveredExemption && isSilvered {
			return false
		}

		return true
	}

	return false
}

// DivineSmiteDice is the number of d8s of Divine Smite (SRD 5.2.1 spell): 2d8, +1d8 for each spell slot level above 1
// (no maximum), +1d8 against a Fiend or an Undead; a Critical Hit doubles the dice.
func DivineSmiteDice(slotLevel int, isUndeadOrFiend, isCrit bool) int {
	if slotLevel < 1 {
		slotLevel = 1
	}
	numDice := 2 + (slotLevel - 1)
	if isUndeadOrFiend {
		numDice++
	}
	if isCrit {
		numDice *= 2
	}
	return numDice
}

// AttackModifiers represents advantage/disadvantage state for an attack.
type AttackModifiers struct {
	HasAdvantage    bool
	HasDisadvantage bool
	Reasons         []string // Explanations for why each modifier applies
}

