package main

// More setup steps of the choices in choiceDefs (choices_2024.go). Each is called only through its entry in the table.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// classWith: the class of the character that gives the choice (its main class when only that one does).
func (d *choiceDef) classWith(c *choiceChar) string {
	best, most := c.class, 0
	if d.total == nil {
		return best
	}
	for class, level := range c.levels {
		if n := d.total(class, c.subs[class], level); n > most {
			best, most = class, n
		}
	}
	return best
}

func fightingStyleStep(c *choiceChar) *setupStep {
	cl := choiceByKey("fighting_style").classWith(c)
	var mainHand string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, c.id).Scan(&mainHand)
	style := "defense"
	if mainHand != "" {
		if isTwoHandedWeapon(mainHand) {
			style = "great_weapon_fighting"
		} else if _, wpn, ok := findWeapon(mainHand); ok && strings.Contains(strings.ToLower(wpn.Type+" "+strings.Join(wpn.Properties, " ")), "ranged") {
			style = "archery"
		}
	}
	known := getFightingStyles(c.id)
	if containsStr(known, style) { // Additional Fighting Style: suggest one not known yet
		for _, s := range []string{"defense", "great_weapon_fighting", "archery", "two_weapon_fighting"} {
			if !containsStr(known, s) {
				style = s
				break
			}
		}
	}
	var opts []map[string]string
	var slugs []string
	for slug := range fightingStyles {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		f := fightingStyles[slug]
		if containsStr(known, slug) || !containsStr(f.Classes, cl) {
			continue
		}
		does := f.Description
		if ft, ok := availableFeats[slug]; ok && len(ft.Benefits) > 0 {
			does = strings.Join(ft.Benefits, " ") // the feat's SRD text
		}
		opts = append(opts, map[string]string{"style": slug, "name": f.Name, "what_it_does": does})
	}
	why := "Fighting Style. " + classFeatureText(cl, "Fighting Style")
	if len(known) > 0 {
		why = "Additional Fighting Style: you gain another Fighting Style feat. You have: " + strings.Join(known, ", ") + "."
	}
	return &setupStep{Key: "fighting_style", Title: "Choose a Fighting Style", Why: why,
		Request: req("POST", "/api/characters/fighting-style", map[string]interface{}{"character_id": c.id, "style": style}),
		Choices: map[string]interface{}{"options": opts}}
}

func invocationStep(c *choiceChar) *setupStep {
	pick := suggestInvocation(c.id)
	st := setupStep{Key: "invocations", Title: "Choose your Eldritch Invocation",
		Why:     "Eldritch Invocations. " + classFeatureText("warlock", "Eldritch Invocations") + " Send one invocation per request.",
		Choices: map[string]interface{}{"options_you_qualify_for": invocationOptions(c.id)}}
	if pick == "" {
		st.Request = req("GET", fmt.Sprintf("/api/characters/invocations?character_id=%d", c.id), nil)
		return &st
	}
	body := map[string]interface{}{"character_id": c.id, "invocation": pick}
	if pick == "pact-of-the-tome" { // the Book of Shadows picks go with it
		body["cantrips"], body["rituals"] = suggestBookOfShadows(c.id)
		st.Why += " Pact of the Tome: send cantrips (3 cantrips) and rituals (2 level 1 Ritual spells) for your Book of Shadows with it; any class's spell list."
	}
	st.Request = req("POST", "/api/characters/invocations", body)
	return &st
}

