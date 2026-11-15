package game

// Hit Points (SRD 5.2.1, Rules Glossary and "Damage and Healing"): the pure rule of every change to a creature's Hit Points. The
// server reads a character's values under a row lock, asks this file what they become, and writes the answer in the same
// transaction (cmd/server/hit_points_2024.go); nothing else in the server decides what damage or healing does.
//   - Temporary Hit Points take damage first; what is left reduces Hit Points, which never go below 0.
//   - Dropping to 0 Hit Points: the creature is dying (Unconscious, making Death Saving Throws). If the damage left over after
//     reaching 0 equals or exceeds the Hit Point maximum, it dies outright (Instant Death).
//   - Damage at 0 Hit Points: one Death Saving Throw failure, two from a Critical Hit; damage equal to the Hit Point maximum or more
//     kills; the creature stops being Stable. Three failures: it dies.
//   - Healing never raises Hit Points above the maximum and does nothing for a dead creature. Regaining any Hit Points at 0 ends
//     dying: the Death Saving Throws reset and the creature isn't Stable any more (it is conscious).
//   - Concentration: taking damage calls for a Constitution save, DC 10 or half the damage taken, whichever is higher, up to 30.

// Vitals are the values of a creature that a change of Hit Points reads and writes.
type Vitals struct {
	HP, Max, Temp       int
	Successes, Failures int // Death Saving Throws
	Stable, Dead        bool
}

// Dying: at 0 Hit Points and not dead.
func (v Vitals) Dying() bool { return v.HP <= 0 && !v.Dead }

// DamageOutcome says what a hit did.
type DamageOutcome string

const (
	Damaged       DamageOutcome = "damaged"         // Hit Points (or only Temporary Hit Points) went down, above 0
	DroppedToZero DamageOutcome = "dropped_to_zero" // from above 0 to 0: dying
	InstantDeath  DamageOutcome = "instant_death"   // the damage left over at 0 (or taken at 0) is the Hit Point maximum or more
	FailedAtZero  DamageOutcome = "failed_at_zero"  // damage at 0 Hit Points: Death Saving Throw failures, still alive
	DiedAtZero    DamageOutcome = "died_at_zero"    // damage at 0 Hit Points brought the failures to three
	SameHitAtZero DamageOutcome = "same_hit"        // another damage type of a hit at 0 Hit Points already counted (no new failure)
	AlreadyDead   DamageOutcome = "already_dead"    // nothing changes
)

// DamageResult is what damage did to a creature's Vitals.
type DamageResult struct {
	Before, After Vitals
	Outcome       DamageOutcome
	Absorbed      int // taken by Temporary Hit Points
	ToHP          int // what was left after them
	FailuresAdded int // Death Saving Throw failures from damage at 0 Hit Points
	Overflow      int // the damage left over after reaching 0 Hit Points
}

// TakeDamage applies amount (already adjusted for resistance, vulnerability and immunity) to v. critical: the damage is from a
// Critical Hit (two failures at 0 Hit Points). sameHit: another damage type of one hit whose first part already counted.
func TakeDamage(v Vitals, amount int, critical, sameHit bool) DamageResult {
	if amount < 0 {
		amount = 0
	}
	r := DamageResult{Before: v, After: v}
	if v.Dead {
		r.Outcome = AlreadyDead
		return r
	}
	if v.Temp > 0 {
		r.Absorbed = min(v.Temp, amount)
		r.After.Temp = v.Temp - r.Absorbed
	}
	r.ToHP = amount - r.Absorbed
	if r.ToHP == 0 {
		r.Outcome = Damaged // only Temporary Hit Points (or nothing) lost; no Death Saving Throw failure
		return r
	}
	if v.HP <= 0 { // damage at 0 Hit Points
		r.After.HP, r.After.Stable = 0, false
		switch {
		case r.ToHP >= v.Max:
			r.After.Dead, r.Outcome = true, InstantDeath
		case sameHit:
			r.Outcome = SameHitAtZero
		default:
			r.FailuresAdded = 1
			if critical {
				r.FailuresAdded = 2
			}
			r.After.Failures = v.Failures + r.FailuresAdded
			r.Outcome = FailedAtZero
			if r.After.Failures >= 3 {
				r.After.Dead, r.Outcome = true, DiedAtZero
			}
		}
		return r
	}
	hp := v.HP - r.ToHP
	if hp > 0 {
		r.After.HP, r.Outcome = hp, Damaged
		return r
	}
	r.Overflow = -hp
	r.After.HP, r.After.Successes, r.After.Failures, r.After.Stable = 0, 0, 0, false
	r.Outcome = DroppedToZero
	if r.Overflow >= v.Max {
		r.After.Dead, r.Outcome = true, InstantDeath
	}
	return r
}

