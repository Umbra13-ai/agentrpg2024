package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"log"
	"strconv"
	"strings"
)

// conditionEffects are the 15 conditions with their SRD 5.2.1 Rules Glossary text (word for word), by lower-case name.
var conditionEffects = map[string]string{}

func init() {
	for _, e := range srdConditions() {
		conditionEffects[strings.ToLower(e.Name)] = strings.Join(e.Text, " ")
	}
}

// hasCondition checks if a character has a specific condition
func hasCondition(charID int, condition string) bool {
	var conditionsJSON []byte
	err := db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&conditionsJSON)
	if err != nil {
		return false
	}
	conditions := normalizeConditionList(parseConditionsJSON(conditionsJSON))
	condition = strings.ToLower(condition)
	if downedCondition(condition) && charAtZeroHP(charID) {
		return true
	}
	for _, c := range conditions {
		if strings.ToLower(c) == condition {
			return true
		}
	}
	return false
}

// getCharConditions returns all conditions for a character
func getCharConditions(charID int) []string {
	var conditionsJSON []byte
	db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&conditionsJSON)
	return normalizeConditionList(parseConditionsJSON(conditionsJSON))
}

// F48: at 0 Hit Points a living character is Unconscious (SRD 5.2.1 "Falling to 0 Hit Points"), and Unconscious brings the
// Incapacitated and Prone conditions. The server doesn't store these when a character drops (healing would have to clear them);
// they follow from the Hit Points, so hasCondition and isIncapacitated report them.
func atZeroHP(hp, maxHP int, dead bool) bool { return hp <= 0 && maxHP > 0 && !dead }

func charAtZeroHP(charID int) bool {
	var hp, maxHP int
	var dead bool
	if db.QueryRow("SELECT COALESCE(hp, 0), COALESCE(max_hp, 0), COALESCE(is_dead, false) FROM characters WHERE id = $1", charID).Scan(&hp, &maxHP, &dead) != nil {
		return false
	}
	return atZeroHP(hp, maxHP, dead)
}

func downedCondition(condition string) bool {
	switch condition {
	case "unconscious", "incapacitated", "prone":
		return true
	}
	return false
}

// removeCondition removes a specific condition from a character
// Used for standing up from prone, breaking grapple, etc.
func removeCondition(charID int, condition string) bool {
	condition = strings.ToLower(condition)
	conditions := getCharConditions(charID)
	wholeCondition := isRuleCondition(condition) && !strings.Contains(condition, ":") // "grappled" ends "grappled:12" too

	newConditions := []string{}
	removed := false
	for _, c := range conditions {
		if strings.ToLower(c) == condition || (wholeCondition && conditionBase(c) == condition) {
			removed = true
		} else {
			newConditions = append(newConditions, c)
		}
	}

	if removed {
		updated, _ := json.Marshal(newConditions)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updated, charID)
		syncRecordsFromList(charID)
	}
	return removed
}

func parseConditionsJSON(raw []byte) []string {
	var conditions []string
	if len(raw) == 0 {
		return conditions
	}
	if err := json.Unmarshal(raw, &conditions); err == nil {
		return conditions
	}
	return parseConditionsString(string(raw))
}

func parseConditionsString(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return []string{}
	}

	var conditions []string
	if err := json.Unmarshal([]byte(raw), &conditions); err == nil {
		return conditions
	}

	// Some legacy writes stored the JSON condition array as a JSON string.
	// Decode that extra layer before falling back to the old comma-separated
	// representation. Without this, spectator responses expose fragments such
	// as `[\"dodging` instead of the actual condition name.
	var encoded string
	if err := json.Unmarshal([]byte(raw), &encoded); err == nil && encoded != raw {
		return parseConditionsString(encoded)
	}

	parts := strings.Split(raw, ",")
	parsed := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "[]\"")
		if part == "" {
			continue
		}
		parsed = append(parsed, part)
	}
	return parsed
}

