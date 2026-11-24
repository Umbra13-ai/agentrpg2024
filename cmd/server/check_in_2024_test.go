package main

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

func TestScheduleOf(t *testing.T) {
	want := map[int]struct{ seats, gm string }{
		1: {":00", ":55"},
		2: {":00 :30", ":25 :55"},
		3: {":00 :20 :40", ":15 :35 :55"},
		4: {":00 :15 :30 :45", ":10 :25 :40 :55"},
		5: {":00 :12 :24 :36 :48", ":07 :19 :31 :43 :55"},
		6: {":00 :10 :20 :30 :40 :50", ":05 :15 :25 :35 :45 :55"},
	}
	for n, w := range want {
		s, ok := scheduleOf(n)
		if !ok {
			t.Fatalf("scheduleOf(%d) refused", n)
		}
		if got := strings.Join(minutesText(s.Seats), " "); got != w.seats {
			t.Errorf("%d seats: players %s, want %s", n, got, w.seats)
		}
		if got := strings.Join(minutesText(s.GM), " "); got != w.gm {
			t.Errorf("%d seats: GM %s, want %s", n, got, w.gm)
		}
		if s.final() != finalMinute || s.Interval*n != 60 {
			t.Errorf("%d seats: final :%d, interval %d", n, s.final(), s.Interval)
		}
		if seatCapacityProblem(n) != "" {
			t.Errorf("%d seats refused by the check", n)
		}
	}
	for _, n := range []int{-1, 0, 7, 8, 12} {
		if _, ok := scheduleOf(n); ok || seatCapacityProblem(n) == "" {
			t.Errorf("%d seats allowed", n)
		}
	}
	table := scheduleTable()
	for n := 1; n <= maxScheduledSeats; n++ {
		s, _ := scheduleOf(n)
		if !strings.Contains(table, strings.Join(minutesText(s.GM), " ")) {
			t.Errorf("the GM guide's table lacks the %d-seat GM times", n)
		}
	}
	if g := withScheduleTable("a\n" + scheduleTableMarker + "\nb"); strings.Contains(g, scheduleTableMarker) || !strings.Contains(g, "| 4 | :00 :15 :30 :45 |") {
		t.Errorf("the table isn't written at its marker: %s", g)
	}
}

// The served GM guide carries the table where docs/gm_guide.md has its marker.
func TestGMGuideShowsTheScheduleTable(t *testing.T) {
	g := gmGuide()
	if strings.Contains(g, scheduleTableMarker) || !strings.Contains(g, scheduleTable()) {
		t.Error("the GM guide doesn't show the schedule table from scheduleOf")
	}
}

func at(hhmm string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", "2026-11-16 "+hhmm)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestRoundDeadline(t *testing.T) {
	// every minute (and a second into it) across two hours
	for m := 0; m < 120; m++ {
		for _, sec := range []int{0, 1, 59} {
			opened := at("10:00:00").Add(time.Duration(m)*time.Minute + time.Duration(sec)*time.Second)
			want := opened.Truncate(time.Hour).Add(time.Hour + 55*time.Minute)
			if opened.Minute() == 0 {
				want = opened.Truncate(time.Hour).Add(55 * time.Minute)
			}
			if got := roundDeadline(opened); !got.Equal(want) {
				t.Errorf("opened %s: deadline %s, want %s", opened.Format("15:04:05"), got.Format("15:04"), want.Format("15:04"))
			}
		}
	}
	for opened, want := range map[string]string{"10:00:00": "10:55", "10:00:59": "10:55", "10:25:00": "11:55", "10:55:00": "11:55", "10:55:30": "11:55", "10:59:59": "11:55"} {
		if got := roundDeadline(at(opened)).Format("15:04"); got != want {
			t.Errorf("opened %s: deadline %s, want %s", opened, got, want)
		}
	}
}

