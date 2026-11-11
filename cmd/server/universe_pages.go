package main

// Universe pages and APIs for the item-like categories. Everything here reads
// the SRD 5.2.1 item catalog (`items` table) and the 2024 species and
// background data, so the numbers on /universe are always the real counts.

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// catalogCount counts catalog items of one category (0 if the table is missing).
func catalogCount(category string) int {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM items WHERE ruleset = $1 AND category = $2`, catalogRuleset2024, category).Scan(&n)
	return n
}

type catalogItem struct {
	Slug, Name, Source, CostText string
	CostGP, WeightLb             *float64
	Data                         map[string]interface{}
}

// catalogItems loads every catalog item of the given categories, ordered by name.
func catalogItems(categories ...string) []catalogItem {
	var out []catalogItem
	for _, c := range categories {
		rows, err := db.Query(`SELECT slug, name, source, cost_gp, COALESCE(cost_text, ''), weight_lb, COALESCE(data, '{}') FROM items
			WHERE ruleset = $1 AND category = $2 ORDER BY name`, catalogRuleset2024, c)
		if err != nil {
			continue
		}
		for rows.Next() {
			var it catalogItem
			var raw []byte
			if rows.Scan(&it.Slug, &it.Name, &it.Source, &it.CostGP, &it.CostText, &it.WeightLb, &raw) == nil {
				json.Unmarshal(raw, &it.Data)
				out = append(out, it)
			}
		}
		rows.Close()
	}
	return out
}

func str(m map[string]interface{}, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// ---------------- JSON APIs (same shapes as before, now from the catalog) ----------------

func handleUniverseSpeciesList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	names := []string{}
	var details []map[string]interface{}
	for _, sp := range game.AllSpecies() {
		names = append(names, sp.Key)
		details = append(details, speciesWithText(sp))
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"names": names, "species": details, "count": len(names), "ruleset": "2024 (SRD 5.2.1)"})
}

func speciesJSON(sp game.Species) map[string]interface{} {
	return map[string]interface{}{"key": sp.Key, "name": sp.Name, "sizes": sp.Sizes, "speed": sp.Speed, "darkvision": sp.Darkvision,
		"choices_needed": sp.Needs, "traits": sp.Traits, "ability_bonuses": map[string]int{}, "note": "2024 species give no ability scores or languages; the background does."}
}

// speciesWithText adds the SRD text of the species (label lines and the text with its tables).
func speciesWithText(sp game.Species) map[string]interface{} {
	m := speciesJSON(sp)
	if intro := speciesIntro(sp.Name); intro != "" {
		m["description"] = intro
	}
	if it, ok := originByName(srdOrigins().Species.Items, sp.Name); ok {
		m["srd_fields"], m["srd_text"] = it.Fields, it.Blocks
	}
	return m
}

func handleUniverseSpeciesOne(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimPrefix(r.URL.Path, "/api/universe/species/")
	sp := game.GetSpecies(id)
	if sp == nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "race_not_found"})
		return
	}
	json.NewEncoder(w).Encode(speciesWithText(*sp))
}

func handleUniverseWeapons(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	weapons := map[string]interface{}{}
	for _, it := range catalogItems("weapon") {
		var weight float64
		if it.WeightLb != nil {
			weight = *it.WeightLb
		}
		weapons[it.Slug] = map[string]interface{}{"name": it.Name, "category": str(it.Data, "weapon_category"), "type": str(it.Data, "type"),
			"damage": str(it.Data, "damage"), "damage_type": str(it.Data, "damage_type"), "properties": str(it.Data, "properties_text"),
			"mastery": str(it.Data, "mastery"), "weight": weight, "weight_text": str(it.Data, "weight_text"), "cost": it.CostText}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"weapons": weapons, "count": len(weapons)})
}

func handleUniverseArmor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	armor := map[string]interface{}{}
	for _, it := range catalogItems("armor") {
		var weight float64
		if it.WeightLb != nil {
			weight = *it.WeightLb
		}
		armor[it.Slug] = map[string]interface{}{"name": it.Name, "category": str(it.Data, "armor_category"), "type": str(it.Data, "armor_type"),
			"donning": str(it.Data, "donning"), "ac": it.Data["ac"], "armor_class": str(it.Data, "ac_text"), "strength": str(it.Data, "strength_text"),
			"str_req": it.Data["strength_required"], "stealth": str(it.Data, "stealth_text"), "stealth_disadvantage": it.Data["stealth_disadvantage"],
			"weight": weight, "cost": it.CostText}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"armor": armor, "count": len(armor)})
}

func magicItemJSON(it catalogItem) map[string]interface{} {
	return map[string]interface{}{"slug": it.Slug, "name": it.Name, "rarity": str(it.Data, "rarity"), "type": str(it.Data, "item_type"),
		"attunement": it.Data["requires_attunement"], "attunement_note": str(it.Data, "attunement_note"), "description": str(it.Data, "description")}
}

func handleUniverseMagicItems(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	items := map[string]interface{}{}
	for _, it := range catalogItems("magic") {
		m := magicItemJSON(it)
		if r.URL.Query().Get("full") == "" {
			delete(m, "description")
		}
		items[it.Slug] = m
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"magic_items": items, "count": len(items), "note": "add ?full=1 for the full text"})
}

func handleUniverseMagicItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	slug := strings.TrimPrefix(r.URL.Path, "/api/universe/magic-items/")
	for _, it := range catalogItems("magic") {
		if it.Slug == slug {
			json.NewEncoder(w).Encode(magicItemJSON(it))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"error": "magic item not found"})
}

// ---------------- HTML pages ----------------

const filterScript = `<script>function filterList(q){document.querySelectorAll('.list-item').forEach(el=>{el.style.display=el.dataset.name.includes(q.toLowerCase())?'block':'none'})}</script>`

func e(s string) string { return html.EscapeString(s) }

func paragraphs(text string) string {
	var b strings.Builder
	for _, p := range strings.Split(text, "\n") {
		if strings.TrimSpace(p) != "" {
			b.WriteString("<p>" + e(strings.TrimSpace(p)) + "</p>")
		}
	}
	return b.String()
}

var rarityOrder = map[string]int{"Common": 0, "Uncommon": 1, "Rare": 2, "Very Rare": 3, "Legendary": 4, "Artifact": 5, "Rarity Varies": 6}

// universeCatalogPage builds the page for the catalog-backed categories. ok is false for other categories.
func universeCatalogPage(category, rest string) (string, bool) {
	var b strings.Builder
	switch category {
	case "weapons":
		return weaponsPage(), true
	case "armor":
		return armorPage(), true
	case "gear", "tools":
		return equipmentPage(category, rest), true
	case "mounts-vehicles":
		return mountsVehiclesPage(), true
	case "feats":
		return featsPage(rest), true
	case "rules":
		return rulesPage(rest), true
	case "species", "backgrounds":
		return originsPage(category, rest), true
	case "magic-items":
		items := catalogItems("magic")
		if rest != "" {
			for _, it := range items {
				if it.Slug == rest {
					att := ""
					if v, _ := it.Data["requires_attunement"].(bool); v {
						att = " (requires attunement" + strings.TrimSpace(" "+str(it.Data, "attunement_note")) + ")"
					}
					body := paragraphs(str(it.Data, "description"))
					if blocks, ok := it.Data["description_blocks"].([]interface{}); ok && len(blocks) > 0 {
						body = tableStyle + renderEntryText(blocks)
					}
					if sb, ok := it.Data["statblock"]; ok && sb != nil {
						raw, _ := json.Marshal(sb)
						body += statblockHTML(raw)
					}
					b.WriteString(fmt.Sprintf(`<h1>💎 %s</h1><p class="muted">%s, %s%s</p>%s<p><a href="/universe/magic-items">← All magic items</a></p>`,
						e(it.Name), e(str(it.Data, "item_type")), e(str(it.Data, "rarity_text")), e(att), body))
					return b.String(), true
				}
			}
			return `<h1>Magic item not found</h1><p><a href="/universe/magic-items">← All magic items</a></p>`, true
		}
		sort.SliceStable(items, func(i, j int) bool {
			ri, rj := rarityOrder[str(items[i].Data, "rarity")], rarityOrder[str(items[j].Data, "rarity")]
			return ri < rj
		})
		var list []browseItem
		rarities := []string{"Common", "Uncommon", "Rare", "Very Rare", "Legendary", "Artifact", "Rarity Varies"}
		for _, it := range items {
			att, attFacet := "", "No"
			if v, _ := it.Data["requires_attunement"].(bool); v {
				att, attFacet = " • attunement", "Yes"
			}
			typ := strings.TrimSpace(strings.SplitN(str(it.Data, "item_type"), "(", 2)[0]) // "Weapon (Any Sword)" -> "Weapon"
			rarity := str(it.Data, "rarity")
			if rarity == "" {
				rarity = "Rarity Varies"
			}
			list = append(list, browseItem{Href: "/universe/magic-items/" + it.Slug, Name: it.Name, Group: rarity,
				Meta: fmt.Sprintf("%s • %s%s", str(it.Data, "item_type"), str(it.Data, "rarity"), att),
				Facets: map[string][]string{"type": {typ}, "rarity": {rarity}, "attunement": {attFacet}}})
		}
		b.WriteString(browseList("💎", "Magic Items", list, []browseFacet{
			{Key: "rarity", Label: "Rarity", Order: rarities},
			{Key: "type", Label: "Type"},
			{Key: "attunement", Label: "Attunement"},
		}, rarities))
	default:
		return "", false
	}
	return b.String(), true
}
