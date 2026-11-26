package main

// What a token is stored as (token_normalize_2024.go): a WebP of at most 192 pixels a side, scaled by averaging, for the GM's route and the
// administrator's alike; the tokens stored before that rule are converted once at the start (convertStoredTokens).

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"

	"github.com/HugoSmits86/nativewebp"
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
	decode := func(b []byte) image.Image {
		img, err := decodePicture(b)
		if err != nil {
			t.Fatalf("the stored picture can't be read: %v", err)
		}
		return img
	}
	// 2048 -> 192: a plain picture stays plain, the size is the stored size, the type is WebP
	out, mime, px, note, err := normalizeToken(solidPNG(t, 2048, red), "image/png")
	if err != nil || mime != "image/webp" || px != 192 || !strings.Contains(note, "192 by 192") || !strings.Contains(note, "2048 by 2048") {
		t.Fatalf("2048 png: %v %s %d %q", err, mime, px, note)
	}
	img := decode(out)
	if img.Bounds().Dx() != 192 || img.Bounds().Dy() != 192 {
		t.Errorf("stored size %v", img.Bounds())
	}
	if c := color.NRGBAModel.Convert(img.At(96, 96)).(color.NRGBA); c != red {
		t.Errorf("colour after scaling: %v, want %v", c, red)
	}
	// a size that doesn't divide: still the stored side, still the colour; a half-transparent edge keeps its colour (no bleed to black)
	half := color.NRGBA{10, 200, 10, 128}
	out, _, _, _, err = normalizeToken(solidPNG(t, 700, half), "image/png")
	img2 := decode(out)
	if err != nil || img2.Bounds().Dx() != 192 {
		t.Fatalf("700 png: %v %v", err, img2.Bounds())
	}
	if c := color.NRGBAModel.Convert(img2.At(50, 50)).(color.NRGBA); abs(int(c.G)-200) > 3 || c.A < 126 || c.A > 130 {
		t.Errorf("a half-transparent token's pixel: %v, want about %v", c, half)
	}
	// already small: kept at its size (never enlarged), written as WebP
	out, mime, px, note, err = normalizeToken(solidPNG(t, 150, red), "image/png")
	if err != nil || mime != "image/webp" || px != 150 || note != "Stored as a WebP." || decode(out).Bounds().Dx() != 150 {
		t.Errorf("150 png: %v %s %d %q", err, mime, px, note)
	}
	// a JPEG becomes a WebP
	var j bytes.Buffer
	jpeg.Encode(&j, image.NewRGBA(image.Rect(0, 0, 256, 256)), nil)
	out, mime, _, _, err = normalizeToken(j.Bytes(), "image/jpeg")
	if err != nil || mime != "image/webp" || decode(out).Bounds().Dx() != 192 {
		t.Errorf("jpeg: %v %s", err, mime)
	}
	// a WebP over 192 is scaled; one that is already small enough is kept exactly as it came
	big, _, _, _, _ := normalizeToken(solidPNG(t, 192, red), "image/png")
	if out, mime, px, note, err = normalizeToken(big, "image/webp"); err != nil || mime != "image/webp" || px != 192 || note != "" || !bytes.Equal(out, big) {
		t.Errorf("a small webp: %v %s %d %q", err, mime, px, note)
	}
	var w bytes.Buffer
	if err := nativewebp.Encode(&w, image.NewNRGBA(image.Rect(0, 0, 400, 400)), nil); err != nil {
		t.Fatal(err)
	}
	if out, _, px, _, err = normalizeToken(w.Bytes(), "image/webp"); err != nil || px != 192 || decode(out).Bounds().Dx() != 192 {
		t.Errorf("a 400 px webp: %v %d", err, px)
	}
	if _, _, _, _, err = normalizeToken([]byte("not a picture"), "image/png"); err == nil {
		t.Error("a damaged picture was accepted")
	}
}

