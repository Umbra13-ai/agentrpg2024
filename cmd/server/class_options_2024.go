package main

// POST /api/characters/{id}/class-options: the request for every choice in choiceDefs that has a field (choices_2024.go). The
// handler knows no feature: the fields it takes, who may send each and what each accepts all come from the definitions.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// classOptionFieldNames: the fields the request takes, in the table's order.
func classOptionFieldNames() []string {
	var out []string
	for _, d := range choiceDefs {
		if d.set != nil && !containsStr(out, d.field) {
			out = append(out, d.field)
		}
	}
	return out
}

// classOptionAliases: the other names a class choice can be sent under: each one-option pick's key and feature name.
func classOptionAliases() map[string]string {
	out := map[string]string{}
	for i := range featurePicks {
		p := &featurePicks[i]
		for _, a := range []string{p.key, p.feature} {
			if a != "" && a != p.sendAs() {
				out[a] = p.sendAs()
			}
		}
	}
	return out
}

// classOptionFor: the character's choice a field names (its field, or the name a pick is stored under). When several
// definitions share a field (the class languages of two classes), the one the character has.
func classOptionFor(c *choiceChar, field string) *choiceDef {
	var match *choiceDef
	for _, d := range choiceDefs {
		if d.set == nil {
			continue
		}
		named := game.Key(field) == game.Key(d.field)
		if p := featurePickNamed(field); p != nil && p.sendAs() == d.field {
			named = true
		}
		if !named {
			continue
		}
		if _, want := d.status(c); want > 0 || match == nil {
			match = d
			if want > 0 {
				break
			}
		}
	}
	return match
}

// handleCharacterClassOptions godoc
// @Summary Class choices: Divine Order, Primal Order, land type, Elemental Affinity, Blessed Strikes, Elemental Fury, Hunter's Prey, Defensive Tactics, Wild Shape forms, Primal Knowledge, Bonus Proficiencies, Magical Discoveries, class languages
// @Description Makes one class or subclass choice. One field per request: divine_order (Cleric 1: protector or thaumaturge), primal_order (Druid 1: magician or warden), blessed_strikes (Cleric 7: divine_strike or potent_spellcasting), elemental_fury (Druid 7: potent_spellcasting or primal_strike), elemental_affinity (Draconic Sorcerer 6: acid, cold, fire, lightning or poison), land_type (Circle of the Land 3: arid, polar, temperate or tropical; again after a Long Rest), hunters_prey (Hunter 3: colossus_slayer or horde_breaker) and defensive_tactics (Hunter 7: escape_the_horde or multiattack_defense), each replaceable after a Short or Long Rest, wild_shape_forms (Druid 2+: all your known Beast forms), primal_knowledge (Barbarian 3: one Barbarian skill), bonus_proficiencies (College of Lore 3: three skills), magical_discoveries (College of Lore 6: two Cleric, Druid or Wizard spells), class_languages (Ranger 2: two languages; Rogue 1: one). An option can be sent as its name or its slug. A choice your character doesn't have, an option that isn't one of the feature's, or a second pick of a choice made once is refused with the reason and the valid options. GET /api/characters/{id}/setup shows each open choice with its options explained.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{divine_order=string,primal_order=string,blessed_strikes=string,elemental_fury=string,elemental_affinity=string,land_type=string,hunters_prey=string,defensive_tactics=string,wild_shape_forms=[]string,primal_knowledge=string,bonus_proficiencies=[]string,magical_discoveries=[]string,class_languages=[]string} true "One choice"
// @Router /characters/{id}/class-options [post]
func handleCharacterClassOptions(w http.ResponseWriter, r *http.Request, charID int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if !requireOwnerOrGM(w, r, charID) {
		return
	}
	c, ok := loadChoiceChar(charID)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	// a field that isn't a class choice is refused, and character_id must be the path's (request_body_2024.go)
	var sent map[string]json.RawMessage
	if !readRequest(w, r, &sent, onlyFields(classOptionFieldNames()...), fieldAliases(classOptionAliases())) {
		return
	}
	var fields []string
	for k := range sent {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	switch {
	case len(fields) == 0:
		// nothing sent: say which of these choices the character has open
		var open []string
		for _, o := range openChoicesOf(c) {
			if o.def.set != nil {
				open = append(open, o.def.openLine(c, o.have, o.want))
			}
		}
		msg := "No class choice was sent, and your character has none of these to make now."
		if len(open) > 0 {
			msg = "No class choice was sent. Open for your character: " + strings.Join(open, "; ") + "."
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_set", "message": msg + " Nothing was changed.", "valid_fields": classOptionFieldNames()})
		return
	case len(fields) > 1:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "one_choice_per_request",
			"message": fmt.Sprintf("Send one class choice per request; you sent %s. Nothing was changed.", strings.Join(fields, " and "))})
		return
	}
	d := classOptionFor(c, fields[0])
	if _, want := d.status(c); want == 0 {
		(&pickRefusal{http.StatusBadRequest, "not_available", fmt.Sprintf("%s is %s; your character doesn't have it.", d.feature, d.firstAt()), nil}).write(w)
		return
	}
	reply, refusal := d.set(c, sent[fields[0]])
	if refusal != nil {
		refusal.write(w)
		return
	}
	writeJSONStatus(w, http.StatusOK, reply)
}

