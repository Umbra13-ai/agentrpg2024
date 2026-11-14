package main

// Spells that leave a lasting effect on creatures instead of a condition (SRD 5.2.1):
//	Bless           up to three creatures (+1 per slot level above 1) add 1d4 to attack rolls and saving throws.
//	Bane            up to three creatures (+1 per slot level above 1) that fail a Charisma save subtract 1d4 from attack rolls and saves.
//	Faerie Fire     creatures in a 20-foot Cube that fail a Dexterity save are outlined: attack rolls against them have Advantage
//	                and they can't benefit from the Invisible condition.
//	Slow            up to six creatures in a 40-foot Cube that fail a Wisdom save: Speed halved, -2 to AC and Dexterity saves, no
//	                Reactions, only one attack with the Attack action; the save repeats at the end of each of their turns.
//	Shield of Faith one creature gets +2 to AC.
//	Haste           a willing creature: +2 AC, Advantage on Dexterity saves, Speed doubled, one extra action on each of its turns
//	                (Attack with one attack only, Dash, Disengage, Hide or Utilize). When the spell ends it is Incapacitated
//	                with a Speed of 0 until the end of its next turn.
//	Heroism         immune to Frightened; Temporary Hit Points equal to the caster's spellcasting modifier at the start of its turns.
//	Resistance      the chosen damage type is reduced by 1d4, once per turn.
//	Guidance        +1d4 to ability checks with the chosen skill.
//	Mage Armor      a creature wearing no armor: base AC 13 + its Dexterity modifier for 8 hours (mage_armor_2024.go).
//	Command         creatures (one, +1 per slot level above 1) that fail a Wisdom save follow the command on their next turn:
//	                Approach, Drop, Flee, Grovel (Prone, then the turn ends) or Halt. None of them leaves room to attack.
// The effects of a Concentration spell last while the caster still concentrates on it (characters.concentrating_on) and for
// at most the spell's duration in rounds, counted down at the end of the caster's turns. Monsters (negative ids) and characters
// (positive ids) use the same table.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type activeSpellEffect struct {
	ID          int
	TargetID    int
	CasterID    int
	Spell       string
	Kind        string
	Detail      string
	RoundsLeft  int
	SaveAbility string
	SaveDC      int
}

// spellEffectLabel is what the table sees on an affected creature.
var spellEffectLabel = map[string]string{
	"bless":           "blessed",
	"bane":            "baned",
	"faerie_fire":     "outlined (Faerie Fire)",
	"slowed":          "slowed",
	"shield_of_faith": "shield of faith",
	"shield_spell":    "Shield (+5 AC)",
	"mage_armor":      "Mage Armor",
	"effect_immune":   "immune (saved against a monster's effect)",
	"commanded":       "commanded",
	"hasted":          "hasted",
	"lethargic":       "lethargic (Haste ended)",
	"heroism":         "heroism",
	"resistance":      "resistance",
	"guidance":        "guidance",
	"large_form":      "Large Form",
	"draconic_flight": "flying (Draconic Flight)",
	"stonecunning":    "tremorsense (Stonecunning)",
	"giant_strength":  "giant strength (potion)",
}

// spellEffectText is what the effect does, for the GM view.
var spellEffectText = map[string]string{
	"bless":           "Adds 1d4 to attack rolls and saving throws",
	"bane":            "Subtracts 1d4 from attack rolls and saving throws",
	"faerie_fire":     "Attack rolls against it have Advantage if the attacker can see it; it can't benefit from the Invisible condition",
	"slowed":          "Speed halved, -2 to AC and Dexterity saves, no Reactions, an action or a Bonus Action (not both), one attack with the Attack action; repeats the Wisdom save at the end of each of its turns",
	"shield_of_faith": "+2 bonus to AC",
	"shield_spell":    "+5 bonus to AC until the start of its next turn (the Shield spell)",
	"mage_armor":      "Base AC 13 + Dexterity modifier while it wears no armor (8 hours; ends if it dons armor)",
	"effect_immune":   "Succeeded on the save against this monster's effect: immune to it for 24 hours (the effect is named)",
	"commanded":       "Follows a one-word command on its next turn",
	"hasted":          "+2 AC, Advantage on Dexterity saves, Speed doubled, one extra action each turn (one attack, Dash, Disengage, Hide or Utilize)",
	"lethargic":       "Incapacitated with a Speed of 0 until the end of its next turn",
	"heroism":         "Immune to Frightened; Temporary Hit Points at the start of each of its turns",
	"resistance":      "Damage of the chosen type is reduced by 1d4, once per turn",
	"guidance":        "+1d4 to ability checks with the chosen skill",
	"large_form":      "Large: Advantage on Strength checks, Speed +10 feet (Goliath)",
	"draconic_flight": "Fly Speed equal to its Speed (Dragonborn)",
	"stonecunning":    "Tremorsense 60 feet while on or touching stone (Dwarf)",

	// potions
	"giant_strength":    "Strength score raised by a Potion of Giant Strength",
	"damage_resistance": "Resistance to a damage type (a potion)",
	"potion":            "A potion's effect (the GM runs it)",
}

// noConcentration are effects that outlast the spell that made them.
var noConcentration = map[string]bool{"mage_armor": true, "shield_spell": true, "defender_ac": true, "metal_shell": true, "ethereal": true, "commanded": true, "lethargic": true, "holy_nimbus": true, "large_form": true, "draconic_flight": true, "stonecunning": true}

var commandText = map[string]string{
	"approach": "moves toward the caster by the shortest and most direct route, ending its turn if it moves within 5 feet of the caster",
	"drop":     "drops whatever it is holding and then ends its turn",
	"flee":     "spends its turn moving away from the caster by the fastest available means",
	"grovel":   "has the Prone condition and then ends its turn",
	"halt":     "doesn't move and takes no action or Bonus Action",
}

