package game

import (
	"testing"
)

func TestHasFeatFeature(t *testing.T) {
	tests := []struct {
		name     string
		feats    []string
		feature  string
		expected bool
	}{
		{"war caster has somatic bypass", []string{"war_caster"}, "somatic_with_hands_full", true},
		{"alert has initiative bonus", []string{"alert"}, "initiative_bonus", true},
		{"grappler has grapple advantage", []string{"grappler"}, "grapple_advantage", true},
		{"sentinel has opportunity stops movement", []string{"sentinel"}, "opportunity_stops_movement", true},
		{"no feat has feature", []string{}, "somatic_with_hands_full", false},
		{"wrong feat doesn't have feature", []string{"alert"}, "somatic_with_hands_full", false},
		{"multiple feats checks all", []string{"alert", "war_caster"}, "somatic_with_hands_full", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasFeatFeature(tt.feats, tt.feature)
			if result != tt.expected {
				t.Errorf("HasFeatFeature(%v, %q) = %v, want %v", tt.feats, tt.feature, result, tt.expected)
			}
		})
	}
}

