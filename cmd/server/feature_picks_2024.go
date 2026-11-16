package main

// Class and subclass features whose choice is one option from a fixed list (SRD 5.2.1). featurePicks is the only place that
// says who has such a choice, from which level, what the options are and whether it can be made again. The choice's entry in
// choiceDefs (its "still to choose" line, its level-up note line, its setup step) and both requests that make it
// (POST /api/characters/{id}/class-options and POST /api/characters/subclass-choice) are made from the row.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type featurePick struct {
	key      string   // the name it is stored under and sent as: "hunters_prey"
	field    string   // another name the class-options request also takes for it ("land_type")
	feature  string   // the SRD's name: "Hunter’s Prey"
	class    string   // the class that has it
	subclass string   // only this subclass ("": the class itself)
	level    int      // from this class level
	options  []string // the options, in the SRD's order
	suggest  string   // the option the setup step fills in
	again    string   // "": chosen once. "short rest", "long rest": can be chosen again when the character finishes one
	column   string   // stored in this column of characters instead of subclass_choices
	viaSub   bool     // the request shown is POST /api/characters/subclass-choice (both requests take every pick)
	// describe says what an option does when the SRD's text has no paragraph of its own for it
	describe func(level int, option string) string
	// after runs when the pick is saved (proficiencies that follow from it)
	after func(charID int)
}

// natureWardDamage: the Nature's Ward table (Circle of the Land 10): the damage type each land choice gives Resistance to.
var natureWardDamage = map[string]string{"arid": "fire", "polar": "cold", "temperate": "lightning", "tropical": "poison"}

var featurePicks = []featurePick{
	{key: "divine_order", feature: "Divine Order", class: "cleric", level: 1, options: []string{"protector", "thaumaturge"}, suggest: "thaumaturge",
		column: "divine_order", after: func(id int) { syncCharacterProficiencies(id) }},
	{key: "primal_order", feature: "Primal Order", class: "druid", level: 1, options: []string{"magician", "warden"}, suggest: "magician",
		column: "primal_order", after: func(id int) { syncCharacterProficiencies(id) }},
	{key: "blessed_strikes", feature: "Blessed Strikes", class: "cleric", level: 7, options: []string{"divine_strike", "potent_spellcasting"}, suggest: "potent_spellcasting"},
	{key: "elemental_fury", feature: "Elemental Fury", class: "druid", level: 7, options: []string{"potent_spellcasting", "primal_strike"}, suggest: "potent_spellcasting"},
	{key: "elemental_affinity", feature: "Elemental Affinity", class: "sorcerer", subclass: "draconic", level: 6,
		options: []string{"acid", "cold", "fire", "lightning", "poison"}, suggest: "fire",
		describe: func(_ int, o string) string {
			return fmt.Sprintf("Resistance to %s damage, and your Charisma modifier added to one damage roll of a spell that deals %s damage.", titleCase(o), titleCase(o))
		}},
	{key: "circle_land", field: "land_type", feature: "Circle of the Land Spells", class: "druid", subclass: "land", level: 3,
		options: []string{"arid", "polar", "temperate", "tropical"}, suggest: "temperate", again: "long rest",
		describe: func(level int, o string) string {
			var names []string
			for _, sl := range getDomainSpells("land", level, o) {
				if sp, ok := srdSpellsMemory[sl]; ok {
					names = append(names, sp.Name)
				}
			}
			return fmt.Sprintf("Circle Spells at your level: %s. Nature's Ward (level 10): Resistance to %s damage.", strings.Join(names, ", "), titleCase(natureWardDamage[o]))
		}},
	{key: "hunters_prey", feature: "Hunter’s Prey", class: "ranger", subclass: "hunter", level: 3,
		options: []string{"colossus_slayer", "horde_breaker"}, suggest: "colossus_slayer", again: "short rest", viaSub: true},
	{key: "defensive_tactics", feature: "Defensive Tactics", class: "ranger", subclass: "hunter", level: 7,
		options: []string{"escape_the_horde", "multiattack_defense"}, suggest: "multiattack_defense", again: "short rest", viaSub: true},
}

