package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// standInAllowed: without the schedule at once; with it only once the round's final check-in has come, never while paused, and
// at any time to finish a submitted turn paused for the GM.
func TestStandInAllowed(t *testing.T) {
	r := &roundRecord{Number: 3, Kind: roundCombat, OpenedAt: at("09:55:00"), ClockFrom: at("09:55:00"), Deadline: nullTime(at("10:55:00"))}
	paused := &roundRecord{Number: 3, Kind: roundCombat, OpenedAt: at("09:55:00"), ClockFrom: at("09:55:00")}
	cases := []struct {
		name      string
		state     string
		r         *roundRecord
		now       string
		finishing bool
		allowed   bool
		why       string
	}{
		{"schedule off: at once", scheduleOff, nil, "10:00:00", false, true, ""},
		{"on, before the final check-in", scheduleOn, r, "10:47:00", false, false, "too_early"},
		{"on, a second before it", scheduleOn, r, "10:54:59", false, false, "too_early"},
		{"on, at the final check-in", scheduleOn, r, "10:55:00", false, true, ""},
		{"on, after it", scheduleOn, r, "12:10:00", false, true, ""},
		{"paused, past the old deadline", schedulePaused, paused, "12:10:00", false, false, "paused"},
		{"on but the round has no deadline (paused round)", scheduleOn, paused, "12:10:00", false, false, "paused"},
		{"on with no open round", scheduleOn, nil, "12:10:00", false, false, "no_round"},
		{"finishing a paused submitted turn, early", scheduleOn, r, "10:20:00", true, true, ""},
		{"finishing while the schedule is paused", schedulePaused, paused, "10:20:00", true, true, ""},
	}
	for _, c := range cases {
		got := standInAllowed(c.state, c.r, at(c.now), c.finishing)
		if got.Allowed != c.allowed || (!c.allowed && got.Why != c.why) {
			t.Errorf("%s: %+v", c.name, got)
		}
		if c.why == "too_early" && (got.Deadline == nil || !got.Deadline.Equal(at("10:55:00"))) {
			t.Errorf("%s: deadline %v", c.name, got.Deadline)
		}
		if !got.Allowed {
			ref := got.refusal("Cara")
			if ref["error"] != "stand_in_too_early" || !strings.Contains(ref["message"].(string), "Cara") {
				t.Errorf("%s: refusal %v", c.name, ref)
			}
		}
	}
}

// pendingRoundFor: with the schedule only the open round, and never for a character whose turn in it has resolved; without it
// as before (the next round once the slot has passed).
func TestPendingRoundFor(t *testing.T) {
	c := ptCombat{Round: 4, Index: 2, Entries: []ptEntry{{ID: -1, Name: "Goblin", IsMonster: true}, {ID: 1, Name: "Ann"}, {ID: 2, Name: "Ben"}, {ID: 3, Name: "Cara"}}}
	for _, x := range []struct {
		slot      int
		scheduled bool
		asked     int
		round     int
		code      string
	}{
		{3, true, 0, 4, ""},                     // still to come: this round
		{2, true, 0, 4, ""},                     // the current slot: this round
		{1, true, 0, 4, codeTurnAlreadyResolve}, // passed: done for this round, no turn for the next
		{3, true, 5, 4, codeRoundNotOpen},       // a round that isn't open
		{3, true, 3, 4, codeRoundNotOpen},       // a closed round
		{3, true, 4, 4, ""},                     // the open round, named
		{1, false, 0, 5, ""},                    // without the schedule: passed, the next round (as before)
		{3, false, 0, 4, ""},                    // without the schedule: this round
		{1, false, 4, 5, codeRoundNotOpen},      // a round named that isn't the one the turn is for
	} {
		round, code, msg := pendingRoundFor(c, x.slot, x.scheduled, x.asked)
		if round != x.round || code != x.code || (code != "" && !strings.Contains(msg, "Nothing was saved")) {
			t.Errorf("slot %d scheduled %v asked %d: round %d code %q (%s)", x.slot, x.scheduled, x.asked, round, code, msg)
		}
	}
}

