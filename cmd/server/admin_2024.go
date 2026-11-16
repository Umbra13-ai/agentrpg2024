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
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func logAdminChange(kind string, id int, field, oldV, newV, by, note string) {
	logAdminChangeIn(db, kind, id, field, oldV, newV, by, note)
}

// logAdminChangeIn writes one admin_changes row through db or the caller's transaction.
func logAdminChangeIn(ex execer, kind string, id int, field, oldV, newV, by, note string) error {
	_, err := ex.Exec(`INSERT INTO admin_changes (kind, row_id, field, old_value, new_value, by_whom, note) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		kind, id, field, oldV, newV, by, note)
	return err
}

// characterIsDeleted: the character exists and was deleted.
func characterIsDeleted(charID int) bool {
	var deleted bool
	db.QueryRow(`SELECT deleted_at IS NOT NULL FROM characters WHERE id = $1`, charID).Scan(&deleted)
	return deleted
}

// campaignIsDeleted: the campaign exists and was deleted.
func campaignIsDeleted(id int) bool {
	var deleted bool
	db.QueryRow(`SELECT deleted_at IS NOT NULL FROM lobbies WHERE id = $1`, id).Scan(&deleted)
	return deleted
}

// softDeleteCharacter hides a character: no owner, no campaign, a placeholder name.
func softDeleteCharacter(charID int, by string) error {
	var lobby sql.NullInt64
	err := inTx(func(tx *sql.Tx) error {
		var e error
		lobby, e = softDeleteCharacterIn(tx, charID, by)
		return e
	})
	if err != nil {
		return err
	}
	if lobby.Valid {
		reconcileCampaignStatus(int(lobby.Int64))
	}
	return nil
}

// softDeleteCharacterIn deletes a character inside the caller's transaction (the row locked, the change and its admin_changes
// row together) and returns the campaign it was in. A refusal is an error a player reads; a database failure is a dbFailure.
func softDeleteCharacterIn(tx *sql.Tx, charID int, by string) (sql.NullInt64, error) {
	var owner, lobby sql.NullInt64
	var name string
	var deleted bool
	if err := tx.QueryRow(`SELECT agent_id, lobby_id, name, deleted_at IS NOT NULL FROM characters WHERE id = $1 FOR UPDATE`, charID).
		Scan(&owner, &lobby, &name, &deleted); err != nil {
		if dbFailed(err) {
			return lobby, dbFailureError(err, "the deletion")
		}
		return lobby, fmt.Errorf("character %d not found", charID)
	}
	if deleted {
		return lobby, fmt.Errorf("%s is already deleted", name)
	}
	placeholder := fmt.Sprintf("deleted character %d", charID)
	if _, err := tx.Exec(`UPDATE characters SET deleted_at = NOW(), deleted_owner = $1, deleted_name = $2, deleted_lobby = $3, deleted_by = $4,
		agent_id = NULL, lobby_id = NULL, name = $5 WHERE id = $6`, owner, name, lobby, by, placeholder, charID); err != nil {
		return lobby, dbFailureError(err, "the deletion")
	}
	if err := logAdminChangeIn(tx, "character", charID, "deleted", name, "deleted", by, ""); err != nil {
		return lobby, dbFailureError(err, "the deletion")
	}
	return lobby, nil
}

// restoreCharacter gives a deleted character back its owner and name (and campaign, when that campaign still exists and isn't
// deleted). It returns what happened.
func restoreCharacter(charID int, by string) (string, error) {
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
	if owner.Valid {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM agents WHERE id = $1)`, owner.Int64).Scan(&exists); err != nil {
			return "", dbFailureError(err, "the restore")
		}
		if !exists { // a moderator deleted the account (auth_and_logging.go): the character comes back without an owner
			owner = sql.NullInt64{}
			note += " Its account was deleted, so it has no owner: set one with the editor."
		}
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM characters WHERE LOWER(name) = LOWER($1) AND id <> $2`, newName, charID).Scan(&n)
	if n > 0 || newName == "" {
		newName = fmt.Sprintf("%s (restored %d)", name.String, charID)
		note = fmt.Sprintf(" Another character now has the name %q, so it is called %q: edit the name if you like.", name.String, newName)
	}
	backTo := 0
	if lobby.Valid {
		var status string
		db.QueryRow(`SELECT COALESCE(status,'') FROM lobbies WHERE id = $1`, lobby.Int64).Scan(&status)
		if status != "" && status != "deleted" && status != "completed" {
			backTo = int(lobby.Int64)
		} else {
			note += " Its old campaign is gone or finished, so it is in no campaign."
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return "", dbFailureError(err, "the restore")
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE characters SET agent_id = $1, lobby_id = NULL, name = $2, deleted_at = NULL, deleted_owner = NULL,
		deleted_name = NULL, deleted_lobby = NULL, deleted_by = NULL WHERE id = $3`, owner, newName, charID); err != nil {
		return "", dbFailureError(err, "the restore")
	}
	if backTo != 0 { // back into its campaign through its one way in: a free seat (schedule_2024.go)
		var ref *seatRefusal
		if _, err := claimSeat(tx, seatClaim{Lobby: backTo, Character: charID}); errors.As(err, &ref) {
			note += fmt.Sprintf(" It couldn't go back into campaign %d (%s), so it is in no campaign.", backTo, ref.Message)
		} else if err != nil {
			return "", dbFailureError(err, "the restore")
		} else {
			note += fmt.Sprintf(" It is back in campaign %d.", backTo)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", dbFailureError(err, "the restore")
	}
	if backTo != 0 {
		reconcileCampaignStatus(backTo)
	}
	logAdminChange("character", charID, "restored", "deleted", newName, by, "")
	return "Restored " + newName + "." + note, nil
}

