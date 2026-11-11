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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
)

// handleGMAdjustMonster godoc
// @Summary GM: change anything about a monster in the encounter
// @Description The GM is the final word: exact damage (resistances only with apply_resistances), healing, HP, max HP, Temporary Hit Points, AC, conditions with rounds or a repeated save (set even on an immune creature), conditions removed ("all"), riders ("disadvantage_next_attack": its next attack roll has Disadvantage, as Vicious Mockery), initiative (the turn order is re-sorted, the current turn kept), battle map position, name, or removal from the encounter. target is the monster's name ("Kobold 4") or negative id. Each change goes in the campaign feed with the note. Add monsters with POST /api/campaigns/{id}/combat/add.
// @Tags GM
// @Accept json
// @Produce json
// @Param request body object{target=string,damage=int,damage_type=string,apply_resistances=bool,heal=int,hp=int,max_hp=int,temp_hp=int,ac=int,conditions=[]string,rounds=int,source=string,remove_conditions=[]string,riders=[]string,initiative=int,name=string,remove=bool,note=string} true "What to change"
// @Success 200 {object} map[string]interface{}
// @Router /gm/adjust-monster [post]
func handleGMAdjustMonster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	agentID, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Target           interface{}       `json:"target"`
		TargetID         int               `json:"target_id"`
		Damage           *int              `json:"damage"`
		DamageType       string            `json:"damage_type"`
		ApplyResistances bool              `json:"apply_resistances"`
		Heal             *int              `json:"heal"`
		HP               *int              `json:"hp"`
		MaxHP            *int              `json:"max_hp"`
		TempHP           *int              `json:"temp_hp"`
		AC               *int              `json:"ac"`
		Conditions       []string          `json:"conditions"`
		Rounds           int               `json:"rounds"`
		EndSave          *conditionSave    `json:"end_save"`
		Source           string            `json:"source"`
		RemoveConditions []string          `json:"remove_conditions"`
		Riders           []string          `json:"riders"`
		Initiative       *int              `json:"initiative"`
		Position         *mapSquare        `json:"position"`
		Name             string            `json:"name"`
		Remove           bool              `json:"remove"`
		Note             string            `json:"note"`
		Extra            map[string]string `json:"-"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
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
		body, _ := json.Marshal(map[string]interface{}{"combatant_id": tid})
		rec := httptest.NewRecorder()
		rr := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		rr = rr.WithContext(r.Context())
		rr.Header = r.Header.Clone()
		handleCombatRemove(rec, rr, campaignID)
		out := map[string]interface{}{}
		json.Unmarshal(rec.Body.Bytes(), &out)
		msg := fmt.Sprintf("%s is removed from the encounter.", name)
		logAction(campaignID, 0, agentID, "gm_adjust_monster", "GM: "+msg+gmNote(req.Note), msg)
		writeJSONStatus(w, 200, map[string]interface{}{"success": out["error"] == nil, "message": msg, "result": out})
		return
	}

	// numbers in the turn order entry
	set := func(key string, v int) {
		upd := map[string]interface{}{key: v}
		updateCombatEntry(campaignID, tid, upd)
	}
	hp := asInt(entry["hp"])
	maxHP := asInt(entry["max_hp"])
	if req.MaxHP != nil {
		maxHP = *req.MaxHP
		set("max_hp", maxHP)
		did = append(did, fmt.Sprintf("Hit Point maximum %d", maxHP))
	}
	if req.HP != nil {
		hp = *req.HP
		if hp < 0 {
			hp = 0
		}
		set("hp", hp)
		did = append(did, fmt.Sprintf("Hit Points set to %d", hp))
	}
	if req.Damage != nil && *req.Damage > 0 {
		dtype := strings.ToLower(strings.TrimSpace(req.DamageType))
		if req.ApplyResistances && dtype != "" {
			text, _ := damageMonster(campaignID, tid, *req.Damage, dtype)
			did = append(did, strings.TrimSuffix(text, "."))
		} else {
			d := *req.Damage
			if t := asInt(entry["temp_hp"]); t > 0 { // Temporary Hit Points go first
				use := t
				if use > d {
					use = d
				}
				set("temp_hp", t-use)
				d -= use
			}
			hp -= d
			if hp < 0 {
				hp = 0
			}
			setMonsterHP(campaignID, tid, hp)
			text := fmt.Sprintf("%s takes %d%s damage (exact)", name, *req.Damage, strings.TrimRight(" "+dtype, " "))
			if *req.Damage > 0 {
				text += wakeOnDamage(campaignID, tid, name)
			}
			did = append(did, text)
		}
		for _, e := range combatEntries(campaignID) {
			if asInt(e["id"]) == tid {
				hp = asInt(e["hp"])
			}
		}
	}
	if req.Heal != nil && *req.Heal > 0 {
		hp += *req.Heal
		setMonsterHP(campaignID, tid, hp)
		did = append(did, fmt.Sprintf("heals %d", *req.Heal))
	}
	if req.TempHP != nil {
		set("temp_hp", *req.TempHP)
		did = append(did, fmt.Sprintf("Temporary Hit Points %d", *req.TempHP))
	}
	if req.AC != nil {
		set("ac", *req.AC)
		did = append(did, fmt.Sprintf("AC %d", *req.AC))
	}
	if strings.TrimSpace(req.Name) != "" {
		updateCombatEntry(campaignID, tid, map[string]interface{}{"name": strings.TrimSpace(req.Name)})
		did = append(did, "renamed "+strings.TrimSpace(req.Name))
		name = strings.TrimSpace(req.Name)
	}
	// conditions: set as told (the GM's call, immunities aside)
	for _, rc := range req.RemoveConditions {
		c := strings.ToLower(strings.TrimSpace(rc))
		if c == "all" {
			db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2`, campaignID, tid)
			did = append(did, "every condition ends")
			continue
		}
		db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND split_part(condition, ':', 1) = $3`, campaignID, tid, c)
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
		db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = $3`, campaignID, tid, c)
		db.Exec(`INSERT INTO condition_records (lobby_id, target_id, condition, source, escapes, duration_rounds, end_save, notes, created_by)
			VALUES ($1,$2,$3,$4,'[]',$5,$6,$7,$8)`, campaignID, tid, c, src, req.Rounds, endSaveJSON(req.EndSave), req.Note, agentID)
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
		reorderInitiative(campaignID, tid, *req.Initiative)
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
	logAction(campaignID, 0, agentID, "gm_adjust_monster", "GM: "+msg+gmNote(req.Note), msg)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "target_id": tid, "name": name, "hp": hp, "max_hp": maxHP, "changes": did,
		"conditions": conditionNames(campaignID, tid), "riders": activeRiders(campaignID, tid), "message": msg})
}

