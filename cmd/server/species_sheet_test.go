package main

import "testing"

func TestSpeciesTraits2024(t *testing.T) {
	names := map[string]bool{}
	for _, e := range speciesTraits("dwarf", "") {
		names[e.Name] = true
		if e.Description == "" {
			t.Errorf("%s has no text", e.Name)
		}
	}
	if !names["Darkvision"] || names["Dwarven Combat Training"] {
		t.Errorf("dwarf traits should be the 2024 ones: %v", names)
	}
	for _, e := range speciesTraits("dragonborn", "red") {
		if e.Name == "Breath Weapon" {
			t.Error("Breath Weapon is an Action on the sheet, not a listed trait")
		}
	}
	if speciesTraits("half-orc", "") != nil {
		t.Error("species that are not in the SRD 5.2.1 are gone")
	}
}
