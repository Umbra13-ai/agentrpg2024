package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var saveTextDamageRe = regexp.MustCompile(`\d+ \(\d+d\d+(?:\s*[+−-]\s*\d+)?\) [A-Za-z]+ damage`)

// TestMonsterBookSaveActions reads every "Saving Throw:" action in MM_FIXTURES_DIR and compares what parseSaveEffect represents with
// what the text says, without playing anything. Outcomes:
//
//	represented  every damage amount, the half-damage rule and each named condition are in the parsed effect, and nothing is left over
//	flagged      the parsed effect leaves text for the GM (saveEffect.Extra: movement, pushes, riders...): the reply shows it
//	LOST         a damage amount in the text is not in the parsed effect and nothing flags it: the test fails
//
// MM_SAVES_OUT receives a Markdown report. "Represented" is about parsing the text, not about the rules being right in play.
func TestMonsterBookSaveActions(t *testing.T) {
	dir := os.Getenv("MM_FIXTURES_DIR")
	if dir == "" {
		t.Skip("MM_FIXTURES_DIR not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	var rows []string
	var represented, flagged, lost, total int
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		var doc struct {
			Monsters []srdMonster2024 `json:"monsters"`
		}
		if json.Unmarshal(raw, &doc) != nil || len(doc.Monsters) != 1 {
			t.Fatalf("%s unreadable", f)
		}
		m := doc.Monsters[0]
		for _, group := range [][]srdMonsterEntry{m.Actions, m.BonusActions, m.Reactions, m.Legendary} {
			var entries []srdMonsterEntry
			for _, e := range group {
				if opts := splitSaveOptions(e.Text); len(opts) >= 2 { // each named effect is judged alone; the action itself is refused until one is named
					for _, o := range opts {
						entries = append(entries, srdMonsterEntry{Name: e.Name + " / " + o.Name, Text: o.Text})
					}
					continue
				}
				entries = append(entries, e)
			}
			for _, e := range entries {
				if !strings.Contains(e.Text, "Saving Throw:") {
					continue
				}
				total++
				eff, ok := parseSaveEffect(e.Text)
				if !ok {
					lost++
					rows = append(rows, fmt.Sprintf("| %s | %s | LOST | no save effect parsed |", m.Name, e.Name))
					t.Errorf("%s / %s: save text not parsed", m.Name, e.Name)
					continue
				}
				// an action that is also an attack ("Hit: ... Hit or Miss: ... Dexterity Saving Throw: ...") counts only the save's part
				saveText := e.Text
				if i := strings.Index(saveText, "Saving Throw:"); i >= 0 {
					saveText = saveText[i:]
				}
				inText := len(saveTextDamageRe.FindAllString(saveText, -1))
				switch {
				case len(eff.Damage) < inText && eff.Extra == "":
					lost++
					rows = append(rows, fmt.Sprintf("| %s | %s | LOST | text has %d damage amounts, effect has %d, nothing flagged |", m.Name, e.Name, inText, len(eff.Damage)))
					t.Errorf("%s / %s: %d damage amounts in the text, %d parsed, no GM flag", m.Name, e.Name, inText, len(eff.Damage))
				case eff.Extra != "" || len(eff.Damage) < inText:
					flagged++
					rows = append(rows, fmt.Sprintf("| %s | %s | flagged | damage %d/%d; GM text: %s |", m.Name, e.Name, len(eff.Damage), inText, truncate(eff.Extra, 120)))
				default:
					represented++
					rows = append(rows, fmt.Sprintf("| %s | %s | represented | damage %d, conditions %v, half on success %v |", m.Name, e.Name, len(eff.Damage), eff.Conditions, eff.HalfOnSuccess))
				}
			}
		}
	}
	summary := fmt.Sprintf("%d save actions: %d represented, %d flagged to the GM, %d lost.", total, represented, flagged, lost)
	t.Log(summary)
	if out := os.Getenv("MM_SAVES_OUT"); out != "" {
		body := "# Save-action parse report\n\n" + summary + "\n\n| Monster | Action | Result | Detail |\n|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
		if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
