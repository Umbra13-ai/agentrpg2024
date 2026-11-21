package main

// Dropped items and the ground.
// A player can equip or unequip one weapon per turn in combat, and that is not
// an action. To switch weapons and still attack, they must drop the weapon in
// their hand (dropping is free and does not count as that one equip or
// unequip), then equip the new one. A dropped item is not gone: it lies on the
// ground of the campaign where anyone can pick it up, or it can be forgotten.
//	POST /api/characters/{id}/equipment/drop      {"item": "scimitar", "quantity": 1}
//	POST /api/characters/{id}/equipment/pick-up   {"dropped_id": 12}  an ask: the GM approves it
//	POST /api/campaigns/{id}/ground/{id}/approve  GM: the character that asked takes it
//	POST /api/campaigns/{id}/ground/{id}/refuse   GM: not now {"note": "you are too far away now"}
//	POST /api/gm/take-item                        GM: the narration leaves a character's item somewhere {"character_id": 3, "item": "blanket", "where": "in the bedroll as a decoy"}
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
	DroppedID  int
	Name       string
	Quantity   int
	Contents   int
	Attunement string // the attunement the drop ended (inventory_2024.go)
}

// dropToGround takes an item (or part of a stack) out of a character's
// inventory and puts it on the ground of their campaign. place is where the
// story left it ("in the bedroll as a decoy"; the GM's take-item, S-CALLA-9);
// "" is a plain drop. Either way it stays there until someone picks it up.
func dropToGround(charID int, itemRef string, quantity int, place string) (*dropResult, string) {
	place = strings.TrimSpace(place)
	var lobby sql.NullInt64
	var charName string
	var armor sql.NullString
	if err := db.QueryRow(`SELECT lobby_id, name, equipped_armor FROM characters WHERE id = $1`, charID).Scan(&lobby, &charName, &armor); err != nil {
		return nil, notFoundOrDBError(err, "character_not_found")
	}
	if !lobby.Valid {
		return nil, "not_in_a_campaign"
	}
	// the inventory, the ground and the log in one locked inventory change (inventory_2024.go): an attunement to the item ends
	var bundle []invItem
	var qty, dropID int
	done, err := changeInventory(charID, func(tx *sql.Tx, inv []invItem) ([]invItem, error) {
		it, ferr := findItem(inv, itemRef)
		if ferr != nil {
			return nil, codeRefusal(ferr.Error())
		}
		if isLost(it, indexByID(inv)) {
			return nil, codeRefusal("item_is_lost")
		}
		if armor.Valid && armor.String != "" && itemKey(itemStr(it, "name")) == itemKey(armor.String) {
			return nil, codeRefusal("take_off_your_armor_first")
		}
		rootID := itemStr(it, "id")
		qty = itemQty(it)
		switch {
		case !isContainer(it) && quantity > 0 && quantity < qty:
			piece := copyItem(it)
			piece["quantity"] = float64(quantity)
			piece["location"] = ""
			bundle = []invItem{piece}
			it["quantity"] = float64(qty - quantity)
			qty = quantity
		case quantity > qty:
			return nil, codeRefusal("not_enough_in_stack")
		default:
			bundle = collectBundle(inv, rootID)
			root := copyItem(bundle[0])
			root["location"] = ""
			bundle[0] = root
			inv = removeBundle(inv, rootID)
		}
		b, _ := json.Marshal(bundle)
		if err := tx.QueryRow(`INSERT INTO dropped_items (lobby_id, name, quantity, bundle, dropped_by, left_where) VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')) RETURNING id`,
			lobby.Int64, itemStr(bundle[0], "name"), qty, string(b), charID, place).Scan(&dropID); err != nil {
			return nil, err
		}
		what, result := fmt.Sprintf("%s drops %s", charName, itemStr(bundle[0], "name")), "It lies on the ground."
		if place != "" {
			what, result = fmt.Sprintf("%s leaves %s %s", charName, itemStr(bundle[0], "name"), place), "It is no longer carried: it stays there until someone picks it up."
		}
		if err := writeAction(tx, int(lobby.Int64), charID, "drop", what, result); err != nil {
			return nil, err
		}
		return inv, nil
	})
	if err != nil {
		return nil, refusalCode(err, "character_not_found")
	}

	// a weapon that was in hand and is no longer carried leaves the hand
	clearHandsIfGone(charID)
	syncHeldItems(charID) // the dropped item's own "held" mark, also when the hands didn't change
	return &dropResult{DroppedID: dropID, Name: itemStr(bundle[0], "name"), Quantity: qty, Contents: len(bundle) - 1, Attunement: strings.TrimSpace(done.Note)}, ""
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
		changeHands(charID, `UPDATE characters SET equipped_main_hand = NULL WHERE id = $1`, charID)
	}
	if offHand.Valid && offHand.String != "" && !carried(offHand.String) {
		changeHands(charID, `UPDATE characters SET equipped_off_hand = NULL WHERE id = $1`, charID)
	}
}

