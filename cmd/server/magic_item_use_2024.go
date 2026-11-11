package main

// Using magic items (SRD 5.2.1): charges, spells cast from items, Spell Scrolls and potions (BUGS 54).
//
//	Charges   Every SRD item with charges has them read from its text: how many, how many come back at dawn ("1d6 + 1", "all")
//	          and its last-charge roll (a 1 on 1d20 destroys most wands and staffs). An inventory item keeps its own count
//	          ("charges", "charges_max"); until it is first used it is full. POST /api/characters/{id}/equipment/use-charge spends
//	          charges for a property that isn't a spell (Winged Boots, Wand of Paralysis ...); the GM sets a count with
//	          POST /api/characters/{id}/equipment/charges.
//	Dawn      Items regain charges at dawn. A Long Rest counts as the next dawn (table rule "dawn": "long_rest", the default), or
//	          only the GM's POST /api/gm/dawn does ("gm"); gm/pass-time of 24 hours or more is a dawn too.
//	Spells    "Spells Cast from Items": the spell is cast at the level the charges pay for, uses no spell slot and no components, and
//	          uses the item's save DC when the item gives one (otherwise the caster's); Concentration as usual. The cast action
//	          takes "item" (and "charges" or slot_level to cast a higher version where the item allows it). A spell that only an
//	          item gives the character is cast from that item.
//	Scrolls   Spell Scroll: a spell on your spell list (a Thief of level 13 or higher can use any scroll, with Intelligence); no
//	          Material components; the scroll's level sets the save DC and attack bonus (13/+5 up to 19/+11), or the scribe's
//	          numbers for a scroll a character scribed; a spell of a higher level than you can cast needs an ability check with your
//	          spellcasting ability (DC 10 + the spell's level), and a failure wipes the scroll; once the spell is cast the scroll
//	          crumbles to dust.
//	Potions   Drinking a potion, or giving it to another creature within 5 feet, takes a Bonus Action (2024 Dungeon Master's Guide);
//	          the potion takes effect at once and is used up. Healing, Heroism, Giant Strength, Resistance, Invulnerability, Speed,
//	          Invisibility, Poison, Vitality and the Elixir of Health are applied; the other potions show on the creature for their
//	          duration and the GM runs them.
//	Use Magic Device (Thief 13): when a charged property is used, a 6 on 1d6 means no charges are spent.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// ---------------------------------------------------------------- what the SRD says about each item

type itemSpell struct {
	Slug     string `json:"spell"`
	Name     string `json:"name"`
	Cost     int    `json:"charges"`               // charges for the version listed (0: no charges)
	Level    int    `json:"level,omitempty"`       // the level it is cast at for Cost (0: the spell's own level)
	Upcast   bool   `json:"upcast,omitempty"`      // each extra charge raises the level by 1
	MaxCost  int    `json:"max_charges,omitempty"` // upcast: the most charges at once
	PerLevel bool   `json:"per_level,omitempty"`   // one charge per spell level
	MaxLevel int    `json:"max_level,omitempty"`   // per_level: the highest level
	DC       int    `json:"save_dc,omitempty"`     // the item's save DC (0: the caster's)
	Note     string `json:"note,omitempty"`
	Once     bool   `json:"once_per_dawn,omitempty"` // once a dawn (the Luck Blade's Wish, a Crystal Ball's Suggestion)
}

type magicItemUse struct {
	Name   string
	Max    string // "7", "1d3"
	Regain string // "1d6 + 1", "all", "1", "" (none)
	Last   string // what happens on a 1 when the last charge is spent
	Spells []itemSpell
	Attune bool
	Hold   string // "hold" or "wear": the item works while held or worn
	Potion bool
	Text   string
}

var (
	itemUseOnce sync.Once
	itemUses    map[string]*magicItemUse // itemUseKey(name) -> what the item does
	spellByName map[string]string        // lower-case SRD spell name -> slug
)

func normQuotes(s string) string {
	return strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"").Replace(s)
}

func itemUseKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(normQuotes(name))), " ")
}

const spellNamePat = `[A-Z][\w'/-]*(?:\s+(?:(?:of|and|or|from|without|with|the|to)\s+)?[A-Z][\w'/-]*)*`

var (
	chargeNumPat = `(\d+d\d+(?:\s*\+\s*\d+)?|\d+|one|two|three|four|five|six|seven|eight|nine|ten)`
	chargesRe    = regexp.MustCompile(`(?i)\b(?:has|have|starts with|holds) ` + chargeNumPat + ` charges`)
	chargesOfRe  = regexp.MustCompile(`(?i)\b\d+ of its ` + chargeNumPat + ` charges`)
	regainRe     = regexp.MustCompile(`(?i)regains? (all|\d+d\d+(?:\s*\+\s*\d+)?|\d+) (?:of its )?expended charges? (?:daily |each day )?at dawn`)
	lastChargeRe = regexp.MustCompile(`(?i)last charge[^.]*?roll (?:a )?1d20\. On a 1, ([^.]+)\.`)
	upcastRe     = regexp.MustCompile(`expend (?:no more than (\d+)|1 or more) charges to cast (` + spellNamePat + `)(?: \(save DC (\d+)\))?(?: from (?:it|them))?\. For 1 charge, you cast the level (\d+) version`)
	listCastRe   = regexp.MustCompile(`expend (\d+|one) (?:of (?:its|the [\w']+'s) (?:\d+ )?)?charges? to cast ((?:` + spellNamePat + `)(?:, (?:or )?` + spellNamePat + `)*(?:,? or ` + spellNamePat + `)?)(?: \(save DC (\d+)\))?`)
	followingRe  = regexp.MustCompile(`(?:expend (\d+) charges? to )?cast one of the following spells(?: \(save DC (\d+)\))? from it: (.+)`)
	saveDCRe     = regexp.MustCompile(`\(save DC (\d+)\)`)
	cellRe       = regexp.MustCompile(`^(` + spellNamePat + `)(?:\s*\(([^)]*)\))?\.?$`)
	levelVerRe   = regexp.MustCompile(`level (\d+) version`)
	expendsRe    = regexp.MustCompile(`expends? (\d+) charges?`)
	maximumRe    = regexp.MustCompile(`maximum (\d+)`)
	splitListRe  = regexp.MustCompile(`,? or |, `)
)

var chargeWords = map[string]string{"one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10"}

func loadItemUses() {
	itemUseOnce.Do(func() {
		itemUses, spellByName = map[string]*magicItemUse{}, map[string]string{}
		var spells []struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		}
		json.Unmarshal(srd521SpellsJSON, &spells)
		for _, s := range spells {
			spellByName[strings.ToLower(normQuotes(s.Name))] = s.Slug
		}
		var items []srdMagicItem
		json.Unmarshal(srd521MagicItemsJSON, &items)
		for _, m := range items {
			itemUses[itemUseKey(m.Name)] = parseItemUse(m)
		}
		extendItemUses(items) // Crystal Balls, the Luck Blade's Wish, the Ring of Elemental Command (legendary_more_2024.go)
	})
}

