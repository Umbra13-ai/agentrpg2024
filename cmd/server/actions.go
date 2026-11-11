package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// isCharacterInCombat checks if a character is currently in an active combat
// Returns campaign ID, in-combat status, and any error
func isCharacterInCombat(characterID int) (campaignID int, inCombat bool, err error) {
	// Get character's campaign
	var campID sql.NullInt64
	err = db.QueryRow(`SELECT lobby_id FROM characters WHERE id = $1`, characterID).Scan(&campID)
	if err != nil {
		return 0, false, err
	}
	if !campID.Valid {
		return 0, false, nil // Not in a campaign
	}

	// Check if campaign is in combat (has turn_order set)
	var turnOrder sql.NullString
	err = db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1`, campID.Int64).Scan(&turnOrder)
	if err == sql.ErrNoRows {
		return int(campID.Int64), false, nil // No combat state = exploration mode
	}
	if err != nil {
		return int(campID.Int64), false, err
	}

	// Combat is active if turn_order is not empty
	inCombat = turnOrder.Valid && turnOrder.String != "" && turnOrder.String != "[]"
	return int(campID.Int64), inCombat, nil
}

// parseTargetFromDescription tries to find a character ID from action description
// Looks for character names in the description (e.g., "attack goblin" → finds goblin's ID)
func parseTargetFromDescription(description string, attackerID int) int {
	// Get the lobby ID for this attacker
	var lobbyID int
	err := db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1", attackerID).Scan(&lobbyID)
	if err != nil || lobbyID == 0 {
		return 0
	}

	// Get all characters in the same lobby
	rows, err := db.Query("SELECT id, name FROM characters WHERE lobby_id = $1 AND id != $2", lobbyID, attackerID)
	if err != nil {
		return 0
	}
	defer rows.Close()

	descLower := strings.ToLower(description)

	for rows.Next() {
		var id int
		var name string
		rows.Scan(&id, &name)

		// Check if character name appears in description
		if strings.Contains(descLower, strings.ToLower(name)) {
			return id
		}
	}

	// Also check monster/NPC names from campaign document
	// For now, return 0 if no character match found
	return 0
}

// parseTargetsFromDescription finds every character a description names (F229): "bless Aldren Ironward, Millie and Calla
// Underbough" gives all three, in the order they're written. A character counts when its full name or its first name (a whole
// word) appears; "myself", "me" and "self" name the caster. Used by spells that take several creatures (Bless, Aid).
func parseTargetsFromDescription(description string, casterID int) []int {
	var lobbyID int
	if db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1", casterID).Scan(&lobbyID) != nil || lobbyID == 0 {
		return nil
	}
	rows, err := db.Query("SELECT id, name FROM characters WHERE lobby_id = $1", lobbyID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	dl := " " + strings.ToLower(description) + " "
	wordAt := func(w string) int {
		for from := 0; ; {
			i := strings.Index(dl[from:], w)
			if i < 0 {
				return -1
			}
			i += from
			before, after := dl[i-1], byte(' ')
			if i+len(w) < len(dl) {
				after = dl[i+len(w)]
			}
			isLetter := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
			if !isLetter(before) && !isLetter(after) {
				return i
			}
			from = i + 1
		}
	}
	type hit struct{ id, at int }
	type person struct {
		id   int
		name string
	}
	var people []person
	firsts := map[string]int{} // a first name two characters share names neither
	for rows.Next() {
		var p person
		rows.Scan(&p.id, &p.name)
		p.name = strings.ToLower(strings.TrimSpace(p.name))
		if p.name != "" {
			people = append(people, p)
			firsts[strings.Fields(p.name)[0]]++
		}
	}
	var hits []hit
	for _, p := range people {
		id, n := p.id, p.name
		at := wordAt(n)
		if f := strings.Fields(n); at < 0 && len(f) > 1 && len(f[0]) >= 3 && firsts[f[0]] == 1 {
			at = wordAt(f[0])
		}
		if at < 0 && id == casterID {
			for _, w := range []string{"myself", "me", "self"} {
				if a := wordAt(w); a >= 0 && (at < 0 || a < at) {
					at = a
				}
			}
		}
		if at >= 0 {
			hits = append(hits, hit{id, at})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var out []int
	for _, h := range hits {
		out = append(out, h.id)
	}
	return out
}

// Action Economy: which resource each action type consumes
// Returns: "action", "bonus_action", "reaction", "movement", "free", or "none"
func getActionResourceType(actionType string) string {
	actionType = strings.ToLower(actionType)
	switch actionType {
	// Standard actions (consume your action)
	case "attack", "cast", "dash", "disengage", "dodge", "help", "hide", "ready", "search", "study", "influence", "extinguish", "natures_sanctuary", "use_item", "stabilize", "death_save", "grapple", "shove":
		return "action"
	// Bonus actions (consume bonus action - class/spell specific)
	case "drink_potion", "item_bonus", "bonus_attack", "martial_arts", "move_sanctuary", "cunning_action", "offhand_attack", "second_wind", "steady_aim", "lay_on_hands", "natures_veil", "innate_sorcery", "divine_sense", "wild_shape", "revert_wild_shape", "dragon_wings", "bardic_inspiration", "extend_rage", "rage", "bonus_cast", "frenzy_attack", "merge_with_shadows", "flurry_of_blows", "patient_defense", "step_of_the_wind", "gaze_of_two_minds":
		return "bonus_action"
	// Reactions (consume reaction - used on others' turns too)
	case "opportunity_attack", "counterspell", "shield", "countercharm", "reaction_cast":
		return "reaction"
	// Movement (consumes movement speed) - includes stand (costs half movement)
	case "move", "stand":
		return "movement"
	// Free actions (no resource cost)
	case "gift_of_the_protectors", "jump", "drop", "speak", "interact", "other", "frenzy", "stunning_strike", "action_surge", "wild_resurgence", "use_bardic_inspiration", "improve_fate", "concentration_check", "blink_steps", "natural_recovery", "nature_magician", "self_restoration", "disciplined_survivor", "superior_defense", "quivering_palm", "sacred_weapon": // 2024 Action Surge takes no action of its own
		return "free"
	default:
		// Default unknown actions to using the action
		return "action"
	}
}

// Check if character has the required resource for an action
// Returns: canAct bool, resourceType string, errorMsg string
func checkActionEconomy(charID int, actionType string, movementCost int) (bool, string, string) {
	resourceType := getActionResourceType(actionType)

	var actionUsed, bonusActionUsed, reactionUsed bool
	var movementRemaining int
	var species string
	var attacksRemaining sql.NullInt32
	err := db.QueryRow(`
		SELECT COALESCE(action_used, false), COALESCE(bonus_action_used, false), 
		       COALESCE(reaction_used, false), COALESCE(movement_remaining, 30), COALESCE(species, 'human'),
		       attacks_remaining
		FROM characters WHERE id = $1
	`, charID).Scan(&actionUsed, &bonusActionUsed, &reactionUsed, &movementRemaining, &species, &attacksRemaining)

	if err != nil {
		return false, resourceType, "Failed to check action economy"
	}

	switch resourceType {
	case "action":
		// Special handling for Extra Attack
		// If in the middle of an Attack action with attacks remaining, allow more attacks
		if strings.ToLower(actionType) == "attack" && attacksRemaining.Valid && attacksRemaining.Int32 > 0 {
			// Continue the attack action - attacks remaining
			return true, resourceType, ""
		}
		if actionUsed {
			return false, resourceType, "You have already used your action this turn. Available: bonus action, movement, free actions."
		}
	case "bonus_action":
		if bonusActionUsed {
			return false, resourceType, "You have already used your bonus action this turn."
		}
	case "reaction":
		if reactionUsed {
			return false, resourceType, "You have already used your reaction this round."
		}
	case "movement":
		actionType = strings.ToLower(actionType)

		// Standing up from prone costs half your movement speed (5e)
		if actionType == "stand" {
			// Check if actually prone
			conditions := getCharConditions(charID)
			isProne := false
			for _, c := range conditions {
				if strings.ToLower(c) == "prone" {
					isProne = true
					break
				}
			}
			if !isProne {
				return false, resourceType, "You are not prone. No need to stand up."
			}

			// Standing costs half movement speed
			baseSpeed := characterSpeed(charID, species)
			standCost := baseSpeed / 2
			if standCost > movementRemaining {
				return false, resourceType, fmt.Sprintf("Standing up costs half your speed (%dft). You only have %dft remaining.", standCost, movementRemaining)
			}
		} else if actionType == "move" {
			// Prone movement: crawling costs 2ft per 1ft moved (5e)
			conditions := getCharConditions(charID)
			isProne := false
			for _, c := range conditions {
				if strings.ToLower(c) == "prone" {
					isProne = true
					break
				}
			}

			effectiveCost := movementCost
			if isProne {
				effectiveCost = movementCost * 2 // Crawling costs double
			}

			if effectiveCost > movementRemaining {
				if isProne {
					return false, resourceType, fmt.Sprintf("Not enough movement. Crawling while prone costs 2ft per 1ft. You need %dft (2x%dft) but only have %dft remaining. Consider using 'stand' action first (costs %dft).", effectiveCost, movementCost, movementRemaining, getMovementSpeed(species)/2)
				}
				return false, resourceType, fmt.Sprintf("Not enough movement. You have %dft remaining, need %dft.", movementRemaining, movementCost)
			}
		} else {
			// Generic movement check
			if movementCost > movementRemaining {
				return false, resourceType, fmt.Sprintf("Not enough movement. You have %dft remaining, need %dft.", movementRemaining, movementCost)
			}
		}
	case "free":
		// Free actions always succeed
		return true, resourceType, ""
	}

	return true, resourceType, ""
}

func spendDashAction(charID, speed int) error {
	var remaining int
	return db.QueryRow(`
		UPDATE characters
		SET action_used = true, movement_remaining = COALESCE(movement_remaining, 30) + $1
		WHERE id = $2 AND NOT COALESCE(action_used, false)
		RETURNING movement_remaining
	`, speed, charID).Scan(&remaining)
}

// fleet: Fleet Step, after another Bonus Action (the Bonus Action is already spent).
func spendBonusDash(charID, speed, kiLimit int, fleet ...bool) error {
	var remaining int
	ignoreBonus := len(fleet) > 0 && fleet[0]
	return db.QueryRow(`
		UPDATE characters
		SET bonus_action_used = true,
		    movement_remaining = COALESCE(movement_remaining, 30) + $1,
		    class_resources_used = CASE WHEN $3 > 0 THEN
		        jsonb_set(COALESCE(class_resources_used, '{}'), '{focus}',
		            to_jsonb(COALESCE((class_resources_used->>'focus')::int, 0) + 1))
		        ELSE class_resources_used END
		WHERE id = $2 AND ($4 OR NOT COALESCE(bonus_action_used, false))
		  AND ($3 = 0 OR COALESCE((class_resources_used->>'focus')::int, 0) < $3)
		RETURNING movement_remaining
	`, speed, charID, kiLimit, ignoreBonus).Scan(&remaining)
}

// Consume the appropriate resource after an action
// actionType is the original action (e.g., "attack", "cast") for Extra Attack handling
func consumeActionResource(charID int, resourceType string, movementCost int, actionType ...string) {
	// One with Shadows invisibility ends when you move, take an action, or use a reaction
	// "invisible until you move or take an action or a reaction"
	switch resourceType {
	case "action":
		removeOneWithShadowsInvisibility(charID)
		// Special handling for Extra Attack
		if len(actionType) > 0 && (strings.ToLower(actionType[0]) == "attack" || strings.ToLower(actionType[0]) == "grapple" || strings.ToLower(actionType[0]) == "shove") {
			consumeAttackAction(charID) // a Grapple or Shove replaces one attack of the Attack action (F180)
		} else {
			db.Exec("UPDATE characters SET action_used = true WHERE id = $1", charID)
		}
	case "bonus_action":
		// Note: Bonus actions don't break One with Shadows per RAW (only action/reaction/movement)
		db.Exec("UPDATE characters SET bonus_action_used = true WHERE id = $1", charID)
	case "reaction":
		removeOneWithShadowsInvisibility(charID)
		db.Exec("UPDATE characters SET reaction_used = true WHERE id = $1", charID)
	case "movement":
		if movementCost > 0 {
			removeOneWithShadowsInvisibility(charID)
		}
		db.Exec("UPDATE characters SET movement_remaining = movement_remaining - $1 WHERE id = $2", movementCost, charID)
	}
}

// consumeAttackAction handles Extra Attack
// - If attacks_remaining is NULL, initialize it based on class/level
// - Decrement attacks_remaining
// - When attacks_remaining hits 0, mark action_used = true
func consumeAttackAction(charID int) {
	var attacksRemaining sql.NullInt32
	var class string
	var level int

	err := db.QueryRow(`
		SELECT attacks_remaining, class, level FROM characters WHERE id = $1
	`, charID).Scan(&attacksRemaining, &class, &level)

	if err != nil {
		// Fallback to simple action consumption
		db.Exec("UPDATE characters SET action_used = true WHERE id = $1", charID)
		return
	}

	if !attacksRemaining.Valid {
		// Starting a new Attack action - initialize attacks based on Extra Attack
		totalAttacks := characterExtraAttacks(charID, class, level) // multiclass: Extra Attack doesn't stack
		if pw := pactWeaponAttacks(charID, class); pw > totalAttacks {
			totalAttacks = pw // Thirsting Blade (2), Devouring Blade (3)
		}
		if totalAttacks <= 1 {
			// No Extra Attack, just mark action as used
			db.Exec("UPDATE characters SET action_used = true, attacks_remaining = 0 WHERE id = $1", charID)
		} else {
			// Has Extra Attack - set remaining attacks (minus this one)
			db.Exec("UPDATE characters SET attacks_remaining = $1 WHERE id = $2", totalAttacks-1, charID)
		}
	} else {
		// Continuing an Attack action - decrement
		newRemaining := attacksRemaining.Int32 - 1
		if newRemaining <= 0 {
			// Last attack - mark action as fully used
			db.Exec("UPDATE characters SET action_used = true, attacks_remaining = 0 WHERE id = $1", charID)
		} else {
			// More attacks available
			db.Exec("UPDATE characters SET attacks_remaining = $1 WHERE id = $2", newRemaining, charID)
		}
	}
}

// actionNarrative normalizes the current description field and the legacy
// result field used by earlier action clients. A supplied description wins so
// the API remains predictable when clients send both fields.
func actionNarrative(description, legacyResult string) string {
	if description = strings.TrimSpace(description); description != "" {
		return description
	}
	return strings.TrimSpace(legacyResult)
}

// actionRequiresNarrative identifies actions that have no mechanical default
// and must not silently consume a turn with an empty action log.
func actionRequiresNarrative(action string) bool {
	return strings.ToLower(strings.TrimSpace(action)) == "other"
}

// handleAction godoc
// @Summary Submit an action
// @Description Submit a game action. Server resolves mechanics (dice rolls, damage, etc.). Enforces action economy: 1 action, 1 bonus action, 1 reaction per round, movement in feet. Every action type, with what it costs, is listed on /commands. Companions: send companion_id with attack, dodge, dash, disengage, help, hide, search or move; a Draconic Spirit also takes "breath" (target_ids, type in the description), an Otherworldly Steed "healing_touch", "fey_step" or "fell_glare" by its kind. Feature words: GET /api/my-turn lists the character's own under your_options.feature_words; send them as "features": [...] (checked against that list) or in the description as whole words; Advantage can't be claimed in a description (the GM grants it: POST /api/gm/grant-advantage). Feature words in a cast description: "divine intervention" (Cleric 10; Wish at 20), "free", "wild companion", "overchannel", "delivered by my familiar". Wild Shape needs known forms first (POST /api/characters/{id}/class-options {"wild_shape_forms": [...]}). use_item with "item" resolves potions and these Legendary items: Horn of Valhalla, Ring of Djinni Summoning, Iron Flask (target_id, or "release"), Sphere of Annihilation ("control"; "move" with target_ids), Mysterious Deck ("draw N"; "star: wisdom" and the other choices), Rod of Lordly Might, Staff of the Magi, Ring of Elemental Command, Armor of Invulnerability, Plate Armor of Etherealness, Luck Blade, Sovereign Glue, Universal Solvent, Well of Many Worlds, Apparatus of the Crab ("lever 1 up") (skill.md lists the words). Spells' own actions: spiritual_weapon (Bonus Action, target_id), flame_blade (Magic action, target_id; "evoke", "let go"), gaseous_form ("end"). opportunity_attack with spell_slug: War Caster's Reactive Spell. A spell's gestures need a free hand unless the campaign's spell_hands table rule is "ignored" or the caster has War Caster. At 0 Hit Points only death_save is allowed (unconscious_at_0_hp).
// @Tags Actions
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{action=string,description=string,spell_slug=string,slot_level=int,item=string,charges=int,target_id=int,target_ids=[]int,target=string,target_character_id=int,movement_cost=int,toward_frightened_source=bool,character_id=int,campaign_id=int} true "Action details (character_id or campaign_id: which character acts, for a player in several campaigns; a cast needs a spell the character can cast: prepared, a cantrip, or given by a feature, its origin or an item; item: cast from a magic item or read a Spell Scroll, with charges for a higher version where the item allows it; use_item with item drinks a potion, a Bonus Action, or gives it to target_character_id)"
// @Success 200 {object} map[string]interface{} "Action result with dice rolls"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "No active game or resource exhausted"
// @Router /action [post]
func handleAction(w http.ResponseWriter, r *http.Request) {
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
		Action                 string `json:"action"`
		Description            string `json:"description"`
		Result                 string `json:"result"`       // Backward-compatible narrative alias used by earlier action clients.
		SpellSlug              string `json:"spell_slug"`   // Exact spell slug for cast actions; takes precedence over description.
		CharacterID            int    `json:"character_id"` // Optional: which of your characters acts (when you play in several campaigns)
		CampaignID             int    `json:"campaign_id"`  // Optional: the campaign you act in (when you play in several)
		Target                 string `json:"target"`
		TargetCharacterID      int    `json:"target_character_id"`      // Explicit party member for stabilize.
		MovementCost           int    `json:"movement_cost"`            // feet of movement for move actions
		TowardFrightenedSource bool   `json:"toward_frightened_source"` // set true if moving toward source of fear (blocks movement)
		CloseRange             bool   `json:"close_range"`              // set true if within 5ft of hostile creature (ranged attacks have disadvantage)
		SlotLevel              int    `json:"slot_level"`               // cast: the spell slot level to use (upcasting); 0 means the spell's own level
		ObjectID               int    `json:"object_id"`                // attack: an object the GM created (POST /api/gm/object), such as a door
		CleaveTargetID         int    `json:"cleave_target_id"`         // attack with a Cleave weapon: a second creature within 5 feet of target_id
		TargetIDs              []int  `json:"target_ids"`               // cast: more creatures hit by an area or multi-target spell (monsters have negative ids; characters are healed)

		Item    string `json:"item"`    // cast: the magic item or Spell Scroll to cast from; use_item: the item (a potion ...)
		Charges int    `json:"charges"` // cast from an item: the charges to spend (a higher version where the item allows it)

		TargetID int `json:"target_id"` // attack: the id of an enemy (see situation.enemies in GET /api/my-turn); the server then decides hit or miss

		CompanionID int `json:"companion_id"` // a companion of yours acts instead of you (companions_2024.go): attack, dodge, dash, disengage, help, hide, search, move

		Features []string `json:"features"` // feature words for this action ("sculpt", "stunning strike", "cunning strike trip"): your_options.feature_words in GET /api/my-turn
	}
	json.NewDecoder(r.Body).Decode(&req)
	// An explicit slot_level upcasts the spell; the casting code reads "at level N" from the description.
	req.Action = canonicalAction(req.Action) // 2024 names: Magic is cast, Utilize is use_item
	if strings.EqualFold(req.Action, "cast") && req.SlotLevel > 0 {
		if req.SlotLevel > 9 {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_slot_level", "message": "slot_level must be 1 to 9"})
			return
		}
		req.Description = strings.TrimSpace(req.Description) + fmt.Sprintf(" (cast at level %d)", req.SlotLevel)
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Description = actionNarrative(req.Description, req.Result)
	// F245: a spell's name sent as the action ("fireball") was recorded as narration; say how to cast it, nothing spent
	if slug := normalizeSpellSlug(strings.ReplaceAll(req.Action, "_", "-")); req.Action != "cast" && getActionResourceType(req.Action) == "action" && !knownActionWord(req.Action) {
		if sp, isSpell := srdSpellsMemory[slug]; isSpell {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "spell_as_action",
				"message": fmt.Sprintf("%s is a spell. Cast it with {\"action\": \"cast\", \"spell_slug\": \"%s\", \"target_id\": ...}. Nothing was spent.", sp.Name, slug)})
			return
		}
	}
	if req.Action == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "action_required",
			"message": "Provide an action, such as attack, dodge, cast, or help.",
		})
		return
	}
	if actionRequiresNarrative(req.Action) && req.Description == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "description_required",
			"message": "Provide a non-empty description for an other action; no action was taken.",
			"hint":    "Use description (or the legacy result field) for your narrative.",
		})
		return
	}

	// A cast must identify one spell before we inspect turn state or consume an
	// action. Descriptions remain useful for targets and narration, but a
	// supplied spell_slug is the authoritative mechanics input. This prevents a
	// partial or ambiguous phrase from silently becoming a different spell and
	// costing the player a turn.
	if req.Action == "cast" {
		spellSlug, candidates, selectionErr := resolveCastSpellSlug(req.SpellSlug, req.Description)
		if selectionErr != "" {
			w.WriteHeader(http.StatusBadRequest)
			response := map[string]interface{}{
				"success": false,
				"error":   selectionErr,
				"hint":    "Set spell_slug to one exact slug from GET /api/universe/spells; keep description for target and roleplay.",
			}
			switch selectionErr {
			case "unknown_spell_slug":
				response["message"] = fmt.Sprintf("Unknown spell_slug %q. Use an exact slug from GET /api/universe/spells.", req.SpellSlug)
			case "ambiguous_spell":
				response["message"] = "Your description names more than one spell. Choose one with spell_slug; no action was taken."
				response["spell_candidates"] = candidates
			case "spell_slug_required":
				response["message"] = fmt.Sprintf("Your description suggests %q. Resubmit with that exact spell_slug; no action was taken.", candidates[0])
				response["spell_candidates"] = candidates
			default:
				response["message"] = "Could not identify a spell from the description. Provide spell_slug; no action was taken."
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		req.SpellSlug = spellSlug
	}

	var charID, lobbyID int
	var species string
	err = db.QueryRow(`
		SELECT c.id, c.lobby_id, c.species FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.agent_id = $1 AND l.status = 'active' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
		ORDER BY l.id DESC, c.id DESC
		LIMIT 1
	`, agentID, pickCharacterID(r, req.CharacterID), pickCampaignID(r, req.CampaignID)).Scan(&charID, &lobbyID, &species)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_active_game"})
		return
	}

	endWildShapeIfDown(charID) // a druid that dropped to 0 HP or became Incapacitated is no longer in its form (wild_shape_2024.go)

	// "features": the feature words as a list, checked against the character's own words and added to the description (feature_words_2024.go)
	if add, refusal := featureListToText(charID, featureAction(req.Action), req.Features); refusal != "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "unknown_feature_word", "message": refusal, "feature_words": featureWordsFor(charID)})
		return
	} else if add != "" {
		req.Description = strings.TrimSpace(req.Description) + add
	}

	// A companion's action, or a familiar command of its owner (companions_2024.go)
	if req.CompanionID != 0 || req.Action == "dismiss_familiar" || req.Action == "recall_familiar" || req.Action == "dismiss_familiar_forever" {
		handleCompanionAction(w, charID, lobbyID, req.CompanionID, req.Action, req.Description, req.TargetID, req.TargetIDs...)
		return
	}

	// A touch spell delivered by the caster's familiar (companions_2024.go)
	if req.Action == "cast" && req.SpellSlug != "" {
		if why := familiarTouchCheck(charID, lobbyID, req.SpellSlug, req.Description); why != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "familiar_cannot_deliver", "message": why})
			return
		}
		defer familiarTouchPending.Delete(charID)
	}

	// A character casts the spells it has prepared, its cantrips, and spells a feature, its origin or an item gives it (spell_access_2024.go)
	if (req.Action == "cast" || req.Action == "attack") && (req.TargetID != 0 || len(req.TargetIDs) > 0) && ethereal(lobbyOfChar(charID), charID) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "ethereal", "message": "You are on the Ethereal Plane (Etherealness): you can't affect creatures on the Material Plane. Speak the armor's command word again (use_item) to return. Nothing was spent."})
		return
	}
	if why := moreActionBlock(charID, req.Action); why != "" { // Gaseous Form; inside an Antimagic Field (spells_more_2024.go)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "spell_effect_limit", "message": why})
		return
	}
	if req.Action == "cast" && normalizeSpellSlug(req.SpellSlug) == "shield" { // Shield answers a hit (reactions_2024.go)
		if why := shieldTriggerMissing(charID, req.Description); why != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "no_trigger", "message": why})
			return
		}
	}
	if req.Action == "cast" && req.SpellSlug != "" {
		if ok, why := spellAvailable(charID, req.SpellSlug, req.Description); !ok {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "spell_not_available", "message": why})
			return
		}
	}

	// The item a cast or use_item names (magic_item_use_2024.go)
	if req.Item != "" || req.Charges > 0 || req.Action == "use_item" {
		target := req.TargetCharacterID
		if target == 0 && req.TargetID > 0 {
			target = req.TargetID
		}
		castItems.Store(charID, itemCastReq{Item: req.Item, Charges: req.Charges, Target: target, TargetID: req.TargetID, TargetIDs: req.TargetIDs})
		defer castItems.Delete(charID)
	}

	// The ally or enemy of a Help action (help_2024.go)
	if req.Action == "help" {
		helpTargets.Store(charID, helpReq{Ally: req.TargetCharacterID, Enemy: req.TargetID})
		defer helpTargets.Delete(charID)
	}

	// Heartbeats can overlap when a client retries after a slow response. Keep
	// the check and subsequent recording for one character serialized so the
	// caller gets a durable outcome rather than a contradictory turn failure.
	unlockCharacterAction := lockCharacterAction(lobbyID, charID)
	defer unlockCharacterAction()

	// A dead character must never act, even if a stale initiative entry still
	// points to them. A stable or conscious character cannot roll death saves.
	var actorHP int
	var actorStable, actorDead bool
	if err := db.QueryRow(`SELECT hp, COALESCE(is_stable, false), COALESCE(is_dead, false)
		FROM characters WHERE id = $1`, charID).Scan(&actorHP, &actorStable, &actorDead); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "character_lookup_failed"})
		return
	}
	if errorCode := characterActionBlock(req.Action, actorHP, actorStable, actorDead); errorCode != "" {
		w.WriteHeader(http.StatusConflict)
		msg := "This character cannot take that action; no action was recorded."
		if errorCode == "unconscious_at_0_hp" {
			msg = "You are at 0 Hit Points, so you are Unconscious: you can't take actions. On your turn make a Death Saving Throw ({\"action\": \"death_save\"}) unless you are stable. Healing brings you back. No action was recorded."
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": errorCode, "message": msg})
		return
	}

	// Validate first aid before spending the actor's action. Never infer the
	// patient from prose: a wrong or stale target must be a harmless error.
	stabilizeTargetName := ""
	if req.Action == "stabilize" {
		var hp int
		var stable, dead bool
		err := db.QueryRow(`SELECT name, hp, COALESCE(is_stable, false), COALESCE(is_dead, false)
			FROM characters WHERE id = $1 AND lobby_id = $2`, req.TargetCharacterID, lobbyID).
			Scan(&stabilizeTargetName, &hp, &stable, &dead)
		if req.TargetCharacterID <= 0 || err != nil || hp != 0 || stable || dead || req.TargetCharacterID == charID {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false, "error": "invalid_stabilize_target",
				"message": "target_character_id must name a different, living, dying party member at 0 HP; no action was taken.",
			})
			return
		}
	}

	// CHECK: Incapacitated condition blocks ALL actions (except death saves)
	if req.Action != "death_save" && isIncapacitated(charID) {
		conditions := getCharConditions(charID)
		blockingCondition := "incapacitated"
		for _, c := range conditions {
			switch strings.ToLower(c) {
			case "incapacitated", "paralyzed", "stunned", "unconscious", "petrified":
				blockingCondition = c
				break
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":            false,
			"error":              "incapacitated",
			"message":            fmt.Sprintf("You cannot take actions while %s", blockingCondition),
			"blocking_condition": blockingCondition,
			"hint":               "You must wait for the condition to end or be removed.",
		})
		return
	}

	// CHECK: Movement blocked by certain conditions
	if req.Action == "move" && !canMove(charID) {
		conditions := getCharConditions(charID)
		var blockingCondition string
		for _, c := range conditions {
			switch strings.ToLower(c) {
			case "grappled", "restrained", "stunned", "paralyzed", "unconscious", "petrified":
				blockingCondition = c
				break
			}
		}
		// Also check exhaustion 5
		var exhaustion int
		db.QueryRow("SELECT COALESCE(exhaustion_level, 0) FROM characters WHERE id = $1", charID).Scan(&exhaustion)
		if exhaustion >= 5 {
			blockingCondition = "exhaustion level 5"
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":            false,
			"error":              "cannot_move",
			"message":            fmt.Sprintf("Your speed is 0 due to %s", blockingCondition),
			"blocking_condition": blockingCondition,
			"hint":               "You must remove the condition before you can move.",
		})
		return
	}

	// CHECK: Frightened movement restriction
	// When frightened, you cannot willingly move closer to the source of your fear
	if req.Action == "move" && req.TowardFrightenedSource && hasAnyFrightened(charID) {
		sourceName := getFrightenedSourceName(charID)
		sourceID := getFrightenedSourceID(charID)
		message := "You cannot willingly move closer to the source of your fear while frightened."
		if sourceName != "" {
			message = fmt.Sprintf("You cannot willingly move closer to %s while frightened.", sourceName)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":              false,
			"error":                "frightened_movement",
			"message":              message,
			"frightened_source_id": sourceID,
			"frightened_source":    sourceName,
			"hint":                 "You may move away from the source, move perpendicular, or stay in place. You may also take the Dash action to flee faster.",
		})
		return
	}

	// Check if in combat - action economy only enforced in combat
	var inCombat bool
	err = db.QueryRow("SELECT active FROM combat_state WHERE lobby_id = $1", lobbyID).Scan(&inCombat)
	if err != nil {
		inCombat = false
	}
	// F249: "target" (a name as situation.enemies shows it, or an id) was never read; a cast at "Kobold 4" hit nothing
	if t := strings.TrimSpace(req.Target); t != "" && req.TargetID == 0 && len(req.TargetIDs) == 0 {
		id, names := resolveTargetName(lobbyID, charID, t)
		if id == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "target_not_found",
				"message": fmt.Sprintf("No creature called %q here. Use a name as situation.enemies shows it, or target_id.", t), "targets": names, "hint": "Your action was not used."})
			return
		}
		req.TargetID = id
	}
	if req.Action == "cast" && inCombat && req.TargetID == 0 && len(req.TargetIDs) == 0 {
		if id := combatantNamedIn(lobbyID, req.Description); id < 0 {
			req.TargetID = id // "Sacred Flame on Kobold 2": the monster named in the description is the target (F249)
		}
	}
	if req.Action == "cast" && inCombat && req.TargetID == 0 && len(req.TargetIDs) == 0 {
		key := req.SpellSlug
		if key == "" {
			key = parseSpellFromDescription(req.Description)
		}
		if sp, ok := srdSpellsMemory[normalizeSpellSlug(key)]; ok && needsCreatureTarget(sp) && !descNamesCombatant(lobbyID, req.Description) {
			_, names := resolveTargetName(lobbyID, charID, "")
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "target_required",
				"message": fmt.Sprintf("%s needs a target: send target_id (a monster's negative id) or target (its name as situation.enemies shows it).", sp.Name), "targets": names, "hint": "Your action was not used."})
			return
		}
	}

	if !inCombat && !shouldAllowExplorationFollowup(req.Action) {
		if acted, actionType, actionDesc, actedAt := hasCharacterActedSinceLastNarration(charID, lobbyID); acted {
			if actionType == req.Action && actionDesc == req.Description {
				if replayed, result, replayedAt := actionReplaySinceLastNarration(charID, lobbyID, req.Action, req.Description); replayed {
					replay := map[string]interface{}{
						"success":          true,
						"already_recorded": true,
						"message":          "This exploration action was already recorded; returning its receipt.",
						"acted_at":         replayedAt.UTC().Format(time.RFC3339),
					}
					if result != "" {
						replay["result"] = result
					}
					json.NewEncoder(w).Encode(replay)
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":      false,
				"error":        "awaiting_narration",
				"message":      "You already took an action in this exploration beat. Wait for GM narration before acting again.",
				"last_action":  actionType,
				"description":  actionDesc,
				"acted_at":     actedAt.UTC().Format(time.RFC3339),
				"retry_after":  "After the GM narrates the scene forward",
				"how_to_check": "GET /api/my-turn",
			})
			return
		}
	}

	if inCombat && strings.EqualFold(req.Action, "move") {
		var mapped bool
		if err := db.QueryRow("SELECT battle_map IS NOT NULL FROM combat_state WHERE lobby_id = $1", lobbyID).Scan(&mapped); err != nil && !errors.Is(err, sql.ErrNoRows) {
			battleMapError(w, 500, "map_unavailable")
			return
		}
		if mapped {
			battleMapError(w, 409, "battle_map_path_required")
			return
		}
	}

	if req.MovementCost < 0 || req.MovementCost > 10000 { // a negative cost would add movement
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "invalid_movement_cost", "message": "movement_cost is the feet of movement you use: 0 or more. Nothing was spent."})
		return
	}
	// Calculate effective movement cost (prone mechanics - 5e)
	effectiveMovementCost := req.MovementCost
	isStanding := strings.ToLower(req.Action) == "stand"
	isMovingWhileProne := false

	if strings.ToLower(req.Action) == "move" || isStanding {
		conditions := getCharConditions(charID)
		isProne := false
		for _, c := range conditions {
			if strings.ToLower(c) == "prone" {
				isProne = true
				break
			}
		}

		if isStanding {
			// Standing up costs half your movement speed
			effectiveMovementCost = characterSpeed(charID, species) / 2
		} else if isProne && req.MovementCost > 0 {
			// Crawling while prone: 1ft costs 2ft of movement
			effectiveMovementCost = req.MovementCost * 2
			isMovingWhileProne = true
		}
	}

	// An attack must use an equipped weapon. Refuse before anything is recorded or spent.
	if req.Action == "attack" || req.Action == "offhand_attack" || req.Action == "frenzy_attack" {
		if wk := parseWeaponFromDescription(req.Description); wk != "" {
			equipped := weaponEquippedForChar(charID, wk)
			if req.Action == "offhand_attack" {
				equipped = weaponEquippedInOffHand(charID, wk)
			}
			if !equipped {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   "weapon_not_equipped",
					"message": notEquippedMessage(wk),
					"hint":    "Your action was not used. Equip the weapon (one equip or unequip per turn in combat) or attack with what you have equipped.",
				})
				return
			}
		}
	}

	// A named target is checked before anything is spent: it must be a living enemy in the current combat.
	speedAttack := false // the Scimitar of Speed's Bonus Action attack (F144, ranger_fixes_2024.go)
	if req.Action == "bonus_attack" {
		if why := scimitarOfSpeedReady(charID); why != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "no_bonus_attack", "message": why})
			return
		}
		speedAttack = true
	}
	attackAction := req.Action == "attack" || req.Action == "offhand_attack" || req.Action == "frenzy_attack" || req.Action == "opportunity_attack" || speedAttack
	if req.Action == "opportunity_attack" && !hasOAWindow(lobbyID, charID, req.TargetID) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "no_opportunity",
			"message": "An Opportunity Attack needs a creature you can see leaving your reach: it becomes possible when a monster moves out of your reach on the battle map (the feed and GET /api/my-turn say so). Your Reaction was not used."})
		return
	}
	if attackAction && req.TargetID != 0 {
		if ok, why := validateAttackTarget(lobbyID, req.TargetID); !ok {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "invalid_target", "message": why, "hint": "Your action was not used."})
			return
		}
		// With a battle map, the target must be seen and within reach or range (an Opportunity Attack happens just before it leaves)
		if why := attackRangeProblem(lobbyID, charID, req.TargetID, req.Description); why != "" && req.Action != "opportunity_attack" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "out_of_range", "message": why})
			return
		}
	}
	reactiveSpell := false
	if req.Action == "opportunity_attack" && req.SpellSlug != "" {
		if why := reactiveSpellProblem(charID, req.SpellSlug, req.TargetID, req.TargetIDs); why != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "reactive_spell", "message": why})
			return
		}
		req.Action, reactiveSpell = "cast", true // the creature is still within reach as it leaves: no range check
		if len(req.TargetIDs) == 0 {
			req.TargetIDs = []int{req.TargetID}
		}
	}
	if req.Action == "cast" && inCombat && !reactiveSpell && (req.TargetID != 0 || len(req.TargetIDs) > 0) {
		key := req.SpellSlug
		if key == "" {
			key = parseSpellFromDescription(req.Description)
		}
		if sp, ok := srdSpellsMemory[normalizeSpellSlug(key)]; ok {
			targets := append([]int{}, req.TargetIDs...)
			if req.TargetID != 0 {
				targets = append(targets, req.TargetID)
			}
			if b := eldritchSpearBonus(charID); b > 0 && normalizeSpellSlug(key) == "eldritch-blast" {
				sp.Range = fmt.Sprintf("%d feet", spellRangeFeet(sp.Range)+b) // Eldritch Spear: +30 feet per Warlock level (F195)
			}
			if hasWord(strings.ToLower(req.Description), "distant") && hasMetamagic(charID, "distant") {
				sp = distantRange(sp) // F194: Distant Spell doubles the range (Touch: 30 feet)
			}
			if why := spellRangeProblem(lobbyID, charID, sp, targets); why != "" {
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "out_of_range", "message": why})
				return
			}
			// An area spell reaches only the creatures inside its area on the map
			if len(targets) > 1 {
				ordered := targets
				if req.TargetID != 0 { // the aimed point first
					ordered = append([]int{req.TargetID}, req.TargetIDs...)
				}
				if kept, note := areaFilter(lobbyID, charID, sp, ordered); note != "" {
					areaNotes.Store(charID, note)
					defer areaNotes.Delete(charID)
					req.TargetIDs = kept
					req.TargetID = 0
				}
			}
		}
	}

	// Check action economy (only in combat)
	fleetNote := ""
	resourceUsed := ""
	if inCombat {
		// Command, Haste's lethargy and Slow limit what the character can do this turn
		econAction := spellEconomyAction(req.Action, req.SpellSlug, req.Description)
		if reactiveSpell {
			econAction = "reaction_cast" // War Caster's Reactive Spell takes the Reaction
		}
		if e := moreSpellEconomy(charID, req.Action, req.Description); e != "" {
			econAction = e // Expeditious Retreat's Dash, the Spiritual Weapon, the Flame Blade (spells_more_2024.go)
		}
		if req.Action == "use_item" && potionInUse(charID, req.Item, req.Description) {
			econAction = "drink_potion" // drinking a potion or giving it to a creature within 5 feet is a Bonus Action
		}
		if req.Action == "use_item" && legendaryItemBonusAction(charID, req.Item, req.Description) {
			econAction = "item_bonus" // moving a controlled Sphere of Annihilation (legendary_items_2024.go)
		}
		if req.Action == "use_item" {
			if e := legendaryMoreEconomy(charID, req.Item, req.Description); e != "" {
				econAction = e // a Rod of Lordly Might button (a Bonus Action), the Luck Blade's Luck (no action)
			}
		}
		if why := turnLimitProblem(charID, econAction); why != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "spell_effect_limit", "message": why, "hint": "Nothing was spent."})
			return
		}
		canAct, resourceType, errMsg := true, "free", ""
		if req.Action == "offhand_attack" && nickAvailable(charID, req.Description) {
			markNickUsed(charID) // Nick: the extra attack is part of the Attack action, not a Bonus Action
		} else if req.Action == "step_of_the_wind" && fleetStep(charID) {
			canAct, resourceType = true, "free"                // Fleet Step (Open Hand 11): right after another Bonus Action, without one of its own
			fleetNote = " [Fleet Step: no Bonus Action spent]" // F182
		} else {
			canAct, resourceType, errMsg = checkActionEconomy(charID, econAction, effectiveMovementCost)
		}
		if !canAct && resourceType == "action" && hasteExtraAction(charID, req.Action) {
			canAct, resourceType = true, "haste_action" // Haste: one more action (one attack, Dash, Disengage, Hide or Utilize)
		}
		if !canAct {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":       false,
				"error":         "resource_exhausted",
				"message":       errMsg,
				"resource_type": resourceType,
				"hint":          "Use GET /api/my-turn to see your available resources.",
			})
			return
		}
		resourceUsed = resourceType
		if req.Action == "step_of_the_wind" && resourceType == "bonus_action" {
			noteStepOfWind(charID) // Fleet Step can't follow Step of the Wind itself
		}
	}

	result := ""
	resourceAlreadySpent := false
	var attackInfo map[string]interface{}
	attackTip := ""
	if req.Action == "stabilize" {
		result, err = resolveStabilizeAction(charID, req.TargetCharacterID, stabilizeTargetName)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "stabilize_failed", "message": "First aid could not be recorded; no action was taken."})
			return
		}
		req.Description = fmt.Sprintf("Stabilize %s (character %d): %s", stabilizeTargetName, req.TargetCharacterID, req.Description)
	} else if inCombat && req.Action == "dash" && resourceUsed == "haste_action" {
		speed := 0
		if canMove(charID) {
			speed = characterSpeed(charID, species)
		}
		db.Exec(`UPDATE characters SET movement_remaining = COALESCE(movement_remaining,0) + $1 WHERE id = $2`, speed, charID)
		result = fmt.Sprintf("Haste action: Dash grants %d additional feet of movement this turn.", speed)
	} else if inCombat && req.Action == "dash" && resourceUsed == "bonus_action" { // Expeditious Retreat (spells_more_2024.go)
		speed := 0
		if canMove(charID) {
			speed = characterSpeed(charID, species)
		}
		if err := spendBonusDash(charID, speed, 0); err != nil {
			battleMapError(w, http.StatusConflict, "resource_exhausted")
			return
		}
		result = fmt.Sprintf("Expeditious Retreat: you Dash as a Bonus Action, %d additional feet of movement this turn.", speed)
	} else if inCombat && req.Action == "dash" {
		speed := 0
		if canMove(charID) {
			speed = characterSpeed(charID, species)
		}
		if err := spendDashAction(charID, speed); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				battleMapError(w, http.StatusConflict, "resource_exhausted")
			} else {
				battleMapError(w, http.StatusInternalServerError, "dash_failed")
			}
			return
		}
		removeOneWithShadowsInvisibility(charID)
		result = fmt.Sprintf("Dash grants %d additional feet of movement this turn.", speed)
	} else {
		var bonusDash func(int) (int, error)
		var dashUsed bool
		var dashErr error
		if inCombat && (req.Action == "cunning_action" || req.Action == "step_of_the_wind") {
			bonusDash = func(kiLimit int) (int, error) {
				speed := 0
				if canMove(charID) {
					speed = characterSpeed(charID, species)
				}
				dashErr = spendBonusDash(charID, speed, kiLimit, req.Action == "step_of_the_wind" && resourceUsed == "free")
				dashUsed = dashErr == nil
				return speed, dashErr
			}
		}
		if (attackAction || req.Action == "grapple" || req.Action == "shove" || req.Action == "cast" || req.Action == "spiritual_weapon" || req.Action == "flame_blade" || req.Action == "influence" || req.Action == "divine_spark" || req.Action == "lands_aid" || req.Action == "quivering_palm" || req.Action == "flurry_of_blows" || req.Action == "martial_arts") && req.TargetID != 0 {
			attackTargets.Store(charID, req.TargetID)
			defer attackTargets.Delete(charID)
		}
		if req.Action == "cast" && req.TargetID > 0 { // a character aimed at by a spell
			req.TargetIDs = append(req.TargetIDs, req.TargetID)
			req.TargetID = 0
		}
		if req.Action == "cast" && len(req.TargetIDs) > 0 {
			castTargets.Store(charID, req.TargetIDs)
			defer castTargets.Delete(charID)
			if req.TargetID == 0 {
				for _, id := range req.TargetIDs { // the first monster is the main target
					if id < 0 {
						attackTargets.Store(charID, id)
						defer attackTargets.Delete(charID)
						break
					}
				}
			}
		}
		if req.Action == "turn_undead" || req.Action == "preserve_life" || req.Action == "sacred_weapon" || req.Action == "bardic_inspiration" { // targets for these (F241, F251)
			t := append([]int{}, req.TargetIDs...)
			if req.TargetID != 0 {
				t = append(t, req.TargetID)
			}
			featureTargets.Store(charID, t)
			defer featureTargets.Delete(charID)
		}
		resolveAs, resolveDesc := req.Action, req.Description
		if speedAttack {
			resolveAs, resolveDesc = "attack", strings.TrimSpace(req.Description+" (scimitar of speed)")
		}
		result = resolveActionWithBonusDash(resolveAs, withActionContext(resolveDesc, req.CloseRange), charID, bonusDash, req.SpellSlug)
		result += fleetNote
		if dashErr != nil {
			if errors.Is(dashErr, sql.ErrNoRows) {
				battleMapError(w, http.StatusConflict, "bonus_dash_unavailable")
			} else {
				battleMapError(w, http.StatusInternalServerError, "dash_failed")
			}
			return
		}
		if dashUsed {
			// The Dash write already consumed the bonus action.
			resourceAlreadySpent = true
		}
		// With a target, the server decides hit or miss and applies the damage; the player is told only that.
		if attackAction && req.ObjectID != 0 && req.TargetID == 0 { // breaking objects (toolbox_more_2024.go)
			if out, ok := takeAttackOutcome(charID); ok {
				result = resolveAttackOnObject(lobbyID, req.ObjectID, out)
			}
		} else if attackAction && req.TargetID != 0 {
			if out, ok := takeAttackOutcome(charID); ok {
				out.KnockOut = wantsKnockOut(req.Description) && isMeleeAttackAction(charID, req.Description)
				if text, info, rerr := resolveAttackOnMonster(lobbyID, charID, req.TargetID, out, req.CleaveTargetID); rerr == nil {
					result, attackInfo = text, info
				}
			}
		} else if attackAction && len(enemyTargets(lobbyID)) > 0 {
			attackTip = "Add target_id (an enemy id from situation.enemies in GET /api/my-turn) and the server decides hit or miss and applies the damage. Without it the roll goes to the GM."
		}
		if v, ok := itemCastNotes.LoadAndDelete(charID); ok { // charges spent, or the scroll used up
			result += v.(string)
		}
		if req.Action == "cast" {
			result += familiarTouchSettle(charID, lobbyID, !strings.HasPrefix(result, "Cannot"))
		}
		if (attackAction || req.Action == "cast") && !strings.HasPrefix(result, "Cannot") {
			result += endPotionInvisibility(charID)
			result += endWardsOnOffense(charID) // Invisibility and Sanctuary end (buff_spells_2024.go)
		}
		if req.Action == "cast" && len(req.TargetIDs) > 0 { // Remove Curse and Greater Restoration end curses (toolbox_more_2024.go)
			if txt := endCursesBySpell(lobbyID, strings.ToLower(req.SpellSlug), req.TargetIDs); txt != "" {
				result += " " + txt
			}
		}
		// The game has no map, so melee range is tracked per character: a melee attack
		// means the attacker is next to the target; Disengage gets them out of it.
		if req.Action == "attack" && isMeleeAttackAction(charID, req.Description) {
			setInMeleeRange(charID, true)
		} else if req.Action == "disengage" {
			setInMeleeRange(charID, false)
		}
		if tookDisengage(req.Action, req.Description) {
			markDisengaged(charID) // no Opportunity Attacks for the rest of the turn
		}
	}

	// Consume the resource (only in combat)
	// a cast the rules refused (no slot left, a second slot this turn, a component missing ...) spends nothing
	if reactiveSpell && !strings.HasPrefix(result, "Cannot cast ") {
		takeOAWindow(lobbyID, charID, req.TargetID) // the Opportunity Attack chance is used
		result = "War Caster (Reactive Spell, your Reaction): " + result
	}
	castRefused := (req.Action == "cast" && strings.HasPrefix(result, "Cannot cast ")) ||
		((req.Action == "spiritual_weapon" || req.Action == "flame_blade" || req.Action == "gaseous_form") && strings.HasPrefix(result, "Cannot ")) || (req.Action == "use_item" && strings.HasPrefix(result, "Cannot ")) ||
		(req.Action == "attack" && strings.HasPrefix(result, "Cannot attack with your ")) || // a named weapon without its base weapon (named_weapons_2024.go)
		((req.Action == "wild_shape" || req.Action == "revert_wild_shape") && strings.HasPrefix(result, "Cannot ")) || // a refused Wild Shape spends no Bonus Action
		(req.Action == "bardic_inspiration" && strings.HasPrefix(result, "Cannot inspire")) // F251: a refused Bardic Inspiration spends nothing
	if inCombat && resourceUsed != "" && resourceUsed != "free" && req.Action != "dash" && !resourceAlreadySpent && !castRefused {
		consumeActionResource(charID, resourceUsed, effectiveMovementCost, req.Action)
	}

	// Handle prone condition removal when standing up
	if isStanding {
		removeCondition(charID, "prone")
		result = "You stand up from prone."
	}

	db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, $3, $4, $5)
	`, lobbyID, charID, req.Action, req.Description, result)

	// Build response with resource info
	response := map[string]interface{}{
		"success": !castRefused, // F149: a refused cast (or Wild Shape, item use...) spent nothing and says so
		"action":  req.Action,
		"result":  playerText(result), // monsters' save totals stay with the GM
	}

	// Add prone movement info if crawling
	if isMovingWhileProne {
		response["crawling_note"] = fmt.Sprintf("Crawling while prone: %dft of movement used for %dft of distance.", effectiveMovementCost, req.MovementCost)
	}

	if inCombat {
		response["resource_consumed"] = resourceUsed
		if castRefused {
			response["resource_consumed"] = "none (refused: nothing was spent)"
		}

		// Show remaining resources (including Extra Attack info)
		var actionUsed, bonusActionUsed, reactionUsed bool
		var movementRemaining int
		var attacksRemainingDB sql.NullInt32
		var charClass string
		var charLevel int
		db.QueryRow(`
			SELECT COALESCE(action_used, false), COALESCE(bonus_action_used, false),
			       COALESCE(reaction_used, false), COALESCE(movement_remaining, 30),
			       attacks_remaining, class, level
			FROM characters WHERE id = $1
		`, charID).Scan(&actionUsed, &bonusActionUsed, &reactionUsed, &movementRemaining,
			&attacksRemainingDB, &charClass, &charLevel)

		resources := map[string]interface{}{
			"action":       !actionUsed,
			"bonus_action": !bonusActionUsed,
			"reaction":     !reactionUsed,
			"movement_ft":  movementRemaining,
		}

		// Extra Attack info
		totalAttacks := characterExtraAttacks(charID, charClass, charLevel)
		if pw := pactWeaponAttacks(charID, charClass); pw > totalAttacks {
			totalAttacks = pw // Thirsting Blade, Devouring Blade (F201)
		}
		if totalAttacks > 1 {
			resources["extra_attack"] = true
			resources["total_attacks_per_action"] = totalAttacks

			if attacksRemainingDB.Valid && attacksRemainingDB.Int32 > 0 {
				resources["attacks_remaining"] = attacksRemainingDB.Int32
				resources["attack_action_status"] = fmt.Sprintf("Attack action in progress: %d of %d attacks remaining", attacksRemainingDB.Int32, totalAttacks)
			} else if !actionUsed {
				resources["attacks_remaining"] = totalAttacks
			} else {
				resources["attacks_remaining"] = 0
			}
		}

		response["resources_remaining"] = resources
	}

	if attackInfo != nil {
		response["attack"] = attackInfo
	}
	if attackTip != "" {
		response["tip"] = attackTip
	}
	json.NewEncoder(w).Encode(response)
}

