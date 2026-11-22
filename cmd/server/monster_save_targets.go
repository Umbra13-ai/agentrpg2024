package main

// A monster's save effect on another monster or an NPC in the fight (a charm, a breath weapon, a spell that a monster aims at a
// creature that is not a character). The owner of the rules is the same as for characters (applySaveEffect, monster_effects_2024.go):
// the save is saveCtx (Legendary Resistance through resist, Magic Resistance and the stat block's own save bonus), the damage is
// hurtMonster (resistance, immunity, absorption, Undead Fortitude), the conditions are addCondition. What differs from a character:
// no Exhaustion (reported to the GM), no items. Evasion is the monster's trait of that name.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// monsterTargetByName: the living monster of the fight a target names ("Goblin 2", or "#-3" by id), 0 when there is none.
func monsterTargetByName(lobbyID int, target string) int {
	target = strings.TrimSpace(target)
	if target == "" {
		return 0
	}
	for _, c := range combatEntries(lobbyID) {
		if m, _ := c["is_monster"].(bool); !m || asInt(c["hp"]) <= 0 {
			continue
		}
		id := asInt(c["id"])
		if strings.EqualFold(strings.TrimSpace(sprintAny(c["name"])), target) || target == fmt.Sprintf("#%d", id) {
			return id
		}
	}
	return 0
}

// monsterInFight: id names a monster of this fight.
func monsterInFight(lobbyID, id int) bool {
	if id >= 0 {
		return false
	}
	for _, c := range combatEntries(lobbyID) {
		if asInt(c["id"]) == id {
			return true
		}
	}
	return false
}

// applySaveEffectOnMonster rolls one monster's save against e and applies what follows (rolled are the damage amounts, rolled once for
// every target). failed says the save failed (for the GM's note); immune: the save succeeded against an effect that then grants immunity.
// monsterSaveCursor retains the already-rolled save and damage. Resuming never
// rolls them again or reads a later Constitution modifier for the original save.
type monsterSaveCursor struct {
	Source                string
	Effect                saveEffect
	Target, FailedBy      int
	Apply, Saved, Evasion bool
	Dropped               bool // dropsToZero: no damage, the target drops to 0 Hit Points
	Damage                []int
	Next                  int
	Line                  string
}

func prepareMonsterSaveEffect(lobby int, source string, e saveEffect, tid int, rolled map[int]int, apply bool) monsterSaveCursor {
	total := 0
	for i := range e.Damage {
		total += rolled[i]
	}
	sv := e.rollForTarget(lobby, tid, total, apply)
	c := monsterSaveCursor{Source: source, Effect: e, Target: tid, Apply: apply, Saved: sv.Success, FailedBy: saveFailureMargin(sv), Line: sv.Line(creatureName(lobby, tid))}
	c.Evasion = e.Ability == "dex" && e.HalfOnSuccess && monsterHasTrait(lobby, tid, "Evasion")
	c.Dropped = e.dropsToZero(lobby, tid, c.Saved)
	for i := range e.Damage {
		d := rolled[i]
		switch {
		case c.Dropped:
			d = 0
		case c.Saved && (c.Evasion || !e.HalfOnSuccess):
			d = 0
		case c.Saved, c.Evasion:
			d /= 2
		}
		c.Damage = append(c.Damage, d)
	}
	return c
}
func init() {
	combatResumeHandlers["monster-save-effect"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var c monsterSaveCursor
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		if !monsterInFight(lobby, c.Target) {
			return nil, fmt.Errorf("saved effect target is no longer in this fight")
		}
		line, _ := runMonsterSaveEffect(lobby, c)
		recordGMAction(db, lobby, "monster_effect", line, line)
		return map[string]interface{}{"result": line}, nil
	}
}
func applySaveEffectOnMonster(lobbyID int, source string, e saveEffect, tid int, rolled map[int]int, apply bool) (line string, failed, immune bool) {
	c := prepareMonsterSaveEffect(lobbyID, source, e, tid, rolled, apply)
	line, immune = runMonsterSaveEffect(lobbyID, c)
	return line, !c.Saved, immune
}
func runMonsterSaveEffect(lobby int, c monsterSaveCursor) (line string, immune bool) {
	var parts []string
	e := c.Effect
	if c.Dropped && c.Next == 0 {
		parts = append(parts, e.dropToZero(lobby, c.Target, c.Apply))
	}
	for i := c.Next; i < len(e.Damage); i++ {
		d := c.Damage[i]
		if d <= 0 {
			continue
		}
		if !c.Apply {
			parts = append(parts, fmt.Sprintf("%d %s damage (not applied)", d, e.Damage[i].Type))
			continue
		}
		next := c
		next.Next = i + 1
		next.Line = ""
		restore, err := armCombatFrame(lobby, "monster-save-effect", next)
		if err != nil {
			addGMNotice(lobby, "A save effect could not save its continuation; no further automatic effects were run.")
			return strings.Join(parts, "; "), false
		}
		h, _ := hurtMonster(lobby, c.Target, d, e.Damage[i].Type, false)
		parts = append(parts, h.Text)
		if drained := e.drainAfterDamage(lobby, c.Target, c.Saved, e.Damage[i].Type, h.Dealt); drained != "" {
			parts = append(parts, drained)
		}
		restore()
		if combatResolutionPaused(lobby) {
			return saveEffectLine(c.Line, creatureName(lobby, c.Target), parts), false
		}
	}
	if c.Evasion {
		parts = append(parts, "Evasion")
	}
	riders, immune := e.finishTarget(lobby, c.Target, c.Source, c.Apply, c.Saved, c.FailedBy)
	parts = append(parts, riders...)
	return saveEffectLine(c.Line, creatureName(lobby, c.Target), parts), immune
}
func saveEffectLine(prefix, name string, parts []string) string {
	if prefix == "" {
		prefix = name
	}
	if len(parts) > 0 {
		prefix += ": " + strings.Join(parts, "; ")
	}
	return prefix
}
