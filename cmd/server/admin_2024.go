package main

// Deleting characters and campaigns, and the server operator's admin tools (Nov 13).
//
// Deleting hides; it never erases. A deleted character loses its owner and its campaign and gets a placeholder name, so every
// ownership check refuses it, its name is free again, and old logs that mention its id still work. The owner, name and campaign
// are kept in deleted_* columns, and the admin can restore it. A deleted campaign gets status "deleted" and no GM; its characters
// are let go (they can join another campaign) and listed in deleted_members, so a restore can bring them back.
//
// Players: DELETE /api/characters/{id} (or POST /api/characters/{id}/delete) with {"confirm": "<the character's name>"}, only
// while the character is in no campaign or in one that hasn't started.
//
// Admin: the admin page (at the secret address in ADMIN_PATH; off without it) and /api/admin/characters, /api/admin/campaigns, /api/admin/changes, all with the X-Admin-Key header
// (the server's ADMIN_KEY). Every edit, delete and restore is written to admin_changes.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var adminSchemaOnce sync.Once

func ensureAdminSchema() {
	adminSchemaOnce.Do(func() {
		if db == nil {
			return
		}
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS deleted_owner INTEGER`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS deleted_name TEXT`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS deleted_lobby INTEGER`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS deleted_by TEXT`)
		db.Exec(`ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP`)
		db.Exec(`ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS deleted_dm INTEGER`)
		db.Exec(`ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS deleted_status TEXT`)
		db.Exec(`ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS deleted_members JSONB`)
		db.Exec(`CREATE TABLE IF NOT EXISTS admin_changes (
			id SERIAL PRIMARY KEY,
			at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			kind TEXT NOT NULL,
			row_id INTEGER NOT NULL,
			field TEXT NOT NULL,
			old_value TEXT,
			new_value TEXT,
			by_whom TEXT NOT NULL,
			note TEXT DEFAULT ''
		)`)
	})
}