// softDeleteCampaign hides a campaign and lets its characters go, in one transaction (the campaign row locked): either all of
// it happens or none of it.
func softDeleteCampaign(id int, by string) error {
	return inTx(func(tx *sql.Tx) error {
		var dm sql.NullInt64
		var status, name string
		if err := tx.QueryRow(`SELECT dm_id, COALESCE(status,''), name FROM lobbies WHERE id = $1 FOR UPDATE`, id).Scan(&dm, &status, &name); err != nil {
			if dbFailed(err) {
				return dbFailureError(err, "the deletion")
			}
			return fmt.Errorf("campaign %d not found", id)
		}
		if status == "deleted" {
			return fmt.Errorf("%s is already deleted", name)
		}
		members := []int{}
		rows, err := tx.Query(`SELECT id FROM characters WHERE lobby_id = $1 ORDER BY id`, id)
		if err != nil {
			return dbFailureError(err, "the deletion")
		}
		for rows.Next() {
			var c int
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return dbFailureError(err, "the deletion")
			}
			members = append(members, c)
		}
		rows.Close()
		mj, _ := json.Marshal(members)
		if _, err := tx.Exec(`UPDATE lobbies SET deleted_at = NOW(), deleted_dm = $1, deleted_status = $2, deleted_members = $3, dm_id = NULL,
			status = 'deleted' WHERE id = $4`, dm, status, string(mj), id); err != nil {
			return dbFailureError(err, "the deletion")
		}
		if _, err := tx.Exec(`UPDATE characters SET lobby_id = NULL WHERE lobby_id = $1`, id); err != nil {
			return dbFailureError(err, "the deletion")
		}
		return dbFailureError(logAdminChangeIn(tx, "campaign", id, "deleted", name, fmt.Sprintf("deleted (characters let go: %v)", members), by, ""), "the deletion")
	})
}

