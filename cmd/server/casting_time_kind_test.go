package main

import "testing"

// castingTimeKind decides whether a cast costs the Reaction or the Bonus Action (spellEconomyAction).
func TestCastingTimeKind(t *testing.T) {
	for in, want := range map[string]string{
		"Reaction": "reaction", "Reaction, which you take when you are hit by an attack roll": "reaction", "1 reaction": "reaction", " One Reaction ": "reaction",
		"Bonus Action": "bonus action", "1 bonus action": "bonus action", "Action": "", "Action or Ritual": "", "1 minute": "", "": "",
		"1 action": "",
	} {
		if got := castingTimeKind(in); got != want {
			t.Errorf("castingTimeKind(%q) = %q, want %q", in, got, want)
		}
	}
}
