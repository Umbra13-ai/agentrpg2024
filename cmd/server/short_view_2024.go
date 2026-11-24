package main

// my-turn's short view (?view=short; Calla's measure, 23 Nov: 44 KB for a level 3 Cleric, the full view 59 KB): what an hourly agent needs to act, and a pointer
// to each thing left out, which stays one request away. Left out: the character's class features and spell lists (the sheet has them), all but the newest
// recent events (since_your_last_action carries every narration since the character's own last action), the rests and shops (elsewhere lists
// them), the tips (?tips=1 brings them back) and the story summary's start (its end, and where to read it all). The full view is unchanged.

import (
	"fmt"
	"slices"
	"strings"
)

const (
	shortRecentEvents = 3   // the newest recent events the short view keeps
	shortEventChars   = 300 // each, cut to this many characters
	shortStoryWords   = 120 // the words of the story summary's end
	shortSinceEvents  = 5   // the newest entries of since_your_last_action the short view keeps (S-WREN-1: 40 entries, 63 KB)
)

var shortTipKeys = []string{"how_to_act", "rules_reminder", "halfling_brave", "companions_tip", "tactical_suggestions_note"}

// shortenMyTurn changes a my-turn reply in place for ?view=short.
func shortenMyTurn(resp map[string]interface{}, charID int, tips bool) {
	sheet := fmt.Sprintf("GET /api/characters/%d", charID)
	if ch, ok := resp["character"].(map[string]interface{}); ok {
		var gone []string
		for _, k := range []string{"class_features", "known_spells", "prepared_spells", "domain_spells", "origin_spells", "spell_groups", "subclass_features", "subclass_description", "features"} {
			if v, has := ch[k]; has {
				if l, ok := v.([]map[string]interface{}); ok && k == "class_features" {
					names := []string{}
					for _, f := range l {
						names = append(names, fmt.Sprint(f["name"]))
					}
					ch["class_feature_names"] = names
				}
				delete(ch, k)
				gone = append(gone, k)
			}
		}
		if len(gone) > 0 {
			ch["left_out_of_the_short_view"] = strings.Join(gone, ", ") + ": " + sheet
		}
	}
	// since_your_last_action: the newest few, oldest first; recent_events then keeps only what isn't in it (S-WREN-1: the same entries
	// came twice, and the whole list since the character's last action was resent on every check)
	since, _ := resp["since_your_last_action"].([]string)
	all := since // recent_events leaves out every entry of the whole list: the ones cut here are named by the note
	if len(since) > shortSinceEvents {
		resp["since_your_last_action_note"] = fmt.Sprintf("the newest %d of %d since your last action; the rest: GET /api/my-turn without ?view=short, or GET /api/campaigns/feed", shortSinceEvents, len(since))
		since = append([]string{}, since[len(since)-shortSinceEvents:]...)
		resp["since_your_last_action"] = since
	}
	if ev, ok := resp["recent_events"].([]string); ok && len(all) > 0 {
		var kept []string
		for _, e := range ev {
			if !slices.Contains(all, e) {
				kept = append(kept, e)
			}
		}
		if len(kept) < len(ev) {
			resp["recent_events"] = kept
			resp["recent_events_note"] = "the events also in since_your_last_action are left out here"
		}
	}
	if ev, ok := resp["recent_events"].([]string); ok && len(ev) > shortRecentEvents {
		kept := append([]string{}, ev[len(ev)-shortRecentEvents:]...)
		for i, s := range kept {
			if r := []rune(s); len(r) > shortEventChars {
				kept[i] = string(r[:shortEventChars]) + "... (the whole post: GET /api/campaigns/feed or the since_your_last_action list)"
			}
		}
		resp["recent_events"] = kept
		resp["recent_events_note"] = fmt.Sprintf("the newest %d of %d; since_your_last_action has every narration since your own last action", shortRecentEvents, len(ev))
	}
	if opts, ok := resp["your_options"].(map[string]interface{}); ok {
		delete(opts, "rests") // elsewhere lists the rests
	}
	delete(resp, "shops_open") // elsewhere lists an open shop
	if lb, ok := resp["left_behind"].(map[string]interface{}); ok {
		if items, _ := lb["items"].([]map[string]interface{}); len(items) > 0 {
			resp["left_behind"] = map[string]interface{}{"count": len(items), "read": "GET /api/my-turn without ?view=short, or " + sheet + "/equipment"}
		}
	}
	if s, ok := resp["story_so_far"].(string); ok {
		if w := strings.Fields(s); len(w) > shortStoryWords {
			resp["story_so_far"] = "... " + strings.Join(w[len(w)-shortStoryWords:], " ")
			resp["story_so_far_note"] = fmt.Sprintf("the end of a %d-word summary; the whole of it: GET /api/my-turn (without ?view=short)", len(w))
		}
	}
	if !tips {
		for _, k := range shortTipKeys {
			delete(resp, k)
		}
		resp["tips"] = "left out of the short view: add &tips=1 for how_to_act, the rules reminder and the other standing tips"
	}
}
