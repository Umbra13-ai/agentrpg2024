package main

// Fixes from the C7 coverage session (level20-battle-tests/s39_c7.py, Nov 9 2026; FINDINGS F186-F189):
//	Cloak of Invisibility  worn and attuned, a Magic action and 1 charge: Invisible for 1 hour; "hood down" (no action) ends it (F187)
//	Ring of Invisibility   worn and attuned, a Magic action: Invisible until the ring comes off; use it again (a Bonus Action) to appear (F188)
//	Item names             use_item, equipment and the other item lookups accept a name without its parenthetical ("Horn of Valhalla" for
//	                       "Horn of Valhalla (Iron)") (F189, character_equipment.go findItem)

import (
	"strings"
)

func endInvisibleFrom(lobby, charID int, source string) {
	db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'invisible' AND source = $3`, lobby, charID, source)
	syncListFromRecords(charID)
}

func invisibleFrom(lobby, charID int, source string) bool {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'invisible' AND source = $3`, lobby, charID, source).Scan(&n)
	return n > 0
}

func cloakOfInvisibility(charID int, it invItem, description string) string {
	if _, ok := itemActive(charID, "cloak of invisibility", "worn"); !ok {
		return "Cannot use the Cloak of Invisibility: wear it and attune to it first. Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if invisibleFrom(lobby, charID, "Cloak of Invisibility") || strings.Contains(strings.ToLower(description), "down") {
		endInvisibleFrom(lobby, charID, "Cloak of Invisibility")
		return "You pull the hood down (no action): you are visible again."
	}
	note, refusal := chargedUse(charID, it, 1)
	if refusal != "" {
		return refusal + " Nothing was spent."
	}
	addMonsterCondition(lobby, charID, "invisible", "Cloak of Invisibility", 600, "", 0)
	return "You pull the hood over your head (a Magic action, 1 charge): you have the Invisible condition for 1 hour, until you pull the hood down (no action) or stop wearing the cloak." + note
}

func ringOfInvisibility(charID int, it invItem) string {
	if _, ok := itemActive(charID, "ring of invisibility", "worn"); !ok {
		return "Cannot use the Ring of Invisibility: wear it and attune to it first. Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if invisibleFrom(lobby, charID, "Ring of Invisibility") {
		endInvisibleFrom(lobby, charID, "Ring of Invisibility")
		return "You become visible again (a Bonus Action)."
	}
	addMonsterCondition(lobby, charID, "invisible", "Ring of Invisibility", 0, "", 0)
	return "You turn the ring (a Magic action): you have the Invisible condition until you remove the ring or take a Bonus Action to become visible again (use_item again)."
}
