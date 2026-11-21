package main

import (
	"embed"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// siteAssets: the design's images (the encounter stage's background), served at /assets/ with a day's cache.
//
//go:embed data/site_assets
var siteAssets embed.FS

// handleSiteAsset serves a file of the site's design (GET /assets/{name}).
func handleSiteAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	sub, err := fs.Sub(siteAssets, "data/site_assets")
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	if err != nil || name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(sub, name); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFileFS(w, r, sub, name)
}

// The site's page layout: the GUI rework (Matt, Nov 21), the "Tabletop Command & Spectator" mock-up saved in
// data/ui_preview/gui-2026-11-21/index.html, implemented as it is designed. One owner for every web page's frame:
//   - siteNav: the mock-up's five views, in its order and words (Live Table, Campaigns, Character, Universe, Commands & API). The
//     desktop navigation (from 1024 px) and the phones' bottom tab bar (the mock-up's icons and short words) are made from it; the
//     view being read is marked active by its Match paths. footerLinks: the site's other pages, in the footer.
//   - designCSS (site_design_css_2024.go): the mock-up's stylesheet as it is; siteCSS: what the real site adds (links, the touch
//     targets, the text pages' typography in .prose, the footer's links).
//   - the mock-up's top bar (brand, navigation, theme select, Agent Quickstart), its Agent Quickstart modal (quickstartModal: the
//     server's real start, start_2024.go), its toast and its footer line.
// framePage puts a page into it: wrapView for a view of the design (Live Table, Campaigns, Character, Universe, Commands & API), whose
// markup is the mock-up's own; wrapHTML for a text page (about, how it works, the guide, the universe's lists), its content in
// .prose. Text is never under 12 px and a touch target never under 40 px on a touch screen (checked with Playwright at 320 to 1920 px).

type siteLink struct {
	Label, Short, Href, Icon string
	Match                    []string // the paths (prefixes; "/" alone matches only the home page) that make it the active view
}

var siteNav = []siteLink{
	{Label: "Live Table", Short: "Live", Href: "/live", Icon: "⚔️", Match: []string{"/", "/live", "/campaign/"}},
	{Label: "Campaigns", Short: "Campaigns", Href: "/campaigns", Icon: "🗺️", Match: []string{"/campaigns"}},
	{Label: "Character", Short: "Character", Href: "/character", Icon: "🛡️", Match: []string{"/character", "/profile/"}},
	{Label: "Universe", Short: "Universe", Href: "/universe", Icon: "📖", Match: []string{"/universe"}},
	{Label: "Commands & API", Short: "API", Href: "/commands", Icon: "⚡", Match: []string{"/commands", "/docs"}},
}

// footerLinks: the site's pages outside the five views (the mock-up has no place for them in its navigation).
var footerLinks = []struct{ Label, Href string }{
	{"Agent Start", "/start"}, {"How It Works", "/how-it-works"}, {"Skill", "/skill.md"}, {"API Reference", "/docs"}, {"About", "/about"},
	{"Source", "https://github.com/Umbra13-ai/agentrpg2024"},
}

func (l siteLink) matchAttr() string { return strings.Join(l.Match, " ") }

// siteNavHTML: the desktop navigation and the bottom tab bar, from siteNav.
func siteNavHTML() (desktop, tabs string) {
	var d, t strings.Builder
	for _, l := range siteNav {
		fmt.Fprintf(&d, `<a class="nav-btn" data-match="%s" href="%s">%s</a>`, l.matchAttr(), l.Href, html.EscapeString(l.Label))
		fmt.Fprintf(&t, `<a class="mobile-nav-btn" data-match="%s" href="%s"><span aria-hidden="true">%s</span><span>%s</span></a>`, l.matchAttr(), l.Href, l.Icon, html.EscapeString(l.Short))
	}
	return d.String(), t.String()
}

const siteFonts = `<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Cinzel:wght@600;700;800&family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600;700&display=swap" rel="stylesheet">`

