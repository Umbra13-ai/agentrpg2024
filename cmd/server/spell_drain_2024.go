package main

import (
	"fmt"
	"regexp"
	"strings"
)

// spell_drain_2024.go: a spell that heals its caster from the damage it dealt ("you regain Hit Points equal to half the amount of Necrotic damage
// dealt": Vampiric Touch, SRD 5.2.1). The spell's own text says which type and the share; the caster regains it from the damage actually dealt
// (after resistance), through the Hit Point owner's heal (healCharacter), after a hit at a monster or a character alike (FUNC-06).

var drainHealRe = regexp.MustCompile(`regain Hit Points equal to half the amount of (\w+) damage dealt`)

// spellDrainType is the damage type whose half the caster regains ("" for a spell that drains nothing).
func spellDrainType(spell SRDSpell) string {
	if m := drainHealRe.FindStringSubmatch(spell.Description); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// drainHeal heals the caster by half of dealt when the spell drains that type, and says so ("" when it does not apply).
func drainHeal(charID int, spell SRDSpell, dtype string, dealt int) string {
	if t := spellDrainType(spell); t == "" || t != strings.ToLower(dtype) || dealt/2 <= 0 {
		return ""
	}
	name, before, after := healCharacter(charID, dealt/2)
	if name == "" || after <= before {
		return fmt.Sprintf(" %s regains no Hit Points from the drained life.", characterNameOf(charID))
	}
	return fmt.Sprintf(" %s regains %d Hit Points from the drained life (%d → %d).", name, after-before, before, after)
}

// characterHP is a character's Hit Points now (0 when it can't be read).
func characterHP(charID int) int {
	var hp int
	db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, charID).Scan(&hp)
	return hp
}
