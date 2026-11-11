package main

// Weapons exactly as the SRD 5.2.1 (and the Player's Handbook, p.215) prints them: four
// groups (Simple Melee, Simple Ranged, Martial Melee, Martial Ranged) in a table of
// Name, Damage, Properties, Mastery, Weight and Cost. The text comes from
// data/srd521_weapons.json, which was taken from the SRD table.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
)

//go:embed data/srd521_weapons.json
var srd521WeaponsJSON []byte

type srdWeaponRow struct {
	Name       string `json:"name"`
	Category   string `json:"category"`
	Damage     string `json:"damage"`
	DamageType string `json:"damage_type"`
	Properties string `json:"properties"`
	Mastery    string `json:"mastery"`
	Weight     string `json:"weight"`
	Cost       string `json:"cost"`
}

var weaponGroups = []string{"Simple Melee Weapons", "Simple Ranged Weapons", "Martial Melee Weapons", "Martial Ranged Weapons"}

func srdWeaponTable() []srdWeaponRow {
	var rows []srdWeaponRow
	if err := json.Unmarshal(srd521WeaponsJSON, &rows); err != nil {
		log.Printf("weapons: %v", err)
	}
	return rows
}

// weightPounds reads "2 lb.", "1/4 lb." and "—" (-1: no weight).
func weightPounds(text string) float64 {
	t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "lb."))
	if t == "" || t == "—" || t == "-" {
		return -1
	}
	if i := strings.Index(t, "/"); i > 0 {
		a, _ := strconv.ParseFloat(strings.TrimSpace(t[:i]), 64)
		b, _ := strconv.ParseFloat(strings.TrimSpace(t[i+1:]), 64)
		if b != 0 {
			return a / b
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return -1
	}
	return f
}

func weaponRows() []catalogRow {
	var out []catalogRow
	for _, w := range srdWeaponTable() {
		kind := "melee"
		if strings.Contains(w.Category, "Ranged") {
			kind = "ranged"
		}
		out = append(out, catalogRow{Category: "weapon", Slug: itemSlug(w.Name), Name: w.Name, Source: "SRD 5.2.1",
			CostText: w.Cost, CostGP: parseCostGP(w.Cost), WeightLb: weightPounds(w.Weight),
			Data: map[string]interface{}{"weapon_category": w.Category, "type": kind, "damage": w.Damage, "damage_type": w.DamageType,
				"properties_text": w.Properties, "mastery": w.Mastery, "weight_text": w.Weight}})
	}
	return out
}

// weaponsPage is the /universe/weapons page: one table per group, as printed.
func weaponsPage() string {
	var b strings.Builder
	b.WriteString(`<h1>🗡️ Weapons</h1>`)
	b.WriteString(`<style>table.ref{width:100%;border-collapse:collapse;margin:0 0 1.5em 0}table.ref th,table.ref td{text-align:left;padding:6px 8px;border-bottom:1px solid var(--note-border);vertical-align:top}table.ref th{background:var(--note-bg)}table.ref td.num{white-space:nowrap}</style>`)
	byGroup := map[string][]srdWeaponRow{}
	for _, w := range srdWeaponTable() {
		byGroup[w.Category] = append(byGroup[w.Category], w)
	}
	for _, g := range weaponGroups {
		b.WriteString(fmt.Sprintf(`<h2>%s</h2><table class="ref"><thead><tr><th>Name</th><th>Damage</th><th>Properties</th><th>Mastery</th><th>Weight</th><th>Cost</th></tr></thead><tbody>`, e(g)))
		for _, w := range byGroup[g] {
			b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s %s</td><td>%s</td><td>%s</td><td class="num">%s</td><td class="num">%s</td></tr>`,
				e(w.Name), e(w.Damage), e(w.DamageType), e(w.Properties), e(w.Mastery), e(w.Weight), e(w.Cost)))
		}
		b.WriteString(`</tbody></table>`)
	}
	return b.String()
}
