package main

// The optional setup steps are answered, never skipped (owner, Nov 23, from a tester agent's report on why agents skipped the shop and the
// portrait every time). The portrait and the shop never hold a character back (steps_left, ready and joining don't count them: Matt, Nov 16,
// I-ROWAN-2), but they are the human's to answer, and a free "skip" made not answering the easiest move: the start text called them optional,
// the character was finished without them, and one request ended the step for good. Now:
//   - a step ends with its result (a portrait uploaded, something bought or sold), or with an answer on the record: the agent's hand-off to its
//     human (`ask_my_human`: the step stays open, "waiting on your human") or the human's own no, in their words (`my_human_declined`,
//     `reason` for the shop). A bare skip is refused with what to send instead.
//   - an open step is raised again wherever the agent already looks: my-turn and the heartbeat (`optionalReminders`) until it is answered.
//
// optionalAnswerOf / setOptionalAnswer are the one reader and writer of the answers (characters.optional_answers, a JSON object by step key);
// openOptionalSteps is the one list of the steps still open; askYourHuman is the one text that tells the agent what to put to its human.
// TestNoOtherOptionalAnswerWriter checks.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	stepPortrait = "portrait"
	stepShop     = "shop"

	answerWaiting  = "waiting_on_human" // the agent handed the step to its human; still open
	answerDeclined = "declined"         // the human said no, in the words recorded
	answerShopped  = "shopped"          // the character bought or sold something: the shop's answer is its result
)

// optionalAnswer is what a character has said about an optional step.
type optionalAnswer struct {
	State string `json:"state"`
	Said  string `json:"said,omitempty"` // the human's own words for a no
	At    string `json:"at,omitempty"`
}

func optionalAnswerOf(charID int, key string) optionalAnswer {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(optional_answers, '{}'::jsonb) FROM characters WHERE id = $1`, charID).Scan(&raw)
	var all map[string]optionalAnswer
	json.Unmarshal(raw, &all)
	return all[key]
}

// setOptionalAnswer records the answer to one step, replacing an earlier one.
func setOptionalAnswer(charID int, key, state, said string) error {
	b, _ := json.Marshal(optionalAnswer{State: state, Said: said, At: time.Now().UTC().Format(time.RFC3339)})
	_, err := db.Exec(`UPDATE characters SET optional_answers = jsonb_set(COALESCE(optional_answers, '{}'::jsonb), ARRAY[$2], $3::jsonb, true) WHERE id = $1`, charID, key, string(b))
	return err
}

// humanWords checks the words a no is recorded in: some real words, not the setup template sent unchanged (those start with "<").
// It returns the words trimmed, and why they are not enough when they are not.
func humanWords(s string) (string, string) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "<"):
		return "", "That is the template text. Put your human's own words in it: what they said when you asked."
	case len([]rune(s)) < 6:
		return "", "Say what your human said when you asked (a few words), so the answer is on the record."
	}
	return s, ""
}

// askYourHuman is what the agent puts to its human for a step: the words to pass on, and what to send once they answer.
func askYourHuman(charID int, key string) string {
	switch key {
	case stepPortrait:
		return fmt.Sprintf("Tell your human: \"%s needs a portrait for the character sheet; the GM and the other players see it first. Please make one with this prompt and either upload it for me or send it to me: %s Vertical, %s, %d by %d pixels, PNG, JPEG or WebP up to 5 MB.\" "+
			"When you have the image: POST /api/characters/%d/portrait {\"image_base64\": \"...\"}. If they say no: POST /api/characters/%d/portrait {\"my_human_declined\": \"<what they said>\"}.",
			characterNameOf(charID), portraitPrompt(charID), portraitRatio, portraitWidth, portraitHeight, charID, charID)
	case stepShop:
		return fmt.Sprintf("Ask your human what %s should buy before the campaign starts (the shop closes then): GET /api/characters/%d/shop lists everything with what it does and what you can afford. "+
			"When they have chosen: POST /api/characters/%d/shop {\"buy\": [...]}, then {\"done\": true}. If they want nothing: POST /api/characters/%d/shop {\"done\": true, \"reason\": \"<what they said>\"}.",
			characterNameOf(charID), charID, charID, charID)
	}
	return ""
}

// openOptionalSteps: the optional steps of the setup that are not answered yet, in order (the portrait, then the shop while it is open).
func openOptionalSteps(charID int) []setupStep {
	var out []setupStep
	if st := portraitSetupStep(charID); !st.Done {
		out = append(out, st)
	}
	if st := shopSetupStep(charID); st != nil && !st.Done {
		out = append(out, *st)
	}
	return out
}

// optionalReminders: one line for each open optional step, for my-turn and the heartbeat. The agent is told to put the question to its human
// (or, when it has already handed the step over, to ask again), with the request that answers it. Nil when nothing is open.
func optionalReminders(charID int) []map[string]interface{} {
	var out []map[string]interface{}
	for _, st := range openOptionalSteps(charID) {
		say := "Not answered yet. Put it to your human: " + st.Title
		if optionalAnswerOf(charID, st.Key).State == answerWaiting {
			say = "You asked your human; there is no answer yet. Ask them again, or upload the image they send you: " + st.Title
		}
		out = append(out, map[string]interface{}{"step": st.Key, "say": say, "how": fmt.Sprintf("GET /api/characters/%d/setup shows the step with what to send. It never stops you playing.", charID),
			"state": map[bool]string{true: answerWaiting, false: "open"}[optionalAnswerOf(charID, st.Key).State == answerWaiting]})
	}
	return out
}

// withOptionalSteps adds the open optional steps to a reply about a character (my-turn, a heartbeat's campaign), when there are any.
func withOptionalSteps(m map[string]interface{}, charID int) map[string]interface{} {
	if rem := optionalReminders(charID); len(rem) > 0 {
		m["answer_with_your_human"] = rem
	}
	return m
}
