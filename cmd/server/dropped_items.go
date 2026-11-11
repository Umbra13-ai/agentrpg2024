package main

// Dropped items and the ground.
// A player can equip or unequip one weapon per turn in combat, and that is not
// an action. To switch weapons and still attack, they must drop the weapon in
// their hand (dropping is free and does not count as that one equip or
// unequip), then equip the new one. A dropped item is not gone: it lies on the
// ground of the campaign where anyone can pick it up, or it can be forgotten.
//	POST /api/characters/{id}/equipment/drop      {"item": "scimitar", "quantity": 1}
//	POST /api/characters/{id}/equipment/pick-up   {"dropped_id": 12}
//	GET  /api/campaigns/{id}/ground               items on the ground (members and GM)
//	POST /api/campaigns/{id}/ground/{id}/claim    GM: someone or something took it {"note": "..."}
//	POST /api/campaigns/{id}/ground/{id}/return   GM: put a claimed item back on the ground

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func ensureDroppedItemsTable() {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS dropped_items (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		quantity INTEGER NOT NULL DEFAULT 1,
		bundle TEXT NOT NULL,
		dropped_by INTEGER,
		dropped_at TIMESTAMP DEFAULT NOW(),
		taken_by INTEGER,
		taken_at TIMESTAMP,
		taken_note TEXT
	)`)
	if err != nil {
		fmt.Printf("dropped_items table: %v\n", err)
	}
}

// collectBundle returns the item with the given id followed by everything inside it, at any depth.
func collectBundle(inv []invItem, rootID string) []invItem {
	var out []invItem
	in := map[string]bool{rootID: true}
	for _, it := range inv {
		if itemStr(it, "id") == rootID {
			out = append(out, it)
		}
	}
	changed := true
	for changed {
		changed = false
		for _, it := range inv {
			id := itemStr(it, "id")
			if !in[id] && in[itemStr(it, "location")] {
				in[id] = true
				out = append(out, it)
				changed = true
			}
		}
	}
	return out
}

// removeBundle removes an item and everything inside it.
func removeBundle(inv []invItem, rootID string) []invItem {
	drop := map[string]bool{}
	for _, it := range collectBundle(inv, rootID) {
		drop[itemStr(it, "id")] = true
	}
	out := make([]invItem, 0, len(inv))
	for _, it := range inv {
		if !drop[itemStr(it, "id")] {
			out = append(out, it)
		}
	}
	return out
}

func copyItem(it invItem) invItem {
	c := invItem{}
	for k, v := range it {
		c[k] = v
	}
	return c
}

type dropResult struct {
	DroppedID int
	Name      string
	Quantity  int
	Contents  int
}

// dropToGround takes an item (or part of a stack) out of a character's
// inventory and puts it on the ground of their campaign.
func dropToGround(charID int, itemRef string, quantity int) (*dropResult, string) {
	var lobby sql.NullInt64
	var charName string
	var armor sql.NullString
	if err := db.QueryRow(`SELECT lobby_id, name, equipped_armor FROM characters WHERE id = $1`, charID).Scan(&lobby, &charName, &armor); err != nil {
		return nil, "character_not_found"
	}
	if !lobby.Valid {
		return nil, "not_in_a_campaign"
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, "db_error"
	}
	defer tx.Rollback()
	inv, err := loadInventoryTx(tx, charID)
	if err != nil {
		return nil, "character_not_found"
	}
	it, ferr := findItem(inv, itemRef)
	if ferr != nil {
		return nil, ferr.Error()
	}
	if isLost(it, indexByID(inv)) {
		return nil, "item_is_lost"
	}
	if armor.Valid && armor.String != "" && itemKey(itemStr(it, "name")) == itemKey(armor.String) {
		return nil, "take_off_your_armor_first"
	}

	rootID := itemStr(it, "id")
	var bundle []invItem
	qty := itemQty(it)
	switch {
	case !isContainer(it) && quantity > 0 && quantity < qty:
		piece := copyItem(it)
		piece["quantity"] = float64(quantity)
		piece["location"] = ""
		bundle = []invItem{piece}
		it["quantity"] = float64(qty - quantity)
		qty = quantity
	case quantity > qty:
		return nil, "not_enough_in_stack"
	default:
		bundle = collectBundle(inv, rootID)
		root := copyItem(bundle[0])
		root["location"] = ""
		bundle[0] = root
		inv = removeBundle(inv, rootID)
	}

	if err := saveInventoryTx(tx, charID, inv); err != nil {
		return nil, "save_failed"
	}
	b, _ := json.Marshal(bundle)
	var dropID int
	if err := tx.QueryRow(`INSERT INTO dropped_items (lobby_id, name, quantity, bundle, dropped_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		lobby.Int64, itemStr(bundle[0], "name"), qty, string(b), charID).Scan(&dropID); err != nil {
		return nil, "save_failed"
	}
	if _, err := tx.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'drop', $3, $4)`,
		lobby.Int64, charID, fmt.Sprintf("%s drops %s", charName, itemStr(bundle[0], "name")), "It lies on the ground."); err != nil {
		return nil, "save_failed"
	}
	if err := tx.Commit(); err != nil {
		return nil, "save_failed"
	}

	// a weapon that was in hand and is no longer carried leaves the hand
	clearHandsIfGone(charID)
	syncHeldItems(charID)
	return &dropResult{DroppedID: dropID, Name: itemStr(bundle[0], "name"), Quantity: qty, Contents: len(bundle) - 1}, ""
}

// clearHandsIfGone empties a hand whose weapon is no longer in the inventory.
func clearHandsIfGone(charID int) {
	var raw []byte
	var mainHand, offHand sql.NullString
	if err := db.QueryRow(`SELECT COALESCE(inventory, '[]'), equipped_main_hand, equipped_off_hand FROM characters WHERE id = $1`, charID).
		Scan(&raw, &mainHand, &offHand); err != nil {
		return
	}
	var inv []invItem
	json.Unmarshal(raw, &inv)
	isLostItem := lostChecker(inv)
	carried := func(weapon string) bool {
		for _, it := range inv {
			if !isContainer(it) && !isLostItem(it) && sameWeapon(itemStr(it, "name"), weapon) {
				return true
			}
		}
		return false
	}
	if mainHand.Valid && mainHand.String != "" && !carried(mainHand.String) {
		db.Exec(`UPDATE characters SET equipped_main_hand = NULL WHERE id = $1`, charID)
	}
	if offHand.Valid && offHand.String != "" && !carried(offHand.String) {
		db.Exec(`UPDATE characters SET equipped_off_hand = NULL WHERE id = $1`, charID)
	}
}

// pickUpFromGround gives a dropped item to a character in the same campaign.
func pickUpFromGround(charID, droppedID int) (string, string) {
	var lobby sql.NullInt64
	var charName string
	if err := db.QueryRow(`SELECT lobby_id, name FROM characters WHERE id = $1`, charID).Scan(&lobby, &charName); err != nil {
		return "", "character_not_found"
	}
	tx, err := db.Begin()
	if err != nil {
		return "", "db_error"
	}
	defer tx.Rollback()

	var dropLobby int
	var name, bundleRaw string
	var takenBy sql.NullInt64
	var takenNote sql.NullString
	if err := tx.QueryRow(`SELECT lobby_id, name, bundle, taken_by, taken_note FROM dropped_items WHERE id = $1 FOR UPDATE`, droppedID).
		Scan(&dropLobby, &name, &bundleRaw, &takenBy, &takenNote); err != nil {
		return "", "nothing_on_the_ground_with_that_id"
	}
	if !lobby.Valid || int(lobby.Int64) != dropLobby {
		return "", "not_in_the_same_campaign"
	}
	if takenBy.Valid || takenNote.Valid {
		return "", "already_taken"
	}
	var bundle []invItem
	if json.Unmarshal([]byte(bundleRaw), &bundle) != nil || len(bundle) == 0 {
		return "", "corrupt_item"
	}

	inv, err := loadInventoryTx(tx, charID)
	if err != nil {
		return "", "character_not_found"
	}
	remap := map[string]string{}
	maxN := 0
	for _, it := range inv {
		var n int
		if id := itemStr(it, "id"); strings.HasPrefix(id, "i") {
			fmt.Sscanf(id[1:], "%d", &n)
			if n > maxN {
				maxN = n
			}
		}
	}
	for i, it := range bundle {
		remap[itemStr(it, "id")] = fmt.Sprintf("i%d", maxN+i+1)
	}
	for i, it := range bundle {
		it["id"] = remap[itemStr(it, "id")]
		if i == 0 {
			it["location"] = ""
		} else if m, ok := remap[itemStr(it, "location")]; ok {
			it["location"] = m
		}
		delete(it, "lost")
		inv = append(inv, it)
	}
	fillItemWeights(inv)
	if err := saveInventoryTx(tx, charID, inv); err != nil {
		return "", "save_failed"
	}
	if _, err := tx.Exec(`UPDATE dropped_items SET taken_by = $1, taken_at = NOW() WHERE id = $2`, charID, droppedID); err != nil {
		return "", "save_failed"
	}
	if _, err := tx.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'pick_up', $3, $4)`,
		dropLobby, charID, fmt.Sprintf("%s picks up %s", charName, name), "It is now in their pack."); err != nil {
		return "", "save_failed"
	}
	if err := tx.Commit(); err != nil {
		return "", "save_failed"
	}
	return name, ""
}

