package main

// Options and explanations for the level-up choices in GET /api/characters/{id}/setup (Nov 13). Matt: a character is presented with
// its options and their explanations at every level. The rule texts are the SRD 5.2.1's own.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// srdMasteryText: Mastery Properties, as the SRD prints them.
var srdMasteryText = map[string]string{
	"Cleave": "If you hit a creature with a melee attack roll using this weapon, you can make a melee attack roll with the weapon against a second creature within 5 feet of the first that is also within your reach. On a hit, the second creature takes the weapon’s damage, but don’t add your ability modifier to that damage unless that modifier is negative. You can make this extra attack only once per turn.",
	"Graze":  "If your attack roll with this weapon misses a creature, you can deal damage to that creature equal to the ability modifier you used to make the attack roll. This damage is the same type dealt by the weapon, and the damage can be increased only by increasing the ability modifier.",
	"Nick":   "When you make the extra attack of the Light property, you can make it as part of the Attack action instead of as a Bonus Action. You can make this extra attack only once per turn.",
	"Push":   "If you hit a creature with this weapon, you can push the creature up to 10 feet straight away from yourself if it is Large or smaller.",
	"Sap":    "If you hit a creature with this weapon, that creature has Disadvantage on its next attack roll before the start of your next turn.",
	"Slow":   "If you hit a creature with this weapon and deal damage to it, you can reduce its Speed by 10 feet until the start of your next turn. If the creature is hit more than once by weapons that have this property, the Speed reduction doesn’t exceed 10 feet.",
	"Topple": "If you hit a creature with this weapon, you can force the creature to make a Constitution saving throw (DC 8 plus the ability modifier used to make the attack roll and your Proficiency Bonus). On a failed save, the creature has the Prone condition.",
	"Vex":    "If you hit a creature with this weapon and deal damage to the creature, you have Advantage on your next attack roll against that creature before the end of your next turn.",
}

const srdASIFeat = "Ability Score Improvement (General Feat; Prerequisite: Level 4+). Increase one ability score of your choice by 2, or increase two ability scores of your choice by 1. This feat can’t increase an ability score above 20. Repeatable. You can take this feat more than once."

// classFeatureText: the SRD text of a class feature by name ("" if the class has none by that name).
func classFeatureText(class, name string) string {
	for _, f := range game.GetActiveClassFeatures(class, 20) {
		if strings.EqualFold(f.Name, name) {
			return f.Description
		}
	}
	return ""
}

var recommendedBoon = regexp.MustCompile(`(Boon\s+of\s+(?:the\s+)?[A-Z][A-Za-z]+(?:\s+[A-Z][A-Za-z]+)*)\s+is\s+recommended`)

