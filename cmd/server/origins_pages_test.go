package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The SRD text and the game's own tables must agree.
func TestOriginsMatchGame(t *testing.T) {
	d := srdOrigins()
	if len(d.Species.Items) != 9 || len(d.Backgrounds.Items) != 4 {
		t.Fatalf("want 9 species and 4 backgrounds, got %d and %d", len(d.Species.Items), len(d.Backgrounds.Items))
	}
	field := func(it originEntry, label string) string {
		for _, f := range it.Fields {
			if f.Label == label {
				return f.Text
			}
		}
		return ""
	}
	for _, sp := range game.AllSpecies() {
		it, ok := originByName(d.Species.Items, sp.Name)
		if !ok {
			t.Errorf("no SRD text for species %s", sp.Name)
			continue
		}
		if !strings.Contains(field(it, "Speed"), fmt.Sprint(sp.Speed)) {
			t.Errorf("%s: SRD speed %q, game %d", sp.Name, field(it, "Speed"), sp.Speed)
		}
		for _, size := range sp.Sizes {
			if !strings.Contains(field(it, "Size"), size) {
				t.Errorf("%s: SRD size %q lacks %q", sp.Name, field(it, "Size"), size)
			}
		}
		if len(it.Blocks) == 0 {
			t.Errorf("%s has no text", sp.Name)
		}
	}
	for _, slug := range game.BackgroundSlugs() {
		bg := game.GetBackground(slug)
		it, ok := originByName(d.Backgrounds.Items, bg.Name)
		if !ok {
			t.Errorf("no SRD text for background %s", bg.Name)
			continue
		}
		scores := strings.ToLower(field(it, "Ability Scores"))
		for _, ab := range bg.AbilityScores {
			full := map[string]string{"str": "strength", "dex": "dexterity", "con": "constitution", "int": "intelligence", "wis": "wisdom", "cha": "charisma"}[ab]
			if !strings.Contains(scores, full) {
				t.Errorf("%s: SRD ability scores %q lack %s", bg.Name, scores, full)
			}
		}
		for _, sk := range bg.Skills {
			if !strings.Contains(strings.ToLower(field(it, "Skill Proficiencies")), strings.ReplaceAll(sk, "_", " ")) {
				t.Errorf("%s: SRD skills %q lack %s", bg.Name, field(it, "Skill Proficiencies"), sk)
			}
		}
		if !strings.Contains(strings.ToLower(field(it, "Feat")), strings.ReplaceAll(bg.Feat, "_", " ")) {
			t.Errorf("%s: SRD feat %q, game %s", bg.Name, field(it, "Feat"), bg.Feat)
		}
	}
	// the tables: Draconic Ancestors (6 rows), Elven Lineages, Fiendish Legacies
	count := 0
	for _, s := range d.Species.Items {
		for _, b := range s.Blocks {
			if m, ok := b.(map[string]interface{}); ok && m["table"] != nil {
				count++
			}
		}
	}
	if count != 3 {
		t.Errorf("want 3 species tables, got %d", count)
	}
}
