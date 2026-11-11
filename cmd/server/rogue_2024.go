package main

// Rogue, 2024 rules (SRD 5.2.1).
//	Cunning Strike (level 5)        add "cunning strike poison|trip|withdraw" to an attack that deals Sneak Attack damage:
//	                                the effect costs Sneak Attack dice (removed before rolling) and happens after the damage.
//	                                Level 11: two effects. Level 14 adds daze, knock out, obscure. Thief level 9 adds stealth attack.
//	                                The saving throw DC is 8 + Dexterity modifier + Proficiency Bonus.
//	Steady Aim (level 3)            action "steady_aim": a Bonus Action; Advantage on the next attack roll this turn;
//	                                only if you have not moved, and your Speed is 0 for the rest of the turn.
//	Stroke of Luck (level 20)       POST /api/gm/stroke-of-luck: turn a failed D20 Test into a 20 (modes attack, ability_check, saving_throw).

import (
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type cunningEffect struct {
	Key, Name string
	Cost      int
	MinLevel  int
	Save      string // the target's saving throw ability, "" when there is none
	Thief     bool
}

var cunningEffects = []cunningEffect{
	{"poison", "Poison", 1, 5, "con", false},
	{"trip", "Trip", 1, 5, "dex", false},
	{"withdraw", "Withdraw", 1, 5, "", false},
	{"daze", "Daze", 2, 14, "con", false},
	{"knock out", "Knock Out", 6, 14, "con", false},
	{"obscure", "Obscure", 3, 14, "dex", false},
	{"stealth attack", "Stealth Attack", 1, 9, "", true},
}

type cunningPlan struct {
	Effects []cunningEffect
	DC      int
}

var pendingCunning sync.Map // character id -> cunningPlan, applied when the attack is known to hit

// sneakDiceWithCunning is the Sneak Attack dice string after paying for the chosen Cunning Strike effects.
func sneakDiceWithCunning(charID, level int, descLower string) (string, string) {
	dice := game.SneakAttackDice(level)
	pendingCunning.Delete(charID)
	if level < 5 || !strings.Contains(descLower, "cunning strike") {
		return fmt.Sprintf("%dd6", dice), ""
	}
	limit := 1
	if level >= 11 {
		limit = 2
	}
	var sub string
	var dex int
	db.QueryRow(`SELECT COALESCE(subclass,''), dex FROM characters WHERE id = $1`, charID).Scan(&sub, &dex)
	var chosen []cunningEffect
	cost := 0
	for _, e := range cunningEffects {
		if len(chosen) >= limit || level < e.MinLevel || !strings.Contains(descLower, e.Key) {
			continue
		}
		if e.Thief && strings.ToLower(sub) != "thief" {
			continue
		}
		if dice-cost-e.Cost < 1 { // the attack must keep at least one Sneak Attack die
			continue
		}
		chosen = append(chosen, e)
		cost += e.Cost
	}
	if len(chosen) == 0 {
		return fmt.Sprintf("%dd6", dice), " [Cunning Strike: no effect could be paid for]"
	}
	pendingCunning.Store(charID, cunningPlan{Effects: chosen, DC: 8 + game.Modifier(dex) + game.ProficiencyBonus(level)})
	var names []string
	for _, e := range chosen {
		names = append(names, fmt.Sprintf("%s (-%dd6)", e.Name, e.Cost))
	}
	return fmt.Sprintf("%dd6", dice-cost), " [Cunning Strike: " + strings.Join(names, ", ") + "]"
}

// addMonsterCondition records a condition on a monster in the current combat (as a GM-applied condition would be).
func addMonsterCondition(lobbyID, targetID int, cond, source string, rounds int, saveAbility string, dc int) {
	if targetID > 0 {
		defer endWildShapeIfDown(targetID) // an Incapacitating condition ends a druid's Wild Shape (wild_shape_2024.go)
	}
	if targetID > 0 {
		if imm, _ := buffConditionImmune(lobbyID, targetID, cond, 0, false); imm {
			return // a spell's Immunity on the character (Mind Blank, Heroes' Feast, Gaseous Form ...; F80)
		}
	}
	if heroismImmune(lobbyID, targetID, cond) || (targetID < 0 && monsterConditionImmune(lobbyID, targetID, cond)) {
		return // Heroism, or the monster's own condition immunities (its stat block)
	}
	for _, c := range conditionNames(lobbyID, targetID) {
		if c == cond {
			return // already has it
		}
	}
	var end interface{}
	if saveAbility != "" {
		end = endSaveJSON(&conditionSave{Ability: saveAbility, DC: dc})
	}
	db.Exec(`INSERT INTO condition_records (lobby_id, target_id, condition, source, escapes, duration_rounds, end_save, notes)
		VALUES ($1,$2,$3,$4,'[]',$5,$6,'')`, lobbyID, targetID, cond, source, rounds, end)
	if targetID > 0 {
		syncListFromRecords(targetID)
	}
}

// monsterSave rolls a monster's saving throw against a DC and returns whether it succeeded. A failure against an effect with a
// condition can be turned into a success by Legendary Resistance (monsterSaveFor gives the stakes).
func monsterSave(lobbyID, targetID int, ability string, dc int) (bool, int) {
	return monsterSaveFor(lobbyID, targetID, ability, dc, saveStakes{Condition: true})
}

func monsterSaveFor(lobbyID, targetID int, ability string, dc int, st saveStakes) (bool, int) {
	saved, total := monsterSaveRoll(lobbyID, targetID, ability, dc)
	if !saved && targetID < 0 && legendaryResistance(lobbyID, targetID, st) {
		return true, total
	}
	return saved, total
}

func monsterSaveRoll(lobbyID, targetID int, ability string, dc int) (bool, int) {
	bonus, autoFail, dis := saveBonusFor(lobbyID, targetID, ability)
	dis = dis || moreSaveDisadvantage(lobbyID, targetID, ability)                               // Reduce (spells_more_2024.go)
	adv := spellSaveAdvantage(lobbyID, targetID, ability) || auraOfAuthority(lobbyID, targetID) // Haste; Aura of Authority
	roll := game.RollDie(20)
	if dis && !adv {
		if b := game.RollDie(20); b < roll {
			roll = b
		}
	} else if adv && !dis {
		if b := game.RollDie(20); b > roll {
			roll = b
		}
	}
	return !autoFail && roll+bonus >= dc, roll + bonus
}

// applyCunningStrike applies the chosen effects after a hit.
func applyCunningStrike(lobbyID, charID, targetID int, name, monsterKey string) string {
	v, ok := pendingCunning.LoadAndDelete(charID)
	if !ok {
		return ""
	}
	plan := v.(cunningPlan)
	out := ""
	for _, e := range plan.Effects {
		switch e.Key {
		case "withdraw":
			out += " Withdraw: you move up to half your Speed without provoking Opportunity Attacks."
		case "stealth attack":
			out += " Stealth Attack: this attack doesn't end your Invisible condition if you end the turn behind Three-Quarters or Total Cover."
		default:
			saved, total := monsterSave(lobbyID, targetID, e.Save, plan.DC)
			if saved {
				out += fmt.Sprintf(" Cunning Strike %s: %s succeeds on its %s save (%d vs DC %d).", e.Name, name, strings.ToUpper(e.Save), total, plan.DC)
				continue
			}
			switch e.Key {
			case "poison":
				addMonsterCondition(lobbyID, targetID, "poisoned", "Cunning Strike: Poison", 10, "con", plan.DC)
			case "trip":
				if sizeAtMost(monsterKey, "large") {
					addMonsterCondition(lobbyID, targetID, "prone", "Cunning Strike: Trip", 0, "", 0)
				} else {
					out += fmt.Sprintf(" Cunning Strike Trip: %s is too big to be tripped.", name)
					continue
				}
			case "daze":
				addCombatEffect(lobbyID, targetID, charID, "dazed")
			case "knock out":
				addMonsterCondition(lobbyID, targetID, "unconscious", "Cunning Strike: Knock Out", 10, "con", plan.DC)
			case "obscure":
				addMonsterCondition(lobbyID, targetID, "blinded", "Cunning Strike: Obscure", 1, "", 0)
			}
			out += fmt.Sprintf(" Cunning Strike %s: %s fails its %s save (%d vs DC %d)%s.", e.Name, name, strings.ToUpper(e.Save), total, plan.DC, cunningResult[e.Key])
		}
	}
	return out
}

var cunningResult = map[string]string{
	"poison":    " and is Poisoned for 1 minute",
	"trip":      " and has the Prone condition",
	"daze":      " and is Dazed: on its next turn it can take only one of a move, an action or a Bonus Action",
	"knock out": " and is Unconscious for 1 minute",
	"obscure":   " and is Blinded until the end of its next turn",
}

func sizeAtMost(monsterKey, limit string) bool {
	var size string
	db.QueryRow(`SELECT COALESCE(size,'medium') FROM monsters WHERE slug = $1`, monsterKey).Scan(&size)
	o, ok := sizeOrder[strings.ToLower(strings.SplitN(size, " ", 2)[0])]
	return !ok || o <= sizeOrder[limit]
}

// steadyAim is the Bonus Action of Steady Aim (Rogue level 3).
func steadyAim(charID int) string {
	var class string
	var level, moveLeft, speed int
	var species string
	if db.QueryRow(`SELECT class, level, COALESCE(movement_remaining,0), COALESCE(species,'') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &moveLeft, &species) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "rogue" || level < 3 {
		return "Only a Rogue of level 3 or higher can take Steady Aim."
	}
	speed = characterSpeed(charID, species)
	if moveLeft < speed {
		return "You can't use Steady Aim if you have moved during this turn."
	}
	db.Exec(`UPDATE characters SET steady_aim = true, movement_remaining = 0 WHERE id = $1`, charID)
	return "🎯 Steady Aim: you have Advantage on your next attack roll this turn, and your Speed is 0 until the end of the turn."
}

// takeSteadyAim uses up the Advantage of Steady Aim.
func takeSteadyAim(charID int) bool {
	res, err := db.Exec(`UPDATE characters SET steady_aim = false WHERE id = $1 AND steady_aim = true`, charID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func ensureRogueColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS steady_aim BOOLEAN DEFAULT FALSE`)
	}
}
