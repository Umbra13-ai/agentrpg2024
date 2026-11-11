package main

// More 2024 class features (SRD 5.2.1).
//	Bard   Countercharm (level 7)        Reaction "countercharm": roll a saving throw again with Advantage.
//	       Magical Secrets (level 10)     spells of the Bard, Cleric, Druid and Wizard lists count as Bard spells (see handleCharacterSpells).
//	Druid  Wild Companion (level 2)       "find familiar ... wild companion" casts Find Familiar with a use of Wild Shape instead of a slot.
//	       Land's Aid (Land, level 3)     Magic action "lands_aid": a use of Wild Shape; Necrotic damage to a creature, Hit Points back to another.
//	       Natural Recovery (Land, 6)     "natural_recovery 2 1": recover spell slots after a Short Rest; a Circle spell can be cast once without a slot.
//	       Nature's Ward (Land, 10)       Resistance by land type (see applyDamageResistance).
//	       Archdruid (level 20)           Evergreen Wild Shape on Initiative; Nature Magician turns Wild Shape uses into a spell slot.
//	Wizard Memorize Spell (level 5)       POST /api/characters/memorize after a Short or Long Rest.
//	       Spell Mastery (level 18)       POST /api/characters/spell-mastery: a level 1 and a level 2 spell cast at will.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func ensureExtraColumns() {
	if db == nil {
		return
	}
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS spell_mastery JSONB DEFAULT '[]'`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS memorize_ready BOOLEAN DEFAULT FALSE`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS mastery_swap_ready BOOLEAN DEFAULT FALSE`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS land_ready BOOLEAN DEFAULT FALSE`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS hunter_swaps TEXT DEFAULT ''`)
}

var (
	reBonus = regexp.MustCompile(`bonus\s*([+-]?\d+)`)
	reDC    = regexp.MustCompile(`dc\s*(\d+)`)
)

// countercharm: a Reaction. The creature rolls its saving throw again with Advantage: "countercharm bonus 3 dc 14".
func countercharm(charID int, description string) string {
	var class, name string
	var level int
	if db.QueryRow(`SELECT class, name, level FROM characters WHERE id = $1`, charID).Scan(&class, &name, &level) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "bard" || level < 7 {
		return "Countercharm needs a Bard of level 7 or higher."
	}
	low := strings.ToLower(description)
	a, b := game.RollDie(20), game.RollDie(20)
	best := a
	if b > a {
		best = b
	}
	text := fmt.Sprintf("🎵 Countercharm: %s uses a Reaction; the saving throw against the Charmed or Frightened effect is rolled again with Advantage: d20s %d and %d, keeping %d.", name, a, b, best)
	if m := reBonus.FindStringSubmatch(low); m != nil {
		bonus, _ := strconv.Atoi(m[1])
		total := best + bonus
		text += fmt.Sprintf(" Total %d", total)
		if d := reDC.FindStringSubmatch(low); d != nil {
			dc, _ := strconv.Atoi(d[1])
			if total >= dc {
				text += fmt.Sprintf(" against DC %d: the save succeeds.", dc)
			} else {
				text += fmt.Sprintf(" against DC %d: the save still fails.", dc)
			}
		} else {
			text += "."
		}
	} else {
		text += " Add the creature's saving throw bonus to the kept d20."
	}
	return text
}

// subclassOf returns a character's class, subclass slug and level.
func subclassOf(charID int) (class, sub string, level int) {
	var s *string
	db.QueryRow(`SELECT class, subclass, level FROM characters WHERE id = $1`, charID).Scan(&class, &s, &level)
	if s != nil {
		sub = strings.ToLower(*s)
	}
	class = strings.ToLower(class)
	if cl := classLevelsOf(charID)[class]; cl > 0 && cl < level {
		level = cl // multiclass: the features these callers gate follow the class's own level, not the total level
	}
	return
}

func landTypeOf(charID int) string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var choices map[string]string
	json.Unmarshal(raw, &choices)
	return strings.ToLower(choices["circle_land"])
}

// landsAid: Magic action, a use of Wild Shape. "lands_aid" with target_id (a monster) and/or "heal <name>".
func landsAid(charID int, description string) string {
	class, sub, level := subclassOf(charID)
	if class != "druid" || sub != "land" || level < 3 {
		return "Land's Aid needs a Druid of the Circle of the Land, level 3 or higher."
	}
	var wis, lobby int
	db.QueryRow(`SELECT wis, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&wis, &lobby)
	low := strings.ToLower(description)
	_, monster, isMonster := attackedMonster(charID)
	healTarget := 0
	if strings.Contains(low, "heal") || strings.Contains(low, "restore") {
		if healTarget = parseTargetFromDescription(description, charID); healTarget <= 0 {
			healTarget = charID
		}
	}
	if !isMonster && healTarget == 0 {
		return "Name a creature to hurt with target_id, or say \"heal <name>\" to restore Hit Points."
	}
	if okRes, msg, _ := useClassResource(charID, "wild_shape", 1); !okRes {
		return msg
	}
	dice := 2
	if level >= 14 {
		dice = 4
	} else if level >= 10 {
		dice = 3
	}
	var parts []string
	if isMonster {
		dmg := 0
		for i := 0; i < dice; i++ {
			dmg += game.RollDie(6)
		}
		dc := 8 + game.ProficiencyBonus(level) + game.Modifier(wis)
		saved, roll := monsterSave(lobby, monster, "con", dc)
		how := "fails"
		if saved {
			dmg /= 2
			how = "saves"
		}
		text, _ := damageMonster(lobby, monster, dmg, "necrotic")
		parts = append(parts, fmt.Sprintf("the creature %s its Constitution save (%d vs DC %d). %s", how, roll, dc, text))
	}
	if healTarget > 0 {
		heal := 0
		for i := 0; i < dice; i++ {
			heal += game.RollDie(6)
		}
		var hp, maxHP int
		var tname string
		db.QueryRow(`SELECT hp, max_hp, name FROM characters WHERE id = $1`, healTarget).Scan(&hp, &maxHP, &tname)
		nh := hp + heal
		if nh > maxHP {
			nh = maxHP
		}
		db.Exec(`UPDATE characters SET hp = $1 WHERE id = $2`, nh, healTarget)
		parts = append(parts, fmt.Sprintf("%s regains %d Hit Points (%d → %d)", tname, nh-hp, hp, nh))
	}
	return "🌿 Land's Aid: " + strings.Join(parts, "; ") + "."
}

