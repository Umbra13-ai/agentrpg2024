package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// The short view is built from a fixed list (shortTurn): a key my-turn gains never reaches it by itself; the events since the character's
// last action are entries {id, at, line}, the newest few or those after ?since, never repeating the newest narration (gm_says); the sheet's
// reference text, the long menus and the feature notes stay in the full view (S-WREN-1).
func TestShortTurnIsBuiltNotTrimmed(t *testing.T) {
	long := strings.Repeat("x", 2000)
	var recs []actionRecord
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= 40; i++ {
		typ := "search"
		if i%2 == 0 {
			typ = actionNarration
		}
		recs = append(recs, actionRecord{ID: 100 + i, Actor: "Wren", Type: typ, Description: fmt.Sprintf("event %d %s", i, long), At: at.Add(time.Duration(i) * time.Minute)})
	}
	full := map[string]interface{}{
		"is_my_turn": true, "message": "Your turn.", "gm_says": recs[39].Description, "gm_says_at": "now", "story_so_far": strings.Repeat("word ", 400) + "the end",
		"now": []map[string]interface{}{{"action": "search"}},
		"character": map[string]interface{}{"name": "Sylvara", "hp": 23, "max_hp": 23, "ac": 12, "class_features": []map[string]interface{}{{"description": long}}, "spell_slots": map[string]interface{}{"remaining": map[string]int{"1": 4}},
			"known_spells": []map[string]interface{}{{"name": "Fire Bolt", "level": 0}, {"name": "Shield", "level": 1}}, "prepared_spells": []map[string]interface{}{{"name": "Shield", "level": 1}}},
		"your_options":  map[string]interface{}{"action_economy": map[string]interface{}{"action": true, "bonus_action": false, "reaction": true, "movement_remaining_ft": 30}, "actions": long},
		"recent_events": []string{long}, "since_your_last_action": []string{long}, "dwarven_resilience": map[string]interface{}{"tip": long},
		"elsewhere": []string{long}, "more": []string{long}, "how_to_act": long, "a_key_added_tomorrow": long, "table_held": false,
		"party_status":       []map[string]interface{}{{"character_id": 2, "name": "Calla", "hp": 20, "max_hp": 24, "status": "wounded", "species": "Halfling", "ac": 16}},
		"character_finished": true, "setup_guide": "x",
	}
	full["character"].(map[string]interface{})["currency"] = map[string]interface{}{"cp": 0, "sp": 3, "ep": 0, "gp": 1, "pp": 0, "total_in_gp": 1.3}
	var chat []map[string]interface{} // newest first, as getRecentCampaignMessages gives them
	for i := 8; i >= 1; i-- {
		chat = append(chat, map[string]interface{}{"id": 500 + i, "agent_id": 3, "agent_name": "Calla", "message": fmt.Sprintf("line %d", i), "created_at": "t"})
	}
	full["campaign_messages"] = chat
	out := shortTurn(full, 5, 9, recs, url.Values{})
	allowed := append(append([]string{}, shortLead...), "view", "full_view", "setup_guide", "how_to_act", "rules_reminder")
	for k := range out {
		if !slices.Contains(allowed, k) {
			t.Errorf("the short view carries %q, which nothing placed there", k)
		}
	}
	for _, k := range []string{"a_key_added_tomorrow", "dwarven_resilience", "elsewhere", "more", "your_options", "recent_events", "since_your_last_action", "table_held", "setup_guide", "how_to_act"} {
		if _, has := out[k]; has {
			t.Errorf("%s reached the short view", k)
		}
	}
	scene := out["scene"].(map[string]interface{})
	ev := scene["since_your_last_action"].([]map[string]interface{})
	if len(ev) < 5 || ev[len(ev)-1]["id"] != 140 || scene["since_your_last_action_note"] == nil || !strings.HasSuffix(fmt.Sprint(scene["story_so_far"]), "the end") {
		t.Errorf("the scene: %d events, the last %v", len(ev), ev[len(ev)-1]["id"])
	}
	if scene["gm_says"] != nil {
		t.Error("gm_says repeats the newest narration the list holds")
	}
	// a short history: every event since the last action, no note
	few := shortTurn(full, 5, 9, recs[36:], url.Values{})["scene"].(map[string]interface{})
	if l := few["since_your_last_action"].([]map[string]interface{}); len(l) != 4 || few["since_your_last_action_note"] != nil {
		t.Errorf("four events since the last action: %v", few)
	}
	you := out["you"].(map[string]interface{})
	if you["hp"] != 23 || you["carrying"] != nil || you["class_features"] != nil || you["this_turn"] == nil { // spells_ready is the spell owner's (spellsReady): TestPostgresShortViewSpellsReady
		t.Errorf("you: %v", you)
	}
	if p := out["party"].([]map[string]interface{}); p[0]["species"] != nil || p[0]["hp"] != 20 {
		t.Errorf("party: %v", p)
	}
	b, _ := json.Marshal(out)
	if len(b) > shortEventBytes+6000 {
		t.Errorf("the short view is %d bytes with 2 KB events", len(b))
	}
	// ?since: only the newer events
	after := shortTurn(full, 5, 9, recs, url.Values{"since": {"135"}})
	ev = after["scene"].(map[string]interface{})["since_your_last_action"].([]map[string]interface{})
	if len(ev) != 5 || ev[0]["id"] != 136 {
		t.Errorf("since 135: %v", ev)
	}
	// money with its units and who changes it
	if m, _ := you["money"].(map[string]interface{}); m["sp"] != 3 || m["gp"] != 1 || !strings.Contains(fmt.Sprint(m["note"]), "GM adjusts money and items") || you["gold"] != nil {
		t.Errorf("money: %v", you["money"])
	}
	// the party chat: labelled, the newest lines oldest first, a cursor for only the new ones
	pc := out["party_chat"].(map[string]interface{})
	lines := pc["lines"].([]map[string]interface{})
	if !strings.Contains(fmt.Sprint(pc["what"]), "party chat") || len(lines) != shortChatEntries || lines[len(lines)-1]["id"] != 508 || lines[0]["id"] != 504 || pc["more"] == nil {
		t.Errorf("party chat: %v", pc)
	}
	pc = shortTurn(full, 5, 9, recs, url.Values{"chat_since": {"506"}})["party_chat"].(map[string]interface{})
	if pc["new"] != 2 || len(pc["lines"].([]map[string]interface{})) != 2 || pc["more"] != nil {
		t.Errorf("chat_since 506: %v", pc)
	}
	if pc = shortTurn(full, 5, 9, recs, url.Values{"chat_since": {"508"}})["party_chat"].(map[string]interface{}); pc["new"] != 0 || pc["lines"] != nil {
		t.Errorf("nothing new: %v", pc)
	}
	// tips=1 brings the standing tips back
	if tips := shortTurn(full, 5, 9, recs, url.Values{"tips": {"1"}}); tips["how_to_act"] == nil {
		t.Error("tips=1")
	}
}
