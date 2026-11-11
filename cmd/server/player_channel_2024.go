package main

// Channel Divinity a player uses on their own turn (LOW-LEVEL-PLAN phase D, Nov 10). Turn Undead (Cleric 2), Preserve Life (Life
// Domain 3) and Sacred Weapon (Devotion 3) existed only as GM commands, so an AI Cleric or Paladin couldn't use its level 2-3
// features. The actions run the same rules (the GM handlers, acting for the campaign's GM):
//	{"action": "turn_undead", "target_ids": [-1, -2]}                  Magic action: each Undead makes a Wisdom save or is turned
//	{"action": "preserve_life", "description": "Aldren 6, Millie 4"}  Magic action: share 5 x Cleric level Hit Points (or "target_ids": [4, 2]: split evenly)
//	{"action": "sacred_weapon"}                                       part of the Attack action in the SRD; here a free action before attacking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// featureTargets: the target_ids of the action being resolved (character id -> ids).
var featureTargets sync.Map

func runAsGM(charID int, path string, body map[string]interface{}, h func(http.ResponseWriter, *http.Request)) map[string]interface{} {
	lobby := lobbyOfChar(charID)
	var dm int
	db.QueryRow(`SELECT COALESCE(dm_id,0) FROM lobbies WHERE id = $1`, lobby).Scan(&dm)
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("%s?campaign_id=%d", path, lobby), bytes.NewReader(raw))
	r = r.WithContext(context.WithValue(r.Context(), internalAgentKey{}, dm))
	rec := httptest.NewRecorder()
	h(rec, r)
	out := map[string]interface{}{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

var namedAmount = regexp.MustCompile(`([A-Za-z][A-Za-z' -]*?)\s+(\d+)`)

func playerChannelDivinity(charID int, action, description string) string {
	var ids []int
	if v, ok := featureTargets.Load(charID); ok {
		ids = v.([]int)
	}
	switch action {
	case "turn_undead":
		var undead []int
		for _, id := range ids {
			if id < 0 {
				undead = append(undead, id)
			}
		}
		if len(undead) == 0 {
			return "Turn Undead: name the Undead within 30 feet with target_ids (their negative ids)."
		}
		res := runAsGM(charID, "/api/gm/turn-undead", map[string]interface{}{"caster_id": charID, "target_ids": undead}, handleGMTurnUndead)
		if e, bad := res["error"]; bad {
			return fmt.Sprintf("Turn Undead: %v", firstNonNil(res["message"], e))
		}
		var parts []string
		lobby := lobbyOfChar(charID)
		for _, x := range asList(res["results"]) {
			m, _ := x.(map[string]interface{})
			name := creatureName(lobby, asInt(m["target_id"]))
			switch {
			case m["note"] != nil:
				parts = append(parts, fmt.Sprintf("%s: %v", name, m["note"]))
			case m["turned"] == true:
				parts = append(parts, fmt.Sprintf("%s fails (%v vs DC %v): turned, Frightened and Incapacitated for 1 minute (ends if it takes damage)", name, m["total"], m["dc"]))
			default:
				parts = append(parts, fmt.Sprintf("%s saves (%v vs DC %v)", name, m["total"], m["dc"]))
			}
		}
		return "Channel Divinity: Turn Undead (Wisdom save). " + strings.Join(parts, "; ") + "."
	case "preserve_life":
		var healing []map[string]interface{}
		lobby := lobbyOfChar(charID)
		for _, m := range namedAmount.FindAllStringSubmatch(description, -1) {
			name := strings.TrimSpace(m[1])
			n, _ := strconv.Atoi(m[2])
			var id int
			db.QueryRow(`SELECT id FROM characters WHERE lobby_id = $1 AND (LOWER(name) = LOWER($2) OR LOWER(name) LIKE LOWER($2)||' %' OR LOWER(name) LIKE '% '||LOWER($2) OR LOWER(name) LIKE '% '||LOWER($2)||' %') ORDER BY id LIMIT 1`, lobby, name).Scan(&id) // full, first, last or any word of the name
			if id > 0 && n > 0 {
				healing = append(healing, map[string]interface{}{"target_id": id, "amount": n})
			}
		}
		if len(healing) == 0 && len(ids) > 0 {
			var level int
			db.QueryRow(`SELECT COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&level)
			each := 5 * level / len(ids)
			for _, id := range ids {
				if id > 0 {
					healing = append(healing, map[string]interface{}{"target_id": id, "amount": each})
				}
			}
		}
		if len(healing) == 0 {
			return "Preserve Life: say who gets how many Hit Points (\"Aldren 6, Millie 4\"; 5 x your Cleric level in all) or send target_ids to split it evenly."
		}
		res := runAsGM(charID, "/api/gm/preserve-life", map[string]interface{}{"caster_id": charID, "healing": healing}, handleGMPreserveLife)
		if e, bad := res["error"]; bad {
			return fmt.Sprintf("Preserve Life: %v", firstNonNil(res["message"], e))
		}
		return fmt.Sprintf("Channel Divinity: Preserve Life. %v", firstNonNil(res["message"], res["result"], "Hit Points restored."))
	case "sacred_weapon":
		res := runAsGM(charID, "/api/gm/sacred-weapon", map[string]interface{}{"paladin_id": charID}, handleGMSacredWeapon)
		if e, bad := res["error"]; bad {
			return fmt.Sprintf("Sacred Weapon: %v", firstNonNil(res["message"], e))
		}
		return fmt.Sprintf("Channel Divinity: Sacred Weapon. %v", firstNonNil(res["message"], res["result"], "Your weapon glows: add your Charisma modifier to attack rolls for 1 minute."))
	}
	return ""
}

func firstNonNil(v ...interface{}) interface{} {
	for _, x := range v {
		if x != nil && fmt.Sprint(x) != "" {
			return x
		}
	}
	return ""
}

func asList(v interface{}) []interface{} {
	l, _ := v.([]interface{})
	return l
}
