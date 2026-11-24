package main

// A GM-built monster's token (owner, Nov 24): the picture the encounter stage, the stat block view and the admin's Monsters tab show for a
// compendium monster (custom_monsters), as the SRD stat blocks show their painted tokens (token_assets_2024.go). monsterTokenRule is what a
// token may be and monsterTokenRuleText says it, word for word, in the builder's replies, the token route's refusals and the guide; the
// writes are saveMonsterToken / clearMonsterToken (TestNoOtherMonsterTokenWriter), from the GM who owns the monster (POST|DELETE
// /api/gm/compendium/monsters/token) or the administrator (POST|DELETE /api/admin/monsters/{key}/token). An edit's new version keeps the
// token (insertCompendiumMonster copies it from the version it replaces). GET /monster-tokens/{key} serves it: the key is a random
// custom: id, and the encounter stage of a public campaign shows it.

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// monsterTokenRule: a square picture, shown in a circle (192 pixels across on a page, 54 on the encounter stage).
var monsterTokenRule = imageRule{MaxBytes: 2 * 1024 * 1024, MinPx: 128, MaxPx: 2048, Square: true,
	Best: "512 by 512 pixels is the best size"}

// monsterTokenRuleText: the rule in words.
var monsterTokenRuleText = fmt.Sprintf("A token is a square picture (the same width and height) in PNG, JPEG or WebP, %d to %d pixels a side and at most %d MB; "+
	"%s. It is shown in a circle (192 pixels across on a stat block, 54 on the encounter stage), so keep the creature in the middle and fill the "+
	"square; a plain or transparent background works best. Send it as {\"monster_key\": \"custom:...\", \"image_base64\": \"...\"} (a data: URL "+
	"is accepted) to POST /api/gm/compendium/monsters/token.",
	monsterTokenRule.MinPx, monsterTokenRule.MaxPx, monsterTokenRule.MaxBytes/(1024*1024), monsterTokenRule.Best)

// monsterTokenView: what the builder's replies say about a monster's token.
func monsterTokenView(key string) map[string]interface{} {
	v := map[string]interface{}{"rule": monsterTokenRuleText}
	if u := monsterTokenURL(key); u != "" {
		v["url"] = u
	}
	return v
}

// saveMonsterToken writes a checked picture as the compendium monster's token.
func saveMonsterToken(key string, imageBytes []byte, mime string) error {
	_, err := db.Exec(`UPDATE custom_monsters SET token = $1, token_type = $2, token_updated_at = NOW() WHERE key = $3`, imageBytes, mime, key)
	return err
}

// clearMonsterToken removes it.
func clearMonsterToken(key string) error {
	_, err := db.Exec(`UPDATE custom_monsters SET token = NULL, token_type = NULL, token_updated_at = NULL WHERE key = $1`, key)
	return err
}

// copyMonsterTokenIn gives an edit's new version the token of the version it replaces, in the edit's transaction.
func copyMonsterTokenIn(tx *sql.Tx, from, to string) error {
	_, err := tx.Exec(`UPDATE custom_monsters n SET token = o.token, token_type = o.token_type, token_updated_at = o.token_updated_at
		FROM custom_monsters o WHERE o.key = $1 AND n.key = $2`, from, to)
	return err
}

// customTokenURL: the URL of a compendium monster's token, with its change time so a new one is fetched; "" when it has none.
func customTokenURL(key string) string {
	var at sql.NullTime
	if db == nil || db.QueryRow(`SELECT token_updated_at FROM custom_monsters WHERE key = $1 AND token IS NOT NULL`, key).Scan(&at) != nil {
		return ""
	}
	return customTokenURLAt(key, at)
}

// customTokenURLAt: the URL from a token's change time already read (a list); "" when it has none.
func customTokenURLAt(key string, at sql.NullTime) string {
	if !at.Valid {
		return ""
	}
	return "/monster-tokens/" + url.PathEscape(key) + "?v=" + fmt.Sprint(at.Time.Unix())
}