// parseItemUse reads an item's charges and spells from its SRD text.
func parseItemUse(m srdMagicItem) *magicItemUse {
	t := normQuotes(m.Description)
	u := &magicItemUse{Name: m.Name, Attune: m.RequiresAttunement, Text: m.Description,
		Potion: strings.HasPrefix(strings.ToLower(m.Type), "potion") || strings.HasPrefix(strings.ToLower(m.Name), "potion")}
	switch {
	case strings.Contains(t, "While wearing"):
		u.Hold = "wear"
	case strings.Contains(t, "While holding") || strings.Contains(t, "While you hold"):
		u.Hold = "hold"
	}
	if mm := chargesRe.FindStringSubmatch(t); mm != nil {
		u.Max = mm[1]
	} else if mm := chargesOfRe.FindStringSubmatch(t); mm != nil {
		u.Max = mm[1]
	}
	if w, ok := chargeWords[strings.ToLower(u.Max)]; ok {
		u.Max = w
	}
	if mm := regainRe.FindStringSubmatch(t); mm != nil {
		u.Regain = strings.ToLower(mm[1])
	}
	if mm := lastChargeRe.FindStringSubmatch(t); mm != nil {
		u.Last = mm[1]
	}
	if u.Max == "" {
		return u
	}
	casterDC := strings.Contains(t, "using your spell save DC") || strings.Contains(t, "using your spellcasting ability")
	itemDC := 0
	if !casterDC {
		if mm := saveDCRe.FindStringSubmatch(t); mm != nil {
			itemDC, _ = strconv.Atoi(mm[1])
		}
	}
	add := func(s itemSpell) {
		slug, ok := spellByName[strings.ToLower(s.Name)]
		if !ok {
			return // not a spell (a property of the item)
		}
		s.Slug = slug
		if s.DC == 0 {
			s.DC = itemDC
		}
		for _, have := range u.Spells {
			if have.Slug == s.Slug && have.Level == s.Level {
				return
			}
		}
		u.Spells = append(u.Spells, s)
	}
	for _, mm := range upcastRe.FindAllStringSubmatch(t, -1) {
		max := 9
		if mm[1] != "" {
			max, _ = strconv.Atoi(mm[1])
		}
		dc, _ := strconv.Atoi(mm[3])
		lvl, _ := strconv.Atoi(mm[4])
		add(itemSpell{Name: mm[2], Cost: 1, Level: lvl, Upcast: true, MaxCost: max, DC: dc})
	}
	for _, mm := range listCastRe.FindAllStringSubmatch(t, -1) {
		cost := 1
		if n, err := strconv.Atoi(mm[1]); err == nil {
			cost = n
		}
		dc, _ := strconv.Atoi(mm[3])
		for _, name := range splitListRe.Split(mm[2], -1) {
			if name = strings.TrimSpace(strings.TrimPrefix(name, "or ")); name != "" {
				add(itemSpell{Name: name, Cost: cost, DC: dc})
			}
		}
	}
	for _, mm := range followingRe.FindAllStringSubmatch(t, -1) {
		cost := 1
		if n, err := strconv.Atoi(mm[1]); err == nil {
			cost = n
		}
		dc, _ := strconv.Atoi(mm[2])
		list := mm[3]
		var parts []string
		if strings.Contains(list, "•") {
			parts = strings.Split(list, "•")
		} else {
			parts = splitListRe.Split(list, -1)
		}
		for _, p := range parts {
			s, ok := parseSpellCell(strings.TrimSpace(p))
			if !ok {
				continue
			}
			if s.Cost == 0 {
				s.Cost = cost
			}
			if s.DC == 0 {
				s.DC = dc
			}
			add(s)
		}
	}
	// spells in paragraphs after "one of the following spells" ("Gate. Pressing one side of the cube, you cast Gate"; the Cubic Gate)
	if len(u.Spells) == 0 {
		if i := strings.Index(t, "one of the following spells"); i >= 0 {
			for _, line := range strings.Split(t[i:], "\n")[1:] {
				if j := strings.Index(line, ". "); j > 0 && j <= 40 {
					add(itemSpell{Name: line[:j], Cost: 1})
				}
			}
		}
	}
	// spell tables ("Spell | Charge Cost")
	prevDC := 0
	for _, b := range m.DescriptionBlocks {
		if p, ok := b.(string); ok {
			prevDC = 0
			if mm := saveDCRe.FindStringSubmatch(normQuotes(p)); mm != nil && strings.Contains(p, "table") {
				prevDC, _ = strconv.Atoi(mm[1])
			}
			continue
		}
		tb, _ := b.(map[string]interface{})
		table, _ := tb["table"].(map[string]interface{})
		rows, _ := table["rows"].([]interface{})
		if len(rows) < 2 {
			continue
		}
		head := cellsOf(rows[0])
		for _, row := range rows[1:] {
			cells := cellsOf(row)
			for i := 0; i+1 < len(cells) && i+1 < len(head); i += 2 {
				if !strings.Contains(strings.ToLower(head[i]), "spell") || !strings.Contains(strings.ToLower(head[i+1]), "charge") || strings.TrimSpace(cells[i]) == "" {
					continue
				}
				s, ok := parseSpellCell(normQuotes(strings.TrimSpace(cells[i])))
				if !ok {
					continue
				}
				costText := strings.ToLower(cells[i+1])
				if n, err := strconv.Atoi(strings.TrimSpace(costText)); err == nil {
					s.Cost = n
				} else if strings.Contains(costText, "per spell level") {
					s.Cost, s.PerLevel = 1, true
					if mm := maximumRe.FindStringSubmatch(costText); mm != nil {
						s.MaxLevel, _ = strconv.Atoi(mm[1])
					}
				}
				if s.DC == 0 {
					s.DC = prevDC
				}
				add(s)
			}
		}
	}
	return u
}

func cellsOf(row interface{}) []string {
	list, _ := row.([]interface{})
	out := make([]string, len(list))
	for i, c := range list {
		out[i], _ = c.(string)
	}
	return out
}

// parseSpellCell reads "Fireball (level 5 version)", "Scrying (save DC 18)", "Heal (expends 1 charge)", "Fear (affects Beasts only)".
func parseSpellCell(cell string) (itemSpell, bool) {
	mm := cellRe.FindStringSubmatch(strings.TrimSpace(cell))
	if mm == nil {
		return itemSpell{}, false
	}
	s := itemSpell{Name: strings.TrimSpace(mm[1])}
	if note := strings.TrimSpace(mm[2]); note != "" {
		switch {
		case levelVerRe.MatchString(note):
			s.Level, _ = strconv.Atoi(levelVerRe.FindStringSubmatch(note)[1])
		case saveDCRe.MatchString("(" + note + ")"):
			s.DC, _ = strconv.Atoi(saveDCRe.FindStringSubmatch("(" + note + ")")[1])
		case expendsRe.MatchString(note):
			s.Cost, _ = strconv.Atoi(expendsRe.FindStringSubmatch(note)[1])
		default:
			s.Note = note
		}
	}
	return s, true
}

// ---------------------------------------------------------------- inventory items

var plusSuffixRe = regexp.MustCompile(`\s*,?\s*\+[123]\b`)

// itemUseFor finds what an inventory item is in the SRD (its "magic_item" field, or its name).
func itemUseFor(it invItem) *magicItemUse {
	loadItemUses()
	if u := customItemUse(it); u != nil { // the Moon card's wishes, a Ring of Elemental Command's plane (legendary_more_2024.go)
		return u
	}
	for _, n := range []string{itemStr(it, "magic_item"), itemStr(it, "name")} {
		if n == "" {
			continue
		}
		if u, ok := itemUses[itemUseKey(n)]; ok {
			return u
		}
		base := plusSuffixRe.ReplaceAllString(n, "")
		if u, ok := itemUses[itemUseKey(base)]; ok {
			return u
		}
		if base != n { // "Wand of the War Mage +2" is the SRD's "Wand of the War Mage, +1, +2, or +3"
			if u, ok := itemUses[itemUseKey(base+", +1, +2, or +3")]; ok {
				return u
			}
		}
	}
	return nil
}

// rollExpr rolls "1d6 + 4", "1d3", "7" (and returns n for a plain number).
func rollExpr(expr string) int {
	e := strings.ReplaceAll(strings.ToLower(expr), " ", "")
	if n, err := strconv.Atoi(e); err == nil {
		return n
	}
	if !strings.Contains(e, "d") {
		return 0
	}
	return game.RollDamage(e, false)
}

// itemCharges gives an item's charges now and at most. An item never used is full (a "1d3 charges" item rolls its count once).
func itemCharges(it invItem) (cur, max int, ok bool) {
	u := itemUseFor(it)
	if _, has := it["charges"]; has {
		cur, max = int(itemNum(it, "charges")), int(itemNum(it, "charges_max"))
		if max == 0 && u != nil {
			max = rollExpr(u.Max)
		}
		return cur, max, true
	}
	if u == nil || u.Max == "" {
		return 0, 0, false
	}
	max = rollExpr(u.Max)
	return max, max, true
}

func setItemCharges(it invItem, cur, max int) {
	if cur < 0 {
		cur = 0
	}
	it["charges"], it["charges_max"] = cur, max
	if u := itemUseFor(it); u != nil {
		it["magic_item"] = u.Name
	}
}

// itemPlace says whether an item is where it works: in hand ("hold"), or worn or in hand (otherwise), not in a bag.
func itemPlace(it invItem, u *magicItemUse) string {
	loc := itemStr(it, "location")
	if u != nil && u.Hold == "hold" && loc != "held" {
		return fmt.Sprintf("Hold the %s first (POST /api/characters/{id}/equipment/move {\"item\": \"%s\", \"to\": \"held\"}).", itemStr(it, "name"), itemStr(it, "name"))
	}
	if loc != "held" && loc != "worn" {
		return fmt.Sprintf("The %s is packed away: take it out first (equipment/move to \"held\" or \"worn\").", itemStr(it, "name"))
	}
	return ""
}

