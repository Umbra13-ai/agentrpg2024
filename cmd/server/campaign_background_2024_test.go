package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The administrator sets a campaign's background (campaign_background_2024.go): the same image rules as a portrait with a wide picture's sizes,
// the public image, the Live Table page showing it (the default dragon until one is set), the admin change log.
func TestPostgresAdminCampaignBackground(t *testing.T) {
	testPostgres(t)
	t.Setenv("ADMIN_KEY", "test-admin-key-for-backgrounds")
	key := "test-admin-key-for-backgrounds"
	_, _, camp, _, _ := earlyRulesFixture(t, "Test Background Drake", nil)
	path := fmt.Sprintf("/api/admin/campaigns/%d/background", camp)
	call := func(method, p, ctype string, body []byte, adminKey string) (int, map[string]interface{}, string) {
		r := httptest.NewRequest(method, p, bytes.NewReader(body))
		r.RemoteAddr = "10.77.2.1:1234"
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		if adminKey != "" {
			r.Header.Set("X-Admin-Key", adminKey)
		}
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		out := map[string]interface{}{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Body.String()
	}
	upload := func(img []byte, note string) (int, map[string]interface{}) {
		b, _ := json.Marshal(map[string]interface{}{"image_base64": "data:image/png;base64," + base64.StdEncoding.EncodeToString(img), "note": note})
		c, j, _ := call("POST", path, "application/json", b, key)
		return c, j
	}
	page := func() string { _, _, body := call("GET", fmt.Sprintf("/campaign/%d", camp), "", nil, ""); return body }
	wide := testPNG(t, 640, 360)

	// until one is set the page shows the default scene and the image route says there is none
	if !strings.Contains(page(), `class="vtt-bg" src="`+defaultBackground+`"`) {
		t.Error("the default scene is not on the page")
	}
	if c, j, _ := call("GET", fmt.Sprintf("/api/campaigns/%d/background", camp), "", nil, ""); c != 404 || j["error"] != "no_background" {
		t.Errorf("none set: %d %v", c, j)
	}
	// no key, no change; removing what isn't there changes nothing; a campaign that doesn't exist
	if c, _, _ := call("POST", path, "image/png", wide, ""); c == 200 || hasBackground(camp) {
		t.Fatalf("without the key: %d", c)
	}
	if c, j, _ := call("DELETE", path, "", nil, key); c != 200 || !strings.Contains(fmt.Sprint(j["message"]), "nothing to remove") {
		t.Errorf("remove with none: %d %v", c, j)
	}
	if c, _, _ := call("POST", "/api/admin/campaigns/999999999/background", "image/png", wide, key); c != 404 {
		t.Errorf("a campaign that doesn't exist: %d", c)
	}
	// the owner's rules: empty, not an image, damaged, a side too small, bad base64
	if c, j, _ := call("POST", path, "image/png", nil, key); c != 400 || j["error"] != "empty_image" {
		t.Errorf("empty: %d %v", c, j)
	}
	if c, j, _ := call("POST", path, "application/octet-stream", []byte("just text, no image"), key); c != 415 || j["error"] != "unsupported_image_type" {
		t.Errorf("text: %d %v", c, j)
	}
	if c, j := upload(testPNG(t, 64, 64), ""); c != 400 || j["error"] != "bad_dimensions" {
		t.Errorf("tiny: %d %v", c, j)
	}
	if c, j := upload(wide[:len(wide)/2], ""); c != 400 || j["error"] != "corrupt_image" {
		t.Errorf("cut short: %d %v", c, j)
	}
	if c, j, _ := call("POST", path, "application/json", []byte(`{"image_base64": "!!!"}`), key); c != 400 || j["error"] != "invalid_base64" {
		t.Errorf("bad base64: %d %v", c, j)
	}
	if hasBackground(camp) {
		t.Fatal("a refused image was saved")
	}

	// set: the public image and the page both show it; a tall picture is saved with a note about the crop
	if c, j := upload(wide, "the cover scene"); c != 200 || j["content_type"] != "image/png" || j["note"] != nil {
		t.Fatalf("set: %d %v", c, j)
	}
	if c, _, body := call("GET", fmt.Sprintf("/api/campaigns/%d/background", camp), "", nil, ""); c != 200 || len(body) != len(wide) {
		t.Fatalf("the image is public: %d (%d bytes)", c, len(body))
	}
	if p := page(); !strings.Contains(p, fmt.Sprintf(`class="vtt-bg" src="/api/campaigns/%d/background?v=`, camp)) || strings.Contains(p, `class="vtt-bg" src="`+defaultBackground+`"`) {
		t.Error("the page does not show the campaign's own background")
	}
	if c, j := upload(testPNG(t, 400, 640), ""); c != 200 || j["note"] == nil {
		t.Errorf("a tall picture: %d %v", c, j)
	}
	// the admin editor never sends the picture's bytes
	if _, j, _ := call("GET", fmt.Sprintf("/api/admin/campaigns/%d", camp), "", nil, key); j["campaign"] == nil {
		t.Error("the editor's reply has no campaign")
	} else if _, has := j["campaign"].(map[string]interface{})["background_image"]; has {
		t.Error("the editor's reply carries the picture")
	}
	// remove: the default is back
	if c, j, _ := call("DELETE", path, "", nil, key); c != 200 || hasBackground(camp) {
		t.Fatalf("remove: %d %v", c, j)
	}
	if !strings.Contains(page(), `class="vtt-bg" src="`+defaultBackground+`"`) {
		t.Error("the default scene did not come back")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM admin_changes WHERE kind='campaign' AND row_id=$1 AND field='background'`, camp).Scan(&n)
	if n != 3 { // set, set again (the tall one), remove
		t.Errorf("the admin change log holds %d background changes, want 3", n)
	}
}

// Nothing writes a campaign's background columns but campaign_background_2024.go.
func TestNoOtherBackgroundWriter(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "campaign_background_2024.go" || f == "database_init.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			l := strings.ToLower(line)
			if strings.Contains(l, "background_image =") || strings.Contains(l, "background_type =") || strings.Contains(l, "background_updated_at =") {
				t.Errorf("%s:%d writes the background columns: use saveBackground or clearBackground (campaign_background_2024.go)", f, n+1)
			}
		}
	}
}
