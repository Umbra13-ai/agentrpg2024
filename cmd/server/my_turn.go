package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// buildMovementInfo returns movement info with prone status
// Shows crawling penalty and stand action when prone
// buildActionEconomy constructs the action economy info for /api/my-turn response
// Includes Extra Attack tracking
func buildActionEconomy(charID int, class string, level int, actionUsed, bonusActionUsed, reactionUsed bool,
	movementRemaining int, species string, bonusActionSpellCast bool, cantripsOnlyWarning string,
	conditions []string, actionStatus, bonusActionStatus, reactionStatus string,
	attacksRemaining sql.NullInt32) map[string]interface{} {

	result := map[string]interface{}{
		"action":                  !actionUsed,
		"action_status":           actionStatus,
		"bonus_action":            !bonusActionUsed,
		"bonus_action_status":     bonusActionStatus,
		"reaction":                !reactionUsed,
		"reaction_status":         reactionStatus,
		"movement_remaining_ft":   movementRemaining,
		"movement_speed_ft":       getMovementSpeed(species),
		"bonus_action_spell_cast": bonusActionSpellCast,
		"cantrips_only_warning":   cantripsOnlyWarning,
		"is_prone":                conditionListHas(conditions, "prone"),
	}

	// Extra Attack info
	totalAttacks := attacksPerAction(charID, class, level)
	if totalAttacks > 1 {
		result["extra_attack"] = true
		result["total_attacks_per_action"] = totalAttacks

		if attacksRemaining.Valid && attacksRemaining.Int32 > 0 {
			// In the middle of an Attack action
			result["attacks_remaining"] = attacksRemaining.Int32
			result["action_status"] = fmt.Sprintf("IN PROGRESS: Attack action (%d of %d attacks remaining)", attacksRemaining.Int32, totalAttacks)
			result["action"] = true // Can still attack
		} else if !actionUsed {
			// Haven't started attacking yet
			result["attacks_remaining"] = totalAttacks
			result["extra_attack_note"] = fmt.Sprintf("Extra Attack: You can make %d attacks when you take the Attack action.", totalAttacks)
		} else {
			// Action fully consumed
			result["attacks_remaining"] = 0
		}
	}

	return result
}

func buildMovementInfo(baseSpeed int, movementRemaining int, conditions []string) string {
	isProne := conditionListHas(conditions, "prone")

	if isProne {
		standCost := baseSpeed / 2
		effectiveMovement := movementRemaining / 2 // How far you can actually crawl
		return fmt.Sprintf("You have %dft of movement remaining. ⚠️ PRONE: Crawling costs 2ft per 1ft moved (effective: %dft). Use 'stand' action to stand up (costs %dft movement).", movementRemaining, effectiveMovement, standCost)
	}
	return fmt.Sprintf("You have %dft of movement remaining.", movementRemaining)
}

