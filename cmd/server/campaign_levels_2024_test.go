package main

import (
	"fmt"
	"strings"
	"testing"
)

// The level owner (campaign_levels_2024.go) is pure where it can be: who may join by level, and the money and items by tier.
func TestLevelMayJoin(t *testing.T) {
	cases := []struct {
		level    int
		played   bool
		min, max int
		want     bool
	}{
		{1, false, 1, 1, true},
		{1, false, 5, 7, true}, // a new character joins a campaign that starts higher, raised to min_level
		{3, false, 5, 7, true}, // so does one its player started higher
		{1, true, 5, 7, false}, // one that has played is not new
		{4, true, 5, 7, false},
		{5, true, 5, 7, true},
		{8, false, 5, 7, false}, // above the range, new or not
		{3, true, 1, 3, true},
	}
	for _, c := range cases {
		if got := levelMayJoin(c.level, c.played, c.min, c.max); got != c.want {
			t.Errorf("levelMayJoin(level %d, played %v, %d-%d) = %v", c.level, c.played, c.min, c.max, got)
		}
	}
	f := campaignFilter{level: 1}
	if !f.keeps(1, 5, 7) {
		t.Error("GET /api/campaigns?level=1 hides a campaign that starts at level 5, which a new character may join")
	}
	if (campaignFilter{level: 9}).keeps(1, 5, 7) {
		t.Error("?level=9 keeps a campaign for levels 5-7")
	}
	for level, want := range map[int]string{1: "", 2: "1 Common", 5: "1 Uncommon", 11: "1 Rare", 17: "1 Very Rare"} {
		gold, items := higherLevelStart(level)
		if !strings.Contains(items, want) {
			t.Errorf("level %d suggests %q", level, items)
		}
		if (level >= 5) != (gold > 0) {
			t.Errorf("level %d gives %d GP", level, gold)
		}
	}
	if g, _ := higherLevelStart(5); g < 525 || g > 750 {
		t.Errorf("level 5 money %d is outside 500 + 1d10 x 25", g)
	}
}

