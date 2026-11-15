package game

import "testing"

func TestTakeDamageTemporaryHitPointsFirst(t *testing.T) {
	v := Vitals{HP: 20, Max: 30, Temp: 5}
	r := TakeDamage(v, 3, false, false)
	if r.After.Temp != 2 || r.After.HP != 20 || r.Absorbed != 3 || r.ToHP != 0 || r.Outcome != Damaged {
		t.Fatalf("3 damage with 5 temp: %+v", r)
	}
	r = TakeDamage(v, 8, false, false)
	if r.After.Temp != 0 || r.After.HP != 17 || r.Absorbed != 5 || r.ToHP != 3 {
		t.Fatalf("8 damage with 5 temp: %+v", r)
	}
}

func TestTakeDamageNeverBelowZeroAndDropsToDying(t *testing.T) {
	v := Vitals{HP: 5, Max: 30, Successes: 1, Failures: 1, Stable: true}
	r := TakeDamage(v, 12, false, false)
	if r.After.HP != 0 || r.After.Dead || r.Outcome != DroppedToZero || r.Overflow != 7 {
		t.Fatalf("12 damage at 5 HP: %+v", r)
	}
	if r.After.Successes != 0 || r.After.Failures != 0 || r.After.Stable {
		t.Fatalf("a creature that drops to 0 starts with no Death Saving Throws: %+v", r.After)
	}
	if !r.FromAboveZero() {
		t.Fatal("dropping from 5 to 0 is from above zero")
	}
}

func TestInstantDeathThreshold(t *testing.T) {
	// SRD 5.2.1: damage remaining after reaching 0 that equals or exceeds the Hit Point maximum kills outright
	v := Vitals{HP: 6, Max: 12}
	if r := TakeDamage(v, 17, false, false); r.Outcome != DroppedToZero || r.After.Dead {
		t.Fatalf("11 left over against a maximum of 12 is not Instant Death: %+v", r)
	}
	if r := TakeDamage(v, 18, false, false); r.Outcome != InstantDeath || !r.After.Dead || r.After.HP != 0 {
		t.Fatalf("12 left over against a maximum of 12 is Instant Death: %+v", r)
	}
	// Temporary Hit Points absorb first: the left-over is counted after them
	if r := TakeDamage(Vitals{HP: 6, Max: 12, Temp: 4}, 21, false, false); r.Outcome != DroppedToZero {
		t.Fatalf("21 with 4 temp at 6 HP leaves 11 over: %+v", r)
	}
	// at 0 Hit Points: damage equal to the maximum kills
	if r := TakeDamage(Vitals{HP: 0, Max: 12}, 12, false, false); r.Outcome != InstantDeath || !r.After.Dead {
		t.Fatalf("12 damage at 0 HP with a maximum of 12: %+v", r)
	}
}

func TestDamageAtZeroIsDeathSaveFailures(t *testing.T) {
	v := Vitals{HP: 0, Max: 20, Successes: 2, Failures: 0, Stable: true}
	r := TakeDamage(v, 3, false, false)
	if r.Outcome != FailedAtZero || r.After.Failures != 1 || r.FailuresAdded != 1 || r.After.Stable || r.After.Successes != 2 {
		t.Fatalf("a hit at 0 HP: %+v", r)
	}
	r = TakeDamage(v, 3, true, false)
	if r.After.Failures != 2 || r.FailuresAdded != 2 {
		t.Fatalf("a Critical Hit at 0 HP is two failures: %+v", r)
	}
	r = TakeDamage(Vitals{HP: 0, Max: 20, Failures: 1}, 3, true, false)
	if r.Outcome != DiedAtZero || !r.After.Dead || r.After.Failures != 3 {
		t.Fatalf("one failure and a Critical Hit: dead: %+v", r)
	}
	r = TakeDamage(Vitals{HP: 0, Max: 20, Failures: 1}, 3, false, true)
	if r.Outcome != SameHitAtZero || r.After.Failures != 1 {
		t.Fatalf("the second damage type of one hit adds no failure: %+v", r)
	}
	r = TakeDamage(Vitals{HP: 0, Max: 20, Temp: 5}, 3, false, false)
	if r.After.Failures != 0 || r.After.Temp != 2 {
		t.Fatalf("damage that only Temporary Hit Points take adds no failure: %+v", r)
	}
}

func TestDamageToTheDeadChangesNothing(t *testing.T) {
	v := Vitals{HP: 0, Max: 20, Failures: 3, Dead: true}
	if r := TakeDamage(v, 50, true, false); r.Outcome != AlreadyDead || r.After != v {
		t.Fatalf("%+v", r)
	}
}

