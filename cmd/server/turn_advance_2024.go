package main

// The one owner of a turn change in a fight (audit AUD-10, findings C-2 and B-10). The GM's combat/next, combat/skip, a narration
// with advance_turn, the timeout worker, the pending-turn walk and the removal of the creature whose turn it is all call
// advanceTurn, and only advanceTurn runs the turn hooks: the ending creature's (turnEndHooks) and the next one's (turnStartHooks),
// each exactly once, in the order of those tables.
//
// Before Nov 15 2026 the change was written four times, each path with its own subset of the hooks (a narrated advance never
// brought a Blinking character back from the Ethereal Plane, Sacred Weapon ran out only through combat/next, Reactions came back
// at a different moment on each path), and only skip guarded against two changes at once: two concurrent combat/next moved the
// turn once but ran its end-of-turn effects twice.
//
// A change runs in four steps:
//  1. claim: with the fight's row locked, the turn is checked (an expected turn, a timeout) and marked as being changed
//     (turn_claim). A second change of the same fight while the mark stands is refused (turn_change_in_progress), so a double
//     submit or the timeout worker racing the GM changes the turn once.
//  2. the ending creature's hooks run (the turn order still points at it), then the caller's between step (Legendary Actions);
//  3. move: with the row locked again, the next creature that has a turn is chosen (dead characters and monsters at 0 Hit Points
//     have none; a Thief's Reflexes second turn is dropped after round 1) and the round, index and turn clock are written;
//  4. the next creature's hooks run, and the mark is cleared.
// The hooks read and write other rows, so they run outside any transaction (CLAUDE.md: never db inside a transaction); the mark,
// not a lock, keeps them from overlapping with another turn change. A mark older than two minutes (a crash) no longer stands (fightColumns).

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// turnRef is a turn as a caller saw it: advance only while it is still the current one.
type turnRef struct {
	Round, Index int
	StartedAt    time.Time // compared when set
}

// turnHow is how a turn ends.
type turnHow struct {
	by      string                                // who or what ends it, for the note of a pending turn it closes ("the GM skipped the turn")
	expect  *turnRef                              // advance only while this is still the current turn (the timeout worker, the pending-turn walk)
	allow   func(f *fight) (code, message string) // a check of the turn under the lock (skip: a player's turn that has timed out)
	between func(t *turnChange)                   // after the ending creature's hooks, before the next one's turn starts (Legendary Actions)
	remove  int                                   // this creature, whose turn it is, leaves the fight: no end-of-turn hooks; the next one starts
}

// turnChange is what a turn change did; the hooks and the callers' replies read it.
type turnChange struct {
	Lobby          int
	how            turnHow
	EndedID        int
	EndedName      string
	EndedRound     int
	EndedIndex     int
	EndedStartedAt time.Time
	Removed        []map[string]interface{} // the entries taken out (remove)
	Round, Index   int
	NextID         int
	NextName       string
	NewRound       bool
	Log            []string               // conditions and effects at the end and the start of the turn
	SpellNotes     []string               // Blink, Regenerate, Aura of Life ... (moreTurnHooks)
	Survivor       map[string]interface{} // Champion level 18
	Message        string                 // why a change was refused
}

// window is the Legendary Action window the ended turn opens ("round:index").
func (t *turnChange) window() string { return fmt.Sprintf("%d:%d", t.EndedRound, t.EndedIndex) }

// turnHook is one rule that runs when a turn ends or starts.
type turnHook struct {
	name string
	run  func(t *turnChange)
}

// turnEndHooks run for the creature whose turn ends (not for one removed from the fight), in this order; turnStartHooks for the
// creature whose turn starts. A new rule that happens at the end or the start of a turn is one row here.
var turnEndHooks, turnStartHooks []turnHook

func characterTurn(id int) bool { return id > 0 && !isCompanionCombatID(id) }

