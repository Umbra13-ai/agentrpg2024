package main

// The spells that used to show their text and change nothing (BUGS O-7; the project owner, Nov 8 2026: "you can fix the other items in o-7 too"),
// applied by the server (SRD 5.2.1). Each puts a spell effect on its targets (spell_effects, like buff_spells_2024.go); hooks read it.
//	Blink                 at the end of each of the caster's turns 1d6: on 4-6 it is on the Ethereal Plane until the start of its next
//	                      turn (the Plate Armor of Etherealness "ethereal" state: it can't be targeted or affected, nor affect others)
//	Spiritual Weapon      a melee spell attack on casting (1d8 + the spellcasting modifier Force, +1d8 a slot level above 2); later
//	                      turns action "spiritual_weapon" (a Bonus Action): the force moves 20 feet and attacks a creature by it
//	Flame Blade           action "flame_blade" (a Magic action): a melee spell attack (3d6 + modifier Fire, +1d6 a level above 2); the
//	                      blade fills a hand; "let go" drops it, "evoke" (a Bonus Action) brings it back
//	Fly                   a Fly Speed of 60 feet (+1 creature a level above 3); on the battle map "altitude" (feet) with a move; a
//	                      creature still aloft when the spell ends falls (1d6 per 10 feet, Prone)
//	Expeditious Retreat   the Dash action now, and Dash as a Bonus Action while it lasts ({"action": "dash", "description": "bonus"})
//	Enlarge/Reduce        one size category up or down (the map token too); Advantage or Disadvantage on Strength checks and saves;
//	                      weapon and Unarmed Strike hits deal 1d4 more or less (not below 1); an unwilling creature makes a Con save
//	Polymorph             a Wisdom save, or a Beast form of a Challenge Rating up to the target's (its level for a character): a
//	                      character takes the beast's statistics as in Wild Shape and Temporary Hit Points equal to its Hit Points; a
//	                      monster becomes the beast in the turn order and keeps its own Hit Points apart; the spell ends when the beast's
//	                      Hit Points are gone (extra damage carries over), when Concentration ends, or after 1 hour
//	Gaseous Form          Fly Speed 10 feet and nothing else; Resistance to Bludgeoning, Piercing and Slashing; Immunity to Prone;
//	                      Advantage on Strength, Dexterity and Constitution saves; no attacks, spells or objects; ends at 0 Hit Points
//	                      or with action "gaseous_form" ("end", a Magic action)
//	Globe of Invulnerability  a monster's spell of level 5 or lower (+1 a slot level above 6) cast from outside the 10-foot barrier has
//	                      no effect on anything inside it (the barrier stays where it was cast)
//	Antimagic Field       a 10-foot aura that moves with the caster: no one inside can cast spells or use magic items; spells and
//	                      magic from outside have no effect inside; the spell effects on creatures inside are suppressed
//	Magic Circle          a 10-foot-radius Cylinder for the chosen types (Celestials, Elementals, Fey, Fiends, Undead): they attack
//	                      targets inside with Disadvantage, can't Charm or Frighten them, and can't move into it (the GM's map update
//	                      keeps them out); "reverse" keeps them in and protects targets outside
//	Feather Fall          up to five falling creatures: the GM's falling damage is 0 (the spell then ends for that creature)
//	Regenerate            4d8 + 15 Hit Points now and 1 Hit Point at the start of each of the target's turns for 1 hour
//	Darkvision            Darkvision 150 feet for 8 hours
//	Water Breathing       up to ten creatures breathe underwater for 24 hours (gm/suffocation: no suffocation underwater)
//	Jump                  once on each of its turns the creature jumps up to 30 feet for 10 feet of movement (action "jump")

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

var moreSpells = map[string]buffSpell{
	"antimagic-field":          {"antimagic_field", 600, true, nil},
	"aura-of-life":             {"aura_of_life", 100, true, nil},
	"divine-word":              {"divine_word", 0, false, nil},
	"blink":                    {"blink", 10, true, nil},
	"darkvision":               {"darkvision", 4800, false, nil},
	"enlarge-reduce":           {"enlarge_reduce", 10, false, nil},
	"expeditious-retreat":      {"expeditious_retreat", 100, true, nil},
	"feather-fall":             {"feather_fall", 10, false, func(int) int { return 5 }},
	"flame-blade":              {"flame_blade", 100, true, nil},
	"fly":                      {"fly", 100, false, onePlusAbove(3)},
	"gaseous-form":             {"gaseous_form", 600, false, onePlusAbove(3)},
	"globe-of-invulnerability": {"globe_of_invulnerability", 10, true, nil},
	"jump":                     {"jump_spell", 10, false, onePlusAbove(1)},
	"magic-circle":             {"magic_circle", 600, false, nil},
	"polymorph":                {"polymorph", 600, false, nil},
	"regenerate":               {"regenerate", 600, false, nil},
	"spiritual-weapon":         {"spiritual_weapon", 10, true, nil},
	"water-breathing":          {"water_breathing", 14400, false, func(int) int { return 10 }},
}

var moreLabels = map[string][2]string{
	"antimagic_field":          {"Antimagic Field", "A 10-foot aura of antimagic around the caster: no spells or magic items inside"},
	"aura_of_life":             {"Aura of Life", "Allies within 30 feet: Resistance to Necrotic; an ally at 0 Hit Points regains 1 at the start of its turn"},
	"blink":                    {"Blink", "1d6 at the end of each of its turns: 4-6, the Ethereal Plane until its next turn"},
	"darkvision":               {"Darkvision", "Darkvision 150 feet (8 hours)"},
	"enlarge_reduce":           {"Enlarge/Reduce", "One size larger or smaller; Strength checks and saves and weapon damage change"},
	"expeditious_retreat":      {"Expeditious Retreat", "Dash as a Bonus Action"},
	"feather_fall":             {"Feather Fall", "Falls slowly: no damage when it lands"},
	"flame_blade":              {"Flame Blade", "A fiery blade in hand: a melee spell attack as a Magic action (3d6 + modifier Fire)"},
	"fly":                      {"Fly", "Fly Speed 60 feet; it can hover"},
	"gaseous_form":             {"Gaseous Form", "A misty cloud: Fly Speed 10 feet, Resistance to B/P/S, no attacks or spells"},
	"globe_of_invulnerability": {"Globe of Invulnerability", "Spells of level 5 or lower from outside can't affect anything inside"},
	"jump_spell":               {"Jump", "Once each turn, a 30-foot jump for 10 feet of movement"},
	"magic_circle":             {"Magic Circle", "The chosen creature types can't enter; they attack targets inside with Disadvantage"},
	"polymorph":                {"Polymorph", "In a Beast form until its Temporary Hit Points are gone"},
	"regenerate":               {"Regenerate", "Regains 1 Hit Point at the start of each of its turns (1 hour)"},
	"spiritual_weapon":         {"Spiritual Weapon", "A spectral force: a melee spell attack as a Bonus Action (1d8 + modifier Force)"},
	"water_breathing":          {"Water Breathing", "Breathes underwater (24 hours)"},
}

// the effects that last without the caster's Concentration
var moreNoConcentration = []string{"blink", "darkvision", "ethereal", "feather_fall", "jump_spell", "magic_circle", "regenerate", "water_breathing"}

func init() {
	for k, v := range moreSpells {
		buffSpells[k] = v
	}
	for k, v := range moreLabels {
		buffLabels[k] = v
		spellEffectLabel[k] = v[0]
		spellEffectText[k] = v[1]
	}
	for _, k := range moreNoConcentration {
		noConcentration[k] = true
	}
}

