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
func (e *originError) isRefusal()    {}

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

// normAbility: the short name of the ability an answer means (game.AbilityKey); anything else lowercased, as sent.
func normAbility(a string) string {
	if k, ok := game.AbilityKey(a); ok {
		return k
	}
	return strings.ToLower(strings.TrimSpace(a))
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// resolveFeat checks the choices for one Origin feat.
func resolveFeat(slug, source string, in *featChoiceInput, bgListNote string, usedLists map[string]bool, held *[]string, heldTools *[]string, known *[]string) (*originFeat, *originError) {
	f := &originFeat{Slug: slug, Source: source}
	switch slug {
	case "alert", "savage_attacker":
		return f, nil
	case "magic_initiate":
		if in == nil {
			return nil, originErr("feat_choices_required", nil, "Magic Initiate needs %s: {list, cantrips (2), spell, spellcasting_ability}", featChoicesField(source))
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
		if len(in.Cantrips) != 2 || normalizeSpellSlug(in.Cantrips[0]) == normalizeSpellSlug(in.Cantrips[1]) { // "Sacred Flame" and "sacred-flame" too
			return nil, originErr("invalid_cantrips", nil, "Magic Initiate needs exactly two different cantrips from the %s list", list)
		}
		onList := classSpellListFn(list) // nil when the lists are not loaded; then any spell of the right level is accepted
		check := func(name string, level int) (string, *originError) {
			slug, ok := lookupSpell(name) // any spelling of the name
			sp := srdSpellsMemory[slug]
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
			return nil, originErr("feat_choices_required", nil, "Skilled needs %s.proficiencies: exactly three skills or tools", featChoicesField(source))
		}
		for _, p := range in.Proficiencies {
			if k, ok := game.CanonSkill(p); ok { // "animal-handling" and "Sleight-of-Hand" as the builder takes them (re-check)
				p = k
			}
			if game.IsSkill(p) {
				n := game.SkillName(p)
				if containsStr(*held, n) {
					return nil, originErr("proficiency_already_held", nil, "You already have proficiency in %s; choose another", n)
				}
				*held = append(*held, n)
				f.Proficiencies = append(f.Proficiencies, n)
				continue
			}
			t, ok := game.FindTool(p) // "thieves tools" is Thieves' Tools, as in the builder
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

// speciesFeatChoicesUnused: species_feat_choices are the choices of the Human's Origin feat; they are refused for another species,
// or for parts the Human's feat doesn't take, not ignored.
func speciesFeatChoicesUnused(f *featChoiceInput, feat, species string) *originError {
	if f == nil {
		return nil
	}
	mi := strings.TrimSpace(f.List) != "" || len(f.Cantrips) > 0 || strings.TrimSpace(f.Spell) != "" || strings.TrimSpace(f.SpellcastingAbility) != ""
	sk := len(f.Proficiencies) > 0
	switch {
	case (mi || sk) && feat == "":
		return originErr("species_feat_choices_not_used", nil, "species_feat_choices is only for a Human's Origin feat; %s has none: leave it out.", withArticle(species))
	case sk && feat != "skilled", mi && feat != "magic_initiate":
		return originErr("species_feat_choices_not_used", nil, "Your Human feat is %s, which doesn't take those species_feat_choices: send only the choices it takes (Magic Initiate: list, cantrips, spell, spellcasting_ability; Skilled: proficiencies; Alert and Savage Attacker: none).", titleWords(feat))
	}
	return nil
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

	// Species choices: a part the species doesn't use is refused, not ignored (a Halfling sent "size": "medium" was made Small with no
	// word); a skill written another way ("sleight_of_hand", "sleight-of-hand") is the skill, so the duplicate check below sees it.
	choices := map[string]string{}
	var keys []string
	for k := range in.SpeciesChoices {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k0 := range keys {
		v := in.SpeciesChoices[k0]
		k := game.Key(k0) // "Lineage" is lineage
		if strings.TrimSpace(v) == "" {
			continue
		}
		alias := k == "lineage" && containsStr(sp.Needs, "legacy") && (strings.TrimSpace(in.SpeciesChoices["legacy"]) == "" || strings.EqualFold(game.Key(v), game.Key(in.SpeciesChoices["legacy"])))
		if !containsStr(sp.Needs, k) && !alias {
			why := ""
			if k == "size" && len(sp.Sizes) == 1 {
				why = fmt.Sprintf(" (%s is always %s)", withArticle(sp.Name), sp.Sizes[0])
			}
			asks := "nothing"
			if len(sp.Needs) > 0 {
				asks = strings.Join(sp.Needs, ", ")
			}
			return nil, originErr("species_choice_not_used", sp.Needs, "%s doesn't choose %s%s: leave species_choices.%s out. It asks for: %s.", titleFirst(withArticle(sp.Name)), k, why, k, asks)
		}
		if k == "skill" {
			if n := game.SkillName(v); game.IsSkill(n) {
				v = n
			}
		}
		if k == "spellcasting_ability" { // "Wisdom" is wis, as in the builder (Nov 14 audit re-check)
			if a := normAbility(v); a == "int" || a == "wis" || a == "cha" {
				v = a
			}
		}
		choices[k] = v
	}
	species, serr := game.ResolveSpecies(sp.Key, choices)
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
		if serr := speciesFeatChoicesUnused(in.SpeciesFeat, species.Feat, sp.Name); serr != nil {
			return nil, serr
		}
		f2, ferr := resolveFeat(species.Feat, "species", in.SpeciesFeat, "", usedLists, &heldSkills, &heldTools, &known)
		if ferr != nil {
			return nil, ferr
		}
		feats = append(feats, *f2)
	}
	if species.Feat == "" {
		if serr := speciesFeatChoicesUnused(in.SpeciesFeat, "", sp.Name); serr != nil {
			return nil, serr
		}
	}
	for _, f := range feats {
		out.FeatSlugs = append(out.FeatSlugs, f.Slug)
		skills, tools, _ := f.grants()
		out.Skills = append(out.Skills, skills...)
		out.Tools = append(out.Tools, tools...)
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
	writeJSONStatus(w, http.StatusBadRequest, resp) // a refusal is a 4xx (Nov 14 audit F5: it was a 200)
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

// Where an Origin feat comes from (originFeat.Source): a character's background or the Human's Versatile trait at creation, or a
// feat taken later with an Ability Score Improvement or the Warlock's Lessons of the First Ones (POST /api/characters/{id}/feat).
const (
	featFromASI     = "asi"
	featFromLessons = "lessons"
)

// featSourceName: whose Magic Initiate a message is about.
func featSourceName(source string) string {
	switch source {
	case "species":
		return "your Human feat's"
	case "background":
		return "your background's"
	}
	return "this"
}

// featChoicesField: the field that carries a feat's choices: background_feat_choices and species_feat_choices at creation, choices
// in the feat request.
func featChoicesField(source string) string {
	if source == "background" || source == "species" {
		return source + "_feat_choices"
	}
	return "choices"
}

// featSourceLabel: where a feat comes from, as the character sheet shows it.
func featSourceLabel(f originFeat, rec *originRecord) string {
	switch f.Source {
	case "species":
		return rec.Species.Name + " (Versatile)"
	case featFromASI:
		return "Ability Score Improvement"
	case featFromLessons:
		return "Lessons of the First Ones"
	}
	if bg := game.GetBackground(rec.Background); bg != nil {
		return bg.Name + " background"
	}
	return titleWords(rec.Background) + " background"
}

// grants: what an Origin feat's picks give the character: skills and tools (Skilled) and spells (Magic Initiate). Creation and
// the feat request both apply them from here.
func (f originFeat) grants() (skills, tools, spells []string) {
	for _, p := range f.Proficiencies {
		if game.IsSkill(p) {
			skills = append(skills, p)
		} else {
			tools = append(tools, p)
		}
	}
	spells = append(append(spells, f.Cantrips...), f.Spell)
	if f.Spell == "" {
		spells = spells[:len(spells)-1]
	}
	return
}

// takeOriginFeat: an Origin feat taken after creation (an Ability Score Improvement or Lessons of the First Ones). Its choices are
// checked by resolveFeat, the creation check, against what the character already has: its skill and tool proficiencies, the
// spells it has (spellsHeld), and the spell lists of its other Magic Initiate feats. It returns the feat as it is recorded and the
// columns to write with it (origin, skill_proficiencies, tool_proficiencies, known_spells); nothing is written here.
func takeOriginFeat(charID int, slug, source string, in *featChoiceInput) (*originFeat, map[string]interface{}, *originError) {
	var raw []byte
	var skills, tools string
	if err := db.QueryRow(`SELECT COALESCE(origin,'{}'), COALESCE(skill_proficiencies,''), COALESCE(tool_proficiencies,'') FROM characters WHERE id = $1`, charID).
		Scan(&raw, &skills, &tools); err != nil {
		if dbFailed(err) {
			return nil, nil, originErr(dbErrorCode, nil, "%s", dbReadMessage(fmt.Sprintf("character %d", charID)))
		}
		return nil, nil, originErr("character_not_found", nil, "Character %d not found", charID)
	}
	rec, ok := loadOriginRecord(raw)
	if !ok && in == nil && availableFeats[slug].ChoicesWhenTaken == "" {
		return &originFeat{Slug: slug, Source: source}, map[string]interface{}{}, nil // nothing to record for Alert or Savage Attacker
	}
	if !ok {
		return nil, nil, originErr("no_origin_record", nil, "This character was made before origins were recorded, so a feat with choices can't be added to it. Ask the server's admin.")
	}
	for _, f := range rec.Feats {
		if source == featFromLessons && f.Source == featFromLessons && f.Slug == slug {
			// SRD 5.2.1: each time you gain the invocation, choose a different Origin feat
			return nil, nil, originErr("lessons_feat_repeated", nil, "You already took %s with Lessons of the First Ones: each copy of the invocation gives a different Origin feat", titleWords(strings.ReplaceAll(slug, "_", " ")))
		}
	}
	heldSkills := game.ParseProficiencyList(skills)
	heldTools := game.ParseProficiencyList(tools)
	usedLists := map[string]bool{}
	for _, f := range rec.Feats {
		if f.Slug == "magic_initiate" {
			usedLists[f.List] = true
		}
	}
	var known []string
	for s := range spellsHeld(charID) {
		known = append(known, s)
	}
	sort.Strings(known)
	f, err := resolveFeat(slug, source, in, "", usedLists, &heldSkills, &heldTools, &known)
	if err != nil {
		return nil, nil, err
	}
	rec.Feats = append(rec.Feats, *f)
	newSkills, newTools, newSpells := f.grants()
	for _, sk := range newSkills {
		skills = addListWord(skills, sk)
	}
	knownSpells := jsonListOf(charID, "known_spells")
	for _, sp := range newSpells {
		if !containsStr(knownSpells, sp) {
			knownSpells = append(knownSpells, sp)
		}
	}
	originJSON, _ := json.Marshal(rec)
	knownJSON, _ := json.Marshal(knownSpells)
	return f, map[string]interface{}{"origin": string(originJSON), "skill_proficiencies": skills,
		"tool_proficiencies": game.MergeToolList(tools, newTools), "known_spells": string(knownJSON)}, nil
}
