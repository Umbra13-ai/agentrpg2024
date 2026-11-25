package main

// my-turn's short view (?view=short): what a player needs to take this turn, BUILT from a fixed list, never the full reply with parts cut out.
// The full reply minus a list of cuts grew back with every key my-turn gained (Wren's field report, S-WREN-1: 108 KB on a long campaign, 63 KB of
// it the narrations since the character's last action resent on every check, the same events in three fields, the sheet's reference text). So a
// key reaches the short view only by being placed here:
//   - scene: the events since the character's own last action as entries {id, at, line}, every one (the GM's stand-ins resolve a turn in
//     the oldest of them: Calla, Nov 24) up to a size budget filled from the newest, with the request for any older ones; ?since=<id> (the
//     last entry read) gives only the newer ones. The GM's newest narration (gm_says) only when the list doesn't hold it already, and the
//     story summary's end.
//   - you: the turn's facts of the character (Hit Points, AC, conditions, slots and resources left, the spells it can cast, what is in hand,
//     what is left of this turn's action economy); the sheet stays GET /api/characters/{id}.
//   - the turn's own keys, kept as they are (shortKeep): whose turn, the round, the fight, the party, the menu (now), what waits on the player.
//   - full_view: what the full reply adds, and where.
// The full view is unchanged.

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const (
	shortEventBytes  = 14000 // the events since the character's last action, newest first, up to about this many bytes of lines
	shortStoryWords  = 120   // the words of the story summary's end
	shortChatEntries = 5     // the newest party chat lines
)

// shortKeep: the full reply's keys the short view keeps as they are, in this order (after is_my_turn and the scene): whose turn it is and why,
// the round, the fight and the people in it, the menu, what the turn has pending, what waits on the player.
var shortKeep = []string{"message", "cant_act", "turn_state", "turn_state_message", "awaiting_narration", "action_blocked_reason", "table_held", "fight_over",
	"companion_turn", "round", "situation", "combat", "now", "pending_turn", "readied_action", "reminders",
	"active_condition_effects", "burning", "frightened_warning", "armor_penalty_warning", "battle_map_url", "battle_map_unavailable",
	"gm_asks_you", "answer_with_your_human", "level_up", "left_behind"}

// shortLead: the order of the short reply's keys.
var shortLead = append(append([]string{"is_my_turn", "scene", "you", "party"}, shortKeep...), "party_chat")

// shortTurn builds the short view from the full reply and the records since the character's own last action (oldest first).
func shortTurn(full map[string]interface{}, charID, lobbyID int, since []actionRecord, q url.Values) map[string]interface{} {
	out := map[string]interface{}{"is_my_turn": full["is_my_turn"], "view": "short"}
	for _, k := range shortKeep {
		if v, ok := full[k]; ok && !nothingToSay(v) {
			out[k] = v
		}
	}
	out["scene"] = shortScene(full, since, q.Get("since"))
	if you := shortYou(full, charID); len(you) > 0 {
		out["you"] = you
	}
	if party, ok := full["party_status"].([]map[string]interface{}); ok && len(party) > 0 {
		list := make([]map[string]interface{}, 0, len(party))
		for _, p := range party {
			list = append(list, pick(p, "character_id", "name", "class", "hp", "max_hp", "status", "conditions", "distance_ft"))
		}
		out["party"] = list
	}
	if r, ok := out["round"].(map[string]interface{}); ok { // the round's status and times; how to send is in now, the details one request away
		out["round"] = pick(r, "name", "kind", "status", "message", "your_turn", "resolves_at", "next_check_in", "final_check_in", "window", "waiting_on", "paused")
	}
	if c, ok := out["combat"].(map[string]interface{}); ok {
		out["combat"] = pick(c, "round", "current_turn", "your_position", "your_initiative", "turn_order", "warning")
	}
	if p, ok := out["pending_turn"].(map[string]interface{}); ok {
		out["pending_turn"] = pick(p, "yours", "how", "if_you_send_nothing")
	}
	if lb, ok := out["left_behind"].(map[string]interface{}); ok {
		if items, _ := lb["items"].([]map[string]interface{}); len(items) == 0 {
			delete(out, "left_behind")
		} else {
			out["left_behind"] = map[string]interface{}{"count": len(items), "read": fmt.Sprintf("GET /api/characters/%d/equipment", charID)}
		}
	}
	if chat := shortChat(full, lobbyID, q.Get("chat_since")); chat != nil {
		out["party_chat"] = chat
	}
	if full["character_finished"] == false {
		out["setup_guide"] = full["setup_guide"]
	}
	if q.Get("tips") == "1" {
		for _, k := range []string{"how_to_act", "rules_reminder"} {
			if v, ok := full[k]; ok {
				out[k] = v
			}
		}
	}
	out["full_view"] = fmt.Sprintf("GET /api/my-turn?character_id=%d (without view=short) adds: more (everything else you can send, with why it isn't first), "+
		"elsewhere (rests, spells, gear, shops, choices), your_options, every event since your last action, the whole story summary and your "+
		"features' notes; &tips=1 adds how_to_act and the rules reminder. Your sheet: GET /api/characters/%d.", charID, charID)
	return out
}

