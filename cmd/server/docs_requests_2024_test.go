package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// The guides (docs/*.md: skill.md, the heartbeat templates, the GM guide, the pages under /how-it-works) name requests in two
// ways: "POST /api/gm/milestone {"campaign_id": 1}" in the text, and curl commands in examples. Every request they name must be a
// route of the API reference with that method, and every field of a body they show must be a field that request takes (the
// analysis of TestAPIShapes: the reader's own fields). A guide that showed a field the server refuses (unknown_field) taught
// players a request that fails.

var (
	docMention = regexp.MustCompile("\\b(GET|POST|PUT|PATCH|DELETE) `?(?:https?://YOUR-SERVER)?(/api/[A-Za-z0-9_{}/.:<>-]+)")
	docCurlURL = regexp.MustCompile(`["']?https?://YOUR-SERVER(/api/[A-Za-z0-9_{}/.:<>-]*)`)
	docCurlX   = regexp.MustCompile(`-X\s+([A-Z]+)`)
	docPathArg = regexp.MustCompile(`\{[^}]*\}|<[^>]*>|\b[0-9]+\b|YOUR-[A-Z]+`)
)

// docRequest: one request a guide names, with the body it shows (raw text of a JSON object; "" when none).
type docRequest struct {
	doc, method, path, body string
	line                    int
}

// docRequests reads the requests a guide names.
func docRequests(doc, text string) []docRequest {
	var out []docRequest
	lineOf := func(off int) int { return strings.Count(text[:off], "\n") + 1 }
	for _, m := range docMention.FindAllStringSubmatchIndex(text, -1) {
		method, path := text[m[2]:m[3]], text[m[4]:m[5]]
		body := ""
		rest := text[m[1]:]
		trimmed := strings.TrimLeft(rest, " `")
		if strings.HasPrefix(trimmed, "{") && len(rest)-len(trimmed) <= 2 {
			body = balancedObject(trimmed)
		}
		out = append(out, docRequest{doc, method, path, body, lineOf(m[0])})
	}
	// curl commands: a command runs over lines that end with a backslash
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], "curl ") {
			continue
		}
		start := i
		cmd := lines[i]
		for strings.HasSuffix(strings.TrimSpace(lines[i]), "\\") && i+1 < len(lines) {
			i++
			cmd += "\n" + lines[i]
		}
		// a -d '...' body may run over more lines until its closing quote
		if k := strings.Index(cmd, "-d '"); k >= 0 && !strings.Contains(strings.ReplaceAll(cmd[k+4:], `'\''`, ""), "'") {
			for i+1 < len(lines) {
				i++
				cmd += "\n" + lines[i]
				if strings.Contains(strings.ReplaceAll(lines[i], `'\''`, ""), "'") {
					break
				}
			}
		}
		u := docCurlURL.FindStringSubmatch(cmd)
		if u == nil {
			continue
		}
		body := ""
		if k := strings.Index(cmd, "-d '"); k >= 0 {
			rest := strings.ReplaceAll(cmd[k+4:], `'\''`, "’") // a quote inside the shell's quotes
			if e := strings.Index(rest, "'"); e >= 0 {
				body = rest[:e]
			}
			if b := strings.TrimSpace(body); strings.HasPrefix(b, "{") {
				body = balancedObject(b)
			}
		}
		method := "GET"
		if x := docCurlX.FindStringSubmatch(cmd); x != nil {
			method = x[1]
		} else if strings.Contains(cmd, " -d ") {
			method = "POST"
		}
		out = append(out, docRequest{doc, method, u[1], body, start + 1})
	}
	return out
}

