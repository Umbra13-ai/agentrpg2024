package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"strconv"
	"strings"
)

// getMonkDie returns the monk's Martial Arts damage die based on level
// getMonkDie returns the monk's Martial Arts damage die based on level
// now delegates to game.MartialArtsDie
func getMonkDie(level int) string {
	return fmt.Sprintf("1d%d", game.MartialArtsDie(level))
}

// parseMarkSpell checks if concentrating_on contains a mark spell and returns the target ID
// Returns: spellSlug ("hunters-mark" or "hex"), targetID (or 0 if not a mark spell)
func parseMarkSpell(concentratingOn string) (spellSlug string, targetID int) {
	lower := strings.ToLower(concentratingOn)

	// Check for Hunter's Mark format: "hunter's-mark:123" or "hunters-mark:123" or "hunter's mark:123"
	if strings.HasPrefix(lower, "hunter") && strings.Contains(lower, "mark") {
		parts := strings.Split(concentratingOn, ":")
		if len(parts) >= 2 {
			if id, err := strconv.Atoi(strings.TrimSpace(parts[len(parts)-1])); err == nil {
				return "hunters-mark", id
			}
		}
	}

	// Check for Hex format: "hex:123"
	if strings.HasPrefix(lower, "hex:") {
		parts := strings.Split(concentratingOn, ":")
		if len(parts) >= 2 {
			if id, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				return "hex", id
			}
		}
	}

	return "", 0
}

// getMarkBonusDamage calculates bonus damage from Hunter's Mark or Hex spells
// Returns damage amount and a note string for the attack result
func getMarkBonusDamage(attackerID, targetID int, isCrit bool) (int, string) {
	if targetID == 0 {
		if _, mid, ok := attackedMonster(attackerID); ok {
			targetID = mid
		}
	}
	if targetID == 0 {
		return 0, ""
	}

	var concentratingOn string
	err := db.QueryRow("SELECT COALESCE(concentrating_on, '') FROM characters WHERE id = $1", attackerID).Scan(&concentratingOn)
	if err != nil || concentratingOn == "" {
		return 0, ""
	}

	spellSlug, markedTarget := parseMarkSpell(concentratingOn)
	if spellSlug == "" || markedTarget != targetID {
		return 0, ""
	}

	// Roll 1d6 (doubled on crit); a Ranger of level 20 (Foe Slayer) rolls a d10 for Hunter's Mark
	sides := 6
	if spellSlug == "hunters-mark" {
		sides = markDieSides(attackerID)
	}
	var dmg int
	if isCrit {
		dmg = game.RollDie(sides) + game.RollDie(sides)
	} else {
		dmg = game.RollDie(sides)
	}

	// Build note based on spell type
	diceStr := fmt.Sprintf("1d%d", sides)
	if isCrit {
		diceStr = fmt.Sprintf("2d%d", sides)
	}

	switch spellSlug {
	case "hunters-mark":
		return dmg, fmt.Sprintf(" (+%d Hunter's Mark, %s)", dmg, diceStr)
	case "hex":
		return dmg, fmt.Sprintf(" (+%d Hex, %s necrotic)", dmg, diceStr)
	}

	return 0, ""
}

// isMarkSpell returns true if the spell slug is Hunter's Mark or Hex
func isMarkSpell(spellSlug string) bool {
	lower := strings.ToLower(spellSlug)
	return lower == "hunters-mark" || lower == "hunter's-mark" || lower == "hex"
}

// castDealsNoDamage: spells whose damage dice aren't dealt to a target when they're cast. Hunter's Mark and Hex add damage to later
// hits; the others' damage falls on the caster or comes later: Wish (the stress of a non-duplicating wish), Meld into Stone (the stone
// is destroyed), Dimension Door and Teleport (a mishap), Contact Other Plane (a failed save), Geas (acting against the command).
func castDealsNoDamage(spellSlug string) bool {
	switch strings.ToLower(spellSlug) {
	case "wish", "meld-into-stone", "dimension-door", "teleport", "contact-other-plane", "geas",
		"alter-self", "enlarge-reduce", "shillelagh": // their dice are for later attacks or a weapon, not damage on casting
		return true
	}
	if _, buff := buffSpells[strings.ToLower(spellSlug)]; buff {
		return true // Divine Favor's 1d4, Fire Shield's 2d8: dealt later, by the effect (buff_spells_2024.go)
	}
	return isMarkSpell(spellSlug)
}

