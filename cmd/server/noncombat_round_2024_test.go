package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Every route a player can send that writes anything is in routeEffects (round_effects_2024.go), so the turn schedule knows
// whether it is a game action; every row names a real route; a game action says how it names its character and an immediate
// route says why it is immediate.
func TestEveryPlayerWriteRouteHasAnEffect(t *testing.T) {
	raw, err := os.ReadFile("../../docs/swagger/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	inRef := map[string]bool{}
	for path, ops := range ref.Paths {
		gmOnly := false
		for _, p := range gmOnlyPrefixes {
			gmOnly = gmOnly || strings.HasPrefix(path, p)
		}
		for m := range ops {
			method := strings.ToUpper(m)
			if gmOnly || (method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE") {
				continue
			}
			inRef[method+" "+path] = true
			found := false
			for _, e := range routeEffects {
				found = found || (e.Method == method && e.Path == path)
			}
			if !found {
				t.Errorf("%s %s: a player write route with no row in routeEffects (is it a game action, gear, or immediate, and why?)", method, path)
			}
		}
	}
	seen := map[string]bool{}
	for _, e := range routeEffects {
		key := e.Method + " " + e.Path
		if seen[key] {
			t.Errorf("%s: two rows in routeEffects", key)
		}
		seen[key] = true
		if !inRef[key] {
			t.Errorf("%s: a row of routeEffects that is no player write route of the API reference", key)
		}
		switch e.Effect {
		case effectImmediate:
			if e.Why == "" || e.Character != "" {
				t.Errorf("%s: an immediate route needs its reason and no character rule", key)
			}
		case effectSubmission, effectGear:
			if e.Character != byPath && e.Character != byBody && e.Character != byActor {
				t.Errorf("%s: a game action needs how it names its character", key)
			}
			if e.Character == byPath && !strings.HasPrefix(e.Path, "/api/characters/{id}/") {
				t.Errorf("%s: names its character by the path, but the path has no character id", key)
			}
		default:
			t.Errorf("%s: unknown effect %q", key, e.Effect)
		}
	}
}

func TestPathMatches(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		ok            bool
		id            int
	}{
		{"/api/characters/{id}/rest", "/api/characters/12/rest", true, 12},
		{"/api/characters/{id}/rest", "/api/characters/12/short-rest", false, 0},
		{"/api/campaigns/{id}/shops/{shop_id}", "/api/campaigns/3/shops/7", true, 3},
		{"/api/campaigns/{id}/shops/{shop_id}", "/api/campaigns/3/shops", false, 0},
		{"/api/action", "/api/action", true, 0},
		{"/api/action", "/api/actions", false, 0},
	} {
		ok, id := pathMatches(c.pattern, c.path)
		if ok != c.ok || id != c.id {
			t.Errorf("pathMatches(%q, %q) = %v %d, want %v %d", c.pattern, c.path, ok, id, c.ok, c.id)
		}
	}
	if !actionIsImmediate("speak") || actionIsImmediate("search") || !actionUsesBeat("search") || actionUsesBeat("interact") {
		t.Error("speak is talk (immediate); search is a story action; interact is a free part of a turn")
	}
}

