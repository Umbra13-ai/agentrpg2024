package main

// 2024 spell preparation (SRD 5.2.1): every spellcaster prepares spells, as many level 1+ spells as the Prepared Spells column of its
// class's Features table, plus the cantrips of the Cantrips column. What changes is when the list may change:
//   Cleric, Druid, Wizard: any number of spells after a Long Rest (a Wizard from the spellbook).
//   Paladin, Ranger: one spell after a Long Rest.
//   Bard, Sorcerer, Warlock: one spell when the character gains a level in the class.
//   Cantrips (Bard, Cleric, Druid, Sorcerer, Warlock, Wizard): one cantrip when the character gains a level in the class.
// Filling empty places (a new level gives more prepared spells) is always allowed; replacing a spell uses up one of the changes. The GM
// can grant changes with POST /api/gm/update-character {"set": {"spell_swaps": 1, "cantrip_swaps": 1, "prepare_ready": true}}.
// Bard, Ranger, Sorcerer and Warlock lists are kept in known_spells as before (the cast code reads them) and mirrored in prepared_spells.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var swapOnLevelUp = map[string]bool{"bard": true, "sorcerer": true, "warlock": true}
var swapOneOnLongRest = map[string]bool{"paladin": true, "ranger": true}
var changeAllOnLongRest = map[string]bool{"cleric": true, "druid": true, "wizard": true}
var cantripSwapClasses = map[string]bool{"bard": true, "cleric": true, "druid": true, "sorcerer": true, "warlock": true, "wizard": true}

// listInKnownSpells: the classes whose prepared list lives in known_spells (a Wizard's known_spells is the spellbook).
var listInKnownSpells = map[string]bool{"bard": true, "ranger": true, "sorcerer": true, "warlock": true}

func ensurePrepColumns() {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS spell_swaps INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS cantrip_swaps INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS prepare_ready BOOLEAN DEFAULT false`)
}

func splitByLevel(list []string) (cantrips, leveled map[string]bool) {
	cantrips, leveled = map[string]bool{}, map[string]bool{}
	for _, s := range list {
		if sp, ok := srdSpellsMemory[s]; ok && sp.Level == 0 {
			cantrips[s] = true
		} else {
			leveled[s] = true
		}
	}
	return
}

// checkSpellListChange checks a new prepared list against the 2024 limits and when-to-change rules. It returns an error message, or ""
// and a function that records the changes used (call it after saving).
func checkSpellListChange(charID int, class string, level int, oldList, newList []string) (string, func()) {
	ensurePrepColumns()
	c := strings.ToLower(class)
	oldC, oldL := splitByLevel(oldList)
	newC, newL := splitByLevel(newList)
	if max := game.PreparedSpells2024(c, level); max > 0 && len(newL) > max {
		return fmt.Sprintf("A level %d %s prepares %d spells of level 1 or higher (the Prepared Spells column of the class table); this list has %d.", level, class, max, len(newL)), nil
	}
	if max := game.ClassTableInt(c, level, "Cantrips") + orderExtraCantrips(charID); max > 0 && len(newC) > max {
		return fmt.Sprintf("A level %d %s knows %d cantrips; this list has %d.", level, class, max, len(newC)), nil
	}
	if open, _ := shopOpen(charID); open {
		// Before the campaign starts the character is still being made: any list within the limits is fine (Nov 13: a tester had
		// to clear the old choices before choosing new ones)
		return "", func() {}
	}
	removedL, removedC := 0, 0
	for s := range oldL {
		if !newL[s] {
			removedL++
		}
	}
	for s := range oldC {
		if !newC[s] {
			removedC++
		}
	}
	var swaps, cswaps int
	var ready bool
	db.QueryRow(`SELECT COALESCE(spell_swaps,0), COALESCE(cantrip_swaps,0), COALESCE(prepare_ready,false) FROM characters WHERE id = $1`, charID).Scan(&swaps, &cswaps, &ready)
	if changeAllOnLongRest[c] && ready {
		removedL = 0 // after a Long Rest a Cleric, Druid or Wizard may prepare a new list
	}
	if removedL > swaps {
		rule := map[bool]string{true: "a Cleric, Druid or Wizard changes prepared spells after a Long Rest"}[changeAllOnLongRest[c]]
		switch {
		case swapOneOnLongRest[c]:
			rule = "a Paladin or Ranger replaces one prepared spell after a Long Rest"
		case swapOnLevelUp[c]:
			rule = "a Bard, Sorcerer or Warlock replaces one prepared spell when gaining a level"
		}
		return fmt.Sprintf("This replaces %d prepared spell(s), and you can replace %d now (%s). You can always fill empty places; the GM can allow more changes.", removedL, swaps, rule), nil
	}
	if removedC > cswaps {
		return fmt.Sprintf("This replaces %d cantrip(s), and you can replace %d now (one cantrip when you gain a level). The GM can allow more changes.", removedC, cswaps), nil
	}
	return "", func() {
		useL := removedL
		if changeAllOnLongRest[c] && ready {
			useL = 0
		}
		db.Exec(`UPDATE characters SET spell_swaps = GREATEST(0, COALESCE(spell_swaps,0) - $1), cantrip_swaps = GREATEST(0, COALESCE(cantrip_swaps,0) - $2) WHERE id = $3`, useL, removedC, charID)
	}
}

// grantSpellChangesOnLevelUp: a Bard, Sorcerer or Warlock may replace one prepared spell per level gained; casters with cantrips one cantrip.
func grantSpellChangesOnLevelUp(charID int, class string, levels int) {
	if levels <= 0 {
		return
	}
	ensurePrepColumns()
	c := strings.ToLower(class)
	if swapOnLevelUp[c] {
		db.Exec(`UPDATE characters SET spell_swaps = COALESCE(spell_swaps,0) + $1 WHERE id = $2`, levels, charID)
	}
	if cantripSwapClasses[c] {
		db.Exec(`UPDATE characters SET cantrip_swaps = COALESCE(cantrip_swaps,0) + $1 WHERE id = $2`, levels, charID)
	}
}

// grantSpellChangesOnLongRest: a Cleric, Druid or Wizard may prepare a new list; a Paladin or Ranger may replace one spell.
func grantSpellChangesOnLongRest(charID int, class string) {
	ensurePrepColumns()
	c := strings.ToLower(class)
	switch {
	case changeAllOnLongRest[c]:
		db.Exec(`UPDATE characters SET prepare_ready = true WHERE id = $1`, charID)
	case swapOneOnLongRest[c]:
		db.Exec(`UPDATE characters SET spell_swaps = GREATEST(COALESCE(spell_swaps,0), 1) WHERE id = $1`, charID)
	}
}

// backfillPreparedLists: a Bard's, Ranger's, Sorcerer's or Warlock's list was only in known_spells; mirror it in prepared_spells once.
func backfillPreparedLists() {
	if db == nil {
		return
	}
	ensurePrepColumns()
	db.Exec(`UPDATE characters SET prepared_spells = known_spells WHERE lower(class) IN ('bard','ranger','sorcerer','warlock')
		AND COALESCE(jsonb_array_length(prepared_spells),0) = 0 AND COALESCE(jsonb_array_length(known_spells),0) > 0`)
}

// currentPreparedList is the character's prepared list (known_spells for Bard, Ranger, Sorcerer, Warlock).
func currentPreparedList(charID int, class string) []string {
	col := "prepared_spells"
	if listInKnownSpells[strings.ToLower(class)] {
		col = "known_spells"
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(`+col+`,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var out []string
	json.Unmarshal(raw, &out)
	return out
}

// ---- one spell with a spell slot per turn (SRD 5.2.1 Spellcasting): on a turn (yours, or another creature's for a Reaction) a
// creature expends at most one spell slot to cast a spell. Cantrips, rituals and free casts don't count. Only tracked in combat.

// combatTurnMarker names the current turn of a fight: round, turn index and when the turn started ("" outside combat). Round and
// index alone repeat in the next fight, so "once per turn" records from one fight blocked the same turn of the next.
func combatTurnMarker(lobbyID int) string {
	var round, idx int
	var active bool
	var started interface{}
	if db.QueryRow(`SELECT COALESCE(active,false), COALESCE(round_number,0), COALESCE(current_turn_index,0), turn_started_at FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&active, &round, &idx, &started) != nil || !active {
		return ""
	}
	return fmt.Sprintf("%d:%d:%v", round, idx, started)
}