// balancedObject: the JSON object at the start of s, up to its closing brace (strings respected).
func balancedObject(s string) string {
	depth, inStr, esc := 0, false, false
	for i, c := range s {
		switch {
		case esc:
			esc = false
		case c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return ""
}

// topLevelKeys: the keys of a JSON object's top level, tolerating placeholders such as N or ... in its values.
func topLevelKeys(obj string) []string {
	var keys []string
	depth, inStr, esc, start := 0, false, false, 0
	for i, c := range obj {
		switch {
		case esc:
			esc = false
		case c == '\\':
			esc = true
		case c == '"':
			if !inStr {
				start = i
			} else if depth == 1 {
				rest := strings.TrimLeft(obj[i+1:], " \n\t")
				if strings.HasPrefix(rest, ":") {
					keys = append(keys, obj[start+1:i])
				}
			}
			inStr = !inStr
		case inStr:
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
		}
	}
	return keys
}

func TestDocsNameRealRequests(t *testing.T) {
	check := requestChecker(t)
	docs, _ := filepath.Glob("../../docs/*.md")
	if len(docs) < 5 {
		t.Fatalf("found %d guides", len(docs))
	}
	n := 0
	for _, doc := range docs {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(doc)
		for _, rq := range docRequests(name, string(b)) {
			n++
			check(fmt.Sprintf("%s:%d", name, rq.line), rq.method, rq.path, rq.body)
		}
	}
	if n < 300 {
		t.Fatalf("found only %d requests in the guides: the patterns no longer read them", n)
	}
}

// requestChecker is the one check of a request a text names (a guide, or the server's own reply words): the route must be in
// the API reference with that method, and every top-level field of a body shown with it must be a field that request takes
// (TestAPIShapes' reading of its reader). where says where the text is, for the error.
func requestChecker(t *testing.T) func(where, method, path, body string) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(swaggerJSON, &spec); err != nil {
		t.Fatal(err)
	}
	type route struct {
		rx     *regexp.Regexp
		path   string
		method string
	}
	var routes []route
	for p, ops := range spec.Paths {
		rx := regexp.MustCompile("^" + regexp.MustCompile(`\\\{\w+\\\}`).ReplaceAllString(regexp.QuoteMeta(p), `[^/]+`) + "$")
		for m := range ops {
			routes = append(routes, route{rx, p, strings.ToUpper(m)})
		}
	}
	sort.Slice(routes, func(i, j int) bool { // a literal segment before a parameter: /campaigns/messages before /campaigns/{id}
		return strings.Count(routes[i].path, "{") < strings.Count(routes[j].path, "{")
	})
	fields := map[string]map[string]bool{} // "POST /api/x" -> the fields its reader takes (game.Key)
	for _, sh := range apiShapes(t) {
		if sh.Request == nil {
			continue
		}
		f := map[string]bool{}
		for _, x := range sh.Request.Fields {
			f[game.Key(x.Name)] = true
		}
		for name := range pathIDs(docPathArg.ReplaceAllString(sh.Path, "1")) { // an id that repeats the path's
			f[game.Key(name)] = true
		}
		fields[strings.ToUpper(sh.Method)+" "+sh.Path] = f
	}
	placeholder := regexp.MustCompile(`<[^>]+>|%[a-z]`) // a guide's <id>, a reply's %d
	return func(where, method, path, body string) {
		t.Helper()
		path = strings.SplitN(path, "?", 2)[0]
		path = strings.TrimRight(path, ".:,)`'")
		path = placeholder.ReplaceAllString(path, "x")
		var hit *route
		for i := range routes {
			if routes[i].method == method && routes[i].rx.MatchString(path) {
				hit = &routes[i]
				break
			}
		}
		if hit == nil {
			t.Errorf("%s names %s %s, which the API reference doesn't have", where, method, path)
			return
		}
		if body == "" || method == "GET" {
			return
		}
		takes, ok := fields[method+" "+hit.path]
		if !ok {
			return
		}
		for _, k := range topLevelKeys(body) {
			if strings.Contains(k, "...") || strings.HasPrefix(k, "<") || strings.Contains(k, "%") {
				continue
			}
			if !takes[game.Key(k)] {
				t.Errorf("%s shows %s %s with field %q, which that request doesn't take", where, method, hit.path, k)
			}
		}
	}
}

// The server's own replies name requests too ("start one with POST /api/campaigns/%d/combat/start"): every request a string
// literal of the server names must be a route of the reference with that method, and a body shown with it must use fields the
// request takes (playtest I-GM-18: GM status told the GM to send POST /api/gm/add-monster, which never existed). A literal that
// is only the start of a path (joined to an id in code) is checked as far as it goes.
func TestServerRepliesNameRealRequests(t *testing.T) {
	check := requestChecker(t)
	mention := regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE) (/api/(?:[A-Za-z0-9_{}/.:%-]|<[a-z_ ]+>)+)`) // not into HTML tags
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(nd ast.Node) bool {
			lit, ok := nd.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range mention.FindAllStringSubmatchIndex(s, -1) {
				method, path := s[m[2]:m[3]], s[m[4]:m[5]]
				if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "=") { // joined to an id in code: not a whole path
					continue
				}
				body := ""
				rest := s[m[1]:]
				if trimmed := strings.TrimLeft(rest, " `"); strings.HasPrefix(trimmed, "{") && len(rest)-len(trimmed) <= 2 {
					body = balancedObject(trimmed)
				}
				n++
				check(fset.Position(lit.Pos()).String(), method, path, body)
			}
			return true
		})
	}
	if n < 200 {
		t.Fatalf("found only %d requests in the server's strings: the pattern no longer reads them", n)
	}
}

