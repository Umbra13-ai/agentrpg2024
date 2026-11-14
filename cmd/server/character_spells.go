package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

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
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
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
			Spells *[]string `json:"spells"` // Spell slugs to set
			Add    []string  `json:"add"`    // Spells to add to existing list
			Remove []string  `json:"remove"` // Spells to remove from existing list
		}
		// every answer is saved as sent or refused with the reason (F13: a replace wiped origin spells without a word)
		if msg := decodeStrict(r, &req, "spells (the whole list), add or remove"); msg != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": msg + " Nothing was changed."})
			return
		}
		if req.Spells == nil && len(req.Add) == 0 && len(req.Remove) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_change",
				"message": "Send spells (the whole list, which replaces it), add or remove. Nothing was changed."})
			return
		}
		if req.Spells != nil && (len(req.Add) > 0 || len(req.Remove) > 0) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "spells_with_add_or_remove",
				"message": "Send either spells (the whole list) or add/remove, not both. Nothing was changed."})
			return
		}
		if req.Spells != nil && len(*req.Spells) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "empty_list",
				"message": "spells is empty. To take spells off your list, send them in remove. Nothing was changed."})
			return
		}
		origin := originSpellsOf(charID)
		maxSlotLevel := maxSpellLevelFor(charID, class, level)
		var notes []string

		var newSpells []string
		var newMagicalSecrets []string
		magicalSecretsRemaining := magicalSecretsAvailable

		// Helper to validate and categorize a spell
		var validateOnList func(spellSlug string) (validSlug string, isMagicalSecret bool, errResp map[string]interface{})
		// a spell needs a spell slot of its level (F12: a level 1 Wizard wrote Wish and Meteor Swarm into the book)
		validateSpell := func(spellSlug string) (string, bool, map[string]interface{}) {
			validSlug, isMagicalSecret, errResp := validateOnList(spellSlug)
			if errResp == nil {
				if sp := srdSpellsMemory[validSlug]; sp.Level > maxSlotLevel {
					return "", false, map[string]interface{}{
						"error":   "spell_too_high",
						"message": fmt.Sprintf("%s is a level %d spell; your highest spell slot is level %d.", sp.Name, sp.Level, maxSlotLevel),
					}
				}
			}
			return validSlug, isMagicalSecret, errResp
		}
		validateOnList = func(spellSlug string) (validSlug string, isMagicalSecret bool, errResp map[string]interface{}) {
			slugLower := strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(strings.TrimSpace(spellSlug))) // "Hunter’s Mark" is hunters-mark (Nov 14 audit re-check)

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
						"message": unknownSpellMessage(spellSlug),
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

		if req.Spells != nil {
			// Replace entire spell list
			newSpells = []string{}
			newMagicalSecrets = []string{}
			magicalSecretsRemaining = magicalSecretsSlots // Reset for full replacement

			for _, spellSlug := range *req.Spells {
				if k := normalizeSpellSlug(spellSlug); origin[k].Slug != "" {
					if !containsStr(newSpells, k) {
						newSpells = append(newSpells, k) // an origin spell sent back with the list: kept as it is
					}
					continue
				}
				validSlug, isMagicalSecret, errResp := validateSpell(spellSlug)
				if errResp != nil {
					errResp["message"] = fmt.Sprint(errResp["message"]) + " Nothing was changed."
					writeJSONStatus(w, http.StatusBadRequest, errResp)
					return
				}
				if containsStr(newSpells, validSlug) { // said, as /prepare says it (Nov 14 review: saved once with no note)
					notes = append(notes, fmt.Sprintf("%s was sent twice: it is on your list once.", spellName(validSlug)))
					continue
				}
				newSpells = append(newSpells, validSlug)
				if isMagicalSecret {
					newMagicalSecrets = append(newMagicalSecrets, validSlug)
				}
			}
			// origin spells (species, Magic Initiate) aren't part of the class list: a replace keeps them, and says what it removed
			var kept, removed []string
			for _, k := range knownSpells {
				if containsStr(newSpells, k) {
					continue
				}
				if origin[k].Slug != "" {
					newSpells = append(newSpells, k)
					kept = append(kept, spellName(k))
				} else {
					removed = append(removed, spellName(k))
				}
			}
			if len(kept) > 0 {
				notes = append(notes, fmt.Sprintf("Kept your origin spells (species, Magic Initiate), which this list doesn't change: %s.", strings.Join(kept, ", ")))
			}
			if len(removed) > 0 {
				notes = append(notes, fmt.Sprintf("Removed from your list: %s.", strings.Join(removed, ", ")))
			}
		} else {
			// Incremental add/remove
			newSpells = append([]string{}, knownSpells...)            // Copy existing
			newMagicalSecrets = append([]string{}, magicalSecrets...) // Copy existing magical secrets

			// Add new spells
			for _, spellSlug := range req.Add {
				validSlug, isMagicalSecret, errResp := validateSpell(spellSlug)
				if errResp != nil {
					writeJSONStatus(w, http.StatusBadRequest, errResp)
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
				if alreadyKnown {
					notes = append(notes, fmt.Sprintf("%s was already on your list.", spellName(validSlug)))
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
				slugLower := strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(strings.TrimSpace(spellSlug))) // "Hunter’s Mark" is hunters-mark (Nov 14 audit re-check)
				slugDashed := strings.ReplaceAll(slugLower, " ", "-")
				if o := origin[normalizeSpellSlug(spellSlug)]; o.Slug != "" {
					src := o.Source
					if src == "Magic Initiate" {
						src += " feat"
					} else if !strings.HasSuffix(src, "lineage") && !strings.HasSuffix(src, "legacy") {
						src += " lineage"
					}
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "origin_spell",
						"message": fmt.Sprintf("%s comes from your %s, not your class: it stays on your sheet and can't be removed here. Nothing was changed.", spellName(o.Slug), src)})
					return
				}
				if !containsStr(newSpells, slugLower) && !containsStr(newSpells, slugDashed) && !containsStr(newSpells, normalizeSpellSlug(spellSlug)) {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_on_your_list",
						"message": fmt.Sprintf("%q isn't on your list of spells, so it can't be removed. Nothing was changed.", spellSlug), "your_spells": knownSpells})
					return
				}
				slugLower = normalizeSpellSlug(spellSlug)
				slugDashed = slugLower
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
			// subclass and origin spells, class-feature spells, Words of Creation (Bard 20), Magical Discoveries (College of Lore 6)
			always := allAlwaysPrepared(charID)
			why, commit := checkSpellListChange(charID, class, level, withoutAlwaysPrepared(knownSpells, always), withoutAlwaysPrepared(newSpells, always))
			if why != "" {
				writeJSONStatus(w, http.StatusConflict, spellListRefusal(charID, class, level, why))
				return
			}
			commitChange = commit
		} else {
			// a Wizard's list is the spellbook (no limit), but the cantrips have the Cantrips column and its swap rule
			// (Nov 13: a fourth cantrip was accepted at level 1)
			always := allAlwaysPrepared(charID)
			// the spellbook: 6 spells at level 1 and two more each level are free; others are copied (POST /api/characters/copy-spell).
			// Origin spells and copied spells don't count (F12: any number of spells could be written into a level 1 Wizard's book).
			target := wizardSpellbookTarget(charID, level)
			if before, after := freeSpellbookCount(charID, knownSpells), freeSpellbookCount(charID, newSpells); after > target && after > before {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "spellbook_full", "spellbook_free_spells": target, "in_your_spellbook": before,
					"message": fmt.Sprintf("A level %d Wizard writes %d spells into the spellbook for free (6 at level 1, two more each level); this would make %d. Other spells are copied into the book: POST /api/characters/copy-spell {\"character_id\": %d, \"spell\": \"...\"} (50 GP and 2 hours per spell level). Nothing was changed.", level, target, after, charID)})
				return
			}
			onlyCantrips := func(list []string) []string {
				var out []string
				for _, s := range withoutAlwaysPrepared(list, always) {
					if sp, ok := srdSpellsMemory[s]; ok && sp.Level == 0 {
						out = append(out, s)
					}
				}
				return out
			}
			maxC := game.ClassTableInt("wizard", level, "Cantrips") + orderExtraCantrips(charID)
			if n := len(onlyCantrips(newSpells)); maxC > 0 && n > maxC && n > len(onlyCantrips(knownSpells)) {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "too_many_cantrips",
					"message":      fmt.Sprintf("A level %d Wizard knows %d Wizard cantrips; this list has %d (cantrips from your species or Magic Initiate don't count). To swap one, send it in remove with the new one in add.", level, maxC, n),
					"max_cantrips": maxC, "your_cantrips": onlyCantrips(knownSpells)})
				return
			}
		}

		// Save to database (also save magical_secrets)
		newSpellsJSON, _ := json.Marshal(newSpells)
		newMagicalSecretsJSON, _ := json.Marshal(newMagicalSecrets)
		_, err = db.Exec(`UPDATE characters SET known_spells = $1, magical_secrets = $2 WHERE id = $3`, newSpellsJSON, newMagicalSecretsJSON, charID)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error"})
			return
		}
		if commitChange != nil {
			commitChange()
		}
		if listInKnownSpells[strings.ToLower(class)] {
			db.Exec(`UPDATE characters SET prepared_spells = $1 WHERE id = $2`, newSpellsJSON, charID) // the prepared list, mirrored
		}
		var unprepared []string
		if strings.ToLower(class) == "wizard" {
			// a spell taken out of the spellbook can't stay prepared: it is unprepared too, and said (Nov 14 audit re-check: Sleep
			// stayed prepared, and re-sending the same prepared list was refused as "not in your spellbook")
			var raw []byte
			db.QueryRow(`SELECT COALESCE(prepared_spells, '[]'::jsonb) FROM characters WHERE id = $1`, charID).Scan(&raw)
			var prepared []string
			json.Unmarshal(raw, &prepared)
			always := allAlwaysPrepared(charID)
			var keep []string
			for _, p := range prepared {
				if containsStr(newSpells, p) || containsStr(always, p) {
					keep = append(keep, p)
				} else {
					unprepared = append(unprepared, spellName(p))
				}
			}
			if len(unprepared) > 0 {
				kb, _ := json.Marshal(keep)
				db.Exec(`UPDATE characters SET prepared_spells = $1 WHERE id = $2`, kb, charID)
			}
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
		if len(unprepared) > 0 {
			response["unprepared"] = unprepared
			response["message"] = fmt.Sprint(response["message"]) + " No longer prepared (taken out of your spellbook): " + strings.Join(unprepared, ", ") + "."
		}
		if len(notes) > 0 {
			response["notes"] = notes
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
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
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

	// Check if this class is a prepared caster. A Fighter or Rogue with Magic Initiate or species spells still has those, always
	// prepared: GET lists them (L7: it answered not_spellcaster, though skill.md says origin_spells are listed here).
	if !game.IsPreparedCaster(className) {
		info := originSpellsInfo(charID)
		if r.Method == "GET" && len(info) > 0 {
			writeJSONStatus(w, http.StatusOK, map[string]interface{}{
				"character_id": charID, "class": className, "level": level, "caster_type": "none",
				"prepared_spells": []interface{}{}, "prepared_count": 0, "max_prepared": 0, "slots_remaining": 0,
				"origin_spells":      info,
				"origin_spells_note": fmt.Sprintf("A %s doesn't prepare spells. Species and Magic Initiate spells are always prepared and cast with their own spellcasting ability; with no spell slots you use the free cast (it comes back on a Long Rest).", titleWords(className)),
			})
			return
		}
		msg := fmt.Sprintf("A %s doesn't prepare spells.", titleWords(className))
		if len(info) > 0 {
			msg += " Your species and Magic Initiate spells are always prepared: GET this endpoint lists them."
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_spellcaster", "message": msg})
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
	alwaysPrepared = append(alwaysPrepared, magicalDiscoveriesOf(charID)...)                      // College of Lore 6: Magical Discoveries

	// class features (Druidic, Favored Enemy ...) and the Book of Shadows
	alwaysPrepared = append(alwaysPrepared, featureAlwaysPrepared(charID)...)

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
		if inv := invocationSpellsView(charID); len(inv) > 0 { // spells your Eldritch Invocations give (Pact of the Chain: Find Familiar), as on the sheet (re-check F21)
			response["invocation_spells"] = inv
			response["invocation_spells_note"] = "From your Eldritch Invocations: you know them without preparing them, and they don't count against your limit."
		}

		if len(domainSpellsInfo) > 0 {
			response["domain_spells"] = domainSpellsInfo
			response["domain_spells_note"] = "Always prepared, don't count against your limit"
		}
		if fa := featureAlwaysPreparedInfo(charID); len(fa) > 0 {
			response["class_feature_spells"] = fa
			response["class_feature_spells_note"] = "Always prepared by a class feature or your Book of Shadows; they don't count against your limit."
		}

		response["tip"] = fmt.Sprintf("POST to this endpoint with {\"spells\": [...]} to change prepared spells. You can prepare up to %d spells.", maxPrepared)

		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == "POST" {
		var req struct {
			Spells *[]string `json:"spells"` // Spell slugs to prepare
		}
		// F4: {"prepared": [...]} or {} was read as an empty list and wiped every prepared spell
		if msg := decodeStrict(r, &req, `{"spells": [...]} (your whole list of cantrips and spells)`); msg != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": msg + " Nothing was changed."})
			return
		}
		if req.Spells == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "spells_required",
				"message": `Send {"spells": [...]}: your whole list of cantrips and spells to prepare. Nothing was changed.`})
			return
		}

		// Validate each spell slug and build the prepared list
		newPrepared := []string{}
		var skippedAlways, skippedTwice []string // said in the reply (Nov 13): a list of only always-prepared spells used to answer "Prepared 0 spells" with no reason
		for _, spellSlug := range *req.Spells {
			slugLower := strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(strings.TrimSpace(spellSlug))) // "Hunter’s Mark" is hunters-mark (Nov 14 audit re-check)
			slugDashed := strings.ReplaceAll(slugLower, " ", "-")

			// Check SRD
			validSlug := ""
			if _, ok := srdSpellsMemory[slugLower]; ok {
				validSlug = slugLower
			} else if _, ok := srdSpellsMemory[slugDashed]; ok {
				validSlug = slugDashed
			}

			if validSlug == "" {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
					"error":   "unknown_spell",
					"message": unknownSpellMessage(spellSlug),
				})
				return
			}
			if containsStr(alwaysPrepared, validSlug) { // F240: a Magic Initiate, lineage or subclass spell sent back with the list: always prepared, skip it
				if !containsStr(skippedAlways, srdSpellsMemory[validSlug].Name) {
					skippedAlways = append(skippedAlways, srdSpellsMemory[validSlug].Name)
				}
				continue
			}

			// A Wizard prepares spells from the spellbook
			if strings.ToLower(className) == "wizard" && srdSpellsMemory[validSlug].Level > 0 && !inSpellbook(charID, validSlug) { // cantrips are known, not in the book (F202)
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
					"error":   "not_in_spellbook",
					"message": fmt.Sprintf("'%s' is not in your spellbook. A Wizard prepares spells from the spellbook. Nothing was changed.", srdSpellsMemory[validSlug].Name),
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
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
						"error":   "not_on_class_list",
						"message": fmt.Sprintf("'%s' is not on the %s spell list. Check /api/universe/class-spells/%s for available spells. Nothing was changed.", srdSpellsMemory[validSlug].Name, className, strings.ToLower(className)),
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
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
						"error":   "spell_too_high",
						"message": fmt.Sprintf("Cannot prepare %s (level %d): you don't have level %d spell slots yet. Nothing was changed.", spell.Name, spell.Level, spell.Level),
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
			} else if !containsStr(skippedTwice, spell.Name) {
				skippedTwice = append(skippedTwice, spell.Name)
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
			writeJSONStatus(w, http.StatusConflict, spellListRefusal(charID, className, level, why))
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
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error", "detail": publicError(err)})
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
			"message":         preparedMessage(countLeveled(newPrepared), len(newPrepared)-countLeveled(newPrepared), maxPrepared),
		}
		var notes []string
		if len(skippedAlways) > 0 {
			notes = append(notes, fmt.Sprintf("%s: always prepared for you (it doesn't count against your limit), so it was left out of this list.", strings.Join(skippedAlways, ", ")))
		}
		if len(skippedTwice) > 0 {
			notes = append(notes, fmt.Sprintf("%s: sent twice, prepared once.", strings.Join(skippedTwice, ", ")))
		}
		if n := countLeveled(newPrepared); n < maxPrepared {
			notes = append(notes, fmt.Sprintf("This list replaces the one you had. It has %d spells of level 1 or higher; you can prepare %d.", n, maxPrepared))
		}
		if len(notes) > 0 {
			response["notes"] = notes
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
		w.WriteHeader(http.StatusNotFound)
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
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.Resource == "" {
		w.WriteHeader(http.StatusBadRequest)
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
		w.WriteHeader(http.StatusBadRequest)
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

// preparedMessage counts prepared spells and cantrips apart, as the class table does (Nov 14 tester: "Prepared 6 spells" for 4 spells
// and 2 cantrips).
func preparedMessage(spells, cantrips, max int) string {
	msg := fmt.Sprintf("Prepared %d of %d spells of level 1 or higher", spells, max)
	if spells == 1 {
		msg = fmt.Sprintf("Prepared 1 of %d spells of level 1 or higher", max)
	}
	switch cantrips {
	case 0:
	case 1:
		msg += ", plus 1 cantrip"
	default:
		msg += fmt.Sprintf(", plus %d cantrips", cantrips)
	}
	return msg + "."
}

// decodeStrict reads a JSON body into v and refuses unknown fields: "" when it worked, else what is wrong in plain words. fields
// names what the endpoint takes (F4: POST /prepare {"prepared": [...]} wiped every prepared spell and answered success).
func decodeStrict(r *http.Request, v interface{}, fields string) string {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64*1024))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case msg == "EOF":
		return "Send a JSON body with " + fields + "."
	case strings.HasPrefix(msg, "json: unknown field "):
		return fmt.Sprintf("Unknown field %s: send %s.", strings.TrimPrefix(msg, "json: unknown field "), fields)
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return fmt.Sprintf("%s has the wrong type (expected %s): send %s.", te.Field, te.Type.String(), fields)
	}
	return "The body isn't valid JSON (" + msg + "): send " + fields + "."
}

