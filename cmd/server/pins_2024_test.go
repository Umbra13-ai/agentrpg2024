package main

// A human's pins (pins_2024.go): the Live tab opens the pinned campaign and the Character tab the pinned character, whatever table moved
// last; a pin that no longer works is cleared and the page says so; the campaign page and the character sheet carry the pin button.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostgresPinsOpenTheTabs(t *testing.T) {
	testPostgres(t)
	_, _, campA, heroA, _ := earlyRulesFixture(t, "Test Pin Host A", nil)
	_, _, campB, _, _ := earlyRulesFixture(t, "Test Pin Host B", nil) // the newer table: what the tabs open without a pin
	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w
	}
	pin := func(name string, id int) *http.Cookie { return &http.Cookie{Name: name, Value: fmt.Sprint(id)} }
	pinOf := func(w *httptest.ResponseRecorder) string {
		for _, c := range w.Result().Cookies() {
			if c.Name == pinClearedCookie {
				return c.Value
			}
		}
		return ""
	}
	writeAction(db, campB, 0, actionNarration, "The newest scene of any table.", "") // B is the table that moved last
	if w := get("/live"); !strings.Contains(w.Body.String(), fmt.Sprintf(`data-pin="campaign" data-pin-id="%d"`, campB)) {
		b := w.Body.String()
		i := strings.Index(b, "data-pin=")
		t.Fatalf("without a pin the Live tab opens the newest table (%d): code %d, pin at %d: %.200s", campB, w.Code, i, b[max(i, 0):])
	}
	w := get("/live", pin(pinCampaignCookie, campA))
	if !strings.Contains(w.Body.String(), fmt.Sprintf(`data-pin="campaign" data-pin-id="%d"`, campA)) || pinOf(w) != "" {
		t.Errorf("the pinned campaign %d: not opened", campA)
	}
	// a pin that no longer works: cleared, and the page says so
	w = get("/live", pin(pinCampaignCookie, 99999999))
	if pinOf(w) != "campaign" || !strings.Contains(w.Body.String(), fmt.Sprintf(`data-pin-id="%d"`, campB)) {
		t.Errorf("a pin of no campaign: cleared %q", pinOf(w))
	}
	// the Character tab: the pinned character, else the pinned campaign's party
	if w = get("/character", pin(pinCharacterCookie, heroA)); w.Code != 302 || w.Header().Get("Location") != fmt.Sprintf("/character/%d", heroA) {
		t.Errorf("the pinned character: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w = get("/character", pin(pinCampaignCookie, campA)); w.Code != 302 || w.Header().Get("Location") != fmt.Sprintf("/character/%d", heroA) {
		t.Errorf("the pinned campaign's party: %d %s", w.Code, w.Header().Get("Location"))
	}
	pgExec(t, `UPDATE characters SET deleted_at = NOW() WHERE id = $1`, heroA)
	if w = get("/character", pin(pinCharacterCookie, heroA)); pinOf(w) != "character" || w.Header().Get("Location") == fmt.Sprintf("/character/%d", heroA) {
		t.Errorf("a deleted character's pin: cleared %q, went to %s", pinOf(w), w.Header().Get("Location"))
	}
	pgExec(t, `UPDATE characters SET deleted_at = NULL WHERE id = $1`, heroA)
	// the sheet carries its pin button
	if w = get(fmt.Sprintf("/character/%d", heroA)); !strings.Contains(w.Body.String(), fmt.Sprintf(`data-pin="character" data-pin-id="%d"`, heroA)) {
		t.Errorf("the character sheet has no pin button")
	}
}
