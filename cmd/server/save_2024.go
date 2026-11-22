package main

// One saving throw function.
//
// Every saving throw in the server is made here: a character's (a monster's ability, a spell, an item, a trap, a condition's
// repeated save, Concentration, a Death Saving Throw, the GM's call) and a monster's. The rule of the roll itself (Advantage and
// Disadvantage cancel, the total against the DC, an automatic failure or success, the wording of the result) is game.SavingThrow
// (game/save.go). This file is the server's side of it:
//
//   - saveCtx says who saves, with what ability, against what DC and against what cause (a spell, an effect that would Charm or
//     Poison, the GM's words, Concentration).
//   - saveSources is the ONLY list of what gives a saving throw Advantage or Disadvantage or makes it fail automatically, and
//     saveAdjusts the only list of what is added to it (Bless, Bane, Aura of Protection, magic items, cover, Exhaustion). A rule
//     added there reaches every save by every creature. Do not test for them anywhere else.
//   - saveModifier is the only place a saving throw modifier is worked out (the ability modifier, proficiency from the class,
//     Disciplined Survivor and Slippery Mind, a Wild Shape beast's; a monster's stat block).
//   - roll gathers all of that, rolls, and applies what changes the result afterwards (Halfling Luck, Heroic Inspiration, the
//     Luck Blade, Indomitable Might); resist adds a monster's Legendary Resistance.
//
// A caller says who saves against what (saveCtx), calls roll or resist, and then does only its own effects. Before Nov 15 2026
// saving throws were rolled in about twenty places, each with its own subset of these rules.

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// saveCtx is one saving throw about to be made.
type saveCtx struct {
	lobby   int
	saver   int    // a character's id (> 0) or a monster's combat id (< 0)
	ability string // "str" ... "cha" (a full name is read too); game.DeathSave for a Death Saving Throw
	dc      int

	// the cause, where a rule reads it
	spell         bool     // a spell can also activate effects specifically limited to spells
	against       []string // the conditions the effect would impose, or the one the save would end ("poisoned", "charmed")
	source        int      // the creature that forces the save, when known (Holy Nimbus reads its creature type)
	fiendOrUndead bool     // the GM says a Fiend or an Undead forces it (Holy Nimbus)
	concentration bool     // a save to keep Concentration (Eldritch Mind, War Caster, Extended Spell)
	desc          string   // the GM's words for the cause: "poison", "charm" and "fear" in them count as against

	extraAdv    []string          // sources of Advantage only this caller can know ("the GM's ruling")
	extraDis    []string          // sources of Disadvantage only this caller can know ("Heightened Spell")
	adjust      []game.RollAdjust // amounts only this caller can know ("Indomitable +9")
	autoSucceed string            // a feature of the caller makes the save succeed without a roll ("Sculpt Spells")
	inspiration bool              // Heroic Inspiration is spent on this save if it fails: the save is rolled again
	peek        bool              // only look: nothing is used up (Staggering Blow, the Luck Blade, Heroic Inspiration)

	conds           []string // the saver's conditions, filled by gather
	species         *string  // the saver's species, read once
	inspirationUsed bool     // set by roll: Heroic Inspiration was spent
}

// creatureSave rolls a saving throw for a monster or a character with no cause beyond the ability and the DC; a monster that
// fails may use Legendary Resistance against an effect with a condition. Callers that know more about the cause (a spell, a
// condition it would impose) fill a saveCtx themselves. The result is the owner's: a message says what happened with its
// wordings (Line, Clause, LineAgainst), which name every source of Advantage, Disadvantage and what was added, never with a
// line of its own built from Success and Total.
func creatureSave(lobbyID, id int, ability string, dc int) game.SaveResult {
	return (&saveCtx{lobby: lobbyID, saver: id, ability: ability, dc: dc}).resist(saveStakes{Condition: true})
}

// resist rolls the save; a monster that fails may then turn it into a success with Legendary Resistance (the stakes decide when
// it does). The result says so.
func (c *saveCtx) resist(st saveStakes) game.SaveResult {
	r := c.roll()
	if !r.Success && c.saver < 0 && !c.peek && legendaryResistance(c.lobby, c.saver, st) {
		r.Success = true
		r.Changed = append(r.Changed, "Legendary Resistance")
	}
	return r
}

