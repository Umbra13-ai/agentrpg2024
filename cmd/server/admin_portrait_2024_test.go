package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 3), uint8(y * 3), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The administrator adds or removes a character's portrait (admin_portrait_2024.go), under the same rules as the owner's upload
// (portrait_2024.go), and every change is in the admin change log.
func TestPostgresAdminPortrait(t *testing.T) {
	testPostgres(t)
	t.Setenv("ADMIN_KEY", "test-admin-key-for-portraits")
	_, player, _, hero, _ := earlyRulesFixture(t, "Test Portrait Drake", nil)
	path := fmt.Sprintf("/api/admin/characters/%d/portrait", hero)
	admin := func(method, p, ctype string, body []byte, key string) (int, map[string]interface{}) {
		r := httptest.NewRequest(method, p, bytes.NewReader(body))
		r.RemoteAddr = "10.77.1.1:1234"
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		if key != "" {
			r.Header.Set("X-Admin-Key", key)
		}
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		out := map[string]interface{}{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	key := "test-admin-key-for-portraits"
	upload := func(img []byte, note string) (int, map[string]interface{}) {
		b, _ := json.Marshal(map[string]interface{}{"image_base64": "data:image/png;base64," + base64.StdEncoding.EncodeToString(img), "note": note})
		return admin("POST", path, "application/json", b, key)
	}
	good := testPNG(t, 64, 80)

	// no key, no change
	if c, _ := admin("POST", path, "image/png", good, ""); c == 200 || hasPortrait(hero) {
		t.Fatalf("without the key: %d, portrait %v", c, hasPortrait(hero))
	}
	// removing what isn't there changes nothing
	if c, j := admin("DELETE", path, "", nil, key); c != 200 || !strings.Contains(fmt.Sprint(j["message"]), "no portrait") {
		t.Errorf("remove with none: %d %v", c, j)
	}
	// the owner's rules: an empty body, a file that is no image, a damaged image, a side too small
	if c, j := admin("POST", path, "image/png", nil, key); c != 400 || j["error"] != "empty_image" {
		t.Errorf("empty: %d %v", c, j)
	}
	if c, j := admin("POST", path, "application/octet-stream", []byte("not an image at all, just text"), key); c != 415 || j["error"] != "unsupported_image_type" {
		t.Errorf("text: %d %v", c, j)
	}
	if c, j := upload(testPNG(t, 10, 10), ""); c != 400 || j["error"] != "bad_dimensions" {
		t.Errorf("tiny: %d %v", c, j)
	}
	if c, j := upload(good[:len(good)/2], ""); c != 400 || j["error"] != "corrupt_image" {
		t.Errorf("cut short: %d %v", c, j)
	}
	if c, j := admin("POST", path, "application/json", []byte(`{"image_base64": "!!!"}`), key); c != 400 || j["error"] != "invalid_base64" {
		t.Errorf("bad base64: %d %v", c, j)
	}
	if hasPortrait(hero) {
		t.Fatal("a refused image was saved")
	}
	if c, _ := admin("POST", "/api/admin/characters/999999999/portrait", "image/png", good, key); c != 404 {
		t.Errorf("a character that doesn't exist: %d", c)
	}

	// add: the public image is there with its type
	if c, j := upload(good, "added for the sheet"); c != 200 || j["content_type"] != "image/png" {
		t.Fatalf("add: %d %v", c, j)
	}
	if c, _ := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/portrait", hero), "", nil); c != 200 || !hasPortrait(hero) {
		t.Fatalf("the portrait is public: %d", c)
	}
	// replace with the raw bytes
	if c, j := admin("PUT", path, "image/png", testPNG(t, 96, 120), key); c != 200 {
		t.Fatalf("replace: %d %v", c, j)
	}
	// the owner's own route still works on what the administrator added, through the same owner
	if c, j := pgCall(t, "DELETE", fmt.Sprintf("/api/characters/%d/portrait", hero), player, nil); c != 200 || hasPortrait(hero) {
		t.Fatalf("the owner removes it: %d %v", c, j)
	}
	if c, _ := upload(good, ""); c != 200 || !hasPortrait(hero) {
		t.Fatal("add again")
	}
	// remove
	if c, j := admin("DELETE", path, "", nil, key); c != 200 || hasPortrait(hero) {
		t.Fatalf("remove: %d %v", c, j)
	}
	if c, _ := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/portrait", hero), "", nil); c != 404 {
		t.Errorf("no portrait is 404: %d", c)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM admin_changes WHERE kind='character' AND row_id=$1 AND field='portrait'`, hero).Scan(&n)
	if n != 4 { // add, replace, add again, remove (the owner's own removal is not the administrator's)
		t.Errorf("the admin change log holds %d portrait changes, want 4", n)
	}
	var note string
	db.QueryRow(`SELECT COALESCE(note,'') FROM admin_changes WHERE kind='character' AND row_id=$1 AND field='portrait' ORDER BY id LIMIT 1`, hero).Scan(&note)
	if note != "added for the sheet" {
		t.Errorf("the note is in the log: %q", note)
	}
}

// Nothing writes a character's avatar columns but portrait_2024.go.
func TestNoOtherPortraitWriter(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "portrait_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			l := strings.ToLower(line)
			if strings.Contains(l, "set avatar") || strings.Contains(l, "avatar = null") || strings.Contains(l, "avatar_type = ") && strings.Contains(l, "update") {
				t.Errorf("%s:%d writes the avatar columns: use savePortrait or clearPortrait (portrait_2024.go)", f, n+1)
			}
		}
	}
}
