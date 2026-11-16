package main

import (
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// dbCallMethods: a call of one of these methods is a database call (db, a transaction, a row, rows or a statement).
var dbCallMethods = map[string]bool{"Exec": true, "ExecContext": true, "Query": true, "QueryContext": true, "QueryRow": true,
	"QueryRowContext": true, "Scan": true, "Begin": true, "BeginTx": true, "Commit": true, "Prepare": true, "PrepareContext": true,
	"Ping": true, "PingContext": true}

// containsDBCall: the expression calls the database somewhere.
func containsDBCall(e ast.Node) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // a function passed along (inTx's change) returns its own errors
		}
		if c, ok := n.(*ast.CallExpr); ok {
			if s, ok := c.Fun.(*ast.SelectorExpr); ok && dbCallMethods[s.Sel.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

// errorTextAllowed: a call that may read an error's text without putting it in a reply: the log, and inspecting it.
func errorTextAllowed(c *ast.CallExpr) bool {
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		id, ok := c.Fun.(*ast.Ident)
		return ok && (id.Name == "noteDBError" || id.Name == "handledDBError")
	}
	pkg, _ := s.X.(*ast.Ident)
	if pkg == nil {
		return false
	}
	switch pkg.Name {
	case "log":
		return true
	case "fmt":
		return s.Sel.Name == "Printf" || s.Sel.Name == "Println" || s.Sel.Name == "Print" // the server's own output
	case "strings", "errors":
		return true // strings.Contains(err.Error(), "unique"), errors.Is
	}
	return false
}

// dbErrorTextUses lists where a function puts the text of a database call's error anywhere but the log: err.Error(), or the
// error formatted by fmt.Sprint* / fmt.Errorf / fmt.Fprint*. A variable counts as a database error from an assignment whose
// right side calls the database until it is assigned something else.
func dbErrorTextUses(fset *token.FileSet, body *ast.BlockStmt) []token.Position {
	marked := map[string]bool{}
	var out []token.Position
	var stack []ast.Node
	allowedAbove := func() bool {
		for i := len(stack) - 1; i >= 0; i-- {
			if c, ok := stack[i].(*ast.CallExpr); ok && errorTextAllowed(c) {
				return true
			}
		}
		return false
	}
	isMarked := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && marked[id.Name]
	}
	var saved []map[string]bool // a function literal is its own scope: what it marks is forgotten after it
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			if _, ok := stack[len(stack)-1].(*ast.FuncLit); ok {
				marked, saved = saved[len(saved)-1], saved[:len(saved)-1]
			}
			stack = stack[:len(stack)-1]
			return true
		}
		switch x := n.(type) {
		case *ast.FuncLit:
			cp := map[string]bool{}
			for k, v := range marked {
				cp[k] = v
			}
			saved = append(saved, marked)
			marked = cp
		case *ast.AssignStmt:
			db := false
			for _, r := range x.Rhs {
				if containsDBCall(r) {
					db = true
				}
			}
			for _, l := range x.Lhs {
				if id, ok := l.(*ast.Ident); ok && strings.Contains(strings.ToLower(id.Name), "err") {
					marked[id.Name] = db
				}
			}
		case *ast.CallExpr:
			if s, ok := x.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Error" && len(x.Args) == 0 && isMarked(s.X) && !allowedAbove() {
				out = append(out, fset.Position(x.Pos()))
			}
			if s, ok := x.Fun.(*ast.SelectorExpr); ok {
				if pkg, _ := s.X.(*ast.Ident); pkg != nil && pkg.Name == "fmt" && (strings.HasPrefix(s.Sel.Name, "Sprint") ||
					s.Sel.Name == "Errorf" || strings.HasPrefix(s.Sel.Name, "Fprint")) && !allowedAbove() {
					for _, a := range x.Args {
						if isMarked(a) {
							out = append(out, fset.Position(a.Pos()))
						}
					}
				}
			}
		}
		stack = append(stack, n)
		return true
	})
	return out
}

// startupOnly: functions that run before the server serves; their errors go to the log and stop the start (startup_2024.go).
var startupOnly = map[string]bool{"migrate": true, "loadSRDFromDB": true}

// TestNoDatabaseErrorTextInReplies: no reply carries a database or driver error's own text (HANDOFF item 27: registration answered
// {"error":"dial tcp 127.0.0.1:5433: connect: connection refused"} with Postgres down). A database failure is answered by the
// owner (db_errors_2024.go: writeDBError, writeDBWriteError, answerDBFailure, notFoundOrDBError, dbFailureError) and logged
// there; its text goes only to the log. guardDB (db_guard_2024.go) is the net at run time for what this scan can't follow (an
// error returned by a helper).
func TestNoDatabaseErrorTextInReplies(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "publicError" {
					t.Errorf("%s: publicError guessed a database error from its text and passed any other text on; answer a database failure with the owner (db_errors_2024.go)", fset.Position(c.Pos()))
				}
			}
			return true
		})
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || startupOnly[fn.Name.Name] {
				continue
			}
			for _, p := range dbErrorTextUses(fset, fn.Body) {
				t.Errorf("%s (%s) puts a database error's text somewhere other than the log: answer it with the owner (db_errors_2024.go)", p, fn.Name.Name)
			}
		}
	}
}

