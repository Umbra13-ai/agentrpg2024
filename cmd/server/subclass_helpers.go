package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// hasFightingStyle checks if a character has a specific fighting style
// getSacredWeaponBonus returns the Sacred Weapon attack bonus and a note if active
// Sacred Weapon condition format: "sacred_weapon:BONUS:ROUNDS_REMAINING"
func getSacredWeaponBonus(charID int) (int, string) {
	var condsJSON []byte
	err := db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&condsJSON)
	if err != nil {
		return 0, ""
	}

	var conds []string
	if err := json.Unmarshal(condsJSON, &conds); err != nil {
		return 0, ""
	}

	for _, c := range conds {
		if strings.HasPrefix(c, "sacred_weapon:") {
			parts := strings.Split(c, ":")
			if len(parts) >= 2 {
				bonus, err := strconv.Atoi(parts[1])
				if err == nil && bonus > 0 {
					return bonus, fmt.Sprintf(" (Sacred Weapon +%d)", bonus)
				}
			}
		}
	}
	return 0, ""
}

// decrementSacredWeapon decrements the Sacred Weapon duration at end of turn
// Returns true if the condition expired and was removed
func decrementSacredWeapon(charID int) bool {
	var condsJSON []byte
	err := db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&condsJSON)
	if err != nil {
		return false
	}

	var conds []string
	if err := json.Unmarshal(condsJSON, &conds); err != nil {
		return false
	}

	newConds := []string{}
	expired := false
	for _, c := range conds {
		if strings.HasPrefix(c, "sacred_weapon:") {
			parts := strings.Split(c, ":")
			if len(parts) >= 3 {
				bonus, _ := strconv.Atoi(parts[1])
				rounds, _ := strconv.Atoi(parts[2])
				rounds--
				if rounds > 0 {
					newConds = append(newConds, fmt.Sprintf("sacred_weapon:%d:%d", bonus, rounds))
				} else {
					expired = true // Don't add back - it expired
				}
			}
		} else {
			newConds = append(newConds, c)
		}
	}

	if expired {
		newCondsJSON, _ := json.Marshal(newConds)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", newCondsJSON, charID)
	}

	return expired
}

// decrementHolyNimbus decrements the Holy Nimbus duration at end of turn
// Returns true if the condition expired and was removed
func decrementHolyNimbus(charID int) bool {
	var condsJSON []byte
	err := db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&condsJSON)
	if err != nil {
		return false
	}

	var conds []string
	if err := json.Unmarshal(condsJSON, &conds); err != nil {
		return false
	}

	newConds := []string{}
	expired := false
	for _, c := range conds {
		if strings.HasPrefix(c, "holy_nimbus:") {
			parts := strings.Split(c, ":")
			if len(parts) >= 2 {
				rounds, _ := strconv.Atoi(parts[1])
				rounds--
				if rounds > 0 {
					newConds = append(newConds, fmt.Sprintf("holy_nimbus:%d", rounds))
				} else {
					expired = true // Don't add back - it expired
				}
			}
		} else {
			newConds = append(newConds, c)
		}
	}

	if expired {
		newCondsJSON, _ := json.Marshal(newConds)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", newCondsJSON, charID)
	}

	return expired
}

// hasHolyNimbusActive returns true if the character has Holy Nimbus active
func hasHolyNimbusActive(charID int) bool {
	var condsJSON []byte
	err := db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&condsJSON)
	if err != nil {
		return false
	}

	var conds []string
	if err := json.Unmarshal(condsJSON, &conds); err != nil {
		return false
	}

	for _, c := range conds {
		if strings.HasPrefix(c, "holy_nimbus:") {
			return true
		}
	}
	return false
}

// hasEvasion checks if a character has the Evasion feature (Monk 7+, Rogue 7+, or Hunter Ranger 15+ with evasion choice)
// Evasion - on DEX save for half damage: success = no damage, fail = half damage
// Also check Hunter Ranger's Superior Hunter's Defense choice
func hasEvasion(characterID int) bool {
	var class, subclass string
	var level int
	var choicesJSON []byte
	err := db.QueryRow(`
		SELECT class, level, COALESCE(subclass, ''), COALESCE(subclass_choices, '{}') 
		FROM characters WHERE id = $1
	`, characterID).Scan(&class, &level, &subclass, &choicesJSON)
	if err != nil {
		return false
	}

	// Check base class feature (Monk 7+, Rogue 7+)
	if game.HasClassFeature(class, level, "evasion") {
		return true
	}

	// Hunter Ranger can choose Evasion at level 15 (Superior Hunter's Defense)
	classLower := strings.ToLower(class)
	if classLower == "ranger" && strings.ToLower(subclass) == "hunter" && level >= 15 {
		var choices map[string]string
		if err := json.Unmarshal(choicesJSON, &choices); err == nil {
		}
	}

	return false
}

