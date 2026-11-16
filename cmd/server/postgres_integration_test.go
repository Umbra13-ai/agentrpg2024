package main

// Tests against a real Postgres database, through the whole server (withProtections, guardDB, the router, token sign-in).
//
// They run only with TEST_DATABASE_URL, which must name a throwaway database whose name contains "test": the server's own start
// (startDatabase: the schema and the SRD data) runs on it and the tests write rows. DATABASE_URL is never read here: it names
// a real server's database (audit AUD-50: the old tests fell back to it). Without TEST_DATABASE_URL they are skipped, unless
// REQUIRE_TEST_DATABASE=1 (CI sets it): then a missing database fails the run instead of passing it silently.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	pgOnce  sync.Once
	pgDB    *sql.DB
	pgErr   error
	pgRoute sync.Once
)

// testPostgres points db at the test database (started once per run) for the calling test, or skips it.
func testPostgres(t *testing.T) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		if os.Getenv("REQUIRE_TEST_DATABASE") != "" {
			t.Fatal("REQUIRE_TEST_DATABASE is set but TEST_DATABASE_URL isn't")
		}
		t.Skip("TEST_DATABASE_URL not set")
	}
	u, err := url.Parse(dbURL)
	if err != nil || !strings.Contains(strings.ToLower(strings.TrimPrefix(u.Path, "/")), "test") {
		t.Fatalf("TEST_DATABASE_URL must name a throwaway database whose name contains \"test\" (the tests write to it)")
	}
	pgOnce.Do(func() {
		saved, savedWait := db, startupWait
		startupWait = func(int) time.Duration { return 100 * time.Millisecond }
		pgErr = startDatabase(dbURL)
		pgDB, db, startupWait = db, saved, savedWait
	})
	if pgErr != nil {
		t.Fatalf("the server's start on the test database failed: %v", pgErr)
	}
	saved := db
	db = pgDB
	t.Cleanup(func() { db = saved })
	pgRoute.Do(setupRoutes)
}

// pgCall sends one request through the whole server (withProtections, guardDB, the router) and decodes a JSON reply.
func pgCall(t *testing.T, method, path, token string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = fmt.Sprintf("10.9.%d.%d:1234", time.Now().UnixNano()%250, time.Now().UnixNano()/250%250) // the registration limit is per address
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	newServer("").Handler.ServeHTTP(w, r)
	out := map[string]interface{}{}
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out) == 0 {
		out["_body"] = w.Body.String()
	}
	return w.Code, out
}

// pgAgent registers a new account and returns its id and token.
func pgAgent(t *testing.T, name string) (int, string) {
	t.Helper()
	name = fmt.Sprintf("%s %d", name, time.Now().UnixNano()%1e9)
	c, j := pgCall(t, "POST", "/api/register", "", map[string]interface{}{"name": name})
	if c != 200 || j["token"] == nil {
		t.Fatalf("register %s: %d %v", name, c, j)
	}
	return int(j["agent_id"].(float64)), j["token"].(string)
}

