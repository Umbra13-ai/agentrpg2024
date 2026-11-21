package main

// A monster's attack on a character, resolved by the server from the monster's own stat block (2024 data).
// POST /api/gm/narrate with "monster_action": {"monster": "goblin-warrior" (or its name in the fight), "action":
// "Scimitar" (an action of the stat block, or "Multiattack"), "target": "<character name or id>"}.
// The server rolls the attack bonus printed in the stat block against the target's Armor Class, with Advantage and
// Disadvantage from the conditions of both sides (Prone, Restrained, Paralyzed, Poisoned, Sap, ...), doubles the damage
// dice on a critical hit (a natural 20, or a hit on a Paralyzed or Unconscious target within 5 feet) and deals the damage to the
// character through the Hit Point owner, then the hit's own saving throw if its text has one (unless "apply_damage": false:
// damageApplies is the default of every monster action).
// Without a known target the old behaviour remains: the roll and the damage if it hits.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type monsterActionRow struct {
	FlatDamage  *int   `json:"flat_damage,omitempty"`
	Name        string `json:"name"`
	AttackBonus int    `json:"attack_bonus"`
	DamageDice  string `json:"damage_dice"`
	DamageType  string `json:"damage_type"`
	Desc        string `json:"desc"`
	// Alts: "..., or 18 (4d6 + 4) Piercing damage if the chimera had Advantage on the attack roll": the damage that replaces the
	// primary one when its condition holds (parsed from Desc by parseMonsterAttack, never stored).
	Alts []damageAlt `json:"-"`
}

var countWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12}

// maxMultiattackAttacks bounds one Multiattack (the largest printed ones make about eight attacks). An imported number above it is
// refused before anything is allocated or rolled.
const maxMultiattackAttacks = 20

// multiattackOverCapProblem starts the refusal of a Multiattack above the cap (or with a number that is not readable); an import
// refuses such a definition outright, other refusals only stop the attack when it is made.
const multiattackOverCapProblem = "A Multiattack of more than 20 attacks (or an unreadable number) is not supported: "

var (
	anyCombinationRe = regexp.MustCompile(`(?i)\bany combination\b`)
	attackChoiceRe   = regexp.MustCompile(`(?i)\s(or|either)\s`)
	multiNumberRe    = regexp.MustCompile(`^\d+$`)
)

// multiattackPlan reads a Multiattack description ("makes two Tentacle attacks and uses ...") into the attacks it makes. The plan
// is the one definition of what a Multiattack does for every caller (GM attack, Legendary Actions, companions, preflight). When
// the text cannot be read with certainty it returns a problem instead of a guess, and nothing may be rolled: a count above
// maxMultiattackAttacks, an either/or choice between attacks, a count the text does not give.
func multiattackPlan(desc string, actions []monsterActionRow) ([]monsterActionRow, string) {
	low := strings.ToLower(desc)
	var plan []monsterActionRow
	named := 0
	for _, a := range actions {
		if strings.EqualFold(stripUsage(a.Name), "Multiattack") || (a.DamageDice == "" && !isAttackEntry(a.Desc)) {
			continue
		}
		n := strings.ToLower(stripUsage(a.Name)) // "Grave Strike (Vampire Form Only)" is "Grave Strike" in the Multiattack text
		idx := strings.Index(low, n)
		if idx < 0 {
			continue
		}
		named++
		count := 1
		pre := strings.Fields(low[:idx])
		if len(pre) > 0 {
			w := strings.Trim(pre[len(pre)-1], ",.")
			if c, ok := countWords[w]; ok {
				count = c
			} else if multiNumberRe.MatchString(w) {
				c, err := strconv.Atoi(w)
				if err != nil || c < 1 || c > maxMultiattackAttacks {
					return nil, fmt.Sprintf(multiattackOverCapProblem+"%s attacks of %s is refused. The GM must resolve it.", w, a.Name)
				}
				count = c
			}
		}
		if len(plan)+count > maxMultiattackAttacks {
			return nil, multiattackOverCapProblem + "the plan is refused. The GM must resolve it."
		}
		for i := 0; i < count; i++ {
			plan = append(plan, a)
		}
	}
	// "one Bite attack or one Claw attack", "using A or B in any combination": which attack is made is a choice the text leaves
	// open. Do not turn it into both: the GM attacks with the named action(s) one by one.
	if named > 1 && attackChoiceRe.MatchString(low) {
		return nil, "The Multiattack offers a choice between attacks (" + strings.TrimSpace(desc) + "); the GM must make the attacks one by one by name. Nothing was rolled."
	}
	if len(plan) == 0 { // "makes two attacks": use the first attack in the list
		for _, a := range actions {
			if (a.DamageDice != "" || isAttackEntry(a.Desc)) && !strings.EqualFold(stripUsage(a.Name), "Multiattack") {
				count := 2
				for w, c := range countWords {
					if strings.Contains(low, "makes "+w) {
						count = c
					}
				}
				for i := 0; i < count; i++ {
					plan = append(plan, a)
				}
				break
			}
		}
	}
	return plan, ""
}

// multiattackChoiceSpec is the declared-composition contract of a Multiattack text: how many attacks it makes, which
// named attacks may fill those slots, whether the slots must all be one option ("four Slam attacks or four Gears
// Launcher attacks"), and a named non-attack substitute that may replace a bounded number of slots ("It can replace
// one attack with a use of Roar"). The server never chooses; the GM declares and the server validates the declaration.
type multiattackChoiceSpec struct {
	budget     int
	options    []string // lowercased usage-stripped names of the allowed attack options
	uniform    bool     // every attack slot must be the same option
	substitute string   // lowercased usage-stripped name of the allowed non-attack substitute, "" when the text offers none
	subCap     int      // how many slots the substitute may replace
}

