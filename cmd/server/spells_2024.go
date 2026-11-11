package main

// The 339 spells of the SRD 5.2.1 (2024 rules) and the database columns that hold them.
// The spell data was extracted from the SRD PDF by items/extract_spells.py (research
// folder) into data/srd521_spells.json, which is built into the server. Spells of any other ruleset
// that came from the 5e API are replaced at startup. The SRD is licensed CC-BY-4.0.

import (
	_ "embed"
	"encoding/json"
	"log"
	"strings"
)

//go:embed data/srd521_spells.json
var srd521SpellsJSON []byte

type srdSpell2024 struct {
	Slug                   string            `json:"slug"`
	Name                   string            `json:"name"`
	Level                  int               `json:"level"`
	School                 string            `json:"school"`
	Classes                []string          `json:"classes"`
	CastingTime            string            `json:"casting_time"`
	Range                  string            `json:"range"`
	Components             string            `json:"components"` // as printed: "V, S, M (a pinch of sulfur)"
	ComponentsShort        string            `json:"components_short"`
	Material               string            `json:"material"`
	MaterialCost           int               `json:"material_cost"`
	MaterialConsumed       bool              `json:"material_consumed"`
	Duration               string            `json:"duration"`
	Description            string            `json:"description"`
	DescriptionBlocks      json.RawMessage   `json:"description_blocks"` // paragraphs and tables as printed
	Statblock              json.RawMessage   `json:"statblock"`           // the stat block some summoning spells print
	DamageParts            json.RawMessage   `json:"damage_parts"`        // spells that deal damage in more than one part
	HigherLevel            string            `json:"higher_level"`
	Ritual                 bool              `json:"ritual"`
	Concentration          bool              `json:"concentration"`
	SavingThrow            string            `json:"saving_throw"`
	AoEShape               string            `json:"aoe_shape"`
	AoESize                int               `json:"aoe_size"`
	DamageDice             string            `json:"damage_dice"`
	DamageType             string            `json:"damage_type"`
	Healing                string            `json:"healing"`
	DamageAtSlotLevel      map[string]string `json:"damage_at_slot_level"`
	DamageAtCharacterLevel map[string]string `json:"damage_at_character_level"`
	HealAtSlotLevel        map[string]string `json:"heal_at_slot_level"`
}

// ensureSpellColumns makes the spells table able to hold every field of a 2024 spell.
func ensureSpellColumns() {
	stmts := []string{
		// 2024 text is longer than the old limits ("Reaction, which you take when ...")
		`ALTER TABLE spells ALTER COLUMN casting_time TYPE TEXT`,
		`ALTER TABLE spells ALTER COLUMN range TYPE TEXT`,
		`ALTER TABLE spells ALTER COLUMN components TYPE TEXT`,
		`ALTER TABLE spells ALTER COLUMN duration TYPE TEXT`,
		`ALTER TABLE spells ALTER COLUMN damage_dice TYPE VARCHAR(40)`,
		`ALTER TABLE spells ALTER COLUMN healing TYPE VARCHAR(40)`,
		`ALTER TABLE spells ALTER COLUMN source TYPE VARCHAR(60)`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS material TEXT DEFAULT ''`,
		`ALTER TABLE spells ALTER COLUMN material TYPE TEXT`,
		// new fields
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS classes JSONB DEFAULT '[]'`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS concentration BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS higher_level TEXT DEFAULT ''`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS components_text TEXT DEFAULT ''`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS ruleset VARCHAR(10) DEFAULT '2024'`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS description_blocks JSONB`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS statblock JSONB`,
		`ALTER TABLE spells ADD COLUMN IF NOT EXISTS damage_parts JSONB`,
		`CREATE INDEX IF NOT EXISTS idx_spells_ruleset ON spells(ruleset)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			log.Printf("spell columns: %v (%s)", err, s)
		}
	}
}

