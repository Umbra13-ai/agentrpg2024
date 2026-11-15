package main

// Resources that are spent "when you hit" (Divine Smite and Paladin's Smite, Eldritch Smite). The attack code rolls the damage before
// the server compares the roll with the monster's AC, so these are recorded as pending and given back when the attack misses
// (with the Half Cover of Smite of Protection), and kept when it hits.

import (
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
	pactInSlots := p.EldritchLevel > 0 && pactSlotColumn(charID) == "spell_slots_used" // F132
	changeSpent(charID, func(s *spent) error {                                         // everything given back in one locked step (resources_2024.go)
		if p.SmiteSlot > 0 {
			regainSlotIn(s.Slots, p.SmiteSlot)
		}
		if p.FreeSmite {
			s.regain("paladins_smite", 1)
		}
		if p.EldritchLevel > 0 {
			if pactInSlots {
				regainSlotIn(s.Slots, p.EldritchLevel)
			} else {
				regainSlotIn(s.Pact, p.EldritchLevel)
			}
			s.regain("eldritch_smite", 1)
		}
		return nil
	})
	if p.SmiteSlot > 0 || p.FreeSmite {
		db.Exec(`DELETE FROM combat_effects WHERE source_id = $1 AND kind = 'half_cover'`, charID) // Smite of Protection needs the smite
	}
}
