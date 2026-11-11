package main

// Paladin, 2024 rules (SRD 5.2.1).
//	Paladin's Smite (level 2)   "smite free" (or "paladin's smite") in the attack description casts Divine Smite at level 1
//	                            without a spell slot, once per Long Rest.
//	Radiant Strikes (level 11)  +1d8 Radiant damage when you hit with a Melee weapon or an Unarmed Strike.
//	Lay On Hands                a Bonus Action; removing Poisoned costs 5 points of the pool; from level 14 (Restoring Touch) each of
//	                            Blinded, Charmed, Deafened, Frightened, Paralyzed or Stunned also costs 5 points.
//	Abjure Foes (level 9)       POST /api/gm/abjure-foes: Channel Divinity; up to Charisma-modifier targets within 60 feet make a
//	                            Wisdom saving throw or are Frightened for 1 minute or until they take damage.
//	Channel Divinity            2 uses (3 from level 11); a Short Rest returns one use.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func wantsFreeSmite(description string) bool {
	low := strings.ToLower(description)
	return strings.Contains(low, "smite free") || strings.Contains(low, "free smite") || strings.Contains(low, "paladin's smite") || strings.Contains(low, "without a slot") || strings.Contains(low, "no slot")
}

// freeSmiteAvailable says whether the Paladin still has the once-per-Long-Rest free Divine Smite.
func freeSmiteAvailable(charID int) (bool, string) {
	var class string
	var level int
	var usedJSON []byte
	if db.QueryRow(`SELECT class, level, COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &usedJSON) != nil ||
		strings.ToLower(class) != "paladin" || level < 2 {
		return false, "Paladin's Smite needs a Paladin of level 2 or higher"
	}
	used := map[string]int{}
	json.Unmarshal(usedJSON, &used)
	if used["paladins_smite"] >= 1 {
		return false, "You already cast Divine Smite without a spell slot; finish a Long Rest to do it again"
	}
	return true, ""
}

func useFreeSmite(charID int) {
	var usedJSON []byte
	db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&usedJSON)
	used := map[string]int{}
	json.Unmarshal(usedJSON, &used)
	used["paladins_smite"]++
	b, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, string(b), charID)
}

// radiantStrikes is the extra 1d8 of a Paladin of level 11+ on a melee hit (dice doubled on a critical hit).
func radiantStrikes(charID int, class string, level int, melee, crit bool) (int, string) {
	if strings.ToLower(class) != "paladin" || level < 11 || !melee {
		return 0, ""
	}
	n := game.RollDamage("1d8", crit)
	addRider(charID, "radiant", n)
	return n, fmt.Sprintf(" [Radiant Strikes +%d radiant]", n)
}

// handleGMAbjureFoes: POST /api/gm/abjure-foes {"paladin_id": N, "target_ids": [...]}.
// @Summary Paladin Abjure Foes (Channel Divinity, level 9)
// @Description Creatures of the Paladin's choice within 60 feet make a Wisdom saving throw; on a failure they are Frightened for 1 minute or until they take damage.
// @Tags GM
// @Param request body object{paladin_id=int,target_ids=[]int} true "Request"
// @Router /gm/abjure-foes [post]
func handleGMAbjureFoes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		PaladinID int   `json:"paladin_id"`
		TargetIDs []int `json:"target_ids"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil || req.PaladinID == 0 || len(req.TargetIDs) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send paladin_id and target_ids."})
		return
	}
	var name, class string
	var level, cha, lobby int
	if db.QueryRow(`SELECT name, class, level, cha, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.PaladinID).Scan(&name, &class, &level, &cha, &lobby) != nil || lobby != campaignID {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if strings.ToLower(class) != "paladin" || level < 9 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_abjure_foes", "message": name + " is not a Paladin of level 9 or higher."})
		return
	}
	maxTargets := game.Modifier(cha)
	if maxTargets < 1 {
		maxTargets = 1
	}
	if len(req.TargetIDs) > maxTargets {
		req.TargetIDs = req.TargetIDs[:maxTargets]
	}
	if okRes, msg, _ := useClassResource(req.PaladinID, "channel_divinity", 1); !okRes {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_channel_divinity", "message": msg})
		return
	}
	dc := 8 + game.ProficiencyBonus(level) + game.Modifier(cha)
	var results []map[string]interface{}
	for _, tid := range req.TargetIDs {
		saved, total := monsterSave(campaignID, tid, "wis", dc)
		res := map[string]interface{}{"target_id": tid, "total": total, "dc": dc, "saved": saved}
		if !saved {
			addMonsterCondition(campaignID, tid, "frightened", "Abjure Foes ("+name+")", 10, "", 0)
			res["frightened"] = true
			res["note"] = "Frightened for 1 minute or until it takes any damage; on its turns it can do only one of: move, take an action, or take a Bonus Action."
		}
		results = append(results, res)
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "paladin": name, "save": "Wisdom", "dc": dc, "results": results})
}
