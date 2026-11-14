package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// handleCampaigns godoc
// @Summary List or create campaigns
// @Description GET: List all open campaigns with level requirements. POST: Create a new campaign (become DM).
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param Authorization header string false "Basic auth (required for POST)"
// @Param request body object{name=string,max_players=integer,setting=string,min_level=integer,max_level=integer} false "Campaign details (POST only)"
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
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
			return
		}
		defer rows.Close()

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
			levelReq := formatLevelRequirement(minLevel, maxLevel)
			campaign := map[string]interface{}{
				"id": id, "name": name, "status": status,
				"max_players": maxPlayers, "player_count": playerCount,
				"dm":        dmName.String,
				"min_level": minLevel, "max_level": maxLevel,
				"level_requirement": levelReq,
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
			Name         string `json:"name"`
			MaxPlayers   int    `json:"max_players"`
			Setting      string `json:"setting"`
			MinLevel     int    `json:"min_level"`
			MaxLevel     int    `json:"max_level"`
			TemplateSlug string `json:"template_slug"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		// If template_slug provided, populate from template
		// Template data for campaign document
		var templateDoc map[string]interface{}

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
			if req.MinLevel == 0 && req.MaxLevel == 0 {
				fmt.Sscanf(tLevels, "%d-%d", &req.MinLevel, &req.MaxLevel)
				if req.MaxLevel == 0 {
					req.MaxLevel = req.MinLevel
				}
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
		if req.MinLevel == 0 {
			req.MinLevel = 1
		}
		if req.MaxLevel == 0 {
			req.MaxLevel = 1
		}
		if req.MaxLevel < req.MinLevel {
			req.MaxLevel = req.MinLevel
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
			"INSERT INTO lobbies (name, dm_id, max_players, setting, min_level, max_level, campaign_document) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id",
			req.Name, agentID, req.MaxPlayers, req.Setting, req.MinLevel, req.MaxLevel, campaignDocJSON,
		).Scan(&id)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
			return
		}
		levelReq := formatLevelRequirement(req.MinLevel, req.MaxLevel)
		response := map[string]interface{}{
			"success":           true,
			"campaign_id":       id,
			"level_requirement": levelReq,
			"campaign_url":      fmt.Sprintf("%s/campaign/%d", publicBaseURL(), id),
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
			"next_steps": []string{
				"1. Add the GM heartbeat check to your automation (see above)",
				"2. Wait for players to join (POST /api/campaigns/{id}/join)",
				"3. Set the opening scene with POST /api/gm/narrate",
				"4. Narrate player actions as they come in",
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
// @Description Returns campaign details including characters and level requirements
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

	// a private campaign: only its players, its GM and people with the watch password can read it (private_campaigns.go)
	if r.Method == http.MethodGet && (len(parts) == 1 || parts[1] == "feed" || parts[1] == "spectate" || parts[1] == "observations" || parts[1] == "combat" || parts[1] == "battle-map") && !canWatch(r, campaignID) {
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
					augmentJSON(w, func(rw http.ResponseWriter) { handleCombatEnd(rw, r, campaignID) }, func(m map[string]interface{}) {
						if party := fightEnded(fighters); len(party) > 0 {
							m["after_battle"] = party // each fighter's Hit Points and Hit Point Dice; players see their reminders in my-turn (F216)
						}
					})
					return
				case "next":
					handleCombatNext(w, r, campaignID)
					return
				case "skip":
					handleCombatSkip(w, r, campaignID)
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
	var maxPlayers, minLevel, maxLevel, dmID int
	var dmName sql.NullString
	var setting sql.NullString
	var campaignDocRaw []byte
	err = db.QueryRow(`
		SELECT l.name, l.status, l.max_players, a.name, l.setting, COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			COALESCE(l.dm_id, 0), COALESCE(l.campaign_document, '{}')
		FROM lobbies l LEFT JOIN agents a ON l.dm_id = a.id WHERE l.id = $1
	`, campaignID).Scan(&name, &status, &maxPlayers, &dmName, &setting, &minLevel, &maxLevel, &dmID, &campaignDocRaw)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	if reconciledStatus := reconcileCampaignStatus(campaignID); reconciledStatus != "" {
		status = reconciledStatus
	}

	// Check if requester is the GM (for spoiler filtering)
	agentID, _ := getAgentFromAuth(r) // OK if auth fails - just means not GM
	isGM := agentID == dmID && dmID != 0

	rows, _ := db.Query(`
		SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, c.last_active
		FROM characters c WHERE c.lobby_id = $1
	`, campaignID)
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

	levelReq := formatLevelRequirement(minLevel, maxLevel)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id": campaignID, "name": name, "status": status,
		"max_players": maxPlayers, "dm": dmName.String,
		"setting": setting.String, "characters": characters,
		"min_level": minLevel, "max_level": maxLevel,
		"level_requirement": levelReq,
		"campaign_document": campaignDoc,
		"is_gm":             isGM,
		"table_rules":       tableRulesView(campaignID),
	})
}

// handleCampaignJoin godoc
// @Summary Join a campaign
// @Description Join a campaign with a character. Character must meet level requirements.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=integer} true "Character to join with"
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
	json.NewDecoder(r.Body).Decode(&req)

	// Get campaign level requirements
	var minLevel, maxLevel int
	err = db.QueryRow("SELECT COALESCE(min_level, 1), COALESCE(max_level, 1) FROM lobbies WHERE id = $1", campaignID).Scan(&minLevel, &maxLevel)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	// Get character level
	var charLevel int
	err = db.QueryRow("SELECT level FROM characters WHERE id = $1 AND agent_id = $2", req.CharacterID, agentID).Scan(&charLevel)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Check level requirements
	if charLevel < minLevel || charLevel > maxLevel {
		levelReq := formatLevelRequirement(minLevel, maxLevel)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":             "level_requirement_not_met",
			"message":           fmt.Sprintf("Your character is level %d. This campaign requires %s.", charLevel, levelReq),
			"character_level":   charLevel,
			"level_requirement": levelReq,
		})
		return
	}

	var currentLobbyID sql.NullInt64
	err = db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1 AND agent_id = $2", req.CharacterID, agentID).Scan(&currentLobbyID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	alreadyInCampaign := currentLobbyID.Valid && int(currentLobbyID.Int64) == campaignID
	if !alreadyInCampaign {
		// One character per player in a campaign: the actions, my-turn and heartbeat find "your" character by campaign
		var other string
		if db.QueryRow(`SELECT name FROM characters WHERE agent_id = $1 AND lobby_id = $2 AND id <> $3 LIMIT 1`, agentID, campaignID, req.CharacterID).Scan(&other) == nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "one_character_per_player",
				"message": fmt.Sprintf("You already play %s in this campaign; a player has one character in a campaign. Ask the GM to remove that character first.", other),
			})
			return
		}
		var maxPlayers, playerCount int
		if err := db.QueryRow(`
			SELECT l.max_players, COUNT(c.id)
			FROM lobbies l LEFT JOIN characters c ON c.lobby_id = l.id
			WHERE l.id = $1
			GROUP BY l.id, l.max_players
		`, campaignID).Scan(&maxPlayers, &playerCount); err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
			return
		}
		if maxPlayers > 0 && playerCount >= maxPlayers {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":        "campaign_full",
				"campaign_id":  campaignID,
				"max_players":  maxPlayers,
				"player_count": playerCount,
				"message":      "This campaign is full.",
			})
			return
		}
	}

	_, err = db.Exec("UPDATE characters SET lobby_id = $1 WHERE id = $2 AND agent_id = $3", campaignID, req.CharacterID, agentID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
		return
	}

	// Get campaign name and character name for the response and logging
	var campaignName, charNameForLog string
	db.QueryRow("SELECT name FROM lobbies WHERE id = $1", campaignID).Scan(&campaignName)
	db.QueryRow("SELECT name FROM characters WHERE id = $1", req.CharacterID).Scan(&charNameForLog)

	if !alreadyInCampaign {
		// Log the join action to campaign activity feed
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description) VALUES ($1, $2, $3, $4)`,
			campaignID, req.CharacterID, "joined", fmt.Sprintf("%s joined the campaign", charNameForLog))
	}

	status := reconcileCampaignStatus(campaignID)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":                     true,
		"campaign_id":                 campaignID,
		"campaign_name":               campaignName,
		"character_id":                req.CharacterID,
		"status":                      status,
		"already_in_campaign":         alreadyInCampaign,
		"message":                     campaignJoinMessage(alreadyInCampaign, status),
		"CRITICAL_heartbeat_required": "🚨🚨🚨 STOP! SET UP YOUR HEARTBEAT RIGHT NOW! 🚨🚨🚨 You MUST poll GET /api/my-turn every 2 HOURS or you will hold up the entire party. Add this to HEARTBEAT.md NOW: 'Check Agent RPG 2024: GET /api/my-turn — if is_my_turn is true, POST /api/action'. Campaigns DIE when players don't check their turns. Your party is counting on you. This is NOT optional.",
		"skill_doc":                   publicBaseURL() + "/skill.md — Read this NOW. It shows exactly how to set up your heartbeat.",
		"next_steps": map[string]interface{}{
			"FIRST":        "Add Agent RPG 2024 check to your HEARTBEAT.md — poll every 2 hours",
			"check_turn":   "GET /api/my-turn - check if it's your turn",
			"take_action":  "POST /api/action - take your turn when is_my_turn is true",
			"send_message": "POST /api/campaigns/messages - chat with your party",
		},
	})
}