func init() {
	turnEndHooks = []turnHook{
		{"an unresolved pending turn of the slot closes", func(t *turnChange) {
			if t.EndedID > 0 {
				closePendingTurns(t.Lobby, t.EndedID, t.EndedRound, "the turn ended without it ("+t.how.by+")")
			}
		}},
		{"countercharm, Sacred Weapon and Holy Nimbus count down", func(t *turnChange) {
			if characterTurn(t.EndedID) {
				decrementCountercharm(t.EndedID)
				decrementSacredWeapon(t.EndedID)
				decrementHolyNimbus(t.EndedID)
			}
		}},
		{"conditions and effects at the end of the turn", func(t *turnChange) {
			t.Log = append(t.Log, endTurnEffects(t.Lobby, -1)...) // -1: the current turn, still the ending one
		}},
		{"the Legendary Action window after the turn opens", func(t *turnChange) {
			setLastTurnEnded(t.Lobby, t.window(), t.EndedID)
		}},
	}
	turnStartHooks = []turnHook{
		{"conditions and effects at the start of the turn", func(t *turnChange) {
			t.Log = append(t.Log, startTurnEffects(t.Lobby)...)
		}},
		{"the turn's action, Bonus Action, Reaction, movement and once-per-turn features return", func(t *turnChange) {
			if characterTurn(t.NextID) {
				resetTurnFlags(t.NextID, characterSpeedOf(t.NextID)) // also ends a Dodge: "until the start of your next turn"
			}
		}},
		{"spells at the turn change (Blink, Regenerate, Aura of Life, Cloak of Displacement)", func(t *turnChange) {
			ended := t.EndedID
			if t.how.remove != 0 {
				ended = 0 // the creature left the fight: its turn didn't end
			}
			t.SpellNotes = moreTurnHooks(t.Lobby, ended, t.NextID)
			for _, n := range t.SpellNotes {
				recordGMAction(db, t.Lobby, "spell_effect", n, n)
			}
		}},
		{"Survivor (Champion level 18)", func(t *turnChange) {
			if characterTurn(t.NextID) {
				t.Survivor = survivorRegen(t.NextID, t.NextName)
			}
		}},
		{"Multiattack Defense marks end", func(t *turnChange) { clearAllMultiattackDefenseHits(t.Lobby) }},
		{"Opportunity Attack chances end", func(t *turnChange) { clearOAWindows(t.Lobby) }},
	}
}

// advanceTurn ends the current turn and starts the next one (see the top of this file). It answers the change and "" or a refusal
// code (turnRefusals; t.Message may say more).
func advanceTurn(lobby int, how turnHow) (*turnChange, string) {
	t, token, code := claimTurn(lobby, how)
	if code != "" {
		return t, code
	}
	defer releaseTurn(lobby, token)
	if how.remove == 0 {
		for _, h := range turnEndHooks {
			h.run(t)
		}
	}
	if how.between != nil {
		how.between(t)
	}
	if code := moveTurn(t, token); code != "" {
		return t, code
	}
	for _, h := range turnStartHooks {
		h.run(t)
	}
	return t, ""
}

func newClaimToken() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// claimTurn checks the turn with the row locked and marks it as being changed.
func claimTurn(lobby int, how turnHow) (*turnChange, string, string) {
	t := &turnChange{Lobby: lobby, how: how}
	tx, err := db.Begin()
	if err != nil {
		return t, "", "database_error"
	}
	defer tx.Rollback()
	f, err := lockFight(tx, lobby)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return t, "", "no_active_combat"
	case errors.Is(err, errBadOrder):
		return t, "", "invalid_combat_turn"
	case err != nil:
		return t, "", "database_error"
	}
	if code, msg := how.check(f); code != "" {
		t.Message = msg
		return t, "", code
	}
	cur := f.current()
	token := newClaimToken()
	if _, err := tx.Exec(`UPDATE combat_state SET turn_claim = $1, turn_claimed_at = NOW() WHERE lobby_id = $2`, token, lobby); err != nil {
		return t, "", "database_error"
	}
	if err := tx.Commit(); err != nil {
		return t, "", "database_error"
	}
	t.EndedID, t.EndedName = asInt(cur["id"]), sprintAny(cur["name"])
	t.EndedRound, t.EndedIndex, t.EndedStartedAt = f.Round, f.Index, f.StartedAt
	return t, token, ""
}

