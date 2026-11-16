package main

// A character can only attack with a weapon they have equipped (in their main
// hand or off hand). A weapon in a bag, or sheathed on the belt, must be
// equipped first. Equipping moves the weapon to "held" in the equipment list.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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

// changeHands is the one writer of the equipped hands (equipped_main_hand, equipped_off_hand): it runs the statement, then
// syncHeldItems, so the inventory's "held" marks always match the hands (playtest I-ROWAN-7: a hand emptied without the sync
// left the weapon "held" in the inventory and the shop). TestNoOtherHandWriter checks.
func changeHands(charID int, query string, args ...interface{}) (sql.Result, error) {
	res, err := db.Exec(query, args...)
	if err == nil {
		syncHeldItems(charID)
	}
	return res, err
}

// repairHeldMarks: at each start, every character whose stored "held" marks disagree with its hands is brought in line through
// the inventory writer (syncHeldItems). Characters saved before the writer synced the hands kept a weapon "held" in an empty hand
// until something rewrote their inventory, so the equipment list, the sheet and the shop still disagreed (playtest I-ROWAN-7, retested
// still broken). Only characters with a held mark or a weapon in hand are read; one already in line is not written.
func repairHeldMarks() {
	rows, err := db.Query(`SELECT id, COALESCE(inventory, '[]'), COALESCE(equipped_main_hand, ''), COALESCE(equipped_off_hand, '') FROM characters
		WHERE COALESCE(inventory::text, '') LIKE '%"held"%' OR COALESCE(equipped_main_hand, '') <> '' OR COALESCE(equipped_off_hand, '') <> ''`)
	if err != nil {
		log.Printf("held marks: couldn't read the characters: %v", err)
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		var raw []byte
		var main, off string
		if rows.Scan(&id, &raw, &main, &off) != nil {
			continue
		}
		var inv []invItem
		if json.Unmarshal(raw, &inv) == nil && syncHands(inv, main, off) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		syncHeldItems(id)
	}
	if len(ids) > 0 {
		log.Printf("held marks: %d character(s) brought in line with their hands", len(ids))
	}
}

// syncHeldItems keeps the equipment list in step with the equipped weapon fields:
// a weapon equipped in a hand is "held"; a weapon that was held and no longer is
// goes back to "worn" (sheathed). Items in a lost bag are left alone.
func syncHeldItems(charID int) {
	changeInventory(charID, func(_ *sql.Tx, inv []invItem) ([]invItem, error) { return inv, nil }) // every save syncs the hands (inventory_2024.go)
}

// syncHands marks the items in the equipped hands held and sheathes the weapons no longer in hand; it says whether anything moved.
func syncHands(inv []invItem, main, off string) bool {
	isLostItem := lostChecker(inv)
	changed := false
	handled := map[string]bool{} // one inventory item per hand
	for _, hand := range []string{main, off} {
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
	return changed
}
