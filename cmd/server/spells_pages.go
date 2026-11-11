package main

// Universe API and pages for spells. They show everything the database holds for a
// spell: classes, concentration, ritual, full components with the material, the
// higher-level text, damage, saves, area and the per-slot scaling tables.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

type spellRow struct {
	ID                                                                               int
	Slug, Name, School, CastingTime, Range, Components, ComponentsText, Duration     string
	Description, HigherLevel, SavingThrow, AoEShape, DamageDice, DamageType, Healing string
	Material, Ruleset, Source                                                        string
	Level, AoESize, MaterialCost                                                     int
	Ritual, Concentration, MaterialConsumed                                          bool
	Classes                                                                          []string
	DamageAtSlot, HealAtSlot, DamageAtChar                                           map[string]string
	Blocks, Statblock, Parts                                                         json.RawMessage
}

const spellSelect = `SELECT id, slug, name, level, school, COALESCE(casting_time,''), COALESCE(range,''), COALESCE(components,''), COALESCE(components_text,''),
	COALESCE(duration,''), COALESCE(description,''), COALESCE(higher_level,''), COALESCE(saving_throw,''), COALESCE(aoe_shape,''), COALESCE(aoe_size,0),
	COALESCE(damage_dice,''), COALESCE(damage_type,''), COALESCE(healing,''), COALESCE(material,''), COALESCE(material_cost,0), COALESCE(material_consumed,false),
	COALESCE(is_ritual,false), COALESCE(concentration,false), COALESCE(classes,'[]'), COALESCE(damage_at_slot_level,'{}'), COALESCE(heal_at_slot_level,'{}'),
	COALESCE(damage_at_character_level,'{}'), COALESCE(ruleset,''), COALESCE(source,''), COALESCE(description_blocks,'null'::jsonb), COALESCE(statblock,'null'::jsonb), COALESCE(damage_parts,'null'::jsonb) FROM spells`

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanSpell(r rowScanner) (spellRow, error) {
	var s spellRow
	var classes, dslot, hslot, dchar []byte
	err := r.Scan(&s.ID, &s.Slug, &s.Name, &s.Level, &s.School, &s.CastingTime, &s.Range, &s.Components, &s.ComponentsText, &s.Duration, &s.Description,
		&s.HigherLevel, &s.SavingThrow, &s.AoEShape, &s.AoESize, &s.DamageDice, &s.DamageType, &s.Healing, &s.Material, &s.MaterialCost, &s.MaterialConsumed,
		&s.Ritual, &s.Concentration, &classes, &dslot, &hslot, &dchar, &s.Ruleset, &s.Source, &s.Blocks, &s.Statblock, &s.Parts)
	json.Unmarshal(classes, &s.Classes)
	json.Unmarshal(dslot, &s.DamageAtSlot)
	json.Unmarshal(hslot, &s.HealAtSlot)
	json.Unmarshal(dchar, &s.DamageAtChar)
	return s, err
}

func (s spellRow) toJSON() map[string]interface{} {
	m := map[string]interface{}{"slug": s.Slug, "name": s.Name, "level": s.Level, "school": s.School, "classes": s.Classes,
		"casting_time": s.CastingTime, "range": s.Range, "components": s.Components, "components_text": s.ComponentsText, "duration": s.Duration,
		"concentration": s.Concentration, "ritual": s.Ritual, "description": s.Description, "ruleset": s.Ruleset, "source": s.Source}
	set := func(k string, v interface{}, ok bool) {
		if ok {
			m[k] = v
		}
	}
	set("higher_level", s.HigherLevel, s.HigherLevel != "")
	set("description_blocks", s.Blocks, len(s.Blocks) > 0 && string(s.Blocks) != "null")
	set("statblock", s.Statblock, len(s.Statblock) > 0 && string(s.Statblock) != "null")
	set("damage_parts", s.Parts, len(s.Parts) > 0 && string(s.Parts) != "null")
	set("material", s.Material, s.Material != "")
	set("material_cost_gp", s.MaterialCost, s.MaterialCost > 0)
	set("material_consumed", s.MaterialConsumed, s.MaterialConsumed)
	set("saving_throw", s.SavingThrow, s.SavingThrow != "")
	set("aoe_shape", s.AoEShape, s.AoEShape != "")
	set("aoe_size", s.AoESize, s.AoESize > 0)
	set("damage_dice", s.DamageDice, s.DamageDice != "")
	set("damage_type", s.DamageType, s.DamageType != "")
	set("healing", s.Healing, s.Healing != "")
	set("damage_at_slot_level", s.DamageAtSlot, len(s.DamageAtSlot) > 0)
	set("heal_at_slot_level", s.HealAtSlot, len(s.HealAtSlot) > 0)
	set("damage_at_character_level", s.DamageAtChar, len(s.DamageAtChar) > 0)
	return m
}

