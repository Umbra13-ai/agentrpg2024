package main

// Fixes from Brannoc's coverage session (level20-battle-tests/s22_brannoc_fighter.py, Nov 8 2026; FINDINGS F94-F99):
//	Remarkable Athlete     after a Critical Hit a Champion can move up to half its Speed without Opportunity Attacks (F94)
//	Sword of Sharpness     a natural 20 against a creature: 14 Slashing damage more and 1 Exhaustion level (F95)
//	Powerful Build         a Goliath counts as one size larger for its carrying capacity (F96, character_gear.go)
//	Javelin of Lightning   the Lightning Bolt: a 5-foot Line to a target within 120 feet, DC 13 Dexterity, 4d6 Lightning, once a dawn (F97)
//	Indomitable            the reroll keeps the save's other bonuses (Ring of Protection, Warding Bond, Bless, exhaustion) (F98)
//	Potion of Growth       the enlarge effect of Enlarge/Reduce for 10 minutes, no Concentration (F99)

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// remarkableAthleteNote: the Champion's move after a Critical Hit (said in the attack's reply; the player moves on the map).
func remarkableAthleteNote(charID int, crit bool) string {
	if !crit {
		return ""
	}
	class, sub, level := subclassOf(charID)
	if !strings.EqualFold(class, "fighter") || !strings.EqualFold(sub, "champion") || level < 3 {
		return ""
	}
	return " Remarkable Athlete: you can move up to half your Speed now without provoking Opportunity Attacks."
}

// swordOfSharpnessHit: a natural 20 against a creature with the attuned Sword of Sharpness.
func swordOfSharpnessHit(lobbyID, charID, targetID int, monsterKey string, natural int) (int, string) {
	if natural != 20 {
		return 0, ""
	}
	if _, ok := attunedTo(charID, "sword of sharpness"); !ok {
		return 0, " (the Sword of Sharpness needs attunement for its sharpness)"
	}
	d, how := adjustMonsterDamage(monsterKey, "slashing", 14)
	note := fmt.Sprintf(" Sword of Sharpness: +%d Slashing", d)
	if how == "resistant" {
		note += " (resisted)"
	}
	addMonsterCondition(lobbyID, targetID, "exhaustion", "Sword of Sharpness", 0, "", 0)
	return d, note + " and 1 Exhaustion level."
}

// powerfulBuildSize: the size a Goliath counts as for carrying capacity (one larger).
func powerfulBuildSize(species, size string) string {
	if !strings.Contains(strings.ToLower(species), "goliath") {
		return size
	}
	for i, s := range sizeCategories {
		if strings.EqualFold(s, size) && i+1 < len(sizeCategories) {
			return sizeCategories[i+1]
		}
	}
	return size
}

// javelinOfLightning: the Lightning Bolt. The target (target_id) and every other creature in the 5-foot Line between the thrower and
// it make a DC 13 Dexterity save: 4d6 Lightning, half on a success. The javelin reappears in the hand. Once until the next dawn.
func javelinOfLightning(charID int, it invItem, req itemCastReq, description string) string {
	if dawnUsed(it, "lightning_bolt") {
		return "Cannot throw the Javelin of Lightning as a bolt: it can't be used that way again until the next dawn (attack with it as a javelin instead). Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if req.TargetID >= 0 && len(req.TargetIDs) == 0 {
		return "Cannot throw the Lightning Bolt: name the target with target_id (a monster within 120 feet; target_ids adds the others in the Line). Nothing was spent."
	}
	targets := append([]int{}, req.TargetIDs...)
	if req.TargetID < 0 {
		targets = append([]int{req.TargetID}, targets...)
	}
	if m, ok := activeEncounterMap(lobby); ok {
		me, ok1 := m.token(charID)
		if t, ok2 := m.token(targets[0]); ok1 && ok2 && tokenDistanceFeet(me, t) > 120 {
			return fmt.Sprintf("Cannot throw the Lightning Bolt: %s is %d ft away; the bolt reaches a target no farther than 120 feet. Nothing was spent.", t.Name, tokenDistanceFeet(me, t))
		}
	}
	dmg := game.RollDamage("4d6", false)
	var parts []string
	seen := map[int]bool{}
	for _, id := range targets {
		if seen[id] || id == charID {
			continue
		}
		seen[id] = true
		name := creatureName(lobby, id)
		saved, total := creatureSave(lobby, id, "dex", 13)
		d := dmg
		how := "fails"
		if saved {
			d, how = dmg/2, "succeeds on"
		}
		var res string
		if id < 0 {
			res, _ = damageMonster(lobby, id, d, "lightning")
		} else {
			res = name + " " + damageCharacter(id, d, "lightning")
		}
		parts = append(parts, fmt.Sprintf("%s %s the DC 13 Dexterity save (%d): %s", name, how, total, strings.TrimSpace(res)))
	}
	markDawnUsed(charID, it, "lightning_bolt")
	return fmt.Sprintf("⚡ Javelin of Lightning: you throw it and it becomes a bolt of lightning in a 5-foot-wide Line (4d6 = %d Lightning). %s. The javelin reappears in your hand; the bolt can't be used again until the next dawn.", dmg, strings.Join(parts, "; "))
}

// indomitableExtraBonus: the save's bonuses other than the ability modifier and proficiency (Indomitable rerolls the same save).
func indomitableExtraBonus(charID int, ability string) int {
	b := magicSaveBonus(charID) + buffSaveBonus(charID) - game.ExhaustionD20Penalty(exhaustionLevelOf(charID))
	adj, _ := spellRollAdjust(lobbyOfChar(charID), charID, "save:"+ability)
	return b + adj
}

// potionOfGrowth: the enlarge effect for 10 minutes (100 rounds); an item's effect needs no Concentration.
func potionOfGrowth(lobby, targetID int, label string) string {
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'enlarge_reduce'`, lobby, targetID)
	size := resizeToken(lobby, targetID, 1)
	addSpellEffect(lobby, targetID, targetID, "Potion of Growth", "enlarge_reduce", fmt.Sprintf("enlarge|%d|%d", lobby, size), 100, "", 0)
	return fmt.Sprintf("🧪 %s: the enlarge effect of Enlarge/Reduce for 10 minutes (no Concentration): one size larger, Advantage on Strength checks and saves, and weapon and Unarmed Strike hits deal 1d4 more.", label)
}
