package main

// GM tools found missing by the rules review (Dungeon Master's Guide chapter 2 and the SRD 5.2.1 Rules Glossary hazards):
// group checks, and the Dehydration and Malnutrition hazards (handled in POST /api/gm/environmental-hazard).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// glossaryText is the SRD 5.2.1 Rules Glossary text of an entry, word for word.
func glossaryText(name string) string {
	for _, e := range srdGlossary() {
		if strings.EqualFold(e.Name, name) {
			return strings.Join(e.Text, "\n")
		}
	}
	return ""
}

// @Summary Group check
// @Description When the party tries something together and the most skilled can cover for the less adept, everyone makes the same ability check; if at least half the group succeeds, the whole group succeeds. Send a skill (or an ability) and a DC; character_ids defaults to every living character in the campaign. Proficiency, Expertise, Jack of All Trades, Reliable Talent and Guidance apply; advantage/disadvantage apply to everyone.
// @Tags GM
// @Param request body object{campaign_id=int,skill=string,ability=string,dc=int,character_ids=[]int,advantage=bool,disadvantage=bool,description=string} true "Group check"
// @Router /gm/group-check [post]
func handleGMGroupCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Skill        string `json:"skill"`
		Ability      string `json:"ability"`
		DC           int    `json:"dc"`
		CharacterIDs []int  `json:"character_ids"`
		Advantage    bool   `json:"advantage"`
		Disadvantage bool   `json:"disadvantage"`
		Description  string `json:"description"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	skill := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(req.Skill, " ", "_")))
	ability := strings.ToLower(strings.TrimSpace(req.Ability))
	if skill != "" {
		a, known := skillAbilityMap[skill]
		if !known {
			dmBadRequest(w, "unknown_skill", "skill must be one of the 18 skills (for example perception, athletics, persuasion)")
			return
		}
		ability = a
	}
	if len(ability) > 3 {
		ability = ability[:3]
	}
	col := map[string]string{"str": "str", "dex": "dex", "con": "con", "int": "intl", "wis": "wis", "cha": "cha"}[ability]
	if col == "" || req.DC <= 0 {
		dmBadRequest(w, "skill_and_dc_required", "Send a skill (or an ability: str, dex, con, int, wis, cha) and a dc.")
		return
	}
	ids := req.CharacterIDs
	if len(ids) == 0 {
		rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND NOT COALESCE(is_dead,false) ORDER BY id`, lobby)
		if err == nil {
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
		dmBadRequest(w, "no_characters", "No characters to roll for.")
		return
	}
	label := titleCase(strings.ReplaceAll(skill, "_", " "))
	if skill == "" {
		label = map[string]string{"str": "Strength", "dex": "Dexterity", "con": "Constitution", "int": "Intelligence", "wis": "Wisdom", "cha": "Charisma"}[ability]
	}
	var results []map[string]interface{}
	var lines []string
	successes := 0
	for _, id := range ids {
		var name, class string
		var level, score, charLobby int
		if db.QueryRow(fmt.Sprintf(`SELECT name, class, level, %s, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, col), id).Scan(&name, &class, &level, &score, &charLobby) != nil || charLobby != lobby {
			continue
		}
		mod, prof, profNote, reliable := game.Modifier(score), 0, "", false
		if skill != "" {
			mod, prof, profNote, reliable = checkBonus(id, class, level, skill, score)
		}
		guide, _ := 0, ""
		if skill != "" {
			guide, _ = guidanceBonus(lobby, id, strings.ReplaceAll(skill, "_", " "))
		}
		d1, d2 := game.RollDie(20), game.RollDie(20)
		roll := d1
		bAdv, _ := buffCheckAdvantage(id, ability)                      // Foresight, Enhance Ability
		adv := req.Advantage || largeFormAdvantage(id, ability) || bAdv // Goliath Large Form: Advantage on Strength checks
		if adv && !req.Disadvantage && d2 > roll {
			roll = d2
		} else if req.Disadvantage && !adv && d2 < roll {
			roll = d2
		}
		roll, rt := reliableRoll(roll, reliable)
		total := roll + mod + prof + guide
		ok := total >= req.DC
		if !ok && luckReroll(id, false) { // the Luck Blade's Luck
			r2, _ := reliableRoll(game.RollDie(20), reliable)
			total = r2 + mod + prof + guide
			ok = total >= req.DC
			rt += " [Luck Blade reroll]"
		}
		if ok {
			successes++
		}
		results = append(results, map[string]interface{}{"character_id": id, "name": name, "total": total, "success": ok})
		lines = append(lines, fmt.Sprintf("%s %d%s%s (%s)", name, total, profNote, rt, map[bool]string{true: "success", false: "failure"}[ok]))
	}
	if len(results) == 0 {
		dmBadRequest(w, "no_characters", "None of those characters is in this campaign.")
		return
	}
	groupOK := successes*2 >= len(results)
	summary := fmt.Sprintf("Group %s check (DC %d): %d of %d succeed, so the group %s. %s", label, req.DC, successes, len(results),
		map[bool]string{true: "succeeds", false: "fails"}[groupOK], strings.Join(lines, "; "))
	recordGMAction(db, lobby, "group_check", summary, summary)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "group_success": groupOK, "successes": successes, "characters": len(results), "results": results, "result": summary})
}

// hungerThirstHazard handles the Dehydration and Malnutrition hazards for POST /api/gm/environmental-hazard. water/food is how much the
// creature had that day: "full", "half" (at least half), "less_than_half" or "none". days_without_food counts whole days with no food.
func hungerThirstHazard(lobby, id int, key, amount string, daysWithoutFood int) map[string]interface{} {
	name := creatureName(lobby, id)
	out := map[string]interface{}{"success": true}
	if tableRule(lobby, "food_and_water") == "assumed" {
		out["table_rule"] = "This campaign's table rules assume the adventurers carry enough food and water; apply this only when their supplies have really run out."
	}
	amount = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(amount), " ", "_"))
	switch key {
	case "dehydration":
		out["hazard"], out["rule"] = "Dehydration", glossaryText("Dehydration")
		if amount == "less_than_half" || amount == "none" {
			out["result"] = fmt.Sprintf("%s drank less than half the water it needs today: %s Exhaustion from dehydration can't be removed until it drinks the full amount for a day.", name, addExhaustion(id, 1))
		} else {
			out["result"] = fmt.Sprintf("%s drank enough water today; no Exhaustion.", name)
		}
	case "malnutrition":
		out["hazard"], out["rule"] = "Malnutrition", glossaryText("Malnutrition")
		switch {
		case daysWithoutFood >= 5:
			out["result"] = fmt.Sprintf("%s has eaten nothing for %d days: %s Exhaustion from malnutrition can't be removed until it eats the full amount for a day.", name, daysWithoutFood, addExhaustion(id, 1))
		case amount == "none":
			out["result"] = fmt.Sprintf("%s ate nothing today (%d day(s) without food); after 5 days without food it gains 1 Exhaustion level at the end of each day.", name, maxInt(daysWithoutFood, 1))
		case amount == "less_than_half":
			saved, total := creatureSave(lobby, id, "con", 10)
			if saved {
				out["result"] = fmt.Sprintf("%s ate less than half the food it needs: Constitution save %d vs DC 10, success.", name, total)
			} else {
				out["result"] = fmt.Sprintf("%s ate less than half the food it needs: Constitution save %d vs DC 10, failure: %s", name, total, addExhaustion(id, 1))
			}
		default:
			out["result"] = fmt.Sprintf("%s ate enough today; no Exhaustion.", name)
		}
	}
	return out
}
