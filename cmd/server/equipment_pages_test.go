package main

import (
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The game's tool table must agree with the SRD text: ability, weight, cost.
func TestToolsMatchSRD(t *testing.T) {
	srd := map[string]equipItem{}
	for _, it := range srdEquipment().Tools.Items {
		srd[strings.ToLower(it.Name)] = it
	}
	if len(srd) != 25 {
		t.Fatalf("SRD tools: want 25, got %d", len(srd))
	}
	for _, g := range game.Tools {
		it, ok := srd[g.Name]
		if !ok {
			continue // grouped entries (gaming sets, instruments) are listed as variants in the SRD
		}
		if !strings.EqualFold(it.Ability, g.Ability) {
			t.Errorf("%s: SRD ability %q, game %q", g.Name, it.Ability, g.Ability)
		}
		if w := weightPounds(it.Weight); w >= 0 && g.WeightLb >= 0 && w != g.WeightLb {
			t.Errorf("%s: SRD weight %v, game %v", g.Name, w, g.WeightLb)
		}
		if c := costGP(it.Cost); c >= 0 && c != g.CostGP {
			t.Errorf("%s: SRD cost %v GP, game %v GP", g.Name, c, g.CostGP)
		}
	}
}

func TestEquipmentMechanics(t *testing.T) {
	var acid, thieves map[string]interface{}
	for _, r := range equipmentRows() {
		if r.Slug == "acid" && r.Category == "gear" {
			acid = r.Data
		}
		if r.Slug == "thieves-tools" && r.Category == "tool" {
			thieves = r.Data
		}
	}
	if acid == nil || thieves == nil {
		t.Fatal("rows missing")
	}
	mech, _ := acid["mechanics"].(map[string]interface{})
	if mech == nil || mech["damage"] == nil || mech["saves"] == nil {
		t.Errorf("Acid mechanics: %v", acid)
	}
	ue, _ := thieves["utilize_entries"].([]map[string]interface{})
	if len(ue) != 2 || ue[0]["dc"] != 15 {
		t.Errorf("Thieves' Tools utilize entries: %v", thieves["utilize_entries"])
	}
}

func TestUtilizeDC(t *testing.T) {
	dc, ab, ok := srdUtilizeDC("thieves' tools", "pick a lock")
	if !ok || dc != 15 || ab != "dexterity" {
		t.Errorf("pick a lock: %d %q %v", dc, ab, ok)
	}
	if _, _, ok := srdUtilizeDC("thieves' tools", "bake bread"); ok {
		t.Error("unknown action must not match")
	}
}

func TestMagicItemTables(t *testing.T) {
	rows := magicItemRows()
	tables, stat := 0, 0
	var apparatus [][]interface{}
	for _, r := range rows {
		blocks, _ := r.Data["description_blocks"].([]interface{})
		for _, b := range blocks {
			if m, ok := b.(map[string]interface{}); ok {
				tables++
				if r.Name == "Apparatus of the Crab" {
					for _, row := range m["table"].(map[string]interface{})["rows"].([]interface{}) {
						apparatus = append(apparatus, row.([]interface{}))
					}
				}
			}
		}
		if r.Data["statblock"] != nil {
			stat++
		}
	}
	if len(rows) != 258 || tables != 42 || stat != 2 {
		t.Errorf("want 258 items, 42 tables, 2 stat blocks; got %d, %d, %d", len(rows), tables, stat)
	}
	if len(apparatus) != 11 || len(apparatus[0]) != 3 || apparatus[0][2] != "Down" || apparatus[10][0] != "10" {
		t.Errorf("Apparatus of the Crab levers: %v", apparatus)
	}
}
