package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agentrpg/agentrpg/game"
)

// actionDispatchers: the functions that resolve an action type, and the files they are in. A type the table lists is handled by one
// of them (unless the GM rules on it, or it has its own request); every word they handle is in the table.
var actionDispatchers = map[string][]string{
	"actions.go":              {"handleAction", "resolveActionWithBonusDash"},
	"companions_2024.go":      {"handleCompanionAction"},
	"companions_more_2024.go": {"companionSpecialAction"},
	"pending_turns_2024.go":   {"validatePendingSteps"},
}

// handledActionWords: the string literals the dispatchers compare an action with (action == "x", req.Action == "x", case "x").
func handledActionWords(t *testing.T) map[string]string {
	isActionExpr := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name == "action"
		case *ast.SelectorExpr:
			return x.Sel.Name == "Action"
		}
		return false
	}
	lit := func(e ast.Expr) (string, bool) {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			s, err := strconv.Unquote(b.Value)
			return s, err == nil && s != ""
		}
		return "", false
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for file, funcs := range actionDispatchers {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !containsStr(funcs, fd.Name.Name) {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BinaryExpr:
					if x.Op == token.EQL || x.Op == token.NEQ {
						if s, ok := lit(x.Y); ok && isActionExpr(x.X) {
							out[s] = fd.Name.Name
						}
						if s, ok := lit(x.X); ok && isActionExpr(x.Y) {
							out[s] = fd.Name.Name
						}
					}
				case *ast.SwitchStmt:
					if x.Tag != nil && isActionExpr(x.Tag) {
						for _, st := range x.Body.List {
							for _, e := range st.(*ast.CaseClause).List {
								if s, ok := lit(e); ok {
									out[s] = fd.Name.Name
								}
							}
						}
					}
				}
				return true
			})
		}
	}
	return out
}

// TestActionDispatchIsTheTable: the dispatch handles exactly the action table's types: every word it handles is a type (or an alias),
// and every type the server resolves itself has a handler.
func TestActionDispatchIsTheTable(t *testing.T) {
	handled := handledActionWords(t)
	if len(handled) < 40 {
		t.Fatalf("found only %d handled words: has the dispatch moved?", len(handled))
	}
	for w, fn := range handled {
		if _, ok := lookupActionType(w); !ok {
			if _, variant := costVariants[w]; !variant {
				t.Errorf("%s handles %q, which is not in the action table (action_types_2024.go): add a row (cost, fields, what, example)", fn, w)
			}
		}
	}
	for _, at := range actionTypes {
		if at.GM || at.Send == sendRoute {
			continue // recorded for the GM, or a request of its own
		}
		found := false
		for _, w := range append([]string{at.Name}, at.Aliases...) {
			if _, ok := handled[w]; ok {
				found = true
			}
		}
		if !found {
			t.Errorf("the action table lists %q but no dispatcher handles it (mark it GM if the GM rules on it)", at.Name)
		}
	}
}

