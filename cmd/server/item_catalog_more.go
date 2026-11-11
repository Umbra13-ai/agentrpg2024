package main

// Magic items (SRD 5.2.1 "Magic Items A-Z"), mounts and vehicles for the item catalog.
// The magic items were extracted from the SRD PDF by items/extract_magic_items.py
// (research folder); the SRD is licensed CC-BY-4.0.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

//go:embed data/srd521_magic_items.json
var srd521MagicItemsJSON []byte

type srdMagicItem struct {
	Name               string `json:"name"`
	Type               string `json:"type"`
	Rarity             string `json:"rarity"`
	RarityText         string `json:"rarity_text"`
	RequiresAttunement bool   `json:"requires_attunement"`
	Attunement         string `json:"attunement"`
	Description        string `json:"description"`
	DescriptionBlocks  []interface{}   `json:"description_blocks"` // paragraphs and tables as printed
	Statblock          json.RawMessage `json:"statblock"`          // Figurine of Wondrous Power, Mysterious Deck
}

var attunementNotes map[string]string
var attunementNotesOnce sync.Once

// attunementRestriction says why a character can't attune to an item ("" when it can): the item's "by a Paladin", "by a Cleric or
// Paladin", "by a Spellcaster" or "by a Dwarf ..." note. A multiclass character qualifies through any of its classes.
func attunementRestriction(charID int, item string) string {
	attunementNotesOnce.Do(func() {
		attunementNotes = map[string]string{}
		var items []srdMagicItem
		if json.Unmarshal(srd521MagicItemsJSON, &items) == nil {
			for _, m := range items {
				if m.Attunement != "" {
					attunementNotes[strings.ToLower(m.Name)] = m.Attunement
				}
			}
		}
	})
	note := attunementNotes[strings.ToLower(strings.TrimSpace(item))]
	if note == "" {
		return ""
	}
	levels := classLevelsOf(charID)
	low := strings.ToLower(note)
	switch {
	case strings.Contains(low, "spellcaster"):
		for c := range levels {
			if game.IsPreparedCaster(c) {
				return ""
			}
		}
		return fmt.Sprintf("%s requires attunement %s: you have no spellcasting class.", item, note)
	case strings.Contains(low, "dwarf"):
		var species string
		db.QueryRow(`SELECT COALESCE(species,'') FROM characters WHERE id = $1`, charID).Scan(&species)
		if strings.EqualFold(species, "dwarf") || attunedToName(charID, "Belt of Dwarvenkind") {
			return ""
		}
		return fmt.Sprintf("%s requires attunement %s.", item, note)
	}
	for _, cls := range []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"} {
		if strings.Contains(low, cls) && levels[cls] > 0 {
			return ""
		}
	}
	return fmt.Sprintf("%s requires attunement %s.", item, note)
}

func attunedToName(charID int, item string) bool {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(attuned_items,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var list []string
	json.Unmarshal(raw, &list)
	for _, x := range list {
		if strings.EqualFold(x, item) {
			return true
		}
	}
	return false
}

func magicItemRows() []catalogRow {
	var items []srdMagicItem
	if err := json.Unmarshal(srd521MagicItemsJSON, &items); err != nil {
		log.Printf("magic items: %v", err)
		return nil
	}
	rows := make([]catalogRow, 0, len(items))
	for _, m := range items {
		data := map[string]interface{}{"item_type": m.Type, "rarity": m.Rarity, "rarity_text": m.RarityText,
			"requires_attunement": m.RequiresAttunement, "attunement_note": m.Attunement, "description": m.Description}
		if len(m.DescriptionBlocks) > 0 {
			data["description_blocks"] = m.DescriptionBlocks
		}
		if len(m.Statblock) > 0 && string(m.Statblock) != "null" {
			data["statblock"] = m.Statblock
		}
		rows = append(rows, catalogRow{Category: "magic", Slug: itemSlug(m.Name), Name: m.Name, Source: "SRD 5.2.1", CostGP: -1, WeightLb: -1, Data: data})
	}
	return rows
}

// mountAndVehicleRows are the SRD 5.2.1 Mounts and Other Animals, Tack and Drawn Vehicles, and Airborne and Waterborne Vehicles tables.
func mountAndVehicleRows() []catalogRow {
	var rows []catalogRow
	mount := func(name string, capacity, gp float64, text string) {
		rows = append(rows, catalogRow{Category: "mount", Slug: itemSlug(name), Name: name, Source: "SRD 5.2.1", CostGP: gp, CostText: text, WeightLb: -1,
			Data: map[string]interface{}{"carrying_capacity_lb": capacity, "note": "An animal pulling a vehicle can move up to five times its carrying capacity, including the vehicle's weight."}})
	}
	mount("Camel", 450, 50, "50 GP")
	mount("Elephant", 1320, 200, "200 GP")
	mount("Draft Horse", 540, 50, "50 GP")
	mount("Riding Horse", 480, 75, "75 GP")
	mount("Mastiff", 195, 25, "25 GP")
	mount("Mule", 420, 8, "8 GP")
	mount("Pony", 225, 30, "30 GP")
	mount("Warhorse", 540, 400, "400 GP")

	tack := func(name string, weight, gp float64, text string) {
		rows = append(rows, catalogRow{Category: "vehicle", Slug: itemSlug(name), Name: name, Source: "SRD 5.2.1", CostGP: gp, CostText: text, WeightLb: weight,
			Data: map[string]interface{}{"kind": "tack, harness or drawn vehicle"}})
	}
	tack("Carriage", 600, 100, "100 GP")
	tack("Cart", 200, 15, "15 GP")
	tack("Chariot", 100, 250, "250 GP")
	tack("Feed (per day)", 10, 0.05, "5 CP")
	tack("Saddle, Exotic", 40, 60, "60 GP")
	tack("Saddle, Military", 30, 20, "20 GP")
	tack("Saddle, Riding", 25, 10, "10 GP")
	tack("Sled", 300, 20, "20 GP")
	tack("Stabling (per day)", -1, 0.5, "5 SP")
	tack("Wagon", 400, 35, "35 GP")

	ship := func(name, speed string, crew int, passengers, cargo string, ac, hp int, threshold string, gp float64, text string) {
		rows = append(rows, catalogRow{Category: "vehicle", Slug: itemSlug(name), Name: name, Source: "SRD 5.2.1", CostGP: gp, CostText: text, WeightLb: -1,
			Data: map[string]interface{}{"kind": "airborne or waterborne vehicle", "speed": speed, "crew": crew, "passengers": passengers,
				"cargo_tons": cargo, "armor_class": ac, "hit_points": hp, "damage_threshold": threshold}})
	}
	ship("Airship", "8 mph", 10, "20", "1", 13, 300, "--", 40000, "40,000 GP")
	ship("Galley", "4 mph", 80, "--", "150", 15, 500, "20", 30000, "30,000 GP")
	ship("Keelboat", "1 mph", 1, "6", "1/2", 15, 100, "10", 3000, "3,000 GP")
	ship("Longship", "3 mph", 40, "150", "10", 15, 300, "15", 10000, "10,000 GP")
	ship("Rowboat", "1 1/2 mph", 1, "3", "--", 11, 50, "--", 50, "50 GP")
	ship("Sailing Ship", "2 mph", 20, "20", "100", 15, 300, "15", 10000, "10,000 GP")
	ship("Warship", "2 1/2 mph", 60, "60", "200", 15, 500, "20", 25000, "25,000 GP")
	return rows
}
