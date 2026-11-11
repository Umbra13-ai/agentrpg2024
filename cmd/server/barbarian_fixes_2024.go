package main

// Fixes from Korga's coverage session (level20-battle-tests/s23_korga_barbarian.py, Nov 8 2026; FINDINGS F100-):
//	Rage                 Resistance to Bludgeoning, Piercing and Slashing damage, and Advantage on Strength checks and saves while
//	                     raging: the rage action said so, but no roll or damage used it (F100)
//	Primal Knowledge     while raging, an Acrobatics, Intimidation, Perception, Stealth or Survival check can use Strength (F101)
//	Indomitable Might    a Strength check or save totalling less than the Strength score uses the score (F102)
//	Flame Tongue         a Bonus Action (attuned) sets it ablaze: +2d6 Fire on a hit until the command again or it's put away (F103)
//	Ring of Free Action  attuned: magic can't Paralyze or Restrain the wearer or reduce its Speeds; Difficult Terrain costs nothing extra (F104)

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// raging: the character's Rage is active.
func raging(charID int) bool {
	return charID > 0 && hasCondition(charID, "raging")
}

// rageResists: Rage's Resistance to Bludgeoning, Piercing and Slashing damage.
func rageResists(charID int, dtype string) bool {
	switch strings.ToLower(dtype) {
	case "bludgeoning", "piercing", "slashing":
		return raging(charID)
	}
	return false
}

// primalKnowledgeAbility: the ability a raging Barbarian (level 3+) uses for a check with one of the five skills when the request asks for
// Strength ("ability": "str"); otherwise the skill's own ability.
func primalKnowledgeAbility(charID int, skill, asked, normal string) (string, bool) {
	if a := strings.ToLower(asked); a != "str" && a != "strength" {
		return normal, false
	}
	switch strings.ToLower(strings.ReplaceAll(skill, " ", "_")) {
	case "acrobatics", "intimidation", "perception", "stealth", "survival":
	default:
		return normal, false
	}
	class, _, level := subclassOf(charID)
	if !strings.EqualFold(class, "barbarian") || level < 3 || !raging(charID) {
		return normal, false
	}
	return "str", true
}

// indomitableMight: a Barbarian of level 18 uses the Strength score for a Strength check or save that totals less.
func indomitableMight(charID int, ability string, total int) (int, string) {
	a := strings.ToLower(ability)
	if a != "str" && a != "strength" {
		return total, ""
	}
	class, _, level := subclassOf(charID)
	if !strings.EqualFold(class, "barbarian") || level < 18 {
		return total, ""
	}
	var str int
	db.QueryRow(`SELECT str FROM characters WHERE id = $1`, charID).Scan(&str)
	if total < str {
		return str, " [Indomitable Might: the Strength score " + itoa(str) + " stands in for the total]"
	}
	return total, ""
}

func init() {
	noConcentration["flame_tongue"] = true
	spellEffectLabel["flame_tongue"] = "Flame Tongue"
	spellEffectText["flame_tongue"] = "The Flame Tongue is ablaze: +2d6 Fire on a hit; Bright Light 40 feet"
}

// flameTongueToggle: the command word (a Bonus Action) sets the held Flame Tongue ablaze, or puts the flames out.
func flameTongueToggle(charID int, it invItem) string {
	name := itemStr(it, "name")
	if _, ok := attunedTo(charID, "flame tongue"); !ok {
		return "Cannot use the " + name + ": you must be attuned to it (POST /api/characters/attune). Nothing was spent."
	}
	if !strings.EqualFold(strings.TrimSpace(handsOf(charID).Main), name) && !strings.EqualFold(strings.TrimSpace(handsOf(charID).Off), name) {
		return "Cannot use the " + name + ": hold it first (equip-weapon). Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if _, on := charBuff(charID, "flame_tongue"); on {
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'flame_tongue'`, lobby, charID)
		return "You speak the command word again (a Bonus Action): the flames on the " + name + " go out."
	}
	addSpellEffect(lobby, charID, charID, "Flame Tongue", "flame_tongue", name, 999999, "", 0)
	return "You speak the command word (a Bonus Action): flames engulf the " + name + " (Bright Light 40 feet, Dim Light 40 feet more). While it's ablaze its hits deal 2d6 Fire damage more, until you speak the command again or drop, stow or sheathe it."
}

// flameTongueHit: +2d6 Fire on a hit with the ablaze Flame Tongue in hand.
func flameTongueHit(charID int, monsterKey, held string, crit bool) (int, string) {
	e, on := charBuff(charID, "flame_tongue")
	if !on || !strings.EqualFold(e.Detail, held) {
		return 0, ""
	}
	d, how := adjustMonsterDamage(monsterKey, "fire", game.RollDamage("2d6", crit))
	note := fmt.Sprintf(" Flame Tongue: +%d Fire (2d6)", d)
	switch how {
	case "immune":
		return 0, " Flame Tongue: it is immune to Fire damage."
	case "resistant":
		note += ", resisted"
	}
	return d, note + "."
}

// ringOfFreeAction: the character wears an attuned Ring of Free Action.
func ringOfFreeAction(charID int) bool {
	if charID <= 0 {
		return false
	}
	_, ok := attunedTo(charID, "ring of free action")
	return ok
}
