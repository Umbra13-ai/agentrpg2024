package main

// Ranger, 2024 rules (SRD 5.2.1).
//	Favored Enemy      Hunter's Mark is always prepared and can be cast without a spell slot a number of times per Long Rest
//	                   (the Favored Enemy column of the Features table): write "favored enemy" or "free" in the cast description.
//	Hunter's Mark      the extra damage applies to monsters (target_id on the cast), is 1d6 (1d10 from level 20, Foe Slayer),
//	                   and from level 17 (Precise Hunter) attack rolls against the marked creature have Advantage.
//	Relentless Hunter  (level 13) damage does not break Concentration on Hunter's Mark.
//	Tireless / Nature's Veil (levels 10 and 14): actions "tireless" and "natures_veil".

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// freeCastResource lets a class cast one particular spell without a slot a limited number of times per Long Rest.
// It returns the resource key, the number of uses, and whether this spell/class qualifies.
func freeCastResource(class string, level int, spellKey string) (string, int, bool) {
	c := strings.ToLower(class)
	switch {
	case c == "ranger" && (spellKey == "hunters-mark" || spellKey == "hunter's-mark"):
		return "favored_enemy", game.ClassTableInt("ranger", level, "Favored Enemy"), true
	case c == "paladin" && spellKey == "find-steed" && level >= 5:
		return "faithful_steed", 1, true
	case c == "warlock" && spellKey == "contact-other-plane" && level >= 9:
		return "contact_patron", 1, true // Contact Patron: no slot, and the saving throw is an automatic success
	}
	return "", 0, false
}

// divineInterventionRestsLeft: Long Rests still needed after Greater Divine Intervention (Wish) before Divine Intervention returns.
func divineInterventionRestsLeft(charID int) int {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS divine_intervention_rests INT DEFAULT 0`)
	var n int
	db.QueryRow(`SELECT COALESCE(divine_intervention_rests,0) FROM characters WHERE id = $1`, charID).Scan(&n)
	return n
}

func wantsFreeCast(description string) bool {
	low := strings.ToLower(description)
	return strings.Contains(low, "favored enemy") || strings.Contains(low, "faithful steed") || strings.Contains(low, "contact patron") || strings.Contains(low, "wild companion") || strings.Contains(low, "natural recovery") || strings.Contains(low, "spell mastery") || hasWord(low, "signature") || strings.Contains(low, "dragon companion") || hasWord(low, "free") || // whole word: "freedom of movement" is not a free cast
		strings.Contains(low, "no slot") || strings.Contains(low, "without a slot")
}

// tryFreeCast spends a use of the free-cast resource. It returns a note when the cast is free (no slot is spent),
// an error text when the character asked for a free cast it cannot have, or "" "" when the normal slot is used.
func tryFreeCast(charID int, class string, level int, spellKey, description string, spellLevel int) (note, errText string) {
	if n, e, handled := originFreeCast(charID, class, level, spellKey, description); handled { // species lineage spells, Magic Initiate
		return n, e
	}
	key, max, ok := freeCastResource(class, level, spellKey)
	if !ok {
		key, max, ok = specialFreeCast(charID, class, level, spellKey, description)
	}
	greaterWish := false
	if strings.ToLower(class) == "cleric" && level >= 10 && strings.Contains(strings.ToLower(description), "divine intervention") {
		greaterWish = level >= 20 && spellKey == "wish" // Greater Divine Intervention (level 20): Wish, then 2d4 Long Rests
		if spellLevel > 5 && !greaterWish {
			return "", "Cannot cast this spell with Divine Intervention: it casts a Cleric spell of level 5 or lower (Wish too from level 20, Greater Divine Intervention)."
		}
		if left := divineInterventionRestsLeft(charID); left > 0 {
			return "", fmt.Sprintf("Cannot use Divine Intervention: after Greater Divine Intervention (Wish) it returns after %d more Long Rest(s).", left)
		}
		key, max, ok = "divine_intervention", 1, true // once per Long Rest
	} else if !ok || !wantsFreeCast(description) {
		return "", ""
	}
	var usedJSON []byte
	db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&usedJSON)
	used := map[string]int{}
	json.Unmarshal(usedJSON, &used)
	if used[key] >= max {
		return "", fmt.Sprintf("Cannot cast it free: you have no free casts left (%d of %d used); finish a Long Rest or cast it with a spell slot.", used[key], max)
	}
	used[key]++
	b, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, string(b), charID)
	if greaterWish {
		rests := game.RollDie(4) + game.RollDie(4)
		db.Exec(`UPDATE characters SET divine_intervention_rests = $1 WHERE id = $2`, rests, charID)
		return fmt.Sprintf(" [Greater Divine Intervention: Wish with no spell slot; Divine Intervention returns after %d Long Rests (2d4)]", rests), ""
	}
	if key == "divine_intervention" {
		return " [Divine Intervention: no spell slot and no Material components; again after a Long Rest]", ""
	}
	if key == "contact_patron" {
		return " [Contact Patron: no spell slot, and you automatically succeed on the saving throw]", ""
	}
	if strings.HasPrefix(key, "signature_") {
		return " [Signature Spells: no spell slot; again after a Short or Long Rest]", ""
	}
	switch key {
	case "spell_mastery_free":
		return " [Spell Mastery: no spell slot]", ""
	case "wild_shape":
		return " [Wild Companion: a use of Wild Shape instead of a spell slot; the familiar is a Fey and stays until you finish a Long Rest]", ""
	case "dragon_companion":
		return " [Dragon Companion: Summon Dragon without a spell slot or a Material component; you may drop Concentration for a 1 minute duration]", ""
	case "natural_recovery_cast":
		return " [Natural Recovery: Circle spell without a spell slot]", ""
	}
	return fmt.Sprintf(" [free cast: %d of %d left]", max-used[key], max), ""
}

// markedTargetID is the combat id of the creature a character's Hunter's Mark or Hex is on (0 when there is none).
func markedTargetID(charID int) int {
	var conc string
	if db.QueryRow(`SELECT COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, charID).Scan(&conc) != nil || conc == "" {
		return 0
	}
	_, id := parseMarkSpell(conc)
	return id
}

