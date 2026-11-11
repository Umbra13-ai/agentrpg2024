package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"strconv"
	"strings"
)

// handleCharacterAttune godoc
// @Summary Attune or unattune magic items
// @Description Manage magic item attunement for a character. Max 3 attuned items per 5e rules.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,action=string,item_name=string} true "Attunement action (attune/unattune)"
// @Success 200 {object} map[string]interface{} "Attunement result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request or max attunement reached"
// @Router /characters/attune [post]
func handleCharacterAttune(w http.ResponseWriter, r *http.Request) {
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
		CharacterID int    `json:"character_id"`
		Action      string `json:"action"` // "attune" or "unattune"
		ItemName    string `json:"item_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 || req.ItemName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id and item_name required"})
		return
	}

	if req.Action == "" {
		req.Action = "attune"
	}

	// Verify ownership
	var charAgentID int
	var charName string
	err = db.QueryRow(`SELECT agent_id, name FROM characters WHERE id = $1`, req.CharacterID).Scan(&charAgentID, &charName)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}
	if charAgentID != agentID {
		// Check if requester is GM
		var lobbyID, dmID int
		db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, req.CharacterID).Scan(&lobbyID)
		db.QueryRow(`SELECT dm_id FROM lobbies WHERE id = $1`, lobbyID).Scan(&dmID)
		if dmID != agentID {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
			return
		}
	}

	// Get current attuned items
	var attunedJSON []byte
	db.QueryRow(`SELECT COALESCE(attuned_items, '[]') FROM characters WHERE id = $1`, req.CharacterID).Scan(&attunedJSON)
	var attunedItems []string
	json.Unmarshal(attunedJSON, &attunedItems)

	maxAttunement := attunementMax(req.CharacterID)

	if req.Action == "attune" {
		// Check if already attuned
		for _, item := range attunedItems {
			if strings.EqualFold(item, req.ItemName) {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "already_attuned",
					"message": fmt.Sprintf("%s is already attuned to %s", charName, req.ItemName),
				})
				return
			}
		}

		if attunementNotNeeded(req.ItemName) { // F117: an item that doesn't require attunement took a slot
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "attunement_not_needed", "message": req.ItemName + " doesn't require attunement: it works without it (worn or held as its text says). No slot was used."})
			return
		}
		// "Requires Attunement by a Paladin" and the like (SRD 5.2.1): only those creatures can attune to the item
		if why := attunementRestriction(req.CharacterID, req.ItemName); why != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "attunement_restricted", "message": why})
			return
		}

		// Check max attunement
		if len(attunedItems) >= maxAttunement {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":         "max_attunement_reached",
				"message":       fmt.Sprintf("%s already has %d items attuned (max %d). Unattune an item first.", charName, len(attunedItems), maxAttunement),
				"attuned_items": attunedItems,
			})
			return
		}

		// Attune the item
		attunedItems = append(attunedItems, req.ItemName)
		updatedJSON, _ := json.Marshal(attunedItems)
		db.Exec(`UPDATE characters SET attuned_items = $1 WHERE id = $2`, updatedJSON, req.CharacterID)
		scoreNote := applyAttunementScore(req.CharacterID, req.ItemName, true) // Belt of Giant Strength, Amulet of Health ... (magic_items_2024.go)
		scoreNote += refreshMightOfGiants(req.CharacterID)                     // Hammer of Thunderbolts: Giant's Bane (legendary_more_2024.go)
		scoreNote += attuneCurse(req.CharacterID, req.ItemName)                // Shield of Missile Attraction (missile_attraction_2024.go, F222)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         true,
			"action":          "attuned",
			"character":       charName,
			"item":            req.ItemName,
			"note":            strings.TrimSpace(scoreNote),
			"attuned_items":   attunedItems,
			"slots_remaining": maxAttunement - len(attunedItems),
		})

	} else if req.Action == "unattune" {
		// Find and remove the item
		found := false
		newAttuned := []string{}
		for _, item := range attunedItems {
			if strings.EqualFold(item, req.ItemName) {
				found = true
			} else {
				newAttuned = append(newAttuned, item)
			}
		}

		if !found {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":         "not_attuned",
				"message":       fmt.Sprintf("%s is not attuned to %s", charName, req.ItemName),
				"attuned_items": attunedItems,
			})
			return
		}

		updatedJSON, _ := json.Marshal(newAttuned)
		db.Exec(`UPDATE characters SET attuned_items = $1 WHERE id = $2`, updatedJSON, req.CharacterID)
		applyAttunementScore(req.CharacterID, req.ItemName, false)
		refreshMightOfGiants(req.CharacterID)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         true,
			"action":          "unattuned",
			"character":       charName,
			"item":            req.ItemName,
			"attuned_items":   newAttuned,
			"slots_remaining": maxAttunement - len(newAttuned),
		})

	} else {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_action", "valid_actions": []string{"attune", "unattune"}})
	}
}

// handleCharacterEncumbrance godoc
// @Summary Calculate character encumbrance
// @Description Calculate equipment weight and encumbrance status based on STR score.
// @Tags Characters
// @Produce json
// @Security BasicAuth
// @Param character_id query int true "Character ID"
// @Success 200 {object} map[string]interface{} "Encumbrance calculation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/encumbrance [get]
func handleCharacterEncumbrance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	_, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	characterID, _ := strconv.Atoi(r.URL.Query().Get("character_id"))
	if characterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	// Get character's STR and inventory
	var charName string
	var str int
	var inventoryJSON []byte
	var species string
	var charLobby int
	err = db.QueryRow(`SELECT name, str, COALESCE(inventory, '[]'), COALESCE(species, ''), COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, characterID).Scan(&charName, &str, &inventoryJSON, &species, &charLobby)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	var inventory []map[string]interface{}
	json.Unmarshal(inventoryJSON, &inventory)
	inventory = usableInventory(inventory) // a lost bag is not carried

	// Calculate total weight from inventory
	totalWeight := 0.0
	itemWeights := []map[string]interface{}{}

	for _, item := range inventory {
		if inExtradimensional(item, inventory) {
			continue // inside a Bag of Holding, Efficient Quiver...: no weight (F159)
		}
		itemName, _ := item["name"].(string)
		quantity := 1
		if q, ok := item["quantity"].(float64); ok {
			quantity = int(q)
		}

		// Use the item's own weight, else the SRD weight, else look it up in the database
		weight := itemUnitWeight(item)
		if weight <= 0 {
			// Try to look up weight from SRD
			itemSlug := strings.ToLower(strings.ReplaceAll(itemName, " ", "-"))

			// Check weapons
			var dbWeight float64
			err := db.QueryRow(`SELECT COALESCE(weight, 0) FROM weapons WHERE slug = $1`, itemSlug).Scan(&dbWeight)
			if err == nil && dbWeight > 0 {
				weight = dbWeight
			} else {
				// Check armor
				err = db.QueryRow(`SELECT COALESCE(weight, 0) FROM armor WHERE slug = $1`, itemSlug).Scan(&dbWeight)
				if err == nil && dbWeight > 0 {
					weight = dbWeight
				}
			}
		}

		itemTotalWeight := weight * float64(quantity)
		totalWeight += itemTotalWeight

		if weight > 0 {
			itemWeights = append(itemWeights, map[string]interface{}{
				"name":     itemName,
				"quantity": quantity,
				"weight":   weight,
				"total":    itemTotalWeight,
			})
		}
	}

	// 2024 Carrying Capacity (SRD 5.2.1 Rules Glossary): Strength × 15 lb for a Small or Medium creature (Tiny × 7.5, Large × 30,
	// Huge × 60, Gargantuan × 120); drag, lift or push twice that. Carrying more than the capacity limits Speed to 5 feet.
	mult := map[string]float64{"tiny": 7.5, "small": 15, "medium": 15, "large": 30, "huge": 60, "gargantuan": 120}[strings.ToLower(powerfulBuildSize(species, getSpeciesSize(species)))] // Powerful Build (F96)
	if mult == 0 {
		mult = 15
	}
	carryingCapacity := float64(str) * mult
	status := "within_capacity"
	speedNote := ""
	if totalWeight > carryingCapacity {
		status = "over_capacity"
		speedNote = "Carrying more than its capacity: Speed can be no more than 5 feet."
	}
	out := map[string]interface{}{
		"character":         charName,
		"strength":          str,
		"total_weight":      totalWeight,
		"carrying_capacity": carryingCapacity,
		"drag_lift_push":    carryingCapacity * 2,
		"status":            status,
		"item_weights":      itemWeights,
		"rules_note":        "Carrying Capacity (SRD 5.2.1): Strength x 15 lb for Small or Medium (Tiny x 7.5, Large x 30, Huge x 60, Gargantuan x 120); drag, lift or push twice that. More than the capacity: Speed at most 5 feet.",
	}
	if speedNote != "" {
		out["speed_note"] = speedNote
	}
	if tableRule(charLobby, "encumbrance") == "ignored" {
		out["status"] = "ignored"
		out["table_rule"] = "This campaign's table rules ignore encumbrance unless a player tries to exploit it."
		delete(out, "speed_note")
	}
	json.NewEncoder(w).Encode(out)
}