func featurePickByKey(key string) *featurePick {
	for i := range featurePicks {
		if featurePicks[i].key == key {
			return &featurePicks[i]
		}
	}
	return nil
}

// featurePickNamed: the pick a request names, by its key, its other field name or the feature's displayed name
// ("hunters_prey", "land_type", "Hunter's Prey").
func featurePickNamed(name string) *featurePick {
	k := game.Key(name)
	for i := range featurePicks {
		p := &featurePicks[i]
		if k == game.Key(p.key) || (p.field != "" && k == game.Key(p.field)) || k == game.Key(p.feature) {
			return p
		}
	}
	return nil
}

// sendAs: the name the class-options request shows for the pick.
func (p *featurePick) sendAs() string {
	if p.field != "" {
		return p.field
	}
	return p.key
}

// who: "a Druid of the Circle of the Land, level 3 or higher", from the row.
func (p *featurePick) who() string {
	s := "a " + titleCase(p.class)
	if p.subclass != "" {
		if sub := game.GetSubclass(p.subclass); sub != nil && sub.Name != "" {
			s += " (" + sub.Name + ")"
		} else {
			s += " (" + titleWords(strings.ReplaceAll(p.subclass, "-", " ")) + ")"
		}
	}
	if p.level > 1 {
		s += fmt.Sprintf(" of level %d or higher", p.level)
	}
	return s
}

// has: the character has the feature.
func (p *featurePick) has(c *choiceChar) bool {
	return c.levels[p.class] >= p.level && (p.subclass == "" || c.subs[p.class] == p.subclass)
}

// srdText: the feature's SRD text.
func (p *featurePick) srdText() string {
	if p.subclass != "" {
		return subclassFeatureText(p.subclass, p.feature)
	}
	return classFeatureText(p.class, p.feature)
}

func pickOptionName(slug string) string { return titleWords(strings.ReplaceAll(slug, "_", " ")) }

// optionText: what the SRD says about one option, from the feature's text ("Protector. Trained for battle, ...").
func optionText(featureText, option string) string {
	name := strings.ToLower(pickOptionName(option))
	for _, para := range strings.Split(featureText, "\n") {
		if strings.HasPrefix(strings.ToLower(para), name+". ") {
			return strings.TrimSpace(para[len(name)+2:])
		}
	}
	return ""
}

// optionList: each option with its name and what the SRD says it does.
func (p *featurePick) optionList(level int) []map[string]interface{} {
	text := p.srdText()
	var out []map[string]interface{}
	for _, o := range p.options {
		m := map[string]interface{}{"slug": o, "name": pickOptionName(o)}
		if t := optionText(text, o); t != "" {
			m["description"] = t
		} else if p.describe != nil {
			m["description"] = p.describe(max(level, p.level), o)
		}
		out = append(out, m)
	}
	return out
}

func (p *featurePick) optionWords() string {
	names := make([]string, len(p.options))
	for i, o := range p.options {
		names[i] = pickOptionName(o)
	}
	return joinOr(names)
}

func (p *featurePick) againWords() string {
	switch p.again {
	case "short rest":
		return "You can replace it with another option when you finish a Short or Long Rest."
	case "long rest":
		return "You can choose again when you finish a Long Rest."
	}
	return "It is chosen once."
}

// request: the request the lines and the setup step show for the pick.
func (p *featurePick) request(id int, value string) (choiceRequest, map[string]interface{}) {
	if p.viaSub {
		return choiceRequest{method: "POST", path: "/api/characters/subclass-choice", body: fmt.Sprintf(`{"character_id": %d, "feature": %q, "choice": "..."}`, id, p.key)},
			req("POST", "/api/characters/subclass-choice", map[string]interface{}{"character_id": id, "feature": p.key, "choice": value})
	}
	path := fmt.Sprintf("/api/characters/%d/class-options", id)
	return choiceRequest{method: "POST", path: path, body: fmt.Sprintf(`{%q: "..."}`, p.sendAs())}, req("POST", path, map[string]string{p.sendAs(): value})
}

