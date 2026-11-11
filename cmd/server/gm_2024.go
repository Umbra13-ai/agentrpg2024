package main

// GM commands on the rules of the SRD 5.2.1:
//	counterspell       the caster of the spell makes a Constitution save; on a failure the spell fails and its slot isn't expended
//	hurl-through-hell  Fiend Warlock 14: a Charisma save, 8d10 Psychic (not a Fiend), Incapacitated until the end of the Warlock's next turn
//	grapple, shove     Unarmed Strike options: the target makes a Strength or Dexterity save (its choice) against 8 + Strength + PB
//	escape-grapple     a Strength (Athletics) or Dexterity (Acrobatics) check against the grapple's escape DC (monsters; characters
//	                   use POST /api/conditions/escape)
//	release-grapple    the grappler lets go (no action required)
//	suffocation        out of breath: 1 Exhaustion level at the end of each of its turns; all of it goes when it can breathe again

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// creatureStats returns a creature's Strength and Dexterity scores, Proficiency Bonus, size and whether it is a Fiend.
func creatureStats(lobbyID, id int) (str, dex, pb int, size string, fiend bool) {
	if id > 0 {
		var level int
		var species string
		db.QueryRow(`SELECT str, dex, level, COALESCE(species,'') FROM characters WHERE id = $1`, id).Scan(&str, &dex, &level, &species)
		return str, dex, game.ProficiencyBonus(level), getSpeciesSize(species), false
	}
	key := monsterKeyOfCombatant(lobbyID, id)
	var mtype string
	db.QueryRow(`SELECT COALESCE(str,10), COALESCE(dex,10), COALESCE(proficiency_bonus,2), COALESCE(size,'Medium'), COALESCE(type,'') FROM monsters WHERE slug = $1`, key).
		Scan(&str, &dex, &pb, &size, &mtype)
	if pb == 0 {
		pb = 2
	}
	return str, dex, pb, size, strings.Contains(strings.ToLower(mtype), "fiend")
}

// tooBigToGrab: a grapple or shove works only on a target no more than one size larger.
func tooBigToGrab(attackerSize, targetSize string) bool {
	a, okA := sizeOrder[strings.ToLower(attackerSize)]
	t, okT := sizeOrder[strings.ToLower(targetSize)]
	return okA && okT && t > a+1
}

// unarmedOptionSave: the target of a grapple or shove picks the better of its Strength and Dexterity saves.
func unarmedOptionSave(lobbyID, id, dc int) (bool, int, string) {
	strBonus, strFail, strDis := savePeek(lobbyID, id, "str")
	dexBonus, dexFail, dexDis := savePeek(lobbyID, id, "dex")
	score := func(b int, fail, dis bool) int { // what a sensible target compares before it rolls
		if fail {
			return -100
		}
		if dis {
			return b - 5
		}
		return b
	}
	if score(dexBonus, dexFail, dexDis) > score(strBonus, strFail, strDis) {
		ok, total := creatureSave(lobbyID, id, "dex", dc)
		return ok, total, "Dexterity"
	}
	ok, total := creatureSave(lobbyID, id, "str", dc)
	return ok, total, "Strength"
}

// savePeek is a creature's saving throw bonus without side effects (saveBonusFor would use up a monster's Staggering Blow).
func savePeek(lobbyID, id int, short string) (int, bool, bool) {
	if id > 0 {
		return saveBonusFor(lobbyID, id, map[string]string{"str": "Strength", "dex": "Dexterity"}[short])
	}
	key := monsterKeyOfCombatant(lobbyID, id)
	sb := monsterStatBlock(key)
	if sb == nil {
		return 0, false, false
	}
	conds := conditionNames(lobbyID, id)
	return monsterSaveBonus(key, short, asInt(sb[short])), game.AutoFailsSave(conds, short), game.GetSaveDisadvantage(conds, 0, short)
}

func grappleSource(attackerID int) string { return fmt.Sprintf("Grapple #%d", attackerID) }