func thiefUseMagicDevice(charID int) bool {
	var class, subclass string
	var level int
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(subclass,''), level, COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).Scan(&class, &subclass, &level, &raw)
	if !strings.EqualFold(subclass, "thief") {
		return false
	}
	levels := map[string]int{}
	json.Unmarshal(raw, &levels)
	if n := levels["rogue"] + levels["Rogue"]; n > 0 {
		return n >= 13
	}
	return strings.EqualFold(class, "rogue") && level >= 13
}

// spendCharges spends n charges of the item it (in inv, which the caller saves). It returns a note, or a refusal.
func spendCharges(charID int, it invItem, n int) (string, string) {
	cur, max, ok := itemCharges(it)
	if !ok {
		return "", fmt.Sprintf("The %s has no charges.", itemStr(it, "name"))
	}
	if itemBool(it, "destroyed") {
		return "", fmt.Sprintf("The %s is destroyed.", itemStr(it, "name"))
	}
	if cur < n {
		return "", fmt.Sprintf("The %s has %d of %d charges left; this takes %d.", itemStr(it, "name"), cur, max, n)
	}
	note := ""
	if n > 0 && thiefUseMagicDevice(charID) {
		if d := game.RollDie(6); d == 6 {
			setItemCharges(it, cur, max)
			return fmt.Sprintf(" [Use Magic Device: rolled a 6, no charges spent; %d of %d left]", cur, max), ""
		}
	}
	cur -= n
	setItemCharges(it, cur, max)
	note = fmt.Sprintf(" [%s: %d charge(s) spent, %d of %d left]", itemStr(it, "name"), n, cur, max)
	if u := itemUseFor(it); cur == 0 && n > 0 && u != nil && u.Last != "" {
		d := game.RollDie(20)
		if d == 1 {
			it["destroyed"] = true
			note += fmt.Sprintf(" The last charge is gone: rolled a 1 on 1d20, so %s.", u.Last)
		} else {
			note += fmt.Sprintf(" The last charge is gone: rolled %d on 1d20, so the item survives.", d)
		}
	}
	return note, ""
}

// findInventoryItem finds an item by id or name among the usable items (not lost, not destroyed).
func findInventoryItem(inv []invItem, ref string) (invItem, error) {
	return findItem(usableInventory(inv), ref)
}

// ---------------------------------------------------------------- Spell Scrolls

var scrollNameRe = regexp.MustCompile(`(?i)^(?:spell\s+)?scroll\s*(?:\(|of\s+|:\s*)\s*([^,()]+?)\s*(?:,\s*level\s+(\d)\s*)?\)?$`)

var scrollNumbers = map[int][2]int{0: {13, 5}, 1: {13, 5}, 2: {13, 5}, 3: {15, 7}, 4: {15, 7}, 5: {17, 9}, 6: {17, 9}, 7: {18, 10}, 8: {18, 10}, 9: {19, 11}}

type scrollInfo struct {
	Slug        string
	Level       int
	DC, Attack  int
	CasterLevel int // a scribed cantrip works at the scribe's level
}

// scrollOf reads a Spell Scroll from an inventory item ("Spell Scroll (Fireball)", "Scroll of Fireball", or the fields a scribed
// scroll carries). ok is false for anything else.
func scrollOf(it invItem) (scrollInfo, bool) {
	var s scrollInfo
	if slug := itemStr(it, "spell"); slug != "" && strings.Contains(strings.ToLower(itemStr(it, "name")), "scroll") {
		s.Slug = slug
	} else {
		mm := scrollNameRe.FindStringSubmatch(strings.TrimSpace(itemStr(it, "name")))
		if mm == nil {
			return s, false
		}
		name := strings.ToLower(normQuotes(strings.TrimSpace(mm[1])))
		if name == "cantrip" || strings.HasPrefix(name, "level ") {
			return s, false // the shop's blank-price line, not a scroll of a spell
		}
		slug, ok := spellByName[name]
		if !ok {
			slug = spellSlugFromName(name)
		}
		s.Slug = slug
		if mm[2] != "" {
			s.Level, _ = strconv.Atoi(mm[2])
		}
	}
	sp, ok := srdSpellsMemory[s.Slug]
	if !ok {
		return s, false
	}
	if n := int(itemNum(it, "spell_level")); n > 0 {
		s.Level = n
	}
	if s.Level < sp.Level {
		s.Level = sp.Level
	}
	nums := scrollNumbers[s.Level]
	s.DC, s.Attack = nums[0], nums[1]
	if dc := int(itemNum(it, "save_dc")); dc > 0 {
		s.DC = dc
	}
	if atk := int(itemNum(it, "attack_bonus")); atk > 0 {
		s.Attack = atk
	}
	s.CasterLevel = int(itemNum(it, "caster_level"))
	return s, true
}

// onSpellList: the spell is on the spell list of one of the character's classes.
func onSpellList(charID int, slug string) bool {
	var class string
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).Scan(&class, &raw)
	classes := map[string]int{strings.ToLower(class): 1}
	levels := map[string]int{}
	json.Unmarshal(raw, &levels)
	for c := range levels {
		classes[strings.ToLower(c)] = 1
	}
	for c := range classes {
		if c != "" && containsStr(getClassSpellList(c), slug) {
			return true
		}
	}
	return false
}

// highestSlotLevel is the highest level of spell slot the character has (Pact Magic included).
func highestSlotLevel(charID int) int {
	var class string
	var level int
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class,''), level, COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &raw)
	best := 0
	consider := func(c string, l int) {
		for lvl := range game.SpellSlots(strings.ToLower(c), l) {
			if lvl > best {
				best = lvl
			}
		}
	}
	consider(class, level)
	levels := map[string]int{}
	json.Unmarshal(raw, &levels)
	for c, l := range levels {
		consider(c, l)
	}
	return best
}

// ---------------------------------------------------------------- casting from an item

// itemCastReq is what the action asked for: the item and the charges (cast action fields "item" and "charges").
type itemCastReq struct {
	Item      string
	Charges   int
	Target    int   // use_item: the character a potion is given to (0: the user)
	TargetID  int   // use_item: the action's target_id (a monster has a negative id: the Iron Flask)
	TargetIDs []int // use_item: the action's target_ids (the creatures a moved Sphere of Annihilation enters)
}

var castItems sync.Map      // character id -> itemCastReq, for the length of the action
var itemCastAttack sync.Map // character id -> the attack bonus of the scroll being read
var itemCastNotes sync.Map  // character id -> what the item cast spent (charges, the scroll), added to the action's result

// withoutMaterial drops the Material component from a components line ("V, S, M (a pinch of soot)" -> "V, S").
func withoutMaterial(components string) string {
	var keep []string
	for _, c := range strings.Split(components, ",") {
		c = strings.TrimSpace(c)
		if c == "" || strings.HasPrefix(strings.ToUpper(c), "M") {
			continue
		}
		keep = append(keep, c)
	}
	return strings.Join(keep, ", ")
}

// itemCastPlan is a cast the server will make from an item.
type itemCastPlan struct {
	ItemID      string
	ItemName    string
	Scroll      bool
	Level       int  // the level the spell is cast at
	Charges     int  // charges to spend
	DC          int  // the save DC to use (0: the caster's)
	Attack      int  // the spell attack bonus to use (0: the caster's)
	CasterLevel int  // for a cantrip: the caster level it works at (0: the lowest)
	NoMaterial  bool // a scroll: no Material components (a charged item needs no components at all)
	CheckNote   string
	Failed      bool   // a scroll's ability check failed: the scroll is wiped, nothing is cast
	Slug        string // the spell cast
	OnceSlug    string // a once-a-dawn spell of the item: marked used after the cast
	Gem         string // a Helm of Brilliance spell: the gem it uses up (F152)
}

// itemCastSpells lists the inventory items that can cast a spell: (item, entry) pairs, and the scrolls of it.
func itemCastOptions(inv []invItem, slug string) (charged []invItem, scrolls []invItem) {
	for _, it := range usableInventory(inv) {
		if itemBool(it, "destroyed") {
			continue
		}
		if s, ok := scrollOf(it); ok {
			if s.Slug == slug {
				scrolls = append(scrolls, it)
			}
			continue
		}
		if u := itemUseFor(it); u != nil {
			for _, e := range u.Spells {
				if e.Slug == slug {
					charged = append(charged, it)
					break
				}
			}
		}
	}
	return
}

