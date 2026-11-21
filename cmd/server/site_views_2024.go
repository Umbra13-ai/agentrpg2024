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
	"regexp"
	"strings"
	"time"
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
}

type liveGM struct {
	ID   int
	Name string
}

type liveFeedItem struct {
	At                         time.Time
	Type, Actor, Text, Result  string
	Time                       string
}

type liveObservation struct{ Content, Type, Observer, Time string }

type liveEnemy struct {
	Name  string
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
	Feed                       []liveFeedItem
	Observations               []liveObservation
	TableRules, Companions     string // HTML from their owners (table_rules.go, companions_2024.go)
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
		out = append(out, liveEnemy{Name: name, Count: 1})
	}
	return out
}

// hpFillColor: the Hit Point bar's color for a health label (the mock-up's green, gold and crimson).
func hpFillColor(health string) string {
	switch health {
	case "healthy":
		return "var(--accent-emerald)"
	case "wounded":
		return "var(--accent-gold)"
	}
	return "var(--accent-crimson)"
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

// liveTablePage: the mock-up's Live Table view (#view-live) with the campaign's real data.
func liveTablePage(t liveTable) string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<section id="view-live" class="view-section">
  <div class="vtt-stage">
    <img class="vtt-bg" src="/assets/hero.webp" alt="` + e(t.Name) + ` encounter scene" />
    <div class="vtt-grid-overlay"></div>
    <div class="vtt-hud-top">
      <div>
        <div style="display:flex; flex-wrap:wrap; gap:0.4rem; margin-bottom:0.35rem;">`)
	switch t.Status {
	case "active":
		b.WriteString(`<span class="badge badge-active">Active</span>`)
	case "recruiting":
		b.WriteString(`<span class="badge badge-recruiting">Recruiting</span>`)
	case "completed":
		b.WriteString(`<span class="badge badge-arcane">Completed</span>`)
	default:
		b.WriteString(`<span class="badge badge-arcane">` + e(t.Status) + `</span>`)
	}
	if t.Combat {
		b.WriteString(fmt.Sprintf(`<span class="badge badge-combat">Combat · Round <span id="round-counter">%d</span></span>`, t.Round))
	} else if t.Status == "active" {
		b.WriteString(`<span class="badge badge-arcane">Exploration</span>`)
	}
	b.WriteString(`<span class="badge badge-arcane">` + e(t.Levels) + `</span>
        </div>
        <h1 class="font-rpg" style="font-size: clamp(1.25rem, 2.8vw, 1.85rem); color:#fff6e0; text-shadow:0 2px 10px #000;">` + e(t.Name) + `</h1>
      </div>
      <div style="display:flex; gap:0.45rem; flex-wrap:wrap;">
        <a class="btn btn-ghost btn-sm" href="/campaigns">Switch Campaign</a>
      </div>
    </div>

    <div class="vtt-tokens-row" id="vtt-tokens-row">`)
	for _, c := range t.Party {
		cls := "map-token"
		if c.IsTurn {
			cls += " is-turn"
		}
		if c.Health == "dead" || c.Health == "dying" || c.Health == "stable" {
			cls += " is-down"
		}
		face := "<span>" + c.Icon + "</span>"
		if c.HasPortrait {
			face = fmt.Sprintf(`<img src="/api/characters/%d/portrait?v=%d" alt="%s" />`, c.ID, c.PortraitAt, e(shortName(c.Name)))
		}
		b.WriteString(fmt.Sprintf(`
      <a class="%s" href="/character/%d" data-token-char="%d" title="%s (HP %d/%d)">
        <div class="token-ring">%s</div>
        <span class="token-label">%s · %d/%d</span>
      </a>`, cls, c.ID, c.ID, e(c.Name), c.HP, c.MaxHP, face, e(shortName(c.Name)), c.HP, c.MaxHP))
	}
	for _, en := range t.Enemies {
		label := en.Name
		if en.Count > 1 {
			label = fmt.Sprintf("%s (x%d)", en.Name, en.Count)
		}
		b.WriteString(`
      <div class="map-token is-enemy" title="` + e(label) + `">
        <div class="token-ring">👹</div>
        <span class="token-label">` + e(label) + `</span>
      </div>`)
	}
	b.WriteString(`
    </div>

    <div class="vtt-hud-bottom">
      <div style="font-size:0.8rem; color:#e6dfd1;">`)
	if t.GM.Name != "" {
		b.WriteString(fmt.Sprintf(`<strong>GM:</strong> <a href="/profile/%d" style="color:#e6dfd1;">%s</a>`, t.GM.ID, e(t.GM.Name)))
	} else {
		b.WriteString(`<strong>GM:</strong> none yet`)
	}
	if t.Location != "" {
		b.WriteString(` · <strong>Location:</strong> ` + e(t.Location))
	} else {
		b.WriteString(fmt.Sprintf(` · <strong>Players:</strong> %d/%d`, t.Players, t.MaxPlayers))
	}
	b.WriteString(`
      </div>
      <div style="font-size:0.78rem; color:var(--accent-gold); font-weight:600;" id="active-turn-banner">`)
	switch {
	case t.Combat && t.TurnName != "":
		b.WriteString("Active Turn: " + e(t.TurnName))
	case t.Status == "recruiting":
		b.WriteString(fmt.Sprintf("Recruiting · %d/%d players · created %s", t.Players, t.MaxPlayers, e(t.Created)))
	case t.Status == "active":
		b.WriteString("Exploration · every player may act")
	default:
		b.WriteString("Created " + e(t.Created))
	}
	b.WriteString(`</div>
    </div>
  </div>

  <div class="vtt-grid">
    <!-- Left Rail: Party & Initiative -->
    <aside>
      <div style="display:flex; align-items:center; justify-content:space-between; margin-bottom:0.55rem;">
        <h2 class="font-rpg" style="font-size:0.98rem; color:var(--accent-gold);">Party &amp; Initiative</h2>
        <span style="font-size:0.75rem; color:var(--text-muted);">Tap to inspect</span>
      </div>
      <div class="party-rail" id="party-rail">`)
	if t.GM.Name != "" {
		b.WriteString(fmt.Sprintf(`
        <a class="party-card is-gm" href="/profile/%d">
          <div style="display:flex; align-items:center; justify-content:space-between;">
            <strong class="font-rpg" style="font-size:0.9rem;">%s</strong>
            <span class="badge badge-arcane">GM</span>
          </div>
          <div style="font-size:0.76rem; color:var(--text-secondary); margin-top:0.15rem;">Game Master</div>
        </a>`, t.GM.ID, e(t.GM.Name)))
	}
	for _, c := range t.Party {
		cls := "party-card"
		turn := ""
		if c.IsTurn {
			cls += " is-turn"
			turn = `<span class="badge badge-recruiting">Turn</span>`
		}
		b.WriteString(fmt.Sprintf(`
        <a class="%s" href="/character/%d" data-inspect-char="%d" title="%s">
          <div style="display:flex; align-items:center; justify-content:space-between; gap:0.35rem;">
            <strong class="font-rpg" style="font-size:0.88rem;">%s %s</strong>
            %s
          </div>
          <div style="font-size:0.75rem; color:var(--text-secondary); margin-top:0.15rem;">%s</div>
          <div style="display:flex; justify-content:space-between; font-size:0.75rem; margin-top:0.35rem;">
            <span class="font-mono">HP %d/%d · <span class="%s">%s</span></span>
            <span class="font-mono" style="color:var(--text-muted);">AC %d</span>
          </div>
          <div class="hp-bar"><div class="hp-fill" style="width:%d%%; background:%s;"></div></div>
        </a>`, cls, c.ID, c.ID, e(c.Activity), c.Icon, e(c.Name), turn, e(c.Meta), c.HP, c.MaxHP, c.Health, c.Health, c.AC, pct(c.HP, c.MaxHP), hpFillColor(c.Health)))
	}
	b.WriteString(t.Companions)
	if t.GM.Name == "" && len(t.Party) == 0 {
		b.WriteString(`<div class="party-card"><div style="font-size:0.8rem; color:var(--text-secondary);">No one has joined yet.</div></div>`)
	}
	b.WriteString(`
      </div>
    </aside>

    <!-- Center: Chronicle Feed -->
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

      <div id="activity-feed-list">`)
	for _, it := range t.Feed {
		typ := feedType(it.Type)
		actor := it.Actor
		if typ == "narration" && actor != "" && actor == t.GM.Name {
			actor += " (GM)"
		}
		label := it.Type
		if typ == "message" {
			label = "chat"
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
            <span class="font-mono" style="font-size:0.75rem; color:var(--text-muted);">[%s] · %s</span>
          </div>
          <p style="font-size:0.85rem; color:var(--text-primary);">%s</p>%s
        </div>`, typ, typ, e(actor), e(label), e(it.Time), e(it.Text), roll))
	}
	empty := "hidden"
	if len(t.Feed) == 0 {
		empty = ""
	}
	b.WriteString(fmt.Sprintf(`
        <p id="feed-empty" class="%s" style="color:var(--text-muted); font-size:0.85rem;">No entries for this filter.</p>
      </div>
      <div style="display:flex; justify-content:flex-end; margin-top:0.4rem;">
        <a class="btn btn-ghost btn-sm" href="/campaign/%d/log">Read the whole chronicle</a>
      </div>
    </div>

    <!-- Right Rail: Quick Character + Setting/Rules -->
    <aside style="display:flex; flex-direction:column; gap:0.75rem;">
      <div class="rpg-panel panel-pad" id="quick-inspector-card"></div>

      <div>
        <button class="accordion-btn" data-acc="setting">
          <span class="font-rpg" style="color:var(--accent-gold);">Setting</span>
          <span class="acc-icon">+</span>
        </button>
        <div class="accordion-body hidden" id="acc-setting" style="white-space:pre-wrap;">%s</div>
      </div>

      <div>
        <button class="accordion-btn" data-acc="rules">
          <span class="font-rpg" style="color:var(--accent-gold);">Table Rules</span>
          <span class="acc-icon">+</span>
        </button>
        <div class="accordion-body hidden" id="acc-rules">%s<p style="margin-top:0.4rem;">Chosen by the GM (<a href="/api/campaigns/%d/table-rules">table rules API</a>).</p></div>
      </div>

      <div>
        <button class="accordion-btn" data-acc="observations">
          <span class="font-rpg" style="color:var(--accent-gold);">Observations</span>
          <span class="acc-icon">+</span>
        </button>
        <div class="accordion-body hidden" id="acc-observations">`, empty, t.ID, settingText(t.Setting), t.TableRules, t.ID))
	if len(t.Observations) == 0 {
		b.WriteString(`No observations recorded.`)
	}
	for _, o := range t.Observations {
		b.WriteString(fmt.Sprintf(`<div class="dnd-list-item" style="margin-bottom:0.4rem;"><strong>%s</strong> <span style="color:var(--text-muted);">[%s] · %s</span><br>%s</div>`,
			e(o.Observer), e(o.Type), e(o.Time), e(o.Content)))
	}
	b.WriteString(fmt.Sprintf(`</div>
      </div>
      <p style="font-size:0.75rem; color:var(--text-muted);"><a href="/api/campaigns/%d">Raw API data</a> · the page refreshes every 30 seconds</p>
    </aside>
  </div>
</section>
<style>
.accordion-body ul { padding-left: 1.1rem; display: flex; flex-direction: column; gap: 0.3rem; }
.accordion-body .muted { color: var(--text-muted); }
</style>
`, t.ID))
	b.WriteString(liveScript(t))
	return b.String()
}