// asiSetupStep: unspent Ability Score Improvement points, with both ways to spend them and every feat the character qualifies for.
func asiSetupStep(charID int) *setupStep {
	var pending, level, str, dex, con, intl, wis, cha int
	var class string
	var featsJSON []byte
	if db.QueryRow(`SELECT COALESCE(pending_asi,0), level, COALESCE(class,''), str, dex, con, intl, wis, cha, COALESCE(feats,'[]') FROM characters WHERE id = $1`, charID).
		Scan(&pending, &level, &class, &str, &dex, &con, &intl, &wis, &cha, &featsJSON) != nil || pending <= 0 {
		return nil
	}
	cl := strings.ToLower(class)
	var have []string
	json.Unmarshal(featsJSON, &have)
	scores := map[string]int{"str": str, "dex": dex, "con": con, "int": intl, "wis": wis, "cha": cha}
	epic := level >= 19
	feature := "Ability Score Improvement"
	if epic {
		feature = "Epic Boon"
	}
	// the feats the character qualifies for
	type featOpt struct {
		slug string
		m    map[string]interface{}
	}
	var feats []featOpt
	for slug, f := range availableFeats {
		if f.Category == "Origin" || f.Category == "Fighting Style" || slug == "ability_score_improvement" {
			continue // Origin feats come with a background; Fighting Style feats with that feature; the ASI is option 1
		}
		if f.Category == "Epic Boon" && !epic {
			continue
		}
		if ok, _ := featPrerequisite(f.Prerequisite, cl, level, str, dex); !ok {
			continue
		}
		if containsStr(have, slug) && !f.Repeatable {
			continue
		}
		m := map[string]interface{}{"feat": slug, "name": f.Name, "category": f.Category, "prerequisite": featPrereqText(f.Prerequisite), "what_it_does": strings.Join(f.Benefits, " ")}
		if len(f.AbilityChoices) > 0 {
			m["ability_choice"] = fmt.Sprintf("send ability_choice: one of %s (the +%d increase, up to %d)", strings.Join(f.AbilityChoices, ", "), max1(f.AbilityPoints), maxScoreOr20(f.MaxScore))
		}
		feats = append(feats, featOpt{slug, m})
	}
	sort.Slice(feats, func(i, j int) bool {
		if (feats[i].m["category"] == "Epic Boon") != (feats[j].m["category"] == "Epic Boon") {
			return feats[i].m["category"] == "Epic Boon"
		}
		return feats[i].slug < feats[j].slug
	})
	var featList []map[string]interface{}
	for _, f := range feats {
		featList = append(featList, f.m)
	}
	// the suggestion: an Epic Boon the SRD recommends at 19; otherwise the class's primary ability (up to 20), then Constitution
	order := append(append([]string{}, classPrimary[cl]...), "con", "dex", "wis", "str", "cha", "int")
	st := setupStep{Key: "asi_or_feat", Title: "Spend your " + feature,
		Why: fmt.Sprintf("You have %d Ability Score Improvement point(s) to spend (2 for each %s). %s Either raise your ability scores (option 1, POST /api/characters/%d/asi {\"ability\": \"...\", \"points\": 1 or 2}, as many times as you have points), or take one feat you qualify for, which costs 2 points (option 2, POST /api/characters/%d/feat {\"feat\": \"...\"}).",
			pending, feature, classFeatureText(cl, feature), charID, charID)}
	choices := map[string]interface{}{
		"points_to_spend":                    pending,
		"your_ability_scores_now":            scores,
		"option_1_ability_score_improvement": srdASIFeat,
		"option_2_a_feat_you_qualify_for":    featList,
		"for_your_class":                     abilityGuidance(cl),
	}
	if epic {
		if m := recommendedBoon.FindStringSubmatch(classFeatureText(cl, "Epic Boon")); m != nil {
			name := strings.Join(strings.Fields(m[1]), " ")
			m[1] = name
			slug := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
			if f, ok := availableFeats[slug]; ok && pending >= 2 {
				body := map[string]interface{}{"feat": slug}
				if len(f.AbilityChoices) > 0 {
					pick := f.AbilityChoices[0]
					for _, a := range order {
						if containsStr(f.AbilityChoices, a) {
							pick = a
							break
						}
					}
					body["ability_choice"] = pick
				}
				st.Request = req("POST", fmt.Sprintf("/api/characters/%d/feat", charID), body)
				choices["suggested"] = m[1] + " (the SRD recommends it for your class)"
			}
		}
	}
	if st.Request == nil {
		for _, a := range order {
			if scores[a] < 20 {
				p := pending
				if p > 2 {
					p = 2
				}
				if 20-scores[a] < p {
					p = 20 - scores[a]
				}
				st.Request = req("POST", fmt.Sprintf("/api/characters/%d/asi", charID), map[string]interface{}{"ability": a, "points": p})
				choices["suggested"] = fmt.Sprintf("+%d %s (%s)", p, abilityFullNames[a], ifStr(containsStr(classPrimary[cl], a), "your class's primary ability"))
				break
			}
		}
	}
	st.Choices = choices
	return &st
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func maxScoreOr20(n int) int {
	if n == 0 {
		return 20
	}
	return n
}

// masterySetupStep: more kinds of weapon to choose for Weapon Mastery, each option with its Mastery property's SRD text.
func masterySetupStep(charID int, class string, level int) *setupStep {
	want := 0
	for c, l := range classLevelsOrSingle(charID) {
		if n := masteryCount(c, l); n > want {
			want = n
		}
	}
	cur := jsonListOf(charID, "weapon_masteries")
	if len(cur) >= want {
		return nil
	}
	cl := strings.ToLower(class)
	var opts []map[string]interface{}
	var carried []string
	for _, it := range inventoryOf(charID) {
		carried = append(carried, strings.ToLower(itemStr(it, "name")))
	}
	pick := ""
	for _, w := range eligibleMasteryWeapons(cl) {
		if containsStr(cur, w.Key) {
			continue
		}
		o := map[string]interface{}{"weapon": w.Name, "mastery": w.Mastery, "mastery_does": srdMasteryText[w.Mastery]}
		if containsStr(carried, strings.ToLower(w.Name)) {
			o["you_carry_one"] = true
			if pick == "" {
				pick = w.Name
			}
		}
		opts = append(opts, o)
	}
	if pick == "" && len(opts) > 0 {
		pick = fmt.Sprint(opts[0]["weapon"])
	}
	var names []string
	for _, k := range cur {
		if w, ok := srdWeapons[k]; ok {
			names = append(names, titleWords(w.Name))
		} else {
			names = append(names, k)
		}
	}
	st := setupStep{Key: "weapon_mastery", Title: fmt.Sprintf("Choose %d more kind(s) of weapon for Weapon Mastery", want-len(cur)),
		Why: fmt.Sprintf("%s You can use the mastery property of %d kinds of weapon; you have chosen %d (%s). Send the whole list, the ones you have and the new ones.",
			classFeatureText(cl, "Weapon Mastery"), want, len(cur), strings.Join(names, ", "))}
	st.Choices = map[string]interface{}{"you_have": names, "choose_this_many_more": want - len(cur), "options": opts}
	if pick != "" {
		st.Request = req("POST", "/api/characters/weapon-masteries", map[string]interface{}{"character_id": charID, "weapon_masteries": append(append([]string{}, names...), pick)})
	}
	return &st
}

// subclassChoices: every subclass the class can take, with its SRD introduction and features by level.
func subclassChoices(class string) map[string]interface{} {
	var opts []map[string]interface{}
	for slug, s := range game.GetSubclassesForClass(strings.ToLower(class)) {
		var feats []map[string]interface{}
		for _, f := range s.Features {
			feats = append(feats, map[string]interface{}{"level": f.Level, "name": f.Name, "what_it_does": f.Description})
		}
		opts = append(opts, map[string]interface{}{"subclass": slug, "name": s.Name, "what_it_is": s.Description, "features": feats})
	}
	return map[string]interface{}{"options": opts}
}

// skillChoices: skill options (not already held) with the SRD's Skills table text.
func skillChoices(list []string, held map[string]bool) []map[string]string {
	var out []map[string]string
	for _, s := range list {
		if held[s] {
			continue
		}
		row := srdSkillRows[s]
		out = append(out, map[string]string{"skill": s, "ability": row[0], "example_uses": row[1]})
	}
	return out
}

// skillOrder: the 18 skills in the SRD's order.
var skillOrder = []string{"acrobatics", "animal handling", "arcana", "athletics", "deception", "history", "insight", "intimidation", "investigation",
	"medicine", "nature", "perception", "performance", "persuasion", "religion", "sleight of hand", "stealth", "survival"}

// pendingClassChoices: one-time choices of a class or subclass feature that have no step elsewhere (Nov 13): Blessed Strikes (Cleric 7),
// Elemental Fury (Druid 7), Elemental Affinity (Draconic Sorcerer 6). Each with the feature's SRD text and its options.
type classChoiceDef struct {
	class, subclass string
	level           int
	key, feature    string
	options         []string
	suggest         string
}

var classChoiceDefs = []classChoiceDef{
	{"cleric", "", 7, "blessed_strikes", "Blessed Strikes", []string{"divine_strike", "potent_spellcasting"}, "potent_spellcasting"},
	{"druid", "", 7, "elemental_fury", "Elemental Fury", []string{"potent_spellcasting", "primal_strike"}, "potent_spellcasting"},
	{"sorcerer", "draconic", 6, "elemental_affinity", "Elemental Affinity", []string{"acid", "cold", "fire", "lightning", "poison"}, "fire"},
}

func pendingClassChoices(charID int) []setupStep {
	class, sub, level := subclassOf(charID)
	var raw []byte
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	have := map[string]string{}
	json.Unmarshal(raw, &have)
	var out []setupStep
	for _, d := range classChoiceDefs {
		if !strings.EqualFold(class, d.class) || level < d.level || (d.subclass != "" && !strings.EqualFold(sub, d.subclass)) || have[d.key] != "" {
			continue
		}
		text := classFeatureText(d.class, d.feature)
		if text == "" && d.subclass != "" {
			for _, f := range game.GetActiveSubclassFeatures(d.subclass, level) {
				if f.Name == d.feature {
					text = f.Description
				}
			}
		}
		out = append(out, setupStep{Key: d.key, Title: "Choose your " + d.feature,
			Why:     d.feature + ". " + text,
			Choices: map[string]interface{}{"options": d.options},
			Request: req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]string{d.key: d.suggest})})
	}
	return out
}

