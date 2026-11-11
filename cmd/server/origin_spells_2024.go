package main

// Spells from a character's origin (SRD 5.2.1): the species lineages and legacies (Elven Lineage, Fiendish Legacy, Gnomish Lineage)
// and the Magic Initiate feat. They work the same way:
//   - Always prepared: they don't count against the class's Prepared Spells or Cantrips limits, and a list change keeps them.
//   - Their own spellcasting ability: Intelligence, Wisdom or Charisma, chosen with the species or the feat. When the class list also
//     has the spell, the better of the two is used (the player would cast it the better way).
//   - Free casts: an Elf's or Tiefling's level 3 and level 5 spells and the Magic Initiate level 1 spell can each be cast once without
//     a spell slot per Long Rest; a Forest Gnome casts Speak with Animals without a slot a number of times equal to the Proficiency
//     Bonus per Long Rest. Say "free" (or "no slot") in the cast description; a character with no spell slots at all (a Fighter, a
//     Rogue) uses the free cast automatically. The uses are counted in characters.species_uses and come back on a Long Rest.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type originSpell struct {
	Slug    string
	Source  string // "Drow lineage", "Infernal legacy", "Forest Gnome", "Magic Initiate"
	Ability string // int, wis, cha
	FreeMax int    // free casts per Long Rest (0: cantrip, or no free cast)
	FreeKey string // key in species_uses.Used
}

// originSpellsFor lists the origin spells a character of this level has.
func originSpellsFor(rec *originRecord, level int) map[string]originSpell {
	out := map[string]originSpell{}
	if rec == nil {
		return out
	}
	if sp := rec.Species; sp != nil {
		src := sp.Name
		switch {
		case sp.Key == "gnome" && sp.Choices["lineage"] != "":
			src = titleWords(sp.Choices["lineage"]) + " Gnome"
		case sp.Choices["lineage"] != "":
			src = titleWords(strings.ReplaceAll(sp.Choices["lineage"], "_", " ")) + " lineage"
		case sp.Choices["legacy"] != "":
			src = titleWords(sp.Choices["legacy"]) + " legacy"
		}
		ab := strings.ToLower(sp.SpellAbility)
		for _, c := range sp.Cantrips {
			out[c] = originSpell{Slug: c, Source: src, Ability: ab}
		}
		for lvl, list := range sp.LevelSpells {
			if level < lvl {
				continue
			}
			for _, s := range list {
				out[s] = originSpell{Slug: s, Source: src, Ability: ab, FreeMax: 1, FreeKey: "lineage_spell:" + s}
			}
		}
		if sp.Key == "gnome" && sp.Choices["lineage"] == "forest" { // Speak with Animals, Proficiency Bonus times per Long Rest
			out["speak-with-animals"] = originSpell{Slug: "speak-with-animals", Source: "Forest Gnome", Ability: ab,
				FreeMax: game.ProficiencyBonus(level), FreeKey: "lineage_spell:speak-with-animals"}
		}
	}
	for _, f := range rec.Feats {
		if f.Slug != "magic_initiate" {
			continue
		}
		ab := strings.ToLower(f.Ability)
		for _, c := range f.Cantrips {
			if _, have := out[c]; !have {
				out[c] = originSpell{Slug: c, Source: "Magic Initiate", Ability: ab}
			}
		}
		if f.Spell != "" {
			out[f.Spell] = originSpell{Slug: f.Spell, Source: "Magic Initiate", Ability: ab, FreeMax: 1, FreeKey: "magic_initiate:" + f.Spell}
		}
	}
	return out
}

