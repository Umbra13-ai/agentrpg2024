package main

// Species abilities (2024 rules): the traits that a player actually uses.
//	GET  /api/characters/species-ability?character_id=N    what you have, uses left, active effects
//	POST /api/characters/species-ability                   {"character_id": N, "ability": "...", ...}
// Abilities: breath_weapon (Dragonborn, has its own endpoint), draconic_flight,
// stonecunning, giant_ancestry, large_form, adrenaline_rush, and
// lineage_spell:<spell> (free casts from an Elf or Tiefling lineage, or the Forest Gnome: listed here with the uses left, spent by
// the cast action with "free" in the description; see origin_spells_2024.go).
// Uses are counted in characters.species_uses and come back on a rest. In a campaign the 10-minute effects (Large Form, Draconic
// Flight, Stonecunning) run on the game clock (the spell effects table: 6-second rounds in combat, gm/pass-time and rests out of
// it); outside a campaign, on the real clock. A Goliath's Fire's Burn, Frost's Chill and Hill's Tumble can be named in the attack
// description ("longsword, fire's burn") and are used when the attack hits a monster and deals damage.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

type speciesUses struct {
	Used    map[string]int    `json:"used"`
	Effects map[string]string `json:"effects"` // ability -> expiry time (RFC3339)
}

type speciesAbility struct {
	Key      string `json:"ability"`
	Name     string `json:"name"`
	Max      int    `json:"max_uses"`
	Recovery string `json:"recovery"` // "long rest" or "short or long rest"
	Cost     string `json:"cost"`     // action, bonus action, reaction, free
	MinLevel int    `json:"min_level,omitempty"`
}

func loadSpeciesUses(charID int) speciesUses {
	u := speciesUses{Used: map[string]int{}, Effects: map[string]string{}}
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(species_uses, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw) == nil {
		json.Unmarshal(raw, &u)
	}
	if u.Used == nil {
		u.Used = map[string]int{}
	}
	if u.Effects == nil {
		u.Effects = map[string]string{}
	}
	return u
}

func saveSpeciesUses(charID int, u speciesUses) {
	b, _ := json.Marshal(u)
	db.Exec(`UPDATE characters SET species_uses = $1 WHERE id = $2`, b, charID)
}

// activeEffects returns the effects that have not run out.
func (u speciesUses) activeEffects(now time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for k, v := range u.Effects {
		if t, err := time.Parse(time.RFC3339, v); err == nil && t.After(now) {
			out[k] = t
		}
	}
	return out
}

// speciesAbilitiesFor lists what a character's species lets them use at this level.
func speciesAbilitiesFor(rec *originRecord, level int) []speciesAbility {
	pb := game.ProficiencyBonus(level)
	var out []speciesAbility
	switch rec.Species.Key {
	case "dragonborn":
		out = append(out,
			speciesAbility{"breath_weapon", "Breath Weapon", pb, "long rest", "replaces one attack of the Attack action (use /api/characters/breath-weapon)", 0},
			speciesAbility{"draconic_flight", "Draconic Flight", 1, "long rest", "bonus action", 5})
	case "dwarf":
		out = append(out, speciesAbility{"stonecunning", "Stonecunning", pb, "long rest", "bonus action", 0})
	case "goliath":
		out = append(out,
			speciesAbility{"giant_ancestry", "Giant Ancestry: " + giantBoonName(rec.Species.Choices["ancestry"]), pb, "long rest", giantBoonCost(rec.Species.Choices["ancestry"]), 0},
			speciesAbility{"large_form", "Large Form", 1, "long rest", "bonus action", 5})
	case "orc":
		out = append(out, speciesAbility{"adrenaline_rush", "Adrenaline Rush", pb, "short or long rest", "bonus action", 0})
	case "gnome":
		if rec.Species.Choices["lineage"] == "forest" {
			out = append(out, speciesAbility{"lineage_spell:speak-with-animals", "Speak with Animals (free cast)", pb, "long rest", "the spell's casting time", 0})
		}
	}
	for lvl, spells := range rec.Species.LevelSpells {
		for _, s := range spells {
			out = append(out, speciesAbility{"lineage_spell:" + s, titleWords(s) + " (free cast)", 1, "long rest", "the spell's casting time", lvl})
		}
	}
	// keep only what the level allows
	var avail []speciesAbility
	for _, a := range out {
		if level >= a.MinLevel {
			avail = append(avail, a)
		}
	}
	return avail
}

