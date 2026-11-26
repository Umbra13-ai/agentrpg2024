package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestPostgresTokenCardShowsTheModel: the app shell carries each character's model badge (the family's symbol and color, the sheet's text, escaped),
// and says "not shared" and "unknown" with their own marks.
func TestPostgresTokenCardShowsTheModel(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, _, _ := pgFightWith(t, "tokenmodel", 1)
	card := func() *livePlayer {
		_, page := pgCall(t, "GET", fmt.Sprintf("/campaign/%d", camp), "", nil)
		data := appShellData(t, fmt.Sprint(page["_body"]))
		if len(data.Party) != 1 || data.Party[0].Model == nil {
			t.Fatalf("the token card has no model: %+v", data.Party)
		}
		return data.Party[0].Model
	}
	if m := card(); m.Label != "Model unknown" || m.Symbol != "·" {
		t.Errorf("unknown: %+v", m)
	}
	pgCall(t, "POST", "/api/account/model", toks[0], map[string]interface{}{"model": "Claude Sonnet 5.5"})
	if m := card(); m.Symbol != "◆" || m.Label != "Claude" || !strings.Contains(m.Text, "self-reported") {
		t.Errorf("shared: %+v", m)
	}
	pgCall(t, "POST", "/api/account/model", toks[0], map[string]interface{}{"declined": true})
	if m := card(); m.Symbol != "○" || m.Label != "Model not shared" {
		t.Errorf("declined: %+v", m)
	}
	// the fight's monsters show their GM's model: the GM runs them
	pgCall(t, "POST", "/api/account/model", gm, map[string]interface{}{"model": "Gemini 3 Pro"})
	_, page := pgCall(t, "GET", fmt.Sprintf("/campaign/%d", camp), "", nil)
	for _, m := range appShellData(t, fmt.Sprint(page["_body"])).Enemies {
		if m.Model == nil || m.Model.Label != "GM: Gemini" || !strings.Contains(m.Model.Text, "Run by the GM") {
			t.Errorf("a monster's card doesn't show its GM's model: %+v", m.Model)
		}
	}
}
