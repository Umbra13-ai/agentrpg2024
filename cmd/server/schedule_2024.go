package main

// A campaign's turn schedule: its seats, their check-in times and the GM's, the clock the schedule reads, and the way into a
// seat (Matt's directions, campaign-scheduling-directions.md section 2 and the answers in 11; the design, turn-schedule-design.md
// part 1.1 and 1.6). The round record and the status every participant is shown are check_in_2024.go.
//
//   - scheduleOf(capacity) is the times: N seats (1 to 6) are 60/N minutes apart from :00, and the GM checks in 5 minutes before
//     each next seat's time; the last GM check-in of the hour (:55) is the round's final check-in. seatCapacityProblem is the one
//     check of a seat count. The creation reply, the join reply, GET /api/campaigns/{id}, GET|PATCH .../schedule and the GM
//     guide's table (scheduleTable) are all made from it.
//   - campaignNow / campaignClock is the only clock the schedule reads: real time, or on a test server (AGENTRPG_TEST_CLOCK=1) a
//     campaign's test clock, moved with POST /api/campaigns/{id}/test-clock so a suite can play an hour in minutes.
//   - claimSeat is the one way a character enters a campaign (joining, the admin's add, restore and editor, the GM's recreate):
//     the campaign's row is locked, the capacity checked and the lowest free seat taken in the same transaction, so two joins
//     for the last seat can't both win. A seat is held while its character is in the campaign; a character that leaves (kicked,
//     deleted, the campaign deleted) frees its seat by leaving, and the next player takes the lowest free seat and its time.
//   - schedule_state: on, paused or off. A new campaign is on from creation; a campaign made before the schedule existed is off
//     until its GM turns it on (PATCH .../schedule). A capacity change made while a round is open waits in pending_capacity and
//     takes effect when the next round opens (openRound); with no round open it takes effect at once. Either way the seats keep
//     their join order.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	maxScheduledSeats = 6 // Matt: seat capacity is at most 6, so 60/N is always whole minutes
	gmLeadMinutes     = 5 // the GM checks in this many minutes before each next seat's time
	gmGraceMinutes    = 5 // the GM is overdue this long after the check-in at which a turn could have resolved (Matt, 11.7)

	scheduleOn     = "on"
	schedulePaused = "paused"
	scheduleOff    = "off"
)

// scheduleStates: what each state means, in the words every reply uses.
var scheduleStates = map[string]string{
	scheduleOn:     "on: rounds run on the check-in times below; a round's final check-in is its deadline",
	schedulePaused: "paused: deadlines, reminders and stand-ins wait; submissions are still saved and the GM may still resolve them; resuming gives the open round a fresh deadline",
	scheduleOff:    "off: this campaign doesn't use the turn schedule (it plays as before; check in as GET /api/heartbeat says)",
}

// ---------------------------------------------------------------- the times

// schedule is the check-in times of a seat capacity, in minutes past every hour.
type schedule struct {
	Capacity int
	Interval int   // minutes between seats: 60 / Capacity
	Seats    []int // seat n checks in at Seats[n-1]
	GM       []int // the GM's check-ins: 5 minutes before each next seat's time; the last is the round's final check-in
}

// seatCapacityProblem is the one check of a scheduled campaign's seat count: "" when it is allowed, else why not.
// turnScheduleEnabled says whether campaigns may use the turn schedule on this server. The schedule is built in stages (Matt's
// campaign-scheduling-directions.md): seats, times, rounds and the resolution walk in fights (B-D) are done; rounds outside a
// fight (E) and the reminders in my-turn, the heartbeat and replies (F) are not, so a production server keeps it off until they
// are and no campaign plays a half-built schedule. AGENTRPG_TURN_SCHEDULE=1 turns it on (staging and local tests). While it is
// off a new campaign starts with the schedule off (its capacity unlimited as before) and a GM can't turn it on.
var turnScheduleEnabled = os.Getenv("AGENTRPG_TURN_SCHEDULE") == "1"

// scheduleNotAvailable is the refusal of turning the schedule on while turnScheduleEnabled is false.
const scheduleNotAvailable = "The turn schedule isn't available on this server yet: campaigns play without check-in times for now. Nothing was changed."

func seatCapacityProblem(n int) string {
	if n < 1 || n > maxScheduledSeats {
		return fmt.Sprintf("A campaign has 1 to %d player seats (each seat gets its own check-in time, 60/seats minutes apart); %d is not allowed.", maxScheduledSeats, n)
	}
	return ""
}

// scheduleOf gives the times of a seat capacity; ok is false outside 1 to 6.
func scheduleOf(capacity int) (s schedule, ok bool) {
	if seatCapacityProblem(capacity) != "" {
		return schedule{}, false
	}
	s = schedule{Capacity: capacity, Interval: 60 / capacity}
	for i := 0; i < capacity; i++ {
		s.Seats = append(s.Seats, i*s.Interval)
		s.GM = append(s.GM, (i+1)*s.Interval-gmLeadMinutes)
	}
	return s, true
}

