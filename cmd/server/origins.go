package main

// Character origins (2024 rules): background, species, Origin feats and
// languages. resolveOrigin checks every choice a player makes at character
// creation and returns what the character gets. Nothing here touches the
// database, so it is easy to test.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// classSpellListFn looks up a class spell list; tests replace it.
var classSpellListFn = getClassSpellList

// originError is a refused choice, with a code and a helpful message.
type originError struct {
	Code    string
	Message string
	Options []string
}

func (e *originError) Error() string { return e.Code + ": " + e.Message }

func originErr(code string, options []string, format string, a ...interface{}) *originError {
	return &originError{Code: code, Message: fmt.Sprintf(format, a...), Options: options}
}

// originFeats are the feats a background or the Human species can give.
var originFeatSlugs = []string{"alert", "magic_initiate", "savage_attacker", "skilled"}

func isOriginFeat(slug string) bool {
	for _, s := range originFeatSlugs {
		if s == slug {
			return true
		}
	}
	return false
}

// featChoiceInput is what a player sends for a feat that needs choices.
type featChoiceInput struct {
	List                string   `json:"list"`                 // Magic Initiate: cleric, druid or wizard
	Cantrips            []string `json:"cantrips"`             // Magic Initiate: two cantrips
	Spell               string   `json:"spell"`                // Magic Initiate: one level 1 spell
	SpellcastingAbility string   `json:"spellcasting_ability"` // Magic Initiate: int, wis or cha
	Proficiencies       []string `json:"proficiencies"`        // Skilled: three skills or tools
}

// originFeat is a feat as recorded on the character.
type originFeat struct {
	Slug          string   `json:"slug"`
	Source        string   `json:"source"` // "background" or "species"
	List          string   `json:"list,omitempty"`
	Cantrips      []string `json:"cantrips,omitempty"`
	Spell         string   `json:"spell,omitempty"`
	Ability       string   `json:"spellcasting_ability,omitempty"`
	Proficiencies []string `json:"proficiencies,omitempty"`
}

// originRecord is stored on the character (characters.origin).
type originRecord struct {
	Background          string              `json:"background"`
	AbilityIncreases    map[string]int      `json:"ability_increases"`
	BackgroundEquipment string              `json:"background_equipment"`
	BackgroundTool      string              `json:"background_tool,omitempty"`
	Species             *game.SpeciesResult `json:"species"`
	Feats               []originFeat        `json:"feats"`
	Languages           []string            `json:"languages"`
}

// originInput is the part of the creation request about origins.
type originInput struct {
	Background          string
	Species             string
	AbilityIncreases    map[string]int
	BackgroundTool      string
	BackgroundEquipment string
	SpeciesChoices      map[string]string
	BackgroundFeat      *featChoiceInput
	SpeciesFeat         *featChoiceInput
	Languages           []string
	HeldSkills          []string // skills already chosen from the class
	HeldTools           []string // tool proficiencies already held
}

// originOutput is everything the character gets from its origin.
type originOutput struct {
	Record      originRecord
	Skills      []string // new skill proficiencies (lowercase)
	Tools       []string // new tool proficiencies (lowercase)
	Languages   []string
	Increases   map[string]int // short ability name -> total increase
	KnownSpells []string
	FeatSlugs   []string
	Gold        int
	Kit         []startItem
	KitTool     string
}

var abilityShort = []string{"str", "dex", "con", "int", "wis", "cha"}

func normAbility(a string) string {
	a = strings.ToLower(strings.TrimSpace(a))
	for _, full := range [][2]string{{"strength", "str"}, {"dexterity", "dex"}, {"constitution", "con"}, {"intelligence", "int"}, {"wisdom", "wis"}, {"charisma", "cha"}} {
		if a == full[0] {
			return full[1]
		}
	}
	return a
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func normSkill(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, "_", " ")))
}

