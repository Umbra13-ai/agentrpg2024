package main

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"strings"
)

// handleGMOpportunityAttack godoc
// @Summary Trigger an opportunity attack
// @Description GM triggers an opportunity attack when a creature leaves another's reach. Uses the attacker's reaction.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{attacker_id=integer,target_id=integer,attacker_is_monster=boolean,weapon=string} true "Opportunity attack details"
// @Success 200 {object} map[string]interface{} "Attack result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request or no reaction available"
// @Router /gm/opportunity-attack [post]
func handleGMOpportunityAttack(w http.ResponseWriter, r *http.Request) {
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
		AttackerID        int    `json:"attacker_id"`         // Character ID (if player) or ignored for monster
		TargetID          int    `json:"target_id"`           // Character ID of the creature provoking
		AttackerIsMonster bool   `json:"attacker_is_monster"` // true if monster is making the attack
		MonsterName       string `json:"monster_name"`        // Name of monster (if attacker_is_monster)
		MonsterKey        string `json:"monster_key"`         // SRD slug for monster stats
		Weapon            string `json:"weapon"`              // Optional: specific weapon to use
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.TargetID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "target_id required (the character being attacked)",
		})
		return
	}
	if companionOpportunityAttack(w, req.TargetID, true) { // a companion (its combat id from GET /campaigns/{id}/combat): Flyby, or how to resolve it
		return
	}

	// Get target character info
	// Fixed AC lookup to use stored ac + cover_bonus instead of recalculating from DEX
	// The ac column already includes armor, shield, natural armor, etc.
	var targetName string
	var targetLobbyID int
	var targetAC int
	err = db.QueryRow(`
		SELECT name, lobby_id, ac + COALESCE(cover_bonus, 0) as effective_ac
		FROM characters WHERE id = $1
	`, req.TargetID).Scan(&targetName, &targetLobbyID, &targetAC)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "target_not_found"})
		return
	}

	if targetLobbyID != campaignID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "target_not_in_campaign"})
		return
	}

	// Multiattack Defense (Hunter 7, 2024): a creature that already hit the target this turn has Disadvantage on its other
	// attack rolls against it
	multiattackDefenseActive := false
	multiattackDefenseNote := ""
	if !req.AttackerIsMonster && req.AttackerID > 0 && multiattackDefenseApplies(req.TargetID, req.AttackerID) {
		multiattackDefenseActive = true
		multiattackDefenseNote = " 🛡️[Multiattack Defense: Disadvantage]"
	}

	var attackerName string
	var attackMod, damageMod int
	var damageDice string
	var weaponName string

	if req.AttackerIsMonster {
		// Monster opportunity attack
		if req.MonsterName == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_request",
				"message": "monster_name required when attacker_is_monster is true",
			})
			return
		}
		attackerName = req.MonsterName

		// Try to get monster stats from SRD
		if req.MonsterKey != "" {
			var mStr, mDex int
			var actionsJSON []byte
			err = db.QueryRow(`
				SELECT COALESCE((abilities->>'str')::int, 10), 
				       COALESCE((abilities->>'dex')::int, 10),
				       actions
				FROM monsters WHERE slug = $1
			`, req.MonsterKey).Scan(&mStr, &mDex, &actionsJSON)

			if err == nil {
				// Use STR for melee
				attackMod = game.Modifier(mStr)
				damageMod = game.Modifier(mStr)

				// Try to find a melee attack in actions
				var actions []map[string]interface{}
				json.Unmarshal(actionsJSON, &actions)

				for _, action := range actions {
					if name, ok := action["name"].(string); ok {
						nameLower := strings.ToLower(name)
						// Look for melee attacks (claws, bite, slam, etc.)
						if strings.Contains(nameLower, "claw") || strings.Contains(nameLower, "bite") || strings.Contains(nameLower, "slam") || strings.Contains(nameLower, "attack") || strings.Contains(nameLower, "sword") {
							weaponName = name
							// Try to parse damage from description
							if desc, ok := action["desc"].(string); ok {
								// Look for damage dice pattern like "2d6 + 4"
								if idx := strings.Index(desc, "d"); idx > 0 {
									// Find the start of the dice
									start := idx - 1
									for start > 0 && (desc[start-1] >= '0' && desc[start-1] <= '9') {
										start--
									}
									// Find end of dice
									end := idx + 1
									for end < len(desc) && ((desc[end] >= '0' && desc[end] <= '9') || desc[end] == '+' || desc[end] == ' ') {
										end++
									}
									if end > idx+1 {
										damageDice = strings.TrimSpace(desc[start:end])
										// Clean up the dice string
										damageDice = strings.ReplaceAll(damageDice, " ", "")
										if plusIdx := strings.Index(damageDice, "+"); plusIdx > 0 {
											damageDice = damageDice[:plusIdx]
										}
									}
								}
							}
							break
						}
					}
				}
			}
		}

		// Defaults if not found
		if weaponName == "" {
			weaponName = "melee attack"
		}
		if damageDice == "" {
			damageDice = "1d6"
		}
		if attackMod == 0 {
			attackMod = 3 // Default +3 for a basic monster
		}

	} else {
		// Player character opportunity attack
		if req.AttackerID == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_request",
				"message": "attacker_id required for character opportunity attacks",
			})
			return
		}

		// Get attacker info and check reaction
		var attackerLobbyID int
		var str, dex, level int
		var reactionUsed bool
		var weaponProfsStr string
		err = db.QueryRow(`
			SELECT name, lobby_id, str, dex, level, COALESCE(reaction_used, false), COALESCE(weapon_proficiencies, '')
			FROM characters WHERE id = $1
		`, req.AttackerID).Scan(&attackerName, &attackerLobbyID, &str, &dex, &level, &reactionUsed, &weaponProfsStr)

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "attacker_not_found"})
			return
		}

		if attackerLobbyID != campaignID {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "attacker_not_in_campaign"})
			return
		}

		// Check if reaction is available
		if reactionUsed {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_reaction",
				"message": fmt.Sprintf("%s has already used their reaction this round", attackerName),
			})
			return
		}

		if req.Weapon != "" && !weaponEquippedForChar(req.AttackerID, req.Weapon) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "weapon_not_equipped", "message": notEquippedMessage(req.Weapon)})
			return
		}

		// Mark reaction as used
		db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, req.AttackerID)

		// Determine weapon and modifiers
		attackMod = game.Modifier(str)
		damageMod = game.Modifier(str)
		damageDice = "1d6"
		weaponName = "unarmed strike"
		weaponKey := ""

		// Check for weapon in request or default to equipped weapon
		if req.Weapon != "" {
			weaponKey = strings.ToLower(strings.ReplaceAll(req.Weapon, " ", "-"))
			if weapon, ok := srdWeapons[weaponKey]; ok {
				weaponName = weapon.Name
				damageDice = weapon.Damage
				if weapon.Type == "ranged" || containsProperty(weapon.Properties, "finesse") {
					attackMod = game.Modifier(dex)
					damageMod = game.Modifier(dex)
				}
			}
		}

		// Add proficiency bonus only if proficient with the weapon
		if weaponKey == "" || isWeaponProficient(weaponProfsStr, weaponKey) {
			attackMod += game.ProficiencyBonus(level)
		}

		// Sacred Weapon (Devotion Paladin Channel Divinity)
		oaSacredBonus, _ := getSacredWeaponBonus(req.AttackerID)
		if oaSacredBonus > 0 {
			attackMod += oaSacredBonus
		}
	}

	// Check for Escape the Horde (Hunter Ranger Defensive Tactics)
	// Opportunity attacks against you are made with disadvantage
	escapeTheHordeActive := hasEscapeTheHorde(req.TargetID)

	// Dodge, Blur, Foresight ...: as for any attack roll against the character (buff_spells_2024.go)
	defenderDis, defenderWhy := buffAttackDisadvantage(campaignID, 0, req.TargetID)

	// Roll the attack (with Disadvantage for Escape the Horde, Multiattack Defense or the target's own defenses)
	var attackRoll int
	var oaRoll1, oaRoll2 int
	if escapeTheHordeActive || multiattackDefenseActive || defenderDis {
		attackRoll, oaRoll1, oaRoll2 = game.RollWithDisadvantage()
	} else {
		attackRoll = game.RollDie(20)
		oaRoll1 = attackRoll
		oaRoll2 = 0
	}

	// Halfling Lucky - reroll nat 1s on attack rolls
	oaHalflingLuckyUsed := false
	oaHalflingLuckyOriginal := 0
	if attackRoll == 1 {
		newRoll, rerolled, origRoll := applyHalflingLucky(attackRoll, req.AttackerID)
		if rerolled {
			oaHalflingLuckyUsed = true
			oaHalflingLuckyOriginal = origRoll
			attackRoll = newRoll
		}
	}

	totalAttack := attackRoll + attackMod

	var resultText string
	var hit bool
	var damage int

	luckyNote := ""
	if oaHalflingLuckyUsed {
		luckyNote = fmt.Sprintf(" 🍀[Lucky: %d→%d]", oaHalflingLuckyOriginal, attackRoll)
	}
	escapeNote := ""
	if escapeTheHordeActive {
		escapeNote = fmt.Sprintf(" 🏃[Escape the Horde: %d/%d→%d]", oaRoll1, oaRoll2, attackRoll)
	} else if defenderDis {
		escapeNote = fmt.Sprintf(" [Disadvantage (%s): %d/%d→%d]", defenderWhy, oaRoll1, oaRoll2, attackRoll)
	}

	if attackRoll == 1 && !oaHalflingLuckyUsed {
		// Critical miss (only if not saved by Halfling Lucky)
		resultText = fmt.Sprintf("⚔️ OPPORTUNITY ATTACK: %s attacks %s as they flee!%s%s%s Attack roll: %d (natural 1: miss)",
			attackerName, targetName, escapeNote, luckyNote, multiattackDefenseNote, totalAttack)
		hit = false
	} else if attackRoll == 20 {
		// Critical hit - double damage dice
		damage = game.RollDamage(damageDice, true) + damageMod
		if damage < 1 {
			damage = 1
		}
		resultText = fmt.Sprintf("⚔️ OPPORTUNITY ATTACK: %s attacks %s as they flee!%s%s%s Attack roll: %d (nat 20 - CRITICAL HIT!) Damage: %d with %s",
			attackerName, targetName, escapeNote, luckyNote, multiattackDefenseNote, totalAttack, damage, weaponName)
		hit = true

		// Record hit for Multiattack Defense tracking
		if !req.AttackerIsMonster && req.AttackerID > 0 {
			recordMultiattackDefenseHit(req.TargetID, req.AttackerID)
		}
	} else if totalAttack >= targetAC {
		// Normal hit
		damage = game.RollDamage(damageDice, false) + damageMod
		if damage < 1 {
			damage = 1
		}
		resultText = fmt.Sprintf("⚔️ OPPORTUNITY ATTACK: %s attacks %s as they flee!%s%s%s Attack roll: %d - HIT! Damage: %d with %s",
			attackerName, targetName, escapeNote, luckyNote, multiattackDefenseNote, totalAttack, damage, weaponName)
		hit = true

		// Record hit for Multiattack Defense tracking
		if !req.AttackerIsMonster && req.AttackerID > 0 {
			recordMultiattackDefenseHit(req.TargetID, req.AttackerID)
		}
	} else {
		// Miss
		resultText = fmt.Sprintf("⚔️ OPPORTUNITY ATTACK: %s attacks %s as they flee!%s%s%s Attack roll: %d - MISS!",
			attackerName, targetName, escapeNote, luckyNote, multiattackDefenseNote, totalAttack)
		hit = false
	}

	// Apply damage to target if hit
	if hit && damage > 0 {
		// Apply damage resistance
		dmgMod := applyDamageResistance(req.TargetID, damage, "")
		if dmgMod.WasHalved {
			damage = dmgMod.FinalDamage
			resultText += fmt.Sprintf(" (Resisted: %s, damage halved to %d)", strings.Join(dmgMod.Resistances, ", "), damage)
		}

		var currentHP, maxHP int
		db.QueryRow(`SELECT hp, max_hp FROM characters WHERE id = $1`, req.TargetID).Scan(&currentHP, &maxHP)
		newHP := currentHP - damage
		if newHP < 0 {
			newHP = 0
		}

		// Check Barbarian Relentless Rage first (requires CON save)
		if newHP == 0 {
			relentlessHP, relentlessUsed, relentlessMsg := checkRelentlessRage(req.TargetID, currentHP, damage, maxHP)
			if relentlessUsed {
				newHP = relentlessHP
				resultText += " " + relentlessMsg
			} else if relentlessMsg != "" {
				// Save failed, add the message but still at 0 HP
				resultText += " " + relentlessMsg
			}
		}

		// Check Orc Relentless Endurance (automatic, no save)
		if newHP == 0 {
			relentlessHP, relentlessUsed, relentlessMsg := checkRelentlessEndurance(req.TargetID, currentHP, damage, maxHP)
			if relentlessUsed {
				newHP = relentlessHP
				resultText += " " + relentlessMsg
			}
		}

		db.Exec(`UPDATE characters SET hp = $1 WHERE id = $2`, newHP, req.TargetID)

		if newHP == 0 {
			resultText += fmt.Sprintf(" %s falls to 0 HP!", targetName)

			// Check for kill effects (Dark One's Blessing, etc.)
			if !req.AttackerIsMonster && req.AttackerID > 0 {
				if killEffects := applyKillEffects(req.AttackerID); killEffects != nil {
					if msg, ok := killEffects["message"].(string); ok {
						resultText += " " + msg
					}
				}
			}
		} else {
			resultText += fmt.Sprintf(" (%s: %d → %d HP)", targetName, currentHP, newHP)
		}
	}

	// Log the action
	actionDesc := fmt.Sprintf("Opportunity attack by %s against %s", attackerName, targetName)
	db.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, 'opportunity_attack', $2, $3)
	`, campaignID, actionDesc, resultText)

	response := map[string]interface{}{
		"success":     true,
		"attacker":    attackerName,
		"target":      targetName,
		"attack_roll": attackRoll,
		"attack_mod":  attackMod,
		"total":       totalAttack,
		"target_ac":   targetAC,
		"hit":         hit,
		"result":      resultText,
	}

	if hit {
		response["damage"] = damage
		response["weapon"] = weaponName
	}

	// Include Multiattack Defense info if it was active
	if multiattackDefenseActive {
		response["multiattack_defense"] = map[string]interface{}{
			"active": true,
			"effect": "Disadvantage on the attack roll",
		}
	}

	if !req.AttackerIsMonster {
		response["reaction_used"] = true
		response["note"] = fmt.Sprintf("%s's reaction is now expended for this round", attackerName)
	}

	json.NewEncoder(w).Encode(response)
}

// handleGMRetaliation godoc
// @Summary Berserker's Retaliation reaction attack
// @Description When a Berserker Barbarian (level 14+) takes damage from a creature within 5 feet, they can use their reaction to make a melee weapon attack against that creature.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=integer,attacker_name=string,attacker_monster_key=string,weapon=string} true "Retaliation details"
// @Success 200 {object} map[string]interface{} "Attack result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request or requirements not met"
// @Router /gm/retaliation [post]
func handleGMRetaliation(w http.ResponseWriter, r *http.Request) {
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
		CharacterID        int    `json:"character_id"`         // The Berserker making the reaction attack
		AttackerName       string `json:"attacker_name"`        // Name of attacking creature
		AttackerMonsterKey string `json:"attacker_monster_key"` // Optional: SRD slug for AC lookup
		Weapon             string `json:"weapon"`               // Optional: specific weapon to use
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_id required (the Berserker making the attack)",
		})
		return
	}

	if req.AttackerName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "attacker_name required (the creature that damaged the Berserker)",
		})
		return
	}

	// Get character info
	var charName, class, subclass string
	var charLobbyID, str, dex, level int
	var reactionUsed bool
	var weaponProfsStr string
	err = db.QueryRow(`
		SELECT name, lobby_id, class, str, dex, level, 
		       COALESCE(reaction_used, false), COALESCE(weapon_proficiencies, ''),
		       COALESCE(subclass, '')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &charLobbyID, &class, &str, &dex, &level,
		&reactionUsed, &weaponProfsStr, &subclass)

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

	// Check if character has Retaliation (Berserker Barbarian level 10+)
	if strings.ToLower(class) != "barbarian" || strings.ToLower(subclass) != "berserker" || level < 10 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_retaliation",
			"message": fmt.Sprintf("%s is not a Berserker Barbarian level 10+ with Retaliation", charName),
		})
		return
	}

	// Check if reaction is available
	if reactionUsed {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_reaction",
			"message": fmt.Sprintf("%s has already used their reaction this round", charName),
		})
		return
	}

	if req.Weapon != "" && !weaponEquippedForChar(req.CharacterID, req.Weapon) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "weapon_not_equipped", "message": notEquippedMessage(req.Weapon)})
		return
	}

	// Mark reaction as used
	db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, req.CharacterID)

	// Determine weapon and modifiers (melee only for Retaliation)
	attackMod := game.Modifier(str)
	damageMod := game.Modifier(str)
	damageDice := "1d4"
	weaponName := "unarmed strike"
	weaponKey := ""

	// Check for weapon in request
	if req.Weapon != "" {
		weaponKey = strings.ToLower(strings.ReplaceAll(req.Weapon, " ", "-"))
		if weapon, ok := srdWeapons[weaponKey]; ok {
			// Must be melee for Retaliation
			if weapon.Type == "melee" {
				weaponName = weapon.Name
				damageDice = weapon.Damage
				// Finesse weapons can use DEX
				if containsProperty(weapon.Properties, "finesse") && dex > str {
					attackMod = game.Modifier(dex)
					damageMod = game.Modifier(dex)
				}
			} else {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "not_melee",
					"message": fmt.Sprintf("%s is not a melee weapon. Retaliation requires a melee weapon attack.", weapon.Name),
				})
				return
			}
		}
	}

	// Add proficiency bonus only if proficient with the weapon
	if weaponKey == "" || isWeaponProficient(weaponProfsStr, weaponKey) {
		attackMod += game.ProficiencyBonus(level)
	}

	// Check for Rage damage bonus (if currently raging)
	var conditionsJSON []byte
	db.QueryRow(`SELECT COALESCE(conditions, '[]') FROM characters WHERE id = $1`, req.CharacterID).Scan(&conditionsJSON)
	isRaging := hasCondition(req.CharacterID, "raging")
	_ = conditionsJSON
	rageBonus := 0
	if isRaging {
		// rage damage bonus now from game.RageDamageBonus
		rageBonus = game.RageDamageBonus(level)
	}

	// Get target AC (monster AC or estimate)
	targetAC := 13 // default
	if req.AttackerMonsterKey != "" {
		var mAC int
		err = db.QueryRow("SELECT COALESCE(ac, 13) FROM monsters WHERE slug = $1", req.AttackerMonsterKey).Scan(&mAC)
		if err == nil {
			targetAC = mAC
		}
	}

	// Roll the attack
	attackRoll := game.RollDie(20)
	totalAttack := attackRoll + attackMod

	var resultText string
	var hit bool
	var damage int

	if attackRoll == 1 {
		// Critical miss
		resultText = fmt.Sprintf("💢 RETALIATION: %s strikes back at %s! Attack roll: %d (natural 1: miss)",
			charName, req.AttackerName, totalAttack)
		hit = false
	} else if attackRoll == 20 {
		// Critical hit - double damage dice
		damage = game.RollDamage(damageDice, true) + damageMod + rageBonus
		if damage < 1 {
			damage = 1
		}
		rageText := ""
		if isRaging {
			rageText = fmt.Sprintf(" (+%d rage)", rageBonus)
		}
		resultText = fmt.Sprintf("💢 RETALIATION: %s strikes back at %s! Attack roll: %d (nat 20 - CRITICAL HIT!) Damage: %d%s with %s",
			charName, req.AttackerName, totalAttack, damage, rageText, weaponName)
		hit = true
	} else if totalAttack >= targetAC {
		// Normal hit
		damage = game.RollDamage(damageDice, false) + damageMod + rageBonus
		if damage < 1 {
			damage = 1
		}
		rageText := ""
		if isRaging {
			rageText = fmt.Sprintf(" (+%d rage)", rageBonus)
		}
		resultText = fmt.Sprintf("💢 RETALIATION: %s strikes back at %s! Attack roll: %d - HIT! Damage: %d%s with %s",
			charName, req.AttackerName, totalAttack, damage, rageText, weaponName)
		hit = true
	} else {
		// Miss
		resultText = fmt.Sprintf("💢 RETALIATION: %s strikes back at %s! Attack roll: %d - MISS!",
			charName, req.AttackerName, totalAttack)
		hit = false
	}

	// Log the action
	actionDesc := fmt.Sprintf("Retaliation attack by %s against %s", charName, req.AttackerName)
	db.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, 'retaliation', $2, $3)
	`, campaignID, actionDesc, resultText)

	response := map[string]interface{}{
		"success":       true,
		"attacker":      charName,
		"target":        req.AttackerName,
		"attack_roll":   attackRoll,
		"attack_mod":    attackMod,
		"total":         totalAttack,
		"target_ac":     targetAC,
		"hit":           hit,
		"result":        resultText,
		"reaction_used": true,
		"note":          fmt.Sprintf("%s's reaction is now expended for this round", charName),
	}

	if hit {
		response["damage"] = damage
		response["weapon"] = weaponName
		if isRaging {
			response["rage_bonus"] = rageBonus
		}
	}

	json.NewEncoder(w).Encode(response)
}

// handleGMUncannyDodge godoc
// @Summary Use Uncanny Dodge to halve attack damage
// @Description When a character with Uncanny Dodge (Rogue 5+, or Hunter Ranger 15+ with the choice) is hit by an attack from an attacker they can see, they can use their reaction to halve the damage.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=integer,damage=integer,attacker_name=string} true "Uncanny Dodge details"
// @Success 200 {object} map[string]interface{} "Damage halved result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request or requirements not met"
// @Router /gm/uncanny-dodge [post]
func handleGMUncannyDodge(w http.ResponseWriter, r *http.Request) {
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
		CharacterID  int    `json:"character_id"`  // The character using Uncanny Dodge
		Damage       int    `json:"damage"`        // The original damage amount
		AttackerName string `json:"attacker_name"` // Name of the attacker (for logging)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "character_id required",
		})
		return
	}

	if req.Damage <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "invalid_request",
			"message": "damage must be a positive integer",
		})
		return
	}

	// Get character info
	var charName, class, subclass string
	var charLobbyID, level int
	var reactionUsed bool
	err = db.QueryRow(`
		SELECT name, lobby_id, class, level, 
		       COALESCE(reaction_used, false),
		       COALESCE(subclass, '')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &charLobbyID, &class, &level, &reactionUsed, &subclass)

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

	// Check if character has Uncanny Dodge
	if !hasUncannyDodge(req.CharacterID) {
		classNote := ""
		if strings.ToLower(class) == "rogue" && level < 5 {
			classNote = fmt.Sprintf(" (Rogue level %d, needs level 5+)", level)
		} else if strings.ToLower(class) == "ranger" && strings.ToLower(subclass) == "hunter" && level < 15 {
			classNote = fmt.Sprintf(" (Hunter Ranger level %d, needs level 15+ with Uncanny Dodge choice)", level)
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_uncanny_dodge",
			"message": fmt.Sprintf("%s does not have the Uncanny Dodge feature%s", charName, classNote),
		})
		return
	}

	// Check if reaction is available
	if reactionUsed {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_reaction",
			"message": fmt.Sprintf("%s has already used their reaction this round", charName),
		})
		return
	}

	// Mark reaction as used
	db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, req.CharacterID)

	// Calculate halved damage (round down)
	halvedDamage := req.Damage / 2

	// Build result text
	attackerText := "the attacker"
	if req.AttackerName != "" {
		attackerText = req.AttackerName
	}

	resultText := fmt.Sprintf("⚡ UNCANNY DODGE: %s reacts with lightning speed, halving the damage from %s! (%d → %d damage)",
		charName, attackerText, req.Damage, halvedDamage)

	// Log the action
	actionDesc := fmt.Sprintf("Uncanny Dodge by %s against %s's attack", charName, attackerText)
	db.Exec(`
		INSERT INTO actions (lobby_id, action_type, description, result)
		VALUES ($1, 'uncanny_dodge', $2, $3)
	`, campaignID, actionDesc, resultText)

	response := map[string]interface{}{
		"success":         true,
		"character":       charName,
		"attacker":        attackerText,
		"original_damage": req.Damage,
		"halved_damage":   halvedDamage,
		"damage_reduced":  req.Damage - halvedDamage,
		"result":          resultText,
		"reaction_used":   true,
		"note":            fmt.Sprintf("%s's reaction is now expended for this round", charName),
	}

	json.NewEncoder(w).Encode(response)
}

