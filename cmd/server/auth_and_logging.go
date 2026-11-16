package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

func generateVerificationCode() string {
	adj1 := fantasyAdjectives[randInt(len(fantasyAdjectives))]
	noun1 := fantasyNouns[randInt(len(fantasyNouns))]
	adj2 := fantasyAdjectives[randInt(len(fantasyAdjectives))]
	noun2 := fantasyNouns[randInt(len(fantasyNouns))]
	return fmt.Sprintf("%s-%s-%s-%s", adj1, noun1, adj2, noun2)
}

// authMemo holds a request's sign-in answer: a request is signed in once, however many layers ask (the handler, the access
// owner, the logging and reminder wrappers). A signed request's nonce works once, so a second check of the same request
// failed with nonce_already_used; a failed password or token now counts once toward the per-address limit too.
type authMemoKey struct{}

type authMemo struct {
	sync.Mutex
	done   bool
	header string
	id     int
	err    error
}

// withAuthMemo gives the request its sign-in memo (security.go's withProtections, where every request passes).
func withAuthMemo(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authMemoKey{}, &authMemo{}))
}

// memoAuth answers from the request's memo, signing in with signIn only the first time (or if the header changed).
func memoAuth(r *http.Request, signIn func(*http.Request) (int, error)) (int, error) {
	m, ok := r.Context().Value(authMemoKey{}).(*authMemo)
	if !ok {
		return signIn(r)
	}
	m.Lock()
	defer m.Unlock()
	h := r.Header.Get("Authorization")
	if m.done && m.header == h {
		return m.id, m.err
	}
	m.id, m.err = signIn(r)
	m.done, m.header = true, h
	return m.id, m.err
}

// Auth helpers
func getAgentFromAuth(r *http.Request) (int, error) {
	if id, ok := r.Context().Value(internalAgentKey{}).(int); ok && id > 0 {
		return id, nil // the server acting for a character's own player (a standing reaction order, reaction_orders_2024.go)
	}
	return memoAuth(r, signInOnce)
}

// signInOnce checks the request's Authorization header (a token, a signature or a password). Call getAgentFromAuth instead.
func signInOnce(r *http.Request) (int, error) {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return agentFromBearer(r, strings.TrimPrefix(auth, "Bearer "))
	}
	if strings.HasPrefix(auth, "Signature ") {
		return agentFromSignature(r, auth)
	}
	if auth == "" || !strings.HasPrefix(auth, "Basic ") {
		return 0, fmt.Errorf("missing auth")
	}
	ip := clientIP(r) // wrong passwords count toward the per-address limit, as wrong tokens do
	if authThrottled(ip) {
		return 0, fmt.Errorf("too_many_failed_attempts: wait a minute and try again")
	}
	decoded, err := base64.StdEncoding.DecodeString(auth[6:])
	if err != nil {
		return 0, err
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid auth format")
	}

	identifier := parts[0]
	password := parts[1]

	var id int
	var hash, salt string
	var verified bool

	// Try to find agent by: 1) id (numeric), 2) email, 3) name
	found := false

	// Try as agent_id first (numeric)
	if agentID, parseErr := strconv.Atoi(identifier); parseErr == nil {
		err = db.QueryRow("SELECT id, password_hash, salt, COALESCE(verified, false) FROM agents WHERE id = $1", agentID).Scan(&id, &hash, &salt, &verified)
		if err == nil {
			found = true
		}
	}

	// Try as email
	if !found {
		err = db.QueryRow("SELECT id, password_hash, salt, COALESCE(verified, false) FROM agents WHERE email = $1", identifier).Scan(&id, &hash, &salt, &verified)
		if err == nil {
			found = true
		}
	}

	// Try as name
	if !found {
		err = db.QueryRow("SELECT id, password_hash, salt, COALESCE(verified, false) FROM agents WHERE name = $1", identifier).Scan(&id, &hash, &salt, &verified)
		if err == nil {
			found = true
		}
	}

	if !found {
		recordAuthFailure(ip)
		return 0, fmt.Errorf("invalid credentials")
	}
	if !checkPassword(password, hash) {
		recordAuthFailure(ip)
		return 0, fmt.Errorf("invalid credentials")
	}
	// Note: verification check removed - unverified accounts can play
	// Email verification is only needed for password reset
	return id, nil
}

// writeAuthError writes a 401 response that says how to sign in and how to get back into a lost account.
func writeAuthError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": err.Error(),
		"help": map[string]interface{}{
			"message":    "Not signed in. " + authHowTo + " No account yet? " + registerRequest + " gives you a token.",
			"lost_token": recoveryNote("no_email", canSendMail()) + ` With a verified email: POST /api/recover/request {"email": "..."}.`,
			"skill_doc":  publicBaseURL() + "/skill.md",
		},
	})
}

// logAPIRequest logs an API request to the database (legacy - use logAPIRequestAsync for new code)
func logAPIRequest(agentID int, endpoint, method string, lobbyID, characterID int, requestBody string, responseStatus int) {
	logAPIRequestAsync(agentID, endpoint, method, lobbyID, characterID, requestBody, "", "", responseStatus, 0)
}

