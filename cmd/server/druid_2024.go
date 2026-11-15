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
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "druid" || level < 5 {
		return "Wild Resurgence needs a Druid of level 5 or higher."
	}
	max := game.ClassTableInt(class, level, "Wild Shape")
	slots := characterSpellSlots(charID, class, level)
	toSlot := strings.Contains(strings.ToLower(description), "slot") && !strings.Contains(strings.ToLower(description), "shape")
	reply := ""
	// the check and the trade on the locked counts (resources_2024.go)
	_, _, err := changeSpent(charID, func(sp *spent) error {
		if toSlot {
			switch {
			case sp.Uses["wild_resurgence_slot"] > 0:
				return &notEnough{"You already traded Wild Shape for a spell slot; finish a Long Rest.", 0}
			case sp.Uses["wild_shape"] >= max:
				return &notEnough{"You have no Wild Shape use to expend.", 0}
			case !regainSlotIn(sp.Slots, 1):
				return &notEnough{"You have no expended level 1 spell slot to regain.", 0}
			}
			sp.Uses["wild_shape"]++
			sp.Uses["wild_resurgence_slot"] = 1
			reply = "🌿 Wild Resurgence: you expend a use of Wild Shape and regain a level 1 spell slot."
			return nil
		}
		if sp.Uses["wild_shape"] < max {
			return &notEnough{"Wild Resurgence works when you have no uses of Wild Shape left.", 0}
		}
		if sp.Uses["wild_resurgence_turn"] > 0 {
			return &notEnough{"Once on each of your turns.", 0}
		}
		for lvl := 1; lvl <= 9; lvl++ {
			if slots[lvl] > 0 && spendSlotIn(sp.Slots, lvl, slots[lvl], "") == nil {
				sp.Uses["wild_shape"]--
				reply = fmt.Sprintf("🌿 Wild Resurgence: you expend a level %d spell slot and regain one use of Wild Shape.", lvl)
				return nil
			}
		}
		return &notEnough{"You have no spell slot left to expend.", 0}
	})
	if err != nil {
		return spendText(err, "Wild Resurgence")
	}
	return reply
}
