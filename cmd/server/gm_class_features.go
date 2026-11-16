package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"strings"
)

// handleGMPreserveLife godoc
// @Summary Life Domain Channel Divinity: Preserve Life (mass healing)
// @Description Life Domain Cleric level 3, Preserve Life: as a Magic action, expend a use of Channel Divinity to restore Hit Points equal to five times the Cleric level, divided among Bloodied creatures within 30 feet (which can include the Cleric). A creature can't be restored to more than half its Hit Point maximum.
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param request body object{caster_id=integer,healing=[]object{target_id=integer,amount=integer}} true "Preserve Life request with healing distribution"
// @Success 200 {object} map[string]interface{} "Preserve Life results"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM or not Life Domain Cleric"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/preserve-life [post]
func handleGMPreserveLife(w http.ResponseWriter, r *http.Request) {
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
		CasterID int `json:"caster_id"` // Life Domain Cleric character ID
		Healing  []struct {
			TargetID int `json:"target_id"` // Character ID to heal
			Amount   int `json:"amount"`    // HP to restore to this target
		} `json:"healing"` // Distribution of healing
	}
	// the character's campaign, which must be one the GM runs (gm_campaign_2024.go)
	if _, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CasterID)); !ok {
		return
	}

	if req.CasterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "caster_id required (the Life Domain Cleric using Preserve Life)",
		})
		return
	}

	if len(req.Healing) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "healing array required with target_id and amount for each creature",
			"example": map[string]interface{}{
				"caster_id": 1,
				"healing": []map[string]interface{}{
					{"target_id": 2, "amount": 15},
					{"target_id": 3, "amount": 10},
				},
			},
		})
		return
	}

	// Verify caster is a Life Domain Cleric level 2+
	var casterName, className, subclass string
	var casterLevel int
	var lobbyID int
	err = db.QueryRow(`
		SELECT c.name, c.class, c.level, COALESCE(c.subclass, ''), c.lobby_id
		FROM characters c
		WHERE c.id = $1
	`, req.CasterID).Scan(&casterName, &className, &casterLevel, &subclass, &lobbyID)

	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": "Caster character not found",
		})
		return
	}

	if strings.ToLower(className) != "cleric" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_a_cleric",
			"message": fmt.Sprintf("%s is a %s, not a Cleric. Preserve Life requires Life Domain Cleric.", casterName, className),
		})
		return
	}

	if strings.ToLower(subclass) != "life" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "wrong_domain",
			"message": fmt.Sprintf("%s is a %s Domain Cleric. Preserve Life is only available to Life Domain Clerics.", casterName, subclass),
			"hint":    "Use Turn Undead (available to all Clerics) instead",
		})
		return
	}

	if casterLevel < 3 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "insufficient_level",
			"message": fmt.Sprintf("%s is level %d. Preserve Life is a Life Domain feature from Cleric level 3.", casterName, casterLevel),
		})
		return
	}

	// Check if Channel Divinity is available
	current := getCurrentClassResources(req.CasterID)
	if current["channel_divinity"] <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_channel_divinity",
			"message": fmt.Sprintf("%s has no Channel Divinity uses remaining. Recovers on short or long rest.", casterName),
		})
		return
	}

	// Calculate total healing pool: 5 × cleric level
	healingPool := 5 * casterLevel

	// Calculate total requested healing
	totalRequested := 0
	for _, h := range req.Healing {
		if h.Amount < 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_amount",
				"message": "Healing amounts must be positive",
			})
			return
		}
		totalRequested += h.Amount
	}

	if totalRequested > healingPool {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        "exceeds_pool",
			"message":      fmt.Sprintf("Total healing requested (%d) exceeds pool (%d HP = 5 × level %d)", totalRequested, healingPool, casterLevel),
			"healing_pool": healingPool,
			"requested":    totalRequested,
		})
		return
	}

	// Process each healing target
	results := []map[string]interface{}{}
	totalHealed := 0

	for _, h := range req.Healing {
		if h.Amount == 0 {
			continue
		}

		result := map[string]interface{}{
			"target_id": h.TargetID,
			"requested": h.Amount,
		}

		// Get target info
		var targetName string
		var currentHP, maxHP int
		err = db.QueryRow(`
			SELECT c.name, c.hp, c.max_hp
			FROM characters c
			WHERE c.id = $1 AND c.lobby_id = $2
		`, h.TargetID, lobbyID).Scan(&targetName, &currentHP, &maxHP)

		if err != nil {
			result["error"] = "target_not_found"
			result["message"] = fmt.Sprintf("Character %d not found in this campaign", h.TargetID)
			results = append(results, result)
			continue
		}

		result["target_name"] = targetName
		result["hp_before"] = currentHP
		result["max_hp"] = maxHP

		// Calculate half HP maximum (the cap for Preserve Life)
		halfMax := maxHP / 2

		// Calculate how much we can actually heal
		// Cannot restore above half HP maximum
		actualHeal := h.Amount
		if currentHP >= halfMax {
			// Already at or above half HP — cannot heal with Preserve Life
			result["healed"] = 0
			result["hp_after"] = currentHP
			result["capped_at_half"] = true
			result["message"] = fmt.Sprintf("%s is already at %d HP (≥ half of %d max) — Preserve Life cannot heal above half HP", targetName, currentHP, maxHP)
			results = append(results, result)
			continue
		}

		// Apply healing (the Hit Point owner, hit_points_2024.go: not above half the maximum; from 0 the Death Saving Throws reset)
		healed, err := healHP(h.TargetID, actualHeal, healUpToHalf())
		if err != nil {
			result["error"] = notFoundOrDBError(err, "target_not_found")
			results = append(results, result)
			continue
		}
		currentHP, actualHeal = healed.Before.HP, healed.Healed
		newHP := healed.After.HP
		if actualHeal < h.Amount {
			result["capped_at_half"] = true
		}

		result["healed"] = actualHeal
		result["hp_after"] = newHP
		result["half_max"] = halfMax

		if actualHeal < h.Amount {
			result["message"] = fmt.Sprintf("✨ %s healed for %d HP (capped at half max %d): %d → %d HP", targetName, actualHeal, halfMax, currentHP, newHP)
		} else {
			result["message"] = fmt.Sprintf("✨ %s healed for %d HP: %d → %d HP", targetName, actualHeal, currentHP, newHP)
		}

		totalHealed += actualHeal
		results = append(results, result)
	}

	// Consume Channel Divinity
	useClassResource(req.CasterID, "channel_divinity", 1)
	remaining := getCurrentClassResources(req.CasterID)["channel_divinity"]

	// Log the action
	targetNames := []string{}
	for _, r := range results {
		if name, ok := r["target_name"].(string); ok {
			if healed, ok := r["healed"].(int); ok && healed > 0 {
				targetNames = append(targetNames, fmt.Sprintf("%s (+%d)", name, healed))
			}
		}
	}

	writeAction(db, lobbyID, req.CasterID, "preserve_life", fmt.Sprintf("%s uses Channel Divinity: Preserve Life!", casterName), fmt.Sprintf("Healed %d HP total: %s", totalHealed, strings.Join(targetNames, ", ")))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":                    true,
		"caster":                     casterName,
		"cleric_level":               casterLevel,
		"healing_pool":               healingPool,
		"healing_used":               totalHealed,
		"healing_remaining":          healingPool - totalRequested,
		"channel_divinity_remaining": remaining,
		"results":                    results,
		"rules_note":                 "Preserve Life cannot restore a creature to more than half its hit point maximum",
	})
}

