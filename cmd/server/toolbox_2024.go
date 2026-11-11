package main

// The GM's tools from the SRD 5.2.1 "Gameplay Toolbox":
//	POST /api/gm/apply-poison      the 14 sample poisons: the saving throw, damage and conditions
//	POST /api/gm/apply-contagion   Cackle Fever, Sewer Plague, Sight Rot: infection
//	POST /api/gm/contagion-save    the contagion's onset, its "Fighting the Contagion" save, or a cure
//	POST /api/gm/mental-stress     Psychic damage and a prolonged effect
//	POST /api/gm/trap              the 8 example traps, scaled by level
//	POST /api/gm/environmental-hazard  deep water, extreme cold and heat, frigid water, slippery ice (the others are text)
//	POST /api/gm/morale-check      "Fight or Flight": a DC 10 Wisdom save before Initiative
// The rule text comes word for word from data/srd521_toolbox.json (tools/extractors/extract_toolbox.py); the numbers are coded here.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

//go:embed data/srd521_toolbox.json
var toolboxJSON []byte

type toolboxItem struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind,omitempty"`
	Severity   string   `json:"severity,omitempty"`
	PriceGP    int      `json:"price_gp,omitempty"`
	Paragraphs []string `json:"paragraphs"`
}

var toolbox map[string]map[string]toolboxItem

func toolboxSection(sec string) map[string]toolboxItem {
	if toolbox == nil {
		json.Unmarshal(toolboxJSON, &toolbox)
	}
	return toolbox[sec]
}

func toolboxText(sec, key string) string {
	return strings.Join(toolboxSection(sec)[key].Paragraphs, " ")
}

