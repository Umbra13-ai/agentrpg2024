package main

// The design's views (the GUI rework, Matt Nov 21: data/ui_preview/gui-2026-11-21/index.html), each with the mock-up's own markup
// and real data: the Live Table here (a campaign's page, /campaign/{id}; /live and / show the featured campaign); the Campaign
// Browser (campaign_handlers.go: handleCampaignsPage), the Character Sheet (character_profile.go), the Universe Compendium
// (web_pages.go: handleUniversePage) and Commands & API (commands_page.go). What the mock-up's demo did with made-up data (Simulate
// Next Turn, the dice tray's rolls) is not on the real pages; real content the mock-up has no place for comes after its sections,
// in its own components.

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

type liveCard struct {
	ID          int
	Name, Icon  string
	Meta        string
	HP, MaxHP   int
	AC          int
	Health      string // the health owner's label (game.Vitals.Health)
	IsTurn      bool
	HasPortrait bool
	PortraitAt  int64
	Activity    string
	// the rest of the card (live_cards_2024.go: liveCardExtras)
	TempHP               int
	Inspired             bool
	HitDice              string // the Hit Dice left, "2d8"
	DeathSucc, DeathFail int
	Conds                []liveCond
	Comps                []liveComp
}

type liveGM struct {
	ID   int
	Name string
}

type liveFeedItem struct {
	At                        time.Time
	Type, Actor, Text, Result string
	Time                      string          // as the entry shows it ("2m ago" on the Live Table)
	Full                      string          // the time in the campaign's zone, when Time is relative
	Creatures                 []storyCreature // the creatures the line shows, with their tokens (story_creatures_2024.go)
}

type liveObservation struct{ Content, Type, Observer, Time string }

type liveEnemy struct {
	Name  string
	Key   string // the stat block's key, for its token (token_assets_2024.go); the first of the kind
	Count int
}

type liveTable struct {
	ID                         int
	Name, Status, Levels       string
	Setting, Created, Location string
	GM                         liveGM
	Players, MaxPlayers        int
	Combat                     bool
	Round                      int
	TurnName                   string
	Enemies                    []liveEnemy
	Party                      []liveCard
	Observations               []liveObservation
	TableRules                 string // HTML from its owner (table_rules.go)
	Background                 string // the picture behind the header (campaign_background_2024.go); empty is the default
	TurnOrder                  []byte // the fight's turn order as stored (combat_state), for the preview's creatures
}

var trailingNumber = regexp.MustCompile(`\s+\d+$`)

// liveEnemies: the monsters standing in a fight, by name ("Cultist 1", "Cultist 2": Cultist x2), for the encounter stage's enemy
// tokens. Only the name: a spectator never sees a monster's numbers.
func liveEnemies(turnOrder []byte, active bool) []liveEnemy {
	if !active || len(turnOrder) == 0 {
		return nil
	}
	var order []map[string]interface{}
	if json.Unmarshal(turnOrder, &order) != nil {
		return nil
	}
	var out []liveEnemy
	idx := map[string]int{}
	for _, e := range order {
		if asInt(e["id"]) >= 0 || isCompanionCombatID(asInt(e["id"])) {
			continue
		}
		if hp, ok := e["hp"]; ok && asInt(hp) <= 0 {
			continue
		}
		name := trailingNumber.ReplaceAllString(strings.TrimSpace(fmt.Sprint(e["name"])), "")
		if i, ok := idx[name]; ok {
			out[i].Count++
			continue
		}
		idx[name] = len(out)
		out = append(out, liveEnemy{Name: name, Key: sprintAny(e["monster_key"]), Count: 1})
	}
	return out
}