// preciseHunter: a Ranger of level 17+ has Advantage on attack rolls against the creature marked by Hunter's Mark.
func preciseHunter(charID int) bool {
	var class string
	var level int
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) != nil || strings.ToLower(class) != "ranger" || level < 17 {
		return false
	}
	conc := ""
	db.QueryRow(`SELECT COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, charID).Scan(&conc)
	if slug, _ := parseMarkSpell(conc); slug != "hunters-mark" {
		return false
	}
	_, tgt, ok := attackedMonster(charID)
	return ok && tgt == markedTargetID(charID)
}

// markDieSides is the damage die of Hunter's Mark: d6, d10 from level 20 (Foe Slayer).
func markDieSides(charID int) int {
	var class string
	var level int
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) == nil && strings.ToLower(class) == "ranger" && level >= 20 {
		return 10
	}
	return 6
}

// relentlessHunter: from level 13 a Ranger's Concentration on Hunter's Mark can't be broken by damage.
func relentlessHunter(charID int, concentratingOn string) bool {
	slug, _ := parseMarkSpell(concentratingOn)
	if slug != "hunters-mark" {
		return false
	}
	var class string
	var level int
	return db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) == nil && strings.ToLower(class) == "ranger" && level >= 13
}

// tireless is the Magic action of Tireless (level 10): Temporary Hit Points 1d8 + Wisdom modifier, Wisdom-modifier uses per Long Rest.
func tireless(charID int) string {
	return useWisdomFeature(charID, "tireless", 10, func(wis, level int) string {
		hp := game.RollDie(8) + game.Modifier(wis)
		if hp < 1 {
			hp = 1
		}
		db.Exec(`UPDATE characters SET temp_hp = GREATEST(COALESCE(temp_hp,0), $1) WHERE id = $2`, hp, charID)
		return fmt.Sprintf("🌿 Tireless: you gain %d Temporary Hit Points.", hp)
	})
}

// naturesVeil is the Bonus Action of Nature's Veil (level 14): Invisible until the end of your next turn.
func naturesVeil(charID int) string {
	return useWisdomFeature(charID, "natures_veil", 14, func(wis, level int) string {
		addCharacterCondition(charID, "invisible")
		return "🌿 Nature's Veil: you have the Invisible condition until the end of your next turn."
	})
}

func useWisdomFeature(charID int, key string, minLevel int, run func(wis, level int) string) string {
	var class string
	var level, wis int
	var usedJSON []byte
	if db.QueryRow(`SELECT class, level, wis, COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &wis, &usedJSON) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "ranger" || level < minLevel {
		return fmt.Sprintf("This needs a Ranger of level %d or higher.", minLevel)
	}
	max := game.Modifier(wis)
	if max < 1 {
		max = 1
	}
	used := map[string]int{}
	json.Unmarshal(usedJSON, &used)
	if used[key] >= max {
		return "You have used all your uses; finish a Long Rest."
	}
	used[key]++
	b, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, string(b), charID)
	return run(wis, level) + fmt.Sprintf(" (%d of %d uses left)", max-used[key], max)
}

