package main

// Cleric, 2024 rules (SRD 5.2.1).
//	Divine Spark (Channel Divinity)   action "divine_spark", a Magic action: heal a creature within 30 feet, or force a Constitution save
//	                                  (spell save DC) for Necrotic or Radiant damage, half on a success. 1d8 + Wisdom modifier; an extra d8
//	                                  at levels 7, 13 and 18.
//	Blessed Strikes (level 7)         every Cleric: +1d8 Necrotic or Radiant once per turn on a weapon hit (2d8 from level 14), unless the
//	                                  character chose Potent Spellcasting (subclass_choices.blessed_strikes = "potent_spellcasting").
//	Divine Intervention (level 10)    "divine intervention" in a cast: any Cleric spell of level 5 or lower, no slot, once per Long Rest.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func divineSparkDice(level int) int {
	switch {
	case level >= 18:
		return 4
	case level >= 13:
		return 3
	case level >= 7:
		return 2
	}
	return 1
}

// choseBlessedStrikesDivine says whether the Cleric's Blessed Strikes option is Divine Strike (the default).
func choseBlessedStrikesDivine(charID int) bool {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var choices map[string]string
	json.Unmarshal(raw, &choices)
	return choices["blessed_strikes"] != "potent_spellcasting"
}

// damageMonster deals damage to a monster in the combat (resistances and immunities applied) and says what happened.
func damageMonster(lobbyID, targetID, dmg int, dtype string) (string, bool) {
	tx, err := db.Begin()
	if err != nil {
		return "", false
	}
	defer tx.Rollback()
	var raw []byte
	if tx.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1 AND active = true FOR UPDATE`, lobbyID).Scan(&raw) != nil {
		return "", false
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	for i, e := range entries {
		if asInt(e["id"]) != targetID {
			continue
		}
		name := fmt.Sprint(e["name"])
		d, how := adjustMonsterDamage(fmt.Sprint(e["monster_key"]), dtype, dmg)
		d, how = petrifiedDamage(lobbyID, targetID, d, how)   // F196
		defer noteRegenerationBlock(lobbyID, targetID, dtype) // Troll: no Regeneration on its next turn after Acid or Fire
		if how != "resistant" && how != "immune" {
			if d2, ok := underwaterFire(lobbyID, dtype, d); ok {
				d, how = d2, "resistant"
			}
		}
		hp := asInt(e["hp"]) - d
		pnote := ""
		if hp <= 0 {
			hp, pnote = polymorphDrop(lobbyID, e, hp) // a Polymorph's beast form at 0 (spells_more_2024.go)
		}
		if hp <= 0 {
			var unote string
			hp, unote = undeadFortitude(lobbyID, e, hp, d, dtype, false) // Zombie (dodge_2024.go)
			pnote += unote
		}
		if hp < 0 {
			hp = 0
		}
		e["hp"] = hp
		entries[i] = e
		b, _ := json.Marshal(entries)
		if _, err := tx.Exec(`UPDATE combat_state SET turn_order = $1 WHERE lobby_id = $2`, b, lobbyID); err != nil || tx.Commit() != nil {
			return "", false
		}
		text := fmt.Sprintf("%s takes %d %s damage", name, d, dtype)
		if d > 0 {
			text += wakeOnDamage(lobbyID, targetID, name)
		}
		switch how {
		case "immune":
			text = fmt.Sprintf("%s is immune to %s damage", name, dtype)
		case "resistant":
			text += " (it resists)"
		case "vulnerable":
			text += " (it is vulnerable)"
		}
		if hp == 0 {
			text += ". " + name + " is defeated!"
		} else {
			text += fmt.Sprintf(". %s looks %s.", name, healthWord(hp, asInt(e["max_hp"])))
		}
		return text + pnote, true
	}
	return "", false
}

// divineSpark is the Channel Divinity effect: description "divine spark on <name>" (heal) or with target_id (monster, damage).
func divineSpark(charID int, description string) string {
	var class, name string
	var level, wis, lobby int
	if db.QueryRow(`SELECT class, name, level, wis, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&class, &name, &level, &wis, &lobby) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "cleric" || level < 2 {
		return "Divine Spark is a Channel Divinity effect of a Cleric of level 2 or higher."
	}
	low := strings.ToLower(description)
	_, monster, isMonster := attackedMonster(charID)
	healing := strings.Contains(low, "heal") || strings.Contains(low, "restore")
	if !healing && !isMonster {
		return "Name a creature to hurt with target_id, or say \"heal <name>\" to restore Hit Points."
	}
	if okRes, msg, _ := useClassResource(charID, "channel_divinity", 1); !okRes {
		return msg
	}
	total := game.Modifier(wis)
	_, sub, _ := subclassOf(charID)
	for i := 0; i < divineSparkDice(level); i++ {
		if healing && level >= 17 && strings.EqualFold(sub, "life") {
			total += 8 // Supreme Healing: Channel Divinity healing uses the maximum of each die (F84)
			continue
		}
		total += game.RollDie(8)
	}
	if total < 1 {
		total = 1
	}
	if healing {
		tid := parseTargetFromDescription(description, charID)
		if tid <= 0 {
			tid = charID
		}
		var hp, maxHP int
		var tname string
		db.QueryRow(`SELECT hp, max_hp, name FROM characters WHERE id = $1`, tid).Scan(&hp, &maxHP, &tname)
		nh := hp + total
		if nh > maxHP {
			nh = maxHP
		}
		db.Exec(`UPDATE characters SET hp = $1 WHERE id = $2`, nh, tid)
		return fmt.Sprintf("✨ Divine Spark: %s regains %d Hit Points (%d → %d).", tname, nh-hp, hp, nh)
	}
	dtype := "radiant"
	if strings.Contains(low, "necrotic") {
		dtype = "necrotic"
	}
	dc := 8 + game.ProficiencyBonus(level) + game.Modifier(wis)
	saved, roll := monsterSave(lobby, monster, "con", dc)
	dmg := total
	how := "fails"
	if saved {
		dmg = total / 2
		how = "succeeds on"
	}
	text, ok := damageMonster(lobby, monster, dmg, dtype)
	if !ok {
		return "Divine Spark could not reach that creature."
	}
	return fmt.Sprintf("✨ Divine Spark: the target %s its Constitution save (%d vs DC %d). %s.", how, roll, dc, text)
}

