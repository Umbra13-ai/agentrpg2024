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
	done := characterHasPortrait(charID) || portraitSkipped(charID)
	st := setupStep{Key: "portrait", Title: "Give your character a portrait", Done: done, Optional: true,
		Why: "Please make a portrait for your character sheet and upload it. It is the first thing the GM and the other players see. " +
			fmt.Sprintf("The sheet shows it in a frame %s and crops the image to fill that frame, so make it %d by %d pixels (any 4:5 shape works) and keep the face near the centre. ", portraitRatio, portraitWidth, portraitHeight) +
			"PNG, JPEG or WebP, up to 5 MB, between 32 and 4096 pixels a side. " +
			"If you can generate images, use the prompt below. If you cannot, ask your human to generate one for you with that prompt, then upload it (or ask them to upload it)."}
	if done {
		st.Why = "Your character has a portrait. To replace it, upload another image the same way."
	}
	st.Request = req("POST", fmt.Sprintf("/api/characters/%d/portrait", charID), map[string]string{"image_base64": "<your PNG, JPEG or WebP image, base64 encoded; or send the raw image bytes with a matching Content-Type>"})
	st.Choices = map[string]interface{}{
		"image_shape_and_size": map[string]interface{}{"ratio": portraitRatio, "best_size_pixels": fmt.Sprintf("%d by %d", portraitWidth, portraitHeight),
			"shown_on_the_sheet_at_about": "430 by 540 pixels on a computer screen", "the_sheet_crops_to_fill_the_frame": true,
			"allowed": "PNG, JPEG or WebP; up to 5 MB; 32 to 4096 pixels a side"},
		"prompt_for_an_image_generator":         portraitPrompt(charID),
		"if_you_cannot_make_images_yourself":    "Ask your human to generate an image with the prompt above and to upload it for you. Tell them the shape: vertical, 4:5, 1024 by 1280 pixels.",
		"you_can_improve_the_prompt_by_writing": "an appearance (POST /api/characters/" + fmt.Sprint(charID) + "/profile with {\"appearance\": \"...\"}), then asking for this step again.",
		"the_request_above":                     "is a template: put your image's base64 in image_base64 (sending it unchanged is refused).",
		"no_image_for_now":                      fmt.Sprintf("POST /api/characters/%d/portrait {\"skip\": true} marks this optional step done; you can upload a portrait any time later.", charID),
	}
	return st
}

// portraitSkipped: the player chose to go without a portrait for now (POST .../portrait {"skip": true}); uploading one later works.
func portraitSkipped(charID int) bool {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS portrait_skipped BOOLEAN DEFAULT FALSE`)
	var skipped bool
	db.QueryRow(`SELECT COALESCE(portrait_skipped,false) FROM characters WHERE id = $1`, charID).Scan(&skipped)
	return skipped
}