// handleCharacterEquipArmor godoc
// @Summary Equip armor or shield
// @Description Equip armor and/or a shield; AC updates automatically. armor is a slug ("plate-armor") or a magic armor's name: "Plate Armor +1", "Armor, +2 (Plate)", "Elven Chain", "Dwarven Plate", "Glamoured Studded Leather", "Dragon Scale Mail", "Mithral Armor (Half Plate)", "Armor of Resistance (Plate, Fire)" (its base armor and bonus are read from the name). "none" takes armor off (for example a Shield with Unarmored Defense). A Thief of level 13 can attune to four items (POST /characters/attune); everyone else to three.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,armor=string,shield=bool} true "Armor to equip"
// @Success 200 {object} map[string]interface{} "Updated AC and equipment status"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/equip-armor [post]
func handleCharacterEquipArmor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		Armor       string `json:"armor"`  // Armor slug (e.g., "chain-mail", "leather")
		Shield      *bool  `json:"shield"` // Optional: equip/unequip shield
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	// Verify character belongs to agent
	var charName string
	var ownerID, charStr, charDex, charLevel int
	var armorProfs string
	var currentArmor, charSubclass sql.NullString
	var currentShield bool
	err = db.QueryRow(`SELECT name, agent_id, str, dex, armor_proficiencies, equipped_armor, COALESCE(equipped_shield, false), level, subclass 
		FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName, &ownerID, &charStr, &charDex, &armorProfs, &currentArmor, &currentShield, &charLevel, &charSubclass)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agent {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	// Check if in combat (donning/doffing time)
	_, inCombat, _ := isCharacterInCombat(req.CharacterID)

	newArmor := currentArmor.String
	newShield := currentShield
	warnings := []string{}
	timeInfo := map[string]interface{}{}

	// "none" means no armor (for example a Barbarian with Unarmored Defense and a Shield)
	if a := strings.ToLower(strings.TrimSpace(req.Armor)); a == "none" || a == "no armor" {
		if inCombat && currentArmor.String != "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "cannot_change_armor_in_combat", "reason": "Doffing armor takes minutes. You cannot change armor during combat."})
			return
		}
		req.Armor = ""
		newArmor = ""
	}

	// Handle armor change
	if req.Armor != "" {
		// Validate armor exists (magic armor names resolve to their base armor: getArmorInfo)
		armorInfo, err := getArmorInfo(req.Armor)
		if err != nil || armorInfo == nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "armor_not_found", "armor": req.Armor})
			return
		}

		// Block armor changes during combat
		if inCombat {
			w.WriteHeader(http.StatusBadRequest)
			donTime, _ := game.ArmorDonDoffTime(armorInfo.Type)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":      "cannot_change_armor_in_combat",
				"reason":     fmt.Sprintf("Donning %s armor takes %d minutes. You cannot change armor during combat.", armorInfo.Type, donTime),
				"suggestion": "Wait until combat ends, or use shield changes only (1 action to don/doff shield).",
			})
			return
		}

		// Check proficiency
		if !isArmorProficient(armorProfs, armorInfo.Type) {
			warnings = append(warnings, "not_proficient_with_"+armorInfo.Type+"_armor")
		}

		// Check strength requirement
		if armorInfo.StrengthRequirement > 0 && charStr < armorInfo.StrengthRequirement {
			warnings = append(warnings, fmt.Sprintf("strength_requirement_not_met_need_%d_have_%d_speed_reduced_10", armorInfo.StrengthRequirement, charStr))
		}

		// Check stealth disadvantage
		if armorInfo.StealthDisadvantage {
			warnings = append(warnings, "stealth_disadvantage")
		}

		// Calculate donning time
		donTime, doffTime := game.ArmorDonDoffTime(armorInfo.Type)
		timeInfo["armor_type"] = armorInfo.Type
		timeInfo["don_time_minutes"] = donTime
		timeInfo["doff_time_minutes"] = doffTime

		// If changing FROM existing armor, need to account for doff time too
		if currentArmor.Valid && currentArmor.String != "" && currentArmor.String != req.Armor {
			oldArmor, _ := getArmorInfo(currentArmor.String)
			if oldArmor != nil {
				_, oldDoffTime := game.ArmorDonDoffTime(oldArmor.Type)
				timeInfo["total_time_minutes"] = oldDoffTime + donTime
				timeInfo["note"] = fmt.Sprintf("Doffing %s (%d min) + donning %s (%d min)", oldArmor.Type, oldDoffTime, armorInfo.Type, donTime)
			}
		}

		newArmor = req.Armor
	}

	// Handle shield change
	if req.Shield != nil {
		newShield = *req.Shield
		if newShield && !currentShield {
			if mh, oh := equippedWeapons(req.CharacterID); mh != "" || oh != "" {
				if ok, why := handsAllow(mh, oh, true); !ok {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]interface{}{"error": "hands_full", "message": why})
					return
				}
			}
		}
		if newShield && !strings.Contains(strings.ToLower(armorProfs), "shield") {
			warnings = append(warnings, "not_proficient_with_shields")
		}

		// Shield is 1 action to don/doff - allowed in combat
		if inCombat {
			if newShield != currentShield {
				timeInfo["shield_change"] = "1 action"
				warnings = append(warnings, "shield_change_uses_action")
			}
		} else {
			timeInfo["shield_time"] = "1 action to don or doff"
		}
	}

	// Update database
	_, err = db.Exec(`UPDATE characters SET equipped_armor = $1, equipped_shield = $2 WHERE id = $3`,
		sql.NullString{String: newArmor, Valid: newArmor != ""},
		newShield,
		req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}
	if currentArmor.String != "" && newArmor == "" {
		markAllArmorNoAutoEquip(req.CharacterID) // "none": no armor the character carries is put on for the player (F214)
	} else if currentArmor.String != "" && !strings.EqualFold(currentArmor.String, newArmor) {
		markNoAutoEquip(req.CharacterID, currentArmor.String, true) // swapped out: not put back on for the player (F214)
	}
	if newArmor != "" {
		markNoAutoEquip(req.CharacterID, newArmor, false)
	}

	// Calculate new AC with natural AC base from subclass
	dexMod := game.Modifier(charDex)
	naturalACBase := naturalACBaseFor(req.CharacterID, charSubclass.String, charLevel) // Unarmored Defense, Draconic Resilience ...
	naturalACBase = robeOfArchmagiBase(req.CharacterID, newArmor, naturalACBase)
	naturalACBase = mageArmorBase(req.CharacterID, newArmor, naturalACBase) // Mage Armor; donning armor ends it (F65)
	newAC := calculateArmorACWithNatural(dexMod, newArmor, shieldCounts(req.CharacterID, newShield), naturalACBase)
	newAC += magicACBonus(req.CharacterID, newArmor, shieldCounts(req.CharacterID, newShield)) // magic armor, shields, rings, cloaks, staffs

	// Defense Fighting Style (+1 AC while wearing armor)
	equipDefenseBonus := 0
	if newArmor != "" && hasFightingStyle(req.CharacterID, "defense") {
		equipDefenseBonus = 1
		newAC += equipDefenseBonus
	}

	newAC = computeCharacterAC(req.CharacterID) // one AC calculation for everything: armor, Shield, unarmored bases, Mage Armor, Barkskin (F73)
	// Update stored AC
	db.Exec(`UPDATE characters SET ac = $1 WHERE id = $2`, newAC, req.CharacterID)

	response := map[string]interface{}{
		"success":         true,
		"character":       charName,
		"equipped_armor":  newArmor,
		"equipped_shield": newShield,
		"new_ac":          newAC,
	}
	if equipDefenseBonus > 0 {
		response["defense_style_bonus"] = equipDefenseBonus
	}
	if len(warnings) > 0 {
		response["warnings"] = warnings
	}
	if len(timeInfo) > 0 {
		response["time_required"] = timeInfo
	}

	json.NewEncoder(w).Encode(response)
}

// handleCharacterUnequipArmor godoc
// @Summary Unequip armor and/or shield
// @Description Remove equipped armor and/or shield: armor=true takes the armor off (not in combat), shield=true the Shield (an action in combat). Naming neither is refused (nothing_to_unequip). Without armor the AC is the unarmored one: 10 + DEX mod, or Unarmored Defense, Draconic Resilience, the Robe of the Archmagi or Mage Armor when better.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,armor=bool,shield=bool} true "What to unequip"
// @Success 200 {object} map[string]interface{} "Updated AC and equipment status"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/unequip-armor [post]
func handleCharacterUnequipArmor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CharacterID int  `json:"character_id"`
		Armor       bool `json:"armor"`  // Unequip armor
		Shield      bool `json:"shield"` // Unequip shield
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}
	if !req.Armor && !req.Shield { // F66: a request naming neither used to answer success and change nothing
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "nothing_to_unequip",
			"message": "Say what to take off: {\"armor\": true} for the armor, {\"shield\": true} for the Shield, or both."})
		return
	}

	// Verify character belongs to agent
	var charName string
	var ownerID, charDex, charLevel int
	var currentArmor, charSubclass sql.NullString
	var currentShield bool
	err = db.QueryRow(`SELECT name, agent_id, dex, equipped_armor, COALESCE(equipped_shield, false), level, subclass 
		FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName, &ownerID, &charDex, &currentArmor, &currentShield, &charLevel, &charSubclass)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agent {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	// Check if in combat (donning/doffing time)
	_, inCombat, _ := isCharacterInCombat(req.CharacterID)

	newArmor := currentArmor.String
	newShield := currentShield
	warnings := []string{}
	timeInfo := map[string]interface{}{}

	// Handle armor removal
	if req.Armor && currentArmor.Valid && currentArmor.String != "" {
		// Block armor removal during combat
		if inCombat {
			armorInfo, _ := getArmorInfo(currentArmor.String)
			armorType := "armor"
			doffTime := 1
			if armorInfo != nil {
				armorType = armorInfo.Type
				_, doffTime = game.ArmorDonDoffTime(armorInfo.Type)
			}
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":      "cannot_remove_armor_in_combat",
				"reason":     fmt.Sprintf("Doffing %s armor takes %d minutes. You cannot remove armor during combat.", armorType, doffTime),
				"suggestion": "Wait until combat ends, or remove shield only (1 action to doff shield).",
			})
			return
		}

		// Calculate doff time
		armorInfo, _ := getArmorInfo(currentArmor.String)
		if armorInfo != nil {
			_, doffTime := game.ArmorDonDoffTime(armorInfo.Type)
			timeInfo["armor_type"] = armorInfo.Type
			timeInfo["doff_time_minutes"] = doffTime
		}

		newArmor = ""
	}

	// Handle shield removal
	if req.Shield && currentShield {
		newShield = false

		// Shield is 1 action to doff - allowed in combat
		if inCombat {
			timeInfo["shield_change"] = "1 action"
			warnings = append(warnings, "shield_doff_uses_action")
		} else {
			timeInfo["shield_time"] = "1 action to doff"
		}
	}

	// Update database
	_, err = db.Exec(`UPDATE characters SET equipped_armor = $1, equipped_shield = $2 WHERE id = $3`,
		sql.NullString{String: newArmor, Valid: newArmor != ""},
		newShield,
		req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}
	if currentArmor.String != "" && newArmor == "" {
		markAllArmorNoAutoEquip(req.CharacterID) // taken off: no armor the character carries is put on for the player (F214)
	}

	// Calculate new AC with natural AC base from subclass
	dexMod := game.Modifier(charDex)
	naturalACBase := naturalACBaseFor(req.CharacterID, charSubclass.String, charLevel) // Unarmored Defense, Draconic Resilience ...
	naturalACBase = robeOfArchmagiBase(req.CharacterID, newArmor, naturalACBase)
	naturalACBase = mageArmorBase(req.CharacterID, newArmor, naturalACBase) // Mage Armor; donning armor ends it (F65)
	newAC := calculateArmorACWithNatural(dexMod, newArmor, shieldCounts(req.CharacterID, newShield), naturalACBase)
	newAC += magicACBonus(req.CharacterID, newArmor, shieldCounts(req.CharacterID, newShield)) // magic armor, shields, rings, cloaks, staffs

	// Defense Fighting Style (+1 AC while wearing armor)
	unequipDefenseBonus := 0
	if newArmor != "" && hasFightingStyle(req.CharacterID, "defense") {
		unequipDefenseBonus = 1
		newAC += unequipDefenseBonus
	}

	newAC = computeCharacterAC(req.CharacterID) // one AC calculation for everything: armor, Shield, unarmored bases, Mage Armor, Barkskin (F73)
	// Update stored AC
	db.Exec(`UPDATE characters SET ac = $1 WHERE id = $2`, newAC, req.CharacterID)

	response := map[string]interface{}{
		"success":         true,
		"character":       charName,
		"equipped_armor":  newArmor,
		"equipped_shield": newShield,
		"new_ac":          newAC,
	}
	if unequipDefenseBonus > 0 {
		response["defense_style_bonus"] = unequipDefenseBonus
	}
	if len(warnings) > 0 {
		response["warnings"] = warnings
	}
	if len(timeInfo) > 0 {
		response["time_required"] = timeInfo
	}

	json.NewEncoder(w).Encode(response)
}

