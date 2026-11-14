package main

// Character builder (F234, Nov 10). the project owner: in the game that began today the AI players didn't assign their ability scores properly,
// didn't equip their gear and the casters didn't choose spells; "look at how D&D Beyond guides people through character creation".
// The builder follows the order of the 2024 Player's Handbook and the D&D Beyond builder: Class (with the class's own choices),
// Background, Species, Ability Scores, Details (name and languages), Backstory, Review; then, once the character exists, Spells and gear
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
	"regexp"
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
	out := append([]string{}, knownWildShapes(charID)...) // keep the forms already known, add new ones
	for _, p := range []string{"wolf", "boar", "riding-horse", "giant-badger", "panther", "giant-spider", "brown-bear", "dire-wolf", "giant-eagle"} {
		if len(out) < known && containsStr(ok, p) && !containsStr(out, p) {
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

// maxCharacterNameRunes: the characters table holds a name of up to 255 characters.
const maxCharacterNameRunes = 255

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

// toolDoes: what a background's tool is for ("a gaming set of your choice" when the player picks the kind).
func toolDoes(tool, category string) string {
	if tool != "" {
		return srdTools[strings.ToLower(tool)]
	}
	if category == "gaming" {
		return srdTools["gaming set"]
	}
	return ""
}

// originFeatDoes: the Origin feat's name, description and benefits, in one text.
func originFeatDoes(feat, note string) string {
	if t, ok := srdOriginFeats[feat]; ok {
		return t // the SRD's own text
	}
	ft, ok := game.AvailableFeats[feat]
	if !ok {
		return ""
	}
	out := ft.Name
	if note != "" {
		out += " (" + note + ")"
	}
	out += ": " + ft.Description
	if len(ft.Benefits) > 0 {
		out += " " + strings.Join(ft.Benefits, ". ") + "."
	}
	return out
}

// backgroundKit: the background's equipment package A as one text.
func backgroundKit(b *game.Background) string {
	var items []string
	for _, it := range b.Equipment {
		n := it.Name
		if it.Choice {
			n = "your chosen " + firstNonEmpty(b.ToolCategory, "tool") + " set"
		}
		if it.Qty > 1 {
			n = fmt.Sprintf("%s (%d)", n, it.Qty)
		}
		items = append(items, n)
	}
	return fmt.Sprintf("%s and %d GP", strings.Join(items, ", "), b.GoldA)
}

// skillOptionDetails: the SRD's description of each skill on a class's list (Skills table: Ability and Example Uses).
func skillOptionDetails(allowed []string) map[string]string {
	out := map[string]string{}
	for _, s := range allowed {
		if r, ok := srdSkillRows[s]; ok {
			out[s] = "Ability: " + r[0] + ". Example uses: " + r[1]
		}
	}
	return out
}

func classSkillChoices(class string) (int, []string) {
	if c, ok := classSkillChoices2024[class]; ok { // the SRD's lists (a Bard's is every skill), as creation checks them
		out := append([]string(nil), c.Skills...)
		sort.Strings(out)
		return c.Count, out
	}
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
		return fmt.Sprintf("%d GP and no equipment: buy your own in the shop after the character is made (the setup's shop step, SRD prices)", o.Gold)
	}
	return fmt.Sprintf("%s and %d GP", strings.Join(items, ", "), o.Gold)
}

// classAbilityOrder: the class's abilities from the PHB array, highest first.
// abilityDescriptions: the SRD's Ability Descriptions, Ability Check Examples and Saving Throw Examples tables, joined per ability.
var abilityDescriptions = func() map[string]string {
	out := map[string]string{}
	for k, r := range srdAbilityRows {
		out[k] = abilityFullNames[k] + ". Score measures: " + r[0] + ". Make a check to: " + r[1] + ". Make a save to: " + r[2] + "."
	}
	return out
}()

// classPrimary: each class's Primary Ability (SRD 5.2.1 class tables).
var classPrimary = map[string][]string{
	"barbarian": {"str"}, "bard": {"cha"}, "cleric": {"wis"}, "druid": {"wis"}, "fighter": {"str", "dex"}, "monk": {"dex", "wis"},
	"paladin": {"str", "cha"}, "ranger": {"dex", "wis"}, "rogue": {"dex"}, "sorcerer": {"cha"}, "warlock": {"cha"}, "wizard": {"int"},
}

var abilityFullNames = map[string]string{"str": "Strength", "dex": "Dexterity", "con": "Constitution", "int": "Intelligence", "wis": "Wisdom", "cha": "Charisma"}

// backgroundAbilityInfo: the abilities a background raises: how the raise works, what each ability is for, and which of them are
// primary or secondary for the class (same definitions as Step 5).
func backgroundAbilityInfo(abilities []string, class string) map[string]interface{} {
	what := map[string]string{}
	for _, a := range abilities {
		what[abilityFullNames[a]] = abilityDescriptions[a]
	}
	out := map[string]interface{}{
		"how_it_works":     srdAdjustRule + " You choose in Step 5, after you set your scores.",
		"what_each_is_for": what,
	}
	if g := abilityGuidance(class); g != nil {
		primary := map[string]bool{}
		for _, a := range classPrimary[class] {
			primary[a] = true
		}
		arr := phbArray[class]
		var pr, se []string
		for _, a := range abilities {
			if primary[a] {
				pr = append(pr, abilityFullNames[a])
				continue
			}
			for i, k := range abilityKeys6 {
				if k == a && (arr[i] == 14 || arr[i] == 13) {
					se = append(se, abilityFullNames[a])
				}
			}
		}
		out["for_your_class"] = map[string]interface{}{
			"primary_among_these":   pr,
			"secondary_among_these": se,
			"how_to_read_this":      "Primary: your class’s Primary Ability, from the Core Traits table of your class in the SRD. Secondary (our word, not the SRD’s): the next-highest scores, 14 and 13, in the Standard Array by Class table for your class.",
		}
	}
	return out
}

// abilityGuidance: the class's primary abilities (the SRD's Primary Ability) and its secondary abilities (the next-highest scores
// in the Player's Handbook's suggested Standard Array for the class), so the player knows where each number goes.
func abilityGuidance(class string) map[string]interface{} {
	arr, ok := phbArray[class]
	if !ok {
		return nil
	}
	isPrimary := map[string]bool{}
	var primary []string
	for _, a := range classPrimary[class] {
		isPrimary[a] = true
		primary = append(primary, abilityFullNames[a])
	}
	var secondary []string
	for _, a := range classAbilityOrder(class) {
		if isPrimary[a] {
			continue
		}
		for i, k := range abilityKeys6 {
			if k == a && (arr[i] == 14 || arr[i] == 13) {
				secondary = append(secondary, fmt.Sprintf("%s (%d)", abilityFullNames[a], arr[i]))
			}
		}
	}
	return map[string]interface{}{
		"primary":   primary,
		"secondary": secondary,
		"how_to_read_this": srdStandardArrayTable + " Primary: your class’s Primary Ability, from the Core Traits table of your class in the SRD. Secondary (our word, not the SRD’s): the next-highest scores, 14 and 13, in the Standard Array by Class table for your class.",
		"standard_array_by_class_row": fmt.Sprintf("Str %d, Dex %d, Con %d, Int %d, Wis %d, Cha %d", arr[0], arr[1], arr[2], arr[3], arr[4], arr[5]),
	}
}

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
	if problem == "" {
		problem = listProblem(field, count, options, current)
	}
	if problem != "" {
		c["problem"] = problem
		c["done"] = false
	}
	return c
}

// listProblem: what is wrong with a list answer (Nov 13): the same item twice, the wrong number, or an item that isn't an option.
func listProblem(field string, count int, options interface{}, current interface{}) string {
	var got []string
	switch v := current.(type) {
	case []string:
		got = v
	case []interface{}:
		for _, x := range v {
			if s, ok := x.(string); ok {
				got = append(got, s)
			}
		}
	default:
		return ""
	}
	if len(got) == 0 {
		return ""
	}
	seen := map[string]bool{}
	for _, g := range got {
		k := strings.ToLower(strings.TrimSpace(g))
		if seen[k] {
			return fmt.Sprintf("you chose %s twice: choose %d different ones", g, count)
		}
		seen[k] = true
	}
	// only where the options are the answers themselves (a Monk's tool options are kinds, "artisan" or "instrument", not tool names)
	if opts, ok := options.([]string); ok && len(opts) > 0 && (field == "skill_proficiencies" || field == "extra_languages") {
		for _, g := range got {
			found := false
			for _, o := range opts {
				if strings.EqualFold(strings.TrimSpace(g), o) {
					found = true
				}
			}
			if !found {
				return fmt.Sprintf("%q is not one of the options: %s", g, strings.Join(opts, ", "))
			}
		}
	}
	if count > 0 && len(got) != count {
		return fmt.Sprintf("choose exactly %d (you chose %d)", count, len(got))
	}
	return ""
}

