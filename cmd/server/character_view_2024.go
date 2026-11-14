package main

// What the character record (GET /api/characters/{id}) shows about the choices made at creation: the Divine or Primal Order, the
// species choices (lineage, ancestry or legacy, size, skill, the Human's feat), Size and Speed, damage Resistances, species traits
// that match the lineage (a Drow's Darkvision is 120 feet), each Origin feat's picks (Magic Initiate: list, spells, spellcasting
// ability, free cast) and the spells Eldritch Invocations grant. Nothing here changes the character.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// characterOriginOf loads the origin record (characters.origin); ok is false for a character made before it existed.
func characterOriginOf(charID int) (*originRecord, bool) {
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw) != nil {
		return nil, false
	}
	return loadOriginRecord(raw)
}

// addOrderView shows the chosen Divine Order (Cleric) and Primal Order (Druid): divine_order / primal_order (null while a Cleric
// or Druid hasn't chosen), and the Divine Order / Primal Order class feature names the chosen order with its SRD text.
func addOrderView(response map[string]interface{}, features []map[string]interface{}, charID int) {
	var divine, primal string
	db.QueryRow(`SELECT COALESCE(divine_order,''), COALESCE(primal_order,'') FROM characters WHERE id = $1`, charID).Scan(&divine, &primal)
	levels := classLevelsOf(charID)
	for _, o := range []struct{ field, feature, chosen, class string }{
		{"divine_order", "Divine Order", strings.ToLower(strings.TrimSpace(divine)), "cleric"},
		{"primal_order", "Primal Order", strings.ToLower(strings.TrimSpace(primal)), "druid"},
	} {
		if o.chosen == "" && levels[o.class] == 0 {
			continue
		}
		response[o.field] = nilIfEmpty(o.chosen)
		if o.chosen == "" {
			continue
		}
		for _, f := range features {
			if name, _ := f["name"].(string); strings.HasPrefix(name, o.feature) {
				f["chosen"] = titleWords(o.chosen)
				if text := srdOrders[o.field][o.chosen]; text != "" {
					f["chosen_text"] = text
				}
			}
		}
	}
}

// speciesChoiceSource names the species with its main choice: "Elf (Drow lineage)", "Tiefling (Infernal legacy)",
// "Dragonborn (Red dragon ancestry)", "Gnome (Forest Gnome)".
func speciesChoiceSource(sp *game.SpeciesResult) string {
	c := sp.Choices
	switch {
	case sp.Key == "gnome" && c["lineage"] != "":
		return sp.Name + " (" + titleWords(c["lineage"]) + " Gnome)"
	case c["lineage"] != "":
		return sp.Name + " (" + titleWords(c["lineage"]) + " lineage)"
	case c["legacy"] != "":
		return sp.Name + " (" + titleWords(c["legacy"]) + " legacy)"
	case sp.Key == "dragonborn" && c["ancestry"] != "":
		return sp.Name + " (" + titleWords(c["ancestry"]) + " dragon ancestry)"
	case sp.Key == "goliath" && c["ancestry"] != "":
		return sp.Name + " (" + titleWords(c["ancestry"]) + " giant ancestry)"
	}
	return sp.Name
}

// resolvedSpeciesTraits: the species traits with the player's choices in them. The Darkvision trait gives the character's own
// range (a Drow has 120 feet, not the Elf's 60).
func resolvedSpeciesTraits(sp *game.SpeciesResult) []game.Trait {
	base := game.GetSpecies(sp.Key)
	out := make([]game.Trait, 0, len(sp.Traits))
	for _, t := range sp.Traits {
		if t.Name == "Darkvision" && sp.Darkvision > 0 {
			t.Description = fmt.Sprintf("You have Darkvision with a range of %d feet.", sp.Darkvision)
			if base != nil && base.Darkvision > 0 && base.Darkvision != sp.Darkvision {
				t.Description += fmt.Sprintf(" Your %s raises it from %d feet.", strings.TrimSuffix(strings.TrimPrefix(speciesChoiceSource(sp), sp.Name+" ("), ")"), base.Darkvision)
			}
		}
		out = append(out, t)
	}
	return out
}

