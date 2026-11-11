package main

// Barbarian, 2024 rules (SRD 5.2.1).
//	Rage Damage    a bonus to damage with Strength attacks while the Rage is active (game.RageDamageBonus).
//	Frenzy         Path of the Berserker: while Raging and attacking recklessly, the first target you hit on your
//	               turn with a Strength attack takes extra damage: d6s equal to the Rage Damage bonus.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// frenzyPending marks a Frenzy that was rolled for an attack whose hit is not known yet; a miss gives it back.
var frenzyPending sync.Map

var hitOnlyNotes = regexp.MustCompile(` \[Frenzy \+\d+\]| \[Brutal Strike \+\d+\](?: \[[^\]]*\])?| \[Radiant Strikes \+\d+ radiant\]`)

// withdrawHitEffects removes the damage notes of effects that need a hit and gives back a Frenzy used on a miss.
func withdrawHitEffects(charID int, rollInfo string) string {
	pendingBrutal.Delete(charID)
	pendingCunning.Delete(charID)
	if _, ok := frenzyPending.LoadAndDelete(charID); ok {
		db.Exec(`UPDATE characters SET frenzy_used = false WHERE id = $1`, charID)
	}
	refundOnceAttack(charID) // Lifedrinker, Primal Strike (F147, warlock_fixes_2024.go)
	return hitOnlyNotes.ReplaceAllString(rollInfo, "")
}

func ensureBarbarianColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS frenzy_used BOOLEAN DEFAULT FALSE`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS intimidating_presence_used BOOLEAN DEFAULT FALSE`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS capstone_scores_applied BOOLEAN DEFAULT FALSE`)
	}
}

// frenzyDamage rolls the extra damage of the Berserker's Frenzy for the first Strength hit of the turn.
// The dice double on a critical hit. It returns 0 and "" when Frenzy does not apply.
func frenzyDamage(charID int, class string, subclass sql.NullString, level int, strengthAttack, crit bool) (int, string) {
	if !strengthAttack || strings.ToLower(class) != "barbarian" || level < 3 || !subclass.Valid || strings.ToLower(subclass.String) != "berserker" {
		return 0, ""
	}
	if !hasCondition(charID, "raging") || !hasCondition(charID, "reckless") {
		return 0, ""
	}
	var used bool
	db.QueryRow(`SELECT COALESCE(frenzy_used, false) FROM characters WHERE id = $1`, charID).Scan(&used)
	if used {
		return 0, ""
	}
	db.Exec(`UPDATE characters SET frenzy_used = true WHERE id = $1`, charID)
	frenzyPending.Store(charID, true)
	dice := game.RageDamageBonus(level)
	total := game.RollDamage(fmt.Sprintf("%dd6", dice), crit)
	return total, fmt.Sprintf(" [Frenzy +%d]", total)
}

// wearingHeavyArmor says whether the character wears Heavy armor (a Rage cannot be entered in it).
func wearingHeavyArmor(charID int) bool {
	var armor sql.NullString
	if db.QueryRow(`SELECT equipped_armor FROM characters WHERE id = $1`, charID).Scan(&armor) != nil || !armor.Valid || armor.String == "" {
		return false
	}
	info, err := getArmorInfo(armor.String)
	return err == nil && info != nil && strings.EqualFold(info.Type, "heavy")
}

// handleGMIntimidatingPresence: POST /api/gm/intimidating-presence (Berserker level 14).
// As a Bonus Action the Barbarian frightens each creature of their choice in a 30-foot Emanation: a Wisdom save
// (DC 8 + Strength modifier + Proficiency Bonus); on a failure the creature is Frightened for 1 minute and repeats
// the save at the end of each of its turns. After one use it is unavailable until a Long Rest, unless the Barbarian
// expends a use of Rage ("restore_with_rage": true).
//
//	{"barbarian_id": N, "target_ids": [characters or negative monster ids], "restore_with_rage": false}
//
// @Summary Berserker Intimidating Presence (level 14)
// @Description As a Bonus Action the Barbarian frightens each creature of their choice in a 30-foot Emanation: Wisdom save (DC 8 + Strength modifier + Proficiency Bonus); on a failure Frightened for 1 minute, repeating the save at the end of each of its turns. Once per Long Rest, or expend a use of Rage (restore_with_rage).
// @Tags GM
// @Param request body object{barbarian_id=int,target_ids=[]int,restore_with_rage=bool} true "Request"
// @Router /gm/intimidating-presence [post]
func handleGMIntimidatingPresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		BarbarianID     int   `json:"barbarian_id"`
		TargetIDs       []int `json:"target_ids"`
		RestoreWithRage bool  `json:"restore_with_rage"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil || req.BarbarianID == 0 || len(req.TargetIDs) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send barbarian_id and target_ids (the creatures in the 30-foot Emanation that the Barbarian chooses)."})
		return
	}
	var name, class string
	var sub sql.NullString
	var level, str, lobby int
	var used bool
	if db.QueryRow(`SELECT name, class, subclass, level, str, COALESCE(lobby_id,0), COALESCE(intimidating_presence_used,false) FROM characters WHERE id = $1`, req.BarbarianID).
		Scan(&name, &class, &sub, &level, &str, &lobby, &used) != nil || lobby != campaignID {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if strings.ToLower(class) != "barbarian" || !sub.Valid || strings.ToLower(sub.String) != "berserker" || level < 14 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_intimidating_presence", "message": name + " is not a Berserker Barbarian of level 14 or higher."})
		return
	}
	restored := ""
	if used {
		if !req.RestoreWithRage {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "already_used", "message": "Intimidating Presence is used until the next Long Rest. Send restore_with_rage: true to expend a use of Rage (no action) and use it again."})
			return
		}
		if okRes, msg, _ := useClassResource(req.BarbarianID, "rage", 1); !okRes {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_rage_left", "message": msg})
			return
		}
		restored = "A use of Rage was expended to restore Intimidating Presence."
	}
	dc := 8 + game.Modifier(str) + game.ProficiencyBonus(level)
	var results []map[string]interface{}
	for _, tid := range req.TargetIDs {
		bonus, autoFail, dis := saveBonusFor(campaignID, tid, "wis")
		roll := game.RollDie(20)
		if dis {
			if b := game.RollDie(20); b < roll {
				roll = b
			}
		}
		total := roll + bonus
		saved := !autoFail && total >= dc
		res := map[string]interface{}{"target_id": tid, "roll": roll, "total": total, "dc": dc, "saved": saved}
		if !saved {
			body, _ := json.Marshal(map[string]interface{}{"target_id": tid, "condition": "frightened", "source": "Intimidating Presence (" + name + ")",
				"duration_rounds": 10, "end_save": map[string]interface{}{"ability": "wis", "dc": dc}})
			rr := httptest.NewRecorder()
			rq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			rq.Header = r.Header.Clone()
			handleGMConditions(rr, rq)
			var out map[string]interface{}
			json.Unmarshal(rr.Body.Bytes(), &out)
			if imm, _ := out["immune"].(bool); imm {
				res["frightened"] = false
				res["note"] = out["message"]
			} else {
				res["frightened"] = true
			}
		}
		results = append(results, res)
	}
	db.Exec(`UPDATE characters SET intimidating_presence_used = true, bonus_action_used = true WHERE id = $1`, req.BarbarianID)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "barbarian": name, "save": "Wisdom", "dc": dc, "results": results, "note": restored})
}

