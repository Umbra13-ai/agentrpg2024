package main

// Taking a hit back (monster_reactions_2024.go: Parry, Shield, Redirect Attack turn a recorded hit into a miss).
//
// A hit is more than Hit Points: Temporary Hit Points, a Dead flag, a form that ended at 0, Sleep or charm that the damage ended, the
// conditions and Speed or Disadvantage markers the hit's own text gave, a combat effect such as Sap. The attacker records a snapshot of
// the target before the hit and after it (hitSnapshot, hitRecord); undoRecordedHit gives back exactly what the hit changed.
//
// Given back: the target's whole entry (Hit Points, Temporary Hit Points, Dead, form) when it is unchanged since the hit; the condition
// records and combat effects the hit created; the records the damage removed (Sleep, Hypnotic Pattern, Turn Undead, charm
// on damage); Regeneration's block when this hit set it. Kept: a "Hit or Miss:" effect (it happens whether the hit lands: it is not
// automated on this path, the reply gives it to the GM), the attack's use (a limited-use attack stays counted), anything another
// source changed after the hit. Not given back, said in the reply: effects of the hit this code does not know (the GM is told that
// anything else the hit did is theirs to take back), and a target whose entry changed since the hit gets only its Hit Points and Temporary
// Hit Points corrected by the difference.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// hitSnapshot is what a hit can change on its target, taken before the hit and again after it.
type hitSnapshot struct {
	Entry   map[string]interface{}
	Records map[int]conditionRecord
	Effects map[int]bool
	Regen   bool
}

func snapshotTarget(lobbyID, tid int) hitSnapshot {
	s := hitSnapshot{Records: map[int]conditionRecord{}, Effects: map[int]bool{}}
	if f, err := readFight(db, lobbyID); err == nil {
		if _, e := f.entry(tid); e != nil {
			s.Entry = copyEntry(e)
		}
	}
	for _, r := range loadConditionRecords(lobbyID, tid) {
		s.Records[r.ID] = r
	}
	for _, e := range combatEffectsOn(lobbyID, tid) {
		s.Effects[e.ID] = true
	}
	if tid < 0 {
		s.Regen = loadMonsterState(lobbyID, tid).RegenBlocked
	}
	return s
}

// hitRecord fills what the hit changed, from the snapshot taken before it.
func (h *charHitOnMonster) recordChanges(lobbyID, tid int, before hitSnapshot) {
	after := snapshotTarget(lobbyID, tid)
	h.EntryBefore, h.EntryAfter = before.Entry, after.Entry
	for id := range after.Records {
		h.RecordsAfter = append(h.RecordsAfter, id)
	}
	for id := range after.Records {
		if _, had := before.Records[id]; !had {
			h.NewRecords = append(h.NewRecords, id)
		}
	}
	for id, r := range before.Records {
		if _, still := after.Records[id]; !still {
			h.GoneRecords = append(h.GoneRecords, r)
		}
	}
	sort.Slice(h.GoneRecords, func(i, j int) bool { return h.GoneRecords[i].ID < h.GoneRecords[j].ID })
	sort.Ints(h.NewRecords)
	for id := range after.Effects {
		if !before.Effects[id] {
			h.NewEffects = append(h.NewEffects, id)
		}
	}
	h.RegenSetByHit = after.Regen && !before.Regen
	for _, id := range h.NewRecords {
		h.RiderNames = append(h.RiderNames, after.Records[id].Condition)
	}
}

