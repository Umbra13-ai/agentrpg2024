package main

// Character builder (F234, Nov 10). the project owner: in the game that began today the AI players didn't assign their ability scores properly,
// didn't equip their gear and the casters didn't choose spells; "look at how D&D Beyond guides people through character creation".
// The builder follows the order of the 2024 Player's Handbook and the D&D Beyond builder: Class (with the class's own choices),
// Background, Species, Ability Scores, Details (name and languages), Review; then, once the character exists, Spells and gear
// (GET /api/characters/{id}/setup). Each step says what to choose, how many, the valid options and a suggestion (the PHB's
// Standard Array by Class, the weapons of the starting kit for Weapon Mastery ...), and counts the choices left, like the builder's
// step tabs. The draft is kept on the server, so a player can stop and come back.
//
//	POST /api/character-builder                     start a draft ({"name": "..."} optional)
//	GET  /api/character-builder                     your drafts
//	GET  /api/character-builder/{id}                the steps, the current step in detail, the draft
//	POST /api/character-builder/{id}                set fields (any creation field; null removes one), or {"use_suggestion": true}
//	POST /api/character-builder/{id}/finish        create the character from the draft
//	GET  /api/characters/{id}/setup                 after creation: spells, gear in hand, class choices left, with the requests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

var builderTableOnce sync.Once