// pickUpFromGround gives a dropped item to a character in the same campaign.
func pickUpFromGround(charID, droppedID int) (string, string) {
	var lobby sql.NullInt64
	var charName string
	if err := db.QueryRow(`SELECT lobby_id, name FROM characters WHERE id = $1`, charID).Scan(&lobby, &charName); err != nil {
		return "", notFoundOrDBError(err, "character_not_found")
	}
	// the character's inventory (locked first, as every inventory change: inventory_2024.go) and the item on the ground (locked)
	// in one transaction: one character takes it
	var name string
	_, err := changeInventory(charID, func(tx *sql.Tx, inv []invItem) ([]invItem, error) {
		var dropLobby int
		var bundleRaw string
		var takenBy sql.NullInt64
		var takenNote sql.NullString
		if err := tx.QueryRow(`SELECT lobby_id, name, bundle, taken_by, taken_note FROM dropped_items WHERE id = $1 FOR UPDATE`, droppedID).
			Scan(&dropLobby, &name, &bundleRaw, &takenBy, &takenNote); err != nil {
			if !dbFailed(err) {
				return nil, codeRefusal("nothing_on_the_ground_with_that_id")
			}
			return nil, err
		}
		if !lobby.Valid || int(lobby.Int64) != dropLobby {
			return nil, codeRefusal("not_in_the_same_campaign")
		}
		if takenBy.Valid || takenNote.Valid {
			return nil, codeRefusal("already_taken")
		}
		var bundle []invItem
		if json.Unmarshal([]byte(bundleRaw), &bundle) != nil || len(bundle) == 0 {
			return nil, codeRefusal("corrupt_item")
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
		if _, err := tx.Exec(`UPDATE dropped_items SET taken_by = $1, taken_at = NOW(), pickup_by = NULL, pickup_refused = NULL WHERE id = $2`, charID, droppedID); err != nil {
			return nil, err
		}
		if err := writeAction(tx, dropLobby, charID, "pick_up", fmt.Sprintf("%s picks up %s", charName, name), "It is now in their pack."); err != nil {
			return nil, err
		}
		return inv, nil
	})
	if err != nil {
		return "", refusalCode(err, "character_not_found")
	}
	return name, ""
}

// Picking an item up from the ground is the GM's call (S-CALLA-9): the character may be nowhere near where it was left. A player
// asks (askToPickUp, the pick-up request); the GM approves (pickUpFromGround for the character that asked) or refuses with a note
// (POST /api/campaigns/{id}/ground/{dropped_id}/approve | refuse). One ask per item at a time.

// askToPickUp records the character's ask to pick the item up and answers its name, or a refusal code.
func askToPickUp(charID, droppedID int) (string, string) {
	var name string
	err := db.QueryRow(`UPDATE dropped_items d SET pickup_by = $1, pickup_asked_at = NOW(), pickup_refused = NULL
		FROM characters c
		WHERE d.id = $2 AND c.id = $1 AND c.lobby_id = d.lobby_id AND d.taken_by IS NULL AND d.taken_note IS NULL
		  AND (d.pickup_by IS NULL OR d.pickup_by = $1)
		RETURNING d.name`, charID, droppedID).Scan(&name)
	if err == nil {
		return name, ""
	}
	if dbFailed(err) {
		return "", dbErrorCode
	}
	// why not: the item is gone, another character asked first, or it lies in another campaign
	var lobby, charLobby sql.NullInt64
	var takenBy, pickupBy sql.NullInt64
	var takenNote sql.NullString
	if qerr := db.QueryRow(`SELECT lobby_id, taken_by, taken_note, pickup_by FROM dropped_items WHERE id = $1`, droppedID).Scan(&lobby, &takenBy, &takenNote, &pickupBy); qerr != nil {
		return "", notFoundOrDBError(qerr, "nothing_on_the_ground_with_that_id")
	}
	db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, charID).Scan(&charLobby)
	switch {
	case !charLobby.Valid || charLobby.Int64 != lobby.Int64:
		return "", "not_in_the_same_campaign"
	case takenBy.Valid || takenNote.Valid:
		return "", "already_taken"
	}
	return "", "another_character_asked_first"
}

