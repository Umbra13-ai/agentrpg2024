package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLiveCardHTML(t *testing.T) {
	has := func(name, got string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: no %q in\n%s", name, w, got)
			}
		}
	}
	hasNot := func(name, got string, not ...string) {
		t.Helper()
		for _, w := range not {
			if strings.Contains(got, w) {
				t.Errorf("%s: unexpected %q in\n%s", name, w, got)
			}
		}
	}
	base := liveCard{ID: 7, Name: "Calla <b>Underbough</b>", Icon: "☀️", Meta: "Level 3 Halfling Cleric", HP: 24, MaxHP: 24, AC: 16, Health: "healthy", Activity: "Active now", HitDice: "3d8"}

	// a healthy character: name escaped, the bar full, no footer when nothing is on it
	got := liveCardHTML(base)
	has("healthy", got, `data-inspect-char="7"`, "Calla &lt;b&gt;Underbough&lt;/b&gt;", "HP 24/24", "HD 3d8", "AC 16", `width:100%; background:var(--accent-emerald)`)
	hasNot("healthy", got, "party-bottom-row", "insp-pip", "party-temp", "is-turn", "is-dead")

	// Temporary Hit Points: the +N beside the Hit Points and their own segment of the bar, never less than 4%
	c := base
	c.TempHP = 5
	got = liveCardHTML(c)
	has("temp hp", got, `class="font-mono party-temp"`, "+5", `class="thp-fill-seg" style="width:17%;"`, "24/24 + 5 Temp HP")
	c.HP, c.MaxHP, c.TempHP = 10, 100, 1
	has("a sliver of temp hp", liveCardHTML(c), `class="thp-fill-seg" style="width:4%;"`)

	// the turn, Heroic Inspiration, a companion, Concentration and a condition
	c = base
	c.IsTurn, c.Inspired = true, true
	c.Conds = []liveCond{{"Conc: Bless", "arcane"}, {"Poisoned", "crimson"}}
	c.Comps = []liveComp{{Icon: "🐎", Label: "Horse", Title: "Steed of Calla", HP: 13, MaxHP: 13}, {Icon: "🦉", Label: "Owl", Title: "Familiar of Calla", Away: true}}
	got = liveCardHTML(c)
	has("turn", got, "party-card is-turn", ">Turn<", `class="insp-pip"`, "cond-arcane", "Conc: Bless", "cond-crimson", "Poisoned", "Horse", "13/13", "Owl", "away")

	// dying: the Death Saving Throw dots (1 success, 2 failures)
	c = base
	c.HP, c.Health, c.DeathSucc, c.DeathFail = 0, "dying", 1, 2
	got = liveCardHTML(c)
	has("dying", got, "cond-dying", "Dying", `<span class="ds-dot is-succ"></span><span class="ds-dot"></span><span class="ds-dot"></span>`,
		`<span class="ds-dot is-fail"></span><span class="ds-dot is-fail"></span><span class="ds-dot"></span>`, "1 successes, 2 failures")

	// stable, and dead (greyed, no Temporary Hit Points, whatever the Hit Points say)
	c = base
	c.HP, c.Health = 0, "stable"
	has("stable", liveCardHTML(c), "Stable")
	c = base
	c.HP, c.Health, c.TempHP, c.Inspired = 3, "dead", 5, false
	got = liveCardHTML(c)
	has("dead", got, "party-card is-dead", "cond-dead", "Dead", "HP 0/24", `title="HP 0/24"`)
	hasNot("dead", got, "party-temp", "thp-fill-seg", "cond-dying")

	// a portrait replaces the class icon
	c = base
	c.HasPortrait, c.PortraitAt = true, 99
	has("portrait", liveCardHTML(c), `src="/api/characters/7/portrait?v=99"`)
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

	c, j = pgCall(t, "GET", fmt.Sprintf("/campaign/%d", camp), "", nil)
	page, _ := j["_body"].(string)
	if c != 200 || page == "" {
		t.Fatalf("page %d", c)
	}
	for _, want := range []string{wellName, "+7", "Heroic Inspiration", "Conc: Bless", "Poisoned", "Exhaustion 2", "Pony", "13/13", "HD ", downName, "Dying", "2 successes, 1 failures"} {
		if !strings.Contains(page, want) {
			t.Errorf("the Live Table has no %q", want)
		}
	}
	if n := strings.Count(page, ">Poisoned<"); n != 1 {
		t.Errorf("a condition held twice shows %d times", n)
	}
	for _, not := range []string{"Raging", "raging", "companion-box"} { // a marker is not a condition; the companion boxes are gone
		if strings.Contains(page, not) {
			t.Errorf("the Live Table shows %q", not)
		}
	}
	pgExec(t, `UPDATE characters SET is_dead=true WHERE id=$1`, down)
	_, j = pgCall(t, "GET", fmt.Sprintf("/campaign/%d", camp), "", nil)
	if page, _ = j["_body"].(string); !strings.Contains(page, "party-card is-dead") || !strings.Contains(page, ">Dead<") {
		t.Error("a dead character's card is not marked")
	}
}
