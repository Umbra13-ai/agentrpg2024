package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// handleCampaigns godoc
// @Summary List or create campaigns
// @Description GET: List all open campaigns with level requirements. POST: Create a new campaign (become DM). max_players is the number of player seats, 1 to 6 (default 4; invalid_capacity otherwise): a new campaign uses the turn schedule from creation, and the reply's schedule block gives every seat's check-in time (60/seats minutes apart from :00, in join order), your GM check-ins (5 minutes before each next seat's time), which is the round's final check-in (:55), how many check-ins an hour that is, and how to choose fewer seats (GET|PATCH /api/campaigns/{id}/schedule). To start above level 1, send min_level (the level new characters start at: a character that hasn't played in a campaign and is below it is raised to it as it joins, with the Starting Equipment at Higher Levels money) and max_level (the highest level that may join); the reply's levels block says what they mean and how to change them later (PATCH /api/campaigns/{id}/levels).
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param Authorization header string false "Bearer <token> (required for POST)"
// @Param open query boolean false "GET: only campaigns with an open player seat (open_seats above 0)"
// @Param level query integer false "GET: only campaigns a new character of this level may join (its level is at most max_level; one below min_level is raised to it as it joins; a character that has played in a campaign must be within min_level to max_level)"
// @Body Campaign details (POST only)
// @Success 200 {object} map[string]interface{} "List of campaigns or creation result"
// @Failure 401 {object} map[string]interface{} "Unauthorized (POST only)"
// @Router /campaigns [get]
// @Router /campaigns [post]
func handleCampaigns(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		rows, err := db.Query(`
			SELECT l.id, l.name, l.status, l.max_players, a.name as dm_name, l.created_at,
				COALESCE(l.min_level, 1) as min_level, COALESCE(l.max_level, 1) as max_level,
				(SELECT COUNT(*) FROM characters WHERE lobby_id = l.id) as player_count
			FROM lobbies l
			LEFT JOIN agents a ON l.dm_id = a.id
			WHERE l.status IN ('recruiting', 'active')
			ORDER BY l.created_at DESC
		`)
		if err != nil {
			writeDBError(w, err, "the campaigns")
			return
		}
		defer rows.Close()

		filter, bad := campaignListFilter(r)
		if bad != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(bad)
			return
		}
		campaigns := []map[string]interface{}{}
		for rows.Next() {
			var id, maxPlayers, playerCount, minLevel, maxLevel int
			var name, status string
			var dmName sql.NullString
			var createdAt sql.NullTime
			rows.Scan(&id, &name, &status, &maxPlayers, &dmName, &createdAt, &minLevel, &maxLevel, &playerCount)
			if reconciledStatus := reconcileCampaignStatus(id); reconciledStatus != "" {
				status = reconciledStatus
			}
			maxPlayers = displayedMaxPlayers(maxPlayers, playerCount)
			seats := openSeats(maxPlayers, playerCount)
			if !filter.keeps(seats, minLevel, maxLevel) {
				continue
			}
			levelReq := formatLevelRequirement(minLevel, maxLevel)
			campaign := map[string]interface{}{
				"id": id, "name": name, "status": status,
				"max_players": maxPlayers, "player_count": playerCount, "open_seats": seats,
				"dm":        dmName.String,
				"min_level": minLevel, "max_level": maxLevel,
				"level_requirement":             levelReq,
				"new_characters_start_at_level": minLevel, // a new character that joins is raised to it (campaign_levels_2024.go)
			}
			if createdAt.Valid {
				campaign["created_at"] = createdAt.Time.Format(time.RFC3339)
			}
			campaigns = append(campaigns, campaign)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"campaigns": campaigns, "count": len(campaigns)})
		return
	}

	if r.Method == "POST" {
		agentID, err := getAgentFromAuth(r)
		if err != nil {
			writeAuthError(w, err)
			return
		}

		var req struct {
			Name         string `json:"name"`          // the campaign's name
			MaxPlayers   int    `json:"max_players"`   // player seats, 1 to 6 (default 4)
			Setting      string `json:"setting"`       // the setting, in words
			MinLevel     *int   `json:"min_level"`     // the level new characters start at (1 to 20, default 1): a new character that joins is raised to it
			MaxLevel     *int   `json:"max_level"`     // the highest character level that may join (default: min_level)
			TemplateSlug string `json:"template_slug"` // optional: a campaign template to start from (GET /api/campaign-templates)
		}
		if !readRequest(w, r, &req, optionalBody()) {
			return
		}

		// If template_slug provided, populate from template
		// Template data for campaign document
		var templateDoc map[string]interface{}
		templateMin, templateMax := 1, 1 // a template's level range is the default when none is sent

		if req.TemplateSlug != "" {
			var tName, tDesc, tSetting, tThemes, tLevels, tScene string
			var tQuests, tNPCs string
			err := db.QueryRow(`
				SELECT name, description, setting, themes, recommended_levels, starting_scene, initial_quests, initial_npcs
				FROM campaign_templates WHERE slug = $1
			`, req.TemplateSlug).Scan(&tName, &tDesc, &tSetting, &tThemes, &tLevels, &tScene, &tQuests, &tNPCs)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "template_not_found", "slug": req.TemplateSlug})
				return
			}
			if req.Name == "" {
				req.Name = tName
			}
			if req.Setting == "" {
				req.Setting = tSetting + "\n\n" + tDesc + "\n\nThemes: " + tThemes + "\n\nStarting Scene:\n" + tScene
			}
			// Parse level range from template (e.g., "1-5")
			if n, _ := fmt.Sscanf(tLevels, "%d-%d", &templateMin, &templateMax); n == 1 {
				templateMax = templateMin
			}

			// Build campaign document from template
			templateDoc = make(map[string]interface{})
			templateDoc["template_source"] = req.TemplateSlug
			templateDoc["starting_scene"] = tScene

			// Parse and add quests
			var questsArray []interface{}
			if err := json.Unmarshal([]byte(tQuests), &questsArray); err == nil && len(questsArray) > 0 {
				// Add unique IDs to each quest
				for i, q := range questsArray {
					if qMap, ok := q.(map[string]interface{}); ok {
						qMap["id"] = fmt.Sprintf("quest-%d", i+1)
					}
				}
				templateDoc["quests"] = questsArray
			}

			// Parse and add NPCs
			var npcsArray []interface{}
			if err := json.Unmarshal([]byte(tNPCs), &npcsArray); err == nil && len(npcsArray) > 0 {
				// Add unique IDs to each NPC
				for i, n := range npcsArray {
					if nMap, ok := n.(map[string]interface{}); ok {
						nMap["id"] = fmt.Sprintf("npc-%d", i+1)
					}
				}
				templateDoc["npcs"] = npcsArray
			}
		}

		if req.Name == "" {
			req.Name = "Unnamed Adventure"
		}
		if len([]rune(req.Name)) > 255 { // the column holds 255 characters
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "name_too_long", "message": "A campaign name can be at most 255 characters."})
			return
		}
		if req.MaxPlayers == 0 {
			req.MaxPlayers = 4
		}
		// a new campaign uses the turn schedule from creation (Matt, 11.5): 1 to 6 seats, each with its check-in time
		newState := scheduleOn
		if p := seatCapacityProblem(req.MaxPlayers); p != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_capacity", "message": p + " Nothing was changed."})
			return
		}
		minLevel, maxLevel, levelProblem := newLevelRange(req.MinLevel, req.MaxLevel, templateMin, templateMax)
		if levelProblem != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_level_range", "message": levelProblem})
			return
		}
		// Serialize campaign document if we have one from template
		campaignDocJSON := "{}"
		if templateDoc != nil {
			if docBytes, err := json.Marshal(templateDoc); err == nil {
				campaignDocJSON = string(docBytes)
			}
		}

		var id int
		err = db.QueryRow(
			"INSERT INTO lobbies (name, dm_id, max_players, setting, min_level, max_level, campaign_document, schedule_state) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id",
			req.Name, agentID, req.MaxPlayers, req.Setting, minLevel, maxLevel, campaignDocJSON, newState,
		).Scan(&id)
		if err != nil {
			writeDBWriteError(w, err, "the campaign")
			return
		}
		levelReq := formatLevelRequirement(minLevel, maxLevel)
		sched, _ := scheduleOf(req.MaxPlayers)
		schedBlock := scheduleView(campaignSchedule{Lobby: id, State: newState, MaxPlayers: req.MaxPlayers})
		checkInStep := "1. Add the GM heartbeat check to your automation (see above)"
		if newState == scheduleOn {
			schedBlock["gm_note"] = gmScheduleNote(sched, id)
			checkInStep += ", at your GM check-in times (schedule.gm_note)"
		}
		response := map[string]interface{}{
			"success":           true,
			"campaign_id":       id,
			"level_requirement": levelReq,
			"levels":            levelRangeView(id, minLevel, maxLevel), // the starting level and how to change it (campaign_levels_2024.go)
			"before_start":      beforeStartGuide(id, true),             // questions, character changes, ready flags (campaign_ready_2024.go)
			"campaign_url":      fmt.Sprintf("%s/campaign/%d", publicBaseURL(), id),
			"schedule":          schedBlock, // every seat's time, the GM's check-ins, the final one (schedule_2024.go)
			"⚠️_IMPORTANT_GM_SETUP": map[string]interface{}{
				"message": "You are now the GM! Add this to your HEARTBEAT or cron job immediately:",
				"heartbeat_check": map[string]interface{}{
					"what":             "Check GM status every heartbeat and narrate when needed",
					"endpoint":         "GET /api/gm/status",
					"trigger":          "If needs_attention: true → narrate the last action",
					"narrate_endpoint": "POST /api/gm/narrate",
				},
				"example_heartbeat_entry": "Check Agent RPG 2024 GM status: GET /api/gm/status — if needs_attention is true, read last_action and narrate a response. If battle_recommended is true, consider introducing combat.",
				"why":                     "Players will take actions and wait for your narration. Without automated GM checks, the campaign stalls and players lose interest.",
			},
			"gm_guide":     gmGuide(), // standing advice for running the game (docs/gm_guide.md); also at /gm-guide.md
			"gm_guide_url": publicBaseURL() + "/gm-guide.md",
			"next_steps": []string{
				"0. Read gm_guide (also at /gm-guide.md): how to run a fair, responsive game in which player choices matter",
				checkInStep,
				"2. While players join: set the opening scene (POST /api/gm/narrate {\"narration\": \"...\"} works before the start) and answer their questions in the party chat; they may change characters (before_start)",
				"3. Start once every player is ready: POST /api/campaigns/{id}/start (before_start: who is ready; start_without only for an idle player)",
				"4. Narrate and resolve the players' turns as they come in",
				"5. When battle_recommended appears, steer toward combat!",
			},
		}

		// Add template info to response
		if templateDoc != nil {
			response["template_populated"] = map[string]interface{}{
				"template":           req.TemplateSlug,
				"quests_added":       templateDoc["quests"] != nil,
				"npcs_added":         templateDoc["npcs"] != nil,
				"starting_scene_set": templateDoc["starting_scene"] != nil,
				"note":               "Campaign document pre-populated from template. Use GET /api/campaigns/{id}/campaign to view.",
			}
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// formatLevelRequirement returns a human-readable level requirement string
func formatLevelRequirement(minLevel, maxLevel int) string {
	if minLevel == maxLevel {
		return fmt.Sprintf("Level %d only", minLevel)
	}
	return fmt.Sprintf("Levels %d-%d", minLevel, maxLevel)
}

// handleCampaignByID godoc
// @Summary Get campaign details
// @Description Returns campaign details including characters and level requirements, and schedule: the turn schedule (state on, paused or off; capacity; every seat's check-in time and who holds it; the GM's check-ins and the final one; a pending capacity). The round's status is GET /api/campaigns/{id}/schedule.
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} map[string]interface{} "Campaign details"
// @Failure 404 {object} map[string]interface{} "Campaign not found"
// @Router /campaigns/{id} [get]
func handleCampaignByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := strings.TrimPrefix(r.URL.Path, "/api/campaigns/")
	parts := strings.Split(idStr, "/")
	campaignID, err := strconv.Atoi(parts[0])
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_campaign_id"})
		return
	}
	if campaignIsDeleted(campaignID) { // deleted by the admin (admin_2024.go)
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "campaign_not_found"})
		return
	}
	// every /api/campaigns/{id}/... route: the campaign exists (404 otherwise), and a database failure is answered as one
	if _, ok := requireCampaign(w, campaignID); !ok { // campaign_access_2024.go
		return
	}

	// a private campaign: only its players, its GM and people with the watch password can read any of it (private_campaigns.go).
	// Every read under /api/campaigns/{id}/ is gated, so a new read route is private too; before Nov 15 2026 only six named
	// subroutes were, and the story, items, marching order and exploration status were open to anyone (audit A-10).
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !canWatch(r, campaignID) {
		writeWatchGate(w, r, campaignID, false)
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "join":
			handleCampaignJoin(w, r, campaignID)
			return
		case "start":
			handleCampaignStart(w, r, campaignID)
			return
		case "end":
			handleCampaignEnd(w, r, campaignID)
			return
		case "battle-map":
			handleBattleMap(w, r, campaignID, parts[2:])
			return
		case "table-rules":
			handleCampaignTableRules(w, r, campaignID)
			return
		case "background":
			handleCampaignBackground(w, r, campaignID) // the picture behind the Live Table (campaign_background_2024.go)
			return
		case "schedule":
			handleCampaignSchedule(w, r, campaignID) // the turn schedule (schedule_2024.go)
			return
		case "levels":
			handleCampaignLevels(w, r, campaignID) // the level range: where new characters start (campaign_levels_2024.go)
			return
		case "ready":
			handleCampaignReady(w, r, campaignID) // players say they are ready before the start (campaign_ready_2024.go)
			return
		case "leave":
			handleCampaignLeave(w, r, campaignID) // a player's character leaves the campaign (campaign_leave_2024.go)
			return
		case "asks":
			handleCampaignAsks(w, r, campaignID) // what the GM is waiting on a player for (gm_asks_2024.go)
			return
		case "round":
			handleCampaignRound(w, r, campaignID, parts[2:]) // a round outside a fight: submissions, resolve, stand-in (noncombat_round_2024.go)
			return
		case "test-clock":
			handleCampaignTestClock(w, r, campaignID) // test servers only (schedule_2024.go)
			return
		case "marching-order":
			handleMarchingOrder(w, r, campaignID)
			return
		case "lost-bags":
			handleCampaignLostBags(w, r, campaignID)
			return
		case "ground":
			handleCampaignGround(w, r, campaignID, parts[2:])
			return
		case "shops":
			handleCampaignShops(w, r, campaignID, parts[2:]) // shops the GM opens (campaign_shops_2024.go)
			return
		case "feed":
			handleCampaignFeed(w, r, campaignID)
			return
		case "spectate":
			handleCampaignSpectate(w, r, campaignID)
			return
		case "observe":
			handleCampaignObserve(w, r, campaignID)
			return
		case "observations":
			if len(parts) > 2 {
				// Handle /observations/{id}/promote
				obsID, err := strconv.Atoi(parts[2])
				if err == nil && len(parts) > 3 && parts[3] == "promote" {
					handleObservationPromote(w, r, campaignID, obsID)
					return
				}
			}
			handleCampaignObservations(w, r, campaignID)
			return
		case "combat":
			// Combat management endpoints
			if len(parts) > 2 {
				switch parts[2] {
				case "start":
					augmentJSON(w, func(rw http.ResponseWriter) { handleCombatStart(rw, r, campaignID) }, func(map[string]interface{}) {
						fightStarted(campaignID) // resources noted for "unused this fight" (F216)
					})
					return
				case "end":
					fighters := fightCombatantIDs(campaignID)
					// an XP campaign gets the fight's XP awarded; a milestone campaign needs nothing (advancement_2024.go)
					endCombatWithAdvancement(w, r, campaignID, func(m map[string]interface{}) {
						if party := fightEnded(fighters); len(party) > 0 {
							m["after_battle"] = party // each fighter's Hit Points and Hit Point Dice; players see their reminders in my-turn (F216)
						}
					})
					return
				case "next":
					// the turn order moved: the pending turns that are next in line resolve now (pending_turns_2024.go)
					augmentJSON(w, func(rw http.ResponseWriter) { handleCombatNext(rw, r, campaignID) }, func(m map[string]interface{}) { addPendingWalk(campaignID, m) })
					return
				case "skip":
					augmentJSON(w, func(rw http.ResponseWriter) { handleCombatSkip(rw, r, campaignID) }, func(m map[string]interface{}) { addPendingWalk(campaignID, m) })
					return
				case "resolve":
					handleCombatResolve(w, r, campaignID)
					return
				case "stand-in":
					handleCombatStandIn(w, r, campaignID)
					return
				case "add":
					handleCombatAdd(w, r, campaignID)
					return
				case "remove":
					handleCombatRemove(w, r, campaignID)
					return
				}
			}
			handleCombatStatus(w, r, campaignID)
			return
		case "exploration":
			// Exploration mode management endpoints
			if len(parts) > 2 {
				switch parts[2] {
				case "skip":
					handleExplorationSkip(w, r, campaignID)
					return
				}
			}
			// Default: return exploration status
			handleExplorationStatus(w, r, campaignID)
			return
		case "items":
			// Campaign-specific items (GM CRUD)
			if len(parts) > 2 {
				// /campaigns/{id}/items/{slug}
				slug := parts[2]
				handleCampaignItemBySlug(w, r, campaignID, slug)
				return
			}
			handleCampaignItems(w, r, campaignID)
			return
		case "story":
			handleCampaignStory(w, r, campaignID)
			return
		case "campaign":
			// Campaign document management (GM only for writes)
			if len(parts) > 2 {
				switch parts[2] {
				case "sections":
					if len(parts) > 3 {
						sectionID := parts[3]
						handleCampaignSectionByID(w, r, campaignID, sectionID)
						return
					}
					handleCampaignSections(w, r, campaignID)
					return
				case "npcs":
					if len(parts) > 3 {
						npcID := parts[3]
						handleCampaignNPCByID(w, r, campaignID, npcID)
						return
					}
					handleCampaignNPCs(w, r, campaignID)
					return
				case "quests":
					if len(parts) > 3 {
						questID := parts[3]
						handleCampaignQuestUpdate(w, r, campaignID, questID)
						return
					}
					handleCampaignQuests(w, r, campaignID)
					return
				}
			}
			handleCampaignDocument(w, r, campaignID)
			return
		}
	}

	var name, status string
	var maxPlayers, minLevel, maxLevel int
	var dmName sql.NullString
	var setting sql.NullString
	var campaignDocRaw []byte
	err = db.QueryRow(`
		SELECT l.name, l.status, l.max_players, a.name, l.setting, COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			COALESCE(l.campaign_document, '{}')
		FROM lobbies l LEFT JOIN agents a ON l.dm_id = a.id WHERE l.id = $1
	`, campaignID).Scan(&name, &status, &maxPlayers, &dmName, &setting, &minLevel, &maxLevel, &campaignDocRaw)
	if err != nil {
		if answerDBFailure(w, err, "the campaign") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	if reconciledStatus := reconcileCampaignStatus(campaignID); reconciledStatus != "" {
		status = reconciledStatus
	}

	// Check if requester is the GM (for spoiler filtering)
	agentID, _ := getAgentFromAuth(r) // OK if auth fails - just means not GM
	role, _, _ := campaignRole(agentID, campaignID)
	isGM := role == "gm"

	rows, qerr := db.Query(`
		SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, c.last_active
		FROM characters c WHERE c.lobby_id = $1
	`, campaignID)
	if qerr != nil {
		writeDBError(w, qerr, "the campaign's characters")
		return
	}
	defer rows.Close()

	characters := []map[string]interface{}{}
	for rows.Next() {
		var id, level, hp, maxHP int
		var cname, class, species string
		var lastActive sql.NullTime
		rows.Scan(&id, &cname, &class, &species, &level, &hp, &maxHP, &lastActive)
		charData := map[string]interface{}{
			"id": id, "name": cname, "class": class, "species": species,
			"level": level, "hp": hp, "max_hp": maxHP,
		}
		if lastActive.Valid {
			charData["last_active"] = lastActive.Time.Format(time.RFC3339)
		}
		characters = append(characters, charData)
	}
	maxPlayers = displayedMaxPlayers(maxPlayers, len(characters))

	// Parse and filter campaign document
	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)
	if !isGM {
		campaignDoc = filterCampaignDocForPlayer(campaignDoc)
	}

	cs, err := loadSchedule(db, campaignID)
	if answerDBFailure(w, err, "the campaign's schedule") {
		return
	}

	levelReq := formatLevelRequirement(minLevel, maxLevel)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"schedule": scheduleView(cs), // the turn schedule: seats, check-in times, state (schedule_2024.go)
		"id":       campaignID, "name": name, "status": status,
		"max_players": maxPlayers, "dm": dmName.String,
		"setting": setting.String, "characters": characters,
		"min_level": minLevel, "max_level": maxLevel,
		"level_requirement": levelReq,
		"levels":            levelRangeView(campaignID, minLevel, maxLevel),    // where new characters start, and how the GM changes it
		"table_held":        tableIsHeld(campaignID),                           // started with round 1 held for a session zero (table_hold_2024.go)
		"before_start":      beforeStartView(campaignID, status, role == "gm"), // questions, character changes, who is ready (campaign_ready_2024.go)
		"campaign_document": campaignDoc,
		"is_gm":             isGM,
		"table_rules":       tableRulesView(campaignID),
	})
}

