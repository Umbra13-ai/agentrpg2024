package main

import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Skill to ability mapping (SRD 5.2.1)
var skillAbilityMap = map[string]string{
	// STR
	"athletics": "str",
	// DEX
	"acrobatics": "dex", "sleight_of_hand": "dex", "stealth": "dex",
	// INT
	"arcana": "int", "history": "int", "investigation": "int", "nature": "int", "religion": "int",
	// WIS
	"animal_handling": "wis", "insight": "wis", "medicine": "wis", "perception": "wis", "survival": "wis",
	// CHA
	"deception": "cha", "intimidation": "cha", "performance": "cha", "persuasion": "cha",
}

// containsSkill checks if a skill name is in the skill list (case-insensitive)
func containsSkill(skills []string, skill string) bool {
	skill = strings.ToLower(skill)
	for _, s := range skills {
		if strings.ToLower(s) == skill {
			return true
		}
	}
	return false
}

// getCurrencyColumn maps currency type to database column
func getCurrencyColumn(currencyType string) (string, string, bool) {
	switch strings.ToLower(currencyType) {
	case "cp", "copper":
		return "copper", "cp", true
	case "sp", "silver":
		return "silver", "sp", true
	case "ep", "electrum":
		return "electrum", "ep", true
	case "gp", "gold", "":
		return "gold", "gp", true
	case "pp", "platinum":
		return "platinum", "pp", true
	default:
		return "", "", false
	}
}

// Feat represents a character feat from the SRD/PHB
type Feat struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Prerequisite string            `json:"prerequisite,omitempty"` // e.g., "str:13" or "spellcaster" or ""
	Benefits     []string          `json:"benefits"`
	AbilityBonus map[string]int    `json:"ability_bonus,omitempty"` // e.g., {"str": 1}
	Features     map[string]string `json:"features,omitempty"`      // Special features to track

	Category       string   `json:"category,omitempty"`        // Origin, General, Fighting Style or Epic Boon
	Repeatable     bool     `json:"repeatable,omitempty"`      // can be taken more than once
	AbilityChoices []string `json:"ability_choices,omitempty"` // the abilities ability_choice may name
	AbilityPoints  int      `json:"ability_points,omitempty"`  // the increase (1)
	MaxScore       int      `json:"max_score,omitempty"`       // 20, or 30 for an Epic Boon
}

type SRDMonster struct {
	Name    string      `json:"name"`
	Size    string      `json:"size"`
	Type    string      `json:"type"`
	AC      int         `json:"ac"`
	HP      int         `json:"hp"`
	HitDice string      `json:"hit_dice"`
	Speed   int         `json:"speed"`
	STR     int         `json:"str"`
	DEX     int         `json:"dex"`
	CON     int         `json:"con"`
	INT     int         `json:"int"`
	WIS     int         `json:"wis"`
	CHA     int         `json:"cha"`
	CR      string      `json:"cr"`
	XP      int         `json:"xp"`
	Actions []SRDAction `json:"actions"`
}

type SRDAction struct {
	Name        string `json:"name"`
	AttackBonus int    `json:"attack_bonus"`
	DamageDice  string `json:"damage_dice"`
	DamageType  string `json:"damage_type"`
}

type SRDSpell struct {
	Name              string            `json:"name"`
	Level             int               `json:"level"`
	School            string            `json:"school"`
	CastingTime       string            `json:"casting_time"`
	Range             string            `json:"range"`
	Components        string            `json:"components"`
	Duration          string            `json:"duration"`
	Description       string            `json:"description"`
	DamageDice        string            `json:"damage_dice,omitempty"`
	DamageType        string            `json:"damage_type,omitempty"`
	SavingThrow       string            `json:"saving_throw,omitempty"`
	Healing           string            `json:"healing,omitempty"`
	IsRitual          bool              `json:"is_ritual,omitempty"`
	AoEShape          string            `json:"aoe_shape,omitempty"`
	AoESize           int               `json:"aoe_size,omitempty"`
	DamageAtSlotLevel map[string]string `json:"damage_at_slot_level,omitempty"`
	DamageAtCharLevel map[string]string `json:"damage_at_character_level,omitempty"` // Cantrip scaling
	HealAtSlotLevel   map[string]string `json:"heal_at_slot_level,omitempty"`
	Material          string            `json:"material,omitempty"`
	MaterialCost      int               `json:"material_cost,omitempty"`
	MaterialConsumed  bool              `json:"material_consumed,omitempty"`
	ComponentsText    string            `json:"components_text,omitempty"` // as printed, with the material
	Concentration     bool              `json:"concentration,omitempty"`
	HigherLevel       string            `json:"higher_level,omitempty"`
	Classes           []string          `json:"classes,omitempty"`
	DamageParts       []damagePart      `json:"damage_parts,omitempty"` // spells that deal damage in more than one part
}

type SRDClass struct {
	Name         string   `json:"name"`
	HitDie       int      `json:"hit_die"`
	Primary      string   `json:"primary_ability"`
	Saves        []string `json:"saving_throws"`
	ArmorProf    []string `json:"armor_proficiencies"`
	WeaponProf   []string `json:"weapon_proficiencies"`
	Spellcasting string   `json:"spellcasting_ability,omitempty"`
}