// final is the minute of the round's final check-in (the last GM check-in of the hour: always :55).
func (s schedule) final() int { return s.GM[len(s.GM)-1] }

// seatMinute: a seat's minute past the hour; ok is false for a seat beyond the capacity.
func (s schedule) seatMinute(seat int) (int, bool) {
	if seat < 1 || seat > len(s.Seats) {
		return 0, false
	}
	return s.Seats[seat-1], true
}

func minuteText(m int) string { return fmt.Sprintf(":%02d", m) }

func minutesText(ms []int) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = minuteText(m)
	}
	return out
}

// joinWords: "a", "a and b", "a, b and c".
func joinWords(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

const scheduleTimesAre = "minutes past every hour, UTC (the same minutes in any time zone whose offset is a whole number of hours)"

// gmScheduleNote: what the GM is told when the campaign is made or the schedule changes.
func gmScheduleNote(s schedule, lobby int) string {
	per := len(s.GM)
	fewer := ""
	if s.Capacity > 1 {
		fewer = fmt.Sprintf(" If you can't keep %d check-ins an hour, choose fewer seats: PATCH /api/campaigns/%d/schedule {\"capacity\": N} gives N check-ins an hour.", per, lobby)
	}
	return fmt.Sprintf("Check in at %s past every hour (UTC): %d check-ins an hour, each 5 minutes before the next seat's time, to resolve the turns submitted by then. %s is each round's final check-in: a round runs at most to it, and only there do you take stand-in turns for players who haven't submitted.%s With your human's permission, set up a recurring check at these times (a scheduled task).",
		joinWords(minutesText(s.GM)), per, minuteText(s.final()), fewer)
}

// seatNote: what a player is told about a seat's time.
func seatNote(s schedule, seat int) string {
	m, ok := s.seatMinute(seat)
	if !ok {
		return fmt.Sprintf("Seat %d has no check-in time (the campaign has %d seats): the GM can raise the capacity.", seat, s.Capacity)
	}
	return fmt.Sprintf("Your check-in is at %s past every hour (UTC). With your human's permission, set up a recurring check at that time (a scheduled task). It is when you are expected to look, not a gate: you can submit your turn whenever a round is open, until the round's final check-in at %s.",
		minuteText(m), minuteText(s.final()))
}

// scheduleTable: the times for every seat capacity, as a Markdown table (the GM guide shows it).
func scheduleTable() string {
	var b strings.Builder
	b.WriteString("| Seats | Player check-ins (seat 1, 2, ...) | GM check-ins | GM check-ins an hour | Final check-in |\n|---|---|---|---|---|\n")
	for n := 1; n <= maxScheduledSeats; n++ {
		s, _ := scheduleOf(n)
		fmt.Fprintf(&b, "| %d | %s | %s | %d | %s |\n", n, strings.Join(minutesText(s.Seats), " "), strings.Join(minutesText(s.GM), " "), len(s.GM), minuteText(s.final()))
	}
	return b.String()
}

// scheduleTableMarker is where docs/gm_guide.md shows the table; withScheduleTable writes it there.
const scheduleTableMarker = "<!-- schedule table: written by the server from scheduleOf (cmd/server/schedule_2024.go) -->"

func withScheduleTable(guide string) string {
	return strings.Replace(guide, scheduleTableMarker, scheduleTable(), 1)
}

// ---------------------------------------------------------------- the clock

const testClockEnv = "AGENTRPG_TEST_CLOCK"

// testClockOn: this server has test clocks (a test server only; never set on live).
func testClockOn() bool { return os.Getenv(testClockEnv) == "1" }

// clockSetting: at real time realAt the campaign's clock was set to campaignAt; from there it runs at the speed of real time.
type clockSetting struct {
	realAt, campaignAt time.Time
}

// campaignClock is a campaign's clock: real time, or the latest setting of its test clock (none unless testClockOn).
type campaignClock struct{ settings []clockSetting } // in the order they were made

// at: the campaign time of a moment in real time (a timestamp another table wrote with NOW()): counted from the last setting made
// by then, so a time recorded before a change keeps the campaign time it had.
func (c campaignClock) at(real time.Time) time.Time {
	t := real.UTC()
	for i := len(c.settings) - 1; i >= 0; i-- {
		if s := c.settings[i]; !s.realAt.After(real) {
			return s.campaignAt.Add(real.Sub(s.realAt)).UTC()
		}
	}
	return t
}

func (c campaignClock) now() time.Time { return c.at(time.Now()) }

// clockOf reads a campaign's clock (a test clock only when the server has them).
func clockOf(q rowsQuerier, lobby int) (campaignClock, error) {
	var c campaignClock
	if !testClockOn() {
		return c, nil
	}
	rows, err := q.Query(`SELECT real_at, campaign_at FROM campaign_test_clock WHERE lobby_id = $1 ORDER BY id`, lobby)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var s clockSetting
		if err := rows.Scan(&s.realAt, &s.campaignAt); err != nil {
			return c, err
		}
		s.realAt, s.campaignAt = s.realAt.UTC(), s.campaignAt.UTC()
		c.settings = append(c.settings, s)
	}
	return c, rows.Err()
}

