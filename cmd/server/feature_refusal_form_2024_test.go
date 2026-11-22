package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A feature's refusal is told in the form isRefusalResult reads ("Cannot use <feature>: <why>"): only that form spends nothing in a fight, uses no
// exploration beat and answers 409 action_refused. A resolver that refused in another wording ("You've already used Action Surge", "Superior Defense
// is already active", "You can't use Steady Aim if you have moved") was played as an action: success, its cost spent (feature action sweep, Nov 22).
// This reads every function of the server that returns one string and fails on a returned literal that reads as a refusal in another form.
// refusalForm is its pattern; the functions below answer a setup or a character-creation request, not an action, and say it in their own words.
var notActionRefusals = map[string]string{
	"builder_checks_2024.go":  "character creation: a refusal of a request, answered 400",
	"class_languages_2024.go": "a setup request",
	"items_c8_2024.go":        "the Dragon Orb: tells the player how to cast, a success",
	"worn_items_2024.go":      "an equip request",
	"equipped_weapons.go":     "weapon_not_equipped: its own 400 with the hint that no action was used",
	"map_rules.go":            "a map refusal: its own 4xx, \"Your action was not used\"",
	"battle_map.go":           "a battle map move's reason, answered 400",
}

func TestFeatureRefusalsUseTheRefusalForm(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	var bad []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || notActionRefusals[file] != "" {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
				continue
			}
			if id, ok := fd.Type.Results.List[0].Type.(*ast.Ident); !ok || id.Name != "string" {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				rs, ok := n.(*ast.ReturnStmt)
				if !ok || len(rs.Results) != 1 {
					return true
				}
				e := rs.Results[0]
				if c, ok := e.(*ast.CallExpr); ok && len(c.Args) > 0 {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" {
						e = c.Args[0]
					}
				}
				if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
					s, _ := strconv.Unquote(b.Value)
					if refusalWording.MatchString(s) {
						bad = append(bad, file+": "+fd.Name.Name+": "+s)
					}
				}
				return true
			})
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("a refusal that isn't in the refusal form (\"Cannot <feature>: ...\", featureRefusal): %s", b)
	}
}