// resolveFeat checks the choices for one Origin feat.
func resolveFeat(slug, source string, in *featChoiceInput, bgListNote string, usedLists map[string]bool, held *[]string, heldTools *[]string, known *[]string) (*originFeat, *originError) {
	f := &originFeat{Slug: slug, Source: source}
	switch slug {
	case "alert", "savage_attacker":
		return f, nil
	case "magic_initiate":
		if in == nil {
			return nil, originErr("feat_choices_required", nil, "Magic Initiate needs %s_feat_choices: {list, cantrips (2), spell, spellcasting_ability}", source)
		}
		list := strings.ToLower(strings.TrimSpace(in.List))
		if bgListNote != "" {
			if list == "" {
				list = strings.ToLower(bgListNote)
			}
			if list != strings.ToLower(bgListNote) {
				return nil, originErr("wrong_spell_list", []string{strings.ToLower(bgListNote)}, "This background gives Magic Initiate (%s); the list must be %s", bgListNote, strings.ToLower(bgListNote))
			}
		}
		if list != "cleric" && list != "druid" && list != "wizard" {
			return nil, originErr("invalid_spell_list", []string{"cleric", "druid", "wizard"}, "Magic Initiate list must be cleric, druid or wizard")
		}
		if usedLists[list] {
			return nil, originErr("spell_list_already_used", nil, "You can take Magic Initiate again only with a different spell list than %s", list)
		}
		ab := normAbility(in.SpellcastingAbility)
		if ab != "int" && ab != "wis" && ab != "cha" {
			return nil, originErr("invalid_spellcasting_ability", []string{"int", "wis", "cha"}, "Magic Initiate spellcasting_ability must be int, wis or cha")
		}
		if len(in.Cantrips) != 2 || builderSpellSlug(in.Cantrips[0]) == builderSpellSlug(in.Cantrips[1]) { // "Sacred Flame" and "sacred-flame" too
			return nil, originErr("invalid_cantrips", nil, "Magic Initiate needs exactly two different cantrips from the %s list", list)
		}
		onList := classSpellListFn(list) // nil when the lists are not loaded; then any spell of the right level is accepted
		check := func(name string, level int) (string, *originError) {
			slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
			sp, ok := srdSpellsMemory[slug]
			if !ok {
				return "", originErr("unknown_spell", nil, "%q is not a known spell", name)
			}
			if sp.Level != level {
				return "", originErr("wrong_spell_level", nil, "%s is level %d, but a level %d spell is needed here", sp.Name, sp.Level, level)
			}
			if onList != nil && !containsStr(onList, slug) {
				return "", originErr("not_on_spell_list", nil, "%s is not on the %s spell list", sp.Name, list)
			}
			return slug, nil
		}
		for _, c := range in.Cantrips {
			s, err := check(c, 0)
			if err != nil {
				return nil, err
			}
			if containsStr(*known, s) {
				return nil, originErr("spell_already_known", nil, "You already know %s (from your species or your other Magic Initiate): choose a different cantrip for %s Magic Initiate", originSpellName(s), featSourceName(source))
			}
			f.Cantrips = append(f.Cantrips, s)
		}
		s, err := check(in.Spell, 1)
		if err != nil {
			return nil, err
		}
		if containsStr(*known, s) {
			return nil, originErr("spell_already_known", nil, "You already have %s from your other Magic Initiate: choose a different level 1 spell for %s Magic Initiate", originSpellName(s), featSourceName(source))
		}
		f.Spell, f.List, f.Ability = s, list, ab
		usedLists[list] = true
		*known = append(*known, f.Cantrips...)
		*known = append(*known, f.Spell)
	case "skilled":
		if in == nil || len(in.Proficiencies) != 3 {
			return nil, originErr("feat_choices_required", nil, "Skilled needs %s_feat_choices.proficiencies: exactly three skills or tools", source)
		}
		for _, p := range in.Proficiencies {
			if game.IsSkill(p) {
				n := normSkill(p)
				if containsStr(*held, n) {
					return nil, originErr("proficiency_already_held", nil, "You already have proficiency in %s; choose another", n)
				}
				*held = append(*held, n)
				f.Proficiencies = append(f.Proficiencies, n)
				continue
			}
			t, ok := game.FindTool(p)
			if !ok {
				return nil, originErr("unknown_proficiency", nil, "%q is neither a skill nor a tool", p)
			}
			if game.HasToolProficiency(*heldTools, t.Name) {
				return nil, originErr("proficiency_already_held", nil, "You already have proficiency with %s; choose another", game.ToolDisplayName(t.Name))
			}
			*heldTools = append(*heldTools, t.FullName())
			f.Proficiencies = append(f.Proficiencies, t.FullName())
		}
	default:
		return nil, originErr("not_an_origin_feat", originFeatSlugs, "%q is not an Origin feat", slug)
	}
	return f, nil
}

