package main

import "testing"

// TestActionCosts: what an action type costs comes from getActionResourceType alone (direct actions, pending-turn steps, Command
// and Haste limits, feature routes). A described action ("other") takes the action, as in the SRD; only speaking and one object
// interaction are free.
func TestActionCosts(t *testing.T) {
	for typ, want := range map[string]string{
		"other": "action", "cast": "action", "attack": "action", "utilize-like unknown word": "action",
		"speak": "free", "interact": "free", "drop": "free",
		"cunning_action": "bonus_action", "opportunity_attack": "reaction", "move": "movement",
	} {
		if got := getActionResourceType(typ); got != want {
			t.Errorf("getActionResourceType(%q) = %q, want %q", typ, got, want)
		}
	}
}
