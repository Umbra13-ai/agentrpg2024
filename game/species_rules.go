// Package game provides the core game rules (SRD 5.2.1).
// species_rules.go: the SRD 5.2.1 species and their traits.
package game

import (
	"strings"
)

// Species of the SRD 5.2.1
const (
	SpeciesHuman         = "human"
	SpeciesElf           = "elf"
	SpeciesDwarf         = "dwarf"
	SpeciesHalfling      = "halfling"
	SpeciesGnome         = "gnome"
	SpeciesTiefling      = "tiefling"
	SpeciesDragonborn    = "dragonborn"
	SpeciesGoliath       = "goliath"
	SpeciesOrc           = "orc"
)

// Size constants
const (
	SizeTiny       = "Tiny"
	SizeSmall      = "Small"
	SizeMedium     = "Medium"
	SizeLarge      = "Large"
	SizeHuge       = "Huge"
	SizeGargantuan = "Gargantuan"
)

// SpeciesInfo contains basic species statistics
type SpeciesInfo struct {
	Name  string
	Size  string
	Speed int
}

// normalizeSpecies converts a species string to a standard form for matching.
func normalizeSpecies(species string) string {
	species = strings.ToLower(species)
	species = strings.ReplaceAll(species, " ", "_")
	species = strings.ReplaceAll(species, "-", "_")
	return species
}

// IsElf checks if a species is an Elf (any lineage: Drow, High Elf, Wood Elf).
func IsElf(species string) bool {
	norm := normalizeSpecies(species)
	return strings.Contains(norm, "elf") && !strings.Contains(norm, "half")
}

// IsDwarf checks if a species is any Dwarf variant.
// Matches: dwarf, hill_dwarf, mountain_dwarf
func IsDwarf(species string) bool {
	return strings.Contains(normalizeSpecies(species), "dwarf")
}

// IsHalfling checks if a species is any Halfling variant.
// Matches: halfling, lightfoot, stout
func IsHalfling(species string) bool {
	return strings.Contains(normalizeSpecies(species), "halfling")
}

// IsGnome checks if a species is any Gnome variant.
// Matches: gnome, rock_gnome, forest_gnome
func IsGnome(species string) bool {
	return strings.Contains(normalizeSpecies(species), "gnome")
}

// IsOrc reports whether a species is the Orc (Relentless Endurance, Adrenaline Rush).
func IsOrc(species string) bool {
	return normalizeSpecies(species) == "orc"
}

// IsTiefling checks if a species is Tiefling.
func IsTiefling(species string) bool {
	return strings.Contains(normalizeSpecies(species), "tiefling")
}

// IsDragonborn checks if a species is Dragonborn.
func IsDragonborn(species string) bool {
	return strings.Contains(normalizeSpecies(species), "dragonborn")
}

// HasFeyAncestry returns true if the species has the Fey Ancestry trait.
// Elves (every lineage) have this trait.
// Effects: Advantage on saves against being charmed, immune to magical sleep.
func HasFeyAncestry(species string) bool {
	return IsElf(species)
}

// HasGnomeCunning returns true if the species has the Gnomish Cunning trait.
// All Gnome variants have this trait.
// Effects: Advantage on INT, WIS, and CHA saves against magic.
func HasGnomeCunning(species string) bool {
	return IsGnome(species)
}

// HasDwarvenResilience returns true if the species has the Dwarven Resilience trait.
// All Dwarf variants have this trait.
// Effects: Advantage on saves against poison, resistance to poison damage.
func HasDwarvenResilience(species string) bool {
	return IsDwarf(species)
}

// HasHalflingBrave returns true if the species has the Brave trait.
// All Halfling variants have this trait.
// Effects: Advantage on saves against being frightened.
func HasHalflingBrave(species string) bool {
	return IsHalfling(species)
}

// HasHellishResistance returns true if the species has the Hellish Resistance trait.
// Tieflings have this trait.
// Effects: Resistance to fire damage.
func HasHellishResistance(species string) bool {
	return IsTiefling(species)
}

// HasBreathWeapon returns true if the species has the Breath Weapon trait.
// Dragonborn have this trait.
// Effects: Breath weapon attack based on draconic ancestry.
func HasBreathWeapon(species string) bool {
	return IsDragonborn(species)
}

// GetSpeciesSize returns the size category for a species (PHB sizes).
// Returns "Small" for Halflings and Gnomes, "Medium" for all others.
func GetSpeciesSize(species string) string {
	if IsHalfling(species) || IsGnome(species) {
		return SizeSmall
	}
	return SizeMedium
}

// GetDefaultSpeed returns the base walking speed for a species.
// Most species have 30ft, but some have different speeds.
func GetDefaultSpeed(species string) int {
	norm := normalizeSpecies(species)

	// Small species typically have 25ft speed
	if IsHalfling(species) || IsGnome(species) {
		return 25
	}

	// Dwarves have 25ft speed
	if IsDwarf(species) {
		return 25
	}

	// Wood elves have 35ft speed
	if strings.Contains(norm, "wood") && strings.Contains(norm, "elf") {
		return 35
	}

	// Default speed for most species
	return 30
}

// SizeOrder returns the numeric order for a size category.
// Higher numbers mean larger creatures.
func SizeOrder(size string) int {
	order := map[string]int{
		SizeTiny:       1,
		SizeSmall:      2,
		SizeMedium:     3,
		SizeLarge:      4,
		SizeHuge:       5,
		SizeGargantuan: 6,
	}
	if o, ok := order[size]; ok {
		return o
	}
	return 3 // Default to Medium
}