// roll makes the saving throw: everything gathered, the d20, and what changes the result afterwards.
func (c *saveCtx) roll() game.SaveResult {
	s := c.gather()
	r := s.Roll()
	if !r.Rolled() || c.saver <= 0 || c.peek {
		return r
	}
	if r.Natural == 1 { // Halfling Luck: reroll a 1 on the d20 of a D20 Test, and use the new roll
		if re, used, was := applyHalflingLucky(1, c.saver); used {
			r = r.Replace(re, fmt.Sprintf("Halfling Luck: %d → %d", was, re))
		}
	}
	if !r.Success && c.inspiration { // Heroic Inspiration: reroll the die, and use the new roll
		r = r.RollAgain("Heroic Inspiration")
		c.inspirationUsed = true
	}
	if !r.Success && luckReroll(c.saver, false) { // the Luck Blade's Luck, when its owner armed it: a failed D20 Test is rolled again
		r = r.RollAgain("Luck Blade")
	}
	if t, note := indomitableMight(c.saver, c.ability, r.Total); note != "" { // a Strength save that totals less than the score
		r = r.WithTotal(t, fmt.Sprintf("Indomitable Might: the Strength score %d stands in for %d", t, r.Total))
	}
	return r
}

// gather collects everything the saving throw rule needs.
func (c *saveCtx) gather() game.SavingThrow {
	if c.ability != game.DeathSave {
		c.ability = shortAbility(c.ability)
	}
	if c.saver > 0 && c.lobby == 0 {
		c.lobby = lobbyOfChar(c.saver)
	}
	c.conds = creatureConditions(c.lobby, c.saver)
	s := game.SavingThrow{Ability: c.ability, DC: c.dc, Bonus: c.modifier(), AutoSucceed: c.autoSucceed}
	if c.ability == game.DeathSave {
		s.DC = 10 // a Death Saving Throw
	}
	for _, src := range saveSources {
		ok, why := src.applies(c)
		if !ok {
			continue
		}
		if why == "" {
			why = src.why
		}
		switch src.effect {
		case saveAdv:
			s.Advantage = append(s.Advantage, why)
		case saveDis:
			s.Disadvantage = append(s.Disadvantage, why)
		case saveFail:
			if s.AutoFail == "" {
				s.AutoFail = why
			} else {
				s.AutoFail += ", " + why
			}
		}
	}
	s.Advantage = append(s.Advantage, c.extraAdv...)
	s.Disadvantage = append(s.Disadvantage, c.extraDis...)
	for _, a := range saveAdjusts {
		if n, why := a(c); n != 0 {
			s.Adjust = append(s.Adjust, game.RollAdjust{Amount: n, Why: why})
		}
	}
	s.Adjust = append(s.Adjust, c.adjust...)
	return s
}

// modifier is the saver's saving throw modifier (saveModifier).
func (c *saveCtx) modifier() int {
	m, _, _ := saveModifier(c.lobby, c.saver, c.ability)
	return m
}

