package main

// Each feature's own gate: who may use an action type, as the feature's resolver checks it before it does anything (class, class
// level and subclass; a feat, an invocation, a magic item in hand, a spell effect, a companion; the state the character is in). Each
// gate is a pure function of what its resolver reads, and the resolver calls it, so TestActionWhoMatchesGates (action_who_test.go)
// can ask the gate and the action table's Who and While (action_types_2024.go) the same question for every class at every level,
// every SRD subclass and every feat, invocation, item, effect, companion and state the table names: they must agree. A gate answers
// "" when the character may go on, else the refusal its resolver writes. Resources left, targets, range and what the turn has spent
// are not part of a gate: they are checked by the resolver after it.

import (
	"fmt"
	"strings"
	"time"
)

// featLookup, invocationLookup and effectLookup: what a gate asks about a character's feats, Eldritch Invocations and the spell
// effects on it, read from the database (the resolvers and actorFactsOf pass them).
func featLookup(charID int) func(string) bool {
	return func(slug string) bool { return hasSpecificFeat(charID, slug) }
}

func invocationLookup(charID int) func(string) bool {
	return func(slug string) bool { return hasInvocation(charID, slug) }
}

func effectLookup(charID int) func(string) bool {
	return func(kind string) bool { _, ok := charBuff(charID, kind); return ok }
}

// gateClass: a stored class as the gates compare it ("Fighter", "fighter").
func gateClass(class string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(class), " ", "_"))
}

// ---------------------------------------------------------------- Barbarian

// rageGate (Rage, Barbarian 1): barbarianLevel is the character's Barbarian level (classLevelIn).
func rageGate(class string, barbarianLevel int, raging bool) string {
	if gateClass(class) != "barbarian" && barbarianLevel <= 0 {
		return "Only barbarians can rage!"
	}
	if raging {
		return "You are already raging!"
	}
	return ""
}

// extendRageGate (Rage, Barbarian 1): a Bonus Action keeps your Rage going.
func extendRageGate(class string, raging bool) string {
	if gateClass(class) != "barbarian" {
		return "Only a Barbarian has a Rage to extend."
	}
	if !raging {
		return "You are not raging."
	}
	return ""
}

// endRageGate: ending your own Rage.
func endRageGate(class string, raging bool) string {
	if gateClass(class) != "barbarian" {
		return "Only a Barbarian has a Rage to end."
	}
	if !raging {
		return "You are not currently raging."
	}
	return ""
}

// frenzyGate (Path of the Berserker 3).
func frenzyGate(class, sub string, level int) string {
	if gateClass(class) != "barbarian" || strings.ToLower(sub) != "berserker" || level < 3 {
		return "Frenzy is a Path of the Berserker feature from Barbarian level 3."
	}
	return ""
}

// ---------------------------------------------------------------- Bard

func bardicInspirationGate(class string) string {
	if gateClass(class) != "bard" {
		return "Only a Bard has Bardic Inspiration."
	}
	return ""
}

func countercharmGate(class string, level int) string {
	if gateClass(class) != "bard" || level < 7 {
		return "Countercharm needs a Bard of level 7 or higher."
	}
	return ""
}

// bardicDieGate: using a Bardic Inspiration die you were given (anyone can hold one); it lasts an hour.
func bardicDieGate(die int, until *time.Time, now time.Time) string {
	if die == 0 {
		return "You have no Bardic Inspiration die."
	}
	if until != nil && now.After(*until) {
		return "Your Bardic Inspiration die has faded (it lasts an hour)."
	}
	return ""
}

// ---------------------------------------------------------------- Cleric

// turnUndeadGate: the refusal follows the Cleric's name.
func turnUndeadGate(class string, level int) string {
	if gateClass(class) != "cleric" || level < 2 {
		return "is not a Cleric of level 2 or higher."
	}
	return ""
}

func divineSparkGate(class string, level int) string {
	if gateClass(class) != "cleric" || level < 2 {
		return "Divine Spark is a Channel Divinity effect of a Cleric of level 2 or higher."
	}
	return ""
}

// preserveLifeGate (Life Domain 3): the refusal's code; handleGMPreserveLife writes its message.
func preserveLifeGate(class, sub string, level int) string {
	switch {
	case gateClass(class) != "cleric":
		return "not_a_cleric"
	case strings.ToLower(sub) != "life":
		return "wrong_domain"
	case level < 3:
		return "insufficient_level"
	}
	return ""
}

