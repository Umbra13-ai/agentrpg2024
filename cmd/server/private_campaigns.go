package main

// Private campaigns (a table rule): a GM can make a campaign private with a watch password. Its players and GM always
// see it; anyone else watching the campaign page, its log, feed or spectator view must enter the password first (the page sets a
// signed cookie). API clients can send the password in the X-Watch-Password header (never in the URL). The characters of a
// private campaign are covered too: their page (/character/{id}) and everything read under /api/characters/{id}.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var watchSecret = func() []byte {
	if k := os.Getenv("ADMIN_KEY"); k != "" {
		return []byte("watch:" + k)
	}
	b := make([]byte, 32)
	rand.Read(b)
	return b
}()

func watchHash(campaignID int) string {
	var h string
	db.QueryRow(`SELECT COALESCE(watch_password_hash,'') FROM lobbies WHERE id = $1`, campaignID).Scan(&h)
	return h
}

func isPrivateCampaign(campaignID int) bool {
	return tableRule(campaignID, "visibility") == "private" && watchHash(campaignID) != ""
}

// watchToken is the cookie value that proves the watcher gave the password (it changes when the password changes).
func watchToken(campaignID int, hash string) string {
	m := hmac.New(sha256.New, watchSecret)
	fmt.Fprintf(m, "%d|%s", campaignID, hash)
	return hex.EncodeToString(m.Sum(nil))
}

func watchCookieName(campaignID int) string { return fmt.Sprintf("arpg_watch_%d", campaignID) }

// canWatch reports whether this request may see a campaign: it is public, or the caller is its GM or one of its players, or the
// caller gave the watch password (cookie or X-Watch-Password header).
func canWatch(r *http.Request, campaignID int) bool {
	if !isPrivateCampaign(campaignID) {
		return true
	}
	if agentID, err := getAgentFromAuth(r); err == nil {
		var n int
		db.QueryRow(`SELECT count(*) FROM lobbies WHERE id = $1 AND dm_id = $2`, campaignID, agentID).Scan(&n)
		if n == 0 {
			db.QueryRow(`SELECT count(*) FROM characters WHERE lobby_id = $1 AND agent_id = $2`, campaignID, agentID).Scan(&n)
		}
		if n > 0 {
			return true
		}
	}
	hash := watchHash(campaignID)
	if c, err := r.Cookie(watchCookieName(campaignID)); err == nil && hmac.Equal([]byte(c.Value), []byte(watchToken(campaignID, hash))) {
		return true
	}
	if pw := r.Header.Get("X-Watch-Password"); pw != "" && checkPassword(pw, hash) {
		return true
	}
	return false
}

// canSeeCharacter: a character outside any campaign, or in one the caller may watch, can be seen. It returns the campaign.
func canSeeCharacter(r *http.Request, charID int) (int, bool) {
	lobby := lobbyOfChar(charID)
	return lobby, lobby == 0 || canWatch(r, lobby)
}

// guardCharacterRead: a GET with ?character_id= naming a character of a private campaign needs the right to watch that campaign
// (the /api/characters/<command> routes; /api/characters/{id} checks the same itself).
func guardCharacterRead(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if id, err := strconv.Atoi(r.URL.Query().Get("character_id")); err == nil && id > 0 {
				if lobby, ok := canSeeCharacter(r, id); !ok {
					writeWatchGate(w, r, lobby, false)
					return
				}
			}
		}
		h(w, r)
	}
}

// unlockNext is where the password form may send the watcher back to: a character page.
var unlockNext = regexp.MustCompile(`^/character/[0-9]+$`)

// setWatchPassword stores (or with "" clears) a campaign's watch password as a bcrypt hash.
func setWatchPassword(campaignID int, password string) error {
	if password == "" {
		_, err := db.Exec(`UPDATE lobbies SET watch_password_hash = '' WHERE id = $1`, campaignID)
		return err
	}
	if len(password) < 4 {
		return fmt.Errorf("the watch password needs at least 4 characters")
	}
	h, _ := newPasswordHash(password)
	if h == "" {
		return fmt.Errorf("the password could not be stored")
	}
	_, err := db.Exec(`UPDATE lobbies SET watch_password_hash = $1 WHERE id = $2`, h, campaignID)
	return err
}

// writeWatchGate answers a request for a private campaign the caller may not see: a password form for pages, a 401 for the API.
func writeWatchGate(w http.ResponseWriter, r *http.Request, campaignID int, page bool) {
	writeWatchGateNext(w, r, campaignID, page, "")
}

// writeWatchGateNext is writeWatchGate for a page other than the campaign's (a character page): after the password the
// watcher comes back to next.
func writeWatchGateNext(w http.ResponseWriter, r *http.Request, campaignID int, page bool, next string) {
	if !page {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]interface{}{"error": "private_campaign",
			"message": "This campaign is private. Its players and GM can see it; others send the watch password in the X-Watch-Password header."})
		return
	}
	var name string
	db.QueryRow(`SELECT name FROM lobbies WHERE id = $1`, campaignID).Scan(&name)
	msg := ""
	if r.URL.Query().Get("wrong") == "1" {
		msg = `<p class="muted" style="color:var(--bad)">That password isn't right.</p>`
	}
	hidden := ""
	if unlockNext.MatchString(next) {
		hidden = fmt.Sprintf(`<input type="hidden" name="next" value="%s">`, html.EscapeString(next))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	content := fmt.Sprintf(`<h1>🔒 %s</h1>
<p>This campaign is private. Enter the watch password the GM gave you.</p>%s
<form method="post" action="/campaign/%d/unlock">%s
  <input type="password" name="password" autocomplete="current-password" required style="padding:0.4em;font-size:1em">
  <button type="submit" style="padding:0.4em 1em;font-size:1em">Watch</button>
</form>`, html.EscapeString(name), msg, campaignID, hidden)
	fmt.Fprint(w, wrapHTML(name+" - private campaign", content))
}

// handleCampaignUnlock: POST /campaign/{id}/unlock with the watch password sets the watch cookie and goes back to the campaign.
func handleCampaignUnlock(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, fmt.Sprintf("/campaign/%d", campaignID), http.StatusSeeOther)
		return
	}
	r.ParseForm()
	back := fmt.Sprintf("/campaign/%d", campaignID)
	if next := r.FormValue("next"); unlockNext.MatchString(next) {
		back = next // a character page of the campaign
	}
	hash := watchHash(campaignID)
	if hash == "" || !checkPassword(strings.TrimSpace(r.FormValue("password")), hash) {
		time.Sleep(500 * time.Millisecond) // slow down guessing
		http.Redirect(w, r, back+"?wrong=1", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: watchCookieName(campaignID), Value: watchToken(campaignID, hash), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(30 * 24 * time.Hour)})
	http.Redirect(w, r, back, http.StatusSeeOther)
}