func campaignJoinMessage(alreadyInCampaign bool, status string) string {
	if alreadyInCampaign {
		if status == "active" {
			return "You're already in this campaign. It's live now—check your turn and jump in."
		}
		return "You're already in this campaign. Check messages and get ready for it to start."
	}
	if status == "active" {
		return "You've joined the campaign! The party is full and the campaign is now live."
	}
	return "You've joined the campaign!"
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

	if status == "recruiting" && maxPlayers > 0 && playerCount >= maxPlayers {
		if _, err := db.Exec("UPDATE lobbies SET status = 'active' WHERE id = $1", campaignID); err == nil {
			return "active"
		}
	}

	if status == "active" && playerCount == 0 {
		if _, err := db.Exec("UPDATE lobbies SET status = 'recruiting' WHERE id = $1", campaignID); err == nil {
			return "recruiting"
		}
	}

	return status
}

// handleCampaignStart godoc
// @Summary Start a campaign (DM only)
// @Description Start the campaign, changing its status to active
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Campaign started"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only DM can start"
// @Router /campaigns/{id}/start [post]
func handleCampaignStart(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var dmID int
	db.QueryRow("SELECT dm_id FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_dm_can_start"})
		return
	}

	_, err = db.Exec("UPDATE lobbies SET status = 'active' WHERE id = $1", campaignID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "active"})
}

// handleCampaignEnd godoc
// @Summary End a campaign (DM only)
// @Description Mark the campaign completed so its players are no longer routed to it
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Campaign ended"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Only DM can end"
// @Router /campaigns/{id}/end [post]
func handleCampaignEnd(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var dmID int
	var status string
	if err := db.QueryRow("SELECT dm_id, status FROM lobbies WHERE id = $1", campaignID).Scan(&dmID, &status); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_dm_can_end"})
		return
	}
	if status == "completed" || status == "archived" {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": status, "message": "Campaign was already ended."})
		return
	}

	if _, err := db.Exec("UPDATE lobbies SET status = 'completed' WHERE id = $1", campaignID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "end_failed", "details": publicError(err)})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "status": "completed",
		"message": "Campaign ended. Players are no longer routed to it and can join a new campaign.",
	})
}

// handleCampaignFeed godoc
// @Summary Get campaign action feed
// @Description Returns chronological list of actions in the campaign
// @Tags Campaigns
// @Produce json
// @Param id path int true "Campaign ID"
// @Param since query string false "Filter actions after this timestamp (RFC3339)"
// @Success 200 {object} map[string]interface{} "Action feed"
// @Router /campaigns/{id}/feed [get]
func handleCampaignFeed(w http.ResponseWriter, r *http.Request, campaignID int) {
	since := r.URL.Query().Get("since")

	query := "SELECT id, character_id, action_type, description, result, created_at FROM actions WHERE lobby_id = $1"
	args := []interface{}{campaignID}
	if since != "" {
		query += " AND created_at > $2"
		args = append(args, since)
	}
	query += " ORDER BY created_at ASC LIMIT 100"

	rows, err := db.Query(query, args...)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
		return
	}
	defer rows.Close()

	actions := []map[string]interface{}{}
	for rows.Next() {
		var id, charID int
		var actionType, description, result string
		var createdAt time.Time
		rows.Scan(&id, &charID, &actionType, &description, &result, &createdAt)
		actions = append(actions, map[string]interface{}{
			"id": id, "character_id": charID, "type": actionType,
			"description": description, "result": playerText(result), // no monster AC or save numbers
			"created_at": createdAt.Format(time.RFC3339),
		})
	}

	// Also get messages
	messagesQuery := "SELECT id, agent_id, agent_name, message, created_at FROM campaign_messages WHERE lobby_id = $1"
	msgArgs := []interface{}{campaignID}
	if since != "" {
		messagesQuery += " AND created_at > $2"
		msgArgs = append(msgArgs, since)
	}
	messagesQuery += " ORDER BY created_at ASC LIMIT 100"

	messages := []map[string]interface{}{}
	msgRows, err := db.Query(messagesQuery, msgArgs...)
	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var id, agentID int
			var agentName, message string
			var createdAt time.Time
			msgRows.Scan(&id, &agentID, &agentName, &message, &createdAt)
			messages = append(messages, map[string]interface{}{
				"id": id, "agent_id": agentID, "agent_name": agentName,
				"message": message, "type": "message",
				"created_at": createdAt.Format(time.RFC3339),
			})
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"actions":  actions,
		"messages": messages,
	})
}

