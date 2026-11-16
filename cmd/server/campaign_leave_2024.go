package main

// Leaving a campaign (Kestrel's B1, feedback log 57a: Matt, Nov 16: yes). leaveCampaign is the one way a character leaves its campaign:
// the player's own leave (POST /api/campaigns/{id}/leave) and the GM's removal (POST /api/gm/kick-character) call it. The character
// comes out of a fight (the combat-state owner), its open pending turns are withdrawn and its lobby_id is cleared, which frees its seat
// (a seat is held only while its character is in the campaign: claimSeat). The character is never erased: it stays its player's.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
)

// leaveCampaign takes the character out of the campaign; false when it wasn't in it.
func leaveCampaign(lobby, charID int, why string) (bool, error) {
	if c, fighting := ptLoadCombat(lobby); fighting && c.slotOf(charID) >= 0 {
		removeFromFight(lobby, charID, why) // the combat-state owner (combat_state_2024.go)
	}
	if _, err := db.Exec(`UPDATE pending_turns SET status = 'withdrawn', note = 'The character left the campaign.', resolved_at = NOW()
		WHERE lobby_id = $1 AND character_id = $2 AND status IN ('pending', 'needs_gm')`, lobby, charID); err != nil {
		return false, err
	}
	res, err := db.Exec(`UPDATE characters SET lobby_id = NULL WHERE id = $1 AND lobby_id = $2`, charID, lobby)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// handleCampaignLeave godoc
// @Summary Leave a campaign
// @Description Your character leaves the campaign, before or after its start: its seat is freed, it comes out of a fight and any turn you sent and the GM hasn't resolved is withdrawn. The character isn't erased: it stays yours, to join another campaign. Name the character with character_id if you have more than one in the campaign.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Body The character that leaves (optional: your character in the campaign)
// @Success 200 {object} map[string]interface{} "The character has left"
// @Router /campaigns/{id}/leave [post]
func handleCampaignLeave(w http.ResponseWriter, r *http.Request, lobby int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "POST."})
		return
	}
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int `json:"character_id"` // the character that leaves (optional when you have one in the campaign)
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	var charID int
	var name string
	err = db.QueryRow(`SELECT id, name FROM characters WHERE agent_id = $1 AND lobby_id = $2 AND ($3 = 0 OR id = $3) ORDER BY id LIMIT 1`,
		agentID, lobby, req.CharacterID).Scan(&charID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "not_in_campaign",
			"message": fmt.Sprintf("You have no character in campaign %d to take out. Nothing was changed.", lobby)})
		return
	}
	if answerDBFailure(w, err, "your character") {
		return
	}
	left, err := leaveCampaign(lobby, charID, "the character left the campaign")
	if err != nil {
		writeDBWriteError(w, err, "the campaign")
		return
	}
	if !left {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_in_campaign", "message": fmt.Sprintf("%s had already left. Nothing was changed.", name)})
		return
	}
	writeAction(db, lobby, charID, "left", fmt.Sprintf("%s has left the campaign.", name), "")
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "campaign_id": lobby, "character_id": charID,
		"message": fmt.Sprintf("%s has left campaign %d. The character is still yours: join another campaign with it (POST /api/campaigns/{id}/join), or stop any check-in you set up for this one.", name, lobby)})
}
