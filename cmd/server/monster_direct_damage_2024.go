package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// embeddedDamageText: a damage clause inside a larger rule, which doesn't establish its trigger, eligibility, frequency or other effects.
var embeddedDamageText = regexp.MustCompile(`(?i)takes \d+ \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage`)

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

// resolveEmbeddedDamage: an entry whose damage sits inside a larger rule ("..., and each creature within 10 feet of the space it left
// takes 11 (2d10) Necrotic damage": the Lich's Deathly Teleport) is the GM's: who is hit and when are the rule's, so nothing is rolled.
func resolveEmbeddedDamage(lobby int, name, entry, text string, apply bool) ([]string, bool) {
	if !embeddedDamageText.MatchString(text) {
		return nil, false
	}
	message := fmt.Sprintf("%s needs GM adjudication. Check the compendium entry for its trigger, eligible targets, and all effects. Nothing was rolled.", entry)
	if apply {
		addGMNotice(lobby, name+": "+message)
	}
	return []string{message}, true
}

// runMonsterDirectDamage deals one rolled amount to each target in turn through each kind's Hit Point owner (an aura at the end of the
// monster's turn: runMonsterEndAuras); it saves the rest of the targets before each one, so a pause (a Concentration decision) resumes
// with the same roll.
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
