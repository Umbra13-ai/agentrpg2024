package main

// The post-damage steps of a character's hit use the same continuation owner as
// turn and stat-block effects. Every choice is captured before the first damage
// save can pause; process-local pending choices are not a restart dependency.
import (
	"encoding/json"
	"fmt"
)

type characterHitTail struct {
	Char, Target, HP, Damage, BaseDamage, CleaveTarget, Stage int
	Name, Key                                                 string
	Outcome                                                   attackOutcome
	HasMastery                                                bool
	Mastery                                                   masteryUse
	Snapshot                                                  hitSnapshot
	Record                                                    charHitOnMonster
	Cunning                                                   *cunningPlan
	Brutal                                                    []string
	StunDC                                                    int
	Giant                                                     bool
}

func (c *characterHitTail) takePlans() {
	if v, ok := pendingCunning.LoadAndDelete(c.Char); ok {
		p := v.(cunningPlan)
		c.Cunning = &p
	}
	if v, ok := pendingBrutal.LoadAndDelete(c.Char); ok {
		c.Brutal = v.([]string)
	}
	if v, ok := pendingStun.LoadAndDelete(c.Char); ok {
		c.StunDC = v.(int)
	}
	_, c.Giant = pendingGiantBoon.LoadAndDelete(c.Char)
}
func init() {
	combatResumeHandlers["character-hit-tail"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var c characterHitTail
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		if !monsterInFight(lobby, c.Target) || lobbyOfChar(c.Char) != lobby {
			return nil, fmt.Errorf("saved hit no longer belongs to this fight")
		}
		text, err := runCharacterHitTail(lobby, c)
		if text != "" {
			recordGMAction(db, lobby, "attack_effect", text, text)
		}
		return map[string]interface{}{"result": text}, err
	}
}
func runCharacterHitTail(lobby int, c characterHitTail) (string, error) {
	text := ""
	for c.Stage < 9 {
		next := c
		next.Stage++
		restore, err := armCombatFrame(lobby, "character-hit-tail", next)
		if err != nil {
			return text, err
		}
		switch c.Stage {
		case 0:
			if c.HasMastery && c.HP > 0 {
				text += masteryOnHit(lobby, c.Char, c.Target, c.Name, c.Key, c.Mastery, c.BaseDamage)
			}
		case 1:
			if c.HasMastery && c.Mastery.Name == "Cleave" {
				if c.CleaveTarget != 0 {
					text += cleaveAttack(lobby, c.Char, c.Target, c.CleaveTarget, c.Outcome, c.Mastery)
				} else if !cleaveUsedThisTurn(c.Char) {
					text += " (Cleave: with this weapon a hit lets you attack a second creature within 5 feet of the first, once per turn: send cleave_target_id with the attack.)"
				}
			}
		case 2:
			text += superiorHuntersPrey(lobby, c.Char, c.Target)
		case 3:
			if c.Giant {
				pendingGiantBoon.Store(c.Char, true)
			}
			text += applyGiantBoon(lobby, c.Char, c.Target, c.Damage, c.HP > 0)
		case 4:
			if c.HP > 0 && c.Cunning != nil {
				pendingCunning.Store(c.Char, *c.Cunning)
				text += applyCunningStrike(lobby, c.Char, c.Target, c.Name, c.Key)
			}
		case 5:
			if c.HP > 0 && c.StunDC > 0 {
				pendingStun.Store(c.Char, c.StunDC)
				text += applyStunningStrike(lobby, c.Char, c.Target, c.Name)
			}
		case 6:
			if c.HP > 0 && len(c.Brutal) > 0 {
				pendingBrutal.Store(c.Char, c.Brutal)
				text += applyBrutalEffects(lobby, c.Char, c.Target, c.Name, c.Key)
			}
		case 7:
			c.Record.recordChanges(lobby, c.Target, c.Snapshot)
			noteCharHitOnMonster(lobby, c.Target, c.Record)
		case 8: // a trait that corrodes what touches it (Corrosive Form): the weapon or the ammunition (item_state_2024.go)
			text += corrodeOnContact(lobby, c.Char, c.Target, c.Record.Weapon, c.Damage)
		}
		restore()
		c = next
		if combatResolutionPaused(lobby) {
			return text, nil
		}
	}
	return text, nil
}