// pickUpAsks: the open asks of a campaign, for the GM (GM status and the ground list).
func pickUpAsks(lobbyID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	rows, err := db.Query(`SELECT d.id, d.name, COALESCE(d.left_where, ''), c.id, c.name, d.pickup_asked_at FROM dropped_items d JOIN characters c ON c.id = d.pickup_by
		WHERE d.lobby_id = $1 AND d.taken_by IS NULL AND d.taken_note IS NULL ORDER BY d.pickup_asked_at`, lobbyID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, cid int
		var name, where, cname string
		var at time.Time
		if rows.Scan(&id, &name, &where, &cid, &cname, &at) == nil {
			if where == "" {
				where = "on the ground"
			}
			out = append(out, map[string]interface{}{"dropped_id": id, "item": name, "where": where, "character_id": cid, "character": cname,
				"asked_at": at.Format(time.RFC3339),
				"approve":  fmt.Sprintf("POST /api/campaigns/%d/ground/%d/approve", lobbyID, id),
				"refuse":   fmt.Sprintf(`POST /api/campaigns/%d/ground/%d/refuse {"note": "you are too far away now"}`, lobbyID, id)})
		}
	}
	return out
}

// droppedByCharacter lists what a character dropped that is still on the ground.
func droppedByCharacter(charID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	rows, err := db.Query(`SELECT id, name, quantity, dropped_at, COALESCE(left_where, ''),
			CASE WHEN pickup_by = $1 THEN 'asked: waiting for the GM' WHEN pickup_by IS NOT NULL THEN 'another character asked for it'
			     WHEN pickup_refused IS NOT NULL THEN 'the GM refused: ' || pickup_refused ELSE 'not asked' END
		FROM dropped_items WHERE dropped_by = $1 AND taken_by IS NULL AND taken_note IS NULL ORDER BY id`, charID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, qty int
		var name, where, status string
		var at time.Time
		if rows.Scan(&id, &name, &qty, &at, &where, &status) == nil {
			if where == "" {
				where = "on the ground"
			}
			out = append(out, map[string]interface{}{"dropped_id": id, "name": name, "quantity": qty, "dropped_at": at.Format(time.RFC3339),
				"where": where, "pick_up_status": status, "ask_to_pick_up": fmt.Sprintf(`POST /api/characters/%d/equipment/pick-up {"dropped_id": %d}`, charID, id)})
		}
	}
	return out
}

// leftBehindView: what my-turn tells a player about items it left behind (dropped, or left where the GM's narration put them):
// always present, with an empty list when there are none.
func leftBehindView(charID int) map[string]interface{} {
	if lb := leftBehindNote(charID); lb != nil {
		return lb
	}
	return map[string]interface{}{"items": []map[string]interface{}{}, "note": "You have left nothing behind."}
}