// Fighter Indomitable
// Returns max uses based on Fighter level: 1 at 9, 2 at 13, 3 at 17
func getIndomitableMaxUses(class string, level int) int {
	if strings.ToLower(class) != "fighter" {
		return 0
	}
	if level >= 17 {
		return 3
	}
	if level >= 13 {
		return 2
	}
	if level >= 9 {
		return 1
	}
	return 0
}

// getRogueLevel returns the Rogue class level for a character (handles multiclass)
func getRogueLevel(charID int, primaryClass string, totalLevel int) int {
	// Try to get class_levels for multiclass
	var classLevelsJSON []byte
	db.QueryRow("SELECT COALESCE(class_levels, '{}') FROM characters WHERE id = $1", charID).Scan(&classLevelsJSON)

	classLevels := make(map[string]int)
	json.Unmarshal(classLevelsJSON, &classLevels)

	if len(classLevels) > 1 {
		// Multiclass: check for Rogue levels
		for c, lvl := range classLevels {
			if strings.ToLower(c) == "rogue" {
				return lvl
			}
		}
		return 0
	}

	// Single class
	if strings.ToLower(primaryClass) == "rogue" {
		return totalLevel
	}
	return 0
}

// getEffectiveAbilityScore returns the score to use. Primal Champion's +4 (maximum 25) is written into the stored Strength and
// Constitution once, when the Barbarian reaches level 20 (classCapstones, capstone_scores_applied), so the stored score already
// includes it; adding 4 here again showed Strength 26 on the sheet.
func getEffectiveAbilityScore(baseScore int, ability string, class string, level int, classLevels map[string]int) int {
	return baseScore
}

// asiScoreMax is the most an Ability Score Improvement can raise a score to: 20 (SRD 5.2.1). Scores above 20 come only from
// features and items that say so (Primal Champion and Body and Mind, maximum 25; Epic Boons, maximum 30).
const asiScoreMax = 20

// titleCase capitalizes the first letter of each word ("fell glare" -> "Fell Glare"), replacing the deprecated strings.Title for the
// ASCII words the server builds.
func titleCase(s string) string {
	b := []byte(s)
	start := true
	for i, c := range b {
		switch {
		case c == ' ' || c == '-' || c == '_' || c == '(' || c == '/':
			start = true
		case start:
			if c >= 'a' && c <= 'z' {
				b[i] = c - 'a' + 'A'
			}
			start = false
		}
	}
	return string(b)
}

// hasIndomitableMight checks if character has Indomitable Might (Barbarian 18+)
// "If your total for a Strength check is less than your Strength score, you can use that score in place of the total."
func hasIndomitableMight(class string, level int, classLevels map[string]int) bool {
	// Check multiclass first
	if len(classLevels) > 0 {
		for c, lvl := range classLevels {
			if strings.ToLower(c) == "barbarian" && lvl >= 18 {
				return true
			}
		}
		return false
	}
	// Single class check
	return strings.ToLower(class) == "barbarian" && level >= 18
}

// applyIndomitableMight returns the effective total for a STR check, applying Indomitable Might if applicable
// Returns: effectiveTotal, wasApplied, strScore
func applyIndomitableMight(total int, strScore int, class string, level int, classLevels map[string]int) (int, bool, int) {
	if !hasIndomitableMight(class, level, classLevels) {
		return total, false, strScore
	}
	// Get effective STR score (includes Primal Champion bonus if applicable)
	effectiveStr := getEffectiveAbilityScore(strScore, "str", class, level, classLevels)
	if total < effectiveStr {
		return effectiveStr, true, effectiveStr
	}
	return total, false, effectiveStr
}

// getDefensiveTacticsChoice returns the Hunter Ranger's defensive_tactics choice from subclass_choices
func getDefensiveTacticsChoice(characterID int) string {
	var subclassChoicesJSON []byte
	err := db.QueryRow("SELECT COALESCE(subclass_choices, '{}') FROM characters WHERE id = $1", characterID).Scan(&subclassChoicesJSON)
	if err != nil {
		return ""
	}
	var choices map[string]string
	if err := json.Unmarshal(subclassChoicesJSON, &choices); err != nil {
		return ""
	}
	return choices["defensive_tactics"]
}