// pgExec runs a statement of the test's own setup (a schema change here is the test's, not a request's).
func pgExec(t *testing.T, q string, args ...interface{}) {
	t.Helper()
	schemaChangesAllowed.Store(true)
	defer schemaChangesAllowed.Store(false)
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// watchStatements records the schema changes and the queries outside their transaction that requests send while the test runs.
func watchStatements(t *testing.T) *[]string {
	var mu sync.Mutex
	got := &[]string{}
	oldSchema, oldTx := schemaChangeOutside, txOutside
	schemaChangeOutside = func(q, from string) { mu.Lock(); *got = append(*got, "schema change: "+q+" from "+from); mu.Unlock() }
	txOutside = func(q, began, from string) {
		mu.Lock()
		*got = append(*got, "outside its transaction: "+q+" from "+from)
		mu.Unlock()
	}
	t.Cleanup(func() { schemaChangeOutside, txOutside = oldSchema, oldTx })
	return got
}

// The start makes the whole schema on an empty database, runs again without error on a made one, and /health asks the database.
func TestPostgresStartup(t *testing.T) {
	testPostgres(t)
	if err := migrate(); err != nil {
		t.Fatalf("the migrations are not idempotent: %v", err)
	}
	for _, col := range [][2]string{{"characters", "slot_spent_turn"}, {"characters", "subclasses"}, {"characters", "deleted_at"},
		{"companions", "traits"}, {"pending_turns", "steps"}, {"admin_changes", "by_whom"}, {"agent_tokens", "token_hash"},
		{"monsters", "lore"}, {"spells", "damage_parts"}, {"lobbies", "marching_order"}, {"level_up_notices", "seen_at"}} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`, col[0], col[1]).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s.%s missing after the start (%v)", col[0], col[1], err)
		}
	}
	if len(srdSpellsMemory) == 0 {
		t.Error("no spells were loaded")
	}
	c, j := pgCall(t, "GET", "/health", "", nil)
	if c != 200 || j["_body"] != "ok" {
		t.Errorf("/health with the database up: %d %v", c, j)
	}
}

// Ordinary requests change no schema (audit AUD-11) and read nothing outside their transaction (AUD-02).
func TestPostgresRequestsChangeNoSchema(t *testing.T) {
	testPostgres(t)
	got := watchStatements(t)
	gm, gmTok := pgAgent(t, "pg gm")
	_, plTok := pgAgent(t, "pg player")
	c, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Test %d", gm), "max_players": 4})
	if c != 200 && c != 201 {
		t.Fatalf("create campaign: %d %v", c, j)
	}
	camp := int(j["campaign_id"].(float64))
	c, j = pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Wizard %d", gm)))
	if c != 200 {
		t.Fatalf("create character: %d %v", c, j)
	}
	char := int(j["character_id"].(float64))
	for _, rq := range []struct{ method, path, tok string }{
		{"GET", "/api/my-turn", plTok}, {"GET", fmt.Sprintf("/api/characters/%d", char), plTok},
		{"GET", fmt.Sprintf("/api/characters/%d/setup", char), plTok}, {"GET", "/api/campaigns", ""},
		{"GET", fmt.Sprintf("/api/campaigns/%d", camp), gmTok}, {"GET", "/api/gm/status", gmTok}, {"GET", "/api/heartbeat", plTok},
	} {
		if c, j := pgCall(t, rq.method, rq.path, rq.tok, nil); c >= 500 {
			t.Errorf("%s %s: %d %v", rq.method, rq.path, c, j)
		}
	}
	if len(*got) > 0 {
		t.Errorf("requests sent statements they mustn't:\n%s", strings.Join(*got, "\n"))
	}
}

// pgRogue: a complete creation request (a Dwarf Soldier Rogue, as expertise_rule_test.py sends it).
func pgRogue(name string) map[string]interface{} {
	return map[string]interface{}{"name": name, "class": "rogue", "background": "soldier", "species": "dwarf", "ability_method": "standard_array",
		"str": 12, "dex": 15, "con": 13, "int": 14, "wis": 10, "cha": 8, "ability_increases": map[string]int{"dex": 2, "con": 1},
		"skill_proficiencies": []string{"acrobatics", "stealth", "deception", "perception"}, "weapon_masteries": []string{"dagger", "shortbow"},
		"background_tool": "dice set", "background_equipment": "a", "starting_equipment": "a", "extra_languages": []string{"Elvish", "Goblin"},
		"alignment": "Neutral", "backstory": "One.\n\nTwo.\n\nThree."}
}

// Character creation is one transaction (audit AUD-27): when a step after the first INSERT fails, no character is left behind,
// the reply is the one database_error shape, and the same request succeeds once the database works.
func TestPostgresCharacterCreationIsAtomic(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg creator")
	name := fmt.Sprintf("Pg Atomic %d", time.Now().UnixNano()%1e9)
	pgExec(t, `CREATE OR REPLACE FUNCTION pg_test_refuse_inventory() RETURNS trigger AS $$ BEGIN
		IF NEW.name = '`+name+`' THEN RAISE EXCEPTION 'test: inventory refused'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql`)
	pgExec(t, `CREATE TRIGGER pg_test_refuse_inventory BEFORE UPDATE OF inventory ON characters FOR EACH ROW EXECUTE FUNCTION pg_test_refuse_inventory()`)
	dropped := false
	drop := func() {
		if !dropped {
			pgExec(t, `DROP TRIGGER IF EXISTS pg_test_refuse_inventory ON characters`)
			pgExec(t, `DROP FUNCTION IF EXISTS pg_test_refuse_inventory()`)
			dropped = true
		}
	}
	t.Cleanup(drop)
	c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(name))
	if c != 500 || j["error"] != "database_error" || strings.Contains(fmt.Sprint(j), "test: inventory refused") {
		t.Fatalf("a failed step answered %d %v", c, j)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM characters WHERE name = $1`, name).Scan(&n)
	if n != 0 {
		t.Fatalf("a half-made character was left behind (%d rows)", n)
	}
	drop()
	c, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(name))
	if c != 200 {
		t.Fatalf("the retry: %d %v", c, j)
	}
	var feats, inv string
	db.QueryRow(`SELECT COALESCE(feats::text,''), COALESCE(inventory::text,'') FROM characters WHERE id = $1`, int(j["character_id"].(float64))).Scan(&feats, &inv)
	if !strings.Contains(feats, "savage") || !strings.Contains(strings.ToLower(inv), "dagger") {
		t.Errorf("the character lacks its feat or its inventory: feats %s, inventory %.80s", feats, inv)
	}
}

