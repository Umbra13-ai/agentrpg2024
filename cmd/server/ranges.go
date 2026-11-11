package main

// Melee range. The game has no battle map, so it does not track distances.
// Instead each character has an "in melee range" state: true when an enemy is
// within 5 feet of them. While it is true, a ranged weapon attack is made with
// Disadvantage (2024 "Ranged Attacks in Close Combat"; Crossbow Expert ignores it).
// The state is set when the character makes a melee attack (they are next to the
// target), cleared when they take the Disengage action or combat starts or ends,
// and the GM can set or clear it:
//	POST /api/gm/melee-range   {"character_id": 12, "in_melee": true}

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var actionContextRE = regexp.MustCompile(`\[ctx close_range=(\d)\]`)

// withActionContext appends the explicit close_range request flag to the
// description that resolveAction sees. The original description is what gets recorded.
func withActionContext(desc string, closeRange bool) string {
	if !closeRange {
		return desc
	}
	return fmt.Sprintf("%s [ctx close_range=1]", desc)
}

// parseActionContext reads the flag added by withActionContext.
func parseActionContext(desc string) (closeRange bool) {
	m := actionContextRE.FindStringSubmatch(desc)
	return m != nil && m[1] == "1"
}

// usedAsRanged reports whether the attack is a ranged attack: a ranged weapon,
// or a thrown weapon the player says they are throwing.
func usedAsRanged(w SRDWeapon, description string) bool {
	if w.Type == "ranged" {
		return true
	}
	d := strings.ToLower(description)
	return hasProperty(w.Properties, "thrown") && (strings.Contains(d, "throw") || strings.Contains(d, "hurl"))
}

// meleeRangeEffects says whether being in melee range gives a ranged attack
// Disadvantage, with a short note for the result.
func meleeRangeEffects(w SRDWeapon, description string, inMelee, crossbowExpert bool) (disadvantage bool, note string) {
	if !inMelee || !usedAsRanged(w, description) {
		return false, ""
	}
	if crossbowExpert {
		return false, " (Crossbow Expert: no penalty for an enemy within 5 feet)"
	}
	return true, " (ranged attack while an enemy is within 5 feet: disadvantage)"
}

// isInMeleeRange reads the character's melee range state.
func isInMeleeRange(charID int) bool {
	var in bool
	db.QueryRow(`SELECT COALESCE(in_melee_range, false) FROM characters WHERE id = $1`, charID).Scan(&in)
	return in
}

// enemyWithinFive: is an enemy within 5 feet of the character (for a ranged attack's Disadvantage)? With a battle map and the
// character's token on it, the map decides (a melee range flag left over from earlier doesn't count); without one, the
// close_range flag of the request or the character's melee range state.
func enemyWithinFive(lobbyID, charID int, description string) bool {
	if m, ok := activeEncounterMap(lobbyID); ok {
		if _, onMap := m.token(charID); onMap {
			return enemyAdjacentOnMap(lobbyID, charID)
		}
	}
	return parseActionContext(description) || isInMeleeRange(charID)
}

func setInMeleeRange(charID int, in bool) {
	db.Exec(`UPDATE characters SET in_melee_range = $1 WHERE id = $2`, in, charID)
}

// isMeleeAttackAction reports whether an attack was a melee attack: unarmed, or
// a melee weapon that is not being thrown.
func isMeleeAttackAction(charID int, description string) bool {
	key := attackWeaponKey(charID, description)
	if key == "" {
		return true // unarmed strike
	}
	w, ok := srdWeapons[key]
	return ok && !usedAsRanged(w, description)
}

// attackWeaponKey is the weapon an attack will use: the one named in the
// description, otherwise the equipped main hand.
func attackWeaponKey(charID int, description string) string {
	if k := parseWeaponFromDescription(description); k != "" {
		return k
	}
	main, _ := equippedWeapons(charID)
	if main != "" {
		return parseWeaponFromDescription(main)
	}
	return ""
}

// handleGMMeleeRange lets the GM say whether a character has an enemy within 5 feet.
// @Summary Say whether a character has an enemy within 5 feet
// @Description Without a battle map the server can't measure distance: the GM says whether the character is in melee range (a ranged attack then has Disadvantage).
// @Tags GM
// @Param request body object{character_id=int,in_melee=bool} true "Request"
// @Router /gm/melee-range [post]
func handleGMMeleeRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int  `json:"character_id"`
		InMelee     bool `json:"in_melee"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required"})
		return
	}
	var dm sql.NullInt64
	if err := db.QueryRow(`SELECT l.dm_id FROM characters c LEFT JOIN lobbies l ON c.lobby_id = l.id WHERE c.id = $1`, req.CharacterID).Scan(&dm); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if !dm.Valid || int(dm.Int64) != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "only_the_gm_can_set_melee_range"})
		return
	}
	setInMeleeRange(req.CharacterID, req.InMelee)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "character_id": req.CharacterID, "in_melee_range": req.InMelee})
}