// TestActionTypesAreDocumented: every type carries its title, section, cost, what it does, the fields it reads and an example that
// is a real request (its keys among its fields; the fields real fields of the request).
func TestActionTypesAreDocumented(t *testing.T) {
	requestFieldsKnown := map[string]bool{"path": true, "altitude": true} // a pending turn's step adds these (ptStep)
	for _, f := range requestFields(actionRequest{}) {
		requestFieldsKnown[f] = true
	}
	seen := map[string]string{}
	for _, at := range actionTypes {
		if at.Name == "" || at.Title == "" || at.What == "" || at.Example == "" {
			t.Errorf("%q needs a name, title, what and example", at.Name)
		}
		if !containsStr(actionSections, at.Section) {
			t.Errorf("%s: section %q is not one of actionSections", at.Name, at.Section)
		}
		if _, ok := costLabels[at.Cost]; !ok {
			t.Errorf("%s: cost %q has no label", at.Name, at.Cost)
		}
		if at.Name != strings.ToLower(at.Name) || at.Name != game.Slug(at.Name) && strings.ContainsAny(at.Name, " -") {
			t.Errorf("%s: a type's name is lower case with underscores", at.Name)
		}
		for _, w := range append([]string{at.Name}, at.Aliases...) {
			if other, dup := seen[game.Key(w)]; dup {
				t.Errorf("%q names both %s and %s", w, other, at.Name)
			}
			seen[game.Key(w)] = at.Name
		}
		ex := at.Example
		if at.Send == sendRoute {
			if !strings.HasPrefix(ex, at.Route+" {") {
				t.Errorf("%s: a route's example starts with its request (%s)", at.Name, at.Route)
			}
			ex = ex[len(at.Route)+1:]
		}
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(ex), &body); err != nil {
			t.Errorf("%s: the example is not a JSON body: %v", at.Name, err)
			continue
		}
		if at.Send != sendRoute {
			if body["action"] != at.Name {
				t.Errorf("%s: the example sends action %v", at.Name, body["action"])
			}
			for _, f := range at.Fields {
				if !requestFieldsKnown[f] {
					t.Errorf("%s: field %q is not a field of POST /api/action", at.Name, f)
				}
			}
		}
		for k := range body {
			if k != "action" && !at.takes(k) {
				t.Errorf("%s: the example sends %q, which is not among its fields %v", at.Name, k, at.Fields)
			}
		}
		if at.Send == sendFamily && !at.takes("companion_id") {
			t.Errorf("%s: a companion's action takes companion_id", at.Name)
		}
		if at.GM && !at.takes("description") {
			t.Errorf("%s: an action the GM rules on takes a description", at.Name)
		}
	}
	if _, ok := lookupActionType("other"); ok {
		t.Error("there is no \"other\" action")
	}
}

// TestActionTypesInSkillMd: skill.md's action list is the table's (ACTIONS_UPDATE=1 rewrites it).
func TestActionTypesInSkillMd(t *testing.T) {
	const path = "../../docs/skill.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i, j := strings.Index(s, actionsBegin), strings.Index(s, actionsEnd)
	if i < 0 || j < i {
		t.Fatal("skill.md has no action markers")
	}
	want := s[:i+len(actionsBegin)] + "\n" + actionTypesMarkdown() + s[j:]
	if s != want {
		if os.Getenv("ACTIONS_UPDATE") == "1" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatal("docs/skill.md's action list differs from the action table: run ACTIONS_UPDATE=1 go test ./cmd/server -run TestActionTypesInSkillMd")
	}
}

