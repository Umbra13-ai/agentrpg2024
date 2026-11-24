package main

// Identifying an unidentified magic item (owner, Nov 24; SRD 5.2.1 "Identifying a Magic Item"): "The Identify spell is the fastest way to
// reveal an item's properties. Alternatively, a character can focus on one magic item during a Short Rest, while being in physical contact
// with the item. At the end of the rest, the character learns the item's properties, as well as how to use them. Potions are an exception;
// a little taste is enough to tell the taster what the potion does."
//
// identifyItem is the one writer that makes an item known (its unidentified mark and look removed, through the inventory writer), and
// identifiedLine the one wording of what was learned (its name, what it is, its charges, whether it needs attunement). Its routes: a cast of
// Identify (itemLearningSpells, the cast resolver: the item the description names by its look, the caster's or a party member's), a Short
// Rest's study_item, a taste of a potion (use_item with a description that tastes or sips it; the potion is not drunk) and the GM's
// POST /api/gm/identify-item.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// itemLearningSpells: the spells whose cast teaches an item's properties.
var itemLearningSpells = map[string]bool{"identify": true}

// identifyItem makes the unidentified item that ref names (its look, id or name) known; it returns the item as it now is. refusal is a
// player's reason ("" when done).
func identifyItem(charID int, ref string) (invItem, string) {
	var known invItem
	_, err := changeInventory(charID, func(_ *sql.Tx, inv []invItem) ([]invItem, error) {
		it, ferr := findInventoryItem(inv, ref)
		if ferr != nil || it == nil {
			return nil, codeRefusal("item_not_carried")
		}
		if !itemUnidentified(it) {
			return nil, codeRefusal("item_already_known")
		}
		delete(it, unidentifiedKey)
		delete(it, appearsAsKey)
		known = copyItem(it)
		return inv, nil
	})
	if err != nil {
		switch refusalCode(err, "") {
		case "item_not_carried":
			return nil, fmt.Sprintf("there is no unidentified %q among the items carried", ref)
		case "item_already_known":
			return nil, fmt.Sprintf("%q is already known", ref)
		}
		return nil, "the item could not be read"
	}
	annotateCharges([]invItem{known})
	return known, ""
}

// identifiedLine: what the character learns of an item just identified, from the item and the SRD 5.2.1 magic item it is.
func identifiedLine(look string, it invItem) string {
	name := itemStr(it, "name")
	line := fmt.Sprintf("The %s is %s.", strings.TrimPrefix(strings.TrimPrefix(look, "a "), "an "), name)
	if look == "" {
		line = fmt.Sprintf("It is %s.", name)
	}
	srd := name
	if u := itemUseFor(it); u != nil {
		srd = u.Name
	}
	if m, ok := srdMagicItemNamed(srd); ok {
		if m.RequiresAttunement {
			line += " It requires attunement" + strings.TrimSpace(" "+m.Attunement) + "."
		}
		if d := strings.TrimSpace(m.Description); d != "" {
			if len(d) > 600 {
				d = strings.TrimSpace(d[:600]) + "..."
			}
			line += " " + d
		}
	}
	if cur, max, ok := itemCharges(it); ok {
		line += fmt.Sprintf(" It has %d of %d charges.", cur, max)
	}
	return line
}

var (
	srdMagicItemsOnce sync.Once
	srdMagicItemsBy   map[string]srdMagicItem
)

// srdMagicItemNamed: the SRD 5.2.1 magic item of that name (any case).
func srdMagicItemNamed(name string) (srdMagicItem, bool) {
	srdMagicItemsOnce.Do(func() {
		srdMagicItemsBy = map[string]srdMagicItem{}
		var items []srdMagicItem
		if json.Unmarshal(srd521MagicItemsJSON, &items) == nil {
			for _, m := range items {
				srdMagicItemsBy[strings.ToLower(m.Name)] = m
			}
		}
	})
	m, ok := srdMagicItemsBy[strings.ToLower(strings.TrimSpace(name))]
	return m, ok
}

