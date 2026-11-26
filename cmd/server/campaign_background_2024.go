package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"image"
	"net/http"
	"strconv"
	"strings"

	"github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"
)

// campaign_background_2024.go: the one owner of a campaign's background picture (the scene behind the Live Table's header) and of its writes.
// The administrator sets it (POST, PUT and DELETE /api/admin/campaigns/{id}/background, the admin page's Background card, written to the admin
// change log); everyone who may watch the campaign reads it (GET /api/campaigns/{id}/background, gated like every read under /api/campaigns/{id}/,
// so a private campaign's picture is private too). With none set the page shows the site's dragon (defaultBackground). What a picture may be is
// `checkImage` (portrait_2024.go) with `backgroundRule`, then `normalizeBackground`: every background is stored as a 1920 by 1080 WebP. Nothing else writes the
// background columns (TestNoOtherBackgroundWriter).

const (
	defaultBackground     = "/assets/hero.webp"
	maxBackgroundBytes    = 5 * 1024 * 1024
	maxBackgroundBodySize = 8 * 1024 * 1024 // request body (covers base64 overhead)
)

// backgroundRule: what may be sent in. What is stored is always backgroundW by backgroundH, a WebP (normalizeBackground), so the size and shape
// of what comes in only decide how much is cropped. The WebP is lossy (backgroundQuality): a scene is a painting, not a token, and lossless would be
// 1.5 to 2 MB where this is 100 to 200 KB (the encoder, gen2brain/webp, is libwebp compiled to WebAssembly: it needs no C compiler, so the build
// stays CGO_ENABLED=0).
var backgroundRule = imageRule{MaxBytes: maxBackgroundBytes, MinPx: 320, MaxPx: 4096,
	Best: "1920 by 1080 pixels (16 by 9) is stored as it is; any other picture is scaled to cover that and the edges are cropped"}

// The stored size of every background (owner, Nov 26: "lock it at 1920x1080 and then convert the upload to webp").
const (
	backgroundW = 1920
	backgroundH = 1080
	// backgroundQuality: the lossy WebP's quality (0 to 100): 85 is a close copy of the scene at about a tenth of the lossless size.
	backgroundQuality = 85
)

// normalizeBackground returns the picture to store: scaled to cover backgroundW by backgroundH, the overflow cropped evenly from both sides
// (the scene's centre stays), written as a lossy WebP, and a sentence saying what was done. checkImage has passed the picture. A WebP that
// is already exactly that size is kept as it is.
func normalizeBackground(imageBytes []byte, mime string) (stored []byte, storedMime, note string, err error) {
	src, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, "", "", fmt.Errorf("the picture could not be read: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == backgroundW && h == backgroundH && mime == "image/webp" {
		return imageBytes, mime, "", nil
	}
	// the source area that has the stored shape, centred: the largest such rectangle inside the picture
	cw, ch := w, h
	if w*backgroundH > h*backgroundW { // wider than 16:9: cut the sides
		cw = h * backgroundW / backgroundH
	} else { // taller: cut the top and bottom
		ch = w * backgroundH / backgroundW
	}
	crop := image.Rect(b.Min.X+(w-cw)/2, b.Min.Y+(h-ch)/2, b.Min.X+(w-cw)/2+cw, b.Min.Y+(h-ch)/2+ch)
	out := image.NewRGBA(image.Rect(0, 0, backgroundW, backgroundH))
	xdraw.CatmullRom.Scale(out, out.Bounds(), src, crop, xdraw.Src, nil)
	var buf bytes.Buffer
	if err := webp.Encode(&buf, out, webp.Options{Quality: backgroundQuality, Method: 4}); err != nil {
		return nil, "", "", err
	}
	note = fmt.Sprintf("Stored as a %d by %d WebP (it was %d by %d, %d KB, now %d KB).", backgroundW, backgroundH, w, h, (len(imageBytes)+1023)/1024, (buf.Len()+1023)/1024)
	if cw != w || ch != h {
		note += " The picture was not 16 by 9, so its edges were cropped (the centre is kept)."
	}
	return buf.Bytes(), "image/webp", note, nil
}

// saveBackground writes a checked image as the campaign's background.
func saveBackground(campaignID int, imageBytes []byte, mime string) error {
	_, err := db.Exec(`UPDATE lobbies SET background_image = $1, background_type = $2, background_updated_at = NOW() WHERE id = $3`, imageBytes, mime, campaignID)
	return err
}

// clearBackground removes it: the page goes back to the default.
func clearBackground(campaignID int) error {
	_, err := db.Exec(`UPDATE lobbies SET background_image = NULL, background_type = NULL, background_updated_at = NULL WHERE id = $1`, campaignID)
	return err
}