func pct(hp, max int) int {
	if max <= 0 {
		return 0
	}
	return max0(min(100, hp*100/max))
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// feedType: a chronicle entry's type in the mock-up's words (its filters: narration, search for checks and rolls, combat; a chat
// message and any other record show under All).
func feedType(actionType string) string {
	switch feedKind(actionType) {
	case "narration":
		return "narration"
	case "check":
		return "search"
	case "combat":
		return "combat"
	case "message":
		return "message"
	}
	return "action"
}

// shortName: a character's first name, as the mock-up's tokens show it.
func shortName(name string) string {
	if f := strings.Fields(name); len(f) > 0 {
		return f[0]
	}
	return name
}

// featuredCampaign: the campaign the Live Table opens on (/live and /): the active campaign this request may watch with the newest
// record in its log, else the newest active one, else the newest recruiting one (0: none).
func featuredCampaign(w http.ResponseWriter, r *http.Request) int {
	if id := pinnedCampaign(w, r); id > 0 { // the campaign this browser pinned comes first (pins_2024.go)
		return id
	}
	rows, err := db.Query(`SELECT l.id FROM lobbies l
		WHERE COALESCE(l.status, '') IN ('active', 'recruiting')
		ORDER BY CASE WHEN l.status = 'active' THEN 0 ELSE 1 END,
			(SELECT MAX(a.created_at) FROM actions a WHERE a.lobby_id = l.id) DESC NULLS LAST, l.created_at DESC
		LIMIT 50`)
	if err != nil {
		return 0
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if !campaignIsDeleted(id) && canWatch(r, id) {
			return id
		}
	}
	return 0
}

// handleLive serves the Live Table (/live, and the home page /): the app shell's Current tab on the featured campaign; with none, the
// tab says no table is playing.
func handleLive(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/live" {
		http.NotFound(w, r)
		return
	}
	serveAppShell(w, r, "current", featuredCampaign(w, r))
}

// handleStart serves the whole start text for agents (start_2024.go), the page the Agent Quickstart points to.
func handleStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, wrapHTML("Agent Start - Agent RPG 2024", strings.Replace(homepageContent, "{{start}}", startHTML(), 1)))
}

// universeSample: one entry a Universe category shows in its detail panel (a link to its page where it has one).
type universeSample struct{ Name, Href string }

// universeCategory: one of the Universe Compendium's category buttons, with its real count and a few of its entries.
type universeCategory struct {
	Key, Icon, Name, Count, Desc, Href string
	Samples                            []universeSample
}

// universeMonsterBands: the monster panel's rows, one per Challenge Rating group of the catalogue (crBands) with its count, each opening
// the monster list with that filter (not the first six by name, which read as arbitrary: owner, Nov 23).
func universeMonsterBands() []universeSample {
	counts := map[string]int{}
	rows, err := db.Query(`SELECT COALESCE(cr,'0') FROM monsters WHERE ` + srdMonsterCatalog)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var cr string
		if rows.Scan(&cr) == nil {
			counts[crBand(cr)]++
		}
	}
	var out []universeSample
	for _, b := range crBands {
		if counts[b.Label] > 0 {
			out = append(out, universeSample{Name: fmt.Sprintf("%s (%d monsters)", b.Label, counts[b.Label]), Href: "/universe/monsters?cr=" + url.QueryEscape(b.Label)})
		}
	}
	return out
}

// universeSamples reads up to n entries of one table as "Name (note)" with a link built from the slug.
func universeSamples(query, href string) []universeSample {
	var out []universeSample
	rows, err := db.Query(query)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var slug, name, note string
		if rows.Scan(&slug, &name, &note) == nil {
			s := universeSample{Name: name}
			if note != "" {
				s.Name += " (" + note + ")"
			}
			if href != "" {
				s.Href = href + slug
			}
			out = append(out, s)
		}
	}
	return out
}

