package main

import (
	"fmt"
	"net/http"
	"strings"
)

// admin_portrait_2024.go: the administrator adds or removes a character's portrait (the photo on its sheet), whoever owns the
// character. The rule of what a portrait may be and the writes are portrait_2024.go's, the owner's route uses the same ones; the change is
// written to the admin change log like every other admin edit.

// handleAdminPortrait serves POST and PUT (add or replace) and DELETE (remove) of /api/admin/characters/{id}/portrait.
func handleAdminPortrait(w http.ResponseWriter, r *http.Request, id int) {
	if adminRow("characters", id) == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	had := hasPortrait(id)
	words := func(has bool) string {
		if has {
			return "a portrait"
		}
		return "no portrait"
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
			raw, tooBig := bodyBytes(r, maxPortraitBodyBytes)
			if tooBig {
				writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]interface{}{"error": "image_too_large", "max_bytes": maxPortraitBytes,
					"message": "The image is over 5 MB. Make it smaller (1024 by 1280 pixels is the best size for the sheet) and send it again."})
				return
			}
			imageBytes = raw
		}
		mime, shapeNote, ref := checkPortraitImage(imageBytes)
		if ref != nil {
			writeJSONStatus(w, ref.Status, ref.Body)
			return
		}
		if err := savePortrait(id, imageBytes, mime); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
			return
		}
		logAdminChangeIn(db, "character", id, "portrait", words(had), fmt.Sprintf("a portrait (%s, %d bytes)", mime, len(imageBytes)), "admin", req.Note)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "content_type": mime, "bytes": len(imageBytes),
			"portrait_url": fmt.Sprintf("/api/characters/%d/portrait", id), "note": nilIfEmpty(shapeNote)})
	case http.MethodDelete:
		var req struct {
			Note string `json:"note"` // why: written to the admin change log
		}
		if !readRequest(w, r, &req, optionalBody()) {
			return
		}
		if !had {
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": "The character has no portrait: nothing to remove."})
			return
		}
		if err := clearPortrait(id); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
			return
		}
		logAdminChangeIn(db, "character", id, "portrait", words(true), words(false), "admin", req.Note)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": "The portrait is removed."})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}
