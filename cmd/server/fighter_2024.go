package main

// Fighter, 2024 rules (SRD 5.2.1).
//	Second Wind     2 uses (3 from level 4, 4 from level 10); a Short Rest returns one use. Tactical Shift (level 5): move
//	                up to half your Speed without Opportunity Attacks (stated in the result).
//	Action Surge    one additional action (not the Magic action), once per turn.
//	Tactical Mind   POST /api/gm/tactical-mind: after a failed ability check, spend a Second Wind use for +1d10.
//	Indomitable     the reroll of a failed save gets a bonus equal to the Fighter level (see /api/gm/indomitable).
//	Tactical Master "tactical master push|sap|slow" in an attack replaces the weapon's mastery property for that attack.
//	Studied Attacks a missed attack gives Advantage on the next attack against that creature until the end of your next turn.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// handleGMTacticalMind: POST /api/gm/tactical-mind {"character_id": N, "total": 11, "dc": 15}.
// The check already failed (total < dc). The Fighter rolls 1d10 and adds it; if the check now succeeds the Second Wind use
// is spent, otherwise it is not.
// @Summary Fighter Tactical Mind (level 2)
// @Description After a failed ability check (total below dc) the Fighter may spend a use of Second Wind to roll 1d10 and add it; the use is spent only if the check then succeeds.
// @Tags GM
// @Param request body object{character_id=int,total=int,dc=int} true "Request"
// @Router /gm/tactical-mind [post]
func handleGMTacticalMind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CharacterID int `json:"character_id"`
		Total       int `json:"total"`
		DC          int `json:"dc"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.CharacterID == 0 || req.DC <= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send character_id, total (the failed check's total) and dc."})
		return
	}
	var name, class string
	var level, lobby int
	var usedJSON []byte
	if db.QueryRow(`SELECT name, class, level, COALESCE(lobby_id,0), COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, req.CharacterID).
		Scan(&name, &class, &level, &lobby, &usedJSON) != nil || lobby != campaignID {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if strings.ToLower(class) != "fighter" || level < 2 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_tactical_mind", "message": name + " is not a Fighter of level 2 or higher."})
		return
	}
	if req.Total >= req.DC {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "check_not_failed", "message": "Tactical Mind is used after a failed ability check."})
		return
	}
	used := map[string]int{}
	json.Unmarshal(usedJSON, &used)
	max := classResourceMax(req.CharacterID, class, level, "second_wind", 0)
	if used["second_wind"] >= max {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_second_wind_left", "message": name + " has no Second Wind uses left."})
		return
	}
	roll := game.RollDie(10)
	total := req.Total + roll
	success := total >= req.DC
	if success {
		used["second_wind"]++
		b, _ := json.Marshal(used)
		db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, string(b), req.CharacterID)
	}
	note := "The check still fails, so the Second Wind use is not expended."
	if success {
		note = "The check succeeds; one use of Second Wind is expended."
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": success, "character": name, "d10": roll, "total": total, "dc": req.DC,
		"second_wind_left": max - used["second_wind"], "note": note})
}

// tacticalMasterProperty returns the mastery a Fighter of level 9+ swaps in with "tactical master push|sap|slow".
func tacticalMasterProperty(charID int, desc string) string {
	low := strings.ToLower(desc)
	if !strings.Contains(low, "tactical master") {
		return ""
	}
	var class string
	var level int
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) != nil || strings.ToLower(class) != "fighter" || level < 9 {
		return ""
	}
	for _, p := range []string{"Push", "Sap", "Slow"} {
		if strings.Contains(low, strings.ToLower(p)) {
			return p
		}
	}
	return ""
}

// studiedAttacks gives a Fighter of level 13+ Advantage on the next attack against a creature they just missed.
func studiedAttacks(lobbyID, charID, targetID int) string {
	var class string
	var level int
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) != nil || strings.ToLower(class) != "fighter" || level < 13 {
		return ""
	}
	vexMarks.Store(charID, vexMark{Lobby: lobbyID, Target: targetID})
	return " Studied Attacks: you have Advantage on your next attack roll against it before the end of your next turn."
}

// championDefyDeath: a Champion of level 18 (Survivor: Defy Death) has Advantage on Death Saving Throws, and a roll of
// 18-20 counts as a 20.
func championDefyDeath(charID int) bool {
	var class, sub string
	var level int
	if db.QueryRow(`SELECT class, COALESCE(subclass,''), level FROM characters WHERE id = $1`, charID).Scan(&class, &sub, &level) != nil {
		return false
	}
	return strings.ToLower(class) == "fighter" && strings.ToLower(sub) == "champion" && level >= 18
}
