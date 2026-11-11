package main

// Spells that deal damage in more than one part: Flame Strike (Fire and Radiant), Ice Knife (Piercing on a hit, then a
// Cold explosion), Acid Arrow (damage now and at the end of the target's next turn), Wall of Thorns and so on.
// spells.damage_parts lists the parts in order (the first is the one in damage_dice and damage_type).

import (
	"fmt"
	"regexp"
	"strings"

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

// extraPartsText rolls the other parts that happen now and lists the ones that come later.
func extraPartsText(parts []damagePart, slotLevel, dc int) string {
	if len(parts) < 2 {
		return ""
	}
	var now, later []string
	for _, p := range parts[1:] {
		dice := p.diceAt(slotLevel)
		if when, isLater := laterTimings[p.Timing]; isLater {
			s := fmt.Sprintf("%s %s damage %s", dice, p.Type, when)
			if sw := p.saveWords(dc); sw != "" {
				s += " (" + sw + ")"
			}
			later = append(later, s)
			continue
		}
		s := fmt.Sprintf("%d %s damage (%s, %s)", game.RollDamage(dice, false), p.Type, dice, strings.TrimSpace(p.Name))
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

// monsterSpellSave is a monster's saving throw against a spell: Magic Resistance (a trait in its stat block) gives Advantage.
func monsterSpellSave(lobbyID, id int, ability string, dc int) (bool, int) {
	return monsterSpellSaveFor(lobbyID, id, ability, dc, saveStakes{Condition: true})
}

// monsterSpellSaveFor: the same with the stakes of the save for Legendary Resistance (damage only, or a condition).
func monsterSpellSaveFor(lobbyID, id int, ability string, dc int, st saveStakes) (bool, int) {
	saved, total := monsterSaveRoll(lobbyID, id, ability, dc)
	_, fightAdv := fightingSaveAdvantage.Load(lobbyID)
	if mr := monsterHasTrait(lobbyID, id, "Magic Resistance"); mr {
		if !saved && !fightAdv {
			saved, total = monsterSaveRoll(lobbyID, id, ability, dc) // Advantage: a second roll that succeeds wins
		}
		addSaveNote(lobbyID, fmt.Sprintf("[%s has Magic Resistance: Advantage on saving throws against spells.]", creatureName(lobbyID, id)))
	}
	if !saved && id < 0 && legendaryResistance(lobbyID, id, st) {
		return true, total
	}
	return saved, total
}

// monsterHasTrait: the monster's stat block has a trait of that name.
func monsterHasTrait(lobbyID, id int, trait string) bool {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			var n int
			db.QueryRow(`SELECT count(*) FROM monsters WHERE slug = $1 AND traits::text ILIKE $2`, fmt.Sprint(e["monster_key"]), "%\"name\": \""+trait+"%").Scan(&n)
			return n > 0
		}
	}
	return false
}
