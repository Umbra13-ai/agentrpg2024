package main

// More rules from the rules review (SRD 5.2.1 Rules Glossary and Character Creation): breaking objects (and doors), jumping, marching
// order, curses, and starting at a higher level.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func ensureMoreTables() {
	db.Exec(`CREATE TABLE IF NOT EXISTS campaign_objects (id SERIAL PRIMARY KEY, lobby_id INTEGER NOT NULL, name TEXT NOT NULL, ac INTEGER NOT NULL,
		hp INTEGER NOT NULL, max_hp INTEGER NOT NULL, threshold INTEGER DEFAULT 0, vulnerable TEXT DEFAULT '', resistant TEXT DEFAULT '', created_at TIMESTAMP DEFAULT NOW())`)
	db.Exec(`CREATE TABLE IF NOT EXISTS curses (id SERIAL PRIMARY KEY, lobby_id INTEGER NOT NULL, target_id INTEGER NOT NULL, description TEXT NOT NULL, source TEXT DEFAULT '')`)
	db.Exec(`ALTER TABLE lobbies ADD COLUMN IF NOT EXISTS marching_order JSONB DEFAULT '[]'`)
}

// ---- breaking objects

// The Object Armor Class table.
var objectAC = map[string]int{"cloth": 11, "paper": 11, "rope": 11, "crystal": 13, "glass": 13, "ice": 13, "wood": 15, "stone": 17,
	"iron": 19, "steel": 19, "mithral": 21, "adamantine": 23}

// The Object Hit Points table: fragile and resilient dice by size.
var objectHPDice = map[string][2]string{"tiny": {"1d4", "2d4"}, "small": {"1d6", "3d6"}, "medium": {"1d8", "4d8"}, "large": {"1d10", "5d10"}}

type campaignObject struct {
	ID, AC, HP, MaxHP, Threshold int
	Name, Vulnerable, Resistant  string
}

func loadObject(lobbyID, id int) (campaignObject, bool) {
	ensureMoreTables()
	var o campaignObject
	err := db.QueryRow(`SELECT id, name, ac, hp, max_hp, COALESCE(threshold,0), COALESCE(vulnerable,''), COALESCE(resistant,'') FROM campaign_objects WHERE id = $1 AND lobby_id = $2`, id, lobbyID).
		Scan(&o.ID, &o.Name, &o.AC, &o.HP, &o.MaxHP, &o.Threshold, &o.Vulnerable, &o.Resistant)
	return o, err == nil
}

// damageObject applies damage to an object: Immunity to Poison and Psychic, the object's Vulnerability or Resistance, and its damage
// threshold (damage below the threshold does nothing). It returns the text.
func damageObject(lobbyID int, o campaignObject, dmg int, dtype string) string {
	dtype = strings.ToLower(dtype)
	note := ""
	switch {
	case dtype == "poison" || dtype == "psychic":
		return fmt.Sprintf("%s is immune to %s damage.", o.Name, dtype)
	case dtype != "" && strings.Contains(strings.ToLower(o.Vulnerable), dtype):
		dmg *= 2
		note = " (vulnerable)"
	case dtype != "" && strings.Contains(strings.ToLower(o.Resistant), dtype):
		dmg /= 2
		note = " (resistant)"
	}
	if o.Threshold > 0 && dmg < o.Threshold {
		return fmt.Sprintf("%d damage doesn't get past %s's damage threshold of %d.", dmg, o.Name, o.Threshold)
	}
	hp := o.HP - dmg
	if hp < 0 {
		hp = 0
	}
	db.Exec(`UPDATE campaign_objects SET hp = $1 WHERE id = $2`, hp, o.ID)
	if hp == 0 {
		return fmt.Sprintf("%s takes %d %s damage%s and is destroyed!", o.Name, dmg, dtype, note)
	}
	return fmt.Sprintf("%s takes %d %s damage%s.", o.Name, dmg, dtype, note)
}

// resolveAttackOnObject compares a character's attack with an object's AC and damages it.
func resolveAttackOnObject(lobbyID, objectID int, o attackOutcome) string {
	obj, ok := loadObject(lobbyID, objectID)
	if !ok {
		return "That object isn't in this campaign (GET /api/gm/object lists them)."
	}
	if obj.HP <= 0 {
		return fmt.Sprintf("%s is already destroyed.", obj.Name)
	}
	if o.Fumble || (!o.Crit && o.Total < obj.AC) {
		return fmt.Sprintf("Attack with %s on %s: %d to hit. MISS.", o.Weapon, obj.Name, o.Total)
	}
	return fmt.Sprintf("Attack with %s on %s: %d to hit. HIT! %s", o.Weapon, obj.Name, o.Total, damageObject(lobbyID, obj, o.Damage, o.DamageType))
}

