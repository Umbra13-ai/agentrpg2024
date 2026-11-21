package main

import (
	"database/sql"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GM notices: private facts that arise from a PLAYER's action and that only the GM may read (for example the Concentration reminders for a
// monster that has a Spellcasting entry: whether a monster casts spells or concentrates is not something the player's reply or the shared
// action log may carry). The one owner:
//
//   - addGMNotice stores a notice for the campaign. Nothing about it enters a player's reply or the action log.
//   - The GM reads them in GET /api/gm/status ("gm_notices", unacknowledged ones) and GET /api/gm/notices, and acknowledges them with
//     DELETE /api/gm/notices ({"ids":[...]} or {"all":true}). They stay until acknowledged, so a missed read does not lose one.
//
// A reply the GM itself receives (the GM conditions route, a turn change) may still carry its own notice inline; those never reach a player.
// A notice is stored only while the campaign exists; there is no push to the GM: the GM sees it at its next status check.
//
// Persistence failures are explicit, never silent. A notice that cannot be written to the table is logged as an error and kept in memory
// (heldGMNotices, at most 200 per campaign, lost on a restart), and the GM still receives it, marked "held_in_memory": true, in the same
// places. The action that produced it (a hit, a condition) has already been saved and is not undone: the notice is a reminder and the
// game state does not depend on it. A failure to READ the table is answered as a database error by GET /api/gm/notices and reported in
// GET /api/gm/status as "gm_notices_error"; it is never reported as "no notices".
func addGMNotice(lobbyID int, text string) {
	text = strings.TrimSpace(text)
	if lobbyID == 0 || text == "" {
		return
	}
	if _, err := db.Exec(`INSERT INTO gm_notices (lobby_id, text) VALUES ($1, $2)`, lobbyID, text); err != nil {
		log.Printf("ERROR gm notice for campaign %d not stored in the database (held in memory for the GM): %v", lobbyID, err)
		// The action that wrote this notice is ALREADY committed (every caller writes the notice after the HP or condition change is saved,
		// never inside a transaction that can still roll back). Letting this failure turn the action's reply into database_error would make
		// a committed cast or hit look failed and safe to retry (spending the slot and dealing the damage twice), so the failure is caught
		// here on purpose: the reply stays the true one, and the GM gets the notice from memory, flagged held_in_memory.
		handledDBError(err)
		heldGMNotices.hold(lobbyID, text)
	}
}

// heldNotices: notices whose database write failed, per campaign. Ids are negative so they never collide with table ids.
type heldNotices struct {
	mu   sync.Mutex
	next int
	by   map[int][]gmNotice
}

var heldGMNotices = &heldNotices{by: map[int][]gmNotice{}}

func (h *heldNotices) hold(lobbyID int, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next--
	list := append(h.by[lobbyID], gmNotice{ID: h.next, Text: text, CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z"), HeldInMemory: true})
	if len(list) > 200 {
		list = list[len(list)-200:]
	}
	h.by[lobbyID] = list
}

func (h *heldNotices) list(lobbyID int) []gmNotice {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gmNotice(nil), h.by[lobbyID]...)
}

// ack drops the held notices with these ids (all: every one) of this campaign only, and says how many.
func (h *heldNotices) ack(lobbyID int, ids []int, all bool) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	keep := []gmNotice{}
	n := 0
	for _, x := range h.by[lobbyID] {
		if all || containsInt(ids, x.ID) {
			n++
			continue
		}
		keep = append(keep, x)
	}
	h.by[lobbyID] = keep
	return n
}

type gmNotice struct {
	ID           int    `json:"id"`
	Text         string `json:"text"`
	CreatedAt    string `json:"created_at"`
	HeldInMemory bool   `json:"held_in_memory,omitempty"` // the database write failed: kept in memory until acknowledged or a restart
}

// pendingGMNotices: the campaign's notices the GM has not acknowledged (the held ones first, then the table's, oldest first). Only this
// campaign's. err is a failed read of the table: the held ones are still returned.
func pendingGMNotices(lobbyID int) ([]gmNotice, error) {
	out := heldGMNotices.list(lobbyID)
	if out == nil {
		out = []gmNotice{}
	}
	rows, err := db.Query(`SELECT id, text, to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM gm_notices WHERE lobby_id = $1 AND NOT acknowledged ORDER BY id LIMIT 200`, lobbyID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var n gmNotice
		if err := rows.Scan(&n.ID, &n.Text, &n.CreatedAt); err != nil {
			return out, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// gmNoticesRequest: the body of DELETE /api/gm/notices (GET takes none).
type gmNoticesRequest struct {
	IDs []int `json:"ids"` // the notice ids to acknowledge
	All bool  `json:"all"` // true: acknowledge every unacknowledged notice of the campaign
}

// handleGMNotices godoc
// @Summary List or acknowledge the GM's private notices
// @Description GET lists the unacknowledged GM notices of the campaign: private facts that arose from players' actions and that never appear in a player's reply or the action log (for example Concentration reminders for a monster with a Spellcasting entry). DELETE acknowledges them: {"ids":[1,2]} or {"all":true}. GM only; GET /api/gm/status also carries the unacknowledged ones as gm_notices.
// @Tags GM
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param campaign_id query int false "The campaign (default: the GM's only active campaign)"
// @Success 200 {object} map[string]interface{} "gm_notices (and acknowledged, for DELETE)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM (not_gm)"
// @Router /gm/notices [get]
// @Router /gm/notices [delete]
func handleGMNotices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req gmNoticesRequest
	_, campaignID, ok := gmRead(w, r, &req, optionalBody()) // the sender, the body and the campaign; a player gets 403 not_gm
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := pendingGMNotices(campaignID)
		if answerDBFailure(w, err, "the GM's notices") {
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"campaign_id": campaignID, "gm_notices": list})
	case http.MethodDelete:
		if !req.All && len(req.IDs) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "ids_or_all_required", "message": `Send {"ids":[...]} or {"all":true}.`})
			return
		}
		// Only this campaign's notices: an id of another campaign matches nothing (lobby_id is part of every statement). The reply says how
		// many were acknowledged and which ids matched nothing, so a mixed list is not read as complete.
		// One transaction: either every named notice of this campaign is acknowledged or none is (a failure answers database_error and
		// changes nothing). Held (in-memory) notices are acknowledged only after the commit.
		n := 0
		var unmatched []int
		var heldIDs []int
		err := inTx(func(tx *sql.Tx) error {
			if req.All {
				res, err := tx.Exec(`UPDATE gm_notices SET acknowledged = true WHERE lobby_id = $1 AND NOT acknowledged`, campaignID)
				if err != nil {
					return dbFailureError(err, "the GM's notices")
				}
				k, _ := res.RowsAffected()
				n = int(k)
				return nil
			}
			for _, id := range req.IDs {
				if id < 0 {
					heldIDs = append(heldIDs, id)
					continue
				}
				res, err := tx.Exec(`UPDATE gm_notices SET acknowledged = true WHERE lobby_id = $1 AND id = $2 AND NOT acknowledged`, campaignID, id)
				if err != nil {
					return dbFailureError(err, "the GM's notices")
				}
				if k, _ := res.RowsAffected(); k > 0 {
					n++
				} else {
					unmatched = append(unmatched, id)
				}
			}
			return nil
		})
		if err != nil {
			if !answerHelperDBFailure(w, err) {
				writeDBWriteError(w, err, "the GM's notices")
			}
			return
		}
		if req.All {
			n += heldGMNotices.ack(campaignID, nil, true)
		}
		for _, id := range heldIDs {
			if heldGMNotices.ack(campaignID, []int{id}, false) > 0 {
				n++
			} else {
				unmatched = append(unmatched, id)
			}
		}
		// The acknowledgment is committed. A failure to re-read the list now must not answer database_error (that would read as "nothing
		// was acknowledged"): the reply says what was acknowledged and that the remaining list could not be read.
		list, err := pendingGMNotices(campaignID)
		out := map[string]interface{}{"acknowledged": n, "gm_notices": list}
		if err != nil {
			log.Printf("database error reading gm notices after an acknowledgment: %v", err)
			handledDBError(err)
			out["gm_notices_error"] = "The acknowledgment was saved, but the remaining notices could not be read just now; GET /api/gm/notices shows them."
		}
		if len(unmatched) > 0 {
			out["unmatched_ids"] = unmatched // not this campaign's, already acknowledged, or unknown: nothing was changed for them
		}
		writeJSONStatus(w, 200, out)
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}
