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
	"fmt"
	"net/http"
	"regexp"
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
	alt := itemKey(shopParenRe.ReplaceAllString(name, ", $1")) // "Holy Symbol (Amulet)" is "Holy Symbol, Amulet"
	for _, it := range shopCatalog() {
		if it.row.Slug == strings.ToLower(strings.TrimSpace(name)) {
			return it, true
		}
		if ik := itemKey(it.row.Name); (ik == k || ik == alt) && !extraQualifier(name, it.row.Name) {
			return it, true
		}
	}
	return shopItem{}, false
}

var shopPlusRe = regexp.MustCompile(`\+\s*\d`)
var shopParenRe = regexp.MustCompile(`\(([^)]*)\)`)
var shopDigitsRe = regexp.MustCompile(`\d+`)

// extraQualifier: the name asks for more than the shop item is (a "+1", a "(silvered)", a number): "Dagger +3" is not a Dagger
// (Nov 14 review: the plain item was sold for each of these).
func extraQualifier(asked, item string) bool {
	li := strings.ToLower(item)
	if shopPlusRe.MatchString(asked) && !shopPlusRe.MatchString(item) {
		return true
	}
	for _, m := range shopParenRe.FindAllStringSubmatch(strings.ToLower(asked), -1) {
		if inner := strings.TrimSpace(m[1]); inner != "" && !strings.Contains(li, inner) {
			return true
		}
	}
	outside := shopParenRe.ReplaceAllString(asked, " ")
	for _, d := range shopDigitsRe.FindAllString(outside, -1) {
		if !strings.Contains(li, d) {
			return true
		}
	}
	return false
}