// handleGMSacredWeapon godoc
// @Summary Oath of Devotion Sacred Weapon (Channel Divinity, level 3)
// @Description When the Paladin takes the Attack action, they can expend one use of Channel Divinity to imbue one Melee weapon they are holding with positive energy. For 10 minutes or until used again, they add their Charisma modifier to attack rolls with that weapon (minimum +1), and each hit deals its normal damage type or Radiant damage. The weapon emits Bright Light in a 20-foot radius and Dim Light 20 feet beyond.
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param request body object{paladin_id=integer} true "Sacred Weapon request"
// @Success 200 {object} map[string]interface{} "Sacred Weapon activated"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM or not Devotion Paladin"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/sacred-weapon [post]
func handleGMSacredWeapon(w http.ResponseWriter, r *http.Request) {
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
		PaladinID int `json:"paladin_id"` // Paladin character ID
	}
	// the character's campaign, which must be one the GM runs (gm_campaign_2024.go)
	if _, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.PaladinID)); !ok {
		return
	}

	if req.PaladinID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "paladin_id required (the Paladin using Sacred Weapon)",
		})
		return
	}

	// Verify character is a Devotion Paladin level 3+ in GM's campaign
	var paladinName, className, subclassSlug string
	var paladinLevel, chaScore int
	var lobbyID int
	var subclassRaw sql.NullString
	err = db.QueryRow(`
		SELECT c.name, c.class, c.level, c.cha, c.lobby_id, c.subclass
		FROM characters c
		WHERE c.id = $1
	`, req.PaladinID).Scan(&paladinName, &className, &paladinLevel, &chaScore, &lobbyID, &subclassRaw)

	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "character_not_found",
			"message": "Character not found",
		})
		return
	}

	if subclassRaw.Valid {
		subclassSlug = subclassRaw.String
	}

	if strings.ToLower(className) != "paladin" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_a_paladin",
			"message": fmt.Sprintf("%s is a %s, not a Paladin. Sacred Weapon is a Devotion Paladin feature.", paladinName, className),
		})
		return
	}

	if paladinLevel < 3 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "insufficient_level",
			"message": fmt.Sprintf("%s is level %d. Sacred Weapon requires Paladin level 3+ (when Oath is chosen).", paladinName, paladinLevel),
		})
		return
	}

	if subclassSlug != "devotion" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "wrong_subclass",
			"message": fmt.Sprintf("%s has the %s oath. Sacred Weapon requires Oath of Devotion.", paladinName, subclassSlug),
			"note":    "Devotion Paladins choose their Oath at level 3.",
		})
		return
	}

	// Check if Channel Divinity is available
	current := getCurrentClassResources(req.PaladinID)
	if current["channel_divinity"] <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_channel_divinity",
			"message": fmt.Sprintf("%s has no Channel Divinity uses remaining. Recovers on short or long rest.", paladinName),
		})
		return
	}

	// Calculate bonus: CHA modifier, minimum +1
	chaBonus := game.Modifier(chaScore)
	if chaBonus < 1 {
		chaBonus = 1
	}

	// Apply sacred_weapon condition with bonus and duration (10 rounds = 1 minute)
	// Format: "sacred_weapon:BONUS:ROUNDS_REMAINING"
	setMarker(req.PaladinID, "sacred_weapon", fmt.Sprintf("%d:100", chaBonus)) // 10 minutes; replaces an earlier one (the condition owner)

	// Consume Channel Divinity
	useClassResource(req.PaladinID, "channel_divinity", 1)
	remaining := getCurrentClassResources(req.PaladinID)["channel_divinity"]

	// Log the action
	writeAction(db, lobbyID, req.PaladinID, "sacred_weapon", fmt.Sprintf("%s uses Channel Divinity: Sacred Weapon!", paladinName), fmt.Sprintf("Weapon imbued with positive energy. +%d to attack rolls with it for 10 minutes; each hit deals its normal damage type or Radiant damage. Bright Light in a 20-foot radius and Dim Light 20 feet beyond.", chaBonus))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":                    true,
		"paladin":                    paladinName,
		"paladin_level":              paladinLevel,
		"charisma":                   chaScore,
		"attack_bonus":               chaBonus,
		"duration":                   "10 minutes (100 rounds), or until used again",
		"rounds_remaining":           100,
		"channel_divinity_remaining": remaining,
		"effects": []string{
			fmt.Sprintf("+%d to attack rolls with imbued weapon", chaBonus),
			"Weapon emits bright light in 20-foot radius",
			"Weapon emits dim light 20 feet beyond that",
		},
		"ends_early_if": []string{
			"You drop the weapon",
			"You fall unconscious",
			"You dismiss this effect (no action required)",
		},
		"message": fmt.Sprintf("%s imbues their weapon with positive energy: for 10 minutes, +%d to attack rolls with it, and each hit can deal Radiant damage. It sheds Bright Light in a 20-foot radius.", paladinName, chaBonus),
	})
}

