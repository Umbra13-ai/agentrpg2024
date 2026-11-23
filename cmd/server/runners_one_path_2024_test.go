package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Plan v2.1, S0 gate (Alden: spells that dealt damage without an attack roll or a saving throw, I-CALLA-12): every way the server
// runs a player's request for the player (a pending turn's step, a readied action fired by the player or the GM, a standing Reaction
// order, a round submission the GM resolves or stands in for) goes through POST /api/action's handler, so a spell attack makes its
// roll against Armor Class and a saving-throw spell its save, exactly as on the player's own turn. This test keeps it so: the
// action resolver is called from nowhere else but the named exceptions, and each runner names handleAction (or serveAs, which
// serves the route table) as what it runs.
func TestEveryRunnerOfAPlayersRequestUsesTheActionHandler(t *testing.T) {
	// resolveAction runs one action word; these callers are the only ones outside handleAction's own path
	allowed := map[string]string{
		"resolveActionWithBonusDash": "Horde Breaker's second attack: the same attack owner, in the same turn",
		"handleGMRetaliation":        "the GM's retaliation attack for a character: an attack roll through the attack owner",
		"resolveAction":              "its own recursion",
	}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	callers := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "resolveAction" {
						callers[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	var names []string
	for n := range callers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := allowed[n]; !ok && n != "handleAction" && !strings.HasPrefix(n, "resolveAction") {
			t.Errorf("%s calls resolveAction: a request run for a player goes through handleAction (POST /api/action), so it rolls what the player's own turn rolls; add it to the allowed list only with its reason", n)
		}
	}

	// the runners: each runs the request through the action handler
	runners := map[string][]string{
		"pending_turns_2024.go":   {"handleAction"},   // a pending turn's step
		"readied_2024.go":         {"handleAction"},   // a readied action fired by the player or the GM
		"reaction_orders_2024.go": {"handleAction"},   // a standing Reaction order
		"noncombat_round_2024.go": {"replayAsPlayer"}, // a round submission the GM resolves, and the GM's stand-in: straight to the routes
	}
	for f, words := range runners {
		src := readSource(t, f)
		for _, w := range words {
			if !strings.Contains(src, w) {
				t.Errorf("%s no longer names %s: how does it run a player's request now?", f, w)
			}
		}
	}
	// replayAsPlayer serves the route table as the player: its request is POST /api/action, not a copy of the resolver
	if src := readSource(t, "noncombat_round_2024.go"); !strings.Contains(src, `"/api/action"`) {
		t.Errorf("the GM's stand-in no longer sends its turn as POST /api/action")
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
