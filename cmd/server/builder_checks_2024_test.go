package main

import (
	"reflect"
	"strings"
	"testing"
)

// refusedFields: the fields (or parts) the input layer refused.
func refusedFields(in builderInput) map[string]string {
	out := map[string]string{}
	for _, r := range in.refused {
		out[r["field"].(string)] = r["problem"].(string)
	}
	return out
}

func TestBuilderInputRefusesWrongTypes(t *testing.T) {
	upd := map[string]interface{}{
		"skill_proficiencies": "acrobatics, survival", "weapon_masteries": []interface{}{1.0, 2.0}, "starting_equipment": []interface{}{"a"},
		"class_options": "thaumaturge", "species_choices": []interface{}{"medium"}, "ability_increases": []interface{}{"dex", "dex", "con"},
		"backstory": []interface{}{"a", "b"}, "backstory_skip": "yes", "appearance": 5.0, "str": 15.4, "alignment": true,
		"background_feat_choices": true, "extra_languages": map[string]interface{}{"a": "Elvish"},
	}
	n := len(upd)
	in := checkBuilderInput(upd, map[string]interface{}{"class": "cleric"})
	if len(in.unknown) > 0 {
		t.Fatalf("unexpected unknown fields: %v", in.unknown)
	}
	got := refusedFields(in)
	if len(got) != n {
		t.Fatalf("refused %d of %d fields: %v", len(got), n, got)
	}
	if len(upd) != 0 {
		t.Errorf("refused answers left in the update: %v", upd)
	}
	if !strings.Contains(got["skill_proficiencies"], `["acrobatics", "survival"]`) {
		t.Errorf("a list sent as text should be shown as a list: %q", got["skill_proficiencies"])
	}
}

func TestBuilderInputNormalizes(t *testing.T) {
	upd := map[string]interface{}{
		"class": " FIGHTER ", "name": "  Wren  ", "str": 15.0, "dex": 14.0, "ability_method": "Standard Array",
		"ability_increases":   map[string]interface{}{"Strength": 2.0, "con": 1.0, "cha": 0.0},
		"skill_proficiencies": []interface{}{"Sleight_of_Hand", " perception "},
		"backstory":           "One.\r\n\r\nTwo.",
		"backstory_skip":      false,
		"species":             "elf",
		"species_choices":     map[string]interface{}{"Lineage": "High-Elf", "Spellcasting Ability": "Wisdom", "skill": "Insight"},
	}
	in := checkBuilderInput(upd, map[string]interface{}{})
	if len(in.unknown) > 0 || len(in.refused) > 0 {
		t.Fatalf("unexpected refusals: %v %v", in.unknown, in.refused)
	}
	want := map[string]interface{}{
		"class": "fighter", "name": "Wren", "str": 15.0, "dex": 14.0, "ability_method": "standard_array",
		"ability_increases":   map[string]interface{}{"str": 2.0, "con": 1.0},
		"skill_proficiencies": []interface{}{"sleight of hand", "perception"},
		"backstory":           "One.\n\nTwo.", "backstory_skip": false, "species": "elf",
		"species_choices": map[string]interface{}{"lineage": "high_elf", "spellcasting_ability": "wis", "skill": "insight"},
	}
	for k, v := range want {
		if !reflect.DeepEqual(upd[k], v) {
			t.Errorf("%s = %#v, want %#v", k, upd[k], v)
		}
	}
}

// a number or a true/false sent as text is refused, as POST /api/characters refuses it (Nov 14 audit re-check, F4 and F10)
func TestBuilderInputRefusesText(t *testing.T) {
	upd := map[string]interface{}{"str": "15", "backstory_skip": "true", "ability_increases": map[string]interface{}{"str": "2", "con": 1.0}}
	in := checkBuilderInput(upd, map[string]interface{}{})
	if got := refusedFields(in); len(got) != 3 {
		t.Fatalf("want str, backstory_skip and ability_increases refused, got %v", got)
	}
}

