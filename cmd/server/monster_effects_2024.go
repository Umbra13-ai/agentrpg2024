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
	mfxSaveRe = regexp.MustCompile(`(Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) Saving Throw: DC (\d+)`)
	// SRD 5.2.1 p.185: a nonspell phenomenon is magical when its rule labels it so.
	mfxMagicalRe                 = regexp.MustCompile(`(?i)(?:^|[.!?]\s+)this (?:action|attack|effect|trait) is magical(?:[.!?]|$)`)
	mfxConditionDamageImmunityRe = regexp.MustCompile(`(?i)unless the target has immunity to ([a-z]+) damage`)
	mfxSuccessImmunityTimeRe     = regexp.MustCompile(`(?i)immune to (?:this|the) [^.!?]*? for (?:the next )?(\d+) (rounds?|minutes?|hours?|days?)\b`)
	mfxAllSourcesImmunityRe      = regexp.MustCompile(`(?i)immune to the [^.!?]*? of all [^.!?]*? for (?:the next )?\d+ (?:rounds?|minutes?|hours?|days?)\b`)
	mfxDamageRe                  = regexp.MustCompile(`(\d+) \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage`)
	mfxCondRe                    = regexp.MustCompile(`has the ((?:[A-Z][a-z]+)(?:(?:, | and | or )[A-Z][a-z]+)*) conditions?`)
	mfxEscapeRe                  = regexp.MustCompile(`Grappled condition \(escape DC (\d+)\)`)
	mfxRestrainRe                = regexp.MustCompile(`(?i)(?:,? and it has the Restrained condition until the grapple ends|while Grappled, the target has the Restrained condition)`)
	mfxExhaustRe                 = regexp.MustCompile(`gains? (\d+) Exhaustion levels?`)
	mfxAttackRe                  = regexp.MustCompile(`makes one ([A-Z][A-Za-z’' -]+?)(?: or ([A-Z][A-Za-z’' -]+?))? attack`)
	mfxUsesRe                    = regexp.MustCompile(`uses (?:its )?([A-Z][A-Za-z’' -]+?)(?:[.,]| and | to |$)`)
	mfxSpellRe                   = regexp.MustCompile(`(?:uses Spellcasting to cast|casts) (?:the )?([A-Z][A-Za-z’' /-]+?)(?: spell)?(?: \(level (\d) version\))?(?: on itself)?(?: twice)?(?:[.,;]| requiring| using| on |$)`)
	mfxSpellDCRe                 = regexp.MustCompile(`spell save DC (\d+)`)
	mfxSpellAtkRe                = regexp.MustCompile(`([+−-]\d+) to hit with spell attacks`)
	mfxEmanRe                    = regexp.MustCompile(`(\d+)\s*-?\s*foot Emanation`)
	mfxUntilStartRe              = regexp.MustCompile(`until the start of (?:its|the (?:[^ ]+?)['’]s) next turn`) // the attacker's turn ("the empyrean's"); "the target's" is the target's own
	mfxUntilEndRe                = regexp.MustCompile(`until the end of the ([^ ]+?)['’]s next turn`)             // an end anchored to a named creature's turn; "the target's" is the target's own
	mfxEndSaveRe                 = regexp.MustCompile(`(?i)repeats the save at the end of each of its turns|at the end of each of its turns, (?:the target|it) repeats the save`)
	mfxAutoSucceedRe             = regexp.MustCompile(`(?i)\bAfter (\d+) minutes?, it succeeds automatically`)
	targetIsCreatureGateRe       = regexp.MustCompile(`(?i)\bIf the target is a creature,?(?:\s+(?:its|it)\b)?`)
	mfxDurationRe                = regexp.MustCompile(`(?i)(?:until the (?:start|end) of (?:its|the [^ ]+) next turn|for 1 (?:minute|hour)|for 24 hours)`)
	mfxConditions                = map[string]bool{"blinded": true, "charmed": true, "deafened": true, "frightened": true, "grappled": true, "incapacitated": true,
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
	Ability            string // dex, con ...
	Drain              *hpDrain
	AbilityName        string // Dexterity
	DC                 int
	Damage             []damagePart
	HalfOnSuccess      bool
	SeverePoison       *severePoisonEffect
	Conditions         []string
	Rounds             int                 // condition duration in rounds (0: until removed)
	UntilStart         bool                // "until the start of its next turn"
	UntilEnd           bool                // "until the end of the <creature>'s next turn": ends at the end of the SOURCE creature's turn (monsterFeaturesTurnEnd)
	EndSave            bool                // repeats the save at the end of each of its turns
	Exhaustion         int                 // Exhaustion levels on a failure
	EscapeDC           int                 // "the Grappled condition (escape DC 16)": the DC of the escape checks
	Escapes            []conditionEscape   // a spell's escape checks (conditionSpell.escapes: Entangle, Web)
	RestrainedToo      bool                // "and it has the Restrained condition until the grapple ends": Restrained that ends with the Grappled record
	SpeedMode          string              // "halved", "zero" or "minus": a Speed rider of the text (speed_owner_2024.go), applied as a marker for SpeedEnd
	SpeedEnd           riderEnd            // the Speed rider's duration (rider_duration_2024.go)
	Always             *saveEffect         // the conditions, Speed and Disadvantage of "Failure or Success:": given to every target that rolled the save
	DisadvMode         []string            // the roll riders of the text: "sap", "exposed", "attacks", "concentration", "save:int", "noreactions" (disadvantage_riders_2024.go)
	RollRiderEnd       map[string]riderEnd // the duration of each DisadvMode rider
	SpeedMinus         int                 // the feet of "its Speed decreases by 10 feet"
	ImmunityName       string              // stat-block entry key for persisted success immunity (set by the effect caller)
	Immunity           bool                // success grants immunity to this source and entry
	ImmunityAllSources bool                // "immune to the <entry> of all <kind>s": the immunity covers every source of the entry, not only the grantor
	ImmunityRounds     int                 // source duration, in combat rounds
	ImmunityDuration   string              // the parsed numeric duration for the result
	CondDamageImmunity string              // a damage Immunity excludes its condition rider
	CondMaxSize        string              // "If the target is a Huge or smaller creature, ... has the Prone condition": the conditions apply only to a target of this size or smaller
	CondIfAdvantage    bool                // "... and the darkmantle had Advantage on the attack roll": the conditions apply only when the roll had Advantage (enforced by the hit caller, which has the roll)
	Lasting            *lastingEffects     // "While Poisoned, the target can't regain Hit Points and takes ... at the start of each of its turns" (condition_lasting_effects_2024.go)
	Extra              string              // Failure text the server doesn't apply (for the GM)
	Emanation          int                 // feet, when the targets are in an Emanation from the monster
	IsMagical          bool                // a nonspell effect explicitly labeled magical by its stat block
	IsSpell            bool                // a spell: items that guard against spells work (legendary_more_2024.go)
	SpellLevel         int                 // its level (0: a cantrip)
	School             string              // its school (Necromancy: the Scarab of Protection)
	SingleTarget       bool                // aimed only at one creature, no area (Spell Absorption, Ring of Spell Turning's Reaction)
	SourceID           int                 // the monster it comes from
	Stage              *spellStage         // a condition spell's second stage (Sleep: spellStages)
	FromUndead         bool                // it comes from an Undead (the Scarab of Protection)
}

