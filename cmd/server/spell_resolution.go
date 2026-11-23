package main

// A damaging spell cast at a monster with target_id is resolved by the server (BUGS 67):
//	spell attack    d20 + Proficiency Bonus + spellcasting modifier against the monster's Armor Class; a 20 is a critical hit
//	                (double dice), a 1 misses; on a hit the damage is applied. Eldritch Blast, Scorching Ray and Magic Missile
//	                make several attacks or darts (Magic Missile never misses).
//	saving throw    the monster rolls the printed save against the spell save DC; a failure takes the damage, a success takes
//	                half (or none when the spell says so).
// The player is told hit or miss, the damage and how the creature looks, never its Armor Class or numbers.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// beamPlan is how many separate attacks or darts a spell makes and the dice of each.
func beamPlan(spellKey string, spellLevel, slotLevel, charLevel int) (count int, dice string, autoHit bool) {
	switch spellKey {
	case "eldritch-blast":
		n := 1
		switch {
		case charLevel >= 17:
			n = 4
		case charLevel >= 11:
			n = 3
		case charLevel >= 5:
			n = 2
		}
		return n, "1d10", false
	case "scorching-ray":
		return 3 + maxInt(0, slotLevel-2), "2d6", false
	case "magic-missile":
		return 3 + maxInt(0, slotLevel-1), "1d4+1", true
	}
	return 0, "", false
}

// dartShares: how the caster divides a spell's automatic darts over its targets (Magic Missile: round-robin, in the order named). The caster's cast
// at a character (resolveSpellOnCharacters) and a monster's cast (monsterCastDirect) divide them the same way.
func dartShares(count int, targets []int) map[int]int {
	shares := map[int]int{}
	for i := 0; i < count && len(targets) > 0; i++ {
		shares[targets[i%len(targets)]]++
	}
	return shares
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// halfOnMiss: why a spell attack that misses still deals half its damage ("" when it deals none): Potent Cantrip (a cantrip), or the spell's own
// first damage part ("On a miss, the arrow splashes the target with acid for half as much of the initial damage only": Acid Arrow's on_miss).
func halfOnMiss(charID int, spell SRDSpell) string {
	if len(spell.DamageParts) > 0 && spell.DamageParts[0].OnMiss == "half" {
		return "the spell's splash"
	}
	if potentCantrip(charID, spell) {
		return "Potent Cantrip"
	}
	return ""
}

func isSpellAttack(spell SRDSpell) bool {
	return spell.SavingThrow == "" && strings.Contains(strings.ToLower(spell.Description), "spell attack")
}

func saveAbilityKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) >= 3 {
		return s[:3]
	}
	return s
}