func giantBoonName(a string) string {
	return map[string]string{"cloud": "Cloud's Jaunt", "fire": "Fire's Burn", "frost": "Frost's Chill", "hill": "Hill's Tumble", "stone": "Stone's Endurance", "storm": "Storm's Thunder"}[a]
}

func giantBoonCost(a string) string {
	switch a {
	case "cloud":
		return "bonus action"
	case "stone", "storm":
		return "reaction"
	default:
		return "free (when you hit with an attack roll)"
	}
}

// speciesTraitEffects are the species traits that last a while, with their names and duration in rounds (10 minutes).
var speciesTraitEffects = map[string]string{"large_form": "Large Form", "draconic_flight": "Draconic Flight", "stonecunning": "Stonecunning"}

// speciesEffectActive: a species trait's effect is running (the game clock in a campaign, the real clock otherwise).
func speciesEffectActive(charID int, key string) bool {
	if lobby := lobbyOfChar(charID); lobby != 0 && hasSpellEffect(lobby, charID, key) {
		return true
	}
	_, ok := loadSpeciesUses(charID).activeEffects(time.Now())[key]
	return ok
}

// endSpeciesEffect ends a species trait's effect early (Large Form "until you end it", Draconic Flight retracted).
func endSpeciesEffect(charID int, key string) {
	if lobby := lobbyOfChar(charID); lobby != 0 {
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = $3`, lobby, charID, key)
	}
	u := loadSpeciesUses(charID)
	delete(u.Effects, key)
	saveSpeciesUses(charID, u)
}

// largeFormAdvantage: Large Form gives Advantage on Strength checks (SRD 5.2.1 Goliath).
func largeFormAdvantage(charID int, ability string) bool {
	a := strings.ToLower(strings.TrimSpace(ability))
	return (a == "str" || a == "strength") && charID > 0 && speciesEffectActive(charID, "large_form")
}

var pendingGiantBoon sync.Map // character id -> true: use the Goliath's Fire's Burn, Frost's Chill or Hill's Tumble if the attack hits

// noteGiantBoon remembers a Giant Ancestry boon named in the attack description.
func noteGiantBoon(charID int, descLower string) {
	pendingGiantBoon.Delete(charID)
	d := strings.NewReplacer("’", "'").Replace(descLower)
	for _, k := range []string{"giant ancestry", "fire's burn", "fires burn", "frost's chill", "frosts chill", "hill's tumble", "hills tumble"} {
		if strings.Contains(d, k) {
			pendingGiantBoon.Store(charID, true)
			return
		}
	}
}

// goliathBoon gives the character's Giant Ancestry, level and uses, or a reason it can't be used.
func goliathBoon(charID int) (ancestry string, level int, uses speciesUses, problem string) {
	var raw []byte
	if db.QueryRow(`SELECT level, COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&level, &raw) != nil {
		return "", 0, uses, "character not found"
	}
	rec, ok := loadOriginRecord(raw)
	if !ok || rec.Species.Key != "goliath" {
		return "", level, uses, "only a Goliath has Giant Ancestry"
	}
	ancestry = rec.Species.Choices["ancestry"]
	uses = loadSpeciesUses(charID)
	if uses.Used["giant_ancestry"] >= game.ProficiencyBonus(level) {
		return ancestry, level, uses, giantBoonName(ancestry) + " has no uses left until a Long Rest"
	}
	return ancestry, level, uses, ""
}