// pgModerator: a new account made a moderator.
func pgModerator(t *testing.T) string {
	id, tok := pgAgent(t, "pg mod")
	pgExec(t, `UPDATE agents SET is_moderator = true WHERE id = $1`, id)
	return tok
}

// A moderator's delete of a campaign is the soft delete, all or nothing (audit AUD-28).
func TestPostgresModDeleteCampaignIsAtomic(t *testing.T) {
	testPostgres(t)
	mod := pgModerator(t)
	gm, gmTok := pgAgent(t, "pg gm2")
	_, plTok := pgAgent(t, "pg player2")
	_, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Delete %d", gm), "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	_, j = pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Member %d", gm)))
	char := int(j["character_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), plTok, map[string]interface{}{"character_id": char}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	// a failure in the middle (letting the characters go) changes nothing
	pgExec(t, `CREATE OR REPLACE FUNCTION pg_test_refuse_release() RETURNS trigger AS $$ BEGIN
		IF OLD.lobby_id = `+fmt.Sprint(camp)+` AND NEW.lobby_id IS NULL THEN RAISE EXCEPTION 'test: release refused'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql`)
	pgExec(t, `CREATE TRIGGER pg_test_refuse_release BEFORE UPDATE OF lobby_id ON characters FOR EACH ROW EXECUTE FUNCTION pg_test_refuse_release()`)
	c, j := pgCall(t, "POST", "/api/mod/delete-campaign", mod, map[string]interface{}{"campaign_id": camp})
	pgExec(t, `DROP TRIGGER IF EXISTS pg_test_refuse_release ON characters`)
	pgExec(t, `DROP FUNCTION IF EXISTS pg_test_refuse_release()`)
	if c != 500 || j["error"] != "database_error" {
		t.Fatalf("a failed delete answered %d %v", c, j)
	}
	var status string
	var lobby sql.NullInt64
	db.QueryRow(`SELECT status FROM lobbies WHERE id = $1`, camp).Scan(&status)
	db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, char).Scan(&lobby)
	if status == "deleted" || !lobby.Valid {
		t.Fatalf("a failed delete changed something: status %s, character's campaign %v", status, lobby)
	}
	c, j = pgCall(t, "POST", "/api/mod/delete-campaign", mod, map[string]interface{}{"campaign_id": camp})
	if c != 200 {
		t.Fatalf("delete: %d %v", c, j)
	}
	var name string
	db.QueryRow(`SELECT status FROM lobbies WHERE id = $1`, camp).Scan(&status)
	db.QueryRow(`SELECT lobby_id, name FROM characters WHERE id = $1`, char).Scan(&lobby, &name)
	if status != "deleted" || lobby.Valid || !strings.HasPrefix(name, "Pg Member") {
		t.Errorf("after the delete: status %s, character's campaign %v, name %s (the character is let go, not erased)", status, lobby, name)
	}
	if c, _ := pgCall(t, "POST", "/api/mod/delete-campaign", mod, map[string]interface{}{"campaign_id": camp}); c != 400 {
		t.Errorf("a second delete answered %d", c)
	}
}

// A moderator's delete of an account is all or nothing: a GM's account is refused with nothing changed; a player's characters are
// soft-deleted and its tokens stop working.
func TestPostgresModDeleteUser(t *testing.T) {
	testPostgres(t)
	mod := pgModerator(t)
	gm, gmTok := pgAgent(t, "pg gm3")
	pl, plTok := pgAgent(t, "pg player3")
	_, j := pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Owner %d", gm), "max_players": 4})
	camp := int(j["campaign_id"].(float64))
	_, j = pgCall(t, "POST", "/api/characters", plTok, pgRogue(fmt.Sprintf("Pg Leaver %d", pl)))
	char := int(j["character_id"].(float64))
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), plTok, map[string]interface{}{"character_id": char}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	c, j := pgCall(t, "POST", "/api/mod/delete-user", mod, map[string]interface{}{"user_id": gm})
	if c != 409 || j["error"] != "account_runs_campaigns" {
		t.Fatalf("deleting a GM: %d %v", c, j)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id = $1`, gm).Scan(&n)
	if n != 1 {
		t.Fatal("the refused delete removed the GM's account")
	}
	c, j = pgCall(t, "POST", "/api/mod/delete-user", mod, map[string]interface{}{"user_id": pl})
	if c != 200 {
		t.Fatalf("deleting a player: %d %v", c, j)
	}
	var deleted bool
	var lobby sql.NullInt64
	db.QueryRow(`SELECT deleted_at IS NOT NULL, lobby_id FROM characters WHERE id = $1`, char).Scan(&deleted, &lobby)
	db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id = $1`, pl).Scan(&n)
	if !deleted || lobby.Valid || n != 0 {
		t.Errorf("after deleting the player: character deleted %v, still in campaign %v, account rows %d", deleted, lobby, n)
	}
	if c, _ := pgCall(t, "GET", "/api/my-turn", plTok, nil); c != 401 {
		t.Errorf("the deleted account's token still signs in: %d", c)
	}
	if c, _ := pgCall(t, "POST", "/api/mod/delete-user", mod, map[string]interface{}{"user_id": pl}); c != 404 {
		t.Errorf("deleting a missing account answered %d", c)
	}
}

// pgFinish makes a pgRogue character's required choices (equipment, Expertise, Thieves' Cant), so its player may say it is ready
// (a character with required choices open isn't: feedback log 49).
func pgFinish(t *testing.T, tok string, id int) {
	t.Helper()
	for _, step := range []struct {
		path string
		body map[string]interface{}
	}{
		{"/api/characters/auto-equip", map[string]interface{}{"character_id": id, "answer": "no"}},
		{fmt.Sprintf("/api/characters/%d/expertise", id), map[string]interface{}{"skills": []string{"stealth", "perception"}}},
		{fmt.Sprintf("/api/characters/%d/class-options", id), map[string]interface{}{"class_languages": []string{"Halfling"}}},
	} {
		if c, j := pgCall(t, "POST", step.path, tok, step.body); c != 200 {
			t.Fatalf("finishing character %d: %s: %d %v", id, step.path, c, j)
		}
	}
	if open := characterToFinish(id); len(open) > 0 {
		t.Fatalf("character %d still has open choices: %v", id, open)
	}
}
