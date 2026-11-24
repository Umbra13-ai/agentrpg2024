package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// Feedback log 30: the two-hands refusal names what fills the hands and the request that frees one.
func TestHandsRefusalNamesTheWayOut(t *testing.T) {
	for _, tc := range []struct {
		main, off string
		shield    bool
		want      []string
	}{
		{"Longsword", "", true, []string{`"shield": false`, "equip-armor"}},
		{"Shortsword", "Dagger", false, []string{"put away the Dagger", `"slot": "off_hand"`}},
		{"Shortsword", "Dagger", true, []string{"Dagger", `"shield": false`}},
		{"Greatsword", "", false, []string{"put away the Greatsword", `"slot": "main_hand"`}},
	} {
		got := freeHandRequests(7, tc.main, tc.off, tc.shield)
		for _, w := range tc.want {
			if !strings.Contains(got, w) || !strings.Contains(got, `"character_id": 7`) {
				t.Errorf("%s/%s/%v: %q lacks %q", tc.main, tc.off, tc.shield, got, w)
			}
		}
	}
	if freeHandRequests(0, "A", "B", true) != "" {
		t.Errorf("no character: no request to name")
	}
}

// Feedback log 12 and 37: the join reply's next step names the seat's check-in, says to set it a few minutes early, and what to do
// after; without a seat time it falls back to checking every 2 hours. Every check-in text says schedulers run late.
func TestJoinNextStep(t *testing.T) {
	s, timed := scheduleOf(4)
	if !timed {
		t.Fatal("4 seats should be timed")
	}
	m, _ := s.seatMinute(2)
	got := joinNextStep(s, true, 2, 9, "recruiting")
	for _, w := range []string{"Your next step", minuteText(m), "a few minutes early", "GET /api/my-turn?campaign_id=9", "/api/campaigns/9/ready"} {
		if !strings.Contains(got, w) {
			t.Errorf("recruiting: %q lacks %q", got, w)
		}
	}
	if got := joinNextStep(s, true, 2, 9, "active"); !strings.Contains(got, "is_my_turn") {
		t.Errorf("active: %q", got)
	}
	if got := joinNextStep(s, false, 2, 9, "recruiting"); !strings.Contains(got, "every 2 hours") {
		t.Errorf("schedule off: %q", got)
	}
	for _, text := range []string{seatNote(s, 2), checkInNote} {
		if !strings.Contains(text, "late") {
			t.Errorf("a check-in text doesn't say schedulers run late: %q", text)
		}
	}
}

// Feedback log 18: the GM is told the request that opens play after a held start.
func TestOpenPlayRequest(t *testing.T) {
	if got := openPlayRequest(12); got != "POST /api/campaigns/12/round/open" {
		t.Errorf("%q", got)
	}
	if !strings.Contains(tableHeldGMTask(12), openPlayRequest(12)) {
		t.Errorf("the held-table GM task doesn't name the request: %q", tableHeldGMTask(12))
	}
}

// Feedback log 39: the GM's player_activity and the readiness view give the same last sign of life; a player who has only talked
// in the party chat is active, not "never acted".
func TestPostgresPlayerActivityAgreesWithReadiness(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg activity gm")
	_, plTok := pgAgent(t, "pg activity player")
	_, j := pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Talker %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Activity %d", gm), "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), plTok, map[string]interface{}{"character_id": char}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	if c, _ := pgCall(t, "POST", "/api/campaigns/messages", plTok, map[string]interface{}{"campaign_id": camp, "message": "Hello, table."}); c != 200 {
		t.Fatalf("chat: %d", c)
	}
	time.Sleep(300 * time.Millisecond) // the request log is written after the reply
	_, st := pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", camp), gmTok, nil)
	var act map[string]interface{}
	for _, x := range asList(st["player_activity"]) {
		if m, _ := x.(map[string]interface{}); asInt(m["id"]) == char {
			act = m
		}
	}
	_, cv := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d", camp), gmTok, nil)
	ready := ""
	if bs, _ := cv["before_start"].(map[string]interface{}); bs != nil {
		if rd, _ := bs["readiness"].(map[string]interface{}); rd != nil {
			for _, x := range asList(rd["characters"]) {
				if m, _ := x.(map[string]interface{}); asInt(m["character_id"]) == char {
					ready = fmt.Sprint(m["last_active"])
				}
			}
		}
	}
	if act == nil || act["inactive_status"] != "active" || act["last_active"] == nil || ready == "" {
		t.Fatalf("player_activity %v; readiness last_active %q", act, ready)
	}
	a, _ := time.Parse(time.RFC3339, fmt.Sprint(act["last_active"]))
	r, _ := time.Parse(time.RFC3339Nano, ready)
	if d := a.Sub(r.Truncate(time.Second)); d < 0 || d > time.Second {
		t.Errorf("the two views disagree: player_activity %v, readiness %v", act["last_active"], ready)
	}
}

