package main

// A GM-built monster's stat block as a page (owner, Nov 24): statBlockHTML is the one renderer, in the character sheet's components (the
// hero with the token, the vitals strip, the six ability shields with their saves, the senses), then the traits, actions, Bonus Actions,
// Reactions and Legendary Actions with each sentence marked as the server applies it or as the GM resolves it (entryVerdicts) and the
// mechanics the GM attached (mechanicsView). Two places show it: the GM's own page, opened by an expiring link (statBlockLink: signed for
// 24 hours, given in the builder's replies as stat_block_url, since a browser can't carry a GM's token) at GET /monsters/custom/{key}, and the
// admin page's Monsters tab (GET /api/admin/monsters/{key}'s stat_block_html).

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// statBlockLinkLife: how long a stat block link opens the page.
const statBlockLinkLife = 24 * time.Hour

// statBlockSecret signs the links (the server's admin key, as the watch cookies; a random one when there is none, so links end with a restart).
var statBlockSecret = func() []byte {
	if k := os.Getenv("ADMIN_KEY"); k != "" {
		return []byte("statblock:" + k)
	}
	return append([]byte("statblock:"), watchSecret...)
}()

func statBlockSig(key string, exp int64) string {
	m := hmac.New(sha256.New, statBlockSecret)
	fmt.Fprintf(m, "%s|%d", key, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// statBlockLink: the page of a compendium monster's stat block, open for statBlockLinkLife to whoever has the link.
func statBlockLink(key string) string {
	exp := time.Now().Add(statBlockLinkLife).Unix()
	return fmt.Sprintf("%s/monsters/custom/%s?exp=%d&sig=%s", publicBaseURL(), url.PathEscape(key), exp, statBlockSig(key, exp))
}

// statBlockLinkValid: the link's signature is the server's and it hasn't run out.
func statBlockLinkValid(key, exp, sig string) bool {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > e {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(statBlockSig(key, e)))
}

// compendiumEntry: a compendium row as the views read it.
type compendiumEntry struct {
	Key, Note, Owner, Replaces string
	OwnerID                    int
	Shared                     bool
	Hidden                     sql.NullTime
	Created                    time.Time
	Monster                    srdMonster2024
}

// readCompendiumEntry reads one compendium row with its owner's name.
func readCompendiumEntry(key string) (compendiumEntry, error) {
	var c compendiumEntry
	var raw []byte
	err := db.QueryRow(`SELECT m.key, m.definition, m.source_note, COALESCE(m.owner_id, 0), COALESCE(a.name, ''), m.shared, COALESCE(m.replaces, ''), m.hidden_at, m.created_at
		FROM custom_monsters m LEFT JOIN agents a ON a.id = m.owner_id WHERE m.key = $1`, key).
		Scan(&c.Key, &raw, &c.Note, &c.OwnerID, &c.Owner, &c.Shared, &c.Replaces, &c.Hidden, &c.Created)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c.Monster)
	return c, err
}

// statBlockHTML: the stat block of a compendium monster in the character sheet's components; every text is escaped.
func statBlockHTML(c compendiumEntry) string {
	m := c.Monster
	e := htmlEscape
	var b strings.Builder
	face := `<span style="font-size:3rem;">👹</span>`
	if u := monsterTokenURL(c.Key); u != "" {
		face = `<img src="` + e(u) + `" alt="` + e(m.Name) + `" style="width:100%;height:100%;object-fit:cover;" />`
	}
	line := strings.TrimSpace(strings.Join(nonEmpty(m.Size, m.Type), " "))
	if m.Alignment != "" {
		line += ", " + m.Alignment
	}
	// the stat block's own rule: a long value (a key, a list of senses) wraps in its pill instead of running out of it at a phone's width
	b.WriteString(`<style>.statblock .char-hero{flex-wrap:wrap;}.statblock .badge{white-space:normal;max-width:100%;}.statblock .sense-pill{min-width:0;max-width:100%;}.statblock .sense-pill span:last-child{overflow-wrap:anywhere;min-width:0;}</style>
<div class="rpg-panel panel-pad statblock" data-monster-key="` + e(c.Key) + `">
  <div class="char-hero">
    <div class="char-portrait-col"><div class="char-portrait-frame" style="border-radius:50%;overflow:hidden;display:flex;align-items:center;justify-content:center;">` + face + `</div></div>
    <div style="flex:1;min-width:0;">
      <h2 class="font-rpg" style="font-size:1.4rem;">` + e(m.Name) + `</h2>
      <p style="color:var(--text-secondary);font-style:italic;">` + e(line) + `</p>
      <div style="display:flex;flex-wrap:wrap;gap:0.4rem;margin-top:0.4rem;">`)
	b.WriteString(`<span class="badge badge-arcane">CR ` + e(m.CR) + ` · ` + strconv.Itoa(m.XP) + ` XP</span>`)
	if c.Shared {
		b.WriteString(`<span class="badge badge-recruiting">Shared with every GM</span>`)
	} else {
		b.WriteString(`<span class="badge badge-combat">Private to its GM</span>`)
	}
	if c.Hidden.Valid {
		b.WriteString(`<span class="badge badge-combat">Hidden ` + c.Hidden.Time.UTC().Format("2006-01-02") + `</span>`)
	}
	b.WriteString(`</div>
    </div>
  </div>
  <div class="vitals-strip">`)
	vital := func(label, value string) {
		b.WriteString(`<div class="mini-vital"><span class="mini-vital-lbl">` + e(label) + `</span><span class="mini-vital-val font-mono">` + e(value) + `</span></div>`)
	}
	vital("AC", strconv.Itoa(m.AC))
	vital("HP", strconv.Itoa(m.HP))
	vital("Init", signed(m.Initiative.Mod))
	vital("PB", signed(m.PB))
	b.WriteString(`</div>
  <h3 class="saves-section-title">Abilities and saving throws</h3>
  <div class="abilities-3x2">`)
	for _, a := range []string{"str", "dex", "con", "int", "wis", "cha"} {
		s := m.Abilities[a]
		b.WriteString(fmt.Sprintf(`<div class="ddb-ability"><span class="ddb-ability-name">%s</span><span class="ddb-ability-mod">%s</span><span class="ddb-ability-score">%d · save %s</span></div>`,
			strings.ToUpper(a), signed(s.Mod), s.Score, signed(s.Save)))
	}
	b.WriteString(`</div>`)
	details := [][2]string{{"Hit Points", m.HPFormula}, {"Speed", m.Speed}, {"Skills", m.Skills}, {"Vulnerabilities", m.Vulnerable}, {"Resistances", m.Resistances}, {"Immunities", m.DmgImmune},
		{"Condition immunities", m.CondImmune}, {"Gear", m.Gear}, {"Senses", m.Senses}, {"Languages", m.Languages}}
	b.WriteString(`<div class="senses-stack">`)
	for _, d := range details {
		if strings.TrimSpace(d[1]) != "" {
			b.WriteString(`<div class="sense-pill"><span class="sense-lbl">` + e(d[0]) + `</span> <span>` + e(d[1]) + `</span></div>`)
		}
	}
	b.WriteString(`</div>`)
	for _, g := range []struct {
		title, kind string
		entries     []srdMonsterEntry
		intro       string
	}{{"Traits", "trait", m.Traits, ""}, {"Actions", "action", m.Actions, ""}, {"Bonus Actions", "bonus", m.BonusActions, ""},
		{"Reactions", "reaction", m.Reactions, ""}, {"Legendary Actions", "legendary", m.Legendary, m.LegendaryIn}} {
		if len(g.entries) == 0 {
			continue
		}
		b.WriteString(`<h3 class="saves-section-title">` + e(g.title) + `</h3>`)
		if g.intro != "" {
			b.WriteString(`<p style="color:var(--text-secondary);">` + e(g.intro) + `</p>`)
		}
		for _, en := range g.entries {
			b.WriteString(`<div class="dnd-list-item" style="display:block;padding:0.6rem 0;">
  <div class="font-rpg" style="font-weight:700;">` + e(en.Name) + `</div>`)
			for _, v := range entryVerdicts(g.kind, en) {
				mark, title := `<span class="badge badge-combat">GM</span>`, "the GM resolves it in play"
				if v.Status == "applied" {
					mark, title = `<span class="badge badge-arcane">Server</span>`, "applied by the server: "+v.Rule
				}
				b.WriteString(`<p style="margin:0.3rem 0;" title="` + e(title) + `">` + mark + ` ` + e(v.Sentence) + `</p>`)
			}
			for _, mv := range mechanicsView(en) {
				b.WriteString(`<p style="margin:0.3rem 0;color:var(--text-secondary);"><span class="badge badge-recruiting">Mechanic</span> ` +
					e(fmt.Sprint(mv["kind"])+": "+fmt.Sprint(mv["does"])) + `</p>`)
			}
			b.WriteString(`</div>`)
		}
	}
	b.WriteString(`<h3 class="saves-section-title">About this monster</h3><div class="senses-stack">`)
	for _, d := range [][2]string{{"Key", c.Key}, {"Built by", c.Owner}, {"Source", c.Note}, {"Replaces", c.Replaces}, {"Saved", c.Created.UTC().Format("2006-01-02 15:04 UTC")}} {
		if d[1] != "" {
			b.WriteString(`<div class="sense-pill"><span class="sense-lbl">` + e(d[0]) + `</span> <span>` + e(d[1]) + `</span></div>`)
		}
	}
	b.WriteString(`</div>
  <p style="color:var(--text-secondary);font-size:0.8rem;margin-top:0.6rem;"><span class="badge badge-arcane">Server</span> a rule of the server applies the sentence in play. <span class="badge badge-combat">GM</span> the GM resolves it.</p>
</div>`)
	return b.String()
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

func signed(n int) string {
	if n >= 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// handleCustomStatBlockPage godoc
// @Summary A GM-built monster's stat block page
// @Description The stat block of a compendium monster as a web page, in the character sheet's layout: its token, Armor Class, Hit Points, abilities and saves, senses, and every trait and action with each sentence marked as applied by the server or resolved by the GM, with the mechanics attached. It opens only with the signed link the builder's replies give as stat_block_url (good for 24 hours); without it, or after it runs out, 404.
// @Tags GM
// @Produce html
// @Param key path string true "the monster's custom: key"
// @Param exp query int true "when the link runs out (Unix seconds), from stat_block_url"
// @Param sig query string true "the link's signature, from stat_block_url"
// @Success 200 {string} string "the page"
// @Router /monsters/custom/{key} [get]
func handleCustomStatBlockPage(w http.ResponseWriter, r *http.Request) {
	key, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/monsters/custom/"))
	q := r.URL.Query()
	if !strings.HasPrefix(key, "custom:") || !statBlockLinkValid(key, q.Get("exp"), q.Get("sig")) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, wrapHTML("Stat block", `<h1>Stat block</h1><p>This link is not valid or has run out (a stat block link opens the page for 24 hours). Ask the compendium for a new one: GET /api/gm/compendium/monsters gives each monster's stat_block_url.</p>`))
		return
	}
	c, err := readCompendiumEntry(key)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeDBError(w, err, "the compendium")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	fmt.Fprint(w, wrapView(c.Monster.Name, `<section class="view-section"><h1 class="font-rpg" style="font-size:1.35rem;color:var(--accent-gold);margin-bottom:1rem;">Monster stat block</h1>`+statBlockHTML(c)+`</section>`))
}