var (
	anyCombinationSegRe = regexp.MustCompile(`(?i)\busing\s+(.+?)\s+in any combination\b`)
	// The substitute name ends at the sentence's punctuation, not at a space: "a use of Enthralling Panache."
	// reads the whole name, where a [\s.,] terminator would truncate it to its first word.
	replaceAttackRe = regexp.MustCompile(`(?i)\breplace\s+(\w+)\s+attacks?\s+with\s+(?:a\s+use\s+of\s+)?([a-z][a-z' -]*?)(?:\s*[.,]|\s*$)`)
)

// multiattackCountWord reads a count word or digit string; ok is false when it is not a readable count.
func multiattackCountWord(w string) (int, bool) {
	w = strings.Trim(w, ",.")
	if c, ok := countWords[w]; ok {
		return c, true
	}
	if multiNumberRe.MatchString(w) {
		if c, err := strconv.Atoi(w); err == nil {
			return c, true
		}
	}
	return 0, false
}

// findActionRow resolves an action by its exact name or its usage-stripped name ("Rend (Jackal or Hybrid Form Only)" is "Rend").
func findActionRow(name string, actions []monsterActionRow) *monsterActionRow {
	for i, a := range actions {
		if strings.EqualFold(a.Name, name) || strings.EqualFold(stripUsage(a.Name), name) {
			return &actions[i]
		}
	}
	return nil
}

// resolveAttackOptionName resolves a Multiattack text option to its attack action's lowercased usage-stripped name;
// "" when the option names no attack action (a non-attack action or nothing at all is not an attack option).
func resolveAttackOptionName(name string, actions []monsterActionRow) string {
	row := findActionRow(name, actions)
	if row == nil || (row.DamageDice == "" && !isAttackEntry(row.Desc)) {
		return ""
	}
	return strings.ToLower(stripUsage(row.Name))
}

// parseMultiattackChoice reads the declared-composition contract out of a Multiattack text. ok is false when the text
// does not parse with certainty; the caller then keeps the undecorated behavior (the GM handoff).
func parseMultiattackChoice(desc string, actions []monsterActionRow) (multiattackChoiceSpec, bool) {
	var spec multiattackChoiceSpec
	low := strings.ToLower(desc)
	i := strings.Index(low, "makes ")
	if i < 0 {
		return spec, false
	}
	rest := strings.TrimSpace(low[i+len("makes "):])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return spec, false
	}
	budget, ok := multiattackCountWord(fields[0])
	if !ok || budget < 1 || budget > maxMultiattackAttacks {
		return spec, false
	}
	spec.budget = budget
	body := strings.TrimSpace(rest[len(fields[0]):])
	attackOptions := func(seg string) []string {
		var out []string
		for _, part := range strings.Split(seg, " or ") {
			for _, p := range strings.Split(part, ", ") {
				if n := resolveAttackOptionName(strings.TrimSpace(p), actions); n != "" {
					out = append(out, n)
				}
			}
		}
		return out
	}
	switch {
	case strings.Contains(body, "in any combination"):
		// "two attacks, using Scimitar or Shortbow in any combination": each slot is a free choice.
		m := anyCombinationSegRe.FindStringSubmatch(body)
		if m == nil {
			return spec, false
		}
		spec.options = attackOptions(m[1])
	default:
		if j := strings.Index(body, " attacks or "); j >= 0 {
			// "four Slam attacks or four Gears Launcher attacks": the whole equal-size group is the choice.
			left := strings.TrimSpace(body[:j])
			right := strings.TrimSpace(body[j+len(" attacks or "):])
			rf := strings.Fields(right)
			if len(rf) >= 2 {
				if n2, ok2 := multiattackCountWord(rf[0]); ok2 && n2 == budget {
					if k := strings.Index(right, " attacks"); k > 0 {
						spec.options = attackOptions(left + " or " + strings.TrimSpace(right[len(rf[0]):k]))
						spec.uniform = true
					}
				}
			}
		}
		if !spec.uniform {
			k := strings.Index(body, " attack")
			if k <= 0 {
				return spec, false
			}
			spec.options = attackOptions(body[:k])
		}
	}
	if len(spec.options) == 0 {
		return spec, false
	}
	// "It can replace one attack with a use of Roar": a named non-attack substitute may take a bounded number of
	// slots. Read from the whole text; the option sentences sit after the first.
	if m := replaceAttackRe.FindStringSubmatch(low); m != nil {
		if n, ok := multiattackCountWord(m[1]); ok && n >= 1 && n <= spec.budget {
			if row := findActionRow(m[2], actions); row != nil && row.DamageDice == "" && !isAttackEntry(row.Desc) {
				spec.substitute = strings.ToLower(stripUsage(row.Name))
				spec.subCap = n
			}
		}
	}
	return spec, true
}

// multiattackDeclaredPlan validates the GM's declared composition against the text's contract and returns the attack
// rows to roll plus the substitutes to resolve as uses. Every refusal happens before anything is rolled and says so.
func multiattackDeclaredPlan(spec multiattackChoiceSpec, actions []monsterActionRow, declared []string) ([]monsterActionRow, []string, string) {
	if len(declared) > maxMultiattackAttacks {
		return nil, nil, multiattackOverCapProblem + "the declared composition is refused. The GM must resolve it."
	}
	if len(declared) != spec.budget {
		return nil, nil, fmt.Sprintf("The declared composition names %d attack(s); the Multiattack makes %d. Nothing was rolled.", len(declared), spec.budget)
	}
	var plan []monsterActionRow
	var substitutes []string
	subUsed, uniformName := 0, ""
	for _, d := range declared {
		n := strings.ToLower(strings.TrimSpace(d))
		if n == "" {
			return nil, nil, "The declared composition has an empty attack name. Nothing was rolled."
		}
		if spec.substitute != "" && n == spec.substitute {
			subUsed++
			if subUsed > spec.subCap {
				return nil, nil, fmt.Sprintf("The Multiattack can replace only %d of its attacks with %s. Nothing was rolled.", spec.subCap, spec.substitute)
			}
			substitutes = append(substitutes, n)
			continue
		}
		row := findActionRow(n, actions)
		if row == nil || !containsStr(spec.options, strings.ToLower(stripUsage(row.Name))) {
			return nil, nil, fmt.Sprintf("The Multiattack's text does not allow %q as one of its attacks. Nothing was rolled.", d)
		}
		if row.DamageDice == "" && !isAttackEntry(row.Desc) {
			return nil, nil, fmt.Sprintf("%q is not an attack the Multiattack can make. Nothing was rolled.", d)
		}
		if spec.uniform {
			if uniformName == "" {
				uniformName = strings.ToLower(stripUsage(row.Name))
			}
			if strings.ToLower(stripUsage(row.Name)) != uniformName {
				return nil, nil, "The Multiattack makes all of its attacks one way or all the other; it cannot mix them. Nothing was rolled."
			}
		}
		plan = append(plan, *row)
	}
	return plan, substitutes, ""
}

