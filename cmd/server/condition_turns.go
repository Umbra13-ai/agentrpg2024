package main

// What happens to conditions when a creature's turn ends (SRD 5.2.1: effects that last a number of
// rounds, and "repeat the save at the end of each of its turns"). Called from every place that moves
// combat to the next turn.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// groupSave is the repeated saving throw of one cast, shared by every condition that cast gave (see the turn pipeline's stage 5).
type groupSave struct {
	sv game.SaveResult
	lr bool // a monster's Legendary Resistance turned the failed save
}

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

// endTurnEffects runs at the end of a turn. endingIndex is the position in the turn order of the creature
// whose turn is ending, or -1 for the current turn stored in the database. It returns what happened.
func endTurnEffects(campaignID, endingIndex int) turnEffectLines {
	return turnEffects(campaignID, endingIndex, "end_of_turn")
}

// startTurnEffects runs after combat has moved to the next creature: saves that repeat at the start of its turn.
func startTurnEffects(campaignID int) turnEffectLines {
	return turnEffects(campaignID, -1, "start_of_turn")
}

// turnEffectLines is what a turn change did, split by who may read it. Public lines are written to the action log the players read.
// GM lines are never written there: only a GM-gated turn reply returns them (turnChange.GMNotes, gm_notices). The split is the type,
// not wording: a line is private because it is in GM, whatever it says.
type turnEffectLines struct {
	Public []string
	GM     []string
	Paused bool
	Err    error
}

type turnEffectsCursor struct {
	ID             int
	Name, When     string
	Stage          int
	Conditions     []int
	ConditionIndex int
}

