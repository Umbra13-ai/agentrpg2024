package main

// The one owner of a fight's row in combat_state (audit AUD-09, AUD-39 and the C-7 part of AUD-24): the turn order (every
// combatant's entry, a monster's Hit Points with it), whose turn it is, the round, and the ids given to monsters. Every change
// to them reads the row locked (SELECT ... FOR UPDATE), changes the entries as maps (so a field this code doesn't know survives)
// and writes them back in the same transaction: changeFight. A turn change itself is turn_advance_2024.go (advanceTurn), and a
// monster's Hit Points are monster_hp_2024.go; both write through changeFight.
//
// Before Nov 15 2026 the turn order had 14 writers, 9 of them read-modify-write without a lock (two changes at once lost one),
// and the GM's area spell rewrote the whole order through a five-field struct: every initiative, AC and is_monster flag was
// lost and players could no longer target any monster. Monster ids were "one below the lowest id in the order", so a monster
// added after one was removed took its id and with it the old monster's conditions and effects (AUD-39); now each fight keeps
// a counter (last_monster_id) and an id is never given twice in one fight.
//
// Inside changeFight only the transaction is used (CLAUDE.md: never db inside a transaction): what a change needs from other
// tables (a stat block, who is dead) is read before it.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// errNotInFight: the combatant a change names isn't in the turn order. errNoChange: a change found nothing to do (nothing is written).
var (
	errNotInFight = errors.New("combatant_not_found")
	errNoChange   = errors.New("no_change")
	errBadOrder   = errors.New("the stored turn order isn't a list of entries") // kept for repair: no change is made on top of it
)

// fight is a fight's row as changeFight hands it to a change.
type fight struct {
	Lobby         int
	Round, Index  int
	Order         []map[string]interface{}
	StartedAt     time.Time // when the current turn started
	LastMonsterID int       // the last id given to a monster in this fight (0 or negative)
	claim         string    // the turn change under way (turn_advance_2024.go), "" when none
	claimHeld     bool      // that claim is recent enough to stand
	restartClock  bool      // a new turn starts: turn_started_at is set to now (moveTurn)
}

// fightColumns: the row as scanFight reads it. The last column says whether a turn change is under way: one that started more
// than two minutes ago is taken to have died (a crash) and no longer blocks the next.
const fightColumns = `COALESCE(round_number,1), COALESCE(current_turn_index,0), COALESCE(turn_order,'[]'), turn_started_at,
	COALESCE(last_monster_id,0), COALESCE(turn_claim,''),
	(COALESCE(turn_claim,'') <> '' AND COALESCE(turn_claimed_at > NOW() - interval '2 minutes', false))`

func scanFight(row interface{ Scan(...interface{}) error }, lobby int) (*fight, error) {
	f := &fight{Lobby: lobby}
	var raw []byte
	var started sql.NullTime
	if err := row.Scan(&f.Round, &f.Index, &raw, &started, &f.LastMonsterID, &f.claim, &f.claimHeld); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &f.Order); err != nil {
		return nil, fmt.Errorf("campaign %d: %w (%v)", lobby, errBadOrder, err)
	}
	if started.Valid {
		f.StartedAt = started.Time
	}
	return f, nil
}

// lockFight reads the active fight of a campaign in tx with its row locked until the transaction ends (sql.ErrNoRows: no
// active fight).
func lockFight(tx *sql.Tx, lobby int) (*fight, error) {
	return scanFight(tx.QueryRow(`SELECT `+fightColumns+` FROM combat_state WHERE lobby_id = $1 AND active = true FOR UPDATE`, lobby), lobby)
}

// readFight reads it without a lock (for a check made before a change; the change itself reads it again locked).
func readFight(q rowQuerier, lobby int) (*fight, error) {
	return scanFight(q.QueryRow(`SELECT `+fightColumns+` FROM combat_state WHERE lobby_id = $1 AND active = true`, lobby), lobby)
}

func writeFight(tx *sql.Tx, f *fight) error {
	order, err := json.Marshal(f.Order)
	if err != nil {
		return err
	}
	if f.Order == nil {
		order = []byte("[]")
	}
	clock := ""
	if f.restartClock {
		clock = ", turn_started_at = NOW()"
	}
	_, err = tx.Exec(`UPDATE combat_state SET turn_order = $1, current_turn_index = $2, round_number = $3, last_monster_id = $4`+clock+`
		WHERE lobby_id = $5`, order, f.Index, f.Round, f.LastMonsterID, f.Lobby)
	return err
}

// changeFight is the one transaction every change of the turn order runs in: the row locked, change edits f (its entries are
// maps: fields it doesn't touch are written back as they were), and f is written back. An error from change writes nothing.
func changeFight(lobby int, change func(f *fight) error) (*fight, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	f, err := lockFight(tx, lobby)
	if err != nil {
		return nil, err
	}
	if err := change(f); err != nil {
		return f, err
	}
	if err := writeFight(tx, f); err != nil {
		return f, err
	}
	return f, tx.Commit()
}

