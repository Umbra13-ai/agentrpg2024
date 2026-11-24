package main

// An unidentified magic item (owner, Nov 24; SRD 5.2.1 "Identifying a Magic Item": handling a magic item gives a sense that it is
// extraordinary, but its properties are learned by the Identify spell, by focusing on it during a Short Rest, or, for a potion, by a taste).
//
// The item keeps its real name and fields in the inventory, so every rule that reads an item (its bonus, its charges, its spells) works as
// it does for any item; it also carries "unidentified": true and "appears_as": what the character sees ("a plain silver ring"). The GM gives
// it so (POST /api/gm/give-item's unidentified and appears_as). What a player reads of an item is this file's: playerItemName (the
// appearance while unidentified), playerItemView (the item as the player may read it: its appearance, how many, where it is, what it weighs;
// no magic field) and shownItemName (a name the character's hand, armor or attunement columns hold, as the player sees the carried item).
// The GM reads the item as it is. Every player view of an item asks these; what reads an item for a rule never does.

import (
	"net/http"
	"strings"
)

const (
	unidentifiedKey = "unidentified"
	appearsAsKey    = "appears_as"
)

// itemUnidentified: the character doesn't know what this item is.
func itemUnidentified(it invItem) bool {
	b, _ := it[unidentifiedKey].(bool)
	return b
}

// playerItemName: the item's name as its character knows it.
func playerItemName(it invItem) string {
	if itemUnidentified(it) {
		if a := strings.TrimSpace(itemStr(it, appearsAsKey)); a != "" {
			return a
		}
		return "an unidentified item"
	}
	return itemStr(it, "name")
}

// playerItemFields: what a player may read of an unidentified item besides its appearance: where and how many, its weight, its container,
// what happened to it. Every other field (magic_item, charges, casts, scroll, spell, bonus, rarity, attunement ...) tells what it is.
var playerItemFields = []string{"id", "quantity", "location", "weight", "held", "worn", "type", "container_kind", "capacity", "capacity_lbs",
	"lost", "lost_reason", "lost_at", "destroyed", "no_auto_equip", "remind_off", "note"}

// playerItemView: the item as its character's player may read it (an identified item is unchanged; the map is a copy either way).
func playerItemView(it invItem) invItem {
	out := invItem{}
	if !itemUnidentified(it) {
		for k, v := range it {
			out[k] = v
		}
		return out
	}
	for _, k := range playerItemFields {
		if v, ok := it[k]; ok {
			out[k] = v
		}
	}
	out["name"], out[unidentifiedKey] = playerItemName(it), true
	return out
}

// playerInventory: every item as the player may read it.
func playerInventory(inv []invItem) []invItem {
	out := make([]invItem, 0, len(inv))
	for _, it := range inv {
		out = append(out, playerItemView(it))
	}
	return out
}

// shownItemName: a name one of the character's item columns holds (a hand, the armor, an attunement), as its player knows it: the
// appearance of the carried item of that name while it is unidentified, else the name as held.
func shownItemName(charID int, name string) string {
	if name == "" {
		return name
	}
	return shownNameIn(inventoryOf(charID), name)
}

// shownNameIn: shownItemName over an inventory already read.
func shownNameIn(inv []invItem, name string) string {
	for _, it := range inv {
		if itemUnidentified(it) && strings.EqualFold(strings.TrimSpace(itemStr(it, "name")), strings.TrimSpace(name)) {
			return playerItemName(it)
		}
	}
	return name
}

// viewerKnowsItems: the reader sees items as they are (the character's GM); anyone else, the player and a spectator, as the player does.
func viewerKnowsItems(agentID, charID int) bool {
	return agentID > 0 && isCharacterGM(agentID, charID)
}

// readerKnowsItems: the request's reader is the character's GM (getAgentFromAuth; no sign-in reads as a player does).
func readerKnowsItems(r *http.Request, charID int) bool {
	agent, err := getAgentFromAuth(r)
	return err == nil && viewerKnowsItems(agent, charID)
}

// inventoryAsRead: a list of inventory items (as decoded from the column) as this reader may read them: as they are for the GM, else
// each through playerItemView.
func inventoryAsRead(knows bool, list []interface{}) []interface{} {
	if knows {
		return list
	}
	out := make([]interface{}, 0, len(list))
	for _, x := range list {
		if m, ok := x.(map[string]interface{}); ok {
			out = append(out, map[string]interface{}(playerItemView(invItem(m))))
			continue
		}
		out = append(out, x)
	}
	return out
}