func handleUniverseSpell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	key := strings.TrimPrefix(r.URL.Path, "/api/universe/spells/")
	s, err := scanSpell(db.QueryRow(spellSelect+` WHERE slug = $1 OR id::text = $1`, key))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "spell_not_found"})
		return
	}
	json.NewEncoder(w).Encode(s.toJSON())
}

func scalingLine(title string, table map[string]string) string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, e(k+": "+table[k]))
	}
	return `<p class="muted"><strong>` + title + `:</strong> ` + strings.Join(parts, ", ") + `</p>`
}

// universeSpellPage builds /universe/spells and /universe/spells/{id}.
func universeSpellPage(rest string) string {
	if rest != "" {
		s, err := scanSpell(db.QueryRow(spellSelect+` WHERE id::text = $1 OR slug = $1`, rest))
		if err != nil {
			return `<h1>Spell not found</h1><p><a href="/universe/spells">← All spells</a></p>`
		}
		levelText := "Cantrip"
		if s.Level > 0 {
			levelText = fmt.Sprintf("Level %d", s.Level)
		}
		var flags []string
		if s.Concentration {
			flags = append(flags, "Concentration")
		}
		if s.Ritual {
			flags = append(flags, "Ritual")
		}
		extra := ""
		if len(flags) > 0 {
			extra = " • " + strings.Join(flags, ", ")
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf(`<h1>✨ %s</h1><p class="muted">%s %s (%s)%s</p>`, e(s.Name), e(levelText), e(titleWords(s.School)), e(titleWords(strings.Join(s.Classes, ", "))), e(extra)))
		comp := s.ComponentsText
		if comp == "" {
			comp = s.Components
		}
		b.WriteString(fmt.Sprintf(`<div class="note"><strong>Casting Time:</strong> %s<br><strong>Range:</strong> %s<br><strong>Components:</strong> %s<br><strong>Duration:</strong> %s</div>`,
			e(s.CastingTime), e(s.Range), e(comp), e(s.Duration)))
		b.WriteString(tableStyle)
		b.WriteString(spellBodyHTML(s))
		if s.HigherLevel != "" {
			label := "Using a Higher-Level Spell Slot."
			if s.Level == 0 {
				label = "Cantrip Upgrade."
			}
			b.WriteString(fmt.Sprintf(`<p><strong><em>%s</em></strong> %s</p>`, label, e(s.HigherLevel)))
		}
		if len(s.DamageAtSlot) > 0 {
			b.WriteString(scalingLine("Damage by slot level", s.DamageAtSlot))
		}
		if len(s.HealAtSlot) > 0 {
			b.WriteString(scalingLine("Healing by slot level", s.HealAtSlot))
		}
		if len(s.DamageAtChar) > 0 {
			b.WriteString(scalingLine("Damage by character level", s.DamageAtChar))
		}
		b.WriteString(damagePartsHTML(s.Parts))
		b.WriteString(statblockHTML(s.Statblock))
		b.WriteString(`<p><a href="/universe/spells">← All spells</a></p>`)
		return b.String()
	}
	var items []browseItem
	levelNames := []string{"Cantrips", "Level 1", "Level 2", "Level 3", "Level 4", "Level 5", "Level 6", "Level 7", "Level 8", "Level 9"}
	if rows, err := db.Query(`SELECT id, name, level, school, COALESCE(classes,'[]'), COALESCE(concentration,false), COALESCE(is_ritual,false), COALESCE(casting_time,'') FROM spells ORDER BY level, name`); err == nil {
		for rows.Next() {
			var id, level int
			var name, school, castTime string
			var classesRaw []byte
			var conc, ritual bool
			if rows.Scan(&id, &name, &level, &school, &classesRaw, &conc, &ritual, &castTime) != nil {
				continue
			}
			var classes []string
			json.Unmarshal(classesRaw, &classes)
			lv := "Cantrip"
			if level > 0 {
				lv = fmt.Sprintf("Lvl %d", level)
			}
			flags := ""
			if conc {
				flags += " • Concentration"
			}
			if ritual {
				flags += " • Ritual"
			}
			var classTitles []string
			for _, c := range classes {
				classTitles = append(classTitles, titleWords(c))
			}
			yes := map[bool]string{true: "Yes", false: "No"}
			if level < 0 || level > 9 {
				level = 0
			}
			items = append(items, browseItem{Href: fmt.Sprintf("/universe/spells/%d", id), Name: name, Group: levelNames[level],
				Meta: fmt.Sprintf("%s %s • %s%s", lv, titleWords(school), strings.Join(classTitles, ", "), flags),
				Facets: map[string][]string{"school": {titleWords(school)}, "class": classTitles, "time": {spellCastingTimeKind(castTime)},
					"concentration": {yes[conc]}, "ritual": {yes[ritual]}, "level": {levelNames[level]}}})
		}
		rows.Close()
	}
	return browseList("✨", "Spells", items, []browseFacet{
		{Key: "level", Label: "Level", Order: levelNames},
		{Key: "class", Label: "Class"},
		{Key: "school", Label: "School"},
		{Key: "time", Label: "Casting time", Order: []string{"Action", "Bonus Action", "Reaction", "1 Minute or longer"}},
		{Key: "concentration", Label: "Concentration"},
		{Key: "ritual", Label: "Ritual"},
	}, levelNames)
}

