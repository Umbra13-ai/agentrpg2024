package main

// The GM corrects a spell already in play (F231, Nov 10). Bless, Bane, Shield of Faith and the other lasting spell effects are not
// conditions (the 15 conditions are in POST /api/gm/conditions); they live in spell_effects. When a cast reached fewer creatures than
// it should have (Calla's Bless named three characters and the server blessed one, F229), the GM adds the effect to the creatures it
// missed, or removes it from one:
//	POST   /api/gm/spell-effect {"caster_id": 3, "spell": "bless", "target_ids": [4, 3]}
//	DELETE /api/gm/spell-effect {"target_id": 4, "spell": "bless"}
// With the spell in play, the new targets get a copy of the caster's effect (same kind, same rounds left), so it still ends with the
// caster's Concentration or its duration. With no one's spell in play the GM grants it (spell "Bless (GM)": its own duration, counted
// on the target's turns, no Concentration). The GM is the final word (the project owner): past the spell's target limit the reply only notes it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// spellConditionWords are the words a GM uses for the state a spell puts a creature in (the project owner, Nov 10: "blessed is a condition set
// by a spell"). They are not among the 15 conditions of the rules, so POST /api/gm/conditions passes them to the spell's effect.
var spellConditionWords = map[string]string{
	"blessed": "bless", "bless": "bless", "baned": "bane", "bane": "bane", "shield of faith": "shield-of-faith",
	"shield_of_faith": "shield-of-faith", "hasted": "haste", "haste": "haste", "slowed": "slow", "heroism": "heroism",
	"faerie fire": "faerie-fire", "outlined": "faerie-fire", "guidance": "guidance", "resistance": "resistance",
}

// activeCasterOf finds the one creature in the campaign whose spell is in play (0 when none or several).
func activeCasterOf(campaignID int, spellName string) int {
	rows, err := db.Query(`SELECT DISTINCT caster_id FROM spell_effects WHERE lobby_id = $1 AND LOWER(spell) = LOWER($2)`, campaignID, spellName)
	if err != nil {
		return 0
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		rows.Scan(&id)
		ids = append(ids, id)
	}
	if len(ids) == 1 {
		return ids[0]
	}
	return 0
}

// gmSpellKinds: the effect the GM grants when no one has the spell in play (kind, detail, rounds, how long that is).
var gmSpellKinds = map[string]struct {
	kind, detail string
	rounds       int
	lasts        string
}{
	"bless": {"bless", "", 10, "1 minute"}, "bane": {"bane", "", 10, "1 minute"}, "shield-of-faith": {"shield_of_faith", "", 100, "10 minutes"},
	"haste": {"hasted", "", 10, "1 minute"}, "slow": {"slowed", "", 10, "1 minute"}, "heroism": {"heroism", "0", 10, "1 minute"},
	"faerie-fire": {"faerie_fire", "", 10, "1 minute"}, "guidance": {"guidance", "", 10, "1 minute"}, "resistance": {"resistance", "", 10, "1 minute"},
}

// gmGrantedEffect: an effect the GM granted (its spell name ends in "(GM)") needs no one's Concentration.
func gmGrantedEffect(spell string) bool { return strings.HasSuffix(spell, " (GM)") }

// spellLimit is how many creatures a spell can affect, 0 when the server doesn't count it.
func spellLimit(slug string, slotLevel int) int {
	switch slug {
	case "bless", "bane":
		if slotLevel < 1 {
			slotLevel = 1
		}
		return spellExtraTargets(slotLevel, 1, 3)
	case "shield-of-faith", "haste", "heroism", "resistance", "guidance":
		return 1
	}
	return 0
}

