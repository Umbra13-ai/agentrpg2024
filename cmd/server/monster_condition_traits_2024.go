package main

import (
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// A creature's own traits that change how other creatures act against it (SRD 5.2.1 Steam Mephit, Blurred Form), and the stat block
// of a monster or companion of the fight that those readers share.

// blurredFormRe: a creature's trait that makes attack rolls against it have Disadvantage ("Attack rolls against the mephit are made with
// Disadvantage unless the mephit has the Incapacitated condition": Steam Mephit, Blurred Form).
var blurredFormRe = regexp.MustCompile(`Attack rolls against the [a-z -]+ are made with Disadvantage(?: unless the [a-z -]+ has the Incapacitated condition)?\.?$`)

// blurredHaveRe: the same trait in the 2024 Monster Manual's and the adventures' words ("Attack rolls against the quickling have
// Disadvantage unless the quickling has the Incapacitated condition or its speed is 0": owner, Nov 25; I-ALDENQA-5, -6). Its exceptions
// are read from the trait's own words: the Incapacitated condition, a Speed of 0.
var blurredHaveRe = regexp.MustCompile(`(?i)^Attack rolls against (?:the [a-z' -]+|it) have Disadvantage\b`)

// speedZeroExceptRe: a trait's exception while its creature's Speed is 0 ("... or its speed is 0").
var speedZeroExceptRe = regexp.MustCompile(`(?i)\bits speed is 0\b`)

// attackedWithDisadvantage: the name of the target's trait that gives attack rolls against it Disadvantage now ("" when none): the SRD
// wording (blurredFormRe) or an attached attacked_with_disadvantage mechanic; the Incapacitated exception holds for every condition that
// includes Incapacitated. An attackSources row reads it.
func attackedWithDisadvantage(lobbyID, targetID int, targetConds []string) string {
	meta := creatureMetaOf(lobbyID, targetID)
	if meta == nil {
		return ""
	}
	for _, t := range meta.Traits {
		unless, atSpeedZero := strings.Contains(t.Text, "unless"), false
		if m, ok := t.mechanic("attacked_with_disadvantage"); ok { // a GM's attached mechanic (monster_mechanics_2024.go; GM doc B3)
			b, set := m["unless_incapacitated"].(bool)
			unless = !set || b
			atSpeedZero, _ = m["unless_speed_zero"].(bool)
		} else if blurredHaveRe.MatchString(strings.TrimSpace(t.Text)) {
			unless = strings.Contains(t.Text, "Incapacitated")
		} else if !blurredFormRe.MatchString(strings.TrimSpace(t.Text)) {
			continue
		}
		if speedZeroExceptRe.MatchString(t.Text) { // the trait's own words, whatever an attached mechanic says (I-ALDENQA-5)
			atSpeedZero = true
		}
		if unless {
			for _, c := range targetConds {
				if game.IsIncapacitatingCondition(c) {
					return ""
				}
			}
		}
		if atSpeedZero && creatureSpeedZero(lobbyID, targetID, targetConds) { // the speed owner's answer (speed_owner_2024.go)
			return ""
		}
		return t.Name
	}
	return ""
}

// creatureMetaOf: the stat block of a monster of the fight or of an active companion (nil for a character).
func creatureMetaOf(lobbyID, id int) *monsterMeta {
	if isCompanionCombatID(id) {
		c, ok := activeCompanionInCampaign(lobbyID, id)
		if !ok {
			return nil
		}
		return monsterMetaFor(c.Form)
	}
	if id < 0 {
		_, meta, _ := monsterCombatant(lobbyID, id)
		return meta
	}
	return nil
}