// handleGMCuttingWords godoc
// @Summary College of Lore Cutting Words (level 3)
// @Description Cutting Words (College of Lore, level 3+): When a creature within 60 feet makes an attack roll, ability check, or damage roll, a Lore Bard can use their reaction and expend one Bardic Inspiration die to subtract the roll from the creature's result. The GM calls this after the enemy rolls but before the outcome is determined.
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param request body object{bard_id=integer,enemy_roll=integer,roll_type=string} true "Cutting Words: bard_id (character using the reaction), enemy_roll (the roll to reduce), roll_type (attack/ability/damage)"
// @Success 200 {object} map[string]interface{} "Cutting Words result with reduced roll"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM or not Lore Bard"
// @Failure 400 {object} map[string]interface{} "Invalid request or no Bardic Inspiration"
// @Router /gm/cutting-words [post]
func handleGMCuttingWords(w http.ResponseWriter, r *http.Request) {
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
		BardID    int    `json:"bard_id"`
		EnemyRoll int    `json:"enemy_roll"`
		RollType  string `json:"roll_type"` // "attack", "ability", or "damage"
	}
	// the character's campaign, which must be one the GM runs (gm_campaign_2024.go)
	lobbyID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.BardID))
	if !ok {
		return
	}

	// Validate inputs
	if req.BardID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "bard_id required",
		})
		return
	}

	validRollTypes := map[string]bool{"attack": true, "ability": true, "damage": true}
	if req.RollType == "" {
		req.RollType = "attack" // Default
	}
	if !validRollTypes[req.RollType] {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "roll_type must be 'attack', 'ability', or 'damage'",
		})
		return
	}

	// Get character info
	var charName, class string
	var level int
	var subclass sql.NullString
	err = db.QueryRow(`
		SELECT name, class, level, subclass FROM characters WHERE id = $1
	`, req.BardID).Scan(&charName, &class, &level, &subclass)
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is a Bard with Lore subclass
	if strings.ToLower(class) != "bard" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_a_bard",
			"message": fmt.Sprintf("%s is a %s, not a Bard. Only Bards can use Cutting Words.", charName, class),
		})
		return
	}

	if !subclass.Valid || strings.ToLower(subclass.String) != "lore" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_lore_bard",
			"message": fmt.Sprintf("%s is not a College of Lore bard. Cutting Words is a Lore-specific feature.", charName),
		})
		return
	}

	// Check that character has the cutting_words feature (level 3+)
	if !hasSubclassFeature(subclass.String, level, "cutting_words") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "feature_not_available",
			"message": fmt.Sprintf("%s hasn't unlocked Cutting Words yet (requires College of Lore level 3+)", charName),
		})
		return
	}

	// Check for Bardic Inspiration uses
	success, errMsg, remaining := useClassResource(req.BardID, "bardic_inspiration", 1)
	if !success {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_bardic_inspiration",
			"message": errMsg,
		})
		return
	}

	// Roll the Bardic Inspiration die
	dieSize := getBardicInspirationDie(level)
	subtraction := game.RollDie(dieSize)

	// Calculate the reduced roll
	reducedRoll := req.EnemyRoll - subtraction
	if reducedRoll < 0 && req.RollType != "damage" {
		reducedRoll = 0 // Rolls can't go below 0, except damage can go negative (treated as 0 damage)
	}

	// Build descriptive message based on roll type
	var rollTypeDesc string
	switch req.RollType {
	case "attack":
		rollTypeDesc = "attack roll"
	case "ability":
		rollTypeDesc = "ability check"
	case "damage":
		rollTypeDesc = "damage roll"
	}

	response := map[string]interface{}{
		"success":                      true,
		"bard":                         charName,
		"bard_id":                      req.BardID,
		"feature":                      "Cutting Words",
		"roll_type":                    req.RollType,
		"original_roll":                req.EnemyRoll,
		"inspiration_die":              fmt.Sprintf("d%d", dieSize),
		"subtraction":                  subtraction,
		"reduced_roll":                 reducedRoll,
		"bardic_inspiration_remaining": remaining,
		"message": fmt.Sprintf("🎭 %s uses Cutting Words! Rolling d%d... %d! The enemy's %s is reduced from %d to %d. (%d Bardic Inspiration remaining)",
			charName, dieSize, subtraction, rollTypeDesc, req.EnemyRoll, reducedRoll, remaining),
	}

	// Log the action
	actionDesc := fmt.Sprintf("%s uses Cutting Words (reaction) to reduce enemy %s", charName, rollTypeDesc)
	actionResult := fmt.Sprintf("d%d = %d subtracted. Roll reduced from %d to %d", dieSize, subtraction, req.EnemyRoll, reducedRoll)
	writeAction(db, lobbyID, req.BardID, "cutting_words", actionDesc, actionResult)

	json.NewEncoder(w).Encode(response)
}

