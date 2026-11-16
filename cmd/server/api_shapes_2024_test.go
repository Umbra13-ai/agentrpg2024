package main

// Every command is documented, from the code that serves it (Matt, Nov 16 2026: "All commands should always be documented").
//
// The API reference (/docs, /docs/swagger.json, the route list on /commands) is built by python3 tools/build_swagger.py. The
// words of each operation (summary, description, parameters) are its handler's godoc block; this file reads the rest from the
// code itself, so it cannot drift from what the server does:
//   - the routes: the godoc blocks' @Router lines, and the function each block sits above (the handler);
//   - the request body: what the route's reader takes. Every body is read by readRequest (or gmRead / gmReadAs / noFields,
//     request_body_2024.go), so the analysis follows the handler (and the functions it calls, for the route's method and path)
//     to that call and documents the type it reads into: each field's JSON name, type and comment, the envelope fields the
//     reader adds (campaign_id of a GM command), whether a body is needed at all (optionalBody), and the fields the handler
//     refuses when they are missing. A route that reads no body is documented as taking none;
//   - the reply: every JSON success reply the route writes (writeJSONStatus, json.NewEncoder(w).Encode), with the keys of
//     the map or the fields of the struct it sends and the type of each value, followed through the variables and helper
//     functions that build it; a route that sends a file or a page documents its content type.
// build_swagger.py runs TestAPIShapes with API_SHAPES_OUT set to get that JSON; without it, TestAPIShapes checks the built
// reference against a fresh analysis: every operation's request fields and types, the reply of every operation, and no
// operation, parameter or field without a description.

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------- loading the package with its types

type apiPkg struct {
	fset       *token.FileSet
	files      []*ast.File
	fileNames  map[*ast.File]string
	info       *types.Info
	funcs      map[types.Object]*ast.FuncDecl // package-level functions
	byName     map[string]*ast.FuncDecl
	fieldDoc   map[token.Pos]string // a struct field's comment, by the position of its name
	lineCmt    map[string]map[int]*ast.CommentGroup
	handlers   map[string][]apiRoute // a function a godoc block documents: its routes
	ops        []*apiOp
	locals     map[*ast.FuncDecl]map[types.Object][]ast.Expr // what each local variable is given, per function
	appends    map[*ast.FuncDecl]map[types.Object][]ast.Expr
	keys       map[*ast.FuncDecl]map[types.Object][]keyedValue
	gameFiles  []*ast.File
	helpers    map[string]replyHelper // functions that write the reply they are given
	forwarding map[string]bool
	calls      map[string][]*ast.CallExpr
	augments   map[string][]augmentSite
}

// replyHelper: a function that writes the JSON reply it is given (writeJSONStatus, battleMapReply ...): which parameter holds
// the reply, and the status (a parameter, or a constant).
type replyHelper struct {
	payload, status, statusConst int
}

type keyedValue struct {
	key   string
	value ast.Expr
	pos   token.Pos
}

type apiRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// apiImporter type-checks this module's packages from source and the standard library from source; other modules are empty
// packages (their uses are errors the analysis does not need).
type apiImporter struct {
	fset *token.FileSet
	src  types.Importer
	pkgs map[string]*types.Package
	pkg  *apiPkg
	info *types.Info
}

const modulePath = "github.com/agentrpg/agentrpg/"

func (im *apiImporter) Import(path string) (*types.Package, error) {
	if p, ok := im.pkgs[path]; ok {
		return p, nil
	}
	if strings.HasPrefix(path, modulePath) {
		files := parseDir(im.fset, filepath.Join("..", "..", strings.TrimPrefix(path, modulePath)))
		if path == modulePath+"game" {
			im.pkg.gameFiles = files
		}
		conf := types.Config{Importer: im, Error: func(error) {}}
		p, _ := conf.Check(path, im.fset, files, im.info)
		im.pkgs[path] = p
		return p, nil
	}
	if !strings.Contains(strings.Split(path, "/")[0], ".") {
		if p, err := im.src.Import(path); err == nil {
			im.pkgs[path] = p
			return p, nil
		}
	}
	p := types.NewPackage(path, filepath.Base(path))
	p.MarkComplete()
	im.pkgs[path] = p
	return p, nil
}

func parseDir(fset *token.FileSet, dir string) []*ast.File {
	names, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	sort.Strings(names)
	var out []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		if f, err := parser.ParseFile(fset, n, nil, parser.ParseComments); err == nil {
			out = append(out, f)
		}
	}
	return out
}

var (
	apiPkgOnce sync.Once
	apiPkgMemo *apiPkg
)

// loadAPIPackage parses and type-checks this package (cmd/server) once per test run.
func loadAPIPackage(t testing.TB) *apiPkg {
	apiPkgOnce.Do(func() {
		build.Default.CgoEnabled = false
		fset := token.NewFileSet()
		p := &apiPkg{fset: fset, fileNames: map[*ast.File]string{}, funcs: map[types.Object]*ast.FuncDecl{},
			byName: map[string]*ast.FuncDecl{}, fieldDoc: map[token.Pos]string{}, lineCmt: map[string]map[int]*ast.CommentGroup{},
			handlers: map[string][]apiRoute{}, locals: map[*ast.FuncDecl]map[types.Object][]ast.Expr{},
			appends: map[*ast.FuncDecl]map[types.Object][]ast.Expr{}, keys: map[*ast.FuncDecl]map[types.Object][]keyedValue{}}
		p.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{},
			Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		im := &apiImporter{fset: fset, src: importer.ForCompiler(fset, "source", nil), pkgs: map[string]*types.Package{}, pkg: p, info: p.info}
		p.files = parseDir(fset, ".")
		for _, f := range p.files {
			p.fileNames[f] = filepath.Base(fset.Position(f.Pos()).Filename)
		}
		conf := types.Config{Importer: im, Error: func(error) {}}
		conf.Check("main", fset, p.files, p.info)
		for _, f := range append(append([]*ast.File{}, p.files...), p.gameFiles...) {
			p.indexComments(f)
			if !hasFile(p.files, f) {
				continue
			}
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
					if obj := p.info.Defs[fd.Name]; obj != nil {
						p.funcs[obj] = fd
					}
					p.byName[fd.Name.Name] = fd
				}
			}
		}
		p.ops = p.godocOps()
		p.findReplyHelpers()
		p.forwarding = map[string]bool{}
		for _, o := range p.ops {
			for _, r := range o.Routes {
				p.handlers[o.Func] = append(p.handlers[o.Func], r)
			}
		}
		apiPkgMemo = p
	})
	if apiPkgMemo == nil {
		t.Fatal("could not load the package")
	}
	return apiPkgMemo
}

func hasFile(l []*ast.File, f *ast.File) bool {
	for _, x := range l {
		if x == f {
			return true
		}
	}
	return false
}

// indexComments records each struct field's comment and each comment by line.
func (p *apiPkg) indexComments(f *ast.File) {
	name := p.fset.Position(f.Pos()).Filename
	lines := map[int]*ast.CommentGroup{}
	for _, cg := range f.Comments {
		lines[p.fset.Position(cg.Pos()).Line] = cg
	}
	p.lineCmt[name] = lines
	ast.Inspect(f, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fl := range st.Fields.List {
			text := strings.TrimSpace(strings.TrimSpace(fl.Doc.Text()) + " " + strings.TrimSpace(fl.Comment.Text()))
			if text == "" && len(fl.Names) > 0 { // a field of struct type: the comment after "Buy []struct {" on its first line
				at := p.fset.Position(fl.Names[0].End())
				if cg := lines[at.Line]; cg != nil && p.fset.Position(fl.End()).Line > at.Line {
					text = strings.TrimSpace(cg.Text())
				}
			}
			for _, nm := range fl.Names {
				p.fieldDoc[nm.Pos()] = cleanComment(text)
			}
		}
		return true
	})
}

// cleanComment: a comment as a description (one line, without notes meant only for maintainers' dates).
func cleanComment(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// trailingComment: the comment written after a node on its last line, when nothing else follows the node on that line, or
// the comment on the line above when the node starts its line.
func (p *apiPkg) trailingComment(n ast.Node, startsLine bool) string {
	end := p.fset.Position(n.End())
	lines := p.lineCmt[end.Filename]
	if cg := lines[end.Line]; cg != nil && p.fset.Position(cg.Pos()).Column > end.Column {
		return cleanComment(cg.Text())
	}
	if startsLine {
		start := p.fset.Position(n.Pos())
		if cg := lines[start.Line-1]; cg != nil && p.fset.Position(cg.End()).Line == start.Line-1 && p.fset.Position(cg.Pos()).Column <= start.Column {
			return cleanComment(cg.Text())
		}
	}
	return ""
}

// ---------------------------------------------------------------- the godoc blocks

type apiParam struct {
	Name        string                 `json:"name"`
	In          string                 `json:"in"`
	Required    bool                   `json:"required"`
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema"`
}

type apiOp struct {
	Func        string     `json:"func"`
	Routes      []apiRoute `json:"routes"`
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	Tags        []string   `json:"tags"`
	Params      []apiParam `json:"params"`
	BodyNote    string     `json:"body_note"` // @Body: what the body is for, in words (its fields come from the reader)
	Success     string     `json:"success"`   // @Success: the reply, in words (its shape comes from the code)
	file        string
	line        int
}

var (
	godocHeader = regexp.MustCompile(`^\s*\w+ godoc\s*$`)
	godocParam  = regexp.MustCompile(`^@Param\s+(\S+)\s+(\S+)\s+(\S+)\s+(true|false)\s*"?(.*?)"?\s*$`)
	godocRouter = regexp.MustCompile(`^@Router\s+(\S+)\s+\[(\w+)\]`)
	godocReply  = regexp.MustCompile(`^@Success\s+2\d\d\s+\{\w+\}\s+\S+\s*"?(.*?)"?\s*$`)
)

// siteHandlers: the handlers the router registers only outside /api (/health ...): their @Router path is the site's own.
func (p *apiPkg) siteHandlers() map[string]bool {
	inAPI, outside := map[string]bool{}, map[string]bool{}
	for _, f := range p.files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			path, _ := strconv.Unquote(lit.Value)
			h := innermostIdent(call.Args[1])
			if h == "" {
				return true
			}
			if strings.HasPrefix(path, "/api") {
				inAPI[h] = true
			} else {
				outside[h] = true
			}
			return true
		})
	}
	out := map[string]bool{}
	for h := range outside {
		if !inAPI[h] {
			out[h] = true
		}
	}
	return out
}

