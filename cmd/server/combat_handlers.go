package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
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
// @Body Optional: ids of surprised characters
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Combat started with initiative order"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can start combat"
// @Router /campaigns/{id}/combat/start [post]
func handleCombatStart(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if _, ok := requireCampaignGM(w, r, campaignID, "start a fight"); !ok {
		return
	}

	setTravelPace(campaignID, "") // a fight interrupts the journey: the travel pace no longer affects checks

	// Surprise: a character surprised by combat starting has Disadvantage on the Initiative roll (optional body {"surprised": [character ids]}).
	var startReq struct {
		Surprised []int `json:"surprised"` // optional: the ids of the characters surprised (they roll Initiative with Disadvantage)
	}
	if !readRequest(w, r, &startReq, optionalBody()) {
		return
	}
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
		writeDBError(w, err, "the campaign's characters")
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
		clearUses(id, "improve_fate")     // Boon of Fate returns when Initiative is rolled
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
			biMax := classResourceMax(id, class, level, "bardic_inspiration", game.Modifier(abilityScoreOf(id, "cha")))
			regained := 0
			changeSpent(id, func(sp *spent) error { // resources_2024.go
				if left := biMax - sp.Uses["bardic_inspiration"]; left < 2 && sp.Uses["bardic_inspiration"] > 0 {
					regained = min(2-left, sp.Uses["bardic_inspiration"])
					sp.regain("bardic_inspiration", regained)
				}
				return nil
			})
			if regained > 0 {
				capstoneNotes = append(capstoneNotes, fmt.Sprintf("🎭 %s: Superior Inspiration triggered! Regained %d Bardic Inspiration", name, regained))
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
	clearPendingTurns(campaignID)      // pending turns belong to one fight (pending_turns_2024.go)
	// Store combat state (the combat-state owner, combat_state_2024.go)
	if err := startFight(campaignID, entries); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}

	// Reset action economy for all characters (reactions, actions, bonus actions, movement)
	db.Exec("UPDATE characters SET reaction_used = false, action_used = false, bonus_action_used = false, weapon_interaction_used = false, in_melee_range = false WHERE lobby_id = $1", campaignID)
	if rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1`, campaignID); err == nil { // Dodge and Reckless Attack end with the fight
		var ids []int
		for rows.Next() {
			var id int
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			endUntilNextTurn(id)
		}
	}
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

// handleCombatEnd ends the fight: the turn order cleared, the action economy reset, the combat markers dropped. The route is
// endCombatWithAdvancement (advancement_2024.go), which reads the request and adds the fight's XP.
func handleCombatEnd(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if _, ok := requireCampaignGM(w, r, campaignID, "end the fight"); !ok {
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
// @Description Move to the next character in initiative order. Optional legendary_actions: Legendary Actions taken right after the turn that ends, before the next turn starts (each like POST /api/gm/legendary-action). The reply lists legendary_options: the monsters that may take one now and their actions. In a campaign with the turn schedule a player's turn ends this way only when the player acted in the slot, has nothing to choose, or has a submitted turn paused for the GM; otherwise 409 pending_turn_waiting (a submitted turn: combat/resolve resolves it), automatic_turn (dying: combat/resolve rolls the Death Saving Throw), stand_in_too_early (before the round's final check-in) or stand_in_needed (after it: a missed turn is taken by a stand-in, never skipped). When the round's last turn ends, round_complete says "Round N is complete; round N+1 has begun". Optional expected_turn {"round": R, "turn_index": I}: the turn you mean to end, as GET /api/campaigns/{id}/combat shows it (round, current_turn_index; GET /api/gm/status: combat.round, combat.current_turn_index; each turn change's reply: round, turn_index). Once that turn has ended (a retry that arrives after the first request finished) the reply is 409 turn_already_moved with the turn it is now (round, turn_index, current_turn, current_turn_id) and nothing is changed; without it the current turn ends. Two overlapping requests for the same turn change it once either way (409 turn_change_in_progress).
// @Body Legendary Actions after the ending turn, and expected_turn: the turn you mean to end
// @Tags Combat
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Turn advanced"
// @Router /campaigns/{id}/combat/next [post]
func handleCombatNext(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if _, ok := requireCampaignGM(w, r, campaignID, "advance the turn"); !ok {
		return
	}
	// Optional: Legendary Actions taken right after the turn that is ending (monster_features_2024.go)
	var nextReq struct {
		LegendaryActions []legendaryRequest `json:"legendary_actions"` // optional: Legendary Actions taken right after the ending turn, each like POST /api/gm/legendary-action
		ExpectedTurn     *expectedTurn      `json:"expected_turn"`     // optional: the turn to end ({round, turn_index}); a retry after it ended is 409 turn_already_moved
	}
	if !readRequest(w, r, &nextReq, optionalBody()) {
		return
	}
	named, ok := namedTurn(w, campaignID, nextReq.ExpectedTurn)
	if !ok {
		return
	}
	// with the turn schedule, a player's turn ends only as the schedule allows (gmEndsTurn, schedule_walk_2024.go)
	end, _, err := gmEndsTurn(campaignID, named)
	if answerDBFailure(w, err, "the campaign's turn schedule") {
		return
	}
	if end.Reply != nil {
		writeJSONStatus(w, http.StatusConflict, end.Reply)
		return
	}
	var legendaryResults []map[string]interface{}
	t, code := advanceTurn(campaignID, turnHow{by: "the GM advanced the turn order", expect: end.Expect, between: func(t *turnChange) {
		for _, la := range nextReq.LegendaryActions { // after the turn that ended, before the next creature's turn starts
			res, code, msg := takeLegendaryAction(r, campaignID, la, t.window(), t.EndedID)
			if code != "" {
				res = map[string]interface{}{"combatant_id": la.CombatantID, "error": code, "message": msg}
			}
			legendaryResults = append(legendaryResults, res)
		}
	}})
	if code != "" {
		answerTurnRefusal(w, campaignID, t, code)
		return
	}
	response := t.reply()
	if len(legendaryResults) > 0 {
		response["legendary_actions_taken"] = legendaryResults
	}
	json.NewEncoder(w).Encode(response)
}

// handleCombatSkip godoc
// @Summary Skip a player's turn due to timeout (GM only)
// @Description Skip the current player's turn and advance to the next combatant. Use when a player has been inactive for longer than the campaign's combat turn timeout (table rule combat_turn_timeout, 4 hours by default; when it is off, turns are never skipped: end the turn with combat/next). In a campaign with the turn schedule there is no timeout and skip is for a monster's or NPC's turn only (at once): a player's turn is never skipped (409 player_turn_not_skipped, pointing to combat/resolve, combat/stand-in, or combat/next for a player who acted in the slot). Optional expected_turn {"round": R, "turn_index": I}: the turn you mean to skip, as GET /api/campaigns/{id}/combat shows it (round, current_turn_index); once it has ended the reply is 409 turn_already_moved with the turn it is now (round, turn_index, current_turn, current_turn_id) and nothing is changed.
// @Tags Combat
// @Body expected_turn: the turn you mean to skip
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Turn skipped"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can skip turns"
// @Failure 409 {object} map[string]interface{} "player_turn_not_skipped (turn schedule), turn_already_moved, turn_change_in_progress"
// @Router /campaigns/{id}/combat/skip [post]
func handleCombatSkip(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if _, ok := requireCampaignGM(w, r, campaignID, "skip a turn"); !ok {
		return
	}
	var req struct {
		ExpectedTurn *expectedTurn `json:"expected_turn"` // the turn to skip: a retry after it ended is turn_already_moved
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	named, ok := namedTurn(w, campaignID, req.ExpectedTurn)
	if !ok {
		return
	}
	state, err := scheduleStateOf(db, campaignID)
	if answerDBFailure(w, err, "the campaign's turn schedule") {
		return
	}
	if usesSchedule(state) {
		skipScheduledTurn(w, campaignID, named)
		return
	}
	limit, on := combatTurnTimeout(campaignID) // the table rule, as the timeout worker reads it
	t, code := advanceTurn(campaignID, turnHow{by: "the GM skipped the turn", result: howSkipped, expect: named, allow: func(f *fight) (string, string) {
		if id := asInt(f.current()["id"]); id < 0 {
			return "not_player_turn", ""
		}
		if !on {
			return "turn_not_timed_out", fmt.Sprintf("The campaign's table rule combat_turn_timeout is off: turns are never skipped. End the turn with POST /api/campaigns/%d/combat/next.", campaignID)
		}
		if f.StartedAt.IsZero() || time.Since(f.StartedAt) < limit {
			return "turn_not_timed_out", fmt.Sprintf("The turn hasn't timed out yet: it is skipped after %s (table rule combat_turn_timeout). End it with POST /api/campaigns/%d/combat/next if it is done.", limit, campaignID)
		}
		return "", ""
	}})
	if code != "" {
		answerTurnRefusal(w, campaignID, t, code)
		return
	}
	elapsedMinutes := int(time.Since(t.EndedStartedAt).Minutes())
	writeAction(db, campaignID, t.EndedID, "turn_skipped", "Turn skipped by GM due to timeout", fmt.Sprintf("Inactive for %d minutes", elapsedMinutes))
	response := t.reply()
	response["skipped"] = t.EndedName
	response["inactive_minutes"] = elapsedMinutes
	json.NewEncoder(w).Encode(response)
}

// skipScheduledTurn is combat/skip in a campaign with the turn schedule: no timeout; the turn ends as gmEndsTurn allows (a
// monster's or NPC's turn at once; a player's turn never quietly), recorded as skipped.
func skipScheduledTurn(w http.ResponseWriter, campaignID int, named *turnRef) {
	end, _, err := gmEndsTurn(campaignID, named)
	if answerDBFailure(w, err, "the campaign's turn schedule") {
		return
	}
	if end.Player { // Matt, 11.11: with the schedule a player's turn is resolved, stood in for or automatic, never skipped
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "player_turn_not_skipped",
			"message": fmt.Sprintf("With the turn schedule a player's turn is never skipped: a submitted turn resolves (POST /api/campaigns/%d/combat/resolve, which also rolls a dying character's Death Saving Throw and passes a turn with nothing to choose), a missed turn gets a stand-in at the round's final check-in, and a turn paused for you (needs_gm) is finished with a stand-in at any check-in (POST /api/campaigns/%d/combat/stand-in). A player who acted in the slot ends it with POST /api/campaigns/%d/combat/next. combat/skip is for a monster's or NPC's turn. Nothing was changed.", campaignID, campaignID, campaignID)})
		return
	}
	if end.Reply != nil {
		writeJSONStatus(w, http.StatusConflict, end.Reply)
		return
	}
	t, code := advanceTurn(campaignID, turnHow{by: "the GM skipped the turn", result: howSkipped, expect: end.Expect})
	if code != "" {
		answerTurnRefusal(w, campaignID, t, code)
		return
	}
	writeAction(db, campaignID, t.EndedID, "turn_skipped", "Turn skipped by the GM", "")
	response := t.reply()
	response["skipped"] = t.EndedName
	json.NewEncoder(w).Encode(response)
}

// handleExplorationStatus godoc
// @Summary Get exploration mode status
// @Description Returns exploration mode status including inactive players
// @Tags Exploration
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
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
				(SELECT MAX(a.created_at) FROM actions a WHERE a.character_id = c.id AND a.action_type NOT IN (`+notPlaySQL()+`)),
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
// @Description Mark an inactive player (12h+) as following the party. Records a 'following' action. Only in a campaign whose turn schedule is off: with the schedule a missed turn gets a stand-in at the round's final check-in (POST /api/campaigns/{id}/round/stand-in), never a skip (409 schedule_uses_stand_ins).
// @Tags Exploration
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Character to skip
// @Success 200 {object} map[string]interface{} "Player skipped"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can skip"
// @Failure 409 {object} map[string]interface{} "in_combat, schedule_uses_stand_ins"
// @Router /campaigns/{id}/exploration/skip [post]
func handleExplorationSkip(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "skip a player's exploration turn"); !ok {
		return
	}
	if state, err := scheduleStateOf(db, campaignID); answerDBFailure(w, err, "the campaign's turn schedule") {
		return
	} else if usesSchedule(state) { // Matt, section 6: with the turn schedule no missed turn is skipped
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "schedule_uses_stand_ins",
			"message": fmt.Sprintf("This campaign uses the turn schedule: a player who hasn't submitted gets a stand-in turn at the round's final check-in, never a skip. POST /api/campaigns/%d/round/stand-in (or resolve their submission with POST /api/campaigns/%d/round/resolve). Nothing was changed.", campaignID, campaignID)})
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
	if !readRequest(w, r, &req) {
		return
	}
	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	// Get character info
	var charName string
	var charLobbyID int
	err := db.QueryRow("SELECT name, lobby_id FROM characters WHERE id = $1", req.CharacterID).Scan(&charName, &charLobbyID)
	if err != nil || charLobbyID != campaignID {
		if answerDBFailure(w, err, "the character") {
			return
		}
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
			MAX(created_at) FILTER (WHERE action_type NOT IN (`+notPlaySQL()+`)),
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
	writeAction(db, campaignID, req.CharacterID, "following", "Marked as following the party (exploration skip)", fmt.Sprintf("Inactive for %d minutes, defaulting to follow party", inactiveMinutes))

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
// @Param Authorization header string true "Bearer <token>"
// @Body Combatants to add (name, monster_key, initiative, surprised, hp, ac, in_lair). Identical monsters added together share one Initiative roll; surprised gives Disadvantage on it; in_lair gives a legendary monster the in-Lair uses of Legendary Actions and Legendary Resistance.
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

	if _, ok := requireCampaignGM(w, r, campaignID, "add combatants to the fight"); !ok {
		return
	}

	// Check combat is active
	if _, err := readFight(db, campaignID); answerFightProblem(w, err) {
		return
	}

	// Parse request
	var req struct {
		Combatants []struct { // the combatants to add: name, monster_key, initiative, surprised, hp, ac, in_lair
			Name       string `json:"name"`
			MonsterKey string `json:"monster_key"` // SRD monster slug (e.g., "goblin")
			Initiative int    `json:"initiative"`  // Optional: roll if not provided
			Surprised  bool   `json:"surprised"`   // Optional: surprised by combat starting (Disadvantage on Initiative)
			HP         int    `json:"hp"`          // Optional: use monster default
			AC         int    `json:"ac"`          // Optional: use monster default
			InLair     bool   `json:"in_lair"`     // Optional: the monster fights in its lair (one more Legendary Action use and Legendary Resistance)
		} `json:"combatants"`
	}
	if !readRequest(w, r, &req) {
		return
	}

	if len(req.Combatants) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants_provided"})
		return
	}

	// The new entries (their ids are given by the fight, below): stat block, Initiative, Hit Points, AC.
	var newEntries []map[string]interface{}
	var inLair []bool
	groupInit := map[string]int{}
	for _, c := range req.Combatants {
		if c.Name == "" {
			continue
		}
		init, dex, hp, ac := c.Initiative, 0, 10, 10
		var mdex, mhp, mac int
		known := c.MonsterKey != "" && db.QueryRow(`SELECT COALESCE(dex, 10), COALESCE(hp, 10), COALESCE(ac, 10) FROM monsters WHERE slug = $1`, c.MonsterKey).Scan(&mdex, &mhp, &mac) == nil
		if known {
			dex, hp, ac = mdex, mhp, mac
		}
		if init == 0 {
			if known {
				// A group of identical creatures shares one Initiative roll (one roll per monster kind per request).
				groupKey := fmt.Sprintf("%s#%v", c.MonsterKey, c.Surprised)
				if v, ok := groupInit[groupKey]; ok {
					init = v
				} else {
					d := game.RollDie(20)
					if c.Surprised {
						if d2 := game.RollDie(20); d2 < d {
							d = d2
						}
					}
					init = d + game.Modifier(dex)
					groupInit[groupKey] = init
				}
			} else {
				init = game.RollDie(20)
			}
		}
		if c.HP > 0 {
			hp = c.HP
		}
		if c.AC > 0 {
			ac = c.AC
		}
		newEntries = append(newEntries, map[string]interface{}{"name": c.Name, "initiative": init, "dex_score": dex, "is_monster": true,
			"monster_key": c.MonsterKey, "hp": hp, "max_hp": hp, "ac": ac})
		inLair = append(inLair, known && c.InLair) // in its lair: the "in Lair" numbers of Legendary Actions and Legendary Resistance
	}

	// Each new monster gets an id the fight has never given (audit AUD-39) and its place in the Initiative order; whose turn it
	// is doesn't change; every other entry is kept as it is (the combat-state owner, combat_state_2024.go).
	f, err := changeFight(campaignID, func(f *fight) error {
		if len(f.Order) == 0 {
			return errNotInFight
		}
		if f.current() == nil {
			return errTurnMoved
		}
		for _, e := range newEntries {
			e["id"] = f.newMonsterID()
			f.place(e)
		}
		return nil
	})
	switch {
	case errors.Is(err, errNotInFight):
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_combatants"})
		return
	case errors.Is(err, errTurnMoved):
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "invalid_combat_turn"})
		return
	case answerFightProblem(w, err):
		return
	}

	added := []map[string]interface{}{}
	for i, e := range newEntries {
		id := asInt(e["id"])
		if inLair[i] {
			s := loadMonsterState(campaignID, id)
			s.InLair = true
			saveMonsterState(campaignID, id, s)
		}
		a := map[string]interface{}{"id": id, "name": e["name"], "initiative": e["initiative"], "hp": e["hp"], "ac": e["ac"]}
		if feats := monsterFeaturesInfo(campaignID, id); feats != nil { // Legendary Actions, Legendary Resistance, auras the server runs for this monster
			a["features"] = feats
		}
		added = append(added, a)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":          true,
		"added_count":      len(added),
		"combatants_added": added,
		"turn_order":       f.Order,
		"current_turn":     f.current()["name"],
	})
}

// handleCombatRemove godoc
// @Summary Remove combatant from combat (GM only)
// @Description Remove a monster or NPC from combat (for death, flee, etc.)
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body ID or name of combatant to remove
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

	if _, ok := requireCampaignGM(w, r, campaignID, "remove combatants from the fight"); !ok {
		return
	}

	// Check combat is active
	f, err := readFight(db, campaignID)
	if answerFightProblem(w, err) {
		return
	}

	// Parse request
	var req struct {
		CombatantID   int    `json:"combatant_id"`
		CombatantName string `json:"combatant_name"` // the combatant's name, instead of combatant_id
	}
	if !readRequest(w, r, &req) {
		return
	}

	if req.CombatantID == 0 && req.CombatantName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "must_provide_combatant_id_or_name"})
		return
	}
	if len(f.Order) == 0 {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_combatants"})
		return
	}
	if f.current() == nil {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "invalid_combat_turn"})
		return
	}
	id := req.CombatantID
	if id == 0 {
		for _, e := range f.Order {
			if strings.EqualFold(sprintAny(e["name"]), req.CombatantName) {
				id = asInt(e["id"])
				break
			}
		}
	}
	if id == 0 {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "combatant_not_found"})
		return
	}
	res, code := removeFromFight(campaignID, id, "the GM removed the combatant")
	switch {
	case code == "combatant_not_found":
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": code})
		return
	case code != "":
		answerTurnRefusal(w, campaignID, nil, code)
		return
	}
	writeJSONStatus(w, http.StatusOK, combatRemovalReply(campaignID, res))
}

// combatRemovalReply is the reply of a removal from the fight (POST .../combat/remove and the GM's adjust-monster remove).
func combatRemovalReply(campaignID int, res removal) map[string]interface{} {
	out := map[string]interface{}{"success": true, "removed": res.Removed[0]["name"], "removed_id": asInt(res.Removed[0]["id"])}
	if res.Ended {
		out["combat_ended"] = true
		out["message"] = "All combatants removed, combat ended"
		return out
	}
	if f, err := readFight(db, campaignID); err == nil {
		out["turn_order"] = f.Order
		if cur := f.current(); cur != nil {
			out["current_turn"] = cur["name"]
		}
	}
	if t := res.Turn; t != nil { // it was its turn: the next creature's turn has started
		out["turn_passed"] = t.reply()
		out["current_turn"] = t.NextName
	}
	return out
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

	if answerDBFailure(w, err, "the campaign's fight") {
		return
	}
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
// @Description Deal damage to a character: resistances, Temporary Hit Points first, then Hit Points (never below 0); dropping to 0 (dying), damage at 0 (a Death Saving Throw failure, two from a critical), Instant Death when the damage left after 0 is at least the Hit Point maximum. A concentrating character makes the Constitution save at once (DC 10 or half the damage taken after resistance, up to 30): concentration, concentration_kept; on a failure the spell and what it holds end. Only the GM of the character's campaign (403 not_gm otherwise).
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Damage to apply. One source in several damage types makes one Concentration save: send the whole damage as concentration_damage with the first part and skip_concentration with the others
// @Success 200 {object} map[string]interface{} "Damage applied"
// @Router /characters/{id}/damage [post]
func handleDamage(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Damage     int    `json:"damage"`      // the damage to deal
		Amount     int    `json:"amount"`      // the same, under another name (damage wins when both are sent)
		DamageType string `json:"damage_type"` // the damage type (resistances, immunities and vulnerabilities apply)
		// one source of damage in several types (a hit's "plus 10 (3d6) Fire damage") makes one Concentration save: the first part
		// carries the whole damage for its DC, the others skip the save
		ConcentrationDamage int  `json:"concentration_damage"`
		SkipConcentration   bool `json:"skip_concentration"` // true: another part of the same damage already made the Concentration save
		NoBond              bool `json:"no_bond"`            // a Warding Bond's shared damage (not shared again)
		Critical            bool `json:"critical"`           // the damage is from a Critical Hit (two Death Saving Throw failures at 0 Hit Points)
		SameHit             bool `json:"same_hit"`           // another damage type of a hit already counted (one failure per hit at 0 Hit Points)
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	if req.Damage <= 0 && req.Amount > 0 {
		req.Damage = req.Amount
	}

	if req.Damage <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "damage_must_be_positive"})
		return
	}

	d, err := damageHP(charID, hpHit{Parts: []hpPart{{req.Damage, strings.ToLower(req.DamageType)}}, Critical: req.Critical,
		NoBond: req.NoBond, ConcentrationDamage: req.ConcentrationDamage, SkipConcentration: req.SkipConcentration, SameHit: req.SameHit})
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}
	json.NewEncoder(w).Encode(d.View())
}

// handleHeal godoc
// @Summary Heal a character (GM only)
// @Description Restore HP to a character: never above the maximum, nothing for a dead character; healing from 0 Hit Points resets the Death Saving Throws. Only the GM of the character's campaign (403 not_gm otherwise).
// @Tags Combat
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Healing amount
// @Success 200 {object} map[string]interface{} "Healing applied"
// @Router /characters/{id}/heal [post]
func handleHeal(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Healing int `json:"healing"` // the Hit Points to restore
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	h, err := healHP(charID, req.Healing)
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":                true,
		"hp":                     h.After.HP,
		"max_hp":                 h.After.Max,
		"healing_applied":        h.Healed,
		"regained_consciousness": h.Woke,
		"dead":                   h.After.Dead,
	})
}

// shortRestRequest: the body of a Short Rest (POST .../short-rest, or .../rest with type "short").
type shortRestRequest struct {
	Type         string `json:"type"`          // "short" (on .../rest); a Long Rest takes only type
	HitDice      int    `json:"hit_dice"`      // Hit Point Dice to spend
	Die          string `json:"die"`           // multiclass: which Hit Point Die to spend first ("d10"); the largest first otherwise
	RecoverSlots []int  `json:"recover_slots"` // slot levels to recover (Arcane Recovery, Natural Recovery): [1, 2] is one level 1 and one level 2 slot
}

// restKind reads a rest's type: "long" (also when none is sent), "short", or "" for anything else.
func restKind(t string) string {
	switch game.Key(t) {
	case "", "long", "long_rest":
		return "long"
	case "short", "short_rest":
		return "short"
	}
	return ""
}

// handleShortRest godoc
// @Summary Take a Short Rest
// @Description A Short Rest (1 hour; the spell clock passes 1 hour). Spend Hit Point Dice with hit_dice (each die is rolled and adds the Constitution modifier); what comes back on a Short Rest comes back (Pact Magic slots, class resources such as Focus Points, Second Wind, Channel Divinity, Wild Shape, species uses). A Wizard's Arcane Recovery and a Land Druid's Natural Recovery take recover_slots, the slot levels to recover (for example [1, 2]). A POST always takes the rest, with or without hit_dice; a GET only shows the Hit Point Dice left. Only the character's owner or the campaign's GM.
// @Tags Characters
// @Param id path int true "Character ID"
// @Body Hit Point Dice to spend; slots for Arcane Recovery or Natural Recovery
// @Router /characters/{id}/short-rest [get]
// @Router /characters/{id}/short-rest [post]
func handleShortRest(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	// how many Hit Point Dice to spend, slots to recover (all optional)
	var req shortRestRequest
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	if strings.TrimSpace(req.Type) != "" && restKind(req.Type) != "short" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_rest_type",
			"message": fmt.Sprintf("This route takes a Short Rest; type %q isn't one. Leave type out (or send \"short\"); a Long Rest is POST /api/characters/{id}/rest. Nothing was changed.", req.Type)})
		return
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
		if answerDBFailure(w, err, "the character") {
			return
		}
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
			"message":            "POST to take a Short Rest (1 hour); add hit_dice to spend Hit Point Dice.",
			"rest":               restGuide(charID), // what resting gives and how to ask (rests_2024.go)
		})
		return
	}
	if req.HitDice < 0 {
		req.HitDice = 0
	}
	// checked before anything changes: SRD 5.2.1 "To start a Short Rest, you must have at least 1 Hit Point"; the dice asked for
	if hp < 1 {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "needs_1_hit_point",
			"message": "A Short Rest needs at least 1 Hit Point to start: at 0 you are dying or stable, not resting. Nothing was changed."})
		return
	}
	if req.HitDice > hitDiceAvailable {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_enough_hit_dice", "hit_dice_available": hitDiceAvailable,
			"hit_dice_requested": req.HitDice, "message": fmt.Sprintf("You have %d Hit Point Dice left. Nothing was changed.", hitDiceAvailable)})
		return
	}
	if lobbyID.Valid {
		passSpellTimeOn(int(lobbyID.Int64), 600, charID) // a Short Rest is 1 hour: this character's shorter spell effects end (F72)
	}
	stopCharacterBurning(charID) // F69

	// the Hit Point Dice spent as the rest finishes (rests_2024.go: one guarded claim, rolled, healed by the Hit Point owner)
	dice, err := rollHitDice(charID, req.HitDice, req.Die)
	if errors.Is(err, errNotEnoughHitDice) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_enough_hit_dice", "hit_dice_available": dice.Left,
			"message": "Another request spent Hit Point Dice at the same moment; send the rest again."})
		return
	}
	if answerDBFailure(w, err, "the character") {
		return
	}
	hitDieSize, conMod, rolls, totalHealing, actualHealing, newHP := dice.Die, dice.ConMod, dice.Rolls, dice.Healing, dice.Healed, dice.HP
	maxHP = dice.MaxHP
	clearUses(charID, "improve_fate") // Boon of Fate returns on a Short Rest
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

		alreadyUsed := func() {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   fmt.Sprintf("%s has already been used since your last long rest", strings.ReplaceAll(abilityName, "_", " ")),
				"details": "This ability can only be used once per long rest.",
			})
		}
		// Check if ability has already been used (tracked as class resource)
		current := getCurrentClassResources(charID)
		if current[abilityName] <= 0 {
			alreadyUsed()
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

		totalSlots := characterSpellSlots(charID, class, level)
		abilityMax := classResourceMax(charID, class, level, abilityName, 0)

		// Count how many slots of each level we're recovering
		slotsToRecover := make(map[int]int)
		for _, slotLevel := range req.RecoverSlots {
			slotsToRecover[slotLevel]++
		}

		// The use of the ability and the slots it recovers change together, on the locked counts (resources_2024.go)
		recovered := []string{}
		var shortBody map[string]interface{}
		_, _, err := changeSpent(charID, func(sp *spent) error {
			if abilityMax-sp.Uses[abilityName] <= 0 {
				return &notEnough{"used", 0}
			}
			for slotLevel, countToRecover := range slotsToRecover {
				slotKey := fmt.Sprintf("%d", slotLevel)
				if usedAtLevel := sp.Slots[slotKey]; usedAtLevel < countToRecover {
					shortBody = map[string]interface{}{
						"error":       fmt.Sprintf("Cannot recover %d level %d slots - only %d used", countToRecover, slotLevel, usedAtLevel),
						"used_slots":  sp.Slots,
						"total_slots": totalSlots,
					}
					return &notEnough{"slots", 0}
				}
			}
			for slotLevel, countToRecover := range slotsToRecover {
				slotKey := fmt.Sprintf("%d", slotLevel)
				sp.Slots[slotKey] -= countToRecover
				if sp.Slots[slotKey] <= 0 {
					delete(sp.Slots, slotKey)
				}
				recovered = append(recovered, fmt.Sprintf("%d level %d", countToRecover, slotLevel))
			}
			sp.Uses[abilityName]++
			return nil
		})
		if r, ok := refusedSpend(err); ok {
			if r.Msg == "used" {
				alreadyUsed()
			} else {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(shortBody)
			}
			return
		}
		if answerDBFailure(w, err, "the character") {
			return
		}

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
		// 2024: no more than half your Sorcerer level (round down), once per Long Rest; on the locked counts (resources_2024.go)
		changeSpent(charID, func(sp *spent) error {
			recoverable := min(sorcererLevel/2, sp.Uses["sorcery_points"])
			if sp.Uses["sorcerous_restoration"] > 0 || recoverable <= 0 {
				return nil
			}
			sp.Uses["sorcerous_restoration"] = 1
			sp.regain("sorcery_points", recoverable)
			if sp.Uses["sorcery_points"] <= 0 {
				delete(sp.Uses, "sorcery_points")
			}
			sorcerousRestorationRecovered = recoverable
			return nil
		})
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
		"message":                  "Short rest complete. " + hitDiceMessage(charID, dice),
		"rest":                     restGuide(charID), // what resting gives and how to ask (rests_2024.go)
	}
	writeAction(db, int(lobbyID.Int64), charID, "short_rest", fmt.Sprintf("%s finishes a Short Rest", getCharacterName(charID)), fmt.Sprintf("spent %d Hit Point Dice, regained %d Hit Points", req.HitDice, actualHealing))

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

// handleRest godoc
// @Summary Take a long rest
// @Description Take a long rest (8 hours; an Elf's Trance: 4 hours). Restores HP, spell slots, death saves. Recovers half hit dice. Removes 1 exhaustion level. Only the character's owner or the campaign's GM. A body of {"type": "short"} (with the short-rest fields) takes a Short Rest instead, the same as POST /characters/{id}/short-rest.
// @Tags Characters
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Body Optional: {\"type\": \"long\"} (default) or {\"type\": \"short\", \"hit_dice\": 2}
// @Success 200 {object} map[string]interface{} "Long rest results"
// @Failure 400 {object} map[string]interface{} "Long rest not available (need 24h between rests)"
// @Security BasicAuth
// @Router /characters/{id}/rest [post]
func handleRest(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")
	// {"type": "short"} asks for a Short Rest (F47: it used to take a Long Rest whatever the type said); the Short Rest's fields
	// are refused for a Long Rest
	var kind shortRestRequest
	if !readRequest(w, r, &kind, optionalBody()) {
		return
	}
	switch restKind(kind.Type) {
	case "short":
		handleShortRest(w, r, charID)
		return
	case "":
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_rest_type",
			"message": fmt.Sprintf("type is \"long\" (the default) or \"short\", not %q. Nothing was changed.", kind.Type)})
		return
	}
	var long struct {
		Type string `json:"type"`
	}
	if !readRequest(w, r, &long, optionalBody(), bodyHint(`hit_dice, die and recover_slots are for a Short Rest: send them with {"type": "short"}.`)) {
		return
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
		if answerDBFailure(w, err, "the character") {
			return
		}
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
	// SRD 5.2.1 Long Rest: all Hit Points back; Temporary Hit Points end (F67); the Death Saving Throws reset (the Hit Point owner)
	if _, err := restoreAllHP(charID); answerDBFailure(w, err, "the character") {
		return
	}
	// Reset everything else for long rest
	db.Exec(`
		UPDATE characters SET
			spell_slots_used = '{}',
			pact_slots_used = '{}',
			concentrating_on = NULL,
			hit_dice_spent = $2,
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
	`, charID, newHitDiceSpent)
	longRestConditions(charID) // the markers end, one level of Exhaustion goes (condition_owner_2024.go)
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
// @Param Authorization header string true "Bearer <token>"
// @Body Cover type (none, half, three_quarters, full)
// @Success 200 {object} map[string]interface{} "Cover set"
// @Router /characters/{id}/cover [post]
func handleSetCover(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Cover string `json:"cover"` // none, half, three_quarters or full
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

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
