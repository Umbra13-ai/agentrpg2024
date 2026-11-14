package main

// Builder checks from an AI player's test of the live server (Nov 13): an unrecognized field was saved silently (a typo looked like a
// successful choice), invalid answers stayed in the draft while flagged, the review step had no summary of the character, and the
// one-character-per-campaign rule came as a surprise after creation.
//
// The input layer (Nov 14 audit): every answer is either saved as sent (an obvious spelling is put in the server's form first,
// "Sleight-of-Hand" -> "sleight of hand") or refused with the reason. A value of the wrong JSON type, a control character, a part
// the builder doesn't know and a field the builder has no place for are refused; they are never stored and left unread.

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/agentrpg/agentrpg/game"
)

// paragraphSplit: a blank line (also written with \r\n) between two paragraphs.
var paragraphSplit = regexp.MustCompile(`\r?\n[ \t\r]*\n`)

// builderFields: every field the builder reads or the creation request takes.
var builderFields = map[string]bool{
	"name": true, "class": true, "background": true, "species": true, "alignment": true, "backstory": true, "appearance": true, "backstory_skip": true,
	"str": true, "dex": true, "con": true, "int": true, "wis": true, "cha": true, "ability_method": true, "ability_increases": true,
	"skill_proficiencies": true, "tool_proficiencies": true, "expertise": true, "weapon_masteries": true, "class_options": true,
	"starting_equipment": true, "background_tool": true, "background_equipment": true, "background_feat_choices": true,
	"species_choices": true, "species_feat_choices": true, "extra_languages": true, "known_spells": true, "draconic_ancestry": true,
	"divine_order": true, "primal_order": true, "use_suggestion": true,
}

// closestField: the known field nearest to a typo (edit distance), or "".
func closestField(s string) string {
	return closestOf(s, func(yield func(string)) {
		for f := range builderFields {
			yield(f)
		}
	})
}

// closestOf: the name nearest to a typo (edit distance under 4), or "".
func closestOf(s string, names func(func(string))) string {
	best, bestD := "", 4
	names(func(f string) {
		if d := editDistance(strings.ToLower(s), f); d < bestD || d == bestD && f < best {
			best, bestD = f, d
		}
	})
	return best
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// unknownBuilderFields: the fields sent that the builder doesn't know, each with a hint. It also checks the parts of the groups
// (background_feat_choices, species_feat_choices, class_options, species_choices).
func unknownBuilderFields(upd map[string]interface{}) []string {
	var out []string
	out = append(out, featChoiceSubfields(upd)...)
	out = append(out, groupSubfields(upd)...)
	for k := range upd {
		if !builderFields[k] {
			msg := fmt.Sprintf("%q is not a field the builder knows", k)
			if nk := normKey(k); nk == "spells" || nk == "cantrips" || nk == "spell" || nk == "prepared_spells" { // not "did you mean species" (re-check)
				msg += ": spells and cantrips are chosen after creation, in GET /api/characters/{id}/setup"
			} else if h, ok := createFieldHints[nk]; ok && nk != "use_suggestion" && nk != "spellcasting_ability" { // the same true hints as POST /api/characters (round 2)
				msg += ": " + h
			} else if c := closestField(k); c != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", c)
			}
			out = append(out, msg)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- the input layer

// builderInput: what the input layer made of a request: fields or parts the builder doesn't know (nothing is saved), answers
// refused before they reach the draft, and notes on answers read under another name.
type builderInput struct {
	unknown []string
	refused []map[string]interface{}
	notes   []string
}

func (in *builderInput) refuse(field string, sent interface{}, problem string) {
	in.refused = append(in.refused, map[string]interface{}{"field": field, "you_sent": sent, "problem": problem})
}

// builderAliases: top-level fields that belong inside a group (the creation request's old names). They are read there.
var builderAliases = map[string][2]string{
	"divine_order": {"class_options", "divine_order"}, "primal_order": {"class_options", "primal_order"},
	"draconic_ancestry": {"species_choices", "ancestry"},
}

// speciesChoiceKeys: the parts species_choices can have (each species asks for some of them).
var speciesChoiceKeys = []string{"lineage", "ancestry", "legacy", "spellcasting_ability", "size", "skill", "feat"}

var speciesChoiceExample = map[string]string{"lineage": "high_elf", "ancestry": "red", "legacy": "infernal", "spellcasting_ability": "int",
	"size": "medium", "skill": "perception", "feat": "skilled"}

// classOrders: the class_options parts, the class that has each, and its options.
var classOrders = map[string]struct {
	class string
	opts  []string
}{"divine_order": {"cleric", []string{"protector", "thaumaturge"}}, "primal_order": {"druid", []string{"magician", "warden"}}}

var stringExamples = map[string]string{"name": "Thorin", "class": "fighter", "background": "soldier", "species": "dwarf",
	"alignment": "Neutral Good", "ability_method": "standard_array", "starting_equipment": "a", "background_tool": "dice set",
	"background_equipment": "a"}

var listExamples = map[string]string{"skill_proficiencies": `["perception", "athletics"]`, "tool_proficiencies": `["lute", "flute", "drum"]`,
	"expertise": `["stealth", "perception"]`, "weapon_masteries": `["longsword", "shortbow"]`, "extra_languages": `["Elvish", "Dwarvish"]`}

// maxNameRunes: the longest character name the server stores.
const maxNameRunes = 255

// normKey: "Spellcasting Ability" and "spellcasting-ability" are spellcasting_ability.
func normKey(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(s)), func(r rune) bool { return r == ' ' || r == '-' || r == '_' }), "_")
}

// skillForm: a skill in the server's form ("Sleight_of_Hand" -> "sleight of hand"); anything else as sent.
func skillForm(s string) string {
	k := strings.Join(strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(s))), " ")
	if game.IsSkill(k) {
		return k
	}
	return s
}

// badText: why a text can't be saved ("" when it can): a control character (a line break or a tab is fine in a backstory).
func badText(s string, multiline bool) string {
	for _, r := range s {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return fmt.Sprintf("it has a control character (%U) in it: remove it", r)
		}
	}
	return ""
}

// wholeNumber: a JSON number with no fraction, or digits in a text ("15").
func wholeNumber(v interface{}) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e6 {
			return int(x), true
		}
	case int:
		return x, true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// jsonText: a value as JSON, for a message.
func jsonText(v interface{}) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// textList: a list of names, each trimmed; or why it isn't one.
func textList(field string, v interface{}, example string) ([]interface{}, string) {
	l, ok := v.([]interface{})
	if !ok {
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) != "" { // the same names as a list
			var parts []string
			for _, p := range strings.Split(s, ",") {
				if p = strings.TrimSpace(p); p != "" {
					parts = append(parts, strconv.Quote(p))
				}
			}
			example = "[" + strings.Join(parts, ", ") + "]"
		}
		return nil, fmt.Sprintf("send a list of names, like %s", example)
	}
	out := make([]interface{}, 0, len(l))
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return nil, fmt.Sprintf("send a list of names: %s is not a name", jsonText(x))
		}
		if s = strings.TrimSpace(s); s == "" {
			return nil, "there is an empty name in the list"
		}
		if why := badText(s, false); why != "" {
			return nil, fmt.Sprintf("%q: %s", s, why)
		}
		out = append(out, s)
	}
	return out, ""
}

