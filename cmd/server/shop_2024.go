package main

// The starting shop (Nov 13). Matt: a player can take the gold instead of the equipment at creation and buy weapons, armor and
// other gear, and can spend whatever gold is left at the end of character creation. Prices and items are the SRD 5.2.1 tables
// (weapons, armor, adventuring gear, ammunition, tools), from the same catalog the crafting rules use.
//
//	GET  /api/characters/{id}/shop[?category=weapons|armor|gear|ammunition|tools]   the items, prices, your money
//	POST /api/characters/{id}/shop {"buy": [{"item": "Longsword", "quantity": 1}, ...]}   pay and put the items in the inventory
//	POST /api/characters/{id}/shop {"done": true}                                         finished shopping (the setup step closes)
//
// The shop is open from creation until the character's campaign starts; after that, buying is up to the GM.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

var shopOnce sync.Once

func ensureShopColumn() {
	shopOnce.Do(func() { db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS shop_done BOOLEAN DEFAULT false`) })
}

type shopItem struct {
	row      catalogRow
	category string // weapons, armor, gear, ammunition, tools
	amount   int    // how many one purchase gives (ammunition comes in 20s ...)
}

func shopCatalog() []shopItem {
	var out []shopItem
	seen := map[string]bool{}
	for _, r := range buildItemCatalog() {
		cat := map[string]string{"weapon": "weapons", "armor": "armor", "gear": "gear", "tool": "tools", "mount": "mounts", "vehicle": "vehicles"}[r.Category]
		if cat == "" || r.CostGP <= 0 || r.Name == "Ammunition" || seen[itemKey(r.Name)] {
			continue
		}
		seen[itemKey(r.Name)] = true
		out = append(out, shopItem{row: r, category: cat, amount: 1})
	}
	for _, r := range shopVariantRows() {
		if !seen[itemKey(r.Name)] {
			seen[itemKey(r.Name)] = true
			cat := "gear"
			if r.Category == "tool" {
				cat = "tools"
			}
			out = append(out, shopItem{row: r, category: cat, amount: 1})
		}
	}
	for _, r := range ammunitionRows() {
		if seen[itemKey(r.Name)] || r.CostGP <= 0 {
			continue
		}
		seen[itemKey(r.Name)] = true
		n := 1
		if a, ok := r.Data["amount"].(int); ok && a > 0 {
			n = a
		}
		out = append(out, shopItem{row: r, category: "ammunition", amount: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].category != out[j].category {
			return out[i].category < out[j].category
		}
		return out[i].row.Name < out[j].row.Name
	})
	return out
}

func findShopItem(name string) (shopItem, bool) {
	k := itemKey(name)
	for _, it := range shopCatalog() {
		if itemKey(it.row.Name) == k || it.row.Slug == strings.ToLower(strings.TrimSpace(name)) {
			return it, true
		}
	}
	return shopItem{}, false
}

// walletCP: the character's money in copper pieces (10 CP = 1 SP, 10 SP = 1 GP, 1 EP = 5 SP, 1 PP = 10 GP).
func walletCP(charID int) int {
	var gp, sp, cp, ep, pp int
	db.QueryRow(`SELECT COALESCE(gold,0), COALESCE(silver,0), COALESCE(copper,0), COALESCE(electrum,0), COALESCE(platinum,0) FROM characters WHERE id = $1`, charID).Scan(&gp, &sp, &cp, &ep, &pp)
	return pp*1000 + gp*100 + ep*50 + sp*10 + cp
}

func moneyText(cp int) string {
	var parts []string
	if g := cp / 100; g > 0 {
		parts = append(parts, fmt.Sprintf("%d GP", g))
	}
	if s := (cp % 100) / 10; s > 0 {
		parts = append(parts, fmt.Sprintf("%d SP", s))
	}
	if c := cp % 10; c > 0 {
		parts = append(parts, fmt.Sprintf("%d CP", c))
	}
	if len(parts) == 0 {
		return "0 GP"
	}
	return strings.Join(parts, ", ")
}

// shopOpen: open until the character's campaign starts.
func shopOpen(charID int) (bool, string) {
	var status string
	db.QueryRow(`SELECT COALESCE(l.status,'') FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id WHERE c.id = $1`, charID).Scan(&status)
	if status == "active" || status == "completed" {
		return false, "The starting shop closes when your campaign starts. In the game, buying and selling is up to your GM."
	}
	return true, ""
}

func shopItemView(it shopItem, weaponProfs, armorProfs string, money int) map[string]interface{} {
	r := it.row
	m := map[string]interface{}{"item": r.Name, "category": it.category, "price": r.CostText}
	if it.amount > 1 {
		m["one_purchase_gives"] = it.amount
	}
	if w, ok := r.Data["weight_text"].(string); ok && w != "" {
		m["weight"] = w
	} else if r.WeightLb > 0 {
		m["weight"] = fmt.Sprintf("%g lb.", r.WeightLb)
	}
	switch it.category {
	case "weapons":
		m["damage"] = fmt.Sprintf("%v %v", r.Data["damage"], r.Data["damage_type"])
		m["properties"] = r.Data["properties_text"]
		m["mastery"] = r.Data["mastery"]
		m["weapon_category"] = r.Data["weapon_category"]
		key := strings.ToLower(strings.ReplaceAll(r.Name, " ", "_"))
		m["you_are_proficient"] = isWeaponProficient(weaponProfs, key) || isWeaponProficient(weaponProfs, r.Name)
	case "armor":
		m["armor_class"] = r.Data["ac_text"]
		m["strength"] = r.Data["strength_text"]
		m["stealth"] = r.Data["stealth_text"]
		m["armor_category"] = r.Data["armor_category"]
		at, _ := r.Data["armor_type"].(string)
		m["you_have_training"] = isArmorProficient(armorProfs, at)
	case "tools":
		m["ability"] = r.Data["ability"]
		m["utilize"] = r.Data["utilize"]
	default:
		if d, ok := r.Data["description"].(string); ok && d != "" {
			m["what_it_is"] = d
		}
	}
	m["you_can_afford_one"] = int(r.CostGP*100+0.5) <= money
	return m
}

func handleCharacterShop(w http.ResponseWriter, r *http.Request, charID int) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	ensureShopColumn()
	var owner int
	var weaponProfs, armorProfs, class string
	if db.QueryRow(`SELECT COALESCE(agent_id,0), COALESCE(weapon_proficiencies,''), COALESCE(armor_proficiencies,''), COALESCE(class,'') FROM characters WHERE id = $1`, charID).
		Scan(&owner, &weaponProfs, &armorProfs, &class) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	open, why := shopOpen(charID)
	money := walletCP(charID)
	if r.Method == http.MethodGet {
		cat := strings.ToLower(r.URL.Query().Get("category"))
		var items []map[string]interface{}
		counts := map[string]int{}
		for _, it := range shopCatalog() {
			counts[it.category]++
			if cat == "" || cat == "all" || cat == it.category {
				items = append(items, shopItemView(it, weaponProfs, armorProfs, money))
			}
		}
		out := map[string]interface{}{"open": open, "your_money": moneyText(money), "categories": counts,
			"how_to_buy":      fmt.Sprintf("POST /api/characters/%d/shop {\"buy\": [{\"item\": \"<item name>\", \"quantity\": 1}, ...]}. Prices are the SRD's; you pay from your coins and get change in GP, SP and CP.", charID),
			"when_you_are_done": fmt.Sprintf("POST /api/characters/%d/shop {\"done\": true}", charID),
			"how_to_sell":       fmt.Sprintf("POST /api/characters/%d/shop {\"sell\": [{\"item_id\": \"<id>\", \"quantity\": 1}]}: the shop pays half its price (SRD 5.2.1).", charID),
			"offers_for_your_items": sellOffers(charID, nil, func(gp float64) int { return int(gp*100 + 0.5) }),
			"items":           items}
		if !open {
			out["closed_because"] = why
		}
		writeJSONStatus(w, http.StatusOK, out)
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Buy []struct {
			Item     string `json:"item"`
			Quantity int    `json:"quantity"`
		} `json:"buy"`
		Done bool      `json:"done"`
		Sell []sellReq `json:"sell"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	if req.Done && len(req.Buy) == 0 {
		db.Exec(`UPDATE characters SET shop_done = true WHERE id = $1`, charID)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "your_money": moneyText(money), "message": "Done shopping. You keep the money you didn't spend."})
		return
	}
	if !open {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "shop_closed", "message": why})
		return
	}
	if len(req.Sell) > 0 && len(req.Buy) == 0 {
		status, resp := sellItems(charID, nil, "The starting shop", func(gp float64) int { return int(gp*100 + 0.5) }, req.Sell)
		writeJSONStatus(w, status, resp)
		return
	}
	if len(req.Buy) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_buy", "message": "Send buy: a list of {\"item\": ..., \"quantity\": ...}, or {\"done\": true} when you have finished."})
		return
	}
	total := 0
	var kit []startItem
	var lines []string
	for _, b := range req.Buy {
		q := b.Quantity
		if q == 0 {
			q = 1
		}
		if q < 1 || q > 100 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_quantity", "message": fmt.Sprintf("%s: quantity must be 1 to 100.", b.Item)})
			return
		}
		it, ok := findShopItem(b.Item)
		if !ok {
			msg := fmt.Sprintf("%q is not in the SRD's equipment tables, so the shop doesn't sell it. GET /api/characters/%d/shop lists what it sells.", b.Item, charID)
			if _, isMagic := findCraftItem(b.Item); isMagic || craftMagicItem(b.Item) {
				msg = fmt.Sprintf("%q is a magic item; the shop sells only ordinary equipment. Magic items come from your GM.", b.Item)
			}
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_sold_here", "message": msg})
			return
		}
		cost := int(it.row.CostGP*100+0.5) * q
		total += cost
		name := it.row.Name
		if straight := strings.ReplaceAll(name, "’", "'"); packContents[strings.ToLower(straight)].Container != "" {
			name = straight // a pack opens into its bag and contents (the pack table uses straight apostrophes)
		}
		kit = append(kit, startItem{Name: name, Qty: q * it.amount, Focus: focusKindOf(name)})
		lines = append(lines, fmt.Sprintf("%d x %s (%s each): %s", q, it.row.Name, it.row.CostText, moneyText(cost)))
	}
	status, resp := completePurchase(charID, money, total, kit, lines)
	if status == http.StatusOK {
		resp["next"] = fmt.Sprintf("Put on armor and take up weapons: POST /api/characters/auto-equip {\"character_id\": %d, \"answer\": \"yes\"}. Buy more, or POST /api/characters/%d/shop {\"done\": true} when you have finished.", charID, charID)
	}
	writeJSONStatus(w, status, resp)
}

