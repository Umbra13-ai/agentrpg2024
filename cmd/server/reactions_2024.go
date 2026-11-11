package main

// The Shield spell as a Reaction (SRD 5.2.1; F54, Nov 8, 2026). "Reaction, which you take when you are hit by an attack roll or
// targeted by the Magic Missile spell": until the start of your next turn you have a +5 bonus to AC, including against the triggering
// attack. Monster attacks resolve at once (gm/narrate monster_action), so the server remembers this turn's hits on each character;
// casting Shield ({"action": "cast", "spell_slug": "shield"}) spends the Reaction (not the action) and turns each of those hits that
// +5 AC makes miss into a miss: its damage is undone. Later attacks see the +5 (spellACAdjust) until the caster's next turn starts.
// Before this, Shield spent the action and changed nothing.

import (
	"fmt"
	"strings"
	"sync"
)

type monsterHit struct {
	Attacker, Attack      string
	Total, AC             int
	Auto                  bool // an automatic hit: no attack roll to beat
	Nat20                 bool // a natural 20 hits whatever the AC (F219: Shield turned it into a miss)
	Ranged                bool // a ranged or thrown attack (Gloves of Missile Snaring)
	Snared                bool // already reduced by Gloves of Missile Snaring
	HPBefore, TempBefore  int
	HPAfter, TempAfter    int
	DeadBefore            bool
	ConcBefore, ConcAfter string
}

type turnHits struct {
	Lobby  int
	Marker string
	Hits   []monsterHit
}

var monsterHitsOn sync.Map // character id -> turnHits (this turn's monster hits)

// characterVitals: Hit Points, Temporary Hit Points, dead, Concentration.
func characterVitals(charID int) (hp, temp int, dead bool, conc string) {
	db.QueryRow(`SELECT hp, COALESCE(temp_hp,0), COALESCE(is_dead,false), COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, charID).Scan(&hp, &temp, &dead, &conc)
	return
}

// noteMonsterHit remembers a monster's hit on a character (resolveMonsterAttack, after its damage).
func noteMonsterHit(lobby, charID int, h monsterHit) {
	marker := combatTurnMarker(lobby)
	if marker == "" {
		return
	}
	th := turnHits{Lobby: lobby, Marker: marker}
	if v, ok := monsterHitsOn.Load(charID); ok {
		if old := v.(turnHits); old.Lobby == lobby && old.Marker == marker {
			th = old
		}
	}
	th.Hits = append(th.Hits, h)
	monsterHitsOn.Store(charID, th)
}

// thisTurnHits: the monster hits on the character during the current turn.
func thisTurnHits(charID int) []monsterHit {
	v, ok := monsterHitsOn.Load(charID)
	if !ok {
		return nil
	}
	th := v.(turnHits)
	if th.Marker == "" || th.Marker != combatTurnMarker(th.Lobby) || th.Lobby != lobbyOfChar(charID) {
		return nil
	}
	return th.Hits
}

// shieldTriggerMissing: Shield can be cast only in reply to its trigger. "" when an attack hit the character this turn, or the
// description says Magic Missile targets it.
func shieldTriggerMissing(charID int, description string) string {
	if strings.Contains(strings.ToLower(description), "magic missile") || len(thisTurnHits(charID)) > 0 {
		return ""
	}
	return "Cannot cast Shield now: it is a Reaction when an attack roll hits you (or Magic Missile targets you: say so in the description), and no attack has hit you this turn. Nothing was spent."
}

// shieldReaction applies a cast Shield: +5 AC until the start of the caster's next turn, and the hits of this turn that now miss are
// undone.
func shieldReaction(charID int) string {
	lobby := lobbyOfChar(charID)
	addSpellEffect(lobby, charID, charID, "Shield", "shield_spell", "", 2, "", 0)
	out := " Shield: +5 bonus to AC until the start of your next turn."
	hits := thisTurnHits(charID)
	monsterHitsOn.Delete(charID)
	if len(hits) == 0 {
		return out
	}
	var undone, stand []string
	restore := 0
	for _, h := range hits {
		desc := fmt.Sprintf("%s's %s (%d against AC %d)", h.Attacker, h.Attack, h.Total, h.AC+5)
		if h.Auto || h.Nat20 || h.Total >= h.AC+5 {
			stand = append(stand, desc)
			continue
		}
		undone = append(undone, desc)
		restore += (h.HPBefore + h.TempBefore) - (h.HPAfter + h.TempAfter)
	}
	if len(undone) == len(hits) { // every hit misses now: back to before the first one
		f, last := hits[0], hits[len(hits)-1]
		db.Exec(`UPDATE characters SET hp = $1, temp_hp = $2, is_dead = $3 WHERE id = $4`, f.HPBefore, f.TempBefore, f.DeadBefore, charID)
		if f.ConcBefore != "" && last.ConcAfter == "" {
			db.Exec(`UPDATE characters SET concentrating_on = $1 WHERE id = $2`, f.ConcBefore, charID)
			out += " (Your Concentration on " + f.ConcBefore + " comes back: no damage, no save.)"
		}
	} else if restore > 0 {
		db.Exec(`UPDATE characters SET hp = LEAST(max_hp, hp + $1), is_dead = false WHERE id = $2`, restore, charID)
	}
	if len(undone) > 0 {
		hp, _, _, _ := characterVitals(charID)
		out += fmt.Sprintf(" Now a miss: %s; its damage is undone (%d Hit Points back; you have %d).", strings.Join(undone, ", "), restore, hp)
	}
	if len(stand) > 0 {
		out += " Still a hit: " + strings.Join(stand, ", ") + "."
	}
	return out
}

// pruneConcentrationConditions drops the conditions a Concentration spell gave (Hold Monster's Paralyzed) once its caster no longer
// concentrates on it (F56: they stayed until a save or the duration ended them).
func pruneConcentrationConditions(lobbyID, targetID int) {
	if db == nil {
		return
	}
	rows, err := db.Query(`SELECT id, created_by, source FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND notes = 'concentration' AND COALESCE(created_by,0) > 0`, lobbyID, targetID)
	if err != nil {
		return
	}
	type rec struct {
		id, caster int
		source     string
	}
	var drop []rec
	for rows.Next() {
		var r rec
		if rows.Scan(&r.id, &r.caster, &r.source) == nil && !concentrationHolds(r.caster, r.source) {
			drop = append(drop, r)
		}
	}
	rows.Close()
	for _, r := range drop {
		db.Exec(`DELETE FROM condition_records WHERE id = $1`, r.id)
	}
	if len(drop) > 0 && targetID > 0 {
		syncListFromRecords(targetID) // a character's list follows its records (Invisibility from a spell that ended)
	}
	if len(drop) > 0 && targetID > 0 {
		syncListFromRecords(targetID)
	}
}
