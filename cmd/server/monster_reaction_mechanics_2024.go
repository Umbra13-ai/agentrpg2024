package main

// A Reaction a GM attaches to a monster's stat block (the mechanic kind "reaction", monster_mechanics_2024.go): what it does when the GM
// uses it right after its trigger. The responses build on the reaction owner's record of the hit this turn (thisTurnHitOnMonster) and
// its undo and refund (monster_hit_undo_2024.go), the save owner (creatureSave), the rider duration owner and the Hit Point owners; the
// GM still judges the trigger, as for every Reaction.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// reactionResponses: the responses an attached reaction can have.
var reactionResponses = []string{"ac_bonus", "halve_damage", "resist_damage", "disadvantage_on_attack", "attacker_save_or_miss", "damage_back"}

// reactionWords: an attached reaction as the GM reads it.
func reactionWords(m entryMechanic) string {
	switch m.str("response") {
	case "ac_bonus":
		return fmt.Sprintf("adds %d to its AC against the attack that hit it", m.num("bonus"))
	case "halve_damage":
		return "halves the damage of the attack that hit it"
	case "resist_damage":
		s := "has Resistance to the hit's " + strings.Join(m.list("damage_types"), ", ") + " damage"
		if hp := m.num("temp_hp"); hp > 0 {
			s += fmt.Sprintf(" and gains %d Temporary Hit Points", hp)
		}
		return s
	case "disadvantage_on_attack":
		return "gives the attack roll against it Disadvantage after the roll"
	case "attacker_save_or_miss":
		s := fmt.Sprintf("the attacker makes a DC %d %s saving throw; on a failure the attack misses", m.num("dc"), strings.ToUpper(m.str("save")))
		if c := m.list("conditions"); len(c) > 0 {
			s += " and the attacker has " + strings.Join(c, " and ") + " until the end of its next turn"
		}
		return s
	case "damage_back":
		return fmt.Sprintf("deals %s %s damage to the triggering creature", m.str("damage"), titleCase(m.str("damage_type")))
	}
	return "a reaction"
}

