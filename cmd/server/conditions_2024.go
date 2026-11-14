package main

import (
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// exhaustionLevelOf is the character's Exhaustion level (0 when unknown).
func exhaustionLevelOf(charID int) int {
	var level int
	if db == nil || db.QueryRow("SELECT COALESCE(exhaustion_level, 0) FROM characters WHERE id = $1", charID).Scan(&level) != nil {
		return 0
	}
	return level
}

var _ = game.ExhaustionD20Penalty

// attackTargets remembers the target of a player's attack for the length of the action, so the roll
// can use the target's conditions (character id -> target id; monsters have negative ids).
var attackTargets sync.Map

func attackedMonster(charID int) (lobbyID, monsterID int, ok bool) {
	v, found := attackTargets.Load(charID)
	if !found || db == nil {
		return 0, 0, false
	}
	id, _ := v.(int)
	if id >= 0 {
		return 0, 0, false
	}
	if db.QueryRow(`SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, charID).Scan(&lobbyID) != nil || lobbyID == 0 {
		return 0, 0, false
	}
	return lobbyID, id, true
}

// conditionNames lists the conditions on a target that everyone can see.
func conditionNames(lobbyID, targetID int) []string {
	var names []string
	seen := map[string]bool{}
	for _, r := range loadConditionRecords(lobbyID, targetID) {
		if !seen[r.Condition] {
			seen[r.Condition] = true
			names = append(names, r.Condition)
		}
	}
	return names
}

// monsterConditionViews is the GM's view of a monster's conditions (source, DCs and notes included).
func monsterConditionViews(lobbyID, monsterID int) []map[string]interface{} {
	if db == nil {
		return nil
	}
	var out []map[string]interface{}
	for _, r := range loadConditionRecords(lobbyID, monsterID) {
		out = append(out, conditionView(r, 0, true))
	}
	return out
}

// tokensAdjacent: some square of one token is within 5 feet (one square, diagonals included) of some square of the other.
func tokensAdjacent(a, b mapToken) bool {
	for _, p := range a.footprint(a.Position) {
		for _, q := range b.footprint(b.Position) {
			if mapAbs(p.X-q.X) <= 1 && mapAbs(p.Y-q.Y) <= 1 {
				return true
			}
		}
	}
	return false
}