func TestInsteadDropsToOne(t *testing.T) {
	r := TakeDamage(Vitals{HP: 4, Max: 10, Temp: 2}, 30, false, false) // Instant Death without Death Ward
	if r.Outcome != InstantDeath {
		t.Fatalf("%+v", r)
	}
	r = r.Instead(1)
	if r.After.HP != 1 || r.After.Dead || r.After.Temp != 0 || r.Outcome != Damaged {
		t.Fatalf("Death Ward: 1 Hit Point instead: %+v", r)
	}
}

func TestHealCapsAndWakes(t *testing.T) {
	v := Vitals{HP: 0, Max: 20, Successes: 2, Failures: 2, Stable: true}
	after, healed := Heal(v, 5, 0)
	if after.HP != 5 || healed != 5 || after.Successes != 0 || after.Failures != 0 || after.Stable || !Woke(v, after) {
		t.Fatalf("healing from 0 resets the Death Saving Throws: %+v", after)
	}
	after, healed = Heal(Vitals{HP: 18, Max: 20}, 10, 0)
	if after.HP != 20 || healed != 2 {
		t.Fatalf("never above the maximum: %+v %d", after, healed)
	}
	after, healed = Heal(Vitals{HP: 3, Max: 20}, 50, 10)
	if after.HP != 10 || healed != 7 {
		t.Fatalf("a ceiling (Preserve Life: half): %+v %d", after, healed)
	}
	after, healed = Heal(Vitals{HP: 12, Max: 20}, 5, 10)
	if after.HP != 12 || healed != 0 {
		t.Fatalf("above the ceiling already: nothing (and never lowered): %+v %d", after, healed)
	}
}

func TestNoHealingForTheDead(t *testing.T) {
	v := Vitals{HP: 0, Max: 20, Failures: 3, Dead: true}
	if after, healed := Heal(v, 10, 0); after != v || healed != 0 || Woke(v, after) {
		t.Fatalf("%+v %d", after, healed)
	}
	if after := v.SetHP(10); after != v {
		t.Fatalf("setting a dead creature's Hit Points changes nothing: %+v", after)
	}
	if after := v.Revive(1); after.Dead || after.HP != 1 || after.Failures != 0 {
		t.Fatalf("a spell that returns it to life: %+v", after)
	}
}

func TestSetHP(t *testing.T) {
	if v := (Vitals{HP: 10, Max: 20}).SetHP(50); v.HP != 20 {
		t.Fatalf("not above the maximum: %+v", v)
	}
	if v := (Vitals{HP: 10, Max: 20}).SetHP(-3); v.HP != 0 || v.Failures != 0 {
		t.Fatalf("not below 0: %+v", v)
	}
	if v := (Vitals{HP: 0, Max: 20, Failures: 2}).SetHP(4); v.Failures != 0 || v.HP != 4 {
		t.Fatalf("from 0: saves reset: %+v", v)
	}
}

func TestDeathSave(t *testing.T) {
	dying := Vitals{HP: 0, Max: 20, Successes: 2, Failures: 1}
	if v, out := dying.DeathSave(20, true); out != DeathSaveRegained || v.HP != 1 || v.Successes != 0 || v.Failures != 0 {
		t.Fatalf("natural 20: %+v %s", v, out)
	}
	if v, out := dying.DeathSave(1, false); out != DeathSaveDied || !v.Dead || v.Failures != 3 {
		t.Fatalf("natural 1 is two failures: %+v %s", v, out)
	}
	if v, out := dying.DeathSave(12, true); out != DeathSaveStable || !v.Stable || v.Successes != 0 || v.Failures != 0 {
		t.Fatalf("third success: Stable, both reset: %+v %s", v, out)
	}
	if v, out := dying.DeathSave(5, false); out != DeathSaveFailure || v.Failures != 2 {
		t.Fatalf("a failure: %+v %s", v, out)
	}
	if _, out := (Vitals{HP: 3, Max: 20}).DeathSave(20, true); out != DeathSaveNone {
		t.Fatalf("not dying: %s", out)
	}
}

func TestConcentrationDC(t *testing.T) {
	for _, c := range []struct{ taken, dc int }{{0, 10}, {1, 10}, {21, 10}, {22, 11}, {45, 22}, {60, 30}, {200, 30}} {
		if got := ConcentrationDC(c.taken); got != c.dc {
			t.Errorf("ConcentrationDC(%d) = %d, want %d", c.taken, got, c.dc)
		}
	}
}