// spellBodyHTML prints the description as the SRD does: paragraphs and tables in order.
func spellBodyHTML(s spellRow) string {
	var blocks []interface{}
	if len(s.Blocks) > 0 && json.Unmarshal(s.Blocks, &blocks) == nil && len(blocks) > 0 {
		return renderEntryText(blocks)
	}
	return paragraphs(s.Description)
}

// statblockHTML prints the stat block of a summoning spell (Animate Objects, Find Steed, Giant Insect, Summon Dragon).
func statblockHTML(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var sb struct {
		Name      string                         `json:"name"`
		Type      string                         `json:"type"`
		Stats     []struct{ Label, Text string } `json:"stats"`
		Abilities []struct {
			Ability string `json:"ability"`
			Score   int    `json:"score"`
			Mod     string `json:"mod"`
			Save    string `json:"save"`
		} `json:"abilities"`
		Sections []struct {
			Title   string                        `json:"title"`
			Entries []struct{ Name, Text string } `json:"entries"`
		} `json:"sections"`
	}
	if json.Unmarshal(raw, &sb) != nil || sb.Name == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="note"><h2>` + e(sb.Name) + `</h2><p><em>` + e(sb.Type) + `</em></p>`)
	for _, st := range sb.Stats {
		if st.Label == "AC" || st.Label == "HP" || st.Label == "Speed" {
			b.WriteString(`<p><strong>` + e(st.Label) + `</strong> ` + e(st.Text) + `</p>`)
		}
	}
	if len(sb.Abilities) > 0 {
		b.WriteString(`<table class="ref"><tr><th></th><th>Score</th><th>Mod</th><th>Save</th></tr>`)
		for _, a := range sb.Abilities {
			b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>`, e(a.Ability), a.Score, e(a.Mod), e(a.Save)))
		}
		b.WriteString(`</table>`)
	}
	for _, st := range sb.Stats {
		if st.Label != "AC" && st.Label != "HP" && st.Label != "Speed" {
			b.WriteString(`<p><strong>` + e(st.Label) + `</strong> ` + e(st.Text) + `</p>`)
		}
	}
	for _, sec := range sb.Sections {
		b.WriteString(`<h3>` + e(sec.Title) + `</h3>`)
		for _, en := range sec.Entries {
			b.WriteString(`<p><strong><em>` + e(en.Name) + `.</em></strong> ` + e(en.Text) + `</p>`)
		}
	}
	return b.String() + `</div>`
}

// damagePartsHTML lists the parts of a spell that deals damage in more than one part, with the dice at each slot level.
func damagePartsHTML(raw json.RawMessage) string {
	var parts []damagePart
	if len(raw) == 0 || json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<table class="ref"><caption>Damage parts</caption><tr><th>Part</th><th>Damage</th><th>When</th><th>Save</th><th>By slot level</th></tr>`)
	for _, p := range parts {
		when := strings.ReplaceAll(p.Timing, "_", " ")
		save := "—"
		if p.Save != "" {
			save = abilityWord[p.Save]
			if p.OnSave == "half" {
				save += ", half on a success"
			} else {
				save += ", none on a success"
			}
		}
		var slots []string
		for lvl := 1; lvl <= 9; lvl++ {
			if d, ok := p.AtSlotLevel[fmt.Sprint(lvl)]; ok {
				slots = append(slots, fmt.Sprintf("%d: %s", lvl, d))
			}
		}
		name := p.Name
		if p.Note != "" {
			name += " (" + p.Note + ")"
		}
		b.WriteString("<tr><td>" + e(name) + "</td><td>" + e(p.Dice+" "+p.Type) + "</td><td>" + e(when) + "</td><td>" + e(save) + "</td><td>" + e(strings.Join(slots, ", ")) + "</td></tr>")
	}
	return b.String() + "</table>"
}