// responseConditions normalizes legacy condition data before it reaches API
// consumers. This keeps status endpoints readable even before the startup
// cleanup has repaired pre-existing rows.
func responseConditions(raw []byte) []string {
	return normalizeConditionList(parseConditionsJSON(raw))
}

// spectatorConditions produces the public, display-friendly condition list.
// It accepts both canonical JSON arrays and legacy JSON-encoded arrays.
func spectatorConditions(raw string) []string {
	activeConditions := []string{}
	for _, condition := range responseConditions([]byte(raw)) {
		if strings.HasPrefix(condition, "exhaustion:") {
			activeConditions = append(activeConditions, condition)
		} else if strings.Contains(condition, ":") {
			// Strip IDs from conditions like "charmed:5" -> "charmed".
			activeConditions = append(activeConditions, strings.Split(condition, ":")[0])
		} else {
			activeConditions = append(activeConditions, condition)
		}
	}
	return activeConditions
}

func normalizeConditionList(conditions []string) []string {
	if len(conditions) == 0 {
		return []string{}
	}
	normalized := make([]string, 0, len(conditions))
	seen := make(map[string]bool)
	for _, condition := range conditions {
		condition = strings.TrimSpace(condition)
		condition = strings.Trim(condition, "[]\"")
		if condition == "" {
			continue
		}
		key := strings.ToLower(condition)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, condition)
	}
	return normalized
}

// cleanupDuplicateCharacterConditions repairs legacy rows once the service
// starts. It is idempotent and preserves distinct parameterized conditions.
func cleanupDuplicateCharacterConditions() {
	rows, err := db.Query("SELECT id, COALESCE(conditions, '[]') FROM characters")
	if err != nil {
		log.Printf("Condition cleanup skipped: %v", err)
		return
	}

	type conditionRepair struct {
		id         int
		conditions []byte
	}
	repairs := make([]conditionRepair, 0)
	for rows.Next() {
		var id int
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}
		conditions := parseConditionsJSON(raw)
		normalized := normalizeConditionList(conditions)
		encoded, _ := json.Marshal(normalized)
		if string(encoded) == strings.TrimSpace(string(raw)) {
			continue
		}
		repairs = append(repairs, conditionRepair{id: id, conditions: encoded})
	}
	if err := rows.Close(); err != nil {
		log.Printf("Condition cleanup read failed: %v", err)
		return
	}

	updated := 0
	for _, repair := range repairs {
		if _, err := db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", repair.conditions, repair.id); err == nil {
			updated++
		}
	}
	if updated > 0 {
		log.Printf("Removed duplicate conditions from %d character(s)", updated)
	}
}

// characterHPStatus provides the concise party-status label used by GM and
// spectator reads. A character at 0 HP who has made three successful death
// saves is unconscious but stable, not dying.
func characterHPStatus(hp, maxHP int, isStable, isDead bool) string {
	if isDead {
		return "dead"
	}
	if hp == 0 {
		if isStable {
			return "stable"
		}
		return "dying"
	}
	if hp <= maxHP/4 {
		return "critical"
	}
	if hp <= maxHP/2 {
		return "wounded"
	}
	return "healthy"
}

// removeOneWithShadowsInvisibility checks for and removes the invisible:one_with_shadows condition
// This is called when a character moves, takes an action, or uses a reaction.
// One with Shadows: "invisible until you move or take an action or a reaction"
func removeOneWithShadowsInvisibility(charID int) bool {
	conditions := getCharConditions(charID)

	newConditions := []string{}
	removed := false
	for _, c := range conditions {
		if c == "invisible:one_with_shadows" {
			removed = true
		} else {
			newConditions = append(newConditions, c)
		}
	}

	if removed {
		updated, _ := json.Marshal(newConditions)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updated, charID)
		syncRecordsFromList(charID)
		// Log to campaign if applicable
		var campaignID sql.NullInt64
		var charName string
		db.QueryRow("SELECT lobby_id, name FROM characters WHERE id = $1", charID).Scan(&campaignID, &charName)
		if campaignID.Valid {
			db.Exec(`INSERT INTO actions (campaign_id, character_id, action_type, description, created_at)
				VALUES ($1, $2, 'other', $3, NOW())`,
				campaignID.Int64, charID, fmt.Sprintf("👁️ %s's invisibility from One with Shadows ends", charName))
		}
	}
	return removed
}