var reNumbers = regexp.MustCompile(`\d+`)

// naturalRecovery: after a Short Rest, recover spell slots with a combined level up to half the Druid level (rounded up), none of level 6+.
func naturalRecovery(charID int, description string) string {
	class, sub, level := subclassOf(charID)
	if class != "druid" || sub != "land" || level < 6 {
		return "Natural Recovery needs a Druid of the Circle of the Land, level 6 or higher."
	}
	used := classResourcesUsed(charID)
	if used["natural_recovery_slots"] > 0 {
		return "You already recovered spell slots with Natural Recovery; finish a Long Rest."
	}
	var slotsJSON []byte
	db.QueryRow(`SELECT COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&slotsJSON)
	slotsUsed := map[string]int{}
	json.Unmarshal(slotsJSON, &slotsUsed)
	budget := (level + 1) / 2
	total := 0
	var got []string
	for _, n := range reNumbers.FindAllString(description, -1) {
		lv, _ := strconv.Atoi(n)
		if lv < 1 || lv > 5 {
			return "Choose expended spell slots of level 1 to 5, for example: natural_recovery 3 1 1."
		}
		if slotsUsed[n] <= 0 {
			return fmt.Sprintf("You have no expended level %d spell slot.", lv)
		}
		total += lv
		if total > budget {
			return fmt.Sprintf("The slots can have a combined level of %d or less.", budget)
		}
		slotsUsed[n]--
		got = append(got, "level "+n)
	}
	if len(got) == 0 {
		return fmt.Sprintf("Say which expended slots to recover (combined level up to %d), for example: natural_recovery 2 1.", budget)
	}
	used["natural_recovery_slots"] = 1
	writeUsed(charID, used)
	b, _ := json.Marshal(slotsUsed)
	db.Exec(`UPDATE characters SET spell_slots_used = $1 WHERE id = $2`, string(b), charID)
	return "🌿 Natural Recovery: you recover spell slots (" + strings.Join(got, ", ") + ")."
}

// natureMagician (Archdruid): turn unexpended Wild Shape uses into one spell slot, 2 spell levels for each use. Once per Long Rest.
func natureMagician(charID int, description string) string {
	class, _, level := subclassOf(charID)
	if class != "druid" || level < 20 {
		return "Nature Magician needs a Druid of level 20."
	}
	used := classResourcesUsed(charID)
	if used["nature_magician"] > 0 {
		return "You already converted Wild Shape uses; finish a Long Rest."
	}
	n := 1
	if m := reNumbers.FindString(description); m != "" {
		n, _ = strconv.Atoi(m)
	}
	max := game.ClassTableInt("druid", level, "Wild Shape")
	if n < 1 || used["wild_shape"]+n > max {
		return fmt.Sprintf("You have %d unexpended uses of Wild Shape.", max-used["wild_shape"])
	}
	slotLevel := 2 * n
	if slotLevel > 9 {
		return "A spell slot can be at most level 9."
	}
	var slotsJSON []byte
	db.QueryRow(`SELECT COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&slotsJSON)
	slotsUsed := map[string]int{}
	json.Unmarshal(slotsJSON, &slotsUsed)
	key := strconv.Itoa(slotLevel)
	if slotsUsed[key] <= 0 {
		return fmt.Sprintf("You have no expended level %d spell slot to regain.", slotLevel)
	}
	slotsUsed[key]--
	used["wild_shape"] += n
	used["nature_magician"] = 1
	writeUsed(charID, used)
	b, _ := json.Marshal(slotsUsed)
	db.Exec(`UPDATE characters SET spell_slots_used = $1 WHERE id = $2`, string(b), charID)
	return fmt.Sprintf("🌿 Nature Magician: %d use(s) of Wild Shape become a level %d spell slot.", n, slotLevel)
}