// checkBuilderInput checks the fields of one request before anything is saved: names, JSON types, control characters. It puts
// obvious spellings in the server's form, reads the old top-level names (divine_order ...) in their groups, and takes the refused
// answers out of upd. body is the draft as it is (for the class and the species the parts depend on).
func checkBuilderInput(upd, body map[string]interface{}) builderInput {
	var in builderInput
	class, species := bstr(body, "class"), bstr(body, "species")
	if s, ok := upd["class"].(string); ok {
		class = s
	}
	if s, ok := upd["species"].(string); ok {
		species = s
	}
	class = strings.ToLower(strings.TrimSpace(class))
	sp := game.GetSpecies(strings.ToLower(strings.TrimSpace(species)))

	// the parts of class_options and species_choices under their real names ("Legacy" is legacy, "ability" spellcasting_ability)
	for _, g := range []string{"class_options", "species_choices"} {
		m, ok := upd[g].(map[string]interface{})
		if !ok {
			continue
		}
		out := map[string]interface{}{}
		for k, v := range m {
			nk := normKey(k)
			if g == "species_choices" {
				switch nk {
				case "ability", "spell_ability", "spellcasting":
					nk = "spellcasting_ability"
				case "subrace":
					nk = "lineage"
				}
				if nk == "lineage" && sp != nil && sp.Key == "tiefling" { // creation reads a Tiefling's lineage as the legacy
					// but {"lineage": null} removes a stale lineage left from another species (an Elf's), not the legacy chosen
					// (Nov 14 audit re-check, S4-02/M5: the legacy was deleted and the stale lineage stayed)
					if _, stale := bmap(body, "species_choices")["lineage"]; !(v == nil && stale) {
						nk = "legacy"
					}
				}
				if nk != normKey(k) && nk != strings.ToLower(strings.TrimSpace(k)) { // said, not done silently (re-check)
					in.notes = append(in.notes, fmt.Sprintf("species_choices.%s is read as species_choices.%s", k, nk))
				}
			}
			if old, dup := out[nk]; dup && !reflect.DeepEqual(old, v) {
				in.refuse(g+"."+nk, v, fmt.Sprintf("sent twice with different values (%s and %s): send it once", jsonText(old), jsonText(v)))
				delete(out, nk)
				continue
			}
			out[nk] = v
		}
		upd[g] = out
	}
	// divine_order, primal_order and draconic_ancestry at the top level are read in their groups
	for _, alias := range []string{"divine_order", "primal_order", "draconic_ancestry"} {
		v, sent := upd[alias]
		if !sent {
			continue
		}
		if alias == "draconic_ancestry" && sp != nil && sp.Key != "dragonborn" { // a Goliath's giant ancestry is not a draconic one (re-check)
			in.refuse(alias, v, fmt.Sprintf("only a Dragonborn has a draconic ancestry, not %s %s", aOrAn(sp.Name), sp.Name))
			delete(upd, alias)
			continue
		}
		to := builderAliases[alias]
		delete(upd, alias)
		delete(body, alias) // an old copy at the top level is never read
		g, has := upd[to[0]]
		if has && g == nil {
			in.refuse(alias, v, fmt.Sprintf("%s was sent as null in the same request: send %s.%s alone", to[0], to[0], to[1]))
			continue
		}
		gm, ok := g.(map[string]interface{})
		if has && !ok {
			continue // the group itself is refused below
		}
		if gm == nil {
			gm = map[string]interface{}{}
			upd[to[0]] = gm
		}
		if old, both := gm[to[1]]; both && !reflect.DeepEqual(old, v) {
			in.refuse(alias, v, fmt.Sprintf("%s.%s is also sent, with %s: send it once", to[0], to[1], jsonText(old)))
			continue
		}
		gm[to[1]] = v
		in.notes = append(in.notes, fmt.Sprintf("%s is read as %s.%s", alias, to[0], to[1]))
	}

	if in.unknown = unknownBuilderFields(upd); len(in.unknown) > 0 {
		return in
	}

	for k, v := range upd {
		if v == nil || k == "use_suggestion" { // null removes the field
			continue
		}
		switch k {
		case "known_spells":
			in.refuse(k, v, "spells are chosen after creation, in setup (GET /api/characters/{id}/setup), not in the builder")
			delete(upd, k)
		case "name", "class", "background", "species", "alignment", "ability_method", "starting_equipment", "background_tool", "background_equipment":
			s, ok := v.(string)
			if !ok {
				in.refuse(k, v, fmt.Sprintf("send text, like %q", stringExamples[k]))
				delete(upd, k)
				continue
			}
			s = strings.TrimSpace(s)
			if why := badText(s, false); why != "" {
				in.refuse(k, v, why)
				delete(upd, k)
				continue
			}
			switch k {
			case "name":
				if why := badName(s); why != "" {
					in.refuse(k, v, why)
					delete(upd, k)
					continue
				}
			case "class", "starting_equipment", "background_equipment":
				s = strings.ToLower(s)
			case "alignment":
				if c := canonicalPlayerAlignment(s); c != "" {
					s = c // "True Neutral" is saved as "Neutral"
				}
			case "ability_method":
				switch normKey(s) {
				case "standard_array", "standard", "array", "standardarray":
					s = "standard_array"
				}
			}
			upd[k] = s
		case "backstory", "appearance":
			s, ok := v.(string)
			if !ok {
				in.refuse(k, v, map[string]string{"backstory": "send text (separate paragraphs with a blank line)", "appearance": "send text: a few sentences"}[k])
				delete(upd, k)
				continue
			}
			s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
			if why := badText(s, true); why != "" {
				in.refuse(k, v, why)
				delete(upd, k)
				continue
			}
			upd[k] = s
		case "backstory_skip":
			// one JSON type per field, as documented and as POST /api/characters takes it: "true" (text) is refused, not read as
			// true without a word (Nov 14 audit re-check, F4)
			b, ok := v.(bool)
			if !ok {
				in.refuse(k, v, "send true or false (JSON true/false, not text)")
				delete(upd, k)
				continue
			}
			upd[k] = b
		case "str", "dex", "con", "int", "wis", "cha":
			if t, isStr := v.(string); isStr { // a number sent as text is refused, as POST /api/characters does (re-check F10)
				in.refuse(k, v, fmt.Sprintf("send the score as a number (%s), not as text (%q)", strings.TrimSpace(t), t))
				delete(upd, k)
				continue
			}
			n, ok := wholeNumber(v)
			if !ok {
				in.refuse(k, v, "send a whole number: the Standard Array is 15, 14, 13, 12, 10 and 8, once each")
				delete(upd, k)
				continue
			}
			upd[k] = float64(n)
		case "ability_increases":
			if inc, why := abilityIncreasesInput(v); why != "" {
				in.refuse(k, v, why)
				delete(upd, k)
			} else {
				upd[k] = inc
			}
		case "skill_proficiencies", "tool_proficiencies", "expertise", "weapon_masteries", "extra_languages":
			l, why := textList(k, v, listExamples[k])
			if why != "" {
				in.refuse(k, v, why)
				delete(upd, k)
				continue
			}
			if k == "skill_proficiencies" || k == "expertise" {
				for i, x := range l {
					l[i] = skillForm(x.(string))
				}
			}
			if k == "tool_proficiencies" { // "Smiths Tools" is saved as "smith's tools"
				canon := map[string]string{}
				for _, o := range game.ToolChoicesFor("monk", nil)[0].Options { // every Artisan's Tool and Musical Instrument
					canon[strings.NewReplacer("'", "", "’", "").Replace(o)] = o
				}
				for i, x := range l {
					if c, ok := canon[strings.NewReplacer("'", "", "’", "").Replace(game.NormalizeToolName(x.(string)))]; ok {
						l[i] = c
					}
				}
			}
			upd[k] = l
		case "class_options":
			m, ok := v.(map[string]interface{})
			if !ok {
				in.refuse(k, v, `send an object, like {"divine_order": "protector"} (Cleric) or {"primal_order": "warden"} (Druid)`)
				delete(upd, k)
				continue
			}
			for part, pv := range m {
				if pv == nil {
					continue
				}
				o := classOrders[part]
				s, ok := pv.(string)
				if !ok {
					in.refuse(k+"."+part, pv, fmt.Sprintf("send one word: %s", strings.Join(o.opts, " or ")))
					delete(m, part)
					continue
				}
				s = strings.ToLower(strings.TrimSpace(s))
				if why := badText(s, false); why != "" {
					in.refuse(k+"."+part, pv, why)
					delete(m, part)
					continue
				}
				if class != o.class {
					msg := fmt.Sprintf("only %s %s chooses a %s", aOrAn(o.class), titleWords(o.class), part)
					if _, ok := srdClasses[class]; ok {
						msg += fmt.Sprintf("; your class is %s", titleWords(class))
					} else {
						msg = "choose your class first: " + msg
					}
					in.refuse(k+"."+part, pv, msg)
					delete(m, part)
					continue
				}
				var named []string // "thaumaturge (one extra cantrip)" is thaumaturge; naming both orders is refused
				for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) {
					if containsStr(o.opts, w) && !containsStr(named, w) {
						named = append(named, w)
					}
				}
				if len(named) > 1 {
					in.refuse(k+"."+part, pv, fmt.Sprintf("name one order, not both: %s", strings.Join(o.opts, " or ")))
					delete(m, part)
					continue
				}
				if len(named) == 1 {
					s = named[0]
				}
				m[part] = s
			}
		case "species_choices":
			m, ok := v.(map[string]interface{})
			if !ok {
				ex := `send an object, like {"lineage": "high_elf", "spellcasting_ability": "int", "skill": "perception"} for an Elf`
				if sp != nil && len(sp.Needs) > 0 { // an example that fits the species (re-check: a Dragonborn was shown the Elf's)
					ex = fmt.Sprintf("send an object with what %s %s chooses: %s (for example {%q: ...})", aOrAn(sp.Name), sp.Name, strings.Join(sp.Needs, ", "), sp.Needs[0])
				} else if sp != nil {
					ex = fmt.Sprintf("%s %s makes no species choices: leave species_choices out", aOrAn(sp.Name), sp.Name)
				}
				in.refuse(k, v, ex)
				delete(upd, k)
				continue
			}
			for part, pv := range m {
				if pv == nil {
					continue
				}
				s, ok := pv.(string)
				if !ok {
					in.refuse(k+"."+part, pv, fmt.Sprintf("send one name, like %q", speciesChoiceExample[part]))
					delete(m, part)
					continue
				}
				s = strings.TrimSpace(s)
				if why := badText(s, false); why != "" {
					in.refuse(k+"."+part, pv, why)
					delete(m, part)
					continue
				}
				switch part {
				case "spellcasting_ability":
					s = normAbility(s) // "Wisdom" is wis
				case "skill":
					s = strings.ToLower(skillForm(s))
				default:
					s = normKey(s) // "High Elf" and "high-elf" are high_elf
				}
				if sp != nil && !containsStr(sp.Needs, part) {
					asks := "makes no species choices"
					if len(sp.Needs) > 0 {
						asks = "asks for " + strings.Join(sp.Needs, ", ")
					}
					in.refuse(k+"."+part, pv, fmt.Sprintf("%s %s doesn't choose %s: it %s", aOrAn(sp.Name), sp.Name, part, asks))
					delete(m, part)
					continue
				}
				m[part] = s
			}
		case "background_feat_choices", "species_feat_choices":
			m, ok := v.(map[string]interface{})
			if !ok {
				ex := `send an object: Magic Initiate {"list": "cleric", "cantrips": ["guidance", "light"], "spell": "bless", "spellcasting_ability": "wis"}; Skilled {"proficiencies": ["perception", "stealth", "lute"]}`
				bgKey := bstr(body, "background")
				if b, isStr := upd["background"].(string); isStr {
					bgKey = b
				}
				bg := game.GetBackground(strings.ToLower(strings.TrimSpace(bgKey)))
				if k == "background_feat_choices" && bg != nil && bg.Feat == "magic_initiate" { // an example that fits (re-check: a Sage was shown the cleric list)
					ex = fmt.Sprintf(`send an object: {"cantrips": [two %s cantrips], "spell": "a level 1 %s spell", "spellcasting_ability": "int, wis or cha"} (%s's Magic Initiate uses the %s list)`, bg.FeatNote, bg.FeatNote, bg.Name, strings.ToLower(bg.FeatNote))
				} else if k == "background_feat_choices" && bg != nil {
					ex = fmt.Sprintf("%s's Origin feat takes no choices: leave background_feat_choices out", bg.Name)
				}
				in.refuse(k, v, ex)
				delete(upd, k)
				continue
			}
			featChoicesInput(k, m, &in)
		}
	}
	for _, g := range []string{"class_options", "species_choices", "background_feat_choices", "species_feat_choices"} {
		if m, ok := upd[g].(map[string]interface{}); ok && len(m) == 0 { // every part refused (or none sent): nothing to save
			delete(upd, g)
		}
	}
	return in
}

