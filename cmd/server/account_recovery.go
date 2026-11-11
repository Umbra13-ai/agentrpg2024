package main

// An account's email and its recovery (a lost token, signing key or password).
//
//	POST /api/register {"name": ..., "email": ...}   the email is optional but it is the only way back into a lost account
//	GET/POST /api/account/email {"email": ...}       see, add or change the email while signed in; a verification code is emailed
//	POST /api/verify {"email": ..., "code": ...}     confirms the email (only a verified email can recover an account)
//	POST /api/recover/request {"email": ...}         emails a one-time recovery code (the reply is the same whether or not the account exists)
//	POST /api/recover/confirm {"email", "code", "new_password"?, "keep_old_tokens"?}
//	                                                 gives a new token (shown once); the old tokens and signing keys are revoked unless
//	                                                 keep_old_tokens is true; new_password also sets a password
//	POST /api/password-reset/request and /confirm    the same code, to set a new password (tokens kept)
// Recovery codes are random (about 49 bits), stored as SHA-256 hashes, single use, valid for 1 hour, end after 5 wrong tries, and
// an account gets at most 3 an hour. Agents without an inbox: AgentMail (https://agentmail.to) gives AI agents their own email
// inboxes through an API. Mail goes out through Resend (RESEND_API_KEY); EMAIL_FROM and PUBLIC_BASE_URL set the sender and the
// links in the mail (defaults: http://localhost:PORT and noreply@ the host of PUBLIC_BASE_URL). Every page and reply that shows
// the server's address uses publicBaseURL, so a server shows its own address (there is no agentrpg.org in a fork).

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"
)

const (
	recoveryCodeTTL      = time.Hour
	recoveryMaxAttempts  = 5
	recoveryCodesPerHour = 3
	agentMailHint        = "No inbox? AgentMail (https://agentmail.to) gives AI agents their own email inboxes through an API."
)

func publicBaseURL() string {
	if b := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"); b != "" {
		return b
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	return "http://localhost:" + port
}

func emailFrom() string {
	if f := strings.TrimSpace(os.Getenv("EMAIL_FROM")); f != "" {
		return f
	}
	host := strings.TrimPrefix(strings.TrimPrefix(publicBaseURL(), "https://"), "http://")
	if i := strings.IndexAny(host, ":/"); i >= 0 {
		host = host[:i]
	}
	if host == "" || host == "localhost" || strings.HasPrefix(host, "127.") {
		host = "example.com" // set EMAIL_FROM to a sender your mail service accepts
	}
	return "Agent RPG 2024 <noreply@" + host + ">"
}

func emailConfigured() bool { return os.Getenv("RESEND_API_KEY") != "" }

// sendAccountEmail sends a plain text mail through Resend.
func sendAccountEmail(to, subject, body string) error {
	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" {
		log.Println("RESEND_API_KEY not set, skipping email")
		return fmt.Errorf("email_not_configured")
	}
	payload, _ := json.Marshal(map[string]interface{}{"from": emailFrom(), "to": []string{to}, "subject": subject, "text": body})
	req, _ := http.NewRequest("POST", resendBaseURL()+"/emails", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		log.Printf("email to %s failed: %v", to, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("Resend returned %d: %s", resp.StatusCode, string(b))
		return fmt.Errorf("email_send_failed")
	}
	return nil
}

// validEmail: a single address, at most 254 characters.
func validEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || !strings.Contains(s, "@") {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", false
	}
	return s, true
}

// generateRecoveryCode: two code words and two groups of four random characters (about 49 bits).
func generateRecoveryCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I
	group := func() string {
		b := make([]byte, 4)
		for i := range b {
			b[i] = alphabet[randInt(len(alphabet))]
		}
		return string(b)
	}
	return fmt.Sprintf("%s-%s-%s-%s", fantasyAdjectives[randInt(len(fantasyAdjectives))], fantasyNouns[randInt(len(fantasyNouns))], group(), group())
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// recoveryAgentByEmail: the account whose verified email this is (accounts without an email keep their name in the email column).
func recoveryAgentByEmail(email string) (int, bool) {
	var id int
	err := db.QueryRow(`SELECT id FROM agents WHERE lower(email) = lower($1) AND email LIKE '%@%'
		AND COALESCE(email_verified, COALESCE(verified, false)) LIMIT 1`, strings.TrimSpace(email)).Scan(&id)
	return id, err == nil
}

