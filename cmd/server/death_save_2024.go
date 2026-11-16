package main

import (
	"fmt"

	"github.com/agentrpg/agentrpg/game"
)

// deathSaveTurnKey: the per-turn use that keeps a Death Saving Throw to once a turn (cleared by resetTurnFlags).
const deathSaveTurnKey = "death_save_turn"

// rollDeathSave is the one Death Saving Throw of a character's turn (SRD 5.2.1: "Whenever you start your turn with 0 Hit Points,
// you must make a Death Saving Throw"; it takes no action, PHB 2024). It is rolled once a turn: the turn-start hook rolls it for a
// dying character in a fight (turn_advance_2024.go); a second roll in the same turn is refused (already: true).
func rollDeathSave(charID int) (text string, already bool) {
	if err := useOnce(charID, deathSaveTurnKey, "already rolled"); err != nil {
		if _, refused := refusedSpend(err); refused {
			return "Cannot roll another Death Saving Throw: this turn's was already made (once a turn, at its start).", true
		}
		return fmt.Sprintf("Cannot roll the Death Saving Throw now (%s); try again.", notFoundOrDBError(err, "no such character")), false
	}
	// a Death Saving Throw: the one saving throw function (save_2024.go) rolls it with everything that applies to a save
	// (Foresight, Beacon of Hope, Defy Death, Bless, Aura of Protection, Exhaustion, Halfling Luck); the natural roll
	// decides what the SRD gives a 20 or a 1
	ds := (&saveCtx{saver: charID, ability: game.DeathSave}).roll()
	if championDefyDeath(charID) && ds.Natural >= 18 && ds.Natural < 20 { // Champion 18, Defy Death: 18-20 counts as a 20
		ds = ds.Replace(20, fmt.Sprintf("Defy Death: %d counts as a 20", ds.Natural))
	}
	if ds.Natural <= 9 && periaptActive(charID) { // Periapt of Wound Closure: a 9 or lower becomes a 10 (F142)
		ds = ds.Replace(10, fmt.Sprintf("Periapt of Wound Closure: %d becomes 10", ds.Natural))
	}
	roll := ds.ForSaver("Death saving throw")

	// the result is written by the Hit Point owner with the row locked (hit_points_2024.go): a hit at 0 Hit Points at the same
	// moment still counts. F210: "The number of both is reset to zero when you ... become Stable" (SRD 5.2.1).
	before, after, out, err := recordDeathSave(charID, ds.Natural, ds.Success)
	if err != nil {
		return fmt.Sprintf("%s. The result couldn't be recorded (%s); try again.", roll, notFoundOrDBError(err, "no such character")), false
	}
	nat1 := map[bool]string{true: "Natural 1 (2 failures)!", false: "Failure!"}[ds.Natural == 1]
	switch out {
	case game.DeathSaveRegained:
		return fmt.Sprintf("%s. Natural 20! You regain consciousness with 1 HP!", roll), false
	case game.DeathSaveDied:
		return fmt.Sprintf("%s. %s Total: %d failures. YOU HAVE DIED.", roll, nat1, after.Failures), false
	case game.DeathSaveFailure:
		return fmt.Sprintf("%s. %s Total: %d successes, %d failures.", roll, nat1, after.Successes, after.Failures), false
	case game.DeathSaveStable:
		return fmt.Sprintf("%s. Success! Total: %d successes. You are STABLE.", roll, before.Successes+1), false
	case game.DeathSaveSuccess:
		return fmt.Sprintf("%s. Success! Total: %d successes, %d failures.", roll, after.Successes, after.Failures), false
	}
	return fmt.Sprintf("%s. You aren't dying (at 0 Hit Points and alive), so nothing is recorded.", roll), false
}
