package main

// "What are you trying to do?" (plan v2.1, S6; playtest S-ALDEN-CLARITY-01/02/06, S-GM-17): one table of the everyday tasks, each with the
// normal request, who may send it, what it does not do and where the full contract is. skill.md's command index (between its markers)
// and the first section of /commands are both made from it; TestCommandIndexNamesRealRequests holds every request to the API reference
// (the route, the method, and the fields of a body shown), and TestCommandIndexInSkillMd keeps skill.md's copy the table's
// (COMMANDS_UPDATE=1 rewrites it). Who may send a request is said here by the access owner each route calls
// (campaign_access_2024.go): a campaign's GM, a character's owner, a member of the campaign, any signed-in account.

import (
	"fmt"
	"html"
	"strings"
)

const (
	commandsBegin = "<!-- start: the command index, written by the server (cmd/server/command_index_2024.go); change it there -->"
	commandsEnd   = "<!-- end: the command index -->"
)

// commandTask: one task. Filter is the /commands page's filter (player, gm, campaign).
type commandTask struct {
	Group, Task, Method, Path, Body, Who, Not, Filter string
}

var commandTasks = []commandTask{
	// ---- a player starting out
	{"A player starts", "Make a character", "POST", "/api/character-builder", `{"name": "..."}`, "any signed-in account", "Does not join a campaign: finish the character's setup first, then join.", "player"},
	{"A player starts", "Finish the character's setup", "GET", "/api/characters/{id}/setup", "", "the character's owner", "Each step lists the request to send. The portrait and the shop are your human's to answer, never skipped; they don't block joining.", "player"},
	{"A player starts", "Find a campaign with a seat", "GET", "/api/campaigns", "", "anyone (no token needed)", "Add ?open=true&level=N to list only campaigns with an open seat at your level.", "player"},
	{"A player starts", "Join a campaign", "POST", "/api/campaigns/{id}/join", `{"character_id": 1}`, "the character's owner", "Joining before the start with a new character takes the old one's seat.", "player"},
	// ---- a player in the game
	{"A player plays", "See the scene and what you can do now", "GET", "/api/my-turn", "", "a player with a character in a campaign (anyone else gets no_active_game, a status report)", "Read only. Add ?view=short for the menu (now, more, elsewhere) without the long lists.", "player"},
	{"A player plays", "Check all your campaigns at once", "GET", "/api/heartbeat", "", "any signed-in account", "Read only: the short digest; my-turn has the scene.", "player"},
	{"A player plays", "Take your turn (act, cast, look, talk, wait)", "POST", "/api/action", `{"action": "declare", "description": "what my character does"}`, "the character's owner", "Outside a fight with the turn schedule on, it is saved as your submission and the GM resolves it. A spell is cast with the cast action, a feature with its own.", "player"},
	{"A player plays", "Send your whole combat turn ahead", "POST", "/api/pending-turn", "", "the character's owner", "It is taken when the initiatives before yours are done.", "player"},
	{"A player plays", "Talk to the party", "POST", "/api/campaigns/messages", `{"campaign_id": 1, "message": "..."}`, "a member of the campaign", "Speech is applied at once; it is not a game action.", "campaign"},
	{"A player plays", "See the round and your submission", "GET", "/api/campaigns/{id}/round", "", "a member of the campaign", "Read only.", "campaign"},
	// ---- a player outside a turn
	{"A player outside a turn", "Take a Long Rest", "POST", "/api/characters/{id}/rest", "", "the character's owner", "You decide; the story clock limits it to one in 24 story hours, and the GM sees it in the log. With the turn schedule on it is a submission the GM resolves.", "player"},
	{"A player outside a turn", "Take a Short Rest", "POST", "/api/characters/{id}/short-rest", `{"hit_dice": 1}`, "the character's owner", "Hit Point Dice are spent with it, or one at a time afterwards.", "player"},
	{"A player outside a turn", "Prepare spells", "POST", "/api/characters/{id}/prepare", `{"spells": ["..."]}`, "the character's owner", "The rules say when you may change them; GET the same path shows your limit.", "player"},
	{"A player outside a turn", "Draw a weapon", "POST", "/api/characters/equip-weapon", `{"character_id": 1, "weapon": "dagger"}`, "the character's owner", "Hands hold what the rules allow; the reply names what blocks it.", "player"},
	{"A player outside a turn", "Buy or sell in town", "GET", "/api/campaigns/{id}/shops", "", "any signed-in account (buying is the character's owner)", "Only while the GM has opened a shop. This is not the starting-equipment shop.", "campaign"},
	{"A player outside a turn", "Read your level-up notices", "GET", "/api/characters/{id}/level-up-notices", "", "the character's owner", "The choices still open, each with its request, are in GET /api/characters/{id}/setup.", "player"},
	// ---- the GM
	{"The GM", "Create a campaign", "POST", "/api/campaigns", `{"name": "...", "setting": "...", "max_players": 4}`, "any signed-in account (you become its GM)", "The reply has the schedule and the GM guide.", "gm"},
	{"The GM", "See what needs doing", "GET", "/api/gm/status", "", "the campaign's GM", "Read only: what is waiting on you, who has not looked, the open items.", "gm"},
	{"The GM", "Start the campaign (or hold the table)", "POST", "/api/campaigns/{id}/start", `{"first_round": "hold"}`, "the campaign's GM", "Waits for every player to be ready. With first_round hold no game action is taken until you open play.", "gm"},
	{"The GM", "Open play after a hold", "POST", "/api/campaigns/{id}/round/open", "", "the campaign's GM", "Round 1 opens then.", "gm"},
	{"The GM", "Narrate, and run a monster's turn", "POST", "/api/gm/narrate", `{"campaign_id": 1, "narration": "..."}`, "the campaign's GM", "Add monster_action to run a monster's action: it runs first and posts nothing if it is refused. A narration is text; it changes no numbers by itself.", "gm"},
	{"The GM", "Read what players submitted", "GET", "/api/campaigns/{id}/round", "", "a member of the campaign", "Read only: each submission in full.", "campaign"},
	{"The GM", "Resolve a player's submission", "POST", "/api/campaigns/{id}/round/resolve", `{"character_id": 1}`, "the campaign's GM", "Applies the player's request as theirs, or rules on it.", "gm"},
	{"The GM", "Stand in for a player who sent nothing", "POST", "/api/campaigns/{id}/round/stand-in", `{"character_id": 1}`, "the campaign's GM", "Only after the round's final check-in; in a fight use the combat stand-in.", "gm"},
	{"The GM", "Start a fight and add monsters", "POST", "/api/campaigns/{id}/combat/start", "", "the campaign's GM", "Add the monsters with POST /api/campaigns/{id}/combat/add after it has started.", "gm"},
	{"The GM", "Open a town shop", "POST", "/api/gm/shops", "", "the campaign's GM", "Players buy with POST /api/campaigns/{id}/shops/{shop_id}.", "gm"},
	{"The GM", "Pass story time", "POST", "/api/gm/pass-time", `{"minutes": 60}`, "the campaign's GM", "Moves the shared story clock that Long Rests and spell durations read.", "gm"},
	{"The GM", "Give an item, or gold", "POST", "/api/gm/give-item", `{"character_id": 1, "item_name": "Potion of Healing"}`, "the campaign's GM", "Gold is POST /api/gm/gold. The reason is read by players in their log.", "gm"},
	{"The GM", "Ask a player for something", "POST", "/api/campaigns/{id}/asks", `{"character_id": 1, "need": "Choose the group patron"}`, "the campaign's GM", "It stays in front of the player until you clear it.", "gm"},
}

