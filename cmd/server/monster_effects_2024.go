package main

// Monster effects on characters, resolved from the text of the 2024 stat blocks (SRD 5.2.1). Used by Legendary Actions, auras and
// POST /api/gm/narrate monster_action (breath weapons, gazes, spells and the like, which used to get a made-up attack roll).
//	saving throw effect  "Dexterity Saving Throw: DC 17, one creature ... Failure: 22 (4d10) Poison damage, and the target has the
//	                     Frightened condition until the end of its next turn. Success: Half damage." Each target rolls its save
//	                     (saveCtx, save_2024.go: proficiency, Aura of Protection, Bless, Exhaustion ...); the damage is rolled once; Evasion
//	                     halves or cancels Dexterity save damage; conditions get their duration ("until the start of its next turn",
//	                     "until the end of its next turn", "for 1 minute", a repeated save at the end of each turn); Exhaustion levels
//	                     are added; anything else in the Failure text is shown to the GM.
//	attack               "makes one Rend attack" (or "one Claw or Tail attack": use picks one) -> the stat block's attack.
//	another action       "uses Lightning Strike" -> that action of the stat block.
//	spell                "uses Spellcasting to cast Scorching Ray (level 3 version)" / "casts Fear" -> the spell from the SRD data,
//	                     with the DC and attack bonus of the monster's Spellcasting: spell attacks (rays) against AC, saving throws
//	                     with damage (half on a success when the spell says so) and the condition spells (Fear, Hold Monster ...).
//	damage without save  "each creature within 10 feet ... takes 11 (2d10) Necrotic damage".
//	movement             "moves up to half its Speed", "teleports": a note for the GM to move the token on the battle map.
// Targets are characters (target or target_ids); an Emanation effect with a battle map and no targets reaches the characters in it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var (
	mfxSaveRe                = regexp.MustCompile(`(Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) Saving Throw: DC (\d+)`)
	mfxSuccessImmunityTimeRe = regexp.MustCompile(`(?i)immune to (?:this|the) [^.!?]*? for (?:the next )?(\d+) (rounds?|minutes?|hours?|days?)\b`)
	mfxDamageRe              = regexp.MustCompile(`(\d+) \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage`)
	mfxCondRe                = regexp.MustCompile(`has the ((?:[A-Z][a-z]+)(?:(?:, | and | or )[A-Z][a-z]+)*) conditions?`)
	mfxEscapeRe              = regexp.MustCompile(`Grappled condition \(escape DC (\d+)\)`)
	// mfxRestrainRe: a second condition that lasts as long as the grapple ("and it has the Restrained condition until the grapple ends",
	// "and the target has the Poisoned condition until the grapple ends": SRD 5.2.1 Roper Tentacle; "While Grappled, the target has the
	// Restrained condition"). Group 1: the condition ("" for the last form: Restrained).
	mfxRestrainRe          = regexp.MustCompile(`(?i)(?:,? and (?:it|the target) has the ([A-Za-z]+) condition until the grapple ends|while Grappled, the target has the Restrained condition)`)
	mfxExhaustRe           = regexp.MustCompile(`gains? (\d+) Exhaustion levels?`)
	mfxAttackRe            = regexp.MustCompile(`makes one ([A-Z][A-Za-z’' -]+?)(?: or ([A-Z][A-Za-z’' -]+?))? attack`)
	mfxUsesRe              = regexp.MustCompile(`uses (?:its )?([A-Z][A-Za-z’' -]+?)(?:[.,]| and | to |$)`)
	mfxSpellRe             = regexp.MustCompile(`(?:uses Spellcasting to cast|casts) (?:the )?([A-Z][A-Za-z’' /-]+?)(?: spell)?(?: \(level (\d) version\))?(?: on itself)?(?: twice)?(?:[.,;]| on |$)`)
	mfxSpellDCRe           = regexp.MustCompile(`spell save DC (\d+)`)
	mfxSpellAtkRe          = regexp.MustCompile(`([+−-]\d+) to hit with spell attacks`)
	mfxEmanRe              = regexp.MustCompile(`(\d+)\s*-?\s*foot Emanation`)
	mfxUntilStartRe        = regexp.MustCompile(`until the start of (?:its|the (?:[^ ]+?)['’]s) next turn`) // the attacker's turn ("the vrock's"); "the target's" is the target's own
	mfxUntilEndRe          = regexp.MustCompile(`until the end of the ([^ ]+?)['’]s next turn`)             // an end anchored to a named creature's turn; "the target's" is the target's own
	mfxEndSaveRe           = regexp.MustCompile(`(?i)repeats the save at the end of each of its turns|at the end of each of its turns, (?:the target|it) repeats the save`)
	mfxAutoSucceedRe       = regexp.MustCompile(`(?i)\bAfter (\d+) minutes?, it succeeds automatically`)
	targetIsCreatureGateRe = regexp.MustCompile(`(?i)\bIf the target is a creature,?(?:\s+(?:its|it)\b)?`)
	mfxDurationRe          = regexp.MustCompile(`(?i)(?:until the (?:start|end) of (?:its|the [^ ]+) next turn|for \d+ (?:minutes?|hours?)|, until it takes damage)`)
	// condDurationRe: a condition's duration in time ("for 10 minutes", "for 1 hour")
	condDurationRe = regexp.MustCompile(`(?i)\bfor (\d+) (minutes?|hours?)\b`)
	// endsOnDamageRe: "... for 1 hour, until it takes damage, or until ..." (SRD Incubus Nightmare, Nalfeshnee Horror Nimbus): the
	// condition ends when the creature takes damage
	endsOnDamageRe = regexp.MustCompile(`(?i)(?:\bor|,) until it takes damage\b`)
	// autoSucceedTypesRe: "(demons succeed automatically)" (SRD Vrock Stunning Screech): creatures of those types or tags succeed on the
	// save without a roll
	autoSucceedTypesRe = regexp.MustCompile(`(?i)\(([a-z]+(?:(?:, |,? and |,? or )[a-z]+)*) succeed automatically\)`)
	mfxConditions      = map[string]bool{"blinded": true, "charmed": true, "deafened": true, "frightened": true, "grappled": true, "incapacitated": true,
		"invisible": true, "paralyzed": true, "petrified": true, "poisoned": true, "prone": true, "restrained": true, "stunned": true, "unconscious": true}
)

func normDice(d string) string {
	return strings.ReplaceAll(strings.ReplaceAll(d, " ", ""), "−", "-")
}

// mfxSection cuts the text after a label up to the next label.
func mfxSection(text, label string, next ...string) string {
	i := strings.Index(text, label)
	if i < 0 {
		return ""
	}
	rest := text[i+len(label):]
	end := len(rest)
	for _, n := range next {
		if j := strings.Index(rest, n); j >= 0 && j < end {
			end = j
		}
	}
	return strings.TrimSpace(rest[:end])
}

// hpDrain: the effect's "the target's Hit Point maximum decreases by an amount equal to the [Necrotic] damage taken" (hp_max_drain_2024.go).
type hpDrain struct {
	Type      string // the damage type it counts; "" for all the damage taken
	OnSuccess bool   // under "Failure or Success:": a success's damage drains too (Succubus Draining Kiss)
	Regain    bool   // "and the vampire regains Hit Points equal to that amount"
}

type saveEffect struct {
	Ability          string // dex, con ...
	Drain            *hpDrain
	AbilityName      string // Dexterity
	DC               int
	Damage           []damagePart
	HalfOnSuccess    bool
	SeverePoison     *severePoisonEffect
	Conditions       []string
	Rounds           int                 // condition duration in rounds (0: until removed)
	UntilStart       bool                // "until the start of its next turn"
	UntilEnd         bool                // "until the end of the <creature>'s next turn": ends at the end of the SOURCE creature's turn (monsterFeaturesTurnEnd)
	EndSave          bool                // repeats the save at the end of each of its turns
	Exhaustion       int                 // Exhaustion levels on a failure
	EscapeDC         int                 // "the Grappled condition (escape DC 16)": the DC of the escape checks
	Escapes          []conditionEscape   // a spell's escape checks (conditionSpell.escapes: Entangle, Web)
	OnFailure        []entryMechanic     // the mechanics the GM attached to the entry that a failed save gives (engulf, burning: monster_mechanics_2024.go)
	RestrainedToo    bool                // "and it has the Restrained condition until the grapple ends": a condition that ends with the Grappled record
	LinkedCond       string              // that condition (Restrained; Poisoned for the Roper's Tentacle)
	SpeedMode        string              // "halved", "zero" or "minus": a Speed rider of the text (speed_owner_2024.go), applied as a marker for SpeedEnd
	SpeedEnd         riderEnd            // the Speed rider's duration (rider_duration_2024.go)
	Always           *saveEffect         // the conditions, Speed and Disadvantage of "Failure or Success:": given to every target that rolled the save
	DisadvMode       []string            // the roll riders of the text: "sap", "exposed", "attacks", "concentration", "save:int", "noreactions" (disadvantage_riders_2024.go)
	RollRiderEnd     map[string]riderEnd // the duration of each DisadvMode rider
	Effects          []effectRead        // the effect records of the text: a die subtracted from D20 Tests, Strength-based Disadvantage (effect_records_2024.go)
	Curse            *curseRead          // "the target is cursed" and what the curse does (curse_records_2024.go)
	CurseLinked      bool                // "Until the curse ends, the target has the Charmed and Poisoned conditions": its conditions end with the curse record
	SpellSlug        string              // the spell this effect is a cast of (its lasting remainder is the GM's open item: gm_handover_2024.go)
	Wound            *woundRead          // "The target receives an infernal wound ..." (wound_records_2024.go)
	NotHolding       bool                // "doesn't already have an infernal wound" / "if it doesn't have one": a creature that holds the wound is not a target
	OnlyType         string              // "If the target is a Humanoid": only a creature of this type is a target (applySaveEffect)
	SpeedMinus       int                 // the feet of "its Speed decreases by 10 feet"
	ImmunityName     string              // stat-block entry key for persisted success immunity (set by the effect caller)
	Immunity         bool                // success grants immunity to this source and entry
	ImmunityRounds   int                 // source duration, in combat rounds
	ImmunityDuration string              // the parsed numeric duration for the result
	CondMaxSize      string              // "If the target is a Huge or smaller creature, ... has the Prone condition": the conditions apply only to a target of this size or smaller
	CondIfAdvantage  bool                // "... and the darkmantle had Advantage on the attack roll": the conditions apply only when the roll had Advantage (enforced by the hit caller, which has the roll)
	Lasting          *lastingEffects     // "While Poisoned, the target can't regain Hit Points and takes ... at the start of each of its turns" (condition_lasting_effects_2024.go)
	ItemPenalty      string              // "The object takes a -1 penalty to the AC it offers (armor) or to its attack rolls (weapon)": "metal" (a metal object only) or "any" (item_state_2024.go)
	Extra            string              // Failure text the server doesn't apply (for the GM)
	Emanation        int                 // feet, when the targets are in an Emanation from the monster
	IsSpell          bool                // a spell: items that guard against spells work (legendary_more_2024.go)
	SpellLevel       int                 // its level (0: a cantrip)
	School           string              // its school (Necromancy: the Scarab of Protection)
	SingleTarget     bool                // aimed only at one creature, no area (Spell Absorption, Ring of Spell Turning's Reaction)
	SourceID         int                 // the monster it comes from
	Stage            *spellStage         // a condition spell's second stage (Sleep: spellStages)
	FromUndead       bool                // it comes from an Undead (the Scarab of Protection)
	EndsOnDamage     bool                // "or until it takes damage": the condition ends when the creature takes damage (wakeOnDamageSleep)
	AutoSucceedTypes []string            // creature types that succeed on the save automatically ("construct", "undead": rollForTarget)
	Setup            string              // the entry\'s sentences outside the save line and its sections ("The djinni conjures a whirlwind at a point ...": saveSetupText), the GM\'s
	Push             *savePush           // "the target is pushed up to 30 feet straight away from the dragon" on a failed save (applySavePush)
	DropAtOrBelow    int                 // "If the target has 20 Hit Points or fewer, it drops to 0 Hit Points. Otherwise, the target takes ...": dropsToZero
	Kills            bool                // "Failure: The target dies" (SRD 5.2.1 Will-o'-Wisp Consume Life): killOnFailure
	KillRegain       string              // ", and the wisp regains 10 (3d6) Hit Points": the source's regain, the monster Hit Point owner
}

// conditionAdd is the condition record a failed save leaves (character or monster target): its duration, the repeated save, the note
// "until the start of its next turn", and for Grappled the escape checks against the stat block's escape DC.
func (e saveEffect) conditionAdd(cond, source string) condAdd {
	a := condAdd{Condition: cond, Source: source, Rounds: e.Rounds, SourceID: e.SourceID, Magical: e.IsSpell}
	if e.EndsOnDamage && cond == "unconscious" {
		a.Notes = notesEndsOnDamage // woken by damage (wakeOnDamageSleep)
	}
	if e.CurseLinked {
		a.Notes = notesEndsWith + condCursed // "Until the curse ends, the target has the Charmed and Poisoned conditions"
	}
	if e.EndSave {
		a.EndSave = &conditionSave{Ability: e.Ability, DC: e.DC}
		if e.Stage != nil {
			a.EndSave.Then, a.EndSave.ThenRounds, a.EndSave.ThenEndsOnDamage, a.EndSave.ThenMagicalSleep = e.Stage.Then, e.Stage.ThenRounds, e.Stage.EndsOnDamage, e.Stage.MagicalSleep
			a.EndSave.ThenRepeats = e.Stage.Repeats
		}
	}
	if e.Stage != nil {
		a.MagicalSleep = e.Stage.MagicalSleep
	}
	if e.UntilStart {
		a.Notes = notesUntilStartOfSourceTurn
	}
	if e.UntilEnd {
		a.Notes = notesUntilEndOfSourceTurn
	}
	if len(e.Escapes) > 0 {
		a.Escapes = e.Escapes
	}
	if cond == "grappled" && e.EscapeDC > 0 {
		a.Escapes, _ = normalizeEscapes(defaultEscapes("grappled", e.EscapeDC))
		if a.Notes == "" {
			a.Notes = fmt.Sprintf("Grappled; escape DC %d", e.EscapeDC)
		}
	}
	return a
}