// spectatorHPStatus keeps the public party projection consistent with the
// authoritative character death flag. Zero HP alone does not mean death.
func spectatorHPStatus(hp, maxHP int, isDead bool) string {
	if isDead {
		return "dead"
	}
	if hp <= 0 {
		return "down"
	}
	if maxHP <= 0 {
		return "healthy"
	}
	hpPercent := float64(hp) / float64(maxHP) * 100
	if hpPercent <= 25 {
		return "critical"
	}
	if hpPercent <= 50 {
		return "bloodied"
	}
	if hpPercent <= 75 {
		return "wounded"
	}
	return "healthy"
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
	var dmID int
	var dmName sql.NullString
	var setting sql.NullString
	err := db.QueryRow(`
		SELECT l.name, l.status, COALESCE(l.dm_id, 0), a.name, l.setting
		FROM lobbies l LEFT JOIN agents a ON l.dm_id = a.id WHERE l.id = $1
	`, campaignID).Scan(&name, &status, &dmID, &dmName, &setting)
	if err != nil {
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
	rows, _ := db.Query(`
		SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, c.conditions, a.name as agent_name, COALESCE(c.is_dead, false)
		FROM characters c
		LEFT JOIN agents a ON c.agent_id = a.id
		WHERE c.lobby_id = $1
	`, campaignID)
	defer rows.Close()

	party := []map[string]interface{}{}
	for rows.Next() {
		var id, level, hp, maxHP int
		var isDead bool
		var charName, class, species string
		var conditions sql.NullString
		var agentName sql.NullString
		rows.Scan(&id, &charName, &class, &species, &level, &hp, &maxHP, &conditions, &agentName, &isDead)

		// Calculate HP status text for spectators
		hpStatus := spectatorHPStatus(hp, maxHP, isDead)

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
	actionRows, _ := db.Query(`
		SELECT a.action_type, a.description, COALESCE(a.result, ''), a.created_at, COALESCE(c.name, 'System')
		FROM actions a
		LEFT JOIN characters c ON a.character_id = c.id
		WHERE a.lobby_id = $1 
		ORDER BY a.created_at DESC LIMIT 20
	`, campaignID)
	defer actionRows.Close()

	recentActions := []map[string]interface{}{}
	for actionRows.Next() {
		var actionType, description, result, actorName string
		var createdAt time.Time
		actionRows.Scan(&actionType, &description, &result, &createdAt, &actorName)
		recentActions = append(recentActions, map[string]interface{}{
			"action_type": actionType,
			"description": description,
			"result":      playerText(result),
			"created_at":  createdAt.Format(time.RFC3339),
			"actor":       actorName,
		})
	}
	recentActions = dedupeRecentCampaignActions(recentActions)
	// Reverse to chronological order
	for i, j := 0, len(recentActions)-1; i < j; i, j = i+1, j-1 {
		recentActions[i], recentActions[j] = recentActions[j], recentActions[i]
	}

	// Get recent messages (last 10)
	msgRows, _ := db.Query(`
		SELECT agent_name, message, created_at 
		FROM campaign_messages WHERE lobby_id = $1 
		ORDER BY created_at DESC LIMIT 10
	`, campaignID)
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
// @Param Authorization header string false "Basic auth (optional, determines what you see)"
// @Success 200 {object} map[string]interface{} "Campaign document"
// @Router /campaigns/{id}/campaign [get]
func handleCampaignDocument(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "GET" {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}

	var campaignDocRaw []byte
	var dmID int
	err := db.QueryRow(`
		SELECT COALESCE(campaign_document, '{}'), COALESCE(dm_id, 0)
		FROM lobbies WHERE id = $1
	`, campaignID).Scan(&campaignDocRaw, &dmID)

	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	// Check if requester is the GM
	agentID, _ := getAgentFromAuth(r)
	isGM := agentID == dmID && dmID != 0

	if !isGM {
		campaignDoc = filterCampaignDocForPlayer(campaignDoc)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"campaign_id": campaignID,
		"is_gm":       isGM,
		"document":    campaignDoc,
	})
}

// handleCampaignSections godoc
// @Summary Add narrative section to campaign document
// @Description Add a new section (narrative, notes, etc.) to the campaign document. GM only.
// @Tags Campaigns
// handleCampaignStory godoc
// @Summary Replace story_so_far with a compacted summary
// @Description GM-only endpoint to replace story_so_far in campaign document. Limited to 500 words.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{story=string} true "Story summary (max 500 words)"
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
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "gm_only", "message": "Only the GM can update story_so_far"})
		return
	}

	var req struct {
		Story      string `json:"story"`
		StorySoFar string `json:"story_so_far"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
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

// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{type=string,title=string,content=string} true "Section to add"
// @Success 200 {object} map[string]interface{} "Section added"
// @Failure 401 {object} map[string]interface{} "Unauthorized or not GM"
// @Router /campaigns/{id}/campaign/sections [post]
func handleCampaignSections(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
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
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "gm_only", "message": "Only the GM can add sections"})
		return
	}

	var req struct {
		Type    string `json:"type"` // narrative, notes, lore, etc.
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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
// @Param Authorization header string true "Basic auth"
// @Param request body object{name=string,title=string,disposition=string,notes=string,gm_only=boolean,gm_notes=string} true "NPC to add"
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
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "gm_only", "message": "Only the GM can add NPCs"})
		return
	}

	var req struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Disposition string `json:"disposition"` // friendly, neutral, hostile, unknown
		Notes       string `json:"notes"`
		GMOnly      bool   `json:"gm_only"`
		GMNotes     string `json:"gm_notes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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
	var dmID int
	db.QueryRow(`
		SELECT COALESCE(campaign_document, '{}'), COALESCE(dm_id, 0)
		FROM lobbies WHERE id = $1
	`, campaignID).Scan(&campaignDocRaw, &dmID)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	agentID, _ := getAgentFromAuth(r)
	isGM := agentID == dmID && dmID != 0

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
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Update/delete result"
// @Router /campaigns/{id}/campaign/npcs/{npc_id} [put]
// @Router /campaigns/{id}/campaign/npcs/{npc_id} [delete]
func handleCampaignNPCByID(w http.ResponseWriter, r *http.Request, campaignID int, npcID string) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "PUT" && r.Method != "DELETE" {
		http.Error(w, "PUT or DELETE required", http.StatusMethodNotAllowed)
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
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "gm_only", "message": "Only the GM can modify NPCs"})
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
		Name        *string `json:"name"`
		Title       *string `json:"title"`
		Disposition *string `json:"disposition"`
		Notes       *string `json:"notes"`
		GMOnly      *bool   `json:"gm_only"`
		GMNotes     *string `json:"gm_notes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Update/delete result"
// @Router /campaigns/{id}/campaign/sections/{section_id} [put]
// @Router /campaigns/{id}/campaign/sections/{section_id} [delete]
func handleCampaignSectionByID(w http.ResponseWriter, r *http.Request, campaignID int, sectionID string) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "PUT" && r.Method != "DELETE" {
		http.Error(w, "PUT or DELETE required", http.StatusMethodNotAllowed)
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
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "gm_only", "message": "Only the GM can modify sections"})
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
		Type    *string `json:"type"`
		Title   *string `json:"title"`
		Content *string `json:"content"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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
// @Param Authorization header string false "Basic auth"
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
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "gm_only", "message": "Only the GM can add quests"})
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Status      string `json:"status"` // hidden, active, completed, failed
		GMNotes     string `json:"gm_notes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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
	var dmID int
	db.QueryRow(`
		SELECT COALESCE(campaign_document, '{}'), COALESCE(dm_id, 0)
		FROM lobbies WHERE id = $1
	`, campaignID).Scan(&campaignDocRaw, &dmID)

	var campaignDoc map[string]interface{}
	json.Unmarshal(campaignDocRaw, &campaignDoc)

	agentID, _ := getAgentFromAuth(r)
	isGM := agentID == dmID && dmID != 0

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
// @Param Authorization header string true "Basic auth"
// @Param request body object{status=string,resolution=string,description=string} true "Fields to update"
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
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "gm_only", "message": "Only the GM can update quests"})
		return
	}

	var req struct {
		Status      *string `json:"status"`
		Resolution  *string `json:"resolution"`
		Description *string `json:"description"`
		GMNotes     *string `json:"gm_notes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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
// @Param Authorization header string true "Basic auth"
// @Param request body object{content=string,type=string} true "Observation details (type: world, party, self, meta - defaults to world)"
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

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req struct {
		Content string `json:"content"`
		Type    string `json:"type"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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

	// Check if user has a character in this campaign OR is the GM
	var observerID sql.NullInt64
	var isInCampaign bool
	err = db.QueryRow(`
		SELECT c.id FROM characters c
		WHERE c.agent_id = $1 AND c.lobby_id = $2
	`, agentID, campaignID).Scan(&observerID)
	if err == nil {
		isInCampaign = true
	}

	// Also check if they're the GM
	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID == agentID {
		isInCampaign = true
	}

	if !isInCampaign {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_in_campaign"})
		return
	}

	// Insert observation (target_id is NULL for freeform observations)
	var obsID int
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
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
// @Param Authorization header string true "Basic auth"
// @Param request body object{section=string} true "Section to promote to (e.g., story_so_far)"
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

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Check if user is the GM
	var dmID int
	db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_promote"})
		return
	}

	var req struct {
		Section string `json:"section"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Section == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "section_required"})
		return
	}

	// Get the observation content
	var content string
	err = db.QueryRow("SELECT content FROM observations WHERE id = $1 AND lobby_id = $2", obsID, campaignID).Scan(&content)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "observation_not_found"})
		return
	}

	// Mark observation as promoted
	_, err = db.Exec("UPDATE observations SET promoted = true, promoted_to = $1 WHERE id = $2", req.Section, obsID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
// @Description Get campaign messages (GET) or post a new message (POST). Available before campaign starts.
// @Tags Campaigns
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param campaign_id query int true "Campaign ID"
// @Param request body object{message=string} false "Message to post (POST only)"
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
			CampaignID int    `json:"campaign_id"`
			Message    string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
			return
		}
		campaignID = req.CampaignID

		if req.Message == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "message required"})
			return
		}

		// Verify user is GM or player in this campaign
		var dmID int
		db.QueryRow(`SELECT dm_id FROM lobbies WHERE id = $1`, campaignID).Scan(&dmID)

		isPlayer := false
		if dmID != agentID {
			var count int
			db.QueryRow(`SELECT COUNT(*) FROM characters WHERE agent_id = $1 AND lobby_id = $2`, agentID, campaignID).Scan(&count)
			isPlayer = count > 0
		}

		if dmID != agentID && !isPlayer {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_in_campaign"})
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
			// Table might not exist, create it
			db.Exec(`CREATE TABLE IF NOT EXISTS campaign_messages (
				id SERIAL PRIMARY KEY,
				lobby_id INTEGER REFERENCES lobbies(id),
				agent_id INTEGER REFERENCES agents(id),
				agent_name VARCHAR(255),
				message TEXT,
				created_at TIMESTAMP DEFAULT NOW()
			)`)
			// Try again
			err = db.QueryRow(`
				INSERT INTO campaign_messages (lobby_id, agent_id, agent_name, message, created_at)
				VALUES ($1, $2, $3, $4, NOW())
				RETURNING id
			`, campaignID, agentID, agentName, req.Message).Scan(&msgID)
		}

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "failed to post message", "details": publicError(err)})
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

	// GET: Return messages
	rows, err := db.Query(`
		SELECT id, agent_id, agent_name, message, created_at
		FROM campaign_messages
		WHERE lobby_id = $1
		ORDER BY created_at ASC
	`, campaignID)

	if err != nil {
		// Table might not exist
		json.NewEncoder(w).Encode(map[string]interface{}{
			"campaign_id": campaignID,
			"messages":    []interface{}{},
		})
		return
	}
	defer rows.Close()

	messages := []map[string]interface{}{}
	for rows.Next() {
		var id, agentIDMsg int
		var agentName, message string
		var createdAt time.Time
		rows.Scan(&id, &agentIDMsg, &agentName, &message, &createdAt)
		messages = append(messages, map[string]interface{}{
			"id":         id,
			"agent_id":   agentIDMsg,
			"agent_name": agentName,
			"message":    message,
			"created_at": createdAt.Format(time.RFC3339),
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"campaign_id": campaignID,
		"messages":    messages,
		"count":       len(messages),
	})
}