// handleCharacterEquipWeapon godoc
// @Summary Equip a weapon from inventory
// @Description Equip a weapon to main_hand or off_hand slot. Two-handed weapons require main_hand and leave off_hand empty. Light weapons can be dual-wielded. Weapons must be in inventory to equip. A named magic weapon (Vorpal Sword, Holy Avenger, Luck Blade...) takes base_weapon: the weapon it is, from the choices its SRD entry allows (400 choose_base_weapon lists them; an item with one choice takes it). The choice is kept on the item; attacks use that weapon and the item's bonus.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,weapon=string,slot=string,base_weapon=string} true "Equip weapon. slot: main_hand (default) or off_hand. base_weapon: for a named magic weapon, e.g. longsword"
// @Success 200 {object} map[string]interface{} "Equipped weapon info"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/equip-weapon [post]
func handleCharacterEquipWeapon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		Weapon      string `json:"weapon"`      // Weapon slug or name (e.g., "longsword", "shortbow")
		Slot        string `json:"slot"`        // main_hand (default) or off_hand
		BaseWeapon  string `json:"base_weapon"` // a named magic weapon: which weapon it is ("greatsword" for a Vorpal Sword)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 || req.Weapon == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id and weapon required"})
		return
	}

	if req.Slot == "" {
		req.Slot = "main_hand"
	}
	if req.Slot != "main_hand" && req.Slot != "off_hand" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "slot must be main_hand or off_hand"})
		return
	}

	// Verify character belongs to agent
	var charName string
	var ownerID int
	var inventoryJSON []byte
	var weaponProfs string
	var mainHand, offHand sql.NullString
	err = db.QueryRow(`SELECT name, agent_id, COALESCE(inventory, '[]'), COALESCE(weapon_proficiencies, ''),
		equipped_main_hand, equipped_off_hand
		FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName, &ownerID, &inventoryJSON, &weaponProfs, &mainHand, &offHand)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agent {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	// Check if weapon is in inventory
	var inventory []map[string]interface{}
	json.Unmarshal(inventoryJSON, &inventory)

	weaponSlug := strings.ToLower(strings.ReplaceAll(req.Weapon, " ", "-"))
	foundWeapon := false
	var weaponInfo map[string]interface{}

	isLostItem := lostChecker(inventory)
	for _, item := range inventory {
		if isLostItem(item) {
			continue // in a lost bag
		}
		itemName, _ := item["name"].(string)
		itemSlug := strings.ToLower(strings.ReplaceAll(itemName, " ", "-"))
		if itemSlug == weaponSlug || strings.ToLower(itemName) == strings.ToLower(req.Weapon) {
			foundWeapon = true
			weaponInfo = item
			break
		}
	}

	// Also check weapons table for properties
	var dbWeaponName string
	var dbProperties sql.NullString
	var isTwoHanded, isLight bool
	err = db.QueryRow(`SELECT name, properties FROM weapons WHERE slug = $1`, weaponSlug).Scan(&dbWeaponName, &dbProperties)
	if err == nil {
		propsLower := strings.ToLower(dbProperties.String)
		isTwoHanded = strings.Contains(propsLower, "two-handed") || strings.Contains(propsLower, "2h")
		isLight = strings.Contains(propsLower, "light")
	}

	if !foundWeapon {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "weapon_not_in_inventory",
			"weapon":     req.Weapon,
			"suggestion": "Use /api/gm/give-item to add the weapon to inventory first.",
		})
		return
	}

	warnings := []string{}

	// A named magic weapon (Vorpal Sword, Holy Avenger): which weapon it is, kept on the item (named_weapons_2024.go, F49)
	profWeapon := req.Weapon
	if req.BaseWeapon == "" { // "Frost Brand (Scimitar)": the name already says which weapon it is (F173)
		if i, j := strings.Index(req.Weapon, "("), strings.LastIndex(req.Weapon, ")"); i > 0 && j > i {
			if _, ok := srdWeapons[strings.ToLower(strings.TrimSpace(req.Weapon[i+1:j]))]; ok {
				req.BaseWeapon = strings.ToLower(strings.TrimSpace(req.Weapon[i+1 : j]))
			}
		}
	}
	baseKey, baseChanged, baseRefusal := equipNamedWeapon(weaponInfo, req.BaseWeapon)
	if baseRefusal != "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "choose_base_weapon", "message": baseRefusal})
		return
	}
	if baseKey != "" {
		bw := srdWeapons[baseKey]
		isTwoHanded = containsProperty(bw.Properties, "two-handed")
		isLight = containsProperty(bw.Properties, "light")
		profWeapon = baseKey
	}

	// Check weapon proficiency
	if !isWeaponProficient(weaponProfs, profWeapon) {
		warnings = append(warnings, "not_proficient_no_proficiency_bonus_on_attacks")
	}

	// Handle two-handed weapons
	if isTwoHanded {
		if req.Slot == "off_hand" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "two_handed_weapon",
				"message": "Two-handed weapons must be equipped in main_hand and require both hands.",
			})
			return
		}
		// A two-handed weapon needs both hands: nothing else may be held
		if (offHand.Valid && offHand.String != "") || shieldInHand(req.CharacterID) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "hands_full",
				"message": "A two-handed weapon needs both hands, but your other hand is holding something (a weapon or a shield). Put it away or drop it first.",
			})
			return
		}
	}

	// Handle off-hand restrictions
	if req.Slot == "off_hand" {
		// Check if main hand has two-handed weapon
		if mainHand.Valid && mainHand.String != "" {
			var mainProps sql.NullString
			mainSlug := strings.ToLower(strings.ReplaceAll(mainHand.String, " ", "-"))
			db.QueryRow(`SELECT properties FROM weapons WHERE slug = $1`, mainSlug).Scan(&mainProps)
			if mainProps.Valid && (strings.Contains(strings.ToLower(mainProps.String), "two-handed") || strings.Contains(strings.ToLower(mainProps.String), "2h")) {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "main_hand_is_two_handed",
					"message": fmt.Sprintf("Cannot equip off-hand weapon while wielding two-handed %s.", mainHand.String),
				})
				return
			}
		}

		// Warn if the off-hand weapon isn't Light (the Light property's extra attack needs a Light weapon in each hand)
		if !isLight {
			warnings = append(warnings, "off_hand_not_light_no_extra_attack")
		}
	}

	// Hands, and one equip or unequip per turn in combat (it is not an action)
	{
		slotPrev := mainHand
		if req.Slot == "off_hand" {
			slotPrev = offHand
		}
		// the very same item only: "Longsword +3" is not the plain Longsword already in the hand (sameWeapon ignores the +N)
		sameItem := func(a, b string) bool {
			k := func(s string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "-")) }
			return k(a) == k(b)
		}
		if slotPrev.Valid && slotPrev.String != "" && sameItem(slotPrev.String, req.Weapon) {
			reply := map[string]interface{}{"success": true, "message": "That weapon is already equipped in that hand.", "weapon": req.Weapon}
			if baseChanged { // a named weapon already in hand gets its base weapon now (named_weapons_2024.go)
				weaponInfo["base_weapon"] = baseKey
				if b, err := json.Marshal(inventory); err == nil {
					db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, b, req.CharacterID)
				}
				reply["base_weapon"] = baseKey
				reply["message"] = "That weapon is already equipped in that hand; it is a " + srdWeapons[baseKey].Name + " now."
			}
			json.NewEncoder(w).Encode(reply)
			return
		}
		testMain, testOff := mainHand.String, offHand.String
		if req.Slot == "off_hand" {
			testOff = req.Weapon
		} else {
			testMain = req.Weapon
		}
		if ok, why := handsAllow(testMain, testOff, shieldInHand(req.CharacterID)); !ok {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "hands_full", "message": why})
			return
		}
		if _, fighting, _ := isCharacterInCombat(req.CharacterID); fighting {
			if ok, code := equipRule(slotPrev.Valid && slotPrev.String != "", weaponInteractionUsed(req.CharacterID)); !ok {
				msg := msgAlreadyUsed
				if code == "hand_not_empty" {
					msg = msgHandNotEmpty
				}
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": code, "message": msg})
				return
			}
			markWeaponInteraction(req.CharacterID)
		}
	}

	// Update the appropriate slot
	newMainHand := mainHand
	newOffHand := offHand
	if req.Slot == "main_hand" {
		newMainHand = sql.NullString{String: req.Weapon, Valid: true}
		if isTwoHanded {
			newOffHand = sql.NullString{Valid: false}
		}
	} else {
		newOffHand = sql.NullString{String: req.Weapon, Valid: true}
	}

	// Update database
	_, err = db.Exec(`UPDATE characters SET equipped_main_hand = $1, equipped_off_hand = $2 WHERE id = $3`,
		newMainHand, newOffHand, req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}
	if baseChanged { // a named weapon keeps its base weapon once it is equipped (not when the equip was refused)
		weaponInfo["base_weapon"] = baseKey
		if b, err := json.Marshal(inventory); err == nil {
			db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, b, req.CharacterID)
		}
	}
	syncHeldItems(req.CharacterID) // the equipped weapon is now held; the one it replaced is sheathed

	response := map[string]interface{}{
		"success":            true,
		"character":          charName,
		"equipped_main_hand": nullStringToInterface(newMainHand),
		"equipped_off_hand":  nullStringToInterface(newOffHand),
		"slot_equipped":      req.Slot,
		"weapon":             req.Weapon,
	}
	if weaponInfo != nil {
		response["weapon_info"] = weaponInfo
	}
	if isTwoHanded {
		response["two_handed"] = true
	}
	if isLight {
		response["light"] = true
	}
	if len(warnings) > 0 {
		response["warnings"] = warnings
	}

	json.NewEncoder(w).Encode(response)
}

// handleCharacterUnequipWeapon godoc
// @Summary Unequip weapon(s) from hands
// @Description Remove equipped weapon(s) from main_hand and/or off_hand. Weapons return to inventory. Use drop=true to drop on ground instead (for unconscious mechanic).
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,slot=string,drop=bool} true "Unequip options. slot: main_hand, off_hand, or both (default). drop: true to drop instead of returning to inventory"
// @Success 200 {object} map[string]interface{} "Unequipped weapon info"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/unequip-weapon [post]
func handleCharacterUnequipWeapon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	agent, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		Slot        string `json:"slot"` // main_hand, off_hand, or both (default)
		Drop        bool   `json:"drop"` // If true, drop weapon instead of returning to inventory
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	if req.Slot == "" {
		req.Slot = "both"
	}
	if req.Slot != "main_hand" && req.Slot != "off_hand" && req.Slot != "both" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "slot must be main_hand, off_hand, or both"})
		return
	}

	// Verify character belongs to agent
	var charName string
	var ownerID int
	var mainHand, offHand sql.NullString
	err = db.QueryRow(`SELECT name, agent_id, equipped_main_hand, equipped_off_hand
		FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName, &ownerID, &mainHand, &offHand)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agent {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	droppedItems := []string{}
	newMainHand := mainHand
	newOffHand := offHand

	// Determine what to unequip
	if req.Slot == "main_hand" || req.Slot == "both" {
		if mainHand.Valid && mainHand.String != "" {
			droppedItems = append(droppedItems, mainHand.String)
			newMainHand = sql.NullString{Valid: false}
		}
	}
	if req.Slot == "off_hand" || req.Slot == "both" {
		if offHand.Valid && offHand.String != "" {
			droppedItems = append(droppedItems, offHand.String)
			newOffHand = sql.NullString{Valid: false}
		}
	}

	if len(droppedItems) == 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "No weapons equipped in specified slot(s)",
		})
		return
	}

	// Putting a weapon away is the one weapon interaction of the turn in combat (not an action).
	// Dropping it is free and puts it on the ground.
	if !req.Drop {
		if _, fighting, _ := isCharacterInCombat(req.CharacterID); fighting {
			if weaponInteractionUsed(req.CharacterID) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "already_equipped_or_unequipped_this_turn", "message": msgAlreadyUsed})
				return
			}
			markWeaponInteraction(req.CharacterID)
		}
	}

	// Update database
	_, err = db.Exec(`UPDATE characters SET equipped_main_hand = $1, equipped_off_hand = $2 WHERE id = $3`,
		newMainHand, newOffHand, req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}
	syncHeldItems(req.CharacterID) // the equipped weapon is now held; the one it replaced is sheathed

	action := "stowed"
	groundIDs := []int{}
	if req.Drop {
		action = "dropped"
		for _, name := range droppedItems {
			if res, errCode := dropToGround(req.CharacterID, name, 1); errCode == "" {
				groundIDs = append(groundIDs, res.DroppedID)
			}
		}
	}

	response := map[string]interface{}{
		"success":            true,
		"character":          charName,
		"action":             action,
		"dropped_ids":        groundIDs,
		"items":              droppedItems,
		"equipped_main_hand": nullStringToInterface(newMainHand),
		"equipped_off_hand":  nullStringToInterface(newOffHand),
	}

	if req.Drop {
		response["note"] = "Weapons dropped on ground. GM may need to track dropped item locations."
	}

	json.NewEncoder(w).Encode(response)
}

