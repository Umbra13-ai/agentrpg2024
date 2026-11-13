package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// handleCharacterASI godoc
// @Summary Apply Ability Score Improvement
// @Description Spend pending ASI points to increase ability scores. Max 20 per ability.
// @Tags Characters
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param id path int true "Character ID"
// @Param request body object{ability=string,points=integer} true "ASI application"
// @Success 200 {object} map[string]interface{} "ASI applied"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not your character"
// @Router /characters/{id}/asi [post]
func handleCharacterASI(w http.ResponseWriter, r *http.Request, charID int) {
	defer refreshStoredAC(charID) // Dexterity, Constitution and Wisdom change unarmored AC
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

	// Verify ownership
	var ownerID, pendingASI int
	var str, dex, con, intl, wis, cha int
	var asiClass string
	var asiLevel int
	var asiClassLevelsJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, COALESCE(pending_asi, 0), str, dex, con, intl, wis, cha,
			class, level, COALESCE(class_levels, '{}')
		FROM characters WHERE id = $1
	`, charID).Scan(&ownerID, &pendingASI, &str, &dex, &con, &intl, &wis, &cha,
		&asiClass, &asiLevel, &asiClassLevelsJSON)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	if pendingASI <= 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_asi_available",
			"message": "You have no ability score improvement points to spend.",
		})
		return
	}

	var req struct {
		Ability string `json:"ability"`
		Points  int    `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.Points <= 0 || req.Points > pendingASI {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_points",
			"message": fmt.Sprintf("Points must be between 1 and %d (your available ASI points)", pendingASI),
		})
		return
	}

	// Validate ability and get current value
	ability := strings.ToLower(req.Ability)
	var currentVal int
	var column string
	switch ability {
	case "str", "strength":
		currentVal = str
		column = "str"
	case "dex", "dexterity":
		currentVal = dex
		column = "dex"
	case "con", "constitution":
		currentVal = con
		column = "con"
	case "int", "intelligence":
		currentVal = intl
		column = "intl"
	case "wis", "wisdom":
		currentVal = wis
		column = "wis"
	case "cha", "charisma":
		currentVal = cha
		column = "cha"
	default:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_ability",
			"message": "Ability must be one of: str, dex, con, int, wis, cha",
		})
		return
	}

	// an Ability Score Improvement stops at 20 (Primal Champion and Epic Boons raise scores past it on their own)
	abilityMax := asiScoreMax
	newVal := currentVal + req.Points
	if newVal > abilityMax {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "exceeds_maximum",
			"message": fmt.Sprintf("Cannot increase %s above %d. Current: %d, Requested increase: %d", ability, abilityMax, currentVal, req.Points),
		})
		return
	}

	// Apply the ASI
	// one guarded statement: two requests at once can't both spend the same points or pass the maximum
	query := fmt.Sprintf(`UPDATE characters SET %s = %s + $1, pending_asi = pending_asi - $1 WHERE id = $2 AND COALESCE(pending_asi, 0) >= $1 AND %s + $1 <= $3`, column, column, column)
	res, err := db.Exec(query, req.Points, charID, abilityMax)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "asi_changed", "message": "Your ability scores or points changed meanwhile; check them and try again."})
		return
	}

	// Also update max_hp if CON was increased (level * CON modifier change)
	if column == "con" {
		var level, maxHP int
		db.QueryRow(`SELECT level, max_hp FROM characters WHERE id = $1`, charID).Scan(&level, &maxHP)
		oldMod := game.Modifier(currentVal)
		newMod := game.Modifier(newVal)
		if newMod > oldMod {
			hpIncrease := level * (newMod - oldMod)
			db.Exec(`UPDATE characters SET max_hp = max_hp + $1, hp = hp + $1 WHERE id = $2`, hpIncrease, charID)
		}
	}

	// Recalculate AC if DEX was increased (only if not wearing heavy armor - simplified, assume yes)
	// For now we'll leave AC calculation to be handled by equipment system

	remainingASI := pendingASI - req.Points
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"ability":       column,
		"old_value":     currentVal,
		"new_value":     newVal,
		"points_spent":  req.Points,
		"remaining_asi": remainingASI,
		"message":       fmt.Sprintf("Increased %s from %d to %d! %d ASI points remaining.", strings.ToUpper(column), currentVal, newVal, remainingASI),
	})
}

