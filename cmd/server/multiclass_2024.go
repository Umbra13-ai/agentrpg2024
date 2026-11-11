package main

// Multiclassing (SRD 5.2.1): the rules that depend on the levels in each class, not the total level. Before Nov 8, 2026 most of the
// server treated a multiclass character as its first class at its total level (a Paladin 6 / Sorcerer 12 / Warlock 2 had Paladin 20's
// features, Lay on Hands 100 and Paladin 20's slots, and no Sorcerer or Warlock features). These helpers give each rule the right
// levels; a single-class character gets exactly what it got before.
//   classLevelsOf          the levels in each class ({class: level} for a single class)
//   characterSpellSlots    the Spellcasting slots (Multiclass Spellcaster table, or the one casting class's own table)
//   characterPactSlots     Pact Magic slots of a multiclass Warlock (a single-class Warlock's are its regular table, as before)
//   characterExtraAttacks  attacks per Attack action: the best one class gives (Extra Attack doesn't stack)
//   characterClassFeatures each class's features at that class's level
//   characterMaxResources  each class's resources at that class's level (Focus 13 for a Monk 13, Rage 2 for a Barbarian 2 ...)
//   classResourceMax       one resource's maximum, from the class that has it
//   hitDicePools           Hit Point Dice by die type (d10 x6, d6 x12, d8 x2)

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// ---- whether multiclassing is allowed (the project owner, Nov 8, 2026: off by default; the code stays and works when turned on)
// Multiclassing is an optional rule in the SRD 5.2.1 ("With this rule, you have the option of gaining a level in a new class"). The
// server setting AGENTRPG_MULTICLASS (on/off; off when unset) is the default; a campaign's multiclassing table rule ("allowed" or
// "off"; "server" follows the setting) overrides it. When off, a level in a new class is refused; characters keep the classes they
// have and level in them as before.

func multiclassServerDefault() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTRPG_MULTICLASS"))) {
	case "on", "true", "1", "yes", "allowed":
		return true
	}
	return false
}

func multiclassAllowed(lobbyID int) bool {
	switch tableRule(lobbyID, "multiclassing") {
	case "allowed":
		return true
	case "off":
		return false
	}
	return multiclassServerDefault()
}

func multiclassingTableRule() tableRuleDef {
	return tableRuleDef{Key: "multiclassing", Title: "Multiclassing", Default: "server", Options: []tableRuleOption{
		{"server", "The server's setting", "Follows the server setting AGENTRPG_MULTICLASS (off unless the server turns it on)."},
		{"allowed", "Allowed", "Characters may take levels in a new class (SRD 5.2.1 multiclassing: 13 in the primary abilities)."},
		{"off", "Off", "Characters level only in the classes they have."},
	}}
}

func multiclassOffMessage() string {
	return "Multiclassing is off in this campaign (an optional rule; the GM can allow it with the multiclassing table rule, or the server with AGENTRPG_MULTICLASS=on). You can level in the classes you have."
}

