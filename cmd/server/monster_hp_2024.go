package main

// The one owner of a monster's and a companion's Hit Points (HANDOFF item 28; audit AUD-09 and the C-7 part of AUD-24). A
// monster's Hit Points live in its turn order entry (hp, max_hp, temp_hp), a companion's in the companions table; every change
// to them is made here, with the row locked (changeFight for a monster, SELECT ... FOR UPDATE for a companion), from the values as
// they are then, so two changes at once are both counted. What a change does is game/hit_points.go's rule, the same as for a
// character: Temporary Hit Points take damage first, Hit Points never go below 0 or above the maximum, healing works from 0.
// Monsters and companions make no Death Saving Throws: at 0 Hit Points a monster is down (it has no turn) and a companion is gone.
//
// Before Nov 15 2026 the GM's area spell rewrote the whole turn order to change one monster (AUD-09), the GM's monster
// adjustments, Regeneration, a monster's reactions and the companion routes each read Hit Points, computed in Go and wrote an
// absolute value without a lock: two changes at once lost one.
//
// What a hit is worth (resistances, Undead Fortitude, a Polymorph's form) is worked out before the change from the fight as it is,
// without a lock; only the change itself is made under the lock (saveMonsterHit, loseMonsterHP).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// catalogForbidsHPGain reads an explicit prohibition from the current form.
// The same rule governs healing for encounter monsters and companions. An unreadable catalog is an error, never
// permission: only a missing catalog row (sql.ErrNoRows) is "no prohibition on record".
func catalogForbidsHPGain(q rowQuerier, key string) (bool, error) {
	var raw []byte
	if err := q.QueryRow(monsterSQL(`SELECT COALESCE(traits,'[]') FROM monsters WHERE slug=$1`, []interface{}{key}), key).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var traits []srdMonsterEntry
	if err := json.Unmarshal(raw, &traits); err != nil {
		return false, err
	}
	for _, trait := range traits {
		text := strings.ToLower(strings.ReplaceAll(trait.Text, "’", "'"))
		if strings.Contains(text, "can't regain hit points or gain temporary hit points") {
			return true, nil
		}
	}
	return false, nil
}

// absorptionHealsHP decides whether damage of damageType that a monster is immune to becomes Hit Points: its absorption
// trait says it does, unless the same form forbids regaining Hit Points (the prohibition wins, and the damage is only
// negated, as an immunity). The traits are read once; a failed read is an error, never permission to regain Hit Points.
// Every damage path that can make a monster's Hit Points rise (hurtMonster, adjustMonsterExtra, adjustRiders, a
// companion's damage, the Sphere of Annihilation) decides through this, so the prohibition is enforced in one place.
func absorptionHealsHP(q rowQuerier, key, damageType string) (bool, error) {
	if key == "" || damageType == "" {
		return false, nil
	}
	var raw []byte
	if err := q.QueryRow(monsterSQL(`SELECT COALESCE(traits,'[]') FROM monsters WHERE slug=$1`, []interface{}{key}), key).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var traits []srdMonsterEntry
	if err := json.Unmarshal(raw, &traits); err != nil {
		return false, err
	}
	absorbs := false
	for _, trait := range traits {
		if m := absorptionTraitRe.FindStringSubmatch(trait.Text); m != nil && strings.EqualFold(m[1], damageType) && strings.EqualFold(m[2], damageType) {
			absorbs = true
		}
		text := strings.ToLower(strings.ReplaceAll(trait.Text, "’", "'"))
		if strings.Contains(text, "can't regain hit points or gain temporary hit points") {
			return false, nil
		}
	}
	return absorbs, nil
}

// monsterVitals: an entry's Hit Points as game.Vitals. An entry without a maximum is held at its Hit Points.
func monsterVitals(e map[string]interface{}) game.Vitals {
	v := game.Vitals{HP: asInt(e["hp"]), Max: asInt(e["max_hp"]), Temp: asInt(e["temp_hp"])}
	if v.Max <= 0 {
		v.Max = max(v.HP, 0)
	}
	return v
}

