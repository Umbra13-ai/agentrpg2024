package main

// Dropped items and the ground.
// A player can equip or unequip one weapon per turn in combat, and that is not
// an action. To switch weapons and still attack, they must drop the weapon in
// their hand (dropping is free and does not count as that one equip or
// unequip), then equip the new one. A dropped item is not gone: it lies on the
// ground of the campaign where anyone can pick it up, or it can be forgotten.
//	POST /api/characters/{id}/equipment/drop      {"item": "scimitar", "quantity": 1}
//	POST /api/characters/{id}/equipment/pick-up   {"dropped_id": 12}
//	POST /api/campaigns/{id}/ground/{id}/veto     GM: undo a pick-up that doesn't fit the story {"note": "you are too far away now"}
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
		if _, err := tx.Exec(`UPDATE dropped_items SET taken_by = $1, taken_at = NOW() WHERE id = $2`, charID, droppedID); err != nil {
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

// A pick-up is immediate (the game shouldn't wait); the GM can veto one that doesn't fit the story, such as a character nowhere near
// the item now: POST /api/campaigns/{id}/ground/{dropped_id}/veto puts it back where it was (S-CALLA-9). recentPickUps lists the
// pick-ups of the last day for the GM, with that request.
func recentPickUps(lobbyID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	rows, err := db.Query(`SELECT d.id, d.name, COALESCE(d.left_where, ''), c.id, c.name, d.taken_at FROM dropped_items d JOIN characters c ON c.id = d.taken_by
		WHERE d.lobby_id = $1 AND d.taken_note IS NULL AND d.taken_at > NOW() - INTERVAL '24 hours' ORDER BY d.taken_at`, lobbyID)
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
			out = append(out, map[string]interface{}{"dropped_id": id, "item": name, "was": where, "character_id": cid, "character": cname,
				"picked_up_at": at.Format(time.RFC3339), "veto": fmt.Sprintf(`POST /api/campaigns/%d/ground/%d/veto {"note": "you are too far away now"}`, lobbyID, id)})
		}
	}
	return out
}

// droppedByCharacter lists what a character dropped that is still on the ground.
func droppedByCharacter(charID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	// an item the GM claimed off the ground ("the cultists took it") stays on the list with the GM's note and no pick-up: the player
	// keeps track of where its gear went (playtest I-CALLA-20). A vetoed pick-up's old row has a taker and is left out.
	rows, err := db.Query(`SELECT id, name, quantity, dropped_at, COALESCE(left_where, ''), COALESCE(taken_note, '') FROM dropped_items
		WHERE dropped_by = $1 AND taken_by IS NULL ORDER BY id`, charID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, qty int
		var name, where, claimed string
		var at sql.NullTime
		if rows.Scan(&id, &name, &qty, &at, &where, &claimed) != nil {
			continue
		}
		if where == "" {
			where = "on the ground"
		}
		it := map[string]interface{}{"dropped_id": id, "name": name, "quantity": qty, "where": where}
		if at.Valid { // a row saved without the time still lists
			it["dropped_at"] = at.Time.Format(time.RFC3339)
		}
		if claimed != "" {
			it["gone"] = claimed
			it["note"] = "The GM says it is no longer there: it can't be picked up."
		} else {
			it["pick_up"] = fmt.Sprintf(`POST /api/characters/%d/equipment/pick-up {"dropped_id": %d}`, charID, id)
		}
		out = append(out, it)
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
		"note": "These items are no longer in your inventory. If you want to keep them, pick them up (each item's pick_up request) while your character is still where they were left; the GM can undo a pick-up that doesn't fit the story. An item marked gone was taken away in the story: the GM's words say where."}
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
	name, errCode := pickUpFromGround(charID, req.DroppedID)
	if errCode != "" {
		status := http.StatusBadRequest
		if errCode == "already_taken" {
			status = http.StatusConflict
		}
		if errCode == dbErrorCode {
			writeDBWriteError(w, nil, "the pick-up")
			return
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": errCode})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "picked_up": name,
		"note": "It is in your pack, not yet equipped or sorted into a bag. The GM can undo a pick-up that doesn't fit the story (your character nowhere near the item)."})
}

