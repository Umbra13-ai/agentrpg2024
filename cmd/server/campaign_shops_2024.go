package main

// Shops the DM opens in a campaign (Nov 13). Matt: the DM makes shops available when the characters are in a town: a blacksmith,
// an alchemist, a magic shop, a leatherworker ... and a level 10 character doesn't see the same stock as a level 1.
//
// DM:      GET  /api/gm/shops?campaign_id=N                       the shop types and the campaign's open shops
//          POST /api/gm/shops {"campaign_id": N, "open": "blacksmith", "name": "Hammer and Tongs", "level_range": "auto"}
//               options: price_percent (100 = SRD prices), firearms (true: muskets and pistols), legendary (magic shop),
//               stock_per_rarity (magic shop: how many items of each rarity it has; 0 = all)
//          POST /api/gm/shops {"campaign_id": N, "close": shop_id}
// Players: GET  /api/campaigns/{id}/shops                           the open shops
//          GET  /api/campaigns/{id}/shops/{shop_id}?character_id=C  what it sells to this character, with prices
//          POST /api/campaigns/{id}/shops/{shop_id} {"character_id": C, "buy": [{"item": ..., "quantity": 1, "base": ..., "variant": ..., "spell": ...}]}
//
// Stock by level (the SRD's tiers of play, 1-4, 5-10, 11-16, 17-20): the shop shows each character the stock of that character's
// tier, unless the DM fixes a level_range. Ordinary goods: up to 75 GP an item at tier 1 (a village or town), everything from
// tier 2. Magic items by rarity (SRD: common items in towns, uncommon and rare in cities, rarer ones in wondrous places): tier 1
// Common, tier 2 up to Uncommon, tier 3 up to Rare, tier 4 up to Very Rare; Legendary only when the DM allows it. Spell Scrolls:
// cantrip and level 1 at tier 1, up to level 3 at tier 2, 5 at tier 3, 7 at tier 4.
// Prices: the SRD's equipment costs; magic items by the SRD's Magic Item Rarities and Values table (Common 100 GP, Uncommon 400,
// Rare 4,000, Very Rare 40,000, Legendary 200,000), half for a consumable, plus the cost of the armor or weapon it is made from;
// a Spell Scroll at twice its scribing cost.

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------- the SRD's "Varies" items, by variant

func shopVariantRows() []catalogRow {
	mk := func(cat, name, cost string, gp float64) catalogRow {
		return catalogRow{Category: cat, Slug: itemSlug(name), Name: name, Source: "SRD 5.2.1", CostText: cost, CostGP: gp, Data: map[string]interface{}{}}
	}
	return []catalogRow{
		mk("gear", "Arcane Focus, Crystal", "10 GP", 10), mk("gear", "Arcane Focus, Orb", "20 GP", 20), mk("gear", "Arcane Focus, Rod", "10 GP", 10),
		mk("gear", "Arcane Focus, Staff", "5 GP", 5), mk("gear", "Arcane Focus, Wand", "10 GP", 10),
		mk("gear", "Druidic Focus, Sprig of Mistletoe", "1 GP", 1), mk("gear", "Druidic Focus, Wooden Staff", "5 GP", 5), mk("gear", "Druidic Focus, Yew Wand", "10 GP", 10),
		mk("gear", "Holy Symbol, Amulet", "5 GP", 5), mk("gear", "Holy Symbol, Emblem", "5 GP", 5), mk("gear", "Holy Symbol, Reliquary", "5 GP", 5),
		mk("tool", "Bagpipes", "30 GP", 30), mk("tool", "Drum", "6 GP", 6), mk("tool", "Dulcimer", "25 GP", 25), mk("tool", "Flute", "2 GP", 2),
		mk("tool", "Horn", "3 GP", 3), mk("tool", "Lute", "35 GP", 35), mk("tool", "Lyre", "30 GP", 30), mk("tool", "Pan Flute", "12 GP", 12),
		mk("tool", "Shawm", "2 GP", 2), mk("tool", "Viol", "30 GP", 30),
		mk("tool", "Dice Set", "1 SP", 0.1), mk("tool", "Dragonchess Set", "1 GP", 1), mk("tool", "Playing Card Set", "5 SP", 0.5), mk("tool", "Three-Dragon Ante Set", "1 GP", 1),
		mk("gear", "Spellbook", "50 GP", 50), // Adventuring Gear (SRD 5.2.1): missing from the equipment data, so a Wizard couldn't buy one (Nov 14 review)
	}
}

func focusKindOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "arcane focus"):
		return "arcane"
	case strings.HasPrefix(n, "holy symbol"):
		return "divine"
	case strings.HasPrefix(n, "druidic focus"):
		return "primal"
	}
	for _, i := range []string{"bagpipes", "drum", "dulcimer", "flute", "horn", "lute", "lyre", "pan flute", "shawm", "viol"} {
		if n == i {
			return "instrument"
		}
	}
	return ""
}

// ---------------------------------------------------------------- shop types

type shopTypeDef struct {
	Key, Name, Sells string
	ordinary         func(it shopItem) bool // which ordinary goods it stocks
	potions, magic   bool                   // magic potions; all magic items
	scrolls          string                 // "" or the class list its Spell Scrolls come from
}