// innermostIdent: the handler inside wrappers (withAPILogging(handleX) is handleX).
func innermostIdent(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.CallExpr:
			if len(x.Args) == 0 {
				return ""
			}
			e = x.Args[len(x.Args)-1]
		default:
			return ""
		}
	}
}

// godocOps reads every godoc block with an @Router line, and the function it sits directly above.
func (p *apiPkg) godocOps() []*apiOp {
	site := p.siteHandlers()
	var out []*apiOp
	for _, f := range p.files {
		funcAt := map[int]string{} // the line a function starts on -> its name
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				line := p.fset.Position(fd.Pos()).Line
				if fd.Doc != nil {
					line = p.fset.Position(fd.Type.Pos()).Line
				}
				funcAt[line] = fd.Name.Name
			}
		}
		for _, cg := range f.Comments {
			var block []string
			startLine := 0
			lines := cg.List
			for i, c := range lines {
				s := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				if godocHeader.MatchString(s) {
					block = nil
					startLine = p.fset.Position(c.Pos()).Line
				}
				if startLine == 0 {
					startLine = p.fset.Position(c.Pos()).Line
				}
				block = append(block, s)
				_ = i
			}
			hasRouter := false
			for _, b := range block {
				if strings.HasPrefix(b, "@Router") {
					hasRouter = true
				}
			}
			if !hasRouter {
				continue
			}
			endLine := p.fset.Position(cg.End()).Line
			o := &apiOp{Func: funcAt[endLine+1], file: p.fileNames[f], line: startLine}
			for _, b := range block {
				switch {
				case strings.HasPrefix(b, "@Summary"):
					o.Summary = strings.TrimSpace(strings.TrimPrefix(b, "@Summary"))
				case strings.HasPrefix(b, "@Description"):
					o.Description = strings.TrimSpace(o.Description + " " + strings.TrimSpace(strings.TrimPrefix(b, "@Description")))
				case strings.HasPrefix(b, "@Tags"):
					for _, t := range strings.Split(strings.TrimPrefix(b, "@Tags"), ",") {
						if t = strings.TrimSpace(t); t != "" {
							o.Tags = append(o.Tags, t)
						}
					}
				case strings.HasPrefix(b, "@Success"):
					if m := godocReply.FindStringSubmatch(b); m != nil && m[1] != "" {
						o.Success = m[1]
					}
				case strings.HasPrefix(b, "@Body"):
					o.BodyNote = strings.TrimSpace(o.BodyNote + " " + strings.TrimSpace(strings.TrimPrefix(b, "@Body")))
				case strings.HasPrefix(b, "@Param"):
					m := godocParam.FindStringSubmatch(b)
					if m == nil || (m[2] != "query" && m[2] != "path") {
						continue
					}
					o.Params = append(o.Params, apiParam{Name: m[1], In: m[2], Required: m[4] == "true" || m[2] == "path",
						Description: m[5], Schema: paramSchema(m[3])})
				case strings.HasPrefix(b, "@Router"):
					m := godocRouter.FindStringSubmatch(b)
					if m == nil {
						continue
					}
					path := m[1]
					if !strings.HasPrefix(path, "/api") && !site[o.Func] {
						path = "/api" + path
					}
					o.Routes = append(o.Routes, apiRoute{strings.ToLower(m[2]), path})
				}
			}
			out = append(out, o)
		}
	}
	return out
}

func paramSchema(t string) map[string]interface{} {
	basic := map[string]string{"int": "integer", "integer": "integer", "string": "string", "bool": "boolean", "boolean": "boolean",
		"number": "number", "float": "number", "object": "object", "array": "array"}
	if strings.HasPrefix(t, "[]") {
		it := basic[t[2:]]
		if it == "" {
			it = "string"
		}
		return map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": it}}
	}
	if b := basic[t]; b != "" {
		return map[string]interface{}{"type": b}
	}
	return map[string]interface{}{"type": "string"}
}

// ---------------------------------------------------------------- following a route through its handler

type tri int

const (
	triNo tri = iota - 1
	triUnknown
	triYes
)

func (t tri) not() tri { return -t }

type readerCall struct {
	call *ast.CallExpr
	name string
	fn   *ast.FuncDecl
	rest []ast.Stmt // the statements after the one that reads, in the same block (the checks that follow it)
}

type replyWrite struct {
	payload ast.Expr
	fn      *ast.FuncDecl
	status  int
}

// routeWalk follows one route (method and path) from its handler through the code it can reach with that method and path:
// a branch on r.Method, or on a path segment that tells the handler's routes apart, is followed only when it can hold.
type routeWalk struct {
	p        *apiPkg
	route    apiRoute
	segs     map[string]bool // the route's literal path segments
	distinct map[string]bool // segments some of the handler's routes have and others don't
	visited  map[*ast.FuncDecl]bool
	fn       *ast.FuncDecl
	rest     []ast.Stmt
	status   int
	readers  []readerCall
	replies  []replyWrite
	ctypes   map[string]bool
	text     bool               // writes bytes or text, not JSON
	calls    map[token.Pos]bool // every call reached
	pathVars map[types.Object]pathVar
	forwards []string // other routes' handlers it calls (a route that forwards its request answers with their reply)
	unfollow []string // calls through a value (a map of handlers, a parameter): not followed
}

// readerFuncs: the request body readers (request_body_2024.go, gm_campaign_2024.go).
var readerFuncs = map[string]bool{"readRequest": true, "noFields": true, "gmRead": true, "gmReadAs": true, "parseRequest": true}

func segments(path string) []string {
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s != "" && !strings.HasPrefix(s, "{") {
			out = append(out, s)
		}
	}
	return out
}

func (p *apiPkg) walkRoute(fn string, rt apiRoute, start ...*ast.FuncDecl) *routeWalk {
	w := &routeWalk{p: p, route: rt, segs: map[string]bool{}, distinct: map[string]bool{}, visited: map[*ast.FuncDecl]bool{}, ctypes: map[string]bool{}, calls: map[token.Pos]bool{}, pathVars: map[types.Object]pathVar{}}
	for _, s := range segments(rt.Path) {
		w.segs[s] = true
	}
	count := map[string]int{}
	routes := p.handlers[fn]
	for _, r := range routes {
		seen := map[string]bool{}
		for _, s := range segments(r.Path) {
			if !seen[s] {
				count[s]++
				seen[s] = true
			}
		}
	}
	for s, n := range count {
		if n < len(routes) {
			w.distinct[s] = true
		}
	}
	if len(start) > 0 {
		w.walkFunc(start[0])
	} else if fd := p.byName[fn]; fd != nil {
		w.walkFunc(fd)
	}
	return w
}

func (w *routeWalk) walkFunc(fd *ast.FuncDecl) {
	if w.visited[fd] {
		return
	}
	w.visited[fd] = true
	saveFn, saveStatus, saveRest := w.fn, w.status, w.rest
	w.fn, w.status = fd, 0
	w.walkStmts(fd.Body.List)
	w.fn, w.status, w.rest = saveFn, saveStatus, saveRest
}

// walkStmts walks a block; true when it surely returns.
func (w *routeWalk) walkStmts(list []ast.Stmt) bool {
	saveStatus := w.status // a status written inside a block holds for that block only
	defer func() { w.status = saveStatus }()
	for i, s := range list {
		w.rest = list[i+1:]
		if w.walkStmt(s) {
			return true
		}
	}
	return false
}

func (w *routeWalk) walkStmt(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		w.inspect(s)
		return true
	case *ast.BlockStmt:
		return w.walkStmts(s.List)
	case *ast.LabeledStmt:
		return w.walkStmt(s.Stmt)
	case *ast.IfStmt:
		if s.Init != nil {
			w.walkStmt(s.Init)
		}
		rest := w.rest
		w.inspect(s.Cond)
		w.rest = rest
		switch w.eval(s.Cond) {
		case triYes:
			return w.walkStmts(s.Body.List)
		case triNo:
			if s.Else != nil {
				return w.walkStmt(s.Else)
			}
			return false
		}
		a := w.walkStmts(s.Body.List)
		b := false
		if s.Else != nil {
			b = w.walkStmt(s.Else)
		}
		return a && b
	case *ast.SwitchStmt:
		if s.Init != nil {
			w.walkStmt(s.Init)
		}
		if s.Tag != nil {
			w.inspect(s.Tag)
		}
		conds := make([]tri, len(s.Body.List))
		anyYes, allNo := false, true
		for i, c := range s.Body.List {
			cc := c.(*ast.CaseClause)
			if cc.List == nil {
				continue
			}
			v := triNo
			for _, e := range cc.List {
				var x tri
				if s.Tag != nil {
					x = w.evalEq(s.Tag, e)
				} else {
					x = w.eval(e)
				}
				if x == triYes {
					v = triYes
					break
				}
				if x == triUnknown {
					v = triUnknown
				}
			}
			conds[i] = v
			anyYes = anyYes || v == triYes
			allNo = allNo && v == triNo
		}
		hasDefault, allTerm := false, true
		for i, c := range s.Body.List {
			cc := c.(*ast.CaseClause)
			v := conds[i]
			if cc.List == nil {
				hasDefault = true
				switch {
				case anyYes:
					v = triNo
				case allNo:
					v = triYes
				default:
					v = triUnknown
				}
			}
			if v == triNo {
				continue
			}
			t := w.walkStmts(cc.Body)
			if v == triYes {
				return t
			}
			allTerm = allTerm && t
		}
		return hasDefault && allTerm
	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			w.walkStmt(s.Init)
		}
		w.inspect(s.Assign)
		for _, c := range s.Body.List {
			w.walkStmts(c.(*ast.CaseClause).Body)
		}
		return false
	case *ast.SelectStmt:
		for _, c := range s.Body.List {
			w.walkStmts(c.(*ast.CommClause).Body)
		}
		return false
	case *ast.ForStmt:
		if s.Init != nil {
			w.walkStmt(s.Init)
		}
		if s.Cond != nil {
			w.inspect(s.Cond)
		}
		w.walkStmts(s.Body.List)
		return false
	case *ast.RangeStmt:
		w.inspect(s.X)
		w.walkStmts(s.Body.List)
		return false
	case *ast.AssignStmt:
		if len(s.Lhs) == len(s.Rhs) {
			for i, l := range s.Lhs {
				if id, ok := l.(*ast.Ident); ok {
					obj := w.p.info.Defs[id]
					if obj == nil {
						obj = w.p.info.Uses[id]
					}
					if pv, ok := w.pathExpr(s.Rhs[i]); ok && obj != nil {
						w.pathVars[obj] = pv
					}
				}
			}
		}
		w.inspect(s)
		return false
	case *ast.ExprStmt:
		w.inspect(s)
		if call, ok := s.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
				return true
			}
		}
		return false
	}
	w.inspect(s)
	return false
}

