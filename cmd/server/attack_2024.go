package main

// One attack function.
//
// Every attack roll in the server is made here: a monster's stat-block attack, a monster's spell attack, an Opportunity
// Attack, a character's weapon or spell attack, a companion's, an item's. The rule of the roll itself (Advantage and
// Disadvantage cancel, a natural 1 and a natural 20, Critical Hits, the wording of the result line) is game.AttackRoll
// (game/attack.go). This file is the server's side of it:
//
//   - attackSources is the ONLY list of what gives an attack roll Advantage or Disadvantage. A rule added to it reaches
//     every attack by every kind of creature. Do not test for Advantage or Disadvantage anywhere else.
//   - creatureConditions and armorClassOf are the only places an attack reads a creature's conditions and Armor Class.
//   - attackCtx.blocked is the only place an attack is stopped before the roll (Sanctuary, the Ethereal Plane).
//   - attackCtx.roll gathers all of that, rolls, applies what changes the die (Halfling Luck) and records what the hit
//     means for later rolls (Multiattack Defense).
//
// A caller says who attacks whom and how (attackCtx), calls blocked and roll, and then does only its own damage and effects.
// Before Nov 14 2026 there were about 25 separate copies of this logic, each with its own subset of the rules.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// attackCtx is one attack roll about to be made.
type attackCtx struct {
	lobby    int
	attacker int    // a character's id (> 0); a monster's or companion's combat id (< 0); 0 when it has no place in the turn order
	target   int    // the same for the target; 0 when it is unknown
	name     string // the attack's name on the result line ("Scimitar", "Ray of Sickness")
	bonus    int    // the attack bonus

	melee           bool // made within reach; false: a ranged attack
	spell           bool // a spell attack
	weapon          bool // made with a weapon or an Unarmed Strike (it uses Strength or Dexterity)
	strength        bool // a Strength-based attack roll: the attack uses Strength (a weakened creature has Disadvantage on it, effect_records_2024.go)
	opportunity     bool // an Opportunity Attack
	longRange       bool // beyond the attack's normal range (the caller knows the attack's ranges)
	within5         *bool
	desc            string // the player's description of the action, where a rule reads it (close_range, the Oathbow's "swift death")
	statBlockAttack string // the stat-block wording for attack-specific roll modifiers
	weaponKey       string // the weapon used, where a rule reads it (the Oathbow)
	piercing        bool   // the weapon deals Piercing damage (underwater)
	closeRange      bool   // the player said an enemy is within 5 feet
	allySaid        bool   // the attacker said an ally is within 5 feet of the target (used only without a battle map: ally_adjacent_2024.go)

	ac       *int // the Armor Class to use when the caller holds the target itself (a companion, an object); else armorClassOf
	critFrom int  // the lowest natural roll that is a Critical Hit; 0: 20
	autoHit  bool // the attack's text says it hits automatically

	forgoAdvantage bool     // the attacker gives up any Advantage on this roll (Brutal Strike)
	extraAdv       []string // sources of Advantage only this caller can know
	extraDis       []string // sources of Disadvantage only this caller can know

	attackerConds []string // filled by gather
	targetConds   []string
	oathAdv       *bool // the Oathbow's ruling for this attack, asked once
	oathDis       bool
}

// oathbow: the Oathbow's effect on this attack: Advantage against the sworn enemy, Disadvantage with any other weapon while it
// lives (items_c4_2024.go). Asked once per attack: saying "swift death" names the sworn enemy.
func (c *attackCtx) oathbow() (adv, dis bool) {
	if c.oathAdv == nil {
		a, d := false, false
		if c.attacker > 0 && c.weapon {
			a, d, _ = oathbowRoll(c.attacker, c.weaponKey, c.desc)
		}
		c.oathAdv, c.oathDis = &a, d
	}
	return *c.oathAdv, c.oathDis
}

// hasCond: the condition is in the list, in any letter case, with or without a ":detail" suffix.
func hasCond(conds []string, name string) bool {
	for _, c := range conds {
		c = strings.ToLower(c)
		if c == name || strings.HasPrefix(c, name+":") {
			return true
		}
	}
	return false
}

