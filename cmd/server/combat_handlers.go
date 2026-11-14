package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// isUnderwaterCombat checks if combat in the given campaign is underwater
func isUnderwaterCombat(lobbyID int) bool {
	var underwater bool
	err := db.QueryRow("SELECT COALESCE(underwater, false) FROM combat_state WHERE lobby_id = $1", lobbyID).Scan(&underwater)
	if err != nil {
		return false
	}
	return underwater
}

// getCampaignLighting returns the current lighting level for a campaign
// Returns: "bright" (default), "dim", or "darkness"
func getCampaignLighting(lobbyID int) string {
	var lighting string
	err := db.QueryRow("SELECT COALESCE(lighting, 'bright') FROM combat_state WHERE lobby_id = $1", lobbyID).Scan(&lighting)
	if err != nil {
		return "bright" // Default to bright light
	}
	return lighting
}

// getCharacterVision returns vision capabilities for a character
// Returns darkvision, blindsight, truesight ranges in feet
func getCharacterVision(charID int) (darkvision, blindsight, truesight int) {
	db.QueryRow(`SELECT COALESCE(darkvision_range, 0), COALESCE(blindsight_range, 0), COALESCE(truesight_range, 0) 
		FROM characters WHERE id = $1`, charID).Scan(&darkvision, &blindsight, &truesight)
	if t := buffTruesight(charID); t > truesight {
		truesight = t // True Seeing (buff_spells_2024.go)
	}
	if d := darkvisionBuff(charID); d > darkvision {
		darkvision = d // the Darkvision spell (spells_more_2024.go)
	}
	return
}

// canSeeInLighting checks if a character can see clearly in given lighting
// Returns:
// - "normal": can see normally (bright light, or has appropriate vision)
// - "dim": can see but with disadvantage on Perception (dim light without special vision)
// - "blind": effectively blinded (darkness without darkvision/blindsight/truesight)
func canSeeInLighting(charID int, lighting string) string {
	darkvision, blindsight, truesight := getCharacterVision(charID)

	switch lighting {
	case "bright":
		return "normal"
	case "dim":
		// Dim light: disadvantage on Perception, but no attack penalties
		// Darkvision treats dim light as bright for combat purposes
		if darkvision > 0 || blindsight > 0 || truesight > 0 {
			return "normal"
		}
		return "dim" // Perception disadvantage only, attacks unaffected
	case "darkness":
		// Darkness: heavily obscured = effectively blinded
		// Truesight sees through darkness
		if truesight > 0 {
			return "normal"
		}
		// Blindsight doesn't rely on light
		if blindsight > 0 {
			return "normal"
		}
		// Devil's Sight (Warlock Invocation)
		// Can see normally in darkness, both magical and nonmagical, to 120 feet
		if hasInvocation(charID, "devils-sight") {
			return "normal"
		}
		// Darkvision treats darkness as dim light (no attack penalty, Perception disadvantage)
		if darkvision > 0 {
			return "dim"
		}
		// No vision capabilities = effectively blinded
		return "blind"
	}
	return "normal"
}

// nullString helper for optional string fields
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// handleRoll godoc
// @Summary Roll dice
// @Description Fair dice using crypto/rand. Supports advantage/disadvantage for d20s. No authentication required.
// @Tags Actions
// @Produce json
// @Param dice query string false "Dice notation (e.g., 2d6, 1d20)" default(1d20)
// @Param advantage query bool false "Roll with advantage (d20 only)"
// @Param disadvantage query bool false "Roll with disadvantage (d20 only)"
// @Success 200 {object} map[string]interface{} "Dice roll result with individual rolls and total"
// @Router /roll [get]
func handleRoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	dice := r.URL.Query().Get("dice")
	if dice == "" {
		dice = "1d20"
	}

	advantage := r.URL.Query().Get("advantage") == "true"
	disadvantage := r.URL.Query().Get("disadvantage") == "true"

	parts := strings.Split(strings.ToLower(dice), "d")
	if len(parts) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "format: NdM (e.g., 2d6)"})
		return
	}

	count, _ := strconv.Atoi(parts[0])
	sides, _ := strconv.Atoi(parts[1])
	if count < 1 {
		count = 1
	}
	if count > 100 {
		count = 100
	}
	if sides < 2 {
		sides = 2
	}
	if sides > 100 {
		sides = 100
	}

	// Handle advantage/disadvantage for d20
	if sides == 20 && count == 1 && (advantage || disadvantage) {
		var result, roll1, roll2 int
		rollType := "normal"
		if advantage && !disadvantage {
			result, roll1, roll2 = game.RollWithAdvantage()
			rollType = "advantage"
		} else if disadvantage && !advantage {
			result, roll1, roll2 = game.RollWithDisadvantage()
			rollType = "disadvantage"
		} else {
			// Both cancel out
			result = game.RollDie(20)
			roll1, roll2 = result, result
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"dice": dice, "rolls": []int{roll1, roll2}, "total": result, "type": rollType,
		})
		return
	}

	rolls, total := game.RollDice(count, sides)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"dice": dice, "rolls": rolls, "total": total,
	})
}

// handleCombatStart godoc
// @Summary Start combat (GM only)
// @Description Roll initiative for all characters and enter combat mode. Characters surprised by combat starting (listed in "surprised") roll Initiative with Disadvantage.
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param request body object{surprised=[]int} false "Optional: ids of surprised characters"
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Combat started with initiative order"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can start combat"
// @Router /campaigns/{id}/combat/start [post]
func handleCombatStart(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Check if user is GM
	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_start_combat"})
		return
	}

	setTravelPace(campaignID, "") // a fight interrupts the journey: the travel pace no longer affects checks

	// Surprise: a character surprised by combat starting has Disadvantage on the Initiative roll (optional body {"surprised": [character ids]}).
	var startReq struct {
		Surprised []int `json:"surprised"`
	}
	json.NewDecoder(r.Body).Decode(&startReq)
	surprised := map[int]bool{}
	for _, id := range startReq.Surprised {
		surprised[id] = true
	}

	// Roll initiative for all characters in the campaign
	// Include class info for Feral Instinct, Superior Inspiration, Perfect Focus
	// Include subclass for Thief's Reflexes
	rows, err := db.Query(`
		SELECT c.id, c.name, c.dex, COALESCE(c.initiative_bonus, 0), c.class, c.level, c.cha, c.subclass
		FROM characters c WHERE c.lobby_id = $1
	`, campaignID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
		return
	}
	defer rows.Close()

	type InitEntry struct {
		ID                   int    `json:"id"`
		Name                 string `json:"name"`
		Initiative           int    `json:"initiative"`
		DexScore             int    `json:"dex_score"`
		IsThiefsReflexesTurn bool   `json:"is_thiefs_reflexes_turn,omitempty"` // Thief's Reflexes extra turn
	}

	entries := []InitEntry{}
	capstoneNotes := []string{}
	thiefsReflexesCandidates := []struct {
		ID         int
		Name       string
		Initiative int
		DexScore   int
	}{}

	for rows.Next() {
		var id, dex, initBonus, level, cha int
		var name, class string
		var subclass sql.NullString
		rows.Scan(&id, &name, &dex, &initBonus, &class, &level, &cha, &subclass)
		clearUsedKeys(id, "improve_fate") // Boon of Fate returns when Initiative is rolled
		evergreenWildShape(id)            // Archdruid
		if hasSpecificFeat(id, "alert") { // 2024 Alert: add the Proficiency Bonus to Initiative
			initBonus += game.ProficiencyBonus(level)
		}
		if strings.EqualFold(class, "bard") && level >= 2 { // Jack of All Trades: Initiative is a Dexterity check without proficiency
			initBonus += game.ProficiencyBonus(level) / 2
		}
		var exhaustion int
		db.QueryRow(`SELECT COALESCE(exhaustion_level,0) FROM characters WHERE id = $1`, id).Scan(&exhaustion)
		initBonus -= 2 * exhaustion // 2024 Exhaustion: -2 per level on every D20 Test, Initiative included

		classLower := strings.ToLower(class)
		dexMod := game.Modifier(dex)

		// Feral Instinct (Barbarian 7+) - advantage on initiative rolls
		var init int
		advInit := game.HasClassFeature(class, level, "feral_instinct") || (subclass.Valid && hasSubclassFeature(subclass.String, level, "remarkable_athlete")) // Barbarian Feral Instinct, Champion Remarkable Athlete
		if !advInit && id > 0 && (iounStone(id, "awareness") || sentinelShield(id)) {
			advInit = true // Ioun Stone of Awareness (F204)
		}
		if !advInit && weaponOfWarning(campaignID, id) {
			advInit = true // Weapon of Warning: Advantage on Initiative for its wielder and allies within 30 feet (items_c4_2024.go)
			capstoneNotes = append(capstoneNotes, fmt.Sprintf("🔔 %s: a Weapon of Warning gives Advantage on Initiative", name))
		}
		if surprised[id] && advInit {
			init = game.RollInitiative(dexMod, initBonus)
			capstoneNotes = append(capstoneNotes, fmt.Sprintf("😲 %s is surprised: the Disadvantage cancels the Advantage on Initiative", name))
		} else if surprised[id] {
			roll1, roll2 := game.RollDie(20), game.RollDie(20)
			low := roll1
			if roll2 < low {
				low = roll2
			}
			init = low + dexMod + initBonus
			capstoneNotes = append(capstoneNotes, fmt.Sprintf("😲 %s is surprised: Disadvantage on Initiative (rolled %d, %d, took %d)", name, roll1, roll2, low))
		} else if advInit {
			roll1 := game.RollDie(20)
			roll2 := game.RollDie(20)
			higherRoll := roll1
			if roll2 > roll1 {
				higherRoll = roll2
			}
			init = higherRoll + dexMod + initBonus
			capstoneNotes = append(capstoneNotes, fmt.Sprintf("🐺 %s: Feral Instinct grants advantage on initiative (rolled %d, %d, took %d)", name, roll1, roll2, higherRoll))
		} else {
			init = game.RollInitiative(dexMod, initBonus)
		}

		db.Exec("UPDATE characters SET current_initiative = $1 WHERE id = $2", init, id)

		// 2024 Superior Inspiration (Bard 18): when you roll initiative, regain Bardic Inspiration until you have two
		if classLower == "bard" && level >= 18 {
			currentResources := getCurrentClassResources(id)
			if bi, ok := currentResources["bardic_inspiration"]; ok && bi < 2 {
				var usedJSON []byte
				db.QueryRow("SELECT COALESCE(class_resources_used, '{}') FROM characters WHERE id = $1", id).Scan(&usedJSON)
				used := make(map[string]int)
				json.Unmarshal(usedJSON, &used)
				if used["bardic_inspiration"] > 0 {
					used["bardic_inspiration"] -= 2 - bi
					if used["bardic_inspiration"] < 0 {
						used["bardic_inspiration"] = 0
					}
					updatedJSON, _ := json.Marshal(used)
					db.Exec("UPDATE characters SET class_resources_used = $1 WHERE id = $2", updatedJSON, id)
					capstoneNotes = append(capstoneNotes, fmt.Sprintf("🎭 %s: Superior Inspiration triggered! Regained 1 Bardic Inspiration (had 0)", name))
				}
			}
		}

		// 2024 Monk: Uncanny Metabolism (level 2) and Perfect Focus (level 15) when initiative is rolled
		if note := monkInitiativeFeatures(id, name, class, level); note != "" {
			capstoneNotes = append(capstoneNotes, note)
		}
		if note := classCapstones(id, name, class, level); note != "" {
			capstoneNotes = append(capstoneNotes, note)
		}

		entries = append(entries, InitEntry{ID: id, Name: name, Initiative: init, DexScore: dex})

		// Track Thief Rogues level 17+ for Thief's Reflexes (second turn in first round)
		if classLower == "rogue" && level >= 17 && subclass.Valid && strings.ToLower(subclass.String) == "thief" {
			thiefsReflexesCandidates = append(thiefsReflexesCandidates, struct {
				ID         int
				Name       string
				Initiative int
				DexScore   int
			}{ID: id, Name: name, Initiative: init, DexScore: dex})
		}
	}

	// Thief's Reflexes - Thief Rogues level 17+ get a second turn
	// at their initiative minus 10 during the first round of combat
	for _, thief := range thiefsReflexesCandidates {
		extraInit := thief.Initiative - 10
		entries = append(entries, InitEntry{
			ID:                   thief.ID,
			Name:                 thief.Name + " (2nd turn)",
			Initiative:           extraInit,
			DexScore:             thief.DexScore,
			IsThiefsReflexesTurn: true,
		})
		capstoneNotes = append(capstoneNotes, fmt.Sprintf("🗡️ %s: Thief's Reflexes grants a second turn at initiative %d (normal: %d)", thief.Name, extraInit, thief.Initiative))
	}

	// Familiars with their own Initiative (server setting AGENTRPG_FAMILIAR_INITIATIVE=own; companions_2024.go)
	for _, f := range familiarTurnEntries(campaignID) {
		entries = append(entries, InitEntry{ID: asInt(f["id"]), Name: fmt.Sprint(f["name"]), Initiative: asInt(f["initiative"]), DexScore: asInt(f["dex_score"])})
	}

	// Sort by initiative (highest first), then by DEX (highest first)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Initiative != entries[j].Initiative {
			return entries[i].Initiative > entries[j].Initiative
		}
		return entries[i].DexScore > entries[j].DexScore
	})

	clearMonsterConditions(campaignID) // monster ids are reused from one fight to the next
	// Store combat state
	turnOrderJSON, _ := json.Marshal(entries)
	db.Exec(`
		INSERT INTO combat_state (lobby_id, round_number, current_turn_index, turn_order, active, turn_started_at)
		VALUES ($1, 1, 0, $2, true, NOW())
		ON CONFLICT (lobby_id) DO UPDATE SET
			round_number = 1, current_turn_index = 0, turn_order = $2, active = true, turn_started_at = NOW(), battle_map = NULL
	`, campaignID, turnOrderJSON)

	// Reset action economy for all characters (reactions, actions, bonus actions, movement)
	db.Exec("UPDATE characters SET reaction_used = false, action_used = false, bonus_action_used = false, weapon_interaction_used = false, in_melee_range = false WHERE lobby_id = $1", campaignID)
	if rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1`, campaignID); err == nil { // F124: once-per-turn flags too
		var ids []int
		for rows.Next() {
			var id int
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			resetTurnFlags(id, characterSpeedOf(id))
		}
	}
	for _, entry := range entries { // the AC monsters attack is the current one (armor, Shield, unarmored, magic items)
		if entry.ID > 0 && entry.ID < companionIDBase {
			refreshStoredAC(entry.ID)
		}
	}

	// Initialize movement for each character based on their species speed
	for _, entry := range entries {
		var species string
		db.QueryRow("SELECT species FROM characters WHERE id = $1", entry.ID).Scan(&species)
		speed := characterSpeed(entry.ID, species)
		db.Exec("UPDATE characters SET movement_remaining = $1 WHERE id = $2", speed, entry.ID)
	}

	response := map[string]interface{}{
		"success":             true,
		"round":               1,
		"turn_order":          entries,
		"current_turn":        entries[0].Name,
		"action_economy_note": "All characters have their action, bonus action, reaction, and full movement available.",
	}

	// Add capstone feature notes if any triggered
	if len(capstoneNotes) > 0 {
		response["class_feature_notes"] = capstoneNotes
	}

	json.NewEncoder(w).Encode(response)
}

// clearCombatInitiative deactivates combat and clears all initiative metadata.
// A finished encounter must not continue to present a stale current turn or
// turn order to clients; that stale data can otherwise drive timeout loops.
func clearCombatInitiative(campaignID int) error {
	clearMonsterConditions(campaignID)
	clearBurning(campaignID, false) // F69: the fight is over, so are its fires
	_, err := db.Exec(`
		UPDATE combat_state
		SET active = false,
			battle_map = NULL,
			round_number = 0,
			current_turn_index = 0,
			turn_order = '[]',
			turn_started_at = CURRENT_TIMESTAMP
		WHERE lobby_id = $1
	`, campaignID)
	return err
}

// nextLivingCombatTurn advances initiative without giving a turn to a dead
// character. Monsters (negative IDs) remain eligible. rounds counts wraps.
// The caller supplies the death lookup so the rule can be regression-tested.
func nextLivingCombatTurn(candidate int, ids []int, isDead func(int) bool) (index, rounds int) {
	if len(ids) == 0 {
		return 0, 0
	}
	for checked := 0; checked < len(ids); checked++ {
		if candidate >= len(ids) {
			candidate = 0
			rounds++
		}
		if !isDead(ids[candidate]) {
			return candidate, rounds
		}
		candidate++
	}
	// No eligible combatant remains. Keep the current slot rather than
	// manufacturing a new round or mutating combat state unexpectedly.
	return candidate % len(ids), rounds
}

func combatCharacterIsDead(id int) bool {
	var dead bool
	if err := db.QueryRow(`SELECT COALESCE(is_dead, false) FROM characters WHERE id = $1`, id).Scan(&dead); err != nil {
		return false // Do not skip an entry on an uncertain database read.
	}
	return dead
}

func characterActionBlock(action string, hp int, stable, dead bool) string {
	if dead {
		return "character_dead"
	}
	if action == "death_save" && (hp != 0 || stable) {
		return "death_save_unavailable"
	}
	// F48: at 0 Hit Points a character is Unconscious (SRD 5.2.1 "Falling to 0 Hit Points"), even if the condition wasn't stored
	if action != "death_save" && hp <= 0 {
		return "unconscious_at_0_hp"
	}
	return ""
}

// handleCombatEnd godoc
// @Summary End combat (GM only)
// @Description End combat mode and clear initiative
// @Tags Combat
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Combat ended"
// @Router /campaigns/{id}/combat/end [post]
func handleCombatEnd(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_end_combat"})
		return
	}

	if err := clearCombatInitiative(campaignID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "combat_end_failed"})
		return
	}

	// Clear temporary combat conditions and reset action economy
	db.Exec("UPDATE characters SET reaction_used = false, action_used = false, bonus_action_used = false, weapon_interaction_used = false, in_melee_range = false WHERE lobby_id = $1", campaignID)
	if rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1`, campaignID); err == nil {
		var ids []int
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			dropMarkersKeepConditions(id) // combat markers end; Poisoned, Prone, Unconscious ... do not
		}
	}

	endResp := map[string]interface{}{"success": true, "message": "Combat ended", "action_economy_note": "Action economy reset for all characters."}
	if l := lingeringConditions(campaignID); len(l) > 0 { // F227: these used to stay for good after a fight
		endResp["lingering_conditions"] = l
		endResp["lingering_note"] = "Outside combat each 6 seconds is still a turn: POST /api/gm/pass-time (or a rest) rolls these saves once per round until they succeed."
	}
	json.NewEncoder(w).Encode(endResp)
}