// inspect handles every call in a statement or expression (a function literal's body is walked as a block).
func (w *routeWalk) inspect(n ast.Node) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			rest := w.rest
			w.walkStmts(x.Body.List)
			w.rest = rest
			return false
		case *ast.CallExpr:
			w.call(x)
		}
		return true
	})
}

func (p *apiPkg) constValue(e ast.Expr) constant.Value {
	if tv, ok := p.info.Types[e]; ok {
		return tv.Value
	}
	return nil
}

func (p *apiPkg) constString(e ast.Expr) (string, bool) {
	if v := p.constValue(e); v != nil && v.Kind() == constant.String {
		return constant.StringVal(v), true
	}
	return "", false
}

func (p *apiPkg) constInt(e ast.Expr) int {
	if v := p.constValue(e); v != nil && v.Kind() == constant.Int {
		n, _ := constant.Int64Val(v)
		return int(n)
	}
	return 0
}

// callee: the package-level function a call names (nil for a method, a value or another package's function).
func (p *apiPkg) callee(call *ast.CallExpr) (*ast.FuncDecl, string) {
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil, ""
	}
	obj := p.info.Uses[id]
	if _, isFunc := obj.(*types.Func); !isFunc {
		return nil, ""
	}
	return p.funcs[obj], id.Name
}

func (p *apiPkg) isHTTPType(t types.Type) bool {
	if t == nil {
		return false
	}
	s := t.String()
	return s == "net/http.ResponseWriter" || s == "*net/http.Request"
}

func (w *routeWalk) call(call *ast.CallExpr) {
	p := w.p
	w.calls[call.Pos()] = true
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		switch sel.Sel.Name {
		case "WriteHeader":
			if len(call.Args) == 1 && p.isHTTPType(p.info.TypeOf(sel.X)) {
				w.status = p.constInt(call.Args[0])
			}
		case "Set":
			if inner, ok := sel.X.(*ast.CallExpr); ok && len(call.Args) == 2 {
				if hs, ok := inner.Fun.(*ast.SelectorExpr); ok && hs.Sel.Name == "Header" && p.isHTTPType(p.info.TypeOf(hs.X)) {
					if k, ok := p.constString(call.Args[0]); ok && strings.EqualFold(k, "Content-Type") {
						if v, ok := p.constString(call.Args[1]); ok {
							w.ctypes[strings.TrimSpace(strings.Split(v, ";")[0])] = true
						}
					}
				}
			}
		case "Fprintf", "Fprint", "Fprintln", "WriteString", "Copy", "ServeContent", "ServeFile":
			if len(call.Args) > 0 && p.isHTTPType(p.info.TypeOf(call.Args[0])) {
				w.text = true
			}
		case "Write":
			if p.isHTTPType(p.info.TypeOf(sel.X)) {
				w.text = true
			}
		case "Encode":
			if inner, ok := sel.X.(*ast.CallExpr); ok && len(call.Args) == 1 {
				if ns, ok := inner.Fun.(*ast.SelectorExpr); ok && ns.Sel.Name == "NewEncoder" && len(inner.Args) == 1 && p.isHTTPType(p.info.TypeOf(inner.Args[0])) {
					w.replies = append(w.replies, replyWrite{payload: call.Args[0], fn: w.fn, status: w.status})
				}
			}
		}
		return
	}
	fd, name := p.callee(call)
	if name == "" {
		if _, isIdent := call.Fun.(*ast.Ident); !isIdent {
			return
		}
		for _, a := range call.Args { // a call through a value that is given the request: not followed
			if p.isHTTPType(p.info.TypeOf(a)) {
				w.unfollow = append(w.unfollow, p.fset.Position(call.Pos()).String())
				break
			}
		}
		return
	}
	if readerFuncs[name] {
		w.readers = append(w.readers, readerCall{call: call, name: name, fn: w.fn, rest: w.rest})
		return
	}
	if h, ok := p.helpers[name]; ok && len(call.Args) > h.payload {
		status := h.statusConst
		if h.status >= 0 && h.status < len(call.Args) {
			status = p.constInt(call.Args[h.status])
		}
		w.replies = append(w.replies, replyWrite{payload: call.Args[h.payload], fn: w.fn, status: status})
		return
	}
	if fd != nil && len(p.handlers[name]) > 0 { // another route's handler documents its own route
		w.forwards = append(w.forwards, name)
		return
	}
	if fd == nil {
		return
	}
	given := false
	for _, a := range call.Args { // only a function given the request or the reply can read the body or answer
		if p.isHTTPType(p.info.TypeOf(a)) {
			given = true
		}
	}
	if given {
		w.walkFunc(fd)
	}
}

// eval: can the condition hold for this route?
func (w *routeWalk) eval(e ast.Expr) tri {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return w.eval(x.X)
	case *ast.UnaryExpr:
		if x.Op == token.NOT {
			return w.eval(x.X).not()
		}
	case *ast.BinaryExpr:
		switch x.Op {
		case token.LAND:
			a, b := w.eval(x.X), w.eval(x.Y)
			if a == triNo || b == triNo {
				return triNo
			}
			if a == triYes && b == triYes {
				return triYes
			}
			return triUnknown
		case token.LOR:
			a, b := w.eval(x.X), w.eval(x.Y)
			if a == triYes || b == triYes {
				return triYes
			}
			if a == triNo && b == triNo {
				return triNo
			}
			return triUnknown
		case token.EQL:
			if t := w.evalLen(x); t != triUnknown {
				return t
			}
			return w.evalEq(x.X, x.Y)
		case token.NEQ:
			if t := w.evalLen(x); t != triUnknown {
				return t
			}
			return w.evalEq(x.X, x.Y).not()
		case token.GTR, token.LSS, token.GEQ, token.LEQ:
			return w.evalLen(x)
		}
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && len(x.Args) == 2 {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "strings" {
				switch sel.Sel.Name {
				case "HasSuffix", "HasPrefix", "Contains":
					if c, ok := w.p.constString(x.Args[1]); ok {
						return w.segTri(c)
					}
				case "EqualFold":
					return w.evalEq(x.Args[0], x.Args[1])
				}
			}
		}
	}
	return triUnknown
}

func (w *routeWalk) isMethod(e ast.Expr) bool {
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ToUpper" {
			return w.isMethod(call.Args[0])
		}
	}
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Method" && w.p.isHTTPType(w.p.info.TypeOf(sel.X))
}

func (w *routeWalk) evalEq(a, b ast.Expr) tri {
	if w.isMethod(b) {
		a, b = b, a
	}
	c, isConst := w.p.constString(b)
	if !isConst {
		if c2, ok := w.p.constString(a); ok {
			c, isConst, a, b = c2, true, b, a
		}
	}
	if !isConst {
		return triUnknown
	}
	if w.isMethod(a) {
		if strings.EqualFold(c, w.route.Method) {
			return triYes
		}
		return triNo
	}
	if c == "" {
		if id, ok := a.(*ast.Ident); ok { // rest == "": the path has nothing after the prefix
			if pv, ok := w.pathVars[w.p.info.Uses[id]]; ok && !pv.split {
				if rem, ok := pv.remainder(w.route.Path); ok {
					return triOf(len(rem) == 0)
				}
			}
		}
		if ix, ok := a.(*ast.IndexExpr); ok { // parts[0] == ""
			if id, ok := ix.X.(*ast.Ident); ok && w.p.constInt(ix.Index) == 0 {
				if pv, ok := w.pathVars[w.p.info.Uses[id]]; ok && pv.split {
					if rem, ok := pv.remainder(w.route.Path); ok {
						return triOf(len(rem) == 0)
					}
				}
			}
		}
		return triUnknown
	}
	return w.segTri(c)
}

func triOf(b bool) tri {
	if b {
		return triYes
	}
	return triNo
}

// pathVar: a variable holding the request's path after a prefix (rest := strings.TrimPrefix(r.URL.Path, "/api/x")), or that
// remainder split into segments (parts := strings.Split(rest, "/")).
type pathVar struct {
	prefix string
	split  bool
}

// remainder: the segments of a route's path after the prefix ({id} counts as one).
func (pv pathVar) remainder(path string) ([]string, bool) {
	pre := strings.TrimRight(pv.prefix, "/")
	if pre == "" || !strings.HasPrefix(path, pre) {
		return nil, false
	}
	var out []string
	for _, s := range strings.Split(strings.Trim(path[len(pre):], "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out, true
}

// pathExpr: whether an expression is the request's path (r.URL.Path), trimmed of a prefix, or split.
func (w *routeWalk) pathExpr(e ast.Expr) (pathVar, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return w.pathExpr(x.X)
	case *ast.Ident:
		pv, ok := w.pathVars[w.p.info.Uses[x]]
		return pv, ok
	case *ast.SelectorExpr:
		if x.Sel.Name == "Path" {
			if inner, ok := x.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "URL" && w.p.isHTTPType(w.p.info.TypeOf(inner.X)) {
				return pathVar{}, true
			}
		}
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok || len(x.Args) == 0 {
			return pathVar{}, false
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "strings" {
			return pathVar{}, false
		}
		inner, ok := w.pathExpr(x.Args[0])
		if !ok || inner.split {
			return pathVar{}, false
		}
		switch sel.Sel.Name {
		case "Trim", "TrimSpace", "TrimLeft", "TrimRight", "TrimSuffix", "ToLower":
			return inner, true
		case "TrimPrefix":
			if c, ok := w.p.constString(x.Args[1]); ok && inner.prefix == "" {
				return pathVar{prefix: c}, true
			}
		case "Split", "SplitN":
			if inner.prefix != "" {
				return pathVar{prefix: inner.prefix, split: true}, true
			}
		}
	}
	return pathVar{}, false
}

// evalLen: len(parts) compared with a number, where parts is the path split after a prefix.
func (w *routeWalk) evalLen(x *ast.BinaryExpr) tri {
	call, ok := x.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return triUnknown
	}
	if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "len" {
		return triUnknown
	}
	id, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return triUnknown
	}
	pv, ok := w.pathVars[w.p.info.Uses[id]]
	if !ok || !pv.split {
		return triUnknown
	}
	cv := w.p.constValue(x.Y)
	if cv == nil || cv.Kind() != constant.Int {
		return triUnknown
	}
	rem, ok := pv.remainder(w.route.Path)
	if !ok {
		return triUnknown
	}
	n := len(rem)
	if n == 0 {
		n = 1 // strings.Split("", "/") is [""]
	}
	k, _ := constant.Int64Val(cv)
	switch x.Op {
	case token.EQL:
		return triOf(int64(n) == k)
	case token.NEQ:
		return triOf(int64(n) != k)
	case token.GTR:
		return triOf(int64(n) > k)
	case token.LSS:
		return triOf(int64(n) < k)
	case token.GEQ:
		return triOf(int64(n) >= k)
	case token.LEQ:
		return triOf(int64(n) <= k)
	}
	return triUnknown
}

