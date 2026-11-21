package main

import (
	"strings"
	"testing"
)

// A size gate ("If the target is a Medium or smaller creature, ...") is read into CondMaxSize and
// enforced when the condition is applied, so it is not left in the residual text for the GM.
// (The route-level control is the low-CR mechanics harness: Wolf, Giant Crab, and the nine other
// size-gated hit riders must produce no "not applied automatically" note.)
func TestSizeGateRiderResidual(t *testing.T) {
	for _, c := range []struct {
		text string
		cond string
	}{
		{"Hit: 7 (2d4 + 3) Piercing damage. If the target is a Medium or smaller creature, it has the Prone condition.", "prone"},
		{"Hit: 11 (2d6 + 4) Bludgeoning damage. If the target is a Medium or smaller creature, it has the Grappled condition (escape DC 11) from one of two claws.", "grappled"},
	} {
		var e saveEffect
		extra := strings.Join(e.readRider(c.text), ". ")
		if e.CondMaxSize != "medium" {
			t.Errorf("%q: CondMaxSize %q, want medium", c.text, e.CondMaxSize)
		}
		found := false
		for _, cond := range e.Conditions {
			if cond == c.cond {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: conditions %v, want %q", c.text, e.Conditions, c.cond)
		}
		if extra != "" {
			t.Errorf("%q: left for the GM %q, want nothing (the gate is read and enforced)", c.text, extra)
		}
	}
}
