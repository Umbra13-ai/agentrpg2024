package main

// One level-up path for every way a character's level goes up: an XP award, the GM setting the level, starting at a higher level.
// It adds the Hit Points for each level (the fixed value of the Hit Die + Constitution modifier, with Draconic Resilience, Dwarven
// Toughness and Tough), the Ability Score Improvement points, sets XP to at least the level's minimum, and posts a public note in the
// campaign with the class features gained and the choices the character still has to make (subclass, Ability Score Improvement or a
// feat, Epic Boon, Fighting Style, Expertise, Metamagic, Eldritch Invocations, spells ...) and where to make them.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var featureHeading = regexp.MustCompile(`^Level (\d+): (.+)$`)

// classFeaturesGained lists "Level N: Feature" for the class (and its subclass) for levels after `from` up to `to`, from the SRD.
func classFeaturesGained(class, subclass string, from, to int) []string {
	var out []string
	for _, c := range srdClassTexts() {
		if !strings.EqualFold(c.Name, class) {
			continue
		}
		inSub := false
		subMatches := false
		for _, s := range c.Sections {
			if strings.Contains(s.Heading, " Subclass: ") && s.Level == 2 {
				inSub = true
				subMatches = subclass != "" && strings.Contains(strings.ToLower(s.Heading), strings.ToLower(strings.ReplaceAll(subclass, "-", " ")))
				continue
			}
			m := featureHeading.FindStringSubmatch(s.Heading)
			if m == nil {
				continue
			}
			lvl, _ := strconv.Atoi(m[1])
			if lvl <= from || lvl > to || (inSub && !subMatches) {
				continue
			}
			out = append(out, fmt.Sprintf("level %d: %s", lvl, m[2]))
		}
	}
	return out
}

// applyLevelGain raises a character from oldLevel to newLevel: Hit Points, Ability Score Improvement points, XP, and the public note.
// It returns the Hit Points gained, the ASI points earned and the note.
func applyLevelGain(charID, oldLevel, newLevel int, how string) (int, int, string) {
	defer refreshStoredAC(charID)
	if newLevel <= oldLevel || db == nil {
		return 0, 0, ""
	}
	var name, class string
	var subclass *string
	var lobby, xp int
	db.QueryRow(`SELECT name, COALESCE(class,''), subclass, COALESCE(lobby_id,0), COALESCE(xp,0) FROM characters WHERE id = $1`, charID).Scan(&name, &class, &subclass, &lobby, &xp)
	sub := ""
	if subclass != nil {
		sub = *subclass
	}
	levels := newLevel - oldLevel
	hp := levelUpHitPoints(charID, class, levels)
	if sub != "" {
		if b, ok := getSubclassMechanic(sub, newLevel, "bonus_hp_per_level"); ok { // Draconic Resilience
			if n, err := strconv.Atoi(b); err == nil {
				hp += n * levels
			}
		}
	}
	asi := 0
	for _, l := range asiLevelsFor(class) {
		if oldLevel < l && newLevel >= l {
			asi += 2
		}
	}
	minXP := 0
	if newLevel > 1 {
		minXP = game.XPForNextLevel(newLevel - 1)
	}
	if xp < minXP {
		xp = minXP
	}
	// applied only while the character is still at oldLevel: two gains at once (an XP award and a level-up, two awards) give the
	// level once; XP is never lowered (audit B-18, D-2)
	res, err := db.Exec(`UPDATE characters SET level = $1, `+maxHPChange("$2", true)+`, pending_asi = COALESCE(pending_asi,0) + $3,
		xp = GREATEST(COALESCE(xp, 0), $4) WHERE id = $5 AND COALESCE(level, 1) = $6`, newLevel, hp, asi, xp, charID, oldLevel)
	if err != nil {
		return 0, 0, ""
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, 0, ""
	}
	syncClassLevels(charID, class, newLevel)
	grantSpellChangesOnLevelUp(charID, class, levels) // a Bard, Sorcerer or Warlock replaces a spell; cantrips
	if newLevel >= 20 {
		classCapstones(charID, name, class, newLevel) // Primal Champion, Body and Mind: the +4 scores come with level 20
	}

	recordLevelUpNotices(charID, oldLevel, newLevel, hp) // the server's own notice to the player (level_up_notice_2024.go)
	features := classFeaturesGained(class, sub, oldLevel, newLevel)
	choices := levelChoicesFor(charID, class, sub, oldLevel, newLevel) // from the table of choices (choices_2024.go)
	note := fmt.Sprintf("🔔 Ding! %s reached level %d (from %d, %s): +%d Hit Points.", name, newLevel, oldLevel, how, hp)
	if len(features) > 0 {
		note += " New features: " + strings.Join(features, "; ") + "."
	}
	if added := grantSpeciesLevelSpells(charID, newLevel); len(added) > 0 { // Elf and Tiefling spells at levels 3 and 5
		var names []string
		for _, s := range added {
			if sp, ok := srdSpellsMemory[s]; ok {
				names = append(names, sp.Name)
			} else {
				names = append(names, s)
			}
		}
		note += " Species spells (always prepared, one free cast each per Long Rest): " + strings.Join(names, ", ") + "."
	}
	if len(choices) > 0 {
		note += " Choices to make: " + strings.Join(choices, "; ") + "."
	}
	if lobby > 0 {
		recordGMAction(db, lobby, "level_up", note, note)
	}
	return hp, asi, note
}