// segTri: a path text that names a segment telling the handler's routes apart holds only on the routes with it.
func (w *routeWalk) segTri(c string) tri {
	found := false
	for _, part := range strings.Split(c, "/") {
		if part == "" || !w.distinct[part] {
			continue
		}
		found = true
		if !w.segs[part] {
			return triNo
		}
	}
	if found {
		return triYes
	}
	return triUnknown
}

// ---------------------------------------------------------------- JSON schemas from Go types and expressions

type schema = map[string]interface{}

// typeSchema: the JSON a value of type t is written as (encoding/json's rules), with each struct field's comment.
func (p *apiPkg) typeSchema(t types.Type, seen map[types.Type]bool) schema {
	if t == nil {
		return schema{}
	}
	if n, ok := t.(*types.Named); ok {
		switch n.String() {
		case "time.Time":
			return schema{"type": "string", "format": "date-time"}
		case "encoding/json.RawMessage":
			return schema{}
		case "database/sql.NullString":
			return schema{"type": "string", "nullable": true}
		case "database/sql.NullInt64", "database/sql.NullInt32":
			return schema{"type": "integer", "nullable": true}
		case "database/sql.NullBool":
			return schema{"type": "boolean", "nullable": true}
		case "database/sql.NullFloat64":
			return schema{"type": "number", "nullable": true}
		case "database/sql.NullTime":
			return schema{"type": "string", "format": "date-time", "nullable": true}
		}
		for i := 0; i < n.NumMethods(); i++ {
			if n.Method(i).Name() == "MarshalJSON" {
				return schema{}
			}
		}
		if seen[n] {
			return schema{"type": "object"}
		}
		seen = copySeen(seen)
		seen[n] = true
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return schema{"type": "boolean"}
		case u.Info()&types.IsInteger != 0:
			return schema{"type": "integer"}
		case u.Info()&types.IsFloat != 0:
			return schema{"type": "number"}
		case u.Info()&types.IsString != 0:
			return schema{"type": "string"}
		}
		return schema{}
	case *types.Pointer:
		s := p.typeSchema(u.Elem(), seen)
		return s
	case *types.Slice:
		if b, ok := u.Elem().Underlying().(*types.Basic); ok && b.Kind() == types.Byte {
			return schema{"type": "string", "format": "byte"}
		}
		return schema{"type": "array", "items": p.typeSchema(u.Elem(), seen)}
	case *types.Array:
		return schema{"type": "array", "items": p.typeSchema(u.Elem(), seen)}
	case *types.Map:
		s := schema{"type": "object"}
		if _, isIface := u.Elem().Underlying().(*types.Interface); !isIface {
			s["additionalProperties"] = p.typeSchema(u.Elem(), seen)
		}
		return s
	case *types.Struct:
		props := schema{}
		p.eachJSONField(u, func(name string, f *types.Var) {
			fs := p.typeSchema(f.Type(), seen)
			if d := p.fieldDoc[f.Pos()]; d != "" {
				fs = withDescription(fs, d)
			}
			props[name] = fs
		})
		return schema{"type": "object", "properties": props}
	case *types.Interface:
		return schema{}
	}
	return schema{}
}

func copySeen(m map[types.Type]bool) map[types.Type]bool {
	out := map[types.Type]bool{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func withDescription(s schema, d string) schema {
	out := schema{}
	for k, v := range s {
		out[k] = v
	}
	if d != "" {
		out["description"] = d
	}
	return out
}

// eachJSONField calls f with the JSON name of each field of a struct as encoding/json writes and reads it (the reader's own
// rule: structFields in request_body_2024.go).
func (p *apiPkg) eachJSONField(st *types.Struct, f func(string, *types.Var)) {
	for i := 0; i < st.NumFields(); i++ {
		fv := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" && !strings.HasPrefix(tag, "-,") {
			continue
		}
		if fv.Embedded() && name == "" {
			et := fv.Type()
			if pt, ok := et.(*types.Pointer); ok {
				et = pt.Elem()
			}
			if s, ok := et.Underlying().(*types.Struct); ok {
				p.eachJSONField(s, f)
				continue
			}
		}
		if !fv.Exported() {
			continue
		}
		if name == "" {
			name = fv.Name()
		}
		f(name, fv)
	}
}

// mergeSchema: what either of two values may be (the keys of two replies together; a key whose type differs is any value).
func mergeSchema(a, b schema) schema {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	ta, tb := a["type"], b["type"]
	if ta != tb {
		if ta == nil && len(a) == 0 {
			return b
		}
		if tb == nil && len(b) == 0 {
			return a
		}
		out := schema{}
		if d, ok := a["description"]; ok {
			out["description"] = d
		} else if d, ok := b["description"]; ok {
			out["description"] = d
		}
		return out
	}
	out := schema{}
	for k, v := range a {
		out[k] = v
	}
	if _, ok := out["description"]; !ok {
		if d, ok := b["description"]; ok {
			out["description"] = d
		}
	}
	switch ta {
	case "object":
		pa, _ := a["properties"].(schema)
		pb, _ := b["properties"].(schema)
		if pa != nil || pb != nil {
			props := schema{}
			for k, v := range pa {
				props[k] = v
			}
			for k, v := range pb {
				if old, ok := props[k]; ok {
					props[k] = mergeSchema(old.(schema), v.(schema))
				} else {
					props[k] = v
				}
			}
			out["properties"] = props
		}
		if ap, ok := b["additionalProperties"]; ok {
			if old, ok := out["additionalProperties"]; ok {
				out["additionalProperties"] = mergeSchema(old.(schema), ap.(schema))
			} else {
				out["additionalProperties"] = ap
			}
		}
	case "array":
		ia, _ := a["items"].(schema)
		ib, _ := b["items"].(schema)
		out["items"] = mergeSchema(ia, ib)
		if out["items"] == nil {
			out["items"] = schema{}
		}
	}
	return out
}

// localIndex: what each local variable of a function is given: assigned values, values put under a key (m["k"] = v) and
// values appended (l = append(l, v)).
func (p *apiPkg) localIndex(fd *ast.FuncDecl) {
	if _, done := p.locals[fd]; done {
		return
	}
	locals, appends, keys := map[types.Object][]ast.Expr{}, map[types.Object][]ast.Expr{}, map[types.Object][]keyedValue{}
	objOf := func(id *ast.Ident) types.Object {
		if o := p.info.Defs[id]; o != nil {
			return o
		}
		return p.info.Uses[id]
	}
	assign := func(lhs, rhs ast.Expr) {
		switch l := lhs.(type) {
		case *ast.Ident:
			obj := objOf(l)
			if obj == nil {
				return
			}
			if call, ok := rhs.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "append" && len(call.Args) > 0 {
					if first, ok := call.Args[0].(*ast.Ident); ok && objOf(first) == obj {
						if call.Ellipsis.IsValid() {
							appends[obj] = append(appends[obj], &ast.Ident{Name: "_spread"})
							for _, a := range call.Args[1:] {
								appends[obj] = append(appends[obj], &ast.StarExpr{X: a}) // the elements of a list
							}
						} else {
							appends[obj] = append(appends[obj], call.Args[1:]...)
						}
						return
					}
				}
			}
			locals[obj] = append(locals[obj], rhs)
		case *ast.IndexExpr:
			id, ok := l.X.(*ast.Ident)
			if !ok {
				return
			}
			if k, ok := p.constString(l.Index); ok {
				if obj := objOf(id); obj != nil {
					keys[obj] = append(keys[obj], keyedValue{key: k, value: rhs, pos: l.Pos()})
				}
			}
		}
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == len(x.Rhs) {
				for i := range x.Lhs {
					assign(x.Lhs[i], x.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			if len(x.Names) == len(x.Values) {
				for i := range x.Names {
					assign(x.Names[i], x.Values[i])
				}
			}
		}
		return true
	})
	p.locals[fd], p.appends[fd], p.keys[fd] = locals, appends, keys
}

// funcOf: the function a position is in.
func (p *apiPkg) funcOf(pos token.Pos) *ast.FuncDecl {
	for _, fd := range p.funcs {
		if fd.Pos() <= pos && pos < fd.End() {
			return fd
		}
	}
	return nil
}

// isParam: the object is a parameter of the function.
func isParam(fd *ast.FuncDecl, obj types.Object, info *types.Info) bool {
	for _, fl := range fd.Type.Params.List {
		for _, n := range fl.Names {
			if info.Defs[n] == obj {
				return true
			}
		}
	}
	return false
}

type exprCtx struct {
	depth int
	vars  map[types.Object]bool
}

// exprSchema: what an expression is written as: a map literal's keys and their values, a variable's values (assigned, put
// under a key, appended), what a function of this package returns; otherwise its type.
func (p *apiPkg) exprSchema(e ast.Expr, fd *ast.FuncDecl, c exprCtx) schema {
	t := p.info.TypeOf(e)
	if c.depth > 6 {
		return p.typeSchema(t, nil)
	}
	c.depth++
	switch x := e.(type) {
	case *ast.ParenExpr:
		return p.exprSchema(x.X, fd, c)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return p.exprSchema(x.X, fd, c)
		}
	case *ast.StarExpr: // the elements of a list (append(l, other...))
		s := p.exprSchema(x.X, fd, c)
		if it, ok := s["items"].(schema); ok {
			return it
		}
		return schema{}
	case *ast.CompositeLit:
		switch u := t.Underlying().(type) {
		case *types.Map:
			if b, ok := u.Key().Underlying().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
				return p.typeSchema(t, nil)
			}
			props := schema{}
			for i, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				k, ok := p.constString(kv.Key)
				if !ok {
					continue
				}
				vs := p.exprSchema(kv.Value, fd, c)
				last := i == len(x.Elts)-1 || p.fset.Position(x.Elts[i+1].Pos()).Line > p.fset.Position(kv.End()).Line
				first := i == 0 && p.fset.Position(x.Lbrace).Line < p.fset.Position(kv.Pos()).Line ||
					i > 0 && p.fset.Position(x.Elts[i-1].End()).Line < p.fset.Position(kv.Pos()).Line
				if last {
					if d := p.trailingComment(kv, first); d != "" {
						vs = withDescription(vs, d)
					}
				}
				if old, ok := props[k]; ok {
					vs = mergeSchema(old.(schema), vs)
				}
				props[k] = vs
			}
			s := schema{"type": "object", "properties": props}
			if _, isIface := u.Elem().Underlying().(*types.Interface); !isIface {
				s["additionalProperties"] = p.typeSchema(u.Elem(), nil)
			}
			return s
		case *types.Slice, *types.Array:
			var items schema
			for _, el := range x.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					el = kv.Value
				}
				items = mergeSchema(items, p.exprSchema(el, fd, c))
			}
			if items == nil {
				var et types.Type
				if s, ok := u.(*types.Slice); ok {
					et = s.Elem()
				} else {
					et = u.(*types.Array).Elem()
				}
				items = p.typeSchema(et, nil)
			}
			return schema{"type": "array", "items": items}
		}
		return p.typeSchema(t, nil)
	case *ast.Ident:
		obj, ok := p.info.Uses[x].(*types.Var)
		if !ok || obj.Parent() == nil || obj.Parent() == obj.Pkg().Scope() || obj.IsField() {
			return p.typeSchema(t, nil)
		}
		if c.vars[obj] {
			return p.typeSchema(obj.Type(), nil)
		}
		vars := map[types.Object]bool{obj: true}
		for k := range c.vars {
			vars[k] = true
		}
		c.vars = vars
		owner := fd
		if owner == nil || !(owner.Pos() <= obj.Pos() && obj.Pos() < owner.End()) {
			owner = p.funcOf(obj.Pos())
		}
		if owner == nil {
			return p.typeSchema(obj.Type(), nil)
		}
		p.localIndex(owner)
		var s schema
		for _, v := range p.locals[owner][obj] {
			s = mergeSchema(s, p.exprSchema(v, owner, c))
		}
		if s == nil {
			s = p.typeSchema(obj.Type(), nil)
		}
		if ks := p.keys[owner][obj]; len(ks) > 0 {
			props, _ := s["properties"].(schema)
			np := schema{}
			for k, v := range props {
				np[k] = v
			}
			for _, kv := range ks {
				vs := p.exprSchema(kv.value, owner, c)
				if as := p.assignComment(kv.pos); as != "" {
					vs = withDescription(vs, as)
				}
				if old, ok := np[kv.key]; ok {
					vs = mergeSchema(old.(schema), vs)
				}
				np[kv.key] = vs
			}
			s = withDescription(s, "")
			s["type"] = "object"
			s["properties"] = np
		}
		if as := p.appends[owner][obj]; len(as) > 0 {
			items, _ := s["items"].(schema)
			for _, a := range as {
				if id, ok := a.(*ast.Ident); ok && id.Name == "_spread" {
					continue
				}
				items = mergeSchema(items, p.exprSchema(a, owner, c))
			}
			s = withDescription(s, "")
			s["type"] = "array"
			s["items"] = items
		}
		return s
	case *ast.CallExpr:
		if g, _ := p.callee(x); g != nil && g.Type.Results != nil && len(g.Type.Results.List) > 0 {
			var s schema
			ast.Inspect(g.Body, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) > 0 {
					if id, ok := r.Results[0].(*ast.Ident); ok && id.Name == "nil" {
						return true
					}
					s = mergeSchema(s, p.exprSchema(r.Results[0], g, c))
				}
				return true
			})
			if s != nil {
				return s
			}
		}
	}
	return p.typeSchema(t, nil)
}

