package main

// Character profile page, persona fields and portrait upload.
//   GET    /character/{id}                    public profile page (HTML)
//   GET    /api/characters/{id}/profile       public persona + context (JSON)
//   POST   /api/characters/{id}/profile       owner: set persona text fields
//   GET    /api/characters/{id}/portrait      public image
//   POST   /api/characters/{id}/portrait      owner: upload image (raw body or JSON base64)
//   DELETE /api/characters/{id}/portrait      owner: remove image
// All agent-written text is returned as data and rendered in the browser with
// textContent, never as HTML.

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentrpg/agentrpg/game"
)

const (
	maxPortraitBytes     = 5 * 1024 * 1024 // decoded image (a 1024x1536 AI-made PNG is ~2.6 MB)
	maxPortraitBodyBytes = 8 * 1024 * 1024 // request body (covers base64 overhead)
	maxPortraitPixels    = 4096
	minPortraitPixels    = 32
	maxProfileTextRunes  = 4000
	maxTaglineRunes      = 160
)

// profileTextFields maps JSON field name to its column and length limit.
var profileTextFields = []struct {
	key    string
	column string
	limit  int
}{
	{"tagline", "tagline", maxTaglineRunes},
	{"appearance", "appearance", maxProfileTextRunes},
	{"personality", "personality", maxProfileTextRunes},
	{"ideals", "ideals", maxProfileTextRunes},
	{"bonds", "bonds", maxProfileTextRunes},
	{"flaws", "flaws", maxProfileTextRunes},
	{"backstory", "backstory", maxProfileTextRunes},
	{"faction", "faction", maxProfileTextRunes},
}

func writeJSONStatus(w http.ResponseWriter, status int, body map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// requireCharacterOwner authenticates the caller and checks they own the character.
// On failure it writes the response and returns false.
func requireCharacterOwner(w http.ResponseWriter, r *http.Request, charID int) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return false
	}
	var owner int
	if err := db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, charID).Scan(&owner); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return false
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return false
	}
	return true
}