// resolveMoreSpell applies one of these spells ("" for other spells).
func resolveMoreSpell(charID, lobby int, spell SRDSpell, key string, slot, saveDC int, description string) string {
	if key == "greater-restoration" {
		return greaterRestoration(charID, lobby, description, spellCharacterTargets(charID)) // F91 (cleric_fixes_2024.go)
	}
	if t := reviveSpell(charID, lobby, key, spell, spellCharacterTargets(charID)); t != "" {
		return t // Revivify, Raise Dead, Resurrection, True Resurrection (cleric_fixes_2024.go, F90)
	}
	b, ok := moreSpells[key]
	if !ok {
		return ""
	}
	dl := strings.ToLower(description)
	ids := []int{charID}
	if !b.Self {
		if t := spellCharacterTargets(charID); len(t) > 0 {
			ids = t
		} else if t := parseTargetFromDescription(description, charID); t > 0 {
			ids = []int{t}
		}
	}
	most := 1
	if b.Most != nil {
		most = b.Most(slot)
	}
	note := ""
	if len(ids) > most {
		note = fmt.Sprintf(" (only the first %d: the spell affects %d at this slot level)", most, most)
		ids = ids[:most]
	}
	_, monster, aimed := attackedMonster(charID)
	add := func(id int, detail string) {
		addSpellEffect(lobby, id, charID, spell.Name, b.Kind, detail, b.Rounds, "", 0)
	}
	var names []string
	for _, id := range ids {
		names = append(names, creatureName(lobby, id))
	}
	who := joinNames(names)
	var out string
	switch key {
	case "blink":
		add(charID, "")
		out = "For 1 minute, at the end of each of your turns the server rolls 1d6: on 4-6 you vanish to the Ethereal Plane until the start of your next turn (creatures on the Material Plane can't see, target or harm you, and you can't affect them)."
	case "expeditious-retreat":
		add(charID, "")
		speed := 0
		if canMove(charID) {
			speed = characterSpeedOf(charID)
		}
		if inActiveCombat(lobby) {
			db.Exec(`UPDATE characters SET movement_remaining = COALESCE(movement_remaining,0) + $1 WHERE id = $2`, speed, charID)
		}
		out = fmt.Sprintf("You take the Dash action (%d more feet of movement this turn), and until the spell ends you can Dash as a Bonus Action: {\"action\": \"dash\", \"description\": \"bonus action dash\"}.", speed)
	case "spiritual-weapon":
		x, y := -1, -1
		text := "Name a monster with target_id to attack it now; on your later turns: {\"action\": \"spiritual_weapon\", \"target_id\": -N}."
		if aimed {
			if m, onMap := activeEncounterMap(lobby); onMap {
				me, ok1 := m.token(charID)
				tg, ok2 := m.token(monster)
				if ok1 && ok2 {
					if d := tokenDistanceFeet(me, tg); d > 65 {
						add(charID, fmt.Sprintf("%d|-1|-1", slot))
						return fmt.Sprintf(" %s: the spectral force appears within 60 feet of you, but %s is %d ft away: no creature within 5 feet of it to attack. Move it and attack on a later turn as a Bonus Action ({\"action\": \"spiritual_weapon\", \"target_id\": -N}).", spell.Name, tg.Name, d)
					}
					x, y = tg.Position.X, tg.Position.Y
				}
			}
			text = spellMeleeAttack(charID, lobby, monster, fmt.Sprintf("%dd8", 1+maxInt(0, slot-2)), "force", "Spiritual Weapon") +
				" On your later turns, as a Bonus Action, move it up to 20 feet and attack again: {\"action\": \"spiritual_weapon\", \"target_id\": -N}."
		}
		add(charID, fmt.Sprintf("%d|%d|%d", slot, x, y))
		out = "A spectral force like a weapon appears within 60 feet. " + text
	case "flame-blade":
		held := "held"
		extra := ""
		if tableRule(lobby, "spell_hands") != "ignored" && handsOf(charID).free() == 0 {
			held, extra = "gone", " You have no free hand to hold it: free one, then evoke it as a Bonus Action ({\"action\": \"flame_blade\", \"description\": \"evoke\"})."
		}
		add(charID, fmt.Sprintf("%d|%s", slot, held))
		out = fmt.Sprintf("A fiery blade like a scimitar appears in your free hand (Bright Light 10 feet, Dim Light 10 feet more). Attack with it as a Magic action: {\"action\": \"flame_blade\", \"target_id\": -N} (a melee spell attack, %dd6 + your spellcasting modifier Fire). Letting go makes it vanish (\"let go\"); evoke it again as a Bonus Action.%s", 3+maxInt(0, slot-2), extra)
	case "fly":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("%s has a Fly Speed of 60 feet and can hover while you keep Concentration. On the battle map, add \"altitude\" (feet above the ground) to a move; when the spell ends a creature still aloft falls (1d6 Bludgeoning per 10 feet).", who)
	case "gaseous-form":
		for _, id := range ids {
			add(id, "")
			removeCondition(id, "prone")
		}
		out = fmt.Sprintf("%s becomes a misty cloud while you keep Concentration: Fly Speed 10 feet (its only movement) and hover, Resistance to Bludgeoning, Piercing and Slashing damage, Immunity to Prone, Advantage on Strength, Dexterity and Constitution saves; it can't talk, attack, cast spells or use objects. It ends at 0 Hit Points or with a Magic action: {\"action\": \"gaseous_form\", \"description\": \"end\"}.", who)
	case "enlarge-reduce":
		mode := "enlarge"
		if strings.Contains(dl, "reduce") || strings.Contains(dl, "shrink") {
			mode = "reduce"
		}
		target := ids[0]
		if aimed && len(spellCharacterTargets(charID)) == 0 {
			target = monster
		}
		tname := creatureName(lobby, target)
		if target < 0 || strings.Contains(dl, "unwilling") {
			if saved, total := creatureSave(lobby, target, "con", saveDC); saved {
				return fmt.Sprintf(" %s: %s succeeds on the DC %d Constitution save (%d): the spell has no effect.", spell.Name, tname, saveDC, total)
			}
		}
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'enlarge_reduce'`, lobby, target)
		size := resizeToken(lobby, target, map[string]int{"enlarge": 1, "reduce": -1}[mode])
		addSpellEffect(lobby, target, charID, spell.Name, b.Kind, fmt.Sprintf("%s|%d|%d", mode, lobby, size), b.Rounds, "", 0)
		if mode == "enlarge" {
			out = fmt.Sprintf("%s grows one size larger while you keep Concentration: Advantage on Strength checks and Strength saving throws, and its weapon and Unarmed Strike hits deal 1d4 more damage.", tname)
		} else {
			out = fmt.Sprintf("%s shrinks one size smaller while you keep Concentration: Disadvantage on Strength checks and Strength saving throws, and its weapon and Unarmed Strike hits deal 1d4 less damage (at least 1).", tname)
		}
	case "polymorph":
		target := ids[0]
		if aimed && len(spellCharacterTargets(charID)) == 0 {
			target = monster
		}
		return " " + spell.Name + ": " + polymorphCast(charID, lobby, target, spell, saveDC, dl) + note
	case "globe-of-invulnerability":
		max := 5 + maxInt(0, slot-6)
		x, y := -1, -1
		if m, onMap := activeEncounterMap(lobby); onMap {
			if me, ok := m.token(charID); ok {
				x, y = me.Position.X, me.Position.Y
			}
		}
		add(charID, fmt.Sprintf("%d|%d|%d", max, x, y))
		out = fmt.Sprintf("An immobile barrier fills a 10-foot Emanation around you while you keep Concentration: a spell of level %d or lower cast from outside it can't affect anything inside it (its areas leave the inside out).", max)
	case "antimagic-field":
		add(charID, "")
		out = "A 10-foot aura of antimagic surrounds you while you keep Concentration: no one inside can cast spells, take Magic actions or use magic items' properties; spells and magic from outside can't affect anything inside, and spell effects on creatures inside are suppressed (their time still runs). Dispel Magic doesn't end it."
	case "magic-circle":
		var types []string
		for _, t := range []string{"celestial", "elemental", "fey", "fiend", "undead"} {
			if strings.Contains(dl, t) {
				types = append(types, t)
			}
		}
		if len(types) == 0 {
			return " Magic Circle: name one or more creature types in the description (Celestials, Elementals, Fey, Fiends, Undead); none was chosen, so the circle has no effect."
		}
		reverse := strings.Contains(dl, "reverse") || strings.Contains(dl, "trap") || strings.Contains(dl, "keep in")
		x, y := -1, -1
		if m, onMap := activeEncounterMap(lobby); onMap {
			if me, ok := m.token(charID); ok {
				x, y = me.Position.X, me.Position.Y
			}
			if p := pointRe.FindStringSubmatch(dl); p != nil {
				x, y = atoiSigned(p[1]), atoiSigned(p[2])
			}
		}
		ids := ""
		if x < 0 {
			var parts []string
			for _, id := range append([]int{charID}, spellCharacterTargets(charID)...) {
				parts = append(parts, strconv.Itoa(id))
			}
			ids = strings.Join(parts, ",")
		}
		add(charID, fmt.Sprintf("%s|%v|%d|%d|%s", strings.Join(types, ","), reverse, x, y, ids))
		way := "can't willingly enter the Cylinder by moving, have Disadvantage on attack rolls against targets inside it, and can't Charm or Frighten (or possess) them"
		if reverse {
			way = "can't willingly leave the Cylinder by moving, have Disadvantage on attack rolls against targets outside it, and can't Charm or Frighten (or possess) them"
		}
		out = fmt.Sprintf("A 10-foot-radius, 20-foot-tall Cylinder for 1 hour: %s %s.", titleCase(strings.Join(types, ", ")), way)
		if x < 0 {
			out += " Without a battle map, the circle protects you and the characters you named (target_ids)."
		}
	case "aura-of-life":
		add(charID, "")
		out = "For 10 minutes (Concentration) you and your allies within 30 feet have Resistance to Necrotic damage and your Hit Point maximums can't be reduced; an ally at 0 Hit Points who starts their turn in the aura regains 1 Hit Point."
	case "divine-word":
		var targets []int
		if aimed {
			targets = append(targets, monster)
		}
		for _, id := range castMonsterTargets(charID) {
			if id != monster {
				targets = append(targets, id)
			}
		}
		for _, id := range spellCharacterTargets(charID) {
			if id != charID {
				targets = append(targets, id)
			}
		}
		return divineWord(charID, lobby, saveDC, targets) + note
	case "feather-fall":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("%s fall%s slowly (60 feet a round) for 1 minute and take no damage on landing (the GM's falling damage is 0 for them; the spell then ends for that creature).", who, map[bool]string{true: "s", false: ""}[len(ids) == 1])
	case "regenerate":
		target := ids[0]
		add(target, "")
		n := game.RollDamage("4d8", false) + 15
		heal := ""
		if target > 0 {
			nm, before, after := healCharacter(target, n)
			heal = fmt.Sprintf("%s regains %d Hit Points (%d -> %d)", nm, n, before, after)
		} else {
			heal = fmt.Sprintf("%s regains %d Hit Points (the GM updates the monster)", creatureName(lobby, target), n)
		}
		out = heal + ". For 1 hour it regains 1 Hit Point at the start of each of its turns, and severed body parts regrow after 2 minutes."
	case "darkvision":
		add(ids[0], "")
		out = fmt.Sprintf("For 8 hours %s has Darkvision with a range of 150 feet.", who)
	case "water-breathing":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("For 24 hours %s can breathe underwater (and still breathe air).", who)
	case "jump":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("For 1 minute, once on each of its turns, %s can jump up to 30 feet by spending 10 feet of movement ({\"action\": \"jump\"}).", who)
	}
	return " " + strings.TrimSpace(spell.Name+": "+out) + note
}

var pointRe = regexp.MustCompile(`(?:point|at)\s*\[?\s*(\d+)\s*,\s*(\d+)`)

// atoiSigned reads a whole number that may be negative (-1: no map position); atoiSafe drops the sign.
func atoiSigned(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// castMonsterTargets: the monsters a cast named with target_ids (negative ids).
func castMonsterTargets(charID int) []int {
	var out []int
	if v, ok := castTargets.Load(charID); ok {
		for _, id := range v.([]int) {
			if id < 0 {
				out = append(out, id)
			}
		}
	}
	return out
}

func inActiveCombat(lobby int) bool {
	var active bool
	db.QueryRow(`SELECT COALESCE(active,false) FROM combat_state WHERE lobby_id = $1`, lobby).Scan(&active)
	return active
}

func characterSpeedOf(charID int) int {
	var species string
	db.QueryRow(`SELECT COALESCE(species,'') FROM characters WHERE id = $1`, charID).Scan(&species)
	return characterSpeed(charID, species)
}

// spellMeleeAttack: a melee spell attack against a monster (Spiritual Weapon, Flame Blade).
func spellMeleeAttack(charID, lobby, monster int, dice, dtype, what string) string {
	var ac int
	var tname string
	for _, e := range combatEntries(lobby) {
		if asInt(e["id"]) == monster {
			ac, tname = asInt(e["ac"]), fmt.Sprint(e["name"])
		}
	}
	if tname == "" {
		return what + ": that creature isn't in the fight."
	}
	if antimagicCovers(lobby, monster) || antimagicCovers(lobby, charID) {
		return fmt.Sprintf("%s: %s is inside an Antimagic Field: the magic has no effect.", what, tname)
	}
	var level int
	db.QueryRow(`SELECT level FROM characters WHERE id = $1`, charID).Scan(&level)
	mod := game.Modifier(spellModScore(charID))
	bonus := game.ProficiencyBonus(level) + mod + magicSpellAttackBonus(charID)
	ac += spellACAdjust(lobby, monster)
	nat := game.RollDie(20)
	if outlined(lobby, monster) {
		if b := game.RollDie(20); b > nat {
			nat = b
		}
	}
	adj, _ := spellRollAdjust(lobby, charID, "attack")
	total := nat + bonus + adj
	if nat == 1 || (nat != 20 && total < ac) {
		return fmt.Sprintf("%s: melee spell attack %d vs AC %d - MISS.", what, total, ac)
	}
	crit := nat == 20
	dmg := game.RollDamage(dice, crit) + mod
	if dmg < 0 {
		dmg = 0
	}
	res, _ := damageMonster(lobby, monster, dmg, dtype)
	return fmt.Sprintf("%s: melee spell attack %d vs AC %d - %s! %s takes %d %s damage. %s", what, total, ac,
		map[bool]string{true: "CRITICAL HIT", false: "HIT"}[crit], tname, dmg, titleCase(dtype), strings.TrimSpace(res))
}

// moreSpellEconomy: the action economy of the spells' own actions ("" for the others).
func moreSpellEconomy(charID int, action, description string) string {
	d := strings.ToLower(description)
	switch action {
	case "spiritual_weapon":
		return "item_bonus" // a Bonus Action
	case "flame_blade":
		switch {
		case strings.Contains(d, "let go") || strings.Contains(d, "drop") || strings.Contains(d, "dismiss"):
			return "drop" // letting go is free
		case strings.Contains(d, "evoke"):
			return "item_bonus"
		}
		return "use_item" // the attack is a Magic action
	case "gaseous_form":
		return "use_item" // ending it is a Magic action
	case "dash":
		if _, ok := charBuff(charID, "expeditious_retreat"); ok {
			var used bool
			db.QueryRow(`SELECT COALESCE(action_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
			if used || strings.Contains(d, "bonus") || strings.Contains(d, "expeditious") {
				return "item_bonus"
			}
		}
	}
	return ""
}

// moreActionBlock: what a character can't do now (Gaseous Form, inside an Antimagic Field); "" when it can.
func moreActionBlock(charID int, action string) string {
	lobby := lobbyOfChar(charID)
	if _, ok := charBuff(charID, "gaseous_form"); ok {
		switch action {
		case "attack", "offhand_attack", "opportunity_attack", "cast", "bonus_cast", "use_item", "grapple", "shove", "frenzy_attack",
			"martial_arts", "flurry_of_blows", "spiritual_weapon", "flame_blade", "drink_potion", "help":
			return "You are a misty cloud (Gaseous Form): you can't attack, cast spells or use objects. End the form with a Magic action: {\"action\": \"gaseous_form\", \"description\": \"end\"}. Nothing was spent."
		}
	}
	if antimagicCovers(lobby, charID) {
		switch action {
		case "cast", "bonus_cast", "spiritual_weapon", "flame_blade":
			return "You are inside an Antimagic Field: no one can cast spells or take Magic actions there. Nothing was spent."
		case "use_item":
			return "You are inside an Antimagic Field: magic items' properties don't work there (a mundane item works: use it with \"interact\"). Nothing was spent."
		}
	}
	return ""
}

// moreSpellAction resolves the spells' own actions.
func moreSpellAction(charID int, action, description string, targetID int) string {
	lobby := lobbyOfChar(charID)
	d := strings.ToLower(description)
	switch action {
	case "spiritual_weapon":
		e, ok := charBuff(charID, "spiritual_weapon")
		if !ok {
			return "Cannot use the Spiritual Weapon: you have none (cast Spiritual Weapon; it lasts while you keep Concentration). Nothing was spent."
		}
		if targetID >= 0 {
			return "Cannot use the Spiritual Weapon: name the monster with target_id (a negative id). Nothing was spent."
		}
		parts := strings.Split(e.Detail, "|")
		slot := atoiSafe(parts[0])
		if m, onMap := activeEncounterMap(lobby); onMap && len(parts) == 3 && atoiSigned(parts[1]) >= 0 {
			force := mapToken{Position: mapSquare{X: atoiSigned(parts[1]), Y: atoiSigned(parts[2])}, Size: 1}
			if t, ok := m.token(targetID); ok {
				if dist := tokenDistanceFeet(force, t); dist > 25 {
					return fmt.Sprintf("Cannot use the Spiritual Weapon: %s is %d ft from the spectral force, which moves 20 feet and then reaches 5 feet. Nothing was spent.", t.Name, dist)
				}
			}
		} else if onMap {
			me, ok1 := m.token(charID)
			if t, ok2 := m.token(targetID); ok1 && ok2 && tokenDistanceFeet(me, t) > 65 {
				return fmt.Sprintf("Cannot use the Spiritual Weapon: %s is beyond 60 feet of you, where the force can't go. Nothing was spent.", t.Name)
			}
		}
		text := spellMeleeAttack(charID, lobby, targetID, fmt.Sprintf("%dd8", 1+maxInt(0, slot-2)), "force", "Spiritual Weapon")
		if m, onMap := activeEncounterMap(lobby); onMap {
			if t, ok := m.token(targetID); ok {
				db.Exec(`UPDATE spell_effects SET detail = $1 WHERE id = $2`, fmt.Sprintf("%d|%d|%d", slot, t.Position.X, t.Position.Y), e.ID)
			}
		}
		return "The spectral force moves and strikes (Bonus Action). " + text
	case "flame_blade":
		e, ok := charBuff(charID, "flame_blade")
		if !ok {
			return "Cannot use the Flame Blade: you have none (cast Flame Blade; it lasts while you keep Concentration). Nothing was spent."
		}
		parts := strings.Split(e.Detail, "|")
		slot := atoiSafe(parts[0])
		held := len(parts) > 1 && parts[1] == "held"
		switch {
		case strings.Contains(d, "let go") || strings.Contains(d, "drop") || strings.Contains(d, "dismiss"):
			db.Exec(`UPDATE spell_effects SET detail = $1 WHERE id = $2`, fmt.Sprintf("%d|gone", slot), e.ID)
			return "You let go of the Flame Blade: it vanishes (the spell goes on; evoke it again as a Bonus Action)."
		case strings.Contains(d, "evoke"):
			if held {
				return "Cannot evoke the Flame Blade: it is already in your hand. Nothing was spent."
			}
			if tableRule(lobby, "spell_hands") != "ignored" && handsOf(charID).free() == 0 {
				return "Cannot evoke the Flame Blade: it appears in a free hand, and yours are full (unequip a weapon or the Shield first). Nothing was spent."
			}
			db.Exec(`UPDATE spell_effects SET detail = $1 WHERE id = $2`, fmt.Sprintf("%d|held", slot), e.ID)
			return "You evoke the Flame Blade in your free hand again (Bonus Action)."
		}
		if !held {
			return "Cannot attack with the Flame Blade: it isn't in your hand (evoke it as a Bonus Action: {\"action\": \"flame_blade\", \"description\": \"evoke\"}). Nothing was spent."
		}
		if targetID >= 0 {
			return "Cannot attack with the Flame Blade: name the monster with target_id (a negative id). Nothing was spent."
		}
		if m, onMap := activeEncounterMap(lobby); onMap {
			me, ok1 := m.token(charID)
			if t, ok2 := m.token(targetID); ok1 && ok2 && tokenDistanceFeet(me, t) > 5 {
				return fmt.Sprintf("Cannot attack with the Flame Blade: %s is %d ft away, beyond your 5-foot reach. Move closer first. Nothing was spent.", t.Name, tokenDistanceFeet(me, t))
			}
		}
		return "Flame Blade (Magic action). " + spellMeleeAttack(charID, lobby, targetID, fmt.Sprintf("%dd6", 3+maxInt(0, slot-2)), "fire", "Flame Blade")
	case "gaseous_form":
		if _, ok := charBuff(charID, "gaseous_form"); !ok {
			return "Cannot end Gaseous Form: you aren't in it. Nothing was spent."
		}
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'gaseous_form'`, lobby, charID)
		return "You end Gaseous Form on yourself (a Magic action) and take your normal form."
	}
	return ""
}

func flameBladeHeld(charID int) bool {
	e, ok := charBuff(charID, "flame_blade")
	return ok && strings.HasSuffix(e.Detail, "|held")
}

// ---------------------------------------------------------------- Enlarge/Reduce

func enlargeMode(lobby, id int) string {
	if e, ok := buffEffect(lobby, id, "enlarge_reduce"); ok {
		return strings.SplitN(e.Detail, "|", 2)[0]
	}
	return ""
}

var sizeCategories = []string{"tiny", "small", "medium", "large", "huge", "gargantuan"}

func sizeSquares(size string) int {
	switch strings.ToLower(size) {
	case "large":
		return 2
	case "huge":
		return 3
	case "gargantuan":
		return 4
	}
	return 1
}

// creatureSize: the size category of a character (Small for Halflings and Gnomes) or a monster in the fight.
func creatureSize(lobby, id int) string {
	if id < 0 {
		var size string
		db.QueryRow(`SELECT COALESCE(size,'medium') FROM monsters WHERE slug = $1`, monsterKeyOfCombatant(lobby, id)).Scan(&size)
		return strings.ToLower(size)
	}
	var species string
	db.QueryRow(`SELECT lower(COALESCE(species,'')) FROM characters WHERE id = $1`, id).Scan(&species)
	if strings.Contains(species, "halfling") || strings.Contains(species, "gnome") {
		return "small"
	}
	return "medium"
}

// resizeToken changes a creature's size by steps categories (its token on the battle map too) and returns the new map size.
func resizeToken(lobby, id, steps int) int {
	cur := creatureSize(lobby, id)
	i := 2
	for k, s := range sizeCategories {
		if s == cur {
			i = k
		}
	}
	i += steps
	if i < 0 {
		i = 0
	}
	if i >= len(sizeCategories) {
		i = len(sizeCategories) - 1
	}
	n := sizeSquares(sizeCategories[i])
	setTokenSize(lobby, id, n)
	return n
}

func setTokenSize(lobby, id, n int) {
	m, ok := activeEncounterMap(lobby)
	if !ok {
		return
	}
	for i, t := range m.Tokens {
		if t.ID == id && t.Size != n {
			m.Tokens[i].Size = n
			m.Revision = mapRevision()
			b, _ := json.Marshal(m)
			db.Exec(`UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2 AND active = true`, string(b), lobby)
		}
	}
}

// enlargeHit: the 1d4 more (Enlarge) or less (Reduce, not below 1) on a weapon or Unarmed Strike hit by a creature.
func enlargeHit(lobby, attackerID, dmg int) (int, string) {
	switch enlargeMode(lobby, attackerID) {
	case "enlarge":
		n := game.RollDie(4)
		return n, fmt.Sprintf(" Enlarge: +%d damage.", n)
	case "reduce":
		n := minInt(game.RollDie(4), maxInt(0, dmg-1))
		if n <= 0 {
			return 0, ""
		}
		return -n, fmt.Sprintf(" Reduce: -%d damage.", n)
	}
	return 0, ""
}

// ---------------------------------------------------------------- saves, checks, damage, conditions, attacks, Speed, senses

func moreSaveAdvantage(lobby, id int, ability string) (bool, string) {
	ab := shortAbility(ability)
	if ab == "str" && raging(id) {
		return true, "Rage" // F100
	}
	if _, ok := buffEffect(lobby, id, "gaseous_form"); ok && (ab == "str" || ab == "dex" || ab == "con") {
		return true, "Gaseous Form"
	}
	if ab == "str" && enlargeMode(lobby, id) == "enlarge" {
		return true, "Enlarge"
	}
	return false, ""
}

func moreSaveDisadvantage(lobby, id int, ability string) bool {
	if v, ok := typeSaveDisadvantage.Load(lobby); ok && v.(int) == id { // Shatter against a Construct (F192, spell_resolution.go)
		return true
	}
	return shortAbility(ability) == "str" && enlargeMode(lobby, id) == "reduce"
}

func moreCheckAdvantage(charID int, ability string) (bool, string) {
	if shortAbility(ability) == "str" && raging(charID) {
		return true, "Rage" // F100
	}
	if shortAbility(ability) == "str" && enlargeMode(lobbyOfChar(charID), charID) == "enlarge" {
		return true, "Enlarge"
	}
	return false, ""
}

func moreCheckDisadvantage(charID int, ability string) (bool, string) {
	if shortAbility(ability) == "str" && enlargeMode(lobbyOfChar(charID), charID) == "reduce" {
		return true, "Reduce"
	}
	return false, ""
}

// moreDamageResist: Gaseous Form's Resistance to Bludgeoning, Piercing and Slashing damage.
func moreDamageResist(charID int, dtype string) (bool, string) {
	if ok, why := heldResistance(charID, dtype); ok {
		return true, why // Staff of Fire, Frost Brand, the Helm of Brilliance's rubies (F151, F152)
	}
	if broochOfShielding(charID, dtype) {
		return true, "Brooch of Shielding" // F141 (paladin_fixes_2024.go)
	}
	if rageResists(charID, dtype) {
		return true, "Rage" // F100 (barbarian_fixes_2024.go)
	}
	if strings.ToLower(dtype) == "necrotic" && auraOfLifeCovers(lobbyOfChar(charID), charID) {
		return true, "Aura of Life" // F88 (cleric_fixes_2024.go)
	}
	switch strings.ToLower(dtype) {
	case "bludgeoning", "piercing", "slashing":
		if _, ok := charBuff(charID, "gaseous_form"); ok {
			return true, "Gaseous Form"
		}
	}
	return false, ""
}

// moreConditionImmune: Gaseous Form (Prone); Magic Circle (Charmed, Frightened from the chosen types).
func moreConditionImmune(lobby, id int, cond string, sourceID int) (bool, string) {
	c := strings.ToLower(cond)
	if c == "poisoned" && id > 0 && periaptProofAgainstPoison(id) {
		return true, "Periapt of Proof against Poison" // F162 (items_c5_2024.go)
	}
	if c == "prone" {
		if _, ok := buffEffect(lobby, id, "gaseous_form"); ok {
			return true, "Gaseous Form"
		}
	}
	if (c == "charmed" || c == "frightened") && sourceID < 0 {
		for _, mc := range magicCircles(lobby) {
			if mc.types[monsterBaseType(lobby, sourceID)] && mc.protects(lobby, id) {
				return true, "Magic Circle"
			}
		}
	}
	return false, ""
}

// moreAttackDisadvantage: Magic Circle: a creature of a chosen type attacks a protected target with Disadvantage.
func moreAttackDisadvantage(lobby, monsterID, targetID int) (bool, string) {
	if monsterID >= 0 {
		return false, ""
	}
	for _, mc := range magicCircles(lobby) {
		if mc.types[monsterBaseType(lobby, monsterID)] && mc.protects(lobby, targetID) {
			return true, "Magic Circle"
		}
	}
	return false, ""
}

// moreSpeed: Gaseous Form's Fly Speed of 10 feet is the only movement; Fly gives a Fly Speed of 60 feet.
func moreSpeed(charID, speed int) int {
	speed = bootsSpeed(charID, speed) // Boots of Speed (monk_fixes_2024.go, F107)
	if _, ok := charBuff(charID, "gaseous_form"); ok {
		return 10
	}
	if _, ok := charBuff(charID, "fly"); ok && speed < 60 {
		return 60
	}
	return speed
}

// canFlyNow: the creature has a Fly Speed from a spell (Fly, Gaseous Form) or a beast form that flies.
func canFlyNow(lobby, id int) bool {
	if _, ok := buffEffect(lobby, id, "fly"); ok {
		return true
	}
	if _, ok := buffEffect(lobby, id, "gaseous_form"); ok {
		return true
	}
	if _, ok := buffEffect(lobby, id, "winged_flight"); ok {
		return true // Winged Boots (F163)
	}
	if id > 0 {
		var speed string
		db.QueryRow(`SELECT COALESCE(m.speed_text,'') FROM characters c JOIN monsters m ON m.slug = c.wild_shape_form WHERE c.id = $1`, id).Scan(&speed)
		return strings.Contains(strings.ToLower(speed), "fly")
	}
	return false
}

func darkvisionBuff(charID int) int {
	if _, ok := charBuff(charID, "darkvision"); ok {
		return 150
	}
	return 0
}

func waterBreathing(charID int) bool {
	_, ok := charBuff(charID, "water_breathing")
	return ok
}

// featherFallLands: Feather Fall protects a falling creature: no damage, and the spell ends for it.
func featherFallLands(charID int) (bool, string) {
	e, ok := charBuff(charID, "feather_fall")
	if !ok {
		return false, ""
	}
	db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
	return true, "Feather Fall: " + creatureName(lobbyOfChar(charID), charID) + " drifts down at 60 feet a round and lands without damage (the spell ends for them)."
}

// fallCharacter: a character falls (1d6 Bludgeoning per 10 feet, at most 20d6) and lands Prone, unless Feather Fall holds it.
func fallCharacter(charID, feet int, reason string) string {
	name := creatureName(lobbyOfChar(charID), charID)
	if ok, note := featherFallLands(charID); ok {
		return note
	}
	dice := minInt(20, feet/10)
	if dice <= 0 {
		return ""
	}
	dmg := game.RollDamage(fmt.Sprintf("%dd6", dice), false)
	text := fmt.Sprintf("%s falls %d feet (%s): %s", name, feet, reason, damageCharacter(charID, dmg, "bludgeoning"))
	addMonsterCondition(lobbyOfChar(charID), charID, "prone", "a fall", 0, "", 0)
	return text + " and lands Prone."
}

var jumpUsed sync.Map // charID -> the combat turn marker of the turn the Jump spell was used

// jumpSpellJump: the Jump spell's 30-foot jump for 10 feet of movement, once on each of the creature's turns.
func jumpSpellJump(charID int) (bool, string) {
	if _, ok := charBuff(charID, "jump_spell"); !ok {
		return false, ""
	}
	lobby := lobbyOfChar(charID)
	marker := combatTurnMarker(lobby)
	if marker != "" {
		if v, ok := jumpUsed.Load(charID); ok && v.(string) == marker {
			return false, " (the Jump spell's jump was used this turn)"
		}
		jumpUsed.Store(charID, marker)
	}
	return true, ""
}

// ---------------------------------------------------------------- Polymorph

func polymorphCast(charID, lobby, target int, spell SRDSpell, saveDC int, dl string) string {
	tname := creatureName(lobby, target)
	limit := 0.0
	if target < 0 {
		var cr string
		db.QueryRow(`SELECT COALESCE(cr,'0') FROM monsters WHERE slug = $1`, monsterKeyOfCombatant(lobby, target)).Scan(&cr)
		limit = crValue(cr)
		if strings.EqualFold(monsterTypeOf(lobby, target), "shapechanger") {
			return tname + " is a Shapechanger: the spell has no effect."
		}
	} else {
		var lvl int
		db.QueryRow(`SELECT level FROM characters WHERE id = $1`, target).Scan(&lvl)
		limit = float64(lvl)
	}
	slug, beast, bcr, bhp, bac := "", "", "", 0, 10
	rows, err := db.Query(`SELECT slug, name, COALESCE(cr,'0'), hp, ac FROM monsters WHERE lower(type) LIKE 'beast%' ORDER BY length(name) DESC`)
	if err == nil {
		for rows.Next() {
			var s, n, c string
			var h, a int
			if rows.Scan(&s, &n, &c, &h, &a) == nil && strings.Contains(dl, strings.ToLower(n)) && crValue(c) <= limit {
				slug, beast, bcr, bhp, bac = s, n, c, h, a
				break
			}
		}
		rows.Close()
	}
	if slug == "" {
		return fmt.Sprintf("name the Beast in the description (any Beast of Challenge Rating %s or lower, such as \"into a Giant Ape\"); none was named, so the spell has no effect.", strconv.FormatFloat(limit, 'f', -1, 64))
	}
	if saved, total := creatureSave(lobby, target, "wis", saveDC); saved {
		return fmt.Sprintf("%s succeeds on the DC %d Wisdom save (%d): the spell has no effect.", tname, saveDC, total)
	}
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'polymorph'`, lobby, target)
	if target < 0 {
		var orig map[string]interface{}
		editMonsterEntry(lobby, target, func(e map[string]interface{}) {
			orig = map[string]interface{}{"key": e["monster_key"], "hp": e["hp"], "ac": e["ac"], "max_hp": e["max_hp"]}
			e["monster_key"], e["hp"], e["ac"] = slug, bhp, bac
			if _, ok := e["max_hp"]; ok {
				e["max_hp"] = bhp
			}
		})
		if orig == nil {
			return tname + " isn't in the fight."
		}
		ob, _ := json.Marshal(orig)
		addSpellEffect(lobby, target, charID, spell.Name, "polymorph", fmt.Sprintf("m|%d|%s", lobby, ob), 600, "", 0)
		setTokenSize(lobby, target, sizeSquares(creatureSizeOfKey(slug)))
		return fmt.Sprintf("%s fails the DC %d Wisdom save and becomes a %s (CR %s) while you keep Concentration: its statistics are the beast's (AC %d, %d Hit Points as Temporary Hit Points; its own Hit Points are kept). It can't speak or cast spells; its gear melds into the form. When the beast's Hit Points are gone the spell ends and extra damage carries over.", tname, saveDC, beast, bcr, bac, bhp)
	}
	ensureWildShapeColumns()
	leaveWildShape(target, "Polymorph")
	db.Exec(`UPDATE characters SET wild_shape_form = $1 WHERE id = $2`, slug, target)
	enterWildShapeStats(target, slug, "equipment: merge")
	db.Exec(`UPDATE characters SET wild_shape_rounds_left = 600 WHERE id = $1`, target)
	got := grantTempHP(target, bhp)
	addSpellEffect(lobby, target, charID, spell.Name, "polymorph", "c|"+slug, 600, "", 0)
	return fmt.Sprintf("%s fails the DC %d Wisdom save and becomes a %s (CR %s) while you keep Concentration: the beast's statistics (attack with \"attack\" as in Wild Shape), %d Temporary Hit Points (the spell ends when they are gone; they vanish when it ends), no speech or spells, gear melded into the form.", tname, saveDC, beast, bcr, got)
}

func creatureSizeOfKey(slug string) string {
	var size string
	db.QueryRow(`SELECT COALESCE(size,'medium') FROM monsters WHERE slug = $1`, slug).Scan(&size)
	return size
}

// editMonsterEntry changes a monster's entry in the turn order.
func editMonsterEntry(lobby, id int, f func(e map[string]interface{})) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	var raw []byte
	if tx.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1 AND active = true FOR UPDATE`, lobby).Scan(&raw) != nil {
		return
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	for i, e := range entries {
		if asInt(e["id"]) == id {
			f(e)
			entries[i] = e
		}
	}
	b, _ := json.Marshal(entries)
	if _, err := tx.Exec(`UPDATE combat_state SET turn_order = $1 WHERE lobby_id = $2`, b, lobby); err == nil {
		tx.Commit()
	}
}

// polymorphRevert restores a polymorphed monster's own statistics in its turn order entry e; overflow is the damage beyond the
// beast's Hit Points (it carries over). It returns the monster's Hit Points now.
func polymorphRevert(e map[string]interface{}, detail string, overflow int) int {
	parts := strings.SplitN(detail, "|", 3)
	if len(parts) < 3 {
		return asInt(e["hp"])
	}
	var orig map[string]interface{}
	json.Unmarshal([]byte(parts[2]), &orig)
	e["monster_key"], e["ac"] = orig["key"], orig["ac"]
	if orig["max_hp"] != nil {
		e["max_hp"] = orig["max_hp"]
	}
	hp := asInt(orig["hp"]) - overflow
	if hp < 0 {
		hp = 0
	}
	e["hp"] = hp
	return hp
}

// polymorphDrop: a polymorphed monster's beast form at 0 Hit Points: the spell ends and the extra damage carries over to the monster's
// own Hit Points. hp is the beast's Hit Points after the damage (negative: the extra). It returns the Hit Points to store and a note.
func polymorphDrop(lobby int, e map[string]interface{}, hp int) (int, string) {
	if hp > 0 {
		return hp, ""
	}
	id := asInt(e["id"])
	pe, ok := buffEffect(lobby, id, "polymorph")
	if !ok || !strings.HasPrefix(pe.Detail, "m|") {
		return hp, ""
	}
	db.Exec(`DELETE FROM spell_effects WHERE id = $1`, pe.ID)
	newHP := polymorphRevert(e, pe.Detail, -hp)
	return newHP, fmt.Sprintf(" The beast form drops to 0 Hit Points: the Polymorph ends and %s returns to its true form with %d Hit Points.", e["name"], newHP)
}

// polymorphTempGone: a polymorphed character with no Temporary Hit Points left: the spell ends.
func polymorphTempGone(charID int) string {
	e, ok := charBuff(charID, "polymorph")
	if !ok {
		return ""
	}
	var temp int
	db.QueryRow(`SELECT COALESCE(temp_hp,0) FROM characters WHERE id = $1`, charID).Scan(&temp)
	if temp > 0 {
		return ""
	}
	db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
	leaveWildShape(charID, "Polymorph ends: no Temporary Hit Points left")
	return "The Polymorph ends: no Temporary Hit Points are left, and the character takes their true form."
}

// ---------------------------------------------------------------- Globe of Invulnerability, Antimagic Field, Magic Circle

type areaSpell struct {
	caster, max, x, y int
	ids               map[int]bool
}

// lobbySpellRows: the active effects of a kind in a campaign (the caster's Concentration still holds).
func lobbySpellRows(lobby int, kind string) []activeSpellEffect {
	if db == nil || lobby == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT id, target_id, caster_id, spell, detail FROM spell_effects WHERE lobby_id = $1 AND kind = $2`, lobby, kind)
	if err != nil {
		return nil
	}
	var out []activeSpellEffect
	for rows.Next() {
		var e activeSpellEffect
		if rows.Scan(&e.ID, &e.TargetID, &e.CasterID, &e.Spell, &e.Detail) == nil {
			e.Kind = kind
			out = append(out, e)
		}
	}
	rows.Close()
	var held []activeSpellEffect
	for _, e := range out {
		if noConcentration[kind] || concentrationHolds(e.CasterID, e.Spell) {
			held = append(held, e)
		}
	}
	return held
}

