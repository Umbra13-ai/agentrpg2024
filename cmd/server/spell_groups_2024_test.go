package main

import (
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

func sp(slug string, level int) map[string]interface{} {
	return map[string]interface{}{"slug": slug, "name": slug, "level": level}
}

func groupLabels(g []map[string]interface{}) string {
	var l []string
	for _, x := range g {
		l = append(l, x["label"].(string))
	}
	return strings.Join(l, " | ")
}

func entries(g map[string]interface{}) map[string]string {
	out := map[string]string{}
	for _, s := range g["spells"].([]map[string]interface{}) {
		f, _ := s["from"].(string)
		out[s["slug"].(string)] = f
	}
	return out
}

// Every class prepares from its whole class list except the Wizard (SRD 5.2.1), so the sheet shows one list, Spells Prepared,
// with the always-prepared spells marked by their source; only a Wizard also has a Spellbook. Calla, a level 2 Cleric with Magic
// Initiate, showed her Magic Initiate spells as a "Spellbook".
func TestSpellGroupsForSheet(t *testing.T) {
	calla := map[string]interface{}{
		"prepared_spells": []map[string]interface{}{sp("sacred-flame", 0), sp("cure-wounds", 1)},
		"known_spells":    []map[string]interface{}{sp("sacred-flame", 0), sp("guidance", 0), sp("bless", 1)},
		"origin_spells": []map[string]interface{}{
			{"slug": "guidance", "level": 0, "source": "Magic Initiate (Cleric list)"},
			{"slug": "bless", "level": 1, "source": "Magic Initiate (Cleric list)"}},
	}
	g := spellGroupsForSheet("Cleric", 2, "", calla)
	if groupLabels(g) != "Spells Prepared" {
		t.Fatalf("a Cleric with Magic Initiate: %s", groupLabels(g))
	}
	e := entries(g[0])
	if len(e) != 4 || e["bless"] != "Magic Initiate, Cleric list" || e["guidance"] != "Magic Initiate, Cleric list" || e["cure-wounds"] != "" {
		t.Errorf("Calla's prepared list: %v", e)
	}
	for _, class := range []string{"bard", "cleric", "druid", "paladin", "ranger", "sorcerer", "warlock"} {
		for _, lv := range []int{1, 5, 20} {
			got := groupLabels(spellGroupsForSheet(class, lv, "", map[string]interface{}{
				"prepared_spells": []map[string]interface{}{sp("light", 0)},
				"known_spells":    []map[string]interface{}{sp("light", 0), sp("sleep", 1)}}))
			if got != "Spells Prepared" {
				t.Errorf("a level %d %s: %s (only the prepared list)", lv, class, got)
			}
		}
	}
	wiz := map[string]interface{}{
		"prepared_spells": []map[string]interface{}{sp("fire-bolt", 0), sp("magic-missile", 1)},
		"known_spells":    []map[string]interface{}{sp("fire-bolt", 0), sp("magic-missile", 1), sp("sleep", 1), sp("shield", 1), sp("bless", 1)},
		"origin_spells":   []map[string]interface{}{{"slug": "bless", "level": 1, "source": "Magic Initiate (Cleric list)"}},
	}
	g = spellGroupsForSheet("Wizard", 1, "", wiz)
	if groupLabels(g) != "Spells Prepared | Spellbook" || len(g[1]["spells"].([]map[string]interface{})) != 3 || entries(g[0])["bless"] == "" {
		t.Errorf("a Wizard: %s %v", groupLabels(g), g)
	}
	slug := ""
	for s, sc := range game.AvailableSubclasses {
		if sc.Name == "Life Domain" {
			slug = s
		}
	}
	life := spellGroupsForSheet("Cleric", 3, slug, map[string]interface{}{
		"prepared_spells": []map[string]interface{}{sp("bless", 1)},
		"domain_spells":   []map[string]interface{}{sp("bless", 1), sp("cure-wounds", 1)}})
	if e := entries(life[0]); len(e) != 2 || e["bless"] != "Life Domain" || e["cure-wounds"] != "Life Domain" {
		t.Errorf("a Life Domain Cleric (a domain spell also prepared by choice is listed once, with its source): %v", e)
	}
}
