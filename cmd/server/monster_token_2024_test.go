package main

// A GM-built monster's token (monster_token_2024.go): a square picture within the rule, saved by the GM who owns the monster, kept by an
// edit's new version, shown by monsterTokenURL (the encounter stage) and the builder's list, served at /monster-tokens/{key}.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(w/2, h/2, color.RGBA{200, 30, 30, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestMonsterTokenRule(t *testing.T) {
	srd, err := fs.ReadFile(siteAssets, "data/site_assets/monster-goblin-warrior.webp")
	if err != nil {
		t.Fatal(err)
	}
	if w, h, ok := webpSize(srd); !ok || w != 192 || h != 192 {
		t.Errorf("an SRD token's WebP size: %d x %d %v", w, h, ok)
	}
	if mime, _, ref := checkImage(srd, monsterTokenRule); ref != nil || mime != "image/webp" {
		t.Errorf("a square WebP token: %v", ref)
	}
	cases := []struct {
		w, h int
		code string
	}{{512, 512, ""}, {128, 128, ""}, {300, 200, "not_square"}, {100, 100, "bad_dimensions"}, {2100, 2100, "bad_dimensions"}}
	for _, c := range cases {
		_, _, ref := checkImage(pngOf(t, c.w, c.h), monsterTokenRule)
		got := ""
		if ref != nil {
			got = fmt.Sprint(ref.Body["error"])
		}
		if got != c.code {
			t.Errorf("%dx%d: %q, want %q", c.w, c.h, got, c.code)
		}
	}
	if !strings.Contains(monsterTokenRuleText, "square") || !strings.Contains(monsterTokenRuleText, "128 to 2048") || !strings.Contains(monsterTokenRuleText, "2 MB") {
		t.Errorf("the rule's words: %s", monsterTokenRuleText)
	}
}

// Only the token owner writes a compendium monster's token.
func TestNoOtherMonsterTokenWriter(t *testing.T) {
	re := regexp.MustCompile(`(?i)SET\s+token\s*=|token_type\s*=\s*\$`)
	files, _ := os.ReadDir(".")
	for _, f := range files {
		n := f.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || n == "monster_token_2024.go" {
			continue
		}
		if b, _ := os.ReadFile(n); re.Match(b) {
			t.Errorf("%s writes a monster's token itself: use saveMonsterToken / clearMonsterToken (monster_token_2024.go)", n)
		}
	}
}

func TestPostgresMonsterTokens(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _ := earlyRulesFixture(t, "Test Token Host", []srdMonsterEntry{{Name: "Tap", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 1 Bludgeoning damage."}})
	m := inventedMonster("Test Lantern Wisp", "Fey", nil, []srdMonsterEntry{{Name: "Flicker", Text: "Melee Attack Roll: +3, reach 5 ft. Hit: 2 (1d4) Radiant damage."}})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented token fixture", "monsters": []srdMonster2024{m}})
	if c != 201 || !strings.Contains(fmt.Sprint(j["token_rule"]), "square") {
		t.Fatalf("import: %d %v", c, j)
	}
	key := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1 OR replaces=$1`, key) })
	b64 := func(w, h int) string { return base64.StdEncoding.EncodeToString(pngOf(t, w, h)) }
	// not square: refused with the size it had and the rule
	if c, j = pgCall(t, "POST", "/api/gm/compendium/monsters/token", gm, map[string]interface{}{"campaign_id": camp, "monster_key": key, "image_base64": b64(400, 300)}); c != 400 || j["error"] != "not_square" || j["got"] != "400x300" || j["rule"] == nil {
		t.Errorf("a 400x300 picture: %d %v", c, j)
	}
	if monsterTokenURL(key) != "" {
		t.Error("a refused picture was saved")
	}
	// another GM can't change it
	_, other := pgAgent(t, "token other gm")
	pgCall(t, "POST", "/api/campaigns", other, map[string]interface{}{"name": "Other table", "max_players": 2})
	if c, _ = pgCall(t, "POST", "/api/gm/compendium/monsters/token", other, map[string]interface{}{"monster_key": key, "image_base64": b64(256, 256)}); c < 400 || monsterTokenURL(key) != "" {
		t.Errorf("another GM's upload: %d", c)
	}
	// the owner's square picture: saved, listed, served, on the encounter stage's URL
	if c, j = pgCall(t, "POST", "/api/gm/compendium/monsters/token", gm, map[string]interface{}{"campaign_id": camp, "monster_key": key, "image_base64": "data:image/png;base64," + b64(256, 256)}); c != 200 {
		t.Fatalf("upload: %d %v", c, j)
	}
	u := monsterTokenURL(key)
	if !strings.HasPrefix(u, "/monster-tokens/") {
		t.Fatalf("token URL %q", u)
	}
	if _, l := pgCall(t, "GET", fmt.Sprintf("/api/gm/compendium/monsters?mine=1&campaign_id=%d", camp), gm, nil); !strings.Contains(fmt.Sprint(l["monsters"]), "token_url:/monster-tokens/") {
		t.Errorf("the list: %v", l["monsters"])
	}
	if code, body := pgRawGet(t, u, ""); code != 200 || !bytes.HasPrefix(body, []byte("\x89PNG")) {
		t.Errorf("serving it: %d", code)
	}
	// an edit's new version keeps it
	m.HP++
	c, j = pgCall(t, "POST", "/api/gm/compendium/monsters/edit", gm, map[string]interface{}{"campaign_id": camp, "monster_key": key, "monster": m})
	if c != 201 {
		t.Fatalf("edit: %d %v", c, j)
	}
	newKey := fmt.Sprint(j["monster_key"])
	if monsterTokenURL(newKey) == "" || !strings.Contains(fmt.Sprint(j["token"]), "url:/monster-tokens/") {
		t.Errorf("the new version lost its token: %v", j["token"])
	}
	// removed
	if c, _ = pgCall(t, "DELETE", "/api/gm/compendium/monsters/token", gm, map[string]interface{}{"campaign_id": camp, "monster_key": newKey}); c != 200 || monsterTokenURL(newKey) != "" {
		t.Errorf("remove: %d", c)
	}
}
