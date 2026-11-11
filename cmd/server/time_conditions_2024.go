package main

// Conditions after a fight, and the GM's own dice (F227, F228; Nov 10).
//
// F227: a condition that ends on a save "at the end of each of its turns" (Dust of Sneezing and Choking, Hold Person, Tasha's... any
// end_save record) was only rolled by the turn order. After combat/end no turns came, so the character stayed Incapacitated or
// Paralyzed for good (Morwen in s30). Outside combat time still runs in 6-second rounds: when the GM passes time (gm/pass-time), a rest
// or travel moves the clock, each round of that time rolls the save once until it succeeds (repeatSavesOverTime). combat/end lists the
// conditions still waiting (lingeringConditions) and my-turn tells the player.
//
// F228: POST /api/gm/roll lets the GM roll any dice for the story ("2d6+3", "1d20" with advantage), recorded in the campaign feed for
// the players, or secret (only the GM sees it).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// repeatSavesOverTime: each round of time passing outside combat rolls the repeated save of a condition on a character (end_save),
// until it succeeds or the condition's own duration ends. Conditions held by a caster's Concentration end with it instead.
func repeatSavesOverTime(lobbyID, rounds, onlyTarget int) []string {
	rows, err := db.Query(`SELECT id, target_id, condition, source, duration_rounds, end_save::text FROM condition_records
		WHERE lobby_id = $1 AND target_id > 0 AND end_save IS NOT NULL AND notes <> 'concentration' AND ($2 = 0 OR target_id = $2)`, lobbyID, onlyTarget)
	if err != nil {
		return nil
	}
	type rec struct {
		id, target, dur int
		cond, source    string
		save            conditionSave
	}
	var list []rec
	for rows.Next() {
		var x rec
		var raw string
		if rows.Scan(&x.id, &x.target, &x.cond, &x.source, &x.dur, &raw) == nil && json.Unmarshal([]byte(raw), &x.save) == nil && x.save.DC > 0 {
			list = append(list, x)
		}
	}
	rows.Close()
	var out []string
	touched := map[int]bool{}
	for _, x := range list {
		tries := rounds
		if x.dur > 0 && x.dur < tries {
			tries = x.dur
		}
		for i := 1; i <= tries; i++ {
			bonus, autoFail, dis := saveBonusFor(lobbyID, x.target, x.save.Ability)
			roll := game.RollDie(20)
			if dis {
				if b := game.RollDie(20); b < roll {
					roll = b
				}
			}
			if !autoFail && roll+bonus >= x.save.DC {
				db.Exec(`DELETE FROM condition_records WHERE id = $1`, x.id)
				touched[x.target] = true
				src := ""
				if x.source != "" {
					src = " (" + x.source + ")"
				}
				out = append(out, fmt.Sprintf("%s succeeds on the %s save (%d vs DC %d) after %d round(s): no longer %s%s.",
					getCharacterName(x.target), titleCase(x.save.Ability), roll+bonus, x.save.DC, i, x.cond, src))
				break
			}
		}
	}
	for t := range touched {
		syncListFromRecords(t)
	}
	return out
}

// lingeringConditions: the conditions on the fight's characters that end on a repeated save, for the GM at combat/end.
func lingeringConditions(lobbyID int) []string {
	rows, err := db.Query(`SELECT target_id, condition, source, end_save::text FROM condition_records
		WHERE lobby_id = $1 AND target_id > 0 AND end_save IS NOT NULL AND notes <> 'concentration' ORDER BY target_id`, lobbyID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t int
		var cond, src, raw string
		var s conditionSave
		if rows.Scan(&t, &cond, &src, &raw) == nil && json.Unmarshal([]byte(raw), &s) == nil {
			out = append(out, fmt.Sprintf("%s is %s (%s): a DC %d %s save at the end of each of its turns ends it", getCharacterName(t), cond, src, s.DC, titleCase(s.Ability)))
		}
	}
	return out
}

// conditionTimeNote: my-turn outside combat, for a character with such a condition.
func conditionTimeNote(charID int) string {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM condition_records WHERE target_id = $1 AND end_save IS NOT NULL AND notes <> 'concentration'`, charID).Scan(&n)
	if n == 0 {
		return ""
	}
	return "A condition on you ends on a repeated save at the end of each of your turns. Outside combat each 6 seconds is a turn: the save is rolled when the GM lets time pass (POST /api/gm/pass-time) or you rest."
}

var gmDiceRe = regexp.MustCompile(`^(\d{0,3})d(\d{1,3})\s*([+-]\s*\d{1,4})?$`)

// handleGMRoll godoc
// @Summary The GM rolls dice for the story
// @Description Roll any dice the GM chooses to decide something in the story: {"campaign_id": N, "dice": "2d6+3", "label": "Does the bridge hold?", "advantage": true, "secret": false}. Advantage or disadvantage apply to a single d20. The roll is recorded in the campaign feed for the players unless secret is true (then only the GM sees the reply). For a character's ability check or saving throw use POST /api/gm/skill-check instead (it adds the character's bonuses).
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{campaign_id=int,dice=string,label=string,advantage=bool,disadvantage=bool,secret=bool} true "The roll"
// @Success 200 {object} map[string]interface{} "The rolls and the total"
// @Failure 400 {object} map[string]interface{} "Bad dice"
// @Router /gm/roll [post]
func handleGMRoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Dice         string `json:"dice"`
		Label        string `json:"label"`
		Advantage    bool   `json:"advantage"`
		Disadvantage bool   `json:"disadvantage"`
		Secret       bool   `json:"secret"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	d := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(req.Dice), " ", ""))
	if d == "" {
		d = "1d20"
	}
	m := gmDiceRe.FindStringSubmatch(d)
	if m == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "bad_dice", "message": "Dice like 1d20, 2d6+3 or d100 (up to 100 dice of up to 100 sides)."})
		return
	}
	count, sides := 1, 0
	if m[1] != "" {
		count, _ = strconv.Atoi(m[1])
	}
	sides, _ = strconv.Atoi(m[2])
	mod := 0
	if m[3] != "" {
		mod, _ = strconv.Atoi(strings.ReplaceAll(m[3], "+", ""))
	}
	if count < 1 || count > 100 || sides < 2 || sides > 100 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "bad_dice", "message": "1 to 100 dice of 2 to 100 sides."})
		return
	}
	var rolls []int
	total := 0
	how := ""
	if count == 1 && sides == 20 && req.Advantage != req.Disadvantage {
		var pick, a, b int
		if req.Advantage {
			pick, a, b = game.RollWithAdvantage()
			how = " with Advantage"
		} else {
			pick, a, b = game.RollWithDisadvantage()
			how = " with Disadvantage"
		}
		rolls, total = []int{a, b}, pick
	} else {
		rolls, total = game.RollDice(count, sides)
	}
	total += mod
	label := strings.TrimSpace(req.Label)
	text := fmt.Sprintf("🎲 The GM rolls %s%s: %v → %d", d, how, rolls, total)
	if label != "" {
		text = fmt.Sprintf("🎲 %s: the GM rolls %s%s: %v → %d", label, d, how, rolls, total)
	}
	if !req.Secret {
		recordGMAction(db, campaignID, "gm_roll", text, fmt.Sprint(total))
	}
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "dice": d, "rolls": rolls, "modifier": mod, "total": total, "secret": req.Secret, "result": text})
}