// resolveStabilizeAction applies a DC 10 Wisdom (Medicine) check. Only a
// successful check changes the patient; it does not restore HP. The conditional
// update prevents a late check from changing someone already healed or stable.
func resolveStabilizeAction(actorID, targetID int, targetName string) (string, error) {
	var wisdom, level int
	var skills, expertise string
	if err := db.QueryRow(`SELECT wis, level, COALESCE(skill_proficiencies, ''), COALESCE(expertise, '')
		FROM characters WHERE id = $1`, actorID).Scan(&wisdom, &level, &skills, &expertise); err != nil {
		return "", err
	}
	mod := game.Modifier(wisdom)
	proficient := false
	for _, skill := range strings.Split(skills, ",") {
		proficient = proficient || strings.EqualFold(strings.TrimSpace(skill), "medicine")
	}
	if proficient {
		mod += game.ProficiencyBonus(level)
	}
	for _, skill := range strings.Split(expertise, ",") {
		if strings.EqualFold(strings.TrimSpace(skill), "medicine") {
			mod += game.ProficiencyBonus(level)
			break
		}
	}
	roll := game.RollDie(20)
	total := roll + mod
	if total < 10 {
		return fmt.Sprintf("Medicine check for %s: %d %+d = %d vs DC 10; failed. They remain dying.", targetName, roll, mod, total), nil
	}
	// F211: the backtick string held the Go text " + stableRounds() + " itself (the query failed); F210: Stable resets both tallies
	res, err := db.Exec(`UPDATE characters SET is_stable = true, death_save_successes = 0, death_save_failures = 0, stable_rounds_left = `+stableRounds()+` WHERE id = $1 AND hp = 0
		AND COALESCE(is_stable, false) = false AND COALESCE(is_dead, false) = false`, targetID)
	if err != nil {
		return "", err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed == 0 {
		return fmt.Sprintf("Medicine check for %s: %d %+d = %d vs DC 10; target already recovered or stabilized. No state changed.", targetName, roll, mod, total), nil
	}
	return fmt.Sprintf("Medicine check for %s: %d %+d = %d vs DC 10; success. They are stable at 0 HP and no longer make death saves.", targetName, roll, mod, total), nil
}

// Check if character has a condition that grants advantage/disadvantage
// isRanged indicates if this is a ranged attack (affects prone handling)
// targetID is the target being attacked (for flanking checks, 0 if unknown)
func getAttackModifiers(charID int, targetConditions []string, isRanged bool, targetID ...int) (bool, bool) {
	refreshConcentrationConditions(charID) // Invisibility that its caster's lost Concentration ended (buff_spells_2024.go)
	hasAdvantage := false
	hasDisadvantage := false

	// Get attacker conditions and lobby_id for lighting check
	var conditionsJSON []byte
	var lobbyID int
	db.QueryRow("SELECT COALESCE(conditions, '[]'), COALESCE(lobby_id, 0) FROM characters WHERE id = $1", charID).Scan(&conditionsJSON, &lobbyID)
	var conditions []string
	json.Unmarshal(conditionsJSON, &conditions)
	if ml, mid, ok := attackedMonster(charID); ok && mid < 0 { // Ring of Elemental Command: Elemental Bane (legendary_more_2024.go)
		if e, _, ok2 := monsterCombatant(ml, mid); ok2 && elementalBane(charID, fmt.Sprint(e["monster_key"])) {
			hasAdvantage = true
		}
	}
	if a, d := deckD20(charID); a || d { // the Mysterious Deck's Jester (Advantage) and Fool (Disadvantage), 72 hours
		hasAdvantage = hasAdvantage || a
		hasDisadvantage = hasDisadvantage || d
	}
	if buffAttackAdvantage(charID) { // Foresight (buff_spells_2024.go)
		hasAdvantage = true
	}

	// Lighting check: Darkness without darkvision/blindsight/truesight = effectively blinded
	if lobbyID > 0 {
		attackerVision := canSeeInLighting(charID, getCampaignLighting(lobbyID))
		if attackerVision == "blind" {
			// Attacker can't see: disadvantage on attacks
			hasDisadvantage = true
		}

		// Check defender's vision for advantage against them
		if len(targetID) > 0 && targetID[0] > 0 {
			// Get defender's character info (might be a monster - handle gracefully)
			var defenderLobbyID int
			db.QueryRow("SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1", targetID[0]).Scan(&defenderLobbyID)
			if defenderLobbyID > 0 {
				defenderVision := canSeeInLighting(targetID[0], getCampaignLighting(defenderLobbyID))
				if defenderVision == "blind" {
					// Defender can't see: advantage on attacks against them
					hasAdvantage = true
				}
			}
		}
	}

	// Attacker conditions
	for _, cond := range conditions {
		condLower := strings.ToLower(cond)
		// Handle parameterized invisible conditions like "invisible:one_with_shadows"
		if strings.HasPrefix(condLower, "invisible") {
			// Invisible attacker gets advantage unless defender has blindsight/truesight
			if len(targetID) > 0 && targetID[0] > 0 {
				_, defenderBlindsight, defenderTruesight := getCharacterVision(targetID[0])
				if defenderBlindsight == 0 && defenderTruesight == 0 {
					hasAdvantage = true
				}
			} else {
				hasAdvantage = true
			}
			continue
		}
		switch condLower {
		case "hidden":
			// Hidden attacker gets advantage
			if len(targetID) > 0 && targetID[0] > 0 {
				_, defenderBlindsight, defenderTruesight := getCharacterVision(targetID[0])
				if defenderBlindsight == 0 && defenderTruesight == 0 {
					hasAdvantage = true
				}
			} else {
				hasAdvantage = true
			}
		case "blinded", "frightened", "poisoned", "prone", "restrained":
			hasDisadvantage = true
		}

	}

	// Grappled (SRD 5.2.1): Disadvantage on attack rolls against any target other than the grappler
	if grappledAttackPenalty(lobbyID, charID, attackTargetID(charID)) {
		hasDisadvantage = true
	}

	// Target conditions
	for _, cond := range targetConditions {
		condLower := strings.ToLower(cond)
		// Handle parameterized invisible conditions like "invisible:one_with_shadows"
		if strings.HasPrefix(condLower, "invisible") {
			// Check if attacker has special senses that can see invisible creatures
			// Blindsight and truesight allow seeing invisible creatures (PHB)
			_, attackerBlindsight, attackerTruesight := getCharacterVision(charID)
			if attackerBlindsight == 0 && attackerTruesight == 0 && !buffSeesInvisible(charID) { // See Invisibility
				// Feral Senses (Ranger 18+) negates disadvantage from attacking unseen creatures
				if !hasFeralSenses(charID) {
					hasDisadvantage = true
				}
			}
			continue
		}
		switch condLower {
		case "blinded", "paralyzed", "petrified", "stunned", "unconscious", "restrained":
			hasAdvantage = true
		case "prone":
			// Prone (SRD 5.2.1): Advantage if the attacker is within 5 feet of the target, otherwise Disadvantage
			if attackerWithinFiveFeetOfTarget(charID, isRanged) {
				hasAdvantage = true
			} else {
				hasDisadvantage = true
			}
		case "reckless":
			// Reckless Attack - attacks against the reckless character have advantage
			hasAdvantage = true
		}
	}

	// Elusive (Rogue 18+) negates advantage against you while not incapacitated
	if len(targetID) > 0 && targetID[0] > 0 && hasAdvantage {
		if hasElusive(targetID[0]) && !isIncapacitated(targetID[0]) {
			hasAdvantage = false
		}
	}

	// Non-proficient armor imposes disadvantage on all attack rolls
	// All weapon attacks involve either STR or DEX, so this always applies
	if isWearingNonProficientArmor(charID) {
		hasDisadvantage = true
	}

	return hasAdvantage, hasDisadvantage
}

func resolveAction(action, description string, charID int, castSpellSlug ...string) string {
	return resolveActionWithBonusDash(action, description, charID, nil, castSpellSlug...)
}

func resolveActionWithBonusDash(action, description string, charID int, bonusDash func(int) (int, error), castSpellSlug ...string) string {
	// Get character stats for modifiers (including weapon proficiencies for attack checks)
	var str, dex, intl, wis, cha, level int
	var class string
	var subclass sql.NullString
	var conditionsJSON []byte
	var weaponProfsStr, equippedMainHand string
	db.QueryRow("SELECT str, dex, intl, wis, cha, level, class, COALESCE(subclass, ''), COALESCE(conditions, '[]'), COALESCE(weapon_proficiencies, ''), COALESCE(equipped_main_hand, '') FROM characters WHERE id = $1", charID).Scan(&str, &dex, &intl, &wis, &cha, &level, &class, &subclass, &conditionsJSON, &weaponProfsStr, &equippedMainHand)

	var conditions []string
	json.Unmarshal(conditionsJSON, &conditions)

	// Check for advantage/disadvantage keywords in description
	descLower := strings.ToLower(description)
	// F139: the word "advantage" in a player's description gave the attack Advantage (and "disadvantage" contains "advantage", so asking
	// for Disadvantage gave both). Advantage and Disadvantage come from the rules the server tracks (conditions, Help, features) or from
	// the GM: POST /api/gm/grant-advantage gives this character's next attack roll Advantage or Disadvantage.
	gmAdvantage := func() bool { return takeCombatEffect(lobbyOfChar(charID), charID, "gm_advantage", 0) } // read only where an attack roll is made
	gmDisadvantage := func() bool { return takeCombatEffect(lobbyOfChar(charID), charID, "gm_disadvantage", 0) }

	if action == "opportunity_attack" { // one melee attack with the Reaction (character_oa_2024.go)
		action = "attack"
		takeOAWindow(lobbyOfChar(charID), charID, attackTargetOf(charID))
	}
	switch action {
	case "attack":
		lastAttacks.Delete(charID) // an outcome left by an earlier attack (out of combat, or one that stopped early) must not be resolved now
		attackDescriptions.Store(charID, description)
		if isInWildShape(charID) { // the beast's attacks (wild_shape_2024.go)
			return wildShapeAttack(charID, description)
		}
		if text, ok := thrownFlask(charID, description); ok { // Alchemist's Fire or Acid replaces an attack (burning_2024.go)
			return text
		}
		clearPendingSpends(charID) // what an earlier attack spent "on a hit" stays spent
		// Attacking ends Sanctuary protection on the attacker
		removeSanctuaryOnOffensiveAction(charID)

		// An explicitly named weapon wins. Otherwise honor the equipped main
		// hand before falling back to an unarmed strike.
		weaponKey := parseWeaponFromDescription(description)
		if weaponKey == "" {
			weaponKey = namedWeaponInDescription(charID, description) // "swing the Vorpal Sword" (named_weapons_2024.go)
		}
		if weaponKey != "" && !weaponEquippedForChar(charID, weaponKey) {
			// only equipped weapons can attack; a weapon in a bag or on the belt must be equipped first
			return notEquippedMessage(weaponKey)
		}
		if weaponKey == "" && equippedMainHand == "" && !unarmedWanted(description) { // a weapon held only in the other hand (F166)
			if _, off := equippedWeapons(charID); off != "" {
				weaponKey = heldWeaponKey(charID, off)
			}
		}
		if weaponKey == "" && equippedMainHand != "" && !unarmedWanted(description) { // "unarmed strike": a kick or punch with a weapon in hand (F106)
			weaponKey = heldWeaponKey(charID, equippedMainHand) // "Longsword +2", or a named weapon's base weapon
			if weaponKey == "" {
				if msg := namedWeaponNeedsBase(equippedMainHand, description); msg != "" {
					return msg
				}
			}
		}
		attackHeldItem.Store(charID, heldItemFor(charID, weaponKey, false))
		if hn := strings.ToLower(heldItemFor(charID, weaponKey, false)); hn != "" { // legendary_more_2024.go
			ld := strings.ToLower(description)
			if strings.HasPrefix(hn, "defender") {
				if _, ok := attunedTo(charID, "defender"); ok {
					if note := defenderAttack(charID, description); note != "" {
						attackExtraNote.Store(charID, note)
					}
				}
			}
			if strings.HasPrefix(hn, "rod of lordly might") {
				if strings.Contains(ld, "drain life") {
					rodPowerOnHit.Store(charID, "drain")
				} else if strings.Contains(ld, "paralyze") {
					rodPowerOnHit.Store(charID, "paralyze")
				}
			}
			if strings.HasPrefix(hn, "hammer of thunderbolts") && (strings.Contains(ld, "hurl") || strings.Contains(ld, "throw")) {
				ok, note := hammerHurl(charID)
				if !ok && note != "" {
					return note
				}
				if ok {
					attackExtraNote.Store(charID, " [hurled: thrown 20/60 feet]"+note)
					description = strings.NewReplacer("hurl", "swing", "throw", "swing", "Hurl", "swing", "Throw", "swing").Replace(description) // a magic throw, not an improvised one
				}
			}
		}
		if luckWordRe.MatchString(description) {
			if _, ok := luckBladeReady(charID); ok {
				luckOnAttack.Store(charID, true)
			}
		}
		weapon, hasWeapon := srdWeapons[weaponKey]
		// Improvised weapons (improvised_2024.go): an object, a ranged weapon swung in melee, or a melee weapon without Thrown that is thrown
		improvised := false
		if iw, ok := improvisedWeapon(description, weaponKey, weapon, hasWeapon); ok {
			weapon, hasWeapon, improvised = iw, true, true
		}

		// Check ammunition for ranged weapons
		if hasWeapon && !improvised && containsProperty(weapon.Properties, "ammunition") && !energyBowShot(charID, weaponKey) { // the Energy Bow needs none (F177)
			ammoType := game.AmmoTypeForWeapon(weaponKey)
			hasAmmo, ammoErr := checkAndUseAmmo(charID, ammoType)
			if !hasAmmo {
				return ammoErr
			}
		}

		// Determine attack modifier (STR for melee, DEX for ranged/finesse)
		attackMod := game.Modifier(str)
		damageMod := game.Modifier(str)
		usesStrength := true
		if hasWeapon {
			// Finesse lets the attacker use the better of Strength and Dexterity; a ranged weapon uses Dexterity
			if weapon.Type == "ranged" || (containsProperty(weapon.Properties, "finesse") && game.Modifier(dex) > game.Modifier(str)) {
				attackMod = game.Modifier(dex)
				damageMod = game.Modifier(dex)
				usesStrength = false
			}
		}
		// Martial Arts: a Monk's Unarmed Strike and Monk weapons use the better of Strength and Dexterity
		if martialArtsApplies(charID, class, hasWeapon, weapon) && game.Modifier(dex) > game.Modifier(str) {
			attackMod = game.Modifier(dex)
			damageMod = game.Modifier(dex)
			usesStrength = false
		}
		pactBlade := pactBladeWeapon(charID, class, hasWeapon, weapon) // F133: Pact of the Blade uses Charisma and gives proficiency
		if hasWeapon && sunBladeHeld(charID) {
			weapon.DamageType = "radiant" // the Sun Blade's blade of radiance (F205)
		}
		if pactBlade {
			if t := pactDamageType(description); t != "" {
				weapon.DamageType = t // F200: the bonded weapon deals Necrotic, Psychic or Radiant damage if you choose
			}
		}
		if pactBlade && game.Modifier(spellModScore(charID)) > attackMod {
			attackMod = game.Modifier(spellModScore(charID))
			damageMod = attackMod
			usesStrength = false
		}
		masteryMod := damageMod // the plain ability modifier, for Graze and Cleave
		// 2024 Rage Damage: a bonus to the damage of attacks that use Strength while the Rage is active
		rageNote := ""
		if usesStrength && strings.ToLower(class) == "barbarian" && hasCondition(charID, "raging") {
			damageMod += game.RageDamageBonus(level)
			rageNote = fmt.Sprintf(" [Rage +%d damage]", game.RageDamageBonus(level)) // F243: the bonus was added without a word
		}

		// Add proficiency bonus only if proficient with the weapon
		isProficient := isWeaponProficient(weaponProfsStr, weaponKey)
		if !hasWeapon {
			isProficient = true // every creature adds its Proficiency Bonus to an Unarmed Strike
		}
		if improvised {
			isProficient = false // no Proficiency Bonus with an improvised weapon
		}
		if pactBlade {
			isProficient = true // proficient with the pact weapon (F133)
		}
		if plus := weaponPlusFor(charID, description, weaponKey, false); plus > 0 && hasWeapon && !improvised { // Weapon, +1, +2, or +3; a named weapon's bonus (named_weapons_2024.go)
			attackMod += plus
			damageMod += plus
		}
		magicAmmoNote := ""
		if v, ok := magicAmmoPlus.LoadAndDelete(charID); ok && hasWeapon { // Ammunition, +N (F150)
			if p, _ := v.(int); p > 0 {
				attackMod += p
				damageMod += p
				magicAmmoNote = fmt.Sprintf(" [magic ammunition +%d]", p)
			}
		}
		if isProficient {
			attackMod += game.ProficiencyBonus(level)
		}

		// Determine if ranged attack (for proper prone handling)
		isRangedAttack := hasWeapon && weapon.Type == "ranged"

		// Archery Fighting Style (+2 to ranged attack rolls)
		archeryBonus := 0
		archeryNote := ""
		if isRangedAttack && hasFightingStyle(charID, "archery") {
			archeryBonus = 2
			attackMod += archeryBonus
			archeryNote = " (Archery +2)"
		}

		// Sacred Weapon (Devotion Paladin Channel Divinity)
		// Adds CHA modifier (min +1) to attack rolls for 1 minute
		sacredWeaponBonus, _ := getSacredWeaponBonus(charID)
		if sacredWeaponBonus > 0 {
			attackMod += sacredWeaponBonus
		}

		powerAttackNote := ""

		// Get condition-based advantage/disadvantage (pass isRanged for prone handling)
		hasAdvantage, hasDisadvantage := getAttackModifiers(charID, attackTargetConditions(charID), isRangedAttack)
		if vexActive(charID) {
			hasAdvantage = true // Weapon Mastery: Vex
		}
		if takeSteadyAim(charID) {
			hasAdvantage = true // Rogue: Steady Aim
		}
		if exposedTarget(charID) {
			hasAdvantage = true // after a saved Stunning Strike
		}
		if assistedTarget(charID) {
			hasAdvantage = true // an ally's Help (Assist an Attack Roll)
		}
		if preciseHunter(charID) {
			hasAdvantage = true // Ranger level 17: Advantage against the creature marked by Hunter's Mark
		}
		if lid, mid, ok := attackedMonster(charID); ok && outlined(lid, mid) {
			hasAdvantage = true // Faerie Fire
		}

		// Underwater combat check
		// Melee attacks have disadvantage, ranged attacks have disadvantage unless exempt weapon
		var lobbyID int
		db.QueryRow("SELECT lobby_id FROM characters WHERE id = $1", charID).Scan(&lobbyID)
		underwaterMiss.Delete(charID) // a flag left by an attack that stopped early must not touch this one
		if isUnderwaterCombat(lobbyID) {
			if !isRangedAttack {
				// a melee weapon attack has Disadvantage unless the weapon deals Piercing damage (or the attacker has a Swim Speed)
				if !(hasWeapon && strings.EqualFold(weapon.DamageType, "piercing")) && !hasSwimSpeed(charID) {
					hasDisadvantage = true
				}
			} else {
				// a ranged weapon attack has Disadvantage within normal range and automatically misses beyond it
				hasDisadvantage = true
				if _, mid, ok := attackedMonster(charID); ok && attackAtLongRange(lobbyID, charID, mid, description) {
					underwaterMiss.Store(charID, true)
				}
			}
		}

		// Close-range ranged attack disadvantage
		// "When you make a ranged attack with a weapon, a spell, or some other means,
		// you have disadvantage on the attack roll if you are within 5 feet of a hostile
		// creature who can see you and who isn't incapacitated."
		// Crossbow Expert negates this penalty.
		closeRangeNote := ""
		isCloseRange := strings.Contains(descLower, "close range") ||
			strings.Contains(descLower, "in melee") ||
			strings.Contains(descLower, "within 5") ||
			strings.Contains(descLower, "point blank") ||
			strings.Contains(descLower, "point-blank")
		if _, mid, ok := attackedMonster(charID); ok && isRangedAttack && attackAtLongRange(lobbyID, charID, mid, description) {
			hasDisadvantage = true
			closeRangeNote += " (long range: Disadvantage)"
		}
		if isRangedAttack && isCloseRange {
			hasDisadvantage = true
			closeRangeNote = " (close range: Disadvantage)"
		}

		// Melee range: a ranged attack with an enemy within 5 feet has Disadvantage.
		// The game has no map, so this is the character's tracked melee range state (or
		// the close_range flag the player sent).
		if hasWeapon && !strings.Contains(closeRangeNote, "close range") {
			if meleeDisadv, meleeNote := meleeRangeEffects(weapon, description, enemyWithinFive(lobbyID, charID, description),
				false); meleeDisadv || meleeNote != "" {
				if meleeDisadv {
					hasDisadvantage = true
				}
				closeRangeNote += meleeNote
			}
		}

		// Override with explicit request
		if gmAdvantage() {
			hasAdvantage = true
		}
		if grapplerAdvantage(charID) { // Grappler (F181, items_c6_2024.go)
			hasAdvantage = true
			closeRangeNote += " [Grappler: Advantage against the creature you grapple]"
		}
		if adv, dis, note := oathbowRoll(charID, weaponKey, description); adv || dis { // Oathbow (items_c4_2024.go)
			hasAdvantage = hasAdvantage || adv
			hasDisadvantage = hasDisadvantage || dis
			closeRangeNote += note
		}
		if gmDisadvantage() {
			hasDisadvantage = true
		}

		// Reckless Attack (Barbarian level 2+)
		// "When you make your first attack on your turn, you can decide to attack recklessly"
		// Grants advantage on STR melee attacks, but attacks against you have advantage until next turn
		recklessNote := ""
		if strings.Contains(descLower, "reckless") {
			classLower := strings.ToLower(class)
			if classLower == "barbarian" && level >= 2 {
				// Must be a STR-based melee attack (not ranged, not finesse with DEX)
				isSTRMelee := !isRangedAttack
				if hasWeapon && containsProperty(weapon.Properties, "finesse") {
					// Finesse weapons can use DEX - only grant advantage if using STR (higher mod)
					if game.Modifier(str) < game.Modifier(dex) {
						isSTRMelee = false
					}
				}

				if isSTRMelee {
					hasAdvantage = true
					recklessNote = " ⚔️ RECKLESS!"

					// Apply "reckless" condition (grants enemies advantage against you until your next turn)
					var existingConds []byte
					db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&existingConds)
					var currentConds []string
					json.Unmarshal(existingConds, &currentConds)

					// Check if already reckless
					alreadyReckless := false
					for _, c := range currentConds {
						if c == "reckless" {
							alreadyReckless = true
							break
						}
					}
					if !alreadyReckless {
						currentConds = append(currentConds, "reckless")
						updatedConds, _ := json.Marshal(currentConds)
						db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updatedConds, charID)
					}
				} else {
					recklessNote = " (Reckless Attack requires STR-based melee attack)"
				}
			} else if classLower != "barbarian" {
				recklessNote = " (only Barbarians can attack recklessly)"
			} else {
				recklessNote = " (Reckless Attack requires Barbarian level 2+)"
			}
		}
		// 2024 Brutal Strike (level 9): forgo the Advantage of this reckless Strength attack for extra damage and an effect
		brutalStrike := brutalStrikeWanted(class, level, descLower, strings.Contains(recklessNote, "RECKLESS!"))
		if brutalStrike {
			hasAdvantage = false
		}

		// Check for auto-crit against paralyzed/unconscious targets
		// Try to parse target from description (e.g., "attack goblin" or "attack the orc")
		autoCrit := false
		autoCritReason := ""
		targetID := parseTargetFromDescription(description, charID)

		// Charmed: Can't attack the charmer
		if targetID > 0 && isCharmedBy(charID, targetID) {
			var targetName string
			db.QueryRow("SELECT name FROM characters WHERE id = $1", targetID).Scan(&targetName)
			if targetName == "" {
				targetName = "your charmer"
			}
			return fmt.Sprintf("Cannot attack %s — you are charmed by them!", targetName)
		}

		// Sanctuary check
		// Target protected by Sanctuary requires WIS save or must choose different target
		sanctuaryBlock, _ := checkSanctuaryProtection(charID, targetID, wis)
		if sanctuaryBlock != "" {
			return sanctuaryBlock
		}

		if reason := monsterAutoCritReason(charID, isRangedAttack); reason != "" && !autoCrit {
			autoCrit = true
			autoCritReason = reason
		}
		if targetID > 0 && isAutoCrit(targetID) {
			autoCrit = true
			conditions := getCharConditions(targetID)
			for _, c := range conditions {
				if strings.ToLower(c) == "paralyzed" || strings.ToLower(c) == "unconscious" {
					autoCritReason = c
					break
				}
			}
		}

		// Roll attack (advantage and disadvantage cancel out)
		var attackRoll, roll1, roll2 int
		rollType := "normal"
		if hasAdvantage && !hasDisadvantage {
			attackRoll, roll1, roll2 = game.RollWithAdvantage()
			rollType = "advantage"
		} else if hasDisadvantage && !hasAdvantage {
			attackRoll, roll1, roll2 = game.RollWithDisadvantage()
			rollType = "disadvantage"
		} else {
			attackRoll = game.RollDie(20)
			roll1, roll2 = attackRoll, 0
		}

		// Halfling Lucky - reroll nat 1s on attack rolls
		attackHalflingLuckyUsed := false
		attackHalflingLuckyOriginal := 0
		if attackRoll == 1 {
			newRoll, rerolled, origRoll := applyHalflingLucky(attackRoll, charID)
			if rerolled {
				attackHalflingLuckyUsed = true
				attackHalflingLuckyOriginal = origRoll
				attackRoll = newRoll
				roll1 = newRoll
			}
		}

		totalAttack := attackRoll + attackMod - game.ExhaustionD20Penalty(exhaustionLevelOf(charID))

		rollInfo := ""
		if rollType != "normal" {
			rollInfo = fmt.Sprintf(" [%s: %d, %d → %d]", rollType, roll1, roll2, attackRoll)
		}
		rollInfo += rageNote
		// Add Halfling Lucky note to roll info
		if attackHalflingLuckyUsed {
			rollInfo = fmt.Sprintf(" 🍀[Lucky: %d→%d]", attackHalflingLuckyOriginal, attackRoll) + rollInfo
		}
		// Add reckless note to roll info
		if recklessNote != "" {
			rollInfo = recklessNote + rollInfo
		}
		// Add close-range note to roll info
		if closeRangeNote != "" {
			rollInfo = closeRangeNote + rollInfo
		}
		rollInfo += magicAmmoNote
		if adj, note := spellRollAdjust(lobbyID, charID, "attack"); note != "" { // Bless, Bane
			totalAttack += adj
			rollInfo += note
		}

		// 2024 Weapon Mastery: say what the weapon's Mastery property adds, if the character has it
		masteryUses.Delete(charID)
		pendingBrutal.Delete(charID)
		pendingCunning.Delete(charID) // effects chosen for an earlier attack must not carry over
		riderDamage.Delete(charID)
		noteStunningStrike(charID, class, level, descLower)
		noteGiantBoon(charID, descLower) // Goliath: Fire's Burn, Frost's Chill, Hill's Tumble on a hit
		markRageExtended(charID)         // an attack roll extends a Rage
		if hasWeapon {
			noteMasteryUse(charID, weaponKey, weapon, masteryMod, level, descLower)
			if mn := masteryNote(charID, weaponKey, weapon, masteryMod, level); mn != "" {
				rollInfo += mn
			}
		}

		// Auto-crit against paralyzed/unconscious targets (within 5ft assumed for melee)
		if autoCrit && attackRoll != 1 {
			// Critical hit - double damage dice
			damageDice := unarmedDice(class, level)
			if hasWeapon {
				damageDice = monkWeaponDice(charID, class, level, weapon) // the Martial Arts die for a Monk weapon
			}

			// Great Weapon Fighting - reroll 1s and 2s on damage dice
			autoCritGWFNote := ""
			autoCritIsTwoHanded := hasWeapon && (containsProperty(weapon.Properties, "two-handed") ||
				(containsProperty(weapon.Properties, "versatile") && strings.Contains(descLower, "two hand")))

			var dmg int
			if autoCritIsTwoHanded && hasFightingStyle(charID, "great_weapon_fighting") {
				dmg = game.RollDamageGWF(damageDice, true) + damageMod
				autoCritGWFNote = " (GWF)"
			} else {
				dmg = game.RollDamage(damageDice, true) + damageMod
			}

			// Dueling - +2 damage with one-handed melee
			autoCritDuelingNote := ""
			autoCritIsOneHandedMelee := hasWeapon && weapon.Type == "melee" && !containsProperty(weapon.Properties, "two-handed")
			if autoCritIsOneHandedMelee && hasFightingStyle(charID, "dueling") {
				dmg += 2
				autoCritDuelingNote = " (Dueling +2)"
			}

			if fd, fn := frenzyDamage(charID, class, subclass, level, usesStrength && !isRangedAttack, true); fd > 0 {
				dmg += fd
				rollInfo += fn
			}
			if rd, rn := radiantStrikes(charID, class, level, !isRangedAttack, true); rd > 0 {
				dmg += rd
				rollInfo += rn
			}
			if pd, pn := primalStrike(charID, class, level, descLower, true); pd > 0 {
				dmg += pd
				rollInfo += pn
			}
			if ed, en := eldritchSmite(charID, class, level, descLower, true); en != "" {
				dmg += ed
				rollInfo += en
			}
			if brutalStrike {
				bd, bn := brutalStrikeHit(charID, level, descLower, true)
				dmg += bd
				rollInfo += bn
			}

			weaponName := "unarmed"
			if hasWeapon {
				weaponName = weapon.Name
			}

			// Check for Hunter's Colossus Slayer on auto-crit
			colossusSlayerNote := ""
			if strings.ToLower(class) == "ranger" && subclass.Valid && subclass.String == "hunter" && level >= 3 {
				var choicesJSON []byte
				db.QueryRow("SELECT COALESCE(subclass_choices, '{}') FROM characters WHERE id = $1", charID).Scan(&choicesJSON)
				var choices map[string]string
				json.Unmarshal(choicesJSON, &choices)
				if choices["hunters_prey"] == "colossus_slayer" {
					if woundedTarget(charID, targetID) { // F146: a monster (negative id) too
						colossusSlayerDmg := game.RollDie(8) + game.RollDie(8)
						dmg += colossusSlayerDmg
						colossusSlayerNote = fmt.Sprintf(" (+%d Colossus Slayer, 2d8 vs wounded)", colossusSlayerDmg)
					}
				}
			}

			// Check for Life Cleric's Divine Strike on auto-crit
			// Double dice on crit: 2d8 at level 8+, 4d8 at level 14+
			divineStrikeNote := ""
			if strings.ToLower(class) == "cleric" && level >= 7 && choseBlessedStrikesDivine(charID) {
				var divineStrikeDmg int
				if level >= 14 {
					divineStrikeDmg = game.RollDie(8) + game.RollDie(8) + game.RollDie(8) + game.RollDie(8) // 4d8 on crit
					dmg += divineStrikeDmg
					divineStrikeNote = fmt.Sprintf(" (+%d Divine Strike, 4d8 radiant)", divineStrikeDmg)
				} else {
					divineStrikeDmg = game.RollDie(8) + game.RollDie(8) // 2d8 on crit
					dmg += divineStrikeDmg
					divineStrikeNote = fmt.Sprintf(" (+%d Divine Strike, 2d8 radiant)", divineStrikeDmg)
				}
			}

			// Check for Rogue's Sneak Attack on auto-crit
			// Double dice on crit
			sneakAttackNote := ""
			if rogueLevel := classLevelIn(charID, class, level, "rogue"); rogueLevel > 0 { // a Rogue taken later counts (multiclass)
				var sneakUsed bool
				db.QueryRow("SELECT COALESCE(sneak_attack_used, false) FROM characters WHERE id = $1", charID).Scan(&sneakUsed)

				if !sneakUsed && canSneakAttack(charID, weaponKey, hasAdvantage, hasDisadvantage, targetID) {
					sneakDice, cunningNote := sneakDiceWithCunning(charID, rogueLevel, descLower)
					rollInfo += cunningNote
					sneakDmg := game.RollDamage(sneakDice, true) // Doubled on crit
					dmg += sneakDmg
					// Parse dice count for display (e.g., "3d6" -> "6d6" on crit)
					var diceCount int
					fmt.Sscanf(sneakDice, "%dd6", &diceCount)
					sneakAttackNote = fmt.Sprintf(" (+%d Sneak Attack, %dd6)", sneakDmg, diceCount*2)
					db.Exec("UPDATE characters SET sneak_attack_used = true WHERE id = $1", charID)
				}
			}

			// Check for Divine Smite on auto-crit
			divineSmiteNote := ""
			wantsSmite, smiteSlot := parseDivineSmiteSlot(description)
			if wantsSmite {
				canSmite, smiteErr := canUseDivineSmite(charID, smiteSlot, wantsFreeSmite(description))
				if canSmite {
					isUndead := isUndeadOrFiend(targetID)
					smiteDmg, smiteDice := calculateDivineSmiteDamage(smiteSlot, isUndead, true) // true = crit
					dmg += smiteDmg
					addRider(charID, "radiant", smiteDmg)
					consumeSpellSlotForSmite(charID, smiteSlot, wantsFreeSmite(description))
					undeadBonus := ""
					if isUndead {
						undeadBonus = " +undead/fiend"
					}
					divineSmiteNote = fmt.Sprintf(" (+%d Divine Smite, %s radiant%s)", smiteDmg, smiteDice, undeadBonus) + smiteOfProtection(lobbyID, charID)
				} else {
					divineSmiteNote = fmt.Sprintf(" [Smite failed: %s]", smiteErr)
				}
			}

			ldDmg, autoCritLifedrinkerNote := lifedrinker2024(charID, class, level, isRangedAttack, true, description) // Lifedrinker (2024)
			dmg += ldDmg

			// Hunter's Mark / Hex bonus damage on auto-crit
			// +1d6 damage (doubled on crit) to attacks against marked target
			autoCritMarkDmg, autoCritMarkNote := getMarkBonusDamage(charID, targetID, true)
			dmg += autoCritMarkDmg

			recordAttackOutcome(charID, attackOutcome{Weapon: weaponName, Total: totalAttack, Natural: attackRoll, Crit: true, Damage: dmg, DamageType: attackDamageType(hasWeapon, weapon), RollInfo: archeryNote + rollInfo, SneakUsed: sneakAttackNote != "", SneakNote: autoCritGWFNote + autoCritDuelingNote + sneakAttackNote + colossusSlayerNote + divineStrikeNote + divineSmiteNote + autoCritLifedrinkerNote + autoCritMarkNote, Dice: damageDice, DiceMod: damageMod})
			return fmt.Sprintf("Attack with %s: %d (AUTO-CRIT - target is %s!)%s%s Damage: %d%s%s%s%s%s%s%s%s (doubled dice)",
				weaponName, totalAttack, autoCritReason, archeryNote, rollInfo, dmg, autoCritGWFNote, autoCritDuelingNote, colossusSlayerNote, divineStrikeNote, sneakAttackNote, divineSmiteNote, autoCritLifedrinkerNote, autoCritMarkNote)
		}

		// Get crit range for this character (Champion subclass can lower it)
		critRange := game.CriticalHitRange(subclass.String, level)

		if attackRoll >= critRange {
			// Critical hit - double damage dice
			damageDice := unarmedDice(class, level)
			if hasWeapon {
				damageDice = monkWeaponDice(charID, class, level, weapon) // the Martial Arts die for a Monk weapon
			}

			// Great Weapon Fighting - reroll 1s and 2s on damage dice
			critGWFNote := ""
			critIsTwoHanded := hasWeapon && (containsProperty(weapon.Properties, "two-handed") ||
				(containsProperty(weapon.Properties, "versatile") && strings.Contains(descLower, "two hand")))

			var dmg int
			if critIsTwoHanded && hasFightingStyle(charID, "great_weapon_fighting") {
				dmg = game.RollDamageGWF(damageDice, true) + damageMod
				critGWFNote = " (GWF)"
			} else {
				dmg = game.RollDamage(damageDice, true) + damageMod
			}

			// Dueling - +2 damage with one-handed melee
			critDuelingNote := ""
			critIsOneHandedMelee := hasWeapon && weapon.Type == "melee" && !containsProperty(weapon.Properties, "two-handed")
			if critIsOneHandedMelee && hasFightingStyle(charID, "dueling") {
				dmg += 2
				critDuelingNote = " (Dueling +2)"
			}

			weaponName := "unarmed"
			if hasWeapon {
				weaponName = weapon.Name
			}

			// Check for Hunter's Colossus Slayer on crit
			// Extra 1d8 (doubled on crit = 2d8) against wounded targets
			colossusSlayerNote := ""
			if strings.ToLower(class) == "ranger" && subclass.Valid && subclass.String == "hunter" && level >= 3 {
				var choicesJSON []byte
				db.QueryRow("SELECT COALESCE(subclass_choices, '{}') FROM characters WHERE id = $1", charID).Scan(&choicesJSON)
				var choices map[string]string
				json.Unmarshal(choicesJSON, &choices)
				if choices["hunters_prey"] == "colossus_slayer" {
					if woundedTarget(charID, targetID) { // F146: a monster (negative id) too
						colossusSlayerDmg := game.RollDie(8) + game.RollDie(8) // Doubled on crit
						dmg += colossusSlayerDmg
						colossusSlayerNote = fmt.Sprintf(" (+%d Colossus Slayer, 2d8 vs wounded)", colossusSlayerDmg)
					}
				}
			}

			// Check for Life Cleric's Divine Strike on crit
			// Double dice on crit: 2d8 at level 8+, 4d8 at level 14+
			divineStrikeNote := ""
			if strings.ToLower(class) == "cleric" && level >= 7 && choseBlessedStrikesDivine(charID) {
				var divineStrikeDmg int
				if level >= 14 {
					divineStrikeDmg = game.RollDie(8) + game.RollDie(8) + game.RollDie(8) + game.RollDie(8) // 4d8 on crit
					dmg += divineStrikeDmg
					divineStrikeNote = fmt.Sprintf(" (+%d Divine Strike, 4d8 radiant)", divineStrikeDmg)
				} else {
					divineStrikeDmg = game.RollDie(8) + game.RollDie(8) // 2d8 on crit
					dmg += divineStrikeDmg
					divineStrikeNote = fmt.Sprintf(" (+%d Divine Strike, 2d8 radiant)", divineStrikeDmg)
				}
			}

			// Check for Rogue's Sneak Attack on crit
			// Double dice on crit
			sneakAttackNote := ""
			if rogueLevel := classLevelIn(charID, class, level, "rogue"); rogueLevel > 0 { // a Rogue taken later counts (multiclass)
				var sneakUsed bool
				db.QueryRow("SELECT COALESCE(sneak_attack_used, false) FROM characters WHERE id = $1", charID).Scan(&sneakUsed)

				if !sneakUsed && canSneakAttack(charID, weaponKey, hasAdvantage, hasDisadvantage, targetID) {
					sneakDice, cunningNote := sneakDiceWithCunning(charID, rogueLevel, descLower)
					rollInfo += cunningNote
					sneakDmg := game.RollDamage(sneakDice, true) // Doubled on crit
					dmg += sneakDmg
					var diceCount int
					fmt.Sscanf(sneakDice, "%dd6", &diceCount)
					sneakAttackNote = fmt.Sprintf(" (+%d Sneak Attack, %dd6)", sneakDmg, diceCount*2)
					db.Exec("UPDATE characters SET sneak_attack_used = true WHERE id = $1", charID)
				}
			}

			// Check for Divine Smite on crit
			divineSmiteNote := ""
			wantsSmite, smiteSlot := parseDivineSmiteSlot(description)
			if wantsSmite {
				canSmite, smiteErr := canUseDivineSmite(charID, smiteSlot, wantsFreeSmite(description))
				if canSmite {
					isUndead := isUndeadOrFiend(targetID)
					smiteDmg, smiteDice := calculateDivineSmiteDamage(smiteSlot, isUndead, true) // true = crit
					dmg += smiteDmg
					addRider(charID, "radiant", smiteDmg)
					consumeSpellSlotForSmite(charID, smiteSlot, wantsFreeSmite(description))
					undeadBonus := ""
					if isUndead {
						undeadBonus = " +undead/fiend"
					}
					divineSmiteNote = fmt.Sprintf(" (+%d Divine Smite, %s radiant%s)", smiteDmg, smiteDice, undeadBonus) + smiteOfProtection(lobbyID, charID)
				} else {
					divineSmiteNote = fmt.Sprintf(" [Smite failed: %s]", smiteErr)
				}
			}

			ldDmg, critLifedrinkerNote := lifedrinker2024(charID, class, level, isRangedAttack, true, description) // Lifedrinker (2024)
			dmg += ldDmg

			if fd, fn := frenzyDamage(charID, class, subclass, level, usesStrength && !isRangedAttack, true); fd > 0 {
				dmg += fd
				rollInfo += fn
			}
			if rd, rn := radiantStrikes(charID, class, level, !isRangedAttack, true); rd > 0 {
				dmg += rd
				rollInfo += rn
			}
			if pd, pn := primalStrike(charID, class, level, descLower, true); pd > 0 {
				dmg += pd
				rollInfo += pn
			}
			if ed, en := eldritchSmite(charID, class, level, descLower, true); en != "" {
				dmg += ed
				rollInfo += en
			}
			if brutalStrike {
				bd, bn := brutalStrikeHit(charID, level, descLower, true)
				dmg += bd
				rollInfo += bn
			}
			// Hunter's Mark / Hex bonus damage on crit
			// +1d6 damage (doubled on crit) to attacks against marked target
			critMarkDmg, critMarkNote := getMarkBonusDamage(charID, targetID, true)
			dmg += critMarkDmg

			critLabel := "nat 20 CRITICAL!"
			if critRange < 20 && attackRoll < 20 {
				critLabel = fmt.Sprintf("nat %d CRITICAL! (Improved Critical)", attackRoll)
			}
			// Include power attack note in crit result
			recordAttackOutcome(charID, attackOutcome{Weapon: weaponName, Total: totalAttack, Natural: attackRoll, Crit: true, Damage: dmg, DamageType: attackDamageType(hasWeapon, weapon), RollInfo: archeryNote + rollInfo, SneakUsed: sneakAttackNote != "", SneakNote: critGWFNote + critDuelingNote + sneakAttackNote + colossusSlayerNote + divineStrikeNote + divineSmiteNote + critLifedrinkerNote + critMarkNote, Dice: damageDice, DiceMod: damageMod})
			return fmt.Sprintf("Attack with %s: %d (%s)%s%s%s Damage: %d%s%s%s%s%s%s%s%s", weaponName, totalAttack, critLabel, archeryNote, powerAttackNote, rollInfo, dmg, critGWFNote, critDuelingNote, colossusSlayerNote, divineStrikeNote, sneakAttackNote, divineSmiteNote, critLifedrinkerNote, critMarkNote)
		} else if attackRoll == 1 && !attackHalflingLuckyUsed && !peerlessWillHit(charID) {
			// Critical miss (nat 1) - but not if Halfling Lucky was used (they already rerolled), nor when Peerless Aim will turn it into a hit
			// (O-10: the hit then rolls its damage and every on-hit extra in the normal branch below)
			fumbleWeapon := "your weapon"
			if hasWeapon {
				fumbleWeapon = weapon.Name
			}
			fumbleDice := unarmedDice(class, level) // the damage a Peerless Aim hit would roll (F59: it dealt 0)
			if hasWeapon {
				fumbleDice = monkWeaponDice(charID, class, level, weapon)
			}
			recordAttackOutcome(charID, attackOutcome{Weapon: fumbleWeapon, Total: totalAttack, Natural: 1, Fumble: true, DamageType: attackDamageType(hasWeapon, weapon), RollInfo: archeryNote + rollInfo, Dice: fumbleDice, DiceMod: damageMod})
			return fmt.Sprintf("Attack roll: %d (natural 1: miss)%s", totalAttack, rollInfo)
		}

		// Normal hit
		damageDice := unarmedDice(class, level)
		weaponName := "unarmed"
		if hasWeapon {
			damageDice = monkWeaponDice(charID, class, level, weapon) // the Martial Arts die for a Monk weapon
			weaponName = weapon.Name
		}

		// Great Weapon Fighting - reroll 1s and 2s on damage dice
		gwfNote := ""
		isTwoHanded := hasWeapon && (containsProperty(weapon.Properties, "two-handed") ||
			(containsProperty(weapon.Properties, "versatile") && strings.Contains(descLower, "two hand")))

		// Roll damage (with GWF rerolls if applicable)
		var dmg int
		if isTwoHanded && hasFightingStyle(charID, "great_weapon_fighting") {
			dmg = game.RollDamageGWF(damageDice, false) + damageMod
			gwfNote = " (GWF)"
		} else {
			dmg = game.RollDamage(damageDice, false) + damageMod
		}

		// Dueling - +2 damage with one-handed melee, no other weapons
		duelingNote := ""
		isOneHandedMelee := hasWeapon && weapon.Type == "melee" && !containsProperty(weapon.Properties, "two-handed")
		if isOneHandedMelee && hasFightingStyle(charID, "dueling") {
			// Dueling requires wielding a melee weapon in one hand with no other weapons
			// Shield is fine for Dueling (only blocks with off-hand)
			dmg += 2
			duelingNote = " (Dueling +2)"
		}

		if fd, fn := frenzyDamage(charID, class, subclass, level, usesStrength && !isRangedAttack, false); fd > 0 {
			dmg += fd
			rollInfo += fn
		}
		if rd, rn := radiantStrikes(charID, class, level, !isRangedAttack, false); rd > 0 {
			dmg += rd
			rollInfo += rn
		}
		if pd, pn := primalStrike(charID, class, level, descLower, false); pd > 0 {
			dmg += pd
			rollInfo += pn
		}
		if ed, en := eldritchSmite(charID, class, level, descLower, false); en != "" {
			dmg += ed
			rollInfo += en
		}
		if brutalStrike {
			bd, bn := brutalStrikeHit(charID, level, descLower, false)
			dmg += bd
			rollInfo += bn
		}

		// Check for Hunter's Colossus Slayer
		// Extra 1d8 damage once per turn against wounded targets
		colossusSlayerDmg := 0
		colossusSlayerNote := ""
		if strings.ToLower(class) == "ranger" && subclass.Valid && subclass.String == "hunter" && level >= 3 {
			// Check if they chose colossus_slayer
			var choicesJSON []byte
			db.QueryRow("SELECT COALESCE(subclass_choices, '{}') FROM characters WHERE id = $1", charID).Scan(&choicesJSON)
			var choices map[string]string
			json.Unmarshal(choicesJSON, &choices)
			if choices["hunters_prey"] == "colossus_slayer" {
				// Check if target is wounded (HP < max_hp)
				if woundedTarget(charID, targetID) { // F146: a monster (negative id) too
					colossusSlayerDmg = game.RollDie(8)
					dmg += colossusSlayerDmg
					colossusSlayerNote = fmt.Sprintf(" (+%d Colossus Slayer, 1d8 vs wounded)", colossusSlayerDmg)
				}
			}
		}

		// Check for Life Cleric's Divine Strike
		// Extra 1d8 radiant damage once per turn on weapon attacks (level 8+), 2d8 at level 14+
		divineStrikeNote := ""
		if strings.ToLower(class) == "cleric" && level >= 7 && choseBlessedStrikesDivine(charID) {
			var divineStrikeDmg int
			if level >= 14 {
				divineStrikeDmg = game.RollDie(8) + game.RollDie(8)
				dmg += divineStrikeDmg
				divineStrikeNote = fmt.Sprintf(" (+%d Divine Strike, 2d8 radiant)", divineStrikeDmg)
			} else {
				divineStrikeDmg = game.RollDie(8)
				dmg += divineStrikeDmg
				divineStrikeNote = fmt.Sprintf(" (+%d Divine Strike, 1d8 radiant)", divineStrikeDmg)
			}
		}

		// Check for Rogue's Sneak Attack
		// Extra damage once per turn with finesse/ranged weapon when have advantage or ally adjacent to target
		sneakAttackNote := ""
		if rogueLevel := classLevelIn(charID, class, level, "rogue"); rogueLevel > 0 { // a Rogue taken later counts (multiclass)
			// Check if sneak attack already used this turn
			var sneakUsed bool
			db.QueryRow("SELECT COALESCE(sneak_attack_used, false) FROM characters WHERE id = $1", charID).Scan(&sneakUsed)

			if !sneakUsed && canSneakAttack(charID, weaponKey, hasAdvantage, hasDisadvantage, targetID) {
				sneakDice, cunningNote := sneakDiceWithCunning(charID, rogueLevel, descLower)
				rollInfo += cunningNote
				sneakDmg := game.RollDamage(sneakDice, false)
				dmg += sneakDmg
				sneakAttackNote = fmt.Sprintf(" (+%d Sneak Attack, %s)", sneakDmg, sneakDice)

				// Mark sneak attack as used this turn
				db.Exec("UPDATE characters SET sneak_attack_used = true WHERE id = $1", charID)
			}
		}

		// Check for Divine Smite on normal hit
		// Divine Smite (Paladin spell): 2d8 Radiant + 1d8 per slot level above 1 (no maximum); +1d8 against a Fiend or an Undead
		divineSmiteNote := ""
		wantsSmite, smiteSlot := parseDivineSmiteSlot(description)
		if wantsSmite {
			canSmite, smiteErr := canUseDivineSmite(charID, smiteSlot, wantsFreeSmite(description))
			if canSmite {
				isUndead := isUndeadOrFiend(targetID)
				smiteDmg, smiteDice := calculateDivineSmiteDamage(smiteSlot, isUndead, false) // false = not crit
				dmg += smiteDmg
				addRider(charID, "radiant", smiteDmg)
				consumeSpellSlotForSmite(charID, smiteSlot, wantsFreeSmite(description))
				undeadBonus := ""
				if isUndead {
					undeadBonus = " +undead/fiend"
				}
				divineSmiteNote = fmt.Sprintf(" (+%d Divine Smite, %s radiant%s)", smiteDmg, smiteDice, undeadBonus) + smiteOfProtection(lobbyID, charID)
			} else {
				divineSmiteNote = fmt.Sprintf(" [Smite failed: %s]", smiteErr)
			}
		}

		// Lifedrinker (Warlock Invocation)
		// When you hit with your pact weapon, add CHA modifier as necrotic damage
		// Requires level 12+ and Pact of the Blade
		ldDmg, lifedrinkerNote := lifedrinker2024(charID, class, level, isRangedAttack, false, description) // Lifedrinker (2024)
		dmg += ldDmg

		// Hunter's Mark / Hex bonus damage on normal hit
		// +1d6 damage to attacks against marked target
		markDmg, markNote := getMarkBonusDamage(charID, targetID, false)
		dmg += markDmg

		// Include power attack note in normal hit result
		recordAttackOutcome(charID, attackOutcome{Weapon: weaponName, Total: totalAttack, Natural: attackRoll, Fumble: attackRoll == 1, Damage: dmg, DamageType: attackDamageType(hasWeapon, weapon), RollInfo: archeryNote + rollInfo, SneakUsed: sneakAttackNote != "", SneakNote: gwfNote + duelingNote + sneakAttackNote + colossusSlayerNote + divineStrikeNote + divineSmiteNote + lifedrinkerNote + markNote, Dice: damageDice, DiceMod: damageMod})
		return fmt.Sprintf("Attack with %s: %d to hit%s%s%s. Damage: %d%s%s%s%s%s%s%s%s", weaponName, totalAttack, archeryNote, powerAttackNote, rollInfo, dmg, gwfNote, duelingNote, colossusSlayerNote, divineStrikeNote, sneakAttackNote, divineSmiteNote, lifedrinkerNote, markNote)

	case "grapple", "shove":
		return playerUnarmedOption(charID, action, description) // F180 (items_c6_2024.go)

	case "cast":
		// Non-proficient armor blocks spellcasting entirely
		if isWearingNonProficientArmor(charID) {
			return "Cannot cast spells while wearing armor you are not proficient with"
		}

		// Wild Shape blocks spellcasting unless Druid has Beast Spells (level 18+)
		if isInWildShape(charID) && !hasBeastSpells(charID) {
			return "Cannot cast spells while in Wild Shape form. Druids gain Beast Spells at level 18"
		}

		// The HTTP action handler validates castSpellSlug before an action is
		// committed. Internal callers that predate structured spell slugs retain
		// the safe, unique-description fallback.
		spellKey := ""
		if len(castSpellSlug) > 0 {
			spellKey = castSpellSlug[0]
		}
		if spellKey == "" {
			spellKey = parseSpellFromDescription(description)
		}

		// Check if this is an offensive spell (deals damage or has save DC)
		// Casting offensive spells ends Sanctuary protection
		spellData, hasSpellData := srdSpellsMemory[spellKey]
		if hasSpellData && ((spellData.DamageDice != "" && !castDealsNoDamage(spellKey)) || spellData.SavingThrow != "") {
			removeSanctuaryOnOffensiveAction(charID)
		}
		spell, hasSpell := srdSpellsMemory[spellKey]

		// Check for ritual casting keyword
		descLower := strings.ToLower(description)
		isRitualCast := ritualWord.MatchString(descLower) // the word "ritual" ("spiritual weapon" isn't one)

		// Parse metamagic from description
		// Supports: "quickened fireball", "twinned haste", "subtle charm person", etc.
		var usedMetamagic []string
		var metamagicEffects []string
		var metamagicPointsCost int
		isSorcerer := strings.ToLower(class) == "sorcerer"

		if isSorcerer && level >= 2 { // 2024: Metamagic is learned at level 2
			metamagicKeywords := []string{"careful", "distant", "empowered", "extended", "heightened", "quickened", "seeking", "subtle", "transmuted", "twinned"}
			for _, mm := range metamagicKeywords {
				if hasWord(descLower, mm) && hasMetamagic(charID, mm) { // whole words: "be careful" was Careful Spell
					usedMetamagic = append(usedMetamagic, mm)
				}
			}
		}

		// Parse upcast slot level from description
		// Supports: "at level 5", "at 5th level", "using a level 5 slot", "using 5th level slot"
		requestedSlotLevel := 0
		upcastPatterns := []string{
			`at level (\d+)`,
			`at (\d+)(?:st|nd|rd|th) level`,
			`using (?:a )?level (\d+)`,
			`using (?:a )?(\d+)(?:st|nd|rd|th) level`,
			`with (?:a )?level (\d+)`,
			`with (?:a )?(\d+)(?:st|nd|rd|th) level`,
		}
		for _, pattern := range upcastPatterns {
			re := regexp.MustCompile(pattern)
			if matches := re.FindStringSubmatch(descLower); len(matches) > 1 {
				if lvl, err := strconv.Atoi(matches[1]); err == nil {
					requestedSlotLevel = lvl
					break
				}
			}
		}

		// Get spellcasting ability modifier
		classKey := strings.ToLower(class)
		spellMod := 0
		if c, ok := srdClasses[classKey]; ok {
			switch c.Spellcasting {
			case "INT":
				spellMod = game.Modifier(intl)
			case "WIS":
				spellMod = game.Modifier(wis)
			case "CHA":
				spellMod = game.Modifier(cha)
			}
		}

		// Species lineage and Magic Initiate spells use their own spellcasting ability (origin_spells_2024.go)
		if m, isOrigin := originSpellMod(charID, spellKey, classKey, intl, wis, cha, spellMod); isOrigin {
			spellMod = m
		}

		// Calculate spell save DC
		saveDC := game.SpellSaveDC(level, spellMod) + innateSorceryDC(charID, class) + magicSpellDCBonus(charID) // Robe of the Archmagi

		// A spell cast from a magic item or read from a Spell Scroll (magic_item_use_2024.go): no spell slot; the item's save DC
		// and attack bonus where it has them; charges spent or the scroll used up once the spell is cast.
		var fromItem *itemCastPlan
		if hasSpell {
			v, _ := castItems.Load(charID)
			icr, _ := v.(itemCastReq)
			native, _ := spellAvailableNatively(charID, spellKey, description)
			plan, refusal := planItemCast(charID, spellKey, spell, icr, requestedSlotLevel, description, !native)
			if refusal != "" {
				return refusal
			}
			fromItem = plan
		}
		if fromItem != nil {
			if fromItem.DC > 0 {
				saveDC = fromItem.DC
			}
			if fromItem.Attack > 0 {
				itemCastAttack.Store(charID, fromItem.Attack)
				defer itemCastAttack.Delete(charID)
			}
			if fromItem.Failed { // a Spell Scroll above your level: the check failed and the spell is gone
				note := finishItemCast(charID, fromItem)
				return fmt.Sprintf("You read the %s, but the spell slips away%s.%s", fromItem.ItemName, fromItem.CheckNote, note)
			}
		}

		if hasSpell {
			// Check spell components (V, S, M); added somatic enforcement
			conditions := getCharConditions(charID)
			var inventoryJSON, featsJSON []byte
			var equippedShield bool
			var mainHand string
			db.QueryRow("SELECT COALESCE(inventory, '[]'), COALESCE(equipped_shield, false), COALESCE(feats, '[]'), COALESCE(equipped_main_hand, '') FROM characters WHERE id = $1", charID).Scan(&inventoryJSON, &equippedShield, &featsJSON, &mainHand)
			var inventory []map[string]interface{}
			var feats []string
			json.Unmarshal(inventoryJSON, &inventory)
			json.Unmarshal(featsJSON, &feats)

			inventory = usableInventory(inventory) // items in a lost bag cannot be used
			focusClass := classKey
			if _, isOrigin := originSpellsOf(charID)[spellKey]; isOrigin && !containsStr(getClassSpellList(classKey), spellKey) {
				focusClass = "" // a species or Magic Initiate spell: the class focus doesn't work for it, a Component Pouch does
				if _, _, viaInvocation := getAtWillInvocationForSpell(charID, spellKey); viaInvocation {
					focusClass = "warlock" // cast through a Warlock invocation, as Armor of Shadows' Mage Armor is (F68: Fiendish Vigor's False Life when Magic Initiate also gave it)
				}
			}
			comps := spell.Components
			if fromItem != nil && fromItem.Scroll {
				comps = withoutMaterial(comps) // a Spell Scroll needs no Material components
			}
			// a Shield fills one hand; the other performs the Somatic component when it holds nothing, or a Spellcasting Focus that
			// stands in for the spell's Material component (F64: a Druid with the class kit's Shield and Quarterstaff focus)
			_ = equippedShield
			_ = mainHand
			if fromItem == nil || fromItem.Scroll { // a spell cast from a charged item needs no components at all
				if compErr := checkSpellComponents(comps, conditions, inventory, false, feats, usedMetamagic, focusClass, level); compErr != "" {
					if !(strings.Contains(compErr, "material components") && carriesNamedMaterial(spell.Material, inventory)) {
						return compErr
					}
					// F244: the character carries the material the spell names (a Holy Symbol for Bless): no focus or pouch needed
				}
				// the hands for Somatic and Material components, unless the campaign's table rule spell_hands is "ignored" (hands_2024.go)
				if why := castHandsProblem(lobbyOfChar(charID), charID, comps, spell.MaterialCost == 0 && !spell.MaterialConsumed, inventory, focusClass, feats, usedMetamagic); why != "" {
					return why
				}
			}

			// Check costly material components
			// 2024 Wild Companion casts Find Familiar without Material components
			wildCompanion := (strings.ToLower(classKey) == "druid" && spellKey == "find-familiar" && strings.Contains(strings.ToLower(description), "wild companion")) ||
				(strings.ToLower(classKey) == "sorcerer" && spellKey == "summon-dragon" && level >= 18) // Dragon Companion: no Material component
			materialToConsume := ""
			if spell.MaterialCost > 0 && !wildCompanion && fromItem == nil {
				matErr, foundMaterial := checkCostlyMaterial(inventory, spell.Material, spell.MaterialCost)
				if matErr != "" {
					return matErr
				}
				if spell.MaterialConsumed {
					materialToConsume = foundMaterial
				}
			}

			// One spell with a spell slot per turn: checked where the slot is spent (slotAlreadyUsedThisTurn).

			// Check if ritual casting is valid
			if isRitualCast && fromItem == nil {
				// Check if spell has ritual tag
				var canRitual bool
				db.QueryRow("SELECT COALESCE(is_ritual, false) FROM spells WHERE slug = $1", spellKey).Scan(&canRitual)
				if !canRitual {
					return fmt.Sprintf("Cannot cast %s as a ritual - spell does not have the ritual tag!", spell.Name)
				}
				// Ritual casting - no spell slot used, but takes 10 minutes longer
				return fmt.Sprintf("Ritual casting %s (takes 10 extra minutes, no spell slot used). (DC %d) %s", spell.Name, saveDC, spell.Description)
			}

			// Determine slot level to use (base spell level or upcast level)
			slotLevel := spell.Level
			if requestedSlotLevel > 0 {
				if requestedSlotLevel < spell.Level {
					return fmt.Sprintf("Cannot cast %s at level %d - spell requires at least level %d!", spell.Name, requestedSlotLevel, spell.Level)
				}
				if requestedSlotLevel > 9 {
					return fmt.Sprintf("Cannot cast at level %d - maximum spell slot level is 9!", requestedSlotLevel)
				}
				slotLevel = requestedSlotLevel
			}
			if fromItem != nil { // the item decides the level (the charges spent); no slot is used
				slotLevel, requestedSlotLevel = fromItem.Level, fromItem.Level
			}

			// Process Metamagic
			if len(usedMetamagic) > 0 {
				// Calculate costs
				for _, mm := range usedMetamagic {
					opt := metamagicOptions[mm]
					cost := opt.Cost
					metamagicPointsCost += cost
				}

				if okMM, mmMsg := metamagicAllowed(usedMetamagic, level >= 7 && innateSorceryDC(charID, class) > 0); !okMM {
					return mmMsg
				}

				// Arcane Apotheosis (level 20): while Innate Sorcery is active, one Metamagic option each turn costs no Sorcery Points
				if level >= 20 && strings.ToLower(class) == "sorcerer" && innateSorceryDC(charID, class) > 0 && classResourcesUsed(charID)["apotheosis"] == 0 {
					free := metamagicOptions[usedMetamagic[0]].Cost
					metamagicPointsCost -= free
					setUsedResource(charID, "apotheosis", 1)
					metamagicEffects = append(metamagicEffects, fmt.Sprintf("Arcane Apotheosis: %s costs no Sorcery Points this turn", usedMetamagic[0]))
				}

				// Check sorcery points (the same count Font of Magic and the rests use)
				currentPoints := getCurrentClassResources(charID)["sorcery_points"]
				if currentPoints < metamagicPointsCost {
					return fmt.Sprintf("Cannot cast %s with %v - it costs %d Sorcery Points and you have %d", spell.Name, usedMetamagic, metamagicPointsCost, currentPoints)
				}

				// Apply metamagic effects and build notes
				for _, mm := range usedMetamagic {
					opt := metamagicOptions[mm]
					cost := opt.Cost

					switch mm {
					case "quickened":
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Quickened (%d SP): cast as bonus action", cost))
					case "twinned":
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Twinned (%d SP): the spell's effective level is 1 higher for extra targets", cost))
					case "seeking":
						seekingSpell.Store(charID, true)
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Seeking (%d SP): if the spell attack misses, reroll the d20", cost))
					case "transmuted":
						spell.DamageType = transmutedType(descLower, spell.DamageType) // F198: "into fire" wins over the spell's own name
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Transmuted (%d SP): damage type is now %s", cost, spell.DamageType))
					case "subtle":
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Subtle (%d SP): no verbal/somatic components", cost))
					case "heightened":
						if ts := spellMonsterTargets(charID); len(ts) > 0 { // F194: the first target has Disadvantage on its saves against the spell
							heightenedTarget.Store(charID, ts[0])
							defer heightenedTarget.Delete(charID)
						}
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Heightened (%d SP): the first target has Disadvantage on saves against the spell", cost))
					case "empowered":
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Empowered (%d SP): reroll up to %d damage dice", cost, game.Modifier(cha)))
					case "extended":
						extendedCast.Store(charID, true) // F194: durations of 1 minute or longer double while this cast resolves
						defer extendedCast.Delete(charID)
						if strings.Contains(strings.ToLower(spell.Duration), "concentration") {
							extendedConc.Store(charID, spell.Name) // Advantage on saves to keep Concentration on it
						}
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Extended (%d SP): duration doubled (max 24h); Advantage on saves to keep Concentration on it", cost))
					case "distant":
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Distant (%d SP): range doubled (or 30ft if touch)", cost))
					case "careful":
						carefulLeft.Store(charID, max(1, game.Modifier(cha))) // F194: the characters the spell makes save (resolveSpellOnCharacters)
						defer carefulLeft.Delete(charID)
						metamagicEffects = append(metamagicEffects, fmt.Sprintf("Careful (%d SP): up to %d creatures auto-succeed on save", cost, max(1, game.Modifier(cha))))
					}
				}

				// Spend sorcery points
				useClassResource(charID, "sorcery_points", metamagicPointsCost)
			}

			// Check for once-per-rest invocation spell
			// Invocations like Thief of Five Fates grant casting a spell once per long rest using a warlock slot
			invocationUsedNote := ""
			oncePerRestInvocation := ""
			if slotLevel > 0 && (strings.ToLower(class) == "warlock" || classLevelsOf(charID)["warlock"] > 0) && fromItem == nil { // multiclass Warlocks too
				if invSlug, hasInv := getOncePerRestInvocationForSpell(charID, spellKey); hasInv {
					oncePerRestInvocation = invSlug
					if hasUsedInvocationSpell(charID, invSlug) {
						invName := game.AvailableInvocations[invSlug].Name
						return fmt.Sprintf("Cannot cast %s via %s - you've already used this invocation. Requires a long rest to use again.", spell.Name, invName)
					}
					invName := game.AvailableInvocations[invSlug].Name
					invocationUsedNote = fmt.Sprintf(" [%s: uses warlock slot, once per long rest]", invName)
					if invSlug == "gift-of-the-depths" { // 2024: Water Breathing once without expending a spell slot
						invocationUsedNote = " [Gift of the Depths: no spell slot; again after a Long Rest]"
						slotLevel = 0
					}
				}
			}

			// Check for at-will invocation spells
			// Invocations like Armor of Shadows grant casting spells at will without spell slots
			atWillInvocationNote := ""
			if slotLevel > 0 && (strings.ToLower(class) == "warlock" || classLevelsOf(charID)["warlock"] > 0) && fromItem == nil { // multiclass Warlocks too
				if invSlug, isSelfOnly, hasInv := getAtWillInvocationForSpell(charID, spellKey); hasInv {
					inv := game.AvailableInvocations[invSlug]

					// For self-only spells, validate the target
					if isSelfOnly {
						// Parse target from description
						descLower := strings.ToLower(description)
						targetsSelf := strings.Contains(descLower, "self") ||
							strings.Contains(descLower, "myself") ||
							strings.Contains(descLower, "on me") ||
							!strings.Contains(descLower, " on ") // If no explicit target, assume self

						if !targetsSelf {
							return fmt.Sprintf("Cannot cast %s on another creature: %s can only target yourself. Cast as: '%s on self'", spell.Name, inv.Name, spell.Name)
						}
					}

					// At-will invocation found - no spell slot needed
					atWillInvocationNote = fmt.Sprintf(" [%s: cast at will]", inv.Name)
					slotLevel = 0 // No slot consumption
				}
			}

			// 2024: Hunter's Mark (Favored Enemy) and Find Steed (Faithful Steed) can be cast without a slot a few times per Long Rest
			if slotLevel > 0 && fromItem == nil {
				if freeNote, freeErr := tryFreeCast(charID, class, level, spellKey, description, spell.Level); freeErr != "" {
					return freeErr
				} else if freeNote != "" {
					atWillInvocationNote += freeNote
					slotLevel = 0
				}
			}

			// Check for Mystic Arcanum casting (Warlock level 11+)
			// Keywords: "arcanum", "mystic arcanum", "using arcanum"
			useMysticArcanum := fromItem == nil && (strings.Contains(descLower, "arcanum") ||
				strings.Contains(descLower, "mystic arcanum") ||
				strings.Contains(descLower, "using arcanum"))
			mysticArcanumNote := ""

			if useMysticArcanum && slotLevel >= 6 && slotLevel <= 9 {
				// Check if Warlock with the appropriate arcanum
				if strings.ToLower(class) != "warlock" {
					return "Cannot cast with Mystic Arcanum: it is a Warlock feature"
				}

				warlockLevel := level
				// For multiclass, get warlock level
				var classLevelsJSON []byte
				db.QueryRow("SELECT COALESCE(class_levels, '{}') FROM characters WHERE id = $1", charID).Scan(&classLevelsJSON)
				var classLevels map[string]int
				json.Unmarshal(classLevelsJSON, &classLevels)
				if len(classLevels) > 1 {
					for c, lvl := range classLevels {
						if strings.ToLower(c) == "warlock" {
							warlockLevel = lvl
							break
						}
					}
				}

				requiredLevel := getMysticArcanumLevelRequirement(slotLevel)
				if warlockLevel < requiredLevel {
					return fmt.Sprintf("Cannot cast with Mystic Arcanum: level %d spells need Warlock level %d (current: %d)", slotLevel, requiredLevel, warlockLevel)
				}

				// Check if this spell is chosen as arcanum at this level
				var arcanumJSON, usedJSON []byte
				db.QueryRow("SELECT COALESCE(mystic_arcanum, '{}'), COALESCE(mystic_arcanum_used, '[]') FROM characters WHERE id = $1", charID).Scan(&arcanumJSON, &usedJSON)
				var arcanum map[string]string
				var usedLevels []int
				json.Unmarshal(arcanumJSON, &arcanum)
				json.Unmarshal(usedJSON, &usedLevels)

				chosenSpell := arcanum[strconv.Itoa(slotLevel)]
				if chosenSpell == "" {
					return fmt.Sprintf("Cannot cast with Mystic Arcanum: no level %d Mystic Arcanum chosen. Use POST /api/characters/mystic-arcanum to choose one.", slotLevel)
				}
				if chosenSpell != spellKey {
					return fmt.Sprintf("Cannot cast %s with Mystic Arcanum: your level %d Mystic Arcanum is %s", spell.Name, slotLevel, chosenSpell)
				}

				// Check if already used
				for _, usedLvl := range usedLevels {
					if usedLvl == slotLevel {
						return fmt.Sprintf("Cannot cast %s with Mystic Arcanum: the level %d Mystic Arcanum is used until you finish a Long Rest.", spell.Name, slotLevel)
					}
				}

				// Mark as used
				usedLevels = append(usedLevels, slotLevel)
				usedJSONNew, _ := json.Marshal(usedLevels)
				db.Exec("UPDATE characters SET mystic_arcanum_used = $1 WHERE id = $2", usedJSONNew, charID)

				mysticArcanumNote = fmt.Sprintf(" [Mystic Arcanum: %dth-level, no slot used]", slotLevel)
				slotLevel = 0 // No slot consumption
			}

			// A spell from an item: spend its charges or use up the scroll now (no spell slot)
			if fromItem != nil {
				itemCastNotes.Store(charID, fromItem.CheckNote+finishItemCast(charID, fromItem))
			}

			// Check and use spell slot (non-ritual)
			// Support separate pact slot tracking for multiclass Warlocks
			// Pact of the Chain: "You learn the Find Familiar spell and can cast it as a Magic action without expending a spell slot."
			chainFamiliar := spellKey == "find-familiar" && hasPactBoon(charID, "chain")
			if slotLevel > 0 && fromItem == nil && !chainFamiliar {
				// Check if using pact slot (from description: "pact slot", "pact magic", "warlock slot")
				// Once-per-rest invocation spells implicitly use pact slot
				usePactSlot := strings.Contains(descLower, "pact slot") ||
					strings.Contains(descLower, "pact magic") ||
					strings.Contains(descLower, "warlock slot") ||
					oncePerRestInvocation != ""

				// Check for multiclass Warlock
				var classLevelsJSON []byte
				db.QueryRow("SELECT COALESCE(class_levels, '{}') FROM characters WHERE id = $1", charID).Scan(&classLevelsJSON)
				var classLevels map[string]int
				json.Unmarshal(classLevelsJSON, &classLevels)

				warlockLvl := 0
				isMulticlass := len(classLevels) > 1
				for c, lvl := range classLevels {
					if strings.ToLower(c) == "warlock" {
						warlockLvl = lvl
						break
					}
				}

				// a multiclass Warlock with no regular slot of this level left casts with a Pact Magic slot without having to say so
				// (SRD 5.2.1: Pact Magic slots can cast the other classes' prepared spells, and the other slots Warlock spells)
				if isMulticlass && warlockLvl > 0 && !usePactSlot {
					var regUsedJSON []byte
					db.QueryRow("SELECT COALESCE(spell_slots_used, '{}') FROM characters WHERE id = $1", charID).Scan(&regUsedJSON)
					regUsed := map[string]int{}
					json.Unmarshal(regUsedJSON, &regUsed)
					if n := characterSpellSlots(charID, class, level)[slotLevel]; n == 0 || regUsed[fmt.Sprintf("%d", slotLevel)] >= n {
						usePactSlot = true
					}
				}

				// For multiclass Warlocks, handle pact slots separately
				if isMulticlass && warlockLvl > 0 && usePactSlot {
					pactSlots := game.SpellSlots("warlock", warlockLvl)
					// Warlock slots are all at same level - find the level
					pactSlotLevel := 0
					pactSlotCount := 0
					for lvl, count := range pactSlots {
						pactSlotLevel = lvl
						pactSlotCount = count
					}

					if slotLevel > pactSlotLevel {
						return fmt.Sprintf("Cannot cast %s with pact slot - your pact slots are level %d!", spell.Name, pactSlotLevel)
					}

					// Get used pact slots
					var usedJSON []byte
					db.QueryRow("SELECT COALESCE(pact_slots_used, '{}') FROM characters WHERE id = $1", charID).Scan(&usedJSON)
					var used map[string]int
					json.Unmarshal(usedJSON, &used)

					usedKey := fmt.Sprintf("%d", pactSlotLevel)
					usedSlots := used[usedKey]
					if usedSlots >= pactSlotCount {
						return fmt.Sprintf("Cannot cast %s - no pact magic slots remaining!", spell.Name)
					}

					if why := slotAlreadyUsedThisTurn(charID, spell.Name); why != "" {
						return why
					}
					// Use the pact slot
					markSlotUsedThisTurn(charID)
					used[usedKey] = usedSlots + 1
					updatedJSON, _ := json.Marshal(used)
					db.Exec("UPDATE characters SET pact_slots_used = $1 WHERE id = $2", updatedJSON, charID)
					slotLevel = pactSlotLevel // Pact slots are always at their level
				} else {
					// Use regular spell slots
					slots := characterSpellSlots(charID, class, level)
					if strings.EqualFold(class, "warlock") && !isMulticlass {
						// Pact Magic: every slot is of the same level, and a Warlock spell is cast with one of them at that level
						for lvl := range slots {
							if lvl >= slotLevel {
								slotLevel = lvl
							}
						}
					}
					totalSlots, hasSlot := slots[slotLevel]
					if !hasSlot || totalSlots == 0 {
						return fmt.Sprintf("Cannot cast %s - you don't have level %d spell slots!", spell.Name, slotLevel)
					}

					// Get used slots
					var usedJSON []byte
					db.QueryRow("SELECT COALESCE(spell_slots_used, '{}') FROM characters WHERE id = $1", charID).Scan(&usedJSON)
					var used map[string]int
					json.Unmarshal(usedJSON, &used)

					usedKey := fmt.Sprintf("%d", slotLevel)
					usedSlots := used[usedKey]
					if usedSlots >= totalSlots {
						return fmt.Sprintf("Cannot cast %s - no level %d spell slots remaining!", spell.Name, slotLevel)
					}

					if why := slotAlreadyUsedThisTurn(charID, spell.Name); why != "" {
						return why
					}
					markSlotUsedThisTurn(charID)
					// Use the slot (Boon of Spell Recall: a level 1-4 slot may stay unspent)
					if !spellRecall(charID, slotLevel) {
						used[usedKey] = usedSlots + 1
						updatedJSON, _ := json.Marshal(used)
						db.Exec("UPDATE characters SET spell_slots_used = $1 WHERE id = $2", updatedJSON, charID)
					}
				}
			}

			// Handle concentration
			if strings.Contains(strings.ToLower(spell.Duration), "concentration") {
				// For Hunter's Mark and Hex, store target ID with concentration
				// This enables tracking bonus damage on attacks against marked target
				concentrationValue := spell.Name
				if isMarkSpell(spellKey) {
					// Parse target from description (e.g., "cast hex on goblin" or "hunter's mark the orc")
					castTargetID := parseTargetFromDescription(description, charID)
					if _, mid, isMonster := attackedMonster(charID); castTargetID <= 0 && isMonster {
						castTargetID = mid // a monster picked with target_id
					}
					if castTargetID != 0 {
						concentrationValue = fmt.Sprintf("%s:%d", spell.Name, castTargetID)
					}
				}
				// Drop current concentration
				db.Exec("UPDATE characters SET concentrating_on = $1 WHERE id = $2", concentrationValue, charID)
				pruneConcentrationByCaster(charID) // the old spell's conditions end now (Invisibility on an ally ...)
			}

			// Consume material component if spell requires it
			materialConsumedNote := ""
			if materialToConsume != "" {
				consumeSpellMaterial(charID, materialToConsume)
				materialConsumedNote = fmt.Sprintf(" [Consumed: %s]", materialToConsume)
			}
			materialConsumedNote += takeSpellRecallNote(charID)

			// Mark once-per-rest invocation spell as used
			if oncePerRestInvocation != "" {
				markInvocationSpellUsed(charID, oncePerRestInvocation)
			}

			// Determine damage/healing dice based on slot level (upcasting)
			upcastInfo := ""
			if requestedSlotLevel > spell.Level {
				upcastInfo = fmt.Sprintf(" (upcast at level %d)", requestedSlotLevel)
			}

			// Build metamagic note for output
			metamagicNote := ""
			if len(metamagicEffects) > 0 {
				metamagicNote = fmt.Sprintf(" [Metamagic: %s]", strings.Join(metamagicEffects, "; "))
			}

			if pw := resolvePowerWord(charID, spellKey, saveDC, description); pw != "" { // Power Word Kill, Stun, Heal; Words of Creation
				return fmt.Sprintf("Cast %s%s!%s%s%s%s%s %s", spell.Name, upcastInfo, pw, metamagicNote, materialConsumedNote, mysticArcanumNote, atWillInvocationNote, spell.Description)
			}
			if spell.DamageDice != "" && !castDealsNoDamage(spellKey) && !strings.Contains(atWillInvocationNote, "Contact Patron") { // Hunter's Mark and Hex deal their damage on later hits; Wish's and Teleport's fall on the caster // Contact Patron: the save is an automatic success
				// Check for upcast damage
				damageDice := spell.DamageDice
				slotKey := fmt.Sprintf("%d", slotLevel)
				if len(spell.DamageAtSlotLevel) > 0 {
					if upcastDice, ok := spell.DamageAtSlotLevel[slotKey]; ok {
						damageDice = upcastDice
					}
				}

				// Cantrip damage scaling based on character level (a cantrip from an item works at the lowest caster level, or at the
				// level of the character who scribed the scroll)
				if spell.Level == 0 && len(spell.DamageAtCharLevel) > 0 {
					cantripLevel := level
					if fromItem != nil {
						cantripLevel = max(1, fromItem.CasterLevel)
					}
					damageDice = game.ScaledCantripDamage(spell.DamageAtCharLevel, cantripLevel)
				}

				// Overchannel (Evocation Wizard level 14+)
				// Deal maximum damage with wizard spell of 1st-5th level
				// First use per long rest is free. Subsequent uses: 2d12 necrotic per spell level
				useOverchannel := strings.Contains(descLower, "overchannel") || strings.Contains(descLower, "over channel")
				overchannelNote := ""
				overchannelPenaltyNote := ""

				if useOverchannel {
					// Check wizard level
					wizardLevel := getWizardLevel(charID)

					// Check subclass
					var wizSubclass sql.NullString
					db.QueryRow("SELECT subclass FROM characters WHERE id = $1", charID).Scan(&wizSubclass)
					isEvocationWizard := wizSubclass.Valid && wizSubclass.String == "evocation"

					if wizardLevel < 14 || !isEvocationWizard {
						return "Cannot cast with Overchannel: it needs an Evoker Wizard of level 14"
					}

					if spell.Level < 1 || spell.Level > 5 {
						return fmt.Sprintf("Cannot cast %s with Overchannel: it works on Wizard spells of levels 1 to 5, and %s is level %d.", spell.Name, spell.Name, spell.Level)
					}

					// Check if first use this long rest (free) or subsequent (necrotic penalty)
					var overchannelUsed bool
					db.QueryRow("SELECT COALESCE(overchannel_used, false) FROM characters WHERE id = $1", charID).Scan(&overchannelUsed)

					if overchannelUsed {
						// Subsequent use - take 2d12 necrotic damage per spell level
						necroticDamage := 0
						for i := 0; i < slotLevel; i++ {
							necroticDamage += game.RollDie(12) + game.RollDie(12)
						}

						// Apply necrotic damage to caster (ignores resistance and immunity per PHB)
						var currentHP, maxHP int
						db.QueryRow("SELECT hp, max_hp FROM characters WHERE id = $1", charID).Scan(&currentHP, &maxHP)
						newHP := currentHP - necroticDamage
						if newHP < 0 {
							newHP = 0
						}
						db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", newHP, charID)

						overchannelPenaltyNote = fmt.Sprintf(" ⚠️ Overchannel penalty: took %d necrotic damage (cannot be reduced)!", necroticDamage)
					} else {
						// Mark first use
						db.Exec("UPDATE characters SET overchannel_used = true WHERE id = $1", charID)
					}

					overchannelNote = " (Overchannel: maximum damage)"
				}

				var dmg int
				if useOverchannel {
					dmg = game.RollDamageMax(damageDice)
				} else if containsStr(usedMetamagic, "empowered") { // F194: Empowered Spell rerolls up to Cha modifier dice
					var empNote string
					dmg, empNote = rollEmpowered(damageDice, max(1, game.Modifier(cha)))
					overchannelNote += empNote
				} else {
					dmg = game.RollDamage(damageDice, false)
				}
				dmg += potentSpellcastingBonus(charID, class, level, spell.Level) // Potent Spellcasting (Cleric, Druid level 7)

				// Elemental Affinity (Draconic Sorcerer level 6+)
				// Add CHA mod to damage when spell damage type matches dragon ancestry
				elementalAffinityNote := ""
				if subclass.Valid && subclass.String == "draconic" {
					if hasSubclassFeature(subclass.String, level, "elemental_affinity") {
						ancestryDamageType := getDragonAncestryDamageType(charID)
						if ancestryDamageType != "" && strings.ToLower(spell.DamageType) == ancestryDamageType {
							chaBonus := game.Modifier(cha)
							dmg += chaBonus
							elementalAffinityNote = fmt.Sprintf(" (Elemental Affinity: +%d)", chaBonus)
						}
					}
				}

				if b, note := empoweredEvocation(charID, spellKey, spell); b > 0 { // F119 (wizard_fixes_2024.go)
					dmg += b
					elementalAffinityNote += note
				}
				sculptSpells(charID, spellKey, spell, slotLevel, description) // F119

				// Agonizing Blast (Warlock Invocation)
				// Add CHA mod to eldritch blast damage
				agonizingBlastNote := ""
				if spellKey == "eldritch-blast" && hasInvocation(charID, "agonizing-blast") {
					chaBonus := game.Modifier(cha)
					if chaBonus > 0 {
						dmg += chaBonus
						agonizingBlastNote = fmt.Sprintf(" (Agonizing Blast: +%d)", chaBonus)
					}
				}

				// Repelling Blast (Warlock Invocation)
				// Push target 10 feet away on eldritch blast hit
				repellingBlastNote := ""
				if spellKey == "eldritch-blast" && hasInvocation(charID, "repelling-blast") {
					repellingBlastNote = " (Repelling Blast: target pushed 10 feet away)"
				}

				// Eldritch Spear (Warlock Invocation)
				// Extends eldritch blast range to 300 feet (normal is 120 feet)
				eldritchSpearNote := ""
				if spellKey == "eldritch-blast" && hasInvocation(charID, "eldritch-spear") {
					eldritchSpearNote = fmt.Sprintf(" (Eldritch Spear: range %d feet)", spellRangeFeet(spell.Range)+eldritchSpearBonus(charID)) // F195
				}

				saveInfo := ""
				if len(spell.DamageParts) > 0 {
					saveInfo = primarySaveInfo(spell.DamageParts, saveDC) + extraPartsText(spell.DamageParts, slotLevel, saveDC)
				} else if spell.SavingThrow != "" {
					if dl := strings.ToLower(spell.Description); strings.Contains(dl, "half as much damage") || strings.Contains(dl, "half damage") {
						saveInfo = fmt.Sprintf(" (DC %d %s save for half)", saveDC, spell.SavingThrow)
					} else {
						saveInfo = fmt.Sprintf(" (DC %d %s save: no damage on a success)", saveDC, spell.SavingThrow) // F247
					}
				}
				return withSpellResolution(charID, spellKey, spell, damageDice, dmg, slotLevel, level, spellMod, saveDC, fmt.Sprintf("Cast %s%s! %d %s damage%s.%s%s%s%s%s%s%s%s%s%s%s %s", spell.Name, upcastInfo, dmg, spell.DamageType, saveInfo, overchannelNote, overchannelPenaltyNote, elementalAffinityNote, agonizingBlastNote, repellingBlastNote, eldritchSpearNote, metamagicNote, materialConsumedNote, invocationUsedNote, mysticArcanumNote, atWillInvocationNote, spell.Description))
			} else if spell.Healing != "" && buffSpells[spellKey].Kind == "" { // Aid raises the maximum instead (buff_spells_2024.go)
				// Check for upcast healing
				healDice := spell.Healing
				slotKey := fmt.Sprintf("%d", slotLevel)
				if len(spell.HealAtSlotLevel) > 0 {
					if upcastDice, ok := spell.HealAtSlotLevel[slotKey]; ok {
						// Replace "MOD" with actual modifier
						healDice = strings.Replace(upcastDice, " + MOD", "", 1)
						healDice = strings.Replace(healDice, "+ MOD", "", 1)
						healDice = strings.Replace(healDice, "+MOD", "", 1)
					}
				}

				// Life Domain Cleric healing bonuses
				bonusInfo := ""
				heal := 0
				subclassSlug := ""
				if subclass.Valid {
					subclassSlug = subclass.String
				}

				// the spellcasting ability modifier only where the spell says so (Cure Wounds, Healing Word; not Heal or Regenerate)
				healMod := 0
				if strings.Contains(strings.ToLower(spell.Description), "spellcasting ability modifier") {
					healMod = spellMod
				}
				if flat, err := strconv.Atoi(strings.TrimSpace(healDice)); err == nil {
					// a fixed amount: Heal restores 70 Hit Points (80, 90, 100 with higher slots: heal_at_slot_level); no dice to roll or maximize
					heal = flat
				} else if hasSubclassFeature(subclassSlug, level, "supreme_healing") {
					// Supreme Healing (Life Domain level 17): the highest number on each die instead of rolling
					heal = game.RollDamageMax(healDice) + healMod
					bonusInfo = " (Supreme Healing: max dice)"
				} else {
					heal = game.RollDamage(healDice, false) + healMod
				}
				healMax := heal // Beacon of Hope: healing at its maximum (applySpellHealing)
				if _, err := strconv.Atoi(strings.TrimSpace(healDice)); err != nil {
					healMax = game.RollDamageMax(healDice) + healMod
				}

				// Check for Disciple of Life (Life Domain level 1) - add 2 + spell level bonus
				// Only applies to spells of 1st level or higher
				if slotLevel >= 1 && hasSubclassFeature(subclassSlug, level, "bonus_healing") {
					bonusHealing := 2 + slotLevel
					heal += bonusHealing
					healMax += bonusHealing
					if bonusInfo != "" {
						bonusInfo = fmt.Sprintf(" (Supreme Healing + Disciple of Life: +%d)", bonusHealing)
					} else {
						bonusInfo = fmt.Sprintf(" (Disciple of Life: +%d)", bonusHealing)
					}
				}

				// Blessed Healer (Life Domain level 6) - heal self when healing others
				// When you cast a healing spell on a creature other than yourself, you regain 2 + spell level HP
				blessedHealerInfo := ""
				if slotLevel >= 1 && hasSubclassFeature(subclassSlug, level, "blessed_healer") {
					// Parse target from description to see if healing someone else
					targetID := parseTargetFromDescription(description, charID)
					descLower := strings.ToLower(description)
					healingSelf := targetID == charID || targetID == 0 ||
						strings.Contains(descLower, "self") || strings.Contains(descLower, "myself")
					for _, t := range spellCharacterTargets(charID) { // target_id / target_ids name another creature (F83)
						if t != charID {
							healingSelf = false
						}
					}

					if !healingSelf {
						// Healing another creature - heal self too
						selfHeal := 2 + slotLevel

						// Get current HP and max HP
						var selfHP, selfMaxHP int
						db.QueryRow("SELECT hp, max_hp FROM characters WHERE id = $1", charID).Scan(&selfHP, &selfMaxHP)

						newSelfHP := selfHP + selfHeal
						if newSelfHP > selfMaxHP {
							newSelfHP = selfMaxHP
						}
						actualSelfHeal := newSelfHP - selfHP

						if actualSelfHeal > 0 {
							db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", newSelfHP, charID)
							blessedHealerInfo = fmt.Sprintf(" Blessed Healer: you also heal %d HP!", actualSelfHeal)
						}
					}
				}

				healText := applySpellHealing(charID, heal, healMax, description)
				return fmt.Sprintf("Cast %s%s! Heals %d HP%s.%s%s%s%s%s%s %s", spell.Name, upcastInfo, heal, bonusInfo, healText, metamagicNote, materialConsumedNote, invocationUsedNote, atWillInvocationNote, blessedHealerInfo, spell.Description)
			}
			conditionText := companionFromSpell(charID, spellKey, slotLevel, description) // Find Familiar, Find Steed, summons (companions_2024.go)
			if conditionText == "" {
				conditionText = resolveEffectSpell(charID, spell, spellKey, slotLevel, saveDC, description) // Bless, Bane, Faerie Fire, Slow, Command...
			}
			if conditionText == "" {
				conditionText = resolveConditionSpell(charID, spellKey, saveDC, spell.Name)
			}
			dcNote := "" // only a spell with a saving throw has a DC to show (F229: Bless showed "DC 13")
			if spell.SavingThrow != "" {
				dcNote = fmt.Sprintf(" (DC %d)", saveDC)
			}
			return fmt.Sprintf("Cast %s%s!%s%s%s%s%s%s%s %s", spell.Name, upcastInfo, dcNote, conditionText, metamagicNote, materialConsumedNote, invocationUsedNote, mysticArcanumNote, atWillInvocationNote, spell.Description)
		}
		return fmt.Sprintf("Cast spell: %s (Save DC: %d)", description, saveDC)

	case "death_save":
		// Death saving throw
		roll := game.RollDie(20)
		if championDefyDeath(charID) { // Champion level 18, Defy Death: Advantage, and 18-20 counts as 20
			if second := game.RollDie(20); second > roll {
				roll = second
			}
			if roll >= 18 {
				roll = 20
			}
		}

		dsAdvNote := ""
		if ok, why := buffDeathSaveAdvantage(charID); ok { // Beacon of Hope, Foresight (buff_spells_2024.go)
			if second := game.RollDie(20); second > roll {
				roll = second
			}
			dsAdvNote = "[Advantage: " + why + "] "
		}

		// Halfling Lucky - reroll nat 1s on death saves
		dsHalflingLuckyUsed := false
		dsHalflingLuckyOriginal := 0
		if roll == 1 {
			newRoll, rerolled, origRoll := applyHalflingLucky(roll, charID)
			if rerolled {
				dsHalflingLuckyUsed = true
				dsHalflingLuckyOriginal = origRoll
				roll = newRoll
			}
		}

		if roll <= 9 && periaptActive(charID) { // Periapt of Wound Closure: a 9 or lower becomes a 10 (F142)
			dsAdvNote += fmt.Sprintf("[Periapt of Wound Closure: %d becomes 10] ", roll)
			roll = 10
		}

		luckyPrefix := ""
		if dsHalflingLuckyUsed {
			luckyPrefix = fmt.Sprintf("🍀 Lucky reroll (%d→%d)! ", dsHalflingLuckyOriginal, roll)
		}
		luckyPrefix = dsAdvNote + luckyPrefix

		var successes, failures int
		db.QueryRow("SELECT death_save_successes, death_save_failures FROM characters WHERE id = $1", charID).Scan(&successes, &failures)

		if roll == 20 {
			// Natural 20: regain 1 HP and wake up
			db.Exec("UPDATE characters SET hp = 1, death_save_successes = 0, death_save_failures = 0, is_stable = false WHERE id = $1", charID)
			return fmt.Sprintf("%sDeath save: Natural 20! You regain consciousness with 1 HP!", luckyPrefix)
		} else if roll == 1 && !dsHalflingLuckyUsed {
			// Natural 1: two failures (only if not rerolled by Halfling Lucky)
			failures += 2
			if failures >= 3 {
				db.Exec("UPDATE characters SET death_save_failures = $1, is_dead = true WHERE id = $2", failures, charID)
				return fmt.Sprintf("Death save: Natural 1 (2 failures)! Total: %d failures. YOU HAVE DIED.", failures)
			}
			db.Exec("UPDATE characters SET death_save_failures = $1 WHERE id = $2", failures, charID)
			return fmt.Sprintf("Death save: Natural 1 (2 failures)! Total: %d successes, %d failures.", successes, failures)
		} else if roll >= 10 {
			successes++
			if successes >= 3 {
				db.Exec("UPDATE characters SET death_save_successes = 0, death_save_failures = 0, is_stable = true, stable_rounds_left = "+stableRounds()+" WHERE id = $1", charID) // F210: "The number of both is reset to zero when you ... become Stable" (SRD 5.2.1)
				return fmt.Sprintf("%sDeath save: %d - Success! Total: %d successes. You are STABLE.", luckyPrefix, roll, successes)
			}
			db.Exec("UPDATE characters SET death_save_successes = $1 WHERE id = $2", successes, charID)
			return fmt.Sprintf("%sDeath save: %d - Success! Total: %d successes, %d failures.", luckyPrefix, roll, successes, failures)
		} else {
			failures++
			if failures >= 3 {
				db.Exec("UPDATE characters SET death_save_failures = $1, is_dead = true WHERE id = $2", failures, charID)
				return fmt.Sprintf("%sDeath save: %d - Failure! Total: %d failures. YOU HAVE DIED.", luckyPrefix, roll, failures)
			}
			db.Exec("UPDATE characters SET death_save_failures = $1 WHERE id = $2", failures, charID)
			return fmt.Sprintf("%sDeath save: %d - Failure! Total: %d successes, %d failures.", luckyPrefix, roll, successes, failures)
		}

	case "concentration_check":
		// Concentration check when taking damage
		// DC is 10 or half the damage, whichever is higher, up to a maximum DC of 30 (SRD 5.2.1)
		// Parse damage from description if provided
		dc := 10
		if dmgMatch := strings.Fields(description); len(dmgMatch) > 0 {
			if dmg, err := strconv.Atoi(dmgMatch[0]); err == nil && dmg/2 > 10 {
				dc = dmg / 2
			}
		}
		if dc > 30 {
			dc = 30
		}

		// Constitution saving throw: proficiency only if the class (or Disciplined Survivor) gives it; Eldritch Mind gives Advantage
		var conScore int
		db.QueryRow("SELECT con FROM characters WHERE id = $1", charID).Scan(&conScore)
		conMod := game.Modifier(conScore)
		conBonus := conMod
		if conSaveProficient(charID, class, level) {
			conBonus += game.ProficiencyBonus(level)
		}
		roll := game.RollDie(20)
		rollNote := ""
		if hasInvocation(charID, "eldritch-mind") || charHasWarCaster(charID) || extendedConcentration(charID) {
			if second := game.RollDie(20); second > roll {
				roll = second
			}
			rollNote = " (Eldritch Mind: Advantage)"
			if !hasInvocation(charID, "eldritch-mind") {
				rollNote = " (War Caster: Advantage)"
				if !charHasWarCaster(charID) {
					rollNote = " (Extended Spell: Advantage)"
				}
			}
		}
		total := roll + conBonus
		conMod = conBonus

		var concSpell string
		db.QueryRow("SELECT COALESCE(concentrating_on, '') FROM characters WHERE id = $1", charID).Scan(&concSpell)

		if total >= dc {
			return fmt.Sprintf("Concentration check (DC %d): %d + %d = %d%s - SUCCESS! Maintaining %s.", dc, roll, conMod, total, rollNote, concSpell)
		} else {
			db.Exec("UPDATE characters SET concentrating_on = NULL WHERE id = $1", charID)
			return fmt.Sprintf("Concentration check (DC %d): %d + %d = %d%s - FAILED! Lost concentration on %s.", dc, roll, conMod, total, rollNote, concSpell)
		}

	case "move":
		return fmt.Sprintf("Movement: %s", description)
	case "help":
		return helpAction(charID, description)
	case "dodge":
		// Add dodge condition
		var existing []byte
		db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&existing)
		conds := normalizeConditionList(parseConditionsJSON(existing))
		if !hasCondition(charID, "dodging") {
			conds = append(conds, "dodging")
		}
		updated, _ := json.Marshal(conds)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updated, charID)
		return "Dodging. Attacks against you have disadvantage until your next turn."

	case "rage":
		// Barbarian rage - add raging condition
		// Rage grants: advantage on STR checks/saves, +2 damage on STR melee attacks,
		// resistance to bludgeoning/piercing/slashing damage
		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if bl := classLevelIn(charID, class, level, "barbarian"); bl > 0 && classKey != "barbarian" {
			classKey, level = "barbarian", bl // a Barbarian taken later (multiclass): Rage at the Barbarian level
		}
		if classKey != "barbarian" {
			return "Only barbarians can rage!"
		}

		// Check if already raging
		var existingConds []byte
		db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&existingConds)
		var currentConds []string
		json.Unmarshal(existingConds, &currentConds)
		for _, c := range currentConds {
			if c == "raging" {
				return "You are already raging!"
			}
		}

		// 2024: you cannot enter a Rage while wearing Heavy armor
		if wearingHeavyArmor(charID) {
			return "You can't enter a Rage while wearing Heavy armor."
		}

		// a Rage spends one of its uses (2 at level 1, up to 6; one comes back on a Short Rest, all on a Long Rest). Nothing spent
		// them before, so a Barbarian could rage without limit.
		if okRage, msg, _ := useClassResource(charID, "rage", 1); !okRage {
			return "You have no Rage uses left: " + msg
		}
		// Add raging condition
		currentConds = append(currentConds, "raging")
		updatedConds, _ := json.Marshal(currentConds)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updatedConds, charID)
		startRageClock(charID)

		// Build response with subclass-specific info
		rageInfo := fmt.Sprintf("⚔️ RAGE! While raging: Advantage on Strength checks and saving throws, +%d damage on Strength attacks, Resistance to Bludgeoning, Piercing and Slashing damage; you can't cast spells or keep Concentration.", game.RageDamageBonus(level))

		// Check for Berserker features
		if subclass.Valid && subclass.String == "berserker" {
			if level >= 6 {
				// Mindless Rage: Immunity to Charmed and Frightened while the Rage is active; the conditions end when it starts
				removeCondition(charID, "charmed")
				removeCondition(charID, "frightened")
				rageInfo += " (Mindless Rage: you are immune to Charmed and Frightened while raging, and any you had have ended.)"
			}
			if level >= 3 {
				rageInfo += " Frenzy: if you attack recklessly, the first target you hit with a Strength attack this turn takes extra damage."
			}
		}
		if level >= 7 {
			var species string
			db.QueryRow(`SELECT COALESCE(species,'') FROM characters WHERE id = $1`, charID).Scan(&species)
			pounce := characterSpeed(charID, species) / 2
			db.Exec(`UPDATE characters SET movement_remaining = COALESCE(movement_remaining,0) + $1 WHERE id = $2`, pounce, charID)
			rageInfo += fmt.Sprintf(" Instinctive Pounce: %d feet of extra movement as part of entering the Rage.", pounce)
		}

		return rageInfo

	case "frenzy", "frenzy_attack":
		return "Frenzy works differently in the 2024 rules: while raging, if you attack recklessly (include \"reckless\" in the attack description), the first target you hit with a Strength attack on your turn takes extra damage. There is no extra attack and no Exhaustion."

	case "end_rage":
		// End rage early
		// Check for frenzy exhaustion
		var existingConds []byte
		db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&existingConds)
		var currentConds []string
		json.Unmarshal(existingConds, &currentConds)

		wasRaging := false
		wasFrenzying := false
		newConds := []string{}
		for _, c := range currentConds {
			if c == "raging" {
				wasRaging = true
			} else if c == "frenzying" {
				wasFrenzying = true
			} else {
				newConds = append(newConds, c)
			}
		}

		if !wasRaging {
			return "You are not currently raging."
		}

		// Apply frenzy exhaustion if was frenzying
		result := "Your rage ends."
		if wasFrenzying {
			// Get current exhaustion and increment
			var currentExhaustion int
			db.QueryRow("SELECT COALESCE(exhaustion_level, 0) FROM characters WHERE id = $1", charID).Scan(&currentExhaustion)
			newExhaustion := currentExhaustion + 1
			if newExhaustion > 6 {
				newExhaustion = 6
			}

			// Update or add exhaustion condition
			updatedConditions := []string{}
			foundExhaustion := false
			for _, c := range newConds {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c)), "exhaustion:") {
					updatedConditions = append(updatedConditions, fmt.Sprintf("exhaustion:%d", newExhaustion))
					foundExhaustion = true
				} else {
					updatedConditions = append(updatedConditions, c)
				}
			}
			if !foundExhaustion {
				updatedConditions = append(updatedConditions, fmt.Sprintf("exhaustion:%d", newExhaustion))
			}
			newConds = updatedConditions

			// Update exhaustion level in database
			db.Exec("UPDATE characters SET exhaustion_level = $1 WHERE id = $2", newExhaustion, charID)

			result = fmt.Sprintf("Your rage ends. The frenzy takes its toll — you gain 1 level of exhaustion (now at level %d).", newExhaustion)
			if newExhaustion >= 6 {
				result += " ☠️ EXHAUSTION LEVEL 6: You have died from exhaustion!"
			}
		}

		updatedConds, _ := json.Marshal(newConds)
		db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updatedConds, charID)
		return result

	case "wild_shape":
		// Druid Wild Shape - transform into a beast
		// Use action to assume beast form, can do so twice per short/long rest
		// CR limits: 1/4 at level 2, 1/2 at level 4, 1 at level 8
		// Movement restrictions: no flying until level 8, no swimming until level 4
		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "druid" {
			return "Cannot use Wild Shape: only Druids can."
		}
		if level < 2 {
			return "Cannot use Wild Shape: it needs Druid level 2."
		}

		// Check if already in Wild Shape
		var currentForm sql.NullString
		db.QueryRow("SELECT wild_shape_form FROM characters WHERE id = $1", charID).Scan(&currentForm)
		previousForm := ""
		if currentForm.Valid && currentForm.String != "" {
			previousForm = currentForm.String // "until you use Wild Shape again": the new use ends this form (after the checks below)
		}

		// Check Wild Shape uses
		var resourcesJSON []byte
		db.QueryRow("SELECT COALESCE(class_resources_used, '{}') FROM characters WHERE id = $1", charID).Scan(&resourcesJSON)
		var resourcesUsed map[string]int
		json.Unmarshal(resourcesJSON, &resourcesUsed)

		currentUsed := resourcesUsed["wild_shape"]
		maxUses := game.ClassTableInt(class, level, "Wild Shape") // 2024: the Features table (no unlimited uses)

		if currentUsed >= maxUses {
			return fmt.Sprintf("Cannot use Wild Shape: no uses left (%d/%d used). You regain one on a Short Rest and all on a Long Rest.", currentUsed, maxUses)
		}

		// Parse beast from description
		descLower := strings.ToLower(description)
		beastSlug := ""

		// Common beast mappings
		beastMappings := map[string]string{
			"wolf": "wolf", "dire wolf": "dire-wolf", "brown bear": "brown-bear", "black bear": "black-bear",
			"giant spider": "giant-spider", "giant wolf spider": "giant-wolf-spider", "giant rat": "giant-rat",
			"cat": "cat", "hawk": "hawk", "owl": "owl", "raven": "raven", "bat": "bat",
			"boar": "boar", "giant boar": "giant-boar", "elk": "elk", "giant elk": "giant-elk",
			"panther": "panther", "lion": "lion", "tiger": "tiger", "giant eagle": "giant-eagle",
			"crocodile": "crocodile", "giant crocodile": "giant-crocodile", "giant toad": "giant-toad",
			"constrictor snake": "constrictor-snake", "giant constrictor snake": "giant-constrictor-snake",
			"mastiff": "mastiff", "warhorse": "warhorse", "pony": "pony", "mule": "mule",
			"ape": "ape", "giant ape": "giant-ape", "baboon": "baboon",
		}

		// the druid's known forms first, then the common names; the longest name wins ("dire wolf" is not "wolf")
		names := map[string]string{}
		for name, slug := range beastMappings {
			names[name] = slug
		}
		for _, k := range knownWildShapes(charID) {
			names[strings.ReplaceAll(k, "-", " ")] = k
		}
		best := ""
		for name, slug := range names {
			if strings.Contains(descLower, name) && len(name) > len(best) {
				best, beastSlug = name, slug
			}
		}

		// If not found in mappings, try using description directly as slug
		if beastSlug == "" {
			beastSlug = strings.ReplaceAll(strings.TrimSpace(description), " ", "-")
			beastSlug = strings.ToLower(beastSlug)
		}

		// A form must be one of the druid's known forms (wild_shape_2024.go)
		known := knownWildShapes(charID)
		if len(known) == 0 {
			kn, _, _ := wildShapeLimits(level)
			return fmt.Sprintf("Cannot use Wild Shape: choose your known forms first (%d Beasts at your level): POST /api/characters/%d/class-options {\"wild_shape_forms\": [\"wolf\", ...]}.", kn, charID)
		}
		isKnown := false
		for _, k := range known {
			if k == beastSlug {
				isKnown = true
			}
		}
		if !isKnown {
			return fmt.Sprintf("Cannot use Wild Shape: %s isn't one of your known forms (%s). After a Long Rest you can replace one.", beastSlug, strings.Join(known, ", "))
		}

		// Look up beast in monsters table
		var beastName, beastSize, beastType, beastCR string
		var beastAC, beastHP, beastSpeed, beastStr, beastDex, beastCon int
		var actionsJSON []byte
		err := db.QueryRow(`
			SELECT name, size, type, ac, hp, speed, str, dex, con, cr, COALESCE(actions, '[]')
			FROM monsters WHERE slug = $1
		`, beastSlug).Scan(&beastName, &beastSize, &beastType, &beastAC, &beastHP, &beastSpeed, &beastStr, &beastDex, &beastCon, &beastCR, &actionsJSON)

		if err != nil {
			return fmt.Sprintf("Cannot use Wild Shape: no Beast named %q (your known forms: %s).", beastSlug, strings.Join(knownWildShapes(charID), ", "))
		}

		// Verify it's a beast
		if strings.ToLower(beastType) != "beast" {
			return fmt.Sprintf("Cannot use Wild Shape: %s is a %s, not a Beast.", beastName, beastType)
		}

		// Parse CR and check limits
		crFloat := 0.0
		if beastCR == "1/8" {
			crFloat = 0.125
		} else if beastCR == "1/4" {
			crFloat = 0.25
		} else if beastCR == "1/2" {
			crFloat = 0.5
		} else {
			crFloat, _ = strconv.ParseFloat(beastCR, 64)
		}

		maxCR := 0.25 // Level 2-3: CR 1/4
		if level >= 8 {
			maxCR = 1.0 // Level 8+: CR 1
		} else if level >= 4 {
			maxCR = 0.5 // Level 4-7: CR 1/2
		}

		if crFloat > maxCR {
			return fmt.Sprintf("Cannot use Wild Shape: %s (CR %s) is above your maximum CR of %.2g at Druid level %d.", beastName, beastCR, maxCR, level)
		}

		// 2024 Beast Shapes: a form with a Fly Speed needs Druid level 8
		var speedText string
		db.QueryRow(`SELECT COALESCE(speed_text,'') FROM monsters WHERE slug = $1`, beastSlug).Scan(&speedText)
		if level < 8 && strings.Contains(strings.ToLower(speedText), "fly") {
			return fmt.Sprintf("Cannot use Wild Shape: %s has a Fly Speed; such forms are allowed from Druid level 8.", beastName)
		}

		if previousForm != "" {
			leaveWildShape(charID, "")
		}
		// Expend Wild Shape use
		resourcesUsed["wild_shape"] = currentUsed + 1
		updatedResources, _ := json.Marshal(resourcesUsed)
		db.Exec("UPDATE characters SET class_resources_used = $1 WHERE id = $2", updatedResources, charID)

		// Set Wild Shape form
		db.Exec(`
			UPDATE characters 
			SET wild_shape_form = $1,
			    temp_hp = GREATEST(COALESCE(temp_hp, 0), $2)
			WHERE id = $3
		`, beastSlug, level, charID) // 2024: Temporary Hit Points equal to your Druid level; you keep your own Hit Points
		equipNote := enterWildShapeStats(charID, beastSlug, description) // the beast's Strength, Dexterity, Constitution and AC

		// Parse actions for display
		var actions []map[string]interface{}
		json.Unmarshal(actionsJSON, &actions)
		actionNames := []string{}
		for _, a := range actions {
			if name, ok := a["name"].(string); ok {
				actionNames = append(actionNames, name)
			}
		}
		actionsStr := "None"
		if len(actionNames) > 0 {
			actionsStr = strings.Join(actionNames, ", ")
		}

		usesRemaining := maxUses - currentUsed - 1
		usesInfo := fmt.Sprintf("%d/%d uses remaining; you stay in the form for %d hours", usesRemaining, maxUses, level/2)

		return fmt.Sprintf("🐺 WILD SHAPE! You transform into a %s!\n"+
			"Beast Stats: AC %d, Speed %dft. You keep your own Hit Points and gain %d Temporary Hit Points.\n"+
			"Physical: STR %d (%+d), DEX %d (%+d), CON %d (%+d)\n"+
			"Actions: %s\n"+
			"Wild Shape: %s\n"+
			"Note: You keep your INT, WIS, CHA, proficiencies, and class features. Attacks use your form's attacks. You can't cast spells (except with Beast Spells at level 18). Damage takes your Temporary Hit Points first, then your own Hit Points; the form ends if you have the Incapacitated condition (0 Hit Points makes you Unconscious) or die. Wild Shape is a Bonus Action; leave the form early with revert_wild_shape (also a Bonus Action). %s",
			beastName, beastAC, beastSpeed, level,
			beastStr, game.Modifier(beastStr), beastDex, game.Modifier(beastDex), beastCon, game.Modifier(beastCon),
			actionsStr, usesInfo, equipNote)

	case "revert_wild_shape":
		// Revert from Wild Shape to normal form
		// Can be done as bonus action
		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "druid" {
			return "Cannot revert: only Druids use Wild Shape."
		}

		var currentForm sql.NullString
		db.QueryRow("SELECT wild_shape_form FROM characters WHERE id = $1", charID).Scan(&currentForm)

		if !currentForm.Valid || currentForm.String == "" {
			return "Cannot revert: you aren't in Wild Shape."
		}

		// Get beast name for message
		var beastName string
		db.QueryRow("SELECT name FROM monsters WHERE slug = $1", currentForm.String).Scan(&beastName)
		if beastName == "" {
			beastName = currentForm.String
		}

		// Clear Wild Shape form: the druid's own Strength, Dexterity, Constitution and AC come back (wild_shape_2024.go)
		leaveWildShape(charID, "")

		return fmt.Sprintf("🧑 You revert from %s form back to your normal shape; your own Strength, Dexterity, Constitution and AC return (your Hit Points stay as they are). You can use Wild Shape again if you have uses remaining.", beastName)

	case "use_item":
		// Potions (a Bonus Action), Spell Scrolls (read with the cast action) and charged items (magic_item_use_2024.go)
		return useItemAction(charID, description)

	case "offhand_attack":
		// Two-Weapon Fighting: when you take the Attack action with a light melee weapon,
		// you can use a bonus action to attack with a different light melee weapon.
		// You don't add your ability modifier to the damage (unless negative).

		// Check that the player already used their action this turn (must have attacked first)
		var actionUsed bool
		var attacksLeft sql.NullInt64
		db.QueryRow("SELECT COALESCE(action_used, false), attacks_remaining FROM characters WHERE id = $1", charID).Scan(&actionUsed, &attacksLeft)
		if !actionUsed && !(attacksLeft.Valid && attacksLeft.Int64 >= 0) { // F197: once the Attack action has begun (a Fighter needn't make all four attacks first)
			return "The Light property's extra attack comes after you take the Attack action with a Light weapon: use 'attack' with a Light weapon, then 'offhand_attack' as a Bonus Action."
		}

		// Parse weapon from description
		weaponKey := parseWeaponFromDescription(description)
		if weaponKey == "" {
			weaponKey = namedWeaponInDescription(charID, description)
		}
		if weaponKey == "" {
			if _, off := equippedWeapons(charID); off != "" {
				weaponKey = heldWeaponKey(charID, off) // the weapon in the off hand
			}
		}
		if weaponKey != "" && !weaponEquippedInOffHand(charID, weaponKey) {
			return notEquippedMessage(weaponKey) + " An offhand attack uses the weapon equipped in your off hand."
		}
		weapon, hasWeapon := srdWeapons[weaponKey]

		if !hasWeapon {
			return "Offhand attack requires specifying a weapon (e.g., 'offhand_attack with dagger'). Light weapons: dagger, handaxe, shortsword, scimitar, sickle, light hammer."
		}

		// Validate weapon is light
		isLight := containsProperty(weapon.Properties, "light")
		if !isLight {
			return fmt.Sprintf("Two-Weapon Fighting requires a light weapon! %s is not light. Light weapons: dagger, handaxe, shortsword, scimitar, sickle, light hammer.", weapon.Name)
		}

		// Validate weapon is melee (not ranged)
		if weapon.Type != "melee" {
			return fmt.Sprintf("Two-Weapon Fighting requires melee weapons! %s is a ranged weapon.", weapon.Name)
		}

		// Determine attack modifier (DEX for finesse, STR otherwise)
		attackMod := game.Modifier(str)
		if containsProperty(weapon.Properties, "finesse") && game.Modifier(dex) > game.Modifier(str) {
			attackMod = game.Modifier(dex) // finesse: the better of Strength and Dexterity
		}
		masteryUses.Delete(charID)
		noteMasteryUse(charID, weaponKey, weapon, attackMod, level)

		// Add proficiency bonus only if proficient
		isProficient := isWeaponProficient(weaponProfsStr, weaponKey)
		if isProficient {
			attackMod += game.ProficiencyBonus(level)
		}

		// Get condition-based advantage/disadvantage (offhand is always melee, so not ranged)
		hasAdvantage, hasDisadvantage := getAttackModifiers(charID, attackTargetConditions(charID), false)

		// Check for explicit advantage/disadvantage in description
		if gmAdvantage() {
			hasAdvantage = true
		}
		if gmDisadvantage() {
			hasDisadvantage = true
		}

		// Roll attack
		var attackRoll, roll1, roll2 int
		rollType := "normal"
		if hasAdvantage && !hasDisadvantage {
			attackRoll, roll1, roll2 = game.RollWithAdvantage()
			rollType = "advantage"
		} else if hasDisadvantage && !hasAdvantage {
			attackRoll, roll1, roll2 = game.RollWithDisadvantage()
			rollType = "disadvantage"
		} else {
			attackRoll = game.RollDie(20)
			roll1, roll2 = attackRoll, 0
		}

		totalAttack := attackRoll + attackMod - game.ExhaustionD20Penalty(exhaustionLevelOf(charID))

		rollInfo := ""
		if rollType != "normal" {
			rollInfo = fmt.Sprintf(" [%s: %d, %d → %d]", rollType, roll1, roll2, attackRoll)
		}

		profInfo := ""
		if !isProficient {
			profInfo = " (not proficient)"
		}

		// Two-Weapon Fighting style allows adding ability modifier to off-hand damage
		hasTWFStyle := hasFightingStyle(charID, "two_weapon_fighting")
		damageMod := 0
		twfNote := " (TWF - no ability modifier)"
		if hasTWFStyle {
			// Get the ability modifier used for the attack
			if containsProperty(weapon.Properties, "finesse") {
				damageMod = game.Modifier(dex)
			} else {
				damageMod = game.Modifier(str)
			}
			twfNote = fmt.Sprintf(" (TWF Style +%d)", damageMod)
		}
		attackHeldItem.Store(charID, heldItemFor(charID, weaponKey, true))
		if offPlus := weaponPlusFor(charID, "", weaponKey, true); offPlus > 0 { // a magic weapon in the off hand: +N to attack and damage
			totalAttack += offPlus
			damageMod += offPlus
			twfNote += fmt.Sprintf(" (+%d magic weapon)", offPlus)
		}

		// Critical hit
		if attackRoll == 20 {
			// Double damage dice
			dmg := game.RollDamage(weapon.Damage, true) // crit = double dice
			dmg += damageMod                            // Add ability mod if have TWF style
			recordAttackOutcome(charID, attackOutcome{Weapon: weapon.Name, Total: totalAttack, Natural: attackRoll, Crit: true, Damage: dmg, DamageType: attackDamageType(true, weapon), RollInfo: rollInfo + twfNote})
			return fmt.Sprintf("Offhand attack with %s%s: %d (nat 20 CRITICAL!)%s Damage: %d%s",
				weapon.Name, profInfo, totalAttack, rollInfo, dmg, twfNote)
		}

		// Critical miss
		if attackRoll == 1 {
			recordAttackOutcome(charID, attackOutcome{Weapon: weapon.Name, Total: totalAttack, Natural: 1, Fumble: true, DamageType: attackDamageType(true, weapon), RollInfo: rollInfo})
			return fmt.Sprintf("Offhand attack with %s%s: %d (natural 1: miss)%s",
				weapon.Name, profInfo, totalAttack, rollInfo)
		}

		// Normal hit
		dmg := game.RollDamage(weapon.Damage, false)
		dmg += damageMod // Add ability mod if have TWF style
		recordAttackOutcome(charID, attackOutcome{Weapon: weapon.Name, Total: totalAttack, Natural: attackRoll, Damage: dmg, DamageType: attackDamageType(true, weapon), RollInfo: rollInfo + twfNote})
		return fmt.Sprintf("Offhand attack with %s%s: %d to hit%s. Damage: %d%s",
			weapon.Name, profInfo, totalAttack, rollInfo, dmg, twfNote)

	case "horde_breaker":
		// Hunter Ranger's Horde Breaker
		// Once per turn, when you make a weapon attack, you can make another attack
		// with the same weapon against a different creature within 5ft of the original target.

		// Verify is hunter ranger with horde_breaker choice
		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "ranger" {
			return "Only Rangers can use Horde Breaker!"
		}
		if !subclass.Valid || subclass.String != "hunter" {
			return "Only Hunter Rangers can use Horde Breaker!"
		}
		if level < 3 {
			return "Horde Breaker requires level 3+!"
		}

		// Check subclass choice
		var hbChoicesJSON []byte
		db.QueryRow("SELECT COALESCE(subclass_choices, '{}') FROM characters WHERE id = $1", charID).Scan(&hbChoicesJSON)
		var hbChoices map[string]string
		json.Unmarshal(hbChoicesJSON, &hbChoices)
		if hbChoices["hunters_prey"] != "horde_breaker" {
			return "You have not chosen Horde Breaker for your Hunter's Prey feature! Your choice: " + hbChoices["hunters_prey"]
		}

		// Check if already used this turn
		var hbUsed bool
		db.QueryRow("SELECT COALESCE(horde_breaker_used, false) FROM characters WHERE id = $1", charID).Scan(&hbUsed)
		if hbUsed {
			return "Horde Breaker already used this turn! It can only be used once per turn."
		}

		// Check that we've made an attack this turn (action_used should be true from Attack action)
		var hbActionUsed bool
		db.QueryRow("SELECT COALESCE(action_used, false) FROM characters WHERE id = $1", charID).Scan(&hbActionUsed)
		if !hbActionUsed {
			return "Horde Breaker requires making a weapon attack first! Use 'attack' action, then 'horde_breaker' against a different creature within 5ft of the original target."
		}

		// Parse weapon from description
		hbWeaponKey := parseWeaponFromDescription(description)
		hbWeapon, hbHasWeapon := srdWeapons[hbWeaponKey]

		if !hbHasWeapon {
			return "Horde Breaker requires specifying a weapon (e.g., 'horde_breaker with longbow against goblin B')."
		}

		// Mark horde_breaker as used
		db.Exec(`UPDATE characters SET horde_breaker_used = true WHERE id = $1`, charID)

		// Determine attack modifier
		hbAttackMod := game.Modifier(str)
		hbDamageMod := game.Modifier(str)
		if hbWeapon.Type == "ranged" || containsProperty(hbWeapon.Properties, "finesse") {
			hbAttackMod = game.Modifier(dex)
			hbDamageMod = game.Modifier(dex)
		}

		// Add proficiency bonus if proficient
		hbProficient := isWeaponProficient(weaponProfsStr, hbWeaponKey)
		if hbProficient {
			hbAttackMod += game.ProficiencyBonus(level)
		}

		// Get condition-based advantage/disadvantage
		var hbConditions []byte
		db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&hbConditions)
		var hbConds []string
		json.Unmarshal(hbConditions, &hbConds)
		isRangedAttack := hbWeapon.Type == "ranged"
		hbAdvantage, hbDisadvantage := getAttackModifiers(charID, hbConds, isRangedAttack)

		if gmAdvantage() {
			hbAdvantage = true
		}
		if gmDisadvantage() {
			hbDisadvantage = true
		}

		// Roll attack
		var hbRoll, hbRoll1, hbRoll2 int
		hbRollType := "normal"
		if hbAdvantage && !hbDisadvantage {
			hbRoll, hbRoll1, hbRoll2 = game.RollWithAdvantage()
			hbRollType = "advantage"
		} else if hbDisadvantage && !hbAdvantage {
			hbRoll, hbRoll1, hbRoll2 = game.RollWithDisadvantage()
			hbRollType = "disadvantage"
		} else {
			hbRoll = game.RollDie(20)
			hbRoll1, hbRoll2 = hbRoll, 0
		}

		hbTotalAttack := hbRoll + hbAttackMod

		hbRollInfo := ""
		if hbRollType != "normal" {
			hbRollInfo = fmt.Sprintf(" [%s: %d, %d → %d]", hbRollType, hbRoll1, hbRoll2, hbRoll)
		}

		hbProfInfo := ""
		if !hbProficient {
			hbProfInfo = " (not proficient)"
		}

		// Check for Hunter's Colossus Slayer (should not apply - they chose horde_breaker)
		// This is intentionally not included since they chose horde_breaker instead

		// Critical hit
		if hbRoll == 20 {
			hbDmg := game.RollDamage(hbWeapon.Damage, true) + hbDamageMod
			return fmt.Sprintf("🏹 Horde Breaker with %s%s: %d (nat 20 CRITICAL!)%s Damage: %d (attacking second target within 5ft of original)",
				hbWeapon.Name, hbProfInfo, hbTotalAttack, hbRollInfo, hbDmg)
		}

		// Critical miss
		if hbRoll == 1 {
			return fmt.Sprintf("🏹 Horde Breaker with %s%s: %d (natural 1: miss)%s",
				hbWeapon.Name, hbProfInfo, hbTotalAttack, hbRollInfo)
		}

		// Normal hit
		hbDmg := game.RollDamage(hbWeapon.Damage, false) + hbDamageMod
		return fmt.Sprintf("🏹 Horde Breaker with %s%s: %d to hit%s. Damage: %d (attacking second target within 5ft of original)",
			hbWeapon.Name, hbProfInfo, hbTotalAttack, hbRollInfo, hbDmg)

	case "martial_arts":
		// Martial Arts (Monk level 1): an Unarmed Strike as a Bonus Action, against the enemy picked with target_id
		if text, handled := martialArtsOnMonster(charID, description); handled {
			return text
		}
		return "Martial Arts: pick the enemy with target_id (see situation.enemies in GET /api/my-turn) and the server rolls the Unarmed Strike against it."

	case "flurry_of_blows":
		if flurryText, handled := flurryOnMonster(charID, description); handled {
			return flurryText
		}
		// Monk's Flurry of Blows
		// 2024 Monk's Focus: 1 Focus Point, a Bonus Action, two Unarmed Strikes (three from level 10, Heightened Focus)
		// For Warrior of the Open Hand (level 3+), each hit can impose one effect

		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "monk" {
			return "Only monks can use Flurry of Blows!"
		}

		if level < 2 {
			return "Flurry of Blows requires Monk's Focus (level 2+)!"
		}

		// Spend 1 Focus Point
		kiSuccess, kiErr, kiRemaining := useClassResource(charID, "focus", 1)
		if !kiSuccess {
			return fmt.Sprintf("Cannot use Flurry of Blows: %s", kiErr)
		}

		// Get monk die for damage
		monkDie := getMonkDie(level)

		// Monks can use DEX or STR for unarmed strikes (Martial Arts)
		flurryAttackMod := game.Modifier(dex)
		flurryDamageMod := game.Modifier(dex)
		if game.Modifier(str) > game.Modifier(dex) {
			flurryAttackMod = game.Modifier(str)
			flurryDamageMod = game.Modifier(str)
		}

		// Add proficiency bonus (monks are proficient with unarmed strikes)
		flurryAttackMod += game.ProficiencyBonus(level)

		// Check for conditions
		var flurryConds []byte
		db.QueryRow("SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1", charID).Scan(&flurryConds)
		var flurryConditions []string
		json.Unmarshal(flurryConds, &flurryConditions)

		flurryAdvantage, flurryDisadvantage := getAttackModifiers(charID, flurryConditions, false)
		if gmAdvantage() {
			flurryAdvantage = true
		}
		if gmDisadvantage() {
			flurryDisadvantage = true
		}

		// Check for Open Hand Technique
		isOpenHand := subclass.Valid && strings.ToLower(subclass.String) == "open-hand" && level >= 3

		// Open Hand Technique (2024): write "addle", "push" or "topple" in the description; every hit imposes it
		openHandEffect := ""
		if isOpenHand {
			descLower := strings.ToLower(description)
			for _, e := range []string{"addle", "push", "topple"} {
				if strings.Contains(descLower, e) {
					openHandEffect = e
					break
				}
			}
		}

		// Make two unarmed strikes
		var results []string
		totalDamage := 0

		flurryStrikes := 2
		if level >= 10 {
			flurryStrikes = 3 // Heightened Focus
		}
		for strike := 1; strike <= flurryStrikes; strike++ {
			// Roll attack
			var fRoll, fRoll1, fRoll2 int
			fRollType := "normal"
			if flurryAdvantage && !flurryDisadvantage {
				fRoll, fRoll1, fRoll2 = game.RollWithAdvantage()
				fRollType = "advantage"
			} else if flurryDisadvantage && !flurryAdvantage {
				fRoll, fRoll1, fRoll2 = game.RollWithDisadvantage()
				fRollType = "disadvantage"
			} else {
				fRoll = game.RollDie(20)
				fRoll1, fRoll2 = fRoll, 0
			}

			fTotalAttack := fRoll + flurryAttackMod

			fRollInfo := ""
			if fRollType != "normal" {
				fRollInfo = fmt.Sprintf(" [%s: %d, %d → %d]", fRollType, fRoll1, fRoll2, fRoll)
			}

			// Critical hit
			if fRoll == 20 {
				fDmg := game.RollDamage(monkDie, true) + flurryDamageMod
				totalDamage += fDmg
				strikeResult := fmt.Sprintf("Strike %d: %d (nat 20 CRITICAL!)%s - %d damage", strike, fTotalAttack, fRollInfo, fDmg)

				// Apply Open Hand effect on crit
				if isOpenHand && openHandEffect != "" {
					strikeResult += " " + applyOpenHandEffect(charID, openHandEffect, level)
				}
				results = append(results, strikeResult)
				continue
			}

			// Critical miss
			if fRoll == 1 {
				results = append(results, fmt.Sprintf("Strike %d: %d (natural 1: miss)%s", strike, fTotalAttack, fRollInfo))
				continue
			}

			// Normal hit (damage calculated, GM determines if it hits)
			fDmg := game.RollDamage(monkDie, false) + flurryDamageMod
			totalDamage += fDmg
			strikeResult := fmt.Sprintf("Strike %d: %d to hit%s - %d damage", strike, fTotalAttack, fRollInfo, fDmg)

			// Apply Open Hand effect on hit
			if isOpenHand && openHandEffect != "" {
				strikeResult += " " + applyOpenHandEffect(charID, openHandEffect, level)
			}
			results = append(results, strikeResult)
		}

		// Build response
		header := fmt.Sprintf("👊 Flurry of Blows! (1 Focus Point spent, %d remaining)", kiRemaining)
		if isOpenHand && openHandEffect == "" {
			header += "\n💫 Open Hand Technique: write addle, push or topple in the description to impose it on each hit."
		}

		return header + "\n" + strings.Join(results, "\n") + fmt.Sprintf("\nTotal damage: %d", totalDamage)

	case "patient_defense":
		return patientDefense(charID, description)

	case "gaze_of_two_minds":
		return gazeOfTwoMinds(charID, description)

	case "step_of_the_wind":
		// Monk's Step of the Wind
		// Spend 1 Focus Point to take Dash or Disengage as bonus action, and jump distance is doubled

		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "monk" {
			return "Only monks can use Step of the Wind!"
		}

		if level < 2 {
			return "Step of the Wind requires Monk's Focus (level 2+)!"
		}

		// Parse whether dash or disengage from description
		stepAction := "dash"
		descLower := strings.ToLower(description)
		if strings.Contains(descLower, "disengage") {
			stepAction = "disengage"
		}

		withFocus := strings.Contains(descLower, "focus") || strings.Contains(descLower, "disengage") || strings.Contains(descLower, "jump")
		if stepAction == "dash" && !withFocus && bonusDash != nil {
			speed, err := bonusDash(0) // 2024: the Dash is free
			if err != nil {
				return ""
			}
			return fmt.Sprintf("Step of the Wind: you take the Dash action as a Bonus Action and gain %d additional feet of movement. (Say \"focus\" to spend 1 Focus Point for Disengage and Dash and doubled jump distance.)", speed)
		}
		if stepAction == "dash" && withFocus && bonusDash != nil {
			limit := classResourceMax(charID, classKey, level, "focus", game.Modifier(cha))
			speed, err := bonusDash(limit)
			if err != nil {
				return ""
			}
			return fmt.Sprintf("Step of the Wind: 1 Focus Point spent; Disengage and Dash as a Bonus Action, %d additional feet of movement, and your jump distance is doubled this turn.", speed)
		}

		// Spend 1 Focus Point
		swSuccess, swErr, swRemaining := useClassResource(charID, "focus", 1)
		if !swSuccess {
			return fmt.Sprintf("Cannot use Step of the Wind: %s", swErr)
		}

		if stepAction == "disengage" {
			return fmt.Sprintf("💨 Step of the Wind (Disengage)! (1 Focus Point spent, %d remaining) You can move without provoking opportunity attacks, and your jump distance is doubled this turn.", swRemaining)
		}
		return fmt.Sprintf("💨 Step of the Wind (Dash)! (1 Focus Point spent, %d remaining) Your speed is doubled for this turn, and your jump distance is doubled.", swRemaining)

	case "stunning_strike":
		// Monk's Stunning Strike
		// When you hit with a melee weapon attack, spend 1 Focus Point to force CON save or stunned

		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "monk" {
			return "Only monks can use Stunning Strike!"
		}

		if level < 5 {
			return "Stunning Strike requires level 5+!"
		}

		// Spend 1 Focus Point
		ssSuccess, ssErr, ssRemaining := useClassResource(charID, "focus", 1)
		if !ssSuccess {
			return fmt.Sprintf("Cannot use Stunning Strike: %s", ssErr)
		}

		// Calculate Focus save DC: 8 + proficiency + WIS modifier
		var wis int
		db.QueryRow("SELECT wis FROM characters WHERE id = $1", charID).Scan(&wis)
		kiSaveDC := 8 + game.ProficiencyBonus(level) + game.Modifier(wis)

		return fmt.Sprintf("⚡ Stunning Strike! (1 Focus Point spent, %d remaining) Target must make CON save DC %d or be STUNNED until the end of your next turn.", ssRemaining, kiSaveDC)

	case "cunning_action":
		// Rogue's Cunning Action (level 2+)
		// Take Dash, Disengage, or Hide as a bonus action
		// Thief subclass (level 3+) via Fast Hands: also Sleight of Hand, Thieves' Tools, or Use Object

		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		isRogue := classKey == "rogue" && level >= 2

		if !isRogue {
			return "Cunning Action is a Rogue feature (level 2)."
		}

		// Parse which action from description
		descLower := strings.ToLower(description)

		// Check for Thief subclass Fast Hands options
		var subclass string
		db.QueryRow("SELECT COALESCE(subclass, '') FROM characters WHERE id = $1", charID).Scan(&subclass)
		isThief := strings.ToLower(subclass) == "thief" && level >= 3

		// Thief's Fast Hands (SRD 5.2.1): as a Bonus Action, a Dexterity (Sleight of Hand) check to pick a lock or disarm a trap with
		// Thieves' Tools or to pick a pocket; or Use an Object. With Thieves' Tools, proficiency with them adds the Proficiency Bonus,
		// and proficiency in both the skill and the tools gives Advantage (the tool rule).
		if isThief {
			withTools := strings.Contains(descLower, "thieves") || strings.Contains(descLower, "lockpick") || strings.Contains(descLower, "trap") || strings.Contains(descLower, "lock")
			pocket := strings.Contains(descLower, "sleight") || strings.Contains(descLower, "pickpocket") || strings.Contains(strings.ReplaceAll(descLower, "stealth", ""), "steal")
			if withTools || pocket {
				var dex int
				var tools string
				db.QueryRow("SELECT dex, COALESCE(tool_proficiencies, '') FROM characters WHERE id = $1", charID).Scan(&dex, &tools)
				mod, prof, profNote, reliable := checkBonus(charID, class, level, "sleight of hand", dex)
				advantage := false
				if withTools && game.HasToolProficiency(game.ParseProficiencyList(tools), "thieves' tools") {
					if strings.Contains(profNote, "proficient") || strings.Contains(profNote, "expertise") {
						advantage, profNote = true, profNote+" (and Thieves' Tools: Advantage)"
					} else {
						prof, profNote = game.ProficiencyBonus(level), " (Thieves' Tools)"
					}
				}
				d1 := game.RollDie(20)
				d, dice := d1, fmt.Sprintf("d20: %d", d1)
				if advantage {
					d2 := game.RollDie(20)
					if d2 > d {
						d = d2
					}
					dice = fmt.Sprintf("d20s: %d and %d, Advantage", d1, d2)
				}
				roll, rtNote := reliableRoll(d, reliable)
				what := "pick a pocket"
				switch {
				case withTools && strings.Contains(descLower, "trap"):
					what = "disarm a trap with Thieves' Tools"
				case withTools:
					what = "pick a lock with Thieves' Tools"
				}
				return fmt.Sprintf("🤏 Fast Hands (Sleight of Hand/DEX)%s to %s: %d (%s%s + %d DEX + %d prof). The GM compares it with the DC.", profNote, what, roll+mod+prof, dice, rtNote, mod, prof)
			}

			if strings.Contains(descLower, "use") && (strings.Contains(descLower, "object") || strings.Contains(descLower, "item") || strings.Contains(descLower, "potion")) {
				return "🎒 Fast Hands (Use an Object): you take the Utilize action, or the Magic action to use a magic item that needs it, as a Bonus Action. Say what you use."
			}
		}

		// Standard Cunning Action options (Rogues only for Dash/Disengage)
		if strings.Contains(descLower, "dash") {
			if bonusDash != nil {
				speed, err := bonusDash(0)
				if err != nil {
					return ""
				}
				return fmt.Sprintf("Cunning Action (Dash) grants %d additional feet of movement this turn.", speed)
			}
			return "💨 Cunning Action (Dash)! Your speed is doubled for this turn. You can move up to twice your normal speed."
		}

		if strings.Contains(descLower, "disengage") {
			return "🏃 Cunning Action (Disengage)! Your movement doesn't provoke opportunity attacks for the rest of this turn."
		}

		if strings.Contains(descLower, "hide") {
			// the Hide action as a Bonus Action (hide_2024.go): DC 15 Stealth, and the GM can overrule a success
			return attemptHide(charID, "Cunning Action (Hide)")
		}

		// No specific action given - provide options
		options := "Specify: dash, disengage, or hide"
		if isThief {
			options = "Specify: dash, disengage, hide, sleight of hand, thieves' tools, or use object"
		}
		return fmt.Sprintf("🗡️ Cunning Action! %s", options)

	case "steady_aim":
		return steadyAim(charID)

	case "tireless":
		return tireless(charID)

	case "innate_sorcery":
		return innateSorcery(charID)

	case "divine_sense":
		return divineSense(charID)

	case "wild_resurgence":
		return wildResurgence(charID, description)

	case "improve_fate":
		return improveFate(charID)

	case "countercharm":
		return countercharm(charID, description)

	case "self_restoration":
		return selfRestoration(charID, description)

	case "disciplined_survivor":
		return disciplinedSurvivor(charID, description)

	case "gift_of_the_protectors": // Eldritch Invocation: "add <name>" or "erase <name>"
		return giftOfTheProtectorsAction(charID, description)

	case "superior_defense":
		return superiorDefense(charID)

	case "quivering_palm":
		return quiveringPalm(charID, description)

	case "lands_aid":
		return landsAid(charID, description)

	case "natural_recovery":
		return naturalRecovery(charID, description)

	case "nature_magician":
		return natureMagician(charID, description)

	case "merge_with_shadows":
		return mergeWithShadows(charID)

	case "blink_steps":
		return blinkSteps(charID, description)

	case "bardic_inspiration":
		return giveBardicInspiration(charID, description)

	case "extend_rage":
		return extendRage(charID)

	case "use_bardic_inspiration":
		return useBardicInspiration(charID, description)

	case "dragon_wings":
		return dragonWings(charID)

	case "magical_cunning":
		return magicalCunning(charID)

	case "divine_spark":
		return divineSpark(charID, description)

	case "turn_undead", "preserve_life", "sacred_weapon": // Channel Divinity on the player's own turn (player_channel_2024.go, F241)
		return playerChannelDivinity(charID, action, description)

	case "natures_veil":
		return naturesVeil(charID)

	case "second_wind":
		// Fighter's Second Wind (level 1+)
		// Bonus action to regain 1d10 + fighter level HP. Once per short rest.

		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "fighter" {
			return "Only fighters can use Second Wind!"
		}

		// Check if resource is available
		var resourcesUsedJSON []byte
		db.QueryRow("SELECT COALESCE(class_resources_used, '{}') FROM characters WHERE id = $1", charID).Scan(&resourcesUsedJSON)
		var resourcesUsed map[string]int
		json.Unmarshal(resourcesUsedJSON, &resourcesUsed)

		maxUses := classResourceMax(charID, class, level, "second_wind", 0)
		currentUsed := resourcesUsed["second_wind"]

		if currentUsed >= maxUses {
			return "You've already used Second Wind! Take a short or long rest to regain this ability."
		}

		// Roll 1d10 + fighter level
		healRoll := game.RollDie(10)
		totalHeal := healRoll + level

		// Apply healing (up to max HP)
		var currentHP, maxHP int
		db.QueryRow("SELECT hp, max_hp FROM characters WHERE id = $1", charID).Scan(&currentHP, &maxHP)
		newHP := currentHP + totalHeal
		if newHP > maxHP {
			newHP = maxHP
		}
		actualHeal := newHP - currentHP

		// Update HP
		db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", newHP, charID)

		// Consume the resource
		resourcesUsed["second_wind"] = currentUsed + 1
		updatedResources, _ := json.Marshal(resourcesUsed)
		db.Exec("UPDATE characters SET class_resources_used = $1 WHERE id = $2", updatedResources, charID)

		if actualHeal == 0 {
			return fmt.Sprintf("💪 Second Wind! Rolled 1d10 (%d) + %d (level) = %d healing. You're already at full HP!", healRoll, level, totalHeal)
		}
		shift := ""
		if level >= 5 {
			shift = " Tactical Shift: you can move up to half your Speed without provoking Opportunity Attacks."
		}
		return fmt.Sprintf("💪 Second Wind! Rolled 1d10 (%d) + %d (level) = %d healing. HP: %d → %d (%d use(s) left).%s", healRoll, level, totalHeal, currentHP, newHP, maxUses-currentUsed-1, shift)

	case "action_surge":
		// Fighter's Action Surge (level 2+)
		// Bonus action to gain an additional action this turn. Once per short rest (twice at level 17+).

		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "fighter" {
			return "Only fighters can use Action Surge!"
		}

		if level < 2 {
			return "Action Surge requires Fighter level 2+!"
		}

		// Check if resource is available
		var resourcesUsedJSON []byte
		db.QueryRow("SELECT COALESCE(class_resources_used, '{}') FROM characters WHERE id = $1", charID).Scan(&resourcesUsedJSON)
		var resourcesUsed map[string]int
		json.Unmarshal(resourcesUsedJSON, &resourcesUsed)

		maxUses := classResourceMax(charID, class, level, "action_surge", 0)
		currentUsed := resourcesUsed["action_surge"]

		if currentUsed >= maxUses {
			usesWord := "use"
			if maxUses > 1 {
				usesWord = "uses"
			}
			return fmt.Sprintf("You've already used Action Surge %d time(s)! Take a short or long rest to regain this ability (%d %s per rest).", currentUsed, maxUses, usesWord)
		}

		// 2024: Action Surge can be used only once on a turn
		var surgedThisTurn bool
		db.QueryRow("SELECT COALESCE(action_surge_this_turn, false) FROM characters WHERE id = $1", charID).Scan(&surgedThisTurn)
		if surgedThisTurn {
			return "You can use Action Surge only once on a turn."
		}
		db.Exec("UPDATE characters SET action_surge_this_turn = true WHERE id = $1", charID)

		// Grant an extra action by resetting action_used
		db.Exec("UPDATE characters SET action_used = false WHERE id = $1", charID)

		// Consume the resource
		resourcesUsed["action_surge"] = currentUsed + 1
		updatedResources, _ := json.Marshal(resourcesUsed)
		db.Exec("UPDATE characters SET class_resources_used = $1 WHERE id = $2", updatedResources, charID)

		remainingUses := maxUses - currentUsed - 1
		usesNote := ""
		if remainingUses > 0 {
			usesNote = fmt.Sprintf(" (%d use(s) remaining until rest)", remainingUses)
		}

		return fmt.Sprintf("⚡ Action Surge! You push beyond your limits and gain one additional ACTION this turn (not the Magic action).%s", usesNote)

	case "lay_on_hands":
		// Paladin's Lay on Hands (level 1+)
		// Action to heal from your pool (paladin level × 5 HP total).
		// Can also cure disease or poison (costs 5 HP from pool per ailment).

		classKey := strings.ToLower(strings.ReplaceAll(class, " ", "_"))
		if classKey != "paladin" {
			return "Only paladins can use Lay on Hands!"
		}

		// Check remaining pool
		var resourcesUsedJSON []byte
		db.QueryRow("SELECT COALESCE(class_resources_used, '{}') FROM characters WHERE id = $1", charID).Scan(&resourcesUsedJSON)
		var resourcesUsed map[string]int
		json.Unmarshal(resourcesUsedJSON, &resourcesUsed)

		maxPool := classResourceMax(charID, class, level, "lay_on_hands", 0)
		currentUsed := resourcesUsed["lay_on_hands"]
		remainingPool := maxPool - currentUsed

		if remainingPool <= 0 {
			return "Your Lay on Hands pool is empty! Take a long rest to restore it."
		}

		// Parse amount and target from description
		// Format: "lay_on_hands 15" or "lay_on_hands 10 on Thorn" or "lay_on_hands cure disease on self"
		descLower := strings.ToLower(description)

		// Check for cure disease/poison
		if strings.Contains(descLower, "cure") {
			if remainingPool < 5 {
				return fmt.Sprintf("Curing disease or poison costs 5 HP from your pool. You only have %d HP remaining.", remainingPool)
			}

			// Determine target (self or another character)
			targetID := charID // default to self
			if !strings.Contains(descLower, "self") && !strings.Contains(descLower, "myself") {
				parsedTarget := parseTargetFromDescription(description, charID)
				if parsedTarget > 0 {
					targetID = parsedTarget
				}
			}

			// Look for disease or poison conditions to cure
			var targetConds []byte
			var targetName string
			db.QueryRow("SELECT COALESCE(conditions, '[]'), name FROM characters WHERE id = $1", targetID).Scan(&targetConds, &targetName)
			var conds []string
			json.Unmarshal(targetConds, &conds)

			curedAilment := ""
			newConds := []string{}
			for _, c := range conds {
				cLower := strings.ToLower(c)
				restoring := level >= 14 && (cLower == "blinded" || cLower == "charmed" || cLower == "deafened" || cLower == "frightened" || cLower == "paralyzed" || cLower == "stunned") && strings.Contains(descLower, cLower) // Restoring Touch
				if restoring || (strings.Contains(descLower, "poison") && (cLower == "poisoned" || strings.HasPrefix(cLower, "poison:"))) {
					curedAilment = c
					// Don't add this condition back (it's cured)
				} else {
					newConds = append(newConds, c)
				}
			}

			if curedAilment == "" {
				ailmentType := "disease or poison"
				if strings.Contains(descLower, "disease") {
					ailmentType = "disease"
				} else if strings.Contains(descLower, "poison") {
					ailmentType = "poison"
				}
				return fmt.Sprintf("%s has no %s to cure!", targetName, ailmentType)
			}

			// Update conditions and consume 5 HP from pool
			updatedConds, _ := json.Marshal(newConds)
			db.Exec("UPDATE characters SET conditions = $1 WHERE id = $2", updatedConds, targetID)
			syncRecordsFromList(targetID) // Lay On Hands cured a condition

			resourcesUsed["lay_on_hands"] = currentUsed + 5
			updatedResources, _ := json.Marshal(resourcesUsed)
			db.Exec("UPDATE characters SET class_resources_used = $1 WHERE id = $2", updatedResources, charID)

			targetNote := ""
			if targetID != charID {
				targetNote = fmt.Sprintf(" on %s", targetName)
			}
			return fmt.Sprintf("🙌 Lay on Hands! Cured %s%s. Pool: %d → %d HP remaining.", curedAilment, targetNote, remainingPool, remainingPool-5)
		}

		// Healing mode - parse amount
		healAmount := 0
		// Try to find a number in the description
		words := strings.Fields(description)
		for _, w := range words {
			if n, err := strconv.Atoi(w); err == nil && n > 0 {
				healAmount = n
				break
			}
		}

		if healAmount == 0 {
			return fmt.Sprintf("🙌 Lay on Hands pool: %d HP remaining. Specify amount to heal (e.g., 'lay_on_hands 10' or 'lay_on_hands 10 on Thorn'). To cure disease/poison: 'lay_on_hands cure disease on [target]'.", remainingPool)
		}

		if healAmount > remainingPool {
			healAmount = remainingPool
		}

		// Determine target
		targetID := charID
		if !strings.Contains(descLower, "self") && !strings.Contains(descLower, "myself") {
			parsedTarget := parseTargetFromDescription(description, charID)
			if parsedTarget > 0 {
				targetID = parsedTarget
			}
		}

		// Apply healing
		var targetHP, targetMaxHP int
		var targetName string
		db.QueryRow("SELECT hp, max_hp, name FROM characters WHERE id = $1", targetID).Scan(&targetHP, &targetMaxHP, &targetName)

		newHP := targetHP + healAmount
		if newHP > targetMaxHP {
			newHP = targetMaxHP
		}
		actualHeal := newHP - targetHP

		if actualHeal == 0 {
			if targetID == charID {
				return fmt.Sprintf("🙌 Lay on Hands! You're already at full HP. Pool: %d HP remaining.", remainingPool)
			}
			return fmt.Sprintf("🙌 Lay on Hands! %s is already at full HP. Pool: %d HP remaining.", targetName, remainingPool)
		}

		db.Exec("UPDATE characters SET hp = $1 WHERE id = $2", newHP, targetID)

		// Consume from pool
		resourcesUsed["lay_on_hands"] = currentUsed + actualHeal
		updatedResources, _ := json.Marshal(resourcesUsed)
		db.Exec("UPDATE characters SET class_resources_used = $1 WHERE id = $2", updatedResources, charID)

		targetNote := ""
		if targetID != charID {
			targetNote = fmt.Sprintf(" %s:", targetName)
		}
		return fmt.Sprintf("🙌 Lay on Hands!%s HP %d → %d (+%d). Pool: %d → %d HP remaining.", targetNote, targetHP, newHP, actualHeal, remainingPool, remainingPool-actualHeal)

	case "ready":
		// Ready action: hold your action for a trigger condition
		// Parse trigger from description (format: "trigger: X, action: Y" or just describe it)
		trigger := ""
		readyAction := ""
		readyDesc := description

		// Try to parse structured format: "trigger: when goblin attacks; action: attack with sword"
		descLower := strings.ToLower(description)
		if strings.Contains(descLower, "trigger:") {
			parts := strings.SplitN(description, ";", 2)
			if len(parts) >= 1 {
				triggerPart := strings.TrimPrefix(strings.TrimPrefix(parts[0], "trigger:"), "Trigger:")
				trigger = strings.TrimSpace(triggerPart)
			}
			if len(parts) >= 2 {
				actionPart := strings.TrimPrefix(strings.TrimPrefix(parts[1], "action:"), "Action:")
				actionPart = strings.TrimSpace(actionPart)
				// Parse action type from description
				for _, aType := range []string{"attack", "cast", "dash", "disengage", "help", "hide"} {
					if strings.Contains(strings.ToLower(actionPart), aType) {
						readyAction = aType
						readyDesc = actionPart
						break
					}
				}
			}
		}

		if trigger == "" {
			trigger = description
		}
		if readyAction == "" {
			// Default to "other" action type if not specified
			readyAction = "other"
		}

		// Store the readied action
		readiedData := map[string]string{
			"trigger":     trigger,
			"action":      readyAction,
			"description": readyDesc,
		}
		readiedJSON, _ := json.Marshal(readiedData)
		db.Exec("UPDATE characters SET readied_action = $1 WHERE id = $2", readiedJSON, charID)

		return fmt.Sprintf("Readied action: When '%s' → %s (%s). Use your REACTION to trigger when the condition occurs, or it will be lost at the start of your next turn.",
			trigger, readyAction, readyDesc)

	case "hide":
		return attemptHide(charID, "Hide") // hide_2024.go

	case "extinguish":
		return extinguishSelf(charID) // burning_2024.go

	case "spiritual_weapon", "flame_blade", "gaseous_form": // the spells' own actions (spells_more_2024.go)
		return moreSpellAction(charID, action, description, attackTargetOf(charID))

	case "jump":
		return jumpAction(charID, description) // toolbox_more_2024.go

	case "natures_sanctuary":
		return naturesSanctuary(charID, description, false) // natures_sanctuary_2024.go

	case "move_sanctuary":
		return naturesSanctuary(charID, description, true)

	case "search", "study", "influence":
		// 2024 Search (Wisdom), Study (Intelligence) and Influence (Charisma or Wisdom) checks: actions_checks_2024.go
		return resolveCheckAction(action, description, charID, class, level, intl, wis, cha)

	default:
		return fmt.Sprintf("Action: %s", description)
	}
}