// srdClasses: the SRD 5.2.1 core traits of the twelve classes (the classes table replaces the entries at startup; the weapon and armor
// training comes from game.ClassProficiencies).
var srdClasses = map[string]SRDClass{
	"barbarian": {Name: "Barbarian", HitDie: 12, Primary: "STR", Saves: []string{"STR", "CON"}},
	"bard":      {Name: "Bard", HitDie: 8, Primary: "CHA", Saves: []string{"DEX", "CHA"}, Spellcasting: "CHA"},
	"cleric":    {Name: "Cleric", HitDie: 8, Primary: "WIS", Saves: []string{"WIS", "CHA"}, Spellcasting: "WIS"},
	"druid":     {Name: "Druid", HitDie: 8, Primary: "WIS", Saves: []string{"INT", "WIS"}, Spellcasting: "WIS"},
	"fighter":   {Name: "Fighter", HitDie: 10, Primary: "STR or DEX", Saves: []string{"STR", "CON"}},
	"monk":      {Name: "Monk", HitDie: 8, Primary: "DEX and WIS", Saves: []string{"STR", "DEX"}},
	"paladin":   {Name: "Paladin", HitDie: 10, Primary: "STR and CHA", Saves: []string{"WIS", "CHA"}, Spellcasting: "CHA"},
	"ranger":    {Name: "Ranger", HitDie: 10, Primary: "DEX and WIS", Saves: []string{"STR", "DEX"}, Spellcasting: "WIS"},
	"rogue":     {Name: "Rogue", HitDie: 8, Primary: "DEX", Saves: []string{"DEX", "INT"}},
	"sorcerer":  {Name: "Sorcerer", HitDie: 6, Primary: "CHA", Saves: []string{"CON", "CHA"}, Spellcasting: "CHA"},
	"warlock":   {Name: "Warlock", HitDie: 8, Primary: "CHA", Saves: []string{"WIS", "CHA"}, Spellcasting: "CHA"},
	"wizard":    {Name: "Wizard", HitDie: 6, Primary: "INT", Saves: []string{"INT", "WIS"}, Spellcasting: "INT"},
}

func init() {
	for k, c := range srdClasses {
		p := game.ClassProficiencies(k)
		c.ArmorProf, c.WeaponProf = p.Armor, p.Weapons
		srdClasses[k] = c
	}
}

// Multiclass prerequisites (SRD 5.2.1 Multiclassing): to take a level in a new class, a character needs a score of at least 13 in the
// primary ability of the new class and of its current classes.
type MulticlassPrereqs struct {
	STR int `json:"str,omitempty"`
	DEX int `json:"dex,omitempty"`
	INT int `json:"int,omitempty"`
	WIS int `json:"wis,omitempty"`
	CHA int `json:"cha,omitempty"`
	// Some classes require meeting EITHER stat (use -1 to indicate OR logic)
	OrLogic bool `json:"or_logic,omitempty"` // If true, meet ANY of the stats, not ALL
}

// multiclassPrereqs maps each class to its multiclassing prerequisites
var multiclassPrereqs = map[string]MulticlassPrereqs{
	"barbarian": {STR: 13},
	"bard":      {CHA: 13},
	"cleric":    {WIS: 13},
	"druid":     {WIS: 13},
	"fighter":   {STR: 13, DEX: 13, OrLogic: true}, // STR 13 OR DEX 13
	"monk":      {DEX: 13, WIS: 13},                // Both required
	"paladin":   {STR: 13, CHA: 13},                // Both required
	"ranger":    {DEX: 13, WIS: 13},                // Both required
	"rogue":     {DEX: 13},
	"sorcerer":  {CHA: 13},
	"warlock":   {CHA: 13},
	"wizard":    {INT: 13},
}

// Multiclass proficiencies: a first level in a class other than the first class gives only some of its training.
type MulticlassProfs struct {
	ArmorProf  []string `json:"armor_proficiencies"`
	WeaponProf []string `json:"weapon_proficiencies"`
	ToolProf   []string `json:"tool_proficiencies,omitempty"`
	Skills     int      `json:"skill_choices,omitempty"` // Number of skill choices
}

// multiclassProfsFor: what a character gains by taking a first level in a class that isn't its first class (each class's "As a
// Multiclass Character" entry, SRD 5.2.1): the training from game.MulticlassProficiencies and the skill choices.
func multiclassProfsFor(class string) MulticlassProfs {
	c := strings.ToLower(class)
	p := game.MulticlassProficiencies(c)
	return MulticlassProfs{ArmorProf: nonNil(p.Armor), WeaponProf: nonNil(p.Weapons), ToolProf: p.Tools,
		Skills: map[string]int{"bard": 1, "ranger": 1, "rogue": 1}[c]}
}

// meetsMulticlassPrereqs checks if a character meets the ability score requirements for a class
func meetsMulticlassPrereqs(class string, str, dex, intl, wis, cha int) (bool, string) {
	prereqs, ok := multiclassPrereqs[strings.ToLower(class)]
	if !ok {
		return false, fmt.Sprintf("Unknown class: %s", class)
	}

	if prereqs.OrLogic {
		// Meet ANY of the requirements (Fighter: STR 13 OR DEX 13)
		metAny := false
		reasons := []string{}
		if prereqs.STR > 0 {
			if str >= prereqs.STR {
				metAny = true
			} else {
				reasons = append(reasons, fmt.Sprintf("STR %d (have %d)", prereqs.STR, str))
			}
		}
		if prereqs.DEX > 0 {
			if dex >= prereqs.DEX {
				metAny = true
			} else {
				reasons = append(reasons, fmt.Sprintf("DEX %d (have %d)", prereqs.DEX, dex))
			}
		}
		if !metAny {
			return false, fmt.Sprintf("Need %s", strings.Join(reasons, " OR "))
		}
	} else {
		// Meet ALL of the requirements
		failedReqs := []string{}
		if prereqs.STR > 0 && str < prereqs.STR {
			failedReqs = append(failedReqs, fmt.Sprintf("STR %d (have %d)", prereqs.STR, str))
		}
		if prereqs.DEX > 0 && dex < prereqs.DEX {
			failedReqs = append(failedReqs, fmt.Sprintf("DEX %d (have %d)", prereqs.DEX, dex))
		}
		if prereqs.INT > 0 && intl < prereqs.INT {
			failedReqs = append(failedReqs, fmt.Sprintf("INT %d (have %d)", prereqs.INT, intl))
		}
		if prereqs.WIS > 0 && wis < prereqs.WIS {
			failedReqs = append(failedReqs, fmt.Sprintf("WIS %d (have %d)", prereqs.WIS, wis))
		}
		if prereqs.CHA > 0 && cha < prereqs.CHA {
			failedReqs = append(failedReqs, fmt.Sprintf("CHA %d (have %d)", prereqs.CHA, cha))
		}
		if len(failedReqs) > 0 {
			return false, fmt.Sprintf("Need %s", strings.Join(failedReqs, " AND "))
		}
	}

	return true, ""
}

