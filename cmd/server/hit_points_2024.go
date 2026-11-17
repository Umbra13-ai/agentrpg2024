package main

// The one owner of a character's Hit Points (audit AUD-06, findings C-3, C-4 and B-1). Every change to characters.hp (damage,
// healing, a drop to 0 and what stops it, death, a return to life, a set value, a change of the maximum) goes through a function
// in this file. Each reads the character's values with the row locked (SELECT ... FOR UPDATE), asks game.TakeDamage, game.Heal or
// a Vitals method what they become (game/hit_points.go: the rule itself) and writes the answer in the same transaction, so two
// changes at once are both counted. Before Nov 15 2026 about 55 statements in 26 files read Hit Points, computed in Go and wrote
// an absolute value without a lock: ten 1-point hits sent at once took 12 Hit Points to 10. Many skipped the rules that come
// with a change: Lay on Hands, Divine Spark and Land's Aid healed a dying character without resetting its Death Saving Throws (one
// died against the rules); the GM's area spells and falling damage skipped Temporary Hit Points, damage at 0 Hit Points, Instant
// Death and Concentration; the Concentration DC was half the damage before resistance.
//
// Work that reads or writes other rows (resistances, Warding Bond, the save that Relentless Rage rolls) is done outside the
// transaction: before it (what the damage is) or after it (what the change sets off). Inside, only the transaction is used
// (CLAUDE.md: never db inside a transaction).

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// vitalsColumns are the columns game.Vitals holds, in its order (readVitals, writeVitals).
const vitalsColumns = `hp, max_hp, COALESCE(temp_hp,0), COALESCE(death_save_successes,0), COALESCE(death_save_failures,0),
	COALESCE(is_stable,false), COALESCE(is_dead,false)`

// lockVitals reads a character's Hit Point values in tx with the row locked until the transaction ends.
func lockVitals(tx *sql.Tx, charID int) (game.Vitals, error) {
	var v game.Vitals
	err := tx.QueryRow(`SELECT `+vitalsColumns+` FROM characters WHERE id = $1 FOR UPDATE`, charID).
		Scan(&v.HP, &v.Max, &v.Temp, &v.Successes, &v.Failures, &v.Stable, &v.Dead)
	return v, err
}

// readVitals reads them without a lock (for a reply).
func readVitals(q rowQuerier, charID int) (game.Vitals, error) {
	var v game.Vitals
	err := q.QueryRow(`SELECT `+vitalsColumns+` FROM characters WHERE id = $1`, charID).
		Scan(&v.HP, &v.Max, &v.Temp, &v.Successes, &v.Failures, &v.Stable, &v.Dead)
	return v, err
}

func writeVitals(q execer, charID int, v game.Vitals) error {
	_, err := q.Exec(`UPDATE characters SET hp = $1, temp_hp = $2, death_save_successes = $3, death_save_failures = $4, is_stable = $5,
		is_dead = $6 WHERE id = $7`, v.HP, v.Temp, v.Successes, v.Failures, v.Stable, v.Dead, charID)
	return err
}

// changeVitals is the one transaction every change runs in: the row locked, change computes the new values from the current
// ones (and may use tx for a row of its own, as Death Ward does), and they are written when they differ.
func changeVitals(charID int, change func(tx *sql.Tx, v game.Vitals) (game.Vitals, error)) (before, after game.Vitals, err error) {
	tx, err := db.Begin()
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback()
	if before, err = lockVitals(tx, charID); err != nil {
		return before, before, err
	}
	if after, err = change(tx, before); err != nil {
		return before, before, err
	}
	if after != before {
		if err = writeVitals(tx, charID, after); err != nil {
			return before, before, err
		}
	}
	return before, after, tx.Commit()
}

// ---------------------------------------------------------------- damage

// hpPart is damage of one type.
type hpPart struct {
	Amount int
	Type   string
}