// Helper to parse weapon name from action description
func parseWeaponFromDescription(desc string) string {
	desc = strings.ToLower(desc)
	// The longest matching name wins ("greatclub" over "club"), and ties break
	// alphabetically, so the result does not depend on map order.
	best := ""
	for key := range srdWeapons {
		weaponName := strings.ReplaceAll(key, "_", " ")
		if strings.Contains(desc, weaponName) || strings.Contains(desc, key) {
			if len(key) > len(best) || (len(key) == len(best) && key < best) {
				best = key
			}
		}
	}
	return best
}

// resolveCastSpellSlug selects the spell for a player-submitted cast action.
// A supplied slug is authoritative. Description matching can suggest candidates
// but deliberately never chooses a spell on the player's behalf.
// Callers must handle an error before committing an action or mutating state.
func resolveCastSpellSlug(requestedSlug, description string) (string, []string, string) {
	requestedSlug = normalizeSpellSlug(requestedSlug)
	if requestedSlug != "" {
		if _, ok := srdSpellsMemory[requestedSlug]; ok {
			return requestedSlug, nil, ""
		}
		return "", nil, "unknown_spell_slug"
	}

	candidates := spellCandidatesFromDescription(description)
	switch len(candidates) {
	case 0:
		return "", nil, "spell_not_identified"
	case 1:
		// Free prose can suggest a spelling, but never authorizes a turn. The
		// client must echo the structured slug so the mechanical choice is
		// explicit and inspectable.
		return "", candidates, "spell_slug_required"
	default:
		return "", candidates, "ambiguous_spell"
	}
}

