package main

// background_library_2024.go: the library of scenes a campaign's background is chosen from (owner, Nov 26: "multiple backgrounds an agent can
// select when creating new campaigns", with tags to help it choose and never a campaign's or a book's name). One owner of the library and of a
// campaign's pick from it:
//
//   - The library is the table background_library: a key, a title and a sentence of description written for an agent, tags from the one
//     vocabulary bgVocabulary (place, mood, light, features), the picture stored as every background is (1920 by 1080 lossy WebP,
//     normalizeBackground) and a 480 by 270 thumbnail. The pictures the site ships are data/backgrounds (library.json and the WebP files),
//     put in at each start by seedBackgroundLibrary; an entry already there keeps the administrator's edits.
//   - A campaign's pick is lobbies.background_key, written only by setLibraryBackground. Which picture a campaign shows is backgroundURL
//     (campaign_background_2024.go): the administrator's upload, else the pick, else the default dragon.
//   - An agent lists the library with GET /api/backgrounds (tags and words to match, best first), names one in campaign creation's
//     `background`, or lets the server pick (pickBackground: the best match for the campaign's own words); the GM changes it with
//     POST /api/campaigns/{id}/background. The administrator picks one for a campaign (POST /api/admin/campaigns/{id}/background
//     {"library": key}) and adds, edits and retires entries (/api/admin/backgrounds). A retired entry is never offered again; the campaigns
//     that use it keep it.

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"
)

//go:embed data/backgrounds
var backgroundFiles embed.FS

// bgVocabulary: the one list of tag words, by group. An entry's tags come only from it (bgTagProblem), so an agent matches on the same words
// the library is tagged with. A new word is one more string here.
var bgVocabulary = []struct {
	Group string
	Words []string
}{
	{"place", []string{"castle", "city", "dungeon", "cavern", "forest", "swamp", "mountains", "arctic", "desert", "jungle", "coast", "sea", "underwater", "sky", "planes", "hell", "abyss", "feywild", "library", "village", "tavern", "ruins"}},
	{"mood", []string{"gothic", "horror", "heroic", "mystery", "whimsical", "war", "intrigue", "exploration", "epic", "peaceful"}},
	{"light", []string{"night", "day", "dusk", "storm", "fire", "dark"}},
	{"features", []string{"dragon", "undead", "giants", "demons", "devils", "ship", "airship", "magic", "heist", "school", "treasure", "monsters", "crowd", "temple", "portal", "ice", "lava"}},
}

// bgWordHints: words a campaign's own text may use for a tag ("vampire" for undead); read only by the matcher, never shown.
var bgWordHints = map[string][]string{
	"undead": {"vampire", "vampires", "zombie", "zombies", "skeleton", "skeletons", "ghost", "ghosts", "lich", "necromancer", "wraith"},
	"gothic": {"vampire", "haunted", "cursed", "curse"}, "horror": {"haunted", "dread", "terror", "nightmare"},
	"arctic": {"frozen", "snow", "winter", "ice", "tundra", "glacier"}, "sea": {"ocean", "pirate", "pirates", "sail", "voyage", "island"},
	"coast": {"port", "harbor", "harbour", "shore", "beach"}, "city": {"metropolis", "streets", "noble", "guild", "urban"},
	"cavern": {"underground", "caves", "cave", "underdark", "mine", "mines"}, "dungeon": {"crypt", "tomb", "labyrinth", "vault", "delve"},
	"hell": {"infernal", "nine"}, "abyss": {"demonic"}, "devils": {"devil", "infernal", "archdevil"}, "demons": {"demon", "demonic"},
	"feywild": {"fey", "faerie", "fairy", "carnival", "witchlight"}, "sky": {"clouds", "flying", "astral", "stars", "space"},
	"airship": {"flying", "space", "astral"}, "school": {"academy", "university", "students", "college"}, "library": {"books", "archive", "scholar", "scholars"},
	"heist": {"thieves", "thief", "robbery", "steal", "vault"}, "giants": {"giant", "titan"}, "dragon": {"dragons", "wyrm", "drake"},
	"jungle": {"tropical", "rainforest"}, "desert": {"sand", "dunes"}, "war": {"army", "armies", "battle", "siege"}, "intrigue": {"politics", "conspiracy", "noble"},
	"mystery": {"investigation", "detective", "secrets", "murder"}, "underwater": {"sunken", "drowned", "merfolk", "deep"},
	"tavern": {"inn", "taproom"}, "temple": {"shrine", "cathedral", "priest", "god", "gods"}, "planes": {"plane", "planar", "multiverse", "outer"},
}

