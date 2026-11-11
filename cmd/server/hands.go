package main

// A character has two hands. A two-handed weapon takes both; a shield takes
// one; a main-hand weapon and an off-hand weapon take one each. Nothing may be
// equipped that would need more than two hands.
// In combat a character may equip or unequip one weapon per turn, and it is not
// an action. Switching weapons and still attacking means dropping the weapon in
// hand first (see dropped_items.go).

import (
	"fmt"
	"strings"
)

// isTwoHandedWeapon reports whether a weapon needs both hands (Versatile
// weapons are one-handed here).
func isTwoHandedWeapon(name string) bool {
	if name == "" {
		return false
	}
	_, w, ok := findWeapon(name)
	if !ok { // a magic weapon's base weapon (F215: a Shield went on beside a Sword of Sharpness (Greatsword))
		_, w, ok = findWeapon(strings.ReplaceAll(mapWeaponKey(0, name), "_", " "))
	}
	return ok && hasProperty(w.Properties, "two-handed")
}

// handsAllow checks that the equipped weapons and shield fit in two hands.
func handsAllow(mainHand, offHand string, shield bool) (bool, string) {
	if offHand != "" && isTwoHandedWeapon(offHand) {
		return false, "A two-handed weapon needs both hands, so it can only be equipped in the main hand."
	}
	var held []string
	hands := 0
	if mainHand != "" {
		hands++
		held = append(held, mainHand)
		if isTwoHandedWeapon(mainHand) {
			hands++
			held[len(held)-1] += " (two-handed)"
		}
	}
	if offHand != "" {
		hands++
		held = append(held, offHand)
	}
	if shield {
		hands++
		held = append(held, "a shield")
	}
	if hands > 2 {
		return false, fmt.Sprintf("You only have two hands. That would need %d: %s. Drop or put away something first.", hands, strings.Join(held, ", "))
	}
	return true, ""
}

// shieldInHand reports whether the character has a shield equipped.
func shieldInHand(charID int) bool {
	var shield bool
	db.QueryRow(`SELECT COALESCE(equipped_shield, false) FROM characters WHERE id = $1`, charID).Scan(&shield)
	return shield
}

// weaponInteractionUsed reports whether the character already equipped or
// unequipped a weapon this turn.
func weaponInteractionUsed(charID int) bool {
	var used bool
	db.QueryRow(`SELECT COALESCE(weapon_interaction_used, false) FROM characters WHERE id = $1`, charID).Scan(&used)
	return used
}

func markWeaponInteraction(charID int) {
	db.Exec(`UPDATE characters SET weapon_interaction_used = true WHERE id = $1`, charID)
}

// equipRule decides whether an equip may go ahead in combat.
//
//	handOccupied: the hand being filled already holds a different weapon
//	interactionUsed: a weapon was already equipped or unequipped this turn
func equipRule(handOccupied, interactionUsed bool) (ok bool, errCode string) {
	if handOccupied {
		return false, "hand_not_empty"
	}
	if interactionUsed {
		return false, "already_equipped_or_unequipped_this_turn"
	}
	return true, ""
}

const (
	msgHandNotEmpty = "That hand is already holding a weapon. You can equip or unequip only one weapon per turn, and it is not an action, so you cannot put the old one away and draw a new one and still attack. " +
		"To switch weapons you must drop the weapon in your hand first (it falls to the ground where anyone can pick it up, or you may forget it), then equip the new one: " +
		"POST /api/characters/{id}/equipment/drop, then POST /api/characters/equip-weapon."
	msgAlreadyUsed = "You have already equipped or unequipped a weapon this turn. You may do that once per turn, and it is not an action. Wait for your next turn, or drop what you are holding (dropping is free)."
)