// resolveOrigin checks all origin choices and works out what the character gets.
func resolveOrigin(in originInput) (*originOutput, *originError) {
	bg := game.GetBackground(in.Background)
	if bg == nil {
		return nil, originErr("invalid_background", game.BackgroundSlugs(), "%q is not a background. The SRD 5.2.1 backgrounds are: %s", in.Background, strings.Join(game.BackgroundSlugs(), ", "))
	}
	sp := game.GetSpecies(in.Species)
	if sp == nil {
		hint := game.RetiredSpeciesHint(in.Species)
		var names []string
		for _, s := range game.AllSpecies() {
			names = append(names, s.Key)
		}
		msg := fmt.Sprintf("%q is not a species. The SRD 5.2.1 species are: %s", in.Species, strings.Join(names, ", "))
		if hint != "" {
			msg += ". Use " + hint + " instead"
		}
		return nil, originErr("invalid_species", names, "%s", msg)
	}

	// Ability score increases come from the background
	inc := map[string]int{}
	for k, v := range in.AbilityIncreases {
		inc[normAbility(k)] += v
	}
	if err := game.ValidateAbilityIncreases(bg, inc); err != nil {
		return nil, originErr("invalid_ability_increases", bg.AbilityScores,
			"%s: %s must be raised +2 and +1 on two of (%s), or +1 on all three. Send ability_increases like {\"%s\": 2, \"%s\": 1}",
			bg.Name, bg.Name, strings.Join(bg.AbilityScores, ", "), bg.AbilityScores[0], bg.AbilityScores[1])
	}

	species, serr := game.ResolveSpecies(sp.Key, in.SpeciesChoices)
	if serr != nil {
		return nil, originErr("species_choices_required", sp.Needs, "%s", serr.Error())
	}

	langs, lerr := game.ValidateLanguages(in.Languages)
	if lerr != nil {
		return nil, originErr("invalid_languages", game.StandardLanguages, "extra_languages: %s", lerr.Error())
	}

	out := &originOutput{Increases: inc, Languages: langs}
	heldSkills := append([]string(nil), in.HeldSkills...)
	heldTools := append([]string(nil), in.HeldTools...)

	// Skills: background (2), species (Human or Elf). A skill that comes twice would leave the character a proficiency short: the
	// player chooses another one instead (the class choice, or the species choice).
	for _, s := range bg.Skills {
		if containsStr(heldSkills, s) {
			return nil, originErr("duplicate_skill", nil, "%s already gives %s: choose a different class skill in skill_proficiencies, so you don't lose a proficiency.", bg.Name, titleWords(s))
		}
		heldSkills = append(heldSkills, s)
		out.Skills = append(out.Skills, s)
	}
	for _, s := range species.Skills {
		if containsStr(heldSkills, s) {
			return nil, originErr("duplicate_skill", nil, "You already have %s (from your class or background): choose a different skill in species_choices.skill.", titleWords(s))
		}
		heldSkills = append(heldSkills, s)
		out.Skills = append(out.Skills, s)
	}

	// Tool proficiency, stored under its full name: a Gaming Set with its kind, "gaming set (dice)" (BUGS 45)
	tool := ""
	if bg.Tool != "" {
		tool = game.CanonicalToolName(bg.Tool)
	}
	if bg.ToolCategory != "" {
		t, ok := game.FindTool(in.BackgroundTool)
		if !ok || t.Category != bg.ToolCategory {
			return nil, originErr("background_tool_required", game.ToolsInCategory(bg.ToolCategory),
				"%s needs background_tool: one kind of Gaming Set (%s)", bg.Name, strings.Join(game.ToolsInCategory(bg.ToolCategory), ", "))
		}
		tool = t.FullName()
	}
	if tool != "" && !game.HasToolProficiency(heldTools, tool) {
		heldTools = append(heldTools, tool)
		out.Tools = append(out.Tools, tool)
	}

	// Feats: the background's Origin feat, and the Human's extra one
	usedLists := map[string]bool{}
	known := append([]string(nil), species.Cantrips...) // a feat can't give a cantrip the species already gives
	var feats []originFeat
	f1, ferr := resolveFeat(bg.Feat, "background", in.BackgroundFeat, bg.FeatNote, usedLists, &heldSkills, &heldTools, &known)
	if ferr != nil {
		return nil, ferr
	}
	feats = append(feats, *f1)
	if species.Feat != "" {
		if !isOriginFeat(species.Feat) {
			return nil, originErr("not_an_origin_feat", originFeatSlugs, "The Human's feat must be an Origin feat: %s", strings.Join(originFeatSlugs, ", "))
		}
		if species.Feat == bg.Feat && species.Feat != "skilled" && species.Feat != "magic_initiate" {
			return nil, originErr("feat_already_taken", originFeatSlugs, "Your background already gives %s; choose a different Origin feat", species.Feat)
		}
		f2, ferr := resolveFeat(species.Feat, "species", in.SpeciesFeat, "", usedLists, &heldSkills, &heldTools, &known)
		if ferr != nil {
			return nil, ferr
		}
		feats = append(feats, *f2)
	}
	for _, f := range feats {
		out.FeatSlugs = append(out.FeatSlugs, f.Slug)
		for _, p := range f.Proficiencies {
			if game.IsSkill(p) {
				out.Skills = append(out.Skills, p)
			} else {
				out.Tools = append(out.Tools, p)
			}
		}
	}

	// Spells from species and feats
	out.KnownSpells = known // species cantrips first, then the feats' spells

	// Equipment: package A or 50 GP
	opt := strings.ToLower(strings.TrimSpace(in.BackgroundEquipment))
	if opt == "" {
		opt = "a"
	}
	switch opt {
	case "a":
		out.Gold = bg.GoldA
		for _, it := range bg.Equipment {
			s := startItem{Name: it.Name, Qty: it.Qty}
			if it.Quiver {
				s.Place = "quiver"
			}
			if it.Choice {
				s.Name = choiceTool
				out.KitTool = tool
			}
			out.Kit = append(out.Kit, s)
		}
	case "b":
		out.Gold = bg.GoldB
	default:
		return nil, originErr("invalid_background_equipment", []string{"a", "b"}, "background_equipment must be \"a\" (the package) or \"b\" (50 GP)")
	}

	sort.Strings(out.Skills)
	out.Record = originRecord{
		Background: bg.Key, AbilityIncreases: inc, BackgroundEquipment: opt, BackgroundTool: tool,
		Species: species, Feats: feats, Languages: langs,
	}
	return out, nil
}