// handleGMDarkOnesLuck godoc
// @Summary Fiend Patron Dark One's Own Luck (level 6)
// @Description When the Warlock makes an ability check or a saving throw, they can add 1d10 to the roll, after seeing the roll but before any of its effects occur. Uses: the Charisma modifier (minimum once), at most once per roll; all uses return on a Long Rest.
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param request body object{character_id=integer,original_roll=integer,roll_type=string} true "Dark One's Own Luck: character_id (Fiend Warlock using the feature), original_roll (the roll to boost), roll_type (ability/saving)"
// @Success 200 {object} map[string]interface{} "Dark One's Own Luck result with boosted roll"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM or not Fiend Warlock level 6+"
// @Failure 400 {object} map[string]interface{} "Invalid request or feature already used"
// @Router /gm/dark-ones-luck [post]
func handleGMDarkOnesLuck(w http.ResponseWriter, r *http.Request) {
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
		CharacterID  int    `json:"character_id"`
		OriginalRoll int    `json:"original_roll"`
		RollType     string `json:"roll_type"` // "ability" or "saving"
	}
	// the character's campaign, which must be one the GM runs (gm_campaign_2024.go)
	lobbyID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID))
	if !ok {
		return
	}

	// Validate inputs
	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_id required",
		})
		return
	}

	validRollTypes := map[string]bool{"ability": true, "saving": true}
	if req.RollType == "" {
		req.RollType = "ability" // Default
	}
	if !validRollTypes[req.RollType] {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "roll_type must be 'ability' or 'saving'",
		})
		return
	}

	// Get character info
	var charName, class string
	var level int
	var subclass sql.NullString
	var featureUsed bool
	var luckCha, luckCount int
	err = db.QueryRow(`
		SELECT name, class, level, subclass, COALESCE(dark_ones_luck_used, false), cha, COALESCE(dark_ones_luck_count, 0)
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &class, &level, &subclass, &featureUsed, &luckCha, &luckCount)
	luckUses := game.Modifier(luckCha) // 2024: a number of times equal to the Charisma modifier (minimum once) per Long Rest
	if luckUses < 1 {
		luckUses = 1
	}
	featureUsed = luckCount >= luckUses
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is a Warlock with Fiend patron
	if strings.ToLower(class) != "warlock" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_a_warlock",
			"message": fmt.Sprintf("%s is a %s, not a Warlock. Only Warlocks can use Dark One's Own Luck.", charName, class),
		})
		return
	}

	if !subclass.Valid || strings.ToLower(subclass.String) != "fiend" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_fiend_warlock",
			"message": fmt.Sprintf("%s is not a Fiend patron warlock. Dark One's Own Luck is a Fiend-specific feature.", charName),
		})
		return
	}

	// Check that character has the dark_ones_luck feature (level 6+)
	if level < 6 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "feature_not_available",
			"message": fmt.Sprintf("%s hasn't unlocked Dark One's Own Luck yet (requires Fiend Warlock level 6+, currently level %d)", charName, level),
		})
		return
	}

	// Check if feature has already been used
	if featureUsed {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "feature_already_used",
			"message": fmt.Sprintf("%s has used Dark One's Own Luck %d times (Charisma modifier, minimum once) since their last Long Rest", charName, luckUses),
			"note":    "All uses return on a Long Rest",
		})
		return
	}

	// Roll the d10 bonus
	bonus := game.RollDie(10)

	// Calculate the boosted roll
	boostedRoll := req.OriginalRoll + bonus

	// Mark the feature as used
	db.Exec("UPDATE characters SET dark_ones_luck_count = COALESCE(dark_ones_luck_count,0) + 1, dark_ones_luck_used = $2 WHERE id = $1", req.CharacterID, luckCount+1 >= luckUses)

	// Build descriptive message based on roll type
	var rollTypeDesc string
	switch req.RollType {
	case "ability":
		rollTypeDesc = "ability check"
	case "saving":
		rollTypeDesc = "saving throw"
	}

	response := map[string]interface{}{
		"success":       true,
		"character":     charName,
		"character_id":  req.CharacterID,
		"feature":       "Dark One's Own Luck",
		"roll_type":     req.RollType,
		"original_roll": req.OriginalRoll,
		"bonus_die":     "d10",
		"bonus":         bonus,
		"boosted_roll":  boostedRoll,
		"message": fmt.Sprintf("🔥 %s calls upon Dark One's Own Luck! Rolling d10... +%d! The %s is boosted from %d to %d.",
			charName, bonus, rollTypeDesc, req.OriginalRoll, boostedRoll),
		"uses_left": luckUses - luckCount - 1,
		"note":      "Uses return on a Long Rest",
	}

	// Log the action
	actionDesc := fmt.Sprintf("%s uses Dark One's Own Luck to boost %s", charName, rollTypeDesc)
	actionResult := fmt.Sprintf("d10 = +%d. Roll boosted from %d to %d", bonus, req.OriginalRoll, boostedRoll)
	writeAction(db, lobbyID, req.CharacterID, "dark_ones_luck", actionDesc, actionResult)

	json.NewEncoder(w).Encode(response)
}

// handleGMIndomitable godoc
// @Summary Fighter Indomitable (level 9)
// @Description Level 9+ Fighters can reroll a failed saving throw. They must use the new roll. Uses per long rest: 1 at level 9, 2 at level 13, 3 at level 17.
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param request body object{character_id=integer,ability=string,dc=integer} true "character_id (Fighter), ability (str/dex/con/int/wis/cha), dc (difficulty class)"
// @Success 200 {object} map[string]interface{} "Saving throw reroll result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM"
// @Failure 400 {object} map[string]interface{} "Invalid request or feature unavailable"
// @Router /gm/indomitable [post]
func handleGMIndomitable(w http.ResponseWriter, r *http.Request) {
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
		Ability     string `json:"ability"` // str, dex, con, int, wis, cha
		DC          int    `json:"dc"`
	}
	// the character's campaign, which must be one the GM runs (gm_campaign_2024.go)
	lobbyID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID))
	if !ok {
		return
	}

	// Validate inputs
	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_id required",
		})
		return
	}

	if req.Ability == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "ability required (str, dex, con, int, wis, cha)",
		})
		return
	}

	if req.DC == 0 {
		req.DC = 10 // Default DC
	}

	// Get character info
	var charName, class string
	var level, usedCount int
	var str, dex, con, intl, wis, cha int
	err = db.QueryRow(`
		SELECT name, class, level, COALESCE(indomitable_used, 0), str, dex, con, intl, wis, cha
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &class, &level, &usedCount, &str, &dex, &con, &intl, &wis, &cha)
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is a Fighter level 9+
	maxUses := getIndomitableMaxUses(class, level)
	if maxUses == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "feature_unavailable",
			"message": fmt.Sprintf("%s is a level %d %s. Indomitable requires Fighter level 9+.", charName, level, class),
		})
		return
	}

	// Check if uses remaining
	remaining := maxUses - usedCount
	if remaining <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":            "no_uses_remaining",
			"message":          fmt.Sprintf("%s has used all Indomitable uses for this long rest (%d/%d)", charName, usedCount, maxUses),
			"indomitable_used": usedCount,
			"indomitable_max":  maxUses,
			"recovery":         "long rest",
		})
		return
	}

	abilityShort, okAbility := game.AbilityKey(req.Ability)
	if !okAbility {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid ability - use str, dex, con, int, wis, or cha"})
		return
	}
	abilityName := game.AbilityName(abilityShort)

	// Track usage: the guarded spend before the reroll (resources_2024.go)
	_, after, err := changeSpent(req.CharacterID, func(s *spent) error {
		if s.Indomitable >= maxUses {
			return &notEnough{"no_uses_remaining", 0}
		}
		s.Indomitable++
		return nil
	})
	if _, refused := refusedSpend(err); refused {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error":            "no_uses_remaining",
			"message":          fmt.Sprintf("%s has used all Indomitable uses for this long rest (%d/%d)", charName, maxUses, maxUses),
			"indomitable_used": maxUses,
			"indomitable_max":  maxUses,
			"recovery":         "long rest",
		})
		return
	}
	if answerDBFailure(w, err, "the character") {
		return
	}
	newUsedCount := after.Indomitable

	// the reroll is the same saving throw made again (save_2024.go), with a bonus equal to the Fighter level (2024 Indomitable);
	// the new roll stands
	fighterLevel := classLevelIn(req.CharacterID, class, level, "fighter")
	sv := (&saveCtx{lobby: lobbyID, saver: req.CharacterID, ability: abilityShort, dc: req.DC,
		adjust: []game.RollAdjust{{Amount: fighterLevel, Why: fmt.Sprintf("Indomitable +%d", fighterLevel)}}}).roll()
	_, proficient, _ := saveModifier(lobbyID, req.CharacterID, abilityShort)
	success, newTotal := sv.Success, sv.Total

	label := "Indomitable reroll: " + abilityName + " saving throw"
	if proficient {
		label += " (proficient)"
	}
	fullResult := sv.ForSaver(label)

	// Log the action
	actionDesc := fmt.Sprintf("%s uses Indomitable to reroll failed %s save", charName, abilityName)
	writeAction(db, lobbyID, req.CharacterID, "indomitable", actionDesc, fullResult)

	response := map[string]interface{}{
		"success":               success,
		"character":             charName,
		"ability":               abilityName,
		"proficient":            proficient,
		"roll":                  sv.Natural,
		"ability_mod":           game.Modifier(abilityScoreOf(req.CharacterID, abilityShort)),
		"total_mod":             sv.Save.Modifier(),
		"total":                 newTotal,
		"dc":                    req.DC,
		"outcome":               sv.Outcome(),
		"result":                fullResult,
		"advantage":             sv.Save.Advantage,
		"disadvantage":          sv.Save.Disadvantage,
		"indomitable_used":      newUsedCount,
		"indomitable_max":       maxUses,
		"indomitable_remaining": maxUses - newUsedCount,
		"feature_note":          "Indomitable (Fighter): You must use the new roll result.",
	}
	if len(sv.Changed) > 0 {
		response["changed"] = sv.Changed // Halfling Luck, the Luck Blade, Indomitable Might
	}

	json.NewEncoder(w).Encode(response)
}

