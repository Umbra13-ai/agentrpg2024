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

var saveAbilityShort = map[string]string{"Strength": "str", "Dexterity": "dex", "Constitution": "con", "Intelligence": "int", "Wisdom": "wis", "Charisma": "cha"}

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

// saveBonusFor is the modifier a creature adds to a saving throw. ok is false when it is unknown.
// Characters add their Proficiency Bonus for the saves of their class and lose 2 per Exhaustion level.
func saveBonusFor(lobbyID, targetID int, ability string) (bonus int, autoFail, disadvantage bool) {
	short := saveAbilityShort[ability]
	if short == "" && len(ability) >= 3 { // callers also pass "dex", "con" ... (before this, a short name scored the ability as 0: -5)
		short = strings.ToLower(ability[:3])
	}
	if targetID > 0 {
		var str, dex, con, intl, wis, cha, level int
		var class string
		if db.QueryRow(`SELECT str, dex, con, intl, wis, cha, level, COALESCE(class,'') FROM characters WHERE id = $1`, targetID).
			Scan(&str, &dex, &con, &intl, &wis, &cha, &level, &class) != nil {
			return 0, false, false
		}
		score := map[string]int{"str": str, "dex": dex, "con": con, "int": intl, "wis": wis, "cha": cha}[short]
		bonus = game.Modifier(score)
		if ok, _ := saveProficiency(targetID, class, level, short); ok { // class saves, Disciplined Survivor, Slippery Mind
			bonus += game.ProficiencyBonus(level)
		}
		bonus = wildShapeHigher(targetID, "save:"+short, bonus) // the beast's save modifier when it's higher
		// Ring/Cloak of Protection, Luckstone, Staff of Power; Exhaustion
		bonus += magicSaveBonus(targetID) + buffSaveBonus(targetID) - game.ExhaustionD20Penalty(exhaustionLevelOf(targetID))
		conds := getCharConditions(targetID)
		adj, _ := spellRollAdjust(lobbyID, targetID, "save:"+short) // Bless, Bane, Slow
		return bonus + adj, game.AutoFailsSave(conds, short), game.GetSaveDisadvantage(conds, 0, short)
	}
	// a monster: its ability modifier (its stat block has no separate saving throw bonuses here)
	var raw []byte
	var entries []map[string]interface{}
	if db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw) == nil {
		json.Unmarshal(raw, &entries)
		for _, e := range entries {
			if asInt(e["id"]) == targetID {
				if sb := monsterStatBlock(fmt.Sprint(e["monster_key"])); sb != nil {
					bonus = monsterSaveBonus(fmt.Sprint(e["monster_key"]), short, asInt(sb[short]))
				}
			}
		}
	}
	conds := []string{}
	for _, r := range loadConditionRecords(lobbyID, targetID) {
		conds = append(conds, r.Condition)
	}
	dis := game.GetSaveDisadvantage(conds, 0, short)
	if takeCombatEffect(lobbyID, targetID, "staggering", 0) {
		dis = true // Staggering Blow: Disadvantage on the next saving throw
	}
	adj, _ := spellRollAdjust(lobbyID, targetID, "save:"+short) // Bless, Bane, Slow
	return bonus + adj, game.AutoFailsSave(conds, short), dis
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
			bonus, autoFail, dis := saveBonusFor(campaignID, id, s.Ability)
			roll := game.RollDie(20)
			if dis {
				roll, _, _ = game.RollWithDisadvantage()
			}
			total := roll + bonus
			if !autoFail && total >= s.DC {
				ended = fmt.Sprintf("%s repeats the %s saving throw %s and succeeds (%d): the %s condition ends.", name, s.Ability, phase, total, rec.Condition)
			} else if id < 0 && legendaryResistance(campaignID, id, saveStakes{Condition: true}) {
				ended = fmt.Sprintf("%s repeats the %s saving throw %s: the %s condition ends (Legendary Resistance).", name, s.Ability, phase, rec.Condition)
			} else {
				log = append(log, fmt.Sprintf("%s repeats the %s saving throw %s and fails (%d): the %s condition continues.", name, s.Ability, phase, total, rec.Condition))
			}
		}
		if ended == "" && rec.DurationRounds > 0 && when == "end_of_turn" {
			if rec.DurationRounds <= 1 {
				ended = fmt.Sprintf("The %s condition on %s runs out.", rec.Condition, name)
			} else {
				db.Exec(`UPDATE condition_records SET duration_rounds = duration_rounds - 1 WHERE id = $1`, rec.ID)
			}
		}
		if ended != "" {
			deleteConditionRecord(rec.ID)
			log = append(log, ended)
		}
	}
	for _, line := range log {
		recordGMAction(db, campaignID, "condition", line, line)
	}
	return log
}