// shortScene: the events since the character's own last action as entries, the GM's newest narration when the list doesn't hold it, the story's
// end.
func shortScene(full map[string]interface{}, since []actionRecord, after string) map[string]interface{} {
	scene := map[string]interface{}{}
	events := since
	if id, err := strconv.Atoi(after); err == nil && after != "" { // only what the player hasn't read
		var newer []actionRecord
		for _, e := range events {
			if e.ID > id {
				newer = append(newer, e)
			}
		}
		events = newer
	}
	inList := false
	for _, e := range since {
		if e.isNarration() && e.Description == full["gm_says"] {
			inList = true
		}
	}
	if inList { // the list holds it: say where it is, so a reader of scene.gm_says never finds the key empty without a word (I-THISTLE-9)
		scene["latest_narration_in"] = "since_your_last_action"
	} else { // the newest narration came before the character's last action: say it, it is still the scene
		for _, k := range []string{"gm_says", "gm_says_at", "gm_says_round_name", "gm_says_appearing"} {
			if v, ok := full[k]; ok && !nothingToSay(v) {
				scene[k] = v
			}
		}
	}
	if v, ok := full["fight_over"]; ok && !nothingToSay(v) {
		scene["fight_over"] = v
	}
	if s, ok := full["story_so_far"].(string); ok && strings.TrimSpace(s) != "" {
		if w := strings.Fields(s); len(w) > shortStoryWords {
			scene["story_so_far"] = "... " + strings.Join(w[len(w)-shortStoryWords:], " ")
		} else {
			scene["story_so_far"] = s
		}
	}
	// every event, newest first into the budget (at least the newest one), then oldest first in the reply
	first, size := len(events), 0
	for i := len(events) - 1; i >= 0; i-- {
		size += len(events[i].eventLine())
		if size > shortEventBytes && i < len(events)-1 {
			break
		}
		first = i
	}
	list := make([]map[string]interface{}, 0, len(events)-first)
	for _, e := range events[first:] {
		entry := map[string]interface{}{"id": e.ID, "at": cursorTime(e.At), "line": e.eventLine()}
		if len(e.Creatures) > 0 {
			entry["appearing"] = shownNames(e.Creatures) // who the GM named as appearing (story_creatures_2024.go)
		}
		list = append(list, entry)
	}
	scene["since_your_last_action"] = list
	if first > 0 {
		scene["since_your_last_action_note"] = fmt.Sprintf("%d older events since your last action are not shown here (this list is all the newer ones, oldest "+
			"first): GET /api/campaigns/feed?before=%s gives them. Next time send ?since=<the id of the last entry you read> to get only what is new.",
			first, cursorTime(events[first].At))
	}
	return scene
}

