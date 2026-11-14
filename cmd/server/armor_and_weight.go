package main

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

// ArmorInfo holds armor data for AC calculation
type ArmorInfo struct {
	AC                  int
	Type                string // light, medium, heavy, shield
	StealthDisadvantage bool
	StrengthRequirement int
}

// getArmorInfo fetches armor data from the database
func getArmorInfo(armorSlug string) (*ArmorInfo, error) {
	if armorSlug == "" {
		return nil, nil
	}

	var info ArmorInfo
	err := db.QueryRow(`SELECT ac, type, stealth_disadvantage, COALESCE(str_req, 0) FROM armor WHERE slug = $1`, armorSlug).Scan(&info.AC, &info.Type, &info.StealthDisadvantage, &info.StrengthRequirement)
	if err != nil {
		// magic armor names ("Plate Armor +1", "Armor, +2 (Plate)", "Elven Chain", "Dwarven Plate") use their base armor
		if base, _ := magicArmorBase(armorSlug); base != "" && base != armorSlug {
			if err2 := db.QueryRow(`SELECT ac, type, stealth_disadvantage, COALESCE(str_req, 0) FROM armor WHERE slug = $1`, base).Scan(&info.AC, &info.Type, &info.StealthDisadvantage, &info.StrengthRequirement); err2 == nil {
				if strings.HasPrefix(strings.ToLower(armorSlug), "mithral armor") {
					info.StealthDisadvantage, info.StrengthRequirement = false, 0 // Mithral Armor: no Strength requirement, no Stealth Disadvantage
				}
				if strings.HasPrefix(strings.ToLower(armorSlug), "elven chain") {
					info.StrengthRequirement = 0
				}
				return &info, nil
			}
		}
		return nil, err
	}
	return &info, nil
}

// armorBaseSlugs: the base armor named in a magic armor's name.
var armorBaseSlugs = []struct{ word, slug string }{
	{"half plate", "half-plate-armor"}, {"plate", "plate-armor"}, {"studded leather", "studded-leather-armor"}, {"leather", "leather-armor"},
	{"padded", "padded-armor"}, {"hide", "hide-armor"}, {"chain shirt", "chain-shirt"}, {"chain mail", "chain-mail"}, {"scale mail", "scale-mail"},
	{"breastplate", "breastplate"}, {"ring mail", "ring-mail"}, {"splint", "splint-armor"},
}

// magicArmorBase is the base armor slug and the AC bonus of a magic armor name (SRD 5.2.1 Magic Items): "Plate Armor +2" and
// "Armor, +2 (Plate)" are plate +2; Glamoured Studded Leather +1; Elven Chain (a Chain Shirt unless it says Chain Mail) +1; Dwarven Plate
// (Plate Armor, or Half Plate if it says so) +2; Dragon Scale Mail (Scale Mail) +1; Demon Armor (+1, the base in parentheses); Armor of
// Invulnerability and Plate Armor of Etherealness (Plate Armor); Mithral, Adamantine, Armor of Resistance and Armor of Vulnerability
// (the base in parentheses or in the name).
func magicArmorBase(name string) (string, int) {
	n := strings.ToLower(strings.TrimSpace(name))
	plus := itemPlus(n)
	inner := ""
	if i := strings.Index(n, "("); i >= 0 {
		inner = strings.Trim(n[i:], "() ")
	}
	find := func(s string) string {
		for _, b := range armorBaseSlugs {
			if strings.Contains(s, b.word) {
				return b.slug
			}
		}
		return ""
	}
	switch {
	case strings.HasPrefix(n, "glamoured studded leather"):
		return "studded-leather-armor", 1
	case strings.HasPrefix(n, "elven chain"):
		if strings.Contains(n, "chain mail") {
			return "chain-mail", 1
		}
		return "chain-shirt", 1
	case strings.HasPrefix(n, "dwarven plate"):
		if strings.Contains(n, "half") {
			return "half-plate-armor", 2
		}
		return "plate-armor", 2
	case strings.HasPrefix(n, "dragon scale mail"):
		return "scale-mail", 1
	case strings.HasPrefix(n, "demon armor"):
		if b := find(inner); b != "" {
			return b, 1
		}
		return "plate-armor", 1
	case strings.HasPrefix(n, "armor of invulnerability"):
		return "plate-armor", 0
	case strings.HasPrefix(n, "plate armor of etherealness"):
		if strings.Contains(inner, "half") {
			return "half-plate-armor", 0
		}
		return "plate-armor", 0
	}
	if inner != "" {
		if b := find(inner); b != "" {
			return b, plus
		}
	}
	if b := find(plusRe.ReplaceAllString(n, "")); b != "" {
		return b, plus
	}
	return "", 0
}

