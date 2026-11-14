package main

// Selling to a shop (Nov 13). Matt: shops buy items for half their selling price. The SRD 5.2.1 says the same of equipment
// ("undamaged weapons, armor, and other equipment fetch half their cost when sold in a market"). A shop buys the kinds of items it
// sells, whatever the buyer's level: the blacksmith weapons and metal armor, the magic shop magic items and so on; the starting shop
// buys anything it sells. The price is half what that shop asks for the item (with the DM's price_percent).
//
//	POST /api/campaigns/{id}/shops/{shop_id} {"character_id": N, "sell": [{"item_id": "i14", "quantity": 1}]}
//	POST /api/characters/{id}/shop {"sell": [{"item_id": "i14"}]}       (the starting shop, until the campaign starts)
// The shop's GET lists what it would pay for each item the character carries (offers_for_your_items).
// Not bought: an item in hand or worn as armor (unequip it first), an attuned item (end the attunement first), a bag with
// something in it (empty it first), and things the SRD gives no price.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// appraisal: what an item is worth (the full price, in GP) and what kind of goods it is.
type appraisal struct {
	ValueGP float64
	Kind    string   // ordinary, magic, scroll
	Item    shopItem // ordinary goods
	Rarity  string
	Spell   string // scrolls: the spell's slug
}

var scrollName = regexp.MustCompile(`^Spell Scroll \((.+)\)$`)
var madeFrom = regexp.MustCompile(`^(.+) \((.+)\)$`)

func appraiseItem(name string) (appraisal, bool) {
	n := strings.TrimSpace(name)
	if m := scrollName.FindStringSubmatch(n); m != nil {
		k := normalizeSpellSlug(m[1])
		if sp, ok := srdSpellsMemory[k]; ok {
			return appraisal{ValueGP: float64(scrollPriceGP(sp.Level)), Kind: "scroll", Spell: k}, true
		}
	}
	// "Longsword +2", "Shield +1", "Arrows +1", "Wand of the War Mage +1"
	if p := itemPlus(n); p > 0 {
		base := strings.TrimSpace(plusRe.ReplaceAllString(n, ""))
		variant := fmt.Sprintf("+%d", p)
		for _, o := range magicOffers() {
			if len(o.Variants) == 0 {
				continue
			}
			if itemKey(o.Short) == itemKey(base) { // a named item with variants
				return appraisal{ValueGP: float64(rarityValueGP[variantRarity(o.Rarity, variant)]), Kind: "magic", Rarity: variantRarity(o.Rarity, variant)}, true
			}
		}
		if bi, ok := findShopItem(base); ok {
			short := map[string]string{"weapons": "Weapon", "armor": "Armor", "ammunition": "Ammunition"}[bi.category]
			if bi.row.Name == "Shield" {
				short = "Shield"
			}
			for _, o := range magicOffers() {
				if o.Short == short && len(o.Variants) > 0 {
					r := variantRarity(o.Rarity, variant)
					v := float64(rarityValueGP[r])
					if o.Consumable {
						v /= 2
					}
					return appraisal{ValueGP: v + bi.row.CostGP/float64(bi.amount), Kind: "magic", Rarity: r}, true
				}
			}
		}
	}
	if it, ok := findShopItem(n); ok {
		return appraisal{ValueGP: it.row.CostGP / float64(it.amount), Kind: "ordinary", Item: it}, true
	}
	if it, ok := findShopItem(strings.ReplaceAll(n, "'", "’")); ok {
		return appraisal{ValueGP: it.row.CostGP / float64(it.amount), Kind: "ordinary", Item: it}, true
	}
	// "Flame Tongue (Longsword)": the item plus its base
	baseCost, magicName := 0.0, n
	if m := madeFrom.FindStringSubmatch(n); m != nil {
		if bi, ok := findShopItem(m[2]); ok {
			baseCost, magicName = bi.row.CostGP, m[1]
		}
	}
	for _, o := range magicOffers() {
		if itemKey(o.Name) == itemKey(magicName) || itemKey(o.Short) == itemKey(magicName) {
			if o.Rarity == "Artifact" || o.ValueGP == 0 {
				return appraisal{}, false
			}
			return appraisal{ValueGP: float64(o.ValueGP) + baseCost, Kind: "magic", Rarity: o.Rarity}, true
		}
	}
	return appraisal{}, false
}

