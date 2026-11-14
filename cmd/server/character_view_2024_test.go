package main

import (
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The Drow's Darkvision trait on the character record says 120 feet, as vision.darkvision does (S4-14).
func TestResolvedSpeciesTraitsDrowDarkvision(t *testing.T) {
	sp, err := game.ResolveSpecies("elf", map[string]string{"lineage": "drow", "spellcasting_ability": "cha", "skill": "insight"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tr := range resolvedSpeciesTraits(sp) {
		if tr.Name == "Darkvision" {
			found = true
			if !strings.Contains(tr.Description, "120 feet") || !strings.Contains(tr.Description, "Drow lineage") {
				t.Errorf("Drow Darkvision trait = %q", tr.Description)
			}
		}
	}
	if !found {
		t.Error("no Darkvision trait")
	}
	if got := speciesChoiceSource(sp); got != "Elf (Drow lineage)" {
		t.Errorf("speciesChoiceSource = %q", got)
	}
	hi, _ := game.ResolveSpecies("elf", map[string]string{"lineage": "high_elf", "spellcasting_ability": "int", "skill": "insight"})
	for _, tr := range resolvedSpeciesTraits(hi) {
		if tr.Name == "Darkvision" && tr.Description != "You have Darkvision with a range of 60 feet." {
			t.Errorf("High Elf Darkvision trait = %q", tr.Description)
		}
	}
}

// species_choices sent before a species: a value no species accepts is refused at once (S4-13); values some species accepts pass.
func TestSpeciesChoicesBeforeSpecies(t *testing.T) {
	bg := game.GetBackground("criminal") // Sleight of Hand and Stealth
	probs := func(sc map[string]interface{}) []string {
		var out []string
		for _, c := range speciesChoicesBeforeSpecies(map[string]interface{}{"species_choices": sc}, "rogue", bg) {
			out = append(out, c["problem"].(string))
		}
		return out
	}
	for _, ok := range []map[string]interface{}{
		{"ancestry": "red"}, {"ancestry": "Stone"}, {"lineage": "High Elf"}, {"lineage": "infernal"}, {"size": "Small"},
		{"spellcasting_ability": "wis"}, {"feat": "magic_initiate"}, {"skill": "insight"},
	} {
		if p := probs(ok); len(p) > 0 {
			t.Errorf("%v refused: %v", ok, p)
		}
	}
	for _, bad := range []map[string]interface{}{
		{"ancestry": "purple"}, {"lineage": "dragon"}, {"size": "huge"}, {"spellcasting_ability": 3.0}, {"feat": "tough"},
		{"skill": "flying"}, {"skill": "sleight_of_hand"},
	} {
		if p := probs(bad); len(p) != 1 {
			t.Errorf("%v: want one problem, got %v", bad, p)
		}
	}
}
