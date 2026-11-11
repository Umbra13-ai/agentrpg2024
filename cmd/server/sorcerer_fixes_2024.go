package main

// Fixes from Vyrrah's coverage session (level20-battle-tests/s28_vyrrah_sorcerer.py, Nov 9 2026; FINDINGS F125-F128):
//	Font of Magic          read the old class_resources column (0 points) instead of the points Metamagic spends (F125,
//	                       class_feature_handlers.go); it couldn't create a slot while all slots of that level were unspent (F128)
//	Circlet of Blasting    Scorching Ray (+5 to hit), once a dawn (F126, legendary_more_2024.go)
//	Necklace of Fireballs  a Magic action throws beads: a level 3 Fireball (DC 15), +1d6 per extra bead, at most 12d6 (F127)

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var beadsRe = regexp.MustCompile(`(\d+) beads`)

// necklaceOfFireballs: target_id / target_ids are the creatures in the 20-foot-radius Sphere; "N beads" in the description throws N.
func necklaceOfFireballs(charID int, it invItem, req itemCastReq, description string) string {
	lobby := lobbyOfChar(charID)
	left := int(itemNum(it, "beads"))
	if _, has := it["beads"]; !has {
		left = game.RollDamage("1d6+3", false)
	}
	n := 1
	if m := beadsRe.FindStringSubmatch(strings.ToLower(description)); m != nil {
		n, _ = strconv.Atoi(m[1])
	}
	if n < 1 {
		n = 1
	}
	if n > left {
		n = left
	}
	if left <= 0 {
		return "The Necklace of Fireballs has no beads left. Nothing was spent."
	}
	dice := min(8+n-1, 12)
	dmg := game.RollDamage(fmt.Sprintf("%dd6", dice), false)
	ids := append([]int{}, req.TargetIDs...)
	if req.TargetID != 0 {
		ids = append([]int{req.TargetID}, ids...)
	}
	var lines []string
	for _, id := range ids {
		if id < 0 {
			saved, _ := monsterSave(lobby, id, "dex", 15)
			d := dmg
			how := "fails its DEX save"
			if saved {
				d, how = dmg/2, "saves"
			}
			res, _ := damageMonster(lobby, id, d, "fire")
			lines = append(lines, fmt.Sprintf("%s %s. %s", monsterName(lobby, id), how, strings.TrimSpace(res)))
		} else {
			saved, _ := characterSave(id, "dex", 15)
			d := dmg
			if saved {
				d = dmg / 2
			}
			lines = append(lines, fmt.Sprintf("%s %s and %s.", characterNameOf(id), map[bool]string{true: "saves", false: "fails its DEX save"}[saved], damageCharacter(id, d, "fire")))
		}
	}
	left -= n
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				x["beads"] = left
			}
		}
		return inv
	})
	return fmt.Sprintf("You detach %d bead%s and throw %s (a Magic action): a level 3 Fireball (DC 15 Dexterity save, half on a success), %dd6 = %d Fire damage. %s %d bead%s left.",
		n, map[bool]string{true: "", false: "s"}[n == 1], map[bool]string{true: "it", false: "them"}[n == 1], dice, dmg, strings.Join(lines, " "), left, map[bool]string{true: "", false: "s"}[left == 1])
}

// materialAlternative: a material given as a choice ("either a jeweled horn for hearing or a glass eye for seeing") is met by an item
// named after any of the choices (F129: only the first keyword found counted).
func materialAlternative(material, itemName string) bool {
	if !strings.Contains(material, " or ") {
		return false
	}
	for _, w := range strings.Fields(itemName) {
		if len(w) > 3 && strings.Contains(material, w) {
			return true
		}
	}
	return false
}