// calculateArmorACWithNatural is like calculateArmorAC but allows specifying natural AC base
// for features like Draconic Resilience (13 + DEX) or Unarmored Defense
func calculateArmorACWithNatural(dexMod int, equippedArmor string, equippedShield bool, naturalACBase int) int {
	baseAC := naturalACBase + dexMod // Unarmored with natural AC

	if equippedArmor != "" {
		armor, err := getArmorInfo(equippedArmor)
		if err != nil || armor == nil {
			armor, err = getArmorInfo(stripPlus(equippedArmor)) // magic armor: "Plate Armor +1" is plate (magic_items_2024.go)
		}
		if err != nil || armor == nil {
			armor, err = getArmorInfo(stripPlus(equippedArmor) + "-armor") // the armor table's slugs: plate-armor, studded-leather-armor
		}
		if err == nil && armor != nil {
			switch strings.ToLower(armor.Type) {
			case "light":
				baseAC = armor.AC + dexMod
			case "medium":
				dexBonus := dexMod
				if dexBonus > 2 {
					dexBonus = 2
				}
				baseAC = armor.AC + dexBonus
			case "heavy":
				baseAC = armor.AC
			}
		}
	}

	if equippedShield {
		baseAC += 2
	}

	return baseAC
}

// getNaturalACBase returns the unarmored AC base for a character based on subclass features
// Draconic Sorcerers get 13 (from Draconic Resilience)
// Default is 10 for most characters
func getNaturalACBase(subclass string, level int) int {
	if subclass == "" {
		return 10
	}
	// Check for natural_ac mechanic (e.g., Draconic Resilience gives "13+dex")
	if acStr, ok := getSubclassMechanic(subclass, level, "natural_ac"); ok {
		// Parse the base AC from strings like "13+dex"
		parts := strings.Split(acStr, "+")
		if len(parts) >= 1 {
			base, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err == nil {
				return base
			}
		}
	}
	return 10
}

// getArmorStealthDisadvantage checks if equipped armor causes stealth disadvantage
func getArmorStealthDisadvantage(equippedArmor string) bool {
	if equippedArmor == "" {
		return false
	}
	armor, err := getArmorInfo(equippedArmor)
	if err != nil || armor == nil {
		return false
	}
	return armor.StealthDisadvantage
}

