package main

// Fixes from Aldric's coverage session (level20-battle-tests/s21_aldric_cleric.py, Nov 8 2026; FINDINGS F81-F90):
//	Divine Strike          the combat attack path (attack_target.go) never added Blessed Strikes' Radiant damage; once a turn (F82)
//	Supreme Healing        Divine Spark's healing rolled its dice; a Life Cleric of level 17 uses the maximum (F84, cleric_2024.go)
//	Blessed Healer         it read the target from the description only; target_id is a creature other than the caster (F83, actions.go)
//	Turn Undead            the turning ends when the Undead takes damage (F85, wakeOnDamage)
//	Mace of Disruption     +2d6 Radiant against Fiends and Undead; at 25 Hit Points or fewer a DC 15 Wisdom save or destroyed (F86)
//	Pearl of Power         a Magic action: one expended spell slot of level 3 or lower comes back, once a dawn (F87)
//	Aura of Life           allies within 30 feet: Resistance to Necrotic; an ally at 0 Hit Points regains 1 at the start of its turn (F88)
//	Divine Word            Charisma save; the Hit Point table; Celestials, Elementals, Fey and Fiends sent home (F89)
//	Revivify, Raise Dead, Resurrection, True Resurrection   a dead character returns (1 Hit Point, or all); the -4 penalty noted (F90)
//	Dwarven Resilience     the Dwarf's Resistance to Poison and Advantage on saves against being Poisoned (F81, game/species.go)

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

var divineStrikeUsed sync.Map // charID -> the combat turn marker of the turn the Divine Strike was dealt

// divineStrikeHit: Blessed Strikes (Divine Strike), once on each of the Cleric's turns when a weapon attack hits: 1d8 Radiant (or
// Necrotic, "necrotic" in the description) from level 7, 2d8 from level 14. Out of combat each attack counts as its own turn.
func divineStrikeHit(lobbyID, charID int, monsterKey string, crit bool, description string) (int, string) {
	class, _, level := subclassOf(charID)
	if !strings.EqualFold(class, "cleric") || level < 7 || !choseBlessedStrikesDivine(charID) {
		return 0, ""
	}
	if marker := combatTurnMarker(lobbyID); marker != "" {
		if v, ok := divineStrikeUsed.Load(charID); ok && v.(string) == marker {
			return 0, ""
		}
		divineStrikeUsed.Store(charID, marker)
	}
	dice := "1d8"
	if level >= 14 {
		dice = "2d8"
	}
	dtype := "radiant"
	if strings.Contains(strings.ToLower(description), "necrotic") {
		dtype = "necrotic"
	}
	d, how := adjustMonsterDamage(monsterKey, dtype, game.RollDamage(dice, crit))
	note := fmt.Sprintf(" Divine Strike: +%d %s (%s)", d, titleCase(dtype), dice)
	if how == "immune" {
		return 0, fmt.Sprintf(" Divine Strike: it is immune to %s damage.", titleCase(dtype))
	}
	if how == "resistant" {
		note += ", resisted"
	}
	return d, note
}

// maceOfDisruptionHit: a hit on a Fiend or an Undead with the Mace of Disruption (attuned) deals 2d6 Radiant more; if the target has 25
// Hit Points or fewer after that, it makes a DC 15 Wisdom save or is destroyed; on a success it is Frightened until the end of the
// attacker's next turn.
func maceOfDisruptionHit(lobbyID, charID, targetID int, monsterKey, name string, crit bool, hpAfter int) (extra int, dies bool, text string) {
	if _, ok := attunedTo(charID, "mace of disruption"); !ok {
		return 0, false, " (the Mace of Disruption needs attunement for its Radiant damage)"
	}
	kind := monsterKind(monsterKey)
	if kind != "fiend" && kind != "undead" {
		return 0, false, ""
	}
	d, how := adjustMonsterDamage(monsterKey, "radiant", game.RollDamage("2d6", crit))
	text = fmt.Sprintf(" Mace of Disruption: +%d Radiant (2d6)", d)
	if how == "immune" {
		d, text = 0, " Mace of Disruption: it is immune to Radiant damage"
	}
	left := hpAfter - d
	if left > 0 && left <= 25 {
		if saved, total := monsterSave(lobbyID, targetID, "wis", 15); saved {
			addMonsterCondition(lobbyID, targetID, "frightened", "Mace of Disruption", 2, "", 0)
			text += fmt.Sprintf("; at %d Hit Points it makes the DC 15 Wisdom save (%d) and is Frightened until the end of your next turn", left, total)
		} else {
			text += fmt.Sprintf("; at %d Hit Points it fails the DC 15 Wisdom save (%d) and is destroyed", left, total)
			return d, true, text + "."
		}
	}
	return d, false, text + "."
}

