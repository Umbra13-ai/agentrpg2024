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

// handleCharacterMetamagic godoc
// @Summary Choose or view Metamagic options
// @Description Sorcerers choose 2 Metamagic options at level 2, and 2 more at levels 10 and 17 (SRD 5.2.1). GET to view choices, POST to learn a new option.
// @Tags Characters
// @Accept json
// @Produce json
// @Param character_id query int false "Character ID (for GET)"
// @Param request body object{character_id=int,metamagic=string} false "Learn a metamagic option"
// @Security BasicAuth
// @Success 200 {object} object{metamagic_known=[]string,max_choices=int,can_learn_more=bool}
// @Router /characters/metamagic [get]
// @Router /characters/metamagic [post]
func handleCharacterMetamagic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// View metamagic options (available and known)
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			// List all available metamagic options
			options := []MetamagicOption{}
			for _, opt := range metamagicOptions {
				options = append(options, opt)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"available_metamagic": options,
				"note":                "Use character_id parameter to see a specific character's learned metamagic",
			})
			return
		}

		var class string
		var level int
		var choicesJSON []byte
		err = db.QueryRow(`
			SELECT class, level, COALESCE(metamagic_choices, '[]')
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &level, &choicesJSON)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		if strings.ToLower(class) != "sorcerer" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_a_sorcerer",
				"message": "Only Sorcerers can learn Metamagic",
			})
			return
		}

		var knownSlugs []string
		json.Unmarshal(choicesJSON, &knownSlugs)

		knownOptions := []MetamagicOption{}
		for _, slug := range knownSlugs {
			if opt, ok := metamagicOptions[slug]; ok {
				knownOptions = append(knownOptions, opt)
			}
		}

		maxChoices := getMaxMetamagicChoices(level)
		canLearnMore := len(knownSlugs) < maxChoices

		// Available to learn
		availableToLearn := []MetamagicOption{}
		for slug, opt := range metamagicOptions {
			known := false
			for _, k := range knownSlugs {
				if k == slug {
					known = true
					break
				}
			}
			if !known {
				availableToLearn = append(availableToLearn, opt)
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"character_id":       charID,
			"level":              level,
			"metamagic_known":    knownOptions,
			"known_count":        len(knownSlugs),
			"max_choices":        maxChoices,
			"can_learn_more":     canLearnMore,
			"available_to_learn": availableToLearn,
			"how_to_use":         "Include metamagic keyword in spell description, e.g., 'quickened fireball', 'twinned haste', 'subtle charm person'",
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
		Metamagic   string `json:"metamagic"` // slug: careful, distant, empowered, extended, heightened, quickened, subtle, twinned
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_json",
			"message": publicError(err),
		})
		return
	}

	// Validate metamagic option exists
	metamagicSlug := strings.ToLower(strings.TrimSpace(req.Metamagic))
	opt, validMetamagic := metamagicOptions[metamagicSlug]
	if !validMetamagic {
		validSlugs := []string{}
		for slug := range metamagicOptions {
			validSlugs = append(validSlugs, slug)
		}
		sort.Strings(validSlugs)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "invalid_metamagic",
			"message":       fmt.Sprintf("'%s' is not a valid Metamagic option", req.Metamagic),
			"valid_options": validSlugs,
		})
		return
	}

	// Get character info
	var ownerID int
	var class, charName string
	var level int
	var choicesJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, name, class, level, COALESCE(metamagic_choices, '[]')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &charName, &class, &level, &choicesJSON)

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
			"message": "You can only choose Metamagic for your own characters",
		})
		return
	}

	if strings.ToLower(class) != "sorcerer" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_a_sorcerer",
			"message": "Only Sorcerers can learn Metamagic",
		})
		return
	}

	if level < 2 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "level_too_low",
			"message": "Sorcerers gain Metamagic at level 2",
			"level":   level,
		})
		return
	}

	var currentChoices []string
	json.Unmarshal(choicesJSON, &currentChoices)

	// Check if already known
	for _, c := range currentChoices {
		if c == metamagicSlug {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "already_known",
				"message": fmt.Sprintf("%s already knows %s", charName, opt.Name),
			})
			return
		}
	}

	// Check if at capacity
	maxChoices := getMaxMetamagicChoices(level)
	if len(currentChoices) >= maxChoices {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":       "at_capacity",
			"message":     fmt.Sprintf("%s has already learned %d Metamagic options (max at level %d)", charName, len(currentChoices), level),
			"known":       currentChoices,
			"max_choices": maxChoices,
			"next_at":     []int{10, 17},
		})
		return
	}

	// Add the choice
	currentChoices = append(currentChoices, metamagicSlug)
	updatedJSON, _ := json.Marshal(currentChoices)

	_, err = db.Exec("UPDATE characters SET metamagic_choices = $1 WHERE id = $2", updatedJSON, req.CharacterID)
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
		"learned":        opt.Name,
		"description":    opt.Description,
		"cost":           opt.Cost,
		"cost_formula":   opt.CostFormula,
		"known_count":    len(currentChoices),
		"max_choices":    maxChoices,
		"can_learn_more": len(currentChoices) < maxChoices,
		"all_known":      currentChoices,
		"how_to_use":     fmt.Sprintf("Include '%s' in your spell description, e.g., '%s fireball'", metamagicSlug, metamagicSlug),
	})
}

// handleCharacterInvocations godoc
// @Summary Choose or view Eldritch Invocations (Warlock)
// @Description Warlocks gain an Eldritch Invocation at level 1 and more as the Invocations column of the Warlock table shows (3 at level 2, 5 at 5 ...). GET to view options, POST to learn one.
// @Tags Characters
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param character_id query integer false "Character ID (for GET)"
// @Param request body object{character_id=integer,invocation=string} false "Learn an invocation (for POST)"
// @Success 200 {object} map[string]interface{} "Invocation info"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /characters/invocations [get]
// @Router /characters/invocations [post]
func handleCharacterInvocations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// View invocations (available and known)
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			// List all available invocations
			invocations := []map[string]interface{}{}
			for slug, inv := range game.AvailableInvocations {
				prereqs := map[string]interface{}{}
				if inv.Prerequisites.Level > 0 {
					prereqs["level"] = inv.Prerequisites.Level
				}
				if inv.Prerequisites.RequiresSpell != "" {
					prereqs["requires_spell"] = inv.Prerequisites.RequiresSpell
				}
				if inv.Prerequisites.Pact != "" {
					prereqs["pact_invocation"] = inv.Prerequisites.Pact
				}
				invocations = append(invocations, map[string]interface{}{
					"slug":          slug,
					"name":          inv.Name,
					"description":   inv.Description,
					"prerequisites": prereqs,
				})
			}
			// Sort by name
			sort.Slice(invocations, func(i, j int) bool {
				return invocations[i]["name"].(string) < invocations[j]["name"].(string)
			})
			json.NewEncoder(w).Encode(map[string]interface{}{
				"available_invocations": invocations,
				"total":                 len(invocations),
				"note":                  "Use character_id parameter to see a Warlock's learned invocations",
			})
			return
		}

		var class string
		var level int
		var invocationsJSON, knownSpellsJSON []byte
		err = db.QueryRow(`
			SELECT class, level, COALESCE(eldritch_invocations, '[]'), COALESCE(known_spells, '[]')
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &level, &invocationsJSON, &knownSpellsJSON)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		if wl := classLevelsOf(charID)["warlock"]; wl > 0 {
			class, level = "warlock", wl // multiclass: the Warlock level
		}
		if strings.ToLower(class) != "warlock" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_a_warlock",
				"message": "Only Warlocks can learn Eldritch Invocations",
			})
			return
		}

		var knownSlugs []string
		json.Unmarshal(invocationsJSON, &knownSlugs)

		var knownSpells []string
		json.Unmarshal(knownSpellsJSON, &knownSpells)

		knownInvocations := []map[string]interface{}{}
		for _, slug := range knownSlugs {
			if inv, ok := game.AvailableInvocations[slug]; ok {
				knownInvocations = append(knownInvocations, map[string]interface{}{
					"slug":        slug,
					"name":        inv.Name,
					"description": inv.Description,
					"mechanics":   inv.Mechanics,
				})
			}
		}

		maxInvocations := game.GetMaxInvocations(level)
		canLearnMore := len(knownSlugs) < maxInvocations

		// Available to learn (filtered by prerequisites)
		availableToLearn := []map[string]interface{}{}
		for slug, inv := range game.AvailableInvocations {
			// Skip if already known
			known := false
			for _, k := range knownSlugs {
				if k == slug {
					known = true
					break
				}
			}
			if known {
				continue
			}

			// Check prerequisites
			meetsReqs, reason := meetsInvocationPrerequisites(charID, inv)
			prereqs := map[string]interface{}{}
			if inv.Prerequisites.Level > 0 {
				prereqs["level"] = inv.Prerequisites.Level
			}
			if inv.Prerequisites.RequiresSpell != "" {
				prereqs["requires_spell"] = inv.Prerequisites.RequiresSpell
			}
			if inv.Prerequisites.Pact != "" {
				prereqs["pact_invocation"] = inv.Prerequisites.Pact
			}

			entry := map[string]interface{}{
				"slug":          slug,
				"name":          inv.Name,
				"description":   inv.Description,
				"prerequisites": prereqs,
				"eligible":      meetsReqs,
			}
			if !meetsReqs {
				entry["ineligible_reason"] = reason
			}
			availableToLearn = append(availableToLearn, entry)
		}
		// Sort by eligibility then name
		sort.Slice(availableToLearn, func(i, j int) bool {
			iElig := availableToLearn[i]["eligible"].(bool)
			jElig := availableToLearn[j]["eligible"].(bool)
			if iElig != jElig {
				return iElig // Eligible first
			}
			return availableToLearn[i]["name"].(string) < availableToLearn[j]["name"].(string)
		})

		json.NewEncoder(w).Encode(map[string]interface{}{
			"character_id":       charID,
			"level":              level,
			"invocations_known":  knownInvocations,
			"known_count":        len(knownSlugs),
			"max_invocations":    maxInvocations,
			"can_learn_more":     canLearnMore,
			"available_to_learn": availableToLearn,
			"note":               "Invocations with 'agonizing-blast' add CHA mod to eldritch blast damage. 'at_will_spell' invocations let you cast a spell without using a spell slot.",
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
		Invocation  string `json:"invocation"` // slug
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_json",
			"message": publicError(err),
		})
		return
	}

	// Validate invocation exists
	invocationSlug := strings.ToLower(strings.TrimSpace(req.Invocation))
	inv, validInvocation := game.AvailableInvocations[invocationSlug]
	if !validInvocation {
		validSlugs := []string{}
		for slug := range game.AvailableInvocations {
			validSlugs = append(validSlugs, slug)
		}
		sort.Strings(validSlugs)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "invalid_invocation",
			"message":       fmt.Sprintf("'%s' is not a valid Eldritch Invocation", req.Invocation),
			"valid_options": validSlugs,
		})
		return
	}

	// Get character info
	var ownerID int
	var class, charName string
	var level int
	var invocationsJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, name, class, level, COALESCE(eldritch_invocations, '[]')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &charName, &class, &level, &invocationsJSON)

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
			"message": "You can only choose Invocations for your own characters",
		})
		return
	}

	if wl := classLevelsOf(req.CharacterID)["warlock"]; wl > 0 {
		class, level = "warlock", wl // a multiclass character's invocations follow its Warlock level (count and prerequisites)
	}
	if strings.ToLower(class) != "warlock" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_a_warlock",
			"message": "Only Warlocks can learn Eldritch Invocations",
		})
		return
	}
	// 2024: a level 1 Warlock already has one invocation (the 2014 rules started them at level 2)

	var currentInvocations []string
	json.Unmarshal(invocationsJSON, &currentInvocations)

	// Check if already known
	for _, c := range currentInvocations {
		if c == invocationSlug {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "already_known",
				"message": fmt.Sprintf("%s already knows %s", charName, inv.Name),
			})
			return
		}
	}

	// Check if at capacity
	maxInvocations := game.GetMaxInvocations(level)
	if len(currentInvocations) >= maxInvocations {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":           "at_capacity",
			"message":         fmt.Sprintf("%s has already learned %d Invocations (max at level %d)", charName, len(currentInvocations), level),
			"known":           currentInvocations,
			"max_invocations": maxInvocations,
		})
		return
	}

	// Check prerequisites
	meetsReqs, reason := meetsInvocationPrerequisites(req.CharacterID, inv)
	if !meetsReqs {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "prerequisite_not_met",
			"message": reason,
		})
		return
	}

	// Add the invocation
	currentInvocations = append(currentInvocations, invocationSlug)
	updatedJSON, _ := json.Marshal(currentInvocations)

	_, err = db.Exec("UPDATE characters SET eldritch_invocations = $1 WHERE id = $2", updatedJSON, req.CharacterID)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "database_error",
			"message": publicError(err),
		})
		return
	}

	// Apply passive effects
	effects := []string{}
	if inv.Mechanics["grant_proficiency"] != "" {
		// Add skill proficiencies (Beguiling Influence)
		proficiencies := strings.Split(inv.Mechanics["grant_proficiency"], ",")
		var currentSkillsJSON []byte
		db.QueryRow("SELECT COALESCE(skill_proficiencies, '[]') FROM characters WHERE id = $1", req.CharacterID).Scan(&currentSkillsJSON)
		var currentSkills []string
		json.Unmarshal(currentSkillsJSON, &currentSkills)

		for _, prof := range proficiencies {
			prof = strings.TrimSpace(prof)
			alreadyHas := false
			for _, s := range currentSkills {
				if strings.EqualFold(s, prof) {
					alreadyHas = true
					break
				}
			}
			if !alreadyHas {
				currentSkills = append(currentSkills, prof)
				effects = append(effects, fmt.Sprintf("Gained proficiency in %s", prof))
			}
		}
		updatedSkillsJSON, _ := json.Marshal(currentSkills)
		db.Exec("UPDATE characters SET skill_proficiencies = $1 WHERE id = $2", updatedSkillsJSON, req.CharacterID)
	}

	response := map[string]interface{}{
		"success":         true,
		"character_id":    req.CharacterID,
		"character_name":  charName,
		"learned":         inv.Name,
		"description":     inv.Description,
		"mechanics":       inv.Mechanics,
		"known_count":     len(currentInvocations),
		"max_invocations": maxInvocations,
		"can_learn_more":  len(currentInvocations) < maxInvocations,
		"all_known":       currentInvocations,
	}
	if len(effects) > 0 {
		response["effects_applied"] = effects
	}

	json.NewEncoder(w).Encode(response)
}

