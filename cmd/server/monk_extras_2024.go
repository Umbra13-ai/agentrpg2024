package main

// More Monk (SRD 5.2.1) and class choices.
//	Deflect Attacks (level 3)      POST /api/gm/deflect-attacks (also /api/gm/deflect-missiles): Reaction, reduces the damage of an attack that
//	                               hit by 1d10 + Dexterity modifier + Monk level; at 0 damage, 1 Focus Point redirects the force.
//	Patient Defense / Step of the Wind  Disengage / Dash as a Bonus Action; 1 Focus Point for the stronger version; Heightened Focus (level 10).
//	Self-Restoration (level 10)    action "self_restoration" removes Charmed, Frightened or Poisoned.
//	Disciplined Survivor (level 14) proficiency in all saving throws (see the saving throw code); action "disciplined_survivor": 1 Focus Point, reroll.
//	Quivering Palm (level 17)      "quivering_palm start" (4 Focus Points, after an Unarmed Strike hit) and "quivering_palm end" with target_id.
//	Superior Defense (level 18)    action "superior_defense": 3 Focus Points, Resistance to all damage except Force.

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"strings"
)

// martialArtsDice is "2d8" for two rolls of the Martial Arts die.
func martialArtsRolls(level, n int) string {
	return fmt.Sprintf("%dd%d", n, game.MartialArtsDie(level))
}

// handleGMDeflectAttacks: POST {"character_id": N, "damage": D, "damage_type": "piercing", "ranged": false, "attacker_name": "x", "redirect_target_id": monster id (optional)}
// @Summary Monk Deflect Attacks (Reaction)
// @Description When an attack hits a Monk (level 3) and deals Bludgeoning, Piercing or Slashing damage, the Monk reduces the damage by 1d10 + Dexterity modifier + Monk level. If the damage drops to 0, the Monk can spend 1 Focus Point to redirect it at a creature within 5 feet (melee) or 60 feet (ranged): redirect_target_id.
// @Tags GM
// @Param request body object{character_id=int,damage=int,damage_type=string,ranged=bool,attacker_name=string,redirect_target_id=int} true "Request"
// @Router /gm/deflect-attacks [post]
func handleGMDeflectAttacks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		CharacterID      int    `json:"character_id"`
		Damage           int    `json:"damage"`
		DamageType       string `json:"damage_type"`
		Ranged           bool   `json:"ranged"`
		AttackerName     string `json:"attacker_name"`
		RedirectTargetID int    `json:"redirect_target_id"`
	}
	_, campaignID, ok := gmRead(w, r, &req, targetsIn(&req.CharacterID, &req.RedirectTargetID))
	if !ok {
		return
	}
	if req.CharacterID == 0 || req.Damage <= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send character_id and damage (a positive number)."})
		return
	}
	var name, class string
	var level, dex, lobby int
	var reactionUsed bool
	if err := db.QueryRow(`SELECT name, class, level, dex, COALESCE(lobby_id,0), COALESCE(reaction_used,false) FROM characters WHERE id = $1`, req.CharacterID).
		Scan(&name, &class, &level, &dex, &lobby, &reactionUsed); err != nil {
		if answerDBFailure(w, err, "the character") {
			return
		}
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if strings.ToLower(class) != "monk" || level < 3 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_deflect_attacks", "message": name + " is not a Monk of level 3 or higher."})
		return
	}
	dt := strings.ToLower(req.DamageType)
	if dt != "" && dt != "bludgeoning" && dt != "piercing" && dt != "slashing" && level < 13 { // Deflect Energy (13): any type
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_physical", "message": "Deflect Attacks works on attacks whose damage includes Bludgeoning, Piercing or Slashing damage."})
		return
	}
	if reactionUsed {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_reaction", "message": name + " has already used their Reaction."})
		return
	}
	db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, req.CharacterID)
	roll := game.RollDie(10)
	reduction := roll + game.Modifier(dex) + level
	if reduction < 0 {
		reduction = 0
	}
	final := req.Damage - reduction
	if final < 0 {
		final = 0
	}
	resp := map[string]interface{}{"success": true, "character": name, "original_damage": req.Damage, "reduction": reduction,
		"calculation": fmt.Sprintf("1d10(%d) + Dexterity modifier(%d) + Monk level(%d)", roll, game.Modifier(dex), level), "final_damage": final}
	text := fmt.Sprintf("🥷 Deflect Attacks: %s reduces the damage from %d to %d.", name, req.Damage, final)
	if final == 0 && req.RedirectTargetID != 0 {
		if okFP, msg, _ := useClassResource(req.CharacterID, "focus", 1); !okFP {
			text += " No Focus Point to redirect the force (" + msg + ")."
		} else {
			dc := 8 + game.ProficiencyBonus(level) + game.Modifier(dex)
			saved, total := monsterSave(campaignID, req.RedirectTargetID, "dex", dc)
			dmg := game.RollDamage(martialArtsRolls(level, 2), false) + game.Modifier(dex)
			if saved {
				text += fmt.Sprintf(" The redirected force: the creature saves (%d vs DC %d) and takes nothing.", total, dc)
			} else {
				if dt == "" {
					dt = "bludgeoning"
				}
				dm, _ := damageMonster(campaignID, req.RedirectTargetID, dmg, dt)
				text += fmt.Sprintf(" The redirected force: the creature fails its Dexterity save (%d vs DC %d). %s", total, dc, dm)
			}
		}
	} else if final == 0 {
		resp["can_redirect"] = "Send redirect_target_id to spend 1 Focus Point and redirect the force (Dexterity save, two Martial Arts dice + Dexterity modifier)."
	}
	resp["result"] = text
	writeJSONStatus(w, http.StatusOK, resp)
}

