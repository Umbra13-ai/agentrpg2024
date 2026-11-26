package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCardPills: a card's pills say what is on the character, each once, in the card's words: dead, dying with its saves, stable, the
// conditions and Concentration, Temporary Hit Points (none for the dead), Heroic Inspiration and the companions.
func TestCardPills(t *testing.T) {
	labels := func(c liveCard) string {
		var out []string
		for _, p := range cardPills(c) {
			out = append(out, p.Label+"/"+p.Tone)
		}
		return strings.Join(out, ", ")
	}
	base := liveCard{ID: 7, Name: "Calla", HP: 24, MaxHP: 24, Health: "healthy"}
	if got := labels(base); got != "" {
		t.Errorf("a healthy character with nothing on it: %q", got)
	}
	c := base
	c.TempHP, c.Inspired = 5, true
	c.Conds = []liveCond{{"Conc: Bless", "arcane"}, {"Poisoned", "crimson"}}
	c.Comps = []liveComp{{Icon: "🐎", Label: "Horse", HP: 13, MaxHP: 13}, {Icon: "🦉", Label: "Owl", Away: true}}
	if got, want := labels(c), "Conc: Bless/arcane, Poisoned/crimson, +5 Temp HP/arcane, ★ Inspiration/gold, 🐎 Horse 13/13/emerald, 🦉 Owl away/emerald"; got != want {
		t.Errorf("pills\n got %q\nwant %q", got, want)
	}
	c = base
	c.HP, c.Health, c.DeathSucc, c.DeathFail = 0, "dying", 1, 2
	if got := labels(c); got != "Dying · saves 1✓ 2✗/crimson" {
		t.Errorf("dying: %q", got)
	}
	c.Health = "stable"
	if got := labels(c); got != "Stable/gold" {
		t.Errorf("stable: %q", got)
	}
	c = base
	c.Health, c.TempHP = "dead", 5
	if got := labels(c); got != "Dead/crimson" {
		t.Errorf("dead: %q", got)
	}
}

// TestPostgresLiveCardsShowTheCharacter: the Live Table's cards carry what is on the character: Temporary Hit Points, Heroic Inspiration, Hit
// Dice, Concentration, conditions and Exhaustion, the Death Saving Throws of a dying one, the mark of a dead one, and its companions
// (which replace the separate row of companion boxes).
func TestPostgresLiveCardsShowTheCharacter(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "cards gm")
	_, p1 := pgAgent(t, "cards player one")
	_, p2 := pgAgent(t, "cards player two")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Cards test", "max_players": 4})
	if c != 200 {
		t.Fatalf("campaign %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	pgExec(t, `UPDATE lobbies SET status='active',schedule_state='off' WHERE id=$1`, camp)
	mk := func(token, name string) int {
		c, j := pgCall(t, "POST", "/api/characters", token, pgRogue(name))
		if c != 200 {
			t.Fatalf("character %d %v", c, j)
		}
		id := int(j["character_id"].(float64))
		pgExec(t, `UPDATE characters SET lobby_id=$1 WHERE id=$2`, camp, id)
		return id
	}
	stamp := time.Now().UnixNano() % 1e9 // character names are unique on the server
	wellName, downName := fmt.Sprintf("Cardwell %d", stamp), fmt.Sprintf("Cardfall %d", stamp)
	well, down := mk(p1, wellName), mk(p2, downName)
	pgExec(t, `UPDATE characters SET temp_hp=7, inspiration=true, concentrating_on='bless', conditions='["poisoned","poisoned","exhaustion:2","raging"]' WHERE id=$1`, well)
	pgExec(t, `UPDATE characters SET hp=0, is_dead=false, is_stable=false, death_save_successes=2, death_save_failures=1 WHERE id=$1`, down)
	pgExec(t, `INSERT INTO companions (owner_id, lobby_id, kind, name, status, hp, max_hp, ac) VALUES ($1,$2,'steed','Pony','active',13,13,11)`, well, camp)

	pills := func() map[string]string { // each party card's pills, as the page carries them
		c, j := pgCall(t, "GET", fmt.Sprintf("/campaign/%d", camp), "", nil)
		page, _ := j["_body"].(string)
		if c != 200 || page == "" {
			t.Fatalf("page %d", c)
		}
		out := map[string]string{}
		for _, u := range appShellData(t, page).Party {
			var l []string
			for _, p := range u.Pills {
				l = append(l, p.Label)
			}
			out[u.Name] = strings.Join(l, " | ")
		}
		return out
	}
	got := pills()
	if want := "Conc: Bless | Poisoned | Exhaustion 2 | +7 Temp HP | ★ Inspiration | 🐎 Pony 13/13"; got[wellName] != want {
		t.Errorf("%s's pills\n got %q\nwant %q (a condition held twice is one pill, a marker none)", wellName, got[wellName], want)
	}
	if want := "Dying · saves 2✓ 1✗"; got[downName] != want {
		t.Errorf("%s's pills: %q, want %q", downName, got[downName], want)
	}
	pgExec(t, `UPDATE characters SET is_dead=true WHERE id=$1`, down)
	if got := pills(); got[downName] != "Dead" {
		t.Errorf("a dead character's pills: %q", got[downName])
	}
}