func TestPostgresTokenStoredAsAWebPOnBothRoutes(t *testing.T) {
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
	if w, mime, _ := stored(); c != 200 || w != 192 || mime != "image/webp" || !strings.Contains(fmt.Sprint(r["message"]), "1536 by 1536") {
		t.Errorf("the GM's upload: %d %v (stored %d px %s)", c, r["message"], w, mime)
	}
	if _, err := db.Exec(`UPDATE custom_monsters SET token = NULL, token_type = NULL, token_px = NULL, token_updated_at = NULL WHERE key = $1`, key); err != nil {
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
	if px, mime, size := stored(); w.Code != 200 || px != 192 || mime != "image/webp" || size >= len(big) {
		t.Errorf("the admin's upload: %d %s (stored %d px %s, %d bytes from %d)", w.Code, w.Body.String(), px, mime, size, len(big))
	}
}

// TestPostgresStoredTokensAreConvertedOnce: a token stored before the rule (a 512 pixel PNG, no recorded side) is brought to a 192 pixel WebP by the
// start's conversion, which records the side, keeps whose it is and which monster has it, leaves a damaged one alone, and finds nothing to do the
// second time.
func TestPostgresStoredTokensAreConvertedOnce(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _ := earlyRulesFixture(t, "Test Convert Host", nil)
	m := inventedMonster("Test Convert Lantern", "Fey", nil, []srdMonsterEntry{{Name: "Flicker", Text: "Melee Attack Roll: +3, reach 5 ft. Hit: 2 (1d4) Radiant damage."}})
	_, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented token fixture", "monsters": []srdMonster2024{m}})
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1 OR key = $2`, key, key+"-bad") })
	old := solidPNG(t, 512, color.NRGBA{40, 90, 200, 255})
	if _, err := db.Exec(`UPDATE custom_monsters SET token = $1, token_type = 'image/png', token_px = NULL WHERE key = $2`, old, key); err != nil {
		t.Fatal(err)
	}
	// a damaged one (it was stored before checks existed): left as it is
	if _, err := db.Exec(`INSERT INTO custom_monsters(key, definition, stats, source_note, token, token_type) SELECT $2, definition, stats, source_note, $3, 'image/png' FROM custom_monsters WHERE key = $1`,
		key, key+"-bad", []byte("not a picture")); err != nil {
		t.Fatal(err)
	}
	if err := convertStoredTokens(); err != nil {
		t.Fatal(err)
	}
	var img []byte
	var mime string
	var px int
	db.QueryRow(`SELECT token, token_type, token_px FROM custom_monsters WHERE key = $1`, key).Scan(&img, &mime, &px)
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(img)); err != nil || cfg.Width != 192 || mime != "image/webp" || px != 192 || len(img) >= len(old) {
		t.Errorf("the converted token: %v %d px %s, side recorded %d, %d bytes from %d", err, cfg.Width, mime, px, len(img), len(old))
	}
	if monsterTokenURL(key) == "" {
		t.Error("the monster lost its token")
	}
	var bad []byte
	var badPx int
	db.QueryRow(`SELECT token, token_px FROM custom_monsters WHERE key = $1`, key+"-bad").Scan(&bad, &badPx)
	if string(bad) != "not a picture" || badPx != 0 {
		t.Errorf("the damaged token: %q side %d", bad, badPx)
	}
	// the second time finds nothing to convert: the bytes are the same ones
	var again []byte
	if err := convertStoredTokens(); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT token FROM custom_monsters WHERE key = $1`, key).Scan(&again)
	if !bytes.Equal(again, img) {
		t.Error("the conversion ran again")
	}
}

// TestNoOtherPictureDecode: a picture's pixels are read only by decodePicture (two WebP readers are registered with image.Decode, and the one that
// answers is not the exact one).
func TestNoOtherPictureDecode(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "token_normalize_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "image.Decode(") {
			t.Errorf("%s reads a picture with image.Decode: use decodePicture (token_normalize_2024.go)", f)
		}
	}
}
