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

// Only a class whose features keep a spellbook (the Wizard) shows a Spellbook; a feat's spells are named for the feat and a
// subclass's for the subclass; every spell the sheet has is in some list (Calla, a level 2 Cleric with Magic Initiate, showed
// her Magic Initiate spells as a "Spellbook").
func TestSpellGroupsForSheet(t *testing.T) {
	calla := map[string]interface{}{
		"prepared_spells": []map[string]interface{}{sp("sacred-flame", 0), sp("cure-wounds", 1)},
		"known_spells":    []map[string]interface{}{sp("sacred-flame", 0), sp("guidance", 0), sp("bless", 1)},
		"origin_spells": []map[string]interface{}{
			{"slug": "guidance", "level": 0, "source": "Magic Initiate (Cleric list)"},
			{"slug": "bless", "level": 1, "source": "Magic Initiate (Cleric list)"}},
	}
	got := groupLabels(spellGroupsForSheet("Cleric", 2, "", calla))
	if got != "Spells Prepared | Magic Initiate (Cleric list)" {
		t.Errorf("a Cleric with Magic Initiate: %s", got)
	}
	for _, class := range []string{"bard", "cleric", "druid", "paladin", "ranger", "sorcerer", "warlock"} {
		for _, lv := range []int{1, 5, 20} {
			if g := groupLabels(spellGroupsForSheet(class, lv, "", map[string]interface{}{
				"known_spells": []map[string]interface{}{sp("light", 0), sp("sleep", 1)}})); strings.Contains(g, "Spellbook") {
				t.Errorf("a level %d %s shows a Spellbook: %s", lv, class, g)
			}
		}
	}
	wiz := map[string]interface{}{
		"prepared_spells": []map[string]interface{}{sp("fire-bolt", 0), sp("magic-missile", 1)},
		"known_spells":    []map[string]interface{}{sp("fire-bolt", 0), sp("magic-missile", 1), sp("sleep", 1), sp("shield", 1), sp("bless", 1)},
		"origin_spells":   []map[string]interface{}{{"slug": "bless", "level": 1, "source": "Magic Initiate (Cleric list)"}},
	}
	g := spellGroupsForSheet("Wizard", 1, "", wiz)
	if groupLabels(g) != "Spells Prepared | Magic Initiate (Cleric list) | Spellbook" || len(g[2]["spells"].([]map[string]interface{})) != 3 {
		t.Errorf("a Wizard: %s %v", groupLabels(g), g)
	}
	var slug string
	for s, sc := range game.AvailableSubclasses {
		if strings.EqualFold(sc.Name, "Life Domain") {
			slug = s
		}
	}
	life := spellGroupsForSheet("Cleric", 3, slug, map[string]interface{}{"domain_spells": []map[string]interface{}{sp("bless", 1)}})
	if groupLabels(life) != "Always Prepared (Life Domain)" {
		t.Errorf("a Life Domain Cleric's domain spells: %s", groupLabels(life))
	}
	odd := spellGroupsForSheet("Cleric", 1, "", map[string]interface{}{"known_spells": []map[string]interface{}{sp("light", 0)}})
	if groupLabels(odd) != "Spells Known" {
		t.Errorf("a known spell no other list shows: %s", groupLabels(odd))
	}
}
