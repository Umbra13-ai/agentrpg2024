package main

// College of Lore, level 6: Magical Discoveries (SRD 5.2.1), Nov 13. "You learn two spells of your choice. These spells can come from the
// Cleric, Druid, or Wizard spell list or any combination thereof. A spell you choose must be a cantrip or a spell for which you have
// spell slots, as shown in the Bard Features table. You always have the chosen spells prepared, and whenever you gain a Bard level, you
// can replace one of the spells with another spell that meets these requirements."
// Set with POST /api/characters/{id}/class-options {"magical_discoveries": ["spell", "spell"]}; kept in the magical_discoveries column.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

var magicalDiscoveriesOnce sync.Once

func ensureMagicalDiscoveries() {
	magicalDiscoveriesOnce.Do(func() {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS magical_discoveries JSONB DEFAULT '[]'`)
	})
}

func magicalDiscoveriesOf(charID int) []string {
	if db == nil {
		return nil
	}
	ensureMagicalDiscoveries()
	var raw []byte
	db.QueryRow(`SELECT COALESCE(magical_discoveries,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var out []string
	json.Unmarshal(raw, &out)
	return out
}

// hasMagicalDiscoveries: a Lore Bard of level 6 or higher.
func hasMagicalDiscoveries(charID int) bool {
	var class, sub string
	var level int
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(subclass,''), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&class, &sub, &level)
	return strings.EqualFold(class, "bard") && strings.EqualFold(sub, "lore") && level >= 6
}

func bardMaxSpellLevel(level int) int {
	m := (level + 1) / 2
	if m > 9 {
		m = 9
	}
	return m
}

const srdMagicalDiscoveries = "You learn two spells of your choice. These spells can come from the Cleric, Druid, or Wizard spell list or any combination thereof (see a class’s section for its spell list). A spell you choose must be a cantrip or a spell for which you have spell slots, as shown in the Bard Features table. You always have the chosen spells prepared, and whenever you gain a Bard level, you can replace one of the spells with another spell that meets these requirements."

// spellsAlreadyHad: the spells a character already has (known, prepared, origin and other always-prepared spells), not counting its
// Magical Discoveries themselves. Magical Discoveries options and the suggestion leave these out, and picking one is refused (setup
// checker, Nov 14: the suggestion named a spell the Bard already had through Magic Initiate).
func spellsAlreadyHad(charID int) map[string]bool {
	have := map[string]bool{}
	for _, k := range jsonListOf(charID, "known_spells") {
		have[k] = true
	}
	for _, k := range jsonListOf(charID, "prepared_spells") {
		have[k] = true
	}
	for k := range originSpellsOf(charID) {
		have[k] = true
	}
	class, sub, level := subclassOf(charID)
	for _, k := range append(append(getDomainSpells(sub, level, subclassChoicesOf(charID)["circle_land"]), originAlwaysPrepared(charID)...), append(featureAlwaysPrepared(charID), wordsOfCreationSpells(strings.ToLower(class), level)...)...) {
		have[k] = true
	}
	for _, k := range magicalDiscoveriesOf(charID) {
		delete(have, k)
	}
	return have
}

// setMagicalDiscoveries checks and saves the two spells ("" on success, else what is wrong).
func setMagicalDiscoveries(charID int, picks []string) string {
	if !hasMagicalDiscoveries(charID) {
		return "Magical Discoveries is a College of Lore feature from Bard level 6."
	}
	var level int
	db.QueryRow(`SELECT COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&level)
	if len(picks) != 2 {
		return fmt.Sprintf("choose exactly 2 spells (you sent %d)", len(picks))
	}
	var keys []string
	had := spellsAlreadyHad(charID)
	for _, p := range picks {
		k := normalizeSpellSlug(strings.NewReplacer("'", "", "’", "").Replace(p))
		sp, ok := srdSpellsMemory[k]
		if !ok {
			if note := phbNote("spell", p); note != "" {
				return note
			}
			return fmt.Sprintf("%q is not a spell on this server (GET /api/universe/spells)", p)
		}
		if !isSpellOnClassList(k, "cleric") && !isSpellOnClassList(k, "druid") && !isSpellOnClassList(k, "wizard") {
			return fmt.Sprintf("%s is not on the Cleric, Druid or Wizard spell list", sp.Name)
		}
		if sp.Level > bardMaxSpellLevel(level) {
			return fmt.Sprintf("%s is a level %d spell; a level %d Bard has spell slots up to level %d", sp.Name, sp.Level, level, bardMaxSpellLevel(level))
		}
		if containsStr(keys, k) {
			return "you chose " + sp.Name + " twice: choose 2 different spells"
		}
		if had[k] {
			return "you already have " + sp.Name + ": choose a spell you don't have"
		}
		keys = append(keys, k)
	}
	b, _ := json.Marshal(keys)
	ensureMagicalDiscoveries()
	db.Exec(`UPDATE characters SET magical_discoveries = $1 WHERE id = $2`, b, charID)
	return ""
}

// magicalDiscoveriesStep: the setup step while fewer than 2 spells are chosen.
func magicalDiscoveriesStep(charID int) *setupStep {
	if !hasMagicalDiscoveries(charID) || len(magicalDiscoveriesOf(charID)) >= 2 {
		return nil
	}
	var level int
	db.QueryRow(`SELECT COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&level)
	maxL := bardMaxSpellLevel(level)
	var opts []map[string]interface{}
	seen := spellsAlreadyHad(charID) // spells the Bard already has aren't options
	for _, list := range []string{"cleric", "druid", "wizard"} {
		for _, k := range getClassSpellList(list) {
			sp, ok := srdSpellsMemory[k]
			if !ok || seen[k] || sp.Level > maxL || isSpellOnClassList(k, "bard") {
				continue
			}
			seen[k] = true
			o := spellOptionInfo(k, map[string]bool{})
			var lists []string
			for _, l := range []string{"cleric", "druid", "wizard"} {
				if isSpellOnClassList(k, l) {
					lists = append(lists, l)
				}
			}
			o["on_the_lists"] = lists
			opts = append(opts, o)
		}
	}
	pick := []string{}
	had := spellsAlreadyHad(charID)
	for _, k := range []string{"guidance", "spirit-guardians", "revivify", "cure-wounds", "shield", "magic-missile"} {
		if sp, ok := srdSpellsMemory[k]; ok && sp.Level <= maxL && len(pick) < 2 && !isSpellOnClassList(k, "bard") && !had[k] {
			pick = append(pick, k)
		}
	}
	for _, o := range opts { // the preferred picks ran out: any option
		if k, _ := o["spell"].(string); k != "" && len(pick) < 2 && !containsStr(pick, k) {
			pick = append(pick, k)
		}
	}
	st := setupStep{Key: "magical_discoveries", Title: "Choose your two Magical Discoveries spells", Why: "College of Lore, level 6: Magical Discoveries. " + srdMagicalDiscoveries +
		fmt.Sprintf(" The options are the Cleric, Druid and Wizard spells that are not already on the Bard list, up to level %d (your highest spell slot).", maxL)}
	st.Choices = map[string]interface{}{"choose": 2, "options": opts}
	st.Request = req("POST", fmt.Sprintf("/api/characters/%d/class-options", charID), map[string]interface{}{"magical_discoveries": pick})
	return &st
}