func TestTurnNeedOf(t *testing.T) {
	cases := []struct {
		v      game.Vitals
		uncon  string
		kind   string
		reason string
	}{
		{game.Vitals{HP: 10, Max: 10}, "", needChoose, ""},
		{game.Vitals{HP: 0, Max: 10}, "unconscious", needDeathSave, "dying"},
		{game.Vitals{HP: 0, Max: 10, Failures: 2}, "unconscious", needDeathSave, "dying"},
		{game.Vitals{HP: 0, Max: 10, Stable: true}, "unconscious", needNothing, "stable"},
		{game.Vitals{HP: 0, Max: 10, Dead: true}, "unconscious", needNothing, "dead"},
		{game.Vitals{HP: 7, Max: 10}, "unconscious", needNothing, "unconscious"},
		{game.Vitals{HP: 7, Max: 10}, "paralyzed", needNothing, "paralyzed"},
	}
	for _, c := range cases {
		if got := turnNeedOf(c.v, c.uncon); got.Kind != c.kind || got.Why != c.reason {
			t.Errorf("%+v can't act=%q: %+v, want %s/%s", c.v, c.uncon, got, c.kind, c.reason)
		}
	}
}

// The design's example table (turn-schedule-design.md 1.7): 4 seats, Ann :00, Ben :15, Cara :30, Dev :45; GM :10 :25 :40 :55.
// Initiative: Goblin 18, Cara 15, Ann 12, Ogre 10, Ben 8, Dev 5.
const (
	ann, ben, cara, dev = 1, 2, 3, 4
)

func exampleInputs(opened time.Time) checkInInputs {
	s, _ := scheduleOf(4)
	return checkInInputs{
		State: scheduleOn, Sched: s, Timed: true,
		Seats: []seatHolder{{1, ann, "Ann"}, {2, ben, "Ben"}, {3, cara, "Cara"}, {4, dev, "Dev"}},
		Round: &roundRecord{ID: 1, Number: 1, Kind: roundCombat, OpenedAt: opened, ClockFrom: opened, Deadline: nullTime(roundDeadline(opened))},
		Fight: &checkInFight{Round: 1, Current: 0, SlotSince: opened, Slots: []checkInSlot{
			{-1, "Goblin", false}, {cara, "Cara", true}, {ann, "Ann", true}, {-2, "Ogre", false}, {ben, "Ben", true}, {dev, "Dev", true}}},
		Turns: map[int]charTurn{ann: {Needs: turnNeed{Kind: needChoose}}, ben: {Needs: turnNeed{Kind: needChoose}},
			cara: {Needs: turnNeed{Kind: needChoose}}, dev: {Needs: turnNeed{Kind: needChoose}}},
	}
}

func submit(in *checkInInputs, id int, when time.Time) {
	t := in.Turns[id]
	t.Submitted, t.SubmittedAt = true, when
	in.Turns[id] = t
}

func statusOfChar(t *testing.T, c checkIn, id int) charStatus {
	t.Helper()
	for _, s := range c.Characters {
		if s.CharacterID == id {
			return s
		}
	}
	t.Fatalf("no status for %d", id)
	return charStatus{}
}

func expect(t *testing.T, when string, c checkIn, id int, status string, msgHas string) {
	t.Helper()
	s := statusOfChar(t, c, id)
	if s.Status != status || (msgHas != "" && !strings.Contains(s.Message, msgHas)) {
		t.Errorf("%s: %s is %s (%q), want %s with %q", when, s.Name, s.Status, s.Message, status, msgHas)
	}
	for _, bad := range []string{"blocked", "missing"} {
		if strings.Contains(strings.ToLower(s.Message), bad) {
			t.Errorf("%s: %s's status says %q: %s", when, s.Name, bad, s.Message)
		}
	}
}