// originSpellsOf loads a character's origin spells.
func originSpellsOf(charID int) map[string]originSpell {
	var raw []byte
	var level int
	if db == nil || db.QueryRow(`SELECT COALESCE(origin,'{}'), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&raw, &level) != nil {
		return map[string]originSpell{}
	}
	rec, ok := loadOriginRecord(raw)
	if !ok {
		return map[string]originSpell{}
	}
	return originSpellsFor(rec, level)
}

// originAlwaysPrepared is the list of origin spell slugs (they never count against the prepared limits).
func originAlwaysPrepared(charID int) []string {
	var out []string
	for s := range originSpellsOf(charID) {
		out = append(out, s)
	}
	return out
}

// originSpellMod is the spellcasting modifier for an origin spell: its own ability, or the class's when the class list has the spell
// and the class ability is better. ok is false when the spell isn't an origin spell.
func originSpellMod(charID int, spellKey, class string, intl, wis, cha, classMod int) (int, bool) {
	os, found := originSpellsOf(charID)[spellKey]
	if !found {
		return classMod, false
	}
	mod := map[string]int{"int": game.Modifier(intl), "wis": game.Modifier(wis), "cha": game.Modifier(cha)}[os.Ability]
	if classMod > mod && class != "" && containsStr(getClassSpellList(class), spellKey) {
		mod = classMod
	}
	return mod, true
}

// hasAnySpellSlots: does the character have spell slots (or Pact Magic slots) at all?
func hasAnySpellSlots(charID int, class string, level int) bool {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	levels := map[string]int{}
	json.Unmarshal(raw, &levels)
	if len(levels) > 1 {
		return len(game.MulticlassSpellSlots(levels)) > 0 || levels["warlock"] > 0 || levels["Warlock"] > 0
	}
	return len(game.SpellSlots(class, level)) > 0
}

// originFreeCast spends a free cast of an origin spell. handled is false when the cast isn't an origin free cast (the normal slot
// rules apply); otherwise note is added to the cast text, or errText refuses the cast (nothing is spent).
func originFreeCast(charID int, class string, level int, spellKey, description string) (note, errText string, handled bool) {
	os, found := originSpellsOf(charID)[spellKey]
	if !found || os.FreeMax == 0 {
		return "", "", false
	}
	asked := wantsFreeCast(description)
	noSlots := !hasAnySpellSlots(charID, class, level)
	if !asked && !noSlots {
		return "", "", false // cast with a spell slot, as the player chose
	}
	u := loadSpeciesUses(charID)
	name := spellKey
	if sp, ok := srdSpellsMemory[spellKey]; ok {
		name = sp.Name
	}
	if u.Used[os.FreeKey] >= os.FreeMax {
		if noSlots {
			return "", fmt.Sprintf("Cannot cast %s: you have no free cast of it left (%s: %d per Long Rest) and no spell slots to cast it with.", name, os.Source, os.FreeMax), true
		}
		return "", fmt.Sprintf("Cannot cast %s free: you have no free cast of it left (%s: %d per Long Rest); finish a Long Rest or cast it with a spell slot.", name, os.Source, os.FreeMax), true
	}
	u.Used[os.FreeKey]++
	saveSpeciesUses(charID, u)
	if os.FreeMax == 1 {
		return fmt.Sprintf(" [%s: no spell slot (once per Long Rest)]", os.Source), "", true
	}
	return fmt.Sprintf(" [%s: no spell slot, %d of %d left until a Long Rest]", os.Source, os.FreeMax-u.Used[os.FreeKey], os.FreeMax), "", true
}

// originSpellsInfo lists the origin spells for the prepare endpoint and the sheet.
func originSpellsInfo(charID int) []map[string]interface{} {
	u := loadSpeciesUses(charID)
	var out []map[string]interface{}
	for slug, os := range originSpellsOf(charID) {
		e := map[string]interface{}{"slug": slug, "name": slug, "source": os.Source, "spellcasting_ability": strings.ToUpper(os.Ability)}
		if sp, ok := srdSpellsMemory[slug]; ok {
			e["name"], e["level"] = sp.Name, sp.Level
		}
		if os.FreeMax > 0 {
			left := os.FreeMax - u.Used[os.FreeKey]
			if left < 0 {
				left = 0
			}
			e["free_casts_left"], e["free_casts_per_long_rest"] = left, os.FreeMax
		}
		out = append(out, e)
	}
	return out
}

// pickCharacterID / pickCampaignID: a player who plays in several campaigns says which character or campaign a request is for
// (?character_id=N or ?campaign_id=N, or the same fields in the request body); without them the newest active campaign is used.
func pickCharacterID(r *http.Request, body int) int {
	if body > 0 {
		return body
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("character_id"))
	return n
}

func pickCampaignID(r *http.Request, body int) int {
	if body > 0 {
		return body
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("campaign_id"))
	return n
}