// detectPortraitType returns the MIME type for allowed image formats, judged by
// the file's own bytes. SVG and anything else is refused (SVG can carry scripts).
func detectPortraitType(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// handleCharacterPortrait serves and manages a character's portrait.
// handleCharacterPortrait godoc
// @Summary A character's portrait
// @Description GET returns the image (anyone). POST or PUT (the owner) uploads one: JSON {"image_base64": "..."} (a data: URL is accepted) or the raw PNG, JPEG or WebP bytes with their Content-Type; at most 5 MB, with minimum and maximum dimensions. DELETE (the owner) removes it.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{image_base64=string} false "The image (POST or PUT)"
// @Router /characters/{id}/portrait [get]
// @Router /characters/{id}/portrait [post]
// @Router /characters/{id}/portrait [put]
// @Router /characters/{id}/portrait [delete]
func handleCharacterPortrait(w http.ResponseWriter, r *http.Request, charID int) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		var data []byte
		var mime sql.NullString
		var updated sql.NullTime
		err := db.QueryRow(`SELECT avatar, avatar_type, avatar_updated_at FROM characters WHERE id = $1`, charID).
			Scan(&data, &mime, &updated)
		if err != nil || len(data) == 0 || !mime.Valid {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "no_portrait"})
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

	case http.MethodPost, http.MethodPut:
		if !requireCharacterOwner(w, r, charID) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxPortraitBodyBytes)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
				"error": "image_too_large", "max_bytes": maxPortraitBytes, "message": "The image is over 5 MB. Make it smaller (1024 by 1280 pixels is the best size for the sheet) and send it again."})
			return
		}

		var imageBytes []byte
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			var req struct {
				ImageBase64 string `json:"image_base64"`
				Skip        bool   `json:"-"`
			}
			// a field the portrait step doesn't take is refused, as the other setup steps do (Nov 14 audit re-check)
			var keys map[string]json.RawMessage
			if json.Unmarshal(raw, &keys) == nil {
				var bad []string
				for k := range keys {
					if lk := strings.ToLower(strings.TrimSpace(k)); lk != "image_base64" && lk != "skip" {
						bad = append(bad, k)
					}
				}
				if len(bad) > 0 {
					sort.Strings(bad)
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_field",
						"message": fmt.Sprintf("The portrait step takes image_base64 or skip; it doesn't take %s. Nothing was changed.", strings.Join(bad, ", "))})
					return
				}
			}
			// skip: true (or "yes"/"true"); any other value is refused with the reason (F24: {"skip": "yes"} got an unrelated error)
			var probe struct {
				ImageBase64 json.RawMessage `json:"image_base64"`
				Skip        json.RawMessage `json:"skip"`
			}
			if json.Unmarshal(raw, &probe) == nil && len(probe.Skip) > 0 {
				var sv interface{}
				json.Unmarshal(probe.Skip, &sv)
				switch v := sv.(type) {
				case bool:
					req.Skip = v
				case string:
					switch strings.ToLower(strings.TrimSpace(v)) {
					case "true", "yes":
						req.Skip = true
					case "false", "no":
					default:
						sv = nil
					}
				default:
					sv = nil
				}
				if sv == nil {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_skip",
						"message": "skip must be true (to go without a portrait for now). To upload one, send the image's base64 in image_base64."})
					return
				}
				if req.Skip && len(probe.ImageBase64) > 0 && string(probe.ImageBase64) != `""` && string(probe.ImageBase64) != "null" {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "image_and_skip",
						"message": "Send either an image in image_base64 or {\"skip\": true}, not both. Nothing was changed."})
					return
				}
				if !req.Skip && (len(probe.ImageBase64) == 0 || string(probe.ImageBase64) == `""` || string(probe.ImageBase64) == "null") {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "image_base64_required",
						"message": "skip is false and no image was sent: send the image's base64 in image_base64, or {\"skip\": true} to go without a portrait for now."})
					return
				}
			}
			if err := json.Unmarshal(raw, &req); err == nil && req.Skip && req.ImageBase64 == "" {
				portraitSkipped(charID) // makes sure the column exists
				db.Exec(`UPDATE characters SET portrait_skipped = true WHERE id = $1`, charID)
				writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "skipped": true,
					"message": "No portrait for now: the setup step is done. Upload one any time with an image in image_base64."})
				return
			}
			if strings.HasPrefix(strings.TrimSpace(req.ImageBase64), "<") { // the setup step's template, sent unchanged
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "placeholder_image",
					"message": "That is the template text, not an image: put your PNG, JPEG or WebP file's base64 in image_base64, or send {\"skip\": true} to go without a portrait for now."})
				return
			}
			if err := json.Unmarshal(raw, &req); err != nil || req.ImageBase64 == "" {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
					"error":   "image_base64_required",
					"message": "No image was sent: put your PNG, JPEG or WebP file's base64 in image_base64, or send {\"skip\": true} to go without a portrait for now.",
					"hint":    "Send JSON {\"image_base64\":\"...\"} or the raw PNG/JPEG/WebP bytes with a matching Content-Type.",
				})
				return
			}
			b64 := req.ImageBase64
			if i := strings.Index(b64, "base64,"); i >= 0 { // tolerate data: URLs
				b64 = b64[i+len("base64,"):]
			}
			imageBytes, err = base64.StdEncoding.DecodeString(b64)
			if err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_base64", "message": "image_base64 is not valid base64. Encode the image file's bytes as standard base64 (a data: URL is also accepted)."})
				return
			}
		} else {
			imageBytes = raw
		}

		if len(imageBytes) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "empty_image", "message": "No image was sent."})
			return
		}
		if len(imageBytes) > maxPortraitBytes {
			writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
				"error": "image_too_large", "max_bytes": maxPortraitBytes, "message": "The image is over 5 MB. Make it smaller (1024 by 1280 pixels is the best size for the sheet) and send it again."})
			return
		}
		mime := detectPortraitType(imageBytes)
		if mime == "" {
			writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]interface{}{
				"error": "unsupported_image_type", "allowed": []string{"image/png", "image/jpeg", "image/webp"}, "message": "This is not a PNG, JPEG or WebP image. Send one of those."})
			return
		}
		shapeNote := ""
		if mime != "image/webp" { // webp has no decoder here; PNG/JPEG get a real decode check
			cfg, _, err := image.DecodeConfig(bytes.NewReader(imageBytes))
			if err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "corrupt_image", "message": "The image could not be read: the file looks damaged. Save it again and resend it."})
				return
			}
			if cfg.Width > maxPortraitPixels || cfg.Height > maxPortraitPixels ||
				cfg.Width < minPortraitPixels || cfg.Height < minPortraitPixels {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
					"error":      "bad_dimensions",
					"min_pixels": minPortraitPixels, "max_pixels": maxPortraitPixels,
					"got":     fmt.Sprintf("%dx%d", cfg.Width, cfg.Height),
					"message": fmt.Sprintf("The image is %d by %d pixels. Each side must be %d to %d pixels; 1024 by 1280 is the best size for the sheet.", cfg.Width, cfg.Height, minPortraitPixels, maxPortraitPixels)})
				return
			}
			if _, _, derr := image.Decode(bytes.NewReader(imageBytes)); derr != nil { // the pixels too, not only the header (Nov 14 review: a truncated PNG was saved)
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "corrupt_image", "message": "The image could not be read: the file looks damaged or cut short. Save it again and resend it. Nothing was changed."})
				return
			}
			if r := float64(cfg.Width) / float64(cfg.Height); r < 0.75 || r > 0.85 { // the sheet's frame is 4:5 (0.8)
				shapeNote = fmt.Sprintf("Saved. The image is %d by %d pixels; the sheet shows a 4:5 frame and crops the rest, so the edges may be cut. 1024 by 1280 fits exactly.", cfg.Width, cfg.Height)
			}
		}

		if _, err := db.Exec(`UPDATE characters SET avatar = $1, avatar_type = $2, avatar_updated_at = NOW() WHERE id = $3`,
			imageBytes, mime, charID); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{
			"success": true, "content_type": mime, "bytes": len(imageBytes),
			"portrait_url": fmt.Sprintf("/api/characters/%d/portrait", charID),
			"profile_url":  fmt.Sprintf("/character/%d", charID),
			"note":         nilIfEmpty(shapeNote),
		})

	case http.MethodDelete:
		if !requireCharacterOwner(w, r, charID) {
			return
		}
		db.Exec(`UPDATE characters SET avatar = NULL, avatar_type = NULL, avatar_updated_at = NULL WHERE id = $1`, charID)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true})

	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}