// attachedReaction resolves an attached reaction. undoIfMiss is the reaction owner's own (the hit becomes a miss against a new Armor
// Class). refusal: nothing was used (no trigger the response needs, or the GM must name a creature).
func attachedReaction(r *http.Request, lobbyID, id int, name, base, key string, m entryMechanic, hit charHitOnMonster, haveHit bool, charName,
	target string, targetIDs []int, apply bool, undoIfMiss func(bonus int, label string) string) (out, refusal string) {
	resp := m.str("response")
	note := ""
	if n := m.str("note"); n != "" {
		note = " [GM: the stat block adds: " + n + "]"
	}
	if resp != "damage_back" {
		if !haveHit {
			return "", fmt.Sprintf("%s's %s answers an attack roll that hit it, and none hit %s this turn. Nothing was used.", name, base, name)
		}
		if a := m.str("against"); (a == "melee" && !hit.Melee) || (a == "ranged" && hit.Melee) {
			return "", fmt.Sprintf("%s's %s answers a %s attack; the attack that hit it this turn was not. Nothing was used.", name, base, a)
		}
	}
	if !apply {
		return fmt.Sprintf("%s would use %s: it %s (not applied).", name, base, reactionWords(m)), ""
	}
	head := name + " uses " + base + "."
	switch resp {
	case "ac_bonus":
		return head + undoIfMiss(m.num("bonus"), base) + note, ""
	case "halve_damage", "resist_damage":
		if resp == "resist_damage" && !slices.Contains(m.list("damage_types"), strings.ToLower(hit.DamageType)) {
			return "", fmt.Sprintf("%s's %s resists %s damage; the hit was %s damage. Nothing was used.", name, base, strings.Join(m.list("damage_types"), ", "), hit.DamageType)
		}
		taken := hitDamageTaken(hit)
		back, after, ok := refundHitDamage(lobbyID, id, key, hit, taken-taken/2) // it takes half, round down
		if !ok {
			return "", fmt.Sprintf("%s's %s: RECONCILIATION NEEDED, its Hit Points changed after the hit, so the server doesn't halve it; the GM settles it. Nothing was used.", name, base)
		}
		out = fmt.Sprintf("%s The hit's damage is halved: %d of %d taken back (it has %d HP, %d Temporary).", head, back, taken, after.HP, after.Temp)
		if hp := m.num("temp_hp"); resp == "resist_damage" && hp > 0 {
			if after.Temp < hp { // Temporary Hit Points don't add up: the higher stays
				setMonsterTempHP(lobbyID, id, hp)
			}
			out += fmt.Sprintf(" It gains %d Temporary Hit Points.", hp)
		}
		return out + note, ""
	case "disadvantage_on_attack":
		if hit.Natural == 0 {
			return "", fmt.Sprintf("%s's %s: the server doesn't know the d20 of the attack that hit it; the GM rolls the Disadvantage. Nothing was used.", name, base)
		}
		o, ok := game.LaterDisadvantage(hit.Natural, hit.Total, hit.AC, hit.Mode) // the attack roll owner (game/attack.go)
		if !ok {
			return "", fmt.Sprintf("%s's %s: the attack roll had Advantage, so Disadvantage cancels it to one d20, and which of the two d20s counts is the GM's. Nothing was used.", name, base)
		}
		if o.Dice[1] == 0 {
			return head + fmt.Sprintf(" The attack roll already had Disadvantage (or Advantage and Disadvantage): nothing changes; %s's attack still hits.", charName) + note, ""
		}
		out = fmt.Sprintf("%s The attack roll has Disadvantage: a second d20 rolls %d, so the d20 is %d (total %d against AC %d).", head, o.Dice[1], o.Natural, o.Total, hit.AC)
		if o.Hit {
			if hit.Crit && !o.Critical {
				// gm-one-shot: the Critical Hit's extra dice are settled by the GM in the same breath as the reply
				out += " [GM: it still hits, but the lower d20 may not be a Critical Hit any more: take back the extra dice if it isn't.]"
			}
			return out + fmt.Sprintf(" %s's attack still hits.", charName) + note, ""
		}
		after, said, ok := undoRecordedHit(lobbyID, id, hit)
		lastHitOnMonster.Delete(key)
		if !ok {
			return out + " It is now a miss." + said + note, ""
		}
		return out + fmt.Sprintf(" It is now a miss, its damage undone (%s is back to %d HP).", name, after.HP) + said + note, ""
	case "attacker_save_or_miss":
		saver := hit.CharID
		if saver == 0 {
			saver = hit.AttackerID
		}
		if saver == 0 {
			return "", fmt.Sprintf("%s's %s: the server doesn't know who made the attack; the GM rolls the save. Nothing was used.", name, base)
		}
		sv := creatureSave(lobbyID, saver, m.str("save"), m.num("dc"))
		out = head + " " + sv.Line(creatureName(lobbyID, saver))
		if sv.Success {
			return out + fmt.Sprintf(" %s's attack still hits.", charName) + note, ""
		}
		after, said, ok := undoRecordedHit(lobbyID, id, hit)
		lastHitOnMonster.Delete(key)
		if ok {
			out += fmt.Sprintf(" The attack misses instead, its damage undone (%s is back to %d HP).", name, after.HP) + said
		} else {
			out += " The attack misses instead." + said
		}
		for _, c := range m.list("conditions") {
			if a, ok := riderEndSourceTurnEnd.markerAdd(lobbyID, saver, id, c, name+"'s "+base, true); ok {
				if res := addCondition(lobbyID, saver, a); res.Immune != "" {
					out += fmt.Sprintf(" %s is immune to the %s condition (%s).", creatureName(lobbyID, saver), titleCase(c), res.Immune)
				} else {
					out += fmt.Sprintf(" %s has the %s condition until the end of %s's next turn.", creatureName(lobbyID, saver), titleCase(c), name)
				}
			}
		}
		return out + note, ""
	case "damage_back":
		victim := 0
		if len(targetIDs) > 0 {
			victim = targetIDs[0]
		} else if ids := targetIDsFrom(lobbyID, target, nil); len(ids) > 0 {
			victim = ids[0]
		} else if haveHit {
			victim = hit.CharID
			if victim == 0 {
				victim = hit.AttackerID
			}
		}
		if victim == 0 || victim == id {
			return "", fmt.Sprintf("%s's %s deals damage to the triggering creature: name it (target or target_ids). Nothing was used.", name, base)
		}
		amount := game.RollDamage(m.str("damage"), false)
		dtype := strings.ToLower(m.str("damage_type"))
		var text string
		switch {
		case isCompanionCombatID(victim):
			c, ok := activeCompanionInCampaign(lobbyID, victim)
			if !ok {
				return "", "That companion isn't active in this campaign. Nothing was used."
			}
			text = damageCompanion(c, amount, dtype)
		case victim < 0:
			text, _ = damageMonster(lobbyID, victim, amount, dtype)
		default:
			text = creatureName(lobbyID, victim) + " " + damageCharacter(victim, amount, dtype) + "."
		}
		return fmt.Sprintf("%s %s: %s", head, fmt.Sprintf("%d (%s) %s damage", amount, m.str("damage"), titleCase(dtype)), text) + note, ""
	}
	return "", fmt.Sprintf("%s's %s: the reaction's response isn't one the server knows. Nothing was used.", name, base)
}

// hitDamageTaken: what the recorded hit took from the monster, Temporary Hit Points included.
func hitDamageTaken(h charHitOnMonster) int {
	if h.EntryBefore == nil || h.EntryAfter == nil {
		return max(h.HPBefore-h.HPAfter, 0)
	}
	bv, av := monsterVitals(h.EntryBefore), monsterVitals(h.EntryAfter)
	return max(bv.HP-av.HP, 0) + max(bv.Temp-av.Temp, 0)
}