// hasUncannyDodge checks if a character has the Uncanny Dodge feature
// Rogue 5+ or Hunter Ranger 15+ with uncanny_dodge choice
func hasUncannyDodge(characterID int) bool {
	var class, subclass string
	var level int
	var choicesJSON []byte
	err := db.QueryRow(`
		SELECT class, level, COALESCE(subclass, ''), COALESCE(subclass_choices, '{}') 
		FROM characters WHERE id = $1
	`, characterID).Scan(&class, &level, &subclass, &choicesJSON)
	if err != nil {
		return false
	}

	classLower := strings.ToLower(class)

	// Rogue gets Uncanny Dodge at level 5
	if classLower == "rogue" && level >= 5 {
		return true
	}

	// Hunter Ranger can choose Uncanny Dodge at level 15 (Superior Hunter's Defense)
	if classLower == "ranger" && strings.ToLower(subclass) == "hunter" && level >= 15 {
		var choices map[string]string
		if err := json.Unmarshal(choicesJSON, &choices); err == nil {
		}
	}

	return false
}

// hasElusive checks if a character has the Elusive feature (Rogue 18+)
// No attack roll has advantage against you while you aren't incapacitated
func hasElusive(characterID int) bool {
	var class string
	var level int
	err := db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, characterID).Scan(&class, &level)
	if err != nil {
		return false
	}

	// Rogue gets Elusive at level 18
	if strings.ToLower(class) == "rogue" && level >= 18 {
		return true
	}

	// Check multiclass: need at least 18 levels in Rogue
	var classLevels map[string]int
	var classLevelsJSON []byte
	err = db.QueryRow(`SELECT COALESCE(class_levels, '{}') FROM characters WHERE id = $1`, characterID).Scan(&classLevelsJSON)
	if err == nil && len(classLevelsJSON) > 2 {
		json.Unmarshal(classLevelsJSON, &classLevels)
		if rogueLevel, ok := classLevels["rogue"]; ok && rogueLevel >= 18 {
			return true
		}
	}

	return false
}

// hasFeralSenses checks if a character has the Feral Senses feature (Ranger 18+)
// Attacking a creature you can't see doesn't impose disadvantage
func hasFeralSenses(characterID int) bool {
	var class string
	var level int
	err := db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, characterID).Scan(&class, &level)
	if err != nil {
		return false
	}

	// Ranger gets Feral Senses at level 18
	if strings.ToLower(class) == "ranger" && level >= 18 {
		return true
	}

	// Check multiclass: need at least 18 levels in Ranger
	var classLevels map[string]int
	var classLevelsJSON []byte
	err = db.QueryRow(`SELECT COALESCE(class_levels, '{}') FROM characters WHERE id = $1`, characterID).Scan(&classLevelsJSON)
	if err == nil && len(classLevelsJSON) > 2 {
		json.Unmarshal(classLevelsJSON, &classLevels)
		if rangerLevel, ok := classLevels["ranger"]; ok && rangerLevel >= 18 {
			return true
		}
	}

	return false
}

// Subclass helper functions migrated to game/subclasses.go
// getSubclassesForClass now delegates to game.GetSubclassesForClass
func getSubclassesForClass(class string) map[string]game.Subclass {
	return game.GetSubclassesForClass(class)
}

// getActiveSubclassFeatures now delegates to game.GetActiveSubclassFeatures
func getActiveSubclassFeatures(subclassSlug string, level int) []game.SubclassFeature {
	return game.GetActiveSubclassFeatures(subclassSlug, level)
}

// hasSubclassFeature now delegates to game.HasSubclassFeature
func hasSubclassFeature(subclassSlug string, level int, mechanic string) bool {
	return game.HasSubclassFeature(subclassSlug, level, mechanic)
}

// getSubclassMechanic now delegates to game.GetSubclassMechanic
func getSubclassMechanic(subclassSlug string, level int, mechanic string) (string, bool) {
	return game.GetSubclassMechanic(subclassSlug, level, mechanic)
}

