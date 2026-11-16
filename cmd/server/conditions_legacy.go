package main

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
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

// removeOneWithShadowsInvisibility ends the invisible:one_with_shadows entry (One with Shadows: "invisible until you move or take an
// action or a reaction"). Called when the character moves, takes an action or uses a reaction.
func removeOneWithShadowsInvisibility(charID int) bool {
	if !hasCondition(charID, "invisible:one_with_shadows") || !removeCondition(charID, "invisible:one_with_shadows") {
		return false
	}
	if lobby := lobbyOfChar(charID); lobby != 0 {
		writeAction(db, lobby, charID, "one_with_shadows", fmt.Sprintf("👁️ %s's invisibility from One with Shadows ends", getCharacterName(charID)), "")
	}
	return true
}

// conditionListHas checks if a condition list contains a specific condition
// Helper for checking conditions without database query when list is already available
// Now delegates to game.HasCondition for consistency.
func conditionListHas(conditions []string, condition string) bool {
	return game.HasCondition(conditions, condition)
}

// isIncapacitated: the character can't take actions or Reactions: an Incapacitating condition (game.IsIncapacitatingCondition), or
// 0 Hit Points (Unconscious).
func isIncapacitated(charID int) bool {
	return charAtZeroHP(charID) || game.IsIncapacitated(getCharConditions(charID))
}

// canMove checks if character's speed is not reduced to 0 by conditions
// Grappled, restrained, and stunned set speed to 0
func canMove(charID int) bool {
	if game.SpeedZeroAny(getCharConditions(charID)) {
		return false
	}
	return exhaustionLevelOf(charID) < 6
}

// getCharacterName returns the name of a character by ID
func getCharacterName(charID int) string {
	var name string
	db.QueryRow("SELECT COALESCE(name, 'Unknown') FROM characters WHERE id = $1", charID).Scan(&name)
	return name
}

// grappledBy: the record is a grapple held by the creature (a grapple's source, or an old "grappled:ID" entry).
func grappledBy(r conditionRecord, grapplerID int) bool {
	return r.Condition == "grappled" && (r.Source == grappleSource(grapplerID) || r.Detail == strconv.Itoa(grapplerID))
}

// releaseAllGrapplesFrom ends every grapple the creature holds (SRD 5.2.1, Ending a Grapple: the grappler has the Incapacitated
// condition). It returns the names of the creatures released.
func releaseAllGrapplesFrom(lobbyID, grapplerID int) []string {
	where := `condition = 'grappled' AND (source = $1 OR detail = $2)`
	args := []interface{}{grappleSource(grapplerID), strconv.Itoa(grapplerID)}
	if grapplerID < 0 { // a monster's id is its campaign's
		where += ` AND lobby_id = $3`
		args = append(args, lobbyID)
	}
	var found []conditionRecord
	for _, r := range findConditionRecords(where, args...) {
		if grappledBy(r, grapplerID) {
			found = append(found, r)
		}
	}
	released := []string{}
	seen := map[int]bool{}
	for _, r := range endConditionRecords(found) {
		if !seen[r.TargetID] {
			seen[r.TargetID] = true
			released = append(released, creatureName(r.LobbyID, r.TargetID))
		}
	}
	return released
}

// breakGrapplesOnTarget ends every grapple on a character (it was moved out of reach: a shove, Thunderwave ...). It returns the
// names of the grapplers ("unknown" for a grapple whose grappler isn't recorded).
func breakGrapplesOnTarget(targetID int) []string {
	broken := []string{}
	for _, r := range endCondition(0, targetID, "grappled", "") {
		id, err := strconv.Atoi(r.Detail)
		if err != nil {
			if _, scanErr := fmt.Sscanf(r.Source, "Grapple #%d", &id); scanErr != nil {
				broken = append(broken, "unknown")
				continue
			}
		}
		broken = append(broken, creatureName(r.LobbyID, id))
	}
	return broken
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
func checkSanctuaryProtection(attackerID, targetID int) (string, string) {
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

	// the attacker's Wisdom save against the spell (save_2024.go)
	sv := (&saveCtx{saver: attackerID, ability: "wis", dc: sanctuaryDC, spell: true}).roll()
	if sv.Success {
		// Save succeeded - can attack, but include note
		return "", " [Sanctuary: " + sv.ForSaver("Wisdom saving throw") + "]"
	}

	// Save failed - cannot attack this target
	return fmt.Sprintf("🕊️ %s is protected by Sanctuary! %s. You must choose a different target or take a different action.",
		targetName, sv.ForSaver("Wisdom saving throw")), ""
}

// removeSanctuaryOnOffensiveAction ends a character's sanctuary entry when it attacks or casts an offensive spell.
func removeSanctuaryOnOffensiveAction(charID int) {
	removeConditionsWhere(0, charID, func(r conditionRecord) bool { return r.Condition == "sanctuary" })
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
