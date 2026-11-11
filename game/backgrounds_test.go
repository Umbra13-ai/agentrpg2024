package game

import (
	"testing"
)

func TestBackgroundsAre2024(t *testing.T) {
	if BackgroundCount() != 4 {
		t.Fatalf("SRD 5.2.1 has 4 backgrounds, got %d", BackgroundCount())
	}
	want := map[string]struct {
		feat   string
		skills [2]string
	}{
		"acolyte":  {"magic_initiate", [2]string{"insight", "religion"}},
		"criminal": {"alert", [2]string{"sleight of hand", "stealth"}},
		"sage":     {"magic_initiate", [2]string{"arcana", "history"}},
		"soldier":  {"savage_attacker", [2]string{"athletics", "intimidation"}},
	}
	for key, w := range want {
		bg := GetBackground(key)
		if bg == nil {
			t.Fatalf("missing %s", key)
		}
		if bg.Feat != w.feat || bg.Skills[0] != w.skills[0] || bg.Skills[1] != w.skills[1] {
			t.Errorf("%s: wrong feat or skills: %+v", key, bg)
		}
		if len(bg.AbilityScores) != 3 || bg.GoldB != 50 || len(bg.Equipment) == 0 {
			t.Errorf("%s: wrong abilities, gold or equipment", key)
		}
	}
	if GetBackground("Folk Hero") != nil || GetBackground("charlatan") != nil {
		t.Error("backgrounds that are not in the SRD 5.2.1 must be gone")
	}
	if GetBackground("Acolyte") == nil {
		t.Error("lookup should ignore case")
	}
	if GetBackground("soldier").ToolCategory != "gaming" {
		t.Error("Soldier picks a Gaming Set")
	}
}

func TestValidateAbilityIncreases(t *testing.T) {
	bg := GetBackground("soldier") // str, dex, con
	good := []map[string]int{
		{"str": 2, "dex": 1},
		{"con": 2, "str": 1},
		{"str": 1, "dex": 1, "con": 1},
	}
	for _, g := range good {
		if err := ValidateAbilityIncreases(bg, g); err != nil {
			t.Errorf("%v should be allowed: %v", g, err)
		}
	}
	bad := []map[string]int{
		{},
		{"str": 2},
		{"str": 2, "int": 1}, // int is not a Soldier ability
		{"str": 3},
		{"str": 2, "dex": 2},
		{"str": 1, "dex": 1},
	}
	for _, b := range bad {
		if ValidateAbilityIncreases(bg, b) == nil {
			t.Errorf("%v should be refused", b)
		}
	}
}

func TestSpeciesList(t *testing.T) {
	if len(AllSpecies()) != 9 {
		t.Fatalf("SRD 5.2.1 has 9 species, got %d", len(AllSpecies()))
	}
	if GetSpecies("Half-Orc") != nil || RetiredSpeciesHint("half-orc") != "orc" {
		t.Error("Half-Orc is retired and should point at Orc")
	}
}

func TestResolveSpecies(t *testing.T) {
	for _, name := range []string{"dragonborn", "elf", "gnome", "goliath", "human", "tiefling"} {
		if _, err := ResolveSpecies(name, nil); err == nil {
			t.Errorf("%s should need choices", name)
		}
	}
	for _, name := range []string{"dwarf", "halfling", "orc"} {
		if _, err := ResolveSpecies(name, nil); err != nil {
			t.Errorf("%s needs no choices: %v", name, err)
		}
	}

	r, err := ResolveSpecies("Dragonborn", map[string]string{"ancestry": "Gold"})
	if err != nil || len(r.Resistances) != 1 || r.Resistances[0] != "fire" || r.Size != "Medium" {
		t.Errorf("gold dragonborn: %+v %v", r, err)
	}
	r, err = ResolveSpecies("elf", map[string]string{"lineage": "wood elf", "spellcasting_ability": "WIS", "skill": "Perception"})
	if err != nil || r.Speed != 35 || r.Cantrips[0] != "druidcraft" || r.Skills[0] != "perception" || r.LevelSpells[3][0] != "longstrider" {
		t.Errorf("wood elf: %+v %v", r, err)
	}
	r, _ = ResolveSpecies("elf", map[string]string{"lineage": "drow", "spellcasting_ability": "cha", "skill": "insight"})
	if r.Darkvision != 120 {
		t.Errorf("drow darkvision should be 120, got %d", r.Darkvision)
	}
	if _, err = ResolveSpecies("elf", map[string]string{"lineage": "drow", "spellcasting_ability": "str", "skill": "insight"}); err == nil {
		t.Error("strength is not a spellcasting ability for elves")
	}
	r, err = ResolveSpecies("tiefling", map[string]string{"legacy": "infernal", "spellcasting_ability": "cha", "size": "small"})
	if err != nil || r.Size != "Small" || r.Resistances[0] != "fire" || len(r.Cantrips) != 2 {
		t.Errorf("infernal tiefling: %+v %v", r, err)
	}
	r, err = ResolveSpecies("human", map[string]string{"size": "medium", "skill": "stealth", "feat": "skilled"})
	if err != nil || r.Skills[0] != "stealth" || r.Feat != "skilled" {
		t.Errorf("human: %+v %v", r, err)
	}
	if _, err = ResolveSpecies("human", map[string]string{"size": "medium", "skill": "juggling", "feat": "skilled"}); err == nil {
		t.Error("juggling is not a skill")
	}
	r, _ = ResolveSpecies("dwarf", nil)
	if r.HPPerLevel != 1 || r.Darkvision != 120 {
		t.Errorf("dwarf: %+v", r)
	}
	r, _ = ResolveSpecies("goliath", map[string]string{"ancestry": "storm"})
	if r.Speed != 35 {
		t.Errorf("goliath speed should be 35, got %d", r.Speed)
	}
}

func TestValidateLanguages(t *testing.T) {
	got, err := ValidateLanguages([]string{"elvish", "Dwarvish"})
	if err != nil || len(got) != 3 || got[0] != "Common" || got[1] != "Elvish" {
		t.Errorf("good languages: %v %v", got, err)
	}
	for _, bad := range [][]string{nil, {"Elvish"}, {"Elvish", "Elvish"}, {"Elvish", "Klingon"}, {"Elvish", "Abyssal"}, {"Elvish", "Dwarvish", "Orc"}} {
		if _, err := ValidateLanguages(bad); err == nil {
			t.Errorf("%v should be refused", bad)
		}
	}
}

// The SRD gives backgrounds no narrative; a private server's local content can add one (SetBackgroundDescriptions).
func TestBackgroundDescriptions(t *testing.T) {
	for _, k := range []string{"acolyte", "criminal", "sage", "soldier"} {
		if bg := GetBackground(k); bg == nil || bg.Description != "" {
			t.Errorf("%s: the published data has no narrative", k)
		}
	}
	SetBackgroundDescriptions(map[string]string{"Criminal": "A test narrative."})
	defer SetBackgroundDescriptions(map[string]string{"criminal": ""})
	if GetBackground("criminal").Description != "A test narrative." {
		t.Error("SetBackgroundDescriptions")
	}
}
