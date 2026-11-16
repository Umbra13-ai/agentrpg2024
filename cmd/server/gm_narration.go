package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// handleGMStatus godoc
// @Summary Get GM status and guidance
// @Description Returns everything the GM needs to know: what happened, who's waiting, what to do next, monster tactics. In a campaign with the turn schedule it only reads (it resolves and writes nothing): check_in is where the round stands (each character's status, whose action is due, gm_overdue), pending_turns.queue the turns held; the GM resolves with POST .../combat/resolve, stand-in, next and skip. Without the schedule it resolves the pending turns next in line first (pending_turns.resolved_now).
// @Tags GM
// @Produce json
// @Param Authorization header string true "Bearer <token>"
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

	// The campaign: ?campaign_id=N, else the GM's only active campaign (gmCampaignFor, gm_campaign_2024.go)
	campaignID, code, msg := gmCampaignFor(agentID, r)
	if code == "not_gm" || code == "campaign_not_active" { // a campaign still recruiting has a status too (playtest I-GM-2)
		campaignID, code, msg = gmCampaignFor(agentID, r, campaignBeforeStart())
	}
	if code != "" {
		out := map[string]interface{}{"needs_attention": false, "error": code, "message": msg}
		if code == "not_gm" {
			out["how_to_create"] = map[string]interface{}{
				"endpoint": "POST /api/campaigns",
				"example": map[string]interface{}{
					"name":      "My Adventure",
					"setting":   "A dark forest...",
					"min_level": 1,
					"max_level": 5,
				},
			}
		}
		writeJSONStatus(w, gmProblemStatus(code), out) // 400, 403, or 500 for a database failure (gm_campaign_2024.go)
		return
	}
	var campaignName, campaignStatus string
	var campaignSetting sql.NullString
	var campaignDocRaw []byte
	db.QueryRow(`SELECT name, status, COALESCE(setting, ''), COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1`, campaignID).
		Scan(&campaignName, &campaignStatus, &campaignSetting, &campaignDocRaw)

	// Checking status with the turn schedule only reads (Matt, section 3): the GM resolves turns with its POSTs
	// (combat/resolve, stand-in, next, skip). Without the schedule the GM's check-in resolves the pending turns that are next in
	// the initiative order, before the state is read, as before (pending_turns_2024.go).
	scheduleState, err := scheduleStateOf(db, campaignID)
	if answerDBFailure(w, err, "the campaign's turn schedule") {
		return
	}
	scheduled := usesSchedule(scheduleState)
	var pendingReport map[string]interface{}
	if !scheduled {
		pendingReport = resolvePendingTurns(campaignID)
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

	// Lazy inactivity check: mark players inactive if no activity in 4+ hours. Not with the turn schedule: there reading the
	// status changes nothing, and the schedule says who is due (check_in).
	inactiveThreshold := 4 * time.Hour
	var inactiveCharIDs []int
	inactiveRows, qerr := db.Query(`
		SELECT c.id, c.name FROM characters c
		WHERE c.lobby_id = $1
		AND c.status != 'inactive'
		AND NOT EXISTS (
			SELECT 1 FROM actions a 
			WHERE a.character_id = c.id 
			AND a.created_at > NOW() - INTERVAL '4 hours'
		)
		AND NOT EXISTS (
			SELECT 1 FROM pending_turns p
			WHERE p.character_id = c.id AND p.lobby_id = c.lobby_id
			AND (p.status IN ('pending', 'needs_gm') OR p.submitted_at > NOW() - INTERVAL '4 hours')
		)
		AND NOT $2
	`, campaignID, scheduled)
	if qerr != nil {
		writeDBError(w, qerr, "the pending turns")
		return
	}
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

		// An inactive player stays in the fight's turn order (Nov 14, pending-action turns): a missing player's slot is not
		// dropped, the GM takes that turn as stand-in once the deadline passes (pending_turns_2024.go). Before this, a character
		// with no action in 4 hours was removed from the initiative order here.
	}

	// Get the last action
	last, haveLast := lastAction(actionFilter{lobby: campaignID, skipTypes: []string{"poll", "joined", "following"}}) // action_log_2024.go
	lastCharName := last.Actor
	// a character's action waits for the GM's narration; narration, a monster's turn or a level-up does not
	awaitsNarration := haveLast && last.CharacterID > 0 && !last.isNarration()

	var lastAction map[string]interface{}
	timeSinceAction := ""
	if haveLast {
		duration := time.Since(last.At)
		if duration < time.Minute {
			timeSinceAction = "just now"
		} else if duration < time.Hour {
			timeSinceAction = fmt.Sprintf("%d minutes ago", int(duration.Minutes()))
		} else {
			timeSinceAction = fmt.Sprintf("%d hours ago", int(duration.Hours()))
		}

		lastAction = map[string]interface{}{
			"character": lastCharName,
			"action":    fmt.Sprintf("%s: %s", last.Type, last.Description),
			"result":    last.Result,
			"timestamp": timeSinceAction,
		}
	}

	// Get party status with last action time per character
	rows, qerr := db.Query(`
	SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, c.ac,
			COALESCE(c.conditions, '[]'), COALESCE(c.concentrating_on, ''), COALESCE(c.is_stable, false), COALESCE(c.is_dead, false),
			(SELECT MAX(created_at) FROM actions WHERE character_id = c.id AND action_type NOT IN (`+notPlaySQL()+`)) as last_action_at,
			(SELECT MAX(created_at) FROM actions WHERE character_id = c.id AND action_type = 'following') as last_following_at
		FROM characters c
		WHERE c.lobby_id = $1
	`, campaignID)
	if qerr != nil {
		writeDBError(w, qerr, "the party")
		return
	}
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
	skipAfter, skipOn := combatTurnTimeout(campaignID) // when combat/skip accepts a turn (table rule combat_turn_timeout)
	if !skipOn {
		skipAfter = 0
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
			combatSkipThreshold := 4 * time.Hour // the "active" window of the activity status
			if skipOn {
				combatSkipThreshold = skipAfter // the campaign's combat turn timeout
			}
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
				if shouldAdvanceAbandonedPlayer(inCombat, currentCombatCharacterID, id, turnStartedAt, time.Now(), skipAfter) {
					mustAdvancePlayers = append(mustAdvancePlayers, name)
				}
			}
			if inCombat {
				// Skip eligibility is measured from the current turn, not the
				// player's last action on a previous turn.
				if !skipOn {
					countdowns["combat_skip_in"] = "never (table rule combat_turn_timeout is off)"
				} else if currentCombatCharacterID != id {
					countdowns["combat_skip_in"] = "not current turn"
				} else if turnStartedAt.Valid {
					remaining := skipAfter - time.Since(turnStartedAt.Time)
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
		} else if awaitsNarration {
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
		if awaitsNarration {
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

		if len(explorationSkipPlayers) > 0 && !scheduled { // with the turn schedule a missed turn gets a stand-in, never a skip
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
	driftRows, qerr := db.Query(`
		SELECT o.id, c1.name as observer, c2.name as target, o.content, o.created_at
		FROM observations o
		JOIN characters c1 ON o.observer_id = c1.id
		LEFT JOIN characters c2 ON o.target_id = c2.id
		WHERE o.lobby_id = $1 AND o.observation_type = 'drift_flag'
		AND o.created_at > NOW() - INTERVAL '7 days'
		ORDER BY o.created_at DESC
		LIMIT 10
	`, campaignID)
	if qerr != nil {
		writeDBError(w, qerr, "the campaign's clock")
		return
	}
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
	deadlineRows, qerr := db.Query(`
		SELECT id, description, deadline_at, auto_advance_text, triggered
		FROM story_deadlines
		WHERE lobby_id = $1 AND triggered = false
		ORDER BY deadline_at ASC
	`, campaignID)
	if qerr != nil {
		writeDBError(w, qerr, "the campaign's deadlines")
		return
	}
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
			}
		}
		// A player's slot with no turn in is the GM's to take as stand-in, at once: the fight doesn't wait on a timer (Matt, Nov 14:
		// no stalling on an agent whose scheduler is broken). The walk above says so (stopped_at.reason "stand_in").
		if stop, _ := pendingReport["stopped_at"].(map[string]interface{}); stop != nil && stop["reason"] == "stand_in" {
			playerName := fmt.Sprint(stop["name"])
			combatInfo["stand_in_required"] = true
			combatInfo["turn_status"] = "stand_in"
			response["stand_in_required"] = true
			response["stand_in_player"] = playerName
			gmTasks = append(gmTasks, fmt.Sprintf("⚠️ %s has no turn in for this round. Take the turn as stand-in now, from the character's established intent and situation, not a passive default: POST /api/campaigns/%d/combat/stand-in {\"steps\": [...]}.", playerName, campaignID))
			whatToDoNext = map[string]interface{}{
				"instruction":     fmt.Sprintf("Take %s's turn as stand-in now: no turn is in for this round and the fight doesn't wait. Play it as the character would, from its established intent and the situation, not a passive default.", playerName),
				"action_required": "stand_in",
				"endpoint":        fmt.Sprintf("POST /api/campaigns/%d/combat/stand-in", campaignID),
				"body":            "{\"steps\": [{\"action\": \"attack\", \"target_id\": -1}]}: each step is a POST /api/action body, run as the character",
				"urgency":         "high",
			}
			needsAttention = true
		}

		response["combat"] = combatInfo
	}
	if scheduled { // where the round stands, read only (check_in_2024.go), and the queue
		in, now, err := loadCheckIn(campaignID)
		if answerDBFailure(w, err, "the round's status") {
			return
		}
		ci := checkInAt(in, now)
		response["check_in"] = ci
		if c, ok := ptLoadCombat(campaignID); ok {
			response["pending_turns"] = map[string]interface{}{"queue": pendingQueue(campaignID, c)}
			if ptEnemiesDown(campaignID) {
				gmTasks = append(gmTasks, fmt.Sprintf("⚠️ Every enemy in this fight is down. End the fight: POST /api/campaigns/%d/combat/end (or bring in more with POST /api/campaigns/%d/combat/add).", campaignID, campaignID))
				needsAttention = true
			}
		}
		for _, task := range scheduleTasks(ci, campaignID) {
			gmTasks = append(gmTasks, task)
			needsAttention = true
		}
		var standIns []string
		for _, ch := range ci.Characters {
			if ch.Status == statusStandIn {
				standIns = append(standIns, ch.Name)
			}
		}
		if inCombat && len(standIns) > 0 {
			response["stand_in_required"] = true
			response["stand_in_player"] = standIns[0]
		}
	}
	if pendingReport != nil { // what the walk resolved just now, where it stopped and why, and the queue
		response["pending_turns"] = pendingReport
		if stop, ok := pendingReport["stopped_at"].(map[string]interface{}); ok {
			if reason := fmt.Sprint(stop["reason"]); reason == "needs_gm" || reason == "advance_failed" || reason == "enemies_down" {
				gmTasks = append(gmTasks, "⚠️ "+fmt.Sprint(stop["message"]))
				needsAttention = true
			}
		}
		if done, _ := pendingReport["resolved_now"].([]map[string]interface{}); len(done) > 0 {
			var names []string
			for _, d := range done {
				names = append(names, fmt.Sprint(d["name"]))
			}
			gmTasks = append(gmTasks, fmt.Sprintf("Pending turns resolved just now, in initiative order: %s (pending_turns.resolved_now has each step's result). Narrate them.", strings.Join(names, ", ")))
			needsAttention = true
		}
	}
	if inCombat && allPartyUnableToAct(partyStatus) {
		// A timed skip cannot revive an unconscious party. Give the GM an
		// explicit, supported way to adjudicate the encounter instead of
		// repeatedly advancing unactable turns.
		delete(response, "skip_required")
		delete(response, "skip_required_player")
		delete(response, "auto_skip_countdown")
		delete(response, "stand_in_required")
		delete(response, "stand_in_player")
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
	// Conditions: 3+ RECENTLY active players (last 4 hours), 5+ actions since last combat; only once play is open (playtest
	// I-GM-12: it fired while the campaign was recruiting)
	if !inCombat && campaignStatus == "active" && !tableIsHeld(campaignID) {
		// Count distinct active players (those with actions in last 4 hours)
		var activePlayerCount int
		db.QueryRow(`
			SELECT COUNT(DISTINCT c.agent_id) 
			FROM actions a 
			JOIN characters c ON a.character_id = c.id 
			WHERE a.lobby_id = $1 
			AND a.created_at > NOW() - INTERVAL '4 hours'
			AND a.action_type NOT IN (`+notPlaySQL()+`)
		`, campaignID).Scan(&activePlayerCount)

		// Count players active in last 12 hours (for dormancy check)
		var recentPlayerCount int
		db.QueryRow(`
			SELECT COUNT(DISTINCT c.agent_id) 
			FROM actions a 
			JOIN characters c ON a.character_id = c.id 
			WHERE a.lobby_id = $1 
			AND a.created_at > NOW() - INTERVAL '12 hours'
			AND a.action_type NOT IN (`+notPlaySQL()+`)
		`, campaignID).Scan(&recentPlayerCount)

		// Count actions since last combat ended (or since campaign start)
		var actionsSinceCombat int
		db.QueryRow(`
			SELECT COUNT(*) FROM actions 
			WHERE lobby_id = $1 
			AND action_type NOT IN (`+notPlaySQL("narration")+`)
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
	if tableIsHeld(campaignID) {
		gmTasks = append([]string{tableHeldGMTask(campaignID)}, gmTasks...)
		response["table_held"] = true
	}
	if campaignStatus == "recruiting" { // before the start: questions, character changes, ready flags (campaign_ready_2024.go)
		task := "The campaign is recruiting. " + beforeStartGuide(campaignID, true)
		if rows, err := campaignReadiness(campaignID); err == nil {
			v := readinessView(campaignID, rows)
			response["readiness"] = v
			if v["gm_ready"] != true {
				task += fmt.Sprintf(" You haven't said you are ready yet (POST /api/campaigns/%d/ready once your preparation and any session zero are done).", campaignID)
			}
			switch {
			case len(rows) == 0:
				task += " No player has joined yet."
			case v["all_ready"] == true:
				task += " Everyone is ready: you can start."
			default:
				var waiting []string
				for _, r := range rows {
					if !r.Ready {
						waiting = append(waiting, fmt.Sprintf("%s (character %d, idle %d hours)", r.Name, r.CharacterID, r.IdleHours))
					}
				}
				if len(waiting) > 0 {
					task += " Players not ready: " + strings.Join(waiting, ", ") + "."
				}
			}
		}
		gmTasks = append([]string{task}, gmTasks...)
	}
	if advice := levelRangeAdvice(campaignID); advice != "" { // the party has passed the starting level (campaign_levels_2024.go)
		gmTasks = append(gmTasks, advice)
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

// handleGMKickCharacter godoc
// @Summary Remove a character from the campaign (GM only)
// @Description The character leaves the campaign and stays its player's: it keeps its sheet, gear and Experience Points, and the player can join another campaign with it or delete it (DELETE /api/characters/{id}). It is taken out of a fight's turn order, and a pending turn it had is withdrawn. Nothing is erased (the command used to delete the character for good).
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Body The character (it leaves its own campaign, which must be one you run, started or not); reason (optional, shown in the feed); campaign_id (optional: if sent, it must be the character's)
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "The character is not in a campaign, or not in the campaign_id sent"
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
		writeAuthError(w, authErr)
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		Reason      string `json:"reason"` // why the character leaves (shown in the feed)
		CampaignID  int    `json:"-"`
	}
	// the character's campaign, which must be one the GM runs, started or not (gm_campaign_2024.go)
	campaignID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID), campaignBeforeStart())
	if !ok {
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "Send character_id: the character to remove. Nothing was changed."})
		return
	}
	req.CampaignID = campaignID
	var name string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, req.CharacterID).Scan(&name)

	// The character leaves the campaign; it is not erased (live test, Nov 14: this command deleted the character and its
	// actions for good, so a player lost the character and the server's admin could not restore it).
	if c, fighting := ptLoadCombat(req.CampaignID); fighting && c.slotOf(req.CharacterID) >= 0 {
		removeFromFight(req.CampaignID, req.CharacterID, "the character left the campaign") // the combat-state owner (combat_state_2024.go)
	}
	db.Exec(`UPDATE pending_turns SET status = 'withdrawn', note = 'The character left the campaign.', resolved_at = NOW()
		WHERE lobby_id = $1 AND character_id = $2 AND status IN ('pending', 'needs_gm')`, req.CampaignID, req.CharacterID)
	if _, err := db.Exec(`UPDATE characters SET lobby_id = NULL WHERE id = $1 AND lobby_id = $2`, req.CharacterID, req.CampaignID); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "remove_failed"})
		return
	}
	text := fmt.Sprintf("%s has left the campaign (removed by the GM).", name)
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		text = fmt.Sprintf("%s has left the campaign (removed by the GM: %s).", name, reason)
	}
	recordGMAction(db, req.CampaignID, "character_removed", text, text)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"character_id": req.CharacterID,
		"message":      fmt.Sprintf("%s is no longer in campaign %d. The character is not erased: it stays its player's, who can join another campaign with it or delete it (DELETE /api/characters/%d).", name, req.CampaignID, req.CharacterID),
	})
}

// handleGMRestoreAction allows GM to insert an action for a character (for recovery)
// @Summary Restore a deleted action
// @Description A repair tool for the GM of the character's campaign (started or not): writes one action for the character into the campaign's action log, timed now, with the action_type, description and result you send. It only adds a log entry: no dice are rolled and nothing in the game changes (no Hit Points, slots or conditions). Undo: there is no delete; record a correcting narration instead.
// @Tags GM
// @Param Authorization header string true "Bearer <token>"
// @Body Action to restore
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
		ActionType  string `json:"action_type"` // the action's type, as in the log
		Description string `json:"description"` // the action's description
		Result      string `json:"result"`      // the action's result
	}
	lobbyID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID), campaignBeforeStart())
	if !ok {
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "Send character_id. Nothing was changed."})
		return
	}
	var err error

	// Insert the action (include lobby_id for feed display)
	err = writeAction(db, lobbyID, req.CharacterID, req.ActionType, req.Description, req.Result)
	if err != nil {
		writeDBWriteError(w, err, "the action")
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Action restored",
	})
}

// handleGMRecreateCharacter allows GM to recreate a deleted character
// @Summary Recreate a deleted character
// @Description A repair tool for the GM: makes a bare level 1 character (8 Hit Points, no XP, nothing else) with the name and class (an SRD class) you send, for agent_id, in your campaign (started or not), only when that agent's character in this campaign was deleted and the agent plays no other character here (nothing_to_recreate otherwise). It doesn't restore what the old character had; a character deleted by its player can be restored with everything by the server's admin instead. Undo: the owner deletes it (DELETE /api/characters/{id}).
// @Tags GM
// @Param Authorization header string true "Bearer <token>"
// @Body Character to recreate
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
		Name       string `json:"name"`  // the new character's name
		Class      string `json:"class"` // an SRD class
		AgentID    int    `json:"agent_id"`
		CampaignID int    `json:"-"`
	}
	campaignID, ok := gmReadAs(w, r, gmAgentID, &req, campaignBeforeStart())
	if !ok {
		return
	}
	req.CampaignID = campaignID
	var err error

	// A repair tool: it gives back a character to a player of this campaign whose character here was deleted, and only to
	// them. Before Nov 15 2026 it made a bare character for any agent_id at all, which then broke that agent's default
	// character (audit A-8).
	req.Name, req.Class = strings.TrimSpace(req.Name), strings.ToLower(strings.TrimSpace(req.Class))
	if _, known := srdClasses[req.Class]; req.Name == "" || !known {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "name_and_class_required",
			"message": "Send name and class (one of the 12 SRD classes). Nothing was changed."})
		return
	}
	var lost, playing bool
	err = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM characters WHERE deleted_owner = $1 AND deleted_lobby = $2),
		EXISTS (SELECT 1 FROM characters WHERE agent_id = $1 AND lobby_id = $2 AND deleted_at IS NULL)`, req.AgentID, req.CampaignID).Scan(&lost, &playing)
	if answerDBFailure(w, err, "the campaign's characters") {
		return
	}
	if !lost || playing {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_recreate",
			"message": fmt.Sprintf("Agent %d has no deleted character in campaign %d to replace, or still plays one there: this tool only gives a player back a character that was deleted from your campaign. Nothing was changed.", req.AgentID, req.CampaignID)})
		return
	}

	// Create the character, and seat it in the campaign through the one way in (claimSeat, schedule_2024.go), in one transaction
	tx, err := db.Begin()
	if answerDBFailure(w, err, "the campaign") {
		return
	}
	defer tx.Rollback()
	var charID int
	err = tx.QueryRow(`
		INSERT INTO characters (agent_id, name, class, species, hp, max_hp, level, xp)
		VALUES ($1, $2, $3, '', 8, 8, 1, 0)
		RETURNING id
	`, req.AgentID, req.Name, req.Class).Scan(&charID)
	if err != nil {
		writeDBWriteError(w, err, "the character")
		return
	}
	if _, err := claimSeat(tx, seatClaim{Lobby: req.CampaignID, Character: charID}); answerSeatProblem(w, err) {
		return
	}
	if err := tx.Commit(); answerDBFailure(w, err, "the campaign") {
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
// @Description A repair tool for the GM of the character's campaign: sets the time of that character's most recent action to timestamp (ISO 8601, read as UTC). Only the time changes; the action and its effects stay. It changes the order of the feed and the log, and inactivity checks read it. Undo: send the old time.
// @Tags GM
// @Param Authorization header string true "Bearer <token>"
// @Body Character and ISO timestamp
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
		Timestamp   string `json:"timestamp"` // the new time of the character's latest action (ISO 8601, read as UTC)
	}
	_, ok := gmReadAs(w, r, gmAgentID, &req, campaignFromTarget(&req.CharacterID), campaignBeforeStart())
	if !ok {
		return
	}
	ts, tsOK := parseClientTime(req.Timestamp) // action_log_2024.go
	if strings.TrimSpace(req.Timestamp) == "" || !tsOK {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_timestamp",
			"message": "Send timestamp as a time in RFC3339 form, such as 2026-11-15T16:05:00Z. Nothing was changed."})
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "Send character_id. Nothing was changed."})
		return
	}
	var err error

	// Update the most recent action's timestamp
	_, err = db.Exec(`
		UPDATE actions SET created_at = $1
		WHERE character_id = $2
		AND id = (SELECT id FROM actions WHERE character_id = $2 ORDER BY created_at DESC LIMIT 1)
	`, ts.UTC().Format("2006-01-02 15:04:05.999999"), req.CharacterID)
	if err != nil {
		writeDBWriteError(w, err, "the action's time")
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Action timestamp updated",
	})
}