// conditionListHas checks if a condition list contains a specific condition
// Helper for checking conditions without database query when list is already available
// Now delegates to game.HasCondition for consistency.
func conditionListHas(conditions []string, condition string) bool {
	return game.HasCondition(conditions, condition)
}

// isIncapacitated checks if character cannot take actions or reactions
// Per 5e: paralyzed, stunned, unconscious, petrified, and incapacitated all prevent actions
func isIncapacitated(charID int) bool {
	if charAtZeroHP(charID) {
		return true
	}
	conditions := getCharConditions(charID)
	for _, c := range conditions {
		switch strings.ToLower(c) {
		case "incapacitated", "paralyzed", "stunned", "unconscious", "petrified":
			return true
		}
	}
	return false
}

// getPaladinAuraBonus returns the bonus to saving throws from Paladin's Aura of Protection
// At level 6+, allies within 10ft (30ft at level 18) add Paladin's CHA mod to saves
// Returns the highest bonus from all conscious Paladins in the party
// Note: Since we don't track positions, we assume party members are within aura range
// The GM can separate the party narratively if needed
func getPaladinAuraBonus(campaignID int, excludeCharID int) (int, string) {
	// Find all Paladins in this campaign who are:
	// 1. Level 6+ (Aura of Protection unlocks at level 6)
	// 2. Conscious (not incapacitated, unconscious, etc.)
	rows, err := db.Query(`
		SELECT c.id, c.name, c.cha, c.level, COALESCE(c.class_levels, '{}')::jsonb as class_levels
		FROM characters c
		WHERE c.lobby_id = $1
		  AND c.id != $2
		  AND (LOWER(c.class) = 'paladin' OR c.class_levels::text ILIKE '%paladin%')
	`, campaignID, excludeCharID)
	if err != nil {
		return 0, ""
	}
	defer rows.Close()

	bestBonus := 0
	bestPaladinName := ""

	for rows.Next() {
		var charID int
		var charName string
		var cha, level int
		var classLevelsJSON []byte

		if err := rows.Scan(&charID, &charName, &cha, &level, &classLevelsJSON); err != nil {
			continue
		}

		// Check if this Paladin is incapacitated (aura requires consciousness)
		if isIncapacitated(charID) {
			continue
		}

		// Determine Paladin level (for multiclass)
		paladinLevel := 0
		var classLevels map[string]int
		if err := json.Unmarshal(classLevelsJSON, &classLevels); err == nil && len(classLevels) > 0 {
			// Multiclass - check Paladin-specific level
			for className, lvl := range classLevels {
				if strings.ToLower(className) == "paladin" {
					paladinLevel = lvl
					break
				}
			}
		} else {
			// Single class - total level is Paladin level
			paladinLevel = level
		}

		// Aura of Protection requires Paladin level 6+
		if paladinLevel < 6 {
			continue
		}

		// The aura reaches 10 feet (30 feet at level 18); with a battle map the distance counts
		if !auraReaches(campaignID, charID, excludeCharID, paladinLevel) {
			continue
		}

		// Calculate CHA modifier (minimum +1 per PHB)
		chaBonus := game.Modifier(cha)
		if chaBonus < 1 {
			chaBonus = 1
		}

		// Track the best bonus
		if chaBonus > bestBonus {
			bestBonus = chaBonus
			bestPaladinName = charName
		}
	}

	return bestBonus, bestPaladinName
}

