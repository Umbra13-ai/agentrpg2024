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
	"net/http"
	"strings"
	"github.com/agentrpg/agentrpg/game"
)

// martialArtsDice is "2d8" for two rolls of the Martial Arts die.
func martialArtsRolls(level, n int) string { return fmt.Sprintf("%dd%d", n, game.MartialArtsDie(level)) }

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
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
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
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.CharacterID == 0 || req.Damage <= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send character_id and damage (a positive number)."})
		return
	}
	var name, class string
	var level, dex, lobby int
	var reactionUsed bool
	if db.QueryRow(`SELECT name, class, level, dex, COALESCE(lobby_id,0), COALESCE(reaction_used,false) FROM characters WHERE id = $1`, req.CharacterID).
		Scan(&name, &class, &level, &dex, &lobby, &reactionUsed) != nil || lobby != campaignID {
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
		db.Exec(`UPDATE characters SET temp_hp = GREATEST(COALESCE(temp_hp,0), $1) WHERE id = $2`, t, charID)
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
	roll := game.RollDie(20)
	text := fmt.Sprintf("🧘 Disciplined Survivor: 1 Focus Point spent (%d left); the new d20 is %d and you must use it.", left, roll)
	low := strings.ToLower(description)
	if m := reBonus.FindStringSubmatch(low); m != nil {
		var b int
		fmt.Sscan(m[1], &b)
		text += fmt.Sprintf(" Total %d", roll+b)
		if d := reDC.FindStringSubmatch(low); d != nil {
			var dc int
			fmt.Sscan(d[1], &dc)
			if roll+b >= dc {
				text += fmt.Sprintf(" against DC %d: the save succeeds.", dc)
			} else {
				text += fmt.Sprintf(" against DC %d: the save fails.", dc)
			}
		}
	}
	return text
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
	setUsedResource(charID, "superior_defense_rounds", 10) // 1 minute
	return fmt.Sprintf("🛡️ Superior Defense: 3 Focus Points spent (%d left); for 1 minute or until you are Incapacitated you have Resistance to all damage except Force damage.", left)
}

// superiorDefenseTick counts a round at the end of the Monk's turn; the effect ends after 1 minute or when the Monk is Incapacitated.
func superiorDefenseTick(charID int) {
	if !hasCondition(charID, "superior_defense") {
		return
	}
	used := classResourcesUsed(charID)
	used["superior_defense_rounds"]--
	if used["superior_defense_rounds"] <= 0 || isIncapacitated(charID) {
		delete(used, "superior_defense_rounds")
		removeCondition(charID, "superior_defense")
	}
	writeUsed(charID, used)
}

// quiveringPalm: "quivering_palm start" (4 Focus Points, after an Unarmed Strike hit) with target_id; "quivering_palm end" with target_id
// (or "end harmlessly").
func quiveringPalm(charID int, description string) string {
	class, _, level := subclassOf(charID)
	if class != "monk" || level < 17 {
		return "Quivering Palm needs a Monk of level 17 or higher."
	}
	used := classResourcesUsed(charID)
	lobby, mid, isMonster := attackedMonster(charID)
	low := strings.ToLower(description)
	switch {
	case strings.Contains(low, "end"):
		if used["quivering_palm"] == 0 {
			return "No creature is under your Quivering Palm."
		}
		target := -used["quivering_palm"]
		delete(used, "quivering_palm")
		writeUsed(charID, used)
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
		if used["quivering_palm"] != 0 {
			return "Only one creature can be under your Quivering Palm."
		}
		if okFP, msg, left := useClassResource(charID, "focus", 4); !okFP {
			return "Cannot start Quivering Palm: " + msg
		} else {
			used = classResourcesUsed(charID)
			used["quivering_palm"] = -mid
			writeUsed(charID, used)
			return fmt.Sprintf("🌀 Quivering Palm: 4 Focus Points spent (%d left); imperceptible vibrations last %d days or until you end them.", left, level)
		}
	}
	return "Say \"quivering_palm start\" with a target_id after an Unarmed Strike hit, or \"quivering_palm end\"."
}

// ---- class choices (Land type, Elemental Affinity, Blessed Strikes, Elemental Fury)

// applyClassChoice handles the class-options fields that live in subclass_choices. It returns true when it answered.
func applyClassChoice(w http.ResponseWriter, charID int, landType, affinity, blessed, fury string) bool {
	if landType == "" && affinity == "" && blessed == "" && fury == "" {
		return false
	}
	class, sub, level := subclassOf(charID)
	var raw []byte
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	choices := map[string]string{}
	json.Unmarshal(raw, &choices)
	fail := func(code, msg string) bool {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg})
		return true
	}
	set := func(key, val string) bool {
		choices[key] = val
		b, _ := json.Marshal(choices)
		db.Exec(`UPDATE characters SET subclass_choices = $1 WHERE id = $2`, string(b), charID)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, key: val})
		return true
	}
	in := func(v string, opts ...string) bool {
		for _, o := range opts {
			if v == o {
				return true
			}
		}
		return false
	}
	switch {
	case landType != "":
		v := strings.ToLower(strings.TrimSpace(landType))
		if dl := classLevelsOf(charID)["druid"]; class != "druid" && dl > 0 && subclassesByClass(charID)["druid"] == "land" {
			class, sub, level = "druid", "land", dl // a multiclass character's Druid levels
		}
		if class != "druid" || sub != "land" || level < 3 {
			return fail("not_land_druid", "Only a Druid of the Circle of the Land, level 3 or higher, chooses a land type.")
		}
		if !in(v, "arid", "polar", "temperate", "tropical") {
			return fail("invalid_land_type", "Choose arid, polar, temperate or tropical.")
		}
		var ready bool
		db.QueryRow(`SELECT COALESCE(land_ready,false) FROM characters WHERE id = $1`, charID).Scan(&ready)
		if choices["circle_land"] != "" && !ready {
			return fail("needs_long_rest", "You choose a land type again when you finish a Long Rest.")
		}
		db.Exec(`UPDATE characters SET land_ready = false WHERE id = $1`, charID)
		return set("circle_land", v)
	case affinity != "":
		v := strings.ToLower(strings.TrimSpace(affinity))
		if class != "sorcerer" || sub != "draconic" || level < 6 {
			return fail("not_draconic", "Only a Draconic Sorcerer of level 6 or higher chooses an Elemental Affinity.")
		}
		if !in(v, "acid", "cold", "fire", "lightning", "poison") {
			return fail("invalid_type", "Choose acid, cold, fire, lightning or poison.")
		}
		if choices["elemental_affinity"] != "" {
			return fail("already_chosen", "Your Elemental Affinity is "+choices["elemental_affinity"]+".")
		}
		return set("elemental_affinity", v)
	case blessed != "":
		v := strings.ToLower(strings.TrimSpace(blessed))
		if class != "cleric" || level < 7 || !in(v, "divine_strike", "potent_spellcasting") {
			return fail("invalid_choice", "A Cleric of level 7+ chooses divine_strike or potent_spellcasting (Blessed Strikes).")
		}
		return set("blessed_strikes", v)
	default:
		v := strings.ToLower(strings.TrimSpace(fury))
		if class != "druid" || level < 7 || !in(v, "primal_strike", "potent_spellcasting") {
			return fail("invalid_choice", "A Druid of level 7+ chooses primal_strike or potent_spellcasting (Elemental Fury).")
		}
		return set("elemental_fury", v)
	}
}

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