func slotTurnMarker(charID int) string {
	lobby := lobbyOfChar(charID)
	if m := combatTurnMarker(lobby); m != "" {
		return fmt.Sprintf("%d:%s", lobby, m)
	}
	return ""
}

// slotAlreadyUsedThisTurn returns why a spell slot can't be spent now ("" when it can).
func slotAlreadyUsedThisTurn(charID int, spellName string) string {
	m := slotTurnMarker(charID)
	if m == "" {
		return ""
	}
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS slot_spent_turn TEXT DEFAULT ''`)
	var last string
	db.QueryRow(`SELECT COALESCE(slot_spent_turn,'') FROM characters WHERE id = $1`, charID).Scan(&last)
	if last == m {
		return fmt.Sprintf("Cannot cast %s with a spell slot: you already expended a spell slot this turn (one spell with a spell slot per turn). A cantrip, a ritual or a cast that needs no slot is still fine.", spellName)
	}
	return ""
}

func markSlotUsedThisTurn(charID int) {
	if m := slotTurnMarker(charID); m != "" {
		db.Exec(`UPDATE characters SET slot_spent_turn = $1 WHERE id = $2`, m, charID)
	}
}

// countLeveled counts the spells of level 1 or higher in a list (cantrips have their own limit).
func countLeveled(list []string) int {
	_, leveled := splitByLevel(list)
	return len(leveled)
}

// withoutAlwaysPrepared drops the always-prepared spells (subclass spells) from a list: they don't count against the limit.
func withoutAlwaysPrepared(list, always []string) []string {
	skip := map[string]bool{}
	for _, a := range always {
		skip[a] = true
	}
	var out []string
	for _, s := range list {
		if !skip[s] {
			out = append(out, s)
		}
	}
	return out
}