// shopKindsOf: shop items whose name starts with the one asked ("Holy Symbol" -> "Holy Symbol, Amulet", "Holy Symbol, Emblem" ...).
func shopKindsOf(name string) []string {
	n := strings.ToLower(strings.TrimSpace(shopParenRe.ReplaceAllString(name, "")))
	var out []string
	for _, it := range shopCatalog() {
		if n != "" && strings.HasPrefix(strings.ToLower(it.row.Name), n) {
			out = append(out, fmt.Sprintf("%s (%s)", it.row.Name, moneyText(int(it.row.CostGP*100+0.5))))
		}
	}
	return out
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
			"how_to_buy":            fmt.Sprintf("POST /api/characters/%d/shop {\"buy\": [{\"item\": \"<item name>\", \"quantity\": 1}, ...]}. Prices are the SRD's; you pay from your coins and get change in GP, SP and CP.", charID),
			"when_you_are_done":     fmt.Sprintf("POST /api/characters/%d/shop {\"done\": true}", charID),
			"how_to_sell":           fmt.Sprintf("POST /api/characters/%d/shop {\"sell\": [{\"item_id\": \"<id>\", \"quantity\": 1}]}: the shop pays half its price (SRD 5.2.1).", charID),
			"offers_for_your_items": sellOffers(charID, nil, func(gp float64) int { return int(gp*100 + 0.5) }),
			"items":                 items}
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
			Quantity *int   `json:"quantity"` // missing: 1
		} `json:"buy"`
		Done bool      `json:"done"`
		Sell []sellReq `json:"sell"`
	}
	// a misspelled field (qty) is refused, not read as 1 (F17)
	if msg := decodeStrict(r, &req, `buy [{"item", "quantity"}], sell [{"item_id", "quantity"}] or done`); msg != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": msg + " Nothing was bought or sold."})
		return
	}
	if !req.Done && len(req.Buy) == 0 && len(req.Sell) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_buy", "message": "Send buy: a list of {\"item\": ..., \"quantity\": ...}, sell, or {\"done\": true} when you have finished."})
		return
	}
	if !open && (len(req.Buy) > 0 || len(req.Sell) > 0) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "shop_closed", "message": why + " Nothing was bought or sold."})
		return
	}
	// every part is done, in this order: sell, buy, done (F16: buy with sell or done dropped the rest without a word). Each part is
	// checked before it runs; when a later part is refused, the reply says what was already done (HTTP 422).
	reply := map[string]interface{}{"success": true}
	var doneParts []string
	if len(req.Sell) > 0 {
		status, resp := sellItems(charID, nil, "The starting shop", func(gp float64) int { return int(gp*100 + 0.5) }, req.Sell)
		if status != http.StatusOK {
			resp["message"] = fmt.Sprint(resp["message"]) + " Nothing was bought or sold."
			writeJSONStatus(w, status, resp)
			return
		}
		for k, v := range resp {
			reply[k] = v
		}
		doneParts = append(doneParts, "sold "+strings.Join(anyStrings(resp["sold"]), ", "))
		money = walletCP(charID)
	}
	refuse := func(status int, body map[string]interface{}) {
		if len(doneParts) > 0 {
			body["not_saved"] = body["message"]
			body["already_done"] = doneParts
			body["message"] = fmt.Sprintf("%s Already done: %s.", body["message"], strings.Join(doneParts, "; "))
			status = http.StatusUnprocessableEntity
		} else {
			body["message"] = fmt.Sprint(body["message"]) + " Nothing was bought or sold."
		}
		writeJSONStatus(w, status, body)
	}
	if len(req.Buy) == 0 {
		if req.Done {
			db.Exec(`UPDATE characters SET shop_done = true WHERE id = $1`, charID)
			reply["your_money"] = moneyText(money)
			reply["message"] = "Done shopping. You keep the money you didn't spend."
			if len(doneParts) > 0 {
				reply["message"] = fmt.Sprintf("Done: %s. Done shopping; you keep the money you didn't spend.", strings.Join(doneParts, "; "))
			}
		}
		writeJSONStatus(w, http.StatusOK, reply)
		return
	}
	total := 0
	var kit []startItem
	var lines []string
	for _, b := range req.Buy {
		q := 1
		if b.Quantity != nil {
			q = *b.Quantity
		}
		if q < 1 || q > 100 {
			refuse(http.StatusBadRequest, map[string]interface{}{"error": "invalid_quantity", "message": fmt.Sprintf("%s: quantity must be 1 to 100.", b.Item)})
			return
		}
		it, ok := findShopItem(b.Item)
		if !ok {
			msg := fmt.Sprintf("%q is not in the SRD's equipment tables, so the shop doesn't sell it. GET /api/characters/%d/shop lists what it sells.", b.Item, charID)
			plain := strings.TrimSpace(shopPlusRe.ReplaceAllString(shopParenRe.ReplaceAllString(b.Item, ""), ""))
			if base, ok := findShopItem(plain); ok && plain != strings.TrimSpace(b.Item) {
				msg = fmt.Sprintf("%q isn't sold here: the shop sells the plain %s (%s). A +1, silvered or other magic version comes from your GM.", b.Item, base.row.Name, moneyText(int(base.row.CostGP*100+0.5)))
			} else if craftMagicItem(b.Item) || shopPlusRe.MatchString(b.Item) { // only a real magic item (Nov 14 review: Holy Symbol was called one)
				msg = fmt.Sprintf("%q is a magic item; the shop sells only ordinary equipment. Magic items come from your GM.", b.Item)
			} else if kinds := shopKindsOf(b.Item); len(kinds) > 0 {
				msg = fmt.Sprintf("%q comes in kinds: choose one of %s.", b.Item, strings.Join(kinds, ", "))
			}
			refuse(http.StatusBadRequest, map[string]interface{}{"error": "not_sold_here", "message": msg})
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
	if status != http.StatusOK {
		refuse(status, resp)
		return
	}
	for k, v := range resp {
		reply[k] = v
	}
	reply["next"] = fmt.Sprintf("Put on armor and take up weapons: POST /api/characters/auto-equip {\"character_id\": %d, \"answer\": \"yes\"}. Buy more, or POST /api/characters/%d/shop {\"done\": true} when you have finished.", charID, charID)
	if req.Done {
		db.Exec(`UPDATE characters SET shop_done = true WHERE id = $1`, charID)
		reply["next"] = fmt.Sprintf("Done shopping; you keep the money you didn't spend. Put on armor and take up weapons: POST /api/characters/auto-equip {\"character_id\": %d, \"answer\": \"yes\"}.", charID)
		reply["done_shopping"] = true
	}
	writeJSONStatus(w, http.StatusOK, reply)
}

func anyStrings(v interface{}) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []interface{}:
		var out []string
		for _, e := range x {
			out = append(out, fmt.Sprint(e))
		}
		return out
	}
	return nil
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
	if f := freedItemIDFloor(charID); f > base { // not an id a sale just freed
		base = f
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
	// offered to every character while the shop is open and they haven't said done, with or without money (selling works with an
	// empty purse); only the wording depends on what they chose (Matt, Nov 14: gold-option characters got no prompt while an
	// equipment-pack fighter did)
	if open, _ := shopOpen(charID); done || !open {
		return nil
	}
	var class, opt string
	db.QueryRow(`SELECT LOWER(COALESCE(class,'')), LOWER(COALESCE(starting_equipment,'')) FROM characters WHERE id = $1`, charID).Scan(&class, &opt)
	classGold := false
	if o, ok := classStartingEquipment[class][opt]; ok && len(o.Items) == 0 {
		classGold = true // the class kit option that is gold only
	}
	bgGold := false
	if rec, ok := characterOriginOf(charID); ok && rec != nil && strings.EqualFold(rec.BackgroundEquipment, "b") {
		bgGold = true // the background's 50 GP instead of its package
	}
	shop := fmt.Sprintf("GET /api/characters/%d/shop", charID)
	buySell := fmt.Sprintf("Buy with POST /api/characters/%d/shop {\"buy\": [{\"item\": \"Longsword\", \"quantity\": 1}]}; sell with {\"sell\": [{\"item_id\": \"<id from your inventory>\"}]} (a shop pays half the price).", charID)
	// two wordings (Matt, Nov 14): a character who didn't take both equipment packages is told to shop for its gear; one who took
	// both may buy extras or sell items from its pack
	title := "Visit the shop: buy extras or sell items from your pack"
	why := fmt.Sprintf("You took your equipment packages, and you have %s. You may visit the shop to buy extras or to sell items from your pack. %s lists everything (add ?category=weapons, armor, gear, ammunition or tools) with what each item does and whether you can afford it. %s ", moneyText(money), shop, buySell)
	if classGold || bgGold {
		title = "Visit the shop: buy your starting gear"
		why = fmt.Sprintf("You took gold instead of an equipment package, so you have %s: visit the shop to buy your starting gear (weapons, armor, a pack, tools, ammunition) at the SRD's prices. You can also sell items you have. %s lists everything (add ?category=weapons, armor, gear, ammunition or tools) with what each item does, whether you are proficient with it and whether you can afford it. %s ", moneyText(money), shop, buySell)
	}
	why += "When you have finished shopping (or want nothing), send the request below to continue setup. The shop closes when your campaign starts; after that, buying and selling are up to your GM."
	st := setupStep{Key: "shop", Title: title, Why: why,
		Request: req("POST", fmt.Sprintf("/api/characters/%d/shop", charID), map[string]interface{}{"done": true}),
		Choices: map[string]interface{}{"your_money": moneyText(money), "see_the_shop": shop,
			"buy":             fmt.Sprintf("POST /api/characters/%d/shop {\"buy\": [{\"item\": \"...\", \"quantity\": 1}]}", charID),
			"sell":            fmt.Sprintf("POST /api/characters/%d/shop {\"sell\": [{\"item_id\": \"...\"}]}", charID),
			"finish_shopping": fmt.Sprintf("POST /api/characters/%d/shop {\"done\": true}", charID),
			"you_chose":       map[bool]string{true: "gold instead of equipment", false: "the equipment pack"}[classGold || bgGold]}}
	return &st
}