// siteCSS: what the real site adds to the mock-up's stylesheet.
const siteCSS = `
a { color: var(--accent-gold); }
img { max-width: 100%; }
a.brand, a.nav-btn, a.mobile-nav-btn, a.btn, a.pill-btn, a.map-token, a.party-card { text-decoration: none; }
a.nav-btn { display: inline-flex; align-items: center; }
a.mobile-nav-btn { text-align: center; }
a.btn-gold, a.btn-gold:visited { color: var(--btn-accent-text); }
a.btn-ghost, a.btn-ghost:visited, a.party-card, a.party-card:visited { color: var(--text-primary); }
a.map-token, a.map-token:visited { color: #f5efe2; }
a.party-card { display: block; }
.brand-title { color: var(--accent-gold); }
.token-label { max-width: 12.5rem; overflow: hidden; text-overflow: ellipsis; }
.map-token.is-down .token-ring { border-color: var(--accent-crimson); filter: grayscale(0.6); }
.map-token.is-gm .token-ring { border-color: #b48cf0; }
.feed-entry.type-message { border-left-color: var(--accent-emerald); }
.hidden-by-filter { display: none !important; }
/* a touch screen: every control at least 40 px */
@media (max-width: 1023px), (pointer: coarse) {
  .btn, .btn-sm, .pill-btn, .theme-select, .nav-btn, .accordion-btn { min-height: 40px; }
  .mobile-nav-btn { min-height: 46px; }
}
@media (max-width: 420px) { .topbar-actions #open-quickstart-btn { padding-left: 0.6rem; padding-right: 0.6rem; } }
/* the footer's links */
.site-footer-links { display: flex; flex-wrap: wrap; justify-content: center; gap: 0 0.9rem; margin-bottom: 0.35rem; }
.site-footer-links a { display: inline-flex; align-items: center; min-height: 40px; padding: 0 0.25rem; }
/* the modal's steps */
.qs-steps code { overflow-wrap: anywhere; }
/* the text pages (about, how it works, the guide, the universe's lists): the typography the mock-up's reset takes away */
.prose { max-width: 100%; line-height: 1.6; color: var(--text-primary); }
.prose h1 { font-family: 'Cinzel', Georgia, serif; color: var(--accent-gold); font-size: clamp(1.35rem, 3.6vw, 1.9rem); margin: 0 0 0.75rem; letter-spacing: 0.03em; line-height: 1.25; }
.prose h2 { font-family: 'Cinzel', Georgia, serif; color: var(--accent-gold); font-size: clamp(1.1rem, 2.6vw, 1.3rem); margin: 1.5rem 0 0.6rem; padding-bottom: 0.3rem; border-bottom: 1px solid var(--border-subtle); letter-spacing: 0.03em; line-height: 1.25; }
.prose h3 { font-family: 'Cinzel', Georgia, serif; color: var(--accent-gold); font-size: 1.02rem; margin: 1rem 0 0.5rem; }
.prose h4 { margin: 0.75rem 0 0.35rem; }
.prose p { margin: 0.6rem 0; overflow-wrap: anywhere; }
.prose ul, .prose ol { margin: 0.5rem 0; padding-left: 1.5rem; }
.prose li { margin: 0.3rem 0; }
.prose pre { background: var(--bg-code); padding: 1rem; overflow-x: auto; font-size: 0.875rem; border: 1px solid var(--border-subtle); border-radius: var(--radius-sm); max-width: 100%; margin: 0.6rem 0; }
.prose code { font-family: 'JetBrains Mono', ui-monospace, monospace; background: var(--bg-code); padding: 0.1rem 0.3rem; border-radius: 4px; font-size: 0.875em; overflow-wrap: anywhere; }
.prose pre code { padding: 0; background: none; }
.prose table { border-collapse: collapse; max-width: 100%; }
.prose th, .prose td { padding: 0.35rem 0.55rem; border-bottom: 1px solid var(--border-subtle); text-align: left; vertical-align: top; }
.prose summary { cursor: pointer; min-height: 40px; padding: 0.45rem 0; }
.prose input:not([type=checkbox]):not([type=radio]):not([type=hidden]), .prose select, .prose textarea { min-height: 40px; font: inherit; font-size: 0.9375rem; color: var(--text-primary); background: var(--bg-code); border: 1px solid var(--border-subtle); border-radius: var(--radius-sm); padding: 0.4rem 0.7rem; max-width: 100%; }
.prose button { font: inherit; min-height: 40px; }
.prose p > strong > a, .prose .doc-links li > a { display: inline-block; padding: 0.6rem 0; overflow-wrap: anywhere; }
.prose .rpg-panel { padding: 1rem; margin: 1rem 0; }
.table-scroll { max-width: 100%; overflow-x: auto; -webkit-overflow-scrolling: touch; }
.note { background: var(--bg-elevated); border: 1px solid var(--border-strong); border-radius: var(--radius-md); padding: 0.85rem 1rem; margin: 1rem 0; }
.muted { color: var(--text-secondary); }
.healthy { color: var(--accent-emerald); }
.wounded { color: var(--accent-gold); }
.bloodied, .critical, .dying, .stable, .dead, .defeated { color: var(--accent-crimson); }
.copy-btn { position: absolute; top: 0.5rem; right: 0.5rem; min-height: 40px; padding: 0.25rem 0.75rem; font-size: 0.8125rem; cursor: pointer; background: var(--bg-app); border: 1px solid var(--border-subtle); color: var(--text-primary); border-radius: var(--radius-sm); }
.code-container { position: relative; }
.skill-code { max-height: 400px; overflow-y: auto; }
`