// leftBehindNote: items the character left behind (dropped, or left where the GM's narration put them): they are no longer
// carried, and how to ask for them back; nil when there are none (the after-battle reminder says nothing then).
func leftBehindNote(charID int) map[string]interface{} {
	items := droppedByCharacter(charID)
	if len(items) == 0 {
		return nil
	}
	return map[string]interface{}{"items": items,
		"note": "These items are no longer in your inventory. If you want to keep them, ask to pick them up (each item's ask_to_pick_up request) while your character is still where they were left: the GM approves it, and it is back in your pack."}
}

func handleCharacterDrop(w http.ResponseWriter, r *http.Request, charID int) {
	var req struct {
		Item     string `json:"item"`     // the item to drop (its id or name)
		Quantity int    `json:"quantity"` // how many of a stack to drop (default all)
	}
	if !readRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Item) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_required"})
		return
	}
	res, errCode := dropToGround(charID, req.Item, req.Quantity, "")
	if errCode != "" {
		status := http.StatusBadRequest
		if strings.HasPrefix(errCode, "ambiguous_item") {
			status = http.StatusConflict
		}
		if errCode == dbErrorCode {
			writeDBWriteError(w, nil, "the drop")
			return
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": errCode})
		return
	}
	reply := map[string]interface{}{
		"success": true, "dropped_id": res.DroppedID, "name": res.Name, "quantity": res.Quantity, "items_inside": res.Contents,
		"note": "It lies on the ground. Anyone in the campaign can pick it up (POST /api/characters/{id}/equipment/pick-up), and it stays there until someone does. Dropping is free.",
	}
	if res.Attunement != "" {
		reply["attunement_ended"] = res.Attunement
	}
	writeJSONStatus(w, http.StatusOK, reply)
}

func handleCharacterPickUp(w http.ResponseWriter, r *http.Request, charID int) {
	var req struct {
		DroppedID int `json:"dropped_id"` // the dropped item to pick up (its id from GET /api/campaigns/{id}/ground)
	}
	if !readRequest(w, r, &req) {
		return
	}
	if req.DroppedID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "dropped_id_required"})
		return
	}
	name, errCode := askToPickUp(charID, req.DroppedID) // the GM approves it (S-CALLA-9)
	if errCode != "" {
		status := http.StatusBadRequest
		if errCode == "already_taken" || errCode == "another_character_asked_first" {
			status = http.StatusConflict
		}
		if errCode == dbErrorCode {
			writeDBWriteError(w, nil, "the ask to pick it up")
			return
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": errCode})
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{"success": true, "asked_to_pick_up": name, "status": "waiting for the GM",
		"note": "The GM approves a pick-up when your character is where the item is: once approved it is in your pack (GET /api/my-turn's left_behind shows the answer). Nothing is in your pack yet."})
}

