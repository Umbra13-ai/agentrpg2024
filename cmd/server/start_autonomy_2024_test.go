package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The agent is the player and plays on its own (owner, Nov 23): the server never tells it to wait for its human's permission or to ask its human to make a
// choice that is its own. When there is something it cannot do itself it may ask its human for help: the image, the scheduler, the inbox and the token are
// examples (youPlayNote and humanLimits, start_2024.go), never a list to stay within, so this test forbids the old wording and does not require a reason.

var (
	// the human's permission is not something an agent waits for
	humanPermissionRe = regexp.MustCompile(`(?s)\b([Yy]our|[Ii]ts) human\b.{0,60}permission|permission.{0,60}\b([Yy]our|[Ii]ts) human\b`)
	// a choice that is the agent's own is not put to its human
	humanChoiceRe  = regexp.MustCompile(`(?i)\b(ask|asks|asking|check with|wait for|let) (your|its) human\b.{0,12}\b(whether (to|you|your)|if you|which|what to|how to|to decide|to choose|before you|first)\b`)
	sentenceSplitR = regexp.MustCompile(`[.!?]\s+`)
)

func humanWordingProblem(text string) string {
	if humanPermissionRe.MatchString(text) {
		return "waits for the human's permission"
	}
	for _, s := range sentenceSplitR.Split(text, -1) {
		if humanChoiceRe.MatchString(s) {
			return "sends the agent to its human to make a choice that is its own (" + strings.TrimSpace(s) + ")"
		}
	}
	return ""
}

func TestNoTextSendsTheAgentToItsHumanForWhatItCanDo(t *testing.T) {
	// 1. every string literal of the server's own code, and every godoc description (the API reference is made from them)
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				if why := humanWordingProblem(s); why != "" {
					t.Errorf("%s:%d %s", f, fset.Position(lit.Pos()).Line, why)
				}
			}
			return true
		})
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "// @Description") {
				if why := humanWordingProblem(line); why != "" {
					t.Errorf("%s:%d (godoc) %s", f, i+1, why)
				}
			}
		}
	}
	// 2. every guide
	docs, _ := filepath.Glob("../../docs/*.md")
	for _, d := range docs {
		if strings.Contains(d, "SRD_") || strings.Contains(d, "PLAYTEST") || strings.Contains(d, "MONSTER") {
			continue // records and tables, not guides an agent reads
		}
		b, _ := os.ReadFile(d)
		for i, line := range strings.Split(string(b), "\n") {
			if why := humanWordingProblem(line); why != "" {
				t.Errorf("%s:%d %s", filepath.Base(d), i+1, why)
			}
		}
	}
}

// Everywhere the start is shown it says the agent is the player and decides on its own, with the one list of what the human is for: the Markdown
// start (llms.txt, skill.md), the home page, GET /api/.
func TestTheStartSaysTheAgentPlaysOnItsOwn(t *testing.T) {
	if !strings.Contains(youPlayNote, "yours") || !strings.Contains(humanLimits, "cannot do yourself") || !strings.Contains(humanLimits, "examples") {
		t.Fatalf("the two sentences: %q / %q", youPlayNote, humanLimits)
	}
	mail := true
	for name, text := range map[string]string{"startText": startText(&mail), "startText(nil)": startText(nil), "startHTML": startHTML()} {
		plain := strings.NewReplacer("&#39;", "'", "&amp;", "&", "&quot;", `"`, "&lt;", "<", "&gt;", ">").Replace(text)
		for _, want := range []string{youPlayNote, humanLimits} {
			if !strings.Contains(plain, want) {
				t.Errorf("%s does not say: %.80s...", name, want)
			}
		}
	}
	j := startJSON()
	if j["you_play"] != youPlayNote || j["ask_your_human_only_for"] != humanLimits {
		t.Errorf("GET /api/ does not carry the two sentences: %v", j["you_play"])
	}
	// the check-in note, the join and creation replies set up the check themselves, and name the human only for a scheduler
	four, _ := scheduleOf(4)
	for name, text := range map[string]string{"checkInNote": checkInNote, "seat": seatNote(four, 1), "gm": gmScheduleNote(four, 1)} {
		if !strings.Contains(text, "scheduler") || humanPermissionRe.MatchString(text) {
			t.Errorf("%s: %q", name, text)
		}
	}
}
