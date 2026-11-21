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
	// what the hit's riders created (conditions, combat effects), so a Shield or Protection that turns the hit into a miss takes them back
	RiderRecords, RiderEffects []int
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
		undoDamage(charID, f.HPBefore, f.TempBefore, f.DeadBefore, true) // the Hit Point owner (hit_points_2024.go)
		if f.ConcBefore != "" && last.ConcAfter == "" {
			db.Exec(`UPDATE characters SET concentrating_on = $1 WHERE id = $2`, f.ConcBefore, charID)
			out += " (Your Concentration on " + f.ConcBefore + " comes back: no damage, no save.)"
		}
	} else if restore > 0 {
		undoDamage(charID, restore, 0, hits[0].DeadBefore, false) // the Hit Point owner (hit_points_2024.go)
	}
	if len(undone) > 0 {
		hp, _, _, _ := characterVitals(charID)
		out += fmt.Sprintf(" Now a miss: %s; its damage is undone (%d Hit Points back; you have %d).", strings.Join(undone, ", "), restore, hp)
		var gone []monsterHit
		for _, h := range hits {
			if !(h.Auto || h.Nat20 || h.Total >= h.AC+5) {
				gone = append(gone, h)
			}
		}
		out += takeBackRiders(lobby, charID, gone)
	}
	if len(stand) > 0 {
		out += " Still a hit: " + strings.Join(stand, ", ") + "."
	}
	return out
}

// pruneConcentrationConditions drops the conditions a Concentration spell gave (Hold Monster's Paralyzed) once its caster no longer
// concentrates on it (F56: they stayed until a save or the duration ended them).
func pruneConcentrationConditions(lobbyID, targetID int) {
	where, args := `target_id = $1 AND notes = 'concentration' AND COALESCE(created_by,0) > 0`, []interface{}{targetID}
	if targetID < 0 {
		where, args = where+` AND lobby_id = $2`, append(args, lobbyID)
	}
	var drop []conditionRecord
	for _, r := range findConditionRecords(where, args...) {
		if !concentrationHolds(r.CreatedBy, r.Source) {
			drop = append(drop, r)
		}
	}
	endConditionRecords(drop) // the condition owner
}

// riderIDsOf lists the condition records and combat effects a creature has now (for finding what a hit's riders created).
func riderIDsOf(lobbyID, id int) (records, effects []int) {
	for _, rec := range loadConditionRecords(lobbyID, id) {
		records = append(records, rec.ID)
	}
	for _, e := range combatEffectsOn(lobbyID, id) {
		effects = append(effects, e.ID)
	}
	return
}

// newIDs: the ids in after that are not in before.
func newIDs(before, after []int) []int {
	had := map[int]bool{}
	for _, b := range before {
		had[b] = true
	}
	var out []int
	for _, a := range after {
		if !had[a] {
			out = append(out, a)
		}
	}
	return out
}

// addRidersToLastHit records on the character's latest hit this turn what its riders created.
func addRidersToLastHit(charID int, records, effects []int) {
	if len(records)+len(effects) == 0 {
		return
	}
	v, ok := monsterHitsOn.Load(charID)
	if !ok {
		return
	}
	th := v.(turnHits)
	if len(th.Hits) == 0 {
		return
	}
	last := &th.Hits[len(th.Hits)-1]
	last.RiderRecords = append(last.RiderRecords, records...)
	last.RiderEffects = append(last.RiderEffects, effects...)
	monsterHitsOn.Store(charID, th)
}

// takeBackRiders removes what the riders of the hits that now miss created: by id, only those records and effects.
func takeBackRiders(lobbyID, charID int, hits []monsterHit) string {
	mine := map[int]bool{}
	var effs []int
	for _, h := range hits {
		for _, id := range h.RiderRecords {
			mine[id] = true
		}
		effs = append(effs, h.RiderEffects...)
	}
	if len(mine)+len(effs) == 0 {
		return ""
	}
	var names []string
	for _, rec := range removeConditionsWhere(lobbyID, charID, func(r conditionRecord) bool { return mine[r.ID] }) {
		names = append(names, rec.Condition)
	}
	for _, id := range effs {
		db.Exec(`DELETE FROM combat_effects WHERE id = $1 AND lobby_id = $2`, id, lobbyID)
	}
	if len(names) == 0 && len(effs) == 0 {
		return ""
	}
	return " What the hit gave is removed: " + strings.Join(append(names, map[bool]string{true: "a combat effect", false: ""}[len(effs) > 0]), ", ") + "."
}
