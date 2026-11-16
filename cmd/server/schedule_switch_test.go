package main

import "testing"

// The tests run with the turn schedule switched on (as staging and local servers do with AGENTRPG_TURN_SCHEDULE=1); the tests
// below switch it off to check what a production server does until stages E and F are built.
func init() { turnScheduleEnabled = true }

// TestScheduleSwitchedOff: while the schedule isn't available, every view of it says so and shows no times.
func TestScheduleSwitchedOff(t *testing.T) {
	turnScheduleEnabled = false
	defer func() { turnScheduleEnabled = true }()
	v := scheduleView(campaignSchedule{Lobby: 1, State: scheduleOn, MaxPlayers: 4})
	if v["available"] != false || v["state"] != scheduleOff {
		t.Fatalf("scheduleView with the schedule switched off = %v, want available false and state off", v)
	}
	if _, has := v["seats"]; has {
		t.Fatalf("scheduleView with the schedule switched off shows seats: %v", v)
	}
}
