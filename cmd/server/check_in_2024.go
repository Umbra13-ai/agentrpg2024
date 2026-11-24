package main

// The round record and the one answer to "where does this round stand" (Matt's directions sections 2, 3 and 11; the design,
// turn-schedule-design.md 1.2, 1.3, 1.5 and 1.7, stage C). The seats, their times and the clock are schedule_2024.go.
//
//   - campaign_rounds holds each round of a campaign that uses the schedule, combat and noncombat; one row is open at a time.
//     openRound is the one way a round opens: it closes the open one, applies a capacity the GM set during it (pending_capacity),
//     and gives the new round its deadline, roundDeadline(opened): the :55 of the hour it opened in when it opened in that hour's
//     :00 minute, otherwise the next hour's :55, so every seat has a check-in before anyone can be treated as missed. Paused, the
//     round has no deadline; resumed, it gets a fresh one as if it opened then (clock_from).
//     For now a round opens when a fight starts (the fight's round number), when the fight's round goes up, when the fight ends
//     (a noncombat round), and when the GM turns the schedule on. Opening and closing rounds by the GM's resolution is stage D.
//   - turnNeeds says what a character's turn needs (Matt, 11.3), from the Hit Points owner and the condition owner: a dying
//     character's Death Saving Throw (automatic), nothing for a stable, unconscious or dead one, a chosen action for anyone else.
//   - loadCheckIn reads what a status needs and the pure checkInAt(inputs, now) answers it: the round, whose action is due, each
//     character's status (resolved, submitted, waiting on an earlier player, not submitted with its expected check-in, needs a
//     stand-in only once the final check-in has come, automatic) and whether the GM is overdue (from the first GM check-in at
//     which a turn could have resolved, plus 5 minutes; a turn waiting on an earlier player never is). Reading it never changes
//     anything. GET /api/campaigns/{id}/schedule shows it as check_in.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

const (
	roundCombat    = "combat"
	roundNoncombat = "noncombat"
)

// ---------------------------------------------------------------- the round record

// roundDeadline: a round's final check-in (its deadline) from the moment it opened (or was resumed). The final GM check-in of
// every capacity is :55 (scheduleOf).
func roundDeadline(opened time.Time) time.Time {
	t := opened.UTC()
	hour := t.Truncate(time.Hour)
	final := time.Duration(finalMinute) * time.Minute
	if t.Minute() == 0 {
		return hour.Add(final)
	}
	return hour.Add(time.Hour + final)
}

// finalMinute: the final check-in's minute, which every capacity shares (scheduleOf: the last GM check-in, 5 minutes before
// the next hour's :00).
const finalMinute = 60 - gmLeadMinutes

// roundRecord: a round as stored. Its times are campaign time.
type roundRecord struct {
	ID, Number int
	Kind       string
	OpenedAt   time.Time
	ClockFrom  time.Time    // when the round opened, or when the schedule was last resumed: seats' check-ins count from here
	Deadline   sql.NullTime // the final check-in; not set while paused
}

