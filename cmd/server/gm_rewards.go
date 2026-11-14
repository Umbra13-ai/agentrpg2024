package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// handleGMUpdateCharacter godoc
// @Summary Change any of a character's data (GM)
// @Description The GM of the character's campaign can change any of its data. GET ?character_id=N returns every field and its value. POST {"character_id": N, "set": {"field": value, ...}} writes any field of the character (hp, temp_hp, gold, conditions, inventory, spell_slots_used, known_spells, feats, exhaustion_level, subclass, ...; JSON fields take arrays or objects; null clears a field); only id, agent_id, lobby_id and created_at are fixed. The older shortcuts still work: class, species, background, name, str, dex, con, intl, wis, cha, hp, max_hp, level, and items (names added to the inventory).
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,set=object,class=string,species=string,background=string,items=[]string,str=int,dex=int,con=int,intl=int,wis=int,cha=int,hp=int,max_hp=int,level=int,name=string} true "Character updates"
// @Success 200 {object} map[string]interface{} "Updated character"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /gm/update-character [post]
func handleGMUpdateCharacter(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "GET" {
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if r.Method == "GET" {
		id, _ := strconv.Atoi(r.URL.Query().Get("character_id"))
		var lobby, dm int
		if id == 0 || db.QueryRow(`SELECT COALESCE(c.lobby_id,0), COALESCE(l.dm_id,0) FROM characters c LEFT JOIN lobbies l ON l.id = c.lobby_id WHERE c.id = $1`, id).Scan(&lobby, &dm) != nil || dm != agentID {
			writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_gm", "message": "Send character_id of a character in a campaign you run."})
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"character_id": id, "fields": gmCharacterRecord(id), "fixed_fields": []string{"id", "agent_id", "lobby_id", "created_at"}})
		return
	}

	var req struct {
		CharacterID int                        `json:"character_id"`
		Class       *string                    `json:"class"`
		Species     *string                    `json:"species"`
		Background  *string                    `json:"background"`
		Items       []string                   `json:"items"`
		STR         *int                       `json:"str"`
		DEX         *int                       `json:"dex"`
		CON         *int                       `json:"con"`
		INT         *int                       `json:"intl"`
		WIS         *int                       `json:"wis"`
		CHA         *int                       `json:"cha"`
		HP          *int                       `json:"hp"`
		MaxHP       *int                       `json:"max_hp"`
		Level       *int                       `json:"level"`
		Name        *string                    `json:"name"`
		Set         map[string]json.RawMessage `json:"set"` // any field: {"gold": 500, "conditions": ["prone"]}
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

	// Get character and verify GM owns the campaign
	var charLobbyID int
	err = db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, req.CharacterID).Scan(&charLobbyID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify this agent is the DM
	var dmID int
	err = db.QueryRow(`SELECT dm_id FROM lobbies WHERE id = $1`, charLobbyID).Scan(&dmID)
	if err != nil || dmID != agentID {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_gm", "message": "You are not the GM of this campaign"})
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argNum := 1

	if req.Class != nil {
		updates = append(updates, fmt.Sprintf("class = $%d", argNum))
		args = append(args, *req.Class)
		argNum++
	}
	if req.Species != nil {
		updates = append(updates, fmt.Sprintf("species = $%d", argNum))
		args = append(args, *req.Species)
		argNum++
	}
	if req.Background != nil {
		updates = append(updates, fmt.Sprintf("background = $%d", argNum))
		args = append(args, *req.Background)
		argNum++
	}
	if req.Name != nil {
		updates = append(updates, fmt.Sprintf("name = $%d", argNum))
		args = append(args, *req.Name)
		argNum++
	}
	if req.STR != nil {
		updates = append(updates, fmt.Sprintf("str = $%d", argNum))
		args = append(args, *req.STR)
		argNum++
	}
	if req.DEX != nil {
		updates = append(updates, fmt.Sprintf("dex = $%d", argNum))
		args = append(args, *req.DEX)
		argNum++
	}
	if req.CON != nil {
		updates = append(updates, fmt.Sprintf("con = $%d", argNum))
		args = append(args, *req.CON)
		argNum++
	}
	if req.INT != nil {
		updates = append(updates, fmt.Sprintf("intl = $%d", argNum))
		args = append(args, *req.INT)
		argNum++
	}
	if req.WIS != nil {
		updates = append(updates, fmt.Sprintf("wis = $%d", argNum))
		args = append(args, *req.WIS)
		argNum++
	}
	if req.CHA != nil {
		updates = append(updates, fmt.Sprintf("cha = $%d", argNum))
		args = append(args, *req.CHA)
		argNum++
	}
	if req.HP != nil {
		updates = append(updates, fmt.Sprintf("hp = $%d", argNum))
		args = append(args, *req.HP)
		argNum++
	}
	if req.MaxHP != nil {
		updates = append(updates, fmt.Sprintf("max_hp = $%d", argNum))
		args = append(args, *req.MaxHP)
		argNum++
	}
	// A new level goes through the level-up path (Hit Points, ASI points, the public note), after the other updates.
	var oldLevel int
	db.QueryRow(`SELECT level FROM characters WHERE id = $1`, req.CharacterID).Scan(&oldLevel)
	targetLevel := 0
	if req.Level != nil {
		targetLevel = *req.Level
	}
	if raw, ok := req.Set["level"]; ok {
		json.Unmarshal(raw, &targetLevel)
		delete(req.Set, "level")
	}
	if targetLevel != 0 && (targetLevel < 1 || targetLevel > 20) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_level", "message": "level must be 1 to 20"})
		return
	}

	changed, setErr := gmSetCharacterFields(req.CharacterID, req.Set)
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_field", "message": setErr.Error()})
		return
	}

	levelNote := ""
	if targetLevel > oldLevel {
		_, _, levelNote = applyLevelGain(req.CharacterID, oldLevel, targetLevel, "set by the GM")
		changed = append(changed, "level")
	} else if targetLevel != 0 && targetLevel < oldLevel {
		db.Exec(`UPDATE characters SET level = $1 WHERE id = $2`, targetLevel, req.CharacterID)
		var cls string
		db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, req.CharacterID).Scan(&cls)
		syncClassLevels(req.CharacterID, cls, targetLevel)
		levelNote = fmt.Sprintf("Level lowered from %d to %d. Hit Points, spells and features are not taken away automatically: adjust them with set (max_hp, hp, known_spells ...).", oldLevel, targetLevel)
		changed = append(changed, "level")
	}

	if len(updates) == 0 && len(req.Items) == 0 && len(changed) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_updates", "message": "No fields to update"})
		return
	}

	// Execute character updates
	if len(updates) > 0 {
		query := fmt.Sprintf("UPDATE characters SET %s WHERE id = $%d", strings.Join(updates, ", "), argNum)
		args = append(args, req.CharacterID)
		_, err = db.Exec(query, args...)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "update_failed", "details": publicError(err)})
			return
		}
	}

	// Items go into the inventory (the old character_items table no longer exists)
	addInventoryItems(req.CharacterID, req.Items)

	// Fetch updated character
	var char struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		Class      string `json:"class"`
		Species    string `json:"species"`
		Background string `json:"background"`
		Level      int    `json:"level"`
		HP         int    `json:"hp"`
		MaxHP      int    `json:"max_hp"`
	}
	db.QueryRow(`SELECT id, name, class, species, COALESCE(background, ''), level, hp, max_hp 
		FROM characters WHERE id = $1`, req.CharacterID).Scan(
		&char.ID, &char.Name, &char.Class, &char.Species, &char.Background, &char.Level, &char.HP, &char.MaxHP)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"message":   "Character updated",
		"character": char,
		"changed":   changed,
		"level_up":  levelNote,
	})
}