// planItemCast decides whether a cast comes from an item and how. needItem is true when the character can't cast the spell
// otherwise. It returns nil (a normal cast), a plan, or a refusal (which starts with "Cannot cast").
func planItemCast(charID int, spellKey string, spell SRDSpell, req itemCastReq, requestedLevel int, description string, needItem bool) (*itemCastPlan, string) {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []invItem
	json.Unmarshal(raw, &inv)
	charged, scrolls := itemCastOptions(inv, spellKey)
	var it invItem
	switch {
	case req.Item != "":
		found, err := findInventoryItem(inv, req.Item)
		if err != nil {
			return nil, fmt.Sprintf("Cannot cast %s from %q: %v. Nothing was spent.", spell.Name, req.Item, err)
		}
		it = found
	default:
		low := strings.ToLower(normQuotes(description))
		for _, c := range append(append([]invItem{}, charged...), scrolls...) {
			if n := strings.ToLower(normQuotes(itemStr(c, "name"))); n != "" && strings.Contains(low, n) {
				it = c
				break
			}
		}
		if it == nil && needItem {
			switch {
			case len(charged) > 0:
				it = charged[0]
			case len(scrolls) > 0:
				it = scrolls[0]
			}
		}
	}
	if it == nil {
		return nil, ""
	}
	name := itemStr(it, "name")
	plan := &itemCastPlan{ItemID: itemStr(it, "id"), ItemName: name}
	if strings.HasPrefix(strings.ToLower(name), "circlet of blasting") {
		plan.Attack = 5 // the circlet's own attack bonus (F126)
	}
	if s, ok := scrollOf(it); ok {
		if s.Slug != spellKey {
			return nil, fmt.Sprintf("Cannot cast %s from the %s: it holds a different spell. Nothing was spent.", spell.Name, name)
		}
		plan.Scroll, plan.Level, plan.DC, plan.Attack, plan.CasterLevel, plan.NoMaterial = true, s.Level, s.DC, s.Attack, s.CasterLevel, true
		thief := thiefUseMagicDevice(charID)
		if !thief && !onSpellList(charID, spellKey) {
			return nil, fmt.Sprintf("Cannot cast %s from the %s: the spell isn't on your spell list, so the scroll is unintelligible to you. Nothing was spent.", spell.Name, name)
		}
		var intl, wis, cha int
		var class string
		db.QueryRow(`SELECT intl, wis, cha, COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&intl, &wis, &cha, &class)
		switch {
		case thief && s.Level >= 2:
			// Use Magic Device: an Intelligence (Arcana) check, DC 10 + the spell's level
			mod, prof, _, _ := checkBonus(charID, class, levelOf(charID), "arcana", intl)
			d := game.RollDie(20)
			total := d + mod + prof
			plan.CheckNote = fmt.Sprintf(" [Use Magic Device: Intelligence (Arcana) %d (d20 %d) vs DC %d]", total, d, 10+s.Level)
			plan.Failed = total < 10+s.Level
		case !thief && s.Level > highestSlotLevel(charID):
			ab := map[string]int{"INT": intl, "WIS": wis, "CHA": cha}[strings.ToUpper(srdClasses[strings.ToLower(class)].Spellcasting)]
			d := game.RollDie(20)
			total := d + game.Modifier(ab) - game.ExhaustionD20Penalty(exhaustionLevelOf(charID))
			plan.CheckNote = fmt.Sprintf(" [a level %d spell is above what you can cast: spellcasting ability check %d (d20 %d) vs DC %d]", s.Level, total, d, 10+s.Level)
			plan.Failed = total < 10+s.Level
		}
		return plan, ""
	}
	u := itemUseFor(it)
	if u == nil {
		return nil, fmt.Sprintf("Cannot cast %s from the %s: it doesn't cast spells. Nothing was spent.", spell.Name, name)
	}
	var entry *itemSpell
	for i := range u.Spells {
		if u.Spells[i].Slug == spellKey {
			entry = &u.Spells[i]
			break
		}
	}
	if entry == nil {
		var names []string
		for _, e := range u.Spells {
			names = append(names, e.Name)
		}
		return nil, fmt.Sprintf("Cannot cast %s from the %s: it casts %s. Nothing was spent.", spell.Name, name, joinOr(names))
	}
	handHeld(charID, it) // F118
	if why := itemPlace(it, u); why != "" {
		return nil, "Cannot cast " + spell.Name + ": " + why + " Nothing was spent."
	}
	if u.Attune {
		if _, ok := attunedTo(charID, u.Name); !ok {
			if _, ok2 := attunedTo(charID, name); !ok2 {
				return nil, fmt.Sprintf("Cannot cast %s from the %s: you must be attuned to it (POST /api/characters/attune). Nothing was spent.", spell.Name, name)
			}
		}
	}
	base := entry.Level
	if base == 0 {
		base = spell.Level
	}
	if why := dragonOrbControl(it); why != "" { // F185: only a controlled orb casts
		return nil, why
	}
	if strings.HasPrefix(strings.ToLower(name), "helm of brilliance") { // F152: the gem of the spell's type
		gem, why := helmGemFor(charID, it, spellKey)
		if why != "" {
			return nil, why
		}
		plan.Gem = gem
	}
	if entry.Once && itemBool(it, "once_used_"+entry.Slug) {
		return nil, fmt.Sprintf("Cannot cast %s from the %s again until the next dawn. Nothing was spent.", spell.Name, name)
	}
	if entry.Once {
		plan.OnceSlug = entry.Slug
	}
	plan.Slug = spellKey
	plan.Level, plan.Charges, plan.DC = base, entry.Cost, entry.DC
	switch {
	case entry.Upcast:
		n := req.Charges
		if n <= 0 && requestedLevel > base {
			n = requestedLevel - base + 1
		}
		if n <= 0 {
			n = 1
		}
		if entry.MaxCost > 0 && n > entry.MaxCost {
			return nil, fmt.Sprintf("Cannot cast %s from the %s with %d charges: at most %d at once. Nothing was spent.", spell.Name, name, n, entry.MaxCost)
		}
		plan.Charges, plan.Level = n, base+n-1
	case entry.PerLevel:
		lvl := spell.Level
		if requestedLevel > lvl {
			lvl = requestedLevel
		}
		if req.Charges > lvl {
			lvl = req.Charges
		}
		if entry.MaxLevel > 0 && lvl > entry.MaxLevel {
			return nil, fmt.Sprintf("Cannot cast %s from the %s above level %d. Nothing was spent.", spell.Name, name, entry.MaxLevel)
		}
		plan.Charges, plan.Level = lvl, lvl
	}
	cur, max, _ := itemCharges(it)
	if cur < plan.Charges {
		return nil, fmt.Sprintf("Cannot cast %s from the %s: it has %d of %d charges left and this takes %d. Nothing was spent.", spell.Name, name, cur, max, plan.Charges)
	}
	return plan, ""
}

func joinOr(names []string) string {
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

func levelOf(charID int) int {
	var l int
	db.QueryRow(`SELECT level FROM characters WHERE id = $1`, charID).Scan(&l)
	return l
}

// finishItemCast spends the charges or uses up the scroll after the spell is cast (or after a failed scroll check).
func finishItemCast(charID int, plan *itemCastPlan) string {
	note := ""
	editInventory(charID, func(inv []invItem) []invItem {
		var it invItem
		for _, x := range inv {
			if (plan.ItemID != "" && itemStr(x, "id") == plan.ItemID) || (plan.ItemID == "" && itemStr(x, "name") == plan.ItemName) {
				it = x
				break
			}
		}
		if it == nil {
			return inv
		}
		if plan.Scroll {
			note = fmt.Sprintf(" [The %s crumbles to dust.]", plan.ItemName)
			if plan.Failed {
				note = fmt.Sprintf(" [The spell disappears from the %s.]", plan.ItemName)
			}
			return takeOne(inv, it)
		}
		n, _ := spendCharges(charID, it, plan.Charges)
		note = n
		if plan.OnceSlug != "" {
			it["once_used_"+plan.OnceSlug] = true // once a dawn
		}
		if plan.Gem != "" {
			note += useHelmGemInPlace(it, plan.Gem) // F152
		}
		note += crystalBallNote(plan.ItemName, plan.Slug)
		return inv
	})
	return note
}

// takeOne removes one of a stack (the whole entry when it is the last one).
func takeOne(inv []invItem, it invItem) []invItem {
	if q := itemQty(it); q > 1 {
		it["quantity"] = q - 1
		return inv
	}
	out := make([]invItem, 0, len(inv))
	for _, x := range inv {
		if !sameMap(x, it) {
			out = append(out, x)
		}
	}
	return out
}

func sameMap(a, b invItem) bool {
	return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b)
}

// editInventory changes a character's inventory in one transaction.
func editInventory(charID int, fn func([]invItem) []invItem) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	var raw []byte
	if tx.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1 FOR UPDATE`, charID).Scan(&raw) != nil {
		return
	}
	var inv []invItem
	json.Unmarshal(raw, &inv)
	inv = fn(inv)
	out, _ := json.Marshal(inv)
	if _, err := tx.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, out, charID); err == nil {
		tx.Commit()
	}
}