// hasEscapeTheHorde returns true if character has Escape the Horde
// Escape the Horde: Opportunity attacks against you are made with disadvantage.
func hasEscapeTheHorde(characterID int) bool {
	var class, subclass string
	var level int
	err := db.QueryRow(`
		SELECT COALESCE(class, ''), COALESCE(subclass, ''), level 
		FROM characters WHERE id = $1
	`, characterID).Scan(&class, &subclass, &level)
	if err != nil {
		return false
	}
	if strings.ToLower(class) != "ranger" || strings.ToLower(subclass) != "hunter" || level < 7 {
		return false
	}
	return getDefensiveTacticsChoice(characterID) == "escape_the_horde"
}

// hasMultiattackDefense returns true if character has Multiattack Defense
// Multiattack Defense: a creature that hits you has Disadvantage on its other attack rolls against you this turn.
func hasMultiattackDefense(characterID int) bool {
	var class, subclass string
	var level int
	err := db.QueryRow(`
		SELECT COALESCE(class, ''), COALESCE(subclass, ''), level 
		FROM characters WHERE id = $1
	`, characterID).Scan(&class, &subclass, &level)
	if err != nil {
		return false
	}
	if strings.ToLower(class) != "ranger" || strings.ToLower(subclass) != "hunter" || level < 7 {
		return false
	}
	return getDefensiveTacticsChoice(characterID) == "multiattack_defense"
}

// getMultiattackDefenseHits returns the list of attacker IDs that have hit this character this turn
func getMultiattackDefenseHits(characterID int) []int {
	var hitsJSON []byte
	err := db.QueryRow(`SELECT COALESCE(multiattack_defense_hits, '[]') FROM characters WHERE id = $1`, characterID).Scan(&hitsJSON)
	if err != nil {
		return []int{}
	}
	var hits []int
	json.Unmarshal(hitsJSON, &hits)
	return hits
}

// recordMultiattackDefenseHit adds an attacker to the defender's hit list
// Only records if defender has Multiattack Defense chosen
func recordMultiattackDefenseHit(defenderID, attackerID int) {
	if !hasMultiattackDefense(defenderID) {
		return
	}

	hits := getMultiattackDefenseHits(defenderID)

	// Check if already recorded
	for _, id := range hits {
		if id == attackerID {
			return
		}
	}

	// Add attacker to list
	hits = append(hits, attackerID)
	hitsJSON, _ := json.Marshal(hits)
	db.Exec(`UPDATE characters SET multiattack_defense_hits = $1 WHERE id = $2`, hitsJSON, defenderID)
}

// multiattackDefenseApplies says whether the attacker already hit this defender this turn while the defender has Multiattack
// Defense (SRD 5.2.1 Hunter: "that creature has Disadvantage on all other attack rolls against you this turn").
func multiattackDefenseApplies(defenderID, attackerID int) bool {
	if !hasMultiattackDefense(defenderID) {
		return false
	}
	for _, id := range getMultiattackDefenseHits(defenderID) {
		if id == attackerID {
			return true
		}
	}
	return false
}

// clearAllMultiattackDefenseHits resets hit tracking for all characters in a campaign
// Called at turn transitions
func clearAllMultiattackDefenseHits(campaignID int) {
	db.Exec(`UPDATE characters SET multiattack_defense_hits = '[]' WHERE lobby_id = $1`, campaignID)
}

// ClassResource defines a class resource and its recovery behavior
type ClassResource struct {
	Name         string // Display name (e.g., "Focus Points")
	Key          string // JSON key (e.g., "focus")
	RecoverShort bool   // Recovers on short rest
	RecoverLong  bool   // Recovers on long rest
}

// getCurrentClassResources returns current available resources (max - used)
func getCurrentClassResources(charID int) map[string]int {
	var class string
	var level, cha int
	var usedJSON []byte

	err := db.QueryRow(`
		SELECT class, level, cha, COALESCE(class_resources_used, '{}')
		FROM characters WHERE id = $1
	`, charID).Scan(&class, &level, &cha, &usedJSON)
	if err != nil {
		return nil
	}

	used := make(map[string]int)
	json.Unmarshal(usedJSON, &used)

	chaMod := game.Modifier(cha)
	maxResources := characterMaxResources(charID, class, level, chaMod)

	current := make(map[string]int)
	for key, max := range maxResources {
		current[key] = max - used[key]
		if current[key] < 0 {
			current[key] = 0
		}
	}

	return current
}

