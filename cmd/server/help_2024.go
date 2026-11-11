package main

// Rules from the 2024 re-audit (agentrpg-reference/working-docs/AUDIT-2024-RULES.md, part B):
//
//	Help (SRD 5.2.1 Rules Glossary)  POST /api/action {"action": "help", ...}
//	    Assist an Attack Roll: "target_id" is an enemy within 5 feet of you; the next attack roll by one of your allies
//	    against it has Advantage. The benefit expires at the start of your next turn.
//	    Assist an Ability Check: "target_character_id" is an ally and the description names one of your skill or tool
//	    proficiencies; the ally has Advantage on its next ability check with it, until the start of your next turn.
//	Grappled (SRD 5.2.1): Disadvantage on attack rolls against any target other than the grappler.
//	Martial Arts (Monk level 1): an Unarmed Strike as a Bonus Action ("martial_arts"); Monk weapons (Simple Melee weapons
//	    and Martial Melee weapons with the Light property) use Dexterity and the Martial Arts die, while the Monk wears no
//	    armor and carries no Shield.

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type helpReq struct{ Ally, Enemy int }

// helpTargets holds the ally and the enemy of a Help action while it resolves.
var helpTargets sync.Map

var helpSkills = []string{"acrobatics", "animal_handling", "arcana", "athletics", "deception", "history", "insight",
	"intimidation", "investigation", "medicine", "nature", "perception", "performance", "persuasion", "religion",
	"sleight_of_hand", "stealth", "survival"}

func init() {
	effectText["assisted"] = "The next attack roll against it by one of the helper's allies has Advantage (Help)"
}