// aOrAn: "a" or "an" before a word.
func aOrAn(w string) string {
	if w != "" && strings.ContainsRune("aeiouAEIOU", rune(w[0])) {
		return "an"
	}
	return "a"
}

// increaseOptions: every answer a background's increases can be, as sent ({"str": 2, "dex": 1} ... {"str": 1, "dex": 1, "con": 1}),
// so the options are the answers themselves (Nov 14 audit: the bare ability names were sent back as the answer).
func increaseOptions(abilities []string) []map[string]int {
	var out []map[string]int
	for _, a := range abilities {
		for _, b := range abilities {
			if a != b {
				out = append(out, map[string]int{a: 2, b: 1})
			}
		}
	}
	all := map[string]int{}
	for _, a := range abilities {
		all[a] = 1
	}
	return append(out, all)
}

// badName: why a character name can't be used ("" when it can).
func badName(s string) string {
	if n := utf8.RuneCountInString(s); n > maxNameRunes {
		return fmt.Sprintf("a name is at most %d characters (yours has %d)", maxNameRunes, n)
	}
	letter := false
	for _, r := range s {
		// invisible: format characters and the other default-ignorable ones (U+3164 HANGUL FILLER is a "letter" that shows nothing),
		// variation selectors and the line and paragraph separators (Nov 14 audit F22)
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return fmt.Sprintf("the name has an invisible character (%U) in it: use letters, digits and spaces", r)
		}
		letter = letter || unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	if s != "" && !letter {
		return "a name needs at least one letter"
	}
	return ""
}

