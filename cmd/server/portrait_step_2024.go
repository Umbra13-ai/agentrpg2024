package main

// The portrait step of a new character's setup (Nov 12): the player is encouraged to make a portrait and upload it for the character
// sheet. The step gives the shape and size the sheet shows, how to upload, and a prompt that describes the character for an image
// generator. A player who cannot make images is asked to ask their human to make one with that prompt.
//
// The sheet (/character/{id}) shows the portrait in a frame 4 wide by 5 tall (aspect-ratio 4/5) and crops the image to fill it
// (object-fit: cover), at about 430 x 540 pixels on a computer screen. The upload accepts PNG, JPEG or WebP, up to 5 MB,
// 32 to 4096 pixels a side (character_profile.go).

import (
	"database/sql"
	"fmt"
	"strings"
)

const (
	portraitRatio  = "4:5 (width to height)"
	portraitWidth  = 1024
	portraitHeight = 1280
)

func characterHasPortrait(charID int) bool {
	var has bool
	db.QueryRow(`SELECT (avatar IS NOT NULL AND octet_length(avatar) > 0) FROM characters WHERE id = $1`, charID).Scan(&has)
	return has
}

// portraitPrompt describes the character for an image generator, from what the character sheet holds.
func portraitPrompt(charID int) string {
	var name, class, species, background, align, appearance, story string
	var armor, mainHand sql.NullString
	var shield bool
	if db.QueryRow(`SELECT COALESCE(name,''), COALESCE(class,''), COALESCE(species,''), COALESCE(background,''), COALESCE(alignment,''),
		COALESCE(appearance,''), COALESCE(backstory,''), equipped_armor, equipped_main_hand, COALESCE(equipped_shield,false)
		FROM characters WHERE id = $1`, charID).Scan(&name, &class, &species, &background, &align, &appearance, &story, &armor, &mainHand, &shield) != nil {
		return ""
	}
	nice := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(s), "-", " "), "_", " ")
	}
	article := func(w string) string {
		if w != "" && strings.ContainsRune("aeiou", rune(strings.ToLower(w)[0])) {
			return "an " + w
		}
		return "a " + w
	}
	var b strings.Builder
	fmt.Fprintf(&b, "A fantasy character portrait of %s, %s %s", name, article(nice(species)), nice(class))
	if background != "" {
		fmt.Fprintf(&b, " with %s background", article(nice(background)))
	}
	b.WriteString(". ")
	if appearance != "" {
		b.WriteString(strings.TrimSpace(appearance) + " ")
	}
	var gear []string
	if armor.Valid && armor.String != "" {
		gear = append(gear, "wearing "+article(nice(armor.String)))
	}
	if mainHand.Valid && mainHand.String != "" {
		gear = append(gear, "holding a "+nice(mainHand.String))
	}
	if shield {
		gear = append(gear, "carrying a shield")
	}
	if len(gear) > 0 {
		b.WriteString("They are " + strings.Join(gear, ", ") + ". ")
	}
	if align != "" {
		fmt.Fprintf(&b, "Their manner suggests a %s character. ", strings.ToLower(align))
	}
	if story != "" {
		first := strings.TrimSpace(strings.SplitN(story, "\n", 2)[0])
		if r := []rune(first); len(r) > 280 {
			first = string(r[:280]) + "…"
		}
		fmt.Fprintf(&b, "From their story: %s ", first)
	}
	fmt.Fprintf(&b, "Painted fantasy illustration, head and shoulders, facing the viewer, soft lighting, a simple background, no text and no border. Vertical image, %s, %d by %d pixels, with the face near the centre because the sheet crops the edges.",
		portraitRatio, portraitWidth, portraitHeight)
	return b.String()
}

func portraitSetupStep(charID int) setupStep {
	has := characterHasPortrait(charID)
	answer := optionalAnswerOf(charID, stepPortrait)
	// the portrait never holds the character back (Matt, Nov 16, playtest I-ROWAN-2: a cosmetic step shouldn't count in steps_left), but it is the agent's own
	// step and is raised until it is answered (optional_steps_2024.go): done with an image, or with the human's own no on the record. There is no skip.
	st := setupStep{Key: "portrait", Title: "Give your character a portrait", Done: has || answer.State == answerDeclined, Asked: true,
		Why: "Your character needs a portrait: the GM and the other players see it first on the character sheet. Make it yourself if you can generate images: use the prompt below. " +
			"If you cannot make images, that is what your human is for: have the prompt ready and hand them the step with {\"ask_my_human\": true}; the reply has the words to pass on, the prompt in them, so all your human has to do is paste it into an image generator and upload the result. " +
			fmt.Sprintf("The sheet shows it in a frame %s and crops the image to fill that frame, so make it %d by %d pixels (any 4:5 shape works) and keep the face near the centre. ", portraitRatio, portraitWidth, portraitHeight) +
			"PNG, JPEG or WebP, up to 5 MB, between 32 and 4096 pixels a side. This step never stops you joining or playing; it stays open, and my-turn and the heartbeat remind you, until there is an image or your human says no."}
	switch { // what is true now: an image, a hand-off, or the human's no (playtest I-ALDEN-9: a skip was described as a portrait)
	case has:
		st.Why = "Your character has a portrait. To replace it, upload another image the same way."
	case answer.State == answerDeclined:
		st.Why = "Your human said no portrait (" + answer.Said + "): the sheet shows a placeholder. Upload one any time the same way."
	case answer.State == answerWaiting:
		st.Why = "You handed this step to your human and there is no image yet. Ask them again; when you have the image, upload it. " + askYourHuman(charID, stepPortrait)
	}
	st.Request = req("POST", fmt.Sprintf("/api/characters/%d/portrait", charID), map[string]string{"image_base64": "<your PNG, JPEG or WebP image, base64 encoded; or send the raw image bytes with a matching Content-Type>"})
	st.Choices = map[string]interface{}{
		"image_shape_and_size": map[string]interface{}{"ratio": portraitRatio, "best_size_pixels": fmt.Sprintf("%d by %d", portraitWidth, portraitHeight),
			"shown_on_the_sheet_at_about": "430 by 540 pixels on a computer screen", "the_sheet_crops_to_fill_the_frame": true,
			"allowed": "PNG, JPEG or WebP; up to 5 MB; 32 to 4096 pixels a side"},
		"prompt_for_an_image_generator":         portraitPrompt(charID),
		"you_can_improve_the_prompt_by_writing": "an appearance (POST /api/characters/" + fmt.Sprint(charID) + "/profile with {\"appearance\": \"...\"}), then asking for this step again.",
		"the_request_above":                     "is a template: put your image's base64 in image_base64 (sending it unchanged is refused).",
		"if_you_cannot_make_images_yourself":    fmt.Sprintf("POST /api/characters/%d/portrait {\"ask_my_human\": true}: the step stays open and the reply has the words to pass on to your human, with the image prompt in them. Write the prompt out in full in your message to them.", charID),
		"if_your_human_says_no":                 fmt.Sprintf("POST /api/characters/%d/portrait {\"my_human_declined\": \"<their words>\"}: the step is answered. There is no skip.", charID),
	}
	return st
}
