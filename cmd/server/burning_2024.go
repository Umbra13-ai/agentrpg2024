package main

// Burning (SRD 5.2.1 Rules Glossary hazard): a burning creature takes 1d4 Fire damage at the start of each of its turns. As an action
// it can put the fire out by giving itself the Prone condition and rolling on the ground; the fire also goes out if it is doused,
// submerged or suffocated (the GM ends it). Alchemist's Fire sets a creature burning; Acid is the other thrown flask.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func ensureBurningTable() {
	db.Exec(`CREATE TABLE IF NOT EXISTS burning (lobby_id INTEGER NOT NULL, target_id INTEGER NOT NULL, source TEXT DEFAULT '', PRIMARY KEY (lobby_id, target_id))`)
}

func isBurning(lobbyID, targetID int) bool {
	if db == nil || lobbyID == 0 {
		return false
	}
	ensureBurningTable()
	var n int
	db.QueryRow(`SELECT count(*) FROM burning WHERE lobby_id = $1 AND target_id = $2`, lobbyID, targetID).Scan(&n)
	return n > 0
}

func startBurning(lobbyID, targetID int, source string) {
	ensureBurningTable()
	db.Exec(`INSERT INTO burning (lobby_id, target_id, source) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, lobbyID, targetID, source)
}

func stopBurning(lobbyID, targetID int) bool {
	ensureBurningTable()
	res, err := db.Exec(`DELETE FROM burning WHERE lobby_id = $1 AND target_id = $2`, lobbyID, targetID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// clearBurning ends Burning in a campaign (F69: it used to last forever, through later fights and Long Rests). When a fight ends, all
// of it goes: the fire goes out with an action, which nothing stops a character from taking out of combat. When a fight starts or
// ends, the monsters' rows go: monster ids are reused from one fight to the next, so a monster's fire must not pass on.
func clearBurning(lobbyID int, monstersOnly bool) {
	if db == nil || lobbyID == 0 {
		return
	}
	ensureBurningTable()
	if monstersOnly {
		db.Exec(`DELETE FROM burning WHERE lobby_id = $1 AND target_id < 0`, lobbyID)
		return
	}
	db.Exec(`DELETE FROM burning WHERE lobby_id = $1`, lobbyID)
}

// stopCharacterBurning: a rest (an hour or more) ends a character's Burning wherever it was set.
func stopCharacterBurning(charID int) {
	if db == nil || charID <= 0 {
		return
	}
	ensureBurningTable()
	db.Exec(`DELETE FROM burning WHERE target_id = $1`, charID)
}

// burningTurnStart deals the 1d4 Fire damage at the start of a burning creature's turn.
func burningTurnStart(lobbyID, id int, name string) string {
	if !isBurning(lobbyID, id) {
		return ""
	}
	dmg := game.RollDie(4)
	if id < 0 {
		text, _ := damageMonster(lobbyID, id, dmg, "fire")
		if text == "" {
			stopBurning(lobbyID, id)
			return ""
		}
		return fmt.Sprintf("🔥 %s is burning: %s", name, text)
	}
	return fmt.Sprintf("🔥 %s is burning and %s. (Action \"extinguish\": drop Prone and roll on the ground to put it out.)", name, damageCharacter(id, dmg, "fire"))
}

// extinguishSelf is the action that puts out the fire on yourself: you give yourself the Prone condition and roll on the ground.
func extinguishSelf(charID int) string {
	lobby := lobbyOfChar(charID)
	if !stopBurning(lobby, charID) {
		return "You aren't burning."
	}
	conds := getCharConditions(charID)
	hasProne := false
	for _, c := range conds {
		if c == "prone" {
			hasProne = true
		}
	}
	if !hasProne {
		conds = append(conds, "prone")
		b, _ := json.Marshal(conds)
		db.Exec(`UPDATE characters SET conditions = $1 WHERE id = $2`, b, charID)
		syncRecordsFromList(charID)
	}
	return "🔥 You drop and roll on the ground: the fire goes out. You have the Prone condition."
}

// consumeInventoryItem removes one of the first item whose name matches (not from a lost bag). It returns false if there is none.
func consumeInventoryItem(charID int, names ...string) bool {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []map[string]interface{}
	json.Unmarshal(raw, &inv)
	lost := lostChecker(inv)
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "’", "'") }
	for i, it := range inv {
		if lost(it) {
			continue
		}
		n := norm(fmt.Sprint(it["name"]))
		for _, want := range names {
			if n == norm(want) || strings.HasPrefix(n, norm(want)+" (") || n == norm(want)+"s" {
				qty := 1
				if q, ok := it["quantity"].(float64); ok {
					qty = int(q)
				}
				if qty <= 1 {
					inv = append(inv[:i], inv[i+1:]...)
				} else {
					inv[i]["quantity"] = float64(qty - 1)
				}
				b, _ := json.Marshal(inv)
				db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, b, charID)
				return true
			}
		}
	}
	return false
}

// thrownFlask replaces an attack with throwing a flask of Alchemist's Fire or a vial of Acid at the monster picked with target_id
// (within 20 feet). The target makes a Dexterity save (DC 8 + the thrower's Dexterity modifier + Proficiency Bonus) or takes the damage;
// Alchemist's Fire also sets it burning.
func thrownFlask(charID int, description string) (string, bool) {
	d := strings.ToLower(strings.ReplaceAll(description, "’", "'"))
	fire := strings.Contains(d, "alchemist's fire") || strings.Contains(d, "alchemists fire") || strings.Contains(d, "alchemist fire")
	acid := !fire && (strings.Contains(d, "vial of acid") || strings.Contains(d, "throw acid") || strings.Contains(d, "acid vial") || strings.Contains(d, "throw the acid") || strings.Contains(d, "throw a vial"))
	if !fire && !acid {
		return "", false
	}
	item, dice, dtype := "Alchemist's Fire", "1d4", "fire"
	if acid {
		item, dice, dtype = "Acid", "2d6", "acid"
	}
	lobby, target, ok := attackedMonster(charID)
	if !ok {
		return fmt.Sprintf("Throwing %s needs target_id (an enemy within 20 feet).", item), true
	}
	if !consumeInventoryItem(charID, item, "flask of "+item, "vial of "+item) {
		return fmt.Sprintf("You have no %s to throw.", item), true
	}
	var dex, level int
	db.QueryRow(`SELECT dex, level FROM characters WHERE id = $1`, charID).Scan(&dex, &level)
	dc := 8 + game.Modifier(dex) + game.ProficiencyBonus(level)
	name := creatureName(lobby, target)
	if saved, _ := monsterSave(lobby, target, "dex", dc); saved {
		return fmt.Sprintf("You throw the %s at %s, who dodges it (Dexterity save succeeds).", item, name), true
	}
	text, _ := damageMonster(lobby, target, game.RollDamage(dice, false), dtype)
	out := fmt.Sprintf("You throw the %s at %s (Dexterity save fails): %s", item, name, text)
	if fire {
		startBurning(lobby, target, "Alchemist's Fire")
		out += fmt.Sprintf(" %s is burning (1d4 Fire at the start of each of its turns until it puts the fire out).", name)
	}
	return out, true
}

// @Summary Set a creature burning, or put the fire out
// @Description Burning (SRD 5.2.1): a burning creature takes 1d4 Fire damage at the start of each of its turns. It can put the fire out with an action (Prone, rolling on the ground; players use the action "extinguish"); it also goes out if doused, submerged or suffocated. Send burning=true to set target_id (a character, or a monster's negative id) on fire, burning=false to put it out. GET with ?campaign_id lists who is burning. Every fire goes out when the fight ends, and a character's when it finishes a Short or Long Rest.
// @Tags GM
// @Param request body object{campaign_id=int,target_id=int,burning=bool,source=string} true "Target"
// @Router /gm/burning [post]
func handleGMBurning(w http.ResponseWriter, r *http.Request) {
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	ensureBurningTable()
	if r.Method == http.MethodGet {
		var list []map[string]interface{}
		rows, err := db.Query(`SELECT target_id, source FROM burning WHERE lobby_id = $1`, lobby)
		if err == nil {
			for rows.Next() {
				var id int
				var src string
				if rows.Scan(&id, &src) == nil {
					list = append(list, map[string]interface{}{"target_id": id, "name": creatureName(lobby, id), "source": src})
				}
			}
			rows.Close()
		}
		writeJSONStatus(w, 200, map[string]interface{}{"burning": list})
		return
	}
	var req struct {
		TargetID int    `json:"target_id"`
		Burning  *bool  `json:"burning"`
		Source   string `json:"source"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.TargetID == 0 || req.Burning == nil {
		dmBadRequest(w, "target_and_burning_required", "Send target_id and burning (true to set it on fire, false to put it out).")
		return
	}
	name := creatureName(lobby, req.TargetID)
	var result string
	if *req.Burning {
		startBurning(lobby, req.TargetID, req.Source)
		result = fmt.Sprintf("🔥 %s is burning: 1d4 Fire damage at the start of each of its turns until the fire is put out.", name)
	} else if stopBurning(lobby, req.TargetID) {
		result = fmt.Sprintf("The fire on %s goes out.", name)
	} else {
		result = fmt.Sprintf("%s wasn't burning.", name)
	}
	recordGMAction(db, lobby, "burning", result, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result})
}