// handleGMTriggerReadied godoc
// @Summary GM triggers a character's readied action
// @Description When a player's trigger condition occurs during narration, GM can trigger their readied action. Costs the character's reaction.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=integer} true "Character whose readied action to trigger"
// @Success 200 {object} map[string]interface{} "Readied action result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Not a GM or no readied action"
// @Router /gm/trigger-readied [post]
func handleGMTriggerReadied(w http.ResponseWriter, r *http.Request) {
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
		CharacterID int `json:"character_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// Verify agent is DM of the campaign containing this character
	var lobbyID int
	err = db.QueryRow(`
		SELECT c.lobby_id FROM characters c
		JOIN lobbies l ON c.lobby_id = l.id
		WHERE c.id = $1 AND l.dm_id = $2 AND l.status = 'active'
	`, req.CharacterID, agentID).Scan(&lobbyID)

	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm_or_no_character",
			"message": "Either you're not the DM of this campaign, or the character doesn't exist in an active campaign.",
		})
		return
	}

	// Check for readied action
	var readiedJSON []byte
	var charName string
	err = db.QueryRow("SELECT name, readied_action FROM characters WHERE id = $1", req.CharacterID).Scan(&charName, &readiedJSON)
	if err != nil || readiedJSON == nil || string(readiedJSON) == "null" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "no_readied_action",
			"message": fmt.Sprintf("%s doesn't have a readied action.", charName),
		})
		return
	}

	var readied map[string]string
	json.Unmarshal(readiedJSON, &readied)

	// Check if reaction is available
	var reactionUsed bool
	db.QueryRow("SELECT COALESCE(reaction_used, false) FROM characters WHERE id = $1", req.CharacterID).Scan(&reactionUsed)
	if reactionUsed {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        false,
			"error":          "reaction_used",
			"message":        fmt.Sprintf("%s has already used their reaction this round.", charName),
			"readied_action": readied,
		})
		return
	}

	// Execute the readied action
	result := resolveAction(readied["action"], readied["description"], req.CharacterID)

	// Consume reaction and clear readied action
	db.Exec("UPDATE characters SET reaction_used = true, readied_action = NULL WHERE id = $1", req.CharacterID)

	// Log the action
	db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, $3, $4, $5)
	`, lobbyID, req.CharacterID, "readied_"+readied["action"],
		fmt.Sprintf("GM triggered: %s → %s", readied["trigger"], readied["description"]), result)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"character":         charName,
		"trigger":           readied["trigger"],
		"action":            readied["action"],
		"result":            result,
		"reaction_consumed": true,
		"message":           fmt.Sprintf("%s's readied action triggered! '%s' → %s", charName, readied["trigger"], result),
	})
}
