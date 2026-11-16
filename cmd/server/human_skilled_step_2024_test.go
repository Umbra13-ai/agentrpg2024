package main

import (
	"testing"
)

// The Human's Skilled picks are asked only once Skilled is the chosen feat (feedback log 25: they showed before any feat was
// chosen, and with another feat); while no feat is chosen they stay in the suggestion when Skilled is the suggested feat.
func TestPostgresHumanSkilledPicksOnlyAfterTheFeat(t *testing.T) {
	testPostgres(t)
	speciesStep := func(sc map[string]interface{}) builderStep {
		body := map[string]interface{}{"class": "fighter", "species": "human"}
		if sc != nil {
			body["species_choices"] = sc
		}
		for _, s := range builderSteps(body, 0, false) {
			if s.Key == "species" {
				return s
			}
		}
		t.Fatalf("no species step")
		return builderStep{}
	}
	asksSkilled := func(s builderStep) bool {
		for _, c := range s.Choices {
			if c["field"] == "species_feat_choices.proficiencies" {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		feat string
		want bool
	}{{"", false}, {"alert", false}, {"magic_initiate", false}, {"skilled", true}} {
		var sc map[string]interface{}
		if tc.feat != "" {
			sc = map[string]interface{}{"feat": tc.feat}
		}
		s := speciesStep(sc)
		if asksSkilled(s) != tc.want {
			t.Errorf("feat %q: Skilled's picks asked %v, want %v", tc.feat, !tc.want, tc.want)
		}
		if tc.feat == "" {
			sug, _ := s.Suggestion["species_choices"].(map[string]interface{})
			if sug["feat"] == "skilled" && s.Suggestion["species_feat_choices"] == nil {
				t.Errorf("Skilled is suggested but its picks aren't: %v", s.Suggestion)
			}
		}
	}
}