// handleMyTurn godoc
// @Summary Get full context to act
// @Description Returns everything needed to take your turn. No memory required - designed for stateless agents. round (with the turn schedule; null without): the open round, its kind (combat or noncombat), final_check_in, your status in it, next_check_in and how_to_send: outside a fight a game action is saved as your submission for the round and the GM applies it at a check-in. now, more and elsewhere (first in the reply, after is_my_turn): what fits this scene with the request to send, everything else you can send with why it is not first (nothing is hidden), and what you do outside a turn (rest, prepare spells, equip, shop, level-up choices, pick up).
// @Tags Actions
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param view query string false "short: the menu (now, more, elsewhere) without the long action lists, the class features and spell lists, older recent events, the rests, shops and standing tips"
// @Param tips query string false "1: keep the standing tips (how_to_act, rules_reminder) in the short view"
// @Success 200 {object} map[string]interface{} "Turn context with character, situation, options, and suggestions"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "No active game"
// @Router /my-turn [get]
func handleMyTurn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Get character and campaign info
	var charID, lobbyID, hp, maxHP, ac, level, tempHP, charXP, charGold int
	var charCopper, charSilver, charElectrum, charPlatinum int
	var str, dex, con, intl, wis, cha int
	var charName, class, species, lobbyName, setting, lobbyStatus string
	var charSubclass sql.NullString
	var conditionsJSON, slotsUsedJSON, campaignDocRaw, subclassChoicesJSON []byte
	var concentratingOn string
	var deathSuccesses, deathFailures, pendingASI, movementRemaining int
	var isStable, isDead, reactionUsed, actionUsed, bonusActionUsed, bonusActionSpellCast, hordeUsed bool
	var mountedOnCreature sql.NullString
	var mountIsControlled sql.NullBool
	var attacksRemaining sql.NullInt32
	var wildShapeForm sql.NullString
	var classLevelsJSONMyTurn []byte // For Primal Champion

	charID, _, err = actingCharacter(db, r, agentID, 0, 0) // acting_character_2024.go
	if answerNoActiveGame(w, err, http.StatusOK, map[string]interface{}{"is_my_turn": false}) {
		return
	}
	settleMovement(charID) // a Speed effect that arrived or ended during the turn (speed_owner_2024.go)
	err = db.QueryRow(`
		SELECT c.id, c.name, c.class, c.species, COALESCE(c.subclass, ''), c.level, c.hp, c.max_hp, c.ac,
			c.str, c.dex, c.con, c.intl, c.wis, c.cha,
			l.id, l.name, COALESCE(l.setting, ''), l.status,
			COALESCE(c.temp_hp, 0), COALESCE(c.conditions, '[]'), COALESCE(c.spell_slots_used, '{}'),
			COALESCE(c.concentrating_on, ''), COALESCE(c.death_save_successes, 0), COALESCE(c.death_save_failures, 0),
			COALESCE(c.is_stable, false), COALESCE(c.is_dead, false), COALESCE(c.reaction_used, false),
			COALESCE(c.xp, 0), COALESCE(c.gold, 0), COALESCE(c.copper, 0), COALESCE(c.silver, 0),
			COALESCE(c.electrum, 0), COALESCE(c.platinum, 0), COALESCE(c.pending_asi, 0),
			COALESCE(c.action_used, false), COALESCE(c.bonus_action_used, false), COALESCE(c.movement_remaining, 30),
			COALESCE(c.bonus_action_spell_cast, false),
			COALESCE(l.campaign_document, '{}'),
			c.mounted_on_creature, c.mount_is_controlled,
			c.attacks_remaining,
			COALESCE(c.subclass_choices, '{}'), COALESCE(c.horde_breaker_used, false),
			c.wild_shape_form,
			COALESCE(c.class_levels, '{}')
		FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.id = $1
	`, charID).Scan(&charID, &charName, &class, &species, &charSubclass, &level, &hp, &maxHP, &ac,
		&str, &dex, &con, &intl, &wis, &cha,
		&lobbyID, &lobbyName, &setting, &lobbyStatus,
		&tempHP, &conditionsJSON, &slotsUsedJSON, &concentratingOn,
		&deathSuccesses, &deathFailures, &isStable, &isDead, &reactionUsed, &charXP, &charGold, &charCopper, &charSilver,
		&charElectrum, &charPlatinum, &pendingASI,
		&actionUsed, &bonusActionUsed, &movementRemaining, &bonusActionSpellCast, &campaignDocRaw,
		&mountedOnCreature, &mountIsControlled, &attacksRemaining, &subclassChoicesJSON, &hordeUsed,
		&wildShapeForm, &classLevelsJSONMyTurn)
	reactionUsed = reactionUsed || charCantTakeReactions(charID) // shown as unavailable while "can't take Reactions" stands

	if answerNoActiveGame(w, err, http.StatusOK, map[string]interface{}{"is_my_turn": false}) { // gone since the lookup above
		return
	}
	autoEquipped := autoEquipArmor(charID) // plain armor put on outside combat (F214, gear_reminders_2024.go)
	if autoEquipped != nil {
		db.QueryRow(`SELECT ac FROM characters WHERE id = $1`, charID).Scan(&ac)
	}

	// Get party members
	rows, qerr := db.Query(`
		SELECT id, name, class, species, hp, max_hp, ac, COALESCE(is_stable, false), COALESCE(is_dead, false)
		FROM characters WHERE lobby_id = $1 AND id != $2
	`, lobbyID, charID)
	if qerr != nil {
		writeDBError(w, qerr, "the party")
		return
	}
	defer rows.Close()

	allies := []string{}
	partyStatus := []map[string]interface{}{}
	for rows.Next() {
		var pid, php, pmaxHP, pac int
		var pname, pclass, pspecies string
		var pstable, pdead bool
		rows.Scan(&pid, &pname, &pclass, &pspecies, &php, &pmaxHP, &pac, &pstable, &pdead)

		status := game.Vitals{HP: php, Max: pmaxHP, Stable: pstable, Dead: pdead}.Health() // the health owner (game/hit_points.go)

		allies = append(allies, fmt.Sprintf("%s (%s, %d/%d HP)", pname, pclass, php, pmaxHP))
		partyStatus = append(partyStatus, map[string]interface{}{
			"character_id": pid, "name": pname, "class": pclass, "species": pspecies,
			"hp": php, "max_hp": pmaxHP, "ac": pac,
			"status": status,
		})
	}

	// Get recent actions as events (including GM narrations which have no character_id)
	recs, _ := readActions(actionFilter{lobby: lobbyID, newestFirst: true, limit: 40}) // action_log_2024.go
	recentEvents := []string{}
	var latestNarration string
	var narrationAt time.Time
	sinceMine, mineSeen := []string{}, false // what happened since the character's own last action (I-CALLA-6, I-CALLA-16)
	var fightOverAt time.Time                // a fight ended and the GM hasn't narrated since (I-CALLA-18)
	for i, rec := range recs {
		line := rec.eventLine()
		if rec.Type == actionFightOver && latestNarration == "" && fightOverAt.IsZero() {
			fightOverAt = rec.At
		}
		if rec.isNarration() && latestNarration == "" {
			latestNarration, narrationAt = rec.Description, rec.At // the most recent DM narration
		}
		if i < 10 {
			recentEvents = append(recentEvents, line)
		}
		if rec.isOwnActionOf(charID) {
			mineSeen = true
		}
		if !mineSeen {
			sinceMine = append(sinceMine, line)
		}
	}
	slices.Reverse(recentEvents) // oldest first: the newest event is last
	slices.Reverse(sinceMine)

	// the character's own status: the health owner (game/hit_points.go), which the hints below follow
	charStatus := game.Vitals{HP: hp, Max: maxHP, Stable: isStable, Dead: isDead}.Health()

	// Every action type this character has now, by part of the turn, with the request to send and the uses left (action_types_2024.go)
	actions, featBonus, featFree := turnOptions(charID)
	classKey := strings.ToLower(class)
	// two ways of attacking that aren't action types of their own
	if classKey == "barbarian" && level >= 2 {
		actions = append(actions, map[string]interface{}{"name": "Reckless Attack", "action": "attack", "use": `{"action": "attack", "target_id": -1, "description": "reckless attack"}`,
			"description": "Advantage on your Strength attack rolls this turn; attack rolls against you have Advantage until your next turn."})
	}
	if classKey == "rogue" {
		actions = append(actions, map[string]interface{}{"name": "Sneak Attack", "action": "attack", "use": `{"action": "attack", "target_id": -1}`,
			"description": "Once a turn, extra damage when you have Advantage or an ally is within 5 feet of your target (the server applies it on a hit)."})
	}
	if bonusActionUsed {
		featBonus = nil
	}

	// Build tactical suggestions based on situation
	suggestions := partyHints(charStatus, partyStatus)

	// Build contextual rules reminder
	rulesReminder := map[string]interface{}{}
	if classKey == "barbarian" && level >= 2 {
		rulesReminder["reckless_attack"] = "You can attack recklessly (include 'reckless' in attack description) for advantage on STR melee attacks, but attacks against you also have advantage until your next turn."
		rulesReminder["danger_sense"] = "You have advantage on DEX saving throws against effects you can see (traps, spells). Disabled if blinded, deafened, or incapacitated."
	}
	// Disciplined Survivor (Monk 14) and Slippery Mind (Rogue 15)
	if classKey == "monk" && level >= 14 {
		rulesReminder["disciplined_survivor"] = "You have proficiency in all saving throws. When you fail a saving throw, you can spend 1 Focus Point to reroll it and must use the new roll: the action disciplined_survivor."
	}
	if classKey == "rogue" && level >= 15 {
		rulesReminder["slippery_mind"] = "You have proficiency in Wisdom and Charisma saving throws."
	}
	if c, ok := srdClasses[classKey]; ok && c.Spellcasting != "" {
		spellMod := 0
		switch c.Spellcasting {
		case "INT":
			spellMod = game.Modifier(intl)
		case "WIS":
			spellMod = game.Modifier(wis)
		case "CHA":
			spellMod = game.Modifier(cha)
		}
		saveDC := game.SpellSaveDC(level, spellMod)
		rulesReminder["spellcasting"] = fmt.Sprintf("Your spellcasting ability is %s. Spell save DC: %d. Spell attack bonus: +%d.", c.Spellcasting, saveDC, spellMod+game.ProficiencyBonus(level))
	}

	// Parse conditions from JSON
	var conditions []string
	json.Unmarshal(conditionsJSON, &conditions)

	// Parse spell slots
	var slotsUsed map[string]int
	json.Unmarshal(slotsUsedJSON, &slotsUsed)
	totalSlots := characterSpellSlots(charID, class, level)

	// Check combat state for initiative-based turn tracking
	isMyTurn := true
	companionTurn := 0 // a familiar's own turn, taken by this character's player (companions_2024.go)
	inCombat := false
	var combatInfo map[string]interface{}

	var combatRound, turnIndex int
	var turnOrderJSON []byte
	var combatActive bool
	var myTurnStartedAt sql.NullTime
	err = db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active, COALESCE(turn_started_at, NOW())
		FROM combat_state WHERE lobby_id = $1
	`, lobbyID).Scan(&combatRound, &turnIndex, &turnOrderJSON, &combatActive, &myTurnStartedAt)

	if err == nil && combatActive {
		inCombat = true

		type InitEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Initiative int    `json:"initiative"`
		}
		var entries []InitEntry
		json.Unmarshal(turnOrderJSON, &entries)

		currentTurnID := 0
		currentTurnName := ""
		if len(entries) > turnIndex {
			currentTurnID = entries[turnIndex].ID
			currentTurnName = entries[turnIndex].Name
		}

		// Check if it's this character's turn (or its familiar's, when familiars have their own Initiative: companions_2024.go)
		isMyTurn = currentTurnID == charID
		if isCompanionCombatID(currentTurnID) && companionTurnOwner(currentTurnID) == charID {
			isMyTurn = true
			companionTurn = currentTurnID - companionIDBase
		}

		combatInfo = map[string]interface{}{
			"round":         combatRound,
			"turn_order":    entries,
			"current_turn":  currentTurnName,
			"your_position": -1,
		}

		// Find this character's position in initiative
		for i, e := range entries {
			if e.ID == charID {
				combatInfo["your_position"] = i + 1 // 1-indexed
				combatInfo["your_initiative"] = e.Initiative
				break
			}
		}

		// Add turn timeout info if it's my turn
		if isMyTurn && myTurnStartedAt.Valid {
			elapsed := time.Since(myTurnStartedAt.Time)
			elapsedMinutes := int(elapsed.Minutes())
			combatInfo["turn_elapsed_minutes"] = elapsedMinutes
			if elapsedMinutes >= 120 {
				combatInfo["warning"] = "⏰ It has been your slot for over 2 hours. The fight doesn't wait: when the GM checks in and no turn is in, the GM takes your turn. Send it now (POST /api/pending-turn, or POST /api/action)."
			}
		}
	}

	roundNote := roundOrNil(lobbyID, charID) // with the turn schedule: the open round and this character's place in it
	if note, ok := roundNote.(map[string]interface{}); ok && !inCombat {
		if open, ok := note["your_turn"].(bool); ok {
			isMyTurn = open // sent or resolved this round: wait for the next one (check_in_2024.go)
		}
	}
	heldNote := "" // the GM holds the table before round 1 (table_hold_2024.go)
	if !inCombat && tableIsHeld(lobbyID) {
		isMyTurn, heldNote = false, tableHeldMessage(lobbyID)
	}
	awaitingGMNarration := false
	awaitingGMNarrationMessage := ""
	if !inCombat && beatApplies(lobbyID) {
		if acted, actionType, actionDesc, actedAt := hasCharacterActedSinceLastNarration(charID, lobbyID); acted {
			awaitingGMNarration = true
			isMyTurn = false
			awaitingGMNarrationMessage = fmt.Sprintf("You already acted (%s) at %s. Wait for GM narration before taking another major action.", actionType, actedAt.UTC().Format(time.RFC3339))
			if strings.TrimSpace(actionDesc) != "" {
				awaitingGMNarrationMessage = fmt.Sprintf("You already acted (%s: %s) at %s. Wait for GM narration before taking another major action.", actionType, actionDesc, actedAt.UTC().Format(time.RFC3339))
			}
		}
	}

	// Build character info
	xpToNext := getXPForNextLevel(level) - charXP

	// Build subclass info for my-turn
	var myTurnSubclassInfo interface{}
	if charSubclass.Valid && charSubclass.String != "" {
		subSlug := charSubclass.String
		if sub, ok := game.AvailableSubclasses[subSlug]; ok {
			activeFeatures := characterSubclassFeatures(charID, class, subSlug, level) // multiclass: every class's subclass at its own level
			featuresInfo := []map[string]interface{}{}
			for _, f := range activeFeatures {
				featuresInfo = append(featuresInfo, map[string]interface{}{
					"name":        f.Name,
					"description": f.Description,
				})
			}
			myTurnSubclassInfo = map[string]interface{}{
				"slug":            subSlug,
				"name":            sub.Name,
				"active_features": featuresInfo,
			}
		} else {
			myTurnSubclassInfo = subSlug
		}
	}

	// Build class features for my-turn
	myTurnClassFeatures := []map[string]interface{}{}
	for _, f := range characterClassFeatures(charID, class, level) {
		myTurnClassFeatures = append(myTurnClassFeatures, map[string]interface{}{
			"name":        f.Name,
			"description": f.Description,
		})
	}

	characterInfo := map[string]interface{}{
		"id":                charID,
		"name":              charName,
		"class":             class,
		"subclass":          myTurnSubclassInfo,
		"class_features":    myTurnClassFeatures,
		"species":           species,
		"level":             level,
		"hp":                hp,
		"max_hp":            maxHP,
		"temp_hp":           tempHP,
		"ac":                ac,
		"status":            charStatus,
		"conditions":        conditions,
		"conditions_detail": conditionsDetail(lobbyID, charID, characterSpeed(charID, species), false),
		"proficiency_bonus": game.ProficiencyBonus(level),
		"xp":                charXP,
		"xp_to_next_level":  xpToNext,
		"currency": map[string]interface{}{
			"cp": charCopper, "sp": charSilver, "ep": charElectrum, "gp": charGold, "pp": charPlatinum,
			"total_in_gp": float64(charCopper)/100 + float64(charSilver)/10 + float64(charElectrum)/2 + float64(charGold) + float64(charPlatinum)*10,
		},
		"gold":        charGold, // Keep for backwards compatibility
		"pending_asi": pendingASI,
		"stats": func() map[string]int {
			// Apply Primal Champion bonus to STR/CON for level 20 Barbarians
			var classLevelsMyTurn map[string]int
			json.Unmarshal(classLevelsJSONMyTurn, &classLevelsMyTurn)
			effectiveStr := getEffectiveAbilityScore(str, "str", class, level, classLevelsMyTurn)
			effectiveCon := getEffectiveAbilityScore(con, "con", class, level, classLevelsMyTurn)
			return map[string]int{
				"str": effectiveStr, "dex": dex, "con": effectiveCon,
				"int": intl, "wis": wis, "cha": cha,
			}
		}(),
		"modifiers": func() map[string]int {
			// Apply Primal Champion bonus to STR/CON modifiers
			var classLevelsMyTurn map[string]int
			json.Unmarshal(classLevelsJSONMyTurn, &classLevelsMyTurn)
			effectiveStr := getEffectiveAbilityScore(str, "str", class, level, classLevelsMyTurn)
			effectiveCon := getEffectiveAbilityScore(con, "con", class, level, classLevelsMyTurn)
			return map[string]int{
				"str": game.Modifier(effectiveStr), "dex": game.Modifier(dex), "con": game.Modifier(effectiveCon),
				"int": game.Modifier(intl), "wis": game.Modifier(wis), "cha": game.Modifier(cha),
			}
		}(),
	}
	advancementFields(characterInfo, lobbyID) // XP or milestones, as the campaign's table rule says (advancement_2024.go)

	// Add equipped weapons to character info
	var equippedMainHandMyTurn, equippedOffHandMyTurn sql.NullString
	db.QueryRow(`SELECT equipped_main_hand, equipped_off_hand FROM characters WHERE id = $1`, charID).Scan(&equippedMainHandMyTurn, &equippedOffHandMyTurn)
	if equippedMainHandMyTurn.Valid || equippedOffHandMyTurn.Valid {
		equippedWeapons := map[string]interface{}{}
		if equippedMainHandMyTurn.Valid && equippedMainHandMyTurn.String != "" {
			equippedWeapons["main_hand"] = equippedMainHandMyTurn.String
		}
		if equippedOffHandMyTurn.Valid && equippedOffHandMyTurn.String != "" {
			equippedWeapons["off_hand"] = equippedOffHandMyTurn.String
		}
		if len(equippedWeapons) > 0 {
			characterInfo["equipped_weapons"] = equippedWeapons
		}
	}

	// Add class resources (Focus Points, Rage, Sorcery Points, etc.)
	myTurnChaMod := game.Modifier(cha)
	maxResources := characterMaxResources(charID, class, level, myTurnChaMod)
	if len(maxResources) > 0 {
		currentResources := getCurrentClassResources(charID)
		resourceList := []map[string]interface{}{}
		for _, res := range characterResourceDefs(charID, class) {
			if max, ok := maxResources[res.Key]; ok && max > 0 {
				current := currentResources[res.Key]
				resourceList = append(resourceList, map[string]interface{}{
					"name":          res.Name,
					"key":           res.Key,
					"current":       current,
					"max":           max,
					"recover_short": res.RecoverShort,
					"unit":          nilIfEmpty(res.Unit), // a pool's unit ("Hit Points" for Lay on Hands); none: counted uses
				})
			}
		}
		characterInfo["class_resources"] = resourceList
	}

	// Add Wild Shape info for Druids
	if wildShapeForm.Valid && wildShapeForm.String != "" {
		// Character is in Wild Shape - fetch beast details
		var beastName, beastSize, beastType string
		var beastAC, beastSpeed, beastStr, beastDex, beastCon int
		var beastActionsJSON []byte
		monsterQueryRow(`
			SELECT name, size, type, ac, speed, str, dex, con, COALESCE(actions, '[]')
			FROM monsters WHERE slug = $1
		`, wildShapeForm.String).Scan(&beastName, &beastSize, &beastType, &beastAC, &beastSpeed,
			&beastStr, &beastDex, &beastCon, &beastActionsJSON)

		var beastActions []map[string]interface{}
		json.Unmarshal(beastActionsJSON, &beastActions)
		actionNames := []string{}
		for _, a := range beastActions {
			if name, ok := a["name"].(string); ok {
				actionNames = append(actionNames, name)
			}
		}

		characterInfo["wild_shape"] = map[string]interface{}{
			"active": true,
			"form":   beastName,
			"slug":   wildShapeForm.String,
			"ac":     beastAC,
			"speed":  beastSpeed,
			"stats": map[string]interface{}{
				"str": beastStr, "dex": beastDex, "con": beastCon,
				"str_mod": game.Modifier(beastStr), "dex_mod": game.Modifier(beastDex), "con_mod": game.Modifier(beastCon),
			},
			"actions": actionNames,
			"note":    "You use the beast's Strength, Dexterity, Constitution, AC, Speed and attacks. You keep your own Hit Points (plus the Temporary Hit Points Wild Shape gave), Intelligence, Wisdom, Charisma, proficiencies and class features. You can't cast spells unless you have Beast Spells (level 18). The form ends if you have the Incapacitated condition (0 Hit Points makes you Unconscious) or die.",
		}

		// Override displayed HP/AC for beast form
		characterInfo["wild_shape_warning"] = fmt.Sprintf("⚠️ You are in %s form (your own Hit Points: %d/%d, Temporary Hit Points: %d).", beastName, hp, maxHP, tempHP)
		characterInfo["wild_shape"].(map[string]interface{})["known_forms"] = knownWildShapes(charID)
	} else if strings.ToLower(class) == "druid" && level >= 2 {
		// Show Wild Shape as available action
		maxCR := 0.25
		if level >= 8 {
			maxCR = 1.0
		} else if level >= 4 {
			maxCR = 0.5
		}
		characterInfo["wild_shape_available"] = map[string]interface{}{
			"max_cr":   maxCR,
			"cr_limit": fmt.Sprintf("CR %.2g or lower", maxCR),
			"usage":    "Use 'wild_shape' action with beast name (e.g., 'wild_shape wolf' or 'wild_shape brown-bear')",
		}
	}

	// Add Metamagic info for Sorcerers
	if strings.ToLower(class) == "sorcerer" && level >= 3 {
		knownMetamagic := getMetamagicChoices(charID)
		maxChoices := getMaxMetamagicChoices(level)

		if len(knownMetamagic) > 0 {
			metamagicInfo := []map[string]interface{}{}
			for _, slug := range knownMetamagic {
				if opt, ok := metamagicOptions[slug]; ok {
					info := map[string]interface{}{
						"name": opt.Name,
						"slug": opt.Slug,
						"cost": opt.Cost,
					}
					if opt.CostFormula != "" {
						info["cost_formula"] = opt.CostFormula
					}
					metamagicInfo = append(metamagicInfo, info)
				}
			}
			characterInfo["metamagic"] = map[string]interface{}{
				"known":       metamagicInfo,
				"known_count": len(knownMetamagic),
				"max_choices": maxChoices,
				"usage":       "Include metamagic keyword in spell description, e.g., 'quickened fireball', 'twinned haste'",
			}
		} else {
			characterInfo["metamagic_prompt"] = fmt.Sprintf("You can learn %d Metamagic options! GET /api/characters/metamagic?character_id=%d to see choices", maxChoices, charID)
		}
	}

	// Add ASI notification if points available
	if pendingASI > 0 {
		characterInfo["asi_available"] = true
		characterInfo["asi_message"] = fmt.Sprintf("You have %d ability score improvement points to spend! POST /api/characters/%d/asi OR take a feat (costs 2 points): POST /api/characters/%d/feat", pendingASI, charID, charID)
		characterInfo["feat_option"] = fmt.Sprintf("GET /api/characters/%d/feat for available feats", charID)
	}

	// Add subclass prompt if eligible but hasn't chosen
	if myTurnSubclassInfo == nil {
		// Check if eligible for subclass
		availableSubs := getSubclassesForClass(class)
		if len(availableSubs) > 0 {
			// Get the subclass level for this class (from first available subclass)
			subclassLevel := 3
			for _, sub := range availableSubs {
				subclassLevel = sub.SubclassLevel
				break
			}
			if level >= subclassLevel {
				characterInfo["subclass_available"] = true
				characterInfo["subclass_message"] = fmt.Sprintf("You can choose a subclass! GET /api/characters/subclass?character_id=%d to see options.", charID)
			}
		}
	}

	// Add concentration if active
	if concentratingOn != "" {
		characterInfo["concentrating_on"] = concentratingOn
	}
	if eff := spellEffectViews(lobbyID, charID); len(eff) > 0 {
		characterInfo["spell_effects"] = eff // Bless, Bane, Slow, Shield of Faith, Command ... on this character
	}
	if dk := deckEffects(charID); len(dk) > 0 {
		characterInfo["deck_effects"] = dk // lasting Mysterious Deck cards (deck_choices_2024.go)
	}

	// at 0 Hit Points or dead: the hint and the actions follow the health owner's label (I-ALDRIC-5)
	if game.AtZero(charStatus) {
		characterInfo["status"] = charStatus
		suggestions = []string{zeroHPHint(charStatus, inCombat)}
		if charStatus != game.HealthDead {
			characterInfo["death_saves"] = map[string]interface{}{
				"successes": deathSuccesses,
				"failures":  deathFailures,
				"stable":    isStable,
			}
		}
		actions = []map[string]interface{}{}
		if charStatus == game.HealthDying {
			actions = []map[string]interface{}{
				{"name": "death_save", "description": "Made once at the start of your turn, no action (in a fight the server rolls it). A d20: 10+ = success, <10 = failure. 3 successes = stable. 3 failures = death. Nat 20 = regain 1 HP. Nat 1 = 2 failures."},
			}
		}
		if charStatus == game.HealthDead {
			characterInfo["is_dead"] = true
			isMyTurn = false
		}
	}

	// Add spell slots if caster
	if len(totalSlots) > 0 {
		remainingSlots := map[string]int{}
		for lvl, total := range totalSlots {
			key := fmt.Sprintf("%d", lvl)
			used := slotsUsed[key]
			remainingSlots[key] = total - used
		}
		characterInfo["spell_slots"] = map[string]interface{}{
			"total":     totalSlots,
			"remaining": remainingSlots,
		}
	}

	// Add known spells
	var knownSpellsJSON []byte
	db.QueryRow("SELECT COALESCE(known_spells, '[]') FROM characters WHERE id = $1", charID).Scan(&knownSpellsJSON)
	var knownSpells []string
	json.Unmarshal(knownSpellsJSON, &knownSpells)
	if len(knownSpells) > 0 {
		// Enrich with spell info for easy reference
		spellsAvailable := []map[string]interface{}{}
		for _, slug := range knownSpells {
			if spell, ok := srdSpellsMemory[slug]; ok {
				spellsAvailable = append(spellsAvailable, map[string]interface{}{
					"slug":         slug,
					"name":         spell.Name,
					"level":        spell.Level,
					"casting_time": spell.CastingTime,
					"components":   spell.Components,
					"is_ritual":    spell.IsRitual,
				})
			}
		}
		characterInfo["known_spells"] = spellsAvailable
	}

	// Climb Speed (Second-Story Work), Acrobatic Movement
	if notes := movementFeatureNotes(charID); len(notes) > 0 {
		characterInfo["movement_features"] = notes
	}
	if pact := characterPactSlots(charID); len(pact) > 0 { // a multiclass Warlock's Pact Magic slots, apart from the other slots
		var pactUsedJSON []byte
		db.QueryRow(`SELECT COALESCE(pact_slots_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&pactUsedJSON)
		pactUsed := map[string]int{}
		json.Unmarshal(pactUsedJSON, &pactUsed)
		total, remaining := map[string]int{}, map[string]int{}
		for lvl, n := range pact {
			k := strconv.Itoa(lvl)
			total[k], remaining[k] = n, n-pactUsed[k]
		}
		characterInfo["pact_slots"] = map[string]interface{}{"total": total, "remaining": remaining,
			"note": "Pact Magic: they come back on a Short Rest. A cast uses one when no other slot of that level is left, or say \"pact slot\"."}
	}
	characterInfo["inventory"] = carriedView(charID) // what the character carries, in short (character_equipment.go)
	characterInfo["class_levels"] = classLevelsOf(charID)
	characterInfo["hit_dice_by_type"] = hitDiceLeftByType(charID) // the dice left to spend, as the rest options say (multiclass_2024.go)

	// Words of Creation (Bard 20): Power Word Heal and Power Word Kill always prepared
	if woc := wordsOfCreationSpells(class, level); len(woc) > 0 {
		characterInfo["words_of_creation"] = "Power Word Heal and Power Word Kill are always prepared; a cast of either can target a second creature within 10 feet of the first (target_ids)."
	}

	// Add domain/subclass spells - always prepared, don't count against limits
	// For Land druids, include Circle Spells based on chosen land type
	if charSubclass.Valid && charSubclass.String != "" {
		var domainSubclassChoices map[string]string
		json.Unmarshal(subclassChoicesJSON, &domainSubclassChoices)
		landType := domainSubclassChoices["circle_land"]
		domainSpells := getDomainSpellsWithInfo(charSubclass.String, level, landType)
		if len(domainSpells) > 0 {
			characterInfo["domain_spells"] = domainSpells
			// Show chosen land type for Land druids
			if charSubclass.String == "land" && landType != "" {
				characterInfo["circle_land"] = landType
			}
		}
	}

	// Add prepared spells for prepared casters
	if game.IsPreparedCaster(class) {
		var preparedSpellsJSON []byte
		var myTurnIntl, myTurnWis, myTurnCha int
		db.QueryRow("SELECT COALESCE(prepared_spells, '[]'), intl, wis, cha FROM characters WHERE id = $1", charID).Scan(&preparedSpellsJSON, &myTurnIntl, &myTurnWis, &myTurnCha)
		var preparedSpells []string
		json.Unmarshal(preparedSpellsJSON, &preparedSpells)

		preparedInfo := []map[string]interface{}{}
		for _, slug := range preparedSpells {
			if spell, ok := srdSpellsMemory[slug]; ok {
				preparedInfo = append(preparedInfo, map[string]interface{}{
					"slug":         slug,
					"name":         spell.Name,
					"level":        spell.Level,
					"casting_time": spell.CastingTime,
					"components":   spell.Components,
					"is_ritual":    spell.IsRitual,
				})
			}
		}

		maxPrepared := game.MaxPreparedSpells(class, level, myTurnIntl, myTurnWis, myTurnCha)
		characterInfo["prepared_spells"] = preparedInfo
		characterInfo["max_prepared"] = maxPrepared
		characterInfo["caster_type"] = "prepared"
		characterInfo["prepared_tip"] = "Use POST /api/characters/{id}/prepare to change prepared spells after a long rest."
	}

	// Add feats
	var featsJSONMyTurn []byte
	db.QueryRow("SELECT COALESCE(feats, '[]') FROM characters WHERE id = $1", charID).Scan(&featsJSONMyTurn)
	var charFeats []string
	json.Unmarshal(featsJSONMyTurn, &charFeats)
	if len(charFeats) > 0 {
		featsInfo := []map[string]interface{}{}
		for _, slug := range charFeats {
			if feat, ok := lookupFeat(slug); ok {
				featsInfo = append(featsInfo, map[string]interface{}{
					"slug":     slug,
					"name":     feat.Name,
					"features": feat.Features,
				})
			}
		}
		characterInfo["feats"] = featsInfo
	}

	// Reaction status
	reactionStatus := "You have your reaction available."
	if reactionUsed {
		reactionStatus = "Your reaction has been used this round."
	}

	// Check for readied action
	readiedAction, hasReadiedAction, _ := readiedOf(db, charID) // readied_2024.go

	// Action economy status (for in-combat turns)
	actionStatus := "You have your action available."
	if actionUsed {
		actionStatus = "You have already used your action this turn."
	}
	bonusActionStatus := "You have your bonus action available."
	if bonusActionUsed {
		bonusActionStatus = "You have already used your bonus action this turn."
	}
	// Bonus action spell restriction warning
	cantripsOnlyWarning := ""
	if bonusActionSpellCast && !actionUsed {
		cantripsOnlyWarning = "⚠️ You cast a bonus action spell - you may only cast cantrips with your action this turn."
	}

	// Update character activity (log poll, update last_active)
	updateCharacterActivity(charID, "poll", "Checked game status")

	// Log the API request
	logAPIRequest(agentID, "/api/my-turn", "GET", lobbyID, charID, "", 200)

	// Get recent campaign messages (last 6 hours)
	recentMessages := getRecentCampaignMessages(lobbyID, 6)

	// Extract enemy information from combat turn_order
	enemies := []map[string]interface{}{}
	if inCombat && len(turnOrderJSON) > 0 {
		type FullTurnEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Initiative int    `json:"initiative"`
			IsMonster  bool   `json:"is_monster"`
			MonsterKey string `json:"monster_key"`
			HP         int    `json:"hp"`
			MaxHP      int    `json:"max_hp"`
			AC         int    `json:"ac"`
		}
		var fullEntries []FullTurnEntry
		json.Unmarshal(turnOrderJSON, &fullEntries)

		hiddenOnMap := hiddenMonsterIDs(lobbyID) // creatures the GM hid on the battle map are not listed
		for _, e := range fullEntries {
			if e.IsMonster && e.HP > 0 && !hiddenOnMap[e.ID] {
				healthStatus := game.MonsterHealth(e.HP, e.MaxHP) // how it looks (the health owner); never its numbers

				enemy := map[string]interface{}{
					"name":   e.Name,
					"id":     e.ID,         // Negative ID for targeting
					"status": healthStatus, // a monster's AC and exact HP are never shown to players
				}

				if cs := conditionNames(lobbyID, e.ID); len(cs) > 0 {
					enemy["conditions"] = cs // visible to everyone; the source and the DCs stay with the GM
				}

				// Add monster type info if available
				if e.MonsterKey != "" {
					var mType string
					monsterQueryRow("SELECT COALESCE(type, '') FROM monsters WHERE slug = $1", e.MonsterKey).Scan(&mType)
					if mType != "" {
						enemy["type"] = mType
					}
				}

				enemies = append(enemies, enemy)
			}
		}
	}

	var mapURL, mapReason string
	if inCombat {
		mapURL, mapReason = playerMapView(lobbyID, charID) // battle_map.go: the URL only when the character's view works
	}

	// Build enemies summary for situation (simple string list for backward compat)
	enemySummary := []string{}
	for _, e := range enemies {
		status := e["status"].(string)
		if cs, ok := e["conditions"].([]string); ok && len(cs) > 0 {
			status += ", " + strings.Join(cs, ", ")
		}
		summary := fmt.Sprintf("%s (%s)", e["name"], status)
		if mtype, ok := e["type"].(string); ok && mtype != "" {
			summary = fmt.Sprintf("%s [%s] (%s)", e["name"], mtype, status)
		}
		enemySummary = append(enemySummary, summary)
	}

	// Build response
	response := map[string]interface{}{
		"table_held":  nilIfEmpty(heldNote),
		"round":       roundNote, // with the turn schedule: the open round, your status in it, your next check-in (check_in_2024.go)
		"is_my_turn":  isMyTurn,
		"gm_asks_you": playerAskView(lobbyID, charID), // what the GM is waiting on you for (gm_asks_2024.go)
		"character":   characterInfo,
		"situation": map[string]interface{}{
			"summary":       fmt.Sprintf("You are in %s. %s", lobbyName, setting),
			"allies":        allies,
			"enemies":       enemySummary,
			"enemy_details": enemies, // Full enemy data for targeting
			"terrain":       "",      // TODO: track terrain when position system is built
			"in_combat":     inCombat,
		},
		"your_options": map[string]interface{}{
			"feature_words": featureWordsFor(charID), // the words that switch on this character's features: put them in "features" or the description
			"actions":       actions,
			"bonus_actions": featBonus,
			"free_actions":  featFree, // movement, speaking, one object, Action Surge, Font of Magic ... (action_types_2024.go)
			"movement":      buildMovementInfo(characterSpeed(charID, species), max(movementRemaining, 0), conditions) + speedNoteFor(lobbyID, charID),
			"rests":         restGuide(charID), // Short and Long Rest: Hit Point Dice, what comes back, the requests (rests_2024.go)
			"reaction":      reactionStatus,
			"action_economy": buildActionEconomy(charID, class, level, actionUsed, bonusActionUsed, reactionUsed,
				movementRemaining, species, bonusActionSpellCast, cantripsOnlyWarning, conditions,
				actionStatus, bonusActionStatus, reactionStatus, attacksRemaining),
		},
		"tactical_suggestions":   suggestions,
		"rules_reminder":         rulesReminder,
		"recent_events":          recentEvents,
		"recent_events_order":    "oldest first: the newest event is the last one",
		"since_your_last_action": sinceMine,
		"level_up":               nilIfNoNotices(unseenLevelUpNotices(charID)),
		"shops_open":             shopsOpenFor(lobbyID), // shops the GM has opened (campaign_shops_2024.go)
		"gm_says":                latestNarration,
		"story_so_far":           parseStorySoFar(campaignDocRaw),
		"party_status":           partyStatus,
		"how_to_act":             actionHowTo(enemies), // made from the action request (actions.go)
	}
	if latestNarration != "" { // when the GM said it, and the round it belongs to (feedback log 56: last round's words read as the new round's)
		response["gm_says_at"] = narrationAt
		if n, name, ok := roundAtTime(lobbyID, narrationAt); ok {
			response["gm_says_round"], response["gm_says_round_name"] = n, name // roundName: campaign round or fight round
		}
	}
	if autoEquipped != nil {
		response["auto_equipped"] = autoEquipped
	}
	response["left_behind"] = leftBehindView(charID) // items no longer carried and how to ask for them back; always present (dropped_items.go)
	if inCombat {
		if pt := pendingTurnForMyTurn(lobbyID, charID); pt != nil {
			response["pending_turn"] = pt                                        // send your whole turn ahead of your slot (pending_turns_2024.go)
			if _, howTo := pt["how"]; howTo && pt["yours"] == nil && !isMyTurn { // feedback log 48: is_my_turn false read as "wait"
				response["is_my_turn_note"] = "It isn't your initiative yet, but you can send your turn now: it is held and taken when the initiatives before yours are done (pending_turn.how shows the request)."
			}
		}
	}
	if rem := myTurnReminders(charID, inCombat, isMyTurn); rem != nil {
		response["reminders"] = rem // turn_left, after_battle, prep (F216, reminders_2024.go)
	}
	if !inCombat {
		if rem := optionalReminders(charID); len(rem) > 0 { // the portrait and the shop still to put to your human (optional_steps_2024.go)
			response["answer_with_your_human"] = rem
		}
		if g := gearReminders(charID); len(g) > 0 {
			response["gear_reminders"] = g // gear that could be worn and isn't; mute with POST /api/characters/gear-reminders (F214)
		}
	}
	if open := characterToFinish(charID); len(open) > 0 {
		response["level_up_choices"] = open // everything the character still has to choose: subclass, ASI, masteries, spells ... (F232)
		response["character_finished"] = false
		response["setup_guide"] = fmt.Sprintf("GET /api/characters/%d/setup: each step with the request to send", charID)
	}
	if oas := openOpportunityAttacks(lobbyOfChar(charID), charID); len(oas) > 0 {
		response["opportunity_attack_available"] = map[string]interface{}{"target_ids": oas, "how": "POST /api/action {\"action\":\"opportunity_attack\",\"target_id\": id}: one melee attack with your Reaction"}
	}
	if isBurning(lobbyOfChar(charID), charID) {
		response["burning"] = "You are burning: 1d4 Fire damage at the start of each of your turns. Action \"extinguish\" puts it out (you drop Prone and roll)."
	}
	if awaitingGMNarration {
		response["turn_state"] = "waiting_for_gm"
		response["turn_state_message"] = awaitingGMNarrationMessage
		response["awaiting_narration"] = true
		response["action_blocked_reason"] = "awaiting_narration"
	}

	if !fightOverAt.IsZero() && !inCombat {
		response["fight_over"] = fmt.Sprintf("The fight ended at %s and the GM hasn't narrated what follows yet: wait for that narration (gm_says) before you act.", fightOverAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	// combat: the fight, or null when there is none (always the key, always one value for "no fight": I-CALLA-7)
	response["combat"] = nil
	if inCombat {
		response["combat"] = combatInfo
		if !isMyTurn {
			response["message"] = fmt.Sprintf("It's not your turn. Current turn: %s", combatInfo["current_turn"])
			if note, ok := response["is_my_turn_note"].(string); ok { // you may still send your turn now (I-CALLA-5)
				response["message"] = fmt.Sprint(response["message"], ". ", note)
			}
		}
	}

	// Add readied action info if one is set
	if hasReadiedAction {
		response["readied_action"] = readiedView(readiedAction)

		// Update action economy to show readied action
		if opts, ok := response["your_options"].(map[string]interface{}); ok {
			if ae, ok := opts["action_economy"].(map[string]interface{}); ok {
				ae["has_readied_action"] = true
				ae["readied_trigger"] = readiedAction.Trigger
			}
		}
	}

	// Add condition effects if any conditions are active
	if len(conditions) > 0 {
		activeEffects := []string{}
		for _, cond := range conditions {
			if effect, ok := conditionEffects[cond]; ok {
				activeEffects = append(activeEffects, fmt.Sprintf("%s: %s", cond, effect))
			}
		}
		if len(activeEffects) > 0 {
			response["active_condition_effects"] = activeEffects
		}
	}

	// Add frightened movement restriction warning
	if hasAnyFrightened(charID) {
		sourceName := getFrightenedSourceName(charID)
		sourceID := getFrightenedSourceID(charID)

		movementWarning := "⚠️ FRIGHTENED: You cannot willingly move closer to the source of your fear."
		if sourceName != "" {
			movementWarning = fmt.Sprintf("⚠️ FRIGHTENED: You cannot willingly move closer to %s.", sourceName)
		}

		response["frightened_warning"] = map[string]interface{}{
			"warning":        movementWarning,
			"source_id":      sourceID,
			"source_name":    sourceName,
			"effects":        "Disadvantage on ability checks and attack rolls while the source is visible.",
			"movement_rules": "You may move away from the source, move perpendicular, or stay in place.",
			"action_advice":  "When submitting a move action, if you are moving toward the source, set toward_frightened_source: true (will be blocked).",
		}
	}

	// Add mounted combat info
	if mountedOnCreature.Valid && mountedOnCreature.String != "" {
		// Get mount details from monsters table
		var mountName string
		var mountSpeed, mountHP, mountAC int
		err := monsterQueryRow(`SELECT name, COALESCE(speed, 60), COALESCE(hp, 20), COALESCE(ac, 10)
			FROM monsters WHERE slug = $1`, mountedOnCreature.String).Scan(&mountName, &mountSpeed, &mountHP, &mountAC)
		if err != nil {
			mountName = mountedOnCreature.String
			mountSpeed = 60
			mountHP = 20
			mountAC = 10
		}

		controlType := "controlled"
		if mountIsControlled.Valid && !mountIsControlled.Bool {
			controlType = "independent"
		}

		mountInfo := map[string]interface{}{
			"mounted_on":     mountName,
			"mount_slug":     mountedOnCreature.String,
			"mount_speed_ft": mountSpeed,
			"mount_hp":       mountHP,
			"mount_ac":       mountAC,
			"control_type":   controlType,
		}

		if controlType == "controlled" {
			mountInfo["rules"] = map[string]interface{}{
				"movement":   fmt.Sprintf("You control the mount's movement (speed %dft). Use mount's speed instead of your own.", mountSpeed),
				"initiative": "The mount acts on your initiative.",
				"actions":    "The mount can only Dash, Disengage, or Dodge. You use YOUR action to direct it.",
				"dismount":   "Use POST /api/characters/dismount to dismount (costs half your movement).",
			}
		} else {
			mountInfo["rules"] = map[string]interface{}{
				"movement":     "The mount acts independently and may move as it wishes on its turn.",
				"initiative":   "The mount has its own initiative and acts on its own turn.",
				"actions":      "The mount can take any action it is capable of, not just Dash/Disengage/Dodge.",
				"coordination": "You can only move when the mount uses the Ready action to wait for you.",
				"dismount":     "Use POST /api/characters/dismount to dismount (costs half your movement).",
			}
		}

		response["mounted_combat"] = mountInfo

		// Update action economy to show mounted status
		if opts, ok := response["your_options"].(map[string]interface{}); ok {
			if ae, ok := opts["action_economy"].(map[string]interface{}); ok {
				ae["is_mounted"] = true
				ae["mount_speed_ft"] = mountSpeed
				if controlType == "controlled" {
					ae["movement_note"] = fmt.Sprintf("Mounted on %s - use mount's speed (%dft)", mountName, mountSpeed)
				}
			}
		}
	}

	// Warn about non-proficient armor penalties
	if isWearingNonProficientArmor(charID) {
		response["armor_penalty_warning"] = map[string]interface{}{
			"warning": "⚠️ You are wearing armor you are not proficient with!",
			"penalties": []string{
				"Disadvantage on all attack rolls (STR and DEX based)",
				"Disadvantage on STR and DEX ability checks",
				"Disadvantage on STR and DEX saving throws",
				"You cannot cast spells",
			},
			"how_to_resolve": "Unequip the armor (POST /api/characters/unequip-armor).",
		}
	}

	// Add recent campaign messages (last 6 hours)
	if len(recentMessages) > 0 {
		response["campaign_messages"] = recentMessages
	}

	// Dragonborn Breath Weapon info
	if strings.ToLower(species) == "dragonborn" {
		var breathWeaponUsed bool
		var draconicAncestry sql.NullString
		db.QueryRow("SELECT COALESCE(breath_weapon_used, false), draconic_ancestry FROM characters WHERE id = $1", charID).Scan(&breathWeaponUsed, &draconicAncestry)

		ancestry := ""
		if draconicAncestry.Valid {
			ancestry = draconicAncestry.String
		}

		breathWeaponInfo := map[string]interface{}{
			"available":       !breathWeaponUsed,
			"used_since_rest": breathWeaponUsed,
			"recovery":        "short or long rest",
		}

		if ancestry != "" {
			damageType := game.DragonAncestryDamageTypes[ancestry]
			area := dragonAncestryAreaShapes[ancestry]
			savingThrow := dragonAncestryBreathSavingThrows[ancestry]
			damageDice := getBreathWeaponDamageDice(level)
			dc := 8 + game.Modifier(con) + game.ProficiencyBonus(level)

			breathWeaponInfo["draconic_ancestry"] = ancestry
			breathWeaponInfo["damage_type"] = damageType
			breathWeaponInfo["damage_dice"] = damageDice
			breathWeaponInfo["area"] = area
			breathWeaponInfo["saving_throw"] = savingThrow
			breathWeaponInfo["dc"] = dc
			breathWeaponInfo["dc_formula"] = "8 + CON mod + proficiency"
			breathWeaponInfo["how_to_use"] = "POST /api/characters/breath-weapon with character_id, target_ids (array), description"

			if !breathWeaponUsed {
				breathWeaponInfo["tip"] = fmt.Sprintf("🔥 Breath Weapon ready! %s %s damage in %s, DC %d %s save for half.", damageDice, damageType, area, dc, savingThrow)
			}
		} else {
			breathWeaponInfo["ancestry_required"] = true
			breathWeaponInfo["warning"] = "Draconic ancestry not set: it is chosen at character creation (the draconic_ancestry field)."
			breathWeaponInfo["valid_ancestries"] = []string{"black", "blue", "brass", "bronze", "copper", "gold", "green", "red", "silver", "white"}
		}

		response["breath_weapon"] = breathWeaponInfo

	}

	// Orc Relentless Endurance status
	if isOrc(charID) {
		var relentlessUsed bool
		db.QueryRow("SELECT COALESCE(relentless_endurance_used, false) FROM characters WHERE id = $1", charID).Scan(&relentlessUsed)

		relentlessInfo := map[string]interface{}{
			"available":       !relentlessUsed,
			"used_since_rest": relentlessUsed,
			"recovery":        "long rest",
			"trigger":         "automatic when dropping to 0 HP (not killed outright)",
		}

		if !relentlessUsed {
			relentlessInfo["tip"] = "💪 Relentless Endurance ready! If you drop to 0 HP (but not from massive damage), you'll drop to 1 HP instead. (Orc, once per long rest)"
		}

		response["relentless_endurance"] = relentlessInfo
	}

	// Barbarian Relentless Rage status (level 11+)
	if strings.ToLower(class) == "barbarian" && level >= 11 {
		var relentlessUses int
		db.QueryRow("SELECT COALESCE(relentless_rage_uses, 0) FROM characters WHERE id = $1", charID).Scan(&relentlessUses)

		currentDC := 10 + (5 * relentlessUses)
		relentlessRageInfo := map[string]interface{}{
			"current_dc":      currentDC,
			"uses_since_rest": relentlessUses,
			"recovery":        "short or long rest",
			"trigger":         "CON save when dropping to 0 HP while raging",
			"requirement":     "must be raging",
		}

		if relentlessUses > 0 {
			relentlessRageInfo["tip"] = fmt.Sprintf("⚠️ Relentless Rage DC now %d (used %d times). Short/long rest to reset.", currentDC, relentlessUses)
		} else {
			relentlessRageInfo["tip"] = "🔥 Relentless Rage ready! If raging and dropped to 0 HP, make DC 10 CON save to stay at 1 HP."
		}

		response["relentless_rage"] = relentlessRageInfo
	}

	// Barbarian Indomitable Might (level 18+)
	// Parse class levels for multiclass support
	var classLevelsMyTurn map[string]int
	json.Unmarshal(classLevelsJSONMyTurn, &classLevelsMyTurn)
	if hasIndomitableMight(class, level, classLevelsMyTurn) {
		// Get effective STR score (includes Primal Champion bonus at level 20)
		effectiveStr := getEffectiveAbilityScore(str, "str", class, level, classLevelsMyTurn)
		response["indomitable_might"] = map[string]interface{}{
			"str_score":      effectiveStr,
			"minimum_result": effectiveStr,
			"affects":        "all Strength-based ability checks",
			"automatic":      true,
			"tip":            fmt.Sprintf("💪 Indomitable Might: Your STR checks can never be lower than %d!", effectiveStr),
		}
	}

	// Gnomish Cunning tip
	if isGnome(charID) {
		response["gnome_cunning"] = map[string]interface{}{
			"applies_to": []string{"INT saves", "WIS saves", "CHA saves"},
			"against":    "magic (spells, magical effects)",
			"automatic":  true,
			"tip":        "🧠 Gnomish Cunning: Advantage on Intelligence, Wisdom and Charisma saving throws.",
		}
	}

	// Fey Ancestry tip (Elf)
	if hasFeyAncestry(charID) {
		response["fey_ancestry"] = map[string]interface{}{
			"charm_save_advantage":   true,
			"magical_sleep_immunity": true,
			"automatic":              true,
			"tip":                    "✨ Your Fey Ancestry grants advantage on saves vs charm effects and immunity to magical sleep (Sleep spell)!",
		}
	}

	// Dwarven Resilience tip
	if isDwarf(charID) {
		response["dwarven_resilience"] = map[string]interface{}{
			"poison_save_advantage":    true,
			"poison_damage_resistance": true,
			"automatic":                true,
			"tip":                      "⛏️ Your Dwarven Resilience grants advantage on poison saves and halves poison damage!",
		}
	}

	// Halfling Brave tip
	if isHalfling(charID) {
		response["halfling_brave"] = map[string]interface{}{
			"frightened_save_advantage": true,
			"automatic":                 true,
			"tip":                       "💪 Your Halfling Brave grants advantage on saves vs frightened effects - stand tall!",
		}
	}

	// Warrior of the Open Hand 6, Wholeness of Body (2024): a Bonus Action, the Martial Arts die + Wisdom modifier Hit Points (at least 1),
	// Wisdom-modifier uses (at least 1) per Long Rest, counted in class_resources_used "wholeness_of_body" (sweep_2024.go).
	if strings.ToLower(class) == "monk" && level >= 6 {
		if _, sub, _ := subclassOf(charID); sub == "open-hand" {
			var wisScore int
			db.QueryRow(`SELECT wis FROM characters WHERE id = $1`, charID).Scan(&wisScore)
			uses := game.Modifier(wisScore)
			if uses < 1 {
				uses = 1
			}
			left := uses - classResourcesUsed(charID)["wholeness_of_body"]
			if left < 0 {
				left = 0
			}
			response["wholeness_of_body"] = map[string]interface{}{
				"uses_left":  left,
				"uses":       uses,
				"cost":       "a Bonus Action",
				"healing":    "your Martial Arts die + your Wisdom modifier (at least 1)",
				"recovery":   "a Long Rest",
				"how_to_use": `POST /api/characters/wholeness-of-body {"character_id": N}`,
			}
		}
	}

	// Cleric Divine Intervention (level 10, 2024): as a Magic action, cast any Cleric spell of level 5 or lower that doesn't need a
	// Reaction, with no spell slot and no Material components; once per Long Rest. Level 20 (Greater Divine Intervention): Wish too,
	// after which Divine Intervention returns only after 2d4 Long Rests.
	if strings.ToLower(class) == "cleric" && level >= 10 {
		var usedJSON []byte
		db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&usedJSON)
		usedRes := map[string]int{}
		json.Unmarshal(usedJSON, &usedRes)
		rests := divineInterventionRestsLeft(charID)
		available := usedRes["divine_intervention"] == 0 && rests == 0
		info := map[string]interface{}{
			"available":  available,
			"what":       "Cast any Cleric spell of level 5 or lower that doesn't need a Reaction, with no spell slot or Material components (a Magic action).",
			"how_to_use": `POST /api/action {"action": "cast", "spell_slug": "flame-strike", "description": "divine intervention"}`,
			"recovery":   "a Long Rest",
		}
		if level >= 20 {
			info["greater_divine_intervention"] = `You may choose Wish ("spell_slug": "wish"); then Divine Intervention returns only after 2d4 Long Rests.`
		}
		switch {
		case rests > 0:
			info["unavailable_reason"] = fmt.Sprintf("After Greater Divine Intervention it returns after %d more Long Rest(s).", rests)
		case !available:
			info["unavailable_reason"] = "Used since your last Long Rest."
		}
		response["divine_intervention"] = info
	}

	// Hunter Ranger Stand Against the Tide (level 15+)
	if strings.ToLower(class) == "ranger" && level >= 15 {
		if charSubclass.Valid && strings.ToLower(charSubclass.String) == "hunter" {
		}
	}

	// Fiend Warlock - Dark One's Own Luck (level 6+)
	if strings.ToLower(class) == "warlock" && level >= 6 {
		if charSubclass.Valid && strings.ToLower(charSubclass.String) == "fiend" {
			var darkOnesLuckUsed bool
			db.QueryRow("SELECT COALESCE(dark_ones_luck_used, false) FROM characters WHERE id = $1", charID).Scan(&darkOnesLuckUsed)

			darkOnesLuckInfo := map[string]interface{}{
				"available":       !darkOnesLuckUsed,
				"used_since_rest": darkOnesLuckUsed,
				"recovery":        "short or long rest",
				"trigger":         "When you make an ability check or saving throw",
				"effect":          "Add d10 to the roll (after rolling, before determining success)",
				"how_to_use":      "Ask GM to call POST /api/gm/dark-ones-luck after you see a roll result",
			}

			if !darkOnesLuckUsed {
				darkOnesLuckInfo["tip"] = "🔥 Dark One's Own Luck ready! When you fail an ability check or saving throw, you can add d10 to the roll!"
			}

			response["dark_ones_luck"] = darkOnesLuckInfo
		}
	}

	// Fiend Warlock - Fiendish Resilience (level 10+)
	if strings.ToLower(class) == "warlock" && level >= 10 {
		if charSubclass.Valid && strings.ToLower(charSubclass.String) == "fiend" {
			var fiendishRes sql.NullString
			db.QueryRow("SELECT fiendish_resilience FROM characters WHERE id = $1", charID).Scan(&fiendishRes)

			currentResistance := ""
			if fiendishRes.Valid {
				currentResistance = fiendishRes.String
			}

			fiendishResInfo := map[string]interface{}{
				"current_resistance": currentResistance,
				"has_resistance":     currentResistance != "",
				"can_change":         "After a short or long rest",
				"valid_types":        []string{"acid", "cold", "fire", "lightning", "poison", "thunder", "necrotic", "psychic", "bludgeoning", "piercing", "slashing"},
				"excluded_types":     []string{"radiant", "force"},
				"how_to_use":         "POST /api/characters/fiendish-resilience with character_id and damage_type",
			}

			if currentResistance != "" {
				fiendishResInfo["tip"] = fmt.Sprintf("🛡️ You have resistance to %s damage from your Fiendish Resilience!", currentResistance)
			} else {
				fiendishResInfo["tip"] = "⚠️ You haven't chosen a damage type for Fiendish Resilience yet! Use POST /api/characters/fiendish-resilience"
			}

			response["fiendish_resilience"] = fiendishResInfo

			// Hurl Through Hell (level 14+)
			if level >= 14 {
				var hurlUsed bool
				db.QueryRow("SELECT COALESCE(hurl_through_hell_used, false) FROM characters WHERE id = $1", charID).Scan(&hurlUsed)
				hurlInfo := map[string]interface{}{
					"available":   !hurlUsed,
					"used":        hurlUsed,
					"recovers_on": "long rest",
					"description": "When you hit a creature with an attack, transport them through the lower planes. They return at the end of your next turn. Non-fiends take 10d10 psychic damage.",
					"how_to_use":  "After hitting with an attack, ask GM to call POST /api/gm/hurl-through-hell",
				}
				if hurlUsed {
					hurlInfo["tip"] = "⚠️ You've already used Hurl Through Hell. It recovers on your next long rest."
				} else {
					hurlInfo["tip"] = "🔥💀 You can hurl a creature you hit through the nightmare planes! Non-fiends take 10d10 psychic damage."
				}
				response["hurl_through_hell"] = hurlInfo
			}
		}
	}

	// Eldritch Invocations for Warlocks (level 2+)
	if strings.ToLower(class) == "warlock" && level >= 2 {
		invocations := getCharacterInvocations(charID)
		maxInvocations := game.GetMaxInvocations(level)
		invocationSpellsUsed := getInvocationSpellsUsed(charID)

		if len(invocations) > 0 {
			invocationsInfo := invocationSummary(charID) // invocation_repeats_2024.go
			invocationsResponse := map[string]interface{}{
				"known":           invocationsInfo,
				"known_count":     len(invocations),
				"max_invocations": maxInvocations,
				"can_learn_more":  len(invocations) < maxInvocations,
				"tip":             "Agonizing Blast adds your Charisma modifier to its cantrip's damage, Eldritch Spear adds 30 feet of range per Warlock level, Repelling Blast pushes up to 10 feet on a hit: each copy works on the cantrip it names. 'at_will_spell' invocations let you cast a spell without spell slots.",
			}
			// Include once-per-rest usage info
			if len(invocationSpellsUsed) > 0 {
				invocationsResponse["spells_used_today"] = invocationSpellsUsed
			}
			// Include at-will spells from invocations
			atWillSpells := getCharacterAtWillSpells(charID)
			if len(atWillSpells) > 0 {
				invocationsResponse["at_will_spells"] = atWillSpells
				invocationsResponse["at_will_note"] = "These spells can be cast without using spell slots. For self-only spells, include 'on self' in description."
			}
			response["eldritch_invocations"] = invocationsResponse
		} else {
			response["invocations_prompt"] = fmt.Sprintf("You can learn %d Eldritch Invocations! GET /api/characters/invocations?character_id=%d", maxInvocations, charID)
		}
	}

	// Mystic Arcanum for Warlocks level 11+
	if strings.ToLower(class) == "warlock" && level >= 11 {
		var arcanumJSON, usedJSON []byte
		db.QueryRow("SELECT COALESCE(mystic_arcanum, '{}'), COALESCE(mystic_arcanum_used, '[]') FROM characters WHERE id = $1", charID).Scan(&arcanumJSON, &usedJSON)
		var arcanum map[string]string
		var usedLevels []int
		json.Unmarshal(arcanumJSON, &arcanum)
		json.Unmarshal(usedJSON, &usedLevels)

		availableLevels := getAvailableMysticArcanumLevels(level)
		arcanumInfo := []map[string]interface{}{}
		unchosen := []int{}
		anyAvailable := false

		for _, spellLvl := range availableLevels {
			spellSlug := arcanum[strconv.Itoa(spellLvl)]
			info := map[string]interface{}{"spell_level": spellLvl}

			if spellSlug != "" {
				var spellName string
				db.QueryRow("SELECT name FROM spells WHERE slug = $1", spellSlug).Scan(&spellName)
				info["spell"] = spellSlug
				info["spell_name"] = spellName
				// Check if used
				used := false
				for _, usedLvl := range usedLevels {
					if usedLvl == spellLvl {
						used = true
						break
					}
				}
				info["used"] = used
				if used {
					info["status"] = "Used"
				} else {
					info["status"] = "Available"
					anyAvailable = true
				}
			} else {
				info["spell"] = nil
				info["status"] = "Not chosen"
				unchosen = append(unchosen, spellLvl)
			}
			arcanumInfo = append(arcanumInfo, info)
		}

		mysticArcanumResponse := map[string]interface{}{
			"arcanum":     arcanumInfo,
			"how_to_cast": "Include 'arcanum' in cast description: 'cast [spell] using arcanum'",
		}
		if len(unchosen) > 0 {
			mysticArcanumResponse["unchosen_levels"] = unchosen
			mysticArcanumResponse["how_to_choose"] = fmt.Sprintf("POST /api/characters/mystic-arcanum with character_id=%d, spell_level, spell_slug", charID)
		}
		if anyAvailable {
			mysticArcanumResponse["tip"] = "✨ You have Mystic Arcanum spells ready! Cast them once per long rest without a slot."
		}
		response["mystic_arcanum"] = mysticArcanumResponse
	}

	// Warlock Eldritch Master (level 20)
	warlockLevelMyTurn := getWarlockLevel(charID)
	if warlockLevelMyTurn >= 20 {
		response["eldritch_master"] = eldritchMasterView(charID)
	}

	// Wizard Signature Spells (level 20)
	wizardLevelMyTurn := getWizardLevel(charID)
	if wizardLevelMyTurn >= 20 {
		var signatureSpellsJSON, signatureSpellsUsedJSON []byte
		db.QueryRow(`SELECT COALESCE(signature_spells, '[]'), COALESCE(signature_spells_used, '[]') FROM characters WHERE id = $1`, charID).Scan(&signatureSpellsJSON, &signatureSpellsUsedJSON)

		var signatureSpells []string
		var signatureSpellsUsed []string
		json.Unmarshal(signatureSpellsJSON, &signatureSpells)
		json.Unmarshal(signatureSpellsUsedJSON, &signatureSpellsUsed)

		spellInfo := []map[string]interface{}{}
		for _, spell := range signatureSpells {
			used := false
			for _, usedSpell := range signatureSpellsUsed {
				if usedSpell == spell {
					used = true
					break
				}
			}
			var spellName string
			db.QueryRow("SELECT name FROM spells WHERE slug = $1", spell).Scan(&spellName)
			if spellName == "" {
				spellName = spell
			}
			spellInfo = append(spellInfo, map[string]interface{}{
				"slug":      spell,
				"name":      spellName,
				"used":      used,
				"available": !used,
			})
		}

		freeCastsRemaining := 2 - len(signatureSpellsUsed)
		sigSpellsInfo := map[string]interface{}{
			"spells_chosen":        len(signatureSpells),
			"max_spells":           2,
			"needs_choice":         len(signatureSpells) < 2,
			"spells":               spellInfo,
			"free_casts_remaining": freeCastsRemaining,
			"recovers_on":          "long rest (free casts)",
			"description":          "Two 3rd-level wizard spells you have mastered. Always prepared (don't count against limit), and you can cast each once at 3rd level without expending a spell slot.",
			"how_to_cast":          "POST /api/characters/signature-spells with action='cast', spell='spell-slug'",
		}

		if len(signatureSpells) < 2 {
			sigSpellsInfo["tip"] = fmt.Sprintf("📜 You need to choose %d more signature spell(s)! Use POST /api/characters/signature-spells with action='choose'", 2-len(signatureSpells))
		} else if freeCastsRemaining > 0 {
			sigSpellsInfo["tip"] = fmt.Sprintf("📜 You have %d free signature spell cast(s) available!", freeCastsRemaining)
		} else {
			sigSpellsInfo["tip"] = "📜 You've used your free signature spell casts. They recover on long rest. You can still cast these spells using slots."
		}

		response["signature_spells"] = sigSpellsInfo
	}

	// Evocation Wizard Overchannel (level 14+)
	if wizardLevelMyTurn >= 14 {
		var wizSubclass sql.NullString
		db.QueryRow("SELECT subclass FROM characters WHERE id = $1", charID).Scan(&wizSubclass)
		if wizSubclass.Valid && wizSubclass.String == "evocation" {
			var overchannelUsed bool
			db.QueryRow("SELECT COALESCE(overchannel_used, false) FROM characters WHERE id = $1", charID).Scan(&overchannelUsed)

			overchannelInfo := map[string]interface{}{
				"available":       true,
				"used":            overchannelUsed,
				"first_use_free":  !overchannelUsed,
				"penalty_warning": "Subsequent uses deal 2d12 necrotic per spell level (cannot be reduced)",
				"valid_spells":    "Wizard spells of 1st through 5th level that deal damage",
				"how_to_use":      "Include 'overchannel' in cast description (e.g., 'overchannel fireball')",
				"recovers_on":     "long rest",
				"description":     "Deal maximum damage with a wizard spell. First use is free; subsequent uses inflict necrotic backlash.",
			}

			if !overchannelUsed {
				overchannelInfo["tip"] = "💥 Overchannel is ready! Your first use this rest deals max damage for free."
			} else {
				overchannelInfo["tip"] = "⚠️ You've used Overchannel. Further uses will deal necrotic damage to you (2d12 per spell level)."
			}

			response["overchannel"] = overchannelInfo
		}
	}

	// Fighter Indomitable (level 9+)
	if strings.ToLower(class) == "fighter" && level >= 9 {
		var indomitableUsed int
		db.QueryRow("SELECT COALESCE(indomitable_used, 0) FROM characters WHERE id = $1", charID).Scan(&indomitableUsed)

		maxUses := getIndomitableMaxUses(class, level)
		remaining := maxUses - indomitableUsed
		if remaining < 0 {
			remaining = 0
		}

		indomitableInfo := map[string]interface{}{
			"uses_remaining": remaining,
			"uses_max":       maxUses,
			"uses_spent":     indomitableUsed,
			"recovery":       "long rest",
			"description":    "When you fail a saving throw, you can reroll it. You must use the new roll.",
			"trigger":        "After failing any saving throw",
			"how_to_use":     "Ask GM to call POST /api/gm/indomitable with your character_id, ability, and dc",
		}

		if remaining > 0 {
			indomitableInfo["tip"] = fmt.Sprintf("⚔️ You have %d Indomitable use(s)! If you fail a save, you can reroll it.", remaining)
		} else {
			indomitableInfo["tip"] = "⚔️ Indomitable exhausted. Take a long rest to recover."
		}

		response["indomitable"] = indomitableInfo
	}

	// Rogue Stroke of Luck (level 20)
	rogueLevel := getRogueLevel(charID, class, level)
	if rogueLevel >= 20 {
		var strokeUsed bool
		db.QueryRow("SELECT COALESCE(stroke_of_luck_used, false) FROM characters WHERE id = $1", charID).Scan(&strokeUsed)

		strokeInfo := map[string]interface{}{
			"available":   !strokeUsed,
			"used":        strokeUsed,
			"recovery":    "short or long rest",
			"description": "If your attack misses, you can turn the miss into a hit. Alternatively, if you fail an ability check, you can treat the d20 roll as a 20.",
			"modes": []map[string]string{
				{"mode": "attack", "description": "Turn a missed attack into a hit"},
				{"mode": "ability_check", "description": "Treat a failed ability check d20 as a 20"},
			},
			"how_to_use": "Ask GM to call POST /api/gm/stroke-of-luck with your character_id and mode",
		}

		if !strokeUsed {
			strokeInfo["tip"] = "🎲 Stroke of Luck ready! If you miss an attack or fail an ability check, use it!"
		} else {
			strokeInfo["tip"] = "🎲 Stroke of Luck used. Take a short or long rest to recover."
		}

		response["stroke_of_luck"] = strokeInfo
	}

	if mapURL != "" {
		response["battle_map_url"] = mapURL
	} else if mapReason != "" {
		response["battle_map_unavailable"] = mapReason
	}
	if comps := companionsOf(charID); len(comps) > 0 {
		var list []map[string]interface{}
		for _, c := range comps {
			list = append(list, companionView(c))
		}
		response["companions"] = list
		response["companions_tip"] = "Your companions act on your turn: POST /api/action {\"companion_id\": N, \"action\": \"attack\", \"target_id\": ...} (or dodge, dash, disengage, help, hide, search, move); each acts once per round."
	}
	if companionTurn > 0 {
		response["companion_turn"] = companionTurn
		response["companion_turn_tip"] = "It's your familiar's turn: act with POST /api/action {\"companion_id\": " + fmt.Sprint(companionTurn) + ", ...}, then end the turn."
	}
	// the scene menu (scene_menu_2024.go): what fits now, everything else it can send with why it isn't first, and what a player does
	// outside a turn; the short view leaves out the long lists these replace
	facts := sceneFactsOf(inCombat, enemies, partyStatus, hp, maxHP, charStatus, beatApplies(lobbyID))
	response["now"], response["more"] = sceneMenu(charID, facts)
	hours := int64(0)
	if soon := longRestTooSoon(charID, 0); soon != nil {
		hours, _ = soon["story_hours_left"].(int64)
	}
	cl := strings.ToLower(class)
	response["elsewhere"] = elsewhere(elsewhereScene{CharID: charID, Lobby: lobbyID, Class: cl, Level: level, InFight: inCombat,
		Caster:   game.ClassTableInt(cl, level, "Prepared Spells") > 0 || game.ClassTableInt(cl, level, "Cantrips") > 0,
		Schedule: usesScheduleFor(charID), ShopOpen: shopsOpenFor(lobbyID) != nil, ChoicesOpen: len(characterToFinish(charID)) > 0,
		LeftBehind: leftBehindNote(charID) != nil, LongRestHours: hours})
	response["menu_note"] = "now: what fits this scene, with the request to send. more: everything else you can send, each with why it isn't first (nothing is hidden or refused). elsewhere: what you do outside a turn. Add ?view=short for these without the long actions, bonus_actions and free_actions lists."
	if r.URL.Query().Get("view") == "short" {
		if d := heartbeatLevelUp(charID); d != nil {
			response["level_up"] = d // the notices as a digest: the newest, the levels, each open choice with its exact request (heartbeat_digest_2024.go)
		}
		shortenMyTurn(response, charID, r.URL.Query().Get("tips") == "1") // the short view's other cuts (short_view_2024.go)
		if opts, ok := response["your_options"].(map[string]interface{}); ok {
			delete(opts, "actions")
			delete(opts, "bonus_actions")
			delete(opts, "free_actions")
		}
	}
	if body, err := marshalLeading(response, myTurnLeadKeys...); err == nil {
		w.Write(append(body, '\n'))
		return
	}
	json.NewEncoder(w).Encode(response)
}

