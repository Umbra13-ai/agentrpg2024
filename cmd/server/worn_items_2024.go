package main

// Where worn items go (2024 Player's Handbook / SRD 5.2.1, "Wearing and Wielding Items" and "Multiple Items of the Same Kind"):
// boots go on the feet, gloves on the hands, hats and helmets on the head, rings on a finger, a cloak about the shoulders; armor is
// donned and a Shield strapped to the arm (equip-armor). A creature can't normally wear more than one pair of footwear, one pair of
// gloves or gauntlets, one pair of bracers, one item of headwear or one cloak (and one suit of armor and one Shield); the GM can make
// exceptions (table rule worn_items "no_limit"). Rings, necklaces, amulets, belts, robes and lenses have no such limit (attunement still
// allows three items). Moving a second item of a limited kind to "worn" is refused (F213); the sheet's worn_items shows each place.

import (
	"fmt"
	"strings"
)

// bodyPlaces: the place an item is worn, from words in its name (first match wins); limited places hold one item.
var bodyPlaces = []struct {
	Place   string
	Words   []string
	Limited bool
	Label   string
}{
	{"feet", []string{"boots", "boot of", "slippers", "shoes", "sandals"}, true, "one pair of footwear"},
	{"hands", []string{"gloves", "gauntlets"}, true, "one pair of gloves or gauntlets"},
	{"wrists", []string{"bracers"}, true, "one pair of bracers"},
	{"head", []string{"helm ", "helm,", "helmet", "hat ", "hat,", "cap ", "cap,", "headband", "circlet", "crown", "diadem"}, true, "one item of headwear"},
	{"shoulders", []string{"cloak", "cape", "mantle"}, true, "one cloak"},
	{"fingers", []string{"ring "}, false, ""},
	{"neck", []string{"amulet", "necklace", "periapt", "medallion", "pendant", "scarab", "brooch"}, false, ""},
	{"waist", []string{"belt", "girdle"}, false, ""},
	{"eyes", []string{"goggles", "eyes of", "lenses", "spectacles"}, false, ""},
	{"body", []string{"robe"}, false, ""},
}

// bodyPlaceOf: where the item is worn ("" when it isn't clothing or jewelry: a wand or arrows carried on the body) and whether one only.
func bodyPlaceOf(name string) (place string, limited bool, label string) {
	n := " " + strings.ToLower(name) + " "
	for _, p := range bodyPlaces {
		for _, w := range p.Words {
			if strings.Contains(n, " "+w) {
				return p.Place, p.Limited, p.Label
			}
		}
	}
	return "", false, ""
}

// wornConflict: why the item can't be worn now ("" when it can): another item of the same limited kind is worn.
func wornConflict(charID int, inv []invItem, it invItem) string {
	place, limited, label := bodyPlaceOf(itemStr(it, "name"))
	if !limited || tableRule(lobbyOfChar(charID), "worn_items") == "no_limit" {
		return ""
	}
	for _, x := range inv {
		if itemStr(x, "id") == itemStr(it, "id") || itemStr(x, "location") != "worn" {
			continue
		}
		if p, _, _ := bodyPlaceOf(itemStr(x, "name")); p == place {
			return fmt.Sprintf("You already wear the %s on your %s: a creature wears only %s at a time (Multiple Items of the Same Kind). Take it off first (equipment/move it to a bag or \"unsorted\"), or the GM can allow it (table rule worn_items).",
				itemStr(x, "name"), place, label)
		}
	}
	return ""
}

// wornItemsView: what the character wears, by place (armor and Shield from equip-armor), for the sheet.
func wornItemsView(charID int) map[string]interface{} {
	out := map[string]interface{}{}
	if a := equippedArmorName(charID); a != "" {
		out["armor"] = a
	}
	var shield bool
	db.QueryRow(`SELECT COALESCE(equipped_shield,false) FROM characters WHERE id = $1`, charID).Scan(&shield)
	if shield {
		out["shield"] = true
	}
	lists := map[string][]string{}
	for _, it := range inventoryOf(charID) {
		if itemStr(it, "location") != "worn" {
			continue
		}
		name := itemStr(it, "name")
		if strings.EqualFold(name, equippedArmorName(charID)) {
			continue
		}
		if p, _, _ := bodyPlaceOf(name); p != "" {
			lists[p] = append(lists[p], name)
		}
	}
	for p, l := range lists {
		out[p] = l
	}
	return out
}

func wornItemsTableRule() tableRuleDef {
	return tableRuleDef{Key: "worn_items", Title: "Worn magic items", Default: "rules", Options: []tableRuleOption{
		{"rules", "One of each kind", "A character wears only one pair of footwear, one pair of gloves or gauntlets, one pair of bracers, one item of headwear and one cloak (with one suit of armor and one Shield). Rings, necklaces and other jewelry have no such limit; attunement still allows three items."},
		{"no_limit", "The GM allows more", "The GM makes an exception to Multiple Items of the Same Kind: a character may wear more than one item of a kind."},
	}}
}

func init() { tableRuleDefs = append(tableRuleDefs, wornItemsTableRule()) }