// TestCommandsAndReferenceListTheTable: /commands lists every type of the table and no other; the API reference's action field takes
// exactly the types sent to POST /api/action (and their aliases).
func TestCommandsAndReferenceListTheTable(t *testing.T) {
	rec := httptest.NewRecorder()
	handleCommandsPage(rec, httptest.NewRequest("GET", "/commands", nil))
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`data-action="([^"]+)"`).FindAllStringSubmatch(rec.Body.String(), -1) {
		listed[m[1]] = true
	}
	var want, got []string
	for _, at := range actionTypes {
		want = append(want, at.Name)
	}
	for n := range listed {
		got = append(got, n)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("/commands lists %v, the table %v", got, want)
	}
	if strings.Contains(rec.Body.String(), `<code>other</code>`) {
		t.Error("/commands still lists an \"other\" action")
	}

	var spec struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Properties map[string]struct {
							Enum []string `json:"enum"`
						} `json:"properties"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(swaggerJSON, &spec); err != nil {
		t.Fatal(err)
	}
	enum := spec.Paths["/api/action"]["post"].RequestBody.Content["application/json"].Schema.Properties["action"].Enum
	var sent []string
	for _, at := range actionTypes {
		if at.sentToAction() {
			sent = append(sent, at.Name)
			sent = append(sent, at.Aliases...)
		}
	}
	sort.Strings(sent)
	sort.Strings(enum)
	if !reflect.DeepEqual(sent, enum) {
		t.Errorf("the API reference's action types %v differ from the table's %v: run python3 tools/build_swagger.py", enum, sent)
	}
}

// TestNoOtherActionWordList: no list of action types is kept by hand: no case, slice or map in the server's code (outside the table)
// names six or more of them, and no line of the docs names six or more outside skill.md's generated list.
func TestNoOtherActionWordList(t *testing.T) {
	isType := func(s string) bool { _, ok := actionTypeIndex[s]; return ok && s == strings.ToLower(s) }
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "action_types_2024.go" {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		check := func(at token.Pos, exprs []ast.Expr) {
			n := map[string]bool{}
			for _, e := range exprs {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					e = kv.Key
				}
				if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
					if s, err := strconv.Unquote(b.Value); err == nil && isType(s) {
						n[s] = true
					}
				}
			}
			if len(n) >= 6 {
				t.Errorf("%s keeps a list of %d action types by hand: read the action table (action_types_2024.go)", fset.Position(at), len(n))
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CaseClause:
				check(x.Pos(), x.List)
			case *ast.CompositeLit:
				check(x.Pos(), x.Elts)
			}
			return true
		})
	}
	tick := regexp.MustCompile("`([a-z_]+)`")
	for _, doc := range []string{"../../docs/skill.md", "../../docs/PLAYER_HEARTBEAT.md", "../../docs/GM_HEARTBEAT.md", "../../docs/gm_guide.md"} {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if i, j := strings.Index(s, actionsBegin), strings.Index(s, actionsEnd); i >= 0 && j > i {
			s = s[:i] + s[j:]
		}
		for ln, line := range strings.Split(s, "\n") {
			n := map[string]bool{}
			for _, m := range tick.FindAllStringSubmatch(line, -1) {
				if isType(m[1]) {
					n[m[1]] = true
				}
			}
			if len(n) >= 6 {
				t.Errorf("%s:%d lists %d action types by hand: point to skill.md's action list or /commands", doc, ln+1, len(n))
			}
		}
	}
}

// Every player action a doc shows ({"action": "x" ...}) is a type of the table, and none is "other".
func TestDocsShowOnlyRealActionTypes(t *testing.T) {
	ex := regexp.MustCompile(`\{\s*\\?"action\\?"\s*:\s*\\?"([^"\\]+)\\?"`)
	for _, doc := range []string{"../../docs/skill.md", "../../docs/PLAYER_HEARTBEAT.md", "../../docs/GM_HEARTBEAT.md", "../../docs/gm_guide.md"} {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ex.FindAllStringSubmatch(string(b), -1) {
			if strings.Contains(m[1], "...") || strings.HasPrefix(m[1], "<") {
				continue // a placeholder
			}
			if _, ok := lookupActionType(m[1]); !ok {
				t.Errorf("%s shows {\"action\": %q}, which is not an action type", doc, m[1])
			}
		}
	}
}

// A Ready holds a real action, read from "trigger: ...; action: ..." or the description.
func TestReadiedAction(t *testing.T) {
	for desc, want := range map[string]string{
		"trigger: the goblin comes through the door; action: attack it with my spear": "attack",
		"trigger: a spell is cast; action: cast counterspell":                         "cast",
		"attack the first goblin through the door":                                    "attack",
		"trigger: they run; action: Utilize the lever":                                "use_item",
		"trigger: the ogre charges; action: move behind the pillar":                   "move",
	} {
		if _, got, _, ok := readiedAction(desc); !ok || got != want {
			t.Errorf("readiedAction(%q) = %q, %v; want %q", desc, got, ok, want)
		}
	}
	for _, desc := range []string{"trigger: the door opens; action: other", "when the door opens I do something", "trigger: x; action: ready again", ""} {
		if _, got, _, ok := readiedAction(desc); ok {
			t.Errorf("readiedAction(%q) holds %q; a Ready must name an action it can hold", desc, got)
		}
	}
}

// A word that is no action type is refused, and a spell's name points to cast.
func TestActionWordProblem(t *testing.T) {
	for word, want := range map[string]string{
		"other": "unknown_action", "frobnicate": "unknown_action", "": "unknown_action",
		"attack": "", "Magic": "", "UTILIZE": "", "speak": "", "breath": "companion_id_required",
		"map_move": "pending_turn_step", "wholeness_of_body": "use_its_request", "font_of_magic": "use_its_request",
	} {
		if got, _ := actionWordProblem(canonicalAction(word), 0); got != want {
			t.Errorf("actionWordProblem(%q) = %q, want %q", word, got, want)
		}
	}
	if got, _ := actionWordProblem("speak", 5); got != "invalid_companion_action" {
		t.Errorf("a companion can't be sent speak: got %q", got)
	}
	if got, _ := actionWordProblem("attack", 5); got != "" {
		t.Errorf("a companion can attack: got %q", got)
	}
}