// parseStorySoFar extracts story_so_far string from campaign document JSON
func parseStorySoFar(campaignDocRaw []byte) string {
	var doc map[string]interface{}
	if err := json.Unmarshal(campaignDocRaw, &doc); err != nil {
		return ""
	}
	if s, ok := doc["story_so_far"].(string); ok {
		return s
	}
	return ""
}

// getMovementSpeed returns base movement speed for a species
func getMovementSpeed(species string) int {
	speciesKey := strings.ToLower(strings.ReplaceAll(species, " ", "_"))
	speciesKey = strings.ReplaceAll(speciesKey, "-", "_")
	if r, ok := srdSpecies[speciesKey]; ok {
		return r.Speed
	}
	return 30 // default
}

// actionableCombatCharacterID returns the current player turn, or zero when
// the turn belongs to a monster or the stored turn order is unusable.
func actionableCombatCharacterID(turnOrderJSON []byte, turnIndex int) int {
	var turns []struct {
		ID        int  `json:"id"`
		IsMonster bool `json:"is_monster"`
	}
	if err := json.Unmarshal(turnOrderJSON, &turns); err != nil || turnIndex < 0 || turnIndex >= len(turns) {
		return 0
	}
	turn := turns[turnIndex]
	if turn.IsMonster || turn.ID <= 0 {
		return 0
	}
	return turn.ID
}

