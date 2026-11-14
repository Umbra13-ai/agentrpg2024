package main

// Expertise in the server. game.ExpertiseGrants (game/expertise.go) is the only place that says which class gets Expertise, at
// which level, in how many skills and in which skills. This file is its database side. Everything that deals with Expertise goes
// through here and the game package: both creation routes, POST /api/characters/{id}/expertise, the setup step, the "still to
// choose" line, the level-up note and the level-up notice. Do not add a class name or a skill list anywhere else.

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// expertiseState: what the Expertise rule needs to know about a character.
type expertiseState struct {
	levels map[string]int  // level in each class
	held   map[string]bool // skill proficiencies
	other  map[string]bool // Expertise that isn't a class choice (a magic item's gift): kept as it is, never a class pick
	picks  []string        // the class Expertise chosen so far
	total  int             // how many skills the classes give Expertise in
}

var expertiseOtherOnce sync.Once

// ensureExpertiseOther: the expertise column holds every skill the character has Expertise in (rolls read it); expertise_other
// names the ones that didn't come from a class feature, so the class's choices can be counted and changed without losing them.
func ensureExpertiseOther() {
	expertiseOtherOnce.Do(func() {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS expertise_other TEXT DEFAULT ''`)
	})
}

func skillSet(csv string) (map[string]bool, []string) {
	set := map[string]bool{}
	var list []string
	for _, s := range strings.Split(csv, ",") {
		if k, ok := game.CanonSkill(s); ok && !set[k] {
			set[k] = true
			list = append(list, k)
		}
	}
	return set, list
}

func loadExpertise(charID int) expertiseState {
	ensureExpertiseOther()
	var exp, other string
	db.QueryRow(`SELECT COALESCE(expertise,''), COALESCE(expertise_other,'') FROM characters WHERE id = $1`, charID).Scan(&exp, &other)
	st := expertiseState{levels: classLevelsOrSingle(charID)}
	st.held, _ = heldSkills(charID)
	st.other, _ = skillSet(other)
	_, all := skillSet(exp)
	for _, k := range all {
		if !st.other[k] {
			st.picks = append(st.picks, k)
		}
	}
	st.total = game.ExpertiseCount(st.levels)
	return st
}

// open: how many class Expertise skills are still to choose.
func (st expertiseState) open() int {
	if n := st.total - len(st.picks); n > 0 {
		return n
	}
	return 0
}

// save writes the class picks, keeping Expertise from other sources.
func (st expertiseState) save(charID int, picks []string) string {
	all := append([]string{}, picks...)
	for _, s := range game.SkillNames() {
		if st.other[s] && !containsStr(all, s) {
			all = append(all, s)
		}
	}
	csv := strings.Join(all, ", ")
	db.Exec(`UPDATE characters SET expertise = $1 WHERE id = $2`, csv, charID)
	return csv
}

// giveExpertise: Expertise in a skill from something other than a class feature (the Throne card). A skill the character
// already has Expertise in stays as it was.
func giveExpertise(charID int, skill string) {
	k, ok := game.CanonSkill(skill)
	if !ok {
		return
	}
	st := loadExpertise(charID)
	if st.other[k] || containsStr(st.picks, k) {
		return
	}
	st.other[k] = true
	var other []string
	for _, s := range game.SkillNames() {
		if st.other[s] {
			other = append(other, s)
		}
	}
	db.Exec(`UPDATE characters SET expertise_other = $1 WHERE id = $2`, strings.Join(other, ", "), charID)
	st.save(charID, st.picks)
}

func expertisePath(charID int) string { return fmt.Sprintf("/api/characters/%d/expertise", charID) }

// expertiseFeatures: the character's Expertise features as a reply shows them, each with what it lets the player choose and the
// SRD text of the class feature.
func expertiseFeatures(levels map[string]int) []map[string]interface{} {
	var out []map[string]interface{}
	for _, g := range game.ExpertiseGrantsFor(levels) {
		f := map[string]interface{}{"class": g.Class, "level": g.Level, "feature": g.Feature, "choose": g.Count, "what_you_choose": g.Describe(),
			"srd": classFeatureText(g.Class, g.Feature)}
		if g.Skills != nil {
			f["only_these_skills"] = g.Skills
		}
		out = append(out, f)
	}
	return out
}

// expertiseRuleText: the SRD text of the character's Expertise features (each feature once).
func expertiseRuleText(levels map[string]int) string {
	var parts []string
	seen := map[string]bool{}
	for _, g := range game.ExpertiseGrantsFor(levels) {
		if k := g.Class + "/" + g.Feature; !seen[k] {
			seen[k] = true
			if t := classFeatureText(g.Class, g.Feature); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, " ")
}

// expertiseOpenLine: the "still to choose" line, or "" when nothing is open.
func expertiseOpenLine(charID int) string {
	st := loadExpertise(charID)
	if st.open() == 0 {
		return ""
	}
	return fmt.Sprintf("Expertise: %d of %d skills chosen (POST %s with all of them)", len(st.picks), st.total, expertisePath(charID))
}

// expertiseSuggestionPrefs: the order the suggestion tries skills in, after the picks the character has and the SRD's own
// recommendation for the class.
var expertiseSuggestionPrefs = []string{"stealth", "perception", "persuasion", "insight", "athletics", "acrobatics", "investigation", "sleight of hand", "deception", "arcana", "survival"}

// expertiseSetupStep: the setup step while class Expertise is still to choose, or nil. Options, suggestion and rule text come from
// the character's grants.
func expertiseSetupStep(charID int) *setupStep {
	st := loadExpertise(charID)
	if st.open() == 0 {
		return nil
	}
	var opts []map[string]string
	for _, s := range game.ExpertiseOptions(st.levels, st.held, st.other) {
		sk, _ := game.LookupSkill(s)
		o := map[string]string{"skill": s, "ability": sk.AbilityName, "example_uses": sk.Examples}
		if containsStr(st.picks, s) {
			o["already_expertise"] = "yes"
		}
		opts = append(opts, o)
	}
	picks := game.SuggestExpertise(st.levels, st.held, st.other, st.picks, expertiseSuggestionPrefs)
	step := setupStep{Key: "expertise", Title: "Choose your Expertise skills",
		Why:     expertiseRuleText(st.levels) + fmt.Sprintf(" You have %d of %d; send all of them (the ones you have and the new ones).", len(st.picks), st.total),
		Request: req("POST", expertisePath(charID), map[string]interface{}{"skills": picks}),
		Choices: map[string]interface{}{"expertise_in_all": st.total, "you_have": st.picks, "your_features": expertiseFeatures(st.levels), "options_your_skill_proficiencies": opts}}
	return &step
}

// expertiseNoteLines: the level-up note lines for the Expertise a class gains exactly at a level.
func expertiseNoteLines(charID int, class string, level int) []string {
	var out []string
	for _, g := range game.ExpertiseGrantsAtLevel(class, level) {
		out = append(out, fmt.Sprintf("%s (POST %s {\"skills\": [...]}, with all your Expertise skills)", g.Describe(), expertisePath(charID)))
	}
	return out
}

// creationExpertise checks the Expertise sent when a level 1 character is made (both creation routes). held is the skills the
// character will have. complete: every skill must be there (the one-request route; the builder checks as the answer is filled in).
// It returns the picks in their stored names, or an error code and message.
func creationExpertise(class string, held map[string]bool, picks []string, complete bool) ([]string, string, string) {
	return game.CheckExpertise(map[string]int{strings.ToLower(class): 1}, held, nil, picks, complete)
}

// handleCharacterExpertise godoc
// @Summary Choose Expertise skills
// @Description The skills your class gives Expertise in (double Proficiency Bonus). GET shows your features (which class feature, at which level, how many skills, and which skills it allows), your picks and your options. POST sends all of your class Expertise skills; each must be a skill you are proficient in that one of your features allows (most allow any; the Wizard's Scholar allows six). Once your campaign has started, Expertise you have chosen stays: send it again with the new skills.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{skills=[]string} true "Skills"
// @Router /characters/{id}/expertise [post]
func handleCharacterExpertise(w http.ResponseWriter, r *http.Request, charID int) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var owner int
	var current string
	if db.QueryRow(`SELECT agent_id, COALESCE(expertise,'') FROM characters WHERE id = $1`, charID).Scan(&owner, &current) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	st := loadExpertise(charID)
	options := game.ExpertiseOptions(st.levels, st.held, st.other)
	if r.Method == http.MethodGet {
		writeJSONStatus(w, 200, map[string]interface{}{"expertise": current, "allowed": st.total, "chosen": st.picks, "still_to_choose": st.open(),
			"your_features": expertiseFeatures(st.levels), "options": options})
		return
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return
	}
	var req struct {
		Skills []string `json:"skills"`
	}
	if !decodeRequest(w, r, &req, false) {
		return
	}
	chosen, code, msg := game.CheckExpertise(st.levels, st.held, st.other, req.Skills, true)
	if code != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg + " Nothing was changed.",
			"valid_options": options, "your_features": expertiseFeatures(st.levels)})
		return
	}
	// Expertise has no rule for changing it later (SRD 5.2.1). While the character is still being made (its campaign hasn't
	// started) the picks can be corrected; after that, what was chosen stays and only new skills are added.
	if inSetup, _ := shopOpen(charID); !inSetup {
		for _, k := range st.picks {
			if !containsStr(chosen, k) {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "expertise_is_permanent",
					"message":       fmt.Sprintf("You have Expertise in %s, and Expertise can't be changed once your campaign has started: send it again with your new skills. (Your GM can correct a mistake.) Nothing was changed.", game.TitleSkill(k)),
					"you_have":      st.picks,
					"valid_options": options})
				return
			}
		}
	}
	csv := st.save(charID, chosen)
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "expertise": csv})
}
