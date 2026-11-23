package main

// A monster attack's damage must be known before an attack is rolled. The SRD
// importer and custom imports share this parser; runtime parsing also supports
// custom monsters saved before this feature was added.
import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type monsterUseContextKey struct{}

func withMonsterUse(r *http.Request, use string) *http.Request {
	if r == nil || use == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), monsterUseContextKey{}, use))
}
func monsterUseIn(r *http.Request) string {
	if r == nil {
		return ""
	}
	s, _ := r.Context().Value(monsterUseContextKey{}).(string)
	return s
}

type multiattackDeclaredContextKey struct{}

// withMultiattackDeclared carries the GM's declared composition for a choice Multiattack ("two attacks, using
// Scimitar or Shortbow in any combination"): the named attacks, in order, as many as the text makes. The
// declaration is validated against the Multiattack's text before anything is rolled (multiattackDeclaredPlan).
func withMultiattackDeclared(r *http.Request, declared []string) *http.Request {
	if r == nil || len(declared) == 0 {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), multiattackDeclaredContextKey{}, declared))
}
func multiattackDeclaredIn(r *http.Request) []string {
	if r == nil {
		return nil
	}
	s, _ := r.Context().Value(multiattackDeclaredContextKey{}).([]string)
	return s
}

var validAttackDiceRe = regexp.MustCompile(`(?i)^(\d+)d(\d+)([+-]\d+)?$`)
var attackDamageTypes = map[string]bool{"acid": true, "bludgeoning": true, "cold": true, "fire": true, "force": true, "lightning": true, "necrotic": true, "piercing": true, "poison": true, "psychic": true, "radiant": true, "slashing": true, "thunder": true}

func validMonsterDamageDice(dice string) bool {
	match := validAttackDiceRe.FindStringSubmatch(dice)
	if match == nil {
		return false
	}
	count, e1 := strconv.Atoi(match[1])
	sides, e2 := strconv.Atoi(match[2])
	if e1 != nil || e2 != nil || count < 1 || count > game.MaxDice/2 || sides < 1 || sides > game.MaxDieSides {
		return false
	}
	if match[3] != "" {
		bonus, err := strconv.Atoi(match[3])
		if err != nil || bonus < -10000 || bonus > 10000 {
			return false
		}
	}
	return true
}

// parsedMonsterAttack is the one definition used by seeding, custom imports,
// runtime attacks, companions and Wild Shape. Storage adapters never parse text.
type parsedMonsterAttack struct {
	monsterActionRow
	Extras  []extraDamage
	Problem string
	// ExtraTypeChosen: an extra amount's damage type is chosen by the GM (use), see chosenTypeExtraRe
	ExtraTypeChosen bool
}

// damageCond is a condition the server can judge for a conditional replacement of a hit's damage.
type damageCond int

const (
	condAdvantage            damageCond = iota + 1 // "if the chimera had Advantage on the attack roll"
	condAttackerBloodied                           // "if the swarm is Bloodied"
	condTargetBloodied                             // "if the target is Bloodied"
	condTargetGrappledByUser                       // "if the target is Grappled by the mimic"
	condCharged                                    // "if the goat moved 20+ feet straight toward the target immediately before the hit" (creature_movement_2024.go)
	condGMJudged                                   // any other condition: the primary damage is dealt and the GM is told
)

// damageAlt replaces a hit's primary damage when Cond holds.
type damageAlt struct {
	Dice string
	Flat *int
	Type string
	Cond damageCond
	Text string // the condition as printed, for condGMJudged
}

var damageConditionTexts = []struct {
	re   *regexp.Regexp
	cond damageCond
}{
	{regexp.MustCompile(`(?i)^if [\w' -]+ had Advantage on the attack roll$`), condAdvantage},
	{regexp.MustCompile(`(?i)^if the target is Bloodied$`), condTargetBloodied},
	{regexp.MustCompile(`(?i)^if the target is Grappled by [\w' -]+$`), condTargetGrappledByUser},
	{regexp.MustCompile(`(?i)^if the [\w' -]+ is Bloodied$`), condAttackerBloodied}, // after the target rule: the subject is the attacker
}