// choice: the pick's entry in choiceDefs.
func (p *featurePick) choice() *choiceDef {
	stepKey := p.sendAs()
	return &choiceDef{key: stepKey, feature: strings.ReplaceAll(p.feature, "’", "'"), one: p.optionWords(),
		total: func(class, sub string, level int) int {
			if class == p.class && level >= p.level && (p.subclass == "" || sub == p.subclass) {
				return 1
			}
			return 0
		},
		have: func(c *choiceChar) int {
			if c.picks[p.key] != "" {
				return 1
			}
			return 0
		},
		request: func(id int) choiceRequest {
			r, _ := p.request(id, "")
			return r
		},
		field: p.sendAs(),
		set: func(c *choiceChar, value json.RawMessage) (map[string]interface{}, *pickRefusal) {
			var v string
			if json.Unmarshal(value, &v) != nil {
				return nil, &pickRefusal{http.StatusBadRequest, "invalid_value", fmt.Sprintf("%s takes one option as text: %s.", p.sendAs(), p.optionWords()), nil}
			}
			chosen, refusal := applyFeaturePick(c.id, p, v)
			if refusal != nil {
				return nil, refusal
			}
			return featurePickSaved(c.id, p, chosen), nil
		},
		step: func(c *choiceChar) *setupStep {
			_, request := p.request(c.id, p.suggest)
			choices := map[string]interface{}{"options": p.optionList(c.levels[p.class])}
			return &setupStep{Key: stepKey, Title: "Choose your " + strings.ReplaceAll(p.feature, "’", "'"),
				Why: p.feature + ". " + p.srdText() + " " + p.againWords(), Choices: choices, Request: request}
		}}
}