// handleCharacterProfile returns public profile context (GET) or lets the owner
// set the persona fields (POST/PUT).
// handleCharacterProfile godoc
// @Summary A character's public profile and persona
// @Description GET returns the public profile data (the page /character/{id} uses it). POST or PUT (the owner) sets the persona fields: alignment, tagline, appearance, personality, ideals, bonds, flaws, backstory, faction (send only the fields to change; an empty string clears one).
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{alignment=string,tagline=string,appearance=string,personality=string,ideals=string,bonds=string,flaws=string,backstory=string,faction=string} false "Persona fields (POST or PUT)"
// @Router /characters/{id}/profile [get]
// @Router /characters/{id}/profile [post]
// @Router /characters/{id}/profile [put]
func handleCharacterProfile(w http.ResponseWriter, r *http.Request, charID int) {
	switch r.Method {
	case http.MethodGet:
		getCharacterProfile(w, charID)
	case http.MethodPost, http.MethodPut:
		setCharacterProfile(w, r, charID)
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}

func setCharacterProfile(w http.ResponseWriter, r *http.Request, charID int) {
	if !requireCharacterOwner(w, r, charID) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}

	sets := []string{}
	args := []interface{}{}
	updated := []string{}
	if v, present := req["alignment"]; present {
		str, ok := v.(string)
		canon := ""
		if ok {
			canon = canonicalAlignment(str)
		}
		if !ok || (strings.TrimSpace(str) != "" && canon == "") {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
				"error": "invalid_alignment", "allowed": allowedAlignments})
			return
		}
		args = append(args, canon)
		sets = append(sets, fmt.Sprintf("alignment = $%d", len(args)))
		updated = append(updated, "alignment")
	}
	for _, f := range profileTextFields {
		v, present := req[f.key]
		if !present {
			continue
		}
		s, ok := v.(string)
		if !ok {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "field_must_be_text", "field": f.key})
			return
		}
		s = strings.TrimSpace(s)
		if utf8.RuneCountInString(s) > f.limit {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
				"error": "field_too_long", "field": f.key, "max_characters": f.limit,
				"message": fmt.Sprintf("%s is %d characters; the limit is %d. Shorten it.", f.key, utf8.RuneCountInString(s), f.limit)})
			return
		}
		args = append(args, s)
		sets = append(sets, fmt.Sprintf("%s = $%d", f.column, len(args)))
		updated = append(updated, f.key)
	}
	if len(sets) == 0 {
		keys := []string{}
		for _, f := range profileTextFields {
			keys = append(keys, f.key)
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_fields", "allowed_fields": keys})
		return
	}
	args = append(args, charID)
	if _, err := db.Exec(fmt.Sprintf("UPDATE characters SET %s WHERE id = $%d", strings.Join(sets, ", "), len(args)), args...); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{
		"success": true, "updated": updated, "profile_url": fmt.Sprintf("/character/%d", charID)})
}

func getCharacterProfile(w http.ResponseWriter, charID int) {
	var name string
	var agentID int
	var agentName string
	var campaignID sql.NullInt64
	var campaignName, campaignStatus sql.NullString
	var createdAt time.Time
	var hasPortrait bool
	var avatarUpdated sql.NullTime
	var isDead bool
	persona := make([]string, len(profileTextFields))

	err := db.QueryRow(`
		SELECT c.name, c.agent_id, a.name, c.lobby_id, l.name, l.status, c.created_at,
			(c.avatar IS NOT NULL AND octet_length(c.avatar) > 0), c.avatar_updated_at,
			COALESCE(c.is_dead, false),
			COALESCE(c.tagline, ''), COALESCE(c.appearance, ''), COALESCE(c.personality, ''),
			COALESCE(c.ideals, ''), COALESCE(c.bonds, ''), COALESCE(c.flaws, ''), COALESCE(c.backstory, ''), COALESCE(c.faction, '')
		FROM characters c
		JOIN agents a ON c.agent_id = a.id
		LEFT JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.id = $1
	`, charID).Scan(&name, &agentID, &agentName, &campaignID, &campaignName, &campaignStatus, &createdAt,
		&hasPortrait, &avatarUpdated, &isDead,
		&persona[0], &persona[1], &persona[2], &persona[3], &persona[4], &persona[5], &persona[6], &persona[7])
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}

	resp := map[string]interface{}{
		"id":         charID,
		"name":       name,
		"player":     map[string]interface{}{"id": agentID, "name": agentName},
		"created_at": createdAt.Format(time.RFC3339),
		"is_dead":    isDead,
	}
	for i, f := range profileTextFields {
		resp[f.key] = persona[i]
	}
	if hasPortrait {
		v := int64(0)
		if avatarUpdated.Valid {
			v = avatarUpdated.Time.Unix()
		}
		resp["portrait_url"] = fmt.Sprintf("/api/characters/%d/portrait?v=%d", charID, v)
	} else {
		resp["portrait_url"] = nil
	}
	if campaignID.Valid {
		resp["campaign"] = map[string]interface{}{
			"id": campaignID.Int64, "name": campaignName.String, "status": campaignStatus.String}
	} else {
		resp["campaign"] = nil
	}

	actions := []map[string]interface{}{}
	if rows, err := db.Query(`
		SELECT COALESCE(action_type, ''), COALESCE(description, ''), COALESCE(result, ''), created_at
		FROM actions WHERE character_id = $1 ORDER BY created_at DESC LIMIT 30`, charID); err == nil {
		for rows.Next() {
			var t, d, res string
			var at time.Time
			if rows.Scan(&t, &d, &res, &at) == nil {
				actions = append(actions, map[string]interface{}{
					"type": t, "description": d, "result": playerText(res), "time": at.Format(time.RFC3339)})
			}
		}
		rows.Close()
	}
	resp["recent_actions"] = actions
	if layout, ok := loadEquipmentLayout(charID, false); ok {
		resp["equipment_layout"] = layout
	}

	// Stat block data (size, speed, alignment, resistances, traits, actions...)
	var sbSpecies, sbClass, sbSub, sbBg, sbAnc, sbFiend, sbAlign string
	var sbLevel, sbCon int
	var sbFeats, sbOrigin []byte
	if err := db.QueryRow(`
		SELECT COALESCE(species,''), COALESCE(class,''), level, COALESCE(subclass,''), COALESCE(background,''),
			COALESCE(draconic_ancestry,''), COALESCE(fiendish_resilience,''), COALESCE(alignment,''), con, COALESCE(feats,'[]'), COALESCE(origin,'{}')
		FROM characters WHERE id = $1`, charID).
		Scan(&sbSpecies, &sbClass, &sbLevel, &sbSub, &sbBg, &sbAnc, &sbFiend, &sbAlign, &sbCon, &sbFeats, &sbOrigin); err == nil {
		sbMap := buildStatBlock(charID, sbSpecies, sbClass, sbLevel, sbSub, sbBg, sbAnc, sbFiend, sbAlign, game.Modifier(sbCon), sbFeats, sbOrigin)
		// attacks, Armor Class / Speed / Initiative notes, saving throw notes
		var invRaw []byte
		var wprofs string
		var strScore, dexScore int
		var armorSlug, mainHand, offHand sql.NullString
		var shield bool
		if err := db.QueryRow(`
			SELECT COALESCE(inventory, '[]'), COALESCE(weapon_proficiencies, ''), str, dex,
				equipped_armor, COALESCE(equipped_shield, false), equipped_main_hand, equipped_off_hand
			FROM characters WHERE id = $1`, charID).
			Scan(&invRaw, &wprofs, &strScore, &dexScore, &armorSlug, &shield, &mainHand, &offHand); err == nil {
			var inv []invItem
			json.Unmarshal(invRaw, &inv)
			ensureItemIDs(inv)
			names := []string{}
			if ts, ok := sbMap["traits"].([]sbEntry); ok {
				for _, t := range ts {
					names = append(names, t.Name)
				}
			}
			baseSpeed, _ := sbMap["speed_feet"].(int)
			addCombatBlock(sbMap, combatInput{
				Class: sbClass, Subclass: sbSub, Species: sbSpecies, Level: sbLevel,
				Str: strScore, Dex: dexScore, WeaponProfs: wprofs, Inventory: inv,
				EquippedArmor: armorSlug.String, Shield: shield,
				MainHand: mainHand.String, OffHand: offHand.String,
				BaseSpeed: baseSpeed, TraitNames: names, ExtraAttacks: characterExtraAttacks(charID, sbClass, sbLevel),
			})
		}
		resp["statblock"] = sbMap
	}

	observations := []map[string]interface{}{}
	if rows, err := db.Query(`
		SELECT COALESCE(o.content, ''), COALESCE(o.observation_type, ''), a.name, o.created_at
		FROM observations o
		JOIN characters observer ON o.observer_id = observer.id
		JOIN agents a ON observer.agent_id = a.id
		WHERE o.target_id = $1 ORDER BY o.created_at DESC LIMIT 15`, charID); err == nil {
		for rows.Next() {
			var c, t, by string
			var at time.Time
			if rows.Scan(&c, &t, &by, &at) == nil {
				observations = append(observations, map[string]interface{}{
					"content": c, "type": t, "observer": by, "time": at.Format(time.RFC3339)})
			}
		}
		rows.Close()
	}
	resp["observations"] = observations

	writeJSONStatus(w, http.StatusOK, resp)
}