// classifyDamageCondition reads the condition text of an alternative damage amount; ok is false when the server cannot judge it.
func classifyDamageCondition(tail string) (damageCond, bool) {
	tail = strings.TrimSpace(strings.Trim(strings.TrimSpace(tail), ".,;—–-"))
	if _, ok := chargeAltFeet(tail); ok {
		return condCharged, true
	}
	for _, c := range damageConditionTexts {
		if c.re.MatchString(tail) {
			return c.cond, true
		}
	}
	return 0, false
}

// hitCondition is what the server knows about one hit when it picks between the primary and an alternative amount.
type hitCondition struct {
	Advantage, AttackerBloodied, TargetBloodied, TargetGrappledByAttacker bool
	ChargedFeet                                                           int // the attack's straight approach toward the target (chargedFeet)
}

func (h hitCondition) holds(c damageCond) bool {
	switch c {
	case condAdvantage:
		return h.Advantage
	case condAttackerBloodied:
		return h.AttackerBloodied
	case condTargetBloodied:
		return h.TargetBloodied
	case condTargetGrappledByUser:
		return h.TargetGrappledByAttacker
	}
	return false // condGMJudged never holds by itself
}

// applyDamageAlternatives returns the attack with the first alternative whose condition holds as its damage.
func applyDamageAlternatives(a monsterActionRow, h hitCondition) monsterActionRow {
	for _, alt := range a.Alts {
		if n, ok := chargeAltFeet(alt.Text); ok && alt.Cond == condCharged && h.ChargedFeet >= n {
			a.DamageDice, a.FlatDamage, a.DamageType = alt.Dice, alt.Flat, alt.Type
			break
		}
		if h.holds(alt.Cond) {
			a.DamageDice, a.FlatDamage, a.DamageType = alt.Dice, alt.Flat, alt.Type
			break
		}
	}
	return a
}

// monsterHitCondition gathers the facts alternatives ask about, for a monster (attackerID, 0 when it is outside the turn order)
// hitting a target character (tid) in the campaign.
func monsterHitCondition(lobbyID, attackerID int, haveTarget bool, tid int, advantage bool) hitCondition {
	h := hitCondition{Advantage: advantage, ChargedFeet: chargedFeet(lobbyID, attackerID, tid)}
	if attackerID != 0 {
		for _, e := range combatEntries(lobbyID) {
			if asInt(e["id"]) == attackerID {
				h.AttackerBloodied = monsterVitals(e).Bloodied()
			}
		}
	}
	if haveTarget && tid < 0 { // a monster of the fight (monster_vs_monster.go)
		for _, e := range combatEntries(lobbyID) {
			if asInt(e["id"]) == tid {
				h.TargetBloodied = monsterVitals(e).Bloodied()
			}
		}
		for _, r := range findConditionRecords(`target_id = $1 AND lobby_id = $2 AND condition = 'grappled'`, tid, lobbyID) {
			if attackerID != 0 && grappledBy(r, attackerID) {
				h.TargetGrappledByAttacker = true
			}
		}
	} else if haveTarget {
		var v game.Vitals
		if db.QueryRow(`SELECT COALESCE(hp,0), COALESCE(max_hp,0) FROM characters WHERE id = $1`, tid).Scan(&v.HP, &v.Max) == nil {
			h.TargetBloodied = v.Bloodied()
		}
		for _, r := range findConditionRecords(`target_id = $1 AND lobby_id = $2 AND condition = 'grappled'`, tid, lobbyID) {
			if attackerID != 0 && grappledBy(r, attackerID) {
				h.TargetGrappledByAttacker = true
			}
		}
	}
	return h
}