// hpHit is one source of damage to a character: one hit or one effect, in one or more damage types. It makes one Concentration
// save (its DC from the whole damage taken) and, at 0 Hit Points, one Death Saving Throw failure (two from a Critical Hit).
type hpHit struct {
	Parts    []hpPart
	Critical bool // a Critical Hit: two failures at 0 Hit Points
	Raw      bool // can't be reduced in any way (Overchannel): no resistance, immunity or reduction
	NoBond   bool // a Warding Bond's shared damage (not shared again)
	// Sent by a caller that applies one source in several requests (POST /characters/{id}/damage): the whole damage for the
	// Concentration DC, no save for the later parts, and no second failure at 0 Hit Points.
	ConcentrationDamage int
	SkipConcentration   bool
	SameHit             bool
}

// oneHit is damage of one type.
func oneHit(amount int, dtype string) hpHit {
	return hpHit{Parts: []hpPart{{amount, strings.ToLower(dtype)}}}
}

// hpDamage is what a hit did: the rule's result, the damage after resistances, and the reply (view) the damage request answers.
type hpDamage struct {
	game.DamageResult
	Name   string
	Dealt  int    // after resistance, immunity and reductions; before Temporary Hit Points
	Status string // damaged, unconscious, dead, INSTANT_DEATH, already_dead, death_ward, relentless_rage ...
	Down   bool   // the character went from above 0 Hit Points to 0 with this hit (dying or dead)
	view   map[string]interface{}
}

// View is the reply of POST /characters/{id}/damage.
func (d hpDamage) View() map[string]interface{} { return d.view }

// Sentence is the clause other replies show: "takes 7 fire damage (now 5 HP) [Concentration: ...]".
func (d hpDamage) Sentence(dtype string) string {
	text := fmt.Sprintf("takes %d %s damage (now %d HP)", d.Dealt, dtype, d.After.HP)
	if c, ok := d.view["concentration"].(string); ok {
		text += " [" + c + "]"
	}
	return text
}

// dropRescue is a feature that acts when a character drops to 0 Hit Points but isn't killed outright: it gives hp > 0 instead.
// failed is said when the feature tried and failed (Relentless Rage's save).
type dropRescue struct {
	status, flag, noteKey string
	try                   func(charID, hpBefore, damage, maxHP int) (hp int, note string, ok bool, failed string)
}

// dropRescues, in the order they are tried (Death Ward acts before them, inside the transaction, because it also stops Instant
// Death).
var dropRescues []dropRescue // set in init: its functions reach damageHP again (a fall, a Warding Bond)

func init() {
	dropRescues = []dropRescue{
		{"relentless_rage", "relentless_rage", "class_feature_note", func(id, hp, dmg, max int) (int, string, bool, string) {
			n, used, msg := checkRelentlessRage(id, hp, dmg, max)
			if used {
				return n, msg, true, ""
			}
			return 0, "", false, msg
		}},
		{"relentless_endurance", "relentless_endurance", "racial_feature_note", func(id, hp, dmg, max int) (int, string, bool, string) {
			n, used, msg := checkRelentlessEndurance(id, hp, dmg, max)
			return n, msg, used, ""
		}},
		{"gift_of_the_protectors", "", "", func(id, _, _, _ int) (int, string, bool, string) {
			note := giftOfTheProtectors(id)
			return 1, note, note != "", ""
		}},
	}
}