// handleGMSpellEffect godoc
// @Summary GM: add a spell already in play to more creatures, or remove it from one
// @Description Bless, Bane, Shield of Faith and other lasting spell effects are not conditions. POST {"caster_id": N, "spell": "bless", "target_ids": [..], "slot_level": 1} copies the caster's active effect (same duration, still ends with their Concentration) to the creatures a cast missed; nothing is spent; the spell's target limit is kept. DELETE {"target_id": N, "spell": "bless"} removes that spell's effect from the creature.
// @Tags GM
// @Accept json
// @Produce json
// @Param request body object{caster_id=int,spell=string,target_ids=[]int,target_id=int,slot_level=int,campaign_id=int} true "the spell (slug or name), its caster and the creatures"
// @Success 200 {object} map[string]interface{}
// @Router /gm/spell-effect [post]
func handleGMSpellEffect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST to add a spell's effect, DELETE to remove it"})
		return
	}
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CasterID  int    `json:"caster_id"`
		Spell     string `json:"spell"`
		TargetIDs []int  `json:"target_ids"`
		TargetID  int    `json:"target_id"`
		SlotLevel int    `json:"slot_level"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Spell) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send spell (a slug or name), with caster_id and target_ids to add, or target_id to remove"})
		return
	}
	slug := normalizeSpellSlug(req.Spell)
	name := req.Spell
	if sp, ok := srdSpellsMemory[slug]; ok {
		name = sp.Name
	}
	if req.TargetID != 0 {
		req.TargetIDs = append(req.TargetIDs, req.TargetID)
	}
	inCampaign := func(id int) bool {
		if id < 0 {
			return monsterInCombat(campaignID, id)
		}
		var lobby int
		return db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, id).Scan(&lobby) == nil && lobby == campaignID
	}
	if r.Method == http.MethodDelete {
		var removed []string
		for _, id := range req.TargetIDs {
			if !inCampaign(id) {
				continue
			}
			if res, err := db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND (LOWER(spell) = LOWER($3) OR LOWER(spell) = LOWER($3) || ' (gm)')`, campaignID, id, name); err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					removed = append(removed, creatureName(campaignID, id))
				}
			}
		}
		out := map[string]interface{}{"success": true, "spell": name, "removed_from": removed}
		if len(removed) == 0 {
			out["removed_from"] = []string{}
			out["message"] = fmt.Sprintf("No creature named had %s's effect.", name)
		}
		writeJSONStatus(w, 200, out)
		return
	}
	if req.CasterID == 0 {
		req.CasterID = activeCasterOf(campaignID, name) // the one caster with this spell in play
	}
	if req.CasterID != 0 && !inCampaign(req.CasterID) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "caster_not_found", "message": "caster_id is a character of this campaign or the negative id of a monster in the current combat"})
		return
	}
	var add, outside []int
	seenID := map[int]bool{}
	for _, id := range req.TargetIDs {
		if !inCampaign(id) {
			outside = append(outside, id)
		} else if !seenID[id] {
			seenID[id] = true
			add = append(add, id)
		}
	}
	if len(outside) > 0 || len(add) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "target_not_in_campaign", "target_ids": outside,
			"message": "Each target is a character of this campaign or the negative id of a monster in the current combat"})
		return
	}
	// the caster's active effect of this spell: the one to copy
	var kind, detail, saveAbility string
	var rounds, saveDC int
	var existing []int
	if req.CasterID != 0 {
		rows, err := db.Query(`SELECT target_id, kind, detail, rounds_left, save_ability, save_dc FROM spell_effects
			WHERE lobby_id = $1 AND caster_id = $2 AND LOWER(spell) = LOWER($3) ORDER BY id`, campaignID, req.CasterID, name)
		if err == nil {
			for rows.Next() {
				var t int
				rows.Scan(&t, &kind, &detail, &rounds, &saveAbility, &saveDC)
				existing = append(existing, t)
			}
			rows.Close()
		}
		if kind != "" && !noConcentration[kind] && !concentrationHolds(req.CasterID, name) {
			kind, existing = "", nil // the caster's Concentration ended: nothing in play to join
		}
	}
	var names, notes []string
	if kind == "" {
		// the project owner (Nov 10): "DMs are effectively gods in the game and what they decide is what happens". No such spell in play: the GM
		// grants the effect itself. It lasts the spell's own duration, counted on the target's turns, and needs no one's Concentration.
		g, ok := gmSpellKinds[slug]
		if !ok {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_spell_effect",
				"message": fmt.Sprintf("The server has no lasting effect for %s. It grants: bless, bane, shield of faith, haste, slow, heroism, faerie fire, guidance, resistance.", name)})
			return
		}
		for _, id := range add {
			db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = $3 AND LOWER(spell) = LOWER($4)`, campaignID, id, g.kind, name+" (GM)")
			db.Exec(`INSERT INTO spell_effects (lobby_id, target_id, caster_id, spell, kind, detail, rounds_left, save_ability, save_dc)
				VALUES ($1,$2,$2,$3,$4,$5,$6,'',0)`, campaignID, id, name+" (GM)", g.kind, g.detail, g.rounds)
			names = append(names, creatureName(campaignID, id))
		}
		msg := fmt.Sprintf("The GM grants %s to %s for %s; no Concentration holds it.", name, joinNames(names), g.lasts)
		logAction(campaignID, 0, 0, "gm_spell_effect", "GM: "+msg, msg)
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "spell": name, "added": names, "gm_granted": true, "rounds_left": g.rounds, "message": msg})
		return
	}
	have := map[int]bool{}
	for _, t := range existing {
		have[t] = true
	}
	var fresh []int
	for _, id := range add {
		if !have[id] {
			fresh = append(fresh, id)
		}
	}
	if limit := spellLimit(slug, req.SlotLevel); limit > 0 && len(existing)+len(fresh) > limit {
		notes = append(notes, fmt.Sprintf("%s normally affects %d creatures at this slot level; the GM's call adds more.", name, limit))
	}
	for _, id := range fresh {
		db.Exec(`INSERT INTO spell_effects (lobby_id, target_id, caster_id, spell, kind, detail, rounds_left, save_ability, save_dc)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, campaignID, id, req.CasterID, name, kind, detail, rounds, saveAbility, saveDC)
		names = append(names, creatureName(campaignID, id))
	}
	var all []string
	for _, t := range existing {
		all = append(all, creatureName(campaignID, t))
	}
	all = append(all, names...)
	msg := fmt.Sprintf("%s's %s now affects %s.", creatureName(campaignID, req.CasterID), name, joinNames(all))
	if len(names) == 0 {
		msg = fmt.Sprintf("No one was added: %s already affects %s.", name, joinNames(all))
	}
	if len(notes) > 0 {
		msg += " " + strings.Join(notes, " ")
	}
	logAction(campaignID, 0, 0, "gm_spell_effect", "GM: "+msg, msg)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "spell": name, "added": names, "affects": all, "rounds_left": rounds, "message": msg})
}
