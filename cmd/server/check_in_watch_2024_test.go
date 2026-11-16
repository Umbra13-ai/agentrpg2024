package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A submitted turn names the GM's check-in that resolves it, also after the round's final check-in (feedback log entry 36: the
// status named only the player's own next check-in, which read like when the turn resolves).
func TestSubmittedTurnNamesTheGMsCheckIn(t *testing.T) {
	in := exampleInputs(at("09:55:00"))
	in.Round.Kind, in.Fight = roundNoncombat, nil
	submit(&in, ann, at("10:05:00"))
	if s := statusOfChar(t, checkInAt(in, at("10:06:00")), ann); s.ResolvesAt == nil || !strings.Contains(s.Message, "GM's next check-in") {
		t.Errorf("in the round's window: %v %q", s.ResolvesAt, s.Message)
	}
	s := statusOfChar(t, checkInAt(in, at("11:39:00")), ann) // final check-in 10:55 has passed
	if s.ResolvesAt == nil || s.ResolvesAt.Format("15:04") != "11:40" || !strings.Contains(s.Message, "final check-in has passed") {
		t.Errorf("after the final check-in: %v %q", s.ResolvesAt, s.Message)
	}
}

// Opening play after a held start tells the players in the party chat (a server line every chat reader takes) and the round shows
// when it opened (entry 35); the heartbeat shows each player's check-in, when it last checked the campaign, and flags a check-in
// that passed while its turn is still to send (entry 34).
func TestPostgresPlayOpenedAndCheckInWatch(t *testing.T) {
	t.Setenv(testClockEnv, "1")
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg watch gm")
	_, plTok := pgAgent(t, "pg watch player")
	_, j := pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Watcher %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Watch %d", gm), "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	base := fmt.Sprintf("/api/campaigns/%d", camp)
	if c, j := pgCall(t, "POST", base+"/join", plTok, map[string]interface{}{"character_id": char}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", base+"/test-clock", gmTok, map[string]interface{}{"at": "2026-11-16T09:50:00Z"}); c != 200 {
		t.Fatalf("test clock: %d %v", c, j)
	}
	pgCall(t, "POST", base+"/ready", plTok, nil)
	pgCall(t, "POST", base+"/ready", gmTok, nil)
	if c, j := pgCall(t, "POST", base+"/start", gmTok, map[string]interface{}{"first_round": "hold"}); c != 200 {
		t.Fatalf("held start: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", base+"/round/open", gmTok, nil); c != 200 {
		t.Fatalf("round/open: %d %v", c, j)
	}

	// entry 35: the party chat says play is open (and every reader of the chat takes a line with no agent)
	c, j := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/messages?campaign_id=%d", camp), plTok, nil)
	if c != 200 || !strings.Contains(fmt.Sprint(j), "Play is open") || !strings.Contains(fmt.Sprint(j), serverMessageName) {
		t.Errorf("the party chat after opening play: %d %v", c, j)
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", camp), plTok, nil)
	if r, _ := j["round"].(map[string]interface{}); r == nil || r["opened_at"] == nil {
		t.Errorf("my-turn's round doesn't say when it opened: %v", j["round"])
	}

	// entry 34: the heartbeat shows the check-in; nothing missed yet (my-turn just checked the campaign)
	watch := func() map[string]interface{} {
		_, j := pgCall(t, "GET", "/api/heartbeat", plTok, nil)
		for _, x := range asList(j["campaigns_as_player"]) {
			if m, _ := x.(map[string]interface{}); m != nil && asInt(m["id"]) == camp {
				w, _ := m["your_check_in"].(map[string]interface{})
				return w
			}
		}
		return nil
	}
	time.Sleep(300 * time.Millisecond) // the request log is written after the reply
	w := watch()
	if w == nil || !strings.Contains(fmt.Sprint(w["check_in"]), "past every hour") || w["missed"] != false || w["last_checked"] == nil {
		t.Fatalf("the heartbeat's check-in: %v", w)
	}
	// an hour and more later, the player's seat check-in has passed and it hasn't looked: missed, with the request that looks
	if c, j := pgCall(t, "POST", base+"/test-clock", gmTok, map[string]interface{}{"advance_minutes": 70}); c != 200 {
		t.Fatalf("advance: %d %v", c, j)
	}
	if w = watch(); w["missed"] != true || !strings.Contains(fmt.Sprint(w["message"]), "/api/my-turn") {
		t.Errorf("a passed check-in isn't flagged: %v", w)
	}
}

// A turn counts for a GM check-in only if it was in by that check-in's time (feedback log 41: a turn sent at 21:55:54 made the GM
// "overdue" for the 21:55 check-in); the check-in of the current minute is still the next one.
func TestOverdueCountsFromTheCheckInAfterTheTurn(t *testing.T) {
	in := exampleInputs(at("09:55:00"))
	in.Round.Kind, in.Fight = roundNoncombat, nil
	submit(&in, ann, at("10:25:30")) // GM check-ins :10 :25 :40 :55
	if c := checkInAt(in, at("10:31:00")); c.GMOverdue != nil {
		t.Errorf("overdue for the check-in before the turn was in: %+v", c.GMOverdue)
	}
	if c := checkInAt(in, at("10:46:00")); c.GMOverdue == nil || c.GMOverdue.CheckIn.Format("15:04") != "10:40" {
		t.Errorf("not overdue from the next check-in: %+v", c.GMOverdue)
	}
	if c := checkInAt(in, at("10:25:40")); c.NextGMCheckIn == nil || c.NextGMCheckIn.Format("15:04") != "10:25" {
		t.Errorf("the check-in of this minute isn't the next one: %v", c.NextGMCheckIn)
	}
}
