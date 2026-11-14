package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Advancement (Matt, Nov 14): each campaign chooses how characters gain levels, with the table rule "advancement".
//
//	xp         Experience Points (the default). When a fight ends, the XP of every defeated monster is added up, divided evenly
//	           among the characters who fought, and awarded: the GM no longer has to do it by hand.
//	milestone  No Experience Points. The GM grants a level when the party reaches a milestone (POST /api/gm/milestone), and
//	           nothing about XP is asked for or shown at the end of a fight.

func init() {
	tableRuleDefs = append(tableRuleDefs, tableRuleDef{Key: "advancement", Title: "Level advancement", Default: "xp", Options: []tableRuleOption{
		{"xp", "Experience Points", "Characters gain levels with Experience Points. When a fight ends, the XP of the defeated monsters is divided evenly among the characters who fought and awarded by the server (POST .../combat/end {\"award_xp\": false} leaves it to the GM). The GM can award more with POST /api/gm/award-xp."},
		{"milestone", "Milestones", "No Experience Points. The GM grants a level when the party reaches a milestone in the story: POST /api/gm/milestone {\"campaign_id\": N}. Nothing is awarded at the end of a fight."},
	}})
}

// milestoneCampaign: the campaign levels by milestones, not Experience Points.
func milestoneCampaign(lobbyID int) bool {
	return lobbyID > 0 && db != nil && tableRule(lobbyID, "advancement") == "milestone"
}

// advancementFields puts how the character gains levels on a sheet or a my-turn reply: with milestones the XP numbers say
// nothing, so they are left out.
func advancementFields(m map[string]interface{}, lobbyID int) {
	if m == nil {
		return
	}
	if milestoneCampaign(lobbyID) {
		delete(m, "xp_to_next_level")
		delete(m, "xp_threshold")
		m["advancement"] = "milestone"
		m["advancement_note"] = "This campaign uses milestones: you gain a level when the GM says the party has reached one. Experience Points are not used."
		return
	}
	m["advancement"] = "xp"
}

// fightXPPlan reads the fight as it stands (before it is cleared): the monsters at 0 Hit Points with the XP of their stat blocks,
// and the characters who share it.
type fightXPPlan struct {
	Defeated []map[string]interface{}
	NoValue  []string // defeated, but with no stat block to take an XP value from (a creature the GM made up)
	Total    int
	Fighters []int
}

func planFightXP(lobbyID int) fightXPPlan {
	var p fightXPPlan
	for _, e := range combatEntries(lobbyID) {
		id := asInt(e["id"])
		if mon, _ := e["is_monster"].(bool); !mon {
			if id > 0 {
				p.Fighters = append(p.Fighters, id)
			}
			continue
		}
		if asInt(e["hp"]) > 0 {
			continue
		}
		name := fmt.Sprint(e["name"])
		xp := 0
		if meta := monsterMetaFor(sprintAny(e["monster_key"])); meta != nil {
			xp = meta.XP
			if lair, _ := e["in_lair"].(bool); lair && meta.XPLair > 0 {
				xp = meta.XPLair
			}
		}
		if xp <= 0 {
			p.NoValue = append(p.NoValue, name)
			continue
		}
		p.Defeated = append(p.Defeated, map[string]interface{}{"name": name, "xp": xp})
		p.Total += xp
	}
	sort.Ints(p.Fighters)
	return p
}