// handleCharacterMount godoc
// @Summary Mount a creature
// @Description Mount a willing creature that is at least one size larger than you.
// @Description Mounting costs half your movement speed. The mount can be controlled or independent.
// @Description Controlled: Mount acts on rider's initiative, rider directs movement.
// @Description Independent: Mount rolls its own initiative, acts on its own turn.
// @Description Intelligent creatures (INT >= 6) are typically independent.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=integer,creature=string,controlled=boolean} true "Mount request"
// @Success 200 {object} map[string]interface{} "Mount result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/mount [post]
func handleCharacterMount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "POST required"})
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		Creature    string `json:"creature"`   // slug or name of creature to mount
		Controlled  *bool  `json:"controlled"` // nil = auto-determine based on INT
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 || req.Creature == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "character_id and creature required",
			"hint":  "creature should be a monster slug like 'riding-horse' or 'warhorse'",
		})
		return
	}

	// Verify character belongs to agent or agent is GM
	var charName, charSpecies string
	var ownerID, lobbyID, movementRemaining int
	var currentMount sql.NullString
	err = db.QueryRow(`
		SELECT c.name, c.agent_id, c.lobby_id, c.species, 
		       COALESCE(c.movement_remaining, 30), c.mounted_on_creature
		FROM characters c WHERE c.id = $1`, req.CharacterID).Scan(
		&charName, &ownerID, &lobbyID, &charSpecies, &movementRemaining, &currentMount)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Check authorization
	if ownerID != agentID {
		var dmID int
		db.QueryRow(`SELECT dm_id FROM lobbies WHERE id = $1`, lobbyID).Scan(&dmID)
		if dmID != agentID {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
			return
		}
	}

	// Check if already mounted
	if currentMount.Valid && currentMount.String != "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "already_mounted",
			"message": fmt.Sprintf("%s is already mounted on %s. Dismount first.", charName, currentMount.String),
		})
		return
	}

	// Look up the creature in the monsters table
	creatureSlug := strings.ToLower(strings.ReplaceAll(req.Creature, " ", "-"))
	var mountName, mountSize string
	var mountSpeed, mountInt int
	err = db.QueryRow(`
		SELECT name, size, speed, COALESCE(intl, 2) 
		FROM monsters WHERE slug = $1 OR LOWER(name) = LOWER($2)`,
		creatureSlug, req.Creature).Scan(&mountName, &mountSize, &mountSpeed, &mountInt)
	if err != nil {
		// If not found in monsters, allow custom mount (GM flexibility)
		mountName = req.Creature
		mountSize = "Large" // Assume large
		mountSpeed = 60     // Default mount speed
		mountInt = 2        // Default low INT
	}

	// Get rider's size for comparison
	riderSize := getSpeciesSize(charSpecies)
	if !isMountLargeEnough(mountSize, riderSize) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "mount_too_small",
			"message":    fmt.Sprintf("A %s creature cannot serve as a mount for a %s rider. Mount must be at least one size larger.", mountSize, riderSize),
			"mount_size": mountSize,
			"rider_size": riderSize,
		})
		return
	}

	// Check movement cost (mounting costs half your speed)
	movementSpeed := characterSpeed(req.CharacterID, charSpecies)
	mountCost := movementSpeed / 2
	if movementRemaining < mountCost {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":                 "insufficient_movement",
			"message":               fmt.Sprintf("Mounting costs %dft of movement, but %s only has %dft remaining.", mountCost, charName, movementRemaining),
			"mount_cost_ft":         mountCost,
			"movement_remaining_ft": movementRemaining,
		})
		return
	}

	// Determine controlled vs independent
	// Intelligent creatures (INT >= 6) are typically independent
	isControlled := true
	if req.Controlled != nil {
		isControlled = *req.Controlled
	} else if mountInt >= 6 {
		isControlled = false
	}

	// Deduct movement and set mount
	newMovement := movementRemaining - mountCost
	_, err = db.Exec(`
		UPDATE characters 
		SET mounted_on_creature = $1, mount_is_controlled = $2, movement_remaining = $3
		WHERE id = $4`,
		creatureSlug, isControlled, newMovement, req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error", "detail": publicError(err)})
		return
	}

	controlType := "controlled"
	if !isControlled {
		controlType = "independent"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":               true,
		"character":             charName,
		"mount":                 mountName,
		"mount_slug":            creatureSlug,
		"mount_speed_ft":        mountSpeed,
		"control_type":          controlType,
		"movement_cost_ft":      mountCost,
		"movement_remaining_ft": newMovement,
		"message":               fmt.Sprintf("%s mounts %s (%s mount). Movement speed while mounted: %dft.", charName, mountName, controlType, mountSpeed),
		"rules": map[string]interface{}{
			"controlled":  "Mount acts on your initiative. You control its movement.",
			"independent": "Mount rolls its own initiative and acts independently. You can only move when mount Ready's its action.",
		},
	})

	// Log to campaign feed
	if lobbyID != 0 {
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result)
			VALUES ($1, $2, 'mount', $3, $4)`,
			lobbyID, req.CharacterID,
			fmt.Sprintf("%s mounts %s", charName, mountName),
			fmt.Sprintf("%s mount, speed %dft", controlType, mountSpeed))
	}
}

// handleCharacterDismount godoc
// @Summary Dismount from a creature
// @Description Dismount from your current mount.
// @Description Dismounting costs half your movement speed.
// @Description Forced dismounts (mount dies, knocked prone, thrown off) don't cost movement.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=integer,forced=boolean} true "Dismount request"
// @Success 200 {object} map[string]interface{} "Dismount result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/dismount [post]
func handleCharacterDismount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "POST required"})
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CharacterID int  `json:"character_id"`
		Forced      bool `json:"forced"` // true = no movement cost (mount died, knocked off, etc.)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	// Verify character belongs to agent or agent is GM
	var charName, charSpecies string
	var ownerID, lobbyID, movementRemaining int
	var currentMount sql.NullString
	err = db.QueryRow(`
		SELECT c.name, c.agent_id, c.lobby_id, c.species, 
		       COALESCE(c.movement_remaining, 30), c.mounted_on_creature
		FROM characters c WHERE c.id = $1`, req.CharacterID).Scan(
		&charName, &ownerID, &lobbyID, &charSpecies, &movementRemaining, &currentMount)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Check authorization
	if ownerID != agentID {
		var dmID int
		db.QueryRow(`SELECT dm_id FROM lobbies WHERE id = $1`, lobbyID).Scan(&dmID)
		if dmID != agentID {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
			return
		}
	}

	// Check if currently mounted
	if !currentMount.Valid || currentMount.String == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_mounted",
			"message": fmt.Sprintf("%s is not currently mounted on anything.", charName),
		})
		return
	}

	// Get mount name for message
	var mountName string
	db.QueryRow(`SELECT name FROM monsters WHERE slug = $1`, currentMount.String).Scan(&mountName)
	if mountName == "" {
		mountName = currentMount.String
	}

	// Calculate movement cost (half speed for voluntary dismount, 0 for forced)
	movementSpeed := characterSpeed(req.CharacterID, charSpecies)
	dismountCost := 0
	if !req.Forced {
		dismountCost = movementSpeed / 2
		if movementRemaining < dismountCost {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":                 "insufficient_movement",
				"message":               fmt.Sprintf("Dismounting costs %dft of movement, but %s only has %dft remaining. Use forced:true for emergency dismount.", dismountCost, charName, movementRemaining),
				"dismount_cost_ft":      dismountCost,
				"movement_remaining_ft": movementRemaining,
				"hint":                  "Set forced:true if the dismount is involuntary (mount died, knocked off, etc.)",
			})
			return
		}
	}

	// Update database
	newMovement := movementRemaining - dismountCost
	_, err = db.Exec(`
		UPDATE characters 
		SET mounted_on_creature = NULL, mount_is_controlled = NULL, movement_remaining = $1
		WHERE id = $2`,
		newMovement, req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error", "detail": publicError(err)})
		return
	}

	dismountType := "voluntary"
	if req.Forced {
		dismountType = "forced"
	}

	response := map[string]interface{}{
		"success":               true,
		"character":             charName,
		"dismounted_from":       mountName,
		"dismount_type":         dismountType,
		"movement_remaining_ft": newMovement,
	}

	if req.Forced {
		response["message"] = fmt.Sprintf("%s is forcibly dismounted from %s! (No movement cost)", charName, mountName)
		response["note"] = "Forced dismount: you may need to make a DC 10 Dexterity saving throw to land on your feet. On failure, you land prone."
	} else {
		response["message"] = fmt.Sprintf("%s dismounts from %s. (%dft movement spent)", charName, mountName, dismountCost)
		response["movement_cost_ft"] = dismountCost
	}

	json.NewEncoder(w).Encode(response)

	// Log to campaign feed
	if lobbyID != 0 {
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result)
			VALUES ($1, $2, 'dismount', $3, $4)`,
			lobbyID, req.CharacterID,
			fmt.Sprintf("%s dismounts from %s", charName, mountName),
			dismountType)
	}
}
