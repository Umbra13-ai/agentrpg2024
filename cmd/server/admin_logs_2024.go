package main

// The admin's Logs, Settings and an account's activity (owner, Nov 22). What the operator reads, never what a player sees:
//   - Reports: what players and GMs send to POST /api/feature-requests (bugs, errors, requests: the table feature_requests), with their status
//     (open, resolved, closed) changed here and written to the admin change log (kind "report").
//   - Errors: the API requests that failed, by account, route and status (request_errors, written by failureRecorder in withProtections for every
//     /api reply of 400 and over but 401 (sign-in failures have their own table) and 429; the error code of the reply is kept; 30 days). api_logs is
//     not a request log (only a few routes write it), so nothing reads it here.
//   - Settings: a read-only account of the running server (version, database, mail, counts); no secret, no ADMIN_PATH.
//   - Sign-in failures: sign_in_failures, written by recordAuthFailure (the one place a failed sign-in is recorded): the time, the address, the way
//     (token, signature, password, email_code) and the account it was aimed at when that is known (a wrong token names none). Kept 30 days.
//   - An account's activity: when it was first and last seen, its failed requests in the last day and week and the routes that fail most, its tokens
//     and its recent failed sign-ins.

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const logKeepDays = 30

var lastSignInPrune atomic.Int64 // unix seconds of the last prune of sign_in_failures

// logSignInFailure writes one failed sign-in (recordAuthFailure calls it). A database failure is logged and never changes the answer to the request.
func logSignInFailure(ip string, agentID int, how string) {
	if db == nil {
		return
	}
	var agent interface{}
	if agentID > 0 {
		agent = agentID
	}
	if _, err := db.Exec(`INSERT INTO sign_in_failures (ip, agent_id, how) VALUES ($1, $2, $3)`, ip, agent, how); err != nil {
		handledDBError(err)
		return
	}
	pruneLogs()
}

// pruneLogs forgets what is older than logKeepDays, at most once an hour.
func pruneLogs() {
	if now := time.Now().Unix(); now-lastSignInPrune.Load() > 3600 && lastSignInPrune.CompareAndSwap(lastSignInPrune.Load(), now) {
		for _, t := range []string{"sign_in_failures", "request_errors"} {
			db.Exec(`DELETE FROM `+t+` WHERE at < NOW() - ($1 || ' days')::interval`, strconv.Itoa(logKeepDays))
		}
	}
}

// failureRecorder wraps a request's ResponseWriter to see its status and the start of its body, so withProtections can record a failed request.
type failureRecorder struct {
	http.ResponseWriter
	status int
	head   []byte
}

func (f *failureRecorder) WriteHeader(code int) {
	if f.status == 0 {
		f.status = code
	}
	f.ResponseWriter.WriteHeader(code)
}

func (f *failureRecorder) Write(b []byte) (int, error) {
	if f.status == 0 {
		f.status = http.StatusOK
	}
	if room := 32768 - len(f.head); room > 0 { // the reply's "error" can follow a long list: JSON keys come in order
		if len(b) < room {
			room = len(b)
		}
		f.head = append(f.head, b[:room]...)
	}
	return f.ResponseWriter.Write(b)
}

func (f *failureRecorder) Unwrap() http.ResponseWriter { return f.ResponseWriter }

var (
	pathCampaignRe  = regexp.MustCompile(`/campaigns/(\d+)`)
	pathCharacterRe = regexp.MustCompile(`/characters/(\d+)`)
	errorCodeRe     = regexp.MustCompile(`"error"\s*:\s*"([^"]{1,80})"`)
)

