package main

// The armor rules of the SRD 5.2.1, applied:
//   - Armor Training: wearing Light, Medium or Heavy armor without training gives Disadvantage on
//     any D20 Test that involves Strength or Dexterity and means no spellcasting
//     (isWearingNonProficientArmor); a Shield gives its Armor Class only with training.
//   - Strength: armor with a Strength score in the table reduces Speed by 10 feet unless the
//     wearer's Strength is at least that score.
//   - Stealth: armor marked Disadvantage gives Disadvantage on Dexterity (Stealth) checks.
//   - One suit of armor and one Shield at a time.

import (
	"database/sql"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// shieldTrained says whether the armor proficiencies include Shields.
func shieldTrained(profs string) bool { return strings.Contains(strings.ToLower(profs), "shield") }

// shieldCounts reports whether a character's shield actually adds to Armor Class: it must be
// carried and the character must have training with it.
func shieldCounts(charID int, shieldEquipped bool) bool {
	if !shieldEquipped {
		return false
	}
	var profs string
	db.QueryRow(`SELECT COALESCE(armor_proficiencies, '') FROM characters WHERE id = $1`, charID).Scan(&profs)
	return shieldTrained(profs)
}

// strengthSpeedPenalty is 10 when the wearer's Strength is below the armor's requirement.
func strengthSpeedPenalty(strength, required int) int {
	if required > 0 && strength < required {
		return 10
	}
	return 0
}

// armorSpeedPenalty is the Speed lost to the armor a character wears.
func armorSpeedPenalty(charID int) int {
	var armor sql.NullString
	var str int
	if db.QueryRow(`SELECT equipped_armor, str FROM characters WHERE id = $1`, charID).Scan(&armor, &str) != nil || !armor.Valid || armor.String == "" {
		return 0
	}
	info, err := getArmorInfo(armor.String)
	if err != nil || info == nil {
		return 0
	}
	return strengthSpeedPenalty(str, info.StrengthRequirement)
}

// armorStealthDisadvantage says whether the worn armor gives Disadvantage on Dexterity (Stealth) checks.
func armorStealthDisadvantage(charID int) bool {
	var armor sql.NullString
	if db.QueryRow(`SELECT equipped_armor FROM characters WHERE id = $1`, charID).Scan(&armor) != nil || !armor.Valid {
		return false
	}
	return getArmorStealthDisadvantage(armor.String)
}

// characterSpeed is a character's Speed in feet: the species' Speed (a Wood Elf has 35), +10 while
// Large Form is active, minus 10 for armor whose Strength requirement is not met.
func characterSpeed(charID int, species string) int {
	// in Wild Shape the beast's Speed replaces the druid's (its game statistics are the stat block's; wild_shape_2024.go)
	var beastSpeed int
	if db.QueryRow(`SELECT COALESCE(m.speed,0) FROM characters c JOIN monsters m ON m.slug = c.wild_shape_form WHERE c.id = $1`, charID).Scan(&beastSpeed) == nil && beastSpeed > 0 {
		return beastSpeed
	}
	speed := getMovementSpeed(species)
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw) == nil {
		if rec, ok := loadOriginRecord(raw); ok {
			speed = rec.Species.Speed
		}
	}
	if speciesEffectActive(charID, "large_form") {
		speed += 10
	}
	speed += classSpeedBonus(charID)
	speed += buffSpeedBonus(charID) // Longstrider (buff_spells_2024.go)
	speed -= armorSpeedPenalty(charID)
	speed -= game.ExhaustionSpeedPenalty(exhaustionLevelOf(charID))
	speed = moreSpeed(charID, speed) // Fly's 60 feet; Gaseous Form's 10 (spells_more_2024.go)
	if speed < 0 {
		speed = 0
	}
	return speed
}