func toolboxKeys(sec string) []string {
	var out []string
	for k := range toolboxSection(sec) {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toolboxKey(s string) string {
	return strings.Trim(strings.NewReplacer("'", "", " ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s))), "_")
}

// ---- shared helpers for monsters (negative ids) and characters

func damageAny(lobbyID, id, dmg int, dtype string) string {
	if dmg <= 0 {
		return "takes no damage"
	}
	if id < 0 {
		text, _ := damageMonster(lobbyID, id, dmg, dtype) // "<name> takes N <type> damage. <name> looks ..."
		return strings.TrimRight(strings.TrimSpace(text), ".")
	}
	return damageCharacter(id, dmg, dtype)
}

// applyCondition puts a condition on a monster or a character, with a duration in rounds (0 = until removed) and an optional
// repeated save ("end_of_turn").
func applyCondition(lobbyID, id int, cond, source string, rounds int, saveAbility string, dc int) {
	addMonsterCondition(lobbyID, id, cond, source, rounds, saveAbility, dc) // the record (a character's list follows it)
}

// addExhaustion changes a character's Exhaustion level (2024: a creature dies at level 6).
func addExhaustion(charID, n int) string {
	var level int
	db.QueryRow(`UPDATE characters SET exhaustion_level = GREATEST(0, LEAST(6, COALESCE(exhaustion_level,0) + $1)) WHERE id = $2 RETURNING exhaustion_level`, n, charID).Scan(&level)
	if level >= 6 {
		db.Exec(`UPDATE characters SET hp = 0, is_dead = true WHERE id = $1`, charID)
		return "Exhaustion level 6: the creature dies."
	}
	return fmt.Sprintf("Exhaustion level %d.", level)
}

// failWord is "failure", or "automatic failure" when a condition (Paralyzed, Stunned, Unconscious ...) made a high roll fail.
func failWord(total, dc int) string {
	if total >= dc {
		return "automatic failure (a condition makes this save fail)"
	}
	return "failure"
}

func targetIDsOf(single int, list []int) []int {
	if len(list) > 0 {
		return list
	}
	if single != 0 {
		return []int{single}
	}
	return nil
}

func dmBadRequest(w http.ResponseWriter, code, msg string) {
	writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg})
}

// ---- poisons

type poisonRule struct {
	DC        int
	Dice      string
	Half      bool     // half damage on a success
	Conds     []string // on a failure
	Rounds    int      // duration of the conditions (10 = 1 minute); 0 = until removed
	EndSave   bool     // the save repeats at the end of each of its turns
	RollHours string   // a rolled duration in hours (Torpor)
}

var poisonRules = map[string]poisonRule{
	"assassins_blood":    {DC: 10, Dice: "1d12", Half: true, Conds: []string{"poisoned"}, Rounds: 14400},
	"burnt_othur_fumes":  {DC: 13, Dice: "3d6"},
	"crawler_mucus":      {DC: 13, Conds: []string{"poisoned", "paralyzed"}, Rounds: 10, EndSave: true},
	"essence_of_ether":   {DC: 15, Conds: []string{"poisoned", "unconscious"}, Rounds: 4800},
	"malice":             {DC: 15, Conds: []string{"poisoned", "blinded"}, Rounds: 600},
	"midnight_tears":     {DC: 17, Dice: "9d6", Half: true},
	"oil_of_taggit":      {DC: 13, Conds: []string{"poisoned", "unconscious"}, Rounds: 14400},
	"pale_tincture":      {DC: 16, Dice: "1d6", Conds: []string{"poisoned"}},
	"purple_worm_poison": {DC: 21, Dice: "10d6", Half: true},
	"serpent_venom":      {DC: 11, Dice: "3d6", Half: true},
	"spiders_sting":      {DC: 13, Conds: []string{"poisoned"}, Rounds: 600},
	"torpor":             {DC: 15, Conds: []string{"poisoned"}, RollHours: "4d6"},
	"truth_serum":        {DC: 11, Conds: []string{"poisoned"}, Rounds: 600},
	"wyvern_poison":      {DC: 14, Dice: "7d6", Half: true},
}

// @Summary Expose creatures to a poison
// @Description One of the 14 sample poisons of the SRD 5.2.1 (assassins_blood, burnt_othur_fumes, crawler_mucus, essence_of_ether, malice, midnight_tears, oil_of_taggit, pale_tincture, purple_worm_poison, serpent_venom, spiders_sting, torpor, truth_serum, wyvern_poison). Each target makes the Constitution save; damage and conditions are applied. repeat=true is a repeated save that deals the poison's later damage (Burnt Othur Fumes 1d6 at the start of each turn, Pale Tincture 1d6 every 24 hours). Midnight Tears is resolved when midnight strikes.
// @Tags GM
// @Param request body object{poison=string,target_id=int,target_ids=[]int,repeat=bool} true "Poison and targets (characters or negative monster ids)"
// @Router /gm/apply-poison [post]
func handleGMApplyPoison2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Poison      string `json:"poison"`
		TargetID    int    `json:"target_id"`
		TargetIDs   []int  `json:"target_ids"`
		CharacterID int    `json:"character_id"`
		Repeat      bool   `json:"repeat"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	key := toolboxKey(req.Poison)
	rule, okRule := poisonRules[key]
	if !okRule {
		dmBadRequest(w, "unknown_poison", "poison must be one of: "+strings.Join(toolboxKeys("poisons"), ", "))
		return
	}
	if req.TargetID == 0 {
		req.TargetID = req.CharacterID
	}
	ids := targetIDsOf(req.TargetID, req.TargetIDs)
	if len(ids) == 0 {
		dmBadRequest(w, "target_required", "Send target_id or target_ids (characters, or negative monster ids).")
		return
	}
	item := toolboxSection("poisons")[key]
	dice := rule.Dice
	if req.Repeat && (key == "burnt_othur_fumes" || key == "pale_tincture") {
		dice = "1d6"
	}
	var results []string
	for _, id := range ids {
		name := creatureName(lobby, id)
		poisonSaveCtx.Store(id, true) // a save against being Poisoned: Protection from Poison, Dwarven Resilience
		saved, total := creatureSave(lobby, id, "con", rule.DC)
		advNote := ""
		if adv, why := buffSaveAdvantage(lobby, id, "con"); adv {
			advNote = " (Advantage: " + why + ")"
		}
		poisonSaveCtx.Delete(id)
		line := fmt.Sprintf("%s: Constitution save %d%s vs DC %d, ", name, total, advNote, rule.DC)
		dmg := 0
		if dice != "" {
			dmg = game.RollDamage(dice, false)
		}
		if saved {
			line += "success"
			if rule.Half && dmg > 0 {
				line += "; " + damageAny(lobby, id, dmg/2, "poison")
			}
		} else {
			line += failWord(total, rule.DC)
			if dmg > 0 {
				line += "; " + damageAny(lobby, id, dmg, "poison")
			}
			conds, rounds := rule.Conds, rule.Rounds
			if key == "spiders_sting" && total <= rule.DC-5 {
				conds = append(conds, "unconscious") // failed by 5 or more
			}
			if rule.RollHours != "" {
				rounds = game.RollDamage(rule.RollHours, false) * 600
				line += fmt.Sprintf("; Poisoned for %d hours", rounds/600)
			}
			end := ""
			if rule.EndSave {
				end = "con"
			}
			if !req.Repeat {
				for _, c := range conds {
					applyCondition(lobby, id, c, item.Name, rounds, end, rule.DC)
				}
				if len(conds) > 0 {
					line += "; " + strings.Join(conds, ", ")
				}
			}
		}
		results = append(results, line+".")
	}
	recordGMAction(db, lobby, "poison", item.Name, strings.Join(results, " "))
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "poison": item.Name, "type": item.Kind, "rule": toolboxText("poisons", key), "results": results})
}

// ---- magical contagions

var contagionInfectDC = map[string]int{"cackle_fever": 10, "sewer_plague": 11, "sight_rot": 15}

func ensureContagionTable() {
	if db != nil {
		db.Exec(`CREATE TABLE IF NOT EXISTS contagions (id SERIAL PRIMARY KEY, lobby_id INTEGER NOT NULL, target_id INTEGER NOT NULL,
			contagion TEXT NOT NULL, successes INTEGER NOT NULL DEFAULT 0, onset BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ DEFAULT now())`)
	}
}

// @Summary Expose a creature to a magical contagion
// @Description Cackle Fever (DC 10), Sewer Plague (DC 11) or Sight Rot (DC 15): the target makes the Constitution save (dc overrides it) or becomes infected. Sight Rot blinds at once; the others take effect 1d4 days later (POST /api/gm/contagion-save with onset=true).
// @Tags GM
// @Param request body object{contagion=string,target_id=int,dc=int} true "Contagion and target"
// @Router /gm/apply-contagion [post]
func handleGMApplyContagion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Contagion   string `json:"contagion"`
		TargetID    int    `json:"target_id"`
		CharacterID int    `json:"character_id"`
		DC          int    `json:"dc"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	key := toolboxKey(req.Contagion)
	dc, known := contagionInfectDC[key]
	if !known {
		dmBadRequest(w, "unknown_contagion", "contagion must be one of: cackle_fever, sewer_plague, sight_rot")
		return
	}
	if req.TargetID == 0 {
		req.TargetID = req.CharacterID
	}
	if req.TargetID == 0 {
		dmBadRequest(w, "target_required", "Send target_id.")
		return
	}
	if req.DC > 0 {
		dc = req.DC
	}
	ensureContagionTable()
	name := creatureName(lobby, req.TargetID)
	saved, total := creatureSave(lobby, req.TargetID, "con", dc)
	out := map[string]interface{}{"success": true, "contagion": toolboxSection("contagions")[key].Name, "rule": toolboxText("contagions", key), "save": total, "dc": dc}
	if saved {
		out["result"] = fmt.Sprintf("%s succeeds on the Constitution save (%d vs DC %d) and is not infected.", name, total, dc)
	} else {
		db.Exec(`INSERT INTO contagions (lobby_id, target_id, contagion, onset) VALUES ($1,$2,$3,$4)`, lobby, req.TargetID, key, key == "sight_rot")
		res := fmt.Sprintf("%s fails the Constitution save (%d vs DC %d) and is infected.", name, total, dc)
		if key == "sight_rot" {
			applyCondition(lobby, req.TargetID, "blinded", "Sight Rot", 0, "", 0)
			res += " It has the Blinded condition until the contagion ends."
		} else {
			res += fmt.Sprintf(" The effects begin %d days from now (1d4): then POST /api/gm/contagion-save with onset=true.", game.RollDie(4))
		}
		out["result"] = res
	}
	recordGMAction(db, lobby, "contagion", fmt.Sprint(out["contagion"]), fmt.Sprint(out["result"]))
	writeJSONStatus(w, 200, out)
}

// @Summary A contagion's onset, its daily or Long Rest save, or its cure
// @Description onset=true applies the first effects (Cackle Fever and Sewer Plague: 1 Exhaustion level). Otherwise it is "Fighting the Contagion": Cackle Fever DC 13 at the end of each Long Rest (three successes end it), Sewer Plague DC 11 daily at dawn (failure +1 Exhaustion, success -1, ends at 0). cured=true ends any contagion (Sight Rot: Heal or Lesser Restoration, or 72 hours of ointment).
// @Tags GM
// @Param request body object{contagion=string,target_id=int,onset=bool,cured=bool} true "Contagion and target"
// @Router /gm/contagion-save [post]
func handleGMContagionSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Contagion   string `json:"contagion"`
		TargetID    int    `json:"target_id"`
		CharacterID int    `json:"character_id"`
		Onset       bool   `json:"onset"`
		Cured       bool   `json:"cured"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.TargetID == 0 {
		req.TargetID = req.CharacterID
	}
	key := toolboxKey(req.Contagion)
	ensureContagionTable()
	var id, successes int
	var onset bool
	if db.QueryRow(`SELECT id, successes, onset FROM contagions WHERE lobby_id = $1 AND target_id = $2 AND contagion = $3 ORDER BY id DESC LIMIT 1`, lobby, req.TargetID, key).Scan(&id, &successes, &onset) != nil {
		writeJSONStatus(w, 404, map[string]interface{}{"error": "not_infected", "message": "That creature has no record of this contagion (POST /api/gm/apply-contagion first)."})
		return
	}
	name := creatureName(lobby, req.TargetID)
	end := func(why string) string {
		db.Exec(`DELETE FROM contagions WHERE id = $1`, id)
		if key == "sight_rot" {
			db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'blinded' AND source = 'Sight Rot'`, lobby, req.TargetID)
			if req.TargetID > 0 {
				syncListFromRecords(req.TargetID)
			}
		}
		if key == "cackle_fever" && onset && req.TargetID > 0 {
			addExhaustion(req.TargetID, -1) // the Fever's Exhaustion level lasts until the contagion ends
		}
		return fmt.Sprintf("%s: the contagion ends (%s).", name, why)
	}
	var result string
	switch {
	case req.Cured:
		result = end("cured")
	case req.Onset:
		db.Exec(`UPDATE contagions SET onset = true WHERE id = $1`, id)
		if req.TargetID > 0 && key != "sight_rot" {
			result = fmt.Sprintf("%s: the contagion takes hold. %s", name, addExhaustion(req.TargetID, 1))
		} else {
			result = name + ": the contagion takes hold."
		}
	case key == "cackle_fever":
		saved, total := creatureSave(lobby, req.TargetID, "con", 13)
		if saved {
			successes++
			db.Exec(`UPDATE contagions SET successes = $1 WHERE id = $2`, successes, id)
			result = fmt.Sprintf("%s succeeds (%d vs DC 13): %d of 3 successes.", name, total, successes)
			if successes >= 3 {
				result += " " + end("three successful saves; immune to Cackle Fever for 1 year")
			}
		} else {
			result = fmt.Sprintf("%s fails (%d vs DC 13): %d of 3 successes.", name, total, successes)
		}
	case key == "sewer_plague":
		saved, total := creatureSave(lobby, req.TargetID, "con", 11)
		if req.TargetID <= 0 {
			result = fmt.Sprintf("%s rolls %d vs DC 11.", name, total)
			break
		}
		if saved {
			result = fmt.Sprintf("%s succeeds (%d vs DC 11). %s", name, total, addExhaustion(req.TargetID, -1))
			if exhaustionLevelOf(req.TargetID) == 0 {
				result += " " + end("Exhaustion reduced to 0")
			}
		} else {
			result = fmt.Sprintf("%s fails (%d vs DC 11). %s", name, total, addExhaustion(req.TargetID, 1))
		}
	default:
		result = "Sight Rot has no daily save: it ends with Heal or Lesser Restoration, or 72 hours of ointment (cured=true)."
	}
	recordGMAction(db, lobby, "contagion", key, result)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": result, "rule": toolboxText("contagions", key)})
}

