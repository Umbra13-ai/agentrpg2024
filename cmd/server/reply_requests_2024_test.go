package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The server's own replies name requests too ("Add monsters (POST /api/campaigns/{id}/combat/add ...)", "Set standing orders: POST
// /api/characters/reaction-orders {...}"). Like the guides (TestDocsNameRealRequests), every request a Go string in the server
// names must be a route of the API reference with that method, and every field of a body shown with it must be one that request
// takes. Feedback log 58: GM status told GMs to send POST /api/gm/add-monster, which never existed.
//
// The texts are read from the source (go/ast), not from replies: a string built with + is read as one text with each non-string
// part as "1", and a fmt verb (%d, %s, %v, %q) as "1", so "POST /api/campaigns/%d/combat/add" is checked as
// "POST /api/campaigns/1/combat/add".

var fmtVerb = regexp.MustCompile(`%[-+# 0-9.]*[dsvqx]`)

// serverTexts: every string text in the server's non-test Go files, with where it is.
func serverTexts(t *testing.T) map[string][]string {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	out := map[string][]string{} // text -> positions
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		inConcat := map[ast.Expr]bool{}
		add := func(text string, pos token.Pos) {
			text = fmtVerb.ReplaceAllString(text, "1")
			if strings.Contains(text, "/api/") {
				out[text] = append(out[text], fset.Position(pos).String())
			}
		}
		var flatten func(e ast.Expr) string
		flatten = func(e ast.Expr) string {
			switch x := e.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					if s, err := strconv.Unquote(x.Value); err == nil {
						return s
					}
				}
			case *ast.BinaryExpr:
				if x.Op == token.ADD {
					inConcat[x.X], inConcat[x.Y] = true, true
					return flatten(x.X) + flatten(x.Y)
				}
			case *ast.ParenExpr:
				inConcat[x.X] = true
				return flatten(x.X)
			}
			return "1"
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BinaryExpr:
				if x.Op == token.ADD && !inConcat[x] {
					if s := flatten(x); s != "1" {
						add(s, x.Pos())
					}
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING && !inConcat[x] {
					if s, err := strconv.Unquote(x.Value); err == nil {
						add(s, x.Pos())
					}
				}
			}
			return true
		})
	}
	return out
}

func TestServerRepliesNameRealRequests(t *testing.T) {
	check := namedRequestCheck(t)
	n := 0
	for text, where := range serverTexts(t) {
		text = strings.ReplaceAll(text, "</", " </") // an HTML page's "POST /api/action</h2>"
		for _, rq := range docRequests("", text) {
			n++
			for _, p := range check(rq.method, rq.path, rq.body) {
				t.Errorf("%s: %s", where[0], p)
			}
		}
	}
	if n < 200 {
		t.Fatalf("found only %d requests in the server's texts: the reader no longer finds them", n)
	}
}
