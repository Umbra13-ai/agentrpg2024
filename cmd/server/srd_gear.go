package main

// Item weights. Weights come from the SRD 5.2.1 gear table, the weapons table
// and the armor table, matched by name. An item with its own non-zero weight
// keeps it.

import (
	"encoding/json"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

var itemTokenSplit = regexp.MustCompile(`[^a-z0-9]+`)
var itemParens = regexp.MustCompile(`\([^)]*\)`)

// itemKey turns an item name into a comparable key: lowercase, no
// parentheses, numbers or one-letter words, plurals trimmed, words sorted.
// "Scholar's Pack", "scholars pack" and "Pack, Scholar's" give the same key.
func itemKey(name string) string {
	n := itemParens.ReplaceAllString(strings.ToLower(name), " ")
	var words []string
	for _, w := range itemTokenSplit.Split(n, -1) {
		if len(w) <= 1 || isDigits(w) {
			continue
		}
		switch {
		case len(w) > 4 && (strings.HasSuffix(w, "ches") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "xes")):
			w = strings.TrimSuffix(w, "es")
		case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
			w = strings.TrimSuffix(w, "s")
		}
		words = append(words, w)
	}
	sort.Strings(words)
	return strings.Join(words, " ")
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// itemWeightAliases maps names used in backgrounds and class equipment to the
// SRD gear entry they correspond to.
var itemWeightAliases = map[string]string{
	"common clothes":     "Clothes, Traveler's",
	"traveler clothes":   "Clothes, Traveler's",
	"fine clothes":       "Clothes, Fine",
	"prayer book":        "Book",
	"hempen rope":        "Rope",
	"silk rope":          "Rope",
	"rope (50 feet)":     "Rope",
	"crossbow bolt case": "Case, Crossbow Bolt",
	"map case":           "Case, Map or Scroll",
	"scroll case":        "Case, Map or Scroll",
}

var (
	weightIndexOnce sync.Once
	weightIndex     map[string]float64
)

func buildWeightIndex() {
	idx := map[string]float64{}
	for _, g := range srd521Gear {
		if g.WeightLb >= 0 {
			idx[itemKey(g.Name)] = g.WeightLb
		}
	}
	for _, w := range srdWeapons {
		if w.Weight > 0 {
			if k := itemKey(w.Name); idx[k] == 0 {
				idx[k] = w.Weight
			}
		}
	}
	for _, a := range srdArmor {
		if a.Weight > 0 {
			idx[itemKey(a.Name)] = a.Weight
			if !strings.Contains(strings.ToLower(a.Name), "armor") && !strings.Contains(strings.ToLower(a.Name), "shield") {
				idx[itemKey(a.Name+" armor")] = a.Weight
			}
		}
	}
	// Tools and instruments (2024 Player's Handbook)
	for _, tl := range game.Tools {
		if tl.WeightLb > 0 {
			idx[itemKey(tl.Name)] = tl.WeightLb
		}
	}
	// Ammunition (SRD 5.2.1: 20 arrows 1 lb, 20 bolts 1.5 lb, 10 firearm bullets 2 lb,
	// 20 sling bullets 1.5 lb, 50 needles 1 lb), focus forms, and the spellbook.
	// The spellbook weighs 3 pounds (the Wizard's Spellbook feature).
	for name, w := range map[string]float64{
		"Arrow": 0.05, "Bolt": 0.075, "Bullet, Firearm": 0.2, "Bullet, Sling": 0.075, "Needle": 0.02,
		"Arcane Focus, Crystal": 1, "Arcane Focus, Orb": 3, "Arcane Focus, Rod": 2, "Arcane Focus, Staff": 4, "Arcane Focus, Wand": 1,
		"Druidic Focus, Wooden Staff": 4, "Druidic Focus, Yew Wand": 1,
		"Holy Symbol, Amulet": 1, "Holy Symbol, Reliquary": 2,
		"Spellbook": 3,
	} {
		idx[itemKey(name)] = w
	}
	for alias, target := range itemWeightAliases {
		if w, ok := idx[itemKey(target)]; ok {
			idx[itemKey(alias)] = w
		}
	}
	weightIndex = idx
}

// catalogWeightFor returns the SRD weight in pounds for an item name.
func catalogWeightFor(name string) (float64, bool) {
	weightIndexOnce.Do(buildWeightIndex)
	w, ok := weightIndex[itemKey(name)]
	return w, ok
}

// itemUnitWeight is the weight of one unit: the item's own weight if set,
// otherwise the SRD weight, otherwise 0.
func itemUnitWeight(it map[string]interface{}) float64 {
	if w, ok := it["weight"].(float64); ok && w > 0 {
		return w
	}
	name, _ := it["name"].(string)
	if w, ok := catalogWeightFor(name); ok {
		return w
	}
	return 0
}

// fillItemWeights stores the SRD weight on every item that has none.
// It returns true if anything changed.
func fillItemWeights(inv []map[string]interface{}) bool {
	changed := false
	for _, it := range inv {
		if w, ok := it["weight"].(float64); ok && w > 0 {
			continue
		}
		name, _ := it["name"].(string)
		if w, ok := catalogWeightFor(name); ok && w > 0 {
			it["weight"] = w
			changed = true
		}
	}
	return changed
}

// backfillItemWeights fills in weights on existing characters' inventories.
func backfillItemWeights() {
	rows, err := db.Query(`SELECT id, COALESCE(inventory, '[]') FROM characters`)
	if err != nil {
		return
	}
	type rec struct {
		id  int
		raw []byte
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if rows.Scan(&r.id, &r.raw) == nil {
			recs = append(recs, r)
		}
	}
	rows.Close()
	updated := 0
	for _, r := range recs {
		var inv []map[string]interface{}
		if json.Unmarshal(r.raw, &inv) != nil || len(inv) == 0 {
			continue
		}
		if fillItemWeights(inv) {
			b, _ := json.Marshal(inv)
			if _, err := db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, string(b), r.id); err == nil {
				updated++
			}
		}
	}
	if updated > 0 {
		log.Printf("Filled in item weights for %d characters", updated)
	}
}
