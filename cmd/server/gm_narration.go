package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// handleGMStatus godoc
// @Summary Get GM status and guidance
// @Description Returns everything the GM needs to know: what happened, who's waiting, what to do next, monster tactics.
// @Tags GM
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "GM status with guidance"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM of any active campaign"
// @Router /gm/status [get]
func handleGMStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Find campaign where this agent is the DM
	// Support optional ?campaign_id= param for GMs running multiple campaigns
	var campaignID int
	var campaignName, campaignStatus string
	var campaignSetting sql.NullString
	var campaignDocRaw []byte
	requestedCampaignID := 0
	if cidStr := r.URL.Query().Get("campaign_id"); cidStr != "" {
		requestedCampaignID, _ = strconv.Atoi(cidStr)
	}
	if requestedCampaignID > 0 {
		err = db.QueryRow(`
			SELECT id, name, status, COALESCE(setting, ''), COALESCE(campaign_document, '{}')
			FROM lobbies
			WHERE dm_id = $1 AND status = 'active' AND id = $2
			LIMIT 1
		`, agentID, requestedCampaignID).Scan(&campaignID, &campaignName, &campaignStatus, &campaignSetting, &campaignDocRaw)
	} else {
		err = db.QueryRow(`
			SELECT id, name, status, COALESCE(setting, ''), COALESCE(campaign_document, '{}')
			FROM lobbies
			WHERE dm_id = $1 AND status = 'active'
			ORDER BY id DESC
			LIMIT 1
		`, agentID).Scan(&campaignID, &campaignName, &campaignStatus, &campaignSetting, &campaignDocRaw)
	}

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"needs_attention": false,
			"error":           "not_gm",
			"message":         "You are not the GM of any active campaign.",
			"how_to_create": map[string]interface{}{
				"endpoint": "POST /api/campaigns",
				"example": map[string]interface{}{
					"name":      "My Adventure",
					"setting":   "A dark forest...",
					"min_level": 1,
					"max_level": 5,
				},
			},
		})
		return
	}

	// Get combat state
	var combatRound, turnIndex int
	var turnOrderJSON []byte
	var combatActive bool
	var turnStartedAt sql.NullTime
	inCombat := false

	err = db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active, COALESCE(turn_started_at, NOW())
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&combatRound, &turnIndex, &turnOrderJSON, &combatActive, &turnStartedAt)

	if err == nil && combatActive {
		inCombat = true
	}

	gameState := "exploration"
	if inCombat {
		gameState = "combat"
	}

	// Lazy inactivity check: mark players inactive if no activity in 4+ hours
	// Also remove inactive players from combat turn order
	inactiveThreshold := 4 * time.Hour
	var inactiveCharIDs []int
	inactiveRows, _ := db.Query(`
		SELECT c.id, c.name FROM characters c
		WHERE c.lobby_id = $1
		AND c.status != 'inactive'
		AND NOT EXISTS (
			SELECT 1 FROM actions a 
			WHERE a.character_id = c.id 
			AND a.created_at > NOW() - INTERVAL '4 hours'
		)
	`, campaignID)
	if inactiveRows != nil {
		for inactiveRows.Next() {
			var charID int
			var charName string
			inactiveRows.Scan(&charID, &charName)
			inactiveCharIDs = append(inactiveCharIDs, charID)
			log.Printf("Marking character %s (ID %d) as inactive (no activity in %v)", charName, charID, inactiveThreshold)
		}
		inactiveRows.Close()

		// Mark them inactive in the database
		for _, charID := range inactiveCharIDs {
			db.Exec(`UPDATE characters SET status = 'inactive' WHERE id = $1`, charID)
		}

		// Remove inactive players from combat turn order
		if inCombat && len(inactiveCharIDs) > 0 {
			type TurnEntry struct {
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
			var turnOrder []TurnEntry
			json.Unmarshal(turnOrderJSON, &turnOrder)

			// Filter out inactive characters
			newTurnOrder := []TurnEntry{}
			for _, entry := range turnOrder {
				isInactive := false
				for _, inactiveID := range inactiveCharIDs {
					if entry.ID == inactiveID {
						isInactive = true
						break
					}
				}
				if !isInactive {
					newTurnOrder = append(newTurnOrder, entry)
				}
			}

			// Update turn order if changed
			if len(newTurnOrder) != len(turnOrder) {
				newOrderJSON, _ := json.Marshal(newTurnOrder)
				// Adjust turn index if needed
				newIndex := turnIndex
				if newIndex >= len(newTurnOrder) {
					newIndex = 0
				}
				db.Exec(`UPDATE combat_state SET turn_order = $1, current_turn_index = $2 WHERE lobby_id = $3`,
					newOrderJSON, newIndex, campaignID)
				turnOrderJSON = newOrderJSON
				turnIndex = newIndex
			}
		}
	}

	// Get the last action
	var lastActionID, lastCharID int
	var lastActionType, lastDesc, lastResult string
	var lastActionTime time.Time
	var lastCharName string
	err = db.QueryRow(`
		SELECT a.id, a.character_id, COALESCE(c.name, 'Unknown'), a.action_type, a.description, a.result, a.created_at
		FROM actions a
		LEFT JOIN characters c ON a.character_id = c.id
		WHERE a.lobby_id = $1
		  AND a.action_type NOT IN ('poll', 'joined', 'following')
		ORDER BY a.created_at DESC
		LIMIT 1
	`, campaignID).Scan(&lastActionID, &lastCharID, &lastCharName, &lastActionType, &lastDesc, &lastResult, &lastActionTime)

	var lastAction map[string]interface{}
	timeSinceAction := ""
	if err == nil {
		duration := time.Since(lastActionTime)
		if duration < time.Minute {
			timeSinceAction = "just now"
		} else if duration < time.Hour {
			timeSinceAction = fmt.Sprintf("%d minutes ago", int(duration.Minutes()))
		} else {
			timeSinceAction = fmt.Sprintf("%d hours ago", int(duration.Hours()))
		}

		lastAction = map[string]interface{}{
			"character": lastCharName,
			"action":    fmt.Sprintf("%s: %s", lastActionType, lastDesc),
			"result":    lastResult,
			"timestamp": timeSinceAction,
		}
	}

	// Get party status with last action time per character
	rows, _ := db.Query(`
	SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, c.ac,
			COALESCE(c.conditions, '[]'), COALESCE(c.concentrating_on, ''), COALESCE(c.is_stable, false), COALESCE(c.is_dead, false),
			(SELECT MAX(created_at) FROM actions WHERE character_id = c.id AND action_type NOT IN ('poll', 'joined', 'following')) as last_action_at,
			(SELECT MAX(created_at) FROM actions WHERE character_id = c.id AND action_type = 'following') as last_following_at
		FROM characters c
		WHERE c.lobby_id = $1
	`, campaignID)
	defer rows.Close()

	partyStatus := []map[string]interface{}{}
	playerActivity := []map[string]interface{}{}
	gmTasks := []string{}
	var waitingFor *string

	// Track who needs auto-advance
	mustAdvance := false
	var mustAdvanceReason string
	var mustAdvancePlayers []string
	currentCombatCharacterID := 0
	if inCombat {
		currentCombatCharacterID = actionableCombatCharacterID(turnOrderJSON, turnIndex)
	}

	for rows.Next() {
		var id, level, hp, maxHP, ac int
		var name, class, species, concentrating string
		var isStable, isDead bool
		var conditionsJSON []byte
		var lastActionAt, lastFollowingAt sql.NullTime
		rows.Scan(&id, &name, &class, &species, &level, &hp, &maxHP, &ac, &conditionsJSON, &concentrating, &isStable, &isDead, &lastActionAt, &lastFollowingAt)

		conditions := responseConditions(conditionsJSON)

		status := characterHPStatus(hp, maxHP, isStable, isDead)

		charInfo := map[string]interface{}{
			"id":     id,
			"name":   name,
			"class":  class,
			"hp":     fmt.Sprintf("%d/%d", hp, maxHP),
			"ac":     ac,
			"status": status,
		}
		if len(conditions) > 0 {
			charInfo["conditions"] = conditions
		}
		if concentrating != "" {
			charInfo["concentrating_on"] = concentrating
		}
		partyStatus = append(partyStatus, charInfo)

		// Track player activity for auto-advance logic (add countdowns)
		activityInfo := map[string]interface{}{
			"name": name,
			"id":   id,
		}
		if isFollowingParty(lastActionAt, lastFollowingAt) {
			if lastActionAt.Valid {
				activityInfo["last_action_at"] = lastActionAt.Time.Format(time.RFC3339)
			} else {
				activityInfo["last_action_at"] = nil
			}
			activityInfo["following_since"] = lastFollowingAt.Time.Format(time.RFC3339)
			activityInfo["inactive_hours"] = 0
			activityInfo["inactive_status"] = "following"
			activityInfo["countdowns"] = map[string]string{
				"combat_skip_in":      "not needed (following party)",
				"exploration_skip_in": "not needed (following party)",
				"abandon_in":          "not needed (following party)",
			}
		} else if lastActionAt.Valid {
			inactiveDuration := time.Since(lastActionAt.Time)
			inactiveHours := inactiveDuration.Hours()
			activityInfo["last_action_at"] = lastActionAt.Time.Format(time.RFC3339)
			activityInfo["inactive_hours"] = int(inactiveHours)

			// Add countdowns to each threshold
			combatSkipThreshold := 4 * time.Hour
			explorationSkipThreshold := 12 * time.Hour
			abandonThreshold := 24 * time.Hour

			countdowns := map[string]interface{}{}

			if inactiveDuration < combatSkipThreshold {
				remaining := combatSkipThreshold - inactiveDuration
				countdowns["combat_skip_in"] = formatDuration(remaining)
				activityInfo["inactive_status"] = "active"
			} else if inactiveDuration < explorationSkipThreshold {
				remaining := explorationSkipThreshold - inactiveDuration
				countdowns["exploration_skip_in"] = formatDuration(remaining)
				countdowns["combat_skip_in"] = "overdue"
				activityInfo["inactive_status"] = "overdue"
			} else if inactiveDuration < abandonThreshold {
				remaining := abandonThreshold - inactiveDuration
				countdowns["abandon_in"] = formatDuration(remaining)
				countdowns["exploration_skip_in"] = "overdue"
				countdowns["combat_skip_in"] = "overdue"
				activityInfo["inactive_status"] = "stale"
			} else {
				countdowns["abandon_in"] = "overdue"
				countdowns["exploration_skip_in"] = "overdue"
				countdowns["combat_skip_in"] = "overdue"
				activityInfo["inactive_status"] = "abandoned"
				// The GM combat skip endpoint requires four hours on this exact
				// turn. Old player activity alone cannot make a fresh turn skippable.
				if shouldAdvanceAbandonedPlayer(inCombat, currentCombatCharacterID, id, turnStartedAt, time.Now()) {
					mustAdvancePlayers = append(mustAdvancePlayers, name)
				}
			}
			if inCombat {
				// Skip eligibility is measured from the current turn, not the
				// player's last action on a previous turn.
				if currentCombatCharacterID != id {
					countdowns["combat_skip_in"] = "not current turn"
				} else if turnStartedAt.Valid {
					remaining := 4*time.Hour - time.Since(turnStartedAt.Time)
					if remaining > 0 {
						countdowns["combat_skip_in"] = formatDuration(remaining)
					} else {
						countdowns["combat_skip_in"] = "overdue"
					}
				} else {
					countdowns["combat_skip_in"] = "unknown (turn clock unavailable)"
				}
			}
			activityInfo["countdowns"] = countdowns
		} else {
			activityInfo["last_action_at"] = nil
			activityInfo["inactive_hours"] = -1
			activityInfo["inactive_status"] = "never_acted"
			activityInfo["countdowns"] = map[string]string{
				"combat_skip_in":      "unknown (never acted)",
				"exploration_skip_in": "unknown (never acted)",
				"abandon_in":          "unknown (never acted)",
			}
		}
		playerActivity = append(playerActivity, activityInfo)
	}

	// Only request an advance when an abandoned player has an actionable turn.
	if len(mustAdvancePlayers) > 0 {
		mustAdvance = true
		mustAdvanceReason = fmt.Sprintf("MUST ADVANCE: %v inactive 24h+. Story cannot wait. Skip or default their actions and move forward.", mustAdvancePlayers)
	}

	// Determine what GM needs to do
	needsAttention := false
	whatToDoNext := map[string]interface{}{}

	if inCombat {
		// Parse turn order
		type InitEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Initiative int    `json:"initiative"`
			IsMonster  bool   `json:"is_monster"`
		}
		var entries []InitEntry
		json.Unmarshal(turnOrderJSON, &entries)

		currentTurnName := ""
		isMonsterTurn := false
		if len(entries) > turnIndex {
			currentTurnName = entries[turnIndex].Name
			isMonsterTurn = entries[turnIndex].IsMonster
		}

		if isMonsterTurn {
			needsAttention = true
			whatToDoNext = map[string]interface{}{
				"instruction":          fmt.Sprintf("It's %s's turn. Run the monster's action.", currentTurnName),
				"narrative_suggestion": "The enemy doesn't hesitate. Describe the attack with visceral urgency—the threat is immediate and the party feels the pressure.",
				"next_in_initiative":   currentTurnName,
				"time_pressure_tips": []string{
					"Monsters act decisively; the clock is ticking",
					"Describe what's at stake if the party doesn't respond",
					"Environmental hazards worsen each round",
				},
			}
		} else if lastAction != nil && strings.ToLower(lastActionType) != "narration" {
			needsAttention = true
			whatToDoNext = map[string]interface{}{
				"instruction":          fmt.Sprintf("Narrate %s's action, then check if it's a monster's turn.", lastCharName),
				"narrative_suggestion": "Show consequences immediately. The world reacts in real time—what changes? What new threat emerges?",
				"next_in_initiative":   currentTurnName,
				"time_pressure_tips": []string{
					"Every action costs something (time, resources, attention)",
					"Success should reveal new complications",
					"Failure creates urgency—something worse is coming",
				},
			}
			waitingFor = &currentTurnName
		}
	} else {
		// Exploration mode
		if lastAction != nil && strings.ToLower(lastActionType) != "narration" {
			needsAttention = true
			whatToDoNext = map[string]interface{}{
				"instruction":          fmt.Sprintf("Narrate the result of %s's action.", lastCharName),
				"narrative_suggestion": "Time passes as they act. What complication builds in the background? Something is always counting down.",
				"time_pressure_tips": []string{
					"Hint at approaching danger or closing windows",
					"Resources deplete; torches dim, air runs thin",
					"NPCs and threats have their own timelines",
					"Each choice forecloses other options",
				},
			}
		} else {
			whatToDoNext = map[string]interface{}{
				"instruction":          "Set the scene. Describe what the party sees.",
				"narrative_suggestion": "Establish stakes immediately. Something is urgent: a pursuit, a timer, a deteriorating situation. Give them a reason to act NOW.",
				"time_pressure_tips": []string{
					"Start with a hook that demands response",
					"Mention something time-sensitive in the environment",
					"The status quo is unstable—standing still has cost",
					"Hint at what will happen if they delay",
				},
			}
		}

		// Check for players inactive 12h+ in exploration mode
		var explorationSkipPlayers []string
		for _, activityMap := range playerActivity {
			countdowns, ok := activityMap["countdowns"].(map[string]interface{})
			if !ok {
				continue
			}
			explorationSkip, ok := countdowns["exploration_skip_in"].(string)
			if ok && explorationSkip == "overdue" {
				if name, ok := activityMap["name"].(string); ok {
					explorationSkipPlayers = append(explorationSkipPlayers, name)
				}
			}
		}

		if len(explorationSkipPlayers) > 0 {
			needsAttention = true
			// Note: gmTasks append happens later after gmTasks is defined
			whatToDoNext["exploration_skip_required"] = true
			whatToDoNext["exploration_skip_players"] = explorationSkipPlayers
			whatToDoNext["exploration_skip_instruction"] = fmt.Sprintf("Skip inactive players via POST /api/campaigns/%d/exploration/skip with {\"character_id\": ID}. They will be marked as following the party.", campaignID)
		}
	}

	// Build monster guidance (if in combat and monsters present)
	monsterGuidance := map[string]interface{}{}
	if inCombat {
		type InitEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Initiative int    `json:"initiative"`
			IsMonster  bool   `json:"is_monster"`
			MonsterKey string `json:"monster_key"`
			HP         int    `json:"hp"`
			MaxHP      int    `json:"max_hp"`
		}
		var entries []InitEntry
		json.Unmarshal(turnOrderJSON, &entries)

		for _, e := range entries {
			if e.IsMonster {
				guidance := map[string]interface{}{
					"hp":           fmt.Sprintf("%d/%d", e.HP, e.MaxHP),
					"combatant_id": e.ID, // Include ID for use with legendary resistance/action endpoints
				}

				// Legendary Actions, Legendary Resistance, lair, auras (monster_features_2024.go)
				if f := monsterFeaturesInfo(campaignID, e.ID); f != nil {
					guidance["features"] = f
				}

				// Look up monster in SRD for tactics
				if e.MonsterKey != "" {
					var mType string
					var mAC, mHP int
					var actionsJSON []byte
					var dmgResistances, dmgImmunities, dmgVulnerabilities, condImmunities string
					err := db.QueryRow(`
						SELECT type, ac, hp, actions, 
							COALESCE(damage_resistances, ''), 
							COALESCE(damage_immunities, ''), 
							COALESCE(damage_vulnerabilities, ''),
							COALESCE(condition_immunities, '')
						FROM monsters WHERE slug = $1
					`, e.MonsterKey).Scan(&mType, &mAC, &mHP, &actionsJSON,
						&dmgResistances, &dmgImmunities, &dmgVulnerabilities, &condImmunities)

					if err == nil {
						var actions []map[string]interface{}
						json.Unmarshal(actionsJSON, &actions)

						actionNames := []string{}
						for _, a := range actions {
							if name, ok := a["name"].(string); ok {
								actionNames = append(actionNames, name)
							}
						}

						guidance["type"] = mType
						guidance["ac"] = mAC
						guidance["abilities"] = actionNames
						guidance["behavior"] = getMonsterBehavior(mType)

						// Damage resistances/immunities/vulnerabilities
						if dmgResistances != "" {
							guidance["damage_resistances"] = dmgResistances
						}
						if dmgImmunities != "" {
							guidance["damage_immunities"] = dmgImmunities
						}
						if dmgVulnerabilities != "" {
							guidance["damage_vulnerabilities"] = dmgVulnerabilities
						}
						if condImmunities != "" {
							guidance["condition_immunities"] = condImmunities
						}

						// Tactical suggestions based on HP
						if e.HP <= e.MaxHP/4 {
							guidance["tactical_options"] = []string{
								"Flee if intelligent",
								"Fight desperately",
								"Surrender if capable of speech",
							}
						} else {
							guidance["tactical_options"] = []string{
								"Attack the nearest threat",
								"Focus on the spellcaster",
								"Use special abilities",
							}
						}
					}
				}
				monsterGuidance[e.Name] = guidance
			}
		}
	}

	// GM tasks / maintenance reminders

	// DRIFT DETECTION: Check for drift_flag observations
	driftAlerts := []map[string]interface{}{}
	driftRows, _ := db.Query(`
		SELECT o.id, c1.name as observer, c2.name as target, o.content, o.created_at
		FROM observations o
		JOIN characters c1 ON o.observer_id = c1.id
		LEFT JOIN characters c2 ON o.target_id = c2.id
		WHERE o.lobby_id = $1 AND o.observation_type = 'drift_flag'
		AND o.created_at > NOW() - INTERVAL '7 days'
		ORDER BY o.created_at DESC
		LIMIT 10
	`, campaignID)
	if driftRows != nil {
		defer driftRows.Close()
		for driftRows.Next() {
			var obsID int
			var observer, content string
			var target sql.NullString
			var createdAt time.Time
			driftRows.Scan(&obsID, &observer, &target, &content, &createdAt)
			alert := map[string]interface{}{
				"id":       obsID,
				"observer": observer,
				"content":  content,
				"time":     createdAt.Format(time.RFC3339),
			}
			if target.Valid {
				alert["about"] = target.String
			}
			driftAlerts = append(driftAlerts, alert)
		}
	}
	if len(driftAlerts) > 0 {
		gmTasks = append(gmTasks, fmt.Sprintf("⚠️ %d drift flag(s) detected! Review player observations for potential out-of-character or disruptive behavior.", len(driftAlerts)))
	}

	// Add exploration skip task if players are inactive 12h+
	if whatToDoNext["exploration_skip_required"] == true {
		if players, ok := whatToDoNext["exploration_skip_players"].([]string); ok {
			gmTasks = append(gmTasks, fmt.Sprintf("⚠️ Exploration skip required: %v inactive 12h+. POST /api/campaigns/%d/exploration/skip with character_id.", players, campaignID))
		}
	}

	// Check story_so_far freshness — this is the primary long-term memory for stateless players
	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Query latest player action time
	latestPlayerAction := latestMeaningfulPlayerActionTime(campaignID)

	if _, hasStory := campaignDoc["story_so_far"]; !hasStory {
		gmTasks = append(gmTasks, fmt.Sprintf("🚨 URGENT: You MUST create a story_so_far summary. Players are STATELESS — they only know what you tell them. PUT /api/campaigns/%d/story with a <=500 word summary of everything that has happened. This is the MOST important thing you can do right now.", campaignID))
	} else {
		// Check if story is stale (updated_at < latest player action)
		storyStale := false
		if updatedAtStr, ok := campaignDoc["story_so_far_updated_at"].(string); ok {
			updatedAt, err := time.Parse(time.RFC3339, updatedAtStr)
			if err == nil && latestPlayerAction.Valid && latestPlayerAction.Time.After(updatedAt) {
				storyStale = true
			}
		} else if latestPlayerAction.Valid {
			// No updated_at recorded but players have acted — treat as stale
			storyStale = true
		}

		if storyStale {
			gmTasks = append(gmTasks, fmt.Sprintf("🚨 URGENT: Players have acted since your last story_so_far update. Update it NOW via PUT /api/campaigns/%d/story (<=500 words). This is how stateless players know what happened. Read the current story_so_far, incorporate recent events, and compact it.", campaignID))
		}
	}

	// Count observations that could be promoted
	var unpromotedCount int
	db.QueryRow(`
		SELECT COUNT(*) FROM observations 
		WHERE lobby_id = $1 AND (promoted = false OR promoted IS NULL)
	`, campaignID).Scan(&unpromotedCount)
	if unpromotedCount > 5 {
		gmTasks = append(gmTasks, fmt.Sprintf("%d observations pending review - consider promoting good ones to the campaign document", unpromotedCount))
	}

	// Check story deadlines (Phase 9 Autonomous GM)
	var storyDeadlines []map[string]interface{}
	var overdueDeadlines []map[string]interface{}
	deadlineRows, _ := db.Query(`
		SELECT id, description, deadline_at, auto_advance_text, triggered
		FROM story_deadlines
		WHERE lobby_id = $1 AND triggered = false
		ORDER BY deadline_at ASC
	`, campaignID)
	if deadlineRows != nil {
		for deadlineRows.Next() {
			var id int
			var description string
			var deadlineAt time.Time
			var autoAdvanceText sql.NullString
			var triggered bool
			deadlineRows.Scan(&id, &description, &deadlineAt, &autoAdvanceText, &triggered)

			remaining := time.Until(deadlineAt)
			deadline := map[string]interface{}{
				"id":          id,
				"description": description,
				"deadline_at": deadlineAt.Format(time.RFC3339),
			}
			if autoAdvanceText.Valid {
				deadline["auto_advance_text"] = autoAdvanceText.String
			}

			if remaining <= 0 {
				// Deadline is overdue - needs attention!
				deadline["overdue_by"] = (-remaining).Round(time.Minute).String()
				deadline["status"] = "overdue"
				deadline["action_required"] = fmt.Sprintf("POST /api/gm/deadline/%d with action:trigger to auto-advance, or action:cancel to dismiss", id)
				overdueDeadlines = append(overdueDeadlines, deadline)
				gmTasks = append(gmTasks, fmt.Sprintf("🚨 OVERDUE DEADLINE: '%s' was due %s ago! Trigger or cancel it.", description, (-remaining).Round(time.Minute).String()))
				needsAttention = true
			} else {
				deadline["time_remaining"] = remaining.Round(time.Minute).String()
				deadline["status"] = "pending"
				// Warn if deadline is approaching (< 1 hour)
				if remaining < time.Hour {
					deadline["warning"] = "approaching"
					gmTasks = append(gmTasks, fmt.Sprintf("⏰ Deadline approaching in %s: '%s'", remaining.Round(time.Minute).String(), description))
				}
			}

			storyDeadlines = append(storyDeadlines, deadline)
		}
		deadlineRows.Close()
	}

	// Build response
	response := map[string]interface{}{
		"needs_attention": needsAttention,
		"game_state":      gameState,
		"campaign_id":     campaignID,
		"campaign_name":   campaignName,
		"campaign": map[string]interface{}{
			"id":      campaignID,
			"name":    campaignName,
			"status":  campaignStatus,
			"setting": campaignSetting.String,
		},
		"party_status":    partyStatus,
		"player_activity": playerActivity,
		"what_to_do_next": whatToDoNext,
	}

	// Add must_advance flag (autonomous GM)
	if mustAdvance {
		response["must_advance"] = true
		response["must_advance_reason"] = mustAdvanceReason
		needsAttention = true
		gmTasks = append(gmTasks, "🚨 "+mustAdvanceReason)
	}

	// Add exploration_skip_required to top-level response
	if whatToDoNext["exploration_skip_required"] == true {
		response["exploration_skip_required"] = true
		response["exploration_skip_players"] = whatToDoNext["exploration_skip_players"]
		response["exploration_skip_endpoint"] = fmt.Sprintf("POST /api/campaigns/%d/exploration/skip", campaignID)
	}

	if waitingFor != nil {
		response["waiting_for"] = *waitingFor
	}

	if lastAction != nil {
		response["last_action"] = lastAction
	}

	if len(monsterGuidance) > 0 {
		response["monster_guidance"] = monsterGuidance
	}

	if inCombat { // Legendary Actions the GM may take now, after the turn that ended last (monster_features_2024.go)
		if opts, task := openLegendaryOptions(campaignID); len(opts) > 0 {
			response["legendary_options"] = opts
			gmTasks = append(gmTasks, task)
		}
	}

	if len(gmTasks) > 0 {
		response["gm_tasks"] = gmTasks
	}

	// Add drift detection alerts if any
	if len(driftAlerts) > 0 {
		response["drift_alerts"] = driftAlerts
		needsAttention = true // Drift flags need GM attention
	}

	// Add story deadlines (Phase 9 Autonomous GM)
	if len(storyDeadlines) > 0 {
		response["story_deadlines"] = storyDeadlines
	}
	if len(overdueDeadlines) > 0 {
		response["overdue_deadlines"] = overdueDeadlines
		needsAttention = true // Overdue deadlines need immediate attention
	}

	for _, task := range gmTasks {
		if strings.Contains(task, "🚨") || strings.Contains(task, "⚠️") {
			needsAttention = true
			break
		}
	}
	response["needs_attention"] = needsAttention

	// Add combat info if in combat
	if inCombat {
		type InitEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Initiative int    `json:"initiative"`
			IsMonster  bool   `json:"is_monster"`
		}
		var entries []InitEntry
		json.Unmarshal(turnOrderJSON, &entries)

		combatInfo := map[string]interface{}{
			"round":              combatRound,
			"turn_order":         entries,
			"current_turn_index": turnIndex,
			"combatants":         gmCombatants(turnOrderJSON, campaignID), // GM only: AC, HP and stat blocks
		}

		// Turn timeout tracking
		if turnStartedAt.Valid {
			elapsed := time.Since(turnStartedAt.Time)
			elapsedMinutes := int(elapsed.Minutes())
			combatInfo["turn_elapsed_minutes"] = elapsedMinutes

			// Nudge recommended at 2 hours (only for players, not monsters)
			if elapsedMinutes >= 120 && elapsedMinutes < 240 {
				combatInfo["nudge_recommended"] = true
				combatInfo["turn_status"] = "overdue"
				if len(entries) > turnIndex && !entries[turnIndex].IsMonster {
					gmTasks = append(gmTasks, fmt.Sprintf("⏰ %s has been on this turn for %d hours. Consider sending a nudge (POST /api/gm/nudge)", entries[turnIndex].Name, elapsedMinutes/60))
				}
			}
			// Skip REQUIRED at 4 hours (autonomous GM phase 2)
			// This is NOT optional - the server expects the GM to skip immediately
			// Cron job will auto-skip 30 min after this flag appears
			if elapsedMinutes >= 240 {
				combatInfo["skip_required"] = true
				combatInfo["turn_status"] = "timeout"
				if len(entries) > turnIndex && !entries[turnIndex].IsMonster {
					playerName := entries[turnIndex].Name
					response["skip_required"] = true
					response["skip_required_player"] = playerName

					// Calculate countdown to auto-skip (cron runs 30 min after 4h threshold)
					autoSkipTime := turnStartedAt.Time.Add(4*time.Hour + 30*time.Minute)
					remaining := time.Until(autoSkipTime)
					if remaining > 0 {
						combatInfo["auto_skip_countdown"] = formatDuration(remaining)
						response["auto_skip_countdown"] = formatDuration(remaining)
					} else {
						combatInfo["auto_skip_countdown"] = "imminent"
						response["auto_skip_countdown"] = "imminent"
					}

					// Urgent task - this is NOT a suggestion
					gmTasks = append(gmTasks, fmt.Sprintf("⚠️ %s turn timeout (4h+). SKIP NOW via POST /api/campaigns/%d/combat/skip. Auto-skip in %s.", playerName, campaignID, combatInfo["auto_skip_countdown"]))

					// Override what_to_do_next with skip instruction
					whatToDoNext = map[string]interface{}{
						"instruction":          fmt.Sprintf("SKIP %s's turn immediately. They have exceeded the 4-hour timeout.", playerName),
						"action_required":      "skip_turn",
						"endpoint":             fmt.Sprintf("POST /api/campaigns/%d/combat/skip", campaignID),
						"narrative_suggestion": fmt.Sprintf("%s hesitates, taking the Dodge action defensively.", playerName),
						"urgency":              "critical",
						"auto_skip_in":         combatInfo["auto_skip_countdown"],
					}
					needsAttention = true
				}
			}
		}

		response["combat"] = combatInfo
	}
	if inCombat && allPartyUnableToAct(partyStatus) {
		// A timed skip cannot revive an unconscious party. Give the GM an
		// explicit, supported way to adjudicate the encounter instead of
		// repeatedly advancing unactable turns.
		delete(response, "skip_required")
		delete(response, "skip_required_player")
		delete(response, "auto_skip_countdown")
		if combatInfo, ok := response["combat"].(map[string]interface{}); ok {
			delete(combatInfo, "skip_required")
			delete(combatInfo, "nudge_recommended")
			delete(combatInfo, "auto_skip_countdown")
			combatInfo["party_defeated"] = true
		}
		whatToDoNext = map[string]interface{}{
			"action_required": "resolve_incapacitated_party",
			"instruction":     "No party member can act. Adjudicate defeat, capture, or a supported rescue; then end combat rather than skipping unconscious turns. Do not restore HP without a game-world cause.",
			"endpoint":        fmt.Sprintf("POST /api/campaigns/%d/combat/end", campaignID),
			"urgency":         "critical",
		}
		gmTasks = []string{"⚠️ All party members are incapacitated. Resolve the encounter and end combat; do not keep skipping turns."}
		needsAttention = true
	}

	// Add how_to_narrate instructions
	response["how_to_narrate"] = map[string]interface{}{
		"endpoint": "POST /api/gm/narrate",
		"headers":  "Authorization: Basic <base64(email:password)>",
		"example": map[string]interface{}{
			"narration": "The goblin's blade scrapes against stone as it lunges forward...",
			"monster_action": map[string]interface{}{
				"monster":     "goblin_a",
				"action":      "attack",
				"target":      "Thorgrim",
				"description": "The goblin lunges at Thorgrim with its rusty scimitar",
			},
		},
	}

	// Battle recommendation: check if we should nudge GM toward combat
	// Conditions: 3+ RECENTLY active players (last 4 hours), 5+ actions since last combat
	if !inCombat {
		// Count distinct active players (those with actions in last 4 hours)
		var activePlayerCount int
		db.QueryRow(`
			SELECT COUNT(DISTINCT c.agent_id) 
			FROM actions a 
			JOIN characters c ON a.character_id = c.id 
			WHERE a.lobby_id = $1 
			AND a.created_at > NOW() - INTERVAL '4 hours'
			AND a.action_type NOT IN ('poll', 'joined', 'following')
		`, campaignID).Scan(&activePlayerCount)

		// Count players active in last 12 hours (for dormancy check)
		var recentPlayerCount int
		db.QueryRow(`
			SELECT COUNT(DISTINCT c.agent_id) 
			FROM actions a 
			JOIN characters c ON a.character_id = c.id 
			WHERE a.lobby_id = $1 
			AND a.created_at > NOW() - INTERVAL '12 hours'
			AND a.action_type NOT IN ('poll', 'joined', 'following')
		`, campaignID).Scan(&recentPlayerCount)

		// Count actions since last combat ended (or since campaign start)
		var actionsSinceCombat int
		db.QueryRow(`
			SELECT COUNT(*) FROM actions 
			WHERE lobby_id = $1 
			AND action_type NOT IN ('poll', 'joined', 'following', 'narration')
			AND created_at > COALESCE(
				(SELECT MAX(created_at) FROM actions WHERE lobby_id = $1 AND action_type = 'combat_end'),
				(SELECT created_at FROM lobbies WHERE id = $1)
			)
		`, campaignID).Scan(&actionsSinceCombat)

		// Recommend combat only if 3+ players active in last 4 hours AND 5+ actions
		if activePlayerCount >= 3 && actionsSinceCombat >= 5 {
			response["battle_recommended"] = true
			response["battle_guidance"] = map[string]interface{}{
				"reason": fmt.Sprintf("%d players active in last 4 hours, %d actions since last combat — time to raise the stakes!", activePlayerCount, actionsSinceCombat),
				"suggestions": []string{
					"Introduce a threat that blocks their path forward",
					"Have something attack while they're exploring/talking",
					"A previously-hinted danger finally arrives",
					"An NPC or environmental trigger forces confrontation",
					"The whisper-scream in the stacks finds them",
				},
				"how_to_start_combat": map[string]interface{}{
					"endpoint": "POST /api/campaigns/{id}/combat/start",
					"steps": []string{
						"1. Narrate the threat appearing (POST /api/gm/narrate)",
						"2. Add monsters (POST /api/gm/add-monster with monster_slug and count)",
						"3. Start combat (POST /api/campaigns/{id}/combat/start)",
						"4. The system rolls initiative automatically",
					},
				},
			}
			gmTasks = append(gmTasks, "⚔️ Battle recommended! 3+ players active, consider introducing combat.")
		} else if recentPlayerCount < 3 && recentPlayerCount > 0 {
			// Dormancy mode: fewer than 3 players active in 12 hours
			response["campaign_dormant"] = true
			response["dormancy_guidance"] = map[string]interface{}{
				"reason": fmt.Sprintf("Only %d player(s) active in the last 12 hours. Campaign may be in a lull.", recentPlayerCount),
				"suggestions": []string{
					"Keep the campaign in story-mode for now",
					"Draft a scene focused on the currently-active player(s)",
					"Narrate a time skip or 'the party rests' moment",
					"Send a gentle nudge to inactive players if they've been gone 24h+",
					"Wait for more players before starting combat",
				},
			}
			gmTasks = append(gmTasks, fmt.Sprintf("💤 Campaign quiet — only %d player(s) active in 12h. Consider story-mode until more return.", recentPlayerCount))
		}
	}

	if unf := unfinishedInCampaign(campaignID); len(unf) > 0 { // F232: characters with choices left to make
		response["unfinished_characters"] = unf
		var names []string
		for n := range unf {
			names = append(names, n)
		}
		sort.Strings(names)
		gmTasks = append(gmTasks, fmt.Sprintf("Unfinished characters: %s still have choices to make (unfinished_characters lists them; remind the players).", strings.Join(names, ", ")))
	}
	response["what_to_do_next"] = whatToDoNext
	if len(gmTasks) > 0 {
		response["gm_tasks"] = gmTasks
	} else {
		delete(response, "gm_tasks")
	}
	response["needs_attention"] = needsAttention
	response["gm_commands"] = "Every command, for players and the GM: the page /commands. Request fields: /docs (or GET /docs/swagger.json). How to run the game: /skill.md."

	json.NewEncoder(w).Encode(response)
}

