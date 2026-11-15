package game

import (
	"strings"
	"testing"
)

// Advantage and Disadvantage on a saving throw follow the D20 Test rule: any number of one and none of the other, or both
// cancelling into one d20.
func TestSavingThrowAdvantageAndDisadvantage(t *testing.T) {
	cases := []struct {
		adv, dis []string
		mode     string
	}{
		{nil, nil, ""},
		{[]string{"Danger Sense"}, nil, "advantage"},
		{[]string{"Danger Sense", "Haste"}, nil, "advantage"},
		{nil, []string{"Restrained"}, "disadvantage"},
		{[]string{"Haste"}, []string{"Restrained"}, "cancelled"},
		{[]string{"Haste", "Foresight", "Danger Sense"}, []string{"Restrained"}, "cancelled"},
	}
	for _, c := range cases {
		s := SavingThrow{Ability: "dex", DC: 15, Bonus: 3, Advantage: c.adv, Disadvantage: c.dis}
		if s.Mode() != c.mode {
			t.Errorf("%v / %v: mode %q, want %q", c.adv, c.dis, s.Mode(), c.mode)
		}
		for i := 0; i < 200; i++ {
			r := s.Roll()
			switch c.mode {
			case "advantage":
				if r.Dice[1] == 0 || r.Natural != maxOf(r.Dice[0], r.Dice[1]) {
					t.Fatalf("Advantage keeps the higher of two d20s: %+v", r)
				}
			case "disadvantage":
				if r.Dice[1] == 0 || r.Natural != minOf(r.Dice[0], r.Dice[1]) {
					t.Fatalf("Disadvantage keeps the lower of two d20s: %+v", r)
				}
			default:
				if r.Dice[1] != 0 || r.Natural != r.Dice[0] {
					t.Fatalf("one d20 when neither applies or both cancel: %+v", r)
				}
			}
			if r.Natural < 1 || r.Natural > 20 || r.Total != r.Natural+3 || r.Success != (r.Total >= 15) {
				t.Fatalf("judged wrong: %+v", r)
			}
		}
	}
	if !(SavingThrow{Advantage: []string{"x"}}).HasAdvantage() || (SavingThrow{Advantage: []string{"x"}, Disadvantage: []string{"y"}}).HasAdvantage() {
		t.Error("HasAdvantage")
	}
}

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minOf(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// A natural 1 or 20 means nothing on a saving throw (SRD 5.2.1: only an attack roll reads them): the total against the DC decides.
func TestSavingThrowNaturalRollsHaveNoSpecialMeaning(t *testing.T) {
	if r := (SavingThrow{Ability: "wis", DC: 10, Bonus: 12}).Judge(1); !r.Success || r.Total != 13 {
		t.Errorf("a natural 1 with a total of 13 against DC 10 succeeds: %+v", r)
	}
	if r := (SavingThrow{Ability: "wis", DC: 25, Bonus: 2}).Judge(20); r.Success || r.Total != 22 {
		t.Errorf("a natural 20 with a total of 22 against DC 25 fails: %+v", r)
	}
	if r := (SavingThrow{Ability: "con", DC: 14, Bonus: 2}).Judge(12); !r.Success {
		t.Errorf("equal to the DC succeeds: %+v", r)
	}
	if r := (SavingThrow{Ability: DeathSave, DC: 10}).Judge(10); !r.Success || r.Save.Name() != "Death saving throw" {
		t.Errorf("a Death Saving Throw of 10 succeeds: %+v", r)
	}
}

// Adjustments are added to the total and named; a feature that replaces the die or the total judges again.
func TestSavingThrowAdjustmentsAndChanges(t *testing.T) {
	s := SavingThrow{Ability: "dex", DC: 15, Bonus: 2, Adjust: []RollAdjust{{3, "Bless +3"}, {2, "Aura of Protection +2 (Uther)"}, {-4, "Exhaustion -4"}}}
	if s.Modifier() != 3 {
		t.Fatalf("modifier %d", s.Modifier())
	}
	r := s.Judge(11)
	if r.Total != 14 || r.Success {
		t.Fatalf("11 + 2 + 3 + 2 - 4 = 14 against DC 15 fails: %+v", r)
	}
	r = r.Replace(13, "Halfling Luck: 11 → 13")
	if r.Total != 16 || !r.Success || len(r.Changed) != 1 {
		t.Fatalf("a replaced die is judged again: %+v", r)
	}
	r = r.WithTotal(12, "Indomitable Might")
	if r.Success || r.Total != 12 || len(r.Changed) != 2 {
		t.Fatalf("a replaced total is judged again: %+v", r)
	}
	again := s.Judge(1).RollAgain("Heroic Inspiration")
	if len(again.Changed) != 1 || !strings.HasPrefix(again.Changed[0], "Heroic Inspiration: ") || again.Total != again.Natural+3 {
		t.Fatalf("a save rolled again keeps its modifier and says why: %+v", again)
	}
}

// An automatic failure needs no roll; an automatic success (Sculpt Spells) wins over it.
func TestSavingThrowAutomatic(t *testing.T) {
	fail := SavingThrow{Ability: "dex", DC: 5, Bonus: 20, AutoFail: "Paralyzed"}
	for i := 0; i < 50; i++ {
		if r := fail.Roll(); r.Success || r.Rolled() || r.Natural != 0 || r.Outcome() != "AUTO-FAIL" {
			t.Fatalf("auto-fail: %+v", r)
		}
	}
	both := fail
	both.AutoSucceed = "Sculpt Spells"
	if r := both.Roll(); !r.Success || r.Outcome() != "AUTO-SUCCESS" {
		t.Fatalf("auto-succeed wins: %+v", r)
	}
	if r := fail.Judge(20).Replace(20, "x"); r.Success || len(r.Changed) != 0 {
		t.Fatalf("nothing changes a save that wasn't rolled: %+v", r)
	}
	if got := fail.Roll().Line("Aria"); got != "Aria fails the Dexterity saving throw automatically (Paralyzed)" {
		t.Errorf("auto-fail line: %q", got)
	}
	if got := fail.Roll().ForSaver(""); got != "Dexterity saving throw: AUTO-FAIL (Paralyzed)" {
		t.Errorf("auto-fail for the saver: %q", got)
	}
}

// The wordings: the GM's line, the tail other sentences use, and the saver's own line.
func TestSavingThrowWording(t *testing.T) {
	s := SavingThrow{Ability: "dex", DC: 15, Bonus: 3, Advantage: []string{"Danger Sense"}, Adjust: []RollAdjust{{2, "Bless +2"}}}
	r := SaveResult{Save: s, Natural: 14, Dice: [2]int{9, 14}}.judge()
	if got, want := r.Line("Aria"), "Aria succeeds on the Dexterity saving throw (19 vs DC 15; d20s 9 and 14: 14 + 3) with Advantage (Danger Sense) [Bless +2]"; got != want {
		t.Errorf("line\n got  %q\n want %q", got, want)
	}
	if got, want := r.ForSaver(""), "Dexterity saving throw with Advantage (Danger Sense): d20(9,14→14)+3 [Bless +2] = 19 vs DC 15 → SUCCESS"; got != want {
		t.Errorf("for the saver\n got  %q\n want %q", got, want)
	}
	c := SavingThrow{Ability: "wis", DC: 12, Bonus: -1, Advantage: []string{"Haste"}, Disadvantage: []string{"Restrained"}}.Judge(8)
	if got, want := c.Tail(), " (7 vs DC 12; d20 8 - 1) (Advantage from Haste and Disadvantage from Restrained cancel: one d20)"; got != want {
		t.Errorf("tail\n got  %q\n want %q", got, want)
	}
	if got := (SavingThrow{Ability: "", DC: 10}).Judge(10).Clause(); got != "succeeds on the saving throw (10 vs DC 10; d20 10)" {
		t.Errorf("a save of no known ability: %q", got)
	}
}

// What conditions do to a saving throw (SRD 5.2.1 Conditions).
func TestSaveConditions(t *testing.T) {
	cases := []struct {
		conds     []string
		ability   string
		fail, dis string
	}{
		{nil, "str", "", ""},
		{[]string{"paralyzed"}, "str", "Paralyzed", ""},
		{[]string{"paralyzed"}, "DEX", "Paralyzed", ""},
		{[]string{"paralyzed"}, "wis", "", ""},
		{[]string{"unconscious", "stunned"}, "dex", "Stunned, Unconscious", ""},
		{[]string{"petrified"}, "str", "Petrified", ""},
		{[]string{"prone"}, "str", "", ""},
		{[]string{"restrained"}, "dex", "", "Restrained"},
		{[]string{"restrained"}, "str", "", ""},
		{[]string{"grappled:-3"}, "dex", "", ""},
	}
	for _, c := range cases {
		fail, dis := SaveConditions(c.conds, c.ability)
		if strings.Join(fail, ", ") != c.fail || strings.Join(dis, ", ") != c.dis {
			t.Errorf("%v %s: fail %v dis %v, want %q %q", c.conds, c.ability, fail, dis, c.fail, c.dis)
		}
	}
}
