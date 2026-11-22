package main

// The admin's accounts (the Accounts tab of the admin page; owner, Nov 22: "edit and delete accounts, not just players"). An account is a row of
// agents. The admin's editor, delete and restore are the same ones characters and campaigns have (adminRowRoutes, adminSet): this file is what is
// particular to an account.
//
//   - Delete hides, nothing is erased (as for characters and campaigns): agents.deleted_at, deleted_by and deleted_characters (the characters
//     hidden with it). A deleted account cannot sign in: signInOnce (auth_and_logging.go) asks accountDeleted once for every kind of sign-in, so
//     its tokens and signing keys stop working and come back with a restore. Its email and name stay reserved.
//   - Its characters are deleted with it (softDeleteCharacterIn, restorable) and come back with it (restoreCharacter). An account that is the GM of
//     a campaign cannot be deleted: the refusal names the campaign (delete it, or give it another GM, first), as the moderator's delete-user does.
//   - What may be edited is editorColumns' ("never": the password hash and salt, the verification code, the delete record, last_seen); a name or an
//     email is checked here (adminAccountValue): a name is 1 to 64 characters and unused, an email is a valid address nobody else has.
//   - GET /api/admin/accounts lists accounts (q by name, email or id; deleted=include|only); the secrets of a row are never sent (adminRow).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// accountDeleted: the account exists and was deleted by the admin.
func accountDeleted(id int) (bool, error) {
	var deleted bool
	err := db.QueryRow(`SELECT COALESCE(deleted_at IS NOT NULL, false) FROM agents WHERE id = $1`, id).Scan(&deleted)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return deleted, err
}

// accountUsable: an account an editor may give a character or a campaign to (it exists and isn't deleted).
func accountUsable(id int) error {
	var deleted bool
	if err := db.QueryRow(`SELECT deleted_at IS NOT NULL FROM agents WHERE id = $1`, id).Scan(&deleted); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("account %d not found", id)
		}
		return dbFailureError(err, "the account")
	}
	if deleted {
		return fmt.Errorf("account %d is deleted: restore it first", id)
	}
	return nil
}

// softDeleteAccount hides an account and its characters in one transaction (the account row locked). It is refused while the account runs a campaign.
func softDeleteAccount(id int, by string) error {
	var lobbies []int
	err := inTx(func(tx *sql.Tx) error {
		var name string
		var deleted bool
		if err := tx.QueryRow(`SELECT COALESCE(name,''), deleted_at IS NOT NULL FROM agents WHERE id = $1 FOR UPDATE`, id).Scan(&name, &deleted); err != nil {
			if dbFailed(err) {
				return dbFailureError(err, "the deletion")
			}
			return fmt.Errorf("account %d not found", id)
		}
		if deleted {
			return fmt.Errorf("%s is already deleted", name)
		}
		rows, err := tx.Query(`SELECT id, name FROM lobbies WHERE dm_id = $1 AND deleted_at IS NULL ORDER BY id`, id)
		if err != nil {
			return dbFailureError(err, "the deletion")
		}
		var runs []string
		for rows.Next() {
			var cid int
			var cname string
			rows.Scan(&cid, &cname)
			runs = append(runs, fmt.Sprintf("%q (campaign %d)", cname, cid))
		}
		rows.Close()
		if len(runs) > 0 {
			return fmt.Errorf("%s is the GM of %s: delete that campaign, or give it another GM, first", name, strings.Join(runs, ", "))
		}
		var chars []int
		rows, err = tx.Query(`SELECT id FROM characters WHERE agent_id = $1 AND deleted_at IS NULL ORDER BY id`, id)
		if err != nil {
			return dbFailureError(err, "the deletion")
		}
		for rows.Next() {
			var c int
			rows.Scan(&c)
			chars = append(chars, c)
		}
		rows.Close()
		for _, c := range chars {
			lobby, err := softDeleteCharacterIn(tx, c, by)
			if err != nil {
				return err
			}
			if lobby.Valid {
				lobbies = append(lobbies, int(lobby.Int64))
			}
		}
		list, _ := json.Marshal(chars)
		if _, err := tx.Exec(`UPDATE agents SET deleted_at = NOW(), deleted_by = $1, deleted_characters = $2::jsonb WHERE id = $3`, by, string(list), id); err != nil {
			return dbFailureError(err, "the deletion")
		}
		return logAdminChangeIn(tx, "account", id, "deleted", name, "deleted", by, "")
	})
	if err != nil {
		return err
	}
	for _, l := range lobbies {
		reconcileCampaignStatus(l)
	}
	return nil
}