// droppedByCharacter lists what a character dropped that is still on the ground.
func droppedByCharacter(charID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	rows, err := db.Query(`SELECT id, name, quantity, dropped_at FROM dropped_items
		WHERE dropped_by = $1 AND taken_by IS NULL AND taken_note IS NULL ORDER BY id`, charID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, qty int
		var name string
		var at time.Time
		if rows.Scan(&id, &name, &qty, &at) == nil {
			out = append(out, map[string]interface{}{"dropped_id": id, "name": name, "quantity": qty, "dropped_at": at.Format(time.RFC3339)})
		}
	}
	return out
}

func handleCharacterDrop(w http.ResponseWriter, r *http.Request, charID int) {
	var req struct {
		Item     string `json:"item"`
		Quantity int    `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Item) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_required"})
		return
	}
	res, errCode := dropToGround(charID, req.Item, req.Quantity)
	if errCode != "" {
		status := http.StatusBadRequest
		if strings.HasPrefix(errCode, "ambiguous_item") {
			status = http.StatusConflict
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": errCode})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{
		"success": true, "dropped_id": res.DroppedID, "name": res.Name, "quantity": res.Quantity, "items_inside": res.Contents,
		"note": "It lies on the ground. Anyone in the campaign can pick it up (POST /api/characters/{id}/equipment/pick-up), and it stays there until someone does. Dropping is free.",
	})
}

func handleCharacterPickUp(w http.ResponseWriter, r *http.Request, charID int) {
	var req struct {
		DroppedID int `json:"dropped_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DroppedID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "dropped_id_required"})
		return
	}
	name, errCode := pickUpFromGround(charID, req.DroppedID)
	if errCode != "" {
		status := http.StatusBadRequest
		if errCode == "already_taken" {
			status = http.StatusConflict
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": errCode})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "picked_up": name,
		"note": "It is in your pack, not yet equipped or sorted into a bag."})
}