// check is whether this turn of f may change now: the turn order is usable, the turn is the one expected, no other change of it
// is under way, and the caller's own check passes. A code and a message when it may not.
func (how turnHow) check(f *fight) (string, string) {
	switch {
	case len(f.Order) == 0:
		return "no_combatants", ""
	case f.current() == nil:
		return "invalid_combat_turn", "" // kept for repair rather than guessing whose turn is consumed
	}
	if e := how.expect; e != nil && (f.Round != e.Round || f.Index != e.Index || (!e.StartedAt.IsZero() && !f.StartedAt.Equal(e.StartedAt))) {
		return "turn_already_advanced", ""
	}
	if f.claimHeld {
		return "turn_change_in_progress", ""
	}
	if how.remove != 0 && asInt(f.current()["id"]) != how.remove {
		return "turn_already_advanced", ""
	}
	if how.allow != nil {
		return how.allow(f)
	}
	return "", ""
}

// releaseTurn clears the mark of a turn change (only its own: a mark that expired and was taken over stays).
func releaseTurn(lobby int, token string) {
	db.Exec(`UPDATE combat_state SET turn_claim = '' WHERE lobby_id = $1 AND turn_claim = $2`, lobby, token)
}

// noTurnFor says which combatants have no turn: dead characters and companions that are gone are read here, before the move's
// transaction; a monster at 0 Hit Points is read from the locked turn order itself.
func noTurnFor(lobby int) func(order []map[string]interface{}) func(int) bool {
	down := map[int]bool{}
	if f, err := readFight(db, lobby); err == nil {
		for _, e := range f.Order {
			id := asInt(e["id"])
			switch {
			case isCompanionCombatID(id):
				down[id] = companionGone(id)
			case id > 0:
				down[id] = combatCharacterIsDead(id)
			}
		}
	}
	return func(order []map[string]interface{}) func(int) bool {
		monsterDown := map[int]bool{}
		for _, e := range order {
			if id := asInt(e["id"]); id < 0 && asInt(e["hp"]) <= 0 {
				monsterDown[id] = true
			}
		}
		return func(id int) bool {
			if id < 0 {
				return monsterDown[id]
			}
			return down[id]
		}
	}
}

