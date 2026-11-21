package main

// One owner for a creature's Speed in a fight (characterSpeed asks it; nothing else decides a Speed that effects change).
//
// What changes a Speed, and what the repository and the supplied rules settle.
//
// Base Speed is one calculation (characterBaseSpeed): the species' Speed (or the Wild Shape form's), Large Form, class bonuses,
// Longstrider, Boots, armor whose Strength requirement is not met, Exhaustion (SRD 5.2.1 glossary, Exhaustion: "Your Speed is reduced
// by 5 times your Exhaustion level"), a Fly Speed from a spell. That is the creature's Speed before an EFFECT of a fight changes it, and
// it is never a reason to refuse movement. Before this owner existed the Slow spell halved, and Haste doubled, the budget AFTER that
// base calculation (spellTurnStart); the owner keeps that order, so ordinary combinations (Slow with Exhaustion, Haste with
// Longstrider, a Fly Speed halved) are applied.
//
// The effects (applied to the base Speed):
//   - SETTLED by the glossary: "Speed 0. Your Speed is 0 and can't increase" (Grappled, Restrained, Paralyzed ...): 0 wins over a doubling
//     or a bonus. An effect that changes the Speed changes every special speed (Fly, Climb ...) by the same amount (glossary, Speed:
//     "Changes to Your Speeds"; effects are applied after the special speeds are known). Dash adds "your Speed after applying any
//     modifiers" (glossary, Dash; the Dash code reads the Speed now). One effect on its own (a flat reduction, a halving, a doubling)
//     applies as its text says; flat reductions from different effects each say "decreases by N" and are summed (one effect repeated,
//     such as a second Hamstring Blow, replaces itself, as that mastery's text says).
//   - A halving and a doubling, or a reduction with either, are applied in every order and compared (Speed.Ambiguous): when every
//     order gives the same Speed (Slow with Haste on an even Speed: 30 -> 15 -> 30) it is settled. When the orders give different
//     Speeds (a 10 ft reduction with a halving: (30-10)/2 = 10 but 30/2-10 = 5; Slow with Haste on 35 ft: 17*2 = 34 but 70/2 = 35) no text
//     supplied says which comes first, and two halvings (or two doublings) have no text saying whether the second one stacks. Those are
//     the only combinations left to the GM: the Speed is not computed, movement is refused with the effects named, and the GM rules with
//     POST /api/gm/conditions {"condition":"speed_ruling","speed_ft":N,"duration_rounds":R} (DELETE removes it).
//
// A character's movement budget is movement_remaining, set from the Speed when its turn starts. movement_speed_base is the Speed that
// budget was set from; settleMovement moves the budget by the difference when the Speed has changed since (an effect that arrives or
// ends in the middle of the turn). The budget may go below 0 internally (more already spent than the new Speed allows), so effects
// that come and go never give movement back that was spent; readers show max(0, budget).
//
// A monster's Speed is not a budget here (the GM moves its token); the markers are on its condition records and monsterSpeedNote tells
// the GM what applies, and whether a ruling is needed.

import (
	"fmt"
	"regexp"
	"strings"
)

// speedFactors is what changes the Speed of a creature (id > 0 a character, < 0 a monster or companion).
type speedFactors struct {
	Zero    bool
	Halves  []string // the sources that halve it
	Doubles []string // the sources that double it
	Minuses []int    // the flat reductions, one per source
	MinusBy []string
	Set     *int // the GM's ruling: the Speed
	Why     []string
}

func (f *speedFactors) note(why string) {
	for _, w := range f.Why {
		if w == why {
			return
		}
	}
	f.Why = append(f.Why, why)
}

