package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpellTablesAndStatblocks(t *testing.T) {
	var spells []srdSpell2024
	if err := json.Unmarshal(srd521SpellsJSON, &spells); err != nil {
		t.Fatal(err)
	}
	tables := map[string][][]string{}
	count, stat := 0, 0
	for _, s := range spells {
		var blocks []map[string]interface{}
		var raw []interface{}
		json.Unmarshal(s.DescriptionBlocks, &raw)
		for _, r := range raw {
			if m, ok := r.(map[string]interface{}); ok {
				blocks = append(blocks, m)
			}
		}
		for _, m := range blocks {
			tb := m["table"].(map[string]interface{})
			var rows [][]string
			for _, r := range tb["rows"].([]interface{}) {
				var cells []string
				for _, c := range r.([]interface{}) {
					cells = append(cells, c.(string))
				}
				rows = append(rows, cells)
			}
			tables[s.Slug+"/"+tb["title"].(string)] = rows
			count++
		}
		if len(s.Statblock) > 0 && string(s.Statblock) != "null" {
			stat++
		}
	}
	if count != 13 {
		t.Errorf("want 13 tables in 10 spells, got %d", count)
	}
	tp := tables["teleport/Teleportation Outcome"]
	if len(tp) != 7 || len(tp[5]) != 5 || tp[5][0] != "Viewed once or described" || tp[5][4] != "74–00" {
		t.Errorf("Teleport table: %v", tp)
	}
	if pw := tables["prismatic-wall/Prismatic Layers"]; len(pw) != 8 {
		t.Errorf("Prismatic Wall wants a header and 7 layers, got %d rows", len(pw))
	}
	if stat != 4 {
		t.Errorf("want 4 stat blocks, got %d", stat)
	}
}

func TestDamagePartsOfSpells(t *testing.T) {
	var spells []srdSpell2024
	if err := json.Unmarshal(srd521SpellsJSON, &spells); err != nil {
		t.Fatal(err)
	}
	with := map[string][]damagePart{}
	for _, s := range spells {
		var parts []damagePart
		if len(s.DamageParts) > 0 {
			json.Unmarshal(s.DamageParts, &parts)
		}
		if len(parts) > 0 {
			with[s.Slug] = parts
			if s.Slug != "animate-objects" && (parts[0].Dice != s.DamageDice || parts[0].Type != s.DamageType) {
				t.Errorf("%s: the first part %s %s is not the spell's damage %s %s", s.Slug, parts[0].Dice, parts[0].Type, s.DamageDice, s.DamageType)
			}
		}
	}
	if len(with) != 12 {
		t.Errorf("want 12 spells with damage parts, got %d", len(with))
	}
	fs := with["flame-strike"]
	if len(fs) != 2 || fs[1].Type != "radiant" || fs[1].diceAt(7) != "7d6" {
		t.Errorf("Flame Strike: %+v", fs)
	}
	ik := with["ice-knife"]
	if len(ik) != 2 || ik[1].Save != "DEX" || ik[1].OnSave != "none" || ik[1].diceAt(3) != "4d6" {
		t.Errorf("Ice Knife: %+v", ik)
	}
	text := extraPartsText(with["acid-arrow"], 3, 15)
	if !strings.Contains(text, "3d4 acid") || !strings.Contains(text, "end of the target's next turn") {
		t.Errorf("Acid Arrow later damage: %q", text)
	}
	if got := primarySaveInfo(ik, 14); got != "" {
		t.Errorf("Ice Knife's attack has no save: %q", got)
	}
	if got := primarySaveInfo(fs, 14); !strings.Contains(got, "DC 14 Dexterity save, half") {
		t.Errorf("Flame Strike save text: %q", got)
	}
}