// endCombatWithAdvancement wraps POST /api/campaigns/{id}/combat/end: after the fight has ended, an XP campaign gets the fight's
// XP awarded, and a milestone campaign is told that nothing is needed.
func endCombatWithAdvancement(w http.ResponseWriter, r *http.Request, campaignID int, more func(map[string]interface{})) {
	var body struct {
		AwardXP *bool `json:"award_xp"`
	}
	if r.Body != nil {
		raw, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 32*1024))
		json.Unmarshal(raw, &body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	plan := planFightXP(campaignID) // read before the turn order is cleared
	augmentJSON(w, func(rw http.ResponseWriter) { handleCombatEnd(rw, r, campaignID) }, func(m map[string]interface{}) {
		if more != nil {
			more(m)
		}
		if milestoneCampaign(campaignID) {
			m["advancement"] = map[string]interface{}{"mode": "milestone",
				"note": fmt.Sprintf("This campaign uses milestones: no Experience Points for the fight. When the party reaches a milestone, grant a level with POST /api/gm/milestone {\"campaign_id\": %d}.", campaignID)}
			return
		}
		xp := map[string]interface{}{"mode": "xp", "defeated": plan.Defeated, "total": plan.Total, "shared_by": len(plan.Fighters)}
		if len(plan.NoValue) > 0 {
			xp["no_xp_value"] = plan.NoValue
			xp["no_xp_value_note"] = "These have no stat block to take an XP value from: award what they are worth with POST /api/gm/award-xp."
		}
		m["xp"] = xp
		each := 0
		if len(plan.Fighters) > 0 {
			each = plan.Total / len(plan.Fighters) // divided evenly, rounded down
		}
		xp["each"] = each
		switch {
		case plan.Total == 0 || each == 0:
			xp["note"] = "No monster with an XP value was defeated, so nothing was awarded. Award XP for the encounter yourself if it earned some: POST /api/gm/award-xp."
		case body.AwardXP != nil && !*body.AwardXP:
			xp["awarded"] = false
			xp["note"] = fmt.Sprintf("Not awarded (award_xp was false). To award it: POST /api/gm/award-xp {\"character_ids\": %s, \"xp\": %d}.", intsJSON(plan.Fighters), each)
		default:
			var dm int
			db.QueryRow(`SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1`, campaignID).Scan(&dm)
			var names []string
			for _, d := range plan.Defeated {
				names = append(names, fmt.Sprint(d["name"]))
			}
			req, rec := ptInternal(http.MethodPost, "/api/gm/award-xp", map[string]interface{}{"character_ids": plan.Fighters, "xp": each,
				"reason": fmt.Sprintf("XP for the fight (%s): %d XP shared by %d", strings.Join(names, ", "), plan.Total, len(plan.Fighters))}, dm)
			handleGMAwardXP(rec, req)
			var out map[string]interface{}
			json.Unmarshal(rec.Body.Bytes(), &out)
			if rec.Code != http.StatusOK || out["success"] != true {
				xp["awarded"] = false
				xp["note"] = fmt.Sprintf("The award did not go through (%v). Award it yourself: POST /api/gm/award-xp {\"character_ids\": %s, \"xp\": %d}.", out["message"], intsJSON(plan.Fighters), each)
				return
			}
			xp["awarded"] = true
			xp["awards"] = out["awards"]
			if out["level_ups"] != nil {
				xp["level_ups"] = out["level_ups"]
			}
			xp["note"] = fmt.Sprintf("%d XP for the defeated monsters, divided evenly: %d XP to each of the %d characters who fought. Award more for the story with POST /api/gm/award-xp.", plan.Total, each, len(plan.Fighters))
		}
	})
}

func intsJSON(ids []int) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

