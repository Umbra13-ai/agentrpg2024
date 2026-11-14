package main

// One level-up path for every way a character's level goes up: an XP award, the GM setting the level, starting at a higher level.
// It adds the Hit Points for each level (the fixed value of the Hit Die + Constitution modifier, with Draconic Resilience, Dwarven
// Toughness and Tough), the Ability Score Improvement points, sets XP to at least the level's minimum, and posts a public note in the
// campaign with the class features gained and the choices the character still has to make (subclass, Ability Score Improvement or a
// feat, Epic Boon, Fighting Style, Expertise, Metamagic, Eldritch Invocations, spells ...) and where to make them.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
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

// levelUpChoices lists the selections the levels gained ask for (level_choices_2024.go has the table).
func levelUpChoices(charID int, class, subclass string, from, to int, features []string) []string {
	return levelChoicesFor(charID, class, subclass, from, to)
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
	db.Exec(`UPDATE characters SET level = $1, hp = hp + $2, max_hp = max_hp + $2, pending_asi = COALESCE(pending_asi,0) + $3, xp = $4 WHERE id = $5`, newLevel, hp, asi, xp, charID)
	syncClassLevels(charID, class, newLevel)
	grantSpellChangesOnLevelUp(charID, class, levels) // a Bard, Sorcerer or Warlock replaces a spell; cantrips
	if newLevel >= 20 {
		classCapstones(charID, name, class, newLevel) // Primal Champion, Body and Mind: the +4 scores come with level 20
	}

	recordLevelUpNotices(charID, oldLevel, newLevel, hp) // the server's own notice to the player (level_up_notice_2024.go)
	features := classFeaturesGained(class, sub, oldLevel, newLevel)
	choices := levelUpChoices(charID, class, sub, oldLevel, newLevel, features)
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

// levelUpOpenChoices lists what a character still has to choose (for the sheet and my-turn): unspent ASI points and a missing subclass.
func levelUpOpenChoices(charID int) []string {
	var level, pending int
	var class string
	var subclass *string
	if db.QueryRow(`SELECT level, COALESCE(class,''), subclass, COALESCE(pending_asi,0) FROM characters WHERE id = $1`, charID).Scan(&level, &class, &subclass, &pending) != nil {
		return nil
	}
	var out []string
	if level >= 3 && (subclass == nil || *subclass == "") {
		out = append(out, fmt.Sprintf("choose a subclass (POST /api/characters/subclass with character_id %d)", charID))
	}
	if pending > 0 {
		out = append(out, fmt.Sprintf("%d Ability Score Improvement point(s) to spend, or take a feat (POST /api/characters/%d/asi or /feat)", pending, charID))
	}
	if missing := missingCantrips(charID, class, level); missing > 0 { // F223: a caster with empty cantrip places was never told
		out = append(out, fmt.Sprintf("%d cantrip(s) to choose (the Cantrips column of your class table): add them to your list with POST /api/characters/%d/prepare {\"spells\": [...]}", missing, charID))
	}
	sort.Strings(out)
	return out
}

// expertiseAllowed is how many skills a character's class gives Expertise in at its level (SRD 5.2.1): Rogue 2 at level 1 and 2 more at
// level 6; Bard 2 at level 2 and 2 more at level 9; Ranger 1 at level 2 (Deft Explorer) and 2 more at level 9; Wizard 1 at level 2 (Scholar).
func expertiseAllowed(class string, level int) int {
	steps := map[string][][2]int{"rogue": {{1, 2}, {6, 2}}, "bard": {{2, 2}, {9, 2}}, "ranger": {{2, 1}, {9, 2}}, "wizard": {{2, 1}}}[strings.ToLower(class)]
	n := 0
	for _, st := range steps {
		if level >= st[0] {
			n += st[1]
		}
	}
	return n
}

// handleCharacterExpertise godoc
// @Summary Choose Expertise skills
// @Description The skills your class gives Expertise in (double Proficiency Bonus): Rogue levels 1 and 6, Bard 2 and 9, Ranger 2 (Deft Explorer, 1 skill) and 9, Wizard 2 (Scholar, 1 skill). Send all of them; each must be a skill you are proficient in. GET shows the current ones and how many you may have.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{skills=[]string} true "Skills"
// @Router /characters/{id}/expertise [post]
// handleCharacterExpertise: POST /api/characters/{id}/expertise {"skills": ["stealth", "perception"]} chooses the skills (ones the
// character is proficient in) that get Expertise from the class (Rogue levels 1 and 6, Bard 2 and 9, Ranger 2 and 9). GET shows them.
func handleCharacterExpertise(w http.ResponseWriter, r *http.Request, charID int) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var owner, level int
	var class, profs, current string
	if db.QueryRow(`SELECT agent_id, level, COALESCE(class,''), COALESCE(skill_proficiencies,''), COALESCE(expertise,'') FROM characters WHERE id = $1`, charID).
		Scan(&owner, &level, &class, &profs, &current) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	allowed := 0
	for c, l := range classLevelsOrSingle(charID) { // a multiclass character's classes each give theirs, as to_finish counts them
		allowed += expertiseAllowed(c, l)
	}
	if r.Method == http.MethodGet {
		writeJSONStatus(w, 200, map[string]interface{}{"expertise": current, "allowed": allowed})
		return
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	var req struct {
		Skills []string `json:"skills"`
	}
	if msg := decodeStrict(r, &req, `{"skills": [...]}`); msg != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_request", "message": msg + " Nothing was changed."})
		return
	}
	if allowed == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_expertise", "message": fmt.Sprintf("Your class gives no Expertise at level %d. Nothing was changed.", level)})
		return
	}
	if len(req.Skills) != allowed { // fewer silently dropped the ones not sent (Nov 14 review)
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_count", "message": fmt.Sprintf("Your class gives Expertise in %d skill(s) at level %d; you sent %d. Send all %d in skills (the list replaces your Expertise). Nothing was changed.", allowed, level, len(req.Skills), allowed)})
		return
	}
	// each skill exactly as a proficiency the character has, and different ones (F6: ["stealth","stealth"] closed the step with one
	// Expertise, and "ion" passed because it is part of "perception")
	held, _ := heldSkills(charID)
	var heldList []string
	for _, k := range skillOrder {
		if held[k] {
			heldList = append(heldList, k)
		}
	}
	// Wizard Scholar: one of Arcana, History, Investigation, Medicine, Nature or Religion (SRD 5.2.1), not any skill (setup checker,
	// Nov 14). It applies when the Wizard's Scholar is where all of this character's Expertise comes from.
	scholarOnly := true
	for c, l := range classLevelsOrSingle(charID) {
		if expertiseAllowed(c, l) > 0 && !strings.EqualFold(c, "wizard") {
			scholarOnly = false
		}
	}
	if scholarOnly {
		var opts []string
		for _, k := range heldList {
			if containsStr(scholarSkills, k) {
				opts = append(opts, k)
			}
		}
		for _, sk := range req.Skills {
			if k := skillKey(sk); !containsStr(scholarSkills, k) {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_a_scholar_skill",
					"message":       fmt.Sprintf("Scholar gives Expertise in one of Arcana, History, Investigation, Medicine, Nature or Religion that you are proficient in; %q isn't one of them. Nothing was changed.", sk),
					"valid_options": opts, "srd": classFeatureText("wizard", "Scholar")})
				return
			}
		}
		heldList = opts
	}
	var chosen []string
	for _, sk := range req.Skills {
		k := skillKey(sk)
		if !held[k] {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_proficient",
				"message":       fmt.Sprintf("Expertise needs a skill you are proficient in; %q isn't one. Nothing was changed.", sk),
				"valid_options": heldList})
			return
		}
		if containsStr(chosen, k) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "duplicate_skill",
				"message": fmt.Sprintf("You chose %s twice: choose %d different skills. Nothing was changed.", titleWords(k), len(req.Skills))})
			return
		}
		chosen = append(chosen, k)
	}
	db.Exec(`UPDATE characters SET expertise = $1 WHERE id = $2`, strings.Join(chosen, ", "), charID)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "expertise": strings.Join(chosen, ", ")})
}

