package main

import (
	"strings"
	"testing"
)

func TestParseSaveEffect(t *testing.T) {
	fear := "The pit fiend emanates an aura in a 20foot Emanation while it doesn’t have the Incapacitated condition. Wisdom Saving Throw: DC 21, any enemy that starts its turn in the aura. Failure: The target has the Frightened condition until the start of its next turn. Success: The target is immune to this pit fiend’s aura for 24 hours."
	e, ok := parseSaveEffect(fear)
	if !ok || e.Ability != "wis" || e.DC != 21 || len(e.Conditions) != 1 || e.Conditions[0] != "frightened" || !e.UntilStart || !e.Immunity || e.Emanation != 20 || e.Extra != "" {
		t.Errorf("Fear Aura: %+v", e)
	}
	m := startAuraRe.FindStringSubmatch(fear)
	if m == nil || m[1] != "enemy" {
		t.Errorf("Fear Aura is a start-of-turn aura: %v", m)
	}
	insects := "Dexterity Saving Throw: DC 17, one creature the dragon can see within 120 feet. Failure: 22 (4d10) Poison damage, and the target has Disadvantage on saving throws to maintain Concentration until the end of its next turn. Success: Half damage."
	e, _ = parseSaveEffect(insects)
	if len(e.Damage) != 1 || e.Damage[0].Dice != "4d10" || e.Damage[0].Type != "poison" || !e.HalfOnSuccess || e.Ability != "dex" {
		t.Errorf("Cloud of Insects: %+v", e)
	}
	if !strings.Contains(e.Extra, "Concentration") {
		t.Errorf("the Concentration rider is for the GM: %q", e.Extra)
	}
	lich := "Constitution Saving Throw: DC 20, each creature that isn’t an Undead in a 20-foot Emanation originating from the lich. Failure: 31 (9d6) Necrotic damage. Success: Half damage. Failure or Success: The lich can’t take this action again until the start of its next turn."
	e, _ = parseSaveEffect(lich)
	if e.Emanation != 20 || !e.HalfOnSuccess || len(e.Damage) != 1 || !strings.Contains(e.Extra, "Failure or Success") {
		t.Errorf("Disrupt Life: %+v", e)
	}
	mace := "Melee Attack Roll: +14, reach 10 ft. Hit: 22 (4d6 + 8) Force damage plus 21 (6d6) Fire damage."
	if _, ok := parseSaveEffect(mace); ok {
		t.Error("an attack is not a saving throw effect")
	}
	if !isAttackEntry(mace) {
		t.Error("an attack entry")
	}
	weight := "Constitution Saving Throw: DC 16, one creature the sphinx can see within 120 feet. Failure: The target gains 1 Exhaustion level."
	if e, _ := parseSaveEffect(weight); e.Exhaustion != 1 {
		t.Errorf("Weight of Years: %+v", e)
	}
	rakshasa := "Wisdom Saving Throw: DC 18, each enemy in a 30-foot Emanation originating from the rakshasa. Failure: 28 (8d6) Psychic damage, and the target has the Frightened and Incapacitated conditions until the start of the rakshasa’s next turn."
	if e, _ := parseSaveEffect(rakshasa); len(e.Conditions) != 2 || e.Rounds != 1 {
		t.Errorf("Baleful Command: %+v", e)
	}
}

func TestMonsterTextPatterns(t *testing.T) {
	if m := mfxAttackRe.FindStringSubmatch("The tarrasque moves up to half its Speed, and it makes one Claw or Tail attack."); m == nil || m[1] != "Claw" || m[2] != "Tail" {
		t.Errorf("one X or Y attack: %v", m)
	}
	if m := mfxSpellRe.FindStringSubmatch("The dragon uses Spellcasting to cast Scorching Ray (level 3 version). The dragon can’t take this action again until the start of its next turn."); m == nil || m[1] != "Scorching Ray" || m[2] != "3" {
		t.Errorf("spell: %v", m)
	}
	if m := mfxSpellRe.FindStringSubmatch("The dragon casts Fear, requiring no Material components and using Charisma as the spellcasting ability (spell save DC 14)."); m == nil || m[1] != "Fear" {
		t.Errorf("casts: %v", m)
	}
	if dc, atk := monsterSpellNumbers("(spell save DC 14)", nil); dc != 14 || atk != 6 {
		t.Errorf("spell numbers: %d %d", dc, atk)
	}
	if m := endAuraRe.FindStringSubmatch("At the end of each of the balor’s turns, each creature in a 5-foot Emanation originating from the balor takes 13 (3d8) Fire damage."); m == nil || m[2] != "5" || m[4] != "3d8" {
		t.Errorf("Fire Aura: %v", m)
	}
	if m := regenBlockRe.FindStringSubmatch("The troll regains 15 Hit Points at the start of each of its turns. If the troll takes Acid or Fire damage, this trait doesn’t function on the troll’s next turn."); m == nil || m[1] != "Acid" {
		t.Errorf("Regeneration block: %v", m)
	}
	if !legendaryOnceRe.MatchString("The dragon can’t take this action again until the start of its next turn.") {
		t.Error("not again until its next turn")
	}
	if stripUsage("Fire Breath (Recharge 5–6)") != "Fire Breath" || rechargeRe.FindStringSubmatch("Fire Breath (Recharge 5–6)")[1] != "5" {
		t.Error("Recharge name")
	}
}

func TestMonsterMeta(t *testing.T) {
	d := monsterMetaFor("adult-red-dragon")
	if d == nil || d.LAUses != 3 || d.LAUsesLair != 4 || d.LR != 3 || d.LRLair != 4 || len(d.Legendary) != 3 {
		t.Fatalf("adult red dragon: %+v", d)
	}
	pf := monsterMetaFor("pit-fiend")
	if pf == nil || pf.LR != 4 || pf.LRLair != 4 || pf.LAUses != 0 {
		t.Errorf("pit fiend: %+v", pf)
	}
	n := 0
	for _, m := range monsterMetaMap {
		if len(m.Legendary) > 0 {
			n++
			if m.LAUses == 0 {
				t.Errorf("%s has Legendary Actions but no uses", m.Name)
			}
			for _, a := range m.Legendary { // every Legendary Action is one the server resolves or explains
				if a.Text == "" {
					t.Errorf("%s: %s has no text", m.Name, a.Name)
				}
			}
		}
	}
	if n != 30 {
		t.Errorf("30 SRD monsters have Legendary Actions, got %d", n)
	}
}
