package main

import (
	"fmt"
	"regexp"
	"strings"
)

// curse_records_2024.go: a curse a stat block or a GM puts on a creature is a `cursed` record in the one condition store; its Detail is
// the flags of what it does, joined by "+" (the rules owners read them: curseHas). Remove Curse and Greater Restoration end the record
// (endCursesBySpell). The record survives a fight and a Long Rest (keptRecordKinds): it ends with its time or the spell.
//
//	no_regain     "While cursed, the target can't regain Hit Points" (SRD 5.2.1 Mummy): cantRegainHP
//	short_rest    "the target gains no benefit from finishing Short Rests" (Incubus), "a Short or Long Rest" (Rakshasa): the rest owners
//	long_rest     the Long Rest half of it
//	lycanthropy   "If the cursed target drops to 0 Hit Points, it instead becomes a Werewolf under the GM's control" (the lycanthropes):
//	              what the record means to the GM when the creature drops (hitAfterDamage names it); the transformation is the GM's
//
// Not applied (the GM's, said in the reply): "or until the incubus dies" (the source's id is gone when the fight ends), the Mummy's
// Hit Point maximum that doesn't return and decreases every 24 hours.
const (
	condCursed        = "cursed"
	curseNoRegain     = "no_regain"
	curseShortRest    = "short_rest"
	curseLongRest     = "long_rest"
	curseLycanthropy  = "lycanthropy"
	woundedConditions = "wounded"
)

// keptRecordKinds: the record kinds that are not rules conditions and still survive the cleanups of a fight and of a Long Rest
// (dropMarkersKeepConditions, longRestConditions): their own time or spell ends them.
var keptRecordKinds = map[string]bool{"disease": true, condCursed: true, woundedConditions: true}

var (
	// curseIsRe: "the target is cursed for 24 hours or until the incubus dies" / "it is cursed" / "the target is cursed".
	curseIsRe = regexp.MustCompile(`(?i)(?:the target|it) is cursed(?: for (\d+) (minutes?|hours?))?(?: or until [^.,;]*? dies)?`)
	// curseRestRe: "Until the curse ends, the target gains no benefit from finishing Short Rests" / "While cursed, ... a Short or Long Rest".
	curseRestRe = regexp.MustCompile(`(?i)(?:Until the curse ends|While cursed), the target gains no benefit from finishing (?:a )?(Short or Long Rests?|Short Rests?)`)
	// curseNoRegainRe: "While cursed, the target can't regain Hit Points".
	curseNoRegainRe = regexp.MustCompile(`(?i)While cursed, the target can['’]t regain Hit Points`)
	// curseDropRe: "If the cursed target drops to 0 Hit Points, it instead becomes a Werewolf under the GM's control and has 10 Hit Points."
	curseDropRe = regexp.MustCompile(`(?i)If the cursed target drops to 0 Hit Points, it instead becomes [^.]*`)
	// curseConditionsRe: "Until the curse ends, the target has the Charmed and Poisoned conditions" (Lamia): the conditions end with the record.
	curseConditionsRe = regexp.MustCompile(`(?i)Until the curse ends, the target has the [A-Za-z, ]+ conditions?`)
	// humanoidGateRe: "If the target is a Humanoid, it is subjected to the following effect" (the lycanthropes).
	humanoidGateRe = regexp.MustCompile(`(?i)If the target is a Humanoid`)
)

// curseRead is a text's curse: the flags of what it does and how long it lasts (0: until a spell ends it).
type curseRead struct {
	Flags  []string
	Rounds int
	Until  string // "or until the incubus dies": the GM's, not applied
}

