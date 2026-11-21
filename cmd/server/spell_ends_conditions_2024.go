package main

import (
	"fmt"
	"strings"
)

// The conditions a spell ends (SRD 5.2.1; play-test feedback log entry 64, I-GM-23): one table, read by every cast path through
// endSpellConditions, which ends them through the condition owner (endCondition). Before, Lesser Restoration, Greater Restoration,
// Power Word Heal and Protection from Poison each had their own code, and Heal and Mass Heal had none: their reply quoted the spell's
// "This spell also ends the Blinded, Deafened, and Poisoned conditions" and nothing ended.
//
// A healing spell that also ends conditions ends them first, then heals (the spell does both at once): a target the Poisoned condition
// kept from regaining Hit Points (a Pit Fiend's Bite) gets the healing. (The owner's choice, pending: this is the suggested default.)

// conditionEnding: the conditions a spell ends; Pick: the caster chooses one (the one the description names; else the first, in this
// order, the target has).
type conditionEnding struct {
	Conditions []string
	Pick       bool
}

var spellEndsConditions = map[string]conditionEnding{
	"heal":                   {Conditions: []string{"blinded", "deafened", "poisoned"}},
	"mass-heal":              {Conditions: []string{"blinded", "deafened", "poisoned"}},
	"power-word-heal":        {Conditions: []string{"charmed", "frightened", "paralyzed", "poisoned", "stunned"}},
	"protection-from-poison": {Conditions: []string{"poisoned"}},
	"lesser-restoration":     {Conditions: []string{"paralyzed", "poisoned", "blinded", "deafened"}, Pick: true},
	"greater-restoration":    {Conditions: []string{"charmed", "petrified"}, Pick: true}, // its other effects (Exhaustion, a curse, a reduction): greaterRestoration
}

// endSpellConditions ends on creature id the conditions spellKey ends. ended are the conditions that ended (title case); named is the
// condition the description chose for a Pick spell ("" when it named none).
func endSpellConditions(lobbyID, id int, spellKey, description string) (ended []string, named string) {
	row, ok := spellEndsConditions[spellKey]
	if !ok || id == 0 {
		return nil, ""
	}
	end := func(c string) bool {
		if len(endCondition(lobbyID, id, c, "")) > 0 {
			ended = append(ended, titleCase(c))
			return true
		}
		return false
	}
	if !row.Pick {
		for _, c := range row.Conditions {
			end(c)
		}
		return ended, ""
	}
	dl := strings.ToLower(description)
	for _, c := range row.Conditions {
		if strings.Contains(dl, conditionStem(c)) { // "poison", "deaf", "paraly", "petrif", "charm": the caster's choice
			end(c)
			return ended, c
		}
	}
	for _, c := range row.Conditions {
		if end(c) {
			break
		}
	}
	return ended, ""
}

// spellEndsOnTargets: for a spell no other resolver applied (Mass Heal: the GM divides its Hit Points), the conditions it ends on each
// creature it is aimed at (target_id / target_ids, else a character the description names). "" when it ends none.
func spellEndsOnTargets(charID int, spellKey, description string) string {
	if _, ok := spellEndsConditions[spellKey]; !ok {
		return ""
	}
	aim := spellAllyAim(charID, description) // living_target_2024.go, as a healing spell's
	if aim.Others {
		return othersNote
	}
	targets := aim.IDs
	lobby := lobbyOfChar(charID)
	var parts []string
	for _, id := range targets {
		if ended, _ := endSpellConditions(lobby, id, spellKey, description); len(ended) > 0 {
			parts = append(parts, fmt.Sprintf("the %s condition on %s ends", joinNames(ended), creatureName(lobby, id)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, "; ") + "."
}

// conditionStem: the part of a condition's name every form of the word has ("deaf" for deafened and deafness, "petrif" for petrified
// and petrify).
func conditionStem(c string) string {
	s := strings.TrimSuffix(c, "ed")
	for _, suffix := range []string{"en", "i", "z", "n"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return s
}

// spellEndsWords: what a pick spell's row offers ("Blinded, Deafened, Paralyzed or Poisoned"), for its reply.
func spellEndsWords(spellKey string) string {
	row := spellEndsConditions[spellKey]
	names := make([]string, len(row.Conditions))
	for i, c := range row.Conditions {
		names[i] = titleCase(c)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
