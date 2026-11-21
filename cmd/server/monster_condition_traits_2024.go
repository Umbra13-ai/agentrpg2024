package main

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// A creature's trait that acts when it receives a condition (Monster Manual Flumph, Prone Deficiency: "If the flumph receives the Prone
// condition, roll a die. On an odd number, it has the Incapacitated condition. At the end of each of its turns, the flumph makes a DC 10
// Dexterity saving throw, ending the Incapacitated condition on a success."). The condition owner calls conditionTraitReaction once a
// condition took hold (conditionTakesHold), for a monster and for a companion with the stat block; the die is rolled and the second
// condition, with its repeated save, goes through addCondition.

var conditionTriggerRe = regexp.MustCompile(`If the [a-z -]+ receives the ([A-Z][a-z]+) condition, roll a die\. On an (odd|even) number, it has the ([A-Z][a-z]+) condition\. At the end of each of its turns, the [a-z -]+ makes a DC (\d+) ([A-Z][a-z]+) saving throw, ending the [A-Z][a-z]+ condition on a success`)

// conditionTraitReaction: what a creature's trait does now that it received cond ("" when it has no such trait for cond).
func conditionTraitReaction(lobbyID, targetID int, cond string) string {
	meta := creatureMetaOf(lobbyID, targetID)
	if meta == nil {
		return ""
	}
	for _, t := range meta.Traits {
		m := conditionTriggerRe.FindStringSubmatch(t.Text)
		if m == nil || !strings.EqualFold(m[1], cond) {
			continue
		}
		roll := rand.Intn(6) + 1
		hit := (roll%2 == 1) == (m[2] == "odd")
		name := creatureName(lobbyID, targetID)
		if !hit {
			return fmt.Sprintf("%s's %s: the die shows %d, nothing more happens.", name, t.Name, roll)
		}
		dc, _ := strconv.Atoi(m[4])
		second := strings.ToLower(m[3])
		res := addCondition(lobbyID, targetID, condAdd{Condition: second, Source: name + " (" + t.Name + ")", SourceID: targetID,
			EndSave: &conditionSave{Ability: strings.ToLower(m[5][:3]), DC: dc}})
		if res.Immune != "" {
			return fmt.Sprintf("%s's %s: the die shows %d, but it is immune to %s (%s).", name, t.Name, roll, m[3], res.Immune)
		}
		return fmt.Sprintf("%s's %s: the die shows %d: it has the %s condition (a DC %d %s save at the end of each of its turns ends it).", name, t.Name, roll, m[3], dc, m[5])
	}
	return ""
}

// blurredFormRe: a creature's trait that makes attack rolls against it have Disadvantage ("Attack rolls against the mephit are made with
// Disadvantage unless the mephit has the Incapacitated condition": Steam Mephit, Blurred Form).
var blurredFormRe = regexp.MustCompile(`Attack rolls against the [a-z -]+ are made with Disadvantage(?: unless the [a-z -]+ has the Incapacitated condition)?\.?$`)

// attackedWithDisadvantage: the name of the target's trait that gives attack rolls against it Disadvantage now ("" when none); the
// Incapacitated exception holds for every condition that includes Incapacitated. An attackSources row reads it.
func attackedWithDisadvantage(lobbyID, targetID int, targetConds []string) string {
	meta := creatureMetaOf(lobbyID, targetID)
	if meta == nil {
		return ""
	}
	for _, t := range meta.Traits {
		if !blurredFormRe.MatchString(strings.TrimSpace(t.Text)) {
			continue
		}
		if strings.Contains(t.Text, "unless") {
			for _, c := range targetConds {
				if game.IsIncapacitatingCondition(c) {
					return ""
				}
			}
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
