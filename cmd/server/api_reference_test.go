package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/docs"
)

// The API reference (docs/swagger/swagger.json, built by tools/build_swagger.py from the handlers' godoc) is a contract an AI
// client reads on its own. It must be valid where clients look (a request body under content, every path parameter declared, no
// body on a read), carry this server's version and address, list every route the router registers and every route the guide
// (skill.md) names, and the commands page must show its paths as they are. After changing a handler's godoc or a route, run
// python3 tools/build_swagger.py.
func TestAPIReference(t *testing.T) {
	var spec struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Components struct {
			SecuritySchemes map[string]interface{} `json:"securitySchemes"`
		} `json:"components"`
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
			RequestBody map[string]interface{}   `json:"requestBody"`
			Security    []map[string]interface{} `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(swaggerJSON, &spec); err != nil {
		t.Fatalf("the reference isn't JSON: %v", err)
	}
	if spec.Info.Version != version {
		t.Errorf("the reference says version %q, the server is %q: run python3 tools/build_swagger.py", spec.Info.Version, version)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != "/" {
		t.Errorf("the reference's server must be the one serving it (\"/\"), not %+v", spec.Servers)
	}
	pathParam := regexp.MustCompile(`\{(\w+)\}`)
	for p, ops := range spec.Paths {
		for m, op := range ops {
			declared := map[string]bool{}
			for _, x := range op.Parameters {
				if x.In == "path" {
					declared[x.Name] = true
				}
			}
			for _, name := range pathParam.FindAllStringSubmatch(p, -1) {
				if !declared[name[1]] {
					t.Errorf("%s %s: path parameter %s isn't declared", strings.ToUpper(m), p, name[1])
				}
			}
			if op.RequestBody != nil {
				if _, ok := op.RequestBody["content"]; !ok {
					t.Errorf("%s %s: the request body has no content (application/json schema)", strings.ToUpper(m), p)
				}
				if req, ok := op.RequestBody["required"]; ok {
					if _, isBool := req.(bool); !isBool {
						t.Errorf("%s %s: requestBody.required must be true or false", strings.ToUpper(m), p)
					}
				}
				if m == "get" {
					t.Errorf("GET %s: a read takes no body", p)
				}
			}
			for _, sec := range op.Security {
				for name := range sec {
					if _, ok := spec.Components.SecuritySchemes[name]; !ok {
						t.Errorf("%s %s: security scheme %q isn't defined", strings.ToUpper(m), p, name)
					}
				}
			}
		}
	}

	// every route the router registers is listed (a route under a prefix is listed through its handler's godoc)
	notListed := map[string]string{"/api/characters/multiclass": "multiclassing isn't supported: the route only says so and is never documented"}
	main, _ := os.ReadFile("main.go")
	for _, m := range regexp.MustCompile(`HandleFunc\("(/api/[^"]*[^/"])"`).FindAllStringSubmatch(string(main), -1) {
		if _, ok := spec.Paths[m[1]]; !ok && notListed[m[1]] == "" {
			t.Errorf("the router serves %s but the reference doesn't list it: give its handler a godoc block (@Router)", m[1])
		}
	}

	// every request the guide names is in the reference with that method
	type route struct {
		rx      *regexp.Regexp
		methods map[string]bool
	}
	var routes []route
	for p, ops := range spec.Paths {
		rx := regexp.MustCompile("^" + regexp.MustCompile(`\\\{\w+\\\}`).ReplaceAllString(regexp.QuoteMeta(p), `[^/]+`) + "$")
		ms := map[string]bool{}
		for m := range ops {
			ms[strings.ToUpper(m)] = true
		}
		routes = append(routes, route{rx, ms})
	}
	named := regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE) (/api/[A-Za-z0-9_{}/.:<>-]+)`)
	mentions := named.FindAllStringSubmatch(docs.Skill, -1)
	if len(mentions) < 100 {
		t.Fatalf("found only %d requests named in skill.md: the pattern no longer reads the guide", len(mentions))
	}
	for _, m := range mentions {
		path := strings.TrimRight(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[2], "x"), ".:,")
		found := false
		for _, r := range routes {
			if r.rx.MatchString(path) && r.methods[m[1]] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("skill.md names %s %s, which the reference doesn't list", m[1], path)
		}
	}

	// the commands page shows each path once, as the reference has it
	w := httptest.NewRecorder()
	handleCommandsPage(w, httptest.NewRequest("GET", "/commands", nil))
	if body := w.Body.String(); strings.Contains(body, "/api/api/") || !strings.Contains(body, "/api/pending-turn") {
		t.Errorf("the commands page doesn't show the reference's paths as they are (/api/api/: %d)", strings.Count(body, "/api/api/"))
	}
}

// A godoc block ("// handleX godoc") sits directly above the function it documents: a block left above another function (or
// split by one) gave its routes the wrong text, or glued half of it onto the next route.
func TestGodocBlocksSitAboveTheirHandlers(t *testing.T) {
	header := regexp.MustCompile(`^//\s*(\w+) godoc\s*$`)
	fn := regexp.MustCompile(`^func (?:\([^)]*\) )?(\w+)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			m := header.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], "//") {
				if header.MatchString(lines[j]) {
					break
				}
				j++
			}
			got := ""
			if j < len(lines) {
				if fm := fn.FindStringSubmatch(lines[j]); fm != nil {
					got = fm[1]
				}
			}
			if got != m[1] {
				t.Errorf("%s:%d: the godoc block of %s sits above %q", f, i+1, m[1], got)
			}
		}
	}
}