// handleCampaignGround lists and manages the items lying on the ground of a campaign.
// handleCampaignGround godoc
// @Summary The items lying on the ground of a campaign
// @Description GET lists what lies on the ground (dropped with /characters/{id}/equipment/drop or left by the GM's /gm/take-item; the GM adds ?include=claimed to see items taken away too, and recent_pick_ups lists the last day's pick-ups). Players in the campaign pick an item up with /characters/{id}/equipment/pick-up, at once; the GM can veto a pick-up that doesn't fit the story (the character nowhere near the item): POST /ground/{dropped_id}/veto {"note": "you are too far away now"} puts it back where it was. GM only too: POST /ground/{dropped_id}/claim takes an item off the ground with a note ({"note": "the troll took it"}): the log says so and its owner's left_behind keeps it, marked gone with the note and no pick-up; POST /ground/{dropped_id}/return puts it back.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Param include query string false "claimed (GM)"
// @Body Why it left the ground (claim)
// @Router /campaigns/{id}/ground [get]
// @Router /campaigns/{id}/ground/{dropped_id}/claim [post]
// @Router /campaigns/{id}/ground/{dropped_id}/return [post]
// @Router /campaigns/{id}/ground/{dropped_id}/veto [post]
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
			logGroundChange(campaignID, did, "is gone: "+note)
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
			logGroundChange(campaignID, did, "is back where it was left")
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true})
		case "veto": // the GM undoes a pick-up that doesn't fit the story: the item goes back where it was (S-CALLA-9)
			var req struct {
				Note string `json:"note"` // why (the player sees it in the log), such as "you are too far away now"
			}
			if !readRequest(w, r, &req, optionalBody()) {
				return
			}
			var taker sql.NullInt64
			var name, where string
			var qty int
			if err := db.QueryRow(`SELECT taken_by, name, quantity, COALESCE(left_where, '') FROM dropped_items WHERE id = $1 AND lobby_id = $2 AND taken_note IS NULL`, did, campaignID).
				Scan(&taker, &name, &qty, &where); err != nil {
				if answerDBFailure(w, err, "the item") {
					return
				}
				writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "nothing_with_that_id"})
				return
			}
			if !taker.Valid {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_picked_up", "message": "Nobody has picked this item up. Nothing was changed."})
				return
			}
			res, code := dropToGround(int(taker.Int64), name, qty, where) // back where it was (dropped_items.go)
			if code == dbErrorCode {
				writeDBWriteError(w, nil, "the veto")
				return
			}
			if code != "" {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": code, "message": "The item couldn't be taken back from the character (" + code + "); it may have been used or moved. Nothing was changed."})
				return
			}
			note := strings.TrimSpace(req.Note)
			if note == "" {
				note = "it doesn't fit the story"
			}
			db.Exec(`UPDATE dropped_items SET taken_note = $1 WHERE id = $2`, "vetoed by the GM: "+note, did)
			line := fmt.Sprintf("The GM undoes %s's pick-up of %s: %s. It is back where it was.", getCharacterName(int(taker.Int64)), name, note)
			writeAction(db, campaignID, 0, "gm_veto", line, line)
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "dropped_id": res.DroppedID, "note": "The item is back where it was; the player sees why in the log."})
		default:
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "unknown_ground_action", "actions": []string{"claim", "return", "veto"}})
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
		reply["recent_pick_ups"] = recentPickUps(campaignID) // what the GM can still veto (S-CALLA-9)
	}
	writeJSONStatus(w, http.StatusOK, reply)
}

// logGroundChange: the GM's claim or return of an item on the ground is written to the log, so its owner and the party read what
// happened to it (playtest I-CALLA-20: a claimed item vanished from the player's view with no word).
func logGroundChange(campaignID, droppedID int, what string) {
	var name, owner, where string
	if db.QueryRow(`SELECT d.name, COALESCE(c.name, ''), COALESCE(d.left_where, '') FROM dropped_items d LEFT JOIN characters c ON c.id = d.dropped_by
		WHERE d.id = $1`, droppedID).Scan(&name, &owner, &where) != nil {
		return
	}
	if where == "" {
		where = "on the ground"
	}
	line := fmt.Sprintf("%s (left %s", name, where)
	if owner != "" {
		line += " by " + owner
	}
	writeAction(db, campaignID, 0, "ground", line+") "+what, "")
}
