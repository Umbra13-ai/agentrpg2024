package main

import (
	"regexp"
	"strings"
)

// evasion_2024.go: which creature has Evasion (SRD 5.2.1: a Monk or Rogue of level 7 or more; a stat block's trait, the Assassin), for
// a Dexterity saving throw. What Evasion does to the damage is game.SaveDamage; every route that deals a saving throw's damage (a
// character's spell at a character or a monster, a stat block's save effect at a character, a monster or a companion, the GM's area
// cast) asks creatureEvasion and hands the answer to game.SaveDamage, so a creature has the same Evasion whoever makes it save.
// Go: TestSaveDamageAndEvasion, TestPostgresEvasionOnEverySaveRoute.

// creatureEvasion: the creature (a character id > 0, a fight monster id < 0) has Evasion and the save is a Dexterity saving throw.
func creatureEvasion(lobbyID, id int, ability string) bool {
	if id < 0 && evasionByWords(lobbyID, id, ability) {
		return true
	}
	if saveAbilityKey(ability) != "dex" {
		return false
	}
	switch {
	case id > 0:
		return hasEvasion(id)
	case id < 0:
		return monsterHasTrait(lobbyID, id, "Evasion") || traitMechanicOf(lobbyID, id, "evasion") // the SRD trait, or an attached evasion
	}
	return false
}

// evasionWordsRe: a trait in Evasion's words under another name ("Avoidance. If the displacer beast is subjected to an effect that allows it
// to make a saving throw to take only half damage, it instead takes no damage if it succeeds on the saving throw and only half damage if it
// fails": owner, Nov 25; I-ALDENQA-6). It holds for the saving throw its words name, any one when they name none.
var evasionWordsRe = regexp.MustCompile(`(?i)takes no damage if it succeeds on the saving throw,? and only half damage if it fails`)

// evasionByWords: a monster's trait that is Evasion in its words for this saving throw.
func evasionByWords(lobbyID, id int, ability string) bool {
	meta := creatureMetaOf(lobbyID, id)
	if meta == nil {
		return false
	}
	for _, t := range meta.Traits {
		if !evasionWordsRe.MatchString(t.Text) {
			continue
		}
		named := false
		for _, a := range []string{"Strength", "Dexterity", "Constitution", "Intelligence", "Wisdom", "Charisma"} {
			if strings.Contains(t.Text, a+" saving throw") {
				named = true
				if saveAbilityKey(a) == saveAbilityKey(ability) {
					return true
				}
			}
		}
		if !named {
			return true
		}
	}
	return false
}
