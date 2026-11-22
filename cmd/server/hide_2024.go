package main

// The Hide action (SRD 5.2.1 Rules Glossary): a DC 15 Dexterity (Stealth) check, made while Heavily Obscured or behind Three-Quarters
// Cover or Total Cover and out of any enemy's line of sight. On a success the character is hidden (it has the Invisible condition
// while hidden) and the check's total is the DC for a Wisdom (Perception) check to find it. Whether there is anywhere to hide is
// the GM's call: a thief in the middle of a bright, empty room can't hide whatever the roll, so the GM can overrule a success.

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

const hideDC = 15

// attemptHide rolls the Hide action for a character and marks it hidden on a success. label is how the action is named in the reply
// ("Hide", "Cunning Action (Hide)").
func attemptHide(charID int, label string) string {
	var dex, level int
	var class string
	db.QueryRow(`SELECT dex, level, class FROM characters WHERE id = $1`, charID).Scan(&dex, &level, &class)
	mod, prof, profNote, reliable := checkBonus(charID, class, level, "stealth", dex)
	lobby := lobbyOfChar(charID)
	guide, guideNote := guidanceBonus(lobby, charID, "stealth")
	pwt, pwtNote := buffStealthBonus(charID) // Pass without Trace +10
	guide += pwt
	notes := guideNote + pwtNote
	// What gives the check Advantage or Disadvantage has one owner (check_2024.go): conditions, riders, armor, Boots of Elvenkind, travel
	// pace, Foresight, Enhance Ability, Reduce ...; they cancel to one d20
	why := checkCtx{lobby: lobby, who: charID, ability: "dex", skill: "stealth"}.sources()
	d, _, _, _ := rollCheckD20(why.Advantage(), why.Disadvantage())
	notes += why.note()
	roll, rtNote := reliableRoll(d, reliable)
	total := roll + mod + prof + guide
	if total < hideDC && luckReroll(charID, false) { // the Luck Blade's Luck
		roll, rtNote = reliableRoll(game.RollDie(20), reliable)
		total = roll + mod + prof + guide
		notes += " [Luck Blade: rerolled]"
	}
	removeCondition(charID, "hidden")
	if total < hideDC {
		db.Exec(`UPDATE characters SET hide_dc = 0 WHERE id = $1`, charID)
		return fmt.Sprintf("🙈 %s: Dexterity (Stealth)%s %d (d20: %d%s%s). Not hidden: the check needs %d.", label, profNote, total, roll, rtNote, notes, hideDC)
	}
	addCharacterCondition(charID, "hidden") // the condition owner
	db.Exec(`UPDATE characters SET hide_dc = $1 WHERE id = $2`, total, charID)
	return fmt.Sprintf("🙈 %s: Dexterity (Stealth)%s %d (d20: %d%s%s). Hidden, if there is somewhere to hide: you must be Heavily Obscured or behind Three-Quarters or Total Cover and out of every enemy's line of sight, and the GM can rule that there isn't. "+
		"While hidden you have the Invisible condition; a Wisdom (Perception) check of %d finds you. You stop being hidden if you make a sound louder than a whisper, an enemy finds you, you make an attack roll or cast a spell with a Verbal component.",
		label, profNote, total, roll, rtNote, notes, total)
}

// hideLightGateRe: a Hide that a stat block allows only in some light ("While in Dim Light or Darkness, the shadow takes the Hide
// action").
var hideLightGateRe = regexp.MustCompile(`(?i)\bWhile in (Dim Light or Darkness|Dim Light)\b`)