type SRDSpecies struct {
	Name            string         `json:"name"`
	Size            string         `json:"size"`
	Speed           int            `json:"speed"`
	AbilityMods     map[string]int `json:"ability_modifiers"`
	Traits          []string       `json:"traits"`
	Languages       []string       `json:"languages"`
	DarkvisionRange int            `json:"darkvision_range"` // 0 = none, 60 = standard, 120 = superior
}

// srdSpecies: the nine SRD 5.2.1 species, filled at startup (speciesAsSRDSpecies).
var srdSpecies = map[string]SRDSpecies{}

type SRDWeapon struct {
	Name       string   `json:"name"`
	Category   string   `json:"category"`
	Type       string   `json:"type"`
	Damage     string   `json:"damage"`
	DamageType string   `json:"damage_type"`
	Properties []string `json:"properties"`
	Weight     float64  `json:"weight"`
	Cost       string   `json:"cost"`
	Mastery    string   `json:"mastery,omitempty"`
}

var srdWeapons = map[string]SRDWeapon{
	"dagger":         {Name: "Dagger", Category: "simple", Type: "melee", Damage: "1d4", DamageType: "piercing", Properties: []string{"finesse", "light", "thrown (20/60)"}, Weight: 1, Cost: "2 gp"},
	"handaxe":        {Name: "Handaxe", Category: "simple", Type: "melee", Damage: "1d6", DamageType: "slashing", Properties: []string{"light", "thrown (20/60)"}, Weight: 2, Cost: "5 gp"},
	"mace":           {Name: "Mace", Category: "simple", Type: "melee", Damage: "1d6", DamageType: "bludgeoning", Properties: []string{}, Weight: 4, Cost: "5 gp"},
	"quarterstaff":   {Name: "Quarterstaff", Category: "simple", Type: "melee", Damage: "1d6", DamageType: "bludgeoning", Properties: []string{"versatile (1d8)"}, Weight: 4, Cost: "2 sp"},
	"spear":          {Name: "Spear", Category: "simple", Type: "melee", Damage: "1d6", DamageType: "piercing", Properties: []string{"thrown (20/60)", "versatile (1d8)"}, Weight: 3, Cost: "1 gp"},
	"shortbow":       {Name: "Shortbow", Category: "simple", Type: "ranged", Damage: "1d6", DamageType: "piercing", Properties: []string{"ammunition (80/320)", "two-handed"}, Weight: 2, Cost: "25 gp"},
	"light_crossbow": {Name: "Light Crossbow", Category: "simple", Type: "ranged", Damage: "1d8", DamageType: "piercing", Properties: []string{"ammunition (80/320)", "loading", "two-handed"}, Weight: 5, Cost: "25 gp"},
	"longsword":      {Name: "Longsword", Category: "martial", Type: "melee", Damage: "1d8", DamageType: "slashing", Properties: []string{"versatile (1d10)"}, Weight: 3, Cost: "15 gp"},
	"rapier":         {Name: "Rapier", Category: "martial", Type: "melee", Damage: "1d8", DamageType: "piercing", Properties: []string{"finesse"}, Weight: 2, Cost: "25 gp"},
	"shortsword":     {Name: "Shortsword", Category: "martial", Type: "melee", Damage: "1d6", DamageType: "piercing", Properties: []string{"finesse", "light"}, Weight: 2, Cost: "10 gp"},
	"greatsword":     {Name: "Greatsword", Category: "martial", Type: "melee", Damage: "2d6", DamageType: "slashing", Properties: []string{"heavy", "two-handed"}, Weight: 6, Cost: "50 gp"},
	"greataxe":       {Name: "Greataxe", Category: "martial", Type: "melee", Damage: "1d12", DamageType: "slashing", Properties: []string{"heavy", "two-handed"}, Weight: 7, Cost: "30 gp"},
	"longbow":        {Name: "Longbow", Category: "martial", Type: "ranged", Damage: "1d8", DamageType: "piercing", Properties: []string{"ammunition (150/600)", "heavy", "two-handed"}, Weight: 2, Cost: "50 gp"},
	// Additional ranged weapons with ammunition
	"hand_crossbow":  {Name: "Hand Crossbow", Category: "martial", Type: "ranged", Damage: "1d6", DamageType: "piercing", Properties: []string{"ammunition (30/120)", "light", "loading"}, Weight: 3, Cost: "75 gp"},
	"heavy_crossbow": {Name: "Heavy Crossbow", Category: "martial", Type: "ranged", Damage: "1d10", DamageType: "piercing", Properties: []string{"ammunition (100/400)", "heavy", "loading", "two-handed"}, Weight: 18, Cost: "50 gp"},
	"blowgun":        {Name: "Blowgun", Category: "martial", Type: "ranged", Damage: "1", DamageType: "piercing", Properties: []string{"ammunition (25/100)", "loading"}, Weight: 1, Cost: "10 gp"},
	"musket":         {Name: "Musket", Category: "martial", Type: "ranged", Damage: "1d12", DamageType: "piercing", Properties: []string{"ammunition (40/120)", "loading", "two-handed"}, Weight: 10, Cost: "500 gp"},
	"pistol":         {Name: "Pistol", Category: "martial", Type: "ranged", Damage: "1d10", DamageType: "piercing", Properties: []string{"ammunition (30/90)", "loading"}, Weight: 3, Cost: "250 gp"},
	"sling":          {Name: "Sling", Category: "simple", Type: "ranged", Damage: "1d4", DamageType: "bludgeoning", Properties: []string{"ammunition (30/120)"}, Weight: 0, Cost: "1 sp"},
}

