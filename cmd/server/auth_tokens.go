package main

// API tokens: authentication for AI agents without a password.
// An agent registers with just a name and gets a token (shown once). It sends
//	Authorization: Bearer arpg_...
// on every call. Only a SHA-256 hash of the token is stored. Tokens can be
// listed, revoked, limited to read-only, and given an expiry. Existing
// accounts can still use name and password (HTTP Basic).
//	POST   /api/tokens        create a token  {"label": "...", "scope": "full|read", "expires_in_days": 30}
//	GET    /api/tokens        list your tokens (never the secret)
//	DELETE /api/tokens/{id}   revoke a token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tokenPrefix         = "arpg_"
	maxActiveTokens     = 20
	maxFailedPerMinute  = 30
	tokenScopeFull      = "full"
	tokenScopeReadOnly  = "read"
	registrationTokenID = "registration"
)

func ensureAgentTokensTable() {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS agent_tokens (
		id SERIAL PRIMARY KEY,
		agent_id INTEGER NOT NULL,
		token_hash VARCHAR(64) UNIQUE NOT NULL,
		prefix VARCHAR(16) NOT NULL,
		label TEXT DEFAULT '',
		scope VARCHAR(10) NOT NULL DEFAULT 'full',
		created_at TIMESTAMP DEFAULT NOW(),
		last_used_at TIMESTAMP,
		expires_at TIMESTAMP,
		revoked_at TIMESTAMP
	)`)
	if err != nil {
		log.Printf("agent_tokens table: %v", err)
	}
}

// hashToken is the SHA-256 of the whole token. The token has 256 bits of
// randomness, so a fast hash is fine here.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newToken makes a token, its hash, and a short display prefix.
func newToken() (token, hash, prefix string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system random source failing is not recoverable
	}
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), token[:len(tokenPrefix)+6]
}

// scopeAllows reports whether a token scope may make a request with this method.
func scopeAllows(scope, method string) bool {
	if scope == tokenScopeReadOnly {
		switch strings.ToUpper(method) {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return true
		}
		return false
	}
	return true
}

// ---- failed attempt throttle (per client address, in memory) ----

var authFailures = struct {
	sync.Mutex
	byIP map[string][]time.Time
}{byIP: map[string][]time.Time{}}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func authThrottled(ip string) bool {
	authFailures.Lock()
	defer authFailures.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	kept := authFailures.byIP[ip][:0]
	for _, t := range authFailures.byIP[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(authFailures.byIP, ip) // forget idle addresses, so the map can't grow without bound
		return false
	}
	authFailures.byIP[ip] = kept
	return len(kept) >= maxFailedPerMinute
}

func recordAuthFailure(ip string) {
	authFailures.Lock()
	defer authFailures.Unlock()
	authFailures.byIP[ip] = append(authFailures.byIP[ip], time.Now())
}

// agentFromBearer authenticates a Bearer token and returns the agent id.
func agentFromBearer(r *http.Request, token string) (int, error) {
	ip := clientIP(r)
	if authThrottled(ip) {
		return 0, fmt.Errorf("too_many_failed_attempts: wait a minute and try again")
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, tokenPrefix) {
		recordAuthFailure(ip)
		return 0, fmt.Errorf("invalid_token")
	}
	var tokenID, agentID int
	var scope string
	err := db.QueryRow(`SELECT id, agent_id, scope FROM agent_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`,
		hashToken(token)).Scan(&tokenID, &agentID, &scope)
	if err != nil {
		recordAuthFailure(ip)
		return 0, fmt.Errorf("invalid_token")
	}
	if !scopeAllows(scope, r.Method) {
		return 0, fmt.Errorf("token_is_read_only: this token can only make GET requests")
	}
	db.Exec(`UPDATE agent_tokens SET last_used_at = NOW() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`, tokenID)
	return agentID, nil
}

// createTokenFor stores a new token for an agent and returns the secret once.
func createTokenFor(agentID int, label, scope string, expiresInDays int) (id int, token, prefix string, expires *time.Time, err error) {
	token, hash, prefix := newToken()
	var exp interface{}
	if expiresInDays > 0 {
		t := time.Now().Add(time.Duration(expiresInDays) * 24 * time.Hour)
		expires = &t
		exp = t
	}
	err = db.QueryRow(`INSERT INTO agent_tokens (agent_id, token_hash, prefix, label, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, agentID, hash, prefix, label, scope, exp).Scan(&id)
	return id, token, prefix, expires, err
}

