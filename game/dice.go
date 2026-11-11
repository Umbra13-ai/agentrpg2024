// Package game provides the core game rules (SRD 5.2.1).
// This package is part of the Agent RPG 2024 modularization effort to break up
// the 46K+ line main.go into manageable, testable components.
package game

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"strings"
)

// RollDie rolls a single die with the given number of sides using crypto/rand.
// Returns a value from 1 to sides (inclusive).
// MaxDice and MaxDieSides bound any roll (the largest SRD rolls are a few dozen dice); a dice string can come from a GM's request.
const (
	MaxDice     = 1000
	MaxDieSides = 1000
)

func RollDie(sides int) int {
	if sides < 1 {
		sides = 1
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(sides)))
	return int(n.Int64()) + 1
}

// RollDice rolls multiple dice and returns individual rolls and total.
// Example: RollDice(2, 6) rolls 2d6.
func RollDice(count, sides int) ([]int, int) {
	if count < 1 {
		count = 1
	}
	if count > MaxDice {
		count = MaxDice
	}
	if sides > MaxDieSides {
		sides = MaxDieSides
	}
	rolls := make([]int, count)
	total := 0
	for i := 0; i < count; i++ {
		rolls[i] = RollDie(sides)
		total += rolls[i]
	}
	return rolls, total
}

// RollWithAdvantage rolls 2d20 and returns (result, roll1, roll2).
// Result is the higher of the two rolls.
func RollWithAdvantage() (int, int, int) {
	roll1 := RollDie(20)
	roll2 := RollDie(20)
	result := roll1
	if roll2 > roll1 {
		result = roll2
	}
	return result, roll1, roll2
}

// RollWithDisadvantage rolls 2d20 and returns (result, roll1, roll2).
// Result is the lower of the two rolls.
func RollWithDisadvantage() (int, int, int) {
	roll1 := RollDie(20)
	roll2 := RollDie(20)
	result := roll1
	if roll2 < roll1 {
		result = roll2
	}
	return result, roll1, roll2
}

// ParseDice parses a dice string like "2d6" or "1d8+2" and returns (count, sides).
// The +modifier is ignored for pure dice parsing.
func ParseDice(dice string) (count int, sides int) {
	dice = strings.ToLower(dice)
	// Remove any +X modifier
	if idx := strings.Index(dice, "+"); idx > 0 {
		dice = dice[:idx]
	}

	parts := strings.Split(dice, "d")
	if len(parts) != 2 {
		return 1, 6 // Default to 1d6
	}

	count, _ = strconv.Atoi(parts[0])
	sides, _ = strconv.Atoi(parts[1])
	if count < 1 {
		count = 1
	}
	if sides < 1 {
		sides = 6
	}
	if count > MaxDice {
		count = MaxDice
	}
	if sides > MaxDieSides {
		sides = MaxDieSides
	}
	return count, sides
}

// RollDamage rolls damage dice from a string like "2d6" or "1d8+2".
// If critical is true, dice are doubled (but not the modifier).
// Returns the total of all dice rolled.
func RollDamage(dice string, critical bool) int {
	count, sides := ParseDice(dice)

	if critical {
		count *= 2 // Double dice on crit
	}

	_, total := RollDice(count, sides)
	return total + FlatBonus(dice)
}

// FlatBonus is the fixed amount in a dice string such as "3d4+3" (0 when there is none).
func FlatBonus(dice string) int {
	idx := strings.Index(dice, "+")
	if idx < 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(dice[idx+1:]))
	if err != nil {
		return 0
	}
	return n
}

// RollDamageGWF rolls damage with the Great Weapon Fighting style (2024): a 1 or a 2 on a damage die counts as a 3.
func RollDamageGWF(dice string, critical bool) int {
	count, sides := ParseDice(dice)

	if critical {
		count *= 2 // Double dice on crit
	}

	total := 0
	for i := 0; i < count; i++ {
		roll := RollDie(sides)
		// 2024 Great Weapon Fighting: a 1 or a 2 on a damage die counts as a 3
		if roll < 3 {
			roll = 3
		}
		total += roll
	}
	return total
}

// RollDamageMax returns the maximum possible roll for a dice string.
// Used for features like Life Domain's Supreme Healing.
func RollDamageMax(dice string) int {
	count, sides := ParseDice(dice)
	return count * sides // Max roll = count * sides
}

// Modifier calculates the ability modifier from an ability score.
// Standard 5e formula: (score - 10) / 2, rounded down (floor).
// Go's integer division truncates toward zero, so we need special handling
// for negative results to ensure proper floor behavior.
func Modifier(stat int) int {
	diff := stat - 10
	// For negative differences, we need floor division
	// e.g., -9/2 should be -5, not -4
	if diff < 0 && diff%2 != 0 {
		return diff/2 - 1
	}
	return diff / 2
}

// RollInitiative rolls initiative for combat (d20 + DEX mod + bonus).
func RollInitiative(dexMod, initiativeBonus int) int {
	return RollDie(20) + dexMod + initiativeBonus
}

// ProficiencyBonus returns the proficiency bonus for a given character level.
// Standard 5e formula: +2 at levels 1-4, +3 at 5-8, +4 at 9-12, +5 at 13-16, +6 at 17-20.
func ProficiencyBonus(level int) int {
	if level < 5 {
		return 2
	} else if level < 9 {
		return 3
	} else if level < 13 {
		return 4
	} else if level < 17 {
		return 5
	}
	return 6
}
