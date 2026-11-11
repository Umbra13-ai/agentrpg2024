package main

// Druid, 2024 rules (SRD 5.2.1).
//	Wild Shape        uses from the Features table (2, 3 at level 6, 4 at level 17, ...); a Short Rest returns one use.
//	Primal Strike     (level 7, Elemental Fury option) once per turn on a weapon hit: +1d8 Cold, Fire, Lightning or Thunder damage
//	                  (2d8 from level 15). Name the type in the attack ("primal strike lightning"); the default is Fire.
//	                  A Druid who chose Potent Spellcasting (subclass_choices.elemental_fury) does not get it.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func ensureDruidColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS primal_strike_used BOOLEAN DEFAULT FALSE`)
	}
}

// primalStrike rolls the extra elemental damage of a Druid of level 7+ once per turn.
func primalStrike(charID int, class string, level int, descLower string, crit bool) (int, string) {
	if strings.ToLower(class) != "druid" || level < 7 {
		return 0, ""
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var choices map[string]string
	json.Unmarshal(raw, &choices)
	if choices["elemental_fury"] == "potent_spellcasting" {
		return 0, ""
	}
	var used bool
	db.QueryRow(`SELECT COALESCE(primal_strike_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
	if used {
		return 0, ""
	}
	db.Exec(`UPDATE characters SET primal_strike_used = true WHERE id = $1`, charID)
	primalPending.Store(charID, true) // F147: a miss gives the once-a-turn use back (withdrawHitEffects)
	dtype := "fire"
	for _, t := range []string{"cold", "lightning", "thunder", "fire"} {
		if strings.Contains(descLower, t) {
			dtype = t
			break
		}
	}
	dice := "1d8"
	if level >= 15 {
		dice = "2d8"
	}
	n := game.RollDamage(dice, crit)
	addRider(charID, dtype, n)
	return n, fmt.Sprintf(" [Primal Strike +%d %s]", n, dtype)
}

// wildResurgence (Druid level 5), no action. "wild_resurgence shape" (default): with no Wild Shape uses left, expend a spell slot
// (the lowest one you have) to regain one use. "wild_resurgence slot": expend a Wild Shape use to gain a level 1 spell slot, once per
// Long Rest.
func wildResurgence(charID int, description string) string {
	var class string
	var level int
	var usedJSON, slotsJSON []byte
	if db.QueryRow(`SELECT class, level, COALESCE(class_resources_used,'{}'), COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&class, &level, &usedJSON, &slotsJSON) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "druid" || level < 5 {
		return "Wild Resurgence needs a Druid of level 5 or higher."
	}
	used := map[string]int{}
	json.Unmarshal(usedJSON, &used)
	slotsUsed := map[string]int{}
	json.Unmarshal(slotsJSON, &slotsUsed)
	max := game.ClassTableInt(class, level, "Wild Shape")
	slots := characterSpellSlots(charID, class, level)
	if strings.Contains(strings.ToLower(description), "slot") && !strings.Contains(strings.ToLower(description), "shape") {
		if used["wild_resurgence_slot"] > 0 {
			return "You already traded Wild Shape for a spell slot; finish a Long Rest."
		}
		if used["wild_shape"] >= max {
			return "You have no Wild Shape use to expend."
		}
		if slotsUsed["1"] <= 0 {
			return "You have no expended level 1 spell slot to regain."
		}
		used["wild_shape"]++
		used["wild_resurgence_slot"] = 1
		slotsUsed["1"]--
		writeUsed(charID, used)
		b, _ := json.Marshal(slotsUsed)
		db.Exec(`UPDATE characters SET spell_slots_used = $1 WHERE id = $2`, string(b), charID)
		return "🌿 Wild Resurgence: you expend a use of Wild Shape and regain a level 1 spell slot."
	}
	if used["wild_shape"] < max {
		return "Wild Resurgence works when you have no uses of Wild Shape left."
	}
	if used["wild_resurgence_turn"] > 0 {
		return "Once on each of your turns."
	}
	for lvl := 1; lvl <= 9; lvl++ {
		if slots[lvl] > 0 && slotsUsed[fmt.Sprint(lvl)] < slots[lvl] {
			slotsUsed[fmt.Sprint(lvl)]++
			used["wild_shape"]--
			writeUsed(charID, used)
			b, _ := json.Marshal(slotsUsed)
			db.Exec(`UPDATE characters SET spell_slots_used = $1 WHERE id = $2`, string(b), charID)
			return fmt.Sprintf("🌿 Wild Resurgence: you expend a level %d spell slot and regain one use of Wild Shape.", lvl)
		}
	}
	return "You have no spell slot left to expend."
}