// handleMonsterTokenImage serves a compendium monster's token.
// handleMonsterTokenImage godoc
// @Summary A GM-built monster's token
// @Description The token picture of a compendium monster (its custom: key), as the encounter stage and the stat block show it. 404 when it has none.
// @Tags Universe
// @Produce png
// @Param key path string true "the monster's custom: key"
// @Success 200 {file} binary "the picture"
// @Router /monster-tokens/{key} [get]
func handleMonsterTokenImage(w http.ResponseWriter, r *http.Request) {
	key, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/monster-tokens/"))
	var img []byte
	var mime string
	var at time.Time
	err := db.QueryRow(`SELECT token, token_type, token_updated_at FROM custom_monsters WHERE key = $1 AND token IS NOT NULL`, key).Scan(&img, &mime, &at)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeDBError(w, err, "the token")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Last-Modified", at.UTC().Format(http.TimeFormat))
	w.Write(img)
}

// putMonsterToken is the upload and removal both routes share: the request's image_base64 checked by the rule and saved, or removed (a
// DELETE); it answers the request and says what changed ("saved", "removed"; "" when refused).
func putMonsterToken(w http.ResponseWriter, r *http.Request, key, b64 string) (changed string, ok bool) {
	if r.Method == http.MethodDelete {
		if err := clearMonsterToken(key); err != nil {
			writeDBWriteError(w, err, "the token")
			return "", false
		}
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "monster_key": key, "token": monsterTokenView(key), "message": "The token was removed."})
		return "removed", true
	}
	if strings.TrimSpace(b64) == "" {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "image_required", "rule": monsterTokenRuleText, "message": "Send the picture as image_base64. " + monsterTokenRuleText})
		return "", false
	}
	imageBytes, bad := decodePortraitBase64(b64)
	if bad != nil {
		writeJSONStatus(w, bad.Status, bad.Body)
		return "", false
	}
	mime, _, refused := checkImage(imageBytes, monsterTokenRule)
	if refused != nil {
		refused.Body["rule"] = monsterTokenRuleText
		writeJSONStatus(w, refused.Status, refused.Body)
		return "", false
	}
	if err := saveMonsterToken(key, imageBytes, mime); err != nil {
		writeDBWriteError(w, err, "the token")
		return "", false
	}
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "monster_key": key, "token": monsterTokenView(key), "stat_block_url": statBlockLink(key),
		"message": "Saved. The encounter stage and the stat block show it now."})
	return "saved", true
}

// handleGMCompendiumToken godoc
// @Summary Upload or remove a compendium monster's token
// @Description POST (or PUT) saves the token of a monster you imported: {"monster_key": "custom:...", "image_base64": "..."} (a data: URL is accepted). The rule: a square PNG, JPEG or WebP, 128 to 2048 pixels a side, at most 2 MB, 512 by 512 best; it is shown in a circle, so keep the creature in the middle. Another shape is refused (400 not_square) with the size it had. DELETE (or POST ?_method=DELETE) with {"monster_key": ...} removes it. A new version made by an edit keeps the token. Only the GM who imported the monster may change its token. campaign_id names a campaign you run (needed when you run more than one).
// @Tags GM
// @Router /gm/compendium/monsters/token [post]
// @Router /gm/compendium/monsters/token [put]
// @Router /gm/compendium/monsters/token [delete]
func handleGMCompendiumToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "POST, PUT, DELETE")
		writeJSONStatus(w, 405, map[string]interface{}{"error": "method_not_allowed", "message": "Use POST or PUT to upload, DELETE to remove."})
		return
	}
	var req struct {
		MonsterKey  string `json:"monster_key"`  // the custom: key of a monster you imported
		ImageBase64 string `json:"image_base64"` // the picture (POST and PUT): base64, or a data: URL
	}
	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if _, ok := gmReadAs(w, r, agent, &req, campaignBeforeStart()); !ok {
		return
	}
	if strings.TrimSpace(req.MonsterKey) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "monster_key_required", "message": "Name the monster with monster_key (a custom: key you imported). Nothing was changed."})
		return
	}
	if !compendiumKeyOwned(w, agent, req.MonsterKey) {
		return
	}
	putMonsterToken(w, r, strings.TrimSpace(req.MonsterKey), req.ImageBase64)
}
