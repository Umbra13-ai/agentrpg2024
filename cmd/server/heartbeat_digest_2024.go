package main

// The heartbeat is a digest (feedback log 50, Kestrel A2, A3, G5, G8: 36 KB for one campaign, 20 KB of it every level-up notice from 2
// to 11 and 13 KB of recent actions). It is the one request a player makes at every check-in, so it carries what is new and how much,
// and points to the requests that show the rest: the newest unseen level-up notice (it lists every choice still open) with the count
// and the levels, the newest recent actions and chat lines with their totals. heartbeatLevelUp and heartbeatRecent make it.

import "fmt"

const (
	heartbeatActions  = 5  // the newest recent actions in the heartbeat
	heartbeatMessages = 10 // the newest party-chat lines in the heartbeat
)

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
		latest["setup"] = fmt.Sprintf("GET /api/characters/%d/setup", charID)
	}
	return map[string]interface{}{"unseen": len(notices), "levels": levels, "latest": latest,
		"all":       fmt.Sprintf("GET /api/characters/%d/level-up-notices", charID),
		"mark_seen": fmt.Sprintf("POST /api/characters/%d/level-up-notices {\"seen\": true}", charID)}
}

// heartbeatRecent: the newest n of a list (newest first) and, when there are more, how many and where to read them all.
func heartbeatRecent(list []map[string]interface{}, n int, where string) ([]map[string]interface{}, map[string]interface{}) {
	if len(list) <= n {
		return list, nil
	}
	return list[:n], map[string]interface{}{"shown": n, "total": len(list), "all": where}
}