type SRDArmor struct {
	Name          string  `json:"name"`
	Category      string  `json:"category"`
	AC            int     `json:"ac"`
	DexBonus      bool    `json:"dex_bonus"`
	MaxDexBonus   int     `json:"max_dex_bonus"`
	StrRequired   int     `json:"str_required"`
	StealthDisadv bool    `json:"stealth_disadvantage"`
	Weight        float64 `json:"weight"`
	Cost          string  `json:"cost"`
}

var srdArmor = map[string]SRDArmor{
	"padded":          {Name: "Padded", Category: "light", AC: 11, DexBonus: true, MaxDexBonus: -1, StealthDisadv: true, Weight: 8, Cost: "5 gp"},
	"leather":         {Name: "Leather", Category: "light", AC: 11, DexBonus: true, MaxDexBonus: -1, Weight: 10, Cost: "10 gp"},
	"studded_leather": {Name: "Studded Leather", Category: "light", AC: 12, DexBonus: true, MaxDexBonus: -1, Weight: 13, Cost: "45 gp"},
	"hide":            {Name: "Hide", Category: "medium", AC: 12, DexBonus: true, MaxDexBonus: 2, Weight: 12, Cost: "10 gp"},
	"chain_shirt":     {Name: "Chain Shirt", Category: "medium", AC: 13, DexBonus: true, MaxDexBonus: 2, Weight: 20, Cost: "50 gp"},
	"scale_mail":      {Name: "Scale Mail", Category: "medium", AC: 14, DexBonus: true, MaxDexBonus: 2, StealthDisadv: true, Weight: 45, Cost: "50 gp"},
	"breastplate":     {Name: "Breastplate", Category: "medium", AC: 14, DexBonus: true, MaxDexBonus: 2, Weight: 20, Cost: "400 gp"},
	"half_plate":      {Name: "Half Plate", Category: "medium", AC: 15, DexBonus: true, MaxDexBonus: 2, StealthDisadv: true, Weight: 40, Cost: "750 gp"},
	"ring_mail":       {Name: "Ring Mail", Category: "heavy", AC: 14, StealthDisadv: true, Weight: 40, Cost: "30 gp"},
	"chain_mail":      {Name: "Chain Mail", Category: "heavy", AC: 16, StrRequired: 13, StealthDisadv: true, Weight: 55, Cost: "75 gp"},
	"splint":          {Name: "Splint", Category: "heavy", AC: 17, StrRequired: 15, StealthDisadv: true, Weight: 60, Cost: "200 gp"},
	"plate":           {Name: "Plate", Category: "heavy", AC: 18, StrRequired: 15, StealthDisadv: true, Weight: 65, Cost: "1500 gp"},
	"shield":          {Name: "Shield", Category: "shield", AC: 2, Weight: 6, Cost: "10 gp"},
}

// handleUniverseIndex godoc
// @Summary Universe index
// @Description Returns list of available universe endpoints (monsters, spells, classes, species, weapons, armor). Universe is the shared 5e SRD content.
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "Universe endpoints list"
// @Router /universe/ [get]
func handleUniverseIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name":        "5e Universe (SRD)",
		"description": "Shared game content from the SRD 5.2.1. GMs can also create campaign-specific items via /api/campaigns/{id}/items",
		"license":     "CC-BY-4.0",
		"endpoints": map[string]string{
			"monsters":    "/api/universe/monsters",
			"spells":      "/api/universe/spells",
			"classes":     "/api/universe/classes",
			"species":     "/api/universe/species",
			"weapons":     "/api/universe/weapons",
			"armor":       "/api/universe/armor",
			"magic-items": "/api/universe/magic-items",
			"backgrounds": "/api/universe/backgrounds",
			"feats":       "/api/universe/feats",
		},
	})
}

// handleUniverseMonsters godoc
// @Summary List all monsters
// @Description Returns list of monster slugs. Use /universe/monsters/{slug} for details, or /universe/monsters/search for filtering.
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "List of monster slugs"
// @Router /universe/monsters [get]
func handleUniverseMonsters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	rows, err := db.Query("SELECT slug FROM monsters WHERE " + rulesetFilter(r) + " ORDER BY slug")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var slug string
		rows.Scan(&slug)
		names = append(names, slug)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"monsters": names, "count": len(names)})
}

// handleUniverseSpells godoc
// @Summary List all spells
// @Description Returns list of spell slugs. Use /universe/spells/{slug} for details, or /universe/spells/search for filtering.
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "List of spell slugs"
// @Router /universe/spells [get]
func handleUniverseSpells(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	rows, err := db.Query("SELECT slug FROM spells ORDER BY slug")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var slug string
		rows.Scan(&slug)
		names = append(names, slug)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"spells": names, "count": len(names)})
}

// handleUniverseClasses godoc
// @Summary List all classes
// @Description Returns list of class slugs (barbarian, bard, cleric, etc.)
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "List of class slugs"
// @Router /universe/classes [get]
func handleUniverseClasses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	rows, err := db.Query("SELECT slug FROM classes ORDER BY slug")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var slug string
		rows.Scan(&slug)
		names = append(names, slug)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"classes": names, "count": len(names)})
}

