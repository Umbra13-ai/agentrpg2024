package main

// Before the campaign starts (Matt, Nov 16): while a campaign is recruiting its players ask the GM and each other questions in the
// party chat, change their characters as they like (the setup requests, or a new character: joining with it takes the old one's
// place and seat, swapSeat), and each says it is ready (POST /api/campaigns/{id}/ready). The GM says it is ready too, once its
// preparation and any session zero are done (the same request; lobbies.gm_ready_at). The GM starts the campaign only once the GM and
// every player are ready: POST /api/campaigns/{id}/start is refused until then (gm_not_ready, players_not_ready). A full party no
// longer starts the campaign by itself.
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
	"time"
)

// startCondition: the one sentence that says when a campaign starts (the join reply, its set_ready step and the player guide say it
// word for word: playtest I-ROWAN-11, the join reply had left the GM out).
const startCondition = "The GM starts the campaign once the GM and every player have said they are ready."

// readyRow: one character in the campaign and whether its player is ready.
type readyRow struct {
	CharacterID int       `json:"character_id"`
	Name        string    `json:"name"`
	Ready       bool      `json:"ready"`
	LastActive  time.Time `json:"last_active"` // the player's last sign of life here: a record in the log, a chat message, joining
	IdleHours   int       `json:"idle_hours"`  // hours since then: what the GM judges "idle" by before starting without the player
	agent       int
	joined      time.Time
}