// jsonArg passes JSON to a jsonb column as text (a []byte would be sent as bytea); nil becomes NULL.
func jsonArg(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// seedSpells2024 replaces the spells table with the SRD 5.2.1 spells and rebuilds the class spell lists.
func seedSpells2024() {
	ensureSpellColumns()
	var spells []srdSpell2024
	if err := json.Unmarshal(srd521SpellsJSON, &spells); err != nil {
		log.Printf("spells 2024: %v", err)
		return
	}
	applyLocalSpellChanges(spells) // a private server's wording from other books (local_content.go)
	slugs := make([]string, 0, len(spells))
	n := 0
	var firstErr error
	for _, s := range spells {
		slugs = append(slugs, s.Slug)
		nz := func(m map[string]string) []byte {
			if m == nil {
				m = map[string]string{}
			}
			b, _ := json.Marshal(m)
			return b
		}
		classes := make([]string, 0, len(s.Classes))
		for _, c := range s.Classes {
			classes = append(classes, strings.ToLower(c))
		}
		classJSON, _ := json.Marshal(classes)
		_, err := db.Exec(`INSERT INTO spells (slug, name, level, school, casting_time, range, components, components_text, duration, description,
				damage_dice, damage_type, saving_throw, healing, is_ritual, aoe_shape, aoe_size, damage_at_slot_level, heal_at_slot_level,
				damage_at_character_level, material, material_cost, material_consumed, classes, concentration, higher_level, ruleset, source)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,'2024','SRD 5.2.1')
			ON CONFLICT (slug) DO UPDATE SET name=EXCLUDED.name, level=EXCLUDED.level, school=EXCLUDED.school, casting_time=EXCLUDED.casting_time,
				range=EXCLUDED.range, components=EXCLUDED.components, components_text=EXCLUDED.components_text, duration=EXCLUDED.duration,
				description=EXCLUDED.description, damage_dice=EXCLUDED.damage_dice, damage_type=EXCLUDED.damage_type, saving_throw=EXCLUDED.saving_throw,
				healing=EXCLUDED.healing, is_ritual=EXCLUDED.is_ritual, aoe_shape=EXCLUDED.aoe_shape, aoe_size=EXCLUDED.aoe_size,
				damage_at_slot_level=EXCLUDED.damage_at_slot_level, heal_at_slot_level=EXCLUDED.heal_at_slot_level,
				damage_at_character_level=EXCLUDED.damage_at_character_level, material=EXCLUDED.material, material_cost=EXCLUDED.material_cost,
				material_consumed=EXCLUDED.material_consumed, classes=EXCLUDED.classes, concentration=EXCLUDED.concentration,
				higher_level=EXCLUDED.higher_level, ruleset='2024', source='SRD 5.2.1'`,
			s.Slug, s.Name, s.Level, strings.ToLower(s.School), s.CastingTime, s.Range, s.ComponentsShort, s.Components, s.Duration, s.Description,
			s.DamageDice, s.DamageType, s.SavingThrow, s.Healing, s.Ritual, s.AoEShape, s.AoESize, nz(s.DamageAtSlotLevel), nz(s.HealAtSlotLevel),
			nz(s.DamageAtCharacterLevel), s.Material, s.MaterialCost, s.MaterialConsumed, classJSON, s.Concentration, s.HigherLevel)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		blocks, sb := []byte(s.DescriptionBlocks), []byte(s.Statblock)
		if len(blocks) == 0 || string(blocks) == "null" {
			blocks = nil
		}
		if len(sb) == 0 || string(sb) == "null" {
			sb = nil
		}
		parts := []byte(s.DamageParts)
		if len(parts) == 0 || string(parts) == "null" || string(parts) == "[]" {
			parts = nil
		}
		if _, err := db.Exec(`UPDATE spells SET description_blocks = $1, statblock = $2, damage_parts = $3 WHERE slug = $4`, jsonArg(blocks), jsonArg(sb), jsonArg(parts), s.Slug); err != nil && firstErr == nil {
			firstErr = err
		}
		n++
	}
	if firstErr != nil {
		log.Printf("spells 2024: some rows failed, first error: %v", firstErr)
	}
	// spells that are not in the SRD 5.2.1 go away
	if res, err := db.Exec(`DELETE FROM spells WHERE ruleset IS DISTINCT FROM '2024' OR slug <> ALL($1::text[])`, pqStringArray(slugs)); err == nil {
		if gone, _ := res.RowsAffected(); gone > 0 {
			log.Printf("Removed %d spells that are not in the SRD 5.2.1", gone)
		}
	}
	// class spell lists
	db.Exec(`DELETE FROM class_spells`)
	lists := 0
	for _, s := range spells {
		for _, c := range s.Classes {
			if _, err := db.Exec(`INSERT INTO class_spells (class_slug, spell_slug) VALUES ($1, $2) ON CONFLICT DO NOTHING`, strings.ToLower(c), s.Slug); err == nil {
				lists++
			}
		}
	}
	log.Printf("Spells: %d (SRD 5.2.1), %d class spell list entries", n, lists)
}

// pqStringArray formats a Go slice as a Postgres text array literal.
func pqStringArray(items []string) string {
	var b strings.Builder
	b.WriteString("{")
	for i, s := range items {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`)
	}
	b.WriteString("}")
	return b.String()
}