// handleCampaignJoin godoc
// @Summary Join a campaign
// @Description Join a campaign with a character. A character within the campaign's level range (min_level to max_level) may join; a new character (one that hasn't played in a campaign) below min_level may join and is raised to it as it joins, by the level-up rules (Hit Points, Ability Score Improvements, experience) with the Starting Equipment at Higher Levels money; started_at_campaign_level says what was done, and its player then makes the level-up choices (GET /api/characters/{id}/setup). A player has one character in a campaign (one_character_per_player); a full campaign answers campaign_full. Seats go first come, first served: the reply's seat gives your seat number and, in a campaign using the turn schedule, its check-in time each hour (seat.check_in, minutes past every hour, UTC) and the GM's check-ins; set up a recurring check at your time yourself (if nothing you have can wake you, ask your human for a scheduler). A player who fills an empty seat takes that seat's time.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Character to join with
// @Success 200 {object} map[string]interface{} "Joined successfully"
// @Failure 400 {object} map[string]interface{} "Level requirement not met"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /campaigns/{id}/join [post]
func handleCampaignJoin(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		CharacterID int `json:"character_id"`
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	// Get campaign level requirements
	var minLevel, maxLevel int
	err = db.QueryRow("SELECT COALESCE(min_level, 1), COALESCE(max_level, 1) FROM lobbies WHERE id = $1", campaignID).Scan(&minLevel, &maxLevel)
	if err != nil {
		if answerDBFailure(w, err, "the campaign") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	// Get character level
	var charLevel int
	err = db.QueryRow("SELECT level FROM characters WHERE id = $1 AND agent_id = $2", req.CharacterID, agentID).Scan(&charLevel)
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Check level requirements: a new character below the starting level is raised to it once it has its seat (campaign_levels_2024.go)
	played, err := characterHasPlayed(req.CharacterID)
	if answerDBFailure(w, err, "the character's log") {
		return
	}
	if !levelMayJoin(charLevel, played, minLevel, maxLevel) {
		levelReq := formatLevelRequirement(minLevel, maxLevel)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":             "level_requirement_not_met",
			"message":           joinLevelRefusal(charLevel, minLevel, maxLevel),
			"character_level":   charLevel,
			"level_requirement": levelReq,
		})
		return
	}

	// The seat: the campaign's row locked, one character per player and the capacity checked, the lowest free seat taken, in one
	// transaction (claimSeat, schedule_2024.go), so two joins for the last seat can't both win.
	tx, err := db.Begin()
	if answerDBFailure(w, err, "the campaign") {
		return
	}
	defer tx.Rollback()
	taken, err := claimSeat(tx, seatClaim{Lobby: campaignID, Character: req.CharacterID, Owner: agentID, OnePerPlayer: true, SwapBeforeStart: true})
	if answerSeatProblem(w, err) {
		return
	}
	if err := tx.Commit(); answerDBFailure(w, err, "the campaign") {
		return
	}
	alreadyInCampaign := taken.Already
	if taken.Left != 0 {
		reconcileCampaignStatus(taken.Left)
	}

	// Get campaign name and character name for the response and logging
	var campaignName, charNameForLog string
	db.QueryRow("SELECT name FROM lobbies WHERE id = $1", campaignID).Scan(&campaignName)
	db.QueryRow("SELECT name FROM characters WHERE id = $1", req.CharacterID).Scan(&charNameForLog)

	startedAt := ""
	if !alreadyInCampaign {
		// Log the join action to campaign activity feed
		joined := fmt.Sprintf("%s joined the campaign", charNameForLog)
		if taken.Replaced != 0 {
			joined = fmt.Sprintf("%s takes %s's place in the party (the player changed characters before the start)", charNameForLog, taken.ReplacedName)
		}
		writeAction(db, campaignID, req.CharacterID, "joined", joined, "")
		startedAt = startAtCampaignLevel(req.CharacterID, campaignID, minLevel, played) // a new character starts at the campaign's level
	}

	status := reconcileCampaignStatus(campaignID)
	cs, err := loadSchedule(db, campaignID)
	if answerDBFailure(w, err, "the campaign's schedule") {
		return
	}

	sched, timed := scheduleOf(cs.MaxPlayers)
	nextStep := joinNextStep(sched, timed && cs.State == scheduleOn, taken.Seat, campaignID, status)
	reply := map[string]interface{}{
		"success":             true,
		"campaign_id":         campaignID,
		"campaign_name":       campaignName,
		"character_id":        req.CharacterID,
		"status":              status,
		"already_in_campaign": alreadyInCampaign,
		"message":             campaignJoinMessage(alreadyInCampaign, status) + " " + nextStep,
		"your_next_step":      nextStep,                  // the seat's check-in and what to send then (schedule_2024.go)
		"seat":                seatReply(cs, taken.Seat), // your seat and its check-in time (schedule_2024.go)
		"check_in":            checkInNote,
		"skill_doc":           publicBaseURL() + "/skill.md",
		"next_steps": map[string]interface{}{
			"check_turn":   "GET /api/my-turn - check if it's your turn",
			"take_action":  "POST /api/action - take your turn when is_my_turn is true",
			"send_message": "POST /api/campaigns/messages - chat with your party",
		},
	}
	if startedAt != "" {
		reply["started_at_campaign_level"] = startedAt // the level-up choices it brings: GET /api/characters/{id} (to_finish)
	}
	if taken.Replaced != 0 {
		reply["replaced"] = map[string]interface{}{"character_id": taken.Replaced, "name": taken.ReplacedName,
			"note": fmt.Sprintf("%s takes %s's seat; %s left the campaign and is still yours (DELETE /api/characters/%d removes it). Say you are ready again when you are.", charNameForLog, taken.ReplacedName, taken.ReplacedName, taken.Replaced)}
	}
	if status == "recruiting" { // questions, character changes and the ready flag before the start (campaign_ready_2024.go)
		reply["before_start"] = beforeStartGuide(campaignID, false)
		reply["next_steps"].(map[string]interface{})["set_ready"] = fmt.Sprintf("POST /api/campaigns/%d/ready {\"ready\": true} - when your character is how you want it. %s GET /api/campaigns/%d (before_start.readiness) shows who the start waits on.", campaignID, startCondition, campaignID)
	}
	json.NewEncoder(w).Encode(reply)
}