// normalizeSpellSlug accepts the conventional dashed spelling and its common
// underscore/space variants, but only exact known slugs are ever accepted.
func normalizeSpellSlug(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	slug = strings.ReplaceAll(slug, "_", "-")
	return strings.Join(strings.Fields(slug), "-")
}

// spellCandidatesFromDescription returns every whole spell title mentioned in
// a description. It compares words rather than substrings, so "daylight" does
// not also select "light". Results are sorted to make API errors stable.
func spellCandidatesFromDescription(description string) []string {
	normalizedDescription := " " + normalizeSpellText(description) + " "
	seen := make(map[string]bool)
	candidates := make([]string, 0)

	for slug, spell := range srdSpellsMemory {
		names := []string{slug}
		if spell.Name != "" {
			names = append(names, spell.Name)
		}
		for _, name := range names {
			normalizedName := normalizeSpellText(name)
			if normalizedName == "" {
				continue
			}
			if strings.Contains(normalizedDescription, " "+normalizedName+" ") {
				if !seen[slug] {
					seen[slug] = true
					candidates = append(candidates, slug)
				}
				break
			}
		}
	}

	sort.Strings(candidates)
	return candidates
}

// normalizeSpellText makes spell titles and free-form prose comparable while
// retaining word boundaries. Apostrophes, hyphens, and underscores are spaces.
func normalizeSpellText(text string) string {
	var b strings.Builder
	lastWasSpace := true
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastWasSpace = false
		} else if !lastWasSpace {
			b.WriteByte(' ')
			lastWasSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Helper to parse spell name from an internal, already-committed action
// description. Only a single candidate is safe to resolve; ambiguity must
// never depend on Go map iteration order. Player HTTP casts use
// resolveCastSpellSlug and require spell_slug instead.
func parseSpellFromDescription(desc string) string {
	candidates := spellCandidatesFromDescription(desc)
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

// hasSpecificFeat checks if a character has a specific feat by querying the database
// Used for feat mechanic checks like Alert's hidden attacker immunity
func hasSpecificFeat(charID int, featSlug string) bool {
	var featsJSON []byte
	err := db.QueryRow("SELECT COALESCE(feats, '[]') FROM characters WHERE id = $1", charID).Scan(&featsJSON)
	if err != nil {
		return false
	}
	var feats []string
	json.Unmarshal(featsJSON, &feats)
	for _, f := range feats {
		if f == featSlug {
			return true
		}
	}
	return false
}

// checkSpellComponents validates if character can cast spell with V, S, M components
// Added somatic component enforcement with shield/War Caster check
// Parameters:
//   - components: spell components string (e.g., "V, S, M")
//   - conditions: character's current conditions
//   - inventory: character's inventory items
//   - equippedShield: whether character has a shield equipped
//   - feats: array of feat slugs the character has
//   - usedMetamagic: array of metamagic options being used (e.g., "subtle")
//   - classKey: character's class (e.g., "druid")
//   - level: character's level (for Archdruid check)
//
// Returns: error message if blocked, empty string if OK
func checkSpellComponents(components string, conditions []string, inventory []map[string]interface{}, equippedShield bool, feats []string, usedMetamagic []string, classKey string, level int) string {
	compLower := strings.ToLower(components)
	hasV := strings.Contains(compLower, "v")
	hasS := strings.Contains(compLower, "s")
	hasM := strings.Contains(compLower, "m")

	// Check for Subtle Spell metamagic - bypasses V and S components
	isSubtle := false
	for _, mm := range usedMetamagic {
		if mm == "subtle" {
			isSubtle = true
			break
		}
	}

	// Check for Archdruid feature (Druid level 20) - ignores V, S, and non-costly M
	// V (Verbal): Can't cast if silenced (unless Subtle Spell)
	if hasV && !isSubtle {
		for _, c := range conditions {
			if strings.ToLower(c) == "silenced" {
				return "Cannot cast - you are silenced and the spell requires verbal components (V)!"
			}
		}
	}

	// S (Somatic): Need a free hand (unless Subtle Spell)
	// If shield is equipped, both hands are occupied (weapon + shield)
	if hasS && !isSubtle {
		// equippedShield: no hand is free (the caller decides; the cast path uses castHandsProblem, hands_2024.go). A Holy Symbol on a
		// Shield stands in for Material components only: it makes no gestures (O-6).
		if equippedShield && !hasWarCaster(feats) {
			return "Cannot cast - the spell has a Somatic component (S) and you have no free hand (a Shield in one hand, a weapon in the other; a Spellcasting Focus held there does the gestures only for a spell with a Material component that has no cost). Unequip the shield (POST /api/characters/unequip-armor) or the weapon first."
		}
	}

	// M (Material): Need arcane focus, component pouch, or specific material
	if hasM {
		// Each class casts with its own kind of focus (see spell_focus.go); a Component Pouch works for all
		if !hasUsableFocus(classKey, inventory) {
			if classFocusKind(classKey) == "" {
				return "Cannot cast - spell requires material components (M) but you have no Component Pouch! Without a class Spellcasting Focus for this spell, a Component Pouch is needed in the inventory."
			}
			return "Cannot cast - spell requires material components (M) but you have no suitable spellcasting focus or component pouch! A " + titleCase(classKey) + " needs " + focusNeededName(classKey) + " (or a Component Pouch) in the inventory."
		}
	}

	return "" // OK to cast
}

// Check if weapon has a property
func containsProperty(props []string, prop string) bool {
	for _, p := range props {
		if strings.Contains(strings.ToLower(p), prop) {
			return true
		}
	}
	return false
}

// checkCostlyMaterial validates that a character has the required costly material in inventory
// Returns: error message if missing, empty string if OK
// Also returns the item name found (for consumption tracking)
func checkCostlyMaterial(inventory []map[string]interface{}, materialDesc string, materialCost int) (string, string) {
	if materialCost == 0 || materialDesc == "" {
		return "", "" // No costly material required
	}

	// Parse material name from description
	// Examples: "Diamonds worth 300gp, which the spell consumes."
	//           "A diamond worth at least 1,000 gp"
	//           "Ruby dust worth 50 gp"
	matLower := strings.ToLower(materialDesc)

	// Common material keywords to look for
	materialKeywords := []string{
		"holy symbol", // Bless: "a Holy Symbol worth 5+ GP"; every 2024 Holy Symbol (Amulet, Emblem, Reliquary) costs at least 5 GP
		"diamond", "ruby", "emerald", "sapphire", "pearl", "gem", "jewel",
		"gold", "silver", "platinum", "dust", "powder", "incense",
		"holy water", "oil", "herbs", "crystal", "stone", "bone",
	}

	// Find which material keyword is mentioned
	targetMaterial := ""
	for _, keyword := range materialKeywords {
		if strings.Contains(matLower, keyword) {
			targetMaterial = keyword
			break
		}
	}

	// Search inventory for matching item with sufficient value
	for _, item := range inventory {
		itemName := ""
		if name, ok := item["name"].(string); ok {
			itemName = strings.ToLower(name)
		} else if name, ok := item["item"].(string); ok {
			itemName = strings.ToLower(name)
		}

		// Check if item matches the material
		if targetMaterial == "holy symbol" {
			if strings.Contains(itemName, "holy symbol") || strings.Contains(itemName, "emblem") || strings.Contains(itemName, "reliquary") { // not "amulet" (F79)
				return "", itemName
			}
			continue
		}
		if targetMaterial != "" && !strings.Contains(itemName, targetMaterial) && !materialAlternative(matLower, itemName) {
			continue
		}

		// Check item value meets the cost requirement
		itemValue := 0
		if val, ok := item["value"].(float64); ok {
			itemValue = int(val)
		} else if val, ok := item["value"].(int); ok {
			itemValue = val
		} else if val, ok := item["cost"].(float64); ok {
			itemValue = int(val)
		} else if val, ok := item["cost"].(int); ok {
			itemValue = val
		}

		// If item has no value but name matches material, accept it
		// (GM can track value manually or it's assumed to be worth enough)
		if itemValue >= materialCost || (itemValue == 0 && targetMaterial != "" && strings.Contains(itemName, targetMaterial)) {
			return "", itemName // Found suitable material
		}
	}

	return fmt.Sprintf("Cannot cast - spell requires %s (worth %d gp). Add the material to your inventory or ask your GM to provide it.", materialDesc, materialCost), ""
}

// consumeSpellMaterial removes a consumed spell material from character inventory
// Only call this after the spell has been successfully cast
func consumeSpellMaterial(charID int, materialName string) {
	if materialName == "" {
		return
	}

	var inventoryJSON []byte
	db.QueryRow("SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1", charID).Scan(&inventoryJSON)
	var inventory []map[string]interface{}
	json.Unmarshal(inventoryJSON, &inventory)

	// Find and remove/decrement the material (not one in a lost bag)
	isLostItem := lostChecker(inventory)
	for i, item := range inventory {
		if isLostItem(item) {
			continue
		}
		itemName := ""
		if name, ok := item["name"].(string); ok {
			itemName = strings.ToLower(name)
		} else if name, ok := item["item"].(string); ok {
			itemName = strings.ToLower(name)
		}

		if strings.Contains(itemName, materialName) {
			// Check quantity
			qty := 1
			if q, ok := item["quantity"].(float64); ok {
				qty = int(q)
			} else if q, ok := item["qty"].(float64); ok {
				qty = int(q)
			} else if q, ok := item["quantity"].(int); ok {
				qty = q
			} else if q, ok := item["qty"].(int); ok {
				qty = q
			}

			if qty > 1 {
				// Decrement quantity
				if _, ok := item["quantity"]; ok {
					item["quantity"] = qty - 1
				} else if _, ok := item["qty"]; ok {
					item["qty"] = qty - 1
				}
			} else {
				// Remove item entirely
				inventory = append(inventory[:i], inventory[i+1:]...)
			}
			break
		}
	}

	updatedJSON, _ := json.Marshal(inventory)
	db.Exec("UPDATE characters SET inventory = $1 WHERE id = $2", updatedJSON, charID)
}

// checkAndUseAmmo checks if character has ammunition and decrements it
// Returns (success, message)
func checkAndUseAmmo(charID int, ammoType string) (bool, string) {
	if ammoType == "" {
		return true, "" // No ammo needed
	}
	if wantsMagicAmmo(charID) && useMagicAmmo(charID, ammoType) { // "+2 arrows": the magic ammunition (F150, items_c4_2024.go)
		return true, ""
	}
	if tableRule(lobbyOfChar(charID), "mundane_ammunition") == "unlimited" {
		return true, "" // table rule: ordinary ammunition isn't used up (magic ammunition is tracked separately)
	}

	// Get inventory
	var inventoryJSON []byte
	db.QueryRow("SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1", charID).Scan(&inventoryJSON)
	var inventory []map[string]interface{}
	json.Unmarshal(inventoryJSON, &inventory)

	// Find ammo in inventory
	ammoNames := map[string][]string{
		"arrows":  {"arrows", "arrow", "quiver of arrows"},
		"bolts":   {"bolts", "bolt", "crossbow bolts", "crossbow bolt"},
		"needles": {"needles", "needle", "blowgun needles", "blowgun needle"},
		"bullets": {"bullets", "bullet", "sling bullets", "sling bullet"},
	}

	validNames := ammoNames[ammoType]
	if validNames == nil {
		validNames = []string{ammoType}
	}

	// Find and decrement ammo (not ammo in a lost bag)
	found := false
	isLostItem := lostChecker(inventory)
	for i, item := range inventory {
		if isLostItem(item) {
			continue
		}
		if name, ok := item["name"].(string); ok {
			nameLower := strings.ToLower(name)
			for _, validName := range validNames {
				if nameLower == validName {
					// Check quantity
					qty := 1
					if q, ok := item["quantity"].(float64); ok {
						qty = int(q)
					}
					if qty <= 0 {
						continue // Empty, try next
					}

					// Decrement
					if qty == 1 {
						// Remove item entirely
						inventory = append(inventory[:i], inventory[i+1:]...)
					} else {
						inventory[i]["quantity"] = float64(qty - 1)
					}

					// Update inventory
					updatedInv, _ := json.Marshal(inventory)
					db.Exec("UPDATE characters SET inventory = $1, ammo_used_since_rest = ammo_used_since_rest + 1 WHERE id = $2", updatedInv, charID)

					found = true
					break
				}
			}
		}
		if found {
			break
		}
	}

	if !found && useMagicAmmo(charID, ammoType) { // only magic ammunition left: it is ammunition too (F150)
		return true, ""
	}
	if !found {
		return false, fmt.Sprintf("Out of %s! You need ammunition to attack with this weapon.", ammoType)
	}

	return true, ""
}

// recoverAmmo recovers half of ammunition used since last rest
// Returns number of ammo recovered
func recoverAmmo(charID int, ammoType string) (int, error) {
	// Get ammo used since rest
	var ammoUsed int
	err := db.QueryRow("SELECT COALESCE(ammo_used_since_rest, 0) FROM characters WHERE id = $1", charID).Scan(&ammoUsed)
	if err != nil {
		return 0, err
	}

	if ammoUsed == 0 {
		return 0, nil
	}

	// Recover half (round down)
	recovered := ammoUsed / 2
	if recovered == 0 && ammoUsed > 0 {
		recovered = 1 // Always recover at least 1 if any were used
	}

	// Get inventory and add recovered ammo
	var inventoryJSON []byte
	db.QueryRow("SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1", charID).Scan(&inventoryJSON)
	var inventory []map[string]interface{}
	json.Unmarshal(inventoryJSON, &inventory)

	// Find existing ammo stack or create new one
	ammoName := ammoType
	if ammoName == "" {
		ammoName = "arrows" // Default
	}

	found := false
	isLostItem := lostChecker(inventory)
	for i, item := range inventory {
		if isLostItem(item) {
			continue
		}
		if name, ok := item["name"].(string); ok {
			if strings.ToLower(name) == ammoName {
				qty := 0
				if q, ok := item["quantity"].(float64); ok {
					qty = int(q)
				}
				inventory[i]["quantity"] = float64(qty + recovered)
				found = true
				break
			}
		}
	}

	if !found {
		// Add new stack
		inventory = append(inventory, map[string]interface{}{
			"name":     ammoName,
			"quantity": float64(recovered),
		})
	}

	// Update inventory and reset counter
	updatedInv, _ := json.Marshal(inventory)
	db.Exec("UPDATE characters SET inventory = $1, ammo_used_since_rest = 0 WHERE id = $2", updatedInv, charID)

	return recovered, nil
}

// handleTriggerReadied godoc
// @Summary Trigger your readied action
// @Description When the trigger condition for your readied action occurs, use this endpoint to execute it. Costs your reaction.
// @Tags Actions
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Success 200 {object} map[string]interface{} "Readied action result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "No readied action or reaction already used"
// @Router /trigger-readied [post]
func handleTriggerReadied(w http.ResponseWriter, r *http.Request) {
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

	// Find the character's active game
	var charID, lobbyID int
	err = db.QueryRow(`
		SELECT c.id, c.lobby_id FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.agent_id = $1 AND l.status = 'active' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
		ORDER BY l.id DESC, c.id DESC
		LIMIT 1
	`, agentID, pickCharacterID(r, 0), pickCampaignID(r, 0)).Scan(&charID, &lobbyID)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "no_active_game"})
		return
	}

	// Check for readied action
	var readiedJSON []byte
	err = db.QueryRow("SELECT readied_action FROM characters WHERE id = $1", charID).Scan(&readiedJSON)
	if err != nil || readiedJSON == nil || string(readiedJSON) == "null" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "no_readied_action",
			"message": "You don't have a readied action. Use 'ready' action first with a trigger condition.",
		})
		return
	}

	var readied map[string]string
	json.Unmarshal(readiedJSON, &readied)

	// Check if reaction is available
	var reactionUsed bool
	db.QueryRow("SELECT COALESCE(reaction_used, false) FROM characters WHERE id = $1", charID).Scan(&reactionUsed)
	if reactionUsed {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        false,
			"error":          "reaction_used",
			"message":        "You have already used your reaction this round.",
			"readied_action": readied,
			"hint":           "Your readied action is still set, but you can't trigger it until your reaction refreshes.",
		})
		return
	}

	// Execute the readied action
	result := resolveAction(readied["action"], readied["description"], charID)

	// Consume reaction and clear readied action
	db.Exec("UPDATE characters SET reaction_used = true, readied_action = NULL WHERE id = $1", charID)

	// Log the action
	db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, $3, $4, $5)
	`, lobbyID, charID, "readied_"+readied["action"],
		fmt.Sprintf("Triggered: %s → %s", readied["trigger"], readied["description"]), result)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"trigger":           readied["trigger"],
		"action":            readied["action"],
		"result":            result,
		"reaction_consumed": true,
		"message":           fmt.Sprintf("Readied action triggered! Trigger: '%s' → %s", readied["trigger"], result),
	})
}

// getTargetCreatureType returns the creature type for a character/monster ID
// Used for Divine Smite bonus damage against undead/fiend
func getTargetCreatureType(targetID int) string {
	// First check if it's a combat participant with a monster key
	var monsterKey sql.NullString
	err := db.QueryRow(`
		SELECT cc.monster_key 
		FROM combat_combatants cc 
		WHERE cc.combatant_id = $1 AND cc.is_monster = true
	`, targetID).Scan(&monsterKey)

	if err == nil && monsterKey.Valid && monsterKey.String != "" {
		// Look up monster type from monsters table
		var monsterType string
		db.QueryRow("SELECT COALESCE(type, '') FROM monsters WHERE slug = $1", monsterKey.String).Scan(&monsterType)
		return strings.ToLower(monsterType)
	}

	// Check if there's a creature_type stored on the character (for custom NPCs)
	var creatureType string
	db.QueryRow("SELECT COALESCE(creature_type, '') FROM characters WHERE id = $1", targetID).Scan(&creatureType)
	return strings.ToLower(creatureType)
}

// isUndeadOrFiend checks if target is undead or fiend for Divine Smite bonus
func isUndeadOrFiend(targetID int) bool {
	if targetID <= 0 {
		return false
	}
	creatureType := getTargetCreatureType(targetID)
	return creatureType == "undead" || creatureType == "fiend"
}

// parseDivineSmiteSlot parses Divine Smite request from description
// Returns (wantsSmite bool, slotLevel int)
// Supports: "smite", "divine smite", "smite 2", "smite with 2nd level"
func parseDivineSmiteSlot(description string) (bool, int) {
	descLower := strings.ToLower(description)

	if !strings.Contains(descLower, "smite") {
		return false, 0
	}

	// Default to 1st level slot
	slotLevel := 1

	// Parse slot level from description
	patterns := []string{
		`smite (?:at |with )?(?:a )?(\d+)(?:st|nd|rd|th)?(?: level)?`,
		`smite (?:using )?(?:a )?level (\d+)`,
		`divine smite (\d+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(descLower); len(matches) > 1 {
			if lvl, err := strconv.Atoi(matches[1]); err == nil && lvl >= 1 && lvl <= 9 {
				slotLevel = lvl
			}
			break
		}
	}

	return true, slotLevel
}

