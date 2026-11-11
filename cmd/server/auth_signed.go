package main

// Signed requests: authentication with a key pair instead of a secret that is sent.
// An agent registers an Ed25519 public key (POST /api/keys while signed in by
// any other method). Afterwards it proves who it is on each call without ever
// sending a secret:
//	Authorization: Signature keyid="<id>", ts="<unix seconds>", nonce="<random>", sig="<base64>"
// The signature covers this text, one item per line:
//	METHOD
//	/path?query
//	ts
//	nonce
//	hex(sha256(body))
// A request is accepted within 5 minutes of the server clock and each nonce
// works once, so a captured request cannot be replayed.
//	POST   /api/keys        register a public key {"public_key":"<base64 32 bytes>", "label":"..."}
//	GET    /api/keys        list keys
//	DELETE /api/keys/{id}   revoke a key

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	signatureWindow = 5 * time.Minute
	maxActiveKeys   = 10
	maxSignedBody   = 10 << 20
)

func ensureAgentKeysTable() {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS agent_keys (
		id SERIAL PRIMARY KEY,
		agent_id INTEGER NOT NULL,
		public_key VARCHAR(64) UNIQUE NOT NULL,
		label TEXT DEFAULT '',
		created_at TIMESTAMP DEFAULT NOW(),
		last_used_at TIMESTAMP,
		revoked_at TIMESTAMP
	)`)
	if err != nil {
		log.Printf("agent_keys table: %v", err)
	}
}

// signingString is the exact text that is signed.
func signingString(method, uri, ts, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.ToUpper(method) + "\n" + uri + "\n" + ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
}

// ---- nonce memory (in memory; entries expire with the time window) ----

var seenNonces = struct {
	sync.Mutex
	m map[string]time.Time
}{m: map[string]time.Time{}}

// useNonce returns false if the nonce was already used.
func useNonce(key string, now time.Time) bool {
	seenNonces.Lock()
	defer seenNonces.Unlock()
	for k, t := range seenNonces.m {
		if now.Sub(t) > 2*signatureWindow {
			delete(seenNonces.m, k)
		}
	}
	if _, used := seenNonces.m[key]; used {
		return false
	}
	seenNonces.m[key] = now
	return true
}

// parseSignatureHeader reads key="value" pairs after "Signature ".
func parseSignatureHeader(h string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(h, "Signature "), ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			out[strings.TrimSpace(kv[0])] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
		}
	}
	return out
}

// verifySignature checks a signature against a public key. Pure, for testing.
func verifySignature(pub ed25519.PublicKey, method, uri, ts, nonce string, body []byte, sigB64 string, now time.Time) error {
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("bad_timestamp")
	}
	d := now.Sub(time.Unix(secs, 0))
	if d < 0 {
		d = -d
	}
	if d > signatureWindow {
		return fmt.Errorf("timestamp_outside_window: check your clock (allowed +/- 5 minutes)")
	}
	if len(nonce) < 8 || len(nonce) > 64 {
		return fmt.Errorf("bad_nonce: use 8 to 64 random characters")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("bad_signature")
	}
	if !ed25519.Verify(pub, []byte(signingString(method, uri, ts, nonce, body)), sig) {
		return fmt.Errorf("bad_signature")
	}
	return nil
}

// agentFromSignature authenticates a signed request and returns the agent id.
// The body is read and put back so handlers can still read it.
func agentFromSignature(r *http.Request, header string) (int, error) {
	ip := clientIP(r)
	if authThrottled(ip) {
		return 0, fmt.Errorf("too_many_failed_attempts: wait a minute and try again")
	}
	fail := func(msg string) (int, error) {
		recordAuthFailure(ip)
		return 0, fmt.Errorf("%s", msg)
	}
	p := parseSignatureHeader(header)
	keyID, err := strconv.Atoi(p["keyid"])
	if err != nil || p["ts"] == "" || p["nonce"] == "" || p["sig"] == "" {
		return fail("bad_signature_header: need keyid, ts, nonce, sig")
	}
	var body []byte
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, maxSignedBody))
		if err != nil {
			return fail("could_not_read_body")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	var agentID int
	var pubB64 string
	err = db.QueryRow(`SELECT agent_id, public_key FROM agent_keys WHERE id = $1 AND revoked_at IS NULL`, keyID).Scan(&agentID, &pubB64)
	if err != nil {
		return fail("unknown_key")
	}
	raw, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return fail("unknown_key")
	}
	now := time.Now()
	if err := verifySignature(ed25519.PublicKey(raw), r.Method, r.URL.RequestURI(), p["ts"], p["nonce"], body, p["sig"], now); err != nil {
		return fail(err.Error())
	}
	if !useNonce(fmt.Sprintf("%d:%s", keyID, p["nonce"]), now) {
		return fail("nonce_already_used")
	}
	db.Exec(`UPDATE agent_keys SET last_used_at = NOW() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`, keyID)
	return agentID, nil
}

// handleKeys serves POST (register) and GET (list) on /api/keys.
func handleKeys(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			PublicKey string `json:"public_key"`
			Label     string `json:"label"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req)
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(req.PublicKey))
		if derr != nil || len(raw) != ed25519.PublicKeySize {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_public_key", "hint": "Send the 32-byte Ed25519 public key, base64 encoded."})
			return
		}
		var active int
		db.QueryRow(`SELECT COUNT(*) FROM agent_keys WHERE agent_id = $1 AND revoked_at IS NULL`, agentID).Scan(&active)
		if active >= maxActiveKeys {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "too_many_keys", "max_active": maxActiveKeys})
			return
		}
		label := strings.TrimSpace(req.Label)
		if len(label) > 80 {
			label = label[:80]
		}
		pub := base64.StdEncoding.EncodeToString(raw)
		var id int
		if ierr := db.QueryRow(`INSERT INTO agent_keys (agent_id, public_key, label) VALUES ($1, $2, $3) RETURNING id`, agentID, pub, label).Scan(&id); ierr != nil {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "key_already_registered"})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "key_id": id,
			"use": `Authorization: Signature keyid="` + strconv.Itoa(id) + `", ts="<unix seconds>", nonce="<random>", sig="<base64 signature>"`})

	case http.MethodGet:
		rows, qerr := db.Query(`SELECT id, public_key, COALESCE(label, ''), created_at, last_used_at, revoked_at FROM agent_keys WHERE agent_id = $1 ORDER BY id`, agentID)
		if qerr != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "db_error"})
			return
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id int
			var pub, label string
			var created time.Time
			var lastUsed, revoked *time.Time
			if rows.Scan(&id, &pub, &label, &created, &lastUsed, &revoked) != nil {
				continue
			}
			item := map[string]interface{}{"key_id": id, "public_key": pub, "label": label, "created_at": created.UTC().Format(time.RFC3339), "active": revoked == nil}
			if lastUsed != nil {
				item["last_used_at"] = lastUsed.UTC().Format(time.RFC3339)
			}
			if revoked != nil {
				item["revoked_at"] = revoked.UTC().Format(time.RFC3339)
			}
			list = append(list, item)
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"keys": list})

	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}

// handleKeyByID revokes a key: DELETE /api/keys/{id}.
func handleKeyByID(w http.ResponseWriter, r *http.Request) {
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
	id, convErr := strconv.Atoi(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/keys/"), "/"))
	if convErr != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_key_id"})
		return
	}
	res, _ := db.Exec(`UPDATE agent_keys SET revoked_at = NOW() WHERE id = $1 AND agent_id = $2 AND revoked_at IS NULL`, id, agentID)
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "key_not_found_or_already_revoked"})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "revoked": id})
}