// putMonsterVitals writes after into the entry (only what changed; a monster is never marked dead or dying).
func putMonsterVitals(e map[string]interface{}, before, after game.Vitals) {
	e["hp"] = max(after.HP, 0)
	if after.Temp != before.Temp {
		e["temp_hp"] = max(after.Temp, 0)
	}
	if after.Max != before.Max {
		e["max_hp"] = after.Max
	}
}

// changeMonsterVitals is the one change of a monster's Hit Points: the fight locked, change computes the new values from the
// current ones (and may read the rest of the entry), and they are written in the same transaction.
func changeMonsterVitals(lobby, id int, change func(tx *sql.Tx, v game.Vitals, e map[string]interface{}) game.Vitals) (before, after game.Vitals, name string, err error) {
	return changeMonsterVitalsE(lobby, id, func(tx *sql.Tx, v game.Vitals, e map[string]interface{}) (game.Vitals, error) {
		return change(tx, v, e), nil
	})
}

// changeMonsterVitalsE is changeMonsterVitals with a change that can fail (a catalog read, for one): the error rolls the
// change back and reaches the caller.
func changeMonsterVitalsE(lobby, id int, change func(tx *sql.Tx, v game.Vitals, e map[string]interface{}) (game.Vitals, error)) (before, after game.Vitals, name string, err error) {
	_, err = changeFightWithTx(lobby, func(tx *sql.Tx, f *fight) error {
		_, e := f.entry(id)
		if e == nil || id >= 0 {
			return errNotInFight
		}
		name = sprintAny(e["name"])
		before = monsterVitals(e)
		var cerr error
		after, cerr = change(tx, before, e)
		if cerr != nil {
			return cerr
		}
		if before.HP > 0 && after.HP <= 0 {
			if err := journalMonsterConcentrationIn(tx, lobby, id, map[string]interface{}{"event": "damage_or_death", "damage": 0, "hp_before": before.HP, "hp_after": after.HP, "target_name": name}); err != nil {
				return err
			}
		}
		putMonsterVitals(e, before, after)
		return nil
	})
	return before, after, name, err
}

// setMonsterHP sets a monster's Hit Points (0 to its maximum): a kill, a knock-out at 1, the GM's value, all of them back.
func setMonsterHP(lobby, id, hp int) error {
	defer directConcentrationJournalScope(lobby)()
	before, after, name, err := changeMonsterVitals(lobby, id, func(_ *sql.Tx, v game.Vitals, _ map[string]interface{}) game.Vitals { return v.SetHP(hp) })
	if err == nil && before.HP > 0 && after.HP <= 0 { // a kill by a Hit Point set (GM adjust, power words, items): the damage path's note does not run
		gmNoteMonsterConcentrationEnds(lobby, id, name, "is dead")
	}
	return err
}

// healMonster gives a monster up to amount Hit Points (never above its maximum; from 0 too: Regeneration, a hit undone).
// A form that forbids regaining Hit Points is left unchanged; an unreadable catalog is an error, not a heal.
func healMonster(lobby, id, amount int) (before, after game.Vitals, err error) {
	before, after, _, err = changeMonsterVitalsE(lobby, id, func(tx *sql.Tx, v game.Vitals, entry map[string]interface{}) (game.Vitals, error) {
		forbidden, cerr := catalogForbidsHPGain(tx, sprintAny(entry["monster_key"]))
		if cerr != nil {
			return v, cerr
		}
		if forbidden {
			return v, nil
		}
		v2, _ := game.Heal(v, amount, 0)
		return v2, nil
	})
	return before, after, err
}

