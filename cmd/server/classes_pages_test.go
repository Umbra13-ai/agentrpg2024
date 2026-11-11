package main

import "testing"

func TestClassTextStructure(t *testing.T) {
	classes := srdClassTexts()
	if len(classes) != 12 {
		t.Fatalf("want 12 classes, got %d", len(classes))
	}
	casters := map[string]bool{"Bard": true, "Cleric": true, "Druid": true, "Paladin": true, "Ranger": true, "Sorcerer": true, "Warlock": true, "Wizard": true}
	for _, c := range classes {
		var core, features, subclass, lists int
		for _, s := range c.Sections {
			if s.Level == 2 && len(s.Heading) > 9 && s.Heading[len(s.Heading)-len("Spell List"):] == "Spell List" {
				lists++
			}
			if s.Level == 2 && len(s.Heading) > 8 && s.Heading[:len(c.Name)+len(" Subclass")] == c.Name+" Subclass" {
				subclass++
			}
			for _, b := range s.Blocks {
				m, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				if kv, ok := m["keyvalue"].([]interface{}); ok && len(kv) >= 7 {
					core++
				}
				if tb, ok := m["table"].(map[string]interface{}); ok {
					if rows, _ := tb["rows"].([]interface{}); len(rows) == 21 && tb["title"] == c.Name+" Features" {
						features++
					}
				}
			}
		}
		if core != 1 || features != 1 || subclass != 1 {
			t.Errorf("%s: core %d, features table %d, subclass %d", c.Name, core, features, subclass)
		}
		if casters[c.Name] != (lists == 1) {
			t.Errorf("%s: spell list sections %d", c.Name, lists)
		}
		if coreTraitText(c, "Hit Point Die") == "" || coreTraitText(c, "Primary Ability") == "" {
			t.Errorf("%s: missing core traits", c.Name)
		}
		if classBrief[c.Name] == "" {
			t.Errorf("%s: no brief idea", c.Name)
		}
	}
}