// shopBuysKind: whether a shop takes this kind of goods (ordinary goods it sells, magic items in a magic shop, potions at the
// alchemist, scrolls of its list). nil def = the starting shop: ordinary goods only.
func shopBuysKind(def *shopTypeDef, a appraisal, name string) bool {
	if def == nil {
		return a.Kind == "ordinary"
	}
	switch a.Kind {
	case "ordinary":
		if def.potions && strings.Contains(strings.ToLower(name), "potion") {
			return true // a Potion of Healing is in the gear table too
		}
		if def.ordinary == nil || !def.ordinary(a.Item) {
			return false
		}
		return true
	case "magic":
		if def.magic {
			return true
		}
		return def.potions && strings.Contains(strings.ToLower(name), "potion")
	case "scroll":
		return def.scrolls != "" && (def.magic || isSpellOnClassList(a.Spell, def.scrolls))
	}
	return false
}

// sellBlocked: why an item can't be sold as it is ("" if it can).
func sellBlocked(charID int, inv []invItem, it invItem, qty int) string {
	name := itemStr(it, "name")
	id := itemStr(it, "id")
	for _, x := range inv {
		if itemStr(x, "location") == id {
			return fmt.Sprintf("%s (id %s) has things in it: take them out first.", name, id)
		}
	}
	var armor, attuned string
	var attRaw []byte
	db.QueryRow(`SELECT COALESCE(equipped_armor,''), COALESCE(attuned_items,'[]') FROM characters WHERE id = $1`, charID).Scan(&armor, &attRaw)
	attuned = string(attRaw)
	if strings.Contains(strings.ToLower(attuned), strings.ToLower(`"`+name+`"`)) {
		return fmt.Sprintf("You are attuned to %s: end the attunement first.", name)
	}
	same, have := 0, 0
	for _, x := range inv {
		if strings.EqualFold(itemStr(x, "name"), name) {
			same++
			q := 1
			if v, ok := x["quantity"].(float64); ok && v > 1 {
				q = int(v)
			}
			have += q
		}
	}
	// the very item in hand (F25: the held Quarterstaff was sold while a carried one remained, and main_hand pointed at nothing held)
	if itemStr(it, "location") == "held" {
		other := ""
		for _, x := range inv {
			if itemStr(x, "id") != id && strings.EqualFold(itemStr(x, "name"), name) && itemStr(x, "location") != "held" {
				other = itemStr(x, "id")
				break
			}
		}
		if other != "" {
			return fmt.Sprintf("You hold this %s (id %s) in your hand: sell the other one (id %s) instead, or unequip this one first (POST /api/characters/unequip-weapon).", name, id, other)
		}
		return fmt.Sprintf("You hold %s (id %s) in your hand: unequip it first (POST /api/characters/unequip-weapon).", name, id)
	}
	lastOne := have-qty < 1
	main, off := equippedWeapons(charID)
	held := strings.EqualFold(main, name) || strings.EqualFold(off, name)
	worn := armor != "" && (strings.EqualFold(armor, name) || itemSlug(name) == armor || sameArmor(name, armor))
	shield := strings.EqualFold(name, "Shield") && shieldInHand(charID) || (strings.HasPrefix(name, "Shield +") && shieldInHand(charID))
	if lastOne && (held || worn || shield) {
		return fmt.Sprintf("You are using %s (in hand or worn): unequip it first (POST /api/characters/unequip-weapon or unequip-armor).", name)
	}
	return ""
}

type sellReq struct {
	ItemID   string `json:"item_id"`
	Item     string `json:"item"`
	Quantity *int   `json:"quantity"` // missing: the whole stack; 0 or less is refused (F17: 0 sold the whole stack)
}

