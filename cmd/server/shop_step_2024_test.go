package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The starting shop is an optional setup step, asked as a question (Matt, Nov 16; playtest I-ROWAN-2): it never counts in
// steps_left, whether the character took its equipment packages or gold; the step asks whether the player wants to shop for
// additional equipment, offers "yes" (the shop's requests) and "no" ({"done": true}); a "no" completes it.
func TestPostgresShopIsAnOptionalQuestion(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg shopper")
	gold := pgRogue("")
	gold["starting_equipment"], gold["background_equipment"] = "b", "b" // gold instead of both equipment packages
	for name, body := range map[string]map[string]interface{}{"packages": pgRogue(""), "gold": gold} {
		body["name"] = fmt.Sprintf("Pg Shopper %s %d", name, time.Now().UnixNano()%1e9)
		c, j := pgCall(t, "POST", "/api/characters", tok, body)
		if c != 200 {
			t.Fatalf("%s: create: %d %v", name, c, j)
		}
		id := int(j["character_id"].(float64))
		setup := func() (map[string]interface{}, map[string]interface{}) {
			_, s := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", id), tok, nil)
			for _, st := range asList(s["steps"]) {
				if m, _ := st.(map[string]interface{}); m["key"] == "shop" {
					return s, m
				}
			}
			return s, nil
		}
		s, shop := setup()
		if shop == nil {
			t.Fatalf("%s: no shop step: %v", name, s["steps"])
		}
		if shop["optional"] != true || !strings.Contains(fmt.Sprint(shop["title"]), "Do you want to shop") {
			t.Errorf("%s: the shop step isn't an optional question: optional %v, title %q", name, shop["optional"], shop["title"])
		}
		choices, _ := shop["choices"].(map[string]interface{})
		if choices["yes"] == nil || !strings.Contains(fmt.Sprint(choices["no"]), `{"done": true}`) {
			t.Errorf("%s: the question's answers: %v", name, choices)
		}
		if nextStep, _ := s["next"].(map[string]interface{}); nextStep != nil && nextStep["key"] == "shop" {
			t.Errorf("%s: the shop is the next required step", name)
		}
		if c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/shop", id), tok, map[string]interface{}{"done": true}); c != 200 {
			t.Fatalf("%s: answering no: %d %v", name, c, j)
		}
		if _, shop = setup(); shop != nil {
			t.Errorf("%s: the shop step is still open after a no: %v", name, shop)
		}
	}
}
