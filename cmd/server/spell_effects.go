package main

// Spells that do more than one thing to one monster (SRD 5.2.1):
//	several targets     "target_ids": [...] on a cast: monsters (negative ids) each make their own saving throw against an area or
//	                    multi-target spell; characters (positive ids) are healed by a healing spell. The damage or healing is rolled once.
//	healing             a healing spell restores Hit Points to the named character, the target_ids, or the caster; a creature at 0 Hit Points
//	                    wakes up (death saves reset).
//	condition spells    Hold Person, Sleep, Entangle, Web, Fear and others: a failed saving throw puts the condition on the monster
//	                    (condition_records, with the end-of-turn saving throw where the spell allows one).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
			if !monsterConditionImmune(lobby, id, c) {
				immuneAll = false
			}
		}
		if immuneAll {
			lines = append(lines, fmt.Sprintf("%s is immune to the %s condition.", name, strings.Join(cs.Conditions, " and ")))
			continue
		}
		heightened := heightenedDisadvantage(charID, id) // Heightened Spell (F194)
		if heightened {
			typeSaveDisadvantage.Store(lobby, id)
		}
		if fightingItAdvantage(spellKey) && currentTurnID(lobby) != 0 {
			fightingSaveAdvantage.Store(lobby, id) // "Advantage on the save if you or your allies are fighting it" (F225)
		}
		saved, total := monsterSpellSave(lobby, id, cs.Save, saveDC) // Magic Resistance gives Advantage
		fightingSaveAdvantage.Delete(lobby)
		typeSaveDisadvantage.Delete(lobby)
		if saved {
			lines = append(lines, fmt.Sprintf("%s saves (%d vs DC %d)%s.", name, total, saveDC, map[bool]string{true: " despite Disadvantage (Heightened Spell)"}[heightened]))
			continue
		}
		if heightened {
			lines = append(lines, fmt.Sprintf("%s fails its save with Disadvantage (Heightened Spell).", name))
		}
		endSave := ""
		if cs.EndSave {
			endSave = cs.Save
		}
		for _, c := range cs.Conditions {
			addMonsterCondition(lobby, id, c, spellName, extendRounds(charID, cs.Rounds), endSave, saveDC) // Extended Spell doubles it (F194)
			if sp, ok := srdSpellsMemory[spellKey]; ok && strings.Contains(strings.ToLower(sp.Duration), "concentration") {
				// F56: it lasts while the caster keeps Concentration (loadConditionRecords drops it when that ends)
				db.Exec(`UPDATE condition_records SET created_by = $1, notes = 'concentration' WHERE lobby_id = $2 AND target_id = $3 AND condition = $4 AND source = $5 AND COALESCE(created_by,0) = 0`,
					charID, lobby, id, c, spellName)
			}
		}
		lines = append(lines, fmt.Sprintf("%s fails its %s save (%d vs DC %d) and is %s.", name, strings.ToUpper(cs.Save), total, saveDC, strings.Join(cs.Conditions, " and ")))
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

// healCharacter restores Hit Points (not above the maximum). A creature at 0 Hit Points wakes up with its death saves cleared.
func healCharacter(targetID, amount int) (name string, before, after int) {
	if _, wounded := charBuff(targetID, "no_regain"); wounded {
		amount = 0 // Sword of Wounding: can't regain Hit Points (F171)
	}
	var maxHP int
	if db.QueryRow(`SELECT name, hp, max_hp FROM characters WHERE id = $1`, targetID).Scan(&name, &before, &maxHP) != nil {
		return "", 0, 0
	}
	after = before + amount
	if after > maxHP {
		after = maxHP
	}
	if before <= 0 && after > 0 {
		db.Exec(`UPDATE characters SET hp = $1, death_save_successes = 0, death_save_failures = 0, is_stable = false WHERE id = $2`, after, targetID)
		removeCondition(targetID, "unconscious")
	} else {
		db.Exec(`UPDATE characters SET hp = $1 WHERE id = $2`, after, targetID)
	}
	return
}

