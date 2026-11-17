package main

// What happens to conditions when a creature's turn ends (SRD 5.2.1: effects that last a number of
// rounds, and "repeat the save at the end of each of its turns"). Called from every place that moves
// combat to the next turn.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// validateEndSave checks the end_save the GM sent and fills in the ability name.
func validateEndSave(s *conditionSave) string {
	if s == nil {
		return ""
	}
	ab, ok := abilityNames[strings.ToLower(strings.TrimSpace(s.Ability))]
	if !ok {
		return "end_save.ability must be one of Strength, Dexterity, Constitution, Intelligence, Wisdom, Charisma"
	}
	if s.DC < 1 || s.DC > 30 {
		return "end_save.dc must be between 1 and 30"
	}
	if s.When == "" {
		s.When = "end_of_turn" // most effects: "At the end of each of its turns, the target repeats the save"
	}
	if s.When != "end_of_turn" && s.When != "start_of_turn" {
		return "end_save.when must be end_of_turn (the default) or start_of_turn (for example Conjure Elemental)"
	}
	s.Ability = ab
	return ""
}

// dealConditionTurnDamage: the damage a creature's condition records deal at the start or the end of each of its turns (when), through
// the Hit Point owner of a character (damageHP: resistances, Temporary Hit Points, damage at 0 Hit Points, Concentration) or of a
// monster (damageMonster). It runs from the turn hooks (turn_advance_2024.go) and returns the lines, which go to the action log.
func dealConditionTurnDamage(lobbyID, id int, name, when string) []string {
	if id == 0 || isCompanionCombatID(id) {
		return nil
	}
	var log []string
	for _, rec := range loadConditionRecords(lobbyID, id) {
		for _, d := range rec.TurnDamage {
			if d.When != when {
				continue
			}
			n := game.RollDamage(d.Dice, false)
			var took string
			if id > 0 {
				hit, err := damageHP(id, oneHit(n, d.Type))
				if err != nil {
					logConditionFailure("turn damage", id, err)
					continue
				}
				took = name + " " + hit.Sentence(d.Type)
			} else {
				text, ok := damageMonster(lobbyID, id, n, d.Type)
				if !ok {
					continue
				}
				took = text
			}
			phase := "At the end of its turn"
			if when == "start_of_turn" {
				phase = "At the start of its turn"
			}
			log = append(log, strings.TrimSuffix(fmt.Sprintf("%s, %s deals %s %s damage: %s", phase, rec.phrase(), d.Dice, titleCase(d.Type), took), ".")+".")
		}
	}
	for _, line := range log {
		recordGMAction(db, lobbyID, "condition", line, line)
	}
	return log
}

// endTurnEffects runs at the end of a turn. endingIndex is the position in the turn order of the creature
// whose turn is ending, or -1 for the current turn stored in the database. It returns what happened.
func endTurnEffects(campaignID, endingIndex int) []string {
	return turnEffects(campaignID, endingIndex, "end_of_turn")
}

// startTurnEffects runs after combat has moved to the next creature: saves that repeat at the start of its turn.
func startTurnEffects(campaignID int) []string {
	return turnEffects(campaignID, -1, "start_of_turn")
}

func turnEffects(campaignID, endingIndex int, when string) []string {
	var raw []byte
	var cur int
	if db == nil || db.QueryRow(`SELECT turn_order, current_turn_index FROM combat_state WHERE lobby_id = $1 AND active = true`, campaignID).Scan(&raw, &cur) != nil {
		return nil
	}
	if endingIndex < 0 {
		endingIndex = cur
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	if endingIndex < 0 || endingIndex >= len(entries) {
		return nil
	}
	id, name := asInt(entries[endingIndex]["id"]), fmt.Sprint(entries[endingIndex]["name"])
	if when == "end_of_turn" && id > 0 {
		vexTurnEnded(id)
		innateSorceryTick(id)
		superiorDefenseTick(id)
	}
	if when == "start_of_turn" {
		expireCombatEffectsOf(campaignID, id) // Sap, Slow, Hamstring Blow ... last until the start of the causer's next turn
	}
	if when == "end_of_turn" { // a GM rider (adjust-monster) lasts until the end of the creature's next turn if it isn't used up first
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND source_id = $3`, campaignID, id, gmRiderSource)
	}
	var log []string
	if when == "start_of_turn" {
		log = append(log, monsterFeaturesTurnStart(campaignID, id, name)...) // Legendary Action uses, Regeneration, Recharge, aura saves
	} else {
		log = append(log, monsterFeaturesTurnEnd(campaignID, id, name)...) // damage auras (Fire Aura ...)
	}
	if when == "start_of_turn" {
		if b := burningTurnStart(campaignID, id, name); b != "" { // Burning: 1d4 Fire at the start of the turn
			log = append(log, b)
		}
		log = append(log, spellEffectsTurnStart(campaignID, id, name)...) // Command, Slow
	} else {
		log = append(log, spellEffectsTurnEnd(campaignID, id, name)...) // Slow's repeated save, Command ends, spell durations
	}
	if when == "end_of_turn" && id > 0 {
		if n := rageTick(id); n != "" {
			log = append(log, n)
		}
	}
	if when == "end_of_turn" && id < 0 {
		log = append(log, spiritGuardiansTurnEnd(campaignID, id)...) // Spirit Guardians: a monster ending its turn in the Emanation
	}
	for _, rec := range loadConditionRecords(campaignID, id) {
		ended := ""
		if s := rec.EndSave; s != nil && (s.When == when || (s.When == "" && when == "end_of_turn")) {
			phase := "at the end of its turn"
			if when == "start_of_turn" {
				phase = "at the start of its turn"
			}
			// the one saving throw function (save_2024.go): Dodge on a Dexterity save, Haste, Foresight, Brave against Frightened ...
			sv := (&saveCtx{lobby: campaignID, saver: id, ability: s.Ability, dc: s.DC, against: []string{rec.Condition}}).roll()
			switch {
			case sv.Success:
				ended = fmt.Sprintf("%s repeats the %s saving throw %s and succeeds%s: the %s condition ends.", name, s.Ability, phase, sv.Tail(), rec.Condition)
			case id < 0 && legendaryResistance(campaignID, id, saveStakes{Condition: true}):
				ended = fmt.Sprintf("%s repeats the %s saving throw %s: the %s condition ends (Legendary Resistance).", name, s.Ability, phase, rec.Condition)
			default:
				log = append(log, fmt.Sprintf("%s repeats the %s saving throw %s and fails%s: the %s condition continues.", name, s.Ability, phase, sv.Tail(), rec.Condition))
			}
		}
		if ended == "" && rec.DurationRounds > 0 && when == "end_of_turn" {
			if countDownCondition(campaignID, id, rec.ID) { // the condition owner: one round off, or it runs out
				log = append(log, fmt.Sprintf("The %s condition on %s runs out.", rec.Condition, name))
			}
			continue
		}
		if ended != "" {
			deleteConditionRecord(campaignID, id, rec.ID)
			log = append(log, ended)
		}
	}
	for _, line := range log {
		recordGMAction(db, campaignID, "condition", line, line)
	}
	return log
}
