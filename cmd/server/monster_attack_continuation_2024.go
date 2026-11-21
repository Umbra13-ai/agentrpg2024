package main

// Multiattack rolls, damage components and riders have explicit boundaries. A
// continuation retains rolls already made and starts the next attack only after
// the earlier hit's unresolved Concentration branch has been settled.
import (
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"strings"
)

type savedMonsterAttack struct {
	Action       monsterActionRow
	Alternatives []damageAlt // Action.Alts is deliberately absent from its public JSON adapter
}
type monsterAttackCursor struct {
	Attacker, Target, Next, Hits, Damage int
	AttackerName                         string
	Apply                                bool
	Chosen                               []savedMonsterAttack
	Actions                              []monsterActionRow
}
type monsterHitCursor struct {
	Attacker, Target, Next, Stage int
	AttackerName                  string
	Apply, Melee                  bool
	StopDamageWhenDown            bool // preserve the companion adapter's existing damage-component contract
	Action                        monsterActionRow
	Alternatives                  []damageAlt
	Roll                          game.AttackRoll
	Outcome                       game.AttackOutcome
	Parts                         []hitDamagePart
	Snapshot                      hitSnapshot
	HPBefore                      int
	Dealt                         map[string]int // what each damage type dealt (a typed Hit Point maximum drain, hitAfterDamage)
	Line                          string
}

