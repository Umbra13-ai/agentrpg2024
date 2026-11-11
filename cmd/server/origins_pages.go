package main

// Species and Backgrounds exactly as the SRD 5.2.1 prints them (pages 83-86). data/srd521_origins.json holds the
// label lines (Creature Type, Size, Speed; Ability Scores, Feat, Skill Proficiencies, Tool Proficiency, Equipment)
// and the text with its tables. The game's own species and background tables carry the mechanics.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

//go:embed data/srd521_origins.json
var srd521OriginsJSON []byte


type originField struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type originEntry struct {
	Name   string        `json:"name"`
	Fields []originField `json:"fields"`
	Blocks []interface{} `json:"blocks"`
}

type originsData struct {
	Backgrounds struct {
		Intro []interface{} `json:"intro"`
		Items []originEntry `json:"items"`
	} `json:"backgrounds"`
	Species struct {
		Intro []interface{} `json:"intro"`
		Items []originEntry `json:"items"`
	} `json:"species"`
}

func srdOrigins() originsData {
	var d originsData
	if err := json.Unmarshal(srd521OriginsJSON, &d); err != nil {
		fmt.Println("origins:", err)
	}
	return d
}

// originByName finds the SRD entry of a species or background.
func originByName(items []originEntry, name string) (originEntry, bool) {
	for _, it := range items {
		if strings.EqualFold(it.Name, name) {
			return it, true
		}
	}
	return originEntry{}, false
}

// renderOriginBlocks prints paragraphs, headings, run-in trait names and tables in order.
func renderOriginBlocks(blocks []interface{}) string {
	var b strings.Builder
	for _, x := range blocks {
		switch v := x.(type) {
		case string:
			b.WriteString("<p>" + e(v) + "</p>")
		case map[string]interface{}:
			switch {
			case v["table"] != nil:
				tbl, _ := v["table"].(map[string]interface{})
				b.WriteString(renderTable(fmt.Sprint(tbl["title"]), tbl["rows"]))
			case v["heading"] != nil:
				tag := "h3"
				if lvl, _ := v["level"].(float64); lvl >= 3 {
					tag = "h4"
				}
				b.WriteString("<" + tag + ">" + e(fmt.Sprint(v["heading"])) + "</" + tag + ">")
			case v["trait"] != nil:
				b.WriteString("<p><strong><em>" + e(fmt.Sprint(v["trait"])) + ".</em></strong> " + e(fmt.Sprint(v["text"])) + "</p>")
			case v["entry"] != nil:
				b.WriteString("<p><strong>" + e(fmt.Sprint(v["entry"])) + ".</strong> " + e(fmt.Sprint(v["text"])) + "</p>")
			}
		}
	}
	return b.String()
}

func originFieldsHTML(fields []originField) string {
	var b strings.Builder
	for _, f := range fields {
		b.WriteString("<p><strong>" + e(f.Label) + ":</strong> " + e(f.Text) + "</p>")
	}
	return b.String()
}

// originsPage builds /universe/species (species) and /universe/backgrounds, their lists and their pages.
func originsPage(category, rest string) string {
	d := srdOrigins()
	items, title, icon := d.Species.Items, "Species", "🧝"
	if category == "backgrounds" {
		items, title, icon = d.Backgrounds.Items, "Backgrounds", "📜"
	}
	if rest != "" {
		for _, it := range items {
			if itemSlug(it.Name) == rest {
				narrative := ""
				if category == "backgrounds" {
					if bg := game.GetBackground(it.Name); bg != nil && bg.Description != "" {
						narrative = `<p>` + e(bg.Description) + `</p>` // a background narrative from the local content (local_content.go)
					}
				} else if intro := speciesIntro(it.Name); intro != "" {
					narrative = `<p>` + e(intro) + `</p>` // a species introduction from the local content (local_content.go)
				}
				return tableStyle + fmt.Sprintf(`<h1>%s %s</h1>`, icon, e(it.Name)) + originFieldsHTML(it.Fields) + narrative + renderOriginBlocks(it.Blocks) +
					`<p><a href="/universe/` + category + `">← All ` + strings.ToLower(title) + `</a></p>`
			}
		}
		return `<h1>Not found</h1><p><a href="/universe/` + category + `">← All ` + strings.ToLower(title) + `</a></p>`
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<h1>%s %s</h1><div class="category-grid">`, icon, e(title)))
	for _, it := range items {
		var lines []string
		for _, f := range it.Fields {
			if category == "backgrounds" && (f.Label == "Ability Scores" || f.Label == "Feat") || category == "species" && (f.Label == "Size" || f.Label == "Speed") {
				lines = append(lines, "<strong>"+e(f.Label)+":</strong> "+e(f.Text))
			}
		}
		b.WriteString(fmt.Sprintf(`<div class="category-card"><a href="/universe/%s/%s"><h3>%s</h3></a><p class="description">%s</p></div>`,
			category, e(itemSlug(it.Name)), e(it.Name), strings.Join(lines, "<br>")))
	}
	b.WriteString(`</div>`)
	return b.String()
}
