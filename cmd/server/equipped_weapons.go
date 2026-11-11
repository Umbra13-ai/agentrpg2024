package main

// A character can only attack with a weapon they have equipped (in their main
// hand or off hand). A weapon in a bag, or sheathed on the belt, must be
// equipped first. Equipping moves the weapon to "held" in the equipment list.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// sameWeapon reports whether two weapon names or keys mean the same weapon
// ("crossbow-light", "Light Crossbow" and "light_crossbow" all match).
func sameWeapon(a, b string) bool {
	canon := func(s string) string {
		s = strings.TrimSpace(plusRe.ReplaceAllString(s, "")) // a magic weapon: "Greataxe +3" is a greataxe (magic_items_2024.go)
		if _, w, ok := findWeapon(s); ok {
			return weaponNameKey(w.Name)
		}
		return weaponNameKey(s)
	}
	ka, kb := canon(a), canon(b)
	return ka != "" && ka == kb
}

// equippedWeapons returns the weapons in the character's main hand and off hand.
func equippedWeapons(charID int) (mainHand, offHand string) {
	var m, o sql.NullString
	db.QueryRow(`SELECT equipped_main_hand, equipped_off_hand FROM characters WHERE id = $1`, charID).Scan(&m, &o)
	return m.String, o.String
}

// weaponEquippedForChar reports whether the weapon is equipped in either hand.
func weaponEquippedForChar(charID int, weapon string) bool {
	m, o := equippedWeapons(charID)
	return heldMatches(charID, m, weapon) || heldMatches(charID, o, weapon) // a named magic weapon is its base weapon (named_weapons_2024.go)
}

// weaponEquippedInOffHand reports whether the weapon is the one in the off hand.
func weaponEquippedInOffHand(charID int, weapon string) bool {
	_, o := equippedWeapons(charID)
	return heldMatches(charID, o, weapon)
}

// notEquippedMessage is what a player is told when they name a weapon they have not equipped.
func notEquippedMessage(weapon string) string {
	name := weapon
	if _, w, ok := findWeapon(weapon); ok {
		name = w.Name
	}
	return fmt.Sprintf("You can't attack with your %s: it isn't equipped. Only equipped weapons can be used to attack. "+
		"Equip it first with POST /api/characters/equip-weapon (a weapon in a bag or on your belt must be equipped before you attack).", name)
}

// syncHeldItems keeps the equipment list in step with the equipped weapon fields:
// a weapon equipped in a hand is "held"; a weapon that was held and no longer is
// goes back to "worn" (sheathed). Items in a lost bag are left alone.
func syncHeldItems(charID int) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	inv, err := loadInventoryTx(tx, charID)
	if err != nil {
		return
	}
	var m, o sql.NullString
	if tx.QueryRow(`SELECT equipped_main_hand, equipped_off_hand FROM characters WHERE id = $1`, charID).Scan(&m, &o) != nil {
		return
	}
	isLostItem := lostChecker(inv)
	changed := false
	handled := map[string]bool{} // one inventory item per hand
	for _, hand := range []string{m.String, o.String} {
		if hand == "" {
			continue
		}
		for _, it := range inv {
			if isContainer(it) || isLostItem(it) || handled[itemStr(it, "id")] {
				continue
			}
			if !strings.EqualFold(itemStr(it, "name"), hand) { // the item named in the hand: a Staff of Power is held too (F118)
				if _, _, isWeapon := findWeapon(itemStr(it, "name")); !isWeapon || !sameWeapon(itemStr(it, "name"), hand) {
					continue
				}
			}
			if itemStr(it, "location") != "held" {
				it["location"] = "held"
				changed = true
			}
			handled[itemStr(it, "id")] = true
			break
		}
	}
	for _, it := range inv {
		if isContainer(it) || itemStr(it, "location") != "held" || handled[itemStr(it, "id")] {
			continue
		}
		if _, _, isWeapon := findWeapon(itemStr(it, "name")); isWeapon || namedWeaponFor(itemStr(it, "name")) != nil { // a named magic weapon too (F174)
			it["location"] = "worn" // no longer in hand: sheathed
			changed = true
		}
	}
	if changed {
		if b, err := json.Marshal(inv); err == nil {
			tx.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, string(b), charID)
			tx.Commit()
		}
	}
	_ = strings.TrimSpace
}
