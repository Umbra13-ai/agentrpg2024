package main

import "testing"

func TestConditionBase(t *testing.T) {
	cases := map[string]string{"charmed:12": "charmed", " Prone ": "prone", "sanctuary:14": "sanctuary", "grappled": "grappled"}
	for in, want := range cases {
		if got := conditionBase(in); got != want {
			t.Errorf("conditionBase(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []string{"poisoned", "charmed:3", "unconscious", "invisible"} {
		if !isRuleCondition(c) {
			t.Errorf("%s is a rules condition", c)
		}
	}
	for _, c := range []string{"raging", "reckless", "dodging", "hidden", "sanctuary:12", "sacred_weapon:2:100", "exhaustion:2"} {
		if isRuleCondition(c) {
			t.Errorf("%s is a marker, not a rules condition", c)
		}
	}
}
