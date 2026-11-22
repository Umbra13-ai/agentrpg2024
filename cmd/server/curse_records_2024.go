package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
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
	curseHPMax        = "hpmax" // hpmax=3d6@<story round of its last tick>: "its Hit Point maximum decreases by 10 (3d6) every 24 hours that elapse"
	woundedConditions = "wounded"
)

// keptRecordKinds: the record kinds that are not rules conditions and still survive the cleanups of a fight and of a Long Rest
// (dropMarkersKeepConditions, longRestConditions): their own time or spell ends them.
var keptRecordKinds = map[string]bool{"disease": true, condCursed: true, woundedConditions: true}

var (
	// curseIsRe: "the target is cursed for 24 hours or until the incubus dies" / "it is cursed" / "the target is cursed".
	curseIsRe = regexp.MustCompile(`(?i)(?:the target|it) is cursed(?: for (\d+) (minutes?|hours?))?(?: or until [^.,;]*? dies)?`)
	// curseRestRe: "Until the curse ends, the target gains no benefit from finishing Short Rests" / "While cursed, ... a Short or Long Rest".
	curseRestRe = regexp.MustCompile(`(?i)(?:(?:Until the curse ends|While cursed), the target|, it) gains no benefit from finishing (?:a )?(Short or Long Rests?|Short Rests?|Long Rests?)`)
	// curseHPMaxRe: "its Hit Point maximum doesn't return to normal when finishing a Long Rest, and its Hit Point maximum decreases by 10 (3d6) every 24 hours
	// that elapse" (Mummy) / "it gains no benefit from finishing a Long Rest, and its Hit Point maximum decreases by 10 (3d6) every 24 hours that elapse".
	curseHPMaxRe = regexp.MustCompile(`(?i)(?:, )?(?:and )?its Hit Point maximum decreases by \d+ \((\d+d\d+)\) every 24 hours that elapse`)
	// curseHPMaxStaysRe: "its Hit Point maximum doesn't return to normal when finishing a Long Rest".
	curseHPMaxStaysRe = regexp.MustCompile(`(?i)(?:, )?its Hit Point maximum doesn['’]t return to normal when finishing a Long Rest`)
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
		if strings.Contains(strings.ToLower(r[1]), "short") {
			c.Flags = append(c.Flags, curseShortRest)
		}
		if strings.Contains(strings.ToLower(r[1]), "long") {
			c.Flags = append(c.Flags, curseLongRest)
		}
	}
	if h := curseHPMaxRe.FindStringSubmatch(text); h != nil {
		c.Flags = append(c.Flags, curseHPMax+"="+h[1]) // the dice; the day it began is added when it is given (giveCurse)
	}
	if curseDropRe.MatchString(text) {
		c.Flags = append(c.Flags, curseLycanthropy)
	}
	e.Curse = c
	e.CurseLinked = curseConditionsRe.MatchString(text)
}

// stripCurse removes what readCurse applies from a sentence.
func stripCurse(s string) string {
	for _, re := range []*regexp.Regexp{curseNoRegainRe, curseRestRe, curseHPMaxRe, curseHPMaxStaysRe, curseConditionsRe, humanoidGateRe} {
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
	flags := append([]string{}, e.Curse.Flags...)
	for i, f := range flags {
		if strings.HasPrefix(f, curseHPMax+"=") && !strings.Contains(f, "@") {
			if holdsHPMaxCurse(lobbyID, tid) { // a second hit doesn't start a second count: the creature is cursed once
				return []string{"already cursed (its days keep counting)"}
			}
			flags[i] = fmt.Sprintf("%s@%d@%d", f, storyRoundsOf(lobbyID), lobbyID) // its day count starts now, on this campaign's clock
		}
	}
	a := condAdd{Condition: condCursed + ":" + strings.Join(flags, "+"), Source: source, SourceID: e.SourceID, Magical: e.IsSpell, Rounds: e.Curse.Rounds}
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
		default:
			if strings.HasPrefix(f, curseHPMax+"=") {
				out = append(out, "its Hit Point maximum falls every 24 hours and a Long Rest doesn't return it")
			}
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

// curseTakesHPMax: the record's Detail has the Hit Point maximum flag.
func curseTakesHPMax(detail string) bool {
	_, _, _, ok := curseHPMaxOf(detail)
	return ok
}

// curseHPMaxOf reads the flag out of a record's Detail: the dice, the story round of its last tick and the campaign whose clock that round is on
// (0: not recorded).
func curseHPMaxOf(detail string) (dice string, last int64, lobby int, ok bool) {
	for _, f := range strings.Split(detail, "+") {
		if !strings.HasPrefix(f, curseHPMax+"=") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(f, curseHPMax+"="), "@")
		dice = parts[0]
		if len(parts) > 1 {
			n, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return dice, 0, 0, false
			}
			last = n
		}
		if len(parts) > 2 {
			lobby, _ = strconv.Atoi(parts[2])
		}
		return dice, last, lobby, dice != ""
	}
	return "", 0, 0, false
}

