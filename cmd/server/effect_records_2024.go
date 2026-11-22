package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// effect_records_2024.go: the effects a stat block puts on a creature that are not one of the rules conditions, as RECORDS in the one
// condition store (condition_owner_2024.go), each of a kind in the table below. The parser reads a kind once (saveEffect.readEffects,
// called by readRider), giveConditions writes it (saveEffect.giveEffects) and every rules owner that the effect changes reads the
// record: the attack roll (attackCtx.gather), the saving throw (saveSources), the ability check (checkCtx) and the damage roll
// (damageRollAdjust). A new kind is one row here plus the reader of the owner it changes; never a per-monster branch.
//
//	d20_minus:1d6  "Until the end of its next turn, the target rolls 1d6 whenever it makes an ability check or attack roll and
//	               subtracts the number rolled from the D20 Test" (SRD 5.2.1 Copper Dragon Giggling Magic): each ability check and attack
//	               roll the creature makes rolls the die and subtracts it. Ends with its time (rider_duration_2024.go).
//	weakened:1d6   "Disadvantage on Strength-based D20 Tests and subtracts 3 (1d6) from its damage rolls. It repeats the save at the end of
//	               each of its turns, ending the effect on itself on a success. After 1 minute, it succeeds automatically" (SRD 5.2.1 Gold
//	               Dragon Weakening Breath): Disadvantage on Strength attack rolls, checks and saves, the die off each damage roll. Ends with
//	               the repeated save (the record's EndSave) and its 10 rounds. "each creature that isn't currently affected by this breath"
//	               is a gate on the targets (affectedBySource).
const (
	recD20Minus = "d20_minus"
	recWeakened = "weakened"
)

// effectRecordKinds: every record kind of this file (the readers ask for them by name).
var effectRecordKinds = []string{recD20Minus, recWeakened}

var (
	d20MinusRe = regexp.MustCompile(`(?i)` + riderUntil + `, the target rolls (\d+d\d+) whenever it makes an ability check or attack roll and subtracts the number rolled from the D20 Test`)
	weakenedRe = regexp.MustCompile(`(?i)(?:the target )?has Disadvantage on Strength-based D20 Tests and subtracts \d+ \((\d+d\d+)\) from its damage rolls`)
	// notAffectedRe: the gate in a save line's header: a creature already holding the effect from this source is not a target.
	notAffectedRe = regexp.MustCompile(`(?i)each creature that isn['’]t currently affected by this breath`)
)

// effectRead is one effect record kind a text gives.
type effectRead struct {
	Record string
	Die    string
	End    riderEnd // the duration the text names (riderEndNone: the record's own save and time)
}

// readEffects reads the effect records of a Failure's text.
func (e *saveEffect) readEffects(text string) {
	if m := d20MinusRe.FindStringSubmatch(text); m != nil {
		e.Effects = append(e.Effects, effectRead{Record: recD20Minus, Die: m[3], End: riderEndOf(m[1], m[2])})
	}
	if m := weakenedRe.FindStringSubmatch(text); m != nil {
		e.Effects = append(e.Effects, effectRead{Record: recWeakened, Die: m[1]})
	}
}

// stripEffects removes what readEffects applies from a sentence, so the GM is not told to apply it again.
func stripEffects(s string) string {
	s = d20MinusRe.ReplaceAllString(s, "")
	s = weakenedRe.ReplaceAllString(s, "")
	return strings.NewReplacer("The target has", "", "the target has", "").Replace(s)
}

// giveEffects writes the effect records on the target. e.SourceID is the creature the text comes from.
func (e saveEffect) giveEffects(lobbyID, tid int, source string) []string {
	var parts []string
	for _, f := range e.Effects {
		switch f.Record {
		case recD20Minus:
			a, ok := f.End.markerAdd(lobbyID, tid, e.SourceID, recD20Minus+":"+f.Die, source, e.IsSpell)
			if !ok {
				parts = append(parts, riderNotApplied("rolls "+f.Die+" on each ability check and attack roll"))
				continue
			}
			addCondition(lobbyID, tid, a)
			parts = append(parts, fmt.Sprintf("rolls %s on each ability check and attack roll and subtracts it %s", f.Die, f.End.describe(lobbyID, e.SourceID)))
		case recWeakened:
			a := condAdd{Condition: recWeakened + ":" + f.Die, Source: source, SourceID: e.SourceID, Magical: e.IsSpell, Rounds: e.Rounds}
			if e.EndSave {
				a.EndSave = &conditionSave{Ability: e.Ability, DC: e.DC}
			}
			addCondition(lobbyID, tid, a)
			parts = append(parts, fmt.Sprintf("Disadvantage on Strength-based D20 Tests and -%s on its damage rolls", f.Die))
		}
	}
	return parts
}