// ---- fear and mental stress

// @Summary Mental stress: Psychic damage and a prolonged effect
// @Description SRD 5.2.1 Fear and Mental Stress. Each target makes a saving throw (ability wis by default, or int or cha) against dc, taking the Psychic damage on a failure (half on a success if half_on_success). A failure can add a prolonged effect: short (condition frightened, incapacitated or stunned for 1d10 minutes), long (Disadvantage on some or all ability checks for 1d10 x 10 hours) or indefinite (until Greater Restoration). Plain fear is the Frightened condition through POST /api/gm/conditions with an end_save.
// @Tags GM
// @Param request body object{target_id=int,target_ids=[]int,dc=int,ability=string,damage=string,half_on_success=bool,prolonged=string,condition=string,checks=string} true "Mental stress"
// @Router /gm/mental-stress [post]
func handleGMMentalStress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		TargetID      int    `json:"target_id"`
		TargetIDs     []int  `json:"target_ids"`
		CharacterID   int    `json:"character_id"`
		DC            int    `json:"dc"`
		Ability       string `json:"ability"`
		Damage        string `json:"damage"`
		HalfOnSuccess bool   `json:"half_on_success"`
		Prolonged     string `json:"prolonged"`
		Condition     string `json:"condition"`
		Checks        string `json:"checks"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.TargetID == 0 {
		req.TargetID = req.CharacterID
	}
	ids := targetIDsOf(req.TargetID, req.TargetIDs)
	if len(ids) == 0 || req.DC <= 0 {
		dmBadRequest(w, "target_and_dc_required", "Send target_id or target_ids and dc (the sample table: DC 10 1d6, DC 15 3d6, DC 20 9d6).")
		return
	}
	ab := strings.ToLower(req.Ability)
	if ab == "" {
		ab = "wis"
	}
	ab = ab[:3]
	if ab != "wis" && ab != "int" && ab != "cha" {
		dmBadRequest(w, "invalid_ability", "ability must be wis, int or cha")
		return
	}
	if req.Damage == "" {
		req.Damage = map[bool]string{true: "9d6", false: map[bool]string{true: "3d6", false: "1d6"}[req.DC >= 15]}[req.DC >= 20]
	}
	cond := strings.ToLower(req.Condition)
	if req.Prolonged == "short" && cond != "frightened" && cond != "incapacitated" && cond != "stunned" {
		cond = "frightened"
	}
	var results []string
	for _, id := range ids {
		name := creatureName(lobby, id)
		saved, total := creatureSave(lobby, id, ab, req.DC)
		dmg := game.RollDamage(req.Damage, false)
		line := fmt.Sprintf("%s: %s save %d vs DC %d, ", name, strings.ToUpper(ab), total, req.DC)
		if saved {
			line += "success"
			if req.HalfOnSuccess {
				line += "; " + damageAny(lobby, id, dmg/2, "psychic")
			}
		} else {
			line += failWord(total, req.DC) + "; " + damageAny(lobby, id, dmg, "psychic")
			switch req.Prolonged {
			case "short":
				mins := game.RollDie(10)
				applyCondition(lobby, id, cond, "Mental stress", mins*10, "", 0)
				line += fmt.Sprintf("; %s for %d minutes (Calm Emotions suppresses it, Lesser Restoration removes it)", cond, mins)
			case "long":
				hours := game.RollDie(10) * 10
				what := req.Checks
				if what == "" {
					what = "some or all ability checks"
				}
				line += fmt.Sprintf("; Disadvantage on %s for %d hours (Calm Emotions suppresses it, Lesser Restoration removes it)", what, hours)
			case "indefinite":
				line += "; an indefinite long-term effect: Disadvantage on ability checks until removed by Greater Restoration (Calm Emotions suppresses it)"
			}
		}
		results = append(results, line+".")
	}
	recordGMAction(db, lobby, "mental_stress", "Mental stress", strings.Join(results, " "))
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "results": results, "rule": toolboxText("fear", "mental_stress_effects") + " " + toolboxText("fear", "prolonged_effects")})
}

// ---- traps

type trapBand struct {
	DC    int
	Dice  string
	Extra string // second damage (Spiked Pit piercing)
}

type trapRule struct {
	Save   string // "" = no save
	DType  string
	Extra  string // type of the second damage
	Half   bool
	Cond   string
	Bands  [4]trapBand // levels 1-4, 5-10, 11-16, 17-20
	MinLvl int
}

var trapRules = map[string]trapRule{
	"collapsing_roof":     {Save: "dex", DType: "bludgeoning", Half: true, Bands: [4]trapBand{{13, "2d10", ""}, {15, "4d10", ""}, {17, "10d10", ""}, {19, "18d10", ""}}},
	"falling_net":         {Save: "dex", Cond: "restrained", Bands: [4]trapBand{{10, "", ""}, {12, "", ""}, {14, "", ""}, {16, "", ""}}},
	"fire_casting_statue": {Save: "dex", DType: "fire", Half: true, Bands: [4]trapBand{{15, "2d10", ""}, {15, "4d10", ""}, {15, "10d10", ""}, {15, "18d10", ""}}},
	"hidden_pit":          {DType: "bludgeoning", Bands: [4]trapBand{{0, "1d6", ""}, {0, "3d6", ""}, {0, "6d6", ""}, {0, "12d6", ""}}},
	"poisoned_darts":      {Save: "dex", DType: "poison", Bands: [4]trapBand{{13, "1d6", ""}, {13, "2d6", ""}, {13, "4d6", ""}, {13, "7d6", ""}}},
	"poisoned_needle":     {Save: "con", DType: "poison", Half: true, Cond: "poisoned", Bands: [4]trapBand{{11, "1d10", ""}, {13, "2d10", ""}, {15, "4d10", ""}, {17, "10d10", ""}}},
	"rolling_stone":       {Save: "dex", DType: "bludgeoning", Cond: "prone", Bands: [4]trapBand{{15, "10d10", ""}, {15, "10d10", ""}, {15, "10d10", ""}, {15, "10d10", ""}}, MinLvl: 11},
	"spiked_pit":          {DType: "bludgeoning", Extra: "piercing", Bands: [4]trapBand{{0, "1d6", "2d8"}, {0, "3d6", "3d8"}, {0, "6d6", "8d8"}, {0, "12d6", "13d8"}}},
}

func levelBand(level int) int {
	switch {
	case level >= 17:
		return 3
	case level >= 11:
		return 2
	case level >= 5:
		return 1
	}
	return 0
}

// @Summary Spring a trap
// @Description One of the 8 example traps of the SRD 5.2.1 (collapsing_roof, falling_net, fire_casting_statue, hidden_pit, poisoned_darts, poisoned_needle, rolling_stone, spiked_pit), scaled to level (1-20, default 1) with its "At Higher Levels" table. Each target makes the trap's saving throw (pits have none); damage and conditions are applied. The response has the trap's full text, with how to detect and disarm it.
// @Tags GM
// @Param request body object{trap=string,target_id=int,target_ids=[]int,level=int} true "Trap and targets"
// @Router /gm/trap [post]
func handleGMTrap2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Trap        string `json:"trap"`
		TargetID    int    `json:"target_id"`
		TargetIDs   []int  `json:"target_ids"`
		CharacterID int    `json:"character_id"`
		Level       int    `json:"level"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	key := toolboxKey(req.Trap)
	rule, known := trapRules[key]
	if !known {
		dmBadRequest(w, "unknown_trap", "trap must be one of: "+strings.Join(toolboxKeys("traps"), ", "))
		return
	}
	if req.TargetID == 0 {
		req.TargetID = req.CharacterID
	}
	ids := targetIDsOf(req.TargetID, req.TargetIDs)
	if len(ids) == 0 {
		dmBadRequest(w, "target_required", "Send target_id or target_ids.")
		return
	}
	if req.Level <= 0 {
		req.Level = 1
	}
	if req.Level < rule.MinLvl {
		req.Level = rule.MinLvl
	}
	band := rule.Bands[levelBand(req.Level)]
	item := toolboxSection("traps")[key]
	var results []string
	for _, id := range ids {
		name := creatureName(lobby, id)
		saved, total := false, 0
		line := name + ": "
		if rule.Save != "" {
			saved, total = creatureSave(lobby, id, rule.Save, band.DC)
			if key == "falling_net" && id < 0 {
				if sb := monsterStatBlock(monsterKeyOfCombatant(lobby, id)); sb != nil && (sb["size"] == "Huge" || sb["size"] == "Gargantuan") {
					saved = true // the target succeeds automatically if it's Huge or larger
				}
			}
			line += fmt.Sprintf("%s save %d vs DC %d, %s", strings.ToUpper(rule.Save), total, band.DC, map[bool]string{true: "success", false: failWord(total, band.DC)}[saved])
		} else {
			line += "falls into the pit"
		}
		dmg := 0
		if band.Dice != "" {
			dmg = game.RollDamage(band.Dice, false)
			if key == "poisoned_darts" {
				darts := game.RollDie(3)
				dmg = 0
				for i := 0; i < darts; i++ {
					dmg += game.RollDamage(band.Dice, false)
				}
				line += fmt.Sprintf(" (%d darts)", darts)
			}
		}
		switch {
		case !saved && dmg > 0:
			line += "; " + damageAny(lobby, id, dmg, rule.DType)
		case saved && rule.Half && dmg > 0:
			line += "; " + damageAny(lobby, id, dmg/2, rule.DType)
		}
		if band.Extra != "" {
			line += "; " + damageAny(lobby, id, game.RollDamage(band.Extra, false), rule.Extra)
		}
		if !saved && rule.Cond != "" {
			rounds := 0
			if rule.Cond == "poisoned" {
				rounds = 600 // 1 hour
			}
			applyCondition(lobby, id, rule.Cond, item.Name, rounds, "", 0)
			line += "; " + rule.Cond
			if key == "falling_net" {
				line += fmt.Sprintf(" until it escapes (an action: DC %d Strength (Athletics) check)", band.DC)
			}
		}
		results = append(results, line+".")
	}
	recordGMAction(db, lobby, "trap", item.Name, strings.Join(results, " "))
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "trap": item.Name, "severity": item.Severity, "level": req.Level, "results": results, "rule": toolboxText("traps", key)})
}

