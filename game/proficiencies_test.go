package game

import (
	"strings"
	"testing"
)

func list(p []string) string { return strings.Join(p, ", ") }

// Expected values are the SRD 5.2.1 "Core ... Traits" tables.
func TestClassProficiencies2024(t *testing.T) {
	cases := []struct {
		class, weapons, armor, tools string
	}{
		{"barbarian", "simple, martial", "light, medium, shields", ""},
		{"bard", "simple", "light", ""},
		{"cleric", "simple", "light, medium, shields", ""},
		{"druid", "simple", "light, shields", "herbalism kit"},
		{"fighter", "simple, martial", "light, medium, heavy, shields", ""},
		{"monk", "simple, " + WeaponMartialLight, "", ""},
		{"paladin", "simple, martial", "light, medium, heavy, shields", ""},
		{"ranger", "simple, martial", "light, medium, shields", ""},
		{"rogue", "simple, " + WeaponMartialFinesseLight, "light", "thieves' tools"},
		{"sorcerer", "simple", "", ""},
		{"warlock", "simple", "light", ""},
		{"wizard", "simple", "", ""},
	}
	for _, c := range cases {
		p := ClassProficiencies(c.class)
		if list(p.Weapons) != c.weapons || list(p.Armor) != c.armor || list(p.Tools) != c.tools {
			t.Errorf("%s = weapons [%s] armor [%s] tools [%s]; want [%s] [%s] [%s]",
				c.class, list(p.Weapons), list(p.Armor), list(p.Tools), c.weapons, c.armor, c.tools)
		}
	}
}

func TestEveryClassHasStartAndMulticlassEntry(t *testing.T) {
	for _, c := range []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"} {
		if _, ok := classStartProficiencies[c]; !ok {
			t.Errorf("missing start proficiencies for %s", c)
		}
		if _, ok := classMulticlassProficiencies[c]; !ok {
			t.Errorf("missing multiclass proficiencies for %s", c)
		}
	}
}

func TestMulticlassProficiencies2024(t *testing.T) {
	// Wizard 3 / Fighter 1: fighter multiclass gives Martial weapons and Light, Medium, Shields
	p := ProficienciesFor(CharacterInput{FirstClass: "wizard", ClassLevels: map[string]int{"wizard": 3, "fighter": 1}})
	if !strings.Contains(list(p.Weapons), "martial") || list(p.Armor) != "light, medium, shields" {
		t.Errorf("wizard/fighter = weapons [%s] armor [%s]", list(p.Weapons), list(p.Armor))
	}
	// Barbarian multiclass: Martial weapons and Shields only
	b := MulticlassProficiencies("barbarian")
	if list(b.Weapons) != "martial" || list(b.Armor) != "shields" {
		t.Errorf("barbarian multiclass = %v %v", b.Weapons, b.Armor)
	}
	// Monk and Sorcerer multiclass grant nothing
	if m := MulticlassProficiencies("monk"); len(m.Weapons)+len(m.Armor)+len(m.Tools) != 0 {
		t.Errorf("monk multiclass should grant nothing, got %+v", m)
	}
}

func TestDivineAndPrimalOrder(t *testing.T) {
	p := ProficienciesFor(CharacterInput{FirstClass: "cleric", DivineOrder: "Protector"})
	if !strings.Contains(list(p.Weapons), "martial") || !strings.Contains(list(p.Armor), "heavy") {
		t.Errorf("protector cleric = %v %v", p.Weapons, p.Armor)
	}
	w := ProficienciesFor(CharacterInput{FirstClass: "druid", PrimalOrder: "warden"})
	if !strings.Contains(list(w.Weapons), "martial") || !strings.Contains(list(w.Armor), "medium") {
		t.Errorf("warden druid = %v %v", w.Weapons, w.Armor)
	}
	n := ProficienciesFor(CharacterInput{FirstClass: "cleric", DivineOrder: "thaumaturge"})
	if strings.Contains(list(n.Armor), "heavy") {
		t.Errorf("thaumaturge should not get heavy armor")
	}
}

func TestMergeProficiencyListKeepsOrderAndDedupes(t *testing.T) {
	got := MergeProficiencyList("Simple, dagger", []string{"dagger", "Dart", " ", "simple"})
	if got != "simple, dagger, dart" {
		t.Errorf("got %q", got)
	}
}

func TestToolCatalogMatchesHandbook(t *testing.T) {
	if n := len(ToolsInCategory(ToolArtisan)); n != 17 {
		t.Errorf("artisan tools = %d, want 17", n)
	}
	if n := len(ToolsInCategory(ToolInstrument)); n != 10 {
		t.Errorf("musical instruments = %d, want 10", n)
	}
	if n := len(ToolsInCategory(ToolGaming)); n != 4 {
		t.Errorf("gaming sets = %d, want 4", n)
	}
	th, ok := FindTool("Thieves' Tools")
	if !ok || th.WeightLb != 1 || th.CostGP != 25 || th.Ability != "dexterity" {
		t.Errorf("thieves' tools = %+v", th)
	}
	if lute, ok := FindTool("lute"); !ok || lute.WeightLb != 2 || lute.CostGP != 35 {
		t.Errorf("lute = %+v", lute)
	}
	if _, ok := FindTool("dice set"); !ok {
		t.Error("dice set should resolve to dice")
	}
}

