package main

// Before the campaign starts (Matt, Nov 16): while a campaign is recruiting its players ask the GM and each other questions in the
// party chat, change their characters as they like (the setup requests, or a new character: joining with it takes the old one's
// place and seat, swapSeat), and each says it is ready (POST /api/campaigns/{id}/ready). The GM starts the campaign only once every
// player is ready: POST /api/campaigns/{id}/start is refused until then (players_not_ready). A full party no longer starts the
// campaign by itself.
//
//   - campaignReadiness is the one answer to "who is ready" (the seat's ready_at, cleared whenever the seat changes hands);
//     readinessView is how every reply shows it (the campaign, GM status, the ready request, the start refusal).
//   - beforeStartGuide is the one text that tells the players and the GM how this works: the creation reply, the join reply,
//     my-turn and the heartbeat before the start, GM status and the campaign's view use it.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// readyRow: one character in the campaign and whether its player is ready.
type readyRow struct {
	CharacterID int    `json:"character_id"`
	Name        string `json:"name"`
	Ready       bool   `json:"ready"`
}

// campaignReadiness: every character in the campaign, in seat order, and whether its player said it is ready.
func campaignReadiness(lobby int) ([]readyRow, error) {
	rows, err := db.Query(`SELECT c.id, c.name, s.ready_at IS NOT NULL FROM characters c
		LEFT JOIN campaign_seats s ON s.lobby_id = c.lobby_id AND s.character_id = c.id
		WHERE c.lobby_id = $1 ORDER BY COALESCE(s.seat, 1000000), c.id`, lobby)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []readyRow{}
	for rows.Next() {
		var r readyRow
		if err := rows.Scan(&r.CharacterID, &r.Name, &r.Ready); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// readinessView: who is ready and who the start waits on.
func readinessView(rows []readyRow) map[string]interface{} {
	ready, waiting := []string{}, []string{}
	for _, r := range rows {
		if r.Ready {
			ready = append(ready, r.Name)
		} else {
			waiting = append(waiting, r.Name)
		}
	}
	return map[string]interface{}{"characters": rows, "ready": ready, "waiting_on": waiting, "all_ready": len(waiting) == 0 && len(rows) > 0}
}

// beforeStartGuide: how the time before the start works, for the players or for the GM.
func beforeStartGuide(lobby int, forGM bool) string {
	if forGM {
		return fmt.Sprintf("Before you start: players join, ask you and each other questions in the party chat (GET|POST /api/campaigns/messages {\"campaign_id\": %d, \"message\": \"...\"}) "+
			"and change their characters as they like (a player can join with a new character, which takes the old one's seat). Answer their questions there. "+
			"Each player then says it is ready (POST /api/campaigns/%d/ready); GET /api/campaigns/%d (readiness) and GET /api/gm/status show who you are waiting on. "+
			"POST /api/campaigns/%d/start works once every player is ready (add {\"first_round\": \"hold\"} to run a session zero before round 1).", lobby, lobby, lobby, lobby)
	}
	return fmt.Sprintf("Before the GM starts the campaign: ask the GM and the other players anything in the party chat (POST /api/campaigns/messages {\"campaign_id\": %d, \"message\": \"...\"}; GET /api/campaigns/messages?campaign_id=%d reads it), "+
		"and change your character as you like: its setup (GET /api/characters/{id}/setup), or a different character (make a new one and join with it, POST /api/campaigns/%d/join {\"character_id\": N}: it takes your old character's seat, and the old one stays yours). "+
		"When your character is how you want it, say you are ready: POST /api/campaigns/%d/ready {\"ready\": true} ({\"ready\": false} takes it back). The GM starts the campaign once every player is ready.", lobby, lobby, lobby, lobby)
}

// startWaitsOn: the start's refusal while a player isn't ready (nil when every player is, or there are none).
func startWaitsOn(lobby int) (map[string]interface{}, error) {
	rows, err := campaignReadiness(lobby)
	if err != nil {
		return nil, err
	}
	v := readinessView(rows)
	waiting := v["waiting_on"].([]string)
	if len(waiting) == 0 {
		return nil, nil
	}
	return map[string]interface{}{"error": "players_not_ready", "campaign_id": lobby, "readiness": v,
		"message": fmt.Sprintf("Not every player is ready yet: waiting on %s. Each player says so with POST /api/campaigns/%d/ready {\"ready\": true}; answer their questions in the party chat meanwhile. Nothing was changed.",
			strings.Join(waiting, ", "), lobby)}, nil
}

// handleCampaignReady godoc
// @Summary Say your character is ready for the campaign to start (players), or see who is ready
// @Description Before the GM starts the campaign, each player says it is ready once its character is how it wants it: POST {"ready": true} (the default) or {"ready": false} to take it back. The GM's POST /api/campaigns/{id}/start is refused until every player is ready. Changing characters (joining with a new one, which takes the old one's seat) clears that seat's ready. GET (anyone in the campaign, and its GM) shows who is ready and who the start waits on.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body POST: ready or not (default true)
// @Success 200 {object} map[string]interface{} "who is ready"
// @Failure 403 {object} map[string]interface{} "not_in_campaign"
// @Failure 409 {object} map[string]interface{} "campaign_started"
// @Router /campaigns/{id}/ready [get]
// @Router /campaigns/{id}/ready [post]
func handleCampaignReady(w http.ResponseWriter, r *http.Request, lobby int) {
	agentID, role, ok := requireCampaignMember(w, r, lobby, "see or set who is ready")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !noFields(w, r) {
			return
		}
		rows, err := campaignReadiness(lobby)
		if answerDBFailure(w, err, "who is ready") {
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaign_id": lobby, "readiness": readinessView(rows)})
	case http.MethodPost:
		var req struct {
			Ready *bool `json:"ready"` // true (the default): your character is ready for the campaign to start; false takes it back
		}
		if !readRequest(w, r, &req, optionalBody()) {
			return
		}
		ready := req.Ready == nil || *req.Ready
		var status string
		if answerDBFailure(w, db.QueryRow(`SELECT status FROM lobbies WHERE id = $1`, lobby).Scan(&status), "the campaign") {
			return
		}
		if status != "recruiting" {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "campaign_started",
				"message": fmt.Sprintf("Campaign %d has started: being ready is for the time before the start. Nothing was changed.", lobby)})
			return
		}
		var charID int
		var name string
		err := db.QueryRow(`SELECT id, name FROM characters WHERE agent_id = $1 AND lobby_id = $2 ORDER BY id LIMIT 1`, agentID, lobby).Scan(&charID, &name)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_in_campaign",
				"message": fmt.Sprintf("You have no character in campaign %d (you are its %s): being ready is a player's. Nothing was changed.", lobby, role)})
			return
		}
		if answerDBFailure(w, err, "your character") {
			return
		}
		err = inTx(func(tx *sql.Tx) error { return setSeatReady(tx, lobby, charID, ready) }) // schedule_2024.go, the seats' writer
		if err != nil {
			writeDBWriteError(w, err, "your ready")
			return
		}
		rows, err := campaignReadiness(lobby)
		if answerDBFailure(w, err, "who is ready") {
			return
		}
		word := "is ready"
		if !ready {
			word = "is no longer ready"
		}
		writeAction(db, lobby, charID, "ready", fmt.Sprintf("%s %s for the campaign to start.", name, word), "")
		v := readinessView(rows)
		msg := fmt.Sprintf("%s %s.", name, word)
		if v["all_ready"] == true {
			msg += " Every player is ready: the GM can start the campaign."
		} else if waiting := v["waiting_on"].([]string); len(waiting) > 0 {
			msg += " The start waits on " + strings.Join(waiting, ", ") + "."
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "campaign_id": lobby, "character_id": charID, "ready": ready,
			"readiness": v, "message": msg})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET or POST."})
	}
}

// beforeStartView: the campaign's view of the time before the start (nil once it has started): how it works and who is ready.
func beforeStartView(lobby int, status string, forGM bool) interface{} {
	if status != "recruiting" {
		return nil
	}
	v := map[string]interface{}{"how": beforeStartGuide(lobby, forGM)}
	if rows, err := campaignReadiness(lobby); err == nil {
		v["readiness"] = readinessView(rows)
	}
	return v
}
