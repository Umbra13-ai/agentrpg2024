package main

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"strings"
)

// handleGMForcedMovement godoc
// @Summary Break grapples due to forced movement
// @Description GM reports that a creature was forcibly moved (by a spell, a shove and so on). A grapple ends if the distance between the Grappled target and the grappler exceeds the grapple's range.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{target_id=int,cause=string,distance=string} true "Forced movement details"
// @Success 200 {object} map[string]interface{} "Result with broken grapples"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /gm/forced-movement [post]
func handleGMForcedMovement(w http.ResponseWriter, r *http.Request) {
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": publicError(err),
		})
		return
	}

	var req struct {
		TargetID int    `json:"target_id"`
		Cause    string `json:"cause"`    // e.g., "Thunderwave", "Eldritch Blast with Repelling Blast", "gust of wind"
		Distance string `json:"distance"` // e.g., "10ft", "15 feet"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.TargetID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "target_id required"})
		return
	}

	// Verify target is in this campaign
	var targetName string
	var targetLobby int
	err = db.QueryRow(`SELECT name, lobby_id FROM characters WHERE id = $1`, req.TargetID).Scan(&targetName, &targetLobby)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "target_not_found"})
		return
	}

	if targetLobby != campaignID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "target_not_in_campaign"})
		return
	}

	// Break any grapples on the target
	brokenGrapples := breakGrapplesOnTarget(req.TargetID)

	// Build cause description
	cause := req.Cause
	if cause == "" {
		cause = "forced movement"
	}
	distance := req.Distance
	if distance == "" {
		distance = "out of reach"
	}

	response := map[string]interface{}{
		"target_id":   req.TargetID,
		"target_name": targetName,
		"cause":       cause,
		"distance":    distance,
	}

	if len(brokenGrapples) > 0 {
		response["grapples_broken"] = brokenGrapples
		response["message"] = fmt.Sprintf("%s was moved %s by %s, breaking grapples from: %s",
			targetName, distance, cause, strings.Join(brokenGrapples, ", "))

		// Record the action
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'forced_movement', $3, $4)`,
			campaignID, req.TargetID,
			fmt.Sprintf("%s forced movement (%s)", targetName, cause),
			fmt.Sprintf("%s moved %s, grapples broken: %s", targetName, distance, strings.Join(brokenGrapples, ", ")))
	} else {
		response["grapples_broken"] = []string{}
		response["message"] = fmt.Sprintf("%s was moved %s by %s (no grapples to break)", targetName, distance, cause)
	}

	response["rules_note"] = "SRD 5.2.1, Ending a Grapple: the condition also ends if the grappler has the Incapacitated condition or if the distance between the Grappled target and the grappler exceeds the grapple's range."

	json.NewEncoder(w).Encode(response)
}

// handleGMFallingDamage godoc
// @Summary Apply falling damage to a character
// @Description Deal falling damage: 1d6 per 10 feet fallen (max 20d6 at 200ft). Damage type is bludgeoning. Monks level 4+ can use Slow Fall (use_slow_fall=true) to reduce damage by 5 × monk level using their reaction. A creature Feather Fall protects takes no damage (the spell then ends for it).
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=integer,distance_feet=integer,reason=string,use_slow_fall=boolean} true "Falling details. use_slow_fall requires Monk level 4+ and uses reaction."
// @Success 200 {object} map[string]interface{} "Damage applied"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/falling-damage [post]
func handleGMFallingDamage(w http.ResponseWriter, r *http.Request) {
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
		CharacterID  int    `json:"character_id"`
		DistanceFeet int    `json:"distance_feet"`
		Reason       string `json:"reason"`
		UseSlowFall  bool   `json:"use_slow_fall"` // Monk Slow Fall
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 || req.DistanceFeet <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_id and positive distance_feet required",
		})
		return
	}

	// Verify agent is DM of the character's campaign
	var lobbyID, dmID int
	err = db.QueryRow(`
		SELECT c.lobby_id, l.dm_id FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.id = $1
	`, req.CharacterID).Scan(&lobbyID, &dmID)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Character %d not found", req.CharacterID),
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

	// Calculate damage: 1d6 per 10 feet, max 20d6 (200 feet)
	diceCount := req.DistanceFeet / 10
	if diceCount < 1 {
		diceCount = 1 // Minimum 1d6 if any fall at all
	}
	if diceCount > 20 {
		diceCount = 20 // Maximum 20d6 at 200+ feet
	}

	// Roll the dice
	rolls, totalDamage := game.RollDice(diceCount, 6)

	// Get character info including class info for Slow Fall
	var charName, class string
	var currentHP, maxHP, level int
	var reactionUsed bool
	var classLevelsJSON []byte
	err = db.QueryRow(`
		SELECT name, hp, max_hp, class, level, COALESCE(reaction_used, false), COALESCE(class_levels, '{}')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &currentHP, &maxHP, &class, &level, &reactionUsed, &classLevelsJSON)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}
	if ok, note := featherFallLands(req.CharacterID); ok { // Feather Fall (spells_more_2024.go)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "character": charName, "distance_feet": req.DistanceFeet, "damage": 0, "feather_fall": true, "message": note})
		return
	}

	// Monk Slow Fall
	// When falling, can use reaction to reduce damage by 5 × monk level
	slowFallReduction := 0
	slowFallUsed := false
	slowFallMsg := ""

	if req.UseSlowFall {
		// Calculate monk level (supports multiclassing)
		monkLevel := 0
		var classLevels map[string]int
		json.Unmarshal(classLevelsJSON, &classLevels)
		if len(classLevels) > 0 {
			monkLevel = classLevels["monk"]
		} else if strings.ToLower(class) == "monk" {
			monkLevel = level
		}

		if monkLevel < 4 {
			if monkLevel == 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "not_monk",
					"message": fmt.Sprintf("%s is not a Monk. Slow Fall is a Monk feature (level 4+).", charName),
				})
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "level_requirement",
				"message": fmt.Sprintf("%s is a Monk level %d. Slow Fall requires Monk level 4+.", charName, monkLevel),
			})
			return
		}

		if reactionUsed {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_reaction",
				"message": fmt.Sprintf("%s has already used their reaction this round", charName),
			})
			return
		}

		// Calculate reduction: 5 × monk level
		slowFallReduction = 5 * monkLevel
		slowFallUsed = true

		// Mark reaction as used
		db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, req.CharacterID)

		slowFallMsg = fmt.Sprintf("🍃 Slow Fall reduces damage by %d (5 × %d monk levels)", slowFallReduction, monkLevel)
	}

	// Apply Slow Fall reduction first (can reduce to 0)
	damageAfterSlowFall := totalDamage - slowFallReduction
	if damageAfterSlowFall < 0 {
		damageAfterSlowFall = 0
	}

	// Apply damage resistance if character has bludgeoning resistance (e.g., petrified)
	damageResult := applyDamageResistance(req.CharacterID, damageAfterSlowFall, "bludgeoning")
	finalDamage := damageResult.FinalDamage

	// Apply the damage
	newHP := currentHP - finalDamage
	if newHP < 0 {
		newHP = 0
	}

	// Check Barbarian Relentless Rage first (requires CON save)
	relentlessTriggered := false
	relentlessMsg := ""
	if newHP == 0 {
		relentlessHP, relentlessUsed, msg := checkRelentlessRage(req.CharacterID, currentHP, finalDamage, maxHP)
		if relentlessUsed {
			newHP = relentlessHP
			relentlessTriggered = true
			relentlessMsg = msg
		} else if msg != "" {
			// Save failed, still add the message
			relentlessMsg = msg
		}
	}

	// Check Orc Relentless Endurance (automatic, no save)
	if newHP == 0 {
		relentlessHP, relentlessUsed, msg := checkRelentlessEndurance(req.CharacterID, currentHP, finalDamage, maxHP)
		if relentlessUsed {
			newHP = relentlessHP
			relentlessTriggered = true
			relentlessMsg = msg
		}
	}

	_, err = db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", newHP, req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "failed_to_update_hp"})
		return
	}

	// Determine consequence
	consequence := ""
	if relentlessTriggered {
		consequence = relentlessMsg
	} else if newHP == 0 {
		consequence = "💀 Character is unconscious and must make death saving throws!"
	} else if newHP <= maxHP/4 {
		consequence = "⚠️ Character is badly hurt!"
	}

	// Build reason string
	reason := req.Reason
	if reason == "" {
		reason = fmt.Sprintf("fell %d feet", req.DistanceFeet)
	}

	// Log the action
	actionDesc := fmt.Sprintf("Falling damage: %s %s", charName, reason)
	actionResult := fmt.Sprintf("%dd6 = %v → %d bludgeoning damage", diceCount, rolls, totalDamage)
	if slowFallUsed {
		actionResult += fmt.Sprintf(" (Slow Fall: -%d)", slowFallReduction)
	}
	if damageResult.WasHalved {
		actionResult += " (halved due to resistance)"
	}

	db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, $3, $4, $5)
	`, lobbyID, req.CharacterID, "falling_damage", actionDesc, actionResult)

	// Build message
	message := fmt.Sprintf("%s %s and takes %d bludgeoning damage (%dd6=%v)", charName, reason, finalDamage, diceCount, rolls)
	if slowFallUsed && slowFallReduction >= totalDamage {
		message = fmt.Sprintf("%s %s but lands gracefully thanks to Slow Fall (🍃 negated %d damage)", charName, reason, totalDamage)
	} else if slowFallUsed {
		message = fmt.Sprintf("%s %s, using Slow Fall to reduce the impact (🍃 -%d), taking %d bludgeoning damage", charName, reason, slowFallReduction, finalDamage)
	}

	response := map[string]interface{}{
		"success":          true,
		"character":        charName,
		"character_id":     req.CharacterID,
		"distance_feet":    req.DistanceFeet,
		"dice_rolled":      fmt.Sprintf("%dd6", diceCount),
		"individual_rolls": rolls,
		"raw_damage":       totalDamage,
		"damage_type":      "bludgeoning",
		"final_damage":     finalDamage,
		"previous_hp":      currentHP,
		"current_hp":       newHP,
		"max_hp":           maxHP,
		"message":          message,
	}

	// Add Slow Fall info if used
	if slowFallUsed {
		response["slow_fall"] = map[string]interface{}{
			"used":          true,
			"reduction":     slowFallReduction,
			"reaction_used": true,
			"note":          slowFallMsg,
		}
	}

	if damageResult.WasHalved {
		response["resistance_applied"] = true
		response["resistances"] = damageResult.Resistances
	}

	if consequence != "" {
		response["consequence"] = consequence
	}

	json.NewEncoder(w).Encode(response)
}

// handleGMUnderwater godoc
// @Summary Toggle underwater combat mode
// @Description Set or toggle underwater combat for a campaign. When underwater (SRD 5.2.1): a melee weapon attack has Disadvantage unless the weapon deals Piercing damage or the attacker has a Swim Speed; a ranged weapon attack has Disadvantage within normal range and automatically misses beyond it; anything underwater has Resistance to Fire damage.
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{campaign_id=integer,underwater=boolean} true "Underwater combat settings. If underwater is omitted, toggles current state."
// @Success 200 {object} map[string]interface{} "Underwater status updated"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/underwater [post]
func handleGMUnderwater(w http.ResponseWriter, r *http.Request) {
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
		CampaignID int   `json:"campaign_id"`
		Underwater *bool `json:"underwater"` // Pointer to allow nil (toggle)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CampaignID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "campaign_id required",
		})
		return
	}

	// Verify agent is DM
	var dmID int
	err = db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", req.CampaignID).Scan(&dmID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "campaign_not_found",
			"message": fmt.Sprintf("Campaign %d not found", req.CampaignID),
		})
		return
	}

	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": "You are not the GM of this campaign",
		})
		return
	}

	// Check if combat state exists
	var currentUnderwater bool
	err = db.QueryRow("SELECT COALESCE(underwater, false) FROM combat_state WHERE lobby_id = $1", req.CampaignID).Scan(&currentUnderwater)
	if err != nil {
		// No combat state - create one (can set underwater even outside active combat)
		newState := false
		if req.Underwater != nil {
			newState = *req.Underwater
		}
		_, err = db.Exec(`
			INSERT INTO combat_state (lobby_id, active, underwater)
			VALUES ($1, false, $2)
			ON CONFLICT (lobby_id) DO UPDATE SET underwater = $2
		`, req.CampaignID, newState)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error", "message": publicError(err)})
			return
		}
		currentUnderwater = newState
	} else {
		// Determine new state (toggle if not specified)
		newState := !currentUnderwater
		if req.Underwater != nil {
			newState = *req.Underwater
		}

		_, err = db.Exec("UPDATE combat_state SET underwater = $1 WHERE lobby_id = $2", newState, req.CampaignID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error", "message": publicError(err)})
			return
		}
		currentUnderwater = newState
	}

	// Log the action
	statusText := "The party surfaces"
	if currentUnderwater {
		statusText = "The party submerges into water"
	}
	db.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, $2, $3, $4)
	`, req.CampaignID, "environment", statusText,
		fmt.Sprintf("Underwater combat: %v", currentUnderwater))

	effects := []string{}
	if currentUnderwater {
		effects = append(effects, "Melee attacks have disadvantage (without swim speed)")
		effects = append(effects, "Ranged attacks have disadvantage (except crossbows, nets, and thrown weapons)")
		effects = append(effects, "Fire damage is halved (resistance)")
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"campaign_id": req.CampaignID,
		"underwater":  currentUnderwater,
		"effects":     effects,
		"message":     statusText,
		"note":        "Underwater effects apply to all combatants. Crossbows, nets, and thrown weapons (javelin, trident, spear, dart) work normally for ranged attacks.",
	})
}

