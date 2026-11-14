package main

// Which spells a character can cast (SRD 5.2.1): a character casts the spells it has prepared (and its cantrips), plus spells that
// a feature, its origin or an item gives it. Before this check the cast action accepted any spell of the SRD.
//
//	cantrips          the character's cantrips (known_spells), species and Magic Initiate cantrips
//	level 1+          the prepared list: prepared_spells (Cleric, Druid, Paladin, Wizard) or known_spells (Bard, Ranger, Sorcerer,
//	                  Warlock, whose list lives there); always-prepared subclass spells; species and Magic Initiate spells
//	features          a Wizard's ritual from the spellbook; Magical Secrets; Mystic Arcanum; Spell Mastery and Signature Spells;
//	                  spells of Eldritch Invocations; the spells a class always has prepared (Hunter's Mark for a Ranger, Find Steed
//	                  for a Paladin of level 5, Contact Other Plane for a Warlock of level 9, Summon Dragon for a Sorcerer of level
//	                  18, Find Familiar for a Druid's Wild Companion); Divine Intervention (any Cleric spell of level 5 or lower)
//	items             a magic item the character carries whose text names the spell (Staff of Power, wands ...), a Spell Scroll of it
// A character with levels in several classes may cast from any list it has (known or prepared).

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func jsonList(raw []byte) []string {
	var out []string
	json.Unmarshal(raw, &out)
	return out
}

// spellAvailable says whether the character can cast the spell now (itself, or from a magic item or Spell Scroll it carries);
// why explains a refusal.
func spellAvailable(charID int, spellKey, description string) (bool, string) {
	ok, why := spellAvailableNatively(charID, spellKey, description)
	if ok || db == nil {
		return ok, why
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []invItem
	json.Unmarshal(raw, &inv)
	if charged, scrolls := itemCastOptions(inv, spellKey); len(charged) > 0 || len(scrolls) > 0 {
		return true, ""
	}
	return false, why
}

// spellAvailableNatively: the character can cast the spell without an item (prepared, a cantrip, a feature or its origin).
func spellAvailableNatively(charID int, spellKey, description string) (bool, string) {
	sp, ok := srdSpellsMemory[spellKey]
	if !ok || db == nil {
		return true, "" // unknown spells are refused elsewhere
	}
	var class, subclass string
	var level int
	var known, prepared, secrets, arcanum, mastery, signature, choices, levels []byte
	if db.QueryRow(`SELECT COALESCE(class,''), COALESCE(subclass,''), COALESCE(level,1), COALESCE(known_spells,'[]'), COALESCE(prepared_spells,'[]'),
		COALESCE(magical_secrets,'[]'), COALESCE(mystic_arcanum,'{}'), COALESCE(spell_mastery,'[]'), COALESCE(signature_spells,'[]'),
		COALESCE(subclass_choices,'{}'), COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).
		Scan(&class, &subclass, &level, &known, &prepared, &secrets, &arcanum, &mastery, &signature, &choices, &levels) != nil {
		return true, ""
	}
	c := strings.ToLower(class)
	knownL, preparedL := jsonList(known), jsonList(prepared)
	has := func(list []string) bool { return containsStr(list, spellKey) }
	low := strings.ToLower(description)

	if _, isOrigin := originSpellsOf(charID)[spellKey]; isOrigin {
		return true, ""
	}
	classLevels := map[string]int{}
	json.Unmarshal(levels, &classLevels)
	if len(classLevels) > 1 && (has(knownL) || has(preparedL)) {
		return true, "" // multiclass: any list it has
	}
	if sp.Level == 0 {
		if has(knownL) || has(preparedL) {
			return true, ""
		}
	} else {
		switch {
		case listInKnownSpells[c] && has(knownL):
			return true, ""
		case !listInKnownSpells[c] && has(preparedL):
			return true, ""
		case c == "wizard" && sp.IsRitual && ritualWord.MatchString(low) && has(knownL):
			return true, "" // Ritual Adept: a ritual from the spellbook without preparing it
		}
	}
	sc := map[string]string{}
	json.Unmarshal(choices, &sc)
	if containsStr(getDomainSpells(subclass, level, sc["circle_land"]), spellKey) || containsStr(wordsOfCreationSpells(c, level), spellKey) || containsStr(magicalDiscoveriesOf(charID), spellKey) {
		return true, "" // subclass spells; Words of Creation (Bard 20)
	}
	if containsStr(featureAlwaysPrepared(charID), spellKey) {
		return true, "" // Druidic's Speak with Animals, Favored Enemy's Hunter's Mark ...; a Warlock's Book of Shadows
	}
	if has(jsonList(secrets)) || has(jsonList(mastery)) || has(jsonList(signature)) {
		return true, ""
	}
	arc := map[string]string{}
	json.Unmarshal(arcanum, &arc)
	for _, s := range arc {
		if s == spellKey {
			return true, ""
		}
	}
	if c == "warlock" {
		if _, ok := getOncePerRestInvocationForSpell(charID, spellKey); ok {
			return true, ""
		}
		if _, _, ok := getAtWillInvocationForSpell(charID, spellKey); ok {
			return true, ""
		}
		if spellKey == "find-familiar" && hasPactBoon(charID, "chain") {
			return true, ""
		}
	}
	switch {
	case c == "ranger" && spellKey == "hunters-mark",
		c == "paladin" && spellKey == "find-steed" && level >= 5,
		c == "warlock" && spellKey == "contact-other-plane" && level >= 9,
		c == "sorcerer" && spellKey == "summon-dragon" && level >= 18,
		c == "druid" && spellKey == "find-familiar" && level >= 2 && strings.Contains(low, "wild companion"):
		return true, ""
	case c == "cleric" && level >= 10 && strings.Contains(low, "divine intervention") && sp.Level <= 5 && containsStr(getClassSpellList("cleric"), spellKey),
		c == "cleric" && level >= 20 && strings.Contains(low, "divine intervention") && spellKey == "wish": // Greater Divine Intervention
		return true, ""
	}
	where := "prepare it first (POST /api/characters/{id}/prepare)"
	switch {
	case sp.Level == 0:
		where = "it isn't one of your cantrips"
	case listInKnownSpells[c]:
		where = "it isn't on your list of spells (POST /api/characters/{id}/spells or /prepare to change it, when your class allows)"
	case game.SpellSlots(c, level) == nil || len(game.SpellSlots(c, level)) == 0:
		where = "your class doesn't cast spells; a species trait, a feat or a magic item would have to give it"
	}
	return false, fmt.Sprintf("You can't cast %s: %s. Nothing was spent.", sp.Name, where)
}

