package game

import (
	"fmt"
	"strings"
)

// SavingThrow is one saving throw, described before the d20 is rolled. It is the only place the server decides how a saving
// throw is made and judged (SRD 5.2.1, "Saving Throws", "Advantage and Disadvantage", "D20 Tests"):
//   - one or more sources of Advantage and none of Disadvantage: roll two d20s, keep the higher;
//   - one or more of Disadvantage and none of Advantage: keep the lower;
//   - both, in any numbers: they cancel, one d20;
//   - the total is the d20, the saving throw modifier (Bonus) and every adjustment (Bless, Bane, Aura of Protection, a Ring
//     of Protection, Exhaustion ...); the save succeeds when the total equals or exceeds the DC;
//   - a natural 1 or 20 means nothing on a saving throw: only an attack roll reads them. A Death Saving Throw (Ability "death") is
//     judged against DC 10 like any save, and its caller reads the natural roll for what the SRD gives it (a 20 regains 1 Hit
//     Point, a 1 counts as two failures);
//   - AutoFail (a Paralyzed creature's Strength save) fails without a roll; AutoSucceed (Sculpt Spells) succeeds without one.
//     When both apply AutoSucceed wins: the feature that says so names this very save, the condition speaks of saves in general.
//
// Every saving throw in the server (a character's, a monster's, a death save) fills one of these and calls Roll. What gives
// Advantage, Disadvantage, an automatic failure or an adjustment is gathered by the server in one place (saveSources,
// cmd/server/save_2024.go); this type only applies the rule, and SaveResult gives the wordings every result line uses.
type SavingThrow struct {
	Ability      string       // "str" ... "cha"; DeathSave for a Death Saving Throw
	DC           int          // the Difficulty Class (10 for a Death Saving Throw)
	Bonus        int          // the saving throw modifier: the ability modifier, the Proficiency Bonus where proficient
	Advantage    []string     // every source of Advantage, in words ("Danger Sense", "Haste")
	Disadvantage []string     // every source of Disadvantage ("Restrained", "the Fool card")
	Adjust       []RollAdjust // amounts added to the total besides the bonus, already rolled ("Bless +3", "Exhaustion -2")
	AutoFail     string       // why the save fails without a roll ("Paralyzed"); "" when it doesn't
	AutoSucceed  string       // why the save succeeds without a roll ("Sculpt Spells"); "" when it doesn't
}

// SaveResult is what a saving throw came to.
type SaveResult struct {
	Save    SavingThrow // the save as it was rolled
	Natural int         // the d20 that counts (0 when there was no roll)
	Dice    [2]int      // both d20s when two were rolled (Dice[1] is 0 for one d20)
	Total   int         // Natural + Bonus + every Adjust (or what a feature put in its place: Indomitable Might)
	Success bool
	Changed []string // what changed the result after the d20 was rolled, in words ("Halfling Luck: 1 → 14")
}

// DeathSave is the Ability of a Death Saving Throw: it uses no ability.
const DeathSave = "death"

// AbilityName: "Dexterity" for "dex"; "Death" for a Death Saving Throw.
func AbilityName(short string) string {
	if short == DeathSave {
		return "Death"
	}
	for _, o := range Abilities {
		if o.Slug == short {
			return o.Name
		}
	}
	return strings.ToUpper(short)
}

// Mode: how the d20 is rolled, from the sources gathered: "advantage", "disadvantage", "cancelled" or "".
func (s SavingThrow) Mode() string {
	switch {
	case len(s.Advantage) > 0 && len(s.Disadvantage) > 0:
		return "cancelled"
	case len(s.Advantage) > 0:
		return "advantage"
	case len(s.Disadvantage) > 0:
		return "disadvantage"
	}
	return ""
}

// HasAdvantage: the save is rolled with Advantage (a source of it and none of Disadvantage).
func (s SavingThrow) HasAdvantage() bool { return s.Mode() == "advantage" }

// HasDisadvantage: the save is rolled with Disadvantage.
func (s SavingThrow) HasDisadvantage() bool { return s.Mode() == "disadvantage" }

// Modifier: everything added to the d20: the bonus and every adjustment.
func (s SavingThrow) Modifier() int {
	t := s.Bonus
	for _, x := range s.Adjust {
		t += x.Amount
	}
	return t
}

// Automatic: the save needs no roll; success says how it ends and why says what decided it.
func (s SavingThrow) Automatic() (auto, success bool, why string) {
	switch {
	case s.AutoSucceed != "":
		return true, true, s.AutoSucceed
	case s.AutoFail != "":
		return true, false, s.AutoFail
	}
	return false, false, ""
}