// handleCombatNext godoc
// @Summary Advance to next turn (GM only)
// @Description Move to the next character in initiative order. Optional legendary_actions: Legendary Actions taken right after the turn that ends, before the next turn starts (each like POST /api/gm/legendary-action). The reply lists legendary_options: the monsters that may take one now and their actions.
// @Param request body object{legendary_actions=[]object} false "Legendary Actions after the ending turn: combatant_id, action, target, target_ids, use"
// @Tags Combat
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Turn advanced"
// @Router /campaigns/{id}/combat/next [post]
func handleCombatNext(w http.ResponseWriter, r *http.Request, campaignID int) {
	defer clearOAWindows(campaignID) // Opportunity Attack chances last until the turn order moves on (character_oa_2024.go)
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_advance_turn"})
		return
	}
	// Optional: Legendary Actions taken right after the turn that is ending (monster_features_2024.go)
	var nextReq struct {
		LegendaryActions []legendaryRequest `json:"legendary_actions"`
	}
	if r.Body != nil {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&nextReq)
	}

	var round, turnIndex int
	var turnOrderJSON []byte
	var active bool
	err = db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active 
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&round, &turnIndex, &turnOrderJSON, &active)

	if err != nil || !active {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_active_combat"})
		return
	}

	type InitEntry struct {
		ID                   int    `json:"id"`
		Name                 string `json:"name"`
		Initiative           int    `json:"initiative"`
		DexScore             int    `json:"dex_score"`
		IsMonster            bool   `json:"is_monster"`
		MonsterKey           string `json:"monster_key"`
		HP                   int    `json:"hp"`
		MaxHP                int    `json:"max_hp"`
		AC                   int    `json:"ac"`
		IsThiefsReflexesTurn bool   `json:"is_thiefs_reflexes_turn,omitempty"`
	}
	var entries []InitEntry
	json.Unmarshal(turnOrderJSON, &entries)

	if len(entries) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants"})
		return
	}
	// Preserve inconsistent initiative for repair instead of panicking or
	// guessing which character's turn should be consumed.
	if turnIndex < 0 || turnIndex >= len(entries) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_combat_turn"})
		return
	}
	// Clear start-of-turn conditions for current character (ending their turn)
	currentID := entries[turnIndex].ID
	endedRound, endedIndex := round, turnIndex

	// Remove "dodging" and "reckless" conditions at end of turn (added reckless)
	// Decrement countercharm duration
	var condJSON []byte
	db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", currentID).Scan(&condJSON)
	var conds []string
	json.Unmarshal(condJSON, &conds)
	newConds := []string{}
	for _, c := range conds {
		if c == "dodging" || c == "reckless" {
			continue // Remove these conditions
		}
		// Handle countercharm duration (performing_countercharm:N)
		if strings.HasPrefix(c, "performing_countercharm:") {
			parts := strings.Split(c, ":")
			if len(parts) == 2 {
				remaining, err := strconv.Atoi(parts[1])
				if err == nil {
					if remaining > 0 {
						// Decrement and keep
						newConds = append(newConds, fmt.Sprintf("performing_countercharm:%d", remaining-1))
					}
					// If remaining == 0, don't add (effect expires)
					continue
				}
			}
		}
		newConds = append(newConds, c)
	}
	updatedConds, _ := json.Marshal(newConds)
	db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updatedConds, currentID)

	// Decrement Sacred Weapon duration at end of turn (if active)
	decrementSacredWeapon(currentID)

	// Decrement Holy Nimbus duration at end of turn (if active)
	decrementHolyNimbus(currentID)

	// Advance turn
	turnIndex++
	if turnIndex >= len(entries) {
		turnIndex = 0
		round++

		// Remove Thief's Reflexes extra turns when advancing to round 2
		// "You can take two turns during the first round of any combat"
		if round == 2 {
			originalLen := len(entries)
			filteredEntries := []InitEntry{}
			for _, e := range entries {
				if !e.IsThiefsReflexesTurn {
					filteredEntries = append(filteredEntries, e)
				}
			}
			if len(filteredEntries) < originalLen {
				entries = filteredEntries
				// turnIndex is already 0, which is correct for round 2
			}
		}
	}
	endTurnEffects(campaignID, -1)
	// Legendary Actions immediately after the turn that ended, before the next creature's turn starts
	window := fmt.Sprintf("%d:%d", endedRound, endedIndex)
	setLastTurnEnded(campaignID, window, currentID)
	var legendaryResults []map[string]interface{}
	for _, la := range nextReq.LegendaryActions {
		res, code, msg := takeLegendaryAction(r, campaignID, la, window, currentID)
		if code != "" {
			res = map[string]interface{}{"combatant_id": la.CombatantID, "error": code, "message": msg}
		}
		legendaryResults = append(legendaryResults, res)
	}
	ids := make([]int, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	if nextIndex, wraps := nextLivingCombatTurn(turnIndex, ids, combatDeadFn(campaignID)); nextIndex != turnIndex || wraps > 0 {
		turnIndex = nextIndex
		round += wraps
	}

	// Thief's Reflexes turns are removed after round 1: save the turn order without them, keeping every field of the other
	// entries (Legendary Action uses now live in combat_monster_state and come back in turnEffects)
	var originalEntries []InitEntry
	json.Unmarshal(turnOrderJSON, &originalEntries)
	needsUpdate := len(entries) != len(originalEntries) // True if Thief's Reflexes entries were removed
	newEntry := &entries[turnIndex]
	if needsUpdate {
		var rawEntries, kept []map[string]interface{}
		json.Unmarshal(turnOrderJSON, &rawEntries)
		for _, e := range rawEntries {
			if extra, _ := e["is_thiefs_reflexes_turn"].(bool); !extra {
				kept = append(kept, e)
			}
		}
		updatedTurnOrder, _ := json.Marshal(kept)
		db.Exec("UPDATE combat_state SET current_turn_index = $1, round_number = $2, turn_started_at = NOW(), turn_order = $3 WHERE lobby_id = $4", turnIndex, round, updatedTurnOrder, campaignID)
	} else {
		db.Exec("UPDATE combat_state SET current_turn_index = $1, round_number = $2, turn_started_at = NOW() WHERE lobby_id = $3", turnIndex, round, campaignID)
	}
	startTurnEffects(campaignID)

	// Reset action economy for the new active character (only for player characters)
	newActiveID := entries[turnIndex].ID
	if !newEntry.IsMonster {
		var species string
		db.QueryRow("SELECT species FROM characters WHERE id = $1", newActiveID).Scan(&species)
		speed := characterSpeed(newActiveID, species)
		resetTurnFlags(newActiveID, speed)
	}

	// Clear Multiattack Defense tracking for all characters when turn advances
	// "...for the rest of the turn" - resets when the attacker's turn ends
	clearAllMultiattackDefenseHits(campaignID)

	response := map[string]interface{}{
		"success":              true,
		"round":                round,
		"current_turn":         entries[turnIndex].Name,
		"turn_index":           turnIndex,
		"action_economy_reset": true,
	}

	if len(legendaryResults) > 0 {
		response["legendary_actions_taken"] = legendaryResults
	}
	if opts := legendaryOptions(campaignID, window, currentID); len(opts) > 0 {
		response["legendary_options"] = opts // after the turn that just ended: POST /api/gm/legendary-action
	}
	if notes := moreTurnHooks(campaignID, currentID, newActiveID); len(notes) > 0 { // Blink, Regenerate ... (spells_more_2024.go)
		response["spell_turn_effects"] = notes
		for _, n := range notes {
			recordGMAction(db, campaignID, "spell_effect", n, n)
		}
	}

	// Champion's Survivor feature - regenerate HP at start of turn if below 50% (level 18+)
	if !newEntry.IsMonster {
		var charClass, subclass sql.NullString
		var charLevel, hp, maxHP, conScore int
		err := db.QueryRow(`
			SELECT class, COALESCE(subclass, ''), level, hp, max_hp, con 
			FROM characters WHERE id = $1
		`, newActiveID).Scan(&charClass, &subclass, &charLevel, &hp, &maxHP, &conScore)
		if err == nil && charClass.Valid && subclass.Valid && subclass.String == "champion" && charLevel >= 18 {
			if _, hasSurvivor := getSubclassMechanic("champion", charLevel, "survivor_regen"); hasSurvivor {
				// Survivor triggers if HP > 0 and HP <= 50% max HP
				if hp > 0 && hp <= maxHP/2 {
					conMod := (conScore - 10) / 2
					healAmount := 5 + conMod
					if healAmount < 1 {
						healAmount = 1 // Minimum 1 HP
					}
					newHP := hp + healAmount
					if newHP > maxHP {
						newHP = maxHP
					}
					db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", newHP, newActiveID)
					response["survivor_regen"] = map[string]interface{}{
						"feature":     "Survivor",
						"healed":      healAmount,
						"previous_hp": hp,
						"new_hp":      newHP,
						"max_hp":      maxHP,
						"message":     fmt.Sprintf("⚔️ Survivor: %s regenerates %d HP at start of turn (5 + CON %+d)", entries[turnIndex].Name, healAmount, conMod),
					}
				}
			}
		}
	}

	json.NewEncoder(w).Encode(response)
}