func gmNote(n string) string {
	if strings.TrimSpace(n) == "" {
		return ""
	}
	return " (" + strings.TrimSpace(n) + ")"
}

// updateCombatEntry sets fields of one combatant in the turn order.
func updateCombatEntry(lobbyID, id int, fields map[string]interface{}) {
	entries := combatEntries(lobbyID)
	for i, e := range entries {
		if asInt(e["id"]) == id {
			for k, v := range fields {
				e[k] = v
			}
			entries[i] = e
		}
	}
	b, _ := json.Marshal(entries)
	db.Exec(`UPDATE combat_state SET turn_order = $1 WHERE lobby_id = $2`, b, lobbyID)
}

// reorderInitiative sets a combatant's initiative and re-sorts the order, keeping whose turn it is.
func reorderInitiative(lobbyID, id, init int) {
	var idx int
	db.QueryRow(`SELECT COALESCE(current_turn_index,0) FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&idx)
	entries := combatEntries(lobbyID)
	cur := 0
	if idx >= 0 && idx < len(entries) {
		cur = asInt(entries[idx]["id"])
	}
	for i, e := range entries {
		if asInt(e["id"]) == id {
			e["initiative"] = init
			entries[i] = e
		}
	}
	sort.SliceStable(entries, func(a, b int) bool { return asInt(entries[a]["initiative"]) > asInt(entries[b]["initiative"]) })
	for i, e := range entries {
		if asInt(e["id"]) == cur {
			idx = i
		}
	}
	b, _ := json.Marshal(entries)
	db.Exec(`UPDATE combat_state SET turn_order = $1, current_turn_index = $2 WHERE lobby_id = $3`, b, idx, lobbyID)
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