// IsSizeAtLeastOneLarger returns true if sizeA is at least one size category larger than sizeB.
// Used for mount eligibility (mounts must be at least one size larger than rider).
func IsSizeAtLeastOneLarger(sizeA, sizeB string) bool {
	return SizeOrder(sizeA) > SizeOrder(sizeB)
}

// Keyword lists for species save advantages

var frightenKeywords = []string{
	"frighten", "frightened", "frightening", "fear", "feared", "fearful",
	"terrify", "terrified", "terror", "scare", "scared", "dread", "panic",
}

var charmKeywords = []string{
	"charm", "charmed", "charming", "dominate", "suggestion",
	"command", "compulsion", "enthrall",
}

var poisonKeywords = []string{
	"poison", "poisoned", "poisoning", "toxic", "venom", "venomous",
}

// CheckFrightenKeywords returns true if the description contains frighten-related keywords.
// Used for Halfling Brave, Fey Ancestry (elves/half-elves), etc.
func CheckFrightenKeywords(description string) bool {
	descLower := strings.ToLower(description)
	for _, keyword := range frightenKeywords {
		if strings.Contains(descLower, keyword) {
			return true
		}
	}
	return false
}

// CheckCharmKeywords returns true if the description contains charm-related keywords.
// Used for Fey Ancestry (elves/half-elves).
func CheckCharmKeywords(description string) bool {
	descLower := strings.ToLower(description)
	for _, keyword := range charmKeywords {
		if strings.Contains(descLower, keyword) {
			return true
		}
	}
	return false
}

// CheckPoisonKeywords returns true if the description contains poison-related keywords.
// Used for Dwarven Resilience.
func CheckPoisonKeywords(description string) bool {
	descLower := strings.ToLower(description)
	for _, keyword := range poisonKeywords {
		if strings.Contains(descLower, keyword) {
			return true
		}
	}
	return false
}

// CheckHalflingBrave returns true if Halfling Brave grants advantage on this save.
// Requires the species to be Halfling AND the save to be against frightened effects.
func CheckHalflingBrave(species, description string) bool {
	if !HasHalflingBrave(species) {
		return false
	}
	return CheckFrightenKeywords(description)
}

// CheckFeyAncestryCharm returns true if Fey Ancestry grants advantage on this charm save.
// Requires the species to have Fey Ancestry AND the save to be against charm effects.
func CheckFeyAncestryCharm(species, description string) bool {
	if !HasFeyAncestry(species) {
		return false
	}
	return CheckCharmKeywords(description)
}

// CheckDwarvenResiliencePoison returns true if Dwarven Resilience grants advantage on this save.
// Requires the species to be Dwarf AND the save to be against poison effects.
func CheckDwarvenResiliencePoison(species, description string) bool {
	if !HasDwarvenResilience(species) {
		return false
	}
	return CheckPoisonKeywords(description)
}

// CheckGnomeCunningMagic returns true if Gnomish Cunning grants advantage on this save.
// 2024 Gnomish Cunning: Advantage on every INT, WIS and CHA save, magical or not.
func CheckGnomeCunningMagic(species, abilityShort string, fromMagic bool) bool {
	if !HasGnomeCunning(species) {
		return false
	}
	switch strings.ToLower(abilityShort) {
	case "int", "wis", "cha":
		return true
	default:
		return false
	}
}

// ApplyHalflingLucky implements the Halfling Lucky species trait:
// When you roll a 1 on the d20, you can reroll and must use the new roll.
// Returns: (finalRoll, wasRerolled, originalRoll)
// Note: Caller must check HasHalflingLucky before calling this for correct behavior,
// OR this will apply the logic regardless (it checks the roll value, not the species).
func ApplyHalflingLucky(d20Roll int, isHalfling bool) (finalRoll int, wasRerolled bool, originalRoll int) {
	if d20Roll == 1 && isHalfling {
		newRoll := RollDie(20)
		return newRoll, true, d20Roll
	}
	return d20Roll, false, d20Roll
}

// BreathWeaponDamage returns the damage dice for a Dragonborn breath weapon at a given level.
// Scales: 2d6 (1-5), 3d6 (6-10), 4d6 (11-15), 5d6 (16+).
func BreathWeaponDamage(level int) string {
	switch { // 2024: 1d10, 2d10 at level 5, 3d10 at 11, 4d10 at 17
	case level >= 17:
		return "4d10"
	case level >= 11:
		return "3d10"
	case level >= 5:
		return "2d10"
	default:
		return "1d10"
	}
}

// DragonAncestry maps dragon types to their damage types and breath weapon shapes.
type DragonAncestry struct {
	Color       string
	DamageType  string
	BreathArea  string // "15ft cone" or "5x30ft line"
	SaveAbility string // "dex" or "con"
}

// DragonAncestries contains all draconic ancestry options from.
var DragonAncestries = map[string]DragonAncestry{
	"black":  {"Black", "acid", "5x30ft line", "dex"},
	"blue":   {"Blue", "lightning", "5x30ft line", "dex"},
	"brass":  {"Brass", "fire", "5x30ft line", "dex"},
	"bronze": {"Bronze", "lightning", "5x30ft line", "dex"},
	"copper": {"Copper", "acid", "5x30ft line", "dex"},
	"gold":   {"Gold", "fire", "15ft cone", "dex"},
	"green":  {"Green", "poison", "15ft cone", "con"},
	"red":    {"Red", "fire", "15ft cone", "dex"},
	"silver": {"Silver", "cold", "15ft cone", "con"},
	"white":  {"White", "cold", "15ft cone", "con"},
}

// GetDragonAncestry returns the ancestry info for a dragon color.
// Returns nil if the color is not found.
func GetDragonAncestry(color string) *DragonAncestry {
	color = strings.ToLower(color)
	if ancestry, ok := DragonAncestries[color]; ok {
		return &ancestry
	}
	return nil
}
