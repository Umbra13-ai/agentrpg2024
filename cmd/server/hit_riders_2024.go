package main

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// The on-hit riders (SRD 5.2.1): extra damage that an effect of the attacker's adds "whenever you hit" a creature with an attack
// roll. This file is the one owner. A rider is rolled after the outcome and only on a hit (CLAUDE.md, attack rolls), by every attack
// route of a character:
//   - a weapon attack or Unarmed Strike against a monster (the main and off-hand attacks, Horde Breaker, a Monk's strikes, Cleave, an
//     Opportunity Attack, a readied attack): resolveAttackOnMonster, inside the hit's own damage (ridersOnMonster);
//   - a weapon attack at a character, which the GM applies: the reply's damage (actions.go);
//   - a spell attack: a single spell attack and each beam of Eldritch Blast and Scorching Ray (spell_resolution.go), a spell attack
//     at a character (spell_effects.go), Spiritual Weapon and Flame Blade (spells_more_2024.go);
//   - an attack roll an item makes as the character's own (the Dancing Sword, the Ring of the Ram, Iron Bands);
//   - a companion's attack: a Wild Shape form is the character itself and gets its riders; a familiar or a steed has none of its
//     owner's (Hex says "you").
// Before Nov 21 Hex and Hunter's Mark were added only by the main weapon attack, before the outcome and as the weapon's damage type;
// a spell attack, a beam, the off-hand attack, a Monk's strike and Cleave never added them (feedback log 65, I-KESTREL-3, -4).
// A new rider is one row of hitRiderRules.

// hitRider is one rider of a hit, rolled.
type hitRider struct {
	Name   string // "Hex"
	Dice   string // as rolled: "1d6", or "2d6" on a Critical Hit
	Damage int
	Type   string // lower case: "necrotic"
}

// hitRiderRule: an effect that adds damage to the attacker's hits on a target. dice answers its dice and damage type when the
// attacker's hit on targetID carries it ("" when it doesn't).
type hitRiderRule struct {
	Name string
	dice func(attackerID, targetID int) (dice, dtype string)
}

var hitRiderRules = []hitRiderRule{
	// Hex: "you deal an extra 1d6 Necrotic damage to the target whenever you hit it with an attack roll"
	{"Hex", func(a, t int) (string, string) {
		if markOn(a, t) == "hex" {
			return "1d6", "necrotic"
		}
		return "", ""
	}},
	// Hunter's Mark: "you deal an extra 1d6 Force damage to the target whenever you hit it with an attack roll"; Foe Slayer (Ranger
	// level 20): the damage die is a d10
	{"Hunter's Mark", func(a, t int) (string, string) {
		if markOn(a, t) == "hunters-mark" {
			return fmt.Sprintf("1d%d", markDieSides(a)), "force"
		}
		return "", ""
	}},
}