func speedFactorsOf(lobbyID, id int) speedFactors {
	var f speedFactors
	for _, r := range loadConditionRecords(lobbyID, id) {
		switch r.Condition {
		case "grappled", "restrained":
			f.Zero = true
			f.note(titleCase(r.Condition) + ": Speed 0")
		case "speed_zero":
			f.Zero = true
			f.note("Speed 0" + speedSource(r))
		case "speed_halved":
			f.Halves = append(f.Halves, "speed_halved")
			f.note("Speed halved" + speedSource(r))
		case "speed_minus":
			var n int
			fmt.Sscanf(r.Detail, "%d", &n)
			f.Minuses, f.MinusBy = append(f.Minuses, n), append(f.MinusBy, "speed_minus")
			f.note(fmt.Sprintf("Speed -%d ft", n) + speedSource(r))
		case "speed_ruling":
			var n int
			fmt.Sscanf(r.Detail, "%d", &n)
			f.Set = &n
			f.note(fmt.Sprintf("the GM ruled the Speed at %d ft", n))
		}
	}
	for _, e := range spellEffectsOn(lobbyID, id) {
		switch e.Kind {
		case "slowed":
			if id > 0 && buffIgnoresDifficult(id) { // magic can't reduce its Speed
				continue
			}
			f.Halves = append(f.Halves, "slow")
			f.note("Slow: Speed halved")
		case "hasted":
			f.Doubles = append(f.Doubles, "haste")
			f.note("Haste: Speed doubled")
		case "lethargic":
			f.Zero = true
			f.note("Haste's lethargy: Speed 0")
		}
	}
	for _, e := range combatEffectsOn(lobbyID, id) {
		switch e.Kind {
		case "slow", "chilled":
			f.Minuses, f.MinusBy = append(f.Minuses, 10), append(f.MinusBy, e.Kind)
			f.note("Speed -10 ft (" + e.Kind + ")")
		case "hamstring":
			f.Minuses, f.MinusBy = append(f.Minuses, 15), append(f.MinusBy, "hamstring")
			f.note("Speed -15 ft (Hamstring Blow)")
		case "halved":
			f.Halves = append(f.Halves, "halved")
			f.note("Speed halved")
		}
	}
	// An attach the creature is the SOURCE of pins it in place: "Its Speed becomes 0, it can't benefit from
	// any bonus to its Speed, and it moves with the target" (Darkmantle Crush; the stirge's Proboscis reads
	// the same). Speed 0 can't increase (the settled 0-wins order), so a Zero factor carries the no-bonus
	// rule exactly. The target is NOT Grappled by an attach: the attach text owns no such condition (the
	// Darkmantle's cover owns Blinded only), so the target keeps its Speed and carries the attacher when
	// it moves (the carry coupling, monster_move_2024.go).
	var attachedTo int
	if err := monsterQueryRow(`SELECT target_id FROM combat_effects WHERE lobby_id=$1 AND source_id=$2 AND kind='attached'`, lobbyID, id).Scan(&attachedTo); err == nil {
		f.Zero = true
		f.note("attached to " + creatureName(lobbyID, attachedTo) + ": Speed 0, no bonus to Speed")
	}
	return f
}

func speedSource(r conditionRecord) string {
	if r.Source == "" {
		return ""
	}
	return " (" + r.Source + ")"
}

// Ambiguous: effects meet whose result depends on an order or on stacking that no text supplied settles (see the top of this file).
func (f speedFactors) Ambiguous() bool {
	if f.Set != nil || f.Zero { // a ruling decides; 0 is settled
		return false
	}
	if len(f.Halves) >= 2 || len(f.Doubles) >= 2 {
		return true // does the second one stack? not said
	}
	// the operations in every order: the same result for every base Speed is not required, only for the Speed asked about
	return false
}

// results of the three operations in every order; settled when they all agree.
func (f speedFactors) orders(speed int) (int, bool) {
	flat := 0
	for _, n := range f.Minuses {
		flat += n
	}
	ops := []func(int) int{
		func(s int) int { return max(s-flat, 0) },
		func(s int) int {
			if len(f.Halves) > 0 {
				return s / 2
			}
			return s
		},
		func(s int) int {
			if len(f.Doubles) > 0 {
				return s * 2
			}
			return s
		},
	}
	first, agree := 0, true
	for n, p := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		v := speed
		for _, k := range p {
			v = ops[k](v)
		}
		if n == 0 {
			first = v
		} else if v != first {
			agree = false
		}
	}
	return first, agree
}

// apply is the Speed after the effects when they are settled; ok is false when they are not (the Speed is unchanged then).
func (f speedFactors) apply(speed int) (int, bool) {
	switch {
	case f.Set != nil:
		return max(*f.Set, 0), true
	case f.Zero:
		return 0, true
	case len(f.Halves) >= 2 || len(f.Doubles) >= 2:
		return speed, false
	}
	v, agree := f.orders(speed)
	if !agree {
		return speed, false
	}
	return v, true
}

// ambiguousNote says which effects need the GM's ruling.
func (f speedFactors) ambiguousNote() string {
	return "The Speed effects on this creature meet in a way the rules supplied do not settle (" + strings.Join(f.Why, "; ") +
		"): the GM rules the Speed with POST /api/gm/conditions {\"condition\": \"speed_ruling\", \"target_id\": ID, \"speed_ft\": N, \"duration_rounds\": R}."
}

// effectiveSpeed is a creature's Speed with its effects, what changed it, and whether the GM must rule (ruling: the Speed is unchanged).
func effectiveSpeed(lobbyID, id, speed int) (out int, why []string, ruling bool) {
	if lobbyID == 0 || id == 0 || db == nil {
		return speed, nil, false
	}
	f := speedFactorsOf(lobbyID, id)
	out, ok := f.apply(speed)
	return out, f.Why, !ok
}

// speedRulingNeeded: the character's Speed effects need the GM's ruling (movement is refused until it is given).
func speedRulingNeeded(charID int) string {
	lobby := lobbyOfChar(charID)
	if lobby == 0 {
		return ""
	}
	f := speedFactorsOf(lobby, charID)
	var species string
	db.QueryRow(`SELECT COALESCE(species,'') FROM characters WHERE id = $1`, charID).Scan(&species)
	if _, ok := f.apply(characterBaseSpeed(charID, species)); !ok {
		return f.ambiguousNote()
	}
	return ""
}

