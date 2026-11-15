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
func changeMonsterVitals(lobby, id int, change func(v game.Vitals, e map[string]interface{}) game.Vitals) (before, after game.Vitals, name string, err error) {
	_, err = changeFight(lobby, func(f *fight) error {
		_, e := f.entry(id)
		if e == nil || id >= 0 {
			return errNotInFight
		}
		name = sprintAny(e["name"])
		before = monsterVitals(e)
		after = change(before, e)
		putMonsterVitals(e, before, after)
		return nil
	})
	return before, after, name, err
}

// setMonsterHP sets a monster's Hit Points (0 to its maximum): a kill, a knock-out at 1, the GM's value, all of them back.
func setMonsterHP(lobby, id, hp int) error {
	_, _, _, err := changeMonsterVitals(lobby, id, func(v game.Vitals, _ map[string]interface{}) game.Vitals { return v.SetHP(hp) })
	return err
}

// healMonster gives a monster up to amount Hit Points (never above its maximum; from 0 too: Regeneration, a hit undone).
func healMonster(lobby, id, amount int) (before, after game.Vitals, err error) {
	before, after, _, err = changeMonsterVitals(lobby, id, func(v game.Vitals, _ map[string]interface{}) game.Vitals {
		v2, _ := game.Heal(v, amount, 0)
		return v2
	})
	return before, after, err
}

// loseMonsterHP: a monster takes damage already worked out (resistances applied): Temporary Hit Points first, never below 0.
func loseMonsterHP(lobby, id, amount int) (game.DamageResult, error) {
	var r game.DamageResult
	_, _, _, err := changeMonsterVitals(lobby, id, func(v game.Vitals, _ map[string]interface{}) game.Vitals {
		r = game.TakeDamage(v, amount, false, false)
		return r.After
	})
	return r, err
}

// setMonsterMaxHP sets a monster's Hit Point maximum (its Hit Points are kept within it); setMonsterTempHP its Temporary Hit Points.
func setMonsterMaxHP(lobby, id, maxHP int) error {
	_, _, _, err := changeMonsterVitals(lobby, id, func(v game.Vitals, _ map[string]interface{}) game.Vitals {
		v.Max = max(maxHP, 1)
		v.HP = min(v.HP, v.Max)
		return v
	})
	return err
}

func setMonsterTempHP(lobby, id, temp int) error {
	_, _, _, err := changeMonsterVitals(lobby, id, func(v game.Vitals, _ map[string]interface{}) game.Vitals {
		v.Temp = max(temp, 0)
		return v
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
func saveMonsterHit(lobbyID, targetID int, before, after map[string]interface{}, lost int) (int, error) {
	hp := 0
	_, err := changeFight(lobbyID, func(f *fight) error {
		_, e := f.entry(targetID)
		if e == nil {
			return errNotInFight
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
			hp = asInt(e["hp"])
			return nil
		}
		v := monsterVitals(e)
		r := game.TakeDamage(v, lost, false, false)
		putMonsterVitals(e, v, r.After)
		hp = r.After.HP
		return nil
	})
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
	saved, err := saveMonsterHit(lobbyID, targetID, e0, e, asInt(e0["hp"])-asInt(e["hp"]))
	if err != nil {
		return monsterDamage{}, false
	}
	h.After, h.MaxHP = saved, asInt(e["max_hp"])
	text := fmt.Sprintf("%s takes %d %s damage", h.Name, d, dtype)
	if d > 0 {
		text += wakeOnDamage(lobbyID, targetID, h.Name)
	}
	switch how {
	case "immune":
		text = fmt.Sprintf("%s is immune to %s damage", h.Name, dtype)
	case "resistant":
		text += " (it resists)"
	case "vulnerable":
		text += " (it is vulnerable)"
	}
	if saved == 0 {
		text += ". " + h.Name + " is defeated!"
	} else {
		text += fmt.Sprintf(". %s looks %s.", h.Name, healthWord(saved, h.MaxHP))
	}
	h.Text = text + pnote
	return h, true
}

// damageMonster deals damage to a monster in the combat and says what happened (hurtMonster).
func damageMonster(lobbyID, targetID, dmg int, dtype string, critical ...bool) (string, bool) {
	h, ok := hurtMonster(lobbyID, targetID, dmg, dtype, len(critical) > 0 && critical[0])
	return h.Text, ok
}

// ---------------------------------------------------------------- companions

// changeCompanionHP is the one change of a companion's Hit Points: its row locked, change computes the new values from the
// current ones. A companion that is gone doesn't change (errNotInFight).
func changeCompanionHP(id int, change func(v game.Vitals) game.Vitals) (before, after game.Vitals, err error) {
	ensureCompanions()
	tx, err := db.Begin()
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow(`SELECT hp, max_hp, COALESCE(status,'active') FROM companions WHERE id = $1 FOR UPDATE`, id).Scan(&before.HP, &before.Max, &status); err != nil {
		return before, before, err
	}
	if status == "gone" {
		return before, before, errNotInFight
	}
	after = change(before)
	after.HP = max(after.HP, 0)
	if after.HP != before.HP {
		if _, err = tx.Exec(`UPDATE companions SET hp = $1 WHERE id = $2`, after.HP, id); err != nil {
			return before, before, err
		}
	}
	return before, after, tx.Commit()
}

// damageCompanion deals damage to a companion and says what happened; at 0 Hit Points it disappears.
func damageCompanion(c companion, dmg int, dtype string) string {
	_, after, err := changeCompanionHP(c.ID, func(v game.Vitals) game.Vitals { return game.TakeDamage(v, dmg, false, false).After })
	if err != nil {
		if errors.Is(err, errNotInFight) || errors.Is(err, sql.ErrNoRows) {
			return fmt.Sprintf("%s is already gone.", companionDisplayName(c))
		}
		return ""
	}
	if after.HP <= 0 {
		endCompanion(c.ID, "dropped to 0 Hit Points")
		text := fmt.Sprintf("%s takes %d %s damage and drops to 0 Hit Points: it disappears.", companionDisplayName(c), dmg, dtype)
		if c.Kind == "familiar" {
			text += " It reappears when its owner casts Find Familiar again."
		}
		return text
	}
	return fmt.Sprintf("%s takes %d %s damage (%s).", companionDisplayName(c), dmg, dtype, healthWord(after.HP, after.Max))
}

// healCompanion gives a companion up to amount Hit Points (never above its maximum); it returns the Hit Points regained.
func healCompanion(id, amount int) (int, error) {
	var got int
	_, _, err := changeCompanionHP(id, func(v game.Vitals) game.Vitals {
		var v2 game.Vitals
		v2, got = game.Heal(v, amount, 0)
		return v2
	})
	return got, err
}

// companionTakesForm: a familiar called again in a new form (Find Familiar cast while it exists, or after it dropped to 0): the
// form's statistics, with its full Hit Points, replace the old ones and it is active again.
func companionTakesForm(id int, form, name, ctype string, hp, ac int, speed string, lobbyID int, attacks string) error {
	_, err := db.Exec(`UPDATE companions SET form = $1, name = $2, creature_type = $3, hp = $4, max_hp = $4, ac = $5, speed = $6, status = 'active', lobby_id = $7, attacks = $9,
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
	case "resistant":
		out["resistances_applied"] = []string{strings.ToLower(dtype)}
	case "vulnerable":
		out["vulnerabilities_applied"] = []string{strings.ToLower(dtype)}
	}
	return out
}