// conditionAdd is the condition record a failed save leaves (character or monster target): its duration, the repeated save, the note
// "until the start of its next turn", and for Grappled the escape checks against the stat block's escape DC.
func (e saveEffect) conditionAdd(cond, source string) condAdd {
	a := condAdd{Condition: cond, Source: source, Rounds: e.Rounds, SourceID: e.SourceID, Magical: e.IsSpell}
	if e.EndSave {
		a.EndSave = &conditionSave{Ability: e.Ability, DC: e.DC}
		if e.Stage != nil {
			a.EndSave.Then, a.EndSave.ThenRounds, a.EndSave.ThenEndsOnDamage, a.EndSave.ThenMagicalSleep = e.Stage.Then, e.Stage.ThenRounds, e.Stage.EndsOnDamage, e.Stage.MagicalSleep
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

// linkedCondition: the second condition of "Grappled (escape DC N), and it has the Restrained condition until the grapple ends": a Restrained
// record from the same source that the condition owner ends together with the Grappled record (conditionChange.remove).
func (e saveEffect) linkedCondition(cond, source string) (condAdd, bool) {
	if cond != "grappled" || !e.RestrainedToo {
		return condAdd{}, false
	}
	return condAdd{Condition: "restrained", Source: source, SourceID: e.SourceID, Magical: e.IsSpell, Notes: notesEndsWithGrapple}, true
}

// conditionsFor: the conditions of a failed save that this target gets. A condition gated by size ("If the target is a Huge or
// smaller creature ...") is not given to a bigger target: gated says so for the reply. The one rule for characters and monsters.
func (e saveEffect) conditionsFor(lobbyID, tid int) (conds []string, gated string) {
	if dtype := e.CondDamageImmunity; dtype != "" {
		if !attackDamageTypes[dtype] {
			return nil, "[GM: resolve the condition's unrecognized damage immunity prerequisite.]"
		}
		if creatureIsImmuneToDamage(lobbyID, tid, dtype) {
			return nil, fmt.Sprintf("not %s (immune to %s damage)", strings.Join(e.Conditions, ", "), dtype)
		}
	}

	if _, ok := sizeOrder[e.CondMaxSize]; ok && isCompanionCombatID(tid) { // the server does not know a companion's size: the GM checks
		return nil, fmt.Sprintf("not applied: %s only to a target of %s size or smaller (the GM checks the companion's size)", strings.Join(e.Conditions, ", "), titleCase(e.CondMaxSize))
	}
	if lim, ok := sizeOrder[e.CondMaxSize]; ok && sizeOrder[strings.ToLower(creatureSize(lobbyID, tid))] > lim {
		return nil, fmt.Sprintf("not %s (larger than %s)", strings.Join(e.Conditions, ", "), titleCase(e.CondMaxSize))
	}
	return e.Conditions, ""
}

// Use the same type mitigation sources that the HP owners use. Resistance
// (even when rounding leaves no damage) does not meet an Immunity prerequisite.
func creatureIsImmuneToDamage(lobby, target int, dtype string) bool {
	switch {
	case target < 0:
		_, kind := adjustMonsterDamage(monsterKeyOfCombatant(lobby, target), dtype, 2)
		return kind == "immune"
	case isCompanionCombatID(target):
		c, ok := activeCompanionInCampaign(lobby, target)
		if !ok {
			return false
		}
		_, kind := adjustMonsterDamage(c.Form, dtype, 2)
		return kind == "immune"
	default:
		return len(applyDamageResistance(target, 2, dtype).Immunities) > 0
	}
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
	if c := mfxCondRe.FindStringSubmatch(fail); c != nil {
		for _, name := range regexp.MustCompile(`, | and | or `).Split(c[1], -1) {
			if n := strings.ToLower(strings.TrimSpace(name)); mfxConditions[n] {
				e.Conditions = append(e.Conditions, n)
			}
		}
	}
	if len(e.Conditions) > 0 {
		for _, sentence := range sentenceSplitRe.Split(fail, -1) {
			if !mfxCondRe.MatchString(sentence) {
				continue
			}
			if gate := mfxConditionDamageImmunityRe.FindStringSubmatch(sentence); gate != nil {
				e.CondDamageImmunity = strings.ToLower(gate[1])
			}
			break
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
	case strings.Contains(lf, "for 1 minute"):
		e.Rounds = 10
	case strings.Contains(lf, "for 1 hour"):
		e.Rounds = 600
	}
	e.readSpeed(fail)
	_, gmDisadv := e.readDisadvantage(fail)
	extra = append(extra, gmDisadv...)
	e.EndSave = mfxEndSaveRe.MatchString(lf)
	autoLimit := false // the condition's limit was read from "After N minutes, it succeeds automatically"
	if m := mfxAutoSucceedRe.FindStringSubmatch(fail); m != nil && e.EndSave && e.Rounds == 0 && len(e.Conditions) > 0 {
		n, _ := strconv.Atoi(m[1])
		e.Rounds, autoLimit = n*10, true // the repeated save ends it at the latest then
	}
	if x := mfxEscapeRe.FindStringSubmatch(fail); x != nil {
		e.EscapeDC, _ = strconv.Atoi(x[1])
	}
	e.RestrainedToo = e.EscapeDC > 0 && mfxRestrainRe.MatchString(fail)
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
				if rest := strings.Trim(strings.TrimSpace(s[loc[1]:]), "."); rest != "" {
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
		if grappleLockoutTextRe.MatchString(s) {
			plain = grappleLockoutTextRe.ReplaceAllString(plain, "") // enforced at the attack entry (monsterAttackLockout): not the GM's
		}
		if attackDamageTypes[e.CondDamageImmunity] && mfxCondRe.MatchString(s) {
			plain = mfxConditionDamageImmunityRe.ReplaceAllString(plain, "")
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
		extra = append(extra, e.unappliedPart(s)) // the applied riders of the sentence are not repeated to the GM
	}
	return extra
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
		for _, re := range []*regexp.Regexp{disadvSapRe, exposedRe, disadvAttackRe, disadvConcRe, disadvSaveRe, noReactRe} {
			r = re.ReplaceAllString(r, "")
		}
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

var hitRiderChoiceRe = regexp.MustCompile(`(?i)choice of|determined by rolling|one of the following|\bcursed\b`)

// parseHitRider reads the conditions a stat-block hit gives besides its damage ("..., and the target has the Stunned condition until
// the start of the empyrean's next turn", "If the target is a Large or smaller creature, it has the Prone condition"): the same rules
// as a failed save (readRider), with no saving throw of their own. A hit with its own saving throw is the save owner's.
func parseHitRider(desc string) (saveEffect, bool) {
	i := strings.Index(desc, "Hit:")
	if i < 0 || mfxSaveRe.MatchString(desc) {
		return saveEffect{}, false
	}
	hit := desc[i+len("Hit:"):]
	if j := strings.Index(hit, "Hit or Miss:"); j >= 0 {
		hit = hit[:j]
	}
	if hitRiderChoiceRe.MatchString(hit) || hitRiderDeferredRe.MatchString(hit) {
		return saveEffect{}, false
	}
	var e saveEffect
	e.Extra = strings.Join(e.readRider(hit), ". ")
	if hpMaxDrainSentenceRe.MatchString(e.Extra) { // a hit's own: applied by hitAfterDamage on every hit route
		e.Extra = strings.Trim(hpMaxDrainSentenceRe.ReplaceAllString(e.Extra, ""), " .")
	}
	if stripped := stripBurnForCreatures(e.Extra); stripped != e.Extra {
		e.Extra = strings.Trim(stripped, " .")
	}
	if stripped := stripForcedMoves(e.Extra); stripped != e.Extra { // a push or pull: applied by hitAfterDamage
		e.Extra = strings.Trim(stripped, " .")
	}
	return e, len(e.Conditions) > 0 || e.SpeedMode != "" || len(e.DisadvMode) > 0
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
// the Restrained condition, for 24 hours.", Cockatrice). It is the condition owner's repeated save with a second stage (spellStage,
// conditionSave.Then), as for the Sleep spell.
var secondFailureText = regexp.MustCompile(`(?i)^The target has the (\w+) condition(?:, instead of the \w+ condition,)? for (\d+) (minute|hour)s?\.\s*(.*)$`)
var oneRepeatRe = regexp.MustCompile(`(?i)repeats the save at the end of its next turn|until the end of its next turn, at which point it (?:must )?repeats? the save`)
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
	rest := strings.TrimSpace(m[4])
	if rest != "" && !secondFailureWakeText.MatchString(rest) {
		return false
	}
	n, _ := strconv.Atoi(m[2])
	rounds := n * 10
	if strings.EqualFold(m[3], "hour") {
		rounds = n * 600
	}
	e.Stage = &spellStage{Then: strings.ToLower(m[1]), ThenRounds: rounds, EndsOnDamage: rest != ""}
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

func parseSaveEffect(text string) (saveEffect, bool) {
	m := mfxSaveRe.FindStringSubmatch(text)
	if m == nil {
		return saveEffect{}, false
	}
	e := saveEffect{AbilityName: m[1], Ability: strings.ToLower(m[1][:3]), IsMagical: mfxMagicalRe.MatchString(strings.TrimSpace(text))}
	e.DC, _ = strconv.Atoi(m[2])
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
	if !e.Immunity && mfxAllSourcesImmunityRe.MatchString(succ) {
		// "immune to the Stench of all troglodytes for 1 hour": one grant covers
		// every source of the named entry, not only the creature saved against.
		e.Immunity, e.ImmunityAllSources = true, true
	}
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
	extra := e.readRider(fail)
	if branch := e.readSevereFailure(text); branch != "" {
		extra = append(extra, branch)
	}
	if second := mfxSection(text, "Second Failure:", "Success:", "Failure or Success:"); second != "" && !e.readSecondFailure(text, second) { // the stage after the first Failure's repeat save: the GM's unless readSecondFailure reads it
		extra = append(extra, "Second Failure (after the repeat save the first Failure gives): "+strings.TrimSuffix(second, "."))
	}
	if both := mfxSection(text, "Failure or Success:"); both != "" && (e.Drain == nil || strings.Trim(hpMaxDrainSentenceRe.ReplaceAllString(both, ""), " .") != "") {
		var a saveEffect
		left := a.readRider(both)
		if len(a.Conditions) > 0 || a.SpeedMode != "" || len(a.DisadvMode) > 0 {
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
		spell: e.IsSpell, magical: e.IsMagical, against: e.Conditions, source: e.SourceID, fiendOrUndead: e.FromUndead, peek: !apply}
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
		if err := storeEffectImmunityKind(lobby, target, e.SourceID, e.ImmunityName, e.ImmunityRounds, e.ImmunityAllSources); err != nil {
			return append(parts, "[GM: the server couldn't store this success immunity. Track it for "+e.ImmunityDuration+" for this source and action.]"), false
		}
		if e.ImmunityAllSources {
			return append(parts, "immune to it from every source for "+e.ImmunityDuration), true
		}
		return append(parts, "immune to it for "+e.ImmunityDuration), true
	}
	return parts, false
}

func applySaveEffect(lobbyID int, source string, e saveEffect, targets []int, apply bool) (lines []string, immuneAfter []int) {
	rolled := map[int]int{}
	for i, p := range e.Damage {
		rolled[i] = game.RollDamage(p.Dice, false)
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
		evasion := e.Ability == "dex" && e.HalfOnSuccess && hasEvasion(tid)
		for i, p := range e.Damage {
			d := rolled[i]
			switch {
			case saved && (evasion || !e.HalfOnSuccess):
				d = 0
			case saved, evasion: // a success halves; Evasion halves a failure
				d /= 2
			}
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
		// effect_immune_all: one grant covers every source of the entry ("immune to
		// the Stench of all troglodytes"); effect_immune: the granting source only.
		if e.Kind == "effect_immune_all" || (e.Kind == "effect_immune" && e.CasterID == monsterID) {
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
	dice := sp.DamageDice
	if d, ok := sp.DamageAtSlotLevel[strconv.Itoa(level)]; ok && d != "" {
		dice = d
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
		}
		if len(e.Damage) == 0 && len(e.Conditions) == 0 {
			e.Extra = sp.Name + ": apply the spell's effect on a failed save"
		}
		e.IsSpell, e.SpellLevel, e.School, e.SourceID = true, level, sp.School, monsterID
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
	return append(lines, "The GM resolves the spell's effect.")
}

// resolveMonsterEffectText resolves what a stat block entry says (a Legendary Action, an action without an attack roll).
func resolveMonsterEffectText(r *http.Request, lobbyID, monsterID int, meta *monsterMeta, entryName, text string, targets []int, use string, apply bool, depth int) []string {
	name := creatureName(lobbyID, monsterID)
	var lines []string
	var notes []string
	if depth == 0 {
		if note := grantSpecialMove(lobbyID, monsterID, entryName, text, apply); note != "" { // the entry's own move (creature_movement_2024.go)
			notes = append(notes, note)
		}
		if note := shapeShift(lobbyID, monsterID, text, use, apply); note != "" { // a Shape-Shift form's Speeds (refused before: shapeShiftRefusal)
			notes = append(notes, note)
		}
		if note := monsterFalls(lobbyID, monsterID, text, apply); note != "" { // an entry that makes it fall (Drop)
			notes = append(notes, note)
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
		if chosen < 0 {
			names := make([]string, len(opts))
			for i, o := range opts {
				names[i] = o.Name
			}
			return append([]string{fmt.Sprintf("%s offers %d effects (%s). Name the one that happened in use (the GM rolls or picks it as the text says). Nothing was rolled.", entryName, len(opts), strings.Join(names, ", "))}, notes...)
		}
		return append(resolveMonsterEffectText(r, lobbyID, monsterID, meta, entryName+": "+opts[chosen].Name, opts[chosen].Text, targets, "", apply, depth+1), notes...)
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
		// a limited spell called by another entry (a Legendary Action that casts): monsterAction already reserved its own entry's use
		if rec := execIn(r); apply && rec != nil && !rec.dayHandled {
			if key, limit := monsterDayUse(srdMonsterEntry{Name: entryName, Text: text}, spName); key != "" {
				if !reserveDayUse(lobbyID, monsterID, key, limit) {
					return append([]string{dayUseRefusal(loadMonsterState(lobbyID, monsterID), name, srdMonsterEntry{Name: entryName, Text: text}, spName, key, limit)}, notes...)
				}
				before := rec.count()
				lines = append(lines, resolveMonsterSpell(r, lobbyID, monsterID, name, spName, lvl, dc, atk, targets, false, apply, use)...)
				if rec.count() == before {
					releaseDayUse(lobbyID, monsterID, key)
				}
				return append(lines, notes...)
			}
		}
		lines = append(lines, resolveMonsterSpell(r, lobbyID, monsterID, name, spName, lvl, dc, atk, targets, false, apply, use)...)
		return append(lines, notes...)
	}
	// an entry that offers several spells ("casts Bless, Healing Word, or Sanctuary"): the GM names the one cast with use; none is guessed
	if choices := castChoiceList(text); len(choices) >= 2 && depth == 0 {
		why := ""
		if u := strings.TrimSpace(use); u != "" {
			why = fmt.Sprintf(" %q isn't one of them.", u)
		}
		return append([]string{fmt.Sprintf("%s casts one of %s.%s Name the spell cast in use. Nothing was cast.", entryName, strings.Join(choices, ", "), why)}, notes...)
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
		return append(lines, notes...)
	}
	// a saving throw effect in the text itself
	if e, ok := parseSaveEffect(text); ok {
		if issue := monsterSaveTargetIssue(r, lobbyID, monsterID, meta, text, targets); issue != "" {
			if rec := execIn(r); rec != nil {
				rec.targetBlocked = true
			}
			return append([]string{"Nothing was rolled. [GM: " + issue + "]"}, notes...)
		}
		if len(targets) == 0 && e.Emanation > 0 {
			targets = charactersInEmanation(lobbyID, monsterID, e.Emanation)
		}
		if _, pt := monsterAreaPoint.Load(lobbyID); len(targets) == 0 && pt {
			targets, _ = monsterAreaFilter(lobbyID, monsterID, text, nil) // aimed at the GM's point: everyone in the area (O-5)
		}
		if len(targets) == 0 {
			return append([]string{fmt.Sprintf("%s: name the targets (target or target_ids) of the %s saving throw (DC %d). Nothing was rolled.", entryName, e.AbilityName, e.DC)}, notes...)
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
	if lines, handled := resolveMonsterDirectDamage(r, lobbyID, name, entryName, text, targets, apply); handled {
		return append(lines, notes...)
	}
	// another action of the stat block ("uses Lightning Strike")
	if m := mfxUsesRe.FindStringSubmatch(text); m != nil && meta != nil && depth < 2 {
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
					if execIn(r).count() == before {
						releaseDayUse(lobbyID, monsterID, key)
					}
					return append(out, notes...)
				}
				return append(resolveMonsterEffectText(r, lobbyID, monsterID, meta, a.Name, a.Text, targets, use, apply, depth+1), notes...)
			}
		}
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
	return append([]string{fmt.Sprintf("%s uses %s: %s (the GM resolves it).", name, entryName, text)}, notes...)
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