// creatureConditions: the conditions of a creature as an attack roll sees them. A character at 0 Hit Points is Unconscious and
// Prone (SRD 5.2.1 "Falling to 0 Hit Points"); a creature outlined by Faerie Fire gets nothing from being Invisible.
func creatureConditions(lobbyID, id int) []string {
	var conds []string
	switch {
	case id > 0 && !isCompanionCombatID(id):
		refreshConcentrationConditions(id) // Invisibility whose caster lost Concentration
		conds = getCharConditions(id)      // the character's condition records, as its list (condition_owner_2024.go)
		if charAtZeroHP(id) {
			for _, c := range []string{"unconscious", "prone"} {
				if !hasCond(conds, c) {
					conds = append(conds, c)
				}
			}
		}
	case id < 0, isCompanionCombatID(id): // a companion's records are kept like a monster's (the condition owner), and were not read here: a Paralyzed companion gave attackers nothing
		conds = append(conditionNames(lobbyID, id), combatEffectNames(lobbyID, id)...)
	}
	return withoutInvisible(lobbyID, id, conds)
}

// armorClassOf: a creature's Armor Class against an attack: its own, what spells change (Shield, Shield of Faith, Haste, Slow,
// Warding Bond) and, for a character, cover (the most protective source only).
func armorClassOf(lobbyID, id int) int {
	switch {
	case id >= companionIDBase: // a companion: its own Armor Class (before this branch the characters table was read: AC 0, no row)
		if c, ok := activeCompanionInCampaign(lobbyID, id); ok {
			return c.AC + halfCoverBonus(lobbyID, id) + spellACAdjust(lobbyID, id)
		}
	case id > 0:
		var ac, cover int
		db.QueryRow(`SELECT COALESCE(ac,10), COALESCE(cover_bonus,0) FROM characters WHERE id = $1`, id).Scan(&ac, &cover)
		if h := halfCoverBonus(lobbyID, id); h > cover {
			cover = h
		}
		return ac + cover + spellACAdjust(lobbyID, id)
	case id < 0:
		for _, e := range combatEntries(lobbyID) {
			if asInt(e["id"]) == id {
				return asInt(e["ac"]) + spellACAdjust(lobbyID, id)
			}
		}
	}
	return 10
}

// withinFive: the attacker is within 5 feet of the target. The battle map decides when both are on it; without one a melee
// attack is within 5 feet, and a character's ranged attack is when the character has said an enemy is next to it.
func (c *attackCtx) withinFive() bool {
	if c.within5 != nil {
		return *c.within5
	}
	if c.attacker != 0 && c.target != 0 {
		if d, ok := creatureDistance(c.lobby, c.attacker, c.target); ok {
			return d <= 5
		}
	}
	if c.melee {
		return true
	}
	return c.attacker > 0 && isInMeleeRange(c.attacker)
}

// seesUnseen: the creature perceives an Invisible or hidden creature anyway (Blindsight, Truesight, See Invisibility, True
// Seeing, a Ranger's Feral Senses).
func seesUnseen(lobbyID, id int) bool {
	switch {
	case id > 0:
		_, blind, truesight := getCharacterVision(id)
		return blind > 0 || truesight > 0 || buffSeesInvisible(id) || hasFeralSenses(id)
	case id < 0:
		return monsterSeesThrough(lobbyID, id)
	}
	return false
}

func (c *attackCtx) monsterKey(id int) string {
	if id >= 0 {
		return ""
	}
	if e, _, ok := monsterCombatant(c.lobby, id); ok {
		return fmt.Sprint(e["monster_key"])
	}
	return ""
}

// attackSource is one thing that gives an attack roll Advantage or Disadvantage.
type attackSource struct {
	adv     bool                              // true: Advantage; false: Disadvantage
	why     string                            // the words shown for it
	applies func(c *attackCtx) (bool, string) // whether it applies to this attack; a non-empty string replaces why
}

func yes(b bool) (bool, string) { return b, "" }

// attackSources: every source of Advantage and Disadvantage on an attack roll (SRD 5.2.1). Each row decides from the attacker,
// the target and the kind of attack whether it applies; a row that is used up by the attack (Help, a GM's grant, Sap) is used
// up here and nowhere else.
var woundedTargetAdvantage = regexp.MustCompile(`(?i)\bwith Advantage if the target doesn['’]t have all its Hit Points\b`)