// openRoundOf reads a campaign's open round (nil when none).
func openRoundOf(q rowQuerier, lobby int) (*roundRecord, error) {
	r := &roundRecord{}
	err := q.QueryRow(`SELECT id, number, kind, opened_at, clock_from, deadline FROM campaign_rounds WHERE lobby_id = $1 AND closed_at IS NULL`, lobby).
		Scan(&r.ID, &r.Number, &r.Kind, &r.OpenedAt, &r.ClockFrom, &r.Deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.OpenedAt, r.ClockFrom = r.OpenedAt.UTC(), r.ClockFrom.UTC()
	if r.Deadline.Valid {
		r.Deadline.Time = r.Deadline.Time.UTC()
	}
	return r, nil
}

// clockText: a time as messages show it ("10:55 UTC"); the replies' fields carry the full time.
func clockText(t time.Time) string { return t.UTC().Format("15:04") + " UTC" }

// roundName: how every reply names a round of the schedule. A fight's round is "fight round N" (the fight's own count), a round
// outside a fight "campaign round N" (the campaign's count of those), so the two numbers never read as one (I-KESTREL-7): the chat
// line, my-turn, the heartbeat and the GM's replies all name a round with it.
func roundName(kind string, number int) string {
	if kind == roundCombat {
		return fmt.Sprintf("fight round %d", number)
	}
	return fmt.Sprintf("campaign round %d", number)
}

// roundTitle: roundName at the start of a sentence.
func roundTitle(kind string, number int) string {
	n := roundName(kind, number)
	return strings.ToUpper(n[:1]) + n[1:]
}

// opened: the sentence that says a round opened.
func (r *roundRecord) opened() string {
	what := roundTitle(r.Kind, r.Number) + " is open"
	if !r.Deadline.Valid {
		return what + "; the schedule is paused, so it has no deadline until the GM resumes it."
	}
	return what + fmt.Sprintf("; its final check-in is %s.", clockText(r.Deadline.Time))
}

// roundToOpen: the round a campaign opens now: the fight's round in a fight, else the next noncombat round.
func roundToOpen(q rowQuerier, lobby int) (kind string, number int, err error) {
	f, err := readFight(q, lobby)
	if err == nil {
		return roundCombat, f.Round, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	err = q.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM campaign_rounds WHERE lobby_id = $1 AND kind = $2`, lobby, roundNoncombat).Scan(&number)
	return roundNoncombat, number, err
}

// closeOpenRound closes the campaign's open round (if any); its submissions still waiting are withdrawn, saying why.
func closeOpenRound(tx *sql.Tx, lobby int, now time.Time, why string) error {
	if err := withdrawOpenSubmissions(tx, lobby, why); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE campaign_rounds SET closed_at = $1 WHERE lobby_id = $2 AND closed_at IS NULL`, now.UTC(), lobby)
	return err
}

// openRound opens a round in tx (the caller has locked the campaign's row and the schedule isn't off): the open round closes, a
// capacity set for the next round takes effect, and the new round gets its deadline (none while paused).
func openRound(tx *sql.Tx, lobby int, kind string, number int, now time.Time) (*roundRecord, error) {
	var state string
	var pending sql.NullInt64
	if err := tx.QueryRow(`SELECT COALESCE(schedule_state, 'off'), pending_capacity FROM lobbies WHERE id = $1`, lobby).Scan(&state, &pending); err != nil {
		return nil, err
	}
	now = now.UTC()
	if err := closeOpenRound(tx, lobby, now, "the round closed before it was resolved"); err != nil {
		return nil, err
	}
	if pending.Valid {
		if err := applyCapacity(tx, lobby, int(pending.Int64)); err != nil {
			return nil, err
		}
	}
	r := &roundRecord{Number: number, Kind: kind, OpenedAt: now, ClockFrom: now}
	if state != schedulePaused {
		r.Deadline = nullTime(roundDeadline(now))
	}
	err := tx.QueryRow(`INSERT INTO campaign_rounds (lobby_id, kind, number, opened_at, clock_from, deadline) VALUES ($1, $2, $3, $4, $4, $5) RETURNING id`,
		lobby, kind, number, now, r.Deadline).Scan(&r.ID)
	if err != nil {
		return r, err
	}
	// every player learns that a round opened in the party chat, where they look between check-ins (feedback log 35, 56)
	return r, postServerMessage(tx, lobby, roundOpenedLine(r))
}

// roundOpenedLine: the server's party-chat line when a round opens.
func roundOpenedLine(r *roundRecord) string {
	line := fmt.Sprintf("%s began at %s.", roundTitle(r.Kind, r.Number), clockText(r.OpenedAt))
	if r.Number == 1 && r.Kind != roundCombat {
		line = "Play is open: " + line
	}
	if r.Deadline.Valid {
		return line + fmt.Sprintf(" Send your turn by the round's final check-in (%s); GET /api/my-turn shows your next check-in.", clockText(r.Deadline.Time))
	}
	return line + " The schedule is paused: GET /api/my-turn shows when turns are due."
}

// openOnlyIfNone: roundOpens's onlyAfter for a round that opens only when none is open (the campaign starting).
const openOnlyIfNone = "none"

// roundOpens opens a round for a fight's change (it starts, its round goes up, it ends) in its own transaction, in a campaign
// whose schedule isn't off. onlyIfNew: not when the open round is already this one (a hook that may run twice); onlyAfter: only
// when the open round is of that kind (a fight's end opens a noncombat round only after a combat round).
// It answers the round it opened (nil when it opened none).
func roundOpens(lobby int, kind string, number int, onlyIfNew bool, onlyAfter string) (*roundRecord, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRow(`SELECT COALESCE(schedule_state, 'off') FROM lobbies WHERE id = $1 FOR UPDATE`, lobby).Scan(&state); err != nil {
		return nil, notFoundIsNothing(err)
	}
	if state == scheduleOff {
		return nil, nil
	}
	open, err := openRoundOf(tx, lobby)
	if err != nil {
		return nil, err
	}
	if onlyIfNew && open != nil && open.Kind == kind && open.Number == number {
		return nil, nil
	}
	switch {
	case onlyAfter == openOnlyIfNone:
		if open != nil {
			return nil, nil
		}
	case onlyAfter != "" && (open == nil || open.Kind != onlyAfter):
		return nil, nil
	}
	if kind == roundNoncombat {
		if err := tx.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM campaign_rounds WHERE lobby_id = $1 AND kind = $2`, lobby, roundNoncombat).Scan(&number); err != nil {
			return nil, err
		}
	}
	now, err := campaignNow(tx, lobby)
	if err != nil {
		return nil, err
	}
	r, err := openRound(tx, lobby, kind, number, now)
	if err != nil {
		return nil, err
	}
	return r, tx.Commit()
}

func notFoundIsNothing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// The fight's hooks (combat_state_2024.go, turn_advance_2024.go): the round record follows the fight's round.
func scheduleFightStarts(lobby int) {
	if _, err := releaseTable(lobby); err != nil { // a fight opens play after a held start (table_hold_2024.go)
		logScheduleProblem(lobby, "a fight started", err)
	}
	_, err := roundOpens(lobby, roundCombat, 1, false, "")
	logScheduleProblem(lobby, "a fight started", err)
}

// scheduleFightRound: the fight's round went up because its last turn ended: the open round closes and the next opens (the
// round it opened; nil when the campaign has no schedule).
func scheduleFightRound(lobby, round int) *roundRecord {
	r, err := roundOpens(lobby, roundCombat, round, true, "")
	logScheduleProblem(lobby, "a new combat round", err)
	return r
}
func scheduleFightEnds(lobby int) {
	_, err := roundOpens(lobby, roundNoncombat, 0, false, roundCombat)
	logScheduleProblem(lobby, "a fight ended", err)
}

// ---------------------------------------------------------------- what a turn needs

const (
	needChoose    = "choose"     // a chosen action: a submission (or a stand-in at the final check-in)
	needDeathSave = "death_save" // dying: the Death Saving Throw, rolled automatically at the character's turn
	needNothing   = "none"       // stable, dead or unable to act (an Incapacitating condition): the turn passes with no action
)

// turnNeed: what a character's turn needs, and why ("dying", "stable", "unconscious", "dead"; "" for a chosen action).
type turnNeed struct {
	Kind string
	Why  string
}

// turnNeedOf is the rule (Matt, 11.3): 0 Hit Points alone is not the exemption. A dying character (at 0, not stable) gets the
// automatic Death Saving Throw, never a submission or a stand-in; a stable one rolls none and needs no action; a dead character
// is never a missing submission; a character an Incapacitating condition holds (cantActBecause: asleep, Paralyzed, Stunned ...) has
// no action to choose, its Why the condition. Anyone else chooses an action.
func turnNeedOf(v game.Vitals, cantAct string) turnNeed {
	switch {
	case v.Dead:
		return turnNeed{needNothing, "dead"}
	case v.Dying() && !v.Stable:
		return turnNeed{needDeathSave, "dying"}
	case v.Dying():
		return turnNeed{needNothing, "stable"}
	case cantAct != "":
		return turnNeed{needNothing, cantAct}
	}
	return turnNeed{Kind: needChoose}
}

// turnNeeds reads a character's Hit Points (the Hit Points owner) and conditions (the condition owner's column).
func turnNeeds(charID int) (turnNeed, error) {
	v, err := readVitals(db, charID)
	if err != nil {
		return turnNeed{}, err
	}
	return turnNeedOf(v, cantActBecause(charID)), nil
}

// needsDeathSave: the character's turn is its automatic Death Saving Throw (the pending-turn walk, my-turn).
func needsDeathSave(charID int) bool {
	n, err := turnNeeds(charID)
	return err == nil && n.Kind == needDeathSave
}

// ---------------------------------------------------------------- the status

// checkInSlot: one place in a fight's turn order. Player: a seated character of the campaign (anything else is the GM's to play).
type checkInSlot struct {
	ID     int
	Name   string
	Player bool
}

// checkInFight: the fight as the status reads it.
type checkInFight struct {
	Round, Current int
	Slots          []checkInSlot
	SlotSince      time.Time // campaign time: when the current slot's turn started
}

// charTurn: a seated character's turn this round.
type charTurn struct {
	Needs       turnNeed
	Submitted   bool
	SubmittedAt time.Time // campaign time
	PausedForGM bool      // its submitted turn no longer worked: paused for the GM to decide
	Resolved    bool      // outside a fight: its turn has resolved (in a fight the turn order says it)
	How         string    // how its turn ended this round, from turn_results (own, fallback, stand_in, death_save, automatic, skipped)
}

// checkInInputs: everything checkInAt reads.
type checkInInputs struct {
	State string
	Sched schedule
	Timed bool // the capacity gives the seats times (1 to 6)
	Seats []seatHolder
	Round *roundRecord
	Fight *checkInFight // nil outside a fight
	Turns map[int]charTurn
	Held  bool // the GM holds the table before round 1 (table_hold_2024.go)
}

// Character statuses (never "blocked" or "missing": Matt, 11.1).
const (
	statusResolved     = "resolved"
	statusSubmitted    = "submitted"
	statusWaiting      = "waiting"
	statusNotSubmitted = "not_submitted"
	statusStandIn      = "needs_stand_in"
	statusAutomatic    = "automatic"
	statusNotInFight   = "not_in_fight"
)

type charStatus struct {
	CharacterID int         `json:"character_id"`
	Name        string      `json:"name"`
	Seat        int         `json:"seat"`
	Status      string      `json:"status"`
	Message     string      `json:"message"`
	Automatic   string      `json:"automatic,omitempty"` // death_save, stable, unconscious, dead
	How         string      `json:"how,omitempty"`       // resolved: how the turn ended (turn_results)
	WaitingOn   *nameRef    `json:"waiting_on,omitempty"`
	CheckInAt   *time.Time  `json:"check_in_at,omitempty"`
	WindowUntil *time.Time  `json:"window_until,omitempty"`
	Window      *turnWindow `json:"window,omitempty"`
	ResolvesAt  *time.Time  `json:"resolves_at,omitempty"`
}

// turnWindow: a seat's submission window this round, from the schedule (scheduleOf): its check-in (opens_at) to the GM's check-in
// after it (closes_at, never after the round's final check-in). open: now is inside it. send_now: the turn is still to send and its
// check-in has come (inside the window, or after it until the GM takes a stand-in), so a client can act on one field (I-ALDRIC-3).
type turnWindow struct {
	OpensAt  time.Time `json:"opens_at"`
	ClosesAt time.Time `json:"closes_at"`
	Open     bool      `json:"open"`
	SendNow  bool      `json:"send_now"`
	Means    string    `json:"means"` // windowMeans: the window is the seat's check-in, never a gate on sending (Thistlewick's playtest)
}

// windowMeans: what a seat's window is, said with every window: a player read "open": false as "can't send yet" while its turn was accepted.
const windowMeans = "Your seat's check-in time: open says whether it has come, send_now that it has and your turn is still to send. " +
	"You may send your turn before it; it is saved for the GM."

type nameRef struct {
	CharacterID int    `json:"character_id,omitempty"`
	Name        string `json:"name"`
}

type roundView struct {
	Number        int        `json:"number"`
	Kind          string     `json:"kind"`
	Name          string     `json:"name"` // roundName: "campaign round 3", "fight round 1"
	OpenedAt      time.Time  `json:"opened_at"`
	FinalCheckIn  *time.Time `json:"final_check_in"` // null while paused
	FinalReached  bool       `json:"final_check_in_reached"`
	Paused        bool       `json:"paused"`
	DeadlineRule  string     `json:"deadline_rule"`
	CombatRound   int        `json:"combat_round,omitempty"`
	CurrentTurnOf string     `json:"current_turn_of,omitempty"`
}

type dueView struct {
	Kind       string    `json:"kind"` // player, players, gm, automatic, none
	Characters []nameRef `json:"characters,omitempty"`
	Message    string    `json:"message"`
}

type overdueView struct {
	Turn    string    `json:"turn"`
	CheckIn time.Time `json:"check_in"` // the GM check-in at which it could have resolved
	Since   time.Time `json:"since"`    // that check-in plus 5 minutes
	Message string    `json:"message"`
}

// checkIn: where the open round stands, for everyone (GET .../schedule's check_in).
type checkIn struct {
	Now           time.Time    `json:"now"`
	State         string       `json:"state"`
	Round         *roundView   `json:"round"`
	GMCheckIns    []time.Time  `json:"gm_check_ins,omitempty"`
	NextGMCheckIn *time.Time   `json:"next_gm_check_in,omitempty"`
	Due           *dueView     `json:"due,omitempty"`
	GMOverdue     *overdueView `json:"gm_overdue"`
	Characters    []charStatus `json:"characters"`
	Message       string       `json:"message,omitempty"`
}

const deadlineRuleText = "A round's final check-in is the :55 of the hour it opened in when it opened in that hour's :00 minute, else the next hour's :55; resuming the schedule gives a fresh one."

func truncMinute(t time.Time) time.Time { return t.UTC().Truncate(time.Minute) }

// nextAt: the first time at or after from's minute that is minute past an hour.
func nextAt(from time.Time, minute int) time.Time {
	f := truncMinute(from)
	t := f.Truncate(time.Hour).Add(time.Duration(minute) * time.Minute)
	if t.Before(f) {
		t = t.Add(time.Hour)
	}
	return t
}

// gmCheckInsOf: the round's GM check-ins: each GM minute after the minute it opened (or resumed), up to its deadline.
func gmCheckInsOf(s schedule, from, deadline time.Time) []time.Time {
	var out []time.Time
	start := truncMinute(from)
	for h := start.Truncate(time.Hour); !h.After(deadline); h = h.Add(time.Hour) {
		for _, m := range s.GM {
			t := h.Add(time.Duration(m) * time.Minute)
			if t.After(start) && !t.After(deadline) {
				out = append(out, t)
			}
		}
	}
	return out
}

// firstAtOrAfter: the first time in ts at or after t. A caller that means "the check-in going on now" passes truncMinute(now); a turn
// counts for a check-in only if it was in by that check-in's time (feedback log 41: a turn sent at 21:55:54 was called due at 21:55).
func firstAtOrAfter(ts []time.Time, t time.Time) (time.Time, bool) {
	for _, x := range ts {
		if !x.Before(t) {
			return x, true
		}
	}
	return time.Time{}, false
}

func tp(t time.Time) *time.Time { return &t }

// checkInAt answers where the open round stands at now. It only reads its inputs.
func checkInAt(in checkInInputs, now time.Time) checkIn {
	now = now.UTC()
	out := checkIn{Now: now, State: in.State, Characters: []charStatus{}}
	switch {
	case in.State == scheduleOff:
		out.Message = "The turn schedule is off: the campaign plays as before."
		return out
	case in.Round == nil && in.Held:
		out.Message = "No round is open: the GM is holding the table before round 1 (a session zero). Players can talk and finish their characters; round 1 opens when the GM opens play."
		return out
	case in.Round == nil:
		out.Message = "No round is open: one opens when the GM starts the campaign (or turns the schedule on), when a fight starts and when a fight ends."
		return out
	}
	r := in.Round
	paused := in.State == schedulePaused || !r.Deadline.Valid
	rv := &roundView{Number: r.Number, Kind: r.Kind, Name: roundName(r.Kind, r.Number), OpenedAt: r.OpenedAt, Paused: paused, DeadlineRule: deadlineRuleText}
	var deadline time.Time
	if !paused {
		deadline = r.Deadline.Time
		rv.FinalCheckIn = tp(deadline)
		rv.FinalReached = !now.Before(deadline)
	}
	out.Round = rv
	var gm []time.Time
	if !paused && in.Timed {
		gm = gmCheckInsOf(in.Sched, r.ClockFrom, deadline)
		out.GMCheckIns = gm
		if next, ok := firstAtOrAfter(gm, truncMinute(now)); ok { // the check-in of this minute is still the next one
			out.NextGMCheckIn = tp(next)
		}
	}
	// a submitted turn resolves at the GM's next check-in: in the round's window, or after its final check-in the GM's next one at all
	// (feedback log entry 36: the status named only the player's own next check-in)
	lateGM := (*time.Time)(nil)
	if out.NextGMCheckIn == nil && !paused && in.Timed && len(in.Sched.GM) > 0 {
		next := time.Time{}
		for _, m := range in.Sched.GM {
			if t := nextAt(now, m); next.IsZero() || t.Before(next) {
				next = t
			}
		}
		lateGM = tp(next)
	}
	// a submitted turn counts for a GM check-in only if it was in by that check-in's time (feedback log 41, 55: a turn sent at 21:55:54
	// said it resolves at 21:55); a check-in that passed after it came in (the GM late) gives way to the next one
	resolvesAt := func(sent time.Time) *time.Time {
		if out.NextGMCheckIn != nil {
			from := truncMinute(now)
			if sent.After(from) {
				from = sent
			}
			if next, ok := firstAtOrAfter(gm, from); ok {
				return tp(next)
			}
			return out.NextGMCheckIn
		}
		return lateGM
	}
	submittedText := func(sent time.Time) string {
		switch at := resolvesAt(sent); {
		case out.NextGMCheckIn != nil && at != nil:
			return fmt.Sprintf("Submitted: it resolves at the GM's next check-in (%s).", clockText(*at))
		case lateGM != nil:
			return fmt.Sprintf("Submitted: the round's final check-in has passed, so the GM resolves it at their next check-in (%s). There is nothing more for you to do this round.", clockText(*lateGM))
		}
		return "Submitted: the GM resolves it at a check-in."
	}
	// a seat's window this round (nil while paused or without seat times)
	windowOf := func(seat int) *turnWindow {
		if paused || !in.Timed {
			return nil
		}
		m, ok := in.Sched.seatMinute(seat)
		if !ok {
			return nil
		}
		at := nextAt(r.ClockFrom, m)
		until := at.Add(time.Duration(in.Sched.Interval-gmLeadMinutes) * time.Minute)
		if until.After(deadline) {
			until = deadline
		}
		return &turnWindow{OpensAt: at, ClosesAt: until, Open: !now.Before(at) && now.Before(until), Means: windowMeans}
	}
	// the GM check-in at which a stand-in is taken: the one going on now or next in the round, else the GM's next one after it
	standInAt := func() *time.Time {
		if out.NextGMCheckIn != nil {
			return out.NextGMCheckIn
		}
		return lateGM
	}
	// a character who still has to submit: its expected check-in, its window, the final check-in
	notSubmitted := func(st *charStatus) {
		if w := windowOf(st.Seat); w != nil {
			w.SendNow = !now.Before(w.OpensAt)
			st.Window = w
		}
		if !paused && rv.FinalReached {
			st.Status = statusStandIn
			// the player may still send its turn until the GM takes the stand-in (Matt, Nov 17: a late turn is taken until then)
			if at := standInAt(); at != nil {
				st.Message = fmt.Sprintf("The round's final check-in (%s) has passed with no submission: you can still send your turn until the GM takes a stand-in turn for %s at its next check-in (%s).", clockText(deadline), st.Name, clockText(*at))
			} else {
				st.Message = fmt.Sprintf("The round's final check-in (%s) has passed with no submission: you can still send your turn until the GM takes a stand-in turn for %s at its next check-in.", clockText(deadline), st.Name)
			}
			return
		}
		st.Status = statusNotSubmitted
		if paused {
			st.Message = "Not submitted yet. The schedule is paused: no check-in or deadline until the GM resumes it; you can submit any time."
			return
		}
		if st.Window == nil {
			st.Message = fmt.Sprintf("Not submitted yet; you can submit until the round's final check-in at %s.", clockText(deadline))
			return
		}
		at, until := st.Window.OpensAt, st.Window.ClosesAt
		st.CheckInAt, st.WindowUntil = tp(at), tp(until)
		switch {
		case now.Before(at):
			st.Message = fmt.Sprintf("Not submitted yet. You can send your turn now: it is saved for the GM, and your check-in is at %s.", clockText(at))
		case now.Before(until):
			st.Message = fmt.Sprintf("Not submitted yet. Send your turn now: your check-in window is open until %s.", clockText(until))
		default:
			st.Message = fmt.Sprintf("Your check-in has passed; you can still submit until the round's final check-in at %s.", clockText(deadline))
		}
	}
	automatic := func(st *charStatus, n turnNeed) {
		st.Status, st.Automatic = statusAutomatic, n.Why
		if n.Kind == needDeathSave {
			st.Automatic = needDeathSave
		}
		switch n.Why {
		case "dying":
			st.Message = "At 0 Hit Points and dying: the Death Saving Throw is rolled automatically when the turn comes; no submission is needed."
		case "stable":
			st.Message = "Stable at 0 Hit Points: no Death Saving Throws and no action to choose this round."
		case "unconscious":
			st.Message = "Unconscious: no action to choose this round."
		default:
			st.Message = "Dead: no turn to take."
		}
	}
	resolved := func(st *charStatus, how string) {
		st.Window = windowOf(st.Seat) // send_now false: nothing to send
		st.Status, st.How, st.Message = statusResolved, how, "Done for this round."
		if w, ok := turnResultHows[how]; ok {
			st.Message = "Done for this round: " + w + "."
		}
	}
	statusOf := map[int]*charStatus{}
	for _, s := range in.Seats {
		statusOf[s.CharacterID] = &charStatus{CharacterID: s.CharacterID, Name: s.Name, Seat: s.Seat}
	}

	if f := in.Fight; f != nil && f.Current >= 0 && f.Current < len(f.Slots) {
		rv.CombatRound, rv.CurrentTurnOf = f.Round, f.Slots[f.Current].Name
		inFight := map[int]bool{}
		for i, slot := range f.Slots {
			if !slot.Player {
				continue
			}
			st := statusOf[slot.ID]
			if st == nil || inFight[slot.ID] { // a second entry (Thief's Reflexes): the first decides
				continue
			}
			inFight[slot.ID] = true
			t := in.Turns[slot.ID]
			switch {
			case t.How != "" || (i < f.Current && t.Needs.Why != "dead"): // a dead character has no turn to resolve
				resolved(st, t.How)
			case t.Needs.Kind != needChoose:
				automatic(st, t.Needs)
			case t.Submitted:
				var blocker *nameRef
				for j := f.Current; j < i; j++ {
					b := f.Slots[j]
					bt := in.Turns[b.ID]
					if b.Player && bt.Needs.Kind == needChoose && !bt.Submitted {
						blocker = &nameRef{CharacterID: b.ID, Name: b.Name}
						break
					}
				}
				switch {
				case blocker != nil:
					st.Status, st.WaitingOn = statusWaiting, blocker
					st.Message = fmt.Sprintf("Submitted; waiting on %s, whose turn comes first in Initiative and who hasn't submitted yet. Nobody is late.", blocker.Name)
				case t.PausedForGM:
					st.Status = statusSubmitted
					st.Message = "Submitted, but it no longer works as sent: it is paused for the GM to decide. You can send a new one."
				default:
					st.Status, st.ResolvesAt, st.Message = statusSubmitted, resolvesAt(t.SubmittedAt), submittedText(t.SubmittedAt)
				}
				st.Window = windowOf(st.Seat)
			default:
				notSubmitted(st)
			}
		}
		for _, s := range in.Seats {
			if !inFight[s.CharacterID] {
				st := statusOf[s.CharacterID]
				st.Status, st.Message = statusNotInFight, "Not in this fight's turn order."
			}
		}
		// whose action is due, and whether the GM is overdue for it
		cur := f.Slots[f.Current]
		t := in.Turns[cur.ID]
		resolvable := true
		since := f.SlotSince
		switch {
		case !cur.Player:
			out.Due = &dueView{Kind: "gm", Characters: []nameRef{{Name: cur.Name}}, Message: fmt.Sprintf("%s's turn: the GM chooses its action at a check-in.", cur.Name)}
		case t.Needs.Kind != needChoose:
			out.Due = &dueView{Kind: "automatic", Characters: []nameRef{{cur.ID, cur.Name}}, Message: fmt.Sprintf("%s's turn passes automatically (%s) at the GM's check-in.", cur.Name, t.Needs.Why)}
		case t.Submitted:
			out.Due = &dueView{Kind: "gm", Characters: []nameRef{{cur.ID, cur.Name}}, Message: fmt.Sprintf("%s has submitted: the GM resolves the turn at a check-in.", cur.Name)}
			if t.SubmittedAt.After(since) {
				since = t.SubmittedAt
			}
		default:
			resolvable = false
			out.Due = &dueView{Kind: "player", Characters: []nameRef{{cur.ID, cur.Name}}, Message: fmt.Sprintf("%s's turn: waiting for their submission.", cur.Name)}
		}
		if resolvable && !paused {
			out.GMOverdue = overdue(gm, since, now, cur.Name)
		}
	} else {
		// outside a fight: no Initiative; every character submits and the GM resolves in an order that suits the story
		var waiting []nameRef
		var firstSubmitted *time.Time
		var firstName string
		for _, s := range in.Seats {
			st, t := statusOf[s.CharacterID], in.Turns[s.CharacterID]
			switch {
			case t.Resolved || t.How != "":
				resolved(st, t.How)
			case t.Needs.Kind != needChoose:
				automatic(st, t.Needs)
			case t.Submitted:
				st.Status, st.ResolvesAt, st.Message = statusSubmitted, resolvesAt(t.SubmittedAt), submittedText(t.SubmittedAt)
				st.Window = windowOf(st.Seat)
				if firstSubmitted == nil || t.SubmittedAt.Before(*firstSubmitted) {
					firstSubmitted, firstName = tp(t.SubmittedAt), s.Name
				}
			default:
				notSubmitted(st)
				waiting = append(waiting, nameRef{s.CharacterID, s.Name})
			}
		}
		switch {
		case len(waiting) > 0:
			out.Due = &dueView{Kind: "players", Characters: waiting, Message: "Every character who hasn't submitted can submit now; the GM resolves the submitted turns at its check-ins."}
		case firstSubmitted != nil:
			out.Due = &dueView{Kind: "gm", Message: "Every turn is in: the GM resolves them at a check-in."}
		default:
			out.Due = &dueView{Kind: "none", Message: "Nothing is waiting."}
		}
		if firstSubmitted != nil && !paused {
			since := *firstSubmitted
			if r.ClockFrom.After(since) {
				since = r.ClockFrom
			}
			out.GMOverdue = overdue(gm, since, now, firstName)
		}
	}
	for _, s := range in.Seats {
		out.Characters = append(out.Characters, *statusOf[s.CharacterID])
	}
	return out
}

// overdue: a turn that could resolve since since is overdue once the first GM check-in at or after that minute is 5 minutes past.
func overdue(gm []time.Time, since, now time.Time, turn string) *overdueView {
	g, ok := firstAtOrAfter(gm, since)
	if !ok {
		return nil
	}
	late := g.Add(gmGraceMinutes * time.Minute)
	if now.Before(late) {
		return nil
	}
	return &overdueView{Turn: turn, CheckIn: g, Since: late,
		Message: fmt.Sprintf("The GM is overdue: %s's turn could have resolved at the %s check-in. The delay is the GM's resolution, not a player's.", turn, clockText(g))}
}

// loadCheckIn reads what checkInAt needs for a campaign, and the campaign's time now.
func loadCheckIn(lobby int) (checkInInputs, time.Time, error) {
	var in checkInInputs
	cs, err := loadSchedule(db, lobby)
	if err != nil {
		return in, time.Time{}, err
	}
	clock, err := clockOf(db, lobby)
	if err != nil {
		return in, time.Time{}, err
	}
	in.State, in.Seats = cs.State, cs.Seats
	if in.Held, err = tableHeld(db, lobby); err != nil {
		return in, time.Time{}, err
	}
	in.Sched, in.Timed = scheduleOf(cs.MaxPlayers)
	if in.Round, err = openRoundOf(db, lobby); err != nil || in.Round == nil || cs.State == scheduleOff {
		return in, clock.now(), err
	}
	seated := map[int]bool{}
	in.Turns = map[int]charTurn{}
	for _, s := range cs.Seats {
		seated[s.CharacterID] = true
		n, err := turnNeeds(s.CharacterID)
		if err != nil {
			return in, time.Time{}, err
		}
		in.Turns[s.CharacterID] = charTurn{Needs: n}
	}
	done, err := roundResults(db, in.Round.ID) // how each turn has ended (schedule_walk_2024.go)
	if err != nil {
		return in, time.Time{}, err
	}
	for id, how := range done {
		if t, ok := in.Turns[id]; ok {
			t.How = how
			in.Turns[id] = t
		}
	}
	if in.Round.Kind == roundNoncombat { // a round outside a fight: a character has submitted while a step of it waits
		steps, err := roundSteps(db, in.Round.ID, 0)
		if err != nil {
			return in, time.Time{}, err
		}
		for _, s := range steps {
			if t, ok := in.Turns[s.CharacterID]; ok && s.Status == "pending" && !t.Submitted {
				t.Submitted, t.SubmittedAt = true, s.SubmittedAt // stored in campaign time
				in.Turns[s.CharacterID] = t
			}
		}
	}
	if in.Round.Kind == roundCombat {
		f, err := readFight(db, lobby)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return in, time.Time{}, err
		}
		if err == nil {
			cf := &checkInFight{Round: f.Round, Current: f.Index, SlotSince: clock.at(f.StartedAt)}
			if cf.SlotSince.Before(in.Round.ClockFrom) {
				cf.SlotSince = in.Round.ClockFrom
			}
			for _, e := range f.Order {
				id := asInt(e["id"])
				monster, _ := e["is_monster"].(bool)
				cf.Slots = append(cf.Slots, checkInSlot{ID: id, Name: fmt.Sprint(e["name"]), Player: !monster && seated[id]})
			}
			in.Fight = cf
			rows, err := db.Query(`SELECT character_id, status, submitted_at FROM pending_turns WHERE lobby_id = $1 AND round = $2 AND status IN ('pending', 'needs_gm')`, lobby, f.Round)
			if err != nil {
				return in, time.Time{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var id int
				var status string
				var at time.Time
				if err := rows.Scan(&id, &status, &at); err != nil {
					return in, time.Time{}, err
				}
				if t, ok := in.Turns[id]; ok {
					t.Submitted, t.SubmittedAt, t.PausedForGM = true, clock.at(at), status == "needs_gm"
					in.Turns[id] = t
				}
			}
			if err := rows.Err(); err != nil {
				return in, time.Time{}, err
			}
		}
	}
	return in, clock.now(), nil
}

// roundNoteFor is the open round as the heartbeat and my-turn show it, a view of checkInAt: for a character (charID > 0) its own
// status, its next check-in and how to send its turn; for the GM (charID 0) its next check-in, whose action is due and whether it
// is overdue. nil when the campaign doesn't use the schedule or no round is open.
// roundAtTime: the number and the name (roundName) of the schedule's round that was open at a moment of real time (false: none).
func roundAtTime(lobby int, at time.Time) (int, string, bool) {
	clock, err := clockOf(db, lobby)
	if err != nil {
		return 0, "", false
	}
	var n int
	var kind string
	if db.QueryRow(`SELECT number, kind FROM campaign_rounds WHERE lobby_id = $1 AND opened_at <= $2 ORDER BY opened_at DESC, id DESC LIMIT 1`, lobby, clock.at(at)).Scan(&n, &kind) != nil {
		return 0, "", false
	}
	return n, roundName(kind, n), true
}

func roundNoteFor(lobby, charID int) (map[string]interface{}, error) {
	state, err := scheduleStateOf(db, lobby)
	if err != nil || !usesSchedule(state) {
		return nil, notFoundIsNothing(err)
	}
	in, now, err := loadCheckIn(lobby)
	if err != nil || in.Round == nil {
		return nil, err
	}
	ci := checkInAt(in, now)
	v := map[string]interface{}{"round": ci.Round.Number, "kind": ci.Round.Kind, "name": ci.Round.Name, "opened_at": ci.Round.OpenedAt, "final_check_in": ci.Round.FinalCheckIn,
		"paused": ci.Round.Paused, "details": fmt.Sprintf("GET /api/campaigns/%d/round", lobby)}
	if charID == 0 {
		v["next_gm_check_in"], v["due"], v["gm_overdue"] = ci.NextGMCheckIn, ci.Due, ci.GMOverdue
		v["reminder"] = gmReminder(ci)
		return v, nil
	}
	for _, c := range ci.Characters {
		if c.CharacterID != charID {
			continue
		}
		v["status"], v["message"] = c.Status, c.Message
		if c.ResolvesAt != nil { // a submitted turn resolves at the GM's check-in, not at the player's own (feedback log entry 36)
			v["resolves_at"] = c.ResolvesAt
		}
		if c.Window != nil { // the seat's window this round and whether to send now (I-ALDRIC-3)
			v["window"] = c.Window
		}
		if ci.Round.Kind != roundCombat { // outside a fight, the round says whether the player can still send its turn (playtest I-ALDEN-5)
			v["your_turn"] = roundTurnOpen(c.Status)
		}
		if c.WaitingOn != nil {
			v["waiting_on"] = c.WaitingOn.Name
		}
		if ci.Round.FinalReached && c.Status == statusResolved && ci.Round.Kind != roundCombat { // feedback log 56
			v["next_round"] = fmt.Sprintf("%s is over for you. The next round opens when the GM has resolved this round's last turns; the server says so in the party chat.", roundTitle(ci.Round.Kind, ci.Round.Number))
		}
	}
	if next, ok := playerNextCheckIn(lobby, charID, in.Round, now); ok {
		v["next_check_in"] = next
		if ci.Round.FinalCheckIn != nil && next.After(*ci.Round.FinalCheckIn) { // I-ALDRIC-3: a later round's check-in, not this round's
			v["next_check_in_note"] = fmt.Sprintf("next_check_in is your seat's next recurring check-in, after this round's final check-in (%s): this round's turn is due before then (window.send_now says when to send it).", clockText(*ci.Round.FinalCheckIn))
		}
	}
	if ci.Round.Kind == roundCombat {
		v["how_to_send"] = "POST /api/pending-turn {\"steps\": [...]}: your turn is saved for this round and the GM resolves it at a check-in, in Initiative order."
	} else {
		v["how_to_send"] = fmt.Sprintf("Send your turn as usual (POST /api/action, a rest, a feature, buying or changing gear once you have played a round): outside a fight it is saved as your submission for this round (202) and the GM applies it at a check-in. Speech and campaign messages are immediate. DELETE /api/campaigns/%d/round?character_id=%d withdraws it.", lobby, charID)
	}
	return v, nil
}

// roundTurnOpen: a player can still send this round's turn (nothing sent and nothing resolved yet). my-turn's is_my_turn and the
// heartbeat's your_turn outside a fight read it through roundNoteFor's your_turn.
func roundTurnOpen(status string) bool {
	return status == statusNotSubmitted || status == statusStandIn
}

// roundOrNil: roundNoteFor as a reply field (nil without the schedule; a database failure in it is answered by guardDB).
func roundOrNil(lobby, charID int) interface{} {
	if v, err := roundNoteFor(lobby, charID); err == nil && v != nil {
		return v
	}
	return nil
}
