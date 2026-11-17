package main

// Hit riders: damage a character's feature, spell or item adds when one of its attacks hits (feedback log 63). Each is one row of
// hitRiders: which attacks it applies to (any attack roll, a weapon attack, or a weapon or an Unarmed Strike) and
// what it rolls, in its own damage type. Every character attack that hits a monster asks hitRiderDamage once, after the outcome
// (CLAUDE.md: an effect that depends on the outcome is told after it): the weapon routes through resolveAttackOnMonster (the main
// attack, the off-hand attack, a Monk's strikes, Cleave, an Opportunity Attack), the spell attack routes in resolveSpellOnMonster
// (one spell attack, each beam of Eldritch Blast and Scorching Ray), the melee spell attacks (spells_more_2024.go), the Dancing
// Sword and the Ring of the Ram. A new rider is one row.
//
// SRD 5.2.1: Hex "an extra 1d6 Necrotic damage to the target whenever you hit it with an attack roll"; Hunter's Mark "an extra
// 1d6 Force damage to the target whenever you hit it with an attack roll"; Divine Favor "Your weapon attacks deal an extra 1d4
// Radiant damage on a hit"; Blessed Strikes (Divine Strike) "once on each of your turns when you hit a creature with an attack
// roll using a weapon".

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// hitAttack: what kind of attack hit.
type hitAttack struct {
	spell   bool   // a spell attack
	item    bool   // an item's own attack that isn't a weapon's (a ring's ram)
	unarmed bool   // an Unarmed Strike
	weapon  string // the weapon's name (for a weapon attack)
}

// hitInfo: one hit by a character on a monster.
type hitInfo struct {
	lobby, char, target int
	monsterKey          string
	crit                bool
	attack              hitAttack
	damage              int    // the hit's own damage so far (Reduce can't take it below 1)
	damageType          string // the hit's own damage type (Enlarge and the Bracers add to it)
}

// hitRider: one row. applies says which attacks it works with; roll returns the raw damage (before the target's resistances),
// its type and its name as the reply shows it (0 when it doesn't apply now).
type hitRider struct {
	name    string
	applies func(a hitAttack) bool
	roll    func(h hitInfo) (amount int, dtype, label string)
}

func anyAttackRoll(hitAttack) bool     { return true }
func weaponAttack(a hitAttack) bool    { return !a.spell && !a.unarmed && !a.item }
func weaponOrUnarmed(a hitAttack) bool { return !a.spell && !a.item }

var hitRiders = []hitRider{
	{"Hunter's Mark", anyAttackRoll, func(h hitInfo) (int, string, string) { return markRider(h, "hunters-mark") }},
	{"Hex", anyAttackRoll, func(h hitInfo) (int, string, string) { return markRider(h, "hex") }},
	{"Divine Favor", weaponAttack, func(h hitInfo) (int, string, string) {
		if _, ok := charBuff(h.char, "divine_favor"); !ok {
			return 0, "", ""
		}
		return game.RollDamage("1d4", h.crit), "radiant", "Divine Favor"
	}},
	{"Divine Strike", weaponOrUnarmed, divineStrikeRider},
	{"Enlarge/Reduce", weaponOrUnarmed, func(h hitInfo) (int, string, string) {
		n, _ := enlargeHit(h.lobby, h.char, h.damage) // the one Enlarge/Reduce rule (spells_more_2024.go), monsters' hits too
		if n < 0 {
			return n, h.damageType, "Reduce"
		}
		return n, h.damageType, "Enlarge"
	}},
	{"Helm of Brilliance flames", weaponAttack, func(h hitInfo) (int, string, string) {
		if _, on := charBuff(h.char, "helm_flames"); !on {
			return 0, "", ""
		}
		return game.RollDamage("1d6", h.crit), "fire", "Helm of Brilliance flames"
	}},
	{"Bracers of Archery", weaponAttack, func(h hitInfo) (int, string, string) {
		w := strings.ToLower(h.attack.weapon)
		if !strings.Contains(w, "longbow") && !strings.Contains(w, "shortbow") {
			return 0, "", ""
		}
		if _, ok := itemActive(h.char, "bracers of archery", "worn"); !ok {
			return 0, "", ""
		}
		return 2, h.damageType, "Bracers of Archery"
	}},
}

// markRider: Hunter's Mark or Hex on this target (the caster concentrates on it, with the target recorded). A Critical Hit doubles
// the die; a Ranger of level 20 (Foe Slayer) rolls a d10 for Hunter's Mark.
func markRider(h hitInfo, slug string) (int, string, string) {
	var conc string
	if err := db.QueryRow(`SELECT COALESCE(concentrating_on, '') FROM characters WHERE id = $1`, h.char).Scan(&conc); err != nil || conc == "" {
		return 0, "", ""
	}
	if s, target := parseMarkSpell(conc); s != slug || target != h.target || target == 0 {
		return 0, "", ""
	}
	if slug == "hunters-mark" {
		return game.RollDamage(fmt.Sprintf("1d%d", markDieSides(h.char)), h.crit), "force", "Hunter's Mark"
	}
	return game.RollDamage("1d6", h.crit), "necrotic", "Hex"
}