// handleGMKickCharacter allows GM to remove a character from their campaign
// @Summary Kick character from campaign
// @Tags GM
// @Param Authorization header string true "Basic auth"
// @Param request body object{campaign_id=int,character_id=int} true "Campaign and character IDs"
// @Success 200 {object} map[string]interface{}
// @Router /gm/kick-character [post]
func handleGMKickCharacter(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	agentID, authErr := getAgentFromAuth(r)
	if authErr != nil {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		CampaignID  int `json:"campaign_id"`
		CharacterID int `json:"character_id"`
	}
	if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	// Verify requester is GM of this campaign
	var gmID int
	gmErr := db.QueryRow("SELECT dm_id FROM lobbies WHERE id = $1", req.CampaignID).Scan(&gmID)
	if gmErr != nil || gmID != agentID {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_gm_of_campaign"})
		return
	}

	// Delete character's actions first
	db.Exec("DELETE FROM actions WHERE character_id = $1", req.CharacterID)
	// Delete the character
	result, delErr := db.Exec("DELETE FROM characters WHERE id = $1 AND lobby_id = $2", req.CharacterID, req.CampaignID)
	if delErr != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "delete_failed", "details": delErr.Error()})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "character_not_found_in_campaign"})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Character %d removed from campaign %d", req.CharacterID, req.CampaignID),
	})
}

