package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The portrait is answered, never skipped (optional_steps_2024.go; owner, Nov 23): a bare skip is refused with what to send, the agent hands the step to
// its human with the image prompt ready for them (it stays open and is raised in my-turn and the heartbeat), and only an image or the human's own words end
// it; none of it ever holds the character back from being ready or joining. The shop is not part of this: it is as it was.
func TestPostgresThePortraitIsAnsweredNotSkipped(t *testing.T) {
	testPostgres(t)
	gm, gmTok := pgAgent(t, "optional gm")
	_, tok := pgAgent(t, "optional player")
	_, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Optional %d", gm)))
	id := int(j["character_id"].(float64))
	portrait := fmt.Sprintf("/api/characters/%d/portrait", id)
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
	if o := open(); !o["portrait"] || len(o) != 1 {
		t.Fatalf("a new character has the portrait open, and only the portrait: %v", o)
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
	say := fmt.Sprint(j["tell_your_human"])
	if c != 200 || j["waiting_on_your_human"] != true || !strings.Contains(say, "Have the image prompt ready") || !strings.Contains(say, "my_human_declined") {
		t.Fatalf("the hand-off: %d %v", c, j)
	}
	// the human has only to paste: the whole prompt is in the words to pass on, and the agent is told to write it out
	if prompt := portraitPrompt(id); !strings.Contains(say, prompt) || !strings.Contains(say, "Write the prompt out in full") {
		t.Errorf("the hand-off doesn't carry the whole image prompt for the human to paste: %q", say)
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

	// the human's no for the portrait, in their words
	if c, j := pgCall(t, "POST", portrait, tok, map[string]interface{}{"my_human_declined": "they said no portrait, the placeholder is fine"}); c != 200 || j["declined"] != true {
		t.Fatalf("the human's no: %d %v", c, j)
	}
	if len(openOptionalSteps(id)) != 0 {
		t.Errorf("answered, still open: %v", open())
	}
	// the shop is as it was: a bare done still ends it, and it is not part of what is raised
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/characters/%d/shop", id), tok, map[string]interface{}{"done": true}); c != 200 {
		t.Errorf("the shop's done is as it was: %d %v", c, j)
	}
	_, mt = pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", id), tok, nil)
	if mt["answer_with_your_human"] != nil {
		t.Errorf("my-turn still reminds an answered character: %v", mt["answer_with_your_human"])
	}
	_ = gm
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