const (
	bgThumbW = 480
	bgThumbH = 270
)

// bgEntry is one library picture as every reader sees it.
type bgEntry struct {
	Key, Title, Description string
	Tags                    map[string][]string
	Retired                 bool
	Updated                 time.Time
}

func (e bgEntry) words() []string {
	var out []string
	for _, g := range bgVocabulary {
		out = append(out, e.Tags[g.Group]...)
	}
	return out
}

// view is an entry as a reply shows it.
func (e bgEntry) view() map[string]interface{} {
	tags := map[string]interface{}{}
	for _, g := range bgVocabulary {
		tags[g.Group] = append([]string{}, e.Tags[g.Group]...)
	}
	v := e.Updated.Unix()
	return map[string]interface{}{"key": e.Key, "title": e.Title, "description": e.Description, "tags": tags, "retired": e.Retired,
		"image_url": fmt.Sprintf("/backgrounds/%s.webp?v=%d", e.Key, v), "thumb_url": fmt.Sprintf("/backgrounds/%s.thumb.webp?v=%d", e.Key, v)}
}

var bgKeyRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+){0,7}$`)

// bgTagProblem: what is wrong with an entry's words, "" when nothing.
func bgEntryProblem(e bgEntry) string {
	if !bgKeyRe.MatchString(e.Key) || len(e.Key) > 64 {
		return "The key is lowercase words joined by hyphens (up to 8 words, 64 characters), describing the picture, such as gothic-castle-moonlit-valley."
	}
	if t := strings.TrimSpace(e.Title); t == "" || len([]rune(t)) > 100 {
		return "The title is 1 to 100 characters describing the picture."
	}
	if d := strings.TrimSpace(e.Description); d == "" || len([]rune(d)) > 300 {
		return "The description is one sentence (up to 300 characters) that helps a GM decide whether the picture fits a campaign."
	}
	for group, words := range e.Tags {
		allowed := bgGroupWords(group)
		if allowed == nil {
			return fmt.Sprintf("%q is no tag group: the groups are %s.", group, strings.Join(bgGroups(), ", "))
		}
		for _, w := range words {
			if !allowed[w] {
				return fmt.Sprintf("%q is not a %s tag: the %s tags are %s.", w, group, group, strings.Join(bgVocabularyWords(group), ", "))
			}
		}
	}
	if len(e.Tags["place"]) == 0 {
		return "Give the picture at least one place tag: " + strings.Join(bgVocabularyWords("place"), ", ") + "."
	}
	return ""
}

func bgGroups() []string {
	var out []string
	for _, g := range bgVocabulary {
		out = append(out, g.Group)
	}
	return out
}

func bgVocabularyWords(group string) []string {
	for _, g := range bgVocabulary {
		if g.Group == group {
			return g.Words
		}
	}
	return nil
}

func bgGroupWords(group string) map[string]bool {
	ws := bgVocabularyWords(group)
	if ws == nil {
		return nil
	}
	m := map[string]bool{}
	for _, w := range ws {
		m[w] = true
	}
	return m
}

// bgVocabularyView: the vocabulary as a reply gives it.
func bgVocabularyView() map[string][]string {
	out := map[string][]string{}
	for _, g := range bgVocabulary {
		out[g.Group] = g.Words
	}
	return out
}

// seedBackgroundLibrary puts the pictures the site ships in the library, once each (an entry already there keeps its edits and its picture).
func seedBackgroundLibrary() {
	raw, err := backgroundFiles.ReadFile("data/backgrounds/library.json")
	if err != nil {
		log.Printf("background library: %v", err)
		return
	}
	var list []struct {
		Key, Title, Description string
		Tags                    map[string][]string
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		log.Printf("background library: %v", err)
		return
	}
	added := 0
	for _, x := range list {
		var n int
		if db.QueryRow(`SELECT COUNT(*) FROM background_library WHERE key = $1`, x.Key).Scan(&n); n > 0 {
			continue
		}
		img, err1 := backgroundFiles.ReadFile("data/backgrounds/" + x.Key + ".webp")
		thumb, err2 := backgroundFiles.ReadFile("data/backgrounds/" + x.Key + ".thumb.webp")
		if err1 != nil || err2 != nil {
			log.Printf("background library: %s has no picture", x.Key)
			continue
		}
		tags, _ := json.Marshal(x.Tags)
		if _, err := db.Exec(`INSERT INTO background_library (key, title, description, tags, image, thumb, builtin) VALUES ($1, $2, $3, $4, $5, $6, TRUE) ON CONFLICT (key) DO NOTHING`,
			x.Key, x.Title, x.Description, string(tags), img, thumb); err != nil {
			log.Printf("background library: %s: %v", x.Key, err)
			continue
		}
		added++
	}
	if added > 0 {
		log.Printf("background library: %d pictures added", added)
	}
}

func scanBgEntry(sc interface{ Scan(...interface{}) error }) (bgEntry, error) {
	var e bgEntry
	var tags string
	err := sc.Scan(&e.Key, &e.Title, &e.Description, &tags, &e.Retired, &e.Updated)
	if err == nil {
		json.Unmarshal([]byte(tags), &e.Tags)
	}
	return e, err
}

const bgColumns = `key, title, description, tags::text, retired, updated_at`

// backgroundLibrary: the entries (the retired ones too when asked), by title.
func backgroundLibrary(withRetired bool) ([]bgEntry, error) {
	q := `SELECT ` + bgColumns + ` FROM background_library`
	if !withRetired {
		q += ` WHERE NOT retired`
	}
	rows, err := db.Query(q + ` ORDER BY title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []bgEntry
	for rows.Next() {
		e, err := scanBgEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// backgroundEntry: one entry by its key (sql.ErrNoRows when none).
func backgroundEntry(key string) (bgEntry, error) {
	return scanBgEntry(db.QueryRow(`SELECT `+bgColumns+` FROM background_library WHERE key = $1`, key))
}

var bgWordRe = regexp.MustCompile(`[a-z]+`)

// bgMatch is how well an entry fits a campaign: each tag the text names (or hints at, bgWordHints) or the request asks for, and the words the
// text shares with the entry's own title and description.
func bgScore(e bgEntry, text string, asked []string) int {
	words := map[string]bool{}
	for _, w := range bgWordRe.FindAllString(strings.ToLower(text), -1) {
		words[w] = true
	}
	score := 0
	for _, tag := range e.words() {
		hit := words[tag]
		for _, h := range bgWordHints[tag] {
			hit = hit || words[h]
		}
		if hit {
			score += 3
		}
		for _, a := range asked {
			if a == tag {
				score += 5
			}
		}
	}
	for _, w := range bgWordRe.FindAllString(strings.ToLower(e.Title+" "+e.Description), -1) {
		if len(w) > 4 && words[w] {
			score++
		}
	}
	return score
}

// matchBackgrounds: the offered entries, best match first (equal ones by title).
func matchBackgrounds(text string, asked []string) ([]bgEntry, []int, error) {
	list, err := backgroundLibrary(false)
	if err != nil {
		return nil, nil, err
	}
	scores := make([]int, len(list))
	for i, e := range list {
		scores[i] = bgScore(e, text, asked)
	}
	idx := make([]int, len(list))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	out, sc := make([]bgEntry, len(list)), make([]int, len(list))
	for i, j := range idx {
		out[i], sc[i] = list[j], scores[j]
	}
	return out, sc, nil
}

// pickBackground: the entry that best fits a campaign's own words, "" when none fits at all (the page then shows the default).
func pickBackground(text string) string {
	list, scores, err := matchBackgrounds(text, nil)
	if err != nil || len(list) == 0 || scores[0] == 0 {
		return ""
	}
	return list[0].Key
}

// bgUnknown: a key that is not offered in the library (a refusal, never a database failure).
type bgUnknown string

func (k bgUnknown) Error() string {
	return fmt.Sprintf("%q is not a background in the library: GET /api/backgrounds lists them", string(k))
}
func (bgUnknown) isRefusal() {}

// setLibraryBackground is the one writer of a campaign's pick ("" for none). A retired or unknown key is refused.
func setLibraryBackground(campaignID int, key string) (string, error) {
	if key != "" {
		e, err := backgroundEntry(key)
		if err == sql.ErrNoRows || (err == nil && e.Retired) {
			return "", bgUnknown(key)
		}
		if err != nil {
			return "", dbFailureError(err, "the background library")
		}
	}
	_, err := db.Exec(`UPDATE lobbies SET background_key = NULLIF($1, ''), background_key_at = NOW() WHERE id = $2`, key, campaignID)
	if err != nil {
		return "", dbFailureError(err, "the campaign")
	}
	return key, nil
}

// libraryBackgroundOf: a campaign's pick and its entry's change time ("" when none).
func libraryBackgroundOf(campaignID int) (string, time.Time) {
	var key sql.NullString
	var updated sql.NullTime
	db.QueryRow(`SELECT l.background_key, b.updated_at FROM lobbies l LEFT JOIN background_library b ON b.key = l.background_key WHERE l.id = $1`, campaignID).Scan(&key, &updated)
	if !key.Valid || !updated.Valid {
		return "", time.Time{}
	}
	return key.String, updated.Time
}

// backgroundChoiceView: what a campaign shows and how to change it, for the creation and change replies.
func backgroundChoiceView(campaignID int, key, how, text string) map[string]interface{} {
	v := map[string]interface{}{"how": how, "change": fmt.Sprintf("POST /api/campaigns/%d/background {\"background\": \"<key>\"} (keys: GET /api/backgrounds)", campaignID)}
	if key != "" {
		if e, err := backgroundEntry(key); err == nil {
			v["chosen"] = e.view()
		}
	} else {
		v["chosen"] = nil
	}
	list, _, err := matchBackgrounds(text, nil)
	if err == nil {
		var others []map[string]interface{}
		for _, e := range list {
			if e.Key != key && len(others) < 3 {
				others = append(others, map[string]interface{}{"key": e.Key, "title": e.Title, "description": e.Description})
			}
		}
		v["other_matches"] = others
	}
	return v
}

// handleBackgrounds godoc
// @Summary The background library
// @Description The scenes a campaign's background is chosen from, best match first: each with its key, title, a sentence of description, its tags (place, mood, light, features) and its picture and thumbnail. Name a key in campaign creation's `background`, or change a campaign's with POST /api/campaigns/{id}/background. With no `background` at creation the server picks the best match for the campaign's own name and setting.
// @Tags Campaigns
// @Param tags query string false "Tag words to match, comma separated (the words are in vocabulary)"
// @Param q query string false "Words describing the campaign to match against the tags and descriptions"
// @Success 200 {object} map[string]interface{} "backgrounds, vocabulary"
// @Router /backgrounds [get]
func handleBackgrounds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var asked []string
	for _, t := range strings.Split(strings.ToLower(r.URL.Query().Get("tags")), ",") {
		if t = strings.TrimSpace(t); t != "" {
			asked = append(asked, t)
		}
	}
	list, scores, err := matchBackgrounds(r.URL.Query().Get("q"), asked)
	if err != nil {
		writeDBError(w, err, "the background library")
		return
	}
	out := []map[string]interface{}{}
	for i, e := range list {
		v := e.view()
		v["match"] = scores[i]
		out = append(out, v)
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"backgrounds": out, "vocabulary": bgVocabularyView(),
		"how": "Name a key in POST /api/campaigns {\"background\": \"<key>\"}, or change a campaign's with POST /api/campaigns/{id}/background {\"background\": \"<key>\"}."})
}