// scholarSkills: the skills Wizard's Scholar (level 2) can give Expertise in.
var scholarSkills = []string{"arcana", "history", "investigation", "medicine", "nature", "religion"}

// handleCharacterLevelUp godoc
// @Summary Take your next level
// @Description When you have the XP for the next level and it wasn't taken automatically (the GM awarded XP with auto_level false, or you planned a multiclass level), take it: optionally {"class": "..."} to multiclass, where the campaign allows it (multiclassing table rule; off by default: AGENTRPG_MULTICLASS) (SRD 5.2.1: a score of 13 in the primary ability of the new class and of each class you have; GET /api/characters/multiclass lists them). To multiclass while the GM levels characters automatically, plan it first: {"class": "wizard", "plan": true} records the class (prerequisites checked now); the next XP award then waits for you to take that level here instead of raising your main class. {"class": "", "plan": true} cancels the plan.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{class=string,plan=bool,skill=string,multiclass_skill=string} false "Class to take the level in (default: your class); plan: record it for the next level"
// @Router /characters/{id}/level-up [post]
// handleCharacterLevelUp: POST /api/characters/{id}/level-up {"class": "wizard"} takes the next level when the character has the XP for
// it (the GM awards XP; with auto_level false the player chooses when and in which class). The class defaults to the character's own;
// another class is multiclassing (its prerequisites apply). The reply and the public note list the choices to make.
func handleCharacterLevelUp(w http.ResponseWriter, r *http.Request, charID int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	var req struct {
		Class           string `json:"class"`
		Plan            bool   `json:"plan"`
		Skill           string `json:"skill"`            // multiclassing into Bard, Ranger or Rogue: the skill it gives
		MulticlassSkill string `json:"multiclass_skill"` // take a skill a multiclass level gave but that wasn't chosen yet
	}
	if !decodeRequest(w, r, &req, true) {
		return
	}
	if req.MulticlassSkill != "" {
		claimOwedMulticlassSkill(w, r, charID, req.MulticlassSkill)
		return
	}
	if req.Plan {
		planNextLevelClass(w, r, charID, req.Class)
		return
	}
	if req.Class == "" {
		db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&req.Class)
	}
	var levelBefore int
	db.QueryRow(`SELECT level FROM characters WHERE id = $1`, charID).Scan(&levelBefore)
	defer func() { // the planned level is taken: the plan is used up
		var levelAfter int
		db.QueryRow(`SELECT level FROM characters WHERE id = $1`, charID).Scan(&levelAfter)
		if levelAfter > levelBefore {
			db.Exec(`UPDATE characters SET next_level_class = '' WHERE id = $1 AND lower(COALESCE(next_level_class,'')) = lower($2)`, charID, req.Class)
		}
	}()
	body, _ := json.Marshal(map[string]interface{}{"character_id": charID, "target_class": req.Class, "skill": req.Skill})
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(body))
	r2.ContentLength = int64(len(body))
	handleCharacterMulticlass(w, r2)
}

