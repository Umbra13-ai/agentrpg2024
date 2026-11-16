package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// "Which character is this agent playing" is asked only through actingCharacter (HANDOFF item 22: the same query was written in
// six places, and the round gate read character_id from the body only, while the action it gated also read the query string).
func TestNoOtherActingCharacterQuery(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "acting_character_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "l.status = 'active' AND ($2 = 0 OR c.id = $2)") {
				t.Errorf("%s:%d picks the agent's character itself: use actingCharacter", f, n+1)
			}
		}
	}
}

// actingCharacter: the named character or campaign (body first, then the query string), else the newest active campaign;
// another agent's character, or one in a campaign that isn't active, is no character. Every route that asks reads the same answer:
// GET /api/my-turn is checked through the server, with and without ?character_id.
func TestPostgresActingCharacter(t *testing.T) {
	testPostgres(t)
	agent, tok := pgAgent(t, "pg two seats")
	other, _ := pgAgent(t, "pg stranger")
	stamp := time.Now().UnixNano() % 1e9
	char := func(name string) int {
		c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("%s %d", name, stamp)))
		if c != 200 {
			t.Fatalf("create %s: %d %v", name, c, j)
		}
		return int(j["character_id"].(float64))
	}
	lobby := func(status string) int {
		var id int
		if err := db.QueryRow(`INSERT INTO lobbies (name, dm_id, status) VALUES ($1, $2, $3) RETURNING id`,
			fmt.Sprintf("Acting %s %d", status, stamp), other, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	older, newer, closed := lobby("active"), lobby("active"), lobby("completed")
	a, b, c := char("Pg Elder"), char("Pg Younger"), char("Pg Retired")
	pgExec(t, `UPDATE characters SET lobby_id = $1 WHERE id = $2`, older, a)
	pgExec(t, `UPDATE characters SET lobby_id = $1 WHERE id = $2`, newer, b)
	pgExec(t, `UPDATE characters SET lobby_id = $1 WHERE id = $2`, closed, c)

	cases := []struct {
		name               string
		query              string
		bodyChar, bodyCamp int
		want, wantLobby    int
	}{
		{"nothing named: the newest active campaign", "", 0, 0, b, newer},
		{"character in the body", "", a, 0, a, older},
		{"campaign in the body", "", 0, older, a, older},
		{"character in the query string", fmt.Sprintf("?character_id=%d", a), 0, 0, a, older},
		{"campaign in the query string", fmt.Sprintf("?campaign_id=%d", older), 0, 0, a, older},
		{"the body wins over the query string", fmt.Sprintf("?character_id=%d", a), b, 0, b, newer},
		{"a character in a finished campaign", "", c, 0, 0, 0},
		{"a character and a campaign that don't match", "", a, newer, 0, 0},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/api/my-turn"+tc.query, nil)
		id, lob, err := actingCharacter(db, r, agent, tc.bodyChar, tc.bodyCamp)
		if tc.want == 0 {
			if !errors.Is(err, sql.ErrNoRows) {
				t.Errorf("%s: want no character, got %d (%v)", tc.name, id, err)
			}
			continue
		}
		if err != nil || id != tc.want || lob != tc.wantLobby {
			t.Errorf("%s: got character %d in %d (%v), want %d in %d", tc.name, id, lob, err, tc.want, tc.wantLobby)
		}
	}
	strangerTok := func() string { _, s := pgAgent(t, "pg nobody"); return s }()
	if id, _, err := actingCharacter(db, httptest.NewRequest("GET", "/api/my-turn", nil), other, a, 0); err == nil {
		t.Errorf("another agent's character is never the acting character: got %d", id)
	}

	nameIn := func(j map[string]interface{}) string {
		ch, _ := j["character"].(map[string]interface{})
		s, _ := ch["name"].(string)
		return s
	}
	if code, j := pgCall(t, "GET", "/api/my-turn", tok, nil); code != 200 || !strings.HasPrefix(nameIn(j), "Pg Younger") {
		t.Errorf("my-turn without a name: %d %q", code, nameIn(j))
	}
	if code, j := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", a), tok, nil); code != 200 || !strings.HasPrefix(nameIn(j), "Pg Elder") {
		t.Errorf("my-turn?character_id: %d %q", code, nameIn(j))
	}
	code, j := pgCall(t, "GET", "/api/my-turn", strangerTok, nil)
	if code != 200 || j["error"] != "no_active_game" || j["is_my_turn"] != false || j["how_to_join"] == nil {
		t.Errorf("my-turn without a character: %d %v", code, j)
	}
	code, j = pgCall(t, "POST", "/api/observe", strangerTok, map[string]interface{}{"content": "a crow", "type": "world"})
	if code != 409 || j["error"] != "no_active_game" || j["how_to_join"] == nil {
		t.Errorf("observe without a character: %d %v", code, j)
	}
}