func gearIn(names ...string) func(it shopItem) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[itemKey(n)] = true
	}
	return func(it shopItem) bool { return set[itemKey(it.row.Name)] }
}

func anyOf(fs ...func(it shopItem) bool) func(it shopItem) bool {
	return func(it shopItem) bool {
		for _, f := range fs {
			if f(it) {
				return true
			}
		}
		return false
	}
}

func armorType(it shopItem) string {
	s, _ := it.row.Data["armor_type"].(string)
	return strings.ToLower(s)
}
func weaponKind(it shopItem) string {
	s, _ := it.row.Data["type"].(string)
	return strings.ToLower(s)
}

var leatherGoods = gearIn("Backpack", "Pouch", "Quiver", "Case, Crossbow Bolt", "Case, Map or Scroll", "Waterskin", "Sling", "Whip", "Saddle, Riding",
	"Saddle, Military", "Saddle, Exotic", "Leatherworker’s Tools", "Cobbler’s Tools", "Blanket", "Bedroll")

var shopTypes = []shopTypeDef{
	{Key: "blacksmith", Name: "Blacksmith", Sells: "Melee and thrown weapons, metal armor and Shields, iron goods and Smith's Tools.",
		ordinary: anyOf(func(it shopItem) bool {
			return it.category == "weapons" && weaponKind(it) == "melee" && it.row.Name != "Whip"
		}, func(it shopItem) bool {
			return it.category == "armor" && (armorType(it) == "medium" || armorType(it) == "heavy" || armorType(it) == "shield") && it.row.Name != "Hide Armor"
		}, gearIn("Chain", "Manacles", "Spikes, Iron", "Pot, Iron", "Lock", "Crowbar", "Grappling Hook", "Shovel", "Hunting Trap", "Caltrops",
			"Ball Bearings", "Smith’s Tools", "Ram, Portable", "Block and Tackle"))},
	{Key: "leatherworker", Name: "Leatherworker", Sells: "Light armor and Hide Armor, packs, pouches, quivers, cases, saddles, slings and whips.",
		ordinary: anyOf(func(it shopItem) bool {
			return it.category == "armor" && (armorType(it) == "light" || it.row.Name == "Hide Armor")
		}, leatherGoods)},
	{Key: "bowyer", Name: "Bowyer and Fletcher", Sells: "Bows, crossbows, blowguns, darts and slings, ammunition, quivers and cases.",
		ordinary: anyOf(func(it shopItem) bool {
			return it.category == "weapons" && (weaponKind(it) == "ranged" || it.row.Name == "Dart")
		}, func(it shopItem) bool { return it.category == "ammunition" }, gearIn("Quiver", "Case, Crossbow Bolt", "Woodcarver’s Tools"))},
	{Key: "general_store", Name: "General Store", Sells: "Adventuring gear: packs, rations, rope, light, camping and climbing gear, clothes, containers, writing supplies.",
		ordinary: func(it shopItem) bool {
			if it.category != "gear" {
				return false
			}
			switch {
			case strings.Contains(it.row.Name, "Focus"), strings.HasPrefix(it.row.Name, "Holy"), it.row.Name == "Acid", it.row.Name == "Alchemist’s Fire",
				it.row.Name == "Antitoxin", it.row.Name == "Poison, Basic", it.row.Name == "Potion of Healing", strings.HasPrefix(it.row.Name, "Spell Scroll"),
				it.row.Name == "Component Pouch", it.row.Name == "Healer’s Kit", it.row.Name == "Spyglass", it.row.Name == "Magnifying Glass":
				return false
			}
			return true
		}},
	{Key: "alchemist", Name: "Alchemist and Herbalist", Sells: "Acid, Alchemist's Fire, Antitoxin, oils and poisons, vials and flasks, Druidic Focuses, alchemy and herbalism kits, and potions (magic potions by level).",
		ordinary: gearIn("Acid", "Alchemist’s Fire", "Antitoxin", "Poison, Basic", "Oil", "Perfume", "Vial", "Flask", "Bottle, Glass", "Component Pouch",
			"Alchemist’s Supplies", "Herbalism Kit", "Poisoner’s Kit", "Brewer’s Supplies", "Glassblower’s Tools",
			"Druidic Focus, Sprig of Mistletoe", "Druidic Focus, Wooden Staff", "Druidic Focus, Yew Wand"),
		potions: true},
	{Key: "temple", Name: "Temple", Sells: "Holy Water, Holy Symbols, Healer's Kits, candles and prayer books, Potions of Healing, and Cleric Spell Scrolls by level.",
		ordinary: gearIn("Holy Water", "Healer’s Kit", "Holy Symbol, Amulet", "Holy Symbol, Emblem", "Holy Symbol, Reliquary", "Candle", "Book", "Priest’s Pack",
			"Potion of Healing", "Robe"),
		scrolls: "cleric"},
	{Key: "craftsman", Name: "Craftsman's Guild", Sells: "Artisan's tools, musical instruments, gaming sets and specialist kits.",
		ordinary: func(it shopItem) bool { return it.category == "tools" && !strings.Contains(it.row.Name, "Poisoner") }},
	{Key: "stable", Name: "Stable", Sells: "Mounts, saddles, feed and stabling, carts, wagons and other land vehicles.",
		ordinary: func(it shopItem) bool {
			if it.category == "mounts" {
				return true
			}
			return it.category == "vehicles" && !containsStr([]string{"Airship", "Galley", "Keelboat", "Longship", "Rowboat", "Sailing Ship", "Warship"}, it.row.Name)
		}},
	{Key: "shipwright", Name: "Shipwright", Sells: "Boats and ships.",
		ordinary: func(it shopItem) bool {
			return it.category == "vehicles" && containsStr([]string{"Airship", "Galley", "Keelboat", "Longship", "Rowboat", "Sailing Ship", "Warship"}, it.row.Name)
		}},
	{Key: "magic_shop", Name: "Magic Shop", Sells: "Magic items by rarity for the buyer's level, Arcane Focuses, Component Pouches, and Wizard Spell Scrolls by level.",
		ordinary: gearIn("Arcane Focus, Crystal", "Arcane Focus, Orb", "Arcane Focus, Rod", "Arcane Focus, Staff", "Arcane Focus, Wand", "Component Pouch",
			"Book", "Ink", "Ink Pen", "Parchment", "Paper", "Magnifying Glass", "Spyglass"),
		potions: true, magic: true, scrolls: "wizard"},
}