func monsterKeyOfCombatant(lobbyID, id int) string {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			return sprintAny(e["monster_key"])
		}
	}
	return ""
}

// ---- environmental effects

// @Summary Environmental effects
// @Description SRD 5.2.1 Environmental Effects. deep_water (each hour of swimming more than 100 feet deep without a Swim Speed: DC 10 Constitution save or 1 Exhaustion level), extreme_cold (each hour at 0 F or lower: DC 10), extreme_heat (each hour at 100 F or higher without drinkable water: DC 5, +1 per further hour; Disadvantage in Medium or Heavy armor), frigid_water (each minute beyond the Constitution score: DC 10), slippery_ice (DC 10 Dexterity save or Prone). resistant=true is Resistance or Immunity to the relevant damage (Cold or Fire; automatic success) or a creature adapted to the water. heavy_precipitation, high_altitude, strong_wind and thin_ice return their rules (thin ice also rolls its weight tolerance). Rules Glossary hazards, once per day: dehydration (water less than half: 1 Exhaustion level), malnutrition (food less than half: DC 10 Constitution save or 1 Exhaustion level; after 5 days with no food: 1 level at the end of each day).
// @Tags GM
// @Param request body object{character_id=int,hazard=string,hours=int,minutes=int,medium_or_heavy_armor=bool,resistant=bool,swim_speed=bool,water=string,food=string,days_without_food=int} true "Hazard (also dehydration with water, malnutrition with food and days_without_food: full, half, less_than_half or none)"
// @Router /gm/environmental-hazard [post]
func handleGMEnvironmentalHazard2024(w http.ResponseWriter, r *http.Request) {
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
		Hazard      string `json:"hazard"`
		Hours       int    `json:"hours"`
		Minutes     int    `json:"minutes"`
		Armor       bool   `json:"medium_or_heavy_armor"`
		HeavyArmor  bool   `json:"heavy_armor"`
		Resistant   bool   `json:"resistant"`
		SwimSpeed   bool   `json:"swim_speed"`
		Water       string `json:"water"`             // dehydration: full, half, less_than_half, none
		Food        string `json:"food"`              // malnutrition: full, half, less_than_half, none
		DaysNoFood  int    `json:"days_without_food"` // malnutrition: whole days with no food
	}
	json.NewDecoder(r.Body).Decode(&req)
	key := toolboxKey(req.Hazard)
	if key == "dehydration" || key == "malnutrition" { // Rules Glossary hazards
		if req.CharacterID == 0 {
			dmBadRequest(w, "character_required", "Send character_id.")
			return
		}
		amount := req.Water
		if key == "malnutrition" {
			amount = req.Food
		}
		out := hungerThirstHazard(lobby, req.CharacterID, key, amount, req.DaysNoFood)
		recordGMAction(db, lobby, "environment", fmt.Sprint(out["hazard"]), fmt.Sprint(out["result"]))
		writeJSONStatus(w, 200, out)
		return
	}
	if _, known := toolboxSection("environment")[key]; !known {
		dmBadRequest(w, "unknown_hazard", "hazard must be one of: "+strings.Join(append(toolboxKeys("environment"), "dehydration", "malnutrition"), ", "))
		return
	}
	out := map[string]interface{}{"success": true, "hazard": toolboxSection("environment")[key].Name, "rule": toolboxText("environment", key)}
	id := req.CharacterID
	name := creatureName(lobby, id)
	saves := func(n int, dc func(i int) int, ability string, auto bool, dis bool) []string {
		var log []string
		for i := 0; i < n; i++ {
			d := dc(i)
			var dead bool
			db.QueryRow(`SELECT COALESCE(is_dead,false) FROM characters WHERE id = $1`, id).Scan(&dead)
			if dead {
				break
			}
			if auto {
				log = append(log, fmt.Sprintf("%d: automatic success", i+1))
				continue
			}
			saved, total := creatureSave(lobby, id, ability, d)
			if dis && saved { // Disadvantage: a second roll must also succeed
				saved, total = creatureSave(lobby, id, ability, d)
			}
			if saved {
				log = append(log, fmt.Sprintf("%d: %d vs DC %d, success", i+1, total, d))
			} else {
				log = append(log, fmt.Sprintf("%d: %d vs DC %d, failure: %s", i+1, total, d, addExhaustion(id, 1)))
			}
		}
		return log
	}
	if id == 0 && key != "thin_ice" && key != "heavy_precipitation" && key != "high_altitude" && key != "strong_wind" {
		dmBadRequest(w, "character_required", "Send character_id.")
		return
	}
	one := func(int) int { return 10 }
	switch key {
	case "deep_water":
		out["saves"] = saves(maxInt(req.Hours, 1), one, "con", req.SwimSpeed, false)
	case "extreme_cold":
		out["saves"] = saves(maxInt(req.Hours, 1), one, "con", req.Resistant, false)
	case "extreme_heat":
		out["saves"] = saves(maxInt(req.Hours, 1), func(i int) int { return 5 + i }, "con", req.Resistant, req.Armor || req.HeavyArmor)
	case "frigid_water":
		var con int
		db.QueryRow(`SELECT con FROM characters WHERE id = $1`, id).Scan(&con)
		extra := req.Minutes - con
		if extra <= 0 {
			out["result"] = fmt.Sprintf("%s can stay %d minutes in frigid water (its Constitution score) before suffering any ill effects.", name, con)
		} else {
			out["saves"] = saves(extra, one, "con", req.Resistant, false)
		}
	case "slippery_ice":
		saved, total := creatureSave(lobby, id, "dex", 10)
		if saved {
			out["result"] = fmt.Sprintf("%s: Dexterity save %d vs DC 10, success.", name, total)
		} else {
			applyCondition(lobby, id, "prone", "Slippery ice", 0, "", 0)
			out["result"] = fmt.Sprintf("%s: Dexterity save %d vs DC 10, failure: Prone.", name, total)
		}
	case "thin_ice":
		out["weight_tolerance_lb"] = game.RollDamage("3d10", false) * 10
	}
	recordGMAction(db, lobby, "environment", fmt.Sprint(out["hazard"]), fmt.Sprint(out["saves"], out["result"]))
	writeJSONStatus(w, 200, out)
}

