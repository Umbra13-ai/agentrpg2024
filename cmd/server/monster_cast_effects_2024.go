package main

// What a monster's cast does when the spell is neither a spell attack nor a saving throw (SRD 5.2.1). A monster casts through
// resolveMonsterSpell (monster_effects_2024.go); this file gives that cast the same effects a character's cast of the spell has,
// read from the same tables, so a monster's Bless is a character's Bless:
//   - lasting effects: lastingSpellEffect (buffSpells and gmSpellKinds, the tables a character's cast and the GM's grant read: the
//     effect's kind, its duration and how many creatures it reaches at a level). A monster's cast applies only the kinds whose readers
//     serve any creature (castEffectAnyCreature: the attack, save and Armor Class owners read spell_effects by id); the others stay
//     the GM's, named as such.
//   - Invisibility and Greater Invisibility: the Invisible condition through the condition owner, recorded with the caster
//     (SourceID) so the end of the monster's Concentration ends it (endMonsterConcentrationIn); Invisibility and Sanctuary also end
//     when their creature attacks, casts a spell or deals damage (endWardsOnOffenseOf, buff_spells_2024.go), now for monsters too.
//   - healing: spellHealDice, the healing a character's cast of the spell rolls (dice at the level, the spellcasting ability
//     modifier where the spell says so), given through the Hit Point owner of each kind of target (healHP, healMonster,
//     healCompanion).
//   - the spellcasting ability modifier and save DC of a monster: monsterSpellcastingMod / monsterSpellDC, from the stat block's
//     "using <Ability> as the spellcasting ability" and its scores (a DC the text omits is 8 + Proficiency Bonus + the modifier).

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// castEffectAnyCreature: the lasting effect kinds whose readers serve any creature, character or monster (spellRollAdjust,
// spellACAdjust, sanctuaryBlocks, the Invisible condition). A kind not listed is read for characters only, so a monster's cast of it
// stays the GM's.
var castEffectAnyCreature = map[string]bool{"bless": true, "shield_of_faith": true, "invisibility": true, "greater_invisibility": true, "sanctuary": true,
	"pass_without_trace": true} // passWithoutTraceOn reads any creature (the monster Hide action, attemptMonsterHide)

// castInvisibleKinds: the kinds that give the Invisible condition while they last.
var castInvisibleKinds = map[string]bool{"invisibility": true, "greater_invisibility": true}

// lastingSpellEffect is the effect a lasting spell puts on its targets: its kind, its duration in rounds, how many creatures it
// reaches with a slot of that level, and whether it is the caster's own (Range Self). One reading of buffSpells and gmSpellKinds for
// a character's cast, a monster's cast and the GM's grant.
func lastingSpellEffect(slug string, slot int) (kind string, rounds, limit int, self, ok bool) {
	if b, found := buffSpells[slug]; found {
		limit = 1
		if b.Most != nil {
			limit = b.Most(slot)
		}
		return b.Kind, b.Rounds, limit, b.Self, true
	}
	if g, found := gmSpellKinds[slug]; found {
		if limit = spellLimit(slug, slot); limit == 0 {
			limit = 1
		}
		return g.kind, g.rounds, limit, false, true
	}
	return "", 0, 0, false, false
}

// spellHealDice is the healing a spell rolls with a slot of that level (its dice, or a fixed number as text: Heal's 70) and whether
// the caster's spellcasting ability modifier is added ("plus your spellcasting ability modifier": Cure Wounds, Healing Word).
func spellHealDice(sp SRDSpell, slot int) (dice string, addsMod bool) {
	dice = sp.Healing
	if d, ok := sp.HealAtSlotLevel[strconv.Itoa(slot)]; ok && d != "" {
		for _, m := range []string{" + MOD", "+ MOD", "+MOD"} {
			d = strings.Replace(d, m, "", 1)
		}
		dice = strings.TrimSpace(d)
	}
	return dice, strings.Contains(strings.ToLower(sp.Description), "spellcasting ability modifier")
}

// rollSpellHealing rolls the healing (a fixed number is that number) plus mod.
func rollSpellHealing(dice string, mod int) int {
	if flat, err := strconv.Atoi(strings.TrimSpace(dice)); err == nil {
		return flat
	}
	return game.RollDamage(dice, false) + mod
}