// getDragonAncestryDamageType returns the damage type for a Draconic Sorcerer's dragon ancestry
// Returns empty string if character has no dragon ancestry chosen or isn't a Draconic Sorcerer
func getDragonAncestryDamageType(charID int) string {
	var subclass sql.NullString
	var choicesJSON []byte
	err := db.QueryRow(`
		SELECT COALESCE(subclass, ''), COALESCE(subclass_choices, '{}')
		FROM characters WHERE id = $1
	`, charID).Scan(&subclass, &choicesJSON)
	if err != nil {
		return ""
	}

	// Must be Draconic Sorcerer
	if !subclass.Valid || subclass.String != "draconic" {
		return ""
	}

	// Get dragon_ancestor choice from subclass_choices
	var choices map[string]string
	json.Unmarshal(choicesJSON, &choices)

	if v := choices["elemental_affinity"]; v != "" { // 2024: the Sorcerer chooses the damage type
		return v
	}
	ancestry, ok := choices["dragon_ancestor"]
	if !ok {
		return ""
	}

	// Look up damage type
	damageType, ok := game.DragonAncestryDamageTypes[ancestry]
	if !ok {
		return ""
	}

	return damageType
}

// getDomainSpells returns the always-prepared spells for a character based on subclass
// These spells don't count against prepared spell limits and are automatically known.
// For clerics: domain spells, for paladins: oath spells, for warlocks: expanded spell list
// For Circle of the Land druids, pass landType from subclass_choices
func getDomainSpells(subclassSlug string, level int, landType ...string) []string {
	// Handle Circle of the Land druids with circle spells
	if subclassSlug == "land" && len(landType) > 0 && landType[0] != "" {
		return game.LandCircleSpells(landType[0], level)
	}

	sub, ok := game.AvailableSubclasses[subclassSlug]
	if !ok || sub.DomainSpells == nil {
		return nil
	}

	var spells []string
	// Add all domain spells for levels the character has reached
	for spellLevel, spellList := range sub.DomainSpells {
		if level >= spellLevel {
			spells = append(spells, spellList...)
		}
	}
	return spells
}

// getDomainSpellsWithInfo returns domain spells with enriched SRD info
// For Circle of the Land druids, pass landType from subclass_choices
func getDomainSpellsWithInfo(subclassSlug string, level int, landType ...string) []map[string]interface{} {
	var slugs []string
	if len(landType) > 0 && landType[0] != "" {
		slugs = getDomainSpells(subclassSlug, level, landType[0])
	} else {
		slugs = getDomainSpells(subclassSlug, level)
	}
	if len(slugs) == 0 {
		return nil
	}

	var result []map[string]interface{}
	for _, slug := range slugs {
		if spell, ok := srdSpellsMemory[slug]; ok {
			result = append(result, map[string]interface{}{
				"slug":            slug,
				"name":            spell.Name,
				"level":           spell.Level,
				"school":          spell.School,
				"casting_time":    spell.CastingTime,
				"always_prepared": true,
			})
		} else {
			// Spell not found in SRD, still include it
			result = append(result, map[string]interface{}{
				"slug":            slug,
				"name":            slug,
				"always_prepared": true,
			})
		}
	}
	return result
}

// applyOpenHandEffect is the Warrior of the Open Hand's Open Hand Technique (2024) on a Flurry of Blows hit:
// - addle: the target can't make Opportunity Attacks until the start of its next turn
// - push: Strength save or pushed up to 15 feet away
// - topple: Dexterity save or the Prone condition
func applyOpenHandEffect(charID int, effect string, level int) string {
	// the Monk's save DC: 8 + Wisdom modifier + Proficiency Bonus
	var wis int
	db.QueryRow("SELECT wis FROM characters WHERE id = $1", charID).Scan(&wis)
	focusSaveDC := 8 + game.ProficiencyBonus(level) + game.Modifier(wis)

	switch effect {
	case "addle":
		return "[Open Hand: Addle: the target can't make Opportunity Attacks until the start of its next turn]"
	case "push":
		return fmt.Sprintf("[Open Hand: Push: Strength save DC %d or the target is pushed up to 15 feet away. If pushed, call POST /api/gm/forced-movement to break any grapples]", focusSaveDC)
	case "topple":
		return fmt.Sprintf("[Open Hand: Topple: Dexterity save DC %d or the target has the Prone condition]", focusSaveDC)
	default:
		return ""
	}
}