// classLevelsOf: the character's levels per class, lowercased; a single-class character (or one without class_levels) is {class: level}.
func classLevelsOf(charID int) map[string]int {
	var raw []byte
	var class string
	var level int
	db.QueryRow(`SELECT COALESCE(class_levels,'{}'), COALESCE(class,''), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&raw, &class, &level)
	m := map[string]int{}
	var in map[string]int
	json.Unmarshal(raw, &in)
	for k, v := range in {
		if v > 0 {
			m[strings.ToLower(k)] += v
		}
	}
	if len(m) == 0 && class != "" {
		m[strings.ToLower(class)] = level
	}
	return m
}

func isMulticlassChar(charID int) bool { return len(classLevelsOf(charID)) > 1 }

// classOrder lists the classes with the main class first, then by level (for stable lists).
func classOrder(levels map[string]int, main string) []string {
	var out []string
	for c := range levels {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == strings.ToLower(main)) != (out[j] == strings.ToLower(main)) {
			return out[i] == strings.ToLower(main)
		}
		if levels[out[i]] != levels[out[j]] {
			return levels[out[i]] > levels[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// characterSpellSlots: the slots of the Spellcasting feature. A single-class character keeps its class table (a Warlock's Pact Magic
// table included, as before).
func characterSpellSlots(charID int, class string, level int) map[int]int {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return game.SpellSlots(class, level)
	}
	regular, _ := game.MulticlassSlots(levels)
	return regular
}

// characterPactSlots: a multiclass character's Pact Magic slots (nil without Warlock levels, and for a single-class Warlock).
func characterPactSlots(charID int) map[int]int {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return nil
	}
	_, pact := game.MulticlassSlots(levels)
	if len(pact) == 0 {
		return nil
	}
	return pact
}

func characterExtraAttacks(charID int, class string, level int) int {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return game.ExtraAttackCount(class, level)
	}
	return game.MulticlassExtraAttacks(levels)
}

// characterClassFeatures: each class's features up to its own level, the main class first.
func characterClassFeatures(charID int, class string, level int) []game.ClassFeature {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return game.GetActiveClassFeatures(class, level)
	}
	var out []game.ClassFeature
	for _, c := range classOrder(levels, class) {
		for _, f := range game.GetActiveClassFeatures(c, levels[c]) {
			if f.Name == "Epic Boon" && levels[c] < 19 {
				continue
			}
			f.Name = fmt.Sprintf("%s (%s %d)", f.Name, titleCase(c), f.Level)
			out = append(out, f)
		}
	}
	return out
}

// characterResourceDefs and characterMaxResources: the resources of every class the character has, each at that class's level.
func characterResourceDefs(charID int, class string) []game.ClassResource {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return game.ClassResources(class)
	}
	var out []game.ClassResource
	seen := map[string]bool{}
	for _, c := range classOrder(levels, class) {
		for _, r := range game.ClassResources(c) {
			if !seen[r.Key] {
				seen[r.Key] = true
				out = append(out, r)
			}
		}
	}
	return out
}

func characterMaxResources(charID int, class string, level, chaMod int) map[string]int {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return game.AllMaxClassResources(class, level, chaMod)
	}
	out := map[string]int{}
	for c, l := range levels {
		for k, v := range game.AllMaxClassResources(c, l, chaMod) {
			if v > out[k] {
				out[k] = v
			}
		}
	}
	return out
}

// classResourceMax: one resource's maximum from the class that has it (a Monk 13 / Barbarian 2 has 13 Focus Points, not 20).
func classResourceMax(charID int, class string, level int, key string, chaMod int) int {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return game.MaxClassResource(class, level, key, chaMod)
	}
	best := 0
	for c, l := range levels {
		if v := game.MaxClassResource(c, l, key, chaMod); v > best {
			best = v
		}
	}
	return best
}

// ---- Armor Class

// computeCharacterAC is the character's AC from its armor, Shield, the best unarmored calculation (Unarmored Defense of any of its
// classes, Draconic Resilience: only one counts, SRD 5.2.1), magic items and the Defense Fighting Style. The character sheet used this
// calculation; the stored AC (what monsters attack) was only corrected when someone opened the sheet, so a new character stood at
// 10 + Dexterity until then.
func computeCharacterAC(charID int) int {
	var armor sql.NullString
	var shield bool
	if db.QueryRow(`SELECT equipped_armor, COALESCE(equipped_shield,false) FROM characters WHERE id = $1`, charID).Scan(&armor, &shield) != nil {
		return 0
	}
	return characterACWith(charID, armor.String, shield) // unarmored bases, Mage Armor (F65), magic items, Defense, Barkskin's floor
}

// refreshStoredAC writes the computed AC, except in Wild Shape (the beast's AC is in place until the form ends).
func refreshStoredAC(charID int) {
	if ac := computeCharacterAC(charID); ac > 0 {
		db.Exec(`UPDATE characters SET ac = $1 WHERE id = $2 AND COALESCE(wild_shape_form,'') = ''`, ac, charID)
	}
}

// ---- subclasses: one per class (SRD 5.2.1: each class's subclass at that class's own level). The first class's is the subclass column;
// the others are in subclasses {"sorcerer": "draconic", ...}.

func ensureSubclassesColumn() {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS subclasses JSONB DEFAULT '{}'`)
}

// subclassesByClass: every class's subclass, the main class's included.
func subclassesByClass(charID int) map[string]string {
	ensureSubclassesColumn()
	var raw []byte
	var class, sub string
	db.QueryRow(`SELECT COALESCE(subclasses,'{}'), COALESCE(class,''), COALESCE(subclass,'') FROM characters WHERE id = $1`, charID).Scan(&raw, &class, &sub)
	out := map[string]string{}
	json.Unmarshal(raw, &out)
	if sub != "" {
		out[strings.ToLower(class)] = sub
	}
	return out
}

