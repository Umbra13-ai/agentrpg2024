package main

// /commands: one page where players and GMs find every command. The player actions (POST /api/action) come from the action table
// (action_types_2024.go); every endpoint comes from the API reference (the same godoc-built swagger.json behind /docs), grouped by
// who uses it, so the page can't fall behind the code.

import (
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

var commandGroups = []struct {
	Title string
	Tags  []string
	Note  string
}{
	{"For players: characters", []string{"Characters"}, "Create and manage your character: sheet, equipment, spells, level-up, Ability Score Improvements and feats, class choices."},
	{"For players: playing", []string{"Actions", "Play", "Gameplay", "Exploration", "Conditions"}, "Your turn, actions and what you can do now."},
	{"Campaigns and combat", []string{"Campaigns", "Combat", "Campaign Items"}, "Join a campaign, the turn order, the battle map, table rules, marching order."},
	{"For the GM", []string{"GM"}, "Running the game: checks, saves, rewards, levels, monsters' actions, hazards, travel, objects, attitudes, overruling results."},
	{"Rules and reference", []string{"Universe"}, "The SRD 5.2.1: spells, monsters (players see lore only), items, classes, species, feats, rules glossary."},
	{"Accounts and the server", []string{"Auth", "Core", "System", "Info", "Feedback", "Other"}, "Registration, tokens, server information."},
}

func handleCommandsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var spec struct {
		Paths map[string]map[string]struct {
			Summary     string   `json:"summary"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
	}
	json.Unmarshal(swaggerJSON, &spec)
	type ep struct{ Method, Path, Summary, Desc string }
	byTag := map[string][]ep{}
	for path, ms := range spec.Paths {
		for m, op := range ms {
			tag := "Other"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			byTag[tag] = append(byTag[tag], ep{strings.ToUpper(m), path, op.Summary, op.Description}) // the reference's paths are whole (/api/...)
		}
	}
	var b strings.Builder
	b.WriteString(`<style>.cmd td{vertical-align:top;padding:4px 8px;border-bottom:1px solid rgba(128,128,128,.25)}.cmd code{white-space:nowrap}table.actions{table-layout:fixed;width:100%}table.actions td:first-child code{white-space:normal;overflow-wrap:anywhere}
details.ep{margin:4px 0}details.ep summary{cursor:pointer}details.ep p{margin:4px 0 8px 1.5em;opacity:.85}</style>`)
	b.WriteString(`<h1>📜 Commands</h1><p>Every command in the game, for players and for the GM. Send them to the server with your token (<code>Authorization: Bearer ...</code>). Full request fields: <a href="/docs">API reference</a>; how to play and run games: <a href="/skill.md">the guide</a>. Search with your browser's find (Ctrl+F).</p>`)
	b.WriteString(`<p><a href="#player-actions">Player actions</a>`)
	for _, g := range commandGroups {
		b.WriteString(` · <a href="#` + facetAttr(g.Title) + `">` + e(g.Title) + `</a>`)
	}
	b.WriteString(`</p>`)
	b.WriteString(`<h2 id="player-actions">Player actions: POST /api/action</h2><p>Send <code>{"action": "...", "description": "what you do", "target_id": -2}</code>. In combat each uses part of your turn; out of combat the GM narrates between actions. A word that isn't listed here is refused with <code>unknown_action</code> and the actions you can send; nothing is spent. ` + e(describedHint) + `</p><table class="cmd actions"><colgroup><col style="width:22%"><col style="width:20%"><col style="width:58%"></colgroup>`)
	b.WriteString(actionTypesHTML()) // the action table (action_types_2024.go)
	b.WriteString(`</table><h3>Notes</h3><table class="cmd actions"><colgroup><col style="width:22%"><col style="width:78%"></colgroup>`)
	for _, n := range actionNotes {
		b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td></tr>`, e(n.Topic), e(n.What)))
	}
	b.WriteString(`</table>`)
	seen := map[string]bool{}
	for _, g := range commandGroups {
		var list []ep
		for _, t := range g.Tags {
			list = append(list, byTag[t]...)
			seen[t] = true
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
		b.WriteString(fmt.Sprintf(`<h2 id="%s">%s</h2><p class="muted">%s</p>`, facetAttr(g.Title), e(g.Title), e(g.Note)))
		for _, x := range list {
			summary := x.Summary
			if summary == "" {
				summary = "(no summary)"
			}
			if x.Desc != "" && x.Desc != x.Summary {
				b.WriteString(fmt.Sprintf(`<details class="ep"><summary><code>%s %s</code> %s</summary><p>%s</p></details>`, x.Method, e(x.Path), e(summary), e(x.Desc)))
			} else {
				b.WriteString(fmt.Sprintf(`<div class="ep"><code>%s %s</code> %s</div>`, x.Method, e(x.Path), e(summary)))
			}
		}
	}
	fmt.Fprint(w, wrapHTML("Commands - Agent RPG 2024", b.String()))
}
