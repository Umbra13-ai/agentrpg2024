package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"strings"
)

// handleGMAoECast godoc
// @Summary Cast an area of effect spell on multiple targets
// @Description GM resolves an AoE spell (like Fireball) against multiple targets. Each target makes a saving throw.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Body AoE cast details
// @Success 200 {object} map[string]interface{} "Results for each target"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /gm/aoe-cast [post]
func handleGMAoECast(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		SpellSlug     string `json:"spell_slug"` // the spell (its slug)
		CasterID      int    `json:"caster_id"`
		TargetIDs     []int  `json:"target_ids"`
		DC            int    `json:"dc"`
		Ritual        bool   `json:"ritual"`         // true: cast as a ritual (no slot; the spell must have the Ritual tag)
		SlotLevel     int    `json:"slot_level"`     // For upcasting
		SculptTargets []int  `json:"sculpt_targets"` // Evocation Wizard's Sculpt Spells - allies to protect
	}
	// the characters' campaign (caster and targets), which must be an active one the GM runs (gm_campaign_2024.go)
	campaignID, ok := gmReadAs(w, r, agentID, &req, targetsIn(&req.CasterID, &req.TargetIDs))
	if !ok {
		return
	}

	if req.SpellSlug == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "spell_slug_required"})
		return
	}
	if len(req.TargetIDs) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "target_ids_required"})
		return
	}

	// Get spell info (added school for Sculpt Spells/Empowered Evocation)
	var spellName, damageDice, damageType, savingThrow, description, spellSchool string
	var damageAtSlotLevelJSON []byte
	var spellLevel int
	var isRitual bool
	err = db.QueryRow(`
		SELECT name, COALESCE(damage_dice, ''), COALESCE(damage_type, ''), COALESCE(saving_throw, ''), 
		       COALESCE(description, ''), level, COALESCE(is_ritual, false), COALESCE(damage_at_slot_level, '{}'),
		       COALESCE(school, '')
		FROM spells WHERE slug = $1
	`, req.SpellSlug).Scan(&spellName, &damageDice, &damageType, &savingThrow, &description, &spellLevel, &isRitual, &damageAtSlotLevelJSON, &spellSchool)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "spell_not_found", "slug": req.SpellSlug})
		return
	}

	// Parse damage at slot level for upcasting
	damageAtSlotLevel := map[string]string{}
	json.Unmarshal(damageAtSlotLevelJSON, &damageAtSlotLevel)

	// Check ritual casting
	usedSlot := true
	if req.Ritual {
		if !isRitual {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "spell_cannot_be_ritual",
				"message": fmt.Sprintf("%s cannot be cast as a ritual", spellName),
			})
			return
		}
		usedSlot = false // Ritual casting doesn't use a spell slot
	}

	// If caster provided, handle spell slot usage
	var casterName string
	if req.CasterID > 0 && usedSlot && spellLevel > 0 {
		var class string
		var level int
		db.QueryRow(`SELECT name, class, level FROM characters WHERE id = $1`, req.CasterID).Scan(&casterName, &class, &level)

		// Use spell slot if not ritual
		slots := characterSpellSlots(req.CasterID, class, level)
		slotLevel := spellLevel
		if req.SlotLevel > spellLevel {
			slotLevel = req.SlotLevel // Upcasting
		}

		if totalSlots, ok := slots[slotLevel]; ok && totalSlots > 0 {
			// the guarded spend (resources_2024.go)
			if _, _, err := changeSpent(req.CasterID, func(s *spent) error {
				return spendSlotIn(s.Slots, slotLevel, totalSlots, fmt.Sprintf("No level %d spell slots remaining", slotLevel))
			}); err != nil {
				if r, refused := refusedSpend(err); refused {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_spell_slots", "message": r.Msg})
					return
				}
				answerDBFailure(w, err, "the caster")
				return
			}
		}
	}

	// Calculate DC if not provided
	dc := req.DC
	if dc == 0 {
		dc = 15 // Default DC
		if req.CasterID > 0 {
			var intl, wis, cha, level int
			var class string
			db.QueryRow(`SELECT intl, wis, cha, level, class FROM characters WHERE id = $1`, req.CasterID).Scan(&intl, &wis, &cha, &level, &class)
			classKey := strings.ToLower(class)
			spellMod := 0
			if c, ok := srdClasses[classKey]; ok {
				switch c.Spellcasting {
				case "INT":
					spellMod = game.Modifier(intl)
				case "WIS":
					spellMod = game.Modifier(wis)
				case "CHA":
					spellMod = game.Modifier(cha)
				}
			}
			dc = game.SpellSaveDC(level, spellMod)
		}
	}

	// Determine actual slot level used (for upcast damage)
	actualSlotLevel := spellLevel
	if req.SlotLevel > spellLevel {
		actualSlotLevel = req.SlotLevel
	}

	// Roll base damage once (same for all targets) - use upcast dice if available
	actualDamageDice := damageDice
	if len(damageAtSlotLevel) > 0 {
		slotKey := fmt.Sprintf("%d", actualSlotLevel)
		if upcastDice, ok := damageAtSlotLevel[slotKey]; ok {
			actualDamageDice = upcastDice
		}
	}

	// Cantrip damage scaling based on caster's character level
	// Get damage_at_character_level from spell if it's a cantrip
	if spellLevel == 0 && req.CasterID > 0 {
		var damageAtCharLevelJSON []byte
		var casterCharLevel int
		db.QueryRow(`SELECT COALESCE(damage_at_character_level, '{}') FROM spells WHERE slug = $1`, req.SpellSlug).Scan(&damageAtCharLevelJSON)
		db.QueryRow(`SELECT level FROM characters WHERE id = $1`, req.CasterID).Scan(&casterCharLevel)
		damageAtCharLevel := map[string]string{}
		json.Unmarshal(damageAtCharLevelJSON, &damageAtCharLevel)
		if len(damageAtCharLevel) > 0 && casterCharLevel > 0 {
			actualDamageDice = game.ScaledCantripDamage(damageAtCharLevel, casterCharLevel)
		}
	}

	baseDamage := 0
	if actualDamageDice != "" {
		baseDamage = game.RollDamage(actualDamageDice, false)
	}

	// Evocation Wizard features
	// Empowered Evocation and Elemental Affinity: one damage roll of the spell (spell_one_roll_2024.go)
	hasSculptSpells := false
	maxSculptTargets := 0
	isEvocationSpell := strings.ToLower(spellSchool) == "evocation"
	oneRoll := oneRollBonus{}
	if req.CasterID > 0 {
		oneRoll = spellOneRollBonus(req.CasterID, req.SpellSlug, srdSpellsMemory[req.SpellSlug])
		baseDamage += oneRoll.Total()
	}
	empoweredEvocationBonus := oneRoll.Empowered

	if req.CasterID > 0 && isEvocationSpell {
		// Get caster's subclass and level
		var subclass sql.NullString
		var casterLevel int
		db.QueryRow(`SELECT COALESCE(subclass, ''), level FROM characters WHERE id = $1`, req.CasterID).Scan(&subclass, &casterLevel)

		if subclass.Valid && subclass.String != "" {
			// Check for Sculpt Spells (level 2 Evocation Wizard feature)
			if hasSubclassFeature(subclass.String, casterLevel, "sculpt_spells") {
				hasSculptSpells = true
				maxSculptTargets = 1 + actualSlotLevel // 1 + spell's level
			}
		}
	}

	elementalAffinityBonus, elementalAffinityAncestry := oneRoll.Affinity, oneRoll.AncestryDmg

	// Check for Potent Cantrip (Evocation Wizard level 6+)
	// Potent Cantrip: when a creature succeeds on save against your cantrip, they take half damage
	hasPotentCantrip := false
	if req.CasterID > 0 && spellLevel == 0 {
		// Potent Cantrip only applies to cantrips (level 0 spells)
		var subclass sql.NullString
		var casterLevel int
		db.QueryRow(`SELECT COALESCE(subclass, ''), level FROM characters WHERE id = $1`, req.CasterID).Scan(&subclass, &casterLevel)

		if subclass.Valid && subclass.String != "" {
			if hasSubclassFeature(subclass.String, casterLevel, "potent_cantrip") {
				hasPotentCantrip = true
			}
		}
	}

	// Validate sculpt targets if provided
	sculptTargetSet := make(map[int]bool)
	if hasSculptSpells && len(req.SculptTargets) > 0 {
		// Limit to max allowed sculpt targets
		allowedCount := maxSculptTargets
		if len(req.SculptTargets) < allowedCount {
			allowedCount = len(req.SculptTargets)
		}
		for i := 0; i < allowedCount; i++ {
			sculptTargetSet[req.SculptTargets[i]] = true
		}
	}

	// Process each target
	results := []map[string]interface{}{}
	totalDamageDealt := 0

	for _, targetID := range req.TargetIDs {
		var targetName string
		var targetHP, targetMaxHP int
		var targetLobbyID int

		// Check if target is a character or a monster (negative IDs are combat-added monsters)
		if targetID > 0 {
			err = db.QueryRow(`SELECT name, hp, max_hp, lobby_id FROM characters WHERE id = $1`, targetID).Scan(
				&targetName, &targetHP, &targetMaxHP, &targetLobbyID)
			if err != nil {
				results = append(results, map[string]interface{}{
					"target_id": targetID,
					"error":     "target_not_found",
				})
				continue
			}
		} else {
			targetName = creatureName(campaignID, targetID) // the name the fight gives it, as every other line of this reply says it (I-ALDENQA-3)
		}

		// the target's saving throw against the spell: the one saving throw function (save_2024.go), for a character or a monster.
		// Sculpt Spells: a protected target succeeds automatically and takes no damage.
		sc := saveCtx{lobby: campaignID, saver: targetID, ability: savingThrow, dc: dc, spell: true, source: req.CasterID}
		sculptSpellsApplied := sculptTargetSet[targetID]
		if sculptSpellsApplied {
			sc.autoSucceed = "Sculpt Spells"
		}
		sv := sc.roll()
		saved := sv.Success

		// the damage after the save: game.SaveDamage (half on a success for a leveled area spell; none for a cantrip unless Potent
		// Cantrip; Sculpt Spells none) with the target's Evasion (evasion_2024.go: a character's or a monster's)
		half := !sculptSpellsApplied && (spellLevel > 0 || hasPotentCantrip)
		evasionApplied := half && creatureEvasion(campaignID, targetID, savingThrow)
		potentCantripApplied := saved && half && spellLevel == 0 && !evasionApplied
		damage := game.SaveDamage(baseDamage, saved, half, evasionApplied)

		result := map[string]interface{}{
			"target_id":   targetID,
			"target_name": targetName,
			"save_roll":   sv.Natural,
			"save_mod":    sv.Save.Modifier(),
			"save_total":  sv.Total,
			"save":        sv.Line(targetName),
			"dc":          dc,
			"saved":       saved,
			"damage":      damage,
		}

		// Add Sculpt Spells info to result
		if sculptSpellsApplied {
			result["sculpt_spells"] = true
			result["sculpt_spells_info"] = "Protected by Sculpt Spells - automatically succeeded and took no damage"
		}

		// Add Evasion info to result
		if evasionApplied {
			result["evasion"] = true
			if saved {
				result["evasion_info"] = "Evasion: Succeeded on DEX save - took no damage instead of half"
			} else {
				result["evasion_info"] = "Evasion: Failed DEX save - took half damage instead of full"
			}
		}

		// Add Potent Cantrip info to result
		if potentCantripApplied {
			result["potent_cantrip"] = true
			result["potent_cantrip_info"] = "Potent Cantrip: Succeeded on save against cantrip - took half damage instead of none"
		}

		// Apply damage to characters or monsters
		if damage > 0 {
			if targetID > 0 {
				// Character: the Hit Point owner (hit_points_2024.go) applies resistance and immunity, Temporary Hit Points, dropping to 0
				// (Relentless Rage and Endurance, Death Ward), damage at 0, Instant Death and Concentration. Before Nov 15 2026 this path
				// wrote the Hit Points itself and skipped Temporary Hit Points, the 0 Hit Point rules and Concentration.
				d, err := damageHP(targetID, oneHit(damage, damageType))
				if err != nil {
					result["error"] = notFoundOrDBError(err, "character_not_found")
					results = append(results, result)
					continue
				}
				view := d.View()
				for _, k := range []string{"resistances_applied", "temp_hp_absorbed", "status", "message", "death_save_failures", "concentration", "concentration_kept", "relentless_rage", "relentless_rage_failed", "class_feature_note", "relentless_endurance", "racial_feature_note"} {
					if v, ok := view[k]; ok {
						result[k] = v
					}
				}
				damage = d.Dealt
				result["hp_before"] = d.Before.HP
				result["hp_after"] = d.After.HP
				result["temp_hp"] = d.After.Temp
				result["damage"] = damage // Update with resisted damage
				totalDamageDealt += damage

				// Check for kill effects when target drops to 0 HP
				if d.Down && req.CasterID > 0 {
					if killEffects := applyKillEffects(req.CasterID); killEffects != nil {
						result["kill_effects"] = killEffects
					}
				}
			} else {
				// A monster: the monster Hit Point owner (monster_hp_2024.go) applies its resistances, immunities and vulnerabilities
				// and changes only its Hit Points, under the fight's lock. Before Nov 15 2026 this path rewrote the whole turn order
				// through a five-field struct: every initiative, AC and is_monster flag was lost and players could no longer target any
				// monster (audit AUD-09).
				h, ok := hurtMonster(campaignID, targetID, damage, damageType, false)
				if !ok {
					result["error"] = "target_not_found"
				} else {
					for k, v := range monsterHitFields(h, damageType) {
						result[k] = v
					}
					result["target_name"] = h.Name
					result["hp_before"] = h.Before
					result["hp_after"] = h.After
					result["damage"] = h.Dealt
					result["message"] = h.Text
					totalDamageDealt += h.Dealt
					if h.After == 0 && h.Before > 0 && req.CasterID > 0 { // kill effects when the monster drops to 0 HP
						if killEffects := applyKillEffects(req.CasterID); killEffects != nil {
							result["kill_effects"] = killEffects
						}
					}
				}
			}
		}

		results = append(results, result)
	}

	// Log the AoE cast
	targetNames := []string{}
	for _, r := range results {
		if name, ok := r["target_name"].(string); ok {
			targetNames = append(targetNames, name)
		}
	}

	castType := "cast"
	if req.Ritual {
		castType = "ritual cast"
	}

	actionDesc := fmt.Sprintf("%s %s (AoE) targeting %s", castType, spellName, strings.Join(targetNames, ", "))
	resultStr := fmt.Sprintf("DC %d %s save. Base damage: %d. Targets: %d", dc, savingThrow, baseDamage, len(results))

	writeAction(db, campaignID, req.CasterID, "aoe_cast", actionDesc, resultStr)

	response := map[string]interface{}{
		"success":      true,
		"spell":        spellName,
		"spell_school": spellSchool,
		"cast_type":    castType,
		"used_slot":    usedSlot,
		"base_damage":  baseDamage,
		"damage_type":  damageType,
		"save_type":    savingThrow,
		"dc":           dc,
		"targets":      results,
		"total_damage": totalDamageDealt,
	}

	// Add Evocation Wizard feature info
	if empoweredEvocationBonus > 0 {
		response["empowered_evocation"] = map[string]interface{}{
			"applied":   true,
			"int_bonus": empoweredEvocationBonus,
			"info":      "Added Intelligence modifier to damage (Empowered Evocation)",
		}
	}
	if hasSculptSpells && len(sculptTargetSet) > 0 {
		sculptedIDs := make([]int, 0, len(sculptTargetSet))
		for id := range sculptTargetSet {
			sculptedIDs = append(sculptedIDs, id)
		}
		response["sculpt_spells"] = map[string]interface{}{
			"available":         true,
			"max_targets":       maxSculptTargets,
			"protected_targets": sculptedIDs,
			"info":              "Protected targets automatically succeeded on saves and took no damage",
		}
	}

	// Add Elemental Affinity info
	if elementalAffinityBonus > 0 {
		response["elemental_affinity"] = map[string]interface{}{
			"applied":     true,
			"cha_bonus":   elementalAffinityBonus,
			"damage_type": elementalAffinityAncestry,
			"info":        fmt.Sprintf("Added Charisma modifier to %s damage (Elemental Affinity)", elementalAffinityAncestry),
		}
	}

	json.NewEncoder(w).Encode(response)
}

