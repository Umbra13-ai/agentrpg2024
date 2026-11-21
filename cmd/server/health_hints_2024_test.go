package main

import (
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The hints follow the health owner (I-ALDRIC-1, I-ALDRIC-5): a dead ally gets no help hint; a dying one says what keeps it alive,
// a stable one that healing wakes it; a death save is suggested only to a dying character, and in a fight it says the server rolls it.
func TestHintsFollowTheHealthLabel(t *testing.T) {
	party := []map[string]interface{}{
		{"name": "Doran", "status": game.HealthDead},
		{"name": "Mirelle", "status": game.HealthDying},
		{"name": "Vesper", "status": game.HealthStable},
		{"name": "Ash", "status": game.HealthCritical},
	}
	all := strings.Join(partyHints(game.HealthHealthy, party), " | ")
	if strings.Contains(all, "Doran") {
		t.Errorf("a dead ally has a hint: %s", all)
	}
	for _, want := range []string{"Mirelle is dying", "Vesper is stable", "Ash is critically wounded"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %s", want, all)
		}
	}
	if h := partyHints(game.HealthHealthy, party[:1]); len(h) != 0 {
		t.Errorf("a party with a dead ally is not 'in good shape': %v", h)
	}
	if h := partyHints(game.HealthHealthy, nil); len(h) != 1 || !strings.Contains(h[0], "good shape") {
		t.Errorf("a healthy party: %v", h)
	}
	if h := zeroHPHint(game.HealthStable, true); strings.Contains(strings.ToLower(h), "make your death") || !strings.Contains(h, "no Death Saving Throws") {
		t.Errorf("stable: %s", h)
	}
	if h := zeroHPHint(game.HealthDying, true); !strings.Contains(h, "server rolls your Death Saving Throw at the start of your turn") {
		t.Errorf("dying in a fight: %s", h)
	}
	if h := zeroHPHint(game.HealthDying, false); !strings.Contains(h, `"death_save"`) {
		t.Errorf("dying outside a fight: %s", h)
	}
	if h := zeroHPHint(game.HealthDead, false); strings.Contains(h, "Make") {
		t.Errorf("dead: %s", h)
	}
}