// Feedback log 40: a POST with ?_method=DELETE (or the X-HTTP-Method-Override header) is served as DELETE, also PATCH and PUT; a
// plain POST, or one naming another method, stays a POST; a signature is checked against the method sent.
func TestMethodOverride(t *testing.T) {
	for _, tc := range []struct {
		method, target, header, want string
	}{
		{"POST", "/api/campaigns/1/asks?_method=DELETE", "", "DELETE"},
		{"POST", "/api/campaigns/1/asks?_method=patch", "", "PATCH"},
		{"POST", "/api/campaigns/1/asks", "PUT", "PUT"},
		{"POST", "/api/campaigns/1/asks?_method=GET", "", "POST"},
		{"POST", "/api/campaigns/1/asks", "", "POST"},
		{"GET", "/api/campaigns/1/asks?_method=DELETE", "", "GET"},
	} {
		r := httptest.NewRequest(tc.method, tc.target, nil)
		if tc.header != "" {
			r.Header.Set("X-HTTP-Method-Override", tc.header)
		}
		got := overrideMethod(r)
		if got.Method != tc.want || signedMethod(got) != tc.method {
			t.Errorf("%s %s (%q): served as %s, signed as %s; want %s", tc.method, tc.target, tc.header, got.Method, signedMethod(got), tc.want)
		}
	}
	if h := deleteHow("/api/campaigns/3/asks", `{"character_id": 5}`); !strings.Contains(h, "POST /api/campaigns/3/asks?_method=DELETE") {
		t.Errorf("%q", h)
	}
}