func bookOfShadowsStep(c *choiceChar) *setupStep {
	cantrips, rituals := suggestBookOfShadows(c.id)
	suggested := map[string]bool{}
	for _, x := range append(append([]string{}, cantrips...), rituals...) {
		suggested[x] = true
	}
	var cOpts, rOpts []map[string]interface{}
	have := bookHave(c.id)
	for _, slug := range sortedSpellSlugs() {
		sp := srdSpellsMemory[slug]
		if sp.Level == 0 && bookPickOK(slug, false, have) {
			cOpts = append(cOpts, spellOptionInfo(slug, suggested))
		} else if sp.Level == 1 && sp.IsRitual && bookPickOK(slug, true, have) {
			rOpts = append(rOpts, spellOptionInfo(slug, suggested))
		}
	}
	return &setupStep{Key: "book_of_shadows", Title: "Fill your Book of Shadows",
		Why:     "Pact of the Tome: choose three cantrips and two level 1 spells with the Ritual tag, from any class's spell list, that you don't already have prepared. While the book is on your person you have them prepared, and they work as Warlock spells for you.",
		Request: req("POST", "/api/characters/invocations", map[string]interface{}{"character_id": c.id, "invocation": "pact-of-the-tome", "cantrips": cantrips, "rituals": rituals}),
		Choices: map[string]interface{}{"cantrips": map[string]interface{}{"choose": 3, "options": cOpts}, "rituals": map[string]interface{}{"choose": 2, "options": rOpts}}}
}

// subclassStep: the subclass of the first class that is due one.
func subclassStep(c *choiceChar) *setupStep {
	var classes []string
	for class, level := range c.levels {
		if level >= 3 && c.subs[class] == "" {
			classes = append(classes, class)
		}
	}
	sort.Strings(classes)
	for _, cl := range classes {
		if sub := srdSubclassFor[cl]; sub != "" {
			return &setupStep{Key: "subclass", Title: "Choose your subclass",
				Why:     classFeatureText(cl, titleCase(cl)+" Subclass") + " Each option below has its introduction and its features by level.",
				Request: req("POST", "/api/characters/subclass", map[string]interface{}{"character_id": c.id, "subclass": sub}), Choices: subclassChoices(cl)}
		}
	}
	return nil
}

