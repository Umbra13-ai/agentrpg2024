package main

// The twelve classes exactly as the SRD 5.2.1 prints them (pages 28-82): Core Traits, "Becoming a ...", every class
// feature, the Features table, the option lists, the subclass and its features. The spell lists are not stored
// twice: they are built from the spells' own class lists (the spells' headers print them), in the SRD's layout.
// The short idea on the class list is original text. The game's class tables (game/class_features_2024.go, the class
// tables in game/) carry the mechanics.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed data/srd521_classes.json
var srd521ClassesJSON []byte

type classSection struct {
	Heading   string        `json:"heading"`
	Level     int           `json:"level"`
	Blocks    []interface{} `json:"blocks"`
	SpellList bool          `json:"spell_list,omitempty"`
}

type classText struct {
	Name     string         `json:"name"`
	Sections []classSection `json:"sections"`
}

func srdClassTexts() []classText {
	var c []classText
	if err := json.Unmarshal(srd521ClassesJSON, &c); err != nil {
		fmt.Println("classes:", err)
	}
	return c
}

// classBrief is a one-sentence idea of each class for the list (original text, not from the SRD).
var classBrief = map[string]string{
	"Barbarian": "A fierce warrior who channels primal rage into raw strength and toughness.",
	"Bard":      "A magical performer who weaves music, words, and spells into inspiration and mischief.",
	"Cleric":    "A priest who wields divine magic in service of a god or a cause, healing allies and smiting foes.",
	"Druid":     "A guardian of the natural world who casts primal spells and takes the shape of beasts.",
	"Fighter":   "A master of weapons and armor, trained in every fighting style and battlefield tactic.",
	"Monk":      "A disciplined martial artist who harnesses inner power for swift strikes and uncanny movement.",
	"Paladin":   "A sworn holy warrior whose oath fuels divine power, healing, and righteous strikes.",
	"Ranger":    "A wilderness hunter and tracker who blends martial skill with primal magic.",
	"Rogue":     "A cunning expert in stealth and skills who strikes precisely and exploits every opening.",
	"Sorcerer":  "A spellcaster born with innate magic that they shape and bend to their will.",
	"Warlock":   "A seeker who bargains with an otherworldly patron for eldritch power.",
	"Wizard":    "A scholar of the arcane who studies, records, and masters a vast repertoire of spells.",
}

func classTextByName(name string) (classText, bool) {
	for _, c := range srdClassTexts() {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return classText{}, false
}

func coreTraitText(c classText, label string) string {
	for _, s := range c.Sections {
		for _, b := range s.Blocks {
			if m, ok := b.(map[string]interface{}); ok {
				if kv, ok := m["keyvalue"].([]interface{}); ok {
					for _, x := range kv {
						row := x.(map[string]interface{})
						if row["label"] == label {
							return fmt.Sprint(row["text"])
						}
					}
				}
			}
		}
	}
	return ""
}

func renderClassBlocks(blocks []interface{}) string {
	var b strings.Builder
	inList := false
	closeList := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
	}
	for _, x := range blocks {
		m, isMap := x.(map[string]interface{})
		if isMap && m["bullet"] != nil {
			if !inList {
				b.WriteString("<ul>")
				inList = true
			}
			b.WriteString("<li>" + e(fmt.Sprint(m["bullet"])) + "</li>")
			continue
		}
		closeList()
		switch {
		case !isMap:
			if s, ok := x.(string); ok {
				b.WriteString("<p>" + e(s) + "</p>")
			}
		case m["trait"] != nil:
			b.WriteString("<p><strong><em>" + e(fmt.Sprint(m["trait"])) + ".</em></strong> " + e(fmt.Sprint(m["text"])) + "</p>")
		case m["table"] != nil:
			tbl, _ := m["table"].(map[string]interface{})
			b.WriteString(renderClassTable(tbl))
		case m["keyvalue"] != nil:
			b.WriteString(`<table class="ref">`)
			for _, r := range m["keyvalue"].([]interface{}) {
				row := r.(map[string]interface{})
				b.WriteString("<tr><th>" + e(fmt.Sprint(row["label"])) + "</th><td>" + e(fmt.Sprint(row["text"])) + "</td></tr>")
			}
			b.WriteString("</table>")
		case m["sidebar"] != nil:
			b.WriteString(`<div class="note">`)
			for _, p := range m["sidebar"].([]interface{}) {
				b.WriteString("<p>" + e(fmt.Sprint(p)) + "</p>")
			}
			b.WriteString("</div>")
		}
	}
	closeList()
	return b.String()
}