// damageHP deals a hit to a character: resistances and reductions, Temporary Hit Points, Hit Points, and what the SRD makes of
// the result (dying, Death Saving Throw failures, Instant Death, Death Ward, Relentless Rage and Endurance, Concentration).
// The error is the database's (sql.ErrNoRows: no such character).
func damageHP(charID int, hit hpHit) (hpDamage, error) {
	d := hpDamage{view: map[string]interface{}{}}
	if err := db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&d.Name); err != nil {
		return d, err
	}
	original, dealt := 0, 0
	var resisted []string
	for _, p := range hit.Parts {
		original += p.Amount
		dealt += d.reduce(charID, p, hit.Raw, &resisted)
	}
	d.Dealt = dealt
	d.view["original_damage"] = original
	d.view["damage_dealt"] = dealt
	if len(resisted) > 0 {
		d.view["resistances_applied"] = resisted
	}
	if dealt > 0 {
		cloakTakesDamage(charID) // the Cloak of Displacement stops until the start of the wearer's next turn (F113)
		if note := wakeOnDamage(lobbyOfChar(charID), charID, d.Name); note != "" {
			d.view["woke_up"] = note // Hypnotic Pattern, Sleep
		}
		if !hit.NoBond {
			if note := wardingBondShare(charID, dealt); note != "" { // its caster takes the same damage (buff_spells_2024.go)
				d.view["warding_bond"] = strings.TrimSpace(note)
			}
		}
	}

	ward, _ := charBuff(charID, "death_ward") // read here; spent inside the transaction, so two hits can't both use it
	var concentrating, wildShape string
	wardUsed := false
	_, _, err := changeVitals(charID, func(tx *sql.Tx, v game.Vitals) (game.Vitals, error) {
		if err := tx.QueryRow(`SELECT COALESCE(concentrating_on,''), COALESCE(wild_shape_form,'') FROM characters WHERE id = $1`, charID).
			Scan(&concentrating, &wildShape); err != nil {
			return v, err
		}
		d.DamageResult = game.TakeDamage(v, dealt, hit.Critical, hit.SameHit)
		if d.FromAboveZero() && ward.ID > 0 { // Death Ward: 1 Hit Point instead of 0, and the spell ends
			res, err := tx.Exec(`DELETE FROM spell_effects WHERE id = $1`, ward.ID)
			if err != nil {
				return v, err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				d.DamageResult, wardUsed = d.Instead(1), true
			}
		}
		return d.After, nil
	})
	if err != nil {
		return d, err
	}
	if d.Absorbed > 0 {
		d.view["temp_hp_absorbed"] = d.Absorbed
	}
	d.describe(wardUsed)
	if d.Outcome == game.DroppedToZero {
		d.rescue(charID)
	}
	d.Down = d.Before.HP > 0 && (d.After.HP <= 0 || d.After.Dead)
	if d.Down {
		d.afterDown(charID)
	}
	if d.Before.HP > 0 { // damage at 0 Hit Points: these ended when the character dropped
		if note := polymorphTempGone(charID); note != "" {
			d.view["polymorph_ended"] = note // the beast's Temporary Hit Points are gone (spells_more_2024.go)
		} else if wildShape != "" && !d.Down {
			if note := endWildShapeIfDown(charID); note != "" {
				d.view["wild_shape_ended"] = note
			}
		}
		if concentrating != "" && d.After.HP > 0 && !d.After.Dead && dealt > 0 && !hit.SkipConcentration {
			d.concentration(charID, concentrating, max(dealt, hit.ConcentrationDamage))
		}
	}
	d.view["hp"], d.view["max_hp"], d.view["temp_hp"] = d.After.HP, d.After.Max, d.After.Temp
	return d, nil
}

// reduce is one part of a hit after what changes damage before it is taken: resistance, vulnerability and immunity
// (applyDamageResistance), fire underwater, the Ring of Warmth and the Resistance cantrip. raw damage is taken as it is.
func (d *hpDamage) reduce(charID int, p hpPart, raw bool, resisted *[]string) int {
	n := p.Amount
	if raw || n <= 0 {
		return max(n, 0)
	}
	if mod := applyDamageResistance(charID, n, p.Type); mod.WasHalved {
		n = mod.FinalDamage
		*resisted = append(*resisted, mod.Resistances...)
	} else if u, ok := underwaterFire(lobbyOfChar(charID), p.Type, n); ok {
		n = u
		*resisted = append(*resisted, "fire (underwater)")
	}
	if cut, note := ringOfWarmth(charID, p.Type); cut > 0 { // Ring of Warmth: Cold damage 2d8 less (F204)
		n = max(0, n-cut)
		d.view["ring_of_warmth"] = note
	}
	if cut, note := resistanceSpellReduction(charID, p.Type); cut > 0 { // the Resistance cantrip: 1d4 less, once per turn
		n = max(0, n-cut)
		d.view["resistance_spell"] = note
	}
	return n
}