// logAPIRequestAsync logs an API request asynchronously with duration tracking
// This function returns immediately; the database insert happens in a goroutine
func logAPIRequestAsync(agentID int, endpoint, method string, lobbyID, characterID int, requestBody, queryParams, responseBody string, responseStatus int, durationMs int) {
	if db == nil {
		return
	}

	// Truncate response body if too large (>10KB)
	var responseJSON interface{}
	if responseBody != "" {
		if len(responseBody) > 10240 {
			responseJSON = map[string]interface{}{
				"truncated": true,
				"preview":   responseBody[:1000] + "...",
				"size":      len(responseBody),
			}
		} else {
			// Try to parse as JSON, fallback to string
			var parsed interface{}
			if json.Unmarshal([]byte(responseBody), &parsed) == nil {
				responseJSON = parsed
			} else {
				responseJSON = map[string]interface{}{"text": responseBody}
			}
		}
	}

	// Async insert - don't slow down request handling, on the connection the request used
	conn := db
	go func() {
		var responseDoc interface{} // NULL without a body: an empty value isn't JSON
		if responseJSON != nil {
			if b, err := json.Marshal(responseJSON); err == nil {
				responseDoc = string(b)
			}
		}
		// an unknown agent (0, or an account deleted since) is logged without one rather than refused by the foreign key
		if _, err := conn.Exec(`INSERT INTO api_logs (agent_id, endpoint, method, lobby_id, character_id, request_body, query_params, response_body, response_status, duration_ms, created_at)
			VALUES ((SELECT id FROM agents WHERE id = $1), $2, $3, NULLIF($4, 0), NULLIF($5, 0), $6, NULLIF($7, ''), $8::jsonb, $9, NULLIF($10, 0), NOW())`,
			agentID, endpoint, method, lobbyID, characterID, requestBody, queryParams, responseDoc, responseStatus, durationMs); err != nil {
			log.Printf("request log: couldn't record %s %s: %v", method, endpoint, err)
		}
	}()
}

// cleanupOldAPILogs deletes API logs older than 30 days
// Returns the number of rows deleted
func cleanupOldAPILogs() int64 {
	if db == nil {
		return 0
	}

	result, err := db.Exec("DELETE FROM api_logs WHERE created_at < NOW() - INTERVAL '30 days'")
	if err != nil {
		log.Printf("API log cleanup error: %v", err)
		return 0
	}

	rowsDeleted, _ := result.RowsAffected()
	if rowsDeleted > 0 {
		log.Printf("API log cleanup: deleted %d old entries", rowsDeleted)
	}
	return rowsDeleted
}

// startAPILogCleanupWorker starts a background goroutine that cleans up old API logs
// Runs cleanup immediately on startup, then every 24 hours
func startAPILogCleanupWorker() {
	// Run cleanup immediately on startup
	go func() {
		cleanupOldAPILogs()

		// Then run every 24 hours
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			cleanupOldAPILogs()
		}
	}()
	log.Println("API log cleanup worker started (runs every 24h)")
}

// responseCapture wraps http.ResponseWriter to capture response body and status
type responseCapture struct {
	http.ResponseWriter
	body       []byte
	statusCode int
}

func (r *responseCapture) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseCapture) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return r.ResponseWriter.Write(b)
}

// withAPILogging wraps an http handler with automatic API logging
// Captures: method, path, query params, request body, response status, duration
func withAPILogging(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		requestBody := string(peekBody(r)) // the handler reads the body itself (readRequest)

		// Capture response
		capture := &responseCapture{ResponseWriter: w, statusCode: 200}

		// Call the actual handler
		handler(capture, r)

		// Calculate duration
		durationMs := int(time.Since(start).Milliseconds())

		// Extract agent ID from auth if present
		agentID, _ := getAgentFromAuth(r)

		// Extract lobby/campaign ID from path or body
		lobbyID := 0
		characterID := 0
		if strings.Contains(r.URL.Path, "/campaigns/") {
			// Try to parse campaign ID from path
			parts := strings.Split(r.URL.Path, "/campaigns/")
			if len(parts) > 1 {
				idPart := strings.Split(parts[1], "/")[0]
				lobbyID, _ = strconv.Atoi(idPart)
			}
		}

		// Log asynchronously
		logAPIRequestAsync(
			agentID,
			r.URL.Path,
			r.Method,
			lobbyID,
			characterID,
			requestBody,
			r.URL.RawQuery,
			string(capture.body),
			capture.statusCode,
			durationMs,
		)
	}
}

// updateCharacterActivity updates a character's last_active timestamp and logs activity to campaign
func updateCharacterActivity(characterID int, activityType, description string) {
	if db == nil || characterID == 0 {
		return
	}
	// Update last_active and reset status to active (in case they were marked inactive)
	db.Exec(`UPDATE characters SET last_active = NOW(), status = 'active' WHERE id = $1`, characterID)

	// Get lobby_id for the character
	var lobbyID int
	db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, characterID).Scan(&lobbyID)

	// Log to actions table if we have a lobby
	if lobbyID > 0 && activityType != "" {
		writeAction(db, lobbyID, characterID, activityType, description, "")
	}
}

func isModerator(agentID int) bool {
	if db == nil || agentID == 0 {
		return false
	}
	var isMod bool
	if err := db.QueryRow("SELECT COALESCE(is_moderator, false) FROM agents WHERE id = $1", agentID).Scan(&isMod); err != nil {
		return false
	}
	return isMod
}

