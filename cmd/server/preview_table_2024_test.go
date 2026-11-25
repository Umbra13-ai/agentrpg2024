package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPostgresPreviewTable: the Nov 25 mock-up with live data is served only where previews are on, carries the campaign's party, fight and
// story in the mock-up's constants, never a monster's Hit Points, and escapes what players and the GM wrote.
func TestPostgresPreviewTable(t *testing.T) {
	testPostgres(t)
	gm, _, camp, chars, _ := pgFightWith(t, "preview", 1)
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": `The gate groans open. <img src=x onerror=alert(1)> "Welcome."`})
	get := func(path string) (int, string) {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "10.9.1.2:1234"
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	path := fmt.Sprintf("/preview/%s?campaign=%d", previewTablePath, camp)
	t.Setenv("AGENTRPG_UI_PREVIEW", "")
	if c, _ := get(path); c != 404 {
		t.Fatalf("the preview is served where previews are off: %d", c)
	}
	t.Setenv("AGENTRPG_UI_PREVIEW", "1")
	c, page := get(path)
	if c != 200 {
		t.Fatalf("the preview: %d", c)
	}
	for _, left := range []string{"{{TITLE}}", "{{TAILWIND}}", "{{DATA}}", "cdn.tailwindcss.com", "DATAURI"} {
		if strings.Contains(page, left) {
			t.Errorf("the page still holds %q", left)
		}
	}
	for _, kept := range []string{"renderHeroCard", "PARTY &amp; INITIATIVE", "data-tab=\"settings\"", "chronicle-slider"} {
		if !strings.Contains(page, kept) {
			t.Errorf("the mock-up's %q is gone", kept)
		}
	}
	i := strings.Index(page, "const LIVE = ")
	if i < 0 {
		t.Fatalf("no data on the page")
	}
	var data struct {
		CampaignID int               `json:"campaignId"`
		Party      []previewUnit     `json:"party"`
		Enemies    []previewUnit     `json:"enemies"`
		Chronicle  []previewPage     `json:"chronicle"`
		Campaigns  []previewCampaign `json:"campaigns"`
		Combat     bool              `json:"combat"`
		Lore       []previewLore     `json:"lore"`
	}
	dec := json.NewDecoder(strings.NewReader(page[i+len("const LIVE = "):]))
	if err := dec.Decode(&data); err != nil {
		t.Fatalf("the data: %v", err)
	}
	if data.CampaignID != camp || !data.Combat || len(data.Party) != 1 || data.Party[0].ID != fmt.Sprintf("c%d", chars[0]) {
		t.Fatalf("the campaign's table: %+v", data)
	}
	if len(data.Party[0].Actions) == 0 || data.Party[0].Actions[0].Hit == "" || data.Party[0].Init == "" {
		t.Errorf("the character's sheet: %+v", data.Party[0])
	}
	if len(data.Enemies) != 2 {
		t.Fatalf("the fight's creatures: %+v", data.Enemies)
	}
	for _, m := range data.Enemies {
		if m.HP != 0 || m.MaxHP != 0 || m.HPText != "Healthy" || !m.IsEnemy || m.AC == "" || len(m.Actions) == 0 {
			t.Errorf("a monster's card shows its look, never its Hit Points: %+v", m)
		}
	}
	found := false
	for _, p := range data.Chronicle {
		if strings.Contains(p.Text, "The gate groans open.") {
			found = true
			if strings.Contains(p.Text, "<img") || !strings.Contains(p.Text, "&lt;img") || p.SpeakerID != "narrator" {
				t.Errorf("the narration's page: %+v", p)
			}
		}
	}
	if !found {
		t.Errorf("the narration is not in the chronicle")
	}
	listed := false
	for _, cp := range data.Campaigns {
		listed = listed || cp.ID == camp
	}
	if !listed {
		t.Errorf("the campaign is not in the Campaigns tab")
	}
}