// canMove checks if character's speed is not reduced to 0 by conditions
// Grappled, restrained, and stunned set speed to 0
func canMove(charID int) bool {
	if game.SpeedZeroAny(getCharConditions(charID)) {
		return false
	}
	return exhaustionLevelOf(charID) < 6
}

// autoFailsSave checks if a condition causes automatic failure on a saving throw type
// Paralyzed, Petrified, Stunned and Unconscious fail Strength and Dexterity saves automatically (SRD 5.2.1)
func autoFailsSave(charID int, ability string) bool {
	conditions := getCharConditions(charID)
	ability = strings.ToLower(ability)
	if ability == "str" || ability == "strength" || ability == "dex" || ability == "dexterity" {
		for _, c := range conditions {
			switch strings.ToLower(c) {
			case "paralyzed", "petrified", "stunned", "unconscious":
				return true
			}
		}
	}
	return false
}

// isAutoCrit checks if attack against target is automatically a critical hit
// Paralyzed and unconscious targets: hits from within 5 feet are auto-crits
func isAutoCrit(targetID int) bool {
	conditions := getCharConditions(targetID)
	for _, c := range conditions {
		switch strings.ToLower(c) {
		case "paralyzed", "unconscious":
			return true
		}
	}
	return false
}

// getSaveDisadvantage checks if conditions impose disadvantage on saves
// Exhaustion 3+ gives disadvantage on all saves
// Restrained gives disadvantage on DEX saves
// Non-proficient armor gives disadvantage on STR/DEX saves
func getSaveDisadvantage(charID int, ability string) bool {
	ability = strings.ToLower(ability)
	if moreSaveDisadvantage(lobbyOfChar(charID), charID, ability) { // Reduce (spells_more_2024.go)
		return true
	}
	if deckDisadvantage(charID) { // the Mysterious Deck's Fool: Disadvantage on D20 Tests for 72 hours
		return true
	}

	// Non-proficient armor: disadvantage on STR/DEX saves
	if ability == "str" || ability == "strength" || ability == "dex" || ability == "dexterity" {
		if isWearingNonProficientArmor(charID) {
			return true
		}
	}

	// Restrained: disadvantage on DEX saves
	if ability == "dex" || ability == "dexterity" {
		if hasCondition(charID, "restrained") {
			return true
		}
	}
	return false
}

// getCharacterName returns the name of a character by ID
func getCharacterName(charID int) string {
	var name string
	db.QueryRow("SELECT COALESCE(name, 'Unknown') FROM characters WHERE id = $1", charID).Scan(&name)
	return name
}

// releaseAllGrapplesFrom releases all creatures that the specified character is grappling
// Called when a grappler becomes incapacitated (5e PHB: grapple ends if grappler incapacitated)
// Returns list of character names that were released
func releaseAllGrapplesFrom(grapplerID int) []string {
	released := []string{}
	grappleCondition := fmt.Sprintf("grappled:%d", grapplerID)

	// Find all characters with this grapple condition
	rows, err := db.Query(`
		SELECT id, name, COALESCE(conditions, '[]') 
		FROM characters 
		WHERE conditions::text LIKE $1`, "%"+grappleCondition+"%")
	if err != nil {
		return released
	}
	defer rows.Close()

	for rows.Next() {
		var charID int
		var charName string
		var condJSON []byte
		rows.Scan(&charID, &charName, &condJSON)

		var conditions []string
		json.Unmarshal(condJSON, &conditions)

		// Remove the grapple condition
		newConditions := []string{}
		wasGrappled := false
		for _, c := range conditions {
			if c == grappleCondition {
				wasGrappled = true
			} else {
				newConditions = append(newConditions, c)
			}
		}

		if wasGrappled {
			updated, _ := json.Marshal(newConditions)
			db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updated, charID)
			syncRecordsFromList(charID)
			released = append(released, charName)
		}
	}

	return released
}

