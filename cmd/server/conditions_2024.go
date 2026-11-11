package main

import (
	"encoding/json"
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

// attackTargetConditions are the conditions of the monster a player is attacking (none for other targets).
func attackTargetConditions(charID int) []string {
	lobbyID, id, ok := attackedMonster(charID)
	if !ok {
		return []string{}
	}
	return withoutInvisible(lobbyID, id, append([]string{}, conditionNames(lobbyID, id)...)) // Faerie Fire
}

// monsterAutoCritReason: an attack roll that hits a Paralyzed or Unconscious creature is a Critical Hit
// when the attacker is within 5 feet, which every melee attack is.
func monsterAutoCritReason(charID int, ranged bool) string {
	if ranged {
		return ""
	}
	for _, c := range attackTargetConditions(charID) {
		if c == game.ConditionParalyzed || c == game.ConditionUnconscious {
			return c
		}
	}
	return ""
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

// attackerWithinFiveFeetOfTarget decides which way a Prone target affects an attack roll. With a battle
// map it is the real distance between the two tokens. Without one, a melee attack is within 5 feet by
// definition, and a ranged attack is only if the attacker has said an enemy is within 5 feet of them
// (close_range, or the melee range state), taken to be the target.
func attackerWithinFiveFeetOfTarget(charID int, ranged bool) bool {
	if within, known := mapDistanceWithinFive(charID); known {
		return within
	}
	if !ranged {
		return true
	}
	return isInMeleeRange(charID)
}

// mapDistanceWithinFive reads the encounter's battle map. known is false without a map or a token for either side.
func mapDistanceWithinFive(charID int) (within, known bool) {
	v, found := attackTargets.Load(charID)
	if !found || db == nil {
		return false, false
	}
	target, _ := v.(int)
	var lobbyID int
	if db.QueryRow(`SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, charID).Scan(&lobbyID) != nil || lobbyID == 0 {
		return false, false
	}
	var raw []byte
	if db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw) != nil || len(raw) == 0 {
		return false, false
	}
	var m encounterMap
	if json.Unmarshal(raw, &m) != nil {
		return false, false
	}
	var a, b *mapToken
	for i := range m.Tokens {
		switch m.Tokens[i].ID {
		case charID:
			a = &m.Tokens[i]
		case target:
			b = &m.Tokens[i]
		}
	}
	if a == nil || b == nil {
		return false, false
	}
	return tokensAdjacent(*a, *b), true
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