// linkedCondition: the second condition of "Grappled (escape DC N), and it has the Restrained condition until the grapple ends": a record
// of that condition (LinkedCond) from the same source that the condition owner ends together with the Grappled record
// (conditionChange.remove).
func (e saveEffect) linkedCondition(cond, source string) (condAdd, bool) {
	if cond != "grappled" || !e.RestrainedToo {
		return condAdd{}, false
	}
	linked := e.LinkedCond
	if linked == "" {
		linked = "restrained"
	}
	return condAdd{Condition: linked, Source: source, SourceID: e.SourceID, Magical: e.IsSpell, Notes: notesEndsWithGrapple}, true
}

// conditionsFor: the conditions of a failed save that this target gets. A condition gated by size ("If the target is a Huge or
// smaller creature ...") is not given to a bigger target: gated says so for the reply. The one rule for characters and monsters.
func (e saveEffect) conditionsFor(lobbyID, tid int) (conds []string, gated string) {
	if _, ok := sizeOrder[e.CondMaxSize]; ok && isCompanionCombatID(tid) { // the server does not know a companion's size: the GM checks
		return nil, fmt.Sprintf("not applied: %s only to a target of %s size or smaller (the GM checks the companion's size)", strings.Join(e.Conditions, ", "), titleCase(e.CondMaxSize))
	}
	if lim, ok := sizeOrder[e.CondMaxSize]; ok && sizeOrder[strings.ToLower(creatureSize(lobbyID, tid))] > lim {
		return nil, fmt.Sprintf("not %s (larger than %s)", strings.Join(e.Conditions, ", "), titleCase(e.CondMaxSize))
	}
	return e.Conditions, ""
}

// saveOption is one of the effects an action offers: "Charm Ray. Wisdom Saving Throw: DC 16. Failure: ...".
type saveOption struct{ Name, Text string }

var mfxOptionRe = regexp.MustCompile(`(?:: |\. )([A-Z][A-Za-z’' -]{2,40})\. (?:Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) Saving Throw: DC`)

// splitSaveOptions: the named saving throw effects of an action text, in order; fewer than two when the text is one effect.
func splitSaveOptions(text string) []saveOption {
	idx := mfxOptionRe.FindAllStringSubmatchIndex(text, -1)
	var out []saveOption
	for i, m := range idx {
		start := m[2] // the name
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0] + 1 // up to the ". " or ": " that opens the next one
		}
		out = append(out, saveOption{Name: text[m[2]:m[3]], Text: strings.TrimSpace(text[start:end])})
	}
	return out
}

// mfxSizeGateRe: a sentence that gives the condition only to targets of a size or smaller.
var mfxSizeGateRe = regexp.MustCompile(`(?i)\bIf the target is an? (Tiny|Small|Medium|Large|Huge|Gargantuan) or smaller[^.]*`)

// advantageGateRe: the condition clause gated on the attacker's own roll ("... and the darkmantle had
// Advantage on the attack roll"): read at parse, enforced by the hit caller, which has the roll.
var advantageGateRe = regexp.MustCompile(`(?i)\bhad [Aa]dvantage on the attack roll\b`)

// readRider reads what a failed save or a hit gives besides damage out of its text: the conditions, their duration, the escape DC of a
// grapple, Exhaustion, a size gate. extra is what the server does not apply, for the GM: the sentences that are not only damage or a
// condition, and every sentence after the first one that names a condition (only the first is read).
// sentenceSplitRe breaks stat-block prose into sentences across paragraph breaks (". ", ".\n", ".\n\n"),
// which plain strings.Split(text, ". ") cannot see.
var sentenceSplitRe = regexp.MustCompile(`\.\s+`)

func (e *saveEffect) readRider(fail string) (extra []string) {
	// conditions joined by "or" are a choice ("the Charmed or Poisoned condition (its choice)"): the rules never give both, and the server
	// doesn't pick, so none is applied and the sentence stays the GM's (I-ALDENQA-8: both were given). No SRD stat block words one so.
	if c := mfxCondRe.FindStringSubmatch(fail); c != nil && !strings.Contains(c[1], " or ") {
		for _, name := range regexp.MustCompile(`, | and `).Split(c[1], -1) {
			if n := strings.ToLower(strings.TrimSpace(name)); mfxConditions[n] {
				e.Conditions = append(e.Conditions, n)
			}
		}
	}

	if x := mfxSizeGateRe.FindStringSubmatch(fail); x != nil && len(e.Conditions) > 0 && strings.Contains(strings.ToLower(x[0]), "condition") {
		e.CondMaxSize = strings.ToLower(x[1])
	}
	if len(e.Conditions) > 0 && advantageGateRe.MatchString(fail) {
		e.CondIfAdvantage = true
	}
	lf := strings.ToLower(fail)
	switch {
	case mfxUntilStartRe.MatchString(lf) && !strings.Contains(strings.NewReplacer("’", "'").Replace(lf), "start of the target's next turn"):
		e.UntilStart = true
	case mfxUntilEndAnchored(lf):
		e.UntilEnd = true
	case strings.Contains(lf, "until the end of its next turn"), strings.Contains(lf, "next turn"):
		e.Rounds = 1
	default:
		if d := conditionDuration(lf); d != nil && len(e.Conditions) > 0 {
			unit := map[string]int{"minute": 10, "hour": 600}[strings.TrimSuffix(d[2], "s")]
			if n, err := strconv.Atoi(d[1]); err == nil {
				e.Rounds = n * unit
			}
		}
	}
	e.EndsOnDamage = len(e.Conditions) > 0 && endsOnDamageRe.MatchString(fail)
	e.readSpeed(fail)
	_, gmDisadv := e.readDisadvantage(fail)
	e.readEffects(fail) // effect records (effect_records_2024.go)
	e.readCurse(fail)   // a curse (curse_records_2024.go)
	e.readWound(fail)   // a wound (wound_records_2024.go)
	extra = append(extra, gmDisadv...)
	e.EndSave = mfxEndSaveRe.MatchString(lf)
	autoLimit := false // the condition's limit was read from "After N minutes, it succeeds automatically"
	if m := mfxAutoSucceedRe.FindStringSubmatch(fail); m != nil && e.EndSave && e.Rounds == 0 && (len(e.Conditions) > 0 || len(e.Effects) > 0) {
		n, _ := strconv.Atoi(m[1])
		e.Rounds, autoLimit = n*10, true // the repeated save ends it at the latest then
	}
	if x := mfxEscapeRe.FindStringSubmatch(fail); x != nil {
		e.EscapeDC, _ = strconv.Atoi(x[1])
	}
	if m := mfxRestrainRe.FindStringSubmatch(fail); m != nil && e.EscapeDC > 0 && (m[1] == "" || mfxConditions[strings.ToLower(m[1])]) {
		e.RestrainedToo, e.LinkedCond = true, strings.ToLower(m[1])
	}
	if x := mfxExhaustRe.FindStringSubmatch(fail); x != nil {
		e.Exhaustion, _ = strconv.Atoi(x[1])
	}
	// what the server doesn't apply, for the GM: the Failure sentences that are not only damage or a condition
	for _, s := range sentenceSplitRe.Split(fail, -1) {
		ls := strings.ToLower(s)
		// What the target suffers while it has the condition this text gives ("While Poisoned, the target can't regain Hit Points and
		// takes ... at the start of each of its turns"): recorded with the condition (giveConditions → addLastingEffects), not the GM's.
		if l, rest := readLastingEffects(s, e.Conditions); l != nil {
			e.Lasting = l
			if rest != "" {
				extra = append(extra, strings.TrimSuffix(rest, "."))
			}
			continue
		}
		// Recurring damage the turn pipeline ticks (addRecurringDamage recorded it when its gate - the
		// grapple or the attach - held): the clause, the enforced lockout sharing its sentence and the
		// gate lead-in are not the GM's.
		if spec := readRecurringDamage(s); spec != nil {
			s = recurringDamageRe.ReplaceAllString(s, "")
			s = recurringLockoutTailRe.ReplaceAllString(s, "")
			s = grappleGatePrefixRe.ReplaceAllString(s, "")
			s = attachGatePrefixRe.ReplaceAllString(s, "")
			s = attachLockoutLeadRe.ReplaceAllString(s, "")
			if strings.Trim(s, " ,.") == "" {
				continue
			}
			ls = strings.ToLower(s)
		}
		// "If the target has 20 Hit Points or fewer, it drops to 0 Hit Points." (SRD 5.2.1 Sea Hag Death Glare): dropsToZero decides it per
		// target before the damage, which then only "Otherwise" applies
		if m := saveDropRe.FindStringSubmatch(s); m != nil {
			e.DropAtOrBelow, _ = strconv.Atoi(m[1])
			continue
		}
		if m := saveKillRe.FindStringSubmatch(s); m != nil {
			e.Kills, e.KillRegain = true, normDice(m[1])
			continue
		}
		if e.DropAtOrBelow > 0 && saveOtherwiseDamageRe.MatchString(s) {
			continue // the damage above, dealt only to a target with more Hit Points
		}
		// The attach onset ("and the stirge attaches to the target") is enforced by the attach owner on
		// the hit (monster_attach_2024.go): not the GM's.
		if attachOnHitRe.MatchString(s) {
			s = attachOnHitRe.ReplaceAllString(s, "")
			if strings.Trim(s, " ,.") == "" {
				continue
			}
			ls = strings.ToLower(s)
		}
		// The published detach ACTION (the target's or an adjacent creature's, with or without a check)
		// is enforced by the detach action owner (/api/attach/detach): not the GM's. The VOLUNTARY
		// detach ("can detach itself by spending 5 feet of its movement") stays: it is the movement
		// system's.
		if detachAutoRe.MatchString(s) {
			s = detachAutoRe.ReplaceAllString(s, "")
			if strings.Trim(s, " ,.") == "" {
				continue
			}
			ls = strings.ToLower(s)
		}
		// The "attack only the target, with Advantage" rider is enforced by the attach owner (attack
		// entry refusal + the roll's Advantage source): not the GM's. Speed 0 / moves-with-target stays.
		if whileAttachedOnlyAdvRe.MatchString(s) {
			s = whileAttachedOnlyAdvRe.ReplaceAllString(s, "")
			if strings.Trim(s, " ,.") == "" {
				continue
			}
			ls = strings.ToLower(s)
		}
		if detachCheckRe.MatchString(s) {
			s = detachCheckRe.ReplaceAllString(s, "")
			if strings.Trim(s, " ,.") == "" {
				continue
			}
			ls = strings.ToLower(s)
		}
		// The linked condition was already recorded. Preserve a distinct
		// restriction in the same sentence for the GM.
		if e.RestrainedToo {
			if loc := mfxRestrainRe.FindStringIndex(s); loc != nil {
				rest := strings.TrimSpace(grappledTargetLockRe.ReplaceAllString(s[loc[1]:], "")) // applied by grappledTargetLock
				rest = strings.Trim(strings.TrimSpace(strings.TrimPrefix(rest, "and")), " .,")
				if rest != "" {
					extra = append(extra, strings.TrimSpace(s))
				}
				continue
			}
		}
		if first := mfxCondRe.FindString(fail); first != "" && mfxCondRe.MatchString(s) && !strings.Contains(s, first) {
			extra = append(extra, strings.TrimSuffix(strings.TrimSpace(s), ".")) // a second condition sentence: not read, so not dropped
			continue
		}
		plain := mfxDamageRe.ReplaceAllString(s, "")
		if e.CondMaxSize != "" {
			plain = mfxSizeGateRe.ReplaceAllString(plain, "") // the size gate is read (CondMaxSize) and enforced at application: not the GM's
		}
		if e.SpeedMode != "" {
			plain = stripSpeedRider(plain) // applied (giveConditions): not the GM's
		}
		if len(e.DisadvMode) > 0 {
			plain = stripDisadvantage(plain) // applied (giveDisadvantage)
		}
		if len(e.Effects) > 0 {
			plain = stripEffects(plain) // applied (giveEffects)
		}
		if e.Curse != nil {
			plain = stripCurse(plain) // applied (giveCurse)
		}
		if e.Wound != nil {
			plain = stripWound(plain) // applied (giveWound)
		}
		plain = mfxCondRe.ReplaceAllString(plain, "")
		plain = mfxDurationRe.ReplaceAllString(plain, "")
		plain = targetIsCreatureGateRe.ReplaceAllString(plain, "") // every target of these routes is a creature: the gate always holds
		plain = strings.Trim(strings.NewReplacer("The target", "", "and the target", "", "damage", "", ",", "", ".", "").Replace(plain), " ")
		if strings.Contains(ls, "suffocat") {
			if attachTieRe.MatchString(s) {
				continue // suffocating while attached: the suffocation owner (monster_suffocation_2024.go) starts and ends it with the attach
			}
			extra = append(extra, strings.TrimSuffix(strings.TrimSpace(s), ".")) // other suffocation is the GM's (gm_2024.go suffocation): never dropped as short
			continue
		}
		if strings.Contains(strings.NewReplacer("’", "'").Replace(ls), "can't take reactions") && !slices.Contains(e.DisadvMode, "noreactions") {
			extra = append(extra, strings.TrimSuffix(strings.TrimSpace(s), ".")) // "can't take Reactions" with an end the server cannot count: the GM's, never dropped as short
			continue
		}
		if strings.Contains(ls, "speed") && e.SpeedMode == "" && len(plain) < 25 {
			extra = append(extra, strings.TrimSuffix(strings.TrimSpace(s), ".")) // a Speed effect that is not applied is the GM's, never dropped as short
			continue
		}
		if autoLimit && mfxAutoSucceedRe.MatchString(s) {
			autoLimit = false // that one sentence is applied as the condition's duration (Rounds); a later one is another effect's
			continue
		}
		if strings.Contains(ls, "repeats the save") || strings.Contains(ls, "exhaustion level") || len(plain) < 25 {
			continue
		}
		if loc := endsOnDamageRe.FindStringIndex(s); e.EndsOnDamage && loc != nil {
			// the duration and the end on damage are applied (conditionAdd); another way it ends ("or a creature within 5 feet of it
			// takes an action to wake it") is the GM's
			rest := strings.TrimLeft(strings.TrimSpace(s[loc[1]:]), ", ")
			rest = strings.TrimPrefix(strings.TrimPrefix(rest, "or "), "until ")
			if rest = strings.Trim(rest, " ."); rest != "" {
				extra = append(extra, "It also ends if "+rest)
			}
			continue
		}
		extra = append(extra, e.unappliedPart(s)) // the applied riders of the sentence are not repeated to the GM
	}
	return extra
}