func TestBuilderInputAliasesAndParts(t *testing.T) {
	upd := map[string]interface{}{"divine_order": "Thaumaturge (one extra cantrip)", "known_spells": []interface{}{"wish"}}
	in := checkBuilderInput(upd, map[string]interface{}{"class": "cleric"})
	if co, _ := upd["class_options"].(map[string]interface{}); co["divine_order"] != "thaumaturge" {
		t.Errorf("divine_order should be read as class_options.divine_order: %v", upd)
	}
	if _, ok := refusedFields(in)["known_spells"]; !ok {
		t.Errorf("known_spells should be refused: %v", in.refused)
	}

	upd = map[string]interface{}{"draconic_ancestry": "blue"}
	checkBuilderInput(upd, map[string]interface{}{"species": "dragonborn"})
	if sc, _ := upd["species_choices"].(map[string]interface{}); sc["ancestry"] != "blue" {
		t.Errorf("draconic_ancestry should be read as species_choices.ancestry: %v", upd)
	}

	upd = map[string]interface{}{"class_options": map[string]interface{}{"divine_order": "protector, thaumaturge"}}
	if _, ok := refusedFields(checkBuilderInput(upd, map[string]interface{}{"class": "cleric"}))["class_options.divine_order"]; !ok {
		t.Error("an answer naming both orders should be refused")
	}
	upd = map[string]interface{}{"class_options": map[string]interface{}{"primal_order": "warden"}}
	if _, ok := refusedFields(checkBuilderInput(upd, map[string]interface{}{"class": "cleric"}))["class_options.primal_order"]; !ok {
		t.Error("a Druid's order for a Cleric should be refused")
	}
	upd = map[string]interface{}{"species_choices": map[string]interface{}{"lineage": "infernal"}}
	checkBuilderInput(upd, map[string]interface{}{"species": "tiefling"})
	if sc := upd["species_choices"].(map[string]interface{}); sc["legacy"] != "infernal" {
		t.Errorf("a Tiefling's lineage is its legacy: %v", sc)
	}
	upd = map[string]interface{}{"species_choices": map[string]interface{}{"ancestry": "red"}}
	if _, ok := refusedFields(checkBuilderInput(upd, map[string]interface{}{"species": "elf"}))["species_choices.ancestry"]; !ok {
		t.Error("a part the species doesn't choose should be refused")
	}

	for _, upd := range []map[string]interface{}{
		{"class_options": map[string]interface{}{"fighting_style": "defense"}},
		{"class_options": map[string]interface{}{"divineorder": "protector"}},
		{"species_choices": map[string]interface{}{"lineag": "high_elf"}},
		{"background_feat_choices": map[string]interface{}{"cantrip": []interface{}{"light"}}},
	} {
		if in := checkBuilderInput(upd, map[string]interface{}{"class": "cleric", "species": "elf"}); len(in.unknown) == 0 {
			t.Errorf("unknown part not refused: %v", upd)
		}
	}
}

func TestBuilderInputText(t *testing.T) {
	for _, name := range []string{"Nul\x00", "Tab\tName", "​", "---", strings.Repeat("Y", 256)} {
		upd := map[string]interface{}{"name": name}
		if _, ok := refusedFields(checkBuilderInput(upd, map[string]interface{}{}))["name"]; !ok {
			t.Errorf("name %q should be refused", name)
		}
	}
	upd := map[string]interface{}{"backstory": "One.\tTwo.\n\nThree.", "name": "Zoë O'Neil"}
	if in := checkBuilderInput(upd, map[string]interface{}{}); len(in.refused) > 0 {
		t.Errorf("a tab or line break in a backstory, or an accented name, is fine: %v", in.refused)
	}
	upd = map[string]interface{}{"backstory": "One.\x00"}
	if _, ok := refusedFields(checkBuilderInput(upd, map[string]interface{}{}))["backstory"]; !ok {
		t.Error("a NUL in the backstory should be refused")
	}
	upd = map[string]interface{}{"ability_increases": map[string]interface{}{"str": 2.0, "STR": 2.0, "con": 1.0}}
	if _, ok := refusedFields(checkBuilderInput(upd, map[string]interface{}{}))["ability_increases"]; !ok {
		t.Error("an ability sent twice should be refused")
	}
}

func TestBuilderMergeListKeepsValidItems(t *testing.T) {
	c := map[string]interface{}{"count": 4, "options": []string{"athletics", "deception", "intimidation", "perception", "acrobatics"}}
	got := mergeList(map[string]interface{}{}, "skill_proficiencies", []interface{}{"athletics", "deception", "intimidation", "stealth"},
		[]interface{}{"perception", "acrobatics", "deception", "athletics"}, c)
	want := []interface{}{"athletics", "deception", "intimidation", "perception"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeList = %v, want %v", got, want)
	}
}

func TestBuilderSummaryText(t *testing.T) {
	if n := countParagraphs("A.\r\n\r\nB.\r\n\r\nC."); n != 3 {
		t.Errorf("CRLF paragraphs: %d, want 3", n)
	}
	if n := countParagraphs("One.\n\n\n\nTwo."); n != 2 {
		t.Errorf("empty paragraphs counted: %d, want 2", n)
	}
	for _, v := range []interface{}{nil, []interface{}{"greatsword", "flail"}, map[string]interface{}{"proficiencies": []interface{}{"perception"}}, 12.0} {
		if s := readable(v); strings.Contains(s, "<nil>") || strings.Contains(s, "map[") || strings.Contains(s, "[") {
			t.Errorf("readable(%v) = %q", v, s)
		}
	}
	if opts := increaseOptions([]string{"str", "dex", "con"}); len(opts) != 7 {
		t.Errorf("a background's increases have 7 forms, got %d", len(opts))
	}
}
