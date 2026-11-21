package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMonsterBookCoverage reads every stat block in MM_FIXTURES_DIR (written by tools/parse_mm_statblocks.py from the supplied
// Monster Manual text; skipped when the variable is unset) and reports, for each, whether the shared rules accept the import and
// what happens to each attack and action. It distinguishes three outcomes, so a passing import is never read as coverage:
//
//	automated  an "Attack Roll" action the shared parser fully represents (every damage clause rolled)
//	refused    an attack action the parser will not roll (the GM is told why; nothing is rolled)
//	GM         everything else (saving-throw actions, spellcasting, traits, legendary actions): the GM adjudicates
//
// MM_COVERAGE_OUT, when set, receives the report as Markdown. The test fails only on an invariant: an attack counted as automated
// must have a damage amount to roll.
func TestMonsterBookCoverage(t *testing.T) {
	dir := os.Getenv("MM_FIXTURES_DIR")
	if dir == "" {
		t.Skip("MM_FIXTURES_DIR not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no fixtures in %s", dir)
	}
	var rows []string
	refusedReasons := map[string]int{}
	var imports, rejected, auto, refused, saves, other int
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Monsters []srdMonster2024 `json:"monsters"`
		}
		if json.Unmarshal(raw, &doc) != nil || len(doc.Monsters) != 1 {
			t.Fatalf("%s: unreadable fixture", f)
		}
		m := doc.Monsters[0]
		if problem := validateCustomMonster(m); problem != "" {
			rejected++
			rows = append(rows, fmt.Sprintf("| %s | CR %s | IMPORT REJECTED | %s |", m.Name, m.CR, problem))
			continue
		}
		imports++
		var a, r, s, o int
		var why []string
		for _, group := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			for _, e := range group {
				switch {
				case strings.Contains(e.Text, "Attack Roll:"):
					p := parseMonsterAttack(e.Name, e.Text)
					if p.Problem != "" {
						r++
						refusedReasons[firstSentence(p.Problem)]++
						why = append(why, e.Name+": "+firstSentence(p.Problem))
					} else {
						if p.DamageDice == "" && p.FlatDamage == nil && len(p.Choices) == 0 && len(p.Extras) == 0 {
							t.Errorf("%s / %s counted as automated but has no damage to roll", m.Name, e.Name)
						}
						a++
					}
				case strings.Contains(e.Text, "Saving Throw:"):
					s++
				default:
					o++
				}
			}
		}
		auto, refused, saves, other = auto+a, refused+r, saves+s, other+o
		rows = append(rows, fmt.Sprintf("| %s | CR %s | imported | attacks automated %d, refused %d; save actions (GM) %d; other actions (GM) %d%s |",
			m.Name, m.CR, a, r, s, o, joinWhy(why)))
	}
	summary := fmt.Sprintf("%d stat blocks: %d imported, %d import rejected. Actions: %d attack rolls automated, %d attack rolls refused (GM told, nothing rolled), %d saving-throw actions and %d other actions left to the GM.",
		len(files), imports, rejected, auto, refused, saves, other)
	t.Log(summary)
	if out := os.Getenv("MM_COVERAGE_OUT"); out != "" {
		var b strings.Builder
		b.WriteString("# Monster Manual coverage report\n\n" + summary + "\n\n")
		b.WriteString("Imported does not mean automated: saving-throw actions, spellcasting, legendary actions and traits stay with the GM unless a shared rule handles them.\n\n")
		b.WriteString("## Why attacks were refused\n\n")
		var keys []string
		for k := range refusedReasons {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- %d x %s\n", refusedReasons[k], k)
		}
		b.WriteString("\n## Per monster\n\n| Monster | CR | Import | Actions |\n|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n")
		if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func firstSentence(s string) string {
	if i := strings.Index(s, "; the GM"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func joinWhy(w []string) string {
	if len(w) == 0 {
		return ""
	}
	return "; refused: " + strings.Join(w, " / ")
}