// loseMonsterHP: a monster takes damage already worked out (resistances applied): Temporary Hit Points first, never below 0.
func loseMonsterHP(lobby, id, amount int, dtype string) (game.DamageResult, error) {
	defer directConcentrationJournalScope(lobby)()
	var r game.DamageResult
	var name string
	key := monsterKeyOfCombatant(lobby, id)
	penalties := damageDisadvantages(key, map[string]int{dtype: amount})
	_, err := changeFightWithTx(lobby, func(tx *sql.Tx, f *fight) error {
		_, entry := f.entry(id)
		if entry == nil || id >= 0 {
			return errNotInFight
		}
		name = sprintAny(entry["name"])
		r = game.TakeDamage(monsterVitals(entry), amount, false, false)
		if amount > 0 {
			if err := journalMonsterConcentrationIn(tx, lobby, id, map[string]interface{}{"event": "damage_or_death", "damage": amount, "hp_before": r.Before.HP, "hp_after": r.After.HP, "target_name": name}); err != nil {
				return err
			}
		}
		if r.After.HP <= 0 || sprintAny(entry["monster_key"]) != key {
			penalties = nil
		}
		if err := recordDamageDisadvantageIn(tx, lobby, id, asInt(f.current()["id"]) == id, penalties); err != nil {
			return err
		}
		putMonsterVitals(entry, r.Before, r.After)
		return nil
	})
	if err == nil {
		damageTraitGMNotices(lobby, id, name, penalties)
	}
	if err == nil && amount > 0 { // exact damage (GM adjust-monster, a legendary item): the same private Concentration reminders as damage that was rolled
		if r.After.HP <= 0 {
			if r.Before.HP > 0 {
				gmNoteMonsterConcentrationEnds(lobby, id, name, "is dead")
			}
		} else {
			gmNoteMonsterConcentration(lobby, id, name, amount, r.After.HP)
		}
	}
	return r, err
}

// setMonsterMaxHP sets a monster's Hit Point maximum (its Hit Points are kept within it); setMonsterTempHP its Temporary Hit Points.
func setMonsterMaxHP(lobby, id, maxHP int) error {
	_, _, _, err := changeMonsterVitals(lobby, id, func(_ *sql.Tx, v game.Vitals, _ map[string]interface{}) game.Vitals {
		v.Max = max(maxHP, 1)
		v.HP = min(v.HP, v.Max)
		return v
	})
	return err
}

func setMonsterTempHP(lobby, id, temp int) error {
	_, _, _, err := changeMonsterVitalsE(lobby, id, func(tx *sql.Tx, v game.Vitals, entry map[string]interface{}) (game.Vitals, error) {
		if temp > v.Temp {
			forbidden, cerr := catalogForbidsHPGain(tx, sprintAny(entry["monster_key"]))
			if cerr != nil {
				return v, cerr
			}
			if forbidden {
				return v, nil
			}
		}
		v.Temp = max(temp, 0)
		return v, nil
	})
	return err
}

// reshapeMonster changes a monster's entry into another form or back (Polymorph): the form's statistics, its Hit Points and
// maximum among them, replace the entry's. The only change of an entry besides the ones above that may set Hit Points.
func reshapeMonster(lobby, id int, f func(e map[string]interface{})) error {
	_, err := changeFight(lobby, func(fi *fight) error {
		_, e := fi.entry(id)
		if e == nil {
			return errNotInFight
		}
		f(e)
		return nil
	})
	return err
}

// copyEntry: a copy of a turn-order entry (its values are JSON values).
func copyEntry(e map[string]interface{}) map[string]interface{} {
	b, _ := json.Marshal(e)
	var c map[string]interface{}
	json.Unmarshal(b, &c)
	return c
}