// handleGMDispelMagic godoc
// @Summary Cast Dispel Magic to end ongoing spell effects
// @Description Dispel Magic (3rd level abjuration): Choose one creature, object, or magical effect within range. Any spell of 3rd level or lower on the target ends. For higher level spells, make an ability check (DC 10 + spell level). Auto-succeeds if slot level >= target spell level. The spells other casters put on the target (Mage Armor, Barkskin, Bless, Haste ...) are checked one by one by their own level and listed in spells_on_target; without target_spell_level or Concentration, those are what it dispels (effect_name picks one).
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Body Dispel Magic details: target_id is the character/monster affected, target_spell_level required (or auto-detected from concentration), slot_level defaults to 3, effect_name optional
// @Success 200 {object} map[string]interface{} "Dispel Magic result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/dispel-magic [post]
func handleGMDispelMagic(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CasterID         int    `json:"caster_id"`
		TargetID         int    `json:"target_id"`
		TargetSpellLevel int    `json:"target_spell_level"` // the level of the spell to end
		SlotLevel        int    `json:"slot_level"`         // the slot Dispel Magic is cast with (3 to 9): a spell of that level or lower ends without a check
		EffectName       string `json:"effect_name"`        // Optional: name of effect to dispel
	}
	// the character's campaign, which must be one the GM runs (gm_campaign_2024.go)
	lobbyID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CasterID))
	if !ok {
		return
	}

	// Validate inputs
	if req.CasterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "caster_id required",
		})
		return
	}

	if req.TargetID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "target_id required (creature or object with spell effect)",
		})
		return
	}

	// Default to 3rd level slot (minimum for Dispel Magic)
	if req.SlotLevel == 0 {
		req.SlotLevel = 3
	}
	if req.SlotLevel < 3 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "Dispel Magic requires at least a 3rd level spell slot",
		})
		return
	}
	if req.SlotLevel > 9 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "Maximum spell slot level is 9",
		})
		return
	}

	// Get caster info
	var casterName, class string
	var level, intl, wis, cha int
	err = db.QueryRow(`
		SELECT name, class, level, intl, wis, cha FROM characters WHERE id = $1
	`, req.CasterID).Scan(&casterName, &class, &level, &intl, &wis, &cha)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "caster_not_found"})
		return
	}

	// Get target info and check for concentration
	var targetName string
	var concentratingOn sql.NullString
	err = db.QueryRow(`
		SELECT name, COALESCE(concentrating_on, '') FROM characters WHERE id = $1
	`, req.TargetID).Scan(&targetName, &concentratingOn)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "target_not_found",
			"message": fmt.Sprintf("Target character %d not found", req.TargetID),
		})
		return
	}

	// If no target_spell_level provided, try to detect from concentration
	// For now, if concentrating, assume the spell level is at least 1
	// GM should provide target_spell_level for accuracy
	effectsOnly := false // the spells on the target, each checked by its own level (F76)
	if req.TargetSpellLevel == 0 {
		if concentratingOn.String != "" {
			// Default to level 1 for unknown concentration spells
			// GM should specify target_spell_level for higher level effects
			req.TargetSpellLevel = 1
		} else if len(spellEffectsOn(lobbyID, req.TargetID)) > 0 {
			effectsOnly = true
		} else {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_request",
				"message": "target_spell_level required (target has no concentration to auto-detect)",
			})
			return
		}
	}

	if !effectsOnly && (req.TargetSpellLevel < 1 || req.TargetSpellLevel > 9) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "target_spell_level must be between 1 and 9",
		})
		return
	}

	// Check if caster has the spell slot
	slots := characterSpellSlots(req.CasterID, class, level)
	totalSlots, hasSlot := slots[req.SlotLevel]
	if !hasSlot || totalSlots == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_spell_slots",
			"message": fmt.Sprintf("%s doesn't have level %d spell slots!", casterName, req.SlotLevel),
		})
		return
	}

	// Get used slots
	var usedJSON []byte
	db.QueryRow("SELECT COALESCE(spell_slots_used, '{}') FROM characters WHERE id = $1", req.CasterID).Scan(&usedJSON)
	var used map[string]int
	json.Unmarshal(usedJSON, &used)

	usedKey := fmt.Sprintf("%d", req.SlotLevel)
	usedSlots := used[usedKey]
	if usedSlots >= totalSlots {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_spell_slots",
			"message": fmt.Sprintf("%s has no level %d spell slots remaining!", casterName, req.SlotLevel),
		})
		return
	}

	// Get spellcasting ability modifier
	classKey := strings.ToLower(class)
	spellMod := 0
	spellAbility := ""
	if c, ok := srdClasses[classKey]; ok {
		spellAbility = strings.ToLower(c.Spellcasting)
		switch c.Spellcasting {
		case "INT":
			spellMod = game.Modifier(intl)
		case "WIS":
			spellMod = game.Modifier(wis)
		case "CHA":
			spellMod = game.Modifier(cha)
		}
	}

	// Use the spell slot: the guarded spend (resources_2024.go)
	if _, _, err := changeSpent(req.CasterID, func(s *spent) error {
		return spendSlotIn(s.Slots, req.SlotLevel, totalSlots, fmt.Sprintf("%s has no level %d spell slots remaining!", casterName, req.SlotLevel))
	}); err != nil {
		if r, refused := refusedSpend(err); refused {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_spell_slots", "message": r.Msg})
			return
		}
		answerDBFailure(w, err, "the caster")
		return
	}

	// Determine success
	success := false
	roll := 0
	totalCheck := 0
	checkNote := "" // why the check had Advantage or Disadvantage
	dc := 10 + req.TargetSpellLevel
	autoSuccess := req.SlotLevel >= req.TargetSpellLevel

	if autoSuccess {
		success = true
	} else {
		// Roll d20 + spellcasting modifier vs DC
		var why string
		totalCheck, roll, why = spellcastingCheck(lobbyID, req.CasterID, spellAbility, spellMod) // check_2024.go
		checkNote = why
		success = totalCheck >= dc
	}

	// What effect are we dispelling?
	effectDispelled := ""
	if req.EffectName != "" {
		effectDispelled = req.EffectName
	} else if concentratingOn.String != "" {
		effectDispelled = concentratingOn.String
	} else {
		effectDispelled = fmt.Sprintf("level %d spell effect", req.TargetSpellLevel)
	}

	// If successful, end the spell effect
	if success {
		// Clear concentration if that's what we're dispelling
		if concentratingOn.String != "" && (req.EffectName == "" || strings.EqualFold(req.EffectName, concentratingOn.String)) {
			db.Exec("UPDATE characters SET concentrating_on = NULL WHERE id = $1", req.TargetID)
		}
	}

	// Build response
	response := map[string]interface{}{
		"success":             true, // API call succeeded
		"dispel_success":      success,
		"caster":              casterName,
		"caster_id":           req.CasterID,
		"target":              targetName,
		"target_id":           req.TargetID,
		"slot_level_used":     req.SlotLevel,
		"target_spell_level":  req.TargetSpellLevel,
		"effect_targeted":     effectDispelled,
		"spell_slot_consumed": true,
		"slots_remaining":     totalSlots - used[usedKey],
	}

	var actionResult string
	if autoSuccess {
		response["auto_success"] = true
		response["message"] = fmt.Sprintf("✨ %s casts Dispel Magic at level %d on %s! The %s (level %d) is automatically dispelled!",
			casterName, req.SlotLevel, targetName, effectDispelled, req.TargetSpellLevel)
		actionResult = fmt.Sprintf("Dispel Magic (level %d) vs %s (level %d): AUTO SUCCESS - effect ended",
			req.SlotLevel, effectDispelled, req.TargetSpellLevel)
		response["effect_ended"] = true
	} else {
		response["ability_check_required"] = true
		response["dc"] = dc
		response["roll"] = roll
		response["spellcasting_modifier"] = spellMod
		response["total_check"] = totalCheck

		if success {
			response["message"] = fmt.Sprintf("✨ %s casts Dispel Magic at level %d on %s! Ability check: %d + %d = %d vs DC %d - SUCCESS! The %s is dispelled!",
				casterName, req.SlotLevel, targetName, roll, spellMod, totalCheck, dc, effectDispelled)
			actionResult = fmt.Sprintf("Dispel Magic (level %d) vs %s (level %d): %d + %d = %d vs DC %d - SUCCESS!",
				req.SlotLevel, effectDispelled, req.TargetSpellLevel, roll, spellMod, totalCheck, dc)
			response["effect_ended"] = true
		} else {
			response["message"] = fmt.Sprintf("💫 %s casts Dispel Magic at level %d on %s! Ability check: %d + %d = %d vs DC %d - FAILED! The %s persists!",
				casterName, req.SlotLevel, targetName, roll, spellMod, totalCheck, dc, effectDispelled)
			actionResult = fmt.Sprintf("Dispel Magic (level %d) vs %s (level %d): %d + %d = %d vs DC %d - FAILED!",
				req.SlotLevel, effectDispelled, req.TargetSpellLevel, roll, spellMod, totalCheck, dc)
			response["effect_ended"] = false
		}
		response["message"] = fmt.Sprint(response["message"]) + checkNote
		actionResult += checkNote
	}

	// the spells others cast on the target (Mage Armor, Barkskin, Bless, Haste ...): each ends at the slot's level or lower, else
	// with the ability check (F76)
	if effLines, effAny := dispelSpellEffects(lobbyID, req.TargetID, req.CasterID, req.SlotLevel, spellMod, spellAbility, req.EffectName); len(effLines) > 0 || effectsOnly {
		response["spells_on_target"] = effLines
		if effectsOnly {
			delete(response, "auto_success")
			delete(response, "target_spell_level")
			response["dispel_success"], response["effect_ended"] = effAny, effAny
			response["message"] = fmt.Sprintf("✨ %s casts Dispel Magic at level %d on %s: %s.", casterName, req.SlotLevel, targetName, strings.Join(effLines, "; "))
			actionResult = fmt.Sprint(response["message"])
			effectDispelled = "the spells on it"
		} else {
			response["message"] = fmt.Sprint(response["message"]) + " Spells on " + targetName + ": " + strings.Join(effLines, "; ") + "."
		}
	}

	// Log the action
	actionDesc := fmt.Sprintf("%s casts Dispel Magic on %s targeting %s (level %d)", casterName, targetName, effectDispelled, req.TargetSpellLevel)
	writeAction(db, lobbyID, req.CasterID, "dispel_magic", actionDesc, actionResult)

	json.NewEncoder(w).Encode(response)
}