// loadOriginRecord reads characters.origin; ok is false for older characters.
func loadOriginRecord(raw []byte) (*originRecord, bool) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil, false
	}
	var rec originRecord
	if json.Unmarshal(raw, &rec) != nil || rec.Species == nil {
		return nil, false
	}
	return &rec, true
}

// speciesAsSRDSpecies builds the in-memory species table from the 2024 species.
// Species give no ability scores or languages; the background and the player's
// language choices do.
func speciesAsSRDSpecies() map[string]SRDSpecies {
	out := map[string]SRDSpecies{}
	for _, sp := range game.AllSpecies() {
		names := make([]string, 0, len(sp.Traits))
		for _, t := range sp.Traits {
			names = append(names, t.Name)
		}
		out[sp.Key] = SRDSpecies{Name: sp.Name, Size: sp.Sizes[0], Speed: sp.Speed, AbilityMods: map[string]int{},
			Traits: names, Languages: []string{}, DarkvisionRange: sp.Darkvision}
	}
	return out
}

// normalizeSpeciesSlug makes "High Elf" and "high-elf" into one form.
func normalizeSpeciesSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	return strings.ReplaceAll(s, "-", "_")
}

// checkAbilityIncreases validates a background's ability increases early, so
// the scores are right before hit points are worked out.
func checkAbilityIncreases(background string, raw map[string]int) (map[string]int, *originError) {
	bg := game.GetBackground(background)
	if bg == nil {
		return nil, originErr("invalid_background", game.BackgroundSlugs(), "%q is not a background. The SRD 5.2.1 backgrounds are: %s", background, strings.Join(game.BackgroundSlugs(), ", "))
	}
	inc := map[string]int{}
	for k, v := range raw {
		inc[normAbility(k)] += v
	}
	if err := game.ValidateAbilityIncreases(bg, inc); err != nil {
		return nil, originErr("invalid_ability_increases", bg.AbilityScores,
			"%s: raise one of (%s) by 2 and another by 1, or all three by 1. Send ability_increases like {\"%s\": 2, \"%s\": 1}",
			bg.Name, strings.Join(bg.AbilityScores, ", "), bg.AbilityScores[0], bg.AbilityScores[1])
	}
	return inc, nil
}