var (
	atkBonusRe = regexp.MustCompile(`Attack Roll: ([+-]\d+)`)
	hitDmgRe   = regexp.MustCompile(`Hit: \d+ \(([^)]+)\) ([A-Za-z]+) damage`)
	flatHitRe  = regexp.MustCompile(`Hit: (\d+) ([A-Za-z]+) damage`)
	// every damage amount of a hit: "12 (2d6 + 5) Slashing damage", "7 Fire damage"
	damageClauseRe    = regexp.MustCompile(`(\d+)(?: \(([^)]+)\))? ([A-Za-z]+) damage`)
	damageWordRe      = regexp.MustCompile(`(?i)\bdamage\b`)
	hitSentenceEnd    = regexp.MustCompile(`\.(?:\s|$)`)
	damageJoinRe      = regexp.MustCompile(`(?i)(?:^|[\s,;—–-])(plus|and|an additional)\s*$`)
	advantageOnlyRe   = regexp.MustCompile(`(?i)^\s*if the attack roll had Advantage\s*[,.;]?\s*$`)
	alternativeHeadRe = regexp.MustCompile(`(?i)(?:^|[\s,;—–-])or\s*$`)
	// words that make a damage amount conditional; any of them after an amount that is not one of the two recognised conditions
	// above means the server cannot tell when to deal it
	damageConditionRe = regexp.MustCompile(`(?i)\b(if|when|while|unless|only|against|provided|whenever|on a)\b`)
)

// hitDamageSentence is the part of an attack that is its Hit: damage, up to the end of its sentence ("Hit: 10 (2d6 + 3) Piercing
// damage plus 4 (1d8) Fire damage."). What follows (a saving throw's own damage, riders) belongs to the save-effect owner.
func hitDamageSentence(text string) string {
	i := strings.Index(text, "Hit:")
	if i < 0 {
		return ""
	}
	text = text[i+len("Hit:"):]
	if loc := hitSentenceEnd.FindStringIndex(text); loc != nil {
		text = text[:loc[0]+1]
	}
	return text
}

// parseExtraDamage reads every damage amount of the Hit sentence after the first one into extras. It returns a problem, not a
// guess, when the sentence has damage it cannot account for: an amount joined in a way that is not "plus", a condition other than
// the Advantage one, or the word "damage" without a recognised amount.
func parseExtraDamage(sentence string) (extras []extraDamage, alts []damageAlt, problem string) {
	clauses := damageClauseRe.FindAllStringSubmatchIndex(sentence, -1)
	consumed := 0
	for i, loc := range clauses {
		consumed++
		if i == 0 {
			continue // the primary damage
		}
		tail := sentence[loc[1]:]
		if i+1 < len(clauses) {
			tail = sentence[loc[1]:clauses[i+1][0]]
			if j := damageJoinRe.FindStringIndex(tail); j != nil {
				tail = tail[:j[0]]
			}
		}
		head := strings.TrimRight(sentence[:loc[0]], " ")
		x := extraDamage{Type: sentence[loc[6]:loc[7]]}
		if loc[4] >= 0 {
			x.Dice = game.NormalizeDice(sentence[loc[4]:loc[5]])
		} else {
			n, err := strconv.Atoi(sentence[loc[2]:loc[3]])
			if err != nil {
				return nil, nil, "A flat damage amount is outside the supported range; the GM must resolve it."
			}
			x.Flat = &n
		}
		switch {
		case alternativeHeadRe.MatchString(head):
			cond, ok := classifyDamageCondition(tail)
			if !ok {
				cond = condGMJudged
			}
			alts = append(alts, damageAlt{Dice: x.Dice, Flat: x.Flat, Type: x.Type, Cond: cond, Text: strings.TrimSpace(strings.Trim(strings.TrimSpace(tail), ".,;—–-"))})
			continue
		case !damageJoinRe.MatchString(head):
			return nil, nil, "The attack's damage text is not understood (an amount that is not 'plus'); the GM must resolve it."
		}
		if advantageOnlyRe.MatchString(tail) {
			x.IfAdvantage = true
		} else if strings.TrimSpace(strings.Trim(tail, ".,;")) != "" && damageConditionRe.MatchString(tail) {
			return nil, nil, "The attack has damage that depends on a condition the server cannot judge (" + strings.TrimSpace(tail) + "); the GM must resolve it. Nothing was rolled."
		}
		extras = append(extras, x)
	}
	// "equal to the Acid damage taken" points back at an amount already read; it is not another amount
	if len(damageWordRe.FindAllStringIndex(damageBackRefRe.ReplaceAllString(sentence, ""), -1)) > consumed {
		return nil, nil, "The attack has damage text the server does not understand; the GM must resolve it. Nothing was rolled."
	}
	return extras, alts, ""
}