// helpAction is the Help action.
func helpAction(charID int, description string) string {
	var req helpReq
	if v, ok := helpTargets.Load(charID); ok {
		req, _ = v.(helpReq)
	}
	lobbyID := lobbyOfChar(charID)
	if req.Enemy != 0 {
		name := ""
		for _, e := range combatEntries(lobbyID) {
			if asInt(e["id"]) == req.Enemy {
				name = fmt.Sprint(e["name"])
			}
		}
		if name == "" && req.Enemy > 0 {
			db.QueryRow(`SELECT name FROM characters WHERE id = $1 AND lobby_id = $2`, req.Enemy, lobbyID).Scan(&name)
		}
		if name == "" {
			return "Help: target_id must be an enemy in this fight (see situation.enemies in GET /api/my-turn). Nothing was assisted."
		}
		addCombatEffect(lobbyID, req.Enemy, charID, "assisted")
		return fmt.Sprintf("🤝 Help: you distract %s (it must be within 5 feet of you). The next attack roll by one of your allies against it has Advantage; the benefit ends at the start of your next turn.", name)
	}
	if req.Ally != 0 {
		var allyName string
		var allyLobby int
		if req.Ally == charID || db.QueryRow(`SELECT name, COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, req.Ally).Scan(&allyName, &allyLobby) != nil || allyLobby != lobbyID {
			return "Help: target_character_id must be another character in your campaign. Nothing was assisted."
		}
		key, label, ok := helpProficiency(charID, description)
		if !ok {
			return "Help: name one of your skill or tool proficiencies in the description (Assist an Ability Check needs proficiency in it). Nothing was assisted."
		}
		addCombatEffect(lobbyID, req.Ally, charID, "assisted_check:"+key)
		return fmt.Sprintf("🤝 Help: %s has Advantage on the next ability check it makes with %s, if it makes one before the start of your next turn.", allyName, label)
	}
	return "Help: send target_id (an enemy within 5 feet of you) to give an ally Advantage on its next attack roll against it, or target_character_id (an ally) with one of your skill or tool proficiencies in the description to give it Advantage on its next check with it."
}

// helpProficiency finds the skill or tool named in the description among the helper's proficiencies.
func helpProficiency(charID int, description string) (key, label string, ok bool) {
	var skills, tools sql.NullString
	db.QueryRow(`SELECT skill_proficiencies, tool_proficiencies FROM characters WHERE id = $1`, charID).Scan(&skills, &tools)
	low := strings.ToLower(strings.ReplaceAll(description, "_", " "))
	have := map[string]bool{}
	for _, s := range strings.Split(skills.String, ",") {
		have[strings.ReplaceAll(strings.TrimSpace(strings.ToLower(s)), " ", "_")] = true
	}
	for _, s := range helpSkills {
		if strings.Contains(low, strings.ReplaceAll(s, "_", " ")) && have[s] {
			return s, titleCase(strings.ReplaceAll(s, "_", " ")), true
		}
	}
	for _, t := range strings.Split(tools.String, ",") {
		t = strings.TrimSpace(t)
		if t != "" && strings.Contains(low, strings.ToLower(t)) {
			return strings.ToLower(t), t, true
		}
	}
	return "", "", false
}

// assistedTarget says whether the monster the character attacks carries a Help from someone else.
func assistedTarget(charID int) bool {
	lobbyID, id, ok := attackedMonster(charID)
	if !ok {
		return false
	}
	for _, e := range combatEffectsOn(lobbyID, id) {
		if e.Kind == "assisted" && e.SourceID != charID {
			return true
		}
	}
	return false
}

// takeHelpedCheck uses up an ally's Assist an Ability Check for this skill or tool, if there is one.
func takeHelpedCheck(charID int, skillOrTool string) bool {
	if skillOrTool == "" {
		return false
	}
	return takeCombatEffect(lobbyOfChar(charID), charID, "assisted_check:"+strings.ToLower(skillOrTool), 0)
}

// attackTargetID is the id of the creature the character is attacking (a negative monster id or a character id), or 0.
func attackTargetID(charID int) int {
	if v, ok := attackTargets.Load(charID); ok {
		if id, ok := v.(int); ok {
			return id
		}
	}
	return 0
}

// grapplerOf is the creature grappling id (a character id, or a negative monster id) when id is Grappled by a grapple.
func grapplerOf(lobbyID, id int) (int, bool) {
	for _, rec := range loadConditionRecords(lobbyID, id) {
		var g int
		if strings.EqualFold(rec.Condition, "grappled") {
			if _, err := fmt.Sscanf(rec.Source, "Grapple #%d", &g); err == nil {
				return g, true
			}
		}
	}
	if id > 0 {
		var raw string
		db.QueryRow(`SELECT COALESCE(conditions::text, '') FROM characters WHERE id = $1`, id).Scan(&raw)
		if i := strings.Index(strings.ToLower(raw), "grappled:"); i >= 0 {
			digits := strings.TrimLeft(raw[i+len("grappled:"):], " ")
			end := 0
			for end < len(digits) && (digits[end] == '-' || (digits[end] >= '0' && digits[end] <= '9')) {
				end++
			}
			if g, err := strconv.Atoi(digits[:end]); err == nil {
				return g, true
			}
		}
	}
	return 0, false
}

// grappledAttackPenalty: a Grappled attacker has Disadvantage on attack rolls against any target other than its grappler.
func grappledAttackPenalty(lobbyID, attackerID, targetID int) bool {
	if lobbyID == 0 || attackerID == 0 || targetID == 0 {
		return false
	}
	g, ok := grapplerOf(lobbyID, attackerID)
	return ok && g != targetID
}

// martialArtsApplies: a Monk unarmed or with a Monk weapon, wearing no armor and carrying no Shield.
func martialArtsApplies(charID int, class string, hasWeapon bool, w SRDWeapon) bool {
	if strings.ToLower(class) != "monk" || (hasWeapon && !isMonkWeapon(class, w)) {
		return false
	}
	var armor sql.NullString
	var shield sql.NullBool
	db.QueryRow(`SELECT equipped_armor, equipped_shield FROM characters WHERE id = $1`, charID).Scan(&armor, &shield)
	return (!armor.Valid || armor.String == "") && !(shield.Valid && shield.Bool)
}

// monkWeaponDice is a weapon's damage dice, with the Martial Arts die in its place when that die is larger.
func monkWeaponDice(charID int, class string, level int, w SRDWeapon) string {
	if !martialArtsApplies(charID, class, true, w) {
		return w.Damage
	}
	var n, sides int
	if _, err := fmt.Sscanf(w.Damage, "%dd%d", &n, &sides); err != nil || n != 1 {
		return w.Damage
	}
	if ma := game.MartialArtsDie(level); ma > sides {
		return fmt.Sprintf("1d%d", ma)
	}
	return w.Damage
}