func TestCheckInNormalRound(t *testing.T) {
	in := exampleInputs(at("09:55:00")) // opened when the previous round closed: final check-in 10:55
	if in.Round.Deadline.Time != at("10:55:00") {
		t.Fatal("deadline")
	}
	submit(&in, ann, at("10:00:20"))
	c := checkInAt(in, at("10:00:30"))
	expect(t, "10:00", c, ann, statusWaiting, "waiting on Cara")
	if s := statusOfChar(t, c, ann); s.WaitingOn == nil || s.WaitingOn.Name != "Cara" {
		t.Errorf("Ann's waiting_on: %+v", s.WaitingOn)
	}
	if c.Due == nil || c.Due.Kind != "gm" || c.GMOverdue != nil {
		t.Errorf("10:00: due %+v, overdue %+v", c.Due, c.GMOverdue)
	}
	if c.NextGMCheckIn == nil || !c.NextGMCheckIn.Equal(at("10:10:00")) {
		t.Errorf("10:00: next GM check-in %v", c.NextGMCheckIn)
	}
	if len(c.GMCheckIns) != 4 || !c.GMCheckIns[0].Equal(at("10:10:00")) || !c.GMCheckIns[3].Equal(at("10:55:00")) {
		t.Errorf("the round's GM check-ins: %v", c.GMCheckIns)
	}
	// 10:10 the GM plays the Goblin; Cara is next with no submission
	in.Fight.Current, in.Fight.SlotSince = 1, at("10:10:20")
	c = checkInAt(in, at("10:12:00"))
	expect(t, "10:12", c, cara, statusNotSubmitted, "check-in is at 10:30")
	expect(t, "10:12", c, ann, statusWaiting, "Cara")
	if c.Due.Kind != "player" || c.Due.Characters[0].Name != "Cara" {
		t.Errorf("10:12: due %+v", c.Due)
	}
	submit(&in, ben, at("10:15:05"))
	c = checkInAt(in, at("10:26:00")) // 10:25: nothing can resolve: not GM-overdue
	expect(t, "10:26", c, ben, statusWaiting, "Cara")
	if c.GMOverdue != nil {
		t.Errorf("10:26: a turn waiting on an earlier player is GM-overdue: %+v", c.GMOverdue)
	}
	submit(&in, cara, at("10:30:10"))
	c = checkInAt(in, at("10:31:00"))
	expect(t, "10:31", c, cara, statusSubmitted, "10:40")
	expect(t, "10:31", c, ann, statusSubmitted, "10:40")
	expect(t, "10:31", c, ben, statusSubmitted, "") // the Ogre before him is the GM's, not a player he waits on
	expect(t, "10:31", c, dev, statusNotSubmitted, "check-in is at 10:45")
	if c.GMOverdue != nil {
		t.Error("10:31: overdue before the 10:40 check-in")
	}
	if c = checkInAt(in, at("10:45:00")); c.GMOverdue == nil || c.GMOverdue.Turn != "Cara" || !c.GMOverdue.Since.Equal(at("10:45:00")) {
		t.Errorf("10:45 with no GM: overdue %+v", c.GMOverdue)
	}
	// 10:40 the GM resolves Cara, Ann, the Ogre and Ben; Dev has no submission
	in.Fight.Current, in.Fight.SlotSince = 5, at("10:40:40")
	c = checkInAt(in, at("10:41:00"))
	for _, id := range []int{ann, ben, cara} {
		expect(t, "10:41", c, id, statusResolved, "")
	}
	expect(t, "10:41", c, dev, statusNotSubmitted, "check-in is at 10:45")
	c = checkInAt(in, at("10:47:00"))
	expect(t, "10:47", c, dev, statusNotSubmitted, "window is open until 10:55")
	c = checkInAt(in, at("10:55:00"))
	expect(t, "10:55", c, dev, statusStandIn, "stand-in")
	if !c.Round.FinalReached {
		t.Error("10:55: the final check-in hasn't come")
	}
}

