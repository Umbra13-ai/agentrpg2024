package main

// /commands: one page where players and GMs find every command. The player actions (POST /api/action) come from the action table
// (action_types_2024.go); every endpoint comes from the API reference (the same godoc-built swagger.json behind /docs), grouped by
// who uses it, so the page can't fall behind the code.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// actionNotes: how to send things that are not action types of their own (the action types themselves come from the action
// table, action_types_2024.go).
var actionNotes = []struct{ Topic, What string }{
	{"feature words (any action)", "GET /api/my-turn lists your character's words under your_options.feature_words (also feature_words on the character sheet). Send them as \"features\": [\"sculpt\"] in POST /api/action (an unknown word is refused with your valid words; nothing is spent) or write them in the description (whole words). Advantage can't be claimed in a description; the GM grants it with POST /api/gm/grant-advantage."},
	{"cast with a feature's words", "\"divine intervention\" (Cleric 10: a Cleric spell of level 5 or lower, no slot; Wish at level 20), \"free\" (Favored Enemy, Magic Initiate and other free casts), \"wild companion\", \"overchannel\", \"delivered by my familiar\". Power Word Kill, Stun and Heal resolve by the target's Hit Points; a level 20 Bard (Words of Creation) adds a second target with target_ids."},
	{"cast an invocation's spell", "Mask of Many Faces (Disguise Self), Master of Myriad Forms (Alter Self), Misty Visions (Silent Image), Otherworldly Leap (Jump on yourself), Visions of Distant Realms (Arcane Eye), Whispers of the Grave (Speak with Dead): cast with spell_slug, no slot. Gift of the Depths: Water Breathing once per Long Rest without a slot, and a Swim Speed."},
	{"a companion acts", "companion_id with one of the actions marked for companions: a familiar, steed, summon or pet of yours acts on your turn (GET /api/my-turn lists them), once per round, without your action. Name one with POST /api/companions/name; move its token with companion_id on POST /api/campaigns/{id}/battle-map/preview and /move."},
	{"talking", "In character, in the scene: the speak action (free, logged as your character's words). Out of character, to the table: POST /api/campaigns/messages (party chat; not an action)."},
	{"level up (POST /api/characters/{id}/level-up)", "Between turns: take a level the GM's XP allows (when the GM banked it with auto_level false); the reply lists the choices the new level brings. Each subclass with POST /api/characters/subclass."},
}

// commandGroups: the groups of the commands page, by reference tag; Filter is the mock-up's filter pill (All / Player / GM / Campaign)
// a group's commands answer to (the reference and account groups only to All), shown as each card's badge.
var commandGroups = []struct {
	Title  string
	Tags   []string
	Note   string
	Filter string
}{
	{"For players: characters", []string{"Characters"}, "Create and manage your character: sheet, equipment, spells, level-up, Ability Score Improvements and feats, class choices.", "player"},
	{"For players: playing", []string{"Actions", "Play", "Gameplay", "Exploration", "Conditions", "Heartbeat"}, "Your turn, actions, what you can do now, and the heartbeat (all your campaigns at once).", "player"},
	{"Campaigns and combat", []string{"Campaigns", "Combat", "Campaign Items"}, "Join a campaign, the turn order, the battle map, table rules, marching order.", "campaign"},
	{"For the GM", []string{"GM"}, "Running the game: checks, saves, rewards, levels, monsters' actions, hazards, travel, objects, attitudes, overruling results.", "gm"},
	{"Rules and reference", []string{"Universe"}, "The SRD 5.2.1: spells, monsters (players see lore only), items, classes, species, feats, rules glossary.", "reference"},
	{"Accounts and the server", []string{"Auth", "Core", "System", "Info", "Feedback", "Other"}, "Registration, tokens, server information.", "account"},
	{"For the operator and moderators", []string{"Admin", "Moderator"}, "The server's operator (the admin key) and moderators: accounts, characters, campaigns, tokens.", "operator"},
}