func logAdminChange(kind string, id int, field, oldV, newV, by, note string) {
	db.Exec(`INSERT INTO admin_changes (kind, row_id, field, old_value, new_value, by_whom, note) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		kind, id, field, oldV, newV, by, note)
}

// characterIsDeleted: the character exists and was deleted.
func characterIsDeleted(charID int) bool {
	ensureAdminSchema()
	var deleted bool
	db.QueryRow(`SELECT deleted_at IS NOT NULL FROM characters WHERE id = $1`, charID).Scan(&deleted)
	return deleted
}

// campaignIsDeleted: the campaign exists and was deleted.
func campaignIsDeleted(id int) bool {
	ensureAdminSchema()
	var deleted bool
	db.QueryRow(`SELECT deleted_at IS NOT NULL FROM lobbies WHERE id = $1`, id).Scan(&deleted)
	return deleted
}

// softDeleteCharacter hides a character: no owner, no campaign, a placeholder name.
func softDeleteCharacter(charID int, by string) error {
	ensureAdminSchema()
	var owner, lobby sql.NullInt64
	var name string
	var deleted bool
	if err := db.QueryRow(`SELECT agent_id, lobby_id, name, deleted_at IS NOT NULL FROM characters WHERE id = $1`, charID).
		Scan(&owner, &lobby, &name, &deleted); err != nil {
		return fmt.Errorf("character %d not found", charID)
	}
	if deleted {
		return fmt.Errorf("%s is already deleted", name)
	}
	placeholder := fmt.Sprintf("deleted character %d", charID)
	if _, err := db.Exec(`UPDATE characters SET deleted_at = NOW(), deleted_owner = $1, deleted_name = $2, deleted_lobby = $3, deleted_by = $4,
		agent_id = NULL, lobby_id = NULL, name = $5 WHERE id = $6`, owner, name, lobby, by, placeholder, charID); err != nil {
		return fmt.Errorf("could not delete: %v", publicError(err))
	}
	if lobby.Valid {
		reconcileCampaignStatus(int(lobby.Int64))
	}
	logAdminChange("character", charID, "deleted", name, "deleted", by, "")
	return nil
}

// restoreCharacter gives a deleted character back its owner and name (and campaign, when that campaign still exists and isn't
// deleted). It returns what happened.
func restoreCharacter(charID int, by string) (string, error) {
	ensureAdminSchema()
	var owner, lobby sql.NullInt64
	var name sql.NullString
	var deleted bool
	if err := db.QueryRow(`SELECT deleted_owner, deleted_lobby, deleted_name, deleted_at IS NOT NULL FROM characters WHERE id = $1`, charID).
		Scan(&owner, &lobby, &name, &deleted); err != nil {
		return "", fmt.Errorf("character %d not found", charID)
	}
	if !deleted {
		return "", fmt.Errorf("character %d is not deleted", charID)
	}
	newName := name.String
	note := ""
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM characters WHERE LOWER(name) = LOWER($1) AND id <> $2`, newName, charID).Scan(&n)
	if n > 0 || newName == "" {
		newName = fmt.Sprintf("%s (restored %d)", name.String, charID)
		note = fmt.Sprintf(" Another character now has the name %q, so it is called %q: edit the name if you like.", name.String, newName)
	}
	var lobbyArg interface{}
	if lobby.Valid {
		var status string
		db.QueryRow(`SELECT COALESCE(status,'') FROM lobbies WHERE id = $1`, lobby.Int64).Scan(&status)
		if status != "" && status != "deleted" && status != "completed" {
			lobbyArg = lobby.Int64
			note += fmt.Sprintf(" It is back in campaign %d.", lobby.Int64)
		} else {
			note += " Its old campaign is gone or finished, so it is in no campaign."
		}
	}
	if _, err := db.Exec(`UPDATE characters SET agent_id = $1, lobby_id = $2, name = $3, deleted_at = NULL, deleted_owner = NULL,
		deleted_name = NULL, deleted_lobby = NULL, deleted_by = NULL WHERE id = $4`, owner, lobbyArg, newName, charID); err != nil {
		return "", fmt.Errorf("could not restore: %v", publicError(err))
	}
	logAdminChange("character", charID, "restored", "deleted", newName, by, "")
	return "Restored " + newName + "." + note, nil
}

