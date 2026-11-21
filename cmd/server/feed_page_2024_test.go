package main

import (
	"fmt"
	"net/url"
	"testing"
)

// Playtest I-GM-25: the campaign feed always gave the oldest 100 records and ignored every paging word, so a GM couldn't read the
// last rounds of a long campaign. logPage (action_log_2024.go) is the one paging rule: the newest page by default, before to page
// back, since to read forward, limit; every page oldest first, more saying whether more are left. The feed's actions and messages
// and the party chat read through it.
func TestPostgresFeedPagesFromTheNewest(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg feed page gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Feed Page", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	// 130 records and 130 messages, a second apart, in order
	pgExec(t, `INSERT INTO actions (lobby_id, action_type, description, result, created_at)
		SELECT $1, 'narration', 'record ' || g, '', NOW() - INTERVAL '1 day' + g * INTERVAL '1 second' FROM generate_series(1, 130) g`, lobby)
	pgExec(t, `INSERT INTO campaign_messages (lobby_id, agent_name, message, created_at)
		SELECT $1, 'tester', 'message ' || g, NOW() - INTERVAL '1 day' + g * INTERVAL '1 second' FROM generate_series(1, 130) g`, lobby)

	descs := func(list interface{}, key string) []string {
		var out []string
		for _, x := range list.([]interface{}) {
			out = append(out, fmt.Sprint(x.(map[string]interface{})[key]))
		}
		return out
	}
	first := func(list interface{}) string {
		return fmt.Sprint(list.([]interface{})[0].(map[string]interface{})["created_at"])
	}
	last := func(list interface{}) string {
		l := list.([]interface{})
		return fmt.Sprint(l[len(l)-1].(map[string]interface{})["created_at"])
	}
	feed := func(q string) map[string]interface{} {
		t.Helper()
		c, j := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/feed%s", lobby, q), gm, nil)
		if c != 200 {
			t.Fatalf("feed%s: %d %v", q, c, j)
		}
		return j
	}
	chat := func(q string) map[string]interface{} {
		t.Helper()
		c, j := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/messages?campaign_id=%d%s", lobby, q), gm, nil)
		if c != 200 {
			t.Fatalf("chat%s: %d %v", q, c, j)
		}
		return j
	}

	// the newest page, oldest first
	j = feed("")
	a, m := descs(j["actions"], "description"), descs(j["messages"], "message")
	if len(a) != 100 || a[0] != "record 31" || a[99] != "record 130" || j["more_actions"] != true {
		t.Fatalf("newest page: %d records, %v ... %v, more %v", len(a), a[0], a[len(a)-1], j["more_actions"])
	}
	if len(m) != 100 || m[0] != "message 31" || m[99] != "message 130" || j["more_messages"] != true {
		t.Fatalf("newest messages: %d, %v ... %v, more %v", len(m), m[0], m[len(m)-1], j["more_messages"])
	}
	// before pages back
	back := feed("?before=" + url.QueryEscape(first(j["actions"])))
	if a := descs(back["actions"], "description"); len(a) != 30 || a[0] != "record 1" || a[29] != "record 30" || back["more_actions"] != false {
		t.Fatalf("page back: %d records %v, more %v", len(a), a, back["more_actions"])
	}
	// since reads forward from a record
	fwd := feed("?limit=10&since=" + url.QueryEscape(last(back["actions"])))
	if a := descs(fwd["actions"], "description"); len(a) != 10 || a[0] != "record 31" || a[9] != "record 40" || fwd["more_actions"] != true {
		t.Fatalf("forward page: %v, more %v", a, fwd["more_actions"])
	}
	// the end of the log: nothing newer
	if end := feed("?since=" + url.QueryEscape(last(j["actions"]))); len(end["actions"].([]interface{})) != 0 || end["more_actions"] != false {
		t.Fatalf("after the newest record: %v", end)
	}
	// limit and before together
	if p := feed("?limit=5&before=" + url.QueryEscape(first(j["actions"]))); fmt.Sprint(descs(p["actions"], "description")) != "[record 26 record 27 record 28 record 29 record 30]" {
		t.Fatalf("limit with before: %v", descs(p["actions"], "description"))
	}
	// the party chat pages the same way
	c := chat("")
	if m := descs(c["messages"], "message"); len(m) != 100 || m[0] != "message 31" || c["more"] != true {
		t.Fatalf("party chat newest page: %d %v more %v", len(m), m[:1], c["more"])
	}
	if m := descs(chat("&before=" + url.QueryEscape(first(c["messages"])))["messages"], "message"); len(m) != 30 || m[0] != "message 1" {
		t.Fatalf("party chat page back: %v", m)
	}
	// a bad paging word is refused, with the words that work
	for q, code := range map[string]string{"?since=yesterday": "invalid_since", "?before=soon": "invalid_before", "?limit=500": "invalid_limit"} {
		if c, j := pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d/feed%s", lobby, q), gm, nil); c != 400 || j["error"] != code {
			t.Errorf("feed%s: %d %v", q, c, j)
		}
	}
}
