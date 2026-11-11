package main

// Player commands on the rules of the SRD 5.2.1:
//	POST /api/characters/breath-weapon      Dragonborn: replaces one attack; a 15-foot Cone or a 30-foot Line (shape: cone|line);
//	                                        Dexterity save (DC 8 + Constitution modifier + PB), 1d10 (2d10 at 5, 3d10 at 11, 4d10 at 17),
//	                                        half on a success; Proficiency Bonus uses per Long Rest
//	POST /api/characters/wholeness-of-body  Warrior of the Open Hand 6: a Bonus Action, Martial Arts die + Wisdom modifier Hit Points,
//	                                        Wisdom-modifier uses per Long Rest
//	POST /api/characters/holy-nimbus        Oath of Devotion 20: a Bonus Action, 10 minutes: enemies that start their turn in the aura
//	                                        take Radiant damage equal to the Charisma modifier + PB; once per Long Rest or a level 5 slot
//	POST /api/characters/one-with-shadows   Eldritch Invocation: in Dim Light or Darkness, cast Invisibility on yourself without a slot
//	POST /api/gm/superior-hunters-defense   Hunter 15: a Reaction when the Ranger takes damage: Resistance to that damage
// Hunter's Defensive Tactics (2024): Escape the Horde (Opportunity Attacks have Disadvantage against you) and Multiattack Defense (after
// a creature hits you, its other attack rolls against you this turn have Disadvantage) are applied to monster attacks.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var draconicDamage = map[string]string{"black": "acid", "blue": "lightning", "brass": "fire", "bronze": "lightning", "copper": "acid",
	"gold": "fire", "green": "poison", "red": "fire", "silver": "cold", "white": "cold"}

func playerOwns(r *http.Request, charID int) (int, bool) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		return 0, false
	}
	var owner int
	db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, charID).Scan(&owner)
	return agentID, owner == agentID
}

// @Summary Dragonborn Breath Weapon
// @Description When you take the Attack action, you can replace one of your attacks with an exhalation of magical energy in either a 15-foot Cone or a 30-foot Line that is 5 feet wide (shape: cone or line). Each creature in the area (target_ids) makes a Dexterity saving throw (DC 8 + Constitution modifier + Proficiency Bonus): 1d10 damage of your Draconic Ancestry type on a failure (2d10 at level 5, 3d10 at 11, 4d10 at 17), half on a success. Proficiency Bonus uses per Long Rest.
// @Tags Characters
// @Param request body object{character_id=int,target_ids=[]int,shape=string} true "Breath Weapon"
// @Router /characters/breath-weapon [post]
func handleBreathWeapon2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		TargetIDs   []int  `json:"target_ids"`
		Shape       string `json:"shape"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if _, ok := playerOwns(r, req.CharacterID); !ok {
		writeJSONStatus(w, 403, map[string]interface{}{"error": "not_your_character"})
		return
	}
	var name, species string
	var level, con, lobby int
	var originRaw []byte
	var ancestryCol *string
	db.QueryRow(`SELECT name, COALESCE(species,''), level, con, COALESCE(lobby_id,0), COALESCE(origin,'{}'), draconic_ancestry FROM characters WHERE id = $1`, req.CharacterID).
		Scan(&name, &species, &level, &con, &lobby, &originRaw, &ancestryCol)
	if !strings.Contains(strings.ToLower(species), "dragonborn") {
		dmBadRequest(w, "not_dragonborn", "Breath Weapon is a Dragonborn trait.")
		return
	}
	ancestry := ""
	if rec, ok := loadOriginRecord(originRaw); ok {
		ancestry = strings.ToLower(rec.Species.Choices["ancestry"])
	}
	if ancestry == "" && ancestryCol != nil {
		ancestry = strings.ToLower(*ancestryCol)
	}
	dtype := draconicDamage[ancestry]
	if dtype == "" {
		dtype = "fire"
	}
	shape := strings.ToLower(req.Shape)
	if shape != "line" {
		shape = "cone"
	}
	if used, max := breathWeaponUses(req.CharacterID, level); used >= max {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "no_uses_left", "message": fmt.Sprintf("%s has used all %d Breath Weapon uses; they return on a Long Rest.", name, max)})
		return
	}
	_, inCombat, _ := isCharacterInCombat(req.CharacterID)
	if inCombat {
		if ok, _, msg := checkActionEconomy(req.CharacterID, "attack", 0); !ok {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "resource_exhausted", "message": msg + " (Breath Weapon replaces one attack of the Attack action.)"})
			return
		}
	}
	size := 15
	if shape == "line" {
		size = 30
	}
	targets := req.TargetIDs
	note := ""
	if len(targets) > 0 {
		targets, note = areaFilter(lobby, req.CharacterID, SRDSpell{Name: "Breath Weapon", AoEShape: shape, AoESize: size}, targets)
	}
	spendBreathWeapon(req.CharacterID, level)
	if inCombat {
		consumeAttackAction(req.CharacterID)
	}
	dc := 8 + game.Modifier(con) + game.ProficiencyBonus(level)
	dmg := game.RollDamage(getBreathWeaponDamageDice(level), false)
	var lines []string
	for _, id := range targets {
		tn := creatureName(lobby, id)
		saved, _ := creatureSave(lobby, id, "dex", dc)
		d := dmg
		how := "fails its Dexterity save"
		if saved {
			d, how = dmg/2, "succeeds on its Dexterity save"
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s.", tn, how, damageAny(lobby, id, d, dtype)))
	}
	result := fmt.Sprintf("%s exhales %s in a %d-foot %s (DC %d Dexterity save, %d %s damage, half on a success). %s%s", name, dtype, size,
		map[string]string{"cone": "Cone", "line": "Line 5 feet wide"}[shape], dc, dmg, dtype, strings.Join(lines, " "), note)
	if lobby != 0 {
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1,$2,'breath_weapon','Breath Weapon',$3)`, lobby, req.CharacterID, result)
	}
	used, max := breathWeaponUses(req.CharacterID, level)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": playerText(result), "uses_left": max - used})
}