// softDeleteCampaign hides a campaign and lets its characters go.
func softDeleteCampaign(id int, by string) error {
	ensureAdminSchema()
	var dm sql.NullInt64
	var status, name string
	if err := db.QueryRow(`SELECT dm_id, COALESCE(status,''), name FROM lobbies WHERE id = $1`, id).Scan(&dm, &status, &name); err != nil {
		return fmt.Errorf("campaign %d not found", id)
	}
	if status == "deleted" {
		return fmt.Errorf("%s is already deleted", name)
	}
	var members []int
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1`, id)
	if err == nil {
		for rows.Next() {
			var c int
			rows.Scan(&c)
			members = append(members, c)
		}
		rows.Close()
	}
	mj, _ := json.Marshal(members)
	if _, err := db.Exec(`UPDATE lobbies SET deleted_at = NOW(), deleted_dm = $1, deleted_status = $2, deleted_members = $3, dm_id = NULL,
		status = 'deleted' WHERE id = $4`, dm, status, string(mj), id); err != nil {
		return fmt.Errorf("could not delete: %v", publicError(err))
	}
	db.Exec(`UPDATE characters SET lobby_id = NULL WHERE lobby_id = $1`, id)
	logAdminChange("campaign", id, "deleted", name, fmt.Sprintf("deleted (characters let go: %v)", members), by, "")
	return nil
}

// restoreCampaign gives a deleted campaign back its GM and status, and brings back its characters that are free and not deleted.
func restoreCampaign(id int, by string) (string, error) {
	ensureAdminSchema()
	var dm sql.NullInt64
	var prev sql.NullString
	var membersRaw []byte
	var deleted bool
	var name string
	if err := db.QueryRow(`SELECT deleted_dm, deleted_status, COALESCE(deleted_members,'[]'), deleted_at IS NOT NULL, name FROM lobbies WHERE id = $1`, id).
		Scan(&dm, &prev, &membersRaw, &deleted, &name); err != nil {
		return "", fmt.Errorf("campaign %d not found", id)
	}
	if !deleted {
		return "", fmt.Errorf("campaign %d is not deleted", id)
	}
	status := prev.String
	if status == "" || status == "deleted" {
		status = "recruiting"
	}
	if _, err := db.Exec(`UPDATE lobbies SET dm_id = $1, status = $2, deleted_at = NULL, deleted_dm = NULL, deleted_status = NULL,
		deleted_members = NULL WHERE id = $3`, dm, status, id); err != nil {
		return "", fmt.Errorf("could not restore: %v", publicError(err))
	}
	var members []int
	json.Unmarshal(membersRaw, &members)
	var back, notBack []string
	for _, c := range members {
		res, err := db.Exec(`UPDATE characters SET lobby_id = $1 WHERE id = $2 AND lobby_id IS NULL AND deleted_at IS NULL`, id, c)
		if n, _ := res.RowsAffected(); err == nil && n == 1 {
			back = append(back, strconv.Itoa(c))
		} else {
			notBack = append(notBack, strconv.Itoa(c))
		}
	}
	logAdminChange("campaign", id, "restored", "deleted", status, by, "")
	msg := fmt.Sprintf("Restored %s (%s).", name, status)
	if len(back) > 0 {
		msg += " Characters back in it: " + strings.Join(back, ", ") + "."
	}
	if len(notBack) > 0 {
		msg += " Not brought back (deleted, or now in another campaign): " + strings.Join(notBack, ", ") + "."
	}
	return msg, nil
}

// handleCharacterDelete: a player deletes their own character.
func handleCharacterDelete(w http.ResponseWriter, r *http.Request, charID int) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	ensureAdminSchema()
	var owner sql.NullInt64
	var name, status string
	if db.QueryRow(`SELECT c.agent_id, c.name, COALESCE(l.status,'') FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id WHERE c.id = $1 AND c.deleted_at IS NULL`, charID).
		Scan(&owner, &name, &status) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if !owner.Valid || int(owner.Int64) != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character", "message": "You can delete only your own characters."})
		return
	}
	if status == "active" || status == "completed" {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "campaign_started",
			"message": fmt.Sprintf("%s is in a campaign that has started, so it can't be deleted. Ask your GM to remove it from the campaign first.", name)})
		return
	}
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	if !strings.EqualFold(strings.TrimSpace(bstr(body, "confirm")), name) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "confirm_required",
			"message": fmt.Sprintf("To delete %s, send {\"confirm\": %q}. The character is hidden, not erased: the server's admin can restore it if you change your mind.", name, name)})
		return
	}
	if err := softDeleteCharacter(charID, fmt.Sprintf("player %d", agentID)); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "delete_failed", "message": err.Error()})
		return
	}
	msg := fmt.Sprintf("%s is deleted. Its name is free again. If you change your mind, ask the server's admin: it can be restored.", name)
	if status == "recruiting" {
		msg += " It also left its campaign, which hadn't started."
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "deleted": charID, "message": msg})
}

// ---- admin API ----

var adminFails = struct {
	sync.Mutex
	byIP map[string][]time.Time
}{byIP: map[string][]time.Time{}}

// requireAdmin lets the operator through: the X-Admin-Key header, or the admin session cookie (with the X-Admin-Page header on a
// request that changes something, which another site can't send). 10 wrong keys from one address in 15 minutes lock it out for the
// rest of that time; a request with no key at all is just refused (opening the page signed out is not an attempt).
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	ip := clientIP(r)
	adminFails.Lock()
	var recent []time.Time
	for _, t := range adminFails.byIP[ip] {
		if time.Since(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	adminFails.byIP[ip] = recent
	locked := len(recent) >= 10
	adminFails.Unlock()
	if locked {
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]interface{}{"error": "too_many_attempts", "message": "Too many wrong admin keys: try again in 15 minutes."})
		return false
	}
	if adminKeyOK(r) || adminCookieOK(r) {
		ensureAdminSchema()
		return true
	}
	if r.Header.Get("X-Admin-Key") != "" {
		adminFails.Lock()
		adminFails.byIP[ip] = append(adminFails.byIP[ip], time.Now())
		adminFails.Unlock()
	}
	writeJSONStatus(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized", "message": "Sign in with the server's ADMIN_KEY."})
	return false
}

// ---- the admin session cookie (Nov 14, Matt: "I'm tired of adding the key over and over")
// The cookie holds an expiry time and an HMAC of it made with ADMIN_KEY, never the key itself: it lasts 30 days, and changing
// ADMIN_KEY ends every session. HttpOnly (page scripts can't read it), Secure on HTTPS, SameSite=Strict, sent only to /api/admin.

const adminCookieName = "arpg_admin"
const adminSessionDays = 30

func adminCookieSig(expiry string) string {
	m := hmac.New(sha256.New, []byte("admin-session:"+os.Getenv("ADMIN_KEY")))
	m.Write([]byte(expiry))
	return hex.EncodeToString(m.Sum(nil))
}

func adminCookieOK(r *http.Request) bool {
	if os.Getenv("ADMIN_KEY") == "" {
		return false
	}
	c, err := r.Cookie(adminCookieName)
	if err != nil {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	if !hmac.Equal([]byte(parts[1]), []byte(adminCookieSig(parts[0]))) {
		return false
	}
	// a request that changes something must come from the admin page itself
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Admin-Page") != "1" {
		return false
	}
	return true
}

func setAdminCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: adminCookieName, Value: value, Path: "/api/admin", MaxAge: maxAge, HttpOnly: true,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteStrictMode})
}

// handleAdminSession: POST with the X-Admin-Key header signs in (sets the cookie); DELETE signs out; GET says whether signed in.
func handleAdminSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"signed_in": adminCookieOK(r)})
	case http.MethodPost:
		if !requireAdmin(w, r) {
			return
		}
		exp := strconv.FormatInt(time.Now().Add(adminSessionDays*24*time.Hour).Unix(), 10)
		setAdminCookie(w, r, exp+"."+adminCookieSig(exp), adminSessionDays*24*3600)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"signed_in": true, "message": fmt.Sprintf("Signed in for %d days on this browser.", adminSessionDays)})
	case http.MethodDelete:
		setAdminCookie(w, r, "", -1)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"signed_in": false})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}

// tableColumns: a table's columns and their data types (for the admin's generic editor).
func tableColumns(table string) map[string]string {
	out := map[string]string{}
	rows, err := db.Query(`SELECT column_name, data_type FROM information_schema.columns WHERE table_name = $1`, table)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c, t string
		rows.Scan(&c, &t)
		out[c] = t
	}
	return out
}

// Columns the admin edits through delete/restore or not at all.
var adminLocked = map[string]bool{"id": true, "avatar": true, "deleted_at": true, "deleted_owner": true, "deleted_name": true,
	"deleted_lobby": true, "deleted_by": true, "deleted_dm": true, "deleted_status": true, "deleted_members": true,
	"watch_password_hash": true, "password_hash": true}

func adminValueText(v interface{}) (interface{}, string) {
	switch x := v.(type) {
	case nil:
		return nil, "NULL"
	case string:
		return x, x
	case bool:
		return strconv.FormatBool(x), strconv.FormatBool(x)
	case float64:
		s := strconv.FormatFloat(x, 'f', -1, 64)
		return s, s
	default:
		b, _ := json.Marshal(x)
		return string(b), string(b)
	}
}

// adminSet changes columns of one row, each logged. It returns the fields changed, or an error naming the field that failed.
func adminSet(table, kind string, id int, set map[string]interface{}, note string) ([]string, error) {
	cols := tableColumns(table)
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := cols[k]; !ok {
			return nil, fmt.Errorf("%q is not a field of %s", k, kind)
		}
		if adminLocked[k] {
			return nil, fmt.Errorf("%q can't be edited here (delete and restore have their own buttons)", k)
		}
		if kind == "campaign" && k == "status" && set[k] == "deleted" {
			return nil, fmt.Errorf("use the delete button to delete a campaign")
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var changed []string
	oldLobby := ""
	for _, k := range keys {
		var old sql.NullString
		if err := tx.QueryRow(fmt.Sprintf(`SELECT %q::text FROM %s WHERE id = $1`, k, table), id).Scan(&old); err != nil {
			return nil, fmt.Errorf("%s %d not found", kind, id)
		}
		val, text := adminValueText(set[k])
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET %q = $1 WHERE id = $2`, table, k), val, id); err != nil {
			return nil, fmt.Errorf("%s: %q doesn't fit this field (its type is %s)", k, text, cols[k])
		}
		oldText := "NULL"
		if old.Valid {
			oldText = old.String
		}
		if k == "lobby_id" {
			oldLobby = oldText
		}
		if oldText != text {
			tx.Exec(`INSERT INTO admin_changes (kind, row_id, field, old_value, new_value, by_whom, note) VALUES ($1,$2,$3,$4,$5,'admin',$6)`,
				kind, id, k, oldText, text, note)
			changed = append(changed, k)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// follow-ups: conditions live in two places (conditions_unified.go); a campaign's status depends on its players
	if kind == "character" {
		for _, k := range changed {
			switch k {
			case "conditions":
				syncRecordsFromList(id)
			case "lobby_id":
				if n, err := strconv.Atoi(oldLobby); err == nil && n > 0 {
					reconcileCampaignStatus(n)
				}
				var now sql.NullInt64
				db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, id).Scan(&now)
				if now.Valid {
					reconcileCampaignStatus(int(now.Int64))
				}
			}
		}
	}
	return changed, nil
}

