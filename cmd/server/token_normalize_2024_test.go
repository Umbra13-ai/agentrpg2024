package main

// What a token is stored as (token_normalize_2024.go): a PNG of at most 512 pixels a side, scaled by averaging, for the GM's route and the
// administrator's alike; a WebP stays as it came.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func solidPNG(t *testing.T, side int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestNormalizeTokenScalesAndKeepsColour(t *testing.T) {
	red := color.NRGBA{200, 30, 30, 255}
	// 2048 -> 512: a plain picture stays plain, the size is the stored size, the type is PNG
	out, mime, note, err := normalizeToken(solidPNG(t, 2048, red), "image/png")
	if err != nil || mime != "image/png" || !strings.Contains(note, "512 by 512") || !strings.Contains(note, "2048 by 2048") {
		t.Fatalf("2048 png: %v %s %q", err, mime, note)
	}
	img, _, _ := image.Decode(bytes.NewReader(out))
	if img.Bounds().Dx() != 512 || img.Bounds().Dy() != 512 {
		t.Errorf("stored size %v", img.Bounds())
	}
	if c := color.NRGBAModel.Convert(img.At(256, 256)).(color.NRGBA); c != red {
		t.Errorf("colour after scaling: %v, want %v", c, red)
	}
	// a size that doesn't divide: still the stored side, still the colour; a half-transparent edge keeps its colour (no bleed to black)
	half := color.NRGBA{10, 200, 10, 128}
	out, _, _, err = normalizeToken(solidPNG(t, 700, half), "image/png")
	img2, _, _ := image.Decode(bytes.NewReader(out))
	if err != nil || img2.Bounds().Dx() != 512 {
		t.Fatalf("700 png: %v %v", err, img2.Bounds())
	}
	if c := color.NRGBAModel.Convert(img2.At(100, 100)).(color.NRGBA); abs(int(c.G)-200) > 3 || c.A < 126 || c.A > 130 {
		t.Errorf("a half-transparent token's pixel: %v, want about %v", c, half)
	}
	// already small: kept at its size, re-encoded as PNG (nothing said)
	out, mime, note, err = normalizeToken(solidPNG(t, 300, red), "image/png")
	img3, _, _ := image.Decode(bytes.NewReader(out))
	if err != nil || mime != "image/png" || note != "" || img3.Bounds().Dx() != 300 {
		t.Errorf("300 png: %v %s %q %v", err, mime, note, img3.Bounds())
	}
	// a JPEG becomes a PNG, with its tags gone
	var j bytes.Buffer
	jpeg.Encode(&j, image.NewRGBA(image.Rect(0, 0, 256, 256)), nil)
	out, mime, note, err = normalizeToken(j.Bytes(), "image/jpeg")
	if err != nil || mime != "image/png" || note != "Stored as a PNG." || detectPortraitType(out) != "image/png" {
		t.Errorf("jpeg: %v %s %q", err, mime, note)
	}
	// a WebP is stored as it came; a damaged picture is refused
	web := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	if out, mime, note, err = normalizeToken(web, "image/webp"); err != nil || mime != "image/webp" || note != "" || !bytes.Equal(out, web) {
		t.Errorf("webp: %v %s", err, mime)
	}
	if _, _, _, err = normalizeToken([]byte("not a picture"), "image/png"); err == nil {
		t.Error("a damaged picture was accepted")
	}
}

func TestPostgresTokenStoredAsAPNGOnBothRoutes(t *testing.T) {
	testPostgres(t)
	t.Setenv("ADMIN_KEY", "test-admin-key-for-tokens")
	gm, _, camp, _, _ := earlyRulesFixture(t, "Test Token Host", nil)
	m := inventedMonster("Test Store Lantern", "Fey", nil, []srdMonsterEntry{{Name: "Flicker", Text: "Melee Attack Roll: +3, reach 5 ft. Hit: 2 (1d4) Radiant damage."}})
	_, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented token fixture", "monsters": []srdMonster2024{m}})
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1`, key) })
	stored := func() (int, string, int) {
		var img []byte
		var mime string
		db.QueryRow(`SELECT token, token_type FROM custom_monsters WHERE key = $1`, key).Scan(&img, &mime)
		cfg, _, _ := image.DecodeConfig(bytes.NewReader(img))
		return cfg.Width, mime, len(img)
	}
	big := solidPNG(t, 1536, color.NRGBA{40, 90, 200, 255})
	// the GM's route
	c, r := pgCall(t, "POST", "/api/gm/compendium/monsters/token", gm, map[string]interface{}{"campaign_id": camp, "monster_key": key, "image_base64": base64.StdEncoding.EncodeToString(big)})
	if w, mime, _ := stored(); c != 200 || w != 512 || mime != "image/png" || !strings.Contains(fmt.Sprint(r["message"]), "1536 by 1536") {
		t.Errorf("the GM's upload: %d %v (stored %d px %s)", c, r["message"], w, mime)
	}
	if _, err := db.Exec(`UPDATE custom_monsters SET token = NULL, token_type = NULL, token_updated_at = NULL WHERE key = $1`, key); err != nil {
		t.Fatal(err)
	}
	// the administrator's route
	b, _ := json.Marshal(map[string]interface{}{"image_base64": base64.StdEncoding.EncodeToString(big)})
	req := httptest.NewRequest("POST", "/api/admin/monsters/"+url.PathEscape(key)+"/token", bytes.NewReader(b))
	req.RemoteAddr = "10.77.3.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Key", "test-admin-key-for-tokens")
	w := httptest.NewRecorder()
	newServer("").Handler.ServeHTTP(w, req)
	if px, mime, size := stored(); w.Code != 200 || px != 512 || mime != "image/png" || size >= len(big) {
		t.Errorf("the admin's upload: %d %s (stored %d px %s, %d bytes from %d)", w.Code, w.Body.String(), px, mime, size, len(big))
	}
}