// @Summary Objects and doors (breaking objects)
// @Description SRD 5.2.1 Breaking Objects. POST {"name": "oak door", "substance": "wood", "size": "large", "resilient": true} creates an object with the Object Armor Class (cloth/paper/rope 11, crystal/glass/ice 13, wood 15, stone 17, iron/steel 19, mithral 21, adamantine 23) and Object Hit Points (fragile or resilient dice by size: tiny 1d4/2d4, small 1d6/3d6, medium 1d8/4d8, large 1d10/5d10); ac, hp, threshold (damage threshold), vulnerable and resistant (damage types) override or add. Players attack it with POST /api/action {"action":"attack","object_id": N}. POST {"object_id": N, "damage": 12, "damage_type": "fire"} damages it (Immunity to Poison and Psychic; damage below the threshold does nothing). POST {"object_id": N, "remove": true} removes it. GET ?campaign_id lists objects.
// @Tags GM
// @Param request body object{campaign_id=int,name=string,substance=string,size=string,resilient=bool,ac=int,hp=int,threshold=int,vulnerable=string,resistant=string,object_id=int,damage=int,damage_type=string,remove=bool} true "Object"
// @Router /gm/object [post]
func handleGMObject(w http.ResponseWriter, r *http.Request) {
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	ensureMoreTables()
	if r.Method == http.MethodGet {
		var list []map[string]interface{}
		rows, err := db.Query(`SELECT id, name, ac, hp, max_hp, COALESCE(threshold,0) FROM campaign_objects WHERE lobby_id = $1 ORDER BY id`, lobby)
		if err == nil {
			for rows.Next() {
				var id, ac, hp, max, th int
				var name string
				if rows.Scan(&id, &name, &ac, &hp, &max, &th) == nil {
					list = append(list, map[string]interface{}{"object_id": id, "name": name, "ac": ac, "hp": hp, "max_hp": max, "threshold": th})
				}
			}
			rows.Close()
		}
		writeJSONStatus(w, 200, map[string]interface{}{"objects": list})
		return
	}
	var req struct {
		Name       string `json:"name"`
		Substance  string `json:"substance"`
		Size       string `json:"size"`
		Resilient  bool   `json:"resilient"`
		AC         int    `json:"ac"`
		HP         int    `json:"hp"`
		Threshold  int    `json:"threshold"`
		Vulnerable string `json:"vulnerable"`
		Resistant  string `json:"resistant"`
		ObjectID   int    `json:"object_id"`
		Damage     int    `json:"damage"`
		DamageType string `json:"damage_type"`
		Remove     bool   `json:"remove"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ObjectID != 0 {
		obj, found := loadObject(lobby, req.ObjectID)
		if !found {
			dmBadRequest(w, "object_not_found", "No such object in this campaign.")
			return
		}
		if req.Remove {
			db.Exec(`DELETE FROM campaign_objects WHERE id = $1`, obj.ID)
			writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": obj.Name + " removed."})
			return
		}
		text := damageObject(lobby, obj, req.Damage, req.DamageType)
		recordGMAction(db, lobby, "object", text, text)
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": text})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		dmBadRequest(w, "name_required", "Send a name (and substance and size), or object_id to damage or remove one.")
		return
	}
	ac := req.AC
	if ac == 0 {
		ac = objectAC[strings.ToLower(strings.TrimSpace(req.Substance))]
	}
	if ac == 0 {
		ac = 15
	}
	hp := req.HP
	if hp == 0 {
		dice, okSize := objectHPDice[strings.ToLower(strings.TrimSpace(req.Size))]
		if !okSize {
			dice = objectHPDice["medium"]
		}
		d := dice[0]
		if req.Resilient {
			d = dice[1]
		}
		hp = game.RollDamage(d, false)
	}
	var id int
	db.QueryRow(`INSERT INTO campaign_objects (lobby_id, name, ac, hp, max_hp, threshold, vulnerable, resistant) VALUES ($1,$2,$3,$4,$4,$5,$6,$7) RETURNING id`,
		lobby, strings.TrimSpace(req.Name), ac, hp, req.Threshold, req.Vulnerable, req.Resistant).Scan(&id)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "object_id": id, "name": req.Name, "ac": ac, "hp": hp, "threshold": req.Threshold,
		"result": fmt.Sprintf("%s: AC %d, %d Hit Points. Players attack it with object_id %d.", req.Name, ac, hp, id)})
}

// ---- jumping

// jumpAction: a Long Jump covers up to the Strength score in feet with a 10-foot run-up (half standing); a High Jump 3 + Strength
// modifier feet (minimum 0; half standing). Each foot costs a foot of movement. Landing in Difficult Terrain: DC 10 Dexterity
// (Acrobatics) or Prone.
func jumpAction(charID int, description string) string {
	d := strings.ToLower(description)
	var str, dex, level int
	var moveLeft int
	var class string
	db.QueryRow(`SELECT str, dex, level, COALESCE(class,''), COALESCE(movement_remaining, 30) FROM characters WHERE id = $1`, charID).Scan(&str, &dex, &level, &class, &moveLeft)
	score, scoreNote := jumpScore(charID, str, dex) // Second-Story Work: a Thief's jumps can use Dexterity
	str = score
	if ok, note := jumpSpellJump(charID); ok { // the Jump spell: 30 feet for 10 feet of movement, once a turn (spells_more_2024.go)
		lobby := lobbyOfChar(charID)
		out := "🦘 Jump (the spell): you jump up to 30 feet for 10 feet of movement."
		if inActiveCombat(lobby) {
			cost := minInt(10, moveLeft)
			db.Exec(`UPDATE characters SET movement_remaining = GREATEST(0, COALESCE(movement_remaining,30) - $1) WHERE id = $2`, cost, charID)
			out += fmt.Sprintf(" (%d feet of movement left; the GM places your token.)", moveLeft-cost)
		}
		return out
	} else if note != "" {
		d += note // the spell's jump was used this turn: an ordinary jump
	}
	standing := strings.Contains(d, "standing") || strings.Contains(d, "no run")
	high := strings.Contains(d, "high") || strings.Contains(d, "up to") || strings.Contains(d, "upward") || strings.Contains(d, "vertical")
	var dist int
	kind := "Long Jump"
	if high {
		kind = "High Jump"
		dist = 3 + game.Modifier(str)
		if dist < 0 {
			dist = 0
		}
	} else {
		dist = str
	}
	how := "with a 10-foot run-up"
	if standing {
		dist /= 2
		how = "standing"
	}
	out := fmt.Sprintf("🦘 %s (%s)%s: up to %d feet; each foot costs a foot of movement.", kind, how, scoreNote, dist)
	if high {
		out += " You can reach up to the jump's height plus 1½ times your height."
	}
	lobby := lobbyOfChar(charID)
	var active bool
	db.QueryRow(`SELECT COALESCE(active,false) FROM combat_state WHERE lobby_id = $1`, lobby).Scan(&active)
	if active {
		want := dist
		for _, f := range strings.Fields(d) {
			if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(f, "ft"), "feet")); err == nil && n > 0 && n < want {
				want = n
			}
		}
		if want > moveLeft {
			want = moveLeft
		}
		db.Exec(`UPDATE characters SET movement_remaining = GREATEST(0, COALESCE(movement_remaining,30) - $1) WHERE id = $2`, want, charID)
		out += fmt.Sprintf(" You jump %d feet (%d feet of movement left).", want, moveLeft-want)
	}
	if !high && strings.Contains(d, "difficult") {
		mod, prof, _, _ := checkBonus(charID, class, level, "acrobatics", dex)
		roll := game.RollDie(20)
		if roll+mod+prof >= 10 {
			out += fmt.Sprintf(" Landing in Difficult Terrain: Dexterity (Acrobatics) %d vs DC 10, you keep your feet.", roll+mod+prof)
		} else {
			conds := getCharConditions(charID)
			conds = append(conds, "prone")
			b, _ := json.Marshal(conds)
			db.Exec(`UPDATE characters SET conditions = $1 WHERE id = $2`, b, charID)
			syncRecordsFromList(charID)
			out += fmt.Sprintf(" Landing in Difficult Terrain: Dexterity (Acrobatics) %d vs DC 10, you fall Prone.", roll+mod+prof)
		}
	}
	return out
}

// ---- marching order

func marchingOrderNames(lobbyID int) []string {
	ensureMoreTables()
	var raw []byte
	db.QueryRow(`SELECT COALESCE(marching_order,'[]') FROM lobbies WHERE id = $1`, lobbyID).Scan(&raw)
	var ids []int
	json.Unmarshal(raw, &ids)
	var names []string
	for _, id := range ids {
		var n string
		if db.QueryRow(`SELECT name FROM characters WHERE id = $1 AND lobby_id = $2`, id, lobbyID).Scan(&n) == nil {
			names = append(names, n)
		}
	}
	return names
}

// handleMarchingOrder godoc
// @Summary Marching order
// @Description The order the party travels in, front to back (it decides who meets a trap or an ambush first). GET shows it; POST {"order": [character ids, front first]} sets it (the GM or any player in the campaign). Travel reports show it.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Param request body object{order=[]int} false "Character ids, front first (POST)"
// @Router /campaigns/{id}/marching-order [get]
// @Router /campaigns/{id}/marching-order [post]
// handleMarchingOrder: GET /api/campaigns/{id}/marching-order shows the order; POST {"order": [character ids, front first]} sets it
// (the GM or any player in the campaign).
func handleMarchingOrder(w http.ResponseWriter, r *http.Request, campaignID int) {
	ensureMoreTables()
	if r.Method == http.MethodPost {
		agentID, err := getAgentFromAuth(r)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		var n int
		db.QueryRow(`SELECT (SELECT count(*) FROM lobbies WHERE id = $1 AND dm_id = $2) + (SELECT count(*) FROM characters WHERE lobby_id = $1 AND agent_id = $2)`, campaignID, agentID).Scan(&n)
		if n == 0 {
			writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_in_campaign"})
			return
		}
		var req struct {
			Order []int `json:"order"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		for _, id := range req.Order {
			var c int
			db.QueryRow(`SELECT count(*) FROM characters WHERE id = $1 AND lobby_id = $2`, id, campaignID).Scan(&c)
			if c == 0 {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_in_campaign", "message": fmt.Sprintf("Character %d isn't in this campaign.", id)})
				return
			}
		}
		b, _ := json.Marshal(req.Order)
		db.Exec(`UPDATE lobbies SET marching_order = $1 WHERE id = $2`, b, campaignID)
		note := "Marching order (front to back): " + strings.Join(marchingOrderNames(campaignID), ", ")
		recordGMAction(db, campaignID, "marching_order", note, note)
	}
	writeJSONStatus(w, 200, map[string]interface{}{"campaign_id": campaignID, "marching_order": marchingOrderNames(campaignID)})
}