// Roll rolls the d20 (or two) and judges the save.
func (s SavingThrow) Roll() SaveResult {
	if auto, ok, _ := s.Automatic(); auto {
		return SaveResult{Save: s, Success: ok, Total: s.Modifier()}
	}
	r := SaveResult{Save: s}
	switch s.Mode() {
	case "advantage":
		r.Natural, r.Dice[0], r.Dice[1] = RollWithAdvantage()
	case "disadvantage":
		r.Natural, r.Dice[0], r.Dice[1] = RollWithDisadvantage()
	default:
		r.Natural = RollDie(20)
		r.Dice[0] = r.Natural
	}
	return r.judge()
}

// Judge gives the result for a d20 that is already known (a test, or a die a feature fixed).
func (s SavingThrow) Judge(natural int) SaveResult {
	if auto, ok, _ := s.Automatic(); auto {
		return SaveResult{Save: s, Success: ok, Total: s.Modifier()}
	}
	return SaveResult{Save: s, Natural: natural, Dice: [2]int{natural, 0}}.judge()
}

func (r SaveResult) judge() SaveResult {
	r.Total = r.Natural + r.Save.Modifier()
	r.Success = r.Total >= r.Save.DC
	return r
}

// Rolled: the save was rolled (it wasn't decided without a roll).
func (r SaveResult) Rolled() bool {
	auto, _, _ := r.Save.Automatic()
	return !auto
}

// Replace puts another d20 in place of the one that counts and judges again (Halfling Luck rerolls a 1; a Periapt of Wound
// Closure turns a 9 or lower into a 10). why says what changed it.
func (r SaveResult) Replace(natural int, why string) SaveResult {
	if !r.Rolled() {
		return r
	}
	n := r.Save.Judge(natural)
	n.Changed = append(append([]string{}, r.Changed...), why)
	return n
}

// RollAgain rolls the whole saving throw again, with the same Advantage or Disadvantage, and the new roll stands (Heroic
// Inspiration, the Luck Blade's Luck, Disciplined Survivor). why says what rolled it.
func (r SaveResult) RollAgain(why string) SaveResult {
	if !r.Rolled() {
		return r
	}
	n := r.Save.Roll()
	n.Changed = append(append([]string{}, r.Changed...), fmt.Sprintf("%s: %d before", why, r.Total))
	return n
}

// WithTotal puts another total in place of the one rolled (Indomitable Might uses the Strength score) and judges again.
func (r SaveResult) WithTotal(total int, why string) SaveResult {
	r.Total = total
	r.Success = total >= r.Save.DC
	r.Changed = append(append([]string{}, r.Changed...), why)
	return r
}

// Outcome: "SUCCESS" or "FAILURE"; "AUTO-SUCCESS" or "AUTO-FAIL" without a roll.
func (r SaveResult) Outcome() string {
	switch {
	case !r.Rolled() && r.Success:
		return "AUTO-SUCCESS"
	case !r.Rolled():
		return "AUTO-FAIL"
	case r.Success:
		return "SUCCESS"
	}
	return "FAILURE"
}

// Verb: "succeeds on" or "fails", for "<name> <verb> the <ability> saving throw".
func (r SaveResult) Verb() string {
	if r.Success {
		return "succeeds on"
	}
	return "fails"
}

// Describe gives how the d20 was rolled and what was added, in words:
//
//	" with Advantage (Danger Sense)"
//	" with Disadvantage (Restrained)"
//	" (Advantage from Haste and Disadvantage from Restrained cancel: one d20)"
//
// then each adjustment in square brackets (" [Bless +3]"), then what changed the result after the roll. It is "" for a plain roll.
func (r SaveResult) Describe() string {
	s := r.Save
	var out string
	if r.Rolled() {
		switch s.Mode() {
		case "advantage":
			out = " with Advantage (" + strings.Join(s.Advantage, ", ") + ")"
		case "disadvantage":
			out = " with Disadvantage (" + strings.Join(s.Disadvantage, ", ") + ")"
		case "cancelled":
			out = " (Advantage from " + strings.Join(s.Advantage, ", ") + " and Disadvantage from " + strings.Join(s.Disadvantage, ", ") + " cancel: one d20)"
		}
		for _, x := range s.Adjust {
			if x.Why != "" {
				out += " [" + x.Why + "]"
			}
		}
	}
	for _, c := range r.Changed {
		out += " [" + c + "]"
	}
	return out
}

// DiceText: "d20 9 + 3" or, with two d20s, "d20s 9 and 4: 9 + 3". What is added is the bonus; the adjustments are shown by
// Describe, so the d20, the bonus and the adjustments add up to the total (unless a feature replaced the total).
func (r SaveResult) DiceText() string {
	d := fmt.Sprintf("d20 %d", r.Natural)
	if r.Dice[1] != 0 {
		d = fmt.Sprintf("d20s %d and %d: %d", r.Dice[0], r.Dice[1], r.Natural)
	}
	switch b := r.Save.Bonus; {
	case b > 0:
		d += fmt.Sprintf(" + %d", b)
	case b < 0:
		d += fmt.Sprintf(" - %d", -b)
	}
	return d
}

