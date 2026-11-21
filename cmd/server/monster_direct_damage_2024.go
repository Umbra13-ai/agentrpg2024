package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// This pattern recognizes a whole, unconditional delivery to targets selected
// by the GM. A damage clause in a larger rule does not establish its trigger,
// eligibility, frequency, movement, or any other effects.
const directDamageClause = `takes \d+ \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage`

var directDamageText = regexp.MustCompile(`(?i)^(the|each) target ` + directDamageClause + `\.?$`)
var embeddedDamageText = regexp.MustCompile(`(?i)` + directDamageClause)

type monsterDirectDamage struct {
	Targets      []int
	Next, Amount int
	Type         string
}

func init() {
	combatResumeHandlers["monster-direct-damage"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var damage monsterDirectDamage
		if err := json.Unmarshal(raw, &damage); err != nil {
			return nil, err
		}
		lines, _, err := runMonsterDirectDamage(lobby, damage)
		if err != nil {
			return nil, err
		}
		result := strings.Join(lines, " ")
		if result != "" {
			if err := recordGMAction(db, lobby, "monster_effect", "The monster's effect continues.", result); err != nil {
				return nil, err
			}
		}
		return map[string]interface{}{"result": result}, nil
	}
}

func resolveMonsterDirectDamage(r *http.Request, lobby int, name, entry, text string, targets []int, apply bool) ([]string, bool) {
	m := directDamageText.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		if !embeddedDamageText.MatchString(text) {
			return nil, false
		}
		message := fmt.Sprintf("%s needs GM adjudication. Check the compendium entry for its trigger, eligible targets, and all effects. Nothing was rolled.", entry)
		if apply {
			addGMNotice(lobby, name+": "+message)
		}
		return []string{message}, true
	}
	dice, dtype := normDice(m[2]), strings.ToLower(m[3])
	if !validStatBlockDamage(dice, dtype, nil) {
		return []string{"Unsupported damage expression. Nothing was rolled. [GM: resolve the damage as the stat block specifies.]"}, true
	}
	if len(targets) == 0 || (strings.EqualFold(m[1], "the") && len(targets) != 1) {
		return []string{"Name the target or targets as specified in the action. Nothing was rolled."}, true
	}
	amount := rollStatBlockDamage(dice, nil, false)
	lines := []string{fmt.Sprintf("%s uses %s.", name, entry)}
	if !apply {
		return append(lines, fmt.Sprintf("%d %s damage per target (not applied).", amount, dtype)), true
	}
	results, made, err := runMonsterDirectDamage(lobby, monsterDirectDamage{Targets: targets, Amount: amount, Type: dtype})
	if made {
		markExecuted(r)
	}
	if err != nil {
		noteDBError(err, "resolving monster direct damage")
		results = append(results, "[GM: the damage sequence could not finish. Review its state before continuing.]")
	}
	return append(lines, results...), true
}

func runMonsterDirectDamage(lobby int, damage monsterDirectDamage) (lines []string, made bool, err error) {
	if damage.Next < 0 || damage.Next > len(damage.Targets) || damage.Amount < 0 || damage.Amount > game.MaxDice*game.MaxDieSides+10000 || !attackDamageTypes[damage.Type] {
		return nil, false, fmt.Errorf("invalid stored direct damage")
	}
	for i := damage.Next; i < len(damage.Targets); i++ {
		target := damage.Targets[i]
		if len(targetIDsFrom(lobby, "", []int{target})) == 0 {
			lines = append(lines, "[GM: a saved damage target is no longer available in this campaign. No damage was applied to it.]")
			continue
		}
		next := damage
		next.Next = i + 1
		restore, err := armCombatFrame(lobby, "monster-direct-damage", next)
		if err != nil {
			return lines, made, err
		}
		var line string
		switch {
		case target < 0:
			if monsterAlive(lobby, target) {
				line, _ = damageMonster(lobby, target, damage.Amount, damage.Type)
			}
		case isCompanionCombatID(target):
			if companion, active := activeCompanionInCampaign(lobby, target); active {
				line = damageCompanion(companion, damage.Amount, damage.Type)
			}
		default:
			line = creatureName(lobby, target) + " " + damageCharacter(target, damage.Amount, damage.Type)
		}
		restore()
		if line != "" {
			lines = append(lines, line)
			made = true
		} else {
			lines = append(lines, "[GM: a damage target could not be resolved. Check its state before continuing.]")
		}
		if combatResolutionPaused(lobby) {
			break
		}
	}
	return lines, made, nil
}
