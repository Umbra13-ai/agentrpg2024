// Package game provides the game rules (SRD 5.2.1) as pure functions.
// equipment.go contains equipment-related calculations and lookups.
package game

import (
	"encoding/json"
	"strings"
)

// Armor type constants
const (
	ArmorTypeLight  = "light"
	ArmorTypeMedium = "medium"
	ArmorTypeHeavy  = "heavy"
	ArmorTypeShield = "shield"
)

// ArmorInfo contains the relevant stats for an armor piece
type ArmorInfo struct {
	AC                  int
	Type                string // light, medium, heavy, shield
	StealthDisadvantage bool
	StrengthRequirement int
}

// ArmorDonDoffTime returns donning and doffing times in minutes for an armor type
// Shield: 0 minutes (actually 1 action, handled specially in combat)
// Light armor: 1 minute don, 1 minute doff
// Medium armor: 5 minutes don, 1 minute doff
// Heavy armor: 10 minutes don, 5 minutes doff
func ArmorDonDoffTime(armorType string) (donMinutes, doffMinutes int) {
	switch strings.ToLower(armorType) {
	case ArmorTypeShield:
		return 0, 0 // Handled as action in combat
	case ArmorTypeLight:
		return 1, 1
	case ArmorTypeMedium:
		return 5, 1
	case ArmorTypeHeavy:
		return 10, 5
	default:
		return 1, 1 // Default to light armor timing
	}
}

// AmmoTypeForWeapon returns the ammunition type needed for a weapon, or "" if none.
// Standard 5e ammunition weapons: bows use arrows, crossbows use bolts,
// blowguns use needles, slings use bullets.
func AmmoTypeForWeapon(weaponKey string) string {
	ammoMap := map[string]string{
		"shortbow":       "arrows",
		"longbow":        "arrows",
		"light_crossbow": "bolts",
		"heavy_crossbow": "bolts",
		"hand_crossbow":  "bolts",
		"blowgun":        "needles",
		"sling":          "bullets",
		"musket":         "bullets",
		"pistol":         "bullets",
	}
	return ammoMap[weaponKey]
}

// ParseProficiencyList parses a comma-separated proficiency string into a slice.
// E.g., "simple, martial, shields" -> ["simple", "martial", "shields"]
func ParseProficiencyList(profStr string) []string {
	if strings.TrimSpace(profStr) == "" {
		return nil
	}
	// Some older code wrote these columns as a JSON array; read that too.
	if s := strings.TrimSpace(profStr); strings.HasPrefix(s, "[") {
		var arr []string
		if json.Unmarshal([]byte(s), &arr) == nil {
			out := make([]string, 0, len(arr))
			for _, a := range arr {
				if t := strings.TrimSpace(a); t != "" {
					out = append(out, t)
				}
			}
			return out
		}
	}
	parts := strings.Split(profStr, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