// holdsHPMaxCurse: the creature already has a curse that takes Hit Point maximum.
func holdsHPMaxCurse(lobbyID, who int) bool {
	for _, r := range loadConditionRecords(lobbyID, who) {
		if r.Condition == condCursed && curseTakesHPMax(r.Detail) {
			return true
		}
	}
	return false
}

// storyRoundsOf: the campaign's story clock (lobbies.story_rounds: the GM's pass-time and travel).
func storyRoundsOf(lobbyID int) int64 {
	var n int64
	db.QueryRow(`SELECT COALESCE(story_rounds,0) FROM lobbies WHERE id = $1`, lobbyID).Scan(&n)
	return n
}

const storyDayRounds = 14400 // 24 hours of 6-second rounds

// maxCurseDaysPerPass: the days one pass of time counts for a curse (a pass of years is not thousands of rolls and transactions).
const maxCurseDaysPerPass = 100

// cursedHPMaxTick: for each full 24 story hours since a cursed character's last tick, its Hit Point maximum decreases by the curse's dice (the Mummy's
// Rotting Fist). The record's day count is advanced first (it is the claim), then the maximum reduced (reduceCursedMaxHP); a maximum that reaches 0 is a
// death. Called by passSpellTimeOn after the story clock moves. It answers what happened, for the GM.
func cursedHPMaxTick(lobbyID int) []string {
	rows, err := db.Query(`SELECT DISTINCT r.target_id FROM condition_records r JOIN characters c ON c.id = r.target_id
		WHERE c.lobby_id = $1 AND r.condition = $2 AND r.detail LIKE $3 AND r.target_id > 0`, // the creature's campaign now: not the one it was cursed in
		lobbyID, condCursed, "%"+curseHPMax+"=%")
	if err != nil {
		logDBProblem("cursed hit point maximum", err)
		return nil
	}
	var targets []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil && !isCompanionCombatID(id) {
			targets = append(targets, id)
		}
	}
	rows.Close()
	now := storyRoundsOf(lobbyID)
	var out []string
	for _, id := range targets {
		var amounts []int
		err := changeConditions(lobbyID, id, func(c *conditionChange) error {
			for _, r := range c.recs {
				dice, last, stampedIn, ok := curseHPMaxOf(r.Detail)
				if r.Condition != condCursed || !ok {
					continue
				}
				days := int((now - last) / storyDayRounds)
				if stampedIn != 0 && stampedIn != lobbyID { // the creature is in another campaign now: its days count again from this clock
					days, last = 0, now
				} else if days <= 0 {
					continue
				}
				days = min(days, maxCurseDaysPerPass)
				for i := 0; i < days; i++ {
					amounts = append(amounts, game.RollDamage(normDice(dice), false))
				}
				flags := strings.Split(r.Detail, "+")
				for i, f := range flags {
					if strings.HasPrefix(f, curseHPMax+"=") {
						flags[i] = fmt.Sprintf("%s=%s@%d@%d", curseHPMax, dice, last+int64(days)*storyDayRounds, lobbyID)
					}
				}
				if err := c.set(r.ID, r.DurationRounds, strings.Join(flags, "+")); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			logDBProblem("cursed hit point maximum", err)
			continue
		}
		for _, a := range amounts {
			cut, died, err := reduceCursedMaxHP(id, a)
			if err == nil && cut == 0 && !died {
				break // dead already, or nothing left to take
			}
			if err != nil {
				logDBProblem("cursed hit point maximum", err)
				continue
			}
			line := fmt.Sprintf("%s is cursed: its Hit Point maximum decreases by %d (%d taken; a Long Rest doesn't return it).", creatureName(lobbyID, id), a, cut)
			if died {
				if ok, _ := killCharacter(id); ok {
					line += " Its Hit Point maximum is 0: it dies."
				}
				out = append(out, line)
				break // the rest of the days are for the dead
			}
			out = append(out, line)
		}
	}
	return out
}