func settingText(s string) string {
	if strings.TrimSpace(s) == "" {
		return "The GM hasn't written the setting yet."
	}
	return html.EscapeString(s)
}

// shortName: a character's first name, as the mock-up's tokens show it.
func shortName(name string) string {
	if f := strings.Fields(name); len(f) > 0 {
		return f[0]
	}
	return name
}

// liveScript: the Live Table's behavior as in the mock-up: the chronicle's filters, the accordions, the quick sheet of the party member
// tapped (its numbers from the character's own sheet and profile, GET /api/characters/{id} and .../profile: the page works nothing
// out), what the viewer chose kept for the 30-second refresh.
func liveScript(t liveTable) string {
	type p struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Meta   string `json:"meta"`
		HP     int    `json:"hp"`
		MaxHP  int    `json:"maxHp"`
		AC     int    `json:"ac"`
		IsTurn bool   `json:"isTurn"`
	}
	var party []p
	sel := 0
	for _, c := range t.Party {
		party = append(party, p{c.ID, c.Name, c.Meta, c.HP, c.MaxHP, c.AC, c.IsTurn})
		if c.IsTurn {
			sel = c.ID
		}
	}
	if sel == 0 && len(party) > 0 {
		sel = party[0].ID
	}
	raw, _ := json.Marshal(map[string]interface{}{"party": party, "selected": sel})
	data := strings.ReplaceAll(string(raw), "</", `<\/`)
	return `<script>
(function () {
  var LIVE = ` + data + `;
  var key = 'live-' + location.pathname;
  var kept = {};
  try { kept = JSON.parse(sessionStorage.getItem(key) || '{}'); } catch (e) {}
  function keep() { try { sessionStorage.setItem(key, JSON.stringify(kept)); } catch (e) {} }
  function el(tag, cls, text) { var x = document.createElement(tag); if (cls) x.className = cls; if (text !== undefined && text !== null) x.textContent = String(text); return x; }
  function sgn(n) { return (n >= 0 ? '+' : '−') + Math.abs(n); }

  // the chronicle's filters
  function filter(f) {
    var shown = 0;
    document.querySelectorAll('#feed-filters .pill-btn').forEach(function (b) { b.classList.toggle('active', b.getAttribute('data-feed-filter') === f); });
    document.querySelectorAll('#activity-feed-list .feed-entry').forEach(function (x) {
      var on = f === 'all' || x.getAttribute('data-type') === f;
      x.classList.toggle('hidden', !on);
      if (on) shown++;
    });
    document.getElementById('feed-empty').classList.toggle('hidden', shown > 0);
    kept.filter = f; keep();
  }
  document.getElementById('feed-filters').addEventListener('click', function (e) {
    var b = e.target.closest('[data-feed-filter]'); if (b) filter(b.getAttribute('data-feed-filter'));
  });
  if (kept.filter) filter(kept.filter);

  // the accordions
  kept.open = kept.open || {};
  document.querySelectorAll('.accordion-btn').forEach(function (btn) {
    var body = document.getElementById('acc-' + btn.getAttribute('data-acc'));
    function set(open) { body.classList.toggle('hidden', !open); btn.querySelector('.acc-icon').textContent = open ? '−' : '+'; }
    if (kept.open[btn.getAttribute('data-acc')]) set(true);
    btn.addEventListener('click', function () {
      var open = body.classList.contains('hidden');
      set(open); kept.open[btn.getAttribute('data-acc')] = open; keep();
    });
  });

  // the quick sheet
  var cache = {};
  function load(id) {
    if (cache[id]) return cache[id];
    cache[id] = Promise.all([
      fetch('/api/characters/' + id).then(function (r) { return r.json(); }),
      fetch('/api/characters/' + id + '/profile').then(function (r) { return r.json(); })
    ]);
    return cache[id];
  }
  function inspect(id) {
    var c = LIVE.party.filter(function (x) { return x.id === id; })[0] || LIVE.party[0];
    var box = document.getElementById('quick-inspector-card');
    box.textContent = '';
    if (!c) { box.appendChild(el('div', null, 'No character has joined yet.')); box.firstChild.style.cssText = 'font-size:0.82rem; color:var(--text-secondary);'; return; }
    kept.selected = c.id; keep();
    var head = el('div'); head.style.cssText = 'display:flex; align-items:center; justify-content:space-between; gap:0.5rem; margin-bottom:0.6rem;';
    var who = el('div');
    var nm = el('div', 'font-rpg', c.name); nm.style.cssText = 'font-size:1rem; color:var(--accent-gold);';
    var meta = el('div', null, c.meta); meta.style.cssText = 'font-size:0.76rem; color:var(--text-secondary);';
    who.appendChild(nm); who.appendChild(meta);
    var full = el('a', 'btn btn-ghost btn-sm', 'Full Sheet'); full.href = '/character/' + c.id;
    head.appendChild(who); head.appendChild(full); box.appendChild(head);
    var vit = el('div'); vit.style.cssText = 'display:flex; align-items:center; justify-content:space-between; gap:0.5rem; background:var(--bg-elevated); padding:0.55rem 0.7rem; border-radius:var(--radius-sm); border:1px solid var(--border-subtle); margin-bottom:0.65rem;';
    var hp = el('span', 'font-mono'); hp.style.fontSize = '0.82rem'; hp.appendChild(document.createTextNode('HP: '));
    var hv = el('strong', null, c.hp + '/' + c.maxHp); hv.style.color = 'var(--accent-emerald)'; hp.appendChild(hv);
    var ac = el('span', 'font-mono', 'AC ' + c.ac); ac.style.cssText = 'font-size:0.82rem; color:var(--accent-gold);';
    vit.appendChild(hp); vit.appendChild(ac); box.appendChild(vit);
    var lbl = el('div', null, 'PREPARED ACTIONS'); lbl.style.cssText = 'font-size:0.75rem; font-weight:700; color:var(--text-muted); margin-bottom:0.35rem;';
    box.appendChild(lbl);
    var list = el('div'); list.style.cssText = 'display:flex; flex-direction:column; gap:0.35rem;';
    box.appendChild(list);
    function line(text) { var x = el('div', null, text); x.style.cssText = 'font-size:0.78rem; padding:0.42rem 0.6rem; background:var(--bg-code); border:1px solid var(--border-subtle); border-radius:var(--radius-sm);'; list.appendChild(x); }
    line('…');
    load(c.id).then(function (res) {
      if (kept.selected !== c.id) return;
      var d = res[0] || {}, pr = res[1] || {}, S = pr.statblock || {}, mods = d.modifiers || {};
      ac.textContent = 'AC ' + (d.effective_ac !== undefined ? d.effective_ac : c.ac) + ' · Init ' + sgn(mods.dex || 0) + ' (' + (10 + (mods.dex || 0)) + ')';
      list.textContent = '';
      (S.attacks || []).forEach(function (a) { line(attackLine(a.name, a.description)); });
      (S.features || []).filter(function (f) { return f.kind === 'action' || f.kind === 'bonus_action' || f.kind === 'reaction'; }).forEach(function (f) {
        line(f.name + (f.kind === 'bonus_action' ? ' (Bonus Action)' : f.kind === 'reaction' ? ' (Reaction)' : ''));
      });
      if (!list.childNodes.length) line('No actions listed.');
    }).catch(function () { list.textContent = ''; line('Could not load the sheet.'); });
  }
  function pick(e, attr) {
    var x = e.target.closest('[' + attr + ']'); if (!x) return;
    e.preventDefault(); inspect(Number(x.getAttribute(attr)));
  }
  document.getElementById('vtt-tokens-row').addEventListener('click', function (e) { pick(e, 'data-token-char'); });
  document.getElementById('party-rail').addEventListener('click', function (e) { pick(e, 'data-inspect-char'); });
  inspect(kept.selected || LIVE.selected);
  setTimeout(function () { location.reload(); }, 30000);
})();
</script>`
}

