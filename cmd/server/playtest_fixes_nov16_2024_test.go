package main

import (
	"strings"
	"testing"
)

// Feedback log 30: the two-hands refusal names what fills the hands and the request that frees one.
func TestHandsRefusalNamesTheWayOut(t *testing.T) {
	for _, tc := range []struct {
		main, off string
		shield    bool
		want      []string
	}{
		{"Longsword", "", true, []string{`"shield": false`, "equip-armor"}},
		{"Shortsword", "Dagger", false, []string{"put away the Dagger", `"slot": "off_hand"`}},
		{"Shortsword", "Dagger", true, []string{"Dagger", `"shield": false`}},
		{"Greatsword", "", false, []string{"put away the Greatsword", `"slot": "main_hand"`}},
	} {
		got := freeHandRequests(7, tc.main, tc.off, tc.shield)
		for _, w := range tc.want {
			if !strings.Contains(got, w) || !strings.Contains(got, `"character_id": 7`) {
				t.Errorf("%s/%s/%v: %q lacks %q", tc.main, tc.off, tc.shield, got, w)
			}
		}
	}
	if freeHandRequests(0, "A", "B", true) != "" {
		t.Errorf("no character: no request to name")
	}
}

// Feedback log 12 and 37: the join reply's next step names the seat's check-in, says to set it a few minutes early, and what to do
// after; without a seat time it falls back to checking every 2 hours. Every check-in text says schedulers run late.
func TestJoinNextStep(t *testing.T) {
	s, timed := scheduleOf(4)
	if !timed {
		t.Fatal("4 seats should be timed")
	}
	m, _ := s.seatMinute(2)
	got := joinNextStep(s, true, 2, 9, "recruiting")
	for _, w := range []string{"Your next step", minuteText(m), "a few minutes early", "GET /api/my-turn?campaign_id=9", "/api/campaigns/9/ready"} {
		if !strings.Contains(got, w) {
			t.Errorf("recruiting: %q lacks %q", got, w)
		}
	}
	if got := joinNextStep(s, true, 2, 9, "active"); !strings.Contains(got, "is_my_turn") {
		t.Errorf("active: %q", got)
	}
	if got := joinNextStep(s, false, 2, 9, "recruiting"); !strings.Contains(got, "every 2 hours") {
		t.Errorf("schedule off: %q", got)
	}
	for _, text := range []string{seatNote(s, 2), checkInNote} {
		if !strings.Contains(text, "late") {
			t.Errorf("a check-in text doesn't say schedulers run late: %q", text)
		}
	}
}

// Feedback log 18: the GM is told the request that opens play after a held start.
func TestOpenPlayRequest(t *testing.T) {
	if got := openPlayRequest(12); got != "POST /api/campaigns/12/round/open" {
		t.Errorf("%q", got)
	}
	if !strings.Contains(tableHeldGMTask(12), openPlayRequest(12)) {
		t.Errorf("the held-table GM task doesn't name the request: %q", tableHeldGMTask(12))
	}
}
