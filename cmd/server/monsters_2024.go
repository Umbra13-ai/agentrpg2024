package main

// The monsters of the SRD 5.2.1 (2024 rules): 330 stat blocks (Monsters A-Z and Animals), extracted from the SRD PDF by
// tools/extractors/extract_monsters.py into data/srd521_monsters.json and loaded at every startup. Rows of any other ruleset are deleted at startup. The saving throw bonuses are kept per ability (saves).
// The SRD is licensed CC-BY-4.0.

import (
	_ "embed"
	"encoding/json"
	"log"
	"regexp"
	"strconv"
	"strings"
)

//go:embed data/srd521_monsters.json
var srd521MonstersJSON []byte

// monster_lore.json: original appearance and lore text for players (the SRD has none), written for this project
// (tools/monster_lore). Keyed by slug.
//
//go:embed data/monster_lore.json
var monsterLoreJSON []byte

func monsterLore() map[string]struct{ Description, Lore string } {
	m := map[string]struct{ Description, Lore string }{}
	if err := json.Unmarshal(monsterLoreJSON, &m); err != nil {
		log.Printf("monster lore: %v", err)
	}
	return m
}

type srdMonsterEntry struct {
	Name      string          `json:"name"`                // the entry's name, with its usage ("Bite", "Fire Breath (Recharge 5–6)")
	Text      string          `json:"text"`                // the entry's words, as the stat block prints them
	Mechanics []entryMechanic `json:"mechanics,omitempty"` // structured mechanics the GM attaches (GET /api/gm/compendium/mechanics; monster_mechanics_2024.go)
}

// abilityLine: one ability of a stat block (score, modifier, saving throw). saveGiven says the saving throw was written (a JSON "Save" key, or
// the text's third number): a written +0 is a proficient save of a creature whose modifier is -2, never "left out" (normalizeCustomMonster).
type abilityLine struct {
	Score, Mod, Save int
	saveGiven        bool
}

// UnmarshalJSON reads the line as encoding/json would and notes whether the saving throw was in it.
func (a *abilityLine) UnmarshalJSON(b []byte) error {
	type plain abilityLine
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	json.Unmarshal(b, &keys)
	*a = abilityLine(p)
	for k, v := range keys {
		if strings.EqualFold(k, "save") && string(v) != "null" {
			a.saveGiven = true
		}
	}
	return nil
}

type srdMonster2024 struct {
	Name         string                   `json:"name"`
	Group        string                   `json:"group"`
	Size         string                   `json:"size"`
	Type         string                   `json:"type"`
	Alignment    string                   `json:"alignment"`
	AC           int                      `json:"ac"`
	Initiative   struct{ Mod, Score int } `json:"initiative"`
	HP           int                      `json:"hp"`
	HPFormula    string                   `json:"hit_points_formula"`
	Speed        string                   `json:"speed"`
	Abilities    map[string]abilityLine   `json:"abilities"`
	Skills       string                   `json:"skills"`
	Vulnerable   string                   `json:"damage_vulnerabilities"`
	Resistances  string                   `json:"damage_resistances"`
	DmgImmune    string                   `json:"damage_immunities"`
	CondImmune   string                   `json:"condition_immunities"`
	Gear         string                   `json:"gear"`
	Senses       string                   `json:"senses"`
	Languages    string                   `json:"languages"`
	CR           string                   `json:"cr"`
	XP           int                      `json:"xp"`
	XPLair       int                      `json:"xp_lair"`
	PB           int                      `json:"proficiency_bonus"`
	Traits       []srdMonsterEntry        `json:"traits"`
	Actions      []srdMonsterEntry        `json:"actions"`
	BonusActions []srdMonsterEntry        `json:"bonus_actions"`
	Reactions    []srdMonsterEntry        `json:"reactions"`
	Legendary    []srdMonsterEntry        `json:"legendary_actions"`
	LegendaryIn  string                   `json:"legendary_intro"`
}

func srdMonsters2024() []srdMonster2024 {
	var m []srdMonster2024
	if err := json.Unmarshal(srd521MonstersJSON, &m); err != nil {
		log.Printf("monsters 2024: %v", err)
	}
	return m
}