// handleCampaignGround lists and manages the items lying on the ground of a campaign.
// handleCampaignGround godoc
// @Summary The items lying on the ground of a campaign
// @Description GET lists what lies on the ground (dropped with /characters/{id}/equipment/drop; the GM adds ?include=claimed to see items taken away too). Players in the campaign pick an item up with /characters/{id}/equipment/pick-up. GM only: POST /ground/{dropped_id}/claim takes an item off the ground with a note ({"note": "the troll took it"}), POST /ground/{dropped_id}/return puts it back.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Param include query string false "claimed (GM)"
// @Param request body object{note=string} false "Why it left the ground (claim)"
// @Router /campaigns/{id}/ground [get]
// @Router /campaigns/{id}/ground/{dropped_id}/claim [post]
// @Router /campaigns/{id}/ground/{dropped_id}/return [post]
func handleCampaignGround(w http.ResponseWriter, r *http.Request, campaignID int, rest []string) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	var dmID int
	if err := db.QueryRow(`SELECT dm_id FROM lobbies WHERE id = $1`, campaignID).Scan(&dmID); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "campaign_not_found"})
		return
	}
	isGM := dmID == agentID
	if !isGM {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM characters WHERE lobby_id = $1 AND agent_id = $2`, campaignID, agentID).Scan(&n)
		if n == 0 {
			writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_in_this_campaign"})
			return
		}
	}

	// POST /ground/{id}/claim or /return (GM only)
	if len(rest) >= 2 {
		if r.Method != http.MethodPost || !isGM {
			writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "only_the_gm_can_do_that"})
			return
		}
		did, convErr := strconv.Atoi(rest[0])
		if convErr != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_dropped_id"})
			return
		}
		switch rest[1] {
		case "claim":
			var req struct {
				Note string `json:"note"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			note := strings.TrimSpace(req.Note)
			if note == "" {
				note = "taken by someone"
			}
			res, _ := db.Exec(`UPDATE dropped_items SET taken_note = $1, taken_at = NOW() WHERE id = $2 AND lobby_id = $3 AND taken_by IS NULL AND taken_note IS NULL`, note, did, campaignID)
			if n, _ := res.RowsAffected(); n == 0 {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_on_the_ground"})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "note": "It is off the ground but kept on record. Use /return to put it back."})
		case "return":
			res, _ := db.Exec(`UPDATE dropped_items SET taken_note = NULL, taken_at = NULL WHERE id = $1 AND lobby_id = $2 AND taken_by IS NULL AND taken_note IS NOT NULL`, did, campaignID)
			if n, _ := res.RowsAffected(); n == 0 {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "nothing_to_return"})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true})
		default:
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "unknown_ground_action", "actions": []string{"claim", "return"}})
		}
		return
	}

	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	query := `SELECT d.id, d.name, d.quantity, COALESCE(c.name, ''), d.dropped_at, d.taken_by, COALESCE(d.taken_note, '')
		FROM dropped_items d LEFT JOIN characters c ON c.id = d.dropped_by
		WHERE d.lobby_id = $1 AND d.taken_by IS NULL`
	if !isGM || r.URL.Query().Get("include") != "claimed" {
		query += ` AND d.taken_note IS NULL`
	}
	rows, err := db.Query(query+` ORDER BY d.id`, campaignID)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "db_error"})
		return
	}
	defer rows.Close()
	items := []map[string]interface{}{}
	for rows.Next() {
		var id, qty int
		var name, by, note string
		var at time.Time
		var takenBy sql.NullInt64
		if rows.Scan(&id, &name, &qty, &by, &at, &takenBy, &note) != nil {
			continue
		}
		it := map[string]interface{}{"dropped_id": id, "name": name, "quantity": qty, "dropped_by": by, "dropped_at": at.Format(time.RFC3339)}
		if note != "" {
			it["claimed"] = note
		}
		items = append(items, it)
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"campaign_id": campaignID, "on_the_ground": items})
}