// conditionDuration: the first "for N minutes" / "for N hours" of a Failure that is the condition's duration, not an immunity's ("the
// target is immune to this ghost's Possession for 24 hours" is how long the immunity lasts).
func conditionDuration(lf string) []string {
	for _, loc := range condDurationRe.FindAllStringSubmatchIndex(lf, -1) {
		if strings.Contains(lf[max(0, loc[0]-80):loc[0]], "immune") {
			continue
		}
		return []string{lf[loc[0]:loc[1]], lf[loc[2]:loc[3]], lf[loc[4]:loc[5]]}
	}
	return nil
}

// unappliedPart is a residual sentence without the Speed and roll riders the server applies from it ("The target can't take
// Reactions; its Speed is halved; and it can take either an action or a Bonus Action on its turn, not both" leaves the last clause),
// so the GM never reads an applied rider as one to apply. A sentence with nothing applied is kept whole.
func (e *saveEffect) unappliedPart(s string) string {
	r := strings.TrimSuffix(strings.TrimSpace(s), ".")
	if e.SpeedMode != "" {
		for _, re := range []*regexp.Regexp{speedZeroRe, speedHalvedRe, speedMinusRe} {
			r = re.ReplaceAllString(r, "")
		}
	}
	if len(e.DisadvMode) > 0 {
		for _, re := range rollRiderPatterns() {
			r = re.ReplaceAllString(r, "")
		}
	}
	if len(e.Effects) > 0 {
		r = stripEffects(r)
	}
	if e.Curse != nil {
		r = stripCurse(r)
	}
	if e.Wound != nil {
		r = stripWound(r)
	}
	if r == strings.TrimSuffix(strings.TrimSpace(s), ".") {
		return r
	}
	var kept []string
	for _, part := range regexp.MustCompile(`\s*[;,]\s*`).Split(r, -1) {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "and "))
		if part == "" || strings.EqualFold(part, "its") || strings.EqualFold(part, "and") || strings.EqualFold(part, "the target’s") || strings.EqualFold(part, "the target's") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ", ")
}

// giveConditions gives a character target what a failed save or a hit gives besides damage: its conditions (not a size-gated one to a
// bigger target; not one the target is immune to), the Restrained that ends with a grapple, Exhaustion. One line part for each.
func (e saveEffect) giveConditions(lobbyID, tid int, source string) []string {
	var parts []string
	conds, gated := e.conditionsFor(lobbyID, tid)
	if gated != "" {
		parts = append(parts, gated)
	}
	for _, c := range conds {
		a := e.conditionAdd(c, source)
		if res := addCondition(lobbyID, tid, a); res.Immune != "" { // the condition owner: Immunity (auras, Mind Blank ...)
			parts = append(parts, fmt.Sprintf("not %s (%s)", c, res.Immune))
			continue
		}
		parts = append(parts, titleCase(c))
		if e.Lasting != nil && e.Lasting.Condition == c { // what it suffers while it has it, ended with the record
			parts = append(parts, addLastingEffects(lobbyID, tid, e.SourceID, *e.Lasting, source))
			also := e.Lasting.Also
			if e.Lasting.DisadvAttacks { // "While Deafened, the target also has Disadvantage on attack rolls": the roll rider's marker
				also = append(append([]string{}, also...), "disadv_attacks")
			}
			if e.Lasting.DisadvChecks { // "... on ability checks and attack rolls": the check owner's marker
				also = append(append([]string{}, also...), "disadv_checks")
			}
			for _, also := range also { // "While Frightened, the target has the Paralyzed condition": ends with the record
				if res := addCondition(lobbyID, tid, condAdd{Condition: also, Source: source, SourceID: e.SourceID, Magical: e.IsSpell, Notes: e.Lasting.AlsoNotes}); res.Immune != "" {
					parts = append(parts, fmt.Sprintf("not %s (%s)", also, res.Immune))
				}
			}
		}
		if l, ok := e.linkedCondition(c, source); ok {
			if res := addCondition(lobbyID, tid, l); res.Immune != "" {
				parts = append(parts, fmt.Sprintf("not %s (%s)", l.Condition, res.Immune))
			} else {
				parts = append(parts, titleCase(l.Condition)+" until the grapple ends")
			}
		}
	}
	if e.SpeedMode != "" {
		parts = append(parts, e.giveSpeed(lobbyID, tid, source))
	}
	parts = append(parts, e.giveDisadvantage(lobbyID, tid, source)...)
	parts = append(parts, e.giveEffects(lobbyID, tid, source)...)
	parts = append(parts, e.giveCurse(lobbyID, tid, source)...)
	parts = append(parts, e.giveWound(lobbyID, tid, source)...)
	if e.Exhaustion > 0 {
		switch why := immuneToCondition(lobbyID, tid, e.conditionAdd("exhaustion", source)); {
		case why != "":
			parts = append(parts, fmt.Sprintf("no Exhaustion (%s)", why))
		case tid < 0 || isCompanionCombatID(tid):
			parts = append(parts, fmt.Sprintf("The GM tracks %d level(s) of Exhaustion for %s", e.Exhaustion, creatureName(lobbyID, tid)))
		default:
			parts = append(parts, addExhaustion(tid, e.Exhaustion))
		}
	}

	return parts
}

// giveAlways gives what "Failure or Success:" gives, whatever the save's result (nil Always: nothing).
func (e saveEffect) giveAlways(lobbyID, tid int, source string) []string {
	if e.Always == nil {
		return nil
	}
	a := *e.Always
	a.SourceID, a.IsSpell, a.Ability, a.DC = e.SourceID, e.IsSpell, e.Ability, e.DC
	return a.giveConditions(lobbyID, tid, source)
}

// hitRiderChoiceRe: a hit that lets someone choose or roll its effect is not read: all of it goes to the GM.
// These clauses depend on an adjudicated result after damage, an alternate
// result, or a specific movement path. Do not grant their effects on an ordinary hit.
var hitRiderDeferredRe = regexp.MustCompile(`(?i)\b(?:can give it .+ instead of dealing damage|moved \d+\+? feet straight toward(?:s)?|if [^.]*?(?:reduc[[:alpha:]]* [^.]*? to 0 Hit Points|reduced to 0 Hit Points))\b`)

// judgedAlternativeRe: an alternative damage amount of a hit and its condition (", or 11 (2d8 + 2) Necrotic damage if the swarm is
// Bloodied"): group 1 is the condition, judged by classifyDamageCondition.
var judgedAlternativeRe = regexp.MustCompile(`,? or \d+(?: \([^)]*\))? [A-Za-z]+ damage (if [^.,;]+)`)

var hitRiderChoiceRe = regexp.MustCompile(`(?i)choice of|determined by rolling|one of the following`)

// parseHitRider reads the conditions a stat-block hit gives besides its damage ("..., and the target has the Stunned condition until
// the end of the vrock's next turn", "If the target is a Large or smaller creature, it has the Prone condition"): the same rules
// as a failed save (readRider), with no saving throw of their own. A hit with its own saving throw is the save owner's.
// usesAnotherEntry: the "uses <entry>" of a text that calls another entry of the stat block ("uses Lightning Strike"), nil when none.
// A "uses <entry>" in a clause that says when the effect ends ("which ends early immediately after the wisp makes an attack roll or uses
// Consume Life": SRD 5.2.1 Will-o'-Wisp Vanish) is a condition of the effect, never a call to that entry.
func usesAnotherEntry(text string) []string {
	for _, loc := range mfxUsesRe.FindAllStringSubmatchIndex(text, -1) {
		before := text[:loc[0]]
		if i := strings.LastIndexAny(before, ".\n"); i >= 0 {
			before = before[i+1:]
		}
		if usesEndClauseRe.MatchString(before) {
			continue
		}
		m := make([]string, len(loc)/2)
		for k := range m {
			if loc[2*k] >= 0 {
				m[k] = text[loc[2*k]:loc[2*k+1]]
			}
		}
		return m
	}
	return nil
}

// usesEndClauseRe: the lead of a clause that says when an effect ends.
var usesEndClauseRe = regexp.MustCompile(`(?i)\bends?\b[^.]*\bafter\b`)

// hitRiderWhileRe: a sentence that lasts while a condition lasts ("While Poisoned, ...", "Until the grapple ends, ...").
var hitRiderWhileRe = regexp.MustCompile(`(?i)^\s*(?:While (?:it has the )?([A-Za-z]+)(?: condition)?|Until the (grapple) ends),`)

// dependsOnDeferred: the sentence lasts while a condition that only a deferred sentence gives.
func dependsOnDeferred(sentence string, deferred []string) bool {
	m := hitRiderWhileRe.FindStringSubmatch(sentence)
	if m == nil || len(deferred) == 0 {
		return false
	}
	cond := strings.ToLower(m[1])
	if m[2] != "" {
		cond = "grappled"
	}
	return strings.Contains(strings.ToLower(strings.Join(deferred, " ")), "the "+cond+" condition")
}

func parseHitRider(desc string) (saveEffect, bool) {
	restSave := "" // a save that waits for a rest is the GM's: the hit is read without it
	if loc := restSaveRe.FindStringIndex(desc); loc != nil {
		restSave, desc = strings.TrimSpace(desc[loc[0]:]), desc[:loc[0]]
	}
	i := strings.Index(desc, "Hit:")
	if i < 0 || mfxSaveRe.MatchString(desc) {
		return saveEffect{}, false
	}
	hit := desc[i+len("Hit:"):]
	if j := strings.Index(hit, "Hit or Miss:"); j >= 0 {
		hit = hit[:j]
	}
	if hitRiderChoiceRe.MatchString(hit) {
		return saveEffect{}, false
	}
	// a judged damage alternative (", or 11 (2d8 + 2) Necrotic damage if the swarm is Bloodied") is the damage owner's
	hit = judgedAlternativeRe.ReplaceAllStringFunc(hit, func(s string) string {
		if _, ok := classifyDamageCondition(strings.TrimSpace(judgedAlternativeRe.FindStringSubmatch(s)[1])); ok {
			return ""
		}
		return s
	})
	// a sentence that waits on a later result (reduced to 0 Hit Points, an alternate result, a movement path) is the GM's; the rest of
	// the hit is read as usual
	// A sentence that lasts only while a condition a deferred sentence gives lasts ("While Poisoned, the target also has the Paralyzed
	// condition" after "If this damage reduces the target to 0 Hit Points, ... it has the Poisoned condition for 1 hour": Phase Spider,
	// Vampire Familiar; "Until the grapple ends, ..." after "the rug can give it the Grappled condition ... instead of dealing damage")
	// waits with it: it is never applied on a hit that didn't give that condition.
	var deferred []string
	var kept []string
	for _, s := range sentenceSplitRe.Split(strings.TrimSpace(hit), -1) {
		if hitRiderDeferredRe.MatchString(s) || dependsOnDeferred(s, deferred) {
			deferred = append(deferred, strings.TrimSuffix(strings.TrimSpace(s), "."))
		} else {
			kept = append(kept, s)
		}
	}
	if len(deferred) > 0 && len(kept) <= 1 {
		return saveEffect{}, false // nothing but the damage besides: the whole rider stays the GM's (less what hitAfterDamage applies)
	}
	hit = strings.Join(kept, ". ")
	var e saveEffect
	e.Extra = strings.Join(append(e.readRider(hit), deferred...), ". ")
	if hpMaxDrainSentenceRe.MatchString(e.Extra) { // a hit's own: applied by hitAfterDamage on every hit route
		e.Extra = strings.Trim(hpMaxDrainSentenceRe.ReplaceAllString(e.Extra, ""), " .")
	}
	if stripped := stripBurnForCreatures(e.Extra); stripped != e.Extra {
		e.Extra = strings.Trim(stripped, " .")
	}
	if stripped := stripAbilityDecrease(e.Extra); stripped != e.Extra { // hitAfterDamage applies it
		e.Extra = strings.Trim(stripped, " .")
	}
	if stripped := stripItemPenalties(e.Extra); stripped != e.Extra { // hitAfterDamage corrodes the armor (item_state_2024.go)
		e.Extra = strings.Trim(stripped, " .")
	}
	if stripped := stripForcedMoves(e.Extra); stripped != e.Extra { // a push or pull: applied by hitAfterDamage
		e.Extra = strings.Trim(stripped, " .")
	}
	if stripped := stripDropRider(e.Extra + "."); stripped != e.Extra+"." { // reduced to 0 by the hit: applied by hitAfterDamage
		e.Extra = strings.Trim(stripped, " .")
	}
	if restSave != "" {
		e.Extra = strings.TrimSpace(strings.Trim(e.Extra, " .") + ". " + strings.TrimSuffix(restSave, ".") + ".")
		e.Extra = strings.TrimPrefix(e.Extra, ". ")
	}
	return e, len(e.Conditions) > 0 || e.SpeedMode != "" || len(e.DisadvMode) > 0 || len(e.Effects) > 0 || e.Curse != nil || e.Wound != nil
}

// parseSaveEffect reads a saving throw effect from a stat block text. ok is false when there is none.
var saveMarginHeading = regexp.MustCompile(`Failure by (\d+) or More:`)
var severePoisonText = regexp.MustCompile(`(?i)^The target has the Poisoned condition for (\d+) minutes?\. While Poisoned, the target has the Unconscious condition, which ends early if the target takes any damage\.$`)

// severeSleepText: the same severe branch that adds only the Unconscious condition to the Failure's Poisoned (Pseudodragon Sting: "Failure
// by 5 or More: While Poisoned, the target also has the Unconscious condition, which ends early if the target takes damage or a creature
// within 5 feet of it takes an action to wake it."). The waking action is the GM's (a creature's action, then POST /api/gm/conditions).
var severeSleepText = regexp.MustCompile(`(?i)^While Poisoned, the target also has the Unconscious condition, which ends early if the target takes (?:any )?damage(?: or a creature within 5 feet of it takes an action to wake it)?\.$`)