// campaignNow is the only "now" the schedule reads (q is the transaction when there is one).
func campaignNow(q rowsQuerier, lobby int) (time.Time, error) {
	c, err := clockOf(q, lobby)
	return c.now(), err
}

// ---------------------------------------------------------------- seats

// seatHolder: a held seat (its character is in the campaign).
type seatHolder struct {
	Seat        int
	CharacterID int
	Name        string
}

// heldSeats reads a campaign's held seats in seat order.
func heldSeats(q rowsQuerier, lobby int) ([]seatHolder, error) {
	rows, err := q.Query(`SELECT s.seat, c.id, c.name FROM campaign_seats s
		JOIN characters c ON c.id = s.character_id AND c.lobby_id = s.lobby_id
		WHERE s.lobby_id = $1 ORDER BY s.seat`, lobby)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []seatHolder
	for rows.Next() {
		var h seatHolder
		if err := rows.Scan(&h.Seat, &h.CharacterID, &h.Name); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// seatClaim: a character entering a campaign, and how.
type seatClaim struct {
	Lobby, Character int
	Owner            int  // when set, the character must be this agent's (a player's join)
	OnePerPlayer     bool // refuse when the owner already plays another character in the campaign (a player's join)
	OnlyFromNone     bool // refuse when the character is in another campaign (restoring a deleted campaign's members)
}

// seatTaken: what claimSeat did.
type seatTaken struct {
	Seat    int
	Already bool // the character was already in the campaign
	Left    int  // the campaign it left to come here (0: none)
}

// seatRefusal: why a character can't enter. Status is the HTTP status a request answers with.
type seatRefusal struct {
	Status        int
	Code, Message string
	Extra         map[string]interface{}
}

func (e *seatRefusal) Error() string { return e.Message }

// reply: the refusal as a request's answer.
func (e *seatRefusal) reply() map[string]interface{} {
	out := map[string]interface{}{"error": e.Code, "message": e.Message}
	for k, v := range e.Extra {
		out[k] = v
	}
	return out
}

// answerSeatProblem answers a claim that failed: a refusal with its status, a database failure as 500. It says whether it answered.
func answerSeatProblem(w http.ResponseWriter, err error) bool {
	var ref *seatRefusal
	switch {
	case err == nil:
		return false
	case errors.As(err, &ref):
		writeJSONStatus(w, ref.Status, ref.reply())
	default:
		writeDBError(w, err, "the campaign's seats")
	}
	return true
}

// seatLimit: how many characters may be seated: the capacity, or a lower capacity the GM has set for the next round.
func seatLimit(maxPlayers int, pending sql.NullInt64) int {
	if pending.Valid && int(pending.Int64) < maxPlayers {
		return int(pending.Int64)
	}
	return maxPlayers
}

// claimSeat puts a character into a campaign: the one way in (TestNoOtherWayIntoACampaign). In tx, with the campaign's row locked,
// it checks the claim (the owner, one character per player, the capacity) and gives the character the lowest free seat. A
// refusal is a *seatRefusal and changes nothing; any other error is the database's.
func claimSeat(tx *sql.Tx, c seatClaim) (seatTaken, error) {
	var out seatTaken
	var maxPlayers int
	var pending sql.NullInt64
	if err := tx.QueryRow(`SELECT COALESCE(max_players, 0), pending_capacity FROM lobbies WHERE id = $1 FOR UPDATE`, c.Lobby).Scan(&maxPlayers, &pending); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, &seatRefusal{Status: http.StatusNotFound, Code: "campaign_not_found", Message: fmt.Sprintf("There is no campaign %d.", c.Lobby)}
		}
		return out, err
	}
	var owner sql.NullInt64
	var current int
	var name string
	if err := tx.QueryRow(`SELECT agent_id, COALESCE(lobby_id, 0), name FROM characters WHERE id = $1 FOR UPDATE`, c.Character).Scan(&owner, &current, &name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, &seatRefusal{Status: http.StatusNotFound, Code: "character_not_found", Message: fmt.Sprintf("There is no character %d.", c.Character)}
		}
		return out, err
	}
	if c.Owner != 0 && (!owner.Valid || int(owner.Int64) != c.Owner) {
		return out, &seatRefusal{Status: http.StatusNotFound, Code: "character_not_found", Message: fmt.Sprintf("You have no character %d.", c.Character)}
	}
	if current == c.Lobby {
		out.Already = true
		err := tx.QueryRow(`SELECT seat FROM campaign_seats WHERE lobby_id = $1 AND character_id = $2`, c.Lobby, c.Character).Scan(&out.Seat)
		if errors.Is(err, sql.ErrNoRows) { // in the campaign without a seat (it joined before seats existed): it takes one now
			out.Seat, err = takeSeat(tx, c.Lobby, c.Character)
		}
		return out, err
	}
	if c.OnlyFromNone && current != 0 {
		return out, &seatRefusal{Status: http.StatusConflict, Code: "in_another_campaign", Message: fmt.Sprintf("%s is in campaign %d now.", name, current)}
	}
	if c.OnePerPlayer && owner.Valid {
		var other string
		err := tx.QueryRow(`SELECT name FROM characters WHERE agent_id = $1 AND lobby_id = $2 AND id <> $3 LIMIT 1`, owner.Int64, c.Lobby, c.Character).Scan(&other)
		if err == nil {
			return out, &seatRefusal{Status: http.StatusConflict, Code: "one_character_per_player",
				Message: fmt.Sprintf("You already play %s in this campaign; a player has one character in a campaign. Ask the GM to remove that character first.", other)}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM characters WHERE lobby_id = $1`, c.Lobby).Scan(&count); err != nil {
		return out, err
	}
	if limit := seatLimit(maxPlayers, pending); campaignFull(limit, count) {
		return out, &seatRefusal{Status: http.StatusConflict, Code: "campaign_full", Message: "This campaign is full.",
			Extra: map[string]interface{}{"campaign_id": c.Lobby, "max_players": limit, "player_count": count}}
	}
	if _, err := tx.Exec(`UPDATE characters SET lobby_id = $1 WHERE id = $2`, c.Lobby, c.Character); err != nil {
		return out, err
	}
	out.Left = current
	var err error
	out.Seat, err = takeSeat(tx, c.Lobby, c.Character)
	return out, err
}

// takeSeat gives a character (in the campaign, its row locked by the caller) the lowest free seat: one whose character has left,
// or the next seat number.
func takeSeat(tx *sql.Tx, lobby, char int) (int, error) {
	if _, err := tx.Exec(`UPDATE campaign_seats SET character_id = NULL WHERE lobby_id = $1 AND character_id = $2`, lobby, char); err != nil {
		return 0, err
	}
	var seat int
	err := tx.QueryRow(`SELECT s.seat FROM campaign_seats s
		LEFT JOIN characters c ON c.id = s.character_id AND c.lobby_id = s.lobby_id
		WHERE s.lobby_id = $1 AND c.id IS NULL ORDER BY s.seat LIMIT 1`, lobby).Scan(&seat)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT COALESCE(MAX(seat), 0) + 1 FROM campaign_seats WHERE lobby_id = $1`, lobby).Scan(&seat)
	}
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT INTO campaign_seats (lobby_id, seat, character_id, joined_at) VALUES ($1, $2, $3, NOW())
		ON CONFLICT (lobby_id, seat) DO UPDATE SET character_id = EXCLUDED.character_id, joined_at = EXCLUDED.joined_at`, lobby, seat, char)
	return seat, err
}

// seatEveryone gives every character in the campaign without a seat one, in id order (the schedule is turned on).
func seatEveryone(tx *sql.Tx, lobby int) error {
	rows, err := tx.Query(`SELECT c.id FROM characters c WHERE c.lobby_id = $1
		AND NOT EXISTS (SELECT 1 FROM campaign_seats s WHERE s.lobby_id = c.lobby_id AND s.character_id = c.id) ORDER BY c.id`, lobby)
	if err != nil {
		return err
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := takeSeat(tx, lobby, id); err != nil {
			return err
		}
	}
	return nil
}

// compactSeats numbers the held seats 1, 2, ... in their order (join order kept) and drops the empty ones: a new capacity takes
// effect.
func compactSeats(tx *sql.Tx, lobby int) error {
	held, err := heldSeats(tx, lobby)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM campaign_seats WHERE lobby_id = $1`, lobby); err != nil {
		return err
	}
	for i, h := range held {
		if _, err := tx.Exec(`INSERT INTO campaign_seats (lobby_id, seat, character_id, joined_at) VALUES ($1, $2, $3, NOW())`, lobby, i+1, h.CharacterID); err != nil {
			return err
		}
	}
	return nil
}