// calculateDivineSmiteDamage calculates Divine Smite damage
// Base: 2d8, +1d8 per slot level above 1 (no maximum), +1d8 against a Fiend or an Undead
// isCrit doubles the dice
// Uses game.DivineSmiteDice for dice calculation
func calculateDivineSmiteDamage(slotLevel int, isUndeadOrFiend bool, isCrit bool) (int, string) {
	numDice := game.DivineSmiteDice(slotLevel, isUndeadOrFiend, isCrit)

	// Roll the damage
	total := 0
	for i := 0; i < numDice; i++ {
		total += game.RollDie(8)
	}

	diceStr := fmt.Sprintf("%dd8", numDice)
	return total, diceStr
}

// canUseDivineSmite checks if character can use Divine Smite
// Returns (canSmite bool, errorMessage string)
func canUseDivineSmite(charID int, slotLevel int, free ...bool) (bool, string) {
	if !(len(free) > 0 && free[0]) {
		if why := slotAlreadyUsedThisTurn(charID, "Divine Smite"); why != "" {
			return false, why // Divine Smite is a spell: one spell slot per turn
		}
	}
	if len(free) > 0 && free[0] { // Paladin's Smite: Divine Smite without a spell slot, once per Long Rest
		return freeSmiteAvailable(charID)
	}
	var class string
	var level int
	db.QueryRow("SELECT class, level FROM characters WHERE id = $1", charID).Scan(&class, &level)

	// Must be Paladin level 2+
	if strings.ToLower(class) != "paladin" {
		return false, "Only Paladins can use Divine Smite"
	}
	if level < 2 {
		return false, "Divine Smite requires Paladin level 2+"
	}

	// Check available spell slots
	slots := characterSpellSlots(charID, class, level)
	maxSlots := slots[slotLevel]
	if maxSlots == 0 {
		return false, fmt.Sprintf("No %s level spell slots available for Divine Smite", ordinal(slotLevel))
	}

	// Check used slots
	var usedJSON []byte
	db.QueryRow("SELECT COALESCE(spell_slots_used, '{}') FROM characters WHERE id = $1", charID).Scan(&usedJSON)
	var used map[string]int
	json.Unmarshal(usedJSON, &used)

	usedKey := strconv.Itoa(slotLevel)
	if used[usedKey] >= maxSlots {
		return false, fmt.Sprintf("No %s level spell slots remaining for Divine Smite", ordinal(slotLevel))
	}

	return true, ""
}

