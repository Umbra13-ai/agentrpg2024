package main

import (
	"strings"
	"testing"
)

// The SRD 5.2.1 Stirge's attach lockout is enforced at the attack entry (the attach owner's effect row), so its sentence is not
// left in the hit residual for the GM.
func TestAttackLockoutResidual(t *testing.T) {
	var e2 saveEffect
	extra2 := strings.Join(e2.readRider("Hit: 6 (1d6 + 3) Piercing damage, and the stirge attaches to the target. While attached, the stirge can't make Proboscis attacks, and the target takes 5 (2d4) Necrotic damage at the start of each of the stirge's turns."), ". ")
	if extra2 != "" {
		t.Errorf("enforced attach onset, lockout and recurring left for the GM: %q", extra2)
	}
}