// secondFailureText: a monster effect's second stage after its one repeated save ("The target repeats the save at the end of its next
// turn ... Second Failure: The target has the Unconscious condition for 1 minute. This effect ends for the target if it takes damage or a
// creature within 5 feet of it takes an action to wake it.", Brass Dragon Wyrmling Sleep Breath; "... the Petrified condition, instead of
// the Restrained condition, for 24 hours.", Cockatrice; "... the Petrified condition instead of the Restrained condition.", Basilisk,
// with no end; "The target has the Paralyzed condition, and it repeats the save at the end of each of its turns, ending the effect on
// itself on a success. After 1 minute, it succeeds automatically.", the silver dragons: the second condition keeps the repeated save
// for 1 minute at most). It is the condition owner's repeated save with a second stage (spellStage, conditionSave.Then), as for Sleep.
var secondFailureText = regexp.MustCompile(`(?i)^The target has the (\w+) condition(?:,? instead of the \w+ condition,?)?(?: for (\d+) (minute|hour)s?)?(, and it repeats the save at the end of each of its turns, ending the effect on itself on a success)?\.\s*(.*)$`)
var oneRepeatRe = regexp.MustCompile(`(?i)repeats the save at the end of its next turn|until the end of its next turn, (?:at which point|when) it (?:must )?repeats? the save`)
var secondFailureLimitText = regexp.MustCompile(`(?i)^After (\d+) (minute|hour)s?, it succeeds automatically\.?$`)
var secondFailureWakeText = regexp.MustCompile(`(?i)^This effect ends for the target if it takes damage(?: or a creature within 5 feet of it takes an action to wake it)?\.?$`)

// readSecondFailure: the stage a "Second Failure:" sets on the record of the first, when the first gives one condition and repeats its
// save once ("repeats the save at the end of its next turn"); ok is false when the text is anything else (it stays the GM's).
func (e *saveEffect) readSecondFailure(text, second string) bool {
	if len(e.Conditions) != 1 || !oneRepeatRe.MatchString(text) {
		return false
	}
	m := secondFailureText.FindStringSubmatch(strings.TrimSpace(second))
	if m == nil || !mfxConditions[strings.ToLower(m[1])] {
		return false
	}
	rounds := func(n, unit string) int {
		v, _ := strconv.Atoi(n)
		if strings.EqualFold(unit, "hour") {
			return v * 600
		}
		return v * 10
	}
	stage := spellStage{Then: strings.ToLower(m[1]), Repeats: m[4] != ""}
	if m[2] != "" {
		stage.ThenRounds = rounds(m[2], m[3])
	}
	switch rest := strings.TrimSpace(m[5]); {
	case rest == "":
	case secondFailureWakeText.MatchString(rest):
		stage.EndsOnDamage = true
	case stage.Repeats && secondFailureLimitText.MatchString(rest): // the repeated save ends it at the latest then
		l := secondFailureLimitText.FindStringSubmatch(rest)
		stage.ThenRounds = rounds(l[1], l[2])
	default:
		return false
	}
	e.Stage = &stage
	e.EndSave = true // the one repeat: a success ends it, a failure turns it into the second stage (condition_turns.go)
	return true
}

type severePoisonEffect struct {
	Margin int
	Rounds int
}

func (e *saveEffect) readSevereFailure(text string) string {
	m := saveMarginHeading.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	branch := mfxSection(text, m[0], "Success:", "Failure or Success:", "Second Failure:")
	if severeSleepText.MatchString(strings.TrimSpace(branch)) && e.Rounds > 0 && len(e.Conditions) == 1 && e.Conditions[0] == "poisoned" {
		if margin, err := strconv.Atoi(m[1]); err == nil && margin > 0 {
			e.SeverePoison = &severePoisonEffect{Margin: margin, Rounds: e.Rounds} // the Failure's Poisoned, with the Unconscious while it lasts
			return ""
		}
	}
	parts := severePoisonText.FindStringSubmatch(branch)
	if parts != nil {
		margin, marginErr := strconv.Atoi(m[1])
		minutes, durationErr := strconv.Atoi(parts[1])
		if marginErr == nil && durationErr == nil && margin > 0 && minutes > 0 && minutes <= 1440 {
			e.SeverePoison = &severePoisonEffect{Margin: margin, Rounds: minutes * 10}
			return ""
		}
	}
	return m[0] + " " + branch
}

// savePushRe: a failed save that moves the target ("the target is pushed up to 30 feet straight away from the dragon": the SRD
// bronze dragons' Repulsion Breath, the silver dragons' Cold Gale, the Air Elemental's Whirlwind).
var savePushRe = regexp.MustCompile(`(?i)\bthe target is (pushed) up to (\d+) (?:feet|ft\.?) straight (away from) (?:the [\w' -]+?|it|itself)\b`)

// savePushAndRe / savePushTailRe: the push taken out of its sentence ("..., and the target is pushed up to 20 feet straight away from the
// elemental and has the Prone condition." reads "..., and the target has the Prone condition."; "14 (4d6) Cold damage, and the target
// is pushed up to 30 feet straight away from the dragon." reads "14 (4d6) Cold damage.").
var (
	savePushAndRe  = regexp.MustCompile(`(?i)\bthe target is pushed up to \d+ (?:feet|ft\.?) straight away from (?:the [\w' -]+?|it|itself) and\b`)
	savePushTailRe = regexp.MustCompile(`(?i),? and the target is pushed up to \d+ (?:feet|ft\.?) straight away from (?:the [\w' -]+|it|itself)\.`)
)

// savePush: how far a failed save moves its target, and which way.
type savePush struct {
	Feet   int
	Toward bool
}

// applySavePush moves the target on the battle map through the one forced movement owner (forceToken), from the effect's source; with
// no map or no source the GM places it.
func (e saveEffect) applySavePush(lobby, target int) string {
	verb := map[bool]string{false: "pushed", true: "pulled"}[e.Push.Toward]
	way := map[bool]string{false: "away from", true: "toward"}[e.Push.Toward]
	name, from := creatureName(lobby, target), "its source"
	if e.SourceID != 0 {
		from = creatureName(lobby, e.SourceID)
		moved, onMap := forceToken(lobby, e.SourceID, target, e.Push.Feet, e.Push.Toward)
		switch {
		case onMap && moved == 0:
			return fmt.Sprintf("%s can't be %s: something is in the way", name, verb)
		case onMap:
			return fmt.Sprintf("%s is %s %d feet straight %s %s", name, verb, moved, way, from)
		}
	}
	return fmt.Sprintf("%s is %s up to %d feet straight %s %s (no battle map: the GM places it)", name, verb, e.Push.Feet, way, from)
}

// saveOptionsIntro: the sentences of a multi-effect entry before its first named effect ("The sphinx emits a magical roar. Whenever it
// roars, the roar has a different effect ..."): the GM's, beside the effect named in use.
func saveOptionsIntro(text string, opts []saveOption) string {
	if len(opts) == 0 {
		return ""
	}
	i := strings.Index(text, opts[0].Name+".")
	if i <= 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text[:i]), ":"))
}

// castRiders: the sentences of an entry that casts a spell besides the casting sentence ("If the spell kills the target, its soul is
// trapped in the night hag's soul bag ...", "The spell ends early if the hag starts or ends its turn in Bright Light."): the spell's own
// effect is the cast owner's; these are the GM's.
func castRiders(text string) string {
	var kept []string
	for _, s := range sentenceSplitRe.Split(text, -1) {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))
		if s != "" && !mfxSpellRe.MatchString(s) && !strings.Contains(s, "spell save DC") && !strings.HasPrefix(s, "At Will") && !legendaryOnceRe.MatchString(s) {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, ". ")
}

// anySpeed stands for a Speed when only the wording of a move is asked: large enough that "half its Speed" doesn't round to 0.
func anySpeed(string) int { return 60 }

// onlyOwnMove: every sentence of the entry is its creature's own move (readSpecialMove) or the Legendary once-a-turn limit.
func onlyOwnMove(text string) bool {
	for _, s := range sentenceSplitRe.Split(strings.TrimSpace(text), -1) {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))
		if s == "" || legendaryOnceRe.MatchString(s) {
			continue
		}
		if _, ok := readSpecialMove(s, anySpeed); !ok {
			return false
		}
	}
	return true
}

// legendaryOnceSentenceRe: the sentence of a Legendary Action that limits it to once between the monster's turns ("The dragon can't
// take this action again until the start of its next turn."), applied by the Legendary Action owner (LAOnce): never the GM's.
var legendaryOnceSentenceRe = regexp.MustCompile(`(?i)\s*The [\w' -]+? can(?:'|’)t take this action again until the start of its next turn\.?`)

// saveSectionHeadRe: where a save entry's sections begin.
var saveSectionHeadRe = regexp.MustCompile(`(?:First Failure|Second Failure|Failure by \d+ or More|Failure or Success|Failure|Success):`)

// saveSetupText: the sentences of a saving throw entry outside its save line and its Failure / Success sections, which no save rule
// reads: how the area or the targets come about ("The djinni conjures a whirlwind at a point it can see within 120 feet", "The giant
// hurls a boulder at a point ...", "If the medusa sees its reflection in the Cone, the medusa must make this save."). They are the GM's,
// shown with the save's result whatever it is. A move the movement owner reads (readSpecialMove) is left out.
func saveSetupText(text string) string {
	loc := mfxSaveRe.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	end, depth := len(text), 0 // the save line ends at its first period outside parentheses
	for i := loc[1]; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '.':
			if depth <= 0 {
				end = i + 1
				i = len(text)
			}
		}
	}
	mid := text[end:]
	if h := saveSectionHeadRe.FindStringIndex(mid); h != nil {
		mid = mid[:h[0]]
	}
	var kept []string
	for _, s := range sentenceSplitRe.Split(strings.TrimSpace(text[:loc[0]])+" "+strings.TrimSpace(mid), -1) {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))
		if s == "" {
			continue
		}
		if _, ok := readSpecialMove(s, anySpeed); ok || legendaryOnceRe.MatchString(s) {
			continue
		}
		kept = append(kept, s)
	}
	return strings.Join(kept, ". ")
}

// restSaveRe: a saving throw that waits for a rest ("Whenever the Poisoned target finishes a Long Rest, it is subjected to the following
// effect. Constitution Saving Throw: DC 15. ...": SRD 5.2.1 Otyugh Bite). It is not rolled with the hit that sets it up: the text from
// that sentence on is the GM's (parseHitRider keeps it as the hit's rest), and parseSaveEffect reads no effect from it.
var restSaveRe = regexp.MustCompile(`(?i)\bWhenever the [\w ’'-]+? finishes a Long Rest, it is subjected to the following effect\.`)

func parseSaveEffect(text string) (saveEffect, bool) {
	if restSaveRe.MatchString(text) {
		return saveEffect{}, false
	}
	m := mfxSaveRe.FindStringSubmatch(text)
	if m == nil {
		return saveEffect{}, false
	}
	e := saveEffect{AbilityName: m[1], Ability: strings.ToLower(m[1][:3])}
	e.DC, _ = strconv.Atoi(m[2])
	e.NotHolding = woundGateRe.MatchString(text) // "doesn't already have an infernal wound"
	if humanoidGateRe.MatchString(text) {        // "If the target is a Humanoid, it is subjected to the following effect": no save for another type
		e.OnlyType = "humanoid"
	}
	head := text
	if i := strings.Index(head, "Failure"); i >= 0 {
		head = head[:i]
	}
	if a := autoSucceedTypesRe.FindStringSubmatch(head); a != nil { // "(Constructs and Undead succeed automatically)"
		for _, t := range regexp.MustCompile(`, |,? and |,? or `).Split(a[1], -1) {
			e.AutoSucceedTypes = append(e.AutoSucceedTypes, strings.ToLower(strings.TrimSuffix(strings.TrimSpace(t), "s")))
		}
	}
	ordinaryText := text
	if loc := saveMarginHeading.FindStringIndex(ordinaryText); loc != nil {
		ordinaryText = ordinaryText[:loc[0]]
	}
	fail := mfxSection(ordinaryText, "Failure:", "Success:", "Failure or Success:", "Second Failure:")
	if fail == "" {
		fail = mfxSection(text, "First Failure:", "Second Failure:", "Success:")
	}
	succ := mfxSection(text, "Success:", "Failure or Success:")
	if strings.Contains(text, "Failure or Success:") && strings.Index(text, "Success:") == strings.Index(text, "Failure or Success:")+len("Failure or ") {
		succ = "" // the only "Success:" is the one of "Failure or Success:"
	}
	for _, d := range immediateDamage(fail) { // never the damage "at the start of each of its turns": that ticks later (entry 63)
		e.Damage = append(e.Damage, damagePart{Dice: normDice(d[2]), Type: strings.ToLower(d[3])})
	}
	low := strings.ToLower(succ)
	e.HalfOnSuccess = strings.Contains(low, "half damage") || strings.Contains(low, "half as much damage")
	e.Immunity = strings.Contains(low, "immune to this")
	if e.Immunity {
		e.ImmunityRounds, e.ImmunityDuration = parseSuccessImmunityDuration(succ)
	}
	if x := mfxEmanRe.FindStringSubmatch(text); x != nil {
		e.Emanation, _ = strconv.Atoi(x[1])
	}
	if dtype, ok := hpMaxDrainOf(fail); ok {
		e.Drain = &hpDrain{Type: dtype, Regain: drainRegainRe.MatchString(fail)}
	} else if both := mfxSection(text, "Failure or Success:"); both != "" {
		if dtype, ok := hpMaxDrainOf(both); ok {
			e.Drain = &hpDrain{Type: dtype, OnSuccess: true, Regain: drainRegainRe.MatchString(both)}
		}
	}
	if objectOnFailureRe.MatchString(fail) { // the item state owner corrodes the object (item_state_2024.go): not the GM's text
		e.ItemPenalty = "any"
		if metalObjectOnlyRe.MatchString(text) {
			e.ItemPenalty = "metal"
		}
		fail = strings.TrimSpace(objectOnFailureRe.ReplaceAllString(fail, ""))
	}
	if m := savePushRe.FindStringSubmatch(fail); m != nil { // applied (applySavePush): the rest of the sentence is read as usual
		feet, _ := strconv.Atoi(m[2])
		e.Push = &savePush{Feet: feet, Toward: strings.EqualFold(m[3], "toward")}
		fail = savePushAndRe.ReplaceAllString(fail, "the target")
		fail = savePushTailRe.ReplaceAllString(fail, ".")
	}
	extra := e.readRider(fail)
	if branch := e.readSevereFailure(text); branch != "" {
		extra = append(extra, branch)
	}
	if second := mfxSection(text, "Second Failure:", "Success:", "Failure or Success:"); second != "" && !e.readSecondFailure(text, second) { // the stage after the first Failure's repeat save: the GM's unless readSecondFailure reads it
		extra = append(extra, "Second Failure (after the repeat save the first Failure gives): "+strings.TrimSuffix(second, "."))
	}
	both := strings.TrimSpace(legendaryOnceSentenceRe.ReplaceAllString(mfxSection(text, "Failure or Success:"), "")) // the once-a-turn rule is LAOnce's
	if both != "" && (e.Drain == nil || strings.Trim(hpMaxDrainSentenceRe.ReplaceAllString(both, ""), " .") != "") {
		var a saveEffect
		left := a.readRider(both)
		if len(a.Conditions) > 0 || a.SpeedMode != "" || len(a.DisadvMode) > 0 || len(a.Effects) > 0 || a.Curse != nil || a.Wound != nil {
			e.Always = &a // applied to every target that rolled the save (giveAlways); only the rest of the text is the GM's
			if len(left) > 0 {
				extra = append(extra, "Failure or Success: "+strings.Join(left, ". "))
			}
		} else {
			extra = append(extra, "Failure or Success: "+strings.TrimSuffix(both, "."))
		}
	}
	if e.Drain != nil { // applied (drainAfterDamage): not the GM's
		kept := extra[:0]
		for _, x := range extra {
			x = strings.Trim(hpMaxDrainSentenceRe.ReplaceAllString(x, ""), " ,.")
			if strings.Trim(mfxDamageRe.ReplaceAllString(x, ""), " ,.") == "" {
				continue // only the damage the effect rolls and deals was left of the sentence
			}
			if x != "Failure or Success:" {
				kept = append(kept, x)
			}
		}
		extra = kept
	}
	e.Extra = strings.Join(extra, ". ")
	if !strings.Contains(text, "Attack Roll:") {
		e.Setup = saveSetupText(text)
	}
	if strings.HasSuffix(e.Extra, ":") { // "One of the following effects occurs:" lists its options as the next entries of the stat block
		e.Extra += " (the options are the entries that follow this action in the stat block)"
	}
	return e, true
}