// handleCharacterFeat godoc
// @Summary Take a feat instead of ASI
// @Description Spend 2 ASI points to gain a feat. Each feat can only be taken once. Some feats have prerequisites (ability scores, spellcasting, etc.).
// @Tags Characters
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param id path int true "Character ID"
// @Param request body object{feat=string,ability_choice=string} true "Feat selection - feat slug required, ability_choice for feats like Resilient/Observant"
// @Success 200 {object} map[string]interface{} "Feat gained"
// @Failure 400 {object} map[string]interface{} "Invalid request or prerequisite not met"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not your character"
// @Router /characters/{id}/feat [post]
func handleCharacterFeat(w http.ResponseWriter, r *http.Request, charID int) {
	defer refreshStoredAC(charID)
	if r.Method == "GET" {
		// List available feats
		w.Header().Set("Content-Type", "application/json")
		featList := []map[string]interface{}{}
		for slug, feat := range availableFeats {
			featList = append(featList, map[string]interface{}{
				"slug":         slug,
				"name":         feat.Name,
				"description":  feat.Description,
				"category":     feat.Category,
				"prerequisite": featPrereqText(feat.Prerequisite),
				"repeatable":   feat.Repeatable,
				"benefits":     feat.Benefits,
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"available_feats": featList,
			"cost":            "2 ASI points (one ASI slot)",
			"how_to_take":     "POST /api/characters/{id}/feat with {\"feat\": \"slug\"}",
		})
		return
	}

	if r.Method != "POST" {
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Get character data
	var ownerID, pendingASI, level int
	var str, dex, con, intl, wis, cha, maxHP int
	var class string
	var featsJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, COALESCE(pending_asi, 0), level, str, dex, con, intl, wis, cha, 
		       class, max_hp, COALESCE(feats, '[]')
		FROM characters WHERE id = $1
	`, charID).Scan(&ownerID, &pendingASI, &level, &str, &dex, &con, &intl, &wis, &cha, &class, &maxHP, &featsJSON)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	// Feats cost 2 ASI points (one full ASI slot); Lessons of the First Ones grants Origin feats without them
	lessonsGranted, lessonsUsed := lessonsOfTheFirstOnes(charID)
	viaLessons := lessonsGranted > lessonsUsed
	if pendingASI < 2 && !viaLessons {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "insufficient_asi",
			"message": fmt.Sprintf("Taking a feat costs 2 ASI points. You have %d.", pendingASI),
		})
		return
	}

	var req struct {
		Feat          string `json:"feat"`
		AbilityChoice string `json:"ability_choice"` // For feats like Resilient, Observant
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	featSlug := strings.ToLower(strings.TrimSpace(req.Feat))
	feat, exists := availableFeats[featSlug]
	if !exists {
		// the feats this character can take with Ability Score Improvement points, in order
		featSlugs := []string{}
		for slug, f := range availableFeats {
			if f.Category == "Origin" || f.Category == "Fighting Style" || (f.Category == "Epic Boon" && level < 19) {
				continue
			}
			if ok, _ := featPrerequisite(f.Prerequisite, class, level, str, dex); ok {
				featSlugs = append(featSlugs, slug)
			}
		}
		sort.Strings(featSlugs)
		msg := fmt.Sprintf("%q is not a feat on this server.", req.Feat)
		if note := phbNote("feat", req.Feat); note != "" {
			msg = note
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error":           "unknown_feat",
			"message":         msg + " Feats you can take now: " + strings.Join(featSlugs, ", ") + ".",
			"available_feats": featSlugs,
		})
		return
	}

	if featSlug == "ability_score_improvement" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "use_asi",
			"message": "Ability Score Improvement is a feat you take with POST /api/characters/{id}/asi.",
		})
		return
	}

	// Check if already has this feat
	var currentFeats []string
	json.Unmarshal(featsJSON, &currentFeats)
	for _, f := range currentFeats {
		if f == featSlug && !feat.Repeatable {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "already_has_feat",
				"message": fmt.Sprintf("You already have the %s feat.", feat.Name),
			})
			return
		}
	}

	// Check prerequisites
	if ok, msg := featPrerequisite(feat.Prerequisite, class, level, str, dex); !ok {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "prerequisite_not_met",
			"prerequisite": featPrereqText(feat.Prerequisite),
			"message":      msg,
		})
		return
	}

	// Handle ability choice for feats like Resilient or Observant
	abilityChoice := strings.ToLower(strings.TrimSpace(req.AbilityChoice))
	if feat.AbilityBonus != nil {
		if _, hasChosen := feat.AbilityBonus["chosen"]; hasChosen {
			if abilityChoice == "" {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "ability_choice_required",
					"message": "This feat requires you to choose an ability score. Include 'ability_choice' in your request (str, dex, con, int, wis, or cha).",
				})
				return
			}
		}
		if _, hasIntOrWis := feat.AbilityBonus["int_or_wis"]; hasIntOrWis {
			if abilityChoice == "" {
				abilityChoice = "wis" // Default to WIS for Observant
			}
			if abilityChoice != "int" && abilityChoice != "wis" {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "invalid_ability_choice",
					"message": "For this feat, ability_choice must be 'int' or 'wis'.",
				})
				return
			}
		}
	}

	// 2024 feats that raise an ability score: the player names which one
	if len(feat.AbilityChoices) > 0 {
		okChoice := false
		for _, c := range feat.AbilityChoices {
			if c == abilityChoice {
				okChoice = true
			}
		}
		if !okChoice {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "ability_choice_required",
				"message": "This feat raises one ability score. Send ability_choice as one of: " + strings.Join(feat.AbilityChoices, ", ") + ".",
			})
			return
		}
	}

	// Apply the feat
	currentFeats = append(currentFeats, featSlug)
	featsBytes, _ := json.Marshal(currentFeats)

	// Start building the update query
	updates := []string{"feats = $1", "pending_asi = pending_asi - 2"}
	if viaLessons && strings.EqualFold(feat.Category, "Origin") && pendingASI < 2 || viaLessons && strings.EqualFold(feat.Category, "Origin") {
		updates = []string{"feats = $1"} // Lessons of the First Ones
		setUsedResource(charID, "lessons_feats", lessonsUsed+1)
	} else if pendingASI < 2 {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "insufficient_asi", "message": "Lessons of the First Ones grants Origin feats only; other feats cost 2 ASI points."})
		return
	}
	args := []interface{}{featsBytes}
	argIndex := 2

	// Apply ability bonuses
	abilityIncreased := ""
	if feat.AbilityBonus != nil {
		for ability, bonus := range feat.AbilityBonus {
			targetAbility := ability
			if ability == "chosen" || ability == "int_or_wis" {
				targetAbility = abilityChoice
			}

			// Map to column name
			var column string
			var currentVal int
			switch targetAbility {
			case "str":
				column = "str"
				currentVal = str
			case "dex":
				column = "dex"
				currentVal = dex
			case "con":
				column = "con"
				currentVal = con
			case "int":
				column = "intl"
				currentVal = intl
			case "wis":
				column = "wis"
				currentVal = wis
			case "cha":
				column = "cha"
				currentVal = cha
			default:
				continue
			}

			// "to a maximum of 20": the increase stops at 20, and a score already above 20 (Primal Champion, Body and Mind,
			// a belt) is never lowered.
			newVal := currentVal + bonus
			if newVal > 20 {
				newVal = 20
			}
			if newVal < currentVal {
				newVal = currentVal
			}
			updates = append(updates, fmt.Sprintf("%s = $%d", column, argIndex))
			args = append(args, newVal)
			argIndex++
			abilityIncreased = fmt.Sprintf("%s increased by %d (now %d)", strings.ToUpper(targetAbility), bonus, newVal)
		}
	}

	if len(feat.AbilityChoices) > 0 {
		column, currentVal := "", 0
		switch abilityChoice {
		case "str":
			column, currentVal = "str", str
		case "dex":
			column, currentVal = "dex", dex
		case "con":
			column, currentVal = "con", con
		case "int":
			column, currentVal = "intl", intl
		case "wis":
			column, currentVal = "wis", wis
		case "cha":
			column, currentVal = "cha", cha
		}
		capScore := feat.MaxScore
		if capScore == 0 {
			capScore = 20
		}
		newVal := currentVal + feat.AbilityPoints
		if newVal > capScore {
			newVal = capScore
		}
		if newVal < currentVal { // a score already above the feat's maximum stays as it is
			newVal = currentVal
		}
		updates = append(updates, fmt.Sprintf("%s = $%d", column, argIndex))
		args = append(args, newVal)
		argIndex++
		abilityIncreased = fmt.Sprintf("%s increased by %d (now %d)", strings.ToUpper(abilityChoice), feat.AbilityPoints, newVal)
	}

	// Apply Tough feat HP bonus
	if featSlug == "tough" {
		hpBonus := level * 2
		updates = append(updates, fmt.Sprintf("max_hp = max_hp + $%d", argIndex))
		updates = append(updates, fmt.Sprintf("hp = hp + $%d", argIndex))
		args = append(args, hpBonus)
		argIndex++
	}

	// 2024 Alert adds the Proficiency Bonus to Initiative when it is rolled; nothing is stored here.

	// Add character ID as final arg
	args = append(args, charID)
	query := fmt.Sprintf("UPDATE characters SET %s WHERE id = $%d", strings.Join(updates, ", "), argIndex)

	_, err = db.Exec(query, args...)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error", "details": publicError(err)})
		return
	}

	response := map[string]interface{}{
		"success":       true,
		"feat":          feat.Name,
		"feat_slug":     featSlug,
		"description":   feat.Description,
		"benefits":      feat.Benefits,
		"points_spent":  2,
		"remaining_asi": pendingASI - 2,
		"message":       fmt.Sprintf("You gained the %s feat!", feat.Name),
	}

	if abilityIncreased != "" {
		response["ability_bonus"] = abilityIncreased
	}

	if featSlug == "tough" {
		response["hp_bonus"] = level * 2
		response["message"] = fmt.Sprintf("You gained the %s feat! Max HP increased by %d.", feat.Name, level*2)
	}

	// Alert feat message
	if featSlug == "alert" {
		response["initiative_bonus"] = "Proficiency Bonus"
		response["message"] = fmt.Sprintf("You gained the %s feat! You add your Proficiency Bonus to Initiative and can swap Initiative with a willing ally.", feat.Name)
	}

	json.NewEncoder(w).Encode(response)
}

// handleCharacterSubclass godoc
// @Summary Choose or view character subclass
// @Description GET to see available subclasses and current selection. POST to choose a subclass at the appropriate level.
// @Tags Characters
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param character_id query int true "Character ID (for GET)"
// @Param request body object{character_id=integer,subclass=string} false "Subclass selection (for POST)"
// @Success 200 {object} map[string]interface{} "Subclass info or confirmation"
// @Failure 400 {object} map[string]interface{} "Invalid request or not eligible"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /characters/subclass [get]
// @Router /characters/subclass [post]
func handleCharacterSubclass(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Auth
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized", "message": publicError(err)})
		return
	}

	if r.Method == "GET" {
		// View available subclasses for a character
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_character_id",
				"message": "Provide character_id query parameter",
			})
			return
		}

		// Get character info
		var ownerID int
		var class, currentSubclass sql.NullString
		var level int
		err = db.QueryRow(`
			SELECT agent_id, class, level, subclass 
			FROM characters WHERE id = $1
		`, charID).Scan(&ownerID, &class, &level, &currentSubclass)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		if ownerID != agentID {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_your_character",
				"message": "You can only view subclass options for your own characters",
			})
			return
		}

		if !class.Valid || class.String == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_class",
				"message": "Character has no class set",
			})
			return
		}

		// Get available subclasses for this class
		availableSubs := getSubclassesForClass(class.String)
		subOptions := []map[string]interface{}{}
		subclassLevel := 3 // Default

		for slug, sub := range availableSubs {
			subclassLevel = sub.SubclassLevel
			subOptions = append(subOptions, map[string]interface{}{
				"slug":        slug,
				"name":        sub.Name,
				"description": sub.Description,
			})
		}

		response := map[string]interface{}{
			"character_id":   charID,
			"class":          class.String,
			"level":          level,
			"subclass_level": subclassLevel,
		}

		if currentSubclass.Valid && currentSubclass.String != "" {
			// Already has a subclass
			sub := game.AvailableSubclasses[currentSubclass.String]
			activeFeatures := getActiveSubclassFeatures(currentSubclass.String, level)
			featuresInfo := []map[string]interface{}{}
			for _, f := range activeFeatures {
				featuresInfo = append(featuresInfo, map[string]interface{}{
					"name":        f.Name,
					"level":       f.Level,
					"description": f.Description,
					"mechanics":   f.Mechanics,
				})
			}

			response["current_subclass"] = map[string]interface{}{
				"slug":            currentSubclass.String,
				"name":            sub.Name,
				"description":     sub.Description,
				"active_features": featuresInfo,
			}
			response["can_change"] = false
			response["message"] = "Subclass already chosen. Subclasses cannot be changed once selected."
		} else if level < subclassLevel {
			response["current_subclass"] = nil
			response["can_choose"] = false
			response["message"] = fmt.Sprintf("You can choose a subclass at level %d. Current level: %d", subclassLevel, level)
			response["available_subclasses"] = subOptions
		} else {
			response["current_subclass"] = nil
			response["can_choose"] = true
			response["available_subclasses"] = subOptions
			response["how_to_choose"] = fmt.Sprintf("POST /api/characters/subclass with {\"character_id\": %d, \"subclass\": \"slug\"}", charID)
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == "POST" {
		// Choose a subclass
		var req struct {
			CharacterID int      `json:"character_id"`
			Subclass    string   `json:"subclass"`
			BonusSkills []string `json:"bonus_skills"` // For subclasses that grant bonus skill proficiencies (e.g., Lore Bard)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_json",
				"message": publicError(err),
			})
			return
		}

		if req.CharacterID == 0 || req.Subclass == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "missing_fields",
				"message": "Provide character_id and subclass",
			})
			return
		}

		// Get character info
		var ownerID int
		var class, currentSubclass sql.NullString
		var level int
		var charName string
		err = db.QueryRow(`
			SELECT agent_id, name, class, level, subclass 
			FROM characters WHERE id = $1
		`, req.CharacterID).Scan(&ownerID, &charName, &class, &level, &currentSubclass)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", req.CharacterID),
			})
			return
		}

		if ownerID != agentID {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_your_character",
				"message": "You can only choose a subclass for your own characters",
			})
			return
		}

		// a multiclass character's other classes each get their own subclass at that class's level (SRD 5.2.1)
		if sub2, ok2 := game.AvailableSubclasses[strings.ToLower(strings.TrimSpace(req.Subclass))]; ok2 && !strings.EqualFold(sub2.Class, class.String) {
			slug2 := strings.ToLower(strings.TrimSpace(req.Subclass))
			cls2 := strings.ToLower(sub2.Class)
			if clsLevel := classLevelsOf(req.CharacterID)[cls2]; clsLevel > 0 {
				if clsLevel < sub2.SubclassLevel {
					json.NewEncoder(w).Encode(map[string]interface{}{"error": "level_too_low", "required_level": sub2.SubclassLevel, "current_level": clsLevel,
						"message": fmt.Sprintf("You choose the %s subclass at %s level %d; you are a level %d %s.", sub2.Name, titleCase(cls2), sub2.SubclassLevel, clsLevel, titleCase(cls2))})
					return
				}
				if have := subclassesByClass(req.CharacterID)[cls2]; have != "" {
					json.NewEncoder(w).Encode(map[string]interface{}{"error": "already_has_subclass", "current_subclass": have,
						"message": fmt.Sprintf("Your %s levels already have a subclass (%s). Subclasses cannot be changed once selected.", titleCase(cls2), have)})
					return
				}
				setSecondarySubclass(req.CharacterID, cls2, slug2)
				hpBonus := 0
				if bonusStr, ok := getSubclassMechanic(slug2, clsLevel, "bonus_hp_per_level"); ok { // Draconic Resilience: +1 HP per Sorcerer level
					if b, err := strconv.Atoi(bonusStr); err == nil && b > 0 {
						hpBonus = b * clsLevel
						db.Exec("UPDATE characters SET hp = hp + $1, max_hp = max_hp + $1 WHERE id = $2", hpBonus, req.CharacterID)
					}
				}
				refreshStoredAC(req.CharacterID)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "character_id": req.CharacterID, "class": cls2, "subclass": slug2, "subclass_name": sub2.Name,
					"hp_bonus": hpBonus, "features": characterSubclassFeatures(req.CharacterID, class.String, currentSubclass.String, level),
					"message": fmt.Sprintf("%s's %s levels follow the %s (%s level %d).", charName, titleCase(cls2), sub2.Name, titleCase(cls2), clsLevel)})
				return
			}
		}
		// Check if already has a subclass
		if currentSubclass.Valid && currentSubclass.String != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":            "already_has_subclass",
				"current_subclass": currentSubclass.String,
				"message":          "This character already has a subclass. Subclasses cannot be changed once selected.",
			})
			return
		}

		// Validate the subclass exists and is for this class
		subclassSlug := strings.ToLower(strings.TrimSpace(req.Subclass))
		sub, ok := game.AvailableSubclasses[subclassSlug]
		if !ok {
			// List valid options
			validOptions := []string{}
			for slug, s := range game.AvailableSubclasses {
				if strings.ToLower(s.Class) == strings.ToLower(class.String) {
					validOptions = append(validOptions, slug)
				}
			}
			sort.Strings(validOptions)
			msg := fmt.Sprintf("%q is not a %s subclass on this server.", req.Subclass, titleCase(class.String))
			if note := phbNote("subclass", req.Subclass); note != "" {
				msg = note
			}
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
				"error":                                  "invalid_subclass",
				"message":                                msg + " Choose one of: " + strings.Join(validOptions, ", ") + ".",
				"valid_for_" + strings.ToLower(class.String): validOptions,
			})
			return
		}

		// Check class matches
		if strings.ToLower(sub.Class) != strings.ToLower(class.String) {
			validOptions := []string{}
			for slug, s := range game.AvailableSubclasses {
				if strings.ToLower(s.Class) == strings.ToLower(class.String) {
					validOptions = append(validOptions, slug)
				}
			}
			sort.Strings(validOptions)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":         "class_mismatch",
				"message":       fmt.Sprintf("Subclass '%s' is for %s, not %s", sub.Name, sub.Class, class.String),
				"valid_options": validOptions,
			})
			return
		}

		// Check level requirement: the class's own level (a multiclass character's total level isn't its class level)
		if cl := classLevelsOf(req.CharacterID)[strings.ToLower(class.String)]; cl > 0 && cl < level {
			level = cl
		}
		if level < sub.SubclassLevel {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":          "level_too_low",
				"required_level": sub.SubclassLevel,
				"current_level":  level,
				"message":        fmt.Sprintf("You must be level %d to choose the %s subclass. Current level: %d", sub.SubclassLevel, sub.Name, level),
			})
			return
		}

		// Apply the subclass
		_, err = db.Exec("UPDATE characters SET subclass = $1 WHERE id = $2", subclassSlug, req.CharacterID)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "database_error",
				"message": publicError(err),
			})
			return
		}

		// Check for Draconic Resilience HP bonus
		// Draconic sorcerers gain +1 HP per sorcerer level
		var hpBonusApplied int
		if bonusStr, ok := getSubclassMechanic(subclassSlug, level, "bonus_hp_per_level"); ok {
			bonus, err := strconv.Atoi(bonusStr)
			if err == nil && bonus > 0 {
				hpBonusApplied = bonus * level
				db.Exec("UPDATE characters SET hp = hp + $1, max_hp = max_hp + $1 WHERE id = $2", hpBonusApplied, req.CharacterID)
			}
		}

		// Check for bonus armor proficiency (Life Cleric)
		var bonusArmorApplied string
		if _, ok := getSubclassMechanic(subclassSlug, level, "heavy_armor_proficiency"); ok {
			armorType := "heavy"
			// Get current armor proficiencies
			var currentArmorProfs string
			db.QueryRow(`SELECT COALESCE(armor_proficiencies, '') FROM characters WHERE id = $1`, req.CharacterID).Scan(&currentArmorProfs)

			// Add bonus armor type if not already present
			if !strings.Contains(strings.ToLower(currentArmorProfs), strings.ToLower(armorType)) {
				var newArmorProfs string
				if currentArmorProfs == "" {
					newArmorProfs = armorType
				} else {
					newArmorProfs = currentArmorProfs + ", " + armorType
				}
				db.Exec("UPDATE characters SET armor_proficiencies = $1 WHERE id = $2", newArmorProfs, req.CharacterID)
				bonusArmorApplied = armorType
			}
		}

		// Check for bonus skill proficiencies (Lore Bard)
		var bonusSkillsApplied []string
		if numSkillsStr, ok := getSubclassMechanic(subclassSlug, level, "bonus_skill_proficiencies"); ok {
			numSkills, _ := strconv.Atoi(numSkillsStr)
			if numSkills > 0 {
				// Validate that bonus_skills were provided
				if len(req.BonusSkills) != numSkills {
					// List all available skills (any skill for Lore Bard)
					allSkills := []string{
						"acrobatics", "animal handling", "arcana", "athletics", "deception",
						"history", "insight", "intimidation", "investigation", "medicine",
						"nature", "perception", "performance", "persuasion", "religion",
						"sleight of hand", "stealth", "survival",
					}
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":            "bonus_skills_required",
						"message":          fmt.Sprintf("The %s subclass grants %d bonus skill proficiencies. Include bonus_skills array in your request.", sub.Name, numSkills),
						"required_count":   numSkills,
						"available_skills": allSkills,
						"example":          fmt.Sprintf(`{"character_id": %d, "subclass": "%s", "bonus_skills": ["history", "nature", "religion"]}`, req.CharacterID, subclassSlug),
					})
					return
				}

				// Get current skill proficiencies
				var currentSkillProfs string
				db.QueryRow(`SELECT COALESCE(skill_proficiencies, '') FROM characters WHERE id = $1`, req.CharacterID).Scan(&currentSkillProfs)
				currentSkillsList := strings.Split(strings.ToLower(currentSkillProfs), ", ")
				currentSkillsMap := make(map[string]bool)
				for _, s := range currentSkillsList {
					currentSkillsMap[strings.TrimSpace(s)] = true
				}

				// Validate and add each bonus skill
				validSkills := map[string]bool{
					"acrobatics": true, "animal handling": true, "arcana": true, "athletics": true,
					"deception": true, "history": true, "insight": true, "intimidation": true,
					"investigation": true, "medicine": true, "nature": true, "perception": true,
					"performance": true, "persuasion": true, "religion": true, "sleight of hand": true,
					"stealth": true, "survival": true,
				}

				for _, skill := range req.BonusSkills {
					skillLower := strings.ToLower(strings.TrimSpace(skill))
					if !validSkills[skillLower] {
						json.NewEncoder(w).Encode(map[string]interface{}{
							"error":   "invalid_skill",
							"message": fmt.Sprintf("'%s' is not a valid skill", skill),
						})
						return
					}
					if currentSkillsMap[skillLower] {
						json.NewEncoder(w).Encode(map[string]interface{}{
							"error":   "already_proficient",
							"message": fmt.Sprintf("Character is already proficient in '%s'. Choose a different skill.", skill),
						})
						return
					}
					bonusSkillsApplied = append(bonusSkillsApplied, skillLower)
				}

				// Add bonus skills to proficiencies
				var newSkillProfs string
				if currentSkillProfs == "" {
					newSkillProfs = strings.Join(bonusSkillsApplied, ", ")
				} else {
					newSkillProfs = currentSkillProfs + ", " + strings.Join(bonusSkillsApplied, ", ")
				}
				db.Exec("UPDATE characters SET skill_proficiencies = $1 WHERE id = $2", newSkillProfs, req.CharacterID)
			}
		}

		// Get active features
		activeFeatures := getActiveSubclassFeatures(subclassSlug, level)
		featuresInfo := []map[string]interface{}{}
		for _, f := range activeFeatures {
			featuresInfo = append(featuresInfo, map[string]interface{}{
				"name":        f.Name,
				"level":       f.Level,
				"description": f.Description,
			})
		}

		response := map[string]interface{}{
			"success":         true,
			"character_id":    req.CharacterID,
			"character_name":  charName,
			"subclass":        subclassSlug,
			"subclass_name":   sub.Name,
			"class":           class.String,
			"features_gained": featuresInfo,
			"message":         fmt.Sprintf("%s has become a %s!", charName, sub.Name),
		}

		// Include HP bonus info if applied
		if hpBonusApplied > 0 {
			response["hp_bonus_applied"] = hpBonusApplied
			response["hp_bonus_reason"] = "Draconic Resilience: +1 HP per sorcerer level"
		}

		// Include bonus armor proficiency info if applied
		if bonusArmorApplied != "" {
			response["bonus_armor_proficiency"] = bonusArmorApplied
			response["armor_proficiency_reason"] = fmt.Sprintf("%s: Bonus Proficiency grants %s armor proficiency", sub.Name, bonusArmorApplied)
		}

		// Include bonus skill proficiencies info if applied
		if len(bonusSkillsApplied) > 0 {
			response["bonus_skill_proficiencies"] = bonusSkillsApplied
			response["skill_proficiency_reason"] = fmt.Sprintf("%s: Bonus Proficiencies grants %d additional skill proficiencies", sub.Name, len(bonusSkillsApplied))
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleCharacterSubclassChoice godoc
// @Summary Choose a subclass feature option
// @Description Choose from subclass features that offer choices: Hunter's Prey (hunters_prey: colossus_slayer or horde_breaker) and Defensive Tactics (defensive_tactics: escape_the_horde or multiattack_defense). GET lists the pending choices. After each Short or Long Rest a Hunter can replace each of those two options once (SRD 5.2.1); other choices are permanent.
// @Tags Characters
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=integer,feature=string,choice=string} true "Feature choice"
// @Success 200 {object} map[string]interface{} "Choice confirmation"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /characters/subclass-choice [post]
func handleCharacterSubclassChoice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// Show available choices for a character
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_character_id",
				"message": "Provide character_id query parameter",
			})
			return
		}

		var class, subclass sql.NullString
		var level int
		var choicesJSON []byte
		err = db.QueryRow(`
			SELECT class, subclass, level, COALESCE(subclass_choices, '{}')
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &subclass, &level, &choicesJSON)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		if !subclass.Valid || subclass.String == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_subclass",
				"message": "Character has no subclass selected yet. Use POST /api/characters/subclass first.",
			})
			return
		}

		var currentChoices map[string]string
		json.Unmarshal(choicesJSON, &currentChoices)

		// Get pending choices (features with choice mechanics that haven't been chosen yet)
		pendingChoices := []map[string]interface{}{}
		if sub, ok := game.AvailableSubclasses[subclass.String]; ok {
			for _, feat := range sub.Features {
				if feat.Level > level {
					continue // Not yet unlocked
				}
				for mechKey, mechVal := range feat.Mechanics {
					if isChoiceMechanic(subclass.String, mechKey, mechVal) {
						// This is a choice feature
						if _, alreadyChosen := currentChoices[mechKey]; !alreadyChosen {
							// Build options based on feature
							options := getSubclassChoiceOptions(subclass.String, mechKey)
							pendingChoices = append(pendingChoices, map[string]interface{}{
								"feature":     mechKey,
								"name":        feat.Name,
								"description": feat.Description,
								"options":     options,
							})
						}
					}
				}
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"character_id":    charID,
			"subclass":        subclass.String,
			"current_choices": currentChoices,
			"pending_choices": pendingChoices,
		})
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Auth
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized", "message": publicError(err)})
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		Feature     string `json:"feature"` // e.g., "hunters_prey"
		Choice      string `json:"choice"`  // e.g., "colossus_slayer"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_json",
			"message": publicError(err),
		})
		return
	}

	// Get character info
	var ownerID int
	var class, subclass sql.NullString
	var level int
	var charName string
	var choicesJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, name, class, subclass, level, COALESCE(subclass_choices, '{}')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &charName, &class, &subclass, &level, &choicesJSON)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Character %d not found", req.CharacterID),
		})
		return
	}

	if ownerID != agentID {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_your_character",
			"message": "You can only make choices for your own characters",
		})
		return
	}

	if !subclass.Valid || subclass.String == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_subclass",
			"message": "Character has no subclass selected yet. Use POST /api/characters/subclass first.",
		})
		return
	}

	// Validate the feature exists and requires a choice
	sub, ok := game.AvailableSubclasses[subclass.String]
	if !ok {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_subclass",
			"message": fmt.Sprintf("Subclass '%s' not found", subclass.String),
		})
		return
	}

	featureFound := false
	featureName := ""
	for _, feat := range sub.Features {
		if feat.Level > level {
			continue
		}
		if mechVal, ok := feat.Mechanics[req.Feature]; ok && isChoiceMechanic(subclass.String, req.Feature, mechVal) {
			featureFound = true
			featureName = feat.Name
			break
		}
	}

	if !featureFound {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_feature",
			"message": fmt.Sprintf("Feature '%s' not found or doesn't require a choice for %s at level %d", req.Feature, subclass.String, level),
		})
		return
	}

	// Validate the choice is valid for this feature
	validOptions := getSubclassChoiceOptions(subclass.String, req.Feature)
	choiceLower := strings.ToLower(strings.TrimSpace(req.Choice))
	validChoice := false
	var choiceInfo map[string]interface{}
	for _, opt := range validOptions {
		if opt["slug"].(string) == choiceLower {
			validChoice = true
			choiceInfo = opt
			break
		}
	}

	if !validChoice {
		slugs := []string{}
		for _, opt := range validOptions {
			slugs = append(slugs, opt["slug"].(string))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "invalid_choice",
			"message":       fmt.Sprintf("'%s' is not a valid choice for %s", req.Choice, req.Feature),
			"valid_options": validOptions,
			"valid_slugs":   slugs,
		})
		return
	}

	// Update subclass_choices
	var currentChoices map[string]string
	json.Unmarshal(choicesJSON, &currentChoices)
	if currentChoices == nil {
		currentChoices = make(map[string]string)
	}

	// Check if already chosen. Hunter's Prey and Defensive Tactics can be swapped after a Short or Long Rest (SRD 5.2.1).
	swapped := false
	if existing, alreadyChosen := currentChoices[req.Feature]; alreadyChosen {
		var swaps string
		db.QueryRow(`SELECT COALESCE(hunter_swaps,'') FROM characters WHERE id = $1`, req.CharacterID).Scan(&swaps)
		ready := strings.Contains(","+swaps+",", ","+req.Feature+",")
		if !restSwappableChoice(req.Feature) || !ready || existing == choiceLower {
			msg := fmt.Sprintf("You already chose '%s' for %s. This choice is permanent.", existing, featureName)
			if restSwappableChoice(req.Feature) {
				msg = fmt.Sprintf("You chose '%s' for %s. You can replace it with the other option after you finish a Short or Long Rest.", existing, featureName)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":          "already_chosen",
				"current_choice": existing,
				"message":        msg,
			})
			return
		}
		swapped = true
	}

	currentChoices[req.Feature] = choiceLower
	updatedJSON, _ := json.Marshal(currentChoices)

	_, err = db.Exec("UPDATE characters SET subclass_choices = $1 WHERE id = $2", updatedJSON, req.CharacterID)
	if err == nil && swapped {
		// each option can be replaced once per rest
		db.Exec(`UPDATE characters SET hunter_swaps = trim(both ',' from replace(','||COALESCE(hunter_swaps,'')||',', ','||$1||',', ',')) WHERE id = $2`, req.Feature, req.CharacterID)
	}
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "database_error",
			"message": publicError(err),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"character_id":   req.CharacterID,
		"character_name": charName,
		"feature":        req.Feature,
		"feature_name":   featureName,
		"choice":         choiceLower,
		"choice_name":    choiceInfo["name"],
		"description":    choiceInfo["description"],
		"message":        fmt.Sprintf("%s has chosen %s for their %s feature!", charName, choiceInfo["name"], featureName),
	})
}