func centerToken(x, y int) mapToken { return mapToken{Position: mapSquare{X: x, Y: y}, Size: 1} }

// antimagicCovers: the creature is inside an Antimagic Field (10 feet around its caster, who moves it).
func antimagicCovers(lobby, id int) bool {
	fields := lobbySpellRows(lobby, "antimagic_field")
	if len(fields) == 0 {
		return false
	}
	m, onMap := activeEncounterMap(lobby)
	for _, f := range fields {
		if f.CasterID == id {
			return true
		}
		if !onMap {
			continue
		}
		c, ok1 := m.token(f.CasterID)
		t, ok2 := m.token(id)
		if ok1 && ok2 && tokenDistanceFeet(c, t) <= 10 {
			return true
		}
	}
	return false
}

// globeShields: a spell of the level, cast by the source creature, can't affect the target inside a Globe of Invulnerability.
func globeShields(lobby, targetID, sourceID, level int) bool {
	m, onMap := activeEncounterMap(lobby)
	for _, g := range lobbySpellRows(lobby, "globe_of_invulnerability") {
		p := strings.Split(g.Detail, "|")
		if len(p) < 3 || level > atoiSafe(p[0]) {
			continue
		}
		x, y := atoiSigned(p[1]), atoiSigned(p[2])
		if x < 0 || !onMap {
			if targetID == g.CasterID {
				return true // without a map: the caster is inside it
			}
			continue
		}
		t, ok := m.token(targetID)
		if !ok || tokenDistanceFeet(centerToken(x, y), t) > 10 {
			continue
		}
		if s, ok := m.token(sourceID); ok && tokenDistanceFeet(centerToken(x, y), s) <= 10 {
			continue // cast from inside the barrier
		}
		return true
	}
	return false
}

