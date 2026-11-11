package main

// SRD 5.2.1 weapon data layered over the weapons loaded from the database.
// The weapon rows of the SRD 5.2.1; these entries bring the
// weapons in line with the 2024 SRD (damage, weight, properties, Mastery,
// and the new Musket and Pistol).

import (
	"log"
	"regexp"
	"sort"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9 ]`)

// weaponNameKey makes names comparable across spellings:
// "Crossbow, hand" and "Hand Crossbow" give the same key.
func weaponNameKey(name string) string {
	n := nonAlnum.ReplaceAllString(strings.ToLower(strings.ReplaceAll(name, "-", " ")), " ")
	words := strings.Fields(n)
	sort.Strings(words)
	return strings.Join(words, " ")
}

// srd521WeaponMastery is the Mastery property of every SRD 5.2.1 weapon.
var srd521WeaponMastery = map[string]string{
	"club":           "Slow",
	"dagger":         "Nick",
	"greatclub":      "Push",
	"handaxe":        "Vex",
	"javelin":        "Slow",
	"hammer light":   "Nick",
	"mace":           "Sap",
	"quarterstaff":   "Topple",
	"sickle":         "Nick",
	"spear":          "Sap",
	"dart":           "Vex",
	"crossbow light": "Slow",
	"shortbow":       "Vex",
	"sling":          "Slow",
	"battleaxe":      "Topple",
	"flail":          "Sap",
	"glaive":         "Graze",
	"greataxe":       "Cleave",
	"greatsword":     "Graze",
	"halberd":        "Cleave",
	"lance":          "Topple",
	"longsword":      "Sap",
	"maul":           "Topple",
	"morningstar":    "Sap",
	"pike":           "Push",
	"rapier":         "Vex",
	"scimitar":       "Nick",
	"shortsword":     "Vex",
	"trident":        "Topple",
	"warhammer":      "Push",
	"pick war":       "Sap",
	"whip":           "Slow",
	"blowgun":        "Vex",
	"crossbow hand":  "Vex",
	"crossbow heavy": "Push",
	"longbow":        "Slow",
	"musket":         "Slow",
	"pistol":         "Vex",
}

// applyWeaponMastery sets Mastery on every known weapon that has one in the SRD.
func applyWeaponMastery() {
	for slug, w := range srdWeapons {
		if m, ok := srd521WeaponMastery[weaponNameKey(w.Name)]; ok {
			w.Mastery = m
			srdWeapons[slug] = w
		}
	}
}

// srd521WeaponRows are the weapon rows that differ from older stored rows, plus
// the two weapons the seed lacks. Applied to the database at startup.
var srd521WeaponRows = []struct {
	Slug, Name, Type, Damage, DamageType string
	Weight                               float64
	Properties                           string
}{
	{"lance", "Lance", "martial melee", "1d10", "piercing", 6, "Heavy, Reach, Two-Handed (unless mounted)"},
	{"trident", "Trident", "martial melee", "1d8", "piercing", 4, "Thrown (20/60), Versatile (1d10)"},
	{"warhammer", "Warhammer", "martial melee", "1d8", "bludgeoning", 5, "Versatile (1d10)"},
	{"war-pick", "War pick", "martial melee", "1d8", "piercing", 2, "Versatile (1d10)"},
	{"musket", "Musket", "martial ranged", "1d12", "piercing", 10, "Ammunition (40/120), Loading, Two-Handed"},
	{"pistol", "Pistol", "martial ranged", "1d10", "piercing", 3, "Ammunition (30/90), Loading"},
}

// applySRD521Weapons upserts the SRD 5.2.1 weapon rows. Safe to run every start.
func applySRD521Weapons() {
	for _, w := range srd521WeaponRows {
		_, err := db.Exec(`INSERT INTO weapons (slug, name, type, damage, damage_type, weight, properties, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'srd')
			ON CONFLICT (slug) DO UPDATE SET
				name = EXCLUDED.name, type = EXCLUDED.type, damage = EXCLUDED.damage,
				damage_type = EXCLUDED.damage_type, weight = EXCLUDED.weight,
				properties = EXCLUDED.properties`,
			w.Slug, w.Name, w.Type, w.Damage, w.DamageType, w.Weight, w.Properties)
		if err != nil {
			log.Printf("SRD 5.2.1 weapon %s: %v", w.Slug, err)
		}
	}
}

// syncWeaponsToSRDTable makes the combat data of every weapon agree with the printed SRD 5.2.1
// table (data/srd521_weapons.json): damage, damage type, weight, Mastery, category and type.
// The Properties lists are left as they are (the attack code parses them).
func syncWeaponsToSRDTable() {
	changed := 0
	for _, row := range srdWeaponTable() {
		for key, w := range srdWeapons {
			if weaponNameKey(w.Name) != weaponNameKey(row.Name) {
				continue
			}
			before := w
			w.Damage = row.Damage
			w.DamageType = strings.ToLower(row.DamageType)
			if lb := weightPounds(row.Weight); lb >= 0 {
				w.Weight = lb
			}
			w.Mastery = row.Mastery
			w.Category = "simple"
			if strings.HasPrefix(row.Category, "Martial") {
				w.Category = "martial"
			}
			w.Type = "melee"
			if strings.Contains(row.Category, "Ranged") {
				w.Type = "ranged"
			}
			if w.Cost == "" {
				w.Cost = strings.ToLower(row.Cost)
			}
			if w.Damage != before.Damage || w.DamageType != before.DamageType || w.Weight != before.Weight || w.Mastery != before.Mastery || w.Category != before.Category || w.Type != before.Type {
				changed++
			}
			srdWeapons[key] = w
		}
	}
	if changed > 0 {
		log.Printf("Weapons: %d combat entries corrected to the SRD 5.2.1 table", changed)
	}
}