// adminRow: one row as JSON (bytea columns left out).
func adminRow(table string, id int) map[string]interface{} {
	var raw []byte
	if db.QueryRow(fmt.Sprintf(`SELECT to_jsonb(t) - 'avatar' - 'watch_password_hash' FROM %s t WHERE id = $1`, table), id).Scan(&raw) != nil {
		return nil
	}
	out := map[string]interface{}{}
	json.Unmarshal(raw, &out)
	return out
}

// handleAdminCharacters: GET /api/admin/characters[?q=&deleted=include|only], GET/POST /api/admin/characters/{id},
// POST /api/admin/characters/{id}/delete and /restore.
func handleAdminCharacters(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/characters"), "/"), "/")
	if parts[0] == "" {
		q := "%" + strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))) + "%"
		cond := "c.deleted_at IS NULL"
		switch r.URL.Query().Get("deleted") {
		case "include":
			cond = "TRUE"
		case "only":
			cond = "c.deleted_at IS NOT NULL"
		}
		rows, err := db.Query(`SELECT c.id, c.name, COALESCE(c.deleted_name,''), COALESCE(c.class,''), COALESCE(c.species,''), COALESCE(c.level,1),
			COALESCE(a.name, da.name, ''), COALESCE(c.agent_id, c.deleted_owner, 0), COALESCE(l.name,''), COALESCE(c.lobby_id,0), c.deleted_at IS NOT NULL
			FROM characters c LEFT JOIN agents a ON a.id = c.agent_id LEFT JOIN agents da ON da.id = c.deleted_owner LEFT JOIN lobbies l ON l.id = c.lobby_id
			WHERE `+cond+` AND (LOWER(c.name) LIKE $1 OR LOWER(COALESCE(c.deleted_name,'')) LIKE $1 OR LOWER(COALESCE(a.name, da.name, '')) LIKE $1 OR c.id::text = TRIM(BOTH '%' FROM $1))
			ORDER BY c.id DESC LIMIT 200`, q)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": publicError(err)})
			return
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id, level, ownerID, lobbyID int
			var name, oldName, class, species, owner, lobby string
			var deleted bool
			rows.Scan(&id, &name, &oldName, &class, &species, &level, &owner, &ownerID, &lobby, &lobbyID, &deleted)
			if deleted {
				name = oldName
			}
			list = append(list, map[string]interface{}{"id": id, "name": name, "class": class, "species": species, "level": level,
				"owner": owner, "owner_id": ownerID, "campaign": lobby, "campaign_id": lobbyID, "deleted": deleted})
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"characters": list})
		return
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_id"})
		return
	}
	adminRowRoutes(w, r, "characters", "character", id, parts[1:])
}