func TestCheckInLateGM(t *testing.T) {
	in := exampleInputs(at("09:55:00"))
	if c := checkInAt(in, at("10:14:59")); c.GMOverdue != nil {
		t.Error("overdue before 10:15")
	}
	c := checkInAt(in, at("10:15:00"))
	if c.GMOverdue == nil || c.GMOverdue.Turn != "Goblin" || !c.GMOverdue.CheckIn.Equal(at("10:10:00")) {
		t.Fatalf("the Goblin's turn isn't GM-overdue from 10:15: %+v", c.GMOverdue)
	}
	submit(&in, ann, at("10:00:00"))
	submit(&in, ben, at("10:15:00"))
	submit(&in, cara, at("10:30:00"))
	c = checkInAt(in, at("10:47:00")) // the GM comes at 10:47: nobody is missing, nobody needs a stand-in yet
	for _, s := range c.Characters {
		if s.Status == statusStandIn || s.Status == statusNotSubmitted && s.CharacterID != dev {
			t.Errorf("10:47: %s is %s", s.Name, s.Status)
		}
	}
	expect(t, "10:47", c, dev, statusNotSubmitted, "window")
	if c.GMOverdue == nil || c.GMOverdue.Turn != "Goblin" {
		t.Errorf("10:47: the delay isn't the GM's: %+v", c.GMOverdue)
	}
	// at the final check-in only the character still without a submission is flagged; resolved ones never are
	in.Fight.Current, in.Fight.SlotSince = 5, at("10:47:30")
	c = checkInAt(in, at("10:55:00"))
	expect(t, "10:55", c, dev, statusStandIn, "")
	for _, id := range []int{ann, ben, cara} {
		expect(t, "10:55", c, id, statusResolved, "")
	}
}

func TestCheckInEarlyCloseAndMidHourStart(t *testing.T) {
	// everyone submitted by 10:20; the GM resolved the round at 10:25 and round 2 opened then: final check-in 11:55
	in := exampleInputs(at("10:25:10"))
	in.Round.Number, in.Fight.Round = 2, 2
	if !in.Round.Deadline.Time.Equal(at("11:55:00")) {
		t.Fatal("round 2's deadline")
	}
	c := checkInAt(in, at("10:26:00"))
	expect(t, "10:26", c, dev, statusNotSubmitted, "10:45")
	expect(t, "10:26", c, ann, statusNotSubmitted, "11:00")
	expect(t, "10:26", c, cara, statusNotSubmitted, "10:30") // the first :30 after the round opened
	// combat starts mid-hour (10:32): each player gets one check-in before 11:55; stand-ins only at 11:55
	in = exampleInputs(at("10:32:00"))
	for id, want := range map[int]string{dev: "10:45", ann: "11:00", ben: "11:15", cara: "11:30"} {
		if s := statusOfChar(t, checkInAt(in, at("10:33:00")), id); s.CheckInAt == nil || s.CheckInAt.Format("15:04") != want {
			t.Errorf("mid-hour: %s's check-in %v, want %s", s.Name, s.CheckInAt, want)
		}
	}
	c = checkInAt(in, at("11:54:00"))
	expect(t, "11:54", c, dev, statusNotSubmitted, "until the round's final check-in at 11:55")
	c = checkInAt(in, at("11:55:00"))
	expect(t, "11:55", c, dev, statusStandIn, "")
	// the round still closes early when everyone has submitted: nobody is flagged however late the GM is
	for _, id := range []int{ann, ben, cara, dev} {
		submit(&in, id, at("10:50:00"))
	}
	for _, s := range checkInAt(in, at("11:55:00")).Characters {
		if s.Status == statusStandIn {
			t.Errorf("%s needs a stand-in though it submitted", s.Name)
		}
	}
}

func TestCheckInDyingCharacter(t *testing.T) {
	in := exampleInputs(at("09:55:00"))
	in.Turns[ben] = charTurn{Needs: turnNeed{needDeathSave, "dying"}}
	in.Turns[dev] = charTurn{Needs: turnNeed{needNothing, "stable"}}
	in.Fight.Current = 4 // Ben's turn
	c := checkInAt(in, at("10:56:00"))
	expect(t, "dying", c, ben, statusAutomatic, "Death Saving Throw")
	if s := statusOfChar(t, c, ben); s.Automatic != needDeathSave {
		t.Errorf("Ben automatic %q", s.Automatic)
	}
	expect(t, "stable", c, dev, statusAutomatic, "Stable")
	if c.Due.Kind != "automatic" {
		t.Errorf("due %+v", c.Due)
	}
	// nobody waits on a dying character: Dev's submission behind Ben is not waiting on him
	in.Turns[dev] = charTurn{Needs: turnNeed{Kind: needChoose}}
	submit(&in, dev, at("10:00:00"))
	expect(t, "behind a dying character", checkInAt(in, at("10:01:00")), dev, statusSubmitted, "")
}

