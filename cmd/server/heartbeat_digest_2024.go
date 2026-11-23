package main

// The heartbeat is a digest (feedback log 50, Kestrel A2, A3, G5, G8: 36 KB for one campaign, 20 KB of it every level-up notice from 2
// to 11 and 13 KB of recent actions). It is the one request a player makes at every check-in, so it carries what is new and how much,
// and points to the requests that show the rest: the newest unseen level-up notice (it lists every choice still open) with the count
// and the levels, the newest recent actions and chat lines with their totals. heartbeatLevelUp and heartbeatRecent make it.

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	heartbeatActions  = 5   // the newest recent actions in the heartbeat
	heartbeatMessages = 10  // the newest party-chat lines in the heartbeat
	heartbeatStory    = 120 // the words of story_so_far's end that the heartbeat carries (the summary itself may hold 500)
)

// heartbeatDocument: what the player heartbeat carries of the campaign document (plan v2.1, S4; playtest S-GM-19, I-KESTREL-5). The
// document's other fields (a chapter or a round written long ago, npcs, sections) went stale and made a newly assigned player read an old
// scene, so the heartbeat carries only story_so_far, the one summary my-turn reads (parseStorySoFar: same source, never a copy), the end of
// it (the current scene, where the summary leads), when it was written, and where the whole summary and the whole document are. A
// summary longer than heartbeatStory words is cut from the front, and says so.
func heartbeatDocument(doc map[string]interface{}, campaignID int) map[string]interface{} {
	raw, _ := json.Marshal(doc)
	story := strings.TrimSpace(parseStorySoFar(raw))
	updated, _ := doc["story_so_far_updated_at"].(string)
	if updated == "" {
		updated = "unknown (the GM has not updated the summary since it was made)"
	}
	words := strings.Fields(story)
	end, cut := story, false
	if len(words) > heartbeatStory {
		end, cut = "... "+strings.Join(words[len(words)-heartbeatStory:], " "), true
	}
	out := map[string]interface{}{
		"story_so_far":            end,
		"story_so_far_cut":        cut,
		"story_so_far_words":      len(words),
		"story_so_far_updated_at": updated,
		"current_scene":           map[string]interface{}{"as_of": updated, "ends_with": lastSentence(story, 240)},
		"read_the_whole_summary":  "GET /api/my-turn (story_so_far)",
		"read_the_whole_document": fmt.Sprintf("GET /api/campaigns/%d/campaign", campaignID),
	}
	return out
}

// lastSentence: the final sentence of a text, at most n characters (the end of it when longer).
func lastSentence(text string, n int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	body := strings.TrimRight(text, ".!? \n")
	if i := strings.LastIndexAny(body, ".!?\n"); i >= 0 {
		body = strings.TrimSpace(body[i+1:])
	}
	if r := []rune(body); len(r) > n {
		return "..." + string(r[len(r)-n:])
	}
	return body
}

// heartbeatLevelUp: the unseen level-up notices as a digest (nil when there are none).
func heartbeatLevelUp(charID int) map[string]interface{} {
	notices := unseenLevelUpNotices(charID)
	if len(notices) == 0 {
		return nil
	}
	var levels []interface{}
	for _, n := range notices {
		levels = append(levels, n["level"])
	}
	last := notices[len(notices)-1] // the newest notice in short: its words and the choices still open (the full notice: all)
	latest := map[string]interface{}{"level": last["level"], "message": last["message"], "when": last["when"]}
	if open := characterToFinish(charID); len(open) > 0 {
		latest["choices_open"] = open
		latest["open_choices"] = openChoiceRequests(charID) // each with its exact request and body (Calla needed them at level 3)
		latest["setup"] = fmt.Sprintf("GET /api/characters/%d/setup", charID)
	}
	return map[string]interface{}{"unseen": len(notices), "levels": levels, "latest": latest,
		"all":       fmt.Sprintf("GET /api/characters/%d/level-up-notices", charID),
		"mark_seen": fmt.Sprintf("POST /api/characters/%d/level-up-notices {\"seen\": true}", charID)}
}

// openChoiceRequests: every choice the character still has to make, with its exact request and body as its setup step gives it (a step
// that waits for another choice names it and has no request yet). The level-up digest (the heartbeat, my-turn's short view) carries
// them so collapsing the notices never costs a player the request to send.
func openChoiceRequests(charID int) []map[string]interface{} {
	c, ok := loadChoiceChar(charID)
	if !ok {
		return nil
	}
	var out []map[string]interface{}
	for _, st := range choiceSteps(c) {
		if st.Done {
			continue
		}
		e := map[string]interface{}{"key": st.Key, "title": st.Title}
		if st.Request != nil {
			e["request"] = st.Request
		}
		if st.WaitsFor != "" {
			e["waits_for"] = st.WaitsFor
		}
		out = append(out, e)
	}
	return out
}

// heartbeatRecent: the newest n of a list (newest first) and, when there are more, how many and where to read them all.
func heartbeatRecent(list []map[string]interface{}, n int, where string) ([]map[string]interface{}, map[string]interface{}) {
	if len(list) <= n {
		return list, nil
	}
	return list[:n], map[string]interface{}{"shown": n, "total": len(list), "all": where}
}