// resolveSpellOnMonster returns "" when the spell was not cast at a monster with target_id.
func resolveSpellOnMonster(charID int, spellKey string, spell SRDSpell, damageDice string, rolled, slotLevel, charLevel, spellMod, saveDC int) string {
	lobbyID, monster, ok := attackedMonster(charID)
	if !ok {
		return ""
	}
	var name string
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == monster {
			name = fmt.Sprint(e["name"])
		}
	}
	if name == "" {
		return ""
	}
	if antimagicCovers(lobbyID, monster) { // Antimagic Field (spells_more_2024.go)
		return fmt.Sprintf(" %s is inside an Antimagic Field: the spell has no effect on it.", name)
	}
	dtype := strings.ToLower(spell.DamageType)
	// Every spell attack roll here is the one attack function's (attack_2024.go): conditions on both sides, every source of
	// Advantage and Disadvantage (Help, the GM's ruling, a ranged attack next to an enemy ...), Bless and Bane, Exhaustion.
	spellAttack := func() (game.AttackRoll, game.AttackOutcome, string) {
		sa := attackCtx{lobby: lobbyID, attacker: charID, target: monster, name: "Spell attack against " + name,
			bonus: characterSpellAttackBonus(charID, charLevel, spellMod), spell: true, melee: spellAttackIsMelee(spell.Range)}
		return sa.roll()
	}
	attackName := "Spell attack against " + name
	var lines []string

	if count, beamDice, auto := beamPlan(spellKey, spell.Level, slotLevel, charLevel); count > 0 {
		// each beam (or dart) is its own attack roll: its line is the attacker's line (HeadForAttacker), and a hit's riders (Hex,
		// Hunter's Mark: hit_riders_2024.go) are rolled for that beam once it has hit
		total, hits := 0, 0
		var beams []string
		var rs []hitRider
		anyCrit := false
		bonus := &firstHit{left: spellOneRollBonus(charID, spellKey, spell)} // Empowered Evocation, Elemental Affinity: one roll of the spell
		unit := "Beam"
		if auto {
			unit = "Dart"
		}
		for i := 0; i < count; i++ {
			crit := false
			line := fmt.Sprintf("%s %d", unit, i+1)
			if !auto { // Magic Missile hits without a roll
				ar, out, rollNote := spellAttack()
				line = fmt.Sprintf("%s %d: %s%s", unit, i+1, ar.LineForAttacker(attackName, out), rollNote)
				if !out.Hit {
					beams = append(beams, line+".")
					continue
				}
				crit = out.Critical
				anyCrit = anyCrit || crit
			}
			d := game.RollDamage(beamDice, crit)
			if invocationOn(charID, "agonizing-blast", spellKey) && game.Modifier(spellModScore(charID)) > 0 {
				d += game.Modifier(spellModScore(charID))
			}
			d, bonusNote := bonus.add(d)
			total += d
			hits++
			beamRs := hitRiders(charID, monster, crit)
			rs = append(rs, beamRs...)
			if auto {
				line += fmt.Sprintf(" hits: %d %s damage%s%s.", d, dtype, bonusNote, riderNote(beamRs))
			} else {
				line += fmt.Sprintf(", %d %s damage%s%s.", d, dtype, bonusNote, riderNote(beamRs))
			}
			beams = append(beams, line)
		}
		if total == 0 {
			return fmt.Sprintf(" Against %s: %s All %d missed.", name, strings.Join(beams, " "), count)
		}
		text, _ := damageMonster(lobbyID, monster, total, dtype, anyCrit)
		return fmt.Sprintf(" Against %s: %s %d of %d hit for %d %s damage. %s%s", name, strings.Join(beams, " "), hits, count, total, dtype, text, dealRiders(lobbyID, monster, rs, anyCrit))
	}

	hybrid := isHybridSpell(spell) // Ice Knife: the shard's attack, then the explosion's save, hit or miss
	if hybrid {
		ar, out, rollNote := spellAttack()
		if !out.Hit {
			lines = append(lines, fmt.Sprintf(" %s%s.", ar.LineForAttacker(attackName, out), rollNote))
		} else {
			d := rolled
			if out.Critical {
				d += game.RollDamage(damageDice, false)
			}
			text, _ := damageMonster(lobbyID, monster, d, dtype)
			text += hitWithRiders(lobbyID, charID, monster, out.Critical)
			lines = append(lines, fmt.Sprintf(" %s%s, %d %s damage. %s", ar.LineForAttacker(attackName, out), rollNote, d, dtype, text))
		}
	}
	switch {
	case !hybrid && isSpellAttack(spell):
		ar, out, rollNote := spellAttack()
		if _, seeking := seekingSpell.LoadAndDelete(charID); seeking && !out.Hit {
			out = ar.Judge(game.RollDie(20)) // Seeking Spell: reroll a missed attack and use the new roll
			rollNote += " (Seeking Spell: the miss is rerolled)"
		}
		if !out.Hit {
			if why := halfOnMiss(charID, spell); why != "" && rolled/2 > 0 { // F119: Potent Cantrip; Acid Arrow's splash
				text, _ := damageMonster(lobbyID, monster, rolled/2, dtype)
				return fmt.Sprintf(" %s%s (%s: half damage, %d %s). %s", ar.LineForAttacker(attackName, out), rollNote, why, rolled/2, dtype, text)
			}
			return fmt.Sprintf(" %s%s.", ar.LineForAttacker(attackName, out), rollNote)
		}
		d := rolled // the damage already shown in the cast text (all bonuses included)
		if out.Critical {
			d += game.RollDamage(damageDice, false) // a critical hit rolls the damage dice twice
		}
		if dx, dt := damageRollAdjust(lobbyID, charID, d, false); dx != 0 { // a weakened die (effect_records_2024.go)
			d += dx
			rollNote += dt
		}
		hurt, _ := hurtMonster(lobbyID, monster, d, dtype, false)
		text := hurt.Text + drainHeal(charID, spell, dtype, hurt.Dealt) // Vampiric Touch: the caster regains half (spell_drain_2024.go)
		text += hitWithRiders(lobbyID, charID, monster, out.Critical)   // Hex, Hunter's Mark (hit_riders_2024.go)
		if monsterHPNow(lobbyID, monster) <= 0 {
			text += killEffectNote(charID) // Dark One's Blessing (F134)
		}
		text += spellRider(lobbyID, charID, monster, spellKey) // Guiding Bolt, Ray of Frost, Chill Touch ... (F250)
		lines = append(lines, fmt.Sprintf(" %s%s, %d %s damage. %s", ar.LineForAttacker(attackName, out), rollNote, d, dtype, text))
	case spell.SavingThrow != "" || len(spell.DamageParts) > 0:
		ability := saveAbilityKey(spell.SavingThrow)
		mainDmg, mainType, mainPart := rolled, dtype, 0
		if hybrid { // the explosion is the first part with a save; the attack's own damage is dealt above
			for i, p := range spell.DamageParts {
				if _, later := laterTimings[p.Timing]; !later && p.Timing != "on_hit" && p.Save != "" {
					mainPart, mainType = i, strings.ToLower(p.Type)
					mainDmg = partRoll(charID, i, p.diceAt(slotLevel))
					break
				}
			}
		}
		// F247: without damage parts the spell's text decides: "half as much damage" on a success, otherwise none (Sacred Flame, Acid
		// Splash, Vicious Mockery ... took half damage on a successful save)
		onSave := "none"
		if dl := strings.ToLower(spell.Description); strings.Contains(dl, "half as much damage") || strings.Contains(dl, "half damage") {
			onSave = "half"
		}
		if len(spell.DamageParts) > 0 {
			onSave = "half"
			if spell.DamageParts[mainPart].Save != "" {
				ability = saveAbilityKey(spell.DamageParts[mainPart].Save)
			}
			if spell.DamageParts[mainPart].OnSave == "none" {
				onSave = "none"
			}
		}
		if ability == "" {
			return strings.Join(lines, "")
		}
		// the spell's other damage parts that happen now (Meteor Swarm: Fire and Bludgeoning; Flame Strike: Fire and Radiant), rolled once
		type extraPart struct {
			dmg           int
			typ, save, on string
		}
		var extras []extraPart
		for i, p := range spell.DamageParts {
			if i == 0 && !hybrid || hybrid && i == mainPart {
				continue
			}
			if _, later := laterTimings[p.Timing]; later || p.Timing == "on_hit" {
				continue
			}
			extras = append(extras, extraPart{partRoll(charID, i, p.diceAt(slotLevel)), strings.ToLower(p.Type), p.Save, p.OnSave})
		}
		// every creature the spell is aimed at (target_id and target_ids) makes its own saving throw; the damage is rolled once
		for _, id := range spellMonsterTargets(charID) {
			tname := monsterName(lobbyID, id)
			stakes := saveStakes{Damage: mainDmg, Condition: isDamageConditionSpell(spellKey)}
			rule, typed := spellTypeSaveRules[spellKey]
			typed = typed && monsterTypeOf(lobbyID, id) == rule.Type
			heightened := heightenedDisadvantage(charID, id) // Heightened Spell (F194)
			disWhy := ""
			if typed && rule.Disadvantage {
				disWhy = "a " + rule.Type
			} else if heightened {
				disWhy = "Heightened Spell"
			}
			var saved bool
			var how string
			if typed && rule.AutoFail { // Blight: a Plant creature automatically fails (F191); Legendary Resistance may still turn it
				saved = id < 0 && legendaryResistance(lobbyID, id, stakes)
				how = fmt.Sprintf("automatically fails its %s save (a %s creature)", strings.ToUpper(ability), rule.Type)
				if saved {
					how = "automatically fails its save (a " + rule.Type + " creature) but succeeds with Legendary Resistance"
				}
			} else { // Shatter: a Construct has Disadvantage (F192); Heightened Spell (F194); Magic Resistance; Legendary Resistance
				var dis []string
				if disWhy != "" {
					dis = []string{disWhy}
				}
				sv := monsterSpellSaveBy(lobbyID, id, ability, saveDC, stakes, dis, charID)
				saved = sv.Success
				how = sv.Clause()
			}
			d := mainDmg
			if saved {
				if onSave == "half" || potentCantrip(charID, spell) { // F119: Potent Cantrip halves a cantrip's damage on a success
					d /= 2
				} else {
					d = 0
				}
			}
			rider := ""
			if !saved {
				rider = spellRider(lobbyID, charID, id, spellKey) // Vicious Mockery, Mind Sliver ... on a failed save (F250)
			}
			if d == 0 {
				lines = append(lines, fmt.Sprintf(" %s %s and takes no damage.%s", tname, how, rider))
			} else if rider != "" {
				text, _ := damageMonster(lobbyID, id, d, mainType)
				if monsterHPNow(lobbyID, id) <= 0 {
					text += killEffectNote(charID)
				}
				lines = append(lines, fmt.Sprintf(" %s %s. %s%s", tname, how, text, rider))
			} else {
				text, _ := damageMonster(lobbyID, id, d, mainType)
				if monsterHPNow(lobbyID, id) <= 0 {
					text += killEffectNote(charID) // Dark One's Blessing (F134)
				}
				lines = append(lines, fmt.Sprintf(" %s %s. %s", tname, how, text))
			}
			for _, e := range extras {
				d2 := e.dmg
				if saved && e.save != "" {
					if e.on == "half" {
						d2 /= 2
					} else {
						d2 = 0
					}
				}
				if d2 > 0 {
					text, _ := damageMonster(lobbyID, id, d2, e.typ)
					lines = append(lines, " "+text)
				}
			}
			if cs, ok := conditionSpells[spellKey]; ok && isDamageConditionSpell(spellKey) && !saved {
				for _, c := range cs.Conditions {
					applyCondition(lobbyID, id, c, spell.Name, extendRounds(charID, cs.Rounds), "", saveDC)
				}
				lines = append(lines, fmt.Sprintf(" %s is %s.", tname, strings.Join(cs.Conditions, " and ")))
			}
		}
	}
	return strings.Join(lines, "") + takeAreaNote(charID)
}

