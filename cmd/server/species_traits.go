package main

import (
	"database/sql"
	"fmt"
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// isHalfling checks if a character is a halfling (Halfling Lucky)
func isHalfling(characterID int) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.IsHalfling(species)
}

// applyHalflingLucky implements the Halfling Lucky species trait:
// When you roll a 1 on the d20 for an attack roll, ability check, or saving throw,
// you can reroll the die and must use the new roll.
// Returns: (finalRoll, wasRerolled, originalRoll)
func applyHalflingLucky(roll int, characterID int) (int, bool, int) {
	return game.ApplyHalflingLucky(roll, isHalfling(characterID))
}

// checkHalflingBrave returns true if Halfling Brave grants advantage on this save
// Brave: You have advantage on saving throws against being frightened.
func checkHalflingBrave(characterID int, description string) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.CheckHalflingBrave(species, description)
}

// isOrc checks if a character is an Orc (Relentless Endurance)
func isOrc(characterID int) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.IsOrc(species)
}

// isGnome checks if a character is a gnome (Gnomish Cunning)
func isGnome(characterID int) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.IsGnome(species)
}

// checkGnomeCunning returns true if Gnomish Cunning grants advantage on this save
// Gnomish Cunning: Advantage on Intelligence, Wisdom and Charisma saving throws.
func checkGnomeCunning(characterID int, abilityShort string, fromMagic bool) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.CheckGnomeCunningMagic(species, abilityShort, fromMagic)
}

// isElf checks if a character is an elf (Fey Ancestry)
func isElf(characterID int) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.IsElf(species)
}

// hasFeyAncestry checks if a character has the Fey Ancestry trait
// every Elf has Fey Ancestry
func hasFeyAncestry(characterID int) bool {
	return isElf(characterID)
}

// checkFeyAncestryCharm returns true if Fey Ancestry grants advantage on this charm save
// Fey Ancestry: Advantage on saving throws against being charmed. Magic can't put you to sleep.
func checkFeyAncestryCharm(characterID int, description string) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.CheckFeyAncestryCharm(species, description)
}

// isImmuneToMagicalSleep returns true if character has Fey Ancestry (immune to magical sleep)
func isImmuneToMagicalSleep(characterID int) bool {
	return hasFeyAncestry(characterID)
}

// checkRelentlessEndurance implements the Orc Relentless Endurance species trait:
// When you are reduced to 0 HP but not killed outright, you can drop to 1 HP instead.
// You can't use this feature again until you finish a long rest.
// Returns: (newHP, wasUsed, message) - if wasUsed is true, HP should be set to 1
func checkRelentlessEndurance(characterID int, currentHP int, damage int, maxHP int) (int, bool, string) {
	// Only applies when dropping to exactly 0 or below (but not killed outright by massive damage)
	newHP := currentHP - damage
	if newHP > 0 {
		return newHP, false, ""
	}

	// Check for massive damage (instant death) - Relentless Endurance doesn't apply
	if newHP <= -maxHP {
		return 0, false, ""
	}

	// Relentless Endurance (Orc): once per Long Rest
	if !isOrc(characterID) {
		return 0, false, ""
	}

	var relentlessUsed bool
	var charName string
	err := db.QueryRow("SELECT COALESCE(relentless_endurance_used, false), name FROM characters WHERE id = $1", characterID).Scan(&relentlessUsed, &charName)
	if err != nil || relentlessUsed {
		return 0, false, ""
	}

	// Use Relentless Endurance - set HP to 1 and mark as used
	db.Exec("UPDATE characters SET relentless_endurance_used = true WHERE id = $1", characterID)

	msg := fmt.Sprintf("💪 %s's Relentless Endurance triggers! Instead of dropping to 0 HP, they drop to 1 HP. (Orc, once per Long Rest)", charName)
	return 1, true, msg
}