// enterCampaign runs one claim in its own transaction (the admin's paths).
func enterCampaign(c seatClaim) (seatTaken, error) {
	tx, err := db.Begin()
	if err != nil {
		return seatTaken{}, err
	}
	defer tx.Rollback()
	out, err := claimSeat(tx, c)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// ---------------------------------------------------------------- a campaign's schedule

// campaignSchedule: a campaign's schedule as stored.
type campaignSchedule struct {
	Lobby      int
	State      string
	MaxPlayers int
	Pending    sql.NullInt64
	Seats      []seatHolder
}

// loadSchedule reads a campaign's schedule (sql.ErrNoRows: no such campaign).
func loadSchedule(q interface {
	rowQuerier
	rowsQuerier
}, lobby int) (campaignSchedule, error) {
	cs := campaignSchedule{Lobby: lobby}
	if err := q.QueryRow(`SELECT COALESCE(schedule_state, 'off'), COALESCE(max_players, 0), pending_capacity FROM lobbies WHERE id = $1`, lobby).
		Scan(&cs.State, &cs.MaxPlayers, &cs.Pending); err != nil {
		return cs, err
	}
	var err error
	cs.Seats, err = heldSeats(q, lobby)
	return cs, err
}

// seatsView: every seat of a capacity with its time and who holds it; seats held beyond the capacity are listed too.
func seatsView(s schedule, ok bool, capacity int, held []seatHolder) []map[string]interface{} {
	by := map[int]seatHolder{}
	last := capacity
	for _, h := range held {
		by[h.Seat] = h
		if h.Seat > last {
			last = h.Seat
		}
	}
	out := []map[string]interface{}{}
	for n := 1; n <= last; n++ {
		v := map[string]interface{}{"seat": n}
		if ok {
			if m, has := s.seatMinute(n); has {
				v["check_in"], v["minute"] = minuteText(m), m
			}
		}
		if h, has := by[n]; has {
			v["character_id"], v["name"] = h.CharacterID, h.Name
		} else if n <= capacity {
			v["open"] = true
		} else {
			continue
		}
		out = append(out, v)
	}
	return out
}

// timesView: the times of a capacity (the seats, the GM's check-ins, the final one).
func timesView(capacity int, held []seatHolder) map[string]interface{} {
	s, ok := scheduleOf(capacity)
	v := map[string]interface{}{"capacity": capacity, "seats": seatsView(s, ok, capacity, held)}
	if !ok {
		v["note"] = seatCapacityProblem(capacity) + " Set a capacity of 1 to 6 to give the seats times."
		return v
	}
	v["gm_check_ins"] = minutesText(s.GM)
	v["final_check_in"] = minuteText(s.final())
	v["gm_check_ins_per_hour"] = len(s.GM)
	v["minutes_between_seats"] = s.Interval
	return v
}

// scheduleView: a campaign's schedule as every reply shows it (GET /api/campaigns/{id}, GET|PATCH .../schedule, creation).
func scheduleView(cs campaignSchedule) map[string]interface{} {
	if !turnScheduleEnabled {
		return map[string]interface{}{"state": scheduleOff, "available": false,
			"note": "The turn schedule (check-in times for each seat) isn't available on this server yet: campaigns play without it for now."}
	}
	v := timesView(cs.MaxPlayers, cs.Seats)
	v["state"], v["state_means"], v["times_are"] = cs.State, scheduleStates[cs.State], scheduleTimesAre
	if cs.Pending.Valid && int(cs.Pending.Int64) != cs.MaxPlayers {
		p := timesView(int(cs.Pending.Int64), compacted(cs.Seats))
		p["takes_effect"] = "when the next round opens; the current round finishes on the times above"
		v["pending_capacity"] = p
	}
	v["change"] = fmt.Sprintf(`The GM changes it with PATCH /api/campaigns/%d/schedule {"capacity": N} (1 to %d seats) or {"state": "on" | "paused" | "off"}.`, cs.Lobby, maxScheduledSeats)
	return v
}

// compacted: the held seats as a new capacity will number them (join order kept).
func compacted(held []seatHolder) []seatHolder {
	out := make([]seatHolder, len(held))
	for i, h := range held {
		out[i] = h
		out[i].Seat = i + 1
	}
	return out
}

// seatReply: a character's seat as the join reply shows it.
func seatReply(cs campaignSchedule, seat int) map[string]interface{} {
	v := map[string]interface{}{"seat": seat, "schedule_state": cs.State}
	if cs.State == scheduleOff {
		v["note"] = "This campaign doesn't use the turn schedule: " + checkInNote
		return v
	}
	s, ok := scheduleOf(cs.MaxPlayers)
	if !ok {
		v["note"] = seatCapacityProblem(cs.MaxPlayers)
		return v
	}
	if m, has := s.seatMinute(seat); has {
		v["check_in"] = minuteText(m)
	}
	v["note"] = seatNote(s, seat)
	v["gm_check_ins"], v["final_check_in"] = minutesText(s.GM), minuteText(s.final())
	v["schedule"] = fmt.Sprintf("GET /api/campaigns/%d/schedule", cs.Lobby)
	return v
}

// ---------------------------------------------------------------- changing it (the GM)

// scheduleChange: what the GM asked to change (nil: unchanged).
type scheduleChange struct {
	Capacity *int    `json:"capacity"` // player seats, 1 to 6 (during a round it applies when the next round opens)
	State    *string `json:"state"`    // on, paused or off
}

// changeSchedule applies a change in tx (the campaign's row locked first) and returns what changed, in words for the log and
// the reply. A refusal is a *seatRefusal and changes nothing.
func changeSchedule(tx *sql.Tx, lobby int, ch scheduleChange) ([]string, error) {
	var state string
	var maxPlayers int
	var pending sql.NullInt64
	if err := tx.QueryRow(`SELECT COALESCE(schedule_state, 'off'), COALESCE(max_players, 0), pending_capacity FROM lobbies WHERE id = $1 FOR UPDATE`, lobby).
		Scan(&state, &maxPlayers, &pending); err != nil {
		return nil, err
	}
	now, err := campaignNow(tx, lobby)
	if err != nil {
		return nil, err
	}
	round, err := openRoundOf(tx, lobby)
	if err != nil {
		return nil, err
	}
	next := state
	if ch.State != nil {
		next = strings.ToLower(strings.TrimSpace(*ch.State))
		if _, known := scheduleStates[next]; !known {
			return nil, &seatRefusal{Status: http.StatusBadRequest, Code: "invalid_state", Message: `state is "on", "paused" or "off".`}
		}
		if next != scheduleOff && next != state && !turnScheduleEnabled {
			return nil, &seatRefusal{Status: http.StatusConflict, Code: "schedule_not_available", Message: scheduleNotAvailable}
		}
	}
	var done []string
	if ch.Capacity != nil {
		n := *ch.Capacity
		if p := seatCapacityProblem(n); p != "" {
			return nil, &seatRefusal{Status: http.StatusBadRequest, Code: "invalid_capacity", Message: p}
		}
		var seated int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM characters WHERE lobby_id = $1`, lobby).Scan(&seated); err != nil {
			return nil, err
		}
		if n < seated {
			return nil, &seatRefusal{Status: http.StatusConflict, Code: "capacity_below_players",
				Message: fmt.Sprintf("%d characters are seated; the capacity can't go below that. Remove a character first (POST /api/gm/kick-character).", seated)}
		}
		s, _ := scheduleOf(n)
		switch {
		case round != nil && next != scheduleOff && n != maxPlayers:
			if _, err := tx.Exec(`UPDATE lobbies SET pending_capacity = $1 WHERE id = $2`, n, lobby); err != nil {
				return nil, err
			}
			done = append(done, fmt.Sprintf("From round %d (when it opens) the campaign has %d seats: players check in at %s, the GM at %s.",
				round.Number+1, n, joinWords(minutesText(s.Seats)), joinWords(minutesText(s.GM))))
		case round != nil && n == maxPlayers && pending.Valid:
			if _, err := tx.Exec(`UPDATE lobbies SET pending_capacity = NULL WHERE id = $1`, lobby); err != nil {
				return nil, err
			}
			done = append(done, fmt.Sprintf("The capacity stays at %d seats (the change waiting for the next round is cancelled).", n))
		case round == nil || next == scheduleOff:
			if err := applyCapacity(tx, lobby, n); err != nil {
				return nil, err
			}
			maxPlayers = n
			done = append(done, fmt.Sprintf("The campaign has %d seats now: players check in at %s, the GM at %s.", n, joinWords(minutesText(s.Seats)), joinWords(minutesText(s.GM))))
		}
	}
	if next == state {
		return done, nil
	}
	if next != scheduleOff {
		if p := seatCapacityProblem(maxPlayers); p != "" {
			return nil, &seatRefusal{Status: http.StatusBadRequest, Code: "capacity_required", Message: p + ` Send "capacity" (1 to 6) with the state.`}
		}
	}
	switch {
	case state == scheduleOff: // on or paused: the schedule starts, with a round
		if err := seatEveryone(tx, lobby); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE lobbies SET schedule_state = $1 WHERE id = $2`, next, lobby); err != nil {
			return nil, err
		}
		kind, number, err := roundToOpen(tx, lobby)
		if err != nil {
			return nil, err
		}
		r, err := openRound(tx, lobby, kind, number, now)
		if err != nil {
			return nil, err
		}
		done = append(done, "The turn schedule is "+next+". "+r.opened())
		return done, nil
	case next == scheduleOff:
		if round != nil {
			if _, err := tx.Exec(`UPDATE campaign_rounds SET closed_at = $1 WHERE id = $2`, now, round.ID); err != nil {
				return nil, err
			}
		}
		if pending.Valid && ch.Capacity == nil {
			if err := applyCapacity(tx, lobby, int(pending.Int64)); err != nil {
				return nil, err
			}
		}
		done = append(done, "The turn schedule is off: the campaign plays as before, with no rounds, deadlines or stand-ins.")
	case next == schedulePaused:
		if round != nil {
			if _, err := tx.Exec(`UPDATE campaign_rounds SET deadline = NULL WHERE id = $1`, round.ID); err != nil {
				return nil, err
			}
		}
		done = append(done, "The turn schedule is paused: deadlines, reminders and stand-ins wait until the GM resumes it; submissions are still saved.")
	case next == scheduleOn: // resumed
		if round != nil {
			r := *round
			r.ClockFrom, r.Deadline = now, nullTime(roundDeadline(now))
			if _, err := tx.Exec(`UPDATE campaign_rounds SET clock_from = $1, deadline = $2 WHERE id = $3`, r.ClockFrom, r.Deadline, r.ID); err != nil {
				return nil, err
			}
			done = append(done, fmt.Sprintf("The turn schedule is on again. Round %d has a fresh deadline: its final check-in is %s, and every player has a check-in before it.", r.Number, clockText(r.Deadline.Time)))
		} else {
			done = append(done, "The turn schedule is on again.")
		}
	}
	if _, err := tx.Exec(`UPDATE lobbies SET schedule_state = $1 WHERE id = $2`, next, lobby); err != nil {
		return nil, err
	}
	return done, nil
}

