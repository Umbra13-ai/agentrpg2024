package main

import (
	"strings"
	"testing"
)

func TestShortenMyTurn(t *testing.T) {
	long := strings.Repeat("x", 900)
	ev := []string{"one", "two", "three", "four " + long, "five " + long}
	resp := map[string]interface{}{
		"character":     map[string]interface{}{"name": "Calla", "hp": 20, "class_features": []map[string]interface{}{{"name": "Channel Divinity", "description": long}}, "prepared_spells": []string{"bless"}},
		"recent_events": ev, "how_to_act": "tip", "rules_reminder": map[string]interface{}{}, "shops_open": []string{"s"},
		"your_options": map[string]interface{}{"rests": map[string]interface{}{"x": long}, "movement": "30"},
		"left_behind":  map[string]interface{}{"items": []map[string]interface{}{{"item": "Robe"}}},
		"story_so_far": strings.Repeat("word ", 400) + "the end",
	}
	shortenMyTurn(resp, 7, false)
	ch := resp["character"].(map[string]interface{})
	if ch["class_features"] != nil || ch["prepared_spells"] != nil || ch["hp"] != 20 || ch["class_feature_names"].([]string)[0] != "Channel Divinity" || ch["left_out_of_the_short_view"] == nil {
		t.Errorf("character: %v", ch)
	}
	if got := resp["recent_events"].([]string); len(got) != 3 || len([]rune(got[1])) > shortEventChars+100 {
		t.Errorf("recent_events: %d entries", len(got))
	}
	for _, k := range []string{"how_to_act", "rules_reminder", "shops_open"} {
		if resp[k] != nil {
			t.Errorf("%s stays", k)
		}
	}
	if resp["your_options"].(map[string]interface{})["rests"] != nil || resp["your_options"].(map[string]interface{})["movement"] != "30" {
		t.Errorf("your_options: %v", resp["your_options"])
	}
	if lb := resp["left_behind"].(map[string]interface{}); lb["count"] != 1 {
		t.Errorf("left_behind: %v", lb)
	}
	if s := resp["story_so_far"].(string); !strings.HasSuffix(s, "the end") || len(strings.Fields(s)) > shortStoryWords+2 {
		t.Errorf("story: %d words", len(strings.Fields(s)))
	}
	resp2 := map[string]interface{}{"how_to_act": "tip"}
	shortenMyTurn(resp2, 7, true)
	if resp2["how_to_act"] != "tip" {
		t.Error("tips=1 should keep the tips")
	}
}
