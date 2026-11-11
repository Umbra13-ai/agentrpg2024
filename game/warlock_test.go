package game

import (
	"testing"
)

func TestGetMaxInvocations(t *testing.T) {
	tests := []struct {
		level    int
		expected int
	}{
		{1, 1},
		{2, 3},
		{3, 3},
		{4, 3},
		{5, 5},
		{6, 5},
		{7, 6},
		{8, 6},
		{9, 7},
		{10, 7},
		{11, 7},
		{12, 8},
		{13, 8},
		{14, 8},
		{15, 9},
		{16, 9},
		{17, 9},
		{18, 10},
		{19, 10},
		{20, 10},
	}

	for _, tc := range tests {
		got := GetMaxInvocations(tc.level)
		if got != tc.expected {
			t.Errorf("GetMaxInvocations(%d) = %d, want %d", tc.level, got, tc.expected)
		}
	}
}

func TestAvailableInvocations(t *testing.T) {
	// 2024: the 28 invocations of the SRD (the three pacts are invocations now)
	expectedCount := 28
	if len(AvailableInvocations) != expectedCount {
		t.Errorf("Expected %d invocations, got %d", expectedCount, len(AvailableInvocations))
	}

	// Test specific invocations exist
	requiredInvocations := []string{
		"agonizing-blast",
		"armor-of-shadows",
		"devils-sight",
		"eldritch-spear",
		"repelling-blast",
		"lifedrinker",
		"witch-sight",
	}

	for _, slug := range requiredInvocations {
		if _, ok := AvailableInvocations[slug]; !ok {
			t.Errorf("Missing invocation: %s", slug)
		}
	}
}

func TestInvocationPrerequisites(t *testing.T) {
	// Agonizing Blast needs level 2 and a damaging Warlock cantrip
	ab := AvailableInvocations["agonizing-blast"]
	if ab.Prerequisites.Level != 2 {
		t.Errorf("Agonizing Blast should require level 2, got %d", ab.Prerequisites.Level)
	}

	// Lifedrinker requires level 9 and Pact of the Blade
	ld := AvailableInvocations["lifedrinker"]
	if ld.Prerequisites.Level != 9 {
		t.Errorf("Lifedrinker should require level 9, got %d", ld.Prerequisites.Level)
	}
	if ld.Prerequisites.Pact != "blade" {
		t.Errorf("Lifedrinker should require Pact of the Blade, got %s", ld.Prerequisites.Pact)
	}

	// Armor of Shadows has no prerequisites
	aos := AvailableInvocations["armor-of-shadows"]
	if aos.Prerequisites.Level != 0 || aos.Prerequisites.Pact != "" || aos.Prerequisites.RequiresSpell != "" {
		t.Error("Armor of Shadows should have no prerequisites")
	}
}

func TestInvocationMechanics(t *testing.T) {
	// Agonizing Blast should have agonizing_blast mechanic
	ab := AvailableInvocations["agonizing-blast"]
	if ab.Mechanics["agonizing_blast"] != "true" {
		t.Error("Agonizing Blast should have agonizing_blast mechanic")
	}

	// Armor of Shadows should grant at-will mage armor
	aos := AvailableInvocations["armor-of-shadows"]
	if aos.Mechanics["at_will_spell"] != "mage-armor" {
		t.Error("Armor of Shadows should grant at-will mage armor")
	}

}

func TestLevelBasedInvocations(t *testing.T) {
	// the level prerequisites printed in the SRD
	levelRequirements := map[string]int{
		"agonizing-blast":           2,
		"armor-of-shadows":          0,
		"one-with-shadows":          5,
		"ascendant-step":            5,
		"master-of-myriad-forms":    5,
		"whispers-of-the-grave":     7,
		"lifedrinker":               9,
		"visions-of-distant-realms": 9,
		"devouring-blade":           12,
		"witch-sight":               15,
	}

	for slug, expectedLevel := range levelRequirements {
		inv := AvailableInvocations[slug]
		if inv.Prerequisites.Level != expectedLevel {
			t.Errorf("%s: expected level %d, got %d", slug, expectedLevel, inv.Prerequisites.Level)
		}
	}
}