// siteScript: the theme (kept in this browser; a theme saved before the rework still applies), the active view, the Agent Quickstart
// modal and its Copy cURL, the toast. Every storage call is in a try.
var siteScript = `<script>
function showToast(msg) {
  var el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.classList.add('show');
  clearTimeout(window.__toastTimer);
  window.__toastTimer = setTimeout(function () { el.classList.remove('show'); }, 2200);
}
// attackLine: a sheet's attack line in the mock-up's short form ("Mace: +3 to hit (STR +1, prof +2), reach 5 ft., 1d6 + 1 bludgeoning"),
// for the Live Table's quick sheet and the Character Sheet; the words are the sheet's, only the boilerplate goes.
function attackLine(name, d) {
  d = String(d || '').replace(/\s*Equip it to attack with it:.*$/, '');
  d = d.replace(/^(Melee or Ranged|Melee|Ranged) (Weapon |Spell )?Attack( Roll)?:\s*/, '');
  d = d.replace(/,? one target\. Hit:\s*/, ', ').replace(/ damage\.$/, '').replace(/\.$/, '');
  return name + ': ' + d;
}
function setTheme(t) {
  document.documentElement.setAttribute('data-theme', t);
  try { localStorage.setItem('theme', t); } catch (e) {}
}
function copyText(text, label) {
  function done() { showToast(label + ' copied'); }
  function fallback() {
    var ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); done(); } catch (e) { showToast('Copy failed'); }
    document.body.removeChild(ta);
  }
  if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(text).then(done, fallback); } else { fallback(); }
}
(function () {
  var saved = null;
  try { saved = localStorage.getItem('theme') || localStorage.getItem('agent_rpg_theme'); } catch (e) {}
  if (saved) document.documentElement.setAttribute('data-theme', saved);
  var sel = document.getElementById('theme-select');
  if (sel) {
    var t = saved || '` + defaultTheme + `';
    if (!Array.prototype.some.call(sel.options, function (o) { return o.value === t; })) {
      var o = document.createElement('option'); o.value = t; o.textContent = t; sel.appendChild(o);
    }
    sel.value = t;
    sel.addEventListener('change', function () { setTheme(sel.value); });
  }
  var path = location.pathname;
  document.querySelectorAll('[data-match]').forEach(function (a) {
    var hit = a.getAttribute('data-match').split(' ').some(function (m) { return m === '/' ? path === '/' : path === m || path.indexOf(m) === 0; });
    if (hit) { a.classList.add('active'); a.setAttribute('aria-current', 'page'); }
  });
  var modal = document.getElementById('quickstart-modal');
  var open = document.getElementById('open-quickstart-btn');
  var close = document.getElementById('close-modal-btn');
  if (modal && open) {
    open.addEventListener('click', function () { modal.classList.add('open'); });
    close.addEventListener('click', function () { modal.classList.remove('open'); });
    modal.addEventListener('click', function (e) { if (e.target === modal) modal.classList.remove('open'); });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') modal.classList.remove('open'); });
    document.getElementById('copy-quickstart-btn').addEventListener('click', function () {
      copyText(document.getElementById('quickstart-curl').textContent, 'cURL');
    });
  }
})();
</script>`

// quickstartModal: the mock-up's Agent Quickstart modal, with the server's real start (start_2024.go: its six steps and its first
// request as curl) and the way to the whole start text (/start).
func quickstartModal() string {
	var b strings.Builder
	b.WriteString(`<div class="modal-backdrop" id="quickstart-modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
  <div class="modal-panel">
    <div style="display:flex; align-items:center; justify-content:space-between; margin-bottom:0.8rem;">
      <h2 id="modal-title" class="font-rpg" style="font-size:1.15rem; color:var(--accent-gold);">AI Agent 6-Step Quickstart</h2>
      <button class="btn btn-ghost btn-sm" id="close-modal-btn">Close</button>
    </div>
    <ol class="qs-steps" style="padding-left:1.2rem; font-size:0.84rem; color:var(--text-primary); display:flex; flex-direction:column; gap:0.4rem; margin-bottom:0.9rem;">`)
	for _, s := range startQuickSteps() {
		b.WriteString("\n      <li>" + s + "</li>")
	}
	b.WriteString(`
    </ol>
    <pre class="code-block font-mono" id="quickstart-curl">` + html.EscapeString(startCurl()) + `</pre>
    <div style="display:flex; justify-content:space-between; align-items:center; gap:0.5rem; flex-wrap:wrap; margin-top:0.8rem;">
      <a class="btn btn-ghost btn-sm" href="/start">The whole start text</a>
      <button class="btn btn-gold btn-sm" id="copy-quickstart-btn">Copy cURL</button>
    </div>
  </div>
</div>`)
	return b.String()
}

// baseHTML and pageTemplate are the page frame framePage fills, built once here.
var baseHTML, pageTemplate string

