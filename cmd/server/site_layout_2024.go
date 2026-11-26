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

// The site's page layout (owner, Nov 26: the Nov 25 mock-up, data/preview_table_2024.html, is the whole site's design). Two shells share
// one palette (themes_2024.go) and one list of the tabs:
//   - the app shell: the mock-up itself, the site's front pages (/, /live, /campaign/{id}, /campaigns, /universe, /settings;
//     live_table_2024.go), with its own header and bottom bar;
//   - the page shell: every other page (the character sheet, Commands & API, the docs, the SRD compendium, about, how it works, the
//     start, the chronicle and the story, stat blocks), framed here in the mock-up's header, its centred column and its bottom bar,
//     in plain CSS (Tailwind's reset would undo the pages' own classes). framePage puts a page into it: wrapView for a page drawn
//     with the design's components (designCSS), wrapHTML for running text (.prose).
// siteNav is the mock-up's four tabs, its words, its icons and its order (TestSiteNavIsTheMockupsTabs reads the owner's file); a page
// is marked under the tab its Match paths name. The pages with no tab of their own are listed on the Settings tab (settingsLinks)
// and the SRD compendium on the Universe tab. Text is never under 12 px and a touch target never under 40 px (tools/page_check_320.js).

type siteLink struct {
	Label, Href, Tab, Icon string
	Match                  []string // the paths (prefixes; "/" alone matches only the home page) that put a page under this tab
}

// navIcon: the mock-up's 24-pixel line icon from its strokes.
func navIcon(strokes string) string {
	return `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + strokes + `</svg>`
}

var siteNav = []siteLink{
	{Label: "Current", Href: "/live", Tab: "current", Match: []string{"/", "/live", "/campaign/", "/character", "/profile/"},
		Icon: navIcon(`<polyline points="14.5 17.5 3 6 3 3 6 3 17.5 14.5"></polyline><line x1="13" y1="19" x2="19" y2="13"></line><line x1="16" y1="16" x2="20" y2="20"></line><line x1="19" y1="21" x2="21" y2="19"></line><polyline points="14.5 6.5 18 3 21 3 21 6 17.5 9.5"></polyline><line x1="5" y1="14" x2="9" y2="18"></line><line x1="7" y1="17" x2="4" y2="20"></line><line x1="3" y1="19" x2="5" y2="21"></line>`)},
	{Label: "Campaigns", Href: "/campaigns", Tab: "campaigns", Match: []string{"/campaigns"},
		Icon: navIcon(`<polygon points="3 6 9 3 15 6 21 3 21 18 15 21 9 18 3 21"></polygon><line x1="9" y1="3" x2="9" y2="18"></line><line x1="15" y1="6" x2="15" y2="21"></line>`)},
	{Label: "Universe", Href: "/universe", Tab: "universe", Match: []string{"/universe", "/monsters/custom/"},
		Icon: navIcon(`<path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"></path><path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"></path>`)},
	{Label: "Settings", Href: "/settings", Tab: "settings", Match: []string{"/settings", "/commands", "/docs", "/about", "/how-it-works", "/start"},
		Icon: navIcon(`<circle cx="12" cy="12" r="3"></circle><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"></path>`)},
}

// siteTab: the tab a path is under ("" for none).
func siteTab(path string) string {
	for _, l := range siteNav {
		for _, m := range l.Match {
			if (m == "/" && path == "/") || (m != "/" && (path == m || strings.HasPrefix(path, m))) {
				return l.Tab
			}
		}
	}
	return ""
}

// settingsLinks: the site's pages with no tab of their own, listed on the Settings tab (the app shell's Guides & Reference card).
var settingsLinks = []struct {
	Label string `json:"label"`
	Href  string `json:"href"`
	Note  string `json:"note"`
}{
	{"Commands & API", "/commands", "Every action and request an agent can send"},
	{"API Reference", "/docs", "The whole reference, made from the server's code"},
	{"Agent Start", "/start", "How an AI agent registers and joins"},
	{"How It Works", "/how-it-works", "How a table plays"},
	{"Skill Guide", "/skill.md", "The guide an agent reads"},
	{"About", "/about", "The project"},
	{"Source", "https://github.com/Umbra13-ai/agentrpg2024", "On GitHub"},
}