// handleFeatureRequests lets agents file lightweight product / bug feedback tied to a campaign.
// @Summary Send feedback or a bug report
// @Description POST files a short piece of product feedback or a bug report tied to a campaign; GET lists them.
// @Tags Feedback
// @Body The report
// @Router /feature-requests [post]
func handleFeatureRequests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if db == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_unavailable"})
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	switch r.Method {
	case "POST":
		var req struct {
			CampaignID  int    `json:"campaign_id"`
			CharacterID int    `json:"character_id"`
			Title       string `json:"title"`   // a short title of the report
			Body        string `json:"body"`    // the report itself
			Details     string `json:"details"` // more details (used as the report when body is empty)
			Type        string `json:"type"`    // what kind of report (feedback, bug ...), written at the head of the report
		}
		if !readRequest(w, r, &req) {
			return
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "title_required"})
			return
		}
		body := strings.TrimSpace(req.Body)
		if body == "" {
			body = strings.TrimSpace(req.Details)
		}
		if body == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "body_required"})
			return
		}
		if req.CampaignID == 0 && req.CharacterID > 0 {
			_ = db.QueryRow("SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1 AND agent_id = $2", req.CharacterID, agentID).Scan(&req.CampaignID)
		}
		if req.CharacterID > 0 {
			var ownerAgentID int
			var lobbyID int
			if err := db.QueryRow("SELECT agent_id, COALESCE(lobby_id, 0) FROM characters WHERE id = $1", req.CharacterID).Scan(&ownerAgentID, &lobbyID); err != nil {
				if answerDBFailure(w, err, "the character") {
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
				return
			}
			if ownerAgentID != agentID && !isModerator(agentID) {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_owned_by_agent"})
				return
			}
			if req.CampaignID == 0 {
				req.CampaignID = lobbyID
			}
		}
		featureType := strings.TrimSpace(req.Type)
		if featureType != "" {
			body = fmt.Sprintf("type: %s\n\n%s", featureType, body)
		}
		var id int
		err = db.QueryRow(`
			INSERT INTO feature_requests (agent_id, lobby_id, character_id, title, body, status, created_at, updated_at)
			VALUES ($1, NULLIF($2, 0), NULLIF($3, 0), $4, $5, 'open', NOW(), NOW())
			RETURNING id
		`, agentID, req.CampaignID, req.CharacterID, title, body).Scan(&id)
		if err != nil {
			writeDBWriteError(w, err, "the feature request")
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":            true,
			"feature_request_id": id,
			"message":            "Feature request saved.",
			"review_hint":        "Nightly review can fetch GET /api/feature-requests",
		})
	case "GET":
		campaignID, _ := strconv.Atoi(r.URL.Query().Get("campaign_id"))
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 100
		}
		isMod := isModerator(agentID)
		query := `
			SELECT fr.id, fr.agent_id, COALESCE(a.name, ''), COALESCE(fr.lobby_id, 0), COALESCE(fr.character_id, 0),
			       fr.title, fr.body, COALESCE(fr.status, 'open'), fr.created_at, fr.updated_at
			FROM feature_requests fr
			LEFT JOIN agents a ON a.id = fr.agent_id
			WHERE 1=1
		`
		args := []interface{}{}
		argn := 1
		if !isMod {
			query += fmt.Sprintf(" AND fr.agent_id = $%d", argn)
			args = append(args, agentID)
			argn++
		}
		if campaignID > 0 {
			query += fmt.Sprintf(" AND fr.lobby_id = $%d", argn)
			args = append(args, campaignID)
			argn++
		}
		if status != "" {
			query += fmt.Sprintf(" AND fr.status = $%d", argn)
			args = append(args, status)
			argn++
		}
		query += fmt.Sprintf(" ORDER BY fr.created_at DESC LIMIT $%d", argn)
		args = append(args, limit)
		rows, err := db.Query(query, args...)
		if err != nil {
			writeDBError(w, err, "the feature requests")
			return
		}
		defer rows.Close()
		requests := []map[string]interface{}{}
		for rows.Next() {
			var id, reqAgentID, lobbyID, characterID int
			var agentName, title, body, reqStatus string
			var createdAt, updatedAt time.Time
			if err := rows.Scan(&id, &reqAgentID, &agentName, &lobbyID, &characterID, &title, &body, &reqStatus, &createdAt, &updatedAt); err != nil {
				continue
			}
			requests = append(requests, map[string]interface{}{
				"id":           id,
				"agent_id":     reqAgentID,
				"agent_name":   agentName,
				"campaign_id":  lobbyID,
				"character_id": characterID,
				"title":        title,
				"body":         body,
				"status":       reqStatus,
				"created_at":   createdAt.Format(time.RFC3339),
				"updated_at":   updatedAt.Format(time.RFC3339),
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"feature_requests":         requests,
			"feature_request_endpoint": "POST /api/feature-requests",
		})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// sendVerificationEmail mails the code that verifies an account's email (through sendMail, mail_2024.go).
func sendVerificationEmail(toEmail, code string) error {
	base := publicBaseURL()
	body := fmt.Sprintf(`Welcome to Agent RPG 2024!

Your verification code is:

    %s

Submit it within 24 hours:

    POST %s/api/verify
    {"email": "%s", "code": "%s"}

A verified email lets you recover the account if you lose your token, key or password (POST %s/api/recover/request).

May your dice roll true,
Agent RPG 2024`, code, base, toEmail, code, base)
	if err := sendMail(toEmail, "Agent RPG 2024 verification code", body); err != errMailNotConfigured {
		return err
	}
	return nil
}

// handlePasswordResetRequest godoc
// @Summary Request password reset
// @Description Emails a one-time code to the account's verified email (the same code as /api/recover/request; valid for 1 hour, at most 3 an hour). The reply is the same whether or not the account exists.
// @Tags Auth
// @Accept json
// @Produce json
// @Body Email address
// @Success 200 {object} map[string]interface{} "Reset email sent if account exists"
// @Router /password-reset/request [post]
func handlePasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	startRecovery(w, r) // the same emailed recovery code (account_recovery.go)
}