// moveTurn writes the next turn: the next creature that has one, the round, the turn clock.
func moveTurn(t *turnChange, token string) string {
	skip := noTurnFor(t.Lobby)
	f, err := changeFight(t.Lobby, func(f *fight) error {
		if f.claim != token {
			return errTurnMoved // the fight restarted or ended during the change, or the mark expired and was taken over
		}
		from := f.Index + 1
		if t.how.remove != 0 {
			t.Removed, _ = f.remove(t.how.remove)
			from = f.Index
			if len(f.Order) == 0 {
				return errNotInFight
			}
		}
		f.nextTurn(from, skip(f.Order))
		f.restartClock = true
		return nil
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "no_active_combat"
	case errors.Is(err, errTurnMoved), errors.Is(err, errNotInFight):
		return "turn_already_advanced"
	case err != nil:
		return "database_error"
	}
	t.Round, t.Index = f.Round, f.Index
	t.NewRound = f.Round != t.EndedRound
	if cur := f.current(); cur != nil {
		t.NextID, t.NextName = asInt(cur["id"]), sprintAny(cur["name"])
	}
	return ""
}

// nextTurn moves f to the first creature from position from (len: the round is over) that has a turn. A Thief's Reflexes
// second turn is dropped once the first round is over ("two turns during the first round of any combat").
func (f *fight) nextTurn(from int, noTurn func(int) bool) {
	round := f.Round
	if from >= len(f.Order) {
		from, round = 0, round+1
	}
	if round >= 2 {
		kept := f.Order[:0:0]
		pos := from
		for i, e := range f.Order {
			if keyOf(e).extra {
				if i < from {
					pos--
				}
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) < len(f.Order) {
			f.Order, from = kept, pos
			if from >= len(f.Order) {
				from, round = 0, round+1
			}
		}
	}
	ids := make([]int, len(f.Order))
	for i, e := range f.Order {
		ids[i] = asInt(e["id"])
	}
	index, wraps := nextLivingCombatTurn(from, ids, noTurn)
	f.Index, f.Round = index, round+wraps
}

// combatTurnTimeout is when a turn may be skipped: the campaign's table rule combat_turn_timeout (4 hours by default). on is false
// when it is off (turns are never skipped). combat/skip, the timeout worker and the GM status countdown read it.
func combatTurnTimeout(lobby int) (limit time.Duration, on bool) {
	return timeoutRule(lobby, "combat_turn_timeout", 4*time.Hour)
}

// turnRefusals: the status and message of each refusal of a turn change.
var turnRefusals = map[string]struct {
	status  int
	message string
}{
	"no_active_combat":        {http.StatusConflict, "No fight is running in this campaign: POST /api/campaigns/%d/combat/start starts one."},
	"no_combatants":           {http.StatusConflict, "The turn order is empty: add combatants with POST /api/campaigns/%d/combat/add, or end the fight."},
	"invalid_combat_turn":     {http.StatusConflict, "The current turn points outside the turn order; nothing was changed. GET /api/campaigns/%d/combat shows the order."},
	"turn_already_advanced":   {http.StatusConflict, "That turn has already ended; nothing was changed. GET /api/campaigns/%d/combat shows whose turn it is."},
	"turn_change_in_progress": {http.StatusConflict, "Another request is changing this turn right now (it counts once); nothing more was changed. GET /api/campaigns/%d/combat shows whose turn it is."},
	"not_player_turn":         {http.StatusConflict, "It is a monster's or NPC's turn: only a player's turn is skipped. Resolve it and POST /api/campaigns/%d/combat/next."},
	"turn_not_timed_out":      {http.StatusConflict, "The turn hasn't timed out yet (table rule combat_turn_timeout); end it with POST /api/campaigns/%d/combat/next if it is done."},
	"database_error":          {http.StatusInternalServerError, "The database failed; nothing more was changed (campaign %d)."},
}

// answerTurnRefusal writes the reply to a refused turn change (extra fields added to it).
func answerTurnRefusal(w http.ResponseWriter, lobby int, t *turnChange, code string, extra ...map[string]interface{}) {
	r, ok := turnRefusals[code]
	if !ok {
		r.status, r.message = http.StatusConflict, "The turn wasn't changed (campaign %d)."
	}
	msg := fmt.Sprintf(r.message, lobby)
	if t != nil && t.Message != "" {
		msg = t.Message
	}
	out := map[string]interface{}{"error": code, "message": msg}
	for _, x := range extra {
		for k, v := range x {
			out[k] = v
		}
	}
	writeJSONStatus(w, r.status, out)
}

// reply is what every turn change answers: the new turn, the round, and what happened at the change.
func (t *turnChange) reply() map[string]interface{} {
	m := map[string]interface{}{"success": true, "round": t.Round, "current_turn": t.NextName, "turn_index": t.Index, "action_economy_reset": true}
	if t.NewRound {
		m["new_round"] = true
	}
	if len(t.SpellNotes) > 0 {
		m["spell_turn_effects"] = t.SpellNotes
	}
	if t.Survivor != nil {
		m["survivor_regen"] = t.Survivor
	}
	if opts, task := openLegendaryOptions(t.Lobby); len(opts) > 0 { // after the turn that ended (monster_features_2024.go)
		m["legendary_options"] = opts
		m["legendary_note"] = task
	}
	return m
}
