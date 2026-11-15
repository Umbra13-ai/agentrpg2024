package game

import (
	"fmt"
	"strings"
)

// AttackRoll is one attack roll, described before the d20 is rolled. It is the only place the server decides how an attack
// roll is made and judged (SRD 5.2.1, "Attack Rolls", "Advantage and Disadvantage", "Critical Hits"):
//   - one or more sources of Advantage and none of Disadvantage: roll two d20s, keep the higher;
//   - one or more of Disadvantage and none of Advantage: keep the lower;
//   - both, in any numbers: they cancel, one d20;
//   - a natural 1 misses and a natural 20 hits, whatever the bonuses and the Armor Class;
//   - a hit is a Critical Hit on a natural 20, on a natural roll in the attacker's critical range (CritFrom), or always when
//     AutoCrit is set (the target is Paralyzed or Unconscious and the attacker is within 5 feet).
//
// Every attack in the server (a monster's, a character's, a spell's, a companion's, an item's) fills one of these and calls
// Roll. What gives Advantage or Disadvantage is gathered by the caller's side of the server in one place; this type only
// applies the rule, and Describe gives the one wording of the roll that every result line uses.
type AttackRoll struct {
	Bonus        int          // the attack bonus added to the d20
	AC           int          // the target's Armor Class for this attack
	Advantage    []string     // every source of Advantage, in words ("Faerie Fire", "the target is Prone")
	Disadvantage []string     // every source of Disadvantage ("the target is Dodging", "long range")
	Adjust       []RollAdjust // amounts added to the total besides the bonus, already rolled (Bless, Bane)
	CritFrom     int          // the lowest natural roll that makes a hit a Critical Hit; 0 means 20
	AutoCrit     bool         // a hit is a Critical Hit whatever the d20 shows
	AutoHit      bool         // no attack roll: the attack simply hits, and is never a Critical Hit ("Automatic hit")
}

// RollAdjust is an amount added to an attack roll's total, with the words shown for it ("Bless +3").
type RollAdjust struct {
	Amount int
	Why    string
}

// AttackOutcome is what an attack roll came to.
type AttackOutcome struct {
	Natural  int    // the d20 that counts
	Dice     [2]int // both d20s when two were rolled (Dice[1] is 0 for one d20)
	Mode     string // "advantage", "disadvantage", "cancelled" (both applied: one d20) or ""
	Total    int    // Natural + Bonus + every Adjust
	Hit      bool
	Critical bool
	AutoHit  bool // the attack hit without a roll
}

// Mode: how the d20 is rolled, from the sources gathered: "advantage", "disadvantage", "cancelled" or "".
func (a AttackRoll) Mode() string {
	switch {
	case len(a.Advantage) > 0 && len(a.Disadvantage) > 0:
		return "cancelled"
	case len(a.Advantage) > 0:
		return "advantage"
	case len(a.Disadvantage) > 0:
		return "disadvantage"
	}
	return ""
}

// HasAdvantage: the roll is made with Advantage (a source of it and no source of Disadvantage). Rules that depend on the
// attack roll having Advantage (a Rogue's Sneak Attack, "if the attack roll had Advantage" damage) ask this.
func (a AttackRoll) HasAdvantage() bool { return a.Mode() == "advantage" }

// HasDisadvantage: the roll is made with Disadvantage.
func (a AttackRoll) HasDisadvantage() bool { return a.Mode() == "disadvantage" }

// Roll rolls the d20 (or two) and judges the attack.
func (a AttackRoll) Roll() AttackOutcome {
	if a.AutoHit {
		return AttackOutcome{Hit: true, AutoHit: true, Total: a.total(0)}
	}
	var o AttackOutcome
	switch a.Mode() {
	case "advantage":
		o.Natural, o.Dice[0], o.Dice[1] = RollWithAdvantage()
	case "disadvantage":
		o.Natural, o.Dice[0], o.Dice[1] = RollWithDisadvantage()
	default:
		o.Natural = RollDie(20)
		o.Dice[0] = o.Natural
	}
	return a.judge(o)
}

// Judge gives the outcome for a d20 result that is already known: a reroll that replaces the die (Halfling Luck on a
// natural 1, a Luck Blade), or a test.
func (a AttackRoll) Judge(natural int) AttackOutcome {
	return a.judge(AttackOutcome{Natural: natural, Dice: [2]int{natural, 0}})
}

func (a AttackRoll) total(natural int) int {
	t := natural + a.Bonus
	for _, x := range a.Adjust {
		t += x.Amount
	}
	return t
}