func TestCheckInPauseResume(t *testing.T) {
	in := exampleInputs(at("09:55:00"))
	in.State, in.Round.Deadline = schedulePaused, sql.NullTime{}
	c := checkInAt(in, at("12:00:00")) // long past the old deadline: nobody is late while paused
	for _, s := range c.Characters {
		if s.Status == statusStandIn {
			t.Errorf("paused: %s needs a stand-in", s.Name)
		}
	}
	expect(t, "paused", c, ann, statusNotSubmitted, "paused")
	if c.GMOverdue != nil || c.Round.FinalCheckIn != nil || len(c.GMCheckIns) != 0 {
		t.Errorf("paused: overdue %+v, final %v, GM check-ins %v", c.GMOverdue, c.Round.FinalCheckIn, c.GMCheckIns)
	}
	// resumed at 12:40:30: a fresh deadline (13:55) and fresh check-ins
	in.State, in.Round.ClockFrom, in.Round.Deadline = scheduleOn, at("12:40:30"), nullTime(roundDeadline(at("12:40:30")))
	c = checkInAt(in, at("12:41:00"))
	expect(t, "resumed", c, dev, statusNotSubmitted, "12:45")
	expect(t, "resumed", c, ann, statusNotSubmitted, "13:00")
	if c.GMOverdue != nil {
		t.Errorf("resumed: the GM is overdue at once: %+v", c.GMOverdue)
	}
	// the Goblin's turn could resolve from the resume on: the first GM check-in after it is 12:55, so overdue from 13:00
	if c := checkInAt(in, at("12:59:00")); c.GMOverdue != nil {
		t.Errorf("resumed: overdue at 12:59 %+v", c.GMOverdue)
	}
	if c := checkInAt(in, at("13:00:00")); c.GMOverdue == nil || !c.GMOverdue.CheckIn.Equal(at("12:55:00")) {
		t.Errorf("resumed: overdue at 13:00 %+v", c.GMOverdue)
	}
}

func TestCheckInNoncombat(t *testing.T) {
	in := exampleInputs(at("10:00:10"))
	in.Fight, in.Round.Kind = nil, roundNoncombat
	c := checkInAt(in, at("10:05:00"))
	expect(t, "noncombat", c, ann, statusNotSubmitted, "window is open until 10:10")
	expect(t, "noncombat", c, ben, statusNotSubmitted, "10:15")
	if c.Due.Kind != "players" || len(c.Due.Characters) != 4 {
		t.Errorf("noncombat due %+v", c.Due)
	}
	c = checkInAt(in, at("10:55:00"))
	expect(t, "noncombat final", c, cara, statusStandIn, "")
	if (checkInAt(checkInInputs{State: scheduleOff}, at("10:00:00"))).Round != nil {
		t.Error("an off schedule has a round")
	}
}

// A capacity change keeps join order: the seats are numbered again in their order, and the pending view shows the new times.
func TestCapacityChangeKeepsJoinOrder(t *testing.T) {
	held := []seatHolder{{1, 10, "A"}, {3, 11, "B"}, {4, 12, "C"}}
	got := compacted(held)
	for i, h := range got {
		if h.Seat != i+1 || h.CharacterID != held[i].CharacterID {
			t.Errorf("compacted %+v", got)
		}
	}
	v := scheduleView(campaignSchedule{Lobby: 1, State: scheduleOn, MaxPlayers: 4, Pending: sql.NullInt64{Int64: 6, Valid: true}, Seats: held})
	p, ok := v["pending_capacity"].(map[string]interface{})
	if !ok {
		t.Fatal("no pending_capacity")
	}
	seats := p["seats"].([]map[string]interface{})
	if len(seats) != 6 || seats[1]["name"] != "B" || seats[1]["check_in"] != ":10" || seats[3]["open"] != true {
		t.Errorf("pending seats %v", seats)
	}
}
