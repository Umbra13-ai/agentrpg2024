package main

// The admin page's Monsters tab (owner, Nov 24): every monster GMs built with the monster builder (the compendium, custom_monsters), from
// every GM, shared or private, hidden ones and old versions included: a list to search, each monster's stat block (statBlockHTML, the same
// renderer the GM's page uses, in a bare page the tab shows in a frame) and its token, which the administrator can add, replace or remove
// (putMonsterToken, the GM's own route's rule and writes; written to the admin change log).
//   GET  /api/admin/monsters?q=&deleted=          the list ('' visible, only = hidden, include = all)
//   GET  /api/admin/monsters/{key}                one monster: its facts, its versions, its stat block page, the token rule
//   GET  /api/admin/monsters/{key}/page           the stat block as a bare page
//   POST|PUT|DELETE /api/admin/monsters/{key}/token

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// handleAdminMonsters godoc
// @Summary The GM-built monsters (admin)
// @Description GET /api/admin/monsters lists every monster GMs built (q searches the name, the key and the GM's name; deleted=” visible, only hidden ones and replaced versions, include all), newest first, up to 200. GET /api/admin/monsters/{key} gives one with its facts, its versions and the address of its stat block page; .../page is the stat block as a page; POST (or PUT) .../token with {"image_base64": "...", "note": "..."} adds or replaces its token by the same rule as the GM's route (a square PNG, JPEG or WebP, 128 to 2048 pixels a side, at most 2 MB), DELETE .../token removes it; each change is written to the admin change log.
// @Tags Admin
// @Param q query string false "GET list: a name, a key or a GM's name"
// @Param deleted query string false "GET list: ” visible, only, include"
// @Router /admin/monsters [get]
// @Router /admin/monsters/{key} [get]
// @Router /admin/monsters/{key}/page [get]
// @Router /admin/monsters/{key}/token [post]
// @Router /admin/monsters/{key}/token [put]
// @Router /admin/monsters/{key}/token [delete]
func handleAdminMonsters(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/monsters"), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "Use GET."})
			return
		}
		adminMonsterList(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	key, _ := url.PathUnescape(parts[0])
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	c, err := readCompendiumEntry(key)
	if err == sql.ErrNoRows {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "monster_not_found", "message": "No GM-built monster has that key."})
		return
	}
	if err != nil {
		writeDBError(w, err, "the compendium")
		return
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		adminMonsterView(w, c)
	case sub == "page" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN") // the admin page shows it in a frame; only this site may
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'; base-uri 'self'; object-src 'none'")
		fmt.Fprint(w, statBlockDocument(c))
	case sub == "token":
		adminMonsterToken(w, r, c)
	default:
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "not_found", "message": "GET /api/admin/monsters/{key}, .../page, or POST|DELETE .../token."})
	}
}

// adminMonsterList: the list, newest first.
func adminMonsterList(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	hidden := "m.hidden_at IS NULL"
	switch r.URL.Query().Get("deleted") {
	case "only":
		hidden = "m.hidden_at IS NOT NULL"
	case "include":
		hidden = "TRUE"
	}
	rows, err := db.Query(`SELECT m.key, m.definition->>'name', COALESCE(m.definition->>'cr', ''), COALESCE(m.definition->>'size', ''), COALESCE(m.definition->>'type', ''),
			COALESCE(a.name, ''), COALESCE(m.owner_id, 0), m.shared, m.hidden_at IS NOT NULL, COALESCE(m.replaces, ''), CASE WHEN m.token IS NULL THEN NULL ELSE m.token_updated_at END, m.created_at
		FROM custom_monsters m LEFT JOIN agents a ON a.id = m.owner_id
		WHERE `+hidden+` AND ($1 = '' OR m.definition->>'name' ILIKE '%' || $1 || '%' OR m.key ILIKE '%' || $1 || '%' OR a.name ILIKE '%' || $1 || '%')
		ORDER BY m.created_at DESC, m.key LIMIT 200`, q)
	if err != nil {
		writeDBError(w, err, "the compendium")
		return
	}
	defer rows.Close()
	out := []map[string]interface{}{}
	for rows.Next() {
		var key, name, cr, size, typ, owner, replaces string
		var ownerID int
		var shared, isHidden bool
		var tokenAt sql.NullTime
		var created sql.NullTime
		if err := rows.Scan(&key, &name, &cr, &size, &typ, &owner, &ownerID, &shared, &isHidden, &replaces, &tokenAt, &created); err != nil {
			writeDBError(w, err, "the compendium")
			return
		}
		out = append(out, map[string]interface{}{"key": key, "name": name, "cr": cr, "size": size, "type": typ, "owner": owner, "owner_id": ownerID,
			"shared": shared, "deleted": isHidden, "replaces": replaces, "token_url": customTokenURLAt(key, tokenAt), "created": created.Time.UTC().Format("2006-01-02")})
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err, "the compendium")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"monsters": out})
}

