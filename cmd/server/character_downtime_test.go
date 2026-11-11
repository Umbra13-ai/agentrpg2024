package main

import "testing"

// Every item named on a tool's Craft line is in the SRD tables, and the plans follow SRD 5.2.1 (BUGS 108).
func TestCraftListsAndPlans(t *testing.T) {
	loadCraftData()
	for _, ce := range craftEntries {
		if ce.name == "" {
			continue
		}
		found := false
		for _, r := range craftItems {
			if ce.matches(r) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: %q is in no SRD table", ce.tool, ce.name)
		}
	}
	cases := []struct {
		item, tool, notTool string
		materials, days     int
	}{
		{"Longsword", "smith's tools", "", 7, 2},
		{"plate armor", "smith's tools", "", 750, 150},
		{"Club", "carpenter's tools", "smith's tools", 0, 1},
		{"Longbow", "woodcarver's tools", "smith's tools", 25, 5},
		{"Hide Armor", "leatherworker's tools", "smith's tools", 5, 1},
		{"Potion of Healing", "herbalism kit", "", 25, 1},
	}
	for _, c := range cases {
		p, code, msg := planCraft(c.item, 0)
		if code != "" {
			t.Errorf("%s: %s %s", c.item, code, msg)
			continue
		}
		if !containsStr(p.Tools, c.tool) || (c.notTool != "" && containsStr(p.Tools, c.notTool)) {
			t.Errorf("%s: tools %v", c.item, p.Tools)
		}
		if p.Materials != c.materials || p.Days != c.days {
			t.Errorf("%s: %d GP and %d days, want %d and %d", c.item, p.Materials, p.Days, c.materials, c.days)
		}
	}
	if p, code, _ := planCraft("arrows", 0); code != "" || p.Qty != 20 || p.Item != "Arrows" || !containsStr(p.Tools, "woodcarver's tools") {
		t.Errorf("arrows: %+v %s", p, code)
	}
	if p, code, _ := planCraft("Glass Bottle", 0); code != "" || p.Item != "Glass Bottle" || !containsStr(p.Tools, "glassblower's tools") {
		t.Errorf("glass bottle: %+v %s", p, code)
	}
	if _, code, _ := planCraft("Spell Scroll (Fireball)", 0); code != "use_scribe" {
		t.Errorf("spell scroll: %s", code)
	}
	if _, code, _ := planCraft("Bag of Holding", 0); code != "magic_item" {
		t.Errorf("magic item: %s", code)
	}
	if _, code, _ := planCraft("Clockwork Widget", 0); code != "item_cost_required" {
		t.Errorf("unknown item without a cost: %s", code)
	}
	if p, code, _ := planCraft("Clockwork Widget", 35); code != "" || p.Days != 4 || p.Materials != 17 || len(p.Tools) != 0 {
		t.Errorf("unknown item with a cost: %+v %s", p, code)
	}
}