// A round outside a fight, through the whole server (Matt's directions section 7, answers 11.2, 11.6, 11.8, 11.9, 11.10): the
// campaign's start opens it; a story action is saved, not applied; talk stays immediate; one story action a round; gear is setup
// until the character has played a round; only the GM resolves; a stand-in waits for the final check-in; when every turn has
// resolved the next round opens; the old exploration skip is refused.
func TestPostgresNoncombatRound(t *testing.T) {
	t.Setenv(testClockEnv, "1")
	testPostgres(t)
	_, gm := pgAgent(t, "pg round gm")
	c, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Round", "max_players": 2})
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	lobby := int(j["campaign_id"].(float64))
	if s, _ := j["schedule"].(map[string]interface{}); s["state"] != scheduleOn {
		t.Fatalf("a new campaign uses the turn schedule from creation: %v", j["schedule"])
	}
	join := func(who string) (int, string) {
		_, tok := pgAgent(t, "pg round "+who)
		c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg %s %d", who, time.Now().UnixNano()%1e9)))
		if c != 200 {
			t.Fatalf("create %s: %d %v", who, c, j)
		}
		id := int(j["character_id"].(float64))
		if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": id}); c != 200 {
			t.Fatalf("join %s: %d %v", who, c, j)
		}
		return id, tok
	}
	ann, annTok := join("Ann")
	ben, benTok := join("Ben")
	base := fmt.Sprintf("/api/campaigns/%d", lobby)
	if c, j := pgCall(t, "POST", base+"/test-clock", gm, map[string]interface{}{"at": "2026-11-16T10:00:00Z"}); c != 200 {
		t.Fatalf("test clock: %d %v", c, j)
	}
	// before the campaign starts nothing is a submission: gear and setup apply
	pgFinish(t, annTok, ann)
	pgFinish(t, benTok, ben)
	pgCall(t, "POST", base+"/ready", annTok, nil) // every player is ready before the start
	pgCall(t, "POST", base+"/ready", benTok, nil)
	pgCall(t, "POST", base+"/ready", gm, nil)
	if c, j := pgCall(t, "POST", base+"/start", gm, nil); c != 200 || j["round"] == nil {
		t.Fatalf("the start opens the first round outside a fight: %d %v", c, j)
	}

	c, j = pgCall(t, "POST", "/api/action", annTok, map[string]interface{}{"action": "search", "description": "I search the room for hidden doors"})
	if c != 202 || j["submitted"] != true || !strings.Contains(fmt.Sprint(j["message"]), "round 1") {
		t.Fatalf("a story action is saved for the round: %d %v", c, j)
	}
	var acted int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE character_id = $1 AND action_type = 'search'`, ann).Scan(&acted)
	if acted != 0 {
		t.Fatal("a submission was applied before the GM resolved it")
	}
	if c, j := pgCall(t, "POST", "/api/action", annTok, map[string]interface{}{"action": "speak", "description": "Anyone there?"}); c == 202 || j["submitted"] == true {
		t.Errorf("speech stays immediate (Matt 11.8): %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", "/api/action", annTok, map[string]interface{}{"action": "study", "description": "I study the runes"}); c != 409 || j["error"] != "one_story_action_per_round" {
		t.Errorf("a second story action in the round: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", "/api/action", annTok, map[string]interface{}{"action": "other", "description": "I cast Sacred Flame at the shadow"}); c != 400 || j["error"] != "declaration_has_its_own_action" {
		t.Errorf("a declaration that casts a spell is refused, not saved: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", "/api/characters/unequip-armor", annTok, map[string]interface{}{"character_id": ann}); c == 202 {
		t.Errorf("gear is setup until the character has played a round (Matt 11.9): %d %v", c, j)
	}
	if _, j := pgCall(t, "GET", "/api/my-turn", annTok, nil); fmt.Sprint(j["round"].(map[string]interface{})["status"]) != statusSubmitted {
		t.Errorf("my-turn shows the round and Ann's status in it: %v", j["round"])
	}
	_, hb := pgCall(t, "GET", "/api/heartbeat", annTok, nil)
	if !strings.Contains(fmt.Sprint(hb), "round_status") {
		t.Errorf("the heartbeat shows the round's status: %.300v", hb)
	}
	if _, j := pgCall(t, "GET", base+"/round", annTok, nil); len(j["submissions"].([]interface{})) != 1 {
		t.Errorf("the player sees its own submission: %v", j["submissions"])
	}
	if c, j := pgCall(t, "POST", base+"/round/stand-in", gm, map[string]interface{}{"character_id": ben, "description": "Ben keeps watch"}); c != 409 || j["error"] != "stand_in_too_early" {
		t.Errorf("a stand-in before the final check-in: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", "/api/action", benTok, map[string]interface{}{"action": "other", "description": "I keep watch at the door and whisper to Ann"})
	if c != 202 || j["submitted"] != true {
		t.Fatalf("a declaration (\"other\") outside a fight is saved for the GM: %d %v", c, j)
	}
	if c, j := pgCall(t, "DELETE", fmt.Sprintf("%s/round?character_id=%d", base, ben), benTok, nil); c != 200 {
		t.Fatalf("withdrawing it (so the round reaches its stand-in): %d %v", c, j)
	}

	c, j = pgCall(t, "POST", base+"/round/resolve", gm, map[string]interface{}{"character_id": ann})
	res, _ := j["resolved"].([]interface{})
	if c != 200 || len(res) != 1 || res[0].(map[string]interface{})["resolved"] != true || j["round_complete"] != nil {
		t.Fatalf("the GM resolves Ann's turn; the round stays open for Ben: %d %v", c, j)
	}
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE character_id = $1 AND action_type = 'search'`, ann).Scan(&acted)
	if acted != 1 {
		t.Errorf("the resolved submission ran once (search actions: %d)", acted)
	}
	if c, j := pgCall(t, "POST", "/api/characters/unequip-armor", annTok, map[string]interface{}{"character_id": ann}); c != 409 || j["error"] != codeTurnAlreadyResolve {
		t.Errorf("once Ann has played a round her gear is a game action, and her turn is done: %d %v", c, j)
	}

	if c, j := pgCall(t, "POST", base+"/test-clock", gm, map[string]interface{}{"advance_minutes": 56}); c != 200 {
		t.Fatalf("test clock: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", base+"/round/stand-in", gm, map[string]interface{}{"character_id": ben, "description": "Ben keeps watch by the door"})
	if c != 200 || j["round_complete"] == nil || j["next_round"] != float64(2) {
		t.Fatalf("the stand-in at the final check-in completes the round and the next opens: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", base+"/round/resolve", gm, nil); c != 404 || j["error"] != "nothing_submitted" {
		t.Errorf("nothing is waiting in the new round: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", base+"/exploration/skip", gm, map[string]interface{}{"character_id": ben}); c != 409 || j["error"] != "schedule_uses_stand_ins" {
		t.Errorf("the old exploration skip, with the turn schedule: %d %v", c, j)
	}
}