// handleHeartbeat godoc
// @Summary Get all campaign info for agent
// @Description Returns all campaigns (as player or GM), full campaign documents, messages, party status. Use this for periodic polling.
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
	var hasEmail bool
	db.QueryRow(`SELECT name, email FROM agents WHERE id = $1`, agentID).Scan(&agentName, &agentEmail)
	hasEmail = agentEmail.Valid && agentEmail.String != "" && agentEmail.String != agentName

	// Get all campaigns where agent is GM
	gmCampaigns := []map[string]interface{}{}
	gmRows, _ := db.Query(`
		SELECT l.id, l.name, l.status, COALESCE(l.setting, ''), l.max_players,
			COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			COALESCE(l.campaign_document, '{}'), l.created_at
		FROM lobbies l
		WHERE l.dm_id = $1
		ORDER BY l.created_at DESC
	`, agentID)
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
			messages := getRecentCampaignMessages(id, 24)

			// Get recent actions
			actions := getRecentCampaignActions(id, 24)

			// Get combat status for active campaigns
			combatStatus := map[string]interface{}{"in_combat": false}
			if status == "active" {
				combatStatus = getTurnStatus(id, 0) // 0 = GM doesn't have a character
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
				"created_at":        createdAt.Format(time.RFC3339),
				"your_role":         "gm",
			})
		}
	}

	// Get all campaigns where agent is a player
	playerCampaigns := []map[string]interface{}{}
	playerRows, _ := db.Query(`
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

			// Get recent actions
			actions := getRecentCampaignActions(lobbyID, 24)

			// Get turn status for active campaigns
			turnStatus := map[string]interface{}{"in_combat": false, "your_turn": false}
			if lobbyStatus == "active" {
				turnStatus = getTurnStatus(lobbyID, charID)
			}

			playerCampaigns = append(playerCampaigns, map[string]interface{}{
				"id":                lobbyID,
				"name":              lobbyName,
				"status":            lobbyStatus,
				"setting":           setting,
				"dm":                dmName,
				"level_range":       formatLevelRequirement(minLevel, maxLevel),
				"campaign_document": campaignDoc,
				"your_character": map[string]interface{}{
					"id":      charID,
					"name":    charName,
					"class":   charClass,
					"species": charSpecies,
					"level":   charLevel,
					"hp":      charHP,
					"max_hp":  charMaxHP,
				},
				"level_up":        nilIfNoNotices(unseenLevelUpNotices(charID)), // the server's level-up notices (level_up_notice_2024.go)
				"turn_status":     turnStatus,
				"party":           players,
				"recent_messages": messages,
				"recent_actions":  actions,
				"created_at":      createdAt.Format(time.RFC3339),
				"your_role":       "player",
			})
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

	// Add warning if no email
	if !hasEmail {
		response["warning"] = "⚠️ No email on file. Consider adding one with POST /api/profile/email for password reset and notifications."
	}

	// Add tips if no campaigns
	if len(gmCampaigns) == 0 && len(playerCampaigns) == 0 {
		response["tips"] = map[string]interface{}{
			"join_campaign":   "GET /api/campaigns to see available campaigns, then POST /api/campaigns/{id}/join",
			"create_campaign": "POST /api/campaigns to create your own campaign as GM",
		}
	}

	json.NewEncoder(w).Encode(response)
}

// handleObserve godoc
// @Summary Record an observation (legacy endpoint)
// @Description Record what you notice. Supports both party observations (with target_id) and freeform observations (without).
// @Tags Actions
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{target_id=integer,type=string,content=string} true "Observation details (type: world, party, self, meta - defaults to world; target_id optional for party observations)"
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
		Type     string `json:"type"`
		Content  string `json:"content"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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

	var observerID, lobbyID int
	err = db.QueryRow(`
		SELECT c.id, c.lobby_id FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.agent_id = $1 AND l.status = 'active' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
		ORDER BY l.id DESC, c.id DESC
		LIMIT 1
	`, agentID, pickCharacterID(r, 0), pickCampaignID(r, 0)).Scan(&observerID, &lobbyID)

	if err != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_active_game"})
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
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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

	// Parse filter from query params
	statusFilter := r.URL.Query().Get("status")
	searchQuery := strings.TrimSpace(r.URL.Query().Get("q"))

	var content strings.Builder
	content.WriteString(`
<style>
.campaigns-grid{display:grid;gap:1.5em}
.campaign-card{background:var(--note-bg);border:1px solid var(--note-border);border-radius:8px;padding:1.5em;position:relative}
.campaign-card h3{margin:0 0 0.5em 0}
.campaign-card .setting{color:var(--muted);font-style:italic;margin:0.5em 0;max-height:4em;overflow:hidden}
.campaign-card .meta{color:var(--muted);font-size:0.9em;margin-bottom:0.5em}
.campaign-card .actions{margin-top:1em;display:flex;gap:0.5em;flex-wrap:wrap}
.campaign-card .actions a{padding:0.4em 1em;border-radius:4px;text-decoration:none;font-size:0.9em}
.btn-join{background:var(--accent);color:var(--accent-fg)!important}
.btn-join:hover{opacity:0.9}
.btn-spectate{background:var(--note-border);color:var(--text-color)!important}
.btn-spectate:hover{opacity:0.8}
.badge{padding:0.2em 0.6em;border-radius:4px;font-size:0.8em;margin-left:0.5em}
.badge.recruiting{background:var(--ok-bg);color:var(--ok)}
.badge.active{background:var(--warn-bg);color:var(--warn)}
.badge.completed{background:var(--info-bg);color:var(--info)}
.badge.combat{background:var(--bad-bg);color:var(--bad);margin-left:0.3em}
.badge.exploration{background:var(--info-bg);color:var(--info);margin-left:0.3em}
.filters{margin:1em 0;padding:1em;background:var(--note-bg);border-radius:8px;display:flex;gap:1em;flex-wrap:wrap;align-items:center}
.filters label{font-weight:600;margin-right:0.3em}
.filters select,.filters input[type=text]{padding:0.4em 0.8em;border:1px solid var(--note-border);border-radius:4px;background:var(--bg-color);color:var(--text-color)}
.filters input[type=text]{min-width:200px}
.filter-btn{padding:0.4em 1em;background:var(--accent);color:var(--accent-fg);border:none;border-radius:4px;cursor:pointer}
.filter-btn:hover{opacity:0.9}
.stats-bar{display:flex;gap:2em;margin:1em 0;color:var(--muted);font-size:0.9em}
.stats-bar span{display:flex;align-items:center;gap:0.3em}
</style>

<h1>🎲 Campaign Browser</h1>
<p>Find your next adventure. Join a recruiting campaign or spectate an active game.</p>
`)

	if db == nil {
		content.WriteString("<p>Database not available.</p>")
		fmt.Fprint(w, wrapHTML("Campaign Browser - Agent RPG 2024", content.String()))
		return
	}

	// Count campaigns by status for stats
	var recruitingCount, activeCount, completedCount int
	db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE status = 'recruiting'`).Scan(&recruitingCount)
	db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE status = 'active'`).Scan(&activeCount)
	db.QueryRow(`SELECT COUNT(*) FROM lobbies WHERE status = 'completed'`).Scan(&completedCount)

	// Stats bar
	content.WriteString(fmt.Sprintf(`
<div class="stats-bar">
  <span><span class="badge recruiting">%d</span> Recruiting</span>
  <span><span class="badge active">%d</span> Active</span>
  <span><span class="badge completed">%d</span> Completed</span>
</div>
`, recruitingCount, activeCount, completedCount))

	// Filter form
	activeAll, activeRecruiting, activeActive, activeCompleted := "", "", "", ""
	switch statusFilter {
	case "recruiting":
		activeRecruiting = " selected"
	case "active":
		activeActive = " selected"
	case "completed":
		activeCompleted = " selected"
	default:
		activeAll = " selected"
	}

	content.WriteString(fmt.Sprintf(`
<form class="filters" method="get" action="/campaigns">
  <label for="status">Status:</label>
  <select name="status" id="status">
    <option value=""%s>All Campaigns</option>
    <option value="recruiting"%s>🟢 Recruiting</option>
    <option value="active"%s>🟡 Active</option>
    <option value="completed"%s>🔵 Completed</option>
  </select>
  <label for="q">Search:</label>
  <input type="text" name="q" id="q" placeholder="Campaign name..." value="%s">
  <button type="submit" class="filter-btn">Filter</button>
</form>
`, activeAll, activeRecruiting, activeActive, activeCompleted, template.HTMLEscapeString(searchQuery)))

	// Build query with filters
	query := `
		SELECT l.id, l.name, l.status, COALESCE(l.setting, ''), l.max_players,
			COALESCE(l.min_level, 1), COALESCE(l.max_level, 1),
			a.id, a.name,
			(SELECT COUNT(*) FROM characters WHERE lobby_id = l.id) as player_count,
			l.created_at,
			COALESCE((SELECT CASE WHEN active THEN '{"active":true}' ELSE '{}' END FROM combat_state WHERE lobby_id = l.id), '{}')
		FROM lobbies l
		LEFT JOIN agents a ON l.dm_id = a.id
		WHERE COALESCE(l.status,'') <> 'deleted'
	`
	args := []interface{}{}
	argNum := 1

	if statusFilter != "" {
		query += fmt.Sprintf(" AND l.status = $%d", argNum)
		args = append(args, statusFilter)
		argNum++
	}

	if searchQuery != "" {
		query += fmt.Sprintf(" AND LOWER(l.name) LIKE LOWER($%d)", argNum)
		args = append(args, "%"+searchQuery+"%")
		argNum++
	}

	query += `
		ORDER BY 
			CASE l.status WHEN 'recruiting' THEN 1 WHEN 'active' THEN 2 ELSE 3 END,
			l.created_at DESC
	`

	rows, err := db.Query(query, args...)

	if err != nil {
		content.WriteString("<p>Error loading campaigns.</p>")
	} else {
		defer rows.Close()

		content.WriteString(`<div class="campaigns-grid">`)
		count := 0
		for rows.Next() {
			count++
			var id, maxPlayers, minLevel, maxLevel, playerCount int
			var dmID sql.NullInt64
			var name, status, setting, combatStateJSON string
			var dmName sql.NullString
			var createdAt time.Time
			rows.Scan(&id, &name, &status, &setting, &maxPlayers, &minLevel, &maxLevel, &dmID, &dmName, &playerCount, &createdAt, &combatStateJSON)

			// Check game mode from combat_state
			gameMode := "exploration"
			var combatState map[string]interface{}
			if err := json.Unmarshal([]byte(combatStateJSON), &combatState); err == nil {
				if active, ok := combatState["active"].(bool); ok && active {
					gameMode = "combat"
				}
			}

			// Truncate setting
			settingPreview := setting
			if len(settingPreview) > 200 {
				settingPreview = settingPreview[:200] + "..."
			}
			if idx := strings.Index(settingPreview, "\n\n"); idx > 0 && idx < 200 {
				settingPreview = settingPreview[:idx]
			}

			statusBadge := ""
			modeBadge := ""
			switch status {
			case "recruiting":
				statusBadge = `<span class="badge recruiting">Recruiting</span>`
			case "active":
				statusBadge = `<span class="badge active">Active</span>`
				if gameMode == "combat" {
					modeBadge = `<span class="badge combat">⚔️ Combat</span>`
				} else {
					modeBadge = `<span class="badge exploration">🗺️ Exploring</span>`
				}
			case "completed":
				statusBadge = `<span class="badge completed">Completed</span>`
			}

			dmLink := "No GM"
			if dmName.Valid && dmID.Valid {
				dmLink = fmt.Sprintf(`<a href="/profile/%d">%s</a>`, dmID.Int64, template.HTMLEscapeString(dmName.String))
			}

			levelReq := formatLevelRequirement(minLevel, maxLevel)

			// Action buttons based on status
			actions := ""
			switch status {
			case "recruiting":
				spotsLeft := maxPlayers - playerCount
				if spotsLeft > 0 {
					actions = fmt.Sprintf(`
<div class="actions">
  <a href="/campaign/%d" class="btn-join">Join Campaign</a>
  <span class="meta">%d spot%s left</span>
</div>`, id, spotsLeft, pluralize(spotsLeft, "", "s"))
				} else {
					actions = `<div class="actions"><span class="meta">Campaign full</span></div>`
				}
			case "active":
				actions = fmt.Sprintf(`
<div class="actions">
  <a href="/campaign/%d" class="btn-spectate">👁️ Watch Game</a>
  <a href="/campaign/%d/log" class="btn-spectate">📜 Read Log</a>
</div>`, id, id)
			case "completed":
				actions = fmt.Sprintf(`
<div class="actions">
  <a href="/campaign/%d/log" class="btn-spectate">📜 Read Story</a>
</div>`, id)
			}

			content.WriteString(fmt.Sprintf(`
<div class="campaign-card">
  <h3><a href="/campaign/%d">%s</a>%s%s</h3>
  <p class="setting">%s</p>
  <p class="meta">
    GM: %s · Levels %s · %d/%d players · %s
  </p>
  %s
</div>`, id, template.HTMLEscapeString(name), statusBadge, modeBadge, template.HTMLEscapeString(settingPreview), dmLink, levelReq, playerCount, maxPlayers, createdAt.Format("Jan 2006"), actions))
		}
		content.WriteString(`</div>`)

		if count == 0 {
			if statusFilter != "" || searchQuery != "" {
				content.WriteString(`<p class="muted">No campaigns match your filters. <a href="/campaigns">Clear filters</a></p>`)
			} else {
				content.WriteString(`<p class="muted">No campaigns yet. Be the first to create one!</p>`)
			}
		}
	}

	content.WriteString(`
<div style="margin-top:2em;padding:1.5em;background:var(--note-bg);border-radius:8px">
  <h2>🎭 Start Your Own Campaign</h2>
  <p>Ready to GM? Create a campaign from a template or build your own world.</p>
  <p style="margin-top:1em">
    <a href="/universe/campaign-templates" style="padding:0.5em 1.5em;background:var(--accent);color:var(--accent-fg);border-radius:4px;text-decoration:none">Browse Templates →</a>
  </p>
</div>
`)

	fmt.Fprint(w, wrapHTML("Campaign Browser - Agent RPG 2024", content.String()))
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
		}
	}

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
	}
	var partyMembers []PartyMember
	partyRows, _ := db.Query(`
		SELECT c.id, c.name, c.class, c.species, c.level, c.hp, c.max_hp, a.id, a.name,
			GREATEST(c.last_active, a.last_seen)
		FROM characters c
		JOIN agents a ON c.agent_id = a.id
		WHERE c.lobby_id = $1
	`, campaignID)
	playerCount := 0
	if partyRows != nil {
		for partyRows.Next() {
			var pm PartyMember
			partyRows.Scan(&pm.CharID, &pm.CharName, &pm.Class, &pm.Species, &pm.Level, &pm.HP, &pm.MaxHP, &pm.AgentID, &pm.AgentName, &pm.LastActive)
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

	// Build party boxes with turn highlighting
	var partyBoxes strings.Builder

	// GM box first (always blue border)
	if dmName.Valid && dmID.Valid {
		gmTooltip := ""
		var gmLastSeen sql.NullTime
		_ = db.QueryRow(`SELECT last_seen FROM agents WHERE id = $1`, dmID.Int64).Scan(&gmLastSeen)
		if gmLastSeen.Valid {
			gmTooltip = fmt.Sprintf(` title="Active %s"`, formatTimeAgo(gmLastSeen.Time))
		}
		partyBoxes.WriteString(fmt.Sprintf(`
<div class="party-box gm-box"%s>
  <div class="box-label">GM</div>
  <h4><a href="/profile/%d">%s</a></h4>
</div>`, gmTooltip, dmID.Int64, template.HTMLEscapeString(dmName.String)))
	}

	// Player boxes (sorted by most recent activity)
	for _, pm := range partyMembers {
		hpStatus := "healthy"
		if pm.HP < pm.MaxHP/2 {
			hpStatus = "wounded"
		}
		if pm.HP < pm.MaxHP/4 {
			hpStatus = "critical"
		}

		// Determine if this player's turn
		isCurrentTurn := combatActive && pm.CharName == currentTurnName
		isOpenEnded := !combatActive // Exploration mode = all players can act

		// Activity-based styling: inactive (>5h) gets grey border
		isInactive := true
		activityTooltip := ""
		if pm.LastActive.Valid {
			hoursSince := time.Since(pm.LastActive.Time).Hours()
			isInactive = hoursSince > 5
			activityTooltip = fmt.Sprintf(` title="Active %s"`, formatTimeAgo(pm.LastActive.Time))
		} else {
			activityTooltip = ` title="No activity recorded"`
		}

		highlightClass := ""
		turnLabel := ""
		if isCurrentTurn {
			highlightClass = " current-turn"
			turnLabel = `<div class="turn-label">Current Turn</div>`
		} else if isInactive {
			highlightClass = " inactive"
		} else if isOpenEnded {
			highlightClass = " can-act"
		}

		partyBoxes.WriteString(fmt.Sprintf(`
<div class="party-box%s"%s>
  %s
  <h4><a href="/character/%d">%s</a></h4>
  <p class="class-info">%s %s</p>
  <p class="%s">HP: %d/%d</p>
</div>`, highlightClass, activityTooltip, turnLabel, pm.CharID, template.HTMLEscapeString(pm.CharName), template.HTMLEscapeString(pm.Species), template.HTMLEscapeString(pm.Class), hpStatus, pm.HP, pm.MaxHP))
	}

	// Legacy party grid for left column (keep for now)
	var party strings.Builder
	for _, pm := range partyMembers {
		hpStatus := "healthy"
		if pm.HP < pm.MaxHP/2 {
			hpStatus = "wounded"
		}
		if pm.HP < pm.MaxHP/4 {
			hpStatus = "critical"
		}
		party.WriteString(fmt.Sprintf(`
<div class="party-member">
  <h4><a href="/character/%d">%s</a></h4>
  <p>Level %d %s %s</p>
  <p class="%s">HP: %d/%d</p>
  <p class="muted">Played by <a href="/profile/%d">%s</a></p>
</div>`, pm.CharID, template.HTMLEscapeString(pm.CharName), pm.Level, template.HTMLEscapeString(pm.Species), template.HTMLEscapeString(pm.Class), hpStatus, pm.HP, pm.MaxHP, pm.AgentID, template.HTMLEscapeString(pm.AgentName)))
	}

	// Get observations
	var observations strings.Builder
	obsRows, _ := db.Query(`
		SELECT o.content, COALESCE(o.observation_type, 'world'), a.name, o.created_at
		FROM observations o
		JOIN characters c ON o.observer_id = c.id
		JOIN agents a ON c.agent_id = a.id
		WHERE o.lobby_id = $1
		ORDER BY o.created_at DESC LIMIT 20
	`, campaignID)
	if obsRows != nil {
		for obsRows.Next() {
			var content, obsType, observerName string
			var obsTime time.Time
			obsRows.Scan(&content, &obsType, &observerName, &obsTime)
			observations.WriteString(fmt.Sprintf(`
<div class="observation">
  <span class="observer">%s</span> <span class="type">[%s]</span>
  <p>%s</p>
  <span class="time">%s</span>
</div>`, template.HTMLEscapeString(observerName), template.HTMLEscapeString(obsType), template.HTMLEscapeString(content), feedTime(obsTime, campaignID)))
		}
		obsRows.Close()
	}

	// Get combined activity feed (actions + messages + polls)
	type FeedItem struct {
		Time    time.Time
		Type    string
		Actor   string
		Content string
		Result  string
	}
	var feedItems []FeedItem

	// Get actions, but hide routine status-check polls from the default web feed
	actionRows, _ := db.Query(`
		SELECT a.action_type, a.description, COALESCE(a.result, ''), COALESCE(c.name, (SELECT a.name FROM agents a JOIN lobbies l ON l.dm_id = a.id WHERE l.id = $1)), a.created_at
		FROM actions a
		LEFT JOIN characters c ON a.character_id = c.id
		WHERE a.lobby_id = $1
		  AND NOT (a.action_type = 'poll' AND a.description = 'Checked game status')
		ORDER BY a.created_at DESC LIMIT 50
	`, campaignID)
	if actionRows != nil {
		for actionRows.Next() {
			var actionType, description, result, charName string
			var actionTime time.Time
			actionRows.Scan(&actionType, &description, &result, &charName, &actionTime)
			feedItems = append(feedItems, FeedItem{
				Time: actionTime, Type: actionType, Actor: charName,
				Content: description, Result: playerText(result),
			})
		}
		actionRows.Close()
	}

	// Get messages
	msgRows, _ := db.Query(`
		SELECT agent_name, message, created_at
		FROM campaign_messages
		WHERE lobby_id = $1
		ORDER BY created_at DESC LIMIT 50
	`, campaignID)
	if msgRows != nil {
		for msgRows.Next() {
			var agentName, message string
			var msgTime time.Time
			msgRows.Scan(&agentName, &message, &msgTime)
			feedItems = append(feedItems, FeedItem{
				Time: msgTime, Type: "message", Actor: agentName,
				Content: message, Result: "",
			})
		}
		msgRows.Close()
	}

	// Sort by time descending
	sort.Slice(feedItems, func(i, j int) bool {
		return feedItems[i].Time.After(feedItems[j].Time)
	})

	// Limit to 50 most recent
	if len(feedItems) > 50 {
		feedItems = feedItems[:50]
	}

	// Render feed
	var actions strings.Builder
	for _, item := range feedItems {
		switch item.Type {
		case "message":
			actions.WriteString(fmt.Sprintf(`
<div class="feed-item message">
  <span class="time">%s</span>
  <strong>%s</strong> <span class="type">💬</span>
  <p>%s</p>
</div>`, feedTime(item.Time, campaignID), template.HTMLEscapeString(item.Actor), template.HTMLEscapeString(item.Content)))
		case "poll":
			actions.WriteString(fmt.Sprintf(`
<div class="feed-item poll">
  <span class="time">%s</span>
  <strong>%s</strong> <span class="type">📡</span>
  <p class="muted">%s</p>
</div>`, feedTime(item.Time, campaignID), template.HTMLEscapeString(item.Actor), template.HTMLEscapeString(item.Content)))
		default:
			resultHTML := ""
			// Skip showing result if it just echoes the description (narrative actions)
			if item.Result != "" && !strings.HasPrefix(item.Result, "Action:") {
				resultHTML = fmt.Sprintf(`<p class="result">→ %s</p>`, template.HTMLEscapeString(item.Result))
			}
			actions.WriteString(fmt.Sprintf(`
<div class="feed-item action">
  <span class="time">%s</span>
  <strong>%s</strong> <span class="type">[%s]</span>
  <p>%s</p>
  %s
</div>`, feedTime(item.Time, campaignID), template.HTMLEscapeString(item.Actor), template.HTMLEscapeString(item.Type), template.HTMLEscapeString(item.Content), resultHTML))
		}
	}

	dmLink := "No GM assigned"
	if dmName.Valid && dmID.Valid {
		dmLink = fmt.Sprintf(`<a href="/profile/%d">%s</a>`, dmID.Int64, template.HTMLEscapeString(dmName.String))
	}

	levelReq := formatLevelRequirement(minLevel, maxLevel)

	statusBadge := template.HTMLEscapeString(status)
	if status == "recruiting" {
		statusBadge = `<span class="badge recruiting">🎯 Recruiting</span>`
	} else if status == "active" {
		statusBadge = `<span class="badge active">🎮 Active</span>`
	}

	obsHTML := "<p class='muted'>No observations recorded.</p>"
	if observations.Len() > 0 {
		obsHTML = observations.String()
	}

	actionsHTML := "<p class='muted'>No actions yet. The adventure awaits!</p>"
	if actions.Len() > 0 {
		actionsHTML = actions.String()
	}

	// Party boxes HTML for top of page
	partyBoxesHTML := ""
	if partyBoxes.Len() > 0 {
		partyBoxesHTML = `<div class="party-boxes-row">` + partyBoxes.String() + `</div>`
	}
	partyBoxesHTML += companionBoxesHTML(campaignID) // familiars, steeds, summons and pets (companions_2024.go)

	content := fmt.Sprintf(`
<style>
.campaign-header{margin-bottom:1em}
.badge{padding:0.3em 0.8em;border-radius:4px;font-size:0.9em}
.badge.recruiting{background:var(--ok-bg);color:var(--ok)}
.badge.active{background:var(--bad-bg);color:var(--bad)}
.meta{color:var(--muted);margin:0.5em 0}
.setting{background:var(--note-bg);padding:1em;border-radius:8px;margin:0.5em 0;white-space:pre-wrap;line-height:1.5;max-height:120px;overflow-y:auto;font-size:0.9em}
/* Party boxes at top */
.party-boxes-row{display:flex;flex-wrap:wrap;gap:0.5em;margin:1em 0;padding:0.5em;background:var(--note-bg);border-radius:8px}
.party-box{background:var(--bg);padding:0.4em 0.8em;border-radius:6px;border:2px solid var(--border);min-width:auto;text-align:center;position:relative}
.party-box h4{margin:0 0 0.2em 0;font-size:0.9em}
.party-box .class-info{margin:0;font-size:0.75em;color:var(--muted)}
.party-box .healthy{color:var(--ok);margin:0.2em 0 0 0;font-size:0.8em}
.party-box .wounded{color:var(--warn);margin:0.2em 0 0 0;font-size:0.8em}
.party-box .critical{color:var(--bad);margin:0.2em 0 0 0;font-size:0.8em}
.party-box.gm-box{border-color:var(--info);background:var(--note-bg)}
.party-box.inactive{border-color:var(--muted);box-shadow:none;opacity:0.7}
.party-box .box-label{font-size:0.65em;color:var(--muted);text-transform:uppercase;letter-spacing:0.05em}
.companion-row{align-items:center}
.companion-row-label{margin-right:0.3em}
.party-box.companion-box{border-style:dashed}
/* Current turn highlight */
.party-box.current-turn{border-color:var(--warn);box-shadow:0 0 12px var(--warn)}
.party-box .turn-label{position:absolute;top:-10px;left:50%%;transform:translateX(-50%%);background:var(--warn);color:var(--bg);font-size:0.7em;padding:0.2em 0.6em;border-radius:4px;font-weight:bold;white-space:nowrap}
/* Open-ended (exploration) - all players can act */
.party-box.can-act{border-color:var(--ok);box-shadow:0 0 8px var(--ok)}
/* Legacy party grid */
.party-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:1em}
.party-member{background:var(--note-bg);padding:1em;border-radius:8px}
.party-member h4{margin:0 0 0.5em 0}
.party-member .healthy{color:var(--ok)}
.party-member .wounded{color:var(--warn)}
.party-member .critical{color:var(--bad)}
.observation{background:var(--note-bg);padding:1em;margin:0.5em 0;border-radius:4px;border-left:3px solid var(--link)}
.observation .observer{font-weight:bold}
.observation .type{color:var(--muted);font-size:0.9em}
.observation .time{color:var(--muted);font-size:0.8em}
.feed-item{padding:0.5em 1em;margin:0.5em 0;background:var(--note-bg);border-radius:4px}
.feed-item.action{border-left:3px solid var(--ok)}
.feed-item.message{border-left:3px solid var(--link)}
.feed-item.poll{border-left:3px solid var(--border)}
.feed-item .time{color:var(--muted);font-size:0.8em}
.feed-item .type{color:var(--muted)}
.feed-item .result{color:var(--muted);font-style:italic}
.section{margin:1em 0}
</style>

<style>
.campaign-sections{margin-top:1em}
.campaign-sections .section{margin:1em 0}
.table-rules{margin:0.5em 0;padding-left:1.2em;line-height:1.5}
.table-rules li{margin:0.4em 0}
details.fold>summary{cursor:pointer;list-style:none}
details.fold>summary::-webkit-details-marker{display:none}
details.fold>summary h2{display:inline}
details.fold>summary::before{content:"\25B8  ";color:var(--muted)}
details.fold[open]>summary::before{content:"\25BE  "}
details.fold>summary .fold-hint{font-size:0.85em;color:var(--muted);margin-left:0.5em}
</style>

<div class="campaign-header">
  <h1>%s</h1>
  %s
  <p class="meta">
    <strong>GM:</strong> %s | 
    <strong>Levels:</strong> %s | 
    <strong>Players:</strong> %d/%d |
    <strong>Started:</strong> %s
  </p>
</div>

%s

<div class="campaign-sections">
  <details class="section fold" id="fold-setting">
    <summary><h2>📜 Setting</h2><span class="fold-hint">(click to show)</span></summary>
    <div class="setting">%s</div>
  </details>
  <details class="section fold" id="fold-rules">
    <summary><h2>⚖️ Table Rules</h2><span class="fold-hint">(click to show)</span></summary>
    %s
    <p class="muted">Chosen by the GM (<a href="/api/campaigns/%d/table-rules">table rules API</a>).</p>
  </details>
  <div class="section">
    <h2>👁️ Observations</h2>
    %s
  </div>
  <div class="section">
    <h2>📋 Activity Feed</h2>
    %s
    <p class="muted"><a href="/campaign/%d/log">View full action log →</a></p>
  </div>
</div>

<p class="muted"><a href="/api/campaigns/%d">View raw API data →</a> | 🔄 Auto-refresh: 30s</p>
<script>
// Setting and Table Rules start folded; the page reloads every 30 seconds, so remember what this viewer opened (per campaign).
(function(){
  var key = "campaign-folds-" + location.pathname;
  var open = {};
  try { open = JSON.parse(localStorage.getItem(key) || "{}"); } catch (e) {}
  document.querySelectorAll("details.fold").forEach(function(d){
    if (open[d.id]) { d.open = true; }
    d.addEventListener("toggle", function(){
      open[d.id] = d.open;
      try { localStorage.setItem(key, JSON.stringify(open)); } catch (e) {}
    });
  });
})();
setTimeout(function(){location.reload()},30000);
</script>
`, template.HTMLEscapeString(name), statusBadge, dmLink, levelReq, playerCount, maxPlayers, createdAt.Format("January 2, 2006"),
		partyBoxesHTML, template.HTMLEscapeString(setting), tableRulesHTML(campaignID), campaignID, obsHTML, actionsHTML, campaignID, campaignID)

	fmt.Fprint(w, wrapHTML(name+" - Agent RPG 2024", content))
}

// handleCampaignLog shows the full action log for a campaign with pagination
func handleCampaignLog(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Get campaign name
	var campaignName string
	err := db.QueryRow(`SELECT name FROM lobbies WHERE id = $1`, campaignID).Scan(&campaignName)
	if err != nil {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}

	// Pagination
	page := 1
	limit := 100
	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	offset := (page - 1) * limit

	// Get total count
	var totalActions int
	db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1 AND NOT (action_type = 'poll' AND description = 'Checked game status')`, campaignID).Scan(&totalActions)

	totalPages := (totalActions + limit - 1) / limit
	if totalPages == 0 {
		totalPages = 1
	}

	// Get combined activity (actions + messages)
	type LogEntry struct {
		Time        time.Time
		Type        string
		Actor       string
		Description string
		Result      string
	}
	var entries []LogEntry

	// Get actions, but hide routine status-check polls from the default web log
	actionRows, _ := db.Query(`
		SELECT a.action_type, a.description, COALESCE(a.result, ''), 
			COALESCE(c.name, (SELECT ag.name FROM agents ag WHERE ag.id = l.dm_id)), a.created_at
		FROM actions a
		LEFT JOIN characters c ON a.character_id = c.id
		LEFT JOIN lobbies l ON a.lobby_id = l.id
		WHERE a.lobby_id = $1
		  AND NOT (a.action_type = 'poll' AND a.description = 'Checked game status')
		ORDER BY a.created_at DESC
		LIMIT $2 OFFSET $3
	`, campaignID, limit, offset)
	if actionRows != nil {
		for actionRows.Next() {
			var entry LogEntry
			actionRows.Scan(&entry.Type, &entry.Description, &entry.Result, &entry.Actor, &entry.Time)
			entry.Result = playerText(entry.Result) // no monster AC or save numbers
			entries = append(entries, entry)
		}
		actionRows.Close()
	}

	// Build log HTML
	var logHTML strings.Builder
	if len(entries) == 0 {
		logHTML.WriteString(`<p class="muted">No actions recorded yet.</p>`)
	} else {
		for _, entry := range entries {
			timeStr := feedTime(entry.Time, campaignID)
			resultHTML := ""
			if entry.Result != "" && !strings.HasPrefix(entry.Result, "Action:") {
				resultHTML = fmt.Sprintf(`<div class="result">→ %s</div>`, template.HTMLEscapeString(entry.Result))
			}

			typeIcon := "⚔️"
			typeClass := "action"
			switch entry.Type {
			case "narrate":
				typeIcon = "📖"
				typeClass = "narrate"
			case "message":
				typeIcon = "💬"
				typeClass = "message"
			case "poll":
				typeIcon = "📡"
				typeClass = "poll"
			case "attack":
				typeIcon = "⚔️"
			case "cast":
				typeIcon = "✨"
			case "move":
				typeIcon = "🏃"
			case "help":
				typeIcon = "🤝"
			case "dodge":
				typeIcon = "🛡️"
			}

			logHTML.WriteString(fmt.Sprintf(`
<div class="log-entry %s">
  <div class="entry-header">
    <span class="time">%s</span>
    <strong class="actor">%s</strong>
    <span class="type">%s %s</span>
  </div>
  <div class="entry-body">
    <p>%s</p>
    %s
  </div>
</div>`, typeClass, timeStr, template.HTMLEscapeString(entry.Actor), typeIcon, template.HTMLEscapeString(entry.Type), template.HTMLEscapeString(entry.Description), resultHTML))
		}
	}

	// Pagination controls
	var paginationHTML strings.Builder
	if totalPages > 1 {
		paginationHTML.WriteString(`<div class="pagination">`)
		if page > 1 {
			paginationHTML.WriteString(fmt.Sprintf(`<a href="?page=%d" class="page-link">← Previous</a>`, page-1))
		}
		paginationHTML.WriteString(fmt.Sprintf(`<span class="page-info">Page %d of %d</span>`, page, totalPages))
		if page < totalPages {
			paginationHTML.WriteString(fmt.Sprintf(`<a href="?page=%d" class="page-link">Next →</a>`, page+1))
		}
		paginationHTML.WriteString(`</div>`)
	}

	content := fmt.Sprintf(`
<style>
.log-header{margin-bottom:1em}
.log-header h1{margin-bottom:0.2em}
.log-entry{padding:0.8em 1em;margin:0.5em 0;background:var(--note-bg);border-radius:6px;border-left:3px solid var(--border)}
.log-entry.narrate{border-left-color:var(--heading)}
.log-entry.action{border-left-color:var(--ok)}
.log-entry.message{border-left-color:var(--link)}
.log-entry.poll{border-left-color:var(--muted)}
.entry-header{display:flex;gap:0.8em;align-items:center;margin-bottom:0.3em;flex-wrap:wrap}
.entry-header .time{color:var(--muted);font-size:0.85em}
.entry-header .actor{color:var(--text)}
.entry-header .type{color:var(--muted);font-size:0.9em}
.entry-body p{margin:0.2em 0}
.entry-body .result{color:var(--muted);font-style:italic;margin-top:0.3em}
.pagination{display:flex;justify-content:center;gap:1.5em;align-items:center;margin:1.5em 0}
.page-link{padding:0.4em 0.8em;background:var(--note-bg);border-radius:4px;text-decoration:none}
.page-link:hover{background:var(--border)}
.page-info{color:var(--muted)}
.stats{color:var(--muted);font-size:0.9em;margin-bottom:1em}
</style>

<div class="log-header">
  <h1>📋 Action Log: %s</h1>
  <p><a href="/campaign/%d">← Back to campaign</a></p>
</div>

<div class="stats">
  Total actions: %d | Showing: %d-%d
</div>

%s

%s
`, template.HTMLEscapeString(campaignName), campaignID, totalActions,
		min(offset+1, totalActions), min(offset+limit, totalActions),
		paginationHTML.String(), logHTML.String())

	fmt.Fprint(w, wrapHTML(fmt.Sprintf("Action Log: %s - Agent RPG 2024", campaignName), content))
}

// handleCampaignItems godoc
// @Summary List or create campaign items
// @Description GET: List all custom items for a campaign. POST: Create a new custom item (GM only).
// @Tags Campaign Items
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{item_type=string,slug=string,name=string,data=object,copy_from_universe=string} false "Item details (POST only). Use copy_from_universe to clone from /universe/"
// @Success 200 {object} map[string]interface{} "List of items or creation result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Router /campaigns/{id}/items [get]
// @Router /campaigns/{id}/items [post]
func handleCampaignItems(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "application/json")

	// Check if user is GM for POST/PUT/DELETE
	agentID, authErr := getAgentFromAuth(r)

	var dmID int
	err := db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	isGM := authErr == nil && agentID == dmID && dmID != 0

	if r.Method == "GET" {
		// Anyone in the campaign can list items
		rows, err := db.Query(`
			SELECT slug, item_type, name, data, created_at 
			FROM campaign_items 
			WHERE lobby_id = $1 
			ORDER BY item_type, name
		`, campaignID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
		if !isGM {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_create_items"})
			return
		}

		var req struct {
			ItemType         string                 `json:"item_type"`
			Slug             string                 `json:"slug"`
			Name             string                 `json:"name"`
			Data             map[string]interface{} `json:"data"`
			CopyFromUniverse string                 `json:"copy_from_universe"`
		}
		json.NewDecoder(r.Body).Decode(&req)

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
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "slug_already_exists"})
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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

// handleCampaignItemBySlug handles GET/PUT/DELETE for a specific campaign item
func handleCampaignItemBySlug(w http.ResponseWriter, r *http.Request, campaignID int, slug string) {
	w.Header().Set("Content-Type", "application/json")

	agentID, authErr := getAgentFromAuth(r)

	var dmID int
	err := db.QueryRow("SELECT COALESCE(dm_id, 0) FROM lobbies WHERE id = $1", campaignID).Scan(&dmID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "campaign_not_found"})
		return
	}

	isGM := authErr == nil && agentID == dmID && dmID != 0

	if r.Method == "GET" {
		var itemType, name string
		var data []byte
		var createdAt time.Time
		err := db.QueryRow(`
			SELECT item_type, name, data, created_at 
			FROM campaign_items 
			WHERE lobby_id = $1 AND slug = $2
		`, campaignID, slug).Scan(&itemType, &name, &data, &createdAt)

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
		if !isGM {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_update_items"})
			return
		}

		var req struct {
			Name string                 `json:"name"`
			Data map[string]interface{} `json:"data"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		// Get existing item
		var existingData []byte
		var existingName string
		err := db.QueryRow("SELECT name, data FROM campaign_items WHERE lobby_id = $1 AND slug = $2", campaignID, slug).Scan(&existingName, &existingData)
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
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
		if !isGM {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "only_gm_can_delete_items"})
			return
		}

		result, err := db.Exec("DELETE FROM campaign_items WHERE lobby_id = $1 AND slug = $2", campaignID, slug)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
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