// markOn: the mark spell ("hex", "hunters-mark") the attacker concentrates on with targetID as its target ("" for none).
func markOn(attackerID, targetID int) string {
	var conc string
	if db.QueryRow(`SELECT COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, attackerID).Scan(&conc) != nil || conc == "" {
		return ""
	}
	slug, id := parseMarkSpell(conc)
	if id == 0 || id != targetID {
		return ""
	}
	return slug
}

// hitRiders rolls the riders of the attacker's hit on targetID (a Critical Hit doubles their dice). Call it only once the roll has
// hit. A companion's own attack (its combat id) has none: the riders are its owner's.
func hitRiders(attackerID, targetID int, crit bool) []hitRider {
	if attackerID <= 0 || isCompanionCombatID(attackerID) || targetID == 0 || db == nil {
		return nil
	}
	var out []hitRider
	for _, r := range hitRiderRules {
		dice, dtype := r.dice(attackerID, targetID)
		if dice == "" {
			continue
		}
		out = append(out, hitRider{Name: r.Name, Dice: critDice(dice, crit), Damage: game.RollDamage(dice, crit), Type: dtype})
	}
	return out
}

// riderTotal: the riders' damage before the target's resistances.
func riderTotal(rs []hitRider) int {
	n := 0
	for _, r := range rs {
		n += r.Damage
	}
	return n
}

// riderNote: " (+4 Hex, 1d6 necrotic)" for each rider, as the reply of a hit the GM applies says it.
func riderNote(rs []hitRider) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, " (+%d %s, %s %s)", r.Damage, r.Name, r.Dice, r.Type)
	}
	return b.String()
}

// riderTypes: the riders' damage types, for a hit's Troll Regeneration and the like.
func riderTypes(rs []hitRider) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Type)
	}
	return out
}

// ridersOnMonster works the riders into a hit on a monster that is resolved in one step (resolveAttackOnMonster): each rider's own
// type against the monster's resistances, immunities and vulnerabilities, halved on a Petrified creature. It answers the damage
// the monster takes and the note.
func ridersOnMonster(lobbyID, targetID int, monsterKey string, rs []hitRider) (int, string, map[string]int) {
	total, byType := 0, map[string]int{}
	var b strings.Builder
	for _, r := range rs {
		d, how := adjustMonsterDamage(monsterKey, r.Type, r.Damage)
		d, how = petrifiedDamage(lobbyID, targetID, d, how) // F196
		total += d
		byType[r.Type] += d // the hit's damage by type, saved with it (saveMonsterHit)
		b.WriteString(riderClause(r, d, how))
	}
	return total, b.String(), byType
}

// riderClause: " (+4 Hex, 1d6 necrotic)", with what the target's defenses did.
func riderClause(r hitRider, dealt int, how string) string {
	switch how {
	case "immune":
		return fmt.Sprintf(" (%s: immune to %s damage)", r.Name, r.Type)
	case "resistant":
		return fmt.Sprintf(" (+%d %s, %s %s: it resists)", dealt, r.Name, r.Dice, r.Type)
	case "vulnerable":
		return fmt.Sprintf(" (+%d %s, %s %s: it is vulnerable)", dealt, r.Name, r.Dice, r.Type)
	}
	return fmt.Sprintf(" (+%d %s, %s %s)", dealt, r.Name, r.Dice, r.Type)
}

// damageCharacterWithRiders deals a hit and its riders to a character as one hit (the Hit Point owner: one Concentration save from
// the whole damage, one failure at 0 Hit Points) and answers the clause: "takes 11 fire and necrotic damage (now 5 HP) (+4 Hex, 1d6
// necrotic)". Without riders it is damageCharacter.
func damageCharacterWithRiders(targetID, dmg int, dtype string, crit bool, rs []hitRider) string {
	if len(rs) == 0 {
		return damageCharacter(targetID, dmg, dtype, crit)
	}
	hit := oneHit(dmg, dtype)
	hit.Critical = crit
	types := []string{strings.ToLower(dtype)}
	for _, r := range rs {
		hit.Parts = append(hit.Parts, hpPart{r.Damage, r.Type})
		if !containsStr(types, r.Type) {
			types = append(types, r.Type)
		}
	}
	d, err := damageHP(targetID, hit)
	if dbFailed(err) {
		return fmt.Sprintf("takes no damage (%s)", notFoundOrDBError(err, ""))
	}
	if err != nil {
		return fmt.Sprintf("takes 0 %s damage", dtype)
	}
	return d.Sentence(strings.Join(types, " and ")) + riderNote(rs)
}

// dealRiders deals riders already rolled to a target whose hit damage has been dealt (a spell attack, an item's attack): a monster
// through the monster Hit Point owner (hurtMonster), one change per damage type; a character through the character's (damageHP), as
// part of the same hit (no second Concentration save, no second failure at 0 Hit Points). It answers the clause to add.
func dealRiders(lobbyID, targetID int, rs []hitRider, crit bool) string {
	if len(rs) == 0 {
		return ""
	}
	if targetID > 0 {
		hit := hpHit{Critical: crit, SameHit: true, SkipConcentration: true}
		for _, r := range rs {
			hit.Parts = append(hit.Parts, hpPart{r.Damage, r.Type})
		}
		d, err := damageHP(targetID, hit)
		if err != nil {
			return riderNote(rs) + " (not applied: " + notFoundOrDBError(err, "no such character") + ")"
		}
		return riderNote(rs) + fmt.Sprintf(" (now %d HP)", d.After.HP)
	}
	// one change per type, summed over the riders (several beams' Hex dice are one Necrotic change)
	sum := map[string]int{}
	var order []string
	for _, r := range rs {
		if _, seen := sum[r.Type]; !seen {
			order = append(order, r.Type)
		}
		sum[r.Type] += r.Damage
	}
	var b strings.Builder
	for _, dtype := range order {
		h, ok := hurtMonster(lobbyID, targetID, sum[dtype], dtype, crit)
		if !ok {
			continue
		}
		names := ridersOfType(rs, dtype)
		switch h.How {
		case "immune":
			fmt.Fprintf(&b, " %s: %s is immune to %s damage.", names, h.Name, dtype)
		case "resistant":
			fmt.Fprintf(&b, " %s: +%d %s damage (it resists).", names, h.Dealt, dtype)
		case "vulnerable":
			fmt.Fprintf(&b, " %s: +%d %s damage (it is vulnerable).", names, h.Dealt, dtype)
		default:
			fmt.Fprintf(&b, " %s: +%d %s damage.", names, h.Dealt, dtype)
		}
		if h.After == 0 && h.Before > 0 {
			fmt.Fprintf(&b, " %s is defeated!", h.Name)
		}
	}
	return b.String()
}

// ridersOfType: "Hex (1d6, 1d6)": the riders of one damage type with their dice, for a monster's line.
func ridersOfType(rs []hitRider, dtype string) string {
	byName := map[string][]string{}
	var names []string
	for _, r := range rs {
		if r.Type != dtype {
			continue
		}
		if _, seen := byName[r.Name]; !seen {
			names = append(names, r.Name)
		}
		byName[r.Name] = append(byName[r.Name], r.Dice)
	}
	var parts []string
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s (%s)", n, strings.Join(byName[n], ", ")))
	}
	return strings.Join(parts, ", ")
}

// hitWithRiders: the riders of a hit that has already been dealt, rolled and dealt: the one call of an attack route whose damage is
// its own (a spell attack, Spiritual Weapon, an item's attack, a companion's).
func hitWithRiders(lobbyID, attackerID, targetID int, crit bool) string {
	return dealRiders(lobbyID, targetID, hitRiders(attackerID, targetID, crit), crit)
}

// hexAbilityKey: the ability a Hex names in the caster's words ("Hex on the goblin: Dexterity"), as its key ("dex"), "" when none is
// named. The target has Disadvantage on ability checks made with it: castHexChecks records it, the check owner (check_2024.go) reads it.
func hexAbilityKey(description string) string {
	for _, w := range strings.FieldsFunc(description, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) {
		if len(w) < 3 {
			continue
		}
		if k, ok := game.AbilityKey(w); ok {
			return k
		}
	}
	return ""
}

// hexAbilityIn: the ability a Hex names, spelled out ("Dexterity"), "" when none is named.
func hexAbilityIn(description string) string {
	if k := hexAbilityKey(description); k != "" {
		return game.AbilityName(k)
	}
	return ""
}

// hexChecksCondition: the record a Hex puts on its target (its Detail is the chosen ability's key); it lasts while the caster
// concentrates (Notes "concentration": pruneConcentrationByCaster ends it with the spell).
const hexChecksCondition = "hex_checks"

// castHexChecks records the Hex's ability on its target: "The target has Disadvantage on ability checks made with the chosen ability."
func castHexChecks(lobbyID, casterID, targetID int, ability string) {
	if targetID == 0 || ability == "" {
		return
	}
	addCondition(lobbyID, targetID, condAdd{Condition: hexChecksCondition + ":" + ability, Source: "Hex", Rounds: 600, Notes: "concentration", CreatedBy: casterID, SourceID: casterID})
}

// hexOnChecks: a Hex on the creature names this ability (the check owner's row).
func hexOnChecks(lobbyID, who int, ability string) bool {
	k, ok := game.AbilityKey(ability)
	if !ok {
		return false
	}
	for _, r := range loadConditionRecords(lobbyID, who) {
		if r.Condition == hexChecksCondition && r.Detail == k {
			return true
		}
	}
	return false
}

// markCastNote: what a cast of Hex or Hunter's Mark tells the caster: the creature marked, the server's part (the extra damage on
// every hit, any attack route) and, for Hex, the ability whose checks have Disadvantage.
func markCastNote(spellKey string, lobbyID, targetID int, ability string) string {
	spell := "Hunter's Mark"
	extra := "an extra 1d6 Force damage"
	if spellKey == "hex" {
		spell, extra = "Hex", "an extra 1d6 Necrotic damage"
	}
	if targetID == 0 {
		return fmt.Sprintf(" No creature was named: %s marks nothing, so no extra damage is added. Cast it with target_id (a monster) or target_character_id.", spell)
	}
	note := fmt.Sprintf(" %s is on %s: each of your hits on it with an attack roll (a weapon, a spell attack, each beam) deals %s, rolled by the server.", spell, creatureName(lobbyID, targetID), extra)
	if spellKey == "hex" {
		if ability != "" {
			note += fmt.Sprintf(" It has Disadvantage on %s checks (the server applies it).", ability)
		} else {
			note += " Name the ability whose checks it makes with Disadvantage in the description (Strength, Dexterity, Constitution, Intelligence, Wisdom or Charisma); without it nothing is applied."
		}
	}
	return note
}
