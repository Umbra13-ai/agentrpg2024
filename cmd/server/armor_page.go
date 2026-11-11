package main

// Armor exactly as the SRD 5.2.1 (and the Player's Handbook, p.219) prints it: the rules text
// (Category, Armor Class, Strength, Stealth, Armor Training, One at a Time) and the table
// grouped as Light, Medium, Heavy and Shield with Armor Class, Strength, Stealth, Weight and
// Cost. The text comes from data/srd521_armor.json, taken from the SRD page. The `armor`
// table that the game reads is brought into line with it at startup.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

//go:embed data/srd521_armor.json
var srd521ArmorJSON []byte

type srdArmorRow struct {
	Name     string `json:"name"`
	AC       string `json:"ac"`
	Strength string `json:"strength"`
	Stealth  string `json:"stealth"`
	Weight   string `json:"weight"`
	Cost     string `json:"cost"`
}

type srdArmorGroup struct {
	Name  string        `json:"name"`
	Note  string        `json:"note"`
	Armor []srdArmorRow `json:"armor"`
}

type srdRuleBlock struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type srdArmorData struct {
	Rules  []srdRuleBlock  `json:"rules"`
	Groups []srdArmorGroup `json:"groups"`
}

func srdArmorTable() srdArmorData {
	var d srdArmorData
	if err := json.Unmarshal(srd521ArmorJSON, &d); err != nil {
		log.Printf("armor: %v", err)
	}
	return d
}

var leadingNumber = regexp.MustCompile(`^\+?(\d+)`)

// armorBaseAC reads the number at the start of "11 + Dex modifier", "16" or "+2".
func armorBaseAC(text string) int {
	if m := leadingNumber.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// armorStrengthNeeded reads "Str 13" (0 for "—").
func armorStrengthNeeded(text string) int {
	f := strings.Fields(text)
	if len(f) == 2 && strings.EqualFold(f[0], "Str") {
		n, _ := strconv.Atoi(f[1])
		return n
	}
	return 0
}

func armorGroupType(group string) string {
	return strings.ToLower(strings.TrimSuffix(group, " Armor")) // light, medium, heavy, shield
}

// armorSlugFromName gives the slug the game uses for armor ("Studded Leather Armor" -> studded-leather-armor).
func armorSlugFromName(name string) string { return armorSlugFor(name) }

func armorRows() []catalogRow {
	var out []catalogRow
	for _, g := range srdArmorTable().Groups {
		for _, a := range g.Armor {
			out = append(out, catalogRow{Category: "armor", Slug: itemSlug(a.Name), Name: a.Name, Source: "SRD 5.2.1",
				CostText: a.Cost, CostGP: parseCostGP(strings.ReplaceAll(a.Cost, ",", "")), WeightLb: weightPounds(a.Weight),
				Data: map[string]interface{}{"armor_category": g.Name, "armor_type": armorGroupType(g.Name), "donning": g.Note,
					"ac_text": a.AC, "ac": armorBaseAC(a.AC), "strength_text": a.Strength, "strength_required": armorStrengthNeeded(a.Strength),
					"stealth_text": a.Stealth, "stealth_disadvantage": strings.EqualFold(a.Stealth, "Disadvantage"), "weight_text": a.Weight}})
		}
	}
	return out
}

// syncArmorToSRDTable makes the armor table the game reads agree with the printed SRD table.
func syncArmorToSRDTable() {
	n := 0
	for _, g := range srdArmorTable().Groups {
		for _, a := range g.Armor {
			slug := armorSlugFromName(a.Name)
			lb := weightPounds(a.Weight)
			if lb < 0 {
				lb = 0
			}
			res, err := db.Exec(`INSERT INTO armor (slug, name, type, ac, ac_bonus, str_req, stealth_disadvantage, weight, source)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'srd')
				ON CONFLICT (slug) DO UPDATE SET name=EXCLUDED.name, type=EXCLUDED.type, ac=EXCLUDED.ac, ac_bonus=EXCLUDED.ac_bonus,
					str_req=EXCLUDED.str_req, stealth_disadvantage=EXCLUDED.stealth_disadvantage, weight=EXCLUDED.weight`,
				slug, a.Name, armorGroupType(g.Name), armorBaseAC(a.AC), a.AC, armorStrengthNeeded(a.Strength), strings.EqualFold(a.Stealth, "Disadvantage"), lb)
			if err != nil {
				log.Printf("SRD armor %s: %v", slug, err)
				continue
			}
			if c, _ := res.RowsAffected(); c > 0 {
				n++
			}
		}
	}
	// rows an earlier version of this sync made under shortened slugs
	db.Exec(`DELETE FROM armor WHERE slug IN ('padded','leather','studded-leather','hide','half-plate','splint','plate')`)
	log.Printf("Armor: %d rows match the SRD 5.2.1 table", n)
}

// armorPage is /universe/armor: the rules text, then one table per group, as printed.
func armorPage() string {
	d := srdArmorTable()
	var b strings.Builder
	b.WriteString(`<h1>🛡️ Armor</h1>`)
	b.WriteString(`<style>table.ref{width:100%;border-collapse:collapse;margin:0 0 1.5em 0}table.ref th,table.ref td{text-align:left;padding:6px 8px;border-bottom:1px solid var(--note-border);vertical-align:top}table.ref th{background:var(--note-bg)}table.ref tr.grp td{font-style:italic;background:var(--note-bg)}table.ref td.num{white-space:nowrap}</style>`)
	b.WriteString(`<table class="ref"><thead><tr><th>Armor</th><th>Armor Class (AC)</th><th>Strength</th><th>Stealth</th><th>Weight</th><th>Cost</th></tr></thead><tbody>`)
	for _, g := range d.Groups {
		b.WriteString(fmt.Sprintf(`<tr class="grp"><td colspan="6">%s (%s)</td></tr>`, e(g.Name), e(g.Note)))
		for _, a := range g.Armor {
			b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="num">%s</td><td class="num">%s</td></tr>`,
				e(a.Name), e(a.AC), e(a.Strength), e(a.Stealth), e(a.Weight), e(a.Cost)))
		}
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}
