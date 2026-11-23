package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg" // the decoders checkPortraitImage reads
	_ "image/png"
	"net/http"
	"strings"
)

// portrait_2024.go: the one owner of what a character's portrait may be and of its writes. The owner's route (POST and DELETE
// /api/characters/{id}/portrait, character_profile.go) and the administrator's (POST and DELETE /api/admin/characters/{id}/portrait,
// admin_portrait_2024.go) both ask checkPortraitImage and write through savePortrait and clearPortrait; nothing else writes the
// avatar columns (TestNoOtherPortraitWriter).

// portraitRefusal is why an image is not saved: the status and the reply a route sends.
type portraitRefusal struct {
	Status int
	Body   map[string]interface{}
}

// imageRule is what one kind of uploaded picture may be: its size in bytes and pixels, the size that fits best, and the note given when its shape will be
// cropped. A character's portrait and a campaign's background (campaign_background_2024.go) each have one; checkImage is the one check.
type imageRule struct {
	MaxBytes, MinPx, MaxPx int
	Best                   string                // "1024 by 1280 pixels is the best size for the sheet"
	Shape                  func(w, h int) string // the note when the picture will be cropped, "" when it fits
}

// portraitRule: the sheet's frame is 4:5 (0.8).
var portraitRule = imageRule{MaxBytes: maxPortraitBytes, MinPx: minPortraitPixels, MaxPx: maxPortraitPixels,
	Best: "1024 by 1280 pixels is the best size for the sheet",
	Shape: func(w, h int) string {
		if r := float64(w) / float64(h); r < 0.75 || r > 0.85 {
			return fmt.Sprintf("Saved. The image is %d by %d pixels; the sheet shows a 4:5 frame and crops the rest, so the edges may be cut. 1024 by 1280 fits exactly.", w, h)
		}
		return ""
	}}

// checkPortraitImage judges a portrait.
func checkPortraitImage(imageBytes []byte) (mime, shapeNote string, ref *portraitRefusal) {
	return checkImage(imageBytes, portraitRule)
}

// checkImage judges the bytes of an image: not empty, within the rule's size, a PNG, JPEG or WebP, readable (the header and the pixels), each side
// within the rule's pixels. mime is its type; shapeNote says when the frame will crop it.
func checkImage(imageBytes []byte, rule imageRule) (mime, shapeNote string, ref *portraitRefusal) {
	refuse := func(status int, body map[string]interface{}) (string, string, *portraitRefusal) {
		return "", "", &portraitRefusal{Status: status, Body: body}
	}
	if len(imageBytes) == 0 {
		return refuse(http.StatusBadRequest, map[string]interface{}{"error": "empty_image", "message": "No image was sent."})
	}
	if len(imageBytes) > rule.MaxBytes {
		return refuse(http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error": "image_too_large", "max_bytes": rule.MaxBytes, "message": fmt.Sprintf("The image is over %d MB. Make it smaller (%s) and send it again.", rule.MaxBytes/(1024*1024), rule.Best)})
	}
	mime = detectPortraitType(imageBytes)
	if mime == "" {
		return refuse(http.StatusUnsupportedMediaType, map[string]interface{}{
			"error": "unsupported_image_type", "allowed": []string{"image/png", "image/jpeg", "image/webp"}, "message": "This is not a PNG, JPEG or WebP image. Send one of those."})
	}
	if mime != "image/webp" { // webp has no decoder here; PNG/JPEG get a real decode check
		cfg, _, err := image.DecodeConfig(bytes.NewReader(imageBytes))
		if err != nil {
			return refuse(http.StatusBadRequest, map[string]interface{}{"error": "corrupt_image", "message": "The image could not be read: the file looks damaged. Save it again and resend it."})
		}
		if cfg.Width > rule.MaxPx || cfg.Height > rule.MaxPx || cfg.Width < rule.MinPx || cfg.Height < rule.MinPx {
			return refuse(http.StatusBadRequest, map[string]interface{}{
				"error":      "bad_dimensions",
				"min_pixels": rule.MinPx, "max_pixels": rule.MaxPx,
				"got":     fmt.Sprintf("%dx%d", cfg.Width, cfg.Height),
				"message": fmt.Sprintf("The image is %d by %d pixels. Each side must be %d to %d pixels; %s.", cfg.Width, cfg.Height, rule.MinPx, rule.MaxPx, rule.Best)})
		}
		if _, _, derr := image.Decode(bytes.NewReader(imageBytes)); derr != nil { // the pixels too, not only the header (Nov 14 review: a truncated PNG was saved)
			return refuse(http.StatusBadRequest, map[string]interface{}{"error": "corrupt_image", "message": "The image could not be read: the file looks damaged or cut short. Save it again and resend it. Nothing was changed."})
		}
		if rule.Shape != nil {
			shapeNote = rule.Shape(cfg.Width, cfg.Height)
		}
	}
	return mime, shapeNote, nil
}

// savePortrait writes a checked image as the character's portrait.
func savePortrait(charID int, imageBytes []byte, mime string) error {
	_, err := db.Exec(`UPDATE characters SET avatar = $1, avatar_type = $2, avatar_updated_at = NOW() WHERE id = $3`, imageBytes, mime, charID)
	return err
}

// clearPortrait removes the character's portrait.
func clearPortrait(charID int) error {
	_, err := db.Exec(`UPDATE characters SET avatar = NULL, avatar_type = NULL, avatar_updated_at = NULL WHERE id = $1`, charID)
	return err
}

// hasPortrait: the character has a portrait now.
func hasPortrait(charID int) bool {
	var n int
	db.QueryRow(`SELECT COALESCE(length(avatar), 0) FROM characters WHERE id = $1`, charID).Scan(&n)
	return n > 0
}

// decodePortraitBase64 reads image_base64 (a data: URL is accepted too) into the image's bytes.
func decodePortraitBase64(b64 string) ([]byte, *portraitRefusal) {
	if i := strings.Index(b64, "base64,"); i >= 0 { // tolerate data: URLs
		b64 = b64[i+len("base64,"):]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, &portraitRefusal{Status: http.StatusBadRequest, Body: map[string]interface{}{"error": "invalid_base64",
			"message": "image_base64 is not valid base64. Encode the image file's bytes as standard base64 (a data: URL is also accepted)."}}
	}
	return data, nil
}