// evergreenWildShape: on rolling Initiative with no Wild Shape uses left, an Archdruid regains one.
func evergreenWildShape(charID int) {
	class, _, level := subclassOf(charID)
	if class != "druid" || level < 20 {
		return
	}
	used := classResourcesUsed(charID)
	if used["wild_shape"] >= game.ClassTableInt("druid", level, "Wild Shape") && used["wild_shape"] > 0 {
		used["wild_shape"]--
		writeUsed(charID, used)
	}
}

// natureWardResists: Nature's Ward (Land, level 10) gives Resistance by land type.
func natureWardResists(charID int, dtype string) bool {
	class, sub, level := subclassOf(charID)
	if class != "druid" || sub != "land" || level < 10 {
		return false
	}
	return map[string]string{"arid": "fire", "polar": "cold", "temperate": "lightning", "tropical": "poison"}[landTypeOf(charID)] == strings.ToLower(dtype)
}

// specialFreeCast: free casts that depend on more than the spell: Wild Companion, Natural Recovery's Circle spell, Spell Mastery.
func specialFreeCast(charID int, class string, level int, spellKey, description string) (string, int, bool) {
	low := strings.ToLower(description)
	c := strings.ToLower(class)
	switch {
	case c == "druid" && level >= 2 && spellKey == "find-familiar" && strings.Contains(low, "wild companion"):
		return "wild_shape", game.ClassTableInt("druid", level, "Wild Shape"), true
	case c == "druid" && level >= 6 && strings.Contains(low, "natural recovery"):
		_, sub, _ := subclassOf(charID)
		if sub == "land" {
			for _, s := range game.LandCircleSpells(landTypeOf(charID), level) {
				if s == spellKey {
					return "natural_recovery_cast", 1, true
				}
			}
		}
	case c == "sorcerer" && level >= 18 && spellKey == "summon-dragon" && strings.Contains(low, "dragon companion"):
		return "dragon_companion", 1, true
	case c == "wizard" && level >= 20 && hasWord(low, "signature") && signatureFree(charID, spellKey):
		return "signature_" + spellKey, 1, true // F120 (wizard_fixes_2024.go)
	case c == "wizard" && level >= 18 && strings.Contains(low, "spell mastery"):
		var raw []byte
		db.QueryRow(`SELECT COALESCE(spell_mastery,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
		var list []string
		json.Unmarshal(raw, &list)
		for _, s := range list {
			if s == spellKey {
				return "spell_mastery_free", 1 << 20, true
			}
		}
	}
	return "", 0, false
}

// ---- Wizard: Memorize Spell, Spell Mastery

func wizardOwner(w http.ResponseWriter, r *http.Request, charID int) (class string, level int, ok bool) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var owner int
	if db.QueryRow(`SELECT agent_id, class, level FROM characters WHERE id = $1`, charID).Scan(&owner, &class, &level) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	return strings.ToLower(class), level, true
}

func spellSlugOf(s string) (string, bool) {
	l := strings.ToLower(strings.TrimSpace(s))
	if _, ok := srdSpellsMemory[l]; ok {
		return l, true
	}
	d := strings.ReplaceAll(l, " ", "-")
	if _, ok := srdSpellsMemory[d]; ok {
		return d, true
	}
	return "", false
}

func inSpellbook(charID int, slug string) bool {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(known_spells,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var book []string
	json.Unmarshal(raw, &book)
	if len(book) == 0 {
		return true // no spellbook recorded: nothing to check against
	}
	for _, s := range book {
		if s == slug {
			return true
		}
	}
	return false
}

// handleMemorizeSpell: POST /api/characters/memorize {"character_id": N, "replace": "old-spell", "with": "new-spell"}
// @Summary Wizard Memorize Spell (level 5)
// @Description Whenever you finish a Short Rest, you can study your spellbook and replace one of the level 1+ Wizard spells you have prepared with another level 1+ spell from the book.
// @Tags Characters
// @Param request body object{character_id=int,replace=string,with=string} true "The prepared spell to replace and the spellbook spell to prepare"
// @Router /characters/memorize [post]
func handleMemorizeSpell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		Replace     string `json:"replace"`
		With        string `json:"with"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": "Send character_id, replace (a prepared spell) and with (a spell from your spellbook)."})
		return
	}
	class, level, ok := wizardOwner(w, r, req.CharacterID)
	if !ok {
		return
	}
	if class != "wizard" || level < 5 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_memorize_spell", "message": "Memorize Spell is a feature of a Wizard of level 5 or higher."})
		return
	}
	var ready bool
	db.QueryRow(`SELECT COALESCE(memorize_ready,false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&ready)
	if !ready {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "needs_rest", "message": "You can study your spellbook when you finish a Short Rest (once per rest)."})
		return
	}
	oldSlug, ok1 := spellSlugOf(req.Replace)
	newSlug, ok2 := spellSlugOf(req.With)
	if !ok1 || !ok2 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_spell"})
		return
	}
	if srdSpellsMemory[oldSlug].Level < 1 || srdSpellsMemory[newSlug].Level < 1 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "level_1_plus", "message": "Memorize Spell swaps level 1+ spells."})
		return
	}
	if !inSpellbook(req.CharacterID, newSlug) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_in_spellbook", "message": "The new spell must be in your spellbook."})
		return
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(prepared_spells,'[]') FROM characters WHERE id = $1`, req.CharacterID).Scan(&raw)
	var prepared []string
	json.Unmarshal(raw, &prepared)
	found := false
	for i, s := range prepared {
		if s == oldSlug {
			prepared[i] = newSlug
			found = true
		}
	}
	if !found {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_prepared", "message": "That spell is not one of your prepared spells."})
		return
	}
	b, _ := json.Marshal(prepared)
	db.Exec(`UPDATE characters SET prepared_spells = $1, memorize_ready = false WHERE id = $2`, string(b), req.CharacterID)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": fmt.Sprintf("You study your spellbook and replace %s with %s.", srdSpellsMemory[oldSlug].Name, srdSpellsMemory[newSlug].Name), "prepared_spells": prepared})
}

