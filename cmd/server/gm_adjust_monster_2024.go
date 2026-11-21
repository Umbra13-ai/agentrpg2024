package main

// POST /api/gm/adjust-monster (Nov 11, the project owner): one GM command to change anything about an enemy in the current encounter. A DM who plays
// a missed player turn (or rolls dice at the table) had no way to record exact damage, healing or a rider on a surviving monster:
// /api/gm/aoe-cast rerolls and drops riders. The GM is the final word: nothing here is refused on rules limits (no resistances or
// immunities are applied unless asked, conditions are set even on a creature immune to them, HP can exceed the maximum).
//
//	{"target": "Kobold 4" or -3,
//	 "damage": 2, "damage_type": "psychic", "apply_resistances": false,   exact damage (resistances only if asked)
//	 "heal": 5, "hp": 12, "max_hp": 20, "temp_hp": 4, "ac": 15,
//	 "conditions": ["prone"], "rounds": 10, "end_save": {"ability": "wis", "dc": 13}, "source": "Vicious Mockery",
//	 "remove_conditions": ["prone"] or ["all"],
//	 "riders": ["disadvantage_next_attack"],
//	 "initiative": 14, "position": {"x": 5, "y": 7}, "name": "Kobold Chief",
//	 "remove": true,                                                       take it out of the encounter
//	 "note": "Millie's Vicious Mockery (DM-played turn)"}
// Add monsters with POST /api/campaigns/{id}/combat/add. Every change is logged in the campaign feed (with the note).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// handleGMAdjustMonster godoc
// @Summary GM: change anything about a monster in the encounter
// @Description The GM is the final word: exact damage (resistances only with apply_resistances), healing, HP, max HP, Temporary Hit Points, AC, conditions with rounds or a repeated save (set even on an immune creature), conditions removed ("all"), riders ("disadvantage_next_attack": its next attack roll has Disadvantage, as Vicious Mockery), initiative (the turn order is re-sorted, the current turn kept), battle map position, name, or removal from the encounter. target is the monster's name ("Kobold 4") or negative id. Each change goes in the campaign feed with the note. Add monsters with POST /api/campaigns/{id}/combat/add.
// @Tags GM
// @Accept json
// @Produce json
// @Body What to change
// @Success 200 {object} map[string]interface{}
// @Router /gm/adjust-monster [post]
func handleGMAdjustMonster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		Target           interface{}       `json:"target"` // the monster by name (exact, then a unique partial match) or id, instead of target_id
		TargetID         int               `json:"target_id"`
		Damage           *int              `json:"damage"`            // damage to deal, exactly (resistances only with apply_resistances)
		DamageType       string            `json:"damage_type"`       // the damage type
		ApplyResistances bool              `json:"apply_resistances"` // true: the monster's resistances, immunities and vulnerabilities apply to damage
		Heal             *int              `json:"heal"`              // Hit Points to restore
		HP               *int              `json:"hp"`                // set its Hit Points
		MaxHP            *int              `json:"max_hp"`            // set its Hit Point maximum
		TempHP           *int              `json:"temp_hp"`           // set its Temporary Hit Points
		AC               *int              `json:"ac"`                // set its Armor Class
		Conditions       []string          `json:"conditions"`        // conditions to apply (set even on an immune creature)
		Rounds           int               `json:"rounds"`            // the conditions last this many of its turns
		EndSave          *conditionSave    `json:"end_save"`          // a saving throw repeated at the end of each of its turns: {ability, dc}; a success ends the conditions
		Source           string            `json:"source"`            // what caused the conditions (shown to the GM)
		RemoveConditions []string          `json:"remove_conditions"` // conditions to remove ("all" for every one)
		Riders           []string          `json:"riders"`            // riders the server applies: disadvantage_next_attack (others are noted for the GM to narrate)
		Initiative       *int              `json:"initiative"`        // set its Initiative (its place in the turn order)
		Position         *mapSquare        `json:"position"`          // move its token on the battle map: {x, y}
		Name             string            `json:"name"`              // a new name
		Remove           bool              `json:"remove"`            // true: take it out of the fight
		Note             string            `json:"note"`              // why (shown in the log)
		Extra            map[string]string `json:"-"`
	}
	agentID, campaignID, ok := gmRead(w, r, &req)
	if !ok {
		return
	}
	entries := combatEntries(campaignID)
	if len(entries) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_combat", "message": "No encounter is running: start one (POST /api/campaigns/{id}/combat/start) and add monsters (/combat/add)."})
		return
	}
	// the target: a negative id, or a name (exact, then a unique partial match)
	tid := req.TargetID
	if tid == 0 {
		switch v := req.Target.(type) {
		case float64:
			tid = int(v)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				tid = n
			} else {
				want := strings.ToLower(strings.TrimSpace(v))
				var partial []int
				for _, e := range entries {
					if m, _ := e["is_monster"].(bool); !m {
						continue
					}
					nm := strings.ToLower(fmt.Sprint(e["name"]))
					if nm == want {
						tid = asInt(e["id"])
						break
					}
					if strings.Contains(nm, want) {
						partial = append(partial, asInt(e["id"]))
					}
				}
				if tid == 0 && len(partial) == 1 {
					tid = partial[0]
				}
			}
		}
	}
	var entry map[string]interface{}
	var monsters []string
	for _, e := range entries {
		if m, _ := e["is_monster"].(bool); m {
			monsters = append(monsters, fmt.Sprintf("%v (%v)", e["name"], e["id"]))
			if asInt(e["id"]) == tid {
				entry = e
			}
		}
	}
	if entry == nil || tid >= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "monster_not_found",
			"message": "target is a monster of the encounter, by name or negative id (characters: POST /api/gm/update-character).", "monsters": monsters})
		return
	}
	name := fmt.Sprint(entry["name"])
	var did []string

	if req.Remove {
		res, code := removeFromFight(campaignID, tid, "the GM removed the monster") // the combat-state owner (combat_state_2024.go)
		if code != "" {
			answerTurnRefusal(w, campaignID, nil, code)
			return
		}
		msg := fmt.Sprintf("%s is removed from the encounter.", name)
		writeAction(db, campaignID, 0, "gm_adjust_monster", "GM: "+msg+gmNote(req.Note), msg)
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "message": msg, "result": combatRemovalReply(campaignID, res)})
		return
	}

	// Hit Points through the monster Hit Point owner (monster_hp_2024.go), each a change of the values as they are then, under
	// the fight's lock; the other numbers through the combat-state owner (editCombatant)
	if req.MaxHP != nil {
		setMonsterMaxHP(campaignID, tid, *req.MaxHP)
		did = append(did, fmt.Sprintf("Hit Point maximum %d", max(*req.MaxHP, 1)))
	}
	if req.HP != nil {
		setMonsterHP(campaignID, tid, *req.HP)
		did = append(did, fmt.Sprintf("Hit Points set to %d", max(*req.HP, 0)))
	}
	if req.Damage != nil && *req.Damage > 0 {
		dtype := strings.ToLower(strings.TrimSpace(req.DamageType))
		if req.ApplyResistances && dtype != "" {
			text, ok := damageMonster(campaignID, tid, *req.Damage, dtype)
			if !ok {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "monster_damage_failed", "message": "The monster damage could not be applied."})
				return
			}
			did = append(did, strings.TrimSuffix(text, "."))
		} else {
			if _, err := loseMonsterHP(campaignID, tid, *req.Damage, dtype); err != nil { // exact: Temporary Hit Points first
				writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "monster_damage_failed", "message": "The monster damage could not be applied."})
				return
			}
			text := fmt.Sprintf("%s takes %d%s damage (exact)", name, *req.Damage, strings.TrimRight(" "+dtype, " "))
			text += wakeOnDamage(campaignID, tid, name)
			did = append(did, text)
		}
	}
	if req.Heal != nil && *req.Heal > 0 {
		if _, _, err := healMonster(campaignID, tid, *req.Heal); err != nil { // the monster Hit Point owner (monster_hp_2024.go): a failure is answered, never dropped
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "monster_heal_failed", "message": "The monster healing could not be applied."})
			return
		}
		did = append(did, fmt.Sprintf("heals %d", *req.Heal))
	}
	if req.TempHP != nil {
		if err := setMonsterTempHP(campaignID, tid, *req.TempHP); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "monster_temp_hp_failed", "message": "The monster's Temporary Hit Points could not be applied."})
			return
		}
		did = append(did, fmt.Sprintf("Temporary Hit Points %d", max(*req.TempHP, 0)))
	}
	if req.AC != nil {
		editCombatant(campaignID, tid, func(e map[string]interface{}) { e["ac"] = *req.AC })
		did = append(did, fmt.Sprintf("AC %d", *req.AC))
	}
	if strings.TrimSpace(req.Name) != "" {
		editCombatant(campaignID, tid, func(e map[string]interface{}) { e["name"] = strings.TrimSpace(req.Name) })
		did = append(did, "renamed "+strings.TrimSpace(req.Name))
		name = strings.TrimSpace(req.Name)
	}
	// conditions: set as told (the GM's call, immunities aside)
	for _, rc := range req.RemoveConditions {
		c := strings.ToLower(strings.TrimSpace(rc))
		if c == "all" {
			removeConditionsWhere(campaignID, tid, func(conditionRecord) bool { return true })
			did = append(did, "every condition ends")
			continue
		}
		endCondition(campaignID, tid, c, "")
		did = append(did, c+" ends")
	}
	src := req.Source
	if src == "" {
		src = req.Note
	}
	for _, rc := range req.Conditions {
		c := strings.ToLower(strings.TrimSpace(rc))
		if c == "" {
			continue
		}
		// the condition owner, as the GM tells it: replacing the creature's other records of it, even on an immune creature
		addCondition(campaignID, tid, condAdd{Condition: c, Source: src, Rounds: req.Rounds, EndSave: req.EndSave, Notes: req.Note,
			CreatedBy: agentID, Replace: true, Force: true})
		d := titleCase(c)
		if req.Rounds > 0 {
			d += fmt.Sprintf(" (%d rounds)", req.Rounds)
		}
		if req.EndSave != nil {
			d += fmt.Sprintf(" (DC %d %s save at the end of each of its turns)", req.EndSave.DC, strings.ToUpper(req.EndSave.Ability))
		}
		did = append(did, d)
	}
	for _, rd := range req.Riders {
		switch strings.ToLower(strings.TrimSpace(rd)) {
		case "disadvantage_next_attack", "disadvantage", "vicious_mockery":
			addCombatEffect(campaignID, tid, gmRiderSource, "sap") // its next attack roll before the end of its next turn has Disadvantage
			did = append(did, "Disadvantage on its next attack roll")
		default:
			did = append(did, fmt.Sprintf("note: %s (the server doesn't apply this rider; the GM narrates it)", rd))
		}
	}
	if req.Initiative != nil {
		changeFight(campaignID, func(f *fight) error { // its new place in the order; whose turn it is doesn't change
			f.setInitiative(tid, *req.Initiative)
			return nil
		})
		did = append(did, fmt.Sprintf("initiative %d", *req.Initiative))
	}
	if req.Position != nil {
		if moveMapToken(campaignID, tid, *req.Position) {
			did = append(did, fmt.Sprintf("moved to (%d, %d)", req.Position.X, req.Position.Y))
		} else {
			did = append(did, "no battle map token to move")
		}
	}
	if len(did) == 0 { // only a target: a readback
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "target_id": tid, "name": name, "hp": asInt(entry["hp"]), "max_hp": asInt(entry["max_hp"]),
			"temp_hp": asInt(entry["temp_hp"]), "ac": asInt(entry["ac"]), "conditions": conditionNames(campaignID, tid), "riders": activeRiders(campaignID, tid),
			"message": "Readback only (nothing sent to change): damage, heal, hp, max_hp, temp_hp, ac, conditions, remove_conditions, riders, initiative, position, name or remove."})
		return
	}
	state := ""
	hp, maxHP := 0, 0
	for _, e := range combatEntries(campaignID) {
		if asInt(e["id"]) == tid {
			hp, maxHP = asInt(e["hp"]), asInt(e["max_hp"])
		}
	}
	if hp <= 0 {
		state = name + " is at 0 Hit Points (defeated)."
	} else {
		state = fmt.Sprintf("%s: %d/%d Hit Points.", name, hp, maxHP)
	}
	msg := fmt.Sprintf("%s: %s. %s", name, strings.Join(did, "; "), state)
	writeAction(db, campaignID, 0, "gm_adjust_monster", "GM: "+msg+gmNote(req.Note), msg)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "target_id": tid, "name": name, "hp": hp, "max_hp": maxHP, "changes": did,
		"conditions": conditionNames(campaignID, tid), "riders": activeRiders(campaignID, tid), "message": msg})
}