// ---- morale

// @Summary Fight or Flight
// @Description When it isn't clear whether a creature is willing to fight, it makes a DC 10 Wisdom saving throw before Initiative is rolled (the GM can set the DC higher). On a failure it avoids the battle: it flees, surrenders or stays out of the fight.
// @Tags GM
// @Param request body object{target_id=int,target_ids=[]int,dc=int} true "Creatures (negative monster ids, or characters)"
// @Router /gm/morale-check [post]
func handleGMMoraleCheck2024(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		TargetID  int   `json:"target_id"`
		TargetIDs []int `json:"target_ids"`
		DC        int   `json:"dc"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	ids := targetIDsOf(req.TargetID, req.TargetIDs)
	if len(ids) == 0 {
		dmBadRequest(w, "target_required", "Send target_id or target_ids.")
		return
	}
	if req.DC < 10 {
		req.DC = 10
	}
	var results []string
	for _, id := range ids {
		saved, total := creatureSave(lobby, id, "wis", req.DC)
		verdict := "is willing to fight"
		if !saved {
			verdict = "is not willing to fight: it tries to avoid the battle (flees, surrenders or holds back)"
		}
		results = append(results, fmt.Sprintf("%s: Wisdom save %d vs DC %d, %s.", creatureName(lobby, id), total, req.DC, verdict))
	}
	recordGMAction(db, lobby, "morale", "Fight or Flight", strings.Join(results, " "))
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "results": results})
}