// abilityIncreasesInput: the background's increases with the abilities under their short names ("strength" is str) and whole
// numbers; a 0 means no increase. Or why they can't be read.
func abilityIncreasesInput(v interface{}) (map[string]interface{}, string) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, `send an object: +2 and +1, like {"dex": 2, "con": 1}, or +1 to all three`
	}
	out := map[string]interface{}{}
	seen := map[string]string{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := normAbility(k)
		if !containsStr(abilityShort, a) {
			return nil, fmt.Sprintf("%q is not an ability: use str, dex, con, int, wis or cha", k)
		}
		if prev, dup := seen[a]; dup {
			return nil, fmt.Sprintf("%s is sent twice (%q and %q): send each ability once", a, prev, k)
		}
		seen[a] = k
		if t, isStr := m[k].(string); isStr {
			return nil, fmt.Sprintf("the increase for %s is text (%q): send it as a number, 2 or 1", a, t)
		}
		n, ok := wholeNumber(m[k])
		if !ok {
			return nil, fmt.Sprintf("the increase for %s is %s: send a whole number, 2 or 1", a, jsonText(m[k]))
		}
		if n != 0 {
			out[a] = float64(n)
		}
	}
	return out, ""
}

// featChoicesInput checks the parts of a Magic Initiate or Skilled answer: their JSON types and spellings.
func featChoicesInput(field string, m map[string]interface{}, in *builderInput) {
	for part, v := range m {
		if v == nil {
			continue
		}
		path := field + "." + part
		bad := func(why string) {
			in.refuse(path, v, why)
			delete(m, part)
		}
		switch part {
		case "list", "spell", "spellcasting_ability":
			if l, ok := v.([]interface{}); ok && part == "spell" { // a list of one spell is the spell
				if len(l) != 1 {
					bad(fmt.Sprintf("send one level 1 spell (you sent %d)", len(l)))
					continue
				}
				v = l[0]
			}
			s, ok := v.(string)
			if !ok {
				bad(map[string]string{"list": "send one spell list: cleric, druid or wizard", "spell": "send one level 1 spell, by name",
					"spellcasting_ability": "send int, wis or cha"}[part])
				continue
			}
			s = strings.TrimSpace(s)
			if why := badText(s, false); why != "" {
				bad(why)
				continue
			}
			switch part {
			case "list":
				s = strings.ToLower(s)
			case "spellcasting_ability":
				s = normAbility(s) // "Wisdom" is wis
			}
			m[part] = s
		case "cantrips", "proficiencies":
			ex := map[string]string{"cantrips": `["guidance", "light"]`, "proficiencies": `["perception", "stealth", "lute"]`}[part]
			l, why := textList(part, v, ex)
			if why != "" {
				bad(why)
				continue
			}
			if part == "proficiencies" {
				for i, x := range l {
					l[i] = skillForm(x.(string))
				}
			}
			m[part] = l
		}
	}
}

// groupSubfields: the parts of class_options and species_choices the builder doesn't know, each with a hint (the parts are
// already under their real names).
func groupSubfields(upd map[string]interface{}) []string {
	var out []string
	if m, ok := upd["class_options"].(map[string]interface{}); ok {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := classOrders[k]; ok {
				continue
			}
			switch k {
			case "fighting_style", "style":
				out = append(out, fmt.Sprintf("%q is not part of class_options: a Fighting Style is chosen after creation (the setup lists it: POST /api/characters/fighting-style)", k))
			case "subclass":
				out = append(out, fmt.Sprintf("%q is not part of class_options: a subclass is chosen at level 3 (POST /api/characters/subclass)", k))
			default:
				msg := fmt.Sprintf("%q is not part of class_options: it takes divine_order (Cleric) or primal_order (Druid)", k)
				if c := closestOf(k, func(y func(string)) { y("divine_order"); y("primal_order") }); c != "" {
					msg += fmt.Sprintf(" (did you mean %q?)", c)
				}
				out = append(out, msg)
			}
		}
	}
	if m, ok := upd["species_choices"].(map[string]interface{}); ok {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if containsStr(speciesChoiceKeys, k) {
				continue
			}
			msg := fmt.Sprintf("%q is not part of species_choices: it takes %s, as your species asks", k, strings.Join(speciesChoiceKeys, ", "))
			if c := closestOf(k, func(y func(string)) {
				for _, s := range speciesChoiceKeys {
					y(s)
				}
			}); c != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", c)
			}
			out = append(out, msg)
		}
	}
	return out
}

// ---------------------------------------------------------------- refusing answers with a problem

// splitField: "species_choices.skill" -> species_choices, skill.
func splitField(f string) (string, string) {
	if i := strings.Index(f, "."); i > 0 {
		return f[:i], f[i+1:]
	}
	return f, ""
}

// isFeatGroup: a Magic Initiate answer is one choice made of parts.
func isFeatGroup(f string) bool { return f == "background_feat_choices" || f == "species_feat_choices" }

// rejectedFields: the answers just sent whose choice has a problem; those answers are not saved (the earlier answer stays). For a
// group (species_choices, class_options, a Magic Initiate answer) only the parts sent that have the problem are refused: a valid
// lineage isn't refused because the skill sent with it, or sent earlier, has a problem. problemBefore gives a choice's problem
// before this request; skip lists the answers already refused.
func rejectedFields(steps []builderStep, sent, body map[string]interface{}, problemBefore func(string) string, skip map[string]bool) []map[string]interface{} {
	var out []map[string]interface{}
	seen := map[string]bool{}
	add := func(path string, v interface{}, prob string) {
		if !seen[path] && !skip[path] {
			seen[path] = true
			// a refused answer was never saved: the leftover-answer advice ("Remove it: send ... null (or use_suggestion)") doesn't
			// fit it (Nov 14 audit re-check)
			for _, cut := range []string{". Remove it: send ", ". Remove them: send ", ": send {"} {
				if i := strings.Index(prob, cut); i > 0 && strings.Contains(prob[i:], "null") {
					prob = prob[:i]
				}
			}
			out = append(out, map[string]interface{}{"field": path, "you_sent": v, "problem": prob})
		}
	}
	for _, st := range steps {
		for _, c := range st.Choices {
			prob, _ := c["problem"].(string)
			if prob == "" {
				continue
			}
			f, _ := c["field"].(string)
			for _, part := range strings.Split(f, ", ") {
				top, sub := splitField(part)
				v, was := sent[top]
				if !was || skip[top] {
					continue
				}
				m, isMap := v.(map[string]interface{})
				if sub != "" {
					if sv, has := m[sub]; isMap && has {
						add(part, sv, prob)
					}
					continue
				}
				if isMap && isFeatGroup(top) {
					pp := featPartProblems(body, top)
					found := false
					for sk, sv := range m {
						if p := pp[sk]; p != "" && !skip[top+"."+sk] {
							add(top+"."+sk, sv, p)
							found = true
						}
					}
					if !found && problemBefore(f) != prob { // the request as a whole made the problem
						add(top, v, prob)
					}
					continue
				}
				add(top, v, prob)
			}
		}
		for _, p := range st.Problems { // Step 5's Standard Array check is a step problem
			if st.Key == "ability_scores" && strings.Contains(p, "Standard Array") {
				for _, k := range abilityKeys6 {
					if _, was := sent[k]; was {
						add(k, sent[k], p)
					}
				}
			}
		}
	}
	return out
}

