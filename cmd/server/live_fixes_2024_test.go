package main

import "testing"

// What the live test of Nov 14 found (4 level 1 characters, three fights on the live server).

func TestWeaponProficiencyByShopName(t *testing.T) {
	for _, name := range []string{"Longsword", "longsword", "Greataxe +1", "Light Crossbow", "light_crossbow"} {
		if !isWeaponProficient("simple, martial", name) {
			t.Errorf("a character proficient with simple and martial weapons should be proficient with %q", name)
		}
	}
	if isWeaponProficient("simple", "Longsword") {
		t.Error("a Longsword is a Martial weapon: simple proficiency doesn't cover it")
	}
}

func TestExtraDamageOnlyWithAdvantage(t *testing.T) {
	got := attackExtraDamage("Melee Attack Roll: +4, reach 5 ft. Hit: 5 (1d6 + 2) Slashing damage, plus 2 (1d4) Slashing damage if the attack roll had Advantage.")
	if len(got) != 1 || !got[0].IfAdvantage || got[0].Dice != "1d4" {
		t.Fatalf("Goblin Warrior's extra damage should be marked as needing Advantage: %+v", got)
	}
	got = attackExtraDamage("Melee Attack Roll: +11, reach 10 ft. Hit: 21 (4d6 + 7) Slashing damage plus 10 (3d6) Fire damage.")
	if len(got) != 1 || got[0].IfAdvantage {
		t.Fatalf("plain extra damage is always dealt: %+v", got)
	}
}

func TestSpellHarms(t *testing.T) {
	if !spellHarms(SRDSpell{DamageDice: "1d8"}) || !spellHarms(SRDSpell{SavingThrow: "WIS"}) {
		t.Error("a spell with damage or a saving throw harms its target")
	}
	if spellHarms(SRDSpell{Healing: "2d4"}) {
		t.Error("a healing spell does not harm")
	}
}