// handleUniverseInvocations godoc
// @Summary List all Eldritch Invocations
// @Description Get a list of all available Eldritch Invocations with prerequisites
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "List of invocations"
// @Router /universe/invocations [get]
func handleUniverseInvocations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Build list with prerequisites
	invocations := []map[string]interface{}{}
	for slug, inv := range game.AvailableInvocations {
		prereqs := map[string]interface{}{}
		if inv.Prerequisites.Level > 0 {
			prereqs["level"] = inv.Prerequisites.Level
		}
		if inv.Prerequisites.RequiresSpell != "" {
			prereqs["requires_spell"] = inv.Prerequisites.RequiresSpell
		}
		if inv.Prerequisites.Pact != "" {
			prereqs["pact_invocation"] = inv.Prerequisites.Pact
		}

		invocations = append(invocations, map[string]interface{}{
			"slug":          slug,
			"name":          inv.Name,
			"description":   inv.Description,
			"prerequisites": prereqs,
			"mechanics":     inv.Mechanics,
		})
	}

	// Sort by level requirement then name
	sort.Slice(invocations, func(i, j int) bool {
		iLevel := 0
		jLevel := 0
		if prereqs, ok := invocations[i]["prerequisites"].(map[string]interface{}); ok {
			if l, ok := prereqs["level"].(int); ok {
				iLevel = l
			}
		}
		if prereqs, ok := invocations[j]["prerequisites"].(map[string]interface{}); ok {
			if l, ok := prereqs["level"].(int); ok {
				jLevel = l
			}
		}
		if iLevel != jLevel {
			return iLevel < jLevel
		}
		return invocations[i]["name"].(string) < invocations[j]["name"].(string)
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"invocations": invocations,
		"total":       len(invocations),
		"invocations_by_level": map[string]int{
			"no_prerequisite": countInvocationsByLevel(0),
			"level_5":         countInvocationsByLevel(5),
			"level_7":         countInvocationsByLevel(7),
			"level_9":         countInvocationsByLevel(9),
			"level_12":        countInvocationsByLevel(12),
			"level_15":        countInvocationsByLevel(15),
		},
		"note": "Use POST /api/characters/invocations to learn an invocation for your Warlock",
	})
}