// @Summary Grapple (Unarmed Strike)
// @Description The attacker uses an Unarmed Strike to grapple a creature within 5 feet that is no more than one size larger. The target makes a Strength or Dexterity saving throw (the better one) against 8 + the attacker's Strength modifier + Proficiency Bonus; on a failure it has the Grappled condition, and the same DC is its escape DC (Strength (Athletics) or Dexterity (Acrobatics) check as an action). Ids: characters, or negative monster ids.
// @Tags GM
// @Param request body object{attacker_id=int,target_id=int} true "Grappler and target"
// @Router /gm/grapple [post]
func handleGMGrapple2024(w http.ResponseWriter, r *http.Request) {
	unarmedOption(w, r, "grapple")
}

// @Summary Shove (Unarmed Strike)
// @Description The attacker uses an Unarmed Strike to shove a creature within 5 feet that is no more than one size larger. The target makes a Strength or Dexterity saving throw (the better one) against 8 + the attacker's Strength modifier + Proficiency Bonus; on a failure it is pushed 5 feet away (effect=push, moved on the battle map) or has the Prone condition (effect=prone).
// @Tags GM
// @Param request body object{attacker_id=int,target_id=int,effect=string} true "Attacker, target and push or prone"
// @Router /gm/shove [post]
func handleGMShove2024(w http.ResponseWriter, r *http.Request) {
	unarmedOption(w, r, "shove")
}

func unarmedOption(w http.ResponseWriter, r *http.Request, kind string) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		AttackerID int    `json:"attacker_id"`
		TargetID   int    `json:"target_id"`
		Effect     string `json:"effect"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.AttackerID == 0 || req.TargetID == 0 || req.AttackerID == req.TargetID {
		dmBadRequest(w, "ids_required", "Send attacker_id and target_id (characters, or negative monster ids).")
		return
	}
	effect := strings.ToLower(req.Effect)
	if kind == "shove" && effect != "push" && effect != "prone" {
		dmBadRequest(w, "effect_required", "effect must be push (5 feet away) or prone.")
		return
	}
	result, _, out := resolveUnarmedOption(lobby, req.AttackerID, req.TargetID, kind, effect) // shared with the player actions (F180, items_c6_2024.go)
	if out == nil {
		writeJSONStatus(w, 200, map[string]interface{}{"success": false, "result": result})
		return
	}
	recordGMAction(db, lobby, kind, result, result)
	writeJSONStatus(w, 200, out)
}

// creatureDistance is the distance between two tokens on the battle map (known is false without one).
func creatureDistance(lobbyID, a, b int) (int, bool) {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return 0, false
	}
	ta, okA := m.token(a)
	tb, okB := m.token(b)
	if !okA || !okB {
		return 0, false
	}
	return tokenDistanceFeet(ta, tb), true
}

// @Summary Escape a grapple
// @Description The Grappled creature uses its action for a Strength (Athletics) or Dexterity (Acrobatics) check against the escape DC (the better of the two is used). Meant for monsters; a player's character uses POST /api/conditions/escape.
// @Tags GM
// @Param request body object{target_id=int} true "The Grappled creature"
// @Router /gm/escape-grapple [post]
func handleGMEscapeGrapple2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		TargetID    int `json:"target_id"`
		CharacterID int `json:"character_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.TargetID == 0 {
		req.TargetID = req.CharacterID
	}
	var rec *conditionRecord
	for _, c := range loadConditionRecords(lobby, req.TargetID) {
		if c.Condition == "grappled" {
			cc := c
			rec = &cc
			break
		}
	}
	if rec == nil {
		writeJSONStatus(w, 404, map[string]interface{}{"error": "not_grappled", "message": "That creature isn't Grappled."})
		return
	}
	dc := 10
	if len(rec.Escapes) > 0 && rec.Escapes[0].DC > 0 {
		dc = rec.Escapes[0].DC
	}
	str, dex, pb, _, _ := creatureStats(lobby, req.TargetID)
	bonus := game.Modifier(str)
	if b := game.Modifier(dex); b > bonus {
		bonus = b
	}
	if req.TargetID < 0 {
		if m := monsterSkillBonus(monsterKeyOfCombatant(lobby, req.TargetID), "athletics", "acrobatics"); m > bonus {
			bonus = m
		}
	} else {
		_ = pb
	}
	roll := game.RollDie(20)
	total := roll + bonus
	name := creatureName(lobby, req.TargetID)
	result := fmt.Sprintf("%s tries to escape: %d (d20 %d + %d) vs DC %d, ", name, total, roll, bonus, dc)
	if total >= dc {
		deleteConditionRecord(rec.ID)
		result += "success. The Grappled condition ends."
	} else {
		result += "failure."
	}
	recordGMAction(db, lobby, "escape_grapple", result, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": total >= dc, "result": result})
}