// finishStep: a step is done only when every choice in it is done (a choice can carry a problem its step didn't count).
func finishStep(st *builderStep) {
	open := 0
	for _, c := range st.Choices {
		if d, ok := c["done"].(bool); ok && !d {
			open++
		}
	}
	if st.Remaining < open {
		st.Remaining = open
	}
	st.Done = st.Remaining == 0
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
		Explain: "Each class makes its own choices at level 1: skills, and for some Weapon Mastery, instruments, Expertise, a Divine or Primal Order, and the starting equipment. Spells are chosen after the character is made (GET /api/characters/{id}/setup)."}
	var leftovers []map[string]interface{} // answers the class doesn't take (left from an earlier class), flagged after its own choices
	if classOK {
		n, allowed := classSkillChoices(class)
		have := normalizeListField(body, "skill_proficiencies", canonSkillOK) // "sleight_of_hand" is saved as "sleight of hand"
		// a class skill the character already gets elsewhere would be lost (creation refuses it): the background's skills, the
		// species' skill and the Human's Skilled picks are left out of the options and refused, each saying where it comes from
		from := map[string]string{}
		if bg != nil {
			for _, s := range bg.Skills {
				from[normSkillName(s)] = "your background"
			}
		}
		spSkill, featSkills, featTools := draftSpeciesProficiencies(body)
		if spSkill != "" && from[spSkill] == "" {
			from[spSkill] = "your species (species_choices.skill)"
		}
		for _, s := range featSkills {
			if from[s] == "" {
				from[s] = "your Skilled feat (species_feat_choices)"
			}
		}
		allowedSet := map[string]bool{}
		for _, a := range allowed {
			allowedSet[a] = true
		}
		var offered, fromBg, fromSpecies []string
		for _, a := range allowed {
			switch from[a] {
			case "":
				offered = append(offered, a)
			case "your background":
				fromBg = append(fromBg, a)
			default:
				fromSpecies = append(fromSpecies, a)
			}
		}
		prob := ""
		seenSk := map[string]bool{}
		for _, h := range have {
			switch {
			case seenSk[h]:
				prob = fmt.Sprintf("you chose %s twice: choose %d different skills", h, n)
			case !allowedSet[h]:
				prob = fmt.Sprintf("%s isn't one of your class's skills: choose from %s", h, strings.Join(offered, ", "))
			case from[h] != "":
				prob = fmt.Sprintf("%s also comes from %s: choose another so you don't lose a proficiency", h, from[h])
			}
			if prob != "" {
				break
			}
			seenSk[h] = true
		}
		if prob == "" && len(have) > 0 && len(have) != n {
			prob = fmt.Sprintf("choose exactly %d (you chose %d)", n, len(have))
		}
		okSkills := len(have) == n && prob == ""
		skillChoice := choice("skill_proficiencies", fmt.Sprintf("%d skills from your class's list", n), n, offered, have, okSkills, prob)
		skillChoice["option_details"] = skillOptionDetails(offered) // what each skill is, so the player can choose knowing what it does
		if len(fromBg) > 0 {
			skillChoice["left_out_your_background_gives_them"] = fromBg
		}
		if len(fromSpecies) > 0 {
			skillChoice["left_out_your_species_gives_them"] = fromSpecies
		}
		s2.Choices = append(s2.Choices, skillChoice)
		// the suggestion avoids those skills too and, before a background is chosen, the skills of the background Step 3 will
		// suggest, so taking every suggestion in order never reopens this step (Nov 14 audit: Acolyte's Insight, Soldier's Athletics)
		sugAvoid := map[string]bool{}
		if bg == nil {
			if pb := game.GetBackground(bestBackground(class, nil)); pb != nil {
				for _, s := range pb.Skills {
					sugAvoid[normSkillName(s)] = true
				}
			}
		}
		sugSkills := pickFrom(skillPrefs[class], offered, sugAvoid, n)
		if len(sugSkills) < n {
			sugSkills = pickFrom(skillPrefs[class], offered, map[string]bool{}, n)
		}
		sug := map[string]interface{}{"skill_proficiencies": sugSkills}
		if !okSkills {
			s2.Remaining++
		}
		// the starting equipment answer: a letter, or an option exactly as listed ("a: Chain Mail, ...")
		eqKeys := startingOptionKeys(class)
		eq, eqProblem := "", ""
		if raw := body["starting_equipment"]; raw != nil {
			if s, isText := raw.(string); !isText {
				eqProblem = "send one option as text: " + strings.Join(eqKeys, " or ")
			} else {
				eq = strings.ToLower(strings.TrimSpace(s))
				for _, k := range eqKeys {
					if eq == strings.ToLower(k+": "+kitText(class, k)) {
						eq = k
					}
				}
				if eq != "" && !containsStr(eqKeys, eq) {
					eqProblem = fmt.Sprintf("%q is not an option: send %s", s, strings.Join(eqKeys, " or "))
				} else if eq != "" && eq != s {
					body["starting_equipment"] = eq // saved as its letter
				}
			}
		}
		if mc := masteryCount(class, 1); mc > 0 {
			// the weapons of the kit chosen (kit a until one is chosen, as the suggestion takes it), not always kit a (Nov 14 audit)
			kitKey := "a"
			if eq != "" && eqProblem == "" {
				kitKey = eq
			}
			var kit []string
			for _, it := range classStartingEquipment[class][kitKey].Items {
				if _, _, ok := findWeapon(it.Name); ok {
					kit = append(kit, strings.ToLower(it.Name))
				}
			}
			kitHint := ""
			switch {
			case len(kit) > 0 && kitKey == eq:
				kitHint = " (the weapons of the kit you chose are a good choice: " + strings.Join(kit, ", ") + ")"
			case len(kit) > 0:
				kitHint = " (the weapons of starting equipment a, the suggested kit, are a good choice: " + strings.Join(kit, ", ") + ")"
			default:
				kitHint = " (you chose gold, not a kit: choose weapons you plan to buy in the shop)"
			}
			// the same check as creation (Nov 14 tester: step 2 took flail and javelin, the review refused them): every pick must be
			// a weapon the class can master, and the options list them
			var mOpts []string
			mDetails := map[string]string{}
			for _, c := range eligibleMasteryWeapons(class) {
				mOpts = append(mOpts, strings.ToLower(c.Name))
				t := masteryText[c.Mastery]
				if t == "" {
					t = masteryTextMore[c.Mastery]
				}
				mDetails[strings.ToLower(c.Name)] = c.Mastery + ": " + t
			}
			m := blist(body, "weapon_masteries")
			mProblem := ""
			if len(m) > 0 {
				if _, oerr := validateMasteryPicks(class, 1, m, true); oerr != nil {
					mProblem = oerr.Message
					seenM := map[string]bool{}
					for _, p := range m {
						k, ok := resolveMasteryPick(class, p)
						if !ok {
							mProblem = fmt.Sprintf("%q is not a weapon a %s can choose for Weapon Mastery. Choose %d from the options.", p, titleWords(class), mc)
							break
						}
						if seenM[k] {
							mProblem = fmt.Sprintf("You chose %s twice: choose %d different weapons.", titleWords(p), mc)
							break
						}
						seenM[k] = true
					}
				}
			}
			okM := len(m) == mc && mProblem == ""
			mChoice := choice("weapon_masteries", fmt.Sprintf("Weapon Mastery: %d kinds of weapon you are proficient with%s", mc, kitHint), mc, mOpts, m, okM, mProblem)
			mChoice["option_details"] = mDetails
			s2.Choices = append(s2.Choices, mChoice)
			var mSug []string // only weapons the check accepts
			for _, w := range append(append([]string(nil), kit...), masteryFallback[class]...) {
				if _, ok := resolveMasteryPick(class, w); ok {
					mSug = append(mSug, w)
				}
			}
			mSug = append(mSug, mOpts...)
			sug["weapon_masteries"] = pickFrom(mSug, nil, map[string]bool{}, mc)
			if !okM {
				s2.Remaining++
			}
		} else if hasAnswer(body["weapon_masteries"]) {
			leftovers = append(leftovers, leftoverChoice("weapon_masteries", body["weapon_masteries"], fmt.Sprintf("A %s has no Weapon Mastery", titleWords(class))))
			sug["weapon_masteries"] = nil
		}
		if tc := game.ToolChoicesFor(class, nil); len(tc) > 0 {
			canon := classToolCanon(tc[0].Options)
			// the actual tools, each saved under its option name ("Musical Instrument (Lute)" as "lute", as creation reads it), and a
			// problem for one that isn't among them (Nov 14 tester: the options were only the categories)
			tools := normalizeListField(body, "tool_proficiencies", canon)
			pending := game.PendingToolChoices(tc, strings.Join(tools, ", "))
			// a tool the character gets anyway (the background's, a Skilled pick) would be a wasted choice: left out and refused
			toolFrom := map[string]string{}
			if bg != nil && bg.Tool != "" {
				if t, ok := game.FindTool(bg.Tool); ok {
					toolFrom[t.Name] = "your background"
				}
			}
			for _, t := range featTools {
				if toolFrom[t] == "" {
					toolFrom[t] = "your Skilled feat (species_feat_choices)"
				}
			}
			var tOpts []string
			for _, o := range tc[0].Options {
				if toolFrom[o] == "" {
					tOpts = append(tOpts, o)
				}
			}
			toolProblem := ""
			for _, t := range tools {
				c, ok := canon(t)
				if !ok {
					toolProblem = fmt.Sprintf("%q is not one of the tools a %s can choose: choose from %s", t, titleWords(class), strings.Join(tOpts, ", "))
					break
				}
				if src := toolFrom[c]; src != "" {
					toolProblem = fmt.Sprintf("%s also comes from %s: choose another so you don't lose a proficiency", c, src)
					break
				}
			}
			if toolProblem == "" && len(tools) > tc[0].Count {
				toolProblem = fmt.Sprintf("choose %d, not %d", tc[0].Count, len(tools))
			}
			toolWhat := map[string]string{"bard": "3 Musical Instruments you are proficient with", "monk": "1 type of Artisan's Tools or 1 Musical Instrument you are proficient with"}[class]
			if toolWhat == "" {
				toolWhat = "tools your class lets you choose"
			}
			s2.Choices = append(s2.Choices, choice("tool_proficiencies", toolWhat, tc[0].Count, tOpts, tools, len(pending) == 0 && toolProblem == "", toolProblem))
			toolPrefs := []string{"flute"}
			if class == "bard" {
				toolPrefs = []string{"lute", "flute", "drum"}
			}
			sug["tool_proficiencies"] = pickFrom(toolPrefs, tOpts, map[string]bool{}, tc[0].Count)
			if len(pending) > 0 || toolProblem != "" {
				s2.Remaining++
			}
		} else if hasAnswer(body["tool_proficiencies"]) {
			// creation takes only the class's own tools here (a no-op); anything else is refused there, so it is refused here
			own := game.ProficienciesFor(game.CharacterInput{FirstClass: class}).Tools
			items := normalizeListField(body, "tool_proficiencies", func(s string) (string, bool) {
				if t, ok := findToolLoose(s); ok && game.HasToolProficiency(own, t.Name) {
					return t.Name, true // "Thieves Tools" is saved as "thieves' tools", as creation reads it
				}
				return "", false
			})
			extra := len(items) == 0
			for _, t := range items {
				if !game.HasToolProficiency(own, t) {
					extra = true
				}
			}
			if extra {
				leftovers = append(leftovers, leftoverChoice("tool_proficiencies", body["tool_proficiencies"], fmt.Sprintf("A %s has no tools to choose (tool_proficiencies is for a Bard's or a Monk's choice)", titleWords(class))))
				sug["tool_proficiencies"] = nil
			}
		}
		if class == "rogue" {
			// Expertise: 2 skills the character has (class, background, species, the Human's Skilled feat), checked here as creation
			// checks it (Nov 14: the suggestion was always Stealth and Perception, and the review refused it when the rogue hadn't
			// taken those)
			profSet := map[string]bool{}
			var profs []string
			addProf := func(set map[string]bool, list *[]string, s string) {
				s = normSkillName(s)
				if s != "" && !set[s] {
					set[s] = true
					*list = append(*list, s)
				}
			}
			others := append([]string(nil), featSkills...) // from the background, the species and the Skilled feat
			if spSkill != "" {
				others = append([]string{spSkill}, others...)
			}
			if bg != nil {
				others = append(append([]string(nil), bg.Skills...), others...)
			}
			for _, s := range append(append([]string(nil), have...), others...) {
				addProf(profSet, &profs, s)
			}
			e := normalizeListField(body, "expertise", canonSkillOK)
			eProb := ""
			seenE := map[string]bool{}
			for _, x := range e {
				if seenE[x] {
					eProb = fmt.Sprintf("you chose %s twice: choose 2 different skills", x)
					break
				}
				seenE[x] = true
				if !profSet[x] {
					eProb = fmt.Sprintf("%s isn't one of your skill proficiencies (class, background, species or Skilled feat): Expertise is for skills you have", x)
					if len(profs) > 0 {
						eProb += " (" + strings.Join(profs, ", ") + ")"
					}
					break
				}
			}
			if eProb == "" && len(e) > 2 {
				eProb = "choose exactly 2"
			}
			s2.Choices = append(s2.Choices, choice("expertise", "Expertise: 2 of your skill proficiencies (class, background, species or Skilled feat) get double your Proficiency Bonus", 2, profs, e, len(e) == 2 && eProb == "", eProb))
			// suggested from the skills the character will have once the suggestion is taken (Nov 14 audit: it was null before the
			// skills were chosen, so one use_suggestion left Expertise open)
			sugSet, sugList := profSet, profs
			if !okSkills {
				sugSet, sugList = map[string]bool{}, nil
				for _, s := range append(append([]string(nil), sugSkills...), others...) {
					addProf(sugSet, &sugList, s)
				}
			}
			var eSug []string
			for _, p := range append([]string{"stealth", "perception", "sleight of hand", "acrobatics", "deception", "investigation", "insight", "persuasion"}, sugList...) {
				if sugSet[p] && len(eSug) < 2 && !containsStr(eSug, p) {
					eSug = append(eSug, p)
				}
			}
			if len(eSug) == 2 {
				sug["expertise"] = eSug
			}
			if len(e) != 2 || eProb != "" {
				s2.Remaining++
			}
		} else if hasAnswer(body["expertise"]) {
			leftovers = append(leftovers, leftoverChoice("expertise", body["expertise"], "Only Rogues choose Expertise at level 1 (Bards, Rangers and Wizards choose theirs after creation)"))
			sug["expertise"] = nil
		}
		coSug := map[string]interface{}{}
		co := bmap(body, "class_options")
		for _, o := range []struct {
			class, field, sv string
			opts             []string
		}{{"cleric", "divine_order", "thaumaturge", []string{"protector", "thaumaturge"}}, {"druid", "primal_order", "magician", []string{"magician", "warden"}}} {
			if class != o.class {
				if co != nil && hasAnswer(co[o.field]) { // left from an earlier class, or sent by mistake: creation refuses it
					leftovers = append(leftovers, leftoverChoice("class_options."+o.field, co[o.field], fmt.Sprintf("Only %ss choose a %s", titleWords(o.class), o.field)))
					coSug[o.field] = nil
				}
				continue
			}
			field, opts := o.field, o.opts
			v := strings.ToLower(strings.TrimSpace(bstr(co, field)))
			if f := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '(' || r == ':' || r == ',' }); len(f) > 0 && containsStr(opts, f[0]) && f[0] != v {
				v = f[0] // "thaumaturge (one extra cantrip ...)" means thaumaturge
				co[field] = v
			}
			orderProblem := ""
			if v != "" && !containsStr(opts, v) {
				orderProblem = fmt.Sprintf("%q is not an order you can choose: send %s.", v, strings.Join(opts, " or "))
			}
			orderChoice := choice("class_options."+field, "your order", 1, opts, nilIfEmpty(v), v != "" && orderProblem == "", orderProblem)
			orderChoice["option_details"] = srdOrders[field] // the SRD's text for each order
			s2.Choices = append(s2.Choices, orderChoice)
			coSug[field] = o.sv
			if v == "" || orderProblem != "" {
				s2.Remaining++
			}
		}
		if len(coSug) > 0 {
			sug["class_options"] = coSug
		}
		var eqOpts []string
		for _, k := range eqKeys {
			eqOpts = append(eqOpts, k+": "+kitText(class, k))
		}
		s2.Choices = append(s2.Choices, choice("starting_equipment", "your starting equipment: the kit, or gold to buy your own in the shop after the character is made (any gold left over can be spent there too); send the letter", 1, eqOpts, nilIfEmpty(eq), eq != "" && eqProblem == "", eqProblem))
		sug["starting_equipment"] = "a"
		if eq == "" {
			s2.Remaining++
		}
		s2.Suggestion = sug
	} else {
		s2.Remaining = 1
		s2.Problems = []string{"choose a class first"}
	}
	if len(s2.Choices) > 0 && class != "" { // name this class's choices (Nov 14 tester: the monk's tool wasn't mentioned)
		var what []string
		for _, c := range s2.Choices {
			f, _ := c["field"].(string)
			w, _ := c["what"].(string)
			if f != "" {
				what = append(what, f+" ("+w+")")
			}
		}
		s2.Explain += " A " + titleWords(class) + " chooses: " + strings.Join(what, "; ") + "."
	}
	s2.Choices = append(s2.Choices, leftovers...)
	finishStep(&s2)
	steps = append(steps, s2)

	// 3. Background
	s3 := builderStep{Key: "background", Title: "Step 3: Choose a background",
		Explain: "Your background is what you did before adventuring. It raises three of your abilities (in Step 5), and gives two skills, a tool, an Origin feat and equipment."}
	var bgOpts []map[string]interface{}
	for _, k := range game.BackgroundSlugs() {
		b := game.GetBackground(k)
		if b == nil {
			continue
		}
		bo := map[string]interface{}{"background": b.Key, "abilities": b.AbilityScores, "origin_feat": strings.TrimSpace(b.Feat + " " + b.FeatNote), "skills": b.Skills, "tool": firstNonEmpty(b.Tool, "a "+b.ToolCategory+" set of your choice")}
		bo["origin_feat_does"] = originFeatDoes(b.Feat, b.FeatNote)              // what the Origin feat does, so the player can choose knowing it
		bo["tool_does"] = toolDoes(b.Tool, b.ToolCategory)                       // what the tool is for
		bo["skills_do"] = skillOptionDetails(b.Skills)                           // what its two skills cover
		bo["ability_bonuses_do"] = backgroundAbilityInfo(b.AbilityScores, class) // what its three abilities are for, and how they fit the class
		bo["equipment_a"] = backgroundKit(b)                                     // what the package holds
		bo["equipment_b"] = fmt.Sprintf("%d GP and no equipment", game.BackgroundAltGold)
		bgOpts = append(bgOpts, bo)
	}
	s3.Choices = append(s3.Choices, choice("background", "your background", 1, bgOpts, nilIfEmpty(bgKey), bg != nil, ifStr(bgKey != "" && bg == nil, notOneOf("background", bgKey, bgOpts))))
	sug3 := map[string]interface{}{}
	if bg == nil {
		s3.Remaining++
		// the background that suits the class's abilities best, but not one that repeats a class skill already chosen (Nov 14 audit:
		// taking every suggestion in order reopened Step 2 for 5 classes)
		classSkills := map[string]bool{}
		for _, s := range blist(body, "skill_proficiencies") {
			classSkills[normSkillName(s)] = true
		}
		if best := bestBackground(class, classSkills); best != "" { // never suggest replacing the background the player chose (Nov 13)
			sug3["background"] = best
		}
	} else {
		toolOpts := []string{"dice set", "dragonchess set", "playing card set", "three-dragon ante set"}
		bt := body["background_tool"]
		if bg.ToolCategory != "" {
			t, isText := bt.(string)
			t = strings.TrimSpace(t)
			toolProblem := ""
			if bt != nil && !isText {
				toolProblem = "send the kind of Gaming Set as text: " + strings.Join(toolOpts, ", ")
			} else if tl, ok := game.FindTool(t); t != "" && (!ok || tl.Category != bg.ToolCategory) {
				toolProblem = fmt.Sprintf("%q is not a Gaming Set: choose dice set, dragonchess set, playing card set or three-dragon ante set", t)
			} else if ok {
				for _, o := range toolOpts { // "Gaming Set (Dice)" is saved as "dice set"
					if ot, _ := game.FindTool(o); ot.Name == tl.Name && o != t {
						body["background_tool"], t = o, o
					}
				}
			}
			toolChoice := choice("background_tool", "the kind of "+bg.ToolCategory+" set", 1, toolOpts, nilIfEmpty(t), t != "" && toolProblem == "", toolProblem)
			toolChoice["option_details"] = map[string]string{
				"dice set":              srdTools["gaming set"],
				"dragonchess set":       srdTools["gaming set"],
				"playing card set":      srdTools["gaming set"],
				"three-dragon ante set": srdTools["gaming set"],
			}
			s3.Choices = append(s3.Choices, toolChoice)
			sug3["background_tool"] = "dice set"
			if t == "" || toolProblem != "" {
				s3.Remaining++
			}
		} else if hasAnswer(bt) {
			// this background's tool is fixed: a background_tool would be ignored at creation (Nov 14 audit), unless it names that tool
			t, _ := bt.(string)
			ft, ok1 := findToolLoose(t)
			bgt, ok2 := findToolLoose(bg.Tool)
			if !ok1 || !ok2 || ft.Name != bgt.Name {
				s3.Choices = append(s3.Choices, leftoverChoice("background_tool", bt, fmt.Sprintf("%s gives %s; only a background with a tool to choose (%s) takes background_tool", bg.Name, game.ToolDisplayName(bg.Tool), strings.Join(backgroundsWithToolChoice(), ", "))))
				sug3["background_tool"] = nil
			}
		}
		fcRaw := body["background_feat_choices"]
		if bg.Feat == "magic_initiate" {
			list := strings.ToLower(bg.FeatNote)
			fc := bmap(body, "background_feat_choices")
			miProblem := ""
			if fcRaw != nil && fc == nil {
				miProblem = "send background_feat_choices as an object: {\"cantrips\": [2 cantrips], \"spell\": \"...\", \"spellcasting_ability\": \"int, wis or cha\"}"
			}
			if lv, has := fc["list"]; has && lv != nil { // the list is fixed by the background; creation refuses another (Nov 14 audit)
				if ls, isText := lv.(string); !isText || strings.ToLower(strings.TrimSpace(ls)) != list {
					miProblem = fmt.Sprintf("%s's Magic Initiate uses the %s list, not %v: send \"list\": %q", bg.Name, list, lv, list)
				}
			}
			if miProblem == "" {
				miProblem = magicInitiateProblem(fc, list)
			}
			spCantrips := draftSpeciesCantrips(body)
			if miProblem == "" {
				miProblem = alreadyKnownProblem(fc, spCantrips, nil)
			}
			okF := fc != nil && miProblem == "" && len(blist(fc, "cantrips")) == 2 && bstr(fc, "spell") != "" && bstr(fc, "spellcasting_ability") != ""
			featChoice := choice("background_feat_choices", fmt.Sprintf("Magic Initiate (%s): 2 cantrips and 1 level 1 spell from the %s list, and the ability you cast them with (int, wis or cha)", bg.FeatNote, bg.FeatNote), 1,
				magicInitiateOptions(list, spCantrips, nil), fcRaw, okF, miProblem)
			featChoice["option_details"] = map[string]interface{}{
				"what_the_feat_does":   originFeatDoes(bg.Feat, bg.FeatNote),
				"spellcasting_ability": map[string]string{"int": abilityDescriptions["int"], "wis": abilityDescriptions["wis"], "cha": abilityDescriptions["cha"]},
			}
			s3.Choices = append(s3.Choices, featChoice)
			sug3["background_feat_choices"] = magicInitiateSuggestion(list, class, spCantrips...)
			if !okF {
				s3.Remaining++
			}
		} else if hasAnswer(fcRaw) {
			// Alert and Savage Attacker take no choices: picks sent here would be shown and then dropped (Nov 14 audit)
			s3.Choices = append(s3.Choices, leftoverChoice("background_feat_choices", fcRaw, fmt.Sprintf("%s's Origin feat, %s, takes no choices", bg.Name, titleWords(strings.ReplaceAll(bg.Feat, "_", " ")))))
			sug3["background_feat_choices"] = nil
		}
		// the equipment answer: "a" or "b" as text (Nov 14 audit: ["b"], true or 2 were marked done, then the character got package a)
		be, beProblem := "", ""
		if raw := body["background_equipment"]; raw != nil {
			if s, isText := raw.(string); !isText {
				beProblem = "send \"a\" (the package) or \"b\" (50 GP) as text"
			} else if be = strings.ToLower(strings.TrimSpace(s)); be != "" && be != "a" && be != "b" {
				beProblem = fmt.Sprintf("%q is not an option: send a or b", s)
			} else if be != s {
				body["background_equipment"] = be // saved as its letter
			}
		}
		eqChoice := choice("background_equipment", "the background's equipment (a: its package and a little gold; b: 50 GP)", 1, []string{"a", "b"}, nilIfEmpty(be), beProblem == "", beProblem)
		eqChoice["option_details"] = map[string]string{"a": backgroundKit(bg), "b": fmt.Sprintf("%d GP and no equipment: buy your own in the shop after the character is made (the setup's shop step, SRD prices)", game.BackgroundAltGold)}
		s3.Choices = append(s3.Choices, eqChoice)
		sug3["background_equipment"] = "a"
		// increases saved for another background (or before one was chosen) no longer fit: said here, where the change was made
		// (Nov 14 audit: the reply to the background change said nothing, and Step 5 replaced +2 Cha with +2 Dex later)
		if inc := draftIncreases(body); len(inc) > 0 {
			for k := range inc {
				if !containsStr(bg.AbilityScores, k) {
					s3.Warnings = append(s3.Warnings, fmt.Sprintf("Your saved ability_increases (%s) don't fit the %s, which raises %s: choose them again in Step 5.", formatIncreases(inc), bg.Name, strings.Join(bg.AbilityScores, ", ")))
					break
				}
			}
		}
	}
	s3.Suggestion = sug3
	finishStep(&s3)
	steps = append(steps, s3)

	// 4. Species
	s4 := builderStep{Key: "species", Title: "Step 4: Choose a species",
		Explain: "Your species gives traits such as Darkvision, a size and a speed (no ability scores in the 2024 rules). Some species ask for a choice: a lineage, an ancestry, a size, a skill."}
	var spOpts []map[string]interface{}
	for _, s := range game.AllSpecies() {
		var traits []map[string]string
		for _, t := range s.Traits {
			d := srdTrait(s.Key, t.Name) // the SRD's own words (srd_species_2024.go)
			if d == "" {
				d = t.Description
			}
			traits = append(traits, map[string]string{"name": t.Name, "description": d})
		}
		spOpts = append(spOpts, map[string]interface{}{"species": s.Key, "sizes": s.Sizes, "speed": s.Speed, "darkvision": s.Darkvision, "asks_for": s.Needs, "traits": traits}) // traits: each special trait with its description, so the player can choose knowing what the species does
	}
	s4.Choices = append(s4.Choices, choice("species", "your species", 1, spOpts, nilIfEmpty(spKey), sp != nil, ifStr(spKey != "" && sp == nil, notOneOf("species", spKey, spOpts))))
	sug4 := map[string]interface{}{}
	// the background's Magic Initiate cantrips: the suggestions avoid a species that gives one again
	miCantrips := draftMICantrips(body)
	if s, ok := speciesFor[class]; ok && sp == nil { // never suggest replacing the species the player chose (Nov 13: a Tiefling became a Human)
		if !speciesCantripFree(s, miCantrips) {
			s = "human" // the class's usual species would give a cantrip the player's Magic Initiate already picks (a Tiefling's Thaumaturgy)
		}
		sug4["species"] = s
	}
	if sp == nil {
		s4.Remaining++
		s4.Choices = append(s4.Choices, speciesChoicesBeforeSpecies(body, class, bg)...) // a value no species accepts is refused now (S4-13)
	} else {
		// species_choices: the answers this species uses, each checked as creation checks it. Answers it doesn't use (left from an
		// earlier species, or sent for this one) are refused or flagged, never kept silently (Nov 14 tester: a Halfling kept "size
		// medium", a Dwarf kept the Human's feat, and the review summary listed them as if they applied).
		var sc map[string]interface{}
		switch v := body["species_choices"].(type) {
		case map[string]interface{}:
			sc = v
		case nil:
			delete(body, "species_choices")
		default:
			s4.Choices = append(s4.Choices, choice("species_choices", "your species' choices", 1, nil, v, false, `species_choices must be an object, like {"lineage": "wood_elf"}: send it again that way`))
			s4.Remaining++
		}
		if sc == nil {
			sc = map[string]interface{}{}
		}
		for k, v := range sc { // a part sent as null is no answer; "Lineage" or "spellcasting ability" is the key it names
			if v == nil {
				delete(sc, k)
			} else if ck := optionKey(k); ck != k && sc[ck] == nil {
				sc[ck] = v
				delete(sc, k)
			}
		}
		// a Tiefling's "lineage" is its legacy (creation reads it so), and an Elf's or Gnome's "legacy" its lineage: saved under the key
		// the species uses, so the cantrip checks see it
		for _, pair := range [][2]string{{"legacy", "lineage"}, {"lineage", "legacy"}} {
			want, alias := pair[0], pair[1]
			a, isStr := sc[alias].(string)
			if !isStr || !containsStr(sp.Needs, want) || containsStr(sp.Needs, alias) {
				continue
			}
			opts, _ := speciesChoiceOptions(sp.Key, want, class, nil)
			if n := optionKey(a); containsStr(opts, n) && (bstr(sc, want) == "" || optionKey(bstr(sc, want)) == n) {
				sc[want] = n
				delete(sc, alias)
			}
		}
		held := map[string]bool{} // the skills the class and the background give
		for _, s := range blist(body, "skill_proficiencies") {
			held[skillKey(s)] = true
		}
		if bg != nil {
			for _, s := range bg.Skills {
				held[skillKey(s)] = true
			}
		}
		sfcRaw, hasSfc := body["species_feat_choices"]
		if hasSfc && sfcRaw == nil {
			delete(body, "species_feat_choices")
			hasSfc = false
		}
		sfc, sfcIsMap := sfcRaw.(map[string]interface{})
		for k, v := range sfc {
			if v == nil {
				delete(sfc, k)
			}
		}
		var skilledPicks []string // the player's own Skilled picks: the Human's skill suggestion avoids them
		for _, p := range blist(sfc, "proficiencies") {
			if k := canonSkill(p); k != "" {
				skilledPicks = append(skilledPicks, k)
			}
		}
		sugc := map[string]interface{}{}
		for _, need := range sp.Needs {
			raw, isStr := sc[need].(string)
			v := strings.ToLower(strings.TrimSpace(raw))
			opts, sv := speciesChoiceOptions(sp.Key, need, class, held)
			prob := ""
			if sc[need] != nil && !isStr {
				prob = fmt.Sprintf("send species_choices.%s as text, one of the options (you sent %v)", need, sc[need])
			}
			// a spelling of an option is saved as the option: "High Elf" is high_elf, "Intelligence" is int, "sleight_of_hand" is sleight of hand
			if v != "" {
				canon := optionKey(v)
				switch need {
				case "skill":
					canon = canonSkill(v)
				case "spellcasting_ability":
					canon = normAbility(v)
				}
				if canon != "" && ((need == "skill" && sp.Key == "human") || containsStr(opts, canon)) {
					v = canon
					if raw != canon {
						sc[need] = canon
					}
				}
			}
			note := ""
			switch need {
			case "skill":
				cands := opts
				if sp.Key == "human" {
					cands = allSkills // any skill
				}
				keep := []string{} // the skills the class and background don't already give
				for _, o := range cands {
					if !held[o] {
						keep = append(keep, o)
					}
				}
				opts = keep
				if len(keep) == 0 && sp.Key == "elf" {
					note = "Insight, Perception and Survival all come from your class or background already, so Keen Senses has none left: change one of your class skills in step 2 (skill_proficiencies) to free one."
				}
				if prob == "" && v != "" && held[v] {
					prob = fmt.Sprintf("You already have %s (from your class or background): choose a different skill", skillNames([]string{v}))
					if note != "" {
						prob += ". " + strings.TrimSuffix(note, ".")
					}
				} else if prob == "" && v != "" && sp.Key == "human" && canonSkill(v) == "" {
					prob = fmt.Sprintf("%q is not a skill. Choose one of: %s", strings.TrimSpace(raw), strings.Join(keep, ", "))
				}
				sv = "" // the suggestion: a skill the character doesn't have and the player hasn't picked for Skilled
				for _, s := range append([]string{"perception", "insight", "survival", "stealth", "athletics"}, keep...) {
					if sv == "" && containsStr(keep, s) && !containsStr(skilledPicks, s) {
						sv = s
					}
				}
			case "lineage", "legacy":
				if overlapsAny(game.SpeciesCantrips(sp.Key, sv), miCantrips) { // a lineage whose cantrip the Magic Initiate already picks
					for _, o := range opts {
						if !overlapsAny(game.SpeciesCantrips(sp.Key, o), miCantrips) {
							sv = o
							break
						}
					}
				}
			case "feat":
				if bg != nil && bg.Feat != "skilled" && bg.Feat != "magic_initiate" {
					var keep []string
					for _, o := range opts {
						if o != bg.Feat {
							keep = append(keep, o)
						}
					}
					opts = keep
					if v == bg.Feat {
						prob = fmt.Sprintf("Your background already gives %s: choose a different Origin feat.", v)
					}
				}
			}
			if prob == "" && v != "" && opts != nil && !containsStr(opts, v) {
				prob = fmt.Sprintf("%q is not one of the options: %s", v, strings.Join(opts, ", "))
				if need == "feat" {
					if n := phbNote("feat", v); n != "" {
						prob = n + " Choose one of: " + strings.Join(opts, ", ") + "."
					}
				}
			}
			c := choice("species_choices."+need, need, 1, opts, nilIfEmpty(v), v != "" && prob == "", prob)
			if note != "" {
				c["note"] = note
				s4.Problems = append(s4.Problems, note)
			}
			s4.Choices = append(s4.Choices, c)
			if sv != "" {
				sugc[need] = sv
			}
			if v == "" || prob != "" {
				s4.Remaining++
			}
		}
		var unused []string // answers this species doesn't use
		for k := range sc {
			if !containsStr(sp.Needs, k) {
				unused = append(unused, k)
			}
		}
		sort.Strings(unused)
		if len(unused) > 0 {
			var fields, nulls []string
			cur := map[string]interface{}{}
			for _, k := range unused {
				fields = append(fields, "species_choices."+k)
				nulls = append(nulls, fmt.Sprintf("%q: null", k))
				cur[k] = sc[k]
				sugc[k] = nil // use_suggestion removes them
			}
			why := ""
			if containsStr(unused, "size") && len(sp.Sizes) == 1 {
				why = fmt.Sprintf(" (%s is always %s)", withArticle(sp.Name), sp.Sizes[0])
			}
			prob := fmt.Sprintf("%s doesn't choose %s%s, so %s can't be used. Remove %s: send {\"species_choices\": {%s}} (or use_suggestion)",
				titleFirst(withArticle(sp.Name)), joinOr(unused), why, pickMsg(len(unused) == 1, "that answer", "those answers"), pickMsg(len(unused) == 1, "it", "them"), strings.Join(nulls, ", "))
			s4.Choices = append(s4.Choices, choice(strings.Join(fields, ", "), "answers your species doesn't use", 0, nil, cur, false, prob))
			s4.Problems = append(s4.Problems, prob) // shown in the steps list too, whichever step is current
			s4.Remaining++
		}
		if len(sugc) > 0 {
			sug4["species_choices"] = sugc
		}
		humanFeat := ""
		if sp.Key == "human" {
			humanFeat = optionKey(bstr(sc, "feat"))
		}
		if hasSfc && !sfcIsMap {
			s4.Choices = append(s4.Choices, choice("species_feat_choices", "your Human feat's choices", 1, nil, sfcRaw, false, `species_feat_choices must be an object, like {"proficiencies": ["stealth", "insight", "medicine"]}: send it again that way`))
			s4.Remaining++
		}
		if humanFeat == "magic_initiate" {
			// the Human's Magic Initiate: the player picks the list (Cleric, Druid or Wizard), a different one from the background's
			fc := sfc
			list := strings.ToLower(bstr(fc, "list"))
			bgList := ""
			if bg != nil && bg.Feat == "magic_initiate" {
				bgList = strings.ToLower(bg.FeatNote)
			}
			prob := ""
			if list != "" && list != "cleric" && list != "druid" && list != "wizard" {
				prob = fmt.Sprintf("%q is not a list Magic Initiate uses: choose cleric, druid or wizard.", list)
			} else if list != "" && list == bgList {
				prob = "Your background's Magic Initiate already uses the " + list + " list: choose a different list (the feat is repeatable only with a different list)."
			}
			if prob == "" && list != "" {
				prob = magicInitiateProblem(fc, list)
			}
			var bgSpells []string // the background's Magic Initiate spells: the Human's can't repeat them
			if bgList != "" {
				bfc := bmap(body, "background_feat_choices")
				bgSpells = append(blist(bfc, "cantrips"), bstr(bfc, "spell"))
			}
			if prob == "" {
				prob = alreadyKnownProblem(fc, draftSpeciesCantrips(body), bgSpells)
			}
			okF := fc != nil && list != "" && prob == "" && len(blist(fc, "cantrips")) == 2 && bstr(fc, "spell") != "" && bstr(fc, "spellcasting_ability") != ""
			var cur interface{}
			if fc != nil {
				cur = fc
			}
			featChoice := choice("species_feat_choices", "Magic Initiate (your Human feat): a spell list (cleric, druid or wizard), 2 cantrips and 1 level 1 spell from it, and the ability you cast them with (int, wis or cha)", 1,
				humanMagicInitiateOptions(bgList, bgSpells), cur, okF, prob)
			featChoice["option_details"] = map[string]interface{}{
				"what_the_feat_does":   srdOriginFeats["magic_initiate"],
				"spellcasting_ability": map[string]string{"int": abilityDescriptions["int"], "wis": abilityDescriptions["wis"], "cha": abilityDescriptions["cha"]},
			}
			s4.Choices = append(s4.Choices, featChoice)
			sugList := "wizard"
			if bgList == "wizard" {
				sugList = "cleric"
			}
			sug4["species_feat_choices"] = magicInitiateSuggestion(sugList, class, bgSpells...)
			if !okF {
				s4.Remaining++
			}
		}
		if sp.Key == "human" && (humanFeat == "" || humanFeat == "skilled") {
			// Skilled: 3 skills or tools the character doesn't have, checked as creation checks them (Nov 14 tester: unknown names,
			// the background's tool and one skill in two spellings were accepted here and refused by the review)
			heldSk := map[string]bool{}
			for k := range held {
				heldSk[k] = true
			}
			if k := canonSkill(bstr(sc, "skill")); k != "" {
				heldSk[k] = true // the Human's own skill comes first
			}
			heldTools := draftHeldTools(body, class, bg)
			picks, notText := rawList(sfc["proficiencies"])
			skProb := ""
			if notText {
				skProb = `send species_feat_choices.proficiencies as a list of names, like ["stealth", "insight", "herbalism kit"]`
			}
			var saved []interface{}
			changed := false
			seen := map[string]bool{}
			for _, x := range picks {
				if skProb != "" {
					break
				}
				if k := canonSkill(x); k != "" {
					if seen[k] {
						skProb = fmt.Sprintf("You chose %s twice: choose 3 different skills or tools", skillNames([]string{k}))
					} else if heldSk[k] {
						skProb = fmt.Sprintf("You already have %s: choose a skill or tool you don't have", skillNames([]string{k}))
					}
					seen[k] = true
					saved = append(saved, k)
					changed = changed || k != x
					continue
				}
				t, ok := findToolLoose(x)
				if !ok {
					skProb = fmt.Sprintf("%q is neither a skill nor a tool: choose from the options", x)
					break
				}
				if seen["tool:"+t.Name] {
					skProb = fmt.Sprintf("You chose %s twice: choose 3 different skills or tools", game.ToolDisplayName(t.Name))
				} else if game.HasToolProficiency(heldTools, t.Name) {
					skProb = fmt.Sprintf("You already have proficiency with %s: choose a skill or tool you don't have", game.ToolDisplayName(t.Name))
				}
				seen["tool:"+t.Name] = true
				if _, plain := game.FindTool(x); plain {
					saved = append(saved, x)
				} else { // "thieves tools" is saved as thieves' tools, the name creation reads
					saved = append(saved, t.FullName())
					changed = true
				}
			}
			if skProb == "" && len(picks) > 0 && len(picks) != 3 {
				skProb = fmt.Sprintf("choose exactly 3 (you chose %d)", len(picks))
			}
			if skProb == "" && changed && sfc != nil {
				sfc["proficiencies"] = saved
				picks = nil
				for _, s := range saved {
					picks = append(picks, fmt.Sprint(s))
				}
			}
			var skOpts []string // what can still be picked
			for _, s := range allSkills {
				if !heldSk[s] {
					skOpts = append(skOpts, s)
				}
			}
			for _, t := range game.Tools {
				if !game.HasToolProficiency(heldTools, t.Name) {
					skOpts = append(skOpts, t.FullName())
				}
			}
			var cur interface{}
			if len(picks) > 0 {
				cur = picks
			}
			s4.Choices = append(s4.Choices, choice("species_feat_choices.proficiencies", "Skilled: 3 skills or tools you don't already have", 3, skOpts, cur, len(picks) == 3 && skProb == "", skProb))
			avoid := map[string]bool{}
			for k := range heldSk {
				avoid[k] = true
			}
			if v, _ := sugc["skill"].(string); v != "" && bstr(sc, "skill") == "" {
				avoid[v] = true
			}
			sug4["species_feat_choices"] = map[string]interface{}{"proficiencies": pickFrom([]string{"perception", "stealth", "insight", "athletics", "persuasion", "survival", "investigation", "medicine"}, nil, avoid, 3)}
			if len(picks) != 3 || skProb != "" {
				s4.Remaining++
			}
		}
		// species_feat_choices parts the Human's feat doesn't take, or any for another species (a Dwarf kept the Human's Skilled picks)
		if sfcIsMap && len(sfc) > 0 && (sp.Key != "human" || humanFeat == "" || isOriginFeat(humanFeat)) { // an unknown feat is refused above
			allowed := map[string][]string{"skilled": {"proficiencies"}, "": {"proficiencies", "list", "cantrips", "spell", "spellcasting_ability"},
				"magic_initiate": {"list", "cantrips", "spell", "spellcasting_ability"}}[humanFeat]
			if sp.Key != "human" {
				allowed = nil
			}
			var extra []string
			for k := range sfc {
				if !containsStr(allowed, k) {
					extra = append(extra, k)
				}
			}
			sort.Strings(extra)
			if len(extra) > 0 {
				who := titleFirst(withArticle(sp.Name)) + " has no Origin feat from its species: species_feat_choices is only for a Human's feat"
				if sp.Key == "human" {
					who = titleWords(humanFeat) + " doesn't take " + joinOr(extra)
				}
				field := "species_feat_choices"
				send := `{"species_feat_choices": null}`
				if len(extra) < len(sfc) { // only some parts: remove those
					var fields, nulls []string
					for _, k := range extra {
						fields = append(fields, "species_feat_choices."+k)
						nulls = append(nulls, fmt.Sprintf("%q: null", k))
					}
					field, send = strings.Join(fields, ", "), "{\"species_feat_choices\": {"+strings.Join(nulls, ", ")+"}}"
				}
				// the suggestion removes them, and fills the feat's own choices where it has them (Skilled's proficiencies ...)
				if sub, _ := sug4["species_feat_choices"].(map[string]interface{}); sub != nil || field != "species_feat_choices" {
					if sub == nil {
						sub = map[string]interface{}{}
					}
					for _, k := range extra {
						sub[k] = nil
					}
					sug4["species_feat_choices"] = sub
				} else {
					sug4["species_feat_choices"] = nil
				}
				prob := fmt.Sprintf("%s, so %s can't be used. Remove %s: send %s (or use_suggestion)", who, pickMsg(len(extra) == 1, "that answer", "those answers"), pickMsg(len(extra) == 1, "it", "them"), send)
				s4.Choices = append(s4.Choices, choice(field, "answers your species' feat doesn't use", 0, nil, sfc, false, prob))
				s4.Problems = append(s4.Problems, prob)
				s4.Remaining++
			}
		}
	}
	s4.Suggestion = sug4
	finishStep(&s4)
	steps = append(steps, s4)

	// 5. Ability scores
	s5 := builderStep{Key: "ability_scores", Title: "Step 5: Determine ability scores",
		Explain: "This server uses the Standard Array only. " + srdStandardArrayRule + " " + srdAssignRule + " " + srdStandardArrayTable + " " + srdAdjustRule}
	sug5 := map[string]interface{}{}
	if arr, ok := phbArray[class]; ok {
		sug5["ability_method"] = "standard_array"
		for i, k := range abilityKeys6 {
			sug5[k] = arr[i]
		}
		s5.Explain += fmt.Sprintf(" The Standard Array by Class table’s row for a %s: Str %d, Dex %d, Con %d, Int %d, Wis %d, Cha %d.", class, arr[0], arr[1], arr[2], arr[3], arr[4], arr[5])
	}
	method := strings.ToLower(bstr(body, "ability_method"))
	if optionKey(method) == "standard_array" && method != "standard_array" { // "Standard Array" is the standard array
		method = "standard_array"
		body["ability_method"] = method
	}
	s5.Choices = append(s5.Choices, choice("ability_method", "how you generate the scores (the Standard Array is the only method)", 1, []string{"standard_array"}, nilIfEmpty(method), method == "standard_array", ifStr(method != "" && method != "standard_array", "only the Standard Array is used on this server")))
	if method != "standard_array" {
		s5.Remaining++
	}
	scores := map[string]int{}
	missing := 0
	scoreProblem := ""
	for _, k := range abilityKeys6 {
		// a score is a whole number, as creation reads it: "15" is saved as 15; 15.5 or "fifteen" is refused, not read as 15 here
		// and as no score at all by creation
		n, present, ok := wholeNumberField(body[k])
		if present && !ok && scoreProblem == "" {
			scoreProblem = fmt.Sprintf("%s must be a whole number (you sent %v): use 15, 14, 13, 12, 10 and 8 once each", k, body[k])
		}
		if _, isStr := body[k].(string); isStr && ok {
			body[k] = n
		}
		scores[k] = n
		if n == 0 {
			missing++
		}
	}
	if scoreProblem == "" && missing > 0 && missing < 6 {
		scoreProblem = fmt.Sprintf("%d abilities have no score", missing)
	}
	scoresChoice := choice("str, dex, con, int, wis, cha", "one score for each ability: use 15, 14, 13, 12, 10 and 8 once each", 6, nil, scores, missing == 0 && scoreProblem == "", scoreProblem)
	scoresChoice["option_details"] = map[string]interface{}{"what_each_ability_is_for": abilityDescriptions, "for_your_class": abilityGuidance(class)}
	s5.Choices = append(s5.Choices, scoresChoice)
	if missing > 0 || scoreProblem != "" {
		s5.Remaining++
	}
	if method == "standard_array" && missing == 0 && scoreProblem == "" && !isStandardArray([]int{scores["str"], scores["dex"], scores["con"], scores["int"], scores["wis"], scores["cha"]}) {
		s5.Problems = append(s5.Problems, "the Standard Array is 15, 14, 13, 12, 10 and 8, each used once")
		scoresChoice["done"] = false
		scoresChoice["problem"] = fmt.Sprintf("these scores are not the Standard Array: you sent %d, %d, %d, %d, %d and %d (Str, Dex, Con, Int, Wis, Cha); use 15, 14, 13, 12, 10 and 8 once each", scores["str"], scores["dex"], scores["con"], scores["int"], scores["wis"], scores["cha"])
	}
	inc := map[string]int{}
	incRaw := bmap(body, "ability_increases")
	incTypeProblem := ""
	if incRaw == nil { // {"use_suggestion": true} puts the suggestion in as a map[string]int, not a decoded JSON object
		if m, ok := body["ability_increases"].(map[string]int); ok {
			incRaw = map[string]interface{}{}
			for k, v := range m {
				incRaw[k] = v
			}
		} else if body["ability_increases"] != nil {
			incTypeProblem = "not an object" // said with the background's abilities below
		}
	}
	canonInc := map[string]interface{}{} // under the short names creation uses ("Dexterity" is dex), each a number
	rewrite := false
	for k, v := range incRaw {
		n, present, ok := wholeNumberField(v)
		if present && !ok && incTypeProblem == "" {
			incTypeProblem = fmt.Sprintf("the increase for %s must be a whole number, +2 or +1 (you sent %v)", k, v)
		}
		a := normAbility(k)
		if _, isStr := v.(string); isStr || a != k {
			rewrite = true
		}
		canonInc[a] = bint(canonInc, a) + n
		if n != 0 {
			inc[a] += n
		}
	}
	if rewrite && incTypeProblem == "" && bmap(body, "ability_increases") != nil {
		body["ability_increases"] = canonInc
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
		// the options are the answers themselves (Nov 14: they were the three abilities, and an ability sent alone was kept but
		// never read): +2 and +1 on two of them, the class's best first, or +1 on all three
		var incOpts []map[string]int
		if len(ranked) >= 2 {
			incOpts = append(incOpts, map[string]int{ranked[0]: 2, ranked[1]: 1})
		}
		for _, a := range bg.AbilityScores {
			for _, b := range bg.AbilityScores {
				if a != b && !(len(ranked) >= 2 && a == ranked[0] && b == ranked[1]) {
					incOpts = append(incOpts, map[string]int{a: 2, b: 1})
				}
			}
		}
		allOne := map[string]int{}
		for _, a := range bg.AbilityScores {
			allOne[a] = 1
		}
		incOpts = append(incOpts, allOne)
		if incTypeProblem == "not an object" {
			ex, _ := json.Marshal(incOpts[0])
			incTypeProblem = fmt.Sprintf("send ability_increases as an object: +2 and +1 on two of %s, like %s, or +1 on all three (you sent %v)", strings.Join(bg.AbilityScores, ", "), ex, body["ability_increases"])
		}
		incErr := game.ValidateAbilityIncreases(bg, inc)
		okInc := len(inc) > 0 && incErr == nil && incTypeProblem == ""
		incProblem := incTypeProblem
		if incProblem == "" && len(inc) > 0 && incErr != nil {
			incProblem = incErr.Error()
		}
		s5.Choices = append(s5.Choices, choice("ability_increases", fmt.Sprintf("your background's increases among %s: +2 and +1, or +1 to all three", strings.Join(bg.AbilityScores, ", ")), 1, incOpts, inc, okInc, incProblem))
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
	finishStep(&s5)
	steps = append(steps, s5)

	// 6. Details
	s6 := builderStep{Key: "details", Title: "Step 6: Name, alignment and languages",
		Explain: "Give your character a name and an alignment, and choose two Standard Languages (you already know Common). " + srdAlignmentChoose + " " + srdAlignmentEvil + " " + srdAlignmentIntro}
	name := strings.TrimSpace(bstr(body, "name"))
	nameProblem := ""
	if _, isStr := body["name"].(string); body["name"] != nil && !isStr {
		nameProblem = fmt.Sprintf("send the name as text (you sent %v)", body["name"])
	} else if name == "" {
		nameProblem = "your character needs a name"
	} else if n := len([]rune(fmt.Sprint(body["name"]))); n > maxCharacterNameRunes { // the characters table holds 255 characters: a longer name fails at creation
		nameProblem = fmt.Sprintf("that name is %d characters; the limit is %d: shorten it", n, maxCharacterNameRunes)
	} else if db != nil {
		var n int
		db.QueryRow("SELECT COUNT(*) FROM characters WHERE LOWER(name) = LOWER($1)", name).Scan(&n)
		if n > 0 {
			nameProblem = fmt.Sprintf("%q is already the name of a character on this server: choose another", name)
		}
	}
	s6.Choices = append(s6.Choices, choice("name", "your character's name", 1, nil, nilIfEmpty(name), nameProblem == "", nameProblem))
	if nameProblem != "" {
		s6.Remaining++
	}
	align := bstr(body, "alignment")
	alignOK := canonicalPlayerAlignment(align) != ""
	alignChoice := choice("alignment", "your character's alignment (your own choice; there is no suggestion)", 1, playerAlignments(), nilIfEmpty(align), alignOK, ifStr(align != "" && !alignOK, "not one of the nine alignments"))
	alignChoice["option_details"] = alignmentDescriptions
	s6.Choices = append(s6.Choices, alignChoice)
	if !alignOK {
		s6.Remaining++
	}
	langs := blist(body, "extra_languages")
	langProblem := ""
	for _, l := range langs {
		if strings.EqualFold(l, "common") {
			langProblem = "you already know Common: choose two others"
		}
	}
	langChoice := choice("extra_languages", "two Standard Languages besides Common", 2, standardLanguages, langs, len(langs) == 2 && langProblem == "", langProblem)
	s6.Choices = append(s6.Choices, langChoice)
	if len(langs) != 2 || langProblem != "" {
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
	finishStep(&s6)
	steps = append(steps, s6)

	// 7. Backstory (Nov 12): the player is encouraged to write one
	sb := builderStep{Key: "backstory", Title: "Step 7: Write your character’s backstory",
		Explain: "Tell the story of who your character was before this adventure and what brought them here. A good length is 3 to 6 paragraphs, but it is your character, so write what suits them: shorter or longer, serious or funny. Separate paragraphs with a blank line. Up to 4000 characters. " +
			"A backstory gives the GM and the other players something to play with, so please write one. You can also describe how your character looks (optional): it helps when you make a portrait."}
	story := strings.TrimSpace(bstr(body, "backstory"))
	skip, _ := body["backstory_skip"].(bool)
	skipProblem := ""
	if s, isStr := body["backstory_skip"].(string); isStr { // "true" is true; anything else that isn't true or false is refused
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "yes":
			skip = true
			body["backstory_skip"] = true
		case "false", "no":
			body["backstory_skip"] = false
		default:
			skipProblem = fmt.Sprintf("send backstory_skip as true or false (you sent %q)", s)
		}
	} else if _, isBool := body["backstory_skip"].(bool); body["backstory_skip"] != nil && !isBool {
		skipProblem = fmt.Sprintf("send backstory_skip as true or false (you sent %v)", body["backstory_skip"])
	}
	paras := 0
	for _, p := range regexp.MustCompile(`\n[ \t]*\n`).Split(story, -1) {
		if strings.TrimSpace(p) != "" {
			paras++
		}
	}
	storyProblem := ""
	if _, isStr := body["backstory"].(string); body["backstory"] != nil && !isStr {
		storyProblem = "send the backstory as text"
	} else if n := len([]rune(story)); n > maxProfileTextRunes {
		storyProblem = fmt.Sprintf("that is %d characters; the limit is %d: shorten it", n, maxProfileTextRunes)
	}
	looks := strings.TrimSpace(bstr(body, "appearance"))
	looksProblem := "" // the same limit as the backstory: creation refuses a longer one (Nov 14 tester: accepted here, refused by the review)
	if _, isStr := body["appearance"].(string); body["appearance"] != nil && !isStr {
		looksProblem = "send the appearance as text"
	} else if n := len([]rune(looks)); n > maxProfileTextRunes {
		looksProblem = fmt.Sprintf("that is %d characters; the limit is %d: shorten it", n, maxProfileTextRunes)
	}
	storyOK := storyProblem == "" && (paras > 0 || skip)
	storyChoice := choice("backstory", "your character’s backstory: 3 to 6 paragraphs suggested, your choice of what to write", 1, nil, nilIfEmpty(story), storyOK, storyProblem)
	storyChoice["prompts_from_the_srd"] = map[string]interface{}{"intro": srdImaginePast, "questions": srdImaginePastQuestions}
	sb.Choices = append(sb.Choices, storyChoice)
	sb.Choices = append(sb.Choices, choice("appearance", "optional: a few sentences about how your character looks (up to 4000 characters)", 1, nil, nilIfEmpty(looks), looksProblem == "", looksProblem))
	sb.Choices = append(sb.Choices, choice("backstory_skip", "send true to skip the backstory (not recommended)", 1, []bool{true}, skip, skip, skipProblem))
	if paras > 0 && (paras < 3 || paras > 6) {
		sb.Warnings = append(sb.Warnings, fmt.Sprintf("You wrote %d paragraph(s). 3 to 6 is the suggestion, but it is your character: this is fine if it is what you want to write.", paras))
	}
	if !storyOK {
		sb.Remaining++
	}
	if looksProblem != "" {
		sb.Remaining++
	}
	if skipProblem != "" && storyOK {
		sb.Remaining++
	}
	sb.Done = sb.Remaining == 0
	steps = append(steps, sb)

	// 8. Review: the server checks the draft as the creation would
	s7 := builderStep{Key: "review", Title: "Step 8: Review and create",
		Explain: "When steps 1-7 are done, the server checks the whole draft as a creation would. Then POST /api/character-builder/{id}/finish creates the character; GET /api/characters/{id}/setup then guides the spells, the gear and the portrait."}
	allDone := true
	for _, s := range steps {
		allDone = allDone && s.Done
	}
	if !allDone {
		s7.Remaining = 1
		s7.Problems = []string{"finish steps 1-7 first"}
	} else if withCheck {
		if res := builderDryRun(body, agentID); res["success"] != true {
			s7.Remaining = 1
			s7.Problems = []string{fmt.Sprintf("%v: %v", res["error"], res["message"])}
			if vo, ok := res["valid_options"]; ok {
				s7.Choices = append(s7.Choices, map[string]interface{}{"field": "(server)", "valid_options": vo})
			}
		} else {
			wc, _ := res["would_create"].(map[string]interface{})
			s7.Choices = append(s7.Choices, map[string]interface{}{"field": "(server)", "what": "the character this draft makes", "current": res["would_create"],
				"summary": draftSummary(body, wc), "before_you_finish": "Check the summary. To change anything, send that field again (POST /api/character-builder/{id}); when it is right, POST /api/character-builder/{id}/finish."})
		}
	}
	finishStep(&s7)
	steps = append(steps, s7)
	for i := range steps {
		keepPlayerChoices(&steps[i])
	}
	return steps
}

