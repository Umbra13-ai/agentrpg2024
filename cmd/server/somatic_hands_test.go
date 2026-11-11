package main

import "testing"

// F64: with a Shield in one hand, the other hand performs a Somatic component when it holds nothing, or when it holds a
// Spellcasting Focus standing in for the spell's Material component (SRD 5.2.1, Components). A weapon there leaves no hand.
func TestSomaticHandFree(t *testing.T) {
	druidKit := []map[string]interface{}{
		{"name": "Shield", "location": "worn"},
		{"name": "Quarterstaff", "note": "Druidic Focus", "location": "held", "spellcasting_focus": true, "focus_kind": "primal"},
		{"name": "Sickle"},
	}
	cases := []struct {
		name     string
		mainHand string
		comps    string
		focusOK  bool // the materials have no cost and aren't consumed
		class    string
		want     bool
	}{
		{"empty hand, V S spell", "", "V, S", true, "druid", true},
		{"focus in hand, V S M spell (Sunbeam)", "Quarterstaff", "V, S, M", true, "druid", true},
		{"focus in hand, V S spell (Heal)", "Quarterstaff", "V, S", true, "druid", false},
		{"focus in hand, costly material (Revivify)", "Quarterstaff", "V, S, M", false, "druid", false},
		{"a weapon in hand, V S M spell", "Sickle", "V, S, M", true, "druid", false},
		{"a primal focus doesn't serve a cleric", "Quarterstaff", "V, S, M", true, "cleric", false},
		{"an item not in the inventory", "Wand", "V, S, M", true, "druid", false},
	}
	for _, c := range cases {
		if got := somaticHandFree(c.mainHand, c.comps, c.focusOK, druidKit, c.class); got != c.want {
			t.Errorf("%s: somaticHandFree = %v, want %v", c.name, got, c.want)
		}
	}
}

// The refusal itself still comes from checkSpellComponents when both hands are full.
func TestSomaticRefusalNeedsBothHandsFull(t *testing.T) {
	focus := []map[string]interface{}{{"name": "Quarterstaff", "spellcasting_focus": true, "focus_kind": "primal"}}
	if msg := checkSpellComponents("V, S", nil, focus, true, nil, nil, "druid", 20); msg == "" {
		t.Error("weapon and shield: a V, S spell should be refused")
	}
	if msg := checkSpellComponents("V, S, M", nil, focus, false, nil, nil, "druid", 20); msg != "" {
		t.Errorf("a hand free for the gestures: the spell should be allowed, got %q", msg)
	}
}