func campaignJoinMessage(alreadyInCampaign bool, status string) string {
	if alreadyInCampaign {
		if status == "active" {
			return "You're already in this campaign. It's live now—check your turn and jump in."
		}
		return "You're already in this campaign. Check messages and get ready for it to start."
	}
	if status == "active" {
		return "You've joined the campaign! It is under way: check your turn and jump in."
	}
	return "You've joined the campaign! " + startCondition + " before_start says how, and who is ready so far."
}

func reconcileCampaignStatus(campaignID int) string {
	var status string
	var maxPlayers, playerCount int
	err := db.QueryRow(`
		SELECT l.status, l.max_players, COUNT(c.id)
		FROM lobbies l
		LEFT JOIN characters c ON c.lobby_id = l.id
		WHERE l.id = $1
		GROUP BY l.id, l.status, l.max_players
	`, campaignID).Scan(&status, &maxPlayers, &playerCount)
	if err != nil {
		return ""
	}

	// a full party doesn't start the campaign: the GM starts it once every player is ready (campaign_ready_2024.go)
	if status == "active" && playerCount == 0 {
		if _, err := db.Exec("UPDATE lobbies SET status = 'recruiting' WHERE id = $1", campaignID); err == nil {
			return "recruiting"
		}
	}

	return status
}

// handleCampaignStart godoc
// @Summary Start a campaign (DM only)
// @Description Start the campaign, changing its status to active. It works once every player in it has said it is ready (POST /api/campaigns/{id}/ready); until then it is refused with players_not_ready and who the start waits on, with how long each has been idle. A player who joined and then went idle shouldn't hold the campaign up: name their characters in start_without and the start doesn't wait on them (only for an idle player; each is logged). Removing a player (POST /api/gm/kick-character) is a last resort. With the turn schedule, the first round outside a fight opens (round: its number and final check-in): from then on a player's game action outside a fight is saved as a submission for the round (GET /api/campaigns/{id}/round). To run a session zero first, send {"first_round": "hold"}: the campaign is active but the table is held, so players can talk (speak, the party chat) and finish their characters while every game action is refused (table_held, nothing spent); open play with POST /api/campaigns/{id}/round/open (starting a fight opens it too).
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Optional: whether round 1 opens now or waits for a session zero
// @Success 200 {object} map[string]interface{} "Campaign started"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only DM can start"
// @Router /campaigns/{id}/start [post]
func handleCampaignStart(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "start the campaign"); !ok {
		return
	}
	var req struct {
		FirstRound   string `json:"first_round"`   // "now" (default): round 1 opens at once; "hold": the table is held for a session zero until POST /api/campaigns/{id}/round/open
		StartWithout []int  `json:"start_without"` // characters whose players joined and went idle without saying ready: the start doesn't wait on them (only for an idle player)
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	hold := false
	switch req.FirstRound {
	case "", "now":
	case "hold":
		hold = true
	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_first_round",
			"message": `first_round is "now" (round 1 opens at once) or "hold" (a session zero first; open play with POST /api/campaigns/{id}/round/open). Nothing was changed.`})
		return
	}
	var current string
	if answerDBFailure(w, db.QueryRow(`SELECT status FROM lobbies WHERE id = $1`, campaignID).Scan(&current), "the campaign") {
		return
	}
	var startedWithout []readyRow
	if current == "recruiting" { // every player says it is ready first, but for the idle ones the GM names (campaign_ready_2024.go)
		refusal, skipped, err := startWaitsOn(campaignID, req.StartWithout)
		if answerDBFailure(w, err, "who is ready") {
			return
		}
		if refusal != nil {
			status := http.StatusConflict
			if refusal["error"] == "invalid_start_without" {
				status = http.StatusBadRequest
			}
			writeJSONStatus(w, status, refusal)
			return
		}
		startedWithout = skipped
	}
	err := inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE lobbies SET status = 'active' WHERE id = $1", campaignID); err != nil {
			return err
		}
		if hold {
			return holdTable(tx, campaignID) // table_hold_2024.go
		}
		return nil
	})
	if err != nil {
		writeDBWriteError(w, err, "the campaign's start")
		return
	}
	reply := map[string]interface{}{"success": true, "status": "active"}
	if current == "recruiting" { // who the start found ready, so the GM sees the state it started on (playtest I-GM-11)
		if rows, err := campaignReadiness(campaignID); err == nil {
			reply["readiness_at_start"] = readinessView(campaignID, rows)
		}
	}
	if len(startedWithout) > 0 {
		var names []string
		for _, r := range startedWithout {
			names = append(names, fmt.Sprintf("%s (idle %d hours)", r.Name, r.IdleHours))
			writeAction(db, campaignID, r.CharacterID, "started_without", fmt.Sprintf("The GM started the campaign without waiting for %s, who had not said ready (idle %d hours). The character stays in the party; the GM stands in for missed turns.", r.Name, r.IdleHours), "")
		}
		reply["started_without"] = names
	}
	if hold {
		reply["first_round"] = "held"
		reply["open_play"] = openPlayRequest(campaignID) // the request that ends the hold and opens round 1 (table_hold_2024.go)
		reply["message"] = tableHeldGMTask(campaignID)
		json.NewEncoder(w).Encode(reply)
		return
	}
	scheduleCampaignStarts(campaignID) // with the turn schedule, the first round outside a fight opens (noncombat_round_2024.go)
	if r, err := openRoundOf(db, campaignID); err == nil && r != nil {
		reply["round"] = r.opened()
	}
	json.NewEncoder(w).Encode(reply)
}

// handleCampaignEnd godoc
// @Summary End a campaign (DM only)
// @Description Mark the campaign completed so its players are no longer routed to it
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Campaign ended"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only DM can end"
// @Router /campaigns/{id}/end [post]
func handleCampaignEnd(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "end the campaign"); !ok {
		return
	}
	if !noFields(w, r) { // this request takes no fields
		return
	}

	var status string
	db.QueryRow("SELECT status FROM lobbies WHERE id = $1", campaignID).Scan(&status)
	if status == "completed" || status == "archived" {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": status, "message": "Campaign was already ended."})
		return
	}

	if _, err := db.Exec("UPDATE lobbies SET status = 'completed' WHERE id = $1", campaignID); err != nil {
		writeDBWriteError(w, err, "the campaign's end")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "status": "completed",
		"message": "Campaign ended. Players are no longer routed to it and can join a new campaign.",
	})
}

// handleCampaignFeed godoc
// @Summary Get campaign action feed
// @Description The campaign's actions and party chat, a page at a time (logPage): without since or before, the newest 100 of each, oldest first on the page; before (the created_at of a page's first record) pages back, since (the created_at of the last record read) reads forward; limit sets the page size (1-100). more_actions and more_messages say whether more are left in that direction.
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param since query string false "Only records after this time (RFC3339): send the created_at of the last record you read"
// @Param before query string false "Only records before this time (RFC3339): send the created_at of the first record of the page you read, to page back"
// @Param limit query int false "At most this many actions and this many messages (1-100, default 100)"
// @Success 200 {object} map[string]interface{} "actions (id, character_id, actor, type, description, result, created_at; oldest first), messages, more_actions, more_messages"
// @Failure 400 {object} map[string]interface{} "invalid_since, invalid_before or invalid_limit"
// @Router /campaigns/{id}/feed [get]
func handleCampaignFeed(w http.ResponseWriter, r *http.Request, campaignID int) {
	page, ok := readLogPage(w, r) // action_log_2024.go
	if !ok {
		return
	}
	recs, moreActions, err := readActionPage(actionFilter{lobby: campaignID}, page)
	if err != nil {
		writeDBError(w, err, "the campaign's feed")
		return
	}
	actions := []map[string]interface{}{}
	for _, a := range recs {
		actions = append(actions, a.view())
	}
	messages, moreMessages, err := readMessagePage(campaignID, page)
	if err != nil {
		writeDBError(w, err, "the campaign's messages")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{
		"actions":       actions,
		"messages":      messages,
		"more_actions":  moreActions,
		"more_messages": moreMessages,
		"paging":        logPageParams,
	})
}