func countInvocationsByLevel(level int) int {
	count := 0
	for _, inv := range game.AvailableInvocations {
		if inv.Prerequisites.Level == level {
			count++
		}
	}
	return count
}

// handleFlexibleCasting godoc
// @Summary Convert between sorcery points and spell slots
// @Description Sorcerer's Font of Magic feature: create spell slots from sorcery points or convert slots to points
// @Tags Characters
// @Accept json
// @Produce json
// @Param request body object{character_id=int,action=string,slot_level=int} true "Action: 'create_slot' or 'convert_slot', slot_level: 1-5"
// @Security BasicAuth
// @Success 200 {object} object{success=bool,sorcery_points=int,message=string}
// @Router /characters/flexible-casting [post]
func handleFlexibleCasting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// Return conversion table
		json.NewEncoder(w).Encode(map[string]interface{}{
			"description": "Font of Magic allows Sorcerers to convert between sorcery points and spell slots",
			"create_slot_costs": map[string]int{
				"1st_level": 2,
				"2nd_level": 3,
				"3rd_level": 5,
				"4th_level": 6,
				"5th_level": 7,
			},
			"convert_slot_yields": map[string]int{
				"1st_level": 1,
				"2nd_level": 2,
				"3rd_level": 3,
				"4th_level": 4,
				"5th_level": 5,
			},
			"note":  "You can create spell slots no higher than 5th level. Cannot exceed your maximum sorcery points when converting.",
			"usage": "POST with character_id, action ('create_slot' or 'convert_slot'), and slot_level (1-5)",
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
		Action      string `json:"action"`     // "create_slot" or "convert_slot"
		SlotLevel   int    `json:"slot_level"` // 1-5
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_json",
			"message": publicError(err),
		})
		return
	}

	// Validate action
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "create_slot" && action != "convert_slot" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_action",
			"message": "Action must be 'create_slot' (spend points for slot) or 'convert_slot' (spend slot for points)",
		})
		return
	}

	// Validate slot level
	if req.SlotLevel < 1 || req.SlotLevel > 5 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_slot_level",
			"message": "Slot level must be 1-5 for Flexible Casting",
		})
		return
	}

	// Sorcery point costs to CREATE a slot
	slotCreationCosts := map[int]int{1: 2, 2: 3, 3: 5, 4: 6, 5: 7}
	// Sorcery points GAINED from converting a slot
	slotConversionYields := map[int]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 5}

	// Get character info
	var ownerID int
	var class, charName string
	var level int
	var resourcesJSON, slotsUsedJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, name, class, level, COALESCE(class_resources, '{}'), COALESCE(spell_slots_used, '{}')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &charName, &class, &level, &resourcesJSON, &slotsUsedJSON)

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
			"message": "You can only use Flexible Casting for your own characters",
		})
		return
	}

	if strings.ToLower(class) != "sorcerer" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_a_sorcerer",
			"message": "Only Sorcerers have Font of Magic",
		})
		return
	}

	if level < 2 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "level_too_low",
			"message": "Sorcerers gain Font of Magic at level 2",
		})
		return
	}

	var resources map[string]int
	json.Unmarshal(resourcesJSON, &resources)
	if resources == nil {
		resources = make(map[string]int)
	}

	var slotsUsed map[string]int
	json.Unmarshal(slotsUsedJSON, &slotsUsed)
	if slotsUsed == nil {
		slotsUsed = make(map[string]int)
	}

	maxPoints := level // Sorcery points = sorcerer level
	usedRes := classResourcesUsed(req.CharacterID)
	currentPoints := max(0, maxPoints-usedRes["sorcery_points"]) // F125: the points Metamagic spends (class_resources_used), not the old class_resources column
	resources["sorcery_points"] = currentPoints

	// Get spell slots for this level
	spellSlots := characterSpellSlots(req.CharacterID, class, level)
	slotKey := fmt.Sprintf("%d", req.SlotLevel)
	totalSlots := spellSlots[req.SlotLevel]
	usedSlots := slotsUsed[slotKey]
	availableSlots := totalSlots - usedSlots

	if action == "create_slot" {
		// Spend sorcery points to create a spell slot
		cost := slotCreationCosts[req.SlotLevel]
		if currentPoints < cost {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":          "insufficient_points",
				"message":        fmt.Sprintf("Creating a level %d spell slot costs %d sorcery points, but you only have %d", req.SlotLevel, cost, currentPoints),
				"current_points": currentPoints,
				"cost":           cost,
			})
			return
		}

		// Spend points
		resources["sorcery_points"] = currentPoints - cost
		// Gain a slot: an unspent one comes back, or (F128, SRD 5.2.1) an extra slot that vanishes on a Long Rest (a negative used count)
		if true {
			slotsUsed[slotKey] = usedSlots - 1
		} else {
			// Already at max slots - can't create more
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":           "slots_full",
				"message":         fmt.Sprintf("You already have all your level %d spell slots available (%d/%d)", req.SlotLevel, availableSlots, totalSlots),
				"available_slots": availableSlots,
				"total_slots":     totalSlots,
			})
			return
		}

		// Save
		resourcesJSON, _ = json.Marshal(resources)
		slotsUsedJSON, _ = json.Marshal(slotsUsed)
		db.Exec("UPDATE characters SET class_resources = $1, spell_slots_used = $2 WHERE id = $3",
			resourcesJSON, slotsUsedJSON, req.CharacterID)
		usedRes["sorcery_points"] = maxPoints - resources["sorcery_points"]
		writeUsed(req.CharacterID, usedRes) // F125

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":            true,
			"action":             "create_slot",
			"slot_level":         req.SlotLevel,
			"points_spent":       cost,
			"sorcery_points":     resources["sorcery_points"],
			"max_sorcery_points": maxPoints,
			"slots_available":    availableSlots + 1,
			"total_slots":        totalSlots,
			"message":            fmt.Sprintf("%s spent %d sorcery points to create a level %d spell slot", charName, cost, req.SlotLevel),
		})

	} else { // convert_slot
		// Convert a spell slot to sorcery points
		if availableSlots <= 0 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":       "no_slots_available",
				"message":     fmt.Sprintf("You have no level %d spell slots to convert (%d/%d used)", req.SlotLevel, usedSlots, totalSlots),
				"used_slots":  usedSlots,
				"total_slots": totalSlots,
			})
			return
		}

		pointsGained := slotConversionYields[req.SlotLevel]
		newPoints := currentPoints + pointsGained
		if newPoints > maxPoints {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":            "would_exceed_max",
				"message":          fmt.Sprintf("Converting would give you %d points, but max is %d", newPoints, maxPoints),
				"current_points":   currentPoints,
				"points_from_slot": pointsGained,
				"max_points":       maxPoints,
			})
			return
		}

		// Use the slot
		slotsUsed[slotKey] = usedSlots + 1
		// Gain points
		resources["sorcery_points"] = newPoints

		// Save
		resourcesJSON, _ = json.Marshal(resources)
		slotsUsedJSON, _ = json.Marshal(slotsUsed)
		db.Exec("UPDATE characters SET class_resources = $1, spell_slots_used = $2 WHERE id = $3",
			resourcesJSON, slotsUsedJSON, req.CharacterID)
		usedRes["sorcery_points"] = maxPoints - resources["sorcery_points"]
		writeUsed(req.CharacterID, usedRes) // F125

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":            true,
			"action":             "convert_slot",
			"slot_level":         req.SlotLevel,
			"points_gained":      pointsGained,
			"sorcery_points":     resources["sorcery_points"],
			"max_sorcery_points": maxPoints,
			"slots_remaining":    availableSlots - 1,
			"total_slots":        totalSlots,
			"message":            fmt.Sprintf("%s converted a level %d spell slot into %d sorcery points", charName, req.SlotLevel, pointsGained),
		})
	}
}