// handleUniverseClass godoc
// @Summary Get class details
// @Description Returns class details including hit die, saving throws, and spellcasting ability
// @Tags Universe
// @Produce json
// @Param slug path string true "Class slug (e.g., fighter, wizard)"
// @Success 200 {object} map[string]interface{} "Class details"
// @Failure 404 {object} map[string]interface{} "Class not found"
// @Router /universe/classes/{slug} [get]
func handleUniverseClass(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimPrefix(r.URL.Path, "/api/universe/classes/")
	var c struct {
		Name                string `json:"name"`
		HitDie              int    `json:"hit_die"`
		PrimaryAbility      string `json:"primary_ability"`
		SavingThrows        string `json:"saving_throws"`
		SpellcastingAbility string `json:"spellcasting_ability,omitempty"`
	}
	err := db.QueryRow("SELECT name, hit_die, primary_ability, saving_throws, spellcasting_ability FROM classes WHERE slug = $1", id).Scan(
		&c.Name, &c.HitDie, &c.PrimaryAbility, &c.SavingThrows, &c.SpellcastingAbility)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "class_not_found"})
		return
	}
	out := map[string]interface{}{"name": c.Name, "hit_die": c.HitDie, "primary_ability": c.PrimaryAbility, "saving_throws": c.SavingThrows}
	if c.SpellcastingAbility != "" {
		out["spellcasting_ability"] = c.SpellcastingAbility
	}
	if txt, ok := classTextByName(id); ok { // the SRD text of the class, word for word (the numbers above are the game's own tables)
		out["srd_sections"] = txt.Sections
		out["brief"] = classBrief[txt.Name]
	}
	json.NewEncoder(w).Encode(out)
}

// handleUniverseClassSpells godoc
// @Summary List all spellcasting classes with spell counts
// @Description Returns list of classes that have spell lists with their spell counts
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "List of classes with spell counts"
// @Router /universe/class-spells [get]
func handleUniverseClassSpells(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	rows, err := db.Query(`
		SELECT class_slug, COUNT(*) as spell_count 
		FROM class_spells 
		GROUP BY class_slug 
		ORDER BY class_slug
	`)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()

	type ClassSpellCount struct {
		Class      string `json:"class"`
		SpellCount int    `json:"spell_count"`
	}
	classes := []ClassSpellCount{}
	for rows.Next() {
		var c ClassSpellCount
		rows.Scan(&c.Class, &c.SpellCount)
		classes = append(classes, c)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"classes": classes, "count": len(classes)})
}

// handleUniverseClassSpellList godoc
// @Summary Get spell list for a class
// @Description Returns all spells available to a specific class with optional level filter
// @Tags Universe
// @Produce json
// @Param class path string true "Class slug (e.g., wizard, cleric)"
// @Param level query int false "Filter by spell level (0-9)"
// @Success 200 {object} map[string]interface{} "List of spells for the class"
// @Failure 404 {object} map[string]interface{} "Class not found or has no spell list"
// @Router /universe/class-spells/{class} [get]
func handleUniverseClassSpellList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	class := strings.TrimPrefix(r.URL.Path, "/api/universe/class-spells/")
	class = strings.ToLower(class)

	// Optional level filter
	levelFilter := r.URL.Query().Get("level")

	var query string
	var args []interface{}

	if levelFilter != "" {
		level, err := strconv.Atoi(levelFilter)
		if err != nil || level < 0 || level > 9 {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_level"})
			return
		}
		query = `
			SELECT s.slug, s.name, s.level, s.school
			FROM class_spells cs
			JOIN spells s ON s.slug = cs.spell_slug
			WHERE cs.class_slug = $1 AND s.level = $2
			ORDER BY s.level, s.name
		`
		args = []interface{}{class, level}
	} else {
		query = `
			SELECT s.slug, s.name, s.level, s.school
			FROM class_spells cs
			JOIN spells s ON s.slug = cs.spell_slug
			WHERE cs.class_slug = $1
			ORDER BY s.level, s.name
		`
		args = []interface{}{class}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()

	type SpellInfo struct {
		Slug   string `json:"slug"`
		Name   string `json:"name"`
		Level  int    `json:"level"`
		School string `json:"school"`
	}
	spells := []SpellInfo{}
	for rows.Next() {
		var s SpellInfo
		rows.Scan(&s.Slug, &s.Name, &s.Level, &s.School)
		spells = append(spells, s)
	}

	if len(spells) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"error": "class_not_found_or_no_spells"})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"class":  class,
		"spells": spells,
		"count":  len(spells),
	})
}

// handleUniverseConsumables godoc
// @Summary List consumable items
// @Description The SRD 5.2.1 potions (a Bonus Action to drink, or to give to a creature within 5 feet) and the Spell Scroll table (save DC and attack bonus by spell level); give them with POST /api/gm/give-item
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "Consumables list"
// @Router /universe/consumables [get]
func handleUniverseConsumables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// The SRD 5.2.1 potions (each a Bonus Action to drink or give) and the Spell Scroll table (magic_item_use_2024.go)
	items := potionAndScrollList()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"consumables": items,
		"count":       len(items),
		"usage":       "Use POST /api/gm/give-item with {character_id, item_name} to give items to characters",
	})
}

// handleUniverseBackgrounds godoc
// @Summary List all backgrounds
// @Description Returns all character backgrounds with skill/tool proficiencies, languages, equipment, and features
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "Background list with details"
// @Router /universe/backgrounds [get]
func handleUniverseBackgrounds(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	backgrounds := []map[string]interface{}{}
	for key, bg := range game.GetAllBackgrounds() {
		_ = key
		backgrounds = append(backgrounds, backgroundJSON(bg))
	}

	// Sort by name
	sort.Slice(backgrounds, func(i, j int) bool {
		return backgrounds[i]["name"].(string) < backgrounds[j]["name"].(string)
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"backgrounds": backgrounds,
		"count":       len(backgrounds),
		"usage":       "Use 'background' field in POST /api/characters to apply background benefits",
	})
}

// handleUniverseBackground godoc
// @Summary Get background details
// @Description Returns details for a specific background including proficiencies, equipment, and feature
// @Tags Universe
// @Produce json
// @Param slug path string true "Background slug (e.g., soldier, sage, criminal)"
// @Success 200 {object} map[string]interface{} "Background details"
// @Failure 404 {object} map[string]interface{} "Background not found"
// @Router /universe/backgrounds/{slug} [get]
func handleUniverseBackground(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	slug := strings.TrimPrefix(r.URL.Path, "/api/universe/backgrounds/")
	slug = strings.ToLower(strings.TrimSpace(slug))

	// Try with underscores (normalize hyphens)
	slug = strings.ReplaceAll(slug, "-", "_")

	bg := game.GetBackground(slug)
	if bg == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "background_not_found",
			"message": fmt.Sprintf("Background '%s' not found. Use GET /api/universe/backgrounds to list all.", slug),
		})
		return
	}

	json.NewEncoder(w).Encode(backgroundJSON(*bg))
}