func setSecondarySubclass(charID int, class, slug string) {
	ensureSubclassesColumn()
	m := subclassesByClass(charID)
	var main string
	db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&main)
	delete(m, strings.ToLower(main))
	m[strings.ToLower(class)] = slug
	b, _ := json.Marshal(m)
	db.Exec(`UPDATE characters SET subclasses = $1 WHERE id = $2`, string(b), charID)
}

// characterSubclassFeatures: each subclass's features at its class's level ("Draconic Resilience (Sorcerer 3)" for a multiclass).
func characterSubclassFeatures(charID int, class, subclass string, level int) []game.SubclassFeature {
	levels := classLevelsOf(charID)
	if len(levels) <= 1 {
		return getActiveSubclassFeatures(subclass, level)
	}
	var out []game.SubclassFeature
	subs := subclassesByClass(charID)
	for _, c := range classOrder(levels, class) {
		if s := subs[c]; s != "" {
			for _, f := range getActiveSubclassFeatures(s, levels[c]) {
				f.Name = fmt.Sprintf("%s (%s %d)", f.Name, titleCase(c), f.Level)
				out = append(out, f)
			}
		}
	}
	return out
}

// subclassLevelFor: the level a subclass's features use: its class's level (the total level for a single class).
func subclassLevelFor(charID int, subclass string, total int) int {
	sub, ok := game.AvailableSubclasses[strings.ToLower(subclass)]
	levels := classLevelsOf(charID)
	if !ok || len(levels) <= 1 {
		return total
	}
	return levels[strings.ToLower(sub.Class)]
}

// charHasSubclassFeature: any of the character's subclasses has the mechanic at its class's level.
func charHasSubclassFeature(charID int, mechanic string) bool {
	levels := classLevelsOf(charID)
	for c, s := range subclassesByClass(charID) {
		if hasSubclassFeature(s, levels[c], mechanic) {
			return true
		}
	}
	return false
}

// hitDiceLeftByType: the Hit Point Dice not spent yet, by type (multiclass); nil for a single class (hit_dice_spent covers it).
func hitDiceLeftByType(charID int) map[string]int {
	pools := hitDicePools(charID)
	if len(pools) <= 1 {
		return nil
	}
	spent := hitDiceSpentByType(charID)
	out := map[string]int{}
	for k, n := range pools {
		out[k] = n - spent[k]
	}
	return out
}

func hitDiceSpentByType(charID int) map[string]int {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS hit_dice_spent_by_type JSONB DEFAULT '{}'`)
	var raw []byte
	db.QueryRow(`SELECT COALESCE(hit_dice_spent_by_type,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	m := map[string]int{}
	json.Unmarshal(raw, &m)
	return m
}

// spendHitDiceByType records n Hit Point Dice spent from a multiclass character's pools (the type asked for first, then the largest)
// and returns their sizes; nil for a single class.
func spendHitDiceByType(charID, n int, first string) []int {
	left := hitDiceLeftByType(charID)
	if left == nil || n <= 0 {
		return nil
	}
	spent := hitDiceSpentByType(charID)
	var types []string
	for k := range left {
		types = append(types, k)
	}
	size := func(k string) int { v := 0; fmt.Sscanf(k, "d%d", &v); return v }
	sort.Slice(types, func(i, j int) bool {
		if (types[i] == first) != (types[j] == first) {
			return types[i] == first
		}
		return size(types[i]) > size(types[j])
	})
	var out []int
	for _, k := range types {
		for left[k] > 0 && len(out) < n {
			left[k]--
			spent[k]++
			out = append(out, size(k))
		}
	}
	b, _ := json.Marshal(spent)
	db.Exec(`UPDATE characters SET hit_dice_spent_by_type = $1 WHERE id = $2`, string(b), charID)
	return out
}

// hitDicePools: Hit Point Dice by die type ("d10": 6, "d6": 12, "d8": 2); one class gives one pool.
func hitDicePools(charID int) map[string]int {
	out := map[string]int{}
	for c, l := range classLevelsOf(charID) {
		out[fmt.Sprintf("d%d", game.HitDie(c))] += l
	}
	return out
}