// ---------------------------------------------------------------- Druid

func wildShapeGate(class string, level int) string {
	if gateClass(class) != "druid" {
		return "Cannot use Wild Shape: only Druids can."
	}
	if level < 2 {
		return "Cannot use Wild Shape: it needs Druid level 2."
	}
	return ""
}

// revertWildShapeGate: leaving the form early is part of Wild Shape (Druid 2).
func revertWildShapeGate(class string, level int, shaped bool) string {
	if gateClass(class) != "druid" {
		return "Cannot revert: only Druids use Wild Shape."
	}
	if level < 2 {
		return "Cannot revert: Wild Shape comes at Druid level 2."
	}
	if !shaped {
		return "Cannot revert: you aren't in Wild Shape."
	}
	return ""
}

func wildResurgenceGate(class string, level int) string {
	if gateClass(class) != "druid" || level < 5 {
		return "Wild Resurgence needs a Druid of level 5 or higher."
	}
	return ""
}

func landsAidGate(class, sub string, level int) string {
	if gateClass(class) != "druid" || strings.ToLower(sub) != "land" || level < 3 {
		return "Land's Aid needs a Druid of the Circle of the Land, level 3 or higher."
	}
	return ""
}

func naturalRecoveryGate(class, sub string, level int) string {
	if gateClass(class) != "druid" || strings.ToLower(sub) != "land" || level < 6 {
		return "Natural Recovery needs a Druid of the Circle of the Land, level 6 or higher."
	}
	return ""
}

// naturesSanctuaryGate: making the Cube and moving it.
func naturesSanctuaryGate(class, sub string, level int) string {
	if gateClass(class) != "druid" || strings.ToLower(sub) != "land" || level < 14 {
		return "Nature's Sanctuary is a feature of a level 14 Druid of the Circle of the Land."
	}
	return ""
}

func natureMagicianGate(class string, level int) string {
	if gateClass(class) != "druid" || level < 20 {
		return "Nature Magician needs a Druid of level 20."
	}
	return ""
}

// ---------------------------------------------------------------- Fighter

func secondWindGate(class string) string {
	if gateClass(class) != "fighter" {
		return "Only fighters can use Second Wind!"
	}
	return ""
}

func actionSurgeGate(class string, level int) string {
	if gateClass(class) != "fighter" {
		return "Only fighters can use Action Surge!"
	}
	if level < 2 {
		return "Action Surge requires Fighter level 2+!"
	}
	return ""
}

// ---------------------------------------------------------------- Monk

func martialArtsGate(class string, level int) string {
	if gateClass(class) != "monk" {
		return "Only a Monk has Martial Arts."
	}
	return ""
}

func flurryOfBlowsGate(class string, level int) string {
	if gateClass(class) != "monk" || level < 2 {
		return "Flurry of Blows needs a Monk of level 2 or higher."
	}
	return ""
}

func patientDefenseGate(class string, level int) string {
	if gateClass(class) != "monk" || level < 2 {
		return "Patient Defense needs a Monk of level 2 or higher."
	}
	return ""
}

func stepOfTheWindGate(class string, level int) string {
	if gateClass(class) != "monk" {
		return "Only monks can use Step of the Wind!"
	}
	if level < 2 {
		return "Step of the Wind requires Monk's Focus (level 2+)!"
	}
	return ""
}

func stunningStrikeGate(class string, level int) string {
	if gateClass(class) != "monk" {
		return "Only monks can use Stunning Strike!"
	}
	if level < 5 {
		return "Stunning Strike requires level 5+!"
	}
	return ""
}

func selfRestorationGate(class string, level int) string {
	if gateClass(class) != "monk" || level < 10 {
		return "Self-Restoration needs a Monk of level 10 or higher."
	}
	return ""
}

func disciplinedSurvivorGate(class string, level int) string {
	if gateClass(class) != "monk" || level < 14 {
		return "Disciplined Survivor needs a Monk of level 14 or higher."
	}
	return ""
}

func superiorDefenseGate(class string, level int) string {
	if gateClass(class) != "monk" || level < 18 {
		return "Superior Defense needs a Monk of level 18 or higher."
	}
	return ""
}

// quiveringPalmGate (Warrior of the Open Hand 17, SRD 5.2.1).
func quiveringPalmGate(class, sub string, level int) string {
	if gateClass(class) != "monk" || strings.ToLower(sub) != "open-hand" || level < 17 {
		return "Quivering Palm is a Warrior of the Open Hand feature from Monk level 17."
	}
	return ""
}

