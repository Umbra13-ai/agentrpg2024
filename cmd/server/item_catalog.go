package main

// Item catalog: one table (`items`) for weapons, armor, adventuring gear and
// tools, each row tagged with the ruleset it belongs to. The data comes from
// the SRD 5.2.1 tables already loaded in memory, so the catalog is rebuilt at
// startup and always matches the code.
//	GET /api/universe/items?category=weapon|armor|gear|tool&ruleset=2024&q=sword
//	GET /api/universe/items/{category}/{slug}

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

const catalogRuleset2024 = "2024"

func ensureItemCatalogTable() {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS items (
		id SERIAL PRIMARY KEY,
		category VARCHAR(20) NOT NULL,
		slug VARCHAR(100) NOT NULL,
		name VARCHAR(120) NOT NULL,
		ruleset VARCHAR(10) NOT NULL DEFAULT '2024',
		source VARCHAR(60) NOT NULL DEFAULT 'SRD 5.2.1',
		cost_gp DECIMAL(10,2),
		cost_text VARCHAR(30),
		weight_lb DECIMAL(7,2),
		data JSONB DEFAULT '{}',
		UNIQUE (category, slug, ruleset)
	)`)
	if err != nil {
		log.Printf("items table: %v", err)
	}
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

// itemSlug makes "Armor, +1, +2, or +3" into "armor-1-2-or-3" and "Thieves' Tools" into "thieves-tools".
func itemSlug(name string) string {
	n := strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	return strings.Trim(slugJunk.ReplaceAllString(n, "-"), "-")
}

// parseCostGP reads "15 gp", "2 sp" and "5 cp" into gold pieces.
func parseCostGP(text string) float64 {
	var n float64
	var unit string
	if _, err := fmt.Sscanf(text, "%f %s", &n, &unit); err != nil {
		return -1
	}
	switch strings.ToLower(unit) {
	case "gp":
		return n
	case "sp":
		return n / 10
	case "cp":
		return n / 100
	case "ep":
		return n / 2
	case "pp":
		return n * 10
	}
	return -1
}

type catalogRow struct {
	Category, Slug, Name, Source, CostText string
	CostGP, WeightLb                       float64
	Data                                   map[string]interface{}
}

// buildItemCatalog gathers every SRD item from the in-memory tables.
func buildItemCatalog() []catalogRow {
	var rows []catalogRow
	rows = append(rows, weaponRows()...) // the SRD weapon table, as printed
	rows = append(rows, armorRows()...) // the SRD armor table, as printed
	rows = append(rows, equipmentRows()...) // tools and adventuring gear, as printed in the SRD
	rows = append(rows, magicItemRows()...)
	rows = append(rows, mountAndVehicleRows()...)
	return rows
}

// seedItemCatalog rebuilds the 2024 rows of the items table.
func seedItemCatalog() {
	ensureItemCatalogTable()
	ensureConditionRecordsTable()
	reconcileAllConditions() // one set of conditions per character (see conditions_unified.go)
	ensureBarbarianColumns()
	ensureCombatEffectsTable()
	ensureSpellEffectsTable()
	ensureInvocationColumns()
	ensureMasteryColumns()
	ensureRogueColumns()
	ensureMonkColumns()
	ensureDruidColumns()
	ensureFeatColumns()
	ensureExtraColumns()
	ensureCasterColumns()
	ensureBardColumns()
	ensureRageColumns()
	if _, err := db.Exec(`DELETE FROM items WHERE ruleset = $1`, catalogRuleset2024); err != nil {
		log.Printf("item catalog: %v", err)
		return
	}
	n := 0
	var firstErr error
	for _, r := range buildItemCatalog() {
		data, _ := json.Marshal(r.Data)
		var cost, weight interface{}
		if r.CostGP >= 0 {
			cost = r.CostGP
		}
		if r.WeightLb >= 0 {
			weight = r.WeightLb
		}
		var costText interface{}
		if r.CostText != "" {
			costText = r.CostText
		}
		if _, err := db.Exec(`INSERT INTO items (category, slug, name, ruleset, source, cost_gp, cost_text, weight_lb, data)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (category, slug, ruleset) DO NOTHING`,
			r.Category, r.Slug, r.Name, catalogRuleset2024, r.Source, cost, costText, weight, data); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			n++
		}
	}
	if firstErr != nil {
		log.Printf("item catalog: some rows failed, first error: %v", firstErr)
	}
	log.Printf("Item catalog: %d items", n)
}

func itemRowJSON(category, slug, name, ruleset, source string, cost, weight *float64, costText *string, data []byte) map[string]interface{} {
	m := map[string]interface{}{"category": category, "slug": slug, "name": name, "ruleset": ruleset, "source": source}
	if cost != nil {
		m["cost_gp"] = *cost
	}
	if costText != nil {
		m["cost"] = *costText
	}
	if weight != nil {
		m["weight_lb"] = *weight
	}
	var d map[string]interface{}
	if json.Unmarshal(data, &d) == nil && len(d) > 0 {
		for k, v := range d {
			m[k] = v
		}
	}
	return m
}

func handleUniverseItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/universe/items"), "/")
	if rest != "" { // one item
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "use /api/universe/items/{category}/{slug}"})
			return
		}
		ruleset := r.URL.Query().Get("ruleset")
		if ruleset == "" {
			ruleset = catalogRuleset2024
		}
		var name, source string
		var cost, weight *float64
		var costText *string
		var data []byte
		if db.QueryRow(`SELECT name, source, cost_gp, cost_text, weight_lb, COALESCE(data, '{}') FROM items WHERE category = $1 AND slug = $2 AND ruleset = $3`,
			parts[0], parts[1], ruleset).Scan(&name, &source, &cost, &costText, &weight, &data) != nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "item_not_found"})
			return
		}
		writeJSONStatus(w, http.StatusOK, itemRowJSON(parts[0], parts[1], name, ruleset, source, cost, weight, costText, data))
		return
	}
	q := r.URL.Query()
	ruleset := q.Get("ruleset")
	if ruleset == "" {
		ruleset = catalogRuleset2024
	}
	sqlText := `SELECT category, slug, name, source, cost_gp, cost_text, weight_lb, COALESCE(data, '{}') FROM items WHERE ruleset = $1`
	args := []interface{}{ruleset}
	if c := strings.ToLower(q.Get("category")); c != "" {
		args = append(args, c)
		sqlText += " AND category = $2"
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		args = append(args, "%"+strings.ToLower(s)+"%")
		sqlText += " AND LOWER(name) LIKE $" + itoa(len(args))
	}
	sqlText += " ORDER BY category, name"
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "db_error"})
		return
	}
	defer rows.Close()
	items := []map[string]interface{}{}
	counts := map[string]int{}
	for rows.Next() {
		var category, slug, name, source string
		var cost, weight *float64
		var costText *string
		var data []byte
		if rows.Scan(&category, &slug, &name, &source, &cost, &costText, &weight, &data) == nil {
			row := itemRowJSON(category, slug, name, ruleset, source, cost, weight, costText, data)
			if q.Get("full") == "" {
				delete(row, "description")
			}
			items = append(items, row)
			counts[category]++
		}
	}
	cats := make([]string, 0, len(counts))
	for c := range counts {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"ruleset": ruleset, "count": len(items), "by_category": counts, "categories": cats, "items": items})
}