// completePurchase pays total (in CP) from the character's coins and puts the kit items in the inventory, in one transaction.
// Shared by the starting shop and the shops a DM opens (campaign_shops_2024.go).
func completePurchase(charID, money, total int, kit []startItem, lines []string) (int, map[string]interface{}) {
	if total > money {
		return http.StatusBadRequest, map[string]interface{}{"error": "not_enough_money",
			"message": fmt.Sprintf("That costs %s and you have %s.", moneyText(total), moneyText(money)), "would_cost": lines}
	}
	res, err := buildKit(kit, 0, "", "s", "shop", false)
	if err != nil {
		return http.StatusBadRequest, map[string]interface{}{"error": "cannot_add", "message": publicError(err)}
	}
	tx, err := db.Begin()
	if err != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "db_error"}
	}
	defer tx.Rollback()
	inv, err := loadInventoryTx(tx, charID)
	if err != nil {
		return http.StatusNotFound, map[string]interface{}{"error": "character_not_found"}
	}
	base := 0
	for _, it := range inv {
		if id := itemStr(it, "id"); strings.HasPrefix(id, "i") {
			var n int
			fmt.Sscanf(id[1:], "%d", &n)
			if n > base {
				base = n
			}
		}
	}
	remap := map[string]string{}
	for i, it := range res.Items {
		remap[itemStr(it, "id")] = fmt.Sprintf("i%d", base+i+1)
	}
	var bought []string
	for _, it := range res.Items {
		it["id"] = remap[itemStr(it, "id")]
		if loc := itemStr(it, "location"); loc != "" {
			if m, ok := remap[loc]; ok {
				it["location"] = m
			}
		}
		inv = append(inv, it)
		bought = append(bought, fmt.Sprintf("%s (id %s)", itemStr(it, "name"), itemStr(it, "id")))
	}
	fillItemWeights(inv)
	if err := saveInventoryTx(tx, charID, inv); err != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"}
	}
	left := money - total
	if _, err := tx.Exec(`UPDATE characters SET gold = $1, silver = $2, copper = $3, electrum = 0, platinum = 0 WHERE id = $4`, left/100, (left%100)/10, left%10, charID); err != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"}
	}
	if tx.Commit() != nil {
		return http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"}
	}
	refreshStoredAC(charID)
	return http.StatusOK, map[string]interface{}{"success": true, "bought": lines, "paid": moneyText(total), "your_money": moneyText(left), "now_in_your_inventory": bought}
}