var (
	castAbilityRe  = regexp.MustCompile(`using (Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) as the spellcasting ability`)
	healUpToRe     = regexp.MustCompile(`(?i)up to (two|three|four|five|six|seven|eight|nine|ten) creatures`)
	healCountWords = map[string]int{"two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10}
)

// healTargetLimit: how many creatures a healing spell reaches ("up to six creatures": Mass Healing Word), else one.
func healTargetLimit(sp SRDSpell) int {
	if m := healUpToRe.FindStringSubmatch(sp.Description); m != nil {
		return healCountWords[strings.ToLower(m[1])]
	}
	return 1
}

// monsterSpellcastingAbility: the ability a monster casts with, from its stat block ("using Wisdom as the spellcasting ability"; an
// entry that says "the same spellcasting ability as Spellcasting" reads the Spellcasting entry, which the search finds). "" when the
// stat block names none.
func monsterSpellcastingAbility(meta *monsterMeta) string {
	if meta == nil {
		return ""
	}
	for _, list := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Reactions, meta.Traits, meta.Legendary} {
		for _, a := range list {
			if m := castAbilityRe.FindStringSubmatch(a.Text); m != nil {
				return shortAbility(m[1])
			}
		}
	}
	return ""
}

// monsterSpellcastingMod: the monster's spellcasting ability modifier (0 when its stat block names no ability).
func monsterSpellcastingMod(lobbyID, monsterID int) int {
	key := monsterKeyOfCombatant(lobbyID, monsterID)
	ab := monsterSpellcastingAbility(monsterMetaFor(key))
	if ab == "" {
		return 0
	}
	if sb := monsterStatBlock(key); sb != nil {
		return game.Modifier(asInt(sb[ab]))
	}
	return 0
}

// crProficiencyBonus: a monster's Proficiency Bonus by its Challenge Rating (SRD 5.2.1: +2 up to CR 4, one more every four).
func crProficiencyBonus(cr string) int {
	v := crValue(cr)
	if v < 5 {
		return 2
	}
	return 2 + int((v-1)/4)
}

// monsterSpellDC: the save DC the stat block gives, else 8 + Proficiency Bonus + the spellcasting ability modifier (a Divine Aid
// that casts "using the same spellcasting ability as Spellcasting" with no DC written).
func monsterSpellDC(lobbyID, monsterID, dc int) int {
	if dc > 0 {
		return dc
	}
	key := monsterKeyOfCombatant(lobbyID, monsterID)
	if monsterSpellcastingAbility(monsterMetaFor(key)) == "" {
		return 0
	}
	pb := 2
	if sb := monsterStatBlock(key); sb != nil {
		pb = crProficiencyBonus(sprintAny(sb["cr"]))
	}
	return 8 + pb + monsterSpellcastingMod(lobbyID, monsterID)
}