// handleUniverseMetamagic godoc
// @Summary List all Metamagic options
// handleCharacterFightingStyle handles viewing and choosing fighting styles
// @Summary View or choose fighting style
// @Description GET: View available and known fighting styles. POST: Choose a fighting style.
// @Tags Characters
// @Accept json
// @Produce json
// @Param character_id query int false "Character ID (for GET)"
// @Param request body object{character_id=int,style=string} false "Fighting style choice (for POST)"
// @Success 200 {object} object
// @Router /characters/fighting-style [get]
// @Router /characters/fighting-style [post]
func handleCharacterFightingStyle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			// List all fighting styles
			styles := []FightingStyle{}
			slugs := []string{}
			for slug := range fightingStyles {
				slugs = append(slugs, slug)
			}
			sort.Strings(slugs)
			for _, slug := range slugs {
				styles = append(styles, fightingStyles[slug])
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"fighting_styles": styles,
				"note":            "Use character_id parameter to see a specific character's fighting styles",
			})
			return
		}

		var class, subclass string
		var level int
		var stylesJSON []byte
		err = db.QueryRow(`
			SELECT class, level, COALESCE(subclass, ''), COALESCE(fighting_styles, '[]')
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &level, &subclass, &stylesJSON)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		classLower := strings.ToLower(class)
		maxStyles := getMaxFightingStyles(class, level, subclass)

		if maxStyles == 0 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_fighting_style_feature",
				"message": fmt.Sprintf("%s level %d does not have the Fighting Style feature", class, level),
				"note":    "Fighters get Fighting Style at level 1. Paladins and Rangers get it at level 2.",
			})
			return
		}

		var knownSlugs []string
		json.Unmarshal(stylesJSON, &knownSlugs)

		knownStyles := []FightingStyle{}
		for _, slug := range knownSlugs {
			if style, ok := fightingStyles[slug]; ok {
				knownStyles = append(knownStyles, style)
			}
		}

		// Available styles for this class
		available := getAvailableFightingStyles(classLower)

		// Filter out already known
		availableToChoose := []FightingStyle{}
		for _, style := range available {
			known := false
			for _, k := range knownSlugs {
				if k == style.Slug {
					known = true
					break
				}
			}
			if !known {
				availableToChoose = append(availableToChoose, style)
			}
		}

		canChooseMore := len(knownSlugs) < maxStyles

		response := map[string]interface{}{
			"character_id":    charID,
			"class":           class,
			"level":           level,
			"fighting_styles": knownStyles,
			"styles_count":    len(knownSlugs),
			"max_styles":      maxStyles,
			"can_choose_more": canChooseMore,
			"available":       availableToChoose,
		}

		if canChooseMore {
			response["how_to_choose"] = "POST /api/characters/fighting-style with character_id and style (slug)"
		}

		if subclass == "champion" && level >= 7 && len(knownSlugs) < 2 {
			response["champion_note"] = "Champion's Additional Fighting Style: You can choose a second fighting style!"
		}

		json.NewEncoder(w).Encode(response)
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
		CharacterID int      `json:"character_id"`
		Style       string   `json:"style"`
		Replace     string   `json:"replace"`  // Fighter: the style to swap out when gaining a Fighter level
		Cantrips    []string `json:"cantrips"` // Blessed Warrior / Druidic Warrior: two cantrips
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json", "message": publicError(err)})
		return
	}

	// Verify ownership
	var ownerID int
	var class, subclass string
	var level int
	var stylesJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, class, level, COALESCE(subclass, ''), COALESCE(fighting_styles, '[]')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &class, &level, &subclass, &stylesJSON)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Character %d not found", req.CharacterID),
		})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_owner",
			"message": "You can only choose fighting styles for your own characters",
		})
		return
	}

	classLower := strings.ToLower(class)
	maxStyles := getMaxFightingStyles(class, level, subclass)

	if maxStyles == 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_fighting_style_feature",
			"message": fmt.Sprintf("%s level %d does not have the Fighting Style feature", class, level),
		})
		return
	}

	var knownSlugs []string
	json.Unmarshal(stylesJSON, &knownSlugs)

	replacing := ""
	if len(knownSlugs) >= maxStyles {
		if req.Replace == "" || classLower != "fighter" {
			msg := fmt.Sprintf("You already have %d fighting style(s), the maximum for %s level %d.", len(knownSlugs), class, level)
			if classLower == "fighter" {
				msg += " Whenever you gain a Fighter level you can replace one: send replace with the slug to swap out."
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "max_styles_reached", "message": msg})
			return
		}
		replacing = strings.ToLower(strings.ReplaceAll(req.Replace, " ", "_"))
		found := false
		for _, k := range knownSlugs {
			if k == replacing {
				found = true
			}
		}
		if !found {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_known", "message": "You do not have the fighting style " + req.Replace, "known": knownSlugs})
			return
		}
		var lastLevel int
		db.QueryRow(`SELECT COALESCE(fighting_style_level,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&lastLevel)
		if level <= lastLevel {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "swap_not_yet", "message": "You can replace a Fighting Style feat whenever you gain a Fighter level; you already changed it at this level."})
			return
		}
	}

	// Validate style exists
	styleSlug := strings.ToLower(strings.ReplaceAll(req.Style, " ", "_"))
	style, exists := fightingStyles[styleSlug]
	if !exists {
		msg := fmt.Sprintf("%q is not a Fighting Style on this server.", req.Style)
		if note := phbNote("feat", req.Style); note != "" {
			msg = note
		}
		var names []string
		for slug, f := range fightingStyles {
			if containsStr(f.Classes, classLower) {
				names = append(names, slug)
			}
		}
		sort.Strings(names)
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error":        "invalid_style",
			"message":      msg + " The Fighting Styles you can choose: " + strings.Join(names, ", ") + ".",
			"valid_styles": getAvailableFightingStyles(classLower),
		})
		return
	}

	// Check if class can use this style
	classCanUse := false
	for _, c := range style.Classes {
		if c == classLower {
			classCanUse = true
			break
		}
	}
	if !classCanUse {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":     "style_not_available",
			"message":   fmt.Sprintf("%s cannot choose the %s fighting style", class, style.Name),
			"available": getAvailableFightingStyles(classLower),
		})
		return
	}

	// Check if already known
	for _, k := range knownSlugs {
		if k == styleSlug {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "already_known",
				"message": fmt.Sprintf("You already know the %s fighting style", style.Name),
			})
			return
		}
	}

	if styleSlug == "blessed_warrior" || styleSlug == "druidic_warrior" {
		if len(req.Cantrips) != 2 || strings.EqualFold(req.Cantrips[0], req.Cantrips[1]) {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "cantrips_needed", "message": "Send cantrips: two different cantrips (Cleric cantrips for Blessed Warrior, Druid cantrips for Druidic Warrior)."})
			return
		}
		db.Exec(`UPDATE characters SET subclass_choices = COALESCE(subclass_choices,'{}'::jsonb) || jsonb_build_object('fighting_style_cantrips', $1::text) WHERE id = $2`,
			strings.Join(req.Cantrips, ", "), req.CharacterID)
	}

	// Add the style (or swap it for the one being replaced)
	if replacing != "" {
		for k, v := range knownSlugs {
			if v == replacing {
				knownSlugs[k] = styleSlug
			}
		}
	} else {
		knownSlugs = append(knownSlugs, styleSlug)
	}
	newStylesJSON, _ := json.Marshal(knownSlugs)

	db.Exec(`UPDATE characters SET fighting_style_level = $1 WHERE id = $2`, level, req.CharacterID)
	_, err = db.Exec(`UPDATE characters SET fighting_styles = $1 WHERE id = $2`, newStylesJSON, req.CharacterID)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "db_error",
			"message": publicError(err),
		})
		return
	}

	// Get all known styles with info
	knownStyles := []FightingStyle{}
	for _, slug := range knownSlugs {
		if s, ok := fightingStyles[slug]; ok {
			knownStyles = append(knownStyles, s)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"message":         fmt.Sprintf("⚔️ You have adopted the %s fighting style!", style.Name),
		"style":           style,
		"fighting_styles": knownStyles,
		"styles_count":    len(knownSlugs),
		"max_styles":      maxStyles,
		"effects": map[string]string{
			"archery":               "+2 to attack rolls with Ranged weapons",
			"defense":               "+1 AC while wearing armor",
			"great_weapon_fighting": "A 1 or 2 on a damage die counts as a 3 (two-handed Melee weapons)",
			"two_weapon_fighting":   "Add your ability modifier to the damage of the Light-weapon extra attack",
			"blessed_warrior":       "Two Cleric cantrips as Paladin spells (Charisma)",
			"druidic_warrior":       "Two Druid cantrips as Ranger spells (Wisdom)",
		}[styleSlug],
	})
}