// wholenessOfBodyGate (Warrior of the Open Hand 6).
func wholenessOfBodyGate(class, sub string, level int) string {
	if !strings.EqualFold(class, "monk") || !strings.Contains(strings.ToLower(sub), "open") || level < 6 {
		return "Wholeness of Body is a Warrior of the Open Hand feature from Monk level 6."
	}
	return ""
}

// ---------------------------------------------------------------- Paladin

func layOnHandsGate(class string) string {
	if gateClass(class) != "paladin" {
		return "Only paladins can use Lay on Hands!"
	}
	return ""
}

func divineSenseGate(class string, level int) string {
	if gateClass(class) != "paladin" || level < 3 {
		return "Divine Sense is a Channel Divinity effect of a Paladin of level 3 or higher."
	}
	return ""
}

// sacredWeaponGate (Oath of Devotion 3): the refusal's code; handleGMSacredWeapon writes its message.
func sacredWeaponGate(class, sub string, level int) string {
	switch {
	case gateClass(class) != "paladin":
		return "not_a_paladin"
	case level < 3:
		return "insufficient_level"
	case sub != "devotion":
		return "wrong_subclass"
	}
	return ""
}

// holyNimbusGate (Oath of Devotion 20).
func holyNimbusGate(class, sub string, level int) string {
	if !strings.EqualFold(class, "paladin") || !strings.EqualFold(sub, "devotion") || level < 20 {
		return "Holy Nimbus is an Oath of Devotion feature at Paladin level 20."
	}
	return ""
}

// ---------------------------------------------------------------- Ranger

// hordeBreakerGate (Hunter 3, with Horde Breaker chosen for Hunter's Prey).
func hordeBreakerGate(class, sub string, level int, huntersPrey string) string {
	switch {
	case gateClass(class) != "ranger":
		return "Only Rangers can use Horde Breaker!"
	case sub != "hunter":
		return "Only Hunter Rangers can use Horde Breaker!"
	case level < 3:
		return "Horde Breaker requires level 3+!"
	case huntersPrey != "horde_breaker":
		return "You have not chosen Horde Breaker for your Hunter's Prey feature! Your choice: " + huntersPrey
	}
	return ""
}

func rangerLevelGate(class string, level, minLevel int) string {
	if gateClass(class) != "ranger" || level < minLevel {
		return fmt.Sprintf("This needs a Ranger of level %d or higher.", minLevel)
	}
	return ""
}

func tirelessGate(class string, level int) string    { return rangerLevelGate(class, level, 10) }
func naturesVeilGate(class string, level int) string { return rangerLevelGate(class, level, 14) }

// ---------------------------------------------------------------- Rogue

func cunningActionGate(class string, level int) string {
	if gateClass(class) != "rogue" || level < 2 {
		return "Cunning Action is a Rogue feature (level 2)."
	}
	return ""
}

func steadyAimGate(class string, level int) string {
	if gateClass(class) != "rogue" || level < 3 {
		return "Only a Rogue of level 3 or higher can take Steady Aim."
	}
	return ""
}

// ---------------------------------------------------------------- Sorcerer

func innateSorceryGate(class string) string {
	if gateClass(class) != "sorcerer" {
		return "Only a Sorcerer has Innate Sorcery."
	}
	return ""
}

// fontOfMagicGate (Sorcerer 2): the refusal's code; handleFlexibleCasting writes its message.
func fontOfMagicGate(class string, level int) string {
	if gateClass(class) != "sorcerer" {
		return "not_a_sorcerer"
	}
	if level < 2 {
		return "level_too_low"
	}
	return ""
}

func dragonWingsGate(class, sub string, level int) string {
	if gateClass(class) != "sorcerer" || strings.ToLower(sub) != "draconic" || level < 14 {
		return "Dragon Wings needs a Draconic Sorcerer of level 14 or higher."
	}
	return ""
}

// ---------------------------------------------------------------- Warlock

func magicalCunningGate(class string, level int) string {
	if gateClass(class) != "warlock" || level < 2 {
		return "Magical Cunning needs a Warlock of level 2 or higher."
	}
	return ""
}

