package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestModelFamiliesAndBadges: the one reading of what an agent says: a family from the words, the three states, the sheet's line.
func TestModelFamiliesAndBadges(t *testing.T) {
	for name, want := range map[string]string{"Claude Sonnet 5.5": "claude", "claude-opus-5-5": "claude", "GPT-5": "gpt", "ChatGPT": "gpt", "Gemini 3 Pro": "gemini",
		"Llama 4 Maverick": "meta", "Muse Spark": "meta", "Mistral Large": "mistral", "DeepSeek V4": "deepseek", "Grok 5": "grok", "Qwen 3": "qwen", "Fo": "fo", "Instinct": "instinct", "Instinct Assistant 2": "instinct", "Wajo Fo 2": "fo", "Info Bot": "other", "Fortune 1": "other", "Homebrew 7B": "other"} {
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
	for _, b := range []modelBadge{modelBadgeOf("shared", "Homebrew 7B"), modelBadgeOf("declined", ""), modelBadgeOf("", ""), modelBadgeOf("shared", "Claude 5")} {
		if b.Icon == "" {
			t.Errorf("a badge with no icon: %+v", b)
		}
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

// TestPostgresAdminSetsAnAccountsModel: the administrator sets an account's model in the account editor: words alone are "shared", declined drops
// the words, an emptied state clears, and a bad name or state is refused; the character sheet's profile shows what the editor set.
func TestPostgresAdminSetsAnAccountsModel(t *testing.T) {
	testPostgres(t)
	const key = "test-admin-key-for-models"
	t.Setenv("ADMIN_KEY", key)
	_, _, _, chars, _ := pgFightWith(t, "adminmodel", 1)
	var agentID int
	db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, chars[0]).Scan(&agentID)
	set := func(fields map[string]interface{}) int {
		b, _ := json.Marshal(map[string]interface{}{"set": fields})
		r := httptest.NewRequest("POST", fmt.Sprintf("/api/admin/accounts/%d", agentID), bytes.NewReader(b))
		r.RemoteAddr = "10.77.3.1:1234"
		r.Header.Set("X-Admin-Key", key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w.Code
	}
	profile := func() map[string]interface{} {
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/profile", chars[0]), "", nil)
		return j["player"].(map[string]interface{})["model"].(map[string]interface{})
	}
	if c := set(map[string]interface{}{"ai_model": "Gemini 3 Pro"}); c != 200 {
		t.Fatalf("words alone: %d", c)
	}
	if m := profile(); m["state"] != "shared" || m["family"] != "gemini" {
		t.Errorf("words alone are shared: %v", m)
	}
	if c := set(map[string]interface{}{"ai_model_state": "declined"}); c != 200 {
		t.Fatalf("declined: %d", c)
	}
	if m := profile(); m["state"] != "declined" || m["model"] != nil {
		t.Errorf("declined drops the words: %v", m)
	}
	if c := set(map[string]interface{}{"ai_model_state": ""}); c != 200 {
		t.Fatalf("cleared: %d", c)
	}
	if m := profile(); m["state"] != "unknown" {
		t.Errorf("cleared: %v", m)
	}
	for _, bad := range []map[string]interface{}{{"ai_model": "<b>x</b>"}, {"ai_model": strings.Repeat("z", 61)}, {"ai_model_state": "maybe"}} {
		if c := set(bad); c != 400 {
			t.Errorf("%v: %d", bad, c)
		}
	}
}

// TestPostgresSetupAsksForTheModel: the character's setup has the optional model step: open while the account has said nothing, answered by a model or
// a no, never counted in steps_left, and never sending the agent to its human.
func TestPostgresSetupAsksForTheModel(t *testing.T) {
	testPostgres(t)
	_, toks, _, chars, _ := pgFightWith(t, "setupmodel", 1)
	step := func() map[string]interface{} {
		_, j := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", chars[0]), toks[0], nil)
		for _, s := range asList(j["steps"]) {
			if m, _ := s.(map[string]interface{}); m["key"] == "model" {
				return m
			}
		}
		t.Fatalf("setup has no model step: %v", j)
		return nil
	}
	st := step()
	if st["done"] == true || st["optional"] != true || strings.Contains(fmt.Sprint(st), "human") {
		t.Errorf("the open step: %v", st)
	}
	pgCall(t, "POST", "/api/account/model", toks[0], map[string]interface{}{"declined": true})
	if st := step(); st["done"] != true {
		t.Errorf("a no answers it: %v", st)
	}
	if c, _ := pgCall(t, "POST", "/api/account/model", toks[0], map[string]interface{}{"model": "<your model's name, as you know it>"}); c != 400 {
		t.Errorf("the template is accepted: %d", c)
	}
}

// TestAdminModelChoice: the admin's pick is stored as the families read it: a family with or without a name, Other's typed name, a no, unknown.
func TestAdminModelChoice(t *testing.T) {
	for _, c := range []struct{ family, name, state, words string }{
		{"claude", "", "shared", "Claude"}, {"claude", "Sonnet 5.5", "shared", "Sonnet 5.5"}, {"gpt", "5", "shared", "GPT 5"},
		{"meta", "Muse Spark", "shared", "Muse Spark"}, {"other", "Homebrew 7B", "shared", "Homebrew 7B"},
		{"declined", "ignored", "declined", ""}, {"unknown", "", "", ""},
	} {
		state, words, problem := adminModelChoice(c.family, c.name)
		if problem != "" || state != c.state || words != c.words {
			t.Errorf("%s %q: %q %q %q", c.family, c.name, state, words, problem)
		}
		if c.state == "shared" && c.family != "other" && modelBadgeOf(state, words).Family != c.family {
			t.Errorf("%s %q reads as %s", c.family, c.name, modelBadgeOf(state, words).Family)
		}
	}
	for _, bad := range [][2]string{{"other", ""}, {"other", "<b>"}, {"nope", ""}} {
		if _, _, problem := adminModelChoice(bad[0], bad[1]); problem == "" {
			t.Errorf("%v is accepted", bad)
		}
	}
	if n := len(modelChoices()); n != len(modelFamilies)+3 {
		t.Errorf("the picker offers %d choices", n)
	}
}

// TestPostgresAdminPicksAModel: the admin page's picker sets the account's model through its route, logged, and the character's sheet shows it.
func TestPostgresAdminPicksAModel(t *testing.T) {
	testPostgres(t)
	const key = "test-admin-key-for-model-picker"
	t.Setenv("ADMIN_KEY", key)
	_, _, _, chars, _ := pgFightWith(t, "pickmodel", 1)
	var agentID int
	db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, chars[0]).Scan(&agentID)
	call := func(method string, body interface{}) (int, map[string]interface{}) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, fmt.Sprintf("/api/admin/accounts/%d/model", agentID), bytes.NewReader(b))
		r.RemoteAddr = "10.77.4.1:1234"
		r.Header.Set("X-Admin-Key", key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		out := map[string]interface{}{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if c, j := call("GET", nil); c != 200 || len(asList(j["choices"])) != len(modelFamilies)+3 {
		t.Fatalf("GET: %d %v", c, j)
	}
	if c, j := call("POST", map[string]interface{}{"family": "other", "model": "Homebrew 7B", "note": "test"}); c != 200 {
		t.Fatalf("other: %d %v", c, j)
	}
	_, j := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/profile", chars[0]), "", nil)
	if m := j["player"].(map[string]interface{})["model"].(map[string]interface{}); m["family"] != "other" || m["model"] != "Homebrew 7B" {
		t.Errorf("the sheet after Other: %v", m)
	}
	if c, _ := call("POST", map[string]interface{}{"family": "other"}); c != 400 {
		t.Errorf("Other with no name: %d", c)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM admin_changes WHERE kind='account' AND row_id=$1 AND field='ai_model'`, agentID).Scan(&n)
	if n != 1 {
		t.Errorf("logged %d times, want 1", n)
	}
}
