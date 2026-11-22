package main

import (
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// check_2024.go: the one owner of what gives an ability check Advantage or Disadvantage (SRD 5.2.1), for a character, a monster or a
// companion, as saveSources owns a saving throw's and attackSources an attack's. A route builds a checkCtx, asks sources() and rolls
// with rollCheckD20; it never decides Advantage or Disadvantage itself and never rolls a check's d20 anywhere else
// (TestNoOtherCheckRoll). The bonus of a skill stays checkBonus's (actions_checks_2024.go), Exhaustion's -2 per level
// game.ExhaustionD20Penalty (checkExhaustion), Reliable Talent and the rolls' other adjustments the routes' own.
//
// Reading the sources USES UP what one check uses up (an ally's Help), as attackSources does for Help and Sap.

// checkCtx is one ability check as the sources see it.
type checkCtx struct {
	lobby   int    // the campaign (0: a character's own, found from its row)
	who     int    // the creature that rolls: a character (> 0), a monster (< 0) or a companion (its combat id)
	ability string // "str" or "strength", ...
	skill   string // the skill the check uses ("" for a bare ability check)
	help    string // what an ally's Help names (the skill, or the tool), default the skill
	// from the request (a character's check): the GM's own ruling, a target of a Charisma check, a move of at most half Speed
	gmAdvantage, gmDisadvantage bool
	targetID                    int
	halfSpeedMovement           bool
	// toolAndSkill: proficiency in both the tool and the skill a tool check uses (SRD 5.2.1: Advantage)
	toolAndSkill bool
	// escape: the check ends a Grappled condition; holder is the creature holding it (0 when not known)
	escape bool
	holder int
}

// checkWhy: every source that applied, in words (a reply says them, and says when Advantage and Disadvantage cancel).
type checkWhy struct {
	Adv, Dis []string
	Notes    string // the bracketed notes of the buff and item sources ("[Advantage: Foresight]")
}

func (w *checkWhy) adv(why string) { w.Adv = append(w.Adv, why) }
func (w *checkWhy) dis(why string) { w.Dis = append(w.Dis, why) }
func (w checkWhy) hasAdv(why string) bool {
	for _, a := range w.Adv {
		if a == why {
			return true
		}
	}
	return false
}
func (w checkWhy) hasDis(why string) bool {
	for _, d := range w.Dis {
		if d == why {
			return true
		}
	}
	return false
}

// Advantage and Disadvantage: whether the roll has either, once the sources say (they cancel to a normal roll in rollCheckD20).
func (w checkWhy) Advantage() bool    { return len(w.Adv) > 0 }
func (w checkWhy) Disadvantage() bool { return len(w.Dis) > 0 }

// escapeDisadvRe: a holder's own words that make escaping its grapple harder ("Ability checks made to escape this grapple have
// Disadvantage": SRD 5.2.1 Mimic).
var escapeDisadvRe = regexp.MustCompile(`(?i)Ability checks made to escape this grapple have Disadvantage`)

// sources decides Advantage and Disadvantage for the check. The rows common to every creature come first (conditions, riders,
// Sunlight Sensitivity, the holder's words); a character's own (class, subclass, species, items, spells, armor, Help, travel pace)
// follow.
func (c checkCtx) sources() checkWhy {
	var w checkWhy
	if c.gmAdvantage {
		w.adv("the GM's ruling")
	}
	if c.gmDisadvantage {
		w.dis("the GM's ruling")
	}
	lobby := c.lobby
	if lobby == 0 && c.who > 0 && !isCompanionCombatID(c.who) {
		lobby = lobbyOfChar(c.who)
	}
	// ---- every creature
	conds := creatureConditions(lobby, c.who)
	if game.HasCondition(conds, game.ConditionPoisoned) {
		w.dis("Poisoned")
	}
	if game.HasCondition(conds, game.ConditionFrightened) && frightenSourceInSight(lobby, c.who) {
		w.dis("Frightened (its source is in sight)")
	}
	if lobby > 0 && hasCondMarker(lobby, c.who, "disadv_checks") { // "Disadvantage on ... ability checks until ..." (disadvantage_riders_2024.go)
		w.dis("a Disadvantage rider on its ability checks")
	}
	if lobby > 0 && hasCondMarker(lobby, c.who, damageDisadvantageMarker) { // "If it takes Fire damage, it has Disadvantage on attack rolls and ability checks until ..."
		if t, ok := creatureHasTraitText(lobby, c.who, damageDisadvantageRe); ok {
			if m := damageDisadvantageRe.FindStringSubmatch(t.Text); m != nil && m[2] != "" {
				w.dis(t.Name)
			}
		}
	}
	if lobby > 0 {
		if on, _ := sunlightPenalty(lobby, c.who); on { // Sunlight Sensitivity, Vampire Weakness: every one of its wordings has ability checks
			w.dis("Sunlight Sensitivity")
		}
	}
	if c.escape && lobby > 0 && c.holder != 0 && holderMakesEscapeHarder(lobby, c.holder) {
		w.dis("its holder's own words (escaping this grapple has Disadvantage)")
	}
	if c.toolAndSkill {
		w.adv("proficiency in the tool and the skill")
	}
	if c.who <= 0 || isCompanionCombatID(c.who) {
		return w
	}
	// ---- a character
	id := c.who
	ability := strings.ToLower(strings.TrimSpace(c.ability))
	skill := c.skill
	if c.targetID > 0 && (ability == "cha" || ability == "charisma") && isCharmedBy(c.targetID, id) { // the charmer's Charisma checks at the charmed
		w.adv("Charmed target")
	}
	var subclass string
	var level int
	db.QueryRow(`SELECT COALESCE(subclass,''), COALESCE(level,1) FROM characters WHERE id=$1`, id).Scan(&subclass, &level)
	if skill == "athletics" && subclass != "" && hasSubclassFeature(subclass, level, "remarkable_athlete") { // Champion
		w.adv("Remarkable Athlete")
	}
	if skill == "stealth" && c.halfSpeedMovement && subclass != "" && hasSubclassFeature(subclass, level, "supreme_sneak") { // Thief
		w.adv("Supreme Sneak")
	}
	if bootsOfElvenkind(id, skill) { // F112 (rogue_fixes_2024.go)
		w.adv("Boots of Elvenkind / Cloak of the Bat")
	}
	if (ability == "str" || ability == "strength" || ability == "dex" || ability == "dexterity") && isWearingNonProficientArmor(id) {
		w.dis("armor without proficiency")
	}
	if skill == "stealth" && armorStealthDisadvantage(id) { // SRD 5.2.1 Armor, Stealth column
		w.dis("armor (Stealth Disadvantage)")
	}
	if largeFormAdvantage(id, ability) { // Goliath Large Form: Advantage on Strength checks
		w.adv("Large Form")
	}
	if c.escape && powerfulBuild(id) { // Goliath Powerful Build: Advantage on any check to end the Grappled condition
		w.adv("Powerful Build")
	}
	help := c.help
	if help == "" {
		help = skill
	}
	if takeHelpedCheck(id, help) { // an ally's Help (Assist an Ability Check): used up here
		w.adv("Help")
	}
	if pAdv, pDis, _ := travelPaceEffect(lobby, skill); pAdv { // Fast: Disadvantage on Perception, Survival, Stealth; Slow: Advantage on Perception, Survival
		w.adv("travel pace")
	} else if pDis {
		w.dis("travel pace")
	}
	if ok, why := itemSkillAdvantage(id, skill); ok { // Cloak of Elvenkind, Eyes of the Eagle, Robe of Eyes ... (F204)
		w.adv(why)
		w.Notes += " [Advantage: " + why + "]"
	}
	if ok, why := buffCheckAdvantage(id, ability); ok { // Foresight, Enhance Ability (buff_spells_2024.go)
		w.adv(why)
		w.Notes += " [Advantage: " + why + "]"
	}
	if dis, why := moreCheckDisadvantage(id, ability); dis { // Reduce (spells_more_2024.go)
		w.dis(why)
		w.Notes += " [Disadvantage: " + why + "]"
	}
	return w
}

// rollCheckD20 is the one d20 of an ability check: Advantage and Disadvantage cancel (SRD 5.2.1), final is the number that counts,
// r1 and r2 the dice rolled (r2 is 0 for a normal roll), kind says how it was rolled.
func rollCheckD20(adv, dis bool) (final, r1, r2 int, kind string) {
	switch {
	case adv && !dis:
		final, r1, r2 = game.RollWithAdvantage()
		return final, r1, r2, "advantage"
	case dis && !adv:
		final, r1, r2 = game.RollWithDisadvantage()
		return final, r1, r2, "disadvantage"
	}
	final = game.RollDie(20)
	return final, final, 0, "normal"
}

// checkExhaustion: the penalty Exhaustion puts on a creature's check (SRD 5.2.1: -2 per level, a D20 Test), any kind of creature.
func checkExhaustion(lobby, who int) int {
	return game.ExhaustionD20Penalty(exhaustionLevelOfAny(lobby, who))
}

// frightenSourceInSight: the creature is Frightened by a creature it can see (the condition's own record names it) on the battle map.
// Without a map or a named source nothing is assumed: the GM's ruling (the same rule as an ally next to the target).
func frightenSourceInSight(lobby, who int) bool {
	if lobby == 0 {
		return false
	}
	m, ok := activeEncounterMap(lobby)
	if !ok {
		return false
	}
	for _, r := range loadConditionRecords(lobby, who) {
		if r.Condition != "frightened" || r.SourceID == 0 {
			continue
		}
		a, okA := m.token(who)
		b, okB := m.token(r.SourceID)
		if okA && okB && m.canSee(a, b) {
			return true
		}
	}
	return false
}

// holderMakesEscapeHarder: the creature that holds a grapple says escaping it has Disadvantage (read from its stat block's text).
func holderMakesEscapeHarder(lobby, holder int) bool {
	if holder >= 0 {
		return false
	}
	_, meta, ok := monsterCombatant(lobby, holder)
	if !ok || meta == nil {
		return false
	}
	for _, g := range [][]srdMonsterEntry{meta.Traits, meta.Actions, meta.BonusActions, meta.Reactions} {
		for _, e := range g {
			if escapeDisadvRe.MatchString(e.Text) {
				return true
			}
		}
	}
	return false
}

// powerfulBuild: a Goliath (SRD 5.2.1 Powerful Build: Advantage on any ability check to end the Grappled condition).
func powerfulBuild(charID int) bool {
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(origin, '{}') FROM characters WHERE id = $1`, charID).Scan(&raw) != nil {
		return false
	}
	rec, ok := loadOriginRecord(raw)
	return ok && rec.Species.Key == "goliath"
}
