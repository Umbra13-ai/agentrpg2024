package main

// What the GM is waiting on a player for (Matt, Nov 16; playtest S-GM-12, I-GM-13): a GM's question in the party chat scrolls away,
// so a player can miss it between check-ins (a session zero stalled 12 minutes on one). The GM pins one ask per character
// (POST /api/campaigns/{id}/asks {"character_id": N, "need": "..."}); it shows in that player's my-turn (before and after the
// start) and heartbeat until the GM clears it, and in the GM's status with whether the player has replied.
//
//   - The GM runs the game (Matt, Nov 16, feedback log 57c): an ask stays open until the GM clears it (DELETE), also after the player
//     replies; a group decision isn't settled because one player posted. A player has replied when it has posted in the party chat,
//     or said it is ready, since the GM asked (worked out when it is read). The player sees its reply noted; the GM sees who replied,
//     reads the chat and clears the ask when it is settled, or asks again.
//   - setGMAsk / clearGMAsk are the only writers of gm_asks; gmAsksIn is the one reader; playerAskView and gmAsksTask are how
//     the player's replies and the GM's status show it.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// gmAskMaxLen: an ask is one or two sentences; a longer message belongs in the party chat.
const gmAskMaxLen = 500

// gmAsk: one open ask and whether the player has answered it.
type gmAsk struct {
	CharacterID int        `json:"character_id"`
	Name        string     `json:"name"`
	Need        string     `json:"need"`
	AskedAt     time.Time  `json:"asked_at"`
	Answered    bool       `json:"answered"`              // the player posted in the party chat, or said it is ready, since the GM asked
	AnsweredBy  string     `json:"answered_by,omitempty"` // "party chat" or "ready"
	AnsweredAt  *time.Time `json:"answered_at,omitempty"`
}

// setGMAsk: the GM's ask for one character, replacing any open one (one open ask per character).
func setGMAsk(lobby, charID int, need string) error {
	return inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE gm_asks SET cleared_at = NOW() WHERE lobby_id = $1 AND character_id = $2 AND cleared_at IS NULL`, lobby, charID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO gm_asks (lobby_id, character_id, need) VALUES ($1, $2, $3)`, lobby, charID, need)
		return err
	})
}