// handleUniverseFeats godoc
// @Summary List all available feats
// @Description Returns list of feats that can be taken instead of ASI points. Each feat costs 2 ASI points.
// @Tags Universe
// @Produce json
// @Success 200 {object} map[string]interface{} "List of feats"
// @Router /universe/feats [get]
func handleUniverseFeats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	featList := []map[string]interface{}{}
	for slug, feat := range availableFeats {
		featList = append(featList, map[string]interface{}{
			"slug":         slug,
			"name":         feat.Name,
			"category":     feat.Category,
			"prerequisite": featPrereqText(feat.Prerequisite),
			"description":  feat.Description,
			"benefits":     feat.Benefits,
		})
	}
	sort.Slice(featList, func(i, j int) bool {
		ci, cj := fmt.Sprint(featList[i]["category"]), fmt.Sprint(featList[j]["category"])
		if ci != cj {
			return ci < cj
		}
		return fmt.Sprint(featList[i]["name"]) < fmt.Sprint(featList[j]["name"])
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"feats":       featList,
		"count":       len(featList),
		"cost":        "2 ASI points (one full ASI slot)",
		"how_to_take": "POST /api/characters/{id}/feat with {\"feat\": \"slug\"}",
		"note":        "The feats of the SRD 5.2.1. Ability Score Improvement is itself a feat; Epic Boon feats need character level 19.",
	})
}

// handleUniverseFeat godoc
// @Summary Get feat details
// @Description Returns full feat information including prerequisites, benefits, and features
// @Tags Universe
// @Produce json
// @Param slug path string true "Feat slug (e.g., grappler, alert, lucky)"
// @Success 200 {object} map[string]interface{} "Feat details"
// @Failure 404 {object} map[string]interface{} "Feat not found"
// @Router /universe/feats/{slug} [get]
func handleUniverseFeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	slug := strings.TrimPrefix(r.URL.Path, "/api/universe/feats/")
	slug = strings.ToLower(strings.TrimSpace(slug))

	feat, ok := availableFeats[slug]
	if !ok {
		// List available feats
		featSlugs := []string{}
		for s := range availableFeats {
			featSlugs = append(featSlugs, s)
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":           "feat_not_found",
			"message":         fmt.Sprintf("Feat '%s' not found", slug),
			"available_feats": featSlugs,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"slug":          slug,
		"name":          feat.Name,
		"description":   feat.Description,
		"category":      feat.Category,
		"prerequisite":  featPrereqText(feat.Prerequisite),
		"benefits":      feat.Benefits,
		"ability_bonus": feat.AbilityBonus,
		"features":      feat.Features,
		"cost":          "2 ASI points",
		"how_to_take":   fmt.Sprintf("POST /api/characters/{id}/feat with {\"feat\": \"%s\"}", slug),
	})
}

// handleUniverseSubclasses godoc
// @Summary List all subclasses
// @Description Returns all available subclasses from the SRD, optionally filtered by class
// @Tags Universe
// @Produce json
// @Param class query string false "Filter by parent class (e.g., fighter, rogue)"
// @Success 200 {object} map[string]interface{} "List of subclasses"
// @Router /universe/subclasses [get]
func handleUniverseSubclasses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	classFilter := strings.ToLower(r.URL.Query().Get("class"))

	subclassList := []map[string]interface{}{}
	for slug, sub := range game.AvailableSubclasses {
		if classFilter != "" && strings.ToLower(sub.Class) != classFilter {
			continue
		}
		subclassList = append(subclassList, map[string]interface{}{
			"slug":           slug,
			"name":           sub.Name,
			"class":          sub.Class,
			"subclass_level": sub.SubclassLevel,
			"description":    sub.Description,
		})
	}

	// Sort by class then name for consistent output
	sort.Slice(subclassList, func(i, j int) bool {
		if subclassList[i]["class"].(string) != subclassList[j]["class"].(string) {
			return subclassList[i]["class"].(string) < subclassList[j]["class"].(string)
		}
		return subclassList[i]["name"].(string) < subclassList[j]["name"].(string)
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"subclasses":    subclassList,
		"count":         len(subclassList),
		"how_to_choose": "POST /api/characters/{id}/subclass with {\"subclass\": \"slug\"}",
		"note":          "Every class chooses its subclass at level 3 (SRD 5.2.1).",
	})
}

