package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"strings"
)

// handleGMSkillCheck godoc
// @Summary Call for a skill check
// @Description GM calls for a skill check. Server rolls d20 + modifier and compares to DC.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{character_id=integer,skill=string,ability=string,dc=integer,advantage=boolean,disadvantage=boolean} true "Skill check parameters"
// @Success 200 {object} map[string]interface{} "Skill check result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Failure 400 {object} map[string]interface{} "Invalid request"
// @Router /gm/skill-check [post]
func handleGMSkillCheck(w http.ResponseWriter, r *http.Request) {
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
		CharacterID       int    `json:"character_id"`
		Skill             string `json:"skill"`   // e.g., "perception", "athletics"
		Ability           string `json:"ability"` // e.g., "str", "dex" - used if no skill
		DC                int    `json:"dc"`      // Difficulty Class
		Advantage         bool   `json:"advantage"`
		Disadvantage      bool   `json:"disadvantage"`
		Description       string `json:"description"`         // Optional context
		UseInspiration    bool   `json:"use_inspiration"`     // Spend Heroic Inspiration: reroll a failed d20
		TargetID          int    `json:"target_id"`           // Optional: target of the check (for charmed advantage)
		RequiresHearing   bool   `json:"requires_hearing"`    // Auto-fail if deafened
		RequiresSight     bool   `json:"requires_sight"`      // Auto-fail if blinded
		UsePeerlessSkill  bool   `json:"use_peerless_skill"`  // Lore Bard 14+ adds Bardic Inspiration die to own check
		HalfSpeedMovement bool   `json:"half_speed_movement"` // For Supreme Sneak (Thief 9+) - moved no more than half speed this turn
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

	if req.DC == 0 {
		req.DC = 10 // Default DC
	}

	// Get character stats (including inspiration, expertise, subclass, and class)
	var charName string
	var str, dex, con, intl, wis, cha, level int
	var charLobbyID int
	var skillProfsRaw sql.NullString
	var expertiseRaw sql.NullString
	var hasInspiration bool
	var subclassRaw sql.NullString
	var class string
	var classLevelsJSON []byte
	err = db.QueryRow(`
		SELECT name, str, dex, con, intl, wis, cha, level, lobby_id, COALESCE(skill_proficiencies, ''), COALESCE(expertise, ''), COALESCE(inspiration, false), COALESCE(subclass, ''), COALESCE(class, ''), COALESCE(class_levels, '{}')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &str, &dex, &con, &intl, &wis, &cha, &level, &charLobbyID, &skillProfsRaw, &expertiseRaw, &hasInspiration, &subclassRaw, &class, &classLevelsJSON)

	// Parse class levels for multiclass feature checks
	var classLevels map[string]int
	json.Unmarshal(classLevelsJSON, &classLevels)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is in this campaign
	if charLobbyID != campaignID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_in_campaign"})
		return
	}

	// Check for auto-fail conditions (deafened/blinded)
	skillUsedForCheck := strings.ToLower(strings.ReplaceAll(req.Skill, " ", "_"))
	if req.RequiresHearing && hasCondition(req.CharacterID, "deafened") {
		desc := fmt.Sprintf("%s: %s check (DC %d) - auto-fail (deafened)", charName, req.Skill, req.DC)
		if req.Description != "" {
			desc = fmt.Sprintf("%s: %s - %s check (DC %d) - auto-fail (deafened)", charName, req.Description, req.Skill, req.DC)
		}
		_, _ = db.Exec(`
			INSERT INTO actions (lobby_id, character_id, action_type, description, result)
			VALUES ($1, $2, 'skill_check', $3, $4)
		`, campaignID, req.CharacterID, desc, "AUTO-FAIL (deafened)")

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":          false,
			"character":        charName,
			"check":            skillUsedForCheck,
			"auto_fail":        true,
			"auto_fail_reason": "deafened",
			"outcome":          "AUTO-FAIL",
			"result":           fmt.Sprintf("%s check: AUTO-FAIL (%s is deafened and cannot hear)", skillUsedForCheck, charName),
			"condition_note":   fmt.Sprintf("%s is deafened and automatically fails checks requiring hearing", charName),
		})
		return
	}
	if req.RequiresSight && hasCondition(req.CharacterID, "blinded") {
		desc := fmt.Sprintf("%s: %s check (DC %d) - auto-fail (blinded)", charName, req.Skill, req.DC)
		if req.Description != "" {
			desc = fmt.Sprintf("%s: %s - %s check (DC %d) - auto-fail (blinded)", charName, req.Description, req.Skill, req.DC)
		}
		_, _ = db.Exec(`
			INSERT INTO actions (lobby_id, character_id, action_type, description, result)
			VALUES ($1, $2, 'skill_check', $3, $4)
		`, campaignID, req.CharacterID, desc, "AUTO-FAIL (blinded)")

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":          false,
			"character":        charName,
			"check":            skillUsedForCheck,
			"auto_fail":        true,
			"auto_fail_reason": "blinded",
			"outcome":          "AUTO-FAIL",
			"result":           fmt.Sprintf("%s check: AUTO-FAIL (%s is blinded and cannot see)", skillUsedForCheck, charName),
			"condition_note":   fmt.Sprintf("%s is blinded and automatically fails checks requiring sight", charName),
		})
		return
	}

	// Parse skill proficiencies into a set for quick lookup
	skillProfs := make(map[string]bool)
	if skillProfsRaw.Valid && skillProfsRaw.String != "" {
		for _, skill := range strings.Split(skillProfsRaw.String, ",") {
			skillProfs[strings.TrimSpace(strings.ToLower(skill))] = true
		}
	}

	// Parse expertise into a set
	expertiseSkills := make(map[string]bool)
	if expertiseRaw.Valid && expertiseRaw.String != "" {
		for _, exp := range strings.Split(expertiseRaw.String, ",") {
			expertiseSkills[strings.TrimSpace(strings.ToLower(exp))] = true
		}
	}

	// Determine which ability to use
	abilityUsed := strings.ToLower(req.Ability)
	skillUsed := strings.ToLower(strings.ReplaceAll(req.Skill, " ", "_"))

	// If skill provided, map to ability
	primalKnowledge := false
	if skillUsed != "" {
		if mapped, ok := skillAbilityMap[skillUsed]; ok {
			abilityUsed, primalKnowledge = primalKnowledgeAbility(req.CharacterID, skillUsed, req.Ability, mapped) // F101
		}
	}

	// Get the modifier for the ability
	var abilityMod int
	var abilityName string
	switch abilityUsed {
	case "str", "strength":
		abilityMod = game.Modifier(str)
		abilityName = "Strength"
	case "dex", "dexterity":
		abilityMod = game.Modifier(dex)
		abilityName = "Dexterity"
	case "con", "constitution":
		abilityMod = game.Modifier(con)
		abilityName = "Constitution"
	case "int", "intelligence":
		abilityMod = game.Modifier(intl)
		abilityName = "Intelligence"
	case "wis", "wisdom":
		abilityMod = game.Modifier(wis)
		abilityName = "Wisdom"
	case "cha", "charisma":
		abilityMod = game.Modifier(cha)
		abilityName = "Charisma"
	default:
		// Default to wisdom for unknown skills
		abilityMod = game.Modifier(wis)
		abilityName = "Wisdom"
	}

	// Add proficiency bonus if proficient in the skill
	// Double proficiency bonus if the character has expertise
	totalMod := abilityMod
	totalMod -= game.ExhaustionD20Penalty(exhaustionLevelOf(req.CharacterID)) // SRD 5.2.1: -2 per Exhaustion level
	isProficient := false
	hasExpertise := false
	if skillUsed != "" && skillProfs[skillUsed] {
		if expertiseSkills[skillUsed] {
			// Expertise: double proficiency bonus
			totalMod += game.ProficiencyBonus(level) * 2
			hasExpertise = true
		} else {
			totalMod += game.ProficiencyBonus(level)
		}
		isProficient = true
	}

	// Remarkable Athlete (Champion Fighter level 7+)
	// Add half proficiency bonus (rounded up) to STR, DEX, or CON checks when not proficient
	remarkableAthleteBonus := 0
	isPhysicalCheck := abilityUsed == "str" || abilityUsed == "strength" ||
		abilityUsed == "dex" || abilityUsed == "dexterity" ||
		abilityUsed == "con" || abilityUsed == "constitution"
	if !isProficient && subclassRaw.Valid && subclassRaw.String != "" {
		// 2024 Remarkable Athlete is Advantage on Initiative and Athletics checks (below), not a proficiency bonus
		_ = isPhysicalCheck
	}

	// Jack of All Trades (Bard level 2+)
	// Add half proficiency bonus (rounded down) to ability checks not already using proficiency
	// Does not stack with Remarkable Athlete - RA is better for physical checks (rounds up)
	jackOfAllTradesBonus := 0
	if !isProficient && remarkableAthleteBonus == 0 && game.HasClassFeature(class, level, "jack_of_all_trades") {
		// Half proficiency bonus, rounded down
		jackOfAllTradesBonus = game.ProficiencyBonus(level) / 2
		totalMod += jackOfAllTradesBonus
	}

	// 2024 Divine Order (Thaumaturge) / Primal Order (Magician): bonus to Arcana and Religion / Nature checks = Wisdom modifier (minimum +1)
	totalMod += orderSkillBonus(req.CharacterID, skillUsed, wis)
	totalMod += magicCheckBonus(req.CharacterID)             // Stone of Good Luck (F111: missing from the GM's checks)
	totalMod += glovesOfThievery(req.CharacterID, skillUsed) // F165 (items_c5_2024.go)
	if skillUsed != "" {                                     // Wild Shape: the beast's skill modifier when it's higher (Exhaustion still applies)
		exh := game.ExhaustionD20Penalty(exhaustionLevelOf(req.CharacterID))
		totalMod = wildShapeHigher(req.CharacterID, skillUsed, totalMod+exh) - exh
	}

	// Handle Heroic Inspiration (2024): reroll after a failure
	usedInspiration := false
	if req.UseInspiration {
		if hasInspiration {
			db.Exec(`UPDATE characters SET inspiration = false WHERE id = $1`, req.CharacterID)
			usedInspiration = true
		} else {
			// Character doesn't have Heroic Inspiration to spend
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_inspiration",
				"message": fmt.Sprintf("%s doesn't have Heroic Inspiration to spend", charName),
			})
			return
		}
	}

	// O-11: why the roll has Advantage or Disadvantage, so the reply can say when they cancel
	var advWhy, disWhy []string
	if req.Advantage {
		advWhy = append(advWhy, "the GM's ruling")
	}
	if req.Disadvantage {
		disWhy = append(disWhy, "the GM's ruling")
	}

	// Charmed: Charmer has advantage on CHA-based checks against charmed target
	charmedAdvantage := false
	if req.TargetID > 0 && (abilityUsed == "cha" || abilityUsed == "charisma") {
		if isCharmedBy(req.TargetID, req.CharacterID) {
			req.Advantage = true
			charmedAdvantage = true
			advWhy = append(advWhy, "Charmed target")
		}
	}

	// 2024 Remarkable Athlete (Champion): Advantage on Strength (Athletics) checks
	if skillUsed == "athletics" && subclassRaw.Valid && hasSubclassFeature(subclassRaw.String, level, "remarkable_athlete") {
		req.Advantage = true
		advWhy = append(advWhy, "Remarkable Athlete")
	}

	// Supreme Sneak (Thief Rogue level 9+) - advantage on Stealth if moved no more than half speed
	supremeSneakAdvantage := false
	if skillUsed == "stealth" && req.HalfSpeedMovement {
		if subclassRaw.Valid && hasSubclassFeature(subclassRaw.String, level, "supreme_sneak") {
			req.Advantage = true
			supremeSneakAdvantage = true
			advWhy = append(advWhy, "Supreme Sneak")
		}
	}

	if bootsOfElvenkind(req.CharacterID, skillUsed) { // F112 (rogue_fixes_2024.go)
		req.Advantage = true
		advWhy = append(advWhy, "Boots of Elvenkind / Cloak of the Bat")
	}

	// Poisoned condition gives disadvantage on ability checks
	poisonedDisadvantage := false
	if hasCondition(req.CharacterID, "poisoned") {
		req.Disadvantage = true
		poisonedDisadvantage = true
		disWhy = append(disWhy, "Poisoned")
	}

	// Exhaustion level 1+ gives disadvantage on ability checks
	exhaustionDisadvantage := false
	var charExhaustion int
	db.QueryRow("SELECT COALESCE(exhaustion_level, 0) FROM characters WHERE id = $1", req.CharacterID).Scan(&charExhaustion)
	if charExhaustion >= 1 {
		exhaustionDisadvantage = false // 2024: Exhaustion is a -2 per level penalty on the D20 Test, not Disadvantage
	}

	// Non-proficient armor gives disadvantage on STR/DEX ability checks
	armorDisadvantage := false
	isStrOrDexCheck := abilityUsed == "str" || abilityUsed == "strength" || abilityUsed == "dex" || abilityUsed == "dexterity"
	if isStrOrDexCheck && isWearingNonProficientArmor(req.CharacterID) {
		req.Disadvantage = true
		armorDisadvantage = true
		disWhy = append(disWhy, "armor without proficiency")
	}
	// SRD 5.2.1 Armor, Stealth: armor marked Disadvantage in the Stealth column gives Disadvantage on Dexterity (Stealth) checks
	if skillUsed == "stealth" && armorStealthDisadvantage(req.CharacterID) {
		req.Disadvantage = true
		disWhy = append(disWhy, "armor (Stealth Disadvantage)")
	}
	largeForm := largeFormAdvantage(req.CharacterID, abilityUsed) // Goliath Large Form: Advantage on Strength checks
	if largeForm {
		req.Advantage = true
		advWhy = append(advWhy, "Large Form")
	}
	if takeHelpedCheck(req.CharacterID, skillUsed) {
		req.Advantage = true // an ally's Help (Assist an Ability Check)
		advWhy = append(advWhy, "Help")
	}
	// Travel pace (Fast: Disadvantage on Perception, Survival, Stealth; Normal: on Stealth; Slow: Advantage on Perception, Survival)
	if pAdv, pDis, _ := travelPaceEffect(lobbyOfChar(req.CharacterID), skillUsed); pAdv {
		req.Advantage = true
		advWhy = append(advWhy, "travel pace")
	} else if pDis {
		req.Disadvantage = true
		disWhy = append(disWhy, "travel pace")
	}
	buffCheckWhy := ""
	if ok, why := itemSkillAdvantage(req.CharacterID, skillUsed); ok { // Cloak of Elvenkind, Eyes of the Eagle, Robe of Eyes ... (F204)
		req.Advantage = true
		buffCheckWhy += " [Advantage: " + why + "]"
		advWhy = append(advWhy, why)
	}
	if ok, why := buffCheckAdvantage(req.CharacterID, abilityUsed); ok { // Foresight, Enhance Ability (buff_spells_2024.go)
		req.Advantage = true
		buffCheckWhy = " [Advantage: " + why + "]"
		advWhy = append(advWhy, why)
	}
	if dis, why := moreCheckDisadvantage(req.CharacterID, abilityUsed); dis { // Reduce (spells_more_2024.go)
		req.Disadvantage = true
		buffCheckWhy += " [Disadvantage: " + why + "]"
		disWhy = append(disWhy, why)
	}

	// Roll the die
	var roll1, roll2, finalRoll int
	rollType := "normal"

	if req.Advantage && !req.Disadvantage {
		finalRoll, roll1, roll2 = game.RollWithAdvantage()
		rollType = "advantage"
		if usedInspiration {
			rollType = "advantage (inspiration)"
		}
		if charmedAdvantage {
			rollType = "advantage (charmed)"
		}
		if supremeSneakAdvantage {
			rollType = "advantage (Supreme Sneak)"
		}
		if largeForm {
			rollType = "advantage (Large Form)"
		}
	} else if req.Disadvantage && !req.Advantage {
		finalRoll, roll1, roll2 = game.RollWithDisadvantage()
		rollType = "disadvantage"
		// Build descriptive reason for disadvantage
		reasons := []string{}
		if poisonedDisadvantage {
			reasons = append(reasons, "poisoned")
		}
		if exhaustionDisadvantage {
			reasons = append(reasons, "exhaustion")
		}
		if armorDisadvantage {
			reasons = append(reasons, "non-proficient armor")
		}
		if len(reasons) > 0 {
			rollType = "disadvantage (" + strings.Join(reasons, ", ") + ")"
		}
	} else {
		finalRoll = game.RollDie(20)
		roll1 = finalRoll
		if req.Advantage && req.Disadvantage { // O-11: say why there is one d20
			rollType = "normal (Advantage from " + strings.Join(advWhy, ", ") + " and Disadvantage from " + strings.Join(disWhy, ", ") + " cancel)"
		}
		roll2 = 0
	}

	// Halfling Lucky
	// When you roll a 1 on the d20, you can reroll and must use the new roll
	halflingLuckyUsed := false
	halflingLuckyOriginal := 0
	if finalRoll == 1 {
		newRoll, rerolled, origRoll := applyHalflingLucky(finalRoll, req.CharacterID)
		if rerolled {
			halflingLuckyUsed = true
			halflingLuckyOriginal = origRoll
			finalRoll = newRoll
			// Update roll1 to reflect the reroll for display purposes
			roll1 = newRoll
		}
	}

	// Reliable Talent (Rogue level 11+)
	// Treat d20 rolls of 9 or lower as 10 on ability checks with proficiency
	reliableTalentApplied := false
	originalRoll := finalRoll
	if isProficient && game.HasClassFeature(class, level, "reliable_talent") && finalRoll <= 9 {
		finalRoll = 10
		reliableTalentApplied = true
	}

	// Peerless Skill (Lore Bard level 14+)
	// When you make an ability check, you can expend one use of Bardic Inspiration
	// Roll a Bardic Inspiration die and add the number rolled to your ability check
	peerlessSkillApplied := false
	peerlessSkillRoll := 0
	peerlessSkillRemaining := 0
	if req.UsePeerlessSkill {
		// Check if character has Peerless Skill feature
		if !hasSubclassFeature(subclassRaw.String, level, "peerless_skill") {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_peerless_skill",
				"message": fmt.Sprintf("%s doesn't have Peerless Skill (requires College of Lore Bard level 14+)", charName),
			})
			return
		}

		// Check and consume Bardic Inspiration
		success, errMsg, remaining := useClassResource(req.CharacterID, "bardic_inspiration", 1)
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
		peerlessSkillRoll = game.RollDie(dieSize)
		peerlessSkillApplied = true
		peerlessSkillRemaining = remaining
	}

	inspirationRerolled := false
	guidanceRoll, guidanceNote := guidanceBonus(campaignID, req.CharacterID, skillUsed) // Guidance: +1d4 with the chosen skill
	pwtBonus, pwtNote := 0, ""
	if skillUsed == "stealth" {
		pwtBonus, pwtNote = buffStealthBonus(req.CharacterID) // Pass without Trace +10 (buff_spells_2024.go)
	}
	total := finalRoll + totalMod + peerlessSkillRoll + guidanceRoll + pwtBonus
	total, mightNote := indomitableMight(req.CharacterID, abilityUsed, total) // F102 (barbarian_fixes_2024.go)
	if primalKnowledge {
		mightNote += " [Primal Knowledge: a Strength check while raging]"
	}
	if usedInspiration && req.DC > 0 && total < req.DC { // 2024 Heroic Inspiration: reroll the die after a failure and use the new roll
		oldRoll := finalRoll
		finalRoll = game.RollDie(20)
		roll1, roll2 = finalRoll, 0
		total = total - oldRoll + finalRoll
		inspirationRerolled = true
	}
	if req.DC > 0 && total < req.DC && luckReroll(req.CharacterID, false) { // the Luck Blade's Luck rerolls a failed check too (O-3)
		oldRoll := finalRoll
		finalRoll = game.RollDie(20)
		roll1, roll2 = finalRoll, 0
		total = total - oldRoll + finalRoll
		rollType = "normal"
		pwtNote += fmt.Sprintf(" [Luck Blade: the %d is rerolled and the new roll stands]", oldRoll)
	}
	guidanceNote += buffCheckWhy + pwtNote

	// Indomitable Might (Barbarian 18+)
	// If the total for a Strength check is less than STR score, use STR score instead
	indomitableMightApplied := false
	indomitableMightOriginalTotal := total
	indomitableMightStrScore := 0
	isStrCheck := abilityUsed == "str" || abilityUsed == "strength"
	if isStrCheck {
		newTotal, applied, effectiveStr := applyIndomitableMight(total, str, class, level, classLevels)
		if applied {
			indomitableMightApplied = true
			indomitableMightOriginalTotal = total
			indomitableMightStrScore = effectiveStr
			total = newTotal
		}
	}

	success := total >= req.DC

	// Format check name
	checkName := skillUsed
	if checkName == "" {
		checkName = strings.ToLower(abilityName)
	}

	// Build result description
	resultStr := fmt.Sprintf("d20(%d)", finalRoll)
	if halflingLuckyUsed {
		// Show the reroll: "d20(1→X Lucky)"
		resultStr = fmt.Sprintf("d20(%d→%d Lucky)", halflingLuckyOriginal, finalRoll)
	}
	if reliableTalentApplied {
		if halflingLuckyUsed {
			resultStr = fmt.Sprintf("d20(%d→%d Lucky→10)", halflingLuckyOriginal, originalRoll)
		} else {
			resultStr = fmt.Sprintf("d20(%d→10)", originalRoll)
		}
	} else if rollType == "advantage" {
		resultStr = fmt.Sprintf("d20(%d,%d→%d)", roll1, roll2, finalRoll)
	} else if rollType == "disadvantage" {
		if halflingLuckyUsed {
			resultStr = fmt.Sprintf("d20(%d,%d→%d→%d Lucky)", roll1, roll2, halflingLuckyOriginal, finalRoll)
		} else {
			resultStr = fmt.Sprintf("d20(%d,%d→%d)", roll1, roll2, finalRoll)
		}
	}

	modStr := ""
	if totalMod >= 0 {
		modStr = fmt.Sprintf("+%d", totalMod)
	} else {
		modStr = fmt.Sprintf("%d", totalMod)
	}

	outcomeStr := "FAILURE"
	if success {
		outcomeStr = "SUCCESS"
	}

	// A natural 20 or 1 means nothing special on an ability check (only attack rolls and death saves use them).

	// Build Peerless Skill string if used
	peerlessStr := ""
	if peerlessSkillApplied {
		peerlessStr = fmt.Sprintf("+d%d(%d)", getBardicInspirationDie(level), peerlessSkillRoll)
	}

	fullResult := fmt.Sprintf("%s check: %s%s%s = %d vs DC %d → %s%s",
		titleCase(checkName), resultStr, modStr, peerlessStr, total, req.DC, outcomeStr, guidanceNote)

	// Record the skill check
	desc := fmt.Sprintf("%s: %s check (DC %d)", charName, titleCase(checkName), req.DC)
	if req.Description != "" {
		desc = fmt.Sprintf("%s: %s - %s check (DC %d)", charName, req.Description, titleCase(checkName), req.DC)
	}

	_, _ = db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, 'skill_check', $3, $4)
	`, campaignID, req.CharacterID, desc, fullResult)

	response := map[string]interface{}{
		"success":    success,
		"character":  charName,
		"check":      checkName,
		"ability":    abilityName,
		"roll":       finalRoll,
		"roll_type":  rollType,
		"modifier":   totalMod,
		"proficient": isProficient,
		"expertise":  hasExpertise,
		"total":      total,
		"dc":         req.DC,
		"outcome":    outcomeStr,
		"result":     fullResult,
		"rolls_detail": map[string]interface{}{
			"die1": roll1,
			"die2": roll2,
		},
	}
	if mightNote != "" {
		response["class_feature_note"] = strings.TrimSpace(mightNote) // F101, F102
	}
	if usedInspiration {
		response["used_inspiration"] = true
		response["inspiration_note"] = fmt.Sprintf("%s spent Heroic Inspiration: rerolled=%v (a failed roll is rolled again and the new roll is used)", charName, inspirationRerolled)
	}
	// Add Remarkable Athlete note
	if remarkableAthleteBonus > 0 {
		response["remarkable_athlete"] = true
		response["remarkable_athlete_bonus"] = remarkableAthleteBonus
		response["class_feature_note"] = fmt.Sprintf("%s adds +%d from Remarkable Athlete (half proficiency for physical checks)", charName, remarkableAthleteBonus)
	}
	// Add Jack of All Trades note
	if jackOfAllTradesBonus > 0 {
		response["jack_of_all_trades"] = true
		response["jack_of_all_trades_bonus"] = jackOfAllTradesBonus
		response["class_feature_note"] = fmt.Sprintf("%s adds +%d from Jack of All Trades (half proficiency for non-proficient checks)", charName, jackOfAllTradesBonus)
	}
	// Add Reliable Talent note
	if reliableTalentApplied {
		response["reliable_talent"] = true
		response["original_roll"] = originalRoll
		response["class_feature_note"] = fmt.Sprintf("%s's Reliable Talent: rolled %d, treated as 10", charName, originalRoll)
	}
	// Add Halfling Lucky note
	if halflingLuckyUsed {
		response["halfling_lucky"] = true
		response["halfling_lucky_original"] = halflingLuckyOriginal
		response["halfling_lucky_reroll"] = finalRoll
		response["racial_feature_note"] = fmt.Sprintf("🍀 %s's Halfling Lucky: rerolled nat 1 → %d", charName, finalRoll)
	}
	// Add Peerless Skill note
	if peerlessSkillApplied {
		response["peerless_skill"] = true
		response["peerless_skill_roll"] = peerlessSkillRoll
		response["peerless_skill_die"] = fmt.Sprintf("d%d", getBardicInspirationDie(level))
		response["bardic_inspiration_remaining"] = peerlessSkillRemaining
		response["class_feature_note"] = fmt.Sprintf("🎭 %s uses Peerless Skill: rolled d%d = %d, added to check (%d Bardic Inspiration remaining)", charName, getBardicInspirationDie(level), peerlessSkillRoll, peerlessSkillRemaining)
	}
	// Add condition notes for disadvantage sources
	if poisonedDisadvantage {
		response["poisoned"] = true
		response["condition_note"] = fmt.Sprintf("%s has disadvantage on ability checks (poisoned)", charName)
	}
	if exhaustionDisadvantage {
		response["exhausted"] = true
		response["exhaustion_level"] = charExhaustion
		if poisonedDisadvantage {
			response["condition_note"] = fmt.Sprintf("%s has disadvantage on ability checks (poisoned, exhaustion level %d)", charName, charExhaustion)
		} else {
			response["condition_note"] = fmt.Sprintf("%s has disadvantage on ability checks (exhaustion level %d)", charName, charExhaustion)
		}
	}
	// Add Indomitable Might note
	if indomitableMightApplied {
		response["indomitable_might"] = true
		response["indomitable_might_original_total"] = indomitableMightOriginalTotal
		response["indomitable_might_str_score"] = indomitableMightStrScore
		response["class_feature_note"] = fmt.Sprintf("💪 %s's Indomitable Might: total %d replaced with STR score %d", charName, indomitableMightOriginalTotal, indomitableMightStrScore)
	}
	json.NewEncoder(w).Encode(response)
}