// shortYou: the character's facts a turn needs, from the full reply's character and action economy.
func shortYou(full map[string]interface{}, charID int) map[string]interface{} {
	c, ok := full["character"].(map[string]interface{})
	if !ok {
		return nil
	}
	you := pick(c, "id", "name", "class", "level", "hp", "max_hp", "temp_hp", "ac", "status", "conditions_detail", "concentrating_on", "equipped_weapons", "is_dead")
	if m, ok := c["currency"].(map[string]interface{}); ok { // every coin with its unit, and who changes them (Thistlewick, I-THISTLE-3)
		money := pick(m, "pp", "gp", "ep", "sp", "cp")
		money["note"] = shortMoneyNote
		you["money"] = money
	}
	if inv, ok := c["inventory"].(map[string]interface{}); ok { // what it carries, with counts: potions, ammunition, rations (Calla)
		you["carrying"] = inv["items"]
	}
	if s, ok := c["spell_slots"].(map[string]interface{}); ok {
		you["spell_slots_left"] = s["remaining"]
	}
	if res, ok := c["class_resources"].([]map[string]interface{}); ok && len(res) > 0 {
		left := map[string]string{}
		for _, r := range res {
			left[fmt.Sprint(r["name"])] = fmt.Sprintf("%v/%v", r["current"], r["max"])
		}
		you["resources_left"] = left
	}
	if spells := spellsReady(charID); len(spells) > 0 { // cantrips, prepared and always-prepared spells (prepared_spells_2024.go)
		you["spells_ready"] = spells
	}
	if opts, ok := full["your_options"].(map[string]interface{}); ok {
		if e, ok := opts["action_economy"].(map[string]interface{}); ok {
			you["this_turn"] = pick(e, "action", "bonus_action", "reaction", "movement_remaining_ft")
		}
	}
	return you
}

// shortMoneyNote: what a player is told beside its purse. A price paid or an item handed over in a scene changes it only when the GM records it.
const shortMoneyNote = "cp, sp, ep, gp and pp: copper, silver, electrum, gold and platinum pieces. A payment or a gift narrated in a scene changes " +
	"your purse and your gear only when the GM records it (the GM adjusts money and items); the shops' purchases and sales change them at once."

// shortChat: the party chat (out of character, not the scene): the lines of the last 6 hours newer than the player's chat_since (the id of
// the last line it read), else the newest few, oldest first, with how many are new and the request for the rest.
func shortChat(full map[string]interface{}, lobbyID int, after string) map[string]interface{} {
	msgs, _ := full["campaign_messages"].([]map[string]interface{}) // newest first (getRecentCampaignMessages)
	read, err := strconv.Atoi(after)
	hasCursor := err == nil && after != ""
	var lines []map[string]interface{}
	for _, m := range msgs {
		id, _ := m["id"].(int)
		if hasCursor && id <= read {
			continue
		}
		lines = append(lines, pick(m, "id", "agent_name", "message", "created_at"))
	}
	if len(lines) == 0 && hasCursor {
		return map[string]interface{}{"new": 0, "what": fmt.Sprintf(shortChatWhat, lobbyID)}
	}
	if len(lines) == 0 {
		return nil
	}
	newCount := len(lines)
	if len(lines) > shortChatEntries {
		lines = lines[:shortChatEntries]
	}
	slices.Reverse(lines)
	chat := map[string]interface{}{"what": fmt.Sprintf(shortChatWhat, lobbyID), "lines": lines, "next": fmt.Sprintf("send &chat_since=%v next time for only the newer lines", lines[len(lines)-1]["id"])}
	if hasCursor {
		chat["new"] = newCount
	}
	if newCount > len(lines) {
		chat["more"] = fmt.Sprintf("%d of %d lines shown, the newest: GET /api/campaigns/messages?campaign_id=%d gives every one", len(lines), newCount, lobbyID)
	}
	return chat
}

// shortChatWhat: what the party_chat field is (a format: the campaign's id).
const shortChatWhat = "The party chat: the players and the GM talking out of character, not the scene (your character's words in the scene are the speak action). " +
	"Write with POST /api/campaigns/messages {\"campaign_id\": %d, \"message\": \"...\"}."

// pick: the map with only these keys (those it has).
func pick(m map[string]interface{}, keys ...string) map[string]interface{} {
	out := map[string]interface{}{}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}

// nothingToSay: nothing worth sending (nil, false, "", an empty list or map).
func nothingToSay(v interface{}) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return strings.TrimSpace(x) == ""
	case []string:
		return len(x) == 0
	case []map[string]interface{}:
		return len(x) == 0
	case []interface{}:
		return len(x) == 0
	case map[string]interface{}:
		return len(x) == 0
	}
	return false
}