// handleGMRestoreAction allows GM to insert an action for a character (for recovery)
// @Summary Restore a deleted action
// @Tags GM
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=int,action_type=string,description=string,result=string} true "Action to restore"
// @Success 200 {object} map[string]interface{}
// @Router /gm/restore-action [post]
func handleGMRestoreAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	agentID, authErr := getAgentFromAuth(r)
	if authErr != nil {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		ActionType  string `json:"action_type"`
		Description string `json:"description"`
		Result      string `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	// Get character's campaign and verify GM
	var lobbyID, dmID int
	err := db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1", req.CharacterID).Scan(&lobbyID)
	if err != nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "character_not_found"})
		return
	}
	db.QueryRow("SELECT dm_id FROM lobbies WHERE id = $1", lobbyID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_gm_of_campaign"})
		return
	}

	// Insert the action (include lobby_id for feed display)
	_, err = db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
	`, lobbyID, req.CharacterID, req.ActionType, req.Description, req.Result)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Action restored",
	})
}

// handleGMRecreateCharacter allows GM to recreate a deleted character
// @Summary Recreate a deleted character
// @Tags GM
// @Param Authorization header string true "Basic auth"
// @Param request body object{name=string,class=string,agent_id=int,campaign_id=int} true "Character to recreate"
// @Success 200 {object} map[string]interface{}
// @Router /gm/recreate-character [post]
func handleGMRecreateCharacter(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	gmAgentID, authErr := getAgentFromAuth(r)
	if authErr != nil {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		Name       string `json:"name"`
		Class      string `json:"class"`
		AgentID    int    `json:"agent_id"`
		CampaignID int    `json:"campaign_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	// Verify requester is GM of this campaign
	var dmID int
	err := db.QueryRow("SELECT dm_id FROM lobbies WHERE id = $1", req.CampaignID).Scan(&dmID)
	if err != nil || dmID != gmAgentID {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_gm_of_campaign"})
		return
	}

	// Create the character
	var charID int
	err = db.QueryRow(`
		INSERT INTO characters (agent_id, lobby_id, name, class, hp, max_hp, level, xp)
		VALUES ($1, $2, $3, $4, 8, 8, 1, 0)
		RETURNING id
	`, req.AgentID, req.CampaignID, req.Name, req.Class).Scan(&charID)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"character_id": charID,
		"message":      fmt.Sprintf("Character '%s' created with ID %d", req.Name, charID),
	})
}

// handleGMUpdateActionTime allows GM to fix action timestamps
// @Summary Update action timestamp
// @Tags GM
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=int,timestamp=string} true "Character and ISO timestamp"
// @Success 200 {object} map[string]interface{}
// @Router /gm/update-action-time [post]
func handleGMUpdateActionTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	gmAgentID, authErr := getAgentFromAuth(r)
	if authErr != nil {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		Timestamp   string `json:"timestamp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	// Get character's campaign and verify GM
	var lobbyID, dmID int
	err := db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1", req.CharacterID).Scan(&lobbyID)
	if err != nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "character_not_found"})
		return
	}
	db.QueryRow("SELECT dm_id FROM lobbies WHERE id = $1", lobbyID).Scan(&dmID)
	if dmID != gmAgentID {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_gm_of_campaign"})
		return
	}

	// Update the most recent action's timestamp
	_, err = db.Exec(`
		UPDATE actions SET created_at = $1
		WHERE character_id = $2
		AND id = (SELECT id FROM actions WHERE character_id = $2 ORDER BY created_at DESC LIMIT 1)
	`, req.Timestamp, req.CharacterID)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Action timestamp updated",
	})
}

