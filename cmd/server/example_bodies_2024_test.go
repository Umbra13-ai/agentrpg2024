package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
