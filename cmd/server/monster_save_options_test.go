package main

import (
	"strings"
	"testing"
)

// An action that offers several saving throw effects is split by name; a staged effect keeps its later stage for the GM.
// The texts are invented in the shape of a stat block (the book's own text stays out of the repository).
func TestSplitSaveOptionsAndStages(t *testing.T) {
	rays := "The creature shoots one of the following rays at random (roll 1d4): Frost Ray. Constitution Saving Throw: DC 15. Failure: 10 (3d6) Cold damage. Success: Half damage. Mind Ray. Wisdom Saving Throw: DC 16. Failure: 9 (2d8) Psychic damage, and the target has the Charmed condition for 1 hour. Success: Half damage. Doom Ray. Dexterity Saving Throw: DC 17. Failure: 20 (4d10) Necrotic damage. Failure or Success: The target dies if the ray reduces it to 0 Hit Points."
	opts := splitSaveOptions(rays)
	if len(opts) != 3 || opts[0].Name != "Frost Ray" || opts[1].Name != "Mind Ray" || opts[2].Name != "Doom Ray" {
		t.Fatalf("options: %+v", opts)
	}
	e, ok := parseSaveEffect(opts[1].Text)
	if !ok || e.DC != 16 || e.Ability != "wis" || len(e.Conditions) != 1 || e.Conditions[0] != "charmed" || len(e.Damage) != 1 {
		t.Fatalf("Mind Ray read as %+v", e)
	}
	if strings.Contains(opts[0].Text, "Mind Ray") || !strings.Contains(opts[2].Text, "dies") {
		t.Fatalf("options bleed into each other: %+v", opts)
	}
	if one := splitSaveOptions("Dexterity Saving Throw: DC 15, each creature in a 30-foot Cone. Failure: 10 (3d6) Fire damage. Success: Half damage."); len(one) != 0 {
		t.Fatalf("a single effect split into options: %+v", one)
	}
	staged := "Constitution Saving Throw: DC 18, each creature in a 60-foot Cone. Failure: The target has the Incapacitated condition until the end of its next turn, at which point it repeats the save. Second Failure: The target has the Unconscious condition for 10 minutes."
	s, _ := parseSaveEffect(staged)
	// the second stage is the condition owner's now (readSecondFailure): applied on the failed repeat, not given to the GM
	if len(s.Conditions) != 1 || s.Conditions[0] != "incapacitated" || !s.EndSave || s.Stage == nil || s.Stage.Then != "unconscious" || s.Stage.ThenRounds != 100 || strings.Contains(s.Extra, "Second Failure") {
		t.Fatalf("a staged effect lost its second stage: conditions %v, stage %+v, GM text %q", s.Conditions, s.Stage, s.Extra)
	}
	// a second stage the reader can't apply still reaches the GM
	odd := "Constitution Saving Throw: DC 18, each creature in a 60-foot Cone. Failure: The target has the Incapacitated condition until the end of its next turn, at which point it repeats the save. Second Failure: The target is banished to another plane."
	if s, _ := parseSaveEffect(odd); s.Stage != nil || !strings.Contains(s.Extra, "Second Failure") {
		t.Fatalf("an unread second stage: stage %+v, GM text %q", s.Stage, s.Extra)
	}
}