// @Summary Warrior of the Open Hand Wholeness of Body (level 6)
// @Description As a Bonus Action, roll your Martial Arts die and regain that many Hit Points plus your Wisdom modifier (minimum 1). Uses: the Wisdom modifier (minimum once) per Long Rest.
// @Tags Characters
// @Param request body object{character_id=int} true "The Monk"
// @Router /characters/wholeness-of-body [post]
func handleWholenessOfBody2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		CharacterID int `json:"character_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if _, ok := playerOwns(r, req.CharacterID); !ok {
		writeJSONStatus(w, 403, map[string]interface{}{"error": "not_your_character"})
		return
	}
	var class, subclass string
	var level, wis int
	db.QueryRow(`SELECT class, COALESCE(subclass,''), level, wis FROM characters WHERE id = $1`, req.CharacterID).Scan(&class, &subclass, &level, &wis)
	if !strings.EqualFold(class, "monk") || !strings.Contains(strings.ToLower(subclass), "open") || level < 6 {
		dmBadRequest(w, "not_available", "Wholeness of Body is a Warrior of the Open Hand feature from Monk level 6.")
		return
	}
	uses := game.Modifier(wis)
	if uses < 1 {
		uses = 1
	}
	spent := classResourcesUsed(req.CharacterID)["wholeness_of_body"]
	if spent >= uses {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "no_uses_left", "message": fmt.Sprintf("All %d uses are spent; they return on a Long Rest.", uses)})
		return
	}
	if _, fighting, _ := isCharacterInCombat(req.CharacterID); fighting {
		if ok, _, msg := checkActionEconomy(req.CharacterID, "bonus_action", 0); !ok {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "resource_exhausted", "message": msg})
			return
		}
		db.Exec(`UPDATE characters SET bonus_action_used = true WHERE id = $1`, req.CharacterID)
	}
	setUsedResource(req.CharacterID, "wholeness_of_body", spent+1)
	die := getMonkDie(level)
	roll := game.RollDamage("1"+strings.TrimPrefix(die, "1"), false)
	heal := roll + game.Modifier(wis)
	if heal < 1 {
		heal = 1
	}
	n, before, after := healCharacter(req.CharacterID, heal)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": fmt.Sprintf("Wholeness of Body: %s regains %d Hit Points (%s %d + Wisdom; %d -> %d).", n, after-before, die, roll, before, after), "uses_left": uses - spent - 1})
}

// @Summary Oath of Devotion Holy Nimbus (level 20)
// @Description As a Bonus Action, imbue your Aura of Protection with holy power for 10 minutes: Advantage on saving throws forced by a Fiend or an Undead (Holy Ward), enemies that start their turn in the aura take Radiant damage equal to your Charisma modifier plus Proficiency Bonus, and the aura is filled with sunlight. Once per Long Rest, or expend a level 5 spell slot (use_slot=true).
// @Tags Characters
// @Param request body object{character_id=int,use_slot=bool} true "The Paladin"
// @Router /characters/holy-nimbus [post]
func handleHolyNimbus2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		CharacterID int  `json:"character_id"`
		UseSlot     bool `json:"use_slot"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if _, ok := playerOwns(r, req.CharacterID); !ok {
		writeJSONStatus(w, 403, map[string]interface{}{"error": "not_your_character"})
		return
	}
	var name, class, subclass string
	var level, lobby int
	db.QueryRow(`SELECT name, class, COALESCE(subclass,''), level, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&name, &class, &subclass, &level, &lobby)
	if !strings.EqualFold(class, "paladin") || !strings.EqualFold(subclass, "devotion") || level < 20 {
		dmBadRequest(w, "not_available", "Holy Nimbus is an Oath of Devotion feature at Paladin level 20.")
		return
	}
	note := ""
	if classResourcesUsed(req.CharacterID)["holy_nimbus"] > 0 {
		if !req.UseSlot {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "already_used", "message": "Holy Nimbus returns on a Long Rest; send use_slot=true to expend a level 5 spell slot instead."})
			return
		}
		if !spendSlot(req.CharacterID, class, level, 5) {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "no_spell_slot", "message": "No level 5 spell slot left."})
			return
		}
		note = " (a level 5 spell slot is expended)"
	}
	if _, fighting, _ := isCharacterInCombat(req.CharacterID); fighting {
		if ok, _, msg := checkActionEconomy(req.CharacterID, "bonus_action", 0); !ok {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "resource_exhausted", "message": msg})
			return
		}
		db.Exec(`UPDATE characters SET bonus_action_used = true WHERE id = $1`, req.CharacterID)
	}
	setUsedResource(req.CharacterID, "holy_nimbus", 1)
	addSpellEffect(lobby, req.CharacterID, req.CharacterID, "Holy Nimbus", "holy_nimbus", "", 100, "", 0)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": fmt.Sprintf("%s's aura blazes with sunlight for 10 minutes%s: enemies that start their turn in it take Radiant damage, and %s has Advantage on saves forced by Fiends and Undead.", name, note, name)})
}

// holyNimbusDamage: a monster that starts its turn in a Holy Nimbus takes Radiant damage (Charisma modifier + Proficiency Bonus).
func holyNimbusDamage(lobbyID, monsterID int) []string {
	rows, err := db.Query(`SELECT target_id FROM spell_effects WHERE lobby_id = $1 AND kind = 'holy_nimbus'`, lobbyID)
	if err != nil {
		return nil
	}
	var paladins []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			paladins = append(paladins, id)
		}
	}
	rows.Close()
	var out []string
	for _, p := range paladins {
		var level, cha, hp int
		db.QueryRow(`SELECT level, cha, hp FROM characters WHERE id = $1`, p).Scan(&level, &cha, &hp)
		d, known := charDistanceFeet(lobbyID, p, monsterID)
		if hp <= 0 || !known || d > 30 { // the aura is 30 feet at level 18+; positions need the battle map
			continue
		}
		dmg := game.Modifier(cha) + game.ProficiencyBonus(level)
		text, _ := damageMonster(lobbyID, monsterID, dmg, "radiant")
		out = append(out, "Holy Nimbus: "+strings.TrimSpace(text))
	}
	return out
}

// @Summary Eldritch Invocation One with Shadows
// @Description While you're in an area of Dim Light or Darkness, you can cast Invisibility on yourself without expending a spell slot (an action; Concentration). It ends when you make an attack roll, deal damage or cast a spell.
// @Tags Characters
// @Param request body object{character_id=int} true "The Warlock"
// @Router /characters/one-with-shadows [post]
func handleOneWithShadows2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		CharacterID int `json:"character_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if _, ok := playerOwns(r, req.CharacterID); !ok {
		writeJSONStatus(w, 403, map[string]interface{}{"error": "not_your_character"})
		return
	}
	if !hasInvocation(req.CharacterID, "one-with-shadows") {
		dmBadRequest(w, "no_invocation", "This needs the One with Shadows invocation.")
		return
	}
	var lobby int
	db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&lobby)
	if l := strings.ToLower(getCampaignLighting(lobby)); l == "bright" {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "too_bright", "message": "One with Shadows needs Dim Light or Darkness (the GM sets the light with POST /api/gm/set-lighting)."})
		return
	}
	if _, fighting, _ := isCharacterInCombat(req.CharacterID); fighting {
		if ok, _, msg := checkActionEconomy(req.CharacterID, "action", 0); !ok {
			writeJSONStatus(w, 409, map[string]interface{}{"error": "resource_exhausted", "message": msg})
			return
		}
		db.Exec(`UPDATE characters SET action_used = true WHERE id = $1`, req.CharacterID)
	}
	db.Exec(`UPDATE characters SET concentrating_on = 'Invisibility' WHERE id = $1`, req.CharacterID)
	addCharacterCondition(req.CharacterID, "invisible:one_with_shadows")
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": "You cast Invisibility on yourself without a spell slot: you have the Invisible condition while you keep Concentration (up to 1 hour), until you make an attack roll, deal damage or cast a spell."})
}

