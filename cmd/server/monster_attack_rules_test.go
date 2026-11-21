package main

import (
	"fmt"
	"strings"
	"testing"
)

// The shared attack-data rules (monster_attacks.go, monster_damage_choice_2024.go): every damage amount of a Hit is represented or
// the whole attack is refused before anything is rolled; a Multiattack is bounded and never turns a choice into both attacks.
// Originated as the independent review's counterexamples (Instinct, 2026-11-17), now regression tests of the rules.

func attackRow(desc string) monsterActionRow {
	return monsterActionRow{Name: "Test Attack", Desc: desc}
}

func TestHitDamageEveryComponentIsRepresentedOrRefused(t *testing.T) {
	cases := []struct {
		name    string
		desc    string
		extras  int
		flat    int // value of the first extra when it is flat
		refused bool
	}{
		{"dice plus dice", "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage plus 10 (3d6) Fire damage.", 1, -1, false},
		{"flat secondary", "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage plus 7 Fire damage.", 1, 7, false},
		{"flat primary and flat secondary", "Melee Attack Roll: +8. Hit: 9 Slashing damage plus 7 Cold damage.", 1, 7, false},
		{"advantage rider", "Melee Attack Roll: +4. Hit: 5 (1d6 + 2) Piercing damage plus 2 (1d4) Slashing damage if the attack roll had Advantage.", 1, -1, false},
		{"unrecognised condition on dice", "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage plus 7 (2d6) Fire damage if the target is Prone.", 0, -1, true},
		{"unrecognised condition on flat", "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage plus 7 Fire damage while the target is Frightened.", 0, -1, true},
		{"damage text without an amount", "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage plus extra Fire damage.", 0, -1, true},
		{"amount joined another way", "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage, then 7 (2d6) Fire damage.", 0, -1, true},
		{"two-handed alternative is not extra damage", "Melee Attack Roll: +4. Hit: 5 (1d6 + 2) Slashing damage, or 6 (1d8 + 2) Slashing damage if used with two hands.", 0, -1, false},
		{"alternative the server cannot judge is kept for the GM", "Melee Attack Roll: +4. Hit: 5 (1d6 + 2) Slashing damage, or 9 (2d6 + 2) Fire damage if the target is a Construct.", 0, -1, false},
		{"rider effect after the sentence is not damage of the hit", "Melee Attack Roll: +4. Hit: 5 (1d6 + 2) Piercing damage. Constitution Saving Throw: DC 12. Failure: 9 (2d8) Poison damage.", 0, -1, false},
	}
	for _, c := range cases {
		p, problem := prepareMonsterAttack(attackRow(c.desc), "")
		if c.refused {
			if problem == "" {
				t.Errorf("%s: accepted, but part of its damage cannot be represented", c.name)
			}
			continue
		}
		if problem != "" {
			t.Errorf("%s: refused: %s", c.name, problem)
			continue
		}
		extras := attackExtraDamage(p.Desc)
		if len(extras) != c.extras {
			t.Errorf("%s: %d extra components, want %d", c.name, len(extras), c.extras)
			continue
		}
		if c.flat >= 0 && (extras[0].Flat == nil || *extras[0].Flat != c.flat || extras[0].roll(false) != c.flat || extras[0].roll(true) != c.flat) {
			t.Errorf("%s: flat extra not carried as a fixed %d: %+v", c.name, c.flat, extras[0])
		}
	}
}

func TestFlatSecondaryDamageIsDealtByTheSharedRoll(t *testing.T) {
	seven := 7
	x := extraDamage{Type: "Fire", Flat: &seven}
	if !x.valid() || x.roll(true) != 7 {
		t.Fatal("a flat component must be valid and unaffected by a critical hit")
	}
}

func multiRows() []monsterActionRow {
	return []monsterActionRow{
		{Name: "Bite", Desc: "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Piercing damage."},
		{Name: "Claw", Desc: "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Slashing damage."},
		{Name: "Slam", Desc: "Melee Attack Roll: +8. Hit: 12 (2d6 + 5) Bludgeoning damage."},
	}
}

func TestMultiattackPlanReadsCountsAndBoundsThem(t *testing.T) {
	plan, problem := multiattackPlan("The creature makes two Claw attacks and one Bite attack.", multiRows())
	if problem != "" || len(plan) != 3 {
		t.Fatalf("plan %d problem %q, want three attacks", len(plan), problem)
	}
	for _, n := range []string{"10001", "21", "99999999999999999999"} {
		if plan, problem := multiattackPlan("The creature makes "+n+" Slam attacks.", multiRows()); problem == "" || len(plan) != 0 {
			t.Errorf("%s Slam attacks: planned %d, want a refusal", n, len(plan))
		}
	}
	if plan, problem := multiattackPlan("The creature makes 20 Slam attacks.", multiRows()); problem != "" || len(plan) != 20 {
		t.Errorf("the cap itself is allowed: %d %q", len(plan), problem)
	}
	// the cap covers the whole plan, not each entry
	if _, problem := multiattackPlan("The creature makes 12 Slam attacks and 12 Claw attacks.", multiRows()); problem == "" {
		t.Error("a plan above the cap through several entries was accepted")
	}
}

func TestMultiattackAlternativesAreNeverCombined(t *testing.T) {
	for _, text := range []string{
		"The creature makes one Bite attack or one Claw attack.",
		"The creature makes two attacks, using Bite or Claw in any combination.",
		"The creature makes either one Bite attack or two Claw attacks.",
	} {
		if plan, problem := multiattackPlan(text, multiRows()); problem == "" {
			t.Errorf("%q: planned %d attacks instead of refusing the open choice", text, len(plan))
		} else if !strings.Contains(problem, "Nothing was rolled") {
			t.Errorf("%q: refusal does not say nothing was rolled: %s", text, problem)
		}
	}
}