// multiattackUseCount: how often a Multiattack uses a non-attack action ("uses Antennae twice", "uses Bite", "and Swallow").
// The count follows the name: "twice", "thrice", "<word or digits> times"; without one the use is once. 0 means the
// Multiattack does not use the action. An unreadable, ambiguous or over-cap count resolves NOTHING for that use:
// the refusal goes to the reply so the GM resolves it, rather than a partial execution of an ambiguous action.
func multiattackUseCount(multi, name string) (int, string) {
	i := strings.Index(multi, "uses "+name)
	if i < 0 {
		if strings.Contains(multi, "and "+name) {
			return 1, ""
		}
		return 0, ""
	}
	rest := strings.TrimSpace(multi[i+len("uses "+name):])
	if rest == "" {
		return 1, ""
	}
	fields := strings.Fields(rest)
	w := strings.Trim(fields[0], ",.")
	switch w {
	case "twice":
		return 2, ""
	case "thrice":
		return 3, ""
	}
	times := len(fields) > 1 && strings.Trim(fields[1], ",.") == "times"
	if c, ok := countWords[w]; ok {
		if times {
			return c, ""
		}
		return 1, "" // "uses Bite, two ...": the word starts the next clause, not a count
	}
	if multiNumberRe.MatchString(w) {
		c, err := strconv.Atoi(w)
		if !times || err != nil || c < 1 {
			return 0, fmt.Sprintf("A Multiattack use count %q of %s is not readable; the GM must resolve it.", w, name)
		}
		if c > maxMultiattackAttacks {
			return 0, fmt.Sprintf(multiattackOverCapProblem+"%s uses of %s is refused; the GM must resolve it.", w, name)
		}
		return c, ""
	}
	if times {
		return 0, fmt.Sprintf("A Multiattack use count %q of %s is not readable; the GM must resolve it.", w, name)
	}
	return 1, ""
}

// multiattackDesc: the Multiattack entry's text, which the use counts are read from.
func multiattackDesc(actions []monsterActionRow) string {
	for _, m := range actions {
		if strings.EqualFold(stripUsage(m.Name), "Multiattack") {
			return strings.ToLower(m.Desc)
		}
	}
	return ""
}