// restoreAccount gives a deleted account its sign-in back and restores the characters that were hidden with it (restoreCharacter: each as it
// can be, its notes joined). It returns what happened.
func restoreAccount(id int, by string) (string, error) {
	var chars []int
	var name string
	err := inTx(func(tx *sql.Tx) error {
		var deleted bool
		var raw []byte
		if err := tx.QueryRow(`SELECT COALESCE(name,''), deleted_at IS NOT NULL, COALESCE(deleted_characters, '[]') FROM agents WHERE id = $1 FOR UPDATE`, id).Scan(&name, &deleted, &raw); err != nil {
			if dbFailed(err) {
				return dbFailureError(err, "the restore")
			}
			return fmt.Errorf("account %d not found", id)
		}
		if !deleted {
			return fmt.Errorf("account %d is not deleted", id)
		}
		json.Unmarshal(raw, &chars)
		if _, err := tx.Exec(`UPDATE agents SET deleted_at = NULL, deleted_by = NULL, deleted_characters = NULL WHERE id = $1`, id); err != nil {
			return dbFailureError(err, "the restore")
		}
		return logAdminChangeIn(tx, "account", id, "restored", "deleted", name, by, "")
	})
	if err != nil {
		return "", err
	}
	msg := "Restored " + name + ": it can sign in again."
	for _, c := range chars {
		if gone, _ := characterIsDeletedErr(c); !gone {
			continue // restored on its own since
		}
		m, err := restoreCharacter(c, by)
		if err != nil {
			msg += fmt.Sprintf(" Character %d: %v.", c, err)
			continue
		}
		msg += " " + m
	}
	return msg, nil
}

// characterIsDeletedErr: characterIsDeleted with its database error.
func characterIsDeletedErr(id int) (bool, error) {
	var d bool
	err := db.QueryRow(`SELECT COALESCE(deleted_at IS NOT NULL, false) FROM characters WHERE id = $1`, id).Scan(&d)
	return d, err
}

// adminAccountValue checks a value an editor sets on an account and answers it as the database should hold it: a name is 1 to 64 characters with no
// control characters and no other account's; an email is one valid address (lower case) nobody else has.
func adminAccountValue(id int, field string, v interface{}) (interface{}, error) {
	switch field {
	case "name":
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if n := len([]rune(s)); n < 1 || n > 64 || strings.IndexFunc(s, func(c rune) bool { return c < 32 || c == 127 }) >= 0 {
			return nil, fmt.Errorf("name: an agent name is 1 to 64 characters, with no control characters")
		}
		var other int
		if err := db.QueryRow(`SELECT id FROM agents WHERE name = $1 AND id <> $2 LIMIT 1`, s, id).Scan(&other); err == nil {
			return nil, fmt.Errorf("name: account %d already has the name %q", other, s)
		}
		return s, nil
	case "email":
		s, _ := v.(string)
		e, ok := validEmail(s)
		if !ok {
			return nil, fmt.Errorf("email: %q isn't one valid email address", s)
		}
		var other int
		if err := db.QueryRow(`SELECT id FROM agents WHERE lower(email) = lower($1) AND id <> $2 LIMIT 1`, e, id).Scan(&other); err == nil {
			return nil, fmt.Errorf("email: account %d already uses %s", other, e)
		}
		return e, nil
	}
	return v, nil
}