func addCharacterCondition(charID int, cond string) {
	if heroismImmune(lobbyOfChar(charID), charID, cond) {
		return
	}
	conds := getCharConditions(charID)
	for _, c := range conds {
		if c == cond {
			return
		}
	}
	conds = append(conds, cond)
	b, _ := json.Marshal(conds)
	db.Exec(`UPDATE characters SET conditions = $1 WHERE id = $2`, string(b), charID)
	syncRecordsFromList(charID)
	endWildShapeIfDown(charID) // an Incapacitating condition ends Wild Shape
}

// classSpeedBonus is the Speed a class feature adds: Barbarian Fast Movement (level 5) and Ranger Roving (level 6) when not wearing
// Heavy armor, Monk Unarmored Movement (the Features table) when wearing no armor and no Shield.
func classSpeedBonus(charID int) int {
	var class string
	var level int
	var armor sql.NullString
	var shield bool
	if db.QueryRow(`SELECT class, level, equipped_armor, COALESCE(equipped_shield,false) FROM characters WHERE id = $1`, charID).Scan(&class, &level, &armor, &shield) != nil {
		return 0
	}
	heavy := false
	if armor.Valid && armor.String != "" {
		if info, err := getArmorInfo(armor.String); err == nil && info != nil && strings.EqualFold(info.Type, "heavy") {
			heavy = true
		}
	}
	switch strings.ToLower(class) {
	case "barbarian":
		if level >= 5 && !heavy {
			return 10
		}
	case "ranger":
		if level >= 6 && !heavy {
			return 10
		}
	case "monk":
		if (!armor.Valid || armor.String == "") && !shield && level >= 2 {
			return game.ClassTableInt("monk", level, "Unarmored Movement")
		}
	}
	return 0
}

// potentSpellcastingBonus: a Cleric or Druid of level 7 who chose Potent Spellcasting adds the Wisdom modifier to cantrip damage.
func potentSpellcastingBonus(charID int, class string, level, spellLevel int) int {
	c := strings.ToLower(class)
	if spellLevel != 0 || (c != "cleric" && c != "druid") || level < 7 {
		return 0
	}
	var raw []byte
	var wis int
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}'), wis FROM characters WHERE id = $1`, charID).Scan(&raw, &wis)
	var choices map[string]string
	json.Unmarshal(raw, &choices)
	key := "blessed_strikes"
	if c == "druid" {
		key = "elemental_fury"
	}
	if choices[key] != "potent_spellcasting" {
		return 0
	}
	if m := game.Modifier(wis); m > 0 {
		return m
	}
	return 0
}

// divineSense is the Channel Divinity Bonus Action of a Paladin: for 10 minutes you know where Celestials, Fiends and Undead are within 60 feet.
func divineSense(charID int) string {
	var class string
	var level int
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) != nil || strings.ToLower(class) != "paladin" || level < 3 {
		return "Divine Sense is a Channel Divinity effect of a Paladin of level 3 or higher."
	}
	if okRes, msg, _ := useClassResource(charID, "channel_divinity", 1); !okRes {
		return msg
	}
	return "🙏 Divine Sense: for 10 minutes (or until you are Incapacitated) you know the location and creature type of every Celestial, Fiend and Undead within 60 feet, and any consecrated or desecrated place or object there."
}

// hunterLore (Hunter, level 3): while a creature is marked by your Hunter's Mark you know its Immunities, Resistances and Vulnerabilities.
func hunterLore(charID, targetID int, monsterKey string) string {
	var class string
	var sub sql.NullString
	var level int
	if db.QueryRow(`SELECT class, subclass, level FROM characters WHERE id = $1`, charID).Scan(&class, &sub, &level) != nil ||
		strings.ToLower(class) != "ranger" || !sub.Valid || strings.ToLower(sub.String) != "hunter" || level < 3 || markedTargetID(charID) != targetID {
		return ""
	}
	var res, imm, vul, cimm string
	db.QueryRow(`SELECT COALESCE(damage_resistances,''), COALESCE(damage_immunities,''), COALESCE(damage_vulnerabilities,''), COALESCE(condition_immunities,'') FROM monsters WHERE slug = $1`, monsterKey).Scan(&res, &imm, &vul, &cimm)
	var parts []string
	if res != "" {
		parts = append(parts, "Resistances: "+res)
	}
	if imm != "" || cimm != "" {
		parts = append(parts, "Immunities: "+strings.Trim(imm+", "+cimm, ", "))
	}
	if vul != "" {
		parts = append(parts, "Vulnerabilities: "+vul)
	}
	if len(parts) == 0 {
		return " Hunter's Lore: it has no Immunities, Resistances or Vulnerabilities."
	}
	return " Hunter's Lore: " + strings.Join(parts, "; ") + "."
}