// saveMonsterHit writes one hit on a monster, in a short transaction that locks the fight: the monster loses lost Hit Points from
// what it has by then (Temporary Hit Points first, never below 0), and every other field of the entry that the hit changed
// (before -> after) is written. A new form (Polymorph's end: a new maximum) takes the Hit Points the rules gave it. It returns
// the monster's Hit Points after the hit.
func saveMonsterHit(lobbyID, targetID int, before, after map[string]interface{}, lost int, amounts map[string]int) (int, error) {
	hp := 0
	key := sprintAny(before["monster_key"])
	penalties := damageDisadvantages(key, amounts)
	_, err := changeFightWithTx(lobbyID, func(tx *sql.Tx, f *fight) error {
		_, e := f.entry(targetID)
		if e == nil {
			return errNotInFight
		}
		if lost > 0 || (asInt(before["hp"]) > 0 && asInt(after["hp"]) <= 0) {
			if err := journalMonsterConcentrationIn(tx, lobbyID, targetID, map[string]interface{}{"event": "damage", "damage": max(0, lost), "hp_before": asInt(e["hp"]), "hp_after_requested": asInt(after["hp"]), "target_name": sprintAny(e["name"])}); err != nil {
				return err
			}
		}
		if sprintAny(e["monster_key"]) != key || sprintAny(after["monster_key"]) != key {
			penalties = nil
		}
		for k, v := range after {
			if k == "hp" || k == "temp_hp" {
				continue
			}
			if old, had := before[k]; !had || !reflect.DeepEqual(old, v) {
				e[k] = v
			}
		}
		for k := range before {
			if _, still := after[k]; !still && k != "hp" && k != "temp_hp" {
				delete(e, k)
			}
		}
		if asInt(after["max_hp"]) != asInt(before["max_hp"]) { // a new form: its Hit Points are the ones the rules set
			e["hp"] = max(asInt(after["hp"]), 0)
		} else if lost < 0 { // the hit's rules gave Hit Points back (absorption, a form ended without a stored maximum): never above the maximum
			forbidden, ferr := catalogForbidsHPGain(tx, sprintAny(e["monster_key"]))
			if ferr != nil {
				return ferr // an unreadable catalog is not permission to regain Hit Points
			}
			if !forbidden {
				e["hp"] = asInt(e["hp"]) - lost
				if m := asInt(e["max_hp"]); m > 0 && asInt(e["hp"]) > m {
					e["hp"] = m
				}
			}
		} else {
			v := monsterVitals(e)
			r := game.TakeDamage(v, lost, false, false)
			putMonsterVitals(e, v, r.After)
		}
		hp = asInt(e["hp"])
		if hp <= 0 {
			penalties = nil
		}
		return recordDamageDisadvantageIn(tx, lobbyID, targetID, asInt(f.current()["id"]) == targetID, penalties)
	})
	if err == nil {
		damageTraitGMNotices(lobbyID, targetID, sprintAny(before["name"]), penalties)
	}
	return hp, err
}

// monsterDamage is what damage to a monster did.
type monsterDamage struct {
	Name          string
	Dealt         int    // after resistance, immunity and vulnerability
	How           string // "immune", "resistant", "vulnerable" or ""
	Before, After int    // Hit Points
	MaxHP         int
	Text          string // "Goblin takes 7 fire damage. Goblin looks hurt."
}

// hurtMonster deals damage of one type to a monster in the combat: its resistances, immunities and vulnerabilities, Petrified,
// underwater fire, a Polymorph's form, Undead Fortitude; then saveMonsterHit. ok is false when it isn't in the fight.
func hurtMonster(lobbyID, targetID, dmg int, dtype string, crit bool) (monsterDamage, bool) {
	defer directConcentrationJournalScope(lobbyID)()
	f, err := readFight(db, lobbyID)
	if err != nil {
		return monsterDamage{}, false
	}
	_, e := f.entry(targetID)
	if e == nil || targetID >= 0 {
		return monsterDamage{}, false
	}
	e0 := copyEntry(e)
	h := monsterDamage{Name: sprintAny(e["name"]), Before: asInt(e["hp"])}
	d, how := adjustMonsterDamage(sprintAny(e["monster_key"]), dtype, dmg)
	d, how = petrifiedDamage(lobbyID, targetID, d, how)   // F196
	defer noteRegenerationBlock(lobbyID, targetID, dtype) // Troll: no Regeneration on its next turn after Acid or Fire
	if how != "resistant" && how != "immune" {
		if d2, ok := underwaterFire(lobbyID, dtype, d); ok {
			d, how = d2, "resistant"
		}
	}
	h.Dealt, h.How = d, how
	hp := asInt(e["hp"]) - d
	if how == "immune" && dmg > 0 {
		heals, aerr := absorptionHealsHP(db, sprintAny(e["monster_key"]), strings.ToLower(dtype))
		if aerr != nil {
			addGMNotice(lobbyID, fmt.Sprintf("The server couldn't apply damage to %s. Review this hit before continuing.", h.Name))
			return monsterDamage{}, false
		}
		if heals {
			h.How = "absorbed" // the trait turns this damage into Hit Points, up to the maximum
			hp = min(hp+dmg, max(asInt(e["max_hp"]), hp))
			how = h.How
		}
	}
	pnote := ""
	if hp <= 0 {
		hp, pnote = polymorphDrop(lobbyID, e, hp) // a Polymorph's beast form at 0 (spells_more_2024.go)
	}
	if hp <= 0 {
		var unote string
		hp, unote = undeadFortitude(lobbyID, e, hp, d, dtype, crit) // Zombie (dodge_2024.go); no save against a Critical Hit
		pnote += unote
	}
	e["hp"] = max(hp, 0)
	saved, err := saveMonsterHit(lobbyID, targetID, e0, e, asInt(e0["hp"])-asInt(e["hp"]), map[string]int{strings.ToLower(dtype): d})
	if err != nil {
		addGMNotice(lobbyID, fmt.Sprintf("The server couldn't apply damage to %s. Review this hit before continuing.", h.Name))
		return monsterDamage{}, false
	}
	h.After, h.MaxHP = saved, asInt(e["max_hp"])
	text := fmt.Sprintf("%s takes %d %s damage", h.Name, d, dtype)
	if d > 0 {
		text += wakeOnDamage(lobbyID, targetID, h.Name)
	}
	switch how {
	case "absorbed":
		text = fmt.Sprintf("%s absorbs %d %s damage and regains that many Hit Points", h.Name, dmg, dtype)
	case "immune":
		text = fmt.Sprintf("%s is immune to %s damage", h.Name, dtype)
	case "resistant":
		text += " (it resists)"
	case "vulnerable":
		text += " (it is vulnerable)"
	}
	if saved == 0 {
		text += ". " + h.Name + " is defeated!"
		gmNoteMonsterConcentrationEnds(lobbyID, targetID, h.Name, "is dead")
	} else {
		text += fmt.Sprintf(". %s looks %s.", h.Name, healthWord(saved, h.MaxHP))
	}
	h.Text = text + pnote
	gmNoteMonsterConcentration(lobbyID, targetID, h.Name, d, saved)
	return h, true
}

