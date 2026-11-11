// Package game provides the core game rules (SRD 5.2.1).
// classes.go - Class features, resources, spell slots, and related calculations
package game

import (
	"fmt"
	"strings"
)

// ExtraAttackCount returns the number of attacks a character gets with the Attack action.
// Fighter scales: 2 at 5, 3 at 11, 4 at 20
// Barbarian/Monk/Paladin/Ranger: 2 at 5
// Others: 1
func ExtraAttackCount(class string, level int) int {
	classLower := strings.ToLower(class)

	switch classLower {
	case "fighter":
		if level >= 20 {
			return 4
		} else if level >= 11 {
			return 3
		} else if level >= 5 {
			return 2
		}
	case "barbarian", "monk", "paladin", "ranger":
		if level >= 5 {
			return 2
		}
	}

	// All other classes and lower levels get 1 attack
	return 1
}

// HitDie returns the hit die size for a class (e.g., 12 for Barbarian's d12)
func HitDie(class string) int {
	switch strings.ToLower(class) {
	case "barbarian":
		return 12
	case "fighter", "paladin", "ranger":
		return 10
	case "bard", "cleric", "druid", "monk", "rogue", "warlock":
		return 8
	case "sorcerer", "wizard":
		return 6
	default:
		return 8 // Default to d8
	}
}

// SpellSlots returns the spell slots available for a class at a given level.
// Returns a map of spell level -> number of slots.
func SpellSlots(class string, level int) map[int]int {
	class = strings.ToLower(class)

	// 2024: the slots are the ones printed in each class's Features table (Paladins and Rangers start at level 1)
	if class != "warlock" {
		if s, ok := slotsFromTable(class, level); ok {
			return s
		}
	}

	switch class {
	case "bard", "cleric", "druid", "sorcerer", "wizard":
		return fullCasterSlots(level)
	case "paladin", "ranger":
		return halfCasterSlots(level)
	case "warlock":
		return warlockSlots(level)
	}

	return map[int]int{} // Non-casters have no slots
}

// fullCasterSlots returns spell slots for full casters (Bard, Cleric, Druid, Sorcerer, Wizard)
func fullCasterSlots(level int) map[int]int {
	slots := map[int]map[int]int{
		1:  {1: 2},
		2:  {1: 3},
		3:  {1: 4, 2: 2},
		4:  {1: 4, 2: 3},
		5:  {1: 4, 2: 3, 3: 2},
		6:  {1: 4, 2: 3, 3: 3},
		7:  {1: 4, 2: 3, 3: 3, 4: 1},
		8:  {1: 4, 2: 3, 3: 3, 4: 2},
		9:  {1: 4, 2: 3, 3: 3, 4: 3, 5: 1},
		10: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2},
		11: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2, 6: 1},
		12: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2, 6: 1},
		13: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2, 6: 1, 7: 1},
		14: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2, 6: 1, 7: 1},
		15: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2, 6: 1, 7: 1, 8: 1},
		16: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2, 6: 1, 7: 1, 8: 1},
		17: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2, 6: 1, 7: 1, 8: 1, 9: 1},
		18: {1: 4, 2: 3, 3: 3, 4: 3, 5: 3, 6: 1, 7: 1, 8: 1, 9: 1},
		19: {1: 4, 2: 3, 3: 3, 4: 3, 5: 3, 6: 2, 7: 1, 8: 1, 9: 1},
		20: {1: 4, 2: 3, 3: 3, 4: 3, 5: 3, 6: 2, 7: 2, 8: 1, 9: 1},
	}
	if s, ok := slots[level]; ok {
		return s
	}
	return map[int]int{}
}

// halfCasterSlots returns spell slots for half casters (Paladin, Ranger) - start at level 2
func halfCasterSlots(level int) map[int]int {
	slots := map[int]map[int]int{
		2:  {1: 2},
		3:  {1: 3},
		4:  {1: 3},
		5:  {1: 4, 2: 2},
		6:  {1: 4, 2: 2},
		7:  {1: 4, 2: 3},
		8:  {1: 4, 2: 3},
		9:  {1: 4, 2: 3, 3: 2},
		10: {1: 4, 2: 3, 3: 2},
		11: {1: 4, 2: 3, 3: 3},
		12: {1: 4, 2: 3, 3: 3},
		13: {1: 4, 2: 3, 3: 3, 4: 1},
		14: {1: 4, 2: 3, 3: 3, 4: 1},
		15: {1: 4, 2: 3, 3: 3, 4: 2},
		16: {1: 4, 2: 3, 3: 3, 4: 2},
		17: {1: 4, 2: 3, 3: 3, 4: 3, 5: 1},
		18: {1: 4, 2: 3, 3: 3, 4: 3, 5: 1},
		19: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2},
		20: {1: 4, 2: 3, 3: 3, 4: 3, 5: 2},
	}
	if s, ok := slots[level]; ok {
		return s
	}
	return map[int]int{}
}

