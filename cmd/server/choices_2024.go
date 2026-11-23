package main

// The choices a character makes after it is created (SRD 5.2.1). choiceDefs is the only list of them in the server. Each
// definition says who has the choice and how many picks it gives at each class level, how to count the picks the character has
// made, the request that makes it, and how to build its setup step. Everything that tells a player about a choice is made from
// the definition:
//
//   - the public level-up note ("Choices to make: ..."), from what each level adds (gainLine);
//   - the "still to choose" list on the sheet, in my-turn and for the GM (characterToFinish), from what is open (openLine);
//   - the steps of GET /api/characters/{id}/setup and the level-up notice's choices_to_make (choiceSteps);
//   - the count a request checks its picks against (choiceCount).
//
// To add a choice: one entry in choiceDefs (a feature that picks one option from a fixed list is a row in featurePicks, in
// feature_picks_2024.go). Do not write a choice's text, level or count anywhere else.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// choiceChar: what the choice rules need to know about a character, read once for a request.
type choiceChar struct {
	id     int
	class  string            // the main class, lowercase
	level  int               // character level
	levels map[string]int    // level in each class
	subs   map[string]string // each class's subclass slug ("" until chosen)
	picks  map[string]string // the one-option picks made (subclass_choices, and the Divine Order and Primal Order columns)
	spells *spellChoice      // where its cantrips and prepared spells stand, once worked out (spellChoiceState)
}