// handleGMAwardXP godoc
// @Summary Award XP to characters
// @Description GM awards experience points to one or more characters. Automatically handles level-ups.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_ids=[]integer,xp=integer,reason=string,auto_level=bool} true "XP award details (auto_level false: the XP is banked and the player takes the level with POST /api/characters/{id}/level-up)"
// @Success 200 {object} map[string]interface{} "XP awarded with level-up notifications"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/award-xp [post]
func handleGMAwardXP(w http.ResponseWriter, r *http.Request) {
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
		CharacterIDs []int  `json:"character_ids"`
		XP           int    `json:"xp"`
		Reason       string `json:"reason"`
		AutoLevel    *bool  `json:"auto_level"` // default true; false banks the XP and the player takes the level (POST /api/characters/{id}/level-up)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if len(req.CharacterIDs) == 0 || req.XP <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_ids and positive xp required",
		})
		return
	}

	// Verify this agent is the GM of all these characters' campaigns
	for _, charID := range req.CharacterIDs {
		var dmID int
		err = db.QueryRow(`
			SELECT l.dm_id FROM characters c 
			JOIN lobbies l ON c.lobby_id = l.id 
			WHERE c.id = $1
		`, charID).Scan(&dmID)

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		if dmID != agentID {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_gm",
				"message": fmt.Sprintf("You are not the GM for character %d's campaign", charID),
			})
			return
		}
	}

	// Award XP and check for level-ups
	results := []map[string]interface{}{}
	levelUps := []map[string]interface{}{}

	for _, charID := range req.CharacterIDs {
		// Get current XP, level, and subclass
		var name string
		var currentXP, currentLevel int
		var subclass sql.NullString
		err = db.QueryRow(`
			SELECT name, COALESCE(xp, 0), level, subclass FROM characters WHERE id = $1
		`, charID).Scan(&name, &currentXP, &currentLevel, &subclass)

		if err != nil {
			continue
		}

		newXP := currentXP + req.XP
		newLevel := getLevelForXP(newXP)
		planned := plannedNextClass(charID) // a multiclass level the player planned (POST level-up {"class", "plan": true})
		if ((req.AutoLevel != nil && !*req.AutoLevel) || planned != "") && newLevel > currentLevel {
			// the XP is banked: the player levels up when ready, choosing the class (multiclassing) and making the choices
			db.Exec(`UPDATE characters SET xp = $1 WHERE id = $2`, newXP, charID)
			var lob int
			db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&lob)
			note := fmt.Sprintf("⬆️ %s has enough XP for level %d: take it with POST /api/characters/%d/level-up (optionally {\"class\": \"...\"} to multiclass).", name, currentLevel+1, charID)
			if planned != "" {
				note = fmt.Sprintf("⬆️ %s has enough XP for level %d and planned it in %s: take it with POST /api/characters/%d/level-up {\"class\": \"%s\"}.", name, currentLevel+1, planned, charID, planned)
			}
			if lob > 0 {
				recordGMAction(db, lob, "level_available", note, note)
			}
			results = append(results, map[string]interface{}{"character_id": charID, "character_name": name, "xp_gained": req.XP, "total_xp": newXP, "level_available": currentLevel + 1, "note": note})
			continue
		}

		// Update character
		_, err = db.Exec(`UPDATE characters SET xp = $1 WHERE id = $2`, newXP, charID)
		if err != nil {
			continue
		}

		result := map[string]interface{}{
			"character_id":   charID,
			"character_name": name,
			"xp_gained":      req.XP,
			"total_xp":       newXP,
		}

		// Check for level up
		if newLevel > currentLevel {
			// one level-up path for every way a level goes up (level_up_2024.go): Hit Points, ASI points, a public note of the choices
			hpBonus, asiEarned, note := applyLevelGain(charID, currentLevel, newLevel, "XP award")
			result["level_up"] = true
			result["old_level"] = currentLevel
			result["new_level"] = newLevel
			result["hp_bonus"] = hpBonus
			result["level_up_note"] = note
			if asiEarned > 0 {
				result["asi_earned"] = asiEarned
			}
			levelUps = append(levelUps, map[string]interface{}{
				"character_name": name,
				"old_level":      currentLevel,
				"new_level":      newLevel,
				"asi_earned":     asiEarned,
				"hp_bonus":       hpBonus,
				"note":           note,
			})
		} else {
			result["level"] = currentLevel
			result["xp_to_next_level"] = getXPForNextLevel(currentLevel) - newXP
		}

		results = append(results, result)
	}

	// Log XP award as an action
	reason := req.Reason
	if reason == "" {
		reason = fmt.Sprintf("XP award: %d", req.XP)
	}

	if len(req.CharacterIDs) > 0 {
		var lobbyID int
		db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, req.CharacterIDs[0]).Scan(&lobbyID)

		if lobbyID > 0 {
			charNames := []string{}
			for _, r := range results {
				if name, ok := r["character_name"].(string); ok {
					charNames = append(charNames, name)
				}
			}

			_, err = db.Exec(`
				INSERT INTO actions (lobby_id, action_type, description, result)
				VALUES ($1, 'xp_award', $2, $3)
			`, lobbyID, reason, fmt.Sprintf("%d XP to: %s", req.XP, strings.Join(charNames, ", ")))
		}
	}

	response := map[string]interface{}{
		"success": true,
		"awards":  results,
	}

	if len(levelUps) > 0 {
		response["level_ups"] = levelUps
		response["message"] = fmt.Sprintf("%d character(s) leveled up!", len(levelUps))
	}

	json.NewEncoder(w).Encode(response)
}