// saveModifier is a creature's saving throw modifier for an ability, and for a character whether it is proficient and which
// feature makes it so ("" for its class): the ability modifier, the Proficiency Bonus for the saves of its class, every save from
// Monk level 14 (Disciplined Survivor), Wisdom and Charisma from Rogue level 15 (Slippery Mind), and a Wild Shape beast's modifier
// when it is higher. A monster's is the bonus in its stat block, or its ability modifier. A Death Saving Throw has none.
func saveModifier(lobbyID, id int, ability string) (mod int, proficient bool, feature string) {
	short := shortAbility(ability)
	if short == "" || db == nil {
		return 0, false, ""
	}
	if isCompanionCombatID(id) { // a companion saves with the stat block it was made from (its form), like a monster: its listed save bonus or its ability modifier
		if c, ok := loadCompanion(id - companionIDBase); ok {
			if sb := monsterStatBlock(c.Form); sb != nil { // less what a reduction of the score took (ability_owner_2024.go)
				return monsterSaveBonus(c.Form, short, asInt(sb[short])) + monsterAbilityShift(lobbyID, id, short), false, ""
			}
		}
		return 0, false, ""
	}
	if id > 0 {
		var class string
		var level int
		db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1) FROM characters WHERE id = $1`, id).Scan(&class, &level)
		mod = game.Modifier(abilityScoreOf(id, short))
		if proficient, feature = saveProficiency(id, class, level, short); proficient {
			mod += game.ProficiencyBonus(level)
		}
		return wildShapeHigher(id, "save:"+short, mod), proficient, feature
	}
	key := monsterKeyOfCombatant(lobbyID, id)
	if sb := monsterStatBlock(key); sb != nil {
		return monsterSaveBonus(key, short, asInt(sb[short])) + monsterAbilityShift(lobbyID, id, short), false, ""
	}
	return 0, false, ""
}

// againstCond: the save is against an effect that would impose the condition (or would end it), from the caller's list or the
// GM's words ("poison", "charm", "fear").
func (c *saveCtx) againstCond(cond string) bool {
	for _, a := range c.against {
		if strings.EqualFold(conditionBase(a), cond) {
			return true
		}
	}
	switch cond {
	case "poisoned":
		return game.CheckPoisonKeywords(c.desc)
	case "charmed":
		return game.CheckCharmKeywords(c.desc)
	case "frightened":
		return game.CheckFrightenKeywords(c.desc)
	}
	return false
}

// speciesOf: the saving character's species, read once.
func (c *saveCtx) speciesOf() string {
	if c.species == nil {
		var s string
		if c.saver > 0 {
			db.QueryRow(`SELECT COALESCE(species,'') FROM characters WHERE id = $1`, c.saver).Scan(&s)
		}
		c.species = &s
	}
	return *c.species
}

// incapacitated: the saver has the Incapacitated condition (or one that includes it).
func (c *saveCtx) incapacitated() bool {
	return game.IsIncapacitated(c.conds)
}

func (c *saveCtx) is(abilities ...string) bool {
	for _, a := range abilities {
		if c.ability == a {
			return true
		}
	}
	return false
}

// takeOrSee: a one-use effect on the saver: used up by this save, or only looked at when the caller peeks.
func (c *saveCtx) takeOrSee(kind string) bool {
	if !c.peek {
		return takeCombatEffect(c.lobby, c.saver, kind, 0)
	}
	for _, e := range combatEffectsOn(c.lobby, c.saver) {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

const (
	saveAdv = iota
	saveDis
	saveFail
)

// saveSource is one thing that gives a saving throw Advantage or Disadvantage, or makes it fail automatically.
type saveSource struct {
	effect  int                             // saveAdv, saveDis or saveFail
	why     string                          // the words shown for it
	applies func(c *saveCtx) (bool, string) // whether it applies to this save; a non-empty string replaces why
}

// saveSources: every source of Advantage, Disadvantage and automatic failure on a saving throw (SRD 5.2.1). Each row decides
// from the saver, the ability and the cause whether it applies; a row used up by the save (Staggering Blow) is used up here and
// nowhere else. (Filled by init: a save can deal damage, and damage can call for a save.)
var saveSources []saveSource

func init() { saveSources = saveSourceRows }

var saveSourceRows = []saveSource{
	// ---- automatic failure: conditions
	{saveFail, "", func(c *saveCtx) (bool, string) { // Paralyzed, Petrified, Stunned, Unconscious: Strength and Dexterity saves
		fail, _ := game.SaveConditions(c.conds, c.ability)
		return len(fail) > 0, strings.Join(fail, ", ")
	}},

	// ---- Disadvantage
	{saveDis, "", func(c *saveCtx) (bool, string) { // Restrained: Dexterity saves
		_, dis := game.SaveConditions(c.conds, c.ability)
		return len(dis) > 0, strings.Join(dis, ", ")
	}},
	{saveDis, "a Disadvantage rider on its Concentration saves", func(c *saveCtx) (bool, string) {
		return yes(c.concentration && c.saver != 0 && c.lobby > 0 && hasCondMarker(c.lobby, c.saver, "disadv_concentration"))
	}},
	{saveDis, "armor the saver isn't trained in", func(c *saveCtx) (bool, string) { // any D20 Test that uses Strength or Dexterity
		return yes(c.saver > 0 && c.is("str", "dex") && isWearingNonProficientArmor(c.saver))
	}},
	{saveDis, "Reduce", func(c *saveCtx) (bool, string) { return yes(c.is("str") && enlargeMode(c.lobby, c.saver) == "reduce") }},
	{saveDis, "weakened (a Strength-based D20 Test)", func(c *saveCtx) (bool, string) { // Weakening Breath (effect_records_2024.go)
		return yes(c.is("str") && c.saver != 0 && weakenedOn(c.lobby, c.saver))
	}},
	{saveDis, "the Fool card", func(c *saveCtx) (bool, string) { // the Mysterious Deck: Disadvantage on D20 Tests
		_, d := deckD20(c.saver)
		return yes(d)
	}},
	{saveDis, "Staggering Blow", func(c *saveCtx) (bool, string) { // Improved Brutal Strike: the next saving throw it makes
		return yes(c.takeOrSee("staggering"))
	}},

	// ---- Advantage: class features and species traits
	{saveAdv, "Danger Sense", func(c *saveCtx) (bool, string) { // Barbarian 2: Dexterity saves, unless Incapacitated
		return yes(c.saver > 0 && c.is("dex") && !c.incapacitated() && barbarianLevel(c.saver) >= 2)
	}},
	{saveAdv, "Rage", func(c *saveCtx) (bool, string) { return yes(c.is("str") && raging(c.saver)) }}, // Strength saves while raging
	{saveAdv, "Gnomish Cunning", func(c *saveCtx) (bool, string) { // Intelligence, Wisdom and Charisma saves
		return yes(c.saver > 0 && c.is("int", "wis", "cha") && game.HasGnomeCunning(c.speciesOf()))
	}},
	{saveAdv, "Fey Ancestry", func(c *saveCtx) (bool, string) { // to avoid or end the Charmed condition
		return yes(c.saver > 0 && c.againstCond("charmed") && game.HasFeyAncestry(c.speciesOf()))
	}},
	{saveAdv, "Brave", func(c *saveCtx) (bool, string) { // Halfling: to avoid or end the Frightened condition
		return yes(c.saver > 0 && c.againstCond("frightened") && game.HasHalflingBrave(c.speciesOf()))
	}},
	{saveAdv, "Dwarven Resilience", func(c *saveCtx) (bool, string) { // to avoid or end the Poisoned condition
		return yes(c.saver > 0 && c.againstCond("poisoned") && game.HasDwarvenResilience(c.speciesOf()))
	}},
	{saveAdv, "Holy Nimbus", func(c *saveCtx) (bool, string) { // Paladin 20 (Holy Ward): a save forced by a Fiend or an Undead
		if c.saver <= 0 || !hasHolyNimbusActive(c.saver) {
			return false, ""
		}
		t := ""
		if c.source < 0 {
			t = monsterTypeOf(c.lobby, c.source)
		}
		return yes(c.fiendOrUndead || t == "fiend" || t == "undead")
	}},
	{saveAdv, "Defy Death", func(c *saveCtx) (bool, string) {
		return yes(c.ability == game.DeathSave && c.saver > 0 && championDefyDeath(c.saver))
	}}, // Champion 18
	{saveAdv, "Eldritch Mind", func(c *saveCtx) (bool, string) {
		return yes(c.concentration && hasInvocation(c.saver, "eldritch-mind"))
	}},
	{saveAdv, "War Caster", func(c *saveCtx) (bool, string) { return yes(c.concentration && charHasWarCaster(c.saver)) }},
	{saveAdv, "Extended Spell", func(c *saveCtx) (bool, string) { return yes(c.concentration && extendedConcentration(c.saver)) }},

	// ---- Advantage: spells and actions on the saver
	{saveAdv, "Dodge", func(c *saveCtx) (bool, string) { return yes(c.saver > 0 && c.is("dex") && dodgeActive(c.saver)) }}, // dodge_2024.go
	{saveAdv, "Haste", func(c *saveCtx) (bool, string) { return yes(c.is("dex") && hasSpellEffect(c.lobby, c.saver, "hasted")) }},
	{saveAdv, "Gaseous Form", func(c *saveCtx) (bool, string) {
		_, ok := buffEffect(c.lobby, c.saver, "gaseous_form")
		return yes(ok && c.is("str", "dex", "con"))
	}},
	{saveAdv, "Enlarge", func(c *saveCtx) (bool, string) { return yes(c.is("str") && enlargeMode(c.lobby, c.saver) == "enlarge") }},
	{saveAdv, "Foresight", func(c *saveCtx) (bool, string) { // every D20 Test, Death Saving Throws included
		_, ok := buffEffect(c.lobby, c.saver, "foresight")
		return yes(c.saver > 0 && ok)
	}},
	{saveAdv, "Holy Aura", func(c *saveCtx) (bool, string) {
		ok, _ := holyAuraOn(c.lobby, c.saver)
		return yes(ok)
	}},
	{saveAdv, "Beacon of Hope", func(c *saveCtx) (bool, string) { // Wisdom saves and Death Saving Throws
		_, ok := buffEffect(c.lobby, c.saver, "beacon_of_hope")
		return yes(c.saver > 0 && ok && c.is("wis", game.DeathSave))
	}},
	{saveAdv, "Protection from Poison", func(c *saveCtx) (bool, string) {
		_, ok := buffEffect(c.lobby, c.saver, "protection_from_poison")
		return yes(c.saver > 0 && ok && c.againstCond("poisoned"))
	}},

	// ---- Advantage: items
	{saveAdv, "", func(c *saveCtx) (bool, string) { // Ring of Spell Turning, Scarab of Protection, Mantle of Spell Resistance, a Holy Avenger ...: against spells
		if c.saver <= 0 || !c.spell {
			return false, ""
		}
		p := spellSaveProtection(c.lobby, c.saver)
		return p != "", p
	}},
	{saveAdv, "Periapt of Health", func(c *saveCtx) (bool, string) {
		return yes(c.againstCond("poisoned") && periaptOfHealthPoison(c.saver))
	}},
	{saveAdv, "Necklace of Adaptation", func(c *saveCtx) (bool, string) {
		return yes(c.saver > 0 && c.againstCond("poisoned") && wornItem(c.saver, "necklace of adaptation"))
	}},
	{saveAdv, "Belt of Dwarvenkind", func(c *saveCtx) (bool, string) { // a dwarf has Dwarven Resilience already
		return yes(c.saver > 0 && c.againstCond("poisoned") && wornItem(c.saver, "belt of dwarvenkind") && !game.IsDwarf(c.speciesOf()))
	}},
	{saveAdv, "the Jester card", func(c *saveCtx) (bool, string) { // the Mysterious Deck: Advantage on D20 Tests
		a, _ := deckD20(c.saver)
		return yes(a)
	}},

	// ---- Advantage: monsters
	{saveAdv, "Aura of Authority", func(c *saveCtx) (bool, string) { return yes(c.saver < 0 && auraOfAuthority(c.lobby, c.saver)) }}, // Hobgoblin Captain
	{saveAdv, "", func(c *saveCtx) (bool, string) { // Bloodied Frenzy: the saver's own trait (rollTraitAdvantage)
		if c.saver == 0 || (c.saver > 0 && !isCompanionCombatID(c.saver)) {
			return false, ""
		}
		name := rollTraitAdvantage(c.lobby, c.saver, true, false, 0)
		return name != "", name
	}},
	{saveDis, "Sunlight Sensitivity", func(c *saveCtx) (bool, string) { // the variant "Disadvantage on D20 Tests", in sunlight (creature_senses_2024.go)
		_, saves := sunlightPenalty(c.lobby, c.saver)
		return yes(saves)
	}},
	{saveAdv, "Magic Resistance", func(c *saveCtx) (bool, string) { // against spells and other magical effects
		if !c.spell {
			return yes(false)
		}
		if isCompanionCombatID(c.saver) { // a companion's traits are kept on it (the stat block it was made from)
			cp, ok := loadCompanion(c.saver - companionIDBase)
			return yes(ok && companionHasTrait(cp, "Magic Resistance"))
		}
		return yes(c.saver < 0 && monsterHasTrait(c.lobby, c.saver, "Magic Resistance"))
	}},
}

// saveAdjusts: everything added to a saving throw besides its modifier, each with the words shown for it.
var saveAdjusts []func(c *saveCtx) (int, string)

func init() { saveAdjusts = saveAdjustRows }

var saveAdjustRows = []func(c *saveCtx) (int, string){
	func(c *saveCtx) (int, string) { // Bless, Bane, Slow (Dexterity), the Euryale card
		adj, note := spellRollAdjust(c.lobby, c.saver, "save:"+c.ability)
		return adj, strings.Trim(note, " []")
	},
	func(c *saveCtx) (int, string) { // Paladin 6: the saver in the aura of a Paladin (itself included)
		n, who := auraOfProtection(c.lobby, c.saver)
		return n, fmt.Sprintf("Aura of Protection +%d (%s)", n, who)
	},
	func(c *saveCtx) (int, string) { // Ring and Cloak of Protection, Stone of Good Luck, Staff of Power, Luck Blade
		if c.saver <= 0 {
			return 0, ""
		}
		n := magicSaveBonus(c.saver)
		return n, fmt.Sprintf("magic items +%d", n)
	},
	func(c *saveCtx) (int, string) { // Warding Bond
		if c.saver <= 0 {
			return 0, ""
		}
		return buffSaveBonus(c.saver), "Warding Bond +1"
	},
	func(c *saveCtx) (int, string) { // cover: its bonus to AC is also a bonus to Dexterity saves (the most protective source only)
		if c.saver <= 0 || !c.is("dex") {
			return 0, ""
		}
		var cover int
		db.QueryRow(`SELECT COALESCE(cover_bonus,0) FROM characters WHERE id = $1`, c.saver).Scan(&cover)
		if h := halfCoverBonus(c.lobby, c.saver); h > cover {
			cover = h
		}
		return cover, fmt.Sprintf("cover +%d", cover)
	},
	func(c *saveCtx) (int, string) { // Exhaustion: -2 per level on every D20 Test, whatever the saver's kind (exhaustion_any_2024.go)
		if c.saver == 0 {
			return 0, ""
		}
		p := game.ExhaustionD20Penalty(exhaustionLevelOfAny(c.lobby, c.saver))
		if p == 0 {
			return 0, ""
		}
		return -p, fmt.Sprintf("Exhaustion -%d", p)
	},
}

// barbarianLevel: the character's Barbarian level.
func barbarianLevel(charID int) int {
	class, _, level := subclassOf(charID)
	return classLevelIn(charID, class, level, "barbarian")
}

// auraOfProtection: the best Aura of Protection covering a character (SRD 5.2.1, Paladin 6): a conscious Paladin's Charisma
// modifier (at least +1) for the Paladin and its allies within 10 feet (30 feet at level 18). Only one aura counts. Without a
// battle map the party stands together. It returns the bonus and the Paladin's name.
func auraOfProtection(lobbyID, charID int) (int, string) {
	if charID <= 0 || lobbyID == 0 || db == nil {
		return 0, ""
	}
	rows, err := db.Query(`SELECT id, name, COALESCE(cha,10), COALESCE(level,1), COALESCE(class,'') FROM characters
		WHERE lobby_id = $1 AND (LOWER(class) = 'paladin' OR COALESCE(class_levels::text,'') ILIKE '%paladin%')`, lobbyID)
	if err != nil {
		return 0, ""
	}
	type paladin struct {
		id, cha, level int
		name, class    string
	}
	var ps []paladin
	for rows.Next() {
		var p paladin
		if rows.Scan(&p.id, &p.name, &p.cha, &p.level, &p.class) == nil {
			ps = append(ps, p)
		}
	}
	rows.Close()
	best, who := 0, ""
	for _, p := range ps {
		pl := classLevelIn(p.id, p.class, p.level, "paladin")
		if pl < 6 || isIncapacitated(p.id) || !auraReaches(lobbyID, p.id, charID, pl) {
			continue
		}
		bonus := game.Modifier(p.cha)
		if bonus < 1 {
			bonus = 1
		}
		if bonus > best {
			best, who = bonus, p.name
		}
	}
	return best, who
}
