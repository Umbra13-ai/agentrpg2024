package main

// Spells that do more than one thing to one monster (SRD 5.2.1):
//	several targets     "target_ids": [...] on a cast: monsters (negative ids) each make their own saving throw against an area or
//	                    multi-target spell; characters (positive ids) are healed by a healing spell. The damage or healing is rolled once.
//	healing             a healing spell restores Hit Points to the named character, the target_ids, or the caster; a creature at 0 Hit Points
//	                    wakes up (death saves reset).
//	condition spells    Hold Person, Sleep, Entangle, Web, Fear and others: a failed saving throw puts the condition on the monster
//	                    (condition_records, with the end-of-turn saving throw where the spell allows one).

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// castTargets are the extra targets of a cast (character id -> ids).
var castTargets sync.Map

// spellTargetIDs lists the monsters (negative ids) a cast is aimed at: target_id plus target_ids.
func spellMonsterTargets(charID int) []int {
	var out []int
	seen := map[int]bool{}
	if _, id, ok := attackedMonster(charID); ok {
		out = append(out, id)
		seen[id] = true
	}
	if v, ok := castTargets.Load(charID); ok {
		for _, id := range v.([]int) {
			if id < 0 && !seen[id] {
				out = append(out, id)
				seen[id] = true
			}
		}
	}
	return out
}

// spellCharacterTargets lists the characters (positive ids) a cast is aimed at.
func spellCharacterTargets(charID int) []int {
	var out []int
	if v, ok := castTargets.Load(charID); ok {
		for _, id := range v.([]int) {
			if id > 0 {
				out = append(out, id)
			}
		}
	}
	return out
}

// conditionSpell is what a failed saving throw does.
type conditionSpell struct {
	Save       string   // the ability of the monster's saving throw
	Conditions []string // conditions applied on a failure
	Rounds     int      // duration in rounds (10 = 1 minute)
	EndSave    bool     // the creature repeats the save at the end of its turns
}

var conditionSpells = map[string]conditionSpell{
	"hold-person":             {"wis", []string{"paralyzed"}, 10, true},
	"hold-monster":            {"wis", []string{"paralyzed"}, 10, true},
	"sleep":                   {"wis", []string{"incapacitated"}, 10, true},
	"blindness-deafness":      {"con", []string{"blinded"}, 10, true},
	"fear":                    {"wis", []string{"frightened"}, 10, true},
	"charm-person":            {"wis", []string{"charmed"}, 600, false},
	"charm-monster":           {"wis", []string{"charmed"}, 600, false},
	"hypnotic-pattern":        {"wis", []string{"charmed", "incapacitated"}, 10, false},
	"entangle":                {"str", []string{"restrained"}, 10, true},
	"web":                     {"dex", []string{"restrained"}, 10, true},
	"tashas-hideous-laughter": {"wis", []string{"prone", "incapacitated"}, 10, true},
	"hideous-laughter":        {"wis", []string{"prone", "incapacitated"}, 10, true}, // the 2024 name (F203)
	"dominate-person":         {"wis", []string{"charmed"}, 10, false},               // F203: Charmed (a repeat save when it takes damage)
	"dominate-beast":          {"wis", []string{"charmed"}, 10, false},
	"dominate-monster":        {"wis", []string{"charmed"}, 600, false},
	"compulsion":              {"wis", []string{"charmed"}, 10, false},
	"suggestion":              {"wis", []string{"charmed"}, 4800, false},
	"mass-suggestion":         {"wis", []string{"charmed"}, 14400, false},
	"modify-memory":           {"wis", []string{"charmed", "incapacitated"}, 10, false},
	"grease":                  {"dex", []string{"prone"}, 0, false},      // the creatures in the square when the grease appears
	"weird":                   {"wis", []string{"frightened"}, 10, true}, // after its damage
	"color-spray":             {"con", []string{"blinded"}, 1, false},
	"ray-of-sickness":         {"con", []string{"poisoned"}, 2, false}, // after its damage
	"stinking-cloud":          {"con", []string{"poisoned"}, 2, false}, // after its damage
}