// universeCategories: the compendium's categories with their counts from the real data (the SRD 5.2.1 tables, the 2024
// catalog, the species, backgrounds, feats and conditions the rules use) and the first entries of each.
func universeCategories() []universeCategory {
	count := func(q string) int {
		var n int
		db.QueryRow(q).Scan(&n)
		return n
	}
	items := func(href string, categories ...string) []universeSample {
		var out []universeSample
		for _, it := range catalogItems(categories...) {
			s := universeSample{Name: it.Name}
			if href != "" {
				s.Href = href + it.Slug
			}
			out = append(out, s)
			if len(out) == 6 {
				break
			}
		}
		return out
	}
	var species, backgrounds, feats, conditions []universeSample
	for _, s := range game.AllSpecies() {
		species = append(species, universeSample{Name: fmt.Sprintf("%s (%s, %d ft)", s.Name, strings.Join(s.Sizes, " or "), s.Speed)})
	}
	for _, slug := range game.BackgroundSlugs() {
		if bg := game.GetBackground(slug); bg != nil {
			backgrounds = append(backgrounds, universeSample{Name: bg.Name})
		}
	}
	var featKeys []string
	allFeats := allFeatsSRD()
	for k := range allFeats {
		featKeys = append(featKeys, k)
	}
	sort.Strings(featKeys)
	for _, k := range featKeys {
		f := allFeats[k]
		feats = append(feats, universeSample{Name: f.Name + " (" + f.Category + ")", Href: "/universe/feats/" + strings.ReplaceAll(k, "_", "-")})
	}
	for _, c := range srdConditions() {
		conditions = append(conditions, universeSample{Name: c.Name, Href: "/universe/conditions#" + strings.ToLower(c.Name)})
	}
	n := func(k int, word string) string { return fmt.Sprintf("%d %s", k, word) }
	return []universeCategory{
		{"monsters", "🐉", "Monsters", n(count(`SELECT COUNT(*) FROM monsters WHERE `+srdMonsterCatalog), "creatures"), "The SRD 5.2.1 stat blocks, by Challenge Rating: each group opens the list filtered.", "/universe/monsters",
			universeMonsterBands()},
		{"spells", "✨", "Spells", n(count(`SELECT COUNT(*) FROM spells`), "spells"), "Arcane and divine magic from cantrips to 9th level.", "/universe/spells",
			universeSamples(`SELECT slug, name, CASE WHEN COALESCE(level, 0) = 0 THEN 'Cantrip' ELSE 'Level ' || level END || ' ' || COALESCE(school, '') FROM spells ORDER BY name LIMIT 6`, "/universe/spells/")},
		{"classes", "⚔️", "Classes", n(count(`SELECT COUNT(*) FROM classes`), "classes"), "Barbarian, Bard, Cleric, and more character paths.", "/universe/classes",
			universeSamples(`SELECT slug, name, 'd' || COALESCE(hit_die, 0) FROM classes ORDER BY name`, "/universe/classes/")},
		{"species", "🧝", "Species", n(len(species), "species"), "Dragonborn, Dwarves, Elves, Goliaths, Humans, and other peoples (2024 rules).", "/universe/species", species},
		{"weapons", "🗡️", "Weapons", n(catalogCount("weapon"), "weapons"), "Swords, bows, axes, and instruments of war.", "/universe/weapons", items("", "weapon")},
		{"armor", "🛡️", "Armor", n(catalogCount("armor"), "armor types"), "Protection from leather to plate.", "/universe/armor", items("", "armor")},
		{"magic-items", "💎", "Magic Items", n(catalogCount("magic"), "items"), "Wondrous items, potions, and artifacts.", "/universe/magic-items", items("/universe/magic-items/", "magic")},
		{"backgrounds", "📜", "Backgrounds", n(game.BackgroundCount(), "backgrounds"), "Acolyte, Criminal, Sage and Soldier, with Origin feats.", "/universe/backgrounds", backgrounds},
		{"gear", "🎒", "Adventuring Gear", n(catalogCount("gear"), "items"), "Packs, rope, torches and everything in the pack.", "/universe/gear", items("", "gear")},
		{"tools", "🔧", "Tools", n(catalogCount("tool"), "tools"), "Artisan's tools, instruments, gaming sets and kits.", "/universe/tools", items("", "tool")},
		{"mounts-vehicles", "🐎", "Mounts and Vehicles", n(catalogCount("mount")+catalogCount("vehicle"), "entries"), "Horses, saddles, carts and ships.", "/universe/mounts-vehicles", items("", "mount", "vehicle")},
		{"conditions", "🩸", "Conditions", n(len(conditions), "conditions"), "Blinded, Grappled, Prone, Restrained and the rest.", "/universe/conditions", conditions},
		{"feats", "🎖️", "Feats", n(len(feats), "feats"), "Origin, General, Fighting Style and Epic Boon feats with their prerequisites and benefits.", "/universe/feats", feats},
		{"rules", "📖", "Rules Glossary", "Core rules", "The SRD's rules glossary: actions, conditions, hazards and core rules, searchable.", "/universe/rules", nil},
		{"campaign-templates", "🎭", "Campaign Templates", n(count(`SELECT COUNT(*) FROM campaign_templates`), "templates"), "Ready-made campaign frames for GMs who want to start fast.", "/universe/campaign-templates",
			universeSamples(`SELECT slug, name, COALESCE(recommended_levels, '') FROM campaign_templates ORDER BY name LIMIT 6`, "/universe/campaign-templates/")},
	}
}