// undoRecordedHit gives back what the recorded hit changed on the monster id, and says what it did and did not. It undoes only what
// this hit owns and only while that is still what the hit left: if anything else changed the target since (a heal, another hit, a
// condition that ended), nothing is guessed. ok is false then: the target is untouched, the reply says reconciliation is needed and the GM
// settles it. The caller consumes the recorded hit either way, so a second Reaction to the same hit finds nothing to undo and no Hit Point
// difference is ever applied twice.
func undoRecordedHit(lobbyID, id int, h charHitOnMonster) (after game.Vitals, said string, ok bool) {
	reconcile := func(why string) (game.Vitals, string, bool) {
		return monsterEntryVitals(lobbyID, id), " RECONCILIATION NEEDED: " + why + " Nothing of the hit was taken back by the server; the GM settles what the hit did (Hit Points, conditions, effects) before play continues.", false
	}
	if h.EntryBefore == nil || h.EntryAfter == nil {
		return reconcile("this hit was recorded without a snapshot of its target.")
	}
	var did []string
	restored := false
	_, err := changeFight(lobbyID, func(f *fight) error {
		_, e := f.entry(id)
		if e == nil {
			return errNotInFight
		}
		if !reflect.DeepEqual(copyEntry(e), h.EntryAfter) {
			return nil // changed since the hit
		}
		for k := range e {
			if _, had := h.EntryBefore[k]; !had {
				delete(e, k)
			}
		}
		for k, v := range h.EntryBefore {
			e[k] = v
		}
		restored = true
		return nil
	})
	if err != nil {
		return game.Vitals{}, " (the target is no longer in the fight)", false
	}
	if !restored {
		return reconcile("the target's Hit Points or state changed after this hit, so the hit's own changes cannot be separated from the later ones.")
	}
	after = monsterEntryVitals(lobbyID, id)
	did = append(did, "Hit Points, Temporary Hit Points and form restored")
	mine := map[int]bool{}
	for _, rid := range h.NewRecords {
		mine[rid] = true
	}
	// the records as they are now, apart from the hit's own: the same as the hit left them?
	untouched := true
	have := map[int]bool{}
	for _, r := range loadConditionRecords(lobbyID, id) {
		if !mine[r.ID] {
			have[r.ID] = true
		}
	}
	for _, rid := range h.RecordsAfter {
		if !mine[rid] && !have[rid] {
			untouched = false // something that was there after the hit is gone: another event ended it
		}
		delete(have, rid)
	}
	if len(have) > 0 {
		untouched = false // something was added after the hit
	}
	if len(mine) > 0 {
		gone := removeConditionsWhere(lobbyID, id, func(r conditionRecord) bool { return mine[r.ID] })
		var names []string
		for _, r := range gone {
			names = append(names, r.Condition)
		}
		if len(names) > 0 {
			sort.Strings(names)
			did = append(did, "removed what the hit gave: "+strings.Join(names, ", "))
		}
	}
	pending := ""
	if len(h.GoneRecords) > 0 {
		if untouched {
			for _, r := range h.GoneRecords {
				a := condAdd{Condition: r.Condition, Source: r.Source, Rounds: r.DurationRounds, EndSave: r.EndSave, Notes: r.Notes, CreatedBy: r.CreatedBy, SourceID: r.SourceID, Force: true}
				if r.Detail != "" {
					a.Condition = r.Condition + ":" + r.Detail
				}
				addCondition(lobbyID, id, a)
				did = append(did, "put back what the damage ended: "+r.Condition+" ("+r.Source+")")
			}
		} else {
			var names []string
			for _, r := range h.GoneRecords {
				names = append(names, r.Condition+" ("+r.Source+")")
			}
			pending = " RECONCILIATION NEEDED for " + strings.Join(names, ", ") + ": the damage ended it, but the target's conditions changed after the hit, so the server does not put it back."
		}
	}
	for _, eid := range h.NewEffects {
		db.Exec(`DELETE FROM combat_effects WHERE id = $1 AND lobby_id = $2`, eid, lobbyID)
	}
	if len(h.NewEffects) > 0 {
		did = append(did, "removed the combat effect(s) the hit gave")
	}
	if h.RegenSetByHit {
		s := loadMonsterState(lobbyID, id)
		s.RegenBlocked = false
		saveMonsterState(lobbyID, id, s)
		did = append(did, "Regeneration is not blocked by this hit")
	}
	return after, fmt.Sprintf(" Undone: %s. Kept: the attack's use and any \"Hit or Miss:\" effect (the GM's); anything else the hit did that this code does not track is the GM's to take back.%s", strings.Join(did, "; "), pending), true
}

// monsterEntryVitals reads the target's Vitals now.
func monsterEntryVitals(lobbyID, id int) game.Vitals {
	if f, err := readFight(db, lobbyID); err == nil {
		if _, e := f.entry(id); e != nil {
			return monsterVitals(e)
		}
	}
	return game.Vitals{}
}

// refundHitDamage gives back up to amount points of the recorded hit's damage (Deflect Missile: "reduces the damage it takes by ...").
// Damage empties Temporary Hit Points first and then Hit Points, so the refund fills Hit Points first and Temporary Hit Points with
// what is left, never past what the hit took. Only when the target is as the hit left it; otherwise nothing changes and ok is false
// (the caller says RECONCILIATION NEEDED). The recorded hit is updated to the new state, so a later correction of the whole hit
// (Parry) still restores the pre-hit entry exactly. Returns the points refunded and the target's vitals after.
func refundHitDamage(lobbyID, id int, key string, h charHitOnMonster, amount int) (refunded int, after game.Vitals, ok bool) {
	if h.EntryBefore == nil || h.EntryAfter == nil {
		return 0, monsterEntryVitals(lobbyID, id), false
	}
	bv, av := monsterVitals(h.EntryBefore), monsterVitals(h.EntryAfter)
	_, err := changeFight(lobbyID, func(f *fight) error {
		_, e := f.entry(id)
		if e == nil {
			return errNotInFight
		}
		if !reflect.DeepEqual(copyEntry(e), h.EntryAfter) {
			return nil
		}
		hpLost := max(bv.HP-av.HP, 0)
		tempLost := max(bv.Temp-av.Temp, 0)
		toHP := min(amount, hpLost)
		toTemp := min(amount-toHP, tempLost)
		if toHP+toTemp == 0 {
			ok = true
			return nil
		}
		v := av
		if toHP > 0 {
			v = v.SetHP(av.HP + toHP)
		}
		v.Temp = av.Temp + toTemp
		putMonsterVitals(e, monsterVitals(e), v)
		refunded, ok = toHP+toTemp, true
		return nil
	})
	if err != nil || !ok {
		return 0, monsterEntryVitals(lobbyID, id), false
	}
	after = monsterEntryVitals(lobbyID, id)
	if f, err := readFight(db, lobbyID); err == nil {
		if _, e := f.entry(id); e != nil {
			h.EntryAfter = copyEntry(e)
			h.HPAfter = after.HP
			lastHitOnMonster.Store(key, h)
		}
	}
	return refunded, after, true
}