// describe sets the status and the message of the rule's outcome.
func (d *hpDamage) describe(wardUsed bool) {
	plural := map[bool]string{true: "s", false: ""}
	switch d.Outcome {
	case game.AlreadyDead:
		d.Status = "already_dead"
	case game.InstantDeath:
		d.Status = "INSTANT_DEATH"
		if d.Before.HP <= 0 {
			d.view["message"] = "Damage at 0 Hit Points equal to the Hit Point maximum or more: the character dies."
		} else {
			d.view["message"] = "Massive damage: the damage left after reaching 0 Hit Points equals or exceeds the Hit Point maximum - instant death!"
		}
	case game.SameHitAtZero:
		d.Status = "unconscious"
	case game.FailedAtZero:
		d.Status = "unconscious"
		d.view["death_save_failures"] = d.After.Failures
		d.view["message"] = fmt.Sprintf("Damage at 0 Hit Points: %d Death Saving Throw failure%s (%d of 3); the character isn't Stable.", d.FailuresAdded, plural[d.FailuresAdded > 1], d.After.Failures)
	case game.DiedAtZero:
		d.Status = "dead"
		d.view["death_save_failures"] = d.After.Failures
		d.view["message"] = fmt.Sprintf("Damage at 0 Hit Points: %d Death Saving Throw failure%s (%d in all). The character dies.", d.FailuresAdded, plural[d.FailuresAdded > 1], d.After.Failures)
	case game.DroppedToZero:
		d.Status = "unconscious"
		d.view["message"] = "Dropped to 0 HP - unconscious and making death saves"
	default:
		d.Status = "damaged"
		if wardUsed {
			d.Status = "death_ward"
			d.view["message"] = "Death Ward: instead of dropping to 0 Hit Points you drop to 1, and the spell ends."
		}
	}
	d.view["status"] = d.Status
}

// rescue tries the features that keep a character who dropped to 0 Hit Points (and wasn't killed outright) above 0. They roll
// saves and spend uses on other rows, so they run after the drop is written; the Hit Points they give are written only while the
// character is still at 0 and alive (rescueFromZero).
func (d *hpDamage) rescue(charID int) {
	for _, r := range dropRescues {
		hp, note, ok, failed := r.try(charID, d.Before.HP, d.ToHP, d.Before.Max)
		if failed != "" && r.flag != "" {
			d.view[r.flag+"_failed"] = true
			d.view[r.noteKey] = failed
		}
		if !ok {
			continue
		}
		if after, done := rescueFromZero(charID, hp); done {
			d.After, d.Outcome, d.Status = after, game.Damaged, r.status
			d.view["status"], d.view["message"] = r.status, note
			if r.flag != "" {
				d.view[r.flag] = true
				d.view[r.noteKey] = note
			}
		}
		return
	}
}

// rescueFromZero gives a character at 0 Hit Points (alive) hp Hit Points instead: not healing, so nothing stops it.
func rescueFromZero(charID, hp int) (game.Vitals, bool) {
	done := false
	_, after, err := changeVitals(charID, func(_ *sql.Tx, v game.Vitals) (game.Vitals, error) {
		if !v.Dying() {
			return v, nil
		}
		done = true
		return v.SetHP(hp), nil
	})
	return after, err == nil && done
}

// afterDown: what dropping to 0 Hit Points (or dying) ends: Concentration (Unconscious), the Warding Bonds the character holds,
// its Gaseous Form and its Wild Shape.
func (d *hpDamage) afterDown(charID int) {
	for k, v := range characterDown(charID) {
		d.view[k] = v
	}
}

