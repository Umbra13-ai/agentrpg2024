package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// handleCharacterSpells godoc
// @Summary Manage character's known spells
// @Description GET: View known spells. PUT: Update known spells list. Spell slugs are validated against SRD.
// @Tags Characters
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{spells=[]string} false "Spell slugs to learn (PUT only)"
// @Success 200 {object} map[string]interface{} "Known spells list"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not your character"
// @Router /characters/{id}/spells [get]
// @Router /characters/{id}/spells [put]
func handleCharacterSpells(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Verify ownership
	var ownerID int
	var knownSpellsJSON []byte
	var magicalSecretsJSON []byte
	var class string
	var subclass sql.NullString
	var level int
	err = db.QueryRow(`
		SELECT agent_id, COALESCE(known_spells, '[]'), COALESCE(magical_secrets, '[]'), class, subclass, level
		FROM characters WHERE id = $1
	`, charID).Scan(&ownerID, &knownSpellsJSON, &magicalSecretsJSON, &class, &subclass, &level)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	var knownSpells []string
	json.Unmarshal(knownSpellsJSON, &knownSpells)

	var magicalSecrets []string
	json.Unmarshal(magicalSecretsJSON, &magicalSecrets)

	// Calculate Magical Secrets slots for Bards
	subclassStr := ""
	if subclass.Valid {
		subclassStr = subclass.String
	}
	magicalSecretsSlots := getMagicalSecretsSlots(class, subclassStr, level)
	magicalSecretsUsed := len(magicalSecrets)
	magicalSecretsAvailable := magicalSecretsSlots - magicalSecretsUsed
	if magicalSecretsAvailable < 0 {
		magicalSecretsAvailable = 0
	}

	if r.Method == "GET" {
		// Return current known spells with enriched info
		spellsInfo := []map[string]interface{}{}
		for _, slug := range knownSpells {
			if spell, ok := srdSpellsMemory[slug]; ok {
				spellsInfo = append(spellsInfo, map[string]interface{}{
					"slug":         slug,
					"name":         spell.Name,
					"level":        spell.Level,
					"school":       spell.School,
					"casting_time": spell.CastingTime,
					"components":   spell.Components,
					"is_ritual":    spell.IsRitual,
				})
			} else {
				spellsInfo = append(spellsInfo, map[string]interface{}{
					"slug": slug,
					"name": slug,
				})
			}
		}
		response := map[string]interface{}{
			"character_id": charID,
			"class":        class,
			"level":        level,
			"known_spells": spellsInfo,
			"count":        len(spellsInfo),
		}

		// Add Magical Secrets info for Bards
		if magicalSecretsSlots > 0 {
			// Enrich magical secrets spells with info
			magicalSecretsInfo := []map[string]interface{}{}
			for _, slug := range magicalSecrets {
				if spell, ok := srdSpellsMemory[slug]; ok {
					magicalSecretsInfo = append(magicalSecretsInfo, map[string]interface{}{
						"slug":  slug,
						"name":  spell.Name,
						"level": spell.Level,
					})
				} else {
					magicalSecretsInfo = append(magicalSecretsInfo, map[string]interface{}{
						"slug": slug,
						"name": slug,
					})
				}
			}
			response["magical_secrets"] = magicalSecretsInfo
			response["magical_secrets_slots"] = magicalSecretsSlots
			response["magical_secrets_used"] = magicalSecretsUsed
			response["magical_secrets_available"] = magicalSecretsAvailable
			if magicalSecretsAvailable > 0 {
				response["magical_secrets_tip"] = fmt.Sprintf("You can learn %d more spell(s) from ANY class via Magical Secrets. Use add=[\"spell-slug\"] to add spells not on the bard list.", magicalSecretsAvailable)
			}
		}
		if es := evocationSavantInfo(charID); es != nil {
			response["evocation_savant"] = es // Evoker 3: free Evocation spells for the spellbook
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == "PUT" {
		var req struct {
			Spells []string `json:"spells"` // Spell slugs to set
			Add    []string `json:"add"`    // Spells to add to existing list
			Remove []string `json:"remove"` // Spells to remove from existing list
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
			return
		}

		var newSpells []string
		var newMagicalSecrets []string
		magicalSecretsRemaining := magicalSecretsAvailable

		// Helper to validate and categorize a spell
		validateSpell := func(spellSlug string) (validSlug string, isMagicalSecret bool, errResp map[string]interface{}) {
			slugLower := strings.ToLower(strings.TrimSpace(spellSlug))

			// Find the spell in SRD
			if _, ok := srdSpellsMemory[slugLower]; ok {
				validSlug = slugLower
			} else {
				slugDashed := strings.ReplaceAll(slugLower, " ", "-")
				if _, ok := srdSpellsMemory[slugDashed]; ok {
					validSlug = slugDashed
				} else {
					return "", false, map[string]interface{}{
						"error":   "unknown_spell",
						"message": fmt.Sprintf("Spell '%s' not found in SRD. Check /api/universe/spells for valid spell slugs.", spellSlug),
					}
				}
			}

			// Check if on class spell list
			if isSpellOnClassList(validSlug, class) {
				return validSlug, false, nil
			}

			// 2024 Magical Secrets (Bard level 10): spells of the Bard, Cleric, Druid and Wizard lists count as Bard spells
			if strings.ToLower(class) == "bard" && level >= 10 {
				for _, other := range []string{"cleric", "druid", "wizard"} {
					if isSpellOnClassList(validSlug, other) {
						return validSlug, false, nil
					}
				}
			}

			// Not on class list - check Magical Secrets
			if magicalSecretsSlots > 0 {
				// Check if this spell is already in magical secrets
				for _, ms := range magicalSecrets {
					if ms == validSlug {
						return validSlug, true, nil // Already a magical secret
					}
				}
				// Can we add a new magical secret?
				if magicalSecretsRemaining > 0 {
					magicalSecretsRemaining--
					return validSlug, true, nil
				}
				return "", false, map[string]interface{}{
					"error":   "magical_secrets_full",
					"message": fmt.Sprintf("'%s' is not on the %s spell list. You have used all %d Magical Secrets slots.", srdSpellsMemory[validSlug].Name, class, magicalSecretsSlots),
				}
			}

			return "", false, map[string]interface{}{
				"error":   "not_on_class_list",
				"message": fmt.Sprintf("'%s' is not on the %s spell list. Check /api/universe/class-spells/%s for available spells.", srdSpellsMemory[validSlug].Name, class, strings.ToLower(class)),
			}
		}

		if len(req.Spells) > 0 {
			// Replace entire spell list
			newSpells = []string{}
			newMagicalSecrets = []string{}
			magicalSecretsRemaining = magicalSecretsSlots // Reset for full replacement

			for _, spellSlug := range req.Spells {
				validSlug, isMagicalSecret, errResp := validateSpell(spellSlug)
				if errResp != nil {
					json.NewEncoder(w).Encode(errResp)
					return
				}
				newSpells = append(newSpells, validSlug)
				if isMagicalSecret {
					newMagicalSecrets = append(newMagicalSecrets, validSlug)
				}
			}
		} else {
			// Incremental add/remove
			newSpells = append([]string{}, knownSpells...)            // Copy existing
			newMagicalSecrets = append([]string{}, magicalSecrets...) // Copy existing magical secrets

			// Add new spells
			for _, spellSlug := range req.Add {
				validSlug, isMagicalSecret, errResp := validateSpell(spellSlug)
				if errResp != nil {
					json.NewEncoder(w).Encode(errResp)
					return
				}
				// Check if already known
				alreadyKnown := false
				for _, known := range newSpells {
					if known == validSlug {
						alreadyKnown = true
						break
					}
				}
				if !alreadyKnown {
					newSpells = append(newSpells, validSlug)
					if isMagicalSecret {
						// Check if not already tracked as magical secret
						alreadyMS := false
						for _, ms := range newMagicalSecrets {
							if ms == validSlug {
								alreadyMS = true
								break
							}
						}
						if !alreadyMS {
							newMagicalSecrets = append(newMagicalSecrets, validSlug)
						}
					}
				}
			}

			// Remove spells
			for _, spellSlug := range req.Remove {
				slugLower := strings.ToLower(strings.TrimSpace(spellSlug))
				slugDashed := strings.ReplaceAll(slugLower, " ", "-")
				filtered := []string{}
				for _, known := range newSpells {
					if known != slugLower && known != slugDashed {
						filtered = append(filtered, known)
					}
				}
				newSpells = filtered

				// Also remove from magical secrets if present
				filteredMS := []string{}
				for _, ms := range newMagicalSecrets {
					if ms != slugLower && ms != slugDashed {
						filteredMS = append(filteredMS, ms)
					}
				}
				newMagicalSecrets = filteredMS
			}
		}

		// 2024: the Prepared Spells and Cantrips columns, and when the list may change (prepared_spells_2024.go); always-prepared spells
		// (subclass spells) don't count; a Wizard's list here is the spellbook
		var commitChange func()
		if strings.ToLower(class) != "wizard" {
			var choicesRaw []byte
			db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&choicesRaw)
			choices := map[string]string{}
			json.Unmarshal(choicesRaw, &choices)
			always := append(getDomainSpells(subclassStr, level, choices["circle_land"]), originAlwaysPrepared(charID)...) // subclass and origin spells
			// Words of Creation (Bard 20): Power Word Heal and Kill
			always = append(always, wordsOfCreationSpells(class, level)...)
			why, commit := checkSpellListChange(charID, class, level, withoutAlwaysPrepared(knownSpells, always), withoutAlwaysPrepared(newSpells, always))
			if why != "" {
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "cannot_change_now", "message": why, "max": game.PreparedSpells2024(class, level)})
				return
			}
			commitChange = commit
		}

		// Save to database (also save magical_secrets)
		newSpellsJSON, _ := json.Marshal(newSpells)
		newMagicalSecretsJSON, _ := json.Marshal(newMagicalSecrets)
		_, err = db.Exec(`UPDATE characters SET known_spells = $1, magical_secrets = $2 WHERE id = $3`, newSpellsJSON, newMagicalSecretsJSON, charID)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error"})
			return
		}
		if commitChange != nil {
			commitChange()
		}
		if listInKnownSpells[strings.ToLower(class)] {
			db.Exec(`UPDATE characters SET prepared_spells = $1 WHERE id = $2`, newSpellsJSON, charID) // the prepared list, mirrored
		}

		// Return updated spell list
		spellsInfo := []map[string]interface{}{}
		for _, slug := range newSpells {
			if spell, ok := srdSpellsMemory[slug]; ok {
				spellsInfo = append(spellsInfo, map[string]interface{}{
					"slug":   slug,
					"name":   spell.Name,
					"level":  spell.Level,
					"school": spell.School,
				})
			}
		}

		response := map[string]interface{}{
			"success":      true,
			"known_spells": spellsInfo,
			"count":        len(spellsInfo),
			"message":      fmt.Sprintf("Updated your spells: %d spells and cantrips.", len(spellsInfo)),
		}

		// Include magical secrets info for Bards
		if magicalSecretsSlots > 0 {
			magicalSecretsInfo := []map[string]interface{}{}
			for _, slug := range newMagicalSecrets {
				if spell, ok := srdSpellsMemory[slug]; ok {
					magicalSecretsInfo = append(magicalSecretsInfo, map[string]interface{}{
						"slug":  slug,
						"name":  spell.Name,
						"level": spell.Level,
					})
				}
			}
			response["magical_secrets"] = magicalSecretsInfo
			response["magical_secrets_slots"] = magicalSecretsSlots
			response["magical_secrets_used"] = len(newMagicalSecrets)
			response["magical_secrets_available"] = magicalSecretsSlots - len(newMagicalSecrets)
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	http.Error(w, "Method not allowed. Use GET or PUT.", http.StatusMethodNotAllowed)
}