// listValue reads a field that takes a list of names.
func listValue(field string, value json.RawMessage) ([]string, *pickRefusal) {
	var list []string
	if strings.TrimSpace(string(value)) == "null" || json.Unmarshal(value, &list) != nil {
		return nil, &pickRefusal{http.StatusBadRequest, "invalid_value", fmt.Sprintf("%s takes a list, like [\"...\", \"...\"].", field), nil}
	}
	return list, nil
}

func setWildShapeChoice(c *choiceChar, value json.RawMessage) (map[string]interface{}, *pickRefusal) {
	forms, bad := listValue("wild_shape_forms", value)
	if bad != nil {
		return nil, bad
	}
	if why := setWildShapeForms(c.id, forms); why != "" {
		return nil, &pickRefusal{http.StatusBadRequest, "invalid_wild_shape_forms", why, nil}
	}
	return map[string]interface{}{"success": true, "wild_shape_forms": knownWildShapes(c.id)}, nil
}

func setMagicalDiscoveriesChoice(c *choiceChar, value json.RawMessage) (map[string]interface{}, *pickRefusal) {
	spells, bad := listValue("magical_discoveries", value)
	if bad != nil {
		return nil, bad
	}
	if why := setMagicalDiscoveries(c.id, spells); why != "" {
		return nil, &pickRefusal{http.StatusBadRequest, "invalid_magical_discoveries", why, nil}
	}
	return map[string]interface{}{"success": true, "magical_discoveries": magicalDiscoveriesOf(c.id),
		"message": "Learned; these two spells are always prepared and don't count against your prepared spells."}, nil
}

func setClassLanguagesChoice(c *choiceChar, value json.RawMessage) (map[string]interface{}, *pickRefusal) {
	langs, bad := listValue("class_languages", value)
	if bad != nil {
		return nil, bad
	}
	if why := setClassLanguages(c.id, langs); why != "" {
		return nil, &pickRefusal{http.StatusBadRequest, "invalid_languages", why, nil}
	}
	return map[string]interface{}{"success": true, "languages": languagesOf(c.id)}, nil
}

// saveSkillChoice adds the skills a class feature gives and records the pick.
func saveSkillChoice(c *choiceChar, key string, add []string) map[string]interface{} {
	_, csv := heldSkills(c.id)
	for _, s := range add {
		if strings.TrimSpace(csv) == "" {
			csv = s
		} else {
			csv += ", " + s
		}
	}
	stored := subclassChoicesOf(c.id)
	stored[key] = strings.Join(add, ", ")
	b, _ := json.Marshal(stored)
	db.Exec(`UPDATE characters SET skill_proficiencies = $1, subclass_choices = $2 WHERE id = $3`, csv, string(b), c.id)
	return map[string]interface{}{"success": true, key: add, "skill_proficiencies": csv, "message": fmt.Sprintf("You are now proficient in %s.", joinNames(add))}
}

// setPrimalKnowledge: Barbarian 3, another skill from the Barbarian skill list.
func setPrimalKnowledge(c *choiceChar, value json.RawMessage) (map[string]interface{}, *pickRefusal) {
	var v string
	if json.Unmarshal(value, &v) != nil {
		return nil, &pickRefusal{http.StatusBadRequest, "invalid_value", "primal_knowledge takes one skill as text.", nil}
	}
	if cur := c.picks["primal_knowledge"]; cur != "" {
		return nil, &pickRefusal{http.StatusConflict, "already_chosen", "Primal Knowledge was chosen: " + cur + ".", nil}
	}
	list := classSkillChoices2024["barbarian"].Skills // "the skill list available to Barbarians at level 1"
	held, _ := heldSkills(c.id)
	var open []string
	for _, s := range list {
		if !held[s] {
			open = append(open, s)
		}
	}
	s, isSkill := game.CanonSkill(v)
	switch {
	case !isSkill || !containsStr(list, s):
		return nil, &pickRefusal{http.StatusBadRequest, "invalid_skill", fmt.Sprintf("Primal Knowledge gives one skill from the Barbarian skill list, and %q isn't on it. You can choose: %s.", v, strings.Join(open, ", ")),
			map[string]interface{}{"valid_options": open}}
	case held[s]:
		return nil, &pickRefusal{http.StatusBadRequest, "already_proficient", fmt.Sprintf("You are already proficient in %s. You can choose: %s.", s, strings.Join(open, ", ")),
			map[string]interface{}{"valid_options": open}}
	}
	return saveSkillChoice(c, "primal_knowledge", []string{s}), nil
}

// setBonusProficiencies: College of Lore 3, three skills.
func setBonusProficiencies(c *choiceChar, value json.RawMessage) (map[string]interface{}, *pickRefusal) {
	skills, bad := listValue("bonus_proficiencies", value)
	if bad != nil {
		return nil, bad
	}
	if cur := c.picks["bonus_proficiencies"]; cur != "" {
		return nil, &pickRefusal{http.StatusConflict, "already_chosen", "Bonus Proficiencies were chosen: " + cur + ".", nil}
	}
	held, _ := heldSkills(c.id)
	add, code, msg := checkBonusProficiencies(held, skills)
	if code != "" {
		return nil, &pickRefusal{http.StatusBadRequest, code, msg, nil}
	}
	return saveSkillChoice(c, "bonus_proficiencies", add), nil
}