// characterDown ends what a character's drop to 0 Hit Points ends, and returns the notes for a reply.
func characterDown(charID int) map[string]interface{} {
	notes := map[string]interface{}{}
	db.Exec(`UPDATE characters SET concentrating_on = NULL WHERE id = $1`, charID)
	endWardingBondsOf(charID) // a Warding Bond ends when its caster drops to 0 Hit Points
	if n, _ := db.Exec(`DELETE FROM spell_effects WHERE target_id = $1 AND kind = 'gaseous_form'`, charID); n != nil {
		if c, _ := n.RowsAffected(); c > 0 {
			notes["gaseous_form_ended"] = "Gaseous Form ends: the creature dropped to 0 Hit Points."
		}
	}
	if note := endWildShapeIfDown(charID); note != "" {
		notes["wild_shape_ended"] = note // 0 Hit Points: Unconscious, so Incapacitated; the druid's own scores and AC come back
	}
	return notes
}

// concentration rolls the Constitution save damage calls for (F55), its DC from the damage taken after resistance (C-3).
func (d *hpDamage) concentration(charID int, spell string, taken int) {
	if relentlessHunter(charID, spell) { // Ranger level 13: damage can't break Hunter's Mark
		return
	}
	dc := game.ConcentrationDC(taken)
	cs := (&saveCtx{saver: charID, ability: "con", dc: dc, concentration: true}).roll() // Eldritch Mind, War Caster, Extended Spell
	d.view["concentration_dc"] = dc
	d.view["concentrating_on"] = spell
	d.view["concentration_save"] = cs.Total
	d.view["concentration_kept"] = cs.Success
	if cs.Success {
		d.view["concentration"] = fmt.Sprintf("Concentration: %s; %s goes on.", cs.Line(characterNameOf(charID)), spell)
		return
	}
	db.Exec(`UPDATE characters SET concentrating_on = NULL WHERE id = $1`, charID)
	d.view["concentration"] = fmt.Sprintf("Concentration: %s; %s ends.", cs.Line(characterNameOf(charID)), spell)
}

// ---------------------------------------------------------------- healing

type healSpec struct {
	ceilingHalf  bool // Preserve Life: not above half the Hit Point maximum
	onlyAtZero   bool // only a character at 0 Hit Points (Aura of Life, a natural 20 on a Death Saving Throw, a Stable creature's rest)
	onlyBloodied bool // only a Bloodied character (Survivor)
	giveBack     bool // damage undone, not Hit Points regained (Shield, Stone's Endurance): nothing that stops healing stops it (regainBlocked)
}

type healOpt func(*healSpec)

func healUpToHalf() healOpt   { return func(s *healSpec) { s.ceilingHalf = true } }
func healOnlyAtZero() healOpt { return func(s *healSpec) { s.onlyAtZero = true } }

// healOnlyBloodied: only a Bloodied character (game.Vitals.Bloodied) heals, decided on the locked row (Survivor).
func healOnlyBloodied() healOpt { return func(s *healSpec) { s.onlyBloodied = true } }

// hpHeal is what healing did.
type hpHeal struct {
	Name          string
	Before, After game.Vitals
	Healed        int    // the Hit Points regained
	Woke          bool   // from 0 to above 0: conscious again, Death Saving Throws reset
	Blocked       string // what kept the character from regaining Hit Points it would have regained (regainBlocked), or ""
}