// targetMissingHitPoints reads current and maximum Hit Points from the target's
// owner. Temporary Hit Points do not count toward either amount.
func targetMissingHitPoints(lobby, target int) bool {
	switch {
	case isCompanionCombatID(target):
		c, ok := activeCompanionInCampaign(lobby, target)
		return ok && c.MaxHP > 0 && c.HP < c.MaxHP
	case target < 0:
		v := monsterEntryVitals(lobby, target)
		return v.Max > 0 && v.HP < v.Max
	case target > 0:
		var hp, maximum int
		err := db.QueryRow(`SELECT hp,max_hp FROM characters WHERE id=$1 AND lobby_id=$2`, target, lobby).Scan(&hp, &maximum)
		return err == nil && maximum > 0 && hp < maximum
	}
	return false
}

var attackSources = []attackSource{
	{true, "target is missing Hit Points", func(c *attackCtx) (bool, string) {
		header, _, _ := strings.Cut(c.statBlockAttack, "Hit:")
		return yes(woundedTargetAdvantage.MatchString(header) && targetMissingHitPoints(c.lobby, c.target))
	}},
	// ---- Advantage: the attacker's traits
	{true, "Pack Tactics", func(c *attackCtx) (bool, string) { // an ally of the monster, not Incapacitated, within 5 feet of the target
		return yes(c.attacker < 0 && monsterHasTrait(c.lobby, c.attacker, "Pack Tactics") && allyNextToTarget(c.lobby, c.attacker, c.target, c.allySaid))
	}},
	{true, "its own rider (Advantage on the next attack roll it makes this turn)", func(c *attackCtx) (bool, string) {
		return yes(c.attacker != 0 && c.lobby > 0 && takeCondMarker(c.lobby, c.attacker, "adv_next_attack"))
	}},
	// ---- Advantage: the target's state
	{true, "", func(c *attackCtx) (bool, string) { // Blinded, Paralyzed, Petrified, Restrained, Stunned, Unconscious; Prone within 5 feet
		var why []string
		for _, cond := range []string{"blinded", "paralyzed", "petrified", "restrained", "stunned", "unconscious"} {
			if hasCond(c.targetConds, cond) {
				why = append(why, titleCase(cond))
			}
		}
		if hasCond(c.targetConds, "prone") && c.withinFive() {
			why = append(why, "Prone")
		}
		return len(why) > 0, "the target is " + strings.Join(why, ", ")
	}},
	{true, "the target attacked recklessly", func(c *attackCtx) (bool, string) { return yes(hasCond(c.targetConds, "reckless")) }}, // Reckless Attack
	{true, "Faerie Fire", func(c *attackCtx) (bool, string) { return yes(c.target != 0 && outlined(c.lobby, c.target)) }},
	{true, "Help", func(c *attackCtx) (bool, string) { // the Help action: the next attack roll against the creature by an ally of the helper
		return yes(c.target != 0 && takeCombatEffect(c.lobby, c.target, "assisted", c.attacker))
	}},
	{true, "the target is exposed", func(c *attackCtx) (bool, string) { // a saved Stunning Strike, Guiding Bolt: the next attack roll against it
		return yes(c.target != 0 && takeCombatEffect(c.lobby, c.target, "exposed", 0))
	}},
	// ---- Advantage: the attacker's state
	{true, "the target can't see the attacker", func(c *attackCtx) (bool, string) { // an Invisible or hidden attacker, or one the dark hides
		return yes((hasCond(c.attackerConds, "invisible") || hasCond(c.attackerConds, "hidden")) && !seesUnseen(c.lobby, c.target) ||
			c.target != 0 && c.lobby > 0 && (areaBlocksSight(c.lobby, c.target, c.attacker) ||
				!creatureSeesInLighting(c.lobby, c.target, monsterLightingAt(c.lobby, c.attacker), c.distanceFeet())))
	}},
	{true, "Aura of Authority", func(c *attackCtx) (bool, string) { return yes(c.attacker < 0 && auraOfAuthority(c.lobby, c.attacker)) }}, // Hobgoblin Captain
	{true, "", func(c *attackCtx) (bool, string) { // Blood Frenzy, Bloodied Fury, Bloodied Frenzy: the attacker's own trait (rollTraitAdvantage)
		if c.attacker == 0 || (c.attacker > 0 && !isCompanionCombatID(c.attacker)) {
			return false, ""
		}
		name := rollTraitAdvantage(c.lobby, c.attacker, false, c.melee, c.target)
		return name != "", name
	}},
	{true, "it is attached to the target", func(c *attackCtx) (bool, string) { // "While attached to a target, it ... has Advantage on its attack rolls" (the attach owner's rider, monster_attach_2024.go)
		if c.attacker == 0 || c.target == 0 {
			return false, ""
		}
		tid, ok := attachOnlyTarget(c.lobby, c.attacker)
		return yes(ok && tid == c.target)
	}},
	{true, "Foresight", func(c *attackCtx) (bool, string) { return yes(c.attacker > 0 && buffAttackAdvantage(c.attacker)) }},
	{true, "the Jester card", func(c *attackCtx) (bool, string) {
		if c.attacker <= 0 {
			return false, ""
		}
		a, _ := deckD20(c.attacker)
		return yes(a)
	}},
	{true, "the GM's ruling", func(c *attackCtx) (bool, string) { // POST /api/gm/grant-advantage: the character's next attack roll
		return yes(c.attacker > 0 && takeCombatEffect(c.lobby, c.attacker, "gm_advantage", 0))
	}},
	// ---- Advantage: a character's features and items
	{true, "Vex", func(c *attackCtx) (bool, string) { // the Vex mastery: the next attack roll against the creature it hit
		if c.attacker <= 0 {
			return false, ""
		}
		if v, ok := vexMarks.Load(c.attacker); ok && v.(vexMark).Target == c.target {
			vexMarks.Delete(c.attacker)
			return true, ""
		}
		return false, ""
	}},
	{true, "Steady Aim", func(c *attackCtx) (bool, string) { return yes(c.attacker > 0 && takeSteadyAim(c.attacker)) }},     // Rogue: the next attack roll this turn
	{true, "Precise Hunter", func(c *attackCtx) (bool, string) { return yes(c.attacker > 0 && preciseHunter(c.attacker)) }}, // Ranger 17: against the creature marked by Hunter's Mark
	{true, "Grappler", func(c *attackCtx) (bool, string) { return yes(c.attacker > 0 && grapplerAdvantage(c.attacker)) }},   // the feat: against a creature the attacker has Grappled
	{true, "Ring of Elemental Command", func(c *attackCtx) (bool, string) { // the wearer attacks an Elemental of the ring's plane
		return yes(c.attacker > 0 && c.target < 0 && elementalBane(c.attacker, c.monsterKey(c.target)))
	}},
	{true, "the Oathbow's sworn enemy", func(c *attackCtx) (bool, string) {
		a, _ := c.oathbow()
		return yes(a)
	}},

	// ---- Disadvantage: the attacker's state
	{false, "", func(c *attackCtx) (bool, string) { // Blinded, Frightened, Poisoned, Prone, Restrained
		var why []string
		for _, cond := range []string{"blinded", "frightened", "poisoned", "prone", "restrained"} {
			if hasCond(c.attackerConds, cond) {
				why = append(why, titleCase(cond))
			}
		}
		return len(why) > 0, "the attacker is " + strings.Join(why, ", ")
	}},
	{false, "the attacker can't see in this light", func(c *attackCtx) (bool, string) {
		// The unseen-target leg of the shared model: the opaque emanation (either endpoint inside,
		// or the line crossing it) or the light on the target's square leaves the target unseen -
		// for a monster attacker too, with its senses and the map distance (creatureSeesInLighting).
		// The matching unseen-attacker Advantage lives above, so two creatures unable to see each
		// other cancel to a straight roll (SRD 5.2.1 "Blinded").
		if c.attacker == 0 || c.lobby <= 0 {
			return false, ""
		}
		if areaBlocksSight(c.lobby, c.attacker, c.target) { // Magical Darkness, fog, ink (obscured_areas_2024.go)
			return yes(true)
		}
		return yes(!creatureSeesInLighting(c.lobby, c.attacker, monsterLightingAt(c.lobby, c.target), c.distanceFeet()))
	}},
	{false, "Sunlight Sensitivity", func(c *attackCtx) (bool, string) { // the attacker's own light trait, in its light (creature_senses_2024.go)
		p := lightPenaltyNow(c.lobby, c.attacker)
		return p.Attacks, p.Trait
	}},
	{false, "the attacker is Grappled by another creature", func(c *attackCtx) (bool, string) {
		return yes(c.attacker != 0 && c.target != 0 && grappledAttackPenalty(c.lobby, c.attacker, c.target))
	}},
	{false, "Sap", func(c *attackCtx) (bool, string) {
		return yes(c.attacker != 0 && takeCombatEffect(c.lobby, c.attacker, "sap", 0))
	}}, // the Sap mastery: its next attack roll
	{false, "a Disadvantage rider on its next attack roll", func(c *attackCtx) (bool, string) { // "Disadvantage on the next attack roll it makes before the end of its next turn": used up
		return yes(c.attacker != 0 && c.lobby > 0 && takeCondMarker(c.lobby, c.attacker, "disadv_next_attack"))
	}},
	{false, "a Disadvantage rider on its attack rolls", func(c *attackCtx) (bool, string) { // "Disadvantage on attack rolls until the end of its next turn"
		return yes(c.attacker != 0 && c.lobby > 0 && hasCondMarker(c.lobby, c.attacker, "disadv_attacks"))
	}},
	{false, "", func(c *attackCtx) (bool, string) {
		reason := damageDisadvantageForAttack(c.lobby, c.attacker)
		return reason != "", reason
	}},
	{false, "armor the attacker isn't trained in", func(c *attackCtx) (bool, string) { // any D20 Test that uses Strength or Dexterity
		return yes(c.attacker > 0 && c.weapon && isWearingNonProficientArmor(c.attacker))
	}},
	{false, "the Fool card", func(c *attackCtx) (bool, string) {
		if c.attacker <= 0 {
			return false, ""
		}
		_, d := deckD20(c.attacker)
		return yes(d)
	}},
	{false, "the GM's ruling", func(c *attackCtx) (bool, string) {
		return yes(c.attacker > 0 && takeCombatEffect(c.lobby, c.attacker, "gm_disadvantage", 0))
	}},
	{false, "another weapon while the Oathbow's sworn enemy lives", func(c *attackCtx) (bool, string) {
		_, d := c.oathbow()
		return yes(d)
	}},
	{false, "underwater", func(c *attackCtx) (bool, string) { // a weapon attack underwater: ranged, or melee without Piercing damage or a Swim Speed
		if c.attacker <= 0 || !c.weapon || !isUnderwaterCombat(c.lobby) {
			return false, ""
		}
		return yes(!c.melee || (!c.piercing && !hasSwimSpeed(c.attacker)))
	}},
	// ---- Disadvantage: range
	{false, "long range", func(c *attackCtx) (bool, string) { return yes(c.longRange) }},
	{false, "a ranged attack with an enemy within 5 feet", func(c *attackCtx) (bool, string) {
		switch {
		case c.melee:
			return false, ""
		case c.attacker < 0:
			return yes(characterAdjacentToMonster(c.lobby, c.attacker))
		case c.attacker > 0:
			return yes(c.closeRange || enemyWithinFive(c.lobby, c.attacker, c.desc))
		}
		return false, ""
	}},
	// ---- Disadvantage: the target's state and defenses
	{false, "the attacker can't see the target", func(c *attackCtx) (bool, string) {
		return yes(hasCond(c.targetConds, "invisible") && !seesUnseen(c.lobby, c.attacker))
	}},
	{false, "the target is Prone and more than 5 feet away", func(c *attackCtx) (bool, string) {
		return yes(hasCond(c.targetConds, "prone") && !c.withinFive())
	}},
	{false, "the target is Dodging", func(c *attackCtx) (bool, string) { return yes(c.target > 0 && dodgeActive(c.target)) }}, // the Dodge action (dodge_2024.go)
	{false, "", func(c *attackCtx) (bool, string) { // Blurred Form: the target's own trait (attackedWithDisadvantage)
		if c.lobby <= 0 || c.target == 0 {
			return false, ""
		}
		name := attackedWithDisadvantage(c.lobby, c.target, c.targetConds)
		return name != "", name
	}},
	{false, "Foresight", func(c *attackCtx) (bool, string) {
		if c.target <= 0 {
			return false, ""
		}
		_, ok := buffEffect(c.lobby, c.target, "foresight")
		return yes(ok)
	}},
	{false, "Blur", func(c *attackCtx) (bool, string) {
		if c.target <= 0 {
			return false, ""
		}
		_, ok := buffEffect(c.lobby, c.target, "blur")
		return yes(ok && !seesUnseen(c.lobby, c.attacker)) // Blindsight and Truesight see through it
	}},
	{false, "Holy Aura", func(c *attackCtx) (bool, string) {
		if c.target <= 0 {
			return false, ""
		}
		ok, _ := holyAuraOn(c.lobby, c.target)
		return yes(ok)
	}},
	{false, "Protection from Evil and Good", func(c *attackCtx) (bool, string) {
		if c.target <= 0 || c.attacker >= 0 {
			return false, ""
		}
		_, ok := buffEffect(c.lobby, c.target, "protection_from_evil_and_good")
		return yes(ok && evilGoodTypes[strings.ToLower(monsterTypeOf(c.lobby, c.attacker))])
	}},
	{false, "Magic Circle", func(c *attackCtx) (bool, string) {
		if c.target <= 0 {
			return false, ""
		}
		ok, _ := moreAttackDisadvantage(c.lobby, c.attacker, c.target)
		return yes(ok)
	}},
	{false, "Robe of Scintillating Colors", func(c *attackCtx) (bool, string) { return yes(c.target > 0 && robeDazzle(c.lobby, c.target)) }},
	{false, "Cloak of Displacement", func(c *attackCtx) (bool, string) { return yes(c.target > 0 && cloakOfDisplacement(c.lobby, c.target)) }},
	{false, "Ring of Elemental Command", func(c *attackCtx) (bool, string) { // an Elemental attacks the wearer with Disadvantage
		return yes(c.target > 0 && c.attacker < 0 && elementalBane(c.target, c.monsterKey(c.attacker)))
	}},
	{false, "Escape the Horde", func(c *attackCtx) (bool, string) { // Hunter: Opportunity Attacks against you have Disadvantage
		return yes(c.opportunity && c.target > 0 && hunterDefense(c.target) == "escape_the_horde")
	}},
	{false, "Multiattack Defense", func(c *attackCtx) (bool, string) { // Hunter: a creature that hit you has Disadvantage on its other attack rolls against you this turn
		return yes(c.target > 0 && c.attacker != 0 && multiattackDefenseApplies(c.target, c.attacker))
	}},
}