// handleGMToolCheck godoc
// @Summary Call for a tool check
// @Description GM calls for a tool check (thieves' tools, a herbalism kit, "gaming set (dice)", a lute...). The server rolls d20 + the tool's ability (from the tool table unless you send ability) + the Proficiency Bonus if the character is proficient with the tool ("dice", "dice set" and "gaming set (dice)" are the same tool; asking for a whole kind, "gaming set" or "musical instrument", counts any tool of that kind). Send skill when a skill also applies (SRD 5.2.1): proficiency in it adds the Proficiency Bonus (Expertise doubles it), and proficiency in both the tool and the skill gives Advantage. utilize names an SRD Utilize action of the tool and sets the DC.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,tool=string,skill=string,utilize=string,ability=string,dc=int,advantage=bool,disadvantage=bool,description=string,use_inspiration=bool,use_peerless_skill=bool} true "Tool check details"
// @Success 200 {object} map[string]interface{} "Tool check result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /gm/tool-check [post]
func handleGMToolCheck(w http.ResponseWriter, r *http.Request) {
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
		CharacterID      int    `json:"character_id"`
		Tool             string `json:"tool"`    // e.g., "thieves' tools", "herbalism kit"
		Skill            string `json:"skill"`   // a skill that also applies to the check (SRD 5.2.1: proficiency in both gives Advantage)
		Ability          string `json:"ability"` // e.g., "dex" - defaults based on tool if omitted
		DC               int    `json:"dc"`      // Difficulty Class
		Advantage        bool   `json:"advantage"`
		Disadvantage     bool   `json:"disadvantage"`
		Description      string `json:"description"`        // Optional context
		UseInspiration   bool   `json:"use_inspiration"`    // Spend Heroic Inspiration: reroll a failed d20
		UsePeerlessSkill bool   `json:"use_peerless_skill"` // Lore Bard 14+ adds Bardic Inspiration die to own check
		Utilize          string `json:"utilize"`            // an SRD Utilize action of the tool, e.g. "pick a lock": sets the DC (and the ability) when you leave them out
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}
	if req.Utilize != "" && req.Tool != "" {
		if dc, ability, ok := srdUtilizeDC(req.Tool, req.Utilize); ok {
			if req.DC == 0 {
				req.DC = dc
			}
			if req.Ability == "" {
				req.Ability = ability
			}
		} else {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "unknown_utilize_action", "message": "That tool has no such Utilize action in the SRD. See GET /api/universe/items/tool/{slug} (utilize_entries), or give a dc."})
			return
		}
	}

	if req.CharacterID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_id required"})
		return
	}

	if req.Tool == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "tool required", "example": "thieves' tools"})
		return
	}

	if req.DC == 0 {
		req.DC = 15 // Default DC for tool checks
	}

	// Get character stats (including expertise for, subclass for)
	var charName string
	var str, dex, con, intl, wis, cha, level int
	var charLobbyID int
	var toolProfsRaw string
	var expertiseRaw string
	var hasInspiration bool
	var toolSubclassRaw sql.NullString
	var toolClass string
	var toolClassLevelsJSON []byte
	err = db.QueryRow(`
		SELECT name, str, dex, con, intl, wis, cha, level, lobby_id, 
			COALESCE(tool_proficiencies, ''), COALESCE(expertise, ''), COALESCE(inspiration, false), COALESCE(subclass, ''), COALESCE(class, ''), COALESCE(class_levels, '{}')
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &str, &dex, &con, &intl, &wis, &cha, &level, &charLobbyID, &toolProfsRaw, &expertiseRaw, &hasInspiration, &toolSubclassRaw, &toolClass, &toolClassLevelsJSON)

	// Parse class levels for multiclass feature checks
	var toolClassLevels map[string]int
	json.Unmarshal(toolClassLevelsJSON, &toolClassLevels)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is in this campaign
	if charLobbyID != campaignID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_in_campaign"})
		return
	}

	// Tool proficiencies: "dice", "Dice Set" and "Gaming Set (Dice)" are the same tool (BUGS 45). Expertise is for skills (2024).
	ownedTools := game.ParseProficiencyList(toolProfsRaw)

	// Normalize the requested tool name
	toolLower := strings.TrimSpace(strings.ToLower(req.Tool))

	// Default ability: the tool's own (the tool table), then by kind
	abilityUsed := strings.ToLower(req.Ability)
	if t, ok := game.FindTool(req.Tool); ok && abilityUsed == "" {
		abilityUsed = t.Ability
	} else if kind, isKind := game.ToolKind(req.Tool); isKind && abilityUsed == "" {
		abilityUsed = map[string]string{game.ToolGaming: "wis", game.ToolInstrument: "cha"}[kind]
	}
	if abilityUsed == "" {
		// Default abilities for common tools
		switch {
		case strings.Contains(toolLower, "thieves"):
			abilityUsed = "dex"
		case strings.Contains(toolLower, "herbalism"):
			abilityUsed = "wis"
		case strings.Contains(toolLower, "navigator"):
			abilityUsed = "wis"
		case strings.Contains(toolLower, "smith") || strings.Contains(toolLower, "mason") || strings.Contains(toolLower, "carpenter"):
			abilityUsed = "str"
		case strings.Contains(toolLower, "calligrapher") || strings.Contains(toolLower, "cartographer") || strings.Contains(toolLower, "painter"):
			abilityUsed = "dex"
		case strings.Contains(toolLower, "alchemist") || strings.Contains(toolLower, "tinker"):
			abilityUsed = "int"
		default:
			// Musical instruments and gaming sets often use DEX or CHA
			if strings.Contains(toolLower, "instrument") || strings.HasSuffix(toolLower, "set") {
				abilityUsed = "cha"
			} else {
				abilityUsed = "dex" // Default fallback
			}
		}
	}

	// Get ability modifier
	var abilityMod int
	var abilityName string
	switch abilityUsed {
	case "str", "strength":
		abilityMod = game.Modifier(str)
		abilityName = "Strength"
	case "dex", "dexterity":
		abilityMod = game.Modifier(dex)
		abilityName = "Dexterity"
	case "con", "constitution":
		abilityMod = game.Modifier(con)
		abilityName = "Constitution"
	case "int", "intelligence":
		abilityMod = game.Modifier(intl)
		abilityName = "Intelligence"
	case "wis", "wisdom":
		abilityMod = game.Modifier(wis)
		abilityName = "Wisdom"
	case "cha", "charisma":
		abilityMod = game.Modifier(cha)
		abilityName = "Charisma"
	default:
		abilityMod = game.Modifier(dex)
		abilityName = "Dexterity"
	}

	// Check for proficiency and expertise
	totalMod := abilityMod
	totalMod -= game.ExhaustionD20Penalty(exhaustionLevelOf(req.CharacterID)) // SRD 5.2.1: -2 per Exhaustion level
	isProficient := game.HasToolProficiency(ownedTools, req.Tool)
	// SRD 5.2.1: a skill that's also used with the check. Proficiency in it adds the Proficiency Bonus (Expertise doubles it), and
	// proficiency in both the tool and the skill gives Advantage.
	skillProficient, hasExpertise := false, false
	if req.Skill != "" {
		skillProficient, hasExpertise = characterSkillProficiency(req.CharacterID, req.Skill)
	}
	toolAndSkill := isProficient && skillProficient
	if toolAndSkill {
		req.Advantage = true
	}
	isProficient = isProficient || skillProficient
	if isProficient {
		if hasExpertise {
			// Expertise: double proficiency bonus
			totalMod += game.ProficiencyBonus(level) * 2
		} else {
			totalMod += game.ProficiencyBonus(level)
		}
	}

	// Remarkable Athlete (Champion Fighter level 7+)
	// Add half proficiency bonus (rounded up) to STR, DEX, or CON checks when not proficient
	toolRemarkableAthleteBonus := 0
	toolIsPhysicalCheck := abilityUsed == "str" || abilityUsed == "strength" ||
		abilityUsed == "dex" || abilityUsed == "dexterity" ||
		abilityUsed == "con" || abilityUsed == "constitution"
	if !isProficient && toolSubclassRaw.Valid && toolSubclassRaw.String != "" {
		_ = toolIsPhysicalCheck // 2024 Remarkable Athlete no longer adds a bonus to other checks
	}

	// Jack of All Trades (Bard level 2+)
	// Add half proficiency bonus (rounded down) to ability checks not already using proficiency
	// Does not stack with Remarkable Athlete - RA is better for physical checks (rounds up)
	toolJackOfAllTradesBonus := 0
	if !isProficient && toolRemarkableAthleteBonus == 0 && game.HasClassFeature(toolClass, level, "jack_of_all_trades") {
		// Half proficiency bonus, rounded down
		toolJackOfAllTradesBonus = game.ProficiencyBonus(level) / 2
		totalMod += toolJackOfAllTradesBonus
	}

	// Handle inspiration
	usedInspiration := false
	if req.UseInspiration {
		if hasInspiration {
			db.Exec(`UPDATE characters SET inspiration = false WHERE id = $1`, req.CharacterID)
			usedInspiration = true
		} else {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_inspiration",
				"message": fmt.Sprintf("%s doesn't have Heroic Inspiration to spend", charName),
			})
			return
		}
	}

	// Poisoned condition gives disadvantage on ability checks (tool checks are ability checks)
	poisonedDisadvantage := false
	if hasCondition(req.CharacterID, "poisoned") {
		req.Disadvantage = true
		poisonedDisadvantage = true
	}

	// Exhaustion level 1+ gives disadvantage on ability checks
	exhaustionDisadvantage := false
	var charExhaustion int
	db.QueryRow("SELECT COALESCE(exhaustion_level, 0) FROM characters WHERE id = $1", req.CharacterID).Scan(&charExhaustion)
	if charExhaustion >= 1 {
		exhaustionDisadvantage = false // 2024: Exhaustion is a -2 per level penalty on the D20 Test, not Disadvantage
	}

	// Non-proficient armor gives disadvantage on STR/DEX ability checks
	armorDisadvantage := false
	isStrOrDexTool := abilityUsed == "str" || abilityUsed == "strength" || abilityUsed == "dex" || abilityUsed == "dexterity"
	if isStrOrDexTool && isWearingNonProficientArmor(req.CharacterID) {
		req.Disadvantage = true
		armorDisadvantage = true
	}
	if largeFormAdvantage(req.CharacterID, abilityUsed) { // Goliath Large Form
		req.Advantage = true
	}

	// Roll the die
	var roll1, roll2, finalRoll int
	rollType := "normal"

	if takeHelpedCheck(req.CharacterID, req.Tool) {
		req.Advantage = true // an ally's Help (Assist an Ability Check)
	}
	if req.Advantage && !req.Disadvantage {
		finalRoll, roll1, roll2 = game.RollWithAdvantage()
		rollType = "advantage"
		if usedInspiration {
			rollType = "advantage (inspiration)"
		}
	} else if req.Disadvantage && !req.Advantage {
		finalRoll, roll1, roll2 = game.RollWithDisadvantage()
		rollType = "disadvantage"
		// Build descriptive reason for disadvantage
		toolReasons := []string{}
		if poisonedDisadvantage {
			toolReasons = append(toolReasons, "poisoned")
		}
		if exhaustionDisadvantage {
			toolReasons = append(toolReasons, "exhaustion")
		}
		if armorDisadvantage {
			toolReasons = append(toolReasons, "non-proficient armor")
		}
		if len(toolReasons) > 0 {
			rollType = "disadvantage (" + strings.Join(toolReasons, ", ") + ")"
		}
	} else {
		finalRoll = game.RollDie(20)
		roll1 = finalRoll
		roll2 = 0
	}

	// Halfling Lucky
	// When you roll a 1 on the d20, you can reroll and must use the new roll
	toolHalflingLuckyUsed := false
	toolHalflingLuckyOriginal := 0
	if finalRoll == 1 {
		newRoll, rerolled, origRoll := applyHalflingLucky(finalRoll, req.CharacterID)
		if rerolled {
			toolHalflingLuckyUsed = true
			toolHalflingLuckyOriginal = origRoll
			finalRoll = newRoll
			roll1 = newRoll
		}
	}

	// Reliable Talent (Rogue level 11+)
	// Treat d20 rolls of 9 or lower as 10 on ability checks with proficiency
	toolReliableTalentApplied := false
	toolOriginalRoll := finalRoll
	if isProficient && game.HasClassFeature(toolClass, level, "reliable_talent") && finalRoll <= 9 {
		finalRoll = 10
		toolReliableTalentApplied = true
	}

	// Peerless Skill (Lore Bard level 14+)
	// When you make an ability check, you can expend one use of Bardic Inspiration
	// Roll a Bardic Inspiration die and add the number rolled to your ability check
	toolPeerlessSkillApplied := false
	toolPeerlessSkillRoll := 0
	toolPeerlessSkillRemaining := 0
	if req.UsePeerlessSkill {
		// Check if character has Peerless Skill feature
		if !hasSubclassFeature(toolSubclassRaw.String, level, "peerless_skill") {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_peerless_skill",
				"message": fmt.Sprintf("%s doesn't have Peerless Skill (requires College of Lore Bard level 14+)", charName),
			})
			return
		}

		// Check and consume Bardic Inspiration
		success, errMsg, remaining := useClassResource(req.CharacterID, "bardic_inspiration", 1)
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
		toolPeerlessSkillRoll = game.RollDie(dieSize)
		toolPeerlessSkillApplied = true
		toolPeerlessSkillRemaining = remaining
	}

	inspirationRerolled := false
	total := finalRoll + totalMod + toolPeerlessSkillRoll
	if usedInspiration && req.DC > 0 && total < req.DC { // 2024 Heroic Inspiration: reroll the die after a failure and use the new roll
		oldRoll := finalRoll
		finalRoll = game.RollDie(20)
		roll1, roll2 = finalRoll, 0
		total = total - oldRoll + finalRoll
		inspirationRerolled = true
	}

	// Indomitable Might (Barbarian 18+)
	// If the total for a Strength check is less than STR score, use STR score instead
	toolIndomitableMightApplied := false
	toolIndomitableMightOriginalTotal := total
	toolIndomitableMightStrScore := 0
	isToolStrCheck := abilityUsed == "str" || abilityUsed == "strength"
	if isToolStrCheck {
		newTotal, applied, effectiveStr := applyIndomitableMight(total, str, toolClass, level, toolClassLevels)
		if applied {
			toolIndomitableMightApplied = true
			toolIndomitableMightOriginalTotal = total
			toolIndomitableMightStrScore = effectiveStr
			total = newTotal
		}
	}

	success := total >= req.DC

	// Build result description
	resultStr := fmt.Sprintf("d20(%d)", finalRoll)
	if toolHalflingLuckyUsed {
		resultStr = fmt.Sprintf("d20(%d→%d Lucky)", toolHalflingLuckyOriginal, finalRoll)
	}
	if toolReliableTalentApplied {
		if toolHalflingLuckyUsed {
			resultStr = fmt.Sprintf("d20(%d→%d Lucky→10)", toolHalflingLuckyOriginal, toolOriginalRoll)
		} else {
			resultStr = fmt.Sprintf("d20(%d→10)", toolOriginalRoll)
		}
	} else if rollType == "advantage" || rollType == "advantage (inspiration)" {
		resultStr = fmt.Sprintf("d20(%d,%d→%d)", roll1, roll2, finalRoll)
	} else if strings.HasPrefix(rollType, "disadvantage") {
		if toolHalflingLuckyUsed {
			resultStr = fmt.Sprintf("d20(%d,%d→%d→%d Lucky)", roll1, roll2, toolHalflingLuckyOriginal, finalRoll)
		} else {
			resultStr = fmt.Sprintf("d20(%d,%d→%d)", roll1, roll2, finalRoll)
		}
	}

	// Build Peerless Skill string if used
	toolPeerlessStr := ""
	if toolPeerlessSkillApplied {
		toolPeerlessStr = fmt.Sprintf("+d%d(%d)", getBardicInspirationDie(level), toolPeerlessSkillRoll)
	}

	modStr := ""
	if totalMod >= 0 {
		modStr = fmt.Sprintf("+%d", totalMod)
	} else {
		modStr = fmt.Sprintf("%d", totalMod)
	}

	outcomeStr := "FAILURE"
	if success {
		outcomeStr = "SUCCESS"
	}

	// A natural 20 or 1 means nothing special on an ability check (only attack rolls and death saves use them).

	fullResult := fmt.Sprintf("%s check (%s): %s%s%s = %d vs DC %d → %s",
		req.Tool, abilityName, resultStr, modStr, toolPeerlessStr, total, req.DC, outcomeStr)

	// Record the tool check
	desc := fmt.Sprintf("%s: %s check (DC %d)", charName, req.Tool, req.DC)
	if req.Description != "" {
		desc = fmt.Sprintf("%s: %s - %s check (DC %d)", charName, req.Description, req.Tool, req.DC)
	}

	_, _ = db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, 'tool_check', $3, $4)
	`, campaignID, req.CharacterID, desc, fullResult)

	response := map[string]interface{}{
		"success":    success,
		"character":  charName,
		"tool":       req.Tool,
		"ability":    abilityName,
		"roll":       finalRoll,
		"roll_type":  rollType,
		"modifier":   totalMod,
		"proficient": isProficient,
		"expertise":  hasExpertise,
		"total":      total,
		"dc":         req.DC,
		"outcome":    outcomeStr,
		"result":     fullResult,
		"rolls_detail": map[string]interface{}{
			"die1": roll1,
			"die2": roll2,
		},
	}
	if !isProficient {
		response["note"] = fmt.Sprintf("%s is not proficient with %s (no proficiency bonus added)", charName, req.Tool)
	}
	if toolAndSkill {
		response["tool_and_skill"] = fmt.Sprintf("%s is proficient with %s and in %s: Advantage (SRD 5.2.1)", charName, req.Tool, titleSkill(normSkill(req.Skill)))
	}
	if usedInspiration {
		response["used_inspiration"] = true
		response["inspiration_note"] = fmt.Sprintf("%s spent Heroic Inspiration: rerolled=%v (a failed roll is rolled again and the new roll is used)", charName, inspirationRerolled)
	}
	// Add Remarkable Athlete note
	if toolRemarkableAthleteBonus > 0 {
		response["remarkable_athlete"] = true
		response["remarkable_athlete_bonus"] = toolRemarkableAthleteBonus
		response["class_feature_note"] = fmt.Sprintf("%s adds +%d from Remarkable Athlete (half proficiency for physical checks)", charName, toolRemarkableAthleteBonus)
		// Update the note to reflect the bonus
		response["note"] = fmt.Sprintf("%s is not proficient with %s, but gains +%d from Remarkable Athlete", charName, req.Tool, toolRemarkableAthleteBonus)
	}
	// Add Jack of All Trades note
	if toolJackOfAllTradesBonus > 0 {
		response["jack_of_all_trades"] = true
		response["jack_of_all_trades_bonus"] = toolJackOfAllTradesBonus
		response["class_feature_note"] = fmt.Sprintf("%s adds +%d from Jack of All Trades (half proficiency for non-proficient checks)", charName, toolJackOfAllTradesBonus)
		// Update the note to reflect the bonus
		response["note"] = fmt.Sprintf("%s is not proficient with %s, but gains +%d from Jack of All Trades", charName, req.Tool, toolJackOfAllTradesBonus)
	}
	// Add Reliable Talent note
	if toolReliableTalentApplied {
		response["reliable_talent"] = true
		response["original_roll"] = toolOriginalRoll
		response["class_feature_note"] = fmt.Sprintf("%s's Reliable Talent: rolled %d, treated as 10", charName, toolOriginalRoll)
	}
	// Add Halfling Lucky note
	if toolHalflingLuckyUsed {
		response["halfling_lucky"] = true
		response["halfling_lucky_original"] = toolHalflingLuckyOriginal
		response["halfling_lucky_reroll"] = finalRoll
		response["racial_feature_note"] = fmt.Sprintf("🍀 %s's Halfling Lucky: rerolled nat 1 → %d", charName, finalRoll)
	}
	// Add Peerless Skill note
	if toolPeerlessSkillApplied {
		response["peerless_skill"] = true
		response["peerless_skill_roll"] = toolPeerlessSkillRoll
		response["peerless_skill_die"] = fmt.Sprintf("d%d", getBardicInspirationDie(level))
		response["bardic_inspiration_remaining"] = toolPeerlessSkillRemaining
		response["class_feature_note"] = fmt.Sprintf("🎭 %s uses Peerless Skill: rolled d%d = %d, added to check (%d Bardic Inspiration remaining)", charName, getBardicInspirationDie(level), toolPeerlessSkillRoll, toolPeerlessSkillRemaining)
	}
	// Add condition notes for disadvantage sources
	if poisonedDisadvantage {
		response["poisoned"] = true
		response["condition_note"] = fmt.Sprintf("%s has disadvantage on ability checks (poisoned)", charName)
	}
	if exhaustionDisadvantage {
		response["exhausted"] = true
		response["exhaustion_level"] = charExhaustion
		if poisonedDisadvantage {
			response["condition_note"] = fmt.Sprintf("%s has disadvantage on ability checks (poisoned, exhaustion level %d)", charName, charExhaustion)
		} else {
			response["condition_note"] = fmt.Sprintf("%s has disadvantage on ability checks (exhaustion level %d)", charName, charExhaustion)
		}
	}
	// Add Indomitable Might note
	if toolIndomitableMightApplied {
		response["indomitable_might"] = true
		response["indomitable_might_original_total"] = toolIndomitableMightOriginalTotal
		response["indomitable_might_str_score"] = toolIndomitableMightStrScore
		response["class_feature_note"] = fmt.Sprintf("💪 %s's Indomitable Might: total %d replaced with STR score %d", charName, toolIndomitableMightOriginalTotal, toolIndomitableMightStrScore)
	}
	json.NewEncoder(w).Encode(response)
}