// planNextLevelClass records the class a player wants the next level in ("" cancels). The prerequisites are checked now: 13 in the
// primary ability of that class and of every class the character already has (SRD 5.2.1, Multiclassing).
func planNextLevelClass(w http.ResponseWriter, r *http.Request, charID int, class string) {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS next_level_class TEXT DEFAULT ''`)
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var owner, str, dex, intl, wis, cha int
	var main string
	var levelsRaw []byte
	if db.QueryRow(`SELECT agent_id, COALESCE(class,''), str, dex, intl, wis, cha, COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).
		Scan(&owner, &main, &str, &dex, &intl, &wis, &cha, &levelsRaw) != nil || owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	class = strings.ToLower(strings.TrimSpace(class))
	var planLobby int
	db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&planLobby)
	if class != "" && class != strings.ToLower(main) && classLevelsOf(charID)[class] == 0 && !multiclassAllowed(planLobby) {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "multiclassing_off", "message": multiclassOffMessage()})
		return
	}
	if class == "" || class == strings.ToLower(main) {
		db.Exec(`UPDATE characters SET next_level_class = '' WHERE id = $1`, charID)
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "next_level_class": "", "message": "No multiclass plan: your next level goes to your class as usual."})
		return
	}
	levels := map[string]int{}
	json.Unmarshal(levelsRaw, &levels)
	have := map[string]bool{strings.ToLower(main): true}
	for c := range levels {
		have[strings.ToLower(c)] = true
	}
	if ok, why := meetsMulticlassPrereqs(class, str, dex, intl, wis, cha); !ok {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "prerequisites_not_met", "message": fmt.Sprintf("You can't take a level in %s: %s (13 in its primary ability).", class, why)})
		return
	}
	for c := range have {
		if ok, why := meetsMulticlassPrereqs(c, str, dex, intl, wis, cha); !ok {
			writeJSONStatus(w, 400, map[string]interface{}{"error": "prerequisites_not_met", "message": fmt.Sprintf("To multiclass you also need 13 in the primary ability of your current class %s: %s.", c, why)})
			return
		}
	}
	db.Exec(`UPDATE characters SET next_level_class = $1 WHERE id = $2`, class, charID)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "next_level_class": class,
		"message": fmt.Sprintf("Planned: your next level goes to %s. When the GM's XP award reaches it, the level waits for you: take it with POST /api/characters/%d/level-up {\"class\": \"%s\"}.", class, charID, class)})
}