// Dragonborn breath weapon area shapes
// Line breaths: black, blue, brass, bronze, copper
// Cone breaths: gold, green, red, silver, white
var dragonAncestryAreaShapes = map[string]string{
	"black":  "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"blue":   "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"brass":  "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"bronze": "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"copper": "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"gold":   "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"green":  "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"red":    "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"silver": "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
	"white":  "15-foot Cone or 30-foot Line (5 feet wide), chosen each use",
}

// dragonAncestryBreathSavingThrows maps dragon ancestry to saving throw
// DEX save: fire, lightning, cold, acid breaths
// CON save: poison breath
var dragonAncestryBreathSavingThrows = map[string]string{
	"black":  "DEX", // acid
	"blue":   "DEX", // lightning
	"brass":  "DEX", // fire
	"bronze": "DEX", // lightning
	"copper": "DEX", // acid
	"gold":   "DEX", // fire
	"green":  "DEX", // 2024: the breath weapon is always a Dexterity save
	"red":    "DEX", // fire
	"silver": "DEX", // cold
	"white":  "DEX", // cold
}

// getBreathWeaponDamageDice returns damage dice based on character level
func getBreathWeaponDamageDice(level int) string {
	switch { // 2024: d10s, stepping up at levels 5, 11 and 17
	case level >= 17:
		return "4d10"
	case level >= 11:
		return "3d10"
	case level >= 5:
		return "2d10"
	default:
		return "1d10"
	}
}