func init() {
	combatResumeHandlers["monster-attack-sequence"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var c monsterAttackCursor
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		if !monsterInFight(lobby, c.Target) || (c.Attacker != 0 && !monsterInFight(lobby, c.Attacker)) {
			return nil, fmt.Errorf("saved attack no longer belongs to this fight")
		}
		text, info, err := runMonsterAttackSequence(lobby, c)
		recordGMAction(db, lobby, "monster_action", text, text)
		return info, err
	}
	combatResumeHandlers["monster-hit"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var c monsterHitCursor
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		if !monsterInFight(lobby, c.Target) {
			return nil, fmt.Errorf("saved hit target is no longer in this fight")
		}
		text, err := runMonsterHit(lobby, &c)
		recordGMAction(db, lobby, "monster_action", text, text)
		return map[string]interface{}{"result": text}, err
	}
}
func runMonsterAttackSequence(lobby int, c monsterAttackCursor) (string, map[string]interface{}, error) {
	name := creatureName(lobby, c.Target)
	var lines []string
	reply := func(err error) (string, map[string]interface{}, error) {
		text := fmt.Sprintf("%s attacks %s. %s.", c.AttackerName, name, strings.Join(lines, "; "))
		for _, m := range c.Actions {
			if strings.EqualFold(stripUsage(m.Name), "Multiattack") && len(c.Chosen) > 0 {
				if extra := multiattackExtraText(m.Desc); extra != "" {
					text += " [GM, not automated: " + extra + "]"
				}
			}
		}
		text += " [" + monsterTargetReactionNote(lobby, c.Target) + "]"
		return text, map[string]interface{}{"attacks": len(c.Chosen), "hits": c.Hits, "damage": c.Damage, "applied": c.Apply, "target": name, "target_id": c.Target, "made": true, "combat_paused": combatResolutionPaused(lobby)}, err
	}
	for i := c.Next; i < len(c.Chosen); i++ {
		if !monsterAlive(lobby, c.Target) {
			lines = append(lines, name+" is already down: no further attack")
			break
		}
		a := c.Chosen[i].Action
		a.Alts = c.Chosen[i].Alternatives
		melee := meleeRe.MatchString(a.Desc) && !rangedOnlyRe.MatchString(a.Desc)
		withinFive, longRange, refusal := monsterAttackRange(lobby, c.Attacker, c.Target, a.Desc, a.Name, name, melee)
		if refusal != "" {
			lines = append(lines, refusal)
			continue
		}
		atk := attackCtx{lobby: lobby, attacker: c.Attacker, target: c.Target, name: a.Name, bonus: a.AttackBonus, melee: melee, weapon: true, longRange: longRange, within5: &withinFive, statBlockAttack: a.Desc, autoHit: autoHitRe.MatchString(a.Desc)}
		rolled, out, note := atk.roll()
		if !out.Hit {
			lines = append(lines, rolled.Line(a.Name, out)+note+hitOrMissNote(a.Desc, false))
			continue
		}
		if len(a.Alts) > 0 {
			a = applyDamageAlternatives(a, monsterHitCondition(lobby, c.Attacker, true, c.Target, rolled.HasAdvantage()))
		}
		chargeNote := ""
		a.Desc, chargeNote = hitChargeDesc(lobby, c.Attacker, c.Target, a.Desc) // a charge's effect for this hit (creature_movement_2024.go)
		note += chargeNote
		dmg := rollStatBlockDamage(a.DamageDice, a.FlatDamage, out.Critical)
		if dx, _ := enlargeHit(lobby, c.Attacker, dmg); dx != 0 {
			dmg += dx
		}
		hit := monsterHitCursor{Attacker: c.Attacker, AttackerName: c.AttackerName, Target: c.Target, Apply: c.Apply, Melee: melee, Action: a, Alternatives: a.Alts, Roll: rolled, Outcome: out, Parts: []hitDamagePart{{Amount: max(dmg, 0), Type: a.DamageType}}, Snapshot: snapshotTarget(lobby, c.Target), HPBefore: monsterHPNow(lobby, c.Target), Line: rolled.Line(a.Name, out) + note}
		for _, extra := range attackExtraDamage(a.Desc) {
			if extra.IfAdvantage && !rolled.HasAdvantage() {
				continue
			}
			hit.Parts = append(hit.Parts, hitDamagePart{Amount: extra.roll(out.Critical), Type: extra.Type})
		}
		c.Hits++
		for _, p := range hit.Parts {
			c.Damage += p.Amount
		}
		next := c
		next.Next = i + 1
		restore, err := armCombatFrame(lobby, "monster-attack-sequence", next)
		if err != nil {
			return reply(err)
		}
		line, err := runMonsterHit(lobby, &hit)
		restore()
		lines = append(lines, line)
		if err != nil || combatResolutionPaused(lobby) {
			pendingDamage := 0
			if hit.Stage == 0 {
				for _, p := range hit.Parts[hit.Next:] {
					pendingDamage += p.Amount
				}
			}
			c.Damage -= pendingDamage
			text, info, runErr := reply(err)
			info["attacks_planned"] = len(c.Chosen)
			info["attacks"] = i + 1
			info["attacks_pending"] = len(c.Chosen) - i - 1
			info["damage_pending"] = pendingDamage
			return text, info, runErr
		}
	}
	return reply(nil)
}
func runMonsterHit(lobby int, c *monsterHitCursor) (string, error) {
	c.Action.Alts = c.Alternatives
	line := c.Line
	if line == "" {
		line = creatureName(lobby, c.Target)
	}
	if c.Stage == 0 {
		for i := c.Next; i < len(c.Parts); i++ {
			if i > 0 && c.StopDamageWhenDown && !monsterAlive(lobby, c.Target) {
				break
			}
			p := c.Parts[i]
			if !c.Apply {
				line += fmt.Sprintf(" %s %d %s damage (not applied)", map[bool]string{true: ",", false: "plus"}[i == 0], p.Amount, p.Type)
				continue
			}
			next := *c
			next.Next = i + 1
			next.Line = ""
			restore, err := armCombatFrame(lobby, "monster-hit", next)
			if err != nil {
				return line, err
			}
			h, _ := hurtMonster(lobby, c.Target, p.Amount, strings.ToLower(p.Type), c.Outcome.Critical)
			line += ", " + h.Text
			restore()
			if c.Dealt == nil {
				c.Dealt = map[string]int{}
			}
			c.Dealt[strings.ToLower(p.Type)] += h.Dealt
			c.Next = i + 1
			if combatResolutionPaused(lobby) {
				return line, nil
			}
		}
		line += gmJudgedAlternatives(c.Action)
		if rider := unappliedHitRider(c.Action.Desc); rider != "" {
			line += " [GM, not applied automatically: " + rider + "]"
		}
		line += hitOrMissNote(c.Action.Desc, true)
		c.Stage = 1
	}
	for c.Stage < 4 {
		next := *c
		next.Stage++
		next.Line = ""
		restore, err := armCombatFrame(lobby, "monster-hit", next)
		if err != nil {
			return line, err
		}
		if c.Apply {
			switch c.Stage {
			case 1:
				if monsterAlive(lobby, c.Target) {
					// the same riders the direct route enforces (monster_attacks.go): the conditions under their
					// Advantage gate, the attach under its cover gates, then the recurring damage. The stage 0
					// residual (unappliedHitRider) already strips what these owners enforce; before they ran here,
					// an attach onset on this route was neither noted nor enforced - silently dropped.
					if e, ok := parseHitRider(c.Action.Desc); ok {
						e.SourceID = c.Attacker
						if !(e.CondIfAdvantage && !c.Roll.HasAdvantage()) {
							if given := e.giveConditions(lobby, c.Target, c.AttackerName+"'s "+stripUsage(c.Action.Name)); len(given) > 0 {
								line += " [" + strings.Join(given, "; ") + "]"
							}
						}
					}
					line += hitAfterDamage(lobby, c.Target, c.Action.Desc, c.AttackerName+"'s "+stripUsage(c.Action.Name), c.HPBefore-monsterHPNow(lobby, c.Target), c.Dealt) // Life Drain, burning (hit_after_damage_2024.go)
					coverOK := true                                                                                                                                           // the cover clause the conditions and suffocating hang on fired (its gates held)
					if e, ok := parseHitRider(c.Action.Desc); ok {
						_, gated := e.conditionsFor(lobby, c.Target)
						coverOK = gated == "" && !(e.CondIfAdvantage && !c.Roll.HasAdvantage())
					}
					if attached := addAttach(lobby, c.Attacker, c.Target, c.Action.Desc, c.AttackerName+"'s "+stripUsage(c.Action.Name), coverOK); attached != "" {
						line += " [" + attached + "]"
					}
					if rec, gmNote := addRecurringDamage(lobby, c.Attacker, c.Target, c.Action.Desc, c.AttackerName); rec != "" {
						line += " [" + rec + "]"
					} else if gmNote != "" {
						line += " [GM, not applied automatically: " + gmNote + "]"
					}
				}
			case 2:
				if monsterAlive(lobby, c.Target) {
					if e, ok := parseSaveEffect(c.Action.Desc); ok {
						e.SourceID = c.Attacker
						if result, _ := applySaveEffect(lobby, c.AttackerName+"'s "+stripUsage(c.Action.Name), e, []int{c.Target}, true); len(result) > 0 {
							line += " [" + strings.Join(result, " ") + "]"
						}
					}
				}
			case 3:
				h := charHitOnMonster{AttackerName: c.AttackerName, Total: c.Outcome.Total, AC: c.Roll.AC, Nat20: c.Outcome.Natural == 20, Melee: c.Melee, DamageType: c.Action.DamageType, Weapon: c.Action.Name, HPBefore: c.HPBefore, HPAfter: monsterHPNow(lobby, c.Target), Parts: c.Parts, Crit: c.Outcome.Critical, AttackerID: c.Attacker, Desc: c.Action.Desc}
				h.recordChanges(lobby, c.Target, c.Snapshot)
				noteCharHitOnMonster(lobby, c.Target, h)
			}
		}
		restore()
		*c = next
		if combatResolutionPaused(lobby) {
			return line, nil
		}
	}
	return line, nil
}
