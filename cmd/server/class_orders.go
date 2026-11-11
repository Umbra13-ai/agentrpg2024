package main

// Divine Order (Cleric) and Primal Order (Druid), 2024 rules (SRD 5.2.1).
// Thaumaturge / Magician: a bonus to Intelligence (Arcana or Religion / Arcana or Nature) checks equal to the Wisdom modifier (minimum +1)
// and one extra cantrip. Protector / Warden proficiencies are applied in proficiency_sync.go.

import (
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// orderExtraCantrips: Thaumaturge (Cleric) and Magician (Druid) each know one extra cantrip from their class list.
func orderExtraCantrips(charID int) int {
	var class, divine, primal string
	if db.QueryRow(`SELECT COALESCE(class,''), COALESCE(divine_order,''), COALESCE(primal_order,'') FROM characters WHERE id = $1`, charID).Scan(&class, &divine, &primal) != nil {
		return 0
	}
	class = strings.ToLower(class)
	if (class == "cleric" && divine == "thaumaturge") || (class == "druid" && primal == "magician") {
		return 1
	}
	return 0
}

// orderSkillBonus is the Thaumaturge or Magician bonus for the skill being rolled (0 when it does not apply).
func orderSkillBonus(charID int, skill string, wis int) int {
	skill = strings.ToLower(skill)
	if skill != "arcana" && skill != "religion" && skill != "nature" {
		return 0
	}
	var class, divine, primal string
	if db.QueryRow(`SELECT COALESCE(class,''), COALESCE(divine_order,''), COALESCE(primal_order,'') FROM characters WHERE id = $1`, charID).Scan(&class, &divine, &primal) != nil {
		return 0
	}
	class = strings.ToLower(class)
	ok := (class == "cleric" && divine == "thaumaturge" && (skill == "arcana" || skill == "religion")) ||
		(class == "druid" && primal == "magician" && (skill == "arcana" || skill == "nature"))
	if !ok {
		return 0
	}
	if m := game.Modifier(wis); m > 1 {
		return m
	}
	return 1
}