// deepCopyJSON copies a decoded JSON value.
func deepCopyJSON(v interface{}) interface{} {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out interface{}
	if json.Unmarshal(raw, &out) != nil {
		return v
	}
	return out
}

// rollbackAnswer puts back what the draft had before for one field or one part of a group.
func rollbackAnswer(body, before map[string]interface{}, path string) {
	top, sub := splitField(path)
	if sub == "" {
		if old, had := before[top]; had {
			body[top] = deepCopyJSON(old)
		} else {
			delete(body, top)
		}
		return
	}
	m, _ := body[top].(map[string]interface{})
	if m == nil {
		return
	}
	oldM, _ := before[top].(map[string]interface{})
	if old, had := oldM[sub]; had {
		m[sub] = deepCopyJSON(old)
	} else {
		delete(m, sub)
	}
	if _, had := before[top]; len(m) == 0 && !had {
		delete(body, top)
	}
}

// spellSlug: "Sacred Flame" -> sacred-flame.
func spellSlug(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(s), "_", " ")), "-"))
}

// miSpellProblem: what is wrong with one Magic Initiate pick on its own (level 0: a cantrip; 1: the level 1 spell).
func miSpellProblem(name string, level int, list string, speciesCantrips, other []string) string {
	slug := spellSlug(name)
	if sp, ok := srdSpellsMemory[slug]; ok && sp.Level != level {
		if level == 0 {
			return fmt.Sprintf("%s is not a cantrip", sp.Name)
		}
		return fmt.Sprintf("%s is not a level 1 spell", sp.Name)
	}
	if !onClassList(slug, list) {
		return fmt.Sprintf("%s is not on the %s list", originSpellName(slug), list)
	}
	if containsStr(speciesCantrips, slug) {
		return fmt.Sprintf("Your species already gives you %s: choose a different cantrip.", originSpellName(slug))
	}
	if containsStr(other, slug) {
		return fmt.Sprintf("Your background's Magic Initiate already gives you %s: choose a different spell.", originSpellName(slug))
	}
	return ""
}

// miContext: a Magic Initiate answer's list (fixed by the background, or the Human's pick), the background's list, the cantrips
// the species gives and the other Magic Initiate's spells. ok is false when the field isn't a Magic Initiate answer.
func miContext(body map[string]interface{}, field string) (list, bgList string, spC, other []string, ok bool) {
	if bg := game.GetBackground(strings.ToLower(bstr(body, "background"))); bg != nil && bg.Feat == "magic_initiate" {
		bgList = strings.ToLower(bg.FeatNote)
	}
	spC = draftSpeciesCantrips(body)
	if field == "background_feat_choices" {
		return bgList, bgList, spC, nil, bgList != ""
	}
	if !strings.EqualFold(bstr(bmap(body, "species_choices"), "feat"), "magic_initiate") || !strings.EqualFold(bstr(body, "species"), "human") {
		return "", bgList, spC, nil, false
	}
	if bgList != "" {
		bfc := bmap(body, "background_feat_choices")
		for _, s := range append(blist(bfc, "cantrips"), bstr(bfc, "spell")) {
			if s != "" {
				other = append(other, spellSlug(s))
			}
		}
	}
	return strings.ToLower(bstr(bmap(body, field), "list")), bgList, spC, other, true
}

// humanListProblem: what is wrong with the list the Human's Magic Initiate names ("" when it is fine).
func humanListProblem(list, bgList string) string {
	if list != "cleric" && list != "druid" && list != "wizard" {
		return fmt.Sprintf("%q is not a list Magic Initiate uses: choose cleric, druid or wizard", list)
	}
	if list == bgList {
		return "Your background's Magic Initiate already uses the " + list + " list: choose a different list"
	}
	return ""
}

// featPartProblems: what is wrong with each part of a Magic Initiate answer on its own (list, cantrips, spell, ability).
func featPartProblems(body map[string]interface{}, field string) map[string]string {
	out := map[string]string{}
	fc := bmap(body, field)
	list, bgList, spC, other, ok := miContext(body, field)
	if fc == nil || !ok {
		return out
	}
	if field == "species_feat_choices" && list != "" {
		if p := humanListProblem(list, bgList); p != "" {
			out["list"] = p
			list = ""
		}
	}
	if l := strings.ToLower(bstr(fc, "list")); field == "background_feat_choices" && l != "" && l != bgList {
		out["list"] = fmt.Sprintf("your background's Magic Initiate uses the %s list, not %s", bgList, l)
	}
	if a := bstr(fc, "spellcasting_ability"); a != "" && a != "int" && a != "wis" && a != "cha" {
		out["spellcasting_ability"] = fmt.Sprintf("%q can't be the spellcasting ability: choose int, wis or cha", a)
	}
	cs := blist(fc, "cantrips")
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[spellSlug(c)] {
			out["cantrips"] = "you chose the same cantrip twice: choose 2 different ones"
			break
		}
		seen[spellSlug(c)] = true
		if list != "" {
			if p := miSpellProblem(c, 0, list, spC, other); p != "" {
				out["cantrips"] = p
				break
			}
		}
	}
	if out["cantrips"] == "" && len(cs) > 0 && len(cs) != 2 {
		out["cantrips"] = fmt.Sprintf("choose exactly 2 cantrips (you chose %d)", len(cs))
	}
	if s := bstr(fc, "spell"); s != "" && list != "" {
		if p := miSpellProblem(s, 1, list, nil, other); p != "" {
			out["spell"] = p
		}
	}
	return out
}

// featStillNeeded: the parts a Magic Initiate answer still lacks ("list (cleric, druid or wizard)", "spell" ...).
func featStillNeeded(body map[string]interface{}, field string) []string {
	fc := bmap(body, field)
	_, bgList, _, _, ok := miContext(body, field)
	if fc == nil || !ok {
		return nil
	}
	var out []string
	if field == "species_feat_choices" && bstr(fc, "list") == "" {
		var lists []string
		for _, l := range []string{"cleric", "druid", "wizard"} {
			if l != bgList {
				lists = append(lists, l)
			}
		}
		out = append(out, "list ("+strings.Join(lists[:len(lists)-1], ", ")+" or "+lists[len(lists)-1]+")")
	}
	if n := len(blist(fc, "cantrips")); n < 2 {
		out = append(out, fmt.Sprintf("cantrips (%d more)", 2-n))
	}
	if bstr(fc, "spell") == "" {
		out = append(out, "spell (1 level 1 spell)")
	}
	if bstr(fc, "spellcasting_ability") == "" {
		out = append(out, "spellcasting_ability (int, wis or cha)")
	}
	return out
}