// commandTextYou is the same command told to the character who obeys it.
var commandTextYou = map[string]string{
	"approach": "move toward the caster by the shortest and most direct route, ending your turn if you move within 5 feet of the caster",
	"drop":     "drop whatever you are holding and then end your turn",
	"flee":     "spend your turn moving away from the caster by the fastest available means",
	"grovel":   "have the Prone condition and then end your turn",
	"halt":     "don't move and take no action or Bonus Action",
}

func ensureSpellEffectsTable() {
	if db == nil {
		return
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS spell_effects (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER NOT NULL,
		target_id INTEGER NOT NULL,
		caster_id INTEGER NOT NULL,
		spell TEXT NOT NULL,
		kind TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT '',
		rounds_left INTEGER NOT NULL DEFAULT 10,
		save_ability TEXT NOT NULL DEFAULT '',
		save_dc INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS spell_effects_target ON spell_effects (lobby_id, target_id)`)
}

// addSpellEffect puts an effect on a creature. The same spell from the same caster replaces its earlier effect on that creature.
func addSpellEffect(lobbyID, targetID, casterID int, spell, kind, detail string, rounds int, saveAbility string, saveDC int) {
	rounds = extendRounds(casterID, rounds) // Extended Spell (F194)
	if db == nil || lobbyID == 0 {
		return
	}
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND caster_id = $3 AND kind = $4`, lobbyID, targetID, casterID, kind)
	db.Exec(`INSERT INTO spell_effects (lobby_id, target_id, caster_id, spell, kind, detail, rounds_left, save_ability, save_dc)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, lobbyID, targetID, casterID, spell, kind, detail, rounds, saveAbility, saveDC)
}

func clearSpellEffects(lobbyID int) {
	if db != nil {
		// the monsters are gone and turn-bound effects are over; a character's spell keeps its remaining time (6 seconds a round)
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND (target_id < 0 OR caster_id < 0 OR kind IN ('commanded','lethargic'))`, lobbyID)
	}
}