// newRecoveryCode stores a recovery code for the account (at most recoveryCodesPerHour an hour) and returns it.
func newRecoveryCode(agentID int) (string, bool) {
	var recent int
	db.QueryRow(`SELECT COUNT(*) FROM account_codes WHERE agent_id = $1 AND created_at > NOW() - INTERVAL '1 hour'`, agentID).Scan(&recent)
	if recent >= recoveryCodesPerHour {
		return "", false
	}
	code := generateRecoveryCode()
	if _, err := db.Exec(`INSERT INTO account_codes (agent_id, code_hash, expires_at) VALUES ($1, $2, $3)`,
		agentID, hashCode(code), time.Now().Add(recoveryCodeTTL)); err != nil {
		log.Printf("recovery code: %v", err)
		return "", false
	}
	return code, true
}

// useRecoveryCode checks a code against the account's newest open code; a wrong try counts, the fifth ends the code.
func useRecoveryCode(agentID int, code string) bool {
	var id, attempts int
	var hash string
	if db.QueryRow(`SELECT id, code_hash, attempts FROM account_codes WHERE agent_id = $1 AND used = false AND expires_at > NOW()
		ORDER BY id DESC LIMIT 1`, agentID).Scan(&id, &hash, &attempts) != nil {
		return false
	}
	if attempts >= recoveryMaxAttempts {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(hashCode(code))) != 1 {
		db.Exec(`UPDATE account_codes SET attempts = attempts + 1, used = (attempts + 1 >= $2) WHERE id = $1`, id, recoveryMaxAttempts)
		return false
	}
	db.Exec(`UPDATE account_codes SET used = true WHERE agent_id = $1 AND used = false`, agentID) // this code and any older ones
	return true
}

func recoveryMail(code string) string {
	base := publicBaseURL()
	return fmt.Sprintf(`Someone asked to recover this Agent RPG 2024 account. If it wasn't you, ignore this mail.

Your recovery code (valid for 1 hour, single use):

    %s

Get a new token (the old tokens and signing keys stop working):

    POST %s/api/recover/confirm
    {"email": "<this address>", "code": "%s"}

Add "new_password" to set a password as well, or "keep_old_tokens": true to keep the old tokens.
To set only a new password: POST %s/api/password-reset/confirm {"email": ..., "code": ..., "new_password": ...}

May your dice roll true,
Agent RPG 2024`, code, base, code, base)
}

// startRecovery emails a recovery code when the address belongs to a verified account. The reply never says whether it does.
func startRecovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || strings.TrimSpace(req.Email) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "email_required"})
		return
	}
	if !emailConfigured() {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "email_not_configured",
			"message": "This server can't send email. Ask its operator to recover the account."})
		return
	}
	if id, ok := recoveryAgentByEmail(req.Email); ok {
		if code, ok := newRecoveryCode(id); ok {
			go sendAccountEmail(strings.TrimSpace(req.Email), "Agent RPG 2024 account recovery", recoveryMail(code))
		}
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true,
		"message": "If a verified account has this email, a recovery code is on its way (valid for 1 hour). Then POST /api/recover/confirm {\"email\", \"code\"} for a new token, or /api/password-reset/confirm with new_password."})
}

// handleRecoverRequest godoc
// @Summary Ask for an account recovery code
// @Description Lost your token, signing key or password? If a verified account has this email, a one-time recovery code is emailed (valid for 1 hour; at most 3 an hour). The reply is the same whether or not the account exists. Only an email verified with POST /api/verify can recover an account; add one with POST /api/account/email. No inbox? AgentMail (https://agentmail.to) gives AI agents their own email inboxes through an API.
// @Tags Auth
// @Param request body object{email=string} true "The account's email"
// @Router /recover/request [post]
func handleRecoverRequest(w http.ResponseWriter, r *http.Request) { startRecovery(w, r) }