// FromAboveZero: the hit took a creature from above 0 Hit Points to 0 (dying or killed outright), the moment a feature that says
// "when you would drop to 0 Hit Points" (Death Ward) can act.
func (r DamageResult) FromAboveZero() bool {
	return r.Before.HP > 0 && (r.Outcome == DroppedToZero || r.Outcome == InstantDeath)
}

// Instead is the hit with the creature at hp instead (Death Ward, Relentless Endurance: "you drop to 1 Hit Point instead").
func (r DamageResult) Instead(hp int) DamageResult {
	r.After = r.Before.SetHP(hp)
	r.After.Temp = r.Before.Temp - r.Absorbed
	r.Outcome = Damaged
	return r
}

// Heal gives v up to amount Hit Points, never above ceiling (the maximum, or less: Preserve Life stops at half). A dead creature
// regains nothing. It returns the new Vitals and the Hit Points regained.
func Heal(v Vitals, amount, ceiling int) (Vitals, int) {
	if ceiling <= 0 || ceiling > v.Max {
		ceiling = v.Max
	}
	if v.Dead || amount <= 0 {
		return v, 0
	}
	from := max(v.HP, 0)
	to := min(from+amount, ceiling)
	if to <= from {
		return v, 0
	}
	return v.SetHP(to), to - from
}

// SetHP is v with its Hit Points set to hp (0 to the maximum): above 0 from 0 ends dying (the Death Saving Throws reset and the
// creature isn't Stable); from above 0 to 0 starts it (no saves yet). A dead creature keeps its Hit Points: only a spell that
// returns it to life changes them (Revive).
func (v Vitals) SetHP(hp int) Vitals {
	if v.Dead {
		return v
	}
	hp = max(0, min(hp, v.Max))
	if (v.HP <= 0) != (hp <= 0) {
		v.Successes, v.Failures, v.Stable = 0, 0, false
	}
	v.HP = hp
	return v
}

// Revive is a dead creature returned to life with hp Hit Points (Revivify, Raise Dead ...): no Death Saving Throws, not Stable, no
// Temporary Hit Points.
func (v Vitals) Revive(hp int) Vitals {
	v.Dead = false
	v.HP = 0
	v = v.SetHP(hp)
	v.Successes, v.Failures, v.Stable, v.Temp = 0, 0, false, 0
	return v
}

// Kill is the creature dead at 0 Hit Points (Power Word Kill, Exhaustion level 6, Divine Word).
func (v Vitals) Kill() Vitals {
	v.HP, v.Dead, v.Stable = 0, true, false
	return v
}

// Woke: Hit Points went from 0 to above 0 (the creature regains consciousness).
func Woke(before, after Vitals) bool { return before.HP <= 0 && after.HP > 0 && !after.Dead }

// DeathSaveOutcome says what a Death Saving Throw did.
type DeathSaveOutcome string

const (
	DeathSaveRegained DeathSaveOutcome = "regained" // a natural 20: 1 Hit Point, conscious
	DeathSaveSuccess  DeathSaveOutcome = "success"
	DeathSaveStable   DeathSaveOutcome = "stable" // the third success: Stable, both counts reset
	DeathSaveFailure  DeathSaveOutcome = "failure"
	DeathSaveDied     DeathSaveOutcome = "died" // the third failure
	DeathSaveNone     DeathSaveOutcome = "none" // not dying: nothing to record
)

// DeathSave records a Death Saving Throw on a dying creature (SRD 5.2.1): a natural 20 regains 1 Hit Point; a natural 1 is two
// failures; three successes make it Stable (both counts reset to zero); three failures kill it. natural is the d20 as the save
// counts it (Defy Death and the like already applied); success is the save's result against DC 10.
func (v Vitals) DeathSave(natural int, success bool) (Vitals, DeathSaveOutcome) {
	if !v.Dying() {
		return v, DeathSaveNone
	}
	switch {
	case natural == 20:
		return v.SetHP(1), DeathSaveRegained
	case natural == 1 || !success:
		v.Failures += 1
		if natural == 1 {
			v.Failures++
		}
		if v.Failures >= 3 {
			v.Dead, v.Stable = true, false
			return v, DeathSaveDied
		}
		return v, DeathSaveFailure
	}
	v.Successes++
	if v.Successes >= 3 {
		v.Successes, v.Failures, v.Stable = 0, 0, true
		return v, DeathSaveStable
	}
	return v, DeathSaveSuccess
}

// ConcentrationDC is the DC of the Constitution save that damage calls for: 10 or half the damage taken, whichever is higher, up
// to 30. damageTaken is the damage after resistance, vulnerability and immunity.
func ConcentrationDC(damageTaken int) int {
	return min(max(10, damageTaken/2), 30)
}