func (a AttackRoll) judge(o AttackOutcome) AttackOutcome {
	o.Mode = a.Mode()
	o.Total = a.total(o.Natural)
	o.Hit = o.Natural != 1 && (o.Natural == 20 || o.Total >= a.AC)
	critFrom := a.CritFrom
	if critFrom <= 0 || critFrom > 20 {
		critFrom = 20
	}
	o.Critical = o.Hit && (o.Natural >= critFrom || a.AutoCrit)
	return o
}

// Describe gives the roll in words, the same in every result line, to follow the total:
//
//	" with Advantage (Faerie Fire)"
//	" with Disadvantage (the target is Dodging, long range)"
//	" (Advantage from Faerie Fire and Disadvantage from long range cancel: one d20)"
//
// and then each adjustment in square brackets (" [Bless +3]"). It is "" for a plain roll.
func (a AttackRoll) Describe() string {
	var s string
	switch a.Mode() {
	case "advantage":
		s = " with Advantage (" + strings.Join(a.Advantage, ", ") + ")"
	case "disadvantage":
		s = " with Disadvantage (" + strings.Join(a.Disadvantage, ", ") + ")"
	case "cancelled":
		s = " (Advantage from " + strings.Join(a.Advantage, ", ") + " and Disadvantage from " + strings.Join(a.Disadvantage, ", ") + " cancel: one d20)"
	}
	for _, x := range a.Adjust {
		if x.Why != "" {
			s += " [" + x.Why + "]"
		}
	}
	return s
}

// Label: "MISS", "HIT" or "CRITICAL HIT".
func (o AttackOutcome) Label() string {
	switch {
	case o.Critical:
		return "CRITICAL HIT"
	case o.Hit:
		return "HIT"
	}
	return "MISS"
}

// Line gives the attack's result line up to the outcome, the same for every attack:
//
//	"Scimitar: 17 with Disadvantage (the target is Dodging) vs AC 15 - HIT"
//	"Claw: automatic hit - HIT"
//
// The caller adds the damage and what follows. The pattern that hides the total and the Armor Class from players
// (cmd/server/player_text.go) is written for exactly this form.
func (a AttackRoll) Line(name string, o AttackOutcome) string {
	if o.AutoHit {
		return fmt.Sprintf("%s: automatic hit - %s", name, o.Label())
	}
	return fmt.Sprintf("%s: %d%s vs AC %d - %s", name, o.Total, a.Describe(), a.AC, o.Label())
}

// HeadForAttacker is the attacker's side of every attack result line, up to the outcome: the total, the d20 and what was added
// to it, why it had Advantage or Disadvantage, and each adjustment. It never shows the target's Armor Class (players aren't
// told it; the GM's line, Line, has it). Every character, companion, item and spell attack the attacker reads uses it.
//
//	"Attack with Longsword: 17 to hit (d20 12 + 5)"
//	"Attack with Longsword: 17 to hit (d20s 12 and 7: 12 + 5) with Advantage (Help) [Bless +2]"
//	"Claw: automatic hit"
func (a AttackRoll) HeadForAttacker(name string, o AttackOutcome) string {
	if o.AutoHit {
		return name + ": automatic hit"
	}
	return fmt.Sprintf("%s: %d to hit%s%s", name, o.Total, a.dice(o), a.Describe())
}

// dice: " (d20 12 + 5)" or, with two d20s, " (d20s 12 and 7: 12 + 5)". What is added is the total less the d20 and the
// adjustments in square brackets, so the numbers always add up to the total shown.
func (a AttackRoll) dice(o AttackOutcome) string {
	added := o.Total - o.Natural
	for _, x := range a.Adjust {
		added -= x.Amount
	}
	d := fmt.Sprintf("d20 %d", o.Natural)
	if o.Dice[1] != 0 {
		d = fmt.Sprintf("d20s %d and %d: %d", o.Dice[0], o.Dice[1], o.Natural)
	}
	switch {
	case added > 0:
		d += fmt.Sprintf(" + %d", added)
	case added < 0:
		d += fmt.Sprintf(" - %d", -added)
	}
	return " (" + d + ")"
}

// LineForAttacker gives the result line as the attacker's player reads it: HeadForAttacker and the outcome.
//
//	"Attack with Longsword: 17 to hit (d20 12 + 5) with Advantage (Help). HIT"
func (a AttackRoll) LineForAttacker(name string, o AttackOutcome) string {
	return a.HeadForAttacker(name, o) + ". " + o.Label()
}