// attemptMonsterHide rolls the Hide action a monster's stat block entry takes: the entry's light condition first (the light at the
// monster's square: monsterLightingAt), then a DC 15 Dexterity (Stealth) check (its Stealth skill, else its Dexterity modifier;
// Pass without Trace +10; Disadvantage in sunlight with Sunlight Sensitivity). On a success it is hidden (the hidden condition: the
// attack roll owner reads it as an unseen attacker until it makes an attack roll). Where there is nowhere to hide is the GM's call,
// as for a character (POST /api/gm/overrule-hide ends it).
func attemptMonsterHide(lobbyID, monsterID int, name, entryName, text string, apply bool) string {
	if m := hideLightGateRe.FindStringSubmatch(text); m != nil {
		light := monsterLightingAt(lobbyID, monsterID)
		allowed := map[string]bool{"dim": strings.Contains(strings.ToLower(m[1]), "dim"),
			"darkness": strings.Contains(strings.ToLower(m[1]), "darkness"), "magical darkness": strings.Contains(strings.ToLower(m[1]), "darkness")}
		if !allowed[light] {
			return fmt.Sprintf("%s can't use %s: it can take this Hide only while in %s, and it stands in %s light. Nothing was rolled.", name, entryName, m[1], light)
		}
	}
	if !apply {
		return fmt.Sprintf("%s would use %s: the Hide action, a DC %d Dexterity (Stealth) check. Nothing was rolled.", name, entryName, hideDC)
	}
	key := ""
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == monsterID {
			key = sprintAny(e["monster_key"])
		}
	}
	bonus := monsterSkillBonus(key, "stealth")
	if bonus == -99 {
		var str, dex, pb int
		var size, ctype string
		monsterQueryRow(`SELECT COALESCE(str,10), COALESCE(dex,10), COALESCE(proficiency_bonus,2), COALESCE(size,'Medium'), COALESCE(type,'') FROM monsters WHERE slug = $1`, key).
			Scan(&str, &dex, &pb, &size, &ctype)
		bonus = game.Modifier(dex)
	}
	notes := ""
	if passWithoutTraceOn(lobbyID, monsterID) {
		bonus += 10
		notes += " [Pass without Trace +10]"
	}
	bonus -= checkExhaustion(lobbyID, monsterID) // SRD 5.2.1: -2 per Exhaustion level on a D20 Test
	// What gives the check Advantage or Disadvantage has one owner (check_2024.go): Poisoned, Frightened, riders, Sunlight Sensitivity ...
	why := checkCtx{lobby: lobbyID, who: monsterID, ability: "dex", skill: "stealth"}.sources()
	d, _, _, _ := rollCheckD20(why.Advantage(), why.Disadvantage())
	switch {
	case why.Advantage() && why.Disadvantage():
		notes += " [Advantage (" + strings.Join(why.Adv, ", ") + ") and Disadvantage (" + strings.Join(why.Dis, ", ") + ") cancel]"
	case why.Disadvantage():
		notes += " [" + strings.Join(why.Dis, ", ") + ": Disadvantage]"
	case why.Advantage():
		notes += " [" + strings.Join(why.Adv, ", ") + ": Advantage]"
	}
	total := d + bonus
	endCondition(lobbyID, monsterID, "hidden", "")
	if total < hideDC {
		return fmt.Sprintf("%s uses %s: the Hide action, Dexterity (Stealth) %d (d20: %d %+d%s). Not hidden: the check needs %d.", name, entryName, total, d, bonus, notes, hideDC)
	}
	addCondition(lobbyID, monsterID, condAdd{Condition: "hidden", Source: entryName})
	return fmt.Sprintf("%s uses %s: the Hide action, Dexterity (Stealth) %d (d20: %d %+d%s). Hidden, if there is somewhere to hide (Heavily Obscured, or Three-Quarters or Total Cover out of every enemy's sight; the GM can rule there isn't): it has the Invisible condition while hidden, and a Wisdom (Perception) check of %d finds it. It stops being hidden when it makes an attack roll.",
		name, entryName, total, d, bonus, notes, total)
}

// endHidden: the creature is no longer hidden (it made an attack roll: SRD 5.2.1 Hide).
func endHidden(lobbyID, id int) {
	if id > 0 && !isCompanionCombatID(id) {
		if removeCondition(id, "hidden") {
			db.Exec(`UPDATE characters SET hide_dc = 0 WHERE id = $1`, id)
		}
		return
	}
	endCondition(lobbyID, id, "hidden", "")
}

// @Summary Overrule a hide
// @Description The GM rules that a character who hid successfully has nowhere to hide (for example a thief standing in a bright, empty room): the character is no longer hidden, whatever the Stealth roll. The action spent on hiding stays spent. Also use it when an enemy finds the character.
// @Tags GM
// @Body Character and the reason
// @Router /gm/overrule-hide [post]
func handleGMOverruleHide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		Reason      string `json:"reason"` // why the character is no longer hidden (shown in the log)
	}
	_, lobby, ok := gmRead(w, r, &req, optionalBody(), targetsIn(&req.CharacterID))
	if !ok {
		return
	}
	var name string
	var charLobby int
	if req.CharacterID == 0 || db.QueryRow(`SELECT name, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&name, &charLobby) != nil {
		dmBadRequest(w, "character_required", "Send the character_id of a character in this campaign.")
		return
	}
	was := removeCondition(req.CharacterID, "hidden")
	db.Exec(`UPDATE characters SET hide_dc = 0 WHERE id = $1`, req.CharacterID)
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "there is nowhere to hide here"
	}
	result := fmt.Sprintf("%s is not hidden: %s.", name, reason)
	if !was {
		result = fmt.Sprintf("%s wasn't hidden (%s).", name, reason)
	}
	recordGMAction(db, lobby, "hide_overruled", result, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "was_hidden": was, "result": result})
}