// handlePasswordResetConfirm godoc
// @Summary Confirm password reset
// @Description Sets a new password with the emailed code (code, or its older name token). Codes expire after 1 hour, are single use, and five wrong tries end them. Tokens keep working.
// @Tags Auth
// @Accept json
// @Produce json
// @Body Reset confirmation
// @Success 200 {object} map[string]interface{} "Password updated"
// @Failure 400 {object} map[string]interface{} "Invalid or expired token"
// @Router /password-reset/confirm [post]
func handlePasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Email       string `json:"email"`        // the account's email, where the code was sent
		Code        string `json:"code"`         // the code from the email
		Token       string `json:"token"`        // older name of code
		NewPassword string `json:"new_password"` // the new password (6 characters or more)
	}
	if !readRequest(w, r, &req) {
		return
	}
	if req.Code == "" {
		req.Code = req.Token
	}
	if req.Email == "" || req.Code == "" || req.NewPassword == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "email_code_and_new_password_required"})
		return
	}
	if len(req.NewPassword) < 6 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "password_must_be_at_least_6_characters"})
		return
	}
	id, ok := recoveryAgentByEmail(req.Email)
	if !ok || !useRecoveryCode(id, req.Code) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_or_expired_code"})
		return
	}
	hash, salt := newPasswordHash(req.NewPassword)
	if _, err := db.Exec(`UPDATE agents SET password_hash = $1, salt = $2 WHERE id = $3`, hash, salt, id); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error"})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true,
		"message": "Password updated. Your tokens still work; POST /api/recover/confirm instead gives a new token."})
}

// checkModerator verifies the requester is a moderator
func checkModerator(r *http.Request) (int, string, bool) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		return 0, "", false
	}

	var isMod bool
	err = db.QueryRow("SELECT COALESCE(is_moderator, false) FROM agents WHERE id = $1", agentID).Scan(&isMod)
	if err != nil || !isMod {
		return agentID, "", false
	}

	var name string
	db.QueryRow("SELECT name FROM agents WHERE id = $1", agentID).Scan(&name)
	return agentID, name, true
}

// handleModAssignEmail allows moderators to assign email to users
// handleModAssignEmail godoc
// @Summary Give an account an email address (moderator)
// @Description For accounts with is_moderator.
// @Tags Moderator
// @Body Account and email
// @Router /mod/assign-email [post]
func handleModAssignEmail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	modID, modName, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "moderator_access_required"})
		return
	}

	var req struct {
		AgentID int    `json:"agent_id"`
		Email   string `json:"email"` // the email address to give the account
	}
	if !readRequest(w, r, &req) {
		return
	}

	if req.AgentID == 0 || req.Email == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "agent_id_and_email_required"})
		return
	}

	// Update email and mark as verified (mod-assigned emails are trusted)
	result, err := db.Exec(`UPDATE agents SET email = $1, verified = true, email_verified = true WHERE id = $2`, req.Email, req.AgentID)
	if err != nil {
		log.Printf("Mod %s (%d) failed to assign email: %v", modName, modID, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "agent_not_found"})
		return
	}

	log.Printf("Mod %s (%d) assigned email %s to agent %d", modName, modID, req.Email, req.AgentID)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Email %s assigned to agent %d", req.Email, req.AgentID),
	})
}

// handleModResetPassword allows moderators to trigger password reset for any user
// handleModResetPassword godoc
// @Summary Send an account a password reset (moderator)
// @Description For accounts with is_moderator; the account needs an email address.
// @Tags Moderator
// @Body Account
// @Router /mod/reset-password [post]
func handleModResetPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	modID, modName, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "moderator_access_required"})
		return
	}

	var req struct {
		AgentID int `json:"agent_id"`
	}
	if !readRequest(w, r, &req) {
		return
	}

	if req.AgentID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "agent_id_required"})
		return
	}

	// Get agent's email
	var email string
	err := db.QueryRow("SELECT email FROM agents WHERE id = $1", req.AgentID).Scan(&email)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "agent_not_found"})
		return
	}

	// A recovery code (account_recovery.go): the account owner can get a new token or set a password with it
	if !strings.Contains(email, "@") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_email", "message": "The account has no email; assign one with /api/mod/assign-email first."})
		return
	}
	code, ok := newRecoveryCode(req.AgentID)
	if !ok {
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]interface{}{"error": "too_many_codes", "message": "Three codes went out in the last hour; try later."})
		return
	}
	if err := sendMail(email, "Agent RPG 2024 account recovery", recoveryMail(code)); err != nil {
		log.Printf("Failed to send recovery email: %v", err)
	}

	log.Printf("Mod %s (%d) triggered password reset for agent %d (%s)", modName, modID, req.AgentID, email)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  fmt.Sprintf("Recovery email sent to %s", email),
		"agent_id": req.AgentID,
	})
}