// logRequestFailure records a failed API request (withProtections calls it after the reply): the account that was signed in, the route and the status,
// and the reply's error code. A 401 is a sign-in failure (sign_in_failures) and a 429 a throttle: neither is written here.
func (f *failureRecorder) logRequestFailure(r *http.Request) {
	if db == nil || f.status < 400 || f.status == http.StatusUnauthorized || f.status == http.StatusTooManyRequests || !strings.HasPrefix(r.URL.Path, "/api/") {
		return
	}
	var agent, lobby, char interface{}
	if r.Header.Get("Authorization") != "" { // the route may have answered before signing in (a missing record): the account is still named (signed in once per request)
		if id, err := getAgentFromAuth(r); err == nil && id > 0 {
			agent = id
		}
	}
	if g := pathCampaignRe.FindStringSubmatch(r.URL.Path); g != nil {
		lobby, _ = strconv.Atoi(g[1])
	}
	if g := pathCharacterRe.FindStringSubmatch(r.URL.Path); g != nil {
		char, _ = strconv.Atoi(g[1])
	}
	code := ""
	if g := errorCodeRe.FindSubmatch(f.head); g != nil {
		code = string(g[1])
	}
	if _, err := db.Exec(`INSERT INTO request_errors (agent_id, method, route, status, error, lobby_id, character_id) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		agent, r.Method, r.URL.Path, f.status, code, lobby, char); err != nil {
		handledDBError(err)
		return
	}
	pruneLogs()
}

// adminLimit: the ?limit= of a list (default 100, at most 500).
func adminLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 500 {
		return 100
	}
	return n
}

// handleAdminReports godoc
// handleAdminReports: GET /api/admin/reports[?status=open|resolved|closed|all&q=&limit=] lists what players and GMs reported (bugs, errors, requests),
// POST /api/admin/reports/{id} {"status": "open|resolved|closed", "note": "why"} changes one's status.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/reports [get]
// @Router /admin/reports/{id} [post]
func handleAdminReports(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/reports"), "/")
	if rest != "" {
		id, err := strconv.Atoi(rest)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_id"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
			return
		}
		var req struct {
			Status string `json:"status"` // open, resolved or closed
			Note   string `json:"note"`   // why: written to the admin change log
		}
		if !readRequest(w, r, &req) {
			return
		}
		if req.Status != "open" && req.Status != "resolved" && req.Status != "closed" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_status", "message": "status is open, resolved or closed."})
			return
		}
		var old, title string
		err = inTx(func(tx *sql.Tx) error {
			if err := tx.QueryRow(`SELECT COALESCE(status,''), title FROM feature_requests WHERE id = $1 FOR UPDATE`, id).Scan(&old, &title); err != nil {
				if dbFailed(err) {
					return dbFailureError(err, "the report")
				}
				return fmt.Errorf("report %d not found", id)
			}
			if _, err := tx.Exec(`UPDATE feature_requests SET status = $1, updated_at = NOW() WHERE id = $2`, req.Status, id); err != nil {
				return dbFailureError(err, "the report")
			}
			if old == req.Status {
				return nil
			}
			return logAdminChangeIn(tx, "report", id, "status", old, req.Status, "admin", req.Note)
		})
		if answerHelperDBFailure(w, err) {
			return
		}
		if err != nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "report_not_found", "message": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "id": id, "status": req.Status, "was": old})
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	q := "%" + strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))) + "%"
	rows, err := db.Query(`SELECT f.id, f.title, f.body, COALESCE(f.status,''), COALESCE(f.agent_id,0), COALESCE(a.name,''), COALESCE(f.lobby_id,0), COALESCE(l.name,''),
		COALESCE(f.character_id,0), COALESCE(c.name,''), to_char(f.created_at,'YYYY-MM-DD HH24:MI'), to_char(f.updated_at,'YYYY-MM-DD HH24:MI')
		FROM feature_requests f LEFT JOIN agents a ON a.id = f.agent_id LEFT JOIN lobbies l ON l.id = f.lobby_id LEFT JOIN characters c ON c.id = f.character_id
		WHERE ($1 = 'all' OR f.status = $1) AND (LOWER(f.title) LIKE $2 OR LOWER(f.body) LIKE $2 OR LOWER(COALESCE(a.name,'')) LIKE $2 OR f.id::text = TRIM(BOTH '%' FROM $2))
		ORDER BY f.id DESC LIMIT $3`, status, q, adminLimit(r))
	if err != nil {
		writeDBError(w, err, "the reports")
		return
	}
	defer rows.Close()
	list := []map[string]interface{}{}
	for rows.Next() {
		var id, agent, lobby, char int
		var title, body, st, agentName, lobbyName, charName, created, updated string
		rows.Scan(&id, &title, &body, &st, &agent, &agentName, &lobby, &lobbyName, &char, &charName, &created, &updated)
		list = append(list, map[string]interface{}{"id": id, "title": title, "body": body, "status": st, "account_id": agent, "account": agentName,
			"campaign_id": lobby, "campaign": lobbyName, "character_id": char, "character": charName, "created_at": created, "updated_at": updated})
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"reports": list})
}

// handleAdminErrors godoc
// handleAdminErrors: GET /api/admin/errors[?account_id=&min_status=400&q=&limit=] lists the API requests that failed, newest first (not sign-in failures).
// The words of this route are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/errors [get]
func handleAdminErrors(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	min, _ := strconv.Atoi(r.URL.Query().Get("min_status"))
	if min < 400 || min > 599 {
		min = 400
	}
	account, _ := strconv.Atoi(r.URL.Query().Get("account_id"))
	q := "%" + strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))) + "%"
	rows, err := db.Query(`SELECT x.id, to_char(x.at,'YYYY-MM-DD HH24:MI:SS'), COALESCE(x.agent_id,0), COALESCE(a.name,''), x.method, x.route, x.status, COALESCE(x.error,''),
		COALESCE(x.lobby_id,0), COALESCE(x.character_id,0)
		FROM request_errors x LEFT JOIN agents a ON a.id = x.agent_id
		WHERE x.status >= $1 AND ($2 = 0 OR x.agent_id = $2) AND (LOWER(x.route) LIKE $3 OR LOWER(COALESCE(x.error,'')) LIKE $3)
		ORDER BY x.id DESC LIMIT $4`, min, account, q, adminLimit(r))
	if err != nil {
		writeDBError(w, err, "the errors")
		return
	}
	defer rows.Close()
	list := []map[string]interface{}{}
	for rows.Next() {
		var id, agent, status, lobby, char int
		var at, name, method, route, code string
		rows.Scan(&id, &at, &agent, &name, &method, &route, &status, &code, &lobby, &char)
		list = append(list, map[string]interface{}{"id": id, "at": at, "account_id": agent, "account": name, "method": method, "route": route,
			"status": status, "error": code, "campaign_id": lobby, "character_id": char})
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"errors": list})
}

// handleAdminSettings godoc
// handleAdminSettings: GET /api/admin/settings is a read-only account of the running server.
// The words of this route are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/settings [get]
func handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	count := func(q string, args ...interface{}) int {
		var n int
		db.QueryRow(q, args...).Scan(&n)
		return n
	}
	start := time.Now()
	dbOK := db.Ping() == nil
	pingMS := time.Since(start).Milliseconds()
	mail := "not configured"
	if svc := mailServiceInUse(); svc != nil {
		mail = "configured"
	}
	byStatus := map[string]int{}
	if rows, err := db.Query(`SELECT COALESCE(status,''), COUNT(*) FROM lobbies WHERE deleted_at IS NULL GROUP BY 1`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var s string
			var n int
			rows.Scan(&s, &n)
			byStatus[s] = n
		}
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{
		"server": map[string]interface{}{"version": version, "build_time": buildTime, "started_at": serverStartTime, "go": runtime.Version(),
			"public_address": publicBaseURL(), "on_render": behindRenderProxy, "test_clock": testClockOn()},
		"database":       map[string]interface{}{"ok": dbOK, "ping_ms": pingMS, "spells": len(srdSpellsMemory)},
		"usage_baseline": usageBaseline(), // how agents use the server, read only (admin_baseline_2024.go)
		"mail":           map[string]interface{}{"status": mail},
		"admin":          map[string]interface{}{"page_on": func() bool { p, _ := adminPagePath(); return p != "" }()},
		"counts": map[string]interface{}{
			"accounts": count(`SELECT COUNT(*) FROM agents WHERE deleted_at IS NULL`), "accounts_deleted": count(`SELECT COUNT(*) FROM agents WHERE deleted_at IS NOT NULL`),
			"characters": count(`SELECT COUNT(*) FROM characters WHERE deleted_at IS NULL`), "campaigns_by_status": byStatus,
			"open_reports":        count(`SELECT COUNT(*) FROM feature_requests WHERE status = 'open'`),
			"requests_24h":        count(`SELECT COUNT(*) FROM api_logs WHERE created_at > NOW() - INTERVAL '24 hours'`),
			"errors_24h":          count(`SELECT COUNT(*) FROM api_logs WHERE created_at > NOW() - INTERVAL '24 hours' AND response_status >= 400`),
			"server_errors_24h":   count(`SELECT COUNT(*) FROM api_logs WHERE created_at > NOW() - INTERVAL '24 hours' AND response_status >= 500`),
			"failed_sign_ins_24h": count(`SELECT COUNT(*) FROM sign_in_failures WHERE at > NOW() - INTERVAL '24 hours'`),
		},
	})
}

// accountActivity: an account's activity (GET /api/admin/accounts/{id}/activity). The server keeps no log of every request, so: when it was last seen
// (last_seen, a token's last use, a failed request), its failed requests and the routes that fail most, its tokens and its failed sign-ins.
func accountActivity(w http.ResponseWriter, id int) {
	var name string
	var created time.Time
	var seen sql.NullTime
	if err := db.QueryRow(`SELECT COALESCE(name,''), created_at, last_seen FROM agents WHERE id = $1`, id).Scan(&name, &created, &seen); err != nil {
		if answerDBFailure(w, err, "the account") {
			return
		}
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "account_not_found"})
		return
	}
	count := func(q string, args ...interface{}) int {
		var n int
		db.QueryRow(q, args...).Scan(&n)
		return n
	}
	var lastError, lastToken sql.NullTime
	db.QueryRow(`SELECT MAX(at) FROM request_errors WHERE agent_id = $1`, id).Scan(&lastError)
	db.QueryRow(`SELECT MAX(last_used_at) FROM agent_tokens WHERE agent_id = $1`, id).Scan(&lastToken)
	last := ""
	for _, t := range []sql.NullTime{seen, lastError, lastToken} {
		if t.Valid && t.Time.Format("2006-01-02 15:04") > last {
			last = t.Time.Format("2006-01-02 15:04")
		}
	}
	e24 := count(`SELECT COUNT(*) FROM request_errors WHERE agent_id = $1 AND at > NOW() - INTERVAL '24 hours'`, id)
	e7 := count(`SELECT COUNT(*) FROM request_errors WHERE agent_id = $1 AND at > NOW() - INTERVAL '7 days'`, id)
	routes := []map[string]interface{}{}
	if rows, err := db.Query(`SELECT method || ' ' || regexp_replace(route, '/[0-9]+', '/{id}', 'g') AS r, COALESCE(MAX(NULLIF(error,'')),''), COUNT(*)
		FROM request_errors WHERE agent_id = $1 AND at > NOW() - INTERVAL '7 days' GROUP BY 1 ORDER BY 3 DESC LIMIT 8`, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var route, code string
			var n int
			rows.Scan(&route, &code, &n)
			routes = append(routes, map[string]interface{}{"route": route, "error": code, "failed": n})
		}
	}
	failures := []map[string]interface{}{}
	if rows, err := db.Query(`SELECT to_char(at,'YYYY-MM-DD HH24:MI:SS'), ip, how FROM sign_in_failures WHERE agent_id = $1 ORDER BY id DESC LIMIT 20`, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var at, ip, how string
			rows.Scan(&at, &ip, &how)
			failures = append(failures, map[string]interface{}{"at": at, "address": ip, "how": how})
		}
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{
		"account": name, "first_seen": created.Format("2006-01-02"), "last_active": last,
		"failed_requests_24h": e24, "failed_requests_7d": e7, "failing_routes": routes,
		"tokens": map[string]interface{}{"active": count(`SELECT COUNT(*) FROM agent_tokens WHERE agent_id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`, id),
			"signing_keys": count(`SELECT COUNT(*) FROM agent_keys WHERE agent_id = $1 AND revoked_at IS NULL`, id)},
		"failed_sign_ins": map[string]interface{}{"last_24h": count(`SELECT COUNT(*) FROM sign_in_failures WHERE agent_id = $1 AND at > NOW() - INTERVAL '24 hours'`, id),
			"last_30d": count(`SELECT COUNT(*) FROM sign_in_failures WHERE agent_id = $1`, id), "recent": failures},
	})
}