// assignComment: the comment after m["k"] = v on its line.
func (p *apiPkg) assignComment(pos token.Pos) string {
	at := p.fset.Position(pos)
	if cg := p.lineCmt[at.Filename][at.Line]; cg != nil && p.fset.Position(cg.Pos()).Column > at.Column {
		return cleanComment(cg.Text())
	}
	return ""
}

// ---------------------------------------------------------------- the request body a route reads

// commonFieldDocs: what a field name means wherever a request takes it and its declaration has no comment of its own (ids name
// the same things in every request; a monster in a fight has a negative id).
var commonFieldDocs = map[string]string{
	"character_id":  "The id of the character this request is for",
	"character_ids": "The ids of the characters this request is for",
	"campaign_id":   "The id of the campaign this request is for",
	"target_id":     "The target: a character's id, or a monster's negative id from the fight's turn order",
	"target_ids":    "The targets: characters' ids, or monsters' negative ids from the fight's turn order",
	"caster_id":     "The caster: a character's id, or a monster's negative id from the fight's turn order",
	"combatant_id":  "A combatant: a character's id, or a monster's negative id from the fight's turn order",
	"agent_id":      "The account's id (agent_id)",
	"advantage":     "true: the roll has Advantage",
	"disadvantage":  "true: the roll has Disadvantage",
	"dc":            "The Difficulty Class",
	"gm_notes":      "Notes only the GM sees",
}

// dynamicFieldLists: the functions a map request's reader is given its field names by (onlyFields(f()...),
// keysCheckedByHandler(f()...)), each with what describes those fields: the table the names come from, or the request type of
// the handler that decodes them. The names are always the function's own. A reader given another function fails TestAPIShapes
// until it is here.
var dynamicFieldLists = map[string]func(p *apiPkg) map[string]string{
	"tableRuleKeys": func(p *apiPkg) map[string]string {
		out := map[string]string{}
		for _, k := range tableRuleKeys() {
			out[k] = ""
		}
		for _, d := range tableRuleDefs {
			var opts []string
			for _, o := range d.Options {
				opts = append(opts, fmt.Sprintf("%s (%s)", o.Value, o.Label))
			}
			out[d.Key] = fmt.Sprintf("%s: %s; the default is %s", d.Title, strings.Join(opts, ", "), d.Default)
		}
		out["watch_password"] = "A private campaign's watch password (at least 4 characters): it lets people who aren't players read the campaign"
		return out
	},
	"classOptionFieldNames": func(p *apiPkg) map[string]string {
		out := map[string]string{}
		for _, k := range classOptionFieldNames() {
			out[k] = ""
		}
		for _, d := range choiceDefs {
			if d.set != nil && out[d.field] == "" {
				what := d.many
				if what == "" {
					what = d.one
				}
				out[d.field] = fmt.Sprintf("%s: choose %s (GET /api/characters/{id}/setup shows the options)", d.feature, what)
			}
		}
		return out
	},
	"profileFieldNames": func(p *apiPkg) map[string]string {
		out := map[string]string{}
		for _, k := range profileFieldNames() {
			out[k] = ""
		}
		for _, f := range profileTextFields {
			out[f.key] = fmt.Sprintf("The character's %s: text of up to %d characters", f.key, f.limit)
		}
		out["alignment"] = "One of the nine alignments (Lawful Good ... Chaotic Evil)"
		return out
	},
	"createFieldNames": func(p *apiPkg) map[string]string { return p.namesWithCreateDocs(createFieldNames()) },
	"builderFieldNames": func(p *apiPkg) map[string]string {
		out := p.namesWithCreateDocs(builderFieldNames())
		for k, d := range p.literalKeyDocs("builderFields") { // the builder's own fields, described where builderFields lists them
			if out[k] == "" {
				out[k] = d
			}
		}
		return out
	},
}

// namesWithCreateDocs: creation's fields (and the builder's, which takes the same) described by the creation request's own
// type in handleCharacters, else by what createFields says to send.
func (p *apiPkg) namesWithCreateDocs(names []string) map[string]string {
	doc := p.structFieldsIn(p.byName["handleCharacters"])
	out := map[string]string{}
	for _, n := range names {
		out[n] = doc[n].Description
		if out[n] == "" && createFields[n] != "" {
			out[n] = "Send " + createFields[n]
		}
	}
	return out
}