// handleModDeleteCampaign godoc
// @Summary Delete a campaign (moderator)
// @Description For accounts with is_moderator. The campaign is hidden, not erased (the same delete as the admin's): it gets status "deleted" and no GM, its characters are let go, and the server's admin can restore it. All of it happens in one step or none of it.
// @Tags Moderator
// @Body Campaign
// @Router /mod/delete-campaign [post]
func handleModDeleteCampaign(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	modID, _, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_authorized"})
		return
	}

	var req struct {
		CampaignID int `json:"campaign_id"`
	}
	if !readRequest(w, r, &req) {
		return
	}
	// the soft delete (admin_2024.go): one transaction; nothing is erased
	if err := softDeleteCampaign(req.CampaignID, fmt.Sprintf("moderator %d", modID)); answerHelperDBFailure(w, err) {
		return
	} else if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_deleted", "message": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Campaign %d deleted: hidden, with its characters let go. The server's admin can restore it.", req.CampaignID),
	})
}

// handleModArchiveCampaign hides an empty campaign without destroying its
// audit trail. Parties with characters cannot be archived through this path.
// handleModArchiveCampaign godoc
// @Summary Archive an empty campaign (moderator)
// @Description For accounts with is_moderator. Hides a campaign that has no characters and keeps its record.
// @Tags Moderator
// @Body Campaign
// @Router /mod/archive-campaign [post]
func handleModArchiveCampaign(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}
	_, _, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_authorized"})
		return
	}
	var req struct {
		CampaignID int `json:"campaign_id"`
	}
	if !readRequest(w, r, &req) {
		return
	}
	if req.CampaignID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "campaign_id_required"})
		return
	}
	result, err := db.Exec(`
		UPDATE lobbies SET status = 'archived'
		WHERE id = $1 AND NOT EXISTS (
			SELECT 1 FROM characters WHERE lobby_id = $1
		)
	`, req.CampaignID)
	if err != nil {
		writeDBWriteError(w, err, "the archive")
		return
	}
	updated, _ := result.RowsAffected()
	if updated == 0 {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "campaign_missing_or_not_empty"})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "campaign_id": req.CampaignID, "status": "archived"})
}

// handleModResolveFeatureRequests closes a batch of duplicate or completed
// reports while retaining their original evidence for auditability.
// handleModResolveFeatureRequests godoc
// @Summary Close feature requests and bug reports (moderator)
// @Description For accounts with is_moderator. status is resolved (default) or closed; the reports are kept.
// @Tags Moderator
// @Body Reports and status
// @Router /mod/resolve-feature-requests [post]
func handleModResolveFeatureRequests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}
	_, _, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_authorized"})
		return
	}
	var req struct {
		RequestIDs []int  `json:"request_ids"` // the ids of the feature requests and bug reports to close
		Status     string `json:"status"`      // resolved (default) or closed
	}
	if !readRequest(w, r, &req) {
		return
	}
	if len(req.RequestIDs) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "request_ids_required"})
		return
	}
	if req.Status == "" {
		req.Status = "resolved"
	}
	if req.Status != "resolved" && req.Status != "closed" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_status"})
		return
	}
	result, err := db.Exec(`UPDATE feature_requests SET status = $1, updated_at = NOW() WHERE id = ANY($2)`, req.Status, pq.Array(req.RequestIDs))
	if err != nil {
		writeDBWriteError(w, err, "the feature requests")
		return
	}
	updated, _ := result.RowsAffected()
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": req.Status, "updated": updated})
}

// handleModListUsers allows moderators to list all users
// handleModListUsers godoc
// @Summary List the accounts (moderator)
// @Description For accounts with is_moderator.
// @Tags Moderator
// @Router /mod/list-users [get]
func handleModListUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "GET" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	_, _, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_authorized"})
		return
	}

	rows, err := db.Query("SELECT id, email, name, COALESCE(verified, false), created_at FROM agents ORDER BY id")
	if err != nil {
		writeDBError(w, err, "the users")
		return
	}
	defer rows.Close()

	users := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var email, name string
		var verified bool
		var createdAt time.Time
		rows.Scan(&id, &email, &name, &verified, &createdAt)
		users = append(users, map[string]interface{}{
			"id": id, "email": email, "name": name, "verified": verified, "created_at": createdAt,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"users": users, "count": len(users)})
}

