package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// What the agent's report on the builder asked (Nov 23): the finish rule said once, the count that says why it rose, the follow-up named on the option before
// it is picked, the skills a background gives marked before the pick.

func builderCall(t *testing.T, tok, method, path string, body map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	return pgCall(t, method, path, tok, body)
}

func currentChoices(view map[string]interface{}) []map[string]interface{} {
	cur, _ := view["current_step"].(map[string]interface{})
	var out []map[string]interface{}
	for _, c := range cur["choices"].([]interface{}) {
		out = append(out, c.(map[string]interface{}))
	}
	return out
}

func choiceOf(view map[string]interface{}, field string) map[string]interface{} {
	for _, c := range currentChoices(view) {
		if c["field"] == field {
			return c
		}
	}
	return nil
}

// Item 3 and 4: every background and species option says its follow-up before it is picked; every background's option lists the class skills it uses up; the
// class step marks the skills a background gives before one is chosen.
func TestPostgresBuilderOptionsNameTheirFollowUps(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "builder followups")
	c, d := builderCall(t, tok, "POST", "/api/character-builder", map[string]interface{}{})
	if c != 200 {
		t.Fatalf("start: %d %v", c, d)
	}
	id := int(d["draft_id"].(float64))
	path := fmt.Sprintf("/api/character-builder/%d", id)
	for _, class := range []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"} {
		builderCall(t, tok, "POST", path, map[string]interface{}{"class": class, "skill_proficiencies": nil})
		c, v := builderCall(t, tok, "POST", path, map[string]interface{}{"class": class})
		if c != 200 {
			t.Fatalf("%s: %d %v", class, c, v)
		}
		// the class step before a background: the skills a background gives are marked on the skill choice
		if sk := choiceOf(v, "skill_proficiencies"); sk == nil || sk["given_by_backgrounds"] == nil {
			t.Errorf("%s: the class step does not mark the skills a background gives: %v", class, sk)
		}
		c, v = builderCall(t, tok, "POST", path, map[string]interface{}{"use_suggestion": true})
		if c != 200 {
			t.Fatalf("%s suggestion: %d %v", class, c, v)
		}
		bgs := choiceOf(v, "background")
		if bgs == nil {
			t.Fatalf("%s: the current step has no background choice: %v", class, v["current_step"])
		}
		for _, o := range bgs["options"].([]interface{}) {
			opt := o.(map[string]interface{})
			b := game.GetBackground(fmt.Sprint(opt["background"]))
			want := classSkillsUsedBy(class, b)
			got, _ := opt["uses_up_class_skills"].([]interface{})
			if len(got) != len(want) {
				t.Errorf("%s with %s: uses_up_class_skills %v, the data says %v", class, b.Key, got, want)
			}
			_, hasFeat := featFollowUps[b.Feat]
			if fu, _ := opt["follow_up"].(map[string]interface{}); hasFeat != (fu != nil) {
				t.Errorf("%s: follow_up %v, but its feat %q takes follow-ups %v", b.Key, fu != nil, b.Feat, hasFeat)
			}
		}
		break // the options do not depend on the class beyond the skills: one class shown in full, the data check below covers the rest
	}
	// every class and background pair through the one data function
	for _, class := range []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"} {
		_, allowed := classSkillChoices(class)
		for _, k := range game.BackgroundSlugs() {
			b := game.GetBackground(k)
			for _, s := range classSkillsUsedBy(class, b) {
				if !containsStr(allowed, s) || !containsStr(skillSources()[s], b.Key) {
					t.Errorf("%s / %s: %s is reported as used up but is not on both lists", class, b.Key, s)
				}
			}
		}
	}
	// species: every one with Needs names them before the pick
	for _, sp := range game.AllSpecies() {
		fu := followUpOfSpecies(sp)
		if (len(sp.Needs) > 0) != (fu != nil) {
			t.Errorf("species %s: follow_up %v for needs %v", sp.Key, fu != nil, sp.Needs)
		}
	}
}