// divineStrikeRider: Blessed Strikes (Divine Strike), once on each of the Cleric's turns: 1d8 Radiant from level 7, 2d8 from 14.
// Out of combat each attack counts as its own turn.
func divineStrikeRider(h hitInfo) (int, string, string) {
	class, _, level := subclassOf(h.char)
	if !strings.EqualFold(class, "cleric") || level < 7 || !choseBlessedStrikesDivine(h.char) {
		return 0, "", ""
	}
	if marker := combatTurnMarker(h.lobby); marker != "" {
		if v, ok := divineStrikeUsed.Load(h.char); ok && v.(string) == marker {
			return 0, "", ""
		}
		divineStrikeUsed.Store(h.char, marker)
	}
	dice := "1d8"
	if level >= 14 {
		dice = "2d8"
	}
	return game.RollDamage(dice, h.crit), "radiant", "Divine Strike"
}

// riderHit: one rider that applied: its raw damage and type, and the damage after the target's resistances.
type riderHit struct {
	label, dtype string
	raw, dealt   int
	how          string // "", "resistant", "immune", "vulnerable"
}

// hitRiderDamage: every rider that applies to this hit, with the target's resistances (adjustMonsterDamage) applied to each in
// its own type. total is the damage they add; text says each one.
func hitRiderDamage(h hitInfo) (total int, text string, hits []riderHit) {
	for _, r := range hitRiders {
		if !r.applies(h.attack) {
			continue
		}
		raw, dtype, label := r.roll(h)
		if raw == 0 {
			continue
		}
		rh := riderHit{label: label, dtype: dtype, raw: raw, dealt: raw}
		if raw > 0 && h.monsterKey != "" {
			rh.dealt, rh.how = adjustMonsterDamage(h.monsterKey, dtype, raw)
		}
		total += rh.dealt
		text += riderText(rh)
		hits = append(hits, rh)
	}
	return total, text, hits
}

// riderText: how the reply says one rider.
func riderText(rh riderHit) string {
	if rh.raw < 0 {
		return fmt.Sprintf(" %s: %d damage.", rh.label, rh.raw)
	}
	switch rh.how {
	case "immune":
		return fmt.Sprintf(" %s: it is immune to %s damage.", rh.label, titleCase(rh.dtype))
	case "resistant":
		return fmt.Sprintf(" %s: +%d %s (it resists).", rh.label, rh.dealt, titleCase(rh.dtype))
	case "vulnerable":
		return fmt.Sprintf(" %s: +%d %s (it is vulnerable).", rh.label, rh.dealt, titleCase(rh.dtype))
	}
	return fmt.Sprintf(" %s: +%d %s.", rh.label, rh.dealt, titleCase(rh.dtype))
}

// dealHitRiders deals the riders of a hit outside resolveAttackOnMonster (a spell attack, an item's attack) on a monster, each in its own type through the monster HP owner
// (hurtMonster: resistances, Petrified, a Troll's Regeneration), and says them.
func dealHitRiders(h hitInfo) string {
	h.monsterKey = "" // hurtMonster applies the target's resistances
	_, _, hits := hitRiderDamage(h)
	text := ""
	for _, rh := range hits {
		if rh.raw <= 0 {
			continue
		}
		if md, ok := hurtMonster(h.lobby, h.target, rh.raw, rh.dtype, h.crit); ok {
			rh.dealt, rh.how = md.Dealt, md.How
			text += riderText(rh)
		}
	}
	return text
}

// markCastNote: what casting Hunter's Mark or Hex says: the creature it is on and that the server adds its damage to each attack
// roll that hits it; for Hex also the ability whose checks the target makes with Disadvantage (named in the description; the GM
// applies it to checks). "" for other spells.
func markCastNote(charID int, spellKey, description string) string {
	if !isMarkSpell(spellKey) {
		return ""
	}
	var conc string
	db.QueryRow(`SELECT COALESCE(concentrating_on, '') FROM characters WHERE id = $1`, charID).Scan(&conc)
	slug, target := parseMarkSpell(conc)
	if target == 0 {
		return " No target was named (send target_id): the extra damage applies only to the creature the spell is on."
	}
	name := creatureName(lobbyOfChar(charID), target)
	dice, dtype := fmt.Sprintf("1d%d", markDieSides(charID)), "Force"
	if slug == "hex" {
		dice, dtype = "1d6", "Necrotic"
	}
	note := fmt.Sprintf(" It is on %s: each of your attack rolls that hits it deals %s %s more (the server adds it).", name, dice, dtype)
	if slug != "hex" {
		return note
	}
	for _, w := range strings.FieldsFunc(description, func(r rune) bool { return r == ' ' || r == ',' || r == '.' || r == ';' || r == ':' }) {
		if k, ok := game.AbilityKey(w); ok {
			return note + fmt.Sprintf(" %s has Disadvantage on %s checks.", name, game.AbilityName(k))
		}
	}
	return note + " Name the ability whose checks it makes with Disadvantage in the description (strength ... charisma); the GM applies it."
}
