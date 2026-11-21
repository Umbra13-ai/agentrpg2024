package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// A stat-block trait that acts when the monster dies ("The magmin explodes when it dies. Dexterity Saving Throw: DC 11, each
// creature in a 10-foot Emanation originating from the magmin. Failure: 7 (2d6) Fire damage. Success: Half damage."; SRD 5.2.1
// Death Burst of the Magmin and the Dust, Ice, Magma and Steam Mephits; the Balor's Death Throes).
//
// The death is decided by the monster Hit Point owner (changeMonsterVitalsE): in the same transaction it records the pending
// trigger (recordDeathTriggerIn, table pending_death_triggers), so a death that happened is never lost. The trigger is resolved
// after the request that killed the monster has finished (withProtections calls drainDeathTriggers) and at the start of every
// turn (the backstop): never inside the damage transaction, whose caller may still hold the locks the burst's own damage needs.
// Each pending row is claimed by one DELETE, so it is resolved by one drain (at most once: a crash in the middle of the
// resolution loses that burst; recorded in docs/MONSTER_SYSTEM.md).
//
// The effect is the save owner's (parseSaveEffect, applySaveEffect) on every creature in the Emanation around the monster's token
// on the battle map: characters, companions and the other monsters (a burst can kill another monster and set off its own). Without
// a battle map or without the monster's token, nothing is rolled and the GM is told to resolve it. The result goes to the
// campaign's log (its GM-only parts to the GM's notices, as every log line) and, whole, to the GM's notices.
var deathTriggerRe = regexp.MustCompile(`(?i)\b(?:explodes|bursts) when it dies\b`)

// deathTriggerOf: the trait of a stat block that acts on its death, if any.
func deathTriggerOf(traits []srdMonsterEntry) (srdMonsterEntry, bool) {
	for _, t := range traits {
		if deathTriggerRe.MatchString(t.Text) {
			if _, ok := parseSaveEffect(t.Text); ok {
				return t, true
			}
		}
	}
	return srdMonsterEntry{}, false
}

// deathTriggersPending: a request recorded a trigger that no drain has resolved yet (a cheap check before the drain's query).
var deathTriggersPending atomic.Bool

// recordDeathTriggerIn records the death of a monster whose form has a death trigger, in the transaction of the Hit Point change
// that killed it. A failed catalog read is the change's error (it rolls back), never a silent skip.
func recordDeathTriggerIn(tx *sql.Tx, lobby, id int, name, key string) error {
	if key == "" {
		return nil
	}
	traits, err := catalogTraits(tx, key)
	if err != nil {
		return err
	}
	trait, ok := deathTriggerOf(traits)
	if !ok {
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO pending_death_triggers (lobby_id, monster_id, name, trait_name, trait_text) VALUES ($1,$2,$3,$4,$5)`,
		lobby, id, name, stripUsage(trait.Name), trait.Text); err != nil {
		return err
	}
	deathTriggersPending.Store(true)
	return nil
}

// drainDeathTriggers resolves every pending trigger (of every campaign, oldest first), including those its own bursts set off.
// It returns the lines it wrote, for a caller that shows them (the turn change's log).
func drainDeathTriggers() []string {
	if db == nil {
		return nil
	}
	deathTriggersPending.Store(false)
	var lines []string
	for n := 0; n < 200; n++ { // each row is one monster's death; a chain ends when no living monster is left in reach
		var lobby, id int
		var name, traitName, text string
		err := db.QueryRow(`DELETE FROM pending_death_triggers WHERE id = (SELECT id FROM pending_death_triggers ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING lobby_id, monster_id, name, trait_name, trait_text`).Scan(&lobby, &id, &name, &traitName, &text)
		if err != nil {
			if err != sql.ErrNoRows {
				logDBProblem("death trigger claim", err)
			}
			return lines
		}
		line := resolveDeathTrigger(lobby, id, name, traitName, text)
		lines = append(lines, line)
	}
	return lines
}

// resolveDeathTrigger rolls the trait's saving throw for every creature in its Emanation and records what happened.
func resolveDeathTrigger(lobby, id int, name, traitName, text string) string {
	e, _ := parseSaveEffect(text)
	e.SourceID = id
	e.FromUndead = monsterTypeOf(lobby, id) == "undead"
	source := name + "'s " + traitName
	var line string
	switch _, hasMap := activeEncounterMap(lobby); {
	case e.Emanation <= 0:
		line = fmt.Sprintf("%s dies: its %s acts (%s). The GM resolves it: its area is not an Emanation the server can find.", name, traitName, text)
	case !hasMap || !monsterOnMap(lobby, id):
		line = fmt.Sprintf("%s dies: its %s acts. Nothing was rolled: there is no battle map with its token to find the creatures within %d feet. [GM: resolve it: %s]", name, traitName, e.Emanation, text)
	default:
		targets := append(charactersInEmanation(lobby, id, e.Emanation), creaturesWithin(lobby, id, e.Emanation, true, false)...)
		if len(targets) == 0 {
			line = fmt.Sprintf("%s dies: its %s acts; no creature is within %d feet.", name, traitName, e.Emanation)
		} else {
			rolled, _ := applySaveEffect(lobby, source, e, targets, true)
			line = fmt.Sprintf("%s dies: its %s acts. %s", name, traitName, strings.Join(rolled, " "))
			if e.Extra != "" {
				line += " [GM, not applied automatically: " + e.Extra + "]"
			}
		}
	}
	recordGMAction(db, lobby, "monster_trait", fmt.Sprintf("%s: %s", name, traitName), line)
	addGMNotice(lobby, line)
	return line
}

// monsterOnMap: the monster has a token on the campaign's battle map.
func monsterOnMap(lobby, id int) bool {
	m, ok := activeEncounterMap(lobby)
	if !ok {
		return false
	}
	_, ok = m.token(id)
	return ok
}
