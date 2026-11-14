package main

import "testing"

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
	for _, f := range []string{"action", "target_id", "spell_slug", "path", "features"} {
		if !ptStepFields[f] {
			t.Errorf("%s should be a step field", f)
		}
	}
	if ptStepFields["character_id"] {
		t.Error("character_id belongs at the top of the request, not in a step")
	}
}