// subclassSlug gives a stored subclass in the form the subclass tables use ("lore", "open-hand"), whatever spelling it was
// saved in ("College of Lore").
func subclassSlug(s string) string {
	if slug, ok := game.LookupSubclass(s); ok {
		return slug
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func loadChoiceChar(charID int) (*choiceChar, bool) {
	if db == nil {
		return nil, false
	}
	c := &choiceChar{id: charID, subs: map[string]string{}}
	var divine, primal string
	if db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1), COALESCE(divine_order,''), COALESCE(primal_order,'') FROM characters WHERE id = $1`, charID).
		Scan(&c.class, &c.level, &divine, &primal) != nil {
		return nil, false
	}
	c.class = strings.ToLower(c.class)
	c.levels = classLevelsOrSingle(charID)
	for class, sub := range subclassesByClass(charID) {
		c.subs[strings.ToLower(class)] = subclassSlug(sub)
	}
	c.picks = subclassChoicesOf(charID)
	c.picks["divine_order"], c.picks["primal_order"] = divine, primal
	return c, true
}

// classNames: the twelve classes, in alphabetical order.
func classNames() []string {
	var out []string
	for c := range srdClasses {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// choiceRequest: the request that makes a choice, as a line shows it.
type choiceRequest struct {
	method, path string
	body         string // the body in words: {"divine_order": "..."}
	how          string // "one per request", "with all of them"
}

func (r choiceRequest) String() string {
	s := r.method + " " + r.path
	if r.body != "" {
		s += " " + r.body
	}
	if r.how != "" {
		s += ", " + r.how
	}
	return s
}

// choiceDef: one choice.
type choiceDef struct {
	key     string // the setup step's key
	feature string // the SRD's name for the feature: "Weapon Mastery"
	one     string // one pick, after "choose": "a kind of weapon", "Protector or Thaumaturge"
	many    string // several: "kinds of weapon"

	// total: how many picks a character of this class, subclass and class level has from the feature in all (0: none yet). The
	// level-up note is what a level adds to it.
	total func(class, subclass string, level int) int
	// have: how many of them the character has made.
	have func(c *choiceChar) int
	// more: picks the character has from elsewhere, added to total (a Fighting Style feat taken with an Ability Score Improvement).
	more func(c *choiceChar) int
	// state replaces total and have for a choice that isn't a count of picks per class level (unspent points, the prepared list).
	state func(c *choiceChar) (have, want int)

	request func(id int) choiceRequest
	// step builds the setup step: the SRD text, the options explained, a suggestion in the request. nil, or a nil result: a
	// plain step with the request.
	step func(c *choiceChar) *setupStep
	// line and gain replace the default words of the "still to choose" line and of the level-up note (after "Feature: ").
	line func(c *choiceChar, have, want int) string
	gain func(charID int, class, subclass string, level int) string
	// always: the step is listed (as done) even when nothing is open.
	always func(c *choiceChar) bool
	// waitsFor: another open choice whose result changes this one's options (the subclass adds always-prepared spells to the
	// spells a character prepares): its key and why, or "". The step then gives no suggestion until that choice is made
	// (playtest I-CALLA-21: a prepare list suggested before Life Domain held Aid, which the domain then made always prepared).
	waitsFor func(c *choiceChar) (key, why string)

	// field and set: a choice made with POST /api/characters/{id}/class-options is sent under this field name, and set checks
	// and saves the value (class_options_2024.go). A choice with a request of its own has neither.
	field string
	set   func(c *choiceChar, value json.RawMessage) (map[string]interface{}, *pickRefusal)
}

// firstAt: the first class level (and the subclass, if it takes one) that gives the choice, for "X is a Barbarian level 3
// feature".
func (d *choiceDef) firstAt() string {
	if d.total == nil {
		return ""
	}
	for level := 1; level <= 20; level++ {
		for _, class := range classNames() {
			if d.total(class, "", level) > 0 {
				return fmt.Sprintf("a %s level %d feature", titleCase(class), level)
			}
			for sub := range game.GetSubclassesForClass(class) {
				if d.total(class, sub, level) > 0 {
					name := titleWords(strings.ReplaceAll(sub, "-", " "))
					if s := game.GetSubclass(sub); s != nil && s.Name != "" {
						name = s.Name
					}
					return fmt.Sprintf("a %s (%s) level %d feature", titleCase(class), name, level)
				}
			}
		}
	}
	return ""
}

var choiceDefs []*choiceDef

func choiceByKey(key string) *choiceDef {
	for _, d := range choiceDefs {
		if d.key == key {
			return d
		}
	}
	return nil
}

// status: how many picks the character has made and how many it has in all. The choice is open while have < want.
func (d *choiceDef) status(c *choiceChar) (have, want int) {
	if d.state != nil {
		return d.state(c)
	}
	for class, level := range c.levels {
		if n := d.total(class, c.subs[class], level); n > want {
			want = n
		}
	}
	if want > 0 {
		if d.more != nil {
			want += d.more(c)
		}
		have = d.have(c)
	}
	return have, want
}

// choiceCount: how many picks of a choice a character has made and how many it has in all. Requests check their picks
// against it, so a request, the setup step and the "still to choose" line can't disagree about the number or the level.
func choiceCount(charID int, key string) (have, want int) {
	d := choiceByKey(key)
	c, ok := loadChoiceChar(charID)
	if d == nil || !ok {
		return 0, 0
	}
	return d.status(c)
}

// openLine: the "still to choose" line.
func (d *choiceDef) openLine(c *choiceChar, have, want int) string {
	var text string
	switch {
	case d.line != nil:
		text = d.line(c, have, want)
	case d.many == "": // one option from a list
		text = "choose " + d.one
	default:
		text = fmt.Sprintf("%d of %d %s chosen", have, want, d.many)
	}
	return fmt.Sprintf("%s: %s (%s)", d.feature, text, d.request(c.id))
}

// gainLine: the level-up note's line for what this class level adds ("" when it adds nothing).
func (d *choiceDef) gainLine(charID int, class, subclass string, level int) string {
	if d.total == nil {
		return ""
	}
	class = strings.ToLower(class)
	now, before := d.total(class, subclass, level), d.total(class, subclass, level-1)
	n := now - before
	if n <= 0 {
		return ""
	}
	var text string
	switch {
	case d.gain != nil:
		text = d.gain(charID, class, subclass, level)
	case before == 0 && n == 1:
		text = "choose " + d.one
	case before == 0:
		text = fmt.Sprintf("choose %d %s", n, d.many)
	default:
		text = fmt.Sprintf("choose %d more (%d %s in all)", n, now, d.many)
	}
	if text == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s (%s)", d.feature, text, d.request(charID))
}

// levelChoicesFor: the level-up note's choices for the class levels after `from` up to `to`, in level order.
func levelChoicesFor(charID int, class, subclass string, from, to int) []string {
	sub := subclassSlug(subclass)
	var out []string
	for lvl := from + 1; lvl <= to; lvl++ {
		for _, d := range choiceDefs {
			if line := d.gainLine(charID, class, sub, lvl); line != "" {
				out = append(out, fmt.Sprintf("level %d: %s", lvl, line))
			}
		}
	}
	return out
}

// openChoice: a choice the character still has to make.
type openChoice struct {
	def        *choiceDef
	have, want int
}

func openChoicesOf(c *choiceChar) []openChoice {
	var out []openChoice
	for _, d := range choiceDefs {
		if have, want := d.status(c); have < want {
			out = append(out, openChoice{d, have, want})
		}
	}
	return out
}

// characterToFinish is everything a character still has to choose, one line for each open choice, sorted.
func characterToFinish(charID int) []string {
	c, ok := loadChoiceChar(charID)
	if !ok {
		return nil
	}
	var out []string
	for _, o := range openChoicesOf(c) {
		out = append(out, o.def.openLine(c, o.have, o.want))
	}
	sort.Strings(out)
	return out
}

// choiceSteps: the setup step of every open choice (and of the ones always listed), in the table's order. A choice with no
// step of its own gets a plain one with its request, so an open choice always has a step.
func choiceSteps(c *choiceChar) []setupStep {
	var out []setupStep
	for _, d := range choiceDefs {
		have, want := d.status(c)
		open := have < want
		if !open && (d.always == nil || !d.always(c)) {
			continue
		}
		var st *setupStep
		if d.step != nil {
			st = d.step(c)
		}
		if st == nil {
			if !open {
				continue
			}
			r := d.request(c.id)
			st = &setupStep{Key: d.key, Title: "Choose: " + d.feature, Why: d.openLine(c, have, want),
				Request: map[string]interface{}{"method": r.method, "path": r.path, "body_like": r.body}}
		}
		st.Done = !open
		if open && d.waitsFor != nil {
			if key, why := d.waitsFor(c); key != "" {
				st.WaitsFor, st.Why = key, strings.TrimSpace(why+" "+st.Why+" The options are listed; a suggested request comes once that choice is made.")
				st.Request, st.Then, st.Requests, st.RequestsLeft = nil, nil, nil, 0 // no suggestion yet: it would be computed without the other choice
				unsuggest(st.Choices)
			}
		}
		if t := countedTitle(d, have, want); open && t != "" {
			st.Title = t // how many are left, from the table's counts (feedback log 51: "Choose your Eldritch Invocation" until all 7)
		}
		out = append(out, *st)
	}
	return out
}

// countedTitle: the step title of a choice made several times, saying how many are left ("Choose 5 more Eldritch Invocations (2 of
// 7 chosen)"); "" for a choice of one option.
func countedTitle(d *choiceDef, have, want int) string {
	if d.many == "" || want < 2 || have >= want {
		return ""
	}
	if left := want - have; left > 1 {
		return fmt.Sprintf("Choose %d more %s (%d of %d chosen)", left, d.many, have, want)
	}
	if have == 0 {
		return fmt.Sprintf("Choose %s (0 of 1 chosen)", d.one)
	}
	return fmt.Sprintf("Choose 1 more: %s (%d of %d chosen)", d.one, have, want)
}

// requiredOpenSteps: the steps of the choices still open (what the level-up notice lists as choices to make).
func requiredOpenSteps(charID int) []setupStep {
	c, ok := loadChoiceChar(charID)
	if !ok {
		return nil
	}
	var out []setupStep
	for _, st := range choiceSteps(c) {
		if !st.Done && !st.Optional {
			out = append(out, st)
		}
	}
	return out
}

// classOnly: a total for one class from a level on.
func classOnly(class string, f func(level int) int) func(string, string, int) int {
	return func(c, _ string, level int) int {
		if c != class {
			return 0
		}
		return f(level)
	}
}

func fromLevel(min, n int) func(int) int {
	return func(level int) int {
		if level >= min {
			return n
		}
		return 0
	}
}

func classOptionsRequest(body string) func(int) choiceRequest {
	return func(id int) choiceRequest {
		return choiceRequest{method: "POST", path: fmt.Sprintf("/api/characters/%d/class-options", id), body: body}
	}
}

func characterRequest(path, fields, how string) func(int) choiceRequest {
	return func(id int) choiceRequest {
		return choiceRequest{method: "POST", path: path, body: fmt.Sprintf("{\"character_id\": %d, %s}", id, fields), how: how}
	}
}

func init() {
	pick := func(key string) *choiceDef { return featurePickByKey(key).choice() }
	choiceDefs = []*choiceDef{
		pick("divine_order"),
		pick("primal_order"),
		{key: "fighting_style", feature: "Fighting Style", one: "a Fighting Style feat", many: "Fighting Style feats",
			total:   func(class, sub string, level int) int { return getMaxFightingStyles(class, level, sub) },
			have:    func(c *choiceChar) int { return len(getFightingStyles(c.id)) },
			more:    func(c *choiceChar) int { return fightingStyleFeatsTaken(c.id) },
			request: characterRequest("/api/characters/fighting-style", `"style": "..."`, "one per request"),
			step:    fightingStyleStep},
		{key: "invocations", feature: "Eldritch Invocations", one: "an Eldritch Invocation", many: "Eldritch Invocations",
			total:   classOnly("warlock", game.GetMaxInvocations),
			have:    func(c *choiceChar) int { return jsonCount(c.id, "eldritch_invocations") },
			request: characterRequest("/api/characters/invocations", `"invocation": "..."`, `one per request; Agonizing Blast, Eldritch Spear and Repelling Blast also take "cantrip" (each copy a different one) and may be taken again`),
			step:    invocationStep},
		{key: "lessons_feat", feature: "Lessons of the First Ones", one: "an Origin feat", many: "Origin feats",
			state: func(c *choiceChar) (int, int) {
				granted, used := lessonsOfTheFirstOnes(c.id)
				return used, granted
			},
			request: func(id int) choiceRequest {
				return choiceRequest{method: "POST", path: fmt.Sprintf("/api/characters/%d/feat", id), body: `{"feat": "...", "choices": {...}}`, how: "one feat per request; Skilled and Magic Initiate send their choices"}
			},
			step: lessonsFeatStep},
		{key: "book_of_shadows", feature: "Pact of the Tome", one: "3 cantrips and 2 level 1 Ritual spells for your Book of Shadows",
			state: func(c *choiceChar) (int, int) {
				if c.levels["warlock"] > 0 && bookOfShadowsOpen(c.id) {
					return 0, 1
				}
				return 0, 0
			},
			request: characterRequest("/api/characters/invocations", `"invocation": "pact-of-the-tome", "cantrips": [...], "rituals": [...]`, ""),
			step:    bookOfShadowsStep},
		{key: "expertise", feature: "Expertise", one: "a skill you are proficient in", many: "skills",
			total: func(class, _ string, level int) int { return game.ExpertiseCount(map[string]int{class: level}) },
			state: func(c *choiceChar) (int, int) {
				st := loadExpertise(c.id)
				return len(st.picks), st.total
			},
			request: func(id int) choiceRequest {
				return choiceRequest{method: "POST", path: expertisePath(id), body: `{"skills": [...]}`, how: "with all your Expertise skills"}
			},
			gain: func(_ int, class, _ string, level int) string {
				var parts []string
				for _, g := range game.ExpertiseGrantsAtLevel(class, level) {
					parts = append(parts, g.Describe())
				}
				return strings.Join(parts, "; ")
			},
			step: func(c *choiceChar) *setupStep { return expertiseSetupStep(c.id) }},
		{key: "subclass", feature: "Subclass", one: "a subclass",
			total: func(_, _ string, level int) int { return fromLevel(3, 1)(level) },
			state: func(c *choiceChar) (have, want int) {
				for class, level := range c.levels {
					if level >= 3 {
						want++
						if c.subs[class] != "" {
							have++
						}
					}
				}
				return have, want
			},
			line:    func(*choiceChar, int, int) string { return "choose a subclass" },
			request: characterRequest("/api/characters/subclass", `"subclass": "..."`, ""),
			step:    subclassStep},
		{key: "metamagic", feature: "Metamagic", one: "a Metamagic option", many: "Metamagic options",
			total:   classOnly("sorcerer", getMaxMetamagicChoices),
			have:    func(c *choiceChar) int { return len(getMetamagicChoices(c.id)) },
			request: characterRequest("/api/characters/metamagic", `"metamagic": "..."`, "one per request"),
			step:    metamagicStep},
		{key: "wild_shape_forms", feature: "Wild Shape", one: "a Beast form", many: "known Beast forms",
			total: classOnly("druid", func(level int) int {
				if level < 2 {
					return 0
				}
				known, _, _ := wildShapeLimits(level)
				return known
			}),
			have: func(c *choiceChar) int { return len(knownWildShapes(c.id)) },
			request: func(id int) choiceRequest {
				r := classOptionsRequest(`{"wild_shape_forms": [...]}`)(id)
				r.how = "with all of them"
				return r
			},
			step: wildShapeStep, field: "wild_shape_forms", set: setWildShapeChoice},
		pick("circle_land"),
		pick("hunters_prey"),
		pick("defensive_tactics"),
		{key: "primal_knowledge", feature: "Primal Knowledge", one: "one more skill from the Barbarian skill list",
			total:   classOnly("barbarian", fromLevel(3, 1)),
			have:    func(c *choiceChar) int { return pickCount(c, "primal_knowledge") },
			request: classOptionsRequest(`{"primal_knowledge": "..."}`),
			step:    primalKnowledgeStep, field: "primal_knowledge", set: setPrimalKnowledge},
		{key: "bonus_proficiencies", feature: "Bonus Proficiencies", one: "a skill", many: "skills of your choice",
			total: func(class, sub string, level int) int {
				if class == "bard" && sub == "lore" {
					return fromLevel(3, 3)(level)
				}
				return 0
			},
			have:    func(c *choiceChar) int { return pickCount(c, "bonus_proficiencies") },
			request: classOptionsRequest(`{"bonus_proficiencies": ["...", "...", "..."]}`),
			step:    bonusProficienciesStep, field: "bonus_proficiencies", set: setBonusProficiencies},
		{key: "asi_or_feat", feature: "Ability Score Improvement",
			total: func(class, _ string, level int) int {
				n := 0
				for _, l := range asiLevelsFor(class) {
					if l <= level {
						n++
					}
				}
				return n
			},
			state: func(c *choiceChar) (int, int) { // open while there are points to spend
				var pending int
				db.QueryRow(`SELECT COALESCE(pending_asi,0) FROM characters WHERE id = $1`, c.id).Scan(&pending)
				return 0, pending
			},
			line: func(_ *choiceChar, _, pending int) string {
				return fmt.Sprintf("%d point(s) to spend: raise ability scores, or take a feat for 2 points", pending)
			},
			gain: func(_ int, _, _ string, level int) string {
				if level >= 19 {
					return "Epic Boon: choose an Epic Boon feat or another feat you qualify for"
				}
				return "+2 to one ability score or +1 to two, or a feat you qualify for instead"
			},
			request: func(id int) choiceRequest {
				return choiceRequest{method: "POST", path: fmt.Sprintf("/api/characters/%d/asi", id), body: `{"ability": "...", "points": 1 or 2}`,
					how: fmt.Sprintf(`or POST /api/characters/%d/feat {"feat": "..."}`, id)}
			},
			step: func(c *choiceChar) *setupStep { return asiSetupStep(c.id) }},
		pick("blessed_strikes"),
		pick("elemental_fury"),
		pick("elemental_affinity"),
		{key: "mystic_arcanum", feature: "Mystic Arcanum", one: "a Warlock spell", many: "Mystic Arcanum spells",
			total: classOnly("warlock", func(level int) int { return len(getAvailableMysticArcanumLevels(level)) }),
			have:  func(c *choiceChar) int { return len(mysticArcanumChosen(c.id, c.levels["warlock"])) },
			gain: func(_ int, _, _ string, level int) string {
				l := getAvailableMysticArcanumLevels(level)
				return fmt.Sprintf("choose a level %d Warlock spell", l[len(l)-1])
			},
			request: characterRequest("/api/characters/mystic-arcanum", `"spell_level": N, "spell_slug": "..."`, "one per request"),
			step:    func(c *choiceChar) *setupStep { return mysticArcanumStep(c.id) }},
		{key: "spell_mastery", feature: "Spell Mastery", one: "a spell", many: "spells from your spellbook (one of level 1 and one of level 2, each cast with an action)",
			total:   classOnly("wizard", fromLevel(18, 2)),
			have:    func(c *choiceChar) int { return jsonCount(c.id, "spell_mastery") },
			request: characterRequest("/api/characters/spell-mastery", `"spells": ["...", "..."]`, ""),
			step:    func(c *choiceChar) *setupStep { return spellMasteryStep(c.id) }},
		{key: "signature_spells", feature: "Signature Spells", one: "a spell", many: "level 3 spells from your spellbook",
			total:   classOnly("wizard", fromLevel(20, 2)),
			have:    func(c *choiceChar) int { return jsonCount(c.id, "signature_spells") },
			request: characterRequest("/api/characters/signature-spells", `"action": "choose", "spell": "..."`, "one per request"),
			step:    func(c *choiceChar) *setupStep { return signatureSpellsStep(c.id) }},
	}
	for i := range classLangDefs {
		choiceDefs = append(choiceDefs, classLangDefs[i].choice())
	}
	choiceDefs = append(choiceDefs,
		&choiceDef{key: "magical_discoveries", feature: "Magical Discoveries", one: "a spell", many: "spells from the Cleric, Druid or Wizard lists",
			total: func(class, sub string, level int) int {
				if class == "bard" && sub == "lore" {
					return fromLevel(6, 2)(level)
				}
				return 0
			},
			have:    func(c *choiceChar) int { return len(magicalDiscoveriesOf(c.id)) },
			request: classOptionsRequest(`{"magical_discoveries": ["...", "..."]}`),
			step:    func(c *choiceChar) *setupStep { return magicalDiscoveriesStep(c.id) }, field: "magical_discoveries", set: setMagicalDiscoveriesChoice},
		&choiceDef{key: "weapon_mastery", feature: "Weapon Mastery", one: "a kind of weapon", many: "kinds of weapon",
			total:   func(class, _ string, level int) int { return masteryCount(class, level) },
			have:    func(c *choiceChar) int { return jsonCount(c.id, "weapon_masteries") },
			request: characterRequest("/api/characters/weapon-masteries", `"weapon_masteries": [...]`, "with all of them"),
			step:    func(c *choiceChar) *setupStep { return masterySetupStep(c.id) }},
		spellsChoice(),
		&choiceDef{key: "auto_equip", feature: "Equipment",
			state: func(c *choiceChar) (int, int) {
				if autoEquipOpen(c.id) {
					return 0, 1
				}
				return 0, 0
			},
			line: func(*choiceChar, int, int) string {
				return "do you want me to put on your armor, arm your weapons and wear your gear for you? If you have a choice to make (two weapons, two Robes) I will ask you which"
			},
			request: characterRequest("/api/characters/auto-equip", `"answer": "yes"`, `or "no"`),
			step:    autoEquipStep},
	)
}

// pickCount: how many answers a stored pick holds (a list is stored joined with commas).
func pickCount(c *choiceChar, key string) int {
	n := 0
	for _, s := range strings.Split(c.picks[key], ",") {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}

// choiceOpen: the choice with this key has picks still to make.
func choiceOpen(c *choiceChar, key string) bool {
	for _, d := range choiceDefs {
		if d.key == key {
			have, want := d.status(c)
			return have < want
		}
	}
	return false
}

// unsuggest clears the "suggested" marks of a step's options (a waiting step lists them without a suggestion).
func unsuggest(v interface{}) {
	switch x := v.(type) {
	case map[string]interface{}:
		if _, ok := x["suggested"]; ok {
			x["suggested"] = false
		}
		for _, e := range x {
			unsuggest(e)
		}
	case []map[string]interface{}:
		for _, e := range x {
			unsuggest(e)
		}
	case []interface{}:
		for _, e := range x {
			unsuggest(e)
		}
	}
}
