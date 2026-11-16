package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// scheduleTestDB: a schema of its own in TEST_DATABASE_URL with the tables the seats and rounds use (skipped without one).
func scheduleTestDB(t *testing.T) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := sql.Open("postgres", raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("sched_test_%d_%d", os.Getpid(), time.Now().UnixNano()%1e6)
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	database, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(20)
	original := db
	db = database
	t.Cleanup(func() {
		db = original
		database.Close()
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})
	if _, err := db.Exec(`
		CREATE TABLE lobbies (id SERIAL PRIMARY KEY, name TEXT DEFAULT '', max_players INTEGER DEFAULT 4, status TEXT DEFAULT 'recruiting');
		CREATE TABLE characters (id SERIAL PRIMARY KEY, agent_id INTEGER, lobby_id INTEGER, name TEXT NOT NULL);
		CREATE TABLE actions (id SERIAL PRIMARY KEY, lobby_id INTEGER, character_id INTEGER, action_type TEXT, description TEXT, result TEXT, created_at TIMESTAMP DEFAULT NOW());
		CREATE TABLE combat_state (lobby_id INTEGER UNIQUE, round_number INTEGER, current_turn_index INTEGER, turn_order JSONB, turn_started_at TIMESTAMP,
			last_monster_id INTEGER, turn_claim TEXT, turn_claimed_at TIMESTAMP, active BOOLEAN);`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(turnScheduleSchema); err != nil {
		t.Fatal(err)
	}
}

func newTestCampaign(t *testing.T, capacity int, state string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO lobbies (max_players, schedule_state) VALUES ($1, $2) RETURNING id`, capacity, state).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newTestCharacter(t *testing.T, agent int, name string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO characters (agent_id, name) VALUES ($1, $2) RETURNING id`, agent, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func joinTest(t *testing.T, lobby, char, agent int) (seatTaken, error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	out, err := claimSeat(tx, seatClaim{Lobby: lobby, Character: char, Owner: agent, OnePerPlayer: true})
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// Parallel joins for the last seat: exactly one wins, the rest are campaign_full, and the seats are 1..capacity.
func TestSeatRaceForTheLastSeat(t *testing.T) {
	scheduleTestDB(t)
	for round := 0; round < 5; round++ {
		lobby := newTestCampaign(t, 4, scheduleOn)
		for i := 0; i < 3; i++ {
			c := newTestCharacter(t, 100+i, fmt.Sprintf("Seated %d-%d", round, i))
			if s, err := joinTest(t, lobby, c, 100+i); err != nil || s.Seat != i+1 {
				t.Fatalf("join %d: seat %d, %v", i, s.Seat, err)
			}
		}
		const racers = 8
		var wg sync.WaitGroup
		results := make([]error, racers)
		for i := 0; i < racers; i++ {
			c := newTestCharacter(t, 200+i, fmt.Sprintf("Racer %d-%d", round, i))
			wg.Add(1)
			go func(i, c int) {
				defer wg.Done()
				_, results[i] = joinTest(t, lobby, c, 200+i)
			}(i, c)
		}
		wg.Wait()
		won := 0
		for _, err := range results {
			var ref *seatRefusal
			switch {
			case err == nil:
				won++
			case errors.As(err, &ref) && ref.Code == "campaign_full":
			default:
				t.Errorf("a racer failed otherwise: %v", err)
			}
		}
		var members int
		db.QueryRow(`SELECT COUNT(*) FROM characters WHERE lobby_id = $1`, lobby).Scan(&members)
		held, _ := heldSeats(db, lobby)
		if won != 1 || members != 4 || len(held) != 4 || held[3].Seat != 4 {
			t.Errorf("race %d: %d won, %d members, seats %+v", round, won, members, held)
		}
	}
}

// One character per player, a free seat's time for the next player, and a character that leaves frees its seat.
func TestSeatsFirstComeFirstServed(t *testing.T) {
	scheduleTestDB(t)
	lobby := newTestCampaign(t, 4, scheduleOn)
	a := newTestCharacter(t, 1, "A")
	a2 := newTestCharacter(t, 1, "A2")
	b := newTestCharacter(t, 2, "B")
	c := newTestCharacter(t, 3, "C")
	d := newTestCharacter(t, 4, "D")
	for _, x := range []struct{ char, agent, seat int }{{a, 1, 1}, {b, 2, 2}, {c, 3, 3}} {
		if s, err := joinTest(t, lobby, x.char, x.agent); err != nil || s.Seat != x.seat {
			t.Fatalf("seat %d: %+v %v", x.seat, s, err)
		}
	}
	var ref *seatRefusal
	if _, err := joinTest(t, lobby, a2, 1); !errors.As(err, &ref) || ref.Code != "one_character_per_player" {
		t.Errorf("a second character of a player: %v", err)
	}
	if s, err := joinTest(t, lobby, a, 1); err != nil || !s.Already || s.Seat != 1 {
		t.Errorf("joining again: %+v %v", s, err)
	}
	db.Exec(`UPDATE characters SET lobby_id = NULL WHERE id = $1`, b) // B leaves (kicked): seat 2 is free
	if s, err := joinTest(t, lobby, d, 4); err != nil || s.Seat != 2 {
		t.Errorf("the next player takes the free seat 2: %+v %v", s, err)
	}
	if _, err := joinTest(t, lobby, d, 9); !errors.As(err, &ref) || ref.Code != "character_not_found" {
		t.Errorf("someone else's character: %v", err)
	}
}

func changeTest(t *testing.T, lobby int, capacity *int, state string) ([]string, error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ch := scheduleChange{Capacity: capacity}
	if state != "" {
		ch.State = &state
	}
	done, err := changeSchedule(tx, lobby, ch)
	if err != nil {
		return done, err
	}
	return done, tx.Commit()
}

func intp(n int) *int { return &n }

// Turning the schedule on opens a noncombat round; a capacity change during it waits for the next round; pausing suspends the
// deadline and resuming gives a fresh one; the next round applies the new capacity, keeping join order.
func TestScheduleChanges(t *testing.T) {
	scheduleTestDB(t)
	lobby := newTestCampaign(t, 8, scheduleOff) // an old campaign: off, 8 seats
	var chars []int
	for i := 0; i < 3; i++ {
		c := newTestCharacter(t, 10+i, fmt.Sprintf("P%d", i))
		chars = append(chars, c)
		db.Exec(`UPDATE characters SET lobby_id = $1 WHERE id = $2`, lobby, c) // joined before seats existed
	}
	var ref *seatRefusal
	if _, err := changeTest(t, lobby, nil, scheduleOn); !errors.As(err, &ref) || ref.Code != "capacity_required" {
		t.Fatalf("on with 8 seats: %v", err)
	}
	if _, err := changeTest(t, lobby, intp(2), ""); !errors.As(err, &ref) || ref.Code != "capacity_below_players" {
		t.Fatalf("below the players: %v", err)
	}
	if _, err := changeTest(t, lobby, intp(4), scheduleOn); err != nil {
		t.Fatal(err)
	}
	cs, _ := loadSchedule(db, lobby)
	r, _ := openRoundOf(db, lobby)
	if cs.State != scheduleOn || cs.MaxPlayers != 4 || len(cs.Seats) != 3 || r == nil || r.Kind != roundNoncombat || r.Number != 1 || !r.Deadline.Valid {
		t.Fatalf("on: %+v round %+v", cs, r)
	}
	if !r.Deadline.Time.Equal(roundDeadline(r.OpenedAt)) {
		t.Errorf("deadline %v for a round opened %v", r.Deadline.Time, r.OpenedAt)
	}
	// a capacity change during the round waits for the next one
	if _, err := changeTest(t, lobby, intp(6), ""); err != nil {
		t.Fatal(err)
	}
	cs, _ = loadSchedule(db, lobby)
	if cs.MaxPlayers != 4 || !cs.Pending.Valid || cs.Pending.Int64 != 6 {
		t.Errorf("pending: %+v", cs)
	}
	// pause, then resume: a fresh deadline
	if _, err := changeTest(t, lobby, nil, schedulePaused); err != nil {
		t.Fatal(err)
	}
	if r, _ := openRoundOf(db, lobby); r == nil || r.Deadline.Valid {
		t.Errorf("paused round %+v", r)
	}
	if _, err := changeTest(t, lobby, nil, scheduleOn); err != nil {
		t.Fatal(err)
	}
	r2, _ := openRoundOf(db, lobby)
	if r2 == nil || r2.ID != r.ID || !r2.Deadline.Valid || r2.ClockFrom.Before(r.ClockFrom) || !r2.Deadline.Time.Equal(roundDeadline(r2.ClockFrom)) {
		t.Errorf("resumed round %+v (was %+v)", r2, r)
	}
	// B leaves: seats 1 and 3 held; the next round opens with 6 seats, numbered in join order
	db.Exec(`UPDATE characters SET lobby_id = NULL WHERE id = $1`, chars[1])
	if err := roundOpens(lobby, roundNoncombat, 0, false, ""); err != nil {
		t.Fatal(err)
	}
	cs, _ = loadSchedule(db, lobby)
	r3, _ := openRoundOf(db, lobby)
	if cs.MaxPlayers != 6 || cs.Pending.Valid || len(cs.Seats) != 2 || cs.Seats[0].CharacterID != chars[0] || cs.Seats[1].Seat != 2 || cs.Seats[1].CharacterID != chars[2] {
		t.Errorf("the new capacity at the next round: %+v", cs)
	}
	if r3 == nil || r3.Number != 2 || r3.ID == r.ID {
		t.Errorf("next round %+v", r3)
	}
	var open int
	db.QueryRow(`SELECT COUNT(*) FROM campaign_rounds WHERE lobby_id = $1 AND closed_at IS NULL`, lobby).Scan(&open)
	if open != 1 {
		t.Errorf("%d open rounds", open)
	}
	// off: the round closes
	if _, err := changeTest(t, lobby, nil, scheduleOff); err != nil {
		t.Fatal(err)
	}
	if r, _ := openRoundOf(db, lobby); r != nil {
		t.Errorf("off with a round open: %+v", r)
	}
	// with no round open, a capacity change takes effect at once
	if _, err := changeTest(t, lobby, intp(3), ""); err != nil {
		t.Fatal(err)
	}
	if cs, _ = loadSchedule(db, lobby); cs.MaxPlayers != 3 || cs.Pending.Valid {
		t.Errorf("at once: %+v", cs)
	}
}

// ---------------------------------------------------------------- source checks

// claimSeat is the one way into a campaign: no other code sets a character's campaign (to anything but none).
func TestNoOtherWayIntoACampaign(t *testing.T) {
	sets := regexp.MustCompile("(?is)UPDATE\\s+characters\\s+SET\\s+([^`\"]*?)(\\bWHERE\\b|`|\")")
	assigns := regexp.MustCompile(`(?i)\blobby_id\s*=\s*\$`)
	inserts := regexp.MustCompile("(?is)INSERT\\s+INTO\\s+characters\\s*\\([^)]*\\blobby_id\\b")
	seats := regexp.MustCompile("(?is)(INSERT\\s+INTO|UPDATE|DELETE\\s+FROM)\\s+campaign_seats\\b")
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "schedule_2024.go" || f == "database_init.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range sets.FindAllStringSubmatch(string(b), -1) {
			if assigns.MatchString(m[1]) {
				t.Errorf("%s puts a character into a campaign itself: %q; use claimSeat (schedule_2024.go)", f, m[0])
			}
		}
		for _, rx := range []*regexp.Regexp{inserts, seats} {
			if m := rx.FindString(string(b)); m != "" {
				t.Errorf("%s puts a character into a campaign (or writes a seat) itself: %q; use claimSeat (schedule_2024.go)", f, m)
			}
		}
	}
}

// The schedule reads one clock: no time.Now in its owners except the clock itself.
func TestScheduleReadsOneClock(t *testing.T) {
	for _, f := range []string{"schedule_2024.go", "check_in_2024.go"} {
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "time.Now()") && !strings.Contains(line, "func (c campaignClock) now()") && !strings.Contains(line, "INSERT INTO campaign_test_clock") {
				t.Errorf("%s:%d reads time.Now outside the campaign clock: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
