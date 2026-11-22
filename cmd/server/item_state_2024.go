package main

// The state of one item a character carries has one owner (this file): a corrosion penalty kept on the item itself in the
// inventory ("corrosion": 1, 2 ...), written only through the inventory writer (changeInventory, inventory_2024.go), read by the
// weapon attack bonus (weaponAttackMods: -1 to attack rolls for each point) and the Armor Class (characterACWith: -1 to the AC the
// armor offers for each point). SRD 5.2.1:
//   - Gray Ooze Pseudopod, Black Pudding Dissolving Pseudopod (a hit): "Nonmagical armor worn by the target takes a -1 penalty to the
//     AC it offers. The armor is destroyed if the penalty reduces its AC to 10." (armorOnHitRe, through hitAfterDamage);
//   - Gray Ooze and Black Pudding Corrosive Form (a trait the attacker meets): "Nonmagical ammunition is destroyed immediately after
//     hitting the ooze and dealing any damage. Any nonmagical weapon takes a cumulative -1 penalty to attack rolls immediately after
//     dealing damage to the ooze and coming into contact with it. The weapon is destroyed if the penalty reaches -5."
//     (corrodeOnContact, a stage of a character's hit: runCharacterHitTail);
//   - Rust Monster Antennae (a saving throw effect): "The object takes a -1 penalty to the AC it offers (armor) or to its attack rolls
//     (weapon). Armor is destroyed if the penalty reduces its AC to 10, and a weapon is destroyed if its penalty reaches -5."
//     (saveEffect.ItemPenalty: the creature's nonmagical metal armor, else the nonmagical metal weapon in its hand);
//   - "The penalty can be removed by casting the Mending spell on the armor or weapon." (mendItem).
// Destroyed is gone from the inventory (the writer ends an attunement and frees the hands; worn armor comes off). Only a magic item is
// spared ("nonmagical"); the Rust Monster also spares what isn't metal. A Rust Monster's Destroy Metal touches an object nobody wears
// or carries: no item of a character, so it stays the GM's.

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var (
	armorOnHitRe       = regexp.MustCompile(`(?i)\s*Nonmagical armor worn by the target takes a -1 penalty to the AC it offers\.(?:\s*The armor is destroyed if the penalty reduces its AC to 10\.)?(?:\s*The penalty can be removed by casting the Mending spell on the armor\.)?`)
	objectOnFailureRe  = regexp.MustCompile(`(?i)\s*The object takes a -1 penalty to the AC it offers \(armor\) or to its attack rolls \(weapon\)\.(?:\s*Armor is destroyed if the penalty reduces its AC to 10, and a weapon is destroyed if its penalty reaches -5\.)?(?:\s*The penalty can be removed by casting the Mending spell on the armor or weapon\.)?`)
	corrosiveWeaponRe  = regexp.MustCompile(`(?i)Any nonmagical weapon takes a cumulative -1 penalty to attack rolls immediately after dealing damage to the [\w ]+? and coming into contact with it\.`)
	corrosiveAmmoRe    = regexp.MustCompile(`(?i)Nonmagical ammunition is destroyed immediately after hitting the [\w ]+? and dealing any damage\.`)
	metalObjectOnlyRe  = regexp.MustCompile(`(?i)\bnonmagical metal object\b`)
	weaponDestroyedAt  = 5  // "a weapon is destroyed if its penalty reaches -5"
	armorDestroyedAtAC = 10 // "destroyed if the penalty reduces its AC to 10"
)

// nonMetalWeapons: the SRD 5.2.1 weapons with no metal to rust (the Rust Monster: "one nonmagical metal object").
var nonMetalWeapons = map[string]bool{"club": true, "greatclub": true, "quarterstaff": true, "sling": true, "whip": true,
	"shortbow": true, "longbow": true, "blowgun": true}

// metalArmor: the SRD 5.2.1 armor of metal (the leathers, padded and hide are not).
var metalArmor = map[string]bool{"chain-shirt": true, "scale-mail": true, "breastplate": true, "half-plate-armor": true,
	"ring-mail": true, "chain-mail": true, "splint-armor": true, "plate-armor": true}

// itemIsMagic: a magic item (+N, a named magic weapon or armor, an SRD magic item): corrosion spares it.
func itemIsMagic(name string) bool {
	return itemPlus(name) > 0 || namedWeaponFor(name) != nil || isSRDMagicItem(name) || isSRDMagicItem(baseItemName(name))
}

// itemCorrosion: the item's corrosion penalty (0: none).
func itemCorrosion(it invItem) int {
	if it == nil {
		return 0
	}
	return int(itemNum(it, "corrosion"))
}