// brutalStrikeOptions are the Brutal Strike effects (Improved Brutal Strike adds the last two at level 13).
var brutalStrikeOptions = []struct {
	Key, Name, Text string
	Level           int
}{
	{"forceful blow", "Forceful Blow", "the target is pushed 15 feet straight away from you; you can then move up to half your Speed straight toward it without provoking Opportunity Attacks", 9},
	{"hamstring blow", "Hamstring Blow", "the target's Speed is reduced by 15 feet until the start of your next turn (only the most recent Hamstring Blow applies)", 9},
	{"staggering blow", "Staggering Blow", "the target has Disadvantage on the next saving throw it makes and can't make Opportunity Attacks until the start of your next turn", 13},
	{"sundering blow", "Sundering Blow", "before the start of your next turn the next attack roll made by another creature against the target gains a +5 bonus", 13},
}

// brutalStrikeDice is the extra damage of Brutal Strike: 1d10, or 2d10 from level 17.
func brutalStrikeDice(level int) string {
	if level >= 17 {
		return "2d10"
	}
	return "1d10"
}

// brutalStrikeWanted says whether this attack is a Brutal Strike: a Barbarian of level 9+ who attacked recklessly and asked for it.
func brutalStrikeWanted(class string, level int, descLower string, reckless bool) bool {
	return strings.ToLower(class) == "barbarian" && level >= 9 && reckless && strings.Contains(descLower, "brutal strike")
}

// pendingBrutal holds the Brutal Strike effects chosen for the attack being resolved; a hit applies them, a miss drops them.
var pendingBrutal sync.Map

// applyBrutalEffects puts the chosen Brutal Strike effects on the target after a hit.
func applyBrutalEffects(lobbyID, charID, targetID int, name, monsterKey string) string {
	v, ok := pendingBrutal.LoadAndDelete(charID)
	if !ok {
		return ""
	}
	out := ""
	for _, k := range v.([]string) {
		switch k {
		case "forceful blow":
			out += pushText(lobbyID, charID, targetID, name, monsterKey, 15)
		case "hamstring blow":
			addCombatEffect(lobbyID, targetID, charID, "hamstring")
		case "staggering blow":
			addCombatEffect(lobbyID, targetID, charID, "staggering")
		case "sundering blow":
			addCombatEffect(lobbyID, targetID, charID, "sundering")
		}
	}
	return out
}

