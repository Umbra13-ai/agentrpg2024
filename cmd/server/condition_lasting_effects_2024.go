package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"
)

// A condition's lasting effects (play-test feedback log entry 63; I-GM-24, I-GM-23, S-ALDEN-10). A stat block's Failure (or hit) can
// give a condition and say what the target suffers while it has it: "The target has the Poisoned condition. While Poisoned, the target
// can't regain Hit Points and takes 21 (6d6) Poison damage at the start of each of its turns" (Pit Fiend Bite), "While Poisoned, the
// target can't regain Hit Points" (Bone Devil), "While Poisoned, the target takes 5 (1d10) Poison damage at the start of each of its
// turns" (Vrock Spores), "Until this poison ends, the target can't regain Hit Points" (Bearded Devil). Before, the reader counted every
// damage of a Failure as damage taken now (the Bite dealt its 6d6 at once and never again), and nothing stopped the healing.
//
// The rules here:
//   - immediateDamage is the damage of a text dealt now: never a damage "at the start (or end) of each of ... turns", which ticks later
//     (a recurring row, monster_recurring_damage_2024.go) or is the GM's. parseSaveEffect reads its damage through it.
//   - readLastingEffects reads the "While <Condition>, the target ..." sentence of a condition the same text gives: can't regain Hit Points,
//     and damage at the start of each of its turns.
//   - addLastingEffects writes them for a target that got the condition, as combat_effects rows gated on that condition record (column
//     gate_condition, with the record's source): a recurring_damage row the turn pipeline ticks through the Hit Point owners
//     (recurringDamageTurnStart, the gate checked by recurringDamageLive), and a no_healing row. They end with the record (the repeated
//     save, "After 1 minute, it succeeds automatically", Lesser Restoration ...): a row whose record is gone is dead and is deleted when read.
//   - cantRegainHP is the one answer to "can this creature regain Hit Points": healHP, healMonster and healCompanion read it.

// recurringTailRe: what follows a damage phrase that ticks later ("... Poison damage at the start of each of its turns", "... Acid damage
// plus 10 (3d6) Fire damage at the start of each of the remorhaz's turns").
var recurringTailRe = regexp.MustCompile(`(?i)^(?:\s*plus \d+ \([^)]*\) [A-Za-z]+ damage)*\s+at the (?:start|end) of each of `)

// immediateDamage: the mfxDamageRe matches of text dealt now (not the recurring ones).
func immediateDamage(text string) [][]string {
	var out [][]string
	for _, loc := range mfxDamageRe.FindAllStringSubmatchIndex(text, -1) {
		if recurringTailRe.MatchString(text[loc[1]:]) {
			continue
		}
		m := make([]string, len(loc)/2)
		for i := range m {
			if loc[2*i] >= 0 {
				m[i] = text[loc[2*i]:loc[2*i+1]]
			}
		}
		out = append(out, m)
	}
	return out
}

// lastingEffects: what a target suffers while it has Condition from the source of the text.
type lastingEffects struct {
	Condition string
	NoRegain  bool
	Tick      *recurringDamageSpec
	Also      []string // conditions the target also has while it has Condition ("While Frightened, the target has the Paralyzed condition")
	AlsoNotes string   // the note of those records (notesEndsWith + Condition; Unconscious that also ends on damage: notesEndsWithPoisonOrDamage)
	// DisadvAttacks: "While Deafened, the target also has Disadvantage on ... attack rolls" (Swarm of Ravens Cacophony): the roll rider
	// marker "disadv_attacks" (read by attackSources), a record noted AlsoNotes that ends with the condition's. "... on ability checks and
	// attack rolls" is also the marker "disadv_checks" (DisadvChecks), read by the check owner (check_2024.go).
	DisadvAttacks bool
	DisadvChecks  bool
}

var (
	lastingGateRe     = regexp.MustCompile(`(?i)^\s*(?:while (?:it has the )?([a-z]+)(?: condition)?|until this (poison) ends),\s*(?:the target|it)\s+`)
	lastingNoRegainRe = regexp.MustCompile(`(?i)can(?:'|’)t regain Hit Points`)
	lastingTickRe     = regexp.MustCompile(`(?i)takes (\d+) \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage at the start of each of its turns`)
	lastingRepeatRe   = regexp.MustCompile(`(?i),?\s*and it repeats the save at the end of each of its turns, ending the effect on itself on a success`)
	lastingJoinRe     = regexp.MustCompile(`(?i)^[\s,.]*(?:and[\s,.]*)*$`)
	// lastingAlsoRe: "(also) has the Paralyzed condition" in a "While <Condition>, the target ..." sentence
	lastingAlsoRe = regexp.MustCompile(`(?:also )?has the ([A-Z][a-z]+(?:(?:, | and )[A-Z][a-z]+)*) conditions?`)
	// lastingWakeRe: ", which ends early if the target takes (any) damage": an Unconscious that also ends on damage
	lastingWakeRe = regexp.MustCompile(`(?i),?\s*which ends early if the target takes (?:any )?damage`)
	// lastingDisadvAttacksRe: "(also) has Disadvantage on ability checks and attack rolls" in a "While <Condition>, the target ..." sentence
	lastingDisadvAttacksRe = regexp.MustCompile(`(?i)(?:also )?has Disadvantage on (?:ability checks and )?attack rolls`)
)