func parseMonsterAttack(name, text string) parsedMonsterAttack {
	p := parsedMonsterAttack{monsterActionRow: monsterActionRow{Name: name, Desc: text}}
	if m := atkBonusRe.FindStringSubmatch(text); m != nil {
		p.AttackBonus, _ = strconv.Atoi(m[1])
	}
	if m := hitDmgRe.FindStringSubmatch(text); m != nil {
		p.DamageDice = game.NormalizeDice(m[1])
		p.DamageType = m[2]
	} else if m := flatHitRe.FindStringSubmatch(text); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			p.Problem = "Flat damage is outside the supported range."
		} else {
			p.FlatDamage = &n
			p.DamageType = m[2]
		}
	}
	if sentence := hitDamageSentence(text); sentence != "" && p.Problem == "" {
		// "7 (2d6) damage of the type chosen for the Draconic Origin trait": the type is the GM's choice (use), read as "Chosen"
		if chosenTypeExtraRe.MatchString(sentence) {
			p.ExtraTypeChosen = true
			sentence = chosenTypeExtraRe.ReplaceAllString(sentence, "$1 ($2) "+chosenTypeWord+" damage")
		}
		p.Extras, p.monsterActionRow.Alts, p.Problem = parseExtraDamage(sentence)
	}
	return p
}

// noDamageHit: an attack whose Hit deals no damage and gives conditions the rider owner reads ("Hit: The target has the Grappled
// condition (escape DC 14) from one of six tentacles, and the target has the Poisoned condition until the grapple ends.": SRD 5.2.1
// Roper Tentacle). It is a valid attack: every hit route skips its damage (hitDamageParts) and applies the conditions as for any hit.
func noDamageHit(desc string) bool {
	i := strings.Index(desc, "Hit:")
	if i < 0 || hitDmgRe.MatchString(desc) || flatHitRe.MatchString(desc) {
		return false
	}
	if !noDamageHitRe.MatchString(desc[i+len("Hit:"):]) {
		return false
	}
	_, ok := parseHitRider(desc)
	return ok
}

// noDamageHitRe: a Hit whose first words give a condition instead of an amount of damage.
var noDamageHitRe = regexp.MustCompile(`^\s*The target has the [A-Z][a-z]+ condition\b`)

// hitDamageParts: a hit's primary damage part, none for a hit that deals no damage (noDamageHit).
func hitDamageParts(desc string, amount int, typ string) []hitDamagePart {
	if noDamageHit(desc) {
		return nil
	}
	return []hitDamagePart{{Amount: max(amount, 0), Type: typ}}
}

func validStatBlockDamage(dice, typ string, flat *int) bool {
	if !attackDamageTypes[strings.ToLower(typ)] {
		return false
	}
	if flat != nil {
		return *flat >= 0 && *flat <= 100000
	}
	return validMonsterDamageDice(dice)
}
func rollStatBlockDamage(dice string, flat *int, crit bool) int {
	if flat != nil {
		return *flat
	}
	return game.RollDamage(dice, crit)
}

// prepareMonsterAttack refuses missing, malformed or unsupported damage before
// any roll, HP change or effects. The use choice must be one of the stat block's types.
func prepareMonsterAttack(a monsterActionRow, use string) (monsterActionRow, string) {
	if strings.Contains(a.Desc, "Hit:") {
		parsed := parseMonsterAttack(a.Name, a.Desc)
		if parsed.Problem != "" {
			return a, parsed.Problem
		}
		desc := a.Desc
		a = parsed.monsterActionRow
		a.Desc = desc
		if parsed.ExtraTypeChosen {
			chosen, problem := withChosenDamageType(a.Name, a.Desc, use)
			if problem != "" {
				return a, problem
			}
			a.Desc = chosen // the rest of the engine reads an ordinary typed amount
		}
	}
	if !validStatBlockDamage(a.DamageDice, a.DamageType, a.FlatDamage) && !noDamageHit(a.Desc) {
		return a, fmt.Sprintf("%s has unsupported or missing attack damage; the GM must resolve it. Nothing was rolled.", a.Name)
	}
	for _, x := range attackExtraDamage(a.Desc) {
		if !x.valid() {
			return a, "An attack damage component is unsupported. Nothing was rolled."
		}
	}

	return a, ""
}