func shopTypeByKey(k string) (shopTypeDef, bool) {
	for _, t := range shopTypes {
		if t.Key == strings.ToLower(strings.TrimSpace(k)) {
			return t, true
		}
	}
	return shopTypeDef{}, false
}

// ---------------------------------------------------------------- tiers

func tierOfLevel(l int) int {
	switch {
	case l >= 17:
		return 4
	case l >= 11:
		return 3
	case l >= 5:
		return 2
	}
	return 1
}

var tierRanges = map[int]string{1: "levels 1-4", 2: "levels 5-10", 3: "levels 11-16", 4: "levels 17-20"}
var shopRarityOrder = []string{"Common", "Uncommon", "Rare", "Very Rare", "Legendary"}
var rarityValueGP = map[string]int{"Common": 100, "Uncommon": 400, "Rare": 4000, "Very Rare": 40000, "Legendary": 200000}
var maxScrollLevel = map[int]int{1: 1, 2: 3, 3: 5, 4: 7}

func rarityAllowed(r string, tier int, legendary bool) bool {
	i := map[string]int{"Common": 1, "Uncommon": 2, "Rare": 3, "Very Rare": 4, "Legendary": 5}[r]
	if i == 5 {
		return legendary && tier == 4
	}
	return i > 0 && i <= tier
}

// ---------------------------------------------------------------- magic items for sale

type magicOffer struct {
	Name, Short, Rarity, Type, Description string // Name: the SRD's name ("Weapon, +1, +2, or +3"); Short: without the variants ("Weapon")
	ValueGP                                int    // the rarity value (halved for a consumable), without a base item
	Consumable, Attunement                 bool
	Bases                                  []string // the weapon or armor it can be made from ([] = none needed)
	Variants                               []string // "+1", "+2", "+3" when it comes in those
}

var plusVariants = regexp.MustCompile(`,\s*\+1,\s*\+2,\s*or\s*\+3`)
var baseList = regexp.MustCompile(`^(Weapon|Armor) \((.+)\)$`)

