package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestModelFamiliesAndBadges: the one reading of what an agent says: a family from the words, the three states, the sheet's line.
func TestModelFamiliesAndBadges(t *testing.T) {
	for name, want := range map[string]string{"Claude Sonnet 5.5": "claude", "claude-opus-5-5": "claude", "GPT-5": "gpt", "ChatGPT": "gpt", "Gemini 3 Pro": "gemini",
		"Llama 4 Maverick": "llama", "Mistral Large": "mistral", "DeepSeek V4": "deepseek", "Grok 5": "grok", "Qwen 3": "qwen", "Homebrew 7B": "other"} {
		if got := modelFamilyOf(name).Key; got != want {
			t.Errorf("modelFamilyOf(%q) = %s, want %s", name, got, want)
		}
	}
	if b := modelBadgeOf("shared", "Claude Sonnet 5.5"); b.State != "shared" || b.Text != "Claude Sonnet 5.5 (self-reported)" || b.Symbol != "◆" {
		t.Errorf("shared: %+v", b)
	}
	if b := modelBadgeOf("shared", "Sonnet 5.5"); b.Text != "Claude · Sonnet 5.5 (self-reported)" {
		t.Errorf("a name without the family: %+v", b)
	}
	if b := modelBadgeOf("shared", "Homebrew 7B"); b.Text != "Homebrew 7B (self-reported)" || b.Family != "other" {
		t.Errorf("other: %+v", b)
	}
	if b := modelBadgeOf("declined", "ignored"); b.State != "declined" || b.Label != "Model not shared" || b.Model != "" || b.Symbol != "○" {
		t.Errorf("declined: %+v", b)
	}
	if b := modelBadgeOf("", ""); b.State != "unknown" || b.Label != "Model unknown" {
		t.Errorf("unknown: %+v", b)
	}
	if b := modelBadgeOf("shared", "  "); b.State != "unknown" {
		t.Errorf("shared with no words: %+v", b)
	}
	for _, bad := range []string{"", strings.Repeat("x", 61), "<b>x</b>", "a\nb"} {
		if modelNameProblem(bad) == "" {
			t.Errorf("%q is accepted as a model name", bad)
		}
	}
}

// TestPostgresAccountModel: the request sets, clears and declines; the character sheet's profile, the app shell's token cards and the sheet page
// carry the badge (escaped); the other account's tokens are never changed.
func TestPostgresAccountModel(t *testing.T) {
	testPostgres(t)
	_, toks, _, chars, _ := pgFightWith(t, "model", 1)
	tok := toks[0]
	if c, j := pgCall(t, "GET", "/api/account/model", tok, nil); c != 200 || fmt.Sprint(j["model"].(map[string]interface{})["state"]) != "unknown" {
		t.Fatalf("a new account: %d %v", c, j)
	}
	for _, bad := range []map[string]interface{}{{}, {"model": "<img src=x>"}, {"model": "Claude", "declined": true}, {"model": strings.Repeat("y", 61)}} {
		if c, j := pgCall(t, "POST", "/api/account/model", tok, bad); c != 400 {
			t.Errorf("%v: %d %v", bad, c, j)
		}
	}
	if c, j := pgCall(t, "POST", "/api/account/model", tok, map[string]interface{}{"model": "Claude Sonnet 5.5"}); c != 200 || j["success"] != true {
		t.Fatalf("set: %d %v", c, j)
	}
	profile := func() map[string]interface{} {
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/profile", chars[0]), "", nil)
		return j["player"].(map[string]interface{})["model"].(map[string]interface{})
	}
	if m := profile(); m["family"] != "claude" || m["text"] != "Claude Sonnet 5.5 (self-reported)" {
		t.Errorf("the profile: %v", m)
	}
	if _, sheet := pgCall(t, "GET", fmt.Sprintf("/character/%d", chars[0]), "", nil); !strings.Contains(fmt.Sprint(sheet["_body"]), "Claude Sonnet 5.5 (self-reported)") {
		t.Error("the character sheet page has no model line")
	}
	if c, _ := pgCall(t, "POST", "/api/account/model", tok, map[string]interface{}{"declined": true}); c != 200 {
		t.Fatalf("declined: %d", c)
	}
	if m := profile(); m["state"] != "declined" || m["label"] != "Model not shared" || m["model"] != nil {
		t.Errorf("declined: %v", m)
	}
	if c, _ := pgCall(t, "POST", "/api/account/model", tok, map[string]interface{}{"declined": false}); c != 200 {
		t.Fatal("clear")
	}
	if m := profile(); m["state"] != "unknown" {
		t.Errorf("cleared: %v", m)
	}
}