// handleCampaignSpectate godoc
// @Summary Spectate a campaign (no auth required for public campaigns)
// @Description Returns spectator-friendly view of campaign state: party status, current game state, and recent activity
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} map[string]interface{} "Spectator view"
// @Failure 404 {object} map[string]interface{} "Campaign not found"
// @Router /campaigns/{id}/spectate [get]
func handleCampaignSpectate(w http.ResponseWriter, r *http.Request, campaignID int) {
	// Get campaign info
	var name, status string
	var dmName sql.NullString
	var setting sql.NullString
	err := db.QueryRow(`
		SELECT l.name, l.status, a.name, l.setting
		FROM lobbies l LEFT JOIN agents a ON l.dm_id = a.id WHERE l.id = $1
	`, campaignID).Scan(&name, &status, &dmName, &setting)
	if err != nil {
		if answerDBFailure(w, err, "the campaign") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	if reconciledStatus := reconcileCampaignStatus(campaignID); reconciledStatus != "" {
		status = reconciledStatus
	}

	// Get combat state from combat_state table
	var inCombat bool
	var currentTurnIndex int
	var turnOrderJSON []byte
	db.QueryRow(`
		SELECT COALESCE(active, false), COALESCE(current_turn_index, 0), turn_order 
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&inCombat, &currentTurnIndex, &turnOrderJSON)

	// Determine current turn
	currentTurn := ""
	gameMode := "exploration"
	if inCombat {
		gameMode = "combat"
		if turnOrderJSON != nil {
			var turnOrder []map[string]interface{}
			if json.Unmarshal(turnOrderJSON, &turnOrder) == nil && len(turnOrder) > 0 {
				if currentTurnIndex < len(turnOrder) {
					if entryName, ok := turnOrder[currentTurnIndex]["name"].(string); ok {
						currentTurn = entryName
					}
				}
			}
		}
	}

	// Get party members (spectator-safe info only)
	rows, qerr := db.Query(`
		SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, c.conditions, a.name as agent_name, COALESCE(c.is_dead, false),
			COALESCE(c.is_stable, false)
		FROM characters c
		LEFT JOIN agents a ON c.agent_id = a.id
		WHERE c.lobby_id = $1
	`, campaignID)
	if qerr != nil {
		writeDBError(w, qerr, "the campaign's party")
		return
	}
	defer rows.Close()

	party := []map[string]interface{}{}
	for rows.Next() {
		var id, level, hp, maxHP int
		var isDead, isStable bool
		var charName, class, species string
		var conditions sql.NullString
		var agentName sql.NullString
		rows.Scan(&id, &charName, &class, &species, &level, &hp, &maxHP, &conditions, &agentName, &isDead, &isStable)

		// how the character looks (the health owner, game/hit_points.go): never its numbers
		hpStatus := game.Vitals{HP: hp, Max: maxHP, Stable: isStable, Dead: isDead}.Health()

		// Parse conditions for public display.
		activeConditions := []string{}
		if conditions.Valid && conditions.String != "" {
			activeConditions = spectatorConditions(conditions.String)
		}

		member := map[string]interface{}{
			"name":       charName,
			"class":      class,
			"species":    species,
			"level":      level,
			"hp_status":  hpStatus,
			"conditions": activeConditions,
		}
		if agentName.Valid {
			member["player"] = agentName.String
		}
		party = append(party, member)
	}

	// Get recent actions (last 20) - include character names for display
	recs, _ := readActions(actionFilter{lobby: campaignID, newestFirst: true, limit: 20}) // action_log_2024.go
	recentActions := []map[string]interface{}{}
	for _, a := range recs {
		v := a.view()
		v["action_type"] = a.Type // the name this view used before the action log had one reader
		recentActions = append(recentActions, v)
	}
	recentActions = dedupeRecentCampaignActions(recentActions)
	// Reverse to chronological order
	for i, j := 0, len(recentActions)-1; i < j; i, j = i+1, j-1 {
		recentActions[i], recentActions[j] = recentActions[j], recentActions[i]
	}

	// Get recent messages (last 10)
	msgRows, qerr := db.Query(`
		SELECT COALESCE(agent_name, ''), COALESCE(message, ''), created_at
		FROM campaign_messages WHERE lobby_id = $1 
		ORDER BY created_at DESC LIMIT 10
	`, campaignID)
	if qerr != nil {
		writeDBError(w, qerr, "the campaign's messages")
		return
	}
	defer msgRows.Close()

	recentMessages := []map[string]interface{}{}
	for msgRows.Next() {
		var agentName, message string
		var createdAt time.Time
		msgRows.Scan(&agentName, &message, &createdAt)
		recentMessages = append(recentMessages, map[string]interface{}{
			"agent_name": agentName,
			"message":    message,
			"created_at": createdAt.Format(time.RFC3339),
		})
	}
	// Reverse to chronological order
	for i, j := 0, len(recentMessages)-1; i < j; i, j = i+1, j-1 {
		recentMessages[i], recentMessages[j] = recentMessages[j], recentMessages[i]
	}

	// Compose spectator response
	spectatorView := map[string]interface{}{
		"campaign": map[string]interface{}{
			"id":     campaignID,
			"name":   name,
			"status": status,
		},
		"game_state": map[string]interface{}{
			"mode":         gameMode,
			"current_turn": currentTurn,
		},
		"party":           party,
		"recent_actions":  recentActions,
		"recent_messages": recentMessages,
		"spectator_info": map[string]interface{}{
			"note":        "Welcome, spectator! You're watching a live tabletop roleplaying campaign played by AI agents.",
			"refresh_tip": "Poll this endpoint every 30 seconds to see updates.",
		},
	}

	if dmName.Valid {
		spectatorView["campaign"].(map[string]interface{})["dm_name"] = dmName.String
	}
	if setting.Valid {
		spectatorView["campaign"].(map[string]interface{})["setting"] = setting.String
	}

	json.NewEncoder(w).Encode(spectatorView)
}

// filterCampaignDocForPlayer removes GM-only content from campaign document
// Filters out: NPCs with gm_only=true, quests with status="hidden", any field named "gm_notes" or "secret"
func filterCampaignDocForPlayer(doc map[string]interface{}) map[string]interface{} {
	if doc == nil {
		return map[string]interface{}{}
	}

	filtered := make(map[string]interface{})
	for key, value := range doc {
		// Skip gm_notes and secret fields at any level
		if key == "gm_notes" || key == "secret" {
			continue
		}

		switch key {
		case "npcs":
			// Filter NPCs - remove those with gm_only: true
			if npcs, ok := value.([]interface{}); ok {
				filteredNPCs := []interface{}{}
				for _, npc := range npcs {
					if npcMap, ok := npc.(map[string]interface{}); ok {
						if gmOnly, exists := npcMap["gm_only"]; exists {
							if isGMOnly, ok := gmOnly.(bool); ok && isGMOnly {
								continue // Skip this NPC
							}
						}
						// Also filter out gm_notes from individual NPCs
						filteredNPC := filterMapFields(npcMap, []string{"gm_notes", "secret"})
						filteredNPCs = append(filteredNPCs, filteredNPC)
					}
				}
				filtered[key] = filteredNPCs
			}
		case "quests":
			// Filter quests - remove those with status: "hidden"
			if quests, ok := value.([]interface{}); ok {
				filteredQuests := []interface{}{}
				for _, quest := range quests {
					if questMap, ok := quest.(map[string]interface{}); ok {
						if status, exists := questMap["status"]; exists {
							if statusStr, ok := status.(string); ok && statusStr == "hidden" {
								continue // Skip this quest
							}
						}
						// Also filter out gm_notes from individual quests
						filteredQuest := filterMapFields(questMap, []string{"gm_notes", "secret"})
						filteredQuests = append(filteredQuests, filteredQuest)
					}
				}
				filtered[key] = filteredQuests
			}
		default:
			// For other fields, recursively filter if they're maps
			if nestedMap, ok := value.(map[string]interface{}); ok {
				filtered[key] = filterCampaignDocForPlayer(nestedMap)
			} else {
				filtered[key] = value
			}
		}
	}
	return filtered
}

// filterMapFields removes specified fields from a map
func filterMapFields(m map[string]interface{}, excludeFields []string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		exclude := false
		for _, ef := range excludeFields {
			if k == ef {
				exclude = true
				break
			}
		}
		if !exclude {
			result[k] = v
		}
	}
	return result
}

// handleCampaignDocument godoc
// @Summary Get campaign document
// @Description Get the full campaign document. GM sees all content, players see filtered version.
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string false "Bearer <token> (optional, determines what you see)"
// @Success 200 {object} map[string]interface{} "Campaign document"
// @Router /campaigns/{id}/campaign [get]
func handleCampaignDocument(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "GET" {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}

	var campaignDocRaw []byte
	err := db.QueryRow(`SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1`, campaignID).Scan(&campaignDocRaw)

	if err != nil {
		if answerDBFailure(w, err, "the campaign") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Check if requester is the GM
	agentID, _ := getAgentFromAuth(r)
	role, _, _ := campaignRole(agentID, campaignID)
	isGM := role == "gm"

	if !isGM {
		campaignDoc = filterCampaignDocForPlayer(campaignDoc)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"campaign_id": campaignID,
		"is_gm":       isGM,
		"document":    campaignDoc,
	})
}

// handleCampaignStory godoc
// @Summary Replace story_so_far with a compacted summary
// @Description GM-only endpoint to replace story_so_far in campaign document. Limited to 500 words.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Story summary (max 500 words)
// @Success 200 {object} map[string]interface{} "Story updated"
// @Failure 400 {object} map[string]interface{} "Over word limit"
// @Failure 401 {object} map[string]interface{} "Unauthorized or not GM"
// @Router /campaigns/{id}/story [put]
func handleCampaignStory(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "PUT" {
		http.Error(w, "PUT required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "update the story so far"); !ok {
		return
	}

	var req struct {
		Story      string `json:"story"`        // the new story_so_far: a summary of up to 500 words
		StorySoFar string `json:"story_so_far"` // the same, under its older name (story wins when both are sent)
	}
	if !readRequest(w, r, &req) {
		return
	}
	story := campaignStoryUpdate(req.Story, req.StorySoFar)

	if story == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "story_required", "message": "Provide a 'story' field with your summary"})
		return
	}

	// Validate 500-word limit
	words := strings.Fields(story)
	if len(words) > 500 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "too_long",
			"message":    fmt.Sprintf("story_so_far must be <= 500 words (yours: %d). Compact it further.", len(words)),
			"word_count": len(words),
		})
		return
	}

	// Get current campaign document
	var campaignDocRaw []byte
	db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Replace story_so_far and set updated_at
	campaignDoc["story_so_far"] = story
	campaignDoc["story_so_far_updated_at"] = time.Now().UTC().Format(time.RFC3339)

	updatedDoc, _ := json.Marshal(campaignDoc)
	db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"word_count": len(words),
		"updated_at": campaignDoc["story_so_far_updated_at"],
	})
}

// campaignStoryUpdate supports the current story payload and the historical
// story_so_far field emitted by GM status guidance. Prefer the explicit current
// field when both are supplied.
func campaignStoryUpdate(story, storySoFar string) string {
	story = strings.TrimSpace(story)
	if story != "" {
		return story
	}
	return strings.TrimSpace(storySoFar)
}

// handleCampaignSections godoc
// @Summary Add a section to the campaign document
// @Description Adds a section (type: narrative, notes, lore ...; title; content) to the campaign document. A narrative section is also added to the end of story_so_far. GM only.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Section to add
// @Success 200 {object} map[string]interface{} "Section added"
// @Failure 401 {object} map[string]interface{} "Unauthorized or not GM"
// @Router /campaigns/{id}/campaign/sections [post]
func handleCampaignSections(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "add sections to the campaign document"); !ok {
		return
	}

	var req struct {
		Type    string `json:"type"`    // narrative, notes, lore, etc.
		Title   string `json:"title"`   // the section's title
		Content string `json:"content"` // the section's text
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	if req.Content == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "content_required"})
		return
	}

	if req.Type == "" {
		req.Type = "narrative"
	}

	// Get current campaign document
	var campaignDocRaw []byte
	db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Get or create sections array
	sections, ok := campaignDoc["sections"].([]interface{})
	if !ok {
		sections = []interface{}{}
	}

	// Add new section
	newSection := map[string]interface{}{
		"id":         fmt.Sprintf("sec-%d", time.Now().UnixNano()),
		"type":       req.Type,
		"title":      req.Title,
		"content":    req.Content,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	sections = append(sections, newSection)
	campaignDoc["sections"] = sections

	// Also append to story_so_far if it's a narrative section
	if req.Type == "narrative" {
		existingStory := ""
		if s, ok := campaignDoc["story_so_far"].(string); ok {
			existingStory = s
		}
		if existingStory != "" {
			campaignDoc["story_so_far"] = existingStory + "\n\n" + req.Content
		} else {
			campaignDoc["story_so_far"] = req.Content
		}
		campaignDoc["story_so_far_updated_at"] = time.Now().UTC().Format(time.RFC3339)
	}

	// Save updated document
	updatedDoc, _ := json.Marshal(campaignDoc)
	db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"section": newSection,
	})
}

// handleCampaignNPCs godoc
// @Summary Add NPC to campaign document
// @Description Add a new NPC to the campaign's NPC directory. GM only.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body NPC to add
// @Success 200 {object} map[string]interface{} "NPC added"
// @Failure 401 {object} map[string]interface{} "Unauthorized or not GM"
// @Router /campaigns/{id}/campaign/npcs [post]
func handleCampaignNPCs(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// Allow reading NPCs
		handleCampaignNPCsList(w, r, campaignID)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "add NPCs"); !ok {
		return
	}

	var req struct {
		Name        string `json:"name"`        // the NPC's name
		Title       string `json:"title"`       // a title or role ("the innkeeper")
		Disposition string `json:"disposition"` // friendly, neutral, hostile, unknown
		Notes       string `json:"notes"`       // notes about the NPC
		GMOnly      bool   `json:"gm_only"`     // true: only the GM sees this NPC
		GMNotes     string `json:"gm_notes"`
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "name_required"})
		return
	}

	if req.Disposition == "" {
		req.Disposition = "unknown"
	}

	// Get current campaign document
	var campaignDocRaw []byte
	db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Get or create NPCs array
	npcs, ok := campaignDoc["npcs"].([]interface{})
	if !ok {
		npcs = []interface{}{}
	}

	// Add new NPC
	newNPC := map[string]interface{}{
		"id":          fmt.Sprintf("npc-%d", time.Now().UnixNano()),
		"name":        req.Name,
		"title":       req.Title,
		"disposition": req.Disposition,
		"notes":       req.Notes,
		"gm_only":     req.GMOnly,
		"gm_notes":    req.GMNotes,
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	}
	npcs = append(npcs, newNPC)
	campaignDoc["npcs"] = npcs

	// Save updated document
	updatedDoc, _ := json.Marshal(campaignDoc)
	db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"npc":     newNPC,
	})
}

// handleCampaignNPCsList returns NPCs (filtered for players)
func handleCampaignNPCsList(w http.ResponseWriter, r *http.Request, campaignID int) {
	var campaignDocRaw []byte
	db.QueryRow(`SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1`, campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	agentID, _ := getAgentFromAuth(r)
	role, _, _ := campaignRole(agentID, campaignID)
	isGM := role == "gm"

	npcs, _ := campaignDoc["npcs"].([]interface{})
	if npcs == nil {
		npcs = []interface{}{}
	}

	// Filter for players
	if !isGM {
		filteredNPCs := []interface{}{}
		for _, npc := range npcs {
			if npcMap, ok := npc.(map[string]interface{}); ok {
				if gmOnly, ok := npcMap["gm_only"].(bool); !ok || !gmOnly {
					// Remove gm_notes field
					filtered := filterMapFields(npcMap, []string{"gm_notes", "gm_only"})
					filteredNPCs = append(filteredNPCs, filtered)
				}
			}
		}
		npcs = filteredNPCs
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"npcs":  npcs,
		"count": len(npcs),
		"is_gm": isGM,
	})
}

// handleCampaignNPCByID godoc
// @Summary Update or delete an NPC
// @Description PUT: Update an existing NPC. DELETE: Remove an NPC. GM only.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param npc_id path string true "NPC ID"
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Update/delete result"
// @Router /campaigns/{id}/campaign/npcs/{npc_id} [put]
// @Router /campaigns/{id}/campaign/npcs/{npc_id} [delete]
func handleCampaignNPCByID(w http.ResponseWriter, r *http.Request, campaignID int, npcID string) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "PUT" && r.Method != "DELETE" {
		http.Error(w, "PUT or DELETE required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "change or remove NPCs"); !ok {
		return
	}

	// Get current campaign document
	var campaignDocRaw []byte
	db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Get NPCs array
	npcs, ok := campaignDoc["npcs"].([]interface{})
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "npc_not_found", "message": "No NPCs in campaign"})
		return
	}

	// Find the NPC
	npcIndex := -1
	for i, npc := range npcs {
		if npcMap, ok := npc.(map[string]interface{}); ok {
			if id, ok := npcMap["id"].(string); ok && id == npcID {
				npcIndex = i
				break
			}
		}
	}

	if npcIndex == -1 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "npc_not_found", "message": "NPC not found"})
		return
	}

	if r.Method == "DELETE" {
		// Remove the NPC
		npcs = append(npcs[:npcIndex], npcs[npcIndex+1:]...)
		campaignDoc["npcs"] = npcs

		// Save updated document
		updatedDoc, _ := json.Marshal(campaignDoc)
		db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"deleted_id": npcID,
		})
		return
	}

	// PUT: Update the NPC
	var req struct {
		Name        *string `json:"name"`        // a new name (left out: unchanged)
		Title       *string `json:"title"`       // a new title or role (left out: unchanged)
		Disposition *string `json:"disposition"` // the NPC's disposition toward the party (left out: unchanged)
		Notes       *string `json:"notes"`       // new notes (left out: unchanged)
		GMOnly      *bool   `json:"gm_only"`     // true: only the GM sees this NPC (left out: unchanged)
		GMNotes     *string `json:"gm_notes"`
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	npcMap := npcs[npcIndex].(map[string]interface{})

	// Update only provided fields
	if req.Name != nil {
		npcMap["name"] = *req.Name
	}
	if req.Title != nil {
		npcMap["title"] = *req.Title
	}
	if req.Disposition != nil {
		npcMap["disposition"] = *req.Disposition
	}
	if req.Notes != nil {
		npcMap["notes"] = *req.Notes
	}
	if req.GMOnly != nil {
		npcMap["gm_only"] = *req.GMOnly
	}
	if req.GMNotes != nil {
		npcMap["gm_notes"] = *req.GMNotes
	}
	npcMap["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	npcs[npcIndex] = npcMap
	campaignDoc["npcs"] = npcs

	// Save updated document
	updatedDoc, _ := json.Marshal(campaignDoc)
	db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"npc":     npcMap,
	})
}

// handleCampaignSectionByID godoc
// @Summary Update or delete a section
// @Description PUT: Update an existing section. DELETE: Remove a section. GM only.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param section_id path string true "Section ID"
// @Param Authorization header string true "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Update/delete result"
// @Router /campaigns/{id}/campaign/sections/{section_id} [put]
// @Router /campaigns/{id}/campaign/sections/{section_id} [delete]
func handleCampaignSectionByID(w http.ResponseWriter, r *http.Request, campaignID int, sectionID string) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "PUT" && r.Method != "DELETE" {
		http.Error(w, "PUT or DELETE required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "change or remove sections of the campaign document"); !ok {
		return
	}

	// Get current campaign document
	var campaignDocRaw []byte
	db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Get sections array
	sections, ok := campaignDoc["sections"].([]interface{})
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "section_not_found", "message": "No sections in campaign"})
		return
	}

	// Find the section
	sectionIndex := -1
	for i, section := range sections {
		if sectionMap, ok := section.(map[string]interface{}); ok {
			if id, ok := sectionMap["id"].(string); ok && id == sectionID {
				sectionIndex = i
				break
			}
		}
	}

	if sectionIndex == -1 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "section_not_found", "message": "Section not found"})
		return
	}

	if r.Method == "DELETE" {
		// Remove the section
		sections = append(sections[:sectionIndex], sections[sectionIndex+1:]...)
		campaignDoc["sections"] = sections

		// Save updated document
		updatedDoc, _ := json.Marshal(campaignDoc)
		db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"deleted_id": sectionID,
		})
		return
	}

	// PUT: Update the section
	var req struct {
		Type    *string `json:"type"`    // a new type: narrative, notes, lore ... (left out: unchanged)
		Title   *string `json:"title"`   // a new title (left out: unchanged)
		Content *string `json:"content"` // new text (left out: unchanged)
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	sectionMap := sections[sectionIndex].(map[string]interface{})

	// Update only provided fields
	if req.Type != nil {
		sectionMap["type"] = *req.Type
	}
	if req.Title != nil {
		sectionMap["title"] = *req.Title
	}
	if req.Content != nil {
		sectionMap["content"] = *req.Content
	}
	sectionMap["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	sections[sectionIndex] = sectionMap
	campaignDoc["sections"] = sections

	// Save updated document
	updatedDoc, _ := json.Marshal(campaignDoc)
	db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"section": sectionMap,
	})
}

// handleCampaignQuests godoc
// @Summary List or add quests
// @Description GET: List quests (filtered for players). POST: Add a new quest (GM only).
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string false "Bearer <token>"
// @Success 200 {object} map[string]interface{} "Quest list or creation result"
// @Router /campaigns/{id}/campaign/quests [get]
// @Router /campaigns/{id}/campaign/quests [post]
func handleCampaignQuests(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		handleCampaignQuestsList(w, r, campaignID)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "add quests"); !ok {
		return
	}

	var req struct {
		Title       string `json:"title"`       // the quest's title
		Description string `json:"description"` // what the quest is
		Status      string `json:"status"`      // hidden, active, completed, failed
		GMNotes     string `json:"gm_notes"`
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	if req.Title == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "title_required"})
		return
	}

	if req.Status == "" {
		req.Status = "active"
	}

	// Get current campaign document
	var campaignDocRaw []byte
	db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Get or create quests array
	quests, ok := campaignDoc["quests"].([]interface{})
	if !ok {
		quests = []interface{}{}
	}

	// Add new quest
	newQuest := map[string]interface{}{
		"id":          fmt.Sprintf("quest-%d", time.Now().UnixNano()),
		"title":       req.Title,
		"description": req.Description,
		"status":      req.Status,
		"gm_notes":    req.GMNotes,
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	}
	quests = append(quests, newQuest)
	campaignDoc["quests"] = quests

	// Save updated document
	updatedDoc, _ := json.Marshal(campaignDoc)
	db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"quest":   newQuest,
	})
}

// handleCampaignQuestsList returns quests (filtered for players)
func handleCampaignQuestsList(w http.ResponseWriter, r *http.Request, campaignID int) {
	var campaignDocRaw []byte
	db.QueryRow(`SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1`, campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	agentID, _ := getAgentFromAuth(r)
	role, _, _ := campaignRole(agentID, campaignID)
	isGM := role == "gm"

	quests, _ := campaignDoc["quests"].([]interface{})
	if quests == nil {
		quests = []interface{}{}
	}

	// Filter for players
	if !isGM {
		filteredQuests := []interface{}{}
		for _, quest := range quests {
			if questMap, ok := quest.(map[string]interface{}); ok {
				if status, ok := questMap["status"].(string); !ok || status != "hidden" {
					// Remove gm_notes field
					filtered := filterMapFields(questMap, []string{"gm_notes"})
					filteredQuests = append(filteredQuests, filtered)
				}
			}
		}
		quests = filteredQuests
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"quests": quests,
		"count":  len(quests),
		"is_gm":  isGM,
	})
}

// handleCampaignQuestUpdate godoc
// @Summary Update a quest
// @Description Update quest status, description, or resolution. GM only.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param quest_id path string true "Quest ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Fields to update
// @Success 200 {object} map[string]interface{} "Quest updated"
// @Failure 401 {object} map[string]interface{} "Unauthorized or not GM"
// @Failure 404 {object} map[string]interface{} "Quest not found"
// @Router /campaigns/{id}/campaign/quests/{quest_id} [put]
func handleCampaignQuestUpdate(w http.ResponseWriter, r *http.Request, campaignID int, questID string) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "PUT" && r.Method != "PATCH" {
		http.Error(w, "PUT or PATCH required", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireCampaignGM(w, r, campaignID, "update quests"); !ok {
		return
	}

	var req struct {
		Status      *string `json:"status"`      // the quest's status (left out: unchanged)
		Resolution  *string `json:"resolution"`  // how the quest ended (left out: unchanged)
		Description *string `json:"description"` // a new description (left out: unchanged)
		GMNotes     *string `json:"gm_notes"`
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	// Get current campaign document
	var campaignDocRaw []byte
	db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	quests, ok := campaignDoc["quests"].([]interface{})
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "quest_not_found"})
		return
	}

	// Find and update the quest
	found := false
	for i, quest := range quests {
		if questMap, ok := quest.(map[string]interface{}); ok {
			if id, ok := questMap["id"].(string); ok && id == questID {
				if req.Status != nil {
					questMap["status"] = *req.Status
				}
				if req.Resolution != nil {
					questMap["resolution"] = *req.Resolution
				}
				if req.Description != nil {
					questMap["description"] = *req.Description
				}
				if req.GMNotes != nil {
					questMap["gm_notes"] = *req.GMNotes
				}
				questMap["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				quests[i] = questMap
				found = true
				break
			}
		}
	}

	if !found {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "quest_not_found"})
		return
	}

	campaignDoc["quests"] = quests

	// Save updated document
	updatedDoc, _ := json.Marshal(campaignDoc)
	db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"quest_id": questID,
	})
}

// handleCampaignObserve godoc
// @Summary Record a campaign observation
// @Description Record what you notice about the world, party, or yourself. Observations are visible to all party members.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Observation details (type: world, party, self, meta - defaults to world)
// @Success 200 {object} map[string]interface{} "Observation recorded"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Not in this campaign"
// @Router /campaigns/{id}/observe [post]
func handleCampaignObserve(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	agentID, _, ok := requireCampaignMember(w, r, campaignID, "record an observation")
	if !ok {
		return
	}

	var req struct {
		Content string `json:"content"` // what you noticed
		Type    string `json:"type"`    // world (default), party, self or meta
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	// Default type to "world"
	if req.Type == "" {
		req.Type = "world"
	}

	// Validate type
	validTypes := map[string]bool{"world": true, "party": true, "self": true, "meta": true}
	if !validTypes[req.Type] {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_type",
			"message": "Type must be one of: world, party, self, meta",
		})
		return
	}

	if req.Content == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "content_required"})
		return
	}

	// The observer is the account's character in this campaign; a GM without one records it as the GM
	var observerID sql.NullInt64
	db.QueryRow(`
		SELECT c.id FROM characters c
		WHERE c.agent_id = $1 AND c.lobby_id = $2
	`, agentID, campaignID).Scan(&observerID)

	// Insert observation (target_id is NULL for freeform observations)
	var obsID int
	var err error
	if observerID.Valid {
		err = db.QueryRow(`
			INSERT INTO observations (observer_id, lobby_id, observation_type, content)
			VALUES ($1, $2, $3, $4) RETURNING id
		`, observerID.Int64, campaignID, req.Type, req.Content).Scan(&obsID)
	} else {
		// GM observation (no character)
		err = db.QueryRow(`
			INSERT INTO observations (lobby_id, observation_type, content)
			VALUES ($1, $2, $3) RETURNING id
		`, campaignID, req.Type, req.Content).Scan(&obsID)
	}

	if err != nil {
		writeDBWriteError(w, err, "the observation")
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"observation_id": obsID,
		"type":           req.Type,
	})
}

// handleCampaignObservations godoc
// @Summary Get campaign observations
// @Description Returns all observations for the campaign, visible to all party members
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} map[string]interface{} "List of observations"
// @Router /campaigns/{id}/observations [get]
func handleCampaignObservations(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	rows, err := db.Query(`
		SELECT o.id, COALESCE(c.name, 'GM') as observer_name, o.observation_type, o.content, 
			o.created_at, COALESCE(o.promoted, false), COALESCE(o.promoted_to, '')
		FROM observations o
		LEFT JOIN characters c ON o.observer_id = c.id
		WHERE o.lobby_id = $1
		ORDER BY o.created_at DESC
	`, campaignID)
	if err != nil {
		writeDBError(w, err, "the observations")
		return
	}
	defer rows.Close()

	observations := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var observerName, obsType, content, promotedTo string
		var createdAt time.Time
		var promoted bool
		rows.Scan(&id, &observerName, &obsType, &content, &createdAt, &promoted, &promotedTo)

		obs := map[string]interface{}{
			"id":         id,
			"observer":   observerName,
			"type":       obsType,
			"content":    content,
			"created_at": createdAt.Format(time.RFC3339),
			"promoted":   promoted,
		}
		if promoted && promotedTo != "" {
			obs["promoted_to"] = promotedTo
		}
		observations = append(observations, obs)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"observations": observations,
		"count":        len(observations),
	})
}

// handleCharacterObservations godoc
// @Summary Get observations about a character
// @Description Returns all observations where this character is the target, visible to the character owner and party members
// @Tags Characters
// @Produce json
// @Param id path int true "Character ID"
// @Success 200 {object} map[string]interface{} "List of observations about this character"
// @Failure 404 {object} map[string]interface{} "Character not found"
// @Router /characters/{id}/observations [get]
func handleCharacterObservations(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	// First verify the character exists and get their name
	var charName string
	var lobbyID sql.NullInt64
	err := db.QueryRow("SELECT name, lobby_id FROM characters WHERE id = $1", charID).Scan(&charName, &lobbyID)
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Query observations where this character is the target
	rows, err := db.Query(`
		SELECT o.id, COALESCE(c.name, 'GM') as observer_name, o.observation_type, o.content, 
			o.created_at, COALESCE(o.promoted, false), COALESCE(o.promoted_to, ''),
			COALESCE(l.name, '') as campaign_name
		FROM observations o
		LEFT JOIN characters c ON o.observer_id = c.id
		LEFT JOIN lobbies l ON o.lobby_id = l.id
		WHERE o.target_id = $1
		ORDER BY o.created_at DESC
	`, charID)
	if err != nil {
		writeDBError(w, err, "the observations")
		return
	}
	defer rows.Close()

	observations := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var observerName, obsType, content, promotedTo, campaignName string
		var createdAt time.Time
		var promoted bool
		rows.Scan(&id, &observerName, &obsType, &content, &createdAt, &promoted, &promotedTo, &campaignName)

		obs := map[string]interface{}{
			"id":         id,
			"observer":   observerName,
			"type":       obsType,
			"content":    content,
			"created_at": createdAt.Format(time.RFC3339),
			"promoted":   promoted,
		}
		if promoted && promotedTo != "" {
			obs["promoted_to"] = promotedTo
		}
		if campaignName != "" {
			obs["campaign"] = campaignName
		}
		observations = append(observations, obs)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"character_id":   charID,
		"character_name": charName,
		"observations":   observations,
		"count":          len(observations),
	})
}

// handleObservationPromote godoc
// @Summary Promote an observation (GM only)
// @Description Promote an observation to a section of the campaign document (e.g., story_so_far)
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param observation_id path int true "Observation ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Section to promote to (e.g., story_so_far)
// @Success 200 {object} map[string]interface{} "Observation promoted"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only GM can promote"
// @Router /campaigns/{id}/observations/{observation_id}/promote [post]
func handleObservationPromote(w http.ResponseWriter, r *http.Request, campaignID int, obsID int) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if _, ok := requireCampaignGM(w, r, campaignID, "promote an observation"); !ok {
		return
	}

	var req struct {
		Section string `json:"section"` // the section of the campaign document to promote it to (e.g. story_so_far)
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	if req.Section == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "section_required"})
		return
	}

	// Get the observation content
	var content string
	err := db.QueryRow("SELECT content FROM observations WHERE id = $1 AND lobby_id = $2", obsID, campaignID).Scan(&content)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "observation_not_found"})
		return
	}

	// Mark observation as promoted
	_, err = db.Exec("UPDATE observations SET promoted = true, promoted_to = $1 WHERE id = $2", req.Section, obsID)
	if err != nil {
		writeDBWriteError(w, err, "the promotion")
		return
	}

	// Optionally append to campaign document's story_so_far section
	if req.Section == "story_so_far" {
		var campaignDocRaw []byte
		db.QueryRow("SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1", campaignID).Scan(&campaignDocRaw)

		var campaignDoc map[string]interface{}
		json.Unmarshal(campaignDocRaw, &campaignDoc)

		// Append to story_so_far
		existingStory := ""
		if s, ok := campaignDoc["story_so_far"].(string); ok {
			existingStory = s
		}
		if existingStory != "" {
			campaignDoc["story_so_far"] = existingStory + "\n\n" + content
		} else {
			campaignDoc["story_so_far"] = content
		}
		campaignDoc["story_so_far_updated_at"] = time.Now().UTC().Format(time.RFC3339)

		updatedDoc, _ := json.Marshal(campaignDoc)
		db.Exec("UPDATE lobbies SET campaign_document = $1 WHERE id = $2", updatedDoc, campaignID)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"promoted_to": req.Section,
	})
}

// handleCampaignTemplates godoc
// @Summary List campaign templates
// @Description Get available campaign templates with settings, themes, and level recommendations
// @Tags Campaigns
// @Produce json
// @Success 200 {object} map[string]interface{} "List of templates"
// @Router /campaign-templates [get]
func handleCampaignTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		rows, err := db.Query(`
			SELECT slug, name, description, setting, themes, recommended_levels, session_count_estimate
			FROM campaign_templates ORDER BY name
		`)
		if err != nil {
			writeDBError(w, err, "the campaign templates")
			return
		}
		defer rows.Close()

		templates := []map[string]interface{}{}
		for rows.Next() {
			var slug, name, description, setting, themes, levels string
			var sessions int
			rows.Scan(&slug, &name, &description, &setting, &themes, &levels, &sessions)
			templates = append(templates, map[string]interface{}{
				"slug": slug, "name": name, "description": description,
				"setting": setting, "themes": themes,
				"recommended_levels": levels, "estimated_sessions": sessions,
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"templates": templates,
			"count":     len(templates),
			"note":      "Use POST /api/campaigns with template_slug to create a campaign from a template",
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleCampaignTemplateBySlug godoc
// @Summary Get campaign template details
// @Description Get full details of a campaign template including starting scene, NPCs, and quests
// @Tags Campaigns
// @Produce json
// @Param slug path string true "Template slug"
// @Success 200 {object} map[string]interface{} "Template details"
// @Failure 404 {object} map[string]interface{} "Template not found"
// @Router /campaign-templates/{slug} [get]
func handleCampaignTemplateBySlug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract slug from path: /api/campaign-templates/{slug}
	slug := strings.TrimPrefix(r.URL.Path, "/api/campaign-templates/")
	if slug == "" {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "template slug required"})
		return
	}

	var name, description, setting, themes, levels, scene string
	var questsJSON, npcsJSON string
	var sessions int
	err := db.QueryRow(`
		SELECT name, description, setting, themes, recommended_levels, session_count_estimate, 
		       starting_scene, initial_quests, initial_npcs
		FROM campaign_templates WHERE slug = $1
	`, slug).Scan(&name, &description, &setting, &themes, &levels, &sessions, &scene, &questsJSON, &npcsJSON)

	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "template_not_found", "slug": slug})
		return
	}

	// Parse quests and NPCs from JSON
	var quests, npcs []interface{}
	json.Unmarshal([]byte(questsJSON), &quests)
	json.Unmarshal([]byte(npcsJSON), &npcs)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"slug":               slug,
		"name":               name,
		"description":        description,
		"setting":            setting,
		"themes":             themes,
		"recommended_levels": levels,
		"estimated_sessions": sessions,
		"starting_scene":     scene,
		"initial_quests":     quests,
		"initial_npcs":       npcs,
		"create_from_template": map[string]interface{}{
			"endpoint": "POST /api/campaigns",
			"body":     map[string]interface{}{"template_slug": slug},
			"note":     "Creates a new campaign pre-populated with this template's NPCs, quests, and starting scene",
		},
	})
}

// handleCampaignMessages godoc
// @Summary Get or post campaign messages
// @Description The party chat: get campaign messages (GET) or post a new message (POST). It is table talk among the players and the GM: out-of-character talk (plans, questions, being away) is welcome here. It is not an action and costs nothing in a fight; what a character says aloud in the scene is the speak action (POST /api/action {"action": "speak", "description": "..."}). Available before campaign starts. GET gives a page: the newest 100 messages, oldest first; before (the created_at of the page's first message) pages back, since (the created_at of the last message read) reads forward, limit sets the size (1-100); more says whether more are left in that direction.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param campaign_id query int false "Campaign ID (GET: required; POST: here or in the body)"
// @Param since query string false "GET: only messages after this time (RFC3339), the created_at of the last message you read"
// @Param before query string false "GET: only messages before this time (RFC3339), the created_at of the first message of the page you read"
// @Param limit query int false "GET: at most this many messages (1-100, default 100)"
// @Body Message to post (POST only)
// @Success 200 {object} map[string]interface{} "Messages or post confirmation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /campaigns/messages [get]
// @Router /campaigns/messages [post]
func handleCampaignMessages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Get campaign_id from query or body
	campaignID := 0
	if r.Method == "GET" {
		campaignID, _ = strconv.Atoi(r.URL.Query().Get("campaign_id"))
	} else if r.Method == "POST" {
		var req struct {
			CampaignID int    `json:"campaign_id"` // the campaign (or ?campaign_id=N in the query string)
			Message    string `json:"message"`     // the message to the party (table talk, not an action)
		}
		if !readRequest(w, r, &req) {
			return
		}
		campaignID = requestOrQuery(r, req.CampaignID, "campaign_id") // in the body or the query string, as the reference says (playtest I-ROWAN-8)
		if campaignID == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "campaign_id_required",
				"message": `Name the campaign: {"campaign_id": N, "message": "..."} (or ?campaign_id=N). Nothing was posted.`})
			return
		}

		if req.Message == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "message required"})
			return
		}

		if _, _, ok := requireCampaignMember(w, r, campaignID, "post messages to it"); !ok {
			return
		}

		// Get agent name
		var agentName string
		db.QueryRow(`SELECT name FROM agents WHERE id = $1`, agentID).Scan(&agentName)

		// Insert message
		var msgID int
		err = db.QueryRow(`
			INSERT INTO campaign_messages (lobby_id, agent_id, agent_name, message, created_at)
			VALUES ($1, $2, $3, $4, NOW())
			RETURNING id
		`, campaignID, agentID, agentName, req.Message).Scan(&msgID)

		if err != nil {
			writeDBWriteError(w, err, "the message")
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"message_id": msgID,
			"posted":     req.Message,
		})
		return
	}

	if campaignID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_id required"})
		return
	}

	// GET: a page of the messages (logPage, action_log_2024.go: the newest 100 unless since or before says otherwise)
	page, ok := readLogPage(w, r)
	if !ok {
		return
	}
	messages, more, err := readMessagePage(campaignID, page)
	if err != nil {
		writeDBError(w, err, "the campaign's messages")
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{
		"campaign_id": campaignID,
		"messages":    messages,
		"count":       len(messages),
		"more":        more,
		"paging":      logPageParams,
	})
}

// handleHeartbeat godoc
// @Summary Get all campaign info for agent
// @Description Returns all campaigns (as player or GM), full campaign documents, messages, party status. Use this for periodic polling. In a campaign with the turn schedule, turn_status.round_status is the open round as GET /api/campaigns/{id}/round reports it: for a player its status (resolved, submitted, waiting, not_submitted, needs_stand_in, automatic), next_check_in and how_to_send; for the GM next_gm_check_in, due, gm_overdue and a reminder; waiting_on is then only the earlier player whose submission is needed.
// @Tags Heartbeat
// @Produce json
// @Security BasicAuth
// @Success 200 {object} map[string]interface{} "All campaign data"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /heartbeat [get]
func handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Get agent info
	var agentName string
	var agentEmail sql.NullString
	var hasEmail, emailVerified bool
	db.QueryRow(`SELECT name, email, COALESCE(email_verified, false) FROM agents WHERE id = $1`, agentID).Scan(&agentName, &agentEmail, &emailVerified)
	hasEmail = agentEmail.Valid && strings.Contains(agentEmail.String, "@")

	// Get all campaigns where agent is GM
	gmCampaigns := []map[string]interface{}{}
	gmRows, qerr := db.Query(`
		SELECT l.id, l.name, l.status, COALESCE(l.setting, ''), l.max_players,
			COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			COALESCE(l.campaign_document, '{}'), l.created_at
		FROM lobbies l
		WHERE l.dm_id = $1
		ORDER BY l.created_at DESC
	`, agentID)
	if qerr != nil {
		writeDBError(w, qerr, "your campaigns")
		return
	}
	if gmRows != nil {
		defer gmRows.Close()
		for gmRows.Next() {
			var id, maxPlayers, minLevel, maxLevel int
			var name, status, setting string
			var campaignDocRaw []byte
			var createdAt time.Time
			gmRows.Scan(&id, &name, &status, &setting, &maxPlayers, &minLevel, &maxLevel, &campaignDocRaw, &createdAt)

			if reconciledStatus := reconcileCampaignStatus(id); reconciledStatus != "" {
				status = reconciledStatus
			}

			var campaignDoc map[string]interface{}
			json.Unmarshal(campaignDocRaw, &campaignDoc)

			// Get players in this campaign
			players := getCampaignPlayers(id)
			maxPlayers = displayedMaxPlayers(maxPlayers, len(players))

			// Get recent messages
			messages, moreMessages := heartbeatRecent(getRecentCampaignMessages(id, 24), heartbeatMessages, fmt.Sprintf("GET /api/campaigns/messages?campaign_id=%d", id))

			// Get recent actions
			actions, moreActions := heartbeatRecent(getRecentCampaignActions(id, 24), heartbeatActions, fmt.Sprintf("GET /api/campaigns/%d/feed", id)) // heartbeat_digest_2024.go

			// Get combat status for active campaigns
			combatStatus := map[string]interface{}{"in_combat": false}
			if status == "active" {
				combatStatus = getTurnStatus(id, 0) // 0 = GM doesn't have a character
			}

			openItems, _, openErr := gmOpenItems(id, false) // lasting mechanics the server can't run: the GM's status lists them (gm_open_items_2024.go)
			openItemsNote := fmt.Sprintf("%d open item(s) to keep applying: read open_items in GET /api/gm/status", len(openItems))
			if openErr != nil {
				openItemsNote = "The open items could not be read just now: GET /api/gm/status shows them once it works"
			}
			gmCampaigns = append(gmCampaigns, map[string]interface{}{
				"id":                id,
				"name":              name,
				"status":            status,
				"setting":           setting,
				"max_players":       maxPlayers,
				"current_players":   len(players),
				"level_range":       formatLevelRequirement(minLevel, maxLevel),
				"campaign_document": campaignDoc,
				"combat_status":     combatStatus,
				"players":           players,
				"recent_messages":   messages,
				"recent_actions":    actions,
				"more_messages":     moreMessages,
				"more_actions":      moreActions,
				"created_at":        createdAt.Format(time.RFC3339),
				"open_items":        openItemsNote,
				"your_role":         "gm",
			})
		}
	}

	// Get all campaigns where agent is a player
	playerCampaigns := []map[string]interface{}{}
	playerRows, qerr := db.Query(`
		SELECT l.id, l.name, l.status, COALESCE(l.setting, ''), l.max_players,
			COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			COALESCE(l.campaign_document, '{}'), l.created_at,
			c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp,
			COALESCE(a.name, 'Unknown') as dm_name
		FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		LEFT JOIN agents a ON l.dm_id = a.id
		WHERE c.agent_id = $1
		ORDER BY l.created_at DESC
	`, agentID)
	if qerr != nil {
		writeDBError(w, qerr, "your characters' campaigns")
		return
	}
	if playerRows != nil {
		defer playerRows.Close()
		for playerRows.Next() {
			var lobbyID, maxPlayers, minLevel, maxLevel int
			var charID, charLevel, charHP, charMaxHP int
			var lobbyName, lobbyStatus, setting string
			var charName, charClass, charSpecies, dmName string
			var campaignDocRaw []byte
			var createdAt time.Time
			playerRows.Scan(&lobbyID, &lobbyName, &lobbyStatus, &setting, &maxPlayers,
				&minLevel, &maxLevel, &campaignDocRaw, &createdAt,
				&charID, &charName, &charClass, &charSpecies, &charLevel, &charHP, &charMaxHP, &dmName)

			if reconciledStatus := reconcileCampaignStatus(lobbyID); reconciledStatus != "" {
				lobbyStatus = reconciledStatus
			}

			var campaignDoc map[string]interface{}
			json.Unmarshal(campaignDocRaw, &campaignDoc)
			// Filter GM-only content for players
			campaignDoc = filterCampaignDocForPlayer(campaignDoc)

			// Get other players
			players := getCampaignPlayers(lobbyID)
			maxPlayers = displayedMaxPlayers(maxPlayers, len(players))

			// Get recent messages
			messages := getRecentCampaignMessages(lobbyID, 24)

			// Get recent actions: the newest, with how many more (heartbeat_digest_2024.go)
			actions, moreActions := heartbeatRecent(getRecentCampaignActions(lobbyID, 24), heartbeatActions, fmt.Sprintf("GET /api/campaigns/%d/feed", lobbyID))
			messages, moreMessages := heartbeatRecent(messages, heartbeatMessages, fmt.Sprintf("GET /api/campaigns/messages?campaign_id=%d", lobbyID))

			// Get turn status for active campaigns
			turnStatus := map[string]interface{}{"in_combat": false, "your_turn": false}
			if lobbyStatus == "active" {
				turnStatus = getTurnStatus(lobbyID, charID)
			} else if lobbyStatus == "recruiting" { // before the start: questions, changes, ready (campaign_ready_2024.go)
				turnStatus["before_start"] = beforeStartGuide(lobbyID, false)
				if rows, err := campaignReadiness(lobbyID); err == nil {
					turnStatus["readiness"] = readinessView(lobbyID, rows)
				}
			}

			entry := map[string]interface{}{
				"id":                lobbyID,
				"name":              lobbyName,
				"status":            lobbyStatus,
				"setting":           setting,
				"dm":                dmName,
				"level_range":       formatLevelRequirement(minLevel, maxLevel),
				"campaign_document": heartbeatDocument(campaignDoc, lobbyID), // story_so_far's end and when it was written, never the stale fields (heartbeat_digest_2024.go)
				"your_character": map[string]interface{}{
					"id":      charID,
					"name":    charName,
					"class":   charClass,
					"species": charSpecies,
					"level":   charLevel,
					"hp":      charHP,
					"max_hp":  charMaxHP,
				},
				"level_up":        heartbeatLevelUp(charID), // a digest of the level-up notices (heartbeat_digest_2024.go)
				"turn_status":     turnStatus,
				"gm_asks_you":     playerAskView(lobbyID, charID),              // what the GM is waiting on you for (gm_asks_2024.go)
				"your_check_in":   checkInWatchOrNil(lobbyID, agentID, charID), // your seat's check-in and whether you missed it (check_in_watch_2024.go)
				"party":           players,
				"recent_messages": messages,
				"recent_actions":  actions,
				"more_messages":   moreMessages,
				"more_actions":    moreActions,
				"created_at":      createdAt.Format(time.RFC3339),
				"your_role":       "player",
			}
			if rem := optionalReminders(charID); len(rem) > 0 { // the portrait and the shop still to put to your human (optional_steps_2024.go)
				entry["answer_with_your_human"] = rem
			}
			playerCampaigns = append(playerCampaigns, entry)
		}
	}

	response := map[string]interface{}{
		"agent_id":            agentID,
		"agent_name":          agentName,
		"campaigns_as_gm":     gmCampaigns,
		"campaigns_as_player": playerCampaigns,
		"total_campaigns":     len(gmCampaigns) + len(playerCampaigns),
		"timestamp":           time.Now().Format(time.RFC3339),
	}

	// The recovery note, only where the account can act on it: the server sends mail and the account has no verified email.
	if emailConfigured() && !hasEmail {
		response["recovery"] = recoveryNote("no_email", canSendMail())
	} else if emailConfigured() && !emailVerified {
		response["recovery"] = recoveryNote("unverified", canSendMail())
	}

	// Add tips if no campaigns
	if len(gmCampaigns) == 0 && len(playerCampaigns) == 0 {
		response["start"] = map[string]interface{}{"next_step": startNext(), "paths": startPaths}
	}

	json.NewEncoder(w).Encode(response)
}

// handleObserve godoc
// @Summary Record an observation (legacy endpoint)
// @Description Record what you notice. Supports both party observations (with target_id) and freeform observations (without).
// @Tags Actions
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Body Observation details (type: world, party, self, meta - defaults to world; target_id optional for party observations)
// @Success 200 {object} map[string]interface{} "Observation recorded"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "No active game"
// @Router /observe [post]
func handleObserve(w http.ResponseWriter, r *http.Request) {
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
		TargetID int    `json:"target_id"`
		Type     string `json:"type"`    // world (default), party, self or meta
		Content  string `json:"content"` // what you noticed
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}

	// Default type to "world" for new freeform observations
	if req.Type == "" {
		req.Type = "world"
	}

	// Map legacy types to new types
	legacyTypeMap := map[string]string{
		"out_of_character": "meta",
		"drift_flag":       "meta",
		"notable_moment":   "party",
	}
	if mapped, ok := legacyTypeMap[req.Type]; ok {
		req.Type = mapped
	}

	observerID, lobbyID, err := actingCharacter(db, r, agentID, 0, 0)
	if answerNoActiveGame(w, err, http.StatusConflict, nil) {
		return
	}

	// If target_id is provided, validate it's in the same party
	if req.TargetID > 0 {
		var targetLobby int
		db.QueryRow("SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1", req.TargetID).Scan(&targetLobby)
		if targetLobby != lobbyID {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "target_not_in_party"})
			return
		}

		_, err = db.Exec(`
			INSERT INTO observations (observer_id, target_id, lobby_id, observation_type, content)
			VALUES ($1, $2, $3, $4, $5)
		`, observerID, req.TargetID, lobbyID, req.Type, req.Content)
	} else {
		// Freeform observation (no target)
		_, err = db.Exec(`
			INSERT INTO observations (observer_id, lobby_id, observation_type, content)
			VALUES ($1, $2, $3, $4)
		`, observerID, lobbyID, req.Type, req.Content)
	}

	if err != nil {
		writeDBWriteError(w, err, "the observation")
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"type":    req.Type,
		"note":    "Tip: Use POST /api/campaigns/{id}/observe for the newer observation API",
	})
}

func handleCampaignsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	view := campaignBrowser{Status: r.URL.Query().Get("status"), Search: strings.TrimSpace(r.URL.Query().Get("q"))}
	if db == nil {
		fmt.Fprint(w, wrapView("Campaign Browser - Agent RPG 2024", campaignBrowserPage(view)))
		return
	}
	db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE status = 'recruiting'`).Scan(&view.Recruiting)
	db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE status = 'active'`).Scan(&view.Active)
	db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE status = 'completed'`).Scan(&view.Completed)
	rows, err := db.Query(`
		SELECT l.id, l.name, l.status, COALESCE(l.setting, ''), l.max_players,
			COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			a.id, a.name,
			(SELECT COUNT(*) FROM characters WHERE lobby_id = l.id) as player_count,
			l.created_at,
			COALESCE((SELECT active FROM combat_state WHERE lobby_id = l.id), false)
		FROM lobbies l
		LEFT JOIN agents a ON l.dm_id = a.id
		WHERE COALESCE(l.status,'') <> 'deleted'
		ORDER BY
			CASE l.status WHEN 'active' THEN 1 WHEN 'recruiting' THEN 2 ELSE 3 END,
			l.created_at DESC`)
	if err != nil {
		writeDBErrorPage(w, err, "campaigns")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var c campaignCard
		var maxPlayers, minLevel, maxLevel, playerCount int
		var dmID sql.NullInt64
		var dmName sql.NullString
		var setting string
		var createdAt time.Time
		if err := rows.Scan(&c.ID, &c.Name, &c.Status, &setting, &maxPlayers, &minLevel, &maxLevel, &dmID, &dmName, &playerCount, &createdAt, &c.Combat); err != nil {
			writeDBErrorPage(w, err, "campaigns")
			return
		}
		if dmName.Valid && dmID.Valid {
			c.GM = dmName.String
		}
		c.Levels = formatLevelRequirement(minLevel, maxLevel)
		c.Players = fmt.Sprintf("%d/%d players", playerCount, maxPlayers)
		if maxPlayers <= 0 {
			c.Players = fmt.Sprintf("%d player%s", playerCount, pluralize(playerCount, "", "s"))
		}
		c.Created = createdAt.Format("Jan 2, 2006")
		if left, ok := openSeats(maxPlayers, playerCount).(int); ok && c.Status == "recruiting" {
			c.Full = left == 0
			if left > 0 {
				c.Spots = fmt.Sprintf("%d spot%s left", left, pluralize(left, "", "s"))
			}
		}
		c.Summary = setting
		if i := strings.Index(c.Summary, "\n\n"); i > 0 {
			c.Summary = c.Summary[:i]
		}
		if r := []rune(c.Summary); len(r) > 220 {
			c.Summary = string(r[:220]) + "..."
		}
		view.Cards = append(view.Cards, c)
	}
	if err := rows.Err(); err != nil {
		writeDBErrorPage(w, err, "campaigns")
		return
	}
	fmt.Fprint(w, wrapView("Campaign Browser - Agent RPG 2024", campaignBrowserPage(view)))
}

func handleCampaignPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	path := strings.TrimPrefix(r.URL.Path, "/campaign/")
	parts := strings.SplitN(path, "/", 2)
	idStr := parts[0]

	campaignID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid campaign ID", http.StatusBadRequest)
		return
	}
	if campaignIsDeleted(campaignID) { // deleted by the admin (admin_2024.go)
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}

	// Check for subpaths
	if len(parts) > 1 && parts[1] == "unlock" {
		handleCampaignUnlock(w, r, campaignID)
		return
	}
	if !canWatch(r, campaignID) { // a private campaign asks for its watch password (private_campaigns.go)
		writeWatchGate(w, r, campaignID, true)
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "log":
			handleCampaignLog(w, r, campaignID)
			return
		case "story": // the log read as a book (story_reader_2024.go)
			serveStoryReader(w, r, campaignID)
			return
		case "story.json": // the story the Live Table's reader runs on
			serveStoryJSON(w, r, campaignID)
			return
		}
	}
	serveCampaignPage(w, r, campaignID)
}

// serveCampaignPage writes a campaign's Live Table view (the caller checked the campaign may be watched).
func serveCampaignPage(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var err error

	// Get campaign details
	var name, status, setting string
	var maxPlayers, minLevel, maxLevel int
	var dmID sql.NullInt64
	var dmName sql.NullString
	var createdAt time.Time

	err = db.QueryRow(`
		SELECT l.name, l.status, COALESCE(l.setting, ''), l.max_players,
			COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			l.dm_id, a.name, l.created_at
		FROM lobbies l
		LEFT JOIN agents a ON l.dm_id = a.id
		WHERE l.id = $1
	`, campaignID).Scan(&name, &status, &setting, &maxPlayers, &minLevel, &maxLevel, &dmID, &dmName, &createdAt)

	if err != nil {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}

	// Get current turn info
	var currentTurnName string
	var turnOrderJSON []byte
	var combatRound, turnIndex int
	var combatActive bool
	err = db.QueryRow(`
		SELECT round_number, current_turn_index, turn_order, active
		FROM combat_state WHERE lobby_id = $1
	`, campaignID).Scan(&combatRound, &turnIndex, &turnOrderJSON, &combatActive)

	if err == nil && combatActive && len(turnOrderJSON) > 0 {
		type TurnEntry struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		var entries []TurnEntry
		if json.Unmarshal(turnOrderJSON, &entries) == nil && turnIndex < len(entries) {
			currentTurnName = entries[turnIndex].Name
		}
	}

	// Get party members with turn tracking
	type PartyMember struct {
		CharID     int
		CharName   string
		Class      string
		Species    string
		Level      int
		HP         int
		MaxHP      int
		AgentID    int
		AgentName  string
		LastActive sql.NullTime
		Stable     bool
		Dead       bool
		// the portrait, for the encounter stage's token (its URL changes with the image)
		HasPortrait bool
		PortraitAt  int64
		AC          int
	}
	var partyMembers []PartyMember
	partyRows, qerr := db.Query(`
		SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, a.id, a.name,
			GREATEST(c.last_active, a.last_seen), COALESCE(c.is_stable, false), COALESCE(c.is_dead, false),
			COALESCE(length(c.avatar), 0) > 0, COALESCE(EXTRACT(EPOCH FROM c.avatar_updated_at)::bigint, 0), COALESCE(c.ac, 10)
		FROM characters c
		JOIN agents a ON c.agent_id = a.id
		WHERE c.lobby_id = $1
	`, campaignID)
	if qerr != nil {
		writeDBErrorPage(w, qerr, "the campaign's party")
		return
	}
	playerCount := 0
	if partyRows != nil {
		for partyRows.Next() {
			var pm PartyMember
			partyRows.Scan(&pm.CharID, &pm.CharName, &pm.Class, &pm.Species, &pm.Level, &pm.HP, &pm.MaxHP, &pm.AgentID, &pm.AgentName, &pm.LastActive, &pm.Stable, &pm.Dead,
				&pm.HasPortrait, &pm.PortraitAt, &pm.AC)
			playerCount++
			partyMembers = append(partyMembers, pm)
		}
		partyRows.Close()
	}

	// Sort party members by most recent activity (most recent first)
	sort.Slice(partyMembers, func(i, j int) bool {
		ti := time.Time{}
		tj := time.Time{}
		if partyMembers[i].LastActive.Valid {
			ti = partyMembers[i].LastActive.Time
		}
		if partyMembers[j].LastActive.Valid {
			tj = partyMembers[j].LastActive.Time
		}
		return ti.After(tj)
	})

	// Helper to format time-ago for tooltips
	formatTimeAgo := func(t time.Time) string {
		dur := time.Since(t)
		minutes := int(dur.Minutes())
		hours := int(dur.Hours())
		days := hours / 24
		if days >= 2 {
			return fmt.Sprintf("%d+ days ago", days)
		}
		if days >= 1 {
			return "1+ days ago"
		}
		if hours >= 1 {
			return fmt.Sprintf("~%d hours ago", hours)
		}
		if minutes >= 1 {
			return fmt.Sprintf("~%d minutes ago", minutes)
		}
		return "just now"
	}

	// The party: the encounter stage's tokens, the Party & Initiative rail's cards and the quick sheet's list, from the health owner
	// (game.Vitals.Health) and the stored Hit Points and Armor Class; the order of the turn holder first, then the most recently active.
	var cards []liveCard
	for _, pm := range partyMembers {
		health := game.Vitals{HP: pm.HP, Max: pm.MaxHP, Stable: pm.Stable, Dead: pm.Dead}.Health() // the health owner
		activity := "No activity recorded"
		if pm.LastActive.Valid {
			activity = "Active " + formatTimeAgo(pm.LastActive.Time)
		}
		card := liveCard{ID: pm.CharID, Name: pm.CharName, Icon: classIcon(pm.Class),
			Meta: fmt.Sprintf("Level %d %s %s", pm.Level, titleCase(pm.Species), titleCase(pm.Class)), HP: pm.HP, MaxHP: pm.MaxHP, AC: pm.AC,
			Health: health, IsTurn: combatActive && pm.CharName == currentTurnName, HasPortrait: pm.HasPortrait, PortraitAt: pm.PortraitAt,
			Activity: activity}
		if err := liveCardExtras(&card); err != nil { // Temporary Hit Points, Inspiration, saves, Hit Dice, conditions, companions (live_cards_2024.go)
			writeDBErrorPage(w, err, "the party's cards")
			return
		}
		cards = append(cards, card)
	}
	gm := liveGM{}
	if dmName.Valid && dmID.Valid {
		gm = liveGM{ID: int(dmID.Int64), Name: dmName.String}
	}

	// Get observations
	var observations []liveObservation
	obsRows, qerr := db.Query(`
		SELECT o.content, COALESCE(o.observation_type, 'world'), a.name, o.created_at
		FROM observations o
		JOIN characters c ON o.observer_id = c.id
		JOIN agents a ON c.agent_id = a.id
		WHERE o.lobby_id = $1
		ORDER BY o.created_at DESC LIMIT 20
	`, campaignID)
	if qerr != nil {
		writeDBErrorPage(w, qerr, "the campaign's observations")
		return
	}
	for obsRows.Next() {
		var o liveObservation
		var obsTime time.Time
		obsRows.Scan(&o.Content, &o.Type, &o.Observer, &obsTime)
		o.Time = feedTime(obsTime, campaignID)
		observations = append(observations, o)
	}
	obsRows.Close()

	var doc map[string]interface{}
	var docRaw []byte
	db.QueryRow(`SELECT COALESCE(campaign_document, '{}') FROM lobbies WHERE id = $1`, campaignID).Scan(&docRaw)
	json.Unmarshal(docRaw, &doc)
	location := ""
	for _, k := range []string{"current_location", "location"} {
		if s, ok := doc[k].(string); ok && strings.TrimSpace(s) != "" {
			location = strings.TrimSpace(s)
			break
		}
	}

	page := liveTablePage(liveTable{
		ID: campaignID, Name: name, Status: status, Levels: formatLevelRequirement(minLevel, maxLevel), Setting: setting,
		GM: gm, Players: playerCount, MaxPlayers: maxPlayers, Created: createdAt.Format("January 2, 2006"), Location: location,
		Combat: combatActive, Round: combatRound, TurnName: currentTurnName, Enemies: liveEnemies(turnOrderJSON, combatActive),
		Party: cards, Observations: observations, TableRules: tableRulesHTML(campaignID), Background: backgroundURL(campaignID),
	})
	fmt.Fprint(w, wrapView(name+" - Agent RPG 2024", page))
}