// isChoiceMechanic: a subclass mechanic is a player choice when it is marked "choice", or when the server lists options for it
// (the generated SRD feature data marks Hunter's Prey and Defensive Tactics "true").
func isChoiceMechanic(subclass, key, val string) bool {
	return val == "choice" || len(getSubclassChoiceOptions(subclass, key)) > 0
}

// restSwappableChoice: choices the SRD lets the character replace after a Short or Long Rest.
func restSwappableChoice(feature string) bool {
	return feature == "hunters_prey" || feature == "defensive_tactics"
}

// getSubclassChoiceOptions returns the valid options for a subclass feature choice
func getSubclassChoiceOptions(subclass, feature string) []map[string]interface{} {
	switch feature {
	case "circle_land":
		// 2024 Circle of the Land: the land type decides the Circle Spells and Nature's Ward (SRD 5.2.1)
		return []map[string]interface{}{
			{"slug": "arid", "name": "Arid", "description": "Circle spells: Blur, Burning Hands, Fire Bolt (3rd), Fireball (5th), Blight (7th), Wall of Stone (9th). Nature's Ward: Resistance to Fire."},
			{"slug": "polar", "name": "Polar", "description": "Circle spells: Fog Cloud, Hold Person, Ray of Frost (3rd), Sleet Storm (5th), Ice Storm (7th), Cone of Cold (9th). Nature's Ward: Resistance to Cold."},
			{"slug": "temperate", "name": "Temperate", "description": "Circle spells: Misty Step, Shocking Grasp, Sleep (3rd), Lightning Bolt (5th), Freedom of Movement (7th), Tree Stride (9th). Nature's Ward: Resistance to Lightning."},
			{"slug": "tropical", "name": "Tropical", "description": "Circle spells: Acid Splash, Ray of Sickness, Web (3rd), Stinking Cloud (5th), Polymorph (7th), Insect Plague (9th). Nature's Ward: Resistance to Poison."},
		}
	case "hunters_prey":
		return []map[string]interface{}{
			{
				"slug":        "colossus_slayer",
				"name":        "Colossus Slayer",
				"description": "Your tenacity can wear down even the most resilient foes. When you hit a creature with a weapon, the weapon deals an extra 1d8 damage to the target if it’s missing any of its Hit Points. You can deal this extra damage only once per turn.",
			},
			{
				"slug":        "horde_breaker",
				"name":        "Horde Breaker",
				"description": "Once on each of your turns when you make an attack with a weapon, you can make another attack with the same weapon against a different creature that is within 5 feet of the original target, that is within the weapon’s range, and that you haven’t attacked this turn.",
			},
		}
	case "defensive_tactics":
		return []map[string]interface{}{
			{
				"slug":        "escape_the_horde",
				"name":        "Escape the Horde",
				"description": "Opportunity Attacks have Disadvantage against you.",
			},
			{
				"slug":        "multiattack_defense",
				"name":        "Multiattack Defense",
				"description": "When a creature hits you with an attack roll, that creature has Disadvantage on all other attack rolls against you this turn.",
			},
		}
	case "dragon_ancestor":
		// Draconic Sorcerer dragon ancestry choice
		return []map[string]interface{}{
			{
				"slug":        "black",
				"name":        "Black Dragon",
				"damage_type": "acid",
				"description": "Black dragon ancestry. Associated damage type: Acid.",
			},
			{
				"slug":        "blue",
				"name":        "Blue Dragon",
				"damage_type": "lightning",
				"description": "Blue dragon ancestry. Associated damage type: Lightning.",
			},
			{
				"slug":        "brass",
				"name":        "Brass Dragon",
				"damage_type": "fire",
				"description": "Brass dragon ancestry. Associated damage type: Fire.",
			},
			{
				"slug":        "bronze",
				"name":        "Bronze Dragon",
				"damage_type": "lightning",
				"description": "Bronze dragon ancestry. Associated damage type: Lightning.",
			},
			{
				"slug":        "copper",
				"name":        "Copper Dragon",
				"damage_type": "acid",
				"description": "Copper dragon ancestry. Associated damage type: Acid.",
			},
			{
				"slug":        "gold",
				"name":        "Gold Dragon",
				"damage_type": "fire",
				"description": "Gold dragon ancestry. Associated damage type: Fire.",
			},
			{
				"slug":        "green",
				"name":        "Green Dragon",
				"damage_type": "poison",
				"description": "Green dragon ancestry. Associated damage type: Poison.",
			},
			{
				"slug":        "red",
				"name":        "Red Dragon",
				"damage_type": "fire",
				"description": "Red dragon ancestry. Associated damage type: Fire.",
			},
			{
				"slug":        "silver",
				"name":        "Silver Dragon",
				"damage_type": "cold",
				"description": "Silver dragon ancestry. Associated damage type: Cold.",
			},
			{
				"slug":        "white",
				"name":        "White Dragon",
				"damage_type": "cold",
				"description": "White dragon ancestry. Associated damage type: Cold.",
			},
		}
	}
	return []map[string]interface{}{}
}

