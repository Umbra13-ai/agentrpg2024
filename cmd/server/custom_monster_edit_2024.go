package main

// What a GM does with a compendium monster it owns (owner, Nov 22): save a new version, take one off the list, bring it back, share it
// with the other GMs or make it private again. A definition itself never changes: a fight that started with a key keeps reading it, so an
// edit is a new key that names the one it replaces (custom_monsters.replaces). Who may use an entry is compendiumVisible
// (custom_monsters.go); who may change one is ownedCompendiumEntry here: its owner only. Entries from before ownership have no owner and
// are nobody's to change: import the corrected monster again.

import (
	"database/sql"
	"net/http"
	"strings"
)

// compendiumKeyRequest is the body of the hide and restore requests.
type compendiumKeyRequest struct {
	MonsterKey string `json:"monster_key"` // the compendium monster (a custom: key); it must be one you imported
}

// ownedCompendiumEntry: whether the account owns the entry named by key, answering the refusal itself when it does not. The key has already
// been read as a reference (checkCustomReferences: an entry the caller may not use is 404), so what is left is an entry the caller may use
// but did not import.
func ownedCompendiumEntry(w http.ResponseWriter, agentID int, key string) bool {
	var owner sql.NullInt64
	err := db.QueryRow(`SELECT owner_id FROM custom_monsters WHERE key=$1`, key).Scan(&owner)
	if err == sql.ErrNoRows {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "monster_not_found", "message": "No compendium monster has that key. GET /api/gm/compendium/monsters lists yours. Nothing was changed."})
		return false
	}
	if answerDBFailure(w, err, "the compendium") {
		return false
	}
	if !owner.Valid || int(owner.Int64) != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_monster", "message": "Only the GM who imported a compendium monster changes it, and one imported before ownership was kept has no owner: import the corrected monster again. Nothing was changed."})
		return false
	}
	return true
}

// compendiumOwnerStart is the start every request here shares: the method and the sign-in. The handler then reads its body with gmReadAs
// (which also checks every compendium key the body names are visible to the caller) and asks compendiumKeyOwned.
func compendiumOwnerStart(w http.ResponseWriter, r *http.Request) (int, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "Use POST."})
		return 0, false
	}
	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return 0, false
	}
	return agent, true
}

// compendiumKeyOwned: the key a request names is a compendium key the caller imported; a request with no key passes "" and checks none.
func compendiumKeyOwned(w http.ResponseWriter, agent int, key string) bool {
	k := strings.TrimSpace(key)
	if k == "" {
		return true
	}
	if !strings.HasPrefix(strings.ToLower(k), "custom:") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_monster_key", "message": "monster_key is a custom: key from the compendium. Nothing was changed."})
		return false
	}
	return ownedCompendiumEntry(w, agent, k)
}

// handleGMCompendiumEdit godoc
// @Summary Edit a compendium monster
// @Description Saves a corrected stat block as a new version of a monster you imported, with the same sharing. The old version is taken off the list but a fight that started with it keeps it; the reply names the new monster_key and the one it replaces. Only the GM who imported it may edit it. campaign_id names a campaign you run (needed when you run more than one).
// @Tags GM
// @Router /gm/compendium/monsters/edit [post]
func handleGMCompendiumEdit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MonsterKey string         `json:"monster_key"` // the version to replace (a custom: key you imported)
		SourceNote string         `json:"source_note"` // where the corrected stat block comes from, up to 500 bytes; the old note stays when you leave it out
		Monster    srdMonster2024 `json:"monster"`     // the whole corrected stat block, as in an import
	}
	agent, ok := compendiumOwnerStart(w, r)
	if !ok {
		return
	}
	if _, ok := gmReadAs(w, r, agent, &req, campaignBeforeStart()); !ok || !compendiumKeyOwned(w, agent, req.MonsterKey) {
		return
	}
	if strings.TrimSpace(req.MonsterKey) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "monster_key_required", "message": "Name the version to replace with monster_key. Nothing was changed."})
		return
	}
	if len(req.SourceNote) > 500 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_monster", "message": "source_note is up to 500 bytes."})
		return
	}
	notes := normalizeCustomMonster(&req.Monster)
	if msg := validateCustomMonster(req.Monster); msg != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_monster", "message": "monster: " + msg})
		return
	}
	tx, err := db.Begin()
	if err != nil {
		writeDBWriteError(w, err, "the compendium")
		return
	}
	defer tx.Rollback()
	var note string
	var shared bool
	if err := tx.QueryRow(`SELECT source_note, shared FROM custom_monsters WHERE key=$1 AND owner_id=$2 FOR UPDATE`, req.MonsterKey, agent).Scan(&note, &shared); err != nil {
		if err == sql.ErrNoRows {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "monster_not_found", "message": "No compendium monster of yours has that key. Nothing was changed."})
			return
		}
		writeDBError(w, err, "the compendium")
		return
	}
	if strings.TrimSpace(req.SourceNote) != "" {
		note = req.SourceNote
	}
	key, ok := insertCompendiumMonster(w, tx, agent, req.Monster, note, req.MonsterKey, shared)
	if !ok {
		return
	}
	if _, err := tx.Exec(`UPDATE custom_monsters SET hidden_at = COALESCE(hidden_at, NOW()) WHERE key=$1`, req.MonsterKey); err != nil {
		writeDBWriteError(w, err, "the compendium")
		return
	}
	if err := tx.Commit(); err != nil {
		writeDBWriteError(w, err, "the compendium")
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]interface{}{"monster_key": key, "replaces": req.MonsterKey, "name": req.Monster.Name, "shared": shared, "report": customMonsterReport(req.Monster, notes, false),
		"message": "Saved as a new version. The old one is off your list; a fight that started with it keeps it. Use the new monster_key in combat/add."})
}