// addSpeciesView shows size, speed, the species choices and the species traits as the character has them.
func addSpeciesView(response map[string]interface{}, charID int, species string, rec *originRecord, hasOrigin bool) {
	size, baseSpeed := game.GetSpeciesSize(species), getMovementSpeed(species)
	if hasOrigin {
		size, baseSpeed = rec.Species.Size, rec.Species.Speed
	}
	if speciesEffectActive(charID, "large_form") {
		size = "Large"
	}
	response["size"] = size
	speed := characterSpeed(charID, species) // armor, class features, Longstrider, exhaustion, Wild Shape (armor_rules.go)
	response["speed"] = speed
	if speed != baseSpeed {
		response["species_speed"] = baseSpeed
	}
	if !hasOrigin {
		return
	}
	response["species_traits"] = resolvedSpeciesTraits(rec.Species)
	choices := map[string]string{}
	for k, v := range rec.Species.Choices {
		choices[k] = v
	}
	response["species_choices"] = choices
	for _, k := range []string{"lineage", "legacy", "ancestry"} {
		if v := rec.Species.Choices[k]; v != "" {
			response[k] = v
		}
	}
}

// resistancesView: the damage Resistances the character has all the time, each with where it comes from: the species (Dwarf;
// Dragonborn ancestry; Tiefling legacy), Elemental Affinity (Draconic Sorcerer 6), Fiendish Resilience (Fiend Warlock 10), and
// Rage while it lasts.
func resistancesView(charID int, species string, rec *originRecord, hasOrigin bool) []map[string]string {
	out := []map[string]string{}
	seen := map[string]bool{}
	add := func(t, from string) {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, map[string]string{"damage_type": t, "from": from})
		}
	}
	if hasOrigin {
		for _, r := range rec.Species.Resistances {
			add(r, speciesChoiceSource(rec.Species))
		}
		if rec.Species.Key == "dwarf" {
			add("poison", "Dwarf (Dwarven Resilience)") // origin records made before the species listed it (hasSpeciesResistance)
		}
	} else {
		var draconicAncestry string
		db.QueryRow(`SELECT COALESCE(draconic_ancestry,'') FROM characters WHERE id = $1`, charID).Scan(&draconicAncestry)
		switch {
		case game.HasDwarvenResilience(species):
			add("poison", "Dwarf (Dwarven Resilience)")
		case game.HasHellishResistance(species):
			add("fire", titleWords(species))
		case game.IsDragonborn(species) && draconicAncestry != "":
			if a := game.GetDragonAncestry(draconicAncestry); a != nil {
				add(a.DamageType, "Dragonborn ("+titleWords(draconicAncestry)+" dragon ancestry)")
			}
		}
	}
	for _, t := range []string{"acid", "cold", "fire", "lightning", "poison"} {
		if elementalAffinityResists(charID, t) {
			add(t, "Elemental Affinity")
		}
	}
	if fr := getFiendishResilience(charID); fr != "" {
		add(fr, "Fiendish Resilience")
	}
	if rageResists(charID, "bludgeoning") {
		for _, t := range []string{"bludgeoning", "piercing", "slashing"} {
			add(t, "Rage")
		}
	}
	return out
}

// addOriginFeatPicks adds each Origin feat's picks to its entry in the feats list, in order (a Human whose background also gives
// Magic Initiate shows both): where it comes from; for Magic Initiate the list, cantrips, level 1 spell, spellcasting ability and
// the free cast; for Skilled the proficiencies.
func addOriginFeatPicks(feats []map[string]interface{}, charID int, rec *originRecord) {
	used := make([]bool, len(rec.Feats))
	uses := loadSpeciesUses(charID)
	bgName := titleWords(rec.Background)
	if bg := game.GetBackground(rec.Background); bg != nil {
		bgName = bg.Name
	}
	for _, fe := range feats {
		slug := strings.ReplaceAll(fmt.Sprint(fe["slug"]), "-", "_")
		for i, of := range rec.Feats {
			if used[i] || of.Slug != slug {
				continue
			}
			used[i] = true
			fe["source"] = bgName + " background"
			if of.Source == "species" {
				fe["source"] = rec.Species.Name + " (Versatile)"
			}
			switch of.Slug {
			case "magic_initiate":
				ability := strings.ToUpper(of.Ability)
				fe["list"], fe["cantrips"], fe["spell"], fe["spellcasting_ability"] = of.List, of.Cantrips, of.Spell, ability
				chosen := fmt.Sprintf("%s list; cantrips %s", titleWords(of.List), skillNames(of.Cantrips))
				if of.Spell != "" {
					left := 1 - uses.Used["magic_initiate:"+of.Spell]
					if left < 0 {
						left = 0
					}
					fe["free_cast"] = map[string]interface{}{"spell": of.Spell, "per_long_rest": 1, "left": left}
					chosen += fmt.Sprintf("; level 1 spell %s (once per Long Rest without a spell slot)", originSpellName(of.Spell))
				}
				fe["chosen"] = chosen + "; spellcasting ability " + ability
			case "skilled":
				fe["proficiencies"] = of.Proficiencies
				fe["chosen"] = profNames(of.Proficiencies)
			}
			break
		}
	}
}