// monsterSkillBonus reads the best of the given skills from a monster's stat block ("Athletics +5").
func monsterSkillBonus(slug string, skills ...string) int {
	var raw string
	db.QueryRow(`SELECT COALESCE(skills::text,'') FROM monsters WHERE slug = $1`, slug).Scan(&raw)
	best := -99
	l := strings.ToLower(raw)
	for _, s := range skills {
		if i := strings.Index(l, s); i >= 0 {
			var n int
			rest := strings.TrimLeft(l[i+len(s):], "\": ")
			if _, err := fmt.Sscanf(rest, "%d", &n); err == nil && n > best {
				best = n
			}
		}
	}
	return best
}

// @Summary Release a grapple
// @Description The grappler releases the target at any time (no action required).
// @Tags GM
// @Param request body object{grappler_id=int,target_id=int} true "Grappler and target"
// @Router /gm/release-grapple [post]
func handleGMReleaseGrapple2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		GrapplerID int `json:"grappler_id"`
		TargetID   int `json:"target_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	res, _ := db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'grappled' AND source = $3`, lobby, req.TargetID, grappleSource(req.GrapplerID))
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSONStatus(w, 404, map[string]interface{}{"error": "no_such_grapple", "message": "That creature doesn't grapple the target."})
		return
	}
	if req.TargetID > 0 {
		syncListFromRecords(req.TargetID)
	}
	result := fmt.Sprintf("%s releases %s.", creatureName(lobby, req.GrapplerID), creatureName(lobby, req.TargetID))
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}