// craftMagicItem: the name is an SRD magic item.
func craftMagicItem(name string) bool {
	loadCraftData()
	return craftMagic[itemKey(name)]
}

// shopSetupStep: offered after creation while the character has money, the shop is open and the player hasn't said done.
func shopSetupStep(charID int) *setupStep {
	ensureShopColumn()
	var done bool
	db.QueryRow(`SELECT COALESCE(shop_done,false) FROM characters WHERE id = $1`, charID).Scan(&done)
	money := walletCP(charID)
	if open, _ := shopOpen(charID); done || !open || money <= 0 {
		return nil
	}
	st := setupStep{Key: "shop", Optional: true, Title: "Buy equipment with your gold (optional)",
		Why: fmt.Sprintf("You have %s. Before your campaign starts you can buy weapons, armor, adventuring gear, ammunition and tools at the SRD's prices. GET /api/characters/%d/shop lists everything (add ?category=weapons, armor, gear, ammunition or tools), with what each item does, whether you are proficient with it and whether you can afford it. Buy with POST /api/characters/%d/shop {\"buy\": [{\"item\": \"Longsword\", \"quantity\": 1}]}. When you have finished, or if you want to keep your money, send the request below. The shop closes when your campaign starts; after that, buying is up to your GM.", moneyText(money), charID, charID),
		Request: req("POST", fmt.Sprintf("/api/characters/%d/shop", charID), map[string]interface{}{"done": true}),
		Choices: map[string]interface{}{"your_money": moneyText(money), "see_the_shop": fmt.Sprintf("GET /api/characters/%d/shop", charID)}}
	return &st
}