// settleMovement moves a character's movement budget by the change in its Speed since the budget was set (an effect that arrived or
// ended in the middle of the turn). The budget may go below 0: what was spent stays spent when an effect comes and goes.
func settleMovement(charID int) {
	if db == nil || charID <= 0 {
		return
	}
	now := characterSpeedOf(charID)
	db.Exec(`UPDATE characters SET movement_remaining = COALESCE(movement_remaining,0) + $1 - movement_speed_base, movement_speed_base = $1
		WHERE id = $2 AND movement_speed_base IS NOT NULL AND movement_speed_base <> $1`, now, charID)
}

// setMovementBudget sets the movement budget of a new turn from the Speed and remembers the Speed it came from.
func setMovementBudget(charID, speed int) {
	db.Exec(`UPDATE characters SET movement_remaining = $1, movement_speed_base = $1 WHERE id = $2`, speed, charID)
}

// monsterSpeedNote tells the GM a monster's Speed effects ("" when none): the GM moves monster tokens, so the server says what applies.
func monsterSpeedNote(lobbyID, id int) string {
	f := speedFactorsOf(lobbyID, id)
	if len(f.Why) == 0 {
		return ""
	}
	note := strings.Join(f.Why, "; ")
	if f.Ambiguous() || (len(f.Minuses) > 0 && len(f.Halves)+len(f.Doubles) > 0) { // the Speed is the stat block's: the order of a reduction and a multiplier matters
		note += ". The combination may need the GM's ruling (a reduction with a halving or doubling, or a second halving): " + f.ambiguousNote()
	}
	return note
}

var (
	speedHalvedRe = regexp.MustCompile(`(?i)\bSpeed (?:is|becomes) halved`)
	speedZeroRe   = regexp.MustCompile(`(?i)\bSpeed (?:is|becomes) (?:reduced to )?0\b`)
	speedMinusRe  = regexp.MustCompile(`(?i)\bSpeed (?:decreases|is reduced|is decreased) by (\d+) f(?:ee|oo)t`)
)

// readSpeed reads a Speed rider out of a failure or hit text with its duration (riderEndFor, rider_duration_2024.go): the one in the
// rider's own sentence ("its Speed decreases by 5 feet until the start of the ghoul's next turn"), else the effect's ("This effect
// lasts until the end of its next turn"). A Speed effect without a duration the server can count stays with the GM, in the text it
// came from.
func (e *saveEffect) readSpeed(text string) {
	for _, sentence := range sentenceSplitRe.Split(text, -1) {
		mode, minus := "", 0
		switch {
		case speedZeroRe.MatchString(sentence):
			mode = "zero"
		case speedHalvedRe.MatchString(sentence):
			mode = "halved"
		default:
			if m := speedMinusRe.FindStringSubmatch(sentence); m != nil {
				mode = "minus"
				fmt.Sscanf(m[1], "%d", &minus)
			}
		}
		if mode == "" {
			continue
		}
		if end := riderEndFor(sentence, text); end != riderEndNone {
			e.SpeedMode, e.SpeedMinus, e.SpeedEnd = mode, minus, end
		}
		return // the first Speed rider decides; one without a duration is the GM's
	}
}

// stripSpeedRider removes the Speed effect and its duration from a sentence (what is left is not applied).
func stripSpeedRider(s string) string {
	s = riderEndRe.ReplaceAllString(s, "")
	for _, re := range []*regexp.Regexp{speedZeroRe, speedHalvedRe, speedMinusRe} {
		s = re.ReplaceAllString(s, "")
	}
	return strings.NewReplacer("the target’s", "", "the target's", "", "target’s", "", "target's", "").Replace(s)
}

// giveSpeed puts the Speed rider on the target as a marker for the rider's duration (riderEnd.markerAdd: the end of the target's
// next turn, or the start or end of the source's next turn).
func (e saveEffect) giveSpeed(lobbyID, tid int, source string) string {
	cond, what := "speed_halved", "Speed halved"
	switch e.SpeedMode {
	case "zero":
		cond, what = "speed_zero", "Speed 0"
	case "minus":
		cond, what = fmt.Sprintf("speed_minus:%d", e.SpeedMinus), fmt.Sprintf("Speed -%d ft", e.SpeedMinus)
	}
	a, ok := e.SpeedEnd.markerAdd(lobbyID, tid, e.SourceID, cond, source, e.IsSpell)
	if !ok {
		return riderNotApplied(what)
	}
	addCondition(lobbyID, tid, a)
	return what + " " + e.SpeedEnd.describe(lobbyID, e.SourceID)
}

// speedNoteFor is what a player reads beside its movement: the effects on its Speed, and that the GM must rule when they meet in a
// way the rules supplied do not settle ("" when nothing changes its Speed).
func speedNoteFor(lobbyID, charID int) string {
	f := speedFactorsOf(lobbyID, charID)
	if len(f.Why) == 0 {
		return ""
	}
	note := " Speed effects: " + strings.Join(f.Why, "; ") + "."
	if why := speedRulingNeeded(charID); why != "" {
		note += " Movement waits for the GM: these effects are not combined by the rules supplied."
	}
	return note
}