// handleBackgroundImage serves /backgrounds/{key}.webp and /backgrounds/{key}.thumb.webp (public: the pictures are the site's).
func handleBackgroundImage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/backgrounds/")
	col, key := "image", strings.TrimSuffix(name, ".webp")
	if strings.HasSuffix(key, ".thumb") {
		col, key = "thumb", strings.TrimSuffix(key, ".thumb")
	}
	if !strings.HasSuffix(name, ".webp") || !bgKeyRe.MatchString(key) {
		http.NotFound(w, r)
		return
	}
	var data []byte
	if err := db.QueryRow(`SELECT `+col+` FROM background_library WHERE key = $1`, key).Scan(&data); err != nil || len(data) == 0 {
		if err != nil && err != sql.ErrNoRows {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}

// backgroundThumb: the 480 by 270 thumbnail of a stored background.
func backgroundThumb(stored []byte) ([]byte, error) {
	src, err := decodePicture(stored)
	if err != nil {
		return nil, err
	}
	out := image.NewRGBA(image.Rect(0, 0, bgThumbW, bgThumbH))
	xdraw.CatmullRom.Scale(out, out.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	var buf bytes.Buffer
	if err := webp.Encode(&buf, out, webp.Options{Quality: 80, Method: 4}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// adminBackgroundRequest: an entry the administrator adds or edits.
type adminBackgroundRequest struct {
	Key         string              `json:"key"`          // the new entry's key (adding only): lowercase words joined by hyphens, describing the picture
	Title       *string             `json:"title"`        // a short descriptive name (never a campaign's or a book's)
	Description *string             `json:"description"`  // one sentence that helps a GM decide whether it fits
	Tags        map[string][]string `json:"tags"`         // tag words by group (place, mood, light, features), from the vocabulary
	Retired     *bool               `json:"retired"`      // true: no longer offered (the campaigns that use it keep it); false: offered again
	ImageBase64 string              `json:"image_base64"` // the picture (adding, or replacing it): PNG, JPEG or WebP, stored as a 1920 by 1080 WebP
	Note        string              `json:"note"`         // why: written to the admin change log
}

// handleAdminBackgrounds serves the library for the administrator: GET /api/admin/backgrounds (every entry, the retired ones too, with the
// vocabulary), POST /api/admin/backgrounds (add one) and POST /api/admin/backgrounds/{key} (edit one: its words, tags, picture or retired). The
// words of these routes are written in tools/build_swagger.py; the request and the reply come from the code.
// @Router /admin/backgrounds [get]
// @Router /admin/backgrounds [post]
// @Router /admin/backgrounds/{key} [post]
func handleAdminBackgrounds(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	key := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/admin/backgrounds"), "/"), "/")
	switch {
	case r.Method == http.MethodGet && key == "":
		list, err := backgroundLibrary(true)
		if err != nil {
			writeDBError(w, err, "the background library")
			return
		}
		out := []map[string]interface{}{}
		for _, e := range list {
			v := e.view()
			var n int
			db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE background_key = $1`, e.Key).Scan(&n)
			v["campaigns"] = n
			out = append(out, v)
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"backgrounds": out, "vocabulary": bgVocabularyView()})
	case r.Method == http.MethodPost:
		var req adminBackgroundRequest
		if !readRequest(w, r, &req) {
			return
		}
		adding := key == ""
		var e bgEntry
		if adding {
			e = bgEntry{Key: strings.TrimSpace(req.Key), Tags: req.Tags}
			if _, err := backgroundEntry(e.Key); err == nil {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "key_taken", "message": "A background with that key is in the library already. Nothing was changed."})
				return
			}
		} else {
			var err error
			if e, err = backgroundEntry(key); err != nil {
				if answerDBFailure(w, err, "the background library") {
					return
				}
				writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "background_not_found"})
				return
			}
			if req.Tags != nil {
				e.Tags = req.Tags
			}
		}
		before := e
		if req.Title != nil {
			e.Title = strings.TrimSpace(*req.Title)
		}
		if req.Description != nil {
			e.Description = strings.TrimSpace(*req.Description)
		}
		if req.Retired != nil {
			e.Retired = *req.Retired
		}
		if p := bgEntryProblem(e); p != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_background", "message": p + " Nothing was changed.", "vocabulary": bgVocabularyView()})
			return
		}
		var img, thumb []byte
		note := ""
		if req.ImageBase64 != "" || adding {
			raw, ref := decodePortraitBase64(req.ImageBase64)
			if ref != nil {
				writeJSONStatus(w, ref.Status, ref.Body)
				return
			}
			mime, _, ref := checkImage(raw, backgroundRule)
			if ref != nil {
				writeJSONStatus(w, ref.Status, ref.Body)
				return
			}
			var err error
			if img, _, note, err = normalizeBackground(raw, mime); err == nil {
				thumb, err = backgroundThumb(img)
			}
			if err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "corrupt_image", "message": "The image could not be read: the file looks damaged. Nothing was changed."})
				return
			}
		}
		tags, _ := json.Marshal(e.Tags)
		var err error
		if adding {
			_, err = db.Exec(`INSERT INTO background_library (key, title, description, tags, image, thumb) VALUES ($1, $2, $3, $4, $5, $6)`, e.Key, e.Title, e.Description, string(tags), img, thumb)
		} else if img != nil {
			_, err = db.Exec(`UPDATE background_library SET title = $2, description = $3, tags = $4, retired = $5, image = $6, thumb = $7, updated_at = NOW() WHERE key = $1`, e.Key, e.Title, e.Description, string(tags), e.Retired, img, thumb)
		} else {
			_, err = db.Exec(`UPDATE background_library SET title = $2, description = $3, tags = $4, retired = $5, updated_at = NOW() WHERE key = $1`, e.Key, e.Title, e.Description, string(tags), e.Retired)
		}
		if err != nil {
			writeDBWriteError(w, err, "the background library")
			return
		}
		was := "none"
		if !adding {
			was = bgEntrySummary(before)
		}
		now := bgEntrySummary(e)
		if img != nil {
			now += " (new picture)"
		}
		logAdminChangeIn(db, "background", 0, e.Key, was, now, "admin", req.Note)
		saved, _ := backgroundEntry(e.Key)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "background": saved.view(), "note": nilIfEmpty(note)})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}

// bgEntrySummary: an entry in the change log's words.
func bgEntrySummary(e bgEntry) string {
	s := e.Title + " [" + strings.Join(e.words(), ", ") + "]"
	if e.Retired {
		s += " (retired)"
	}
	return s
}