// patientDefense: Disengage as a Bonus Action; with 1 Focus Point (write "focus" or "dodge") Disengage and Dodge.
func patientDefense(charID int, description string) string {
	class, _, level := subclassOf(charID)
	if class != "monk" || level < 2 {
		return "Patient Defense needs a Monk of level 2 or higher."
	}
	low := strings.ToLower(description)
	if !strings.Contains(low, "focus") && !strings.Contains(low, "dodge") {
		return "🛡️ Patient Defense: you take the Disengage action as a Bonus Action. (Say \"focus\" to spend 1 Focus Point and take Disengage and Dodge.)"
	}
	okFP, msg, left := useClassResource(charID, "focus", 1)
	if !okFP {
		return "Cannot use Patient Defense: " + msg
	}
	if !hasCondition(charID, "dodging") {
		addCharacterCondition(charID, "dodging")
	}
	text := fmt.Sprintf("🛡️ Patient Defense: 1 Focus Point spent (%d left); you take Disengage and Dodge: attacks against you have Disadvantage until your next turn.", left)
	if level >= 10 { // Heightened Focus
		t := game.RollDamage(martialArtsRolls(level, 2), false)
		grantTempHP(charID, t)
		text += fmt.Sprintf(" Heightened Focus: you gain %d Temporary Hit Points.", t)
	}
	return text
}

// selfRestoration (level 10): remove Charmed, Frightened or Poisoned.
func selfRestoration(charID int, description string) string {
	class, _, level := subclassOf(charID)
	if class != "monk" || level < 10 {
		return "Self-Restoration needs a Monk of level 10 or higher."
	}
	low := strings.ToLower(description)
	for _, c := range []string{"charmed", "frightened", "poisoned"} {
		if strings.Contains(low, c) && hasCondition(charID, c) {
			removeCondition(charID, c)
			return "🧘 Self-Restoration: you remove the " + c + " condition from yourself."
		}
	}
	for _, c := range []string{"charmed", "frightened", "poisoned"} {
		if hasCondition(charID, c) {
			removeCondition(charID, c)
			return "🧘 Self-Restoration: you remove the " + c + " condition from yourself."
		}
	}
	return "You have none of Charmed, Frightened or Poisoned."
}

// disciplinedSurvivor (level 14): after a failed saving throw, 1 Focus Point to roll it again ("bonus 5 dc 15").
func disciplinedSurvivor(charID int, description string) string {
	class, _, level := subclassOf(charID)
	if class != "monk" || level < 14 {
		return "Disciplined Survivor needs a Monk of level 14 or higher."
	}
	okFP, msg, left := useClassResource(charID, "focus", 1)
	if !okFP {
		return "Cannot reroll: " + msg
	}
	return fmt.Sprintf("🧘 Disciplined Survivor: 1 Focus Point spent (%d left); the save is rolled again and you must use the new roll: %s", left, rerollReportedSave(description, nil))
}

// superiorDefense (level 18): 3 Focus Points, Resistance to all damage except Force for 1 minute.
func superiorDefense(charID int) string {
	class, _, level := subclassOf(charID)
	if class != "monk" || level < 18 {
		return "Superior Defense needs a Monk of level 18 or higher."
	}
	if hasCondition(charID, "superior_defense") {
		return "Superior Defense is already active."
	}
	okFP, msg, left := useClassResource(charID, "focus", 3)
	if !okFP {
		return "Cannot use Superior Defense: " + msg
	}
	addCharacterCondition(charID, "superior_defense")
	setUse(charID, "superior_defense_rounds", 10) // 1 minute
	return fmt.Sprintf("🛡️ Superior Defense: 3 Focus Points spent (%d left); for 1 minute or until you are Incapacitated you have Resistance to all damage except Force damage.", left)
}