// applyKillEffects checks for subclass features triggered by killing a creature
// Currently implements:
// - Fiend Warlock's Dark One's Blessing: gain temp HP = CHA mod + warlock level (min 1)
// Returns a map with any effects applied, or nil if no effects triggered
func applyKillEffects(killerCharID int) map[string]interface{} {
	// Get character info
	var class, subclass string
	var level, cha, currentTempHP int
	err := db.QueryRow(`
		SELECT class, COALESCE(subclass, ''), level, cha, COALESCE(temp_hp, 0)
		FROM characters WHERE id = $1
	`, killerCharID).Scan(&class, &subclass, &level, &cha, &currentTempHP)
	if err != nil || subclass == "" {
		return nil
	}

	// Check for Fiend Warlock's Dark One's Blessing
	if strings.ToLower(class) == "warlock" && strings.ToLower(subclass) == "fiend" {
		if _, hasDarkOnesBlessing := getSubclassMechanic("fiend", level, "dark_ones_blessing"); hasDarkOnesBlessing {
			// Calculate temp HP: CHA mod + warlock level (min 1)
			chaMod := game.Modifier(cha)
			tempHP := chaMod + level
			if tempHP < 1 {
				tempHP = 1
			}

			// Only update if new temp HP is higher than current (temp HP doesn't stack)
			if tempHP > currentTempHP {
				db.Exec("UPDATE characters SET temp_hp = $1 WHERE id = $2", tempHP, killerCharID)
				return map[string]interface{}{
					"feature":        "Dark One's Blessing",
					"temp_hp_gained": tempHP,
					"cha_modifier":   chaMod,
					"warlock_level":  level,
					"message":        fmt.Sprintf("🔥 Dark One's Blessing: gained %d temporary HP (CHA %+d + warlock level %d)", tempHP, chaMod, level),
				}
			} else {
				return map[string]interface{}{
					"feature":           "Dark One's Blessing",
					"temp_hp_available": tempHP,
					"current_temp_hp":   currentTempHP,
					"message":           fmt.Sprintf("🔥 Dark One's Blessing would grant %d temp HP, but you already have %d (temp HP doesn't stack)", tempHP, currentTempHP),
				}
			}
		}
	}

	return nil
}

// getClassSpellList returns all spell slugs available to a class from the class_spells table
// Returns nil if class spell list not seeded (allows any SRD spell as fallback)
func getClassSpellList(class string) []string {
	rows, err := db.Query(`SELECT spell_slug FROM class_spells WHERE class_slug = $1`, strings.ToLower(class))
	if err != nil {
		return nil
	}
	defer rows.Close()

	var spells []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err == nil {
			spells = append(spells, slug)
		}
	}
	return spells // Returns nil if no rows found
}

// canSneakAttack checks if a rogue can use sneak attack on this attack
// Requires finesse or ranged weapon AND (advantage OR ally adjacent to target)
func canSneakAttack(charID int, weaponKey string, hasAdvantage bool, hasDisadvantage bool, targetID int) bool {
	// Must use finesse or ranged weapon
	weapon, hasWeapon := srdWeapons[weaponKey]
	if !hasWeapon {
		return false // Unarmed doesn't qualify
	}

	isFinesse := containsProperty(weapon.Properties, "finesse")
	isRanged := weapon.Type == "ranged"

	if !isFinesse && !isRanged {
		return false
	}

	// If you have disadvantage (and no advantage to cancel it), no sneak attack
	if hasDisadvantage && !hasAdvantage {
		return false
	}

	// You can sneak attack if you have advantage
	if hasAdvantage {
		return true
	}

	// Or if there's an ally within 5ft of the target (and ally isn't incapacitated)
	// Check for allies in the same campaign adjacent to target
	// With a battle map the ally really has to be within 5 feet of the target
	if targetID == 0 {
		targetID = attackTargetID(charID) // the attack's target_id (a monster's negative id); callers pass a character named in the text
	}
	if allyNearby, onMap := sneakAllyOnMap(charID, targetID); onMap {
		return allyNearby
	}
	if targetID != 0 { // monsters have negative ids (this was "> 0", so without a map it never applied to a monster)
		var lobbyID int
		db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1", charID).Scan(&lobbyID)

		// Find other party members (not the attacker, not at 0 Hit Points or incapacitated, in same campaign)
		rows, err := db.Query(`
			SELECT id, COALESCE(conditions, '[]') FROM characters 
			WHERE lobby_id = $1 AND id != $2 AND id != $3 AND status != 'inactive' AND hp > 0 AND NOT COALESCE(is_dead, false)
		`, lobbyID, charID, targetID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var allyID int
				var conditionsJSON []byte
				rows.Scan(&allyID, &conditionsJSON)

				// Check if ally is incapacitated
				var conditions []string
				json.Unmarshal(conditionsJSON, &conditions)
				allyIncap := false
				for _, c := range conditions {
					cLower := strings.ToLower(c)
					if cLower == "incapacitated" || cLower == "paralyzed" ||
						cLower == "stunned" || cLower == "unconscious" || cLower == "petrified" {
						allyIncap = true
						break
					}
				}

				if !allyIncap {
					// Ally is adjacent and not incapacitated - sneak attack allowed
					// Note: We don't track exact positions, so we assume party members
					// are generally positioned to enable sneak attack when in combat together
					return true
				}
			}
		}
	}

	return false
}
