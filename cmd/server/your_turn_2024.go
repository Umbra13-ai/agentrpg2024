package main

import "encoding/json"

// yourTurn: whether the character can act now, the one answer my-turn's is_my_turn and the heartbeat's turn_status.your_turn read (I-WREN-1:
// the heartbeat's own copy said false outside a fight without the turn schedule, while my-turn said true seconds apart).
//   - In a fight: the creature whose turn it is is the character, or a familiar of its own with its own Initiative.
//   - Outside one, with the turn schedule: the open round's answer (roundNoteFor's your_turn: not yet sent or resolved this round).
//   - Outside one, without it: the exploration beat (beatApplies): not when the character acted since the GM's last narration.
//   - Never while the GM holds the table before round 1, and never for the dead.
func yourTurn(lobbyID, charID int) bool {
	var dead bool
	if db.QueryRow(`SELECT COALESCE(is_dead, false) FROM characters WHERE id = $1`, charID).Scan(&dead) != nil || dead {
		return false
	}
	var idx int
	var raw []byte
	var active bool
	if db.QueryRow(`SELECT current_turn_index, turn_order, active FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&idx, &raw, &active) == nil && active {
		var entries []struct {
			ID int `json:"id"`
		}
		json.Unmarshal(raw, &entries)
		if idx < 0 || idx >= len(entries) {
			return false
		}
		cur := entries[idx].ID
		return cur == charID || (isCompanionCombatID(cur) && companionTurnOwner(cur) == charID)
	}
	if tableIsHeld(lobbyID) {
		return false
	}
	if note, err := roundNoteFor(lobbyID, charID); err == nil && note != nil {
		if open, ok := note["your_turn"].(bool); ok {
			return open
		}
	}
	if beatApplies(lobbyID) {
		if acted, _, _, _ := hasCharacterActedSinceLastNarration(charID, lobbyID); acted {
			return false
		}
	}
	return true
}