// originSpellsView: the spells from the species and the Origin feats, each with its source, spellcasting ability, save DC, attack
// bonus and free casts left. A character without class spellcasting (a Fighter with Magic Initiate) sees them here too.
func originSpellsView(charID int, rec *originRecord, class, classAbility string, level, intl, wis, cha, classMod int) []map[string]interface{} {
	info := originSpellsInfo(charID)
	mods := map[string]int{"INT": game.Modifier(intl), "WIS": game.Modifier(wis), "CHA": game.Modifier(cha)}
	for _, e := range info {
		slug, _ := e["slug"].(string)
		if e["source"] == "Magic Initiate" && rec != nil { // which Magic Initiate, when the background and the Human feat both give one
			for _, f := range rec.Feats {
				if f.Slug == "magic_initiate" && (f.Spell == slug || containsStr(f.Cantrips, slug)) {
					e["source"] = "Magic Initiate (" + titleWords(f.List) + " list)"
					break
				}
			}
		}
		ab, _ := e["spellcasting_ability"].(string)
		mod, _ := originSpellMod(charID, slug, strings.ToLower(class), intl, wis, cha, classMod)
		if m, ok := mods[ab]; ok && mod > m && classAbility != "" {
			e["cast_with_class_ability"] = classAbility // the class list has the spell and the class ability is better
		}
		e["spell_save_dc"] = game.SpellSaveDC(level, mod) + magicSpellDCBonus(charID)
		e["spell_attack_bonus"] = mod + game.ProficiencyBonus(level) + magicSpellAttackBonus(charID)
	}
	sort.Slice(info, func(i, j int) bool {
		li, _ := info[i]["level"].(int)
		lj, _ := info[j]["level"].(int)
		if li != lj {
			return li < lj
		}
		return fmt.Sprint(info[i]["name"]) < fmt.Sprint(info[j]["name"])
	})
	return info
}

// invocationSpellsView: the spells the character's Eldritch Invocations grant, and how each is cast.
func invocationSpellsView(charID int) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, slug := range getCharacterInvocations(charID) {
		inv, ok := game.AvailableInvocations[slug]
		if !ok {
			continue
		}
		spell, how := "", ""
		switch {
		case slug == "pact-of-the-chain":
			spell, how = "find-familiar", "You know it and cast it as a Magic action without a spell slot."
		case slug == "one-with-shadows":
			spell, how = "invisibility", "On yourself without a spell slot, while you are in Dim Light or Darkness (POST /api/characters/one-with-shadows)."
		case slug == "gift-of-the-depths":
			spell, how = "water-breathing", "Once without a spell slot; again after a Long Rest."
		case inv.Mechanics["at_will_spell"] != "":
			spell, how = inv.Mechanics["at_will_spell"], "At will, without a spell slot."
			if atWillSelfOnlySpells[spell] {
				how = "On yourself, at will, without a spell slot."
			}
		case inv.Mechanics["once_per_rest_spell"] != "":
			spell, how = inv.Mechanics["once_per_rest_spell"], "Once per Long Rest, with a Pact Magic spell slot."
		}
		if spell == "" {
			continue
		}
		e := map[string]interface{}{"slug": spell, "name": originSpellName(spell), "invocation": inv.Name, "invocation_slug": slug, "how": how}
		if sp, ok := srdSpellsMemory[spell]; ok {
			e["level"] = sp.Level
		}
		if slug == "gift-of-the-depths" {
			e["used"] = hasUsedInvocationSpell(charID, slug)
		}
		out = append(out, e)
	}
	return out
}
