package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedTestSpells() {
	classSpellListFn = func(string) []string { return nil } // no database in unit tests
	for slug, lvl := range map[string]int{"light": 0, "mage-hand": 0, "guidance": 0, "shield": 1, "bless": 1, "magic-missile": 1} {
		srdSpellsMemory[slug] = SRDSpell{Name: slug, Level: lvl}
	}
}

func baseInput() originInput {
	return originInput{
		Background: "Soldier", Species: "Dwarf", AbilityIncreases: map[string]int{"str": 2, "con": 1},
		BackgroundTool: "dice set", Languages: []string{"Dwarvish", "Giant"},
		HeldSkills: []string{"perception", "survival"},
	}
}

func TestResolveOriginSoldierDwarf(t *testing.T) {
	out, err := resolveOrigin(baseInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.Gold != 14 || out.Increases["str"] != 2 {
		t.Errorf("gold or increases wrong: %+v", out)
	}
	if len(out.FeatSlugs) != 1 || out.FeatSlugs[0] != "savage_attacker" {
		t.Errorf("feats: %v", out.FeatSlugs)
	}
	if !containsStr(out.Skills, "athletics") || !containsStr(out.Skills, "intimidation") {
		t.Errorf("skills: %v", out.Skills)
	}
	if len(out.Tools) != 1 || out.Tools[0] != "gaming set (dice)" || out.KitTool != "gaming set (dice)" || out.Record.BackgroundTool != "gaming set (dice)" {
		t.Errorf("tool: %v / %q", out.Tools, out.KitTool)
	}
	if s := backgroundSummary(&out.Record); !strings.Contains(s, "Tool Proficiency: Gaming Set (Dice).") {
		t.Errorf("summary: %s", s)
	}
	if out.Record.Species.HPPerLevel != 1 {
		t.Error("Dwarven Toughness missing")
	}
	if len(out.Languages) != 3 || out.Languages[0] != "Common" {
		t.Errorf("languages: %v", out.Languages)
	}
}

func TestResolveOriginGoldOption(t *testing.T) {
	in := baseInput()
	in.BackgroundEquipment = "b"
	out, err := resolveOrigin(in)
	if err != nil || out.Gold != 50 || len(out.Kit) != 0 {
		t.Errorf("option b should be 50 GP and no items: %+v %v", out, err)
	}
	in.BackgroundEquipment = "c"
	if _, err := resolveOrigin(in); err == nil || err.Code != "invalid_background_equipment" {
		t.Errorf("option c should be refused: %v", err)
	}
}

func TestResolveOriginRefusals(t *testing.T) {
	cases := map[string]func(*originInput){
		"invalid_background":        func(i *originInput) { i.Background = "Charlatan" },
		"invalid_species":           func(i *originInput) { i.Species = "Half-Orc" },
		"invalid_ability_increases": func(i *originInput) { i.AbilityIncreases = map[string]int{"int": 2, "con": 1} },
		"invalid_languages":         func(i *originInput) { i.Languages = []string{"Elvish"} },
		"background_tool_required":  func(i *originInput) { i.BackgroundTool = "" },
	}
	for code, mutate := range cases {
		in := baseInput()
		mutate(&in)
		if _, err := resolveOrigin(in); err == nil || err.Code != code {
			t.Errorf("want %s, got %v", code, err)
		}
	}
	in := baseInput()
	in.BackgroundTool = "thieves' tools" // not a gaming set
	if _, err := resolveOrigin(in); err == nil || err.Code != "background_tool_required" {
		t.Errorf("a non-gaming tool should be refused: %v", err)
	}
	in = baseInput()
	in.Species = "Elf"
	if _, err := resolveOrigin(in); err == nil || err.Code != "species_choices_required" {
		t.Errorf("elf without choices should be refused: %v", err)
	}
}

// A skill given twice is refused, so the character never ends up a proficiency short (BUGS #44).
func TestResolveOriginDuplicateSkill(t *testing.T) {
	in := baseInput() // Soldier: Athletics and Intimidation
	in.HeldSkills = []string{"athletics", "survival"}
	if _, err := resolveOrigin(in); err == nil || err.Code != "duplicate_skill" {
		t.Errorf("class Athletics + Soldier Athletics: %v", err)
	}
	in = baseInput()
	in.Species = "Elf"
	in.SpeciesChoices = map[string]string{"lineage": "high_elf", "spellcasting_ability": "int", "skill": "perception"} // held from the class
	if _, err := resolveOrigin(in); err == nil || err.Code != "duplicate_skill" {
		t.Errorf("class Perception + Keen Senses Perception: %v", err)
	}
	in.SpeciesChoices["skill"] = "insight"
	if _, err := resolveOrigin(in); err != nil {
		t.Errorf("a different Keen Senses skill should be fine: %v", err)
	}
}

func TestResolveOriginMagicInitiate(t *testing.T) {
	seedTestSpells()
	in := originInput{
		Background: "Sage", Species: "Dwarf", AbilityIncreases: map[string]int{"int": 2, "wis": 1}, Languages: []string{"Elvish", "Gnomish"},
		BackgroundFeat: &featChoiceInput{List: "wizard", Cantrips: []string{"light", "mage-hand"}, Spell: "shield", SpellcastingAbility: "int"},
	}
	out, err := resolveOrigin(in)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(out.KnownSpells, "light") || !containsStr(out.KnownSpells, "shield") {
		t.Errorf("known spells: %v", out.KnownSpells)
	}
	// Sage fixes the list to Wizard
	in.BackgroundFeat.List = "cleric"
	if _, err := resolveOrigin(in); err == nil || err.Code != "wrong_spell_list" {
		t.Errorf("wrong list: %v", err)
	}
	// a level 1 spell cannot be a cantrip pick
	in.BackgroundFeat = &featChoiceInput{List: "wizard", Cantrips: []string{"light", "shield"}, Spell: "magic-missile", SpellcastingAbility: "int"}
	if _, err := resolveOrigin(in); err == nil || err.Code != "wrong_spell_level" {
		t.Errorf("shield as a cantrip: %v", err)
	}
	in.BackgroundFeat = nil
	if _, err := resolveOrigin(in); err == nil || err.Code != "feat_choices_required" {
		t.Errorf("missing feat choices: %v", err)
	}
}

func TestResolveOriginHumanSkilled(t *testing.T) {
	in := originInput{
		Background: "Criminal", Species: "Human", AbilityIncreases: map[string]int{"dex": 1, "con": 1, "int": 1}, Languages: []string{"Elvish", "Orc"},
		SpeciesChoices: map[string]string{"size": "small", "skill": "perception", "feat": "skilled"},
		SpeciesFeat:    &featChoiceInput{Proficiencies: []string{"athletics", "history", "smith's tools"}},
	}
	out, err := resolveOrigin(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Record.Species.Size != "Small" || len(out.FeatSlugs) != 2 {
		t.Errorf("human: %+v", out.Record.Species)
	}
	for _, s := range []string{"athletics", "history", "perception", "stealth", "sleight of hand"} {
		if !containsStr(out.Skills, s) {
			t.Errorf("missing skill %s in %v", s, out.Skills)
		}
	}
	if !containsStr(out.Tools, "thieves' tools") || !containsStr(out.Tools, "smith's tools") {
		t.Errorf("tools: %v", out.Tools)
	}
	// Human cannot repeat the background's feat unless repeatable
	in.SpeciesChoices["feat"] = "alert"
	if _, err := resolveOrigin(in); err == nil || err.Code != "feat_already_taken" {
		t.Errorf("duplicate feat: %v", err)
	}
	// Skilled may not pick a proficiency the character already has
	in.SpeciesChoices["feat"] = "skilled"
	in.SpeciesFeat = &featChoiceInput{Proficiencies: []string{"stealth", "history", "smith's tools"}}
	if _, err := resolveOrigin(in); err == nil || err.Code != "proficiency_already_held" {
		t.Errorf("duplicate proficiency: %v", err)
	}
}

func TestEverySpeciesCanBeBuilt(t *testing.T) {
	choices := map[string]map[string]string{
		"dragonborn": {"ancestry": "red"},
		"dwarf":      {},
		"elf":        {"lineage": "drow", "spellcasting_ability": "cha", "skill": "insight"},
		"gnome":      {"lineage": "rock", "spellcasting_ability": "int"},
		"goliath":    {"ancestry": "fire"},
		"halfling":   {},
		"human":      {"size": "medium", "skill": "insight", "feat": "alert"},
		"orc":        {},
		"tiefling":   {"legacy": "chthonic", "spellcasting_ability": "wis", "size": "medium"},
	}
	for sp, ch := range choices {
		in := baseInput()
		in.Species = sp
		in.Background = "Criminal"
		in.AbilityIncreases = map[string]int{"dex": 2, "con": 1}
		in.SpeciesChoices = ch
		in.HeldSkills = nil
		if sp == "human" {
			in.SpeciesChoices["feat"] = "skilled" // Criminal already has Alert
			in.SpeciesFeat = &featChoiceInput{Proficiencies: []string{"athletics", "history", "arcana"}}
		}
		out, err := resolveOrigin(in)
		if err != nil {
			t.Errorf("%s: %v", sp, err)
			continue
		}
		if out.Record.Species.Name == "" || len(out.Record.Species.Traits) == 0 {
			t.Errorf("%s has no traits", sp)
		}
	}
}

func TestSpeciesTable(t *testing.T) {
	species := speciesAsSRDSpecies()
	if len(species) != 9 {
		t.Fatalf("want 9 species, got %d", len(species))
	}
	if _, old := species["half_orc"]; old {
		t.Error("Half-Orc must not exist")
	}
	for k, r := range species {
		if len(r.AbilityMods) != 0 || len(r.Languages) != 0 {
			t.Errorf("%s: 2024 species give no ability scores or languages", k)
		}
	}
	if species["orc"].DarkvisionRange != 120 || species["dwarf"].DarkvisionRange != 120 {
		t.Error("orc and dwarf have 120 ft darkvision")
	}
}

// Text that isn't in the SRD comes from a local content folder; without one there is none (local_content.go).
func TestLocalContent(t *testing.T) {
	t.Setenv("AGENTRPG_LOCAL_CONTENT", "")
	var m map[string]string
	if loadLocalJSON("species_intro.json", &m) {
		t.Error("no folder, no local content")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "spells.json"), []byte(`{"test-spell": {"replace": [["a simple tool,", "a simple tool, and more."]], "school": "Evocation", "damage_at_slot_level": {"5": "9d9"}}}`), 0o600)
	t.Setenv("AGENTRPG_LOCAL_CONTENT", dir)
	spells := []srdSpell2024{{Slug: "test-spell", School: "Transmutation", Description: "such as manipulating a simple tool,", DescriptionBlocks: []byte(`["such as manipulating a simple tool,"]`)}}
	applyLocalSpellChanges(spells)
	if spells[0].Description != "such as manipulating a simple tool, and more." || spells[0].School != "Evocation" || spells[0].DamageAtSlotLevel["5"] != "9d9" ||
		!strings.Contains(string(spells[0].DescriptionBlocks), "and more.") {
		t.Errorf("local spell change: %+v", spells[0])
	}
}

// The Handbook's corrections to four SRD spells are applied on every server (spell_corrections_2024.json).
func TestSpellCorrections(t *testing.T) {
	t.Setenv("AGENTRPG_LOCAL_CONTENT", "")
	spells := []srdSpell2024{{Slug: "flaming-sphere", School: "Conjuration"}}
	applyLocalSpellChanges(spells)
	if spells[0].School != "Evocation" {
		t.Errorf("Flaming Sphere's school: %q", spells[0].School)
	}
}

// A species part is checked as sent: another spelling of a skill is the skill (so a duplicate is caught), and a part the species
// doesn't use is refused rather than ignored (Nov 14 tester: a Halfling sent size medium was made Small with no word).
func TestResolveOriginSpeciesParts(t *testing.T) {
	in := baseInput()
	in.Background, in.AbilityIncreases, in.HeldSkills = "Criminal", map[string]int{"dex": 2, "con": 1}, nil
	in.Species = "Human"
	in.SpeciesChoices = map[string]string{"size": "medium", "skill": "sleight_of_hand", "feat": "skilled"}
	in.SpeciesFeat = &featChoiceInput{Proficiencies: []string{"athletics", "history", "arcana"}}
	if _, err := resolveOrigin(in); err == nil || err.Code != "duplicate_skill" {
		t.Errorf("sleight_of_hand with the Criminal's Sleight of Hand: %v", err)
	}
	in.SpeciesChoices["skill"] = "Animal-Handling"
	out, err := resolveOrigin(in)
	if err != nil || !containsStr(out.Skills, "animal handling") || containsStr(out.Skills, "Animal-Handling") {
		t.Errorf("Animal-Handling: %v %v", err, out)
	}
	in.SpeciesChoices["feat"] = "savage_attacker"
	if _, err := resolveOrigin(in); err == nil || err.Code != "species_feat_choices_not_used" {
		t.Errorf("Savage Attacker with Skilled picks: %v", err)
	}
	in.Species, in.SpeciesChoices, in.SpeciesFeat = "Halfling", map[string]string{"size": "medium"}, nil
	if _, err := resolveOrigin(in); err == nil || err.Code != "species_choice_not_used" {
		t.Errorf("Halfling size medium: %v", err)
	}
	in.Species, in.SpeciesChoices = "Dwarf", map[string]string{"lineage": ""}
	in.SpeciesFeat = &featChoiceInput{Proficiencies: []string{"athletics", "history", "arcana"}}
	if _, err := resolveOrigin(in); err == nil || err.Code != "species_feat_choices_not_used" {
		t.Errorf("Dwarf with Skilled picks: %v", err)
	}
	in.Species, in.SpeciesFeat = "Tiefling", nil
	in.SpeciesChoices = map[string]string{"lineage": "infernal", "spellcasting_ability": "cha", "size": "small"}
	if out, err := resolveOrigin(in); err != nil || out.Record.Species.Choices["legacy"] != "infernal" {
		t.Errorf("a Tiefling's lineage is its legacy: %v", err)
	}
}