// A 5xx a handler wrote itself over a database failure can carry the driver's text (HANDOFF item 27: "dial tcp ...: connection
// refused"): guardDB answers it with the one shape. A reply written by the owner keeps its own words; a helper's dbFailure reads
// as the owner's words even when a handler writes its text.
func TestDatabaseFailureAnswersOneShape(t *testing.T) {
	d := fakeDB(t)
	w := serve(func(w http.ResponseWriter, r *http.Request) {
		_, err := d.Exec("INSERT FAIL")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"` + err.Error() + `"}`))
	})
	if w.Code != 500 || strings.Contains(w.Body.String(), "pq:") || !strings.Contains(w.Body.String(), `"database_error"`) {
		t.Errorf("a raw 500 over a failure answered %d %s", w.Code, w.Body.String())
	}
	w = serve(func(w http.ResponseWriter, r *http.Request) {
		_, err := d.Exec("INSERT FAIL")
		writeDBWriteError(w, err, "the widget")
	})
	if w.Code != 500 || !strings.Contains(w.Body.String(), "the widget") || strings.Contains(w.Body.String(), "pq:") {
		t.Errorf("the owner's reply changed: %d %s", w.Code, w.Body.String())
	}
	w = serve(func(w http.ResponseWriter, r *http.Request) {
		_, err := d.Exec("INSERT FAIL")
		err = dbFailureError(err, "the gadget")
		if answerHelperDBFailure(w, err) {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	})
	if w.Code != 500 || !strings.Contains(w.Body.String(), "the gadget") {
		t.Errorf("a helper's database failure answered %d %s", w.Code, w.Body.String())
	}
	if f := dbFailureError(errors.New(`pq: relation "x" does not exist`), "the thing"); strings.Contains(f.Error(), "pq:") {
		t.Errorf("a dbFailure's text carries the driver's: %s", f.Error())
	}
	if dbFailureError(sql.ErrNoRows, "x") != sql.ErrNoRows || dbFailureError(nil, "x") != nil || answerHelperDBFailure(httptest.NewRecorder(), errors.New("a refusal")) {
		t.Error("a missing row, nil or a refusal was taken for a database failure")
	}
}

// isSchemaChange knows a schema change from any other statement.
func TestIsSchemaChange(t *testing.T) {
	for _, q := range []string{"ALTER TABLE characters ADD COLUMN IF NOT EXISTS x INT", "\n\t create table if not exists t (a int)",
		"CREATE UNIQUE INDEX IF NOT EXISTS i ON t (a)", "DROP TABLE IF EXISTS races", "TRUNCATE actions",
		"DO $$ BEGIN IF true THEN ALTER TABLE armor RENAME COLUMN a TO b; END IF; END $$"} {
		if !isSchemaChange(q) {
			t.Errorf("not seen as a schema change: %q", q)
		}
	}
	for _, q := range []string{"SELECT altered FROM t", "UPDATE characters SET hunter_swaps = 'create table'", "INSERT INTO t VALUES ('DROP TABLE x')",
		"DELETE FROM items WHERE ruleset = $1", "WITH x AS (SELECT 1) SELECT * FROM x"} {
		if isSchemaChange(q) {
			t.Errorf("taken for a schema change: %q", q)
		}
	}
}

// A schema change sent while the migrations aren't running is reported, with where it came from; during them it isn't.
func TestSchemaChangeOutsideStartupIsReported(t *testing.T) {
	d := fakeDB(t)
	var got []string
	old := schemaChangeOutside
	schemaChangeOutside = func(q, from string) { got = append(got, q+" <- "+from) }
	defer func() { schemaChangeOutside = old }()
	d.Exec("ALTER TABLE characters ADD COLUMN IF NOT EXISTS y INT")
	d.Exec("UPDATE characters SET y = 1")
	if len(got) != 1 || !strings.Contains(got[0], "TestSchemaChangeOutsideStartupIsReported") {
		t.Errorf("reported: %v", got)
	}
	got = nil
	schemaChangesAllowed.Store(true)
	d.Exec("ALTER TABLE characters ADD COLUMN IF NOT EXISTS y INT")
	schemaChangesAllowed.Store(false)
	if len(got) != 0 {
		t.Errorf("a migration was reported: %v", got)
	}
}

// TestNoSchemaChangeOutsideTheMigrations: no code but the migration list (database_init.go) holds a statement that changes the
// schema (audit AUD-11: ALTER TABLE in request handlers, GET /api/my-turn among them). Every string literal in the server is
// read, a multi-line one and one joined with + included.
func TestNoSchemaChangeOutsideTheMigrations(t *testing.T) {
	ddl := regexp.MustCompile(`(?is)\b(ALTER\s+TABLE|CREATE\s+(UNIQUE\s+)?INDEX|CREATE\s+(TEMP\s+|TEMPORARY\s+)?TABLE|DROP\s+(TABLE|INDEX|COLUMN)|ADD\s+COLUMN|RENAME\s+COLUMN|TRUNCATE\s+)`)
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "database_init.go" {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if s := joinedLiteral(n); s != "" && ddl.MatchString(s) {
				t.Errorf("%s holds a schema change: add it to schemaSteps (database_init.go), which runs once at start", fset.Position(n.Pos()))
				return false
			}
			return true
		})
	}
}

// joinedLiteral: the text of a string literal, or of literals joined with +.
func joinedLiteral(n ast.Node) string {
	switch x := n.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			if err == nil {
				return s
			}
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			if l, r := joinedLiteral(x.X), joinedLiteral(x.Y); l != "" && r != "" {
				return l + r
			}
		}
	}
	return ""
}