// invocationGate: an Eldritch Invocation's own action (only a Warlock has invocations).
func invocationGate(class string, hasInvocation func(string) bool, slug, name string) string {
	if gateClass(class) != "warlock" || !hasInvocation(slug) {
		return "You don't have the " + name + " invocation."
	}
	return ""
}

func gazeOfTwoMindsGate(class string, hasInvocation func(string) bool) string {
	return invocationGate(class, hasInvocation, "gaze-of-two-minds", "Gaze of Two Minds")
}

func giftOfTheProtectorsGate(class string, hasInvocation func(string) bool) string {
	return invocationGate(class, hasInvocation, "gift-of-the-protectors", "Gift of the Protectors")
}

// ---------------------------------------------------------------- feats and Epic Boons

func improveFateGate(hasFeat func(string) bool) string {
	if !hasFeat("boon_of_fate") {
		return "Improve Fate needs the Boon of Fate."
	}
	return ""
}

func mergeWithShadowsGate(hasFeat func(string) bool) string {
	if !hasFeat("boon_of_the_night_spirit") {
		return "Merge with Shadows needs the Boon of the Night Spirit."
	}
	return ""
}

func blinkStepsGate(hasFeat func(string) bool) string {
	if !hasFeat("boon_of_dimensional_travel") {
		return "Blink Steps needs the Boon of Dimensional Travel."
	}
	return ""
}

// ---------------------------------------------------------------- magic items

// scimitarOfSpeedGate: the Bonus Action attack is made with the Scimitar of Speed, so it is in hand, and it needs attunement.
// heldAttuned answers for an item name: it is in a hand, the character is attuned to it.
func scimitarOfSpeedGate(heldAttuned func(item string) (held, attuned bool)) string {
	held, attuned := heldAttuned("Scimitar of Speed")
	if !held {
		return "A Bonus Action attack (bonus_attack) needs a Scimitar of Speed in hand. Nothing was spent."
	}
	if !attuned {
		return "The Scimitar of Speed's Bonus Action attack needs attunement (POST /api/characters/attune). Nothing was spent."
	}
	return ""
}

// ---------------------------------------------------------------- spells' own actions (hasEffect: a spell effect on the character)

func spiritualWeaponGate(hasEffect func(string) bool) string {
	if !hasEffect("spiritual_weapon") {
		return "Cannot use the Spiritual Weapon: you have none (cast Spiritual Weapon; it lasts while you keep Concentration). Nothing was spent."
	}
	return ""
}

func flameBladeGate(hasEffect func(string) bool) string {
	if !hasEffect("flame_blade") {
		return "Cannot use the Flame Blade: you have none (cast Flame Blade; it lasts while you keep Concentration). Nothing was spent."
	}
	return ""
}

func endGaseousFormGate(hasEffect func(string) bool) string {
	if !hasEffect("gaseous_form") {
		return "Cannot end Gaseous Form: you aren't in it. Nothing was spent."
	}
	return ""
}

// ---------------------------------------------------------------- companions

// familiarCommandGate: dismissing and recalling are commands to a familiar.
func familiarCommandGate(c companion) string {
	if c.Kind != "familiar" {
		return "Only a familiar can be dismissed and recalled."
	}
	return ""
}

// companionSpecialGate: Breath Weapon (a Draconic Spirit) and an Otherworldly Steed's Fey Step, Fell Glare and Healing Touch (the
// steed's creature type decides which).
func companionSpecialGate(action string, c companion) string {
	switch action {
	case "breath", "breath_weapon":
		if c.Form != "draconic-spirit" {
			return "Only a Draconic Spirit (Summon Dragon) has a Breath Weapon."
		}
	case "fey_step", "fell_glare", "healing_touch":
		if c.Kind != "steed" {
			return "Only an Otherworldly Steed has Fey Step, Fell Glare and Healing Touch."
		}
		want := map[string]string{"fey_step": "fey", "fell_glare": "fiend", "healing_touch": "celestial"}[action]
		if c.CreatureType != want {
			return fmt.Sprintf("%s is a %s steed; %s belongs to a %s steed.", companionDisplayName(c), titleCase(c.CreatureType), titleCase(strings.ReplaceAll(action, "_", " ")), titleCase(want))
		}
	}
	return ""
}

// ---------------------------------------------------------------- states anyone can be in

func standGate(prone bool) string {
	if !prone {
		return "You are not prone. No need to stand up."
	}
	return ""
}

func extinguishGate(burning bool) string {
	if !burning {
		return "You aren't burning."
	}
	return ""
}
