// Package game provides the core game rules (SRD 5.2.1).
// subclasses.go - subclass features and mechanics per PHB class chapters
package game

import (
	"strings"
)

// SubclassFeature represents a single feature gained at a specific level
type SubclassFeature struct {
	Name        string            `json:"name"`
	Level       int               `json:"level"`
	Description string            `json:"description"`
	Mechanics   map[string]string `json:"mechanics,omitempty"` // Mechanical effects to track
}

// Subclass represents a character subclass from the SRD
type Subclass struct {
	Name          string            `json:"name"`
	Class         string            `json:"class"`          // Parent class (fighter, rogue, etc.)
	SubclassLevel int               `json:"subclass_level"` // Level when subclass is chosen (3 for most, 1-2 for some)
	Description   string            `json:"description"`
	Features      []SubclassFeature `json:"features"`
	DomainSpells  map[int][]string  `json:"domain_spells,omitempty"` // Always-prepared spells by character level
}

// AvailableSubclasses: the twelve SRD 5.2.1 subclasses by slug. Their name, introduction, features (with the mechanics keys) and
// always-prepared spells are filled in by class_features_2024.go, generated from the SRD text by tools/gen_class_features.py.
var AvailableSubclasses = map[string]Subclass{
	"berserker": {Class: "barbarian"},
	"lore":      {Class: "bard"},
	"life":      {Class: "cleric"},
	"land":      {Class: "druid"},
	"champion":  {Class: "fighter"},
	"open-hand": {Class: "monk"},
	"devotion":  {Class: "paladin"},
	"hunter":    {Class: "ranger"},
	"thief":     {Class: "rogue"},
	"draconic":  {Class: "sorcerer"},
	"fiend":     {Class: "warlock"},
	"evocation": {Class: "wizard"},
}

// DragonAncestryDamageTypes maps dragon ancestry to damage type
var DragonAncestryDamageTypes = map[string]string{
	"black":  "acid",
	"blue":   "lightning",
	"brass":  "fire",
	"bronze": "lightning",
	"copper": "acid",
	"gold":   "fire",
	"green":  "poison",
	"red":    "fire",
	"silver": "cold",
	"white":  "cold",
}

// GetSubclassesForClass returns all subclasses for a given class
func GetSubclassesForClass(class string) map[string]Subclass {
	result := make(map[string]Subclass)
	classLower := strings.ToLower(class)
	for slug, sub := range AvailableSubclasses {
		if strings.ToLower(sub.Class) == classLower {
			result[slug] = sub
		}
	}
	return result
}

// GetSubclass returns a subclass by slug, or nil if not found
func GetSubclass(slug string) *Subclass {
	if sub, ok := AvailableSubclasses[slug]; ok {
		return &sub
	}
	return nil
}

// GetActiveSubclassFeatures returns the features a character has unlocked based on their level
func GetActiveSubclassFeatures(subclassSlug string, level int) []SubclassFeature {
	sub, ok := AvailableSubclasses[subclassSlug]
	if !ok {
		return nil
	}

	var active []SubclassFeature
	for _, feature := range sub.Features {
		if level >= feature.Level {
			active = append(active, feature)
		}
	}
	return active
}

// HasSubclassFeature checks if a character has a specific subclass mechanic at their level
func HasSubclassFeature(subclassSlug string, level int, mechanic string) bool {
	features := GetActiveSubclassFeatures(subclassSlug, level)
	for _, f := range features {
		if _, ok := f.Mechanics[mechanic]; ok {
			return true
		}
	}
	return false
}

// GetSubclassMechanic returns the value of a specific subclass mechanic if present
func GetSubclassMechanic(subclassSlug string, level int, mechanic string) (string, bool) {
	features := GetActiveSubclassFeatures(subclassSlug, level)
	for _, f := range features {
		if val, ok := f.Mechanics[mechanic]; ok {
			return val, true
		}
	}
	return "", false
}