// distanceFeet: the map distance between attacker and target, 0 when the combat has no map (the
// vision helpers then count any positive range, the pre-existing convention).
func (c *attackCtx) distanceFeet() int {
	m, ok := activeEncounterMap(c.lobby)
	if !ok {
		return 0
	}
	a, okA := m.token(c.attacker)
	b, okB := m.token(c.target)
	if !okA || !okB {
		return 0
	}
	return tokenDistanceFeet(a, b)
}

// gather collects everything the attack rule needs: both sides' conditions, the Armor Class, every source of Advantage and
// Disadvantage, what is added to the roll, and whether a hit is automatically a Critical Hit.
func (c *attackCtx) gather() game.AttackRoll {
	if c.attacker > 0 && c.target < 0 { // some feature helpers (Precise Hunter, Grappler, the Oathbow) read the attacked monster from here
		if prev, had := attackTargets.Load(c.attacker); !had || prev != c.target {
			attackTargets.Store(c.attacker, c.target)
			if had {
				defer attackTargets.Store(c.attacker, prev)
			} else {
				defer attackTargets.Delete(c.attacker)
			}
		}
	}
	if c.attacker != 0 {
		c.attackerConds = creatureConditions(c.lobby, c.attacker)
	}
	if c.target != 0 {
		c.targetConds = creatureConditions(c.lobby, c.target)
	}
	a := game.AttackRoll{Bonus: c.bonus, CritFrom: c.critFrom, AutoHit: c.autoHit}
	switch {
	case c.ac != nil:
		a.AC = *c.ac
	case c.target != 0:
		a.AC = armorClassOf(c.lobby, c.target)
	}
	for _, s := range attackSources {
		ok, why := s.applies(c)
		if !ok {
			continue
		}
		if why == "" {
			why = s.why
		}
		if s.adv {
			a.Advantage = append(a.Advantage, why)
		} else {
			a.Disadvantage = append(a.Disadvantage, why)
		}
	}
	a.Advantage = append(a.Advantage, c.extraAdv...)
	a.Disadvantage = append(a.Disadvantage, c.extraDis...)
	if c.attacker != 0 && c.strength && weakenedOn(c.lobby, c.attacker) { // Weakening Breath: Disadvantage on Strength-based D20 Tests
		a.Disadvantage = append(a.Disadvantage, "weakened (a Strength-based D20 Test)")
	}
	// Elusive (Rogue 18): no attack roll has Advantage against the Rogue unless the Rogue is Incapacitated
	if c.target > 0 && len(a.Advantage) > 0 && hasElusive(c.target) && !isIncapacitated(c.target) {
		a.Advantage = nil
	}
	if c.forgoAdvantage {
		a.Advantage = nil
	}
	// what is added to the roll: Bless and Bane on the attacker, and a character's Exhaustion (2 per level on every D20 Test)
	if c.attacker != 0 {
		if adj, note := spellRollAdjust(c.lobby, c.attacker, "attack"); note != "" {
			a.Adjust = append(a.Adjust, game.RollAdjust{Amount: adj, Why: strings.Trim(note, " []")})
		}
	}
	if c.attacker != 0 {
		if n, why := d20MinusRoll(c.lobby, c.attacker); n > 0 { // Giggling Magic: a die subtracted from each attack roll
			a.Adjust = append(a.Adjust, game.RollAdjust{Amount: -n, Why: why})
		}
	}
	if c.attacker != 0 { // every D20 Test: 2 per Exhaustion level, whatever the attacker's kind (exhaustion_any_2024.go)
		if p := game.ExhaustionD20Penalty(exhaustionLevelOfAny(c.lobby, c.attacker)); p > 0 {
			a.Adjust = append(a.Adjust, game.RollAdjust{Amount: -p, Why: fmt.Sprintf("Exhaustion -%d", p)})
		}
	}
	if c.attacker > 0 {
		if c.weapon && c.target < 0 {
			if n := maceOfSmitingAttack(c.attacker, c.monsterKey(c.target)); n != 0 { // +3 in all against a Construct
				a.Adjust = append(a.Adjust, game.RollAdjust{Amount: n, Why: fmt.Sprintf("Mace of Smiting +%d against a Construct", n)})
			}
		}
	}
	if c.target != 0 && takeCombatEffect(c.lobby, c.target, "sundering", c.attacker) { // Sundering Blow: the next attack roll against it by another creature
		a.Adjust = append(a.Adjust, game.RollAdjust{Amount: 5, Why: "Sundering Blow +5"})
	}
	// a hit on a Paralyzed or Unconscious creature is a Critical Hit when the attacker is within 5 feet
	a.AutoCrit = (hasCond(c.targetConds, "paralyzed") || hasCond(c.targetConds, "unconscious")) && c.withinFive()
	return a
}