// applySpellHealing heals the characters a healing spell is aimed at and says who got what.
func applySpellHealing(charID, heal, healMax int, description string) string {
	targets := spellCharacterTargets(charID)
	if len(targets) == 0 {
		if id := parseTargetFromDescription(description, charID); id > 0 {
			targets = []int{id}
		} else {
			targets = []int{charID}
		}
	}
	var parts []string
	for _, id := range targets {
		amount, beacon := heal, ""
		if healMax > heal && beaconMaxHealing(id) {
			amount, beacon = healMax, " (Beacon of Hope: the maximum)" // buff_spells_2024.go
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

// characterSave rolls a saving throw for a character: ability modifier, Proficiency Bonus where the class has the save, Aura of Protection,
// and the Exhaustion penalty.
func characterSave(charID int, ability string, dc int) (bool, int) {
	var class string
	var level, lobby int
	db.QueryRow(`SELECT class, level, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&class, &level, &lobby)
	bonus := game.Modifier(abilityScoreOf(charID, ability))
	var saves string
	db.QueryRow(`SELECT COALESCE(saving_throws,'') FROM classes WHERE slug = $1`, strings.ToLower(class)).Scan(&saves)
	proficient := strings.ToLower(class) == "monk" && level >= 14
	for _, s := range strings.Split(saves, ",") {
		if strings.TrimSpace(strings.ToLower(s)) == ability {
			proficient = true
		}
	}
	if proficient {
		bonus += game.ProficiencyBonus(level)
	}
	bonus = wildShapeHigher(charID, "save:"+ability, bonus) // the beast's save modifier when it's higher
	if aura, _ := getPaladinAuraBonus(lobby, charID); aura > 0 {
		bonus += aura
	}
	bonus -= game.ExhaustionD20Penalty(exhaustionLevelOf(charID))
	adj, _ := spellRollAdjust(lobby, charID, "save:"+ability) // Bless, Bane, Slow
	bonus += adj
	if ability == "dex" {
		bonus += halfCoverBonus(lobby, charID) // Smite of Protection
	}
	bonus += magicSaveBonus(charID) // Ring and Cloak of Protection, Stone of Good Luck, Staff of Power, Luck Blade (F60: missing here)
	bonus += buffSaveBonus(charID)  // Warding Bond +1 (buff_spells_2024.go)
	dis, adv := getSaveDisadvantage(charID, ability), spellSaveAdvantage(lobby, charID, ability)
	d20 := func() int {
		r := game.RollDie(20)
		if dis && !adv {
			if b := game.RollDie(20); b < r {
				r = b
			}
		} else if adv && !dis {
			if b := game.RollDie(20); b > r {
				r = b // Haste, a magic item against a spell
			}
		}
		return r
	}
	roll := d20()
	auto := autoFailsSave(charID, ability)
	ok := !auto && roll+bonus >= dc
	if !ok && !auto && luckReroll(charID, false) { // the Luck Blade's Luck: a failed D20 Test is rerolled; the second roll stands
		roll = d20()
		ok = roll+bonus >= dc
	}
	if t, note := indomitableMight(charID, ability, roll+bonus); note != "" && !auto { // F102
		return t >= dc, t
	}
	return ok, roll + bonus
}

func damageCharacter(targetID, dmg int, dtype string) string {
	body, _ := json.Marshal(map[string]interface{}{"damage": dmg, "damage_type": strings.ToLower(dtype)})
	rr := httptest.NewRecorder()
	handleDamage(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)), targetID)
	var out map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &out)
	text := fmt.Sprintf("takes %d %s damage", asInt(out["damage_dealt"]), dtype)
	if hp, ok := out["hp"]; ok {
		text += fmt.Sprintf(" (now %v HP)", hp)
	}
	if c, ok := out["concentration"].(string); ok {
		text += " [" + c + "]"
	}
	return text
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
		var ac int
		if db.QueryRow(`SELECT name, ac FROM characters WHERE id = $1`, id).Scan(&name, &ac) != nil {
			continue
		}
		if isSpellAttack(spell) {
			lobby := lobbyOfChar(charID)
			ac += spellACAdjust(lobby, id)
			adj, _ := spellRollAdjust(lobby, charID, "attack")
			roll := game.RollDie(20)
			if outlined(lobby, id) {
				if b := game.RollDie(20); b > roll {
					roll = b
				}
			}
			if roll == 1 || (roll != 20 && roll+game.ProficiencyBonus(charLevel)+spellMod+adj < ac) {
				lines = append(lines, fmt.Sprintf(" Spell attack against %s: MISS.", name))
				continue
			}
			d := rolled
			label := "HIT"
			if roll == 20 {
				d += game.RollDamage(damageDice, false)
				label = "CRITICAL HIT"
			}
			lines = append(lines, fmt.Sprintf(" Spell attack against %s: %s, %s.", name, label, damageCharacter(id, d, dtype)))
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
		saved, total := characterSave(id, ability, saveDC)
		d := rolled
		how := fmt.Sprintf("fails its %s save (%d vs DC %d)", strings.ToUpper(ability), total, saveDC)
		shielded := sculpted(charID)              // F119: Sculpt Spells
		carefully := !shielded && careful(charID) // F194: Careful Spell
		if shielded || carefully {
			saved = true
			if onSave == "half" {
				onSave = "none"
			}
		}
		if saved {
			how = fmt.Sprintf("saves (%d vs DC %d)", total, saveDC)
			if shielded {
				how = "is shielded by Sculpt Spells (an automatic success)"
			} else if carefully {
				how = "is protected by Careful Spell (an automatic success)"
			}
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
	if res, err := db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND source IN ('Suggestion','Mass Suggestion')`, lobbyID, id); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			out += fmt.Sprintf(" The damage ends the Suggestion on %s.", name)
		}
	}
	if id >= 0 {
		return out
	}
	rows, err := db.Query(`SELECT DISTINCT source, COALESCE(created_by,0) FROM condition_records WHERE lobby_id = $1 AND target_id = $2
		AND source IN ('Dominate Person','Dominate Beast','Dominate Monster')`, lobbyID, id)
	if err != nil {
		return out
	}
	type dom struct {
		spell  string
		caster int
	}
	var list []dom
	for rows.Next() {
		var d dom
		rows.Scan(&d.spell, &d.caster)
		list = append(list, d)
	}
	rows.Close()
	for _, d := range list {
		dc := 13
		if d.caster > 0 {
			dc = spellSaveDCOf(d.caster)
		}
		saved, total := monsterSpellSave(lobbyID, id, "wis", dc)
		if !saved {
			out += fmt.Sprintf(" %s repeats the Wisdom save against %s (%d vs DC %d) and fails: it stays charmed.", name, d.spell, total, dc)
			continue
		}
		db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND source = $3`, lobbyID, id, d.spell)
		if d.caster > 0 {
			db.Exec(`UPDATE characters SET concentrating_on = '' WHERE id = $1 AND (concentrating_on ILIKE $2 OR concentrating_on ILIKE $3)`, d.caster, d.spell, strings.ReplaceAll(strings.ToLower(d.spell), " ", "-"))
		}
		out += fmt.Sprintf(" %s repeats the Wisdom save against %s (%d vs DC %d) and succeeds: the spell ends.", name, d.spell, total, dc)
	}
	return out
}

// wakeOnDamageSleep: Hypnotic Pattern and Sleep end on damage, and the turning of Turn Undead.
func wakeOnDamageSleep(lobbyID, id int, name string) string {
	turned, _ := db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND source LIKE 'Turn Undead%'`, lobbyID, id)
	res, err := db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND source IN ('Hypnotic Pattern','Sleep')`, lobbyID, id)
	if err != nil {
		return ""
	}
	n, _ := res.RowsAffected()
	var tn int64
	if turned != nil {
		tn, _ = turned.RowsAffected()
	}
	if n == 0 && tn == 0 {
		return ""
	}
	if id > 0 {
		syncListFromRecords(id)
	}
	if n == 0 {
		return fmt.Sprintf(" The damage ends the turning of %s (Turn Undead).", name) // F85
	}
	return fmt.Sprintf(" The damage snaps %s out of the spell.", name)
}

// spellCreatureType: spells that affect only one creature type (SRD 5.2.1): a target of another type is unaffected.
var spellCreatureType = map[string]string{"hold-person": "humanoid", "charm-person": "humanoid", "dominate-person": "humanoid", "animal-friendship": "beast", "dominate-beast": "beast"}

// monsterTypeOf is a combatant's creature type from its stat block ("humanoid", "undead" ...).
func monsterTypeOf(lobbyID, id int) string {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			var t string
			db.QueryRow(`SELECT COALESCE(type,'') FROM monsters WHERE slug = $1`, fmt.Sprint(e["monster_key"])).Scan(&t)
			t = strings.ToLower(strings.TrimSpace(t))
			if i := strings.IndexAny(t, " ("); i > 0 {
				t = t[:i] // "humanoid (goblinoid)" -> "humanoid"
			}
			return t
		}
	}
	return ""
}

// monsterConditionImmune: the monster's stat block lists Immunity to the condition.
func monsterConditionImmune(lobbyID, id int, cond string) bool {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			var imm string
			db.QueryRow(`SELECT COALESCE(condition_immunities,'') FROM monsters WHERE slug = $1`, fmt.Sprint(e["monster_key"])).Scan(&imm)
			return cond != "" && strings.Contains(strings.ToLower(imm), strings.ToLower(conditionBase(cond)))
		}
	}
	return false
}