type fieldShape struct {
	Where       string `json:"-"` // where the field is declared
	Name        string `json:"name"`
	Schema      schema `json:"schema"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

type bodyShape struct {
	None     bool         `json:"none,omitempty"` // takes no body: noFields, or no reader at all
	Refused  bool         `json:"refused,omitempty"`
	Required bool         `json:"required"`
	Fields   []fieldShape `json:"fields"`
	AnyKey   bool         `json:"any_key,omitempty"`
	Aliases  []string     `json:"aliases,omitempty"`
}

// readerShape: what one reader call takes.
func (p *apiPkg) readerShape(rc readerCall) (*bodyShape, error) {
	if rc.name == "noFields" {
		return &bodyShape{None: true, Refused: true, Fields: []fieldShape{}}, nil
	}
	args := rc.call.Args
	vi := 2
	switch rc.name {
	case "gmReadAs":
		vi = 3
	case "parseRequest": // a body already read (the portrait's JSON form): parseRequest(raw, &v, bodySpec{...})
		vi = 1
	}
	if len(args) <= vi {
		return nil, fmt.Errorf("%s with too few arguments", rc.name)
	}
	v, opts := args[vi], args[vi+1:]
	specOptional := false
	if rc.name == "parseRequest" {
		if lit, ok := opts[0].(*ast.CompositeLit); ok {
			for _, el := range lit.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok && types.ExprString(kv.Key) == "optional" && types.ExprString(kv.Value) == "true" {
					specOptional = true
				}
			}
		}
		opts = nil
	}
	if rc.call.Ellipsis.IsValid() {
		return nil, fmt.Errorf("%s given its options as a list: the analysis can't read them", rc.name)
	}
	u, ok := v.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return nil, fmt.Errorf("%s reads into %s, not &variable", rc.name, types.ExprString(v))
	}
	vt := p.info.TypeOf(u.X)
	if vt == nil {
		return nil, fmt.Errorf("%s: no type for %s", rc.name, types.ExprString(u.X))
	}
	b := &bodyShape{Required: !specOptional, Fields: []fieldShape{}}
	var varObj types.Object
	if id, ok := u.X.(*ast.Ident); ok {
		varObj = p.info.Uses[id]
	}
	jsonName := map[*types.Var]string{}
	var mapNames []string
	mapDocs := map[string]string{}
	isMap := false
	switch st := vt.Underlying().(type) {
	case *types.Struct:
		p.eachJSONField(st, func(name string, f *types.Var) {
			jsonName[f] = name
			b.Fields = append(b.Fields, fieldShape{Name: name, Schema: p.typeSchema(f.Type(), nil), Description: p.fieldDoc[f.Pos()], Where: p.fset.Position(f.Pos()).String()})
		})
	case *types.Map:
		isMap = true
	default:
		return nil, fmt.Errorf("%s reads into a %s", rc.name, vt)
	}
	for _, o := range opts {
		oc, ok := o.(*ast.CallExpr)
		if !ok {
			return nil, fmt.Errorf("%s: option %s isn't a call", rc.name, types.ExprString(o))
		}
		_, on := p.callee(oc)
		switch on {
		case "optionalBody":
			b.Required = false
		case "onlyFields", "keysCheckedByHandler":
			if on == "keysCheckedByHandler" {
				b.AnyKey = true
			}
			for _, a := range oc.Args {
				if s, ok := p.constString(a); ok {
					mapNames = append(mapNames, s)
					if d := p.trailingComment(a, false); d != "" { // onlyFields(\n "set", // what it is\n ...)
						mapDocs[s] = d
					}
					continue
				}
				if call, ok := a.(*ast.CallExpr); ok && oc.Ellipsis.IsValid() {
					if _, fname := p.callee(call); dynamicFieldLists[fname] != nil {
						for n, d := range dynamicFieldLists[fname](p) {
							mapNames = append(mapNames, n)
							if d != "" {
								mapDocs[n] = d
							}
						}
						continue
					}
				}
				return nil, fmt.Errorf("%s: %s(%s): add the function to dynamicFieldLists", rc.name, on, types.ExprString(a))
			}
		case "fieldAliases":
			if len(oc.Args) == 1 {
				if call, ok := oc.Args[0].(*ast.CallExpr); ok {
					if _, fname := p.callee(call); fname == "classOptionAliases" {
						for a := range classOptionAliases() {
							b.Aliases = append(b.Aliases, a)
						}
						sort.Strings(b.Aliases)
						continue
					}
				}
			}
			return nil, fmt.Errorf("%s: fieldAliases(%s) isn't known to the analysis", rc.name, types.ExprString(oc))
		case "alsoField":
			name, _ := p.constString(oc.Args[0])
			b.Fields = append(b.Fields, fieldShape{Name: name, Schema: p.typeSchema(p.info.TypeOf(oc.Args[1]).(*types.Pointer).Elem(), nil),
				Description: p.trailingComment(oc, false)})
		case "bodyHint", "targetsIn", "campaignFromTarget", "campaignBeforeStart", "campaignOf", "notGMHint":
		default:
			return nil, fmt.Errorf("%s: option %s isn't known to the analysis", rc.name, types.ExprString(oc))
		}
	}
	if rc.name == "gmRead" || rc.name == "gmReadAs" { // the campaign a GM command is for (gm_campaign_2024.go)
		cf := p.gmCampaignField()
		declared := false
		for i := range b.Fields {
			if b.Fields[i].Name == cf.Name { // the request declares it too: one field
				declared = true
				if b.Fields[i].Description == "" {
					b.Fields[i].Description = cf.Description
				}
			}
		}
		if !declared {
			b.Fields = append(b.Fields, cf)
		}
	}
	if isMap {
		elem := vt.Underlying().(*types.Map).Elem()
		doc := p.structFieldsIn(rc.fn) // a map read is described by the struct the handler decodes it into, when there is one
		decoded := p.mapKeysDecoded(rc.fn, varObj)
		for _, n := range mapNames {
			f := fieldShape{Name: n, Schema: p.typeSchema(elem, nil)}
			if d, ok := doc[n]; ok {
				f.Schema, f.Description = d.Schema, d.Description
			} else if d, ok := decoded[n]; ok {
				f.Schema, f.Description = d.Schema, d.Description
			}
			if f.Description == "" {
				f.Description = mapDocs[n]
			}
			b.Fields = append(b.Fields, f)
		}
	}
	// the fields the handler refuses when they are missing: a check right after the read, on the read value alone
	if varObj != nil && !isMap {
		required := p.requiredAfter(rc.rest, varObj, jsonName)
		for i := range b.Fields {
			if required[b.Fields[i].Name] {
				b.Fields[i].Required = true
			}
		}
	}
	for i := range b.Fields {
		if b.Fields[i].Description == "" {
			b.Fields[i].Description = commonFieldDocs[b.Fields[i].Name]
		}
	}
	sort.Slice(b.Fields, func(i, j int) bool { return b.Fields[i].Name < b.Fields[j].Name })
	return b, nil
}

// gmCampaignField: campaign_id, the envelope field gmReadAs adds to every GM command, with the comment written at that call.
func (p *apiPkg) gmCampaignField() fieldShape {
	f := fieldShape{Name: "campaign_id", Schema: schema{"type": "integer"}}
	if fd := p.byName["gmReadAs"]; fd != nil {
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if _, name := p.callee(call); name == "alsoField" {
					f.Description = p.trailingComment(call, false)
				}
			}
			return true
		})
	}
	return f
}

// structFieldsIn: the JSON fields of the structs declared in a function, by name.
func (p *apiPkg) structFieldsIn(fd *ast.FuncDecl) map[string]fieldShape {
	out := map[string]fieldShape{}
	if fd == nil {
		return out
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		if t, ok := p.info.TypeOf(st).(*types.Struct); ok {
			p.eachJSONField(t, func(name string, f *types.Var) {
				if _, dup := out[name]; !dup {
					out[name] = fieldShape{Name: name, Schema: p.typeSchema(f.Type(), nil), Description: p.fieldDoc[f.Pos()]}
				}
			})
		}
		return false
	})
	return out
}

// requiredAfter: the fields a handler refuses when they are missing, by a check among the statements right after the read
// (an if whose condition is only "this field is empty", or several such joined by ||, and whose body answers and returns).
// A field the handler gives a value first (a default) is not required.
func (p *apiPkg) requiredAfter(rest []ast.Stmt, v types.Object, names map[*types.Var]string) map[string]bool {
	out := map[string]bool{}
	given := map[string]bool{}
	fieldOf := func(e ast.Expr) string {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || p.info.Uses[id] != v {
			return ""
		}
		if fv, ok := p.info.Uses[sel.Sel].(*types.Var); ok {
			return names[fv]
		}
		return ""
	}
	isZero := func(e ast.Expr) bool {
		if id, ok := e.(*ast.Ident); ok && id.Name == "nil" {
			return true
		}
		cv := p.constValue(e)
		if cv == nil {
			return false
		}
		switch cv.Kind() {
		case constant.Int:
			n, _ := constant.Int64Val(cv)
			return n == 0
		case constant.String:
			return constant.StringVal(cv) == ""
		}
		return false
	}
	var emptyTest func(e ast.Expr) []string
	emptyTest = func(e ast.Expr) []string {
		switch x := e.(type) {
		case *ast.ParenExpr:
			return emptyTest(x.X)
		case *ast.BinaryExpr:
			if x.Op == token.LOR {
				a, b := emptyTest(x.X), emptyTest(x.Y)
				if a == nil || b == nil {
					return nil
				}
				return append(a, b...)
			}
			if x.Op == token.EQL || x.Op == token.LEQ {
				lhs := x.X
				if call, ok := lhs.(*ast.CallExpr); ok && len(call.Args) == 1 {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "len" {
						lhs = call.Args[0]
					} else if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "TrimSpace" {
						lhs = call.Args[0]
					}
				}
				if f := fieldOf(lhs); f != "" && isZero(x.Y) {
					return []string{f}
				}
			}
		}
		return nil
	}
	for _, s := range rest {
		if is, ok := s.(*ast.IfStmt); ok && is.Init == nil && is.Else == nil && len(is.Body.List) > 0 {
			if _, returns := is.Body.List[len(is.Body.List)-1].(*ast.ReturnStmt); returns && len(is.Body.List) > 1 {
				for _, f := range emptyTest(is.Cond) {
					if !given[f] {
						out[f] = true
					}
				}
			}
		}
		ast.Inspect(s, func(n ast.Node) bool { // a value given to a field afterwards is a default
			if as, ok := n.(*ast.AssignStmt); ok {
				for _, l := range as.Lhs {
					if f := fieldOf(l); f != "" {
						given[f] = true
					}
				}
			}
			return true
		})
	}
	return out
}

// ---------------------------------------------------------------- the shapes of every operation

type replyShape struct {
	Description string `json:"description"`
	ContentType string `json:"content_type"`
	Schema      schema `json:"schema"`
}

type opShape struct {
	Method    string                `json:"method"`
	Path      string                `json:"path"`
	Func      string                `json:"func"`
	Request   *bodyShape            `json:"request"`
	Responses map[string]replyShape `json:"responses"`
	Problems  []string              `json:"problems,omitempty"`
}

func (p *apiPkg) shapeOf(fn string, rt apiRoute) opShape {
	w := p.walkRoute(fn, rt)
	o := opShape{Method: rt.Method, Path: rt.Path, Func: fn, Responses: map[string]replyShape{}}
	if p.byName[fn] == nil {
		o.Problems = append(o.Problems, "no handler function under the godoc block")
		return o
	}
	if rt.Method != "get" && rt.Method != "head" {
		var shapes []*bodyShape
		seen := map[token.Pos]bool{}
		for _, rc := range w.readers {
			if seen[rc.call.Pos()] {
				continue
			}
			seen[rc.call.Pos()] = true
			b, err := p.readerShape(rc)
			if err != nil {
				o.Problems = append(o.Problems, p.fset.Position(rc.call.Pos()).String()+": "+err.Error())
				continue
			}
			shapes = append(shapes, b)
		}
		switch {
		case len(shapes) == 0:
			o.Request = &bodyShape{None: true, Fields: []fieldShape{}}
		default:
			o.Request = mergeBodies(shapes)
		}
	}
	for _, rw := range w.replies {
		if rw.status >= 300 || (rw.status != 0 && rw.status < 200) {
			continue
		}
		if id, ok := rw.payload.(*ast.Ident); ok {
			if obj := p.info.Uses[id]; obj != nil && rw.fn != nil && isParam(rw.fn, obj, p.info) {
				continue // a writer given its reply by its caller (writeJSONStatus's own body ...)
			}
		}
		s := p.exprSchema(rw.payload, rw.fn, exprCtx{})
		if props, ok := s["properties"].(schema); ok {
			if _, isErr := props["error"]; isErr {
				if _, ok := props["success"]; !ok {
					continue
				}
			}
		}
		code := "200"
		if rw.status != 0 {
			code = strconv.Itoa(rw.status)
		}
		r := o.Responses[code]
		r.ContentType = "application/json"
		r.Schema = mergeSchema(r.Schema, s)
		o.Responses[code] = r
	}
	if len(o.Responses) == 0 {
		var cts []string
		for ct := range w.ctypes {
			if ct != "application/json" {
				cts = append(cts, ct)
			}
		}
		sort.Strings(cts)
		if len(cts) == 0 && w.text {
			cts = []string{"text/plain"}
		}
		if len(cts) > 0 {
			sc := schema{"type": "string"}
			if strings.HasPrefix(cts[0], "image/") {
				sc["format"] = "binary"
			}
			o.Responses["200"] = replyShape{ContentType: cts[0], Schema: sc}
		}
	}
	if extra := p.augmentedKeys(fn, rt); len(extra) > 0 { // keys a wrapper adds to the handler's reply (augmentJSON)
		for code, r := range o.Responses {
			if r.ContentType != "application/json" || r.Schema["type"] != "object" {
				continue
			}
			r.Schema = mergeSchema(r.Schema, schema{"type": "object", "properties": extra})
			o.Responses[code] = r
		}
	}
	for _, name := range w.forwards { // a route that hands its request to another route's handler answers with its reply
		if len(o.Responses) > 0 || p.forwarding[name] {
			break
		}
		routes := p.handlers[name]
		pick := routes[0]
		for _, r := range routes {
			if r.Method == rt.Method {
				pick = r
				break
			}
		}
		p.forwarding[name] = true
		o.Responses = p.shapeOf(name, pick).Responses
		p.forwarding[name] = false
	}
	if len(o.Responses) == 0 {
		o.Problems = append(o.Problems, "no success reply found")
		for _, u := range w.unfollow {
			o.Problems = append(o.Problems, "not followed: "+u)
		}
	}
	return o
}

// mergeBodies: a route that reads its body in more than one way (by case) takes the fields of each.
func mergeBodies(l []*bodyShape) *bodyShape {
	if len(l) == 1 {
		return l[0]
	}
	out := &bodyShape{Required: true, Fields: []fieldShape{}}
	byName := map[string]int{}
	allNone := true
	for _, b := range l {
		if !b.None {
			allNone = false
		}
		if !b.Required || b.None {
			out.Required = false
		}
		out.AnyKey = out.AnyKey || b.AnyKey
		for _, f := range b.Fields {
			if i, ok := byName[f.Name]; ok {
				out.Fields[i].Required = out.Fields[i].Required && f.Required
				if out.Fields[i].Description == "" {
					out.Fields[i].Description = f.Description
				}
				continue
			}
			f.Required = false // required in one way of reading, not in the others
			byName[f.Name] = len(out.Fields)
			out.Fields = append(out.Fields, f)
		}
	}
	out.None = allNone
	sort.Slice(out.Fields, func(i, j int) bool { return out.Fields[i].Name < out.Fields[j].Name })
	return out
}

// apiShapes: every operation the godoc blocks document, with its request and reply.
func apiShapes(t testing.TB) []opShape {
	p := loadAPIPackage(t)
	var out []opShape
	for _, o := range p.ops {
		for _, rt := range o.Routes {
			out = append(out, p.shapeOf(o.Func, rt))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// findReplyHelpers finds the functions that write the JSON reply they are given, from writeJSONStatus and
// json.NewEncoder(w).Encode upwards, so a route's reply is read where the handler builds it.
func (p *apiPkg) findReplyHelpers() {
	p.helpers = map[string]replyHelper{"writeJSONStatus": {payload: 2, status: 1}}
	paramIndex := func(fd *ast.FuncDecl, e ast.Expr) int {
		id, ok := e.(*ast.Ident)
		if !ok {
			return -1
		}
		obj := p.info.Uses[id]
		i := 0
		for _, fl := range fd.Type.Params.List {
			for _, n := range fl.Names {
				if p.info.Defs[n] == obj {
					return i
				}
				i++
			}
		}
		return -1
	}
	for changed := true; changed; {
		changed = false
		for _, fd := range p.funcs {
			name := fd.Name.Name
			if _, done := p.helpers[name]; done || len(p.handlers[name]) > 0 {
				continue
			}
			var statusExpr ast.Expr
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var payload, status ast.Expr
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "WriteHeader":
						if len(call.Args) == 1 && p.isHTTPType(p.info.TypeOf(sel.X)) {
							statusExpr = call.Args[0]
						}
					case "Encode":
						if inner, ok := sel.X.(*ast.CallExpr); ok && len(call.Args) == 1 {
							if ns, ok := inner.Fun.(*ast.SelectorExpr); ok && ns.Sel.Name == "NewEncoder" && len(inner.Args) == 1 && p.isHTTPType(p.info.TypeOf(inner.Args[0])) {
								payload, status = call.Args[0], statusExpr
							}
						}
					}
				} else if _, cn := p.callee(call); cn != "" {
					if h, ok := p.helpers[cn]; ok && len(call.Args) > h.payload {
						payload = call.Args[h.payload]
						if h.status >= 0 && h.status < len(call.Args) {
							status = call.Args[h.status]
						}
					}
				}
				if payload == nil {
					return true
				}
				pi := paramIndex(fd, payload)
				if pi < 0 {
					return true
				}
				h := replyHelper{payload: pi, status: -1}
				if status != nil {
					if si := paramIndex(fd, status); si >= 0 {
						h.status = si
					} else {
						h.statusConst = p.constInt(status)
					}
				}
				p.helpers[name] = h
				changed = true
				return false
			})
		}
	}
}

// mapKeysDecoded: the keys of a map request the handler decodes itself (json.Unmarshal(raw["steps"], &steps), or of a value
// taken out first: v, ok := raw["character_id"]), with the type each is decoded into and the comment on that line.
func (p *apiPkg) mapKeysDecoded(fd *ast.FuncDecl, m types.Object) map[string]fieldShape {
	out := map[string]fieldShape{}
	if fd == nil || m == nil {
		return out
	}
	keyOf := func(e ast.Expr) (string, bool) {
		ix, ok := e.(*ast.IndexExpr)
		if !ok {
			return "", false
		}
		id, ok := ix.X.(*ast.Ident)
		if !ok || p.info.Uses[id] != m {
			return "", false
		}
		return p.constString(ix.Index)
	}
	taken := map[types.Object]string{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Rhs) == 1 && len(as.Lhs) >= 1 {
			if k, ok := keyOf(as.Rhs[0]); ok {
				if id, ok := as.Lhs[0].(*ast.Ident); ok {
					if obj := p.info.Defs[id]; obj != nil {
						taken[obj] = k
					}
				}
			}
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Unmarshal" {
			return true
		}
		k, ok := keyOf(call.Args[0])
		if !ok {
			if id, isID := call.Args[0].(*ast.Ident); isID {
				k, ok = taken[p.info.Uses[id]]
			}
		}
		if !ok {
			return true
		}
		if _, dup := out[k]; dup {
			return true
		}
		if pt, isPtr := p.info.TypeOf(call.Args[1]).(*types.Pointer); isPtr {
			out[k] = fieldShape{Name: k, Schema: p.typeSchema(pt.Elem(), nil), Description: p.trailingComment(call, false)}
		}
		return true
	})
	return out
}

// bodyRefusalSchema: the reply of a refused body, from bodyProblem.reply (request_body_2024.go).
func (p *apiPkg) bodyRefusalSchema() schema {
	for _, f := range p.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Name.Name != "reply" || !strings.Contains(types.ExprString(fd.Recv.List[0].Type), "bodyProblem") {
				continue
			}
			var out schema
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) == 1 {
					out = p.exprSchema(r.Results[0], fd, exprCtx{})
				}
				return true
			})
			return out
		}
	}
	return nil
}

// noBodyNote is what the reference says of a request that takes no body (build_swagger.py writes it; TestAPIShapes checks it).
const noBodyNote = "Takes no request body."

// The API reference documents every operation from the code that serves it: the request body its reader takes (each field,
// its type, its description, required or not), that it takes none, and the reply it sends. With API_SHAPES_OUT set this writes
// the analysis for tools/build_swagger.py; otherwise it checks the built reference (docs/swagger/swagger.json) against it.
func TestAPIShapes(t *testing.T) {
	p := loadAPIPackage(t)
	shapes := apiShapes(t)
	if out := os.Getenv("API_SHAPES_OUT"); out != "" {
		doc := map[string]interface{}{"godoc": p.ops, "operations": shapes, "body_refusal": p.bodyRefusalSchema(), "no_body_note": noBodyNote}
		b, err := json.MarshalIndent(doc, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Description string     `json:"description"`
			Parameters  []apiParam `json:"parameters"`
			RequestBody *struct {
				Required bool `json:"required"`
				Content  map[string]struct {
					Schema schema `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
			Responses map[string]struct {
				Content map[string]struct {
					Schema schema `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(swaggerJSON, &spec); err != nil {
		t.Fatal(err)
	}
	rebuild := " (run python3 tools/build_swagger.py)"
	analysed := map[string]bool{}
	bodyNote := map[string]string{}
	for _, o := range p.ops {
		if o.Func == "" {
			t.Errorf("%s:%d: the godoc block sits above no function", o.file, o.line)
		}
		for _, r := range o.Routes {
			bodyNote[r.Method+" "+r.Path] = o.BodyNote
		}
	}
	blockReads := map[string]bool{} // a godoc block's @Body is about the routes of the block that read a body
	for _, sh := range shapes {
		if sh.Request != nil && !sh.Request.None {
			blockReads[sh.Func] = true
		}
	}
	for _, sh := range shapes {
		key := strings.ToUpper(sh.Method) + " " + sh.Path
		analysed[sh.Method+" "+sh.Path] = true
		for _, pr := range sh.Problems {
			t.Errorf("%s (%s): %s", key, sh.Func, pr)
		}
		op, ok := spec.Paths[sh.Path][sh.Method]
		if !ok {
			t.Errorf("%s: documented by its godoc but not in the reference%s", key, rebuild)
			continue
		}
		if strings.TrimSpace(op.Description) == "" {
			t.Errorf("%s: no description", key)
		}
		for _, prm := range op.Parameters {
			if strings.TrimSpace(prm.Description) == "" {
				t.Errorf("%s: parameter %s has no description", key, prm.Name)
			}
		}
		// the request body
		switch {
		case sh.Request == nil: // a read
		case sh.Request.None:
			if op.RequestBody != nil {
				t.Errorf("%s: the reference shows a request body, but the route reads none%s", key, rebuild)
			}
			if !strings.Contains(op.Description, noBodyNote) {
				t.Errorf("%s: the reference doesn't say it takes no body%s", key, rebuild)
			}
			if bodyNote[sh.Method+" "+sh.Path] != "" && !blockReads[sh.Func] {
				t.Errorf("%s: its godoc describes a body (@Body), but none of its routes reads one: remove the @Body line or read the body", key)
			}
		default:
			if op.RequestBody == nil {
				t.Errorf("%s: the route reads a body the reference doesn't show%s", key, rebuild)
				break
			}
			if op.RequestBody.Required != sh.Request.Required {
				t.Errorf("%s: the reference says the body is required=%v, the reader says %v%s", key, op.RequestBody.Required, sh.Request.Required, rebuild)
			}
			got := op.RequestBody.Content["application/json"].Schema
			props, _ := got["properties"].(map[string]interface{})
			var req []string
			for _, r := range asList(got["required"]) {
				req = append(req, fmt.Sprint(r))
			}
			sort.Strings(req)
			var wantReq []string
			for _, f := range sh.Request.Fields {
				ps, ok := props[f.Name].(map[string]interface{})
				if !ok {
					t.Errorf("%s: field %s, which the reader takes, isn't in the reference%s", key, f.Name, rebuild)
					continue
				}
				if fmt.Sprint(ps["type"]) != fmt.Sprint(f.Schema["type"]) {
					t.Errorf("%s: field %s is %v in the reference, %v in the code%s", key, f.Name, ps["type"], f.Schema["type"], rebuild)
				}
				if strings.TrimSpace(f.Description) == "" {
					where := f.Where
					if where == "" {
						where = "the reader's call in " + sh.Func
					}
					t.Errorf("%s: field %s has no description: write a comment after it (%s)", key, f.Name, where)
				} else if d, _ := ps["description"].(string); !strings.HasPrefix(d, f.Description) { // the builder may add to it (the action types)
					t.Errorf("%s: field %s: the reference's description differs from the code's%s", key, f.Name, rebuild)
				}
				if f.Required {
					wantReq = append(wantReq, f.Name)
				}
			}
			for name := range props {
				found := false
				for _, f := range sh.Request.Fields {
					found = found || f.Name == name
				}
				if !found {
					t.Errorf("%s: the reference shows field %s, which the reader doesn't take%s", key, name, rebuild)
				}
			}
			sort.Strings(wantReq)
			if strings.Join(req, ",") != strings.Join(wantReq, ",") {
				t.Errorf("%s: the reference requires [%s], the handler requires [%s]%s", key, strings.Join(req, ", "), strings.Join(wantReq, ", "), rebuild)
			}
		}
		// the reply
		documented := false
		for code, r := range op.Responses {
			if !strings.HasPrefix(code, "2") {
				continue
			}
			for ct, c := range r.Content {
				want, ok := sh.Responses[code]
				if !ok || want.ContentType != ct {
					t.Errorf("%s: the reference documents a %s %s reply the code doesn't send%s", key, code, ct, rebuild)
					continue
				}
				a, _ := json.Marshal(c.Schema)
				b, _ := json.Marshal(want.Schema)
				if string(a) != string(b) {
					t.Errorf("%s: the %s reply in the reference differs from what the code sends%s", key, code, rebuild)
				}
				documented = true
			}
		}
		if !documented {
			t.Errorf("%s: no documented reply%s", key, rebuild)
		}
	}
	for path, ops := range spec.Paths {
		for m := range ops {
			if !analysed[m+" "+path] {
				t.Errorf("%s %s is in the reference but no godoc block's @Router names it: give its handler one", strings.ToUpper(m), path)
			}
		}
	}
	// a route served under a prefix (/api/universe/species/) is documented under that prefix
	main, _ := os.ReadFile("main.go")
	for _, m := range regexp.MustCompile(`HandleFunc\("(/api/[^"]*/)"`).FindAllStringSubmatch(string(main), -1) {
		found := false
		for path := range spec.Paths {
			if strings.HasPrefix(path, m[1]) {
				found = true
			}
		}
		if !found {
			t.Errorf("the router serves the paths under %s, but the reference documents none of them: give the handler a godoc block (@Router)", m[1])
		}
	}
}

