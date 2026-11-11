package main

// Tools, Adventuring Gear, and Mounts and Vehicles exactly as the SRD 5.2.1 prints them.
// data/srd521_equipment.json holds the verbatim text and tables (extracted from SRD pages 93-101
// by tools/extractors/extract_equipment.py). Each catalog row also carries the mechanics found in
// the text as data: a tool's ability, Utilize actions with their DCs and Craft list; a gear item's
// ability checks, saving throws, damage and carrying capacity.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

//go:embed data/srd521_equipment.json
var srd521EquipmentJSON []byte

type equipSection struct {
	Title string        `json:"title"`
	Text  []interface{} `json:"text"`
}

type equipItem struct {
	Name     string        `json:"name"`
	Cost     string        `json:"cost"`
	Category string        `json:"category"`
	Ability  string        `json:"ability"`
	Weight   string        `json:"weight"`
	Utilize  string        `json:"utilize"`
	Craft    string        `json:"craft"`
	Variants []string      `json:"variants"`
	Text     []interface{} `json:"text"`
}

type equipmentData struct {
	Intros map[string][]string `json:"intros"`
	Tools  struct {
		Intro    []string       `json:"intro"`
		Sections []equipSection `json:"sections"`
		Items    []equipItem    `json:"items"`
	} `json:"tools"`
	Gear struct {
		Items []equipItem `json:"items"`
	} `json:"gear"`
	MountsVehicles struct {
		Sections []equipSection `json:"sections"`
	} `json:"mounts_vehicles"`
}

func srdEquipment() equipmentData {
	var d equipmentData
	if err := json.Unmarshal(srd521EquipmentJSON, &d); err != nil {
		log.Printf("equipment: %v", err)
	}
	return d
}

var (
	utilizeRe = regexp.MustCompile(`([^,(]+?)\s*\(DC (\d+)\)`)
	checkRe   = regexp.MustCompile(`DC (\d+) (Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma)(?: \(([^)]+)\))? check`)
	saveRe    = regexp.MustCompile(`(Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) saving throw \(DC ([^)]+)\)`)
	damageRe  = regexp.MustCompile(`(\d+d\d+|\d+) (Acid|Bludgeoning|Cold|Fire|Force|Lightning|Necrotic|Piercing|Poison|Psychic|Radiant|Slashing|Thunder) damage`)
	holdsRe   = regexp.MustCompile(`(?:holds?|hold) up to (\d+) (pounds|pints|ounces|gallons|cubic feet)`)
)

// plainText joins the paragraphs of an entry (tables are left out).
func plainText(text []interface{}) string {
	var parts []string
	for _, x := range text {
		if s, ok := x.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

// utilizeEntries turns "Identify a substance (DC 15), or start a fire (DC 15)" into [{action, dc}].
func utilizeEntries(u string) []map[string]interface{} {
	var out []map[string]interface{}
	for _, m := range utilizeRe.FindAllStringSubmatch(u, -1) {
		desc := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m[1]), "or "))
		dc, _ := strconv.Atoi(m[2])
		out = append(out, map[string]interface{}{"action": desc, "dc": dc})
	}
	return out
}

// gearMechanics reads ability checks, saving throws, damage and capacity from an item's text.
func gearMechanics(text string) map[string]interface{} {
	mech := map[string]interface{}{}
	var checks []map[string]interface{}
	for _, m := range checkRe.FindAllStringSubmatch(text, -1) {
		dc, _ := strconv.Atoi(m[1])
		c := map[string]interface{}{"dc": dc, "ability": m[2]}
		if m[3] != "" {
			c["skill"] = m[3]
		}
		checks = append(checks, c)
	}
	if len(checks) > 0 {
		mech["checks"] = checks
	}
	var saves []map[string]interface{}
	for _, m := range saveRe.FindAllStringSubmatch(text, -1) {
		saves = append(saves, map[string]interface{}{"ability": m[1], "dc": m[2]})
	}
	if len(saves) > 0 {
		mech["saves"] = saves
	}
	var dmg []map[string]interface{}
	for _, m := range damageRe.FindAllStringSubmatch(text, -1) {
		dmg = append(dmg, map[string]interface{}{"dice": m[1], "type": strings.ToLower(m[2])})
	}
	if len(dmg) > 0 {
		mech["damage"] = dmg
	}
	if m := holdsRe.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		mech["capacity"] = map[string]interface{}{"amount": n, "unit": m[2]}
	}
	return mech
}

func costGP(text string) float64 {
	t := strings.ReplaceAll(strings.TrimSpace(text), ",", "")
	if v := parseCostGP(t); v >= 0 {
		return v
	}
	return -1
}