// prepareUnconditionalAttack is prepareMonsterAttack for the paths that cannot judge a condition of the hit (a companion's or a
// Wild Shape form's attack): a stat block whose damage depends on one is refused instead of dealt at its primary amount.
func prepareUnconditionalAttack(a monsterActionRow, use string) (monsterActionRow, string) {
	prepared, problem := prepareMonsterAttack(a, use)
	for _, alt := range prepared.Alts {
		if problem == "" && alt.Cond != condCharged { // a charge is judged on every path (creature_movement_2024.go)
			return prepared, a.Name + " has damage that depends on a condition (Advantage, Bloodied, Grappled) this path cannot judge; the GM must resolve it. Nothing was rolled."
		}
	}
	return prepared, problem
}

// preflightMonsterAction checks damage for a named attack (including each
// Multiattack component), before recharge or legendary uses can be spent.
// declared is the GM's declared composition for a choice Multiattack, nil elsewhere.
func preflightMonsterAction(meta *monsterMeta, name, use string, declared []string) string {
	if meta == nil {
		return ""
	}
	rows := make([]monsterActionRow, 0, len(meta.Actions))
	for _, entry := range meta.Actions {
		rows = append(rows, parseMonsterAttack(entry.Name, rulesText(entry)).monsterActionRow)
	}

	for _, a := range rows {
		if !strings.EqualFold(stripUsage(a.Name), stripUsage(name)) {
			continue
		}
		if !isAttackEntry(a.Desc) && !strings.EqualFold(stripUsage(a.Name), "Multiattack") {
			return ""
		}
		chosen := []monsterActionRow{a}
		if strings.EqualFold(stripUsage(a.Name), "Multiattack") {
			plan, problem := multiattackPlan(a.Desc, rows)
			if len(declared) > 0 {
				// the same declared-composition contract resolveMonsterAttack applies: the declaration takes
				// over where the text leaves a choice or offers a substitute, validated before anything rolls.
				if spec, ok := parseMultiattackChoice(a.Desc, rows); ok && (problem != "" || spec.substitute != "") {
					dplan, _, dproblem := multiattackDeclaredPlan(spec, rows, declared)
					if dproblem != "" {
						return refusalAudiences("The Multiattack could not be resolved. Nothing was rolled.", dproblem)
					}
					plan, problem = dplan, ""
				}
			}
			if problem != "" {
				return refusalAudiences("The Multiattack could not be resolved. Nothing was rolled.", problem)
			}
			chosen = plan
		}
		for _, one := range chosen {
			if _, problem := prepareMonsterAttack(one, use); problem != "" {
				return problem
			}
		}
		return ""
	}
	return ""
}

// companionRows is a storage adapter; parse/validation remains in this file.
func companionRows(rows []monsterActionRow) []compAttack {
	out := []compAttack{}
	for _, row := range rows {
		if !isAttackEntry(row.Desc) {
			continue
		}
		p := parseMonsterAttack(row.Name, row.Desc)
		out = append(out, compAttack{Name: row.Name, Bonus: p.AttackBonus, Dice: p.DamageDice, Type: p.DamageType, FlatDamage: p.FlatDamage, StatBlockText: row.Desc, Count: 1})
	}
	return out
}

var companionDamageChoiceRE = regexp.MustCompile(`(?i)\bdamage type:\s*([a-z]+)\b`)

func companionDamageChoice(description string) string {
	if m := companionDamageChoiceRE.FindStringSubmatch(description); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}
