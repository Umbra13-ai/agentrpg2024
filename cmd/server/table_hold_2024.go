package main

// The table held before round 1 (playtest I-GM-7, Matt Nov 16): a GM may start a campaign without opening its first round, to run
// a session zero (the setting, the patron, the characters' ties) before anyone takes a turn. While the table is held a player's
// game action is refused (409 table_held: nothing is spent and nothing is saved); talk in the scene, the party chat, character
// setup and gear go on as before. The GM opens play with POST /api/campaigns/{id}/round/open (round 1 opens with the turn
// schedule); a fight the GM starts opens play too.
//
//   - tableHeld is the one answer to "is the table held": the round gate, my-turn, the heartbeat, the check-in status, GM status,
//     the campaign's view and the exploration skip read it.
//   - holdTable and releaseTable are the only writers of lobbies.table_held.

import (
	"fmt"
	"net/http"
)

// tableHeld: the campaign's table is held before round 1.
func tableHeld(q rowQuerier, lobby int) (bool, error) {
	var held bool
	err := q.QueryRow(`SELECT COALESCE(table_held, false) FROM lobbies WHERE id = $1`, lobby).Scan(&held)
	return held, notFoundIsNothing(err)
}

// tableIsHeld: tableHeld for a reply that only shows it (a database failure there is answered by guardDB).
func tableIsHeld(lobby int) bool {
	held, _ := tableHeld(db, lobby)
	return held
}

func holdTable(ex execer, lobby int) error {
	_, err := ex.Exec(`UPDATE lobbies SET table_held = true WHERE id = $1`, lobby)
	return err
}

// releaseTable ends the hold; it says whether the table was held.
func releaseTable(lobby int) (bool, error) {
	res, err := db.Exec(`UPDATE lobbies SET table_held = false WHERE id = $1 AND table_held`, lobby)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// tableHeldMessage: what a player is told while the table is held.
func tableHeldMessage(lobby int) string {
	return fmt.Sprintf("The GM is holding the table before round 1 (a session zero): talk in the scene (POST /api/action {\"action\": \"speak\", \"description\": \"...\"}), "+
		"the party chat (POST /api/campaigns/messages {\"campaign_id\": %d, \"message\": \"...\"}) and your character's setup work now. Your first turn waits until the GM opens round 1; GET /api/my-turn says when.", lobby)
}

// tableHeldGMTask: the GM's reminder while the table is held.
func tableHeldGMTask(lobby int) string {
	return fmt.Sprintf("The table is held before round 1 (session zero): players can talk and finish their characters, but no turn is taken. Present the setting and settle what the party needs, then open play with %s (starting a fight opens it too).", openPlayRequest(lobby))
}

// openPlayRequest: the GM's request that ends the hold and opens round 1, which the held start's reply (open_play), the GM's task and
// the guides name (feedback log 18: the held start's reply said it only inside a long sentence).
func openPlayRequest(lobby int) string {
	return fmt.Sprintf("POST /api/campaigns/%d/round/open", lobby)
}

// refuseWhileHeld answers a player's game action while the table is held (true when it did).
func refuseWhileHeld(w http.ResponseWriter, lobby int) (bool, error) {
	held, err := tableHeld(db, lobby)
	if err != nil || !held {
		return false, err
	}
	writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "table_held", "campaign_id": lobby,
		"message": tableHeldMessage(lobby) + " Nothing was spent or saved."})
	return true, nil
}

// handleRoundOpen godoc
// @Summary Open play: round 1 after a held start (GM only)
// @Description Ends the hold of a campaign started with {"first_round": "hold"} (POST /api/campaigns/{id}/start): with the turn schedule round 1 opens now (round: its number and final check-in), and players' game actions count from then on. Without the schedule play simply opens. A campaign that isn't held answers held_before false and opens round 1 only if no round is open.
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Play is open"
// @Failure 403 {object} map[string]interface{} "not_gm"
// @Failure 409 {object} map[string]interface{} "campaign_not_active"
// @Router /campaigns/{id}/round/open [post]
func handleRoundOpen(w http.ResponseWriter, r *http.Request, lobby int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "POST /api/campaigns/{id}/round/open."})
		return
	}
	if _, ok := requireCampaignGM(w, r, lobby, "open its first round"); !ok {
		return
	}
	if !noFields(w, r) {
		return
	}
	var status string
	if answerDBFailure(w, db.QueryRow(`SELECT status FROM lobbies WHERE id = $1`, lobby).Scan(&status), "the campaign") {
		return
	}
	if status != "active" {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "campaign_not_active",
			"message": fmt.Sprintf("Campaign %d is %s: start it first (POST /api/campaigns/%d/start). Nothing was changed.", lobby, status, lobby)})
		return
	}
	was, err := releaseTable(lobby)
	if err != nil {
		writeDBWriteError(w, err, "the campaign's hold")
		return
	}
	scheduleCampaignStarts(lobby) // with the turn schedule, round 1 opens unless a round is open (noncombat_round_2024.go)
	reply := map[string]interface{}{"success": true, "campaign_id": lobby, "held_before": was,
		"message": "Play is open: players' turns count from now."}
	if rr, err := openRoundOf(db, lobby); err == nil && rr != nil {
		reply["round"] = rr.opened()
	}
	if was {
		writeAction(db, lobby, 0, "round_open", "The GM opens play: the session zero is over and the first round begins.", "")
		// the players learn it in the party chat: the round's opening posts it (openRound, check_in_2024.go)
	}
	writeJSONStatus(w, http.StatusOK, reply)
}
