package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBackgroundLibraryData: every picture the site ships is a valid entry (a describing key and title, a sentence for the GM, tags only from
// the vocabulary, at least one place), stored as a 1920 by 1080 WebP with a 480 by 270 thumbnail, and no key, title or description names a
// published book or one of its places or characters (owner, Nov 26: the backgrounds never use campaign names).
func TestBackgroundLibraryData(t *testing.T) {
	raw, err := backgroundFiles.ReadFile("data/backgrounds/library.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []bgEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) < 28 {
		t.Fatalf("the library ships %d pictures", len(list))
	}
	// distinctive words of the books' titles the pictures were made for: none may reach the site
	named := []string{"strahd", "barovia", "waterdeep", "vecna", "kwalish", "phandelver", "icespire", "spelljammer", "saltmarsh", "avernus", "baldur",
		"candlekeep", "strixhaven", "witchlight", "netherdeep", "dragonlance", "frostmaiden", "icewind", "abyss of", "asmodeus", "tyranny", "annihilation",
		"golden vault", "radiant citadel", "yawning portal", "locathah", "infinite staircase", "fortune's wheel", "storm king", "apocalypse", "mad mage",
		"dragon heist", "mind flayer", "illithid", "beholder", "critical role"}
	seen := map[string]bool{}
	for _, e := range list {
		if p := bgEntryProblem(e); p != "" {
			t.Errorf("%s: %s", e.Key, p)
		}
		if seen[e.Key] {
			t.Errorf("%s twice", e.Key)
		}
		seen[e.Key] = true
		words := strings.ToLower(e.Key + " " + strings.ReplaceAll(e.Key, "-", " ") + " " + e.Title + " " + e.Description)
		for _, n := range named {
			if strings.Contains(words, n) {
				t.Errorf("%s names %q", e.Key, n)
			}
		}
		for file, w := range map[string]int{".webp": backgroundW, ".thumb.webp": bgThumbW} {
			b, err := backgroundFiles.ReadFile("data/backgrounds/" + e.Key + file)
			if err != nil {
				t.Errorf("%s%s: %v", e.Key, file, err)
				continue
			}
			img, err := decodePicture(b)
			if err != nil || !isWebP(b) || img.Bounds().Dx() != w || img.Bounds().Dy() != w*9/16 {
				t.Errorf("%s%s is not a %d by %d WebP", e.Key, file, w, w*9/16)
			}
		}
	}
}

// TestBackgroundScore: a campaign's words reach the tags through their hints, and a tag the request asks for counts most.
func TestBackgroundScore(t *testing.T) {
	castle := bgEntry{Key: "a", Title: "Castle", Description: "A castle.", Tags: map[string][]string{"place": {"castle"}, "mood": {"gothic"}, "features": {"undead"}}}
	sea := bgEntry{Key: "b", Title: "Sea", Description: "A ship.", Tags: map[string][]string{"place": {"sea"}, "features": {"ship"}}}
	text := "A haunted land ruled by a vampire"
	if bgScore(castle, text, nil) <= bgScore(sea, text, nil) {
		t.Error("a vampire's land does not match the gothic castle first")
	}
	if bgScore(sea, "", []string{"ship"}) <= bgScore(castle, "", []string{"ship"}) {
		t.Error("an asked tag does not count")
	}
	if bgScore(castle, "", nil) != 0 {
		t.Error("no words, no match")
	}
}

// TestPostgresBackgroundLibrary: the library through the server. An agent lists it, names one at creation or lets the server pick the best
// match, and its GM changes it; the administrator picks one, uploads a picture of the campaign's own (which wins), removes both, and adds,
// edits and retires entries (a retired one is never offered, the campaigns that use it keep it).
func TestPostgresBackgroundLibrary(t *testing.T) {
	testPostgres(t)
	t.Setenv("ADMIN_KEY", "test-admin-key-for-the-library")
	adminKey := "test-admin-key-for-the-library"
	admin := func(method, p string, body interface{}) (int, map[string]interface{}) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, p, bytes.NewReader(b))
		r.RemoteAddr = "10.77.3.1:1234"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Admin-Key", adminKey)
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		out := map[string]interface{}{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	get := func(p string) (int, string, string) {
		r := httptest.NewRequest("GET", p, nil)
		r.RemoteAddr = "10.77.3.2:1234"
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w.Code, w.Header().Get("Content-Type"), w.Header().Get("Location")
	}
	fk := fmt.Sprintf("test-quiet-forest-%d", time.Now().UnixNano()%1000000) // a new key each run: the test database keeps the library
	_, gm := pgAgent(t, "bglib_gm")
	_, other := pgAgent(t, "bglib_other")

	// the list: every picture, best match first for the tags asked
	c, j := pgCall(t, "GET", "/api/backgrounds?tags=sea,ship", "", nil)
	list, _ := j["backgrounds"].([]interface{})
	if c != 200 || len(list) < 28 || j["vocabulary"] == nil {
		t.Fatalf("the library: %d %d", c, len(list))
	}
	first := list[0].(map[string]interface{})
	tags := fmt.Sprint(first["tags"])
	if !strings.Contains(tags, "sea") && !strings.Contains(tags, "ship") {
		t.Errorf("the best match for sea and ship: %v", first["tags"])
	}
	key := first["key"].(string)
	if c, ct, _ := get(fmt.Sprintf("/backgrounds/%s.webp", key)); c != 200 || ct != "image/webp" {
		t.Errorf("the picture: %d %s", c, ct)
	}
	if c, ct, _ := get(fmt.Sprintf("/backgrounds/%s.thumb.webp", key)); c != 200 || ct != "image/webp" {
		t.Errorf("the thumbnail: %d %s", c, ct)
	}
	if c, _, _ := get("/backgrounds/no-such-scene.webp"); c != 404 {
		t.Errorf("an unknown picture: %d", c)
	}

	shows := func(camp int) string { return backgroundURL(camp) }
	create := func(body map[string]interface{}) (int, map[string]interface{}) {
		c, j := pgCall(t, "POST", "/api/campaigns", gm, body)
		if c != 200 {
			t.Fatalf("create: %d %v", c, j)
		}
		bg, _ := j["background"].(map[string]interface{})
		return int(j["campaign_id"].(float64)), bg
	}
	chosenKey := func(bg map[string]interface{}) string {
		ch, _ := bg["chosen"].(map[string]interface{})
		k, _ := ch["key"].(string)
		return k
	}

	// named at creation
	named, bg := create(map[string]interface{}{"name": "Test Library Named", "background": key})
	if chosenKey(bg) != key || !strings.HasPrefix(shows(named), "/backgrounds/"+key+".webp") || bg["change"] == nil || len(bg["other_matches"].([]interface{})) != 3 {
		t.Errorf("a named background: %v, the page shows %s", bg, shows(named))
	}
	// none named: the best match for the campaign's words
	picked, bg := create(map[string]interface{}{"name": "Test Library Picked", "setting": "A haunted land of mist ruled by a vampire from his castle"})
	if k := chosenKey(bg); k == "" || !strings.Contains(bg["how"].(string), "picked by the server") {
		t.Errorf("the server's pick: %v", bg)
	} else if e, _ := backgroundEntry(k); !strings.Contains(fmt.Sprint(e.Tags), "gothic") && !strings.Contains(fmt.Sprint(e.Tags), "undead") {
		t.Errorf("a vampire's land got %s %v", k, e.Tags)
	}
	// a key that is not in the library: the server's pick, and it says so
	_, bg = create(map[string]interface{}{"name": "Test Library Wrong", "setting": "pirates at sea", "background": "no-such-scene"})
	if chosenKey(bg) == "no-such-scene" || !strings.Contains(bg["how"].(string), "not in the library") {
		t.Errorf("an unknown key at creation: %v", bg)
	}

	// the GM changes it; another agent can't; an unknown key is refused
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/background", picked), gm, map[string]interface{}{"background": key}); c != 200 || !strings.HasPrefix(shows(picked), "/backgrounds/"+key) {
		t.Errorf("the GM's change: %d %v", c, j)
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/background", picked), other, map[string]interface{}{"background": key}); c != 403 {
		t.Errorf("another agent's change: %d", c)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/background", picked), gm, map[string]interface{}{"background": "no-such-scene"}); c != 400 || j["error"] != "unknown_background" {
		t.Errorf("an unknown key: %d %v", c, j)
	}
	if c, _, loc := get(fmt.Sprintf("/api/campaigns/%d/background", picked)); c != 302 || !strings.HasPrefix(loc, "/backgrounds/"+key) {
		t.Errorf("the campaign's picture route: %d %s", c, loc)
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/background", picked), gm, map[string]interface{}{"background": ""}); c != 200 || shows(picked) != defaultBackground {
		t.Errorf("back to the default: %d %s", c, shows(picked))
	}

	// the administrator: a library pick, an upload that wins over it, the pick again (the upload removed), then both removed
	path := fmt.Sprintf("/api/admin/campaigns/%d/background", named)
	second := list[1].(map[string]interface{})["key"].(string)
	if c, j := admin("POST", path, map[string]interface{}{"library": second, "note": "test"}); c != 200 || !strings.HasPrefix(shows(named), "/backgrounds/"+second) {
		t.Errorf("the admin's pick: %d %v", c, j)
	}
	img := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t, 640, 360))
	if c, _ := admin("POST", path, map[string]interface{}{"image_base64": img}); c != 200 || !strings.HasPrefix(shows(named), fmt.Sprintf("/api/campaigns/%d/background", named)) {
		t.Errorf("the upload does not win: %d %s", c, shows(named))
	}
	if c, _ := admin("POST", path, map[string]interface{}{"library": key}); c != 200 || hasBackground(named) || !strings.HasPrefix(shows(named), "/backgrounds/"+key) {
		t.Errorf("the pick after an upload: %d %s", c, shows(named))
	}
	if c, _ := admin("DELETE", path, nil); c != 200 || shows(named) != defaultBackground {
		t.Errorf("removed: %d %s", c, shows(named))
	}
	if c, _ := admin("POST", path, map[string]interface{}{"library": "no-such-scene"}); c != 400 {
		t.Errorf("an unknown pick: %d", c)
	}

	// the library: add, a wrong tag refused, edit, retire (gone from the offer, kept by the campaign that uses it)
	if c, j := admin("POST", "/api/admin/backgrounds", map[string]interface{}{"key": "test-quiet-meadow", "title": "Quiet meadow", "description": "A quiet meadow at dawn.",
		"tags": map[string][]string{"place": {"meadow"}}, "image_base64": img}); c != 400 || j["error"] != "invalid_background" {
		t.Errorf("a tag outside the vocabulary: %d %v", c, j)
	}
	if c, j := admin("POST", "/api/admin/backgrounds", map[string]interface{}{"key": fk, "title": "Quiet forest", "description": "A quiet forest at dawn.",
		"tags": map[string][]string{"place": {"forest"}, "mood": {"peaceful"}}, "image_base64": img}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	if e, err := backgroundEntry(fk); err != nil || e.Title != "Quiet forest" {
		t.Fatalf("added: %v %v", e, err)
	}
	if c, _, _ := get("/backgrounds/" + fk + ".thumb.webp"); c != 200 {
		t.Errorf("the added thumbnail: %d", c)
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/background", named), gm, map[string]interface{}{"background": fk}); c != 200 {
		t.Fatalf("pick the added one: %d", c)
	}
	if c, j := admin("POST", "/api/admin/backgrounds/"+fk, map[string]interface{}{"title": "Still forest", "retired": true}); c != 200 {
		t.Fatalf("retire: %d %v", c, j)
	}
	_, j = pgCall(t, "GET", "/api/backgrounds", "", nil)
	if strings.Contains(fmt.Sprint(j["backgrounds"]), fk) {
		t.Error("a retired picture is still offered")
	}
	if !strings.HasPrefix(shows(named), "/backgrounds/"+fk) {
		t.Error("the campaign lost its retired picture")
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/background", picked), gm, map[string]interface{}{"background": fk}); c != 400 {
		t.Errorf("a retired picture picked: %d", c)
	}
	_, j = admin("GET", "/api/admin/backgrounds", nil)
	if !strings.Contains(fmt.Sprint(j["backgrounds"]), "Still forest") {
		t.Error("the admin's list leaves out the retired picture")
	}
	// the library survives a restart: the shipped pictures are not added twice, the edits are kept
	seedBackgroundLibrary()
	if e, _ := backgroundEntry(fk); e.Title != "Still forest" || !e.Retired {
		t.Errorf("after a restart: %+v", e)
	}
}