// characterAC is a character's Armor Class against a monster's attack (Slow, Shield of Faith, Haste, Smite of Protection).
func characterACFor(lobbyID, charID int) int {
	var ac int
	db.QueryRow(`SELECT COALESCE(ac,10) FROM characters WHERE id = $1`, charID).Scan(&ac)
	return ac + spellACAdjust(lobbyID, charID) + halfCoverBonus(lobbyID, charID)
}

func characterNameOf(charID int) string {
	var n string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&n)
	return n
}

// applySaveEffect makes each target roll the save and applies the result. source names the effect for condition records.
// It returns one line per target; immuneAfter lists the targets that succeeded on an effect that gives immunity.
type saveEffectCursor struct {
	Source    string
	Effect    saveEffect
	Targets   []int
	Apply     bool
	Rolled    map[int]int
	Next      int
	AnyFailed bool
}

func init() {
	combatResumeHandlers["save-effect-targets"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var c saveEffectCursor
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		lines, _ := runSaveEffectTargets(lobby, c)
		for _, line := range lines {
			recordGMAction(db, lobby, "monster_effect", line, line)
		}
		return map[string]interface{}{"result": lines}, nil
	}
}

func parseSuccessImmunityDuration(text string) (int, string) {
	m := mfxSuccessImmunityTimeRe.FindStringSubmatch(text)
	if m == nil {
		return 0, ""
	}
	count, err := strconv.Atoi(m[1])
	per := map[string]int{"round": 1, "minute": 10, "hour": 600, "day": 14400}[strings.TrimSuffix(strings.ToLower(m[2]), "s")]
	if err != nil || count < 1 || per == 0 || count > (1<<31-1)/per {
		return 0, ""
	}
	return count * per, m[1] + " " + strings.ToLower(m[2])
}

// rollForTarget uses the shared save rules without consuming any expendable
// defense during a preview.
func (e saveEffect) rollForTarget(lobby, target, totalDamage int, apply bool) game.SaveResult {
	ctx := saveCtx{lobby: lobby, saver: target, ability: e.Ability, dc: e.DC,
		spell: e.IsSpell, against: e.Conditions, source: e.SourceID, fiendOrUndead: e.FromUndead, peek: !apply}
	if len(e.AutoSucceedTypes) > 0 { // "(Constructs and Undead succeed automatically)"
		for _, ct := range creatureTypeWords(creatureTypeOf(lobby, target)) {
			if slices.Contains(e.AutoSucceedTypes, ct) {
				ctx.autoSucceed = titleCase(ct) + "s succeed automatically on this save"
				break
			}
		}
	}
	return ctx.resist(saveStakes{Condition: len(e.Conditions) > 0, Damage: totalDamage})
}

// Save riders are game state. All three target kinds use this boundary.
func saveFailureMargin(sv game.SaveResult) int {
	if sv.Success || sv.Natural == 0 {
		return 0
	}
	return max(0, sv.Save.DC-sv.Total)
}

func (e saveEffect) giveSeverePoison(lobby, target int, source string) []string {
	source += " (severe failed save)"
	poison := condAdd{Condition: "poisoned", Source: source, SourceID: e.SourceID, Rounds: e.SeverePoison.Rounds, Magical: e.IsSpell}
	result := addCondition(lobby, target, poison)
	if result.Immune != "" {
		return []string{"not Poisoned (" + result.Immune + ")"}
	}
	if result.Err != nil || result.ID == 0 {
		addGMNotice(lobby, "Could not store the severe save's Poisoned condition for "+creatureName(lobby, target)+". The GM should review the failed save.")
		return nil
	}
	sleeper := poison
	sleeper.Condition = "unconscious"
	sleeper.Notes = notesEndsWithPoisonOrDamage
	sleep := addCondition(lobby, target, sleeper)
	if sleep.Immune != "" {
		return []string{"Poisoned", "not Unconscious (" + sleep.Immune + ")"}
	}
	if sleep.Err != nil || sleep.ID == 0 {
		addGMNotice(lobby, "Could not store the severe save's Unconscious condition for "+creatureName(lobby, target)+". The GM should review the failed save.")
		return []string{"Poisoned"}
	}
	return []string{fmt.Sprintf("Poisoned for %d minute(s) and Unconscious while Poisoned or until damaged", poison.Rounds/10)}
}

// drainAfterDamage: the effect's Hit Point maximum drain for one damage part a target took (dtype, dealt), and the source's regain.
// Every save route calls it after each part it deals (the character route here, runMonsterSaveEffect, applySaveEffectOnCompanion).
func (e saveEffect) drainAfterDamage(lobby, target int, saved bool, dtype string, dealt int) string {
	if e.Drain == nil || dealt <= 0 || (saved && !e.Drain.OnSuccess) {
		return ""
	}
	if e.Drain.Type != "" && !strings.EqualFold(e.Drain.Type, dtype) {
		return ""
	}
	note := reduceHPMaximum(lobby, target, dealt)
	if e.Drain.Regain {
		note += drainRegain(lobby, e.SourceID, dealt)
	}
	return strings.TrimSpace(note)
}

func (e saveEffect) finishTarget(lobby, target int, source string, apply, saved bool, failedBy int) (parts []string, immune bool) {
	if !apply {
		return nil, false
	}
	parts = e.giveAlways(lobby, target, source)
	if !saved && e.ItemPenalty != "" { // the creature's armor or weapon corrodes (item_state_2024.go)
		if line := strings.TrimSpace(corrodeCarriedObject(lobby, target, e.ItemPenalty == "metal")); line != "" {
			parts = append(parts, line)
		}
	}
	if !saved && e.Push != nil {
		parts = append(parts, e.applySavePush(lobby, target))
	}
	if !saved && e.Kills {
		parts = append(parts, e.killOnFailure(lobby, target))
	}
	if !saved && len(e.OnFailure) > 0 {
		parts = append(parts, e.giveFailureMechanics(lobby, target, source)...) // attached mechanics (monster_mechanics_2024.go)
	}
	if !saved {
		if e.SeverePoison != nil && failedBy >= e.SeverePoison.Margin {
			return append(parts, e.giveSeverePoison(lobby, target, source)...), false
		}
		return append(parts, e.giveConditions(lobby, target, source)...), false
	}
	if e.Immunity {
		if e.ImmunityRounds <= 0 || e.ImmunityName == "" || e.SourceID >= 0 {
			return append(parts, "[GM: this success grants immunity. Confirm its duration and track it for this source and action.]"), false
		}
		if err := storeEffectImmunity(lobby, target, e.SourceID, e.ImmunityName, e.ImmunityRounds); err != nil {
			return append(parts, "[GM: the server couldn't store this success immunity. Track it for "+e.ImmunityDuration+" for this source and action.]"), false
		}
		return append(parts, "immune to it for "+e.ImmunityDuration), true
	}
	return parts, false
}

// saveDropRe / saveOtherwiseDamageRe: a failure that drops a target with few enough Hit Points to 0 and otherwise deals its damage.
var (
	saveDropRe            = regexp.MustCompile(`(?i)^\s*If the target has (\d+) Hit Points or fewer, it drops to 0 Hit Points\.?\s*$`)
	saveOtherwiseDamageRe = regexp.MustCompile(`(?i)^\s*Otherwise, the target takes \d+ \(\d+d\d+(?:\s*[+−-]\s*\d+)?\) [A-Za-z]+ damage\.?\s*$`)
	// saveKillRe: "The target dies, and the wisp regains 10 (3d6) Hit Points." (the regain is optional)
	saveKillRe = regexp.MustCompile(`(?i)^\s*The target dies(?:, and the [\pL '’-]+ regains \d+ \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) Hit Points)?\.?\s*$`)
)

// killOnFailure: a failed save's "The target dies": a character through the Hit Point owner (killCharacter), a monster at 0 Hit Points,
// a companion at 0; then the source regains what the text says (drainRegain: the monster Hit Point owner).
func (e saveEffect) killOnFailure(lobby, tid int) string {
	name := creatureName(lobby, tid)
	var err error
	switch {
	case isCompanionCombatID(tid):
		if c, ok := loadCompanion(tid - companionIDBase); ok {
			_, _, err = changeCompanionHP(c.ID, func(v game.Vitals) game.Vitals { return v.SetHP(0) }, nil)
		}
	case tid > 0:
		var warded bool
		if _, warded, err = killInstantly(tid); warded { // Death Ward negates an effect that kills instantly without damage (hit_points_2024.go)
			return fmt.Sprintf("%s is protected by Death Ward: the death is negated, and the spell ends.", name)
		}
	default:
		err = setMonsterHP(lobby, tid, 0)
	}
	if err != nil {
		return fmt.Sprintf("[%s dies by this effect: not recorded (%s); the GM applies it]", name, notFoundOrDBError(err, "no such creature"))
	}
	text := name + " dies"
	if e.KillRegain != "" {
		text += drainRegain(lobby, e.SourceID, game.RollDamage(e.KillRegain, false))
	}
	return text
}

// dropsToZero: on a failed save, a target with DropAtOrBelow Hit Points or fewer drops to 0 Hit Points (each target kind through its own
// Hit Point owner: setHP, setMonsterHP, changeCompanionHP), and the effect's damage is not dealt to it. Every target route asks it before
// the damage (the character's, runMonsterSaveEffect, applySaveEffectOnCompanion). It is not damage: no Concentration save, no resistance.
func (e saveEffect) dropsToZero(lobby, tid int, saved bool) bool {
	if saved || e.DropAtOrBelow <= 0 {
		return false
	}
	hp, _ := creatureHP(lobby, tid)
	return hp > 0 && hp <= e.DropAtOrBelow
}

// dropToZero applies it (apply) and says so.
func (e saveEffect) dropToZero(lobby, tid int, apply bool) string {
	name := creatureName(lobby, tid)
	if !apply {
		return fmt.Sprintf("%s has %d Hit Points or fewer: it would drop to 0 Hit Points (not applied)", name, e.DropAtOrBelow)
	}
	var err error
	switch {
	case isCompanionCombatID(tid):
		if c, ok := loadCompanion(tid - companionIDBase); ok {
			_, _, err = changeCompanionHP(c.ID, func(v game.Vitals) game.Vitals { return v.SetHP(0) }, nil)
		}
	case tid > 0:
		_, err = setHP(tid, 0)
	default:
		err = setMonsterHP(lobby, tid, 0)
	}
	if err != nil {
		return fmt.Sprintf("[%s should drop to 0 Hit Points (it has %d or fewer): not recorded (%s); the GM applies it]", name, e.DropAtOrBelow, notFoundOrDBError(err, "no such creature"))
	}
	return fmt.Sprintf("%s has %d Hit Points or fewer and drops to 0 Hit Points", name, e.DropAtOrBelow)
}