// ---------------------------------------------------------------- potions

var giantStrengthScores = map[string]int{"hill": 21, "frost": 23, "stone": 23, "fire": 25, "cloud": 27, "storm": 29}
var healingPotions = map[string]string{"": "2d4+2", "greater": "4d4+4", "superior": "8d4+8", "supreme": "10d4+20"}
var damageTypes = []string{"acid", "cold", "fire", "force", "lightning", "necrotic", "poison", "psychic", "radiant", "thunder"}

// isPotion: the inventory item is a potion (or an oil, philter or elixir from the SRD).
func isPotion(it invItem) bool {
	n := strings.ToLower(itemStr(it, "name"))
	if strings.HasPrefix(n, "potion") || strings.HasPrefix(n, "elixir") || strings.HasPrefix(n, "philter") || strings.HasPrefix(n, "oil of") {
		return true
	}
	u := itemUseFor(it)
	return u != nil && u.Potion
}

// potionKind sorts a potion name: "healing" with "greater"/"superior"/"supreme", "giant strength" with the giant, "resistance" with
// the damage type, or the SRD item name.
func potionKind(name string) (kind, variant string) {
	n := strings.ToLower(normQuotes(name))
	switch {
	case strings.Contains(n, "healing"):
		for _, v := range []string{"greater", "superior", "supreme"} {
			if strings.Contains(n, v) {
				return "healing", v
			}
		}
		return "healing", ""
	case strings.Contains(n, "giant strength"):
		for g := range giantStrengthScores {
			if strings.Contains(n, g) {
				return "giant strength", g
			}
		}
		return "giant strength", "hill"
	case strings.Contains(n, "resistance"):
		for _, d := range damageTypes {
			if strings.Contains(n, d) {
				return "resistance", d
			}
		}
		return "resistance", ""
	}
	return n, ""
}

// potionRounds reads the duration in a potion's text: "for 1 hour" 600 rounds, "for 10 minutes" 100, "for 1 minute" 10,
// "for 24 hours" 14,400, "for 1d4 hours" rolled.
var durationRe = regexp.MustCompile(`(?i)for (\d+d\d+|\d+) (hour|minute)s?`)

func potionRounds(text string) int {
	mm := durationRe.FindStringSubmatch(text)
	if mm == nil {
		return 600
	}
	n := rollExpr(mm[1])
	if strings.ToLower(mm[2]) == "hour" {
		return n * 600
	}
	return n * 10
}

// usePotion makes the target (the drinker, or another character the drinker gives it to) drink the potion it.
func usePotion(lobbyID, drinkerID, targetID int, it invItem) string {
	if targetID == 0 {
		targetID = drinkerID
	}
	name := itemStr(it, "name")
	who := creatureName(lobbyID, targetID)
	kind, variant := potionKind(name)
	u := itemUseFor(it)
	text := ""
	if u != nil {
		text = u.Text
	}
	label := who + " drinks the " + name
	if targetID != drinkerID {
		label = fmt.Sprintf("%s gives the %s to %s", creatureName(lobbyID, drinkerID), name, who)
	}
	if strings.Contains(strings.ToLower(name), "oil of slipperiness") {
		return oilOfSlipperiness(lobbyID, targetID, label) // F109 (monk_fixes_2024.go)
	}
	if strings.Contains(strings.ToLower(name), "potion of climbing") {
		return potionOfClimbing(lobbyID, targetID, label) // F157 (items_c4_2024.go)
	}
	if strings.Contains(strings.ToLower(name), "potion of growth") {
		return potionOfGrowth(lobbyID, targetID, label) // F99 (fighter_fixes_2024.go)
	}
	switch kind {
	case "healing":
		dice := healingPotions[variant]
		amount := game.RollDamage(dice, false)
		n, before, after := healCharacter(targetID, amount)
		if n == "" {
			return label + ": the potion only works on a character the server knows."
		}
		return fmt.Sprintf("🧪 %s: %s = %d Hit Points (%d → %d).", label, dice, amount, before, after)
	case "potion of heroism":
		db.Exec(`UPDATE characters SET temp_hp = GREATEST(COALESCE(temp_hp,0), 10) WHERE id = $1`, targetID)
		addSpellEffect(lobbyID, targetID, targetID, "Potion of Heroism", "bless", "", 600, "", 0)
		return fmt.Sprintf("🧪 %s: 10 Temporary Hit Points for 1 hour, and the effect of Bless for 1 hour (no Concentration).", label)
	case "giant strength":
		score := giantStrengthScores[variant]
		var str int
		db.QueryRow(`SELECT str FROM characters WHERE id = $1`, targetID).Scan(&str)
		if str >= score {
			return fmt.Sprintf("🧪 %s: no effect, Strength %d is already %d or more.", label, str, score)
		}
		db.Exec(`UPDATE characters SET str = $1 WHERE id = $2`, score, targetID)
		addSpellEffect(lobbyID, targetID, targetID, "Potion of Giant Strength", "giant_strength", fmt.Sprintf("orig=%d;set=%d", str, score), 600, "", 0)
		return fmt.Sprintf("🧪 %s: Strength %d for 1 hour (it was %d).", label, score, str)
	case "resistance":
		dt := variant
		roll := ""
		if dt == "" {
			d := game.RollDie(10)
			dt, roll = damageTypes[d-1], fmt.Sprintf(" (the GM chooses the type; rolled %d on 1d10)", d)
		}
		addSpellEffect(lobbyID, targetID, targetID, "Potion of Resistance", "damage_resistance", dt, 600, "", 0)
		return fmt.Sprintf("🧪 %s: Resistance to %s damage for 1 hour%s.", label, titleWords(dt), roll)
	case "potion of invulnerability":
		addSpellEffect(lobbyID, targetID, targetID, "Potion of Invulnerability", "damage_resistance", "all", 10, "", 0)
		return fmt.Sprintf("🧪 %s: Resistance to all damage for 1 minute.", label)
	case "potion of speed":
		addSpellEffect(lobbyID, targetID, targetID, "Potion of Speed", "hasted", "", 10, "", 0)
		return fmt.Sprintf("🧪 %s: the effect of Haste for 1 minute (no Concentration, and no lethargy when it ends): +2 AC, Advantage on Dexterity saves, Speed doubled, one more action each turn.", label)
	case "potion of invisibility":
		addMonsterCondition(lobbyID, targetID, "invisible", "Potion of Invisibility", 600, "", 0)
		return fmt.Sprintf("🧪 %s: Invisible for 1 hour; it ends early if %s makes an attack roll, deals damage or casts a spell.", label, who)
	case "potion of poison":
		dmg := game.RollDamage("4d6", false)
		res := damageCharacter(targetID, dmg, "poison")
		saved, total := characterSave(targetID, "con", 13)
		out := fmt.Sprintf("🧪 %s: it was poison! %s", label, res)
		if saved {
			return out + fmt.Sprintf(" Constitution save %d vs DC 13: success.", total)
		}
		addMonsterCondition(lobbyID, targetID, "poisoned", "Potion of Poison", 600, "", 0)
		return out + fmt.Sprintf(" Constitution save %d vs DC 13: failure, Poisoned for 1 hour.", total)
	case "potion of vitality":
		db.Exec(`UPDATE characters SET exhaustion_level = 0 WHERE id = $1`, targetID)
		removeCondition(targetID, "poisoned")
		addSpellEffect(lobbyID, targetID, targetID, "Potion of Vitality", "potion", "", 14400, "", 0)
		return fmt.Sprintf("🧪 %s: every Exhaustion level is gone and the Poisoned condition ends; for 24 hours each Hit Point Die spent heals the most it can.", label)
	case "elixir of health":
		for _, c := range []string{"blinded", "deafened", "paralyzed", "poisoned"} {
			removeCondition(targetID, c)
		}
		return fmt.Sprintf("🧪 %s: cured of magical contagions (the GM ends them), and Blinded, Deafened, Paralyzed and Poisoned end.", label)
	}
	// the other potions: the effect shows on the creature for its duration and the GM runs it
	rounds := potionRounds(text)
	if strings.Contains(strings.ToLower(text), "24 hours") {
		rounds = 14400
	}
	addSpellEffect(lobbyID, targetID, targetID, name, "potion", "", rounds, "", 0)
	desc := text
	if i := strings.Index(desc, "\n"); i > 0 {
		desc = desc[:i]
	}
	return fmt.Sprintf("🧪 %s. %s (The GM runs the effect.)", label, desc)
}