// consumeSpellSlotForSmite uses a spell slot for Divine Smite
func consumeSpellSlotForSmite(charID int, slotLevel int, free ...bool) {
	if len(free) > 0 && free[0] {
		useFreeSmite(charID)
		notePendingSpend(charID, func(p *pendingSpend) { p.FreeSmite = true }) // given back if the attack misses
		return
	}
	notePendingSpend(charID, func(p *pendingSpend) { p.SmiteSlot = slotLevel })
	markSlotUsedThisTurn(charID)
	var usedJSON []byte
	db.QueryRow("SELECT COALESCE(spell_slots_used, '{}') FROM characters WHERE id = $1", charID).Scan(&usedJSON)
	var used map[string]int
	if err := json.Unmarshal(usedJSON, &used); err != nil {
		used = make(map[string]int)
	}

	usedKey := strconv.Itoa(slotLevel)
	used[usedKey]++

	updatedJSON, _ := json.Marshal(used)
	db.Exec("UPDATE characters SET spell_slots_used = $1 WHERE id = $2", updatedJSON, charID)
}

// ordinal returns the ordinal suffix for a number (1st, 2nd, 3rd, etc.)
func ordinal(n int) string {
	switch n {
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	default:
		return fmt.Sprintf("%dth", n)
	}
}

// holySymbolIn: the inventory holds a Holy Symbol (an Amulet, Emblem or Reliquary).
func holySymbolIn(inventory []map[string]interface{}) bool {
	for _, it := range inventory {
		n := strings.ToLower(fmt.Sprint(it["name"]))
		if strings.Contains(n, "holy symbol") || strings.Contains(n, "emblem") || strings.Contains(n, "reliquary") { // not "amulet" (F79)
			return true
		}
	}
	return false
}