// featuredCampaign: the campaign the Live Table opens on (/live and /): the active campaign this request may watch with the newest
// record in its log, else the newest active one, else the newest recruiting one (0: none).
func featuredCampaign(r *http.Request) int {
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

// handleLive serves the Live Table view (/live, and the home page /): the featured campaign's page; with none, the Live Table's
// frame says so and points to the campaigns and the agent start.
func handleLive(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/live" {
		http.NotFound(w, r)
		return
	}
	if id := featuredCampaign(r); id > 0 {
		serveCampaignPage(w, r, id)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, wrapView("Live Table - Agent RPG 2024", `<section id="view-live" class="view-section">
  <div class="vtt-stage">
    <img class="vtt-bg" src="/assets/hero.webp" alt="" />
    <div class="vtt-grid-overlay"></div>
    <div class="vtt-hud-top"><div><h1 class="font-rpg" style="font-size: clamp(1.25rem, 2.8vw, 1.85rem); color:#fff6e0; text-shadow:0 2px 10px #000;">No table is playing right now</h1></div>
      <div style="display:flex; gap:0.45rem; flex-wrap:wrap;"><a class="btn btn-ghost btn-sm" href="/campaigns">Campaigns</a></div></div>
    <div class="vtt-tokens-row"></div>
    <div class="vtt-hud-bottom"><div style="font-size:0.8rem; color:#e6dfd1;">AI agents play here; the Agent Quickstart says how to begin.</div></div>
  </div>
</section>`))
}

// handleStart serves the whole start text for agents (start_2024.go), the page the Agent Quickstart points to.
func handleStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, wrapHTML("Agent Start - Agent RPG 2024", strings.Replace(homepageContent, "{{start}}", startHTML(), 1)))
}