// Gaming Sets and Musical Instruments are stored with their kind, as the SRD names them (BUGS 45).
func TestToolFullNames(t *testing.T) {
	for in, want := range map[string]string{"dice": "gaming set (dice)", "Dice Set": "gaming set (dice)", "Gaming Set (Dice)": "gaming set (dice)",
		"lute": "musical instrument (lute)", "Musical Instrument: Pan Flute": "musical instrument (pan flute)", "Thieves' Tools": "thieves' tools", "bagpipe": "musical instrument (bagpipes)"} {
		if got := CanonicalToolName(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
	if got := ToolDisplayName("three-dragon ante"); got != "Gaming Set (Three-Dragon Ante)" {
		t.Errorf("display = %q", got)
	}
	if got := ToolDisplayName("thieves' tools"); got != "Thieves' Tools" {
		t.Errorf("display = %q", got)
	}
	if got := MergeToolList("dice, thieves' tools", []string{"Gaming Set (Dice)", "lute"}); got != "gaming set (dice), thieves' tools, musical instrument (lute)" {
		t.Errorf("merge = %q", got)
	}
	owned := []string{"gaming set (playing cards)", "smith's tools"}
	for name, want := range map[string]bool{"playing cards": true, "Gaming Set": true, "dice": false, "artisan's tools": true, "musical instrument": false} {
		if got := HasToolProficiency(owned, name); got != want {
			t.Errorf("proficient with %q = %v", name, got)
		}
	}
	if got := ParseProficiencyList(`["thieves' tools", "lute"]`); len(got) != 2 || got[1] != "lute" {
		t.Errorf("a JSON list = %v", got)
	}
}

func TestBardAndMonkToolChoices(t *testing.T) {
	bard := ToolChoicesFor("bard", nil)
	if len(bard) != 1 || bard[0].Count != 3 || bard[0].Categories[0] != ToolInstrument {
		t.Errorf("bard choices = %+v", bard)
	}
	monk := ToolChoicesFor("monk", nil)
	if len(monk) != 1 || monk[0].Count != 1 || len(monk[0].Categories) != 2 {
		t.Errorf("monk choices = %+v", monk)
	}
	multi := ToolChoicesFor("fighter", map[string]int{"fighter": 5, "bard": 1})
	if len(multi) != 1 || multi[0].Count != 1 {
		t.Errorf("fighter/bard choices = %+v", multi)
	}
	if len(ToolChoicesFor("wizard", nil)) != 0 {
		t.Error("wizard owes no tool choices")
	}
}

func TestPendingToolChoicesCountsOwnedTools(t *testing.T) {
	choices := ToolChoicesFor("bard", nil)
	if p := PendingToolChoices(choices, ""); len(p) != 1 || p[0].Count != 3 {
		t.Errorf("none owned = %+v", p)
	}
	if p := PendingToolChoices(choices, "lute, drum"); len(p) != 1 || p[0].Count != 1 {
		t.Errorf("two owned = %+v", p)
	}
	if p := PendingToolChoices(choices, "lute, drum, flute, thieves' tools"); len(p) != 0 {
		t.Errorf("all owned = %+v", p)
	}
	// a Monk who picked Smith's Tools has nothing pending
	if p := PendingToolChoices(ToolChoicesFor("monk", nil), "smith's tools"); len(p) != 0 {
		t.Errorf("monk with smith's tools = %+v", p)
	}
}

func TestValidateToolChoices(t *testing.T) {
	pending := ToolChoicesFor("bard", nil)
	names, msg := ValidateToolChoices([]string{"Lute", "drum", "flute"}, pending)
	if msg != "" || len(names) != 3 || names[0] != "musical instrument (lute)" {
		t.Errorf("valid picks = %v %q", names, msg)
	}
	if _, msg := ValidateToolChoices([]string{"smith's tools"}, pending); !strings.HasPrefix(msg, "tool_not_allowed_here") {
		t.Errorf("artisan tool should not satisfy bard instruments: %q", msg)
	}
	if _, msg := ValidateToolChoices([]string{"kazoo"}, pending); !strings.HasPrefix(msg, "unknown_tool") {
		t.Errorf("unknown tool: %q", msg)
	}
	if _, msg := ValidateToolChoices([]string{"lute", "lute"}, pending); !strings.HasPrefix(msg, "duplicate_tool") {
		t.Errorf("duplicate: %q", msg)
	}
	if _, msg := ValidateToolChoices([]string{"lute", "drum", "flute", "lyre"}, pending); msg != "wrong_number_of_tools" {
		t.Errorf("too many: %q", msg)
	}
	if _, msg := ValidateToolChoices([]string{"lute"}, nil); msg != "no_tool_choices_pending" {
		t.Errorf("none pending: %q", msg)
	}
	// Monk may pick either an artisan tool or an instrument
	if _, msg := ValidateToolChoices([]string{"lyre"}, ToolChoicesFor("monk", nil)); msg != "" {
		t.Errorf("monk instrument: %q", msg)
	}
}
