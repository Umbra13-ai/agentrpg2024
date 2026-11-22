package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Playtest I-CALLA-26: since_your_last_action starts after the character's own last action, the one its player's request wrote
// (or a request the server makes for that player: serveAs). A record the GM writes under the character's name (here the GM's
// take-item, which logs the drop as the character's) does not reset it.
func TestPostgresSinceYourLastActionCountsOnlyThePlayersOwn(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, _ := pgFightWith(t, "own action", 1)
	tok, char := toks[0], chars[0]
	if c, j := pgCall(t, "POST", "/api/action", tok, map[string]interface{}{"action": "dodge", "description": "Ducks behind the cart wheel"}); c != 200 {
		t.Fatalf("dodge: %d %v", c, j)
	}
	pgCall(t, "POST", "/api/gm/give-item", gm, map[string]interface{}{"campaign_id": camp, "character_id": char, "item_name": "Blanket"})
	if c, j := pgCall(t, "POST", "/api/gm/take-item", gm, map[string]interface{}{"character_id": char, "item": "Blanket", "where": "under the wagon"}); c != 200 {
		t.Fatalf("take-item: %d %v", c, j)
	}
	var own, gms int
	db.QueryRow(`SELECT COUNT(*) FILTER (WHERE by_player), COUNT(*) FILTER (WHERE NOT by_player) FROM actions WHERE character_id = $1`, char).Scan(&own, &gms)
	if own == 0 || gms == 0 {
		t.Errorf("by_player: %d of the player's records, %d of the GM's", own, gms)
	}
	_, mt := pgCall(t, "GET", "/api/my-turn", tok, nil)
	since, _ := json.Marshal(mt["since_your_last_action"])
	if !strings.Contains(string(since), "Blanket") || strings.Contains(string(since), "Ducks behind") {
		t.Errorf("since_your_last_action after the GM's record under the character's name: %s", since)
	}

	// a request the server makes for the player (a submission the GM resolves, a pending turn's step, a readied action) is the
	// player's; the GM's own record written in the same request after it is not
	var player, dm int
	db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, char).Scan(&player)
	db.QueryRow(`SELECT dm_id FROM lobbies WHERE id = $1`, camp).Scan(&dm)
	outer := httptest.NewRequest(http.MethodPost, "/api/gm/test", nil)
	guardDB(httptest.NewRecorder(), withAuthMemo(outer), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveAs(player, httptest.NewRequest(http.MethodPost, "/api/action", nil), func(http.ResponseWriter, *http.Request) {
			writeAction(db, camp, char, "declare", "Own Step Marker", "done")
		})
		serveAs(dm, httptest.NewRequest(http.MethodPost, "/api/gm/x", nil), func(http.ResponseWriter, *http.Request) {
			writeAction(db, camp, char, "ruling", "GM Ruling Marker", "done")
		})
	}))
	for desc, want := range map[string]bool{"Own Step Marker": true, "GM Ruling Marker": false} {
		var got bool
		if err := db.QueryRow(`SELECT by_player FROM actions WHERE character_id = $1 AND description = $2`, char, desc).Scan(&got); err != nil || got != want {
			t.Errorf("%s: by_player %v (%v), want %v", desc, got, err, want)
		}
	}
	_, mt = pgCall(t, "GET", "/api/my-turn", tok, nil)
	since, _ = json.Marshal(mt["since_your_last_action"])
	if !strings.Contains(string(since), "GM Ruling Marker") || strings.Contains(string(since), "Own Step Marker") || strings.Contains(string(since), "Blanket") {
		t.Errorf("since_your_last_action after a step the server ran for the player: %s", since)
	}
}
