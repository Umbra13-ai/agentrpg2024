package main

import "testing"

func TestSetOutcome(t *testing.T) {
	m := map[string]interface{}{"success": true, "resource_consumed": "action"}
	setOutcome(m)
	if m["outcome"] != outcomeApplied || m["spent"] != "action" {
		t.Errorf("an applied action: %v", m)
	}
	m = map[string]interface{}{"success": true, "gm_rules": "The GM rules on what this does and narrates it.", "resource_consumed": "your story action until the GM narrates"}
	setOutcome(m)
	if m["outcome"] != outcomeNeedsGM {
		t.Errorf("a declaration the GM rules on: %v", m)
	}
	m = map[string]interface{}{"outcome": outcomeQueued, "spent": "nothing yet"}
	setOutcome(m)
	if m["outcome"] != outcomeQueued || m["spent"] != "nothing yet" {
		t.Errorf("a field already there is kept: %v", m)
	}
}