// handleGMSetLighting godoc
// @Summary Set area lighting level
// @Description Set the lighting level for a campaign area. Lighting affects visibility and attack rolls: bright (normal), dim (disadvantage on Perception), darkness (heavily obscured - effectively blinded without darkvision/blindsight/truesight).
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{campaign_id=integer,lighting=string} true "Lighting level: 'bright', 'dim', or 'darkness'"
// @Success 200 {object} map[string]interface{} "Lighting updated"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/set-lighting [post]
func handleGMSetLighting(w http.ResponseWriter, r *http.Request) {
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
		CampaignID int    `json:"campaign_id"`
		Lighting   string `json:"lighting"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CampaignID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "campaign_id required",
		})
		return
	}

	// Validate lighting value
	req.Lighting = strings.ToLower(req.Lighting)
	validLighting := map[string]bool{"bright": true, "dim": true, "darkness": true}
	if !validLighting[req.Lighting] {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "invalid_lighting",
			"message":       "lighting must be 'bright', 'dim', or 'darkness'",
			"valid_options": []string{"bright", "dim", "darkness"},
		})
		return
	}

	// Verify agent is DM
	var dmID int
	err = db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", req.CampaignID).Scan(&dmID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "campaign_not_found",
			"message": fmt.Sprintf("Campaign %d not found", req.CampaignID),
		})
		return
	}

	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": "You are not the GM of this campaign",
		})
		return
	}

	// Upsert lighting in combat_state
	_, err = db.Exec(`
		INSERT INTO combat_state (lobby_id, active, lighting)
		VALUES ($1, false, $2)
		ON CONFLICT (lobby_id) DO UPDATE SET lighting = $2
	`, req.CampaignID, req.Lighting)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error", "message": publicError(err)})
		return
	}

	// Log the action
	var statusText string
	switch req.Lighting {
	case "bright":
		statusText = "The area is brightly lit"
	case "dim":
		statusText = "The area is shrouded in dim light"
	case "darkness":
		statusText = "Darkness falls over the area"
	}

	db.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, $2, $3, $4)
	`, req.CampaignID, "environment", statusText,
		fmt.Sprintf("Lighting set to: %s", req.Lighting))

	// Describe effects
	effects := []string{}
	notes := []string{}

	switch req.Lighting {
	case "bright":
		effects = append(effects, "Normal visibility for all creatures")
	case "dim":
		effects = append(effects, "Disadvantage on Wisdom (Perception) checks relying on sight")
		notes = append(notes, "Creatures with darkvision treat dim light as bright light")
	case "darkness":
		effects = append(effects, "Heavily obscured - creatures without darkvision/blindsight/truesight are effectively blinded")
		effects = append(effects, "Blinded: Disadvantage on attacks, advantage against them, auto-fail sight-based checks")
		notes = append(notes, "Darkvision: treats darkness as dim light (Perception disadvantage only)")
		notes = append(notes, "Blindsight/Truesight: unaffected by darkness")
	}

	// Get party vision capabilities for helpful info
	partyVision := []map[string]interface{}{}
	rows, _ := db.Query(`SELECT name, COALESCE(darkvision_range, 0), COALESCE(blindsight_range, 0), COALESCE(truesight_range, 0) 
		FROM characters WHERE lobby_id = $1`, req.CampaignID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var darkvision, blindsight, truesight int
			rows.Scan(&name, &darkvision, &blindsight, &truesight)
			if darkvision > 0 || blindsight > 0 || truesight > 0 {
				vision := map[string]interface{}{"name": name}
				if darkvision > 0 {
					vision["darkvision"] = darkvision
				}
				if blindsight > 0 {
					vision["blindsight"] = blindsight
				}
				if truesight > 0 {
					vision["truesight"] = truesight
				}
				partyVision = append(partyVision, vision)
			}
		}
	}

	response := map[string]interface{}{
		"success":     true,
		"campaign_id": req.CampaignID,
		"lighting":    req.Lighting,
		"effects":     effects,
		"message":     statusText,
	}
	if len(notes) > 0 {
		response["notes"] = notes
	}
	if len(partyVision) > 0 {
		response["party_vision_capabilities"] = partyVision
	}

	json.NewEncoder(w).Encode(response)
}
