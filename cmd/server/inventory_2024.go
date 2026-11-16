package main

// The one owner of a character's inventory and attunement (audit AUD-24, findings B-4 and E-4). Every change to
// characters.inventory goes through changeInventory: the row is locked (SELECT ... FOR UPDATE), the change works on the
// inventory as it is now (and may read and write other columns through the same transaction: the money a purchase pays, the
// ground an item is dropped on), and the result is written in the same transaction. Before Nov 15 2026 about 20 places read the
// inventory, changed it in Go and wrote it back whole without a lock: eight items given at once by the GM came out as five
// (B-4), and ammunition, burning, gear reminders and the equip route could undo each other.
//
// Attunement belongs to the items carried (SRD 5.2.1: attunement ends when the item is no longer in your possession): every
// inventory change ends the attunement to an item that left the inventory (dropped, taken away by the GM, used up, destroyed), in the
// same transaction, so no path can forget it; what the attunement gave (a score an item sets, Might of Giants, the stored Armor
// Class) is undone right after. Attuning and ending attunement (attuneItem, unattuneItem) lock the same row. Before, attunement
// was a list of names nobody checked against the inventory: a dropped Ring of Protection kept its +1 AC (E-4).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// logDBProblem logs a database failure of a change whose caller has no reply to give.
func logDBProblem(what string, err error) {
	if dbFailed(err) {
		log.Printf("%s: %v", what, err)
	}
}

// ---------------------------------------------------------------- storage (only through changeInventory)

func loadInventoryTx(tx *sql.Tx, charID int) ([]invItem, error) {
	var raw []byte
	if err := tx.QueryRow(`SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1 FOR UPDATE`, charID).Scan(&raw); err != nil {
		return nil, err
	}
	var inv []invItem
	if err := json.Unmarshal(raw, &inv); err != nil {
		inv = []invItem{}
	}
	if inv == nil {
		inv = []invItem{}
	}
	ensureItemIDs(inv)
	fillItemWeights(inv)
	return inv, nil
}

func saveInventoryTx(tx *sql.Tx, charID int, inv []invItem) error {
	b, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, string(b), charID)
	return err
}

// invRefusal: a change refused with the reply's status and body; nothing is written.
type invRefusal struct {
	status int
	body   map[string]interface{}
}

func (e *invRefusal) Error() string { return fmt.Sprint(e.body["error"]) }

func refuseInv(status int, body map[string]interface{}) error { return &invRefusal{status, body} }

// answerInvRefusal writes a refusal (true) or nothing (false: err is not one).
func answerInvRefusal(w http.ResponseWriter, err error) bool {
	var r *invRefusal
	if !errors.As(err, &r) {
		return false
	}
	writeJSONStatus(w, r.status, r.body)
	return true
}

// codeRefusal: a change refused with an error code (a caller that answers with the code alone).
type codeRefusal string

func (c codeRefusal) Error() string { return string(c) }

// refusalCode is the code a failed change answers: its refusal's, notFound when the row is missing, else dbErrorCode.
func refusalCode(err error, notFound string) string {
	var c codeRefusal
	if errors.As(err, &c) {
		return string(c)
	}
	if !dbFailed(err) {
		return notFound
	}
	return dbErrorCode
}

// invChange is what a change did besides the inventory: the attunements it ended and what ending them undid.
type invChange struct {
	Ended []string
	Note  string
}

// changeInventory is the one way to change a character's inventory. change gets the locked inventory and the transaction (for
// the other columns of the same change; never db) and returns the inventory to save, nil to save nothing, or an error (an
// invRefusal for a reply) to change nothing. Attunement to an item no longer carried ends in the same transaction.
func changeInventory(charID int, change func(tx *sql.Tx, inv []invItem) ([]invItem, error)) (invChange, error) {
	var done invChange
	tx, err := db.Begin()
	if err != nil {
		return done, err
	}
	defer tx.Rollback()
	if done.Ended, err = changeInventoryIn(tx, charID, change); err != nil {
		return invChange{}, err
	}
	if err = tx.Commit(); err != nil {
		return invChange{}, err
	}
	done.Note = attunementEnded(charID, done.Ended)
	return done, nil
}

