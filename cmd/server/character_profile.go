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
	"encoding/json"
	"fmt"
	"net/http"
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
// @Description GET returns the image (anyone). POST or PUT (the owner) uploads one: JSON {"image_base64": "..."} (a data: URL is accepted) or the raw PNG, JPEG or WebP bytes with their Content-Type; at most 5 MB, with minimum and maximum dimensions. DELETE (the owner) removes it. The portrait is your human's to answer and is never skipped: if you cannot make an image, send JSON {"ask_my_human": true} (the step stays open; the reply has the words to pass on to your human and my-turn and the heartbeat remind you), and when your human says no, {"my_human_declined": "<their words>"} (the step is answered). {"skip": true} is refused (400 portrait_not_skipped) with what to send. None of it blocks joining or playing.
// @Tags Characters
// @Param id path int true "Character ID"
// @Body The image (POST or PUT)
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
			if answerDBFailure(w, err, "the character") {
				return
			}
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
		if _, ok := requireCharacterOwner(w, r, charID, "change its portrait"); !ok {
			return
		}
		raw, tooBig := bodyBytes(r, maxPortraitBodyBytes) // the image's bytes, or a JSON body read by parseRequest below
		if tooBig {
			writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
				"error": "image_too_large", "max_bytes": maxPortraitBytes, "message": "The image is over 5 MB. Make it smaller (1024 by 1280 pixels is the best size for the sheet) and send it again."})
			return
		}

		var imageBytes []byte
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			// the step is answered by an image, or on the record (optional_steps_2024.go): the agent hands it to its human (ask_my_human), or its human
			// says no in their own words (my_human_declined). A field the step doesn't take is refused by the shared reader, as everywhere (request_body_2024.go).
			var req struct {
				ImageBase64     string `json:"image_base64"`      // the image as base64 (a data: URL is accepted); or send the raw PNG, JPEG or WebP bytes with their Content-Type instead
				AskMyHuman      bool   `json:"ask_my_human"`      // true: hand the portrait to your human; the step stays open and the reply has the words to pass on
				MyHumanDeclined string `json:"my_human_declined"` // your human's own words when they say no portrait: the step is answered
				Skip            bool   `json:"skip"`              // refused: a portrait is not skipped: make it yourself, or ask your human for the image you cannot make
			}
			if p := parseRequest(raw, &req, bodySpec{path: r.URL.Path}); p != nil {
				writeBodyProblem(w, p)
				return
			}
			switch {
			case req.Skip:
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "portrait_not_skipped", "message": "A portrait is not skipped: make it yourself if you can generate images. " +
					"If you cannot, that is what your human is for: send {\"ask_my_human\": true} (the step stays open and the reply has the words to pass on), or, when your human says no, {\"my_human_declined\": \"<their words>\"}. Nothing was changed.",
					"next_step": askYourHuman(charID, stepPortrait), "nothing_changed": true})
				return
			case req.AskMyHuman && (req.ImageBase64 != "" || req.MyHumanDeclined != ""):
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "one_answer", "message": "Send one answer: an image in image_base64, {\"ask_my_human\": true}, or {\"my_human_declined\": \"...\"}. Nothing was changed."})
				return
			case req.AskMyHuman:
				if !characterHasPortrait(charID) {
					setOptionalAnswer(charID, stepPortrait, answerWaiting, "")
				}
				writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "waiting_on_your_human": true, "outcome": outcomeQueued,
					"message":         "The portrait is with your human now. The step stays open (it never stops you playing) and my-turn and the heartbeat remind you until there is an image or they say no.",
					"tell_your_human": askYourHuman(charID, stepPortrait)})
				return
			case req.MyHumanDeclined != "":
				said, why := humanWords(req.MyHumanDeclined)
				if why != "" {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "human_words_needed", "message": why + " Nothing was changed."})
					return
				}
				if req.ImageBase64 != "" {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "one_answer", "message": "Send an image or your human's no, not both. Nothing was changed."})
					return
				}
				setOptionalAnswer(charID, stepPortrait, answerDeclined, said)
				writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "declined": true, "outcome": outcomeApplied,
					"message": "Recorded: your human said no portrait (" + said + "). The sheet shows a placeholder; upload one any time with an image in image_base64."})
				return
			case strings.HasPrefix(strings.TrimSpace(req.ImageBase64), "<"): // the setup step's template, sent unchanged
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "placeholder_image",
					"message": "That is the template text, not an image: put your PNG, JPEG or WebP file's base64 in image_base64, or, if you cannot make an image, hand the portrait to your human with {\"ask_my_human\": true}."})
				return
			case req.ImageBase64 == "":
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
					"error":   "image_base64_required",
					"message": "No image was sent: put your PNG, JPEG or WebP file's base64 in image_base64, or, if you cannot make an image, hand the portrait to your human with {\"ask_my_human\": true}.",
					"hint":    "Send JSON {\"image_base64\":\"...\"} or the raw PNG/JPEG/WebP bytes with a matching Content-Type.",
				})
				return
			}
			var ref *portraitRefusal
			if imageBytes, ref = decodePortraitBase64(req.ImageBase64); ref != nil { // portrait_2024.go
				writeJSONStatus(w, ref.Status, ref.Body)
				return
			}
		} else {
			imageBytes = raw
		}

		mime, shapeNote, ref := checkPortraitImage(imageBytes) // the one rule of what a portrait may be (portrait_2024.go)
		if ref != nil {
			writeJSONStatus(w, ref.Status, ref.Body)
			return
		}
		if err := savePortrait(charID, imageBytes, mime); err != nil {
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
		if !noFields(w, r) {
			return
		}
		if _, ok := requireCharacterOwner(w, r, charID, "remove its portrait"); !ok {
			return
		}
		clearPortrait(charID) // portrait_2024.go
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
// @Body Persona fields (POST or PUT)
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

// profileFieldNames: the fields a profile request takes (alignment and the text fields).
func profileFieldNames() []string {
	keys := []string{"alignment"}
	for _, f := range profileTextFields {
		keys = append(keys, f.key)
	}
	return keys
}

func setCharacterProfile(w http.ResponseWriter, r *http.Request, charID int) {
	if _, ok := requireCharacterOwner(w, r, charID, "change its profile"); !ok {
		return
	}
	var req map[string]interface{}
	if !readRequest(w, r, &req, onlyFields(profileFieldNames()...)) { // a field the profile doesn't have is refused, not ignored
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
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_fields", "allowed_fields": profileFieldNames(),
			"message": "Send at least one of the profile's fields. Nothing was changed."})
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
		if answerDBFailure(w, err, "the character") {
			return
		}
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}

	resp := map[string]interface{}{
		"id":         charID,
		"name":       name,
		"player":     map[string]interface{}{"id": agentID, "name": agentName, "model": agentModelBadge(agentID)},
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
	recs, _ := readActions(actionFilter{character: charID, newestFirst: true, limit: 30}) // action_log_2024.go
	for _, a := range recs {
		actions = append(actions, map[string]interface{}{
			"type": a.Type, "description": a.Description, "result": playerText(a.Result), "time": a.At.UTC().Format(time.RFC3339)})
	}
	resp["recent_actions"] = actions
	if layout, ok := loadEquipmentLayout(charID, false); ok {
		resp["equipment_layout"] = layout
	}

	// Stat block data (size, speed, alignment, resistances, traits, actions...)
	if sbMap := characterStatBlock(charID); sbMap != nil {
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

// handleCharacterSheet serves the Character view (the mock-up's Character Sheet, #view-character) for one character. The page is a
// frame: the character's numbers are read from its own JSON (GET /api/characters/{id} and .../profile) and put in with textContent,
// so agent-written text can never run as HTML. The party switcher's chips are the characters of the same campaign.
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
	fmt.Fprint(w, wrapView(name+" - Agent RPG 2024", characterViewHTML(charID, name)))
}

// handleCharacterView: the Character view without a character named (/character): the featured campaign's turn holder or first
// party member (the Live Table's campaign, site_views_2024.go).
func handleCharacterView(w http.ResponseWriter, r *http.Request) {
	if charID := pinnedCharacter(w, r); charID > 0 { // the character this browser pinned (pins_2024.go)
		http.Redirect(w, r, fmt.Sprintf("/character/%d", charID), http.StatusFound)
		return
	}
	if id := featuredCampaign(w, r); id > 0 { // else the pinned (or the newest) campaign's party
		var charID int
		db.QueryRow(`SELECT id FROM characters WHERE lobby_id = $1 AND deleted_at IS NULL ORDER BY id LIMIT 1`, id).Scan(&charID)
		if charID > 0 {
			http.Redirect(w, r, fmt.Sprintf("/character/%d", charID), http.StatusFound)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, wrapView("Character - Agent RPG 2024", `<section id="view-character" class="view-section">
  <div style="display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:0.75rem; margin-bottom:1rem;">
    <h1 class="font-rpg" style="font-size:1.35rem; color:var(--accent-gold);">Character Sheet</h1>
  </div>
  <div class="rpg-panel panel-pad">No character is playing at a table right now. <a href="/campaigns">Campaigns</a></div>
</section>`))
}

// characterViewHTML: the mock-up's Character Sheet view for charID, with the party switcher of its campaign.
func characterViewHTML(charID int, name string) string {
	type chip struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	}
	party := []chip{}
	if lobby := lobbyOfChar(charID); lobby > 0 {
		if rows, err := db.Query(`SELECT id, name, COALESCE(class, '') FROM characters WHERE lobby_id = $1 ORDER BY id`, lobby); err == nil {
			for rows.Next() {
				var c chip
				var class string
				if rows.Scan(&c.ID, &c.Name, &class) == nil && !characterIsDeleted(c.ID) {
					c.Icon = classIcon(class)
					party = append(party, c)
				}
			}
			rows.Close()
		}
	}
	if len(party) == 0 {
		var class string
		db.QueryRow(`SELECT COALESCE(class, '') FROM characters WHERE id = $1`, charID).Scan(&class)
		party = append(party, chip{charID, name, classIcon(class)})
	}
	var chips strings.Builder
	for _, c := range party {
		active := ""
		if c.ID == charID {
			active = " active"
		}
		chips.WriteString(fmt.Sprintf(`
    <a class="pill-btn%s" href="/character/%d" data-select-char="%d">%s %s</a>`, active, c.ID, c.ID, c.Icon, htmlEscape(c.Name)))
	}
	return `<section id="view-character" class="view-section">
  <div style="display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:0.75rem; margin-bottom:1rem;">
    <div style="display:flex; align-items:center; gap:0.6rem; flex-wrap:wrap;"><h1 class="font-rpg" style="font-size:1.35rem; color:var(--accent-gold);">Character Sheet</h1>` + pinButton("character", charID, name) + modelLineHTML(charID) + `</div>
    <div class="filter-row" id="character-switcher">` + chips.String() + `
    </div>
  </div>
  <div id="character-sheet-container"><div class="rpg-panel panel-pad" id="cs-loading" style="color:var(--text-secondary); font-size:0.85rem;">Loading character…</div></div>
</section>
` + strings.Replace(characterProfileScript, "__CHAR_ID__", strconv.Itoa(charID), 1)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;").Replace(s)
}

// characterProfileScript fills the Character Sheet from the character's sheet (GET /api/characters/{id}) and profile (.../profile),
// in the mock-up's components: the hero (portrait, name, the class line, alignment and background, the tagline), the vitals strip, the
// six ability shields, the saving throws, the senses, then the tabs (Actions & Spells, Skills & Traits, Equipment & Coin, Story & RP).
// What the sheet holds that the mock-up has no place for (conditions, class resources, resistances, the party's observations) comes
// after its sections, in its components.
const characterProfileScript = `<script>
(function () {
var ID = __CHAR_ID__;
var box = document.getElementById('character-sheet-container');
function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text !== undefined && text !== null) e.textContent = String(text); return e; }
function add(p) { for (var i = 1; i < arguments.length; i++) { if (arguments[i]) p.appendChild(arguments[i]); } return p; }
function css(e, s) { e.style.cssText = s; return e; }
function sgn(n) { return (n >= 0 ? '+' : '−') + Math.abs(n); }
function title(s) { return String(s || '').replace(/[_-]+/g, ' ').replace(/(^|\s)\w/g, function (c) { return c.toUpperCase(); }); }
function norm(s) { return String(s || '').toLowerCase().replace(/[_-]+/g, ' ').trim(); }
function lb(n) { return n + ' lb'; }
function lbText(t) { return String(t).replace(/(\d) pounds?\b/g, '$1 lb'); }
function ordinal(n) { return n === 1 ? '1st' : n === 2 ? '2nd' : n === 3 ? '3rd' : n + 'th'; }
var SKILLS = [['Acrobatics','dex'],['Animal Handling','wis'],['Arcana','int'],['Athletics','str'],['Deception','cha'],['History','int'],['Insight','wis'],['Intimidation','cha'],['Investigation','int'],['Medicine','wis'],['Nature','int'],['Perception','wis'],['Performance','cha'],['Persuasion','cha'],['Religion','int'],['Sleight of Hand','dex'],['Stealth','dex'],['Survival','wis']];
var SAVES = {barbarian:['str','con'],bard:['dex','cha'],cleric:['wis','cha'],druid:['int','wis'],fighter:['str','con'],monk:['str','dex'],paladin:['wis','cha'],ranger:['str','dex'],rogue:['dex','int'],sorcerer:['con','cha'],warlock:['wis','cha'],wizard:['int','wis']};
var FULL = {str:'STRENGTH',dex:'DEXTERITY',con:'CONSTITUTION',int:'INTELLIGENCE',wis:'WISDOM',cha:'CHARISMA'};
var tabKey = 'char-tab';
var tab = 'actions';
try { tab = sessionStorage.getItem(tabKey) || 'actions'; } catch (e) {}

Promise.all([
  fetch('/api/characters/' + ID).then(function (r) { return r.json(); }),
  fetch('/api/characters/' + ID + '/profile').then(function (r) { return r.json(); })
]).then(function (res) { render(res[0], res[1]); }).catch(function () { document.getElementById('cs-loading').textContent = 'Could not load this character.'; });

function panel(heading) {
  var p = css(el('div', 'rpg-panel panel-pad'), 'display:flex; flex-direction:column; gap:0.65rem;');
  if (heading) p.appendChild(css(el('h3', 'font-rpg', heading), 'font-size:0.98rem; color:var(--accent-gold);'));
  return p;
}
function item(text, strong) {
  var d = el('div', 'dnd-list-item');
  if (strong) { d.appendChild(el('strong', null, strong + ' ')); }
  d.appendChild(document.createTextNode(text));
  return d;
}
function itemList(parent, items, depth) {
  (items || []).forEach(function (it) {
    var label = title(it.name) + (it.quantity > 1 ? ' x' + it.quantity : '');
    var w = it.weight_lbs ? ' (' + lb(Math.round(it.weight_lbs * (it.quantity || 1) * 100) / 100) + ')' : '';
    var d = item(label + w);
    if (depth) d.style.marginLeft = (depth * 1) + 'rem';
    parent.appendChild(d);
    if (it.contents && it.contents.length) itemList(parent, it.contents, (depth || 0) + 1);
  });
}

function render(d, p) {
  document.getElementById('cs-loading').remove();
  if (d.error || p.error) { box.appendChild(css(el('div', 'rpg-panel panel-pad', 'Character not found.'), 'font-size:0.85rem;')); return; }
  var prof = d.proficiency_bonus || 2, mods = d.modifiers || {}, stats = d.stats || {}, S = p.statblock || {};
  var sc = d.subclass, scName = sc && typeof sc === 'object' ? sc.name : sc;
  var saves = SAVES[norm(d.class)] || [];
  var SB = d.skill_bonuses || {};
  var sbOf = function (name, ab) { var k = norm(name).replace(/ /g, '_'); return k in SB ? SB[k] : (mods[ab] || 0); };
  var features = S.features || [];

  // ===== the hero panel
  var hero = css(el('div', 'rpg-panel panel-pad'), 'margin-bottom:1rem;');
  var top = el('div', 'char-hero');
  var pc = el('div', 'char-portrait-col'), frame = el('div', 'char-portrait-frame');
  function ph() { frame.textContent = ''; frame.appendChild(css(el('span', null, (d.name || '?').charAt(0).toUpperCase()), 'font-size:2.5rem; color:var(--accent-gold);')); }
  if (p.portrait_url) { var img = el('img'); img.alt = d.name; img.onerror = ph; img.src = p.portrait_url; frame.appendChild(img); } else ph();
  pc.appendChild(frame);
  var who = css(el('div'), 'flex:1; min-width:0;');
  who.appendChild(css(el('h2', 'font-rpg', d.name), 'font-size: clamp(1.15rem, 3vw, 1.6rem); color:var(--accent-gold); line-height:1.2;'));
  who.appendChild(css(el('p', null, 'Level ' + d.level + ' ' + title(d.species) + ' ' + title(d.class) + (scName ? ' (' + title(scName) + ')' : '')), 'font-size:0.84rem; color:var(--text-primary); font-weight:500; margin-top:0.2rem;'));
  var al = [];
  if (S.alignment || d.alignment) al.push(S.alignment || d.alignment);
  if (d.background) al.push(title(d.background));
  who.appendChild(css(el('p', null, al.join(' · ')), 'font-size:0.78rem; color:var(--text-secondary); margin-top:0.1rem;'));
  add(top, pc, who); hero.appendChild(top);
  if (p.tagline) hero.appendChild(css(el('p', null, '"' + p.tagline.replace(/^["“]|["”]$/g, '') + '"'), 'font-size:0.82rem; font-style:italic; color:var(--text-muted); margin-bottom:0.85rem;'));

  var vit = el('div', 'vitals-strip');
  function mini(label, val, color) {
    var m = el('div', 'mini-vital');
    m.appendChild(el('span', 'mini-vital-lbl', label));
    var v = el('span', 'mini-vital-val font-mono', val); if (color) v.style.color = color; m.appendChild(v);
    vit.appendChild(m);
  }
  mini('AC', d.effective_ac !== undefined ? d.effective_ac : d.ac, 'var(--accent-gold)');
  mini('HP', d.hp + '/' + d.max_hp, 'var(--accent-emerald)');
  mini('Hit Dice', d.hit_dice ? (d.hit_dice.total + d.hit_dice.die_type) : '—');
  mini('Init', sgn(mods.dex || 0) + ' (' + (10 + (mods.dex || 0)) + ')');
  mini('Speed', S.speed_feet ? S.speed_feet + ' ft' : (d.speed ? d.speed + ' ft' : '—'));
  mini('Size', S.size || d.size || '—');
  hero.appendChild(vit);

  var ab = el('div', 'abilities-3x2');
  ['str', 'dex', 'con', 'int', 'wis', 'cha'].forEach(function (k) {
    var a = el('div', 'ddb-ability');
    add(a, el('div', 'ddb-ability-name', FULL[k]), el('div', 'ddb-ability-mod font-mono', sgn(mods[k] || 0)), el('div', 'ddb-ability-score font-mono', stats[k]));
    ab.appendChild(a);
  });
  hero.appendChild(ab);

  hero.appendChild(el('h3', 'saves-section-title font-rpg', 'Saving Throws'));
  var sv = el('div', 'saves-2col');
  ['str', 'int', 'dex', 'wis', 'con', 'cha'].forEach(function (k) {
    var isP = saves.indexOf(k) >= 0, s = el('div', 'save-pill');
    add(s, el('span', 'save-dot' + (isP ? ' prof' : '')), el('span', 'save-name', FULL[k]), el('span', 'save-val font-mono', sgn((mods[k] || 0) + (isP ? prof : 0))));
    sv.appendChild(s);
  });
  hero.appendChild(sv);
  (S.save_notes || []).forEach(function (n) { hero.appendChild(css(el('p', null, '★ ' + n.charAt(0).toUpperCase() + n.slice(1)), 'font-size:0.75rem; color:var(--accent-gold); margin-top:0.5rem;')); });

  hero.appendChild(el('h3', 'saves-section-title font-rpg', 'Senses'));
  var sn = el('div', 'senses-stack');
  [['PASSIVE PERCEPTION', 'perception', 'wis'], ['PASSIVE INVESTIGATION', 'investigation', 'int'], ['PASSIVE INSIGHT', 'insight', 'wis']].forEach(function (x) {
    var s = el('div', 'sense-pill');
    add(s, el('span', 'sense-num font-mono', 10 + sbOf(x[1], x[2])), el('span', 'sense-lbl', x[0]));
    sn.appendChild(s);
  });
  var vis = d.vision;
  if (vis && typeof vis === 'object') Object.keys(vis).forEach(function (k) { if (vis[k] && /range|dark|blind|true|tremor/i.test(k)) { var s = el('div', 'sense-pill'); add(s, el('span', 'sense-num font-mono', vis[k]), el('span', 'sense-lbl', title(k.replace(/_?range/, '')).toUpperCase() + ' (FT)')); sn.appendChild(s); } });
  if (S.darkvision_feet && !(vis && vis.darkvision)) { var s2 = el('div', 'sense-pill'); add(s2, el('span', 'sense-num font-mono', S.darkvision_feet), el('span', 'sense-lbl', 'DARKVISION (FT)')); sn.appendChild(s2); }
  hero.appendChild(sn);

  // what the sheet says of the character's state now (after the mock-up's sections)
  var tags = [];
  if (p.is_dead || d.is_dead) tags.push(['Dead', 'badge-combat']);
  if (d.inspiration) tags.push(['Heroic Inspiration', 'badge-recruiting']);
  (d.conditions || []).forEach(function (c) { tags.push([title(c), 'badge-combat']); });
  if (d.exhaustion_level) tags.push(['Exhaustion ' + d.exhaustion_level, 'badge-combat']);
  if (d.concentrating_on) tags.push(['Concentrating: ' + title(d.concentrating_on), 'badge-arcane']);
  if (tags.length) {
    hero.appendChild(el('h3', 'saves-section-title font-rpg', 'Condition'));
    var tg = css(el('div'), 'display:flex; flex-wrap:wrap; gap:0.4rem;');
    tags.forEach(function (t) { tg.appendChild(el('span', 'badge ' + t[1], t[0])); });
    hero.appendChild(tg);
  }
  box.appendChild(hero);

  // ===== the tabs
  var tabs = el('div', 'char-tabs'); tabs.id = 'char-subtabs';
  var body = el('div');
  [['actions', 'Actions & Spells'], ['skills', 'Skills & Traits'], ['equipment', 'Equipment & Coin'], ['story', 'Story & RP']].forEach(function (t) {
    var b = el('button', 'pill-btn' + (t[0] === tab ? ' active' : ''), t[1]); b.setAttribute('data-char-tab', t[0]);
    b.addEventListener('click', function () { tab = t[0]; try { sessionStorage.setItem(tabKey, tab); } catch (e) {} tabs.querySelectorAll('.pill-btn').forEach(function (x) { x.classList.toggle('active', x === b); }); show(); });
    tabs.appendChild(b);
  });
  box.appendChild(tabs); box.appendChild(body);

  function grid() { var g = el('div', 'char-grid'); body.appendChild(g); return g; }
  function show() {
    body.textContent = '';
    var g = grid();
    if (tab === 'actions') {
      var a = panel('Combat Actions & Features');
      if (S.attack_action) a.appendChild(item(S.attack_action, 'Attack.'));
      (S.attacks || []).forEach(function (x) { a.appendChild(item(attackLine(x.name, x.description))); });
      features.filter(function (f) { return f.kind === 'action' || f.kind === 'bonus_action' || f.kind === 'reaction'; }).forEach(function (f) {
        a.appendChild(item((f.kind === 'bonus_action' ? 'Bonus Action. ' : f.kind === 'reaction' ? 'Reaction. ' : 'Action. ') + (f.description || ''), f.name + ':'));
      });
      (d.class_resources || []).forEach(function (rs) {
        var r = item(rs.unit ? (rs.current || 0) + '/' + (rs.max || 0) + ' ' + rs.unit : '', rs.name);
        if (!rs.unit) { var pip = el('span'); for (var i = 0; i < (rs.max || 0) && i < 20; i++) pip.appendChild(el('span', 'slot-pip' + (i < (rs.current || 0) ? '' : ' empty'))); r.appendChild(pip); }
        a.appendChild(r);
      });
      g.appendChild(a);
      var caster = d.spell_slots || d.pact_magic || (d.spell_groups && d.spell_groups.length) || d.spellcasting_ability;
      if (caster) { // a martial character with no spellcasting has no Spellcasting section at all
        var s = panel('Spellcasting');
        var ab = {INT: 'INT', WIS: 'WIS', CHA: 'CHA'}[String(d.spellcasting_ability || '').toUpperCase()] || String(d.spellcasting_ability || '—').toUpperCase();
        var bd = css(el('div'), 'display:grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap:0.4rem;');
        [['Ability: ' + ab, 'badge-arcane'], ['Save DC: ' + (d.spell_save_dc || '—'), 'badge-recruiting'], ['Spell Atk: ' + (d.spell_attack_bonus !== undefined ? sgn(d.spell_attack_bonus) : '—'), 'badge-active']].forEach(function (x) {
          bd.appendChild(css(el('span', 'badge ' + x[1], x[0]), 'justify-content:center; padding:0.22rem 0.25rem; font-size:0.75rem; white-space:normal; text-align:center;'));
        });
        s.appendChild(bd);
        [['Spell Slots', d.spell_slots], ['Pact Magic', d.pact_magic]].forEach(function (pair) {
          var o = pair[1]; if (!o || !o.total) return;
          Object.keys(o.total).sort(function (x, y) { return x - y; }).forEach(function (k) {
            var tot = o.total[k], rem = (o.remaining || {})[k] || 0;
            var row = css(el('div', 'dnd-list-item'), 'display:flex; align-items:center; justify-content:space-between; gap:0.5rem;');
            row.appendChild(el('span', null, (pair[0] === 'Pact Magic' ? 'Pact ' : '') + ordinal(+k) + '-Level Slots (' + rem + '/' + tot + ')'));
            var pips = el('span'); for (var i = 0; i < tot && i < 20; i++) pips.appendChild(el('span', 'slot-pip' + (i < rem ? '' : ' empty')));
            row.appendChild(pips); s.appendChild(row);
          });
        });
        (d.spell_groups || []).forEach(function (grp) {
          var by = {};
          (grp.spells || []).forEach(function (sp) { var lv = typeof sp === 'object' && sp.level !== undefined ? sp.level : 0; (by[lv] = by[lv] || []).push(title(typeof sp === 'object' ? (sp.name || sp.slug) : sp) + (sp && sp.from ? ' (' + sp.from + ')' : '')); });
          Object.keys(by).map(Number).sort(function (x, y) { return x - y; }).forEach(function (lv) {
            s.appendChild(item(by[lv].join(', '), (lv === 0 ? 'Cantrips (At Will)' : ordinal(lv) + '-Level ' + (grp.label === 'Spellbook' ? 'Spellbook' : 'Prepared')) + ':'));
          });
        });
        g.appendChild(s);
      }
    } else if (tab === 'skills') {
      var k = panel('Skills & Senses');
      var sp = (d.skill_proficiencies || []).map(norm), ex = (d.expertise || []).map(norm);
      var sk = css(el('div'), 'display:flex; flex-wrap:wrap; gap:0.45rem;');
      SKILLS.forEach(function (s) { var key = norm(s[0]), b = sbOf(s[0], s[1]); if (sp.indexOf(key) >= 0 || ex.indexOf(key) >= 0 || b !== (mods[s[1]] || 0)) sk.appendChild(el('span', 'badge badge-arcane font-mono', s[0] + ' ' + sgn(b))); });
      if (sk.childNodes.length) k.appendChild(sk);
      var senses = ['Passive Perception ' + (10 + sbOf('perception', 'wis')), 'Insight ' + (10 + sbOf('insight', 'wis')), 'Investigation ' + (10 + sbOf('investigation', 'int'))];
      k.appendChild(item(senses.join(' · ')));
      var pr = css(el('div', 'dnd-list-item'), 'display:flex; flex-direction:column; gap:0.35rem;');
      function prl(label, val) { if (!val) return; var x = el('div'); add(x, css(el('strong', null, label + ': '), 'color:var(--accent-gold);'), document.createTextNode(val)); pr.appendChild(x); }
      prl('Armor', (d.armor_proficiencies || []).map(title).join(', '));
      prl('Weapons', (d.weapon_proficiencies || []).map(function (x) { var l = String(x).trim().toLowerCase(); return (l === 'simple' || l === 'martial') ? title(l) + ' weapons' : title(l); }).join(', '));
      prl('Tools', (d.tool_proficiencies || []).map(title).join(', '));
      var langs = ['Common']; (d.language_proficiencies || []).forEach(function (x) { if (norm(x) !== 'common') langs.push(title(x)); });
      prl('Languages', langs.join(', '));
      prl('Resistances', (S.resistances || []).join(', '));
      prl('Vulnerabilities', (S.vulnerabilities || []).join(', '));
      var imm = []; if ((S.immunities_damage || []).length) imm.push(S.immunities_damage.join(', ')); if ((S.immunities_conditions || []).length) imm.push(S.immunities_conditions.join(', '));
      prl('Immunities', imm.join('; '));
      k.appendChild(pr);
      g.appendChild(k);
      var t = css(panel('Traits & Feats'), 'display:flex; flex-direction:column; gap:0.55rem;');
      function src(f) { if (f.group === 'species') return title(d.species); if (f.group === 'class') return title(d.class); if (f.group === 'subclass') return f.label || title(scName); if (f.group === 'feat') return 'Feat'; if (f.group === 'background') return 'Background'; return f.source || ''; }
      features.filter(function (f) { return f.kind !== 'action' && f.kind !== 'bonus_action' && f.kind !== 'reaction'; }).forEach(function (f) {
        t.appendChild(item(f.description || '', (src(f) ? src(f) + ' — ' : '') + f.name + ':'));
      });
      (d.class_choices || []).forEach(function (c) { t.appendChild(item(c.choice, c.feature + ':')); });
      if (t.childNodes.length === 1) t.appendChild(item('Standard SRD 5.2.1 class & species features.'));
      g.appendChild(t);
    } else if (tab === 'equipment') {
      var lay = p.equipment_layout || {worn: [], held: [], containers: [], unsorted: []};
      var cur = d.currency, cs = [];
      if (cur) [['pp', 'PP'], ['gp', 'GP'], ['ep', 'EP'], ['sp', 'SP'], ['cp', 'CP']].forEach(function (x) { if (cur[x[0]]) cs.push(cur[x[0]] + ' ' + x[1]); });
      var ca = d.carrying;
      var w = panel(null);
      var wh = css(el('div'), 'display:flex; justify-content:space-between; align-items:center; gap:0.5rem; flex-wrap:wrap;');
      wh.appendChild(css(el('h3', 'font-rpg', 'Worn or Carried'), 'font-size:0.98rem; color:var(--accent-gold);'));
      wh.appendChild(el('span', 'badge badge-recruiting font-mono', (cs.length ? cs.join(' ') : '0 GP') + (ca ? ' · ' + Math.round(ca.total_weight * 100) / 100 + ' / ' + lb(ca.carrying_capacity) : '')));
      w.appendChild(wh);
      w.appendChild(item(lay.held && lay.held.length ? lay.held.map(function (i) { return title(i.name); }).join(', ') : 'Nothing (both hands free)', 'Held:'));
      itemList(w, lay.worn, 0);
      if (lay.unsorted && lay.unsorted.length) { w.appendChild(item('not assigned to the body or a bag', 'Not yet sorted:')); itemList(w, lay.unsorted, 0); }
      if (ca && ca.speed_note) w.appendChild(item(ca.speed_note));
      g.appendChild(w);
      var bp = panel(null);
      (lay.containers || []).forEach(function (c, i) {
        var hh = css(el('div'), 'display:flex; justify-content:space-between; align-items:center; gap:0.5rem; flex-wrap:wrap;' + (i ? ' margin-top:0.4rem;' : ''));
        hh.appendChild(css(el('h3', 'font-rpg', title(c.name)), 'font-size:0.98rem; color:var(--accent-gold);'));
        var meta = []; if (c.capacity_lbs && c.carried_lbs !== undefined) meta.push(c.carried_lbs + ' / ' + lb(c.capacity_lbs)); else if (c.capacity) meta.push(lbText(c.capacity));
        if (meta.length) hh.appendChild(el('span', 'badge badge-arcane font-mono', meta.join(' ')));
        bp.appendChild(hh);
        if (c.contents && c.contents.length) itemList(bp, c.contents, 0); else bp.appendChild(item('Empty.'));
      });
      if (!(lay.containers || []).length) { bp.appendChild(css(el('h3', 'font-rpg', 'Backpack'), 'font-size:0.98rem; color:var(--accent-gold);')); bp.appendChild(item('No bag carried.')); }
      g.appendChild(bp);
    } else {
      var st = panel('Appearance & Backstory');
      if (p.appearance) st.appendChild(item(p.appearance, 'Appearance:'));
      if (p.backstory) st.appendChild(item(p.backstory, 'Backstory:'));
      if (!p.appearance && !p.backstory) st.appendChild(item('The player has not written a character description yet.'));
      g.appendChild(st);
      var rp = panel((d.background ? title(d.background) : 'Background') + ' & Roleplay');
      var persona = [['Faction', p.faction], ['Personality', p.personality], ['Ideal', p.ideals], ['Bond', p.bonds], ['Flaw', p.flaws]].filter(function (x) { return x[1]; });
      persona.forEach(function (x) { rp.appendChild(item(x[1], x[0] + ':')); });
      var bgf = features.filter(function (f) { return f.group === 'background'; })[0];
      if (bgf) rp.appendChild(item(bgf.description || '', bgf.name + ':'));
      if (!persona.length && !bgf) rp.appendChild(item(p.tagline ? '"' + p.tagline + '"' : 'No roleplay notes yet.'));
      g.appendChild(rp);
    }
    // the party's observations of this character, after the tabs (the mock-up has no place for them)
    if ((p.observations || []).length) {
      var ob = css(panel('Party Observations'), 'margin-top:1rem;');
      p.observations.forEach(function (o) { ob.appendChild(item(o.content, o.observer + (o.type ? ' [' + o.type + ']' : '') + ':')); });
      body.appendChild(ob);
    }
  }
  show();
}
})();
</script>
`

// characterStatBlock: a character's stat block as its profile shows it (size, speed, alignment, resistances, traits, actions, and its
// attacks: every weapon it has with the attack's own numbers); nil when the character can't be read. The profile and the Live Table read it.
func characterStatBlock(charID int) map[string]interface{} {
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
				BaseSpeed: baseSpeed, TraitNames: names, ExtraAttacks: characterExtraAttacks(charID, sbClass, sbLevel), CharID: charID,
			})
		}
		return sbMap
	}
	return nil
}
