package main

// The species traits on the character sheet, word for word from the SRD 5.2.1 (data/srd521_origins.json). A paragraph that follows a
// trait belongs to it; a table is written out as "Dragon: Damage Type" lines. Breath Weapon is shown as an Action with the character's
// own numbers, so it is left out here.

import (
	"fmt"
	"strings"
)

func speciesTraits(species, draconicAncestry string) []sbEntry {
	entry, ok := originByName(srdOrigins().Species.Items, strings.TrimSpace(species))
	if !ok {
		return nil
	}
	_ = draconicAncestry
	out := []sbEntry{}
	for _, b := range entry.Blocks {
		switch v := b.(type) {
		case map[string]interface{}:
			if name, ok := v["trait"].(string); ok {
				out = append(out, sbEntry{Name: name, Description: fmt.Sprint(v["text"]), Source: "Species"})
				continue
			}
			if tbl, ok := v["table"].(map[string]interface{}); ok && len(out) > 0 {
				rows, _ := tbl["rows"].([]interface{})
				var lines []string
				for i, r := range rows {
					cells, _ := r.([]interface{})
					if i == 0 || len(cells) < 2 {
						continue
					}
					for c := 0; c+1 < len(cells); c += 2 {
						lines = append(lines, fmt.Sprintf("%v: %v", cells[c], cells[c+1]))
					}
				}
				out[len(out)-1].Description += "\n" + strings.Join(lines, "; ")
			}
		case string:
			if len(out) > 0 { // a paragraph that continues the trait above it
				out[len(out)-1].Description += "\n" + v
			}
		}
	}
	var kept []sbEntry
	for _, e := range out {
		if !strings.EqualFold(e.Name, "Breath Weapon") {
			kept = append(kept, e)
		}
	}
	return kept
}