// warlockSlots returns Warlock pact magic slots (all slots are same level)
func warlockSlots(level int) map[int]int {
	slots := map[int]map[int]int{
		1:  {1: 1},
		2:  {1: 2},
		3:  {2: 2},
		4:  {2: 2},
		5:  {3: 2},
		6:  {3: 2},
		7:  {4: 2},
		8:  {4: 2},
		9:  {5: 2},
		10: {5: 2},
		11: {5: 3},
		12: {5: 3},
		13: {5: 3},
		14: {5: 3},
		15: {5: 3},
		16: {5: 3},
		17: {5: 4},
		18: {5: 4},
		19: {5: 4},
		20: {5: 4},
	}
	if s, ok := slots[level]; ok {
		return s
	}
	return map[int]int{}
}

// IsPreparedCaster returns true if the class prepares spells daily (Cleric, Druid, Paladin, Wizard)
func IsPreparedCaster(class string) bool {
	// 2024: every spellcasting class prepares spells (the Prepared Spells column of its Features table); there are no "known" casters
	switch strings.ToLower(class) {
	case "bard", "cleric", "druid", "paladin", "ranger", "sorcerer", "warlock", "wizard":
		return true
	default:
		return false
	}
}

// SpellcastingAbilityMod returns the spellcasting ability modifier for a class
func SpellcastingAbilityMod(class string, intl, wis, cha int) int {
	switch strings.ToLower(class) {
	case "wizard":
		return Modifier(intl)
	case "cleric", "druid", "ranger":
		return Modifier(wis)
	case "bard", "paladin", "sorcerer", "warlock":
		return Modifier(cha)
	default:
		return 0
	}
}

// SpellSaveDC calculates spell save DC: 8 + proficiency bonus + spellcasting modifier
func SpellSaveDC(level int, spellcastingMod int) int {
	return 8 + ProficiencyBonus(level) + spellcastingMod
}

// ClassResource defines a class resource and its recovery behavior
type ClassResource struct {
	Name         string // Display name (e.g., "Focus Points")
	Key          string // JSON key (e.g., "focus")
	RecoverShort bool   // Recovers on short rest
	RecoverLong  bool   // Recovers on long rest
}

// ClassResources returns the resource types available for a class.
// Each class may have multiple resources (e.g., Fighter has Second Wind AND Action Surge).
func ClassResources(class string) []ClassResource {
	switch strings.ToLower(class) {
	case "monk":
		return []ClassResource{
			{Name: "Focus Points", Key: "focus", RecoverShort: true, RecoverLong: true},
		}
	case "barbarian":
		return []ClassResource{
			{Name: "Rage", Key: "rage", RecoverShort: true, RecoverLong: true}, // 2024: a Short Rest returns one use
		}
	case "sorcerer":
		return []ClassResource{
			{Name: "Sorcery Points", Key: "sorcery_points", RecoverShort: false, RecoverLong: true},
		}
	case "bard":
		return []ClassResource{
			{Name: "Bardic Inspiration", Key: "bardic_inspiration", RecoverShort: true, RecoverLong: true}, // Short rest at 5+
		}
	case "cleric":
		return []ClassResource{
			{Name: "Channel Divinity", Key: "channel_divinity", RecoverShort: true, RecoverLong: true},
		}
	case "paladin":
		return []ClassResource{
			{Name: "Channel Divinity", Key: "channel_divinity", RecoverShort: true, RecoverLong: true},
			{Name: "Lay on Hands", Key: "lay_on_hands", RecoverShort: false, RecoverLong: true},
		}
	case "fighter":
		return []ClassResource{
			{Name: "Second Wind", Key: "second_wind", RecoverShort: true, RecoverLong: true},
			{Name: "Action Surge", Key: "action_surge", RecoverShort: true, RecoverLong: true},
		}
	case "druid":
		return []ClassResource{
			{Name: "Wild Shape", Key: "wild_shape", RecoverShort: true, RecoverLong: true},
			{Name: "Natural Recovery", Key: "natural_recovery", RecoverShort: false, RecoverLong: true},
		}
	case "wizard":
		return []ClassResource{
			{Name: "Arcane Recovery", Key: "arcane_recovery", RecoverShort: false, RecoverLong: true},
		}
	}

	return nil
}

