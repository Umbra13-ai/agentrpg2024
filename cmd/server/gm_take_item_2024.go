package main

import (
	"net/http"
	"strings"
)

// handleGMTakeItem godoc
// @Summary Leave a character's item where the narration put it (GM)
// @Description When your narration has a character use up or leave behind something it carries ("she stuffs her spare robe and blanket under the bedroll as a decoy"), take it out of the character's inventory here, saying where it is now. It goes on the campaign's ground with that place (GET /api/campaigns/{id}/ground), the log says so, and the player's GET /api/my-turn lists it under left_behind with the request that picks it back up. An attunement to the item ends. Use it only for what the player declared or agreed to use; never spend a player's gear the player didn't choose to.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Body The character, the item and where the narration left it
// @Success 200 {object} map[string]interface{} "The item taken out: its dropped_id, where it is and the reminder the player sees"
// @Failure 400 {object} map[string]interface{} "item_required, character_id_required, the item isn't carried"
// @Router /gm/take-item [post]
func handleGMTakeItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"` // the character whose item the narration used
		Item        string `json:"item"`         // the item (its name or id in the character's inventory)
		Quantity    int    `json:"quantity"`     // how many of a stack (default all)
		Where       string `json:"where"`        // where the narration left it ("in the bedroll as a decoy"); default on the ground
	}
	if _, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID), campaignBeforeStart()); !ok {
		return
	}
	if strings.TrimSpace(req.Item) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_required", "message": "Send item: the item the narration used (its name or id). Nothing was changed."})
		return
	}
	res, code := dropToGround(req.CharacterID, req.Item, req.Quantity, req.Where) // dropped_items.go
	if code == dbErrorCode {
		writeDBWriteError(w, nil, "the item")
		return
	}
	if code != "" {
		status := http.StatusBadRequest
		if strings.HasPrefix(code, "ambiguous_item") {
			status = http.StatusConflict
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": code, "message": "The item couldn't be taken out of the character's inventory (" + code + "). Nothing was changed."})
		return
	}
	where := strings.TrimSpace(req.Where)
	if where == "" {
		where = "on the ground"
	}
	reply := map[string]interface{}{"success": true, "dropped_id": res.DroppedID, "name": res.Name, "quantity": res.Quantity, "items_inside": res.Contents,
		"where": where, "player_sees": leftBehindNote(req.CharacterID)}
	if res.Attunement != "" {
		reply["attunement_ended"] = res.Attunement
	}
	writeJSONStatus(w, http.StatusOK, reply)
}