func monsterSlug(name string) string {
	s := strings.ToLower(name)
	s = strings.NewReplacer("'", "", "’", "").Replace(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

var (
	speedNumRe     = regexp.MustCompile(`\d+`)
	usesRe         = regexp.MustCompile(`Legendary Action Uses: (\d+)`)
	legendaryResRe = regexp.MustCompile(`Legendary Resistance \((\d+)/Day`)
)

// oldStyleActions converts the actions into the shape the game's monster code reads:
// name, attack bonus, damage dice and type, and the text.
func oldStyleActions(entries []srdMonsterEntry) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, e := range entries {
		parsed := entryActionRow(e) // the entry's row with its attached mechanics (monster_mechanics_2024.go)
		a := map[string]interface{}{"name": parsed.Name, "desc": parsed.Desc, "attack_bonus": parsed.AttackBonus, "damage_dice": parsed.DamageDice, "damage_type": parsed.DamageType}
		if parsed.FlatDamage != nil {
			a["flat_damage"] = *parsed.FlatDamage
		}
		if len(parsed.ChosenTypes) > 0 {
			a["chosen_types"] = parsed.ChosenTypes
		}

		out = append(out, a)
	}
	return out
}

// srdMonsterCatalog is the SQL condition of the SRD 5.2.1 monster catalogue: the 330 stat blocks seeded from srd521_monsters.json.
// The monsters table also holds creatures an item's own text gives (the Figurine of Wondrous Power's Giant Fly, the Mysterious Deck's
// Avatar of Death: source itemCreatureSource), reached only by their slug from that item; every list, count, search and choice of
// the catalogue (the Universe page, GET /api/universe/monsters, the beast lists of Wild Shape, pets and spells) reads this condition.
const (
	srdMonsterCatalog  = "source = 'srd521'"
	itemCreatureSource = "srd521-item"
)

func seedMonsters2024() {
	if db == nil {
		return
	}
	if res, err := db.Exec(`DELETE FROM monsters WHERE ruleset IS DISTINCT FROM '2024'`); err == nil { // SRD 5.2.1 monsters only
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("Removed %d monsters that are not in the SRD 5.2.1", n)
		}
	}
	lore := monsterLore()
	n := 0
	for _, m := range srdMonsters2024() {
		slug := monsterSlug(m.Name)
		saves := map[string]int{}
		for k, a := range m.Abilities {
			saves[k] = a.Save
		}
		get := func(k string) int { return m.Abilities[k].Score }
		speed := 30
		if x := speedNumRe.FindString(m.Speed); x != "" {
			speed, _ = strconv.Atoi(x)
		}
		baseType := strings.ToLower(strings.TrimSpace(strings.SplitN(m.Type, "(", 2)[0]))
		hitDice := strings.TrimSpace(strings.SplitN(m.HPFormula, "+", 2)[0])
		hitDice = strings.TrimSpace(strings.SplitN(hitDice, "-", 2)[0])
		legendaryUses := 0
		if x := usesRe.FindStringSubmatch(m.LegendaryIn); x != nil {
			legendaryUses, _ = strconv.Atoi(x[1])
		}
		legendary := []map[string]interface{}{}
		for _, e := range m.Legendary {
			legendary = append(legendary, map[string]interface{}{"name": e.Name, "desc": e.Text, "cost": 1})
		}
		js := func(v interface{}) string { b, _ := json.Marshal(v); return string(b) }
		_, err := db.Exec(`INSERT INTO monsters (slug, name, size, type, ac, hp, hit_dice, speed, str, dex, con, intl, wis, cha, cr, xp, actions, source,
				ruleset, alignment, speed_text, initiative_mod, proficiency_bonus, saves, skills, senses, languages, gear, hp_formula, xp_lair, group_name,
				traits, action_entries, bonus_actions, reactions, legendary_intro, legendary_actions, legendary_action_count,
				damage_resistances, damage_immunities, damage_vulnerabilities, condition_immunities)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'srd521','2024',$18,$19,$20,$21,$22::jsonb,$23,$24,$25,$26,$27,$28,$29,
				$30::jsonb,$31::jsonb,$32::jsonb,$33::jsonb,$34,$35::jsonb,$36,$37,$38,$39,$40)
			ON CONFLICT (slug) DO UPDATE SET name=EXCLUDED.name, size=EXCLUDED.size, type=EXCLUDED.type, ac=EXCLUDED.ac, hp=EXCLUDED.hp,
				hit_dice=EXCLUDED.hit_dice, speed=EXCLUDED.speed, str=EXCLUDED.str, dex=EXCLUDED.dex, con=EXCLUDED.con, intl=EXCLUDED.intl,
				wis=EXCLUDED.wis, cha=EXCLUDED.cha, cr=EXCLUDED.cr, xp=EXCLUDED.xp, actions=EXCLUDED.actions, source=EXCLUDED.source,
				ruleset=EXCLUDED.ruleset, alignment=EXCLUDED.alignment, speed_text=EXCLUDED.speed_text, initiative_mod=EXCLUDED.initiative_mod,
				proficiency_bonus=EXCLUDED.proficiency_bonus, saves=EXCLUDED.saves, skills=EXCLUDED.skills, senses=EXCLUDED.senses,
				languages=EXCLUDED.languages, gear=EXCLUDED.gear, hp_formula=EXCLUDED.hp_formula, xp_lair=EXCLUDED.xp_lair,
				group_name=EXCLUDED.group_name, traits=EXCLUDED.traits, action_entries=EXCLUDED.action_entries,
				bonus_actions=EXCLUDED.bonus_actions, reactions=EXCLUDED.reactions, legendary_intro=EXCLUDED.legendary_intro,
				legendary_actions=EXCLUDED.legendary_actions, legendary_action_count=EXCLUDED.legendary_action_count,
				damage_resistances=EXCLUDED.damage_resistances, damage_immunities=EXCLUDED.damage_immunities,
				damage_vulnerabilities=EXCLUDED.damage_vulnerabilities, condition_immunities=EXCLUDED.condition_immunities`,
			slug, m.Name, strings.ToLower(m.Size), baseType, m.AC, m.HP, hitDice, speed, get("str"), get("dex"), get("con"), get("int"), get("wis"), get("cha"),
			m.CR, m.XP, js(oldStyleActions(m.Actions)),
			m.Alignment, m.Speed, m.Initiative.Mod, m.PB, js(saves), m.Skills, m.Senses, m.Languages, m.Gear, m.HPFormula, nullInt(m.XPLair), m.Group,
			js(m.Traits), js(m.Actions), js(m.BonusActions), js(m.Reactions), m.LegendaryIn, js(legendary), legendaryUses,
			strings.ToLower(m.Resistances), strings.ToLower(m.DmgImmune), strings.ToLower(m.Vulnerable), strings.ToLower(m.CondImmune))
		if err != nil {
			log.Printf("monster %s: %v", m.Name, err)
			continue
		}
		n++
		if l, ok := lore[slug]; ok {
			db.Exec(`UPDATE monsters SET description = $1, lore = $2 WHERE slug = $3`, l.Description, l.Lore, slug)
		}
		lr := 0
		for _, tr := range m.Traits {
			if x := legendaryResRe.FindStringSubmatch(tr.Name); x != nil {
				lr, _ = strconv.Atoi(x[1])
			}
		}
		db.Exec(`UPDATE monsters SET legendary_resistances = $1 WHERE slug = $2`, lr, slug)
	}
	log.Printf("Monsters 2024: %d stat blocks loaded", n)
}

func nullInt(n int) interface{} {
	if n == 0 {
		return nil
	}
	return n
}

// monsterSaveBonus is a monster's saving throw bonus for an ability ("str", "dex", ...): the bonus printed in its
// stat block when it has one (2024 data), otherwise the ability modifier from the score.
func monsterSaveBonus(monsterKey, ability string, score int) int {
	if db != nil && monsterKey != "" {
		var raw []byte
		if monsterQueryRow(`SELECT saves FROM monsters WHERE slug = $1`, monsterKey).Scan(&raw) == nil && len(raw) > 0 {
			saves := map[string]int{}
			if json.Unmarshal(raw, &saves) == nil {
				if v, ok := saves[ability]; ok {
					return v
				}
			}
		}
	}
	return (score - 10) / 2
}
