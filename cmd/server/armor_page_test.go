package main

import "testing"

func TestArmorTableIsComplete(t *testing.T) {
	d := srdArmorTable()
	if len(d.Groups) != 4 {
		t.Fatalf("Light, Medium, Heavy and Shield: got %d groups", len(d.Groups))
	}
	n := 0
	for _, g := range d.Groups {
		n += len(g.Armor)
	}
	if n != 13 {
		t.Fatalf("the SRD 5.2.1 has 13 armors and shields, got %d", n)
	}
	if len(d.Rules) < 9 {
		t.Errorf("the armor rules are missing: %d blocks", len(d.Rules))
	}
	titles := map[string]bool{}
	for _, r := range d.Rules {
		titles[r.Title] = true
		if r.Text == "" {
			t.Errorf("rule %q has no text", r.Title)
		}
	}
	for _, want := range []string{"Category", "Armor Class (AC)", "Strength", "Stealth", "Armor Training", "One at a Time"} {
		if !titles[want] {
			t.Errorf("missing rule block %q", want)
		}
	}
}

func TestArmorRowsMatchThePrintedTable(t *testing.T) {
	want := map[string]struct {
		ac      int
		str     int
		stealth bool
		typ     string
	}{
		"Padded Armor": {11, 0, true, "light"}, "Leather Armor": {11, 0, false, "light"}, "Studded Leather Armor": {12, 0, false, "light"},
		"Hide Armor": {12, 0, false, "medium"}, "Chain Shirt": {13, 0, false, "medium"}, "Scale Mail": {14, 0, true, "medium"},
		"Breastplate": {14, 0, false, "medium"}, "Half Plate Armor": {15, 0, true, "medium"},
		"Ring Mail": {14, 0, true, "heavy"}, "Chain Mail": {16, 13, true, "heavy"}, "Splint Armor": {17, 15, true, "heavy"}, "Plate Armor": {18, 15, true, "heavy"},
		"Shield": {2, 0, false, "shield"},
	}
	for _, g := range srdArmorTable().Groups {
		for _, a := range g.Armor {
			w, ok := want[a.Name]
			if !ok {
				t.Errorf("unexpected armor %q", a.Name)
				continue
			}
			if armorBaseAC(a.AC) != w.ac || armorStrengthNeeded(a.Strength) != w.str || (a.Stealth == "Disadvantage") != w.stealth || armorGroupType(g.Name) != w.typ {
				t.Errorf("%s: ac %q strength %q stealth %q in %q", a.Name, a.AC, a.Strength, a.Stealth, g.Name)
			}
		}
	}
}

func TestArmorSlugsMatchWhatTheGameStores(t *testing.T) {
	cases := map[string]string{"Padded Armor": "padded-armor", "Studded Leather Armor": "studded-leather-armor", "Half Plate Armor": "half-plate-armor", "Splint Armor": "splint-armor",
		"Plate Armor": "plate-armor", "Chain Mail": "chain-mail", "Chain Shirt": "chain-shirt", "Shield": "shield", "Hide Armor": "hide-armor", "Leather Armor": "leather-armor"}
	for in, want := range cases {
		if got := armorSlugFromName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestArmorRules(t *testing.T) {
	// SRD 5.2.1: a Strength score in the table reduces Speed by 10 feet unless the wearer's Strength is at least that score
	for _, c := range []struct{ str, req, want int }{{13, 13, 0}, {12, 13, 10}, {10, 15, 10}, {15, 15, 0}, {8, 0, 0}} {
		if got := strengthSpeedPenalty(c.str, c.req); got != c.want {
			t.Errorf("strength %d, requirement %d: penalty %d, want %d", c.str, c.req, got, c.want)
		}
	}
	// a Shield counts only with training
	if !shieldTrained("light, medium, shields") || !shieldTrained("all armor, shields") || shieldTrained("light, medium") || shieldTrained("") {
		t.Error("shield training")
	}
}