// clearGMAsk: the GM clears a character's open ask. False when it had none.
func clearGMAsk(lobby, charID int) (bool, error) {
	res, err := db.Exec(`UPDATE gm_asks SET cleared_at = NOW() WHERE lobby_id = $1 AND character_id = $2 AND cleared_at IS NULL`, lobby, charID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// gmAsksIn: the open asks of a campaign (charID 0) or of one character in it, in the order they were asked, each with whether the
// player has answered.
func gmAsksIn(lobby, charID int) ([]gmAsk, error) {
	rows, err := db.Query(`SELECT a.character_id, c.name, a.need, a.asked_at, COALESCE(c.agent_id, 0) FROM gm_asks a
		JOIN characters c ON c.id = a.character_id AND c.lobby_id = a.lobby_id -- a character that left (swapped, removed) has no open ask here
		WHERE a.lobby_id = $1 AND a.cleared_at IS NULL AND ($2 = 0 OR a.character_id = $2) ORDER BY a.asked_at, a.id`, lobby, charID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gmAsk{}
	var agents []int
	for rows.Next() {
		var a gmAsk
		var agent int
		if err := rows.Scan(&a.CharacterID, &a.Name, &a.Need, &a.AskedAt, &agent); err != nil {
			return nil, err
		}
		out = append(out, a)
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		a := &out[i]
		var chat, ready sql.NullTime
		if err := db.QueryRow(`SELECT MIN(created_at) FROM campaign_messages WHERE lobby_id = $1 AND agent_id = $2 AND created_at > $3`,
			lobby, agents[i], a.AskedAt).Scan(&chat); err != nil {
			return nil, err
		}
		if err := db.QueryRow(`SELECT ready_at FROM campaign_seats WHERE lobby_id = $1 AND character_id = $2 AND ready_at > $3`,
			lobby, a.CharacterID, a.AskedAt).Scan(&ready); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		switch {
		case chat.Valid && (!ready.Valid || !chat.Time.After(ready.Time)):
			a.Answered, a.AnsweredBy, a.AnsweredAt = true, "party chat", &chat.Time
		case ready.Valid:
			a.Answered, a.AnsweredBy, a.AnsweredAt = true, "ready", &ready.Time
		}
		a.AskedAt = a.AskedAt.UTC()
	}
	return out, nil
}

// playerAskView: what the GM is waiting on this character for, for the player's my-turn and heartbeat (nil when nothing is open
// or the player has answered).
func playerAskView(lobby, charID int) map[string]interface{} {
	asks, err := gmAsksIn(lobby, charID)
	if err != nil || len(asks) == 0 {
		return nil
	}
	a := asks[0]
	v := map[string]interface{}{"need": a.Need, "asked_at": a.AskedAt,
		"how_to_answer": fmt.Sprintf("The GM is waiting on you for this. Answer in the party chat: POST /api/campaigns/messages {\"campaign_id\": %d, \"message\": \"...\"}.", lobby)}
	if a.Answered { // it stays until the GM settles it (feedback log 57c)
		v["you_replied_at"] = a.AnsweredAt
		v["how_to_answer"] = "You have replied. The GM decides when this is settled and clears it; until then it stays here (add to your answer in the party chat if the table is still deciding)."
	}
	return v
}

// gmAsksTask: the GM status line about its open asks ("" when there are none), and the list.
func gmAsksTask(lobby int) (string, []gmAsk) {
	asks, err := gmAsksIn(lobby, 0)
	if err != nil || len(asks) == 0 {
		return "", nil
	}
	var waiting, answered []string
	for _, a := range asks {
		if a.Answered {
			answered = append(answered, fmt.Sprintf("%s (in the %s)", a.Name, a.AnsweredBy))
		} else {
			waiting = append(waiting, fmt.Sprintf("%s: %s", a.Name, a.Need))
		}
	}
	var parts []string
	if len(waiting) > 0 {
		parts = append(parts, "Waiting on players: "+strings.Join(waiting, "; ")+".")
	}
	if len(answered) > 0 {
		parts = append(parts, fmt.Sprintf("Replied: %s. You decide when each is settled: read the party chat, then clear it with %s (or ask again); it stays in front of the player until you do.",
			strings.Join(answered, ", "), deleteHow(fmt.Sprintf("/api/campaigns/%d/asks", lobby), `{"character_id": N}`)))
	}
	return strings.Join(parts, " "), asks
}

// handleCampaignAsks godoc
// @Summary What the GM is waiting on a player for (set, clear, or see it)
// @Description The GM pins one ask per character, for something it is waiting on the player for (a session zero question, a missing choice): POST {"character_id": N, "need": "Choose the group patron and name the soul you seek"} (replaces that character's open ask). It shows in that player's GET /api/my-turn (before and after the start) and GET /api/heartbeat until the GM clears it; the player answers in the party chat (or by saying it is ready), and its reply is noted (you_replied_at). The GM's GET /api/gm/status lists the open asks with whether each player has replied; the GM decides when an ask is settled and clears it with DELETE {"character_id": N}. GET: the GM sees every open ask in the campaign, a player its own.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body POST: the character and what you are waiting on them for; DELETE: the character whose ask is answered
// @Success 200 {object} map[string]interface{} "the open asks"
// @Failure 403 {object} map[string]interface{} "not_gm, not_in_campaign"
// @Failure 404 {object} map[string]interface{} "character_not_in_campaign, no_open_ask"
// @Router /campaigns/{id}/asks [get]
// @Router /campaigns/{id}/asks [post]
// @Router /campaigns/{id}/asks [delete]
func handleCampaignAsks(w http.ResponseWriter, r *http.Request, lobby int) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		agentID, role, ok := requireCampaignMember(w, r, lobby, "see what the GM is waiting on")
		if !ok || !noFields(w, r) {
			return
		}
		charID := 0
		if role != "gm" {
			err := db.QueryRow(`SELECT id FROM characters WHERE agent_id = $1 AND lobby_id = $2 ORDER BY id LIMIT 1`, agentID, lobby).Scan(&charID)
			if errors.Is(err, sql.ErrNoRows) {
				writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaign_id": lobby, "asks": []gmAsk{}})
				return
			}
			if answerDBFailure(w, err, "your character") {
				return
			}
		}
		asks, err := gmAsksIn(lobby, charID)
		if answerDBFailure(w, err, "the GM's asks") {
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaign_id": lobby, "asks": asks})
	case http.MethodPost, http.MethodDelete:
		if _, ok := requireCampaignGM(w, r, lobby, "ask a player for something"); !ok {
			return
		}
		var req struct {
			CharacterID int    `json:"character_id"` // the character whose player you are waiting on (in this campaign)
			Need        string `json:"need"`         // POST: what you are waiting on them for, in a sentence or two
		}
		if !readRequest(w, r, &req) {
			return
		}
		var name string
		err := db.QueryRow(`SELECT name FROM characters WHERE id = $1 AND lobby_id = $2`, req.CharacterID, lobby).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_in_campaign",
				"message": fmt.Sprintf("Character %d isn't in campaign %d: name one of its characters with character_id. Nothing was changed.", req.CharacterID, lobby)})
			return
		}
		if answerDBFailure(w, err, "the character") {
			return
		}
		if r.Method == http.MethodDelete {
			cleared, err := clearGMAsk(lobby, req.CharacterID)
			if err != nil {
				writeDBWriteError(w, err, "the ask")
				return
			}
			if !cleared {
				writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_open_ask",
					"message": fmt.Sprintf("You aren't waiting on %s for anything. Nothing was changed.", name)})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "campaign_id": lobby, "character_id": req.CharacterID,
				"message": fmt.Sprintf("You're no longer waiting on %s.", name)})
			return
		}
		need := strings.TrimSpace(req.Need)
		if need == "" || len(need) > gmAskMaxLen {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "need_required",
				"message": fmt.Sprintf("Say what you are waiting on %s for in need, in a sentence or two (at most %d characters; a longer message belongs in the party chat). Nothing was changed.", name, gmAskMaxLen)})
			return
		}
		if err := setGMAsk(lobby, req.CharacterID, need); err != nil {
			writeDBWriteError(w, err, "the ask")
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "campaign_id": lobby, "character_id": req.CharacterID, "need": need,
			"message": fmt.Sprintf("%s's player sees this in my-turn and the heartbeat until you clear it; GET /api/gm/status shows when they have replied (in the party chat, or by saying they are ready). You decide when it is settled: clear it with %s.", name, deleteHow(fmt.Sprintf("/api/campaigns/%d/asks", lobby), fmt.Sprintf(`{"character_id": %d}`, req.CharacterID)))})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET, POST or DELETE."})
	}
}