// handleCombatSkip godoc
// @Summary Skip a player's turn due to timeout (GM only)
// @Description Skip the current player's turn and advance to the next combatant. Use when a player has been inactive for too long.
// @Tags Combat
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Turn skipped"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can skip turns"
// @Router /campaigns/{id}/combat/skip [post]
func handleCombatSkip(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_skip_turns"})
		return
	}

	var round, turnIndex int
	var turnOrderJSON []byte
	var active bool
	var turnStartedAt sql.NullTime
	err = db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active, turn_started_at
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&round, &turnIndex, &turnOrderJSON, &active, &turnStartedAt)

	if err != nil || !active {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_active_combat"})
		return
	}

	type InitEntry struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		Initiative int    `json:"initiative"`
	}
	var entries []InitEntry
	json.Unmarshal(turnOrderJSON, &entries)

	if len(entries) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants"})
		return
	}
	if turnIndex < 0 || turnIndex >= len(entries) || entries[turnIndex].ID < 0 {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_player_turn"})
		return
	}
	if !turnStartedAt.Valid || time.Since(turnStartedAt.Time) < 4*time.Hour {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "turn_not_timed_out"})
		return
	}

	skippedName := entries[turnIndex].Name
	skippedID := entries[turnIndex].ID

	// Calculate how long the turn was
	elapsedMinutes := 0
	if turnStartedAt.Valid {
		elapsedMinutes = int(time.Since(turnStartedAt.Time).Minutes())
	}

	// Advance turn
	oldTurnIndex := turnIndex
	turnIndex++
	ids := make([]int, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	var wraps int
	turnIndex, wraps = nextLivingCombatTurn(turnIndex, ids, combatDeadFn(campaignID))
	round += wraps
	newRound := wraps > 0

	// The auto-skip worker may advance the turn after the read above. Only
	// commit this skip if the exact turn we inspected is still current.
	setLastTurnEnded(campaignID, fmt.Sprintf("%d:%d", round-wraps, oldTurnIndex), skippedID) // Legendary Actions may follow it
	advanced, err := advanceCombatSkipIfCurrent(campaignID, oldTurnIndex, turnIndex, round, turnStartedAt.Time)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "skip_failed"})
		return
	}
	if !advanced {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "turn_already_advanced"})
		return
	}
	if newRound {
		// Reset reactions only after the turn transition is committed.
		db.Exec(`UPDATE characters SET reaction_used = false WHERE lobby_id = $1`, campaignID)
	}
	// Record only a committed skip, never a stale request.
	db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, 'turn_skipped', 'Turn skipped by GM due to timeout', $3)
	`, campaignID, skippedID, fmt.Sprintf("Inactive for %d minutes", elapsedMinutes))

	// Reset action economy for the new active character
	newActiveID := entries[turnIndex].ID
	var species string
	db.QueryRow("SELECT species FROM characters WHERE id = $1", newActiveID).Scan(&species)
	speed := characterSpeed(newActiveID, species)
	resetTurnFlags(newActiveID, speed)

	// Clear Multiattack Defense tracking when turn is skipped
	clearAllMultiattackDefenseHits(campaignID)

	response := map[string]interface{}{
		"success":              true,
		"skipped":              skippedName,
		"inactive_minutes":     elapsedMinutes,
		"round":                round,
		"current_turn":         entries[turnIndex].Name,
		"turn_index":           turnIndex,
		"action_economy_reset": true,
	}
	if opts, task := openLegendaryOptions(campaignID); len(opts) > 0 { // after the skipped turn (monster_features_2024.go)
		response["legendary_options"] = opts
		response["legendary_note"] = task
	}

	if newRound {
		response["new_round"] = true
		response["reactions_reset"] = true
	}

	if notes := moreTurnHooks(campaignID, skippedID, newActiveID); len(notes) > 0 { // Blink, Regenerate ... (spells_more_2024.go)
		response["spell_turn_effects"] = notes
		for _, n := range notes {
			recordGMAction(db, campaignID, "spell_effect", n, n)
		}
	}
	// Champion's Survivor feature - regenerate HP at start of turn if below 50% (level 18+)
	var charClass, subclass sql.NullString
	var charLevel, hp, maxHP, conScore int
	err = db.QueryRow(`
		SELECT class, COALESCE(subclass, ''), level, hp, max_hp, con 
		FROM characters WHERE id = $1
	`, newActiveID).Scan(&charClass, &subclass, &charLevel, &hp, &maxHP, &conScore)
	if err == nil && charClass.Valid && subclass.Valid && subclass.String == "champion" && charLevel >= 18 {
		if _, hasSurvivor := getSubclassMechanic("champion", charLevel, "survivor_regen"); hasSurvivor {
			// Survivor triggers if HP > 0 and HP <= 50% max HP
			if hp > 0 && hp <= maxHP/2 {
				conMod := (conScore - 10) / 2
				healAmount := 5 + conMod
				if healAmount < 1 {
					healAmount = 1 // Minimum 1 HP
				}
				newHP := hp + healAmount
				if newHP > maxHP {
					newHP = maxHP
				}
				db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", newHP, newActiveID)
				response["survivor_regen"] = map[string]interface{}{
					"feature":     "Survivor",
					"healed":      healAmount,
					"previous_hp": hp,
					"new_hp":      newHP,
					"max_hp":      maxHP,
					"message":     fmt.Sprintf("⚔️ Survivor: %s regenerates %d HP at start of turn (5 + CON %+d)", entries[turnIndex].Name, healAmount, conMod),
				}
			}
		}
	}

	json.NewEncoder(w).Encode(response)
}

// advanceCombatSkipIfCurrent prevents a GM skip racing the automatic timeout
// worker from advancing the following combatant's turn instead.
func advanceCombatSkipIfCurrent(campaignID, oldIndex, newIndex, round int, startedAt time.Time) (bool, error) {
	result, err := db.Exec(`
		UPDATE combat_state SET current_turn_index = $1, round_number = $2, turn_started_at = $3
		WHERE lobby_id = $4 AND current_turn_index = $5 AND turn_started_at = $6 AND active = true
	`, newIndex, round, time.Now().UTC(), campaignID, oldIndex, startedAt)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err == nil && updated == 1 {
		endTurnEffects(campaignID, oldIndex)
		startTurnEffects(campaignID)
	}
	return err == nil && updated == 1, err
}

// handleExplorationStatus godoc
// @Summary Get exploration mode status
// @Description Returns exploration mode status including inactive players
// @Tags Exploration
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Exploration status"
// @Router /campaigns/{id}/exploration [get]
func handleExplorationStatus(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	// Check if in combat
	var combatActive bool
	db.QueryRow("SELECT active FROM combat_state WHERE lobby_id = $1", campaignID).Scan(&combatActive)

	if combatActive {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"mode":    "combat",
			"message": "Campaign is in combat mode. Use /api/campaigns/{id}/combat for combat status.",
		})
		return
	}

	// Get inactive players (12h+ without action)
	type InactivePlayer struct {
		ID            int       `json:"id"`
		Name          string    `json:"name"`
		InactiveHours int       `json:"inactive_hours"`
		LastActionAt  time.Time `json:"last_action_at"`
		SkipRequired  bool      `json:"skip_required"`
	}
	var inactivePlayers []InactivePlayer

	rows, err := db.Query(`
		SELECT c.id, c.name,
			COALESCE(
				(SELECT MAX(a.created_at) FROM actions a WHERE a.character_id = c.id AND a.action_type NOT IN ('poll', 'joined', 'following')),
				c.created_at
			) as last_action,
			(SELECT MAX(a.created_at) FROM actions a WHERE a.character_id = c.id AND a.action_type = 'following') as last_following_at
		FROM characters c
		WHERE c.lobby_id = $1
	`, campaignID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int
			var name string
			var lastAction time.Time
			var lastFollowing sql.NullTime
			rows.Scan(&id, &name, &lastAction, &lastFollowing)
			if isFollowingParty(sql.NullTime{Time: lastAction, Valid: true}, lastFollowing) {
				continue
			}

			inactiveDuration := time.Since(lastAction)
			inactiveHours := int(inactiveDuration.Hours())

			if inactiveHours >= 12 {
				inactivePlayers = append(inactivePlayers, InactivePlayer{
					ID:            id,
					Name:          name,
					InactiveHours: inactiveHours,
					LastActionAt:  lastAction,
					SkipRequired:  true,
				})
			}
		}
	}

	response := map[string]interface{}{
		"mode":             "exploration",
		"inactive_players": inactivePlayers,
		"skip_threshold":   "12 hours",
	}

	if len(inactivePlayers) > 0 {
		var names []string
		for _, p := range inactivePlayers {
			names = append(names, p.Name)
		}
		response["skip_required"] = true
		response["skip_required_players"] = names
		response["skip_endpoint"] = fmt.Sprintf("POST /api/campaigns/%d/exploration/skip", campaignID)
		response["instruction"] = "Use the skip endpoint with character_id to mark inactive players as following the party."
	}

	json.NewEncoder(w).Encode(response)
}

// handleExplorationSkip godoc
// @Summary Skip inactive player in exploration mode (GM only)
// @Description Mark an inactive player (12h+) as following the party. Records a 'following' action.
// @Tags Exploration
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=int} true "Character to skip"
// @Success 200 {object} map[string]interface{} "Player skipped"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can skip"
// @Router /campaigns/{id}/exploration/skip [post]
func handleExplorationSkip(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Verify GM
	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_skip"})
		return
	}

	// Check not in combat
	var combatActive bool
	db.QueryRow("SELECT active FROM combat_state WHERE lobby_id = $1", campaignID).Scan(&combatActive)
	if combatActive {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "in_combat",
			"message": "Use /api/campaigns/{id}/combat/skip for combat mode",
		})
		return
	}

	// Parse request
	var req struct {
		CharacterID int `json:"character_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	// Get character info
	var charName string
	var charLobbyID int
	err = db.QueryRow("SELECT name, lobby_id FROM characters WHERE id = $1", req.CharacterID).Scan(&charName, &charLobbyID)
	if err != nil || charLobbyID != campaignID {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Calculate inactivity from a meaningful player action. A prior automatic
	// follow is terminal until the player acts again, so repeated skip calls are
	// safe no-ops instead of continually creating stale GM work.
	var lastActionAt, lastFollowingAt sql.NullTime
	db.QueryRow(`
		SELECT
			MAX(created_at) FILTER (WHERE action_type NOT IN ('poll', 'joined', 'following')),
			MAX(created_at) FILTER (WHERE action_type = 'following')
		FROM actions
		WHERE character_id = $1
	`, req.CharacterID).Scan(&lastActionAt, &lastFollowingAt)

	inactiveMinutes := 0
	if lastActionAt.Valid {
		inactiveMinutes = int(time.Since(lastActionAt.Time).Minutes())
	}
	if isFollowingParty(lastActionAt, lastFollowingAt) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":           true,
			"skipped":           charName,
			"character_id":      req.CharacterID,
			"inactive_minutes":  inactiveMinutes,
			"already_following": true,
			"action_recorded":   "following",
			"message":           fmt.Sprintf("%s is already following the party", charName),
		})
		return
	}

	// Record the skip as a "following" action
	db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, 'following', 'Marked as following the party (exploration skip)', $3)
	`, campaignID, req.CharacterID, fmt.Sprintf("Inactive for %d minutes, defaulting to follow party", inactiveMinutes))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":          true,
		"skipped":          charName,
		"character_id":     req.CharacterID,
		"inactive_minutes": inactiveMinutes,
		"action_recorded":  "following",
		"message":          fmt.Sprintf("%s is now following the party (inactive %d hours)", charName, inactiveMinutes/60),
	})
}

// handleCombatAdd godoc
// @Summary Add combatants to combat (GM only)
// @Description Add monsters or NPCs to an active combat encounter
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{combatants=[]object} true "Combatants to add (name, monster_key, initiative, surprised, hp, ac, in_lair). Identical monsters added together share one Initiative roll; surprised gives Disadvantage on it; in_lair gives a legendary monster the in-Lair uses of Legendary Actions and Legendary Resistance."
// @Success 200 {object} map[string]interface{} "Combatants added"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can add combatants"
// @Router /campaigns/{id}/combat/add [post]
func handleCombatAdd(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Check if user is GM
	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_add_combatants"})
		return
	}

	// Check combat is active
	var round, turnIndex int
	var turnOrderJSON []byte
	var active bool
	err = db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active 
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&round, &turnIndex, &turnOrderJSON, &active)

	if err != nil || !active {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_active_combat", "hint": "Start combat first with POST /api/campaigns/{id}/combat/start"})
		return
	}

	// Parse request
	var req struct {
		Combatants []struct {
			Name       string `json:"name"`
			MonsterKey string `json:"monster_key"` // SRD monster slug (e.g., "goblin")
			Initiative int    `json:"initiative"`  // Optional: roll if not provided
			Surprised  bool   `json:"surprised"`   // Optional: surprised by combat starting (Disadvantage on Initiative)
			HP         int    `json:"hp"`          // Optional: use monster default
			AC         int    `json:"ac"`          // Optional: use monster default
			InLair     bool   `json:"in_lair"`     // Optional: the monster fights in its lair (one more Legendary Action use and Legendary Resistance)
		} `json:"combatants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if len(req.Combatants) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants_provided"})
		return
	}

	// Parse current turn order
	type InitEntry struct {
		ID                   int    `json:"id"`
		Name                 string `json:"name"`
		Initiative           int    `json:"initiative"`
		DexScore             int    `json:"dex_score"`
		IsMonster            bool   `json:"is_monster"`
		MonsterKey           string `json:"monster_key"`
		HP                   int    `json:"hp"`
		MaxHP                int    `json:"max_hp"`
		AC                   int    `json:"ac"`
		IsThiefsReflexesTurn bool   `json:"is_thiefs_reflexes_turn,omitempty"`
	}
	var entries []InitEntry
	if err := json.Unmarshal(turnOrderJSON, &entries); err != nil || len(entries) == 0 {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants"})
		return
	}
	if turnIndex < 0 || turnIndex >= len(entries) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_combat_turn"})
		return
	}

	// Keep original JSON for existing combatants. The typed projection is only
	// used for sorting; serializing it would discard conditions and future fields.
	var originalEntries []json.RawMessage
	if err := json.Unmarshal(turnOrderJSON, &originalEntries); err != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants"})
		return
	}
	// A Thief with Thief's Reflexes has two entries with the same id in the first round (the normal turn and the second turn),
	// so an entry is known by its id and whether it is the extra turn; keying by id alone overwrote the normal turn.
	type turnKey struct {
		id    int
		extra bool
	}
	originalByID := make(map[turnKey]json.RawMessage, len(entries))
	for i, entry := range entries {
		originalByID[turnKey{entry.ID, entry.IsThiefsReflexesTurn}] = originalEntries[i]
	}

	// Names need not be unique (for example, several Goblins). Preserve identity.
	currentTurnID := entries[turnIndex].ID
	currentTurnExtra := entries[turnIndex].IsThiefsReflexesTurn

	// Find highest existing monster ID (monsters use negative IDs)
	minID := 0
	for _, e := range entries {
		if e.ID < minID {
			minID = e.ID
		}
	}

	added := []map[string]interface{}{}
	groupInit := map[string]int{}
	var lairIDs []int

	for _, c := range req.Combatants {
		if c.Name == "" {
			continue
		}

		entry := InitEntry{
			ID:         minID - 1, // Decrement for each new monster
			Name:       c.Name,
			IsMonster:  true,
			MonsterKey: c.MonsterKey,
		}
		minID--

		// Look up monster stats if key provided
		if c.MonsterKey != "" {
			var dex, hp, ac int
			err := db.QueryRow(`
				SELECT COALESCE(dex, 10), COALESCE(hp, 10), COALESCE(ac, 10)
				FROM monsters WHERE slug = $1
			`, c.MonsterKey).Scan(&dex, &hp, &ac)
			if err == nil {
				// Roll initiative based on monster DEX if not provided
				if c.Initiative == 0 {
					// A group of identical creatures shares one Initiative roll (one roll per monster kind per request).
					groupKey := fmt.Sprintf("%s#%v", c.MonsterKey, c.Surprised)
					if v, ok := groupInit[groupKey]; ok {
						entry.Initiative = v
					} else {
						d := game.RollDie(20)
						if c.Surprised {
							if d2 := game.RollDie(20); d2 < d {
								d = d2
							}
						}
						entry.Initiative = d + game.Modifier(dex)
						groupInit[groupKey] = entry.Initiative
					}
				} else {
					entry.Initiative = c.Initiative
				}
				entry.DexScore = dex

				// Use provided HP/AC or monster defaults
				if c.HP > 0 {
					entry.HP = c.HP
					entry.MaxHP = c.HP
				} else {
					entry.HP = hp
					entry.MaxHP = hp
				}
				if c.AC > 0 {
					entry.AC = c.AC
				} else {
					entry.AC = ac
				}

				if c.InLair { // in its lair: the "in Lair" numbers of Legendary Actions and Legendary Resistance (monster_features_2024.go)
					lairIDs = append(lairIDs, entry.ID)
				}
			} else {
				// Monster not found, use provided or defaults
				if c.Initiative == 0 {
					entry.Initiative = game.RollDie(20)
				} else {
					entry.Initiative = c.Initiative
				}
				entry.HP = 10
				entry.MaxHP = 10
				entry.AC = 10
				if c.HP > 0 {
					entry.HP = c.HP
					entry.MaxHP = c.HP
				}
				if c.AC > 0 {
					entry.AC = c.AC
				}
			}
		} else {
			// No monster key, use provided values or defaults
			if c.Initiative == 0 {
				entry.Initiative = game.RollDie(20)
			} else {
				entry.Initiative = c.Initiative
			}
			entry.HP = 10
			entry.MaxHP = 10
			entry.AC = 10
			if c.HP > 0 {
				entry.HP = c.HP
				entry.MaxHP = c.HP
			}
			if c.AC > 0 {
				entry.AC = c.AC
			}
		}

		entries = append(entries, entry)
		added = append(added, map[string]interface{}{
			"id":         entry.ID,
			"name":       entry.Name,
			"initiative": entry.Initiative,
			"hp":         entry.HP,
			"ac":         entry.AC,
		})
	}

	// Re-sort by initiative (highest first), then by DEX (highest first)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Initiative != entries[j].Initiative {
			return entries[i].Initiative > entries[j].Initiative
		}
		return entries[i].DexScore > entries[j].DexScore
	})

	// Find where the current turn holder ended up after re-sort
	newTurnIndex := 0
	for i, e := range entries {
		if e.ID == currentTurnID && e.IsThiefsReflexesTurn == currentTurnExtra {
			newTurnIndex = i
			break
		}
	}

	// Preserve untouched entries in persistence and response after sorting.
	preservedEntries := make([]json.RawMessage, len(entries))
	for i, entry := range entries {
		if original, ok := originalByID[turnKey{entry.ID, entry.IsThiefsReflexesTurn}]; ok {
			preservedEntries[i] = original
		} else {
			preservedEntries[i], _ = json.Marshal(entry)
		}
	}

	// Update combat state
	updatedJSON, _ := json.Marshal(preservedEntries)
	db.Exec(`
		UPDATE combat_state SET turn_order = $1, current_turn_index = $2 WHERE lobby_id = $3
	`, updatedJSON, newTurnIndex, campaignID)
	for _, id := range lairIDs {
		s := loadMonsterState(campaignID, id)
		s.InLair = true
		saveMonsterState(campaignID, id, s)
	}
	for _, a := range added { // Legendary Actions, Legendary Resistance, auras the server runs for this monster
		if f := monsterFeaturesInfo(campaignID, asInt(a["id"])); f != nil {
			a["features"] = f
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":          true,
		"added_count":      len(added),
		"combatants_added": added,
		"turn_order":       preservedEntries,
		"current_turn":     entries[newTurnIndex].Name,
	})
}