func applySaveEffect(lobbyID int, source string, e saveEffect, targets []int, apply bool) (lines []string, immuneAfter []int) {
	if e.Wound != nil && e.NotHolding { // a creature that already has the wound is not subjected to it: no save
		var kept []int
		for _, t := range targets {
			if holdsWound(lobbyID, t) {
				lines = append(lines, fmt.Sprintf("%s already has a wound: not subjected to %s's effect, no saving throw.", creatureName(lobbyID, t), source))
			} else {
				kept = append(kept, t)
			}
		}
		if targets = kept; len(targets) == 0 {
			return lines, nil
		}
	}
	if e.OnlyType != "" { // a gate on the target's creature type: another type is not subjected to the effect and rolls nothing
		var kept []int
		for _, t := range targets {
			if ct := creatureTypeOf(lobbyID, t); ct == "" || slices.Contains(creatureTypeWords(ct), e.OnlyType) {
				kept = append(kept, t)
			} else {
				lines = append(lines, fmt.Sprintf("%s isn't a %s: not subjected to %s's effect, no saving throw.", creatureName(lobbyID, t), titleCase(e.OnlyType), source))
			}
		}
		if targets = kept; len(targets) == 0 {
			return lines, nil
		}
	}
	rolled := map[int]int{}
	for i, p := range e.Damage {
		rolled[i] = game.RollDamage(p.Dice, false)
		if i == 0 && e.SourceID != 0 { // a weakened source's die comes off its damage roll (effect_records_2024.go)
			if dx, _ := damageRollAdjust(lobbyID, e.SourceID, rolled[i], false); dx != 0 {
				rolled[i] += dx
			}
		}
	}
	return runSaveEffectTargets(lobbyID, saveEffectCursor{Source: source, Effect: e, Targets: targets, Apply: apply, Rolled: rolled})
}
func runSaveEffectTargets(lobbyID int, c saveEffectCursor) (lines []string, immuneAfter []int) {
	source, e, targets, apply, rolled := c.Source, c.Effect, c.Targets, c.Apply, c.Rolled
	anyFailed := c.AnyFailed
	for index := c.Next; index < len(targets); index++ {
		tid := targets[index]
		if tid < 0 { // a monster or NPC of the fight (monster_save_targets.go)
			saved := prepareMonsterSaveEffect(lobbyID, source, e, tid, rolled, apply)
			anyFailed = anyFailed || !saved.Saved
			if apply && !saved.Saved {
				e.handOverOnFailure(lobbyID, tid) // a lasting remainder is the GM's open item (gm_handover_2024.go)
			}
			next := c
			next.Next = index + 1
			next.AnyFailed = anyFailed
			restore, err := armCombatFrame(lobbyID, "save-effect-targets", next)
			if err != nil {
				addGMNotice(lobbyID, "An effect could not save its remaining targets; no further automatic effects were run.")
				return lines, immuneAfter
			}
			l, immune := runMonsterSaveEffect(lobbyID, saved)
			restore()
			if immune {
				immuneAfter = append(immuneAfter, tid)
			}
			lines = append(lines, l)
			if combatResolutionPaused(lobbyID) {
				return lines, immuneAfter
			}
			continue
		}
		if isCompanionCombatID(tid) { // a companion (companion_spell_targets_2024.go): the stat-block save and the companion's own Hit Points
			l, failed, immune := applySaveEffectOnCompanion(lobbyID, source, e, tid, rolled, apply)
			anyFailed = anyFailed || failed
			if apply && failed {
				e.handOverOnFailure(lobbyID, tid)
			}
			if immune {
				immuneAfter = append(immuneAfter, tid)
			}
			lines = append(lines, l)
			continue
		}
		if tid <= 0 {
			continue
		}
		name := characterNameOf(tid)
		if why := etherealBlock(lobbyID, tid); why != "" { // Plate Armor of Etherealness
			lines = append(lines, why)
			continue
		}
		if apply && e.IsSpell && e.SingleTarget {
			if absorbed, note := staffAbsorbs(lobbyID, tid, e.SpellLevel); absorbed { // Staff of the Magi
				lines = append(lines, name+": "+note)
				continue
			}
		}
		// the one saving throw function (save_2024.go): items against spells, Protection from Poison and Dwarven Resilience against
		// being Poisoned, Fey Ancestry against being Charmed, Foresight, Holy Aura, Danger Sense ...
		sv := e.rollForTarget(lobbyID, tid, 0, apply)
		saved := sv.Success
		var protect []string
		if apply && !saved {
			if note := scarabPreserves(tid, e.IsSpell && strings.EqualFold(e.School, "necromancy"), e.FromUndead); note != "" {
				saved = true
				protect = append(protect, note)
			}
		}
		anyFailed = anyFailed || !saved
		if apply && !saved {
			e.handOverOnFailure(lobbyID, tid)
		}
		// a failure the Scarab of Protection turned reads "fails" here and says so in the parts after it
		line := sv.Line(name)
		if saved && e.IsSpell && e.SpellLevel <= 7 && ringTurns(tid) { // Ring of Spell Turning: no effect on a success
			line += ": Ring of Spell Turning: the spell has no effect on you."
			if apply && e.SingleTarget {
				line += turnSpellBack(lobbyID, tid, source, e, rolled)
			}
			lines = append(lines, line)
			continue
		}
		var parts []string
		parts = append(parts, protect...)
		evasion := e.HalfOnSuccess && creatureEvasion(lobbyID, tid, e.Ability)
		dropped := e.dropsToZero(lobbyID, tid, saved)
		if dropped {
			parts = append(parts, e.dropToZero(lobbyID, tid, apply))
		}
		for i, p := range e.Damage {
			if dropped {
				break
			}
			d := game.SaveDamage(rolled[i], saved, e.HalfOnSuccess, evasion)
			if d <= 0 {
				continue
			}
			if apply {
				text, dealt := damageCharacterDealt(tid, d, p.Type)
				parts = append(parts, text)
				if drained := e.drainAfterDamage(lobbyID, tid, saved, p.Type, dealt); drained != "" {
					parts = append(parts, drained)
				}
			} else {
				parts = append(parts, fmt.Sprintf("%d %s damage (not applied)", d, p.Type))
			}
		}
		if evasion {
			parts = append(parts, "Evasion")
		}
		riders, immune := e.finishTarget(lobbyID, tid, source, apply, saved, saveFailureMargin(sv))
		parts = append(parts, riders...)
		if immune {
			immuneAfter = append(immuneAfter, tid)
		}
		if len(parts) > 0 {
			line += ": " + strings.Join(parts, "; ")
		}
		lines = append(lines, line)
	}
	if e.Setup != "" {
		lines = append(lines, "The GM handles how this comes about: "+e.Setup) // gm-one-shot: setup text of how a stat block effect comes about, not a lasting effect
	}
	if e.Extra != "" && (anyFailed || strings.HasPrefix(e.Extra, "Failure or Success")) {
		if len(e.Damage) > 0 || len(e.Conditions) > 0 { // the text repeats what was just applied; only the rest is the GM's
			prefix := "Preview: the GM handles the rest of this text (movement, pushes, other effects): "
			if apply {
				prefix = "The damage and conditions above are applied; the GM handles the rest of this text (movement, pushes, other effects): "
			}
			lines = append(lines, prefix+e.Extra)
		} else {
			lines = append(lines, "For the GM to apply on a failure: "+e.Extra)
		}
	}
	return lines, immuneAfter
}

// effectImmune: the creature succeeded on this monster's effect that grants immunity (F70).
func effectImmune(lobbyID, targetID, monsterID int, entry string) bool {
	for _, e := range spellEffectsOn(lobbyID, targetID) {
		if !strings.EqualFold(e.Detail, entry) {
			continue
		}
		if e.Kind == "effect_immune" && e.CasterID == monsterID { // the granting source only
			return true
		}
	}
	return false
}

// charactersInEmanation lists the characters and active companions within feet of a monster on the battle map (nil without a map).
func charactersInEmanation(lobbyID, monsterID, feet int) []int {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return nil
	}
	src, ok := m.token(monsterID)
	if !ok {
		return nil
	}
	var out []int
	for _, t := range m.Tokens {
		if t.ID <= 0 || tokenDistanceFeet(src, t) > feet {
			continue
		}
		if _, ok := activeCompanionInCampaign(lobbyID, t.ID); t.ID < companionIDBase || ok {
			out = append(out, t.ID)
		}
	}
	return out
}

// monsterSpellNumbers reads the spell save DC and attack bonus from an action text or the monster's Spellcasting action.
func monsterSpellNumbers(text string, meta *monsterMeta) (dc, atk int) {
	if m := mfxSpellDCRe.FindStringSubmatch(text); m != nil {
		dc, _ = strconv.Atoi(m[1])
	}
	if m := mfxSpellAtkRe.FindStringSubmatch(text); m != nil {
		atk, _ = strconv.Atoi(strings.ReplaceAll(m[1], "−", "-"))
	}
	if meta != nil && (dc == 0 || atk == 0) {
		for _, a := range meta.Actions {
			if strings.HasPrefix(a.Name, "Spellcasting") {
				d2, a2 := monsterSpellNumbers(a.Text, nil)
				if dc == 0 {
					dc = d2
				}
				if atk == 0 {
					atk = a2
				}
			}
		}
	}
	if atk == 0 && dc > 0 {
		atk = dc - 8 // a stat block's spell attack bonus is its save DC - 8
	}
	return
}

// resolveMonsterSpell casts a spell from the SRD data at characters.
// use carries the GM's choice where the spell needs one (Command's word: approach, drop, flee, grovel or halt; grovel by default).
func resolveMonsterSpell(r *http.Request, lobbyID, monsterID int, monsterName, spellName string, level, dc, atk int, targets []int, selfCast, apply bool, use string) []string {
	if len(targets) == 1 && targets[0] == monsterID { // aimed at itself alone: a willing target, no save is rolled against its own spell
		selfCast = true
	}
	if why := monsterCastBlocked(lobbyID, monsterID); why != "" { // Antimagic Field (spells_more_2024.go)
		return []string{fmt.Sprintf("%s tries to cast %s, but %s.", monsterName, spellName, why)}
	}
	wardNote := ""
	if apply {
		wardNote = strings.TrimSpace(endWardsOnOffenseOf(lobbyID, monsterID)) // casting a spell ends its Invisibility and Sanctuary
	}
	spellLevel := level
	if sp, ok := srdSpellsMemory[normalizeSpellSlug(spellName)]; ok && spellLevel < sp.Level {
		spellLevel = sp.Level
	}
	// Starting another Concentration cast ends the old one BEFORE new effects,
	// even when the spell name is the same or all targets are shielded.
	if sp, ok := srdSpellsMemory[normalizeSpellSlug(spellName)]; ok && sp.Concentration && apply {
		if err := startMonsterConcentration(lobbyID, monsterID, sp.Name); err != nil {
			return []string{monsterName + " could not start the cast; no new spell effects were applied."}
		}
	}
	kept, notes := magicShieldedTargets(lobbyID, monsterID, spellLevel, targets) // Antimagic Field, Globe of Invulnerability
	if len(targets) > 0 && len(kept) == 0 && !selfCast {
		return append([]string{fmt.Sprintf("%s casts %s.", monsterName, spellName)}, notes...)
	}
	out := append(resolveMonsterSpellRules(r, lobbyID, monsterID, monsterName, spellName, level, dc, atk, kept, selfCast, apply, use), notes...)
	if wardNote != "" {
		out = append(out, wardNote)
	}
	return out
}

