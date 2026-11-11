package main

// Shield of Missile Attraction (SRD 5.2.1; F222, Nov 10). "While holding this Shield, you have Resistance to damage from attacks made
// with Ranged weapons." Curse: attuning to it curses you until you are targeted by Remove Curse or similar magic; removing the Shield
// doesn't end the curse; "Whenever an attack with a Ranged weapon targets a creature within 10 feet of you, the curse causes you to
// become the target instead." The curse is a record in the curses table (GET /api/gm/curse lists it; Remove Curse and Greater
// Restoration end it, toolbox_more_2024.go). The redirect needs the battle map to know who is within 10 feet.

import (
	"fmt"
	"strings"
)

const missileAttractionSource = "Shield of Missile Attraction"

// attuneCurse: attuning to a cursed item puts its curse on the character (a note for the attune reply).
func attuneCurse(charID int, item string) string {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(item)), "shield of missile attraction") {
		return ""
	}
	ensureMoreTables()
	lobby := lobbyOfChar(charID)
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM curses WHERE lobby_id = $1 AND target_id = $2 AND source = $3`, lobby, charID, missileAttractionSource).Scan(&n)
	if n == 0 {
		db.Exec(`INSERT INTO curses (lobby_id, target_id, description, source) VALUES ($1,$2,$3,$4)`, lobby, charID,
			"Whenever an attack with a Ranged weapon targets a creature within 10 feet of you, you become the target instead. Removing the Shield doesn't end it; Remove Curse does.",
			missileAttractionSource)
	}
	return " Curse: the Shield of Missile Attraction curses you. Whenever an attack with a Ranged weapon targets a creature within 10 feet of you, you become the target instead. Taking the Shield off doesn't end it; Remove Curse does."
}

// missileShieldResists: holding the Shield (attuned): Resistance to damage from attacks made with Ranged weapons.
func missileShieldResists(charID int) bool {
	if !shieldInHand(charID) {
		return false
	}
	_, ok := itemActive(charID, "shield of missile attraction", "held")
	if !ok {
		_, ok = attunedTo(charID, "shield of missile attraction")
		if ok {
			ok = carriedItem(charID, "shield of missile attraction") != nil
		}
	}
	return ok
}

// missileAttractionRedirect: a cursed character within 10 feet of the target (other than the target) becomes the target of a Ranged
// weapon attack. It returns that character and a note, or 0.
func missileAttractionRedirect(lobbyID, targetID int) (int, string) {
	ensureMoreTables()
	rows, err := db.Query(`SELECT DISTINCT cu.target_id FROM curses cu JOIN characters c ON c.id = cu.target_id
		WHERE cu.lobby_id = $1 AND cu.source = $2 AND cu.target_id > 0 AND cu.target_id <> $3 AND c.hp > 0 AND NOT COALESCE(c.is_dead,false)
		ORDER BY cu.target_id`, lobbyID, missileAttractionSource, targetID)
	if err != nil {
		return 0, ""
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		rows.Scan(&id)
		if d, known := charDistanceFeet(lobbyID, id, targetID); known && d <= 10 {
			return id, fmt.Sprintf(" The Shield of Missile Attraction's curse pulls the shot: %s (%d ft from %s) becomes the target.", getCharacterName(id), d, getCharacterName(targetID))
		}
	}
	return 0, ""
}
