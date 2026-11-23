package main

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// TestEverySRDMonsterHasAToken: the 330 SRD stat blocks and the token files agree: each monster has data/site_assets/monster-<slug>.webp, a
// WebP of a size a phone can afford, and no token is left for a slug nobody has.
func TestEverySRDMonsterHasAToken(t *testing.T) {
	want := map[string]bool{}
	for _, m := range srdMonsters2024() {
		want[monsterSlug(m.Name)] = true
	}
	have := map[string]bool{}
	entries, err := fs.ReadDir(siteAssets, "data/site_assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "monster-") {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(n, "monster-"), ".webp")
		have[slug] = true
		raw, _ := fs.ReadFile(siteAssets, "data/site_assets/"+n)
		if !strings.HasSuffix(n, ".webp") || len(raw) < 12 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WEBP" {
			t.Errorf("%s is not a WebP file", n)
		}
		if len(raw) > 40*1024 {
			t.Errorf("%s is %d bytes: a token is scaled to 192 px (about 10 KB)", n, len(raw))
		}
		if !want[slug] {
			t.Errorf("%s is a token for no SRD monster", n)
		}
		if monsterTokenURL(slug) != "/assets/"+n {
			t.Errorf("monsterTokenURL(%q) = %q", slug, monsterTokenURL(slug))
		}
	}
	for slug := range want {
		if !have[slug] {
			t.Errorf("the SRD monster %s has no token", slug)
		}
	}
	for _, bad := range []string{"", "custom:abc", "../hero", "Goblin Warrior", "no-such-monster"} {
		if u := monsterTokenURL(bad); u != "" {
			t.Errorf("monsterTokenURL(%q) = %q", bad, u)
		}
	}
}

// TestPostgresMonsterTokensOnThePages: the encounter stage shows a kind of enemy once, as its token, with a count; an enemy with no token (a
// GM's own monster) keeps the emoji; one at 0 Hit Points is gone; the Universe page of a monster shows its token; the file is served.
func TestPostgresMonsterTokensOnThePages(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "tokens gm")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Tokens test", "max_players": 4})
	if c != 200 {
		t.Fatalf("campaign %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	order := `[{"id":-1,"name":"Goblin Warrior 1","initiative":15,"hp":7,"monster_key":"goblin-warrior"},
		{"id":-2,"name":"Goblin Warrior 2","initiative":12,"hp":7,"monster_key":"goblin-warrior"},
		{"id":-3,"name":"Homebrew Thing 1","initiative":9,"hp":5,"monster_key":"custom:0123456789abcdef"},
		{"id":-4,"name":"Ogre 1","initiative":5,"hp":0,"monster_key":"ogre"}]`
	pgExec(t, `INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, turn_started_at, active) VALUES ($1, 1, 0, $2, NOW(), true)
		ON CONFLICT (lobby_id) DO UPDATE SET turn_order = EXCLUDED.turn_order, active = true`, camp, order)

	_, j = pgCall(t, "GET", fmt.Sprintf("/campaign/%d", camp), "", nil)
	page, _ := j["_body"].(string)
	if n := strings.Count(page, `src="/assets/monster-goblin-warrior.webp"`); n != 1 {
		t.Errorf("the goblins' token shows %d times, want once for the kind", n)
	}
	for _, want := range []string{"Goblin Warrior (x2)", `alt="Goblin Warrior"`, "Homebrew Thing", "👹"} {
		if !strings.Contains(page, want) {
			t.Errorf("the encounter stage has no %q", want)
		}
	}
	if strings.Contains(page, "monster-ogre.webp") || strings.Contains(page, "Ogre") {
		t.Error("a monster at 0 Hit Points is still on the stage")
	}
	_, j = pgCall(t, "GET", "/universe/monsters/goblin-warrior", "", nil)
	if p, _ := j["_body"].(string); !strings.Contains(p, `src="/assets/monster-goblin-warrior.webp"`) {
		t.Error("the monster's page does not show its token")
	}
	if c, _ := pgCall(t, "GET", "/assets/monster-goblin-warrior.webp", "", nil); c != 200 {
		t.Errorf("the token file: %d", c)
	}
}