// handleCharacterFiendishResilience handles choosing damage resistance for Fiend Warlocks level 10+
// @Summary Fiendish Resilience - choose damage type for resistance
// @Description Fiend Warlocks at level 10+ choose one damage type other than Force whenever they finish a Short or Long Rest, and have Resistance to it.
// @Tags Characters
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param character_id query integer false "Character ID (for GET)"
// @Param request body object{character_id=integer,damage_type=string} false "Fiendish Resilience choice (for POST)"
// @Success 200 {object} map[string]interface{} "Fiendish Resilience status or confirmation"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not your character"
// @Router /characters/fiendish-resilience [get]
// @Router /characters/fiendish-resilience [post]
func handleCharacterFiendishResilience(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Valid damage types for Fiendish Resilience
	validDamageTypes := map[string]bool{
		"acid": true, "cold": true, "fire": true, "lightning": true,
		"poison": true, "thunder": true, "necrotic": true, "psychic": true, "radiant": true,
		"bludgeoning": true, "piercing": true, "slashing": true,
	}

	if r.Method == "GET" {
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_id_required",
				"message": "Provide character_id to check Fiendish Resilience status",
				"usage":   "GET /api/characters/fiendish-resilience?character_id=X",
			})
			return
		}

		var class string
		var level int
		var subclassNull, fiendishResNull sql.NullString
		err = db.QueryRow(`
			SELECT class, level, subclass, fiendish_resilience
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &level, &subclassNull, &fiendishResNull)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		subclass := ""
		if subclassNull.Valid {
			subclass = subclassNull.String
		}

		if strings.ToLower(class) != "warlock" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_warlock",
				"message": fmt.Sprintf("Fiendish Resilience is a Warlock feature (character is %s)", class),
			})
			return
		}

		if strings.ToLower(subclass) != "fiend" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "wrong_patron",
				"message": fmt.Sprintf("Fiendish Resilience is a Fiend patron feature (character's patron: %s)", subclass),
			})
			return
		}

		if level < 10 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "level_requirement",
				"message": fmt.Sprintf("Fiendish Resilience requires Fiend Warlock level 10+ (currently level %d)", level),
			})
			return
		}

		currentResistance := ""
		if fiendishResNull.Valid {
			currentResistance = fiendishResNull.String
		}

		// List valid damage types
		damageTypeList := []string{"acid", "cold", "fire", "lightning", "poison", "thunder", "necrotic", "psychic", "bludgeoning", "piercing", "slashing"}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"character_id":       charID,
			"class":              class,
			"subclass":           subclass,
			"level":              level,
			"feature":            "Fiendish Resilience",
			"current_resistance": currentResistance,
			"available":          currentResistance == "",
			"valid_damage_types": damageTypeList,
			"excluded_types":     []string{"force"},
			"can_change":         "After a short or long rest",
			"how_to_use":         "POST /api/characters/fiendish-resilience with character_id and damage_type",
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
		DamageType  string `json:"damage_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json", "message": publicError(err)})
		return
	}

	// Validate damage type
	damageTypeLower := strings.ToLower(strings.TrimSpace(req.DamageType))
	if !validDamageTypes[damageTypeLower] {
		validList := []string{"acid", "cold", "fire", "lightning", "poison", "thunder", "necrotic", "psychic", "radiant", "bludgeoning", "piercing", "slashing"}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":              "invalid_damage_type",
			"message":            fmt.Sprintf("'%s' is not a valid damage type for Fiendish Resilience", req.DamageType),
			"valid_damage_types": validList,
			"excluded_types":     []string{"force"},
			"note":               "Force is excluded",
		})
		return
	}

	// Verify ownership and get character info
	var ownerID int
	var class, charName string
	var level int
	var subclassNull, fiendishResNull sql.NullString
	var campaignID sql.NullInt64
	err = db.QueryRow(`
		SELECT agent_id, class, name, level, subclass, fiendish_resilience, lobby_id
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &class, &charName, &level, &subclassNull, &fiendishResNull, &campaignID)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Character %d not found", req.CharacterID),
		})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_owner",
			"message": "You can only set Fiendish Resilience for your own characters",
		})
		return
	}

	subclass := ""
	if subclassNull.Valid {
		subclass = subclassNull.String
	}

	// Validate class and subclass
	if strings.ToLower(class) != "warlock" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_warlock",
			"message": fmt.Sprintf("%s is a %s, not a Warlock. Fiendish Resilience is a Fiend Warlock feature.", charName, class),
		})
		return
	}

	if strings.ToLower(subclass) != "fiend" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "wrong_patron",
			"message": fmt.Sprintf("%s has the %s patron, not the Fiend. Fiendish Resilience is a Fiend patron feature.", charName, subclass),
		})
		return
	}

	if level < 10 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "level_requirement",
			"message": fmt.Sprintf("%s is level %d. Fiendish Resilience requires Fiend Warlock level 10+.", charName, level),
		})
		return
	}

	previousResistance := ""
	if fiendishResNull.Valid {
		previousResistance = fiendishResNull.String
	}

	// Update character
	_, err = db.Exec(`UPDATE characters SET fiendish_resilience = $1 WHERE id = $2`, damageTypeLower, req.CharacterID)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "update_failed",
			"message": "Failed to update Fiendish Resilience",
		})
		return
	}

	// Log action if in campaign
	if campaignID.Valid {
		var actionLog string
		if previousResistance == "" {
			actionLog = fmt.Sprintf("🔥 %s attunes their Fiendish Resilience to %s damage, gaining resistance", charName, damageTypeLower)
		} else {
			actionLog = fmt.Sprintf("🔥 %s shifts their Fiendish Resilience from %s to %s damage", charName, previousResistance, damageTypeLower)
		}
		db.Exec(`INSERT INTO actions (campaign_id, character_id, action_type, description, created_at)
			VALUES ($1, $2, 'other', $3, NOW())`, campaignID.Int64, req.CharacterID, actionLog)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":             true,
		"feature":             "Fiendish Resilience",
		"class":               "Warlock",
		"subclass":            "Fiend",
		"character":           charName,
		"damage_type":         damageTypeLower,
		"previous_resistance": previousResistance,
		"effect":              fmt.Sprintf("%s now has resistance to %s damage", charName, damageTypeLower),
		"can_change":          "After a short or long rest",
	})
}

// handleCharacterMysticArcanum handles Warlock Mystic Arcanum spell selection and casting
// @Summary Warlock Mystic Arcanum
// @Description Choose 6th-9th level spells that can be cast once per long rest. Warlocks gain arcanum at levels 11, 13, 15, and 17.
// @Tags Characters
// @Accept json
// @Produce json
// @Param character_id query int false "Character ID (for GET)"
// @Param body body object{character_id=int,spell_level=int,spell_slug=string} false "Body for POST"
// @Success 200 {object} object
// @Router /characters/mystic-arcanum [get]
// @Router /characters/mystic-arcanum [post]
func handleCharacterMysticArcanum(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			// Return info about the feature
			json.NewEncoder(w).Encode(map[string]interface{}{
				"feature":     "Mystic Arcanum",
				"class":       "Warlock",
				"description": "At 11th level, your patron bestows upon you a magical secret called an arcanum. Choose one 6th-level spell from the warlock spell list as this arcanum. You can cast your arcanum spell once without expending a spell slot. You must finish a long rest before you can do so again. At higher levels, you gain more warlock spells of your choice that can be cast this way.",
				"mechanics": map[string]interface{}{
					"level_11": "Choose one 6th-level warlock spell",
					"level_13": "Choose one 7th-level warlock spell",
					"level_15": "Choose one 8th-level warlock spell",
					"level_17": "Choose one 9th-level warlock spell",
				},
				"notes": []string{
					"Each arcanum can be cast once per long rest without a spell slot",
					"You can change an arcanum when you gain a level in Warlock",
					"Arcanum spells don't count against your known spells",
				},
				"usage": "GET /api/characters/mystic-arcanum?character_id=X to view choices, POST to choose a spell",
			})
			return
		}

		// Get character info
		var class, charName string
		var level int
		var arcanumJSON, usedJSON []byte
		err = db.QueryRow(`
			SELECT class, name, level, 
				COALESCE(mystic_arcanum, '{}'),
				COALESCE(mystic_arcanum_used, '[]')
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &charName, &level, &arcanumJSON, &usedJSON)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		if strings.ToLower(class) != "warlock" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "not_warlock",
				"message": fmt.Sprintf("%s is a %s, not a Warlock. Mystic Arcanum is a Warlock feature.", charName, class),
			})
			return
		}

		if level < 11 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":          "level_requirement",
				"message":        fmt.Sprintf("%s is level %d. Mystic Arcanum is available at Warlock level 11.", charName, level),
				"required_level": 11,
				"current_level":  level,
			})
			return
		}

		var currentArcanum map[string]string
		json.Unmarshal(arcanumJSON, &currentArcanum)

		var usedLevels []int
		json.Unmarshal(usedJSON, &usedLevels)

		availableLevels := getAvailableMysticArcanumLevels(level)

		// Build current arcanum info with spell details
		arcanumInfo := []map[string]interface{}{}
		for _, spellLvl := range availableLevels {
			spellLvlStr := strconv.Itoa(spellLvl)
			spellSlug := currentArcanum[spellLvlStr]

			info := map[string]interface{}{
				"spell_level": spellLvl,
				"unlocked_at": getMysticArcanumLevelRequirement(spellLvl),
			}

			if spellSlug != "" {
				// Get spell info
				var spellName, school, castingTime, spellRange, components, duration, description string
				err := db.QueryRow(`
					SELECT name, school, casting_time, range, components, duration, description
					FROM spells WHERE slug = $1
				`, spellSlug).Scan(&spellName, &school, &castingTime, &spellRange, &components, &duration, &description)
				if err == nil {
					info["chosen_spell"] = spellSlug
					info["spell_name"] = spellName
					info["school"] = school
					info["casting_time"] = castingTime
					info["range"] = spellRange
					info["components"] = components
					info["duration"] = duration
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
						info["status"] = "Used (resets on long rest)"
					} else {
						info["status"] = "Available"
					}
				} else {
					info["chosen_spell"] = spellSlug
					info["error"] = "spell_not_found"
				}
			} else {
				info["chosen_spell"] = nil
				info["status"] = "Not yet chosen"
			}

			arcanumInfo = append(arcanumInfo, info)
		}

		// Get available spells for unchosen levels
		unchosenLevels := []int{}
		for _, lvl := range availableLevels {
			if currentArcanum[strconv.Itoa(lvl)] == "" {
				unchosenLevels = append(unchosenLevels, lvl)
			}
		}

		availableSpells := map[string][]map[string]string{}
		for _, spellLvl := range unchosenLevels {
			// Query warlock spells of this level
			rows, err := db.Query(`
				SELECT s.slug, s.name, s.school
				FROM spells s
				JOIN class_spells cs ON s.slug = cs.spell_slug
				WHERE cs.class_slug = 'warlock' AND s.level = $1
				ORDER BY s.name
			`, spellLvl)
			if err == nil {
				defer rows.Close()
				spells := []map[string]string{}
				for rows.Next() {
					var slug, name, school string
					rows.Scan(&slug, &name, &school)
					spells = append(spells, map[string]string{"slug": slug, "name": name, "school": school})
				}
				availableSpells[strconv.Itoa(spellLvl)] = spells
			}
		}

		response := map[string]interface{}{
			"character_id":    charID,
			"character":       charName,
			"class":           class,
			"level":           level,
			"feature":         "Mystic Arcanum",
			"arcanum":         arcanumInfo,
			"unchosen_levels": unchosenLevels,
			"how_to_choose":   "POST /api/characters/mystic-arcanum with character_id, spell_level, and spell_slug",
		}

		if len(availableSpells) > 0 {
			response["available_spells"] = availableSpells
		}

		json.NewEncoder(w).Encode(response)
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
		SpellLevel  int    `json:"spell_level"`
		SpellSlug   string `json:"spell_slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json", "message": publicError(err)})
		return
	}

	if req.CharacterID == 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_id_required",
			"message": "Provide character_id",
		})
		return
	}

	if req.SpellLevel < 6 || req.SpellLevel > 9 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "invalid_spell_level",
			"message":      "Mystic Arcanum spells must be 6th-9th level",
			"valid_levels": []int{6, 7, 8, 9},
		})
		return
	}

	// Verify ownership and get character info
	var ownerID int
	var class, charName string
	var level int
	var arcanumJSON []byte
	var campaignID sql.NullInt64
	err = db.QueryRow(`
		SELECT agent_id, class, name, level, COALESCE(mystic_arcanum, '{}'), lobby_id
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &class, &charName, &level, &arcanumJSON, &campaignID)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Character %d not found", req.CharacterID),
		})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_owner",
			"message": "You can only set Mystic Arcanum for your own characters",
		})
		return
	}

	if strings.ToLower(class) != "warlock" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_warlock",
			"message": fmt.Sprintf("%s is a %s, not a Warlock. Mystic Arcanum is a Warlock feature.", charName, class),
		})
		return
	}

	// Check level requirement for this spell level
	requiredLevel := getMysticArcanumLevelRequirement(req.SpellLevel)
	if level < requiredLevel {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":          "level_requirement",
			"message":        fmt.Sprintf("%s is level %d. A %dth-level Mystic Arcanum requires Warlock level %d.", charName, level, req.SpellLevel, requiredLevel),
			"required_level": requiredLevel,
			"current_level":  level,
		})
		return
	}

	// Verify spell exists and is on warlock list at the correct level
	var spellName, school string
	var spellLevelDB int
	err = db.QueryRow(`
		SELECT s.name, s.level, s.school
		FROM spells s
		JOIN class_spells cs ON s.slug = cs.spell_slug
		WHERE s.slug = $1 AND cs.class_slug = 'warlock'
	`, req.SpellSlug).Scan(&spellName, &spellLevelDB, &school)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "spell_not_found",
			"message": fmt.Sprintf("'%s' is not a valid warlock spell", req.SpellSlug),
		})
		return
	}

	if spellLevelDB != req.SpellLevel {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "wrong_spell_level",
			"message": fmt.Sprintf("%s is a %dth-level spell, not %dth-level", spellName, spellLevelDB, req.SpellLevel),
		})
		return
	}

	// Update the arcanum
	var currentArcanum map[string]string
	json.Unmarshal(arcanumJSON, &currentArcanum)

	oldSpell := currentArcanum[strconv.Itoa(req.SpellLevel)]
	currentArcanum[strconv.Itoa(req.SpellLevel)] = req.SpellSlug

	arcanumJSONNew, _ := json.Marshal(currentArcanum)

	_, err = db.Exec("UPDATE characters SET mystic_arcanum = $1 WHERE id = $2", arcanumJSONNew, req.CharacterID)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "update_failed",
			"message": "Failed to update Mystic Arcanum",
		})
		return
	}

	// Log action if in campaign
	if campaignID.Valid {
		actionText := fmt.Sprintf("✨ %s chooses %s as their %dth-level Mystic Arcanum", charName, spellName, req.SpellLevel)
		if oldSpell != "" {
			actionText = fmt.Sprintf("✨ %s changes their %dth-level Mystic Arcanum to %s", charName, req.SpellLevel, spellName)
		}
		db.Exec(`INSERT INTO actions (campaign_id, character_id, action_type, description, created_at)
			VALUES ($1, $2, 'other', $3, NOW())`,
			campaignID.Int64, req.CharacterID, actionText)
	}

	response := map[string]interface{}{
		"success":     true,
		"feature":     "Mystic Arcanum",
		"character":   charName,
		"spell_level": req.SpellLevel,
		"spell":       req.SpellSlug,
		"spell_name":  spellName,
		"school":      school,
		"note":        fmt.Sprintf("You can cast %s once without expending a spell slot. Resets on long rest.", spellName),
	}

	if oldSpell != "" {
		response["replaced"] = oldSpell
	}

	json.NewEncoder(w).Encode(response)
}

