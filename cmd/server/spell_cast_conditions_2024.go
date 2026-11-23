package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// spell_cast_conditions_2024.go: the condition a damaging spell gives with its damage, read from the spell's own text, for a character's cast at a monster
// and at a character alike (the tester's notes, Nov 23: Sunburst's Blinded, Contagion's Poisoned, Black Tentacles' Restrained and Ray of Sickness's
// Poisoned on a hit were never applied; a table named three spells, and Ray of Sickness, a spell attack, was listed as a save):
//
//	"On a hit, the target takes 2d8 Poison damage and has the Poisoned condition until the end of your next turn."      (Ray of Sickness)
//	"On a failed save, a creature takes 12d6 Radiant damage and has the Blinded condition for 1 minute."                (Sunburst)
//	"... must succeed on a Constitution saving throw or take 11d8 Necrotic damage and have the Poisoned condition."    (Contagion)
//	"On a failed save, it takes 3d6 Bludgeoning damage, and it has the Restrained condition until the spell ends."      (Black Tentacles)
//
// The duration is the text's: until the start or end of your next turn (the rider duration owner, riderEndOf with the caster as the source), a number of
// minutes or hours, or the spell's own (until the spell ends, for the duration: tied to the caster's Concentration when the spell has it). "makes another
// ... saving throw at the end of each of its turns" is the repeated save. What the text adds that no owner reads stays with the GM, in the cast's text.

var (
	castCondRe   = regexp.MustCompile(`(?i)\b(On a hit|On a failed save|or take)\b[^.]*?\bdamage,? and (?:it |the target )?(?:has|have) the (\w+) condition(?: (until the (start|end) of your next turn|for (\d+) (minutes?|hours?)|for the duration|until the spell ends))?`)
	castRepeatRe = regexp.MustCompile(`(?i)makes (?:another |a )?(\w+) saving throw at the end of each of its turns`)
	spellSpanRe  = regexp.MustCompile(`(?i)(\d+) (minute|minutes|hour|hours|day|days)`)
)

// castCondition: what a damaging spell's text says its damage comes with.
type castCondition struct {
	Condition string
	OnHit     bool     // a spell attack's hit (else a failed save)
	End       riderEnd // until the start or end of the caster's next turn
	Rounds    int      // a span: 1 minute = 10
	Spell     bool     // for the spell's duration (Concentration ends it when the spell has it)
	Repeat    string   // the ability of the save it repeats at the end of each of its turns
}

// castConditionOf reads it; ok is false for a spell whose damage comes with no condition.
func castConditionOf(spell SRDSpell) (castCondition, bool) {
	if spell.DamageDice == "" && len(spell.DamageParts) == 0 {
		return castCondition{}, false
	}
	m := castCondRe.FindStringSubmatch(spell.Description)
	if m == nil || strings.Contains(strings.ToLower(m[0]), " you take") { // the caster's own failed save (Contact Other Plane) is not the target's
		return castCondition{}, false
	}
	cc := castCondition{Condition: strings.ToLower(m[2]), OnHit: strings.EqualFold(m[1], "On a hit")}
	switch {
	case m[4] != "":
		cc.End = riderEndOf(m[4], "you") // the caster's next turn: the source creature
	case m[5] != "":
		n, _ := strconv.Atoi(m[5])
		cc.Rounds = durationRounds(n, m[6])
	default: // for the duration, until the spell ends, or no time: the spell's own
		cc.Spell = true
	}
	if r := castRepeatRe.FindStringSubmatch(spell.Description); r != nil {
		cc.Repeat = saveAbilityKey(r[1])
	}
	return cc, true
}

// spellSpanRounds is a spell's duration in rounds ("Concentration, up to 1 minute": 10; "7 days"), 0 when it has none to count.
func spellSpanRounds(spell SRDSpell) int {
	m := spellSpanRe.FindStringSubmatch(spell.Duration)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	if strings.HasPrefix(strings.ToLower(m[2]), "day") {
		return n * 14400
	}
	return durationRounds(n, m[2])
}