// noRegainSources: every effect that keeps a creature from regaining Hit Points (SRD 5.2.1: "can't regain Hit Points"), each
// answering what it is, or "". One table, asked through regainBlocked by every way a character or a monster regains Hit Points
// (healHP, restoreAllHP, healMonster). Damage undone afterwards (undoDamage, undoMonsterDamage) is not regaining. Before feedback log
// 66 only the Sword of Wounding was asked, by healHP alone: a Pit Fiend's Bite and Chill Touch never stopped any healing.
var noRegainSources = []func(lobbyID, targetID int) string{
	func(lobbyID, targetID int) string { // a condition that says so: the Pit Fiend's Bite (condition_records, monster_effects_2024.go)
		for _, r := range loadConditionRecords(lobbyID, targetID) {
			if r.NoHealing {
				return r.phrase()
			}
		}
		return ""
	},
	func(lobbyID, targetID int) string { // the Sword of Wounding (items_c5_2024.go)
		if _, wounded := buffEffect(lobbyID, targetID, "no_regain"); wounded {
			return "Sword of Wounding"
		}
		return ""
	},
	func(lobbyID, targetID int) string { // Chill Touch (spell_riders_2024.go)
		for _, e := range combatEffectsOn(lobbyID, targetID) {
			if e.Kind == "no_healing" {
				return "Chill Touch"
			}
		}
		return ""
	},
}

// regainBlocked answers what keeps a creature (a character, or a monster's negative id in its campaign) from regaining Hit Points, or "".
func regainBlocked(lobbyID, targetID int) string {
	if targetID > 0 {
		lobbyID = lobbyOfChar(targetID)
	}
	for _, src := range noRegainSources {
		if why := src(lobbyID, targetID); why != "" {
			return why
		}
	}
	return ""
}

// noRegainLine is the line a refused healing writes in the campaign's log: every path that heals says why it did nothing.
func noRegainLine(name, why string) string {
	return fmt.Sprintf("%s can't regain Hit Points (%s): the healing has no effect.", name, why)
}

// healHP gives a character amount Hit Points (game.Heal: never above the maximum, nothing for the dead; from 0 the Death Saving
// Throws reset). A creature that can't regain Hit Points (regainBlocked) gets none: Blocked says why, and the campaign's log has the
// line. The error is the database's.
func healHP(charID, amount int, opts ...healOpt) (hpHeal, error) {
	var s healSpec
	for _, o := range opts {
		o(&s)
	}
	var h hpHeal
	if err := db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&h.Name); err != nil {
		return h, err
	}
	blocked := ""
	if !s.giveBack && amount > 0 {
		blocked = regainBlocked(0, charID) // read before the transaction: it reads other rows
	}
	var err error
	h.Before, h.After, err = changeVitals(charID, func(_ *sql.Tx, v game.Vitals) (game.Vitals, error) {
		if (s.onlyAtZero && v.HP > 0) || (s.onlyBloodied && !v.Bloodied()) {
			return v, nil
		}
		ceiling := v.Max
		if s.ceilingHalf {
			ceiling = v.Max / 2
		}
		after, healed := game.Heal(v, amount, ceiling)
		if healed > 0 && blocked != "" { // it would have regained them
			h.Blocked = blocked
			return v, nil
		}
		h.Healed = healed
		return after, nil
	})
	if err != nil {
		return h, err
	}
	if lobby := lobbyOfChar(charID); h.Blocked != "" && lobby > 0 {
		recordGMAction(db, lobby, "condition", noRegainLine(h.Name, h.Blocked), "")
	}
	h.Woke = game.Woke(h.Before, h.After)
	hpChanged(charID, h.Before, h.After)
	return h, nil
}

// healCharacter is healHP for the callers that show "name regains N (before → after)": the name is "" when there is no such
// character.
func healCharacter(targetID, amount int) (name string, before, after int) {
	h, err := healHP(targetID, amount)
	if err != nil {
		return "", 0, 0
	}
	return h.Name, h.Before.HP, h.After.HP
}