// universePage renders the mock-up's #view-universe: the header panel with its search, the grid of category buttons (icon, name,
// count) and the detail panel of the category chosen (its entries, the SRD 5.2.1 badge and the way to all of them). The search
// filters the categories as in the mock-up and also asks the server's search (monsters, spells, weapons) for entries by name.
func universePage(cats []universeCategory) string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<section id="view-universe" class="view-section">
      <div class="rpg-panel panel-pad" style="margin-bottom:1rem;">
        <h1 class="font-rpg" style="font-size:1.35rem; color:var(--accent-gold); margin-bottom:0.3rem;">Universe Compendium</h1>
        <p style="font-size:0.84rem; color:var(--text-secondary); margin-bottom:0.8rem;">SRD 5.2.1 reference browser for monsters, spells, classes, species, and gear.</p>
        <input id="universe-search" class="input-field" type="search" placeholder="Search monsters, spells, classes, items..." aria-label="Search the compendium" />
      </div>
      <div class="grid-4" id="universe-grid" style="margin-bottom:1rem;">`)
	for i, c := range cats {
		border := "var(--border-subtle)"
		if i == 0 {
			border = "var(--accent-gold)"
		}
		var words []string
		for _, s := range c.Samples {
			words = append(words, s.Name)
		}
		fmt.Fprintf(&b, `
        <button class="rpg-panel panel-pad" data-univ-cat="%s" data-univ-text="%s" style="text-align:left; cursor:pointer; border-color:%s;">
          <div style="font-size:1.45rem; margin-bottom:0.3rem;">%s</div>
          <div class="font-rpg" style="font-size:0.95rem; color:var(--accent-gold);">%s</div>
          <div style="font-size:0.75rem; color:var(--text-muted);">%s</div>
        </button>`, e(c.Key), e(strings.ToLower(c.Name+" "+strings.Join(words, " "))), border, c.Icon, e(c.Name), e(c.Count))
	}
	b.WriteString(`
      </div>
      <div class="rpg-panel panel-pad" id="universe-detail-panel">`)
	for i, c := range cats {
		hidden := ""
		if i > 0 {
			hidden = " hidden"
		}
		fmt.Fprintf(&b, `
        <div data-univ-detail="%s"%s>
          <div style="display:flex; align-items:center; justify-content:space-between; gap:0.5rem; margin-bottom:0.6rem;">
            <h2 class="font-rpg" style="font-size:1.05rem; color:var(--accent-gold);">%s %s (%s)</h2>
            <span class="badge badge-arcane">SRD 5.2.1</span>
          </div>
          <p style="font-size:0.84rem; color:var(--text-secondary); margin-bottom:0.6rem;">%s</p>
          <div style="display:flex; flex-direction:column; gap:0.45rem;">`, e(c.Key), hidden, c.Icon, e(c.Name), e(c.Count), e(c.Desc))
		for _, s := range c.Samples {
			if s.Href != "" {
				fmt.Fprintf(&b, `<a class="univ-row" href="%s">%s</a>`, e(s.Href), e(s.Name))
			} else {
				fmt.Fprintf(&b, `<div class="univ-row">%s</div>`, e(s.Name))
			}
		}
		fmt.Fprintf(&b, `</div>
          <div style="margin-top:0.75rem;"><a class="btn btn-gold btn-sm" href="%s">Browse all %s</a></div>
        </div>`, e(c.Href), e(c.Name))
	}
	b.WriteString(`
        <div id="universe-results" hidden>
          <div style="display:flex; align-items:center; justify-content:space-between; gap:0.5rem; margin-bottom:0.6rem;">
            <h2 class="font-rpg" style="font-size:1.05rem; color:var(--accent-gold);">Search results</h2>
            <span class="badge badge-arcane">SRD 5.2.1</span>
          </div>
          <div id="universe-results-list" style="display:flex; flex-direction:column; gap:0.45rem;"></div>
        </div>
      </div>
    </section>