// pearlOfPower: a Magic action regains one expended spell slot of level 3 or lower (the level named in the description, else the
// highest expended), once until the next dawn.
func pearlOfPower(charID int, it invItem, description string) string {
	if _, ok := attunedTo(charID, "pearl of power"); !ok {
		return "Cannot use the Pearl of Power: you must be attuned to it (POST /api/characters/attune). Nothing was spent."
	}
	if dawnUsed(it, "pearl") {
		return "Cannot use the Pearl of Power: it can't be used again until the next dawn. Nothing was spent."
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	used := map[string]int{}
	json.Unmarshal(raw, &used)
	want := 0
	for _, f := range strings.Fields(strings.ToLower(description)) {
		if n, err := strconv.Atoi(strings.Trim(f, ".,:;")); err == nil && n >= 1 && n <= 3 {
			want = n
		}
	}
	if want == 0 {
		for l := 3; l >= 1; l-- {
			if used[strconv.Itoa(l)] > 0 {
				want = l
				break
			}
		}
	}
	if want == 0 || used[strconv.Itoa(want)] <= 0 {
		return "Cannot use the Pearl of Power: you have no expended spell slot of level 3 or lower to regain. Nothing was spent."
	}
	used[strconv.Itoa(want)]--
	b, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET spell_slots_used = $1 WHERE id = $2`, b, charID)
	markDawnUsed(charID, it, "pearl")
	return fmt.Sprintf("Pearl of Power (a Magic action): you regain one expended level %d spell slot. The pearl can't be used again until the next dawn.", want)
}

// dwarvenResilience: the character is a Dwarf (Resistance to Poison; Advantage on saves to avoid or end the Poisoned condition).
func dwarvenResilience(charID int) bool {
	var species string
	db.QueryRow(`SELECT lower(COALESCE(species,'')) FROM characters WHERE id = $1`, charID).Scan(&species)
	return strings.Contains(species, "dwarf")
}

// ---------------------------------------------------------------- Aura of Life

// auraOfLifeCovers: the creature is within 30 feet of a caster whose Aura of Life holds (the caster included). Without a battle map
// every ally in the campaign counts as within the aura.
func auraOfLifeCovers(lobby, id int) bool {
	auras := lobbySpellRows(lobby, "aura_of_life")
	if len(auras) == 0 || id <= 0 {
		return false
	}
	m, onMap := activeEncounterMap(lobby)
	for _, a := range auras {
		if a.CasterID == id {
			return true
		}
		if !onMap {
			return true
		}
		c, ok1 := m.token(a.CasterID)
		t, ok2 := m.token(id)
		if ok1 && ok2 && tokenDistanceFeet(c, t) <= 30 {
			return true
		}
	}
	return false
}

// auraOfLifeRevives: an ally at 0 Hit Points (not dead) who starts its turn in the aura regains 1 Hit Point.
func auraOfLifeRevives(lobby, id int) string {
	if id <= 0 || !auraOfLifeCovers(lobby, id) {
		return ""
	}
	var hp int
	var dead bool
	if db.QueryRow(`SELECT hp, COALESCE(is_dead,false) FROM characters WHERE id = $1`, id).Scan(&hp, &dead) != nil || dead || hp > 0 {
		return ""
	}
	db.Exec(`UPDATE characters SET hp = 1, is_stable = false, death_save_successes = 0, death_save_failures = 0 WHERE id = $1`, id)
	removeCondition(id, "unconscious")
	return fmt.Sprintf("Aura of Life: %s starts their turn in the aura at 0 Hit Points and regains 1 Hit Point.", creatureName(lobby, id))
}

// ---------------------------------------------------------------- Divine Word

var planarTypes = map[string]bool{"celestial": true, "elemental": true, "fey": true, "fiend": true}

// divineWord: each chosen creature makes a Charisma save; on a failure, by its Hit Points: 0-20 dies, 21-30 Blinded, Deafened and
// Stunned for 1 hour, 31-40 Blinded and Deafened for 10 minutes, 41-50 Deafened for 1 minute. A Celestial, Elemental, Fey or Fiend
// that fails is sent back to its plane of origin for 24 hours (the GM removes it from the fight).
func divineWord(charID, lobby int, saveDC int, targets []int) string {
	if len(targets) == 0 {
		return " Divine Word: name the creatures with target_id or target_ids; none was named, so nothing happens."
	}
	var lines []string
	for _, id := range targets {
		name := creatureName(lobby, id)
		saved, total := creatureSave(lobby, id, "cha", saveDC)
		if saved {
			lines = append(lines, fmt.Sprintf("%s succeeds on the Charisma save (%d vs DC %d)", name, total, saveDC))
			continue
		}
		hp := 0
		if id > 0 {
			db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, id).Scan(&hp)
		} else {
			for _, e := range combatEntries(lobby) {
				if asInt(e["id"]) == id {
					hp = asInt(e["hp"])
				}
			}
		}
		effect := "is unaffected by the word (more than 50 Hit Points)"
		switch {
		case hp <= 20:
			effect = "dies"
			if id > 0 {
				db.Exec(`UPDATE characters SET hp = 0, is_dead = true, is_stable = false WHERE id = $1`, id)
			} else {
				editMonsterEntry(lobby, id, func(e map[string]interface{}) { e["hp"] = 0 })
			}
		case hp <= 30:
			for _, c := range []string{"blinded", "deafened", "stunned"} {
				addMonsterCondition(lobby, id, c, "Divine Word", 600, "", 0)
			}
			effect = "has the Blinded, Deafened and Stunned conditions for 1 hour"
		case hp <= 40:
			for _, c := range []string{"blinded", "deafened"} {
				addMonsterCondition(lobby, id, c, "Divine Word", 100, "", 0)
			}
			effect = "has the Blinded and Deafened conditions for 10 minutes"
		case hp <= 50:
			addMonsterCondition(lobby, id, "deafened", "Divine Word", 10, "", 0)
			effect = "has the Deafened condition for 1 minute"
		}
		line := fmt.Sprintf("%s fails the Charisma save (%d vs DC %d) and %s", name, total, saveDC, effect)
		if id < 0 && planarTypes[monsterBaseType(lobby, id)] && hp > 20 {
			addMonsterCondition(lobby, id, "banished", "Divine Word", 14400, "", 0)
			line += "; as a " + titleCase(monsterBaseType(lobby, id)) + " it is forced back to its plane of origin for 24 hours (the GM removes it from the fight)"
		}
		lines = append(lines, line)
	}
	return " Divine Word: " + strings.Join(lines, "; ") + "."
}

// ---------------------------------------------------------------- Revivify, Raise Dead, Resurrection, True Resurrection

var revivalSpells = map[string]struct {
	Full    bool   // all Hit Points, else 1
	Penalty bool   // the -4 penalty to D20 Tests, down by 1 each Long Rest
	Limit   string // how long dead
}{
	"revivify":          {false, false, "within the last minute"},
	"raise-dead":        {false, true, "no longer than 10 days"},
	"resurrection":      {true, true, "no more than a century"},
	"true-resurrection": {true, false, "no longer than 200 years"},
}

// reviveSpell brings a dead character back ("" for other spells or no dead target).
func reviveSpell(charID, lobby int, key string, spell SRDSpell, targets []int) string {
	r, ok := revivalSpells[key]
	if !ok {
		return ""
	}
	if len(targets) == 0 {
		return fmt.Sprintf(" %s: touch the dead creature (target_id); none was named, so nothing happens. The slot and the material are spent.", spell.Name)
	}
	var lines []string
	for _, id := range targets {
		if id <= 0 {
			lines = append(lines, "the GM decides for "+creatureName(lobby, id))
			continue
		}
		var name string
		var dead bool
		var max int
		if db.QueryRow(`SELECT name, COALESCE(is_dead,false), max_hp FROM characters WHERE id = $1`, id).Scan(&name, &dead, &max) != nil {
			continue
		}
		if !dead {
			lines = append(lines, name+" isn't dead, so the spell does nothing for them")
			continue
		}
		hp := 1
		if r.Full {
			hp = max
		}
		db.Exec(`UPDATE characters SET hp = $1, is_dead = false, is_stable = false, death_save_successes = 0, death_save_failures = 0, temp_hp = 0 WHERE id = $2`, hp, id)
		removeCondition(id, "unconscious")
		db.Exec(`DELETE FROM condition_records WHERE target_id = $1 AND condition IN ('unconscious','poisoned')`, id)
		syncListFromRecords(id)
		line := fmt.Sprintf("%s returns to life with %d Hit Point%s (dead %s: the GM confirms)", name, hp, map[bool]string{true: "", false: "s"}[hp == 1], r.Limit)
		if r.Penalty {
			addSpellEffect(lobby, id, charID, spell.Name, "revival_penalty", "4", 999999, "", 0)
			line += "; coming back is an ordeal: -4 to D20 Tests, 1 less after each Long Rest (the GM applies it)"
		}
		lines = append(lines, line)
	}
	return " " + spell.Name + ": " + strings.Join(lines, "; ") + "."
}

// greaterRestoration removes one effect from the touched creature: the one named in the description, else the first it has of
// 1 Exhaustion level, the Charmed condition, the Petrified condition. Curses end through endCursesBySpell; an ability score or Hit
// Point maximum reduction is the GM's to restore.
func greaterRestoration(charID, lobby int, description string, targets []int) string {
	id := charID
	if len(targets) > 0 {
		id = targets[0]
	}
	name := creatureName(lobby, id)
	d := strings.ToLower(description)
	if id <= 0 {
		return " Greater Restoration: the GM removes one effect from " + name + "."
	}
	var ex int
	db.QueryRow(`SELECT COALESCE(exhaustion_level,0) FROM characters WHERE id = $1`, id).Scan(&ex)
	conds := getCharConditions(id)
	has := func(c string) bool {
		for _, x := range conds {
			if strings.EqualFold(strings.SplitN(x, ":", 2)[0], c) {
				return true
			}
		}
		return false
	}
	pick := ""
	switch {
	case strings.Contains(d, "exhaust"):
		pick = "exhaustion"
	case strings.Contains(d, "charm"):
		pick = "charmed"
	case strings.Contains(d, "petrif"):
		pick = "petrified"
	case strings.Contains(d, "curse") || strings.Contains(d, "score") || strings.Contains(d, "maximum"):
		return " Greater Restoration: " + name + "'s curse, ability score reduction or Hit Point maximum reduction is removed (the GM restores the number)."
	case ex > 0:
		pick = "exhaustion"
	case has("charmed"):
		pick = "charmed"
	case has("petrified"):
		pick = "petrified"
	}
	switch pick {
	case "exhaustion":
		if ex <= 0 {
			return " Greater Restoration: " + name + " has no Exhaustion level to remove."
		}
		return " Greater Restoration: one Exhaustion level removed from " + name + " (" + strings.TrimSpace(addExhaustion(id, -1)) + ")."
	case "charmed", "petrified":
		if !has(pick) {
			return fmt.Sprintf(" Greater Restoration: %s doesn't have the %s condition.", name, titleCase(pick))
		}
		removeCondition(id, pick)
		db.Exec(`DELETE FROM condition_records WHERE target_id = $1 AND condition = $2`, id, pick)
		syncListFromRecords(id)
		return fmt.Sprintf(" Greater Restoration: the %s condition on %s ends.", titleCase(pick), name)
	}
	return " Greater Restoration: " + name + " has none of the effects the spell removes (Exhaustion, Charmed, Petrified; name a curse or a reduction for the GM)."
}

func init() {
	spellEffectLabel["revival_penalty"] = "Revived"
	spellEffectText["revival_penalty"] = "-4 to D20 Tests after returning from death; 1 less after each Long Rest"
	spellEffectLabel["banished"] = "Banished"
	spellEffectText["banished"] = "Sent back to its plane of origin (Divine Word): gone for 24 hours"
	noConcentration["revival_penalty"] = true
}
