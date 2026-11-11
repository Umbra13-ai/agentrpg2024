package main

// Keeps each character's weapon, armor and tool proficiencies in step with
// their class, multiclassing, subclass and species. Only ever adds entries.

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/agentrpg/agentrpg/game"
)

// syncCharacterProficiencies adds any missing granted proficiencies.
// It returns true if the character's row changed.
func syncCharacterProficiencies(charID int) bool {
	var class, species, subclass, weapons, armor, tools, divine, primal string
	var classLevelsRaw []byte
	err := db.QueryRow(`
		SELECT COALESCE(class, ''), COALESCE(species, ''), COALESCE(subclass, ''), COALESCE(class_levels, '{}'),
			COALESCE(weapon_proficiencies, ''), COALESCE(armor_proficiencies, ''), COALESCE(tool_proficiencies, ''),
			COALESCE(divine_order, ''), COALESCE(primal_order, '')
		FROM characters WHERE id = $1`, charID).
		Scan(&class, &species, &subclass, &classLevelsRaw, &weapons, &armor, &tools, &divine, &primal)
	if err != nil {
		return false
	}
	var levels map[string]int
	json.Unmarshal(classLevelsRaw, &levels)

	p := game.ProficienciesFor(game.CharacterInput{FirstClass: class, ClassLevels: levels, DivineOrder: divine, PrimalOrder: primal})
	nw := game.MergeProficiencyList(weapons, p.Weapons)
	na := game.MergeProficiencyList(armor, p.Armor)
	nt := game.MergeToolList(tools, p.Tools) // also stores older short tool names under their full names ("gaming set (dice)")
	if nw == weapons && na == armor && nt == tools {
		return false
	}
	if _, err := db.Exec(`UPDATE characters SET weapon_proficiencies = $1, armor_proficiencies = $2, tool_proficiencies = $3 WHERE id = $4`,
		nw, na, nt, charID); err != nil {
		log.Printf("proficiency sync for character %d: %v", charID, err)
		return false
	}
	return true
}

// backfillProficiencies fixes characters created before proficiencies were
// granted automatically. Safe to run on every start.
func backfillProficiencies() {
	rows, err := db.Query(`SELECT id FROM characters`)
	if err != nil {
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	changed := 0
	for _, id := range ids {
		if syncCharacterProficiencies(id) {
			changed++
		}
	}
	if changed > 0 {
		log.Printf("Granted missing class proficiencies to %d characters", changed)
	}
}

// withProficiencySync runs a handler, then re-syncs the proficiencies of the
// calling agent's characters (a subclass or multiclass choice may grant more).
func withProficiencySync(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
		agentID, err := getAgentFromAuth(r)
		if err != nil {
			return
		}
		rows, err := db.Query(`SELECT id FROM characters WHERE agent_id = $1`, agentID)
		if err != nil {
			return
		}
		var ids []int
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			syncCharacterProficiencies(id)
		}
	}
}