// handleGMTurnUndead: POST /api/gm/turn-undead {"caster_id": N, "target_ids": [monster ids]} (Channel Divinity, level 2).
// Each Undead the Cleric chooses within 30 feet makes a Wisdom saving throw (spell save DC); on a failure it has the Frightened and
// Incapacitated conditions for 1 minute (the effect ends early if it takes any damage or the Cleric is Incapacitated or dies).
// Sear Undead (level 5): each Undead that fails also takes Radiant damage, a number of d8s equal to the Wisdom modifier (minimum 1).
// @Summary Cleric Turn Undead (Channel Divinity)
// @Description Each Undead the Cleric chooses within 30 feet makes a Wisdom saving throw against the spell save DC; on a failure it is Frightened and Incapacitated for 1 minute (ends early if it takes damage). Sear Undead (level 5) adds Radiant damage.
// @Tags GM
// @Param request body object{caster_id=int,target_ids=[]int} true "Request"
// @Router /gm/turn-undead [post]
func handleGMTurnUndead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CasterID  int   `json:"caster_id"`
		TargetIDs []int `json:"target_ids"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil || req.CasterID == 0 || len(req.TargetIDs) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send caster_id (the Cleric) and target_ids (the Undead within 30 feet)."})
		return
	}
	var name, class string
	var level, wis, lobby int
	if db.QueryRow(`SELECT name, class, level, wis, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CasterID).Scan(&name, &class, &level, &wis, &lobby) != nil || lobby != campaignID {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if strings.ToLower(class) != "cleric" || level < 2 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_turn_undead", "message": name + " is not a Cleric of level 2 or higher."})
		return
	}
	if okRes, msg, _ := useClassResource(req.CasterID, "channel_divinity", 1); !okRes {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_channel_divinity", "message": msg})
		return
	}
	dc := 8 + game.ProficiencyBonus(level) + game.Modifier(wis)
	dice := game.Modifier(wis)
	if dice < 1 {
		dice = 1
	}
	var results []map[string]interface{}
	for _, tid := range req.TargetIDs {
		res := map[string]interface{}{"target_id": tid}
		var typ string
		for _, e := range combatEntries(campaignID) {
			if asInt(e["id"]) == tid {
				db.QueryRow(`SELECT COALESCE(type,'') FROM monsters WHERE slug = $1`, sprintAny(e["monster_key"])).Scan(&typ)
			}
		}
		if typ != "undead" {
			res["note"] = "not an Undead"
			results = append(results, res)
			continue
		}
		saved, total := monsterSave(campaignID, tid, "wis", dc)
		res["total"], res["dc"], res["saved"] = total, dc, saved
		if !saved {
			if level >= 5 { // Sear Undead first: its damage "doesn't end the turn effect" (other damage does, F85)
				sear := 0
				for i := 0; i < dice; i++ {
					sear += game.RollDie(8)
				}
				text, _ := damageMonster(campaignID, tid, sear, "radiant")
				res["sear_undead"] = text
			}
			addMonsterCondition(campaignID, tid, "frightened", "Turn Undead ("+name+")", 10, "", 0)
			addMonsterCondition(campaignID, tid, "incapacitated", "Turn Undead ("+name+")", 10, "", 0)
			res["turned"] = true
		}
		results = append(results, res)
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "cleric": name, "save": "Wisdom", "dc": dc, "results": results})
}
