package main

import "testing"

func TestHasWordWholeWords(t *testing.T) {
	cases := []struct {
		text, word string
		want       bool
	}{
		{"a forceful punch", "force", false},
		{"unarmed strike, force", "force", true},
		{"cast it free", "free", true},
		{"freedom of movement on Kestrel", "free", false},
		{"be careful", "careful", true},
		{"carefully aimed fireball", "careful", false},
		{"fireball [sculpt, signature]", "sculpt", true},
		{"Fireball, Signature spell", "signature", true},
		{"kickstand", "kick", false},
		{"cunning strike trip", "cunning strike", true},
	}
	for _, c := range cases {
		if got := hasWord(c.text, c.word); got != c.want {
			t.Errorf("hasWord(%q, %q) = %v, want %v", c.text, c.word, got, c.want)
		}
	}
}

func TestFeaturePattern(t *testing.T) {
	cases := []struct {
		pattern, feature string
		want             bool
	}{
		{"cunning strike <effect>", "cunning strike trip", true},
		{"cunning strike <effect>", "cunning strike", false},
		{"absorb level N", "absorb level 3", true},
		{"absorb level N", "absorb level x", false},
		{"N beads", "3 beads", true},
		{"sculpt", "sculpt", true},
		{"sculpt", "sculpture", false},
	}
	for _, c := range cases {
		if got := featurePattern(c.pattern).MatchString(c.feature); got != c.want {
			t.Errorf("featurePattern(%q) on %q = %v, want %v", c.pattern, c.feature, got, c.want)
		}
	}
}