// itemEffectSource: an effect from a potion or another magic item, which needs no Concentration.
func itemEffectSource(spell string) bool {
	s := strings.ToLower(spell)
	return strings.HasPrefix(s, "potion") || strings.HasPrefix(s, "elixir") || strings.HasPrefix(s, "oil of") || strings.HasPrefix(s, "philter")
}

// potionResists: a Potion of Resistance or Invulnerability gives Resistance to this damage type now.
func potionResists(charID int, damageType string) bool {
	lobby := lobbyOfChar(charID)
	if lobby == 0 || damageType == "" {
		return false
	}
	for _, e := range spellEffectsOn(lobby, charID) {
		if e.Kind == "damage_resistance" && (e.Detail == "all" || strings.EqualFold(e.Detail, damageType)) {
			return true
		}
	}
	return false
}

// endItemEffect undoes what an ended effect changed (Giant Strength gives the Strength score back).
func endItemEffect(kind string, targetID int, detail string) {
	buffEnds(kind, targetID, detail)          // Aid, Heroes' Feast, Barkskin, Warding Bond (buff_spells_2024.go)
	if kind == "mage_armor" && targetID > 0 { // Mage Armor ran out (mage_armor_2024.go): the base AC goes back
		refreshStoredAC(targetID)
		return
	}
	if kind != "giant_strength" || targetID <= 0 {
		return
	}
	var orig, set int
	fmt.Sscanf(detail, "orig=%d;set=%d", &orig, &set)
	if orig > 0 {
		db.Exec(`UPDATE characters SET str = $1 WHERE id = $2 AND str = $3`, orig, targetID, set)
	}
}

// endPotionInvisibility: a Potion of Invisibility ends when its drinker attacks, deals damage or casts a spell.
func endPotionInvisibility(charID int) string {
	res, err := db.Exec(`DELETE FROM condition_records WHERE target_id = $1 AND condition = 'invisible' AND source = 'Potion of Invisibility'`, charID)
	if err != nil {
		return ""
	}
	if n, _ := res.RowsAffected(); n > 0 {
		syncListFromRecords(charID)
		return " (The Potion of Invisibility's effect ends.)"
	}
	return ""
}

// ---------------------------------------------------------------- dawn

// rechargeAtDawn gives each charged item of a character the charges it regains at dawn. It returns what came back.
func rechargeAtDawn(charID int) []string {
	var notes []string
	if n := sunCardDawn(charID); n != "" { // the Mysterious Deck's Sun card (legendary_items_2024.go)
		notes = append(notes, n)
	}
	notes = append(notes, deckDawn(charID)...) // the Fool's and the Jester's 72 hours (deck_choices_2024.go)
	editInventory(charID, func(inv []invItem) []invItem {
		for _, it := range inv {
			if n := tickItemCooldown(charID, it); n != "" { // Horn of Valhalla's 7 days, the Ring of Djinni Summoning's 24 hours
				notes = append(notes, n)
			}
			if _, used := it["charges"]; !used || itemBool(it, "destroyed") {
				continue // never used: still full
			}
			u := itemUseFor(it)
			if u == nil || u.Regain == "" {
				continue
			}
			cur, max, _ := itemCharges(it)
			if cur >= max {
				continue
			}
			gain := max - cur
			if u.Regain != "all" {
				gain = rollExpr(u.Regain)
			}
			if cur+gain > max {
				gain = max - cur
			}
			setItemCharges(it, cur+gain, max)
			notes = append(notes, fmt.Sprintf("%s regains %d charge(s) (%d of %d)", itemStr(it, "name"), gain, cur+gain, max))
		}
		return inv
	})
	return notes
}

// rechargeCampaignAtDawn recharges every character of a campaign.
func rechargeCampaignAtDawn(lobbyID int) []string {
	rows, err := db.Query(`SELECT id, name FROM characters WHERE lobby_id = $1`, lobbyID)
	if err != nil {
		return nil
	}
	type ch struct {
		id   int
		name string
	}
	var list []ch
	for rows.Next() {
		var c ch
		if rows.Scan(&c.id, &c.name) == nil {
			list = append(list, c)
		}
	}
	rows.Close()
	var out []string
	for _, c := range list {
		for _, n := range rechargeAtDawn(c.id) {
			out = append(out, c.name+": "+n)
		}
	}
	return out
}

// dawnOnLongRest: by the table rule, a Long Rest counts as the next dawn for magic items.
func dawnOnLongRest(charID int) []string {
	if tableRule(lobbyOfChar(charID), "dawn") == "gm" {
		return nil
	}
	return rechargeAtDawn(charID)
}

// handleGMDawn godoc
// @Summary Dawn: magic items regain charges
// @Description The GM says dawn has come: every charged magic item of the campaign's characters regains its charges (a Wand of Fireballs 1d6 + 1, Eyes of Charming all...). With the table rule dawn "long_rest" (the default) a Long Rest also counts as the next dawn; with "gm" only this command does.
// @Tags GM
// @Param request body object{campaign_id=int} false "Campaign (when you run several)"
// @Router /gm/dawn [post]
func handleGMDawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var body struct {
		CampaignID int `json:"campaign_id"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	if body.CampaignID > 0 && r.URL.Query().Get("campaign_id") == "" { // the documented body field (F51: only ?campaign_id= was read)
		q := r.URL.Query()
		q.Set("campaign_id", strconv.Itoa(body.CampaignID))
		r.URL.RawQuery = q.Encode()
	}
	var lobbyID int
	if err := gmCampaignScan(agentID, r, &lobbyID); err != nil {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_gm", "message": publicError(err)})
		return
	}
	notes := rechargeCampaignAtDawn(lobbyID)
	if len(notes) == 0 {
		notes = []string{"No item needed charges."}
	}
	db.Exec(`INSERT INTO actions (lobby_id, action_type, description, result) VALUES ($1, 'dawn', 'Dawn: magic items regain charges', $2)`, lobbyID, strings.Join(notes, "; "))
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "recharged": notes})
}

// ---------------------------------------------------------------- endpoints: charges

// handleItemUseCharge spends charges of an item for a property that isn't a spell:
//
//	POST /api/characters/{id}/equipment/use-charge {"item": "Winged Boots", "charges": 1}
//
// handleItemUseCharge godoc
// @Summary Spend a magic item's charges
// @Description For a property that isn't a spell (Winged Boots, a Wand of Paralysis ray, a Cloak of Invisibility...): the item must be held or worn (as its text says) and attuned if it needs it. A Thief of level 13 rolls 1d6 and spends nothing on a 6. The last charge may destroy the item (the 1d20 roll of its text). The GM runs what the property does; spells from items go through the cast action with "item".
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{item=string,charges=int} true "Item (name or id) and charges (default 1)"
// @Router /characters/{id}/equipment/use-charge [post]
func handleItemUseCharge(w http.ResponseWriter, r *http.Request, charID int) {
	var req struct {
		Item    string `json:"item"`
		Charges int    `json:"charges"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Item) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_required", "message": "Send item (its name or id) and charges (default 1)."})
		return
	}
	if req.Charges <= 0 {
		req.Charges = 1
	}
	var note, refusal, name, text string
	editInventory(charID, func(inv []invItem) []invItem {
		it, err := findInventoryItem(inv, req.Item)
		if err != nil {
			refusal = err.Error()
			return inv
		}
		name = itemStr(it, "name")
		u := itemUseFor(it)
		if u == nil || u.Max == "" {
			refusal = "The " + name + " has no charges."
			return inv
		}
		text = u.Text
		handHeld(charID, it) // F118
		if why := itemPlace(it, u); why != "" {
			refusal = why
			return inv
		}
		if u.Attune {
			if _, ok := attunedTo(charID, u.Name); !ok {
				refusal = "You must be attuned to the " + name + " to use it (POST /api/characters/attune)."
				return inv
			}
		}
		note, refusal = spendCharges(charID, it, req.Charges)
		return inv
	})
	if refusal != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "cannot_use", "message": refusal})
		return
	}
	var charName string
	var lobby int
	db.QueryRow(`SELECT name, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&charName, &lobby)
	if lobby > 0 {
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'item_charge', $3, $4)`,
			lobby, charID, fmt.Sprintf("%s uses the %s", charName, name), strings.TrimSpace(note))
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "item": name, "result": strings.TrimSpace(note), "item_text": text,
		"note": "The server spent the charges; the GM runs what the property does (the item's text is above)."})
}

