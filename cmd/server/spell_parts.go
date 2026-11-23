package main

// Spells that deal damage in more than one part: Flame Strike (Fire and Radiant), Ice Knife (Piercing on a hit, then a
// Cold explosion), Acid Arrow (damage now and at the end of the target's next turn), Wall of Thorns and so on.
// spells.damage_parts lists the parts in order (the first is the one in damage_dice and damage_type).

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type damagePart struct {
	Name        string            `json:"name"`
	Dice        string            `json:"dice"`
	Type        string            `json:"type"`
	Timing      string            `json:"timing"`            // on_cast, on_hit, end_of_next_turn, when_entering, ...
	Save        string            `json:"save,omitempty"`    // the saving throw ability, when there is one
	OnSave      string            `json:"on_save,omitempty"` // "half" or "none"
	AtSlotLevel map[string]string `json:"at_slot_level,omitempty"`
	Note        string            `json:"note,omitempty"`
	OnMiss      string            `json:"on_miss,omitempty"` // "half": a spell attack that misses still deals half of this part (Acid Arrow's initial damage)
}

var abilityWord = map[string]string{"STR": "Strength", "DEX": "Dexterity", "CON": "Constitution", "INT": "Intelligence", "WIS": "Wisdom", "CHA": "Charisma"}

// diceAt is the dice of a part when the spell is cast with a slot of the given level.
func (p damagePart) diceAt(slotLevel int) string {
	if d, ok := p.AtSlotLevel[fmt.Sprint(slotLevel)]; ok {
		return d
	}
	return p.Dice
}

// saveWords describes what the saving throw of a part does ("Dexterity save, half as much on a success").
func (p damagePart) saveWords(dc int) string {
	if p.Save == "" {
		return ""
	}
	w := abilityWord[p.Save]
	if w == "" {
		w = p.Save
	}
	out := fmt.Sprintf("DC %d %s save", dc, w)
	if p.OnSave == "half" {
		out += ", half as much on a success"
	} else {
		out += ", no damage on a success"
	}
	return out
}

// primarySaveInfo is the save text after the first part of a spell with parts ("" when the first part has no save).
func primarySaveInfo(parts []damagePart, dc int) string {
	if len(parts) == 0 || parts[0].Save == "" {
		return ""
	}
	return " (" + parts[0].saveWords(dc) + ")"
}

// laterTimings are the parts that do not happen when the spell is cast: they are reported, not rolled now.
var laterTimings = map[string]string{
	"end_of_next_turn": "at the end of the target's next turn", "end_of_each_turn": "at the end of each of the target's turns",
	"when_entering": "each time a creature enters or ends its turn there", "each_round": "each round",
	"turn_2": "on your turn 2", "turn_3": "on your turn 3", "turn_4": "on your turn 4", "turns_5_to_10": "on your turns 5 to 10",
}

// castPartRolls: for each caster, what the cast text rolled for each damage part after the first (part index to damage).
var castPartRolls sync.Map

// partRoll is the damage rolled for part idx of the caster's current cast: the number the cast text showed (rolled once), or a fresh roll when
// the cast text rolled none for it.
func partRoll(charID, idx int, dice string) int {
	if v, ok := castPartRolls.Load(charID); ok {
		if n, ok := v.(map[int]int)[idx]; ok {
			return n
		}
	}
	return game.RollDamage(dice, false)
}

// isHybridSpell: a spell with a spell attack for its first damage and a saving throw for the rest, hit or miss (Ice Knife: the shard's
// Piercing on a hit, then the explosion's Cold on a Dexterity save whether it hit or not).
func isHybridSpell(spell SRDSpell) bool {
	if len(spell.DamageParts) < 2 || spell.DamageParts[0].Timing != "on_hit" || !strings.Contains(strings.ToLower(spell.Description), "spell attack") {
		return false
	}
	for _, p := range spell.DamageParts[1:] {
		if p.Save != "" {
			return true
		}
	}
	return false
}

// extraPartsText rolls the other parts that happen now and lists the ones that come later.
func extraPartsText(charID int, parts []damagePart, slotLevel, dc int) string {
	rolls := map[int]int{}
	castPartRolls.Store(charID, rolls) // what is rolled here is what the resolvers apply (partRoll): one roll, shown and dealt
	if len(parts) < 2 {
		return ""
	}
	var now, later []string
	for i, p := range parts {
		if i == 0 {
			continue
		}
		dice := p.diceAt(slotLevel)
		if when, isLater := laterTimings[p.Timing]; isLater {
			s := fmt.Sprintf("%s %s damage %s", dice, p.Type, when)
			if sw := p.saveWords(dc); sw != "" {
				s += " (" + sw + ")"
			}
			later = append(later, s)
			continue
		}
		rolls[i] = game.RollDamage(dice, false)
		s := fmt.Sprintf("%d %s damage (%s, %s)", rolls[i], p.Type, dice, strings.TrimSpace(p.Name))
		if sw := p.saveWords(dc); sw != "" {
			s += " (" + sw + ")"
		}
		now = append(now, s)
	}
	out := ""
	if len(now) > 0 {
		out += " Also: " + strings.Join(now, "; ") + "."
	}
	if len(later) > 0 {
		out += " Later: " + strings.Join(later, "; ") + " (roll it then)."
	}
	return out
}

// ritualWord matches the word "ritual" in a description (not "spiritual").
var ritualWord = regexp.MustCompile(`\britual\b`)

// monsterSpellSave is a monster's saving throw against a spell: Magic Resistance (a trait in its stat block) gives Advantage
// (a row of saveSources, save_2024.go).
func monsterSpellSave(lobbyID, id int, ability string, dc int) game.SaveResult {
	return monsterSpellSaveFor(lobbyID, id, ability, dc, saveStakes{Condition: true})
}

// monsterSpellSaveFor: the same with the stakes of the save for Legendary Resistance (damage only, or a condition).
func monsterSpellSaveFor(lobbyID, id int, ability string, dc int, st saveStakes) game.SaveResult {
	return monsterSpellSaveResult(lobbyID, id, ability, dc, st, nil)
}

// monsterSpellSaveResult: the whole result, with sources of Disadvantage only the caller knows (a Construct against Shatter,
// Heightened Spell). A monster with Magic Resistance gets a note for the GM.
func monsterSpellSaveResult(lobbyID, id int, ability string, dc int, st saveStakes, dis []string) game.SaveResult {
	return monsterSpellSaveBy(lobbyID, id, ability, dc, st, dis, 0)
}

// monsterSpellSaveBy: the same, with the caster (its id: a character, a monster or a companion), the save's source for the rows that
// read it (saveCtx.source).
func monsterSpellSaveBy(lobbyID, id int, ability string, dc int, st saveStakes, dis []string, caster int) game.SaveResult {
	r := (&saveCtx{lobby: lobbyID, saver: id, ability: ability, dc: dc, spell: true, extraDis: dis, source: caster}).resist(st)
	for _, a := range r.Save.Advantage {
		if a == "Magic Resistance" {
			addSaveNote(lobbyID, fmt.Sprintf("[%s has Magic Resistance: Advantage on saving throws against spells.]", creatureName(lobbyID, id)))
		}
	}
	return r
}
