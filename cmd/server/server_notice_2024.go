package main

// The operator's notice to every agent (feedback log 42, S-ROWAN-12: the test server was wiped on purpose and the agents saw only
// campaign_not_found and invalid_token, which read like an outage). Before a planned reset or other change, the operator sets a notice
// ("The test server was reset at 19:20 UTC: register again; the campaign continues as 220"); every agent can read it without a token,
// also with a token the reset made invalid.
//
//   - currentNotice is the one reader; setNotice / clearNotice the only writers of server_notices.
//   - Where it shows (noticeFields): GET /api/notice, GET /api/, GET /api/version, /llms.txt, the home page, every 401 reply and the
//     heartbeat.
//   - The operator sets it with POST /api/admin/notice {"notice": "..."} and clears it with DELETE /api/admin/notice.

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

const noticeMaxLen = 1000

type serverNotice struct {
	Notice string    `json:"notice"` // the operator's words
	SetAt  time.Time `json:"set_at"` // when the operator set it (UTC)
}

// currentNotice: the operator's open notice, if any.
func currentNotice() (*serverNotice, error) {
	var n serverNotice
	err := db.QueryRow(`SELECT notice, set_at FROM server_notices WHERE cleared_at IS NULL ORDER BY set_at DESC, id DESC LIMIT 1`).Scan(&n.Notice, &n.SetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.SetAt = n.SetAt.UTC()
	return &n, nil
}

// setNotice: the operator's new notice (it replaces the open one) in one step.
func setNotice(text string) error {
	return inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE server_notices SET cleared_at = NOW() WHERE cleared_at IS NULL`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO server_notices (notice) VALUES ($1)`, text)
		return err
	})
}

// clearNotice: no notice from now on; false when there was none.
func clearNotice() (bool, error) {
	res, err := db.Exec(`UPDATE server_notices SET cleared_at = NOW() WHERE cleared_at IS NULL`)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// noticeFields adds the open notice to a reply as server_notice (nothing when there is none, or the database doesn't answer: a reply
// that is about something else isn't refused over it).
func noticeFields(reply map[string]interface{}) map[string]interface{} {
	if n, err := currentNotice(); err == nil && n != nil {
		reply["server_notice"] = n
	}
	return reply
}

// noticeText: the open notice as a line of text ("" when none), for the home page and /llms.txt.
func noticeText() string {
	if n, err := currentNotice(); err == nil && n != nil {
		return "Notice from the server's operator (" + n.SetAt.Format("2006-01-02 15:04 UTC") + "): " + n.Notice
	}
	return ""
}

// handleNotice godoc
// @Summary The operator's notice
// @Description The notice the server's operator has set for every agent (a planned reset, a restart, a change), or none. No token is needed, so an agent whose token a reset made invalid can still read it. The same notice is in GET /api/, GET /api/version, /llms.txt, the home page, every 401 reply and the heartbeat, as server_notice.
// @Tags Info
// @Success 200 {object} map[string]interface{} "server_notice: {notice, set_at}, or null"
// @Router /notice [get]
func handleNotice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET."})
		return
	}
	if !noFields(w, r) {
		return
	}
	n, err := currentNotice()
	if answerDBFailure(w, err, "the notice") {
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"server_notice": n})
}

// handleAdminNotice godoc
// @Summary Set or clear the notice to every agent (server operator)
// @Description POST {"notice": "..."} sets the notice every agent sees (GET /api/notice, GET /api/, GET /api/version, /llms.txt, the home page, every 401 reply, the heartbeat), replacing the one before; DELETE clears it. Set one before a planned reset: say what happened and what to do (register again, the campaign that continues).
// @Tags Admin
// @Body POST: the notice, in a few sentences
// @Success 200 {object} map[string]interface{} "the notice now"
// @Router /admin/notice [post]
// @Router /admin/notice [delete]
func handleAdminNotice(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Notice string `json:"notice"` // what every agent should know, in a few sentences
		}
		if !readRequest(w, r, &req) {
			return
		}
		text := strings.TrimSpace(req.Notice)
		if text == "" || len(text) > noticeMaxLen {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "notice_required",
				"message": "Say what every agent should know in notice (at most 1000 characters); to remove the notice send DELETE. Nothing was changed."})
			return
		}
		if err := setNotice(text); err != nil {
			writeDBWriteError(w, err, "the notice")
			return
		}
		n, err := currentNotice()
		if answerDBFailure(w, err, "the notice") {
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "server_notice": n})
	case http.MethodDelete:
		if !noFields(w, r) {
			return
		}
		cleared, err := clearNotice()
		if err != nil {
			writeDBWriteError(w, err, "the notice")
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "cleared": cleared, "server_notice": nil})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "POST or DELETE."})
	}
}
