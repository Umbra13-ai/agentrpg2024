package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// appShellPage: the app shell's data as a test reads it.
type appShellPage struct {
	CampaignID int               `json:"campaignId"`
	InitialTab string            `json:"initialTab"`
	Party      []previewUnit     `json:"party"`
	Enemies    []previewUnit     `json:"enemies"`
	Chronicle  []previewPage     `json:"chronicle"`
	Campaigns  []previewCampaign `json:"campaigns"`
	Combat     bool              `json:"combat"`
	Lore       []previewLore     `json:"lore"`
	Compendium []compendiumLink  `json:"compendium"`
	Guides     []struct {
		Label, Href string
	} `json:"guides"`
	ThemeKey string `json:"themeKey"`
}

func appShellData(t *testing.T, page string) appShellPage {
	t.Helper()
	i := strings.Index(page, "const LIVE = ")
	if i < 0 {
		t.Fatalf("no data on the page")
	}
	var data appShellPage
	if err := json.NewDecoder(strings.NewReader(page[i+len("const LIVE = "):])).Decode(&data); err != nil {
		t.Fatalf("the data: %v", err)
	}
	return data
}

// TestPostgresAppShell: the Nov 25 mock-up is the site's front: each route opens its tab (/ and /live the featured campaign, /campaign/{id}
// that campaign, /campaigns, /universe, /settings), with the campaign's party, fight and story in the mock-up's constants, never a
// monster's Hit Points, what players and the GM wrote escaped, the compendium's lists and the site's other pages on their tabs; the old
// preview addresses send the reader to the front page.
func TestPostgresAppShell(t *testing.T) {
	testPostgres(t)
	gm, _, camp, chars, _ := pgFightWith(t, "appshell", 1)
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": `The gate groans open. <img src=x onerror=alert(1)> "Welcome."`})
	get := func(path string) (int, string, string) {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "10.9.1.2:1234"
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w.Code, w.Body.String(), w.Header().Get("Location")
	}
	for _, path := range []string{"/preview/", "/preview/" + previewTablePath} {
		t.Setenv("AGENTRPG_UI_PREVIEW", "1")
		if c, _, loc := get(path); c != 302 || loc != "/" {
			t.Errorf("%s: %d to %q, want the front page", path, c, loc)
		}
	}
	for path, tab := range map[string]string{"/": "current", "/live": "current", "/campaigns": "campaigns", "/universe": "universe", "/settings": "settings",
		fmt.Sprintf("/campaign/%d", camp): "current"} {
		c, page, _ := get(path)
		if c != 200 {
			t.Fatalf("%s: %d", path, c)
		}
		for _, left := range []string{"{{TITLE}}", "{{TAILWIND}}", "{{DATA}}", "{{THEME_SCRIPT}}", "{{AGENT_START}}", "cdn.tailwindcss.com"} {
			if strings.Contains(page, left) {
				t.Errorf("%s still holds %q", path, left)
			}
		}
		for _, kept := range []string{"renderHeroCard", "PARTY &amp; INITIATIVE", `data-tab="settings"`, "chronicle-slider", "function arpgTheme()", html.EscapeString(registerRequest)} {
			if !strings.Contains(page, kept) {
				t.Errorf("%s: the mock-up's %q is gone", path, kept)
			}
		}
		if d := appShellData(t, page); d.InitialTab != tab || d.ThemeKey != themeStoreKey {
			t.Errorf("%s opens the tab %q (want %q), theme key %q", path, d.InitialTab, tab, d.ThemeKey)
		}
	}
	if c, _, _ := get("/campaign/999999999"); c != 404 {
		t.Errorf("a campaign that doesn't exist: %d", c)
	}

	_, page, _ := get(fmt.Sprintf("/campaign/%d", camp))
	data := appShellData(t, page)
	if data.CampaignID != camp || !data.Combat || len(data.Party) != 1 || data.Party[0].ID != fmt.Sprintf("c%d", chars[0]) {
		t.Fatalf("the campaign's table: %+v", data)
	}
	if len(data.Party[0].Actions) == 0 || data.Party[0].Actions[0].Hit == "" || data.Party[0].Init == "" || data.Party[0].Pills == nil {
		t.Errorf("the character's card: %+v", data.Party[0])
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
	hrefs := map[string]bool{}
	for _, c := range data.Compendium {
		hrefs[c.Href] = true
	}
	for _, want := range []string{"/universe/srd", "/universe/monsters", "/universe/spells", "/universe/classes"} {
		if !hrefs[want] {
			t.Errorf("the Universe tab has no link to %s", want)
		}
	}
	if len(data.Guides) != len(settingsLinks) || data.Guides[0].Href != settingsLinks[0].Href {
		t.Errorf("the Settings tab's guides: %+v", data.Guides)
	}
	if c, page, _ := get("/universe/srd"); c != 200 || !strings.Contains(page, "universe-grid") || !strings.Contains(page, `class="tab-bar"`) {
		t.Errorf("the SRD compendium's browser: %d", c)
	}
}

// TestOwnerFileChangesAreHooks: every change to the owner's file is a marked hook: the file's script keeps the mock-up's own render
// functions, and each line the site added carries the mark (counted, so a new unmarked change shows up as a different count).
func TestOwnerFileChangesAreHooks(t *testing.T) {
	if n := strings.Count(previewTableHTML, "hook (site, Nov 26)"); n != 15 {
		t.Errorf("the owner's file has %d marked hooks; a change to it is a marked hook, listed in live_table_2024.go and CLAUDE.md", n)
	}
	for _, own := range []string{"function renderHeroCard", "function renderPartySection", "function renderChronicleSection", "function renderCampaignsView",
		"function renderUniverseView", "function renderSettingsView", "function renderCharacterSheet"} {
		if !strings.Contains(previewTableHTML, own) {
			t.Errorf("the owner's %s is gone", own)
		}
	}
}

// TestSiteNavIsTheMockupsTabs: the page shell's tabs are the mock-up's own, in its order and words, so the two shells can't drift apart.
func TestSiteNavIsTheMockupsTabs(t *testing.T) {
	re := regexp.MustCompile(`data-tab="([a-z]+)"[\s\S]*?<span class="text-\[11px\] font-medium">([^<]+)</span>`)
	var mock []string
	for _, m := range re.FindAllStringSubmatch(previewTableHTML, -1) {
		mock = append(mock, m[1]+"="+m[2])
	}
	var nav []string
	for _, l := range siteNav {
		nav = append(nav, l.Tab+"="+l.Label)
		if !appTabs[l.Tab] || l.Href != tabHref(l.Tab) {
			t.Errorf("the tab %s: its address %s", l.Tab, l.Href)
		}
	}
	if strings.Join(mock, ",") != strings.Join(nav, ",") {
		t.Errorf("the mock-up's tabs %v, the page shell's %v", mock, nav)
	}
	for path, tab := range map[string]string{"/": "current", "/campaign/3/log": "current", "/character/5": "current", "/universe/spells": "universe",
		"/monsters/custom/x": "universe", "/commands": "settings", "/docs": "settings", "/about": "settings", "/start": "settings", "/campaigns": "campaigns"} {
		if got := siteTab(path); got != tab {
			t.Errorf("siteTab(%s) = %q, want %q", path, got, tab)
		}
	}
}

func tabHref(tab string) string {
	if tab == "current" {
		return "/live"
	}
	return "/" + tab
}

// TestPageShellFrame: a page of the page shell has the mock-up's header, the four tabs, the theme read before it paints and the three
// themes; nothing of the Nov 21 frame is left.
func TestPageShellFrame(t *testing.T) {
	page := wrapHTML("About <b>x</b>", "<p>Hello</p>")
	for _, want := range []string{`class="shell-header"`, "AGENT RPG 2024", `class="tab-bar"`, `href="/settings"`, "function arpgTheme()", `[data-theme="parchment"]`,
		`[data-theme="contrast"]`, "<title>About &lt;b&gt;x&lt;/b&gt;</title>", `id="quickstart-modal"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page shell has no %q", want)
		}
	}
	for _, gone := range []string{`class="topbar"`, "desktop-nav", "mobile-nav", "site-footer-links", `data-theme="obsidian"`, "Royal Sapphire"} {
		if strings.Contains(page, gone) {
			t.Errorf("the page shell still has the Nov 21 %q", gone)
		}
	}
	if strings.Index(page, "function arpgTheme()") > strings.Index(page, "<style>") {
		t.Error("the theme is read after the styles: the page would flash the default theme")
	}
}
