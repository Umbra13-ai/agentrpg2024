package main

// The portrait is answered, never skipped (owner, Nov 23, from a tester agent's report on why agents skipped it every time; the starting shop is left as it
// was: the owner said not to change it). The portrait never holds a character back (steps_left, ready and joining don't count it: Matt, Nov 16,
// I-ROWAN-2), but it is the agent's own step, and a free "skip" made not answering the easiest move: the start text called it optional, the character was
// finished without it, and one request ended the step for good. Now:
//   - the step ends with its result (an image uploaded) or with an answer on the record: the agent's hand-off to its human (`ask_my_human`: the step
//     stays open, "waiting on your human", and the agent has the image prompt ready for the human to paste) or the human's own no, in their words
//     (`my_human_declined`). A bare skip is refused with what to send instead.
//   - the open step is raised again wherever the agent already looks: my-turn and the heartbeat (`optionalReminders`) until it is answered.
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

	answerWaiting  = "waiting_on_human" // the agent handed the step to its human; still open
	answerDeclined = "declined"         // the human said no, in the words recorded
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

// askYourHuman is what the agent puts to its human for a step: the words to pass on, with the image prompt in them so the human has only to make the
// image, and what to send once they answer.
func askYourHuman(charID int, key string) string {
	switch key {
	case stepPortrait:
		return fmt.Sprintf("Have the image prompt ready, and put it to your human in one message they can use as it is: \"%s needs a portrait for the character sheet; the GM and the other players see it first. "+
			"Please paste this prompt into an image generator and upload the result for me (or send me the image): %s\" Vertical, %s, %d by %d pixels, PNG, JPEG or WebP up to 5 MB. "+
			"Write the prompt out in full in that message (do not just point at the step), so all your human has to do is paste it and make the image. "+
			"When you have the image: POST /api/characters/%d/portrait {\"image_base64\": \"...\"}. If they say no: POST /api/characters/%d/portrait {\"my_human_declined\": \"<what they said>\"}.",
			characterNameOf(charID), portraitPrompt(charID), portraitRatio, portraitWidth, portraitHeight, charID, charID)
	}
	return ""
}

// openOptionalSteps: the steps of the setup that are answered by the human and not answered yet (the portrait).
func openOptionalSteps(charID int) []setupStep {
	var out []setupStep
	if st := portraitSetupStep(charID); !st.Done {
		out = append(out, st)
	}
	return out
}

// optionalReminders: one line for each open step, for my-turn and the heartbeat. The agent is told to put the question to its human with the image
// prompt ready (or, when it has already handed the step over, to ask again), with the request that answers it. Nil when nothing is open.
func optionalReminders(charID int) []map[string]interface{} {
	var out []map[string]interface{}
	for _, st := range openOptionalSteps(charID) {
		say := "You have no portrait yet. Make one if you can generate images; if you cannot, ask your human, with the image prompt written out ready for them to paste: " + st.Title
		if optionalAnswerOf(charID, st.Key).State == answerWaiting {
			say = "You asked your human; there is no image yet. Ask them again with the image prompt ready to paste, or upload the image they send you: " + st.Title
		}
		out = append(out, map[string]interface{}{"step": st.Key, "say": say, "how": fmt.Sprintf("GET /api/characters/%d/setup shows the step with what to send. It never stops you playing.", charID),
			"state": map[bool]string{true: answerWaiting, false: "open"}[optionalAnswerOf(charID, st.Key).State == answerWaiting]})
	}
	return out
}