// giantBoonOnTarget applies Fire's Burn, Frost's Chill, Hill's Tumble or Storm's Thunder to a creature in the campaign: a monster
// (negative id) in the fight, or a character. It returns what happened.
func giantBoonOnTarget(lobbyID, charID, targetID int, ancestry string) (string, map[string]interface{}) {
	dmgBoon := map[string][3]interface{}{"fire": {1, 10, "fire"}, "frost": {1, 6, "cold"}, "storm": {1, 8, "thunder"}}
	name := creatureName(lobbyID, targetID)
	res := map[string]interface{}{"target_id": targetID}
	var text string
	if b, ok := dmgBoon[ancestry]; ok {
		roll := game.RollDie(b[1].(int))
		dtype := b[2].(string)
		res["roll"], res["damage_type"] = roll, dtype
		if targetID < 0 {
			t, ok := damageMonster(lobbyID, targetID, roll, dtype)
			if !ok {
				return "", nil
			}
			text = fmt.Sprintf("%s (1d%d = %d): %s", giantBoonName(ancestry), b[1].(int), roll, t)
		} else {
			mod := applyDamageResistance(targetID, roll, dtype)
			db.Exec(`UPDATE characters SET hp = GREATEST(hp - $1, 0) WHERE id = $2`, mod.FinalDamage, targetID)
			res["damage_applied"] = mod.FinalDamage
			text = fmt.Sprintf("%s (1d%d = %d): %s takes %d %s damage.", giantBoonName(ancestry), b[1].(int), roll, name, mod.FinalDamage, dtype)
		}
		if ancestry == "frost" {
			addCombatEffect(lobbyID, targetID, charID, "chilled")
			text += fmt.Sprintf(" %s's Speed is reduced by 10 feet until the start of your next turn.", name)
		}
		return text, res
	}
	if ancestry == "hill" {
		if targetID < 0 {
			if sb := monsterStatBlock(monsterKeyOfCombatant(lobbyID, targetID)); sb != nil && (sb["size"] == "Huge" || sb["size"] == "Gargantuan") {
				return fmt.Sprintf("Hill's Tumble: %s is %s, too big to knock Prone.", name, sb["size"]), res
			}
		}
		addMonsterCondition(lobbyID, targetID, "prone", "Hill's Tumble", 0, "", 0)
		return fmt.Sprintf("Hill's Tumble: %s has the Prone condition.", name), res
	}
	return "", nil
}

// applyGiantBoon runs after an attack roll hits a monster and deals damage, when the description named the boon.
func applyGiantBoon(lobbyID, charID, targetID int, dealt int, alive bool) string {
	if _, ok := pendingGiantBoon.LoadAndDelete(charID); !ok || !alive {
		return ""
	}
	ancestry, level, uses, problem := goliathBoon(charID)
	if problem != "" {
		return " Giant Ancestry: " + problem + "."
	}
	if ancestry != "fire" && ancestry != "frost" && ancestry != "hill" {
		return " " + giantBoonName(ancestry) + " isn't used on a hit (see POST /api/characters/species-ability)."
	}
	if dealt <= 0 {
		return " " + giantBoonName(ancestry) + ": the attack dealt no damage, so the boon isn't used."
	}
	text, _ := giantBoonOnTarget(lobbyID, charID, targetID, ancestry)
	if text == "" {
		return ""
	}
	uses.Used["giant_ancestry"]++
	saveSpeciesUses(charID, uses)
	return fmt.Sprintf(" %s (%d use(s) left)", text, game.ProficiencyBonus(level)-uses.Used["giant_ancestry"])
}

