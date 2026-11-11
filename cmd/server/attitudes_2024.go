package main

// Attitudes (SRD 5.2.1 Rules Glossary): a monster or NPC is Friendly, Indifferent (the default) or Hostile toward the characters.
// Friendly gives Advantage on an ability check to influence it, Hostile gives Disadvantage. The GM records attitudes here (for a
// combatant by its id, for any NPC by name); the Influence action then uses the right one, and attitude shifts persist.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func ensureAttitudeTable() {
	db.Exec(`CREATE TABLE IF NOT EXISTS attitudes (lobby_id INTEGER NOT NULL, target_key TEXT NOT NULL, name TEXT NOT NULL, attitude TEXT NOT NULL, PRIMARY KEY (lobby_id, target_key))`)
}

func attitudeKey(targetID int, npc string) string {
	if targetID != 0 {
		return fmt.Sprintf("id:%d", targetID)
	}
	return "npc:" + strings.ToLower(strings.TrimSpace(npc))
}

// attitudeFor finds the attitude of the creature an Influence action is aimed at: the combatant in target_id, or an NPC whose
// recorded name appears in the description. ok is false when none is recorded.
func attitudeFor(lobbyID, targetID int, description string) (attitude, name string, ok bool) {
	if db == nil || lobbyID == 0 {
		return "", "", false
	}
	ensureAttitudeTable()
	if targetID != 0 {
		if db.QueryRow(`SELECT attitude, name FROM attitudes WHERE lobby_id = $1 AND target_key = $2`, lobbyID, attitudeKey(targetID, "")).Scan(&attitude, &name) == nil {
			return attitude, name, true
		}
	}
	rows, err := db.Query(`SELECT attitude, name FROM attitudes WHERE lobby_id = $1 AND target_key LIKE 'npc:%'`, lobbyID)
	if err != nil {
		return "", "", false
	}
	defer rows.Close()
	d := strings.ToLower(description)
	best := ""
	for rows.Next() {
		var a, n string
		if rows.Scan(&a, &n) == nil && n != "" && strings.Contains(d, strings.ToLower(n)) && len(n) > len(best) {
			attitude, name, best = a, n, n
		}
	}
	return attitude, name, best != ""
}

// @Summary NPC and monster attitudes
// @Description A creature's attitude toward the characters: friendly, indifferent (the default) or hostile. Friendly gives Advantage on Influence checks against it, Hostile Disadvantage. POST {"target_id": -2, "attitude": "friendly"} for a combatant, or {"npc": "Ismark", "attitude": "hostile"} for any named NPC (the Influence action finds the name in the player's description); attitude "clear" forgets it. GET ?campaign_id lists them. Players see the effect in their Influence result, not this list.
// @Tags GM
// @Param request body object{campaign_id=int,target_id=int,npc=string,attitude=string} true "Creature and attitude"
// @Router /gm/attitude [post]
func handleGMAttitude(w http.ResponseWriter, r *http.Request) {
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	ensureAttitudeTable()
	if r.Method == http.MethodGet {
		var list []map[string]interface{}
		rows, err := db.Query(`SELECT target_key, name, attitude FROM attitudes WHERE lobby_id = $1`, lobby)
		if err == nil {
			for rows.Next() {
				var k, n, a string
				if rows.Scan(&k, &n, &a) == nil {
					list = append(list, map[string]interface{}{"key": k, "name": n, "attitude": a})
				}
			}
			rows.Close()
		}
		sort.Slice(list, func(i, j int) bool { return fmt.Sprint(list[i]["name"]) < fmt.Sprint(list[j]["name"]) })
		writeJSONStatus(w, 200, map[string]interface{}{"attitudes": list})
		return
	}
	var req struct {
		TargetID int    `json:"target_id"`
		NPC      string `json:"npc"`
		Attitude string `json:"attitude"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	att := strings.ToLower(strings.TrimSpace(req.Attitude))
	if (req.TargetID == 0 && strings.TrimSpace(req.NPC) == "") || (att != "friendly" && att != "indifferent" && att != "hostile" && att != "clear") {
		dmBadRequest(w, "target_and_attitude_required", "Send target_id (a combatant) or npc (a name), and attitude: friendly, indifferent, hostile or clear.")
		return
	}
	name := strings.TrimSpace(req.NPC)
	if req.TargetID != 0 {
		name = creatureName(lobby, req.TargetID)
	}
	key := attitudeKey(req.TargetID, req.NPC)
	var result string
	if att == "clear" {
		db.Exec(`DELETE FROM attitudes WHERE lobby_id = $1 AND target_key = $2`, lobby, key)
		result = fmt.Sprintf("%s's attitude is no longer recorded (Indifferent by default).", name)
	} else {
		db.Exec(`INSERT INTO attitudes (lobby_id, target_key, name, attitude) VALUES ($1,$2,$3,$4)
			ON CONFLICT (lobby_id, target_key) DO UPDATE SET attitude = EXCLUDED.attitude, name = EXCLUDED.name`, lobby, key, name, att)
		result = fmt.Sprintf("%s is %s toward the party.", name, titleCase(att))
	}
	// not written to the campaign feed: players learn an attitude from how the creature acts and from their Influence result
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}
