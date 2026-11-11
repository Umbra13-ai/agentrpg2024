package main

// Monster pages and API (2024 rules). Players learn what a creature is: name, size, type, alignment, a physical
// description and lore (original text, stored in monsters.description and monsters.lore). The full stat block (Armor
// Class, hit points, abilities, saves, actions, ...) is for the GM only: GET /api/universe/monsters/{slug} with a GM's token.

import (
	"encoding/json"
	"net/http"
	"strings"
)

func rulesetFilter(r *http.Request) string {
	switch r.URL.Query().Get("ruleset") {
	case "all":
		return "TRUE"
	}
	return "ruleset = '2024'"
}

// handleUniverseMonster: GET /api/universe/monsters/{slug}.
func handleUniverseMonster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	slug := strings.TrimPrefix(r.URL.Path, "/api/universe/monsters/")
	var name, size, typ, align, desc, lore, ruleset string
	if db.QueryRow(`SELECT name, COALESCE(size,''), COALESCE(type,''), COALESCE(alignment,''), COALESCE(description,''), COALESCE(lore,''), COALESCE(ruleset,'2024') FROM monsters WHERE slug = $1`, slug).
		Scan(&name, &size, &typ, &align, &desc, &lore, &ruleset) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "monster_not_found"})
		return
	}
	if !requestIsGM(r) {
		out := map[string]interface{}{"slug": slug, "name": name, "size": size, "type": typ}
		if align != "" {
			out["alignment"] = align
		}
		if desc != "" {
			out["description"] = desc
		}
		if lore != "" {
			out["lore"] = lore
		}
		out["note"] = "Armor Class, hit points, abilities and actions are for the GM. A GM signs in with their token to read the full stat block."
		json.NewEncoder(w).Encode(out)
		return
	}
	row := map[string]interface{}{}
	cols := []string{"slug", "name", "size", "type", "alignment", "ac", "hp", "hp_formula", "hit_dice", "speed", "speed_text", "initiative_mod", "proficiency_bonus",
		"str", "dex", "con", "intl", "wis", "cha", "saves", "skills", "damage_vulnerabilities", "damage_resistances", "damage_immunities", "condition_immunities",
		"gear", "senses", "languages", "cr", "xp", "xp_lair", "group_name", "traits", "action_entries", "bonus_actions", "reactions", "legendary_intro",
		"legendary_actions", "legendary_action_count", "legendary_resistances", "actions", "ruleset", "description", "lore"}
	sel := make([]string, len(cols))
	for i, c := range cols {
		switch c {
		case "saves", "traits", "action_entries", "bonus_actions", "reactions", "legendary_actions", "actions":
			sel[i] = "COALESCE(" + c + ", 'null'::jsonb)::text"
		case "ac", "hp", "speed", "str", "dex", "con", "intl", "wis", "cha", "xp", "legendary_action_count", "legendary_resistances", "initiative_mod", "proficiency_bonus", "xp_lair":
			sel[i] = "COALESCE(" + c + ", 0)"
		default:
			sel[i] = "COALESCE(" + c + "::text, '')"
		}
	}
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := db.QueryRow("SELECT "+strings.Join(sel, ", ")+" FROM monsters WHERE slug = $1", slug).Scan(ptrs...); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "read_failed"})
		return
	}
	for i, c := range cols {
		v := vals[i]
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		switch c {
		case "saves", "traits", "action_entries", "bonus_actions", "reactions", "legendary_actions", "actions":
			var x interface{}
			if s, ok := v.(string); ok && json.Unmarshal([]byte(s), &x) == nil {
				v = x
			}
		case "intl":
			c = "int"
		}
		row[c] = v
	}
	json.NewEncoder(w).Encode(row)
}

// monstersPage is /universe/monsters and /universe/monsters/{slug}: the player view.
func monstersPage(rest string) string {
	if rest != "" {
		var name, size, typ, align, desc, lore string
		if db.QueryRow(`SELECT name, COALESCE(size,''), COALESCE(type,''), COALESCE(alignment,''), COALESCE(description,''), COALESCE(lore,'') FROM monsters WHERE slug = $1`, rest).
			Scan(&name, &size, &typ, &align, &desc, &lore) != nil {
			return `<h1>Not found</h1><p><a href="/universe/monsters">← All monsters</a></p>`
		}
		var b strings.Builder
		b.WriteString("<h1>👹 " + e(name) + "</h1>")
		line := strings.TrimSpace(titleWords(size) + " " + titleWords(typ))
		if align != "" {
			line += ", " + align
		}
		b.WriteString(`<p class="muted">` + e(line) + "</p>")
		if desc != "" {
			b.WriteString("<h2>Appearance</h2><p>" + e(desc) + "</p>")
		}
		if lore != "" {
			b.WriteString("<h2>Lore</h2><p>" + e(lore) + "</p>")
		}
		b.WriteString(`<p><a href="/universe/monsters">← All monsters</a></p>`)
		return b.String()
	}
	rows, err := db.Query(`SELECT slug, name, COALESCE(type,''), COALESCE(size,'') FROM monsters WHERE ruleset = '2024' ORDER BY name`)
	var items []browseItem
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var slug, name, typ, size string
			if rows.Scan(&slug, &name, &typ, &size) != nil {
				continue
			}
			baseType := titleWords(strings.TrimSpace(strings.SplitN(typ, "(", 2)[0])) // "Humanoid (Goblinoid)" -> "Humanoid"
			group := strings.ToUpper(name[:1])
			items = append(items, browseItem{Href: "/universe/monsters/" + slug, Name: name, Meta: strings.TrimSpace(titleWords(size) + " " + titleWords(typ)), Group: group,
				Facets: map[string][]string{"type": {baseType}, "size": {titleWords(size)}}})
		}
	}
	return browseList("👹", "Monsters", items, []browseFacet{
		{Key: "type", Label: "Type"},
		{Key: "size", Label: "Size", Order: []string{"Tiny", "Small", "Medium", "Large", "Huge", "Gargantuan"}},
	}, nil)
}
