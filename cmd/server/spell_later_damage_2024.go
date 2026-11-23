package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// spell_later_damage_2024.go: a spell's damage that comes "at the end of the target's next turn" (SRD 5.2.1 Acid Arrow on a hit:
// "2d4 Acid damage at the end of its next turn"; Vitriolic Sphere on a failed save: "another 5d4 Acid damage at the end of its next
// turn"). The tester's run (Nov 23) found it announced and never dealt. A part of the spell data with timing end_of_next_turn is
// scheduled by scheduleLaterDamage when the spell landed on that target (a hit, a failed save), as a combat_effects row (kind
// later_damage) on the target, and dealt by laterDamageAtTurnEnd, a turn-end hook, through the Hit Point owners (damageCharacter,
// damageMonster: resistances, Concentration). A cast during the target's own turn waits for its next one. Outside a fight the server keeps
// no turns: the reply says the GM deals it. Go: TestPostgresSpellLaterDamageAtTheTargetsTurnEnd.

type laterDamage struct {
	Dice, Type, Spell string
	Wait              bool // cast during the target's own turn: this turn's end is not "its next turn"
}

// scheduleLaterDamage schedules the spell's later parts on a target it landed on (a hit or a failed save) and says so.
func scheduleLaterDamage(lobbyID, casterID, targetID int, spell SRDSpell, slotLevel int, landed bool) string {
	if !landed {
		return ""
	}
	var out []string
	for _, p := range spell.DamageParts {
		if p.Timing != "end_of_next_turn" {
			continue
		}
		dice, typ := p.diceAt(slotLevel), strings.ToLower(p.Type)
		inFight := false
		for _, e := range combatEntries(lobbyID) {
			if asInt(e["id"]) == targetID {
				inFight = true
			}
		}
		if !inFight {
			out = append(out, fmt.Sprintf(" Another %s %s damage comes at the end of its next turn: the GM deals it (the server keeps turns only in a fight).", dice, typ)) // gm-one-shot: dealt once, at a time the server doesn't keep outside a fight
			continue
		}
		detail, _ := json.Marshal(laterDamage{Dice: dice, Type: typ, Spell: spell.Name, Wait: currentTurnID(lobbyID) == targetID})
		db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,'later_damage',$4)`, lobbyID, targetID, casterID, string(detail))
		out = append(out, fmt.Sprintf(" Another %s %s damage at the end of its next turn (the server deals it then).", dice, typ))
	}
	return strings.Join(out, "")
}

// laterDamageAtTurnEnd deals the later damage scheduled on the creature whose turn ends (a turn-end hook, turn_advance_2024.go).
func laterDamageAtTurnEnd(lobbyID, id int) []string {
	if db == nil || id == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT id, text FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'later_damage' ORDER BY id`, lobbyID, id)
	if err != nil {
		return nil
	}
	type due struct {
		row int
		ld  laterDamage
	}
	var list []due
	for rows.Next() {
		var d due
		var raw string
		if rows.Scan(&d.row, &raw) == nil && json.Unmarshal([]byte(raw), &d.ld) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	var lines []string
	for _, d := range list {
		if d.ld.Wait { // its next turn is the one after this
			d.ld.Wait = false
			b, _ := json.Marshal(d.ld)
			db.Exec(`UPDATE combat_effects SET text = $2 WHERE id = $1`, d.row, string(b))
			continue
		}
		res, err := db.Exec(`DELETE FROM combat_effects WHERE id = $1`, d.row) // claimed before it is dealt: a retried turn end deals it once
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		dmg := game.RollDamage(d.ld.Dice, false)
		var text string
		if id > 0 {
			text = damageCharacter(id, dmg, d.ld.Type)
		} else {
			text, _ = damageMonster(lobbyID, id, dmg, d.ld.Type)
		}
		lines = append(lines, fmt.Sprintf("%s at the end of %s's turn: %d %s damage (%s). %s", d.ld.Spell, creatureName(lobbyID, id), dmg, d.ld.Type, d.ld.Dice, text))
	}
	return lines
}