// handleCombatRemove godoc
// @Summary Remove combatant from combat (GM only)
// @Description Remove a monster or NPC from combat (for death, flee, etc.)
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{combatant_id=integer,combatant_name=string} true "ID or name of combatant to remove"
// @Success 200 {object} map[string]interface{} "Combatant removed"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can remove combatants"
// @Router /campaigns/{id}/combat/remove [post]
func handleCombatRemove(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Check if user is GM
	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_remove_combatants"})
		return
	}

	// Check combat is active
	var round, turnIndex int
	var turnOrderJSON []byte
	var active bool
	err = db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active 
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&round, &turnIndex, &turnOrderJSON, &active)

	if err != nil || !active {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_active_combat"})
		return
	}

	// Parse request
	var req struct {
		CombatantID   int    `json:"combatant_id"`
		CombatantName string `json:"combatant_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CombatantID == 0 && req.CombatantName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "must_provide_combatant_id_or_name"})
		return
	}

	// Parse turn order
	type InitEntry struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		Initiative int    `json:"initiative"`
		DexScore   int    `json:"dex_score"`
		IsMonster  bool   `json:"is_monster"`
		MonsterKey string `json:"monster_key"`
		HP         int    `json:"hp"`
		MaxHP      int    `json:"max_hp"`
		AC         int    `json:"ac"`
	}
	var entries []InitEntry
	if err := json.Unmarshal(turnOrderJSON, &entries); err != nil || len(entries) == 0 {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants"})
		return
	}
	if turnIndex < 0 || turnIndex >= len(entries) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_combat_turn"})
		return
	}

	// Find and remove the combatant
	// Keep the original JSON for retained combatants: the projection above
	// omits legendary resources and may not know about future metadata.
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(turnOrderJSON, &rawEntries); err != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants"})
		return
	}
	retainedEntries := []json.RawMessage{}
	var removed *InitEntry
	var removedIdx int
	newEntries := []InitEntry{}
	for i, e := range entries {
		match := false
		if req.CombatantID != 0 && e.ID == req.CombatantID {
			match = true
		} else if req.CombatantName != "" && strings.EqualFold(e.Name, req.CombatantName) {
			match = true
		}

		if match && removed == nil {
			removed = &e
			removedIdx = i
		} else {
			newEntries = append(newEntries, e)
			retainedEntries = append(retainedEntries, rawEntries[i])
		}
	}

	if removed == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "combatant_not_found"})
		return
	}

	// Adjust turn index if needed
	newTurnIndex := turnIndex
	if removedIdx < turnIndex {
		newTurnIndex-- // Removed someone before current turn
	} else if removedIdx == turnIndex {
		// Removed current turn holder - stay at same index (next in line becomes current)
		if newTurnIndex >= len(newEntries) && len(newEntries) > 0 {
			newTurnIndex = 0
			round++ // Wrapped around
		}
	}

	if len(newEntries) == 0 {
		// No combatants left, end combat
		if err := clearCombatInitiative(campaignID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "combat_end_failed"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"removed":      removed.Name,
			"combat_ended": true,
			"message":      "All combatants removed, combat ended",
		})
		return
	}

	// Update combat state
	updatedJSON, _ := json.Marshal(retainedEntries)
	db.Exec(`
		UPDATE combat_state SET turn_order = $1, current_turn_index = $2, round_number = $3 WHERE lobby_id = $4
	`, updatedJSON, newTurnIndex, round, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"removed":      removed.Name,
		"removed_id":   removed.ID,
		"turn_order":   retainedEntries,
		"current_turn": newEntries[newTurnIndex].Name,
	})
}

// handleCombatStatus godoc
// @Summary Get combat status
// @Description Get current combat state including initiative order and whose turn it is
// @Tags Combat
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} map[string]interface{} "Combat status"
// @Router /campaigns/{id}/combat [get]
func handleCombatStatus(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	var round, turnIndex int
	var turnOrderJSON []byte
	var active bool
	err := db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active 
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&round, &turnIndex, &turnOrderJSON, &active)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"in_combat": false,
			"message":   "No combat active",
		})
		return
	}

	type InitEntry struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		Initiative int    `json:"initiative"`
	}
	var entries []InitEntry
	json.Unmarshal(turnOrderJSON, &entries)

	currentTurn := ""
	currentID := 0
	if len(entries) > turnIndex {
		currentTurn = entries[turnIndex].Name
		currentID = entries[turnIndex].ID
	}

	resp := map[string]interface{}{
		"in_combat":          active,
		"round":              round,
		"turn_order":         entries,
		"current_turn":       currentTurn,
		"current_turn_id":    currentID,
		"current_turn_index": turnIndex,
	}
	if requestIsGMOf(r, campaignID) {
		resp["combatants"] = gmCombatants(turnOrderJSON, campaignID) // GM only: AC, HP and stat blocks
		var comps []map[string]interface{}
		for _, c := range campaignCompanions(campaignID) {
			v := companionView(c)
			v["owner_id"] = c.OwnerID
			comps = append(comps, v)
		}
		resp["companions"] = comps // the party's familiars, steeds, summons and pets (companions_2024.go)
	}
	json.NewEncoder(w).Encode(resp)
}

// handleDamage godoc
// @Summary Apply damage to a character (GM only)
// @Description Deal damage to a character, tracking HP, temp HP, death saves. A concentrating character makes the Constitution save at once (DC 10 or half the damage, up to 30): concentration, concentration_kept; on a failure the spell and what it holds end. Only the GM of the character's campaign (403 gm_only otherwise).
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{damage=integer,damage_type=string,concentration_damage=integer,skip_concentration=boolean} true "Damage to apply. One source in several damage types makes one Concentration save: send the whole damage as concentration_damage with the first part and skip_concentration with the others"
// @Success 200 {object} map[string]interface{} "Damage applied"
// @Router /characters/{id}/damage [post]
func handleDamage(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Damage     int    `json:"damage"`
		Amount     int    `json:"amount"`
		DamageType string `json:"damage_type"`
		// one source of damage in several types (a hit's "plus 10 (3d6) Fire damage") makes one Concentration save: the first part
		// carries the whole damage for its DC, the others skip the save
		ConcentrationDamage int  `json:"concentration_damage"`
		SkipConcentration   bool `json:"skip_concentration"`
		NoBond              bool `json:"no_bond"`  // a Warding Bond's shared damage (not shared again)
		Critical            bool `json:"critical"` // the damage is from a Critical Hit (two Death Saving Throw failures at 0 Hit Points)
		SameHit             bool `json:"same_hit"` // another damage type of a hit already counted (one failure per hit at 0 Hit Points)
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Damage <= 0 && req.Amount > 0 {
		req.Damage = req.Amount
	}

	if req.Damage <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "damage_must_be_positive"})
		return
	}

	var hp, maxHP, tempHP int
	var concentratingOn string
	var wildShapeForm sql.NullString
	err := db.QueryRow(`
		SELECT hp, max_hp, COALESCE(temp_hp, 0), COALESCE(concentrating_on, ''),
		       wild_shape_form
		FROM characters WHERE id = $1
	`, charID).Scan(&hp, &maxHP, &tempHP, &concentratingOn, &wildShapeForm)

	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	damage := req.Damage
	result := map[string]interface{}{
		"original_damage": damage,
	}

	// Apply damage resistance from conditions
	dmgMod := applyDamageResistance(charID, damage, req.DamageType)
	if dmgMod.WasHalved {
		damage = dmgMod.FinalDamage
		result["resistances_applied"] = dmgMod.Resistances
		result["damage_dealt"] = damage
	} else {
		if d, ok := underwaterFire(lobbyOfChar(charID), req.DamageType, damage); ok {
			damage = d
			result["resistances_applied"] = []string{"fire (underwater)"}
		}
		result["damage_dealt"] = damage
	}
	if damage > 0 {
		cloakTakesDamage(charID) // the Cloak of Displacement stops until the start of the wearer's next turn (F113)
		if note := wakeOnDamage(lobbyOfChar(charID), charID, getCharacterName(charID)); note != "" {
			result["woke_up"] = note // Hypnotic Pattern, Sleep
		}
	}
	if cut, note := ringOfWarmth(charID, req.DamageType); cut > 0 { // Ring of Warmth: Cold damage 2d8 less (F204)
		damage -= cut
		if damage < 0 {
			damage = 0
		}
		result["damage_dealt"] = damage
		result["ring_of_warmth"] = note
	}
	if cut, note := resistanceSpellReduction(charID, req.DamageType); cut > 0 { // the Resistance cantrip: 1d4 less, once per turn
		damage -= cut
		if damage < 0 {
			damage = 0
		}
		result["damage_dealt"] = damage
		result["resistance_spell"] = note
	}

	if !req.NoBond {
		if note := wardingBondShare(charID, damage); note != "" { // its caster takes the same damage (buff_spells_2024.go)
			result["warding_bond"] = strings.TrimSpace(note)
		}
	}

	// Wild Shape (2024): no beast Hit Points; the druid's Temporary Hit Points and Hit Points take the damage, and the form ends at 0 HP

	// Apply to temp HP first
	if tempHP > 0 {
		if damage <= tempHP {
			tempHP -= damage
			damage = 0
		} else {
			damage -= tempHP
			tempHP = 0
		}
		result["temp_hp_absorbed"] = req.Damage - damage
	}

	// Damage at 0 Hit Points (SRD 5.2.1): a Death Saving Throw failure, two from a Critical Hit; damage equal to the Hit Point maximum or
	// more kills; a Stable creature stops being Stable (F93: the server only set the Hit Points to 0 again)
	if hp <= 0 && damage > 0 {
		var fails int
		var dead bool
		db.QueryRow(`SELECT COALESCE(death_save_failures,0), COALESCE(is_dead,false) FROM characters WHERE id = $1`, charID).Scan(&fails, &dead)
		result["damage_dealt"] = damage
		result["hp"], result["max_hp"], result["temp_hp"] = 0, maxHP, tempHP
		switch {
		case dead:
			result["status"] = "already_dead"
		case damage >= maxHP:
			db.Exec(`UPDATE characters SET hp = 0, temp_hp = $1, is_dead = true, is_stable = false WHERE id = $2`, tempHP, charID)
			result["status"] = "INSTANT_DEATH"
			result["message"] = "Damage at 0 Hit Points equal to the Hit Point maximum or more: the character dies."
		case req.SameHit:
			db.Exec(`UPDATE characters SET temp_hp = $1 WHERE id = $2`, tempHP, charID)
			result["status"] = "unconscious"
		default:
			n := 1
			if req.Critical {
				n = 2
			}
			fails += n
			result["death_save_failures"] = fails
			if fails >= 3 {
				db.Exec(`UPDATE characters SET hp = 0, temp_hp = $1, death_save_failures = $2, is_dead = true, is_stable = false WHERE id = $3`, tempHP, fails, charID)
				result["status"] = "dead"
				result["message"] = fmt.Sprintf("Damage at 0 Hit Points: %d Death Saving Throw failure%s (%d in all). The character dies.", n, map[bool]string{true: "s", false: ""}[n > 1], fails)
			} else {
				db.Exec(`UPDATE characters SET hp = 0, temp_hp = $1, death_save_failures = $2, is_stable = false WHERE id = $3`, tempHP, fails, charID)
				result["status"] = "unconscious"
				result["message"] = fmt.Sprintf("Damage at 0 Hit Points: %d Death Saving Throw failure%s (%d of 3); the character isn't Stable.", n, map[bool]string{true: "s", false: ""}[n > 1], fails)
			}
		}
		json.NewEncoder(w).Encode(result)
		return
	}

	// Apply remaining to HP
	hp -= damage

	// Check for unconscious/death
	if hp <= 0 {
		if dwUsed, dwNote := deathWardHolds(charID); dwUsed { // Death Ward: 1 Hit Point instead of 0 (buff_spells_2024.go)
			hp = 1
			db.Exec("UPDATE characters SET hp = 1, temp_hp = $1 WHERE id = $2", tempHP, charID)
			result["status"] = "death_ward"
			result["message"] = dwNote
		} else if hp <= -maxHP {
			// Massive damage - instant death
			db.Exec("UPDATE characters SET hp = 0, temp_hp = $1, is_dead = true WHERE id = $2", tempHP, charID)
			result["status"] = "INSTANT_DEATH"
			result["message"] = "Massive damage (damage exceeded max HP) - instant death!"
			hp = 0
		} else {
			// Check Barbarian Relentless Rage first (requires CON save)
			relentlessHP, relentlessUsed, relentlessMsg := checkRelentlessRage(charID, hp+damage, damage, maxHP)
			if relentlessUsed {
				hp = relentlessHP
				db.Exec("UPDATE characters SET hp = $1, temp_hp = $2 WHERE id = $3", hp, tempHP, charID)
				result["status"] = "relentless_rage"
				result["message"] = relentlessMsg
				result["relentless_rage"] = true
				result["class_feature_note"] = relentlessMsg
			} else {
				// Add failed Relentless Rage message if applicable
				if relentlessMsg != "" {
					result["relentless_rage_failed"] = true
					result["class_feature_note"] = relentlessMsg
				}

				// Check Orc Relentless Endurance before falling unconscious
				enduranceHP, enduranceUsed, enduranceMsg := checkRelentlessEndurance(charID, hp+damage, damage, maxHP)
				if enduranceUsed {
					hp = enduranceHP
					db.Exec("UPDATE characters SET hp = $1, temp_hp = $2 WHERE id = $3", hp, tempHP, charID)
					result["status"] = "relentless_endurance"
					result["message"] = enduranceMsg
					result["relentless_endurance"] = true
					result["racial_feature_note"] = enduranceMsg
				} else if note := giftOfTheProtectors(charID); note != "" {
					hp = 1 // a name in a Warlock's Book of Shadows
					result["status"] = "gift_of_the_protectors"
					result["message"] = note
				} else {
					// Fall unconscious, start death saves
					db.Exec("UPDATE characters SET hp = 0, temp_hp = $1, concentrating_on = NULL WHERE id = $2", tempHP, charID)
					result["status"] = "unconscious"
					result["message"] = "Dropped to 0 HP - unconscious and making death saves"
					hp = 0
				}
			}
		}
	} else {
		db.Exec("UPDATE characters SET hp = $1, temp_hp = $2 WHERE id = $3", hp, tempHP, charID)
		result["status"] = "damaged"
	}

	result["hp"] = hp
	result["max_hp"] = maxHP
	if hp <= 0 {
		endWardingBondsOf(charID) // a Warding Bond ends when its caster drops to 0 Hit Points
		if n, _ := db.Exec(`DELETE FROM spell_effects WHERE target_id = $1 AND kind = 'gaseous_form'`, charID); n != nil {
			if c, _ := n.RowsAffected(); c > 0 {
				result["gaseous_form_ended"] = "Gaseous Form ends: the creature dropped to 0 Hit Points."
			}
		}
	}
	if note := polymorphTempGone(charID); note != "" {
		result["polymorph_ended"] = note // the beast's Temporary Hit Points are gone (spells_more_2024.go)
		wildShapeForm.Valid = false
	}
	if wildShapeForm.Valid && wildShapeForm.String != "" {
		if note := endWildShapeIfDown(charID); note != "" {
			result["wild_shape_ended"] = note // 0 Hit Points: Unconscious, so Incapacitated; the druid's own scores and AC come back
		}
	}
	result["temp_hp"] = tempHP

	// Concentration check if concentrating
	if concentratingOn != "" && hp > 0 && !req.SkipConcentration && !relentlessHunter(charID, concentratingOn) { // Ranger level 13: damage can't break Hunter's Mark
		dc := 10
		concDamage := req.Damage
		if req.ConcentrationDamage > concDamage {
			concDamage = req.ConcentrationDamage
		}
		if concDamage/2 > 10 {
			dc = concDamage / 2
		}
		if dc > 30 {
			dc = 30 // SRD 5.2.1: up to a maximum DC of 30
		}
		// F55: the server rolls the Constitution save (before, it only said one was required, and nobody rolled it)
		kept, total := characterSave(charID, "con", dc)
		how := ""
		if !kept && (hasInvocation(charID, "eldritch-mind") || charHasWarCaster(charID) || extendedConcentration(charID)) { // Eldritch Mind, War Caster, Extended Spell (F194): Advantage on saves to keep Concentration
			kept, total = characterSave(charID, "con", dc)
			how = " (Eldritch Mind: Advantage)"
			if !hasInvocation(charID, "eldritch-mind") {
				how = " (War Caster: Advantage)"
				if !charHasWarCaster(charID) {
					how = " (Extended Spell: Advantage)"
				}
			}
		}
		result["concentration_dc"] = dc
		result["concentrating_on"] = concentratingOn
		result["concentration_save"] = total
		result["concentration_kept"] = kept
		if kept {
			result["concentration"] = fmt.Sprintf("Concentration save (DC %d): %d%s, kept: %s goes on.", dc, total, how, concentratingOn)
		} else {
			db.Exec(`UPDATE characters SET concentrating_on = NULL WHERE id = $1`, charID)
			result["concentration"] = fmt.Sprintf("Concentration save (DC %d): %d%s, failed: %s ends.", dc, total, how, concentratingOn)
		}
	}

	json.NewEncoder(w).Encode(result)
}

// handleHeal godoc
// @Summary Heal a character (GM only)
// @Description Restore HP to a character. Only the GM of the character's campaign (403 gm_only otherwise).
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{healing=integer} true "Healing amount"
// @Success 200 {object} map[string]interface{} "Healing applied"
// @Router /characters/{id}/heal [post]
func handleHeal(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Healing int `json:"healing"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var hp, maxHP int
	var isStable bool
	db.QueryRow("SELECT hp, max_hp, COALESCE(is_stable, false) FROM characters WHERE id = $1", charID).Scan(&hp, &maxHP, &isStable)

	wasUnconscious := hp == 0
	hp += req.Healing
	if hp > maxHP {
		hp = maxHP
	}

	// Reset death saves if healed from 0
	if wasUnconscious {
		db.Exec("UPDATE characters SET hp = $1, death_save_successes = 0, death_save_failures = 0, is_stable = false WHERE id = $2", hp, charID)
	} else {
		db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", hp, charID)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":                true,
		"hp":                     hp,
		"max_hp":                 maxHP,
		"healing_applied":        req.Healing,
		"regained_consciousness": wasUnconscious && hp > 0,
	})
}