// handleCampaignLog shows the full action log for a campaign with pagination
func handleCampaignLog(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var campaignName, gmName string
	err := db.QueryRow(`SELECT l.name, COALESCE(a.name, '') FROM lobbies l LEFT JOIN agents a ON a.id = l.dm_id WHERE l.id = $1`, campaignID).Scan(&campaignName, &gmName)
	if err == sql.ErrNoRows {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeDBErrorPage(w, err, "the campaign")
		return
	}
	page, limit := 1, 100
	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	offset := (page - 1) * limit
	var totalActions int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1 AND NOT (action_type = 'poll' AND description = 'Checked game status')`, campaignID).Scan(&totalActions)
	totalPages := (totalActions + limit - 1) / limit
	if totalPages == 0 {
		totalPages = 1
	}
	// the actions, newest first, without the routine status polls (action_log_2024.go); players' words only (playerText)
	recs, _ := readActions(actionFilter{lobby: campaignID, newestFirst: true, limit: limit, offset: offset})
	var feed []liveFeedItem
	for _, a := range recs {
		feed = append(feed, liveFeedItem{At: a.At, Type: a.Type, Actor: a.Actor, Text: playerText(a.Description), Result: playerText(a.Result), Time: feedTime(a.At, campaignID)})
	}
	page2 := chroniclePage(campaignID, campaignName, gmName, feed, totalActions, min(offset+1, totalActions), min(offset+limit, totalActions), page, totalPages)
	fmt.Fprint(w, wrapView(fmt.Sprintf("Chronicle: %s - Agent RPG 2024", campaignName), page2))
}

// handleCampaignItems godoc
// @Summary List or create campaign items
// @Description GET: List all custom items for a campaign. POST: Create a new custom item (GM only).
// @Tags Campaign Items
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Bearer <token>"
// @Body Item details (POST only). Use copy_from_universe to clone from /universe/
// @Success 200 {object} map[string]interface{} "List of items or creation result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Router /campaigns/{id}/items [get]
// @Router /campaigns/{id}/items [post]
func handleCampaignItems(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	agentID, _ := getAgentFromAuth(r)                 // the list says whether the caller is the GM; signing in is not needed to read it
	if _, ok := requireCampaign(w, campaignID); !ok { // campaign_access_2024.go: 404, or the database failure
		return
	}
	role, _, _ := campaignRole(agentID, campaignID)
	isGM := role == "gm"

	if r.Method == "GET" {
		// Anyone in the campaign can list items
		rows, err := db.Query(`
			SELECT slug, item_type, name, data, created_at 
			FROM campaign_items 
			WHERE lobby_id = $1 
			ORDER BY item_type, name
		`, campaignID)
		if err != nil {
			writeDBError(w, err, "the campaign's items")
			return
		}
		defer rows.Close()

		items := []map[string]interface{}{}
		for rows.Next() {
			var slug, itemType, name string
			var data []byte
			var createdAt time.Time
			rows.Scan(&slug, &itemType, &name, &data, &createdAt)

			var itemData map[string]interface{}
			json.Unmarshal(data, &itemData)

			items = append(items, map[string]interface{}{
				"slug":       slug,
				"item_type":  itemType,
				"name":       name,
				"data":       itemData,
				"created_at": createdAt.Format(time.RFC3339),
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": items,
			"count": len(items),
			"is_gm": isGM,
		})
		return
	}

	if r.Method == "POST" {
		if _, ok := requireCampaignGM(w, r, campaignID, "create campaign items"); !ok {
			return
		}

		var req struct {
			ItemType         string                 `json:"item_type"`          // the kind of item (weapon, armor, magic ...); taken from the copied item with copy_from_universe
			Slug             string                 `json:"slug"`               // the item's slug in the campaign (default: the copied item's slug plus -custom)
			Name             string                 `json:"name"`               // the item's name
			Data             map[string]interface{} `json:"data"`               // the item's data (with copy_from_universe, merged over the copied item's)
			CopyFromUniverse string                 `json:"copy_from_universe"` // the slug of a catalog item to copy (GET /api/universe/items)
		}
		if !readRequest(w, r, &req, optionalBody()) {
			return
		}

		// If copying from universe
		if req.CopyFromUniverse != "" {
			item, itemType, err := getUniverseItem(req.CopyFromUniverse)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "universe_item_not_found", "slug": req.CopyFromUniverse})
				return
			}
			req.ItemType = itemType
			if req.Slug == "" {
				req.Slug = req.CopyFromUniverse + "-custom"
			}
			if req.Name == "" {
				if name, ok := item["name"].(string); ok {
					req.Name = name + " (Custom)"
				}
			}
			// Merge provided data with universe item data
			if req.Data == nil {
				req.Data = item
			} else {
				for k, v := range item {
					if _, exists := req.Data[k]; !exists {
						req.Data[k] = v
					}
				}
			}
		}

		// Validate
		if req.ItemType == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "item_type_required", "valid_types": []string{"weapon", "armor", "item"}})
			return
		}
		if req.ItemType != "weapon" && req.ItemType != "armor" && req.ItemType != "item" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_item_type", "valid_types": []string{"weapon", "armor", "item"}})
			return
		}
		if req.Slug == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "slug_required"})
			return
		}
		if req.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "name_required"})
			return
		}
		if req.Data == nil {
			req.Data = map[string]interface{}{}
		}

		// Ensure name is in data
		req.Data["name"] = req.Name

		dataJSON, _ := json.Marshal(req.Data)

		_, err := db.Exec(`
			INSERT INTO campaign_items (lobby_id, item_type, slug, name, data)
			VALUES ($1, $2, $3, $4, $5)
		`, campaignID, req.ItemType, req.Slug, req.Name, dataJSON)

		if err != nil {
			if strings.Contains(err.Error(), "unique") {
				handledDBError(err) // answered: the slug is taken (db_guard_2024.go)
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "slug_already_exists"})
			} else {
				writeDBWriteError(w, err, "the item")
			}
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"slug":      req.Slug,
			"item_type": req.ItemType,
			"name":      req.Name,
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleCampaignItemBySlug godoc
// @Summary Read, change or delete one campaign item
// @Description GET: one custom item of the campaign (anyone). PUT: change its name or merge fields into its data (GM only). DELETE: remove it (GM only).
// @Tags Campaign Items
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param slug path string true "The item's slug"
// @Param Authorization header string true "Bearer <token>"
// @Body The new name and the data fields to merge (PUT only)
// @Success 200 {object} map[string]interface{} "The item, the changed item, or the deleted slug"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 404 {object} map[string]interface{} "No such item"
// @Router /campaigns/{id}/items/{slug} [get]
// @Router /campaigns/{id}/items/{slug} [put]
// @Router /campaigns/{id}/items/{slug} [delete]
func handleCampaignItemBySlug(w http.ResponseWriter, r *http.Request, campaignID int, slug string) {
	w.Header().Set("Content-Type", "application/json")

	if _, ok := requireCampaign(w, campaignID); !ok { // anyone may read an item; the campaign must exist
		return
	}

	if r.Method == "GET" {
		var itemType, name string
		var data []byte
		var createdAt time.Time
		err := db.QueryRow(`
			SELECT item_type, name, data, created_at 
			FROM campaign_items 
			WHERE lobby_id = $1 AND slug = $2
		`, campaignID, slug).Scan(&itemType, &name, &data, &createdAt)

		if answerDBFailure(w, err, "the item") {
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "item_not_found"})
			return
		}

		var itemData map[string]interface{}
		json.Unmarshal(data, &itemData)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"slug":       slug,
			"item_type":  itemType,
			"name":       name,
			"data":       itemData,
			"created_at": createdAt.Format(time.RFC3339),
		})
		return
	}

	if r.Method == "PUT" {
		if _, ok := requireCampaignGM(w, r, campaignID, "change campaign items"); !ok {
			return
		}

		var req struct {
			Name string                 `json:"name"` // a new name for the item
			Data map[string]interface{} `json:"data"` // fields merged into the item's data
		}
		if !readRequest(w, r, &req, optionalBody()) {
			return
		}

		// Get existing item
		var existingData []byte
		var existingName string
		err := db.QueryRow("SELECT name, data FROM campaign_items WHERE lobby_id = $1 AND slug = $2", campaignID, slug).Scan(&existingName, &existingData)
		if answerDBFailure(w, err, "the item") {
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "item_not_found"})
			return
		}

		// Merge data
		var itemData map[string]interface{}
		json.Unmarshal(existingData, &itemData)

		if req.Data != nil {
			for k, v := range req.Data {
				itemData[k] = v
			}
		}

		name := existingName
		if req.Name != "" {
			name = req.Name
			itemData["name"] = name
		}

		dataJSON, _ := json.Marshal(itemData)

		_, err = db.Exec(`
			UPDATE campaign_items SET name = $1, data = $2 WHERE lobby_id = $3 AND slug = $4
		`, name, dataJSON, campaignID, slug)

		if err != nil {
			writeDBWriteError(w, err, "the item")
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"slug":    slug,
			"name":    name,
			"data":    itemData,
		})
		return
	}

	if r.Method == "DELETE" {
		if _, ok := requireCampaignGM(w, r, campaignID, "delete campaign items"); !ok {
			return
		}

		result, err := db.Exec("DELETE FROM campaign_items WHERE lobby_id = $1 AND slug = $2", campaignID, slug)
		if err != nil {
			writeDBWriteError(w, err, "the item's deletion")
			return
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "item_not_found"})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "deleted": slug})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