// @Summary Counterspell
// @Description Reaction, when the caster sees a creature within 60 feet casting a spell with Verbal, Somatic or Material components, using a level 3+ slot (slot_level, default 3). The creature makes a Constitution saving throw against the counterspeller's spell save DC. On a failure the spell dissipates with no effect, the action, Bonus Action or Reaction used to cast it is wasted, and a spell slot it used isn't expended.
// @Tags GM
// @Param request body object{caster_id=int,target_id=int,slot_level=int} true "The counterspelling character and the creature casting the spell"
// @Router /gm/counterspell [post]
func handleGMCounterspell2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CasterID  int `json:"caster_id"`
		TargetID  int `json:"target_id"`
		SlotLevel int `json:"slot_level"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.CasterID <= 0 || req.TargetID == 0 {
		dmBadRequest(w, "ids_required", "Send caster_id (the character casting Counterspell) and target_id (the creature casting the spell).")
		return
	}
	if req.SlotLevel == 0 {
		req.SlotLevel = 3
	}
	if req.SlotLevel < 3 || req.SlotLevel > 9 {
		dmBadRequest(w, "invalid_slot", "Counterspell uses a spell slot of level 3 to 9.")
		return
	}
	var name, class string
	var level int
	var reactionUsed bool
	db.QueryRow(`SELECT name, class, level, COALESCE(reaction_used,false) FROM characters WHERE id = $1`, req.CasterID).Scan(&name, &class, &level, &reactionUsed)
	if reactionUsed {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "reaction_used", "message": name + " has already used their Reaction this round."})
		return
	}
	if d, known := creatureDistance(lobby, req.CasterID, req.TargetID); known && d > 60 {
		writeJSONStatus(w, 200, map[string]interface{}{"success": false, "message": fmt.Sprintf("The caster is %d feet away; Counterspell reaches 60 feet.", d)})
		return
	}
	if !spendSlot(req.CasterID, class, level, req.SlotLevel) {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "no_spell_slot", "message": fmt.Sprintf("%s has no level %d spell slot left.", name, req.SlotLevel)})
		return
	}
	db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, req.CasterID)
	dc := spellSaveDCOf(req.CasterID)
	tName := creatureName(lobby, req.TargetID)
	saved, total := creatureSave(lobby, req.TargetID, "con", dc)
	result := fmt.Sprintf("%s casts Counterspell (level %d slot). %s makes a Constitution save: %d vs DC %d, ", name, req.SlotLevel, tName, total, dc)
	if saved {
		result += "success: the spell is cast normally."
	} else {
		result += "failure: the spell dissipates with no effect, and the action, Bonus Action or Reaction used to cast it is wasted. If that spell was cast with a spell slot, the slot isn't expended."
	}
	recordGMAction(db, lobby, "counterspell", result, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "countered": !saved, "save": total, "dc": dc, "result": result})
}

// spendSlot uses one spell slot of the level (Pact Magic slots count for a Warlock).
func spendSlot(charID int, class string, level, slot int) bool {
	total := characterSpellSlots(charID, class, level)[slot]
	if strings.EqualFold(class, "warlock") {
		if pl := pactSlotLevel(level); pl >= slot {
			total, slot = getWarlockPactSlots(level), pl
		}
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	used := map[string]int{}
	json.Unmarshal(raw, &used)
	k := fmt.Sprint(slot)
	if used[k] >= total {
		return false
	}
	used[k]++
	b, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET spell_slots_used = $1 WHERE id = $2`, b, charID)
	return true
}

// spellSaveDCOf is a character's spell save DC: 8 + spellcasting modifier + Proficiency Bonus.
func spellSaveDCOf(charID int) int {
	var class string
	var level int
	db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level)
	return 8 + game.Modifier(spellModScore(charID)) + game.ProficiencyBonus(level)
}

// pactSlotLevel is the level of a Warlock's Pact Magic slots (Warlock Features table: level 1 at 1, ... level 5 from 9).
func pactSlotLevel(level int) int {
	n := (level + 1) / 2
	if n > 5 {
		n = 5
	}
	if n < 1 {
		n = 1
	}
	return n
}