// applyCapacity: the campaign has n seats from now (the seats keep their order).
func applyCapacity(tx *sql.Tx, lobby, n int) error {
	if _, err := tx.Exec(`UPDATE lobbies SET max_players = $1, pending_capacity = NULL WHERE id = $2`, n, lobby); err != nil {
		return err
	}
	return compactSeats(tx, lobby)
}

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }

// ---------------------------------------------------------------- the requests

// handleCampaignSchedule godoc
// @Summary A campaign's turn schedule: seats, check-in times and the round's status
// @Description GET (the GM and the campaign's players): the schedule. state: on (rounds run on the check-in times), paused (deadlines, reminders and stand-ins wait; submissions are still saved) or off (the campaign plays as before; a campaign made before the schedule existed is off until its GM turns it on). capacity: the seats, 1 to 6; seats: each seat's check-in time (minutes past every hour, UTC; 60/capacity minutes apart from :00, first come first served in join order) and who holds it; gm_check_ins: the GM's check-ins, 5 minutes before each next seat's time; final_check_in (:55): a round's final check-in, its deadline. pending_capacity: a new capacity the GM set during a round, with its times, taking effect when the next round opens. check_in: the open round and each character's status in it (only reads; never changes anything): round (number, combat or noncombat, final_check_in: the deadline, null while paused), due (whose action is due: a player, the GM for a monster's turn or a submitted turn, or an automatic turn), gm_overdue (a turn that could have resolved at a GM check-in more than 5 minutes ago), and for each character status: resolved, submitted (resolves at next_gm_check_in), waiting (submitted, waiting_on an earlier player in Initiative who hasn't submitted: not late), not_submitted (with check_in_at and window_until: the times are when a player is expected to look, not gates), needs_stand_in (the final check-in has come with no submission), automatic (a dying character's Death Saving Throw, or stable, dead or unconscious: no action to choose) or not_in_fight. A round opens when a fight starts (its number is the fight's round) or when the GM turns the schedule on (a noncombat round outside a fight). PATCH (the GM only): {"capacity": N} and/or {"state": "on" | "paused" | "off"}. A capacity change made while a round is open takes effect when the next round opens (shown at once as pending_capacity); with no round open, at once; it can't go below the characters seated; seats keep their join order. Turning the schedule on opens a round; pausing suspends the round's deadline; resuming gives it a fresh deadline (as if it opened then: that hour's :55 when resumed in the :00 minute, else the next hour's :55), so nobody is late for the paused time; turning it off closes the round. Every change is written to the campaign's log.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body PATCH: the change
// @Success 200 {object} map[string]interface{} "The schedule (and check_in)"
// @Failure 400 {object} map[string]interface{} "invalid_capacity, invalid_state, capacity_required, schedule_change_required"
// @Failure 403 {object} map[string]interface{} "not_gm (PATCH), not_in_campaign (GET)"
// @Failure 409 {object} map[string]interface{} "capacity_below_players"
// @Router /campaigns/{id}/schedule [get]
// @Router /campaigns/{id}/schedule [patch]
func handleCampaignSchedule(w http.ResponseWriter, r *http.Request, lobby int) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if _, _, ok := requireCampaignMember(w, r, lobby, "see the turn schedule"); !ok {
			return
		}
		reply, err := scheduleReply(lobby)
		if answerDBFailure(w, err, "the campaign's schedule") {
			return
		}
		json.NewEncoder(w).Encode(reply)
	case http.MethodPatch:
		if _, ok := requireCampaignGM(w, r, lobby, "change the turn schedule"); !ok {
			return
		}
		var ch scheduleChange
		if !readRequest(w, r, &ch) {
			return
		}
		if ch.Capacity == nil && ch.State == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "schedule_change_required",
				"message": `Send "capacity" (1 to 6 seats) and/or "state" ("on", "paused" or "off"). Nothing was changed.`})
			return
		}
		tx, err := db.Begin()
		if answerDBFailure(w, err, "the campaign's schedule") {
			return
		}
		defer tx.Rollback()
		done, err := changeSchedule(tx, lobby, ch)
		if answerSeatProblem(w, err) {
			return
		}
		for _, d := range done {
			if err := writeAction(tx, lobby, 0, "schedule", d, ""); err != nil {
				writeDBError(w, err, "the campaign's log")
				return
			}
		}
		if err := tx.Commit(); answerDBFailure(w, err, "the campaign's schedule") {
			return
		}
		reply, err := scheduleReply(lobby)
		if answerDBFailure(w, err, "the campaign's schedule") {
			return
		}
		reply["success"] = true
		if len(done) == 0 {
			done = []string{"Nothing changed: the schedule was already so."}
		}
		reply["changed"] = done
		json.NewEncoder(w).Encode(reply)
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET or PATCH."})
	}
}