// spellTypeSaveRules: spells whose save depends on the target's creature type (SRD 5.2.1).
var spellTypeSaveRules = map[string]struct {
	Type                   string
	AutoFail, Disadvantage bool
}{
	"blight":  {Type: "plant", AutoFail: true},         // "A Plant creature automatically fails the save." (F191)
	"shatter": {Type: "construct", Disadvantage: true}, // "A Construct has Disadvantage on the save." (F192)
}

func spellModScore(charID int) int {
	var class string
	var intl, wis, cha int
	db.QueryRow(`SELECT class, intl, wis, cha FROM characters WHERE id = $1`, charID).Scan(&class, &intl, &wis, &cha)
	switch strings.ToLower(class) {
	case "wizard":
		return intl
	case "cleric", "druid", "ranger":
		return wis
	}
	return cha
}

// spellEconomyAction maps a cast of a Bonus Action spell to the bonus action in the action economy.
func spellEconomyAction(action, slug, description string) string {
	if action != "cast" {
		return action
	}
	key := slug
	if key == "" {
		key = parseSpellFromDescription(description)
	}
	if sp, ok := srdSpellsMemory[key]; ok {
		switch castingTimeKind(sp.CastingTime) {
		case "bonus action":
			return "bonus_cast"
		case "reaction":
			return "reaction_cast" // Shield, Counterspell, Feather Fall...: the Reaction, not the action (F54)
		}
	}
	return action
}