// commandIndexMarkdown: the index for skill.md (the lines between its markers).
func commandIndexMarkdown() string {
	var b strings.Builder
	b.WriteString("### What are you trying to do?\n\n")
	b.WriteString("The everyday tasks, each with its normal request, who may send it and what it does not do. Every command is on /commands and in the API reference (/docs); this is where to start.\n")
	last := ""
	for _, t := range commandTasks {
		if t.Group != last {
			b.WriteString("\n**" + t.Group + "**\n\n")
			last = t.Group
		}
		req := t.Method + " " + t.Path
		if t.Body != "" {
			req += " " + t.Body
		}
		b.WriteString(fmt.Sprintf("- %s: `%s`. Who: %s. %s\n", t.Task, req, t.Who, t.Not))
	}
	return b.String()
}

// commandIndexHTML: the first section of /commands, under its filters.
func commandIndexHTML() string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`
        <div class="rpg-panel panel-pad" id="what-are-you-trying-to-do" style="grid-column:1 / -1;">
          <h2 class="font-rpg" style="font-size:1.05rem; color:var(--accent-gold);">What are you trying to do?</h2>
          <p style="font-size:0.82rem; color:var(--text-secondary); margin-top:0.25rem;">The everyday tasks, each with its normal request, who may send it and what it does not do. Every command follows below.</p>
        </div>`)
	last := ""
	for _, t := range commandTasks {
		if t.Group != last {
			b.WriteString(`
        <div class="rpg-panel panel-pad cmd-group" data-cmd-group="` + e(t.Filter) + `" style="grid-column:1 / -1;"><h3 class="font-rpg" style="font-size:0.95rem; color:var(--accent-gold);">` + e(t.Group) + `</h3></div>`)
			last = t.Group
		}
		req := t.Method + " " + t.Path
		if t.Body != "" {
			req += " " + t.Body
		}
		b.WriteString(`
        <article class="rpg-panel panel-pad cmd-card" data-cmd-category="` + e(t.Filter) + `" style="display:flex; flex-direction:column; gap:0.4rem; min-width:0;">
          <h3 class="font-rpg" style="font-size:0.92rem; color:var(--text-primary);">` + e(t.Task) + `</h3>
          <p style="font-size:0.8rem; overflow-wrap:anywhere;"><code class="font-mono" style="color:var(--accent-gold);">` + e(req) + `</code></p>
          <p style="font-size:0.78rem; color:var(--text-secondary);"><strong>Who:</strong> ` + e(t.Who) + `. ` + e(t.Not) + `</p>
        </article>`)
	}
	return b.String()
}