func magicBasesFor(itemType string) []string {
	m := baseList.FindStringSubmatch(itemType)
	if m == nil {
		return nil
	}
	inner := m[2]
	var out []string
	switch {
	case strings.HasPrefix(inner, "Any"):
		for _, it := range shopCatalog() {
			if m[1] == "Weapon" && it.category == "weapons" {
				if strings.Contains(inner, "Melee") && weaponKind(it) != "melee" {
					continue
				}
				if strings.Contains(inner, "Ammunition") {
					continue
				}
				out = append(out, it.row.Name)
			}
			if m[1] == "Armor" && it.category == "armor" && it.row.Name != "Shield" {
				if strings.Contains(inner, "Medium or Heavy") && armorType(it) == "light" || strings.Contains(inner, "Except Hide") && it.row.Name == "Hide Armor" {
					continue
				}
				out = append(out, it.row.Name)
			}
		}
		if strings.Contains(inner, "Ammunition") {
			for _, it := range shopCatalog() {
				if it.category == "ammunition" {
					out = append(out, it.row.Name)
				}
			}
		}
	default:
		for _, p := range regexp.MustCompile(`,\s*(?:or\s+)?|\s+or\s+`).Split(inner, -1) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

var magicOffersOnce sync.Once
var magicOffersList []magicOffer

func magicOffers() []magicOffer {
	magicOffersOnce.Do(func() {
		for _, r := range magicItemRows() {
			rar, _ := r.Data["rarity"].(string)
			typ, _ := r.Data["item_type"].(string)
			desc, _ := r.Data["description"].(string)
			att, _ := r.Data["requires_attunement"].(bool)
			o := magicOffer{Name: r.Name, Rarity: rar, Type: typ, Description: desc, Attunement: att}
			o.Consumable = typ == "Potion" || strings.Contains(typ, "Ammunition")
			if r.Name == "Potions of Healing" { // the four Potions of Healing (SRD: Common, Uncommon, Rare, Very Rare)
				for i, n := range []string{"Potion of Healing", "Potion of Greater Healing", "Potion of Superior Healing", "Potion of Supreme Healing"} {
					magicOffersList = append(magicOffersList, magicOffer{Name: n, Rarity: shopRarityOrder[i], Type: "Potion", Description: desc, Consumable: true, ValueGP: rarityValueGP[shopRarityOrder[i]] / 2})
				}
				continue
			}
			if rar == "Rarity Varies" || rar == "Artifact" || r.Name == "Spell Scroll" {
				continue // their rarity depends on a variant the SRD names item by item; the DM gives those
			}
			o.Short = r.Name
			if plusVariants.MatchString(r.Name) {
				o.Short = strings.TrimSpace(plusVariants.ReplaceAllString(r.Name, ""))
				o.Variants = []string{"+1", "+2", "+3"}
			}
			o.Bases = magicBasesFor(typ)
			if strings.Contains(typ, "Shield") {
				o.Bases = []string{"Shield"}
			}
			o.ValueGP = rarityValueGP[rar]
			if o.Consumable {
				o.ValueGP /= 2
			}
			magicOffersList = append(magicOffersList, o)
		}
	})
	return magicOffersList
}

// variantRarity: +1 is the item's own rarity, each + one rarity higher.
func variantRarity(base, variant string) string {
	i := 0
	for j, r := range shopRarityOrder {
		if r == base {
			i = j
		}
	}
	i += map[string]int{"+1": 0, "+2": 1, "+3": 2}[variant]
	if i >= len(shopRarityOrder) {
		i = len(shopRarityOrder) - 1
	}
	return shopRarityOrder[i]
}

// ---------------------------------------------------------------- the campaign's shops

var campaignShopsOnce sync.Once

func ensureCampaignShops() {
	campaignShopsOnce.Do(func() {
		db.Exec(`CREATE TABLE IF NOT EXISTS campaign_shops (id SERIAL PRIMARY KEY, lobby_id INT NOT NULL, shop_type TEXT NOT NULL, name TEXT NOT NULL,
			options JSONB NOT NULL DEFAULT '{}', opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), closed_at TIMESTAMPTZ)`)
	})
}

type campaignShop struct {
	ID, Lobby  int
	Type, Name string
	Options    shopOptions
}

type shopOptions struct {
	LevelRange     string `json:"level_range,omitempty"`   // "auto" (each buyer's tier) or "1-4", "5-10", "11-16", "17-20"
	PricePercent   int    `json:"price_percent,omitempty"` // 100 = SRD prices
	Firearms       bool   `json:"firearms,omitempty"`
	Legendary      bool   `json:"legendary,omitempty"`
	StockPerRarity int    `json:"stock_per_rarity,omitempty"` // magic shop; 0 = 8; -1 = all
}

func loadShop(lobby, id int) (*campaignShop, bool) {
	ensureCampaignShops()
	var s campaignShop
	var raw []byte
	err := db.QueryRow(`SELECT id, lobby_id, shop_type, name, options FROM campaign_shops WHERE id = $1 AND lobby_id = $2 AND closed_at IS NULL`, id, lobby).
		Scan(&s.ID, &s.Lobby, &s.Type, &s.Name, &raw)
	if err != nil {
		return nil, false
	}
	json.Unmarshal(raw, &s.Options)
	return &s, true
}

func openShops(lobby int) []campaignShop {
	ensureCampaignShops()
	rows, err := db.Query(`SELECT id, lobby_id, shop_type, name, options FROM campaign_shops WHERE lobby_id = $1 AND closed_at IS NULL ORDER BY id`, lobby)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []campaignShop
	for rows.Next() {
		var s campaignShop
		var raw []byte
		rows.Scan(&s.ID, &s.Lobby, &s.Type, &s.Name, &raw)
		json.Unmarshal(raw, &s.Options)
		out = append(out, s)
	}
	return out
}

func (s *campaignShop) tierFor(level int) int {
	switch s.Options.LevelRange {
	case "1-4":
		return 1
	case "5-10":
		return 2
	case "11-16":
		return 3
	case "17-20":
		return 4
	}
	return tierOfLevel(level)
}

func (s *campaignShop) priceCP(gp float64) int {
	p := s.Options.PricePercent
	if p <= 0 {
		p = 100
	}
	return int(gp*float64(p) + 0.5) // gp * 100 cp * p / 100
}

// stableHash picks the same magic stock for a shop every time (it changes only when the DM opens a new shop).
func stableHash(parts ...string) uint32 {
	h := fnv.New32a()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return h.Sum32()
}

// shopEntry is one thing a shop sells to one character.
type shopEntry struct {
	view    map[string]interface{}
	kind    string // ordinary, magic, scroll
	item    shopItem
	magic   magicOffer
	rarity  string
	scrollL int
}

func (s *campaignShop) stockFor(charID int) ([]shopEntry, int) {
	def, _ := shopTypeByKey(s.Type)
	var level int
	var weaponProfs, armorProfs string
	db.QueryRow(`SELECT COALESCE(level,1), COALESCE(weapon_proficiencies,''), COALESCE(armor_proficiencies,'') FROM characters WHERE id = $1`, charID).Scan(&level, &weaponProfs, &armorProfs)
	tier := s.tierFor(level)
	money := walletCP(charID)
	var out []shopEntry
	if def.ordinary != nil {
		for _, it := range shopCatalog() {
			if !def.ordinary(it) {
				continue
			}
			if !s.Options.Firearms && (it.row.Name == "Musket" || it.row.Name == "Pistol" || it.row.Name == "Firearm Bullets") {
				continue
			}
			if tier == 1 && it.row.CostGP > 75 && def.Key != "stable" && def.Key != "shipwright" {
				continue // a village or town stocks the everyday goods; the expensive ones come at tier 2 (larger towns and cities)
			}
			v := shopItemView(it, weaponProfs, armorProfs, money)
			cp := s.priceCP(it.row.CostGP)
			v["price"] = moneyText(cp)
			v["you_can_afford_one"] = cp <= money
			out = append(out, shopEntry{view: v, kind: "ordinary", item: it})
		}
	}
	if def.potions || def.magic {
		per := s.Options.StockPerRarity
		if per == 0 {
			per = 8
		}
		byRarity := map[string][]magicOffer{}
		for _, o := range magicOffers() {
			if !def.magic && o.Type != "Potion" {
				continue
			}
			if def.Key == "magic_shop" && o.Type == "Potion" && !strings.Contains(o.Name, "Healing") {
				// the magic shop sells Potions of Healing; the other potions are the alchemist's
			}
			rar := o.Rarity
			if len(o.Variants) > 0 {
				rar = o.Rarity // listed under the rarity of +1; +2 and +3 are checked at purchase
			}
			if rarityAllowed(rar, tier, s.Options.Legendary) {
				byRarity[rar] = append(byRarity[rar], o)
			}
		}
		for _, rar := range shopRarityOrder {
			list := byRarity[rar]
			sort.Slice(list, func(i, j int) bool {
				return stableHash(strconv.Itoa(s.ID), list[i].Name) < stableHash(strconv.Itoa(s.ID), list[j].Name)
			})
			if per > 0 && def.magic {
				var staples, rest []magicOffer
				for _, o := range list {
					if len(o.Variants) > 0 || strings.Contains(o.Name, "Healing") {
						staples = append(staples, o) // +1/+2/+3 weapons, armor, shields, ammunition and Potions of Healing are always in stock
					} else {
						rest = append(rest, o)
					}
				}
				if len(rest) > per {
					rest = rest[:per]
				}
				list = append(staples, rest...)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			for _, o := range list {
				v := map[string]interface{}{"item": o.Name, "category": "magic item", "magic_item_type": o.Type, "rarity": o.Rarity, "what_it_does": o.Description}
				if o.Attunement {
					v["requires_attunement"] = true
				}
				price := s.priceCP(float64(o.ValueGP))
				if len(o.Variants) > 0 {
					var vs []string
					for _, vr := range o.Variants {
						r := variantRarity(o.Rarity, vr)
						if rarityAllowed(r, tier, s.Options.Legendary) {
							vs = append(vs, fmt.Sprintf("%s (%s, %s)", vr, r, moneyText(s.priceCP(float64(rarityValueGP[r])))))
						}
					}
					v["variants"] = vs
					v["send_variant"] = "variant: \"+1\" (or a higher one listed)"
				}
				if len(o.Bases) > 0 {
					v["made_from"] = o.Bases
					v["send_base"] = "base: the weapon or armor it is made from (its cost is added to the price)"
				}
				v["price"] = moneyText(price)
				if len(o.Bases) > 0 {
					v["price"] = moneyText(price) + " plus the base item's cost"
				}
				if o.Consumable {
					v["consumable"] = "price halved (SRD: a consumable's value is half its rarity value)"
				}
				v["you_can_afford_one"] = price <= money
				out = append(out, shopEntry{view: v, kind: "magic", magic: o, rarity: o.Rarity})
			}
		}
	}
	if def.scrolls != "" {
		maxL := maxScrollLevel[tier]
		var spells []string
		for _, k := range getClassSpellList(def.scrolls) {
			if sp, ok := srdSpellsMemory[k]; ok && sp.Level <= maxL {
				spells = append(spells, k)
			}
		}
		sort.Strings(spells)
		var prices []string
		for l := 0; l <= maxL; l++ {
			prices = append(prices, fmt.Sprintf("%s: %s", map[bool]string{true: "cantrip", false: "level " + strconv.Itoa(l)}[l == 0], moneyText(s.priceCP(float64(scrollPriceGP(l))))))
		}
		v := map[string]interface{}{"item": "Spell Scroll", "category": "magic item", "from_the_list": titleCase(def.scrolls),
			"spell_levels": fmt.Sprintf("cantrip to level %d", maxL), "price_by_spell_level": prices, "spells": spells,
			"send_spell": "spell: the spell's slug", "what_it_does": "A Spell Scroll bears the words of a single spell. If the spell is on your class's spell list, you can read the scroll and cast its spell without Material components (SRD 5.2.1 Spell Scroll). Its value is twice the cost of scribing it."}
		out = append(out, shopEntry{view: v, kind: "scroll", scrollL: maxL})
	}
	return out, tier
}

// scrollPriceGP: a Spell Scroll's value is twice what it costs to scribe (SRD 5.2.1); the Equipment table prices a cantrip at 30 GP and level 1 at 50 GP.
func scrollPriceGP(level int) int {
	return 2 * scrollScribing[level][1]
}

// ---------------------------------------------------------------- handlers

func handleGMShops(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	ensureCampaignShops()
	var types []map[string]interface{}
	for _, t := range shopTypes {
		types = append(types, map[string]interface{}{"shop": t.Key, "name": t.Name, "sells": t.Sells})
	}
	stockRule := "Each character sees the stock for their level: ordinary goods up to 75 GP at levels 1-4, everything from level 5; magic items Common at 1-4, up to Uncommon at 5-10, Rare at 11-16, Very Rare at 17-20 (Legendary only with legendary: true); Spell Scrolls up to level 1, 3, 5 and 7. level_range fixes one range for everyone."
	if r.Method == http.MethodGet {
		var cid int
		fmt.Sscanf(r.URL.Query().Get("campaign_id"), "%d", &cid)
		out := map[string]interface{}{"shop_types": types, "stock_by_level": stockRule,
			"open_a_shop":  "POST /api/gm/shops {\"campaign_id\": N, \"open\": \"blacksmith\", \"name\": \"Hammer and Tongs\"} (options: level_range \"auto\" or \"1-4\" ..., price_percent, firearms, legendary, stock_per_rarity)",
			"close_a_shop": "POST /api/gm/shops {\"campaign_id\": N, \"close\": shop_id}"}
		if cid > 0 {
			var list []map[string]interface{}
			for _, s := range openShops(cid) {
				list = append(list, map[string]interface{}{"shop_id": s.ID, "name": s.Name, "shop": s.Type, "options": s.Options})
			}
			out["open_shops"] = list
		}
		writeJSONStatus(w, http.StatusOK, out)
		return
	}
	var req struct {
		CampaignID int    `json:"campaign_id"`
		Open       string `json:"open"`
		Name       string `json:"name"`
		Close      int    `json:"close"`
		shopOptions
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	var dm int
	if db.QueryRow(`SELECT COALESCE(dm_id,0) FROM lobbies WHERE id = $1`, req.CampaignID).Scan(&dm) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "campaign_not_found"})
		return
	}
	if dm != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "only_the_gm", "message": "Only the campaign's GM opens and closes shops."})
		return
	}
	if req.Close > 0 {
		res, _ := db.Exec(`UPDATE campaign_shops SET closed_at = NOW() WHERE id = $1 AND lobby_id = $2 AND closed_at IS NULL`, req.Close, req.CampaignID)
		if n, _ := res.RowsAffected(); n == 0 {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "shop_not_open", "message": fmt.Sprintf("Shop %d isn't open in this campaign.", req.Close)})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": fmt.Sprintf("Shop %d is closed.", req.Close)})
		return
	}
	t, ok := shopTypeByKey(req.Open)
	if !ok {
		var keys []string
		for _, x := range shopTypes {
			keys = append(keys, x.Key)
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_shop_type", "message": fmt.Sprintf("%q is not a shop type. Choose one of: %s.", req.Open, strings.Join(keys, ", ")), "shop_types": types})
		return
	}
	if lr := req.LevelRange; lr != "" && lr != "auto" && lr != "1-4" && lr != "5-10" && lr != "11-16" && lr != "17-20" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_level_range", "message": "level_range is \"auto\" (each buyer's own level) or one of 1-4, 5-10, 11-16, 17-20."})
		return
	}
	if req.PricePercent < 0 || req.PricePercent > 1000 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_price_percent", "message": "price_percent is 1 to 1000 (100 = the SRD's prices)."})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = t.Name
	}
	if len([]rune(name)) > 80 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "name_too_long", "message": "A shop name is at most 80 characters."})
		return
	}
	opts, _ := json.Marshal(req.shopOptions)
	var id int
	db.QueryRow(`INSERT INTO campaign_shops (lobby_id, shop_type, name, options) VALUES ($1, $2, $3, $4) RETURNING id`, req.CampaignID, t.Key, name, opts).Scan(&id)
	recordGMAction(db, req.CampaignID, "shop_open", fmt.Sprintf("%s (%s) is open: %s", name, t.Name, t.Sells), fmt.Sprintf("Shop %d open", id))
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "shop_id": id, "name": name, "shop": t.Key, "sells": t.Sells,
		"players_see_it_at": fmt.Sprintf("GET /api/campaigns/%d/shops/%d?character_id=N", req.CampaignID, id), "stock_by_level": stockRule})
}