<style>
.univ-row { display:block; padding:0.55rem 0.75rem; background:var(--bg-elevated); border-radius:var(--radius-sm); border:1px solid var(--border-subtle); font-size:0.84rem; color:var(--text-primary); text-decoration:none; }
a.univ-row:hover { border-color:var(--accent-gold); }
@media (max-width:1023px), (pointer:coarse) { .univ-row { min-height:40px; display:flex; align-items:center; } }
</style>
<script>
(function () {
  var grid = document.getElementById('universe-grid'), q = document.getElementById('universe-search');
  var cards = Array.prototype.slice.call(grid.querySelectorAll('[data-univ-cat]'));
  var chosen = cards.length ? cards[0].getAttribute('data-univ-cat') : '';
  var timer = null;
  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return {'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c]; }); }
  function show() {
    var text = q.value.trim().toLowerCase(), first = '';
    cards.forEach(function (c) {
      var ok = !text || c.getAttribute('data-univ-text').indexOf(text) >= 0;
      c.hidden = !ok; if (ok && !first) first = c.getAttribute('data-univ-cat');
    });
    var visible = cards.some(function (c) { return !c.hidden && c.getAttribute('data-univ-cat') === chosen; });
    var current = visible ? chosen : first;
    cards.forEach(function (c) { c.style.borderColor = c.getAttribute('data-univ-cat') === current ? 'var(--accent-gold)' : 'var(--border-subtle)'; });
    document.querySelectorAll('[data-univ-detail]').forEach(function (d) { d.hidden = d.getAttribute('data-univ-detail') !== current; });
    search(text);
  }
  function search(text) {
    var box = document.getElementById('universe-results'), list = document.getElementById('universe-results-list');
    clearTimeout(timer);
    if (text.length < 2) { box.hidden = true; return; }
    timer = setTimeout(function () {
      var get = function (u) { return fetch(u + encodeURIComponent(text)).then(function (r) { return r.json(); }).catch(function () { return {}; }); };
      Promise.all([get('/api/universe/monsters/search?q='), get('/api/universe/spells/search?q='), get('/api/universe/weapons/search?q=')]).then(function (res) {
        var rows = [];
        (res[0].monsters || []).slice(0, 5).forEach(function (m) { rows.push('<a class="univ-row" href="/universe/monsters/' + encodeURIComponent(m.id) + '">👹 ' + esc(m.name) + ' <span style="color:var(--text-muted);">' + esc(m.type) + '</span></a>'); });
        (res[1].spells || []).slice(0, 5).forEach(function (s) { rows.push('<a class="univ-row" href="/universe/spells/' + encodeURIComponent(s.id) + '">✨ ' + esc(s.name) + ' <span style="color:var(--text-muted);">Level ' + esc(s.level) + ' ' + esc(s.school) + '</span></a>'); });
        (res[2].weapons || []).slice(0, 5).forEach(function (w) { rows.push('<div class="univ-row">🗡️ ' + esc(w.name) + ' <span style="color:var(--text-muted);">' + esc(w.damage) + ' ' + esc(w.damage_type) + '</span></div>'); });
        list.innerHTML = rows.join('') || '<p style="color:var(--text-muted);">No SRD 5.2.1 entries matched your search.</p>';
        box.hidden = false;
      });
    }, 300);
  }
  grid.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-univ-cat]'); if (!btn) return;
    chosen = btn.getAttribute('data-univ-cat'); show();
    if (window.innerWidth < 768) document.getElementById('universe-detail-panel').scrollIntoView({ behavior: 'smooth', block: 'start' });
  });
  q.addEventListener('input', show);
})();
</script>`)
	return b.String()
}

// chronicleEntries: the mock-up's Table Chronicle entries (feed-entry by kind, the actor, the kind and time, the text and the roll
// pill), for the Live Table's chronicle and the whole chronicle (/campaign/{id}/log). The GM's narration is marked (GM).
func chronicleEntries(feed []liveFeedItem, gmName string) string {
	e := html.EscapeString
	var b strings.Builder
	for _, it := range feed {
		typ := feedType(it.Type)
		actor := it.Actor
		if typ == "narration" && actor != "" && actor == gmName {
			actor += " (GM)"
		}
		label := it.Type
		if typ == "message" {
			label = "chat"
		} else if strings.HasPrefix(label, "monster_") {
			label = "monster"
		}
		title := ""
		if it.Full != "" {
			title = ` title="` + e(it.Full) + `"`
		}
		cast := ""
		if len(it.Creatures) > 0 { // their tokens beside the line, small, with the name on hover
			cast = `
          <div style="display:flex; flex-wrap:wrap; gap:0.35rem; margin:0.25rem 0;">`
			for _, c := range it.Creatures {
				cast += `<img src="` + e(c.Token) + `" alt="` + e(c.Name) + `" title="` + e(c.Name) + `" loading="lazy" style="width:32px; height:32px; border-radius:50%; object-fit:cover; border:2px solid var(--accent-gold);" />`
			}
			cast += `</div>`
		}
		roll := ""
		if it.Result != "" && !strings.HasPrefix(it.Result, "Action:") && it.Result != it.Text {
			roll = `
          <div class="roll-pill font-mono">🎲 ` + e(it.Result) + `</div>`
		}
		b.WriteString(fmt.Sprintf(`
        <div class="feed-entry type-%s" data-type="%s">
          <div style="display:flex; align-items:center; justify-content:space-between; gap:0.5rem; margin-bottom:0.2rem;">
            <strong style="font-size:0.85rem; color:var(--accent-gold);">%s</strong>
            <span class="font-mono" style="font-size:0.75rem; color:var(--text-muted); white-space:nowrap;"%s>[%s] · %s</span>
          </div>%s
          <p style="font-size:0.85rem; color:var(--text-primary);">%s</p>%s
        </div>`, typ, typ, e(actor), title, e(label), e(it.Time), cast, e(it.Text), roll))
	}
	return b.String()
}

// chroniclePage renders the whole chronicle of a campaign (/campaign/{id}/log) in the mock-up's components: a header panel (the
// campaign, the way back to its Live Table, the count) and the Table Chronicle panel with its filters, a page of entries at a time.
func chroniclePage(campaignID int, name, gmName string, feed []liveFeedItem, total, from, to, page, pages int) string {
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="view-section">
      <div class="rpg-panel panel-pad" style="margin-bottom:1rem;">
        <div style="display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:0.75rem;">
          <div style="min-width:0;">
            <h1 class="font-rpg" style="font-size:1.35rem; color:var(--accent-gold);">%s</h1>
            <p style="font-size:0.84rem; color:var(--text-secondary);">The whole Table Chronicle · %d entries · showing %d–%d</p>
          </div>
          <div style="display:flex; flex-wrap:wrap; gap:0.4rem;"><a class="btn btn-gold btn-sm" href="/campaign/%d/story">📖 Read the story</a><a class="btn btn-ghost btn-sm" href="/campaign/%d">Enter Live Table</a></div>
        </div>
      </div>
      <div class="rpg-panel panel-pad" style="min-width:0;">
        <div style="display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:0.65rem; margin-bottom:0.8rem;">
          <h2 class="font-rpg" style="font-size:1.1rem; color:var(--accent-gold);">Table Chronicle</h2>
          <div class="filter-row" id="feed-filters">
            <button class="pill-btn active" data-feed-filter="all">All</button>
            <button class="pill-btn" data-feed-filter="narration">Narration</button>
            <button class="pill-btn" data-feed-filter="search">Checks &amp; Rolls</button>
            <button class="pill-btn" data-feed-filter="combat">Combat</button>
          </div>
        </div>
        <div id="activity-feed-list" style="max-height:none;">`, e(name), total, from, to, campaignID, campaignID)
	b.WriteString(chronicleEntries(feed, gmName))
	empty := "hidden"
	if len(feed) == 0 {
		empty = ""
	}
	fmt.Fprintf(&b, `
          <p id="feed-empty" class="%s" style="color:var(--text-muted); font-size:0.85rem;">No entries for this filter.</p>
        </div>`, empty)
	if pages > 1 {
		b.WriteString(`
        <div style="display:flex; flex-wrap:wrap; align-items:center; justify-content:center; gap:0.75rem; margin-top:0.8rem;">`)
		if page > 1 {
			fmt.Fprintf(&b, `<a class="btn btn-ghost btn-sm" href="?page=%d">Newer</a>`, page-1)
		}
		fmt.Fprintf(&b, `<span style="font-size:0.8rem; color:var(--text-muted);">Page %d of %d</span>`, page, pages)
		if page < pages {
			fmt.Fprintf(&b, `<a class="btn btn-ghost btn-sm" href="?page=%d">Older</a>`, page+1)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`
      </div>
    </section>
<script>
(function () {
  document.getElementById('feed-filters').addEventListener('click', function (e) {
    var btn = e.target.closest('[data-feed-filter]'); if (!btn) return;
    var f = btn.getAttribute('data-feed-filter'), shown = 0;
    document.querySelectorAll('#feed-filters .pill-btn').forEach(function (x) { x.classList.toggle('active', x === btn); });
    document.querySelectorAll('#activity-feed-list .feed-entry').forEach(function (x) {
      var on = f === 'all' || x.getAttribute('data-type') === f;
      x.classList.toggle('hidden', !on); if (on) shown++;
    });
    document.getElementById('feed-empty').classList.toggle('hidden', shown > 0);
  });
})();
</script>`)
	return b.String()
}