// ---- the skill a first level in Bard, Ranger or Rogue gives a multiclass character (SRD 5.2.1 "As a Multiclass Character")

func classSkillList(class string) []string {
	var list string
	db.QueryRow(`SELECT COALESCE(skill_choices,'') FROM classes WHERE slug = $1`, strings.ToLower(class)).Scan(&list)
	var out []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// grantMulticlassSkill adds the skill when the class allows it ("" when done, otherwise why not).
func grantMulticlassSkill(charID int, class, skill string) string {
	skill = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(skill, "_", " ")))
	if skill == "" {
		return "no skill chosen"
	}
	list := classSkillList(class)
	if strings.ToLower(class) != "bard" && len(list) > 0 && !containsStr(list, skill) {
		return fmt.Sprintf("%s isn't on the %s skill list (%s).", skill, titleCase(class), strings.Join(list, ", "))
	}
	var have string
	db.QueryRow(`SELECT COALESCE(skill_proficiencies,'') FROM characters WHERE id = $1`, charID).Scan(&have)
	for _, h := range strings.Split(strings.ToLower(have), ",") {
		if strings.TrimSpace(h) == skill {
			return fmt.Sprintf("You're already proficient in %s; choose another skill.", skill)
		}
	}
	if have != "" {
		have += ", "
	}
	db.Exec(`UPDATE characters SET skill_proficiencies = $1 WHERE id = $2`, have+skill, charID)
	return ""
}

func addOwedMulticlassSkill(charID int, class string) {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS multiclass_skills_owed TEXT DEFAULT ''`)
	db.Exec(`UPDATE characters SET multiclass_skills_owed = TRIM(BOTH ',' FROM COALESCE(multiclass_skills_owed,'') || ',' || $1) WHERE id = $2`, strings.ToLower(class), charID)
}

// claimOwedMulticlassSkill: POST level-up {"multiclass_skill": "stealth"} takes a skill a Bard, Ranger or Rogue level gave.
func claimOwedMulticlassSkill(w http.ResponseWriter, r *http.Request, charID int, skill string) {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS multiclass_skills_owed TEXT DEFAULT ''`)
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var owner int
	var owed string
	if db.QueryRow(`SELECT agent_id, COALESCE(multiclass_skills_owed,'') FROM characters WHERE id = $1`, charID).Scan(&owner, &owed) != nil || owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	classes := strings.Split(owed, ",")
	if owed == "" || len(classes) == 0 {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "no_skill_owed", "message": "No multiclass level is waiting for a skill choice."})
		return
	}
	cls := classes[0]
	if why := grantMulticlassSkill(charID, cls, skill); why != "" {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "invalid_skill", "message": why})
		return
	}
	db.Exec(`UPDATE characters SET multiclass_skills_owed = $1 WHERE id = $2`, strings.Join(classes[1:], ","), charID)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "message": fmt.Sprintf("Your %s level gives you proficiency in %s.", titleCase(cls), skill)})
}

// plannedNextClass is the class a player planned for the next level ("" when none, or the character's own class).
func plannedNextClass(charID int) string {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS next_level_class TEXT DEFAULT ''`)
	var plan, main string
	db.QueryRow(`SELECT COALESCE(next_level_class,''), COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&plan, &main)
	if strings.EqualFold(plan, main) {
		return ""
	}
	return plan
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