func prepareCompanionAttack(a compAttack, choice string) (compAttack, string) {
	row, problem := prepareUnconditionalAttack(monsterActionRow{Name: a.Name, AttackBonus: a.Bonus, DamageDice: a.Dice, DamageType: a.Type, Desc: a.StatBlockText, FlatDamage: a.FlatDamage}, choice)
	if problem != "" {
		return a, problem
	}
	a.Bonus, a.Dice, a.Type, a.FlatDamage = row.AttackBonus, row.DamageDice, row.DamageType, row.FlatDamage
	if row.Desc != "" {
		a.StatBlockText = row.Desc // with a chosen extra damage type written in ("7 (2d6) Fire damage"), as the monster route reads it
	}
	if a.Extra != "" && !validStatBlockDamage(a.Extra, a.ExtraType, nil) {
		return a, "Unsupported companion extra damage. Nothing was rolled."
	}
	return a, ""
}
func prepareCompanionPlan(c companion, description string) ([]compAttack, string) {
	plan := append([]compAttack{}, c.Attacks...)
	// Older companions saved a flattened attack without its source text. Recover
	// that text from their immutable form without changing summoned attack scaling.
	var source []compAttack
	for i, a := range plan {
		if a.StatBlockText == "" && c.Form != "" {
			if source == nil {
				source = formAttacks(c.Form)
			}
			for _, original := range source {
				if original.Name == a.Name {
					a.StatBlockText = original.StatBlockText
					break
				}
			}
		}
		if a.StatBlockText != "" && a.Extra != "" {
			for _, x := range attackExtraDamage(a.StatBlockText) {
				if game.NormalizeDice(a.Extra) == x.Dice && strings.EqualFold(a.ExtraType, x.Type) {
					a.Extra = ""
					a.ExtraType = ""
					break
				}
			}
		}
		prepared, problem := prepareCompanionAttack(a, companionDamageChoice(description))
		if problem != "" {
			return nil, problem
		}
		plan[i] = prepared
	}
	return plan, ""
}

// unappliedHitRider is the text of a hit that goes beyond its damage amounts ("..., and the target's Hit Point maximum decreases by
// ...", "The target has the Grappled condition (escape DC 14)") and that no rule of the server applies for a stat-block hit: it is
// shown to the GM with the hit instead of being dropped. A saving throw's own text is applied by the save-effect owner and is not
// repeated here.
// subjectedLeadRe: the lead-in of a hit's own saving throw effect ("..., and the target is subjected to the following effect",
// Homunculus Bite): the effect is the save owner's (applySaveEffect), so the lead-in is nothing for the GM.
var subjectedLeadRe = regexp.MustCompile(`(?i)^(?:and\s+)?(?:the target|it)\s+is subjected to the following effect\.?`)