// monsterCastLasting applies a lasting effect a monster casts (Bless, Shield of Faith, Invisibility, Greater Invisibility,
// Sanctuary). handled false: the spell isn't one this file applies.
func monsterCastLasting(r *http.Request, lobbyID, monsterID int, sp SRDSpell, slug, head string, level, dc int, targets []int, selfCast, apply bool) ([]string, bool) {
	kind, rounds, limit, self, ok := lastingSpellEffect(slug, level)
	if !ok || !castEffectAnyCreature[kind] {
		return nil, false
	}
	ids := targets
	if selfCast || self || len(ids) == 0 {
		ids = []int{monsterID}
	}
	note := ""
	if len(ids) > limit {
		note = fmt.Sprintf(" (only the first %d: the spell reaches %d at this level)", limit, limit)
		ids = ids[:limit]
	}
	var names []string
	for _, id := range ids {
		if id == monsterID {
			names = append(names, "itself")
			continue
		}
		names = append(names, creatureName(lobbyID, id))
	}
	what := spellEffectText[kind]
	if kind == "sanctuary" {
		if dc = monsterSpellDC(lobbyID, monsterID, dc); dc == 0 {
			return []string{head + ": its stat block gives no save DC for the ward, so the GM resolves Sanctuary (nothing was applied)."}, true
		}
		what = fmt.Sprintf("a creature that targets it with an attack roll or a damaging spell makes a DC %d Wisdom save or loses it; ends if it attacks, casts a spell or deals damage", dc)
	}
	line := fmt.Sprintf("%s on %s: %s%s.", head, joinNames(names), what, note)
	if !apply {
		return []string{line + " (Preview: nothing was applied.)"}, true
	}
	// a new casting ends the caster's earlier one (a Concentration spell's was ended already by startMonsterConcentration)
	markExecuted(r) // the spell is cast: a limited use is spent
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND caster_id = $2 AND kind = $3`, lobbyID, monsterID, kind)
	for _, id := range ids {
		detail := ""
		if kind == "sanctuary" {
			detail = strconv.Itoa(dc)
		}
		addSpellEffect(lobbyID, id, monsterID, sp.Name, kind, detail, rounds, "", 0)
		if castInvisibleKinds[kind] {
			// recorded with its caster: the end of the monster's Concentration ends it (monster_concentration_2024.go)
			if res := addCondition(lobbyID, id, condAdd{Condition: "invisible", Source: sp.Name, Rounds: rounds, SourceID: monsterID, Magical: true}); res.Immune != "" {
				line += fmt.Sprintf(" %s: %s.", creatureName(lobbyID, id), res.Immune)
			}
		}
	}
	return []string{line}, true
}

// monsterCastHealing heals the targets of a healing spell a monster casts (Healing Word, Cure Wounds ...). handled false: the
// spell heals nothing (or raises a maximum instead: Aid, a lasting effect).
func monsterCastHealing(r *http.Request, lobbyID, monsterID int, sp SRDSpell, slug, head string, level int, targets []int, selfCast, apply bool) ([]string, bool) {
	if sp.Healing == "" || buffSpells[slug].Kind != "" {
		return nil, false
	}
	ids := targets
	if selfCast {
		ids = []int{monsterID}
	}
	if len(ids) == 0 {
		return blockedBeforeRoll(r, head+": name the creature it heals (target or target_ids). Nothing was rolled."), true
	}
	lines := []string{head + "."}
	if limit := healTargetLimit(sp); len(ids) > limit {
		lines[0] = fmt.Sprintf("%s (it reaches %d creature(s); the others named aren't healed).", head, limit)
		ids = ids[:limit]
	}
	dice, addsMod := spellHealDice(sp, level)
	mod := 0
	if addsMod {
		mod = monsterSpellcastingMod(lobbyID, monsterID)
	}
	for _, id := range ids {
		amount := rollSpellHealing(dice, mod)
		roll := fmt.Sprintf("%s%+d = %d", dice, mod, amount)
		if mod == 0 {
			roll = fmt.Sprintf("%s = %d", dice, amount)
		}
		name := creatureName(lobbyID, id)
		if !apply {
			lines = append(lines, fmt.Sprintf("%s would regain %s Hit Points (preview: nothing was applied).", name, roll))
			continue
		}
		markExecuted(r)                                                            // the spell is cast: a limited use is spent
		if ended, _ := endSpellConditions(lobbyID, id, slug, ""); len(ended) > 0 { // Heal: the conditions end first, then it heals (spell_ends_conditions_2024.go)
			lines = append(lines, fmt.Sprintf("The %s condition on %s ends.", joinNames(ended), name))
		}
		switch {
		case isCompanionCombatID(id):
			got, err := healCompanion(id-companionIDBase, amount)
			if err != nil {
				lines = append(lines, fmt.Sprintf("%s: the healing could not be saved (database error); nothing changed.", name))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s regains %d Hit Points (%s).", name, got, roll))
		case id < 0:
			before, after, err := healMonster(lobbyID, id, amount)
			if err != nil {
				lines = append(lines, fmt.Sprintf("%s: the healing could not be saved (database error); nothing changed.", name))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s regains %d Hit Points (%s; %s).", name, after.HP-before.HP, roll, game.MonsterHealth(after.HP, after.Max)))
		default:
			h, err := healHP(id, amount)
			if err != nil {
				lines = append(lines, fmt.Sprintf("%s: the healing could not be saved (database error); nothing changed.", name))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s regains %d Hit Points (%s; %d → %d).", h.Name, h.Healed, roll, h.Before.HP, h.After.HP))
		}
	}
	return lines, true
}

// monsterCastEndsConditions: a monster's cast of a spell that only ends conditions (Lesser Restoration, Protection from Poison's
// ending): the same table as a character's cast (spellEndsConditions, endSpellConditions). For a spell that ends one
// condition of a list, the GM's use names it ("poisoned"), else the first the target has. A healing spell's conditions end in
// monsterCastHealing, before it heals.
func monsterCastEndsConditions(r *http.Request, lobbyID, monsterID int, sp SRDSpell, slug, head string, targets []int, selfCast, apply bool, use string) ([]string, bool) {
	if _, ok := spellEndsConditions[slug]; !ok || sp.Healing != "" {
		return nil, false
	}
	ids := targets
	if selfCast {
		ids = []int{monsterID}
	}
	if len(ids) == 0 {
		return blockedBeforeRoll(r, head+": name the creature it is cast on (target). Nothing was cast."), true
	}
	lines := []string{head + "."}
	for _, id := range ids {
		name := creatureName(lobbyID, id)
		if !apply {
			lines = append(lines, fmt.Sprintf("%s: %s would end %s (preview: nothing was applied).", name, sp.Name, spellEndsWords(slug)))
			continue
		}
		markExecuted(r) // the spell is cast: a limited use is spent
		ended, named := endSpellConditions(lobbyID, id, slug, use)
		switch {
		case len(ended) > 0:
			lines = append(lines, fmt.Sprintf("The %s condition on %s ends.", joinNames(ended), name))
		case named != "":
			lines = append(lines, fmt.Sprintf("%s doesn't have the %s condition, so nothing ends.", name, titleCase(named)))
		default:
			lines = append(lines, fmt.Sprintf("%s has none of %s, so nothing ends.", name, spellEndsWords(slug)))
		}
	}
	return lines, true
}

// castChoiceList: the spells an entry offers to cast ("casts Bless, Healing Word, or Sanctuary"): two or more known spells, in order.
// The GM names the one cast with use; none is guessed.
func castChoiceList(text string) []string {
	low := normQuotes(text)
	i := strings.Index(low, "casts ")
	if i < 0 {
		return nil
	}
	rest := low[i+len("casts "):]
	for _, stop := range []string{", using", ", requiring", " using", " requiring", " on ", "."} { // " on it": "casts Cure Wounds or Lesser Restoration on it" (Unicorn's Blessing)
		if j := strings.Index(rest, stop); j >= 0 {
			rest = rest[:j]
		}
	}
	var out []string
	for _, part := range strings.Split(strings.ReplaceAll(rest, " or ", ", "), ",") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "or "))
		if part == "" {
			continue
		}
		sp, ok := srdSpellsMemory[normalizeSpellSlug(part)]
		if !ok {
			return nil // not a list of spells
		}
		out = append(out, sp.Name)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// monsterCastDirect: a spell with no attack roll and no saving throw whose effect lands on its targets at once, resolved by the same owners as a
// character's cast: Magic Missile's darts (beamPlan: automatic hits, 1d4+1 Force each, the Brooch of Shielding's Immunity) and the Power Words
// (powerWordOn: Kill, Stun, Heal) and Remove Curse (endCursesBySpell). handled is false for any other spell.
func monsterCastDirect(r *http.Request, lobbyID, monsterID int, sp SRDSpell, slug, head string, level, dc int, targets []int, apply bool) (lines []string, handled bool) {
	lines = []string{head + "."}
	if count, dice, auto := beamPlan(slug, sp.Level, level, 0); auto && count > 0 {
		if apply {
			markExecuted(r)
		}
		perDart := dartShares(count, targets) // darts by target, as the caster divides them
		for _, tid := range targets {
			n := perDart[tid]
			if n == 0 {
				continue
			}
			who := creatureName(lobbyID, tid)
			if tid > 0 && magicMissileImmune(tid, sp.Name) { // Brooch of Shielding
				lines = append(lines, fmt.Sprintf("%s is immune to Magic Missile (Brooch of Shielding).", who))
				continue
			}
			total := 0
			for d := 0; d < n; d++ {
				total += game.RollDamage(normDice(dice), false)
			}
			clause := fmt.Sprintf("%d dart(s) deal %d force damage (not applied)", n, total)
			if apply {
				switch {
				case tid < 0 && !isCompanionCombatID(tid):
					clause, _ = damageMonster(lobbyID, tid, total, "force")
				case isCompanionCombatID(tid):
					if c, ok := activeCompanionInCampaign(lobbyID, tid); ok {
						clause = damageCompanion(c, total, "force")
					}
				default:
					clause = damageCharacter(tid, total, "force")
				}
				clause = fmt.Sprintf("%d dart(s): %s", n, clause)
			}
			lines = append(lines, who+" "+clause+".")
		}
		return lines, true
	}
	switch slug {
	case "remove-curse": // every curse on each target ends (endCursesBySpell: the same owner as a character's cast)
		if !apply {
			return append(lines, sp.Name+" would end the curses on its targets (not applied)."), true
		}
		markExecuted(r)
		for _, tid := range targets {
			text := endCursesBySpell(lobbyID, slug, []int{tid})
			if text == "" {
				text = creatureName(lobbyID, tid) + " is under no curse the server tracks, so nothing ends."
			}
			lines = append(lines, text)
		}
		return lines, true
	case "power-word-kill", "power-word-stun", "power-word-heal":
		if len(targets) > 1 {
			lines = append(lines, fmt.Sprintf("(%s affects one creature; %s isn't affected.)", sp.Name, creatureName(lobbyID, targets[1])))
		}
		if !apply {
			return append(lines, sp.Name+" would affect "+creatureName(lobbyID, targets[0])+" (not applied)."), true
		}
		markExecuted(r)
		return append(lines, powerWordOn(lobbyID, slug, targets[0], dc)), true
	}
	return nil, false
}