// The guides that tell an agent when to check in carry checkInNote (start_2024.go, the one owner of how often to check in)
// between these markers, word for word; START_UPDATE=1 go test ./cmd/server -run TestCheckInNoteInGuides rewrites them.
const (
	checkInBegin = "<!-- check-in: written by the server (checkInNote, cmd/server/start_2024.go); change it there -->"
	checkInEnd   = "<!-- end of check-in -->"
)

func TestCheckInNoteInGuides(t *testing.T) {
	for _, doc := range []string{"PLAYER_HEARTBEAT.md", "GM_HEARTBEAT.md", "PLAYER_EXPERIENCE.md", "GAME_MASTER_EXPERIENCE.md"} {
		path := "../../docs/" + doc
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		i, j := strings.Index(s, checkInBegin), strings.Index(s, checkInEnd)
		if i < 0 || j < i {
			t.Errorf("%s has no check-in block (%s ... %s)", doc, checkInBegin, checkInEnd)
			continue
		}
		want := s[:i+len(checkInBegin)] + "\n" + checkInNote + "\n" + s[j:]
		if s == want {
			continue
		}
		if os.Getenv("START_UPDATE") == "1" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("docs/%s's check-in differs from checkInNote: run START_UPDATE=1 go test ./cmd/server -run TestCheckInNoteInGuides", doc)
	}
}

// The GM guide carries pacingCheck (gm_pacing_2024.go) between its pacing markers, word for word; START_UPDATE=1 rewrites it.
const (
	pacingBegin = "<!-- pacing: written by the server (pacingCheck, cmd/server/gm_pacing_2024.go); change it there -->"
	pacingEnd   = "<!-- end of pacing -->"
)

func TestPacingCheckInGMGuide(t *testing.T) {
	path := "../../docs/gm_guide.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i, j := strings.Index(s, pacingBegin), strings.Index(s, pacingEnd)
	if i < 0 || j < i {
		t.Fatalf("docs/gm_guide.md has no pacing block (%s ... %s)", pacingBegin, pacingEnd)
	}
	want := s[:i+len(pacingBegin)] + "\n" + pacingCheck + "\n" + s[j:]
	if s == want {
		return
	}
	if os.Getenv("START_UPDATE") == "1" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Errorf("docs/gm_guide.md's pacing check differs from pacingCheck: run START_UPDATE=1 go test ./cmd/server -run TestPacingCheckInGMGuide")
}

// A guide that names a table rule's default ("`combat_turn_timeout` (default `4h`") says the default tableRuleDefs has.
func TestDocsTableRuleDefaults(t *testing.T) {
	defaults := map[string]string{}
	for _, d := range tableRuleDefs {
		defaults[d.Key] = d.Default
	}
	rx := regexp.MustCompile("`(\\w+)`\\s*\\(default `([^`]+)`")
	docs, _ := filepath.Glob("../../docs/*.md")
	n := 0
	for _, doc := range docs {
		b, _ := os.ReadFile(doc)
		for _, m := range rx.FindAllStringSubmatch(string(b), -1) {
			want, isRule := defaults[m[1]]
			if !isRule {
				continue
			}
			n++
			if m[2] != want {
				t.Errorf("%s says table rule %s defaults to %q; tableRuleDefs says %q", filepath.Base(doc), m[1], m[2], want)
			}
		}
	}
	if n == 0 {
		t.Fatal("no table rule default named in the guides: the pattern no longer reads them")
	}
}