// ---- curses

// @Summary Curses
// @Description SRD 5.2.1 Curses: the effect that confers a curse says what it does; Remove Curse and Greater Restoration end curses. POST {"target_id": 5, "description": "Can't remove the cursed ring; Disadvantage on Wisdom saves", "source": "Ring of ..."} curses a character (or a monster's negative id); {"curse_id": N, "remove": true} lifts one. GET ?campaign_id lists them. Casting Remove Curse or Greater Restoration on a character (target_id or target_ids) ends its curses automatically.
// @Tags GM
// @Param request body object{campaign_id=int,target_id=int,description=string,source=string,curse_id=int,remove=bool} true "Curse"
// @Router /gm/curse [post]
func handleGMCurse(w http.ResponseWriter, r *http.Request) {
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	ensureMoreTables()
	if r.Method == http.MethodGet {
		writeJSONStatus(w, 200, map[string]interface{}{"curses": cursesOf(lobby, 0)})
		return
	}
	var req struct {
		TargetID    int    `json:"target_id"`
		Description string `json:"description"`
		Source      string `json:"source"`
		CurseID     int    `json:"curse_id"`
		Remove      bool   `json:"remove"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Remove && req.CurseID != 0 {
		db.Exec(`DELETE FROM curses WHERE id = $1 AND lobby_id = $2`, req.CurseID, lobby)
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": "The curse is lifted."})
		return
	}
	if req.TargetID == 0 || strings.TrimSpace(req.Description) == "" {
		dmBadRequest(w, "target_and_description_required", "Send target_id and description (what the curse does), or curse_id with remove.")
		return
	}
	var id int
	db.QueryRow(`INSERT INTO curses (lobby_id, target_id, description, source) VALUES ($1,$2,$3,$4) RETURNING id`, lobby, req.TargetID, req.Description, req.Source).Scan(&id)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "curse_id": id, "result": fmt.Sprintf("%s is cursed: %s", creatureName(lobby, req.TargetID), req.Description)})
}

func cursesOf(lobbyID, targetID int) []map[string]interface{} {
	ensureMoreTables()
	q := `SELECT id, target_id, description, COALESCE(source,'') FROM curses WHERE lobby_id = $1`
	args := []interface{}{lobbyID}
	if targetID != 0 {
		q += ` AND target_id = $2`
		args = append(args, targetID)
	}
	var out []map[string]interface{}
	rows, err := db.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, t int
		var d, s string
		if rows.Scan(&id, &t, &d, &s) == nil {
			out = append(out, map[string]interface{}{"curse_id": id, "target_id": t, "target": creatureName(lobbyID, t), "description": d, "source": s})
		}
	}
	return out
}

// endCursesBySpell: Remove Curse and Greater Restoration end every curse on their targets.
func endCursesBySpell(lobbyID int, spellSlug string, targets []int) string {
	if spellSlug != "remove-curse" && spellSlug != "greater-restoration" || len(targets) == 0 {
		return ""
	}
	ensureMoreTables()
	var out []string
	for _, t := range targets {
		res, err := db.Exec(`DELETE FROM curses WHERE lobby_id = $1 AND target_id = $2`, lobbyID, t)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				out = append(out, fmt.Sprintf("The curse(s) on %s end.", creatureName(lobbyID, t)))
			}
		}
	}
	return strings.Join(out, " ")
}

// ---- starting at a higher level

// @Summary Start a character at a higher level
// @Description SRD 5.2.1 Starting at Higher Levels: the character goes from its level to the new one by the level-up rules (Hit Points, Ability Score Improvements, XP set to the level's minimum, the public "Ding!" note of choices), and gets the Starting Equipment at Higher Levels money: levels 5-10 500 GP + 1d10 x 25 GP, 11-16 5,000 GP + 1d10 x 250 GP, 17-20 20,000 GP + 1d10 x 250 GP. The reply gives the suggested magic items (2-4: 1 Common; 5-10: 1 Common, 1 Uncommon; 11-16: 2 Common, 3 Uncommon, 1 Rare; 17-20: 2 Common, 4 Uncommon, 3 Rare, 1 Very Rare), which the GM gives with POST /api/gm/give-item. gold=false skips the money.
// @Tags GM
// @Param request body object{campaign_id=int,character_id=int,level=int,gold=bool} true "Character and starting level"
// @Router /gm/starting-level [post]
func handleGMStartingLevel(w http.ResponseWriter, r *http.Request) {
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CharacterID int   `json:"character_id"`
		Level       int   `json:"level"`
		Gold        *bool `json:"gold"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	var cur, charLobby int
	if db.QueryRow(`SELECT level, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&cur, &charLobby) != nil || charLobby != lobby {
		dmBadRequest(w, "character_required", "Send the character_id of a character in this campaign.")
		return
	}
	if req.Level < 2 || req.Level > 20 || req.Level <= cur {
		dmBadRequest(w, "invalid_level", fmt.Sprintf("level must be higher than the character's level (%d) and at most 20.", cur))
		return
	}
	hp, asi, note := applyLevelGain(req.CharacterID, cur, req.Level, "starting at a higher level")
	gold := 0
	switch {
	case req.Level >= 17:
		gold = 20000 + game.RollDie(10)*250
	case req.Level >= 11:
		gold = 5000 + game.RollDie(10)*250
	case req.Level >= 5:
		gold = 500 + game.RollDie(10)*25
	}
	if req.Gold != nil && !*req.Gold {
		gold = 0
	}
	if gold > 0 {
		db.Exec(`UPDATE characters SET gold = COALESCE(gold,0) + $1 WHERE id = $2`, gold, req.CharacterID)
	}
	var items string
	switch {
	case req.Level >= 17:
		items = "2 Common, 4 Uncommon, 3 Rare and 1 Very Rare magic item"
	case req.Level >= 11:
		items = "2 Common, 3 Uncommon and 1 Rare magic item"
	case req.Level >= 5:
		items = "1 Common and 1 Uncommon magic item"
	default:
		items = "1 Common magic item"
	}
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "hp_gained": hp, "asi_points": asi, "gold_added": gold, "suggested_magic_items": items,
		"note": note, "result": fmt.Sprintf("%s Starting money: +%d GP. Suggested magic items: %s (POST /api/gm/give-item).", note, gold, items)})
}