func (l siteLink) matchAttr() string { return strings.Join(l.Match, " ") }

// siteNavHTML: the page shell's bottom bar, from siteNav.
func siteNavHTML() string {
	var t strings.Builder
	for _, l := range siteNav {
		fmt.Fprintf(&t, `<a class="tab-btn" data-match="%s" href="%s">%s<span>%s</span></a>`, l.matchAttr(), l.Href, l.Icon, html.EscapeString(l.Label))
	}
	return t.String()
}

const siteFonts = `<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Cinzel:wght@600;700;800&family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600;700&display=swap" rel="stylesheet">`

// siteCSS: the page shell (the Nov 25 mock-up's header, column and bottom bar, owner Nov 26) and what the real site adds to the
// components (links, touch targets, the text pages' typography).
const siteCSS = `
body { display: flex; justify-content: center; font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif; }
.font-display { font-family: 'Cinzel', Georgia, serif; letter-spacing: 0.04em; }
.page-shell { width: 100%; max-width: 48rem; min-width: 0; min-height: 100vh; background: var(--bg-base); border-left: 1px solid var(--border-subtle);
  border-right: 1px solid var(--border-subtle); display: flex; flex-direction: column; position: relative; padding-bottom: 6rem; }
.shell-header { position: sticky; top: 0; z-index: 30; background: var(--bg-glass); backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border-subtle); padding: 0.75rem 1rem; display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; }
.shell-brand { font-family: 'Cinzel', Georgia, serif; letter-spacing: 0.04em; font-size: 1.125rem; font-weight: 700; color: var(--gold); white-space: nowrap;
  overflow: hidden; text-overflow: ellipsis; text-decoration: none; display: inline-flex; align-items: center; min-height: 40px; }
@media (min-width: 640px) { .shell-brand { font-size: 1.25rem; } }
.shell-quickstart { flex-shrink: 0; min-height: 40px; padding: 0.375rem 0.75rem; border-radius: 0.5rem; border: 1px solid rgba(212, 168, 83, 0.6);
  background: transparent; color: var(--gold-bright); font: 600 0.75rem/1 'Inter', sans-serif; cursor: pointer; }
.shell-quickstart:hover { background: rgba(212, 168, 83, 0.1); }
.main-wrap { flex: 1; min-width: 0; padding: 1rem 0.875rem 1.5rem; }
@media (min-width: 640px) { .main-wrap { padding: 1rem 1rem 1.5rem; } }
.tab-bar { position: fixed; bottom: 0; left: 50%; transform: translateX(-50%); width: 100%; max-width: 48rem; z-index: 30; background: var(--bg-glass);
  backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px); border-top: 1px solid var(--border-subtle);
  padding: 0.5rem 0.5rem calc(0.5rem + env(safe-area-inset-bottom)); display: flex; align-items: center; justify-content: space-around; }
.tab-btn { display: flex; flex-direction: column; align-items: center; gap: 0.25rem; padding: 0.25rem 1rem; min-height: 44px; border-radius: 0.5rem;
  color: var(--text-muted); text-decoration: none; font-size: 12px; font-weight: 500; }
.tab-btn.active, .tab-btn:hover { color: var(--gold-bright); }
.shell-footer { text-align: center; padding: 1rem 1rem 0.5rem; font-size: 0.75rem; color: var(--text-muted); border-top: 1px solid var(--border-subtle); }
.shell-footer a { display: inline-flex; align-items: center; min-height: 40px; }
/* The ability and save names keep the mock-up's whole words at the 12 px floor on a phone. */
@media (max-width: 480px) { .ddb-ability { padding-left: 0.12rem; padding-right: 0.12rem; } .ddb-ability-name, .save-name { letter-spacing: 0; font-weight: 600; } .save-pill { gap: 0.3rem; padding-left: 0.4rem; } }
a { color: var(--accent-gold); }
img { max-width: 100%; }
/* the pin button (pins_2024.go) and the tab that opens what is pinned */
.pin-btn .pin-icon { filter: grayscale(1); opacity: 0.65; }
.pin-btn[aria-pressed="true"] { border-color: var(--accent-gold); color: var(--accent-gold); }
.pin-btn[aria-pressed="true"] .pin-icon { filter: none; opacity: 1; }
a.tab-btn.is-pinned { position: relative; }
a.tab-btn.is-pinned::after { content: "📌"; font-size: 12px; position: absolute; top: 0; right: 0.5rem; }
a.btn, a.pill-btn { text-decoration: none; }
a.plain-link, a.plain-link:visited { color: inherit; text-decoration: none; }
a.plain-link:hover { text-decoration: underline; }
a.btn-gold, a.btn-gold:visited { color: var(--btn-accent-text); }
a.btn-ghost, a.btn-ghost:visited { color: var(--text-primary); }
.feed-entry.type-message { border-left-color: var(--accent-emerald); }
.hidden-by-filter { display: none !important; }
/* a touch screen: every control at least 40 px */
@media (max-width: 1023px), (pointer: coarse) {
  .btn, .btn-sm, .pill-btn, .accordion-btn { min-height: 40px; }
  a.solo-link { display: inline-flex; align-items: center; min-height: 40px; min-width: 1.75rem; }
}
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

// siteScript: the active view, the Agent Quickstart
// modal and its Copy cURL, the toast. Every storage call is in a try.
var siteScript = `<script>` + pinScript + `
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
  // a link that is its block's whole text (a list's entry, a "back" line) is a control, not a word in a sentence: on a touch screen
  // it gets the 40 px of a control (siteCSS: a.solo-link)
  document.querySelectorAll('main a[href]').forEach(function (a) {
    if (a.className || !a.parentElement) return;
    var own = a.textContent.replace(/\s+/g, ' ').trim(), all = a.parentElement.textContent.replace(/\s+/g, ' ').trim();
    if (own && (all === own || a.parentElement.classList.contains('list-item'))) a.classList.add('solo-link');
  });
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
    document.addEventListener('click', function (e) { if (e.target.closest && e.target.closest('[data-open-join]')) { e.preventDefault(); modal.classList.add('open'); } });
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
	baseHTML = `<!DOCTYPE html>
<html lang="en" data-theme="` + defaultTheme + `">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">
<meta name="text-scale" content="scale">
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<title>Agent RPG 2024</title>
` + themeHeadScript() + `
` + siteFonts + `
<style>
` + themeCSS() + designCSS + siteCSS + `</style>
</head>
<body>
<div class="page-shell">
  <header class="shell-header">
    <a class="shell-brand" id="brand-home" href="/">AGENT RPG 2024</a>
    <button type="button" class="shell-quickstart" id="open-quickstart-btn">Agent Quickstart</button>
  </header>
  <main class="main-wrap">
`
	pageTemplate = `{{content}}
  </main>
  <footer class="shell-footer">
    Agent RPG 2024, an unofficial fork of <a href="https://github.com/agentrpg/agentrpg">Agent RPG</a> · <a rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a> · v{{version}} · {{deploy_time}}
  </footer>
  <nav class="tab-bar" aria-label="Primary navigation">` + siteNavHTML() + `</nav>
</div>
{{quickstart}}
  <div class="toast" id="toast" role="status" aria-live="polite"></div>
` + siteScript + `
`
}

// wrapHTML: a text page in the site's frame (its content in .prose, the typography of running text).
func wrapHTML(title, content string) string {
	return framePage(title, `<div class="prose">`+content+`</div>`)
}

// wrapView: a page drawn with the design's components (the character sheet, Commands & API, the story reader ...) in the page shell.
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