// equipmentRows are the catalog rows for tools and adventuring gear.
func equipmentRows() []catalogRow {
	d := srdEquipment()
	weights := map[string]float64{}
	for _, g := range srd521Gear {
		weights[weaponNameKey(g.Name)] = g.WeightLb
	}
	var out []catalogRow
	for _, t := range d.Tools.Items {
		data := map[string]interface{}{"tool_category": t.Category, "ability": t.Ability, "utilize": t.Utilize, "craft": t.Craft,
			"utilize_entries": utilizeEntries(t.Utilize), "weight_text": t.Weight, "description": plainText(t.Text)}
		if len(t.Variants) > 0 {
			data["variants"] = t.Variants
		}
		out = append(out, catalogRow{Category: "tool", Slug: itemSlug(t.Name), Name: t.Name, Source: "SRD 5.2.1", CostText: t.Cost, CostGP: costGP(t.Cost),
			WeightLb: weightPounds(t.Weight), Data: data})
	}
	for _, g := range d.Gear.Items {
		text := plainText(g.Text)
		data := map[string]interface{}{"description": text}
		if m := gearMechanics(text); len(m) > 0 {
			data["mechanics"] = m
		}
		w := -1.0
		if v, ok := weights[weaponNameKey(g.Name)]; ok {
			w = v
		}
		out = append(out, catalogRow{Category: "gear", Slug: itemSlug(g.Name), Name: g.Name, Source: "SRD 5.2.1", CostText: g.Cost, CostGP: costGP(g.Cost), WeightLb: w, Data: data})
	}
	return out
}

// ---------------- pages ----------------

func renderEntryText(text []interface{}) string {
	var b strings.Builder
	for _, x := range text {
		switch v := x.(type) {
		case string:
			b.WriteString("<p>" + e(v) + "</p>")
		case map[string]interface{}:
			if tbl, ok := v["table"].(map[string]interface{}); ok {
				b.WriteString(renderTable(fmt.Sprint(tbl["title"]), tbl["rows"]))
			}
		}
	}
	return b.String()
}

func renderTable(title string, rowsAny interface{}) string {
	rows, _ := rowsAny.([]interface{})
	var b strings.Builder
	b.WriteString(`<table class="ref">`)
	if title != "" && title != "<nil>" {
		b.WriteString("<caption>" + e(title) + "</caption>")
	}
	for i, r := range rows {
		cells, _ := r.([]interface{})
		tag := "td"
		if i == 0 {
			tag = "th"
		}
		b.WriteString("<tr>")
		for _, c := range cells {
			b.WriteString("<" + tag + ">" + e(fmt.Sprint(c)) + "</" + tag + ">")
		}
		b.WriteString("</tr>")
	}
	return b.String() + "</table>"
}

const tableStyle = `<style>table.ref{width:100%;border-collapse:collapse;margin:0 0 1.5em 0}table.ref th,table.ref td{text-align:left;padding:6px 8px;border-bottom:1px solid var(--note-border);vertical-align:top}table.ref th{background:var(--note-bg)}table.ref caption{text-align:left;font-weight:bold;padding:4px 0}</style>`

// equipmentPage builds /universe/gear, /universe/tools and their item pages.
func equipmentPage(category, rest string) string {
	d := srdEquipment()
	items, title, icon, base := d.Gear.Items, "Adventuring Gear", "🎒", "gear"
	if category == "tools" {
		items, title, icon, base = d.Tools.Items, "Tools", "🔧", "tools"
	}
	var b strings.Builder
	if rest != "" {
		for _, it := range items {
			if itemSlug(it.Name) != rest {
				continue
			}
			b.WriteString(tableStyle + fmt.Sprintf(`<h1>%s %s</h1>`, icon, e(it.Name)))
			var meta []string
			if it.Cost != "" {
				meta = append(meta, "<strong>Cost:</strong> "+e(it.Cost))
			}
			if it.Ability != "" {
				meta = append(meta, "<strong>Ability:</strong> "+e(it.Ability))
			}
			if it.Weight != "" {
				meta = append(meta, "<strong>Weight:</strong> "+e(it.Weight))
			}
			if len(meta) > 0 {
				b.WriteString("<p>" + strings.Join(meta, " &nbsp; ") + "</p>")
			}
			if it.Utilize != "" {
				b.WriteString("<p><strong>Utilize:</strong> " + e(it.Utilize) + "</p>")
			}
			if it.Craft != "" {
				b.WriteString("<p><strong>Craft:</strong> " + e(it.Craft) + "</p>")
			}
			if len(it.Variants) > 0 {
				b.WriteString("<p><strong>Variants:</strong> " + e(strings.Join(it.Variants, ", ")) + "</p>")
			}
			b.WriteString(renderEntryText(it.Text))
			b.WriteString(`<p><a href="/universe/` + base + `">← All ` + strings.ToLower(title) + `</a></p>`)
			return b.String()
		}
		return `<h1>Not found</h1><p><a href="/universe/` + base + `">← All ` + strings.ToLower(title) + `</a></p>`
	}
	b.WriteString(fmt.Sprintf(`<h1>%s %s</h1>`, icon, e(title)))
	if category == "tools" {
		for _, p := range d.Tools.Intro {
			b.WriteString("<p>" + e(p) + "</p>")
		}
		for _, s := range d.Tools.Sections {
			b.WriteString("<h2>" + e(s.Title) + "</h2>" + renderEntryText(s.Text))
		}
	} else {
		for _, p := range d.Intros["Adventuring Gear"] {
			b.WriteString("<p>" + e(p) + "</p>")
		}
	}
	var list []browseItem
	for _, it := range items {
		extra := it.Cost
		if it.Ability != "" {
			extra += " • " + it.Ability
		}
		group := gearGroup(it.Name)
		facets := map[string][]string{"price": {priceBand(costGP(it.Cost))}, "kind": {group}}
		if category == "tools" {
			group = "Other Tools"
			if strings.Contains(strings.ToLower(it.Name), "supplies") || strings.Contains(strings.ToLower(it.Name), "tools") || strings.Contains(strings.ToLower(it.Name), "kit") && it.Ability != "" {
				group = "Artisan's Tools"
			}
			facets = map[string][]string{"price": {priceBand(costGP(it.Cost))}, "ability": {it.Ability}}
		}
		list = append(list, browseItem{Href: "/universe/" + base + "/" + itemSlug(it.Name), Name: it.Name, Meta: extra, Group: group, Facets: facets})
	}
	second := browseFacet{Key: "kind", Label: "Kind"}
	if category == "tools" {
		second = browseFacet{Key: "ability", Label: "Ability"}
	}
	page := browseList(icon, title, list, []browseFacet{second, {Key: "price", Label: "Price", Order: priceOrder}}, nil)
	page = page[strings.Index(page, "</style>")+len("</style>"):] // the heading was written above with the SRD intro
	page = page[strings.Index(page, "</h1>")+len("</h1>"):]
	b.WriteString(browseStyle + page)
	return b.String()
}

