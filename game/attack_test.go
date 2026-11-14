package game

import "testing"

func TestAttackRollMode(t *testing.T) {
	cases := []struct {
		adv, dis []string
		want     string
	}{
		{nil, nil, ""},
		{[]string{"Faerie Fire"}, nil, "advantage"},
		{nil, []string{"long range"}, "disadvantage"},
		{[]string{"Faerie Fire"}, []string{"long range"}, "cancelled"},
		// "even if multiple circumstances impose Disadvantage and only one grants Advantage"
		{[]string{"Faerie Fire"}, []string{"long range", "the target is Dodging", "Blur"}, "cancelled"},
		{[]string{"a", "b", "c"}, []string{"d"}, "cancelled"},
	}
	for _, c := range cases {
		a := AttackRoll{Advantage: c.adv, Disadvantage: c.dis}
		if a.Mode() != c.want {
			t.Errorf("adv %v dis %v: mode %q, want %q", c.adv, c.dis, a.Mode(), c.want)
		}
		if a.HasAdvantage() != (c.want == "advantage") || a.HasDisadvantage() != (c.want == "disadvantage") {
			t.Errorf("adv %v dis %v: HasAdvantage/HasDisadvantage disagree with the mode", c.adv, c.dis)
		}
	}
}

// Every natural roll against a range of Armor Classes: a natural 1 misses, a natural 20 hits, otherwise total vs AC; a
// Critical Hit only on a hit, on a 20, in the critical range, or always with AutoCrit.
func TestAttackRollJudgeEveryRoll(t *testing.T) {
	for _, critFrom := range []int{0, 20, 19, 18} {
		for _, auto := range []bool{false, true} {
			for ac := 5; ac <= 30; ac += 5 {
				for nat := 1; nat <= 20; nat++ {
					a := AttackRoll{Bonus: 4, AC: ac, CritFrom: critFrom, AutoCrit: auto, Adjust: []RollAdjust{{2, "Bless +2"}, {-1, "Bane -1"}}}
					o := a.Judge(nat)
					total := nat + 4 + 2 - 1
					hit := nat != 1 && (nat == 20 || total >= ac)
					from := critFrom
					if from == 0 {
						from = 20
					}
					crit := hit && (nat >= from || auto)
					if o.Total != total || o.Hit != hit || o.Critical != crit {
						t.Errorf("nat %d vs AC %d (critFrom %d, autoCrit %v): total %d hit %v crit %v, want %d %v %v", nat, ac, critFrom, auto, o.Total, o.Hit, o.Critical, total, hit, crit)
					}
				}
			}
		}
	}
}

func TestAttackRollRollsTwoDiceOnlyWithAdvantageOrDisadvantage(t *testing.T) {
	for i := 0; i < 300; i++ {
		o := AttackRoll{AC: 10}.Roll()
		if o.Dice[1] != 0 || o.Natural != o.Dice[0] || o.Mode != "" {
			t.Fatalf("a plain roll is one d20: %+v", o)
		}
		o = AttackRoll{AC: 10, Advantage: []string{"x"}, Disadvantage: []string{"y"}}.Roll()
		if o.Dice[1] != 0 || o.Mode != "cancelled" {
			t.Fatalf("Advantage and Disadvantage together are one d20: %+v", o)
		}
		o = AttackRoll{AC: 10, Advantage: []string{"x"}}.Roll()
		if o.Dice[1] == 0 || o.Natural != maxInt(o.Dice[0], o.Dice[1]) {
			t.Fatalf("Advantage keeps the higher of two d20s: %+v", o)
		}
		o = AttackRoll{AC: 10, Disadvantage: []string{"y"}}.Roll()
		if o.Dice[1] == 0 || o.Natural != minInt(o.Dice[0], o.Dice[1]) {
			t.Fatalf("Disadvantage keeps the lower of two d20s: %+v", o)
		}
	}
	if o := (AttackRoll{AC: 30, AutoHit: true, AutoCrit: true}).Roll(); !o.Hit || o.Critical || !o.AutoHit {
		t.Errorf("an automatic hit hits and is never a Critical Hit: %+v", o)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestAttackRollDescribeAndLine(t *testing.T) {
	cases := []struct {
		a    AttackRoll
		want string
	}{
		{AttackRoll{}, ""},
		{AttackRoll{Advantage: []string{"Faerie Fire", "the target is Prone"}}, " with Advantage (Faerie Fire, the target is Prone)"},
		{AttackRoll{Disadvantage: []string{"the target is Dodging", "long range"}}, " with Disadvantage (the target is Dodging, long range)"},
		{AttackRoll{Advantage: []string{"Faerie Fire"}, Disadvantage: []string{"long range"}}, " (Advantage from Faerie Fire and Disadvantage from long range cancel: one d20)"},
		{AttackRoll{Disadvantage: []string{"Blur"}, Adjust: []RollAdjust{{3, "Bless +3"}}}, " with Disadvantage (Blur) [Bless +3]"},
	}
	for _, c := range cases {
		if got := c.a.Describe(); got != c.want {
			t.Errorf("Describe() = %q, want %q", got, c.want)
		}
	}
	a := AttackRoll{Bonus: 5, AC: 15, Disadvantage: []string{"the target is Dodging"}}
	// the note is on the line whether the attack hits or misses (Nov 14: one path dropped it on a hit)
	if got := a.Line("Ray of Sickness", a.Judge(12)); got != "Ray of Sickness: 17 with Disadvantage (the target is Dodging) vs AC 15 - HIT" {
		t.Errorf("hit line: %q", got)
	}
	if got := a.Line("Ray of Sickness", a.Judge(3)); got != "Ray of Sickness: 8 with Disadvantage (the target is Dodging) vs AC 15 - MISS" {
		t.Errorf("miss line: %q", got)
	}
	if got := a.Line("Ray of Sickness", a.Judge(20)); got != "Ray of Sickness: 25 with Disadvantage (the target is Dodging) vs AC 15 - CRITICAL HIT" {
		t.Errorf("critical line: %q", got)
	}
}
