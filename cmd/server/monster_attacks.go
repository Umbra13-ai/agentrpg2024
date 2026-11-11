package main

// A monster's attack on a character, resolved by the server from the monster's own stat block (2024 data).
// POST /api/gm/narrate with "monster_action": {"monster": "goblin-warrior" (or its name in the fight), "action":
// "Scimitar" (an action of the stat block, or "Multiattack"), "target": "<character name or id>", "apply_damage": true}.
// The server rolls the attack bonus printed in the stat block against the target's Armor Class, with Advantage and
// Disadvantage from the conditions of both sides (Prone, Restrained, Paralyzed, Poisoned, Sap, ...), doubles the damage
// dice on a critical hit (a natural 20, or a hit on a Paralyzed or Unconscious target within 5 feet) and, with
// apply_damage, deals the damage to the character through the same code as /api/characters/{id}/damage.
// Without a known target the old behaviour remains: the roll and the damage if it hits.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type monsterActionRow struct {
	Name        string `json:"name"`
	AttackBonus int    `json:"attack_bonus"`
	DamageDice  string `json:"damage_dice"`
	DamageType  string `json:"damage_type"`
	Desc        string `json:"desc"`
}

var countWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6}

// multiattackPlan reads a Multiattack description ("makes two Tentacle attacks and uses ...") into attack names and counts.
func multiattackPlan(desc string, actions []monsterActionRow) []monsterActionRow {
	low := strings.ToLower(desc)
	var plan []monsterActionRow
	for _, a := range actions {
		if strings.EqualFold(stripUsage(a.Name), "Multiattack") || a.DamageDice == "" {
			continue
		}
		n := strings.ToLower(stripUsage(a.Name)) // "Grave Strike (Vampire Form Only)" is "Grave Strike" in the Multiattack text
		idx := strings.Index(low, n)
		if idx < 0 {
			continue
		}
		count := 1
		pre := strings.Fields(low[:idx])
		if len(pre) > 0 {
			w := strings.Trim(pre[len(pre)-1], ",.")
			if c, ok := countWords[w]; ok {
				count = c
			} else if c, err := strconv.Atoi(w); err == nil {
				count = c
			}
		}
		for i := 0; i < count; i++ {
			plan = append(plan, a)
		}
	}
	if len(plan) == 0 { // "makes two attacks": use the first attack in the list
		for _, a := range actions {
			if a.DamageDice != "" && !strings.EqualFold(stripUsage(a.Name), "Multiattack") {
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
	return plan
}

var meleeRe = regexp.MustCompile(`(?i)melee`)

// extraDamageRe: a hit's second damage type in a 2024 stat block, "Hit: 21 (4d6 + 7) Slashing damage plus 10 (3d6) Fire damage".
var extraDamageRe = regexp.MustCompile(`(?i)\bplus \d+ \((\d+d\d+(?:\s*[+-]\s*\d+)?)\) ([A-Za-z]+) damage`)

type extraDamage struct{ Dice, Type string }

// attackExtraDamage lists the extra damage of an attack ("plus 10 (3d6) Fire damage"); only the part after the first "Hit:".
func attackExtraDamage(desc string) []extraDamage {
	if i := strings.Index(desc, "Hit:"); i >= 0 {
		desc = desc[i:]
	}
	var out []extraDamage
	for _, m := range extraDamageRe.FindAllStringSubmatch(desc, -1) {
		out = append(out, extraDamage{strings.ReplaceAll(m[1], " ", ""), m[2]})
	}
	return out
}

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
func resolveMonsterAttack(r *http.Request, campaignID int, monster, action, target string, apply bool) (string, map[string]interface{}, bool) {
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
	if db.QueryRow(`SELECT COALESCE(actions,'[]'), COALESCE(str,10) FROM monsters WHERE slug = $1`, key).Scan(&raw, &str) != nil {
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
					chosen = multiattackPlan(a.Desc, actions)
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
	tid, tname, ac, found := characterByNameOrID(campaignID, target)
	redirectNote := ""
	if found && allRangedAttacks(chosen) { // Shield of Missile Attraction's curse (missile_attraction_2024.go, F222)
		if nid, note := missileAttractionRedirect(campaignID, tid); nid != 0 {
			if t2, n2, ac2, ok := characterByNameOrID(campaignID, strconv.Itoa(nid)); ok {
				tid, tname, ac, redirectNote = t2, n2, ac2, note
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
	if !found && attackerID != 0 {
		if c, ok := companionByNameOrID(campaignID, target); ok { // a familiar, steed, summon or pet (companions_more_2024.go)
			if why := commandBlock(campaignID, attackerID, attackerName); why != "" {
				return why, map[string]interface{}{"attacks": 0, "commanded": true}, true
			}
			text, info := monsterAttacksCompanion(campaignID, attackerID, attackerName, chosen, c, apply)
			return text, info, true
		}
	}
	if found {
		refreshConcentrationConditions(tid) // Invisibility whose caster lost Concentration
	}
	var targetConds []string
	if found {
		targetConds = withoutInvisible(campaignID, tid, append(getCharConditions(tid), conditionNames(campaignID, tid)...)) // Faerie Fire
		ac += spellACAdjust(campaignID, tid) + halfCoverBonus(campaignID, tid)                                              // Slow, Shield of Faith, Haste; Smite of Protection
	}
	var attackerConds []string
	if attackerID != 0 {
		attackerConds = withoutInvisible(campaignID, attackerID, append(conditionNames(campaignID, attackerID), combatEffectNames(campaignID, attackerID)...))
	}
	if found && buffSeesInvisible(tid) {
		attackerConds = dropInvisible(attackerConds) // See Invisibility, True Seeing: an Invisible monster gains nothing (buff_spells_2024.go)
	}
	if game.IsIncapacitated(attackerConds) {
		return attackerName + " is incapacitated (stunned, paralyzed, unconscious or the like) and can't attack.", map[string]interface{}{"attacks": 0, "incapacitated": true}, true
	}
	if attackerID != 0 {
		if why := commandBlock(campaignID, attackerID, attackerName); why != "" {
			return why, map[string]interface{}{"attacks": 0, "commanded": true}, true
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
			if dist, onMap := monsterDistance(campaignID, attackerID, tid); onMap {
				if n := monsterNormalRange(a.Desc); n > 0 && dist > n {
					longRange = true // beyond the normal range: Disadvantage
				}
				reachFt, ranged := monsterAttackReach(a.Desc)
				if dist > reachFt {
					what := "reach"
					if ranged {
						what = "range"
					}
					lines = append(lines, fmt.Sprintf("%s: %s is %d ft away, beyond its %d ft %s - no attack (move the monster on the battle map first)", a.Name, tname, dist, reachFt, what))
					continue
				}
				withinFive = dist <= 5
			}
		}
		var advWhy []string // why the attack has Advantage, shown when Disadvantage cancels it
		if found && game.GetAttackAdvantage(targetConds, isMelee, withinFive) {
			advWhy = append(advWhy, "the target is "+strings.Join(targetConds, ", "))
		}
		if found && outlined(campaignID, tid) {
			advWhy = append(advWhy, "Faerie Fire")
		}
		if attackerID != 0 && auraOfAuthority(campaignID, attackerID) {
			advWhy = append(advWhy, "Aura of Authority") // Hobgoblin Captain
		}
		adv := len(advWhy) > 0
		hunterDef := ""
		if found {
			hunterDef = hunterDefense(tid)
		}
		dis := (inReach && hunterDef == "escape_the_horde") || (hits > 0 && hunterDef == "multiattack_defense") || // Hunter's Defensive Tactics
			game.GetAttackDisadvantage(attackerConds, 0, false) || (found && game.GetAttackDisadvantageVsTarget(targetConds, !isMelee, withinFive)) || longRange ||
			(!isMelee && attackerID != 0 && characterAdjacentToMonster(campaignID, attackerID)) || // a ranged attack with an enemy within 5 feet
			grappledAttackPenalty(campaignID, attackerID, tid) // Grappled: Disadvantage against anyone but the grappler
		if attackerID != 0 && takeCombatEffect(campaignID, attackerID, "sap", 0) {
			dis = true // Sap
		}
		if found && elementalBane(tid, key) {
			dis = true // Ring of Elemental Command: an Elemental attacks the wearer with Disadvantage
		}
		buffDis := ""
		if found {
			if d, why := buffAttackDisadvantage(campaignID, attackerID, tid); d { // Foresight, Blur, Holy Aura, Protection from Evil and Good
				dis, buffDis = true, why
			}
			if lost, note := sanctuaryBlocks(campaignID, attackerID, attackerName, tid); lost {
				lines = append(lines, a.Name+": "+note)
				continue
			}
		}
		var roll int
		switch {
		case adv && !dis:
			roll, _, _ = game.RollWithAdvantage()
		case dis && !adv:
			roll, _, _ = game.RollWithDisadvantage()
		default:
			roll = game.RollDie(20)
		}
		total := roll + a.AttackBonus
		how := ""
		if adv && !dis {
			how = " with Advantage"
		} else if dis && !adv {
			how = " with Disadvantage"
		}
		if longRange {
			how += " (long range)"
		}
		if buffDis != "" && dis && !adv {
			how += " (" + buffDis + ")"
		}
		if adv && dis {
			how = " (Advantage from " + strings.Join(advWhy, " and ") + " and Disadvantage" + map[bool]string{true: " from " + buffDis, false: ""}[buffDis != ""] + " cancel: one d20)" + how
		}
		if attackerID != 0 {
			if adj, note := spellRollAdjust(campaignID, attackerID, "attack"); note != "" { // Bless, Bane
				total += adj
				how += note
			}
		}
		if !found {
			extra := ""
			for _, x := range attackExtraDamage(a.Desc) {
				extra += fmt.Sprintf(" plus %s %s", x.Dice, x.Type)
			}
			lines = append(lines, fmt.Sprintf("%s: %d to hit%s - %s %s%s damage if it hits", a.Name, total, how, a.DamageDice, a.DamageType, extra))
			continue
		}
		hit := roll != 1 && (roll == 20 || total >= ac) // a natural 1 misses, a natural 20 hits (F218: Paralyzed was an automatic hit)
		crit := hit && (roll == 20 || game.IsAutoCrit(targetConds) && withinFive)
		if autoHitRe.MatchString(a.Desc) {
			hit, crit = true, false // "Automatic hit": no attack roll (the Avatar of Death)
		}
		if !hit {
			lines = append(lines, fmt.Sprintf("%s: %d%s vs AC %d - MISS", a.Name, total, how, ac))
			continue
		}
		if absorbed, note := mirrorImageIntercept(campaignID, attackerID, tid, attackerConds); absorbed { // buff_spells_2024.go
			lines = append(lines, fmt.Sprintf("%s: %d%s vs AC %d - HIT, but %s", a.Name, total, how, ac, note))
			continue
		} else if note != "" {
			how += note
		}
		dmg := game.RollDamage(a.DamageDice, crit)
		sizeNote := ""
		if dx, dt := enlargeHit(campaignID, attackerID, dmg); dx != 0 { // Enlarge/Reduce on the monster (spells_more_2024.go)
			dmg += dx
			sizeNote = dt
		}
		if dmg < 1 {
			dmg = 1
		}
		hits++
		totalDamage += dmg
		label := "HIT"
		if crit {
			label = "CRITICAL HIT"
		}
		line := fmt.Sprintf("%s: %d%s vs AC %d - %s, %d %s damage", a.Name, total, how, ac, label, dmg, a.DamageType) + strings.TrimSuffix(sizeNote, ".")
		parts := []extraDamage{{"", a.DamageType}}
		amounts := []int{dmg}
		for _, x := range attackExtraDamage(a.Desc) { // "plus 10 (3d6) Fire damage": rolled (doubled on a critical hit) and dealt as its own type
			n := game.RollDamage(x.Dice, crit)
			parts, amounts = append(parts, x), append(amounts, n)
			totalDamage += n
			line += fmt.Sprintf(" plus %d %s damage", n, x.Type)
		}
		nat20 := roll == 20 && !autoHitRe.MatchString(a.Desc)
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
			var lastHP interface{}
			hpB, tempB, deadB, concB := characterVitals(tid) // for a Shield cast in reply to this hit (reactions_2024.go)
			for i, p := range parts {
				part := map[string]interface{}{"damage": amounts[i], "damage_type": strings.ToLower(p.Type)}
				if i == 0 { // one hit is one source: one Concentration save, its DC from the whole damage
					sum := 0
					for _, n := range amounts {
						sum += n
					}
					part["concentration_damage"] = sum
					part["critical"] = crit // at 0 Hit Points a Critical Hit is two death save failures (F93)
				} else {
					part["skip_concentration"] = true
					part["same_hit"] = true // one failure per hit at 0 Hit Points
				}
				body, _ := json.Marshal(part)
				rr := httptest.NewRecorder()
				rq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
				rq.Header = r.Header.Clone()
				handleDamage(rr, rq, tid)
				var out map[string]interface{}
				json.Unmarshal(rr.Body.Bytes(), &out)
				if hp, ok := out["hp"]; ok {
					lastHP = hp
				}
				if c, ok := out["concentration"].(string); ok {
					line += " [" + c + "]" // the Concentration save the damage called for (F55)
				}
			}
			if lastHP != nil {
				line += fmt.Sprintf(" (%s now has %v HP)", tname, lastHP)
			}
			hpA, tempA, _, concA := characterVitals(tid)
			noteMonsterHit(campaignID, tid, monsterHit{Attacker: attackerName, Attack: a.Name, Total: total, AC: ac, Auto: autoHitRe.MatchString(a.Desc),
				HPBefore: hpB, TempBefore: tempB, HPAfter: hpA, TempAfter: tempA, DeadBefore: deadB, ConcBefore: concB, ConcAfter: concA, Ranged: !isMelee, Nat20: nat20})
			if note, raised := reactionAfterHit(tid, total, ac, autoHitRe.MatchString(a.Desc), nat20); note != "" { // Shield by standing order (F220)
				line += note
				if raised {
					ac += 5 // the rest of this monster's attacks face the Shield
				}
			}
			if isMelee && withinFive { // Fire Shield; a Fiend or Undead hitting into a Holy Aura (buff_spells_2024.go)
				line += fireShieldBack(campaignID, attackerID, attackerName, tid) + holyAuraBlinds(campaignID, attackerID, attackerName, tid)
			}
		}
		lines = append(lines, line)
	}
	text := attackerName + " attacks"
	if found {
		text += " " + tname
	}
	text += redirectNote
	text += ". " + strings.Join(lines, "; ") + "." + slowNote
	// a Multiattack that also uses an action without an attack roll ("makes two Grave Strike attacks and uses Bite"): resolve it too
	if strings.EqualFold(stripUsage(action), "Multiattack") && attackerID != 0 {
		for _, a := range actions {
			if a.DamageDice != "" || strings.EqualFold(stripUsage(a.Name), "Multiattack") {
				continue
			}
			var multi string
			for _, m := range actions {
				if strings.EqualFold(stripUsage(m.Name), "Multiattack") {
					multi = strings.ToLower(m.Desc)
				}
			}
			if !strings.Contains(multi, "uses "+strings.ToLower(stripUsage(a.Name))) && !strings.Contains(multi, "and "+strings.ToLower(stripUsage(a.Name))) {
				continue
			}
			if txt, handled, refusal := resolveMonsterActionByName(r, campaignID, attackerName, stripUsage(a.Name), target, nil, apply, ""); refusal != "" {
				text += " " + refusal
			} else if handled && txt != "" {
				text += " " + stripUsage(a.Name) + ": " + txt
			}
		}
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