// handleGMGold godoc
// @Summary Award or deduct currency from characters
// @Description GM adjusts currency for one or more characters. Use positive amount to award, negative to deduct. Supports all the coins of the SRD 5.2.1: cp (copper), sp (silver), ep (electrum), gp (gold, default), pp (platinum).
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_ids=[]integer,amount=integer,currency=string,reason=string} true "Currency adjustment"
// @Success 200 {object} map[string]interface{} "Currency adjusted"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/gold [post]
func handleGMGold(w http.ResponseWriter, r *http.Request) {
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
		CharacterIDs []int  `json:"character_ids"`
		CharacterID  int    `json:"character_id"`
		Amount       int    `json:"amount"`
		Currency     string `json:"currency"` // cp, sp, ep, gp (default), pp
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if len(req.CharacterIDs) == 0 && req.CharacterID != 0 {
		req.CharacterIDs = []int{req.CharacterID}
	}

	if len(req.CharacterIDs) == 0 || req.Amount == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_ids and non-zero amount required",
		})
		return
	}

	// Get currency column (defaults to gold)
	column, abbrev, valid := getCurrencyColumn(req.Currency)
	if !valid {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_currency",
			"message": "currency must be one of: cp (copper), sp (silver), ep (electrum), gp (gold), pp (platinum)",
		})
		return
	}

	// Verify this agent is the GM of all these characters' campaigns
	for _, charID := range req.CharacterIDs {
		var dmID int
		err = db.QueryRow(`
			SELECT l.dm_id FROM characters c 
			JOIN lobbies l ON c.lobby_id = l.id 
			WHERE c.id = $1
		`, charID).Scan(&dmID)

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		if dmID != agentID {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_gm",
				"message": fmt.Sprintf("You are not the GM for character %d's campaign", charID),
			})
			return
		}
	}

	// Adjust currency for each character
	results := []map[string]interface{}{}

	for _, charID := range req.CharacterIDs {
		var name string
		var currentAmount int
		err = db.QueryRow(fmt.Sprintf(`
			SELECT name, COALESCE(%s, 0) FROM characters WHERE id = $1
		`, column), charID).Scan(&name, &currentAmount)

		if err != nil {
			continue
		}

		newAmount := currentAmount + req.Amount
		if newAmount < 0 {
			newAmount = 0 // Don't allow negative currency
		}

		_, err = db.Exec(fmt.Sprintf(`UPDATE characters SET %s = $1 WHERE id = $2`, column), newAmount, charID)
		if err != nil {
			continue
		}

		// Get full currency breakdown for result
		var cp, sp, ep, gp, pp int
		db.QueryRow(`SELECT COALESCE(copper,0), COALESCE(silver,0), COALESCE(electrum,0), COALESCE(gold,0), COALESCE(platinum,0) FROM characters WHERE id = $1`, charID).Scan(&cp, &sp, &ep, &gp, &pp)

		result := map[string]interface{}{
			"character_id":   charID,
			"character_name": name,
			"currency":       abbrev,
			"change":         req.Amount,
			"previous":       currentAmount,
			"current":        newAmount,
			"full_currency": map[string]interface{}{
				"cp": cp, "sp": sp, "ep": ep, "gp": gp, "pp": pp,
				"total_in_gp": float64(cp)/100 + float64(sp)/10 + float64(ep)/2 + float64(gp) + float64(pp)*10,
			},
		}

		// Backwards compatibility for gold
		if abbrev == "gp" {
			result["gold_change"] = req.Amount
			result["previous_gold"] = currentAmount
			result["current_gold"] = newAmount
		}

		results = append(results, result)
	}

	// Log currency change as an action
	reason := req.Reason
	if reason == "" {
		if req.Amount > 0 {
			reason = fmt.Sprintf("Currency award: %d %s", req.Amount, abbrev)
		} else {
			reason = fmt.Sprintf("Currency deduction: %d %s", -req.Amount, abbrev)
		}
	}

	if len(req.CharacterIDs) > 0 {
		var lobbyID int
		db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, req.CharacterIDs[0]).Scan(&lobbyID)

		if lobbyID > 0 {
			charNames := []string{}
			for _, r := range results {
				if name, ok := r["character_name"].(string); ok {
					charNames = append(charNames, name)
				}
			}

			_, err = db.Exec(`
				INSERT INTO actions (lobby_id, action_type, description, result)
				VALUES ($1, 'currency_change', $2, $3)
			`, lobbyID, reason, fmt.Sprintf("%d %s to: %s", req.Amount, abbrev, strings.Join(charNames, ", ")))
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"adjustments": results,
	})
}