func picksReadyAgain(charID int) []string {
	var csv string
	db.QueryRow(`SELECT COALESCE(hunter_swaps,'') FROM characters WHERE id = $1`, charID).Scan(&csv)
	var out []string
	for _, k := range strings.Split(csv, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func setPicksReadyAgain(charID int, keys []string) {
	db.Exec(`UPDATE characters SET hunter_swaps = $1 WHERE id = $2`, strings.Join(keys, ","), charID)
}

// restReadiesPicks: finishing a rest lets a character make again the picks whose row says so.
func restReadiesPicks(charID int, long bool) {
	c, ok := loadChoiceChar(charID)
	if !ok {
		return
	}
	ready := picksReadyAgain(charID)
	for i := range featurePicks {
		p := &featurePicks[i]
		if p.again == "" || (p.again == "long rest" && !long) || !p.has(c) || c.picks[p.key] == "" || containsStr(ready, p.key) {
			continue
		}
		ready = append(ready, p.key)
	}
	setPicksReadyAgain(charID, ready)
}

// pickRefusal: why a pick can't be saved.
type pickRefusal struct {
	status  int
	code    string
	message string
	extra   map[string]interface{}
}

func (e *pickRefusal) write(w http.ResponseWriter) {
	msg := e.message
	if !strings.HasSuffix(msg, "Nothing was changed.") {
		msg += " Nothing was changed."
	}
	body := map[string]interface{}{"error": e.code, "message": msg}
	for k, v := range e.extra {
		body[k] = v
	}
	writeJSONStatus(w, e.status, body)
}

// applyFeaturePick checks a pick against its row and saves it. It returns the option saved (its slug), or the refusal.
func applyFeaturePick(charID int, p *featurePick, value string) (string, *pickRefusal) {
	c, ok := loadChoiceChar(charID)
	if !ok {
		return "", &pickRefusal{http.StatusNotFound, "character_not_found", fmt.Sprintf("Character %d not found.", charID), nil}
	}
	name := strings.ReplaceAll(p.feature, "’", "'")
	if !p.has(c) {
		return "", &pickRefusal{http.StatusBadRequest, "not_available", fmt.Sprintf("%s is a choice of %s.", name, p.who()), nil}
	}
	chosen := ""
	for _, o := range p.options {
		if k := game.Key(value); k != "" && (k == game.Key(o) || k == game.Key(pickOptionName(o))) {
			chosen = o
		}
	}
	if chosen == "" {
		msg := fmt.Sprintf("%q is not an option of %s: choose %s.", value, name, p.optionWords())
		if strings.TrimSpace(value) == "" {
			msg = fmt.Sprintf("No option was sent for %s: choose %s.", name, p.optionWords())
		}
		return "", &pickRefusal{http.StatusBadRequest, "invalid_choice", msg, map[string]interface{}{"valid_options": p.optionList(c.levels[p.class]), "valid_slugs": p.options}}
	}
	current := c.picks[p.key]
	ready := picksReadyAgain(charID)
	if current != "" {
		switch {
		case p.again == "":
			return "", &pickRefusal{http.StatusConflict, "already_chosen", fmt.Sprintf("You chose %s for %s, and it is chosen once.", pickOptionName(current), name),
				map[string]interface{}{"current_choice": current}}
		case !containsStr(ready, p.key):
			return "", &pickRefusal{http.StatusConflict, "already_chosen", fmt.Sprintf("You chose %s for %s. %s", pickOptionName(current), name, p.againWords()),
				map[string]interface{}{"current_choice": current}}
		case current == chosen:
			return "", &pickRefusal{http.StatusConflict, "already_chosen", fmt.Sprintf("%s is already your %s.", pickOptionName(current), name),
				map[string]interface{}{"current_choice": current}}
		}
	}
	if p.column != "" {
		db.Exec(`UPDATE characters SET `+p.column+` = $1 WHERE id = $2`, chosen, charID)
	} else {
		stored := subclassChoicesOf(charID)
		stored[p.key] = chosen
		b, _ := json.Marshal(stored)
		db.Exec(`UPDATE characters SET subclass_choices = $1 WHERE id = $2`, string(b), charID)
	}
	if current != "" { // made again: the rest's chance is used
		var left []string
		for _, k := range ready {
			if k != p.key {
				left = append(left, k)
			}
		}
		setPicksReadyAgain(charID, left)
	}
	if p.after != nil {
		p.after(charID)
	}
	return chosen, nil
}

// featurePickSaved: the reply when a pick is saved.
func featurePickSaved(charID int, p *featurePick, chosen string) map[string]interface{} {
	name := strings.ReplaceAll(p.feature, "’", "'")
	out := map[string]interface{}{"success": true, "character_id": charID, "feature": p.key, "feature_name": name, "choice": chosen,
		"choice_name": pickOptionName(chosen), p.key: chosen, "message": fmt.Sprintf("%s: %s. %s", name, pickOptionName(chosen), p.againWords())}
	if p.field != "" {
		out[p.field] = chosen
	}
	if t := optionText(p.srdText(), chosen); t != "" {
		out["description"] = t
	}
	return out
}

// pendingFeaturePicks: the picks the character has and hasn't made, with their options (GET /api/characters/subclass-choice).
func pendingFeaturePicks(c *choiceChar) []map[string]interface{} {
	out := []map[string]interface{}{}
	for i := range featurePicks {
		p := &featurePicks[i]
		if p.has(c) && c.picks[p.key] == "" {
			out = append(out, map[string]interface{}{"feature": p.key, "name": strings.ReplaceAll(p.feature, "’", "'"), "description": p.srdText(), "options": p.optionList(c.levels[p.class])})
		}
	}
	return out
}

// creationPicks: the picks a character makes when it is created (the features of level 1): class_options in both creation
// routes. Creation reads who has each and its options from here.
func creationPicks() []*featurePick {
	var out []*featurePick
	for i := range featurePicks {
		if p := &featurePicks[i]; p.level == 1 && p.subclass == "" {
			out = append(out, p)
		}
	}
	return out
}

func creationPickNames() []string {
	var out []string
	for _, p := range creationPicks() {
		out = append(out, p.key)
	}
	return out
}

// creationPickWords: "divine_order (Cleric) or primal_order (Druid)".
func creationPickWords() string {
	var parts []string
	for _, p := range creationPicks() {
		parts = append(parts, fmt.Sprintf("%s (%s)", p.key, titleCase(p.class)))
	}
	return joinOr(parts)
}

// optionDetails: each option with the SRD's sentence for it ("Protector. Trained for battle, ...").
func (p *featurePick) optionDetails() map[string]string {
	out := map[string]string{}
	text := p.srdText()
	for _, o := range p.options {
		if t := optionText(text, o); t != "" {
			out[o] = pickOptionName(o) + ". " + t
		}
	}
	return out
}