// multiattackUseActions: the non-attack actions a Multiattack can list as "uses ..." (no attack roll of their own).
func multiattackUseActions(actions []monsterActionRow) []monsterActionRow {
	var out []monsterActionRow
	for _, a := range actions {
		if a.DamageDice != "" || isAttackEntry(a.Desc) || strings.EqualFold(stripUsage(a.Name), "Multiattack") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// multiattackUses resolves the non-attack actions a Multiattack lists ("makes two Grave Strike attacks and uses Bite",
// "makes one Bite attack and uses Antennae twice"): every listed use, as many times as the text says. It runs for every
// kind of target the attacks were made against (a character, another monster or a companion), so no target path drops a
// listed use. The counts are validated before anything is rolled (resolveMonsterAttack); the problem text here is a
// backstop for a call path that skipped the validation.
func multiattackUses(r *http.Request, campaignID, attackerID int, attackerName, action string, actions []monsterActionRow, target string, apply bool) string {
	if !strings.EqualFold(stripUsage(action), "Multiattack") || attackerID == 0 {
		return ""
	}
	text := ""
	multi := multiattackDesc(actions)
	for _, a := range multiattackUseActions(actions) {
		uses, problem := multiattackUseCount(multi, strings.ToLower(stripUsage(a.Name)))
		if problem != "" {
			text += " " + problem
		}
		for i := 0; i < uses; i++ {
			if txt, handled, refusal := resolveMonsterActionByName(r, campaignID, attackerName, stripUsage(a.Name), target, nil, apply, ""); refusal != "" {
				text += " " + refusal
			} else if handled && txt != "" {
				text += " " + stripUsage(a.Name) + ": " + txt
			}
		}
	}
	// A declared composition's validated substitutes ("It can replace one attack with a use of Roar") resolve here,
	// once each, like the text-listed uses above. resolveMonsterAttack validated the declaration against the text
	// before anything rolled; the spec re-read here is the backstop for a call path that skipped that validation.
	if declared := multiattackDeclaredIn(r); len(declared) > 0 {
		if spec, ok := parseMultiattackChoice(multi, actions); ok && spec.substitute != "" {
			row := findActionRow(spec.substitute, actions)
			subs := 0
			for _, d := range declared {
				if strings.ToLower(strings.TrimSpace(d)) != spec.substitute || subs >= spec.subCap || row == nil {
					continue
				}
				subs++
				if txt, handled, refusal := resolveMonsterActionByName(r, campaignID, attackerName, stripUsage(row.Name), target, nil, apply, ""); refusal != "" {
					text += " " + refusal
				} else if handled && txt != "" {
					text += " " + stripUsage(row.Name) + ": " + txt
				}
			}
		}
	}
	return text
}

// replaceAttackSentenceRe: the whole "It can replace one attack with a use of Roar." sentence when it leads the
// unautomated remainder. Only the leading sentence is removed; anything after it stays handed to the GM.
var replaceAttackSentenceRe = regexp.MustCompile(`(?i)^\s*it can replace\s+\w+\s+attacks?\s+with\s+(?:a\s+use\s+of\s+)?[a-z][a-z' -]*?\.?\s*$`)

// declaredSubstituteUsed: the validated declaration exercised the text's substitute, so that sentence is automated
// for this resolution rather than an unautomated option.
func declaredSubstituteUsed(r *http.Request, desc string, actions []monsterActionRow) bool {
	spec, ok := parseMultiattackChoice(desc, actions)
	if !ok || spec.substitute == "" {
		return false
	}
	for _, d := range multiattackDeclaredIn(r) {
		if strings.ToLower(strings.TrimSpace(d)) == spec.substitute {
			return true
		}
	}
	return false
}

// multiattackExtraText: sentences of a Multiattack after its first ("It can replace one attack with a use of Spellcasting ...") are
// options the server does not plan; the reply hands them to the GM instead of dropping them.
func multiattackExtraText(desc string) string {
	parts := hitSentenceEnd.Split(strings.TrimSpace(desc), 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(parts[1]), " .")
}

var meleeRe = regexp.MustCompile(`(?i)melee`)

type extraDamage struct {
	Dice, Type  string
	IfAdvantage bool // "... if the attack roll had Advantage": dealt only when the roll had it
	Flat        *int // a fixed amount ("plus 7 Fire damage"): no dice, no doubling on a critical hit
}

// roll is what the component deals (dice doubled on a critical hit, a fixed amount as it is).
func (x extraDamage) roll(crit bool) int { return rollStatBlockDamage(x.Dice, x.Flat, crit) }

// valid: the component's amount and damage type are ones the server can deal.
func (x extraDamage) valid() bool { return validStatBlockDamage(x.Dice, x.Type, x.Flat) }

// attackExtraDamage lists the extra damage of an attack ("plus 10 (3d6) Fire damage"); only the part after the first "Hit:".
func attackExtraDamage(desc string) []extraDamage { return parseMonsterAttack("", desc).Extras }

var rangedOnlyRe = regexp.MustCompile(`(?i)ranged attack roll`)

func characterByNameOrID(campaignID int, who string) (id int, name string, ac int, ok bool) {
	who = strings.TrimSpace(who)
	if who == "" {
		return 0, "", 0, false
	}
	if n, err := strconv.Atoi(who); err == nil {
		if db.QueryRow(`SELECT id, name, COALESCE(ac,10) FROM characters WHERE id = $1 AND lobby_id = $2`, n, campaignID).Scan(&id, &name, &ac) == nil {
			return id, name, ac, true
		}
	}
	if db.QueryRow(`SELECT id, name, COALESCE(ac,10) FROM characters WHERE lobby_id = $1 AND LOWER(name) = LOWER($2)`, campaignID, who).Scan(&id, &name, &ac) == nil {
		return id, name, ac, true
	}
	return 0, "", 0, false
}

// resolveMonsterAttack returns the result text and a structured summary; ok is false when the stat block could not be used.
// damageApplies is the one default of every monster action the GM sends (narrate's monster_action, a Legendary Action): its damage
// and effects are dealt to the targets unless the request says "apply_damage": false (feedback log 62: narrate's default was false,
// so a boss's Multiattack reported 85 damage and the character's Hit Points didn't move, while a Legendary Action dealt its damage).
func damageApplies(p *bool) bool { return p == nil || *p }

func resolveMonsterAttack(r *http.Request, campaignID int, monster, action, target string, apply bool) (string, map[string]interface{}, bool) {
	chargeSaidNote := ""
	ids := monsterIDsByKeyOrName(campaignID, monster)
	if n, err := strconv.Atoi(strings.TrimPrefix(monster, "#")); err == nil && strings.HasPrefix(monster, "#") {
		ids = []int{n} // one particular monster of the fight (Legendary Actions)
	}
	attackerID, key, attackerName := 0, strings.ToLower(strings.ReplaceAll(monster, " ", "-")), monster
	entries := combatEntries(campaignID)
	for _, id := range ids { // the best match first (F208), not the first in the turn order
		for _, c := range entries {
			if asInt(c["id"]) == id && asInt(c["hp"]) > 0 && attackerID == 0 {
				attackerID, attackerName = id, sprintAny(c["name"])
				if k := sprintAny(c["monster_key"]); k != "" {
					key = k
				}
			}
		}
	}
	var raw []byte
	var str int
	if monsterQueryRow(`SELECT COALESCE(actions,'[]'), COALESCE(str,10) FROM monsters WHERE slug = $1`, key).Scan(&raw, &str) != nil {
		return "", nil, false
	}
	var actions []monsterActionRow
	json.Unmarshal(raw, &actions)
	var chosen []monsterActionRow
	// the exact name first, then the name without its usage note ("Grave Strike (Vampire Form Only)", "Bite (Recharge 5-6)")
	for pass := 0; pass < 2 && len(chosen) == 0; pass++ {
		for _, a := range actions {
			n := a.Name
			if pass == 1 {
				n = stripUsage(a.Name)
			}
			if strings.EqualFold(n, action) || (pass == 1 && strings.EqualFold(n, stripUsage(action))) {
				if strings.EqualFold(stripUsage(a.Name), "Multiattack") {
					plan, problem := multiattackPlan(a.Desc, actions)
					if declared := multiattackDeclaredIn(r); len(declared) > 0 {
						// The GM declared the composition: validate it against the text's contract and roll
						// exactly the declaration. The server never chooses for the GM; an undeclared choice
						// keeps the handoff below. The declaration takes over only where the text leaves a
						// choice or offers a substitute; a plain fixed sequence keeps its one plan.
						if spec, ok := parseMultiattackChoice(a.Desc, actions); ok && (problem != "" || spec.substitute != "") {
							dplan, _, dproblem := multiattackDeclaredPlan(spec, actions, declared)
							if dproblem != "" {
								refusal := refusalAudiences("The Multiattack could not be resolved. Nothing was rolled.", dproblem)
								return refusal, map[string]interface{}{"refused": true, "reason": dproblem}, true
							}
							plan, problem = dplan, ""
						}
					}
					if problem != "" {
						// a plan/cap problem is a server limit or an adjudication detail: the GM reads the
						// full reason, the shared log the player-safe refusal (docs/skill.md)
						refusal := refusalAudiences("The Multiattack could not be resolved. Nothing was rolled.", problem)
						return refusal, map[string]interface{}{"refused": true, "reason": problem}, true
					}
					chosen = withoutReplaced(campaignID, plan) // less what a replacement option takes (monster_replace_2024.go)
				} else {
					chosen = []monsterActionRow{a}
				}
				break
			}
		}
	}
	if len(chosen) == 0 {
		return "", nil, false
	}
	// An unreadable, ambiguous or over-cap use count invalidates the whole Multiattack before anything is rolled:
	// the GM adjudicates the plan, never a partial execution of it (the attacks are validated the same way below).
	if strings.EqualFold(stripUsage(action), "Multiattack") {
		multi := multiattackDesc(actions)
		for _, a := range multiattackUseActions(actions) {
			if _, problem := multiattackUseCount(multi, strings.ToLower(stripUsage(a.Name))); problem != "" {
				why := "The Multiattack is refused before anything is rolled: " + problem
				return why, map[string]interface{}{"refused": true, "reason": why}, true
			}
		}
	}
	// Validate the whole Multiattack before any part can deal damage.
	for i, a := range chosen {
		prepared, problem := prepareMonsterAttack(a, monsterUseIn(r))
		if problem != "" {
			return problem, map[string]interface{}{"refused": true, "reason": problem}, true
		}
		chosen[i] = prepared
	}
	// An attack the stat block locks while a grapple it created lasts, or while it is attached to a creature
	// ("Until the grapple ends, the kuo-toa can't make Pincer Staff attacks"): refused here, in the one attack
	// entry every route uses, before a use is reserved or anything is rolled; the lock reads live condition state.
	if attackerID != 0 {
		for _, a := range chosen {
			if why := monsterAttackLockout(campaignID, attackerID, actions, stripUsage(a.Name)); why != "" {
				return why, map[string]interface{}{"refused": true, "reason": why}, true
			}
		}
	}
	// a "(N/Day)" attack: the limit is checked here, in the one attack entry every route uses (narrate, a Legendary Action's attack,
	// the battle map), and the use is counted below once the attack was made against a target (monster_day_uses.go)
	dayKey, dayLimit := "", 0
	dayReserved, daySettled := false, false
	if attackerID != 0 && len(chosen) == 1 {
		dayKey, dayLimit = monsterDayUse(srdMonsterEntry{Name: chosen[0].Name, Text: chosen[0].Desc}, "")
		if dayKey != "" {
			if apply {
				if !reserveDayUse(campaignID, attackerID, dayKey, dayLimit) { // checked and reserved in one step (two requests cannot both take the last use)
					why := dayUseRefusal(loadMonsterState(campaignID, attackerID), attackerName, srdMonsterEntry{Name: chosen[0].Name}, "", dayKey, dayLimit)
					return why, map[string]interface{}{"refused": true, "reason": why}, true
				}
				dayReserved = true
			} else if why := dayUseRefusal(loadMonsterState(campaignID, attackerID), attackerName, srdMonsterEntry{Name: chosen[0].Name}, "", dayKey, dayLimit); why != "" {
				return why, map[string]interface{}{"refused": true, "reason": why}, true
			}
		}
	}
	if dayReserved { // every way out without an attack made against a target gives the reservation back
		defer func() {
			if !daySettled {
				releaseDayUse(campaignID, attackerID, dayKey)
			}
		}()
	}
	tid, tname, _, found := characterByNameOrID(campaignID, target)
	redirectNote := ""
	if found && allRangedAttacks(chosen) { // Shield of Missile Attraction's curse (missile_attraction_2024.go, F222)
		if nid, note := missileAttractionRedirect(campaignID, tid); nid != 0 {
			if t2, n2, _, ok := characterByNameOrID(campaignID, strconv.Itoa(nid)); ok {
				tid, tname, redirectNote = t2, n2, note
			}
		}
	}
	if found {
		if why := etherealBlock(campaignID, tid); why != "" { // Plate Armor of Etherealness
			return why, map[string]interface{}{"attacks": 0, "ethereal": true}, true
		}
	}
	if key == "avatar-of-death" && attackerID != 0 { // the Mysterious Deck's Avatar of Death (legendary_items_2024.go)
		var scythe monsterActionRow
		for _, a := range actions {
			if strings.EqualFold(a.Name, "Reaping Scythe") {
				scythe = a
			}
		}
		plan, why := avatarAttacks(campaignID, attackerID, tid, scythe)
		if why != "" {
			return why, map[string]interface{}{"attacks": 0, "avatar_targets_only_its_summoner": true}, true
		}
		if strings.EqualFold(stripUsage(action), "Multiattack") {
			chosen = plan
		}
	}
	// the attacker's conditions as every attack reads them (attack_2024.go); used here for what stops the whole attack and for
	// the reactions after a hit. The roll itself gathers both sides' conditions in attackCtx.
	var attackerConds []string
	if attackerID != 0 {
		chargeSaidNote = recordChargeSaid(r, campaignID, attackerID) // the GM said it charged (no battle map: creature_movement_2024.go)
		attackerConds = creatureConditions(campaignID, attackerID)
	}
	if found && seesUnseen(campaignID, tid) {
		attackerConds = dropInvisible(attackerConds) // See Invisibility, True Seeing, Blindsight: an Invisible monster gains nothing
	}
	if game.IsIncapacitated(attackerConds) {
		return attackerName + " is incapacitated (stunned, paralyzed, unconscious or the like) and can't attack.", map[string]interface{}{"attacks": 0, "incapacitated": true}, true
	}
	if attackerID != 0 {
		if why := commandBlock(campaignID, attackerID, attackerName); why != "" {
			return why, map[string]interface{}{"attacks": 0, "commanded": true}, true
		}
	}
	// "While attached to a target, it can attack only the target": refused here, at the one attack entry,
	// before a use is reserved or anything is rolled; the roll itself grants the Advantage (attack_2024.go).
	if found && attackerID != 0 {
		if why := attachOnlyTargetRefusal(campaignID, attackerID, tid); why != "" {
			return why, map[string]interface{}{"attacks": 0, "refused": true, "reason": why}, true
		}
	}
	if !found && attackerID != 0 {
		if mid := monsterTargetByName(campaignID, target); mid != 0 && mid != attackerID { // another monster or NPC of the fight (monster_vs_monster.go)
			if why := attachOnlyTargetRefusal(campaignID, attackerID, mid); why != "" {
				return why, map[string]interface{}{"attacks": 0, "refused": true, "reason": why}, true
			}
			text, info := monsterAttacksMonster(r, campaignID, attackerID, attackerName, attackerConds, chosen, actions, mid, apply)
			text += multiattackUses(r, campaignID, attackerID, attackerName, action, actions, target, apply) + chargeSaidNote
			if made, _ := info["made"].(bool); made && dayReserved {
				daySettled = true // a limited-use attack stays counted once it was made against a target
			}
			return text, info, true
		}
		if c, ok := companionByNameOrID(campaignID, target); ok { // a familiar, steed, summon or pet (companions_more_2024.go)
			if why := attachOnlyTargetRefusal(campaignID, attackerID, c.CombatID); why != "" {
				return why, map[string]interface{}{"attacks": 0, "refused": true, "reason": why}, true
			}
			text, info := monsterAttacksCompanion(campaignID, attackerID, attackerName, chosen, c, apply)
			text += multiattackUses(r, campaignID, attackerID, attackerName, action, actions, target, apply) + chargeSaidNote
			return text, info, true
		}
	}
	if found && attackerID != 0 {
		if sees, onMap := monsterSeesTarget(campaignID, attackerID, tid); onMap && !sees {
			return fmt.Sprintf("%s has Total Cover from %s (a wall or closed door is in the way): it can't be targeted. Move the monster first.", tname, attackerName),
				map[string]interface{}{"attacks": 0, "total_cover": true}, true
		}
	}
	var slowNote string
	if attackerID != 0 && len(chosen) > 1 && slowedAttackLimit(campaignID, attackerID) {
		chosen = chosen[:1]
		slowNote = " It is slowed, so it makes only one attack."
	}
	if attackerID != 0 && len(chosen) > 0 && monsterHasteAttack(campaignID, attackerID) {
		chosen = append(chosen, chosen[0]) // Haste: the extra action, used for one attack
		slowNote += " Haste: it uses its extra action for one more attack."
	}
	var lines []string
	totalDamage := 0
	hits := 0
	for _, a := range chosen {
		isMelee := meleeRe.MatchString(a.Desc) && !rangedOnlyRe.MatchString(a.Desc)
		if isMelee && found {
			setInMeleeRange(tid, true) // a monster attacking it in melee is within 5 feet (used without a battle map)
		}
		withinFive := isMelee
		longRange := false
		_, inReach := opportunityInReach.Load(fmt.Sprintf("%d:%d", campaignID, attackerID))
		if found && attackerID != 0 && !inReach {
			var refusal string
			if withinFive, longRange, refusal = monsterAttackRange(campaignID, attackerID, tid, a.Desc, a.Name, tname, isMelee); refusal != "" {
				lines = append(lines, refusal)
				continue
			}
		}
		// The attack roll: every source of Advantage and Disadvantage, the Armor Class, Bless and Bane, Sanctuary and the
		// judging of the roll are the one attack function's (attack_2024.go). Only what this monster's stat block and the
		// battle map say about this attack is decided here.
		atk := attackCtx{lobby: campaignID, attacker: attackerID, name: a.Name, bonus: a.AttackBonus, melee: isMelee, weapon: true, allySaid: allySaidIn(r),
			opportunity: inReach, longRange: longRange, within5: &withinFive, statBlockAttack: a.Desc, autoHit: autoHitRe.MatchString(a.Desc)}
		blockNote := ""
		if found {
			if apply {
				markExecuted(r) // the attack is made against a target (it may miss, be blocked or deal nothing: it is still used)
			}
			atk.target = tid
			if attackerID == 0 && hits > 0 && hunterDefense(tid) == "multiattack_defense" {
				atk.extraDis = append(atk.extraDis, "Multiattack Defense") // a monster outside the turn order: its hits are counted here
			}
			lost, note := atk.blocked()
			if lost {
				lines = append(lines, a.Name+": "+note)
				continue
			}
			blockNote = note // a Sanctuary save that succeeded
		}
		rolled, out, rollNote := atk.roll()
		rollNote = blockNote + rollNote
		if found && apply && attackerID < 0 && !isCompanionCombatID(attackerID) {
			rollNote += endWardsOnOffenseOf(campaignID, attackerID) // its attack roll ends its Invisibility and Sanctuary (after the roll they helped)
		}
		if !found {
			extra := ""
			for _, x := range attackExtraDamage(a.Desc) {
				if x.IfAdvantage && !rolled.HasAdvantage() {
					continue
				}
				extra += fmt.Sprintf(" plus %s %s", x.Dice, x.Type)
			}
			lines = append(lines, fmt.Sprintf("%s: %d%s to hit - %s %s%s damage if it hits", a.Name, out.Total, rolled.Describe()+rollNote, a.DamageDice, a.DamageType, extra))
			continue
		}
		total, ac, crit := out.Total, rolled.AC, out.Critical
		if !out.Hit {
			lines = append(lines, rolled.Line(a.Name, out)+rollNote+hitOrMissNote(a.Desc, false))
			continue
		}
		hitLine := rolled.Line(a.Name, out) + rollNote
		if absorbed, note := mirrorImageIntercept(campaignID, attackerID, tid, attackerConds); absorbed { // buff_spells_2024.go
			lines = append(lines, hitLine+", but "+note)
			continue
		} else if note != "" {
			hitLine += note
		}
		if len(a.Alts) > 0 {
			a = applyDamageAlternatives(a, monsterHitCondition(campaignID, attackerID, found, tid, rolled.HasAdvantage()))
		}
		chargeNote := ""
		a.Desc, chargeNote = hitChargeDesc(campaignID, attackerID, tid, a.Desc) // a charge's effect for this hit (creature_movement_2024.go)
		dmg := rollStatBlockDamage(a.DamageDice, a.FlatDamage, crit)
		sizeNote := ""
		if dx, dt := enlargeHit(campaignID, attackerID, dmg); dx != 0 { // Enlarge/Reduce on the monster (spells_more_2024.go)
			dmg += dx
			sizeNote = dt
		}
		if dmg < 0 {
			dmg = 0
		}
		hits++
		totalDamage += dmg
		line := hitLine + fmt.Sprintf(", %d %s damage", dmg, a.DamageType) + strings.TrimSuffix(sizeNote, ".")
		line += gmJudgedAlternatives(a) + chargeNote
		rider := unappliedHitRider(a.Desc) // appended after the riders: what they enforce is stripped first
		parts := []extraDamage{{Type: a.DamageType}}
		amounts := []int{dmg}
		for _, x := range attackExtraDamage(a.Desc) { // "plus 10 (3d6) Fire damage": rolled (doubled on a critical hit) and dealt as its own type
			if x.IfAdvantage && !rolled.HasAdvantage() {
				continue // "plus 2 (1d4) Slashing damage if the attack roll had Advantage" (Goblin Warrior): only then
			}
			n := x.roll(crit)
			parts, amounts = append(parts, x), append(amounts, n)
			totalDamage += n
			line += fmt.Sprintf(" plus %d %s damage", n, x.Type)
		}
		nat20 := out.Natural == 20 && !out.AutoHit
		if apply && !shieldWouldTurn(tid, total, ac, autoHitRe.MatchString(a.Desc), nat20) { // standing orders (reaction_orders_2024.go, F220)
			types := make([]string, len(parts))
			for i, p := range parts {
				types[i] = p.Type
			}
			if note := reactionBeforeDamage(campaignID, tid, attackerID, attackerConds, types, amounts); note != "" {
				line += note
			}
		}
		if apply && found && !isMelee && missileShieldResists(tid) { // Resistance to Ranged weapon damage, after Uncanny Dodge or Deflect Attacks (other modifiers first; F222)
			before := 0
			for i := range amounts {
				before += amounts[i]
				amounts[i] /= 2
			}
			line += fmt.Sprintf(" [Shield of Missile Attraction: Resistance to Ranged weapon damage, %d halved]", before)
		}
		if apply {
			hpB, tempB, deadB, concB := characterVitals(tid) // for a Shield cast in reply to this hit (reactions_2024.go)
			// one hit is one source (hit_points_2024.go): one Concentration save, its DC from the whole damage taken, and at 0 Hit
			// Points one Death Saving Throw failure, two from a Critical Hit (F93)
			hit := hpHit{Critical: crit}
			for i, p := range parts {
				hit.Parts = append(hit.Parts, hpPart{amounts[i], strings.ToLower(p.Type)})
			}
			var dealtByType map[string]int // what each damage type dealt (a typed Hit Point maximum drain, hitAfterDamage)
			if d, err := damageHP(tid, hit); err == nil {
				dealtByType = d.ByType
				if c, ok := d.View()["concentration"].(string); ok {
					line += " [" + c + "]" // the Concentration save the damage called for (F55)
				}
				line += fmt.Sprintf(" (%s now has %d HP)", tname, d.After.HP)
			}
			hpA, tempA, _, concA := characterVitals(tid)
			noteMonsterHit(campaignID, tid, monsterHit{Attacker: attackerName, Attack: a.Name, Total: total, AC: ac, Auto: autoHitRe.MatchString(a.Desc),
				HPBefore: hpB, TempBefore: tempB, HPAfter: hpA, TempAfter: tempA, DeadBefore: deadB, ConcBefore: concB, ConcAfter: concA, Ranged: !isMelee, Nat20: nat20})
			note, raised := reactionAfterHit(tid, total, ac, autoHitRe.MatchString(a.Desc), nat20) // Shield by standing order (F220)
			if note != "" {
				line += note
			}
			if isMelee && withinFive { // Fire Shield; a Fiend or Undead hitting into a Holy Aura (buff_spells_2024.go)
				line += fireShieldBack(campaignID, attackerID, attackerName, tid) + holyAuraBlinds(campaignID, attackerID, attackerName, tid)
			}
			// a hit's own saving throw ("If the target is a creature, it must make the following saving throw. Constitution Saving Throw:
			// DC 21. Failure: ... Poisoned"): rolled and applied by the save-effect owner (monster_effects_2024.go; feedback log 62)
			line += hitOrMissNote(a.Desc, true)
			if raised { // reactionAfterHit cast Shield because it turns this hit into a miss: a miss gives no conditions
				line += " [The hit became a miss: its riders are not applied.]"
			} else {
				recsBefore, effBefore := riderIDsOf(campaignID, tid)
				if e, ok := parseHitRider(a.Desc); ok { // the conditions the hit gives (Prone, Grappled with its escape DC, Stunned until the start of its next turn ...)
					e.SourceID = attackerID
					// "... and the darkmantle had Advantage on the attack roll": the gated conditions apply
					// only when the roll had it; without it the clause triggers nothing
					if !(e.CondIfAdvantage && !rolled.HasAdvantage()) {
						if given := e.giveConditions(campaignID, tid, attackerName+"'s "+stripUsage(a.Name)); len(given) > 0 {
							line += " [" + strings.Join(given, "; ") + "]"
						}
					}
				}
				line += hitAfterDamage(campaignID, tid, a.Desc, attackerName+"'s "+stripUsage(a.Name), (hpB+tempB)-(hpA+tempA), dealtByType) // Life Drain, burning (hit_after_damage_2024.go)
				// the attach the hit starts ("and the stirge attaches to the target"): before the recurring
				// damage, whose gate reads the attach row (monster_attach_2024.go). Outside the parseHitRider
				// branch: an attach rider (Stirge Proboscis) carries no condition for it to match.
				coverOK := true // the cover clause the conditions and suffocating hang on fired (its gates held)
				if e, ok := parseHitRider(a.Desc); ok {
					_, gated := e.conditionsFor(campaignID, tid)
					coverOK = gated == "" && !(e.CondIfAdvantage && !rolled.HasAdvantage())
				}
				if attached := addAttach(campaignID, attackerID, tid, a.Desc, attackerName+"'s "+stripUsage(a.Name), coverOK); attached != "" {
					line += " [" + attached + "]"
				}
				// recurring damage the hit sets up ("Until the grapple ends / While attached, the target takes
				// ... at the start of each of its/the attacker's turns"): recorded only when its gate holds
				// after this hit; when the gate did not hold, the clause goes back to the GM (it was stripped
				// at parse time)
				if rec, gmNote := addRecurringDamage(campaignID, attackerID, tid, a.Desc, attackerName); rec != "" {
					line += " [" + rec + "]"
				} else if gmNote != "" {
					line += " [GM, not applied automatically: " + gmNote + "]"
				}
				if e, ok := parseSaveEffect(a.Desc); ok {
					e.SourceID = attackerID
					if rl, _ := applySaveEffect(campaignID, attackerName+"'s "+stripUsage(a.Name), e, []int{tid}, true); len(rl) > 0 {
						line += " [" + strings.Join(rl, " ") + "]"
					}
				}
				recsAfter, effAfter := riderIDsOf(campaignID, tid)
				addRidersToLastHit(tid, newIDs(recsBefore, recsAfter), newIDs(effBefore, effAfter))
			}
			if rider != "" {
				if suffocationActive(campaignID, attackerID, tid) { // the cover applied: its suffocating is enforced, not the GM's
					rider = dropSuffocatingSentences(rider)
				}
				if rider != "" {
					line += " [GM, not applied automatically: " + rider + "]"
				}
			}
		}
		lines = append(lines, line)
	}
	text := attackerName + " attacks"
	if found {
		text += " " + tname
	}
	text += redirectNote
	text += ". " + strings.Join(lines, "; ") + "." + slowNote + chargeSaidNote
	if strings.EqualFold(stripUsage(action), "Multiattack") {
		for _, a := range actions {
			if strings.EqualFold(stripUsage(a.Name), "Multiattack") {
				if extra := multiattackExtraText(a.Desc); extra != "" {
					if declaredSubstituteUsed(r, a.Desc, actions) {
						// the validated declaration exercised the substitute: it resolved above, so its
						// sentence is no longer an unautomated option for the GM.
						extra = strings.Trim(strings.TrimSpace(replaceAttackSentenceRe.ReplaceAllString(extra, "")), " .")
					}
					if extra != "" {
						text += " [GM, not automated: " + extra + "]"
					}
				}
			}
		}
	}
	// the non-attack actions the Multiattack lists ("uses Antennae twice"), resolved as many times as the text says
	text += multiattackUses(r, campaignID, attackerID, attackerName, action, actions, target, apply)
	if dayReserved && found { // a limited-use attack stays counted once it was made against a target (monster_day_uses.go)
		daySettled = true
	}
	info := map[string]interface{}{"attacks": len(chosen), "hits": hits, "damage": totalDamage, "applied": apply && found}
	if found {
		info["target"] = tname
	}
	return text, info, true
}

// allRangedAttacks: every chosen attack is a Ranged attack (no melee).
func allRangedAttacks(chosen []monsterActionRow) bool {
	if len(chosen) == 0 {
		return false
	}
	for _, a := range chosen {
		if meleeRe.MatchString(a.Desc) && !rangedOnlyRe.MatchString(a.Desc) {
			return false
		}
	}
	return true
}

// hitOrMissNote tells the GM about a "Hit or Miss:" effect of an attack (Dao's Earth Burst: an explosion around the target whether the
// attack hits or not). The server rolls the attack and, on a hit, the target's own saving throw; the creatures around the target and the
// effect on a miss are the GM's, so the reply says so instead of dropping the text.
func hitOrMissNote(desc string, hit bool) string {
	i := strings.Index(desc, "Hit or Miss:")
	if i < 0 {
		return ""
	}
	text := strings.TrimSpace(desc[i+len("Hit or Miss:"):])
	if hit {
		return " [GM, not automated: Hit or Miss effect for the other creatures it reaches (the target's own save is rolled below): " + text + "]"
	}
	return " [GM, not automated: Hit or Miss effect applies on a miss too: " + text + "]"
}