// Tail is everything a result line says after "succeeds on the Dexterity saving throw" (or "and fails"):
//
//	" (14 vs DC 15; d20 11 + 3)"
//	" (14 vs DC 15; d20s 11 and 4: 11 + 3) with Advantage (Danger Sense) [Bless +2]"
//	" automatically (Paralyzed)"
//
// The numbers are in one pair of brackets that begins with the total and the DC: the pattern that hides them from players
// (cmd/server/player_text.go) is written for exactly this form.
func (r SaveResult) Tail() string {
	if auto, _, why := r.Save.Automatic(); auto {
		return " automatically (" + why + ")" + r.Describe()
	}
	return fmt.Sprintf(" (%d vs DC %d; %s)", r.Total, r.Save.DC, r.DiceText()) + r.Describe()
}

// Line gives the result line of a save as the GM's log keeps it, the same for every save:
//
//	"Aria fails the Dexterity saving throw (12 vs DC 15; d20 9 + 3) with Disadvantage (Restrained)"
//	"Aria fails the Dexterity saving throw automatically (Paralyzed)"
//
// What players read of it goes through playerText, which takes the numbers out.
func (r SaveResult) Line(name string) string {
	return name + " " + r.Clause()
}

// Clause is Line without the name: "fails the Dexterity saving throw (12 vs DC 15; d20 9 + 3)".
func (r SaveResult) Clause() string {
	return fmt.Sprintf("%s the %s%s", r.Verb(), r.Save.Name(), r.Tail())
}

// Name: "Dexterity saving throw", "Death saving throw"; "saving throw" when the ability isn't known.
func (s SavingThrow) Name() string {
	if n := AbilityName(s.Ability); n != "" {
		return n + " saving throw"
	}
	return "saving throw"
}

// ForSaver gives the result as the saver's own player reads it, numbers and all (label is "Dexterity saving throw" and the like):
//
//	"Dexterity saving throw: d20(9)+3 = 12 vs DC 15 → FAILURE"
//	"Dexterity saving throw with Advantage (Danger Sense): d20(9,14→14)+3 [Bless +2] = 19 vs DC 15 → SUCCESS"
//	"Dexterity saving throw: AUTO-FAIL (Paralyzed)"
func (r SaveResult) ForSaver(label string) string {
	if label == "" {
		label = titleWord(r.Save.Name())
	}
	if auto, _, why := r.Save.Automatic(); auto {
		return fmt.Sprintf("%s: %s (%s)", label, r.Outcome(), why)
	}
	s := r.Save
	mode := ""
	switch s.Mode() {
	case "advantage":
		mode = " with Advantage (" + strings.Join(s.Advantage, ", ") + ")"
	case "disadvantage":
		mode = " with Disadvantage (" + strings.Join(s.Disadvantage, ", ") + ")"
	case "cancelled":
		mode = " (Advantage from " + strings.Join(s.Advantage, ", ") + " and Disadvantage from " + strings.Join(s.Disadvantage, ", ") + " cancel: one d20)"
	}
	dice := fmt.Sprintf("d20(%d)", r.Natural)
	if r.Dice[1] != 0 {
		dice = fmt.Sprintf("d20(%d,%d→%d)", r.Dice[0], r.Dice[1], r.Natural)
	}
	adj := ""
	for _, x := range s.Adjust {
		if x.Why != "" {
			adj += " [" + x.Why + "]"
		}
	}
	out := fmt.Sprintf("%s%s: %s%+d%s = %d vs DC %d → %s", label, mode, dice, s.Bonus, adj, r.Total, s.DC, r.Outcome())
	for _, c := range r.Changed {
		out += " [" + c + "]"
	}
	return out
}

// SaveConditions: what a creature's conditions do to a saving throw (SRD 5.2.1, "Conditions"): a Paralyzed, Petrified, Stunned
// or Unconscious creature automatically fails Strength and Dexterity saves; a Restrained one has Disadvantage on Dexterity saves.
// It returns the conditions, in words, for each.
func SaveConditions(conditions []string, ability string) (autoFail, disadvantage []string) {
	ability = strings.ToLower(ability)
	if ability == "str" || ability == "dex" {
		for _, c := range []string{ConditionParalyzed, ConditionPetrified, ConditionStunned, ConditionUnconscious} {
			if HasCondition(conditions, c) {
				autoFail = append(autoFail, titleWord(c))
			}
		}
	}
	if ability == "dex" && HasCondition(conditions, ConditionRestrained) {
		disadvantage = append(disadvantage, titleWord(ConditionRestrained))
	}
	return autoFail, disadvantage
}

func titleWord(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