func gmNote(n string) string {
	if strings.TrimSpace(n) == "" {
		return ""
	}
	return " (" + strings.TrimSpace(n) + ")"
}

// moveMapToken places a token on the encounter's battle map (the GM's say: no movement rules).
func moveMapToken(lobbyID, id int, to mapSquare) bool {
	var raw []byte
	if db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw) != nil || len(raw) == 0 {
		return false
	}
	var m encounterMap
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	moved := false
	for i := range m.Tokens {
		if m.Tokens[i].ID == id {
			m.Tokens[i].Position = to
			moved = true
		}
	}
	if !moved {
		return false
	}
	b, _ := json.Marshal(m)
	db.Exec(`UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2`, b, lobbyID)
	return true
}

// gmRiderSource marks a rider the GM put on a creature: not 0 (an attack only uses up effects caused by someone) and no creature's id,
// so no turn start expires it; the next attack roll uses it up.
const gmRiderSource = -1000000

// activeRiders: the effects waiting on a creature (a rider the GM set, Sap ...); each is used up by its trigger or expires.
func activeRiders(lobbyID, id int) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, e := range combatEffectsOn(lobbyID, id) {
		r := map[string]interface{}{"kind": e.Kind, "state": "pending", "text": e.Text}
		if e.Kind == "sap" {
			r["rider"] = "disadvantage_next_attack"
		}
		if e.SourceID == gmRiderSource {
			r["ends"] = "used up by its trigger, or at the end of its next turn (set by the GM)"
		} else {
			r["source"] = creatureName(lobbyID, e.SourceID)
			r["ends"] = "used up by its trigger, or at the start of " + creatureName(lobbyID, e.SourceID) + "'s next turn"
		}
		out = append(out, r)
	}
	for _, se := range spellEffectsOn(lobbyID, id) { // Bane, Faerie Fire, Slow, Hex ... while their caster concentrates
		out = append(out, map[string]interface{}{"kind": se.Kind, "spell": se.Spell, "state": "active", "source": creatureName(lobbyID, se.CasterID),
			"ends": fmt.Sprintf("when the spell ends (%d rounds left, or its caster's Concentration)", se.RoundsLeft)})
	}
	return out
}