// handleCampaignShops: /api/campaigns/{id}/shops[/{shop_id}]
func handleCampaignShops(w http.ResponseWriter, r *http.Request, campaignID int, parts []string) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	ensureCampaignShops()
	if len(parts) == 0 || parts[0] == "" {
		var list []map[string]interface{}
		for _, s := range openShops(campaignID) {
			t, _ := shopTypeByKey(s.Type)
			list = append(list, map[string]interface{}{"shop_id": s.ID, "name": s.Name, "shop": t.Name, "sells": t.Sells,
				"see_what_it_sells": fmt.Sprintf("GET /api/campaigns/%d/shops/%d?character_id=N", campaignID, s.ID)})
		}
		msg := "No shop is open. Your GM opens shops when you are somewhere you can buy things."
		if len(list) > 0 {
			msg = "These shops are open."
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"open_shops": list, "message": msg})
		return
	}
	sid, _ := strconv.Atoi(parts[0])
	shop, ok := loadShop(campaignID, sid)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "shop_not_open", "message": fmt.Sprintf("Shop %s isn't open in this campaign (GET /api/campaigns/%d/shops lists the open ones).", parts[0], campaignID)})
		return
	}
	var charID int
	if r.Method == http.MethodGet {
		fmt.Sscanf(r.URL.Query().Get("character_id"), "%d", &charID)
	}
	var req struct {
		CharacterID int `json:"character_id"`
		Buy         []struct {
			Item     string `json:"item"`
			Quantity int    `json:"quantity"`
			Base     string `json:"base"`
			Variant  string `json:"variant"`
			Spell    string `json:"spell"`
		} `json:"buy"`
		Sell []sellReq `json:"sell"`
	}
	if r.Method == http.MethodPost {
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
		charID = req.CharacterID
	}
	var owner, lobby int
	if db.QueryRow(`SELECT COALESCE(agent_id,0), COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&owner, &lobby) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "Send character_id: the character who shops (yours, in this campaign)."})
		return
	}
	if owner != agentID || lobby != campaignID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character_here", "message": "That character isn't yours or isn't in this campaign."})
		return
	}
	stock, tier := shop.stockFor(charID)
	t, _ := shopTypeByKey(shop.Type)
	money := walletCP(charID)
	if r.Method == http.MethodGet {
		var items []map[string]interface{}
		for _, e := range stock {
			items = append(items, e.view)
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"shop_id": shop.ID, "name": shop.Name, "shop": t.Name, "sells": t.Sells,
			"stock_for": fmt.Sprintf("%s (tier %d)", tierRanges[tier], tier), "your_money": moneyText(money), "items": items,
			"offers_for_your_items": sellOffers(charID, &t, shop.priceCP),
			"how_to_sell":           fmt.Sprintf("POST /api/campaigns/%d/shops/%d {\"character_id\": %d, \"sell\": [{\"item_id\": \"<id>\", \"quantity\": 1}]}: the shop pays half its own price.", campaignID, shop.ID, charID),
			"how_to_buy":            fmt.Sprintf("POST /api/campaigns/%d/shops/%d {\"character_id\": %d, \"buy\": [{\"item\": \"<item>\", \"quantity\": 1}]}; a magic item made from a weapon or armor also takes base, a +1/+2/+3 item takes variant, a Spell Scroll takes spell.", campaignID, shop.ID, charID)})
		return
	}
	if len(req.Sell) > 0 && len(req.Buy) == 0 {
		status, resp := sellItems(charID, &t, shop.Name, shop.priceCP, req.Sell)
		if status == http.StatusOK {
			resp["shop"] = shop.Name
			var cname string
			db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&cname)
			recordGMAction(db, campaignID, "shop_sale", fmt.Sprintf("%s sold at %s: %v", cname, shop.Name, resp["sold"]), "")
		}
		writeJSONStatus(w, status, resp)
		return
	}
	if len(req.Buy) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_buy", "message": "Send buy (a list of {\"item\": ..., \"quantity\": ...}) or sell (a list of {\"item_id\": ..., \"quantity\": ...}), not both."})
		return
	}
	total := 0
	var kit []startItem
	var lines []string
	refuse := func(code, msg string) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg})
	}
	for _, b := range req.Buy {
		q := b.Quantity
		if q == 0 {
			q = 1
		}
		if q < 1 || q > 100 {
			refuse("invalid_quantity", fmt.Sprintf("%s: quantity must be 1 to 100.", b.Item))
			return
		}
		var found *shopEntry
		for i := range stock {
			n, _ := stock[i].view["item"].(string)
			if itemKey(n) == itemKey(b.Item) || (stock[i].kind == "magic" && stock[i].magic.Short != stock[i].magic.Name && itemKey(stock[i].magic.Short) == itemKey(b.Item)) {
				found = &stock[i]
				break
			}
		}
		if found == nil {
			msg := fmt.Sprintf("%s doesn't sell %q to you. GET /api/campaigns/%d/shops/%d?character_id=%d lists what it sells for your level.", shop.Name, b.Item, campaignID, shop.ID, charID)
			if note := phbNote("feat", b.Item); note != "" {
				msg = note
			} else if si, isOrdinary := findShopItem(b.Item); isOrdinary {
				msg = fmt.Sprintf("%s doesn't stock %s; another kind of shop may (GET /api/campaigns/%d/shops lists the open ones).", shop.Name, si.row.Name, campaignID)
				if t.ordinary != nil && t.ordinary(si) && tier == 1 && si.row.CostGP > 75 {
					msg = fmt.Sprintf("%s sells %s (%s) from level 5: at levels 1-4 a town's %s stocks goods up to 75 GP.", shop.Name, si.row.Name, si.row.CostText, strings.ToLower(t.Name))
				}
			} else if craftMagicItem(b.Item) {
				rar := "its rarity"
				for _, o := range magicOffers() {
					if itemKey(o.Name) == itemKey(b.Item) || itemKey(o.Short) == itemKey(b.Item) {
						rar = o.Rarity
					}
				}
				msg = fmt.Sprintf("%s (%s) isn't for sale to you here: at %s this shop sells magic items up to %s, and it has only some of each rarity in stock.", b.Item, rar, tierRanges[tier], maxRarityText(tier, shop.Options.Legendary))
			}
			refuse("not_sold_here", msg)
			return
		}
		switch found.kind {
		case "ordinary":
			cp := shop.priceCP(found.item.row.CostGP) * q
			total += cp
			name := found.item.row.Name
			if straight := strings.ReplaceAll(name, "’", "'"); packContents[strings.ToLower(straight)].Container != "" {
				name = straight
			}
			kit = append(kit, startItem{Name: name, Qty: q * found.item.amount, Focus: focusKindOf(name)})
			lines = append(lines, fmt.Sprintf("%d x %s: %s", q, found.item.row.Name, moneyText(cp)))
		case "magic":
			o := found.magic
			rar, name := o.Rarity, o.Name
			value := o.ValueGP
			if len(o.Variants) > 0 {
				if !containsStr(o.Variants, b.Variant) {
					refuse("variant_required", fmt.Sprintf("%s: send variant, \"+1\", \"+2\" or \"+3\".", o.Name))
					return
				}
				rar = variantRarity(o.Rarity, b.Variant)
				if !rarityAllowed(rar, tier, shop.Options.Legendary) {
					refuse("not_at_your_level", fmt.Sprintf("%s %s is %s; at %s this shop sells up to %s.", o.Short, b.Variant, rar, tierRanges[tier], maxRarityText(tier, shop.Options.Legendary)))
					return
				}
				value = rarityValueGP[rar]
				if o.Consumable {
					value /= 2
				}
			}
			baseCost := 0.0
			if len(o.Bases) > 0 {
				base := ""
				for _, x := range o.Bases {
					if strings.EqualFold(x, strings.TrimSpace(b.Base)) {
						base = x
					}
				}
				if base == "" {
					refuse("base_required", fmt.Sprintf("%s is made from one of: %s. Send base.", o.Name, strings.Join(o.Bases, ", ")))
					return
				}
				if bi, ok := findShopItem(base); ok {
					baseCost = bi.row.CostGP
				}
				switch {
				case len(o.Variants) > 0:
					name = strings.TrimSpace(base + " " + b.Variant) // "Longsword +2", "Plate Armor +1": the names the game reads (magic_items_2024.go)
				default:
					name = fmt.Sprintf("%s (%s)", o.Name, base)
				}
			} else if len(o.Variants) > 0 {
				name = o.Short + " " + b.Variant // "Wand of the War Mage +1"
			}
			cp := shop.priceCP(float64(value)+baseCost) * q
			total += cp
			kit = append(kit, startItem{Name: name, Qty: q})
			lines = append(lines, fmt.Sprintf("%d x %s (%s): %s", q, name, rar, moneyText(cp)))
		case "scroll":
			k := normalizeSpellSlug(b.Spell)
			sp, ok := srdSpellsMemory[k]
			if !ok {
				refuse("spell_required", "A Spell Scroll needs spell: a spell's slug from the list this shop gives.")
				return
			}
			if !isSpellOnClassList(k, t.scrolls) || sp.Level > found.scrollL {
				refuse("scroll_not_sold", fmt.Sprintf("%s doesn't sell a Spell Scroll of %s to you (%s list, up to level %d at %s).", shop.Name, sp.Name, titleCase(t.scrolls), found.scrollL, tierRanges[tier]))
				return
			}
			cp := shop.priceCP(float64(scrollPriceGP(sp.Level))) * q
			total += cp
			kit = append(kit, startItem{Name: "Spell Scroll (" + sp.Name + ")", Qty: q})
			lines = append(lines, fmt.Sprintf("%d x Spell Scroll (%s): %s", q, sp.Name, moneyText(cp)))
		}
	}
	status, resp := completePurchase(charID, money, total, kit, lines)
	if status == http.StatusOK {
		resp["shop"] = shop.Name
		var cname string
		db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&cname)
		recordGMAction(db, campaignID, "shop_purchase", fmt.Sprintf("%s bought at %s: %s", cname, shop.Name, strings.Join(lines, "; ")), "")
	}
	writeJSONStatus(w, status, resp)
}

func maxRarityText(tier int, legendary bool) string {
	if legendary && tier == 4 {
		return "Legendary"
	}
	return shopRarityOrder[tier-1]
}

// shopsOpenFor: the open shops, for my-turn.
func shopsOpenFor(lobby int) interface{} {
	if lobby == 0 {
		return nil
	}
	shops := openShops(lobby)
	if len(shops) == 0 {
		return nil
	}
	var out []map[string]interface{}
	for _, s := range shops {
		t, _ := shopTypeByKey(s.Type)
		out = append(out, map[string]interface{}{"shop_id": s.ID, "name": s.Name, "shop": t.Name, "sells": t.Sells,
			"see_what_it_sells": fmt.Sprintf("GET /api/campaigns/%d/shops/%d?character_id=N", lobby, s.ID)})
	}
	return out
}