// brutalStrikeHit rolls the extra damage (doubled dice on a critical hit) and names the chosen effects.
func brutalStrikeHit(charID, level int, descLower string, crit bool) (int, string) {
	dmg := game.RollDamage(brutalStrikeDice(level), crit)
	limit := 1
	if level >= 17 {
		limit = 2
	}
	var effects []string
	var kinds []string
	for _, o := range brutalStrikeOptions {
		if o.Level <= level && strings.Contains(descLower, o.Key) && len(effects) < limit {
			effects = append(effects, o.Name+": "+o.Text)
			kinds = append(kinds, o.Key)
		}
	}
	pendingBrutal.Store(charID, kinds)
	note := fmt.Sprintf(" [Brutal Strike +%d]", dmg)
	if len(effects) > 0 {
		note += " [" + strings.Join(effects, "; ") + "]"
	} else {
		note += " [no Brutal Strike effect chosen: name one in the description (forceful blow, hamstring blow" + map[bool]string{true: ", staggering blow, sundering blow", false: ""}[level >= 13] + ")]"
	}
	return dmg, note
}

// classCapstones runs when initiative is rolled: Persistent Rage (Barbarian 15) refills the Rage uses once per Long Rest, and the
// level 20 ability score capstones (Barbarian Primal Champion: Strength and Constitution +4, Monk Body and Mind: Dexterity and
// Wisdom +4, maximum 25) are applied once.
func classCapstones(charID int, name, class string, level int) string {
	var usedJSON []byte
	if db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&usedJSON) != nil {
		return ""
	}
	used := map[string]int{}
	json.Unmarshal(usedJSON, &used)
	note := ""
	var applied bool // the +4 scores are permanent: a Long Rest must not give them again
	db.QueryRow(`SELECT COALESCE(capstone_scores_applied,false) FROM characters WHERE id = $1`, charID).Scan(&applied)
	switch strings.ToLower(class) {
	case "barbarian":
		if level >= 15 && used["persistent_rage"] == 0 && used["rage"] > 0 {
			used["rage"] = 0
			used["persistent_rage"] = 1
			note = fmt.Sprintf("🪓 %s: Persistent Rage: all expended uses of Rage regained", name)
		}
		if level >= 20 && !applied {
			var conBefore int
			db.QueryRow(`SELECT con FROM characters WHERE id = $1`, charID).Scan(&conBefore)
			db.Exec(`UPDATE characters SET str = LEAST(25, str + 4), con = LEAST(25, con + 4), capstone_scores_applied = true WHERE id = $1`, charID)
			conHitPoints(charID, "con", conBefore, minInt(25, conBefore+4)) // a higher Constitution modifier raises the Hit Point maximum for every level
			note = strings.TrimSpace(note + fmt.Sprintf(" Primal Champion: %s's Strength and Constitution rise by 4 (maximum 25)", name))
		}
	case "monk":
		if level >= 20 && !applied {
			db.Exec(`UPDATE characters SET dex = LEAST(25, dex + 4), wis = LEAST(25, wis + 4), capstone_scores_applied = true WHERE id = $1`, charID)
			note = fmt.Sprintf("🥋 %s: Body and Mind: Dexterity and Wisdom rise by 4 (maximum 25)", name)
		}
	}
	if note != "" {
		writeUsed(charID, used)
	}
	return note
}

// Rage duration (2024): a Rage lasts until the end of your next turn. It is extended for another round when you make an attack roll
// against an enemy, force an enemy to make a saving throw, or take a Bonus Action to extend it ("extend_rage"). From level 15
// (Persistent Rage) it lasts 10 minutes without needing to be extended.

func ensureRageColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS rage_age INTEGER DEFAULT 0`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS rage_extended BOOLEAN DEFAULT FALSE`)
	}
}

func startRageClock(charID int) {
	db.Exec(`UPDATE characters SET rage_age = 0, rage_extended = false WHERE id = $1`, charID)
}

// markRageExtended is called when a raging Barbarian makes an attack roll or forces a saving throw.
func markRageExtended(charID int) {
	db.Exec(`UPDATE characters SET rage_extended = true WHERE id = $1 AND class = 'barbarian'`, charID)
}

func extendRage(charID int) string {
	if !hasCondition(charID, "raging") {
		return "You are not raging."
	}
	markRageExtended(charID)
	return "🪓 You take a Bonus Action to extend your Rage: it lasts until the end of your next turn (at most 10 minutes in all)."
}

// rageTick runs at the end of a Barbarian's turn and says when the Rage ends.
func rageTick(charID int) string {
	if !hasCondition(charID, "raging") {
		return ""
	}
	var level, age int
	var extended bool
	var name string
	if db.QueryRow(`SELECT name, level, COALESCE(rage_age,0), COALESCE(rage_extended,false) FROM characters WHERE id = $1`, charID).Scan(&name, &level, &age, &extended) != nil {
		return ""
	}
	if level >= 15 { // Persistent Rage
		return ""
	}
	age++
	if extended {
		db.Exec(`UPDATE characters SET rage_age = 1, rage_extended = false WHERE id = $1`, charID)
		return ""
	}
	if age >= 2 {
		removeCondition(charID, "raging")
		db.Exec(`UPDATE characters SET rage_age = 0, rage_extended = false WHERE id = $1`, charID)
		return fmt.Sprintf("%s's Rage ends: they did not attack, force a save or extend it this turn.", name)
	}
	db.Exec(`UPDATE characters SET rage_age = $1 WHERE id = $2`, age, charID)
	return ""
}