// gmNoteMonsterConcentration: a CONDITIONAL reminder for the GM, not Concentration tracking. The server does not know whether a monster is
// concentrating, so the note applies only if the GM knows it is; Spellcasting in the stat block does not mean it is concentrating, and a
// feature that is not Spellcasting can require Concentration too (this note does not see those). Written for damage that leaves a monster
// with a Spellcasting or Innate Spellcasting entry standing; only damage that goes through hurtMonster/damageMonster and the character
// attack route reaches it. The DC uses the damage TAKEN (see docs for the Temporary Hit Points reading).
// PRIVATE: whether a monster casts or concentrates is not for a player's reply or the shared action log, so this goes to the GM's notices
// (addGMNotice, gm_notices_2024.go) and never into the text the caller returns.
func gmNoteMonsterConcentration(lobbyID, targetID int, name string, dealt, hpAfter int) {
	if dealt <= 0 || hpAfter <= 0 {
		return
	}
	if monsterConcentrationDamage(lobbyID, targetID, name, dealt) { // a tracked Concentration (monster_concentration_2024.go)
		return
	}
	_, meta, ok := monsterCombatant(lobbyID, targetID)
	if !ok || !metaCastsSpells(meta) {
		return
	}
	if monsterIncapacitated(lobbyID, targetID) { // Rules Glossary, Concentration: it ends while the creature has the Incapacitated condition, so there is nothing to save
		addGMNotice(lobbyID, fmt.Sprintf("%s has a condition that makes it Incapacitated. Concentration ends while a creature is Incapacitated, so if it was concentrating that already ended: no saving throw for this damage.", name))
		return
	}
	addGMNotice(lobbyID, fmt.Sprintf("Only if %s is currently concentrating on something (the server does not track it, and being able to cast does not mean it is): this damage calls for a Constitution saving throw, DC %d (10 or half the damage, whichever is higher, up to 30; one source's damage in several types is one save from the total). Roll it yourself and end the effect on a failure. If it is not concentrating, ignore this.", name, game.ConcentrationDC(dealt)))
}

