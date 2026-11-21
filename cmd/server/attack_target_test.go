package main

import "testing"

func TestAttackOutcomeIsTakenOnce(t *testing.T) {
	recordAttackOutcome(42, attackOutcome{Weapon: "Dagger", Total: 17, Natural: 14, Damage: 5, DamageType: "piercing"})
	o, ok := takeAttackOutcome(42)
	if !ok || o.Total != 17 || o.Damage != 5 || o.DamageType != "piercing" {
		t.Fatalf("outcome not kept: %+v %v", o, ok)
	}
	if _, again := takeAttackOutcome(42); again {
		t.Error("an outcome must be used only once")
	}
	if _, none := takeAttackOutcome(999); none {
		t.Error("no outcome for a character that did not attack")
	}
}

func TestAsInt(t *testing.T) {
	if asInt(float64(15)) != 15 || asInt(7) != 7 || asInt("x") != 0 || asInt(nil) != 0 {
		t.Error("asInt")
	}
}

func TestAttackDamageType(t *testing.T) {
	if attackDamageType(false, SRDWeapon{}) != "bludgeoning" {
		t.Error("unarmed strikes are bludgeoning")
	}
	if attackDamageType(true, SRDWeapon{DamageType: "Slashing"}) != "slashing" {
		t.Error("weapon damage type, lowercase")
	}
}