// keepPlayerChoices: a step's suggestion never names a choice the player has already made validly, so taking the suggestion
// (or sending it back) fills only what is still open (Nov 13: the suggestion turned a Tiefling into a Human, and gold back into kits).
func keepPlayerChoices(st *builderStep) {
	if st.Suggestion == nil {
		return
	}
	for _, c := range st.Choices {
		done, _ := c["done"].(bool)
		field, _ := c["field"].(string)
		if !done || field == "" || c["current"] == nil {
			continue
		}
		if i := strings.Index(field, "."); i > 0 {
			if sub, ok := st.Suggestion[field[:i]].(map[string]interface{}); ok {
				delete(sub, field[i+1:])
				if len(sub) == 0 {
					delete(st.Suggestion, field[:i])
				}
			} else if sub, ok := st.Suggestion[field[:i]].(map[string]string); ok {
				delete(sub, field[i+1:])
				if len(sub) == 0 {
					delete(st.Suggestion, field[:i])
				}
			}
			continue
		}
		if sub, ok := st.Suggestion[field].(map[string]interface{}); ok { // the removals of answers that can't be used stay
			rm := map[string]interface{}{}
			for k, v := range sub {
				if v == nil {
					rm[k] = nil
				}
			}
			if len(rm) > 0 {
				st.Suggestion[field] = rm
				continue
			}
		}
		delete(st.Suggestion, field)
	}
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
func magicInitiateSuggestion(list, class string, avoid ...string) map[string]interface{} {
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
			if len(cs) < 2 && (!pass || !classHas[c]) && onClassList(c, list) && !containsStr(cs, c) && !containsStr(avoid, c) {
				cs = append(cs, c)
			}
		}
	}
	sp := ""
	for _, pass := range []bool{true, false} {
		for _, s := range spells {
			if sp == "" && (!pass || !classHas[s]) && onClassList(s, list) && !containsStr(avoid, s) {
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
		return opts, "" // no suggestion the step would refuse
	case "feat":
		return []string{"alert", "magic_initiate", "savage_attacker", "skilled"}, "skilled"
	}
	return nil, ""
}

// optionKey: an answer written as an option is ("High Elf", "high-elf" and "high_elf" are all high_elf).
func optionKey(s string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
}

// canonSkill: a skill as it is stored ("sleight of hand") for any spelling of it ("Sleight_of_Hand", "sleight-of-hand"), or "" when
// the name is not one of the 18 skills.
func canonSkill(s string) string {
	k := strings.Join(strings.Fields(skillKey(s)), " ")
	if game.IsSkill(k) {
		return k
	}
	return ""
}

// titleFirst: the text with a capital first letter (a sentence that starts with withArticle).
func titleFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// withArticle: "a Dwarf", "an Elf".
func withArticle(name string) string {
	if name != "" && strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

// overlapsAny: whether two lists of spells share one.
func overlapsAny(a, b []string) bool {
	for _, x := range a {
		if containsStr(b, x) {
			return true
		}
	}
	return false
}

// speciesCantripFree: whether a species has a lineage or legacy whose cantrips are not among the given ones (every Tiefling
// legacy gives Thaumaturgy).
func speciesCantripFree(species string, cantrips []string) bool {
	sp := game.GetSpecies(species)
	if sp == nil || len(cantrips) == 0 {
		return true
	}
	for _, need := range []string{"lineage", "legacy"} {
		if !containsStr(sp.Needs, need) {
			continue
		}
		opts, _ := speciesChoiceOptions(sp.Key, need, "", nil)
		for _, o := range opts {
			if !overlapsAny(game.SpeciesCantrips(sp.Key, o), cantrips) {
				return true
			}
		}
		return false
	}
	return true
}

// draftMICantrips: the cantrips the background's Magic Initiate picks in the draft, as spell slugs.
func draftMICantrips(body map[string]interface{}) []string {
	bg := game.GetBackground(strings.ToLower(bstr(body, "background")))
	if bg == nil || bg.Feat != "magic_initiate" {
		return nil
	}
	var out []string
	for _, c := range blist(bmap(body, "background_feat_choices"), "cantrips") {
		out = append(out, strings.ReplaceAll(c, " ", "-"))
	}
	return out
}

// draftHeldTools: the tool proficiencies the draft has before the Origin feats, as creation counts them: the class's choices and
// its own tools, and the background's tool.
func draftHeldTools(body map[string]interface{}, class string, bg *game.Background) []string {
	out := append([]string(nil), blist(body, "tool_proficiencies")...)
	out = append(out, game.ProficienciesFor(game.CharacterInput{FirstClass: class}).Tools...)
	if bg != nil {
		if bg.Tool != "" {
			out = append(out, bg.Tool)
		}
		if t, ok := game.FindTool(bstr(body, "background_tool")); ok && bg.ToolCategory != "" && t.Category == bg.ToolCategory {
			out = append(out, t.Name)
		}
	}
	return out
}

// rawList: the strings of a list answer as sent (trimmed); notText is true when it isn't a list of strings.
func rawList(v interface{}) (out []string, notText bool) {
	switch l := v.(type) {
	case nil:
	case []interface{}:
		for _, x := range l {
			s, ok := x.(string)
			if !ok {
				return out, true
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range l {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	default:
		return nil, true
	}
	return out, false
}

// findToolLoose: a tool by its name, also written without the apostrophe ("thieves tools", "calligraphers supplies").
func findToolLoose(s string) (game.Tool, bool) {
	if t, ok := game.FindTool(s); ok {
		return t, true
	}
	bare := func(x string) string {
		return strings.NewReplacer("'", "", "’", "").Replace(game.NormalizeToolName(x))
	}
	b := bare(s)
	for _, t := range game.Tools {
		if n := bare(t.Name); n == b || n == strings.TrimSuffix(b, " set") {
			return t, true
		}
	}
	return game.Tool{}, false
}

// wholeNumber reads a number answer: present is false when nothing was sent; ok is false when it isn't a whole number
// ("15" is 15; 15.5, "fifteen" and true are not numbers creation can read).
func wholeNumberField(v interface{}) (n int, present, ok bool) {
	switch x := v.(type) {
	case nil:
		return 0, false, true
	case float64:
		if x == float64(int(x)) {
			return int(x), true, true
		}
	case int:
		return x, true, true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return i, true, true
		}
	}
	return 0, true, false
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
	delete(b, "known_spells") // the builder takes no spells (an older draft may hold some): they are chosen in setup
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
	return builderViewOf(id, body, builderSteps(body, agentID, true))
}

// suggestionCovers: the step's suggestion has an answer for this choice.
func suggestionCovers(sug map[string]interface{}, field string) bool {
	if field == "str, dex, con, int, wis, cha" {
		_, ok := sug["str"]
		return ok
	}
	top, sub := splitField(field)
	if sub == "" {
		_, ok := sug[top]
		return ok
	}
	switch m := sug[top].(type) {
	case map[string]interface{}:
		_, ok := m[sub]
		return ok
	case map[string]string:
		_, ok := m[sub]
		return ok
	}
	return false
}

// openChoices: the fields of a step's choices still open, split by whether the step's suggestion answers them.
func openChoices(st *builderStep) (withSug, without []string) {
	for _, c := range st.Choices {
		f, _ := c["field"].(string)
		if done, _ := c["done"].(bool); done || f == "" || strings.HasPrefix(f, "(") {
			continue
		}
		if suggestionCovers(st.Suggestion, f) {
			withSug = append(withSug, f)
		} else {
			without = append(without, f)
		}
	}
	return
}

// builderViewOf: the draft with its steps, the current step in detail and what to send next.
func builderViewOf(id int, body map[string]interface{}, steps []builderStep) map[string]interface{} {
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
	out := map[string]interface{}{"draft_id": id, "steps": list, "choices_remaining": total, "draft": body,
		"choices_remaining_note": "This count can go up: a class, background or species brings its own follow-up choices (skills, a tool, a lineage ...), and an answer that conflicts with a later one (a skill your background also gives) opens again."}
	var leftover []string // fields an older draft holds that the builder doesn't read
	for _, k := range []string{"known_spells", "divine_order", "primal_order", "draconic_ancestry"} {
		if _, has := body[k]; has {
			leftover = append(leftover, k)
		}
	}
	if len(leftover) > 0 {
		out["fields_not_used"] = fmt.Sprintf("This draft holds %s from before, which the builder doesn't read or create (spells are chosen after creation, in setup; an order goes in class_options, a Dragonborn's ancestry in species_choices). Send them as null to remove them.", strings.Join(leftover, ", "))
	}
	if current != nil {
		for _, c := range current.Choices { // a Magic Initiate answer with parts still missing says which (Nov 14: no word, the step stayed open)
			f, _ := c["field"].(string)
			if done, _ := c["done"].(bool); !done && isFeatGroup(f) && c["problem"] == nil && c["current"] != nil {
				if need := featStillNeeded(body, f); len(need) > 0 {
					c["still_needed"] = need
				}
			}
		}
		out["current_step"] = current
		send := map[string]interface{}{"method": "POST", "path": fmt.Sprintf("/api/character-builder/%d", id)}
		if len(current.Suggestion) > 0 {
			send["body"] = current.Suggestion
			send["or"] = map[string]interface{}{"use_suggestion": true}
		}
		out["send"] = send
		if current.Key == "review" {
			out["next"] = fmt.Sprintf("%s: the server's check of the draft found a problem: %s. Fix that field (POST /api/character-builder/%d), then GET this draft again; there is no suggestion for it.", current.Title, joinProblems(current.Problems), id)
		} else {
			withSug, without := openChoices(current)
			next := fmt.Sprintf("%s: %d choice(s) left. Send the fields (POST /api/character-builder/%d)", current.Title, current.Remaining, id)
			if len(withSug) > 0 {
				next += fmt.Sprintf(", or {\"use_suggestion\": true} to take the suggestion for %s", strings.Join(withSug, ", "))
			}
			next += "."
			if len(without) > 0 {
				next += fmt.Sprintf(" No suggestion for %s: that is yours to choose.", strings.Join(without, ", "))
			}
			out["next"] = next + " GET this draft again any time."
		}
	} else {
		out["next"] = fmt.Sprintf("Every step is done. Check the summary below, then create the character: POST /api/character-builder/%d/finish", id)
		for _, st := range steps { // the review step's summary of the character (Nov 13 tester: no summary before creation)
			if st.Key != "review" {
				continue
			}
			for _, c := range st.Choices {
				if sm, ok := c["summary"]; ok {
					out["summary_before_you_finish"] = sm
					out["to_change_something"] = fmt.Sprintf("Send that field again: POST /api/character-builder/%d", id)
				}
			}
		}
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

// saveDraft stores the draft; an error means nothing was saved (Nov 14 audit: a refused write was reported as saved).
func saveDraft(id int, body map[string]interface{}) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE character_drafts SET body = $1, updated_at = $2 WHERE id = $3`, raw, time.Now(), id)
	return err
}

// readBuilderBody: the request's JSON object (no body is an empty one).
func readBuilderBody(r *http.Request) (map[string]interface{}, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return map[string]interface{}{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("there is more after the JSON object")
	}
	if v == nil {
		return map[string]interface{}{}, nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("the body must be a JSON object, like {\"class\": \"fighter\"}")
	}
	return m, nil
}

var builderStepKeys = []string{"class", "class_choices", "background", "species", "ability_scores", "details", "backstory"}

// joinProblems: problems as one sentence ("invalid_divine_order: Only Clerics choose a divine_order").
func joinProblems(ps []string) string {
	var out []string
	for _, p := range ps {
		out = append(out, strings.TrimRight(p, ". "))
	}
	return strings.Join(out, "; ")
}

// builderOutcome: what one request did to a draft.
type builderOutcome struct {
	unknown         []string                 // fields or parts the builder doesn't know: nothing is saved
	errCode, errMsg string                   // the request refused as a whole (use_suggestion alone, with nothing it can fill)
	refused         []map[string]interface{} // answers not saved, each with the reason
	notes           []string                 // answers read under another name
	filled          []string                 // parts the suggestion filled
	replaced        []map[string]interface{} // earlier answers the suggestion changed (they had a problem)
	sugNote         string
	anySaved        bool
	stillNeeded     []string
}

// takeSuggestion applies use_suggestion (true: the current step; a step's key: that step) to the draft: open choices are filled and
// those with a problem fixed, valid answers stay. It returns an error code and message when there is nothing it can fill.
func (o *builderOutcome) takeSuggestion(body map[string]interface{}, key string, agentID int) (string, string) {
	steps := builderSteps(body, agentID, false)
	var target *builderStep
	if key != "" {
		for i := range steps {
			if steps[i].Key == key {
				target = &steps[i]
			}
		}
		if target == nil && key != "review" {
			return "unknown_step", fmt.Sprintf("%q is not a step: use_suggestion takes true (the current step) or a step's key: %s", key, strings.Join(builderStepKeys, ", "))
		}
	} else {
		for i := range steps {
			if !steps[i].Done {
				target = &steps[i]
				break
			}
		}
	}
	if target == nil || target.Key == "review" {
		full := builderSteps(body, agentID, true)
		rv := full[len(full)-1]
		if !rv.Done {
			if len(rv.Problems) > 0 && rv.Problems[0] == "finish steps 1-7 first" {
				return "no_suggestion", "Step 8, the review, has no suggestion: finish steps 1-7 first."
			}
			return "no_suggestion", "Steps 1-7 are done, and the review found a problem: " + joinProblems(rv.Problems) + ". There is no suggestion for it: send that field yourself."
		}
		o.sugNote = "Nothing to fill: every step is done. POST /api/character-builder/{id}/finish creates the character."
		return "", ""
	}
	withSug, without := openChoices(target)
	if !target.Done && len(withSug) == 0 {
		return "no_suggestion", fmt.Sprintf("%s: there is no suggestion for %s: that choice is yours (see current_step.choices for the options).", target.Title, strings.Join(without, ", "))
	}
	o.filled, o.replaced = applySuggestion(body, *target)
	if len(o.filled) == 0 && len(o.replaced) == 0 {
		o.sugNote = target.Title + " is done: nothing to fill. To change a choice, send it."
	} else if len(without) > 0 {
		o.sugNote = fmt.Sprintf("No suggestion for %s: that is yours to choose.", strings.Join(without, ", "))
	}
	return "", ""
}

// applyBuilderUpdate checks one request and applies it to the draft (body, changed in place). Each answer is saved as sent (in
// the server's spelling) or refused with the reason; an answer with a problem is not saved and the earlier one stays.
func applyBuilderUpdate(body, upd map[string]interface{}, agentID int) builderOutcome {
	var o builderOutcome
	in := checkBuilderInput(upd, body)
	if len(in.unknown) > 0 {
		o.unknown = in.unknown
		return o
	}
	o.refused, o.notes = in.refused, in.notes
	useSug, hasSug := upd["use_suggestion"]
	delete(upd, "use_suggestion")
	before, _ := deepCopyJSON(body).(map[string]interface{}) // group answers (species_choices ...) are merged in place below
	if before == nil {
		before = map[string]interface{}{}
	}
	if hasSug && useSug != nil && useSug != false {
		code, msg := "", ""
		if key, isStr := useSug.(string); isStr {
			code, msg = o.takeSuggestion(body, strings.ToLower(strings.TrimSpace(key)), agentID)
		} else if useSug == true {
			code, msg = o.takeSuggestion(body, "", agentID)
		} else {
			code, msg = "invalid_use_suggestion", "use_suggestion is true (the current step) or a step's key: "+strings.Join(builderStepKeys, ", ")
		}
		if code != "" {
			if len(upd) == 0 && len(o.refused) == 0 {
				o.errCode, o.errMsg = code, msg
				return o
			}
			o.refused = append(o.refused, map[string]interface{}{"field": "use_suggestion", "you_sent": useSug, "problem": msg})
		}
	}
	refusedIn := map[string]bool{}
	for _, r := range in.refused {
		refusedIn[r["field"].(string)] = true
	}
	sentPart := func(path string) bool { // a field (or part) the player sent, even refused: the suggestion doesn't fill it
		top, sub := splitField(path)
		if refusedIn[path] || refusedIn[top] {
			return true
		}
		v, sent := upd[top]
		if m, isMap := v.(map[string]interface{}); sent && isMap && sub != "" {
			_, has := m[sub]
			return has
		}
		return sent
	}
	var keepFilled []string
	for _, f := range o.filled {
		if !sentPart(f) {
			keepFilled = append(keepFilled, f)
		} else if top, _ := splitField(f); refusedIn[f] || refusedIn[top] {
			rollbackAnswer(body, before, f) // the refused answer stays unsaved: the draft keeps what it had
		}
	}
	o.filled = keepFilled
	var keepReplaced []map[string]interface{}
	for _, r := range o.replaced {
		if !sentPart(fmt.Sprint(r["field"])) {
			keepReplaced = append(keepReplaced, r)
		}
	}
	o.replaced = keepReplaced
	for k, v := range upd { // fields sent with use_suggestion win
		if v == nil {
			delete(body, k)
			continue
		}
		// A group of choices (species_choices, class_options, background_feat_choices ...) merges: the parts sent win, the rest stay;
		// a part sent as null is removed. The ability increases are one answer (+2 and +1): they replace the earlier ones.
		if nm, isMap := v.(map[string]interface{}); isMap && k != "ability_increases" {
			have, hasMap := body[k].(map[string]interface{})
			if !hasMap {
				have = map[string]interface{}{}
			}
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
		body[k] = v
	}
	// an answer with a problem is not saved: the earlier answer stays (Nov 13 tester: invalid skills stayed in the draft while
	// flagged). Checked again after each refusal: an Expertise sent with refused skills is checked against the skills kept.
	var beforeSteps []builderStep
	problemBefore := func(f string) string {
		if beforeSteps == nil {
			beforeSteps = builderSteps(before, agentID, false)
		}
		for _, st := range beforeSteps {
			for _, c := range st.Choices {
				if c["field"] == f {
					p, _ := c["problem"].(string)
					return p
				}
			}
		}
		return ""
	}
	skip := map[string]bool{}
	for i := 0; i < 4; i++ {
		rj := rejectedFields(builderSteps(body, agentID, false), upd, body, problemBefore, skip)
		if len(rj) == 0 {
			break
		}
		for _, r := range rj {
			p := r["field"].(string)
			rollbackAnswer(body, before, p)
			skip[p] = true
		}
		o.refused = append(o.refused, rj...)
	}
	rej := map[string]bool{}
	for _, r := range o.refused {
		rej[r["field"].(string)] = true
	}
	o.anySaved = len(o.filled) > 0
	for k, v := range upd {
		if rej[k] {
			continue
		}
		if m, ok := v.(map[string]interface{}); ok && k != "ability_increases" {
			for sk := range m {
				if !rej[k+"."+sk] {
					o.anySaved = true
				}
			}
			if isFeatGroup(k) && len(m) > 0 {
				if need := featStillNeeded(body, k); len(need) > 0 {
					o.stillNeeded = append(o.stillNeeded, k+": "+strings.Join(need, ", "))
				}
			}
			continue
		}
		o.anySaved = true
	}
	return o
}

// decorate adds what the request did to the reply (refused answers, what the suggestion filled) and gives its status: 200, 422 when
// some answers were refused and others saved, 400 when none was saved.
func (o *builderOutcome) decorate(view map[string]interface{}, steps []builderStep) int {
	var msgs []string
	if len(o.notes) > 0 {
		view["read_as"] = o.notes
	}
	if len(o.filled) > 0 {
		view["filled_by_suggestion"] = o.filled
	}
	if len(o.replaced) > 0 {
		view["replaced_by_suggestion"] = o.replaced
		var f []string
		for _, r := range o.replaced {
			f = append(f, fmt.Sprint(r["field"]))
		}
		msgs = append(msgs, "The suggestion replaced answers that had a problem: "+strings.Join(f, ", ")+" (see replaced_by_suggestion).")
	}
	if len(o.stillNeeded) > 0 {
		view["still_needed"] = o.stillNeeded
		msgs = append(msgs, "Saved; still needed: "+strings.Join(o.stillNeeded, "; ")+".")
	}
	if o.sugNote != "" {
		msgs = append(msgs, o.sugNote)
	}
	status := http.StatusOK
	if len(o.refused) > 0 {
		sugKeys := map[string]bool{} // fields use_suggestion would fill: open choices the suggestion answers
		for i := range steps {
			withSug, _ := openChoices(&steps[i])
			for _, f := range withSug {
				for _, part := range strings.Split(f, ", ") {
					top, _ := splitField(part)
					sugKeys[top] = true
				}
			}
		}
		var lines, withSug []string // said plainly too (Nov 14 tester: +2 Dexterity refused in not_saved, missed, then a suggestion filled +2 Intelligence)
		for _, rj := range o.refused {
			f := fmt.Sprint(rj["field"])
			lines = append(lines, fmt.Sprintf("%s: %s", f, strings.TrimRight(fmt.Sprint(rj["problem"]), ".")))
			if top, _ := splitField(f); sugKeys[top] && !containsStr(withSug, top) {
				withSug = append(withSug, top)
			}
		}
		msg := "NOT SAVED: " + strings.Join(lines, "; ") + ". Send these again with a valid choice."
		if len(withSug) > 0 {
			msg += " {\"use_suggestion\": true} would fill " + strings.Join(withSug, ", ") + " with the suggestion."
		}
		msgs = append([]string{msg}, msgs...)
		view["not_saved"] = o.refused
		// not 200 even when other answers were saved (Nov 14 tester: refusals in a 200 reply were missed, then a suggestion filled them)
		status = http.StatusUnprocessableEntity
		view["error"] = "some_answers_not_saved"
		view["partially_saved"] = true
		view["not_saved_note"] = "These answers were not saved (the draft keeps what it had before); everything else you sent was saved. Fix them and send them again."
		if !o.anySaved {
			status = http.StatusBadRequest
			view["error"] = "answers_not_valid"
			view["partially_saved"] = false
			view["not_saved_note"] = "Nothing you sent was saved (the draft keeps what it had before). Fix these and send them again."
		}
	}
	if len(msgs) > 0 {
		view["message"] = strings.Join(msgs, " ")
	}
	return status
}

// handleCharacterBuilder godoc
// @Summary Character builder: create a character step by step
// @Description Guides character creation in the order of the 2024 Player's Handbook (and the D&D Beyond builder): 1 class, 2 the class's choices (skills, Weapon Mastery, tools, Expertise, Divine/Primal Order, starting equipment), 3 background, 4 species, 5 ability scores (the Standard Array only; the PHB's Standard Array by Class is suggested), 6 name, alignment and languages, 7 backstory (3 to 6 paragraphs suggested) and appearance, 8 review (a summary before you create). POST starts a draft (with any fields, checked as an update is); GET lists your drafts (newest first; ?limit= up to 100 and ?offset=, with the total); GET /api/character-builder/{id} shows every step with the choices left, the current step's options and a suggestion; POST /api/character-builder/{id} with fields (any builder field; null removes one) or {"use_suggestion": true} (fills the open choices and fixes the ones with a problem; valid answers stay; the class, the alignment and the backstory have no suggestion: send them yourself). Each answer is saved as sent or refused with the reason (not_saved; 422 when other answers were saved, 400 when none was). POST /api/character-builder/{id}/finish (no body) creates the character; DELETE /api/character-builder/{id} removes a draft (a character it made is kept). Then GET /api/characters/{id}/setup guides spells, gear, the shop and a portrait.
// @Tags Characters
// @Accept json
// @Produce json
// @Param request body object{name=string,use_suggestion=bool} false "Start a draft, or fields to set"
// @Success 200 {object} map[string]interface{}
// @Router /character-builder [post]
// @Router /character-builder [get]
// @Router /character-builder/{id} [get]
// @Router /character-builder/{id} [post]
// @Router /character-builder/{id} [delete]
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
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			listBuilderDrafts(w, r, agentID)
		case http.MethodPost:
			startBuilderDraft(w, r, agentID)
		default:
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET lists your drafts; POST starts one"})
		}
		return
	}
	parts := strings.Split(rest, "/")
	id, convErr := strconv.Atoi(parts[0])
	if convErr != nil || id <= 0 || len(parts) > 2 || len(parts) == 2 && parts[1] != "finish" { // /finsh is not a draft update
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "not_found",
			"message": "The builder's addresses: /api/character-builder (GET, POST), /api/character-builder/{id} (GET, POST, DELETE) and /api/character-builder/{id}/finish (POST)."})
		return
	}
	body, charID, ok := loadDraft(id, agentID)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "draft_not_found", "message": "GET /api/character-builder lists your drafts"})
		return
	}
	if len(parts) == 2 {
		finishBuilderDraft(w, r, id, body, charID, agentID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSONStatus(w, 200, builderView(id, body, charID, agentID))
		return
	case http.MethodDelete:
		if _, err := db.Exec(`DELETE FROM character_drafts WHERE id = $1 AND agent_id = $2`, id, agentID); err != nil {
			writeJSONStatus(w, 500, map[string]interface{}{"error": "could_not_delete", "message": "The draft was not deleted. Try again."})
			return
		}
		msg := "The draft is deleted."
		if charID != nil {
			msg += fmt.Sprintf(" The character it made (id %d) is kept.", *charID)
		}
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "deleted": true, "draft_id": id, "message": msg})
		return
	case http.MethodPost:
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed", "message": "GET shows the draft; POST sets fields; DELETE removes it"})
		return
	}
	if charID != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "already_created", "message": fmt.Sprintf("This draft made character %d. Use GET /api/characters/%d/setup.", *charID, *charID)})
		return
	}
	upd, perr := readBuilderBody(r)
	if perr != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": "Nothing was saved: " + perr.Error()})
		return
	}
	o := applyBuilderUpdate(body, upd, agentID)
	if len(o.unknown) > 0 { // a typo must not look like a saved choice (Nov 13 tester)
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_field", "message": strings.Join(o.unknown, "; ") + ". Nothing was saved: send the request again with the right field names.",
			"nothing_saved": true, "draft": builderView(id, body, nil, agentID)})
		return
	}
	if o.errCode != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": o.errCode, "message": o.errMsg, "nothing_saved": true, "draft": builderView(id, body, nil, agentID)})
		return
	}
	if err := saveDraft(id, body); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "could_not_save", "message": "Nothing was saved: the server could not store the draft. Send the request again."})
		return
	}
	steps := builderSteps(body, agentID, true)
	view := builderViewOf(id, body, steps)
	writeJSONStatus(w, o.decorate(view, steps), view)
}

// listBuilderDrafts: the player's drafts, newest first, with the total and paging (Nov 14 audit: only the latest 20, silently).
func listBuilderDrafts(w http.ResponseWriter, r *http.Request, agentID int) {
	limit, offset := 20, 0
	for _, p := range []struct {
		name string
		dst  *int
		min  int
		max  int
	}{{"limit", &limit, 1, 100}, {"offset", &offset, 0, 1 << 30}} {
		if v := r.URL.Query().Get(p.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < p.min || n > p.max {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_" + p.name, "message": fmt.Sprintf("%s is a whole number from %d to %d", p.name, p.min, p.max)})
				return
			}
			*p.dst = n
		}
	}
	var total int
	db.QueryRow(`SELECT COUNT(*) FROM character_drafts WHERE agent_id = $1`, agentID).Scan(&total)
	rows, err := db.Query(`SELECT id, body, character_id FROM character_drafts WHERE agent_id = $1 ORDER BY id DESC LIMIT $2 OFFSET $3`, agentID, limit, offset)
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
	out := map[string]interface{}{"drafts": list, "total": total, "limit": limit, "offset": offset,
		"start": "POST /api/character-builder {\"name\": \"...\"} starts a new draft", "delete": "DELETE /api/character-builder/{id} removes a draft (a character it made is kept)"}
	if offset+len(list) < total {
		out["more"] = fmt.Sprintf("Showing %d to %d of %d drafts, newest first: GET /api/character-builder?offset=%d for the next ones.", offset+1, offset+len(list), total, offset+len(list))
	}
	writeJSONStatus(w, 200, out)
}

// startBuilderDraft: a new draft, with the fields sent checked as an update's are (Nov 14 audit: they were stored unchecked).
func startBuilderDraft(w http.ResponseWriter, r *http.Request, agentID int) {
	upd, perr := readBuilderBody(r)
	if perr != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": "No draft was started: " + perr.Error()})
		return
	}
	body := map[string]interface{}{}
	o := applyBuilderUpdate(body, upd, agentID)
	if len(o.unknown) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_field", "message": strings.Join(o.unknown, "; ") + ". No draft was started: send the request again with the right field names.", "nothing_saved": true})
		return
	}
	if o.errCode != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": o.errCode, "message": "No draft was started: " + o.errMsg, "nothing_saved": true})
		return
	}
	if len(o.refused) > 0 && !o.anySaved {
		view := map[string]interface{}{}
		o.decorate(view, nil)
		view["message"] = "No draft was started: " + view["message"].(string)
		view["nothing_saved"] = true
		writeJSONStatus(w, http.StatusBadRequest, view)
		return
	}
	raw, _ := json.Marshal(body)
	var id int
	if db.QueryRow(`INSERT INTO character_drafts (agent_id, body) VALUES ($1, $2) RETURNING id`, agentID, raw).Scan(&id) != nil {
		writeJSONStatus(w, 500, map[string]interface{}{"error": "could_not_start_draft", "message": "No draft was started: the server could not store it. Send the request again."})
		return
	}
	steps := builderSteps(body, agentID, true)
	view := builderViewOf(id, body, steps)
	status := o.decorate(view, steps)
	if seats := campaignSeats(agentID); len(seats) > 0 { // told before the work, not after (Nov 13 tester)
		view["campaigns_note"] = fmt.Sprintf("You already play %s. A player has one character in a campaign: this new character can join a different campaign, or take that seat only if its GM removes the other character.", strings.Join(seats, "; "))
	}
	writeJSONStatus(w, status, view)
}

// finishBuilderDraft creates the character (POST only; Nov 14 audit: a GET created it). It takes no fields.
func finishBuilderDraft(w http.ResponseWriter, r *http.Request, id int, body map[string]interface{}, charID *int, agentID int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed",
			"message": fmt.Sprintf("Nothing was created: POST /api/character-builder/%d/finish creates the character. GET /api/character-builder/%d shows the draft.", id, id)})
		return
	}
	extra, perr := readBuilderBody(r) // read the body: a reply sent before it is read can be reset on some systems
	if perr != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": "Nothing was created: " + perr.Error()})
		return
	}
	if len(extra) > 0 { // fields here would be ignored: say so instead (Nov 14 audit)
		var names []string
		for k := range extra {
			names = append(names, k)
		}
		sort.Strings(names)
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "finish_takes_no_fields",
			"message": fmt.Sprintf("Nothing was created: /finish takes no fields (you sent %s). Send changes to POST /api/character-builder/%d first, then POST /api/character-builder/%d/finish with no body.", strings.Join(names, ", "), id, id)})
		return
	}
	if charID != nil { // finished before (a retried request): the same answer
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
	if seats := campaignSeats(agentID); len(seats) > 0 {
		res["campaigns_note"] = fmt.Sprintf("You already play %s. A player has one character in a campaign, so this character can join a different campaign, or take that seat if its GM removes the other character.", strings.Join(seats, "; "))
	}
	writeJSONStatus(w, 200, res)
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
	Choices map[string]interface{} `json:"choices,omitempty"` // what the player can choose from, each option explained (spells)
	// Optional: the character is finished without it (the shop, the portrait, gear reminders); steps_left counts only the others
	Optional bool `json:"optional,omitempty"`

	Problem string `json:"problem,omitempty"` // what is wrong with what is stored, and what the request fixes
}

// spellOptionInfo: one spell explained for the player choosing it.
func spellOptionInfo(slug string, suggested map[string]bool) map[string]interface{} {
	sp, ok := srdSpellsMemory[slug]
	if !ok {
		return nil
	}
	m := map[string]interface{}{"spell": slug, "name": sp.Name, "level": sp.Level, "school": sp.School, "casting_time": sp.CastingTime,
		"range": sp.Range, "duration": sp.Duration, "components": sp.Components, "what_it_does": sp.Description, "suggested": suggested[slug]}
	if sp.DamageDice != "" {
		m["damage"] = strings.TrimSpace(sp.DamageDice + " " + sp.DamageType)
	}
	if sp.SavingThrow != "" {
		m["saving_throw"] = sp.SavingThrow
	}
	if sp.Healing != "" {
		m["healing"] = sp.Healing
	}
	if sp.IsRitual {
		m["ritual"] = true
	}
	if strings.Contains(strings.ToLower(sp.Duration), "concentration") {
		m["concentration"] = true
	}
	return m
}

// spellOptions: the candidates (class list, level range, not already held), explained, lowest level first.
func spellOptions(cands []string, minLvl, maxLvl int, avoid map[string]bool, suggested map[string]bool) []map[string]interface{} {
	var slugs []string
	for _, c := range cands {
		if sp, ok := srdSpellsMemory[c]; ok && sp.Level >= minLvl && sp.Level <= maxLvl && !avoid[c] {
			slugs = append(slugs, c)
		}
	}
	sort.SliceStable(slugs, func(a, b int) bool {
		la, lb := srdSpellsMemory[slugs[a]].Level, srdSpellsMemory[slugs[b]].Level
		if la != lb {
			return la < lb
		}
		return srdSpellsMemory[slugs[a]].Name < srdSpellsMemory[slugs[b]].Name
	})
	var out []map[string]interface{}
	for _, c := range slugs {
		out = append(out, spellOptionInfo(c, suggested))
	}
	return out
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
		known := getFightingStyles(charID)
		if containsStr(known, style) { // Additional Fighting Style (Champion 7): suggest one not known yet (Nov 13: it suggested the same one again)
			for _, s := range []string{"defense", "great_weapon_fighting", "archery", "two_weapon_fighting"} {
				if !containsStr(known, s) {
					style = s
					break
				}
			}
		}
		var fsOpts []map[string]string
		var fsSlugs []string
		for slug := range fightingStyles {
			fsSlugs = append(fsSlugs, slug)
		}
		sort.Strings(fsSlugs)
		for _, slug := range fsSlugs {
			if containsStr(known, slug) || !containsStr(fightingStyles[slug].Classes, cl) {
				continue
			}
			f := fightingStyles[slug]
			does := f.Description
			if ft, ok := availableFeats[slug]; ok && len(ft.Benefits) > 0 {
				does = strings.Join(ft.Benefits, " ") // the feat's SRD text
			}
			fsOpts = append(fsOpts, map[string]string{"style": slug, "name": f.Name, "what_it_does": does})
		}
		fsWhy := "Fighting Style. " + classFeatureText(cl, "Fighting Style")
		if len(known) > 0 {
			fsWhy = "Additional Fighting Style: you gain another Fighting Style feat. You have: " + strings.Join(known, ", ") + "."
		}
		steps = append(steps, setupStep{Key: "fighting_style", Title: "Choose a Fighting Style", Why: fsWhy,
			Request: req("POST", "/api/characters/fighting-style", map[string]interface{}{"character_id": charID, "style": style}),
			Choices: map[string]interface{}{"options": fsOpts}})
	}
	if has("Eldritch Invocations") {
		pick := suggestInvocation(charID)
		st := setupStep{Key: "invocations", Title: "Choose your Eldritch Invocation", Why: "Eldritch Invocations. " + classFeatureText("warlock", "Eldritch Invocations") + " Send one invocation per request.",
			Choices: map[string]interface{}{"options_you_qualify_for": invocationOptions(charID)}}
		if pick != "" {
			body := map[string]interface{}{"character_id": charID, "invocation": pick}
			if pick == "pact-of-the-tome" { // the Book of Shadows picks go with it (F9)
				body["cantrips"], body["rituals"] = suggestBookOfShadows(charID)
				st.Why += " Pact of the Tome: send cantrips (3 cantrips) and rituals (2 level 1 Ritual spells) for your Book of Shadows with it; any class's spell list."
			}
			st.Request = req("POST", "/api/characters/invocations", body)
		} else {
			st.Request = req("GET", fmt.Sprintf("/api/characters/invocations?character_id=%d", charID), nil)
		}
		steps = append(steps, st)
	}
	if has("Pact of the Tome") {
		c, rt := suggestBookOfShadows(charID)
		var cOpts, rOpts []map[string]interface{}
		sugg := map[string]bool{}
		for _, x := range append(append([]string{}, c...), rt...) {
			sugg[x] = true
		}
		bh := bookHave(charID)
		for _, slug := range sortedSpellSlugs() {
			sp := srdSpellsMemory[slug]
			if sp.Level == 0 && bookPickOK(slug, false, bh) {
				cOpts = append(cOpts, spellOptionInfo(slug, sugg))
			} else if sp.Level == 1 && sp.IsRitual && bookPickOK(slug, true, bh) {
				rOpts = append(rOpts, spellOptionInfo(slug, sugg))
			}
		}
		steps = append(steps, setupStep{Key: "book_of_shadows", Title: "Fill your Book of Shadows", Why: "Pact of the Tome: choose three cantrips and two level 1 spells with the Ritual tag, from any class's spell list, that you don't already have prepared. While the book is on your person you have them prepared, and they work as Warlock spells for you.",
			Request: req("POST", "/api/characters/invocations", map[string]interface{}{"character_id": charID, "invocation": "pact-of-the-tome", "cantrips": c, "rituals": rt}),
			Choices: map[string]interface{}{"cantrips": map[string]interface{}{"choose": 3, "options": cOpts}, "rituals": map[string]interface{}{"choose": 2, "options": rOpts}}})
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
		var expOpts []map[string]string
		for _, s := range append([]string{}, skillOrder...) {
			if heldE[s] {
				row := srdSkillRows[s]
				o := map[string]string{"skill": s, "ability": row[0], "example_uses": row[1]}
				if containsStr(cur, s) {
					o["already_expertise"] = "yes"
				}
				expOpts = append(expOpts, o)
			}
		}
		rule := classFeatureText(cl, "Expertise")
		if cl == "wizard" {
			rule = classFeatureText(cl, "Scholar")
		}
		steps = append(steps, setupStep{Key: "expertise", Title: "Choose your Expertise skills", Why: rule + fmt.Sprintf(" You have %d of %d; send all of them (the ones you have and the new ones).", len(cur), want),
			Request: req("POST", fmt.Sprintf("/api/characters/%d/expertise", charID), map[string]interface{}{"skills": picks}),
			Choices: map[string]interface{}{"expertise_in_all": want, "you_have": cur, "options_your_skill_proficiencies": expOpts}})
	}
	held, _ := heldSkills(charID)
	if has("choose a subclass") {
		if sub := srdSubclassFor[cl]; sub != "" {
			steps = append(steps, setupStep{Key: "subclass", Title: "Choose your subclass", Why: classFeatureText(cl, titleCase(cl)+" Subclass") + " Each option below has its introduction and its features by level.",
				Request: req("POST", "/api/characters/subclass", map[string]interface{}{"character_id": charID, "subclass": sub}), Choices: subclassChoices(cl)})
		}
	}
	if has("Metamagic") {
		known := getMetamagicChoices(charID)
		var mmOpts []map[string]interface{}
		var mmSlugs []string
		for slug := range metamagicOptions {
			mmSlugs = append(mmSlugs, slug)
		}
		sort.Strings(mmSlugs)
		for _, slug := range mmSlugs {
			if !containsStr(known, slug) {
				o := metamagicOptions[slug]
				mmOpts = append(mmOpts, map[string]interface{}{"metamagic": slug, "name": o.Name, "cost_sorcery_points": o.Cost, "what_it_does": o.Description})
			}
		}
		for _, m := range []string{"quickened", "twinned", "careful", "subtle", "extended", "empowered", "distant", "heightened", "seeking", "transmuted"} {
			if !containsStr(known, m) {
				steps = append(steps, setupStep{Key: "metamagic", Title: "Choose a Metamagic option", Why: "Metamagic. " + classFeatureText("sorcerer", "Metamagic") + " Send one option per request.",
					Request: req("POST", "/api/characters/metamagic", map[string]interface{}{"character_id": charID, "metamagic": m}),
					Choices: map[string]interface{}{"you_know": known, "options": mmOpts}})
				break
			}
		}
	}
	if has("Wild Shape") {
		if forms := suggestWildShapeForms(charID); len(forms) > 0 {
			kn, _, _ := wildShapeLimits(level)
			steps = append(steps, setupStep{Key: "wild_shape_forms", Title: "Choose your Wild Shape forms", Why: "Wild Shape. " + classFeatureText("druid", "Wild Shape") +
				fmt.Sprintf(" You know %d forms now and can know %d: send all of them (the ones you have and the new ones).", len(knownWildShapes(charID)), kn),
				Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]interface{}{"wild_shape_forms": forms}),
				Choices: map[string]interface{}{"you_know": knownWildShapes(charID), "know_in_all": kn, "options": wildShapeOptions(level)}})
		}
	}
	if has("Circle of the Land") {
		landText := ""
		for _, f := range game.GetActiveSubclassFeatures("land", 3) {
			if f.Name == "Circle of the Land Spells" || f.Name == "Circle Spells" {
				landText = f.Description
			}
		}
		landSpells := map[string]interface{}{}
		for _, t := range []string{"arid", "polar", "temperate", "tropical"} {
			var names []string
			for _, sl := range getDomainSpells("land", level, t) {
				if sp, ok := srdSpellsMemory[sl]; ok {
					names = append(names, sp.Name)
				}
			}
			landSpells[t] = names
		}
		steps = append(steps, setupStep{Key: "land_type", Title: "Choose your Circle of the Land type", Choices: map[string]interface{}{"options": []string{"arid", "polar", "temperate", "tropical"}, "circle_spells_at_your_level": landSpells},
			Why: landText + " Choose arid, polar, temperate or tropical; you can choose again after a Long Rest.",
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]string{"land_type": "temperate"})})
	}
	if has("Hunter's Prey") {
		steps = append(steps, setupStep{Key: "hunters_prey", Title: "Choose your Hunter's Prey", Why: "Hunter’s Prey. " + subclassFeatureText("hunter", "Hunter’s Prey"),
			Choices: map[string]interface{}{"options": []string{"colossus_slayer", "horde_breaker"}},
			Request: req("POST", "/api/characters/subclass-choice", map[string]interface{}{"character_id": charID, "feature": "hunters_prey", "choice": "colossus_slayer"})})
	}
	if has("Defensive Tactics") {
		steps = append(steps, setupStep{Key: "defensive_tactics", Title: "Choose your Defensive Tactics", Why: "Defensive Tactics. " + subclassFeatureText("hunter", "Defensive Tactics"),
			Choices: map[string]interface{}{"options": []string{"escape_the_horde", "multiattack_defense"}},
			Request: req("POST", "/api/characters/subclass-choice", map[string]interface{}{"character_id": charID, "feature": "defensive_tactics", "choice": "multiattack_defense"})})
	}
	if has("Primal Knowledge") {
		pick := ""
		for _, s := range []string{"perception", "athletics", "survival", "intimidation", "nature", "animal handling"} {
			if pick == "" && !held[s] {
				pick = s
			}
		}
		steps = append(steps, setupStep{Key: "primal_knowledge", Title: "Choose your Primal Knowledge skill", Why: classFeatureText("barbarian", "Primal Knowledge"),
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]string{"primal_knowledge": pick}),
			Choices: map[string]interface{}{"options": skillChoices([]string{"animal handling", "athletics", "intimidation", "nature", "perception", "survival"}, held)}})
	}
	if has("Bonus Proficiencies") {
		var picks []string
		for _, s := range []string{"perception", "stealth", "arcana", "history", "investigation", "insight", "persuasion", "deception", "athletics", "acrobatics"} {
			if len(picks) < 3 && !held[s] {
				picks = append(picks, s)
			}
		}
		bp := ""
		for _, f := range game.GetActiveSubclassFeatures("lore", 3) {
			if f.Name == "Bonus Proficiencies" {
				bp = f.Description
			}
		}
		steps = append(steps, setupStep{Key: "bonus_proficiencies", Title: "Choose your three Bonus Proficiencies", Why: "College of Lore, level 3: Bonus Proficiencies. " + bp,
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]interface{}{"bonus_proficiencies": picks}),
			Choices: map[string]interface{}{"choose": 3, "options": skillChoices(skillOrder, held)}})
	}
	if has("Ability Score Improvement") {
		if st := asiSetupStep(charID); st != nil {
			steps = append(steps, *st)
		}
	}
	steps = append(steps, pendingClassChoices(charID)...) // Blessed Strikes, Elemental Fury, Elemental Affinity
	if st := mysticArcanumStep(charID); st != nil {
		steps = append(steps, *st)
	}
	steps = append(steps, wizardCapstoneSteps(charID)...) // Spell Mastery, Signature Spells
	if st := classLanguageStep(charID); st != nil {
		steps = append(steps, *st)
	}
	if has("Magical Discoveries") {
		if st := magicalDiscoveriesStep(charID); st != nil {
			steps = append(steps, *st)
		}
	}
	if has("Weapon Mastery") {
		if st := masterySetupStep(charID, class, level); st != nil {
			steps = append(steps, *st)
		}
	}
	for _, t := range todo { // anything else the list names
		known := false
		for _, w := range []string{"Spell Mastery", "Signature Spells", "Mystic Arcanum", "Ability Score Improvement", "Weapon Mastery", "Magical Discoveries", "language(s)", "Defensive Tactics", "Choose your Blessed Strikes", "Choose your Elemental Fury", "Choose your Elemental Affinity", "Divine Order", "Primal Order", "Fighting Style", "Eldritch Invocations", "Pact of the Tome", "Expertise", "cantrip", "Spells:", "subclass", "Metamagic", "Wild Shape", "Circle of the Land", "Hunter's Prey", "Primal Knowledge", "Bonus Proficiencies", "Equipment: do you want"} {
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
		always := allAlwaysPrepared(charID) // subclass, origin and class-feature spells, the Book of Shadows: not counted (F11)
		// the prepared list as /prepare and to_finish read it (F3: a Sorcerer's known spells counted here as prepared, so this step
		// said done while to_finish said none were prepared)
		prepList := jsonList(prepared)
		if len(classLevelsOf(charID)) <= 1 {
			prepList = currentPreparedList(charID, cl)
		}
		var cantrips, leveled []string
		seen := map[string]bool{}
		for _, s := range append(jsonList(known), jsonList(prepared)...) {
			if seen[s] {
				continue
			}
			seen[s] = true
			if _, o := origin[s]; o || containsStr(always, s) {
				continue
			}
			if sp, ok := srdSpellsMemory[s]; ok {
				if sp.Level == 0 {
					cantrips = append(cantrips, s)
				} else if containsStr(prepList, s) {
					leveled = append(leveled, s)
				}
			}
		}
		// a list /prepare would refuse (too many, off the class list, too high) keeps the step open, and the request fixes it
		listProblems := preparedListProblems(charID)
		if len(listProblems) > 0 {
			maxSlot := maxSpellLevelFor(charID, cl, level)
			valid := func(cap int, list []string) []string {
				var out []string
				for _, x := range list {
					if sp, ok := srdSpellsMemory[x]; ok && len(out) < cap && onClassList(x, cl) && sp.Level <= maxSlot {
						out = append(out, x)
					}
				}
				return out
			}
			cMax := game.ClassTableInt(cl, level, "Cantrips") + orderExtraCantrips(charID)
			cantrips = valid(cMax, cantrips)
			leveled = valid(maxPrep, leveled)
			if need := cMax - len(cantrips); need > wantCantrips {
				wantCantrips = need
			}
		}
		avoid := map[string]bool{}
		for s := range seen {
			if sp, ok := srdSpellsMemory[s]; ok && cl == "wizard" && sp.Level > 0 && !containsStr(jsonList(prepared), s) {
				continue // a Wizard's spellbook spell not yet prepared is what can be prepared (Nov 13)
			}
			avoid[s] = true
		}
		for s := range origin {
			avoid[s] = true
		}
		for _, s := range always {
			avoid[s] = true
		}
		_, subSlug, _ := subclassOf(charID)
		for _, s := range getDomainSpells(subSlug, level, subclassChoicesOf(charID)["circle_land"]) { // the subclass's spells are always prepared
			avoid[s] = true
		}
		avoidBase := map[string]bool{}
		for k, v := range avoid {
			avoidBase[k] = v
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
		spellsDone := wantCantrips == 0 && len(leveled) >= maxPrep && len(listProblems) == 0
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
				if len(leveled)+len(pick) < maxPrep && !avoid[s] && onClassList(s, cl) && (cl != "wizard" || inSpellbookStrict(charID, s)) { // a Wizard prepares only from the spellbook (Nov 13: the step suggested Burning Hands, not in the book, and every try failed)
					if sp, ok := srdSpellsMemory[s]; ok && sp.Level >= 1 && sp.Level <= maxLvl {
						pick = append(pick, s)
						avoid[s] = true
					}
				}
			}
		}
		st := setupStep{Key: "spells", Title: "Choose your cantrips and prepare your spells", Done: spellsDone,
			Why: fmt.Sprintf("You know %d cantrips (%d more to choose) and prepare %d level 1+ spells (%d prepared now). Cantrips and spells must be on your class's list: GET /api/universe/class-spells/%s. Origin spells (Magic Initiate, lineage) are extra.", len(cantrips)+wantCantrips, wantCantrips, maxPrep, len(leveled), cl)}
		if len(listProblems) > 0 {
			st.Problem = "Your prepared list can't stay as it is: " + strings.Join(listProblems, "; ") + ". The request sends a list that fits."
		}
		if !spellsDone {
			all := append(append(append([]string{}, cantrips...), newC...), leveled...)
			all = append(all, pick...)
			suggestedSet := map[string]bool{}
			for _, x := range all {
				suggestedSet[x] = true
			}
			classList := getClassSpellList(cl)
			if cl == "bard" && level >= 10 { // Magical Secrets: the Cleric, Druid and Wizard lists count as Bard spells (SRD 5.2.1)
				seenL := map[string]bool{}
				for _, x := range classList {
					seenL[x] = true
				}
				for _, other := range []string{"cleric", "druid", "wizard"} {
					for _, x := range getClassSpellList(other) {
						if !seenL[x] {
							seenL[x] = true
							classList = append(classList, x)
						}
					}
				}
				st.Why += " Magical Secrets: " + classFeatureText("bard", "Magical Secrets") + " The options below include those lists."
			}
			var originList []string
			for o := range origin {
				originList = append(originList, o)
			}
			sort.Strings(originList)
			have := map[string]interface{}{"cantrips": cantrips, "prepared_level_1_plus": leveled, "always_prepared_extra (Magic Initiate, lineage)": originList}
			if fa := featureAlwaysPreparedInfo(charID); len(fa) > 0 {
				have["always_prepared_by_class_features"] = fa // Druidic's Speak with Animals, Favored Enemy's Hunter's Mark ... (F11)
			}
			st.Choices = map[string]interface{}{
				"how_to_read_this": "Each option says what the spell does, how it is cast and whether it needs concentration. suggested: true marks the picks in the request body, which are the server's suggestion. Send your cantrips and spells together in one list: POST /api/characters/N/prepare {\"spells\": [...]}.",
				"already_have":     have,
				"cantrips":         map[string]interface{}{"choose_this_many_more": wantCantrips, "options": spellOptions(classList, 0, 0, avoidBase, suggestedSet)},
				"level_1_plus_spells": map[string]interface{}{"prepare_up_to": maxPrep, "already_prepared": len(leveled),
					"highest_spell_level_you_can_cast": maxLvl, "you_can_change_prepared_spells": "after a Long Rest",
					"options": spellOptions(classList, 1, maxLvl, avoidBase, suggestedSet)},
			}
			if cl == "wizard" {
				// origin spells (Magic Initiate) and copied spells aren't among the free spellbook spells (F14)
				book := freeSpellbookCount(charID, jsonList(known))
				var add []string
				target := wizardSpellbookTarget(charID, level) // 6 spells at level 1, two more each level (SRD 5.2.1 Spellbook)
				// the whole Wizard list after the preferred spells, highest level first (Nov 13: the preferred lists ran out at level 7)
				var fullList []string
				for lv := maxLvl; lv >= 1; lv-- {
					for _, k := range getClassSpellList("wizard") {
						if sp, ok := srdSpellsMemory[k]; ok && sp.Level == lv {
							fullList = append(fullList, k)
						}
					}
				}
				if book < target {
					for _, list := range [][]string{spell2Prefs["wizard"], spellPrefs["wizard"], fullList} {
						for _, s := range list {
							if sp, ok := srdSpellsMemory[s]; ok && len(add) < target-book && sp.Level >= 1 && sp.Level <= maxLvl && onClassList(s, "wizard") && !containsStr(jsonList(known), s) && !containsStr(add, s) {
								add = append(add, s)
							}
						}
					}
				}
				add = append(add, newC...)
				st.Why += " A Wizard writes spells in the spellbook (6 at level 1, two more each level: the first request), then prepares from it (the second)."
				if st.Choices != nil {
					st.Choices["wizard_spellbook"] = map[string]interface{}{"spells_in_your_spellbook_now": book, "spellbook_should_hold": target, "write_this_many_more": target - book,
						"note": "A Wizard first writes spells into the spellbook (PUT /api/characters/N/spells {\"add\": [...]}), then prepares spells from the book (POST /api/characters/N/prepare). Choose the spellbook spells from level_1_plus_spells.options."}
				}
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
			if shield && game.ClassTableInt(cl, level, "Cantrips") > 0 {
				// a spellcaster with a Shield: a weapon in the other hand would leave no free hand for spells (Nov 13); not pushed, the auto-equip question asks
				steps = append(steps, setupStep{Key: "weapon_in_hand", Title: "Hold a weapon", Done: true,
					Why: "You hold a Shield and cast spells, so your other hand is left free for spells with gestures (S). To fight with a weapon, draw it: POST /api/characters/equip-weapon {\"character_id\": " + fmt.Sprint(charID) + ", \"weapon\": \"" + strings.ToLower(weapon) + "\", \"slot\": \"main_hand\"} (one weapon change a turn)."})
			} else {
				steps = append(steps, setupStep{Key: "weapon_in_hand", Title: "Hold a weapon", Why: why,
					Request: req("POST", "/api/characters/equip-weapon", map[string]interface{}{"character_id": charID, "weapon": strings.ToLower(weapon), "slot": "main_hand"})})
			}
		}
	} else {
		steps = append(steps, setupStep{Key: "weapon_in_hand", Title: "Hold a weapon", Done: true, Why: "In your main hand: " + mainHand})
	}
	// equip everything: the player is asked once (auto_equip_2024.go)
	if line := autoEquipOpen(charID); line != "" {
		steps = append(steps, setupStep{Key: "auto_equip", Title: "Put on your gear and arm your weapons",
			Why:     "Do you want me to equip everything for you? I will put on your armor, take your weapon (and a Shield or second Light weapon) in hand and wear your clothing and jewelry. If you have a choice to make (more weapons than hands, two items for the same body place) I will ask you which. GET /api/characters/auto-equip?character_id=N shows what I would do first.",
			Request: req("POST", "/api/characters/auto-equip", autoEquipSuggestion(charID))})
	}
	// the starting shop, while the character has money (shop_2024.go)
	if st := shopSetupStep(charID); st != nil {
		steps = append(steps, *st)
	}
	// a portrait for the character sheet (portrait_step_2024.go)
	steps = append(steps, portraitSetupStep(charID))
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
		steps = append(steps, setupStep{Key: "gear", Title: title, Why: why, Optional: true, Request: map[string]interface{}{"how": g["how"], "mute": g["mute"]}})
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
	// never "ready to play" while to_finish still has an item (F3): when no required step is open, each item is listed as a step
	requiredOpen := false
	for _, st := range steps {
		if !st.Done && !st.Optional {
			requiredOpen = true
		}
	}
	if !requiredOpen {
		for _, t := range todo {
			steps = append(steps, setupStep{Key: "other", Title: t})
		}
	}
	left, optLeft := 0, 0
	var next, nextOpt *setupStep
	var optNames []string
	for i := range steps {
		if steps[i].Done {
			continue
		}
		if steps[i].Optional {
			optLeft++
			optNames = append(optNames, steps[i].Key)
			if nextOpt == nil {
				nextOpt = &steps[i]
			}
			continue
		}
		left++
		if next == nil {
			next = &steps[i]
		}
	}
	// finished and steps_left agree: optional steps (shop, portrait, gear reminders) don't count (Nov 14 tester: "finished" while
	// steps were still listed)
	out := map[string]interface{}{"character_id": charID, "character_finished": len(todo) == 0 && left == 0, "steps": steps, "steps_left": left,
		"optional_steps_left": optLeft}
	if next == nil && nextOpt != nil {
		out["optional"] = fmt.Sprintf("Your character is finished. Optional, when you like: %s.", strings.Join(optNames, ", "))
	}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	if next != nil {
		out["next"] = next
		out["how"] = "Send each step's request (the suggested choices are filled in; change them as you like), then GET this page again until steps_left is 0."
	} else {
		out["how"] = "Your character is ready to play."
		if nextOpt != nil {
			out["next_optional"] = nextOpt
		}
	}
	writeJSONStatus(w, 200, out)
}

// magicInitiateProblem: what is wrong with Magic Initiate picks, said at the step (Nov 13).
func magicInitiateProblem(fc map[string]interface{}, list string) string {
	if fc == nil {
		return ""
	}
	if a := strings.ToLower(bstr(fc, "spellcasting_ability")); a != "" && a != "int" && a != "wis" && a != "cha" {
		return fmt.Sprintf("%q can't be the spellcasting ability: choose int, wis or cha", a)
	}
	cs := blist(fc, "cantrips")
	for _, c := range cs {
		slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(c), " ", "-"))
		if sp, ok := srdSpellsMemory[slug]; ok && sp.Level != 0 {
			return fmt.Sprintf("%s is not a cantrip", sp.Name)
		}
		if !onClassList(slug, list) {
			return fmt.Sprintf("%s is not a cantrip on the %s list", titleWords(strings.ReplaceAll(slug, "-", " ")), list)
		}
	}
	if len(cs) > 0 && len(cs) != 2 {
		return fmt.Sprintf("choose exactly 2 cantrips (you chose %d)", len(cs))
	}
	if len(cs) == 2 && builderSpellSlug(cs[0]) == builderSpellSlug(cs[1]) { // "Sacred Flame" and "sacred-flame" are the same cantrip (Nov 14 audit)
		return "you chose the same cantrip twice: choose 2 different ones"
	}
	if sp := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(bstr(fc, "spell")), " ", "-")); sp != "" {
		if s, ok := srdSpellsMemory[sp]; ok && s.Level != 1 {
			return fmt.Sprintf("%s is not a level 1 spell", s.Name)
		}
		if !onClassList(sp, list) {
			return fmt.Sprintf("%s is not on the %s spell list", titleWords(strings.ReplaceAll(sp, "-", " ")), list)
		}
	}
	return ""
}

// draftSpeciesCantrips: the cantrips the draft's species lineage or legacy gives (High Elf: Prestidigitation ...).
// It reads the key the species itself uses (an Elf's lineage, a Tiefling's legacy), so an answer left from an earlier species
// can't hide the real cantrip (Nov 14 tester: a Wood Elf lineage left on a Tiefling hid the Infernal legacy's Fire Bolt).
func draftSpeciesCantrips(body map[string]interface{}) []string {
	sp := game.GetSpecies(bstr(body, "species"))
	if sp == nil {
		return nil
	}
	sc := bmap(body, "species_choices")
	key, alias := "", ""
	if containsStr(sp.Needs, "lineage") {
		key, alias = "lineage", "legacy"
	} else if containsStr(sp.Needs, "legacy") {
		key, alias = "legacy", "lineage" // a Tiefling's "lineage" is its legacy
	}
	choice := ""
	if key != "" {
		choice = optionKey(bstr(sc, key))
		if choice == "" {
			choice = optionKey(bstr(sc, alias))
		}
	}
	return game.SpeciesCantrips(sp.Key, choice)
}

// alreadyKnownProblem: a Magic Initiate cantrip the species already gives, or a spell the other Magic Initiate already gives
// (Nov 13: a High Elf Sage could take Prestidigitation twice).
func alreadyKnownProblem(fc map[string]interface{}, speciesCantrips, otherFeat []string) string {
	norm := func(x string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(x), " ", "-")) }
	for _, c := range blist(fc, "cantrips") {
		if containsStr(speciesCantrips, norm(c)) {
			return fmt.Sprintf("Your species already gives you %s: choose a different cantrip.", originSpellName(norm(c)))
		}
	}
	for _, c := range append(blist(fc, "cantrips"), bstr(fc, "spell")) {
		if c != "" && containsStr(otherFeat, norm(c)) {
			return fmt.Sprintf("Your background's Magic Initiate already gives you %s: choose a different spell.", originSpellName(norm(c)))
		}
	}
	return ""
}

// magicInitiateOptions: what Magic Initiate offers from one list, as lists (Nov 13: the options were a URL string, which a
// client showed letter by letter). Spells the character already gets are left out and named.
func magicInitiateOptions(list string, speciesCantrips, otherFeat []string) map[string]interface{} {
	var cantrips, spells, have []string
	for _, slug := range classSpellListFn(list) {
		sp, ok := srdSpellsMemory[slug]
		if !ok || sp.Level > 1 {
			continue
		}
		if containsStr(speciesCantrips, slug) || containsStr(otherFeat, slug) {
			have = append(have, slug)
			continue
		}
		if sp.Level == 0 {
			cantrips = append(cantrips, slug)
		} else {
			spells = append(spells, slug)
		}
	}
	sort.Strings(cantrips)
	sort.Strings(spells)
	ex := map[string]interface{}{"list": list, "spell": "", "spellcasting_ability": "int"}
	if len(cantrips) >= 2 {
		ex["cantrips"] = cantrips[:2]
	}
	if len(spells) > 0 {
		ex["spell"] = spells[0]
	}
	out := map[string]interface{}{ // keys named as the answer's fields (Nov 14 tester sent the old option names back)
		"list": list, "cantrips": cantrips, "spell": spells, "spellcasting_ability": []string{"int", "wis", "cha"},
		"how_to_answer": map[string]interface{}{"choose": "2 cantrips, 1 level 1 spell and the ability", "example": ex},
		"spell_details": "GET /api/universe/class-spells/" + list,
	}
	if len(have) > 0 {
		out["left_out_you_already_have"] = have
	}
	return out
}

// humanMagicInitiateOptions: the Human's Magic Initiate: each list it can use (not the background's), with its spells.
func humanMagicInitiateOptions(bgList string, bgSpells []string) map[string]interface{} {
	out := map[string]interface{}{}
	example := "" // a list this player can choose (Nov 14 audit: the example named the Acolyte's own cleric list)
	for _, l := range []string{"cleric", "druid", "wizard"} {
		if l != bgList {
			out[l] = magicInitiateOptions(l, nil, bgSpells)
			if example == "" {
				example = l
			}
		}
	}
	return map[string]interface{}{"list": out, "how_to_answer": "choose one list, then from it 2 cantrips, 1 spell and the ability: {\"list\": \"" + example + "\", \"cantrips\": [...], \"spell\": \"...\", \"spellcasting_ability\": \"wis\"}"}
}