// gmNoteMonsterConcentrationEnds: a monster with a Spellcasting entry that dies or is knocked out (the Unconscious condition includes
// Incapacitated). Rules Glossary, Concentration: "Your Concentration ends if you have the Incapacitated condition or you die." Conditional,
// as the server does not track what a monster concentrates on. Only for the defeat or knock-out reached through damage; a kill by a Hit Point set
// (setMonsterHP: GM adjust, power words, items) gives it too. PRIVATE like gmNoteMonsterConcentration.
func gmNoteMonsterConcentrationEnds(lobbyID, targetID int, name, how string) {
	if monsterConcentrationEnds(lobbyID, targetID, name, name+" "+how) { // a tracked Concentration ended (monster_concentration_2024.go)
		return
	}
	_, meta, ok := monsterCombatant(lobbyID, targetID)
	if !ok || !metaCastsSpells(meta) {
		return
	}
	addGMNotice(lobbyID, fmt.Sprintf("%s %s, which ends Concentration (Incapacitated or dead). If it was concentrating on an effect, end that effect now; no saving throw.", name, how))
}

// metaCastsSpells: the stat block has a Spellcasting or Innate Spellcasting entry among its traits or actions.
func metaCastsSpells(meta *monsterMeta) bool {
	if meta == nil {
		return false
	}
	for _, list := range [][]srdMonsterEntry{meta.Traits, meta.Actions} {
		for _, e := range list {
			if strings.Contains(strings.ToLower(traitName(e.Name)), "spellcasting") {
				return true
			}
		}
	}
	return false
}

// damageMonster deals damage to a monster in the combat and says what happened (hurtMonster).
func damageMonster(lobbyID, targetID, dmg int, dtype string, critical ...bool) (string, bool) {
	h, ok := hurtMonster(lobbyID, targetID, dmg, dtype, len(critical) > 0 && critical[0])
	return h.Text, ok
}

// ---------------------------------------------------------------- companions

// changeCompanionHP is the one change of a companion's Hit Points: its row locked, change computes the new values from the
// current ones. A companion that is gone doesn't change (errNotInFight).
func changeCompanionHP(id int, change func(v game.Vitals) game.Vitals, recordDamage func(*sql.Tx) error) (before, after game.Vitals, err error) {
	tx, err := db.Begin()
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback()
	var status, form string
	if err = tx.QueryRow(`SELECT hp, max_hp, COALESCE(status,'active'), COALESCE(form,'') FROM companions WHERE id = $1 FOR UPDATE`, id).Scan(&before.HP, &before.Max, &status, &form); err != nil {
		return before, before, err
	}
	if status == "gone" {
		return before, before, errNotInFight
	}
	after = change(before)
	if after.HP > before.HP {
		forbidden, ferr := catalogForbidsHPGain(tx, form)
		if ferr != nil {
			return before, before, ferr // an unreadable catalog is not permission to regain Hit Points
		}
		if forbidden {
			after.HP = before.HP
		}
	}
	after.HP = max(after.HP, 0)
	if after.HP > 0 && recordDamage != nil {
		if err = recordDamage(tx); err != nil {
			return before, before, err
		}
	}
	if after.HP != before.HP {
		if _, err = tx.Exec(`UPDATE companions SET hp = $1 WHERE id = $2`, after.HP, id); err != nil {
			return before, before, err
		}
	}
	return before, after, tx.Commit()
}