// Check if a character is proficient with a weapon
// weaponProfsStr is comma-separated list from character: "simple, martial" or specific weapons
// weaponKey is the SRD weapon key (e.g., "longsword", "dagger")
func isWeaponProficient(weaponProfsStr string, weaponKey string) bool {
	if weaponProfsStr == "" {
		return false
	}

	// Get weapon info to determine category
	weapon, hasWeapon := srdWeapons[weaponKey]
	if !hasWeapon { // "Flail" as the shop and the inventory write it, "Flail +1": the name is not the table's key (live test, Nov 14)
		if _, w, ok := findWeapon(strings.TrimSpace(plusRe.ReplaceAllString(weaponKey, ""))); ok {
			weapon, hasWeapon = w, true
		}
	}

	// Parse proficiency list
	profs := strings.Split(strings.ToLower(weaponProfsStr), ",")
	for _, prof := range profs {
		prof = strings.TrimSpace(prof)

		// 2024: "martial weapons that have the light property" and
		// "martial weapons that have the finesse or light property"
		if hasWeapon && strings.HasPrefix(prof, "martial weapons that have") && weapon.Category == "martial" {
			if strings.Contains(prof, "finesse") && containsProperty(weapon.Properties, "finesse") {
				return true
			}
			if strings.Contains(prof, "light") && containsProperty(weapon.Properties, "light") {
				return true
			}
		}

		// Check for category proficiency ("simple" or "martial")
		if hasWeapon {
			if prof == "simple" && weapon.Category == "simple" {
				return true
			}
			if prof == "martial" && weapon.Category == "martial" {
				return true
			}
		}

		// Check for specific weapon proficiency
		// Normalize both for comparison (e.g., "light crossbow" -> "light crossbow", "light_crossbow" -> "light crossbow")
		normalizedProf := strings.ReplaceAll(prof, "_", " ")
		normalizedWeapon := strings.ReplaceAll(weaponKey, "_", " ")
		normalizedWeapon = strings.ReplaceAll(normalizedWeapon, "-", " ")

		if normalizedProf == normalizedWeapon {
			return true
		}

		// Also check weapon name if available
		if hasWeapon && strings.ToLower(weapon.Name) == normalizedProf {
			return true
		}

		// Match regardless of word order or spelling ("light crossbow" = "crossbow-light"),
		// and accept plurals ("daggers").
		for _, candidate := range []string{prof, strings.TrimSuffix(prof, "s")} {
			pk := weaponNameKey(candidate)
			if pk == "" || pk == "simple" || pk == "martial" {
				continue
			}
			if pk == weaponNameKey(weaponKey) || (hasWeapon && pk == weaponNameKey(weapon.Name)) {
				return true
			}
		}
	}

	return false
}

// Check if a character is proficient with armor
// armorProfsStr is comma-separated list: "light, medium, shields" or "all armor, shields"
// armorCategory is "light", "medium", "heavy", or "shield"
func isArmorProficient(armorProfsStr string, armorCategory string) bool {
	if armorProfsStr == "" {
		return false
	}

	profs := strings.Split(strings.ToLower(armorProfsStr), ",")
	categoryLower := strings.ToLower(armorCategory)

	for _, prof := range profs {
		prof = strings.TrimSpace(prof)

		// "all armor" covers light, medium, and heavy (but not shields)
		if prof == "all armor" && (categoryLower == "light" || categoryLower == "medium" || categoryLower == "heavy") {
			return true
		}

		// Direct match
		if prof == categoryLower {
			return true
		}

		// Handle plural forms
		if prof == "shields" && categoryLower == "shield" {
			return true
		}
	}

	return false
}

// isWearingNonProficientArmor checks if a character is wearing armor they're not proficient with
// "If you wear armor that you lack proficiency with, you have disadvantage on any
// ability check, saving throw, or attack roll that involves Strength or Dexterity, and you can't cast spells."
// Armor proficiency penalty enforcement
func isWearingNonProficientArmor(charID int) bool {
	var equippedArmor sql.NullString
	var equippedShield bool
	var armorProfs string

	err := db.QueryRow(`SELECT COALESCE(equipped_armor, ''), COALESCE(equipped_shield, false), COALESCE(armor_proficiencies, '')
		FROM characters WHERE id = $1`, charID).Scan(&equippedArmor, &equippedShield, &armorProfs)
	if err != nil {
		return false
	}

	// Check equipped armor
	if equippedArmor.Valid && equippedArmor.String != "" {
		armorInfo, err := getArmorInfo(equippedArmor.String)
		if err == nil && armorInfo != nil {
			if !isArmorProficient(armorProfs, armorInfo.Type) {
				return true
			}
		}
	}

	// Check equipped shield
	if equippedShield && !strings.Contains(strings.ToLower(armorProfs), "shield") {
		return true
	}

	return false
}

// Cover bonuses for AC
// Half cover: +2 AC, Three-quarters cover: +5 AC, Full cover: can't be targeted
var coverBonuses = map[string]int{
	"none":           0,
	"half":           2,
	"three_quarters": 5,
	"three-quarters": 5,
	"full":           0, // Full cover means can't be targeted, not an AC bonus
}

// getBodyWeightBySize returns estimated body weight in pounds based on 5e size category
// Based on PHB/DMG guidelines: Small ~40 lbs, Medium ~150 lbs, Large ~500 lbs
func getBodyWeightBySize(size string) float64 {
	switch strings.ToLower(size) {
	case "tiny":
		return 8.0
	case "small":
		return 40.0
	case "medium":
		return 150.0
	case "large":
		return 500.0
	case "huge":
		return 2000.0
	case "gargantuan":
		return 8000.0
	default:
		return 150.0 // Default to Medium
	}
}