// adminAccountExtras: what the account's page shows besides its row: its characters (with the ones hidden with it) and the campaigns it runs.
func adminAccountExtras(id int) map[string]interface{} {
	chars := []map[string]interface{}{}
	if rows, err := db.Query(`SELECT c.id, COALESCE(c.deleted_name, c.name), COALESCE(c.class,''), COALESCE(c.level,1), COALESCE(l.name, dl.name, ''), c.deleted_at IS NOT NULL
		FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id LEFT JOIN lobbies dl ON dl.id = c.deleted_lobby
		WHERE c.agent_id = $1 OR c.deleted_owner = $1 ORDER BY c.id`, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var cid, level int
			var name, class, camp string
			var del bool
			rows.Scan(&cid, &name, &class, &level, &camp, &del)
			chars = append(chars, map[string]interface{}{"id": cid, "name": name, "class": class, "level": level, "campaign": camp, "deleted": del})
		}
	}
	camps := []map[string]interface{}{}
	if rows, err := db.Query(`SELECT id, name, COALESCE(status,'') FROM lobbies WHERE dm_id = $1 AND deleted_at IS NULL ORDER BY id`, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, status string
			rows.Scan(&cid, &name, &status)
			camps = append(camps, map[string]interface{}{"id": cid, "name": name, "status": status})
		}
	}
	return map[string]interface{}{"characters": chars, "campaigns": camps}
}

// handleAdminAccounts godoc
// handleAdminAccounts: GET /api/admin/accounts[?q=&deleted=include|only] lists accounts (by name, email or id; also how the admin page chooses a character's
// player or a campaign's GM), GET/POST /api/admin/accounts/{id}, POST /api/admin/accounts/{id}/delete and /restore.
// The words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/accounts [get]
// @Router /admin/accounts/{id} [get]
// @Router /admin/accounts/{id} [post]
// @Router /admin/accounts/{id}/delete [post]
// @Router /admin/accounts/{id}/restore [post]
func handleAdminAccounts(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/accounts"), "/"), "/")
	if parts[0] != "" {
		id, err := strconv.Atoi(parts[0])
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_id"})
			return
		}
		adminRowRoutes(w, r, "agents", "account", id, parts[1:])
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	cond := "a.deleted_at IS NULL"
	switch r.URL.Query().Get("deleted") {
	case "include":
		cond = "TRUE"
	case "only":
		cond = "a.deleted_at IS NOT NULL"
	}
	rows, err := db.Query(`SELECT a.id, COALESCE(a.name,''), COALESCE(a.email,''), COALESCE(a.verified,false), COALESCE(a.email_verified,false), COALESCE(a.is_moderator,false),
		(SELECT COUNT(*) FROM characters WHERE agent_id = a.id), (SELECT COUNT(*) FROM lobbies WHERE dm_id = a.id AND deleted_at IS NULL),
		to_char(a.created_at, 'YYYY-MM-DD'), COALESCE(to_char(a.last_seen, 'YYYY-MM-DD HH24:MI'),''), a.deleted_at IS NOT NULL
		FROM agents a WHERE `+cond+` AND (LOWER(a.name) LIKE '%' || LOWER($1) || '%' OR LOWER(a.email) LIKE '%' || LOWER($1) || '%' OR a.id::text = $1)
		ORDER BY (LOWER(a.name) = LOWER($1)) DESC, a.id DESC LIMIT 200`, q)
	if err != nil {
		writeDBError(w, err, "the accounts")
		return
	}
	defer rows.Close()
	list := []map[string]interface{}{}
	for rows.Next() {
		var id, chars, camps int
		var name, email, created, seen string
		var verified, emailVerified, mod, deleted bool
		rows.Scan(&id, &name, &email, &verified, &emailVerified, &mod, &chars, &camps, &created, &seen, &deleted)
		list = append(list, map[string]interface{}{"id": id, "name": name, "email": email, "verified": verified, "email_verified": emailVerified, "is_moderator": mod,
			"characters": chars, "campaigns": camps, "created_at": created, "last_seen": seen, "deleted": deleted})
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"accounts": list})
}
