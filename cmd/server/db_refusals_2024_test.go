package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRefusalsAreNotDatabaseFailures: every refusal type, bare or wrapped, is the request's answer, never a database failure:
// dbFailed is false and dbFailureError gives it back unchanged, so its own words reach the reader. A real failure still is one.
func TestRefusalsAreNotDatabaseFailures(t *testing.T) {
	for _, r := range []error{&seatRefusal{Code: "campaign_full", Message: "This campaign is full: 6 of 6 seats are taken."},
		refuseInv(409, map[string]interface{}{"error": "not_carried"}), codeRefusal("not_carried"),
		&originError{Code: "bad_choice", Message: "no"}, &notEnough{Msg: "Cannot use X: none left."}} {
		for _, err := range []error{r, fmt.Errorf("wrapped: %w", r)} {
			if dbFailed(err) || dbFailureError(err, "the seat") != err {
				t.Errorf("%T read as a database failure: %v", r, err)
			}
		}
	}
	real := errors.New("pq: canceling statement due to lock timeout")
	if !dbFailed(real) || dbFailureError(real, "the seat") == real || dbFailed(sql.ErrNoRows) {
		t.Error("a real failure is no longer one, or a missing row is")
	}
}

// TestPostgresAdminAddToAFullCampaign: the admin adding a character to a full campaign is told it is full, with the seats, not of a
// database problem (owner, Nov 26: the admin page said "a database problem on the server").
func TestPostgresAdminAddToAFullCampaign(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "full seat gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": fmt.Sprintf("Full Seats %d", time.Now().UnixNano()%1e9), "max_players": 1})
	camp := int(j["campaign_id"].(float64))
	mk := func(n string) int {
		_, p := pgAgent(t, "full seat "+n)
		_, j := pgCall(t, "POST", "/api/characters", p, pgRogue(fmt.Sprintf("Full Seat %s %d", n, time.Now().UnixNano()%1e9)))
		return int(j["character_id"].(float64))
	}
	if _, _, err := adminCampaignMembership(camp, map[string]interface{}{"add_character": float64(mk("one"))}, ""); err != nil {
		t.Fatalf("the first: %v", err)
	}
	_, _, err := adminCampaignMembership(camp, map[string]interface{}{"add_character": float64(mk("two"))}, "")
	if err == nil || !strings.Contains(err.Error(), "This campaign is full: 1 of 1 seats are taken.") || strings.Contains(err.Error(), "database") {
		t.Errorf("a full campaign: %v", err)
	}
}