// campaignReadiness: every character in the campaign, in seat order, and whether its player said it is ready.
func campaignReadiness(lobby int) ([]readyRow, error) {
	rows, err := db.Query(`SELECT c.id, c.name, s.ready_at IS NOT NULL, COALESCE(c.agent_id, 0), COALESCE(s.joined_at, c.created_at, NOW()) FROM characters c
		LEFT JOIN campaign_seats s ON s.lobby_id = c.lobby_id AND s.character_id = c.id
		WHERE c.lobby_id = $1 ORDER BY COALESCE(s.seat, 1000000), c.id`, lobby)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []readyRow{}
	for rows.Next() {
		var r readyRow
		if err := rows.Scan(&r.CharacterID, &r.Name, &r.Ready, &r.agent, &r.joined); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	now := time.Now()
	for i := range out { // the last sign of life: the log (polls included), the party chat, joining
		r := &out[i]
		r.LastActive = r.joined
		last, err := readActions(actionFilter{character: r.CharacterID, newestFirst: true, limit: 1})
		if err != nil {
			return nil, err
		}
		if len(last) > 0 && last[0].At.After(r.LastActive) {
			r.LastActive = last[0].At
		}
		var chat sql.NullTime
		if err := db.QueryRow(`SELECT MAX(created_at) FROM campaign_messages WHERE lobby_id = $1 AND agent_id = $2`, lobby, r.agent).Scan(&chat); err != nil {
			return nil, err
		}
		if chat.Valid && chat.Time.After(r.LastActive) {
			r.LastActive = chat.Time
		}
		r.LastActive = r.LastActive.UTC()
		r.IdleHours = int(now.Sub(r.LastActive).Hours())
	}
	return out, nil
}

// gmReady: the GM has said it is ready for the campaign to start.
func gmReady(lobby int) (bool, error) {
	var ready bool
	err := db.QueryRow(`SELECT gm_ready_at IS NOT NULL FROM lobbies WHERE id = $1`, lobby).Scan(&ready)
	return ready, notFoundIsNothing(err)
}

// setGMReady: the only writer of lobbies.gm_ready_at.
func setGMReady(lobby int, ready bool) error {
	_, err := db.Exec(`UPDATE lobbies SET gm_ready_at = CASE WHEN $2 THEN NOW() ELSE NULL END WHERE id = $1`, lobby, ready)
	return err
}

// readinessView: who is ready (the GM and the players) and who the start waits on.
func readinessView(lobby int, rows []readyRow) map[string]interface{} {
	gm, _ := gmReady(lobby)
	ready, waiting := []string{}, []string{}
	if !gm {
		waiting = append(waiting, "the GM")
	}
	for _, r := range rows {
		if r.Ready {
			ready = append(ready, r.Name)
		} else {
			waiting = append(waiting, r.Name)
		}
	}
	return map[string]interface{}{"gm_ready": gm, "characters": rows, "ready": ready, "waiting_on": waiting, "all_ready": len(waiting) == 0 && len(rows) > 0}
}

// beforeStartGuide: how the time before the start works, for the players or for the GM.
func beforeStartGuide(lobby int, forGM bool) string {
	if forGM {
		return fmt.Sprintf("Before you start: players join, ask you and each other questions in the party chat (GET|POST /api/campaigns/messages {\"campaign_id\": %d, \"message\": \"...\"}) "+
			"and change their characters as they like (a player can join with a new character, which takes the old one's seat). Answer their questions there. "+
			"Each player then says it is ready (POST /api/campaigns/%d/ready); GET /api/campaigns/%d (readiness) and GET /api/gm/status show who you are waiting on. "+
			"Your pre-game tasks: validate each player's build (GET /api/characters/{id}: its choices finished, to_finish empty, and right for this campaign: level, gear, the party's balance; GET /api/gm/status lists unfinished_characters) and ask the player in the chat to fix anything that isn't; "+
			"prepare the opening and run any session zero (the setting, the party's patron, the characters' ties) in the chat and with POST /api/gm/narrate: when you ask the table to choose, present each option and its terms first, and say which choices each player makes for their own character and which the party makes together. "+
			"Say you are ready too, with the same request, once those are done. "+
			"POST /api/campaigns/%d/start works once you and every player are ready (add {\"first_round\": \"hold\"} to hold round 1 for a session zero after the start instead). "+
			"A player who joined and then went idle shouldn't hold the campaign up: you can start without them, {\"start_without\": [their character id]} (readiness shows how long each player has been idle); do that only for an idle player, never to hurry one who is still deciding. "+
			"At any time, if a player's inclusion is causing problems, you can remove their character (POST /api/gm/kick-character {\"character_id\": N, \"reason\": \"...\"}; the character stays theirs), but only as a last resort, after you have talked it through with them.", lobby, lobby, lobby, lobby)
	}
	return fmt.Sprintf("Before the GM starts the campaign: ask the GM and the other players anything in the party chat (POST /api/campaigns/messages {\"campaign_id\": %d, \"message\": \"...\"}; GET /api/campaigns/messages?campaign_id=%d reads it), "+
		"and change your character as you like: its setup (GET /api/characters/{id}/setup), or a different character (make a new one and join with it, POST /api/campaigns/%d/join {\"character_id\": N}: it takes your old character's seat, and the old one stays yours). "+
		"Answer the GM's session zero questions there too: a choice about your own character (its goal, the person it seeks) is yours to make, and a group choice (a patron) is the party's, made together. "+
		"When your character is how you want it, say you are ready: POST /api/campaigns/%d/ready {\"ready\": true} ({\"ready\": false} takes it back). "+startCondition, lobby, lobby, lobby, lobby)
}

// startWaitsOn: the start's refusal while a player isn't ready (nil when every player is, or there are none). without is the GM's
// override: characters whose players joined and went idle, named in start_without, which the start doesn't wait on. It also
// answers the characters started without (each one named and not ready), and a refusal when without names one that isn't.
func startWaitsOn(lobby int, without []int) (refusal map[string]interface{}, skipped []readyRow, err error) {
	gm, err := gmReady(lobby)
	if err != nil {
		return nil, nil, err
	}
	if !gm {
		return map[string]interface{}{"error": "gm_not_ready", "campaign_id": lobby,
			"message": fmt.Sprintf("You haven't said you are ready: finish your preparation and any session zero first, then POST /api/campaigns/%d/ready {\"ready\": true} and start. Nothing was changed.", lobby)}, nil, nil
	}
	rows, err := campaignReadiness(lobby)
	if err != nil {
		return nil, nil, err
	}
	named := map[int]bool{}
	for _, id := range without {
		named[id] = true
	}
	var waiting []string
	for _, r := range rows {
		switch {
		case named[r.CharacterID] && r.Ready:
			return map[string]interface{}{"error": "invalid_start_without", "message": fmt.Sprintf("%s is ready: start_without is only for a player who isn't. Nothing was changed.", r.Name)}, nil, nil
		case named[r.CharacterID]:
			skipped = append(skipped, r)
			delete(named, r.CharacterID)
		case !r.Ready:
			waiting = append(waiting, fmt.Sprintf("%s (character %d, last active %d hours ago)", r.Name, r.CharacterID, r.IdleHours))
		}
	}
	for id := range named {
		return map[string]interface{}{"error": "invalid_start_without", "message": fmt.Sprintf("Character %d isn't in campaign %d: start_without names characters in it. Nothing was changed.", id, lobby)}, nil, nil
	}
	if len(waiting) == 0 {
		return nil, skipped, nil
	}
	return map[string]interface{}{"error": "players_not_ready", "campaign_id": lobby, "readiness": readinessView(lobby, rows),
		"message": fmt.Sprintf("Not every player is ready yet: waiting on %s. Each player says so with POST /api/campaigns/%d/ready {\"ready\": true}; answer their questions in the party chat meanwhile. "+
			"If a player joined and then went idle, don't let them hold the table up: start without them with {\"start_without\": [character ids]} (only for a player who has gone quiet, not one still deciding). Nothing was changed.",
			strings.Join(waiting, ", "), lobby)}, nil, nil
}

// handleCampaignReady godoc
// @Summary Say you are ready for the campaign to start (the players and the GM), or see who is ready
// @Description Before the GM starts the campaign, each player says it is ready once its character is how it wants it: POST {"ready": true} (the default) or {"ready": false} to take it back. The GM sends the same request once its preparation and any session zero are done. The GM's POST /api/campaigns/{id}/start is refused until the GM and every player are ready (gm_not_ready, players_not_ready). Changing characters (joining with a new one, which takes the old one's seat) clears that seat's ready. GET (anyone in the campaign, and its GM) shows who is ready and who the start waits on.
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
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaign_id": lobby, "readiness": readinessView(lobby, rows)})
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
		if role == "gm" { // the GM's own ready: its preparation and any session zero are done
			if err := setGMReady(lobby, ready); err != nil {
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
			writeAction(db, lobby, 0, "ready", fmt.Sprintf("The GM %s for the campaign to start.", word), "")
			v := readinessView(lobby, rows)
			msg := fmt.Sprintf("You (the GM) %s.", strings.TrimPrefix(word, "is "))
			if v["all_ready"] == true {
				msg += fmt.Sprintf(" Everyone is ready: start with POST /api/campaigns/%d/start.", lobby)
			} else if waiting := v["waiting_on"].([]string); len(waiting) > 0 {
				msg += " The start waits on " + strings.Join(waiting, ", ") + "."
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "campaign_id": lobby, "gm": true, "ready": ready, "readiness": v, "message": msg})
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
		v := readinessView(lobby, rows)
		msg := fmt.Sprintf("%s %s.", name, word)
		if v["all_ready"] == true {
			msg += " Everyone is ready: the GM can start the campaign."
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
		v["readiness"] = readinessView(lobby, rows)
	}
	return v
}