// hasBackground: the campaign has a background of its own.
func hasBackground(campaignID int) bool {
	var n int
	db.QueryRow(`SELECT COALESCE(length(background_image), 0) FROM lobbies WHERE id = $1`, campaignID).Scan(&n)
	return n > 0
}

// backgroundURL is the picture the Live Table shows: the campaign's own (with its change time, so a new one is fetched) or the default.
func backgroundURL(campaignID int) string {
	var updated sql.NullTime
	var n int
	db.QueryRow(`SELECT COALESCE(length(background_image), 0), background_updated_at FROM lobbies WHERE id = $1`, campaignID).Scan(&n, &updated)
	if n == 0 {
		return defaultBackground
	}
	return fmt.Sprintf("/api/campaigns/%d/background?v=%d", campaignID, updated.Time.Unix())
}

// handleCampaignBackground godoc
// @Summary A campaign's background picture
// @Description The picture behind the Live Table's header, as its image (PNG, JPEG or WebP). 404 `no_background` when the administrator set none: the page then shows the default scene. Private campaigns need the watch password or a seat, like every read under /api/campaigns/{id}/. Only the administrator sets it (POST /api/admin/campaigns/{id}/background).
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Success 200 {file} binary "The image, with its Content-Type"
// @Failure 404 {object} map[string]interface{} "No background set"
// @Router /campaigns/{id}/background [get]
func handleCampaignBackground(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var data []byte
	var mime sql.NullString
	var updated sql.NullTime
	err := db.QueryRow(`SELECT background_image, background_type, background_updated_at FROM lobbies WHERE id = $1`, campaignID).Scan(&data, &mime, &updated)
	if err != nil || len(data) == 0 || !mime.Valid {
		if answerDBFailure(w, err, "the campaign") {
			return
		}
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_background"})
		return
	}
	w.Header().Set("Content-Type", mime.String)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; sandbox")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if updated.Valid {
		w.Header().Set("Last-Modified", updated.Time.UTC().Format(http.TimeFormat))
	}
	if r.Method == http.MethodGet {
		w.Write(data)
	}
}

// handleAdminBackground serves POST and PUT (set or replace) and DELETE (remove) of /api/admin/campaigns/{id}/background. The words of this
// route are written in tools/build_swagger.py; the request and the reply come from the code.
func handleAdminBackground(w http.ResponseWriter, r *http.Request, id int) {
	if adminRow("lobbies", id) == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "campaign_not_found"})
		return
	}
	had := hasBackground(id)
	words := func(has bool) string {
		if has {
			return "a background"
		}
		return "the default background"
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		var req struct {
			ImageBase64 string `json:"image_base64"` // the image as base64 (a data: URL is accepted); or send the raw PNG, JPEG or WebP bytes with their Content-Type instead
			Note        string `json:"note"`         // why: written to the admin change log
		}
		var imageBytes []byte
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			if !readRequest(w, r, &req) {
				return
			}
			var ref *portraitRefusal
			if imageBytes, ref = decodePortraitBase64(req.ImageBase64); ref != nil {
				writeJSONStatus(w, ref.Status, ref.Body)
				return
			}
		} else {
			raw, tooBig := bodyBytes(r, maxBackgroundBodySize)
			if tooBig {
				writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]interface{}{"error": "image_too_large", "max_bytes": maxBackgroundBytes,
					"message": "The image is over 5 MB. Make it smaller (" + backgroundRule.Best + ") and send it again."})
				return
			}
			imageBytes = raw
		}
		mime, _, ref := checkImage(imageBytes, backgroundRule)
		if ref != nil {
			writeJSONStatus(w, ref.Status, ref.Body)
			return
		}
		imageBytes, mime, shapeNote, nerr := normalizeBackground(imageBytes, mime)
		if nerr != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "corrupt_image", "message": "The image could not be read: the file looks damaged. Save it again and resend it. Nothing was changed."})
			return
		}
		if err := saveBackground(id, imageBytes, mime); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
			return
		}
		logAdminChangeIn(db, "campaign", id, "background", words(had), fmt.Sprintf("a background (%s, %d bytes)", mime, len(imageBytes)), "admin", req.Note)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "content_type": mime, "bytes": len(imageBytes),
			"background_url": fmt.Sprintf("/api/campaigns/%d/background", id), "note": nilIfEmpty(shapeNote)})
	case http.MethodDelete:
		var req struct {
			Note string `json:"note"` // why: written to the admin change log
		}
		if !readRequest(w, r, &req, optionalBody()) {
			return
		}
		if !had {
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": "The campaign has no background of its own: nothing to remove."})
			return
		}
		if err := clearBackground(id); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
			return
		}
		logAdminChangeIn(db, "campaign", id, "background", words(true), words(false), "admin", req.Note)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": "The background is removed: the page shows the default scene."})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}