func init() {
	desktop, tabs := siteNavHTML()
	var foot strings.Builder
	for _, l := range footerLinks {
		rel := ""
		if strings.HasPrefix(l.Href, "http") {
			rel = ` rel="noopener"`
		}
		fmt.Fprintf(&foot, `<a href="%s"%s>%s</a>`, l.Href, rel, html.EscapeString(l.Label))
	}
	baseHTML = `<!DOCTYPE html>
<html lang="en" data-theme="` + defaultTheme + `">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">
<meta name="text-scale" content="scale">
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<title>Agent RPG 2024</title>
` + siteFonts + `
<style>
` + themeCSS() + designCSS + siteCSS + `</style>
</head>
<body>
  <header class="topbar">
    <div class="topbar-inner">
      <a class="brand" id="brand-home" href="/">
        <span class="brand-title font-rpg">Agent RPG 2024</span>
      </a>
      <nav class="desktop-nav" aria-label="Main navigation">` + desktop + `</nav>
      <div class="topbar-actions">
        ` + themeMenu() + `
        <button class="btn btn-gold btn-sm" id="open-quickstart-btn">Agent Quickstart</button>
      </div>
    </div>
  </header>
  <main class="main-wrap">
`
	pageTemplate = `{{content}}
  </main>
  <nav class="mobile-nav" aria-label="Mobile navigation">` + tabs + `</nav>
{{quickstart}}
  <div class="toast" id="toast" role="status" aria-live="polite"></div>
  <footer style="text-align:center; padding:1rem 1rem 5rem 1rem; font-size:0.75rem; color:var(--text-muted); border-top:1px solid var(--border-subtle);">
    <nav class="site-footer-links" aria-label="Site links">` + foot.String() + `</nav>
    Agent RPG 2024, an unofficial fork of <a href="https://github.com/agentrpg/agentrpg">Agent RPG</a> · <a href="https://github.com/Umbra13-ai/agentrpg2024/issues">Issues</a> · <a rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a> · v{{version}} · {{deploy_time}}
  </footer>
` + siteScript + `
`
}

// wrapHTML: a text page in the site's frame (its content in .prose, the typography of running text).
func wrapHTML(title, content string) string {
	return framePage(title, `<div class="prose">`+content+`</div>`)
}

// wrapView: a view of the design (Live Table, Campaigns, Character, Universe, Commands & API) in the site's frame; its content is the
// mock-up's markup.
func wrapView(title, content string) string { return framePage(title, content) }

// framePage puts content into the frame: the title (escaped: titles often hold names chosen by users), the version and deploy time,
// the Agent Quickstart modal, and this server's address wherever the page says https://YOUR-SERVER.
func framePage(title, content string) string {
	title = template.HTMLEscapeString(title)
	page := strings.Replace(pageTemplate, "{{content}}", content, 1)
	page = strings.Replace(page, "{{quickstart}}", quickstartModal(), 1)
	page = strings.Replace(page, "{{version}}", version, 1)
	deployTime := serverStartTime // the build time if set, else the server's start, in the server's time zone
	if buildTime != "dev" {
		if t, err := time.Parse(time.RFC3339, buildTime); err == nil {
			deployTime = t.In(serverLocation()).Format("2006-01-02 15:04 MST")
		}
	}
	page = strings.Replace(page, "{{deploy_time}}", deployTime, 1)
	// one <title>, in the head: a second one in the body was ignored, so every page showed the site name only
	return withBaseURL(strings.Replace(baseHTML, "<title>Agent RPG 2024</title>", "<title>"+title+"</title>", 1) + page + "</body></html>")
}

// classIcon: the token a character without a portrait shows on a campaign's encounter stage.
func classIcon(class string) string {
	icons := map[string]string{"barbarian": "🪓", "bard": "🎵", "cleric": "☀️", "druid": "🌿", "fighter": "🛡️", "monk": "👊", "paladin": "⚜️",
		"ranger": "🏹", "rogue": "🗡️", "sorcerer": "🔥", "warlock": "👁️", "wizard": "🔮"}
	if i, ok := icons[strings.ToLower(strings.TrimSpace(class))]; ok {
		return i
	}
	return "🎭"
}

// feedKind: the kind of a campaign feed entry, for its color and the chronicle's filter (narration, check, combat, message, poll,
// or action for anything else).
func feedKind(actionType string) string {
	t := strings.ToLower(actionType)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
		return false
	}
	switch {
	case t == "message" || t == "poll":
		return t
	case t == actionNarration || has("narrat"):
		return "narration"
	case has("check", "search", "study", "influence", "save", "skill"):
		return "check"
	case has("attack", "monster", "combat", "damage", "cast", "initiative", "dodge", "dash", "disengage", "ready", "readied", "spell"):
		return "combat"
	}
	return "action"
}
