package main

import (
	"fmt"
	"strings"
)

// spell_created_items_2024.go: the items a spell creates (SRD 5.2.1 Goodberry: "Ten berries appear in your hand and are infused with magic
// for the duration. A creature can take a Bonus Action to eat one berry. Eating a berry restores 1 Hit Point ... Uneaten berries disappear
// when the spell ends."). The tester's run (Nov 23) found the cast created nothing. A spell that creates items is one row of spellItems:
// the cast (spells_more_2024.go) puts them in the caster's inventory through the inventory writer, each carrying the cast's token (the
// detail of its spell effect, which the story clock runs out: passSpellTimeOn); a creature uses one with use_item (a Bonus Action, as a
// potion: isPotion reads spellItemOf), healed through the Hit Point owner; an item whose spell has ended is gone (spellItemLive, swept by
// sweepSpellItems at every use and when the clock moves). Go: TestPostgresGoodberryMakesBerries.

type spellItem struct {
	Name     string
	Plural   string
	Quantity int
	Each     string // what one does, for the cast's reply
	Heal     int    // Hit Points one restores when eaten or drunk
	Use      string // what using one does, for the reply
}

var spellItems = map[string]spellItem{
	"goodberry": {Name: "Goodberry", Plural: "Goodberries", Quantity: 10, Heal: 1, Each: "eating one restores 1 Hit Point and is a day's nourishment",
		Use: "eats a Goodberry: 1 Hit Point restored, and a day's nourishment"},
}

// spellItemOf: the row of the spell that made the inventory item, if a spell made it.
func spellItemOf(it invItem) (spellItem, bool) {
	s, ok := spellItems[itemStr(it, "spell_item")]
	return s, ok
}

// createSpellItems puts the spell's items in the caster's inventory, marked with the cast's token (the spell effect's detail).
func createSpellItems(charID int, spellKey, token string) string {
	row, ok := spellItems[spellKey]
	if !ok {
		return ""
	}
	editInventory(charID, func(inv []invItem) []invItem {
		return append(inv, invItem{"id": newItemID("si"), "name": row.Name, "quantity": row.Quantity, "type": "consumable",
			"spell_item": spellKey, "spell_token": token})
	})
	return fmt.Sprintf("%d %s are in your inventory: %s, a Bonus Action ({\"action\": \"use_item\", \"item\": \"%s\"}; give one to a character within 5 feet with target_character_id). They disappear when the spell ends.",
		row.Quantity, row.Plural, row.Each, row.Name)
}

// spellItemLive: the spell that made the item still lasts (its effect with the cast's token). An item made outside a campaign has no
// token and no clock: it lasts.
func spellItemLive(it invItem) bool {
	token := itemStr(it, "spell_token")
	if token == "" || db == nil {
		return true
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE kind = $1 AND detail = $2`, spellItemKind(itemStr(it, "spell_item")), token).Scan(&n)
	return n > 0
}

func spellItemKind(spellKey string) string { return strings.ReplaceAll(spellKey, "-", "_") }

// sweepSpellItems removes from a character's inventory the items whose spell has ended; it says how many went.
func sweepSpellItems(charID int) int {
	gone := 0
	for _, it := range inventoryOf(charID) {
		if _, ok := spellItemOf(it); ok && !spellItemLive(it) {
			gone += itemQty(it)
		}
	}
	if gone == 0 {
		return 0
	}
	editInventory(charID, func(inv []invItem) []invItem {
		out := inv[:0:0]
		for _, x := range inv {
			if _, ok := spellItemOf(x); ok && !spellItemLive(x) {
				continue
			}
			out = append(out, x)
		}
		return out
	})
	return gone
}

// sweepCampaignSpellItems sweeps every character of a campaign (the story clock moved: a spell may have ended).
func sweepCampaignSpellItems(lobbyID int) {
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND inventory::text LIKE '%spell_item%'`, lobbyID)
	if err != nil {
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		sweepSpellItems(id)
	}
}

// useSpellItem: a creature uses (eats) a spell's item: its healing through the Hit Point owner.
func useSpellItem(lobbyID, userID, targetID int, it invItem) string {
	row, _ := spellItemOf(it)
	if targetID == 0 {
		targetID = userID
	}
	who := creatureName(lobbyID, targetID)
	label := who + " " + row.Use
	if targetID != userID {
		label = fmt.Sprintf("%s gives a %s to %s, who %s", creatureName(lobbyID, userID), row.Name, who, row.Use)
	}
	if row.Heal > 0 {
		n, before, after := healCharacter(targetID, row.Heal)
		if n == "" {
			return label + ": only a character the server knows is healed this way."
		}
		return fmt.Sprintf("%s (%d → %d).", label, before, after)
	}
	return label + "."
}
