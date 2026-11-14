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
// @Param request body object{spell_slug=string,caster_id=int,target_ids=[]int,dc=int,ritual=bool} true "AoE cast details"
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

	// Find campaign where this agent is the DM
	var campaignID int
	err = gmCampaignScan(agentID, r, &campaignID)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_gm", "message": publicError(err)})
		return
	}

	var req struct {
		SpellSlug     string `json:"spell_slug"`
		CasterID      int    `json:"caster_id"`
		TargetIDs     []int  `json:"target_ids"`
		DC            int    `json:"dc"`
		Ritual        bool   `json:"ritual"`
		SlotLevel     int    `json:"slot_level"`     // For upcasting
		SculptTargets []int  `json:"sculpt_targets"` // Evocation Wizard's Sculpt Spells - allies to protect
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
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
			var usedJSON []byte
			db.QueryRow("SELECT COALESCE(spell_slots_used, '{}') FROM characters WHERE id = $1", req.CasterID).Scan(&usedJSON)
			var used map[string]int
			json.Unmarshal(usedJSON, &used)

			usedKey := fmt.Sprintf("%d", slotLevel)
			usedSlots := used[usedKey]
			if usedSlots >= totalSlots {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "no_spell_slots",
					"message": fmt.Sprintf("No level %d spell slots remaining", slotLevel),
				})
				return
			}

			used[usedKey] = usedSlots + 1
			updatedJSON, _ := json.Marshal(used)
			db.Exec("UPDATE characters SET spell_slots_used = $1 WHERE id = $2", updatedJSON, req.CasterID)
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
	// Check if caster has Empowered Evocation and spell is evocation school
	empoweredEvocationBonus := 0
	hasSculptSpells := false
	maxSculptTargets := 0
	isEvocationSpell := strings.ToLower(spellSchool) == "evocation"

	if req.CasterID > 0 && isEvocationSpell {
		// Get caster's subclass and level
		var subclass sql.NullString
		var casterLevel, casterInt int
		db.QueryRow(`SELECT COALESCE(subclass, ''), level, intl FROM characters WHERE id = $1`, req.CasterID).Scan(&subclass, &casterLevel, &casterInt)

		if subclass.Valid && subclass.String != "" {
			// Check for Empowered Evocation (level 10 Evocation Wizard feature)
			if hasSubclassFeature(subclass.String, casterLevel, "empowered_evocation") {
				empoweredEvocationBonus = game.Modifier(casterInt)
				baseDamage += empoweredEvocationBonus
			}

			// Check for Sculpt Spells (level 2 Evocation Wizard feature)
			if hasSubclassFeature(subclass.String, casterLevel, "sculpt_spells") {
				hasSculptSpells = true
				maxSculptTargets = 1 + actualSlotLevel // 1 + spell's level
			}
		}
	}

	// Check for Elemental Affinity (Draconic Sorcerer level 6+)
	// When casting a spell that deals damage matching dragon ancestry, add CHA mod to damage
	elementalAffinityBonus := 0
	elementalAffinityAncestry := ""
	if req.CasterID > 0 && damageType != "" {
		var subclass sql.NullString
		var casterLevel, casterCha int
		db.QueryRow(`SELECT COALESCE(subclass, ''), level, cha FROM characters WHERE id = $1`, req.CasterID).Scan(&subclass, &casterLevel, &casterCha)

		if subclass.Valid && subclass.String == "draconic" {
			if hasSubclassFeature(subclass.String, casterLevel, "elemental_affinity") {
				ancestryDamageType := getDragonAncestryDamageType(req.CasterID)
				if ancestryDamageType != "" && strings.ToLower(damageType) == ancestryDamageType {
					elementalAffinityBonus = game.Modifier(casterCha)
					elementalAffinityAncestry = ancestryDamageType
					baseDamage += elementalAffinityBonus
				}
			}
		}
	}

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
			if targetLobbyID != campaignID {
				results = append(results, map[string]interface{}{
					"target_id": targetID,
					"error":     "target_not_in_campaign",
				})
				continue
			}
		} else {
			// Monster in combat - get from turn order
			targetName = fmt.Sprintf("Monster %d", -targetID)
		}

		// Get target's save modifier
		saveMod := 0
		// Track aura for each target
		targetAuraBonus := 0
		targetAuraPaladin := ""

		if targetID > 0 && savingThrow != "" {
			var str, dex, con, intl, wis, cha, targetLevel int
			var targetClass string
			var targetClassLevelsJSON []byte
			db.QueryRow(`SELECT str, dex, con, intl, wis, cha, level, class, COALESCE(class_levels, '{}')::jsonb 
				FROM characters WHERE id = $1`, targetID).Scan(&str, &dex, &con, &intl, &wis, &cha, &targetLevel, &targetClass, &targetClassLevelsJSON)
			switch strings.ToUpper(savingThrow) {
			case "STR":
				saveMod = game.Modifier(str)
			case "DEX":
				saveMod = game.Modifier(dex)
			case "CON":
				saveMod = game.Modifier(con)
			case "INT":
				saveMod = game.Modifier(intl)
			case "WIS":
				saveMod = game.Modifier(wis)
			case "CHA":
				saveMod = game.Modifier(cha)
			}

			// Paladin's Aura of Protection for AoE saves
			// Check if target is a Paladin 6+ (benefits from own aura)
			targetPaladinLevel := 0
			var targetClassLevels map[string]int
			if err := json.Unmarshal(targetClassLevelsJSON, &targetClassLevels); err == nil && len(targetClassLevels) > 0 {
				for cn, lvl := range targetClassLevels {
					if strings.ToLower(cn) == "paladin" {
						targetPaladinLevel = lvl
						break
					}
				}
			} else if strings.ToLower(targetClass) == "paladin" {
				targetPaladinLevel = targetLevel
			}

			if targetPaladinLevel >= 6 && !isIncapacitated(targetID) {
				selfCha := game.Modifier(cha)
				if selfCha < 1 {
					selfCha = 1
				}
				targetAuraBonus = selfCha
				targetAuraPaladin = targetName + " (self)"
			}

			// Check for other Paladins in the party
			otherAura, otherPaladin := getPaladinAuraBonus(campaignID, targetID)
			if otherAura > targetAuraBonus {
				targetAuraBonus = otherAura
				targetAuraPaladin = otherPaladin
			}

			if targetAuraBonus > 0 {
				saveMod += targetAuraBonus
			}
		}

		// Roll saving throw
		// Gnomish Cunning - Advantage on Intelligence, Wisdom and Charisma saves
		gnomeCunningAoE := false
		saveRoll := game.RollDie(20)
		if targetID > 0 && checkGnomeCunning(targetID, strings.ToLower(savingThrow), true) {
			// Roll with advantage
			roll2 := game.RollDie(20)
			gnomeCunningAoE = true
			if roll2 > saveRoll {
				saveRoll = roll2
			}
		}
		saveTotal := saveRoll + saveMod
		saved := saveTotal >= dc
		sculptSpellsApplied := false

		// Sculpt Spells: Protected targets auto-succeed and take no damage
		if sculptTargetSet[targetID] {
			saved = true
			sculptSpellsApplied = true
		}

		// Calculate damage (half on save for most AoE spells)
		// Check for Evasion (Monk 7+, Rogue 7+) - DEX saves only
		// Potent Cantrip - cantrips do half damage on successful save
		damage := baseDamage
		evasionApplied := false
		potentCantripApplied := false
		isDEXSave := strings.ToUpper(savingThrow) == "DEX"
		targetHasEvasion := targetID > 0 && isDEXSave && hasEvasion(targetID)

		if saved {
			if sculptSpellsApplied {
				damage = 0 // Sculpt Spells: no damage on auto-succeed
			} else if targetHasEvasion {
				damage = 0 // Evasion: no damage on successful DEX save
				evasionApplied = true
			} else if spellLevel == 0 {
				// Cantrips: normally 0 damage on save, but Potent Cantrip gives half
				if hasPotentCantrip {
					damage = baseDamage / 2
					potentCantripApplied = true
				} else {
					damage = 0 // Cantrips do no damage on successful save
				}
			} else {
				damage = baseDamage / 2 // Most AoE spells do half on save
			}
		} else if targetHasEvasion {
			// Evasion: half damage on failed DEX save (instead of full)
			damage = baseDamage / 2
			evasionApplied = true
		}

		result := map[string]interface{}{
			"target_id":   targetID,
			"target_name": targetName,
			"save_roll":   saveRoll,
			"save_mod":    saveMod,
			"save_total":  saveTotal,
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

		// Add Gnomish Cunning info to result
		if gnomeCunningAoE {
			result["gnome_cunning"] = true
			result["gnomish_cunning_info"] = "🧠 Gnomish Cunning: rolled with Advantage"
		}

		// Add Paladin's Aura of Protection info to result
		if targetAuraBonus > 0 {
			result["aura_of_protection"] = true
			result["aura_bonus"] = targetAuraBonus
			result["aura_source"] = targetAuraPaladin
			result["paladin_aura_info"] = fmt.Sprintf("🛡️ Aura of Protection: +%d to save from %s", targetAuraBonus, targetAuraPaladin)
		}

		// Apply damage to characters or monsters
		if damage > 0 {
			if targetID > 0 {
				// Character - apply condition-based resistance
				dmgMod := applyDamageResistance(targetID, damage, damageType)
				if dmgMod.WasHalved || dmgMod.WasNegated {
					damage = dmgMod.FinalDamage
					result["resistances_applied"] = dmgMod.Resistances
					if dmgMod.WasNegated {
						result["immunities_applied"] = dmgMod.Immunities
					}
				}

				newHP := targetHP - damage
				if newHP < 0 {
					newHP = 0
				}

				var charMaxHP int
				db.QueryRow("SELECT max_hp FROM characters WHERE id = $1", targetID).Scan(&charMaxHP)

				// Check Barbarian Relentless Rage first (requires CON save)
				if newHP == 0 {
					relentlessHP, relentlessUsed, relentlessMsg := checkRelentlessRage(targetID, targetHP, damage, charMaxHP)
					if relentlessUsed {
						newHP = relentlessHP
						result["relentless_rage"] = true
						result["class_feature_note"] = relentlessMsg
					} else if relentlessMsg != "" {
						// Save failed, add the message
						result["relentless_rage_failed"] = true
						result["class_feature_note"] = relentlessMsg
					}
				}

				// Check Orc Relentless Endurance (automatic, no save)
				if newHP == 0 {
					relentlessHP, relentlessUsed, relentlessMsg := checkRelentlessEndurance(targetID, targetHP, damage, charMaxHP)
					if relentlessUsed {
						newHP = relentlessHP
						result["relentless_endurance"] = true
						result["racial_feature_note"] = relentlessMsg
					}
				}

				db.Exec(`UPDATE characters SET hp = $1 WHERE id = $2`, newHP, targetID)
				result["hp_before"] = targetHP
				result["hp_after"] = newHP
				result["damage"] = damage // Update with resisted damage
				totalDamageDealt += damage

				// Check for kill effects when target drops to 0 HP
				if newHP == 0 && req.CasterID > 0 {
					if killEffects := applyKillEffects(req.CasterID); killEffects != nil {
						result["kill_effects"] = killEffects
					}
				}
			} else {
				// Monster in combat - apply monster damage resistance
				// Get monster_key from turn_order
				var turnOrderJSON []byte
				db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1`, campaignID).Scan(&turnOrderJSON)
				if turnOrderJSON != nil {
					type CombatEntry struct {
						ID         int    `json:"id"`
						Name       string `json:"name"`
						MonsterKey string `json:"monster_key"`
						HP         int    `json:"hp"`
						MaxHP      int    `json:"max_hp"`
					}
					var entries []CombatEntry
					json.Unmarshal(turnOrderJSON, &entries)

					for i, e := range entries {
						if e.ID == targetID {
							targetName = e.Name
							targetHP = e.HP
							targetMaxHP = e.MaxHP

							// Apply monster damage resistance (spells are always magical)
							if e.MonsterKey != "" && damageType != "" {
								dmgMod := applyMonsterDamageResistance(e.MonsterKey, damage, damageType, true, false)
								if dmgMod.WasNegated {
									damage = 0
									result["immunities_applied"] = dmgMod.Immunities
								} else if dmgMod.WasDoubled {
									damage = dmgMod.FinalDamage
									result["vulnerabilities_applied"] = dmgMod.Vulnerabilities
								} else if dmgMod.WasHalved {
									damage = dmgMod.FinalDamage
									result["resistances_applied"] = dmgMod.Resistances
								}
							}

							newHP := e.HP - damage
							if newHP < 0 {
								newHP = 0
							}
							entries[i].HP = newHP

							// Update turn_order with new HP
							updatedJSON, _ := json.Marshal(entries)
							db.Exec(`UPDATE combat_state SET turn_order = $1 WHERE lobby_id = $2`, updatedJSON, campaignID)

							result["target_name"] = e.Name
							result["hp_before"] = e.HP
							result["hp_after"] = newHP
							result["damage"] = damage
							totalDamageDealt += damage

							// Check for kill effects when monster drops to 0 HP
							if newHP == 0 && req.CasterID > 0 {
								if killEffects := applyKillEffects(req.CasterID); killEffects != nil {
									result["kill_effects"] = killEffects
								}
							}
							break
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

	db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'aoe_cast', $3, $4)`,
		campaignID, req.CasterID, actionDesc, resultStr)

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
// @Param Authorization header string true "Basic auth"
// @Param request body object{caster_id=integer,target_id=integer,target_spell_level=integer,slot_level=integer,effect_name=string} true "Dispel Magic details: target_id is the character/monster affected, target_spell_level required (or auto-detected from concentration), slot_level defaults to 3, effect_name optional"
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
		TargetSpellLevel int    `json:"target_spell_level"`
		SlotLevel        int    `json:"slot_level"`
		EffectName       string `json:"effect_name"` // Optional: name of effect to dispel
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
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

	// Verify agent is DM of the caster's campaign
	var lobbyID, dmID int
	err = db.QueryRow(`
		SELECT c.lobby_id, l.dm_id FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.id = $1
	`, req.CasterID).Scan(&lobbyID, &dmID)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Caster character %d not found", req.CasterID),
		})
		return
	}

	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": "You are not the GM of this character's campaign",
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

	// Use the spell slot
	used[usedKey] = usedSlots + 1
	updatedJSON, _ := json.Marshal(used)
	db.Exec("UPDATE characters SET spell_slots_used = $1 WHERE id = $2", updatedJSON, req.CasterID)

	// Determine success
	success := false
	roll := 0
	totalCheck := 0
	dc := 10 + req.TargetSpellLevel
	autoSuccess := req.SlotLevel >= req.TargetSpellLevel

	if autoSuccess {
		success = true
	} else {
		// Roll d20 + spellcasting modifier vs DC
		roll = game.RollDie(20)
		totalCheck = roll + spellMod
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
	}

	// the spells others cast on the target (Mage Armor, Barkskin, Bless, Haste ...): each ends at the slot's level or lower, else
	// with the ability check (F76)
	if effLines, effAny := dispelSpellEffects(lobbyID, req.TargetID, req.SlotLevel, spellMod, req.EffectName); len(effLines) > 0 || effectsOnly {
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
	db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, $3, $4, $5)
	`, lobbyID, req.CasterID, "dispel_magic", actionDesc, actionResult)

	json.NewEncoder(w).Encode(response)
}