// Item 2: a request that raises the count says why in the same reply.
func TestPostgresBuilderCountRiseIsExplained(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "builder count")
	_, d := builderCall(t, tok, "POST", "/api/character-builder", map[string]interface{}{})
	id := int(d["draft_id"].(float64))
	path := fmt.Sprintf("/api/character-builder/%d", id)
	builderCall(t, tok, "POST", path, map[string]interface{}{"class": "bard"})
	builderCall(t, tok, "POST", path, map[string]interface{}{"use_suggestion": true})
	remaining := func(v map[string]interface{}) int { n, _ := v["choices_remaining"].(float64); return int(n) }
	_, v := builderCall(t, tok, "GET", path, nil)
	before := remaining(v)
	// a background whose Origin feat takes a follow-up raises the count
	var withFeat string
	for _, k := range game.BackgroundSlugs() {
		if b := game.GetBackground(k); b != nil && featFollowUps[b.Feat] != nil {
			withFeat = k
			break
		}
	}
	c, v := builderCall(t, tok, "POST", path, map[string]interface{}{"background": withFeat})
	if c != 200 {
		t.Fatalf("background %s: %d %v", withFeat, c, v)
	}
	after := remaining(v)
	rose, _ := v["choices_remaining_rose"].(string)
	if after > before && (rose == "" || !strings.Contains(rose, strconv.Itoa(before)) || !strings.Contains(rose, strconv.Itoa(after)) || !strings.Contains(fmt.Sprint(v["message"]), "new question, not an error")) {
		t.Errorf("the count went from %d to %d and the reply says %q / %v", before, after, rose, v["message"])
	}
	if after <= before && rose != "" {
		t.Errorf("the count did not rise (%d to %d) but the reply explains a rise: %q", before, after, rose)
	}
}

// Item 1: the finish rule is one text; the portrait is asked, never optional, and never counted in steps_left.
func TestPostgresPortraitIsAskedNotOptional(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "asked portrait")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Asked Portrait %d", time.Now().UnixNano())))
	id := int(j["character_id"].(float64))
	_, setup := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", id), tok, nil)
	var portrait map[string]interface{}
	for _, s := range setup["steps"].([]interface{}) {
		if st := s.(map[string]interface{}); st["key"] == "portrait" {
			portrait = st
		}
	}
	if portrait == nil || portrait["asked"] != true || portrait["optional"] == true {
		t.Fatalf("the portrait step: %v", portrait)
	}
	if n, _ := setup["asked_steps_left"].(float64); n != 1 {
		t.Errorf("asked_steps_left %v", setup["asked_steps_left"])
	}
	if setup["next_asked"] == nil && setup["next"] == nil {
		t.Errorf("neither next nor next_asked offers the portrait: %v", setup["how"])
	}
	for _, key := range []string{"asked", "optional"} {
		if txt, _ := setup[key].(string); strings.Contains(strings.ToLower(txt), "portrait is optional") {
			t.Errorf("%s says the portrait is optional: %q", key, txt)
		}
	}
	// the sentences come from the owner: start text, command index and skill.md all hold the finish rule
	for name, text := range map[string]string{"startText": startText(nil), "commandIndex": commandIndexMarkdown(), "skill.md": readDoc(t, "skill.md")} {
		if !strings.Contains(text, finishRule) {
			t.Errorf("%s does not hold the finish rule", name)
		}
	}
}

func readDoc(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var portraitOptionalRe = regexp.MustCompile(`(?i)\bportrait[^.]{0,80}\boptional\b|\boptional\b[^.]{0,80}\bportrait\b`)
var fieldNameRe = regexp.MustCompile("(?i)optional_steps_left|next_optional|optional_answers|optionalAnswer\\w*|openOptional\\w*|optional_steps_2024\\.go")
var denyRe = regexp.MustCompile(`(?i)not optional|never optional|isn't optional|no longer optional`)

func portraitOptionalProblem(text string) string {
	text = fieldNameRe.ReplaceAllString(text, "")
	for _, sentence := range regexp.MustCompile(`[.!?]\s+`).Split(text, -1) {
		if portraitOptionalRe.MatchString(sentence) && !denyRe.MatchString(sentence) {
			return strings.TrimSpace(sentence)
		}
	}
	return ""
}

// No text calls the portrait optional while it is asked and answered: every string literal of the server and every guide.
func TestNoTextCallsThePortraitOptional(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					if why := portraitOptionalProblem(s); why != "" {
						t.Errorf("%s:%d calls the portrait optional: %q", f, fset.Position(lit.Pos()).Line, why)
					}
				}
			}
			return true
		})
	}
	docs, _ := filepath.Glob("../../docs/*.md")
	for _, d := range docs {
		if strings.Contains(d, "PLAYTEST") || strings.Contains(d, "SRD_") || strings.Contains(d, "REPAIR") {
			continue // records of what was said then
		}
		b, _ := os.ReadFile(d)
		for i, line := range strings.Split(string(b), "\n") {
			if why := portraitOptionalProblem(line); why != "" {
				t.Errorf("%s:%d calls the portrait optional: %q", filepath.Base(d), i+1, why)
			}
		}
	}
}
