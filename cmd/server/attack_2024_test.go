package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The result line of an attack (game.AttackRoll.Line) and the pattern that hides a monster's total and the Armor Class from
// players (player_text.go) are written for each other: every form of the line must lose its numbers and keep its words.
func TestPlayerTextHidesEveryAttackLineForm(t *testing.T) {
	digits := regexp.MustCompile(`\d`)
	forms := []game.AttackRoll{
		{Bonus: 5, AC: 15},
		{Bonus: 5, AC: 15, Advantage: []string{"Faerie Fire", "the target is Prone"}},
		{Bonus: 5, AC: 15, Disadvantage: []string{"the target is Dodging", "long range"}},
		{Bonus: 5, AC: 15, Advantage: []string{"Help"}, Disadvantage: []string{"the attacker is Poisoned"}},
		{Bonus: 5, AC: 15, Disadvantage: []string{"Blur"}, Adjust: []game.RollAdjust{{Amount: -2, Why: "Bane -2"}}},
		{Bonus: -1, AC: 15, Adjust: []game.RollAdjust{{Amount: 3, Why: "Bless +3"}}},
	}
	for _, a := range forms {
		for _, nat := range []int{1, 2, 12, 20} {
			o := a.Judge(nat)
			line := a.Line("Scimitar", o)
			got := playerText(line)
			shown := strings.ReplaceAll(strings.ReplaceAll(got, "one d20", ""), "d20", "")
			shown = regexp.MustCompile(`\[[^\]]*\]`).ReplaceAllString(shown, "") // "[Bless +3]" stays: it is the roll's own die
			if digits.MatchString(shown) {
				t.Errorf("a player would read numbers in %q (from %q)", got, line)
			}
			if !strings.HasSuffix(got, o.Label()) || !strings.HasPrefix(got, "Scimitar:") {
				t.Errorf("the line lost its name or its outcome: %q (from %q)", got, line)
			}
			for _, why := range append(append([]string{}, a.Advantage...), a.Disadvantage...) {
				if !strings.Contains(got, why) {
					t.Errorf("the reason %q is gone from %q", why, got)
				}
			}
		}
	}
	// a monster's attack at a target the server doesn't know
	a := game.AttackRoll{Bonus: 4, Disadvantage: []string{"the attacker is Prone"}}
	line := "Scimitar: 14" + a.Describe() + " to hit - 1d6+2 Slashing damage if it hits"
	if got := playerText(line); strings.Contains(got, "14") || !strings.Contains(got, "the attacker is Prone") {
		t.Errorf("unknown-target line: %q", got)
	}
}

// Every source row names itself, so a result line can always say why a roll had Advantage or Disadvantage.
func TestAttackSourcesAreNamed(t *testing.T) {
	adv, dis := 0, 0
	for i, s := range attackSources {
		if s.applies == nil {
			t.Fatalf("source %d has no rule", i)
		}
		if s.adv {
			adv++
		} else {
			dis++
		}
	}
	if adv == 0 || dis == 0 {
		t.Fatalf("the table needs both kinds of source (Advantage %d, Disadvantage %d)", adv, dis)
	}
}