// handleCampaignGround lists and manages the items lying on the ground of a campaign.
// handleCampaignGround godoc
// @Summary The items lying on the ground of a campaign
// @Description GET lists what lies on the ground (dropped with /characters/{id}/equipment/drop or left by the GM's /gm/take-item; the GM adds ?include=claimed to see items taken away too, and pick_up_asks lists the open asks). A player asks to pick an item up with /characters/{id}/equipment/pick-up; the GM decides, since the character may be nowhere near it now: POST /ground/{dropped_id}/approve gives it to the character that asked, POST /ground/{dropped_id}/refuse {"note": "you are too far away now"} says no. GM only too: POST /ground/{dropped_id}/claim takes an item off the ground with a note ({"note": "the troll took it"}), POST /ground/{dropped_id}/return puts it back.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Param include query string false "claimed (GM)"
// @Body Why it left the ground (claim)
// @Router /campaigns/{id}/ground [get]
// @Router /campaigns/{id}/ground/{dropped_id}/claim [post]
// @Router /campaigns/{id}/ground/{dropped_id}/return [post]
// @Router /campaigns/{id}/ground/{dropped_id}/approve [post]
// @Router /campaigns/{id}/ground/{dropped_id}/refuse [post]
func handleCampaignGround(w http.ResponseWriter, r *http.Request, campaignID int, rest []string) {
	_, role, ok := requireCampaignMember(w, r, campaignID, "see what lies on its ground")
	if !ok {
		return
	}
	isGM := role == "gm"

	// POST /ground/{id}/claim or /return (GM only)
	if len(rest) >= 2 {
		if _, ok := requireCampaignGM(w, r, campaignID, "take items off the ground or put them back"); !ok {
			return
		}
		if r.Method != http.MethodPost {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
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
				Note string `json:"note"` // why the GM claims or returns it (shown in the log)
			}
			if !readRequest(w, r, &req, optionalBody()) {
				return
			}
			note := strings.TrimSpace(req.Note)
			if note == "" {
				note = "taken by someone"
			}
			res, xerr := db.Exec(`UPDATE dropped_items SET taken_note = $1, taken_at = NOW() WHERE id = $2 AND lobby_id = $3 AND taken_by IS NULL AND taken_note IS NULL`, note, did, campaignID)
			if xerr != nil {
				writeDBWriteError(w, xerr, "the item's new place")
				return
			}
			if n, _ := res.RowsAffected(); n == 0 {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_on_the_ground"})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "note": "It is off the ground but kept on record. Use /return to put it back."})
		case "return":
			if !noFields(w, r) {
				return
			}
			res, xerr := db.Exec(`UPDATE dropped_items SET taken_note = NULL, taken_at = NULL WHERE id = $1 AND lobby_id = $2 AND taken_by IS NULL AND taken_note IS NOT NULL`, did, campaignID)
			if xerr != nil {
				writeDBWriteError(w, xerr, "the item's return")
				return
			}
			if n, _ := res.RowsAffected(); n == 0 {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "nothing_to_return"})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true})
		case "approve": // the character that asked takes it (S-CALLA-9)
			if !noFields(w, r) {
				return
			}
			var asker sql.NullInt64
			if err := db.QueryRow(`SELECT pickup_by FROM dropped_items WHERE id = $1 AND lobby_id = $2`, did, campaignID).Scan(&asker); err != nil {
				if answerDBFailure(w, err, "the item") {
					return
				}
				writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "nothing_on_the_ground_with_that_id"})
				return
			}
			if !asker.Valid {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_pick_up_asked", "message": "No character has asked to pick this item up. Nothing was changed."})
				return
			}
			name, code := pickUpFromGround(int(asker.Int64), did)
			if code == dbErrorCode {
				writeDBWriteError(w, nil, "the pick-up")
				return
			}
			if code != "" {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": code})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "picked_up": name, "character_id": asker.Int64,
				"note": "It is in the character's pack, not yet equipped or sorted into a bag."})
		case "refuse":
			var req struct {
				Note string `json:"note"` // why not (the player sees it), such as "you are too far away now"
			}
			if !readRequest(w, r, &req, optionalBody()) {
				return
			}
			note := strings.TrimSpace(req.Note)
			if note == "" {
				note = "not now"
			}
			res, xerr := db.Exec(`UPDATE dropped_items SET pickup_by = NULL, pickup_refused = $1 WHERE id = $2 AND lobby_id = $3 AND pickup_by IS NOT NULL`, note, did, campaignID)
			if xerr != nil {
				writeDBWriteError(w, xerr, "the refusal")
				return
			}
			if n, _ := res.RowsAffected(); n == 0 {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_pick_up_asked"})
				return
			}
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "note": "Refused: the item stays where it is and the player sees why."})
		default:
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "unknown_ground_action", "actions": []string{"claim", "return", "approve", "refuse"}})
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
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": dbErrorCode})
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
	reply := map[string]interface{}{"campaign_id": campaignID, "on_the_ground": items}
	if isGM {
		reply["pick_up_asks"] = pickUpAsks(campaignID) // the asks waiting for the GM (S-CALLA-9)
	}
	writeJSONStatus(w, http.StatusOK, reply)
}
