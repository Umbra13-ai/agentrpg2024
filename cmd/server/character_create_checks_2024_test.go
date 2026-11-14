package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func createBody(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	m := map[string]interface{}{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateFieldsUnknownAndMisplaced(t *testing.T) {
	raw := createBody(t, `{"name":"A","class":"cleric","languages":["Elvish"],"subclass":"life","divne_order":"x","class_options":{"divne_order":"protector"}}`)
	bad := unknownCreateFields(raw)
	all := strings.Join(bad, " | ")
	for _, want := range []string{`"languages" is not a field this request takes: use extra_languages`, `"subclass"`, `did you mean "class_options.divine_order"`, `"class_options.divne_order" is not a class option`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %q", want, all)
		}
	}
	if bad := unknownCreateFields(createBody(t, `{"name":"A","class":"cleric","dry_run":true,"backstory_skip":true,"known_spells":[]}`)); len(bad) != 0 {
		t.Errorf("known fields refused: %v", bad)
	}
}

func TestCreateFieldAliases(t *testing.T) {
	raw := createBody(t, `{"species":"dragonborn","draconic_ancestry":"Red","divine_order":"protector"}`)
	if c := normalizeCreateAliases(raw); len(c) != 0 {
		t.Fatalf("unexpected conflicts %v", c)
	}
	if got := raw["species_choices"].(map[string]interface{})["ancestry"]; got != "red" {
		t.Errorf("draconic_ancestry did not fill species_choices.ancestry: %v", got)
	}
	if got := raw["class_options"].(map[string]interface{})["divine_order"]; got != "protector" {
		t.Errorf("divine_order not moved into class_options: %v", got)
	}
	if _, still := raw["divine_order"]; still {
		t.Error("top-level divine_order left in place")
	}
	raw = createBody(t, `{"species":"dragonborn","draconic_ancestry":"red","species_choices":{"ancestry":"bronze"}}`)
	if c := normalizeCreateAliases(raw); len(c) != 1 || !strings.Contains(c[0], "disagree") {
		t.Errorf("conflicting ancestry not reported: %v", c)
	}
}

func TestCreateFieldsInapplicable(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"species":"dragonborn","species_choices":{"ancestry":"bronze","lineage":"drow"}}`, "species_choices.lineage: a Dragonborn doesn't choose this"},
		{`{"species":"dwarf","species_choices":{"size":"small"}}`, "a Dwarf makes no species choices"},
		{`{"species":"elf","draconic_ancestry":"red"}`, "only a Dragonborn has one, not an Elf"},
		{`{"species":"orc","species_feat_choices":{"proficiencies":["arcana"]}}`, "only a Human picks an extra Origin feat"},
		{`{"species":"human","species_choices":{"feat":"alert"},"species_feat_choices":{"proficiencies":["arcana"]}}`, "Alert, has no choices"},
		{`{"background":"soldier","background_feat_choices":{"list":"cleric"}}`, "Savage Attacker, has no choices"},
		{`{"background":"acolyte","background_feat_choices":{"list":"cleric","proficiencies":["arcana"]}}`, "Magic Initiate takes list"},
		{`{"background":"acolyte","background_tool":"dice"}`, "Acolyte gives Calligrapher's Supplies"},
		{`{"known_spells":["fireball"]}`, "known_spells: spells are chosen after creation"},
	}
	for _, c := range cases {
		got := strings.Join(inapplicableCreateFields(createBody(t, c.body)), " | ")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q, got %q", c.body, c.want, got)
		}
	}
	for _, ok := range []string{
		`{"species":"tiefling","species_choices":{"lineage":"infernal","spellcasting_ability":"cha","size":"medium"}}`,
		`{"species":"dwarf","species_choices":{"lineage":""}}`,
		`{"background":"criminal","background_tool":"thieves tools"}`,
		`{"species":"human","species_choices":{"feat":"skilled"},"species_feat_choices":{"proficiencies":["arcana","history","nature"]}}`,
		`{"known_spells":[]}`,
	} {
		if got := inapplicableCreateFields(createBody(t, ok)); len(got) != 0 {
			t.Errorf("%s refused: %v", ok, got)
		}
	}
}

func TestCreateTypeError(t *testing.T) {
	var req struct {
		SkillProficiencies []string `json:"skill_proficiencies"`
	}
	err := json.Unmarshal([]byte(`{"skill_proficiencies":"perception"}`), &req)
	if msg := createTypeError(err); !strings.Contains(msg, "skill_proficiencies must be a list") || !strings.Contains(msg, "a string") {
		t.Errorf("unexpected message %q", msg)
	}
}