// handleGMSavingThrow godoc
// @Summary Call for a saving throw
// @Description GM calls for a saving throw from a character. Server resolves mechanics with proficiency.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,ability=string,dc=int,advantage=bool,disadvantage=bool,description=string,from_magic=bool} true "Saving throw details"
// @Success 200 {object} map[string]interface{} "Saving throw result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /gm/saving-throw [post]
func handleGMSavingThrow(w http.ResponseWriter, r *http.Request) {
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
		CharacterID       int    `json:"character_id"`
		Ability           string `json:"ability"` // str, dex, con, int, wis, cha
		DC                int    `json:"dc"`      // Difficulty Class
		Advantage         bool   `json:"advantage"`
		Disadvantage      bool   `json:"disadvantage"`
		Description       string `json:"description"`          // Optional context (e.g., "Fireball", "Dragon's Breath")
		UseInspiration    bool   `json:"use_inspiration"`      // Spend Heroic Inspiration: reroll a failed d20
		FromMagic         bool   `json:"from_magic"`           // Gnomish Cunning (save vs magic)
		FromFiendOrUndead bool   `json:"from_fiend_or_undead"` // Holy Nimbus (advantage on saves vs fiend/undead spells)
		ChooseToFail      bool   `json:"choose_to_fail"`       // the player doesn't resist: the save fails without a roll (SRD 5.2.1)
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

	if req.Ability == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "ability required (str, dex, con, int, wis, cha)"})
		return
	}

	if req.DC == 0 {
		req.DC = 10 // Default DC
	}
	if req.ChooseToFail {
		var nm string
		db.QueryRow(`SELECT name FROM characters WHERE id = $1`, req.CharacterID).Scan(&nm)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "character": nm, "ability": req.Ability, "dc": req.DC, "outcome": "FAILURE",
			"chose_to_fail": true, "result": fmt.Sprintf("%s chooses not to resist: the %s saving throw fails without a roll.", nm, strings.ToUpper(req.Ability))})
		return
	}

	// Get character stats, class, and inspiration
	var charName, className string
	var str, dex, con, intl, wis, cha, level int
	var charLobbyID int
	var hasInspiration bool
	err = db.QueryRow(`
		SELECT name, str, dex, con, intl, wis, cha, level, lobby_id, class, COALESCE(inspiration, false)
		FROM characters WHERE id = $1
	`, req.CharacterID).Scan(&charName, &str, &dex, &con, &intl, &wis, &cha, &level, &charLobbyID, &className, &hasInspiration)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is in this campaign
	if charLobbyID != campaignID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_in_campaign"})
		return
	}

	// Get class saving throw proficiencies
	var classSaves string
	err = db.QueryRow(`SELECT saving_throws FROM classes WHERE slug = $1`, strings.ToLower(className)).Scan(&classSaves)
	if err != nil {
		classSaves = "" // Class not found, no proficiencies
	}

	// Parse the ability
	abilityUsed := strings.ToLower(req.Ability)
	var abilityMod int
	var abilityName string
	var abilityShort string

	switch abilityUsed {
	case "str", "strength":
		abilityMod = game.Modifier(str)
		abilityName = "Strength"
		abilityShort = "str"
	case "dex", "dexterity":
		abilityMod = game.Modifier(dex)
		abilityName = "Dexterity"
		abilityShort = "dex"
	case "con", "constitution":
		abilityMod = game.Modifier(con)
		abilityName = "Constitution"
		abilityShort = "con"
	case "int", "intelligence":
		abilityMod = game.Modifier(intl)
		abilityName = "Intelligence"
		abilityShort = "int"
	case "wis", "wisdom":
		abilityMod = game.Modifier(wis)
		abilityName = "Wisdom"
		abilityShort = "wis"
	case "cha", "charisma":
		abilityMod = game.Modifier(cha)
		abilityName = "Charisma"
		abilityShort = "cha"
	default:
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid ability - use str, dex, con, int, wis, or cha"})
		return
	}

	// Check if proficient in this saving throw
	proficient := false
	if classSaves != "" {
		for _, save := range strings.Split(classSaves, ",") {
			if strings.TrimSpace(strings.ToLower(save)) == abilityShort {
				proficient = true
				break
			}
		}
	}

	// Disciplined Survivor (Monk level 14: every saving throw) and Slippery Mind (Rogue level 15: Wisdom and Charisma)
	saveFeature := ""
	if !proficient {
		if ok, feature := saveProficiency(req.CharacterID, className, level, abilityShort); ok {
			proficient = true
			saveFeature = feature
		}
	}

	// Calculate total modifier
	totalMod := abilityMod
	if proficient {
		totalMod += game.ProficiencyBonus(level)
	}
	totalMod = wildShapeHigher(req.CharacterID, "save:"+abilityShort, totalMod) // Wild Shape: the beast's save modifier when it's higher
	totalMod -= game.ExhaustionD20Penalty(exhaustionLevelOf(req.CharacterID))   // SRD 5.2.1: -2 per Exhaustion level

	// Paladin's Aura of Protection
	// At level 6+, allies within 10ft (30ft at level 18) add Paladin's CHA mod to saves
	// The Paladin also benefits from their own aura
	auraBonus := 0
	auraPaladinName := ""

	// First check if THIS character is a Paladin level 6+ (they benefit from own aura)
	paladinLevelSelf := 0
	var selfClassLevelsJSON []byte
	_ = db.QueryRow(`SELECT COALESCE(class_levels, '{}')::jsonb FROM characters WHERE id = $1`, req.CharacterID).Scan(&selfClassLevelsJSON)
	var selfClassLevels map[string]int
	if err := json.Unmarshal(selfClassLevelsJSON, &selfClassLevels); err == nil && len(selfClassLevels) > 0 {
		for cn, lvl := range selfClassLevels {
			if strings.ToLower(cn) == "paladin" {
				paladinLevelSelf = lvl
				break
			}
		}
	} else if strings.ToLower(className) == "paladin" {
		paladinLevelSelf = level
	}

	if paladinLevelSelf >= 6 && !isIncapacitated(req.CharacterID) {
		selfChaBonus := game.Modifier(cha)
		if selfChaBonus < 1 {
			selfChaBonus = 1
		}
		auraBonus = selfChaBonus
		auraPaladinName = charName + " (self)"
	}

	// Then check for other Paladins in the party
	otherAura, otherPaladin := getPaladinAuraBonus(campaignID, req.CharacterID)
	if otherAura > auraBonus {
		auraBonus = otherAura
		auraPaladinName = otherPaladin
	}

	if auraBonus > 0 {
		totalMod += auraBonus
	}
	totalMod += magicSaveBonus(req.CharacterID) // Ring/Cloak of Protection, Stone of Good Luck, Staff of Power (magic_items_2024.go)
	totalMod += buffSaveBonus(req.CharacterID)  // Warding Bond +1 (buff_spells_2024.go)

	// CHECK: Auto-fail conditions (paralyzed, stunned, unconscious auto-fail STR/DEX saves)
	if autoFailsSave(req.CharacterID, abilityShort) {
		failReason := "condition"
		conditions := getCharConditions(req.CharacterID)
		for _, c := range conditions {
			switch strings.ToLower(c) {
			case "paralyzed", "petrified", "stunned", "unconscious":
				failReason = c
				break
			}
		}

		fullResult := fmt.Sprintf("%s saving throw: AUTO-FAIL (character is %s)", abilityName, failReason)
		desc := fmt.Sprintf("%s: %s saving throw (DC %d) - auto-fail (%s)", charName, abilityName, req.DC, failReason)
		if req.Description != "" {
			desc = fmt.Sprintf("%s: %s - %s saving throw (DC %d) - auto-fail (%s)", charName, req.Description, abilityName, req.DC, failReason)
		}

		_, _ = db.Exec(`
			INSERT INTO actions (lobby_id, character_id, action_type, description, result)
			VALUES ($1, $2, 'saving_throw', $3, $4)
		`, campaignID, req.CharacterID, desc, fullResult)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":          false,
			"character":        charName,
			"ability":          abilityName,
			"auto_fail":        true,
			"auto_fail_reason": failReason,
			"dc":               req.DC,
			"outcome":          "AUTO-FAIL",
			"result":           fullResult,
			"condition_note":   fmt.Sprintf("%s is %s and automatically fails %s saving throws", charName, failReason, abilityName),
		})
		return
	}

	// CHECK: Condition-based disadvantage (exhaustion 3+, restrained for DEX)
	conditionDisadvantage := getSaveDisadvantage(req.CharacterID, abilityShort)
	if conditionDisadvantage && !req.Advantage {
		req.Disadvantage = true
	}
	if spellSaveAdvantage(campaignID, req.CharacterID, abilityShort) {
		req.Advantage = true // Haste: Advantage on Dexterity saving throws
	}

	// Danger Sense (Barbarian level 2+)
	// Advantage on DEX saves against effects you can see (traps, spells)
	// Must not be blinded, deafened, or incapacitated
	dangerSenseActive := false
	if abilityShort == "dex" && strings.ToLower(className) == "barbarian" && level >= 2 {
		// Check conditions that disable Danger Sense
		charConditions := getCharConditions(req.CharacterID)
		canUseDangerSense := true
		for _, cond := range charConditions {
			condLower := strings.ToLower(cond)
			if condLower == "blinded" || condLower == "deafened" || isIncapacitated(req.CharacterID) {
				canUseDangerSense = false
				break
			}
		}
		if canUseDangerSense {
			req.Advantage = true
			dangerSenseActive = true
		}
	}

	// Gnomish Cunning
	// Advantage on Intelligence, Wisdom and Charisma saving throws
	gnomeCunningActive := false
	if req.FromMagic || strings.Contains(strings.ToLower(req.Description), "spell") { // F130: Mantle of Spell Resistance, Robe of the Archmagi...
		if p := spellSaveProtection(campaignID, req.CharacterID); p != "" {
			req.Advantage = true
			req.Description = strings.TrimSpace(req.Description + " [" + p + ": Advantage against spells]")
		}
	}
	if checkGnomeCunning(req.CharacterID, abilityShort, req.FromMagic) {
		req.Advantage = true
		gnomeCunningActive = true
	}

	// Fey Ancestry
	// Advantage on saving throws against being charmed
	feyAncestryActive := false
	if checkFeyAncestryCharm(req.CharacterID, req.Description) {
		req.Advantage = true
		feyAncestryActive = true
	}

	// Dwarven Resilience
	// Advantage on saving throws against poison
	dwarvenResilienceActive := false
	if checkDwarvenResilience(req.CharacterID, req.Description) {
		req.Advantage = true
		dwarvenResilienceActive = true
	}
	if ok, why := itemPoisonSaveAdvantage(req.CharacterID, req.Description); ok { // Necklace of Adaptation, Belt of Dwarvenkind (F204)
		req.Advantage = true
		req.Description += " [" + why + ": Advantage against being Poisoned]"
	}
	if periaptOfHealthPoison(req.CharacterID) && strings.Contains(strings.ToLower(req.Description), "poison") { // F154
		req.Advantage = true
		req.Description += " [Periapt of Health: Advantage against being Poisoned]"
	}

	// Halfling Brave
	// Advantage on saving throws against being frightened
	halflingBraveActive := false
	if checkHalflingBrave(req.CharacterID, req.Description) {
		req.Advantage = true
		halflingBraveActive = true
	}

	// Hunter Ranger Steel Will
	// Advantage on saving throws against being frightened
	steelWillActive := false

	// Holy Nimbus
	// Advantage on saving throws against spells cast by fiends or undead
	holyNimbusActive := false
	if req.FromFiendOrUndead && hasHolyNimbusActive(req.CharacterID) {
		req.Advantage = true
		holyNimbusActive = true
	}

	// Handle Heroic Inspiration (2024): reroll after a failure
	usedInspiration := false
	if req.UseInspiration {
		if hasInspiration {
			db.Exec(`UPDATE characters SET inspiration = false WHERE id = $1`, req.CharacterID)
			usedInspiration = true
		} else {
			// Character doesn't have Heroic Inspiration to spend
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no_inspiration",
				"message": fmt.Sprintf("%s doesn't have Heroic Inspiration to spend", charName),
			})
			return
		}
	}

	// Roll the die
	var roll1, roll2, finalRoll int
	rollType := "normal"

	if req.Advantage && !req.Disadvantage {
		finalRoll, roll1, roll2 = game.RollWithAdvantage()
		rollType = "advantage"
		if usedInspiration {
			rollType = "advantage (inspiration)"
		} else if dangerSenseActive {
			rollType = "advantage (Danger Sense)"
		} else if gnomeCunningActive {
			rollType = "advantage (Gnomish Cunning)"
		} else if feyAncestryActive {
			rollType = "advantage (Fey Ancestry)"
		} else if dwarvenResilienceActive {
			rollType = "advantage (Dwarven Resilience)"
		} else if halflingBraveActive {
			rollType = "advantage (Halfling Brave)"
		} else if steelWillActive {
			rollType = "advantage (Steel Will)"
		} else if holyNimbusActive {
			rollType = "advantage (☀️ Holy Nimbus)"
		}
	} else if req.Disadvantage && !req.Advantage {
		finalRoll, roll1, roll2 = game.RollWithDisadvantage()
		rollType = "disadvantage"
	} else if req.Advantage && req.Disadvantage {
		// Advantage and disadvantage cancel out
		finalRoll = game.RollDie(20)
		roll1 = finalRoll
		roll2 = 0
		rollType = "normal (advantage and disadvantage cancel)"
	} else {
		finalRoll = game.RollDie(20)
		roll1 = finalRoll
		roll2 = 0
	}

	// Halfling Lucky
	// When you roll a 1 on the d20, you can reroll and must use the new roll
	saveHalflingLuckyUsed := false
	saveHalflingLuckyOriginal := 0
	if finalRoll == 1 {
		newRoll, rerolled, origRoll := applyHalflingLucky(finalRoll, req.CharacterID)
		if rerolled {
			saveHalflingLuckyUsed = true
			saveHalflingLuckyOriginal = origRoll
			finalRoll = newRoll
			roll1 = newRoll
		}
	}

	inspirationRerolled := false
	spellAdj, spellNote := spellRollAdjust(campaignID, req.CharacterID, "save:"+abilityShort) // Bless, Bane, Slow
	totalMod += spellAdj
	total := finalRoll + totalMod
	if usedInspiration && req.DC > 0 && total < req.DC { // 2024 Heroic Inspiration: reroll the die after a failure and use the new roll
		oldRoll := finalRoll
		finalRoll = game.RollDie(20)
		roll1, roll2 = finalRoll, 0
		total = total - oldRoll + finalRoll
		inspirationRerolled = true
	}
	_ = inspirationRerolled
	total, saveMightNote := indomitableMight(req.CharacterID, abilityShort, total) // F102
	success := total >= req.DC

	// Build result description
	resultStr := fmt.Sprintf("d20(%d)", finalRoll)
	if saveHalflingLuckyUsed {
		resultStr = fmt.Sprintf("d20(%d→%d Lucky)", saveHalflingLuckyOriginal, finalRoll)
	}
	if rollType == "advantage" {
		resultStr = fmt.Sprintf("d20(%d,%d→%d)", roll1, roll2, finalRoll)
	} else if rollType == "disadvantage" {
		if saveHalflingLuckyUsed {
			resultStr = fmt.Sprintf("d20(%d,%d→%d→%d Lucky)", roll1, roll2, saveHalflingLuckyOriginal, finalRoll)
		} else {
			resultStr = fmt.Sprintf("d20(%d,%d→%d)", roll1, roll2, finalRoll)
		}
	}

	modStr := ""
	if totalMod >= 0 {
		modStr = fmt.Sprintf("+%d", totalMod)
	} else {
		modStr = fmt.Sprintf("%d", totalMod)
	}

	profStr := ""
	if proficient {
		profStr = " (proficient)"
	}

	outcomeStr := "FAILURE"
	if success {
		outcomeStr = "SUCCESS"
	}

	// A natural 20 or 1 means nothing special on a saving throw (only attack rolls and death saves use them).

	fullResult := fmt.Sprintf("%s saving throw%s: %s%s = %d vs DC %d → %s%s",
		abilityName, profStr, resultStr, modStr, total, req.DC, outcomeStr, spellNote)

	// Record the saving throw
	desc := fmt.Sprintf("%s: %s saving throw (DC %d)", charName, abilityName, req.DC)
	if req.Description != "" {
		desc = fmt.Sprintf("%s: %s - %s saving throw (DC %d)", charName, req.Description, abilityName, req.DC)
	}

	_, _ = db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, 'saving_throw', $3, $4)
	`, campaignID, req.CharacterID, desc, fullResult)

	response := map[string]interface{}{
		"success":     success,
		"character":   charName,
		"ability":     abilityName,
		"proficient":  proficient,
		"roll":        finalRoll,
		"roll_type":   rollType,
		"ability_mod": abilityMod,
		"total_mod":   totalMod,
		"total":       total,
		"dc":          req.DC,
		"outcome":     outcomeStr,
		"result":      fullResult,
		"rolls_detail": map[string]interface{}{
			"die1": roll1,
			"die2": roll2,
		},
	}
	if saveMightNote != "" {
		response["indomitable_might"] = strings.TrimSpace(saveMightNote) // F102
	}
	if usedInspiration {
		response["used_inspiration"] = true
		response["inspiration_note"] = fmt.Sprintf("%s spent inspiration for advantage on this save", charName)
	}
	// Add Halfling Lucky note
	if saveHalflingLuckyUsed {
		response["halfling_lucky"] = true
		response["halfling_lucky_original"] = saveHalflingLuckyOriginal
		response["halfling_lucky_reroll"] = finalRoll
		response["racial_feature_note"] = fmt.Sprintf("🍀 %s's Halfling Lucky: rerolled nat 1 → %d", charName, finalRoll)
	}
	// Add Danger Sense note
	if dangerSenseActive {
		response["danger_sense"] = true
		response["class_feature_note"] = fmt.Sprintf("🎯 %s's Danger Sense grants advantage on DEX saves against effects they can see", charName)
	}
	// Add Gnomish Cunning note
	if gnomeCunningActive {
		response["gnome_cunning"] = true
		response["species_trait_note"] = fmt.Sprintf("🧠 %s's Gnomish Cunning gives Advantage on %s saves", charName, abilityName)
	}
	// Add Fey Ancestry note
	if feyAncestryActive {
		response["fey_ancestry"] = true
		response["racial_feature_note"] = fmt.Sprintf("✨ %s's Fey Ancestry grants advantage on saves against charm effects", charName)
	}
	// Add Dwarven Resilience note
	if dwarvenResilienceActive {
		response["dwarven_resilience"] = true
		response["racial_feature_note"] = fmt.Sprintf("⛏️ %s's Dwarven Resilience grants advantage on saves against poison", charName)
	}
	// Add Halfling Brave note
	if halflingBraveActive {
		response["halfling_brave"] = true
		response["racial_feature_note"] = fmt.Sprintf("💪 %s's Halfling Brave grants advantage on saves against frightened", charName)
	}
	// Disciplined Survivor / Slippery Mind note
	if saveFeature != "" {
		response["save_proficiency_from"] = saveFeature
		response["class_feature_note"] = fmt.Sprintf("%s's %s gives proficiency in this saving throw", charName, saveFeature)
	}
	// Add Paladin's Aura of Protection note
	if auraBonus > 0 {
		response["aura_of_protection"] = true
		response["aura_bonus"] = auraBonus
		response["aura_source"] = auraPaladinName
		response["paladin_aura_note"] = fmt.Sprintf("🛡️ Aura of Protection (+%d from %s)", auraBonus, auraPaladinName)
	}
	json.NewEncoder(w).Encode(response)
}

// handleGMContestedCheck godoc
// @Summary Resolve a contested check
// @Description GM calls for an opposed check between two creatures (e.g., grapple, shove). Both roll, highest wins.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{initiator_id=int,defender_id=int,initiator_skill=string,defender_skill=string,description=string} true "Contested check details"
// @Success 200 {object} map[string]interface{} "Contested check result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /gm/contested-check [post]
func handleGMContestedCheck(w http.ResponseWriter, r *http.Request) {
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
		InitiatorID           int    `json:"initiator_id"`    // Character ID of initiator
		DefenderID            int    `json:"defender_id"`     // Character ID of defender
		InitiatorSkill        string `json:"initiator_skill"` // Skill or ability: athletics, acrobatics, str, dex, etc.
		DefenderSkill         string `json:"defender_skill"`  // Skill or ability (can be "athletics_or_acrobatics" for choice)
		InitiatorAdvantage    bool   `json:"initiator_advantage"`
		InitiatorDisadvantage bool   `json:"initiator_disadvantage"`
		DefenderAdvantage     bool   `json:"defender_advantage"`
		DefenderDisadvantage  bool   `json:"defender_disadvantage"`
		Description           string `json:"description"` // e.g., "grapple attempt", "shove"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
		return
	}

	if req.InitiatorID == 0 || req.DefenderID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "initiator_id and defender_id required"})
		return
	}

	if req.InitiatorSkill == "" {
		req.InitiatorSkill = "athletics" // Default for most contests
	}
	if req.DefenderSkill == "" {
		req.DefenderSkill = "athletics" // Default
	}

	// Helper to get character stats
	getCharStats := func(charID int) (name string, str, dex, con, intl, wis, cha, level int, lobbyID int, err error) {
		err = db.QueryRow(`
			SELECT name, str, dex, con, intl, wis, cha, level, lobby_id
			FROM characters WHERE id = $1
		`, charID).Scan(&name, &str, &dex, &con, &intl, &wis, &cha, &level, &lobbyID)
		return
	}

	// Get both characters
	initName, initStr, initDex, initCon, initInt, initWis, initCha, initLevel, initLobby, err := getCharStats(req.InitiatorID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "initiator_not_found"})
		return
	}

	defName, defStr, defDex, defCon, defInt, defWis, defCha, defLevel, defLobby, err := getCharStats(req.DefenderID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "defender_not_found"})
		return
	}

	// Verify both are in this campaign
	if initLobby != campaignID || defLobby != campaignID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "characters_not_in_campaign"})
		return
	}

	// Helper to calculate skill/ability modifier: a skill adds the Proficiency Bonus only when the character is proficient
	// (Expertise doubles it, Jack of All Trades gives half), through checkBonus.
	calcMod := func(charID int, skill string, str, dex, con, intl, wis, cha, level int) (mod int, skillName string) {
		skill = strings.ToLower(skill)
		if k := strings.ReplaceAll(skill, " ", "_"); skillAbilityMap[k] != "" {
			var class string
			db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&class)
			score := map[string]int{"str": str, "dex": dex, "con": con, "int": intl, "wis": wis, "cha": cha}[skillAbilityMap[k]]
			m, prof, _, _ := checkBonus(charID, class, level, strings.ReplaceAll(k, "_", " "), score)
			return m + prof, titleSkill(strings.ReplaceAll(k, "_", " "))
		}
		// Skills map to abilities
		switch skill {
		case "athletics":
			return game.Modifier(str) + game.ProficiencyBonus(level), "Athletics"
		case "acrobatics":
			return game.Modifier(dex) + game.ProficiencyBonus(level), "Acrobatics"
		case "sleight_of_hand", "sleightofhand":
			return game.Modifier(dex) + game.ProficiencyBonus(level), "Sleight of Hand"
		case "stealth":
			return game.Modifier(dex) + game.ProficiencyBonus(level), "Stealth"
		case "arcana":
			return game.Modifier(intl) + game.ProficiencyBonus(level), "Arcana"
		case "history":
			return game.Modifier(intl) + game.ProficiencyBonus(level), "History"
		case "investigation":
			return game.Modifier(intl) + game.ProficiencyBonus(level), "Investigation"
		case "nature":
			return game.Modifier(intl) + game.ProficiencyBonus(level), "Nature"
		case "religion":
			return game.Modifier(intl) + game.ProficiencyBonus(level), "Religion"
		case "animal_handling", "animalhandling":
			return game.Modifier(wis) + game.ProficiencyBonus(level), "Animal Handling"
		case "insight":
			return game.Modifier(wis) + game.ProficiencyBonus(level), "Insight"
		case "medicine":
			return game.Modifier(wis) + game.ProficiencyBonus(level), "Medicine"
		case "perception":
			return game.Modifier(wis) + game.ProficiencyBonus(level), "Perception"
		case "survival":
			return game.Modifier(wis) + game.ProficiencyBonus(level), "Survival"
		case "deception":
			return game.Modifier(cha) + game.ProficiencyBonus(level), "Deception"
		case "intimidation":
			return game.Modifier(cha) + game.ProficiencyBonus(level), "Intimidation"
		case "performance":
			return game.Modifier(cha) + game.ProficiencyBonus(level), "Performance"
		case "persuasion":
			return game.Modifier(cha) + game.ProficiencyBonus(level), "Persuasion"
		// Raw abilities (no proficiency)
		case "str", "strength":
			return game.Modifier(str), "Strength"
		case "dex", "dexterity":
			return game.Modifier(dex), "Dexterity"
		case "con", "constitution":
			return game.Modifier(con), "Constitution"
		case "int", "intelligence":
			return game.Modifier(intl), "Intelligence"
		case "wis", "wisdom":
			return game.Modifier(wis), "Wisdom"
		case "cha", "charisma":
			return game.Modifier(cha), "Charisma"
		default:
			return 0, skill // Unknown, return as-is
		}
	}

	// Handle "X_or_Y" format for defender (e.g., "athletics_or_acrobatics")
	defSkill := req.DefenderSkill
	if strings.Contains(defSkill, "_or_") {
		parts := strings.Split(defSkill, "_or_")
		// Calculate both and use the higher
		mod1, name1 := calcMod(req.DefenderID, parts[0], defStr, defDex, defCon, defInt, defWis, defCha, defLevel)
		mod2, name2 := calcMod(req.DefenderID, parts[1], defStr, defDex, defCon, defInt, defWis, defCha, defLevel)
		if mod1 >= mod2 {
			defSkill = parts[0]
		} else {
			defSkill = parts[1]
			_ = name1 // Suppress unused warning
		}
		_ = name2
	}

	// Calculate modifiers
	initMod, initSkillName := calcMod(req.InitiatorID, req.InitiatorSkill, initStr, initDex, initCon, initInt, initWis, initCha, initLevel)
	defMod, defSkillName := calcMod(req.DefenderID, defSkill, defStr, defDex, defCon, defInt, defWis, defCha, defLevel)

	// Goliath Large Form: Advantage on Strength checks (Athletics included)
	strOf := func(skill string) string {
		if a := skillAbilityMap[strings.ReplaceAll(strings.ToLower(skill), " ", "_")]; a != "" {
			return a
		}
		return skill
	}
	if largeFormAdvantage(req.InitiatorID, strOf(req.InitiatorSkill)) {
		req.InitiatorAdvantage = true
	}
	if largeFormAdvantage(req.DefenderID, strOf(defSkill)) {
		req.DefenderAdvantage = true
	}

	// Roll for initiator
	var initRoll1, initRoll2, initFinalRoll int
	initRollType := "normal"
	if req.InitiatorAdvantage && !req.InitiatorDisadvantage {
		initFinalRoll, initRoll1, initRoll2 = game.RollWithAdvantage()
		initRollType = "advantage"
	} else if req.InitiatorDisadvantage && !req.InitiatorAdvantage {
		initFinalRoll, initRoll1, initRoll2 = game.RollWithDisadvantage()
		initRollType = "disadvantage"
	} else {
		initFinalRoll = game.RollDie(20)
		initRoll1 = initFinalRoll
	}
	if req.InitiatorID > 0 {
		if r, n := buffCheckRoll(req.InitiatorID, skillAbility(req.InitiatorSkill), initFinalRoll); n != "" { // Foresight, Enhance Ability
			initFinalRoll, initRollType = r, "advantage"+n
		}
	}
	initTotal := initFinalRoll + initMod
	if req.InitiatorID > 0 { // Guidance
		g, _ := guidanceBonus(campaignID, req.InitiatorID, req.InitiatorSkill)
		initTotal += g
	}

	// Roll for defender
	var defRoll1, defRoll2, defFinalRoll int
	defRollType := "normal"
	if req.DefenderAdvantage && !req.DefenderDisadvantage {
		defFinalRoll, defRoll1, defRoll2 = game.RollWithAdvantage()
		defRollType = "advantage"
	} else if req.DefenderDisadvantage && !req.DefenderAdvantage {
		defFinalRoll, defRoll1, defRoll2 = game.RollWithDisadvantage()
		defRollType = "disadvantage"
	} else {
		defFinalRoll = game.RollDie(20)
		defRoll1 = defFinalRoll
	}
	if req.DefenderID > 0 {
		if r, n := buffCheckRoll(req.DefenderID, skillAbility(defSkill), defFinalRoll); n != "" { // Foresight, Enhance Ability
			defFinalRoll, defRollType = r, "advantage"+n
		}
	}
	defTotal := defFinalRoll + defMod
	if req.DefenderID > 0 { // Guidance
		g, _ := guidanceBonus(campaignID, req.DefenderID, defSkill)
		defTotal += g
	}

	// Determine winner (ties go to defender in 5e grapples)
	winner := "defender"
	if initTotal > defTotal {
		winner = "initiator"
	}
	margin := initTotal - defTotal
	if margin < 0 {
		margin = -margin
	}

	// Build result strings
	initResultStr := fmt.Sprintf("d20(%d)", initFinalRoll)
	if initRollType != "normal" {
		initResultStr = fmt.Sprintf("d20(%d,%d→%d)", initRoll1, initRoll2, initFinalRoll)
	}

	defResultStr := fmt.Sprintf("d20(%d)", defFinalRoll)
	if defRollType != "normal" {
		defResultStr = fmt.Sprintf("d20(%d,%d→%d)", defRoll1, defRoll2, defFinalRoll)
	}

	desc := fmt.Sprintf("%s vs %s", req.InitiatorSkill, defSkill)
	if req.Description != "" {
		desc = req.Description
	}

	fullResult := fmt.Sprintf("Contested %s: %s (%s %+d = %d) vs %s (%s %+d = %d) → %s wins by %d",
		desc, initName, initResultStr, initMod, initTotal,
		defName, defResultStr, defMod, defTotal,
		winner, margin)

	// Record the contested check
	_, _ = db.Exec(`
		INSERT INTO actions (lobby_id, character_id, action_type, description, result)
		VALUES ($1, $2, 'contested_check', $3, $4)
	`, campaignID, req.InitiatorID, desc, fullResult)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"winner":      winner,
		"winner_name": map[string]string{"initiator": initName, "defender": defName}[winner],
		"margin":      margin,
		"result":      fullResult,
		"initiator": map[string]interface{}{
			"name":      initName,
			"skill":     initSkillName,
			"roll":      initFinalRoll,
			"roll_type": initRollType,
			"modifier":  initMod,
			"total":     initTotal,
			"rolls_detail": map[string]int{
				"die1": initRoll1,
				"die2": initRoll2,
			},
		},
		"defender": map[string]interface{}{
			"name":      defName,
			"skill":     defSkillName,
			"roll":      defFinalRoll,
			"roll_type": defRollType,
			"modifier":  defMod,
			"total":     defTotal,
			"rolls_detail": map[string]int{
				"die1": defRoll1,
				"die2": defRoll2,
			},
		},
	})
}