// calculateInventoryWeight calculates total weight from a character's inventory
func calculateInventoryWeight(inventory []map[string]interface{}) float64 {
	totalWeight := 0.0

	for _, item := range inventory {
		itemName, _ := item["name"].(string)
		quantity := 1
		if q, ok := item["quantity"].(float64); ok {
			quantity = int(q)
		}

		// Use the item's own weight, else the SRD weight, else look it up in the database
		weight := itemUnitWeight(item)
		if weight <= 0 {
			// Try to look up weight from SRD
			itemSlug := strings.ToLower(strings.ReplaceAll(itemName, " ", "-"))

			// Check weapons
			var dbWeight float64
			err := db.QueryRow(`SELECT COALESCE(weight, 0) FROM weapons WHERE slug = $1`, itemSlug).Scan(&dbWeight)
			if err == nil && dbWeight > 0 {
				weight = dbWeight
			} else {
				// Check armor
				err = db.QueryRow(`SELECT COALESCE(weight, 0) FROM armor WHERE slug = $1`, itemSlug).Scan(&dbWeight)
				if err == nil && dbWeight > 0 {
					weight = dbWeight
				}
			}
		}

		totalWeight += weight * float64(quantity)
	}

	return totalWeight
}

// getCharacterTotalWeight returns the character's total weight (body + equipment)
// If petrified, the weight is multiplied by 10
func getCharacterTotalWeight(charID int) map[string]interface{} {
	// Get character species and inventory
	var species string
	var inventoryJSON []byte
	var conditionsJSON []byte
	err := db.QueryRow(`SELECT species, COALESCE(inventory, '[]'), COALESCE(conditions, '[]') FROM characters WHERE id = $1`, charID).Scan(&species, &inventoryJSON, &conditionsJSON)
	if err != nil {
		return nil
	}

	// Get species size
	var size string
	size = getSpeciesSize(species) // the 2024 species

	// Calculate body weight
	bodyWeight := getBodyWeightBySize(size)

	// Calculate equipment weight
	var inventory []map[string]interface{}
	json.Unmarshal(inventoryJSON, &inventory)
	inventory = usableInventory(inventory) // a lost bag is not carried
	equipmentWeight := calculateInventoryWeight(inventory)

	// Total weight
	totalWeight := bodyWeight + equipmentWeight

	// Check for petrified condition
	var conditions []string
	json.Unmarshal(conditionsJSON, &conditions)
	isPetrified := false
	for _, c := range conditions {
		if strings.ToLower(c) == "petrified" {
			isPetrified = true
			break
		}
	}

	result := map[string]interface{}{
		"race_size":            size,
		"body_weight_lbs":      bodyWeight,
		"equipment_weight_lbs": equipmentWeight,
		"total_weight_lbs":     totalWeight,
	}

	if isPetrified {
		petrifiedWeight := totalWeight * 10
		result["petrified"] = true
		result["petrified_weight_lbs"] = petrifiedWeight
		result["petrified_note"] = "When petrified, weight increases by a factor of 10. The petrified creature and all nonmagical objects it carries are transformed to stone."
	}

	return result
}

// dropHeldItems drops equipped weapons when character becomes unconscious
// Called internally when unconscious condition is applied
func dropHeldItems(charID int) []string {
	var mainHand, offHand sql.NullString
	err := db.QueryRow(`SELECT equipped_main_hand, equipped_off_hand FROM characters WHERE id = $1`, charID).Scan(&mainHand, &offHand)
	if err != nil {
		return nil
	}

	droppedItems := []string{}
	if mainHand.Valid && mainHand.String != "" {
		droppedItems = append(droppedItems, mainHand.String)
	}
	if offHand.Valid && offHand.String != "" {
		droppedItems = append(droppedItems, offHand.String)
	}

	if len(droppedItems) > 0 {
		db.Exec(`UPDATE characters SET equipped_main_hand = NULL, equipped_off_hand = NULL WHERE id = $1`, charID)
	}

	return droppedItems
}