// handleCharacterMulticlass godoc
// @Summary Multiclass a character into a new class
// @Description Take a level in a new class (multiclassing) or existing class when leveling up.
// @Description Requires meeting ability score prerequisites for both current and new class.
// @Description multiclassing rules.
// @Tags Characters
// @Accept json
// @Produce json
// @Param character_id body int true "Character ID"
// @Param target_class body string true "Class to take a level in"
// @Success 200 {object} map[string]interface{} "Multiclass success with new class levels"
// @Failure 400 {object} map[string]interface{} "Prerequisites not met or invalid request"
// @Router /characters/multiclass [post]
func handleCharacterMulticlass(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// Return multiclass prerequisites info
		prereqInfo := map[string]interface{}{}
		for class, prereqs := range multiclassPrereqs {
			info := map[string]interface{}{}
			if prereqs.STR > 0 {
				info["str"] = prereqs.STR
			}
			if prereqs.DEX > 0 {
				info["dex"] = prereqs.DEX
			}
			if prereqs.INT > 0 {
				info["int"] = prereqs.INT
			}
			if prereqs.WIS > 0 {
				info["wis"] = prereqs.WIS
			}
			if prereqs.CHA > 0 {
				info["cha"] = prereqs.CHA
			}
			if prereqs.OrLogic {
				info["logic"] = "OR (meet any one)"
			} else if len(info) > 1 {
				info["logic"] = "AND (meet all)"
			}
			prereqInfo[class] = info
		}

		profInfo := map[string]interface{}{}
		for class := range multiclassPrereqs {
			profs := multiclassProfsFor(class)
			info := map[string]interface{}{}
			if len(profs.ArmorProf) > 0 {
				info["armor"] = profs.ArmorProf
			}
			if len(profs.WeaponProf) > 0 {
				info["weapons"] = profs.WeaponProf
			}
			if len(profs.ToolProf) > 0 {
				info["tools"] = profs.ToolProf
			}
			if profs.Skills > 0 {
				info["skill_choices"] = profs.Skills
			}
			profInfo[class] = info
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"description":                      "Multiclassing allows taking levels in multiple classes",
			"server_default_enabled":           multiclassServerDefault(), // AGENTRPG_MULTICLASS; a campaign's multiclassing table rule overrides it
			"prerequisites":                    prereqInfo,
			"proficiencies_when_multiclassing": profInfo,
			"rules": map[string]interface{}{
				"prerequisites": "Must meet ability score requirements for BOTH current class and new class",
				"proficiencies": "When multiclassing INTO a class, gain limited proficiencies (not full)",
				"spell_slots":   "Multiclass spellcasters combine levels for spell slots (full casters count fully, half casters at half level)",
				"hit_points":    "Gain hit die for new class + CON mod (not max like level 1)",
			},
			"usage": "POST with character_id and target_class to take a level in that class",
		})
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Auth
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized", "message": publicError(err)})
		return
	}

	var req struct {
		CharacterID int    `json:"character_id"`
		TargetClass string `json:"target_class"` // Class to take a level in
		Skill       string `json:"skill"`        // the skill a first Bard, Ranger or Rogue level lets a multiclass character choose
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_json",
			"message": publicError(err),
		})
		return
	}

	targetClass := strings.ToLower(strings.TrimSpace(req.TargetClass))
	if targetClass == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "missing_target_class",
			"message": "target_class is required",
		})
		return
	}

	// Validate target class exists
	if _, ok := srdClasses[targetClass]; !ok {
		validClasses := []string{}
		for c := range srdClasses {
			validClasses = append(validClasses, c)
		}
		sort.Strings(validClasses)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "invalid_class",
			"message":       fmt.Sprintf("Unknown class: %s", targetClass),
			"valid_classes": validClasses,
		})
		return
	}

	// Get character info
	var ownerID int
	var charName, currentClass string
	var level, str, dex, con, intl, wis, cha, hp, maxHP int
	var classLevelsJSON []byte
	var pendingASI int
	var armorProfsStr, weaponProfsStr string

	err = db.QueryRow(`
		SELECT agent_id, name, class, level, str, dex, con, intl, wis, cha, hp, max_hp,
		       COALESCE(class_levels, '{}'), COALESCE(pending_asi, 0),
		       COALESCE(armor_proficiencies, ''), COALESCE(weapon_proficiencies, '')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &charName, &currentClass, &level, &str, &dex, &con, &intl, &wis, &cha,
		&hp, &maxHP, &classLevelsJSON, &pendingASI, &armorProfsStr, &weaponProfsStr)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Character %d not found", req.CharacterID),
		})
		return
	}

	if ownerID != agentID {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_your_character",
			"message": "You can only multiclass your own characters",
		})
		return
	}

	// Parse existing class levels
	var classLevels map[string]int
	json.Unmarshal(classLevelsJSON, &classLevels)
	if classLevels == nil {
		classLevels = make(map[string]int)
	}

	// If class_levels is empty, initialize with current class
	if len(classLevels) == 0 {
		classLevels[strings.ToLower(currentClass)] = level
	}

	// Calculate current total level from class_levels; levels set another way (the GM, an XP award) belong to the main class
	totalLevel := 0
	for _, lvl := range classLevels {
		totalLevel += lvl
	}
	if totalLevel < level {
		classLevels[strings.ToLower(currentClass)] += level - totalLevel
		totalLevel = level
	}

	// Check if this is taking first level in a new class (multiclassing)
	isNewClass := classLevels[targetClass] == 0 && targetClass != strings.ToLower(currentClass)
	if isNewClass {
		var mcLobbyID int
		db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&mcLobbyID)
		if !multiclassAllowed(mcLobbyID) {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "multiclassing_off", "message": multiclassOffMessage()})
			return
		}
	}

	// Check prerequisites for multiclassing
	// "To qualify for a new class, you must meet the ability score prerequisites
	// for both your current class and your new one"
	if isNewClass {
		// Check prerequisites for LEAVING current class
		canLeave, leaveReason := meetsMulticlassPrereqs(currentClass, str, dex, intl, wis, cha)
		if !canLeave {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":         "prerequisites_not_met",
				"message":       fmt.Sprintf("Cannot multiclass out of %s: %s", currentClass, leaveReason),
				"current_class": currentClass,
				"failed_prereq": leaveReason,
			})
			return
		}

		// Check prerequisites for ENTERING new class
		canEnter, enterReason := meetsMulticlassPrereqs(targetClass, str, dex, intl, wis, cha)
		if !canEnter {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":         "prerequisites_not_met",
				"message":       fmt.Sprintf("Cannot multiclass into %s: %s", targetClass, enterReason),
				"target_class":  targetClass,
				"failed_prereq": enterReason,
			})
			return
		}
	}

	// Check for pending level-up (need XP or pending ASI to indicate level available)
	// For now, we'll check if they have enough XP for next level
	var currentXP int
	db.QueryRow("SELECT COALESCE(xp, 0) FROM characters WHERE id = $1", req.CharacterID).Scan(&currentXP)
	xpForNextLevel := getXPForNextLevel(totalLevel)

	if currentXP < xpForNextLevel && totalLevel > 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":             "no_level_available",
			"message":           fmt.Sprintf("Not enough XP to level up. Need %d XP, have %d", xpForNextLevel, currentXP),
			"current_xp":        currentXP,
			"xp_for_next_level": xpForNextLevel,
			"current_level":     totalLevel,
		})
		return
	}

	// Take the level!
	oldClassLevels := make(map[string]int)
	for k, v := range classLevels {
		oldClassLevels[k] = v
	}

	classLevels[targetClass]++
	newTotalLevel := totalLevel + 1

	// Calculate HP gain (hit die roll average + CON mod, not max like level 1)
	targetClassInfo := srdClasses[targetClass]
	hitDie := targetClassInfo.HitDie
	hpGain := (hitDie / 2) + 1 + game.Modifier(con) // Average roll + 1 (the fixed value of the rules) + CON mod
	if hpGain < 1 {
		hpGain = 1 // Minimum 1 HP per level
	}
	hpGain += speciesHPPerLevel(req.CharacterID) // Dwarven Toughness
	if hasSpecificFeat(req.CharacterID, "tough") {
		hpGain += 2 // Tough: +2 Hit Points whenever you gain a level
	}
	if s := subclassesByClass(req.CharacterID)[targetClass]; s != "" { // Draconic Resilience: +1 HP per level in that class
		if b, ok := getSubclassMechanic(s, classLevels[targetClass], "bonus_hp_per_level"); ok {
			if n, err := strconv.Atoi(b); err == nil {
				hpGain += n
			}
		}
	}
	newMaxHP := maxHP + hpGain
	newHP := hp + hpGain

	// ASI earned at the CLASS's levels (4, 8, 12, 16; Fighter 6 and 14, Rogue 10; the Epic Boon at 19): SRD 5.2.1, "When you gain a new
	// level in a class, you get its features for that level". This used the total level, so a Sorcerer 11 reaching total level 19 got
	// an Epic Boon's points.
	asiLevels := asiLevelsFor(targetClass)
	asiEarned := 0
	newClassLevel := classLevels[targetClass]
	for _, asiLevel := range asiLevels {
		if newClassLevel == asiLevel {
			asiEarned += 2
		}
	}

	// Grant proficiencies if taking first level in new class
	newProfsMessage := ""
	if isNewClass {
		profs := multiclassProfsFor(targetClass)

		// Add armor proficiencies
		if len(profs.ArmorProf) > 0 {
			existingArmor := strings.Split(armorProfsStr, ", ")
			for _, prof := range profs.ArmorProf {
				found := false
				for _, existing := range existingArmor {
					if strings.ToLower(existing) == strings.ToLower(prof) {
						found = true
						break
					}
				}
				if !found && prof != "" {
					if armorProfsStr != "" {
						armorProfsStr += ", "
					}
					armorProfsStr += strings.ToLower(prof)
				}
			}
			newProfsMessage += fmt.Sprintf("Armor: %v ", profs.ArmorProf)
		}

		// Add weapon proficiencies
		if len(profs.WeaponProf) > 0 {
			existingWeapons := strings.Split(weaponProfsStr, ", ")
			for _, prof := range profs.WeaponProf {
				found := false
				for _, existing := range existingWeapons {
					if strings.ToLower(existing) == strings.ToLower(prof) {
						found = true
						break
					}
				}
				if !found && prof != "" {
					if weaponProfsStr != "" {
						weaponProfsStr += ", "
					}
					weaponProfsStr += strings.ToLower(prof)
				}
			}
			newProfsMessage += fmt.Sprintf("Weapons: %v ", profs.WeaponProf)
		}

		// Note about tools and skills (would need additional handling)
		if len(profs.ToolProf) > 0 {
			newProfsMessage += fmt.Sprintf("Tools: %v ", profs.ToolProf)
		}
		if profs.Skills > 0 { // Bard (any skill), Ranger and Rogue (their lists): one skill proficiency
			if why := grantMulticlassSkill(req.CharacterID, targetClass, req.Skill); req.Skill != "" && why == "" {
				newProfsMessage += fmt.Sprintf("Skill: %s ", req.Skill)
			} else {
				addOwedMulticlassSkill(req.CharacterID, targetClass)
				if why != "" && req.Skill != "" {
					newProfsMessage += why + " "
				}
				newProfsMessage += fmt.Sprintf("(choose 1 skill proficiency: POST /api/characters/%d/level-up {\"multiclass_skill\": \"...\"}) ", req.CharacterID)
			}
		}
	}

	// Save changes
	classLevelsJSON, _ = json.Marshal(classLevels)

	_, err = db.Exec(`
		UPDATE characters 
		SET level = $1, class_levels = $2, hp = $3, max_hp = $4, 
		    pending_asi = pending_asi + $5,
		    armor_proficiencies = $6, weapon_proficiencies = $7
		WHERE id = $8
	`, newTotalLevel, classLevelsJSON, newHP, newMaxHP, asiEarned, armorProfsStr, weaponProfsStr, req.CharacterID)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "database_error",
			"message": publicError(err),
		})
		return
	}

	grantSpellChangesOnLevelUp(req.CharacterID, targetClass, 1) // 2024 spell preparation
	// Public note of the new features and the choices to make (level_up_2024.go)
	newClassLevel = classLevels[targetClass]
	sub := subclassesByClass(req.CharacterID)[targetClass] // each class's own subclass (multiclass)
	mcFeatures := classFeaturesGained(targetClass, sub, newClassLevel-1, newClassLevel)
	mcChoices := levelUpChoices(req.CharacterID, targetClass, sub, newClassLevel-1, newClassLevel, mcFeatures)
	levelNote := fmt.Sprintf("🔔 Ding! %s reached character level %d (%s level %d): +%d Hit Points.", charName, newTotalLevel, srdClasses[targetClass].Name, newClassLevel, hpGain)
	if len(mcFeatures) > 0 {
		levelNote += " New features: " + strings.Join(mcFeatures, "; ") + "."
	}
	if len(mcChoices) > 0 {
		levelNote += " Choices to make: " + strings.Join(mcChoices, "; ") + "."
	}
	var mcLobby int
	db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&mcLobby)
	if mcLobby > 0 {
		recordGMAction(db, mcLobby, "level_up", levelNote, levelNote)
	}

	// Build response
	response := map[string]interface{}{
		"success":          true,
		"level_up_note":    levelNote,
		"character_id":     req.CharacterID,
		"character_name":   charName,
		"class_levels":     classLevels,
		"old_class_levels": oldClassLevels,
		"total_level":      newTotalLevel,
		"hp_gained":        hpGain,
		"new_hp":           newHP,
		"new_max_hp":       newMaxHP,
	}

	if isNewClass {
		response["multiclassed_into"] = targetClass
		response["message"] = fmt.Sprintf("%s took their first level in %s! (Now %s %d)",
			charName, srdClasses[targetClass].Name, formatClassLevels(classLevels), newTotalLevel)
		if newProfsMessage != "" {
			response["new_proficiencies"] = newProfsMessage
		}
	} else {
		response["leveled_up_in"] = targetClass
		response["message"] = fmt.Sprintf("%s gained a level in %s! (Now %s %d)",
			charName, srdClasses[targetClass].Name, formatClassLevels(classLevels), newTotalLevel)
	}

	if asiEarned > 0 {
		response["asi_earned"] = asiEarned
		response["asi_message"] = fmt.Sprintf("You earned %d ability score improvement points! Use POST /api/characters/{id}/asi to apply them.", asiEarned)
	}

	// Calculate new spell slots if multiclassing spellcasters
	newSpellSlots := game.MulticlassSpellSlots(classLevels)
	if len(newSpellSlots) > 0 {
		response["spell_slots"] = newSpellSlots
	}

	if added := grantSpeciesLevelSpells(req.CharacterID, newTotalLevel); len(added) > 0 {
		response["species_spells_gained"] = added
	}

	json.NewEncoder(w).Encode(response)
}

// formatClassLevels formats class levels map as "Fighter 3/Wizard 2" string
func formatClassLevels(classLevels map[string]int) string {
	if len(classLevels) == 0 {
		return ""
	}
	if len(classLevels) == 1 {
		for class, level := range classLevels {
			if info, ok := srdClasses[class]; ok {
				return fmt.Sprintf("%s %d", info.Name, level)
			}
			return fmt.Sprintf("%s %d", titleCase(class), level)
		}
	}

	// Sort by level descending, then alphabetically
	type classLevel struct {
		class string
		level int
	}
	sorted := []classLevel{}
	for c, l := range classLevels {
		sorted = append(sorted, classLevel{c, l})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].level != sorted[j].level {
			return sorted[i].level > sorted[j].level
		}
		return sorted[i].class < sorted[j].class
	})

	parts := []string{}
	for _, cl := range sorted {
		if info, ok := srdClasses[cl.class]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", info.Name, cl.level))
		} else {
			parts = append(parts, fmt.Sprintf("%s %d", titleCase(cl.class), cl.level))
		}
	}
	return strings.Join(parts, "/")
}