func ensureBuilderTable() {
	builderTableOnce.Do(func() {
		db.Exec(`CREATE TABLE IF NOT EXISTS character_drafts (
			id SERIAL PRIMARY KEY,
			agent_id INTEGER NOT NULL,
			body JSONB NOT NULL DEFAULT '{}',
			character_id INTEGER,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	})
}

// phbArray is the 2024 Player's Handbook's Standard Array by Class table (Str, Dex, Con, Int, Wis, Cha).
var phbArray = map[string][6]int{
	"barbarian": {15, 13, 14, 10, 12, 8}, "bard": {8, 14, 12, 13, 10, 15}, "cleric": {14, 8, 13, 10, 15, 12},
	"druid": {8, 12, 14, 13, 15, 10}, "fighter": {15, 14, 13, 8, 10, 12}, "monk": {12, 15, 13, 10, 14, 8},
	"paladin": {15, 10, 13, 8, 12, 14}, "ranger": {12, 15, 13, 8, 14, 10}, "rogue": {12, 15, 13, 14, 10, 8},
	"sorcerer": {10, 13, 14, 8, 12, 15}, "warlock": {8, 14, 13, 12, 10, 15}, "wizard": {8, 12, 13, 15, 14, 10},
}

var abilityKeys6 = [6]string{"str", "dex", "con", "int", "wis", "cha"}

var classRoles = map[string]string{
	"barbarian": "a fierce warrior who Rages; Strength; d12 Hit Points; the toughest front-line fighter",
	"bard":      "a performer whose music is magic; Charisma; d8; spells, healing and support",
	"cleric":    "a priest who wields divine magic; Wisdom; d8; healing, protection and armor",
	"druid":     "a priest of nature; Wisdom; d8; nature spells, healing and (level 2) Wild Shape",
	"fighter":   "a master of weapons and armor; Strength or Dexterity; d10; the steadiest warrior",
	"monk":      "a martial artist; Dexterity and Wisdom; d8; fast unarmed strikes, no armor",
	"paladin":   "a holy warrior; Strength and Charisma; d10; heavy armor, healing touch, smites",
	"ranger":    "a wilderness warrior; Dexterity and Wisdom; d10; bows, tracking, nature spells",
	"rogue":     "a sneak and expert; Dexterity; d8; Sneak Attack and the most skills",
	"sorcerer":  "a born spellcaster; Charisma; d6; strong spells shaped by Metamagic",
	"warlock":   "a caster with an otherworldly patron; Charisma; d8; Eldritch Blast and invocations",
	"wizard":    "a scholar of arcane magic; Intelligence; d6; the widest choice of spells",
}

// skillPrefs: the order the builder suggests a class's skills in (the first ones the class may take and doesn't have already).
var skillPrefs = map[string][]string{
	"barbarian": {"perception", "survival", "athletics", "intimidation", "animal handling", "nature"},
	"bard":      {"perception", "stealth", "insight", "persuasion", "deception", "performance", "acrobatics", "arcana"},
	"cleric":    {"insight", "medicine", "persuasion", "history", "religion"},
	"druid":     {"perception", "medicine", "nature", "insight", "survival", "animal handling", "arcana", "religion"},
	"fighter":   {"perception", "athletics", "survival", "intimidation", "acrobatics", "insight", "history", "persuasion"},
	"monk":      {"acrobatics", "stealth", "insight", "athletics", "history", "religion"},
	"paladin":   {"athletics", "persuasion", "insight", "intimidation", "medicine", "religion"},
	"ranger":    {"perception", "stealth", "survival", "nature", "insight", "investigation", "athletics", "animal handling"},
	"rogue":     {"perception", "stealth", "acrobatics", "investigation", "insight", "deception", "sleight of hand", "athletics", "persuasion"},
	"sorcerer":  {"persuasion", "insight", "arcana", "deception", "intimidation", "religion"},
	"warlock":   {"deception", "arcana", "intimidation", "investigation", "history", "nature", "religion"},
	"wizard":    {"investigation", "insight", "arcana", "history", "medicine", "religion", "nature"},
}

var masteryFallback = map[string][]string{
	"barbarian": {"greataxe", "handaxe", "javelin", "greatsword"}, "fighter": {"greatsword", "flail", "javelin", "longsword", "longbow"},
	"paladin": {"longsword", "javelin", "warhammer", "greatsword"}, "ranger": {"longbow", "shortsword", "scimitar", "shortbow"},
	"rogue": {"dagger", "shortbow", "shortsword", "rapier"},
}

// spellPrefs: cantrips and level 1 spells the setup suggests, in order (only those on the class's list are used).
var cantripPrefs = map[string][]string{
	"bard": {"vicious-mockery", "minor-illusion", "mage-hand", "light"}, "cleric": {"sacred-flame", "guidance", "thaumaturgy", "spare-the-dying", "light"},
	"druid": {"produce-flame", "guidance", "shillelagh", "druidcraft"}, "sorcerer": {"fire-bolt", "ray-of-frost", "mage-hand", "prestidigitation", "light"},
	"warlock": {"eldritch-blast", "minor-illusion", "mage-hand", "prestidigitation"}, "wizard": {"fire-bolt", "mage-hand", "prestidigitation", "ray-of-frost", "light"},
}
var spellPrefs = map[string][]string{
	"bard": {"healing-word", "dissonant-whispers", "faerie-fire", "sleep", "charm-person"}, "cleric": {"bless", "healing-word", "cure-wounds", "guiding-bolt", "shield-of-faith"},
	"druid": {"healing-word", "entangle", "faerie-fire", "cure-wounds", "thunderwave"}, "paladin": {"bless", "cure-wounds", "shield-of-faith", "heroism", "command"},
	"ranger": {"cure-wounds", "goodberry", "ensnaring-strike", "fog-cloud", "hunters-mark"}, "sorcerer": {"magic-missile", "shield", "burning-hands", "sleep"},
	"warlock": {"hex", "armor-of-agathys", "hellish-rebuke", "charm-person"}, "wizard": {"magic-missile", "shield", "mage-armor", "sleep", "detect-magic", "find-familiar", "burning-hands"},
}

// spell2Prefs: level 2 spells the setup suggests from character level 3.
var spell2Prefs = map[string][]string{
	"bard": {"hold-person", "invisibility", "shatter", "heat-metal"}, "cleric": {"spiritual-weapon", "aid", "lesser-restoration", "hold-person"},
	"druid": {"moonbeam", "flaming-sphere", "lesser-restoration", "barkskin"}, "sorcerer": {"scorching-ray", "misty-step", "shatter", "hold-person"},
	"warlock": {"misty-step", "hold-person", "invisibility", "shatter"}, "wizard": {"misty-step", "scorching-ray", "shatter", "web"},
}

// srdSubclassFor: the SRD 5.2.1 subclass of each class.
var srdSubclassFor = map[string]string{"barbarian": "berserker", "bard": "lore", "cleric": "life", "druid": "land", "fighter": "champion",
	"monk": "open-hand", "paladin": "devotion", "ranger": "hunter", "rogue": "thief", "sorcerer": "draconic", "warlock": "fiend", "wizard": "evocation"}

// classLevelsOrSingle: the character's class levels (one class: its level).
func classLevelsOrSingle(charID int) map[string]int {
	if l := classLevelsOf(charID); len(l) > 0 {
		return l
	}
	class, _, level := subclassOf(charID)
	return map[string]int{strings.ToLower(class): level}
}

// suggestWildShapeForms: Beasts a Druid of this level may know (CR limit, no Fly Speed before level 8), well-known ones first.
func suggestWildShapeForms(charID int) []string {
	_, _, level := subclassOf(charID)
	known, maxCR, fly := wildShapeLimits(level)
	rows, err := db.Query(`SELECT slug, cr, COALESCE(speed_text,'') FROM monsters WHERE LOWER(type) = 'beast' ORDER BY slug`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ok []string
	for rows.Next() {
		var slug, cr, speed string
		if rows.Scan(&slug, &cr, &speed) == nil && crValue(cr) <= maxCR && (fly || !strings.Contains(strings.ToLower(speed), "fly")) {
			ok = append(ok, slug)
		}
	}
	var out []string
	for _, p := range []string{"wolf", "boar", "riding-horse", "giant-badger", "panther", "giant-spider", "brown-bear", "dire-wolf", "giant-eagle"} {
		if len(out) < known && containsStr(ok, p) {
			out = append(out, p)
		}
	}
	for _, s := range ok {
		if len(out) < known && !containsStr(out, s) {
			out = append(out, s)
		}
	}
	return out
}

var speciesFor = map[string]string{"barbarian": "goliath", "bard": "human", "cleric": "dwarf", "druid": "elf", "fighter": "dwarf", "monk": "human",
	"paladin": "dragonborn", "ranger": "elf", "rogue": "halfling", "sorcerer": "tiefling", "warlock": "tiefling", "wizard": "elf"}
var speciesLanguage = map[string]string{"dwarf": "Dwarvish", "elf": "Elvish", "gnome": "Gnomish", "halfling": "Halfling", "orc": "Orc",
	"dragonborn": "Draconic", "goliath": "Giant"}
var standardLanguages = []string{"Elvish", "Dwarvish", "Halfling", "Gnomish", "Giant", "Draconic", "Orc", "Goblin", "Common Sign Language"}

// ---------------------------------------------------------------- small helpers

func bstr(b map[string]interface{}, k string) string {
	s, _ := b[k].(string)
	return strings.TrimSpace(s)
}

func bint(b map[string]interface{}, k string) int {
	switch v := b[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func blist(b map[string]interface{}, k string) []string {
	var out []string
	switch v := b[k].(type) {
	case []interface{}:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	case []string:
		for _, s := range v {
			out = append(out, strings.ToLower(strings.TrimSpace(s)))
		}
	}
	return out
}

func bmap(b map[string]interface{}, k string) map[string]interface{} {
	m, _ := b[k].(map[string]interface{})
	return m
}

func classSkillChoices(class string) (int, []string) {
	var list string
	var n int
	db.QueryRow(`SELECT COALESCE(skill_choices,''), COALESCE(num_skill_choices,2) FROM classes WHERE slug = $1`, class).Scan(&list, &n)
	var out []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	if n == 0 {
		n = 2
	}
	return n, out
}

func pickFrom(prefs, allowed []string, avoid map[string]bool, n int) []string {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var out []string
	for _, list := range [][]string{prefs, allowed} {
		for _, p := range list {
			if len(out) >= n {
				return out
			}
			if (len(allowed) == 0 || ok[p]) && !avoid[p] {
				out = append(out, p)
				avoid[p] = true
			}
		}
	}
	return out
}

func kitText(class, opt string) string {
	o := classStartingEquipment[class][opt]
	var items []string
	for _, it := range o.Items {
		name := it.Name
		if name == choiceTool {
			name = "your chosen tool"
		}
		if it.Qty > 1 {
			name = fmt.Sprintf("%s (%d)", name, it.Qty)
		}
		items = append(items, name)
	}
	if len(items) == 0 {
		return fmt.Sprintf("%d GP and no equipment (buy your own)", o.Gold)
	}
	return fmt.Sprintf("%s and %d GP", strings.Join(items, ", "), o.Gold)
}

// classAbilityOrder: the class's abilities from the PHB array, highest first.
func classAbilityOrder(class string) []string {
	arr, ok := phbArray[class]
	if !ok {
		return abilityKeys6[:]
	}
	idx := []int{0, 1, 2, 3, 4, 5}
	sort.SliceStable(idx, func(a, b int) bool { return arr[idx[a]] > arr[idx[b]] })
	var out []string
	for _, i := range idx {
		out = append(out, abilityKeys6[i])
	}
	return out
}

func mentalAbility(class string) string {
	for _, a := range classAbilityOrder(class) {
		if a == "int" || a == "wis" || a == "cha" {
			return a
		}
	}
	return "wis"
}

// ---------------------------------------------------------------- steps

type builderStep struct {
	Key        string                   `json:"key"`
	Title      string                   `json:"title"`
	Done       bool                     `json:"done"`
	Remaining  int                      `json:"choices_remaining"`
	Explain    string                   `json:"explain,omitempty"`
	Choices    []map[string]interface{} `json:"choices,omitempty"`
	Suggestion map[string]interface{}   `json:"suggestion,omitempty"`
	Warnings   []string                 `json:"warnings,omitempty"`
	Problems   []string                 `json:"problems,omitempty"`
}

func choice(field, what string, count int, options interface{}, current interface{}, ok bool, problem string) map[string]interface{} {
	c := map[string]interface{}{"field": field, "what": what, "done": ok}
	if count > 0 {
		c["count"] = count
	}
	if options != nil {
		c["options"] = options
	}
	if current != nil {
		c["current"] = current
	}
	if problem != "" {
		c["problem"] = problem
	}
	return c
}

func builderSteps(body map[string]interface{}, agentID int, withCheck bool) []builderStep {
	class := strings.ToLower(bstr(body, "class"))
	_, classOK := srdClasses[class]
	bgKey := strings.ToLower(bstr(body, "background"))
	bg := game.GetBackground(bgKey)
	spKey := strings.ToLower(bstr(body, "species"))
	sp := game.GetSpecies(spKey)
	var steps []builderStep

	// 1. Class
	s1 := builderStep{Key: "class", Title: "Step 1: Choose a class",
		Explain: "Your class is what your character does best: fighting, spells, sneaking, healing. Choose the one you want to play; the next steps follow from it."}
	var classOpts []map[string]string
	for _, c := range []string{"barbarian", "bard", "cleric", "druid", "fighter", "monk", "paladin", "ranger", "rogue", "sorcerer", "warlock", "wizard"} {
		classOpts = append(classOpts, map[string]string{"class": c, "is": classRoles[c]})
	}
	s1.Done = classOK
	s1.Choices = append(s1.Choices, choice("class", "your class", 1, classOpts, nilIfEmpty(class), classOK, ifStr(class != "" && !classOK, "not one of the 12 classes")))
	if !classOK {
		s1.Remaining = 1
	}
	steps = append(steps, s1)

	// 2. Class choices
	s2 := builderStep{Key: "class_choices", Title: "Step 2: Your class's choices",
		Explain: "Each class makes its own choices at level 1: skills, and for some Weapon Mastery, instruments, Expertise, a Divine or Primal Order, and the starting equipment. Spells are chosen after the character is made (Step 7)."}
	if classOK {
		n, allowed := classSkillChoices(class)
		have := blist(body, "skill_proficiencies")
		avoid := map[string]bool{}
		if bg != nil {
			for _, s := range bg.Skills {
				avoid[strings.ToLower(s)] = true
			}
		}
		prob := ""
		okSkills := len(have) == n
		allowedSet := map[string]bool{}
		for _, a := range allowed {
			allowedSet[a] = true
		}
		for _, h := range have {
			if len(allowed) > 0 && !allowedSet[strings.NewReplacer("_", " ", "-", " ").Replace(h)] {
				okSkills, prob = false, h+" isn't one of your class's skills"
			}
			if avoid[h] {
				okSkills, prob = false, h+" also comes from your background: choose another so you don't lose a proficiency"
			}
		}
		if len(have) != n && prob == "" && len(have) > 0 {
			prob = fmt.Sprintf("choose exactly %d", n)
		}
		s2.Choices = append(s2.Choices, choice("skill_proficiencies", fmt.Sprintf("%d skills from your class's list", n), n, allowed, have, okSkills, prob))
		sug := map[string]interface{}{"skill_proficiencies": pickFrom(skillPrefs[class], allowed, avoid, n)}
		if !okSkills {
			s2.Remaining++
		}
		if mc := masteryCount(class, 1); mc > 0 {
			var kit []string
			for _, it := range classStartingEquipment[class]["a"].Items {
				if _, _, ok := findWeapon(it.Name); ok {
					kit = append(kit, strings.ToLower(it.Name))
				}
			}
			m := blist(body, "weapon_masteries")
			okM := len(m) == mc
			s2.Choices = append(s2.Choices, choice("weapon_masteries", fmt.Sprintf("Weapon Mastery: %d kinds of weapon you are proficient with (the weapons of your starting kit are a good choice: %s)", mc, strings.Join(kit, ", ")), mc, nil, m, okM, ifStr(len(m) > 0 && !okM, fmt.Sprintf("choose exactly %d", mc))))
			sug["weapon_masteries"] = pickFrom(append(kit, masteryFallback[class]...), nil, map[string]bool{}, mc)
			if !okM {
				s2.Remaining++
			}
		}
		if tc := game.ToolChoicesFor(class, nil); len(tc) > 0 {
			tools := blist(body, "tool_proficiencies")
			pending := game.PendingToolChoices(tc, strings.Join(tools, ", "))
			s2.Choices = append(s2.Choices, choice("tool_proficiencies", "tools your class lets you choose", tc[0].Count, tc[0].Categories, tools, len(pending) == 0, ""))
			if class == "bard" {
				sug["tool_proficiencies"] = []string{"lute", "flute", "drum"}
			} else {
				sug["tool_proficiencies"] = []string{"flute"}
			}
			if len(pending) > 0 {
				s2.Remaining++
			}
		}
		if class == "rogue" {
			e := blist(body, "expertise")
			s2.Choices = append(s2.Choices, choice("expertise", "Expertise: 2 of your skill proficiencies (class or background) get double your Proficiency Bonus", 2, nil, e, len(e) == 2, ""))
			sug["expertise"] = []string{"stealth", "perception"}
			if len(e) != 2 {
				s2.Remaining++
			}
		}
		if class == "cleric" || class == "druid" {
			field, opts := "divine_order", []string{"protector (heavy armor and Martial weapons)", "thaumaturge (one extra cantrip; add your Wisdom to Arcana and Religion checks)"}
			sv := "thaumaturge"
			if class == "druid" {
				field, opts = "primal_order", []string{"magician (one extra cantrip; add your Wisdom to Arcana and Nature checks)", "warden (Medium armor and Martial weapons)"}
				sv = "magician"
			}
			v := strings.ToLower(bstr(bmap(body, "class_options"), field))
			s2.Choices = append(s2.Choices, choice("class_options."+field, "your order", 1, opts, nilIfEmpty(v), v != "", ""))
			sug["class_options"] = map[string]interface{}{field: sv}
			if v == "" {
				s2.Remaining++
			}
		}
		var eqOpts []string
		for _, k := range startingOptionKeys(class) {
			eqOpts = append(eqOpts, k+": "+kitText(class, k))
		}
		eq := strings.ToLower(bstr(body, "starting_equipment"))
		s2.Choices = append(s2.Choices, choice("starting_equipment", "your starting equipment (the server has no shops yet: take the kit, \"a\")", 1, eqOpts, nilIfEmpty(eq), eq != "", ""))
		sug["starting_equipment"] = "a"
		if eq == "" {
			s2.Remaining++
		}
		s2.Suggestion = sug
	} else {
		s2.Remaining = 1
		s2.Problems = []string{"choose a class first"}
	}
	s2.Done = s2.Remaining == 0
	steps = append(steps, s2)

	// 3. Background
	s3 := builderStep{Key: "background", Title: "Step 3: Choose a background",
		Explain: "Your background is what you did before adventuring. It raises three of your abilities (in Step 5), and gives two skills, a tool, an Origin feat and equipment."}
	var bgOpts []map[string]interface{}
	bestBG, bestScore := "", -1
	for _, k := range game.BackgroundSlugs() {
		b := game.GetBackground(k)
		if b == nil {
			continue
		}
		bgOpts = append(bgOpts, map[string]interface{}{"background": b.Key, "abilities": b.AbilityScores, "origin_feat": strings.TrimSpace(b.Feat + " " + b.FeatNote), "skills": b.Skills, "tool": firstNonEmpty(b.Tool, "a "+b.ToolCategory+" set of your choice")})
		if arr, ok := phbArray[class]; ok {
			vals := []int{}
			for _, a := range b.AbilityScores {
				for i, k6 := range abilityKeys6 {
					if k6 == a {
						vals = append(vals, arr[i])
					}
				}
			}
			sort.Sort(sort.Reverse(sort.IntSlice(vals)))
			if len(vals) >= 3 && (2*vals[0]+vals[1])*100+vals[2] > bestScore { // +2 and +1 where they count most; the third breaks a tie
				bestScore, bestBG = (2*vals[0]+vals[1])*100+vals[2], b.Key
			}
		}
	}
	s3.Choices = append(s3.Choices, choice("background", "your background", 1, bgOpts, nilIfEmpty(bgKey), bg != nil, ifStr(bgKey != "" && bg == nil, "not one of the backgrounds")))
	sug3 := map[string]interface{}{}
	if bestBG != "" {
		sug3["background"] = bestBG
	}
	if bg == nil {
		s3.Remaining++
	} else {
		if bg.ToolCategory != "" {
			t := bstr(body, "background_tool")
			s3.Choices = append(s3.Choices, choice("background_tool", "the kind of "+bg.ToolCategory+" set", 1, []string{"dice set", "dragonchess set", "playing card set", "three-dragon ante set"}, nilIfEmpty(t), t != "", ""))
			sug3["background_tool"] = "dice set"
			if t == "" {
				s3.Remaining++
			}
		}
		if bg.Feat == "magic_initiate" {
			list := strings.ToLower(bg.FeatNote)
			fc := bmap(body, "background_feat_choices")
			okF := fc != nil && len(blist(fc, "cantrips")) == 2 && bstr(fc, "spell") != "" && bstr(fc, "spellcasting_ability") != ""
			s3.Choices = append(s3.Choices, choice("background_feat_choices", fmt.Sprintf("Magic Initiate (%s): 2 cantrips and 1 level 1 spell from the %s list, and the ability you cast them with (int, wis or cha)", bg.FeatNote, bg.FeatNote), 1,
				fmt.Sprintf("GET /api/universe/class-spells/%s", list), fc, okF, ""))
			sug3["background_feat_choices"] = magicInitiateSuggestion(list, class)
			if !okF {
				s3.Remaining++
			}
		}
		be := strings.ToLower(bstr(body, "background_equipment"))
		s3.Choices = append(s3.Choices, choice("background_equipment", "the background's equipment (a: its package and a little gold; b: 50 GP)", 1, []string{"a", "b"}, nilIfEmpty(be), true, ""))
		sug3["background_equipment"] = "a"
	}
	s3.Suggestion = sug3
	s3.Done = s3.Remaining == 0
	steps = append(steps, s3)

	// 4. Species
	s4 := builderStep{Key: "species", Title: "Step 4: Choose a species",
		Explain: "Your species gives traits such as Darkvision, a size and a speed (no ability scores in the 2024 rules). Some species ask for a choice: a lineage, an ancestry, a size, a skill."}
	var spOpts []map[string]interface{}
	for _, s := range game.AllSpecies() {
		spOpts = append(spOpts, map[string]interface{}{"species": s.Key, "sizes": s.Sizes, "speed": s.Speed, "darkvision": s.Darkvision, "asks_for": s.Needs})
	}
	s4.Choices = append(s4.Choices, choice("species", "your species", 1, spOpts, nilIfEmpty(spKey), sp != nil, ifStr(spKey != "" && sp == nil, "not one of the nine species")))
	sug4 := map[string]interface{}{}
	if s, ok := speciesFor[class]; ok {
		sug4["species"] = s
	}
	if sp == nil {
		s4.Remaining++
	} else {
		sc := bmap(body, "species_choices")
		held := map[string]bool{}
		for _, s := range blist(body, "skill_proficiencies") {
			held[s] = true
		}
		if bg != nil {
			for _, s := range bg.Skills {
				held[strings.ToLower(s)] = true
			}
		}
		sugc := map[string]interface{}{}
		for _, need := range sp.Needs {
			v := bstr(sc, need)
			opts, sv := speciesChoiceOptions(sp.Key, need, class, held)
			s4.Choices = append(s4.Choices, choice("species_choices."+need, need, 1, opts, nilIfEmpty(v), v != "", ""))
			sugc[need] = sv
			if v == "" {
				s4.Remaining++
			}
		}
		if len(sugc) > 0 {
			sug4["species_choices"] = sugc
		}
		if sp.Key == "human" {
			feat := strings.ToLower(firstNonEmpty(bstr(sc, "feat"), "skilled"))
			if feat == "skilled" {
				fc := bmap(body, "species_feat_choices")
				p := blist(fc, "proficiencies")
				s4.Choices = append(s4.Choices, choice("species_feat_choices.proficiencies", "Skilled: 3 skills or tools you don't already have", 3, nil, p, len(p) == 3, ""))
				avoid := map[string]bool{}
				for k := range held {
					avoid[k] = true
				}
				if v := bstr(sc, "skill"); v != "" {
					avoid[strings.ToLower(v)] = true
				} else if v, _ := sugc["skill"].(string); v != "" {
					avoid[v] = true
				}
				sug4["species_feat_choices"] = map[string]interface{}{"proficiencies": pickFrom([]string{"perception", "stealth", "insight", "athletics", "persuasion", "survival", "investigation", "medicine"}, nil, avoid, 3)}
				if len(p) != 3 {
					s4.Remaining++
				}
			}
		}
	}
	s4.Suggestion = sug4
	s4.Done = s4.Remaining == 0
	steps = append(steps, s4)

	// 5. Ability scores
	s5 := builderStep{Key: "ability_scores", Title: "Step 5: Determine ability scores",
		Explain: "Generate six scores and give one to each ability: the Standard Array (15, 14, 13, 12, 10, 8), point buy (27 points, scores 8 to 15) or random (4d6 drop lowest). Put the highest in your class's main ability. Then your background raises its abilities: +2 to one and +1 to another (or +1 to all three)."}
	sug5 := map[string]interface{}{}
	if arr, ok := phbArray[class]; ok {
		sug5["ability_method"] = "standard_array"
		for i, k := range abilityKeys6 {
			sug5[k] = arr[i]
		}
		s5.Explain += fmt.Sprintf(" The Player's Handbook's suggestion for a %s: Str %d, Dex %d, Con %d, Int %d, Wis %d, Cha %d.", class, arr[0], arr[1], arr[2], arr[3], arr[4], arr[5])
	}
	method := strings.ToLower(bstr(body, "ability_method"))
	s5.Choices = append(s5.Choices, choice("ability_method", "how you generate the scores", 1, []string{"standard_array", "point_buy", "random"}, nilIfEmpty(method), method != "", ""))
	if method == "" {
		s5.Remaining++
	}
	scores := map[string]int{}
	missing := 0
	for _, k := range abilityKeys6 {
		scores[k] = bint(body, k)
		if scores[k] == 0 {
			missing++
		}
	}
	s5.Choices = append(s5.Choices, choice("str, dex, con, int, wis, cha", "one score for each ability", 6, nil, scores, missing == 0, ifStr(missing > 0 && missing < 6, fmt.Sprintf("%d abilities have no score", missing))))
	if missing > 0 && method != "random" {
		s5.Remaining++
	}
	if method == "standard_array" && missing == 0 && !isStandardArray([]int{scores["str"], scores["dex"], scores["con"], scores["int"], scores["wis"], scores["cha"]}) {
		s5.Problems = append(s5.Problems, "the Standard Array is 15, 14, 13, 12, 10 and 8, each used once")
		s5.Remaining++
	}
	if method == "point_buy" && missing == 0 {
		if t := pointBuyTotal([]int{scores["str"], scores["dex"], scores["con"], scores["int"], scores["wis"], scores["cha"]}); t < 0 || t > 27 {
			s5.Problems = append(s5.Problems, "point buy: every score 8 to 15, 27 points at most")
			s5.Remaining++
		} else if t < 27 {
			s5.Warnings = append(s5.Warnings, fmt.Sprintf("point buy: you spent %d of 27 points; the rest are lost", t))
		}
	}
	inc := map[string]int{}
	for k, v := range bmap(body, "ability_increases") {
		if f, ok := v.(float64); ok {
			inc[strings.ToLower(k)] = int(f)
		}
	}
	if bg != nil {
		order := classAbilityOrder(class)
		var ranked []string
		for _, a := range order {
			for _, b := range bg.AbilityScores {
				if a == b {
					ranked = append(ranked, a)
				}
			}
		}
		if len(ranked) >= 2 {
			sug5["ability_increases"] = map[string]int{ranked[0]: 2, ranked[1]: 1}
		}
		okInc := len(inc) > 0 && game.ValidateAbilityIncreases(bg, inc) == nil
		s5.Choices = append(s5.Choices, choice("ability_increases", fmt.Sprintf("your background's increases among %s: +2 and +1, or +1 to all three", strings.Join(bg.AbilityScores, ", ")), 1, bg.AbilityScores, inc, okInc, ""))
		if !okInc {
			s5.Remaining++
		}
		if okInc && missing == 0 {
			final := map[string]int{}
			for _, k := range abilityKeys6 {
				final[k] = scores[k] + inc[k]
			}
			s5.Choices = append(s5.Choices, map[string]interface{}{"field": "(result)", "what": "your scores after the background", "current": final})
			if order := classAbilityOrder(class); len(order) > 0 && classOK {
				top := order[0]
				for _, k := range abilityKeys6 {
					if final[k] > final[top] {
						s5.Warnings = append(s5.Warnings, fmt.Sprintf("your highest score is %s (%d), but a %s's main ability is %s (%d): most of your attacks, spells or features use %s", strings.ToUpper(k), final[k], class, strings.ToUpper(top), final[top], strings.ToUpper(top)))
						break
					}
				}
			}
		}
	} else {
		s5.Remaining++
		s5.Problems = append(s5.Problems, "choose a background first (Step 3): it decides which abilities rise")
	}
	s5.Suggestion = sug5
	s5.Done = s5.Remaining == 0
	steps = append(steps, s5)

	// 6. Details
	s6 := builderStep{Key: "details", Title: "Step 6: Name and languages",
		Explain: "You know Common and two more Standard Languages. Give your character a name."}
	name := bstr(body, "name")
	s6.Choices = append(s6.Choices, choice("name", "your character's name", 1, nil, nilIfEmpty(name), name != "", ""))
	if name == "" {
		s6.Remaining++
	}
	langs := blist(body, "extra_languages")
	s6.Choices = append(s6.Choices, choice("extra_languages", "two Standard Languages besides Common", 2, standardLanguages, langs, len(langs) == 2, ""))
	if len(langs) != 2 {
		s6.Remaining++
	}
	var sugL []string
	if l, ok := speciesLanguage[spKey]; ok {
		sugL = append(sugL, l)
	}
	for _, l := range standardLanguages {
		if len(sugL) < 2 && (len(sugL) == 0 || sugL[0] != l) {
			sugL = append(sugL, l)
		}
	}
	s6.Suggestion = map[string]interface{}{"extra_languages": sugL}
	s6.Done = s6.Remaining == 0
	steps = append(steps, s6)

	// 7. Review: the server checks the draft as the creation would
	s7 := builderStep{Key: "review", Title: "Step 7: Review and create",
		Explain: "When steps 1-6 are done, the server checks the whole draft as a creation would. Then POST /api/character-builder/{id}/finish creates the character; GET /api/characters/{id}/setup then guides the spells and the gear in hand."}
	allDone := true
	for _, s := range steps {
		allDone = allDone && s.Done
	}
	if !allDone {
		s7.Remaining = 1
		s7.Problems = []string{"finish steps 1-6 first"}
	} else if withCheck {
		if res := builderDryRun(body, agentID); res["success"] != true {
			s7.Remaining = 1
			s7.Problems = []string{fmt.Sprintf("%v: %v", res["error"], res["message"])}
			if vo, ok := res["valid_options"]; ok {
				s7.Choices = append(s7.Choices, map[string]interface{}{"field": "(server)", "valid_options": vo})
			}
		} else {
			s7.Choices = append(s7.Choices, map[string]interface{}{"field": "(server)", "what": "the character this draft makes", "current": res["would_create"]})
		}
	}
	s7.Done = s7.Remaining == 0
	steps = append(steps, s7)
	return steps
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func ifStr(c bool, s string) string {
	if c {
		return s
	}
	return ""
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func onClassList(slug, class string) bool {
	for _, s := range getClassSpellList(class) {
		if s == slug {
			return true
		}
	}
	return false
}

// magicInitiateSuggestion: two cantrips and a spell from the feat's list that the class doesn't already get.
func magicInitiateSuggestion(list, class string) map[string]interface{} {
	cands := map[string][]string{"cleric": {"guidance", "sacred-flame", "light", "thaumaturgy", "spare-the-dying"}, "wizard": {"fire-bolt", "mage-hand", "light", "minor-illusion", "prestidigitation"},
		"druid": {"guidance", "produce-flame", "druidcraft", "shillelagh"}}[list]
	spells := map[string][]string{"cleric": {"bless", "healing-word", "cure-wounds", "shield-of-faith"}, "wizard": {"magic-missile", "shield", "find-familiar", "detect-magic"},
		"druid": {"goodberry", "healing-word", "entangle"}}[list]
	classHas := map[string]bool{}
	if list == class {
		for _, s := range append(cantripPrefs[class], spellPrefs[class]...) {
			classHas[s] = true
		}
	}
	var cs []string
	for _, pass := range []bool{true, false} { // first what the class doesn't already get, then any
		for _, c := range cands {
			if len(cs) < 2 && (!pass || !classHas[c]) && onClassList(c, list) && !containsStr(cs, c) {
				cs = append(cs, c)
			}
		}
	}
	sp := ""
	for _, pass := range []bool{true, false} {
		for _, s := range spells {
			if sp == "" && (!pass || !classHas[s]) && onClassList(s, list) {
				sp = s
			}
		}
	}
	ab := map[string]string{"cleric": "wis", "wizard": "int", "druid": "wis"}[list]
	if _, ok := phbArray[class]; ok {
		ab = mentalAbility(class) // the class's best mental ability
	}
	return map[string]interface{}{"list": list, "cantrips": cs, "spell": sp, "spellcasting_ability": ab}
}

// speciesChoiceOptions: the options for one species choice and the builder's suggestion.
func speciesChoiceOptions(species, need, class string, held map[string]bool) ([]string, string) {
	mental := mentalAbility(class)
	switch need {
	case "ancestry":
		if species == "goliath" {
			return []string{"cloud", "fire", "frost", "hill", "stone", "storm"}, "stone"
		}
		return []string{"black", "blue", "brass", "bronze", "copper", "gold", "green", "red", "silver", "white"}, "red"
	case "lineage":
		if species == "gnome" {
			if class == "druid" || class == "ranger" {
				return []string{"forest", "rock"}, "forest"
			}
			return []string{"forest", "rock"}, "rock"
		}
		if class == "wizard" || class == "sorcerer" || class == "warlock" || class == "bard" {
			return []string{"drow", "high_elf", "wood_elf"}, "high_elf"
		}
		return []string{"drow", "high_elf", "wood_elf"}, "wood_elf"
	case "legacy":
		return []string{"abyssal", "chthonic", "infernal"}, "infernal"
	case "spellcasting_ability":
		return []string{"int", "wis", "cha"}, mental
	case "size":
		return []string{"medium", "small"}, "medium"
	case "skill":
		opts := []string{"insight", "perception", "survival"}
		if species == "human" {
			opts = nil // any skill
		}
		for _, s := range []string{"perception", "insight", "survival", "stealth", "athletics"} {
			if !held[s] && (opts == nil || containsStr(opts, s)) {
				return opts, s
			}
		}
		return opts, "survival"
	case "feat":
		return []string{"alert", "magic_initiate", "savage_attacker", "skilled"}, "skilled"
	}
	return nil, ""
}

// builderDryRun runs the creation checks on the draft without creating anything.
func builderDryRun(body map[string]interface{}, agentID int) map[string]interface{} {
	return runCreation(body, agentID, true)
}

func runCreation(body map[string]interface{}, agentID int, dry bool) map[string]interface{} {
	b := map[string]interface{}{}
	for k, v := range body {
		b[k] = v
	}
	if dry {
		b["dry_run"] = true
	}
	raw, _ := json.Marshal(b)
	req := httptest.NewRequest(http.MethodPost, "/api/characters", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), internalAgentKey{}, agentID))
	rec := httptest.NewRecorder()
	handleCharacters(rec, req)
	out := map[string]interface{}{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

// ---------------------------------------------------------------- handlers

func builderView(id int, body map[string]interface{}, charID *int, agentID int) map[string]interface{} {
	if charID != nil {
		return map[string]interface{}{"draft_id": id, "character_id": *charID, "draft": body,
			"next": fmt.Sprintf("The character exists (id %d). Finish it with GET /api/characters/%d/setup: spells, gear in hand, class choices left.", *charID, *charID)}
	}
	steps := builderSteps(body, agentID, true)
	var list []map[string]interface{}
	var current *builderStep
	total := 0
	for i := range steps {
		s := steps[i]
		total += s.Remaining
		e := map[string]interface{}{"step": i + 1, "key": s.Key, "title": s.Title, "done": s.Done, "choices_remaining": s.Remaining}
		if len(s.Warnings) > 0 {
			e["warnings"] = s.Warnings
		}
		if len(s.Problems) > 0 && s.Key != "review" {
			e["problems"] = s.Problems
		}
		list = append(list, e)
		if current == nil && !s.Done {
			current = &steps[i]
		}
	}
	out := map[string]interface{}{"draft_id": id, "steps": list, "choices_remaining": total, "draft": body}
	if charID != nil {
		out["character_id"] = *charID
		out["next"] = fmt.Sprintf("The character exists (id %d). Finish it with GET /api/characters/%d/setup: spells, gear in hand, class choices left.", *charID, *charID)
		return out
	}
	if current != nil {
		out["current_step"] = current
		send := map[string]interface{}{"method": "POST", "path": fmt.Sprintf("/api/character-builder/%d", id)}
		if len(current.Suggestion) > 0 {
			send["body"] = current.Suggestion
			send["or"] = map[string]interface{}{"use_suggestion": true}
		}
		out["send"] = send
		out["next"] = fmt.Sprintf("%s: %d choice(s) left. Send the fields (POST /api/character-builder/%d), or {\"use_suggestion\": true} to take the suggestion; GET this draft again any time.", current.Title, current.Remaining, id)
	} else {
		out["next"] = fmt.Sprintf("Every step is done. Create the character: POST /api/character-builder/%d/finish", id)
	}
	return out
}

func loadDraft(id, agentID int) (map[string]interface{}, *int, bool) {
	var raw []byte
	var cid *int
	var c int
	var hasChar bool
	if db.QueryRow(`SELECT body, COALESCE(character_id,0), character_id IS NOT NULL FROM character_drafts WHERE id = $1 AND agent_id = $2`, id, agentID).Scan(&raw, &c, &hasChar) != nil {
		return nil, nil, false
	}
	if hasChar {
		cid = &c
	}
	body := map[string]interface{}{}
	json.Unmarshal(raw, &body)
	return body, cid, true
}

func saveDraft(id int, body map[string]interface{}) {
	raw, _ := json.Marshal(body)
	db.Exec(`UPDATE character_drafts SET body = $1, updated_at = $2 WHERE id = $3`, raw, time.Now(), id)
}

// handleCharacterBuilder godoc
// @Summary Character builder: create a character step by step
// @Description Guides character creation in the order of the 2024 Player's Handbook (and the D&D Beyond builder): 1 class, 2 the class's choices (skills, Weapon Mastery, tools, Expertise, Divine/Primal Order, starting equipment), 3 background, 4 species, 5 ability scores (the PHB's Standard Array by Class is suggested), 6 name and languages, 7 review. POST starts a draft; GET /api/character-builder/{id} shows every step with the choices left, the current step's options and a suggestion; POST /api/character-builder/{id} with fields (any creation field; null removes one) or {"use_suggestion": true}; POST /api/character-builder/{id}/finish creates the character. Then GET /api/characters/{id}/setup guides spells and gear.
// @Tags Characters
// @Accept json
// @Produce json
// @Param request body object{name=string,use_suggestion=bool} false "Start a draft, or fields to set"
// @Success 200 {object} map[string]interface{}
// @Router /character-builder [post]
// @Router /character-builder [get]
// @Router /character-builder/{id} [get]
// @Router /character-builder/{id} [post]
// @Router /character-builder/{id}/finish [post]
func handleCharacterBuilder(w http.ResponseWriter, r *http.Request) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return
	}
	ensureBuilderTable()
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/character-builder"), "/")
	parts := strings.Split(rest, "/")
	if rest == "" {
		if r.Method == http.MethodGet {
			rows, err := db.Query(`SELECT id, body, character_id FROM character_drafts WHERE agent_id = $1 ORDER BY id DESC LIMIT 20`, agentID)
			list := []map[string]interface{}{}
			if err == nil {
				for rows.Next() {
					var id int
					var raw []byte
					var cid *int
					if rows.Scan(&id, &raw, &cid) == nil {
						b := map[string]interface{}{}
						json.Unmarshal(raw, &b)
						list = append(list, map[string]interface{}{"draft_id": id, "name": b["name"], "class": b["class"], "character_id": cid})
					}
				}
				rows.Close()
			}
			writeJSONStatus(w, 200, map[string]interface{}{"drafts": list, "start": "POST /api/character-builder {\"name\": \"...\"} starts a new draft"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "GET lists your drafts; POST starts one"})
			return
		}
		body := map[string]interface{}{}
		json.NewDecoder(r.Body).Decode(&body)
		delete(body, "use_suggestion")
		raw, _ := json.Marshal(body)
		var id int
		if db.QueryRow(`INSERT INTO character_drafts (agent_id, body) VALUES ($1, $2) RETURNING id`, agentID, raw).Scan(&id) != nil {
			writeJSONStatus(w, 500, map[string]interface{}{"error": "could_not_start_draft"})
			return
		}
		writeJSONStatus(w, 200, builderView(id, body, nil, agentID))
		return
	}
	id, _ := strconv.Atoi(parts[0])
	body, charID, ok := loadDraft(id, agentID)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "draft_not_found", "message": "GET /api/character-builder lists your drafts"})
		return
	}
	if len(parts) > 1 && parts[1] == "finish" {
		io.Copy(io.Discard, r.Body) // read the (empty) body: a reply sent before it is read can be reset on some systems
		if charID != nil {           // finished before (a retried request): the same answer
			writeJSONStatus(w, 200, map[string]interface{}{"success": true, "already_created": true, "character_id": *charID,
				"setup": fmt.Sprintf("GET /api/characters/%d/setup: your spells, the gear in your hands and the class choices left, step by step", *charID)})
			return
		}
		for _, s := range builderSteps(body, agentID, true) {
			if !s.Done {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "draft_not_finished", "message": s.Title + " still has choices to make.", "draft": builderView(id, body, nil, agentID)})
				return
			}
		}
		res := runCreation(body, agentID, false)
		if res["success"] != true {
			writeJSONStatus(w, http.StatusBadRequest, res)
			return
		}
		cid := bint(res, "character_id")
		db.Exec(`UPDATE character_drafts SET character_id = $1, updated_at = now() WHERE id = $2`, cid, id)
		res["setup"] = fmt.Sprintf("GET /api/characters/%d/setup: your spells, the gear in your hands and the class choices left, step by step", cid)
		writeJSONStatus(w, 200, res)
		return
	}
	if r.Method == http.MethodGet {
		writeJSONStatus(w, 200, builderView(id, body, charID, agentID))
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "GET shows the draft; POST sets fields"})
		return
	}
	if charID != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "already_created", "message": fmt.Sprintf("This draft made character %d. Use GET /api/characters/%d/setup.", *charID, *charID)})
		return
	}
	upd := map[string]interface{}{}
	if json.NewDecoder(r.Body).Decode(&upd) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	useSug := upd["use_suggestion"]
	delete(upd, "use_suggestion")
	delete(upd, "dry_run")
	if useSug == true || useSug != nil && useSug != false {
		key, _ := useSug.(string)
		for _, s := range builderSteps(body, agentID, false) {
			if (key != "" && s.Key == key) || (key == "" && !s.Done) {
				if len(s.Suggestion) == 0 {
					writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_suggestion", "message": s.Title + ": this choice is yours to make (see current_step.choices)."})
					return
				}
				// The suggestion fills the step's open choices and replaces the ones with a problem; a choice already made and in
				// order stays (the player's Acolyte isn't swapped for the suggested Sage). Asking for a step by name takes all of it.
				keep := map[string]bool{}
				openFull := map[string]bool{} // "species_choices.lineage" and the like
				if key == "" {
					open := map[string]bool{}
					for _, c := range s.Choices {
						f, _ := c["field"].(string)
						done, _ := c["done"].(bool)
						for _, part := range strings.Split(f, ", ") {
							top := strings.Split(part, ".")[0]
							if !done {
								open[top] = true
								openFull[part] = true
							}
						}
					}
					for k := range s.Suggestion {
						if _, set := body[k]; set && !open[k] {
							keep[k] = true
						}
					}
				}
				for k, v := range s.Suggestion { // fields sent with it still win (below)
					if keep[k] {
						continue
					}
					sub, isMap := v.(map[string]interface{})
					have, hasMap := body[k].(map[string]interface{})
					if key == "" && isMap && hasMap && !openFull[k] { // a group of choices: fill what is missing or wrong, keep the rest (an Elf's chosen lineage)
						for sk, sv := range sub {
							if _, set := have[sk]; !set || openFull[k+"."+sk] {
								have[sk] = sv
							}
						}
						body[k] = have
						continue
					}
					body[k] = v
				}
				break
			}
		}
	}
	for k, v := range upd {
		if v == nil {
			delete(body, k)
			continue
		}
		// A group of choices (species_choices, class_options, background_feat_choices ...) merges: the parts sent win, the rest stay;
		// a part sent as null is removed. Setting an Elf's lineage no longer wipes the suggested spellcasting ability and skill.
		if nm, isMap := v.(map[string]interface{}); isMap {
			if have, hasMap := body[k].(map[string]interface{}); hasMap {
				for sk, sv := range nm {
					if sv == nil {
						delete(have, sk)
					} else {
						have[sk] = sv
					}
				}
				body[k] = have
				continue
			}
		}
		body[k] = v
	}
	saveDraft(id, body)
	writeJSONStatus(w, 200, builderView(id, body, nil, agentID))
}

// suggestInvocation: an invocation the Warlock qualifies for and doesn't know (the setup guide's suggestion).
func suggestInvocation(charID int) string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(eldritch_invocations,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	known := map[string]bool{}
	for _, k := range jsonList(raw) {
		known[k] = true
	}
	ok := func(slug string) bool {
		inv, found := game.AvailableInvocations[slug]
		if !found || known[slug] {
			return false
		}
		m, _ := meetsInvocationPrerequisites(charID, inv)
		return m
	}
	for _, s := range []string{"pact-of-the-tome", "armor-of-shadows", "agonizing-blast", "eldritch-mind", "pact-of-the-blade", "pact-of-the-chain", "devils-sight"} {
		if ok(s) {
			return s
		}
	}
	var all []string
	for s := range game.AvailableInvocations {
		all = append(all, s)
	}
	sort.Strings(all)
	for _, s := range all {
		if ok(s) {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- after creation: GET /api/characters/{id}/setup

type setupStep struct {
	Key     string                 `json:"key"`
	Title   string                 `json:"title"`
	Done    bool                   `json:"done"`
	Why     string                 `json:"why,omitempty"`
	Request map[string]interface{} `json:"request,omitempty"`
	Then    *setupStep             `json:"then,omitempty"`
}

func req(method, path string, body interface{}) map[string]interface{} {
	r := map[string]interface{}{"method": method, "path": path}
	if body != nil {
		r["body"] = body
	}
	return r
}

// handleCharacterSetup godoc
// @Summary Finish a new character: spells, gear in hand, class choices left
// @Description After creation, the steps that remain, each with the request to send (suggested choices filled in; change them as you like): class choices (Divine/Primal Order, Fighting Style, Eldritch Invocations, Expertise ...), cantrips and spells, a weapon in hand, armor to put on; and warnings (your class's main ability isn't your highest score). character_finished is true when nothing is left.
// @Tags Characters
// @Produce json
// @Param id path int true "Character ID"
// @Success 200 {object} map[string]interface{}
// @Router /characters/{id}/setup [get]
func handleCharacterSetup(w http.ResponseWriter, r *http.Request, charID int) {
	var level, str, dex, con, intl, wis, cha int
	var class, mainHand string
	var shield bool
	var known, prepared []byte
	if db.QueryRow(`SELECT COALESCE(level,1), COALESCE(class,''), str, dex, con, intl, wis, cha, COALESCE(equipped_main_hand,''), COALESCE(equipped_shield,false),
		COALESCE(known_spells,'[]'), COALESCE(prepared_spells,'[]') FROM characters WHERE id = $1`, charID).
		Scan(&level, &class, &str, &dex, &con, &intl, &wis, &cha, &mainHand, &shield, &known, &prepared) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	cl := strings.ToLower(class)
	var steps []setupStep
	todo := characterToFinish(charID)
	has := func(word string) bool {
		for _, t := range todo {
			if strings.Contains(t, word) {
				return true
			}
		}
		return false
	}
	// class choices
	if has("Divine Order") {
		steps = append(steps, setupStep{Key: "divine_order", Title: "Choose your Divine Order", Why: "Protector: heavy armor and Martial weapons. Thaumaturge: one extra cantrip, and your Wisdom added to Arcana and Religion checks.",
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]string{"divine_order": "thaumaturge"})})
	}
	if has("Primal Order") {
		steps = append(steps, setupStep{Key: "primal_order", Title: "Choose your Primal Order", Why: "Magician: one extra cantrip, and your Wisdom added to Arcana and Nature checks. Warden: Medium armor and Martial weapons.",
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]string{"primal_order": "magician"})})
	}
	if has("Fighting Style") {
		style := "defense"
		if mainHand != "" {
			if isTwoHandedWeapon(mainHand) {
				style = "great_weapon_fighting"
			} else if _, wpn, ok := findWeapon(mainHand); ok && strings.Contains(strings.ToLower(wpn.Type+" "+strings.Join(wpn.Properties, " ")), "ranged") {
				style = "archery"
			}
		}
		steps = append(steps, setupStep{Key: "fighting_style", Title: "Choose a Fighting Style", Why: "Defense: +1 AC in armor. Great Weapon Fighting: better damage with two-handed weapons. Archery: +2 to ranged attacks. GET /api/characters/fighting-style?character_id=N lists them.",
			Request: req("POST", "/api/characters/fighting-style", map[string]interface{}{"character_id": charID, "style": style})})
	}
	if has("Eldritch Invocations") {
		pick := suggestInvocation(charID)
		st := setupStep{Key: "invocations", Title: "Choose your Eldritch Invocation", Why: fmt.Sprintf("GET /api/characters/invocations?character_id=%d lists the ones you qualify for (eligible: true); send the one you want. Pact of the Tome gives a book of extra cantrips and rituals; Armor of Shadows casts Mage Armor at will.", charID)}
		if pick != "" {
			st.Request = req("POST", "/api/characters/invocations", map[string]interface{}{"character_id": charID, "invocation": pick})
		} else {
			st.Request = req("GET", fmt.Sprintf("/api/characters/invocations?character_id=%d", charID), nil)
		}
		steps = append(steps, st)
	}
	if has("Expertise") {
		heldE, _ := heldSkills(charID)
		var cur []string
		var expCSV string
		db.QueryRow(`SELECT COALESCE(expertise,'') FROM characters WHERE id = $1`, charID).Scan(&expCSV)
		for _, e := range strings.Split(expCSV, ",") {
			if e = skillKey(e); e != "" {
				cur = append(cur, e)
			}
		}
		want := 0
		for c, l := range classLevelsOrSingle(charID) {
			want += expertiseAllowed(c, l)
		}
		prefs := []string{"stealth", "perception", "persuasion", "insight", "athletics", "acrobatics", "investigation", "sleight of hand", "deception", "arcana", "survival"}
		if cl == "wizard" {
			prefs = []string{"arcana", "investigation", "history", "religion", "nature", "medicine"} // Scholar's list
		}
		picks := append([]string{}, cur...)
		for _, s := range prefs {
			if len(picks) < want && heldE[s] && !containsStr(picks, s) {
				picks = append(picks, s)
			}
		}
		steps = append(steps, setupStep{Key: "expertise", Title: "Choose your Expertise skills", Why: fmt.Sprintf("%d of your skill proficiencies get double your Proficiency Bonus (send all of them).", want),
			Request: req("POST", fmt.Sprintf("/api/characters/%d/expertise", charID), map[string]interface{}{"skills": picks})})
	}
	held, _ := heldSkills(charID)
	if has("choose a subclass") {
		if sub := srdSubclassFor[cl]; sub != "" {
			steps = append(steps, setupStep{Key: "subclass", Title: "Choose your subclass", Why: fmt.Sprintf("Level 3: your subclass gives features now and at later levels. The SRD's %s subclass is %s; GET /api/characters/subclass?character_id=%d lists the choices.", cl, sub, charID),
				Request: req("POST", "/api/characters/subclass", map[string]interface{}{"character_id": charID, "subclass": sub})})
		}
	}
	if has("Metamagic") {
		known := getMetamagicChoices(charID)
		for _, m := range []string{"quickened", "twinned", "careful", "subtle", "extended", "empowered", "distant", "heightened"} {
			if !containsStr(known, m) {
				steps = append(steps, setupStep{Key: "metamagic", Title: "Choose a Metamagic option", Why: "Sorcerer level 2: two Metamagic options (one request each). Quickened casts a spell as a Bonus Action; Twinned adds a second target; Careful protects allies from your area spells. GET /api/characters/metamagic?character_id=N lists them.",
					Request: req("POST", "/api/characters/metamagic", map[string]interface{}{"character_id": charID, "metamagic": m})})
				break
			}
		}
	}
	if has("Wild Shape") {
		if forms := suggestWildShapeForms(charID); len(forms) > 0 {
			steps = append(steps, setupStep{Key: "wild_shape_forms", Title: "Choose your Wild Shape forms", Why: "Druid level 2: you know 4 Beast forms of CR 1/4 or lower with no Fly Speed (6 of CR 1/2 at level 4, 8 of CR 1 with flyers at level 8). GET /api/universe/monsters lists Beasts.",
				Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]interface{}{"wild_shape_forms": forms})})
		}
	}
	if has("Circle of the Land") {
		steps = append(steps, setupStep{Key: "land_type", Title: "Choose your Circle of the Land type", Why: "Arid, polar, temperate or tropical: it decides your circle spells (you can change it after a Long Rest).",
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]string{"land_type": "temperate"})})
	}
	if has("Hunter's Prey") {
		steps = append(steps, setupStep{Key: "hunters_prey", Title: "Choose your Hunter's Prey", Why: "Colossus Slayer: +1d8 damage once per turn against a creature below its Hit Point maximum. Horde Breaker: one more attack against a second creature near the first.",
			Request: req("POST", "/api/characters/subclass-choice", map[string]interface{}{"character_id": charID, "feature": "hunters_prey", "choice": "colossus_slayer"})})
	}
	if has("Primal Knowledge") {
		pick := ""
		for _, s := range []string{"perception", "athletics", "survival", "intimidation", "nature", "animal handling"} {
			if pick == "" && !held[s] {
				pick = s
			}
		}
		steps = append(steps, setupStep{Key: "primal_knowledge", Title: "Choose your Primal Knowledge skill", Why: "Barbarian level 3: one more skill from the Barbarian list (Animal Handling, Athletics, Intimidation, Nature, Perception, Survival).",
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]string{"primal_knowledge": pick})})
	}
	if has("Bonus Proficiencies") {
		var picks []string
		for _, s := range []string{"perception", "stealth", "arcana", "history", "investigation", "insight", "persuasion", "deception", "athletics", "acrobatics"} {
			if len(picks) < 3 && !held[s] {
				picks = append(picks, s)
			}
		}
		steps = append(steps, setupStep{Key: "bonus_proficiencies", Title: "Choose your three Bonus Proficiencies", Why: "College of Lore level 3: three skills of your choice.",
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]interface{}{"bonus_proficiencies": picks})})
	}
	for _, t := range todo { // anything else the list names
		known := false
		for _, w := range []string{"Divine Order", "Primal Order", "Fighting Style", "Eldritch Invocations", "Expertise", "cantrip", "Spells:", "subclass", "Metamagic", "Wild Shape", "Circle of the Land", "Hunter's Prey", "Primal Knowledge", "Bonus Proficiencies"} {
			if strings.Contains(t, w) {
				known = true
			}
		}
		if !known {
			steps = append(steps, setupStep{Key: "other", Title: t})
		}
	}
	// spells
	wantCantrips := missingCantrips(charID, class, level)
	maxPrep := game.MaxPreparedSpells(class, level, intl, wis, cha)
	if maxPrep > 0 || wantCantrips > 0 || game.ClassTableInt(cl, level, "Cantrips") > 0 {
		origin := originSpellsOf(charID)
		var cantrips, leveled []string
		seen := map[string]bool{}
		for _, s := range append(jsonList(known), jsonList(prepared)...) {
			if seen[s] {
				continue
			}
			seen[s] = true
			if _, o := origin[s]; o {
				continue
			}
			if sp, ok := srdSpellsMemory[s]; ok {
				if sp.Level == 0 {
					cantrips = append(cantrips, s)
				} else if cl != "wizard" || containsStr(jsonList(prepared), s) {
					leveled = append(leveled, s)
				}
			}
		}
		avoid := map[string]bool{}
		for s := range seen {
			avoid[s] = true
		}
		for s := range origin {
			avoid[s] = true
		}
		_, subSlug, _ := subclassOf(charID)
		for _, s := range getDomainSpells(subSlug, level, subclassChoicesOf(charID)["circle_land"]) { // the subclass's spells are always prepared
			avoid[s] = true
		}
		var newC []string
		for _, c := range cantripPrefs[cl] {
			if len(newC) < wantCantrips && !avoid[c] && onClassList(c, cl) {
				newC = append(newC, c)
				avoid[c] = true
			}
		}
		for _, c := range getClassSpellList(cl) {
			if sp, ok := srdSpellsMemory[c]; ok && sp.Level == 0 && len(newC) < wantCantrips && !avoid[c] {
				newC = append(newC, c)
				avoid[c] = true
			}
		}
		spellsDone := wantCantrips == 0 && len(leveled) >= maxPrep
		maxLvl := (level + 1) / 2 // full casters and Pact Magic: level 2 spells from level 3
		if cl == "paladin" || cl == "ranger" {
			maxLvl = (level + 3) / 4
		}
		if maxLvl > 5 {
			maxLvl = 5
		}
		var pick []string
		for _, list := range [][]string{spell2Prefs[cl], spellPrefs[cl], getClassSpellList(cl)} { // the suggestions first, then the class list
			for _, s := range list {
				if len(leveled)+len(pick) < maxPrep && !avoid[s] && onClassList(s, cl) {
					if sp, ok := srdSpellsMemory[s]; ok && sp.Level >= 1 && sp.Level <= maxLvl {
						pick = append(pick, s)
						avoid[s] = true
					}
				}
			}
		}
		st := setupStep{Key: "spells", Title: "Choose your cantrips and prepare your spells", Done: spellsDone,
			Why: fmt.Sprintf("You know %d cantrips (%d more to choose) and prepare %d level 1+ spells (%d prepared now). Cantrips and spells must be on your class's list: GET /api/universe/class-spells/%s. Origin spells (Magic Initiate, lineage) are extra.", len(cantrips)+wantCantrips, wantCantrips, maxPrep, len(leveled), cl)}
		if !spellsDone {
			all := append(append(append([]string{}, cantrips...), newC...), leveled...)
			all = append(all, pick...)
			if cl == "wizard" {
				book := 0
				for _, s := range jsonList(known) {
					if sp, ok := srdSpellsMemory[s]; ok && sp.Level > 0 {
						book++
					}
				}
				var add []string
				target := 6 + 2*(level-1) // 6 spells at level 1, two more each level (SRD 5.2.1 Spellbook)
				if book < target {
					for _, list := range [][]string{spell2Prefs["wizard"], spellPrefs["wizard"]} {
						for _, s := range list {
							if sp, ok := srdSpellsMemory[s]; ok && len(add) < target-book && sp.Level >= 1 && sp.Level <= maxLvl && onClassList(s, "wizard") && !containsStr(jsonList(known), s) && !containsStr(add, s) {
								add = append(add, s)
							}
						}
					}
				}
				add = append(add, newC...)
				st.Why += " A Wizard writes spells in the spellbook (6 at level 1, two more each level: the first request), then prepares from it (the second)."
				if len(add) > 0 {
					st.Request = req("PUT", fmt.Sprintf("/api/characters/%d/spells", charID), map[string]interface{}{"add": add})
					var prep []string
					for _, s := range append(append([]string{}, cantrips...), newC...) {
						prep = append(prep, s)
					}
					n := 0
					for _, s := range append(append([]string{}, leveled...), add...) {
						if sp, ok := srdSpellsMemory[s]; ok && sp.Level > 0 && n < maxPrep && !containsStr(prep, s) {
							prep = append(prep, s)
							n++
						}
					}
					st.Then = &setupStep{Key: "prepare", Title: "Prepare spells from your spellbook", Request: req("POST", fmt.Sprintf("/api/characters/%d/prepare", charID), map[string]interface{}{"spells": prep})}
				} else {
					st.Request = req("POST", fmt.Sprintf("/api/characters/%d/prepare", charID), map[string]interface{}{"spells": all})
				}
			} else {
				st.Request = req("POST", fmt.Sprintf("/api/characters/%d/prepare", charID), map[string]interface{}{"spells": all})
				st.Why += " Send your cantrips and spells together in one list."
			}
		}
		steps = append(steps, st)
	}
	// a weapon in hand
	if mainHand == "" && shield && (cl == "cleric" || cl == "druid") {
		steps = append(steps, setupStep{Key: "weapon_in_hand", Title: "Keep a hand free for spells", Done: true,
			Why: "You hold a Shield, so keep your other hand empty: spells with gestures (S) need a free hand (your focus is worn or in that hand). When you attack, draw your weapon then: equipping or unequipping one weapon a turn is free (POST /api/characters/equip-weapon), and put it away on a later turn before you cast."})
	} else if mainHand == "" {
		var inv []invItem
		var raw []byte
		db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
		json.Unmarshal(raw, &inv)
		weapon := ""
		for _, it := range inv {
			n := itemStr(it, "name")
			if _, _, ok := findWeapon(n); ok && itemStr(it, "location") != "lost" {
				weapon = n
				break
			}
		}
		if weapon != "" {
			why := "Your hands are empty: draw a weapon before a fight (one weapon draw is free during your turn's attack, but being ready is simpler)."
			if game.ClassTableInt(cl, level, "Cantrips") > 0 || maxPrep > 0 {
				why += " Spells with gestures (S) need a free hand: a weapon in one hand and nothing (or a focus) in the other works; a weapon and a Shield leaves no free hand."
				if shield {
					why += " You carry a Shield, so to cast spells with gestures you may prefer to keep your main hand empty and draw the weapon only to attack."
				}
			}
			steps = append(steps, setupStep{Key: "weapon_in_hand", Title: "Hold a weapon", Why: why,
				Request: req("POST", "/api/characters/equip-weapon", map[string]interface{}{"character_id": charID, "weapon": strings.ToLower(weapon), "slot": "main_hand"})})
		}
	} else {
		steps = append(steps, setupStep{Key: "weapon_in_hand", Title: "Hold a weapon", Done: true, Why: "In your main hand: " + mainHand})
	}
	// armor and gear
	for _, g := range gearReminders(charID) {
		why, _ := g["why_not_worn"].(string)
		title := fmt.Sprintf("Put on %v", g["item"])
		if why == "" { // clothing, a cloak, boots, a ring ...: worn on a body place
			why = fmt.Sprintf("%v is carried but not worn; it can be worn (%v).", g["item"], g["place"])
			if a, ok := g["attunement"].(string); ok {
				why += " It " + a + "."
			}
		} else {
			why = fmt.Sprintf("Wearing it: AC %v instead of %v. Not worn now because %s.", g["ac_with_it"], g["ac_now"], why)
		}
		steps = append(steps, setupStep{Key: "gear", Title: title, Why: why, Request: map[string]interface{}{"how": g["how"], "mute": g["mute"]}})
	}
	// warnings
	var warnings []string
	if order := classAbilityOrder(cl); len(order) > 0 {
		sc := map[string]int{"str": str, "dex": dex, "con": con, "int": intl, "wis": wis, "cha": cha}
		top := order[0]
		for _, k := range abilityKeys6 {
			if sc[k] > sc[top] {
				warnings = append(warnings, fmt.Sprintf("Your highest score is %s (%d) but a %s relies on %s (%d). The GM can correct scores with POST /api/gm/update-character.", strings.ToUpper(k), sc[k], cl, strings.ToUpper(top), sc[top]))
				break
			}
		}
	}
	left := 0
	var next *setupStep
	for i := range steps {
		if !steps[i].Done {
			left++
			if next == nil {
				next = &steps[i]
			}
		}
	}
	out := map[string]interface{}{"character_id": charID, "character_finished": len(todo) == 0, "steps": steps, "steps_left": left}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	if next != nil {
		out["next"] = next
		out["how"] = "Send each step's request (the suggested choices are filled in; change them as you like), then GET this page again until steps_left is 0."
	} else {
		out["how"] = "Your character is ready to play."
	}
	writeJSONStatus(w, 200, out)
}