// handleSpellMastery: GET shows the two spells; POST {"character_id": N, "spells": ["level-1-spell", "level-2-spell"]} chooses them
// (the first time, or after a Long Rest).
// @Summary Wizard Spell Mastery (level 18)
// @Description GET shows the two chosen spells; POST chooses a level 1 and a level 2 spell from your spellbook that have a casting time of an action. You can cast them at their lowest level without expending a spell slot while you have them prepared. You can change them when you finish a Long Rest.
// @Tags Characters
// @Param request body object{character_id=int,spells=[]string} true "A level 1 and a level 2 spell"
// @Router /characters/spell-mastery [post]
func handleSpellMastery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		CharacterID int      `json:"character_id"`
		Spells      []string `json:"spells"`
	}
	if r.Method == http.MethodGet {
		req.CharacterID, _ = strconv.Atoi(r.URL.Query().Get("character_id"))
	} else if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request"})
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required"})
		return
	}
	class, level, ok := wizardOwner(w, r, req.CharacterID)
	if !ok {
		return
	}
	if class != "wizard" || level < 18 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_spell_mastery", "message": "Spell Mastery is a feature of a Wizard of level 18 or higher."})
		return
	}
	var raw []byte
	var swapReady bool
	db.QueryRow(`SELECT COALESCE(spell_mastery,'[]'), COALESCE(mastery_swap_ready,false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&raw, &swapReady)
	var current []string
	json.Unmarshal(raw, &current)
	if r.Method == http.MethodGet {
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"spell_mastery": current, "rule": "A level 1 and a level 2 spell in your spellbook with a casting time of an action: always prepared, cast at their lowest level without a spell slot (say \"spell mastery\" in the cast)."})
		return
	}
	if len(current) > 0 && !swapReady {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "needs_long_rest", "message": "You can replace a Spell Mastery spell when you finish a Long Rest."})
		return
	}
	if len(req.Spells) != 2 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "two_spells", "message": "Send two spells: one of level 1 and one of level 2."})
		return
	}
	var slugs []string
	levels := map[int]bool{}
	for _, s := range req.Spells {
		slug, ok := spellSlugOf(s)
		if !ok {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_spell", "message": s})
			return
		}
		sp := srdSpellsMemory[slug]
		if (sp.Level != 1 && sp.Level != 2) || !strings.Contains(strings.ToLower(sp.CastingTime), "action") || strings.Contains(strings.ToLower(sp.CastingTime), "bonus") || strings.Contains(strings.ToLower(sp.CastingTime), "reaction") {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_eligible", "message": sp.Name + " must be a level 1 or 2 spell with a casting time of an action."})
			return
		}
		if !inSpellbook(req.CharacterID, slug) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_in_spellbook", "message": sp.Name + " is not in your spellbook."})
			return
		}
		levels[sp.Level] = true
		slugs = append(slugs, slug)
	}
	if !levels[1] || !levels[2] {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "one_each", "message": "Choose one level 1 and one level 2 spell."})
		return
	}
	b, _ := json.Marshal(slugs)
	db.Exec(`UPDATE characters SET spell_mastery = $1, mastery_swap_ready = false WHERE id = $2`, string(b), req.CharacterID)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "spell_mastery": slugs})
}

// restStudy is called when a character finishes a rest: a Wizard may study the spellbook (Memorize Spell, Spell Mastery).
func restStudy(charID int, long bool) {
	wildShapeRest(charID, long) // a Long Rest lets a Druid replace one known Wild Shape form
	// Hunter's Prey and Defensive Tactics: "Whenever you finish a Short or Long Rest, you can replace the chosen option with the other one."
	db.Exec(`UPDATE characters SET hunter_swaps = 'hunters_prey,defensive_tactics' WHERE id = $1 AND class = 'ranger'`, charID)
	if long {
		db.Exec(`UPDATE characters SET land_ready = true WHERE id = $1 AND class = 'druid'`, charID)
		db.Exec(`UPDATE characters SET memorize_ready = true, mastery_swap_ready = true WHERE id = $1 AND class = 'wizard'`, charID)
	} else {
		db.Exec(`UPDATE characters SET memorize_ready = true WHERE id = $1 AND class = 'wizard'`, charID)
	}
}

// eldritchSmite (Warlock invocation, level 5, Pact of the Blade): once per turn, on a hit with the pact weapon, expend a Pact Magic slot
// for extra Force damage, 1d8 plus 1d8 per slot level. Write "eldritch smite" in the attack description (the slot is spent when the damage
// is rolled, so ask for it only when you mean to spend it). The target is Prone if it is Huge or smaller (the GM applies it).
func eldritchSmite(charID int, class string, level int, descLower string, crit bool) (int, string) {
	if strings.ToLower(class) != "warlock" || !strings.Contains(descLower, "eldritch smite") || !hasInvocation(charID, "eldritch-smite") {
		return 0, ""
	}
	if classResourcesUsed(charID)["eldritch_smite"] > 0 {
		return 0, " [Eldritch Smite: already used this turn]"
	}
	slots := game.SpellSlots("warlock", level)
	var usedRaw []byte
	col := pactSlotColumn(charID) // F132
	db.QueryRow(`SELECT COALESCE(`+col+`,'{}') FROM characters WHERE id = $1`, charID).Scan(&usedRaw)
	spent := map[string]int{}
	json.Unmarshal(usedRaw, &spent)
	for lvl, n := range slots {
		key := strconv.Itoa(lvl)
		if spent[key] < n {
			spent[key]++
			b, _ := json.Marshal(spent)
			db.Exec(`UPDATE characters SET `+col+` = $1 WHERE id = $2`, string(b), charID)
			setUsedResource(charID, "eldritch_smite", 1)
			slotLvl := lvl
			notePendingSpend(charID, func(p *pendingSpend) { p.EldritchLevel = slotLvl }) // given back if the attack misses
			dmg := game.RollDamage(fmt.Sprintf("%dd8", 1+lvl), crit)
			addRider(charID, "force", dmg)
			return dmg, fmt.Sprintf(" [Eldritch Smite +%d force, level %d slot; the target is Prone if Huge or smaller]", dmg, lvl)
		}
	}
	return 0, " [Eldritch Smite: no Pact Magic slot left]"
}

// conSaveProficient says whether the character is proficient in Constitution saving throws.
func conSaveProficient(charID int, class string, level int) bool {
	ok, _ := saveProficiency(charID, class, level, "con")
	return ok
}

// saveProficiency says whether a character is proficient in a saving throw ("str" ... "cha") and, when a class feature gives
// it, which: the saving throws of the character's (first) class, every save from Monk level 14 (Disciplined Survivor), and
// Wisdom and Charisma from Rogue level 15 (Slippery Mind).
func saveProficiency(charID int, class string, level int, short string) (bool, string) {
	short = strings.ToLower(short)
	var saves string
	db.QueryRow(`SELECT COALESCE(saving_throws,'') FROM classes WHERE slug = $1`, strings.ToLower(class)).Scan(&saves)
	for _, s := range strings.Split(saves, ",") {
		if strings.TrimSpace(strings.ToLower(s)) == short {
			return true, ""
		}
	}
	if classLevelIn(charID, class, level, "monk") >= 14 {
		return true, "Disciplined Survivor"
	}
	if (short == "wis" || short == "cha") && classLevelIn(charID, class, level, "rogue") >= 15 {
		return true, "Slippery Mind"
	}
	return false, ""
}

// classLevelIn is the character's level in one class: from class_levels for a multiclass character, else its level when
// that is its class.
// attunementMax: a creature can be attuned to no more than three magic items (SRD 5.2.1 Attunement); a Thief can attune to up to
// four from Rogue level 13 (Use Magic Device).
func attunementMax(charID int) int {
	class, sub, level := subclassOf(charID)
	if sub == "thief" && classLevelIn(charID, class, level, "rogue") >= 13 {
		return 4
	}
	return 3
}

func classLevelIn(charID int, class string, level int, key string) int {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class_levels, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var m map[string]int
	if json.Unmarshal(raw, &m) == nil && len(m) > 0 {
		return m[key]
	}
	if strings.ToLower(class) == key {
		return level
	}
	return 0
}

// natureWardImmune: Nature's Ward (Circle of the Land, level 10) makes the Druid immune to the Poisoned condition.
func natureWardImmune(charID int) bool {
	class, sub, level := subclassOf(charID)
	return class == "druid" && sub == "land" && level >= 10
}
