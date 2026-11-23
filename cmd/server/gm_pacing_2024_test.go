package main

import (
	"fmt"
	"testing"
)

// The GM's status of a campaign in play asks the pacing question at every check-in (gm_pacing_2024.go).
func TestPostgresGMStatusAsksThePacingCheck(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _ := pgFightWith(t, "pacing", 1)
	c, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", camp), gm, nil)
	if c != 200 || j["pacing_check"] != pacingCheck {
		t.Fatalf("GM status of a campaign in play: %d, pacing_check %v", c, j["pacing_check"])
	}
}
