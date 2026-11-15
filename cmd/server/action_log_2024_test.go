package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The action log is read through readActions (action_log_2024.go). A query that reads an action's description or result
// anywhere else is a second reader with its own NULL handling; only the two exploration-turn checks in campaign_flow.go,
// which match on those columns rather than show them, read them themselves.
func TestNoOtherActionLogReader(t *testing.T) {
	allowed := map[string]bool{
		"action_log_2024.go:readActions":                       true,
		"campaign_flow.go:hasCharacterActedSinceLastNarration": true,
		"campaign_flow.go:actionReplaySinceLastNarration":      true,
	}
	reader := regexp.MustCompile("(?is)SELECT[^;`\"]{0,400}?\\b(description|result)\\b[^;`\"]{0,400}?\\bFROM actions\\b")
	funcName := regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?(\w+)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		for _, m := range reader.FindAllStringIndex(src, -1) {
			fn := ""
			for _, d := range funcName.FindAllStringSubmatchIndex(src[:m[0]], -1) {
				fn = src[d[2]:d[3]]
			}
			if !allowed[f+":"+fn] {
				line := strings.Count(src[:m[0]], "\n") + 1
				t.Errorf("%s:%d (%s) reads the action log itself: use readActions (action_log_2024.go)", f, line, fn)
			}
		}
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]string{
		"":                          "0001-01-01T00:00:00Z",
		"2026-11-15T16:05:00Z":      "2026-11-15T16:05:00Z",
		"2026-11-15T11:05:00-05:00": "2026-11-15T16:05:00Z",
		"2026-11-15T16:05:00.25Z":   "2026-11-15T16:05:00Z",
		"2026-11-15":                "2026-11-15T00:00:00Z",
	} {
		got, ok := parseClientTime(in)
		if !ok || got.UTC().Format(time.RFC3339) != want {
			t.Errorf("parseClientTime(%q) = %v %v, want %s", in, got, ok, want)
		}
	}
	for _, bad := range []string{"yesterday", "16:05", "2026-13-01T00:00:00Z"} {
		if _, ok := parseClientTime(bad); ok {
			t.Errorf("parseClientTime(%q) accepted a value that isn't a time", bad)
		}
	}
}

// view is the one JSON form of a row: every field present, the result without monster numbers.
func TestActionRecordView(t *testing.T) {
	a := actionRecord{ID: 7, Actor: "GM", Type: actionNarration, Description: "The door creaks.", At: time.Date(2026, 11, 15, 16, 7, 28, 0, time.UTC)}
	v := a.view()
	for _, k := range []string{"id", "character_id", "actor", "type", "description", "result", "created_at"} {
		if _, ok := v[k]; !ok {
			t.Errorf("view has no %s", k)
		}
	}
	if v["created_at"] != "2026-11-15T16:07:28Z" || !a.isNarration() || cursorTime(a.At.Add(250*time.Millisecond)) != "2026-11-15T16:07:28.25Z" {
		t.Errorf("view = %v", v)
	}
}

// writeAction is the only writer of the action log: a hand-written INSERT named columns the table doesn't have (campaign_id,
// action) and its rows were never written.
func TestNoOtherActionLogWriter(t *testing.T) {
	insert := regexp.MustCompile(`(?i)INSERT INTO actions\b`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "action_log_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if insert.MatchString(line) {
				t.Errorf("%s:%d writes the action log itself: use writeAction (action_log_2024.go)", f, n+1)
			}
		}
	}
}