// renderClassTable prints a table; a banner such as "Spell Slots per Spell Level" spans the columns it names.
func renderClassTable(tbl map[string]interface{}) string {
	groups, _ := tbl["header_groups"].([]interface{})
	if len(groups) == 0 {
		return renderTable(fmt.Sprint(tbl["title"]), tbl["rows"])
	}
	rows, _ := tbl["rows"].([]interface{})
	var b strings.Builder
	b.WriteString(`<table class="ref">`)
	if t := fmt.Sprint(tbl["title"]); t != "" && t != "<nil>" {
		b.WriteString("<caption>" + e(t) + "</caption>")
	}
	if len(rows) > 0 {
		hdr, _ := rows[0].([]interface{})
		g := groups[0].(map[string]interface{})
		from, to := int(g["from"].(float64)), int(g["to"].(float64))
		b.WriteString("<tr>")
		for i := 0; i < from && i < len(hdr); i++ {
			b.WriteString(`<th rowspan="2">` + e(fmt.Sprint(hdr[i])) + "</th>")
		}
		b.WriteString(fmt.Sprintf(`<th colspan="%d" style="text-align:center">%s</th>`, to-from+1, e(fmt.Sprint(g["text"]))))
		for i := to + 1; i < len(hdr); i++ {
			b.WriteString(`<th rowspan="2">` + e(fmt.Sprint(hdr[i])) + "</th>")
		}
		b.WriteString("</tr><tr>")
		for i := from; i <= to && i < len(hdr); i++ {
			b.WriteString("<th>" + e(fmt.Sprint(hdr[i])) + "</th>")
		}
		b.WriteString("</tr>")
		for _, r := range rows[1:] {
			cells, _ := r.([]interface{})
			b.WriteString("<tr>")
			for _, c := range cells {
				b.WriteString("<td>" + e(fmt.Sprint(c)) + "</td>")
			}
			b.WriteString("</tr>")
		}
	}
	return b.String() + "</table>"
}

// classSpellListHTML builds the class spell list from the spells' own class lists, laid out as the SRD lays it out.
func classSpellListHTML(className string) string {
	rows, err := db.Query(`SELECT slug, name, level, school, COALESCE(concentration,false), COALESCE(is_ritual,false), COALESCE(material_cost,0)
		FROM spells WHERE ruleset = '2024' AND classes ? $1 ORDER BY level, name`, strings.ToLower(className))
	if err != nil {
		return ""
	}
	defer rows.Close()
	byLevel := map[int][][]string{}
	for rows.Next() {
		var slug, name, school string
		var level, cost int
		var conc, ritual bool
		if rows.Scan(&slug, &name, &level, &school, &conc, &ritual, &cost) != nil {
			continue
		}
		var special []string
		if conc {
			special = append(special, "C")
		}
		if ritual {
			special = append(special, "R")
		}
		if cost > 0 {
			special = append(special, "M")
		}
		sp := "—"
		if len(special) > 0 {
			sp = strings.Join(special, ", ")
		}
		byLevel[level] = append(byLevel[level], []string{`<a href="/universe/spells/` + slug + `">` + e(name) + `</a>`, e(titleWords(school)), e(sp)})
	}
	var b strings.Builder
	for lvl := 0; lvl <= 9; lvl++ {
		list := byLevel[lvl]
		if len(list) == 0 {
			continue
		}
		title := fmt.Sprintf("Level %d %s Spells", lvl, className)
		if lvl == 0 {
			title = fmt.Sprintf("Cantrips (Level 0 %s Spells)", className)
		}
		b.WriteString(`<table class="ref"><caption>` + e(title) + `</caption><tr><th>Spell</th><th>School</th><th>Special</th></tr>`)
		for _, r := range list {
			b.WriteString("<tr><td>" + r[0] + "</td><td>" + r[1] + "</td><td>" + r[2] + "</td></tr>")
		}
		b.WriteString("</table>")
	}
	return b.String()
}

// classesPage builds /universe/classes and /universe/classes/{slug}.
func classesPage(rest string) string {
	if rest != "" {
		c, ok := classTextByName(rest)
		if !ok {
			return `<h1>Not found</h1><p><a href="/universe/classes">← All classes</a></p>`
		}
		var b strings.Builder
		b.WriteString(tableStyle + `<h1>⚔️ ` + e(c.Name) + `</h1><p>` + e(classBrief[c.Name]) + `</p>`)
		for _, s := range c.Sections {
			switch s.Level {
			case 2:
				b.WriteString("<h2>" + e(s.Heading) + "</h2>")
			case 3:
				b.WriteString("<h3>" + e(s.Heading) + "</h3>")
			case 4:
				b.WriteString("<h4>" + e(s.Heading) + "</h4>")
			}
			b.WriteString(renderClassBlocks(s.Blocks))
			if s.SpellList {
				b.WriteString(classSpellListHTML(c.Name))
			}
		}
		b.WriteString(`<p><a href="/universe/classes">← All classes</a></p>`)
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`<h1>⚔️ Classes</h1><div class="category-grid">`)
	for _, c := range srdClassTexts() {
		b.WriteString(fmt.Sprintf(`<div class="category-card"><a href="/universe/classes/%s"><h3>%s</h3></a><p class="description">%s</p><p><strong>Primary Ability:</strong> %s<br><strong>Hit Point Die:</strong> %s</p></div>`,
			e(strings.ToLower(c.Name)), e(c.Name), e(classBrief[c.Name]), e(coreTraitText(c, "Primary Ability")), e(coreTraitText(c, "Hit Point Die"))))
	}
	b.WriteString(`</div>`)
	return b.String()
}