// handleAddCondition godoc
// @Summary Add a condition to a character (GM only)
// @Description Apply a condition like frightened, poisoned, prone, etc.
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{condition=string} true "Condition to add"
// @Success 200 {object} map[string]interface{} "Condition added"
// @Router /characters/{id}/conditions [post]
func handleAddCondition(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Condition        string `json:"condition"`
		FromMagicalSleep bool   `json:"from_magical_sleep"` // for Sleep spell effects
		FromElemental    bool   `json:"from_elemental"`     // for Nature's Ward immunity
		FromFey          bool   `json:"from_fey"`           // for Nature's Ward immunity
	}
	json.NewDecoder(r.Body).Decode(&req)

	condition := strings.ToLower(req.Condition)

	// Fey Ancestry - Magic can't put you to sleep
	// Elves and half-elves are immune to magical sleep effects like the Sleep spell
	if req.FromMagicalSleep && isImmuneToMagicalSleep(charID) {
		charName := getCharacterName(charID)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         true,
			"immune":          true,
			"immunity_source": "Fey Ancestry (Elf)",
			"character":       charName,
			"character_id":    charID,
			"condition":       condition,
			"message":         fmt.Sprintf("✨ %s is immune to magical sleep through Fey Ancestry! The Sleep spell has no effect.", charName),
		})
		return
	}

	// Validate condition - allow parameterized conditions like "charmed:123" or "grappled:123"
	baseCondition := condition
	paramID := 0
	if idx := strings.Index(condition, ":"); idx != -1 {
		baseCondition = condition[:idx]
		if id, err := strconv.Atoi(condition[idx+1:]); err == nil {
			paramID = id
		}
	}

	if _, valid := conditionEffects[baseCondition]; !valid {
		validConditions := make([]string, 0, len(conditionEffects))
		for k := range conditionEffects {
			validConditions = append(validConditions, k)
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":            "invalid_condition",
			"message":          "Use format 'condition' or 'condition:character_id' for charmed/grappled",
			"valid_conditions": validConditions,
		})
		return
	}

	// For charmed/grappled with ID, validate the ID exists
	if paramID > 0 && (baseCondition == "charmed" || baseCondition == "grappled") {
		var exists bool
		db.QueryRow("SELECT EXISTS(SELECT 1 FROM characters WHERE id = $1)", paramID).Scan(&exists)
		if !exists {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_target",
				"message": fmt.Sprintf("Character ID %d not found", paramID),
			})
			return
		}
	}

	var condJSON []byte
	db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&condJSON)
	var conditions []string
	json.Unmarshal(condJSON, &conditions)

	// Check if already has condition
	for _, c := range conditions {
		if c == condition {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":    true,
				"message":    "Already has condition",
				"conditions": conditions,
			})
			return
		}
	}

	if strings.EqualFold(baseCondition, "frightened") && hasSpellEffect(lobbyOfChar(charID), charID, "heroism") {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true, "immune": true, "immunity_source": "Heroism", "character": getCharacterName(charID), "character_id": charID,
			"condition": condition, "message": "Heroism makes the creature immune to the Frightened condition.",
		})
		return
	}

	// 2024 Nature's Ward (Circle of the Land, level 10): immune to the Poisoned condition
	if baseCondition == "poisoned" && natureWardImmune(charID) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         true,
			"immune":          true,
			"immunity_source": "Nature's Ward (Circle of the Land Druid level 10+)",
			"character":       getCharacterName(charID),
			"character_id":    charID,
			"condition":       condition,
			"message":         "🌿 The Poisoned condition has no effect through Nature's Ward.",
		})
		return
	}

	// Check for Paladin aura immunities before applying charm/frightened
	if baseCondition == "charmed" || baseCondition == "frightened" {
		var class string
		var level int
		var subclass sql.NullString
		err := db.QueryRow(`
			SELECT COALESCE(class, ''), COALESCE(level, 1), subclass 
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &level, &subclass)

		if err == nil {
			classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
			charName := getCharacterName(charID)

			// Aura of Courage (Paladin 10) and Aura of Devotion (Devotion 7) protect everyone in the Aura of Protection
			if immune, source := auraImmune(lobbyOfChar(charID), charID, baseCondition); immune {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true, "immune": true, "immunity_source": source, "character": charName, "character_id": charID, "condition": condition,
					"message": fmt.Sprintf("%s is in a Paladin's Aura of Protection: %s makes them immune to the %s condition.", charName, source, baseCondition),
				})
				return
			}

			// Berserker's Mindless Rage - immune to charm/frightened while raging
			if classKey == "barbarian" && level >= 6 {
				if subclass.Valid && subclass.String == "berserker" {
					// Check if currently raging
					isRaging := false
					for _, c := range conditions {
						if c == "raging" {
							isRaging = true
							break
						}
					}
					if isRaging {
						json.NewEncoder(w).Encode(map[string]interface{}{
							"success":         true,
							"immune":          true,
							"immunity_source": "Mindless Rage (Berserker Barbarian level 6+ while raging)",
							"character":       charName,
							"character_id":    charID,
							"condition":       condition,
							"message":         fmt.Sprintf("⚔️ %s is immune to being %s through Mindless Rage! Their fury cannot be swayed.", charName, baseCondition),
						})
						return
					}
				}
			}

			// Nature's Ward - Land Druid level 10+ immune to charm/frighten from elementals/fey
			// "At 10th level, you can't be charmed or frightened by elementals or fey"
		}
	}

	conditions = append(conditions, condition)
	updated, _ := json.Marshal(conditions)
	db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updated, charID)
	syncRecordsFromList(charID)

	response := map[string]interface{}{
		"success":    true,
		"condition":  condition,
		"effect":     conditionEffects[baseCondition],
		"conditions": conditions,
	}

	// Auto-release grapples if character becomes incapacitated
	// SRD 5.2.1, Ending a Grapple: the condition also ends if the grappler has the Incapacitated condition (or if the
	// distance between the Grappled target and the grappler exceeds the grapple's range)
	if isIncapacitatingCondition(condition) {
		released := releaseAllGrapplesFrom(charID)
		if len(released) > 0 {
			response["grapples_released"] = released
			response["grapple_note"] = fmt.Sprintf("Grapple(s) ended because %s became incapacitated", getCharacterName(charID))
		}
	}

	// Auto-prone when becoming unconscious
	// SRD 5.2.1, Unconscious: you have the Incapacitated and Prone conditions
	if baseCondition == "unconscious" {
		hasProne := false
		for _, c := range conditions {
			if c == "prone" {
				hasProne = true
				break
			}
		}
		if !hasProne {
			conditions = append(conditions, "prone")
			updated, _ := json.Marshal(conditions)
			db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updated, charID)
			syncRecordsFromList(charID)
			response["conditions"] = conditions
			response["auto_prone"] = true
			response["prone_note"] = fmt.Sprintf("%s falls prone (unconscious creatures automatically fall prone)", getCharacterName(charID))
		}

		// Drop held items when becoming unconscious
		// Per 5e: "An unconscious creature drops whatever it's holding"
		droppedItems := dropHeldItems(charID)
		if len(droppedItems) > 0 {
			response["dropped_items"] = droppedItems
			response["drop_note"] = fmt.Sprintf("%s drops held items (unconscious creatures drop whatever they're holding)", getCharacterName(charID))
		}
	}

	json.NewEncoder(w).Encode(response)
}

// handleShortRest godoc
// @Summary Take a Short Rest
// @Description A Short Rest (1 hour; the spell clock passes 1 hour). Spend Hit Point Dice with hit_dice (each die is rolled and adds the Constitution modifier); what comes back on a Short Rest comes back (Pact Magic slots, class resources such as Focus Points, Second Wind, Channel Divinity, Wild Shape, species uses). A Wizard's Arcane Recovery and a Land Druid's Natural Recovery take recover_slots, the slot levels to recover (for example [1, 2]). A POST always takes the rest, with or without hit_dice; a GET only shows the Hit Point Dice left. Only the character's owner or the campaign's GM.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{hit_dice=int,recover_slots=[]int} false "Hit Point Dice to spend; slots for Arcane Recovery or Natural Recovery"
// @Router /characters/{id}/short-rest [post]
func handleShortRest(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	// Parse request - how many hit dice to spend, optional slot recovery
	var req struct {
		HitDice      int    `json:"hit_dice"`
		Die          string `json:"die"`           // multiclass: which Hit Point Die to spend first ("d10"); the largest first otherwise
		RecoverSlots []int  `json:"recover_slots"` // Array of slot levels to recover (e.g., [1, 2] = recover one 1st and one 2nd level slot)
	}
	if r.Body == nil {
		req.HitDice = 0
	} else if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.HitDice = 0 // Default to 0 if not specified (don't spend hit dice unless requested)
	}

	// Get character info including subclass for Natural Recovery, class_levels for multiclass, and lobby_id for the spell clock
	var class string
	var level, hp, maxHP, con, hitDiceSpent int
	var subclass sql.NullString
	var classLevelsJSON []byte
	var lobbyID sql.NullInt64
	err := db.QueryRow(`
		SELECT class, level, hp, max_hp, con, COALESCE(hit_dice_spent, 0), subclass, COALESCE(class_levels, '{}'), lobby_id
		FROM characters WHERE id = $1
	`, charID).Scan(&class, &level, &hp, &maxHP, &con, &hitDiceSpent, &subclass, &classLevelsJSON, &lobbyID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Character not found",
		})
		return
	}

	// Parse class_levels for multiclass detection
	classLevels := make(map[string]int)
	json.Unmarshal(classLevelsJSON, &classLevels)

	// Calculate available hit dice (total = level, available = level - spent)
	hitDiceAvailable := level - hitDiceSpent

	// GET shows the Hit Point Dice; a POST is a Short Rest even when it spends none (what comes back on a Short Rest comes back)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":            true,
			"hit_dice_available": hitDiceAvailable,
			"hit_dice_total":     level,
			"hit_die_type":       fmt.Sprintf("d%d", game.HitDie(class)),
			"hit_dice_by_type":   hitDiceLeftByType(charID),
			"hp":                 hp,
			"max_hp":             maxHP,
			"message":            "POST to take a Short Rest (1 hour); add hit_dice to spend Hit Point Dice (a multiclass character can add \"die\": \"d10\" to spend that type first).",
		})
		return
	}
	if req.HitDice < 0 {
		req.HitDice = 0
	}
	if lobbyID.Valid {
		passSpellTimeOn(int(lobbyID.Int64), 600, charID) // a Short Rest is 1 hour: this character's shorter spell effects end (F72)
	}
	stopCharacterBurning(charID) // F69

	// Validate hit dice to spend
	if req.HitDice > hitDiceAvailable {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":              "Not enough hit dice available",
			"hit_dice_available": hitDiceAvailable,
			"hit_dice_requested": req.HitDice,
		})
		return
	}

	// Roll hit dice and heal. A multiclass character's dice come from its pools (d10 x6, d6 x12 ...): the die named first, then the
	// largest left (SRD 5.2.1: Hit Point Dice of different types are tracked separately).
	hitDieSize := game.HitDie(class)
	conMod := game.Modifier(con)
	totalHealing := 0
	rolls := []int{}
	dieSizes := spendHitDiceByType(charID, req.HitDice, req.Die)

	for i := 0; i < req.HitDice; i++ {
		if i < len(dieSizes) {
			hitDieSize = dieSizes[i]
		}
		roll := game.RollDie(hitDieSize)
		healing := roll + conMod
		if healing < 1 {
			healing = 1 // Minimum 1 HP per die
		}
		if periaptActive(charID) {
			healing *= 2 // Periapt of Wound Closure: a Hit Point Die heals double (F142)
		}
		rolls = append(rolls, roll)
		totalHealing += healing
	}

	// Apply healing (can't exceed max HP)
	newHP := hp + totalHealing
	if newHP > maxHP {
		newHP = maxHP
	}
	actualHealing := newHP - hp

	// Update character
	db.Exec(`
		UPDATE characters SET 
			hp = $1, 
			hit_dice_spent = hit_dice_spent + $2
		WHERE id = $3
	`, newHP, req.HitDice, charID)
	clearUsedKeys(charID, "improve_fate") // Boon of Fate returns on a Short Rest
	restStudy(charID, false)

	// Check for Warlock levels - recover Pact Magic slots
	// For multiclass, check class_levels; for single class, check primary class
	warlockRecovery := ""
	warlockLevel := 0
	if len(classLevels) > 1 {
		// Multiclass character - check for Warlock levels
		for c, lvl := range classLevels {
			if strings.ToLower(c) == "warlock" {
				warlockLevel = lvl
				break
			}
		}
	} else if strings.ToLower(class) == "warlock" {
		// Single class Warlock
		warlockLevel = level
	}

	if warlockLevel > 0 {
		if len(classLevels) > 1 {
			// Multiclass: only reset pact_slots_used (keep regular spell slots)
			db.Exec("UPDATE characters SET pact_slots_used = '{}' WHERE id = $1", charID)
			warlockRecovery = fmt.Sprintf("Pact Magic slots recovered! (Warlock %d)", warlockLevel)
		} else {
			// Single class Warlock: reset spell_slots_used (backward compatible)
			db.Exec("UPDATE characters SET spell_slots_used = '{}' WHERE id = $1", charID)
			warlockRecovery = "Pact Magic spell slots recovered!"
		}
	}

	// Handle Arcane Recovery (Wizard) / Natural Recovery (Land Druid) slot recovery
	var slotRecoveryResult map[string]interface{}
	subclassStr := ""
	if subclass.Valid {
		subclassStr = subclass.String
	}

	if len(req.RecoverSlots) > 0 {
		abilityName, maxCombined, maxSlotLevel := game.SlotRecoveryAbility(class, subclassStr, level)

		if abilityName == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "Your class/subclass cannot recover spell slots on short rest",
				"details": "Only Wizards (Arcane Recovery) and Circle of the Land Druids (Natural Recovery) can recover slots.",
			})
			return
		}

		// Check if ability has already been used (tracked as class resource)
		current := getCurrentClassResources(charID)
		if current[abilityName] <= 0 {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   fmt.Sprintf("%s has already been used since your last long rest", strings.ReplaceAll(abilityName, "_", " ")),
				"details": "This ability can only be used once per long rest.",
			})
			return
		}

		// Validate recover_slots: each slot must be ≤ maxSlotLevel
		totalLevels := 0
		for _, slotLevel := range req.RecoverSlots {
			if slotLevel < 1 || slotLevel > maxSlotLevel {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":          fmt.Sprintf("Cannot recover %d-level slots with %s", slotLevel, strings.ReplaceAll(abilityName, "_", " ")),
					"max_slot_level": maxSlotLevel,
				})
				return
			}
			totalLevels += slotLevel
		}

		// Validate total combined levels
		if totalLevels > maxCombined {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":           "Combined slot levels exceed maximum",
				"total_requested": totalLevels,
				"max_combined":    maxCombined,
				"your_level":      level,
				"details":         fmt.Sprintf("You can recover slots with combined levels up to %d (half your level, rounded up)", maxCombined),
			})
			return
		}

		// Get current spell slots used and total slots
		var usedJSON []byte
		db.QueryRow("SELECT COALESCE(spell_slots_used, '{}') FROM characters WHERE id = $1", charID).Scan(&usedJSON)
		used := make(map[string]int)
		json.Unmarshal(usedJSON, &used)

		totalSlots := characterSpellSlots(charID, class, level)

		// Count how many slots of each level we're recovering
		slotsToRecover := make(map[int]int)
		for _, slotLevel := range req.RecoverSlots {
			slotsToRecover[slotLevel]++
		}

		// Validate we have used slots to recover for each level
		for slotLevel, countToRecover := range slotsToRecover {
			slotKey := fmt.Sprintf("%d", slotLevel)
			usedAtLevel := used[slotKey]
			if usedAtLevel < countToRecover {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":       fmt.Sprintf("Cannot recover %d level %d slots - only %d used", countToRecover, slotLevel, usedAtLevel),
					"used_slots":  used,
					"total_slots": totalSlots,
				})
				return
			}
		}

		// Apply recovery - reduce used slots
		recovered := []string{}
		for slotLevel, countToRecover := range slotsToRecover {
			slotKey := fmt.Sprintf("%d", slotLevel)
			used[slotKey] -= countToRecover
			if used[slotKey] <= 0 {
				delete(used, slotKey)
			}
			recovered = append(recovered, fmt.Sprintf("%d level %d", countToRecover, slotLevel))
		}

		// Save updated spell slots used
		updatedJSON, _ := json.Marshal(used)
		db.Exec("UPDATE characters SET spell_slots_used = $1 WHERE id = $2", updatedJSON, charID)

		// Mark the ability as used
		useClassResource(charID, abilityName, 1)

		slotRecoveryResult = map[string]interface{}{
			"ability":         strings.ReplaceAll(abilityName, "_", " "),
			"slots_recovered": recovered,
			"total_levels":    totalLevels,
			"max_combined":    maxCombined,
		}
	}

	// Recover class resources that refresh on short rest
	classResourcesRecovered := recoverClassResources(charID, false)
	resetSpeciesUsesOnRest(charID, false)

	// Reset Dragonborn breath weapon on short rest
	var breathWeaponReset bool
	var charSpecies string
	db.QueryRow("SELECT species, COALESCE(breath_weapon_used, false) FROM characters WHERE id = $1", charID).Scan(&charSpecies, &breathWeaponReset)
	if strings.ToLower(charSpecies) == "dragonborn" && breathWeaponReset {
		breathWeaponReset = false // 2024: breath weapon uses come back on a Long Rest only
	} else {
		breathWeaponReset = false
	}

	// Reset Fiend Warlock's Dark One's Own Luck on short rest
	var darkOnesLuckReset bool
	var charClass, charSubclass string
	var charLevel int
	db.QueryRow("SELECT class, COALESCE(subclass, ''), level, COALESCE(dark_ones_luck_used, false) FROM characters WHERE id = $1", charID).Scan(&charClass, &charSubclass, &charLevel, &darkOnesLuckReset)
	darkOnesLuckReset = false // 2024: Dark One's Own Luck returns only on a Long Rest

	// Reset Barbarian Relentless Rage DC on short rest
	var relentlessRageReset bool
	if strings.ToLower(charClass) == "barbarian" && charLevel >= 11 {
		var relentlessUses int
		db.QueryRow("SELECT COALESCE(relentless_rage_uses, 0) FROM characters WHERE id = $1", charID).Scan(&relentlessUses)
		if relentlessUses > 0 {
			db.Exec("UPDATE characters SET relentless_rage_uses = 0 WHERE id = $1", charID)
			relentlessRageReset = true
		}
	}

	// Reset Rogue's Stroke of Luck on short rest
	var strokeOfLuckReset bool
	rogueLevel := 0
	if len(classLevels) > 1 {
		for c, lvl := range classLevels {
			if strings.ToLower(c) == "rogue" {
				rogueLevel = lvl
				break
			}
		}
	} else if strings.ToLower(charClass) == "rogue" {
		rogueLevel = charLevel
	}
	if rogueLevel >= 20 {
		var strokeUsed bool
		db.QueryRow("SELECT COALESCE(stroke_of_luck_used, false) FROM characters WHERE id = $1", charID).Scan(&strokeUsed)
		if strokeUsed {
			db.Exec("UPDATE characters SET stroke_of_luck_used = false WHERE id = $1", charID)
			strokeOfLuckReset = true
		}
	}

	// 2024 Signature Spells (Wizard level 20): the free casts come back after a Short or a Long Rest
	if strings.ToLower(class) == "wizard" && level >= 20 {
		db.Exec("UPDATE characters SET signature_spells_used = '[]' WHERE id = $1", charID)
		clearSignatureUses(charID) // F120
	}

	// Sorcerous Restoration (Sorcerer level 20)
	// Regain 4 expended sorcery points on short rest
	var sorcerousRestorationRecovered int
	sorcererLevel := 0
	if len(classLevels) > 1 {
		// Multiclass: check for Sorcerer levels
		for c, lvl := range classLevels {
			if strings.ToLower(c) == "sorcerer" {
				sorcererLevel = lvl
				break
			}
		}
	} else if strings.ToLower(class) == "sorcerer" {
		sorcererLevel = level
	}

	if sorcererLevel >= 5 { // 2024 Sorcerous Restoration (level 5)
		// Get current resources and calculate max sorcery points
		var resourcesUsedJSON []byte
		db.QueryRow("SELECT COALESCE(class_resources_used, '{}') FROM characters WHERE id = $1", charID).Scan(&resourcesUsedJSON)
		resourcesUsed := make(map[string]int)
		json.Unmarshal(resourcesUsedJSON, &resourcesUsed)

		maxSorceryPoints := sorcererLevel // Sorcery points = sorcerer level
		currentUsed := resourcesUsed["sorcery_points"]

		// Calculate how many points to recover (up to 4, not exceeding used)
		recoverable := sorcererLevel / 2 // 2024: no more than half your Sorcerer level (round down), once per Long Rest
		if currentUsed < recoverable {
			recoverable = currentUsed
		}
		if resourcesUsed["sorcerous_restoration"] > 0 {
			recoverable = 0
		}

		if recoverable > 0 {
			resourcesUsed["sorcerous_restoration"] = 1
			resourcesUsed["sorcery_points"] = currentUsed - recoverable
			if resourcesUsed["sorcery_points"] <= 0 {
				delete(resourcesUsed, "sorcery_points")
			}

			updatedResourcesJSON, _ := json.Marshal(resourcesUsed)
			db.Exec("UPDATE characters SET class_resources_used = $1 WHERE id = $2", updatedResourcesJSON, charID)

			sorcerousRestorationRecovered = recoverable
		}
		_ = maxSorceryPoints // Used for clarity in calculations
	}

	response := map[string]interface{}{
		"success":                  true,
		"hit_dice_spent":           req.HitDice,
		"hit_dice_remaining":       hitDiceAvailable - req.HitDice,
		"hit_die_type":             fmt.Sprintf("d%d", hitDieSize),
		"rolls":                    rolls,
		"con_mod":                  conMod,
		"total_healing":            totalHealing,
		"actual_healing":           actualHealing,
		"periapt_of_wound_closure": map[bool]string{true: "Periapt of Wound Closure: each Hit Point Die healed double", false: ""}[periaptActive(charID) && req.HitDice > 0],
		"hp":                       newHP,
		"max_hp":                   maxHP,
		"message":                  fmt.Sprintf("Short rest complete. Spent %d hit dice, healed %d HP.", req.HitDice, actualHealing),
	}

	if warlockRecovery != "" {
		response["warlock_recovery"] = warlockRecovery
	}

	// Show slot recovery results
	if slotRecoveryResult != nil {
		response["slot_recovery"] = slotRecoveryResult
	}

	// Show recovered class resources
	if len(classResourcesRecovered) > 0 {
		response["class_resources_recovered"] = classResourcesRecovered
	}

	// Show breath weapon recovery
	if breathWeaponReset {
		response["breath_weapon_recovered"] = true
	}

	// Show Dark One's Own Luck recovery
	if darkOnesLuckReset {
		response["dark_ones_luck_recovered"] = true
	}

	// Show Relentless Rage DC reset
	if relentlessRageReset {
		response["relentless_rage_dc_reset"] = true
		response["relentless_rage_note"] = "Relentless Rage DC reset to 10"
	}

	// Show Sorcerous Restoration recovery
	if sorcerousRestorationRecovered > 0 {
		response["sorcerous_restoration"] = map[string]interface{}{
			"points_recovered": sorcerousRestorationRecovered,
			"note":             fmt.Sprintf("Sorcerous Restoration: recovered %d sorcery points", sorcerousRestorationRecovered),
		}
	}

	// Show Stroke of Luck recovery
	if strokeOfLuckReset {
		response["stroke_of_luck_recovered"] = true
		response["stroke_of_luck_note"] = "Stroke of Luck is available again!"
	}

	json.NewEncoder(w).Encode(response)
}

// handleLongRest godoc
// @Summary Take a long rest
// @Description Take a long rest (8 hours; an Elf's Trance: 4 hours). Restores HP, spell slots, death saves. Recovers half hit dice. Removes 1 exhaustion level. Only the character's owner or the campaign's GM. A body of {"type": "short"} (with the short-rest fields) takes a Short Rest instead, the same as POST /characters/{id}/short-rest.
// @Tags Characters
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param request body object false "Optional: {\"type\": \"long\"} (default) or {\"type\": \"short\", \"hit_dice\": 2}"
// @Success 200 {object} map[string]interface{} "Long rest results"
// @Failure 400 {object} map[string]interface{} "Long rest not available (need 24h between rests)"
// @Security BasicAuth
// @Router /characters/{id}/rest [post]
func handleRest(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")
	// {"type": "short"} asks for a Short Rest (F47: it used to take a Long Rest whatever the type said)
	if r.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &kind) == nil && strings.EqualFold(strings.TrimSpace(kind.Type), "short") {
			handleShortRest(w, r, charID)
			return
		}
	}

	// Get character info including last long rest
	var class string
	var level, con, wis, hitDiceSpent, exhaustionLevel int
	var isDead bool
	var lastLongRest sql.NullTime
	var subclass sql.NullString
	err := db.QueryRow(`
		SELECT class, level, con, wis, COALESCE(hit_dice_spent, 0), COALESCE(exhaustion_level, 0), last_long_rest, subclass, COALESCE(is_dead, false)
		FROM characters WHERE id = $1
	`, charID).Scan(&class, &level, &con, &wis, &hitDiceSpent, &exhaustionLevel, &lastLongRest, &subclass, &isDead)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Character not found",
		})
		return
	}
	if isDead {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "dead_character_cannot_rest"})
		return
	}

	// Check 24-hour restriction (optional - can be disabled by GM)
	if lastLongRest.Valid {
		hoursSinceRest := time.Since(lastLongRest.Time).Hours()
		if hoursSinceRest < 24 {
			hoursRemaining := 24 - hoursSinceRest
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":           "Can only take one long rest per 24 hours",
				"hours_remaining": int(hoursRemaining),
				"last_rest":       lastLongRest.Time.Format(time.RFC3339),
			})
			return
		}
	}

	// a Long Rest is 8 hours; an Elf's Trance finishes it in 4 (SRD 5.2.1 Elf: Trance)
	trance := isElf(charID)
	restRounds := 4800
	if trance {
		restRounds = 2400
	}
	passSpellTimeOn(lobbyOfChar(charID), restRounds, charID) // the effects on this character (F72)
	stopCharacterBurning(charID)                             // F69

	// SRD 5.2.1 Long Rest: "You regain all lost Hit Points and all spent Hit Point Dice."
	newHitDiceSpent := 0
	actualRecovered := hitDiceSpent

	// Reduce exhaustion by 1 (with food/drink - assumed)
	newExhaustion := exhaustionLevel
	if exhaustionLevel > 0 {
		newExhaustion = exhaustionLevel - 1
	}

	restStudy(charID, true)
	// Reset everything for long rest
	db.Exec(`
		UPDATE characters SET
			hp = max_hp,
			temp_hp = 0, -- Temporary Hit Points last until a Long Rest (F67)
			spell_slots_used = '{}',
			pact_slots_used = '{}',
			death_save_successes = 0,
			death_save_failures = 0,
			is_stable = false,
			concentrating_on = NULL,
			conditions = '[]',
			hit_dice_spent = $2,
			exhaustion_level = $3,
			last_long_rest = NOW(),
			action_used = false,
			bonus_action_used = false, weapon_interaction_used = false,
			reaction_used = false,
			movement_remaining = 30,
			ammo_used_since_rest = 0,
			class_resources_used = '{}',
			breath_weapon_used = false,
			relentless_endurance_used = false,
			relentless_rage_uses = 0,
			intimidating_presence_used = false,
			magical_cunning_used = false,
			innate_sorcery_rounds = 0,
			uncanny_metabolism_used = false,
			divine_intervention_rests = GREATEST(COALESCE(divine_intervention_rests,0) - 1, 0),
			dark_ones_luck_used = false,
			dark_ones_luck_count = 0,
			hurl_through_hell_used = false,
			invocation_spells_used = '[]',
			indomitable_used = 0,
			mystic_arcanum_used = '[]',
			stroke_of_luck_used = false,
			eldritch_master_used = false,
			signature_spells_used = '[]',
			overchannel_used = false
		WHERE id = $1
	`, charID, newHitDiceSpent, newExhaustion)
	resetSpeciesUsesOnRest(charID, true)
	if newHitDiceSpent == 0 { // a Long Rest gives back every Hit Point Die: the multiclass pools too
		db.Exec(`UPDATE characters SET hit_dice_spent_by_type = '{}' WHERE id = $1`, charID)
	}
	grantSpellChangesOnLongRest(charID, class) // 2024 spell preparation (prepared_spells_2024.go)

	// Get updated info for response
	var hp, maxHP, cha int
	db.QueryRow("SELECT hp, max_hp, cha FROM characters WHERE id = $1", charID).Scan(&hp, &maxHP, &cha)

	slots := characterSpellSlots(charID, class, level)

	// Get class resources info
	chaMod := game.Modifier(cha)
	maxResources := characterMaxResources(charID, class, level, chaMod)

	response := map[string]interface{}{
		"success":            true,
		"hp":                 maxHP,
		"max_hp":             maxHP,
		"spell_slots":        slots,
		"hit_dice_recovered": actualRecovered,
		"hit_dice_available": level - newHitDiceSpent,
		"hit_dice_total":     level,
		"hit_die_type":       fmt.Sprintf("d%d", game.HitDie(class)),
		"message":            "Long rest complete. HP and spell slots restored.",
	}
	if trance {
		response["trance"] = "Trance: an Elf finishes a Long Rest in 4 hours of trancelike meditation (4 hours passed, not 8)."
	}
	if recharged := dawnOnLongRest(charID); len(recharged) > 0 { // magic items regain charges at dawn (table rule "dawn")
		response["magic_items_recharged"] = recharged
	}

	// Show class resources restored
	if len(maxResources) > 0 {
		response["class_resources_restored"] = maxResources
	}

	if exhaustionLevel > 0 {
		response["exhaustion_reduced"] = true
		response["exhaustion_level"] = newExhaustion
		response["message"] = fmt.Sprintf("Long rest complete. HP and spell slots restored. Exhaustion reduced to %d.", newExhaustion)
	}

	// Show Indomitable recovery for Fighters level 9+
	indomitableMaxUses := getIndomitableMaxUses(class, level)
	if indomitableMaxUses > 0 {
		response["indomitable_recovered"] = true
		response["indomitable_max"] = indomitableMaxUses
		response["indomitable_note"] = fmt.Sprintf("Indomitable uses restored (%d per long rest)", indomitableMaxUses)
	}

	json.NewEncoder(w).Encode(response)
}

// handleConditionsList godoc
// @Summary List all 5e conditions
// @Description Returns all standard 5e conditions with their effects
// @Tags Combat
// @Produce json
// @Success 200 {object} map[string]interface{} "List of conditions"
// @Router /conditions [get]
func handleConditionsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"conditions": conditionEffects,
		"cover": map[string]interface{}{
			"none":           "+0 AC",
			"half":           "+2 AC (behind low wall, another creature, etc.)",
			"three_quarters": "+5 AC (behind arrow slit, behind thick tree, etc.)",
			"full":           "Can't be directly targeted by attacks or spells",
		},
		"note": "Use POST /api/characters/{id}/conditions to apply a condition. Use POST /api/characters/{id}/cover to set cover.",
	})
}

// handleSetCover godoc
// @Summary Set cover for a character
// @Description Set cover bonus (none, half, three_quarters, full). Only the character's owner or the campaign's GM.
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{cover=string} true "Cover type (none, half, three_quarters, full)"
// @Success 200 {object} map[string]interface{} "Cover set"
// @Router /characters/{id}/cover [post]
func handleSetCover(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Cover string `json:"cover"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	coverType := strings.ToLower(strings.ReplaceAll(req.Cover, "-", "_"))
	bonus, valid := coverBonuses[coverType]
	if !valid {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":       "invalid_cover_type",
			"valid_types": []string{"none", "half", "three_quarters", "full"},
		})
		return
	}

	db.Exec("UPDATE characters SET cover_bonus = $1 WHERE id = $2", bonus, charID)

	message := fmt.Sprintf("Cover set to %s (+%d AC)", req.Cover, bonus)
	if coverType == "full" {
		message = "Full cover - can't be directly targeted by attacks or most spells"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"cover":    req.Cover,
		"ac_bonus": bonus,
		"message":  message,
	})
}
