package main

// A human's pins (owner, Nov 24): the Live and Character tabs open the campaign and the character the reader pinned, not whichever table
// last moved. A pin is a cookie of this browser (no sign-in: the readers are humans watching), set and removed by the pin button a campaign's
// page and a character sheet carry (pinButton; the site script toggles it). pinnedCampaign and pinnedCharacter are the one reading of a pin:
// a pin whose campaign or character is gone or can no longer be seen is cleared (the reply removes the cookie and the page says so,
// pinClearedCookie). featuredCampaign (the Live tab) and handleCharacterView (the Character tab) ask them first.

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
)

const (
	pinCampaignCookie  = "arpg_pin_campaign"
	pinCharacterCookie = "arpg_pin_character"
	pinClearedCookie   = "arpg_pin_cleared" // what pin was cleared, read once by the site script ("campaign", "character")
)

// pinButton: the pin button of a campaign's page or a character sheet; the site script shows whether it is pinned and toggles it.
func pinButton(kind string, id int, name string) string {
	tab := "Live"
	if kind == "character" {
		tab = "Character"
	}
	return fmt.Sprintf(`<button type="button" class="btn btn-ghost btn-sm pin-btn" data-pin="%s" data-pin-id="%d" data-pin-tab="%s" aria-pressed="false" title="Pin %s to the %s tab">`+
		`<span class="pin-icon" aria-hidden="true">📌</span><span class="pin-label">Pin</span></button>`, kind, id, tab, htmlEscape(name), tab)
}

// pinnedID: a pin cookie's id, 0 when there is none.
func pinnedID(r *http.Request, cookie string) int {
	c, err := r.Cookie(cookie)
	if err != nil {
		return 0
	}
	id, _ := strconv.Atoi(c.Value)
	return id
}

// clearPin removes a pin that no longer works and tells the page (the site script shows it once).
func clearPin(w http.ResponseWriter, cookie, kind string) {
	http.SetCookie(w, &http.Cookie{Name: cookie, Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: pinClearedCookie, Value: kind, Path: "/", MaxAge: 60, SameSite: http.SameSiteLaxMode})
}

// pinnedCampaign: the campaign this browser pinned, when it still exists and the reader may see it; a pin that fails is cleared (w may be nil
// to only read).
func pinnedCampaign(w http.ResponseWriter, r *http.Request) int {
	id := pinnedID(r, pinCampaignCookie)
	if id <= 0 {
		return 0
	}
	var one int
	if err := db.QueryRow(`SELECT 1 FROM lobbies WHERE id = $1`, id).Scan(&one); err != nil || campaignIsDeleted(id) || !canWatch(r, id) {
		if w != nil && (err == nil || err == sql.ErrNoRows) {
			clearPin(w, pinCampaignCookie, "campaign")
		}
		return 0
	}
	return id
}

// pinnedCharacter: the character this browser pinned, when it still exists and the reader may see it (a private campaign's character
// needs that campaign's watch password, canSeeCharacter); a pin that fails is cleared.
func pinnedCharacter(w http.ResponseWriter, r *http.Request) int {
	id := pinnedID(r, pinCharacterCookie)
	if id <= 0 {
		return 0
	}
	var one int
	err := db.QueryRow(`SELECT 1 FROM characters WHERE id = $1`, id).Scan(&one)
	if err == nil && !characterIsDeleted(id) {
		if _, ok := canSeeCharacter(r, id); ok {
			return id
		}
	}
	if w != nil && (err == nil || err == sql.ErrNoRows) {
		clearPin(w, pinCharacterCookie, "character")
	}
	return 0
}

// pinScript: the pin button's toggle, the tabs' pinned mark and the note of a cleared pin (part of the site script).
const pinScript = `
(function () {
  function get(n) { var m = document.cookie.match('(?:^|; )' + n + '=([^;]*)'); return m ? decodeURIComponent(m[1]) : ''; }
  function set(n, v) { document.cookie = n + '=' + v + '; Path=/; Max-Age=' + (v ? 31536000 : 0) + '; SameSite=Lax'; }
  var names = { campaign: '` + pinCampaignCookie + `', character: '` + pinCharacterCookie + `' };
  var tabs = { campaign: '/live', character: '/character' };
  function mark() {
    Object.keys(tabs).forEach(function (k) {
      document.querySelectorAll('a[href="' + tabs[k] + '"]').forEach(function (a) {
        var on = !!get(names[k]); a.classList.toggle('is-pinned', on);
        if (on) a.setAttribute('title', 'Pinned: opens the ' + k + ' you pinned'); else a.removeAttribute('title');
      });
    });
    document.querySelectorAll('[data-pin]').forEach(function (b) {
      var on = get(names[b.getAttribute('data-pin')]) === b.getAttribute('data-pin-id');
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      b.querySelector('.pin-label').textContent = on ? 'Pinned' : 'Pin';
      b.title = on ? 'Pinned to the ' + b.getAttribute('data-pin-tab') + ' tab: tap to unpin' : 'Pin to the ' + b.getAttribute('data-pin-tab') + ' tab';
    });
  }
  document.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-pin]'); if (!b) return;
    var kind = b.getAttribute('data-pin'), id = b.getAttribute('data-pin-id'), tab = b.getAttribute('data-pin-tab');
    if (get(names[kind]) === id) { set(names[kind], ''); showToast('Unpinned: the ' + tab + ' tab opens the newest table again'); }
    else { set(names[kind], id); showToast('Pinned: the ' + tab + ' tab opens this ' + kind); }
    mark();
  });
  var cleared = get('` + pinClearedCookie + `');
  if (cleared) { document.cookie = '` + pinClearedCookie + `=; Path=/; Max-Age=0; SameSite=Lax'; setTimeout(function () { showToast('The ' + cleared + ' you pinned is no longer available, so its pin was cleared'); }, 300); }
  mark();
})();
`