// MaxClassResource returns the maximum value for a specific resource based on class and level.
// chaMod is needed for Bard's Bardic Inspiration which scales with CHA.
func MaxClassResource(class string, level int, resourceKey string, chaMod int) int {
	classLower := strings.ToLower(class)

	switch resourceKey {
	case "focus":
		if classLower == "monk" {
			return ClassTableInt(class, level, "Focus Points") // 2024 Focus Points (none at level 1)
		}
	case "rage":
		if classLower == "barbarian" {
			if level >= 17 {
				return 6
			} else if level >= 12 {
				return 5
			} else if level >= 6 {
				return 4
			} else if level >= 3 {
				return 3
			}
			return 2
		}
	case "sorcery_points":
		if classLower == "sorcerer" {
			return level // Sorcery points equal sorcerer level
		}
	case "bardic_inspiration":
		if classLower == "bard" {
			if chaMod < 1 {
				return 1 // Minimum 1 use
			}
			return chaMod
		}
	case "channel_divinity":
		// 2024: the Channel Divinity column of the Cleric's and Paladin's Features table
		if classLower == "cleric" || classLower == "paladin" {
			return ClassTableInt(class, level, "Channel Divinity")
		}
	case "lay_on_hands":
		if classLower == "paladin" {
			return level * 5 // Pool equals paladin level × 5
		}
	case "second_wind":
		// 2024: 2 uses, 3 from level 4, 4 from level 10
		if classLower == "fighter" && level >= 1 {
			switch {
			case level >= 10:
				return 4
			case level >= 4:
				return 3
			}
			return 2
		}
	case "action_surge":
		if classLower == "fighter" {
			if level >= 17 {
				return 2
			} else if level >= 2 {
				return 1
			}
		}
	case "wild_shape":
		if classLower == "druid" && level >= 2 {
			return ClassTableInt(class, level, "Wild Shape") // 2024: the Features table (no unlimited uses at level 20)
		}
	case "arcane_recovery", "natural_recovery":
		if (classLower == "wizard" && resourceKey == "arcane_recovery") ||
			(classLower == "druid" && resourceKey == "natural_recovery") {
			if level >= 1 {
				return 1 // Once per day
			}
		}
	}

	return 0
}

// AllMaxClassResources returns all max resource values for a class at a given level.
func AllMaxClassResources(class string, level int, chaMod int) map[string]int {
	resources := ClassResources(class)
	if resources == nil {
		return nil
	}

	result := make(map[string]int)
	for _, r := range resources {
		result[r.Key] = MaxClassResource(class, level, r.Key, chaMod)
	}
	return result
}

// ClassFeature represents a core class feature granted at a specific level
type ClassFeature struct {
	Name        string            `json:"name"`
	Level       int               `json:"level"`
	Description string            `json:"description"`
	Mechanics   map[string]string `json:"mechanics,omitempty"`
}

// GetActiveClassFeatures returns the class features a character has unlocked based on their level
func GetActiveClassFeatures(class string, level int) []ClassFeature {
	features, ok := classFeatures[strings.ToLower(class)]
	if !ok {
		return nil
	}

	var active []ClassFeature
	for _, feature := range features {
		if level >= feature.Level {
			active = append(active, feature)
		}
	}
	return active
}

// HasClassFeature checks if a character has a specific class mechanic
func HasClassFeature(class string, level int, mechanic string) bool {
	features := GetActiveClassFeatures(class, level)
	for _, f := range features {
		if _, ok := f.Mechanics[mechanic]; ok {
			return true
		}
	}
	return false
}

// classFeatures holds the class features by class: the SRD 5.2.1 features, filled in by class_features_2024.go (generated by
// tools/gen_class_features.py).
var classFeatures = map[string][]ClassFeature{}

// MartialArtsDie returns the martial arts damage die size for a monk at a given level
func MartialArtsDie(level int) int { // 2024 Monk Features table: d6, d8 at 5, d10 at 11, d12 at 17
	if level >= 17 {
		return 12
	} else if level >= 11 {
		return 10
	} else if level >= 5 {
		return 8
	}
	return 6
}

// SneakAttackDice returns the number of Sneak Attack dice for a rogue at a given level
func SneakAttackDice(level int) int {
	// 1d6 at 1, increases by 1d6 every odd level
	return (level + 1) / 2
}

// BardicInspirationDie returns the Bardic Inspiration die size for a bard at a given level
func BardicInspirationDie(level int) int {
	// 2024: the Bardic Die column of the Bard's Features table (d6, d8 at 5, d10 at 9, d12 at 15)
	if v, ok := ClassTableValue("bard", level, "Bardic Die"); ok {
		if n := ClassTableInt("bard", level, "Bardic Die"); n > 0 {
			return n
		}
		var sides int
		if _, err := fmt.Sscanf(strings.ToUpper(v), "D%d", &sides); err == nil && sides > 0 {
			return sides
		}
	}
	return 6 // d6
}

// CriticalHitRange returns the minimum d20 roll needed for a critical hit.
// Champion Fighters get Improved Critical (19-20) at level 3 and Superior Critical (18-20) at level 15.
// All other classes/subclasses need a natural 20.
func CriticalHitRange(subclass string, level int) int {
	if strings.ToLower(subclass) == "champion" {
		if level >= 15 {
			return 18 // Superior Critical: 18-20
		} else if level >= 3 {
			return 19 // Improved Critical: 19-20
		}
	}
	return 20 // Standard: only nat 20 crits
}

// RageDamageBonus returns the bonus damage while raging for a barbarian at a given level
func RageDamageBonus(level int) int {
	if level >= 16 {
		return 4
	} else if level >= 9 {
		return 3
	}
	return 2
}

// UnarmoredMovementBonus returns the speed bonus for an unarmored monk at a given level
func UnarmoredMovementBonus(level int) int {
	if level >= 18 {
		return 30
	} else if level >= 14 {
		return 25
	} else if level >= 10 {
		return 20
	} else if level >= 6 {
		return 15
	} else if level >= 2 {
		return 10
	}
	return 0
}