// restoreCampaign gives a deleted campaign back its GM and status, and brings back its characters that are free and not deleted.
func restoreCampaign(id int, by string) (string, error) {
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
		return "", dbFailureError(err, "the restore")
	}
	var members []int
	json.Unmarshal(membersRaw, &members)
	var back, notBack []string
	for _, c := range members {
		var deleted bool
		if db.QueryRow(`SELECT deleted_at IS NOT NULL FROM characters WHERE id = $1`, c).Scan(&deleted) != nil || deleted {
			notBack = append(notBack, strconv.Itoa(c))
			continue
		}
		// a free character only, through the one way into a campaign (schedule_2024.go)
		if _, err := enterCampaign(seatClaim{Lobby: id, Character: c, OnlyFromNone: true}); err == nil {
			back = append(back, strconv.Itoa(c))
		} else {
			notBack = append(notBack, strconv.Itoa(c))
		}
	}
	if len(back) > 0 {
		reconcileCampaignStatus(id)
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

// handleCharacterDelete godoc
// handleCharacterDelete: a player deletes their own character.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /characters/{id} [delete]
// @Router /characters/{id}/delete [post]
func handleCharacterDelete(w http.ResponseWriter, r *http.Request, charID int) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var owner sql.NullInt64
	var name, status string
	if err := db.QueryRow(`SELECT c.agent_id, c.name, COALESCE(l.status,'') FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id WHERE c.id = $1 AND c.deleted_at IS NULL`, charID).
		Scan(&owner, &name, &status); err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
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
	var body struct {
		Confirm string `json:"confirm"` // the character's name
	}
	if !readRequest(w, r, &body, optionalBody()) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(body.Confirm), name) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "confirm_required",
			"message": fmt.Sprintf("To delete %s, send {\"confirm\": %q}. The character is hidden, not erased: the server's admin can restore it if you change your mind.", name, name)})
		return
	}
	if err := softDeleteCharacter(charID, fmt.Sprintf("player %d", agentID)); answerHelperDBFailure(w, err) {
		return
	} else if err != nil {
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

// handleAdminSession godoc
// handleAdminSession: POST with the X-Admin-Key header signs in (sets the cookie); DELETE signs out; GET says whether signed in.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/session [get]
// @Router /admin/session [post]
// @Router /admin/session [delete]
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

// editorColumns: who may write a column directly through an editor (the admin page, and the GM's "set" on a character in
// their campaign): one table for both. "never": no editor (the server's own bookkeeping: ids, the soft-delete record, passwords,
// and a character's class levels and subclasses, which follow from its one class); "admin": the admin page only (who owns a
// character, which campaign it is in, its class). A column not listed is a game value both may set (Hit Points, gold,
// conditions ...). Before Nov 15 2026 the GM's list protected only four columns: a GM could write the soft-delete record or a
// second class (audit E-11, D-3).
var editorColumns = map[string]string{
	"id": "never", "created_at": "never", "avatar": "never", "password_hash": "never", "watch_password_hash": "never",
	"deleted_at": "never", "deleted_owner": "never", "deleted_name": "never", "deleted_lobby": "never", "deleted_by": "never",
	"deleted_dm": "never", "deleted_status": "never", "deleted_members": "never",
	"class_levels": "never", "subclasses": "never", "hit_dice_spent_by_type": "never",
	"agent_id": "admin", "lobby_id": "admin", "class": "admin", "subclass": "admin",
	"schedule_state": "never", "pending_capacity": "never", // the GM's PATCH /api/campaigns/{id}/schedule (schedule_2024.go)
}

// adminCapacityProblem: a campaign using the turn schedule keeps 1 to 6 seats (seatCapacityProblem), whoever edits it.
func adminCapacityProblem(tx *sql.Tx, lobby int, text string) error {
	var state string
	if err := tx.QueryRow(`SELECT COALESCE(schedule_state, 'off') FROM lobbies WHERE id = $1`, lobby).Scan(&state); err != nil {
		return fmt.Errorf("campaign %d not found", lobby)
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("max_players: %q isn't a number", text)
	}
	if state != scheduleOff {
		if p := seatCapacityProblem(n); p != "" {
			return fmt.Errorf("max_players: %s", p)
		}
	}
	return nil
}

// adminMayEdit / gmMayEdit: may this editor write the column (editorColumns)?
func adminMayEdit(col string) bool { return editorColumns[col] != "never" }
func gmMayEdit(col string) bool    { return editorColumns[col] == "" }

// adminLocked: the columns the admin page shows as locked.
func adminLocked() map[string]bool {
	out := map[string]bool{}
	for c := range editorColumns {
		if !adminMayEdit(c) {
			out[c] = true
		}
	}
	return out
}

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
		if !adminMayEdit(k) {
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
		if kind == "character" && k == "lobby_id" && val != nil { // into a campaign: its one way in (schedule_2024.go)
			lobby, err := strconv.Atoi(text)
			if err != nil {
				return nil, fmt.Errorf("lobby_id: %q isn't a campaign id", text)
			}
			if _, err := claimSeat(tx, seatClaim{Lobby: lobby, Character: id}); err != nil {
				return nil, fmt.Errorf("lobby_id: %w", dbFailureError(err, "the seat"))
			}
		} else if kind == "campaign" && k == "max_players" {
			if err := adminCapacityProblem(tx, id, text); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE lobbies SET max_players = $1 WHERE id = $2`, val, id); err != nil {
				return nil, fmt.Errorf("%s: %q doesn't fit this field (its type is %s)", k, text, cols[k])
			}
		} else if kind == "character" && conditionColumns[k] { // the condition owner writes them (condition_owner_2024.go)
			if err := setConditionColumnIn(tx, id, k, set[k]); err != nil {
				return nil, fmt.Errorf("%s: %v", k, err)
			}
			tx.QueryRow(fmt.Sprintf(`SELECT %q::text FROM %s WHERE id = $1`, k, table), id).Scan(&text)
		} else if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET %q = $1 WHERE id = $2`, table, k), val, id); err != nil {
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
	// follow-up: a campaign's status depends on its players
	if kind == "character" {
		for _, k := range changed {
			switch k {
			case "inventory", "attuned_items":
				syncAttunement(id) // attunement only to items carried (inventory_2024.go)
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

// handleAdminCharacters godoc
// handleAdminCharacters: GET /api/admin/characters[?q=&deleted=include|only], GET/POST /api/admin/characters/{id},
// POST /api/admin/characters/{id}/delete and /restore.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/characters [get]
// @Router /admin/characters/{id} [get]
// @Router /admin/characters/{id} [post]
// @Router /admin/characters/{id}/delete [post]
// @Router /admin/characters/{id}/restore [post]
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
			writeDBError(w, err, "the characters")
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

// handleAdminCampaigns godoc
// handleAdminCampaigns: the same for campaigns, plus {"add_character": id} and {"remove_character": id}.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/campaigns [get]
// @Router /admin/campaigns/{id} [get]
// @Router /admin/campaigns/{id} [post]
// @Router /admin/campaigns/{id}/delete [post]
// @Router /admin/campaigns/{id}/restore [post]
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
			writeDBError(w, err, "the campaigns")
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
	switch { // delete and restore first: a route with more segments is told apart before the row's own GET and POST
	case action == "delete" && r.Method == http.MethodPost:
		if !noFields(w, r) { // takes no body (feedback log 43: the reference showed the editor's fields here)
			return
		}
		var err error
		if kind == "character" {
			err = softDeleteCharacter(id, "admin")
		} else {
			err = softDeleteCampaign(id, "admin")
		}
		if answerHelperDBFailure(w, err) {
			return
		}
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_deleted", "message": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": fmt.Sprintf("Deleted %s %d. It can be restored.", kind, id)})
	case action == "restore" && r.Method == http.MethodPost:
		if !noFields(w, r) {
			return
		}
		var msg string
		var err error
		if kind == "character" {
			msg, err = restoreCharacter(id, "admin")
		} else {
			msg, err = restoreCampaign(id, "admin")
		}
		if answerHelperDBFailure(w, err) {
			return
		}
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_restored", "message": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": msg})
	case action == "" && r.Method == http.MethodGet:
		row := adminRow(table, id)
		if row == nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": kind + "_not_found"})
			return
		}
		out := map[string]interface{}{kind: row, "field_types": tableColumns(table), "locked_fields": adminLocked()}
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
		if !readRequest(w, r, &body, onlyFields(
			"set",              // {"field": value}: the columns to change (the GET of the row lists field_types and locked_fields)
			"note",             // why: written to the admin change log
			"add_character",    // a campaign: the id of a character to put in it
			"remove_character", // a campaign: the id of a character to take out of it
		)) {
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
		if _, err := enterCampaign(seatClaim{Lobby: id, Character: cid}); err != nil { // a free seat, the capacity checked (schedule_2024.go)
			return "", true, fmt.Errorf("%s can't join campaign %d: %w", name, id, dbFailureError(err, "the seat"))
		}
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

// handleAdminChanges godoc
// handleAdminChanges: GET /api/admin/changes[?kind=character&id=N&limit=100]
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/changes [get]
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
		COALESCE(CASE x.kind WHEN 'character' THEN (SELECT COALESCE(deleted_name, name) FROM characters WHERE id = x.row_id)
			WHEN 'account' THEN (SELECT name FROM agents WHERE id = x.row_id)
			ELSE (SELECT name FROM lobbies WHERE id = x.row_id) END, '')
		FROM admin_changes x WHERE ($1 = '' OR x.kind = $1) AND ($2 = 0 OR x.row_id = $2) ORDER BY x.id DESC LIMIT $3`, kind, id, limit)
	if err != nil {
		writeDBError(w, err, "the change log")
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
	for _, taken := range []string{"/api", "/docs", "/character", "/campaign", "/profile", "/universe", "/how-it-works", "/about", "/commands", "/skill", "/gm-guide", "/admin"} {
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

// handleAdminAccounts godoc
// handleAdminAccounts: GET /api/admin/accounts?q= finds accounts by name or id (to choose a character's player or a campaign's GM).
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/accounts [get]
func handleAdminAccounts(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := db.Query(`SELECT id, name, (SELECT COUNT(*) FROM characters WHERE agent_id = agents.id) FROM agents
		WHERE LOWER(name) LIKE '%' || LOWER($1) || '%' OR id::text = $1 ORDER BY (LOWER(name) = LOWER($1)) DESC, id DESC LIMIT 20`, q)
	if err != nil {
		writeDBError(w, err, "the accounts")
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