// handleGMUpdateNarrationTime allows GM to fix narration timestamps
// @Summary Update narration timestamp by matching text
// @Description A repair tool for the GM: sets the time of every narration in your campaign whose text contains text_match to timestamp (ISO 8601, read as UTC); rows_updated says how many. Use a match that is unique. Only the time changes. Undo: send the old time with the same match.
// @Tags GM
// @Param Authorization header string true "Bearer <token>"
// @Body Text to match and new timestamp
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
		TextMatch  string `json:"text_match"` // text the narrations to change contain (use a unique match)
		Timestamp  string `json:"timestamp"`  // their new time (ISO 8601, read as UTC)
		CampaignID int    `json:"-"`
	}
	campaignID, ok := gmReadAs(w, r, gmAgentID, &req, campaignBeforeStart())
	if !ok {
		return
	}
	req.CampaignID = campaignID
	ts, tsOK := parseClientTime(req.Timestamp) // action_log_2024.go
	if strings.TrimSpace(req.Timestamp) == "" || !tsOK {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_timestamp",
			"message": "Send timestamp as a time in RFC3339 form, such as 2026-11-15T16:05:00Z. Nothing was changed."})
		return
	}
	if strings.TrimSpace(req.TextMatch) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "text_match_required",
			"message": "Send text_match: words from the narration to change. Nothing was changed."})
		return
	}

	// Update the action matching the text
	result, err := db.Exec(`
		UPDATE actions SET created_at = $1
		WHERE lobby_id = $2 AND description LIKE '%' || $3 || '%'
		AND action_type = 'narration'
	`, ts.UTC().Format("2006-01-02 15:04:05.999999"), req.CampaignID, req.TextMatch)
	if err != nil {
		writeDBWriteError(w, err, "the narration's time")
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
	CampaignID    int       `json:"campaign_id"`
	Narration     string    `json:"narration"` // the narration posted to the campaign
	MonsterAction *struct { // optional: a monster action to resolve: {monster, action, target or target_ids, use, point, apply_damage, ally_adjacent}
		Monster      string `json:"monster"`
		Action       string `json:"action"`
		Target       string `json:"target"`
		Description  string `json:"description"`
		ApplyDamage  bool   `json:"apply_damage"`  // deal the damage of a hit to the target character
		TargetIDs    []int  `json:"target_ids"`    // characters in a saving throw effect (breath weapon, spell ...)
		Use          string `json:"use"`           // a choice the entry needs: Command's word (approach, drop, flee, grovel, halt)
		Point        []int  `json:"point"`         // [x, y]: the grid square an area is aimed at on the battle map (a Sphere's center, a Cone's or Line's direction)
		AllyAdjacent bool   `json:"ally_adjacent"` // an ally of the monster (not Incapacitated) is within 5 feet of the target: Pack Tactics without a battle map
	} `json:"monster_action"`
	AdvanceTurn  bool          `json:"advance_turn"`  // true: end the current turn after the narration (as POST .../combat/next)
	ExpectedTurn *expectedTurn `json:"expected_turn"` // with advance_turn: the turn to end ({round, turn_index}); a retry after it ended is turn_already_moved
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

// recordGMAction only reports success after the action is durably inserted.
// A failed insert must not be presented to a GM as a completed narration.
func recordGMAction(database *sql.DB, campaignID int, actionType, description, result string) error {
	err := writeAction(database, campaignID, 0, actionType, description, result)
	return err
}

// handleGMNarrate godoc
// @Summary Submit GM narration and monster actions
// @Description GM submits narrative text and optionally runs a monster action. Before the campaign starts (recruiting) a narration sets the opening scene; monster actions and advance_turn wait for the start (campaign_not_active). Attacks resolve mechanically; positioning beats (for example, reposition) are narration-only. monster_action: {"monster", "action" (an action of its stat block, "Multiattack", or a named attack), "target" (a character's name or id, or a companion's name, nickname or combat id), "target_ids" (characters in a saving throw effect), "use" (a choice the entry needs, such as Command's word: approach, drop, flee, grovel or halt; or a spell the entry offers, such as the Pit Fiend's "Hold Monster" or one of a Lich's spells), "point": [x, y] (aims an area at a grid square; every character in a monster's area on the map is affected, listed or not), "apply_damage": true}. A Multiattack also resolves the actions without an attack roll it names (a Vampire's Bite). advance_turn: true ends the current turn after the narration (as POST /api/campaigns/{id}/combat/next); with it, optional expected_turn {"round": R, "turn_index": I} names the turn to end, as GET /api/campaigns/{id}/combat shows it (round, current_turn_index; GET /api/gm/status: combat.round, combat.current_turn_index). Once that turn has ended (a retry after the first request finished) the reply is 409 turn_already_moved with the turn it is now (round, turn_index, current_turn, current_turn_id), narration_recorded false: nothing is recorded or changed.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param campaign_id query int false "Active campaign to narrate; body campaign_id is also accepted. Required when the GM runs more than one."
// @Body Narration, an optional monster action, advance_turn and expected_turn (the turn advance_turn ends)
// @Success 200 {object} map[string]interface{} "Narration recorded, action resolved"
// @Failure 400 {object} map[string]interface{} "Missing, conflicting, or unavailable campaign ID"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Router /gm/narrate [post]
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
	campaignID, ok := gmReadAs(w, r, agentID, &req, campaignBeforeStart()) // campaign_id in the query or the body picks the campaign (gmCampaignFor); the opening scene may come before the start (playtest I-GM-10)
	if !ok {
		return
	}
	var status string
	if answerDBFailure(w, db.QueryRow(`SELECT status FROM lobbies WHERE id = $1`, campaignID).Scan(&status), "the campaign") {
		return
	}
	if status != "active" && (req.MonsterAction != nil || req.AdvanceTurn) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "campaign_not_active",
			"message": fmt.Sprintf("Campaign %d is %s: before the start a narration sets the scene, but monster actions and turns wait until the campaign starts (POST /api/campaigns/%d/start). Nothing was changed.", campaignID, status, campaignID)})
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

	if req.ExpectedTurn != nil && !req.AdvanceTurn {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_expected_turn",
			"message": "expected_turn names the turn advance_turn ends: send it with \"advance_turn\": true. Nothing was changed."})
		return
	}
	named, ok := namedTurn(w, campaignID, req.ExpectedTurn)
	if !ok {
		return
	}
	if req.AdvanceTurn { // a turn change refused anyway refuses the whole request before the narration is recorded
		if t, code := precheckTurn(campaignID, turnHow{expect: named}); code != "" {
			answerTurnRefusal(w, campaignID, t, code, map[string]interface{}{"narration_recorded": false})
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
		} else if text, info, ok := resolveMonsterAttack(withAllySaid(r, req.MonsterAction.AllyAdjacent), campaignID, req.MonsterAction.Monster, req.MonsterAction.Action, req.MonsterAction.Target, req.MonsterAction.ApplyDamage); ok {
			// the stat block's own attack bonus and damage, against the target's Armor Class
			result = text
			response["monster_attack"] = info
		} else {
			// no action of that name in the monster's stat block: nothing is rolled (the server used to make up an attack)
			result = "No action of that name in the stat block of " + req.MonsterAction.Monster + "; nothing was rolled."
			ids := monsterIDsByKeyOrName(campaignID, req.MonsterAction.Monster)
			alive := 0
			for _, id := range ids {
				for _, e := range combatEntries(campaignID) {
					if asInt(e["id"]) == id && asInt(e["hp"]) > 0 {
						alive++
					}
				}
			}
			if len(ids) > 0 && alive == 0 { // the monster is there but at 0 Hit Points: it takes no actions (not "no action of that name")
				result = req.MonsterAction.Monster + " is at 0 Hit Points: it takes no actions; nothing was rolled."
				response["monster_down"] = true
			} else if len(ids) > 0 {
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

	// Advance turn if requested: the one turn change (turn_advance_2024.go), with every hook of the turn that ends and the one that
	// starts, and a fresh turn clock (the previous turn's clock made the next player look overdue at once).
	if req.AdvanceTurn {
		end, _, err := gmEndsTurn(campaignID, named) // with the turn schedule, a player's turn ends only as the schedule allows
		if answerDBFailure(w, err, "the campaign's turn schedule") {
			return
		}
		if end.Reply != nil {
			end.Reply["narration_recorded"] = response["narration_recorded"]
			writeJSONStatus(w, http.StatusConflict, end.Reply)
			return
		}
		t, code := advanceTurn(campaignID, turnHow{by: "the GM's narration advanced the turn order", expect: end.Expect})
		if code != "" {
			answerTurnRefusal(w, campaignID, t, code, map[string]interface{}{"narration_recorded": response["narration_recorded"]})
			return
		}
		for k, v := range t.reply() {
			if k != "success" {
				response[k] = v
			}
		}
		response["action_economy_reset_for"] = t.NextName
		response["turn_advanced"] = true
	}

	json.NewEncoder(w).Encode(response)
}

type gmNudgeRequest struct {
	CharacterID int    `json:"character_id"`
	Message     string `json:"message"` // optional: what the reminder says
}

// handleGMNudge godoc
// @Summary Send a turn reminder to a player
// @Description GM can nudge a player to take their turn. Sends an email reminder with game context.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Body The character (its campaign must be an active one you run); campaign_id is optional
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
	// the character's campaign, which must be an active one the GM runs (gm_campaign_2024.go)
	campaignID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID))
	if !ok {
		return
	}
	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "character_id required",
		})
		return
	}

	var campaignName string
	db.QueryRow(`SELECT name FROM lobbies WHERE id = $1`, campaignID).Scan(&campaignName)

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
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": "Character not in this campaign",
		})
		return
	}

	// Get the last few actions for context
	recs, _ := readActions(actionFilter{lobby: campaignID, newestFirst: true, limit: 3}) // action_log_2024.go
	recentActions := []string{}
	for _, a := range recs {
		if a.Result != "" {
			recentActions = append(recentActions, fmt.Sprintf("- %s: %s → %s", a.Actor, a.Description, playerText(a.Result)))
		} else {
			recentActions = append(recentActions, fmt.Sprintf("- %s: %s", a.Actor, a.Description))
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
	writeAction(db, campaignID, 0, "gm_nudge", fmt.Sprintf("Nudged %s: %s", charName, customMsg), actionResult)

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

// sendNudgeEmail sends a turn reminder email to a player (through sendMail, mail_2024.go). With no mail service set up it
// sends nothing and answers nil: the reminder is still recorded in the campaign feed.
func sendNudgeEmail(toEmail, charName, campaignName, body string) error {
	err := sendMail(toEmail, fmt.Sprintf("⚔️ %s, it's your turn in %s!", charName, campaignName), body)
	if err == errMailNotConfigured {
		return nil
	}
	if err == nil {
		log.Printf("Nudge email sent to %s for character %s", toEmail, charName)
	}
	return err
}
