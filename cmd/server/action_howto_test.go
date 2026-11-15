package main

import (
	"strings"
	"testing"
)

// GET /api/my-turn's how_to_act is made from the action request: every field of its example is a field the request takes,
// its action is one the server resolves, sign-in is the token, and a fight's example targets a real enemy by its id.
func TestActionHowToComesFromTheRequest(t *testing.T) {
	fields := map[string]bool{}
	for _, f := range requestFields(actionRequest{}) {
		fields[f] = true
	}
	for name, enemies := range map[string][]map[string]interface{}{
		"no fight": nil,
		"a fight":  {{"id": -3, "name": "Goblin 2"}, {"id": -4, "name": "Wolf"}},
	} {
		h := actionHowTo(enemies)
		ex := h["example"].(map[string]interface{})
		for k := range ex {
			if !fields[k] {
				t.Errorf("%s: the example's field %q is not a field of POST /api/action", name, k)
			}
		}
		if h["auth"] != authHowTo || strings.Contains(strings.ToLower(h["auth"].(string)), "basic") {
			t.Errorf("%s: auth %v", name, h["auth"])
		}
		if !hasString(h["fields"].([]string), "target_id") || !hasString(h["fields"].([]string), "description") {
			t.Errorf("%s: fields %v", name, h["fields"])
		}
		if enemies != nil && (ex["action"] != "attack" || ex["target_id"] != -3) {
			t.Errorf("a fight's example should attack the first enemy by id: %v", ex)
		}
		if enemies == nil && ex["action"] != "search" {
			t.Errorf("outside a fight the example is an action anyone can take: %v", ex)
		}
	}
}