// handleGMGiveItem godoc
// @Summary Give item to character
// @Description GM gives an item to a character's inventory. An SRD magic item by name gets its charges ("Wand of Fireballs", 7), a potion its kind ("Potion of Healing (greater)", "Potion of Giant Strength (fire)", "Potion of Resistance (cold)"), a Spell Scroll its spell ("Spell Scroll (Fireball)", "Spell Scroll (Fireball, level 5)"); any other name is added as written.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=integer,item_name=string,quantity=integer,custom=object} true "Item to give"
// @Success 200 {object} map[string]interface{} "Item given successfully"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM of this campaign"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/give-item [post]
func handleGMGiveItem(w http.ResponseWriter, r *http.Request) {
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
		CharacterID int                    `json:"character_id"`
		ItemName    string                 `json:"item_name"` // an SRD item ("Wand of Fireballs", "Potion of Healing", "Spell Scroll (Fireball)") or any name
		Quantity    int                    `json:"quantity"`
		Custom      map[string]interface{} `json:"custom"` // For non-standard items
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 || req.ItemName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_id and item_name required",
		})
		return
	}

	if req.Quantity == 0 {
		req.Quantity = 1
	}

	// Verify this agent is the GM of this character's campaign
	var dmID, lobbyID int
	var charName string
	err = db.QueryRow(`
		SELECT l.dm_id, l.id, c.name FROM characters c 
		JOIN lobbies l ON c.lobby_id = l.id 
		WHERE c.id = $1
	`, req.CharacterID).Scan(&dmID, &lobbyID, &charName)

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
			"message": "You are not the GM for this character's campaign",
		})
		return
	}

	// Build the item to add: an SRD magic item gets its charges, a Spell Scroll its spell (magic_item_use_2024.go)
	var itemToAdd map[string]interface{}
	probe := map[string]interface{}{"name": req.ItemName}
	if u := itemUseFor(probe); u != nil || func() bool { _, ok := scrollOf(probe); return ok }() {
		itemToAdd = map[string]interface{}{"name": req.ItemName, "quantity": req.Quantity}
		if u != nil {
			itemToAdd["name"] = u.Name
			if strings.Contains(strings.ToLower(req.ItemName), "(") || strings.HasPrefix(strings.ToLower(u.Name), "potions") ||
				(itemPlus(req.ItemName) > 0 && strings.Contains(u.Name, "+1, +2, or +3")) {
				// a variant ("Potion of Giant Strength (fire)", "Potion of Healing (greater)"), or one bonus of a "+1, +2, or +3"
				// item ("Shield +2", "Wand of the War Mage, +3"): the generic SRD name would lose the bonus
				itemToAdd["name"] = req.ItemName
			}
		}
		magicItemFields(itemToAdd)
	} else if req.Custom != nil {
		// Custom item provided
		itemToAdd = req.Custom
		itemToAdd["name"] = req.ItemName
		itemToAdd["quantity"] = req.Quantity
	} else {
		// Unknown item, just add by name
		itemToAdd = map[string]interface{}{
			"name":     req.ItemName,
			"type":     "misc",
			"quantity": req.Quantity,
		}
	}

	// Get current inventory
	var inventoryJSON []byte
	db.QueryRow("SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1", req.CharacterID).Scan(&inventoryJSON)
	var inventory []map[string]interface{}
	json.Unmarshal(inventoryJSON, &inventory)

	// Check if item already exists (stack quantities); items in a lost bag do not count
	found := false
	isLostItem := lostChecker(inventory)
	for i, invItem := range inventory {
		if isLostItem(invItem) {
			continue
		}
		if name, ok := invItem["name"].(string); ok && strings.EqualFold(name, req.ItemName) {
			// Stack the quantity
			currentQty := 1
			if q, ok := invItem["quantity"].(float64); ok {
				currentQty = int(q)
			}
			inventory[i]["quantity"] = currentQty + req.Quantity
			found = true
			break
		}
	}

	if !found {
		if _, ok := itemToAdd["id"]; !ok {
			itemToAdd["id"] = newItemID("g")
		}
		inventory = append(inventory, itemToAdd)
	}

	// Update inventory
	updatedInv, _ := json.Marshal(inventory)
	_, err = db.Exec("UPDATE characters SET inventory = $1 WHERE id = $2", updatedInv, req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}

	// Log action
	db.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, 'item_given', $2, $3)
	`, lobbyID, fmt.Sprintf("GM gave %s to %s", req.ItemName, charName), fmt.Sprintf("x%d", req.Quantity))

	giveOut := map[string]interface{}{
		"success":        true,
		"character_id":   req.CharacterID,
		"character_name": charName,
		"item_name":      req.ItemName,
		"quantity":       req.Quantity,
		"inventory_size": len(inventory),
	}
	if ae := autoEquipArmor(req.CharacterID); ae != nil {
		giveOut["auto_equipped"] = ae // plain armor that raises AC is put on outside combat (F214)
	}
	json.NewEncoder(w).Encode(giveOut)
}

// handleGMRecoverAmmo godoc
// @Summary Recover ammunition after combat
// @Description GM triggers ammunition recovery for a character. Recovers half of ammo used since last rest.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=integer,ammo_type=string} true "Recovery details (ammo_type: arrows, bolts, needles, bullets)"
// @Success 200 {object} map[string]interface{} "Recovery result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/recover-ammo [post]
func handleGMRecoverAmmo(w http.ResponseWriter, r *http.Request) {
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

	// Get the GM's active campaign
	var campaignID int
	err = gmCampaignScan(agentID, r, &campaignID)

	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": publicError(err),
		})
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		AmmoType    string `json:"ammo_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id_required"})
		return
	}

	// Default ammo type
	if req.AmmoType == "" {
		req.AmmoType = "arrows"
	}

	// Verify character is in GM's campaign
	var charName string
	var charCampaignID int
	err = db.QueryRow(`SELECT name, lobby_id FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName, &charCampaignID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if charCampaignID != campaignID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_your_campaign",
			"message": "Character is not in your campaign",
		})
		return
	}

	// Recover ammo
	recovered, err := recoverAmmo(req.CharacterID, req.AmmoType)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
		return
	}

	if recovered == 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"recovered": 0,
			"ammo_type": req.AmmoType,
			"character": charName,
			"message":   fmt.Sprintf("%s had no ammunition to recover", charName),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"recovered": recovered,
		"ammo_type": req.AmmoType,
		"character": charName,
		"message":   fmt.Sprintf("%s recovered %d %s", charName, recovered, req.AmmoType),
	})
}

// handleGMInspiration godoc
// @Summary Grant or revoke inspiration
// @Description GM grants or revokes inspiration for a character. Inspiration can be spent for advantage on any d20 roll.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=integer,grant=boolean} true "Grant (true) or revoke (false) Heroic Inspiration"
// @Success 200 {object} map[string]interface{} "Inspiration updated"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/inspiration [post]
func handleGMInspiration(w http.ResponseWriter, r *http.Request) {
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": publicError(err),
		})
		return
	}

	var req struct {
		CharacterID int  `json:"character_id"`
		Grant       bool `json:"grant"`   // true to grant, false to revoke
		GiveTo      int  `json:"give_to"` // the character already has it: it goes to this player character, who lacks it (SRD: otherwise it's lost)
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

	// Get character and verify they're in this campaign
	var charName string
	var charLobbyID int
	var hasInspiration bool
	err = db.QueryRow(`
		SELECT name, lobby_id, COALESCE(inspiration, false) 
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &charLobbyID, &hasInspiration)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if charLobbyID != campaignID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_in_campaign"})
		return
	}

	// Gaining Heroic Inspiration while having it: it's lost unless given to a player character who lacks it (SRD 5.2.1)
	if req.Grant && hasInspiration {
		if req.GiveTo != 0 {
			var otherName string
			var otherLobby int
			var otherHas bool
			if db.QueryRow(`SELECT name, COALESCE(lobby_id,0), COALESCE(inspiration,false) FROM characters WHERE id = $1`, req.GiveTo).Scan(&otherName, &otherLobby, &otherHas) != nil || otherLobby != campaignID {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "give_to_not_in_campaign"})
				return
			}
			if otherHas {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "already_has_inspiration", "message": fmt.Sprintf("%s already has Heroic Inspiration too; give it to someone who lacks it, or it's lost.", otherName)})
				return
			}
			db.Exec(`UPDATE characters SET inspiration = true WHERE id = $1`, req.GiveTo)
			note := fmt.Sprintf("%s already had Heroic Inspiration and gives the new one to %s.", charName, otherName)
			recordGMAction(db, campaignID, "inspiration", note, note)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": note, "changed": true, "given_to": req.GiveTo})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"message":     fmt.Sprintf("%s already has Heroic Inspiration: the new one is lost unless they give it to a player character who lacks it (send give_to with that character's id).", charName),
			"inspiration": true,
			"changed":     false,
		})
		return
	}
	if !req.Grant && !hasInspiration {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"message":     fmt.Sprintf("%s doesn't have Heroic Inspiration to revoke", charName),
			"inspiration": false,
			"changed":     false,
		})
		return
	}

	// Update inspiration
	_, err = db.Exec(`UPDATE characters SET inspiration = $1 WHERE id = $2`, req.Grant, req.CharacterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}

	// Log the action
	action := "granted"
	if !req.Grant {
		action = "revoked"
	}
	db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'inspiration', $3, $4)`,
		campaignID, req.CharacterID, fmt.Sprintf("GM %s Heroic Inspiration", action), fmt.Sprintf("%s now %s Heroic Inspiration", charName, map[bool]string{true: "has", false: "does not have"}[req.Grant]))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"character":   charName,
		"inspiration": req.Grant,
		"changed":     true,
		"message":     fmt.Sprintf("Inspiration %s for %s", action, charName),
		"tip":         fmt.Sprintf("%s can spend Heroic Inspiration to reroll a failed d20 on an ability check or saving throw by adding use_inspiration:true to the roll request (the new roll is used)", charName),
	})
}

// levelUpHitPoints is the Hit Points gained over several levels by the fixed value: half the Hit Die + 1 + Constitution modifier
// (at least 1 a level), plus Dwarven Toughness (+1 a level) and the Tough feat (+2 a level).
func levelUpHitPoints(charID int, class string, levels int) int {
	if levels <= 0 {
		return 0
	}
	var con int
	db.QueryRow(`SELECT con FROM characters WHERE id = $1`, charID).Scan(&con)
	hitDie := 8
	if c, ok := srdClasses[strings.ToLower(class)]; ok && c.HitDie > 0 {
		hitDie = c.HitDie
	}
	per := hitDie/2 + 1 + game.Modifier(con)
	if per < 1 {
		per = 1
	}
	per += speciesHPPerLevel(charID)
	if hasSpecificFeat(charID, "tough") {
		per += 2
	}
	return per * levels
}