// damageSpellConditions are the spells that deal damage and also apply a condition on a failed save.
func isDamageConditionSpell(key string) bool {
	return key == "ray-of-sickness" || key == "stinking-cloud" || key == "weird"
}

// resolveConditionSpell handles a cast with no damage dice that puts a condition on monsters. It returns "" when the spell is not one.
func resolveConditionSpell(charID int, spellKey string, saveDC int, spellName string) string {
	cs, ok := conditionSpells[spellKey]
	if !ok || isDamageConditionSpell(spellKey) {
		return ""
	}
	targets := spellMonsterTargets(charID)
	if len(targets) == 0 {
		return ""
	}
	lobby, _, _ := attackedMonster(charID)
	if lobby == 0 {
		lobby = lobbyOfChar(charID)
	}
	var lines []string
	for _, id := range targets {
		name := monsterName(lobby, id)
		if need := spellCreatureType[spellKey]; need != "" && !strings.EqualFold(monsterTypeOf(lobby, id), need) {
			lines = append(lines, fmt.Sprintf("%s isn't a %s, so %s has no effect on it.", name, titleCase(need), spellName))
			continue
		}
		immuneAll := true
		for _, c := range cs.Conditions {
			if immuneToCondition(lobby, id, condAdd{Condition: c, SourceID: charID, Magical: true}) == "" { // the condition owner's Immunity
				immuneAll = false
			}
		}
		if immuneAll {
			lines = append(lines, fmt.Sprintf("%s is immune to the %s condition.", name, strings.Join(cs.Conditions, " and ")))
			continue
		}
		// the one saving throw function (save_2024.go): Magic Resistance against a spell, Legendary Resistance against a condition
		sc := saveCtx{lobby: lobby, saver: id, ability: cs.Save, dc: saveDC, spell: true, source: charID, against: cs.Conditions}
		if heightenedDisadvantage(charID, id) { // Heightened Spell (F194)
			sc.extraDis = []string{"Heightened Spell"}
		}
		if fightingItAdvantage(spellKey) && currentTurnID(lobby) != 0 { // "Advantage on the save if you or your allies are fighting it" (F225)
			sc.extraAdv = []string{"the caster's side is fighting it"}
		}
		sv := sc.resist(saveStakes{Condition: true})
		if sv.Success {
			lines = append(lines, sv.Line(name)+".")
			continue
		}
		endSave := ""
		if cs.EndSave {
			endSave = cs.Save
		}
		for _, c := range cs.Conditions {
			a := condAdd{Condition: c, Source: spellName, Rounds: extendRounds(charID, cs.Rounds), SourceID: charID, Magical: true} // Extended Spell doubles it (F194)
			if endSave != "" {
				a.EndSave = &conditionSave{Ability: endSave, DC: saveDC}
			}
			if sp, ok := srdSpellsMemory[spellKey]; ok && strings.Contains(strings.ToLower(sp.Duration), "concentration") {
				a.Notes, a.CreatedBy = "concentration", charID // F56: it lasts while the caster keeps Concentration (loadConditionRecords drops it when that ends)
			}
			addCondition(lobby, id, a)
		}
		lines = append(lines, fmt.Sprintf("%s and is %s.", sv.Line(name), strings.Join(cs.Conditions, " and ")))
	}
	return " " + strings.Join(lines, " ") + takeAreaNote(charID)
}

func lobbyOfChar(charID int) int {
	var l int
	db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&l)
	return l
}

func monsterName(lobbyID, id int) string {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			return fmt.Sprint(e["name"])
		}
	}
	return "the creature"
}

