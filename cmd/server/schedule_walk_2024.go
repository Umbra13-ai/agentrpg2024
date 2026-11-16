package main

// The turn schedule, stage D: how a round resolves in a campaign whose schedule is on or paused (Matt's directions sections 2,
// 5, 6, 8, 9 and the answers in 11; turn-schedule-design.md 1.2, 1.3 and 2.3). A campaign whose schedule is off plays exactly as
// before. The seats and times are schedule_2024.go, the round record and the status check_in_2024.go.
//
//   - A pending turn is for the round now open only (pendingRoundFor): a turn for another round, or for a character whose turn
//     in the open round has already resolved, is refused and changes nothing. Sending one only saves it.
//   - Only the GM's POSTs run the walk (resolvePendingTurns, pending_turns_2024.go): combat/resolve, combat/stand-in,
//     combat/next and combat/skip. GET /api/gm/status only reports (it runs the walk only for a campaign whose schedule is off).
//   - The walk goes forward through Initiative: a submitted turn resolves (checked again, its fallbacks, never dropped: a turn
//     that no longer works is paused for the GM, needs_gm); an automatic turn (turnNeeds: a dying character's Death Saving
//     Throw, a stable or unconscious character's turn with nothing to choose) is processed once at its place and never waits;
//     at a monster's turn the walk stops for the GM; it stops at the first character who needs a submission and has none.
//   - standInAllowed is the one check of when the GM may take a turn as stand-in: once the round's final check-in has come
//     (none while paused), or to finish a submitted turn that is paused for the GM. The walk, combat/stand-in and combat/skip
//     read it.
//   - turn_results records how each turn of a round ended, once (UNIQUE): the turn-end hook recordTurnResult (turnEndHooks), so
//     every way a turn ends records it; a second resolve re-runs nothing. checkInAt reads it for each character's resolved/how.
//   - When the last turn of a round ends, the fight's round goes up and the turn-start hook opens the schedule's next round
//     (openRound: the open one closes, a pending capacity applies); every reply of that change says "Round N is complete; round
//     N+1 has begun".

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

// How a turn ended (turn_results.how). The table's CHECK lists the same words (turnScheduleSchema; TestTurnResultHows).
const (
	howOwn       = "own"        // the player's submitted turn
	howFallback  = "fallback"   // one of its fallbacks
	howStandIn   = "stand_in"   // the GM took it as stand-in
	howDeathSave = "death_save" // the automatic Death Saving Throw of a dying character
	howAutomatic = "automatic"  // stable, unconscious: nothing to choose, the turn passed
	howMonster   = "monster"    // a monster's or NPC's turn, played by the GM
	howSkipped   = "skipped"    // the GM (or the monster turn timeout) ended it with nothing taken
)

// turnResultHows: each way a turn ends, in the words the status uses.
var turnResultHows = map[string]string{
	howOwn:       "its own turn",
	howFallback:  "a fallback of its own turn",
	howStandIn:   "a stand-in turn by the GM",
	howDeathSave: "the automatic Death Saving Throw",
	howAutomatic: "passed automatically (nothing to choose)",
	howMonster:   "played by the GM",
	howSkipped:   "skipped",
}

// scheduleStateOf: a campaign's schedule state (on, paused or off; sql.ErrNoRows: no such campaign).
func scheduleStateOf(q rowQuerier, lobby int) (string, error) {
	var state string
	err := q.QueryRow(`SELECT COALESCE(schedule_state, 'off') FROM lobbies WHERE id = $1`, lobby).Scan(&state)
	return state, err
}

// usesSchedule: the campaign's rounds run on the schedule (on, or paused: submissions are saved and the GM still resolves them).
func usesSchedule(state string) bool { return state == scheduleOn || state == schedulePaused }

// ---------------------------------------------------------------- submissions: the open round only

// Refusals of a pending turn sent for a round that isn't open (409; nothing is saved).
const (
	codeRoundNotOpen       = "round_not_open"
	codeTurnAlreadyResolve = "turn_already_resolved"
)