func metamagicStep(c *choiceChar) *setupStep {
	known := getMetamagicChoices(c.id)
	var opts []map[string]interface{}
	var slugs []string
	for slug := range metamagicOptions {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	pick := ""
	for _, slug := range slugs {
		if containsStr(known, slug) {
			continue
		}
		o := metamagicOptions[slug]
		opts = append(opts, map[string]interface{}{"metamagic": slug, "name": o.Name, "cost_sorcery_points": o.Cost, "what_it_does": o.Description})
	}
	// the suggestion: the first of these the Sorcerer doesn't know, then any option
	for _, m := range append([]string{"quickened", "twinned", "careful", "subtle", "extended", "empowered", "distant", "heightened", "seeking", "transmuted"}, slugs...) {
		if _, ok := metamagicOptions[m]; ok && !containsStr(known, m) {
			pick = m
			break
		}
	}
	st := setupStep{Key: "metamagic", Title: "Choose a Metamagic option", Why: "Metamagic. " + classFeatureText("sorcerer", "Metamagic") + " Send one option per request.",
		Choices: map[string]interface{}{"you_know": known, "options": opts}}
	if pick != "" {
		st.Request = req("POST", "/api/characters/metamagic", map[string]interface{}{"character_id": c.id, "metamagic": pick})
	}
	return &st
}

func wildShapeStep(c *choiceChar) *setupStep {
	level := c.levels["druid"]
	known := knownWildShapes(c.id)
	total, _, _ := wildShapeLimits(level)
	st := setupStep{Key: "wild_shape_forms", Title: "Choose your Wild Shape forms",
		Why: "Wild Shape. " + classFeatureText("druid", "Wild Shape") +
			fmt.Sprintf(" You know %d forms now and can know %d: send all of them (the ones you have and the new ones).", len(known), total),
		Choices: map[string]interface{}{"you_know": known, "know_in_all": total, "options": wildShapeOptions(level)}}
	path := fmt.Sprintf("/api/characters/%d/class-options", c.id)
	if forms := suggestWildShapeForms(c.id); len(forms) > 0 {
		st.Request = req("POST", path, map[string]interface{}{"wild_shape_forms": forms})
	} else {
		st.Request = map[string]interface{}{"method": "POST", "path": path, "body_like": `{"wild_shape_forms": [...]} with all of them, from the options`}
	}
	return &st
}

func primalKnowledgeStep(c *choiceChar) *setupStep {
	held, _ := heldSkills(c.id)
	list := classSkillChoices2024["barbarian"].Skills // "the skill list available to Barbarians at level 1"
	pick := ""
	for _, s := range append([]string{"perception", "athletics", "survival", "intimidation", "nature", "animal handling"}, list...) {
		if pick == "" && containsStr(list, s) && !held[s] {
			pick = s
		}
	}
	return &setupStep{Key: "primal_knowledge", Title: "Choose your Primal Knowledge skill", Why: classFeatureText("barbarian", "Primal Knowledge"),
		Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", c.id), map[string]string{"primal_knowledge": pick}),
		Choices: map[string]interface{}{"options": skillChoices(list, held)}}
}

func bonusProficienciesStep(c *choiceChar) *setupStep {
	held, _ := heldSkills(c.id)
	var picks []string
	for _, s := range append([]string{"perception", "stealth", "arcana", "history", "investigation", "insight", "persuasion", "deception", "athletics", "acrobatics"}, game.SkillNames()...) {
		if len(picks) < 3 && !held[s] && !containsStr(picks, s) {
			picks = append(picks, s)
		}
	}
	return &setupStep{Key: "bonus_proficiencies", Title: "Choose your three Bonus Proficiencies",
		Why:     "College of Lore, level 3: Bonus Proficiencies. " + subclassFeatureText("lore", "Bonus Proficiencies"),
		Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", c.id), map[string]interface{}{"bonus_proficiencies": picks}),
		Choices: map[string]interface{}{"choose": 3, "options": skillChoices(game.SkillNames(), held)}}
}

// autoEquipStep: the question asked once, whether the server should put the character's gear on (auto_equip_2024.go).
func autoEquipStep(c *choiceChar) *setupStep {
	return &setupStep{Key: "auto_equip", Title: "Put on your gear and arm your weapons",
		Why:     "Do you want me to equip everything for you? I will put on your armor, take your weapon (and a Shield or second Light weapon) in hand and wear your clothing and jewelry. If you have a choice to make (more weapons than hands, two items for the same body place) I will ask you which. GET /api/characters/auto-equip?character_id=N shows what I would do first.",
		Request: req("POST", "/api/characters/auto-equip", autoEquipSuggestion(c.id))}
}

// lessonsFeatStep: an Origin feat from Lessons of the First Ones not taken yet. The options are the Origin feats the feat request
// would accept (featsOpenTo), each with its request; the suggestion is Alert, which takes no choices.
func lessonsFeatStep(c *choiceChar) *setupStep {
	taker, _ := featTakerOf(c.id)
	var opts []map[string]interface{}
	suggest := ""
	for _, slug := range featsOpenTo(taker) {
		f := availableFeats[slug]
		if f.Category != "Origin" {
			continue
		}
		o := map[string]interface{}{"feat": slug, "name": f.Name, "what_it_does": strings.Join(f.Benefits, " "), "how_to_take": featHowTo(c.id, slug)}
		if f.ChoicesWhenTaken != "" {
			o["choices"] = "send choices: " + f.ChoicesWhenTaken
		} else if suggest == "" || slug == "alert" {
			suggest = slug
		}
		opts = append(opts, o)
	}
	st := setupStep{Key: "lessons_feat", Title: "Choose your Origin feat (Lessons of the First Ones)",
		Why:     "Lessons of the First Ones. " + strings.TrimPrefix(game.AvailableInvocations["lessons-of-the-first-ones"].Description, "Prerequisite: Level 2+ Warlock ") + " It costs no Ability Score Improvement points.",
		Choices: map[string]interface{}{"origin_feats": opts}}
	if suggest != "" {
		st.Request = req("POST", fmt.Sprintf("/api/characters/%d/feat", c.id), map[string]interface{}{"feat": suggest})
	}
	return &st
}