// effectRecord: the creature's record of the kind (Detail is its die), ok false when it has none.
func effectRecord(lobbyID, who int, kind string) (conditionRecord, bool) {
	if lobbyID == 0 || who == 0 {
		return conditionRecord{}, false
	}
	for _, r := range loadConditionRecords(lobbyID, who) {
		if r.Condition == kind {
			return r, true
		}
	}
	return conditionRecord{}, false
}

// hasEffectRecord: the creature holds the kind.
func hasEffectRecord(lobbyID, who int, kind string) bool {
	_, ok := effectRecord(lobbyID, who, kind)
	return ok
}

// affectedBySource: the creature holds one of these records from the creature that is the source (the "isn't currently affected by
// this breath" gate on a save's targets).
func affectedBySource(lobbyID, who, sourceID int, kinds []string) bool {
	for _, r := range loadConditionRecords(lobbyID, who) {
		for _, k := range kinds {
			if r.Condition == k && r.SourceID == sourceID {
				return true
			}
		}
	}
	return false
}

// effectKindsIn: the kinds the text gives (for the gate above).
func effectKindsIn(e saveEffect) []string {
	var kinds []string
	for _, f := range e.Effects {
		kinds = append(kinds, f.Record)
	}
	return kinds
}

// d20MinusRoll: the die a creature with a d20_minus record rolls on this ability check or attack roll and subtracts from it (0: none).
func d20MinusRoll(lobbyID, who int) (int, string) {
	r, ok := effectRecord(lobbyID, who, recD20Minus)
	if !ok || r.Detail == "" {
		return 0, ""
	}
	n := game.RollDamage(r.Detail, false)
	return n, fmt.Sprintf("%s -%d", r.Source, n)
}

// weakenedOn: a Strength-based D20 Test of the creature has Disadvantage (a weakened record).
func weakenedOn(lobbyID, who int) bool { return hasEffectRecord(lobbyID, who, recWeakened) }

// damageRollAdjust is what is added to or taken off a creature's damage roll by an effect on it, asked by every route that rolls
// attack damage right after the roll: Enlarge's and Reduce's 1d4 on a weapon or Unarmed Strike hit (weapon), and a weakened record's
// die off each damage roll. The damage never goes below 0 (below 1 for Reduce, as it was). The note ends with a period.
func damageRollAdjust(lobbyID, attackerID, dmg int, weapon bool) (int, string) {
	total, note := 0, ""
	if weapon {
		if dx, dt := enlargeHit(lobbyID, attackerID, dmg); dx != 0 {
			total, note = total+dx, note+dt
		}
	}
	if r, ok := effectRecord(lobbyID, attackerID, recWeakened); ok && r.Detail != "" {
		n := minInt(game.RollDamage(r.Detail, false), maxInt(0, dmg+total))
		if n > 0 {
			total -= n
			note += fmt.Sprintf(" %s: -%d damage.", r.Source, n)
		}
	}
	return total, note
}

// monsterAttackIsStrength: a stat-block attack is Strength-based when its bonus is the creature's Strength modifier plus its
// Proficiency Bonus (Dexterity's when that matches instead); when both or neither match, a melee attack is.
func monsterAttackIsStrength(lobbyID, attackerID, bonus int, melee bool) bool {
	str, dex, pb, _, _ := creatureStats(lobbyID, attackerID)
	byStr, byDex := game.Modifier(str)+pb == bonus, game.Modifier(dex)+pb == bonus
	switch {
	case byStr && !byDex:
		return true
	case byDex && !byStr:
		return false
	}
	return melee
}
