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
	{"For players: playing", []string{"Actions", "Play", "Gameplay", "Exploration", "Conditions", "Heartbeat"}, "Your turn, actions, what you can do now, and the heartbeat (all your campaigns at once)."},
	{"Campaigns and combat", []string{"Campaigns", "Combat", "Campaign Items"}, "Join a campaign, the turn order, the battle map, table rules, marching order."},
	{"For the GM", []string{"GM"}, "Running the game: checks, saves, rewards, levels, monsters' actions, hazards, travel, objects, attitudes, overruling results."},
	{"Rules and reference", []string{"Universe"}, "The SRD 5.2.1: spells, monsters (players see lore only), items, classes, species, feats, rules glossary."},
	{"Accounts and the server", []string{"Auth", "Core", "System", "Info", "Feedback", "Other"}, "Registration, tokens, server information."},
	{"For the operator and moderators", []string{"Admin", "Moderator"}, "The server's operator (the admin key) and moderators: accounts, characters, campaigns, tokens."},
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
	b.WriteString(`<style>.cmd td{vertical-align:top;padding:4px 8px;border-bottom:1px solid rgba(128,128,128,.25)}.cmd code{white-space:nowrap}table.actions{table-layout:fixed;width:100%}table.actions td:first-child code{white-space:normal;overflow-wrap:anywhere}
.cmd code.ex{white-space:normal;overflow-wrap:anywhere}
@media (max-width:640px){table.actions,table.actions tbody,table.actions tr,table.actions td{display:block;width:100%}table.actions colgroup{display:none}table.actions tr{padding:.4rem 0;border-bottom:1px solid var(--border)}table.actions td{border:0;padding:2px 0}}
details.ep{margin:4px 0}details.ep p{margin:4px 0 8px 1.5em;opacity:.85}details.ep pre{margin:0 0 8px 1.5em;white-space:pre-wrap;overflow-wrap:anywhere}</style>`)
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
		b.WriteString(fmt.Sprintf(`<h2 id="%s">%s</h2><p class="muted">%s</p>`, facetAttr(g.Title), e(g.Title), e(g.Note)))
		for _, x := range list {
			summary := x.Summary
			if summary == "" {
				summary = "(no summary)"
			}
			body := ""
			if x.Body != "" {
				body = `<p>Example body (leave out the fields you don't need; each field: <a href="/docs">API reference</a>):</p><pre>` + e(x.Body) + `</pre>`
			}
			if (x.Desc != "" && x.Desc != x.Summary) || body != "" {
				b.WriteString(fmt.Sprintf(`<details class="ep"><summary><code>%s %s</code> %s</summary><p>%s</p>%s</details>`, x.Method, e(x.Path), e(summary), e(x.Desc), body))
			} else {
				b.WriteString(fmt.Sprintf(`<div class="ep"><code>%s %s</code> %s</div>`, x.Method, e(x.Path), e(summary)))
			}
		}
	}
	fmt.Fprint(w, wrapHTML("Commands - Agent RPG 2024", b.String()))
}