// The table's CHECK lists exactly the ways a turn ends that the code knows (turnResultHows).
func TestTurnResultHows(t *testing.T) {
	m := regexp.MustCompile(`how TEXT NOT NULL CHECK \(how IN \(([^)]*)\)\)`).FindStringSubmatch(turnScheduleSchema)
	if m == nil {
		t.Fatal("turn_results.how has no CHECK in turnScheduleSchema")
	}
	var inDDL, inCode []string
	for _, w := range strings.Split(m[1], ",") {
		inDDL = append(inDDL, strings.Trim(strings.TrimSpace(w), "'"))
	}
	for k := range turnResultHows {
		inCode = append(inCode, k)
	}
	sort.Strings(inDDL)
	sort.Strings(inCode)
	if strings.Join(inDDL, ",") != strings.Join(inCode, ",") {
		t.Errorf("the CHECK lists %v, the code %v", inDDL, inCode)
	}
}

// Each turn's end is recorded once (turn_results' UNIQUE), only in a campaign with the schedule; the round closes and the next
// opens when the fight's round goes up, once.
func TestTurnResultsOnceAndTheNextRound(t *testing.T) {
	scheduleTestDB(t)
	lobby := newTestCampaign(t, 4, scheduleOn)
	r1, err := roundOpens(lobby, roundCombat, 1, false, "")
	if err != nil || r1 == nil || r1.Number != 1 {
		t.Fatalf("round 1: %+v %v", r1, err)
	}
	end := func(id int, how string) *turnChange {
		tc := &turnChange{Lobby: lobby, how: turnHow{result: how}, EndedID: id, EndedName: "x", EndedRound: 1}
		recordTurnResult(tc)
		return tc
	}
	if got := end(7, howOwn).Recorded; got != howOwn {
		t.Errorf("first end of the turn recorded %q", got)
	}
	if got := end(7, howStandIn).Recorded; got != "" {
		t.Errorf("a second end of the same turn was recorded too (%q)", got)
	}
	if got := end(-1, howMonster).Recorded; got != howMonster {
		t.Errorf("the monster's turn: %q", got)
	}
	second := &turnChange{Lobby: lobby, how: turnHow{result: howOwn}, EndedID: 7, EndedSecond: true, EndedRound: 1}
	if recordTurnResult(second); second.Recorded != howOwn {
		t.Error("a Thief's Reflexes second turn is its own turn")
	}
	if how, _ := slotRecorded(lobby, 1, 7, false); how != howOwn {
		t.Errorf("slotRecorded: %q", how)
	}
	got, err := roundResults(db, r1.ID)
	if err != nil || len(got) != 2 || got[7] != howOwn || got[-1] != howMonster {
		t.Errorf("roundResults: %v %v", got, err)
	}
	// the fight's round goes up: round 1 closes, round 2 opens, once
	r2 := scheduleFightRound(lobby, 2)
	if r2 == nil || r2.Number != 2 || !r2.Deadline.Valid {
		t.Fatalf("round 2: %+v", r2)
	}
	if again := scheduleFightRound(lobby, 2); again != nil {
		t.Errorf("round 2 opened twice: %+v", again)
	}
	var closed int
	db.QueryRow(`SELECT COUNT(*) FROM campaign_rounds WHERE lobby_id = $1 AND closed_at IS NOT NULL`, lobby).Scan(&closed)
	if closed != 1 {
		t.Errorf("%d closed rounds, want 1", closed)
	}
	if s := roundCompleteText(1, r2); !strings.HasPrefix(s, "Round 1 is complete; round 2 has begun (its final check-in is ") {
		t.Errorf("round complete text: %s", s)
	}
	if tc := end(7, howOwn); tc.Recorded != "" {
		t.Error("a turn of round 1 recorded after round 1 closed")
	}
	// a pending capacity takes effect when the round opens
	db.Exec(`UPDATE lobbies SET pending_capacity = 6 WHERE id = $1`, lobby)
	if r3 := scheduleFightRound(lobby, 3); r3 == nil {
		t.Fatal("round 3 didn't open")
	}
	var capacity int
	db.QueryRow(`SELECT max_players FROM lobbies WHERE id = $1`, lobby).Scan(&capacity)
	if capacity != 6 {
		t.Errorf("capacity %d after the round opened, want 6", capacity)
	}
	// without the schedule: no round, nothing recorded
	off := newTestCampaign(t, 4, scheduleOff)
	if r, _ := roundOpens(off, roundCombat, 1, false, ""); r != nil {
		t.Errorf("a round opened in a campaign without the schedule: %+v", r)
	}
	tc := &turnChange{Lobby: off, how: turnHow{result: howOwn}, EndedID: 7, EndedRound: 1}
	if recordTurnResult(tc); tc.Recorded != "" {
		t.Error("a turn recorded in a campaign without the schedule")
	}
}