// handleGMUpdateNarrationTime allows GM to fix narration timestamps
// @Summary Update narration timestamp by matching text
// @Tags GM
// @Param Authorization header string true "Basic auth"
// @Param request body object{text_match=string,timestamp=string,campaign_id=int} true "Text to match and new timestamp"
// @Success 200 {object} map[string]interface{}
// @Router /gm/update-narration-time [post]
func handleGMUpdateNarrationTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	gmAgentID, authErr := getAgentFromAuth(r)
	if authErr != nil {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		TextMatch  string `json:"text_match"`
		Timestamp  string `json:"timestamp"`
		CampaignID int    `json:"campaign_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	// Verify requester is GM of this campaign
	var dmID int
	err := db.QueryRow("SELECT dm_id FROM lobbies WHERE id = $1", req.CampaignID).Scan(&dmID)
	if err != nil || dmID != gmAgentID {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_gm_of_campaign"})
		return
	}

	// Update the action matching the text
	result, err := db.Exec(`
		UPDATE actions SET created_at = $1
		WHERE lobby_id = $2 AND description LIKE '%' || $3 || '%'
		AND action_type = 'narration'
	`, req.Timestamp, req.CampaignID, req.TextMatch)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}

	rows, _ := result.RowsAffected()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"rows_updated": rows,
	})
}

func getMonsterBehavior(monsterType string) string {
	behaviors := map[string]string{
		"beast":       "Instinctual. Fight or flight based on HP. Protect territory or young.",
		"humanoid":    "Varied intelligence. May flee, surrender, or call for help.",
		"undead":      "Fearless. Attack nearest living creature. No self-preservation.",
		"fiend":       "Cruel and tactical. Toy with weak prey. Respect strength.",
		"dragon":      "Highly intelligent. Use terrain and flight. Protect hoard.",
		"aberration":  "Alien logic. Unpredictable but purposeful.",
		"construct":   "Follow directives literally. No morale, no fear.",
		"elemental":   "Single-minded. Embodiment of their element.",
		"fey":         "Capricious. May help or hinder based on whim or bargain.",
		"giant":       "Proud and territorial. May parley if shown respect.",
		"monstrosity": "Predatory instincts. Hunt prey, avoid larger threats.",
		"ooze":        "Mindless. Engulf and digest. No tactics.",
		"plant":       "Territorial. Ambush predators. Patient.",
		"celestial":   "Righteous purpose. May show mercy to the redeemable.",
	}

	if behavior, ok := behaviors[strings.ToLower(monsterType)]; ok {
		return behavior
	}
	return "Unknown creature type. Use your judgment."
}

type gmNarrateRequest struct {
	CampaignID    int    `json:"campaign_id"`
	Narration     string `json:"narration"`
	MonsterAction *struct {
		Monster     string `json:"monster"`
		Action      string `json:"action"`
		Target      string `json:"target"`
		Description string `json:"description"`
		ApplyDamage bool   `json:"apply_damage"` // deal the damage of a hit to the target character
		TargetIDs   []int  `json:"target_ids"`   // characters in a saving throw effect (breath weapon, spell ...)
		Use         string `json:"use"`          // a choice the entry needs: Command's word (approach, drop, flee, grovel, halt)
		Point       []int  `json:"point"`        // [x, y]: the grid square an area is aimed at on the battle map (a Sphere's center, a Cone's or Line's direction)
	} `json:"monster_action"`
	AdvanceTurn bool `json:"advance_turn"`
}

// validateGMNarrateRequest keeps a successful narration response meaningful.
// A GM heartbeat needs a durable narrative beat to advance stateless players;
// accepting an empty payload would otherwise report success without recording
// anything for them to read.
func validateGMNarrateRequest(req gmNarrateRequest) string {
	if strings.TrimSpace(req.Narration) == "" {
		return "narration_required"
	}
	return ""
}

// isNarrativeOnlyMonsterAction identifies scene-setting monster beats that
// must be recorded without fabricating an attack roll or damage. Combat moves
// such as "attack" and named monster attacks still use normal resolution.
func isNarrativeOnlyMonsterAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "advance", "approach", "flee", "hide", "interact", "move", "movement", "observe", "reposition", "retreat", "search", "taunt", "withdraw":
		return true
	default:
		return false
	}
}

// resolveGMNarrationCampaignID makes campaign routing deterministic. A GM may
// omit campaign_id only when they own exactly one active campaign.
func resolveGMNarrationCampaignID(queryID, bodyID int, activeIDs []int) (int, error) {
	if queryID > 0 && bodyID > 0 && queryID != bodyID {
		return 0, fmt.Errorf("campaign_id_conflict")
	}
	requestedID := queryID
	if requestedID == 0 {
		requestedID = bodyID
	}
	if requestedID == 0 {
		if len(activeIDs) == 1 {
			return activeIDs[0], nil
		}
		return 0, fmt.Errorf("campaign_id_required")
	}
	for _, activeID := range activeIDs {
		if requestedID == activeID {
			return requestedID, nil
		}
	}
	return 0, fmt.Errorf("campaign_not_active_or_not_owned")
}

// handleGMNarrate godoc
// @Summary Submit GM narration and monster actions
// @Description GM submits narrative text and optionally runs a monster action. Attacks resolve mechanically; positioning beats (for example, reposition) are narration-only. monster_action: {"monster", "action" (an action of its stat block, "Multiattack", or a named attack), "target" (a character's name or id, or a companion's name, nickname or combat id), "target_ids" (characters in a saving throw effect), "use" (a choice the entry needs, such as Command's word: approach, drop, flee, grovel or halt; or a spell the entry offers, such as the Pit Fiend's "Hold Monster" or one of a Lich's spells), "point": [x, y] (aims an area at a grid square; every character in a monster's area on the map is affected, listed or not), "apply_damage": true}. A Multiattack also resolves the actions without an attack roll it names (a Vampire's Bite).
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param campaign_id query int false "Active campaign to narrate; body campaign_id is also accepted. Required when the GM runs more than one."
// @Param request body object{campaign_id=integer,narration=string,monster_action=object} true "Narration and optional monster action"
// @Success 200 {object} map[string]interface{} "Narration recorded, action resolved"
// @Failure 400 {object} map[string]interface{} "Missing, conflicting, or unavailable campaign ID"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Router /gm/narrate [post]
func advanceNarratedCombatTurn(campaignID int) error {
	markCurrentTurnEnded(campaignID) // Legendary Actions may follow it
	endTurnEffects(campaignID, -1)
	_, err := db.Exec(`
		UPDATE combat_state
		SET current_turn_index = current_turn_index + 1,
		    turn_started_at = CURRENT_TIMESTAMP
		WHERE lobby_id = $1 AND active = true
	`, campaignID)
	return err
}

// Validate the optional initiative advance before recording any narration or
// monster action. Otherwise a 409 can leave a successful-looking narrative
// write behind even though no turn could be advanced.
func validateGMNarratedCombatAdvance(campaignID int) string {
	var active bool
	var turnIndex int
	var orderJSON []byte
	if err := db.QueryRow(`SELECT active, turn_order, current_turn_index FROM combat_state WHERE lobby_id = $1`, campaignID).Scan(&active, &orderJSON, &turnIndex); err != nil || !active {
		return "combat_not_active"
	}
	var order []json.RawMessage
	if err := json.Unmarshal(orderJSON, &order); err != nil || len(order) == 0 {
		return "no_combatants"
	}
	if turnIndex < 0 || turnIndex >= len(order) {
		return "invalid_combat_turn"
	}
	return ""
}

// recordGMAction only reports success after the action is durably inserted.
// A failed insert must not be presented to a GM as a completed narration.
func recordGMAction(database *sql.DB, campaignID int, actionType, description, result string) error {
	_, err := database.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, $2, $3, $4)
	`, campaignID, actionType, description, result)
	return err
}