// handleUniverseSubclass godoc
// @Summary Get subclass details
// @Description Returns full subclass information including all features and mechanical effects
// @Tags Universe
// @Produce json
// @Param slug path string true "Subclass slug (e.g., champion, thief, life)"
// @Success 200 {object} map[string]interface{} "Subclass details"
// @Failure 404 {object} map[string]interface{} "Subclass not found"
// @Router /universe/subclasses/{slug} [get]
func handleUniverseSubclass(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	slug := strings.TrimPrefix(r.URL.Path, "/api/universe/subclasses/")
	slug = strings.ToLower(strings.TrimSpace(slug))

	sub, ok := game.AvailableSubclasses[slug]
	if !ok {
		// List available subclasses
		subSlugs := []string{}
		for s := range game.AvailableSubclasses {
			subSlugs = append(subSlugs, s)
		}
		sort.Strings(subSlugs)
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":                "subclass_not_found",
			"message":              fmt.Sprintf("Subclass '%s' not found", slug),
			"available_subclasses": subSlugs,
		})
		return
	}

	// Format features for display
	featuresInfo := []map[string]interface{}{}
	for _, f := range sub.Features {
		featuresInfo = append(featuresInfo, map[string]interface{}{
			"name":        f.Name,
			"level":       f.Level,
			"description": f.Description,
			"mechanics":   f.Mechanics,
		})
	}

	response := map[string]interface{}{
		"slug":           slug,
		"name":           sub.Name,
		"class":          sub.Class,
		"subclass_level": sub.SubclassLevel,
		"description":    sub.Description,
		"features":       featuresInfo,
		"how_to_choose":  fmt.Sprintf("POST /api/characters/{id}/subclass with {\"subclass\": \"%s\"}", slug),
	}

	// Include domain spells if this subclass has them
	if sub.DomainSpells != nil && len(sub.DomainSpells) > 0 {
		// Enrich with spell names from SRD
		domainSpellsInfo := map[string][]map[string]interface{}{}
		for level, slugs := range sub.DomainSpells {
			levelKey := fmt.Sprintf("level_%d", level)
			spellsAtLevel := []map[string]interface{}{}
			for _, spellSlug := range slugs {
				spellInfo := map[string]interface{}{
					"slug":            spellSlug,
					"always_prepared": true,
				}
				if spell, ok := srdSpellsMemory[spellSlug]; ok {
					spellInfo["name"] = spell.Name
					spellInfo["spell_level"] = spell.Level
					spellInfo["school"] = spell.School
				}
				spellsAtLevel = append(spellsAtLevel, spellInfo)
			}
			domainSpellsInfo[levelKey] = spellsAtLevel
		}
		response["domain_spells"] = domainSpellsInfo
		response["domain_spells_note"] = "Always prepared spells granted by this subclass at the indicated character level"
	}

	json.NewEncoder(w).Encode(response)
}

// handleUniverseRules godoc
// @Summary The Rules Glossary
// @Description The SRD 5.2.1 Rules Glossary, word for word: every entry's name, kind (Action, Condition, Hazard, Attitude, Area of Effect ...) and slug. ?kind=Action filters. GET /api/universe/rules/{slug} gives an entry's text. The same glossary is the page /universe/rules.
// @Tags Universe
// @Produce json
// @Param kind query string false "Only entries of this kind"
// @Success 200 {object} map[string]interface{} "Glossary entries"
// @Router /universe/rules [get]
func handleUniverseRules(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	var list []map[string]string
	for _, en := range srdGlossary() {
		k := en.Kind
		if k == "" {
			k = "Rule"
		}
		if kind != "" && strings.ToLower(k) != kind {
			continue
		}
		list = append(list, map[string]string{"name": en.Name, "kind": k, "slug": glossarySlug(en.Name)})
	}
	writeJSONStatus(w, 200, map[string]interface{}{"count": len(list), "entries": list, "source": "SRD 5.2.1 Rules Glossary", "usage": "GET /api/universe/rules/{slug}"})
}

// handleUniverseRule godoc
// @Summary A Rules Glossary entry
// @Description One entry of the SRD 5.2.1 Rules Glossary, word for word (slug from GET /api/universe/rules, for example influence, hide, grappled, falling).
// @Tags Universe
// @Produce json
// @Param slug path string true "Entry slug"
// @Success 200 {object} map[string]interface{} "The entry"
// @Router /universe/rules/{slug} [get]
func handleUniverseRule(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/universe/rules/"), "/"))
	slug = strings.ReplaceAll(slug, "_", "-")
	for _, en := range srdGlossary() {
		if glossarySlug(en.Name) == slug {
			k := en.Kind
			if k == "" {
				k = "Rule"
			}
			writeJSONStatus(w, 200, map[string]interface{}{"name": en.Name, "kind": k, "text": en.Text, "source": "SRD 5.2.1 Rules Glossary"})
			return
		}
	}
	writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "not_found", "message": "No such glossary entry; GET /api/universe/rules lists them."})
}