// handleCharacterLevelUp godoc
// @Summary Take your next level
// @Description When you have the XP for the next level and it wasn't taken automatically (the GM awarded XP with auto_level false), take it. The level is in your class (multiclassing is not supported). The reply and the campaign feed list the choices the new level brings, each with its request.
// @Tags Characters
// @Param id path int true "Character ID"
// @Router /characters/{id}/level-up [post]
// handleCharacterLevelUp: POST /api/characters/{id}/level-up takes the next level when the character has the XP for it (the GM awards
// XP; with auto_level false the player chooses when). "class" may name the character's own class; any other is refused (multiclassing
// is not supported).
func handleCharacterLevelUp(w http.ResponseWriter, r *http.Request, charID int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		Class string `json:"class"` // the character's own class (the default); another is refused
	}
	if !readRequest(w, r, &req, optionalBody()) {
		return
	}
	if req.Class == "" {
		db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&req.Class)
	}
	takeClassLevel(w, r, charID, req.Class)
}

// syncClassLevels keeps class_levels (the levels in each class, used by multiclassing and level-ups) in step with the total level:
// the levels not in another class belong to the main class.
func syncClassLevels(charID int, class string, total int) {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	levels := map[string]int{}
	json.Unmarshal(raw, &levels)
	main := strings.ToLower(class)
	others := 0
	for k, v := range levels {
		if k != main {
			others += v
		}
	}
	levels[main] = total - others
	if levels[main] < 1 {
		levels[main] = 1
	}
	b, _ := json.Marshal(levels)
	db.Exec(`UPDATE characters SET class_levels = $1 WHERE id = $2`, b, charID)
}

// missingCantrips: how many cantrips the character's classes give that it hasn't chosen (origin and feat cantrips don't fill them).
func missingCantrips(charID int, class string, level int) int {
	levels := classLevelsOf(charID)
	if len(levels) == 0 {
		levels = map[string]int{strings.ToLower(class): level}
	}
	want := 0
	for c, l := range levels {
		want += game.ClassTableInt(c, l, "Cantrips")
	}
	if want == 0 {
		return 0
	}
	want += orderExtraCantrips(charID)
	var known, prepared []byte
	db.QueryRow(`SELECT COALESCE(known_spells,'[]'), COALESCE(prepared_spells,'[]') FROM characters WHERE id = $1`, charID).Scan(&known, &prepared)
	origin := originSpellsOf(charID)
	seen := map[string]bool{}
	have := 0
	for _, k := range append(jsonList(known), jsonList(prepared)...) {
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, isOrigin := origin[k]; isOrigin {
			continue
		}
		if sp, ok := srdSpellsMemory[k]; ok && sp.Level == 0 {
			have++
		}
	}
	if have >= want {
		return 0
	}
	// a class can't learn more cantrips than its list has: cantrips already known another way (Magic Initiate, lineage) leave fewer
	// (Nov 13: an Acolyte Thaumaturge Cleric with Guidance and Sacred Flame from Magic Initiate ran out of Cleric cantrips at level 10)
	left := 0
	for c := range levels {
		for _, k := range getClassSpellList(c) {
			if sp, ok := srdSpellsMemory[k]; ok && sp.Level == 0 && !seen[k] {
				left++
			}
		}
	}
	if left < want-have {
		return left
	}
	return want - have
}

// cantripsCappedNote: when the class list has run out of cantrips to learn, why the Cantrips column isn't met ("" otherwise).
func cantripsCappedNote(charID int, class string, level int) string {
	want := game.ClassTableInt(strings.ToLower(class), level, "Cantrips") + orderExtraCantrips(charID)
	if want == 0 {
		return ""
	}
	var known, prepared []byte
	db.QueryRow(`SELECT COALESCE(known_spells,'[]'), COALESCE(prepared_spells,'[]') FROM characters WHERE id = $1`, charID).Scan(&known, &prepared)
	origin := originSpellsOf(charID)
	have, onList := 0, 0
	all := map[string]bool{}
	for _, k := range append(jsonList(known), jsonList(prepared)...) {
		all[k] = true
	}
	for k := range all {
		if _, o := origin[k]; !o {
			if sp, ok := srdSpellsMemory[k]; ok && sp.Level == 0 {
				have++
			}
		}
	}
	for _, k := range getClassSpellList(strings.ToLower(class)) {
		if sp, ok := srdSpellsMemory[k]; ok && sp.Level == 0 {
			onList++
		}
	}
	if have < want && missingCantrips(charID, class, level) == 0 {
		return fmt.Sprintf("Your class table allows %d cantrips, but the %s list has only %d and you already know the rest another way (Magic Initiate or your species), so there are none left to learn. You know every %s cantrip.", want, titleCase(class), onList, titleCase(class))
	}
	return ""
}
