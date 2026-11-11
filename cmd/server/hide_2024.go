package main

// The Hide action (SRD 5.2.1 Rules Glossary): a DC 15 Dexterity (Stealth) check, made while Heavily Obscured or behind Three-Quarters
// Cover or Total Cover and out of any enemy's line of sight. On a success the character is hidden (it has the Invisible condition
// while hidden) and the check's total is the DC for a Wisdom (Perception) check to find it. Whether there is anywhere to hide is
// the GM's call: a thief in the middle of a bright, empty room can't hide whatever the roll, so the GM can overrule a success.

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	d, advNote := buffCheckRoll(charID, "dex", game.RollDie(20)) // Foresight, Enhance Ability (Dexterity)
	pwt, pwtNote := buffStealthBonus(charID)                     // Pass without Trace +10
	guide += pwt
	notes := guideNote + advNote + pwtNote
	dis := false
	if armorStealthDisadvantage(charID) {
		dis, notes = true, notes+" [armor: Disadvantage on Stealth]"
	}
	if _, pDis, pNote := travelPaceEffect(lobby, "stealth"); pDis {
		dis, notes = true, notes+pNote
	}
	if bootsOfElvenkind(charID, "stealth") { // F112: Advantage on Stealth (cancels with Disadvantage)
		if dis {
			dis, notes = false, notes+" [Boots of Elvenkind: Advantage and Disadvantage cancel]"
		} else {
			if d2 := game.RollDie(20); d2 > d {
				d = d2
			}
			notes += " [Boots of Elvenkind: Advantage]"
		}
	}
	if dis {
		if d2 := game.RollDie(20); d2 < d {
			d = d2
		}
	}
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
	conds := getCharConditions(charID)
	conds = append(conds, "hidden")
	b, _ := json.Marshal(conds)
	db.Exec(`UPDATE characters SET conditions = $1, hide_dc = $2 WHERE id = $3`, b, total, charID)
	return fmt.Sprintf("🙈 %s: Dexterity (Stealth)%s %d (d20: %d%s%s). Hidden, if there is somewhere to hide: you must be Heavily Obscured or behind Three-Quarters or Total Cover and out of every enemy's line of sight, and the GM can rule that there isn't. "+
		"While hidden you have the Invisible condition; a Wisdom (Perception) check of %d finds you. You stop being hidden if you make a sound louder than a whisper, an enemy finds you, you make an attack roll or cast a spell with a Verbal component.",
		label, profNote, total, roll, rtNote, notes, total)
}

// @Summary Overrule a hide
// @Description The GM rules that a character who hid successfully has nowhere to hide (for example a thief standing in a bright, empty room): the character is no longer hidden, whatever the Stealth roll. The action spent on hiding stays spent. Also use it when an enemy finds the character.
// @Tags GM
// @Param request body object{campaign_id=int,character_id=int,reason=string} true "Character and the reason"
// @Router /gm/overrule-hide [post]
func handleGMOverruleHide(w http.ResponseWriter, r *http.Request) {
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
		Reason      string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	var name string
	var charLobby int
	if req.CharacterID == 0 || db.QueryRow(`SELECT name, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&name, &charLobby) != nil || charLobby != lobby {
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