// handleAdminCampaigns: the same for campaigns, plus {"add_character": id} and {"remove_character": id}.
func handleAdminCampaigns(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/campaigns"), "/"), "/")
	if parts[0] == "" {
		q := "%" + strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))) + "%"
		cond := "l.deleted_at IS NULL"
		switch r.URL.Query().Get("deleted") {
		case "include":
			cond = "TRUE"
		case "only":
			cond = "l.deleted_at IS NOT NULL"
		}
		rows, err := db.Query(`SELECT l.id, l.name, COALESCE(l.status,''), COALESCE(a.name, da.name, ''), COALESCE(l.dm_id, l.deleted_dm, 0),
			(SELECT COUNT(*) FROM characters WHERE lobby_id = l.id), COALESCE(l.max_players,0), l.deleted_at IS NOT NULL
			FROM lobbies l LEFT JOIN agents a ON a.id = l.dm_id LEFT JOIN agents da ON da.id = l.deleted_dm
			WHERE `+cond+` AND (LOWER(l.name) LIKE $1 OR LOWER(COALESCE(a.name, da.name, '')) LIKE $1 OR l.id::text = TRIM(BOTH '%' FROM $1))
			ORDER BY l.id DESC LIMIT 200`, q)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": publicError(err)})
			return
		}
		defer rows.Close()
		list := []map[string]interface{}{}
		for rows.Next() {
			var id, dmID, players, maxP int
			var name, status, dm string
			var deleted bool
			rows.Scan(&id, &name, &status, &dm, &dmID, &players, &maxP, &deleted)
			list = append(list, map[string]interface{}{"id": id, "name": name, "status": status, "gm": dm, "gm_id": dmID,
				"players": players, "max_players": maxP, "deleted": deleted})
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaigns": list})
		return
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_id"})
		return
	}
	adminRowRoutes(w, r, "lobbies", "campaign", id, parts[1:])
}