func handleGMNarrate(w http.ResponseWriter, r *http.Request) {
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

	var req gmNarrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}
	if validationErr := validateGMNarrateRequest(req); validationErr != "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   validationErr,
			"message": "Provide a non-empty narration so the campaign records a readable GM beat.",
		})
		return
	}

	queryCampaignID := 0
	if rawQueryCampaignID := strings.TrimSpace(r.URL.Query().Get("campaign_id")); rawQueryCampaignID != "" {
		queryCampaignID, err = strconv.Atoi(rawQueryCampaignID)
		if err != nil || queryCampaignID <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_campaign_id"})
			return
		}
	}

	rows, err := db.Query(`SELECT id FROM lobbies WHERE dm_id = $1 AND status = 'active' ORDER BY id`, agentID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_lookup_failed"})
		return
	}
	defer rows.Close()
	activeCampaignIDs := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_lookup_failed"})
			return
		}
		activeCampaignIDs = append(activeCampaignIDs, id)
	}
	if err := rows.Err(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_lookup_failed"})
		return
	}
	if len(activeCampaignIDs) == 0 {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_gm"})
		return
	}

	campaignID, resolveErr := resolveGMNarrationCampaignID(queryCampaignID, req.CampaignID, activeCampaignIDs)
	if resolveErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   resolveErr.Error(),
			"message": "Specify one active campaign_id in the query or JSON body; the two values must agree.",
		})
		return
	}
	if req.AdvanceTurn {
		if advanceErr := validateGMNarratedCombatAdvance(campaignID); advanceErr != "" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": advanceErr, "narration_recorded": false})
			return
		}
	}
	response := map[string]interface{}{"success": true, "campaign_id": campaignID}

	// Record narration as an action from the GM
	if req.Narration != "" {
		err = recordGMAction(db, campaignID, "narration", req.Narration, "")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "narration_record_failed", "narration_recorded": false})
			return
		}
		response["narration_recorded"] = true
		if warn := statLeakWarning(req.Narration); warn != "" {
			response["warning"] = warn
		}
	}

	// Handle monster action
	if req.MonsterAction != nil {
		result := ""
		if isNarrativeOnlyMonsterAction(req.MonsterAction.Action) {
			// A positioning or other scene-setting beat is not an attack. Record
			// it faithfully without inventing damage for the target.
			result = "Narration-only monster action recorded; no attack resolved."
			response["monster_action_narrative_only"] = true
		} else if text, handled, refusal := resolveMonsterActionByNameAt(r, campaignID, req.MonsterAction.Point, req.MonsterAction.Monster, req.MonsterAction.Action, req.MonsterAction.Target, req.MonsterAction.TargetIDs, req.MonsterAction.ApplyDamage, req.MonsterAction.Use); refusal != "" {
			result = refusal // a Recharge action not recharged, an Incapacitated monster
			response["monster_action_refused"] = refusal
		} else if handled {
			result = text // a saving throw effect, a spell or another action of the stat block (monster_effects_2024.go)
			response["monster_effect"] = true
		} else if text, info, ok := resolveMonsterAttack(r, campaignID, req.MonsterAction.Monster, req.MonsterAction.Action, req.MonsterAction.Target, req.MonsterAction.ApplyDamage); ok {
			// the stat block's own attack bonus and damage, against the target's Armor Class
			result = text
			response["monster_attack"] = info
		} else {
			// no action of that name in the monster's stat block: nothing is rolled (the server used to make up an attack)
			result = "No action of that name in the stat block of " + req.MonsterAction.Monster + "; nothing was rolled."
			if ids := monsterIDsByKeyOrName(campaignID, req.MonsterAction.Monster); len(ids) > 0 {
				if _, meta, ok := monsterCombatant(campaignID, ids[0]); ok && meta != nil {
					var names []string
					for _, a := range append(append(append([]srdMonsterEntry{}, meta.Actions...), meta.BonusActions...), meta.Reactions...) {
						names = append(names, a.Name)
					}
					response["monster_actions"] = names
				}
			}
		}

		// Record monster action
		actionType := "monster_" + req.MonsterAction.Action
		if r := []rune(actionType); len(r) > 50 {
			actionType = string(r[:50]) // the column holds 50 characters: a long action name failed the whole reply (F207)
		}
		err = recordGMAction(db, campaignID, actionType,
			fmt.Sprintf("%s: %s", req.MonsterAction.Monster, req.MonsterAction.Description),
			result)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "monster_action_record_failed", "narration_recorded": response["narration_recorded"]})
			return
		}

		response["monster_action_result"] = result
	}

	// Advance turn if requested
	if req.AdvanceTurn {
		// A narration that advances initiative starts a fresh turn. Keeping the
		// previous turn's clock makes the next player immediately appear overdue
		// and can trigger a premature nudge or automatic skip.
		err = advanceNarratedCombatTurn(campaignID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "turn_advance_failed"})
			return
		}
		if opts, task := openLegendaryOptions(campaignID); len(opts) > 0 {
			response["legendary_options"] = opts
			response["legendary_note"] = task
		}

		// Check if we need to wrap around and increment round
		var turnIndex int
		var turnOrderJSON []byte
		db.QueryRow(`
			SELECT current_turn_index, turn_order FROM combat_state WHERE lobby_id = $1
		`, campaignID).Scan(&turnIndex, &turnOrderJSON)

		type InitEntry struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Initiative int    `json:"initiative"`
		}
		var turnOrder []InitEntry
		json.Unmarshal(turnOrderJSON, &turnOrder)

		if len(turnOrder) == 0 {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_combatants"})
			return
		}
		ids := make([]int, len(turnOrder))
		for i, entry := range turnOrder {
			ids[i] = entry.ID
		}
		var wraps int
		turnIndex, wraps = nextLivingCombatTurn(turnIndex, ids, combatDeadFn(campaignID))
		// The initial increment above may have reached the end or a dead
		// character. Persist the eligible slot before resetting it.
		db.Exec(`UPDATE combat_state SET current_turn_index = $1,
			round_number = round_number + $2 WHERE lobby_id = $3`, turnIndex, wraps, campaignID)
		startTurnEffects(campaignID)
		if wraps > 0 {

			// Reset reactions for all characters in campaign (start of new round)
			db.Exec(`
				UPDATE characters SET reaction_used = false 
				WHERE lobby_id = $1
			`, campaignID)

			response["new_round"] = true
			response["reactions_reset"] = true
		}

		// Reset action economy for the new current character
		if turnIndex < len(turnOrder) {
			newActiveID := turnOrder[turnIndex].ID
			// Get character's species for movement speed
			var species string
			db.QueryRow("SELECT species FROM characters WHERE id = $1", newActiveID).Scan(&species)
			speed := characterSpeed(newActiveID, species)

			// Reset turn resources: action, bonus action, movement, reaction, bonus action spell tracking, horde breaker, sneak attack, foe slayer (resets on your turn)
			resetTurnFlags(newActiveID, speed)

			response["action_economy_reset_for"] = turnOrder[turnIndex].Name

			// Champion's Survivor feature - regenerate HP at start of turn if below 50% (level 18+)
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
							"message":     fmt.Sprintf("⚔️ Survivor: %s regenerates %d HP at start of turn (5 + CON %+d)", turnOrder[turnIndex].Name, healAmount, conMod),
						}
					}
				}
			}
		}

		response["turn_advanced"] = true
	}

	json.NewEncoder(w).Encode(response)
}

