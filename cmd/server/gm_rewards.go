package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// handleGMUpdateCharacter godoc
// @Summary Change any of a character's data (GM)
// @Description The GM of the character's campaign can change any of its data. GET ?character_id=N returns every field and its value. POST {"character_id": N, "set": {"field": value, ...}} writes any field of the character (hp, temp_hp, gold, conditions, inventory, spell_slots_used, known_spells, feats, exhaustion_level, subclass, ...; JSON fields take arrays or objects; null clears a field); only id, agent_id, lobby_id and created_at are fixed. The older shortcuts still work: class, species, background, name, str, dex, con, intl, wis, cha, hp, max_hp, level, and items (names added to the inventory). Hit Points (hp, max_hp, temp_hp, is_dead, is_stable, death_save_successes, death_save_failures) are one change the rules allow, made after the other fields: the maximum at least 1 and hp 0 to it; is_dead true puts the character at 0; a dead character is given Hit Points only with is_dead false (hp, or 1); Death Saving Throws and is_stable only at 0 Hit Points. Anything else is refused (400 invalid_field) and nothing is changed.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Body Character updates
// @Success 200 {object} map[string]interface{} "Updated character"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /gm/update-character [get]
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
		if id <= 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required", "message": "Send ?character_id= of a character in a campaign you run."})
			return
		}
		want, _ := strconv.Atoi(r.URL.Query().Get("campaign_id"))
		if _, status, code, msg := gmTargetCampaign(agentID, want, []int{id}, gmSpec([]bodyOpt{campaignBeforeStart()}).gmStatuses()); code != "" {
			writeJSONStatus(w, status, map[string]interface{}{"error": code, "message": msg})
			return
		}
		writeJSONStatus(w, 200, map[string]interface{}{"character_id": id, "fields": gmCharacterRecord(id), "fixed_fields": []string{"id", "agent_id", "lobby_id", "created_at"}})
		return
	}

	var req struct {
		CharacterID int                        `json:"character_id"`
		Class       *string                    `json:"class"`      // a new class
		Species     *string                    `json:"species"`    // a new species
		Background  *string                    `json:"background"` // a new background
		Items       []string                   `json:"items"`      // items to add to the inventory, by name
		STR         *int                       `json:"str"`        // set Strength
		DEX         *int                       `json:"dex"`        // set Dexterity
		CON         *int                       `json:"con"`        // set Constitution
		INT         *int                       `json:"intl"`       // set Intelligence (the field is named intl)
		WIS         *int                       `json:"wis"`        // set Wisdom
		CHA         *int                       `json:"cha"`        // set Charisma
		HP          *int                       `json:"hp"`         // set Hit Points
		MaxHP       *int                       `json:"max_hp"`     // set the Hit Point maximum
		Level       *int                       `json:"level"`      // set the level
		Name        *string                    `json:"name"`       // a new name
		Set         map[string]json.RawMessage `json:"set"`        // any field: {"gold": 500, "conditions": ["prone"]}
	}
	// the characters' campaign, which must be one the GM runs (gm_campaign_2024.go)
	_, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID), campaignBeforeStart())
	if !ok {
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
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
	// the scores go through the ability score owner (ability_owner_2024.go), after the other fields
	scores := map[string]int{}
	for short, v := range map[string]*int{"str": req.STR, "dex": req.DEX, "con": req.CON, "int": req.INT, "wis": req.WIS, "cha": req.CHA} {
		if v != nil {
			scores[short] = *v
		}
	}
	// Hit Points (hp, max_hp, temp_hp, is_dead, is_stable, the Death Saving Throws: the shortcuts or set) are one edit of the Hit Point
	// owner (editVitals, hit_points_2024.go), after the other fields and a new level: one state the rules allow, or a refusal before
	// anything is written.
	vitals := map[string]interface{}{}
	if req.HP != nil {
		vitals["hp"] = float64(*req.HP)
	}
	if req.MaxHP != nil {
		vitals["max_hp"] = float64(*req.MaxHP)
	}
	for k, raw := range req.Set {
		if key := strings.ToLower(strings.TrimSpace(k)); vitalsFields[key] {
			var v interface{}
			if json.Unmarshal(raw, &v) != nil {
				v = string(raw)
			}
			vitals[key] = v
			delete(req.Set, k)
		}
	}
	vitalsEdit, vitalsErr := vitalsEditFrom(vitals)
	if vitalsErr == nil {
		var before game.Vitals
		if before, vitalsErr = readVitals(db, req.CharacterID); vitalsErr != nil {
			writeDBError(w, vitalsErr, "the character")
			return
		}
		_, vitalsErr = before.Edit(vitalsEdit) // checked before anything is written; applied after the other fields
	}
	if vitalsErr != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_field", "message": vitalsErr.Error() + " Nothing was changed."})
		return
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
	if answerHelperDBFailure(w, setErr) {
		return
	}
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

	if len(updates) == 0 && len(req.Items) == 0 && len(changed) == 0 && vitalsEdit.Empty() && len(scores) == 0 {
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
			writeDBWriteError(w, err, "the character")
			return
		}
	}
	if len(scores) > 0 {
		if _, err := setAbilityScores(req.CharacterID, scores); err != nil {
			writeDBWriteError(w, err, "the character's ability scores")
			return
		}
	}

	if !vitalsEdit.Empty() {
		if _, err := editVitals(req.CharacterID, vitalsEdit); err != nil {
			var refused vitalsRefused
			if errors.As(err, &refused) { // a new level changed the maximum since the check
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_field", "message": err.Error() + " The other fields were changed."})
				return
			}
			writeDBWriteError(w, err, "the character's Hit Points")
			return
		}
		for k := range vitals {
			changed = append(changed, k)
		}
		sort.Strings(changed)
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
// @Param Authorization header string true "Bearer <token>"
// @Body XP award details (auto_level false: the XP is banked and the player takes the level with POST /api/characters/{id}/level-up)
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
		XP           int    `json:"xp"`         // the XP to award each character
		Reason       string `json:"reason"`     // why (shown in the feed)
		AutoLevel    *bool  `json:"auto_level"` // default true; false banks the XP and the player takes the level (POST /api/characters/{id}/level-up)
	}
	// the characters' campaign, which must be one the GM runs (gm_campaign_2024.go)
	if _, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterIDs), campaignBeforeStart()); !ok {
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

	// A campaign that levels by milestones doesn't use Experience Points (advancement_2024.go)
	for _, charID := range req.CharacterIDs {
		if lob := lobbyOfChar(charID); milestoneCampaign(lob) {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "milestone_campaign",
				"message": fmt.Sprintf("This campaign uses milestones, not Experience Points: grant a level with POST /api/gm/milestone {\"campaign_id\": %d}. To use XP instead, set the table rule: POST /api/campaigns/%d/table-rules {\"advancement\": \"xp\"}. Nothing was awarded.", lob, lob)})
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

		// the XP is added in the database and read back with the level as they are then: two awards at once both count (B-18)
		var newXP int
		if err := db.QueryRow(`UPDATE characters SET xp = COALESCE(xp, 0) + $1 WHERE id = $2 RETURNING xp, COALESCE(level, 1)`, req.XP, charID).Scan(&newXP, &currentLevel); err != nil {
			continue
		}
		newLevel := getLevelForXP(newXP)
		if req.AutoLevel != nil && !*req.AutoLevel && newLevel > currentLevel {
			// the XP is banked: the player levels up when ready and makes the choices
			var lob int
			db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&lob)
			note := fmt.Sprintf("⬆️ %s has enough XP for level %d: take it with POST /api/characters/%d/level-up.", name, currentLevel+1, charID)
			if lob > 0 {
				recordGMAction(db, lob, "level_available", note, note)
			}
			results = append(results, map[string]interface{}{"character_id": charID, "character_name": name, "xp_gained": req.XP, "total_xp": newXP, "level_available": currentLevel + 1, "note": note})
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
			if note == "" { // the level was taken meanwhile (another award or the player's level-up): the XP counts, no second level
				results = append(results, result)
				continue
			}
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

			err = writeAction(db, lobbyID, 0, "xp_award", reason, fmt.Sprintf("%d XP to: %s", req.XP, strings.Join(charNames, ", ")))
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
// @Description GM adjusts currency for one or more characters. Use positive amount to award, negative to deduct. A deduction is a payment from the whole purse: the coin named pays it when it holds enough, otherwise the purse makes change (what is left is written as GP, SP and CP); a character who can't pay refuses the whole request (409 not_enough_money, nothing changed). Supports all the coins of the SRD 5.2.1: cp (copper), sp (silver), ep (electrum), gp (gold, default), pp (platinum).
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Body Currency adjustment
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
		Amount       int    `json:"amount"`   // the amount: positive awards, negative deducts
		Currency     string `json:"currency"` // cp, sp, ep, gp (default), pp
		Reason       string `json:"reason"`   // why (shown in the feed)
	}
	// the characters' campaign, which must be one the GM runs (gm_campaign_2024.go)
	if _, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterIDs, &req.CharacterID), campaignBeforeStart()); !ok {
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
	_, abbrev, valid := getCurrencyColumn(req.Currency)
	if !valid {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_currency",
			"message": "currency must be one of: cp (copper), sp (silver), ep (electrum), gp (gold), pp (platinum)",
		})
		return
	}

	// Adjust currency for each character: an award adds the coin; a deduction is a payment from the whole purse, which makes change when the
	// coin named is short (the purse owner, money_2024.go: I-ALDEN-1). The whole request is one locked step: a character who can't pay
	// refuses it and nothing changes for anyone.
	results := []map[string]interface{}{}
	var short []string
	err = inTx(func(tx *sql.Tx) error {
		results, short = results[:0], short[:0]
		for _, charID := range req.CharacterIDs {
			var name string
			if err := tx.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&name); err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				return err
			}
			before, err := lockPurse(tx, charID)
			if err != nil {
				return err
			}
			after := before
			if req.Amount > 0 {
				*after.coin(abbrev) += req.Amount
			} else if paid, ok := before.pay(-req.Amount, abbrev); ok {
				after = paid
			} else {
				short = append(short, fmt.Sprintf("%s has %s", name, before.text()))
				continue
			}
			if err := writePurseIn(tx, charID, after); err != nil {
				return err
			}
			result := map[string]interface{}{
				"character_id":   charID,
				"character_name": name,
				"currency":       abbrev,
				"change":         req.Amount,
				"previous":       *before.coin(abbrev),
				"current":        *after.coin(abbrev),
				"purse_before":   before.text(),
				"purse_after":    after.text(),
				"full_currency":  after.view(),
			}
			if abbrev == "gp" { // Backwards compatibility for gold
				result["gold_change"] = req.Amount
				result["previous_gold"] = before.GP
				result["current_gold"] = after.GP
			}
			results = append(results, result)
		}
		if len(short) > 0 {
			return errPurseShort
		}
		return nil
	})
	if err == errPurseShort {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "not_enough_money",
			"message": fmt.Sprintf("Can't take %d %s: %s. Nothing was changed for anyone.", -req.Amount, strings.ToUpper(abbrev), strings.Join(short, "; "))})
		return
	}
	if err != nil {
		writeDBWriteError(w, err, "the characters' money")
		return
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

			err = writeAction(db, lobbyID, 0, "currency_change", reason, fmt.Sprintf("%d %s to: %s", req.Amount, abbrev, strings.Join(charNames, ", ")))
		}
	}

	reply := map[string]interface{}{
		"success":     true,
		"adjustments": results,
	}
	if warn := recordNumberWarning(req.Reason); warn != "" { // the reason is read verbatim in the players' log (playtest S-CALLA-12)
		reply["warning"] = warn
	}
	json.NewEncoder(w).Encode(reply)
}