// ---------------------------------------------------------------- use_suggestion

func toStrings(v interface{}) []string {
	var out []string
	switch l := v.(type) {
	case []string:
		out = append(out, l...)
	case []interface{}:
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func toInterfaces(l []string) []interface{} {
	out := make([]interface{}, 0, len(l))
	for _, s := range l {
		out = append(out, s)
	}
	return out
}

// normItem: a list item compared loosely ("Smith's Tools", "smiths-tools" and "smiths tools" are one).
func normItem(s string) string {
	s = strings.NewReplacer("_", " ", "-", " ", "'", "", "’", "").Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

func emptyAnswer(v interface{}) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []interface{}:
		return len(x) == 0
	case map[string]interface{}:
		return len(x) == 0
	}
	return false
}

// applySuggestion fills a step's open choices from its suggestion and replaces only what has a problem: a valid answer stays (an
// Acolyte isn't swapped for the suggested Sage, three good skills of four stay, valid Magic Initiate picks stay). It returns the
// fields it filled and, for each earlier answer it changed, what it was.
func applySuggestion(body map[string]interface{}, st builderStep) ([]string, []map[string]interface{}) {
	var filled []string
	var replaced []map[string]interface{}
	sug := map[string]interface{}{} // the same JSON types as fields a player sends
	if raw, err := json.Marshal(st.Suggestion); err == nil {
		json.Unmarshal(raw, &sug)
	}
	set := func(path string, old, now interface{}) {
		filled = append(filled, path)
		if !emptyAnswer(old) && !reflect.DeepEqual(deepCopyJSON(old), deepCopyJSON(now)) {
			replaced = append(replaced, map[string]interface{}{"field": path, "you_had": old, "now": now})
		}
	}
	class := strings.ToLower(bstr(body, "class"))
	for _, c := range st.Choices {
		field, _ := c["field"].(string)
		if done, _ := c["done"].(bool); done || field == "" || strings.HasPrefix(field, "(") {
			continue
		}
		if field == "str, dex, con, int, wis, cha" {
			fillScores(body, sug, set)
			continue
		}
		// a null in the suggestion removes a leftover answer (an Alert background's feat picks, a Rogue's expertise after a change
		// to Fighter, an Elf's lineage on a Dwarf): applied and said (Nov 14 audit: the problem said "or use_suggestion", and
		// use_suggestion answered "done: nothing to fill"). A field can name several parts ("species_choices.lineage, species_choices.skill").
		removed := false
		for _, part := range strings.Split(field, ", ") {
			pt, ps := splitField(part)
			var present bool
			var pv interface{}
			if ps == "" {
				pv, present = sug[pt]
			} else if m, ok := sug[pt].(map[string]interface{}); ok {
				pv, present = m[ps]
			}
			if !present || pv != nil {
				continue
			}
			if ps == "" {
				if old, has := body[pt]; has && old != nil {
					delete(body, pt)
					filled = append(filled, part)
					replaced = append(replaced, map[string]interface{}{"field": part, "you_had": old, "now": nil})
				}
			} else if m := bmap(body, pt); m != nil {
				if old, has := m[ps]; has {
					delete(m, ps)
					filled = append(filled, part)
					replaced = append(replaced, map[string]interface{}{"field": part, "you_had": old, "now": nil})
				}
			}
			removed = true
		}
		if removed || strings.Contains(field, ", ") {
			continue
		}
		top, sub := splitField(field)
		sv := sug[top]
		if sub != "" {
			m, _ := sv.(map[string]interface{})
			sv = m[sub]
		}
		if sv == nil {
			continue // no suggestion for it (the class, the alignment ...)
		}
		var cur interface{}
		if sub == "" {
			cur = body[top]
		} else {
			cur = bmap(body, top)[sub]
		}
		now := sv
		if isFeatGroup(top) && sub == "" {
			now = mergeMagicInitiate(body, top, cur, sv, class)
		} else if _, isList := sv.([]interface{}); isList {
			now = mergeList(body, field, cur, sv, c)
		}
		if isFeatGroup(top) && sub == "" { // said part by part: the list filled, a cantrip replaced
			oldM, _ := cur.(map[string]interface{})
			newM, _ := now.(map[string]interface{})
			body[top] = now
			for _, k := range []string{"list", "cantrips", "spell", "spellcasting_ability", "proficiencies"} {
				if _, has := newM[k]; has && !reflect.DeepEqual(deepCopyJSON(oldM[k]), deepCopyJSON(newM[k])) {
					set(top+"."+k, oldM[k], newM[k])
				}
			}
			continue
		}
		if sub == "" {
			body[top] = now
		} else {
			m := bmap(body, top)
			if m == nil {
				m = map[string]interface{}{}
				body[top] = m
			}
			m[sub] = now
		}
		set(field, cur, now)
	}
	keys := make([]string, 0, len(sug))
	for k := range sug {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys { // a field with no answer whose default stands (background_equipment "a")
		if _, has := body[k]; !has && sug[k] != nil {
			body[k] = sug[k]
			filled = append(filled, k)
		}
	}
	return filled, replaced
}

// fillScores: the suggested Standard Array when no score (or no valid set) is given; scores already given stay, and the values
// left go to the abilities with none, the higher where the suggestion is higher.
func fillScores(body, sug map[string]interface{}, set func(string, interface{}, interface{})) {
	left := []int{15, 14, 13, 12, 10, 8}
	var missing []string
	takeAll := false
	for _, k := range abilityKeys6 {
		n := bint(body, k)
		if n == 0 {
			missing = append(missing, k)
			continue
		}
		i := -1
		for j, x := range left {
			if x == n {
				i = j
			}
		}
		if i < 0 {
			takeAll = true
		} else {
			left = append(left[:i], left[i+1:]...)
		}
	}
	if takeAll || len(missing) == 0 || len(missing) == 6 {
		for _, k := range abilityKeys6 {
			if sv, ok := sug[k]; ok {
				old := body[k]
				body[k] = sv
				set(k, old, sv)
			}
		}
		return
	}
	sort.SliceStable(missing, func(a, b int) bool { return bint(sug, missing[a]) > bint(sug, missing[b]) })
	for i, k := range missing {
		body[k] = float64(left[i])
		set(k, nil, left[i])
	}
}

// mergeList keeps the valid items of a list answer and fills it up from the suggestion, then from the options.
func mergeList(body map[string]interface{}, field string, cur, sv interface{}, c map[string]interface{}) interface{} {
	sl := toStrings(sv)
	count, _ := c["count"].(int)
	if count == 0 {
		count = len(sl)
	}
	valid := func(string) bool { return true }
	opts, _ := c["options"].([]string)
	if field == "species_feat_choices.proficiencies" { // Skilled: skills and tools the character doesn't have
		held := map[string]bool{}
		for _, s := range blist(body, "skill_proficiencies") {
			held[normItem(s)] = true
		}
		if bg := game.GetBackground(strings.ToLower(bstr(body, "background"))); bg != nil {
			for _, s := range bg.Skills {
				held[normItem(s)] = true
			}
		}
		if s := bstr(bmap(body, "species_choices"), "skill"); s != "" {
			held[normItem(s)] = true
		}
		// tools held from the class and the background count too (Nov 14 audit re-check, S4-08/M3: a Skilled Calligrapher's
		// Supplies held from an Acolyte background was "filled" and kept, and use_suggestion looped)
		heldTools := draftHeldTools(body, strings.ToLower(bstr(body, "class")), game.GetBackground(strings.ToLower(bstr(body, "background"))))
		valid = func(s string) bool {
			if held[normItem(s)] {
				return false
			}
			if t, ok := findToolLoose(s); ok && game.HasToolProficiency(heldTools, t.Name) {
				return false
			}
			return true
		}
	} else if field == "expertise" {
		// Expertise goes on skills the character has once this suggestion's skills are in (the options were listed before they
		// were filled: Nov 14 audit re-check, a Rogue's suggested Perception Expertise became Sleight of Hand)
		have := map[string]bool{}
		for _, s := range blist(body, "skill_proficiencies") {
			have[normItem(s)] = true
		}
		if bg := game.GetBackground(strings.ToLower(bstr(body, "background"))); bg != nil {
			for _, s := range bg.Skills {
				have[normItem(s)] = true
			}
		}
		if s := bstr(bmap(body, "species_choices"), "skill"); s != "" {
			have[normItem(s)] = true
		}
		for _, s := range blist(bmap(body, "species_feat_choices"), "proficiencies") {
			have[normItem(s)] = true
		}
		valid = func(s string) bool { return have[normItem(s)] }
	} else if len(opts) > 0 {
		ok := map[string]bool{}
		for _, o := range opts {
			ok[normItem(o)] = true
		}
		valid = func(s string) bool { return ok[normItem(s)] }
	}
	var keep []string
	seen := map[string]bool{}
	add := func(s string) {
		if k := normItem(s); len(keep) < count && !seen[k] && valid(s) {
			seen[k] = true
			keep = append(keep, s)
		}
	}
	for _, h := range toStrings(cur) {
		add(h)
	}
	for _, s := range append(sl, opts...) {
		add(s)
	}
	return toInterfaces(keep)
}

// mergeMagicInitiate keeps the valid parts of a Magic Initiate answer and fills the missing or wrong ones from the suggestion.
func mergeMagicInitiate(body map[string]interface{}, field string, cur, sv interface{}, class string) interface{} {
	have, _ := cur.(map[string]interface{})
	sug, _ := sv.(map[string]interface{})
	if len(have) == 0 {
		return sug
	}
	list, bgList, spC, other, ok := miContext(body, field)
	if !ok {
		return sug
	}
	out, _ := deepCopyJSON(have).(map[string]interface{})
	sugList, _ := sug["list"].(string)
	if field == "background_feat_choices" {
		if l := strings.ToLower(bstr(out, "list")); l != "" && l != list {
			out["list"] = list
		}
	} else if list == "" || humanListProblem(list, bgList) != "" {
		list = ""
		var picks []string // the one list every pick is on (druidcraft and goodberry: druid)
		for _, s := range append(blist(out, "cantrips"), bstr(out, "spell")) {
			if s != "" {
				picks = append(picks, spellSlug(s))
			}
		}
		var fits []string
		for _, l := range []string{"cleric", "druid", "wizard"} {
			all := len(picks) > 0 && l != bgList
			for _, p := range picks {
				all = all && onClassList(p, l)
			}
			if all {
				fits = append(fits, l)
			}
		}
		if len(fits) == 1 {
			list = fits[0]
		} else {
			list = sugList
		}
		out["list"] = list
	}
	if a := bstr(out, "spellcasting_ability"); a != "int" && a != "wis" && a != "cha" {
		out["spellcasting_ability"] = sug["spellcasting_ability"]
	}
	var cands []string
	if sugList == list {
		cands = toStrings(sug["cantrips"])
	}
	var cs, slugs []string
	add := func(c string) {
		if len(cs) < 2 && !containsStr(slugs, spellSlug(c)) && miSpellProblem(c, 0, list, spC, other) == "" {
			cs = append(cs, c)
			slugs = append(slugs, spellSlug(c))
		}
	}
	for _, c := range blist(out, "cantrips") {
		add(c)
	}
	more := magicInitiateSuggestion(list, class, append(append(append([]string{}, slugs...), spC...), other...)...)
	for _, c := range append(cands, toStrings(more["cantrips"])...) {
		add(c)
	}
	out["cantrips"] = toInterfaces(cs)
	if s := bstr(out, "spell"); s == "" || miSpellProblem(s, 1, list, nil, other) != "" {
		ns, _ := sug["spell"].(string)
		if sugList != list || ns == "" || miSpellProblem(ns, 1, list, nil, other) != "" {
			ns, _ = more["spell"].(string)
		}
		out["spell"] = ns
	}
	return out
}

// ---------------------------------------------------------------- the review's summary

// readable: a value from the draft or the dry run as plain text ("Greatsword, Flail, Javelin"), never Go formatting.
func readable(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case []string:
		return strings.Join(x, ", ")
	case []interface{}:
		var parts []string
		for _, e := range x {
			if s := readable(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			if s := readable(x[k]); s != "" {
				parts = append(parts, strings.ReplaceAll(k, "_", " ")+" "+s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(v)
}

// spellNames: spell slugs or names as their SRD names ("mage-hand" -> Mage Hand).
func spellNames(v interface{}) string {
	var out []string
	for _, s := range toStrings(v) {
		out = append(out, originSpellName(spellSlug(s)))
	}
	return strings.Join(out, ", ")
}

// featSummary: an Origin feat and the picks made for it, in words.
func featSummary(feat, list string, fc map[string]interface{}) string {
	out := titleWords(feat)
	switch feat {
	case "magic_initiate":
		if list != "" {
			out += " (" + titleWords(list) + ")"
		}
		var parts []string
		if s := spellNames(fc["cantrips"]); s != "" {
			parts = append(parts, "cantrips "+s)
		}
		if s := bstr(fc, "spell"); s != "" {
			parts = append(parts, "level 1 spell "+originSpellName(spellSlug(s)))
		}
		if a := bstr(fc, "spellcasting_ability"); a != "" {
			parts = append(parts, "spellcasting ability "+firstNonEmpty(abilityFullNames[a], a))
		}
		if len(parts) > 0 {
			out += ": " + strings.Join(parts, "; ")
		}
	case "skilled":
		var p []string
		for _, s := range toStrings(fc["proficiencies"]) {
			p = append(p, strings.ReplaceAll(titleWords(s), " Of ", " of "))
		}
		if len(p) > 0 {
			out += ": " + strings.Join(p, ", ")
		}
	}
	return out
}

// draftSummary: the character the draft makes, in plain lines, for the review step.
func draftSummary(body map[string]interface{}, wc map[string]interface{}) []string {
	var out []string
	add := func(label string, v interface{}) {
		if s := readable(v); s != "" {
			out = append(out, label+": "+s)
		}
	}
	class := strings.ToLower(bstr(body, "class"))
	bg := game.GetBackground(strings.ToLower(bstr(body, "background")))
	sp := game.GetSpecies(strings.ToLower(bstr(body, "species")))
	add("Name", wc["name"])
	add("Class", titleWords(readable(wc["class"])))
	add("Background", titleWords(readable(wc["background"])))
	species := titleWords(readable(wc["species"]))
	sc := bmap(body, "species_choices")
	if sp != nil && sc != nil { // only the choices this species makes
		var parts []string
		for _, need := range sp.Needs {
			v := bstr(sc, need)
			if v == "" || need == "feat" {
				continue
			}
			if need == "spellcasting_ability" {
				v = firstNonEmpty(abilityFullNames[v], v)
			} else {
				v = strings.ReplaceAll(titleWords(v), " Of ", " of ") // Sleight of Hand
			}
			parts = append(parts, strings.ReplaceAll(need, "_", " ")+" "+v)
		}
		if len(parts) > 0 {
			species += " (" + strings.Join(parts, ", ") + ")"
		}
	}
	add("Species", species)
	add("Alignment", wc["alignment"])
	// final scores: the Standard Array plus the background's increases
	inc := map[string]int{}
	if m, ok := body["ability_increases"].(map[string]interface{}); ok {
		for k, v := range m {
			n, _ := wholeNumber(v)
			inc[normAbility(k)] += n
		}
	}
	var scores []string
	for _, k := range abilityKeys6 {
		base := bint(body, k)
		s := fmt.Sprintf("%s %d", strings.ToUpper(k[:1])+k[1:], base+inc[k])
		if inc[k] > 0 {
			s += fmt.Sprintf(" (%d +%d)", base, inc[k])
		}
		scores = append(scores, s)
	}
	out = append(out, "Ability scores: "+strings.Join(scores, ", "))
	add("Hit Points", wc["hp"])
	add("Skills", wc["skills"])
	add("Expertise", wc["expertise"])
	add("Tools", wc["tools"])
	add("Languages", wc["languages"])
	var wm []string
	for _, w := range toStrings(wc["weapon_masteries"]) {
		wm = append(wm, titleWords(w))
	}
	add("Weapon Mastery", strings.Join(wm, ", "))
	co := bmap(body, "class_options")
	for part, o := range classOrders { // only what creation applies: the Cleric's or the Druid's order
		if v := bstr(co, part); v != "" && class == o.class {
			add(titleWords(part), titleWords(v))
		}
	}
	if bg != nil {
		add("Origin feat (background)", featSummary(bg.Feat, bg.FeatNote, bmap(body, "background_feat_choices")))
	}
	if sp != nil && sp.Key == "human" {
		if f := normKey(bstr(sc, "feat")); f != "" {
			fc := bmap(body, "species_feat_choices")
			add("Origin feat (Human)", featSummary(f, bstr(fc, "list"), fc))
		}
	}
	add("Starting equipment (class)", wc["starting_equipment"])
	gold := 0
	opt := firstNonEmpty(strings.ToLower(bstr(body, "starting_equipment")), "a")
	if o, ok := classStartingEquipment[class][opt]; ok {
		gold += o.Gold
	}
	if bg != nil {
		if strings.ToLower(bstr(body, "background_equipment")) == "b" {
			add("Background equipment", fmt.Sprintf("%d GP instead of the background's package", game.BackgroundAltGold))
			gold += game.BackgroundAltGold
		} else {
			kit := backgroundKit(bg)
			if t := bstr(body, "background_tool"); t != "" && bg.ToolCategory != "" { // the gaming set chosen
				kit = strings.Replace(kit, "your chosen "+bg.ToolCategory+" set", t, 1)
			}
			add("Background equipment", kit)
			gold += bg.GoldA
		}
		add("Gold in all", fmt.Sprintf("%d GP", gold))
	}
	if s := bstr(body, "backstory"); s != "" {
		add("Backstory", fmt.Sprintf("%d paragraph(s) written", countParagraphs(s)))
	}
	if s := bstr(body, "appearance"); s != "" {
		add("Appearance", "written")
	}
	return out
}

// countParagraphs: the paragraphs of a text (blank lines between them; empty ones don't count).
func countParagraphs(s string) int {
	n := 0
	for _, p := range paragraphSplit.Split(strings.ReplaceAll(s, "\r\n", "\n"), -1) {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}

// campaignSeats: the campaigns where this player already has a character (one character per player in a campaign).
func campaignSeats(agentID int) []string {
	rows, err := db.Query(`SELECT c.name, l.name FROM characters c JOIN lobbies l ON l.id = c.lobby_id WHERE c.agent_id = $1 AND COALESCE(l.status,'') <> 'completed'`, agentID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cn, ln string
		rows.Scan(&cn, &ln)
		out = append(out, fmt.Sprintf("%s in %s", cn, ln))
	}
	return out
}

// featChoiceAliases: names an answer may use for the Origin feat's choices (some are the names the options were once listed under).
var featChoiceAliases = map[string]string{
	"cantrips_choose_2": "cantrips", "level_1_spell_choose_1": "spell", "level_1_spell": "spell", "level1_spell": "spell",
	"spellcasting_ability_choose_1": "spellcasting_ability", "ability": "spellcasting_ability", "list_choose_1": "list",
	"spell_list": "list", "skills": "proficiencies",
}

var featChoiceFields = map[string]bool{"list": true, "cantrips": true, "spell": true, "spellcasting_ability": true, "proficiencies": true}

// featChoiceSubfields puts the aliases in background_feat_choices and species_feat_choices under their real names, and reports a
// part the builder doesn't know (Nov 14 tester: picks sent as cantrips_choose_2 and level_1_spell_choose_1 were kept but ignored,
// the step stayed unfinished and the suggestion replaced them, with no error).
func featChoiceSubfields(upd map[string]interface{}) []string {
	var out []string
	for _, field := range []string{"background_feat_choices", "species_feat_choices"} {
		fc, ok := upd[field].(map[string]interface{})
		if !ok {
			continue
		}
		for k, v := range fc {
			nk := normKey(k) // "Spellcasting Ability" is spellcasting_ability
			if real, ok := featChoiceAliases[nk]; ok {
				nk = real
			}
			if nk != k {
				delete(fc, k)
				fc[nk] = v
			}
		}
		if l, ok := fc["spells"].([]interface{}); ok && len(l) == 1 && fc["spell"] == nil {
			fc["spell"] = l[0]
			delete(fc, "spells")
		}
		var bad []string
		for k := range fc {
			if !featChoiceFields[k] {
				bad = append(bad, k)
			}
		}
		sort.Strings(bad)
		for _, k := range bad {
			out = append(out, fmt.Sprintf("%q is not part of %s: Magic Initiate takes list, cantrips (2), spell (1) and spellcasting_ability; Skilled takes proficiencies (3)", k, field))
		}
	}
	return out
}
