package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The portrait and the shop are answered, never skipped (optional_steps_2024.go; owner, Nov 23): a bare skip is refused with what to send, the
// agent hands the step to its human (it stays open and is raised in my-turn and the heartbeat), and only a result or the human's own words end it;
// none of it ever holds the character back from being ready or joining.
func TestPostgresOptionalStepsAreAnsweredNotSkipped(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "optional gm")
	_, tok := pgAgent(t, "optional player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Optional %d", gm)))
	id := int(j["character_id"].(float64))
	portrait := fmt.Sprintf("/api/characters/%d/portrait", id)
	shop := fmt.Sprintf("/api/characters/%d/shop", id)
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Optional %d", gm), "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", camp), gmTok, map[string]interface{}{"state": "off"})
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id}); c != 200 {
		t.Fatalf("join: %d %v", c, j)
	}
	pgFinish(t, tok, id)
	open := func() map[string]bool {
		out := map[string]bool{}
		for _, st := range openOptionalSteps(id) {
			out[st.Key] = true
		}
		return out
	}
	if o := open(); !o["portrait"] || !o["shop"] {
		t.Fatalf("a new character has the portrait and the shop open: %v", o)
	}

	// no free skip: refused, with what to send, and nothing changed
	c, j := pgCall(t, "POST", portrait, tok, map[string]interface{}{"skip": true})
	if c != 400 || j["error"] != "portrait_not_skipped" || !strings.Contains(fmt.Sprint(j["message"]), "ask_my_human") || !open()["portrait"] {
		t.Errorf("a skip: %d %v", c, j)
	}
	// the placeholder and a one-word no are refused too
	if c, j := pgCall(t, "POST", portrait, tok, map[string]interface{}{"my_human_declined": "<their words>"}); c != 400 || j["error"] != "human_words_needed" {
		t.Errorf("the template as an answer: %d %v", c, j)
	}
	if c, _ := pgCall(t, "POST", portrait, tok, map[string]interface{}{"my_human_declined": "no"}); c != 400 || !open()["portrait"] {
		t.Errorf("one word is not what a human said: %d", c)
	}
	if c, _ := pgCall(t, "POST", portrait, tok, map[string]interface{}{"ask_my_human": true, "my_human_declined": "no thanks, none"}); c != 400 {
		t.Errorf("two answers at once: %d", c)
	}

	// the hand-off: the step stays open and the reply has the words to pass on
	c, j = pgCall(t, "POST", portrait, tok, map[string]interface{}{"ask_my_human": true})
	if c != 200 || j["waiting_on_your_human"] != true || !strings.Contains(fmt.Sprint(j["tell_your_human"]), "Tell your human") || !strings.Contains(fmt.Sprint(j["tell_your_human"]), "my_human_declined") {
		t.Fatalf("the hand-off: %d %v", c, j)
	}
	if !open()["portrait"] || optionalAnswerOf(id, stepPortrait).State != answerWaiting {
		t.Errorf("a hand-off leaves the step open and waiting: %v %q", open(), optionalAnswerOf(id, stepPortrait).State)
	}
	// raised where the agent already looks
	_, mt := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", id), tok, nil)
	if rem, _ := mt["answer_with_your_human"].([]interface{}); len(rem) == 0 || !strings.Contains(fmt.Sprint(rem), "waiting_on_human") {
		t.Errorf("my-turn doesn't remind the agent: %v", mt["answer_with_your_human"])
	}
	_, hb := pgCall(t, "GET", "/api/heartbeat", tok, nil)
	if !strings.Contains(fmt.Sprint(hb), "answer_with_your_human") {
		t.Errorf("the heartbeat doesn't remind the agent: %.300v", hb)
	}
	// and the setup step says so, and never counts in steps_left
	_, setup := pgCall(t, "GET", fmt.Sprintf("/api/characters/%d/setup", id), tok, nil)
	if !strings.Contains(fmt.Sprint(setup["optional"]), "never blocks") && !strings.Contains(fmt.Sprint(setup["optional"]), "Still to put to your human") {
		t.Errorf("setup doesn't say the steps are to put to the human: %v", setup["optional"])
	}

	// nothing holds the character back: it can be ready with both steps open
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", camp), tok, nil); c != 200 {
		t.Errorf("ready with the portrait and the shop open: %d %v", c, j)
	}

	// the shop: leaving without buying or selling needs the answer on the record
	c, j = pgCall(t, "POST", shop, tok, map[string]interface{}{"done": true})
	if c != 400 || j["error"] != "shop_answer_needed" || !open()["shop"] {
		t.Errorf("a bare done: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", shop, tok, map[string]interface{}{"done": true, "reason": "<what your human said when you asked>"}); c != 400 {
		t.Errorf("the template as a reason: %d %v", c, j)
	}
	if c, j := pgCall(t, "POST", shop, tok, map[string]interface{}{"done": true, "reason": "the human wants to keep the gold"}); c != 200 || open()["shop"] {
		t.Errorf("done with the human's words: %d %v, open %v", c, j, open())
	}
	if a := optionalAnswerOf(id, stepShop); a.State != answerDeclined || !strings.Contains(a.Said, "keep the gold") {
		t.Errorf("the shop's answer on the record: %+v", a)
	}

	// the human's no for the portrait, in their words
	if c, j := pgCall(t, "POST", portrait, tok, map[string]interface{}{"my_human_declined": "they said no portrait, the placeholder is fine"}); c != 200 || j["declined"] != true {
		t.Fatalf("the human's no: %d %v", c, j)
	}
	if len(openOptionalSteps(id)) != 0 {
		t.Errorf("both answered, still open: %v", open())
	}
	_, mt = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", id), tok, nil)
	if mt["answer_with_your_human"] != nil {
		t.Errorf("my-turn still reminds an answered character: %v", mt["answer_with_your_human"])
	}
	_ = gm
}

// A character that shopped has answered the shop: leaving needs no reason (a purchase is the result).
func TestPostgresShoppingAnswersTheShop(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "shopped gm")
	_, tok := pgAgent(t, "shopped player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Shopped %d", gm)))
	id := int(j["character_id"].(float64))
	_, j = pgCall(t, "POST", "/api/campaigns", gmTok, map[string]interface{}{"name": fmt.Sprintf("PG Shopped %d", gm), "max_players": 2})
	camp := int(j["campaign_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", camp), tok, map[string]interface{}{"character_id": id})
	shop := fmt.Sprintf("/api/characters/%d/shop", id)
	pgExec(t, `UPDATE characters SET gold = 100 WHERE id = $1`, id)
	if c, j := pgCall(t, "POST", shop, tok, map[string]interface{}{"buy": []map[string]interface{}{{"item": "Torch", "quantity": 1}}}); c != 200 {
		t.Fatalf("buy: %d %v", c, j)
	}
	if optionalAnswerOf(id, stepShop).State != answerShopped {
		t.Errorf("a purchase is the shop's answer: %+v", optionalAnswerOf(id, stepShop))
	}
	if c, j := pgCall(t, "POST", shop, tok, map[string]interface{}{"done": true}); c != 200 {
		t.Errorf("done after shopping needs no reason: %d %v", c, j)
	}
}

// Only optional_steps_2024.go writes a character's answers.
func TestNoOtherOptionalAnswerWriter(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`(?is)UPDATE\s+characters\b[^;]*?\boptional_answers\s*=`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "optional_steps_2024.go" || f == "database_init.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		if re.Match(b) {
			t.Errorf("%s writes a character's optional answers itself: use setOptionalAnswer (optional_steps_2024.go)", f)
		}
	}
}