// Feedback log 38: a weapon's attack bonus is one rule (weaponAttackMods) for the sheet and every attack. An Archery Fighter's Longbow
// shows +2 more than its Dexterity and proficiency, part by part, and the sheet's line is the rule's own; a melee weapon gets no
// Archery bonus.
func TestPostgresSheetAttackBonusIsTheAttacksOwn(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg archer")
	body := pgRogue(fmt.Sprintf("Pg Archer %d", time.Now().UnixNano()%1e9))
	body["class"], body["starting_equipment"] = "fighter", "b" // studded leather, scimitar, shortsword, longbow
	body["skill_proficiencies"] = []string{"acrobatics", "perception"}
	body["weapon_masteries"] = []string{"longbow", "scimitar", "shortsword"}
	c, j := pgCall(t, "POST", "/api/characters", tok, body)
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	if c, j := pgCall(t, "POST", "/api/characters/fighting-style", tok, map[string]interface{}{"character_id": id, "style": "archery"}); c != 200 {
		t.Fatalf("style: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", "/api/characters/equip-weapon", tok, map[string]interface{}{"character_id": id, "weapon": "longbow", "slot": "main_hand"}); c != 200 {
		t.Fatalf("equip: %d %v", c, j)
	}
	var str, dex, level int
	var profs string
	if err := db.QueryRow(`SELECT str, dex, level, COALESCE(weapon_proficiencies, '') FROM characters WHERE id = $1`, id).Scan(&str, &dex, &level, &profs); err != nil {
		t.Fatal(err)
	}
	key, bow, _ := findWeapon("longbow")
	m := weaponAttackMods(weaponUse{charID: id, class: "fighter", level: level, str: str, dex: dex, profs: profs, key: key, weapon: bow, hasWeapon: true})
	if !m.Archery || m.Attack != game.Modifier(dex)+game.ProficiencyBonus(level)+2 {
		t.Errorf("the Longbow's bonus: %+v", m)
	}
	// a "+3" the player writes in the attack's description is no magic bonus: the plain longbow it holds has none
	if claimed := weaponAttackMods(weaponUse{charID: id, class: "fighter", level: level, str: str, dex: dex, profs: profs, key: key, weapon: bow, hasWeapon: true,
		description: "I shoot my longbow +3 at the orc"}); claimed.Plus != 0 || claimed.Attack != m.Attack {
		t.Errorf("a +3 written in the description changed the attack: %+v, the bow's own %+v", claimed, m)
	}
	skey, sword, _ := findWeapon("scimitar")
	if s := weaponAttackMods(weaponUse{charID: id, class: "fighter", level: level, str: str, dex: dex, profs: profs, key: skey, weapon: sword, hasWeapon: true}); s.Archery {
		t.Errorf("Archery on a melee weapon: %+v", s)
	}
	_, sheet := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/profile", id), tok, nil) // the stat block the character page shows
	if !strings.Contains(fmt.Sprint(sheet["statblock"]), "Ranged Weapon Attack: "+m.toHit()) || !strings.Contains(m.toHit(), "Archery +2") {
		t.Errorf("the sheet's Longbow line isn't the attack's own %q: %v", m.toHit(), sheet["statblock"])
	}
}

// Feedback log 52: the join reply's next step says what a check-in reads and what a missed turn costs.
func TestJoinNextStepSaysWhatAMissCosts(t *testing.T) {
	s, _ := scheduleOf(4)
	got := joinNextStep(s, true, 1, 9, "recruiting")
	for _, w := range []string{"/api/campaigns/messages?campaign_id=9", "stand-in", minuteText(s.final())} {
		if !strings.Contains(got, w) {
			t.Errorf("%q lacks %q", got, w)
		}
	}
}

// Feedback log 53: my-turn for a campaign that doesn't exist is 404 campaign_not_found, not no_active_game.
func TestPostgresMyTurnForAGoneCampaign(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg gone campaign")
	if c, j := pgCall(t, "GET", "/api/my-turn?campaign_id=987654321", tok, nil); c != 404 || j["error"] != "campaign_not_found" {
		t.Errorf("%d %v", c, j)
	}
	if c, j := pgCall(t, "GET", "/api/my-turn", tok, nil); c != 200 || j["error"] != "no_active_game" {
		t.Errorf("no campaign named: %d %v", c, j)
	}
}

// Feedback log 51: a choice made several times says how many are left, from the choice table's counts, for every counted choice.
func TestCountedStepTitles(t *testing.T) {
	var inv *choiceDef
	for _, d := range choiceDefs {
		if d.key == "invocations" {
			inv = d
		}
		if d.many != "" {
			if got := countedTitle(d, 1, 4); !strings.Contains(got, "3 more "+d.many) || !strings.Contains(got, "(1 of 4 chosen)") {
				t.Errorf("%s: %q", d.key, got)
			}
			if countedTitle(d, 4, 4) != "" {
				t.Errorf("%s: a finished choice has a counted title", d.key)
			}
		}
	}
	if inv == nil || countedTitle(inv, 2, 7) != "Choose 5 more Eldritch Invocations (2 of 7 chosen)" {
		t.Errorf("invocations: %q", countedTitle(inv, 2, 7))
	}
}

// Feedback log 57a: a player's character leaves its campaign (its seat is freed, the character stays the player's); leaving again,
// or a campaign it isn't in, is 404 not_in_campaign. The GM's removal goes the same way (leaveCampaign).
func TestPostgresPlayerLeavesACampaign(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg leave gm")
	_, tok := pgAgent(t, "pg leave player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Leaver %d", time.Now().UnixNano()%1e9)))
	id := int(j["character_id"].(float64))
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Leave %d", gm), "max_players": 1})
	camp := int(j["campaign_id"].(float64))
	leave := fmt.Sprintf("/api/campaigns/%d/leave", camp)
	if c, j := pgCall(t, "POST", leave, tok, nil); c != 404 || j["error"] != "not_in_campaign" {
		t.Errorf("leave before joining: %d %v", c, j)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id})
	if c, j := pgCall(t, "POST", leave, tok, map[string]interface{}{"character_id": id}); c != 200 {
		t.Fatalf("leave: %d %v", c, j)
	}
	var lobby sql.NullInt64
	db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, id).Scan(&lobby)
	if lobby.Valid {
		t.Errorf("still in campaign %d", lobby.Int64)
	}
	_, other := pgAgent(t, "pg leave other")
	_, j = pgCall(t, "POST", "/api/characters", other, pgRogue(fmt.Sprintf("Pg Seat Taker %d", time.Now().UnixNano()%1e9)))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), other, map[string]interface{}{"character_id": int(j["character_id"].(float64))}); c != 200 {
		t.Errorf("the freed seat (capacity 1) can't be taken: %d %v", c, j)
	}
}

