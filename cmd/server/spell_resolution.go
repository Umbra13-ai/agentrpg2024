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
	"sync"

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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
	var ac int
	var name string
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == monster {
			ac = asInt(e["ac"])
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
	bonus := game.ProficiencyBonus(charLevel) + spellMod + magicSpellAttackBonus(charID) // Wand of the War Mage, staffs, Robe of the Archmagi
	ac += spellACAdjust(lobbyID, monster)                                                // Slow, Shield of Faith
	// a Spell Scroll's own attack bonus
	if v, ok := itemCastAttack.Load(charID); ok {
		bonus = v.(int)
	}
	// spellD20 rolls one spell attack: Advantage against a creature outlined by Faerie Fire, Bless and Bane on the caster
	closeCombat := spellRangeFeet(spell.Range) > 5 && enemyAdjacentOnMap(lobbyID, charID) // a ranged spell attack with an enemy within 5 feet
	spellD20 := func() (natural, adj int) {
		natural = game.RollDie(20)
		adv := outlined(lobbyID, monster)
		if adv != closeCombat {
			b := game.RollDie(20)
			if (adv && b > natural) || (closeCombat && b < natural) {
				natural = b
			}
		}
		adj, _ = spellRollAdjust(lobbyID, charID, "attack")
		return
	}
	var lines []string

	if count, beamDice, auto := beamPlan(spellKey, spell.Level, slotLevel, charLevel); count > 0 {
		total, hits := 0, 0
		for i := 0; i < count; i++ {
			roll, adj := spellD20()
			hit := auto || roll == 20 || (roll != 1 && roll+bonus+adj >= ac)
			if !hit {
				continue
			}
			d := game.RollDamage(beamDice, roll == 20 && !auto)
			if spellKey == "eldritch-blast" && hasInvocation(charID, "agonizing-blast") && game.Modifier(spellModScore(charID)) > 0 {
				d += game.Modifier(spellModScore(charID))
			}
			total += d
			hits++
		}
		if total == 0 {
			return fmt.Sprintf(" Against %s: all %d missed.", name, count)
		}
		text, _ := damageMonster(lobbyID, monster, total, dtype)
		return fmt.Sprintf(" Against %s: %d of %d hit for %d %s damage. %s", name, hits, count, total, dtype, text)
	}

	switch {
	case isSpellAttack(spell):
		roll, adj := spellD20()
		bonus += adj
		seekNote := ""
		if _, seeking := seekingSpell.LoadAndDelete(charID); seeking && roll != 20 && (roll == 1 || roll+bonus < ac) {
			roll = game.RollDie(20) // Seeking Spell: reroll a missed attack and use the new roll
			seekNote = " (Seeking Spell: the miss is rerolled)"
		}
		if roll == 1 || (roll != 20 && roll+bonus < ac) {
			if potentCantrip(charID, spell) && rolled/2 > 0 { // F119: Potent Cantrip
				text, _ := damageMonster(lobbyID, monster, rolled/2, dtype)
				return fmt.Sprintf(" Spell attack against %s: MISS (Potent Cantrip: half damage, %d %s). %s", name, rolled/2, dtype, text)
			}
			return fmt.Sprintf(" Spell attack against %s%s: MISS.", name, seekNote)
		}
		d := rolled // the damage already shown in the cast text (all bonuses included)
		if roll == 20 {
			d += game.RollDamage(damageDice, false) // a critical hit rolls the damage dice twice
		}
		label := "HIT"
		if roll == 20 {
			label = "CRITICAL HIT"
		}
		text, _ := damageMonster(lobbyID, monster, d, dtype)
		if monsterHPNow(lobbyID, monster) <= 0 {
			text += killEffectNote(charID) // Dark One's Blessing (F134)
		}
		text += spellRider(lobbyID, charID, monster, spellKey) // Guiding Bolt, Ray of Frost, Chill Touch ... (F250)
		lines = append(lines, fmt.Sprintf(" Spell attack against %s%s: %s, %d %s damage. %s", name, seekNote, label, d, dtype, text))
	case spell.SavingThrow != "" || len(spell.DamageParts) > 0:
		ability := saveAbilityKey(spell.SavingThrow)
		// F247: without damage parts the spell's text decides: "half as much damage" on a success, otherwise none (Sacred Flame, Acid
		// Splash, Vicious Mockery ... took half damage on a successful save)
		onSave := "none"
		if dl := strings.ToLower(spell.Description); strings.Contains(dl, "half as much damage") || strings.Contains(dl, "half damage") {
			onSave = "half"
		}
		if len(spell.DamageParts) > 0 {
			onSave = "half"
			if spell.DamageParts[0].Save != "" {
				ability = saveAbilityKey(spell.DamageParts[0].Save)
			}
			if spell.DamageParts[0].OnSave == "none" {
				onSave = "none"
			}
		}
		if ability == "" {
			return ""
		}
		// the spell's other damage parts that happen now (Meteor Swarm: Fire and Bludgeoning; Flame Strike: Fire and Radiant), rolled once
		type extraPart struct {
			dmg           int
			typ, save, on string
		}
		var extras []extraPart
		if len(spell.DamageParts) > 1 {
			for _, p := range spell.DamageParts[1:] {
				if _, later := laterTimings[p.Timing]; later || p.Timing == "on_hit" {
					continue
				}
				extras = append(extras, extraPart{game.RollDamage(p.diceAt(slotLevel), false), strings.ToLower(p.Type), p.Save, p.OnSave})
			}
		}
		// every creature the spell is aimed at (target_id and target_ids) makes its own saving throw; the damage is rolled once
		for _, id := range spellMonsterTargets(charID) {
			tname := monsterName(lobbyID, id)
			stakes := saveStakes{Damage: rolled, Condition: isDamageConditionSpell(spellKey)}
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
			var total int
			switch {
			case typed && rule.AutoFail: // Blight: a Plant creature automatically fails (F191); Legendary Resistance may still turn it
				saved = id < 0 && legendaryResistance(lobbyID, id, stakes)
			case disWhy != "": // Shatter: a Construct has Disadvantage (F192); Heightened Spell (F194)
				typeSaveDisadvantage.Store(lobbyID, id)
				saved, total = monsterSpellSaveFor(lobbyID, id, ability, saveDC, stakes)
				typeSaveDisadvantage.Delete(lobbyID)
			default:
				saved, total = monsterSpellSaveFor(lobbyID, id, ability, saveDC, stakes) // Magic Resistance gives Advantage; Legendary Resistance
			}
			d := rolled
			how := fmt.Sprintf("fails its %s save (%s)", strings.ToUpper(ability), saveRollText(lobbyID, id, ability, total, saveDC))
			if disWhy != "" {
				how = fmt.Sprintf("fails its %s save with Disadvantage, %s (%s)", strings.ToUpper(ability), disWhy, saveRollText(lobbyID, id, ability, total, saveDC))
			}
			if typed && rule.AutoFail {
				how = fmt.Sprintf("automatically fails its %s save (a %s creature)", strings.ToUpper(ability), rule.Type)
				if saved {
					how = "automatically fails its save (a " + rule.Type + " creature) but succeeds with Legendary Resistance"
				}
			}
			if saved {
				if !(typed && rule.AutoFail) {
					how = fmt.Sprintf("succeeds on its %s save (%s)", strings.ToUpper(ability), saveRollText(lobbyID, id, ability, total, saveDC))
					if disWhy != "" {
						how = fmt.Sprintf("succeeds on its %s save despite Disadvantage, %s (%s)", strings.ToUpper(ability), disWhy, saveRollText(lobbyID, id, ability, total, saveDC))
					}
				}
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
				text, _ := damageMonster(lobbyID, id, d, dtype)
				if monsterHPNow(lobbyID, id) <= 0 {
					text += killEffectNote(charID)
				}
				lines = append(lines, fmt.Sprintf(" %s %s. %s%s", tname, how, text, rider))
			} else {
				text, _ := damageMonster(lobbyID, id, d, dtype)
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
					addMonsterCondition(lobbyID, id, c, spell.Name, extendRounds(charID, cs.Rounds), "", saveDC)
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

// typeSaveDisadvantage: lobby -> the monster whose save now has Disadvantage from its creature type (read by moreSaveDisadvantage).
var typeSaveDisadvantage sync.Map

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
	if sp, ok := srdSpellsMemory[key]; ok && strings.HasPrefix(strings.ToLower(sp.CastingTime), "bonus action") {
		return "bonus_cast"
	}
	if sp, ok := srdSpellsMemory[key]; ok && strings.HasPrefix(strings.ToLower(sp.CastingTime), "reaction") {
		return "reaction_cast" // Shield, Counterspell, Feather Fall...: the Reaction, not the action (F54)
	}
	return action
}

// withSpellResolution appends the result against a targeted monster to the cast text.
func withSpellResolution(charID int, spellKey string, spell SRDSpell, damageDice string, rolled, slotLevel, charLevel, spellMod, saveDC int, castText string) string {
	res := resolveSpellOnMonster(charID, spellKey, spell, damageDice, rolled, slotLevel, charLevel, spellMod, saveDC) +
		resolveSpellOnCharacters(charID, spell, damageDice, rolled, charLevel, spellMod, saveDC)
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

// saveRollText: a monster's saving throw as the DM can check it: "d20 12 +1 = 13 vs DC 13" (F247).
func saveRollText(lobbyID, id int, ability string, total, dc int) string {
	bonus, _, _ := saveBonusFor(lobbyID, id, ability)
	return fmt.Sprintf("d20 %d %+d = %d vs DC %d", total-bonus, bonus, total, dc)
}