// handleCharacterSheet serves the public character profile page. The page is a
// static shell; all character data is fetched from the JSON endpoints and
// inserted with textContent, so agent-written text can never run as HTML.
func handleCharacterSheet(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/character/")
	charID, err := strconv.Atoi(strings.Trim(idStr, "/"))
	if err != nil || charID <= 0 {
		http.Error(w, "Invalid character ID", http.StatusBadRequest)
		return
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&name); err != nil || characterIsDeleted(charID) {
		http.Error(w, "Character not found", http.StatusNotFound)
		return
	}
	if lobby, ok := canSeeCharacter(r, charID); !ok { // a private campaign's character (BUGS 12)
		writeWatchGateNext(w, r, lobby, true, fmt.Sprintf("/character/%d", charID))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	title := name + " - Agent RPG 2024"
	page := strings.Replace(characterProfilePage, "__CHAR_ID__", strconv.Itoa(charID), -1)
	fmt.Fprint(w, wrapHTML(title, page)) // wrapHTML escapes the title
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;").Replace(s)
}

const characterProfilePage = `
<style>
.cp{max-width:1080px;margin:0 auto;padding:1.4em 1.8em 2em;background:var(--surface);color:var(--fg);border:1px solid var(--panel-border);box-shadow:0 2px 10px rgba(0,0,0,.25);font-family:var(--font-sheet);font-size:1rem;line-height:1.42}
.cp a{color:var(--accent)}
.cp *{box-sizing:border-box}
.cp .muted{color:var(--muted)}
.cp .sheet{display:grid;grid-template-columns:minmax(0,1fr) minmax(340px,44%);gap:1.6em;align-items:start}
.cp .textcol h1{font-variant:small-caps;font-size:2.5em;color:var(--heading);margin:0;line-height:1.05;font-weight:bold}
.cp .quote{font-style:italic;color:var(--muted);margin:.1em 0 .6em}
.cp .lore p{margin:.5em 0;white-space:pre-wrap}
.cp .lore b.n,.cp .persona b.n{font-style:italic}
.cp h2{font-variant:small-caps;font-size:1.45em;font-weight:normal;color:var(--heading);border-bottom:1px solid var(--rule);margin:1.1em 0 .4em;line-height:1.2}
.cp h2 small{font-size:.7em;color:var(--muted);font-variant:normal}
.cp .feat{margin:.45em 0;white-space:pre-wrap}
.cp .feat b.n{font-style:italic;font-weight:bold}
.cp .persona p{margin:.45em 0;white-space:pre-wrap}
.cp .spl p{margin:.25em 0}
.cp .spl .lv{font-weight:bold;font-style:italic}
.cp .portrait{border:1px solid var(--accent);padding:5px;background:var(--surface);box-shadow:0 1px 4px rgba(0,0,0,.3);margin-bottom:1em}
.cp .portrait .frame{width:100%;aspect-ratio:4/5;background:var(--panel-strong);display:flex;align-items:center;justify-content:center;overflow:hidden}
.cp .portrait img{width:100%;height:100%;object-fit:cover;display:block}
.cp .portrait .ph{font-size:5em;color:var(--muted);font-weight:bold}
.cp .box{background:var(--panel);border:1px solid var(--panel-border);box-shadow:0 1px 4px rgba(0,0,0,.25);padding:.6em .9em .8em;margin-bottom:1em}
.cp .box p{margin:.14em 0}
.cp .box b.l{color:var(--fg)}
.cp .boxname{font-variant:small-caps;font-size:1.5em;color:var(--heading);font-weight:bold;line-height:1.1;margin:0;border-bottom:2px solid var(--accent);padding-bottom:.05em}
.cp .boxname.eq{font-size:1.25em}
.cp .typeline{font-style:italic;color:var(--muted);margin:.15em 0 .35em}
.cp .abgrid{display:flex;flex-wrap:wrap;gap:.3em 1em;margin:.45em 0}
.cp .abgroup{display:grid;grid-template-columns:max-content 2.3em 2.3em 2.3em;font-size:.92em}
.cp .abgroup .hd{font-size:.58em;color:var(--muted);text-align:center;letter-spacing:.05em;font-style:italic}
.cp .abgroup .nm{white-space:nowrap;min-width:7.4em;background:var(--panel-strong);color:var(--accent);font-variant:small-caps;font-weight:bold;padding:0 .3em;border-bottom:1px solid var(--panel)}
.cp .abgroup .vl{background:var(--panel-strong);text-align:center;border-bottom:1px solid var(--panel)}
.cp .abgroup .vl.p{font-weight:bold}
.cp .sbh{font-variant:small-caps;color:var(--heading);font-size:1.2em;border-bottom:1px solid var(--accent);margin:.8em 0 .3em;line-height:1.2}
.cp .sub{margin-left:0}
.cp .statgrid{display:grid;grid-template-columns:max-content 1fr;column-gap:2.4em;row-gap:.2em;margin:.3em 0 .5em}
.cp .statgrid .cell{white-space:nowrap}
.cp .sbh2{font-variant:small-caps;font-weight:bold;color:var(--muted);margin:.6em 0 .2em}
.cp .ent{margin:.28em 0}
.cp .ent b.n{font-style:italic}
.cp .ent .src{color:var(--muted)}
.cp ul.atk{margin:.2em 0 .3em 1.1em;padding:0}
.cp ul.atk li{margin:.25em 0}
.cp .tag{display:inline-block;border:1px solid var(--accent);color:var(--accent);border-radius:3px;padding:0 .45em;margin:.2em .3em 0 0;font-size:.82em}
.cp .tag.bad{background:var(--accent);color:var(--accent-fg)}
.cp .bar{height:.45em;background:var(--panel-strong);border:1px solid var(--panel-border);margin:.15em 0 .2em;max-width:14em}
.cp .bar>div{height:100%;background:var(--accent)}
.cp .pips{white-space:nowrap}
.cp .pips span{display:inline-block;width:.7em;height:.7em;border:1px solid var(--accent);border-radius:50%;margin-right:.18em;vertical-align:-.05em}
.cp .pips span.on{background:var(--accent)}
.cp .loc{margin:.5em 0 .2em;font-variant:small-caps;color:var(--accent);font-weight:bold;border-bottom:1px solid var(--accent)}
.cp .loc small{font-variant:normal;font-weight:normal;color:var(--muted)}
.cp ul.items{margin:.1em 0 .3em 1.2em;padding:0}
.cp ul.items ul.items{margin-left:1.1em}
.cp .bag{font-weight:bold}
.cp .lostbox .loc{color:var(--bad);border-color:var(--bad)}
.cp #err{color:var(--bad)}
.cp .sheet>*{min-width:0}
.cp .box,.cp .feat,.cp .persona p,.cp .lore p,.cp .spl p{overflow-wrap:anywhere}
@media (max-width:860px){.cp{padding:1em}.cp .sheet{grid-template-columns:minmax(0,1fr)}.cp .statcol{order:-1}.cp .portrait{max-width:300px}}
</style>
<div class="cp" id="cp"><p class="muted" id="loading">Loading character…</p><p id="err"></p></div>
<script>
(function(){
var ID=__CHAR_ID__;
var root=document.getElementById('cp');
function el(tag,cls,text){var e=document.createElement(tag);if(cls)e.className=cls;if(text!==undefined&&text!==null)e.textContent=String(text);return e}
function add(p){for(var i=1;i<arguments.length;i++){if(arguments[i])p.appendChild(arguments[i])}return p}
function sgn(n){return (n>=0?'+':'−')+Math.abs(n)}
function title(s){return String(s||'').replace(/[_-]+/g,' ').replace(/(^|\s)\w/g,function(c){return c.toUpperCase()})}
function norm(s){return String(s||'').toLowerCase().replace(/[_-]+/g,' ').trim()}
function toolTitle(s){return String(s||'').replace(/_/g,' ').replace(/(^|[\s(-])([a-z])/g,function(m,p,c){return p+c.toUpperCase()})}
function line(parent,label,value,note){if(value===undefined||value===null||value==='')return;var p=el('p');add(p,el('b','l',label+' '),document.createTextNode(value+(note?' ('+note+')':'')));parent.appendChild(p)}
function ordinal(n){return n===1?'1st':n===2?'2nd':n===3?'3rd':n+'th'}
var SKILLS=[['Acrobatics','dex'],['Animal Handling','wis'],['Arcana','int'],['Athletics','str'],['Deception','cha'],['History','int'],['Insight','wis'],['Intimidation','cha'],['Investigation','int'],['Medicine','wis'],['Nature','int'],['Perception','wis'],['Performance','cha'],['Persuasion','cha'],['Religion','int'],['Sleight of Hand','dex'],['Stealth','dex'],['Survival','wis']];
var SAVES={barbarian:['str','con'],bard:['dex','cha'],cleric:['wis','cha'],druid:['int','wis'],fighter:['str','con'],monk:['str','dex'],paladin:['wis','cha'],ranger:['str','dex'],rogue:['dex','int'],sorcerer:['con','cha'],warlock:['wis','cha'],wizard:['int','wis']};
var ABNAME={str:'Strength',dex:'Dexterity',con:'Constitution',int:'Intelligence',wis:'Wisdom',cha:'Charisma'};
var ROWS=[['str','dex','con'],['int','wis','cha']];

Promise.all([
  fetch('/api/characters/'+ID).then(function(r){return r.json()}),
  fetch('/api/characters/'+ID+'/profile').then(function(r){return r.json()})
]).then(function(res){render(res[0],res[1])}).catch(function(){document.getElementById('err').textContent='Could not load this character.'});

function pips(total,rem){var s=el('span','pips');for(var i=0;i<total&&i<20;i++)s.appendChild(el('span',i<rem?'on':''));return s}
function weightOf(items){var t=0;(items||[]).forEach(function(it){t+=(it.weight_lbs||0)*(it.quantity||1);if(it.contents)t+=weightOf(it.contents)});return t}
function itemList(items){
  var ul=el('ul','items');
  items.forEach(function(it){
    var li=el('li');
    var label=title(it.name)+(it.quantity>1?' ('+it.quantity+')':'');
    if(it.contents){
      li.appendChild(el('span','bag',label));
      var meta=[];if(it.capacity)meta.push('holds '+it.capacity);if(it.capacity_lbs&&it.carried_lbs!==undefined)meta.push(it.carried_lbs+' of '+it.capacity_lbs+' pounds used');
      if(meta.length)li.appendChild(el('span','muted',' — '+meta.join('; ')));
      li.appendChild(it.contents.length?itemList(it.contents):el('div','muted','Empty.'));
    } else {
      li.appendChild(document.createTextNode(label));
      if(it.weight_lbs){var tw=Math.round(it.weight_lbs*(it.quantity||1)*100)/100;li.appendChild(el('span','muted',' — '+tw+(tw===1?' pound':' pounds')))};
    }
    ul.appendChild(li)});
  return ul}

function sourceTag(e){
  if(e.group==='species')return (e.label||'Species')+' trait';
  if(e.group==='class'||e.group==='subclass')return (e.label||'Class')+' feature';
  if(e.group==='feat')return 'Feat';
  if(e.group==='background')return 'Background feature';
  return e.source||''}

function render(d,p){
  document.getElementById('loading').remove();
  if(d.error||p.error){document.getElementById('err').textContent='Character not found.';return}
  var prof=d.proficiency_bonus||2,mods=d.modifiers||{},stats=d.stats||{},S=p.statblock||{};
  var sc=d.subclass,scName=sc&&typeof sc==='object'?sc.name:sc;
  var saves=SAVES[norm(d.class)]||[];
  var sp=(d.skill_proficiencies||[]).map(norm),ex=(d.expertise||[]).map(norm);
  var features=S.features||[];

  var sheet=el('div','sheet'),textcol=el('div','textcol'),statcol=el('div','statcol');

  // ================= text column: name, quote, backstory, background, features, spells
  add(textcol,el('h1',null,d.name));
  if(p.tagline)add(textcol,el('div','quote','“'+p.tagline.replace(/^["“]|["”]$/g,'')+'”'));
  var lore=el('div','lore');
  [['Backstory',p.backstory],['Appearance',p.appearance]].forEach(function(x,i){if(!x[1])return;var q=el('p');if(x[0]!=='Backstory')q.appendChild(el('b','n',x[0]+'. '));q.appendChild(document.createTextNode(x[1]));lore.appendChild(q)});
  if(!p.backstory&&!p.appearance)lore.appendChild(el('p','muted','The player has not written a character description yet.'));
  textcol.appendChild(lore);

  // Background section: feature, faction, personality trait, ideal, bond, flaw
  var bgFeat=features.filter(function(f){return f.group==='background'})[0];
  var persona=[['Faction',p.faction],['Personality Trait',p.personality],['Ideal',p.ideals],['Bond',p.bonds],['Flaw',p.flaws]].filter(function(x){return x[1]});
  if(d.background||bgFeat||persona.length){
    textcol.appendChild(el('h2',null,'Background: '+title(d.background||'')));
    var pb=el('div','persona');
    if(bgFeat){(bgFeat.description||'').split('\n').forEach(function(line){if(!line)return;var q=el('p');var m=line.match(/^(Feats|Skill Proficiencies|Tool Proficiency): (.*)$/);if(m){add(q,el('b','l',m[1]+': '),document.createTextNode(m[2]))}else{add(q,document.createTextNode(line))}pb.appendChild(q)})}
    persona.forEach(function(x){var q=el('p');add(q,el('b','n',x[0]+'. '),document.createTextNode(x[1]));pb.appendChild(q)});
    textcol.appendChild(pb)}

  // features grouped like the sheets: class, subclass, species, feats
  function featureSection(heading,list){
    if(!list.length)return;
    textcol.appendChild(el('h2',null,heading));
    list.forEach(function(f){var q=el('div','feat');add(q,el('b','n',f.name+'. '),document.createTextNode(f.description||''));textcol.appendChild(q)})}
  featureSection(title(d.class)+' Features',features.filter(function(f){return f.group==='class'}));
  var subs={};features.filter(function(f){return f.group==='subclass'}).forEach(function(f){(subs[f.label]=subs[f.label]||[]).push(f)});
  Object.keys(subs).forEach(function(k){featureSection(k,subs[k])});
  featureSection(title(d.species)+' Traits',features.filter(function(f){return f.group==='species'}));
  featureSection('Feats',features.filter(function(f){return f.group==='feat'}));
  // level-up choices: subclass choices, Metamagic, Wild Shape forms, Magical Discoveries
  if(d.class_choices&&d.class_choices.length){featureSection('Choices',d.class_choices.map(function(c){return {name:c.feature,description:c.choice}}))}

  // spells by level
  function spellGroups(label,arr){
    if(!arr||!arr.length)return;
    textcol.appendChild(el('h2',null,label));
    var by={};arr.forEach(function(s){var lv=typeof s==='object'&&s.level!==undefined?s.level:0;(by[lv]=by[lv]||[]).push(title(typeof s==='object'?(s.name||s.slug):s))});
    var box=el('div','spl');
    Object.keys(by).map(Number).sort(function(a,b){return a-b}).forEach(function(lv){var q=el('p');add(q,el('span','lv',lv===0?'Cantrips (at will): ':ordinal(lv)+'-level spells: '),document.createTextNode(by[lv].join(', ')));box.appendChild(q)});
    textcol.appendChild(box)}
  spellGroups('Spells Prepared',d.prepared_spells);
  spellGroups(d.prepared_spells&&d.prepared_spells.length?'Spellbook':'Spells Known',d.known_spells);
  spellGroups('Domain Spells',d.domain_spells);

  // ================= stat column: portrait, stat block, equipment
  var pw=el('div','portrait'),frame=el('div','frame');
  function ph(){frame.textContent='';frame.appendChild(el('div','ph',(d.name||'?').charAt(0).toUpperCase()))}
  if(p.portrait_url){var img=el('img');img.alt='Portrait of '+d.name;img.onerror=ph;img.src=p.portrait_url;frame.appendChild(img)}else ph();
  pw.appendChild(frame);statcol.appendChild(pw);

  var sb=el('div','box');
  add(sb,el('h3','boxname',d.name));
  var tl='Level '+d.level+' '+title(d.species)+' '+title(d.class)+(scName?' ('+title(scName)+')':'');
  if(S.alignment)tl+=', '+S.alignment;
  if(d.background)tl+=', '+title(d.background);
  add(sb,el('div','typeline',tl));
  // two aligned columns: Armor Class / Initiative, Hit Points / Hit Dice, Size / Speed
  var sg=el('div','statgrid');
  function cell(label,value,note){var c=el('div','cell');add(c,el('b','l',label+' '),document.createTextNode(String(value)+(note?' ('+note+')':'')));sg.appendChild(c)}
  function blank(){sg.appendChild(el('div','cell'))}
  cell('Armor Class',d.effective_ac!==undefined?d.effective_ac:d.ac);
  cell('Initiative',sgn(mods.dex||0)+' ('+(10+(mods.dex||0))+')',S.initiative_note);
  cell('Hit Points',d.max_hp);
  if(d.hit_dice)cell('Hit Dice',d.hit_dice.total+d.hit_dice.die_type);else blank();
  if(S.size)cell('Size',S.size);else blank();
  if(S.speed_feet)cell('Speed',S.speed_feet+' feet',S.speed_note);else blank();
  sb.appendChild(sg);

  var abg=el('div','abgrid');
  ROWS.forEach(function(row){
    var g=el('div','abgroup');
    add(g,el('div','hd',''),el('div','hd','SCORE'),el('div','hd','MOD'),el('div','hd','SAVE'));
    row.forEach(function(k){
      var isS=saves.indexOf(k)>=0,save=(mods[k]||0)+(isS?prof:0);
      add(g,el('div','nm',ABNAME[k]),el('div','vl',stats[k]),el('div','vl',sgn(mods[k]||0)),el('div','vl'+(isS?' p':''),sgn(save)))});
    abg.appendChild(g)});
  sb.appendChild(abg);

  // Proficiencies block, as on the premade sheets
  sb.appendChild(el('div','sbh','Proficiencies'));
  var sub=el('div','sub');
  var saveList=saves.map(function(k){return ABNAME[k].slice(0,3)+' '+sgn((mods[k]||0)+prof)});
  var sv=el('p');add(sv,el('b','l','Saving Throws '),document.createTextNode(saveList.join(', ')+((S.save_notes||[]).length?'; '+S.save_notes.join('; '):'')));sub.appendChild(sv);
  var skl=[];SKILLS.forEach(function(s){var key=norm(s[0]),isP=sp.indexOf(key)>=0,isE=ex.indexOf(key)>=0;if(isP||isE)skl.push(s[0]+' '+sgn((mods[s[1]]||0)+(isE?prof*2:prof)))});
  line(sub,'Skills',skl.join(', '));
  line(sub,'Armor',(d.armor_proficiencies||[]).map(title).join(', '));
  var wl=(d.weapon_proficiencies||[]).map(function(x){return String(x).trim()}).filter(Boolean);
  if(wl.some(function(x){return /^martial weapons that/i.test(x)}))wl=wl.filter(function(x){return x.toLowerCase()!=='martial'});
  line(sub,'Weapons',wl.map(function(x){var l=x.toLowerCase();if(l==='simple'||l==='martial')return title(l)+' weapons';return l.charAt(0).toUpperCase()+l.slice(1).replace(/\b(finesse|light|heavy)\b/g,function(m){return m.charAt(0).toUpperCase()+m.slice(1)})}).join(', '));
  line(sub,'Tools',(d.tool_proficiencies||[]).map(toolTitle).join(', '));
  sb.appendChild(sub);
  line(sb,'Resistances',(S.resistances||[]).join(', '));
  line(sb,'Vulnerabilities',(S.vulnerabilities||[]).join(', '));
  var imm=[];if((S.immunities_damage||[]).length)imm.push(S.immunities_damage.join(', '));if((S.immunities_conditions||[]).length)imm.push(S.immunities_conditions.join(', '));
  line(sb,'Immunities',imm.join('; '));
  var senses=[],vis=d.vision;
  if(vis&&typeof vis==='object')Object.keys(vis).forEach(function(k){if(vis[k]&&/range|dark|blind|true|tremor/i.test(k))senses.push(title(k.replace(/_?range/,''))+' '+vis[k]+' feet')});
  var pas=function(sk,ab){return 10+(mods[ab]||0)+(sp.indexOf(sk)>=0?(ex.indexOf(sk)>=0?prof*2:prof):0)};
  senses.push('Passive Perception '+pas('perception','wis'));
  senses.push('Passive Insight '+pas('insight','wis'));
  senses.push('Passive Investigation '+pas('investigation','int'));
  line(sb,'Senses',senses.join('; '));
  var langs=['Common'];(d.language_proficiencies||[]).forEach(function(x){if(norm(x)!=='common')langs.push(title(x))});
  line(sb,'Languages',langs.join(', '));
  var tags=el('div');
  if(p.is_dead||d.is_dead)tags.appendChild(el('span','tag bad','Dead'));
  if(d.inspiration)tags.appendChild(el('span','tag','Heroic Inspiration'));
  (d.conditions||[]).forEach(function(c){tags.appendChild(el('span','tag bad',title(c)))});
  if(d.exhaustion_level)tags.appendChild(el('span','tag bad','Exhaustion '+d.exhaustion_level));
  if(tags.childNodes.length)sb.appendChild(tags);

  // Spellcasting and resources
  var hasCaster=d.spell_slots||d.pact_magic||d.known_spells||d.prepared_spells;
  if(hasCaster){
    sb.appendChild(el('div','sbh','Spellcasting'));
    var abil=d.spellcasting_ability?({INT:'Intelligence',WIS:'Wisdom',CHA:'Charisma'}[String(d.spellcasting_ability).toUpperCase()]||title(d.spellcasting_ability)):'';
    line(sb,'Spellcasting Ability',abil);
    if(d.spell_save_dc)line(sb,'Spell Save DC',d.spell_save_dc);
    if(d.spell_attack_bonus!==undefined)line(sb,'Spell Attack Modifier',sgn(d.spell_attack_bonus));
    if(d.concentrating_on)line(sb,'Concentrating on',title(d.concentrating_on));
    [['Spell Slots',d.spell_slots],['Pact Magic Slots',d.pact_magic]].forEach(function(pair){var o=pair[1];if(!o||!o.total)return;
      Object.keys(o.total).sort().forEach(function(k){var q=el('p');add(q,el('b','l',pair[0]==='Spell Slots'?ordinal(+k)+'-level slots ':'Pact '+ordinal(+k)+'-level slots '),pips(o.total[k],(o.remaining||{})[k]));sb.appendChild(q)})})}
  (d.class_resources||[]).forEach(function(rs){var q=el('p');add(q,el('b','l',rs.name+' '),pips(rs.max||0,rs.current||0),document.createTextNode(' ('+(rs.current||0)+' of '+(rs.max||0)+')'));sb.appendChild(q)});

  // Actions, Bonus Actions, Reactions, Options
  sb.appendChild(el('div','sbh','Actions'));
  var ap2=el('p');add(ap2,el('b','n','Attack. '),document.createTextNode(S.attack_action||'You can make one attack when you take this action.'));sb.appendChild(ap2);
  var ul=el('ul','atk');(S.attacks||[]).forEach(function(a){var li=el('li');add(li,el('b','n',a.name+'. '),document.createTextNode(a.description));ul.appendChild(li)});sb.appendChild(ul);
  function names(list){list.forEach(function(e){var q=el('div','ent');add(q,el('b','n',e.name+'. '),el('span','src',sourceTag(e)));sb.appendChild(q)})}
  names(features.filter(function(f){return f.kind==='action'}));
  var bon=features.filter(function(f){return f.kind==='bonus_action'});
  if(bon.length){sb.appendChild(el('div','sbh','Bonus Actions'));names(bon)}
  var rea=features.filter(function(f){return f.kind==='reaction'});
  if(rea.length){sb.appendChild(el('div','sbh','Reactions'));names(rea)}
  statcol.appendChild(sb);

  // Equipment by location, with total weight
  var eb=el('div','box');eb.appendChild(el('h3','boxname eq','Equipment'));
  var lay=p.equipment_layout||{worn:[],held:[],containers:[],unsorted:[],lost:[]};
  function group(label,note,items){if(!items||!items.length)return;var h=el('div','loc',label);if(note)h.appendChild(el('small',null,' — '+note));eb.appendChild(h);eb.appendChild(itemList(items))}
  group('Worn or carried','on the body: worn, sheathed, slung or on the belt, not in hand',lay.worn);
  if(lay.held&&lay.held.length){group('Held','in hand',lay.held)}else{var hh=el('div','loc','Held');hh.appendChild(el('small',null,' — in hand: nothing (both hands free)'));eb.appendChild(hh)}
  (lay.containers||[]).forEach(function(c){
    var h=el('div','loc',title(c.name));var meta=[];if(c.carried)meta.push(c.carried);if(c.capacity)meta.push('holds '+c.capacity);if(c.capacity_lbs&&c.carried_lbs!==undefined)meta.push(c.carried_lbs+' of '+c.capacity_lbs+' pounds used');if(!c.carried)meta.push('not on the body');
    h.appendChild(el('small',null,' — '+meta.join('; ')));eb.appendChild(h);
    eb.appendChild(c.contents.length?itemList(c.contents):el('p','muted','Empty.'))});
  group('Not yet sorted','not assigned to the body or a bag',lay.unsorted);
  var cur=d.currency,cs=[];if(cur){[['pp','Platinum'],['gp','Gold'],['ep','Electrum'],['sp','Silver'],['cp','Copper']].forEach(function(k){if(cur[k[0]])cs.push(cur[k[0]]+' '+k[1])})}
  var q2=el('p');add(q2,el('b','l','Coin '),document.createTextNode(cs.length?cs.join(', '):'None'));eb.appendChild(q2);
  var total=weightOf(lay.worn)+weightOf(lay.held)+weightOf(lay.containers)+weightOf(lay.unsorted);
  var q3=el('p');add(q3,el('b','l','Weight carried '),document.createTextNode(Math.round(total*100)/100+' pounds'+(d.stats&&d.stats.str?' of '+d.stats.str*15+' pound capacity':'')));eb.appendChild(q3);
  statcol.appendChild(eb);

  add(sheet,textcol,statcol);root.appendChild(sheet);
}
})();
</script>
`
