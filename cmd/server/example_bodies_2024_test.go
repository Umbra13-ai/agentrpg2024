package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Every request that reads a body has a copyable example in the reference, sending only fields the request takes, and the commands
// page shows it (feedback log 11: GM command bodies weren't shown; tools/build_swagger.py makes each example from the request type).
func TestEveryRequestBodyHasAnExample(t *testing.T) {
	raw, err := os.ReadFile("../../docs/swagger/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			RequestBody *struct {
				Content map[string]struct {
					Schema struct {
						Properties map[string]interface{} `json:"properties"`
					} `json:"schema"`
					Example map[string]interface{} `json:"example"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	gm := 0
	for path, ms := range spec.Paths {
		for m, op := range ms {
			if op.RequestBody == nil {
				continue
			}
			c, ok := op.RequestBody.Content["application/json"]
			if !ok || len(c.Schema.Properties) == 0 {
				continue
			}
			if len(c.Example) == 0 {
				t.Errorf("%s %s: no example body", strings.ToUpper(m), path)
			}
			for k := range c.Example {
				if _, ok := c.Schema.Properties[k]; !ok {
					t.Errorf("%s %s: the example sends %q, which the request doesn't take", strings.ToUpper(m), path, k)
				}
			}
			if strings.HasPrefix(path, "/api/gm/") {
				gm++
			}
		}
	}
	if gm < 50 {
		t.Errorf("only %d GM commands with a body in the reference", gm)
	}
	w := httptest.NewRecorder()
	handleCommandsPage(w, httptest.NewRequest("GET", "/commands", nil))
	if !strings.Contains(w.Body.String(), "Example body") || !strings.Contains(w.Body.String(), "/api/gm/give-item") {
		t.Errorf("the commands page doesn't show the example bodies")
	}
}

// /skill.md gives the guide as Markdown to a reader that doesn't ask for a web page (an agent's fetch tool), the page to a browser
// (feedback log 20).
func TestSkillMdForReaders(t *testing.T) {
	for accept, want := range map[string]string{"": "text/markdown", "*/*": "text/markdown", "text/markdown": "text/markdown",
		"text/html,application/xhtml+xml,*/*;q=0.8": "text/html"} {
		r := httptest.NewRequest("GET", "/skill.md", nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		handleSkillPage(w, r)
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, want) || w.Code != 200 {
			t.Errorf("Accept %q: %d %s, want %s", accept, w.Code, ct, want)
		}
	}
}

// Every command in the reference is on /commands (Matt, Nov 16: all commands documented; the heartbeat, admin and moderator
// commands were left off because their tags were in no group).
func TestCommandsPageListsEveryCommand(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(swaggerJSON, &spec); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleCommandsPage(w, httptest.NewRequest("GET", "/commands", nil))
	page := w.Body.String()
	n := 0
	for path, ms := range spec.Paths {
		for m := range ms {
			n++
			if !strings.Contains(page, "<code>"+strings.ToUpper(m)+" "+e(path)+"</code>") {
				t.Errorf("%s %s is not on /commands", strings.ToUpper(m), path)
			}
		}
	}
	if n < 300 {
		t.Errorf("only %d commands in the reference", n)
	}
}

// Every example body in the reference is a body its request takes: sent to the server (path ids 1, a fresh account), none is refused
// for its shape (unknown_field, wrong_type, invalid_json, body_required, id_mismatch), and the character creation example creates a
// character (feedback log 43: it was refused with skill_choices_required).
func TestPostgresEveryExampleBodyIsTaken(t *testing.T) {
	testPostgres(t)
	raw, err := os.ReadFile("../../docs/swagger/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			RequestBody *struct {
				Content map[string]struct {
					Example map[string]interface{} `json:"example"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	shape := map[string]bool{"unknown_field": true, "wrong_type": true, "invalid_json": true, "body_required": true, "id_mismatch": true}
	_, tok := pgAgent(t, "pg examples")
	ids := regexp.MustCompile(`\{[a-z_]+\}`)
	var keys []string
	for path, ms := range spec.Paths {
		for m := range ms {
			keys = append(keys, m+" "+path)
		}
	}
	sort.Strings(keys)
	tried := 0
	for _, k := range keys {
		m, path, _ := strings.Cut(k, " ")
		op := spec.Paths[path][m]
		if op.RequestBody == nil || op.RequestBody.Content["application/json"].Example == nil || strings.HasPrefix(path, "/api/admin/") {
			continue
		}
		ex := op.RequestBody.Content["application/json"].Example
		if path == "/api/characters" && m == "post" { // a name of its own: the test database keeps earlier runs' characters
			copied := map[string]interface{}{}
			for k, v := range ex {
				copied[k] = v
			}
			copied["name"] = fmt.Sprintf("Example %d", time.Now().UnixNano()%1e9)
			ex = copied
		}
		target := ids.ReplaceAllString(path, "1")
		c, j := pgCall(t, strings.ToUpper(m), target, tok, ex)
		if c == 401 { // an example that ended the account (deleting it): sign up again and send it once more
			_, tok = pgAgent(t, "pg examples")
			c, j = pgCall(t, strings.ToUpper(m), target, tok, ex)
		}
		tried++
		if e, _ := j["error"].(string); shape[e] {
			t.Errorf("%s %s: the example is refused for its shape: %d %v", strings.ToUpper(m), path, c, j)
		}
		if path == "/api/characters" && m == "post" && c != 200 {
			t.Errorf("the character creation example creates nothing: %d %v", c, j)
		}
	}
	if tried < 100 {
		t.Errorf("only %d examples sent", tried)
	}
}
