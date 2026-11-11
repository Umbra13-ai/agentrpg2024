package main

// Mage Armor (SRD 5.2.1, level 1 Abjuration, 8 hours, no Concentration): "You touch a willing creature who isn't wearing armor.
// Until the spell ends, the target's base AC becomes 13 plus its Dexterity modifier. The spell ends early if the target dons armor."
// A Warlock with Armor of Shadows casts it on themself at will (actions.go). F65: the spell used to change nothing.
//   - The cast puts a "mage_armor" effect on the target (8 hours = 4,800 rounds, so a Long Rest ends it) and refreshes its AC.
//   - computeCharacterAC uses 13 as the unarmored base when that beats the character's own (only one base AC calculation applies:
//     Unarmored Defense, Draconic Resilience or the Robe of the Archmagi can be better).
//   - Donning armor ends the spell; when it runs out, the AC goes back (endItemEffect).

import (
	"fmt"
	"strings"
)

const mageArmorRounds = 4800 // 8 hours

// mageArmorCast resolves a cast of Mage Armor on the target named in the request or the description (the caster by default).
func mageArmorCast(charID, lobby int, spell SRDSpell, description string) string {
	id := charID
	if ids := spellCharacterTargets(charID); len(ids) > 0 {
		id = ids[0]
	} else if t := parseTargetFromDescription(description, charID); t > 0 {
		id = t
	}
	if id < 0 {
		return " Mage Armor: the GM applies it to the creature's Armor Class (13 + its Dexterity modifier while it wears no armor)."
	}
	name := creatureName(lobby, id)
	var armor string
	db.QueryRow(`SELECT COALESCE(equipped_armor,'') FROM characters WHERE id = $1`, id).Scan(&armor)
	if strings.TrimSpace(armor) != "" {
		return fmt.Sprintf(" Mage Armor: %s is wearing armor (%s), so the spell has no effect (it targets a creature who isn't wearing armor).", name, armor)
	}
	before := computeCharacterAC(id)
	addSpellEffect(lobby, id, charID, spell.Name, "mage_armor", "", mageArmorRounds, "", 0)
	refreshStoredAC(id)
	after := computeCharacterAC(id)
	if after <= before {
		return fmt.Sprintf(" Mage Armor: %s's base Armor Class could become 13 + its Dexterity modifier for 8 hours, but its own is already as good (AC %d).", name, after)
	}
	return fmt.Sprintf(" Mage Armor: %s's base Armor Class becomes 13 + its Dexterity modifier for 8 hours (AC %d, was %d). The spell ends early if %s dons armor.", name, after, before, name)
}

// mageArmorBase gives the unarmored base AC (the number the Dexterity modifier is added to) under Mage Armor. Wearing armor ends
// the spell.
func mageArmorBase(charID int, armor string, natural int) int {
	if db == nil {
		return natural
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE target_id = $1 AND kind = 'mage_armor'`, charID).Scan(&n)
	if n == 0 {
		return natural
	}
	if strings.TrimSpace(armor) != "" {
		db.Exec(`DELETE FROM spell_effects WHERE target_id = $1 AND kind = 'mage_armor'`, charID) // the target donned armor
		return natural
	}
	return maxInt(natural, 13)
}