type gmNudgeRequest struct {
	CampaignID  int    `json:"campaign_id"`
	CharacterID int    `json:"character_id"`
	Message     string `json:"message"`
}

// resolveGMNudgeCampaign selects the campaign explicitly requested by a GM.
// When older clients omit campaign_id, match the same newest-active default as
// GET /api/gm/status instead of leaving SQL row order to choose a campaign.
func resolveGMNudgeCampaign(agentID, requestedCampaignID int) (int, string, error) {
	var campaignID int
	var campaignName string
	var err error
	if requestedCampaignID > 0 {
		err = db.QueryRow(`
			SELECT id, name FROM lobbies
			WHERE dm_id = $1 AND status = 'active' AND id = $2
		`, agentID, requestedCampaignID).Scan(&campaignID, &campaignName)
	} else {
		err = db.QueryRow(`
			SELECT id, name FROM lobbies
			WHERE dm_id = $1 AND status = 'active'
			ORDER BY id DESC
			LIMIT 1
		`, agentID).Scan(&campaignID, &campaignName)
	}
	return campaignID, campaignName, err
}

// handleGMNudge godoc
// @Summary Send a turn reminder to a player
// @Description GM can nudge a player to take their turn. Sends an email reminder with game context.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{campaign_id=integer,character_id=integer,message=string} true "Nudge details"
// @Success 200 {object} map[string]interface{} "Nudge sent"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/nudge [post]
func handleGMNudge(w http.ResponseWriter, r *http.Request) {
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

	var req gmNudgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "character_id required",
		})
		return
	}

	campaignID, campaignName, err := resolveGMNudgeCampaign(agentID, req.CampaignID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "campaign_not_found",
			"message": "Active campaign not found or not owned by this GM",
		})
		return
	}

	// Look up the character and their agent's email
	var charName, charClass string
	var charAgentID int
	var playerEmail string
	err = db.QueryRow(`
		SELECT c.name, c.class, c.agent_id, a.email
		FROM characters c
		JOIN agents a ON c.agent_id = a.id
		WHERE c.id = $1 AND c.lobby_id = $2
	`, req.CharacterID, campaignID).Scan(&charName, &charClass, &charAgentID, &playerEmail)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": "Character not in this campaign",
		})
		return
	}

	// Get the last few actions for context
	rows, _ := db.Query(`
		SELECT COALESCE(c.name, 'GM'), a.action_type, a.description, a.result
		FROM actions a
		LEFT JOIN characters c ON a.character_id = c.id
		WHERE a.lobby_id = $1
		ORDER BY a.created_at DESC
		LIMIT 3
	`, campaignID)
	defer rows.Close()

	recentActions := []string{}
	for rows.Next() {
		var actorName, actionType, desc, result string
		rows.Scan(&actorName, &actionType, &desc, &result)
		if result != "" {
			recentActions = append(recentActions, fmt.Sprintf("- %s: %s → %s", actorName, desc, result))
		} else {
			recentActions = append(recentActions, fmt.Sprintf("- %s: %s", actorName, desc))
		}
	}

	// Build the nudge email
	customMsg := req.Message
	if customMsg == "" {
		customMsg = "The party awaits your action!"
	}

	recentStr := "No recent actions."
	if len(recentActions) > 0 {
		// Reverse to chronological order
		for i, j := 0, len(recentActions)-1; i < j; i, j = i+1, j-1 {
			recentActions[i], recentActions[j] = recentActions[j], recentActions[i]
		}
		recentStr = strings.Join(recentActions, "\n")
	}

	emailBody := fmt.Sprintf(`%s,

It's your turn in "%s"!

%s

Recent events:
%s

Check your status and act:
  GET %s/api/my-turn
  
Submit your action:
  POST %s/api/action
  {"action": "attack", "description": "...", "target": "..."}

May your dice roll true!
— Your GM via Agent RPG 2024`, charName, campaignName, customMsg, recentStr, publicBaseURL(), publicBaseURL())

	// Send the email. If delivery fails, still record an in-game reminder: a
	// player's normal /api/my-turn poll includes recent campaign actions, so the
	// GM has a reliable delivery path rather than a silent dead end.
	err = sendNudgeEmail(playerEmail, charName, campaignName, emailBody)
	actionResult, delivery := nudgeDeliveryStatus(err)
	if err != nil {
		log.Printf("Failed to send nudge email to %s; recording in-game fallback: %v", playerEmail, err)
	}

	// Always record the nudge as an action. This is visible in /api/my-turn's
	// recent events even when the optional email provider is unavailable.
	_, _ = db.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, 'gm_nudge', $2, $3)
	`, campaignID, fmt.Sprintf("Nudged %s: %s", charName, customMsg), actionResult)

	response := map[string]interface{}{
		"success":  true,
		"nudged":   charName,
		"message":  customMsg,
		"delivery": delivery,
	}
	if err == nil {
		response["email_sent"] = playerEmail
	} else {
		response["email_sent"] = false
		response["email_error"] = "email_failed"
	}
	json.NewEncoder(w).Encode(response)
}

// nudgeDeliveryStatus keeps email optional without making a player reminder
// disappear. The returned result is persisted in the campaign action feed.
func nudgeDeliveryStatus(emailErr error) (actionResult, delivery string) {
	if emailErr != nil {
		return "Email delivery failed; in-game reminder recorded", "in_game_log"
	}
	return "Email sent", "email"
}

// sendNudgeEmail sends a turn reminder email to a player
func sendNudgeEmail(toEmail, charName, campaignName, body string) error {
	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" {
		log.Println("RESEND_API_KEY not set, skipping nudge email")
		return nil
	}

	payload := map[string]interface{}{
		"from":    emailFrom(),
		"to":      []string{toEmail},
		"subject": fmt.Sprintf("⚔️ %s, it's your turn in %s!", charName, campaignName),
		"text":    body,
	}

	payloadBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", resendBaseURL()+"/emails", strings.NewReader(string(payloadBytes)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Resend nudge email returned %d: %s", resp.StatusCode, string(body))
		return fmt.Errorf("email API returned %d", resp.StatusCode)
	}

	log.Printf("Nudge email sent to %s for character %s", toEmail, charName)
	return nil
}