// turn_results is written only by the turn-end hook (recordTurnResult), so every way a turn ends records it once.
func TestNoOtherTurnResultWriter(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	write := regexp.MustCompile(`(?i)(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+turn_results`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			if write.MatchString(line) && f != "schedule_walk_2024.go" {
				t.Errorf("%s:%d writes turn_results outside recordTurnResult: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
	b, _ := os.ReadFile("turn_advance_2024.go")
	if !strings.Contains(string(b), "recordTurnResult}") {
		t.Error("recordTurnResult is not a row of turnEndHooks")
	}
}

// Only the GM's requests run the walk (Matt, section 3: checking status never changes anything): resolvePendingTurns is called
// from combat/resolve, combat/stand-in and the walk after combat/next and combat/skip (addPendingWalk), and from GET
// /api/gm/status only for a campaign without the schedule.
func TestOnlyTheGMsRequestsRunTheWalk(t *testing.T) {
	allowed := map[string]bool{"handleCombatResolve": true, "handleCombatStandIn": true, "addPendingWalk": true, "handleGMStatus": true}
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "resolvePendingTurns" && !allowed[fn.Name.Name] {
					t.Errorf("%s: %s runs the pending-turn walk; only the GM's requests do", fset.Position(call.Pos()), fn.Name.Name)
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "addPendingWalk" && fn.Name.Name != "handleCampaignByID" {
					t.Errorf("%s: %s runs the walk after a turn change; only combat/next and combat/skip do", fset.Position(call.Pos()), fn.Name.Name)
				}
				return true
			})
		}
	}
	b, _ := os.ReadFile("gm_narration.go")
	if !regexp.MustCompile(`if !scheduled \{\s*pendingReport = resolvePendingTurns\(campaignID\)`).Match(b) {
		t.Error("GET /api/gm/status runs the walk for a campaign with the turn schedule")
	}
}

// The status says how each resolved turn ended (turn_results), and a dead character whose slot has passed is automatic, not
// resolved: it had no turn.
func TestCheckInShowsHowEachTurnEnded(t *testing.T) {
	in := exampleInputs(at("09:55:00"))
	in.Fight.Current = 5 // Cara, Ann, the Ogre and Ben are done; Dev is due
	for id, how := range map[int]string{cara: howStandIn, ann: howFallback} {
		x := in.Turns[id]
		x.How = how
		in.Turns[id] = x
	}
	in.Turns[ben] = charTurn{Needs: turnNeed{needNothing, "dead"}}
	c := checkInAt(in, at("10:56:00"))
	if s := statusOfChar(t, c, cara); s.Status != statusResolved || s.How != howStandIn || !strings.Contains(s.Message, "stand-in") {
		t.Errorf("Cara: %+v", s)
	}
	if s := statusOfChar(t, c, ann); s.How != howFallback {
		t.Errorf("Ann: %+v", s)
	}
	if s := statusOfChar(t, c, ben); s.Status != statusAutomatic || s.Automatic != "dead" {
		t.Errorf("Ben (dead): %+v", s)
	}
	// a turn recorded as ended counts as resolved even before the turn order moved on
	x := in.Turns[dev]
	x.How = howOwn
	in.Turns[dev] = x
	if s := statusOfChar(t, checkInAt(in, at("10:56:00")), dev); s.Status != statusResolved {
		t.Errorf("Dev: %+v", s)
	}
}