func TestImportRefusesOverCapMultiattack(t *testing.T) {
	for _, n := range []int{maxMultiattackAttacks + 1, 10001} {
		m := compendiumFixture()
		m.Actions = append([]srdMonsterEntry{{Name: "Multiattack", Text: fmt.Sprintf("The warden makes %d Slam attacks.", n)}}, m.Actions...)
		m.Actions = append(m.Actions, srdMonsterEntry{Name: "Slam", Text: "Melee Attack Roll: +15, reach 10 ft. Hit: 21 (3d8 + 8) Force damage."})
		if validateCustomMonster(m) == "" {
			t.Errorf("a Multiattack of %d attacks was imported", n)
		}
	}
}

func TestConditionalAlternativeDamage(t *testing.T) {
	type tc struct {
		name, desc string
		h          hitCondition
		wantDice   string
		wantType   string
	}
	chimera := "Melee Attack Roll: +7, reach 5 ft. Hit: 11 (2d6 + 4) Piercing damage, or 18 (4d6 + 4) Piercing damage if the chimera had Advantage on the attack roll."
	mimic := "Melee Attack Roll: +5, reach 5 ft. Hit: 7 (1d8 + 3) Piercing damage\u2014or 12 (2d8 + 3) Piercing damage if the target is Grappled by the mimic\u2014plus 4 (1d8) Acid damage."
	swarm := "Melee Attack Roll: +4, reach 5 ft. Hit: 5 (2d4) Piercing damage, or 2 (1d4) Piercing damage if the swarm is Bloodied."
	hawk := "Melee Attack Roll: +4, reach 5 ft. Hit: 4 (1d4 + 2) Piercing damage, or 6 (1d8 + 2) Piercing damage if the target is Bloodied."
	for _, c := range []tc{
		{"advantage", chimera, hitCondition{Advantage: true}, "4d6+4", "Piercing"},
		{"no advantage", chimera, hitCondition{}, "2d6+4", "Piercing"},
		{"grappled by the attacker", mimic, hitCondition{TargetGrappledByAttacker: true}, "2d8+3", "Piercing"},
		{"grappled by someone else", mimic, hitCondition{TargetGrappled: true}, "1d8+3", "Piercing"},
		{"attacker bloodied", swarm, hitCondition{AttackerBloodied: true}, "1d4", "Piercing"},
		{"attacker not bloodied", swarm, hitCondition{TargetBloodied: true}, "2d4", "Piercing"},
		{"target bloodied", hawk, hitCondition{TargetBloodied: true}, "1d8+2", "Piercing"},
		{"target not bloodied", hawk, hitCondition{AttackerBloodied: true}, "1d4+2", "Piercing"},
	} {
		p, problem := prepareMonsterAttack(attackRow(c.desc), "")
		if problem != "" {
			t.Fatalf("%s: %s", c.name, problem)
		}
		got := applyDamageAlternatives(p, c.h)
		if got.DamageDice != c.wantDice || got.DamageType != c.wantType {
			t.Errorf("%s: dealt %s %s, want %s %s", c.name, got.DamageDice, got.DamageType, c.wantDice, c.wantType)
		}
	}
	// the Mimic's plain Acid rider is still an extra component beside the alternative
	if p, _ := prepareMonsterAttack(attackRow(mimic), ""); len(attackExtraDamage(p.Desc)) != 1 {
		t.Error("the Mimic's Acid damage was lost")
	}
	// a path that cannot judge conditions refuses instead of dealing the wrong amount
	if _, problem := prepareUnconditionalAttack(attackRow(chimera), ""); problem == "" {
		t.Error("an attack with conditional damage was accepted by a path that cannot judge it")
	}
	// what the server cannot judge is reported, not dropped
	ram := "Melee Attack Roll: +3, reach 5 ft. Hit: 4 (1d4 + 2) Bludgeoning damage, or 6 (1d8 + 2) Bludgeoning damage if the goat moved 20+ feet straight toward the target immediately before the hit."
	p, problem := prepareMonsterAttack(attackRow(ram), "")
	if problem != "" || !strings.Contains(gmJudgedAlternatives(p), "moved 20+ feet") || unappliedHitRider(ram) != "" {
		t.Errorf("GM-judged alternative: %q %q %q", problem, gmJudgedAlternatives(p), unappliedHitRider(ram))
	}
	// the typed drain is applied on every hit route now (hp_max_drain_2024.go): nothing is left for the GM; a sentence nothing applies is
	clay := "Melee Attack Roll: +9, reach 5 ft. Hit: 10 (1d10 + 5) Bludgeoning damage plus 6 (1d12) Acid damage, and the target's Hit Point maximum decreases by an amount equal to the Acid damage taken."
	if got := unappliedHitRider(clay); got != "" {
		t.Errorf("an applied drain was reported to the GM: %q", got)
	}
	if got := unappliedHitRider("Melee Attack Roll: +9, reach 5 ft. Hit: 10 (1d10 + 5) Bludgeoning damage, and the target's armor rusts away."); !strings.Contains(got, "rusts") {
		t.Errorf("rider text not reported: %q", got)
	}
}

func TestMultiattackOptionsAfterTheFirstSentenceAreHandedToTheGM(t *testing.T) {
	got := multiattackExtraText("The dracolich makes three Rend attacks. It can replace one attack with a use of Spellcasting to cast Ray of Sickness.")
	if !strings.Contains(got, "replace one attack") {
		t.Errorf("option text dropped: %q", got)
	}
	if multiattackExtraText("The vampire makes two Grave Strike attacks and uses Bite.") != "" {
		t.Error("a single-sentence Multiattack has no extra text")
	}
}