// Feedback log 50: the heartbeat is a digest. A level-1 character raised to 11 on joining (Kestrel's case: 36 KB, 20 KB of level-up
// notices) gets the count, the levels and the newest notice; the join reply's start text is a summary pointing to setup.
func TestPostgresHeartbeatIsADigest(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "pg digest gm")
	_, tok := pgAgent(t, "pg digest player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Raised %d", time.Now().UnixNano()%1e9)))
	id := int(j["character_id"].(float64))
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Digest %d", gm), "max_players": 2, "min_level": 11, "max_level": 11})
	camp := int(j["campaign_id"].(float64))
	c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id})
	if c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	if s := fmt.Sprint(j["started_at_campaign_level"]); len(s) > 600 || !strings.Contains(s, "/setup") {
		t.Errorf("the start text (%d chars): %s", len(s), s)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/heartbeat", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	newServer("").Handler.ServeHTTP(w, r)
	var hb map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &hb)
	var entry map[string]interface{}
	for _, x := range asList(hb["campaigns_as_player"]) {
		if m, _ := x.(map[string]interface{}); asInt(m["id"]) == camp {
			entry = m
		}
	}
	lu, _ := entry["level_up"].(map[string]interface{})
	if lu == nil || asInt(lu["unseen"]) != 10 || lu["latest"] == nil || len(asList(lu["levels"])) != 10 {
		t.Errorf("level_up digest: %v", lu)
	}
	if n := w.Body.Len(); n > 15000 {
		sizes := map[string]int{}
		for k, v := range entry {
			b, _ := json.Marshal(v)
			sizes[k] = len(b)
		}
		for k, v := range hb {
			b, _ := json.Marshal(v)
			sizes["top:"+k] = len(b)
		}
		t.Errorf("the heartbeat is %d bytes for one campaign: %v", n, sizes)
	}
}

// Feedback log 46: outside a fight a move isn't the story action (Dash is); 56: every round's opening has its party-chat line.
func TestMoveIsNotTheBeatAndRoundLines(t *testing.T) {
	if actionUsesBeat("move") || !actionUsesBeat("dash") || !actionUsesBeat("search") {
		t.Errorf("move %v, dash %v, search %v", actionUsesBeat("move"), actionUsesBeat("dash"), actionUsesBeat("search"))
	}
	r := &roundRecord{Number: 2, Kind: roundNoncombat, OpenedAt: at("10:00:00"), Deadline: nullTime(at("10:55:00"))}
	if l := roundOpenedLine(r); !strings.Contains(l, "Campaign round 2 began at") || !strings.Contains(l, "final check-in") {
		t.Errorf("%q", l)
	}
	r.Number = 1
	if l := roundOpenedLine(r); !strings.HasPrefix(l, "Play is open") {
		t.Errorf("%q", l)
	}
}