// @Summary Fiend Warlock Hurl Through Hell (level 14)
// @Description Once per turn when the Warlock hits a creature with an attack roll. The target makes a Charisma saving throw against the spell save DC; on a failure it disappears and hurtles through the Lower Planes: 8d10 Psychic damage if it isn't a Fiend, and the Incapacitated condition until the end of the Warlock's next turn, when it returns. Once per Long Rest, unless the Warlock expends a Pact Magic spell slot (use_pact_slot=true).
// @Tags GM
// @Param request body object{character_id=int,target_id=int,use_pact_slot=bool} true "The Warlock and the creature hit"
// @Router /gm/hurl-through-hell [post]
func handleGMHurlThroughHell2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CharacterID int  `json:"character_id"`
		TargetID    int  `json:"target_id"`
		UsePactSlot bool `json:"use_pact_slot"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	var name, class, subclass string
	var level int
	var used bool
	db.QueryRow(`SELECT name, class, COALESCE(subclass,''), level, COALESCE(hurl_through_hell_used,false) FROM characters WHERE id = $1`, req.CharacterID).
		Scan(&name, &class, &subclass, &level, &used)
	if !strings.EqualFold(class, "warlock") || !strings.EqualFold(subclass, "fiend") || level < 14 {
		dmBadRequest(w, "not_available", "Hurl Through Hell needs a Fiend Patron Warlock of level 14 or higher.")
		return
	}
	note := ""
	if used {
		if !req.UsePactSlot {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "already_used", "message": name + " has used Hurl Through Hell since the last Long Rest. Send use_pact_slot=true to expend a Pact Magic slot instead."})
			return
		}
		if !spendSlot(req.CharacterID, class, level, pactSlotLevel(level)) {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "no_pact_slot", "message": name + " has no Pact Magic slot left."})
			return
		}
		note = " (a Pact Magic slot is expended)"
	}
	db.Exec(`UPDATE characters SET hurl_through_hell_used = true WHERE id = $1`, req.CharacterID)
	dc := spellSaveDCOf(req.CharacterID)
	tName := creatureName(lobby, req.TargetID)
	_, _, _, _, fiend := creatureStats(lobby, req.TargetID)
	saved, total := creatureSave(lobby, req.TargetID, "cha", dc)
	result := fmt.Sprintf("%s uses Hurl Through Hell%s. %s makes a Charisma save: %d vs DC %d, ", name, note, tName, total, dc)
	if saved {
		result += "success: nothing happens."
	} else {
		result += "failure: it disappears and hurtles through a nightmare landscape"
		if fiend {
			result += " (a Fiend takes no damage)"
		} else {
			result += "; " + damageAny(lobby, req.TargetID, game.RollDamage("8d10", false), "psychic")
		}
		applyCondition(lobby, req.TargetID, "incapacitated", "Hurl Through Hell", 1, "", 0)
		result += ". It has the Incapacitated condition until the end of " + name + "'s next turn, when it returns to its space or the nearest unoccupied space."
	}
	recordGMAction(db, lobby, "hurl_through_hell", result, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}

// @Summary Suffocation
// @Description A creature can hold its breath for 1 + its Constitution modifier minutes (minimum 30 seconds) before suffocation begins (action=hold reports it). When it runs out of breath or is choking, it gains 1 Exhaustion level at the end of each of its turns (action=turn). When it can breathe again it removes all levels of Exhaustion it gained from suffocating (action=breathe).
// @Tags GM
// @Param request body object{character_id=int,action=string,underwater=bool} true "hold, turn or breathe; underwater: a creature with Water Breathing breathes"
// @Router /gm/suffocation [post]
func handleGMSuffocation2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		Action      string `json:"action"`
		Underwater  bool   `json:"underwater"` // the creature is underwater (Water Breathing lets it breathe; spells_more_2024.go)
	}
	json.NewDecoder(r.Body).Decode(&req)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS suffocation_levels INTEGER DEFAULT 0`)
	var name string
	var con, gained int
	if db.QueryRow(`SELECT name, con, COALESCE(suffocation_levels,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&name, &con, &gained) != nil {
		dmBadRequest(w, "character_not_found", "Send character_id.")
		return
	}
	var result string
	switch strings.ToLower(req.Action) {
	case "hold", "":
		mins := 1 + game.Modifier(con)
		if mins < 1 {
			result = name + " can hold their breath for 30 seconds (5 rounds)."
		} else {
			result = fmt.Sprintf("%s can hold their breath for %d minutes (%d rounds).", name, mins, mins*10)
		}
	case "turn", "tick":
		if waterBreathing(req.CharacterID) && (req.Underwater || isUnderwaterCombat(lobby)) {
			result = name + " breathes underwater (Water Breathing): no suffocation."
			break
		}
		db.Exec(`UPDATE characters SET suffocation_levels = COALESCE(suffocation_levels,0) + 1 WHERE id = $1`, req.CharacterID)
		result = fmt.Sprintf("%s is out of breath and gains 1 Exhaustion level. %s", name, addExhaustion(req.CharacterID, 1))
	case "breathe", "end":
		db.Exec(`UPDATE characters SET suffocation_levels = 0 WHERE id = $1`, req.CharacterID)
		result = fmt.Sprintf("%s can breathe again and removes the %d Exhaustion levels gained from suffocating. %s", name, gained, addExhaustion(req.CharacterID, -gained))
	default:
		dmBadRequest(w, "invalid_action", "action must be hold, turn or breathe.")
		return
	}
	recordGMAction(db, lobby, "suffocation", result, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}