// applySpellHealing heals the characters a healing spell is aimed at and says who got what.
func applySpellHealing(charID, heal, healMax int, description string) string {
	targets := spellAllyTargets(charID, description) // living_target_2024.go
	var parts []string
	for _, id := range targets {
		amount, beacon := heal, ""
		if healMax > heal && beaconMaxHealing(id) {
			amount, beacon = healMax, " (Beacon of Hope: the maximum)" // buff_spells_2024.go
		}
		if isCompanionCombatID(id) { // a companion's Hit Points have their own owner (healCompanion); the route let it through only for a healing spell
			parts = append(parts, healCompanionByCast(charID, id, amount))
			continue
		}
		name, before, after := healCharacter(id, amount)
		if name == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s regains %d Hit Points (%d → %d)%s", name, after-before, before, after, beacon)+strings.TrimSuffix(lifeBond(id, after-before), ".")) // Life Bond: a Paladin's steed within 5 feet
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, "; ") + "."
}

// ---- damaging spells aimed at characters (friendly fire, or a character the GM controls as an enemy)

func abilityScoreOf(charID int, ability string) int {
	col := map[string]string{"str": "str", "dex": "dex", "con": "con", "int": "intl", "wis": "wis", "cha": "cha"}[ability]
	if col == "" {
		return 10
	}
	var v int
	db.QueryRow(`SELECT `+col+` FROM characters WHERE id = $1`, charID).Scan(&v)
	return v
}

// damageCharacter deals damage of one type to a character (the Hit Point owner, hit_points_2024.go) and returns the clause a reply
// shows: "takes 7 fire damage (now 5 HP) [Concentration: ...]".
func damageCharacter(targetID, dmg int, dtype string, critical ...bool) string {
	hit := oneHit(dmg, dtype)
	hit.Critical = len(critical) > 0 && critical[0] // at 0 Hit Points a Critical Hit is two death save failures
	d, err := damageHP(targetID, hit)
	if dbFailed(err) {
		return fmt.Sprintf("takes no damage (%s)", notFoundOrDBError(err, ""))
	}
	if err != nil {
		return fmt.Sprintf("takes 0 %s damage", dtype)
	}
	return d.Sentence(dtype)
}

// resolveSpellOnCharacters applies a damaging spell to the characters in target_ids (or a character target_id): a spell attack against
// their Armor Class, or their saving throw against the spell save DC.
func resolveSpellOnCharacters(charID int, spell SRDSpell, damageDice string, rolled, charLevel, spellMod, saveDC int) string {
	ids := spellCharacterTargets(charID)
	if len(ids) == 0 {
		return ""
	}
	dtype := strings.ToLower(spell.DamageType)
	var lines []string
	for _, id := range ids {
		if magicMissileImmune(id, spell.Name) { // Brooch of Shielding (F141)
			lines = append(lines, fmt.Sprintf(" %s is immune to Magic Missile (Brooch of Shielding).", characterNameOf(id)))
			continue
		}
		var name string
		if db.QueryRow(`SELECT name FROM characters WHERE id = $1`, id).Scan(&name) != nil {
			continue
		}
		if isSpellAttack(spell) {
			// the one attack function (attack_2024.go). Nov 14: this path ignored the target's Dodge, Blur and cover, the
			// caster's conditions and magic item bonus, and Sanctuary.
			sa := attackCtx{lobby: lobbyOfChar(charID), attacker: charID, target: id, name: "Spell attack against " + name,
				bonus: characterSpellAttackBonus(charID, charLevel, spellMod), spell: true, melee: spellAttackIsMelee(spell.Range)}
			if lost, note := sa.blocked(); lost {
				lines = append(lines, " "+note)
				continue
			}
			ar, out, rollNote := sa.roll()
			if !out.Hit {
				lines = append(lines, " "+ar.LineForAttacker(sa.name, out)+rollNote+".")
				continue
			}
			d := rolled
			if out.Critical {
				d += game.RollDamage(damageDice, false)
			}
			lines = append(lines, fmt.Sprintf(" %s%s, %s.", ar.LineForAttacker(sa.name, out), rollNote,
				damageCharacterWithRiders(id, d, dtype, out.Critical, hitRiders(charID, id, out.Critical)))) // Hex, Hunter's Mark (hit_riders_2024.go)
			continue
		}
		ability := saveAbilityKey(spell.SavingThrow)
		onSave := "half"
		if len(spell.DamageParts) > 0 {
			if spell.DamageParts[0].Save != "" {
				ability = saveAbilityKey(spell.DamageParts[0].Save)
			}
			if spell.DamageParts[0].OnSave == "none" {
				onSave = "none"
			}
		}
		if ability == "" {
			continue
		}
		sc := saveCtx{lobby: lobbyOfChar(charID), saver: id, ability: ability, dc: saveDC, spell: true, source: charID}
		switch {
		case sculpted(charID): // F119: Sculpt Spells
			sc.autoSucceed = "Sculpt Spells"
		case careful(charID): // F194: Careful Spell
			sc.autoSucceed = "Careful Spell"
		}
		if sc.autoSucceed != "" && onSave == "half" {
			onSave = "none" // and no damage
		}
		r := sc.roll()
		saved := r.Success
		d := rolled
		how := r.Clause()
		if saved {
			if onSave == "half" {
				d /= 2
			} else {
				d = 0
			}
		}
		if d == 0 {
			lines = append(lines, fmt.Sprintf(" %s %s and takes no damage.", name, how))
		} else {
			lines = append(lines, fmt.Sprintf(" %s %s and %s.", name, how, damageCharacter(id, d, dtype)))
		}
	}
	return strings.Join(lines, "")
}