// mountsVehiclesPage prints the SRD sections and the three tables (from the catalog data).
func mountsVehiclesPage() string {
	d := srdEquipment()
	var b strings.Builder
	b.WriteString(tableStyle + `<h1>🐎 Mounts and Vehicles</h1>`)
	for _, p := range d.Intros["Mounts and Vehicles"] {
		b.WriteString("<p>" + e(p) + "</p>")
	}
	table := func(title string, head []string, rows [][]string) {
		b.WriteString(`<table class="ref"><caption>` + e(title) + `</caption><tr>`)
		for _, h := range head {
			b.WriteString("<th>" + e(h) + "</th>")
		}
		b.WriteString("</tr>")
		for _, r := range rows {
			b.WriteString("<tr>")
			for _, c := range r {
				b.WriteString("<td>" + e(c) + "</td>")
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</table>")
	}
	lb := func(f *float64) string {
		if f == nil {
			return "—"
		}
		return fmt.Sprintf("%g lb.", *f)
	}
	var mounts, tack, ships [][]string
	for _, it := range catalogItems("mount") {
		mounts = append(mounts, []string{it.Name, str(it.Data, "carrying_capacity_lb") + " lb.", it.CostText})
	}
	for _, it := range catalogItems("vehicle") {
		if str(it.Data, "speed") != "" {
			ships = append(ships, []string{it.Name, str(it.Data, "speed"), str(it.Data, "crew"), str(it.Data, "passengers"), str(it.Data, "cargo_tons"),
				str(it.Data, "armor_class"), str(it.Data, "hit_points"), str(it.Data, "damage_threshold"), it.CostText})
		} else {
			tack = append(tack, []string{it.Name, lb(it.WeightLb), it.CostText})
		}
	}
	for _, s := range d.MountsVehicles.Sections {
		b.WriteString("<h2>" + e(s.Title) + "</h2>")
		for _, x := range s.Text {
			if p, ok := x.(string); ok {
				b.WriteString("<p>" + e(p) + "</p>")
			}
		}
		switch s.Title {
		case "Mounts and Cargo":
			table("Mounts and Other Animals", []string{"Item", "Carrying Capacity", "Cost"}, mounts)
		case "Saddles":
			table("Tack, Harness, and Drawn Vehicles", []string{"Item", "Weight", "Cost"}, tack)
		case "Large Vehicles":
			table("Airborne and Waterborne Vehicles", []string{"Ship", "Speed", "Crew", "Passengers", "Cargo (Tons)", "AC", "HP", "Damage Threshold", "Cost"}, ships)
		}
	}
	return b.String()
}

// srdUtilizeDC finds the DC the SRD gives for an action with a tool ("Pick a lock (DC 15)"). action is matched as text.
func srdUtilizeDC(tool, action string) (dc int, ability string, ok bool) {
	want := itemSlug(strings.TrimSpace(tool))
	act := strings.ToLower(strings.TrimSpace(action))
	for _, it := range srdEquipment().Tools.Items {
		if itemSlug(it.Name) != want {
			continue
		}
		for _, e := range utilizeEntries(it.Utilize) {
			if strings.Contains(strings.ToLower(fmt.Sprint(e["action"])), act) {
				return e["dc"].(int), strings.ToLower(it.Ability), true
			}
		}
	}
	return 0, "", false
}