// damageCompanion deals damage to a companion and says what happened; at 0 Hit Points it disappears. The damage goes through the
// stat block's resistances, immunities and vulnerabilities (its form is the monster it was made from: adjustMonsterDamage, the rule
// every monster's damage uses), an absorbing trait, and Petrified (petrifiedDamage). Underwater fire and a Polymorph form are not
// applied to a companion (listed in docs). Before this, a companion took the full amount of every type.
func damageCompanion(c companion, dmg int, dtype string) string {
	dtype = strings.ToLower(strings.TrimSpace(dtype))
	d, how := adjustMonsterDamage(c.Form, dtype, dmg)
	d, how = petrifiedDamage(c.LobbyID, c.CombatID, d, how)
	if how == "immune" && dmg > 0 {
		heals, aerr := absorptionHealsHP(db, c.Form, dtype)
		if aerr != nil {
			addGMNotice(c.LobbyID, fmt.Sprintf("The server couldn't apply damage to %s. Review this hit before continuing.", companionDisplayName(c)))
			return ""
		}
		if heals {
			got, err := healCompanion(c.ID, dmg) // the trait turns this damage into Hit Points
			if err != nil {
				addGMNotice(c.LobbyID, fmt.Sprintf("The server couldn't apply damage to %s. Review this hit before continuing.", companionDisplayName(c)))
				return ""
			}
			return fmt.Sprintf("%s absorbs %d %s damage and regains %d Hit Points.", companionDisplayName(c), dmg, dtype, got)
		}
	}
	if how == "immune" {
		return fmt.Sprintf("%s is immune to %s damage.", companionDisplayName(c), dtype)
	}
	note := ""
	switch how {
	case "resistant":
		note = " (it resists)"
	case "vulnerable":
		note = " (it is vulnerable)"
	}
	penalties := damageDisadvantages(c.Form, map[string]int{dtype: d})
	var recordDamage func(*sql.Tx) error
	if len(penalties) > 0 {
		isCurrent := currentTurnID(c.LobbyID) == c.CombatID
		recordDamage = func(tx *sql.Tx) error {
			return recordDamageDisadvantageIn(tx, c.LobbyID, c.CombatID, isCurrent, penalties)
		}
	}
	_, after, err := changeCompanionHP(c.ID, func(v game.Vitals) game.Vitals {
		return game.TakeDamage(v, d, false, false).After
	}, recordDamage)
	if err != nil {
		if errors.Is(err, errNotInFight) || errors.Is(err, sql.ErrNoRows) {
			return fmt.Sprintf("%s is already gone.", companionDisplayName(c))
		}
		addGMNotice(c.LobbyID, fmt.Sprintf("The server couldn't apply damage to %s. Review this hit before continuing.", companionDisplayName(c)))
		return ""
	}
	if after.HP <= 0 {
		endCompanion(c.ID, "dropped to 0 Hit Points")
		text := fmt.Sprintf("%s takes %d %s damage%s and drops to 0 Hit Points: it disappears.", companionDisplayName(c), d, dtype, note)
		if c.Kind == "familiar" {
			text += " It reappears when its owner casts Find Familiar again."
		}
		return text
	}
	damageTraitGMNotices(c.LobbyID, c.CombatID, companionDisplayName(c), penalties)
	wake := ""
	if d > 0 {
		wake = wakeOnDamageSleep(c.LobbyID, c.CombatID, companionDisplayName(c))
	}
	return fmt.Sprintf("%s takes %d %s damage%s (%s).", companionDisplayName(c), d, dtype, note, healthWord(after.HP, after.Max)) + wake
}

// healCompanion gives a companion up to amount Hit Points (never above its maximum); it returns the Hit Points regained.
func healCompanion(id, amount int) (int, error) {
	var got int
	_, _, err := changeCompanionHP(id, func(v game.Vitals) game.Vitals {
		var v2 game.Vitals
		v2, got = game.Heal(v, amount, 0)
		return v2
	}, nil)
	return got, err
}

// companionTakesForm: a familiar called again in a new form (Find Familiar cast while it exists, or after it dropped to 0): the
// form's statistics, with its full Hit Points, replace the old ones and it is active again.
func companionTakesForm(id int, form, name, ctype string, hp, ac int, speed string, lobbyID int, attacks string) error {
	_, err := monsterExec(`UPDATE companions SET form = $1, name = $2, creature_type = $3, hp = $4, max_hp = $4, ac = $5, speed = $6, status = 'active', lobby_id = $7, attacks = $9,
		traits = (SELECT COALESCE(traits,'[]') FROM monsters WHERE slug = $1) WHERE id = $8`,
		form, name, ctype, hp, ac, speed, lobbyID, id, attacks)
	return err
}

// monsterHitFields are the reply fields of a monster's damage in the GM's area spell, from how it took it.
func monsterHitFields(h monsterDamage, dtype string) map[string]interface{} {
	out := map[string]interface{}{}
	switch h.How {
	case "immune":
		out["immunities_applied"] = []string{strings.ToLower(dtype)}
	case "absorbed":
		out["absorbed"] = []string{strings.ToLower(dtype)}
	case "resistant":
		out["resistances_applied"] = []string{strings.ToLower(dtype)}
	case "vulnerable":
		out["vulnerabilities_applied"] = []string{strings.ToLower(dtype)}
	}
	return out
}