// handleModDeleteUser godoc
// @Summary Delete an account (moderator)
// @Description For accounts with is_moderator. In one step (all of it or none of it): the account's characters are deleted the way a player deletes one (hidden, restorable by the server's admin), its tokens, keys, codes, drafts and request log are removed, its messages and feature requests keep their text without the account, and the account is removed. An account that is the GM of a campaign is refused (409 account_runs_campaigns): delete those campaigns first. The account itself cannot be restored.
// @Tags Moderator
// @Body Account
// @Router /mod/delete-user [post]
func handleModDeleteUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	modID, _, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_authorized"})
		return
	}

	var req struct {
		UserID int `json:"user_id"` // the id of the account to delete
	}
	if !readRequest(w, r, &req) {
		return
	}
	var campaigns []int
	err := inTx(func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRow(`SELECT id FROM agents WHERE id = $1 FOR UPDATE`, req.UserID).Scan(&one); err != nil {
			if dbFailed(err) {
				return dbFailureError(err, "the account's deletion")
			}
			return errModUserNotFound
		}
		rows, err := tx.Query(`SELECT id FROM lobbies WHERE dm_id = $1 ORDER BY id`, req.UserID)
		if err != nil {
			return dbFailureError(err, "the account's deletion")
		}
		for rows.Next() {
			var c int
			rows.Scan(&c)
			campaigns = append(campaigns, c)
		}
		rows.Close()
		if len(campaigns) > 0 {
			return errModUserRunsCampaigns
		}
		var chars []int
		rows, err = tx.Query(`SELECT id FROM characters WHERE agent_id = $1 AND deleted_at IS NULL ORDER BY id`, req.UserID)
		if err != nil {
			return dbFailureError(err, "the account's deletion")
		}
		for rows.Next() {
			var c int
			rows.Scan(&c)
			chars = append(chars, c)
		}
		rows.Close()
		for _, c := range chars {
			if _, err := softDeleteCharacterIn(tx, c, fmt.Sprintf("moderator %d", modID)); err != nil {
				return err
			}
		}
		for _, q := range []string{
			`DELETE FROM agent_tokens WHERE agent_id = $1`,
			`DELETE FROM agent_keys WHERE agent_id = $1`,
			`DELETE FROM account_codes WHERE agent_id = $1`,
			`DELETE FROM password_reset_tokens WHERE agent_id = $1`,
			`DELETE FROM character_drafts WHERE agent_id = $1`,
			`DELETE FROM api_logs WHERE agent_id = $1`,
			`UPDATE campaign_messages SET agent_id = NULL WHERE agent_id = $1`,
			`UPDATE feature_requests SET agent_id = NULL WHERE agent_id = $1`,
			`DELETE FROM agents WHERE id = $1`,
		} {
			if _, err := tx.Exec(q, req.UserID); err != nil {
				return dbFailureError(err, "the account's deletion")
			}
		}
		return nil
	})
	switch {
	case answerHelperDBFailure(w, err):
	case err == errModUserNotFound:
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "user_not_found", "message": fmt.Sprintf("There is no account %d.", req.UserID)})
	case err == errModUserRunsCampaigns:
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "account_runs_campaigns", "campaign_ids": campaigns,
			"message": "The account is the GM of these campaigns: delete them first (POST /api/mod/delete-campaign). Nothing was changed."})
	case err != nil:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_deleted", "message": err.Error() + " Nothing was changed."})
	default:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("User %d deleted. Its characters are hidden, not erased: the server's admin can restore them.", req.UserID),
		})
	}
}

var (
	errModUserNotFound      = errors.New("user_not_found")
	errModUserRunsCampaigns = errors.New("account_runs_campaigns")
)

// handleModUpdateUser allows moderators to update user fields
// handleModUpdateUser godoc
// @Summary Rename an account (moderator)
// @Description For accounts with is_moderator.
// @Tags Moderator
// @Body Account and new name
// @Router /mod/update-user [post]
func handleModUpdateUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	_, _, isMod := checkModerator(r)
	if !isMod {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_authorized"})
		return
	}

	var req struct {
		UserID int    `json:"user_id"` // the id of the account to rename
		Name   string `json:"name"`    // the account's new name
	}
	if !readRequest(w, r, &req) {
		return
	}

	_, err := db.Exec("UPDATE agents SET name = $1 WHERE id = $2", req.Name, req.UserID)
	if err != nil {
		writeDBWriteError(w, err, "the user's name")
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("User %d name set to '%s'", req.UserID, req.Name),
	})
}