// armorKindOf: the AC a body armor offers by its kind and its slug in the armor table (0, "": not body armor).
func armorKindOf(name string) (int, string) {
	slugs := []string{}
	if slug, _ := magicArmorBase(name); slug != "" {
		slugs = append(slugs, slug)
	}
	base := game.Slug(stripPlus(name))
	slugs = append(slugs, base, base+"-armor")
	for _, s := range slugs {
		if a, err := getArmorInfo(s); err == nil && a != nil && !strings.EqualFold(a.Type, "shield") {
			return a.AC, s
		}
	}
	return 0, ""
}

// armorCorrosionOf: the corrosion penalty of the armor the character wears ("" armor: none).
func armorCorrosionOf(charID int, armor string) int {
	if armor == "" || !strings.EqualFold(armor, equippedArmorName(charID)) {
		return 0
	}
	return itemCorrosion(corrodibleItem(inventoryOf(charID), armor))
}

// weaponCorrosionOf: the corrosion penalty of the held weapon an attack with weaponKey uses.
func weaponCorrosionOf(charID int, weaponKey string, offHand bool) int {
	if item := heldItemFor(charID, weaponKey, offHand); item != "" {
		return itemCorrosion(corrodibleItem(inventoryOf(charID), item))
	}
	return 0
}

// corrodibleItem: of the items with this name (a second Leather Armor in the pack), the one worn or held, else the first; nil for none.
// Corrosion is written on it and read from it, so the item that corroded is the one that counts.
func corrodibleItem(inv []invItem, name string) invItem {
	if it, err := findItem(inv, name); err == nil {
		return it
	}
	var first invItem
	for _, it := range inv {
		if normalizeRef(itemStr(it, "name")) != normalizeRef(name) {
			continue
		}
		if loc := itemStr(it, "location"); loc == "worn" || loc == "held" {
			return it
		}
		if first == nil {
			first = it
		}
	}
	return first
}

// corrodeItem adds 1 to an item's corrosion through the inventory writer; at its limit the item is destroyed (removed). kind is
// "armor" or "weapon". It answers the penalty now and whether the item is gone.
func corrodeItem(charID int, name, kind string) (penalty int, destroyed bool, err error) {
	baseAC, _ := armorKindOf(name)
	_, err = changeInventory(charID, func(_ *sql.Tx, inv []invItem) ([]invItem, error) {
		it := corrodibleItem(inv, name)
		if it == nil {
			return nil, codeRefusal("item_not_carried")
		}
		penalty = itemCorrosion(it) + 1
		switch kind {
		case "armor":
			destroyed = baseAC > 0 && baseAC-penalty <= armorDestroyedAtAC
		default:
			destroyed = penalty >= weaponDestroyedAt
		}
		if destroyed {
			return removeBundle(inv, itemStr(it, "id")), nil
		}
		it["corrosion"] = float64(penalty)
		return inv, nil
	})
	if err != nil {
		return 0, false, err
	}
	if kind == "armor" {
		if destroyed { // worn armor that is gone comes off
			db.Exec(`UPDATE characters SET equipped_armor = NULL WHERE id = $1 AND equipped_armor = $2`, charID, name)
		}
		refreshStoredAC(charID) // multiclass_2024.go
	}
	return penalty, destroyed, nil
}

// corrosionLine: what a reply says of a corroded item.
func corrosionLine(owner, name, kind string, penalty int, destroyed bool) string {
	what := map[string]string{"armor": "to the AC it offers", "weapon": "to attack rolls"}[kind]
	if destroyed {
		return fmt.Sprintf(" [%s's %s is destroyed by the corrosion]", owner, name)
	}
	return fmt.Sprintf(" [%s's %s corrodes: -%d %s; Mending removes it]", owner, name, penalty, what)
}

// stripItemPenalties removes the sentences this owner applies from a residual for the GM.
func stripItemPenalties(text string) string {
	text = objectOnFailureRe.ReplaceAllString(armorOnHitRe.ReplaceAllString(text, ""), "")
	return mendingRemovesRe.ReplaceAllString(text, "")
}

// mendingRemovesRe: "The penalty can be removed by casting the Mending spell on the armor." on its own (mendItem applies it).
var mendingRemovesRe = regexp.MustCompile(`(?i)\s*The penalty can be removed by casting the Mending spell on the (?:armor or weapon|armor|weapon)\.?`)

// corrodeArmorOnHit: a hit whose text says "Nonmagical armor worn by the target takes a -1 penalty to the AC it offers" corrodes the
// character target's worn nonmagical armor (a monster's or companion's armor is part of its stat block: no item).
func corrodeArmorOnHit(lobby, target int, desc string) string {
	if !armorOnHitRe.MatchString(hitDamageText(desc)) || target <= 0 || isCompanionCombatID(target) {
		return ""
	}
	armor := equippedArmorName(target)
	if armor == "" || itemIsMagic(armor) {
		return ""
	}
	p, gone, err := corrodeItem(target, armor, "armor")
	if err != nil {
		return ""
	}
	return corrosionLine(creatureName(lobby, target), armor, "armor", p, gone)
}