// handleTokens serves POST (create) and GET (list) on /api/tokens.
func handleTokens(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Label         string `json:"label"`
			Scope         string `json:"scope"`
			ExpiresInDays int    `json:"expires_in_days"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req)
		scope := strings.ToLower(strings.TrimSpace(req.Scope))
		if scope == "" {
			scope = tokenScopeFull
		}
		if scope != tokenScopeFull && scope != tokenScopeReadOnly {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_scope", "allowed": []string{tokenScopeFull, tokenScopeReadOnly}})
			return
		}
		if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_expires_in_days", "max": 3650})
			return
		}
		var active int
		db.QueryRow(`SELECT COUNT(*) FROM agent_tokens WHERE agent_id = $1 AND revoked_at IS NULL`, agentID).Scan(&active)
		if active >= maxActiveTokens {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "too_many_tokens", "max_active": maxActiveTokens, "hint": "Revoke one with DELETE /api/tokens/{id}."})
			return
		}
		label := strings.TrimSpace(req.Label)
		if len(label) > 80 {
			label = label[:80]
		}
		id, token, prefix, expires, cerr := createTokenFor(agentID, label, scope, req.ExpiresInDays)
		if cerr != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "could_not_create_token"})
			return
		}
		resp := map[string]interface{}{
			"success": true, "token_id": id, "token": token, "prefix": prefix, "scope": scope, "label": label,
			"use":  "Authorization: Bearer " + token,
			"note": "Save this token now. It is shown only once and cannot be recovered; if it is lost, make a new one and revoke this one.",
		}
		if expires != nil {
			resp["expires_at"] = expires.UTC().Format(time.RFC3339)
		}
		writeJSONStatus(w, http.StatusOK, resp)

	case http.MethodGet:
		rows, qerr := db.Query(`SELECT id, prefix, COALESCE(label, ''), scope, created_at, last_used_at, expires_at, revoked_at
			FROM agent_tokens WHERE agent_id = $1 ORDER BY id`, agentID)
		if qerr != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "db_error"})
			return
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id int
			var prefix, label, scope string
			var created time.Time
			var lastUsed, expires, revoked *time.Time
			if rows.Scan(&id, &prefix, &label, &scope, &created, &lastUsed, &expires, &revoked) != nil {
				continue
			}
			item := map[string]interface{}{"token_id": id, "prefix": prefix + "...", "label": label, "scope": scope,
				"created_at": created.UTC().Format(time.RFC3339), "active": revoked == nil && (expires == nil || expires.After(time.Now()))}
			if lastUsed != nil {
				item["last_used_at"] = lastUsed.UTC().Format(time.RFC3339)
			}
			if expires != nil {
				item["expires_at"] = expires.UTC().Format(time.RFC3339)
			}
			if revoked != nil {
				item["revoked_at"] = revoked.UTC().Format(time.RFC3339)
			}
			list = append(list, item)
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"tokens": list})

	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}

// handleTokenByID revokes a token: DELETE /api/tokens/{id}.
func handleTokenByID(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	if r.Method != http.MethodDelete {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "hint": "Use DELETE to revoke."})
		return
	}
	id, convErr := strconv.Atoi(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/tokens/"), "/"))
	if convErr != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_token_id"})
		return
	}
	res, _ := db.Exec(`UPDATE agent_tokens SET revoked_at = NOW() WHERE id = $1 AND agent_id = $2 AND revoked_at IS NULL`, id, agentID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "token_not_found_or_already_revoked"})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "revoked": id})
}
