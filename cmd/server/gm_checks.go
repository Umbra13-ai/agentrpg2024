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
// @Param Authorization header string true "Bearer <token>"
// @Body Skill check parameters
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
	campaignID, ok := gmReadAs(w, r, agentID, &req, targetsIn(&req.CharacterID))
	if !ok {
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
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is in this campaign

	// Check for auto-fail conditions (deafened/blinded)
	skillUsedForCheck := strings.ToLower(strings.ReplaceAll(req.Skill, " ", "_"))
	if req.RequiresHearing && hasCondition(req.CharacterID, "deafened") {
		desc := fmt.Sprintf("%s: %s check (DC %d) - auto-fail (deafened)", charName, req.Skill, req.DC)
		if req.Description != "" {
			desc = fmt.Sprintf("%s: %s - %s check (DC %d) - auto-fail (deafened)", charName, req.Description, req.Skill, req.DC)
		}
		writeAction(db, campaignID, req.CharacterID, "skill_check", desc, "AUTO-FAIL (deafened)")

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
		writeAction(db, campaignID, req.CharacterID, "skill_check", desc, "AUTO-FAIL (blinded)")

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
	if sk, ok := game.LookupSkill(req.Skill); ok { // any spelling of the skill (game.Skills)
		skillUsed = game.SkillSlug(sk.Name)
		abilityUsed, primalKnowledge = primalKnowledgeAbility(req.CharacterID, skillUsed, req.Ability, sk.Ability) // F101
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

	// The skill's bonus has one owner, checkBonus (actions_checks_2024.go): proficiency or Expertise, Jack of All Trades, the
	// Thaumaturge or Magician bonus, items and Wild Shape. A check without a skill: Jack of All Trades and items.
	totalMod := abilityMod
	isProficient, hasExpertise := false, false
	remarkableAthleteBonus, jackOfAllTradesBonus := 0, 0 // 2024 Remarkable Athlete is Advantage (below), not a bonus
	if skillUsed != "" {
		score := map[string]int{"Strength": str, "Dexterity": dex, "Constitution": con, "Intelligence": intl, "Wisdom": wis, "Charisma": cha}[abilityName]
		m, p, note, _ := checkBonus(req.CharacterID, class, level, skillUsed, score)
		totalMod += m - abilityMod + p
		isProficient, hasExpertise = note == " (proficient)" || note == " (expertise)", note == " (expertise)"
		if note == " (Jack of All Trades)" {
			jackOfAllTradesBonus = p
		}
	} else {
		if game.HasClassFeature(class, level, "jack_of_all_trades") {
			jackOfAllTradesBonus = game.ProficiencyBonus(level) / 2
			totalMod += jackOfAllTradesBonus
		}
		totalMod += magicCheckBonus(req.CharacterID) // Stone of Good Luck
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

	// What gives the check Advantage or Disadvantage has one owner (check_2024.go): conditions, riders, Sunlight Sensitivity, the character's
	// class, subclass, species, items, spells, armor, Help and travel pace; O-11: the reasons let the reply say when they cancel
	why := checkCtx{lobby: campaignID, who: req.CharacterID, ability: abilityUsed, skill: skillUsed, targetID: req.TargetID,
		gmAdvantage: req.Advantage, gmDisadvantage: req.Disadvantage, halfSpeedMovement: req.HalfSpeedMovement}.sources()
	req.Advantage, req.Disadvantage = why.Advantage(), why.Disadvantage()
	totalMod -= why.Penalty // Exhaustion (-2 per level) and a die subtracted from the check (check_2024.go)
	advWhy, disWhy := why.Adv, why.Dis
	charmedAdvantage := why.hasAdv("Charmed target")
	supremeSneakAdvantage := why.hasAdv("Supreme Sneak")
	largeForm := why.hasAdv("Large Form")
	poisonedDisadvantage := why.hasDis("Poisoned")
	exhaustionDisadvantage := false // 2024: Exhaustion is a -2 per level penalty on the D20 Test (above), not Disadvantage
	var charExhaustion int
	buffCheckWhy := why.Notes

	// Roll the die (the one check d20, check_2024.go)
	finalRoll, roll1, roll2, rollKind := rollCheckD20(req.Advantage, req.Disadvantage)
	finalRoll, floorNote := why.floored(finalRoll) // Glibness (check_2024.go)
	buffCheckWhy += floorNote
	rollType := rollKind
	switch {
	case rollKind == "advantage":
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
	case rollKind == "disadvantage":
		// the reasons, in words: Poisoned and armor as before, every other source the owner named
		reasons := []string{}
		for _, d := range disWhy {
			switch d {
			case "Poisoned":
				reasons = append(reasons, "poisoned")
			case "armor without proficiency":
				reasons = append(reasons, "non-proficient armor")
			default:
				reasons = append(reasons, strings.ToLower(d))
			}
		}
		if len(reasons) > 0 {
			rollType = "disadvantage (" + strings.Join(reasons, ", ") + ")"
		}
	case req.Advantage && req.Disadvantage: // O-11: say why there is one d20
		rollType = "normal (Advantage from " + strings.Join(advWhy, ", ") + " and Disadvantage from " + strings.Join(disWhy, ", ") + " cancel)"
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

	writeAction(db, campaignID, req.CharacterID, "skill_check", desc, fullResult)

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
// @Body Tool check details
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
	campaignID, ok := gmReadAs(w, r, agentID, &req, targetsIn(&req.CharacterID))
	if !ok {
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
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	// Verify character is in this campaign

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
	isProficient := game.HasToolProficiency(ownedTools, req.Tool)
	// SRD 5.2.1: a skill that's also used with the check. Proficiency in it adds the Proficiency Bonus (Expertise doubles it), and
	// proficiency in both the tool and the skill gives Advantage.
	skillProficient, hasExpertise := false, false
	if req.Skill != "" {
		skillProficient, hasExpertise = characterSkillProficiency(req.CharacterID, req.Skill)
	}
	toolAndSkill := isProficient && skillProficient // the owner gives the Advantage (check_2024.go)
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

	// What gives the check Advantage or Disadvantage has one owner (check_2024.go), the same list a skill check has; Help names the tool
	why := checkCtx{lobby: campaignID, who: req.CharacterID, ability: abilityUsed, skill: req.Skill, help: req.Tool,
		gmAdvantage: req.Advantage, gmDisadvantage: req.Disadvantage, toolAndSkill: toolAndSkill}.sources()
	req.Advantage, req.Disadvantage = why.Advantage(), why.Disadvantage()
	totalMod -= why.Penalty // Exhaustion (-2 per level) and a die subtracted from the check (check_2024.go)
	poisonedDisadvantage := why.hasDis("Poisoned")
	exhaustionDisadvantage := false // 2024: Exhaustion is a -2 per level penalty on the D20 Test (above), not Disadvantage
	var charExhaustion int

	// Roll the die (the one check d20, check_2024.go)
	finalRoll, roll1, roll2, rollKind := rollCheckD20(req.Advantage, req.Disadvantage)
	finalRoll, toolFloorNote := why.floored(finalRoll) // Glibness (check_2024.go)
	rollType := rollKind
	if rollKind == "advantage" && usedInspiration {
		rollType = "advantage (inspiration)"
	}
	if rollKind == "disadvantage" {
		toolReasons := []string{}
		for _, d := range why.Dis {
			switch d {
			case "Poisoned":
				toolReasons = append(toolReasons, "poisoned")
			case "armor without proficiency":
				toolReasons = append(toolReasons, "non-proficient armor")
			default:
				toolReasons = append(toolReasons, strings.ToLower(d))
			}
		}
		if len(toolReasons) > 0 {
			rollType = "disadvantage (" + strings.Join(toolReasons, ", ") + ")"
		}
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

	resultStr += toolFloorNote

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

	writeAction(db, campaignID, req.CharacterID, "tool_check", desc, fullResult)

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
		response["tool_and_skill"] = fmt.Sprintf("%s is proficient with %s and in %s: Advantage (SRD 5.2.1)", charName, req.Tool, titleSkill(game.SkillName(req.Skill)))
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
// @Body Saving throw details
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

	var req struct {
		CharacterID       int    `json:"character_id"`
		Ability           string `json:"ability"` // str, dex, con, int, wis, cha
		DC                int    `json:"dc"`      // Difficulty Class
		Advantage         bool   `json:"advantage"`
		Disadvantage      bool   `json:"disadvantage"`
		Description       string `json:"description"`          // Optional context (e.g., "Fireball", "Dragon's Breath")
		UseInspiration    bool   `json:"use_inspiration"`      // Spend Heroic Inspiration: reroll a failed d20
		FromMagic         bool   `json:"from_magic"`           // a save against a spell: items that give Advantage against spells
		FromFiendOrUndead bool   `json:"from_fiend_or_undead"` // Holy Nimbus: Advantage on a save a Fiend or an Undead forces
		ChooseToFail      bool   `json:"choose_to_fail"`       // the player doesn't resist: the save fails without a roll (SRD 5.2.1)
	}
	campaignID, ok := gmReadAs(w, r, agentID, &req, targetsIn(&req.CharacterID))
	if !ok {
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

	var charName string
	var hasInspiration bool
	if err := db.QueryRow(`SELECT name, COALESCE(inspiration, false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&charName, &hasInspiration); err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}
	abilityShort, okAbility := game.AbilityKey(req.Ability)
	if !okAbility {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid ability - use str, dex, con, int, wis, or cha"})
		return
	}
	abilityName := game.AbilityName(abilityShort)
	if req.UseInspiration && !hasInspiration {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "no_inspiration",
			"message": fmt.Sprintf("%s doesn't have Heroic Inspiration to spend", charName),
		})
		return
	}

	// the one saving throw function (save_2024.go): every Advantage, Disadvantage, automatic failure and bonus the character's
	// state gives, with the GM's ruling and the cause the GM describes (a spell, a Fiend or an Undead, poison, charm, fear)
	sc := saveCtx{lobby: campaignID, saver: req.CharacterID, ability: abilityShort, dc: req.DC, desc: req.Description,
		spell: req.FromMagic || strings.Contains(strings.ToLower(req.Description), "spell"), fiendOrUndead: req.FromFiendOrUndead,
		inspiration: req.UseInspiration}
	if req.Advantage {
		sc.extraAdv = []string{"the GM's ruling"}
	}
	if req.Disadvantage {
		sc.extraDis = []string{"the GM's ruling"}
	}
	sv := sc.roll()
	if req.UseInspiration { // spent either way, as on a check (skill.md: "The inspiration is spent either way")
		db.Exec(`UPDATE characters SET inspiration = false WHERE id = $1`, req.CharacterID)
	}
	_, proficient, saveFeature := saveModifier(campaignID, req.CharacterID, abilityShort)
	label := abilityName + " saving throw"
	if proficient {
		label += " (proficient)"
	}
	fullResult := sv.ForSaver(label)

	desc := fmt.Sprintf("%s: %s saving throw (DC %d)", charName, abilityName, req.DC)
	if req.Description != "" {
		desc = fmt.Sprintf("%s: %s - %s saving throw (DC %d)", charName, req.Description, abilityName, req.DC)
	}
	writeAction(db, campaignID, req.CharacterID, "saving_throw", desc, fullResult)

	st := sv.Save
	rollType := "normal"
	switch st.Mode() {
	case "advantage", "disadvantage":
		rollType = st.Mode()
	case "cancelled":
		rollType = "normal (advantage and disadvantage cancel)"
	}
	var adjustments []string
	for _, x := range st.Adjust {
		adjustments = append(adjustments, x.Why)
	}
	response := map[string]interface{}{
		"success":      sv.Success,
		"character":    charName,
		"ability":      abilityName,
		"proficient":   proficient,
		"roll":         sv.Natural,
		"roll_type":    rollType,
		"ability_mod":  game.Modifier(abilityScoreOf(req.CharacterID, abilityShort)),
		"total_mod":    st.Modifier(),
		"total":        sv.Total,
		"dc":           req.DC,
		"outcome":      sv.Outcome(),
		"result":       fullResult,
		"advantage":    st.Advantage,
		"disadvantage": st.Disadvantage,
		"adjustments":  adjustments,
		"rolls_detail": map[string]interface{}{
			"die1": sv.Dice[0],
			"die2": sv.Dice[1],
		},
	}
	if len(sv.Changed) > 0 {
		response["changed"] = sv.Changed // Halfling Luck, Heroic Inspiration, the Luck Blade, Indomitable Might
	}
	if st.AutoFail != "" && st.AutoSucceed == "" {
		response["auto_fail"] = true
		response["auto_fail_reason"] = st.AutoFail
		response["condition_note"] = fmt.Sprintf("%s is %s and automatically fails %s saving throws", charName, st.AutoFail, abilityName)
	}
	if req.UseInspiration {
		response["used_inspiration"] = true
		response["inspiration_note"] = fmt.Sprintf("%s spent Heroic Inspiration: rerolled=%v (a failed save is rolled again and the new roll is used)", charName, sc.inspirationUsed)
	}
	if saveFeature != "" {
		response["save_proficiency_from"] = saveFeature
		response["class_feature_note"] = fmt.Sprintf("%s's %s gives proficiency in this saving throw", charName, saveFeature)
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
// @Body Contested check details
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

	var req struct {
		InitiatorID           int    `json:"initiator_id"`           // Character ID of initiator
		DefenderID            int    `json:"defender_id"`            // Character ID of defender
		InitiatorSkill        string `json:"initiator_skill"`        // Skill or ability: athletics, acrobatics, str, dex, etc.
		DefenderSkill         string `json:"defender_skill"`         // Skill or ability (can be "athletics_or_acrobatics" for choice)
		InitiatorAdvantage    bool   `json:"initiator_advantage"`    // true: the initiator rolls with Advantage
		InitiatorDisadvantage bool   `json:"initiator_disadvantage"` // true: the initiator rolls with Disadvantage
		DefenderAdvantage     bool   `json:"defender_advantage"`     // true: the defender rolls with Advantage
		DefenderDisadvantage  bool   `json:"defender_disadvantage"`  // true: the defender rolls with Disadvantage
		Description           string `json:"description"`            // e.g., "grapple attempt", "shove"
	}
	campaignID, ok := gmReadAs(w, r, agentID, &req, targetsIn(&req.InitiatorID, &req.DefenderID))
	if !ok {
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
	initName, initStr, initDex, initCon, initInt, initWis, initCha, initLevel, _, err := getCharStats(req.InitiatorID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "initiator_not_found"})
		return
	}

	defName, defStr, defDex, defCon, defInt, defWis, defCha, defLevel, _, err := getCharStats(req.DefenderID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "defender_not_found"})
		return
	}

	// Verify both are in this campaign

	// Helper to calculate skill/ability modifier: a skill adds the Proficiency Bonus only when the character is proficient
	// (Expertise doubles it, Jack of All Trades gives half), through checkBonus.
	calcMod := func(charID int, skill string, str, dex, con, intl, wis, cha, level int) (mod int, skillName string) {
		skill = strings.ToLower(skill)
		if sk, ok := game.LookupSkill(skill); ok { // any spelling of a skill (game.Skills)
			var class string
			db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&class)
			score := map[string]int{"str": str, "dex": dex, "con": con, "int": intl, "wis": wis, "cha": cha}[sk.Ability]
			m, prof, _, _ := checkBonus(charID, class, level, sk.Name, score)
			return m + prof, titleSkill(sk.Name)
		}
		switch skill {
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

	// Each side's d20 is the check owner's (check_2024.go): the GM's own Advantage or Disadvantage on a side, Large Form, Foresight,
	// Enhance Ability, Reduce, Poisoned, Frightened, riders, armor, Help ... for a character, a monster or a companion alike.
	side := func(id int, skill string, adv, dis bool) checkRoll {
		return checkCtx{lobby: campaignID, who: id, ability: skillAbility(skill), skill: strings.ReplaceAll(skill, "_", " "),
			gmAdvantage: adv, gmDisadvantage: dis}.roll()
	}
	init := side(req.InitiatorID, req.InitiatorSkill, req.InitiatorAdvantage, req.InitiatorDisadvantage)
	initRoll1, initRoll2, initFinalRoll, initRollType := init.R1, init.R2, init.Final, init.Kind
	initTotal := initFinalRoll + initMod - init.Penalty
	if req.InitiatorID > 0 { // Guidance
		g, _ := guidanceBonus(campaignID, req.InitiatorID, req.InitiatorSkill)
		initTotal += g
	}

	def := side(req.DefenderID, defSkill, req.DefenderAdvantage, req.DefenderDisadvantage)
	defRoll1, defRoll2, defFinalRoll, defRollType := def.R1, def.R2, def.Final, def.Kind
	defTotal := defFinalRoll + defMod - def.Penalty
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
	writeAction(db, campaignID, req.InitiatorID, "contested_check", desc, fullResult)

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
