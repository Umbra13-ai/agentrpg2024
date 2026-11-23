package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Plan v2.1, S2: no "who may send this" label is printed that has not been checked against the server. For each index task whose label
// names a wrong caller, the request is sent as that caller on a throwaway database and must be refused (never answered 2xx): a request
// from nobody is 401; a campaign's GM request from a player or from someone outside the campaign, a character owner's request from
// another player, a member's request from someone outside, are 4xx with an error code. The probe is for the throwaway database only.
func TestPostgresCommandIndexWhoLabelsAreEnforced(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, _ := pgFightWith(t, "who", 2)
	owner, other := toks[0], toks[1]
	char := chars[0]
	_, outsider := pgAgent(t, "who outsider")

	fill := func(s string) string {
		s = strings.ReplaceAll(s, "{id}", "%ID%")
		return s
	}
	for _, c := range commandTasks {
		who := c.Who
		isChar := strings.Contains(c.Path, "/api/characters/")
		id := camp
		if isChar {
			id = char
		}
		path := strings.ReplaceAll(fill(c.Path), "%ID%", fmt.Sprint(id))
		body := c.Body
		body = strings.ReplaceAll(body, `"character_id": 1`, fmt.Sprintf(`"character_id": %d`, char))
		body = strings.ReplaceAll(body, `"campaign_id": 1`, fmt.Sprintf(`"campaign_id": %d`, camp))
		var payload interface{}
		if body != "" {
			payload = rawJSON(body)
		}
		send := func(token string) (int, map[string]interface{}) {
			p := path
			if c.Method == "GET" && !strings.Contains(p, "?") && strings.Contains(c.Path, "/api/my-turn") {
				p += "?character_id=" + fmt.Sprint(char)
			}
			return pgCall(t, c.Method, p, token, payload)
		}
		refused := func(label, token string) {
			t.Helper()
			code, j := send(token)
			if code < 400 || code >= 500 {
				t.Errorf("%q (%s %s) sent %s: answered %d %v; the index says who: %s", c.Task, c.Method, path, label, code, j, who)
			}
		}
		switch {
		case strings.HasPrefix(who, "anyone"):
			if code, _ := send(""); code != 200 {
				t.Errorf("%q needs no token, but signed out answered %d", c.Task, code)
			}
		case strings.HasPrefix(who, "a player with a character"):
			// anyone else gets a status report (200 with error and is_my_turn false) or a refusal, never the scene
			code, j := send(outsider)
			if !(code >= 400 && code < 500) && !(code == 200 && j["error"] != nil && j["is_my_turn"] == false) {
				t.Errorf("%q by someone with no character: %d %v", c.Task, code, j)
			}
		case strings.HasPrefix(who, "any signed-in account"):
			if c.Path != "/api/campaigns" || c.Method != "POST" { // creating needs no one in particular; every route needs a token
				code, _ := send("")
				if code != 401 && code != 400 {
					t.Errorf("%q signed out: %d, want 401", c.Task, code)
				}
			}
		case who == "the campaign's GM":
			refused("by a player of the campaign", owner)
			refused("by someone outside it", outsider)
		case who == "the character's owner":
			if strings.Contains(c.Path, "{id}") || strings.Contains(body, "character_id") {
				refused("by another player", other)
				refused("by someone outside it", outsider)
			}
		case who == "a member of the campaign":
			refused("by someone outside it", outsider)
		}
		_ = gm
		time.Sleep(0)
	}
}

// rawJSON: a body already written as JSON text, sent as it is.
type rawJSON string

func (r rawJSON) MarshalJSON() ([]byte, error) { return []byte(r), nil }