// scheduleReply: GET .../schedule: the schedule and the open round's status.
func scheduleReply(lobby int) (map[string]interface{}, error) {
	cs, err := loadSchedule(db, lobby)
	if err != nil {
		return nil, err
	}
	in, now, err := loadCheckIn(lobby)
	if err != nil {
		return nil, err
	}
	v := scheduleView(cs)
	v["campaign_id"] = lobby
	v["check_in"] = checkInAt(in, now)
	return v, nil
}

// handleCampaignTestClock godoc
// @Summary A campaign's test clock (test servers only)
// @Description Only on a server started with AGENTRPG_TEST_CLOCK=1 (a test server; never live); elsewhere 404 test_clock_off. The turn schedule reads this campaign's clock instead of real time, so a test can play an hour in minutes. GET (the GM and the players): the campaign's time now. POST (the GM only): {"at": "2026-11-16T10:00:00Z"} sets it (RFC3339), {"advance_minutes": N} moves it forward. The clock keeps running from there; times other tables recorded before a change keep the campaign time they had.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body POST: the new time, or minutes to move it
// @Success 200 {object} map[string]interface{} "now: the campaign's time"
// @Failure 404 {object} map[string]interface{} "test_clock_off"
// @Router /campaigns/{id}/test-clock [get]
// @Router /campaigns/{id}/test-clock [post]
func handleCampaignTestClock(w http.ResponseWriter, r *http.Request, lobby int) {
	w.Header().Set("Content-Type", "application/json")
	if !testClockOn() {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "test_clock_off", "message": "This server has no test clock (only a test server started with " + testClockEnv + "=1 has one)."})
		return
	}
	if r.Method == http.MethodPost {
		if _, ok := requireCampaignGM(w, r, lobby, "move the test clock"); !ok {
			return
		}
		var req struct {
			At             string `json:"at"`              // set the campaign's clock to this time (RFC3339)
			AdvanceMinutes int    `json:"advance_minutes"` // move the campaign's clock forward this many minutes
		}
		if !readRequest(w, r, &req) {
			return
		}
		now, err := campaignNow(db, lobby)
		if answerDBFailure(w, err, "the campaign's clock") {
			return
		}
		var to time.Time
		switch {
		case req.At != "" && req.AdvanceMinutes == 0:
			at, err := time.Parse(time.RFC3339, strings.TrimSpace(req.At))
			if err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_time", "message": "at is a time in RFC3339 form, such as 2026-11-16T10:00:00Z. Nothing was changed."})
				return
			}
			to = at
		case req.At == "" && req.AdvanceMinutes > 0:
			to = now.Add(time.Duration(req.AdvanceMinutes) * time.Minute)
		default:
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "at_or_advance_minutes", "message": `Send "at" (RFC3339) or "advance_minutes" (above 0). Nothing was changed.`})
			return
		}
		// stored to the microsecond (Postgres): the real moment is rounded down, so the clock reads at least the time set
		if _, err := db.Exec(`INSERT INTO campaign_test_clock (lobby_id, real_at, campaign_at) VALUES ($1, $2, $3)`, lobby, time.Now().UTC().Truncate(time.Microsecond), to.UTC()); err != nil {
			writeDBError(w, err, "the campaign's clock")
			return
		}
	} else if _, _, ok := requireCampaignMember(w, r, lobby, "read the test clock"); !ok {
		return
	}
	now, err := campaignNow(db, lobby)
	if answerDBFailure(w, err, "the campaign's clock") {
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"campaign_id": lobby, "now": now.Format(time.RFC3339)})
}

// logScheduleProblem: a hook (a fight starting or ending, a new round) couldn't update the round record.
func logScheduleProblem(lobby int, what string, err error) {
	if err != nil {
		log.Printf("turn schedule: campaign %d: %s: %v", lobby, what, err)
	}
}