// wildShapeOptions: the Beasts a Druid of this level can know, with what matters for choosing one.
func wildShapeOptions(level int) []map[string]interface{} {
	_, maxCR, fly := wildShapeLimits(level)
	rows, err := db.Query(`SELECT slug, name, COALESCE(size,''), cr, ac, hp, COALESCE(speed_text,'') FROM monsters WHERE LOWER(type) = 'beast' ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var slug, name, size, cr, speed string
		var ac, hp int
		if rows.Scan(&slug, &name, &size, &cr, &ac, &hp, &speed) != nil || crValue(cr) > maxCR || (!fly && strings.Contains(strings.ToLower(speed), "fly")) {
			continue
		}
		out = append(out, map[string]interface{}{"beast": slug, "name": name, "size": size, "cr": cr, "ac": ac, "hp": hp, "speed": speed})
	}
	return out
}

// subclassFeatureText: a subclass feature's SRD text ("" if none). Names compare without the curly apostrophe.
func subclassFeatureText(subclass, name string) string {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "’", "'")) }
	for _, f := range game.GetActiveSubclassFeatures(subclass, 20) {
		if norm(f.Name) == norm(name) {
			return f.Description
		}
	}
	return ""
}

// mysticArcanumStep: a Warlock of level 11+ with a Mystic Arcanum spell level not yet chosen (Nov 13: never asked before).
func mysticArcanumStep(charID int) *setupStep {
	class, _, level := subclassOf(charID)
	if !strings.EqualFold(class, "warlock") || level < 11 {
		return nil
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(mystic_arcanum,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	cur := map[string]string{}
	json.Unmarshal(raw, &cur)
	for _, sl := range getAvailableMysticArcanumLevels(level) {
		if cur[fmt.Sprint(sl)] != "" {
			continue
		}
		var opts []map[string]interface{}
		pick := ""
		for _, k := range getClassSpellList("warlock") {
			if sp, ok := srdSpellsMemory[k]; ok && sp.Level == sl {
				opts = append(opts, spellOptionInfo(k, map[string]bool{}))
				if pick == "" {
					pick = k
				}
			}
		}
		st := setupStep{Key: "mystic_arcanum", Title: fmt.Sprintf("Choose your level %d Mystic Arcanum spell", sl),
			Why:     "Mystic Arcanum. " + classFeatureText("warlock", "Mystic Arcanum"),
			Choices: map[string]interface{}{"spell_level": sl, "you_have": cur, "options": opts}}
		if pick != "" {
			st.Request = req("POST", "/api/characters/mystic-arcanum", map[string]interface{}{"character_id": charID, "spell_level": sl, "spell_slug": pick})
		}
		return &st
	}
	return nil
}

// invocationOptions: the Eldritch Invocations this Warlock qualifies for and doesn't know, with their SRD text and prerequisites.
func invocationOptions(charID int) []map[string]interface{} {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(eldritch_invocations,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	known := jsonList(raw)
	var slugs []string
	for slug := range game.AvailableInvocations {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	var out []map[string]interface{}
	for _, slug := range slugs {
		inv := game.AvailableInvocations[slug]
		if containsStr(known, slug) {
			continue
		}
		if ok, _ := meetsInvocationPrerequisites(charID, inv); !ok {
			continue
		}
		o := map[string]interface{}{"invocation": slug, "name": inv.Name, "what_it_does": inv.Description}
		p := inv.Prerequisites
		var pre []string
		if p.Level > 0 {
			pre = append(pre, fmt.Sprintf("Level %d+ Warlock", p.Level))
		}
		if p.Pact != "" {
			pre = append(pre, "Pact of the "+titleCase(p.Pact))
		}
		if p.RequiresSpell != "" {
			pre = append(pre, "a Warlock cantrip that deals damage ("+p.RequiresSpell+")")
		}
		if len(pre) > 0 {
			o["prerequisite"] = strings.Join(pre, "; ")
		}
		out = append(out, o)
	}
	return out
}

// wizardCapstoneSteps: Spell Mastery (Wizard 18) and Signature Spells (Wizard 20), chosen from the spellbook (Nov 13: never asked before).
func wizardCapstoneSteps(charID int) []setupStep {
	class, _, level := subclassOf(charID)
	if !strings.EqualFold(class, "wizard") || level < 18 {
		return nil
	}
	var bookRaw, masteryRaw, sigRaw []byte
	db.QueryRow(`SELECT COALESCE(known_spells,'[]'), COALESCE(spell_mastery,'[]'), COALESCE(signature_spells,'[]') FROM characters WHERE id = $1`, charID).Scan(&bookRaw, &masteryRaw, &sigRaw)
	book := jsonList(bookRaw)
	var out []setupStep
	bookOf := func(lv int, actionOnly bool) ([]map[string]interface{}, string) {
		var opts []map[string]interface{}
		first := ""
		for _, k := range book {
			sp, ok := srdSpellsMemory[k]
			if !ok || sp.Level != lv || (actionOnly && !strings.EqualFold(strings.TrimSpace(sp.CastingTime), "action") && !strings.HasPrefix(strings.ToLower(sp.CastingTime), "1 action")) {
				continue
			}
			opts = append(opts, spellOptionInfo(k, map[string]bool{}))
			if first == "" {
				first = k
			}
		}
		return opts, first
	}
	if len(jsonList(masteryRaw)) < 2 {
		o1, f1 := bookOf(1, true)
		o2, f2 := bookOf(2, true)
		st := setupStep{Key: "spell_mastery", Title: "Choose your Spell Mastery spells", Why: "Spell Mastery. " + classFeatureText("wizard", "Spell Mastery"),
			Choices: map[string]interface{}{"level_1_options_from_your_spellbook": o1, "level_2_options_from_your_spellbook": o2}}
		if f1 != "" && f2 != "" {
			st.Request = req("POST", "/api/characters/spell-mastery", map[string]interface{}{"character_id": charID, "spells": []string{f1, f2}})
		}
		out = append(out, st)
	}
	if level >= 20 {
		sig := jsonList(sigRaw)
		if len(sig) < 2 {
			o3, _ := bookOf(3, false)
			pick := ""
			for _, o := range o3 {
				if k := fmt.Sprint(o["spell"]); !containsStr(sig, k) {
					pick = k
					break
				}
			}
			st := setupStep{Key: "signature_spells", Title: "Choose your Signature Spells", Why: "Signature Spells. " + classFeatureText("wizard", "Signature Spells") + " Send one spell per request.",
				Choices: map[string]interface{}{"you_have": sig, "level_3_options_from_your_spellbook": o3}}
			if pick != "" {
				st.Request = req("POST", "/api/characters/signature-spells", map[string]interface{}{"character_id": charID, "action": "choose", "spell": pick})
			}
			out = append(out, st)
		}
	}
	return out
}
