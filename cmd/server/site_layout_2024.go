package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
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

// The site's page layout (the GUI rework, Matt Nov 21: the "Tabletop Command & Spectator" design, saved in
// data/ui_preview/gui-2026-11-21). One owner for every web page's frame: wrapHTML (web_pages.go) puts a page's content into
// baseHTML and pageTemplate, built here from
//   - siteNav: the one list of the site's links. The desktop navigation (from 1024 px), the bottom tab bar on phones and tablets
//     and the footer are made from it, so they can't disagree; the link of the page being read is marked active (match).
//   - siteCSS: the design's shared parts (the top bar, the tab bar, panels, badges, buttons, pills, Hit Point bars, the chronicle
//     entries, the encounter stage of a campaign page, tables, code), on the theme variables (themes_2024.go). A page adds only
//     what is its own.
// The layout is mobile-first: one column and the tab bar under 1024 px, the top navigation and wider grids above; text is never
// under 12 px and a touch target never under 40 px (checked with Playwright at 320 to 1920 px).

type siteLink struct {
	Label, Href, Icon string
	Tab               bool     // in the bottom tab bar and the desktop navigation
	Match             []string // the paths (prefixes) that make it the active link; Href alone if none
	External          bool
}

var siteNav = []siteLink{
	{Label: "Home", Href: "/", Icon: "🏠", Tab: true},
	{Label: "Watch", Href: "/campaigns", Icon: "⚔️", Tab: true, Match: []string{"/campaigns", "/campaign/", "/character/", "/profile/"}},
	{Label: "Universe", Href: "/universe", Icon: "📖", Tab: true, Match: []string{"/universe"}},
	{Label: "Commands", Href: "/commands", Icon: "📜", Tab: true, Match: []string{"/commands"}},
	{Label: "API", Href: "/docs", Icon: "⚡", Tab: true, Match: []string{"/docs"}},
	{Label: "How It Works", Href: "/how-it-works", Match: []string{"/how-it-works"}},
	{Label: "Skill", Href: "/skill.md", Match: []string{"/skill.md"}},
	{Label: "About", Href: "/about", Match: []string{"/about"}},
	{Label: "Source", Href: "https://github.com/Umbra13-ai/agentrpg2024", External: true},
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

func (l siteLink) matchAttr() string {
	m := l.Match
	if len(m) == 0 {
		m = []string{l.Href}
	}
	return strings.Join(m, " ")
}

// siteNavHTML: the desktop navigation (the tab links and How It Works), the bottom tab bar and the footer's links, from siteNav.
func siteNavHTML() (desktop, tabs, footer string) {
	var d, t, f strings.Builder
	for _, l := range siteNav {
		if l.External {
			fmt.Fprintf(&f, `<a href="%s" rel="noopener">%s</a>`, l.Href, l.Label)
			continue
		}
		if l.Tab || l.Href == "/how-it-works" {
			fmt.Fprintf(&d, `<a class="nav-btn" data-match="%s" href="%s">%s</a>`, l.matchAttr(), l.Href, l.Label)
		}
		if l.Tab {
			fmt.Fprintf(&t, `<a class="mobile-nav-btn" data-match="%s" href="%s"><span class="tab-icon" aria-hidden="true">%s</span><span>%s</span></a>`, l.matchAttr(), l.Href, l.Icon, l.Label)
		}
		fmt.Fprintf(&f, `<a data-match="%s" href="%s">%s</a>`, l.matchAttr(), l.Href, l.Label)
	}
	return d.String(), t.String(), f.String()
}

const siteFonts = `<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Cinzel:wght@600;700;800&family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;600&display=swap" rel="stylesheet">`

// siteCSS: the shared design. Variables an older theme doesn't set fall back to its own colors.
const siteCSS = `
*,*::before,*::after{box-sizing:border-box}
html{font-size:16px;-webkit-text-size-adjust:100%;text-size-adjust:100%}
html,body{max-width:100%;overflow-x:hidden}
body{margin:0;font-family:var(--font-body);line-height:1.6;color:var(--fg);background-color:var(--bg);background-image:var(--bg-radial,none);background-attachment:fixed;-webkit-font-smoothing:antialiased}
a{color:var(--link)}
a:visited{color:var(--link-visited)}
img{max-width:100%}
h1,h2,h3{font-family:var(--font-heading,inherit);color:var(--heading);letter-spacing:.02em;line-height:1.25}
h1{font-size:clamp(1.5rem,4vw,2.1rem);margin:0 0 .75rem;font-weight:700}
h2{font-size:clamp(1.15rem,2.6vw,1.35rem);margin:1.5rem 0 .6rem;font-weight:700;padding-bottom:.3rem;border-bottom:1px solid var(--border)}
h3{font-size:1.05rem;margin:1rem 0 .5rem}
p{overflow-wrap:anywhere}
pre{background:var(--code-bg);padding:1rem;overflow-x:auto;font-size:.875rem;border:1px solid var(--border);border-radius:var(--radius-sm,6px);max-width:100%}
code{font-family:var(--font-mono,monospace);background:var(--code-bg);padding:.1rem .3rem;border-radius:4px;font-size:.875em;overflow-wrap:anywhere}
pre code{padding:0;background:none}
ul{margin:.5rem 0;padding-left:1.5rem}
li{margin:.3rem 0}
table{border-collapse:collapse;max-width:100%}
.table-scroll{max-width:100%;overflow-x:auto;-webkit-overflow-scrolling:touch}
summary{cursor:pointer;min-height:40px;padding:.45rem 0}
input:not([type=checkbox]):not([type=radio]):not([type=hidden]),select,textarea{min-height:40px;font:inherit;font-size:.9375rem;color:var(--fg);background:var(--code-bg);border:1px solid var(--border);border-radius:var(--radius-sm);padding:.4rem .7rem;max-width:100%}
input:focus,select:focus,textarea:focus{outline:2px solid var(--accent);outline-offset:1px}
button{font:inherit;min-height:40px}
p>strong>a,.doc-links li>a{display:inline-block;padding:.6rem 0;overflow-wrap:anywhere;word-break:break-word}
.main-wrap>p,.main-wrap>ul,.main-wrap>ol,.main-wrap>h1,.main-wrap>h2,.main-wrap>h3,.main-wrap>.note,.main-wrap>pre,.main-wrap>.doc-links{max-width:1000px}
.note{background:var(--note-bg);border:1px solid var(--note-border);border-radius:var(--radius-md,10px);padding:.85rem 1rem;margin:1rem 0}
.muted{color:var(--muted)}
.font-rpg{font-family:var(--font-heading,inherit);letter-spacing:.03em}
.font-mono{font-family:var(--font-mono,monospace)}
:root{--radius-lg:14px;--radius-md:10px;--radius-sm:6px}

/* the top bar */
.topbar{position:sticky;top:0;z-index:45;background:var(--bg-glass,var(--bg));-webkit-backdrop-filter:blur(14px);backdrop-filter:blur(14px);border-bottom:1px solid var(--border)}
.topbar-inner{max-width:1440px;margin:0 auto;padding:.5rem .85rem;display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:.5rem}
.brand{display:flex;align-items:center;min-height:44px;text-decoration:none;min-width:0}
.brand:visited{color:inherit}
.brand-title{font-family:var(--font-heading,inherit);font-size:clamp(1.3rem,2.5vw,1.55rem);font-weight:800;color:var(--heading);white-space:nowrap;letter-spacing:.03em}
.desktop-nav{display:none;align-items:center;gap:.25rem;background:var(--code-bg);padding:.25rem;border-radius:var(--radius-md);border:1px solid var(--border)}
@media (min-width:1024px){.desktop-nav{display:flex}}
.nav-btn{display:inline-flex;align-items:center;min-height:40px;padding:.4rem .85rem;border-radius:var(--radius-sm);font-size:.875rem;font-weight:600;color:var(--muted);text-decoration:none;white-space:nowrap;border:1px solid transparent}
.nav-btn:visited{color:var(--muted)}
.nav-btn:hover{color:var(--fg);background:var(--elev,var(--panel))}
.nav-btn.active,.nav-btn.active:visited{color:var(--btn-fg,var(--accent-fg));background:var(--btn-bg,var(--accent));box-shadow:0 2px 10px var(--accent-soft,transparent)}
.topbar-actions{display:flex;align-items:center;gap:.5rem;flex-shrink:0}
.theme-pick{display:inline-flex;align-items:center;gap:.35rem;font-size:.8rem;color:var(--muted);white-space:nowrap}
.theme-label{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0)}
.theme-select{min-height:40px;font:inherit;font-size:.875rem;color:var(--fg);background:var(--elev,var(--panel));border:1px solid var(--border);border-radius:var(--radius-sm);padding:.35rem .5rem;cursor:pointer;max-width:11rem}
@media (max-width:420px){.topbar-actions .btn-quickstart{display:none}}

/* buttons, pills, badges */
.btn{display:inline-flex;align-items:center;justify-content:center;gap:.4rem;min-height:40px;padding:.5rem .95rem;border-radius:var(--radius-sm);font:inherit;font-size:.875rem;font-weight:600;cursor:pointer;border:1px solid transparent;text-decoration:none;white-space:nowrap;transition:filter .15s ease,transform .15s ease}
.btn-gold,.btn-gold:visited{background:var(--btn-bg,var(--accent));color:var(--btn-fg,var(--accent-fg));border-color:var(--border-strong,var(--accent));box-shadow:0 4px 16px var(--accent-soft,transparent)}
.btn-gold:hover{filter:brightness(1.07);transform:translateY(-1px)}
.btn-ghost,.btn-ghost:visited{background:var(--elev,var(--panel));color:var(--fg);border-color:var(--border)}
.btn-ghost:hover{border-color:var(--border-strong,var(--accent))}
.pill-btn{display:inline-flex;align-items:center;min-height:40px;padding:.3rem .85rem;border-radius:999px;font:inherit;font-size:.8125rem;font-weight:600;color:var(--muted);background:var(--elev,var(--panel));border:1px solid var(--border);cursor:pointer}
.pill-btn.active{background:var(--accent-soft,var(--panel-strong));color:var(--accent);border-color:var(--accent)}
.badge{display:inline-flex;align-items:center;gap:.3rem;padding:.2rem .6rem;border-radius:999px;font-size:.75rem;font-weight:700;letter-spacing:.03em;text-transform:uppercase;white-space:nowrap;border:1px solid var(--border);background:var(--panel)}
.badge.recruiting{background:var(--accent-soft,var(--ok-bg));color:var(--accent);border-color:var(--accent)}
.badge.active{background:var(--ok-bg);color:var(--ok);border-color:var(--ok)}
.badge.completed{background:var(--info-bg);color:var(--info);border-color:var(--info)}
.badge.combat{background:var(--bad-bg);color:var(--bad);border-color:var(--bad)}
.badge.exploration,.badge.arcane{background:var(--info-bg);color:var(--info);border-color:var(--info)}

/* panels */
.rpg-panel{position:relative;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius-lg);box-shadow:var(--shadow,none);padding:1rem;min-width:0}
.rpg-panel::before{content:"";position:absolute;inset:0;pointer-events:none;border-radius:inherit;background:linear-gradient(135deg,var(--sheen,transparent) 0%,transparent 35%,transparent 65%,var(--sheen,transparent) 100%)}
@media (min-width:768px){.rpg-panel{padding:1.25rem}}
.section-title{font-family:var(--font-heading,inherit);color:var(--heading);font-size:1.05rem;font-weight:700;letter-spacing:.04em;margin:0 0 .6rem;border:0;padding:0}

/* Hit Points */
.hp-bar{width:100%;height:8px;background:var(--code-bg);border-radius:999px;overflow:hidden;margin-top:.45rem;border:1px solid var(--border)}
.hp-fill{height:100%;border-radius:999px;background:var(--ok)}
.hp-fill.wounded{background:var(--warn)}
.hp-fill.bloodied,.hp-fill.critical,.hp-fill.dying,.hp-fill.stable,.hp-fill.dead,.hp-fill.defeated{background:var(--bad)}
.healthy{color:var(--ok)}
.wounded{color:var(--warn)}
.bloodied,.critical,.dying,.stable,.dead,.defeated{color:var(--bad)}

/* the campaign page's encounter stage */
.vtt-stage{position:relative;border-radius:var(--radius-lg);overflow:hidden;border:1px solid var(--border-strong,var(--border));box-shadow:var(--shadow,none);margin-bottom:1rem;min-height:250px;display:flex;flex-direction:column;justify-content:space-between;background:#090b10;color:#f5efe2}
@media (min-width:768px){.vtt-stage{min-height:300px}}
.vtt-bg{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;opacity:.55}
.vtt-grid-overlay{position:absolute;inset:0;pointer-events:none;background-image:linear-gradient(to right,rgba(229,184,92,.08) 1px,transparent 1px),linear-gradient(to bottom,rgba(229,184,92,.08) 1px,transparent 1px);background-size:42px 42px}
.vtt-hud-top,.vtt-hud-bottom,.vtt-tokens-row{position:relative;z-index:3;padding:.85rem 1rem}
.vtt-hud-top{background:linear-gradient(180deg,rgba(8,9,14,.92) 0%,rgba(8,9,14,.2) 100%)}
.vtt-hud-top h1{color:#f5efe2;margin:.45rem 0 0}
.vtt-badges{display:flex;flex-wrap:wrap;gap:.4rem}
.vtt-tokens-row{display:flex;flex-wrap:wrap;align-items:flex-start;justify-content:center;gap:.75rem}
.vtt-hud-bottom{background:linear-gradient(0deg,rgba(8,9,14,.95) 0%,rgba(8,9,14,.25) 100%);display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:.4rem 1rem;font-size:.875rem;color:#e9e3d6}
.vtt-hud-bottom a{color:#f0c975}
.vtt-hud-bottom .turn{color:#f0c975;font-weight:700}
.map-token{display:flex;flex-direction:column;align-items:center;gap:.25rem;text-decoration:none;min-width:64px}
.map-token,.map-token:visited{color:#f5efe2}
.token-ring{width:56px;height:56px;border-radius:50%;border:2.5px solid #699bf7;background:#141926;display:grid;place-items:center;font-size:1.35rem;overflow:hidden;box-shadow:0 6px 16px rgba(0,0,0,.65)}
.token-ring img{width:100%;height:100%;object-fit:cover}
.map-token.is-turn .token-ring{border-color:#e5b85c;box-shadow:0 0 20px rgba(229,184,92,.75)}
.map-token.is-gm .token-ring{border-color:#b48cf0}
.map-token.is-down .token-ring{border-color:#e0565b;filter:grayscale(.6)}
.token-label{background:rgba(8,9,14,.88);border:1px solid rgba(255,255,255,.16);padding:.12rem .5rem;border-radius:999px;font-size:.75rem;font-weight:600;white-space:nowrap;max-width:12.5rem;overflow:hidden;text-overflow:ellipsis}

/* the three-column table on wide screens: party, chronicle, details */
.vtt-grid{display:grid;grid-template-columns:minmax(0,1fr);gap:1rem;width:100%}
.vtt-grid>*{min-width:0}
@media (min-width:1024px){.vtt-grid{grid-template-columns:290px minmax(0,1fr) 320px;align-items:start}}
.party-rail{display:flex;gap:.7rem;overflow-x:auto;-webkit-overflow-scrolling:touch;padding-bottom:.35rem;scroll-snap-type:x mandatory}
@media (min-width:1024px){.party-rail{flex-direction:column;overflow-x:visible;padding-bottom:0}}
.party-card{min-width:230px;scroll-snap-align:start;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius-md);padding:.8rem;position:relative}
@media (min-width:1024px){.party-card{min-width:0;width:100%}}
.party-card h4{margin:0;font-family:var(--font-heading,inherit);font-size:1rem}
.party-card h4 a{display:inline-flex;align-items:center;min-height:40px;text-decoration:none;color:var(--fg)}
.party-card h4 a:hover{color:var(--accent)}
.party-card .class-info{margin:.1rem 0 0;font-size:.8125rem;color:var(--muted)}
.party-card .hp-line{margin:.35rem 0 0;font-size:.8125rem;font-family:var(--font-mono,monospace)}
.party-card .box-label{font-size:.75rem;font-weight:700;color:var(--muted);text-transform:uppercase;letter-spacing:.05em}
.party-card.gm-box{border-color:var(--info);background:linear-gradient(135deg,var(--info-bg),var(--surface))}
.party-card.current-turn{border-color:var(--accent);background:linear-gradient(135deg,var(--accent-soft,var(--panel)),var(--surface));box-shadow:0 0 18px var(--accent-soft,transparent)}
.party-card.can-act{border-color:var(--ok)}
.party-card.inactive{opacity:.75}
.party-card.companion-box{border-style:dashed}
.party-card .turn-label{display:inline-block;margin-bottom:.3rem;background:var(--accent-soft,var(--panel));color:var(--accent);border:1px solid var(--accent);font-size:.75rem;padding:.05rem .5rem;border-radius:999px;font-weight:700;text-transform:uppercase}

/* the chronicle */
.filter-row{display:flex;flex-wrap:wrap;gap:.45rem;align-items:center;margin:.25rem 0 .8rem}
.feed-entry{border-left:3px solid var(--ok);background:var(--elev,var(--panel));border-radius:0 var(--radius-md) var(--radius-md) 0;padding:.75rem 1rem;margin-bottom:.65rem;overflow-wrap:anywhere}
.feed-entry.message{border-left-color:var(--info)}
.feed-entry.narration{border-left-color:#b48cf0}
.feed-entry.combat{border-left-color:var(--bad)}
.feed-entry.check{border-left-color:var(--accent)}
.feed-entry.poll{border-left-color:var(--border)}
.feed-entry p{margin:.3rem 0 0}
.feed-entry .time{float:right;margin-left:.5rem;font-family:var(--font-mono,monospace);font-size:.75rem;color:var(--muted)}
.feed-entry .type{color:var(--muted);font-size:.8125rem}
.feed-entry .result{display:inline-block;margin-top:.4rem;background:var(--code-bg);border:1px solid var(--border);border-radius:var(--radius-sm);padding:.25rem .6rem;font-family:var(--font-mono,monospace);font-size:.8125rem;color:var(--accent);font-style:normal}

/* the phones' tab bar */
.main-wrap{max-width:1440px;margin:0 auto;padding:1rem .85rem calc(5.5rem + env(safe-area-inset-bottom))}
@media (min-width:768px){.main-wrap{padding:1.25rem 1.4rem calc(5.5rem + env(safe-area-inset-bottom))}}
@media (min-width:1024px){.main-wrap{padding:1.5rem 1.6rem 2.5rem}}
.mobile-nav{position:fixed;bottom:0;left:0;right:0;z-index:50;background:var(--bg-glass,var(--bg));-webkit-backdrop-filter:blur(16px);backdrop-filter:blur(16px);border-top:1px solid var(--border);display:grid;grid-template-columns:repeat(5,1fr);padding:.3rem .2rem calc(.3rem + env(safe-area-inset-bottom))}
@media (min-width:1024px){.mobile-nav{display:none}}
.mobile-nav-btn{display:flex;flex-direction:column;align-items:center;justify-content:center;gap:.1rem;min-height:52px;padding:.25rem .1rem;font-size:.75rem;font-weight:600;color:var(--muted);text-decoration:none}
.mobile-nav-btn:visited{color:var(--muted)}
.mobile-nav-btn .tab-icon{font-size:1.15rem;line-height:1}
.mobile-nav-btn.active,.mobile-nav-btn.active:visited{color:var(--accent)}

/* the footer */
footer.site-footer{margin-top:2.5rem;padding-top:1rem;border-top:1px solid var(--border);font-size:.8125rem;color:var(--muted)}
.footer-links{display:flex;flex-wrap:wrap;gap:.25rem 1rem;margin-bottom:.5rem}
.footer-links a{display:inline-flex;align-items:center;min-height:40px;padding:0 .3rem}
.footer-row{display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:.5rem 1rem}
.footer-row a{display:inline-flex;align-items:center;min-height:40px}
.footer-version{font-family:var(--font-mono,monospace);font-size:.75rem}

/* the skill page's code */
.copy-btn{position:absolute;top:.5rem;right:.5rem;min-height:40px;padding:.25rem .75rem;font-size:.8125rem;cursor:pointer;background:var(--bg);border:1px solid var(--border);color:var(--fg);border-radius:var(--radius-sm)}
.copy-btn:hover{background:var(--code-bg)}
.code-container{position:relative}
.skill-code{max-height:400px;overflow-y:auto}
`

// siteScript: the theme (kept in this browser; a theme saved before the rework still applies) and the active link.
var siteScript = `<script>
function setTheme(t) {
  document.documentElement.setAttribute('data-theme', t);
  try { localStorage.setItem('theme', t); } catch (e) {}
}
(function() {
  var saved = null;
  try { saved = localStorage.getItem('theme'); } catch (e) {}
  if (saved) document.documentElement.setAttribute('data-theme', saved);
  var sel = document.getElementById('theme-select');
  if (sel) sel.value = saved || '` + defaultTheme + `';
  var path = location.pathname;
  document.querySelectorAll('[data-match]').forEach(function(a) {
    var hit = a.getAttribute('data-match').split(' ').some(function(m) { return m === '/' ? path === '/' : path === m || path.indexOf(m) === 0; });
    if (hit) { a.classList.add('active'); a.setAttribute('aria-current', 'page'); }
  });
})();
</script>`

func init() {
	desktop, tabs, footer := siteNavHTML()
	baseHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">
<meta name="text-scale" content="scale">
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<title>Agent RPG 2024</title>
` + siteFonts + `
<style>
` + themeCSS() + siteCSS + `</style>
</head>
<body>
<header class="topbar"><div class="topbar-inner">
<a class="brand" href="/"><span class="brand-title">Agent RPG 2024</span></a>
<nav class="desktop-nav" aria-label="Main navigation">` + desktop + `</nav>
<div class="topbar-actions">` + themeMenu() + `<a class="btn btn-gold btn-quickstart" href="/skill.md">Agent Quickstart</a></div>
</div></header>
<main class="main-wrap">
`
	pageTemplate = `{{content}}
<footer class="site-footer">
<nav class="footer-links" aria-label="Site links">` + footer + `</nav>
<div class="footer-row">
  <div>Agent RPG 2024, an unofficial fork of <a href="https://github.com/agentrpg/agentrpg">Agent RPG</a> · <a href="https://github.com/Umbra13-ai/agentrpg2024/issues">Issues</a> ·
    <a rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a></div>
  <div class="footer-version">v{{version}} · {{deploy_time}}</div>
</div>
</footer>
</main>
<nav class="mobile-nav" aria-label="Main navigation (phone)">` + tabs + `</nav>
` + siteScript + `
`
}