// readLastingEffects reads sentence as the lasting effects of one of conds (the conditions the same text gives). rest is the part of
// the sentence the server doesn't apply ("" when it applies all of it); nil when the sentence is not such a sentence.
func readLastingEffects(sentence string, conds []string) (l *lastingEffects, rest string) {
	m := lastingGateRe.FindStringSubmatch(sentence)
	if m == nil {
		return nil, sentence
	}
	cond := strings.ToLower(m[1])
	if m[2] != "" {
		cond = "poisoned" // "this poison": the Poisoned condition the text gives
	}
	given := false
	for _, c := range conds {
		given = given || c == cond
	}
	if !given {
		return nil, sentence
	}
	body := sentence[len(m[0]):]
	l = &lastingEffects{Condition: cond}
	if lastingNoRegainRe.MatchString(body) {
		l.NoRegain = true
		body = lastingNoRegainRe.ReplaceAllString(body, "")
	}
	if t := lastingTickRe.FindStringSubmatch(body); t != nil {
		spec := &recurringDamageSpec{Dice: normDice(t[2]), Type: strings.ToLower(t[3]), Condition: cond}
		fmt.Sscanf(t[1], "%d", &spec.Average)
		l.Tick = spec
		body = lastingTickRe.ReplaceAllString(body, "")
	}
	// "While Poisoned, the target has the Paralyzed condition" (Chuul Paralyzing Tentacles), "While Charmed, the target has the
	// Incapacitated condition" (Harpy): records that last as long as the condition's record (notesEndsWith). Grappled's Restrained is
	// the grapple's linked record (saveEffect.RestrainedToo), not read here.
	if a := lastingAlsoRe.FindStringSubmatch(body); a != nil && cond != "grappled" {
		for _, n := range regexp.MustCompile(`, | and `).Split(a[1], -1) {
			if c := strings.ToLower(strings.TrimSpace(n)); mfxConditions[c] && c != cond {
				l.Also = append(l.Also, c)
			}
		}
		if len(l.Also) > 0 {
			l.AlsoNotes = notesEndsWith + cond
			body = strings.Replace(body, a[0], "", 1)
			if cond == "poisoned" && len(l.Also) == 1 && l.Also[0] == "unconscious" && lastingWakeRe.MatchString(body) {
				l.AlsoNotes = notesEndsWithPoisonOrDamage // ends with the Poisoned record or when it takes damage (wakeOnDamageSleep)
				body = lastingWakeRe.ReplaceAllString(body, "")
			}
		}
	}
	if lastingDisadvAttacksRe.MatchString(body) {
		l.DisadvAttacks = true
		if l.AlsoNotes == "" {
			l.AlsoNotes = notesEndsWith + cond
		}
		if strings.Contains(strings.ToLower(lastingDisadvAttacksRe.FindString(body)), "ability checks") {
			l.DisadvChecks = true // the ability checks too: the check owner reads the marker
		}
		body = lastingDisadvAttacksRe.ReplaceAllString(body, "")
	}
	if !l.NoRegain && l.Tick == nil && len(l.Also) == 0 && !l.DisadvAttacks {
		return nil, sentence
	}
	body = lastingRepeatRe.ReplaceAllString(body, "") // the repeated save is the condition record's (EndSave)
	if lastingJoinRe.MatchString(body) {
		return l, ""
	}
	return l, strings.TrimSpace(sentence) // something else in it: the whole sentence stays the GM's to read, with the parts applied
}

// words: the reply's words for the effects ("can't regain Hit Points and takes 21 (6d6) poison damage at the start of each of its turns
// while Poisoned").
func (l lastingEffects) words() string {
	var parts []string
	if l.NoRegain {
		parts = append(parts, "can't regain Hit Points")
	}
	if l.Tick != nil {
		parts = append(parts, fmt.Sprintf("takes %d (%s) %s damage at the start of each of its turns", l.Tick.Average, l.Tick.Dice, l.Tick.Type))
	}
	if len(l.Also) > 0 {
		parts = append(parts, "has the "+joinNames(titleWordsAll(l.Also))+" condition")
	}
	if l.DisadvChecks {
		parts = append(parts, "has Disadvantage on ability checks and attack rolls")
	} else if l.DisadvAttacks {
		parts = append(parts, "has Disadvantage on attack rolls")
	}
	return strings.Join(parts, " and ") + " while " + titleCase(l.Condition)
}

// titleWordsAll: each condition name in title case.
func titleWordsAll(cs []string) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = titleCase(c)
	}
	return out
}