// superiorDefenseTick counts a round at the end of the Monk's turn; the effect ends after 1 minute or when the Monk is Incapacitated.
func superiorDefenseTick(charID int) {
	if !hasCondition(charID, "superior_defense") {
		return
	}
	incapacitated, ended := isIncapacitated(charID), false
	changeSpent(charID, func(s *spent) error { // resources_2024.go
		s.Uses["superior_defense_rounds"]--
		if ended = s.Uses["superior_defense_rounds"] <= 0 || incapacitated; ended {
			delete(s.Uses, "superior_defense_rounds")
		}
		return nil
	})
	if ended {
		removeCondition(charID, "superior_defense")
	}
}

// quiveringPalm: "quivering_palm start" (4 Focus Points, after an Unarmed Strike hit) with target_id; "quivering_palm end" with target_id
// (or "end harmlessly").
func quiveringPalm(charID int, description string) string {
	class, _, level := subclassOf(charID)
	if class != "monk" || level < 17 {
		return "Quivering Palm needs a Monk of level 17 or higher."
	}
	lobby, mid, isMonster := attackedMonster(charID)
	low := strings.ToLower(description)
	switch {
	case strings.Contains(low, "end"):
		target := 0
		if _, _, err := changeSpent(charID, func(s *spent) error { // resources_2024.go
			if s.Uses["quivering_palm"] == 0 {
				return &notEnough{"No creature is under your Quivering Palm.", 0}
			}
			target = -s.Uses["quivering_palm"]
			delete(s.Uses, "quivering_palm")
			return nil
		}); err != nil {
			return spendText(err, "Quivering Palm")
		}
		if strings.Contains(low, "harmless") {
			return "🌀 You end the vibrations harmlessly."
		}
		var wis int
		db.QueryRow(`SELECT wis FROM characters WHERE id = $1`, charID).Scan(&wis)
		if isMonster {
			target = mid
		} else if lobby == 0 {
			db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&lobby)
		}
		dc := 8 + game.ProficiencyBonus(level) + game.Modifier(wis)
		saved, total := monsterSave(lobby, target, "con", dc)
		dmg := game.RollDamage("10d12", false)
		how := "fails"
		if saved {
			dmg /= 2
			how = "saves"
		}
		text, _ := damageMonster(lobby, target, dmg, "force")
		return fmt.Sprintf("🌀 Quivering Palm ends: the target %s its Constitution save (%d vs DC %d). %s", how, total, dc, text)
	case strings.Contains(low, "start"):
		if !isMonster {
			return "Choose the creature you hit with an Unarmed Strike with target_id."
		}
		focusMax := classResourceMax(charID, class, level, "focus", 0)
		_, after, err := changeSpent(charID, func(s *spent) error { // the 4 Focus Points and the target in one step (resources_2024.go)
			if s.Uses["quivering_palm"] != 0 {
				return &notEnough{"Only one creature can be under your Quivering Palm.", 0}
			}
			if err := s.spend("focus", 4, focusMax); err != nil {
				return &notEnough{"Cannot start Quivering Palm: " + err.Error(), 0}
			}
			s.Uses["quivering_palm"] = -mid
			return nil
		})
		if err != nil {
			return spendText(err, "Quivering Palm")
		}
		return fmt.Sprintf("🌀 Quivering Palm: 4 Focus Points spent (%d left); imperceptible vibrations last %d days or until you end them.", focusMax-after.Uses["focus"], level)
	}
	return "Say \"quivering_palm start\" with a target_id after an Unarmed Strike hit, or \"quivering_palm end\"."
}

// ---- class choices (Land type, Elemental Affinity, Blessed Strikes, Elemental Fury)

// elementalAffinityResists: Elemental Affinity gives Resistance to the chosen damage type.
func elementalAffinityResists(charID int, dtype string) bool {
	class, sub, level := subclassOf(charID)
	if class != "sorcerer" || sub != "draconic" || level < 6 {
		return false
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	choices := map[string]string{}
	json.Unmarshal(raw, &choices)
	return choices["elemental_affinity"] != "" && choices["elemental_affinity"] == strings.ToLower(dtype)
}