// handleGMGiveItem godoc
// @Summary Give item to character
// @Description GM gives an item to a character's inventory. An SRD magic item by name gets its charges ("Wand of Fireballs", 7), a potion its kind ("Potion of Healing (greater)", "Potion of Giant Strength (fire)", "Potion of Resistance (cold)"), a Spell Scroll its spell ("Spell Scroll (Fireball)", "Spell Scroll (Fireball, level 5)"); any other name is added as written.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Body Item to give
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
		Quantity    int                    `json:"quantity"`  // how many (default 1)
		Custom      map[string]interface{} `json:"custom"`    // For non-standard items
	}
	// the characters' campaign, which must be one the GM runs (gm_campaign_2024.go)
	lobbyID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID), campaignBeforeStart())
	if !ok {
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

	var charName string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName)

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

	// Add it to the inventory as it is now, locked (inventory_2024.go): items given at once are all kept
	inventorySize := 0
	_, err = changeInventory(req.CharacterID, func(_ *sql.Tx, inventory []invItem) ([]invItem, error) {
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
		inventorySize = len(inventory)
		return inventory, nil
	})
	if answerDBFailure(w, err, "the character's pack") {
		return
	}

	// Log action
	writeAction(db, lobbyID, 0, "item_given", fmt.Sprintf("GM gave %s to %s", req.ItemName, charName), fmt.Sprintf("x%d", req.Quantity))

	giveOut := map[string]interface{}{
		"success":        true,
		"character_id":   req.CharacterID,
		"character_name": charName,
		"item_name":      req.ItemName,
		"quantity":       req.Quantity,
		"inventory_size": inventorySize,
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
// @Body Recovery details (ammo_type: arrows, bolts, needles, bullets)
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

	var req struct {
		CharacterID int    `json:"character_id"`
		AmmoType    string `json:"ammo_type"` // arrows, bolts, needles or bullets
	}
	// the characters' campaign, which must be one the GM runs (gm_campaign_2024.go)
	if _, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID)); !ok {
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

	var charName string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName)

	// Recover ammo
	recovered, err := recoverAmmo(req.CharacterID, req.AmmoType)
	if err != nil {
		writeDBWriteError(w, err, "the recovered ammunition")
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
// @Param Authorization header string true "Bearer <token>"
// @Body Grant (true) or revoke (false) Heroic Inspiration
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

	var req struct {
		CharacterID int  `json:"character_id"`
		Grant       bool `json:"grant"`   // true to grant, false to revoke
		GiveTo      int  `json:"give_to"` // the character already has it: it goes to this player character, who lacks it (SRD: otherwise it's lost)
	}
	// the characters' campaign (give_to included), which must be one the GM runs (gm_campaign_2024.go)
	campaignID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID, &req.GiveTo))
	if !ok {
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	// Get character and verify they're in this campaign
	var charName string
	var hasInspiration bool
	db.QueryRow(`SELECT name, COALESCE(inspiration, false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName, &hasInspiration)

	// Gaining Heroic Inspiration while having it: it's lost unless given to a player character who lacks it (SRD 5.2.1)
	if req.Grant && hasInspiration {
		if req.GiveTo != 0 {
			var otherName string
			var otherHas bool
			db.QueryRow(`SELECT name, COALESCE(inspiration,false) FROM characters WHERE id = $1`, req.GiveTo).Scan(&otherName, &otherHas)
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
	writeAction(db, campaignID, req.CharacterID, "inspiration", fmt.Sprintf("GM %s Heroic Inspiration", action), fmt.Sprintf("%s now %s Heroic Inspiration", charName, map[bool]string{true: "has", false: "does not have"}[req.Grant]))

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