// applyCastCondition gives the target the condition of the spell's damage (after a hit, or a failed save) and says so ("" when the spell has none).
func applyCastCondition(lobbyID, casterID, targetID int, spell SRDSpell, saveDC int, hit bool) string {
	cc, ok := castConditionOf(spell)
	if !ok || cc.OnHit != hit {
		return ""
	}
	a := condAdd{Condition: cc.Condition, Source: spell.Name, SourceID: casterID, CreatedBy: casterID, Magical: true}
	when := ""
	switch {
	case cc.End != riderEndNone:
		add, ok := cc.End.markerAdd(lobbyID, targetID, casterID, cc.Condition, spell.Name, true)
		if !ok {
			return fmt.Sprintf(" [%s's %s condition: the GM applies it.]", spell.Name, titleCase(cc.Condition))
		}
		a, when = add, cc.End.describe(lobbyID, casterID)
	case cc.Rounds > 0:
		a.Rounds, when = cc.Rounds, fmt.Sprintf("for %d rounds", cc.Rounds)
	default:
		a.Rounds, when = spellSpanRounds(spell), "for the spell's duration"
		if spell.Concentration {
			a.Notes, when = "concentration", "while the caster keeps Concentration"
		}
	}
	if cc.Repeat != "" && saveDC > 0 {
		a.EndSave = &conditionSave{Ability: cc.Repeat, DC: saveDC}
	}
	res := addCondition(lobbyID, targetID, a)
	name := creatureName(lobbyID, targetID)
	if res.Immune != "" {
		return fmt.Sprintf(" %s is immune to the %s condition (%s).", name, titleCase(cc.Condition), res.Immune)
	}
	out := fmt.Sprintf(" %s has the %s condition %s.", name, titleCase(cc.Condition), when)
	if a.EndSave != nil {
		out += fmt.Sprintf(" It repeats the DC %d %s save at the end of each of its turns.", saveDC, strings.ToUpper(cc.Repeat))
	}
	return out
}

// A spell whose failed save takes away spellcasting ("On a failed save, the target takes 10d12 Psychic damage and can't cast spells or take the Magic
// action": Befuddlement). The effect is the "befuddled" record in the condition store; a creature that holds it can't cast (cannotCastSpells: a character's
// cast and a monster's, monsterCastBlocked); its repeated save every 30 days is the GM's, and Greater Restoration, Heal or Wish end it (endBefuddlementBySpell, called by endSpellConditions).
var castNoSpellsRe = regexp.MustCompile(`(?i)On a failed save, the target takes [^.]*damage and can.t cast spells or take the Magic action`)

const befuddledMarker = "befuddled"

// applyCastMarker gives the target the spell's marker after a failed save and says so ("" when the spell gives none).
func applyCastMarker(lobbyID, casterID, targetID int, spell SRDSpell) string {
	if !castNoSpellsRe.MatchString(spell.Description) {
		return ""
	}
	addCondition(lobbyID, targetID, condAdd{Condition: befuddledMarker, Source: spell.Name, SourceID: casterID, CreatedBy: casterID, Magical: true})
	return fmt.Sprintf(" %s can't cast spells or take the Magic action (%s: its save every 30 days is the GM's; Greater Restoration, Heal or Wish ends it).", creatureName(lobbyID, targetID), spell.Name)
}

// cannotCastSpells: the creature holds an effect that takes away its spellcasting.
func cannotCastSpells(lobbyID, id int) bool {
	recs, err := readConditionRecords(db, lobbyID, id)
	if err != nil {
		return false
	}
	for _, r := range recs {
		if r.Condition == befuddledMarker {
			return true
		}
	}
	return false
}

// befuddlementEnders: the spells Befuddlement's own text names ("The effect can also be ended by the Greater Restoration, Heal, or Wish spell").
var befuddlementEnders = map[string]bool{"greater-restoration": true, "heal": true, "wish": true}

// endBefuddlementBySpell ends the creature's befuddled record when spellKey is one that ends it; it returns what ended.
func endBefuddlementBySpell(lobbyID, id int, spellKey string) []string {
	if !befuddlementEnders[spellKey] {
		return nil
	}
	if len(removeConditionsWhere(lobbyID, id, func(r conditionRecord) bool { return r.Condition == befuddledMarker })) > 0 {
		return []string{"Befuddlement"}
	}
	return nil
}