// changeInventoryIn is changeInventory inside the caller's transaction (character creation writes the new row and its first
// inventory in one): the row locked, the change, the save and the attunements it ended. The caller commits, then tells the
// player what attunementEnded says.
func changeInventoryIn(tx *sql.Tx, charID int, change func(tx *sql.Tx, inv []invItem) ([]invItem, error)) ([]string, error) {
	inv, err := loadInventoryTx(tx, charID)
	if err != nil {
		return nil, err
	}
	out, err := change(tx, inv)
	if err != nil || out == nil {
		return nil, err
	}
	// the "held" marks follow the hands on every save, whatever path wrote the inventory (creation's starting gear included:
	// playtest I-ROWAN-7, a spellcaster with a Shield whose Mace was left "held" with the hand empty)
	var main, off sql.NullString
	if err := tx.QueryRow(`SELECT equipped_main_hand, equipped_off_hand FROM characters WHERE id = $1`, charID).Scan(&main, &off); err != nil {
		return nil, err
	}
	syncHands(out, main.String, off.String)
	if err = saveInventoryTx(tx, charID, out); err != nil {
		return nil, err
	}
	return endAttunementNotCarried(tx, charID, out)
}

// editInventory changes a character's inventory (changeInventory) for a caller with nothing to refuse.
func editInventory(charID int, fn func([]invItem) []invItem) invChange {
	done, err := changeInventory(charID, func(_ *sql.Tx, inv []invItem) ([]invItem, error) { return fn(inv), nil })
	if err != nil {
		logDBProblem("changing the inventory", err)
	}
	return done
}

// updateInventoryItem changes one inventory item (found by id, or by name when it has none).
func updateInventoryItem(charID int, it invItem, fn func(x invItem)) {
	id, name := itemStr(it, "id"), itemStr(it, "name")
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if (id != "" && itemStr(x, "id") == id) || (id == "" && itemStr(x, "name") == name) {
				fn(x)
				break
			}
		}
		return inv
	})
}