// monsterCastBlocked: a monster inside an Antimagic Field can't cast spells.
func monsterCastBlocked(lobby, monsterID int) string {
	if antimagicCovers(lobby, monsterID) {
		return "it is inside an Antimagic Field, where no one can cast spells: nothing happens"
	}
	return ""
}

// magicShieldedTargets: the targets a monster's spell (of the level) can still affect; the others are noted.
func magicShieldedTargets(lobby, monsterID, level int, targets []int) ([]int, []string) {
	var kept []int
	var notes []string
	for _, t := range targets {
		switch {
		case antimagicCovers(lobby, t):
			notes = append(notes, creatureName(lobby, t)+" is inside an Antimagic Field: the spell has no effect on them.")
		case globeShields(lobby, t, monsterID, level):
			notes = append(notes, creatureName(lobby, t)+" is inside a Globe of Invulnerability: the spell has no effect on them.")
		default:
			kept = append(kept, t)
		}
	}
	return kept, notes
}

type magicCircle struct {
	types      map[string]bool
	reverse    bool
	x, y       int
	ids        map[int]bool
	casterID   int
	hasCenter  bool
	effectSpan int
}

func magicCircles(lobby int) []magicCircle {
	var out []magicCircle
	for _, e := range lobbySpellRows(lobby, "magic_circle") {
		p := strings.Split(e.Detail, "|")
		if len(p) < 5 {
			continue
		}
		mc := magicCircle{types: map[string]bool{}, reverse: p[1] == "true", x: atoiSigned(p[2]), y: atoiSigned(p[3]), ids: map[int]bool{}, casterID: e.CasterID}
		for _, t := range strings.Split(p[0], ",") {
			mc.types[t] = true
		}
		for _, s := range strings.Split(p[4], ",") {
			if n := atoiSafe(s); n != 0 {
				mc.ids[n] = true
			}
		}
		mc.hasCenter = mc.x >= 0
		out = append(out, mc)
	}
	return out
}