// handlePrepareSpells godoc
// @Summary Prepare spells (every spellcaster, 2024)
// @Description Every caster prepares as many level 1+ spells as the Prepared Spells column of its class table, plus its cantrips; always-prepared subclass spells don't count. When the list may change: Cleric, Druid, Wizard (from the spellbook) after a Long Rest; Paladin, Ranger one spell after a Long Rest; Bard, Sorcerer, Warlock one spell when they gain a level; one cantrip on gaining a level. Filling empty places is always allowed; the GM can allow more changes (set spell_swaps, cantrip_swaps, prepare_ready).
// @Description GET: View currently prepared spells and preparation limits.
// @Description POST: Set the prepared list. Validates against the Prepared Spells and Cantrips columns.
// @Description Domain/subclass spells are always prepared and don't count against the limit.
// @Tags Characters
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param Authorization header string true "Basic auth"
// @Param request body object{spells=[]string} false "Spell slugs to prepare (POST only)"
// @Success 200 {object} map[string]interface{} "Prepared spells info"
// @Failure 400 {object} map[string]interface{} "Not a prepared caster or exceeds limit"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not your character"
// @Router /characters/{id}/prepare [get]
// @Router /characters/{id}/prepare [post]
func handlePrepareSpells(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Get character info
	var ownerID int
	var preparedSpellsJSON []byte
	var className string
	var subclassRaw sql.NullString
	var level, intl, wis, cha int
	var subclassChoicesJSON []byte // For Land druid circle spells
	err = db.QueryRow(`
		SELECT agent_id, COALESCE(prepared_spells, '[]'), class, subclass, level, intl, wis, cha,
			COALESCE(subclass_choices, '{}')
		FROM characters WHERE id = $1
	`, charID).Scan(&ownerID, &preparedSpellsJSON, &className, &subclassRaw, &level, &intl, &wis, &cha,
		&subclassChoicesJSON)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	subclassSlug := ""
	if subclassRaw.Valid {
		subclassSlug = subclassRaw.String
	}

	// Multiclass (SRD 5.2.1): spells are prepared per class, as if single-classed. ?class=druid picks the class; without it, a character
	// whose first class doesn't cast (a Monk / Warlock) prepares for its casting class with the most levels.
	mcClassList := []string(nil)
	if levels := classLevelsOf(charID); len(levels) > 1 {
		pick := strings.ToLower(r.URL.Query().Get("class"))
		if pick == "" && !game.IsPreparedCaster(className) {
			best := 0
			for c, l := range levels {
				if game.IsPreparedCaster(c) && l > best {
					pick, best = c, l
				}
			}
		}
		if pick != "" && levels[pick] > 0 {
			className, level = pick, levels[pick]
			mcClassList = getClassSpellList(pick)
		}
	}

	// Check if this class is a prepared caster
	if !game.IsPreparedCaster(className) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_spellcaster",
			"message": fmt.Sprintf("%ss are not spellcasters.", className),
		})
		return
	}

	// 2024: every caster prepares spells (prepared_spells_2024.go); a Bard's, Ranger's, Sorcerer's or Warlock's list lives in known_spells
	preparedSpells := currentPreparedList(charID, className)
	_ = preparedSpellsJSON
	otherClassSpells := []string(nil) // multiclass: the other classes' prepared spells, kept as they are
	if mcClassList != nil {
		var mine []string
		all := append(append([]string{}, preparedSpells...), jsonListOf(charID, "prepared_spells")...)
		for _, s := range all {
			if containsStr(mcClassList, s) {
				if !containsStr(mine, s) {
					mine = append(mine, s)
				}
			} else if !containsStr(otherClassSpells, s) {
				otherClassSpells = append(otherClassSpells, s)
			}
		}
		preparedSpells = mine
	}

	// Calculate limits
	maxPrepared := game.MaxPreparedSpells(className, level, intl, wis, cha)
	spellAbility := ""
	switch strings.ToLower(className) {
	case "wizard":
		spellAbility = "INT"
	case "cleric", "druid":
		spellAbility = "WIS"
	case "paladin", "bard", "sorcerer", "warlock":
		spellAbility = "CHA"
	case "ranger":
		spellAbility = "WIS"
	}

	// Parse subclass choices to get circle_land for Land druids
	var subclassChoices map[string]string
	json.Unmarshal(subclassChoicesJSON, &subclassChoices)
	landType := subclassChoices["circle_land"]

	// Get domain/subclass spells (always prepared)
	domainSpells := getDomainSpells(subclassSlug, level, landType)
	originSpells := originAlwaysPrepared(charID) // species lineage and Magic Initiate spells: always prepared too
	alwaysPrepared := append(append([]string{}, domainSpells...), originSpells...)
	alwaysPrepared = append(alwaysPrepared, wordsOfCreationSpells(charClassOf(charID), level)...) // Bard 20: Words of Creation

	if r.Method == "GET" {
		// Return current prepared spells with enriched info
		preparedInfo := []map[string]interface{}{}
		for _, slug := range preparedSpells {
			if spell, ok := srdSpellsMemory[slug]; ok {
				preparedInfo = append(preparedInfo, map[string]interface{}{
					"slug":         slug,
					"name":         spell.Name,
					"level":        spell.Level,
					"school":       spell.School,
					"casting_time": spell.CastingTime,
					"components":   spell.Components,
					"is_ritual":    spell.IsRitual,
				})
			} else {
				preparedInfo = append(preparedInfo, map[string]interface{}{
					"slug": slug,
					"name": slug,
				})
			}
		}

		// Enriched domain spells
		domainSpellsInfo := getDomainSpellsWithInfo(subclassSlug, level, landType)

		response := map[string]interface{}{
			"character_id":         charID,
			"class":                className,
			"level":                level,
			"caster_type":          "prepared",
			"spellcasting_ability": spellAbility,
			"prepared_spells":      preparedInfo,
			"prepared_count":       countLeveled(withoutAlwaysPrepared(preparedSpells, alwaysPrepared)),
			"max_prepared":         maxPrepared,
			"slots_remaining":      maxPrepared - countLeveled(withoutAlwaysPrepared(preparedSpells, alwaysPrepared)),
		}
		if info := originSpellsInfo(charID); len(info) > 0 {
			response["origin_spells"] = info
			response["origin_spells_note"] = "Species and Magic Initiate spells: always prepared, don't count against your limit, cast with their own spellcasting ability; the free casts come back on a Long Rest (say \"free\" in the cast)."
		}

		if len(domainSpellsInfo) > 0 {
			response["domain_spells"] = domainSpellsInfo
			response["domain_spells_note"] = "Always prepared, don't count against your limit"
		}

		response["tip"] = fmt.Sprintf("POST to this endpoint with {\"spells\": [...]} to change prepared spells. You can prepare up to %d spells.", maxPrepared)

		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == "POST" {
		var req struct {
			Spells []string `json:"spells"` // Spell slugs to prepare
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
			return
		}

		// Validate each spell slug and build the prepared list
		newPrepared := []string{}
		for _, spellSlug := range req.Spells {
			slugLower := strings.ToLower(strings.TrimSpace(spellSlug))
			slugDashed := strings.ReplaceAll(slugLower, " ", "-")

			// Check SRD
			validSlug := ""
			if _, ok := srdSpellsMemory[slugLower]; ok {
				validSlug = slugLower
			} else if _, ok := srdSpellsMemory[slugDashed]; ok {
				validSlug = slugDashed
			}

			if validSlug == "" {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "unknown_spell",
					"message": fmt.Sprintf("Spell '%s' not found in SRD. Check /api/universe/spells for valid spell slugs.", spellSlug),
				})
				return
			}
			if containsStr(alwaysPrepared, validSlug) { // F240: a Magic Initiate, lineage or subclass spell sent back with the list: always prepared, skip it
				continue
			}

			// A Wizard prepares spells from the spellbook
			if strings.ToLower(className) == "wizard" && srdSpellsMemory[validSlug].Level > 0 && !inSpellbook(charID, validSlug) { // cantrips are known, not in the book (F202)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "not_in_spellbook",
					"message": fmt.Sprintf("'%s' is not in your spellbook. A Wizard prepares spells from the spellbook.", srdSpellsMemory[validSlug].Name),
				})
				return
			}

			// Check if spell is on the class spell list (a Bard of level 10 also prepares from the Cleric, Druid and Wizard lists: Magical Secrets)
			classSpellList := getClassSpellList(className)
			if strings.ToLower(className) == "bard" && level >= 10 {
				for _, other := range []string{"cleric", "druid", "wizard"} {
					classSpellList = append(classSpellList, getClassSpellList(other)...)
				}
			}
			if classSpellList != nil && len(classSpellList) > 0 {
				isOnList := false
				for _, cs := range classSpellList {
					if cs == validSlug {
						isOnList = true
						break
					}
				}
				if !isOnList {
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":   "not_on_class_list",
						"message": fmt.Sprintf("'%s' is not on the %s spell list. Check /api/universe/class-spells/%s for available spells.", srdSpellsMemory[validSlug].Name, className, strings.ToLower(className)),
					})
					return
				}
			}

			// Check spell level isn't too high for this character's slots
			spell := srdSpellsMemory[validSlug]
			slots := characterSpellSlots(charID, className, level)
			if spell.Level > 0 {
				// A spell can be prepared up to the highest slot level the character has. Pact Magic has slots of one level only
				// (all level 5 for a level 9+ Warlock), and a Warlock prepares spells of that level or lower (SRD 5.2.1).
				maxSlot := 0
				if mcClassList != nil && strings.EqualFold(className, "warlock") {
					slots = characterPactSlots(charID) // a multiclass Warlock prepares up to its Pact Magic slot level
				}
				for lv := range slots {
					if lv > maxSlot {
						maxSlot = lv
					}
				}
				if spell.Level > maxSlot {
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":   "spell_too_high",
						"message": fmt.Sprintf("Cannot prepare %s (level %d) - you don't have level %d spell slots yet.", spell.Name, spell.Level, spell.Level),
					})
					return
				}
			}

			// Don't duplicate
			isDuplicate := false
			for _, existing := range newPrepared {
				if existing == validSlug {
					isDuplicate = true
					break
				}
			}
			// Also check if it's a domain or origin spell (auto-prepared)
			for _, ds := range alwaysPrepared {
				if ds == validSlug {
					isDuplicate = true
					break
				}
			}
			if !isDuplicate {
				newPrepared = append(newPrepared, validSlug)
			}
		}

		// F234: a list without cantrips keeps the cantrips the character knows (Bard, Ranger, Sorcerer, Warlock lost them: their list
		// is their known_spells). A list that names cantrips is the whole list, as before.
		hasCantrip := false
		for _, s := range newPrepared {
			if sp, ok := srdSpellsMemory[s]; ok && sp.Level == 0 {
				hasCantrip = true
			}
		}
		if !hasCantrip {
			for _, s := range preparedSpells {
				if sp, ok := srdSpellsMemory[s]; ok && sp.Level == 0 && !containsStr(newPrepared, s) && !containsStr(alwaysPrepared, s) { // origin cantrips are kept apart
					newPrepared = append(newPrepared, s)
				}
			}
		}
		// The 2024 limits (Prepared Spells and Cantrips columns; always-prepared spells don't count) and when the list may change
		if why, commit := checkSpellListChange(charID, className, level, withoutAlwaysPrepared(preparedSpells, alwaysPrepared), newPrepared); why != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "cannot_change_now", "message": why, "max": maxPrepared})
			return
		} else {
			defer commit()
		}

		// Save to database (Bard, Ranger, Sorcerer, Warlock: the list is also their known_spells, which keeps the origin spells)
		saved := newPrepared
		if mcClassList != nil {
			saved = append(append([]string{}, newPrepared...), otherClassSpells...)
		}
		newPreparedJSON, _ := json.Marshal(saved)
		_, err = db.Exec(`UPDATE characters SET prepared_spells = $1 WHERE id = $2`, newPreparedJSON, charID)
		if listInKnownSpells[strings.ToLower(className)] {
			kept := append([]string{}, saved...)
			for _, s := range preparedSpells {
				if containsStr(originSpells, s) && !containsStr(kept, s) {
					kept = append(kept, s)
				}
			}
			keptJSON, _ := json.Marshal(kept)
			db.Exec(`UPDATE characters SET known_spells = $1 WHERE id = $2`, keptJSON, charID)
		}
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "database_error", "detail": publicError(err)})
			return
		}

		// Return updated prepared list
		preparedInfo := []map[string]interface{}{}
		for _, slug := range newPrepared {
			if spell, ok := srdSpellsMemory[slug]; ok {
				preparedInfo = append(preparedInfo, map[string]interface{}{
					"slug":   slug,
					"name":   spell.Name,
					"level":  spell.Level,
					"school": spell.School,
				})
			}
		}

		domainSpellsInfo := getDomainSpellsWithInfo(subclassSlug, level, landType)

		response := map[string]interface{}{
			"success":         true,
			"prepared_spells": preparedInfo,
			"prepared_count":  countLeveled(newPrepared),
			"max_prepared":    maxPrepared,
			"slots_remaining": maxPrepared - countLeveled(newPrepared),
			"message":         fmt.Sprintf("Prepared %d spells for the day.", len(newPrepared)),
		}

		if len(domainSpellsInfo) > 0 {
			response["domain_spells"] = domainSpellsInfo
			response["domain_spells_note"] = "Always prepared, don't count against your limit"
			// Show land type for Land druids
			if subclassSlug == "land" && landType != "" {
				response["circle_land"] = landType
			}
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	http.Error(w, "Method not allowed. Use GET or POST.", http.StatusMethodNotAllowed)
}