// sellItems: one transaction; priceCP turns a full price in GP into this shop's asking price in CP.
func sellItems(charID int, def *shopTypeDef, shopName string, priceCP func(float64) int, req []sellReq) (int, map[string]interface{}) {
	tx, err := db.Begin()
	if err != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "db_error"}
	}
	defer tx.Rollback()
	inv, err := loadInventoryTx(tx, charID)
	if err != nil {
		return http.StatusNotFound, map[string]interface{}{"error": "character_not_found"}
	}
	total := 0
	var lines []string
	for _, s := range req {
		idx := -1
		for i, it := range inv {
			if (s.ItemID != "" && itemStr(it, "id") == s.ItemID) || (s.ItemID == "" && s.Item != "" && strings.EqualFold(itemStr(it, "name"), s.Item)) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return http.StatusBadRequest, map[string]interface{}{"error": "item_not_found", "message": fmt.Sprintf("You don't carry %s%s (send item_id from your inventory).", s.ItemID, s.Item)}
		}
		it := inv[idx]
		name := itemStr(it, "name")
		have := 1
		if v, ok := it["quantity"].(float64); ok && v > 1 {
			have = int(v)
		}
		q := have
		if s.Quantity != nil {
			q = *s.Quantity
		}
		if q < 1 || q > have {
			return http.StatusBadRequest, map[string]interface{}{"error": "invalid_quantity", "message": fmt.Sprintf("%s: quantity must be 1 to %d (you have %d).", name, have, have)}
		}
		a, ok := appraiseItem(name)
		if !ok {
			return http.StatusBadRequest, map[string]interface{}{"error": "no_price", "message": fmt.Sprintf("%s has no price in the SRD, so a shop can't value it; your GM can buy it.", name)}
		}
		if !shopBuysKind(def, a, name) {
			return http.StatusBadRequest, map[string]interface{}{"error": "shop_doesnt_buy", "message": fmt.Sprintf("%s doesn't buy %s: a shop buys the kinds of goods it sells.", shopName, name)}
		}
		if why := sellBlocked(charID, inv, it, q); why != "" {
			return http.StatusBadRequest, map[string]interface{}{"error": "cannot_sell", "message": why}
		}
		each := priceCP(a.ValueGP) / 2
		if each == 0 {
			return http.StatusBadRequest, map[string]interface{}{"error": "worth_nothing", "message": fmt.Sprintf("Half the price of %s is less than 1 CP, so the shop won't buy it.", name)}
		}
		total += each * q
		lines = append(lines, fmt.Sprintf("%d x %s: %s", q, name, moneyText(each*q)))
		if q == have {
			inv = append(inv[:idx], inv[idx+1:]...)
		} else {
			it["quantity"] = float64(have - q)
		}
	}
	if err := saveInventoryTx(tx, charID, inv); err != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"}
	}
	money := walletCP(charID) + total
	if _, err := tx.Exec(`UPDATE characters SET gold = $1, silver = $2, copper = $3, electrum = 0, platinum = 0 WHERE id = $4`, money/100, (money%100)/10, money%10, charID); err != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"}
	}
	if tx.Commit() != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"}
	}
	refreshStoredAC(charID)
	return http.StatusOK, map[string]interface{}{"success": true, "sold": lines, "received": moneyText(total), "your_money": moneyText(money),
		"rule": "A shop pays half its own price for an item (SRD 5.2.1: equipment fetches half its cost when sold)."}
}

// sellOffers: what this shop would pay for each item the character carries.
func sellOffers(charID int, def *shopTypeDef, priceCP func(float64) int) []map[string]interface{} {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []invItem
	json.Unmarshal(raw, &inv)
	var out []map[string]interface{}
	for _, it := range inv {
		name := itemStr(it, "name")
		a, ok := appraiseItem(name)
		if !ok || !shopBuysKind(def, a, name) || priceCP(a.ValueGP)/2 == 0 {
			continue
		}
		o := map[string]interface{}{"item_id": itemStr(it, "id"), "item": name, "pays_each": moneyText(priceCP(a.ValueGP) / 2)}
		if q, ok := it["quantity"].(float64); ok && q > 1 {
			o["you_have"] = int(q)
		}
		if why := sellBlocked(charID, inv, it, 1); why != "" {
			o["not_now"] = why
		}
		out = append(out, o)
	}
	return out
}