// handleCharacterSignatureSpells handles the Wizard level 20 Signature Spells feature
// @Summary Manage Signature Spells (Wizard level 20)
// @Description Choose 2 3rd-level wizard spells. Always prepared, cast each once at 3rd level without slot. Resets on long rest.
// @Tags Characters
// @Accept json
// @Produce json
// @Param character_id query int false "Character ID (GET)"
// @Param body body object{character_id=int,action=string,spell=string} false "Action: choose/cast"
// @Success 200 {object} object "Signature Spells result"
// @Failure 400 {object} object "Not a level 20 Wizard or invalid spell"
// @Security BasicAuth
// @Router /characters/signature-spells [get]
// @Router /characters/signature-spells [post]
func handleCharacterSignatureSpells(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		charIDStr := r.URL.Query().Get("character_id")
		charID, err := strconv.Atoi(charIDStr)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_id_required",
				"message": "Provide character_id to check Signature Spells status",
				"usage":   "GET /api/characters/signature-spells?character_id=X",
			})
			return
		}

		var class string
		var level int
		var signatureSpellsJSON, signatureSpellsUsedJSON []byte
		err = db.QueryRow(`
			SELECT class, level, COALESCE(signature_spells, '[]'), COALESCE(signature_spells_used, '[]')
			FROM characters WHERE id = $1
		`, charID).Scan(&class, &level, &signatureSpellsJSON, &signatureSpellsUsedJSON)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "character_not_found",
				"message": fmt.Sprintf("Character %d not found", charID),
			})
			return
		}

		// Check Wizard level (multiclass support)
		wizardLevel := getWizardLevel(charID)
		if wizardLevel < 20 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":         "level_requirement",
				"message":       fmt.Sprintf("Signature Spells requires Wizard level 20 (current: %d)", wizardLevel),
				"wizard_level":  wizardLevel,
				"class_feature": "Signature Spells",
			})
			return
		}

		var signatureSpells []string
		var signatureSpellsUsed []string
		json.Unmarshal(signatureSpellsJSON, &signatureSpells)
		json.Unmarshal(signatureSpellsUsedJSON, &signatureSpellsUsed)

		// Get available 3rd level wizard spells if they need to choose
		availableSpells := []map[string]interface{}{}
		if len(signatureSpells) < 2 {
			rows, _ := db.Query(`
				SELECT s.slug, s.name 
				FROM spells s
				JOIN class_spells csl ON s.slug = csl.spell_slug
				WHERE csl.class_slug = 'wizard' AND s.level = 3
				ORDER BY s.name
			`)
			if rows != nil {
				defer rows.Close()
				for rows.Next() {
					var slug, name string
					rows.Scan(&slug, &name)
					availableSpells = append(availableSpells, map[string]interface{}{
						"slug": slug,
						"name": name,
					})
				}
			}
		}

		// Build spell info with availability
		spellInfo := []map[string]interface{}{}
		for _, spell := range signatureSpells {
			used := false
			for _, usedSpell := range signatureSpellsUsed {
				if usedSpell == spell {
					used = true
					break
				}
			}
			spellInfo = append(spellInfo, map[string]interface{}{
				"spell":     spell,
				"used":      used,
				"available": !used,
			})
		}

		response := map[string]interface{}{
			"character_id":     charID,
			"class":            class,
			"level":            level,
			"wizard_level":     wizardLevel,
			"class_feature":    "Signature Spells",
			"spells_chosen":    len(signatureSpells),
			"max_spells":       2,
			"signature_spells": spellInfo,
			"effect":           "Always prepared, don't count against limit. Cast each once at 3rd level without slot.",
			"recharge":         "Long rest (free casts)",
		}

		if len(signatureSpells) < 2 {
			response["needs_choice"] = true
			response["slots_remaining"] = 2 - len(signatureSpells)
			response["available_spells"] = availableSpells
			response["how_to_choose"] = "POST /api/characters/signature-spells with action='choose', spell='spell-slug'"
		} else {
			response["complete"] = true
			response["how_to_cast"] = "POST /api/characters/signature-spells with action='cast', spell='spell-slug'"
		}

		json.NewEncoder(w).Encode(response)
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
		Action      string `json:"action"`      // "choose" or "cast"
		Spell       string `json:"spell"`       // Spell slug
		Description string `json:"description"` // Optional flavor
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json", "message": publicError(err)})
		return
	}

	if req.Action == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "action_required",
			"message": "Specify action: 'choose' to select a signature spell, 'cast' to cast one without a slot",
		})
		return
	}

	// Get character info
	var ownerID int
	var class, charName string
	var level int
	var campaignID sql.NullInt64
	var signatureSpellsJSON, signatureSpellsUsedJSON []byte
	err = db.QueryRow(`
		SELECT agent_id, class, name, level, lobby_id, COALESCE(signature_spells, '[]'), COALESCE(signature_spells_used, '[]')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&ownerID, &class, &charName, &level, &campaignID, &signatureSpellsJSON, &signatureSpellsUsedJSON)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": fmt.Sprintf("Character %d not found", req.CharacterID),
		})
		return
	}

	if ownerID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_owner",
			"message": "You can only manage Signature Spells for your own characters",
		})
		return
	}

	// Check Wizard level (multiclass support)
	wizardLevel := getWizardLevel(req.CharacterID)
	if wizardLevel < 20 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "level_requirement",
			"message":      fmt.Sprintf("%s needs Wizard level 20 for Signature Spells (current: %d)", charName, wizardLevel),
			"wizard_level": wizardLevel,
		})
		return
	}

	var signatureSpells []string
	var signatureSpellsUsed []string
	json.Unmarshal(signatureSpellsJSON, &signatureSpells)
	json.Unmarshal(signatureSpellsUsedJSON, &signatureSpellsUsed)

	switch req.Action {
	case "choose":
		if len(signatureSpells) >= 2 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":            "already_chosen",
				"message":          fmt.Sprintf("%s has already chosen 2 signature spells", charName),
				"signature_spells": signatureSpells,
				"tip":              "Signature spells are permanent choices",
			})
			return
		}

		// Check spell is a valid 3rd level wizard spell
		var spellName string
		var spellLevel int
		err := db.QueryRow(`
			SELECT s.name, s.level FROM spells s
			JOIN class_spells csl ON s.slug = csl.spell_slug
			WHERE s.slug = $1 AND csl.class_slug = 'wizard'
		`, req.Spell).Scan(&spellName, &spellLevel)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_spell",
				"message": fmt.Sprintf("'%s' is not a valid wizard spell", req.Spell),
			})
			return
		}

		if spellLevel != 3 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "wrong_level",
				"message": fmt.Sprintf("Signature spells must be 3rd level (%s is level %d)", spellName, spellLevel),
			})
			return
		}

		// Check not already chosen
		for _, existing := range signatureSpells {
			if existing == req.Spell {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "already_selected",
					"message": fmt.Sprintf("%s is already a signature spell", spellName),
				})
				return
			}
		}

		// Add the spell
		signatureSpells = append(signatureSpells, req.Spell)
		updatedJSON, _ := json.Marshal(signatureSpells)
		db.Exec(`UPDATE characters SET signature_spells = $1 WHERE id = $2`, updatedJSON, req.CharacterID)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":          true,
			"action":           "choose",
			"character":        charName,
			"spell_chosen":     spellName,
			"spell_slug":       req.Spell,
			"signature_spells": signatureSpells,
			"slots_remaining":  2 - len(signatureSpells),
			"note":             fmt.Sprintf("%s is now a signature spell (always prepared, can cast once at 3rd level without slot)", spellName),
		})

	case "cast":
		// Check spell is one of their signature spells
		isSignature := false
		for _, spell := range signatureSpells {
			if spell == req.Spell {
				isSignature = true
				break
			}
		}

		if !isSignature {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":            "not_signature_spell",
				"message":          fmt.Sprintf("'%s' is not one of %s's signature spells", req.Spell, charName),
				"signature_spells": signatureSpells,
			})
			return
		}

		// Check if already used this rest
		for _, used := range signatureSpellsUsed {
			if used == req.Spell {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "already_cast",
					"message": fmt.Sprintf("%s has already cast %s for free since their last long rest", charName, req.Spell),
					"tip":     "You can still cast this spell using a spell slot, or take a long rest to use it for free again",
				})
				return
			}
		}

		// Get spell info for response
		var spellName string
		db.QueryRow(`SELECT name FROM spells WHERE slug = $1`, req.Spell).Scan(&spellName)

		// Mark as used
		signatureSpellsUsed = append(signatureSpellsUsed, req.Spell)
		updatedJSON, _ := json.Marshal(signatureSpellsUsed)
		db.Exec(`UPDATE characters SET signature_spells_used = $1 WHERE id = $2`, updatedJSON, req.CharacterID)

		// Log action if in campaign
		if campaignID.Valid {
			desc := req.Description
			if desc == "" {
				desc = fmt.Sprintf("%s casts %s (signature spell)", charName, spellName)
			}
			actionLog := fmt.Sprintf("📜 Signature Spell — %s (3rd level, no slot)", desc)
			db.Exec(`INSERT INTO actions (campaign_id, character_id, action_type, description, created_at)
				VALUES ($1, $2, 'cast', $3, NOW())`, campaignID.Int64, req.CharacterID, actionLog)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":              true,
			"action":               "cast",
			"character":            charName,
			"spell":                spellName,
			"spell_slug":           req.Spell,
			"spell_level":          3,
			"slot_used":            false,
			"description":          fmt.Sprintf("%s casts %s using mastery over this signature spell.", charName, spellName),
			"note":                 "Cast at 3rd level without expending a spell slot",
			"free_casts_remaining": 2 - len(signatureSpellsUsed),
			"tip":                  "Apply the spell effect using /api/action or /api/gm/aoe-cast as appropriate",
		})

	default:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_action",
			"message": fmt.Sprintf("Unknown action '%s'. Use 'choose' or 'cast'", req.Action),
		})
	}
}

// handleUniverseFightingStyles returns all available fighting styles
// @Summary List all fighting styles
// @Description Returns the SRD 5.2.1 Fighting Style options
// @Tags Universe
// @Produce json
// @Success 200 {object} object{fighting_styles=[]FightingStyle}
// @Router /universe/fighting-styles [get]
func handleUniverseFightingStyles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	styles := []FightingStyle{}
	slugs := []string{}
	for slug := range fightingStyles {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		styles = append(styles, fightingStyles[slug])
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"fighting_styles": styles,
		"class_availability": map[string][]string{
			"fighter": {"archery", "defense", "great_weapon_fighting", "two_weapon_fighting"},
			"paladin": {"archery", "defense", "great_weapon_fighting", "two_weapon_fighting", "blessed_warrior"},
			"ranger":  {"archery", "defense", "great_weapon_fighting", "two_weapon_fighting", "druidic_warrior"},
		},
		"levels": map[string]int{
			"fighter": 1,
			"paladin": 2,
			"ranger":  2,
		},
		"note": "Champion Fighters gain an Additional Fighting Style at level 7. A Fighter can replace a Fighting Style feat whenever they gain a Fighter level (send replace with the old slug).",
	})
}

// @Description Returns all 8 SRD Metamagic options available to Sorcerers
// @Tags Universe
// @Produce json
// @Success 200 {object} object{metamagic=[]MetamagicOption}
// @Router /universe/metamagic [get]
func handleUniverseMetamagic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	options := []MetamagicOption{}
	slugs := []string{}
	for slug := range metamagicOptions {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		options = append(options, metamagicOptions[slug])
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"metamagic": options,
		"note":      "Sorcerers choose 2 at level 3, +1 at levels 10 and 17",
		"usage":     "Include metamagic keyword in spell description, e.g., 'quickened fireball', 'twinned healing word'",
	})
}
