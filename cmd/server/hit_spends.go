package main

// Resources that are spent "when you hit" (Divine Smite and Paladin's Smite, Eldritch Smite). The attack code rolls the damage before
// the server compares the roll with the monster's AC, so these are recorded as pending and given back when the attack misses
// (with the Half Cover of Smite of Protection), and kept when it hits.

import (
	"encoding/json"
	"strconv"
	"sync"
)

type pendingSpend struct {
	SmiteSlot     int  // Divine Smite slot level (0 = none)
	FreeSmite     bool // Paladin's Smite (no slot)
	EldritchLevel int  // Eldritch Smite Pact Magic slot level (0 = none)
}

var hitSpends sync.Map // character id -> pendingSpend

func notePendingSpend(charID int, f func(*pendingSpend)) {
	p := pendingSpend{}
	if v, ok := hitSpends.Load(charID); ok {
		p = v.(pendingSpend)
	}
	f(&p)
	hitSpends.Store(charID, p)
}

// clearPendingSpends starts a new attack (and keeps what an attack that hit spent).
func clearPendingSpends(charID int) { hitSpends.Delete(charID) }

// refundMissedSpends gives back what a missed attack spent "on a hit".
func refundMissedSpends(charID int) {
	v, ok := hitSpends.LoadAndDelete(charID)
	if !ok {
		return
	}
	p := v.(pendingSpend)
	adjust := func(col, key string) {
		var raw []byte
		db.QueryRow(`SELECT COALESCE(`+col+`,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
		used := map[string]int{}
		json.Unmarshal(raw, &used)
		if used[key] > 0 {
			used[key]--
		}
		b, _ := json.Marshal(used)
		db.Exec(`UPDATE characters SET `+col+` = $1 WHERE id = $2`, string(b), charID)
	}
	if p.SmiteSlot > 0 {
		adjust("spell_slots_used", strconv.Itoa(p.SmiteSlot))
	}
	if p.FreeSmite {
		adjust("class_resources_used", "paladins_smite")
	}
	if p.SmiteSlot > 0 || p.FreeSmite {
		db.Exec(`DELETE FROM combat_effects WHERE source_id = $1 AND kind = 'half_cover'`, charID) // Smite of Protection needs the smite
	}
	if p.EldritchLevel > 0 {
		adjust("pact_slots_used", strconv.Itoa(p.EldritchLevel))
		adjust("class_resources_used", "eldritch_smite")
	}
}