// inside: the creature's token is within the Cylinder (10-foot radius around the center square).
func (mc magicCircle) inside(lobby, id int) bool {
	if !mc.hasCenter {
		return mc.ids[id]
	}
	m, ok := activeEncounterMap(lobby)
	if !ok {
		return false
	}
	t, ok := m.token(id)
	return ok && tokenDistanceFeet(centerToken(mc.x, mc.y), t) <= 10
}

// protects: a target the circle protects (inside it; outside it for a reversed circle).
func (mc magicCircle) protects(lobby, id int) bool {
	if mc.reverse {
		return !mc.inside(lobby, id)
	}
	return mc.inside(lobby, id)
}

// monsterBaseType: "fiend", "undead" ... (the first word of the monster's type, without an s).
func monsterBaseType(lobby, id int) string {
	t := strings.ToLower(strings.TrimSpace(monsterTypeOf(lobby, id)))
	if i := strings.IndexAny(t, " ("); i > 0 {
		t = t[:i]
	}
	return strings.TrimSuffix(t, "s")
}

// magicCircleHolds keeps the chosen creatures from crossing a Magic Circle by their own movement (a GM map update): such a token goes
// back to where it stood. It returns notes for the feed.
func magicCircleHolds(lobby int, before encounterMap, after *encounterMap) []string {
	circles := magicCircles(lobby)
	if len(circles) == 0 {
		return nil
	}
	var notes []string
	for i, t := range after.Tokens {
		if t.ID >= 0 {
			continue
		}
		old, ok := before.token(t.ID)
		if !ok || old.Position == t.Position {
			continue
		}
		typ := monsterBaseType(lobby, t.ID)
		for _, mc := range circles {
			if !mc.types[typ] || !mc.hasCenter {
				continue
			}
			c := centerToken(mc.x, mc.y)
			wasIn, nowIn := tokenDistanceFeet(c, old) <= 10, tokenDistanceFeet(c, t) <= 10
			if (!mc.reverse && !wasIn && nowIn) || (mc.reverse && wasIn && !nowIn) {
				after.Tokens[i].Position = old.Position
				way := "enter"
				if mc.reverse {
					way = "leave"
				}
				notes = append(notes, fmt.Sprintf("Magic Circle: %s (%s) can't willingly %s the circle by moving; it stays where it was.", t.Name, typ, way))
				break
			}
		}
	}
	return notes
}