// handleItemSetCharges lets the GM set an item's charges:
//
//	POST /api/characters/{id}/equipment/charges {"item": "Wand of Fireballs", "charges": 7}
//
// handleItemSetCharges godoc
// @Summary Set a magic item's charges (GM)
// @Description The GM of the character's campaign sets an item's charges (and optionally its maximum); it also mends an item its last charge destroyed.
// @Tags GM
// @Param id path int true "Character ID"
// @Param request body object{item=string,charges=int,max=int} true "Item, charges, max"
// @Router /characters/{id}/equipment/charges [post]
func handleItemSetCharges(w http.ResponseWriter, r *http.Request, charID int) {
	var req struct {
		Item    string `json:"item"`
		Charges int    `json:"charges"`
		Max     int    `json:"max"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Item) == "" || req.Charges < 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_and_charges_required"})
		return
	}
	var result map[string]interface{}
	refusal := ""
	editInventory(charID, func(inv []invItem) []invItem {
		it, err := findItem(inv, req.Item)
		if err != nil {
			refusal = err.Error()
			return inv
		}
		_, max, _ := itemCharges(it)
		if req.Max > 0 {
			max = req.Max
		}
		if max == 0 {
			max = req.Charges
		}
		cur := req.Charges
		if cur > max {
			cur = max
		}
		setItemCharges(it, cur, max)
		delete(it, "destroyed")
		result = map[string]interface{}{"item": itemStr(it, "name"), "charges": cur, "charges_max": max}
		return inv
	})
	if refusal != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": refusal})
		return
	}
	result["success"] = true
	writeJSONStatus(w, http.StatusOK, result)
}

// annotateCharges adds the charges of magic items that were never used (full) and what each item casts, for the equipment view.
func annotateCharges(inv []invItem) {
	for _, it := range inv {
		if cur, max, ok := itemCharges(it); ok {
			it["charges"], it["charges_max"] = cur, max
		}
		if u := itemUseFor(it); u != nil && len(u.Spells) > 0 {
			var casts []string
			for _, e := range u.Spells {
				c := fmt.Sprintf("%s (%d charge", e.Name, e.Cost)
				if e.Cost != 1 {
					c += "s"
				}
				c += ")"
				casts = append(casts, c)
			}
			it["casts"] = casts
		}
		if s, ok := scrollOf(it); ok {
			it["scroll"] = map[string]interface{}{"spell": s.Slug, "level": s.Level, "save_dc": s.DC, "attack_bonus": s.Attack}
		}
	}
}

// ---------------------------------------------------------------- the SRD potions and scrolls, for the catalog

// potionAndScrollList is GET /api/universe/consumables: the SRD potions and the Spell Scroll table.
func potionAndScrollList() []map[string]interface{} {
	loadItemUses()
	var out []map[string]interface{}
	var names []string
	for _, u := range itemUses {
		if u.Potion {
			names = append(names, u.Name)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		u := itemUses[itemUseKey(n)]
		out = append(out, map[string]interface{}{"name": u.Name, "type": "potion", "use": "Bonus Action: drink it, or give it to a creature within 5 feet", "description": u.Text})
	}
	for lvl := 0; lvl <= 9; lvl++ {
		nums := scrollNumbers[lvl]
		label := fmt.Sprintf("Level %d", lvl)
		if lvl == 0 {
			label = "Cantrip"
		}
		out = append(out, map[string]interface{}{"name": "Spell Scroll (" + label + ")", "type": "scroll", "spell_level": lvl, "save_dc": nums[0], "attack_bonus": nums[1],
			"use": "Cast the spell with the cast action and \"item\": \"Spell Scroll (<spell>)\" (it must be on your spell list)"})
	}
	return out
}

// newItemID makes an id for an item added outside the equipment commands.
func newItemID(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// magicItemFields gives a new inventory item what the server knows about it: the SRD item it is, its charges, its spell (scrolls).
func magicItemFields(it invItem) {
	if u := itemUseFor(it); u != nil {
		it["magic_item"] = u.Name
		if u.Max != "" {
			max := rollExpr(u.Max)
			it["charges"], it["charges_max"] = max, max
		}
		if u.Potion {
			it["type"] = "potion"
		}
		return
	}
	if s, ok := scrollOf(it); ok {
		it["type"], it["spell"], it["spell_level"] = "scroll", s.Slug, s.Level
	}
}

// ---------------------------------------------------------------- the use_item action

// itemInUse finds the item a use_item action names: the "item" field, or an item whose name is in the description.
func itemInUse(charID int, ref, description string) (invItem, []invItem) {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []invItem
	json.Unmarshal(raw, &inv)
	if ref != "" {
		it, err := findInventoryItem(inv, ref)
		if err != nil {
			return nil, inv
		}
		return it, inv
	}
	low := strings.ToLower(normQuotes(description))
	var best invItem
	for _, it := range usableInventory(inv) {
		n := strings.ToLower(normQuotes(itemStr(it, "name")))
		if n != "" && strings.Contains(low, n) && (best == nil || len(n) > len(itemStr(best, "name"))) {
			best = it
		}
	}
	if best == nil && (strings.Contains(low, "potion") || strings.Contains(low, "drink")) {
		for _, it := range usableInventory(inv) {
			if isPotion(it) && strings.Contains(low, strings.ToLower(strings.TrimPrefix(strings.ToLower(itemStr(it, "name")), "potion of "))) {
				best = it
				break
			}
		}
	}
	return best, inv
}

// potionInUse: the use_item action drinks or gives a potion (a Bonus Action).
func potionInUse(charID int, ref, description string) bool {
	it, _ := itemInUse(charID, ref, description)
	return it != nil && isPotion(it)
}

// useItemAction is the use_item action (also "drink"): a potion is drunk or given, a Spell Scroll is read with the cast action,
// a charged item points to its commands. A refusal starts with "Cannot use" and spends nothing.
// studyBookScore: the Manuals and Tomes (SRD 5.2.1): 48 hours of study over 6 days or fewer raise one score by 2, to a maximum of 30;
// the book then loses its magic for a century.
func studyBookScore(name string) (col, ability string, ok bool) {
	n := strings.ToLower(name)
	for _, b := range []struct{ prefix, col, ability string }{
		{"manual of bodily health", "con", "Constitution"}, {"manual of gainful exercise", "str", "Strength"},
		{"manual of quickness of action", "dex", "Dexterity"}, {"tome of clear thought", "intl", "Intelligence"},
		{"tome of leadership and influence", "cha", "Charisma"}, {"tome of understanding", "wis", "Wisdom"},
	} {
		if strings.HasPrefix(n, b.prefix) {
			return b.col, b.ability, true
		}
	}
	return "", "", false
}

func studyBook(charID int, it invItem, name, col, ability string) string {
	if itemBool(it, "magic_lost") {
		return "Cannot use the " + name + ": it has lost its magic (it regains it in a century). Nothing was spent."
	}
	if _, inCombat, _ := isCharacterInCombat(charID); inCombat {
		return "Cannot study the " + name + " in combat: it takes 48 hours over 6 days or fewer. Nothing was spent."
	}
	var cur int
	db.QueryRow(`SELECT `+col+` FROM characters WHERE id = $1`, charID).Scan(&cur)
	if cur >= 30 {
		return fmt.Sprintf("Cannot gain from the %s: your %s is already 30. Nothing was spent.", name, ability)
	}
	newVal := cur + 2
	if newVal > 30 {
		newVal = 30
	}
	db.Exec(`UPDATE characters SET `+col+` = $1 WHERE id = $2`, newVal, charID)
	// an item that sets this score while attuned (Belt of Giant Strength, Amulet of Health) keeps the base it restores: raise it too
	db.Exec(`UPDATE characters SET item_base_scores = jsonb_set(item_base_scores, ARRAY[$1], to_jsonb((item_base_scores->>$1)::int + 2))
		WHERE id = $2 AND item_base_scores ? $1`, col, charID)
	if col == "con" {
		conHitPoints(charID, "con", cur, newVal)
	}
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "name") == name && !itemBool(x, "magic_lost") {
				x["magic_lost"] = true
				break
			}
		}
		return inv
	})
	return fmt.Sprintf("After 48 hours of study over 6 days or fewer, your %s rises from %d to %d (maximum 30). The %s loses its magic for a century.", ability, cur, newVal, name)
}

func useItemAction(charID int, description string) string {
	v, _ := castItems.Load(charID)
	req, _ := v.(itemCastReq)
	it, _ := itemInUse(charID, req.Item, description)
	if it == nil {
		if req.Item != "" {
			return fmt.Sprintf("Cannot use %q: it isn't in your inventory (or it's in a lost bag). Nothing was spent.", req.Item)
		}
		return fmt.Sprintf("Used item: %s (no game effect resolved)", description)
	}
	name := itemStr(it, "name")
	if itemBool(it, "destroyed") {
		return "Cannot use the " + name + ": it is destroyed. Nothing was spent."
	}
	if out, ok := legendaryItemUse(charID, it, req, description); ok { // Horn of Valhalla, Ring of Djinni Summoning, Iron Flask, Sphere, Deck
		return out
	}
	if out, ok := legendaryMoreUse(charID, it, req, description); ok { // Rod of Lordly Might, Staff of the Magi, armors, glue ... (legendary_more_2024.go)
		return out
	}
	if s, ok := scrollOf(it); ok {
		return fmt.Sprintf("Cannot use the %s this way: read it with the cast action, {\"action\": \"cast\", \"spell_slug\": \"%s\", \"item\": \"%s\"}. Nothing was spent.", name, s.Slug, name)
	}
	if isPotion(it) {
		lobby := lobbyOfChar(charID)
		target := req.Target
		if target != 0 && target != charID {
			if lobbyOfChar(target) != lobby || lobby == 0 {
				return "Cannot use the " + name + " on that creature: give a potion to a character of your campaign within 5 feet. Nothing was spent."
			}
		}
		used := false
		editInventory(charID, func(inv []invItem) []invItem {
			for _, x := range inv {
				if (itemStr(it, "id") != "" && itemStr(x, "id") == itemStr(it, "id")) || (itemStr(it, "id") == "" && itemStr(x, "name") == name && !itemBool(x, "destroyed")) {
					used = true
					return takeOne(inv, x)
				}
			}
			return inv
		})
		if !used {
			return "Cannot use the " + name + ": it is no longer in your inventory. Nothing was spent."
		}
		return usePotion(lobby, charID, target, it)
	}
	if strings.HasPrefix(strings.ToLower(name), "boots of speed") {
		return bootsOfSpeed(charID, it) // F107 (monk_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "cloak of invisibility") {
		return cloakOfInvisibility(charID, it, description) // F187 (items_c7_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "ring of invisibility") {
		return ringOfInvisibility(charID, it) // F188
	}
	if strings.HasPrefix(strings.ToLower(name), "talisman of pure good") {
		return talismanRebuke(charID, it, req, true) // F184 (items_c8_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "talisman of ultimate evil") {
		return talismanRebuke(charID, it, req, false) // F184
	}
	if strings.HasPrefix(strings.ToLower(name), "dragon orb") {
		return dragonOrb(charID, it, description) // F185
	}
	if strings.HasPrefix(strings.ToLower(name), "gloves of missile snaring") {
		return glovesOfMissileSnaring(charID) // F183 (items_c6_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "pipes of haunting") {
		return pipesOfHaunting(charID, it, req) // F175 (items_c6_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "robe of scintillating colors") {
		return robeOfScintillatingColors(charID, it, req) // F176
	}
	if strings.HasPrefix(strings.ToLower(name), "ring of the ram") {
		return ringOfTheRam(charID, it, req, description) // F178
	}
	if strings.HasPrefix(strings.ToLower(name), "ring of shooting stars") {
		return ringOfShootingStars(charID, it, req, description) // F179
	}
	if strings.HasPrefix(strings.ToLower(name), "staff of thunder and lightning") {
		return thunderLightningStaff(charID, it, req, description) // F161 (items_c5_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "winged boots") {
		return wingedBoots(charID, it) // F163
	}
	if strings.HasPrefix(strings.ToLower(name), "dagger of venom") {
		return daggerOfVenom(charID, it) // F167
	}
	if strings.HasPrefix(strings.ToLower(name), "horn of blasting") {
		return hornOfBlasting(charID, it, req) // F168
	}
	if strings.HasPrefix(strings.ToLower(name), "wand of wonder") {
		return wandOfWonder(charID, it, req) // F170
	}
	if strings.HasPrefix(strings.ToLower(name), "helm of brilliance") {
		return helmOfBrilliance(charID, it, description) // F152 (items_c4_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "periapt of health") {
		return periaptOfHealth(charID, it) // F154
	}
	if strings.HasPrefix(strings.ToLower(name), "wand of paralysis") {
		return wandOfParalysis(charID, it, req) // F155
	}
	if strings.HasPrefix(strings.ToLower(name), "elemental gem") {
		return elementalGem(charID, it) // F156
	}
	if strings.HasPrefix(strings.ToLower(name), "wings of flying") {
		return wingsOfFlying(charID, it) // F135 (warlock_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "bead of force") {
		return beadOfForce(charID, it, req) // F136
	}
	if strings.HasPrefix(strings.ToLower(name), "dust of sneezing") {
		return dustOfSneezing(charID, it, req) // F137
	}
	if strings.HasPrefix(strings.ToLower(name), "rod of absorption") {
		return rodOfAbsorption(charID, it, description) // F131 (bard_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "necklace of fireballs") {
		return necklaceOfFireballs(charID, it, req, description) // F127 (sorcerer_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "eversmoking bottle") {
		return eversmokingBottle(charID, it) // F121 (wizard_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "dancing sword") {
		return dancingSword(charID, it, req) // F114 (rogue_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "dust of disappearance") {
		return dustOfDisappearance(charID, it, req) // F108
	}
	if strings.HasPrefix(strings.ToLower(name), "flame tongue") {
		return flameTongueToggle(charID, it) // F103 (barbarian_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "javelin of lightning") {
		return javelinOfLightning(charID, it, req, description) // F97 (fighter_fixes_2024.go)
	}
	if strings.HasPrefix(strings.ToLower(name), "pearl of power") {
		return pearlOfPower(charID, it, description) // F87 (cleric_fixes_2024.go)
	}
	if out, ok := moreItemUse(charID, it, req, description); ok { // F206 (items_more_2024.go)
		return out
	}
	if col, ability, ok := studyBookScore(name); ok {
		return studyBook(charID, it, name, col, ability)
	}
	if u := itemUseFor(it); u != nil && u.Max != "" {
		casts := ""
		if len(u.Spells) > 0 {
			var names []string
			for _, e := range u.Spells {
				names = append(names, e.Name)
			}
			casts = fmt.Sprintf(" To cast %s from it, use the cast action with \"item\": \"%s\".", joinOr(names), name)
		}
		if casts != "" {
			return fmt.Sprintf("Cannot use the %s with use_item: spend its charges with POST /api/characters/%d/equipment/use-charge {\"item\": \"%s\", \"charges\": 1}.%s Nothing was spent.", name, charID, name, casts)
		}
		note, refusal := chargedUse(charID, it, 1) // a charged item with no spell: a charge, and what it does (F206)
		if refusal != "" {
			return refusal + " Nothing was spent."
		}
		return fmt.Sprintf("You use the %s (1 charge). What it does (SRD): %s The GM applies its effect in the world.%s", name, itemSRDText(name), note)
	}
	if t := itemSRDText(name); t != "" { // F206: the item's own text instead of "No game effect resolved"
		return fmt.Sprintf("You use the %s. What it does (SRD): %s The GM applies its effect in the world.", name, t)
	}
	return fmt.Sprintf("Used %s. (No game effect resolved; the GM decides what happens.)", name)
}