// setCompendiumHidden is hide and restore: only the owner's row, one statement.
func setCompendiumHidden(w http.ResponseWriter, r *http.Request, hide bool) {
	var req compendiumKeyRequest
	agent, ok := compendiumOwnerStart(w, r)
	if !ok {
		return
	}
	if _, ok := gmReadAs(w, r, agent, &req, campaignBeforeStart()); !ok || !compendiumKeyOwned(w, agent, req.MonsterKey) {
		return
	}
	if strings.TrimSpace(req.MonsterKey) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "monster_key_required", "message": "Name the compendium monster with monster_key. Nothing was changed."})
		return
	}
	set := "NULL"
	if hide {
		set = "COALESCE(hidden_at, NOW())"
	}
	if _, err := db.Exec(`UPDATE custom_monsters SET hidden_at = `+set+` WHERE key=$1 AND owner_id=$2`, req.MonsterKey, agent); err != nil {
		writeDBWriteError(w, err, "the compendium")
		return
	}
	state := "back on your list"
	if hide {
		state = "off your list (a fight that already has it, and anyone who knows its key, keeps it)"
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"monster_key": req.MonsterKey, "hidden": hide, "message": "The monster is " + state + "."})
}

// handleGMCompendiumHide godoc
// @Summary Hide a compendium monster
// @Description Takes a monster you imported off your compendium list. Nothing is erased: a fight that already has it keeps it, and POST /api/gm/compendium/monsters/restore brings it back. Only the GM who imported it may hide it. campaign_id names a campaign you run (needed when you run more than one).
// @Tags GM
// @Router /gm/compendium/monsters/hide [post]
func handleGMCompendiumHide(w http.ResponseWriter, r *http.Request) { setCompendiumHidden(w, r, true) }

// handleGMCompendiumRestore godoc
// @Summary Restore a hidden compendium monster
// @Description Puts a monster you hid back on your compendium list. Only the GM who imported it may restore it. campaign_id names a campaign you run (needed when you run more than one).
// @Tags GM
// @Router /gm/compendium/monsters/restore [post]
func handleGMCompendiumRestore(w http.ResponseWriter, r *http.Request) {
	setCompendiumHidden(w, r, false)
}

// handleGMCompendiumShare godoc
// @Summary Share a compendium monster with other GMs, or make it private
// @Description An import is private to you. shared true lets every GM on the server list it and use it in their campaigns (the stat block and its source_note are visible to them); shared false makes it private again, though a fight that already has it keeps it. Only the GM who imported it may change this. campaign_id names a campaign you run (needed when you run more than one).
// @Tags GM
// @Router /gm/compendium/monsters/share [post]
func handleGMCompendiumShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MonsterKey string `json:"monster_key"` // the compendium monster (a custom: key); it must be one you imported
		Shared     *bool  `json:"shared"`      // true: every GM may use it; false: only you
	}
	agent, ok := compendiumOwnerStart(w, r)
	if !ok {
		return
	}
	if _, ok := gmReadAs(w, r, agent, &req, campaignBeforeStart()); !ok || !compendiumKeyOwned(w, agent, req.MonsterKey) {
		return
	}
	if strings.TrimSpace(req.MonsterKey) == "" || req.Shared == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send monster_key and shared (true or false). Nothing was changed."})
		return
	}
	if _, err := db.Exec(`UPDATE custom_monsters SET shared = $3 WHERE key=$1 AND owner_id=$2`, req.MonsterKey, agent, *req.Shared); err != nil {
		writeDBWriteError(w, err, "the compendium")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"monster_key": req.MonsterKey, "shared": *req.Shared})
}