// useClassResource spends a resource and returns success/error
func useClassResource(charID int, resourceKey string, amount int) (bool, string, int) {
	var class string
	var level, cha int
	var usedJSON []byte

	err := db.QueryRow(`
		SELECT class, level, cha, COALESCE(class_resources_used, '{}')
		FROM characters WHERE id = $1
	`, charID).Scan(&class, &level, &cha, &usedJSON)
	if err != nil {
		return false, "Character not found", 0
	}

	used := make(map[string]int)
	json.Unmarshal(usedJSON, &used)

	chaMod := game.Modifier(cha)
	max := classResourceMax(charID, class, level, resourceKey, chaMod)

	if max == 0 {
		return false, fmt.Sprintf("Class %s does not have resource '%s'", class, resourceKey), 0
	}

	current := max - used[resourceKey]
	if current < amount {
		return false, fmt.Sprintf("Not enough %s (have %d, need %d)", resourceKey, current, amount), current
	}

	// Spend the resource
	used[resourceKey] += amount
	newUsedJSON, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, newUsedJSON, charID)

	return true, "", current - amount
}

// recoverClassResources recovers resources on rest
func recoverClassResources(charID int, isLongRest bool) map[string]int {
	var class string
	var level, cha int
	var usedJSON []byte

	err := db.QueryRow(`
		SELECT class, level, cha, COALESCE(class_resources_used, '{}')
		FROM characters WHERE id = $1
	`, charID).Scan(&class, &level, &cha, &usedJSON)
	if err != nil {
		return nil
	}

	used := make(map[string]int)
	json.Unmarshal(usedJSON, &used)

	recovered := make(map[string]int)
	resources := characterResourceDefs(charID, class)

	for _, res := range resources {
		// Check if this resource recovers on this type of rest
		if (isLongRest && res.RecoverLong) || (!isLongRest && res.RecoverShort) {
			// Special case: Bard's Bardic Inspiration only recovers on short rest at level 5+
			if res.Key == "bardic_inspiration" && !isLongRest && level < 5 {
				continue
			}

			if used[res.Key] > 0 {
				back := used[res.Key]
				if (res.Key == "rage" || res.Key == "second_wind" || res.Key == "channel_divinity" || res.Key == "wild_shape") && !isLongRest {
					back = 1 // 2024: a Short Rest returns one expended use of Rage, Second Wind or Channel Divinity
				}
				recovered[res.Key] = back
				used[res.Key] -= back
			}
		}
	}

	newUsedJSON, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, newUsedJSON, charID)

	return recovered
}

// getBardicInspirationDie returns the die size for Bardic Inspiration based on bard level
// d6 at level 1, d8 at level 5, d10 at level 10, d12 at level 15
// now delegates to game.BardicInspirationDie
func getBardicInspirationDie(level int) int {
	return game.BardicInspirationDie(level)
}

// getMagicalSecretsSlots returns the number of Magical Secrets spells available to a Bard
// Lore Bards get Additional Magical Secrets at level 6
// All Bards get Magical Secrets at levels 10, 14, 18 (2 spells each)
func getMagicalSecretsSlots(class string, subclass string, level int) int {
	if strings.ToLower(class) != "bard" {
		return 0
	}

	slots := 0

	// Lore Bard Additional Magical Secrets (level 6)
	if strings.ToLower(subclass) == "lore" && level >= 6 {
		slots += 2
	}

	// All Bard Magical Secrets (levels 10, 14, 18)
	if level >= 10 {
		slots += 2
	}
	if level >= 14 {
		slots += 2
	}
	if level >= 18 {
		slots += 2
	}

	return slots
}

// isSpellOnClassList checks if a spell is on the given class's spell list
func isSpellOnClassList(spellSlug, class string) bool {
	classSpellList := getClassSpellList(class)
	if classSpellList == nil || len(classSpellList) == 0 {
		return true // No spell list = no restrictions
	}
	for _, cs := range classSpellList {
		if cs == spellSlug {
			return true
		}
	}
	return false
}

