package main

import "testing"

func inv(names ...string) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, n := range names {
		out = append(out, map[string]interface{}{"name": n})
	}
	return out
}

func TestFocusIsPerClass(t *testing.T) {
	cases := []struct {
		class string
		items []string
		ok    bool
	}{
		{"wizard", []string{"Arcane Focus"}, true},
		{"wizard", []string{"Crystal"}, true},
		{"wizard", []string{"Holy Symbol"}, false},
		{"wizard", []string{"Druidic Focus"}, false},
		{"wizard", []string{"Spellbook"}, true},
		{"sorcerer", []string{"Orb"}, true},
		{"sorcerer", []string{"Spellbook"}, false}, // only a Wizard may use a spellbook
		{"warlock", []string{"Rod"}, true},
		{"cleric", []string{"Holy Symbol, Amulet"}, true},
		{"cleric", []string{"Arcane Focus"}, false},
		{"paladin", []string{"Holy Symbol"}, true},
		{"paladin", []string{"Staff"}, false},
		{"druid", []string{"Druidic Focus"}, true},
		{"druid", []string{"Sprig of Mistletoe"}, true},
		{"druid", []string{"Staff"}, true}, // a wooden staff
		{"druid", []string{"Holy Symbol"}, false},
		{"ranger", []string{"Totem"}, true},
		{"ranger", []string{"Arcane Focus"}, false},
		{"bard", []string{"Lute"}, true},
		{"bard", []string{"Musical Instrument"}, true},
		{"bard", []string{"Holy Symbol"}, false},
		{"fighter", []string{"Arcane Focus"}, false},   // no Fighter casts in the SRD 5.2.1 (Eldritch Knight is not in it)
		{"fighter", []string{"Component Pouch"}, true}, // a species spell with a Material component
		{"rogue", []string{"Holy Symbol"}, false},      // Arcane Trickster
		{"cleric", []string{"Component Pouch"}, true},
		{"wizard", []string{"Component Pouch"}, true},
		{"bard", []string{"Component Pouch"}, true},
		{"wizard", []string{"Rope", "Torch"}, false},
		{"wizard", nil, false},
	}
	for _, c := range cases {
		if got := hasUsableFocus(c.class, inv(c.items...)); got != c.ok {
			t.Errorf("%s with %v: got %v, want %v", c.class, c.items, got, c.ok)
		}
	}
}

func TestKitFocusKindIsRespected(t *testing.T) {
	// class kits mark their focus with a kind; the druid's quarterstaff is a primal focus
	druidStaff := []map[string]interface{}{{"name": "Quarterstaff", "spellcasting_focus": true, "focus_kind": "primal"}}
	if !hasUsableFocus("druid", druidStaff) {
		t.Error("a druid's marked quarterstaff should work")
	}
	if hasUsableFocus("wizard", druidStaff) {
		t.Error("a primal focus must not work for a wizard")
	}
	if !hasUsableFocus("wizard", []map[string]interface{}{{"name": "Quarterstaff", "spellcasting_focus": true, "focus_kind": "arcane"}}) {
		t.Error("an arcane focus should work for a wizard")
	}
}

func TestCheckSpellComponentsMessageNamesTheFocus(t *testing.T) {
	msg := checkSpellComponents("V, S, M", nil, inv("Holy Symbol"), false, nil, nil, "wizard", 1)
	if msg == "" || !contains(msg, "Arcane Focus") {
		t.Errorf("a wizard with only a holy symbol should be told to get an Arcane Focus, got %q", msg)
	}
	if msg := checkSpellComponents("V, S, M", nil, inv("Holy Symbol"), false, nil, nil, "cleric", 1); msg != "" {
		t.Errorf("a cleric with a holy symbol can cast: %q", msg)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