// pendingRoundFor is the round a pending turn sent now is for. A campaign without the schedule: this round while the character's
// slot is still to come (or is the current one), the next round once it has passed (as before). With the schedule: only the round
// now open, the fight's round; a character whose slot has passed (its turn resolved) is done for that round, so a turn for it is
// refused with codeTurnAlreadyResolve, and asked is a round the sender named (0: none), refused with codeRoundNotOpen unless it
// is the round the turn is for.
func pendingRoundFor(c ptCombat, slot int, scheduled bool, asked int) (round int, code, message string) {
	if !scheduled {
		round = c.roundFor(slot)
	} else {
		round = c.Round
		if slot < c.Index {
			return c.Round, codeTurnAlreadyResolve, fmt.Sprintf("%s's turn in round %d has resolved: it is done for this round. Round %d begins when the GM resolves this round's last turn; submit for it then. Nothing was saved.",
				c.Entries[slot].Name, c.Round, c.Round+1)
		}
	}
	if asked != 0 && asked != round {
		open := fmt.Sprintf("round %d", round)
		if !scheduled {
			open = fmt.Sprintf("round %d (the round your turn comes in next)", round)
		}
		return round, codeRoundNotOpen, fmt.Sprintf("A turn can only be sent for the round now open: that is %s, not round %d. Nothing was saved.", open, asked)
	}
	return round, "", ""
}

// ---------------------------------------------------------------- stand-ins: once the final check-in has come

// standInCheck: whether the GM may take a turn as stand-in now, and when it may otherwise.
type standInCheck struct {
	Allowed  bool
	Deadline *time.Time // the round's final check-in (nil while paused)
	Why      string     // why not: "paused", "no_round", "too_early"
}

// standInAllowed is the rule (Matt, sections 2 and 6, and 11.4): in a campaign without the schedule the GM may stand in at once
// (as before). With it, a stand-in for a character with no submission waits for the round's final check-in, and there is none
// while the schedule is paused. finishing is a submitted turn that no longer works and is paused for the GM (needs_gm): the GM
// finishes the player's own submission then, at any check-in; that is resolving a submitted turn, not substituting for a missing
// one.
func standInAllowed(state string, r *roundRecord, now time.Time, finishing bool) standInCheck {
	if !usesSchedule(state) || finishing {
		return standInCheck{Allowed: true}
	}
	switch {
	case r == nil:
		return standInCheck{Why: "no_round"}
	case state == schedulePaused || !r.Deadline.Valid:
		return standInCheck{Why: "paused"}
	}
	d := r.Deadline.Time
	if now.UTC().Before(d) {
		return standInCheck{Deadline: tp(d), Why: "too_early"}
	}
	return standInCheck{Allowed: true, Deadline: tp(d)}
}

// standInNow reads what standInAllowed needs for a campaign.
func standInNow(lobby int, finishing bool) (standInCheck, error) {
	state, err := scheduleStateOf(db, lobby)
	if err != nil || !usesSchedule(state) {
		return standInAllowed(state, nil, time.Time{}, finishing), err
	}
	r, err := openRoundOf(db, lobby)
	if err != nil {
		return standInCheck{}, err
	}
	now, err := campaignNow(db, lobby)
	if err != nil {
		return standInCheck{}, err
	}
	return standInAllowed(state, r, now, finishing), nil
}

// tooEarly: the sentence that says a stand-in for name waits, and when.
func (s standInCheck) tooEarly(name string) string {
	switch s.Why {
	case "paused":
		return fmt.Sprintf("The turn schedule is paused: no stand-ins until the GM resumes it. %s can still submit a turn.", name)
	case "no_round":
		return fmt.Sprintf("The turn schedule has no open round, so no stand-in for %s is due. GET /api/campaigns/{id}/schedule shows the schedule.", name)
	}
	return fmt.Sprintf("Stand-ins wait for the round's final check-in (%s): until then %s can still submit a turn, and a GM check-in resolves submitted turns only.",
		clockText(*s.Deadline), name)
}