func init() {
	combatResumeHandlers["turn-effects"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var c turnEffectsCursor
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		result := runTurnEffects(lobby, c)
		// The outer turn frame was saved before this resumed hook returned. Its
		// reply cannot carry these newly produced private lines, so deliver them
		// through the GM notice owner rather than losing or publicly logging them.
		for _, note := range result.GM {
			addGMNotice(lobby, note)
		}
		return map[string]interface{}{"turn_effects": result.Public, "gm_notices": result.GM, "combat_paused": result.Paused}, result.Err
	}
}
func turnEffects(campaignID, endingIndex int, when string) turnEffectLines {
	var raw []byte
	var cur int
	if db == nil || db.QueryRow(`SELECT turn_order,current_turn_index FROM combat_state WHERE lobby_id=$1 AND active=true`, campaignID).Scan(&raw, &cur) != nil {
		return turnEffectLines{}
	}
	if endingIndex < 0 {
		endingIndex = cur
	}
	var entries []map[string]interface{}
	if json.Unmarshal(raw, &entries) != nil || endingIndex < 0 || endingIndex >= len(entries) {
		return turnEffectLines{}
	}
	e := entries[endingIndex]
	result := runTurnEffects(campaignID, turnEffectsCursor{ID: asInt(e["id"]), Name: fmt.Sprint(e["name"]), When: when})
	for _, note := range result.GM {
		addGMNotice(campaignID, note)
	}
	return result
}
func runTurnEffects(campaignID int, c turnEffectsCursor) turnEffectLines {
	var result turnEffectLines
	if combatResolutionPaused(campaignID) {
		result.Paused = true
		return result
	}
	f, err := readFight(db, campaignID)
	if err != nil {
		result.Err = err
		return result
	}
	if _, e := f.entry(c.ID); e == nil {
		result.Err = errors.New("turn-effect creature is no longer in this fight")
		return result
	}
	emit := func(lines ...string) {
		for _, line := range lines {
			public, private := splitGMNotes(line)
			result.GM = append(result.GM, private...)
			if public != "" {
				result.Public = append(result.Public, public)
				recordGMAction(db, campaignID, "condition", public, public)
			}
		}
	}
	id, name, when := c.ID, c.Name, c.When
	for c.Stage < 8 {
		next := c
		next.Stage++
		restore, err := armCombatFrame(campaignID, "turn-effects", next)
		if err != nil {
			result.Err = err
			return result
		}
		switch c.Stage {
		case 0:
			if when == "end_of_turn" && id > 0 {
				vexTurnEnded(id)
				innateSorceryTick(id)
				superiorDefenseTick(id)
			}
			if when == "start_of_turn" {
				expireCombatEffectsOf(campaignID, id)
				emit(attachSweep(campaignID)...)        // a dead creature has no turn: the first turn start ends its attaches
				emit(drainAttachEndings(campaignID)...) // crash backstop: any ending a committed detach recorded but never ran
				emit(swallowSweep(campaignID)...)       // the same for a dead swallower: its Restrained ends, the corpse escape is the GM's
				swallowArm(campaignID, id)              // a swallow landed this turn arms its schedule at the swallower's next turn start
			}
			if when == "end_of_turn" {
				db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND target_id=$2 AND source_id=$3`, campaignID, id, gmRiderSource)
			}
		case 1:
			if when == "start_of_turn" {
				emit(monsterFeaturesTurnStart(campaignID, id, name)...)
			} else {
				emit(monsterFeaturesTurnEnd(campaignID, id, name)...)
			}
		case 2:
			if when == "start_of_turn" {
				emit(burningTurnStart(campaignID, id, name))
			}
			if when == "end_of_turn" {
				emit(suffocationTurnEnd(campaignID, id, name)...) // suffocating: 1 Exhaustion at the end of each of its turns
				emit(swallowTurnEnd(campaignID, id, name)...)     // an armed swallow deals its damage; a survivor is disgorged, exiting Prone
			}
		case 3:
			if when == "start_of_turn" {
				emit(spellEffectsTurnStart(campaignID, id, name)...)
			} else {
				emit(spellEffectsTurnEnd(campaignID, id, name)...)
			}
		case 4:
			if when == "end_of_turn" && id > 0 {
				emit(rageTick(id))
			}
			if when == "end_of_turn" && id < 0 {
				emit(spiritGuardiansTurnEnd(campaignID, id)...)
			}
		case 5:
			groupSaves := map[string]*groupSave{}
			if c.Conditions == nil {
				for _, r := range loadConditionRecords(campaignID, id) {
					c.Conditions = append(c.Conditions, r.ID)
				}
			}
			for c.ConditionIndex < len(c.Conditions) {
				var rec *conditionRecord
				for _, r := range loadConditionRecords(campaignID, id) {
					if r.ID == c.Conditions[c.ConditionIndex] {
						copy := r
						rec = &copy
						break
					}
				}
				c.ConditionIndex++
				if rec == nil {
					continue
				}
				ended := ""
				if svSpec := rec.EndSave; svSpec != nil && (svSpec.When == when || (svSpec.When == "" && when == "end_of_turn")) {
					phase := "at the end of its turn"
					if when == "start_of_turn" {
						phase = "at the start of its turn"
					}
					// one repeated save for the conditions one cast gave (Hideous Laughter's Prone and Incapacitated: "makes another Wisdom saving throw ... On a
					// successful save, the spell ends"): the first record rolls it, the others of the same source and save take its result (FUNC-08)
					key := fmt.Sprintf("%s|%d|%s|%d|%s", rec.Source, rec.SourceID, svSpec.Ability, svSpec.DC, svSpec.When)
					grp, shared := groupSaves[key]
					if !shared {
						g := &groupSave{sv: (&saveCtx{lobby: campaignID, saver: id, ability: svSpec.Ability, dc: svSpec.DC, against: []string{rec.Condition}}).roll()}
						g.lr = !g.sv.Success && id < 0 && legendaryResistance(campaignID, id, saveStakes{Condition: true})
						grp = g
						groupSaves[key] = grp
					}
					sv := grp.sv
					switch {
					case sv.Success && shared:
						ended = fmt.Sprintf("The %s condition ends with the same saving throw.", rec.Condition)
					case sv.Success:
						ended = fmt.Sprintf("%s repeats the %s saving throw %s and succeeds%s: the %s condition ends.", name, svSpec.Ability, phase, sv.Tail(), rec.Condition)
					case grp.lr && shared:
						ended = fmt.Sprintf("The %s condition ends (Legendary Resistance on the same saving throw).", rec.Condition)
					case grp.lr:
						ended = fmt.Sprintf("%s repeats the %s saving throw %s: the %s condition ends (Legendary Resistance).", name, svSpec.Ability, phase, rec.Condition)
					case svSpec.Then != "": // a second stage (Sleep: the Unconscious condition for the rest of the spell)
						deleteConditionRecord(campaignID, id, rec.ID)
						notes := rec.Notes
						if svSpec.ThenEndsOnDamage && notes == "" {
							notes = notesEndsOnDamage
						}
						var again *conditionSave
						if svSpec.ThenRepeats { // the second condition keeps the repeated save (Paralyzing Breath), with no further stage
							again = &conditionSave{Ability: svSpec.Ability, DC: svSpec.DC, When: svSpec.When}
						}
						addCondition(campaignID, id, condAdd{Condition: svSpec.Then, Source: rec.Source, Rounds: svSpec.ThenRounds, SourceID: rec.SourceID,
							Notes: notes, CreatedBy: rec.CreatedBy, Magical: true, MagicalSleep: svSpec.ThenMagicalSleep, EndSave: again})
						emit(fmt.Sprintf("%s repeats the %s saving throw %s and fails%s: the %s condition becomes %s.", name, svSpec.Ability, phase, sv.Tail(), rec.Condition, titleCase(svSpec.Then)))
						continue
					case shared:
						emit(fmt.Sprintf("The %s condition continues.", rec.Condition))
					default:
						emit(fmt.Sprintf("%s repeats the %s saving throw %s and fails%s: the %s condition continues.", name, svSpec.Ability, phase, sv.Tail(), rec.Condition))
					}
				}
				if ended == "" && rec.DurationRounds > 0 && when == "end_of_turn" {
					if countDownCondition(campaignID, id, rec.ID) {
						emit(fmt.Sprintf("The %s condition on %s runs out.", rec.Condition, name))
					}
					continue
				}
				if ended != "" {
					deleteConditionRecord(campaignID, id, rec.ID)
					emit(ended)
				}
			}
		case 6:
			if when == "start_of_turn" {
				result.GM = append(result.GM, conditionOutlastsSourceNotices(campaignID, id, name)...)
			}
		case 7:
			if when == "start_of_turn" {
				emit(recurringDamageTurnStart(campaignID, id, name)...)
				emit(woundTurnStart(campaignID, id, name)...) // a wound's Hit Points lost (wound_records_2024.go)
			}
		}
		restore()
		c = next
		if combatResolutionPaused(campaignID) {
			result.Paused = true
			return result
		}
	}
	return result
}