// adminMonsterView: one monster with its facts, its versions (the ones it replaced, and the one that replaced it) and its stat block page.
func adminMonsterView(w http.ResponseWriter, c compendiumEntry) {
	versions := []map[string]interface{}{}
	rows, err := db.Query(`WITH RECURSIVE older(key, replaces, created_at, depth) AS (
			SELECT key, replaces, created_at, 0 FROM custom_monsters WHERE key = $1
			UNION ALL SELECT m.key, m.replaces, m.created_at, o.depth + 1 FROM custom_monsters m JOIN older o ON m.key = o.replaces WHERE o.depth < 50)
		SELECT key, created_at FROM older WHERE key <> $1
		UNION ALL SELECT key, created_at FROM custom_monsters WHERE replaces = $1
		ORDER BY created_at`, c.Key)
	if err != nil {
		writeDBError(w, err, "the compendium")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var at sql.NullTime
		if err := rows.Scan(&key, &at); err != nil {
			writeDBError(w, err, "the compendium")
			return
		}
		versions = append(versions, map[string]interface{}{"key": key, "saved": at.Time.UTC().Format("2006-01-02 15:04")})
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err, "the compendium")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"monster": map[string]interface{}{
		"key": c.Key, "name": c.Monster.Name, "cr": c.Monster.CR, "owner": c.Owner, "owner_id": c.OwnerID, "shared": c.Shared, "deleted": c.Hidden.Valid,
		"source_note": c.Note, "replaces": c.Replaces, "created": c.Created.UTC().Format("2006-01-02 15:04 UTC"), "token_url": monsterTokenURL(c.Key)},
		"versions": versions, "page": "/api/admin/monsters/" + url.PathEscape(c.Key) + "/page", "token_rule": monsterTokenRuleShort})
}

// adminMonsterToken: the administrator's upload or removal, by the GM route's rule and writes, written to the admin change log.
func adminMonsterToken(w http.ResponseWriter, r *http.Request, c compendiumEntry) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "Use POST or PUT to upload, DELETE to remove."})
		return
	}
	var req struct {
		ImageBase64 string `json:"image_base64"` // POST and PUT: the picture as base64 (a data: URL is accepted)
		Note        string `json:"note"`         // why: written to the admin change log
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	had := monsterTokenURL(c.Key) != ""
	words := func(has bool) string {
		if has {
			return "a token"
		}
		return "no token"
	}
	if changed, ok := putMonsterToken(w, r, c.Key, req.ImageBase64); ok {
		logAdminChangeIn(db, "monster", 0, "token of "+c.Key+" ("+c.Monster.Name+")", words(had), words(changed == "saved"), "admin", req.Note)
	}
}

// statBlockDocument: the stat block alone as a page, with the site's stylesheet and no frame (the admin page shows it in a frame).
func statBlockDocument(c compendiumEntry) string {
	head := baseHTML // the site's head (its stylesheet and theme), without its top bar
	if i := strings.Index(head, "<body>"); i >= 0 {
		head = head[:i]
	}
	head = strings.Replace(head, "<title>Agent RPG 2024</title>", "<title>"+htmlEscape(c.Monster.Name)+"</title>", 1)
	return withBaseURL(head + `<body><main style="padding:4px;">` + statBlockHTML(c) + `</main></body></html>`)
}