// refusal: the reply's fields for a stand-in (or a player's skip) asked too early.
func (s standInCheck) refusal(name string) map[string]interface{} {
	out := map[string]interface{}{"error": "stand_in_too_early", "message": s.tooEarly(name) + " Nothing was changed."}
	if s.Deadline != nil {
		out["final_check_in"] = s.Deadline.UTC().Format(time.RFC3339)
	} else {
		out["final_check_in"] = nil
	}
	return out
}

// ---------------------------------------------------------------- how each turn ended, once

// recordTurnResult is the turn-end hook that writes how a turn of a scheduled round ended (turn_results), exactly once: the
// table's UNIQUE (round, combatant, second turn) keeps a second end of the same turn from being counted again. A campaign without
// the schedule has no open round, so nothing is written.
func recordTurnResult(t *turnChange) {
	if t.EndedID == 0 || db == nil {
		return
	}
	var roundID int
	err := db.QueryRow(`SELECT id FROM campaign_rounds WHERE lobby_id = $1 AND closed_at IS NULL AND kind = $2 AND number = $3`, t.Lobby, roundCombat, t.EndedRound).Scan(&roundID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		log.Printf("turn schedule: campaign %d: reading the open round: %v", t.Lobby, err)
		return
	}
	how := t.how.result
	if how == "" {
		how = endedHow(t.EndedID)
	}
	now, err := campaignNow(db, t.Lobby)
	if err != nil {
		log.Printf("turn schedule: campaign %d: the clock: %v", t.Lobby, err)
		return
	}
	recorded, err := recordResult(db, roundID, t.EndedID, t.EndedSecond, t.EndedName, how, now)
	if err != nil {
		log.Printf("turn schedule: campaign %d: recording %s's turn: %v", t.Lobby, t.EndedName, err)
		return
	}
	if recorded {
		t.Recorded = how
	}
}

