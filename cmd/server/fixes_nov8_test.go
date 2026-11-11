package main

// Unit tests for the pure helpers added on Nov 8, 2026 (level 20 battle tests): they guard the fixes F22-F31 and the features built
// after them against regressions without a database.

import "testing"

func TestAttackExtraDamage(t *testing.T) {
	got := attackExtraDamage("Melee Attack Roll: +11, reach 10 ft. Hit: 21 (4d6 + 7) Slashing damage plus 10 (3d6) Fire damage.")
	if len(got) != 1 || got[0].Dice != "3d6" || got[0].Type != "Fire" {
		t.Fatalf("Flame Sword extra damage: got %+v, want 3d6 Fire", got)
	}
	if got := attackExtraDamage("Melee Attack Roll: +9, reach 5 ft. Hit: 8 (1d8 + 4) Bludgeoning damage."); len(got) != 0 {
		t.Errorf("no extra damage: got %+v", got)
	}
	// "plus" before the Hit: line isn't damage of the hit
	got = attackExtraDamage("Ranged Attack Roll: +11, range 60/240 ft. Hit: 23 (3d10 + 7) Bludgeoning damage plus 4 (1d8) Fire damage, and the target is pushed")
	if len(got) != 1 || got[0].Dice != "1d8" {
		t.Errorf("Hammer Throw: got %+v, want 1d8 Fire", got)
	}
	got = attackExtraDamage("Hit: 13 (2d8 + 4) Piercing damage plus 7 (2d6 + 0) Poison damage.")
	if len(got) != 1 || got[0].Dice != "2d6+0" {
		t.Errorf("a bonus inside the parentheses: got %+v", got)
	}
}

func TestCastDealsNoDamage(t *testing.T) {
	for _, s := range []string{"wish", "meld-into-stone", "dimension-door", "teleport", "contact-other-plane", "geas", "hunters-mark", "hex"} {
		if !castDealsNoDamage(s) {
			t.Errorf("%s should deal no damage to a target when cast", s)
		}
	}
	for _, s := range []string{"fireball", "disintegrate", "power-word-kill", "magic-missile"} {
		if castDealsNoDamage(s) {
			t.Errorf("%s deals damage when cast", s)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := map[string]string{"fell glare": "Fell Glare", "grovel": "Grovel", "": "", "acid-splash": "Acid-Splash", "already Up": "Already Up", "x": "X"}
	for in, want := range cases {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompanionSpeedFeet(t *testing.T) {
	cases := map[string]int{"60 ft., Fly 60 ft.": 60, "5 ft., Fly 60 ft.": 60, "30 ft., Fly 60 ft., Swim 30 ft.": 60, "40 ft., Climb 40 ft.": 40, "": 30}
	for speed, want := range cases {
		if got := companionSpeedFeet(companion{Speed: speed}); got != want {
			t.Errorf("speed %q: got %d, want %d", speed, got, want)
		}
	}
}

func TestAnimatedObjectSize(t *testing.T) {
	cases := []struct {
		desc       string
		slot, mod  int
		size       string
		counts, hp int
		slam       string
	}{
		{"animate the chairs", 5, 5, "Medium", 1, 10, "1d4+3"},
		{"a large statue", 5, 5, "Large", 2, 20, "2d6+8"},
		{"huge statues", 5, 5, "Huge", 3, 40, "2d12+8"},
		{"huge statues", 7, 5, "Huge", 3, 40, "4d12+8"},
		{"brooms", 6, 4, "Medium", 1, 10, "2d4+3"},
	}
	for _, c := range cases {
		size, counts, hp, slam := animatedObjectSize(c.desc, c.slot, c.mod)
		if size != c.size || counts != c.counts || hp != c.hp || slam != c.slam {
			t.Errorf("%q slot %d mod %d: got %s %d %d %s, want %s %d %d %s", c.desc, c.slot, c.mod, size, counts, hp, slam, c.size, c.counts, c.hp, c.slam)
		}
	}
}

func TestStripUsageAndMultiattack(t *testing.T) {
	if got := stripUsage("Grave Strike (Vampire Form Only)"); got != "Grave Strike" {
		t.Errorf("stripUsage: got %q", got)
	}
	actions := []monsterActionRow{
		{Name: "Multiattack (Vampire Form Only)", Desc: "The vampire makes two Grave Strike attacks and uses Bite."},
		{Name: "Grave Strike (Vampire Form Only)", AttackBonus: 9, DamageDice: "1d8+4", DamageType: "Bludgeoning"},
		{Name: "Bite (Bat or Vampire Form Only)"},
	}
	plan := multiattackPlan(actions[0].Desc, actions)
	if len(plan) != 2 || plan[0].DamageDice != "1d8+4" {
		t.Errorf("Vampire Multiattack: got %d attacks %+v, want two Grave Strikes", len(plan), plan)
	}
}

func TestWildShapeLimits(t *testing.T) {
	cases := []struct {
		level int
		known int
		cr    float64
		fly   bool
	}{{2, 4, 0.25, false}, {4, 6, 0.5, false}, {7, 6, 0.5, false}, {8, 8, 1, true}, {20, 8, 1, true}}
	for _, c := range cases {
		k, cr, fly := wildShapeLimits(c.level)
		if k != c.known || cr != c.cr || fly != c.fly {
			t.Errorf("level %d: got %d %v %v", c.level, k, cr, fly)
		}
	}
	if crValue("1/4") != 0.25 || crValue("1/2") != 0.5 || crValue("1") != 1 || crValue("1/8") != 0.125 {
		t.Error("crValue fractions")
	}
}

func TestBeastAttackPlan(t *testing.T) {
	attacks := []compAttack{{Name: "Bite", Dice: "1d8+4"}, {Name: "Claw", Dice: "2d6+4"}}
	acts := []map[string]interface{}{{"name": "Multiattack", "desc": "The bear makes one Bite attack and one Claw attack."}}
	plan := beastAttackPlan(attacks, acts, "")
	if len(plan) != 2 || plan[0].Name != "Bite" || plan[1].Name != "Claw" {
		t.Errorf("Brown Bear Multiattack: got %+v", plan)
	}
	if plan := beastAttackPlan(attacks, nil, "claw it"); len(plan) != 1 || plan[0].Name != "Claw" {
		t.Errorf("a named attack without Multiattack: got %+v", plan)
	}
}