func unappliedHitRider(desc string) string {
	sentence := hitDamageSentence(desc)
	if sentence == "" {
		return ""
	}
	if e, ok := parseHitRider(desc); ok { // its conditions are applied (parseHitRider): only what remains is the GM's
		return e.Extra
	}
	rider := ""
	if clauses := damageClauseRe.FindAllStringIndex(sentence, -1); len(clauses) > 0 {
		rider = sentence[clauses[len(clauses)-1][1]:]
	}
	after := ""
	if _, hasSave := parseSaveEffect(desc); !hasSave {
		after = strings.TrimPrefix(desc[strings.Index(desc, "Hit:")+len("Hit:"):], sentence)
	}
	text := strings.Trim(strings.TrimSpace(rider+" "+after), " ,;.")
	// the attach owner and the recurring owner enforce these on the hit (monster_attach_2024.go,
	// monster_recurring_damage_2024.go): the attach onset, the gated recurring clause, the enforced
	// lockout sharing its sentence, and the gate lead-ins are not the GM's. Stripping them here is what
	// keeps a condition-less rider (Stirge Proboscis) out of the residual; a recurring clause whose gate
	// did not hold still reaches the GM through addRecurringDamage's note, never duplicated from here.
	text = subjectedLeadRe.ReplaceAllString(text, "") // "and the target is subjected to the following effect": the save owner applies that effect
	text = attachOnHitRe.ReplaceAllString(text, "")
	text = hpMaxDrainSentenceRe.ReplaceAllString(text, "") // applied by every hit route (hitAfterDamage)
	text = stripBurnForCreatures(text)
	text = stripForcedMoves(text)     // applied by every hit route (hitAfterDamage)
	text = stripAbilityDecrease(text) // applied by every hit route (hitAfterDamage: the ability score owner)
	text = stripItemPenalties(text)   // hitAfterDamage corrodes the worn armor (item_state_2024.go)
	text = stripDropRider(text)       // reduced to 0 by the hit: hitAfterDamage
	kept := []string{}
	for _, s := range sentenceSplitRe.Split(text, -1) {
		stripped := s
		if spec := readRecurringDamage(s); spec != nil {
			stripped = recurringDamageRe.ReplaceAllString(stripped, "")
			stripped = recurringLockoutTailRe.ReplaceAllString(stripped, "")
			stripped = grappleGatePrefixRe.ReplaceAllString(stripped, "")
			stripped = attachGatePrefixRe.ReplaceAllString(stripped, "")
			stripped = attachLockoutLeadRe.ReplaceAllString(stripped, "")
		}
		// The published detach ACTION is enforced by the detach action owner (/api/attach/detach):
		// not the GM's. A matching sentence is exactly what addAttach parses its rule from, so a
		// stripped sentence always has an enforced route; an unmatched one stays. The VOLUNTARY
		// detach (5 feet of movement) stays: it is the movement system's.
		stripped = detachAutoRe.ReplaceAllString(stripped, "")
		stripped = detachCheckRe.ReplaceAllString(stripped, "")
		// The "attack only the target, with Advantage" rider is enforced by the attach owner (attack
		// entry refusal + the roll's Advantage source): not the GM's. The Speed 0 / moves-with-target
		// sentence stays: no monster speed or map store exists for it.
		stripped = whileAttachedOnlyAdvRe.ReplaceAllString(stripped, "")
		if k := strings.Trim(stripped, " ,;.\n"); k != "" && !strings.EqualFold(k, "and") && !strings.EqualFold(k, "or") {
			kept = append(kept, k) // a bare connector left by a stripped clause is not a residual
		}
	}
	text = strings.Trim(strings.Join(kept, ". "), " ,;.")
	for _, alt := range parseMonsterAttack("", desc).Alts {
		if strings.EqualFold(alt.Text, text) {
			return "" // the condition of an alternative amount: gmJudgedAlternatives reports it
		}
	}
	if _, isCondition := classifyDamageCondition(text); text == "" || isCondition || advantageOnlyRe.MatchString(" "+text) {
		return "" // nothing beyond the damage, or only the condition of an amount already read
	}
	return text
}

var damageBackRefRe = regexp.MustCompile(`(?i)\bthe [a-z]+ damage (?:taken|dealt)\b`)

// gmJudgedAlternatives tells the GM about alternative damage whose condition the server cannot judge: the hit dealt its primary
// amount and says so, so the GM can add the difference when the condition was true.
func gmJudgedAlternatives(a monsterActionRow) string {
	note := ""
	for _, alt := range a.Alts {
		if alt.Cond == condGMJudged {
			amount := alt.Dice
			if alt.Flat != nil {
				amount = strconv.Itoa(*alt.Flat)
			}
			note += fmt.Sprintf(" [GM: dealt its normal damage; %s %s damage applies instead %s]", amount, alt.Type, alt.Text)
		}
	}
	return note
}

const chosenTypeWord = "Chosen"

// withChosenDamageType: a text whose damage is "of the type chosen for the Draconic Origin trait" (SRD 5.2.1 Half-Dragon: Claw's extra
// amount, Dragon's Breath) with the type the GM names in use written in, so every reader sees an ordinary typed amount; the refusal
// when use names no damage type. The attack owner (prepareMonsterAttack) and the save owner's entry (resolveMonsterEffectText) read it.
func withChosenDamageType(entry, text, use string) (string, string) {
	if !chosenTypeExtraRe.MatchString(text) {
		return text, ""
	}
	pick := strings.ToLower(strings.TrimSpace(use))
	if !attackDamageTypes[pick] {
		return text, fmt.Sprintf("%s has damage of a type the GM chooses; give a damage type in use (acid, cold, fire, ...). Nothing was rolled.", entry)
	}
	return chosenTypeExtraRe.ReplaceAllString(text, "$1 ($2) "+titleCase(pick)+" damage"), ""
}

var chosenTypeExtraRe = regexp.MustCompile(`(?i)(\d+) \(([^)]+)\) damage of the type chosen for the [^.,;]+? trait`)