// blocked: the attack can't be made at this target. lost says the attack is used up (a failed save against Sanctuary); note
// is the reason, or what to add to the result when the attack goes ahead (a Sanctuary save that succeeded).
func (c *attackCtx) blocked() (lost bool, note string) {
	if c.target == 0 {
		return false, ""
	}
	if why := etherealBlock(c.lobby, c.target); why != "" {
		return true, why
	}
	if c.attacker < 0 && c.target > 0 {
		return sanctuaryBlocks(c.lobby, c.attacker, c.attackerName(), c.target)
	}
	if c.attacker > 0 && c.target > 0 {
		if msg, ok := checkSanctuaryProtection(c.attacker, c.target); msg != "" {
			return true, msg
		} else if ok != "" {
			return false, ok
		}
	}
	return false, ""
}

func (c *attackCtx) attackerName() string {
	if c.attacker != 0 {
		return creatureName(c.lobby, c.attacker)
	}
	return "The attacker"
}

// roll makes the attack roll. It returns the roll as gathered, its outcome, and a note for anything that changed the die.
func (c *attackCtx) roll() (game.AttackRoll, game.AttackOutcome, string) {
	chargeAtRoll(c.lobby, c.attacker, c.target) // the attacker's move right before this attack (a charge; creature_movement_2024.go)
	a := c.gather()
	o := a.Roll()
	note := ""
	if c.attacker > 0 && o.Natural == 1 && !o.AutoHit { // Halfling Luck: reroll a 1 on the d20 of a D20 Test, and use the new roll
		if rerolled, used, was := applyHalflingLucky(o.Natural, c.attacker); used {
			o = a.Judge(rerolled)
			note = fmt.Sprintf(" 🍀[Lucky: %d→%d]", was, rerolled)
		}
	}
	if c.attacker != 0 && hasCond(c.attackerConds, "hidden") { // the attack roll gave its Advantage; now the attacker is found (hide_2024.go)
		endHidden(c.lobby, c.attacker)
	}
	if o.Hit && c.target > 0 && c.attacker != 0 && hasMultiattackDefense(c.target) {
		recordMultiattackDefenseHit(c.target, c.attacker) // its other attack rolls against the Hunter this turn have Disadvantage
	}
	return a, o, note
}