func adminRowRoutes(w http.ResponseWriter, r *http.Request, table, kind string, id int, rest []string) {
	action := ""
	if len(rest) > 0 {
		action = rest[0]
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		row := adminRow(table, id)
		if row == nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": kind + "_not_found"})
			return
		}
		out := map[string]interface{}{kind: row, "field_types": tableColumns(table), "locked_fields": adminLocked}
		var n1, n2 string
		if kind == "campaign" {
			out["characters"] = adminCampaignMembers(id)
			db.QueryRow(`SELECT COALESCE(a.name,'') FROM lobbies l LEFT JOIN agents a ON a.id = COALESCE(l.dm_id, l.deleted_dm) WHERE l.id = $1`, id).Scan(&n1)
			out["gm_name"] = n1
		} else {
			db.QueryRow(`SELECT COALESCE(a.name,''), COALESCE(l.name,'') FROM characters c LEFT JOIN agents a ON a.id = COALESCE(c.agent_id, c.deleted_owner)
				LEFT JOIN lobbies l ON l.id = c.lobby_id WHERE c.id = $1`, id).Scan(&n1, &n2)
			out["owner_name"], out["campaign_name"] = n1, n2
		}
		writeJSONStatus(w, http.StatusOK, out)
	case action == "" && r.Method == http.MethodPost:
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
		note := bstr(body, "note")
		if kind == "campaign" {
			if msg, done, err := adminCampaignMembership(id, body, note); done {
				if err != nil {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_changed", "message": err.Error()})
				} else {
					writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": msg, "characters": adminCampaignMembers(id)})
				}
				return
			}
		}
		set := bmap(body, "set")
		if len(set) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_change", "message": `Send {"set": {"field": value}}.`})
			return
		}
		changed, err := adminSet(table, kind, id, set, note)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_changed", "message": err.Error() + ". Nothing was saved."})
			return
		}
		if kind == "campaign" {
			reconcileCampaignStatus(id)
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "changed": changed, kind: adminRow(table, id)})
	case action == "delete" && r.Method == http.MethodPost:
		var err error
		if kind == "character" {
			err = softDeleteCharacter(id, "admin")
		} else {
			err = softDeleteCampaign(id, "admin")
		}
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_deleted", "message": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": fmt.Sprintf("Deleted %s %d. It can be restored.", kind, id)})
	case action == "restore" && r.Method == http.MethodPost:
		var msg string
		var err error
		if kind == "character" {
			msg, err = restoreCharacter(id, "admin")
		} else {
			msg, err = restoreCampaign(id, "admin")
		}
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_restored", "message": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": msg})
	default:
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "unknown_admin_request"})
	}
}