// Through the server: a campaign made at level 5, a new character raised to it as it joins, one that has played refused, the GM's
// change of the range, and the GM's reminder once the party has passed the starting level.
func TestPostgresCampaignLevelRange(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg level gm")
	_, plTok := pgAgent(t, "pg level player")
	_, otherTok := pgAgent(t, "pg level other")
	_, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Levels %d", gm), "max_players": 4,
		"min_level": 5, "max_level": 7})
	camp := int(j["campaign_id"].(float64))
	levels, _ := j["levels"].(map[string]interface{})
	if levels["min_level"] != float64(5) || levels["max_level"] != float64(7) || !strings.Contains(fmt.Sprint(levels["change"]), "/levels") ||
		!strings.Contains(fmt.Sprint(levels["gear"]), "Uncommon") {
		t.Fatalf("the creation reply's levels: %v", j["levels"])
	}
	if c, _ := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": "PG Bad Levels", "min_level": 6, "max_level": 3}); c != 400 {
		t.Errorf("min_level above max_level was accepted: %d", c)
	}

	// the list shows it to a new character
	_, list := pgCall(t, "GET", "/api/campaigns?level=1", "", nil)
	if !strings.Contains(fmt.Sprint(list), fmt.Sprintf("PG Levels %d", gm)) {
		t.Error("GET /api/campaigns?level=1 doesn't list a level 5 campaign")
	}

	// a new character joins at level 5 with money
	_, j = pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Newcomer %d", gm)))
	char := int(j["character_id"].(float64))
	var goldBefore int
	db.QueryRow(`SELECT COALESCE(gold, 0) FROM characters WHERE id = $1`, char).Scan(&goldBefore)
	c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), plTok, map[string]interface{}{"character_id": char})
	if c != 200 || !strings.Contains(fmt.Sprint(j["started_at_campaign_level"]), "level 5") {
		t.Fatalf("join: %d %v", c, j)
	}
	var level, xp, gold, maxHP int
	db.QueryRow(`SELECT level, COALESCE(xp, 0), COALESCE(gold, 0), max_hp FROM characters WHERE id = $1`, char).Scan(&level, &xp, &gold, &maxHP)
	if level != 5 || xp < 6500 || gold-goldBefore < 525 || gold-goldBefore > 750 || maxHP <= 10 {
		t.Fatalf("after joining: level %d, xp %d, gold +%d, max HP %d", level, xp, gold-goldBefore, maxHP)
	}
	var logged int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1 AND character_id = $2 AND action_type = 'level_up'`, camp, char).Scan(&logged)
	if logged == 0 {
		t.Error("the start at the campaign's level isn't in the campaign's log")
	}

	// a level 1 character that has played may not join
	_, j = pgCall(t, "POST", "/api/characters", otherTok, pgRogue(fmt.Sprintf("Pg Veteran %d", gm)))
	vet := int(j["character_id"].(float64))
	writeAction(db, camp, vet, "attack", "played in a campaign", "") // it has played in a campaign (and left it)
	c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), otherTok, map[string]interface{}{"character_id": vet})
	if c != 400 || !strings.Contains(fmt.Sprint(j["message"]), "new character") {
		t.Errorf("a character that has played in a campaign joined below the range: %d %v", c, j)
	}

	// the GM's change, and only the GM's
	if c, _ := pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/levels", camp), plTok, map[string]interface{}{"min_level": 2}); c != 403 {
		t.Errorf("a player changed the range: %d", c)
	}
	if c, _ := pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/levels", camp), gmTok, map[string]interface{}{"min_level": 0}); c != 400 {
		t.Errorf("min_level 0 was accepted: %d", c)
	}
	c, j = pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/levels", camp), gmTok, map[string]interface{}{"min_level": 3})
	if c != 200 || j["min_level"] != float64(3) || j["max_level"] != float64(7) {
		t.Fatalf("PATCH levels: %d %v", c, j)
	}
	if advice := levelRangeAdvice(camp); !strings.Contains(advice, `"min_level": 5`) {
		t.Errorf("the party is level 5 and starts newcomers at 3, but the GM's reminder says %q", advice)
	}
	c, j = pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/levels", camp), gmTok, map[string]interface{}{"min_level": 9})
	if c != 200 || j["max_level"] != float64(9) {
		t.Errorf("raising min_level above max_level didn't lift max_level: %d %v", c, j)
	}
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/levels", camp), gmTok, map[string]interface{}{"min_level": 5, "max_level": 7})
	if advice := levelRangeAdvice(camp); advice != "" {
		t.Errorf("a reminder with the starting level at the party's: %q", advice)
	}
	c, j = pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/levels", camp), "", nil)
	if c != 200 || j["min_level"] != float64(5) {
		t.Errorf("GET levels: %d %v", c, j)
	}
}

// A player starts a new character higher before it joins: the level-up rules, the money once per tier, and the refusals.
func TestPostgresStartingLevel(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg start gm")
	_, plTok := pgAgent(t, "pg start player")
	_, j := pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Starter %d", gm)))
	char := int(j["character_id"].(float64))
	path := fmt.Sprintf("/api/characters/%d/starting-level", char)
	gold := func() int {
		var g int
		db.QueryRow(`SELECT COALESCE(gold, 0) FROM characters WHERE id = $1`, char).Scan(&g)
		return g
	}
	if c, _ := pgCall(t, "POST", path, gmTok, map[string]interface{}{"level": 5}); c != 403 {
		t.Errorf("someone else's player started it higher: %d", c)
	}
	if c, _ := pgCall(t, "POST", path, plTok, map[string]interface{}{"level": 21}); c != 400 {
		t.Errorf("level 21 was accepted: %d", c)
	}
	g0 := gold()
	c, j := pgCall(t, "POST", path, plTok, map[string]interface{}{"level": 5})
	if c != 200 || j["level"] != float64(5) || !strings.Contains(fmt.Sprint(j["next"]), "/setup") {
		t.Fatalf("start at 5: %d %v", c, j)
	}
	g5 := gold()
	if g5-g0 < 525 || g5-g0 > 750 {
		t.Errorf("level 5 money: +%d", g5-g0)
	}
	if c, _ := pgCall(t, "POST", path, plTok, map[string]interface{}{"level": 4}); c != 400 {
		t.Errorf("a start lower than the character was accepted: %d", c)
	}
	if c, _ = pgCall(t, "POST", path, plTok, map[string]interface{}{"level": 7}); c != 200 || gold() != g5 {
		t.Errorf("a second start in the same tier paid money again: %d, %d -> %d", c, g5, gold())
	}
	if c, _ = pgCall(t, "POST", path, plTok, map[string]interface{}{"level": 11}); c != 200 || gold()-g0 < 5250 || gold()-g0 > 7500 {
		t.Errorf("a start into the next tier: %d, money in all +%d (the level 11 table: 5,250 to 7,500)", c, gold()-g0)
	}

	// joining a campaign that starts at 5 changes nothing; once in it, it can't start again
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Start %d", gm), "min_level": 5, "max_level": 12})
	camp := int(j["campaign_id"].(float64))
	if c, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), plTok, map[string]interface{}{"character_id": char}); c != 200 || j["started_at_campaign_level"] != nil {
		t.Fatalf("join at level 11: %d %v", c, j)
	}
	if c, j = pgCall(t, "POST", path, plTok, map[string]interface{}{"level": 12}); c != 409 || j["error"] != "in_campaign" {
		t.Errorf("a character in a campaign started higher: %d %v", c, j)
	}
	writeAction(db, camp, char, "attack", "played", "")
	pgExec(t, `UPDATE characters SET lobby_id = NULL WHERE id = $1`, char)
	if c, j = pgCall(t, "POST", path, plTok, map[string]interface{}{"level": 12}); c != 409 || j["error"] != "already_played" {
		t.Errorf("a character that has played started higher: %d %v", c, j)
	}
}