// addLastingEffects writes the effects for a target that has the condition from sourceID (rows replace the same source's earlier ones).
func addLastingEffects(lobbyID, targetID, sourceID int, l lastingEffects, sourceName string) string {
	write := func(kind, text string) bool {
		if _, err := db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND target_id=$2 AND source_id=$3 AND kind=$4 AND gate_condition=$5`,
			lobbyID, targetID, sourceID, kind, l.Condition); err != nil {
			log.Printf("lasting effects: couldn't replace the %s row for %d in campaign %d: %v", kind, targetID, lobbyID, err)
			return false
		}
		if _, err := db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text, gate_condition) VALUES ($1,$2,$3,$4,$5,$6)`,
			lobbyID, targetID, sourceID, kind, text, l.Condition); err != nil {
			log.Printf("lasting effects: couldn't record the %s row for %d in campaign %d: %v", kind, targetID, lobbyID, err)
			return false
		}
		return true
	}
	ok := true
	if l.NoRegain {
		ok = write("no_healing", fmt.Sprintf("Can't regain Hit Points while %s (%s)", titleCase(l.Condition), sourceName)) && ok
	}
	if l.Tick != nil {
		ok = write("recurring_damage", recurringDamageText(*l.Tick)) && ok
	}
	if !ok {
		return "[GM: the server couldn't record that the target " + l.words() + "; track it by hand.]"
	}
	return l.words()
}

// conditionHeldFrom: the target has the condition from that source (its record).
func conditionHeldFrom(lobbyID, targetID int, cond string, sourceID int) bool {
	for _, r := range loadConditionRecords(lobbyID, targetID) {
		if r.Condition == cond && r.SourceID == sourceID {
			return true
		}
	}
	return false
}

// gatedEffect is a combat_effects row with its gate.
type gatedEffect struct {
	ID       int
	SourceID int
	Kind     string
	Text     string
	Gate     string
}

// gatedEffectsOn reads the target's rows of kind ("" every kind) with their gates; a row whose condition record is gone is deleted
// and left out.
func gatedEffectsOn(lobbyID, targetID int, kind string) ([]gatedEffect, error) {
	rows, err := db.Query(`SELECT id, source_id, kind, text, gate_condition FROM combat_effects WHERE lobby_id=$1 AND target_id=$2 AND ($3 = '' OR kind = $3) ORDER BY id`,
		lobbyID, targetID, kind)
	if err != nil {
		return nil, err
	}
	var all []gatedEffect
	for rows.Next() {
		var g gatedEffect
		if err := rows.Scan(&g.ID, &g.SourceID, &g.Kind, &g.Text, &g.Gate); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, g)
	}
	rows.Close()
	var live []gatedEffect
	for _, g := range all {
		if g.Gate != "" && !conditionHeldFrom(lobbyID, targetID, g.Gate, g.SourceID) {
			db.Exec(`DELETE FROM combat_effects WHERE id=$1`, g.ID)
			continue
		}
		live = append(live, g)
	}
	return live, nil
}

// cantRegainHP is the one answer to whether a creature (a character, a monster's negative id, a companion's combat id) can regain Hit
// Points now, and why not: a lasting effect of a condition (the Pit Fiend's poison), Chill Touch (until the caster's next turn), the
// Sword of Wounding. A monster form whose own trait forbids it is the monster owner's (catalogForbidsHPGain, healMonster). An
// unreadable store is "can't": never permission to heal through a prohibition.
func cantRegainHP(lobbyID, id int) (bool, string) {
	if db == nil || lobbyID == 0 {
		return false, ""
	}
	if curseHas(lobbyID, id, curseNoRegain) { // "While cursed, the target can't regain Hit Points" (curse_records_2024.go)
		return true, "it is cursed"
	}
	effects, err := gatedEffectsOn(lobbyID, id, "no_healing")
	if err != nil {
		log.Printf("cantRegainHP: couldn't read the effects on %d in campaign %d: %v", id, lobbyID, err)
		return true, "its effects could not be read"
	}
	for _, g := range effects {
		return true, g.Text
	}
	for _, e := range spellEffectsOn(lobbyID, id) {
		if e.Kind == "no_regain" {
			return true, spellEffectText["no_regain"]
		}
	}
	if why := noHealingAuraOver(lobbyID, id); why != "" { // a monster's no_healing_aura (monster_mechanics_2024.go)
		return true, why
	}
	return false, ""
}

// lastingEffectsOf: the words of what a condition record does besides the condition itself (conditions_detail).
func lastingEffectsOf(rec conditionRecord) []string {
	if rec.LobbyID == 0 || rec.ID == 0 {
		return nil
	}
	effects, err := gatedEffectsOn(rec.LobbyID, rec.TargetID, "")
	if err != nil {
		return nil
	}
	var out []string
	for _, g := range effects {
		if g.Gate != rec.Condition || g.SourceID != rec.SourceID {
			continue
		}
		switch g.Kind {
		case "recurring_damage":
			out = append(out, "Takes "+strings.TrimSuffix(g.Text, ", while the "+rec.Condition+" condition lasts"))
		default:
			out = append(out, g.Text)
		}
	}
	return out
}