// writeOriginError sends a refused origin choice as JSON.
func writeOriginError(w http.ResponseWriter, e *originError) {
	resp := map[string]interface{}{"error": e.Code, "message": e.Message}
	if len(e.Options) > 0 {
		resp["valid_options"] = e.Options
	}
	json.NewEncoder(w).Encode(resp)
}

// backgroundSummary lists what a background gave the character, for the sheet.
func backgroundSummary(rec *originRecord) string {
	var lines []string
	bg := game.GetBackground(rec.Background)
	if bg != nil && bg.Description != "" {
		lines = append(lines, bg.Description)
	}
	var featLines []string
	for _, f := range rec.Feats {
		if f.Source != "" && f.Source != "background" {
			continue // a feat from the species (Human Versatile) is listed with the species
		}
		name := ""
		if ft, ok := lookupFeat(f.Slug); ok {
			name = ft.Name
		} else {
			name = titleWords(f.Slug)
		}
		line := name
		switch f.Slug {
		case "magic_initiate":
			line += fmt.Sprintf(" (%s list; cantrips %s; spell %s; spellcasting ability %s)", titleWords(f.List), skillNames(f.Cantrips), titleWords(f.Spell), strings.ToUpper(f.Ability))
		case "skilled":
			line += " (" + profNames(f.Proficiencies) + ")"
		}
		featLines = append(featLines, line)
	}
	if len(featLines) > 0 {
		lines = append(lines, "Feats: "+strings.Join(featLines, "; ")+".")
	}
	if bg != nil {
		lines = append(lines, "Skill Proficiencies: "+strings.Replace(skillNames(bg.Skills), ", ", " and ", 1)+".")
	}
	if rec.BackgroundTool != "" {
		lines = append(lines, "Tool Proficiency: "+game.ToolDisplayName(rec.BackgroundTool)+".")
	}
	return strings.Join(lines, "\n")
}

// profNames writes skills and tools with capitals: "Athletics, Gaming Set (Dice)".
func profNames(in []string) string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if game.IsSkill(p) {
			out = append(out, skillNames([]string{p}))
		} else {
			out = append(out, game.ToolDisplayName(p))
		}
	}
	return strings.Join(out, ", ")
}

// skillNames writes a list of skills, tools or spells with the usual capitals: "Sleight of Hand, Stealth".
func skillNames(in []string) string {
	small := map[string]bool{"of": true, "and": true, "the": true, "or": true}
	out := make([]string, 0, len(in))
	for _, s := range in {
		words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(s))
		for i, w := range words {
			lw := strings.ToLower(w)
			if i > 0 && small[lw] {
				words[i] = lw
			} else {
				words[i] = strings.ToUpper(lw[:1]) + lw[1:]
			}
		}
		out = append(out, strings.Join(words, " "))
	}
	return strings.Join(out, ", ")
}

// hasSpeciesResistance reports whether the character's species gives Resistance to a damage type.
func hasSpeciesResistance(charID int, damageType string) bool {
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw) != nil {
		return false
	}
	rec, ok := loadOriginRecord(raw)
	if !ok {
		return false
	}
	if rec.Species.Key == "dwarf" && strings.EqualFold(strings.TrimSpace(damageType), "poison") {
		return true // Dwarven Resilience (origin records made before the species listed it; F81)
	}
	return containsStr(rec.Species.Resistances, strings.TrimSpace(damageType))
}

// speciesHPPerLevel is the extra Hit Points per level a species gives (Dwarven Toughness).
func speciesHPPerLevel(charID int) int {
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw) != nil {
		return 0
	}
	if rec, ok := loadOriginRecord(raw); ok {
		return rec.Species.HPPerLevel
	}
	return 0
}

// originSpellName is a spell's SRD name (or its slug in words when the spells aren't loaded).
func originSpellName(slug string) string {
	if sp, ok := srdSpellsMemory[slug]; ok && sp.Name != "" {
		return sp.Name
	}
	return titleWords(strings.ReplaceAll(slug, "-", " "))
}

// featSourceName: "your background's" or "your Human feat's".
func featSourceName(source string) string {
	if source == "species" {
		return "your Human feat's"
	}
	return "your background's"
}