// recordResult is the one write of turn_results: how a turn of a round ended, once (a second end of the same turn is not counted:
// recorded is false). The turn-end hook (a fight's turns) and the resolve and stand-in of a round outside a fight
// (noncombat_round_2024.go) call it.
func recordResult(q execer, roundID, id int, second bool, name, how string, at time.Time) (recorded bool, err error) {
	res, err := q.Exec(`INSERT INTO turn_results (round_id, combatant_id, second_turn, name, how, at) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (round_id, combatant_id, second_turn) DO NOTHING`, roundID, id, second, name, how, at)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// endedHow: how a turn ended that its caller didn't name (the GM's combat/next or a narration that advances the turn): a
// monster's or NPC's turn is the GM's; a character with nothing to choose passed automatically; a character who acted took its
// own turn (POST /api/action in its slot); anyone else's turn was ended with nothing taken.
func endedHow(id int) string {
	if !characterTurn(id) {
		return howMonster
	}
	if n, err := turnNeeds(id); err == nil && n.Kind == needNothing {
		return howAutomatic
	}
	var acted bool
	db.QueryRow(`SELECT COALESCE(action_used, false) OR COALESCE(bonus_action_used, false) FROM characters WHERE id = $1`, id).Scan(&acted)
	if acted {
		return howOwn
	}
	return howSkipped
}

// slotRecorded: how the turn of this slot in the fight's round has already ended ("" when it hasn't), for the walk: a turn with
// a record never runs again.
func slotRecorded(lobby, round, id int, second bool) (string, error) {
	var how string
	err := db.QueryRow(`SELECT t.how FROM turn_results t JOIN campaign_rounds r ON r.id = t.round_id
		WHERE r.lobby_id = $1 AND r.closed_at IS NULL AND r.kind = $2 AND r.number = $3 AND t.combatant_id = $4 AND t.second_turn = $5`,
		lobby, roundCombat, round, id, second).Scan(&how)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return how, err
}

// roundResults: how each turn of a round has ended so far, by combatant (its first turn; a Thief's Reflexes second turn is
// the second's).
func roundResults(q rowsQuerier, roundID int) (map[int]string, error) {
	rows, err := q.Query(`SELECT combatant_id, how FROM turn_results WHERE round_id = $1 AND NOT second_turn`, roundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var id int
		var how string
		if err := rows.Scan(&id, &how); err != nil {
			return nil, err
		}
		out[id] = how
	}
	return out, rows.Err()
}

// roundCompleteText: what a reply says when the last turn of a round ended and the next round opened.
func roundCompleteText(ended int, next *roundRecord) string {
	s := fmt.Sprintf("Round %d is complete; round %d has begun", ended, next.Number)
	if next.Deadline.Valid {
		return s + fmt.Sprintf(" (its final check-in is %s).", clockText(next.Deadline.Time))
	}
	return s + " (the schedule is paused: no deadline until the GM resumes it)."
}

// scheduleTasks: what GET /api/gm/status asks of the GM in a campaign with the turn schedule, from the status (checkInAt): only
// work that can be done now, and the delay told as the GM's when it is (GM overdue).
func scheduleTasks(ci checkIn, lobby int) []string {
	var out []string
	if ci.Round == nil {
		return nil
	}
	combat := ci.Round.Kind == roundCombat
	if ci.GMOverdue != nil {
		out = append(out, "⚠️ "+ci.GMOverdue.Message)
	}
	if d := ci.Due; d != nil && combat {
		switch {
		case d.Kind == "gm" && len(d.Characters) > 0 && d.Characters[0].CharacterID == 0:
			out = append(out, fmt.Sprintf("%s Then POST /api/campaigns/%d/combat/next; the submitted turns after it resolve in order.", d.Message, lobby))
		case d.Kind == "gm" || d.Kind == "automatic":
			out = append(out, fmt.Sprintf("%s POST /api/campaigns/%d/combat/resolve resolves the turns that can resolve now, in initiative order.", d.Message, lobby))
		}
	}
	var standIns, submitted []string
	for _, ch := range ci.Characters {
		switch ch.Status {
		case statusStandIn:
			standIns = append(standIns, ch.Name)
		case statusSubmitted:
			submitted = append(submitted, ch.Name)
		}
	}
	switch {
	case len(standIns) > 0 && combat:
		out = append(out, fmt.Sprintf("⚠️ The round's final check-in has come with no submission from %s: when each one's turn comes, take it as stand-in (a meaningful in-character action from what the character knows, not a default Dodge and not a skip): POST /api/campaigns/%d/combat/resolve, then POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]}.",
			joinWords(standIns), lobby, lobby))
	case len(standIns) > 0:
		out = append(out, fmt.Sprintf("⚠️ The round's final check-in has come with no submission from %s: take a meaningful in-character turn for each (what the character would do, knowing only what it knows; not a default and not a skip): POST /api/campaigns/%d/round/stand-in {\"character_id\": N, \"description\": \"...\"}.",
			joinWords(standIns), lobby))
	}
	if len(submitted) > 0 && !combat {
		out = append(out, fmt.Sprintf("Submitted turns are waiting in round %d (%s): resolve them in an order that suits the story, POST /api/campaigns/%d/round/resolve {\"character_id\": N} (or {} for all, in the order sent; GET /api/campaigns/%d/round shows each submission). A ruling decides one yourself: {\"character_id\": N, \"ruling\": \"...\"}.",
			ci.Round.Number, joinWords(submitted), lobby, lobby))
	}
	return out
}

// ---------------------------------------------------------------- the GM ending a turn itself

// turnEndCheck: whether the GM may end the current turn itself now (combat/next, combat/skip, a narration's advance_turn).
type turnEndCheck struct {
	Expect *turnRef               // the turn as read: the change applies only while it is still the current one
	Player bool                   // the turn is a seated player's character's
	Reply  map[string]interface{} // a refusal (409), nil when it may end
}

// gmEndsTurn is the rule for the GM ending the current turn of a fight itself in a campaign with the turn schedule (Matt,
// sections 2 and 6: a submitted turn is resolved, never dropped; a stand-in, not a skip, for a player with no submission, and
// only once the final check-in has come). A monster's or NPC's turn may end at once. A player's turn may end when the player
// acted in the slot themselves (POST /api/action), when nothing is left to choose (stable or unconscious), when its submitted
// turn is paused for the GM (needs_gm: the GM decides to end it), or once standInAllowed says a stand-in may be taken (it is
// recorded as skipped). It is refused while the player's submitted turn hasn't been tried (pending_turn_waiting: combat/resolve
// resolves it), for a dying character's turn (automatic_turn: combat/resolve rolls the Death Saving Throw) and before the final
// check-in (stand_in_too_early). scheduled is false for a campaign without the schedule: its callers end the turn as before.
// named is the turn the request means to end (expected_turn), nil when it named none: once that turn has ended nothing here
// judges the turn it is now; check.Expect carries the named turn and the turn change refuses it (turn_already_moved).
func gmEndsTurn(lobby int, named *turnRef) (check turnEndCheck, scheduled bool, err error) {
	check.Expect = named
	state, err := scheduleStateOf(db, lobby)
	if err != nil || !usesSchedule(state) {
		return check, false, err
	}
	f, err := readFight(db, lobby)
	if err != nil {
		return check, true, notFoundIsNothing(err) // no fight: the turn change itself answers no_active_combat
	}
	cur := f.current()
	if cur == nil || (named != nil && !named.isCurrent(f)) {
		return check, true, nil
	}
	check.Expect = &turnRef{Round: f.Round, Index: f.Index, StartedAt: f.StartedAt, Named: named != nil}
	id, name := asInt(cur["id"]), sprintAny(cur["name"])
	if !characterTurn(id) {
		return check, true, nil
	}
	if _, check.Player = ptOwner(ptEntry{ID: id, Name: name}); !check.Player {
		return check, true, nil
	}
	refuse := func(code, msg string) {
		check.Reply = map[string]interface{}{"error": code, "message": msg + " Nothing was changed."}
	}
	row, has := ptOpenRow(lobby, id, f.Round)
	if has && row.Status == "pending" {
		refuse("pending_turn_waiting", fmt.Sprintf("%s submitted a turn for round %d: a submitted turn is resolved, never dropped. POST /api/campaigns/%d/combat/resolve resolves it.", name, f.Round, lobby))
		return check, true, nil
	}
	var acted bool
	if err := db.QueryRow(`SELECT COALESCE(action_used, false) OR COALESCE(bonus_action_used, false) FROM characters WHERE id = $1`, id).Scan(&acted); err != nil {
		return check, true, err
	}
	need, err := turnNeeds(id)
	if err != nil {
		return check, true, err
	}
	switch {
	case acted || need.Kind == needNothing || (has && row.Status == "needs_gm"):
		return check, true, nil
	case need.Kind == needDeathSave:
		refuse("automatic_turn", fmt.Sprintf("%s is dying: the turn is the automatic Death Saving Throw. POST /api/campaigns/%d/combat/resolve rolls it and moves on.", name, lobby))
		return check, true, nil
	}
	sc, err := standInNow(lobby, false)
	if err != nil {
		return check, true, err
	}
	if !sc.Allowed {
		check.Reply = sc.refusal(name)
		check.Reply["message"] = fmt.Sprintf("%s hasn't submitted a turn, and the turn schedule doesn't skip a player's turn: %s", name, check.Reply["message"])
		return check, true, nil
	}
	// the final check-in has come: a missed turn is taken by a stand-in, never skipped or waved through (spec section 6)
	refuse("stand_in_needed", fmt.Sprintf("%s hasn't submitted a turn and the round's final check-in has come: take a meaningful in-character stand-in turn for them (what they would do, knowing only what they know): POST /api/campaigns/%d/combat/stand-in. The turn schedule never skips a missed turn.", name, lobby))
	return check, true, nil
}