// identifyBySpell: a cast of Identify learns the unidentified item its description names by its look, carried by the caster or a member of
// its party ("I touch the plain silver ring"); the caster's only unidentified item when the description names none. It answers the cast's line.
func identifyBySpell(charID int, description string) string {
	holders := []int{charID}
	if lobby := lobbyOfChar(charID); lobby > 0 && db != nil {
		rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND id <> $2 ORDER BY id`, lobby, charID)
		if err == nil {
			for rows.Next() {
				var id int
				if rows.Scan(&id) == nil {
					holders = append(holders, id)
				}
			}
			rows.Close()
		}
	}
	var mine []invItem
	for _, h := range holders {
		inv := usableInventory(inventoryOf(h))
		if it := lookInText(inv, description); it != nil {
			return identifyLineFor(h, it)
		}
		if h == charID {
			for _, it := range inv {
				if itemUnidentified(it) {
					mine = append(mine, it)
				}
			}
		}
	}
	switch len(mine) {
	case 0:
		return " Identify: name the unidentified item you touch by how it looks (the caster's or a party member's)."
	case 1:
		return identifyLineFor(charID, mine[0])
	}
	var looks []string
	for _, it := range mine {
		looks = append(looks, playerItemName(it))
	}
	return " Identify: name the item you touch: " + strings.Join(looks, ", ") + "."
}

// identifyLineFor identifies one item of a holder and words it for a reply (" Identify: ...").
func identifyLineFor(holder int, it invItem) string {
	look := playerItemName(it)
	known, refusal := identifyItem(holder, itemStr(it, "id"))
	if refusal != "" {
		return " Identify: " + refusal + "."
	}
	return " Identify: " + identifiedLine(look, known)
}

// tasteRe: a use of a potion that only tastes it ("I take a sip of the vial", "taste the cloudy liquid").
var tasteRe = regexp.MustCompile(`(?i)\b(?:tastes?|tasting|sips?|sipping)\b`)

// tastePotion: a taste of an unidentified potion tells what it does; the potion is not drunk. ok is false when the use is no taste of one.
func tastePotion(charID int, it invItem, description string) (string, bool) {
	if !itemUnidentified(it) || !isPotion(it) || !tasteRe.MatchString(description) {
		return "", false
	}
	look := playerItemName(it)
	known, refusal := identifyItem(charID, itemStr(it, "id"))
	if refusal != "" {
		return featureRefusal("the "+look, refusal), true
	}
	return "A little taste tells you what it does (it is not drunk). " + identifiedLine(look, known), true
}

// handleGMIdentifyItem godoc
// @Summary Make an unidentified item known
// @Description The GM reveals what an unidentified magic item is (the story told it, or a sage named it): the character's player reads its name and properties from now on. item is the item's look, id or name.
// @Tags GM
// @Accept json
// @Produce json
// @Body The character and the item
// @Success 200 {object} map[string]interface{} "The item, now known"
// @Router /gm/identify-item [post]
func handleGMIdentifyItem(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"` // the character carrying the item
		Item        string `json:"item"`         // the item: its look, id or name
	}
	lobby, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID), campaignBeforeStart())
	if !ok {
		return
	}
	look := ""
	if it, ferr := findInventoryItem(inventoryOf(req.CharacterID), req.Item); ferr == nil {
		look = playerItemName(it)
	}
	known, refusal := identifyItem(req.CharacterID, req.Item)
	if refusal != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_not_identified", "message": "Nothing was changed: " + refusal + "."})
		return
	}
	line := identifiedLine(look, known)
	writeAction(db, lobby, req.CharacterID, "item_identified", fmt.Sprintf("The %s is identified", strings.TrimPrefix(strings.TrimPrefix(look, "a "), "an ")), line)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "item": itemStr(known, "name"), "result": line})
}