// concentrationHolds: the caster still concentrates on the spell (monsters' spells are kept until the GM or the duration ends them).
func concentrationHolds(casterID int, spell string) bool {
	if casterID <= 0 {
		return true
	}
	var conc string
	db.QueryRow(`SELECT COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, casterID).Scan(&conc)
	return conc != "" && strings.HasPrefix(strings.ToLower(conc), strings.ToLower(spell))
}

// spellEffectsOn lists the effects on a creature that still hold; effects whose caster lost Concentration are removed.
func spellEffectsOn(lobbyID, targetID int) []activeSpellEffect {
	if db == nil || lobbyID == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT id, target_id, caster_id, spell, kind, detail, rounds_left, save_ability, save_dc
		FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 ORDER BY id`, lobbyID, targetID)
	if err != nil {
		return nil
	}
	var all []activeSpellEffect
	for rows.Next() {
		var e activeSpellEffect
		if rows.Scan(&e.ID, &e.TargetID, &e.CasterID, &e.Spell, &e.Kind, &e.Detail, &e.RoundsLeft, &e.SaveAbility, &e.SaveDC) == nil {
			all = append(all, e)
		}
	}
	rows.Close()
	var out []activeSpellEffect
	for _, e := range all {
		if !noConcentration[e.Kind] && !itemEffectSource(e.Spell) && !gmGrantedEffect(e.Spell) && !concentrationHolds(e.CasterID, e.Spell) { // potions and the GM's grants need no Concentration
			db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
			if e.Kind == "hasted" {
				hasteEnds(lobbyID, e.TargetID, 0)
			}
			if n := moreSpellEnds(e.Kind, e.TargetID, e.Detail); n != "" { // a beast form, a size, a flight end with the Concentration (spells_more_2024.go)
				logAction(lobbyID, maxInt(e.TargetID, 0), 0, "spell_effect", n, "")
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

func hasSpellEffect(lobbyID, targetID int, kind string) bool {
	for _, e := range spellEffectsOn(lobbyID, targetID) {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// spellEffectNames are the effect labels everyone can see on a creature.
func spellEffectNames(lobbyID, targetID int) []string {
	var out []string
	for _, e := range spellEffectsOn(lobbyID, targetID) {
		label := spellEffectLabel[e.Kind]
		if (e.Kind == "commanded" || e.Kind == "resistance" || e.Kind == "guidance") && e.Detail != "" {
			label = e.Kind + ": " + e.Detail
		}
		switch e.Kind {
		case "potion":
			label = e.Spell
		case "damage_resistance":
			label = "resistance to " + e.Detail + " damage (" + e.Spell + ")"
		}
		if itemEffectSource(e.Spell) && e.Kind != "potion" && e.Kind != "damage_resistance" {
			label += " (" + e.Spell + ")"
		}
		out = append(out, label)
	}
	return out
}

// spellEffectViews are the effects with their meaning and caster, for the GM.
func spellEffectViews(lobbyID, targetID int) []map[string]interface{} {
	var out []map[string]interface{}
	for _, e := range spellEffectsOn(lobbyID, targetID) {
		text := spellEffectText[e.Kind]
		if e.Kind == "commanded" {
			text = "On its next turn it " + commandText[e.Detail]
		}
		if (e.Kind == "resistance" || e.Kind == "guidance") && e.Detail != "" {
			text += ": " + e.Detail
		}
		v := map[string]interface{}{"effect": spellEffectLabel[e.Kind], "spell": e.Spell, "text": text, "caused_by": e.CasterID}
		if e.SaveAbility != "" {
			v["end_save"] = fmt.Sprintf("%s DC %d", strings.ToUpper(e.SaveAbility), e.SaveDC)
		}
		out = append(out, v)
	}
	return out
}

// spellRollAdjust is the change Bless, Bane and Slow make to a d20 roll. roll is "attack" or "save:<ability>" (str, dex ...).
func spellRollAdjust(lobbyID, id int, roll string) (int, string) {
	adj := 0
	var notes []string
	if strings.HasPrefix(roll, "save:") && deckBoon(id, "euryale") { // the Mysterious Deck's Euryale card (legendary_items_2024.go)
		adj -= 2
		notes = append(notes, "Euryale curse -2")
	}
	for _, e := range spellEffectsOn(lobbyID, id) {
		switch e.Kind {
		case "bless":
			d := game.RollDie(4)
			adj += d
			notes = append(notes, fmt.Sprintf("Bless +%d", d))
		case "bane":
			d := game.RollDie(4)
			adj -= d
			notes = append(notes, fmt.Sprintf("Bane -%d", d))
		case "slowed":
			if roll == "save:dex" {
				adj -= 2
				notes = append(notes, "Slow -2")
			}
		}
	}
	if len(notes) == 0 {
		return 0, ""
	}
	return adj, " [" + strings.Join(notes, ", ") + "]"
}

// spellACAdjust is the change to a creature's Armor Class: Slow -2, Shield of Faith +2.
func spellACAdjust(lobbyID, id int) int {
	adj := 0
	for _, e := range spellEffectsOn(lobbyID, id) {
		switch e.Kind {
		case "slowed":
			adj -= 2
		case "shield_of_faith", "hasted":
			adj += 2
		case "shield_spell": // the Shield spell, until the start of the caster's next turn (reactions_2024.go)
			adj += 5
		case "defender_ac": // Defender's bonus moved to AC, while the weapon is held (legendary_more_2024.go)
			if id > 0 {
				if m, o := equippedWeapons(id); strings.HasPrefix(strings.ToLower(m), "defender") || strings.HasPrefix(strings.ToLower(o), "defender") {
					adj += atoiSafe(e.Detail)
				}
			}
		}
	}
	adj += buffACBonus(lobbyID, id) // Warding Bond +1 (buff_spells_2024.go)
	return adj
}

// outlined: Faerie Fire is on the creature (attack rolls against it have Advantage, and Invisible does not help it).
func outlined(lobbyID, id int) bool { return hasSpellEffect(lobbyID, id, "faerie_fire") }

// withoutInvisible drops the Invisible condition from a creature's conditions while Faerie Fire outlines it.
func withoutInvisible(lobbyID, id int, conds []string) []string {
	if !outlined(lobbyID, id) {
		return conds
	}
	out := conds[:0:0]
	for _, c := range conds {
		if !strings.EqualFold(c, "invisible") {
			out = append(out, c)
		}
	}
	return out
}

// commandBlock says why a commanded creature can't attack this turn ("" when it can).
func commandBlock(lobbyID, id int, name string) string {
	for _, e := range spellEffectsOn(lobbyID, id) {
		if e.Kind == "commanded" && e.RoundsLeft == 0 { // its turn has started
			return fmt.Sprintf("%s is under a Command (%s): this turn it %s. It can't attack.", name, titleCase(e.Detail), commandText[e.Detail])
		}
	}
	return ""
}

// slowedAttackLimit: a slowed creature makes only one attack with the Attack action.
func slowedAttackLimit(lobbyID, id int) bool { return hasSpellEffect(lobbyID, id, "slowed") }

// ---- casting

var commandWordRe = regexp.MustCompile(`\b(approach|drop|flee|grovel|halt)\b`)

func spellExtraTargets(slotLevel, spellLevel, base int) int {
	if slotLevel > spellLevel {
		return base + slotLevel - spellLevel
	}
	return base
}

// hostileSpellTargets are the creatures an enemy-affecting spell is aimed at: monsters first, then characters other than the caster.
func hostileSpellTargets(charID int) []int {
	out := spellMonsterTargets(charID)
	for _, id := range spellCharacterTargets(charID) {
		if id != charID {
			out = append(out, id)
		}
	}
	return out
}

func creatureName(lobbyID, id int) string {
	if id < 0 {
		return monsterName(lobbyID, id)
	}
	var n string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, id).Scan(&n)
	if n == "" {
		return "the creature"
	}
	return n
}

// creatureSave rolls a saving throw for a monster or a character.
func creatureSave(lobbyID, id int, ability string, dc int) (bool, int) {
	if id < 0 {
		return monsterSave(lobbyID, id, ability, dc)
	}
	return characterSave(id, ability, dc)
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// resolveEffectSpell applies Bless, Bane, Faerie Fire, Slow, Shield of Faith and Command. It returns "" for other spells.
func resolveEffectSpell(charID int, spell SRDSpell, spellKey string, slotLevel, saveDC int, description string) string {
	lobby := lobbyOfChar(charID)
	if lobby == 0 {
		return ""
	}
	if t := resolveBuffSpell(charID, lobby, spell, spellKey, slotLevel, saveDC, description); t != "" {
		return t // Aid, Barkskin, Death Ward, Foresight ... (buff_spells_2024.go, F71)
	}
	switch spellKey {
	case "shield":
		return shieldReaction(charID)
	case "mage-armor":
		return mageArmorCast(charID, lobby, spell, description)
	case "false-life":
		return falseLifeCast(charID, slotLevel) // temp_hp_2024.go (F68)
	case "spare-the-dying": // F242: the spell's text only; the creature never became Stable
		ids := spellCharacterTargets(charID)
		if len(ids) == 0 {
			if id := parseTargetFromDescription(description, charID); id > 0 {
				ids = []int{id}
			}
		}
		for _, id := range ids {
			var hp int
			var dead bool
			if db.QueryRow(`SELECT COALESCE(hp,0), COALESCE(is_dead,false) FROM characters WHERE id = $1`, id).Scan(&hp, &dead) == nil && hp <= 0 && !dead {
				db.Exec(`UPDATE characters SET is_stable = true, death_save_successes = 0, death_save_failures = 0 WHERE id = $1`, id)
				return fmt.Sprintf(" Spare the Dying: %s is Stable (no more death saves; 1 Hit Point after 1d4 hours).", creatureName(lobby, id))
			}
		}
		return " Spare the Dying: no creature at 0 Hit Points was named (target_id), so nothing happens."
	case "bless", "shield-of-faith":
		ids := spellCharacterTargets(charID)
		for _, id := range spellMonsterTargets(charID) {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			if named := parseTargetsFromDescription(description, charID); len(named) > 0 {
				ids = named // every character the description names (F229)
			} else {
				ids = []int{charID}
			}
		}
		limit, kind, rounds := spellExtraTargets(slotLevel, 1, 3), "bless", 10
		if spellKey == "shield-of-faith" {
			limit, kind, rounds = 1, "shield_of_faith", 100
		}
		note := ""
		if len(ids) > limit {
			note = fmt.Sprintf(" (only the first %d targets: the spell affects %d at this level)", limit, limit)
			ids = ids[:limit]
		}
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND caster_id = $2 AND kind = $3`, lobby, charID, kind) // a new casting ends the old one (F231)
		var names []string
		for _, id := range ids {
			addSpellEffect(lobby, id, charID, spell.Name, kind, "", rounds, "", 0)
			names = append(names, creatureName(lobby, id))
		}
		if kind == "bless" {
			verb := "add"
			if len(names) == 1 {
				verb = "adds"
			}
			return fmt.Sprintf(" Bless: %s %s 1d4 to attack rolls and saving throws while you keep Concentration (up to 1 minute)%s.", joinNames(names), verb, note)
		}
		return fmt.Sprintf(" Shield of Faith: %s gains a +2 bonus to AC while you keep Concentration (up to 10 minutes).", joinNames(names))

	case "haste", "heroism", "resistance", "guidance":
		ids := spellCharacterTargets(charID)
		if spellKey == "haste" {
			ids = append(ids, spellMonsterTargets(charID)...)
		}
		if len(ids) == 0 {
			if id := parseTargetFromDescription(description, charID); id > 0 {
				ids = []int{id}
			} else {
				ids = []int{charID}
			}
		}
		id := ids[0] // each of these spells affects one creature
		name := creatureName(lobby, id)
		dl := strings.ToLower(description)
		switch spellKey {
		case "haste":
			addSpellEffect(lobby, id, charID, spell.Name, "hasted", "", 10, "", 0)
			return fmt.Sprintf(" Haste: %s gains +2 AC, Advantage on Dexterity saves, double Speed (from its next turn) and one extra action on each of its turns (one attack, Dash, Disengage, Hide or Utilize) while you keep Concentration. When the spell ends it is Incapacitated with a Speed of 0 until the end of its next turn.", name)
		case "heroism":
			var level int
			db.QueryRow(`SELECT level FROM characters WHERE id = $1`, charID).Scan(&level)
			mod := saveDC - 8 - game.ProficiencyBonus(level)
			if mod < 0 {
				mod = 0
			}
			addSpellEffect(lobby, id, charID, spell.Name, "heroism", strconv.Itoa(mod), 10, "", 0)
			freed := ""
			if id > 0 {
				for _, c := range getCharConditions(id) {
					if strings.EqualFold(c, "frightened") {
						removeCondition(id, c)
						freed = " The Frightened condition ends."
					}
				}
				db.Exec(`UPDATE characters SET temp_hp = GREATEST(COALESCE(temp_hp,0), $1) WHERE id = $2`, mod, id)
			}
			db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'frightened'`, lobby, id)
			if id > 0 {
				syncListFromRecords(id)
			}
			return fmt.Sprintf(" Heroism: %s is immune to the Frightened condition and gains %d Temporary Hit Points now and at the start of each of its turns while you keep Concentration.%s", name, mod, freed)
		case "resistance":
			dtype := ""
			for _, t := range []string{"acid", "bludgeoning", "cold", "fire", "lightning", "necrotic", "piercing", "poison", "radiant", "slashing", "thunder"} {
				if strings.Contains(dl, t) {
					dtype = t
					break
				}
			}
			if dtype == "" {
				return " Resistance: name the damage type in the description (Acid, Bludgeoning, Cold, Fire, Lightning, Necrotic, Piercing, Poison, Radiant, Slashing or Thunder); no type was chosen, so the spell has no effect."
			}
			addSpellEffect(lobby, id, charID, spell.Name, "resistance", dtype, 10, "", 0)
			return fmt.Sprintf(" Resistance: when %s takes %s damage, the damage is reduced by 1d4 (once per turn) while you keep Concentration.", name, dtype)
		case "guidance":
			skill := ""
			for k := range skillAbilityMap {
				w := strings.ReplaceAll(strings.ToLower(k), "_", " ")
				if strings.Contains(dl, w) && len(w) > len(skill) {
					skill = w
				}
			}
			if skill == "" {
				return " Guidance: name the skill in the description (for example \"cast guidance for perception\"); no skill was chosen, so the spell has no effect."
			}
			addSpellEffect(lobby, id, charID, spell.Name, "guidance", skill, 10, "", 0)
			return fmt.Sprintf(" Guidance: %s adds 1d4 to ability checks using %s while you keep Concentration.", name, titleCase(skill))
		}

	case "bane", "faerie-fire", "slow", "command":
		ids := hostileSpellTargets(charID)
		if len(ids) == 0 {
			return ""
		}
		var areaNote string
		if spell.AoEShape != "" {
			ids, areaNote = areaFilter(lobby, charID, spell, ids)
		}
		save, kind, limit, rounds, endSave := "cha", "bane", spellExtraTargets(slotLevel, 1, 3), 10, ""
		word := ""
		switch spellKey {
		case "faerie-fire":
			save, kind, limit = "dex", "faerie_fire", len(ids)
		case "slow":
			save, kind, limit, endSave = "wis", "slowed", 6, "wis"
		case "command":
			save, kind, limit, rounds = "wis", "commanded", spellExtraTargets(slotLevel, 1, 1), 1
			word = "halt"
			if m := commandWordRe.FindStringSubmatch(strings.ToLower(description)); m != nil {
				word = m[1]
			}
		}
		note := ""
		if len(ids) > limit {
			note = fmt.Sprintf(" Only the first %d targets are affected at this level.", limit)
			ids = ids[:limit]
		}
		var lines []string
		for _, id := range ids {
			name := creatureName(lobby, id)
			saved, total := creatureSave(lobby, id, save, saveDC)
			if saved {
				lines = append(lines, fmt.Sprintf("%s saves (%d vs DC %d).", name, total, saveDC))
				continue
			}
			addSpellEffect(lobby, id, charID, spell.Name, kind, word, rounds, endSave, saveDC)
			what := map[string]string{
				"bane":        "subtracts 1d4 from its attack rolls and saving throws",
				"faerie_fire": "is outlined in light: attack rolls against it have Advantage, and it can't benefit from the Invisible condition",
				"slowed":      "is slowed (Speed halved, -2 to AC and Dexterity saves, no Reactions, one attack, an action or a Bonus Action)",
			}[kind]
			if kind == "commanded" {
				what = "must obey \"" + titleCase(word) + "\": on its next turn it " + commandText[word]
			}
			lines = append(lines, fmt.Sprintf("%s fails its %s save (%d vs DC %d) and %s.", name, strings.ToUpper(save), total, saveDC, what))
		}
		return " " + strings.Join(lines, " ") + note + areaNote
	}
	return ""
}

// ---- turns

// spellEffectsTurnStart runs when a creature's turn starts: a commanded creature obeys (Grovel knocks it Prone).
func spellEffectsTurnStart(lobbyID, id int, name string) []string {
	var log []string
	if id > 0 { // the Shield spell lasts until the start of the caster's next turn
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind IN ('shield_spell','defender_ac')`, lobbyID, id) // Shield; Defender's AC
	}
	if id < 0 {
		if s := monsterStandsUp(lobbyID, id, name); s != "" {
			log = append(log, s)
		}
		log = append(log, holyNimbusDamage(lobbyID, id)...) // Oath of Devotion 20
	}
	for _, e := range spellEffectsOn(lobbyID, id) {
		if e.Kind == "lethargic" && e.RoundsLeft == 1 {
			db.Exec(`UPDATE spell_effects SET rounds_left = 0 WHERE id = $1`, e.ID)
			log = append(log, name+" is lethargic after Haste: Incapacitated with a Speed of 0 this turn.")
			continue
		}
		if e.Kind != "commanded" || e.RoundsLeft != 1 {
			continue
		}
		db.Exec(`UPDATE spell_effects SET rounds_left = 0 WHERE id = $1`, e.ID)
		if e.Detail == "grovel" {
			if id < 0 {
				addMonsterCondition(lobbyID, id, "prone", "Command", 0, "", 0)
			} else {
				addCharacterCondition(id, "prone")
			}
		}
		log = append(log, fmt.Sprintf("%s obeys the Command \"%s\": it %s.", name, titleCase(e.Detail), commandText[e.Detail]))
	}
	if hasSpellEffect(lobbyID, id, "slowed") {
		log = append(log, name+" is slowed: Speed halved, no Reactions, an action or a Bonus Action this turn (not both), and only one attack.")
	}
	return log
}

// spellEffectsTurnEnd runs when a creature's turn ends: its Command is over, a slowed creature repeats its save, and the spells it
// concentrates on count down.
func spellEffectsTurnEnd(lobbyID, id int, name string) []string {
	var log []string
	if id > 0 {
		if s := wildShapeTick(id, 1); s != "" { // Wild Shape lasts half the Druid level in hours
			log = append(log, s)
		}
	}
	for _, e := range spellEffectsOn(lobbyID, id) {
		switch {
		case (e.Kind == "commanded" || e.Kind == "lethargic") && e.RoundsLeft == 0:
			db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
		case e.SaveAbility != "":
			saved, total := creatureSave(lobbyID, id, e.SaveAbility, e.SaveDC)
			if saved {
				db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
				log = append(log, fmt.Sprintf("%s repeats the %s save against %s and succeeds (%d): the spell ends on it.", name, strings.ToUpper(e.SaveAbility), e.Spell, total))
			} else {
				log = append(log, fmt.Sprintf("%s repeats the %s save against %s and fails (%d).", name, strings.ToUpper(e.SaveAbility), e.Spell, total))
			}
		}
	}
	// the caster's spells: one round of their duration has passed
	db.Exec(`UPDATE spell_effects SET rounds_left = rounds_left - 1 WHERE lobby_id = $1 AND caster_id = $2 AND kind NOT IN ('commanded','lethargic')`, lobbyID, id)
	var ended []string
	var hastedIDs []int
	rows, err := db.Query(`DELETE FROM spell_effects WHERE lobby_id = $1 AND caster_id = $2 AND kind NOT IN ('commanded','lethargic') AND rounds_left <= 0 RETURNING spell, kind, target_id, detail`, lobbyID, id)
	if err == nil {
		seen := map[string]bool{}
		for rows.Next() {
			var s, k, d string
			var t int
			if rows.Scan(&s, &k, &t, &d) == nil {
				// Giant Strength gives the score back
				endItemEffect(k, t, d)
				if k == "hasted" && s != "Potion of Speed" { // the potion's Haste ends without the wave of lethargy
					hastedIDs = append(hastedIDs, t)
				}
				if !seen[s] {
					seen[s] = true
					ended = append(ended, s)
				}
			}
		}
		rows.Close()
	}
	for _, t := range hastedIDs {
		hasteEnds(lobbyID, t, id)
	}
	for _, s := range ended {
		log = append(log, fmt.Sprintf("%s's %s reaches the end of its duration.", name, s))
		if id > 0 {
			db.Exec(`UPDATE characters SET concentrating_on = NULL WHERE id = $1 AND lower(COALESCE(concentrating_on,'')) LIKE $2`, id, strings.ToLower(s)+"%")
		}
	}
	return log
}

// ---- Haste, Heroism, Resistance, Guidance, and what a creature may do on its turn

// spellSaveAdvantage: Haste gives Advantage on Dexterity saving throws.
func spellSaveAdvantage(lobbyID, id int, ability string) bool {
	return (strings.HasPrefix(strings.ToLower(ability), "dex") && hasSpellEffect(lobbyID, id, "hasted")) ||
		(strings.HasPrefix(strings.ToLower(ability), "dex") && dodgeActive(id)) || // Dodge: Dexterity saving throws with Advantage
		spellSaveItemAdvantage(lobbyID, id) || // a save against a spell with a Ring of Spell Turning, Scarab, Staff of the Magi, a Holy Avenger's aura
		deckAdvantage(id) || // the Jester card
		buffSaveAdv(lobbyID, id, ability) || // Foresight, Holy Aura, Beacon of Hope, Protection from Poison (buff_spells_2024.go)
		fightingAdvantageFor(lobbyID, id) // Charm and Dominate spells against a creature being fought (monster_reactions_2024.go)
}

// hasteEnds: "When the spell ends, the target is Incapacitated and has a Speed of 0 until the end of its next turn." endingNow is the id of
// the creature whose turn is ending right now (its condition then lasts through its next turn).
func hasteEnds(lobbyID, id, endingNow int) {
	rounds := 1
	if id == endingNow {
		rounds = 2
	}
	pending := 1 // waits for the start of its next turn
	if id != endingNow && currentTurnID(lobbyID) == id {
		pending = 0 // this turn is its next turn
	}
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'lethargic'`, lobbyID, id)
	db.Exec(`INSERT INTO spell_effects (lobby_id, target_id, caster_id, spell, kind, rounds_left) VALUES ($1,$2,$2,'Haste','lethargic',$3)`, lobbyID, id, pending)
	addMonsterCondition(lobbyID, id, "incapacitated", "Haste ends", rounds, "", 0)
	if id > 0 {
		addCharacterCondition(id, "incapacitated")
		db.Exec(`UPDATE characters SET movement_remaining = 0 WHERE id = $1`, id)
	}
	line := creatureName(lobbyID, id) + ": Haste ends; a wave of lethargy leaves it Incapacitated with a Speed of 0 until the end of its next turn."
	recordGMAction(db, lobbyID, "condition", line, line)
}

var hasteUsed sync.Map // character id -> true when the Haste action was taken this turn

// currentTurnID is the id of the creature whose turn it is (0 outside combat).
func currentTurnID(lobbyID int) int {
	var raw []byte
	var idx int
	if db.QueryRow(`SELECT turn_order, current_turn_index FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw, &idx) != nil {
		return 0
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	if idx < 0 || idx >= len(entries) {
		return 0
	}
	return asInt(entries[idx]["id"])
}

// spellTurnStart is called by resetTurnFlags: Slow halves movement and takes the Reaction, Haste doubles movement, Haste's lethargy
// sets it to 0, Heroism gives Temporary Hit Points.
func spellTurnStart(charID int) {
	lobby := lobbyOfChar(charID)
	hasteUsed.Delete(charID)
	disengagedThisTurn.Delete(charID)
	for _, e := range spellEffectsOn(lobby, charID) {
		switch e.Kind {
		case "slowed":
			if buffIgnoresDifficult(charID) { // Freedom of Movement: magic can't reduce its Speed
				db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, charID)
			} else {
				db.Exec(`UPDATE characters SET movement_remaining = movement_remaining / 2, reaction_used = true WHERE id = $1`, charID)
			}
		case "hasted":
			db.Exec(`UPDATE characters SET movement_remaining = movement_remaining * 2 WHERE id = $1`, charID)
		case "lethargic":
			db.Exec(`UPDATE characters SET movement_remaining = 0 WHERE id = $1`, charID)
		case "heroism":
			if n, _ := strconv.Atoi(e.Detail); n > 0 {
				db.Exec(`UPDATE characters SET temp_hp = GREATEST(COALESCE(temp_hp,0), $1) WHERE id = $2`, n, charID)
			}
		}
	}
}

// heroismImmune: Heroism makes the creature immune to the Frightened condition.
func heroismImmune(lobbyID, id int, cond string) bool {
	if strings.EqualFold(cond, "frightened") && hasSpellEffect(lobbyID, id, "heroism") {
		return true
	}
	immune, _ := auraImmune(lobbyID, id, cond) // Aura of Courage, Aura of Devotion
	return immune
}

var resistanceUsed sync.Map // effect id -> the turn it was used ("round:index")

// resistanceSpellReduction: Resistance reduces damage of the chosen type by 1d4, once per turn.
func resistanceSpellReduction(charID int, dtype string) (int, string) {
	if dtype == "" {
		return 0, ""
	}
	lobby := lobbyOfChar(charID)
	for _, e := range spellEffectsOn(lobby, charID) {
		if e.Kind != "resistance" || !strings.EqualFold(e.Detail, dtype) {
			continue
		}
		turn := combatTurnMarker(lobby) // F190: unique per turn; "round:index" repeated in the next combat
		if prev, ok := resistanceUsed.Load(e.ID); ok && prev.(string) == turn && turn != "" {
			return 0, ""
		}
		resistanceUsed.Store(e.ID, turn)
		d := game.RollDie(4)
		return d, fmt.Sprintf("Resistance reduces the %s damage by %d", strings.ToLower(dtype), d)
	}
	return 0, ""
}

// guidanceBonus: Guidance adds 1d4 to an ability check with the chosen skill.
func guidanceBonus(lobbyID, charID int, skill string) (int, string) {
	if skill == "" {
		return 0, ""
	}
	norm := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), "_", ""))
	}
	for _, e := range spellEffectsOn(lobbyID, charID) {
		if e.Kind == "guidance" && norm(e.Detail) == norm(skill) {
			d := game.RollDie(4)
			return d, fmt.Sprintf(" [Guidance +%d]", d)
		}
	}
	return 0, ""
}

var hasteActions = map[string]bool{"attack": true, "dash": true, "disengage": true, "hide": true, "utilize": true, "use_item": true}

// hasteExtraAction: after its action is spent, a hasted character may take one more action on its turn: an Attack (one attack only),
// Dash, Disengage, Hide or Utilize. It returns true and marks it used when the action qualifies.
func hasteExtraAction(charID int, action string) bool {
	if !hasteActions[strings.ToLower(action)] || !hasSpellEffect(lobbyOfChar(charID), charID, "hasted") {
		return false
	}
	if _, used := hasteUsed.LoadOrStore(charID, true); used {
		return false
	}
	return true
}

// turnLimitProblem says why a spell effect stops this action on the character's turn ("" when nothing does): a Command being obeyed,
// Haste's lethargy, and Slow (an action or a Bonus Action, not both; one attack).
func turnLimitProblem(charID int, action string) string {
	if action == "death_save" {
		return "" // a Death Saving Throw isn't an action: Command, Haste's lethargy and Slow don't stop it (F92)
	}
	lobby := lobbyOfChar(charID)
	rt := getActionResourceType(action)
	acting := rt == "action" || rt == "bonus_action" || rt == "reaction"
	for _, e := range spellEffectsOn(lobby, charID) {
		switch e.Kind {
		case "commanded":
			if e.RoundsLeft != 0 {
				continue
			}
			word := e.Detail
			if acting {
				return fmt.Sprintf("You are obeying a Command (%s): this turn you %s. You can't take actions, Bonus Actions or Reactions.", titleCase(word), commandTextYou[word])
			}
			if rt == "movement" && (word == "halt" || word == "grovel" || word == "drop") {
				return fmt.Sprintf("You are obeying a Command (%s): you can't move this turn.", titleCase(word))
			}
		case "lethargic":
			if acting {
				return "Haste has ended: you are Incapacitated until the end of your next turn."
			}
			if rt == "movement" {
				return "Haste has ended: your Speed is 0 until the end of your next turn."
			}
		case "slowed":
			var actionUsed, bonusUsed bool
			var attacks sql.NullInt32
			db.QueryRow(`SELECT COALESCE(action_used,false), COALESCE(bonus_action_used,false), attacks_remaining FROM characters WHERE id = $1`, charID).Scan(&actionUsed, &bonusUsed, &attacks)
			tookAction := actionUsed || attacks.Valid
			if strings.EqualFold(action, "attack") && tookAction {
				return "You are slowed: you make only one attack with the Attack action."
			}
			if rt == "action" && bonusUsed {
				return "You are slowed: you already took a Bonus Action this turn, and you can take an action or a Bonus Action, not both."
			}
			if rt == "bonus_action" && tookAction {
				return "You are slowed: you already took an action this turn, and you can take an action or a Bonus Action, not both."
			}
		}
	}
	return ""
}

// monsterStandsUp: at the start of its turn a Prone monster stands up (it spends half its movement), unless something stops it from moving.
func monsterStandsUp(lobbyID, id int, name string) string {
	prone := false
	for _, c := range conditionNames(lobbyID, id) {
		switch c {
		case "prone":
			prone = true
		case "grappled", "restrained", "incapacitated", "paralyzed", "stunned", "unconscious", "petrified":
			return ""
		}
	}
	if !prone || hasSpellEffect(lobbyID, id, "lethargic") {
		return ""
	}
	db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'prone'`, lobbyID, id)
	return name + " stands up from Prone (half its movement)."
}

// ---- a hasted monster's extra action, and time passing outside combat

var monsterHasteUsed sync.Map // "lobby:id" -> the turn ("round:index") its Haste action was used

// monsterHasteAttack: a hasted monster gets one extra attack on its turn (Haste's action, Attack with one attack only). It is
// spent the first time the monster attacks on its turn.
func monsterHasteAttack(lobbyID, id int) bool {
	if !hasSpellEffect(lobbyID, id, "hasted") {
		return false
	}
	turn := combatTurnMarker(lobbyID)
	if turn == "" {
		return false
	}
	key := fmt.Sprintf("%d:%d", lobbyID, id)
	if prev, ok := monsterHasteUsed.Load(key); ok && prev.(string) == turn {
		return false
	}
	monsterHasteUsed.Store(key, turn)
	return true
}

// passSpellTime moves the spell clock forward by a number of rounds (10 rounds = 1 minute) outside the turn order: a rest, or the GM
// saying that time passes. Effects that run out end (with their caster's Concentration), and turn-bound effects (Command, Haste's
// lethargy) are over. It returns what ended.
func passSpellTime(lobbyID, rounds int) []string { return passSpellTimeOn(lobbyID, rounds, 0) }

// passSpellTimeOn: with onlyTarget > 0 (that character's rest) only the effects and conditions on that character move forward.
// Each character's rest used to move the whole campaign's clock, so a party of four resting one after another passed 32 hours for
// every effect, not 8 (F72).
func passSpellTimeOn(lobbyID, rounds, onlyTarget int) []string {
	if db == nil || lobbyID == 0 || rounds <= 0 {
		return nil
	}
	recovered := stableRecovery(lobbyID, rounds)
	recovered = append(recovered, repeatSavesOverTime(lobbyID, rounds, onlyTarget)...) // repeated saves roll each round (F227)
	wildShapeTickLobby(lobbyID, rounds)                                                // a form that runs out ends (posted to the feed)
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND kind IN ('commanded','lethargic')`, lobbyID)
	db.Exec(`UPDATE spell_effects SET rounds_left = rounds_left - $2 WHERE lobby_id = $1 AND ($3 = 0 OR target_id = $3)`, lobbyID, rounds, onlyTarget)
	// conditions with a duration run out too
	if crow, err := db.Query(`DELETE FROM condition_records WHERE lobby_id = $1 AND duration_rounds > 0 AND duration_rounds <= $2 AND ($3 = 0 OR target_id = $3) RETURNING target_id`, lobbyID, rounds, onlyTarget); err == nil {
		var touched []int
		for crow.Next() {
			var t int
			if crow.Scan(&t) == nil && t > 0 {
				touched = append(touched, t)
			}
		}
		crow.Close()
		for _, t := range touched {
			syncListFromRecords(t)
		}
	}
	db.Exec(`UPDATE condition_records SET duration_rounds = duration_rounds - $2 WHERE lobby_id = $1 AND duration_rounds > 0 AND ($3 = 0 OR target_id = $3)`, lobbyID, rounds, onlyTarget)
	rows, err := db.Query(`DELETE FROM spell_effects WHERE lobby_id = $1 AND rounds_left <= 0 RETURNING spell, caster_id, kind, target_id, detail`, lobbyID)
	if err != nil {
		return nil
	}
	type ended struct {
		spell  string
		caster int
	}
	var list []ended
	seen := map[string]bool{}
	for rows.Next() {
		var e ended
		var kind, detail string
		var target int
		if rows.Scan(&e.spell, &e.caster, &kind, &target, &detail) == nil {
			endItemEffect(kind, target, detail) // Giant Strength gives the score back
			k := fmt.Sprintf("%s#%d", e.spell, e.caster)
			if !seen[k] {
				seen[k] = true
				list = append(list, e)
			}
		}
	}
	rows.Close()
	var out []string
	for _, e := range list {
		if e.caster > 0 {
			db.Exec(`UPDATE characters SET concentrating_on = NULL WHERE id = $1 AND lower(COALESCE(concentrating_on,'')) LIKE $2`, e.caster, strings.ToLower(e.spell)+"%")
		}
		if isSpeciesTraitName(e.spell) || itemEffectSource(e.spell) {
			out = append(out, fmt.Sprintf("%s's %s has ended.", creatureName(lobbyID, e.caster), e.spell))
			continue
		}
		out = append(out, fmt.Sprintf("%s (cast by %s) has ended.", e.spell, creatureName(lobbyID, e.caster)))
	}
	return append(recovered, out...)
}

// isSpeciesTraitName: the effect comes from a species trait (Large Form, Draconic Flight, Stonecunning), not a spell.
func isSpeciesTraitName(name string) bool {
	for _, n := range speciesTraitEffects {
		if n == name {
			return true
		}
	}
	return false
}

// stableRounds is 1d4 hours in rounds: how long a Stable creature that isn't healed stays at 0 Hit Points.
func stableRounds() string {
	return fmt.Sprint(game.RollDie(4) * 600)
}

// stableRecovery: a Stable creature that isn't healed regains 1 Hit Point after 1d4 hours (SRD 5.2.1, Damage and Healing).
func stableRecovery(lobbyID, rounds int) []string {
	rows, err := db.Query(`UPDATE characters SET stable_rounds_left = stable_rounds_left - $2
		WHERE lobby_id = $1 AND hp = 0 AND COALESCE(is_stable,false) AND NOT COALESCE(is_dead,false) AND COALESCE(stable_rounds_left,0) > 0
		RETURNING id, stable_rounds_left`, lobbyID, rounds)
	if err != nil {
		return nil
	}
	var ready []int
	for rows.Next() {
		var id, left int
		if rows.Scan(&id, &left) == nil && left <= 0 {
			ready = append(ready, id)
		}
	}
	rows.Close()
	var out []string
	for _, id := range ready {
		db.Exec(`UPDATE characters SET stable_rounds_left = 0 WHERE id = $1`, id)
		if name, _, after := healCharacter(id, 1); after > 0 {
			out = append(out, fmt.Sprintf("%s has been Stable long enough to regain 1 Hit Point and wakes up.", name))
		}
	}
	return out
}

// handleGMPassTime: POST /api/gm/pass-time {"minutes": 10} (or "rounds") tells the server that game time passes outside combat, so
// spells such as Bless (1 minute) or Shield of Faith (10 minutes) end when their time is up. Rests do this by themselves.
// @Summary Let game time pass outside combat
// @Description Out of combat the GM decides how much time passes. Send minutes (or rounds; a round is 6 seconds, 10 rounds = 1 minute). Spell effects whose duration has run out end, with their caster's Concentration. Refused during combat (the turn order moves the clock). Rests pass time by themselves (Short Rest 1 hour, Long Rest 8 hours).
// @Tags GM
// @Param request body object{minutes=int,rounds=int} true "How much time passes"
// @Router /gm/pass-time [post]
func handleGMPassTime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, campaignID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Minutes int `json:"minutes"`
		Rounds  int `json:"rounds"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	rounds := req.Rounds + req.Minutes*10
	if rounds <= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "minutes_required", "message": "Send minutes (or rounds; 10 rounds = 1 minute)."})
		return
	}
	var active bool
	db.QueryRow(`SELECT COALESCE(active,false) FROM combat_state WHERE lobby_id = $1`, campaignID).Scan(&active)
	if active {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "combat_active", "message": "During combat time passes with the turn order (POST /api/campaigns/{id}/combat/next)."})
		return
	}
	ended := passSpellTime(campaignID, rounds)
	for _, line := range ended {
		recordGMAction(db, campaignID, "condition", line, line)
	}
	resp := map[string]interface{}{"success": true, "rounds_passed": rounds, "ended": ended}
	if days := rounds / 14400; days > 0 { // 24 hours: a dawn has passed, magic items regain charges (once per day, up to a week)
		var recharged []string
		for i := 0; i < days && i < 7; i++ {
			recharged = append(recharged, rechargeCampaignAtDawn(campaignID)...)
		}
		if len(recharged) > 0 {
			resp["magic_items_recharged"] = recharged
		}
	}
	writeJSONStatus(w, 200, resp)
}
