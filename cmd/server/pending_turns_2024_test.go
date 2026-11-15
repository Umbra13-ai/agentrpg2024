package main

import (
	"strings"
	"testing"
)

// The round a pending turn is for: this round while the slot is still to come (or is the current one), the next once it has passed.
func TestPendingTurnRoundFor(t *testing.T) {
	c := ptCombat{Round: 3, Index: 2, Entries: []ptEntry{{ID: 11, Name: "A"}, {ID: 12, Name: "B"}, {ID: 13, Name: "C"}, {ID: -1, Name: "Goblin", IsMonster: true}}}
	for charID, want := range map[int]int{11: 4, 12: 4, 13: 3} {
		if got := c.roundFor(c.slotOf(charID)); got != want {
			t.Errorf("character %d: round %d, want %d", charID, got, want)
		}
	}
	if c.slotOf(-1) != -1 || c.slotOf(99) != -1 {
		t.Error("a monster or a character not in the fight has no player slot")
	}
}

func TestPendingStepDecode(t *testing.T) {
	s, err := ptDecodeStep(map[string]interface{}{"action": "Magic", "spell_slug": "fire-bolt", "target_id": -2.0})
	if err != nil || s.Action != "cast" || s.TargetID != -2 {
		t.Errorf("step decoded as %+v (%v)", s, err)
	}
	if _, err := ptDecodeStep(map[string]interface{}{"action": "attack", "target_id": "goblin"}); err == nil {
		t.Error("a target_id sent as text should not decode")
	}
	// a step takes every field of an action (the same type), and a map_move's path and altitude
	steps := requestFields(&ptStep{})
	for _, f := range requestFields(&actionRequest{}) {
		if f != "character_id" && f != "campaign_id" && !hasString(steps, f) {
			t.Errorf("%s is an action field but not a step field", f)
		}
	}
	for _, f := range []string{"path", "altitude"} {
		if !hasString(steps, f) {
			t.Errorf("%s should be a step field", f)
		}
	}
	if _, p := ptDecodeStep(map[string]interface{}{"action": "dodge", "character_id": 5.0}); p == nil || p.Code != "unknown_field" || !strings.Contains(p.Message, "top of the pending turn") {
		t.Errorf("character_id belongs at the top of the request, not in a step: %v", p)
	}
	if _, p := ptDecodeStep(map[string]interface{}{"action": "map_move", "path": []interface{}{map[string]interface{}{"x": 1.0, "y": 2.0, "z": 3.0}}}); p == nil || p.Code != "unknown_field" {
		t.Errorf("an unknown field inside a path square should be refused: %v", p)
	}
}