// handleUniverseMonsterSearch godoc
// @Summary Search monsters
// @Description Search and filter monsters by name, type, or CR
// @Tags Universe
// @Produce json
// @Param name query string false "Filter by name (partial match)"
// @Param type query string false "Filter by type (e.g., humanoid, beast)"
// @Param cr query string false "Filter by challenge rating"
// @Param limit query int false "Max results (default 20)"
// @Success 200 {object} map[string]interface{} "Search results"
// @Router /universe/monsters/search [get]
func handleUniverseMonsterSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.URL.Query().Get("q") // Also accept 'q' for search box
	}
	mtype := r.URL.Query().Get("type")
	cr := r.URL.Query().Get("cr")
	gmView := requestIsGM(r)
	if !gmView {
		cr = "" // challenge rating is a stat too
	}
	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	query := "SELECT slug, name, type, cr, hp, ac FROM monsters WHERE " + rulesetFilter(r)
	args := []interface{}{}
	argNum := 1

	if name != "" {
		query += fmt.Sprintf(" AND LOWER(name) LIKE LOWER($%d)", argNum)
		args = append(args, "%"+name+"%")
		argNum++
	}
	if mtype != "" {
		query += fmt.Sprintf(" AND LOWER(type) = LOWER($%d)", argNum)
		args = append(args, mtype)
		argNum++
	}
	if cr != "" {
		query += fmt.Sprintf(" AND cr = $%d", argNum)
		args = append(args, cr)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY name LIMIT $%d", argNum)
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()

	monsters := []map[string]interface{}{}
	for rows.Next() {
		var slug, mname, mtype, cr string
		var hp, ac int
		rows.Scan(&slug, &mname, &mtype, &cr, &hp, &ac)
		if !gmView {
			monsters = append(monsters, map[string]interface{}{"slug": slug, "name": mname, "type": mtype})
			continue
		}
		monsters = append(monsters, map[string]interface{}{
			"slug": slug, "name": mname, "type": mtype, "cr": cr, "hp": hp, "ac": ac,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"monsters": monsters, "count": len(monsters)})
}

// handleUniverseSpellSearch godoc
// @Summary Search spells
// @Description Search and filter spells by name, level, or school
// @Tags Universe
// @Produce json
// @Param name query string false "Filter by name (partial match)"
// @Param level query int false "Filter by spell level (0-9)"
// @Param school query string false "Filter by school (e.g., evocation, necromancy)"
// @Param limit query int false "Max results (default 20)"
// @Success 200 {object} map[string]interface{} "Search results"
// @Router /universe/spells/search [get]
func handleUniverseSpellSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.URL.Query().Get("q") // Also accept 'q' for search box
	}
	levelStr := r.URL.Query().Get("level")
	school := r.URL.Query().Get("school")
	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	query := "SELECT slug, name, level, school, casting_time, range FROM spells WHERE 1=1"
	args := []interface{}{}
	argNum := 1

	if name != "" {
		query += fmt.Sprintf(" AND LOWER(name) LIKE LOWER($%d)", argNum)
		args = append(args, "%"+name+"%")
		argNum++
	}
	if levelStr != "" {
		if level, err := strconv.Atoi(levelStr); err == nil {
			query += fmt.Sprintf(" AND level = $%d", argNum)
			args = append(args, level)
			argNum++
		}
	}
	if school != "" {
		query += fmt.Sprintf(" AND LOWER(school) = LOWER($%d)", argNum)
		args = append(args, school)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY level, name LIMIT $%d", argNum)
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()

	spells := []map[string]interface{}{}
	for rows.Next() {
		var slug, sname, school, castTime, srange string
		var level int
		rows.Scan(&slug, &sname, &level, &school, &castTime, &srange)
		spells = append(spells, map[string]interface{}{
			"slug": slug, "name": sname, "level": level, "school": school, "casting_time": castTime, "range": srange,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"spells": spells, "count": len(spells)})
}

// handleUniverseWeaponSearch godoc
// @Summary Search weapons
// @Description Search and filter weapons by name or type
// @Tags Universe
// @Produce json
// @Param name query string false "Filter by name (partial match)"
// @Param type query string false "Filter by type (e.g., simple melee, martial ranged)"
// @Param limit query int false "Max results (default 20)"
// @Success 200 {object} map[string]interface{} "Search results"
// @Router /universe/weapons/search [get]
func handleUniverseWeaponSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.URL.Query().Get("q") // Also accept 'q' for search box
	}
	wtype := r.URL.Query().Get("type")
	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	query := "SELECT slug, name, COALESCE(data->>'type', ''), COALESCE(data->>'damage', ''), COALESCE(data->>'damage_type', ''), COALESCE(data->>'properties_text', '') FROM items WHERE ruleset = '2024' AND category = 'weapon'"
	args := []interface{}{}
	argNum := 1

	if name != "" {
		query += fmt.Sprintf(" AND LOWER(name) LIKE LOWER($%d)", argNum)
		args = append(args, "%"+name+"%")
		argNum++
	}
	if wtype != "" {
		query += fmt.Sprintf(" AND LOWER(data->>'type') LIKE LOWER($%d)", argNum)
		args = append(args, "%"+wtype+"%")
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY name LIMIT $%d", argNum)
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	defer rows.Close()

	weapons := []map[string]interface{}{}
	for rows.Next() {
		var slug, wname, wtype, damage, damageType, props string
		rows.Scan(&slug, &wname, &wtype, &damage, &damageType, &props)
		weapons = append(weapons, map[string]interface{}{
			"slug": slug, "name": wname, "type": wtype, "damage": damage, "damage_type": damageType, "properties": props,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"weapons": weapons, "count": len(weapons)})
}

// getUniverseItem looks up an item in the universe (weapons or armor tables)
func getUniverseItem(slug string) (map[string]interface{}, string, error) {
	// Try weapons first
	var name, wtype, damage, damageType, props string
	var weight float64
	err := db.QueryRow(`
		SELECT name, type, damage, damage_type, weight, properties 
		FROM weapons WHERE slug = $1
	`, slug).Scan(&name, &wtype, &damage, &damageType, &weight, &props)

	if err == nil {
		return map[string]interface{}{
			"name":        name,
			"type":        wtype,
			"damage":      damage,
			"damage_type": damageType,
			"weight":      weight,
			"properties":  props,
		}, "weapon", nil
	}

	// Try armor
	var atype, acBonus string
	var ac, strReq int
	var stealth bool
	err = db.QueryRow(`
		SELECT name, type, ac, ac_bonus, str_req, stealth_disadvantage, weight 
		FROM armor WHERE slug = $1
	`, slug).Scan(&name, &atype, &ac, &acBonus, &strReq, &stealth, &weight)

	if err == nil {
		return map[string]interface{}{
			"name":                 name,
			"type":                 atype,
			"ac":                   ac,
			"ac_bonus":             acBonus,
			"str_req":              strReq,
			"stealth_disadvantage": stealth,
			"weight":               weight,
		}, "armor", nil
	}

	return nil, "", fmt.Errorf("item not found")
}

// backgroundJSON describes a 2024 background for the universe endpoints.
func backgroundJSON(bg game.Background) map[string]interface{} {
	m := backgroundData(bg)
	if it, ok := originByName(srdOrigins().Backgrounds.Items, bg.Name); ok {
		m["srd_fields"] = it.Fields // the label lines as the SRD prints them
	}
	return m
}

func backgroundData(bg game.Background) map[string]interface{} {
	return map[string]interface{}{
		"key": bg.Key, "name": bg.Name, "description": bg.Description, "ability_scores": bg.AbilityScores,
		"ability_score_rule": "Increase one of these by 2 and another by 1, or all three by 1 (send ability_increases)",
		"feat":               bg.Feat, "feat_note": bg.FeatNote, "skill_proficiencies": bg.Skills,
		"tool_proficiency": bg.Tool, "tool_choice_category": bg.ToolCategory,
		"equipment_a": bg.Equipment, "gold_a": bg.GoldA, "equipment_b": "50 GP", "gold_b": bg.GoldB,
	}
}