// mutateInventory runs fn on the locked inventory and answers the request: fn's error code (with its status and reply), or
// success with fn's reply (and attunement_ended when the change ended an attunement).
func mutateInventory(w http.ResponseWriter, charID int, fn func(inv []invItem) ([]invItem, map[string]interface{}, int, string)) {
	var resp map[string]interface{}
	done, err := changeInventory(charID, func(_ *sql.Tx, inv []invItem) ([]invItem, error) {
		out, r, status, errCode := fn(inv)
		resp = r
		if errCode != "" {
			resp["error"] = errCode
			return nil, refuseInv(status, resp)
		}
		return out, nil
	})
	if answerInvRefusal(w, err) {
		return
	}
	if err != nil {
		if answerDBFailure(w, err, "the character's pack") {
			return
		}
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	resp["success"] = true
	if len(done.Ended) > 0 {
		resp["attunement_ended"] = strings.TrimSpace(done.Note)
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------- attunement

func attunedListTx(tx *sql.Tx, charID int) ([]string, error) {
	var raw []byte
	if err := tx.QueryRow(`SELECT COALESCE(attuned_items,'[]') FROM characters WHERE id = $1 FOR UPDATE`, charID).Scan(&raw); err != nil {
		return nil, err
	}
	var list []string
	json.Unmarshal(raw, &list)
	return list, nil
}

func writeAttunedTx(tx *sql.Tx, charID int, list []string) error {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	_, err := tx.Exec(`UPDATE characters SET attuned_items = $1 WHERE id = $2`, string(b), charID)
	return err
}

// attunedNameMatches: an attuned name is this item (its name, or its name without a parenthetical: "Horn of Valhalla").
func attunedNameMatches(attuned string, it invItem) bool {
	n := itemStr(it, "name")
	if normalizeRef(n) == normalizeRef(attuned) {
		return true
	}
	if i := strings.Index(n, " ("); i > 0 && normalizeRef(n[:i]) == normalizeRef(attuned) {
		return true
	}
	return false
}

// attunedCarried keeps the attuned names that are items still in the inventory (one attunement per item; a destroyed item is
// gone; an item in a lost bag is still the character's) and returns the others.
func attunedCarried(list []string, inv []invItem) (kept, ended []string) {
	taken := map[int]bool{}
	for _, name := range list {
		found := false
		for i, it := range inv {
			if taken[i] || itemBool(it, "destroyed") || !attunedNameMatches(name, it) {
				continue
			}
			taken[i], found = true, true
			break
		}
		if found {
			kept = append(kept, name)
		} else {
			ended = append(ended, name)
		}
	}
	return kept, ended
}

// endAttunementNotCarried ends, in tx, the attunement to every item inv no longer holds.
func endAttunementNotCarried(tx *sql.Tx, charID int, inv []invItem) ([]string, error) {
	list, err := attunedListTx(tx, charID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	kept, ended := attunedCarried(list, inv)
	if len(ended) == 0 {
		return nil, nil
	}
	return ended, writeAttunedTx(tx, charID, kept)
}

// attunementEnded undoes what the ended attunements gave (after the transaction: these write other values through their own
// owners) and says what changed.
func attunementEnded(charID int, ended []string) string {
	if len(ended) == 0 {
		return ""
	}
	note := " Attunement ended (no longer carried): " + strings.Join(ended, ", ") + "."
	for _, name := range ended {
		note += applyAttunementScore(charID, name, false)
	}
	refreshMightOfGiants(charID)
	refreshStoredAC(charID)
	return note
}

// syncAttunement ends the attunement to items the character no longer carries: after an editor wrote the inventory or the
// attunement list directly (the admin page, the GM's set).
func syncAttunement(charID int) string {
	done, err := changeInventory(charID, func(_ *sql.Tx, inv []invItem) ([]invItem, error) { return inv, nil })
	if err != nil {
		logDBProblem("checking attunement", err)
	}
	return done.Note
}

// attuneItem attunes the character to an item it carries (ref: its name or id) in one locked step: the item must be carried, not
// already attuned, and a slot free (max). (Whether the item can be attuned at all is the caller's check, made before: it reads
// other tables.) It returns the item's name and the attunement list after.
func attuneItem(charID int, ref string, max int) (string, []string, error) {
	var name string
	var after []string
	_, err := changeInventory(charID, func(tx *sql.Tx, inv []invItem) ([]invItem, error) {
		it, ferr := findInventoryItem(inv, ref)
		if ferr != nil || it == nil {
			return nil, refuseInv(http.StatusBadRequest, map[string]interface{}{"error": "item_not_carried"})
		}
		if name = itemStr(it, "name"); name == "" {
			name = ref
		}
		list, err := attunedListTx(tx, charID)
		if err != nil {
			return nil, err
		}
		kept, _ := attunedCarried(list, inv) // an attunement to an item no longer carried doesn't hold a slot
		for _, a := range kept {
			if strings.EqualFold(a, name) && countCarried(inv, name) <= countNamed(kept, name) {
				return nil, refuseInv(http.StatusConflict, map[string]interface{}{"error": "already_attuned"})
			}
		}
		if len(kept) >= max {
			after = kept
			return nil, refuseInv(http.StatusConflict, map[string]interface{}{"error": "max_attunement_reached"})
		}
		after = append(kept, name)
		return nil, writeAttunedTx(tx, charID, after)
	})
	return name, after, err
}

// unattuneItem ends the attunement to the named item in one locked step; false when it isn't attuned.
func unattuneItem(charID int, ref string) (string, []string, bool, error) {
	found := ""
	var after []string
	_, err := changeInventory(charID, func(tx *sql.Tx, _ []invItem) ([]invItem, error) {
		list, err := attunedListTx(tx, charID)
		if err != nil {
			return nil, err
		}
		after = []string{}
		for _, a := range list {
			if found == "" && strings.EqualFold(a, ref) {
				found = a
				continue
			}
			after = append(after, a)
		}
		if found == "" {
			after = list
			return nil, nil
		}
		return nil, writeAttunedTx(tx, charID, after)
	})
	return found, after, found != "", err
}

func countCarried(inv []invItem, name string) int {
	n := 0
	for _, it := range inv {
		if !itemBool(it, "destroyed") && attunedNameMatches(name, it) {
			n++
		}
	}
	return n
}

func countNamed(list []string, name string) int {
	n := 0
	for _, a := range list {
		if strings.EqualFold(a, name) {
			n++
		}
	}
	return n
}