// readCurse reads the curse of a Failure or a Hit.
func (e *saveEffect) readCurse(text string) {
	m := curseIsRe.FindStringSubmatch(text)
	if m == nil {
		return
	}
	c := &curseRead{}
	if m[1] != "" {
		n := 0
		fmt.Sscanf(m[1], "%d", &n)
		unit := 10 // rounds in a minute
		if strings.HasPrefix(strings.ToLower(m[2]), "hour") {
			unit = 600
		}
		c.Rounds = n * unit
	}
	if i := strings.Index(strings.ToLower(m[0]), " or until "); i >= 0 {
		c.Until = strings.TrimSpace(m[0][i+1:])
	}
	if curseNoRegainRe.MatchString(text) {
		c.Flags = append(c.Flags, curseNoRegain)
	}
	if r := curseRestRe.FindStringSubmatch(text); r != nil {
		c.Flags = append(c.Flags, curseShortRest)
		if strings.Contains(strings.ToLower(r[1]), "long") {
			c.Flags = append(c.Flags, curseLongRest)
		}
	}
	if curseDropRe.MatchString(text) {
		c.Flags = append(c.Flags, curseLycanthropy)
	}
	e.Curse = c
	e.CurseLinked = curseConditionsRe.MatchString(text)
}

// stripCurse removes what readCurse applies from a sentence.
func stripCurse(s string) string {
	for _, re := range []*regexp.Regexp{curseNoRegainRe, curseRestRe, curseConditionsRe, humanoidGateRe} {
		s = re.ReplaceAllString(s, "")
	}
	s = curseIsRe.ReplaceAllStringFunc(s, func(m string) string {
		if i := strings.Index(strings.ToLower(m), " or until "); i >= 0 {
			return m[i+1:] // the source's death ends it: the GM's, kept
		}
		return ""
	})
	return s
}

// giveCurse puts the curse record on the target.
func (e saveEffect) giveCurse(lobbyID, tid int, source string) []string {
	if e.Curse == nil {
		return nil
	}
	a := condAdd{Condition: condCursed + ":" + strings.Join(e.Curse.Flags, "+"), Source: source, SourceID: e.SourceID, Magical: e.IsSpell, Rounds: e.Curse.Rounds}
	if res := addCondition(lobbyID, tid, a); res.Err != nil {
		return []string{riderNotApplied("cursed")}
	}
	words := "cursed"
	if len(e.Curse.Flags) > 0 {
		words += " (" + curseWords(e.Curse.Flags) + ")"
	}
	return []string{words}
}

// curseWords: the flags in words, for a reply.
func curseWords(flags []string) string {
	var out []string
	for _, f := range flags {
		switch f {
		case curseNoRegain:
			out = append(out, "can't regain Hit Points")
		case curseShortRest:
			out = append(out, "no benefit from Short Rests")
		case curseLongRest:
			out = append(out, "no benefit from Long Rests")
		case curseLycanthropy:
			out = append(out, "dropping to 0 Hit Points turns it into the creature, under the GM's control")
		}
	}
	return strings.Join(out, ", ")
}

// curseHas: the creature has a curse with the flag.
func curseHas(lobbyID, who int, flag string) bool {
	if lobbyID == 0 || who == 0 {
		return false
	}
	for _, r := range loadConditionRecords(lobbyID, who) {
		if r.Condition != condCursed {
			continue
		}
		for _, f := range strings.Split(r.Detail, "+") {
			if f == flag {
				return true
			}
		}
	}
	return false
}

// restBenefit: whether a character's rest gives its benefits (a curse of "gains no benefit from finishing a Short or Long Rest" says no).
// The rest still takes its time; only what it restores is withheld.
func restBenefit(charID int, long bool) (bool, string) {
	lobby := lobbyOfChar(charID)
	if long && curseHas(lobby, charID, curseLongRest) {
		return false, "it is cursed: it gains no benefit from finishing a Long Rest"
	}
	if curseHas(lobby, charID, curseShortRest) && !long {
		return false, "it is cursed: it gains no benefit from finishing a Short Rest"
	}
	if long && curseHas(lobby, charID, curseShortRest) && curseHas(lobby, charID, curseLongRest) {
		return false, "it is cursed: it gains no benefit from finishing a Long Rest"
	}
	return true, ""
}