// ---------------------------------------------------------------- turns, endings

// moreTurnHooks: the end of one creature's turn and the start of the next one's (combat next and skip).
func moreTurnHooks(lobby, endedID, nextID int) []string {
	var notes []string
	if nextID > 0 {
		displacementDown.Delete(nextID) // the Cloak of Displacement works again (F113)
	}
	// Blink: 1d6 at the end of the caster's turn
	if endedID > 0 {
		if _, ok := buffEffect(lobby, endedID, "blink"); ok {
			roll := game.RollDie(6)
			if roll >= 4 {
				addSpellEffect(lobby, endedID, endedID, "Blink", "ethereal", "blink", 4800, "", 0)
				notes = append(notes, fmt.Sprintf("Blink: %s rolls %d and vanishes to the Ethereal Plane until the start of their next turn.", creatureName(lobby, endedID), roll))
			} else {
				notes = append(notes, fmt.Sprintf("Blink: %s rolls %d and stays.", creatureName(lobby, endedID), roll))
			}
		}
	}
	if nextID > 0 {
		// Blink: back at the start of the turn
		if n, _ := db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'ethereal' AND detail = 'blink'`, lobby, nextID); n != nil {
			if c, _ := n.RowsAffected(); c > 0 {
				notes = append(notes, fmt.Sprintf("Blink: %s returns from the Ethereal Plane to a space within 10 feet of where they vanished.", creatureName(lobby, nextID)))
			}
		}
		if n := auraOfLifeRevives(lobby, nextID); n != "" { // Aura of Life (cleric_fixes_2024.go)
			notes = append(notes, n)
		}
		// Regenerate: 1 Hit Point at the start of each of its turns
		if _, ok := buffEffect(lobby, nextID, "regenerate"); ok {
			var hp, max int
			var dead bool
			db.QueryRow(`SELECT hp, max_hp, COALESCE(is_dead,false) FROM characters WHERE id = $1`, nextID).Scan(&hp, &max, &dead)
			if !dead && hp < max {
				nm, before, after := healCharacter(nextID, 1)
				notes = append(notes, fmt.Sprintf("Regenerate: %s regains 1 Hit Point (%d -> %d).", nm, before, after))
			}
		}
	}
	notes = append(notes, moreSweepLapsed(lobby)...)
	return notes
}

// moreSweepLapsed ends the spells whose caster lost Concentration and that changed something (a beast form, a size, a flight).
func moreSweepLapsed(lobby int) []string {
	rows, err := db.Query(`SELECT id, target_id, caster_id, spell, kind, detail FROM spell_effects WHERE lobby_id = $1 AND kind IN ('polymorph','enlarge_reduce','fly','gaseous_form','flame_blade','spiritual_weapon')`, lobby)
	if err != nil {
		return nil
	}
	type row struct {
		id, target, caster  int
		spell, kind, detail string
	}
	var lapsed []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.target, &r.caster, &r.spell, &r.kind, &r.detail) == nil && !concentrationHolds(r.caster, r.spell) {
			lapsed = append(lapsed, r)
		}
	}
	rows.Close()
	var notes []string
	for _, r := range lapsed {
		db.Exec(`DELETE FROM spell_effects WHERE id = $1`, r.id)
		if n := moreSpellEnds(r.kind, r.target, r.detail); n != "" {
			notes = append(notes, n)
		}
	}
	return notes
}

// moreSpellEnds undoes what an ending spell changed (buffEnds calls it).
func moreSpellEnds(kind string, targetID int, detail string) string {
	switch kind {
	case "polymorph":
		if strings.HasPrefix(detail, "m|") {
			p := strings.SplitN(detail, "|", 3)
			lobby := atoiSafe(p[1])
			name := ""
			editMonsterEntry(lobby, targetID, func(e map[string]interface{}) {
				name = fmt.Sprint(e["name"])
				polymorphRevert(e, detail, 0)
			})
			setTokenSize(lobby, targetID, sizeSquares(creatureSize(lobby, targetID)))
			if name != "" {
				return "Polymorph ends: " + name + " returns to its true form."
			}
			return ""
		}
		if targetID > 0 {
			db.Exec(`UPDATE characters SET temp_hp = 0 WHERE id = $1`, targetID) // the beast's Temporary Hit Points vanish
			return leaveWildShape(targetID, "Polymorph ends")
		}
	case "enlarge_reduce":
		p := strings.Split(detail, "|")
		if len(p) >= 2 {
			lobby := atoiSafe(p[1])
			setTokenSize(lobby, targetID, sizeSquares(creatureSize(lobby, targetID)))
		}
	case "fly":
		if targetID > 0 {
			if feet := landToken(lobbyOfChar(targetID), targetID); feet > 0 {
				return fallCharacter(targetID, feet, "the Fly spell ended")
			}
		}
	case "gaseous_form":
		if targetID > 0 {
			if feet := landToken(lobbyOfChar(targetID), targetID); feet > 0 {
				return fallCharacter(targetID, feet, "Gaseous Form ended")
			}
		}
	}
	return ""
}

// landToken puts a creature's token back on the ground and returns how high it was.
func landToken(lobby, id int) int {
	m, ok := activeEncounterMap(lobby)
	if !ok {
		return 0
	}
	for i, t := range m.Tokens {
		if t.ID == id && t.Altitude > 0 {
			h := t.Altitude
			m.Tokens[i].Altitude = 0
			m.Revision = mapRevision()
			b, _ := json.Marshal(m)
			db.Exec(`UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2 AND active = true`, string(b), lobby)
			return h
		}
	}
	return 0
}

// altitudeProblem: why the creature can't be at that height ("" when it can).
func altitudeProblem(lobby, id, altitude int) string {
	if altitude < 0 {
		return "altitude is feet above the ground: 0 or more"
	}
	if altitude > 0 && !canFlyNow(lobby, id) {
		return "only a creature with a Fly Speed (the Fly spell, Gaseous Form, a flying beast form) can rise above the ground"
	}
	return ""
}