// resetSpeciesUsesOnRest gives back species uses. A short rest restores only
// what says so; a long rest restores everything and ends effects.
func resetSpeciesUsesOnRest(charID int, long bool) {
	u := loadSpeciesUses(charID)
	if long {
		db.Exec(`UPDATE characters SET mastery_swap_used = false WHERE id = $1`, charID) // a Weapon Mastery swap is available again
		u.Used = map[string]int{}
		u.Effects = map[string]string{}
		// Human Resourceful: Heroic Inspiration after a Long Rest
		var raw []byte
		if db.QueryRow(`SELECT COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw) == nil {
			if rec, ok := loadOriginRecord(raw); ok && rec.Species.Key == "human" {
				db.Exec(`UPDATE characters SET inspiration = true WHERE id = $1`, charID)
			}
		}
	} else {
		delete(u.Used, "adrenaline_rush")
	}
	saveSpeciesUses(charID, u)
}

// breathWeaponUses returns the uses spent and available (Proficiency Bonus).
func breathWeaponUses(charID, level int) (used, max int) {
	return loadSpeciesUses(charID).Used["breath_weapon"], game.ProficiencyBonus(level)
}

// spendBreathWeapon counts one use and keeps the old "exhausted" flag in step.
func spendBreathWeapon(charID, level int) {
	u := loadSpeciesUses(charID)
	u.Used["breath_weapon"]++
	saveSpeciesUses(charID, u)
	exhausted := u.Used["breath_weapon"] >= game.ProficiencyBonus(level)
	db.Exec(`UPDATE characters SET breath_weapon_used = $1 WHERE id = $2`, exhausted, charID)
}

// grantSpeciesLevelSpells adds the spells a lineage gives at character levels 3 and 5.
func grantSpeciesLevelSpells(charID, level int) []string {
	var raw, known []byte
	if db.QueryRow(`SELECT COALESCE(origin, '{}'), COALESCE(known_spells, '[]') FROM characters WHERE id = $1`, charID).Scan(&raw, &known) != nil {
		return nil
	}
	rec, ok := loadOriginRecord(raw)
	if !ok {
		return nil
	}
	var spells []string
	json.Unmarshal(known, &spells)
	var added []string
	for lvl, list := range rec.Species.LevelSpells {
		if level < lvl {
			continue
		}
		for _, s := range list {
			if !containsStr(spells, s) {
				spells = append(spells, s)
				added = append(added, s)
			}
		}
	}
	if len(added) > 0 {
		b, _ := json.Marshal(spells)
		db.Exec(`UPDATE characters SET known_spells = $1 WHERE id = $2`, b, charID)
	}
	return added
}

// spendTurnResource uses the bonus action or reaction for the turn.
func spendTurnResource(charID int, kind string) error {
	switch kind {
	case "bonus action":
		var used bool
		db.QueryRow(`SELECT COALESCE(bonus_action_used, false) FROM characters WHERE id = $1`, charID).Scan(&used)
		if used {
			return fmt.Errorf("you have already used your bonus action this turn")
		}
		db.Exec(`UPDATE characters SET bonus_action_used = true WHERE id = $1`, charID)
	case "reaction":
		var used bool
		db.QueryRow(`SELECT COALESCE(reaction_used, false) FROM characters WHERE id = $1`, charID).Scan(&used)
		if used {
			return fmt.Errorf("you have already used your reaction this round")
		}
		db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, charID)
	}
	return nil
}

// dealBoonDamage rolls dice, applies resistance and hurts a character target; monsters are left to the GM.
func dealBoonDamage(targetID int, dice, sides int, damageType string) (map[string]interface{}, int) {
	total := 0
	rolls := []int{}
	for i := 0; i < dice; i++ {
		r := game.RollDie(sides)
		rolls = append(rolls, r)
		total += r
	}
	res := map[string]interface{}{"dice": fmt.Sprintf("%dd%d", dice, sides), "rolls": rolls, "damage": total, "damage_type": damageType}
	if targetID > 0 {
		var name string
		if db.QueryRow(`SELECT name FROM characters WHERE id = $1`, targetID).Scan(&name) == nil {
			mod := applyDamageResistance(targetID, total, damageType)
			db.Exec(`UPDATE characters SET hp = GREATEST(hp - $1, 0) WHERE id = $2`, mod.FinalDamage, targetID)
			res["target"] = name
			res["damage_applied"] = mod.FinalDamage
		} else {
			res["note"] = "Target is not a player character: the GM applies the damage."
		}
	}
	return res, total
}

// handleSpeciesAbility godoc
// @Summary Use a species ability
// @Description GET lists the species traits you can use, the uses left and the running effects; POST uses one: draconic_flight (end: true retracts), stonecunning, giant_ancestry (target_id for Fire's Burn, Frost's Chill, Hill's Tumble and Storm's Thunder: a monster's negative id in the fight or a character; damage_taken for Stone's Endurance), large_form (end: true ends it), adrenaline_rush. In a campaign the 10-minute effects run on the game clock. A Goliath can instead name Fire's Burn, Frost's Chill or Hill's Tumble in the attack description; it is used when the attack hits a monster and deals damage. The Breath Weapon has its own endpoint. Elf, Tiefling and Forest Gnome spells are listed as lineage_spell:<spell> with their free casts left; you cast them with the cast action ("free" in the description uses the free cast).
// @Tags Characters
// @Param character_id query int false "Character ID (GET)"
// @Param request body object{character_id=int,ability=string,target_id=int,damage_taken=int,end=bool} false "The ability (POST)"
// @Router /characters/species-ability [get]
// @Router /characters/species-ability [post]
func handleSpeciesAbility(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		Ability     string `json:"ability"`
		TargetID    int    `json:"target_id"`
		DamageTaken int    `json:"damage_taken"` // Stone's Endurance: the damage you just took
		End         bool   `json:"end"`          // retract Draconic Flight
		Description string `json:"description"`
	}
	if r.Method == http.MethodGet {
		fmt.Sscanf(r.URL.Query().Get("character_id"), "%d", &req.CharacterID)
	} else if r.Method == http.MethodPost {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req)
	} else {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}

	var owner, level, con int
	var name string
	var campaignID sql.NullInt64
	var originRaw []byte
	var temp int
	if db.QueryRow(`SELECT agent_id, name, level, con, lobby_id, COALESCE(origin, '{}'), COALESCE(temp_hp, 0) FROM characters WHERE id = $1`, req.CharacterID).
		Scan(&owner, &name, &level, &con, &campaignID, &originRaw, &temp) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_owner"})
		return
	}
	rec, ok := loadOriginRecord(originRaw)
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_species_abilities", "message": "This character was made before the 2024 species rules."})
		return
	}
	abilities := speciesAbilitiesFor(rec, level)
	uses := loadSpeciesUses(req.CharacterID)
	now := time.Now()
	effects := uses.activeEffects(now)
	if campaignID.Valid { // effects on the game clock
		for key := range speciesTraitEffects {
			if hasSpellEffect(int(campaignID.Int64), req.CharacterID, key) {
				if _, ok := effects[key]; !ok {
					effects[key] = time.Time{}
				}
			}
		}
	}

	if r.Method == http.MethodGet {
		list := []map[string]interface{}{}
		for _, a := range abilities {
			left := a.Max - uses.Used[a.Key]
			if left < 0 {
				left = 0
			}
			list = append(list, map[string]interface{}{"ability": a.Key, "name": a.Name, "uses_left": left, "max_uses": a.Max, "recovery": a.Recovery, "cost": a.Cost})
		}
		active := map[string]string{}
		for k, t := range effects {
			if t.IsZero() {
				active[k] = "running (game clock: 10 minutes from when it began)"
			} else {
				active[k] = t.Format(time.RFC3339)
			}
		}
		var passive []string
		for _, t := range rec.Species.Traits {
			passive = append(passive, t.Name)
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"species": rec.Species.Name, "abilities": list, "active_effects": active, "traits": passive,
			"how_to_use": "POST /api/characters/species-ability {\"character_id\": N, \"ability\": \"<ability>\"}"})
		return
	}

	key := strings.ToLower(strings.TrimSpace(req.Ability))
	if key == "breath_weapon" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "use_breath_weapon_endpoint", "message": "Use POST /api/characters/breath-weapon"})
		return
	}
	var ab *speciesAbility
	for i := range abilities {
		if abilities[i].Key == key {
			ab = &abilities[i]
		}
	}
	if ab == nil {
		var keys []string
		for _, a := range abilities {
			keys = append(keys, a.Key)
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "ability_not_available", "message": "Your species does not give you this ability at your level.", "valid_options": keys})
		return
	}
	if (key == "draconic_flight" || key == "large_form") && req.End {
		endSpeciesEffect(req.CharacterID, key)
		msg := name + " retracts their spectral wings."
		if key == "large_form" {
			msg = name + " returns to their usual size."
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": msg})
		return
	}
	if strings.HasPrefix(key, "lineage_spell:") { // the free cast is spent by the cast itself (origin_spells_2024.go)
		spell := strings.TrimPrefix(key, "lineage_spell:")
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "use_cast_action", "uses_left": ab.Max - uses.Used[key],
			"message": fmt.Sprintf("Cast it with POST /api/action {\"action\": \"cast\", \"spell_slug\": \"%s\", \"description\": \"cast %s free\"}: the word \"free\" uses the free cast (no spell slot); without it a spell slot is used. A character with no spell slots uses the free cast automatically.", spell, spell)})
		return
	}
	if uses.Used[key] >= ab.Max {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "no_uses_left", "message": ab.Name + " has no uses left until a " + ab.Recovery + "."})
		return
	}
	if _, trait := speciesTraitEffects[key]; trait && speciesEffectActive(req.CharacterID, key) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "already_active", "message": ab.Name + " is already active."})
		return
	}
	cost := ab.Cost
	if strings.HasPrefix(cost, "bonus action") {
		cost = "bonus action"
	} else if strings.HasPrefix(cost, "reaction") {
		cost = "reaction"
	}
	if cost == "bonus action" || cost == "reaction" {
		if serr := spendTurnResource(req.CharacterID, cost); serr != nil {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "action_unavailable", "message": serr.Error()})
			return
		}
	}

	pb := game.ProficiencyBonus(level)
	resp := map[string]interface{}{"success": true, "ability": key, "character": name, "cost": ab.Cost}
	setEffect := func(d time.Duration) {
		if campaignID.Valid { // the game clock: 10 minutes is 100 rounds
			addSpellEffect(int(campaignID.Int64), req.CharacterID, req.CharacterID, speciesTraitEffects[key], key, "", int(d/(6*time.Second)), "", 0)
			resp["lasts"] = "10 minutes of game time (100 rounds)"
			return
		}
		uses.Effects[key] = now.Add(d).Format(time.RFC3339)
		resp["lasts_until"] = uses.Effects[key]
	}
	switch {
	case key == "stonecunning":
		setEffect(10 * time.Minute)
		resp["effect"] = "Tremorsense with a range of 60 feet for 10 minutes. You must be on or touching a stone surface (natural or worked)."
	case key == "adrenaline_rush":
		newTemp := temp
		if pb > newTemp { // temporary Hit Points do not add together
			newTemp = pb
		}
		db.Exec(`UPDATE characters SET temp_hp = $1 WHERE id = $2`, newTemp, req.CharacterID)
		resp["effect"] = fmt.Sprintf("You take the Dash action as a bonus action and gain %d Temporary Hit Points (you now have %d).", pb, newTemp)
		resp["temp_hp"] = newTemp
	case key == "large_form":
		setEffect(10 * time.Minute)
		resp["effect"] = "You become Large for 10 minutes or until you end it (end: true, no action): Advantage on Strength checks and Speed +10 feet (if there is room)."
	case key == "draconic_flight":
		setEffect(10 * time.Minute)
		resp["effect"] = "Spectral wings: Fly Speed equal to your Speed for 10 minutes, or until you retract them (end: true) or become Incapacitated."
	case key == "giant_ancestry":
		switch rec.Species.Choices["ancestry"] {
		case "cloud":
			resp["effect"] = "You magically teleport up to 30 feet to an unoccupied space you can see."
		case "fire", "frost", "hill", "storm":
			if req.TargetID != 0 && campaignID.Valid {
				if text, res := giantBoonOnTarget(int(campaignID.Int64), req.CharacterID, req.TargetID, rec.Species.Choices["ancestry"]); text != "" {
					resp["effect"], resp["roll"] = text, res
					break
				}
			}
			resp["effect"] = map[string]string{
				"fire":  "Fire's Burn: 1d10 extra Fire damage to the target you hit (send target_id to apply it; or name the boon in the attack description).",
				"frost": "Frost's Chill: 1d6 extra Cold damage, and the target's Speed is reduced by 10 feet until the start of your next turn (send target_id).",
				"hill":  "Hill's Tumble: if the target you hit is Large or smaller, it has the Prone condition (send target_id).",
				"storm": "Storm's Thunder: 1d8 Thunder damage to the creature that hurt you, within 60 feet (send target_id).",
			}[rec.Species.Choices["ancestry"]]
			if rec.Species.Choices["ancestry"] != "hill" && req.TargetID == 0 {
				d, _ := dealBoonDamage(0, 1, map[string]int{"fire": 10, "frost": 6, "storm": 8}[rec.Species.Choices["ancestry"]], map[string]string{"fire": "fire", "frost": "cold", "storm": "thunder"}[rec.Species.Choices["ancestry"]])
				resp["roll"] = d
			}
		case "stone":
			roll := game.RollDie(12)
			reduce := roll + game.Modifier(con)
			if reduce < 0 {
				reduce = 0
			}
			resp["roll"] = map[string]interface{}{"dice": "1d12", "rolled": roll, "constitution_modifier": game.Modifier(con), "damage_reduced_by": reduce}
			resp["effect"] = fmt.Sprintf("Stone's Endurance: the damage is reduced by %d.", reduce)
			if req.DamageTaken > 0 {
				heal := reduce
				if heal > req.DamageTaken {
					heal = req.DamageTaken
				}
				db.Exec(`UPDATE characters SET hp = LEAST(hp + $1, max_hp) WHERE id = $2`, heal, req.CharacterID)
				resp["hp_restored"] = heal
			}
		}
	}
	uses.Used[key]++
	saveSpeciesUses(req.CharacterID, uses)
	resp["uses_left"] = ab.Max - uses.Used[key]

	if campaignID.Valid {
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'species_ability', $3, $4)`,
			campaignID.Int64, req.CharacterID, fmt.Sprintf("%s uses %s", name, ab.Name), fmt.Sprint(resp["effect"]))
	}
	writeJSONStatus(w, http.StatusOK, resp)
}