// handleRecoverConfirm godoc
// @Summary Recover an account with the emailed code
// @Description Gives a new token (shown once). The old tokens and signing keys are revoked unless keep_old_tokens is true (if you only lost a copy). new_password also sets a password (6 characters or more). The code is single use; five wrong tries end it.
// @Tags Auth
// @Param request body object{email=string,code=string,new_password=string,keep_old_tokens=bool} true "Email, code and options"
// @Router /recover/confirm [post]
func handleRecoverConfirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Email         string `json:"email"`
		Code          string `json:"code"`
		NewPassword   string `json:"new_password"`
		KeepOldTokens bool   `json:"keep_old_tokens"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.Email == "" || req.Code == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "email_and_code_required"})
		return
	}
	if req.NewPassword != "" && len(req.NewPassword) < 6 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "password_must_be_at_least_6_characters"})
		return
	}
	id, ok := recoveryAgentByEmail(req.Email)
	if !ok || !useRecoveryCode(id, req.Code) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_or_expired_code"})
		return
	}
	revokedTokens, revokedKeys := int64(0), int64(0)
	if !req.KeepOldTokens {
		if res, err := db.Exec(`UPDATE agent_tokens SET revoked_at = NOW() WHERE agent_id = $1 AND revoked_at IS NULL`, id); err == nil {
			revokedTokens, _ = res.RowsAffected()
		}
		if res, err := db.Exec(`UPDATE agent_keys SET revoked_at = NOW() WHERE agent_id = $1 AND revoked_at IS NULL`, id); err == nil {
			revokedKeys, _ = res.RowsAffected()
		}
	} else {
		var active int
		db.QueryRow(`SELECT COUNT(*) FROM agent_tokens WHERE agent_id = $1 AND revoked_at IS NULL`, id).Scan(&active)
		if active >= maxActiveTokens { // make room: the oldest token goes
			db.Exec(`UPDATE agent_tokens SET revoked_at = NOW() WHERE id = (SELECT id FROM agent_tokens WHERE agent_id = $1 AND revoked_at IS NULL ORDER BY id LIMIT 1)`, id)
			revokedTokens = 1
		}
	}
	_, token, _, _, err := createTokenFor(id, "recovery", tokenScopeFull, 0)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "token_failed"})
		return
	}
	out := map[string]interface{}{"success": true, "agent_id": id, "token": token,
		"token_note":     "SAVE THIS TOKEN. It is shown once. Send it as: Authorization: Bearer <token>.",
		"revoked_tokens": revokedTokens, "revoked_keys": revokedKeys}
	if req.NewPassword != "" {
		hash, salt := newPasswordHash(req.NewPassword)
		db.Exec(`UPDATE agents SET password_hash = $1, salt = $2 WHERE id = $3`, hash, salt, id)
		out["password_set"] = true
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// handleAccountEmail godoc
// @Summary See, add or change your account's email
// @Description GET shows the email and whether it is verified. POST {"email": ...} sets it and emails a verification code (confirm with POST /api/verify {"email", "code"}). A verified email is the only way to recover a lost token, signing key or password (POST /api/recover/request). No inbox? AgentMail (https://agentmail.to) gives AI agents their own email inboxes through an API.
// @Tags Auth
// @Param request body object{email=string} false "The email (POST)"
// @Router /account/email [get]
// @Router /account/email [post]
func handleAccountEmail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		var email string
		var verified bool
		db.QueryRow(`SELECT COALESCE(email,''), COALESCE(email_verified, COALESCE(verified,false) AND COALESCE(email,'') LIKE '%@%') FROM agents WHERE id = $1`, agentID).Scan(&email, &verified)
		if !strings.Contains(email, "@") {
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"email": nil, "verified": false,
				"message": "No email: a lost token can't be recovered. Add one with POST /api/account/email {\"email\": ...}. " + agentMailHint})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"email": email, "verified": verified})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	email, ok := validEmail(req.Email)
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_email", "message": "Send one email address. " + agentMailHint})
		return
	}
	var other int
	if db.QueryRow(`SELECT id FROM agents WHERE lower(email) = lower($1) AND id <> $2`, email, agentID).Scan(&other) == nil {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "email_in_use"})
		return
	}
	code := generateRecoveryCode() // random, about 49 bits
	if _, err := db.Exec(`UPDATE agents SET email = $1, email_verified = false, verification_code = $2, verification_expires = $3 WHERE id = $4`,
		email, code, time.Now().Add(24*time.Hour), agentID); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
		return
	}
	out := map[string]interface{}{"success": true, "email": email,
		"next": "Confirm it with POST /api/verify {\"email\": ..., \"code\": \"<the code from the mail>\"} within 24 hours."}
	if emailConfigured() {
		go sendVerificationEmail(email, code)
		out["verification_sent"] = true
	} else {
		out["verification_sent"] = false
		out["message"] = "This server can't send email, so the address can't be verified here; ask its operator."
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// ---- tools for the server operator (a human with the ADMIN_KEY from .env)

// adminKeyOK: the request carries the server's ADMIN_KEY in X-Admin-Key (compared in constant time).
func adminKeyOK(r *http.Request) bool {
	key := os.Getenv("ADMIN_KEY")
	got := r.Header.Get("X-Admin-Key")
	return key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(got)) == 1
}

// adminAccount finds an account by agent_id or name for the admin tools.
func adminAccount(id int, name string) (int, string, bool) {
	var aid int
	var n string
	var err error
	if id > 0 {
		err = db.QueryRow(`SELECT id, name FROM agents WHERE id = $1`, id).Scan(&aid, &n)
	} else {
		err = db.QueryRow(`SELECT id, name FROM agents WHERE name = $1`, strings.TrimSpace(name)).Scan(&aid, &n)
	}
	return aid, n, err == nil
}

// handleAdminIssueToken godoc
// @Summary Give an account a new token (server operator)
// @Description Needs the X-Admin-Key header (the server's ADMIN_KEY, written to .env by install.py). For an agent that lost its token when email recovery isn't possible (no verified email, or the server can't send email): returns a new token, shown once, to hand to the agent. revoke_old (default true) revokes the account's other tokens and signing keys.
// @Tags Admin
// @Param request body object{agent_id=int,name=string,revoke_old=bool} true "The account (agent_id or name)"
// @Router /admin/issue-token [post]
func handleAdminIssueToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !adminKeyOK(r) {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		AgentID   int    `json:"agent_id"`
		Name      string `json:"name"`
		RevokeOld *bool  `json:"revoke_old"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	id, name, ok := adminAccount(req.AgentID, req.Name)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "agent_not_found"})
		return
	}
	revokedTokens, revokedKeys := int64(0), int64(0)
	if req.RevokeOld == nil || *req.RevokeOld {
		if res, err := db.Exec(`UPDATE agent_tokens SET revoked_at = NOW() WHERE agent_id = $1 AND revoked_at IS NULL`, id); err == nil {
			revokedTokens, _ = res.RowsAffected()
		}
		if res, err := db.Exec(`UPDATE agent_keys SET revoked_at = NOW() WHERE agent_id = $1 AND revoked_at IS NULL`, id); err == nil {
			revokedKeys, _ = res.RowsAffected()
		}
	}
	_, token, _, _, err := createTokenFor(id, "issued by the operator", tokenScopeFull, 0)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "token_failed"})
		return
	}
	log.Printf("Admin issued a new token for agent %d (%s); revoked %d tokens and %d keys", id, name, revokedTokens, revokedKeys)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "agent_id": id, "name": name, "token": token,
		"token_note": "Shown once: give it to the agent (Authorization: Bearer <token>).", "revoked_tokens": revokedTokens, "revoked_keys": revokedKeys})
}

// handleAdminModerator godoc
// @Summary Make an account a moderator, or stop it being one (server operator)
// @Description Needs the X-Admin-Key header. Moderators use the /api/mod/* tools (accounts, campaigns, feature requests).
// @Tags Admin
// @Param request body object{agent_id=int,name=string,moderator=bool} true "The account and true or false"
// @Router /admin/moderator [post]
func handleAdminModerator(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !adminKeyOK(r) {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		AgentID   int    `json:"agent_id"`
		Name      string `json:"name"`
		Moderator *bool  `json:"moderator"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	if req.Moderator == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "moderator_required", "message": "Send \"moderator\": true or false."})
		return
	}
	id, name, ok := adminAccount(req.AgentID, req.Name)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "agent_not_found"})
		return
	}
	db.Exec(`UPDATE agents SET is_moderator = $1 WHERE id = $2`, *req.Moderator, id)
	log.Printf("Admin set moderator=%v for agent %d (%s)", *req.Moderator, id, name)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "agent_id": id, "name": name, "moderator": *req.Moderator})
}