// breakGrapplesOnTarget removes all grappled conditions FROM a character
// Called when a grappled creature is forcibly moved out of reach (shove, Thunderwave, etc.)
// Returns list of grappler names whose grapples were broken
func breakGrapplesOnTarget(targetID int) []string {
	broken := []string{}

	// Get target's current conditions
	var condJSON []byte
	var targetName string
	err := db.QueryRow(`SELECT name, COALESCE(conditions, '[]') FROM characters WHERE id = $1`, targetID).Scan(&targetName, &condJSON)
	if err != nil {
		return broken
	}

	var conditions []string
	json.Unmarshal(condJSON, &conditions)

	// Find and remove all grappled conditions
	newConditions := []string{}
	for _, c := range conditions {
		if strings.HasPrefix(c, "grappled:") {
			// Extract grappler ID and get their name
			parts := strings.Split(c, ":")
			if len(parts) == 2 {
				grapplerID, _ := strconv.Atoi(parts[1])
				var grapplerName string
				err := db.QueryRow(`SELECT name FROM characters WHERE id = $1`, grapplerID).Scan(&grapplerName)
				if err == nil {
					broken = append(broken, grapplerName)
				} else {
					broken = append(broken, fmt.Sprintf("unknown(id:%d)", grapplerID))
				}
			}
		} else if strings.ToLower(c) == "grappled" {
			// Generic grappled without specific grappler
			broken = append(broken, "unknown")
		} else {
			newConditions = append(newConditions, c)
		}
	}

	if len(broken) > 0 {
		updated, _ := json.Marshal(newConditions)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updated, targetID)
		syncRecordsFromList(targetID)
	}

	return broken
}

// isIncapacitatingCondition checks if a condition prevents taking actions
func isIncapacitatingCondition(condition string) bool {
	baseCondition := condition
	if idx := strings.Index(condition, ":"); idx != -1 {
		baseCondition = condition[:idx]
	}
	switch strings.ToLower(baseCondition) {
	case "incapacitated", "paralyzed", "stunned", "unconscious", "petrified":
		return true
	}
	return false
}

// getCharmerID returns the ID of who charmed this character, or 0 if not charmed
// Charmed condition format: "charmed" (generic) or "charmed:123" (charmed by character 123)
func getCharmerID(charID int) int {
	conditions := getCharConditions(charID)
	for _, c := range conditions {
		cLower := strings.ToLower(c)
		if strings.HasPrefix(cLower, "charmed:") {
			parts := strings.Split(c, ":")
			if len(parts) == 2 {
				if id, err := strconv.Atoi(parts[1]); err == nil {
					return id
				}
			}
		}
	}
	return 0
}

// isCharmedBy checks if charID is charmed by charmerID
func isCharmedBy(charID, charmerID int) bool {
	charmer := getCharmerID(charID)
	return charmer == charmerID && charmerID > 0
}

// getSanctuaryDC returns the DC of a target's sanctuary condition, or 0 if not protected
// Sanctuary condition format: "sanctuary" (generic DC 13) or "sanctuary:15" (specific DC)
func getSanctuaryDC(charID int) int {
	conditions := getCharConditions(charID)
	for _, c := range conditions {
		cLower := strings.ToLower(c)
		if strings.HasPrefix(cLower, "sanctuary:") {
			parts := strings.Split(c, ":")
			if len(parts) == 2 {
				if dc, err := strconv.Atoi(parts[1]); err == nil {
					return dc
				}
			}
		} else if cLower == "sanctuary" {
			return 13 // Default spell DC
		}
	}
	return 0
}

