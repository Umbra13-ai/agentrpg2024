package main

// I-CALLA-6 (feedback item 68): my-turn's recent_events were the newest 10 records reversed to oldest first, as text with no time,
// and polls and joins took places in the 10. They are read through readActions with the bookkeeping filter (playOnly, notPlayTypes),
// newest first, in the action log's own form (actionRecord.view: created_at, actor, text); the heartbeat's recent actions and the
// GM's nudge read the same way.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPostgresRecentEventsNewestFirstWithoutBookkeeping(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg events gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Events", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	base := fmt.Sprintf("/api/campaigns/%d", lobby)
	pgCall(t, "PATCH", base+"/schedule", gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg events player")
	name := fmt.Sprintf("Pg Events %d", time.Now().UnixNano()%1e9)
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(name))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", base+"/join", tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", base+"/ready", tok, nil)
	pgCall(t, "POST", base+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", base+"/start", gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	write := func(charID int, kind, desc, result string) {
		t.Helper()
		if err := writeAction(db, lobby, charID, kind, desc, result); err != nil {
			t.Fatal(err)
		}
	}
	write(char, "search", "Events: I search the shelves", "Events: a hidden latch")
	write(0, actionNarration, "Events: the lamps gutter", "")
	for i := 0; i < 12; i++ { // bookkeeping that used to push play out of the 10
		write(char, "poll", fmt.Sprintf("Events: checked %d", i), "")
	}
	write(char, "joined", "Events: joined again", "")

	_, j = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?campaign_id=%d", lobby), tok, nil)
	events := asList(j["recent_events"])
	if len(events) < 2 {
		t.Fatalf("recent_events: %v", j["recent_events"])
	}
	first, _ := events[0].(map[string]interface{})
	second, _ := events[1].(map[string]interface{})
	if first["type"] != actionNarration || second["type"] != "search" {
		t.Errorf("not newest first: %v, %v", first, second)
	}
	t1, err1 := time.Parse(time.RFC3339Nano, fmt.Sprint(first["created_at"]))
	t2, err2 := time.Parse(time.RFC3339Nano, fmt.Sprint(second["created_at"]))
	if err1 != nil || err2 != nil || t1.Before(t2) {
		t.Errorf("times: %v then %v", first["created_at"], second["created_at"])
	}
	if second["actor"] != name || !strings.Contains(fmt.Sprint(second["text"]), name+": Events: I search the shelves → Events: a hidden latch") {
		t.Errorf("who acted and the readable text: %v", second)
	}
	if !strings.Contains(fmt.Sprint(first["text"]), "(GM): Events: the lamps gutter") || j["gm_says"] != "Events: the lamps gutter" {
		t.Errorf("the GM's narration: %v, gm_says %v", first, j["gm_says"])
	}
	if s := fmt.Sprint(events); strings.Contains(s, "Events: checked") || strings.Contains(s, "joined again") {
		t.Errorf("bookkeeping in recent_events: %v", events)
	}

	_, hb := pgCall(t, "GET", "/api/heartbeat", tok, nil)
	if s := fmt.Sprint(hb); !strings.Contains(s, "Events: I search the shelves") || strings.Contains(s, "Events: checked") || strings.Contains(s, "joined again") {
		t.Errorf("the heartbeat's recent actions: %.800v", s)
	}
}