func resolveMonsterSpellRules(r *http.Request, lobbyID, monsterID int, monsterName, spellName string, level, dc, atk int, targets []int, selfCast, apply bool, use string) []string {
	slug := normalizeSpellSlug(spellName)
	sp, ok := srdSpellsMemory[slug]
	if !ok {
		// gm-one-shot: a spell the server has no data for; nothing is known of how long it lasts
		return []string{fmt.Sprintf("%s casts %s (the GM resolves it).", monsterName, spellName)}
	}
	if level < sp.Level {
		level = sp.Level
	}
	head := fmt.Sprintf("%s casts %s", monsterName, sp.Name)
	if level > sp.Level {
		head += fmt.Sprintf(" (level %d)", level)
	}
	// a spell that fills an area with fog (obscured_areas_2024.go): at the GM's point, else on its first target, else on the monster
	var point *mapSquare
	if v, pt := monsterAreaPoint.Load(lobbyID); pt {
		sq := v.(mapSquare)
		point = &sq
	}
	if line, handled := castObscuringSpell(lobbyID, monsterID, slug, sp.Name, level, point, targets, apply); handled {
		if apply && !strings.Contains(line, "could not be recorded") {
			markExecuted(r)
		}
		return []string{head + ". " + line}
	}
	if line, handled := castMagicalDarknessSpell(lobbyID, monsterID, slug, point, apply); handled { // the Darkness spell's Sphere (monster_darkness_2024.go)
		if apply && !strings.Contains(line, "could not be recorded") {
			markExecuted(r)
		}
		return []string{head + ". " + line}
	}
	// a lasting effect or a heal: the effect a character's cast of the spell has (monster_cast_effects_2024.go)
	if lines, handled := monsterCastLasting(r, lobbyID, monsterID, sp, slug, head, level, dc, targets, selfCast, apply); handled {
		return lines
	}
	if lines, handled := monsterCastHealing(r, lobbyID, monsterID, sp, slug, head, level, targets, selfCast, apply); handled {
		return lines
	}
	if lines, handled := monsterCastEndsConditions(r, lobbyID, monsterID, sp, slug, head, targets, selfCast, apply, use); handled {
		return lines
	}
	if selfCast {
		if apply {
			monsterHandoverSpell(lobbyID, monsterID, slug, nil) // a lasting spell is the GM's open item (gm_handover_2024.go)
		}
		return []string{head + " on itself. The GM resolves the spell's effect."}
	}
	areaAimed := false
	if v, pt := monsterAreaPoint.Load(lobbyID); pt && sp.AoEShape != "" && !isSpellAttack(sp) { // aimed at a point (O-5)
		aim := pointToken(v.(mapSquare))
		targets, _ = monsterSpellArea(lobbyID, monsterID, sp, aim, nil)
		if len(targets) == 0 {
			return []string{head + fmt.Sprintf(" at %s: no character stands in the area.", aim.Name)}
		}
		areaAimed = true
	}
	if len(targets) == 0 {
		return []string{head + ": name the targets (target or target_ids) to resolve it."}
	}
	if lines, handled := monsterCastDirect(r, lobbyID, monsterID, sp, slug, head, level, dc, targets, apply); handled { // Magic Missile, the Power Words
		return lines
	}
	dice := sp.DamageDice
	if d, ok := sp.DamageAtSlotLevel[strconv.Itoa(level)]; ok && d != "" {
		dice = d
	}
	if castDealsNoDamage(slug) { // dice dealt later or never by the cast (Geas, Dream: class_helpers.go), as for a character's cast
		dice = ""
	}
	lines := []string{head + "."}
	if isSpellAttack(sp) || slug == "command" {
		// a spell attack roll and Command are resolved by code that reads a character's row (name, Armor Class, Hit Points, staff
		// absorption): given a companion it printed a blank name, AC 0 and 0 damage as a HIT. A companion is not resolved here; the GM is told
		var rest []int
		for _, tid := range targets {
			if tid >= companionIDBase {
				name := "a companion"
				if c, ok := activeCompanionInCampaign(lobbyID, tid); ok {
					name = companionDisplayName(c)
				}
				lines = append(lines, fmt.Sprintf("%s: %s is not resolved by the server for a companion - nothing was rolled or applied.", name, sp.Name))
				addGMNotice(lobbyID, fmt.Sprintf("%s was the target of %s, but a spell attack roll or Command against a companion is not automated: nothing was rolled or applied. Resolve it by hand (POST /api/gm/companion for damage).", name, sp.Name))
				continue
			}
			rest = append(rest, tid)
		}
		if targets = rest; len(targets) == 0 {
			return lines
		}
	}
	if isSpellAttack(sp) {
		count, beamDice, _ := beamPlan(slug, sp.Level, level, 0)
		if count == 0 {
			count, beamDice = 1, dice
		}
		if count == 1 && len(targets) == 1 {
			if absorbed, note := staffAbsorbs(lobbyID, targets[0], level); absorbed {
				return append(lines, characterNameOf(targets[0])+": "+note)
			}
		}
		for i := 0; i < count; i++ {
			tid := targets[i%len(targets)]
			// the one attack function (attack_2024.go): conditions on both sides, every source of Advantage and Disadvantage,
			// Sanctuary, Bless and Bane, the Critical Hit. Nov 14: this path had only the target's defenses, and its hit line
			// left the Disadvantage note out.
			sa := attackCtx{lobby: lobbyID, attacker: monsterID, target: tid, name: "Spell attack at " + characterNameOf(tid), bonus: atk,
				spell: true, melee: spellRangeFeet(sp.Range) >= 0 && spellRangeFeet(sp.Range) <= 5}
			if apply {
				markExecuted(r) // the spell attack is made at a target (a miss or a blocked one still uses it)
			}
			if lost, note := sa.blocked(); lost {
				lines = append(lines, note)
				continue
			}
			rolled, out, rollNote := sa.roll()
			if !out.Hit {
				lines = append(lines, rolled.Line(sa.name, out)+rollNote)
				continue
			}
			dmg := game.RollDamage(normDice(beamDice), out.Critical)
			res := fmt.Sprintf("%d %s damage", dmg, strings.ToLower(sp.DamageType))
			if apply {
				res = damageCharacter(tid, dmg, strings.ToLower(sp.DamageType), out.Critical)
			}
			lines = append(lines, rolled.Line(sa.name, out)+rollNote+", "+res)
		}
		return lines
	}
	if slug == "command" { // Wisdom save; on a failure the target obeys the word on its next turn (the same record as a player's Command)
		word := "grovel"
		if w := commandWordRe.FindString(strings.ToLower(use)); w != "" {
			word = w
		}
		if len(targets) == 1 {
			if absorbed, note := staffAbsorbs(lobbyID, targets[0], level); absorbed {
				return append(lines, characterNameOf(targets[0])+": "+note)
			}
		}
		limit := 1 + level - 1 // one creature, one more per slot level above 1
		if limit < 1 {
			limit = 1
		}
		for i, tid := range targets {
			if i >= limit {
				lines = append(lines, fmt.Sprintf("(Command at level %d reaches %d creature(s); %s isn't affected.)", level, limit, characterNameOf(tid)))
				continue
			}
			name := characterNameOf(tid)
			sv := (&saveCtx{lobby: lobbyID, saver: tid, ability: "wis", dc: dc, spell: true, source: monsterID}).roll()
			markExecuted(r) // the save was rolled
			if sv.Success {
				lines = append(lines, sv.Line(name)+".")
				continue
			}
			if apply {
				addSpellEffect(lobbyID, tid, monsterID, "Command", "commanded", word, 1, "", 0)
			}
			lines = append(lines, fmt.Sprintf("%s and must obey \"%s\": on its next turn it %s.", sv.Line(name), titleCase(word), commandText[word]))
		}
		return lines
	}
	if sp.SavingThrow != "" {
		e := saveEffect{Ability: saveAbilityKey(sp.SavingThrow), DC: dc}
		e.AbilityName = map[string]string{"str": "Strength", "dex": "Dexterity", "con": "Constitution", "int": "Intelligence", "wis": "Wisdom", "cha": "Charisma"}[e.Ability]
		if dice != "" {
			e.Damage = []damagePart{{Dice: normDice(dice), Type: strings.ToLower(sp.DamageType)}}
			e.HalfOnSuccess = strings.Contains(strings.ToLower(sp.Description), "half as much damage")
		}
		if cs, ok := conditionSpells[slug]; ok {
			e.Conditions, e.Rounds, e.EndSave, e.Escapes = cs.Conditions, cs.Rounds, cs.EndSave, cs.escapes(dc)
			if st, ok := spellStages[slug]; ok {
				e.Stage = &st
			}
		} else if cc, ok := castConditionOf(sp); ok && !cc.OnHit { // the condition a damaging spell gives on a failed save, read from its text (spell_cast_conditions_2024.go)
			e.Conditions, e.EndSave = []string{cc.Condition}, cc.Repeat != ""
			switch {
			case cc.Rounds > 0:
				e.Rounds = cc.Rounds
			case cc.End != riderEndNone:
				e.Rounds = 1 // until the caster's next turn: one round of the monster's
			default:
				e.Rounds = spellSpanRounds(sp)
			}
		}
		if len(e.Damage) == 0 && len(e.Conditions) == 0 {
			e.Extra = sp.Name + ": apply the spell's effect on a failed save"
		}
		e.IsSpell, e.SpellLevel, e.School, e.SourceID = true, level, sp.School, monsterID
		e.SpellSlug = slug
		e.FromUndead = monsterTypeOf(lobbyID, monsterID) == "undead"
		if sp.AoEShape != "" && !areaAimed { // the spell's area on the battle map (F61)
			kept, note := areaFilter(lobbyID, monsterID, sp, targets)
			if note != "" {
				lines = append(lines, strings.TrimSpace(note))
			}
			if m, ok := activeEncounterMap(lobbyID); ok && len(targets) > 0 {
				if aim, ok := m.token(targets[0]); ok {
					if _, added := monsterSpellArea(lobbyID, monsterID, sp, aim, kept); len(added) > 0 {
						all, _ := monsterSpellArea(lobbyID, monsterID, sp, aim, nil)
						seen := map[int]bool{}
						for _, id := range kept {
							seen[id] = true
						}
						for _, id := range all {
							if !seen[id] {
								kept = append(kept, id)
							}
						}
						lines = append(lines, fmt.Sprintf("Also in the area of %s: %s.", sp.Name, strings.Join(added, ", "))) // O-5
					}
				}
			}
			targets = kept
		}
		if need := spellCreatureType[slug]; need != "" { // a spell that names a creature type (Hold Person: a Humanoid): published text, SRD 5.2.1
			var eligible []int
			for _, tid := range targets {
				if have := creatureTypeOf(lobbyID, tid); !strings.EqualFold(have, need) {
					who := creatureName(lobbyID, tid)
					if have == "" {
						lines = append(lines, fmt.Sprintf("%s: %s did not roll for it.", who, sp.Name))
						addGMNotice(lobbyID, fmt.Sprintf("The creature type of %s is unknown, so whether %s (%s only) can affect it is not decided: nothing was rolled for it.", who, sp.Name, titleCase(need)))
					} else {
						lines = append(lines, fmt.Sprintf("%s isn't a %s, so %s has no effect on it.", who, titleCase(need), sp.Name))
					}
					continue
				}
				eligible = append(eligible, tid)
			}
			if targets = eligible; len(targets) == 0 {
				return lines
			}
		}
		e.SingleTarget = sp.AoEShape == "" && len(targets) == 1
		l, _ := applySaveEffect(lobbyID, monsterName+" ("+sp.Name+")", e, targets, apply)
		if apply && len(targets) > 0 {
			markExecuted(r) // the saves were rolled (a success, or an immune target, still uses it)
		}
		return append(lines, l...)
	}
	if apply {
		monsterHandoverSpell(lobbyID, monsterID, slug, targets) // a lasting spell is the GM's open item (gm_handover_2024.go)
	}
	return append(lines, "The GM resolves the spell's effect.")
}