// wakeOnDamage: Hypnotic Pattern and Sleep end for a creature that takes damage. It returns a note ("" when nothing ended).
func wakeOnDamage(lobbyID, id int, name string) string {
	if db == nil {
		return ""
	}
	if note := charmEndsOnDamage(lobbyID, id, name); note != "" {
		return note + wakeOnDamageSleep(lobbyID, id, name)
	}
	return wakeOnDamageSleep(lobbyID, id, name)
}

// charmEndsOnDamage (F221, SRD 5.2.1): Suggestion and Mass Suggestion end for a target that the caster or its allies damage; a target
// of Dominate Person, Beast or Monster repeats the Wisdom save each time it takes damage, ending the spell on itself on a success. The
// damage here comes from the party's attacks, spells and effects (monsters rarely damage each other).
func charmEndsOnDamage(lobbyID, id int, name string) string {
	out := ""
	if len(removeConditionsWhere(lobbyID, id, func(r conditionRecord) bool { return r.Source == "Suggestion" || r.Source == "Mass Suggestion" })) > 0 {
		out += fmt.Sprintf(" The damage ends the Suggestion on %s.", name)
	}
	if id >= 0 {
		return out
	}
	type dom struct {
		spell  string
		caster int
	}
	var list []dom
	seen := map[dom]bool{}
	for _, r := range loadConditionRecords(lobbyID, id) {
		if d := (dom{r.Source, r.CreatedBy}); (d.spell == "Dominate Person" || d.spell == "Dominate Beast" || d.spell == "Dominate Monster") && !seen[d] {
			seen[d] = true
			list = append(list, d)
		}
	}
	for _, d := range list {
		dc := 13
		if d.caster > 0 {
			dc = spellSaveDCOf(d.caster)
		}
		sv := monsterSpellSave(lobbyID, id, "wis", dc)
		if !sv.Success {
			out += fmt.Sprintf(" %s repeats the save against %s: %s: it stays charmed.", name, d.spell, sv.Line(name))
			continue
		}
		removeConditionsWhere(lobbyID, id, func(r conditionRecord) bool { return r.Source == d.spell })
		if d.caster > 0 {
			db.Exec(`UPDATE characters SET concentrating_on = '' WHERE id = $1 AND (concentrating_on ILIKE $2 OR concentrating_on ILIKE $3)`, d.caster, d.spell, strings.ReplaceAll(strings.ToLower(d.spell), " ", "-"))
		}
		out += fmt.Sprintf(" %s repeats the save against %s: %s: the spell ends.", name, d.spell, sv.Line(name))
	}
	return out
}

