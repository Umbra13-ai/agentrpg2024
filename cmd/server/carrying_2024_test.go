package main

import (
	"fmt"
	"testing"
	"time"
)

// Carrying Capacity has one owner (carryingCapacity): Strength × 15 for Small or Medium, × 30 for a Goliath (Powerful Build counts
// it one size larger). The character page computed Strength × 15 itself, whatever the size (HANDOFF item 16's leftover).
func TestCarryingCapacityBySize(t *testing.T) {
	for species, want := range map[string]float64{"human": 150, "halfling": 150, "goliath": 300} {
		if got := carryingCapacity(10, species); got != want {
			t.Errorf("%s with Strength 10: capacity %v, want %v", species, got, want)
		}
	}
}

// The sheet's "carrying" and GET /characters/encumbrance give the same weight, capacity and status.
func TestPostgresSheetCarryingMatchesEncumbrance(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg porter")
	c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Porter %d", time.Now().UnixNano()%1e9)))
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	c, sheet := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d", id), tok, nil)
	carrying, _ := sheet["carrying"].(map[string]interface{})
	if c != 200 || carrying == nil {
		t.Fatalf("sheet: %d, carrying %v", c, sheet["carrying"])
	}
	c, enc := pgCall(t, "GET", fmt.Sprintf("/api/characters/encumbrance?character_id=%d", id), tok, nil)
	if c != 200 {
		t.Fatalf("encumbrance: %d %v", c, enc)
	}
	for _, k := range []string{"total_weight", "carrying_capacity", "status"} {
		if fmt.Sprint(carrying[k]) != fmt.Sprint(enc[k]) {
			t.Errorf("%s: sheet %v, encumbrance %v", k, carrying[k], enc[k])
		}
	}
	if w, _ := carrying["total_weight"].(float64); w <= 0 {
		t.Errorf("a new Rogue carries its starting gear: total_weight %v", carrying["total_weight"])
	}
}