// castingTimeKind: "bonus action" or "reaction" when the casting time starts with that word, with or without a leading "1 " or "one "
// ("Reaction, which you take when ...", "1 reaction"); "" for anything else (an Action, a duration, a ritual). The SRD data writes
// "Reaction" and "Bonus Action"; the tolerant form keeps another spelling of the same casting time from being costed as an Action.
func castingTimeKind(castingTime string) string {
	ct := strings.ToLower(strings.TrimSpace(castingTime))
	ct = strings.TrimPrefix(strings.TrimPrefix(ct, "1 "), "one ")
	for _, k := range []string{"bonus action", "reaction"} {
		if strings.HasPrefix(ct, k) {
			return k
		}
	}
	return ""
}

// withSpellResolution appends the result against a targeted monster to the cast text.
func withSpellResolution(charID int, spellKey string, spell SRDSpell, damageDice string, rolled, slotLevel, charLevel, spellMod, saveDC int, castText string) string {
	res := resolveSpellOnMonster(charID, spellKey, spell, damageDice, rolled, slotLevel, charLevel, spellMod, saveDC) +
		resolveSpellOnCharacters(charID, spellKey, spell, slotLevel, damageDice, rolled, charLevel, spellMod, saveDC)
	if spellKey == "spirit-guardians" { // the Emanation keeps working at the end of enemies' turns (spirit_guardians_2024.go)
		registerSpiritGuardians(charID, damageDice, spell.DamageType, saveDC, spellMonsterTargets(charID))
		res += " The spirits stay around you while you concentrate: an enemy that ends its turn within 15 feet of you, or that enters the area, makes the save (once per turn)."
	}
	if res != "" {
		// the server resolved the spell against its targets: the damage is told per target (hit, miss, save), not up front
		castText = castDamageHeader.ReplaceAllString(castText, "!")
	}
	return castText + res
}

// castDamageHeader is the "! 26 fire damage (DC 19 Dexterity save, ...)." at the start of a damaging cast's text.
var castDamageHeader = regexp.MustCompile(`! \d+ [A-Za-z]+ damage(\s*\([^)]*\))?\.`)