// wakeOnDamageSleep: Hypnotic Pattern and Sleep end on damage, and the turning of Turn Undead.
func wakeOnDamageSleep(lobbyID, id int, name string) string {
	n, tn, poison := 0, 0, 0
	for _, r := range removeConditionsWhere(lobbyID, id, func(r conditionRecord) bool {
		return strings.HasPrefix(r.Source, "Turn Undead") || r.Source == "Hypnotic Pattern" || r.Source == "Sleep" ||
			(r.Condition == "unconscious" && r.Notes == notesEndsWithPoisonOrDamage)
	}) {
		if strings.HasPrefix(r.Source, "Turn Undead") {
			tn++
		} else if r.Notes == notesEndsWithPoisonOrDamage {
			poison++
		} else {
			n++
		}
	}
	var note string
	if n > 0 {
		note = fmt.Sprintf(" The damage snaps %s out of the spell.", name)
	} else if tn > 0 {
		note = fmt.Sprintf(" The damage ends the turning of %s (Turn Undead).", name) // F85
	}
	if poison > 0 {
		note += fmt.Sprintf(" The damage wakes %s.", name)
	}
	return note
}

// spellCreatureType: spells that affect only one creature type (SRD 5.2.1): a target of another type is unaffected.
var spellCreatureType = map[string]string{"hold-person": "humanoid", "charm-person": "humanoid", "dominate-person": "humanoid", "animal-friendship": "beast", "dominate-beast": "beast"}

func catalogCreatureType(key string) string {
	if key == "" {
		return ""
	}
	var kind string
	if err := monsterQueryRow(`SELECT COALESCE(type,'') FROM monsters WHERE slug=$1`, key).Scan(&kind); err != nil {
		return ""
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if words := strings.Fields(kind); len(words) >= 4 && words[0] == "swarm" && words[1] == "of" {
		switch plural := strings.TrimSuffix(words[3], "("); plural {
		case "monstrosities":
			return "monstrosity"
		case "undead":
			return plural
		case "aberrations", "beasts", "celestials", "constructs", "dragons", "elementals", "feys",
			"fiends", "giants", "humanoids", "oozes", "plants":
			return strings.TrimSuffix(plural, "s")
		case "fey":
			return plural
		}
	}
	if i := strings.IndexAny(kind, " ("); i > 0 {
		kind = kind[:i]
	}
	return kind
}

// Polymorph retains the target's type (SRD 5.2.1 p.153). When a monster's
// current key holds the Beast form, its pre-transformation key lives in the
// stored spell snapshot. Read that snapshot without advancing spell effects.
func monsterTypeOf(lobbyID, id int) string {
	if id >= 0 {
		return ""
	}
	kind := catalogCreatureType(monsterKeyOfCombatant(lobbyID, id))
	if kind != "beast" {
		return kind
	}
	var detail string
	err := db.QueryRow(`SELECT detail FROM spell_effects WHERE lobby_id=$1 AND target_id=$2 AND kind='polymorph' ORDER BY id DESC LIMIT 1`, lobbyID, id).Scan(&detail)
	if errors.Is(err, sql.ErrNoRows) {
		return kind
	}
	if err != nil {
		return ""
	}
	snapshotLobby, original, valid := decodeMonsterPolymorph(detail)
	if !valid || snapshotLobby != lobbyID {
		return ""
	}
	key, _ := original["key"].(string)
	return catalogCreatureType(key)
}

// A fight entry and a companion form read their condition defenses from the
// same catalog data (custom compendium or SRD).
func catalogConditionImmune(key, cond string) bool {
	if key == "" || cond == "" {
		return false
	}
	var immunities string
	if err := monsterQueryRow(`SELECT COALESCE(condition_immunities,'') FROM monsters WHERE slug=$1`, key).Scan(&immunities); err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(immunities), conditionBase(cond))
}

func monsterConditionImmune(lobbyID, id int, cond string) bool {
	return catalogConditionImmune(monsterKeyOfCombatant(lobbyID, id), cond)
}
