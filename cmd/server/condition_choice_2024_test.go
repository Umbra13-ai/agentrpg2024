package main

// Conditions joined by "or" are a choice (I-ALDENQA-8): the shared rider reader gives neither, and the sentence stays the GM's; a list joined
// by "and" still gives each. Invented wording (no SRD stat block joins conditions with "or").

import (
	"strings"
	"testing"
)

func TestConditionChoiceIsTheGMs(t *testing.T) {
	hit := "Melee Attack Roll: +5, reach 5 ft. Hit: 1 Radiant damage, and the target has the Charmed or Poisoned condition (the sprite's choice) until the start of the sprite's next turn."
	e, ok := parseHitRider(hit)
	if len(e.Conditions) != 0 {
		t.Errorf("a choice gave conditions: %v (ok %v)", e.Conditions, ok)
	}
	if ok && !strings.Contains(e.Extra, "Charmed or Poisoned") {
		t.Errorf("the choice must stay the GM's: extra %q", e.Extra)
	}
	both := "Melee Attack Roll: +5, reach 5 ft. Hit: 1 Radiant damage, and the target has the Blinded and Deafened conditions until the end of its next turn."
	if e, _ := parseHitRider(both); strings.Join(e.Conditions, ",") != "blinded,deafened" {
		t.Errorf("an and-list: %v", e.Conditions)
	}
	var fail saveEffect
	fail.readRider("The target has the Frightened or Poisoned condition until the end of its next turn.")
	if len(fail.Conditions) != 0 {
		t.Errorf("a failed save's choice gave conditions: %v", fail.Conditions)
	}
}