// handleUseResource godoc
// @Summary Use a class resource
// @Description Spend a class resource (Focus Points, Rage, Sorcery Points, etc.)
// @Tags Characters
// @Accept json
// @Produce json
// @Param id path int true "Character ID"
// @Param request body object true "Resource to use" example({"resource": "focus", "amount": 1})
// @Success 200 {object} map[string]interface{} "Resource usage result"
// @Failure 400 {object} map[string]interface{} "Invalid request or not enough resources"
// @Security BasicAuth
// @Router /characters/{id}/use-resource [post]
func handleUseResource(w http.ResponseWriter, r *http.Request, charID int) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "Method not allowed. Use POST.", http.StatusMethodNotAllowed)
		return
	}

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Verify ownership
	var ownerID int
	err = db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, charID).Scan(&ownerID)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_your_character"})
		return
	}

	var req struct {
		Resource string `json:"resource"` // Resource key: focus, rage, sorcery_points, etc.
		Amount   int    `json:"amount"`   // Amount to spend (default 1)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.Resource == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "missing_resource",
			"message": "Specify 'resource' (focus, rage, sorcery_points, bardic_inspiration, channel_divinity, lay_on_hands, second_wind, action_surge, wild_shape, arcane_recovery)",
		})
		return
	}

	if req.Amount <= 0 {
		req.Amount = 1
	}

	success, errMsg, remaining := useClassResource(charID, req.Resource, req.Amount)

	if !success {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "resource_unavailable",
			"message": errMsg,
		})
		return
	}

	// Get class info for resource name lookup
	var class string
	db.QueryRow("SELECT class FROM characters WHERE id = $1", charID).Scan(&class)

	// Find display name for the resource
	resourceName := req.Resource
	for _, res := range characterResourceDefs(charID, class) {
		if res.Key == req.Resource {
			resourceName = res.Name
			break
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"resource":      req.Resource,
		"resource_name": resourceName,
		"spent":         req.Amount,
		"remaining":     remaining,
		"message":       fmt.Sprintf("Spent %d %s. %d remaining.", req.Amount, resourceName, remaining),
	})
}