// characterSpellAttackBonus: a character's spell attack bonus: Proficiency Bonus, the spellcasting ability modifier and magic
// items (Wand of the War Mage, staffs, Robe of the Archmagi). A Spell Scroll being read uses its own bonus instead.
func characterSpellAttackBonus(charID, charLevel, spellMod int) int {
	if v, ok := itemCastAttack.Load(charID); ok {
		return v.(int)
	}
	return game.ProficiencyBonus(charLevel) + spellMod + magicSpellAttackBonus(charID)
}

// spellAttackIsMelee: the spell's attack is made within reach (Touch or 5 feet), not at range.
func spellAttackIsMelee(rangeText string) bool {
	ft := spellRangeFeet(rangeText)
	return ft >= 0 && ft <= 5
}

// characterWeaponAttack describes a character's attack with a weapon or an Unarmed Strike at whatever it is attacking (the
// monster chosen with target_id). Every weapon attack a character makes starts here: the main attack, the off-hand attack,
// Horde Breaker, a Monk's strikes, Cleave, an Opportunity Attack, Retaliation. bonus is the attack bonus with that weapon.
func characterWeaponAttack(charID, bonus int, w SRDWeapon, hasWeapon bool, weaponKey, description string) attackCtx {
	lobby := lobbyOfChar(charID)
	c := attackCtx{lobby: lobby, attacker: charID, name: "Attack", bonus: bonus, weapon: true, desc: description, weaponKey: weaponKey}
	c.melee = !(hasWeapon && usedAsRanged(w, description)) // a ranged weapon, or a thrown one, is a ranged attack
	c.piercing = hasWeapon && strings.EqualFold(w.DamageType, "piercing")
	d := strings.ToLower(description)
	c.closeRange = strings.Contains(d, "close range") || strings.Contains(d, "in melee") || strings.Contains(d, "within 5") ||
		strings.Contains(d, "point blank") || strings.Contains(d, "point-blank")
	if _, mid, ok := attackedMonster(charID); ok {
		c.target = mid
		c.longRange = hasWeapon && w.Type == "ranged" && attackAtLongRange(lobby, charID, mid, description)
	}
	c.critFrom = characterCritFrom(charID)
	return c
}

// characterCritFrom: the lowest natural roll that is a Critical Hit for the character's weapon attacks and Unarmed Strikes (a
// Champion's Improved and Superior Critical).
func characterCritFrom(charID int) int {
	var subclass string
	var level int
	db.QueryRow(`SELECT COALESCE(subclass,''), COALESCE(level,1) FROM characters WHERE id = $1`, charID).Scan(&subclass, &level)
	return game.CriticalHitRange(subclass, level)
}

// critNoTarget: with no target the server knows, the GM judges the hit; the damage dice are doubled for a natural roll in the
// character's critical range.
func (c *attackCtx) critNoTarget(o game.AttackOutcome) bool {
	from := c.critFrom
	if from <= 0 {
		from = 20
	}
	return o.Natural != 1 && o.Natural >= from
}

// critDice: the dice as rolled for the text of a result: doubled in number on a Critical Hit ("3d6" -> "6d6").
func critDice(dice string, crit bool) string {
	if !crit {
		return dice
	}
	var n, sides int
	if _, err := fmt.Sscanf(dice, "%dd%d", &n, &sides); err != nil || n <= 0 {
		return dice + " doubled"
	}
	return fmt.Sprintf("%dd%d", 2*n, sides)
}