// resolveMonsterEffectText resolves what a stat block entry says (a Legendary Action, an action without an attack roll).
func resolveMonsterEffectText(r *http.Request, lobbyID, monsterID int, meta *monsterMeta, entryName, text string, targets []int, use string, apply bool, depth int) []string {
	name := creatureName(lobbyID, monsterID)
	var lines []string
	var notes []string
	if depth == 0 {
		if e, ok := metaEntry(meta, entryName); ok && len(e.Mechanics) > 0 {
			text = rulesText(e) // the words its attached mechanics cover are theirs (monster_mechanics_2024.go)
		}
		if line, blocked, ok := allyGrant(lobbyID, monsterID, meta, entryName, targets, apply); ok { // an attached ally_grant: before anything else, so a refusal changes nothing
			if blocked {
				return blockedBeforeRoll(r, line)
			}
			notes = append(notes, line)
			if apply {
				markExecuted(r)
			}
		}
		if note := grantSpecialMove(lobbyID, monsterID, entryName, text, apply); note != "" { // the entry's own move (creature_movement_2024.go)
			notes = append(notes, note)
		}
		if line, ok := obscuringZone(r, lobbyID, monsterID, meta, entryName, targets, apply); ok { // an attached obscuring_zone
			notes = append(notes, line)
		}
		if own := monsterOwnUse(lobbyID, monsterID, meta, entryName, apply); len(own) > 0 { // an attached teleport or self_condition
			notes = append(notes, own...)
			if apply {
				markExecuted(r) // the server applied it: a limited use is counted (monster_day_uses.go)
			}
		}
		if strings.TrimSpace(text) == "" { // every word is an attached mechanic's: nothing is left for the text rules
			return append([]string{fmt.Sprintf("%s uses %s.", name, entryName)}, notes...)
		}
	}
	// one action that offers several saving throw effects ("one of the following rays", "at random"): the GM names the one that happened
	// with use; none is guessed, and nothing is rolled until it is named
	if opts := splitSaveOptions(text); len(opts) >= 2 && depth == 0 {
		chosen := -1
		for i, o := range opts {
			if u := strings.TrimSpace(use); u != "" && strings.EqualFold(u, o.Name) {
				chosen = i
			}
		}
		rolled := ""
		if chosen < 0 && strings.TrimSpace(use) == "" { // an attached random_choice rolls for it (monster_mechanics_2024.go)
			chosen, rolled = rollForOption(meta, entryName, opts)
		}
		if chosen < 0 {
			names := make([]string, len(opts))
			for i, o := range opts {
				names[i] = o.Name
			}
			return append(blockedBeforeRoll(r, fmt.Sprintf("%s offers %d effects (%s). Name the one that happened in use (the GM rolls or picks it as the text says). Nothing was rolled.", entryName, len(opts), strings.Join(names, ", "))), notes...)
		}
		lines := resolveMonsterEffectText(r, lobbyID, monsterID, meta, entryName+": "+opts[chosen].Name, opts[chosen].Text, targets, "", apply, depth+1)
		if rolled != "" { // the roll first, then what it did
			lines = append([]string{rolled}, lines...)
		}
		if intro := saveOptionsIntro(text, opts); intro != "" {
			if apply {
				monsterHandoverEntry(lobbyID, monsterID, entryName, intro, targets) // a lasting remainder is the GM's open item (gm_handover_2024.go)
			}
			lines = append(lines, "The GM handles the rest of the entry: "+intro)
		}
		return append(lines, notes...)
	}
	// an attack of the stat block
	if m := mfxAttackRe.FindStringSubmatch(text); m != nil {
		atk := m[1]
		if m[2] != "" && use != "" && strings.EqualFold(strings.TrimSpace(use), m[2]) {
			atk = m[2]
		}
		if len(targets) == 0 {
			return append([]string{fmt.Sprintf("%s: name the target of the %s attack (target).", entryName, atk)}, notes...)
		}
		txt, _, ok := resolveMonsterAttack(withMonsterUse(r, use), lobbyID, fmt.Sprintf("#%d", monsterID), atk, strconv.Itoa(targets[0]), apply)
		if !ok {
			txt = fmt.Sprintf("%s makes one %s attack (the GM rolls it).", name, atk)
		}
		return append([]string{txt}, notes...)
	}
	// a spell cast several times, one of them replaced by the option the GM picks with "use" ("casts Fireball twice ... It can
	// replace one Fireball with Hold Monster or Wall of Fire": monster_replace_2024.go). The option takes the first target when it
	// is a single-target spell, the remaining copies the others; "use" naming the replaced spell is no replacement.
	if rule, ok := replaceRuleOf(text); ok && depth == 0 && rule.Replaced != "" {
		m := mfxSpellRe.FindStringSubmatch(text)
		if m != nil && strings.EqualFold(strings.TrimSpace(m[1]), rule.Replaced) {
			if game.Key(use) == game.Key(rule.Replaced) {
				use = ""
			}
			if o, ok := rule.option(use); ok && o.Spell != "" {
				lvl, _ := strconv.Atoi(m[2])
				dc, atk := monsterSpellNumbers(text, meta)
				times := 1
				if strings.Contains(text, " twice") {
					times = 2
				}
				optTargets, rest := targets, targets
				if sp, ok := srdSpellsMemory[normalizeSpellSlug(o.Spell)]; ok && sp.AoEShape == "" && len(targets) > 1 {
					optTargets, rest = targets[:1], targets[1:]
				}
				lines = append(lines, resolveMonsterSpell(r, lobbyID, monsterID, name, o.Spell, o.Level, dc, atk, optTargets, false, apply, "")...)
				for i := 0; i < times-rule.Count; i++ {
					lines = append(lines, resolveMonsterSpell(r, lobbyID, monsterID, name, m[1], lvl, dc, atk, rest, false, apply, "")...)
				}
				return append(lines, notes...)
			}
		}
	}
	// the spell the GM picks with "use" from the ones the text offers (legendary_more_2024.go)
	if spName, lvl, ok := monsterSpellFromUse(text, use); ok && depth == 0 {
		dc, atk := monsterSpellNumbers(text, meta)
		if note := spellListNote(text, spName); note != "" { // what the stat block changes in the spell is the GM's
			notes = append(notes, fmt.Sprintf("The GM applies what the stat block adds to %s: %s.", spName, note)) // gm-one-shot: a spell list's note, applied when cast
		}
		// a limited spell called by another entry (a Legendary Action that casts): monsterAction already reserved its own entry's use
		if rec := execIn(r); apply && rec != nil && !rec.dayHandled {
			if key, limit := monsterDayUse(srdMonsterEntry{Name: entryName, Text: text}, spName); key != "" {
				if !reserveDayUse(lobbyID, monsterID, key, limit) {
					return append([]string{dayUseRefusal(loadMonsterState(lobbyID, monsterID), name, srdMonsterEntry{Name: entryName, Text: text}, spName, key, limit)}, notes...)
				}
				before := rec.count()
				lines = append(lines, resolveMonsterSpell(r, lobbyID, monsterID, name, spName, lvl, dc, atk, targets, false, apply, use)...)
				if !rec.usedSince(before) {
					releaseDayUse(lobbyID, monsterID, key)
				}
				return append(lines, notes...)
			}
		}
		lines = append(lines, resolveMonsterSpell(r, lobbyID, monsterID, name, spName, lvl, dc, atk, targets, false, apply, use)...)
		return append(lines, notes...)
	}
	// an entry that offers several spells ("casts Bless, Healing Word, or Sanctuary", a Spellcasting entry's lists): the GM names the one
	// cast with use; none is guessed
	choices := castChoiceList(text)
	for _, t := range spellTiersOf(text) {
		choices = append(choices, t.Spells...)
	}
	if len(choices) >= 2 && depth == 0 {
		why := ""
		if u := strings.TrimSpace(use); u != "" {
			why = fmt.Sprintf(" %q isn't one of them.", u)
		}
		return append(blockedBeforeRoll(r, fmt.Sprintf("%s casts one of %s.%s Name the spell cast in use. Nothing was cast.", entryName, strings.Join(choices, ", "), why)), notes...)
	}
	// a spell
	if m := mfxSpellRe.FindStringSubmatch(text); m != nil && !strings.Contains(m[1], "Spellcasting") {
		lvl, _ := strconv.Atoi(m[2])
		dc, atk := monsterSpellNumbers(text, meta)
		times := 1
		if strings.Contains(text, " twice") {
			times = 2
		}
		for i := 0; i < times; i++ {
			lines = append(lines, resolveMonsterSpell(r, lobbyID, monsterID, name, m[1], lvl, dc, atk, targets, strings.Contains(text, "on itself"), apply, use)...)
		}
		if rest := castRiders(text); rest != "" {
			if apply {
				monsterHandoverEntry(lobbyID, monsterID, entryName, rest, targets)
			}
			lines = append(lines, "The GM handles the rest of the entry: "+rest)
		}
		return append(lines, notes...)
	}
	// a saving throw effect in the text itself (its damage type the GM's choice: withChosenDamageType, as for an attack)
	if chosen, problem := withChosenDamageType(entryName, text, use); problem != "" && mfxSaveRe.MatchString(text) {
		return append(blockedBeforeRoll(r, problem), notes...)
	} else {
		text = chosen
	}
	if e, ok := parseSaveEffect(text); ok {
		if issue := monsterSaveTargetIssue(r, lobbyID, monsterID, meta, text, targets); issue != "" {
			return append(blockedBeforeRoll(r, "Nothing was rolled. [GM: "+issue+"]"), notes...)
		}
		if len(targets) == 0 && e.Emanation > 0 {
			targets = charactersInEmanation(lobbyID, monsterID, e.Emanation)
		}
		if _, pt := monsterAreaPoint.Load(lobbyID); len(targets) == 0 && pt {
			targets, _ = monsterAreaFilter(lobbyID, monsterID, text, nil) // aimed at the GM's point: everyone in the area (O-5)
		}
		if len(targets) == 0 {
			return append(blockedBeforeRoll(r, fmt.Sprintf("%s: name the targets (target or target_ids) of the %s saving throw (DC %d). Nothing was rolled.", entryName, e.AbilityName, e.DC)), notes...)
		}
		e.SourceID = monsterID
		e.FromUndead = monsterTypeOf(lobbyID, monsterID) == "undead"           // a harmful effect from an Undead (Scarab of Protection)
		kept, areaNote := monsterAreaFilter(lobbyID, monsterID, text, targets) // a Line, Cone or Sphere on the battle map (F58)
		if areaNote != "" {
			notes = append(notes, areaNote)
		}
		if len(kept) == 0 {
			return append([]string{fmt.Sprintf("%s uses %s; no target stands in the area.", name, entryName)}, notes...)
		}
		// F70: a creature that succeeded on an effect that grants immunity ("immune to this mummy's Dreadful Glare for 24 hours")
		// is skipped; the immunity used to be announced and then forgotten
		var fresh []int
		var already []string
		for _, t := range kept {
			if notAffectedRe.MatchString(text) && affectedBySource(lobbyID, t, monsterID, effectKindsIn(e)) { // "each creature that isn't currently affected by this breath"
				notes = append(notes, fmt.Sprintf("%s is already affected by %s's %s: not a target.", creatureName(lobbyID, t), name, entryName))
				continue
			}
			if effectImmune(lobbyID, t, monsterID, entryName) {
				already = append(already, creatureName(lobbyID, t))
				continue
			}
			fresh = append(fresh, t)
		}
		if len(already) > 0 {
			notes = append(notes, fmt.Sprintf("Immune to %s's %s after a successful save: %s.", name, entryName, strings.Join(already, ", ")))
		}
		if len(fresh) == 0 {
			if apply {
				markExecuted(r) // used, and every target is immune: the use is spent
			}
			return append([]string{fmt.Sprintf("%s uses %s; every target is immune to it.", name, entryName)}, notes...)
		}
		e.ImmunityName = entryName
		e.OnFailure = failureMechanicsOf(meta, entryName) // attached mechanics a failed save gives (finishTarget)
		l, _ := applySaveEffect(lobbyID, name+" ("+entryName+")", e, fresh, apply)
		if apply {
			markExecuted(r)
		}
		lines = append([]string{fmt.Sprintf("%s uses %s.", name, entryName)}, l...)
		return append(lines, notes...)
	}
	if lines, handled := resolveMonsterSwallow(r, lobbyID, monsterID, entryName, text, targets, apply); handled {
		return append(lines, notes...)
	}
	if lines, handled := resolveMonsterPull(r, lobbyID, monsterID, entryName, text, targets, apply); handled { // Reel (creature_movement_2024.go)
		return append(lines, notes...)
	}
	if lines, handled := resolveMonsterDirectDamage(r, lobbyID, monsterID, name, entryName, text, targets, apply); handled {
		return append(lines, notes...)
	}
	// another action of the stat block ("uses Lightning Strike")
	if m := usesAnotherEntry(text); m != nil && meta != nil && depth < 2 {
		for _, a := range append(append([]srdMonsterEntry{}, meta.Actions...), meta.BonusActions...) {
			if strings.EqualFold(stripUsage(a.Name), strings.TrimSpace(m[1])) {
				if isAttackEntry(a.Text) {
					if len(targets) == 0 {
						return append([]string{fmt.Sprintf("%s: name the target of %s (target).", entryName, a.Name)}, notes...)
					}
					txt, _, ok := resolveMonsterAttack(withMonsterUse(r, use), lobbyID, fmt.Sprintf("#%d", monsterID), a.Name, strconv.Itoa(targets[0]), apply)
					if ok {
						return append([]string{txt}, notes...)
					}
				}
				// a limited action called by another entry (a Legendary Action's "uses Howl"): checked and reserved here, kept only if the
				// server executed it
				if key, limit := monsterDayUse(a, ""); key != "" && apply {
					if !reserveDayUse(lobbyID, monsterID, key, limit) {
						return append([]string{dayUseRefusal(loadMonsterState(lobbyID, monsterID), name, a, "", key, limit)}, notes...)
					}
					before := execIn(r).count()
					out := resolveMonsterEffectText(r, lobbyID, monsterID, meta, a.Name, a.Text, targets, use, apply, depth+1)
					if !execIn(r).usedSince(before) {
						releaseDayUse(lobbyID, monsterID, key)
					}
					return append(out, notes...)
				}
				return append(resolveMonsterEffectText(r, lobbyID, monsterID, meta, a.Name, a.Text, targets, use, apply, depth+1), notes...)
			}
		}
	}

	// a light the monster switches on and off (Ignited Illumination): the lighting owner reads it (monster_lighting_2024.go)
	if line, ok := resolveLightToggle(lobbyID, monsterID, name, entryName, text, use, apply); ok {
		return append([]string{line}, notes...)
	}
	// Shape-Shift: the form named in use is recorded; entries limited to a form read it (monster_shape_shift_2024.go)
	if line, ok := resolveShapeShift(lobbyID, monsterID, name, entryName, text, use, apply); ok {
		return append([]string{line}, notes...)
	}
	// a standard action the entry takes: Disengage recorded, a choice named in use (monster_standard_actions_2024.go)
	if line, ok := resolveMonsterStandardAction(lobbyID, monsterID, name, entryName, text, use, apply); ok {
		return append([]string{line}, notes...)
	}
	// Magical Darkness: a token-anchored emanation the visibility owner reads (monster_darkness_2024.go)
	if magicalDarknessRe.MatchString(text) {
		return resolveMagicalDarkness(r, lobbyID, monsterID, entryName, text, apply, notes)
	}
	// fog, ink, smoke that fills a Cube or Sphere centered on the monster (obscured_areas_2024.go)
	if obscuringTextRe.MatchString(text) {
		return append(resolveObscuringText(r, lobbyID, monsterID, entryName, text, targets, apply), notes...)
	}
	if len(notes) > 0 && onlyOwnMove(text) { // "The dog teleports up to 40 feet ...": the movement owner granted it; nothing is left
		return append([]string{fmt.Sprintf("%s uses %s.", name, entryName)}, notes...)
	}
	rest := strings.TrimSpace(legendaryOnceSentenceRe.ReplaceAllString(text, "")) // the once-a-turn limit is LAOnce's
	if apply {
		monsterHandoverEntry(lobbyID, monsterID, entryName, rest, targets) // a lasting part is the GM's open item (gm_handover_2024.go)
	}
	return append([]string{fmt.Sprintf("%s uses %s: %s (the GM resolves it).", name, entryName, rest)}, notes...)
}

// isAttackEntry: the stat block entry is an attack roll.
func isAttackEntry(text string) bool {
	return strings.Contains(text, "Attack Roll:")
}

// stripUsage drops "(Recharge 5–6)", "(3/Day)" and the like from an entry name.
func stripUsage(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return strings.TrimSpace(name[:i])
	}
	return strings.TrimSpace(name)
}

// targetIDsFrom turns a target (name or id) and target_ids into character ids in the campaign.
func targetIDsFrom(lobbyID int, target string, ids []int) []int {
	var out []int
	seen := map[int]bool{}
	if target != "" {
		if id, _, _, ok := characterByNameOrID(lobbyID, target); ok {
			out = append(out, id)
			seen[id] = true
		} else if id := monsterTargetByName(lobbyID, target); id != 0 { // another monster of the fight
			out = append(out, id)
			seen[id] = true
		} else if c, ok := companionByNameOrID(lobbyID, target); ok { // a companion of the campaign: its stat block makes the save
			out = append(out, c.CombatID)
			seen[c.CombatID] = true
		}
	}
	for _, id := range ids {
		if id >= companionIDBase && !seen[id] {
			if c, ok := companionByNameOrID(lobbyID, strconv.Itoa(id)); ok && c.CombatID == id {
				out = append(out, id)
				seen[id] = true
			}
			continue
		}
		if id < 0 && !seen[id] && monsterInFight(lobbyID, id) {
			out = append(out, id)
			seen[id] = true
			continue
		}
		if id > 0 && !seen[id] {
			var n int
			if db.QueryRow(`SELECT count(*) FROM characters WHERE id = $1 AND lobby_id = $2`, id, lobbyID).Scan(&n) == nil && n > 0 {
				out = append(out, id)
				seen[id] = true
			}
		}
	}
	return out
}

// mfxUntilEndAnchored: the text ends the condition at the end of a named creature's next turn ("until the end of the hag's next turn"), not
// the target's own ("its", "the target's"). The record keeps the source creature's id and ends at that creature's turn end.
func mfxUntilEndAnchored(lf string) bool {
	m := mfxUntilEndRe.FindStringSubmatch(strings.NewReplacer("’", "'").Replace(lf))
	return m != nil && m[1] != "target"
}

// creatureTypeOf is the creature type of a monster of the fight or of a companion ("humanoid", "beast" ...), from its stat block; "" when
// it cannot be read.
// creatureTypeWords: a creature type and its tags as single lower-case words ("fiend (demon)": fiend, demon).
func creatureTypeWords(ct string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(ct), func(r rune) bool { return r == ' ' || r == '(' || r == ')' || r == ',' }) {
		out = append(out, strings.TrimSuffix(w, "s"))
	}
	return out
}

func creatureTypeOf(lobbyID, id int) string {
	if id >= companionIDBase {
		c, ok := activeCompanionInCampaign(lobbyID, id)
		if !ok {
			return ""
		}
		return catalogCreatureType(c.Form)
	}
	if id > 0 {
		return characterCreatureType(lobbyID, id)
	}
	return monsterTypeOf(lobbyID, id)
}

// characterCreatureType reads the campaign member's species. Wild Shape and
// Polymorph retain creature type under SRD 5.2.1 (pp. 43, 153).
func characterCreatureType(lobbyID, id int) string {
	var species string
	if err := db.QueryRow(`SELECT COALESCE(species,'') FROM characters WHERE id = $1 AND lobby_id = $2`, id, lobbyID).Scan(&species); err != nil {
		return ""
	}
	it, ok := originByName(srdOrigins().Species.Items, strings.TrimSpace(species))
	if !ok {
		return ""
	}
	for _, f := range it.Fields {
		if f.Label == "Creature Type" {
			return strings.ToLower(strings.TrimSpace(f.Text))
		}
	}
	return ""
}