// The analysis reads a request type's fields as the reader does: checked on the types every request reads into.
func TestAPIShapesReadFieldsAsTheReader(t *testing.T) {
	p := loadAPIPackage(t)
	for name, v := range map[string]interface{}{"actionRequest": &actionRequest{}, "mapRequest": &mapRequest{}} {
		var got []string
		for id, o := range p.info.Defs {
			if id.Name == name && o != nil {
				if tn, ok := o.(*types.TypeName); ok {
					if st, ok := tn.Type().Underlying().(*types.Struct); ok {
						p.eachJSONField(st, func(n string, _ *types.Var) { got = append(got, n) })
					}
				}
			}
		}
		sortFields(got)
		if want := requestFields(v); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: the analysis reads %v, the reader %v", name, got, want)
		}
	}
}

// ---------------------------------------------------------------- keys added to a route's reply by a wrapper (augmentJSON)

// augmentSite: an augmentJSON call (reminders_2024.go) that adds keys to the reply of a route's handler: the function it is in,
// the call (reached or not by the route's method and path), and the keys its add function sets.
type augmentSite struct {
	in   *ast.FuncDecl
	call *ast.CallExpr
	keys schema
}

// callSites: every call of a package function, by name.
func (p *apiPkg) callSites() map[string][]*ast.CallExpr {
	if p.calls != nil {
		return p.calls
	}
	p.calls = map[string][]*ast.CallExpr{}
	for _, f := range p.files {
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if _, name := p.callee(call); name != "" {
					p.calls[name] = append(p.calls[name], call)
				}
				if inner, ok := call.Fun.(*ast.CallExpr); ok { // withRestReminders(handleShortRest, true)(w, r, id)
					if _, name := p.callee(inner); name != "" {
						p.calls[name] = append(p.calls[name], inner)
					}
				}
			}
			return true
		})
	}
	return p.calls
}