func adminCampaignMembers(id int) []map[string]interface{} {
	out := []map[string]interface{}{}
	rows, err := db.Query(`SELECT c.id, c.name, COALESCE(c.class,''), COALESCE(c.level,1), COALESCE(a.name,'') FROM characters c
		LEFT JOIN agents a ON a.id = c.agent_id WHERE c.lobby_id = $1 ORDER BY c.id`, id)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var cid, level int
		var name, class, owner string
		rows.Scan(&cid, &name, &class, &level, &owner)
		out = append(out, map[string]interface{}{"id": cid, "name": name, "class": class, "level": level, "owner": owner})
	}
	return out
}

// adminCampaignMembership: {"add_character": id} or {"remove_character": id}. done is false when the body has neither.
func adminCampaignMembership(id int, body map[string]interface{}, note string) (string, bool, error) {
	if v, ok := body["add_character"].(float64); ok {
		cid := int(v)
		var cur sql.NullInt64
		var name string
		if db.QueryRow(`SELECT lobby_id, name FROM characters WHERE id = $1 AND deleted_at IS NULL`, cid).Scan(&cur, &name) != nil {
			return "", true, fmt.Errorf("character %d not found (or deleted: restore it first)", cid)
		}
		if campaignIsDeleted(id) {
			return "", true, fmt.Errorf("campaign %d is deleted: restore it first", id)
		}
		if cur.Valid && int(cur.Int64) == id {
			return "", true, fmt.Errorf("%s is already in this campaign", name)
		}
		db.Exec(`UPDATE characters SET lobby_id = $1 WHERE id = $2`, id, cid)
		logAdminChange("character", cid, "lobby_id", fmt.Sprint(cur.Int64), strconv.Itoa(id), "admin", note)
		reconcileCampaignStatus(id)
		msg := fmt.Sprintf("%s joined the campaign.", name)
		if cur.Valid {
			msg += fmt.Sprintf(" It left campaign %d.", cur.Int64)
			reconcileCampaignStatus(int(cur.Int64))
		}
		return msg, true, nil
	}
	if v, ok := body["remove_character"].(float64); ok {
		cid := int(v)
		var name string
		if db.QueryRow(`SELECT name FROM characters WHERE id = $1 AND lobby_id = $2`, cid, id).Scan(&name) != nil {
			return "", true, fmt.Errorf("character %d is not in this campaign", cid)
		}
		db.Exec(`UPDATE characters SET lobby_id = NULL WHERE id = $1`, cid)
		logAdminChange("character", cid, "lobby_id", strconv.Itoa(id), "NULL", "admin", note)
		reconcileCampaignStatus(id)
		return fmt.Sprintf("%s left the campaign.", name), true, nil
	}
	return "", false, nil
}