func spellName(slug string) string {
	if sp, ok := srdSpellsMemory[slug]; ok {
		return sp.Name
	}
	return slug
}

// wizardSpellbookTarget: how many spells a Wizard writes into the spellbook for free (SRD 5.2.1 Spellbook: six level 1 spells,
// two more each Wizard level; an Evoker's Evocation Savant spells too).
func wizardSpellbookTarget(charID, level int) int {
	target := 6 + 2*(level-1)
	if _, wsub, _ := subclassOf(charID); strings.EqualFold(wsub, "evocation") && level >= 3 {
		// Evocation Savant: two Evocation spells at level 3, and one more each time a new level of spell slots comes (5, 7 ... 17)
		target += 2
		for _, l := range []int{5, 7, 9, 11, 13, 15, 17} {
			if level >= l {
				target++
			}
		}
	}
	return target
}

var spellbookCopiedOnce sync.Once

func ensureSpellbookCopied() {
	spellbookCopiedOnce.Do(func() {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS spellbook_copied JSONB DEFAULT '[]'`)
	})
}

// spellbookCopied: the spells copied into the book with POST /api/characters/copy-spell (paid for, so not among the free ones).
func spellbookCopied(charID int) []string {
	ensureSpellbookCopied()
	return jsonListOf(charID, "spellbook_copied")
}

// freeSpellbookCount: the level 1+ spells of a spellbook list that count against the free ones (origin and copied spells don't:
// F14, a Sage wizard's Magic Initiate spell took one of the six).
func freeSpellbookCount(charID int, list []string) int {
	origin := originSpellsOf(charID)
	copied := spellbookCopied(charID)
	n := 0
	for _, s := range list {
		if _, o := origin[s]; o || containsStr(copied, s) {
			continue
		}
		if sp, ok := srdSpellsMemory[s]; ok && sp.Level > 0 {
			n++
		}
	}
	return n
}

// spellListRefusal: the error code and limit for a refused list change: a count over the Cantrips column is too_many_cantrips with
// the cantrip limit, over the Prepared Spells column too_many_spells, otherwise cannot_change_now (Nov 14 audit re-check: a cantrip
// count was answered cannot_change_now with the prepared-spell max)
func spellListRefusal(charID int, class string, level int, why string) map[string]interface{} {
	c := strings.ToLower(class)
	switch {
	case strings.Contains(why, "cantrips; this list has"):
		return map[string]interface{}{"error": "too_many_cantrips", "message": why, "max": game.ClassTableInt(c, level, "Cantrips") + orderExtraCantrips(charID)}
	case strings.Contains(why, "spells of level 1 or higher (the Prepared Spells column"):
		return map[string]interface{}{"error": "too_many_spells", "message": why, "max": game.PreparedSpells2024(c, level)}
	}
	return map[string]interface{}{"error": "cannot_change_now", "message": why, "max": game.PreparedSpells2024(c, level)}
}