func (p *apiPkg) paramObjs(ft *ast.FuncType) []types.Object {
	var out []types.Object
	for _, fl := range ft.Params.List {
		for _, n := range fl.Names {
			out = append(out, p.info.Defs[n])
		}
	}
	return out
}

// keysSetOn: the keys set on a map (a parameter of fn or of a function literal): m["k"] = v, in the functions it is handed to,
// and in the function value it is handed to when that value is a parameter (the callers' literals).
func (p *apiPkg) keysSetOn(body *ast.BlockStmt, m types.Object, owner *ast.FuncDecl, depth int) schema {
	props := schema{}
	if body == nil || m == nil || depth > 3 {
		return props
	}
	add := func(more schema) {
		for k, v := range more {
			if old, ok := props[k]; ok {
				props[k] = mergeSchema(old.(schema), v.(schema))
			} else {
				props[k] = v
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, l := range x.Lhs {
				ix, ok := l.(*ast.IndexExpr)
				if !ok || len(x.Rhs) != len(x.Lhs) {
					continue
				}
				id, ok := ix.X.(*ast.Ident)
				if !ok || p.info.Uses[id] != m {
					continue
				}
				if k, ok := p.constString(ix.Index); ok {
					vs := p.exprSchema(x.Rhs[i], owner, exprCtx{})
					if d := p.assignComment(ix.Pos()); d != "" {
						vs = withDescription(vs, d)
					}
					add(schema{k: vs})
				}
			}
		case *ast.CallExpr:
			for ai, a := range x.Args {
				id, ok := a.(*ast.Ident)
				if !ok || p.info.Uses[id] != m {
					continue
				}
				if g, _ := p.callee(x); g != nil {
					if ps := p.paramObjs(g.Type); ai < len(ps) {
						add(p.keysSetOn(g.Body, ps[ai], g, depth+1))
					}
					continue
				}
				// a function value that is a parameter of owner: what the callers hand in
				fid, ok := x.Fun.(*ast.Ident)
				if !ok || owner == nil {
					continue
				}
				ps := p.paramObjs(owner.Type)
				for pi, po := range ps {
					if po == nil || p.info.Uses[fid] != po {
						continue
					}
					for _, site := range p.callSites()[owner.Name.Name] {
						if pi >= len(site.Args) {
							continue
						}
						if lit, ok := site.Args[pi].(*ast.FuncLit); ok {
							if lps := p.paramObjs(lit.Type); ai < len(lps) {
								add(p.keysSetOn(lit.Body, lps[ai], p.funcOf(site.Pos()), depth+1))
							}
						}
					}
				}
			}
		}
		return true
	})
	return props
}

// augmentsOf: the augmentJSON calls that wrap a route handler, with the keys they add.
func (p *apiPkg) augmentsOf(handler string) []augmentSite {
	if p.augments == nil {
		p.augments = map[string][]augmentSite{}
		for _, call := range p.callSites()["augmentJSON"] {
			if len(call.Args) != 3 {
				continue
			}
			in := p.funcOf(call.Pos())
			run, ok1 := call.Args[1].(*ast.FuncLit)
			add, ok2 := call.Args[2].(*ast.FuncLit)
			if !ok1 || !ok2 || in == nil {
				continue
			}
			var keys schema
			if ps := p.paramObjs(add.Type); len(ps) == 1 && ps[0] != nil {
				keys = p.keysSetOn(add.Body, ps[0], in, 0)
			}
			targets := map[string]bool{}
			ast.Inspect(run.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if _, name := p.callee(c); len(p.handlers[name]) > 0 {
					targets[name] = true
				}
				fid, ok := c.Fun.(*ast.Ident) // h(rw, r): the handler the wrapper is given
				if !ok {
					return true
				}
				for pi, po := range p.paramObjs(in.Type) {
					if po == nil || p.info.Uses[fid] != po {
						continue
					}
					for _, site := range p.callSites()[in.Name.Name] {
						if pi < len(site.Args) {
							if id := innermostIdent(site.Args[pi]); len(p.handlers[id]) > 0 {
								targets[id] = true
							}
						}
					}
				}
				return true
			})
			for h := range targets {
				p.augments[h] = append(p.augments[h], augmentSite{in: in, call: call, keys: keys})
			}
		}
	}
	return p.augments[handler]
}

// augmentedKeys: the keys the wrappers of a route's handler add to its reply, where the wrapper reaches its augmentJSON call with
// the route's method and path.
func (p *apiPkg) augmentedKeys(fn string, rt apiRoute) schema {
	props := schema{}
	for _, a := range p.augmentsOf(fn) {
		w := p.walkRoute(fn, rt, a.in)
		if !w.calls[a.call.Pos()] {
			continue
		}
		for k, v := range a.keys {
			props[k] = v
		}
	}
	return props
}

// literalKeyDocs: the comment written after each key of a package variable's map literal.
func (p *apiPkg) literalKeyDocs(name string) map[string]string {
	out := map[string]string{}
	for _, f := range p.files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || vs.Names[0].Name != name || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				for i, el := range lit.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					last := i == len(lit.Elts)-1 || p.fset.Position(lit.Elts[i+1].Pos()).Line > p.fset.Position(kv.End()).Line
					if k, ok := p.constString(kv.Key); ok && last {
						if c := p.trailingComment(kv, false); c != "" {
							out[k] = c
						}
					}
				}
			}
		}
	}
	return out
}