// partyHints: the tactical hints about the party, from the health owner's labels (the character's own and each ally's): a dead ally
// gets none, a dying or stable one says what helps (I-ALDRIC-1).
func partyHints(own string, party []map[string]interface{}) []string {
	hints := []string{}
	if own == game.HealthCritical {
		hints = append(hints, "You're critically wounded. Consider retreating, healing, or using Dodge action.")
	}
	down := game.AtZero(own)
	for _, ally := range party {
		label, _ := ally["status"].(string)
		switch label {
		case game.HealthCritical:
			hints = append(hints, fmt.Sprintf("%s is critically wounded and may need help.", ally["name"]))
		case game.HealthDying:
			hints = append(hints, fmt.Sprintf("%s is dying (0 Hit Points): any healing, a DC 10 Wisdom (Medicine) check or Spare the Dying keeps them alive.", ally["name"]))
		case game.HealthStable:
			hints = append(hints, fmt.Sprintf("%s is stable at 0 Hit Points: any healing wakes them.", ally["name"]))
		}
		down = down || game.AtZero(label)
	}
	if len(hints) == 0 && !down {
		hints = append(hints, "The party is in good shape. Press the attack or explore.")
	}
	return hints
}

// zeroHPHint: the character's own hint at 0 Hit Points or dead (I-ALDRIC-5): only a dying character makes Death Saving Throws (in a
// fight the server rolls it at the start of the turn); a stable one makes none.
func zeroHPHint(label string, inFight bool) string {
	switch label {
	case game.HealthDead:
		return "Your character has died and cannot take combat actions or death saves."
	case game.HealthStable:
		return "You are stable at 0 Hit Points: you make no Death Saving Throws and have nothing to do. Any healing wakes you, or you regain 1 Hit Point after 1d4 hours."
	case game.HealthDying:
		if inFight {
			return "You are dying: the server rolls your Death Saving Throw at the start of your turn (3 successes: stable; 3 failures: death). Allies can heal or stabilize you."
		}
		return `You are dying: make your Death Saving Throw with {"action": "death_save"} (3 successes: stable; 3 failures: death). Allies can heal or stabilize you.`
	}
	return ""
}