// handleGMMilestone godoc
// @Summary Grant a level at a milestone (GM only)
// @Description In a campaign whose table rule "advancement" is "milestone", the party gains a level when the GM says it has reached a milestone. Every character of the campaign gains one level (or only those in character_ids); Hit Points, features and the choices to make follow as for any level-up (GET /api/characters/{id}/setup lists them). A campaign that uses Experience Points answers 409: set the rule with POST /api/campaigns/{id}/table-rules {"advancement": "milestone"}.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param request body object{campaign_id=integer,character_ids=[]integer,reason=string} true "campaign_id; character_ids (optional: the whole party by default); reason (optional, shown in the feed)"
// @Success 200 {object} map[string]interface{} "The levels gained"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 409 {object} map[string]interface{} "The campaign uses Experience Points"
// @Router /gm/milestone [post]
func handleGMMilestone(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "POST."})
		return
	}
	var req struct {
		CampaignID   int    `json:"campaign_id"`
		CharacterIDs []int  `json:"character_ids"`
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": "Send {\"campaign_id\": N} (and optionally character_ids and a reason). Nothing was changed."})
		return
	}
	if req.CampaignID == 0 { // the GM's only campaign
		var n int
		db.QueryRow(`SELECT count(*), COALESCE(max(id), 0) FROM lobbies WHERE dm_id = $1 AND status = 'active'`, agentID).Scan(&n, &req.CampaignID)
		if n != 1 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "campaign_id_required", "message": "Send campaign_id: the campaign whose party reached the milestone. Nothing was changed."})
			return
		}
	}
	var dm int
	if db.QueryRow(`SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1`, req.CampaignID).Scan(&dm) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "campaign_not_found"})
		return
	}
	if dm != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "gm_only", "message": "Only the GM of this campaign can grant a milestone level."})
		return
	}
	if !milestoneCampaign(req.CampaignID) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "xp_campaign",
			"message": fmt.Sprintf("This campaign uses Experience Points: levels come from XP (POST /api/gm/award-xp). To use milestones instead, set the table rule: POST /api/campaigns/%d/table-rules {\"advancement\": \"milestone\"}. Nothing was changed.", req.CampaignID)})
		return
	}
	ids := req.CharacterIDs
	if len(ids) == 0 {
		rows, qerr := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND COALESCE(is_dead, false) = false ORDER BY id`, req.CampaignID)
		if qerr == nil {
			for rows.Next() {
				var id int
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
		}
	}
	if len(ids) == 0 {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_characters", "message": "This campaign has no characters to level. Nothing was changed."})
		return
	}
	type who struct {
		id, level int
		name      string
	}
	var party []who
	for _, id := range ids {
		var c who
		var lobby int
		if db.QueryRow(`SELECT name, COALESCE(level, 1), COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, id).Scan(&c.name, &c.level, &lobby) != nil || lobby != req.CampaignID {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_not_in_campaign", "message": fmt.Sprintf("Character %d is not in this campaign. Nothing was changed.", id)})
			return
		}
		c.id = id
		party = append(party, c)
	}
	reason := strings.TrimSpace(req.Reason)
	how := "milestone"
	if reason != "" {
		how = "milestone: " + reason
	}
	var levels, atTop []map[string]interface{}
	var names []string
	for _, c := range party {
		if c.level >= 20 {
			atTop = append(atTop, map[string]interface{}{"character_id": c.id, "character_name": c.name, "level": c.level, "note": "Already level 20."})
			continue
		}
		hpBonus, asiEarned, note := applyLevelGain(c.id, c.level, c.level+1, how)
		// the XP total follows the level, so the sheet stays coherent if the campaign changes to Experience Points later
		db.Exec(`UPDATE characters SET xp = GREATEST(COALESCE(xp, 0), $1) WHERE id = $2`, getXPForNextLevel(c.level), c.id)
		line := map[string]interface{}{"character_id": c.id, "character_name": c.name, "old_level": c.level, "new_level": c.level + 1, "hp_bonus": hpBonus,
			"level_up_note": note, "choices": fmt.Sprintf("GET /api/characters/%d/setup lists what the new level asks the player to choose", c.id)}
		if asiEarned > 0 {
			line["asi_earned"] = asiEarned
		}
		levels = append(levels, line)
		names = append(names, fmt.Sprintf("%s (level %d)", c.name, c.level+1))
	}
	if len(levels) > 0 {
		text := "Milestone reached: " + strings.Join(names, ", ") + "."
		if reason != "" {
			text = "Milestone reached (" + reason + "): " + strings.Join(names, ", ") + "."
		}
		recordGMAction(db, req.CampaignID, "milestone", text, text)
	}
	out := map[string]interface{}{"success": true, "campaign_id": req.CampaignID, "levels_gained": levels}
	if len(atTop) > 0 {
		out["not_changed"] = atTop
	}
	if len(levels) == 0 {
		out["message"] = "Nobody gained a level: every character named is already level 20."
	} else {
		out["message"] = fmt.Sprintf("%d character(s) gained a level.", len(levels))
	}
	writeJSONStatus(w, http.StatusOK, out)
}