// checkRelentlessRage implements the Barbarian Relentless Rage class feature:
// At level 11+, if you drop to 0 HP while raging and don't die outright, you can make a DC 10 CON save.
// On a success your Hit Points change to twice your Barbarian level. DC increases by 5 each time (10, 15, 20...).
// DC resets after short or long rest.
// Returns (newHP, triggered, message)
func checkRelentlessRage(characterID int, currentHP int, damage int, maxHP int) (int, bool, string) {
	// Only applies when dropping to exactly 0 or below (but not killed outright by massive damage)
	newHP := currentHP - damage
	if newHP > 0 {
		return newHP, false, ""
	}

	// Check for massive damage (instant death) - Relentless Rage doesn't apply
	if newHP <= -maxHP {
		return 0, false, ""
	}

	// Check if character is a Barbarian level 11+ and raging
	var class string
	var level, con int
	var conditionsJSON []byte
	var relentlessUses int
	var charName string
	err := db.QueryRow(`
		SELECT class, level, con, COALESCE(conditions, '[]'), COALESCE(relentless_rage_uses, 0), name 
		FROM characters WHERE id = $1
	`, characterID).Scan(&class, &level, &con, &conditionsJSON, &relentlessUses, &charName)
	if err != nil {
		return 0, false, ""
	}

	// Must be Barbarian level 11+
	if strings.ToLower(class) != "barbarian" || level < 11 {
		return 0, false, ""
	}

	// Must be raging
	if !hasCondition(characterID, "raging") {
		return 0, false, ""
	}

	// Calculate DC: 10 + (5 * uses)
	dc := 10 + (5 * relentlessUses)

	// Make CON save
	conMod := game.Modifier(con)
	roll := game.RollDie(20)
	total := roll + conMod

	if total >= dc {
		// Success: Hit Points become twice the Barbarian level, and the next DC is 5 higher
		db.Exec("UPDATE characters SET relentless_rage_uses = relentless_rage_uses + 1 WHERE id = $1", characterID)
		nextDC := dc + 5
		newHP := 2 * level // 2024: your Hit Points change to twice your Barbarian level
		msg := fmt.Sprintf("🔥 %s's Relentless Rage triggers! Constitution save DC %d: rolled %d + %d = %d - SUCCESS! Hit Points change to %d instead of 0. (Next DC: %d)",
			charName, dc, roll, conMod, total, newHP, nextDC)
		return newHP, true, msg
	}

	// Failed the save - drop to 0 HP as normal
	msg := fmt.Sprintf("🔥 %s's Relentless Rage triggers! CON save DC %d: rolled %d + %d = %d - FAILED. Drops to 0 HP.",
		charName, dc, roll, conMod, total)
	return 0, false, msg
}

// isDwarf checks if a character is a dwarf (Dwarven Resilience)
func isDwarf(characterID int) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.IsDwarf(species)
}

// checkDwarvenResilience returns true if Dwarven Resilience grants advantage on this save
// Dwarven Resilience: Advantage on saving throws against poison, and resistance against poison damage.
func checkDwarvenResilience(characterID int, description string) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.CheckDwarvenResiliencePoison(species, description)
}

// isTiefling checks if a character is a Tiefling
func isTiefling(characterID int) bool {
	var species string
	err := db.QueryRow("SELECT COALESCE(species, '') FROM characters WHERE id = $1", characterID).Scan(&species)
	if err != nil {
		return false
	}
	return game.IsTiefling(species)
}

// getFiendishResilience returns the Fiend Warlock's chosen damage resistance type
// Returns empty string if not a Fiend Warlock level 10+ or no choice made yet
func getFiendishResilience(characterID int) string {
	var class string
	var level int
	var subclass sql.NullString
	var fiendishRes sql.NullString
	err := db.QueryRow(`
		SELECT COALESCE(class, ''), level, subclass, fiendish_resilience
		FROM characters WHERE id = $1
	`, characterID).Scan(&class, &level, &subclass, &fiendishRes)
	if err != nil {
		return ""
	}

	// Must be Warlock with Fiend subclass at level 10+
	if strings.ToLower(class) != "warlock" || !subclass.Valid || strings.ToLower(subclass.String) != "fiend" || level < 10 {
		return ""
	}

	if fiendishRes.Valid {
		return strings.ToLower(fiendishRes.String)
	}
	return ""
}

// hasFiendishResilience returns true if character has Fiendish Resilience for the given damage type
func hasFiendishResilience(characterID int, damageType string) bool {
	chosenType := getFiendishResilience(characterID)
	return chosenType != "" && strings.EqualFold(chosenType, damageType)
}

// getSpeciesSize returns the size category for a species
func getSpeciesSize(species string) string {
	return game.GetSpeciesSize(species)
}

// isMountLargeEnough checks if the mount is at least one size larger than the rider
func isMountLargeEnough(mountSize, riderSize string) bool {
	return game.IsSizeAtLeastOneLarger(mountSize, riderSize)
}