// undoDamage gives back what hits took when they are undone or reduced after they landed (Shield turning hits into misses,
// Stone's Endurance, snaring a missile): hp Hit Points and temp Temporary Hit Points. It isn't healing, so the Sword of Wounding
// doesn't stop it; a character the undone hits killed (alive before them) comes back. With exactly, the values become hp and
// temp (every hit of the turn undone: back to before the first).
func undoDamage(charID, hp, temp int, deadBefore, exactly bool) (game.Vitals, error) {
	before, after, err := changeVitals(charID, func(_ *sql.Tx, v game.Vitals) (game.Vitals, error) {
		if v.Dead && !deadBefore {
			v = v.Revive(0)
		}
		if exactly {
			v = v.SetHP(hp)
			v.Temp = max(temp, 0)
			return v, nil
		}
		v = v.SetHP(max(v.HP, 0) + hp)
		v.Temp = max(v.Temp+temp, 0)
		return v, nil
	})
	if err == nil {
		hpChanged(charID, before, after)
	}
	return after, err
}

// recordDeathSave writes a Death Saving Throw's result (game.Vitals.DeathSave) with the row locked, so damage at 0 Hit Points
// taken at the same moment is counted too. A creature that becomes Stable stays at 0 for 1d4 hours (stableRounds).
func recordDeathSave(charID, natural int, success bool) (before, after game.Vitals, out game.DeathSaveOutcome, err error) {
	before, after, err = changeVitals(charID, func(tx *sql.Tx, v game.Vitals) (game.Vitals, error) {
		var next game.Vitals
		next, out = v.DeathSave(natural, success)
		if out == game.DeathSaveStable {
			if err := startStableHours(tx, charID); err != nil {
				return v, err
			}
		}
		return next, nil
	})
	if err == nil {
		hpChanged(charID, before, after)
	}
	return before, after, out, err
}

// stabilizeCharacter makes a dying character Stable (game.Vitals.Stabilize: the Medicine action, Spare the Dying) with the row
// locked, so a Death Saving Throw or damage at the same moment is counted first. changed is false when the character wasn't
// dying (healed, dead) or was Stable already: nothing is written.
func stabilizeCharacter(charID int) (changed bool, err error) {
	before, after, err := changeVitals(charID, func(tx *sql.Tx, v game.Vitals) (game.Vitals, error) {
		next, ok := v.Stabilize()
		if !ok {
			return v, nil
		}
		changed = true
		return next, startStableHours(tx, charID)
	})
	if err == nil && changed {
		hpChanged(charID, before, after)
	}
	return changed, err
}

// startStableHours: a creature that becomes Stable regains 1 Hit Point after 1d4 hours (stableRounds).
func startStableHours(tx execer, charID int) error {
	_, err := tx.Exec(`UPDATE characters SET stable_rounds_left = `+stableRounds()+` WHERE id = $1`, charID)
	return err
}

// ---------------------------------------------------------------- set values, death, return to life

// setHP sets a character's Hit Points (the GM's "hp"): 0 to the maximum; from 0 the Death Saving Throws reset, to 0 the character
// is dying (and what that ends, ends). A dead character's Hit Points don't change.
func setHP(charID, hp int) (game.Vitals, error) {
	before, after, err := changeVitals(charID, func(_ *sql.Tx, v game.Vitals) (game.Vitals, error) { return v.SetHP(hp), nil })
	if err == nil {
		hpChanged(charID, before, after)
	}
	return after, err
}

// restoreAllHP is a Long Rest's: all Hit Points back, no Temporary Hit Points, the Death Saving Throws reset (not for the dead). A
// character that can't regain Hit Points (regainBlocked) keeps the Hit Points it has; the campaign's log says why.
func restoreAllHP(charID int) (game.Vitals, error) {
	blocked := regainBlocked(0, charID)
	before, after, err := changeVitals(charID, func(_ *sql.Tx, v game.Vitals) (game.Vitals, error) {
		if v.Dead {
			return v, nil
		}
		if blocked == "" {
			v = v.SetHP(v.Max)
		}
		if blocked != "" && v.HP <= 0 {
			return v, nil // still dying: its Death Saving Throws stand
		}
		v.Temp, v.Successes, v.Failures, v.Stable = 0, 0, 0, false
		return v, nil
	})
	if err == nil {
		if blocked != "" && !before.Dead && before.HP < before.Max {
			if lobby := lobbyOfChar(charID); lobby > 0 {
				recordGMAction(db, lobby, "condition", noRegainLine(characterNameOf(charID), blocked), "")
			}
		}
		hpChanged(charID, before, after)
	}
	return after, err
}