// handleGMStrokeOfLuck godoc
// @Summary Rogue Stroke of Luck (level 20)
// @Description Level 20 Rogues can use this feature in two ways: 1) If an attack misses, turn the miss into a hit (mode="attack"). 2) If an ability check fails, treat the d20 roll as a 20 (mode="ability_check"). This feature can only be used once per short or long rest.
// @Tags GM Tools
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer <token>"
// @Param request body object{character_id=integer,mode=string} true "character_id (Rogue level 20), mode ('attack' to turn miss into hit, 'ability_check' to treat roll as 20)"
// @Success 200 {object} map[string]interface{} "Stroke of Luck applied"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not GM"
// @Failure 400 {object} map[string]interface{} "Invalid request or feature unavailable"
// @Router /gm/stroke-of-luck [post]
func handleGMStrokeOfLuck(w http.ResponseWriter, r *http.Request) {
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
		Mode        string `json:"mode"` // "attack" or "ability_check"
	}
	// the character's campaign, which must be one the GM runs (gm_campaign_2024.go)
	lobbyID, ok := gmReadAs(w, r, agentID, &req, campaignFromTarget(&req.CharacterID))
	if !ok {
		return
	}

	// Validate inputs
	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_id required",
		})
		return
	}

	if req.Mode == "" {
		req.Mode = "attack" // Default to attack miss conversion
	}

	modeLower := strings.ToLower(req.Mode)
	if modeLower == "saving_throw" || modeLower == "d20_test" {
		modeLower = "ability_check" // 2024: the failed D20 Test (a save too) becomes a 20
	}
	if modeLower != "attack" && modeLower != "ability_check" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "mode must be 'attack', 'ability_check' or 'saving_throw' (a failed D20 Test becomes a 20)",
		})
		return
	}

	// Get character info and check class/level
	var charName, class string
	var level int
	var strokeOfLuckUsed bool
	var classLevelsJSON []byte
	err = db.QueryRow(`
		SELECT name, class, level, COALESCE(stroke_of_luck_used, false), COALESCE(class_levels, '{}')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &class, &level, &strokeOfLuckUsed, &classLevelsJSON)
	if err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Check Rogue level (handle multiclass)
	rogueLevel := 0
	classLevels := make(map[string]int)
	json.Unmarshal(classLevelsJSON, &classLevels)

	if len(classLevels) > 1 {
		// Multiclass: check for Rogue levels
		for c, lvl := range classLevels {
			if strings.ToLower(c) == "rogue" {
				rogueLevel = lvl
				break
			}
		}
	} else if strings.ToLower(class) == "rogue" {
		rogueLevel = level
	}

	// Verify Rogue level 20
	if rogueLevel < 20 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "feature_unavailable",
			"message": fmt.Sprintf("%s is a Rogue level %d. Stroke of Luck requires Rogue level 20.", charName, rogueLevel),
		})
		return
	}

	// Check if already used
	if strokeOfLuckUsed {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":    "already_used",
			"message":  fmt.Sprintf("%s has already used Stroke of Luck since their last rest.", charName),
			"recovery": "short or long rest",
		})
		return
	}

	// Mark as used
	db.Exec("UPDATE characters SET stroke_of_luck_used = true WHERE id = $1", req.CharacterID)

	// Build result based on mode
	var actionDesc, fullResult, modeDesc string
	if modeLower == "attack" {
		modeDesc = "the d20 of the failed attack roll becomes a 20"
		actionDesc = fmt.Sprintf("%s uses Stroke of Luck to turn a missed attack into a hit!", charName)
		fullResult = "Stroke of Luck: the attack roll is a 20, so the attack hits and is a Critical Hit."
	} else {
		modeDesc = "treat ability check as natural 20"
		actionDesc = fmt.Sprintf("%s uses Stroke of Luck to succeed on an ability check!", charName)
		fullResult = "🎲 Stroke of Luck: The ability check result is treated as a natural 20!"
	}

	// Log the action
	writeAction(db, lobbyID, req.CharacterID, "stroke_of_luck", actionDesc, fullResult)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":             true,
		"character":           charName,
		"mode":                modeLower,
		"mode_description":    modeDesc,
		"result":              fullResult,
		"stroke_of_luck_used": true,
		"recovery":            "short or long rest",
		"feature_note":        "Stroke of Luck (Rogue 20): if you fail a D20 Test, you can turn the roll into a 20. Once per Short or Long Rest.",
	})
}
