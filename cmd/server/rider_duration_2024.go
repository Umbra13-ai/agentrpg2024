package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The duration of a stat-block rider (a hit's or a failed save's Speed, Disadvantage, Advantage and Reaction riders) is read
// here and nowhere else, and applied the same way for every rider kind:
//
//	"until the end of its next turn", "of the target's next turn"  the end of the TARGET's next turn: a marker counted down at the
//	                                                                end of each of the target's turns (twice when the target is the
//	                                                                creature acting now, whose current turn is not its "next turn")
//	"until the start of the worg's next turn" / "before the start"  the start of the SOURCE's next turn (expireSourceTurnConditions,
//	                                                                or expireCombatEffectsOf for a rider used up by one roll)
//	"until the end of the ooze's next turn"                        the end of the SOURCE's next turn (expireSourceTurnConditions)
//
// "Until the start of its/the target's next turn" is not read: the text does not say whose turn the server would count. A rider
// with no duration the server can count stays with the GM, in the text it came from (never dropped, never guessed).
type riderEnd int

const (
	riderEndNone riderEnd = iota
	riderEndTargetTurnEnd
	riderEndSourceTurnStart
	riderEndSourceTurnEnd
)

// riderUntil is the duration clause, written once: it is part of every rider pattern that names its duration right after the
// effect (riderPattern), and riderEndRe finds it alone in a sentence.
const riderUntil = `(?:until|before) the (start|end) of (?:its|the ([^ .,;’']+(?: [^ .,;’']+)?)['’]s) next turn`

var riderEndRe = regexp.MustCompile(`(?i)` + riderUntil)

// riderPattern is a rider effect followed directly by its duration ("Disadvantage on attack rolls until the end of its next turn").
func riderPattern(effect string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)` + effect + `,? ` + riderUntil)
}

// riderEndOf reads the duration from the two submatches of riderUntil (the edge and the creature named, "" for "its").
func riderEndOf(edge, who string) riderEnd {
	target := who == "" || strings.EqualFold(who, "target") || strings.EqualFold(who, "creature")
	switch {
	case strings.EqualFold(edge, "end") && target:
		return riderEndTargetTurnEnd
	case strings.EqualFold(edge, "start") && !target:
		return riderEndSourceTurnStart
	case strings.EqualFold(edge, "end"):
		return riderEndSourceTurnEnd
	}
	return riderEndNone // the start of the target's own turn
}

// readRiderEnd is the first duration in text.
func readRiderEnd(text string) riderEnd {
	if m := riderEndRe.FindStringSubmatch(text); m != nil {
		return riderEndOf(m[1], m[2])
	}
	return riderEndNone
}

// riderEndFor is the duration of a rider written in sentence: the one in its own sentence, else the effect's ("This effect lasts until
// the end of its next turn").
func riderEndFor(sentence, text string) riderEnd {
	if end := readRiderEnd(sentence); end != riderEndNone {
		return end
	}
	return readRiderEnd(text)
}

// riderEndIn reads the duration a riderPattern match carries (its last two submatches).
func riderEndIn(m []string) riderEnd {
	if len(m) < 3 {
		return riderEndNone
	}
	return riderEndOf(m[len(m)-2], m[len(m)-1])
}

// sourceAnchored: the rider ends on the source creature's turn, so the source must be known to end it.
func (d riderEnd) sourceAnchored() bool {
	return d == riderEndSourceTurnStart || d == riderEndSourceTurnEnd
}

// markerAdd is the condition record of a marker rider ("speed_minus:5", "disadv_attacks", "disadv_save:int" ...) for its duration.
// ok is false when the server cannot end it: no duration, or a source-anchored one without its source creature.
func (d riderEnd) markerAdd(lobbyID, tid, sourceID int, cond, source string, magical bool) (condAdd, bool) {
	a := condAdd{Condition: cond, Source: source, SourceID: sourceID, Magical: magical, Replace: true}
	switch d {
	case riderEndTargetTurnEnd:
		a.Rounds = 1
		if currentTurnID(lobbyID) == tid {
			a.Rounds = 2 // the current turn is not its "next turn"
		}
	case riderEndSourceTurnStart:
		a.Notes = notesUntilStartOfSourceTurn
	case riderEndSourceTurnEnd:
		a.Notes = notesUntilEndOfSourceTurn // addCondition skips the source's current turn end
	default:
		return a, false
	}
	if d.sourceAnchored() && sourceID == 0 {
		return a, false
	}
	return a, true
}

// describe is the duration in the words of a reply.
func (d riderEnd) describe(lobbyID, sourceID int) string {
	switch d {
	case riderEndTargetTurnEnd:
		return "until the end of its next turn"
	case riderEndSourceTurnStart:
		return fmt.Sprintf("until the start of %s's next turn", creatureName(lobbyID, sourceID))
	case riderEndSourceTurnEnd:
		return fmt.Sprintf("until the end of %s's next turn", creatureName(lobbyID, sourceID))
	}
	return ""
}

// riderNotApplied is what the GM reads for a rider the server could not record (its source creature is not known).
func riderNotApplied(what string) string {
	return what + ": not applied, the creature whose turn ends it is not known (the GM rules)"
}