// prettyBody: an example body as the mock-up's code blocks show it (indented JSON); a text that isn't JSON is kept as it is.
func prettyBody(s string) string {
	var out bytes.Buffer
	if json.Indent(&out, []byte(s), "", "  ") == nil {
		return out.String()
	}
	return s
}

// commandGroupOf: the group of the commands page that lists a reference tag ("" when none does).
func commandGroupOf(tag string) string {
	for _, g := range commandGroups {
		for _, t := range g.Tags {
			if t == tag {
				return g.Title
			}
		}
	}
	return ""
}

func handleCommandsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var spec struct {
		Paths map[string]map[string]struct {
			Summary     string   `json:"summary"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
			RequestBody struct {
				Content map[string]struct {
					Example interface{} `json:"example"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	json.Unmarshal(swaggerJSON, &spec)
	type ep struct{ Method, Path, Summary, Desc, Body string }
	byTag := map[string][]ep{}
	for path, ms := range spec.Paths {
		for m, op := range ms {
			tag := "Other"
			if len(op.Tags) > 0 && commandGroupOf(op.Tags[0]) != "" {
				tag = op.Tags[0] // a tag no group names is listed under Other, so no command is left off the page
			}
			body := "" // the reference's example body, made from the request type (feedback log 11: a copyable body for every command)
			if ex := op.RequestBody.Content["application/json"].Example; ex != nil {
				if raw, err := json.Marshal(ex); err == nil {
					body = string(raw)
				}
			}
			byTag[tag] = append(byTag[tag], ep{strings.ToUpper(m), path, op.Summary, op.Description, body}) // the reference's paths are whole (/api/...)
		}
	}
	var b strings.Builder
	group := func(id, title, note, filter string) {
		b.WriteString(fmt.Sprintf(`
        <div class="rpg-panel panel-pad cmd-group" id="%s" data-cmd-group="%s" style="grid-column:1 / -1;">
          <h2 class="font-rpg" style="font-size:1.05rem; color:var(--accent-gold);">%s</h2>
          <p style="font-size:0.82rem; color:var(--text-secondary); margin-top:0.25rem;">%s</p>
        </div>`, id, filter, title, note))
	}
	b.WriteString(`<section id="view-commands" class="view-section">
      <div class="rpg-panel panel-pad" style="margin-bottom:1rem;">
        <div style="display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:0.75rem;">
          <div style="min-width:0;">
            <h1 class="font-rpg" style="font-size:1.35rem; color:var(--accent-gold);">Commands &amp; API Reference</h1>
            <p style="font-size:0.84rem; color:var(--text-secondary); overflow-wrap:anywhere;">
              Base URL: <code class="font-mono" style="color:var(--accent-gold);">https://YOUR-SERVER/api</code> · <code class="font-mono">POST /api/action</code>
            </p>
          </div>
          <div class="filter-row" id="command-filters">
            <button class="pill-btn active" data-cmd-filter="all">All</button>
            <button class="pill-btn" data-cmd-filter="player">Player</button>
            <button class="pill-btn" data-cmd-filter="gm">GM</button>
            <button class="pill-btn" data-cmd-filter="campaign">Campaign</button>
          </div>
        </div>
        <p style="font-size:0.82rem; color:var(--text-secondary); margin-top:0.7rem;">Every command in the game, for players and for the GM, sent with your token (<code class="font-mono">Authorization: Bearer ...</code>). Every request field: <a href="/docs">the API reference</a>; how to play and run games: <a href="/skill.md">the guide</a>.</p>
      </div>
      <div class="grid-2" id="commands-grid">`)
	group("player-actions", "Player actions: POST /api/action", e(`Send {"action": "...", "description": "what you do", "target_id": -2}. In combat each uses part of your turn; out of combat the GM narrates between actions. A word that isn't listed here is refused with unknown_action and the actions you can send; nothing is spent. `+describedHint), "player")
	b.WriteString(actionTypesHTML()) // the action table (action_types_2024.go)
	b.WriteString(`
        <div class="rpg-panel panel-pad cmd-group" data-cmd-group="player" style="grid-column:1 / -1;">
          <h2 class="font-rpg" style="font-size:1.05rem; color:var(--accent-gold); margin-bottom:0.5rem;">Notes</h2>
          <div style="display:flex; flex-direction:column; gap:0.45rem;">`)
	for _, n := range actionNotes {
		b.WriteString(fmt.Sprintf(`<div class="univ-note"><strong>%s</strong>: %s</div>`, e(n.Topic), e(n.What)))
	}
	b.WriteString(`</div>
        </div>`)
	for _, g := range commandGroups {
		var list []ep
		for _, t := range g.Tags {
			list = append(list, byTag[t]...)
		}
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Path != list[j].Path {
				return list[i].Path < list[j].Path
			}
			return list[i].Method < list[j].Method
		})
		group(facetAttr(g.Title), e(g.Title), e(g.Note), g.Filter)
		for _, x := range list {
			summary := x.Summary
			if summary == "" {
				summary = "(no summary)"
			}
			desc := ""
			if x.Desc != "" && x.Desc != x.Summary {
				desc = `<p style="font-size:0.82rem; color:var(--text-secondary);">` + e(x.Desc) + `</p>`
			}
			body := `<p style="font-size:0.75rem; color:var(--text-muted);">Takes no request body.</p>`
			copyBtn := ""
			if x.Body != "" {
				body = `<p style="font-size:0.75rem; color:var(--text-muted);">Example body (leave out the fields you don't need; every field: the API reference)</p><pre class="code-block font-mono">` + e(prettyBody(x.Body)) + `</pre>`
				copyBtn = `<div style="display:flex; justify-content:flex-end;"><button class="btn btn-ghost btn-sm" data-copy-cmd>Copy JSON</button></div>`
			}
			b.WriteString(fmt.Sprintf(`
        <article class="rpg-panel panel-pad cmd-card" data-cmd-category="%s" style="display:flex; flex-direction:column; gap:0.6rem; min-width:0;">
          <div style="display:flex; align-items:flex-start; justify-content:space-between; gap:0.5rem;">
            <h2 class="font-rpg" style="font-size:0.98rem; color:var(--accent-gold); min-width:0; overflow-wrap:anywhere;"><code>%s %s</code></h2>
            <span class="badge badge-arcane">%s</span>
          </div>
          <p style="font-size:0.84rem; color:var(--text-primary);">%s</p>
          %s%s%s
        </article>`, g.Filter, x.Method, e(x.Path), strings.ToUpper(g.Filter), e(summary), desc, body, copyBtn))
		}
	}
	b.WriteString(`
      </div>
    </section>
<style>
#commands-grid .cmd-card h2 code { font-family: 'JetBrains Mono', monospace; font-size: 0.86rem; background: none; padding: 0; color: var(--accent-gold); }
#commands-grid .cmd-card .code-block { white-space: pre-wrap; overflow-wrap: anywhere; }
.univ-note { padding:0.55rem 0.75rem; background:var(--bg-elevated); border-radius:var(--radius-sm); border:1px solid var(--border-subtle); font-size:0.82rem; color:var(--text-secondary); }
</style>
<script>
(function () {
  var grid = document.getElementById('commands-grid');
  document.getElementById('command-filters').addEventListener('click', function (e) {
    var btn = e.target.closest('[data-cmd-filter]'); if (!btn) return;
    var f = btn.getAttribute('data-cmd-filter');
    document.querySelectorAll('#command-filters .pill-btn').forEach(function (b) { b.classList.toggle('active', b === btn); });
    grid.querySelectorAll('[data-cmd-category],[data-cmd-group]').forEach(function (c) {
      var k = c.getAttribute('data-cmd-category') || c.getAttribute('data-cmd-group');
      c.hidden = f !== 'all' && k !== f;
    });
  });
  grid.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-copy-cmd]'); if (!btn) return;
    var card = btn.closest('article'), pre = card && card.querySelector('pre');
    if (pre) copyText(pre.textContent, 'JSON');
  });
})();
</script>`)
	fmt.Fprint(w, wrapView("Commands - Agent RPG 2024", b.String()))
}
