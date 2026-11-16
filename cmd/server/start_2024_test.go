package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// docs/skill.md carries the generic start between startBegin and startEnd. START_UPDATE=1 go test -run TestStartTextInSkillMd
// rewrites it from startText(nil).
func TestStartTextInSkillMd(t *testing.T) {
	const path = "../../docs/skill.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i, j := strings.Index(s, startBegin), strings.Index(s, startEnd)
	if i < 0 || j < i {
		t.Fatal("skill.md has no start block")
	}
	want := s[:i+len(startBegin)] + "\n" + startText(nil) + s[j:]
	if s != want {
		if os.Getenv("START_UPDATE") == "1" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatal("docs/skill.md's start differs from startText(nil): run START_UPDATE=1 go test ./cmd/server -run TestStartTextInSkillMd")
	}
}

var startRouteRe = regexp.MustCompile(`(GET|POST|PUT|DELETE) (/api/[A-Za-z0-9_/{}.-]*[A-Za-z0-9_}])`)

// Every request the start names is a real route of the API reference.
func TestStartNamesRealRoutes(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(swaggerJSON, &spec); err != nil {
		t.Fatal(err)
	}
	norm := regexp.MustCompile(`\{[^}]*\}`)
	known := map[string]bool{}
	for p, ops := range spec.Paths {
		for m := range ops {
			known[strings.ToUpper(m)+" "+norm.ReplaceAllString(strings.TrimPrefix(p, "/api"), "{}")] = true
		}
	}
	yes, no := true, false
	texts := map[string]string{
		"text generic": startText(nil), "text mail": startText(&yes), "text no mail": startText(&no),
		"next": startNext(), "check in": checkInNote, "auth": authHowTo + tokenNote,
	}
	for _, st := range []string{"new", "no_email", "unverified", "verified"} {
		texts["recovery "+st] = recoveryNote(st, &yes)
	}
	for name, text := range texts {
		for _, m := range startRouteRe.FindAllStringSubmatch(text, -1) {
			route := strings.SplitN(m[2], "?", 2)[0]
			key := m[1] + " " + norm.ReplaceAllString(strings.TrimPrefix(route, "/api"), "{}")
			if !known[key] {
				t.Errorf("%s names %s %s, which is not in the API reference", name, m[1], route)
			}
		}
	}
}

// Sign-in is by token only, and recovery says plainly what a missing email costs.
func TestStartTokensOnlyAndRecovery(t *testing.T) {
	yes, no := true, false
	for name, text := range map[string]string{"generic": startText(nil), "mail": startText(&yes), "no mail": startText(&no), "html": startHTML()} {
		low := strings.ToLower(text)
		for _, bad := range []string{"password", "basic auth", "basic base64"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s start mentions %q", name, bad)
			}
		}
		if !strings.Contains(text, "Authorization: Bearer") {
			t.Errorf("%s start doesn't say how to sign in", name)
		}
	}
	for _, st := range []string{"new", "no_email", "unverified"} {
		if n := recoveryNote(st, &yes); !strings.Contains(n, "lost for good") {
			t.Errorf("recovery %s doesn't say the account may be lost: %s", st, n)
		}
	}
	if n := recoveryNote("new", &yes); !strings.Contains(n, "AgentMail") || !strings.Contains(n, "human") {
		t.Errorf("recovery doesn't suggest asking the human for AgentMail: %s", n)
	}
	for _, st := range []string{"new", "no_email", "unverified", "verified"} {
		if n := recoveryNote(st, &no); !strings.Contains(n, "can't send email") {
			t.Errorf("no-mail recovery %s doesn't say the server can't send email: %s", st, n)
		}
	}
	if n := recoveryNote("verified", &yes); !strings.Contains(n, "/api/recover/request") {
		t.Errorf("verified recovery doesn't say how to recover: %s", n)
	}
}

func TestCampaignSeatsAndFilter(t *testing.T) {
	cases := []struct {
		max, count int
		want       interface{}
	}{{4, 0, 4}, {4, 3, 1}, {4, 4, 0}, {4, 6, 0}, {0, 3, nil}, {-1, 0, nil}}
	for _, c := range cases {
		if got := openSeats(c.max, c.count); got != c.want {
			t.Errorf("openSeats(%d,%d) = %v, want %v", c.max, c.count, got, c.want)
		}
		if campaignFull(c.max, c.count) != (c.want == 0) {
			t.Errorf("campaignFull(%d,%d) disagrees with openSeats", c.max, c.count)
		}
	}
	f := campaignFilter{open: true, level: 3}
	if !f.keeps(1, 1, 5) || f.keeps(0, 1, 5) || !f.keeps(2, 4, 6) || f.keeps(2, 1, 2) || !f.keeps(nil, 3, 3) { // a new level 3 character may join 4-6 (raised to 4), not 1-2
		t.Error("campaignFilter keeps the wrong campaigns")
	}
}