// getMysticArcanumLevelRequirement returns the warlock level required for a given spell level arcanum
func getMysticArcanumLevelRequirement(spellLevel int) int {
	switch spellLevel {
	case 6:
		return 11
	case 7:
		return 13
	case 8:
		return 15
	case 9:
		return 17
	default:
		return 0 // Invalid
	}
}

// getAvailableMysticArcanumLevels returns which arcanum spell levels a warlock can choose based on their level
func getAvailableMysticArcanumLevels(warlockLevel int) []int {
	levels := []int{}
	if warlockLevel >= 11 {
		levels = append(levels, 6)
	}
	if warlockLevel >= 13 {
		levels = append(levels, 7)
	}
	if warlockLevel >= 15 {
		levels = append(levels, 8)
	}
	if warlockLevel >= 17 {
		levels = append(levels, 9)
	}
	return levels
}

// getWizardLevel returns the Wizard class level for a character (handles multiclass)
func getWizardLevel(charID int) int {
	var classLevelsJSON []byte
	var class string
	var level int
	db.QueryRow(`SELECT class, level, COALESCE(class_levels, '{}') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &classLevelsJSON)

	// Check multiclass first
	var classLevels map[string]int
	if err := json.Unmarshal(classLevelsJSON, &classLevels); err == nil && len(classLevels) > 0 {
		if wizLevel, ok := classLevels["wizard"]; ok {
			return wizLevel
		}
		return 0
	}

	// Single class
	if strings.ToLower(class) == "wizard" {
		return level
	}
	return 0
}

// getWarlockLevel returns the Warlock class level for a character (handles multiclass)
func getWarlockLevel(charID int) int {
	var classLevelsJSON []byte
	var class string
	var level int
	db.QueryRow(`SELECT class, level, COALESCE(class_levels, '{}') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &classLevelsJSON)

	// Check multiclass first
	var classLevels map[string]int
	if err := json.Unmarshal(classLevelsJSON, &classLevels); err == nil && len(classLevels) > 0 {
		if warlockLevel, ok := classLevels["warlock"]; ok {
			return warlockLevel
		}
		return 0
	}

	// Single class
	if strings.ToLower(class) == "warlock" {
		return level
	}
	return 0
}

// getDruidLevel returns the Druid class level for a character (handles multiclass)
func getDruidLevel(charID int) int {
	var classLevelsJSON []byte
	var class string
	var level int
	db.QueryRow(`SELECT class, level, COALESCE(class_levels, '{}') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &classLevelsJSON)

	// Check multiclass first
	var classLevels map[string]int
	if err := json.Unmarshal(classLevelsJSON, &classLevels); err == nil && len(classLevels) > 0 {
		if druidLevel, ok := classLevels["druid"]; ok {
			return druidLevel
		}
		return 0
	}

	// Single class
	if strings.ToLower(class) == "druid" {
		return level
	}
	return 0
}

// hasBeastSpells returns true if the character has the Beast Spells feature (Druid level 18+)
// Beast Spells allows casting druid spells while in Wild Shape form
func hasBeastSpells(charID int) bool {
	return getDruidLevel(charID) >= 18
}

// isInWildShape returns true if the character is currently in Wild Shape form
func isInWildShape(charID int) bool {
	var wildShapeForm sql.NullString
	db.QueryRow(`SELECT wild_shape_form FROM characters WHERE id = $1`, charID).Scan(&wildShapeForm)
	return wildShapeForm.Valid && wildShapeForm.String != ""
}

// getWarlockPactSlots returns the number of Pact Magic spell slots for a Warlock at given level
func getWarlockPactSlots(warlockLevel int) int {
	// Warlocks have limited spell slots that are all the same level
	// Slots: 1 at level 1, 2 at level 2, 2 at levels 3-10, 3 at levels 11-16, 4 at levels 17+
	if warlockLevel < 1 {
		return 0
	}
	if warlockLevel == 1 {
		return 1
	}
	if warlockLevel <= 10 {
		return 2
	}
	if warlockLevel <= 16 {
		return 3
	}
	return 4
}