// corrodeOnContact: a character's weapon hit dealt damage to a monster whose trait corrodes what touches it (Corrosive Form): the
// nonmagical ammunition is destroyed (not recovered after the fight), a nonmagical weapon that touched it takes the penalty.
func corrodeOnContact(lobby, charID, monster int, weaponKey string, damage int) string {
	if damage <= 0 || weaponKey == "" || charID <= 0 {
		return ""
	}
	_, meta, ok := monsterCombatant(lobby, monster)
	if !ok || meta == nil {
		return ""
	}
	var weaponRule, ammoRule bool
	for _, tr := range meta.Traits {
		weaponRule = weaponRule || corrosiveWeaponRe.MatchString(tr.Text)
		ammoRule = ammoRule || corrosiveAmmoRe.MatchString(tr.Text)
	}
	_, w, found := findWeapon(weaponKey)
	if !found || (!weaponRule && !ammoRule) {
		return ""
	}
	owner := creatureName(lobby, charID)
	if hasProperty(w.Properties, "ammunition") { // the ammunition touched it, not the bow
		if !ammoRule || wantsMagicAmmo(charID) {
			return ""
		}
		changeInventory(charID, func(tx *sql.Tx, inv []invItem) ([]invItem, error) { // the piece fired is gone, never recovered
			_, err := tx.Exec(`UPDATE characters SET ammo_used_since_rest = GREATEST(0, COALESCE(ammo_used_since_rest,0) - 1) WHERE id = $1`, charID)
			return inv, err
		})
		return fmt.Sprintf(" %s's ammunition is destroyed by the %s.", owner, meta.Name)
	}
	item := heldItemFor(charID, weaponKey, false)
	if !weaponRule || item == "" || itemIsMagic(item) {
		return ""
	}
	p, gone, err := corrodeItem(charID, item, "weapon")
	if err != nil {
		return ""
	}
	return corrosionLine(owner, item, "weapon", p, gone)
}

// corrodeCarriedObject: a failed save against "The object takes a -1 penalty to the AC it offers (armor) or to its attack rolls
// (weapon)": the creature's worn nonmagical armor (metal when the effect says so), else the nonmagical weapon in its main hand.
func corrodeCarriedObject(lobby, charID int, metalOnly bool) string {
	if charID <= 0 || isCompanionCombatID(charID) {
		return ""
	}
	owner := creatureName(lobby, charID)
	if armor := equippedArmorName(charID); armor != "" && !itemIsMagic(armor) {
		if _, slug := armorKindOf(armor); !metalOnly || metalArmor[slug] {
			if p, gone, err := corrodeItem(charID, armor, "armor"); err == nil {
				return corrosionLine(owner, armor, "armor", p, gone)
			}
		}
	}
	main, _ := equippedWeapons(charID)
	if main == "" || itemIsMagic(main) {
		return fmt.Sprintf(" %s carries no nonmagical object it could corrode.", owner)
	}
	if key, _, ok := findWeapon(main); ok && metalOnly && nonMetalWeapons[strings.ToLower(key)] {
		return fmt.Sprintf(" %s's %s has no metal to corrode.", owner, main)
	}
	p, gone, err := corrodeItem(charID, main, "weapon")
	if err != nil {
		return ""
	}
	return corrosionLine(owner, main, "weapon", p, gone)
}

// itemRepairSpells: the spells whose cast repairs an item the character carries ("The penalty can be removed by casting the Mending
// spell on the armor or weapon").
var itemRepairSpells = map[string]bool{"mending": true}

// mendItem: the Mending spell removes the corrosion of the item the description names (or the only corroded item). It answers the
// line for the cast, "" when nothing it carries is corroded.
func mendItem(charID int, description string) string {
	d := strings.ToLower(description)
	var target string
	var corroded []string
	for _, it := range inventoryOf(charID) {
		if itemCorrosion(it) == 0 {
			continue
		}
		name := itemStr(it, "name")
		corroded = append(corroded, name)
		if strings.Contains(d, strings.ToLower(name)) {
			target = name
		}
	}
	if target == "" && len(corroded) == 1 {
		target = corroded[0]
	}
	if target == "" {
		if len(corroded) > 1 {
			return " Mending: name the item to repair (" + strings.Join(corroded, ", ") + ")."
		}
		return ""
	}
	if _, err := changeInventory(charID, func(_ *sql.Tx, inv []invItem) ([]invItem, error) {
		var it invItem
		for _, x := range inv { // the corroded copy of that name
			if normalizeRef(itemStr(x, "name")) == normalizeRef(target) && itemCorrosion(x) > 0 {
				it = x
			}
		}
		if it == nil {
			return nil, codeRefusal("item_not_carried")
		}
		delete(it, "corrosion")
		return inv, nil
	}); err != nil {
		return ""
	}
	refreshStoredAC(charID)
	return " Mending: the corrosion on the " + target + " is gone."
}
