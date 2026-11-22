package main

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/agentrpg/agentrpg/game"
)

// wound_records_2024.go: a wound a stat block gives ("The target receives an infernal wound. While wounded, the target loses 5 (1d10) Hit
// Points at the start of each of its turns. The wound closes after 1 minute, after a spell restores Hit Points to the target, or after the
// target or a creature within 5 feet of it takes an action to stanch the wound, doing so by succeeding on a DC 12 Wisdom (Medicine)
// check": SRD 5.2.1 Bearded Devil, Horned Devil) is a `wounded` record of the one condition store: its Detail is the Hit Points lost each
// turn, its time the minute, its way to end it the Medicine check (conditionEscape, Adjacent: POST /api/conditions/escape with target_id
// for a creature within 5 feet), and a spell that restores Hit Points ends it (endWoundsBySpell). The Hit Points are LOST at the start of
// each of its turns (woundTurnStart), never damage: no resistance. "doesn't already have an infernal wound" / "if it doesn't have one" is
// a gate on the target, asked once by applySaveEffect (saveEffect.NotHolding), so a wounded creature rolls no save.
var (
	woundGiveRe  = regexp.MustCompile(`(?i)the target receives an? [a-z]+ wound(?: if it doesn['’]t have one)?`)
	woundLossRe  = regexp.MustCompile(`(?i)While wounded, the target loses \d+ \((\d+d\d+)\) Hit Points at the start of each of its turns`)
	woundCloseRe = regexp.MustCompile(`(?i)The wound closes after (\d+) minutes?, after a spell restores Hit Points to the target, or after the target or a creature within 5 feet of it takes an action to stanch the wound, doing so by succeeding on a DC (\d+) Wisdom \(Medicine\) check`)
	// woundGateRe: the whole effect is only for a creature without the wound ("If the target is a creature and doesn't already have an infernal wound, it is subjected to the following effect": Bearded Devil).
	woundGateRe = regexp.MustCompile(`(?i)doesn['’]t already have an? [a-z]+ wound, it is subjected to the following effect`)
	// woundIfNoneRe: only the wound is conditional; the rest of the effect applies ("Necrotic damage, and the target receives an infernal wound if it doesn't have one": Horned Devil).
	woundIfNoneRe = regexp.MustCompile(`(?i)receives an? [a-z]+ wound if it doesn['’]t have one`)
)

// woundRead is a text's wound.
type woundRead struct {
	Dice   string // the Hit Points lost each turn
	Rounds int    // 1 minute is 10 rounds (0: until a spell or the check ends it)
	DC     int    // the Wisdom (Medicine) check that stanches it (0: none)
	IfNone bool   // given only to a creature that has no wound (the rest of the effect applies to every target)
}

// readWound reads the wound of a Failure's text (the give, the loss each turn and, when it is there, how it closes).
func (e *saveEffect) readWound(text string) {
	if !woundGiveRe.MatchString(text) {
		return
	}
	l := woundLossRe.FindStringSubmatch(text)
	if l == nil {
		return
	}
	w := &woundRead{Dice: game.NormalizeDice(l[1]), IfNone: woundIfNoneRe.MatchString(text)}
	if c := woundCloseRe.FindStringSubmatch(text); c != nil {
		n, _ := strconv.Atoi(c[1])
		w.Rounds = n * 10
		w.DC, _ = strconv.Atoi(c[2])
	}
	e.Wound = w
}

// stripWound removes what readWound applies from a sentence.
func stripWound(s string) string {
	for _, re := range []*regexp.Regexp{woundGiveRe, woundLossRe, woundCloseRe, woundIfNoneRe} {
		s = re.ReplaceAllString(s, "")
	}
	return s
}

// giveWound puts the wound on the target (one wound: a creature that holds it already is not given another).
func (e saveEffect) giveWound(lobbyID, tid int, source string) []string {
	if e.Wound == nil {
		return nil
	}
	if e.Wound.IfNone && holdsWound(lobbyID, tid) { // "if it doesn't have one": the damage was dealt; no second wound
		return []string{"already wounded: no second wound"}
	}
	a := condAdd{Condition: woundedConditions + ":" + e.Wound.Dice, Source: source, SourceID: e.SourceID, Magical: e.IsSpell, Rounds: e.Wound.Rounds}
	if e.Wound.DC > 0 {
		yes := true
		a.Escapes = []conditionEscape{{Label: fmt.Sprintf("stanch the wound (Wisdom (Medicine) DC %d)", e.Wound.DC), Ability: "Wisdom", Skill: "Medicine", DC: e.Wound.DC, UsesAction: &yes, Adjacent: true}}
	}
	if res := addCondition(lobbyID, tid, a); res.Err != nil {
		return []string{riderNotApplied("wounded")}
	}
	return []string{fmt.Sprintf("wounded: loses %s Hit Points at the start of each of its turns", e.Wound.Dice)}
}

// woundTurnStart: at the start of creature id's turn each wound it has costs it its Hit Points (stage 7 of the turn pipeline).
func woundTurnStart(lobbyID, id int, name string) []string {
	var out []string
	for _, r := range loadConditionRecords(lobbyID, id) {
		if r.Condition != woundedConditions || r.Detail == "" {
			continue
		}
		n := game.RollDamage(r.Detail, false)
		switch {
		case id < 0:
			if _, err := loseMonsterHP(lobbyID, id, n, ""); err != nil {
				out = append(out, fmt.Sprintf("%s's wound could not be recorded (%v): the GM applies %d Hit Points lost", name, err, n))
				continue
			}
		case isCompanionCombatID(id):
			if c, active := activeCompanionInCampaign(lobbyID, id); active {
				damageCompanion(c, n, "")
			}
		default:
			if _, err := damageHP(id, hpHit{Parts: []hpPart{{Amount: n}}, Raw: true}); err != nil {
				out = append(out, fmt.Sprintf("%s's wound could not be recorded: the GM applies %d Hit Points lost", name, n))
				continue
			}
		}
		out = append(out, fmt.Sprintf("%s loses %d Hit Points to the wound (%s)", name, n, r.Source))
	}
	return out
}

// endWoundsBySpell: a spell that restores Hit Points to the creature closes its wounds ("after a spell restores Hit Points to the target").
func endWoundsBySpell(lobbyID, id int, spellKey string) []string {
	sp, ok := srdSpellsMemory[normalizeSpellSlug(spellKey)]
	if !ok || sp.Healing == "" { // a spell that restores Hit Points: its healing dice are in the spell's data
		return nil
	}
	if len(removeConditionsWhere(lobbyID, id, func(r conditionRecord) bool { return r.Condition == woundedConditions })) > 0 {
		return []string{"Wounded"}
	}
	return nil
}

// holdsWound: the creature has a wound.
func holdsWound(lobbyID, id int) bool {
	for _, r := range loadConditionRecords(lobbyID, id) {
		if r.Condition == woundedConditions {
			return true
		}
	}
	return false
}
