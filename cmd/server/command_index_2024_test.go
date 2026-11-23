package main

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Every request in the index is one the API reference has, with the method it names and a body whose fields the request takes.
func TestCommandIndexNamesRealRequests(t *testing.T) {
	check := requestChecker(t)
	seen := map[string]bool{}
	for _, c := range commandTasks {
		check("the command index: "+c.Task, c.Method, c.Path, c.Body)
		if c.Task == "" || c.Who == "" || c.Not == "" || c.Group == "" {
			t.Errorf("an index row is missing words: %+v", c)
		}
		if c.Filter != "player" && c.Filter != "gm" && c.Filter != "campaign" {
			t.Errorf("%s: filter %q is not one of /commands' filters", c.Task, c.Filter)
		}
		if seen[c.Task] {
			t.Errorf("the task %q is listed twice", c.Task)
		}
		seen[c.Task] = true
	}
}

// skill.md's command index is the table's (COMMANDS_UPDATE=1 rewrites it), and /commands shows it first.
func TestCommandIndexInSkillMd(t *testing.T) {
	const path = "../../docs/skill.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i, j := strings.Index(s, commandsBegin), strings.Index(s, commandsEnd)
	if i < 0 || j < i {
		t.Fatal("skill.md has no command index markers")
	}
	want := s[:i+len(commandsBegin)] + "\n\n" + commandIndexMarkdown() + "\n" + s[j:]
	if s != want {
		if os.Getenv("COMMANDS_UPDATE") == "1" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatal("docs/skill.md's command index differs from the table: run COMMANDS_UPDATE=1 go test ./cmd/server -run TestCommandIndexInSkillMd")
	}
	rec := httptest.NewRecorder()
	handleCommandsPage(rec, httptest.NewRequest("GET", "/commands", nil))
	page := rec.Body.String()
	if !strings.Contains(page, "What are you trying to do?") || strings.Index(page, "What are you trying to do?") > strings.Index(page, "Player actions: POST /api/action") {
		t.Error("/commands doesn't lead with What are you trying to do?")
	}
	for _, c := range commandTasks {
		if !strings.Contains(page, c.Path) {
			t.Errorf("/commands is missing the task %q", c.Task)
		}
	}
}