// killCharacter: the character dies (Power Word Kill, Exhaustion level 6, Divine Word, the Iron Flask's fissure). It returns false
// when it was already dead.
func killCharacter(charID int) (bool, error) {
	before, after, err := changeVitals(charID, func(_ *sql.Tx, v game.Vitals) (game.Vitals, error) { return v.Kill(), nil })
	if err != nil || before.Dead {
		return false, err
	}
	hpChanged(charID, before, after)
	return true, nil
}

// reviveCharacter returns a dead character to life with hp Hit Points (Revivify, Raise Dead, Resurrection, True Resurrection).
// It returns false when the character isn't dead.
func reviveCharacter(charID, hp int) (bool, error) {
	revived := false
	before, after, err := changeVitals(charID, func(_ *sql.Tx, v game.Vitals) (game.Vitals, error) {
		if !v.Dead {
			return v, nil
		}
		revived = true
		return v.Revive(hp), nil
	})
	if err == nil && revived {
		hpChanged(charID, before, after)
	}
	return revived, err
}

// hpChanged does what a change of Hit Points sets off besides the values: waking from 0, or what dropping to 0 ends.
func hpChanged(charID int, before, after game.Vitals) {
	switch {
	case game.Woke(before, after):
		removeCondition(charID, "unconscious")
	case before.HP > 0 && (after.HP <= 0 || after.Dead):
		characterDown(charID)
	}
}

// ---------------------------------------------------------------- the Hit Point maximum

// maxHPChange is the SET clause of a change of the Hit Point maximum by the amount in the placeholder p ("$3"): the maximum
// never below 1; with current, the current Hit Points change by as much (0 to the new maximum; a dead character's stay), and a
// character raised from 0 stops dying (its Death Saving Throws reset); without, they only stay within the new maximum (Aid
// ending). It is written as SQL so that the statements that claim a progression step (a level, the Tough feat: audit AUD-07)
// apply it in their one guarded statement; every expression reads the row as it was before the statement.
func maxHPChange(p string, current bool) string {
	newMax := "GREATEST(1, max_hp + " + p + ")"
	if !current {
		return "max_hp = " + newMax + ", hp = LEAST(hp, " + newMax + ")"
	}
	wakes := "(NOT COALESCE(is_dead,false) AND hp <= 0 AND hp + " + p + " > 0)"
	return "max_hp = " + newMax +
		", hp = CASE WHEN COALESCE(is_dead,false) THEN hp ELSE GREATEST(0, LEAST(" + newMax + ", hp + " + p + ")) END" +
		", death_save_successes = CASE WHEN " + wakes + " THEN 0 ELSE death_save_successes END" +
		", death_save_failures = CASE WHEN " + wakes + " THEN 0 ELSE death_save_failures END" +
		", is_stable = CASE WHEN " + wakes + " THEN false ELSE is_stable END"
}

// changeMaxHP changes a character's Hit Point maximum by delta (maxHPChange), and its current Hit Points with it when current.
func changeMaxHP(charID, delta int, current bool) (game.Vitals, error) {
	tx, err := db.Begin()
	if err != nil {
		return game.Vitals{}, err
	}
	defer tx.Rollback()
	before, err := lockVitals(tx, charID)
	if err != nil {
		return before, err
	}
	if _, err = tx.Exec(`UPDATE characters SET `+maxHPChange("$1", current)+` WHERE id = $2`, delta, charID); err != nil {
		return before, err
	}
	after, err := readVitals(tx, charID)
	if err != nil {
		return before, err
	}
	if err = tx.Commit(); err != nil {
		return before, err
	}
	hpChanged(charID, before, after)
	return after, nil
}