// answerFightProblem answers a request whose read or change of the fight failed: no active fight (409 no_active_combat), a stored
// order that is broken (409 invalid_combat_turn), or the database (500). It says whether it answered.
func answerFightProblem(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, sql.ErrNoRows):
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_active_combat", "hint": "Start combat first with POST /api/campaigns/{id}/combat/start"})
	case errors.Is(err, errBadOrder):
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "invalid_combat_turn", "message": "The stored turn order is broken; nothing was changed."})
	default:
		writeDBError(w, err, "the campaign's fight")
	}
	return true
}

// ---------------------------------------------------------------- the fight starts and ends

// startFight writes a new fight: round 1, the first entry's turn, no monster id given yet, no turn change under way.
func startFight(lobby int, order interface{}) error {
	raw, err := json.Marshal(order)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, active, turn_started_at, last_monster_id, turn_claim)
		VALUES ($1, 1, 0, $2, true, NOW(), 0, '')
		ON CONFLICT (lobby_id) DO UPDATE SET
			round_number = 1, current_turn_index = 0, turn_order = $2, active = true, turn_started_at = NOW(), battle_map = NULL,
			last_monster_id = 0, turn_claim = ''`, lobby, raw)
	if err == nil {
		scheduleFightStarts(lobby) // the turn schedule's combat round 1 opens (check_in_2024.go)
	}
	return err
}

// clearCombatInitiative deactivates combat and clears all initiative metadata.
// A finished encounter must not continue to present a stale current turn or
// turn order to clients; that stale data can otherwise drive timeout loops.
func clearCombatInitiative(campaignID int) error {
	clearMonsterConditions(campaignID)
	clearPendingTurns(campaignID)   // the fight is over: so are its pending turns
	clearBurning(campaignID, false) // F69: the fight is over, so are its fires
	endUntilNextTurnIn(campaignID)  // Dodge, Reckless Attack and readied actions end with the fight (dodge_2024.go)
	_, err := db.Exec(`
		UPDATE combat_state
		SET active = false,
			battle_map = NULL,
			round_number = 0,
			current_turn_index = 0,
			turn_order = '[]',
			turn_started_at = CURRENT_TIMESTAMP,
			last_monster_id = 0,
			turn_claim = ''
		WHERE lobby_id = $1
	`, campaignID)
	if err == nil {
		scheduleFightEnds(campaignID) // the turn schedule's next round is a noncombat one (check_in_2024.go)
	}
	return err
}

// ---------------------------------------------------------------- entries

// slotKey identifies an entry: its id and whether it is a Thief's Reflexes second turn (the one id that has two entries).
type slotKey struct {
	id    int
	extra bool
}

func keyOf(e map[string]interface{}) slotKey {
	extra, _ := e["is_thiefs_reflexes_turn"].(bool)
	return slotKey{asInt(e["id"]), extra}
}

// entry is a combatant's own entry (never a Thief's Reflexes second turn) and its position; nil when it isn't in the fight.
func (f *fight) entry(id int) (int, map[string]interface{}) {
	for i, e := range f.Order {
		if k := keyOf(e); k.id == id && !k.extra {
			return i, e
		}
	}
	return -1, nil
}

// current is the entry whose turn it is (nil when the index is out of the order).
func (f *fight) current() map[string]interface{} {
	if f.Index < 0 || f.Index >= len(f.Order) {
		return nil
	}
	return f.Order[f.Index]
}

// keepHolder runs change and puts the index back on the entry whose turn it was (when it is still there).
func (f *fight) keepHolder(change func()) {
	cur := f.current()
	change()
	if cur == nil {
		return
	}
	k := keyOf(cur)
	for i, e := range f.Order {
		if keyOf(e) == k {
			f.Index = i
			return
		}
	}
}

// newMonsterID gives the next monster id of this fight: below every id given so far and every id in the order, never one given
// before in the fight (a removed monster's id stays retired with its conditions and effects; audit AUD-39).
func (f *fight) newMonsterID() int {
	low := f.LastMonsterID
	for _, e := range f.Order {
		if id := asInt(e["id"]); id < low {
			low = id
		}
	}
	f.LastMonsterID = low - 1
	return f.LastMonsterID
}

// initiativeBefore: entry a acts before entry b (higher Initiative, then higher Dexterity).
func initiativeBefore(a, b map[string]interface{}) bool {
	if ai, bi := asInt(a["initiative"]), asInt(b["initiative"]); ai != bi {
		return ai > bi
	}
	return asInt(a["dex_score"]) > asInt(b["dex_score"])
}

// insertAt puts e at position at (0 to len), keeping whose turn it is.
func (f *fight) insertAt(at int, e map[string]interface{}) {
	if at < 0 || at > len(f.Order) {
		at = len(f.Order)
	}
	f.keepHolder(func() {
		f.Order = append(f.Order[:at], append([]map[string]interface{}{e}, f.Order[at:]...)...)
	})
}

// place puts e in Initiative order (after the entries it ties with), keeping whose turn it is. The other entries keep their order.
func (f *fight) place(e map[string]interface{}) {
	at := len(f.Order)
	for i, o := range f.Order {
		if initiativeBefore(e, o) {
			at = i
			break
		}
	}
	f.insertAt(at, e)
}

// remove takes every entry of id out of the order (a Thief's second turn too). The index stays on whose turn it is; when that
// was the removed creature, it points at the entry after it (which may be past the end: the round is over) and wasCurrent says so.
func (f *fight) remove(id int) (removed []map[string]interface{}, wasCurrent bool) {
	kept := f.Order[:0:0]
	index := f.Index
	for i, e := range f.Order {
		if asInt(e["id"]) != id {
			kept = append(kept, e)
			continue
		}
		removed = append(removed, e)
		switch {
		case i < f.Index:
			index--
		case i == f.Index:
			wasCurrent = true
		}
	}
	f.Order, f.Index = kept, index
	return removed, wasCurrent
}

// setInitiative gives a combatant a new Initiative and puts it in its new place, keeping whose turn it is.
func (f *fight) setInitiative(id, init int) bool {
	at, e := f.entry(id)
	if e == nil {
		return false
	}
	f.keepHolder(func() {
		f.Order = append(f.Order[:at], f.Order[at+1:]...)
		e["initiative"] = init
		pos := len(f.Order)
		for i, o := range f.Order {
			if initiativeBefore(e, o) {
				pos = i
				break
			}
		}
		f.Order = append(f.Order[:pos], append([]map[string]interface{}{e}, f.Order[pos:]...)...)
	})
	return true
}

// vitalKeys are the fields of an entry that only the Hit Point owner changes (monster_hp_2024.go).
var vitalKeys = []string{"hp", "max_hp", "temp_hp"}

// editCombatant changes fields of a combatant's entry other than its Hit Points (a name, an AC, a size, a marker), with the fight
// locked. A change to hp, max_hp or temp_hp is refused: they belong to the monster Hit Point owner.
func editCombatant(lobby, id int, edit func(e map[string]interface{})) error {
	_, err := changeFight(lobby, func(f *fight) error {
		_, e := f.entry(id)
		if e == nil {
			return errNotInFight
		}
		before := map[string]interface{}{}
		for _, k := range vitalKeys {
			before[k] = e[k]
		}
		edit(e)
		for _, k := range vitalKeys {
			if fmt.Sprint(e[k]) != fmt.Sprint(before[k]) {
				return fmt.Errorf("editCombatant may not change %s: use the monster Hit Point owner (monster_hp_2024.go)", k)
			}
		}
		return nil
	})
	return err
}

// removal is what removeFromFight did.
type removal struct {
	Removed []map[string]interface{} // the entries taken out (a Thief's second turn too)
	Turn    *turnChange              // the turn passed to the next creature (the removed one's turn it was)
	Ended   bool                     // nobody was left: the fight ended
}

// removeFromFight takes a combatant out of the fight (the GM's remove, an Iron Flask, a kick). When it was the creature whose
// turn it is, the turn passes to the next one, which starts its turn (advanceTurn with remove); when nobody is left, the fight
// ends. It answers what it did and "" or a refusal code (combatant_not_found, no_active_combat, a turn refusal).
func removeFromFight(lobby, id int, by string) (removal, string) {
	return removeFromFightTry(lobby, id, by, 3)
}

func removeFromFightTry(lobby, id int, by string, tries int) (removal, string) {
	if tries <= 0 {
		return removal{}, "turn_change_in_progress"
	}
	f, err := readFight(db, lobby)
	if err != nil {
		return removal{}, notFoundOrDBError(err, "no_active_combat")
	}
	var mine []map[string]interface{}
	for _, e := range f.Order {
		if asInt(e["id"]) == id {
			mine = append(mine, e)
		}
	}
	switch {
	case len(mine) == 0:
		return removal{}, "combatant_not_found"
	case len(mine) == len(f.Order): // the last combatant: the fight is over
		if err := clearCombatInitiative(lobby); err != nil {
			return removal{}, "database_error"
		}
		return removal{Removed: mine, Ended: true}, ""
	}
	if cur := f.current(); cur != nil && asInt(cur["id"]) == id {
		t, code := advanceTurn(lobby, turnHow{by: by, remove: id})
		if code == "turn_already_advanced" { // its turn ended meanwhile: an ordinary removal
			return removeFromFightTry(lobby, id, by, tries-1)
		}
		if code != "" {
			return removal{}, code
		}
		return removal{Removed: t.Removed, Turn: t}, ""
	}
	var out removal
	_, err = changeFight(lobby, func(f *fight) error {
		var wasCurrent bool
		out.Removed, wasCurrent = f.remove(id)
		switch {
		case len(out.Removed) == 0:
			return errNotInFight
		case wasCurrent || len(f.Order) == 0: // the turn order changed after the read above: decide again
			return errTurnMoved
		}
		return nil
	})
	switch {
	case errors.Is(err, errTurnMoved):
		return removeFromFightTry(lobby, id, by, tries-1)
	case errors.Is(err, errNotInFight):
		return removal{}, "combatant_not_found"
	case err != nil:
		return removal{}, notFoundOrDBError(err, "no_active_combat")
	}
	return out, ""
}

var errTurnMoved = errors.New("turn_moved")