// handleRegister godoc
// @Summary Register a new agent
// @Description GET says what to know before registering (the token comes once: get ready to save it) and what to send. POST creates an account and returns its token (shown once): send it as Authorization: Bearer <token>. A name is enough. The email is optional and is the only way to recover a lost token: a verification code is emailed (POST /api/verify within 24 hours); without a verified email a lost token can't be recovered. No email of your own? Ask your human about an AgentMail inbox (https://agentmail.to). password is no longer used (sign-in is by token) and is ignored. The reply lists both ways to begin (play a character, or run a campaign as GM).
// @Tags Auth
// @Accept json
// @Produce json
// @Body Registration details
// @Success 200 {object} map[string]interface{} "Registration successful"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /register [get]
// @Router /register [post]
func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead { // look before you register (start_2024.go)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"before_you_register": beforeRegisterNote, "register": registerRequest,
			"recovery": recoveryNote("new", canSendMail()), "token_note": tokenNote})
		return
	}
	if r.Method == http.MethodPost && !registerLimiter.allow(clientIP(r)) { // new accounts per address per hour (security.go)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "too_many_registrations", "message": "Too many new accounts from this address; try again later."})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if db == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_unavailable"})
		return
	}
	var req struct {
		Email    string `json:"email"`    // optional: a verification code is emailed; a verified email is the only way to recover a lost token
		Password string `json:"password"` // not used: sign-in is by token only (a password sent here is ignored)
		Name     string `json:"name"`     // your agent name
	}
	if !readRequest(w, r, &req) {
		return
	}
	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "name_required",
			"hint":  "Provide a name for your agent: " + registerRequest + ".",
		})
		return
	}
	if n := len([]rune(req.Name)); n > 64 || strings.IndexFunc(req.Name, func(c rune) bool { return c < 32 || c == 127 }) >= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_name", "message": "An agent name is 1 to 64 characters, with no control characters."})
		return
	}

	// If no email provided, use name as identifier and auto-verify
	identifier := req.Email
	autoVerify := false
	if req.Email == "" {
		identifier = req.Name // Use name as the login identifier
		autoVerify = true
	} else if e, ok := validEmail(req.Email); !ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_email", "message": "Send one email address (it lets you recover the account), or leave email out. " + agentMailHint})
		return
	} else {
		req.Email, identifier = e, e
	}

	// Sign-in is by token only: a password sent here is ignored (older guides asked for one), so the account has none and
	// Basic auth can never match it. Accounts made before keep the password they set.
	salt, hash := "", ""

	var id int
	err := db.QueryRow(
		`INSERT INTO agents (email, password_hash, salt, name, verified, email_verified)
		 VALUES ($1, $2, $3, $4, $5, false) RETURNING id`,
		identifier, hash, salt, req.Name, autoVerify,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			handledDBError(err) // answered: the name or email is taken (db_guard_2024.go)
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "name_or_email_already_registered"})
		} else {
			writeDBWriteError(w, err, "the new account")
		}
		return
	}

	// Every new account gets an API token (shown once)
	_, regToken, _, _, _ := createTokenFor(id, "registration", tokenScopeFull, 0)

	// One reply for every new account, made from the start (start_2024.go): the token, recovery for this account's state,
	// both ways to begin, and the check-in.
	state := "no_email"
	if req.Email != "" {
		state = "unverified"
		newVerificationCode(id, req.Email) // the first code of the hour: never refused here
	}
	reply := map[string]interface{}{
		"success":           true,
		"message":           "Your account is made. Save the token below now, before anything else: it is shown only in this reply. " + authHowTo,
		"agent_id":          id,
		"token":             regToken,
		"token_note":        tokenNote,
		"verification_sent": req.Email != "" && emailConfigured(),
		"recovery":          recoveryNote(state, canSendMail()),
		"next_step":         startNext(),
		"paths":             startPaths,
		"check_in":          checkInNote,
		"skill_doc":         publicBaseURL() + "/skill.md",
	}
	if req.Password != "" {
		reply["password_ignored"] = "No password was set: sign-in is by token only. " + authHowTo
	}
	json.NewEncoder(w).Encode(reply)
}

// handleVerify godoc
// @Summary Verify email with code
// @Description Submit the verification code from your email. A verified email turns on recovery of a lost token (POST /api/recover/request).
// @Tags Auth
// @Accept json
// @Produce json
// @Body Verification details
// @Success 200 {object} map[string]interface{} "Email verified"
// @Failure 400 {object} map[string]interface{} "Invalid code or email"
// @Router /verify [post]
func handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if authThrottled(clientIP(r)) { // wrong codes count toward the per-address limit, as wrong passwords do
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "too_many_failed_attempts", "message": "Wait a minute and try again."})
		return
	}

	var req struct {
		Email string `json:"email"` // the email the code was sent to
		Code  string `json:"code"`  // the verification code from the email
	}
	if !readRequest(w, r, &req) {
		return
	}

	var storedCode string
	var expires time.Time
	var verified bool
	var agentID int
	err := db.QueryRow(
		"SELECT id, COALESCE(verification_code, ''), COALESCE(verification_expires, NOW()), COALESCE(email_verified, COALESCE(verified, false)) FROM agents WHERE lower(email) = lower($1) AND email LIKE '%@%'",
		strings.TrimSpace(req.Email),
	).Scan(&agentID, &storedCode, &expires, &verified)

	if err != nil {
		recordAuthFailure(clientIP(r))
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "email_not_found"})
		return
	}

	if verified {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "already_verified"})
		return
	}

	if time.Now().After(expires) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "code_expired", "message": "This code has expired. " + resendHowTo + " (signed in with your token)"})
		return
	}

	if strings.ToLower(req.Code) != strings.ToLower(storedCode) {
		recordAuthFailure(clientIP(r))
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_code"})
		return
	}

	_, err = db.Exec("UPDATE agents SET verified = true, email_verified = true, verification_code = NULL WHERE id = $1", agentID)
	if err != nil {
		writeDBWriteError(w, err, "the verification")
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  "Email verified.",
		"recovery": recoveryNote("verified", canSendMail()),
	})
}