// checkSanctuaryProtection checks if target is protected by Sanctuary and handles the save
// Returns (blockMessage, noteText) - blockMessage is non-empty if attack is blocked
func checkSanctuaryProtection(attackerID, targetID, attackerWis int) (string, string) {
	if targetID <= 0 {
		return "", ""
	}

	sanctuaryDC := getSanctuaryDC(targetID)
	if sanctuaryDC == 0 {
		return "", ""
	}

	// Get target name for messaging
	var targetName string
	db.QueryRow("SELECT name FROM characters WHERE id = $1", targetID).Scan(&targetName)
	if targetName == "" {
		targetName = "the target"
	}

	// Attacker must make WIS save against the DC
	wisMod := game.Modifier(attackerWis)
	roll := game.RollDie(20)
	total := roll + wisMod

	// Check for Halfling Lucky on nat 1
	luckyNote := ""
	var attackerSpecies string
	db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", attackerID).Scan(&attackerSpecies)
	if roll == 1 && strings.ToLower(attackerSpecies) == "halfling" {
		reroll := game.RollDie(20)
		luckyNote = fmt.Sprintf(" 🍀[Lucky: %d→%d]", roll, reroll)
		roll = reroll
		total = roll + wisMod
	}

	if total >= sanctuaryDC {
		// Save succeeded - can attack, but include note
		return "", fmt.Sprintf(" (WIS save vs Sanctuary DC %d: %d%s = %d ✓)", sanctuaryDC, roll, luckyNote, total)
	}

	// Save failed - cannot attack this target
	return fmt.Sprintf("🕊️ %s is protected by Sanctuary! WIS save DC %d: rolled %d%s + %d = %d (FAILED). You must choose a different target or take a different action.",
		targetName, sanctuaryDC, roll, luckyNote, wisMod, total), ""
}

// removeSanctuaryOnOffensiveAction removes sanctuary condition when character attacks or casts offensive spell
// Called when a protected character takes offensive action (ends the Sanctuary effect)
func removeSanctuaryOnOffensiveAction(charID int) {
	conditions := getCharConditions(charID)
	newConditions := []string{}
	removed := false
	for _, c := range conditions {
		cLower := strings.ToLower(c)
		if cLower == "sanctuary" || strings.HasPrefix(cLower, "sanctuary:") {
			removed = true
			continue
		}
		newConditions = append(newConditions, c)
	}
	if removed {
		updatedJSON, _ := json.Marshal(newConditions)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updatedJSON, charID)
	}
}

// hasAnyCharm checks if character has any form of charmed condition
func hasAnyCharm(charID int) bool {
	conditions := getCharConditions(charID)
	for _, c := range conditions {
		cLower := strings.ToLower(c)
		if cLower == "charmed" || strings.HasPrefix(cLower, "charmed:") {
			return true
		}
	}
	return false
}

// getFrightenedSourceID returns the ID of what frightened this character, or 0 if not frightened
// Frightened condition format: "frightened" (generic) or "frightened:123" (frightened by character/monster 123)
func getFrightenedSourceID(charID int) int {
	conditions := getCharConditions(charID)
	for _, c := range conditions {
		cLower := strings.ToLower(c)
		if strings.HasPrefix(cLower, "frightened:") {
			parts := strings.Split(c, ":")
			if len(parts) == 2 {
				if id, err := strconv.Atoi(parts[1]); err == nil {
					return id
				}
			}
		}
	}
	return 0
}

// hasAnyFrightened checks if character has any form of frightened condition
func hasAnyFrightened(charID int) bool {
	conditions := getCharConditions(charID)
	for _, c := range conditions {
		cLower := strings.ToLower(c)
		if cLower == "frightened" || strings.HasPrefix(cLower, "frightened:") {
			return true
		}
	}
	return false
}

// getFrightenedSourceName returns the name of what frightened this character
func getFrightenedSourceName(charID int) string {
	sourceID := getFrightenedSourceID(charID)
	if sourceID == 0 {
		return ""
	}

	// Try to get name from characters table first
	var name string
	err := db.QueryRow("SELECT name FROM characters WHERE id = $1", sourceID).Scan(&name)
	if err == nil && name != "" {
		return name
	}

	// Could extend to check monsters table in future
	return fmt.Sprintf("creature #%d", sourceID)
}