// @Summary Hunter Superior Hunter's Defense (level 15)
// @Description When the Ranger takes damage, they can take a Reaction to give themselves Resistance to that damage and any other damage of the same type until the end of the current turn. The GM sends the damage here instead of to /api/characters/{id}/damage.
// @Tags GM
// @Param request body object{character_id=int,damage=int,damage_type=string} true "The Ranger and the damage"
// @Router /gm/superior-hunters-defense [post]
func handleGMSuperiorHuntersDefense(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		Damage      int    `json:"damage"`
		DamageType  string `json:"damage_type"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	var class, subclass string
	var level int
	var reaction bool
	db.QueryRow(`SELECT class, COALESCE(subclass,''), level, COALESCE(reaction_used,false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&class, &subclass, &level, &reaction)
	if !strings.EqualFold(class, "ranger") || !strings.EqualFold(subclass, "hunter") || level < 15 {
		dmBadRequest(w, "not_available", "Superior Hunter's Defense is a Hunter feature from Ranger level 15.")
		return
	}
	if reaction {
		writeJSONStatus(w, 409, map[string]interface{}{"error": "reaction_used", "message": "The Ranger has used their Reaction this round."})
		return
	}
	db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, req.CharacterID)
	text := damageCharacter(req.CharacterID, req.Damage/2, strings.ToLower(req.DamageType))
	result := fmt.Sprintf("%s takes a Reaction: Resistance to %s damage until the end of the turn; %s.", creatureName(lobby, req.CharacterID), strings.ToLower(req.DamageType), text)
	recordGMAction(db, lobby, "superior_hunters_defense", result, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}

// hunterDefense is the Hunter's Defensive Tactics choice of a character ("escape_the_horde", "multiattack_defense" or "").
func hunterDefense(charID int) string {
	var class, subclass string
	var level int
	db.QueryRow(`SELECT class, COALESCE(subclass,''), level FROM characters WHERE id = $1`, charID).Scan(&class, &subclass, &level)
	if !strings.EqualFold(class, "ranger") || !strings.EqualFold(subclass, "hunter") || level < 7 {
		return ""
	}
	return getDefensiveTacticsChoice(charID)
}