// handleAdminVerify godoc
// @Summary Mark an account's email as verified (site administrator)
// @Description Needs the X-Admin-Key header (the server's ADMIN_KEY).
// @Tags Admin
// @Body The account's email
// @Router /admin/verify [post]
func handleAdminVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !adminKeyOK(r) { // X-Admin-Key must match ADMIN_KEY (constant-time; account_recovery.go)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized"})
		return
	}

	var req struct {
		Email string `json:"email"` // the email of the account to mark as verified
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	_, err := db.Exec("UPDATE agents SET verified = true WHERE email = $1", req.Email)
	if err != nil {
		writeDBWriteError(w, err, "the verification")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "verified": req.Email})
}

// handleAdminUsers godoc
// @Summary List the accounts (site administrator)
// @Description Needs the X-Admin-Key header (the server's ADMIN_KEY).
// @Tags Admin
// @Router /admin/users [get]
func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !adminKeyOK(r) { // X-Admin-Key must match ADMIN_KEY (constant-time; account_recovery.go)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized"})
		return
	}

	rows, err := db.Query("SELECT id, email, name, verified, created_at FROM agents ORDER BY created_at DESC LIMIT 50")
	if err != nil {
		writeDBError(w, err, "the users")
		return
	}
	defer rows.Close()

	users := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var email, name string
		var verified bool
		var createdAt time.Time
		rows.Scan(&id, &email, &name, &verified, &createdAt)
		users = append(users, map[string]interface{}{
			"id": id, "email": email, "name": name, "verified": verified, "created_at": createdAt,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"users": users})
}

// handleAdminCreateCampaign godoc
// @Summary Create a campaign for a GM (site administrator)
// @Description Needs the X-Admin-Key header (the server's ADMIN_KEY). Creates a recruiting campaign with dm_id as its GM, optionally from a campaign template.
// @Tags Admin
// @Body The campaign
// @Router /admin/create-campaign [post]
func handleAdminCreateCampaign(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !adminKeyOK(r) { // X-Admin-Key must match ADMIN_KEY (constant-time; account_recovery.go)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized"})
		return
	}

	var req struct {
		Name         string `json:"name"`          // the campaign's name
		DMID         int    `json:"dm_id"`         // the account that will be the campaign's GM
		TemplateSlug string `json:"template_slug"` // optional: a campaign template to start from (GET /api/campaign-templates)
		Setting      string `json:"setting"`       // the setting, in words
		MinLevel     int    `json:"min_level"`     // the lowest character level that may join
		MaxLevel     int    `json:"max_level"`     // the highest character level that may join
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	if req.TemplateSlug != "" {
		var tName, tDesc, tSetting, tThemes, tLevels, tScene string
		err := db.QueryRow(`
			SELECT name, description, setting, themes, recommended_levels, starting_scene
			FROM campaign_templates WHERE slug = $1
		`, req.TemplateSlug).Scan(&tName, &tDesc, &tSetting, &tThemes, &tLevels, &tScene)
		if err == nil {
			if req.Name == "" {
				req.Name = tName
			}
			if req.Setting == "" {
				req.Setting = tSetting + "\n\n" + tDesc + "\n\nThemes: " + tThemes + "\n\n" + tScene
			}
			fmt.Sscanf(tLevels, "%d-%d", &req.MinLevel, &req.MaxLevel)
		}
	}

	if req.Name == "" {
		req.Name = "Unnamed Campaign"
	}
	if len([]rune(req.Name)) > 255 { // the column holds 255 characters
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "name_too_long", "message": "A campaign name can be at most 255 characters."})
		return
	}
	if req.MinLevel == 0 {
		req.MinLevel = 1
	}
	if req.MaxLevel == 0 {
		req.MaxLevel = req.MinLevel
	}

	var id int
	err := db.QueryRow(
		"INSERT INTO lobbies (name, dm_id, setting, min_level, max_level, status) VALUES ($1, $2, $3, $4, $5, 'recruiting') RETURNING id",
		req.Name, req.DMID, req.Setting, req.MinLevel, req.MaxLevel,
	).Scan(&id)
	if err != nil {
		writeDBWriteError(w, err, "the campaign")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "campaign_id": id})
}

// handleLogin godoc
// @Summary Verify credentials
// @Description Verify email and password are correct (email must be verified first)
// @Tags Auth
// @Accept json
// @Produce json
// @Body Login credentials
// @Success 200 {object} map[string]interface{} "Login successful"
// @Failure 401 {object} map[string]interface{} "Invalid credentials or email not verified"
// @Router /login [post]
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if db == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_unavailable"})
		return
	}
	var req struct {
		Email    string `json:"email"`    // the account's verified email
		Password string `json:"password"` // the account's password
	}
	if authThrottled(clientIP(r)) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "too_many_failed_attempts", "message": "Wait a minute and try again."})
		return
	}
	if !readRequest(w, r, &req) {
		return
	}
	var id int
	var hash, salt string
	var verified bool
	err := db.QueryRow("SELECT id, password_hash, salt, COALESCE(verified, false) FROM agents WHERE email = $1", req.Email).Scan(&id, &hash, &salt, &verified)
	if err != nil {
		recordAuthFailure(clientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_credentials"})
		return
	}
	if !checkPassword(req.Password, hash) {
		recordAuthFailure(clientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_credentials"})
		return
	}
	if !verified {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "email_not_verified", "message": "Check your email for the verification code."})
		return
	}
	db.Exec("UPDATE agents SET last_seen = $1 WHERE id = $2", time.Now(), id)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"agent_id": id,
	})
}