// handleAdminChanges: GET /api/admin/changes[?kind=character&id=N&limit=100]
func handleAdminChanges(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	kind := r.URL.Query().Get("kind")
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	rows, err := db.Query(`SELECT x.id, x.at, x.kind, x.row_id, x.field, COALESCE(x.old_value,''), COALESCE(x.new_value,''), x.by_whom, COALESCE(x.note,''),
		COALESCE(CASE WHEN x.kind = 'character' THEN (SELECT COALESCE(deleted_name, name) FROM characters WHERE id = x.row_id)
			ELSE (SELECT name FROM lobbies WHERE id = x.row_id) END, '')
		FROM admin_changes x WHERE ($1 = '' OR x.kind = $1) AND ($2 = 0 OR x.row_id = $2) ORDER BY x.id DESC LIMIT $3`, kind, id, limit)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": publicError(err)})
		return
	}
	defer rows.Close()
	list := []map[string]interface{}{}
	for rows.Next() {
		var cid, rowID int
		var at time.Time
		var k, field, oldV, newV, by, note, name string
		rows.Scan(&cid, &at, &k, &rowID, &field, &oldV, &newV, &by, &note, &name)
		list = append(list, map[string]interface{}{"id": cid, "at": at.Format("2006-01-02 15:04:05"), "kind": k, "row_id": rowID,
			"field": field, "old": oldV, "new": newV, "by": by, "note": note, "name": name})
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"changes": list})
}

// adminPagePath: the page's address from ADMIN_PATH (the operator chooses it, so it isn't guessable from the public code), or ""
// when the page is off: no ADMIN_KEY, no ADMIN_PATH, or a path that is too short, has other characters, or clashes with the site.
func adminPagePath() (string, string) {
	if os.Getenv("ADMIN_KEY") == "" {
		return "", "no ADMIN_KEY"
	}
	p := strings.TrimSpace(os.Getenv("ADMIN_PATH"))
	if p == "" {
		return "", "no ADMIN_PATH (choose a secret address such as /manage-<random letters>)"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	if len(p) < 13 {
		return "", "ADMIN_PATH must be at least 12 characters after the /"
	}
	for _, c := range p[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", "ADMIN_PATH may use only letters, digits, - and _"
		}
	}
	for _, taken := range []string{"/api", "/docs", "/character", "/campaign", "/profile", "/universe", "/how-it-works", "/about", "/commands", "/skill", "/admin"} {
		if strings.HasPrefix(strings.ToLower(p), taken) {
			return "", "ADMIN_PATH can't start with " + taken
		}
	}
	return p, ""
}

// registerAdminPage puts the page at its secret address (once, at startup).
func registerAdminPage() {
	p, why := adminPagePath()
	if p == "" {
		log.Printf("Admin page off: %s", why)
		return
	}
	http.HandleFunc(p, handleAdminPage)
	log.Printf("Admin page on (address from ADMIN_PATH)")
}

// handleAdminPage serves the admin page: a static page; the key stays in the browser tab (sessionStorage) and goes in X-Admin-Key.
func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if p, _ := adminPagePath(); p == "" || r.URL.Path != p {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	fmt.Fprint(w, adminPageHTML)
}

// handleAdminAccounts: GET /api/admin/accounts?q= finds accounts by name or id (to choose a character's player or a campaign's GM).
func handleAdminAccounts(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := db.Query(`SELECT id, name, (SELECT COUNT(*) FROM characters WHERE agent_id = agents.id) FROM agents
		WHERE LOWER(name) LIKE '%' || LOWER($1) || '%' OR id::text = $1 ORDER BY (LOWER(name) = LOWER($1)) DESC, id DESC LIMIT 20`, q)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": publicError(err)})
		return
	}
	defer rows.Close()
	list := []map[string]interface{}{}
	for rows.Next() {
		var id, n int
		var name string
		rows.Scan(&id, &name, &n)
		list = append(list, map[string]interface{}{"id": id, "name": name, "characters": n})
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"accounts": list})
}
