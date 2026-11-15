package main

// Sorcerer and Warlock, 2024 rules (SRD 5.2.1).
//	Sorcery Points     the Sorcery Points column of the Features table (none at level 1, 2 at level 2).
//	Innate Sorcery     action "innate_sorcery" (level 1): a Bonus Action; for 1 minute (10 rounds of combat) the spell save DC of
//	                   Sorcerer spells is 1 higher and Sorcerer spell attack rolls have Advantage. Two uses per Long Rest.
//	Magical Cunning    action "magical_cunning" (Warlock level 2): regain half of the expended Pact Magic slots (all at level 20),
//	                   once per Long Rest.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func ensureCasterColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS innate_sorcery_rounds INTEGER DEFAULT 0`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS magical_cunning_used BOOLEAN DEFAULT FALSE`)
	}
}

// innateSorcery is the Bonus Action that turns Innate Sorcery on.
func innateSorcery(charID int) string {
	var class string
	var level int
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "sorcerer" {
		return "Only a Sorcerer has Innate Sorcery."
	}
	extra := ""
	pointsMax := classResourceMax(charID, class, level, "sorcery_points", 0)
	// a use, or (Sorcery Incarnate, level 7) 2 Sorcery Points when none is left: one locked step (resources_2024.go)
	_, used, err := changeSpent(charID, func(sp *spent) error {
		if sp.Uses["innate_sorcery"] < 2 {
			sp.Uses["innate_sorcery"]++
			return nil
		}
		if level < 7 {
			return &notEnough{"You have used Innate Sorcery twice; finish a Long Rest.", 0}
		}
		if err := sp.spend("sorcery_points", 2, pointsMax); err != nil {
			return &notEnough{"You have used Innate Sorcery twice; Sorcery Incarnate needs 2 Sorcery Points (" + err.Error() + ").", 0}
		}
		extra = " (Sorcery Incarnate: 2 Sorcery Points spent)"
		return nil
	})
	if err != nil {
		return spendText(err, "Innate Sorcery")
	}
	db.Exec(`UPDATE characters SET innate_sorcery_rounds = 10 WHERE id = $1`, charID)
	left := 2 - used.Uses["innate_sorcery"]
	if left < 0 {
		left = 0
	}
	return fmt.Sprintf("🔥 Innate Sorcery%s: for 1 minute your Sorcerer spells have +1 to their save DC and your Sorcerer spell attack rolls have Advantage (%d use(s) left).", extra, left)
}

// innateSorceryDC is +1 to the spell save DC while Innate Sorcery is active.
func innateSorceryDC(charID int, class string) int {
	if strings.ToLower(class) != "sorcerer" {
		return 0
	}
	var rounds int
	db.QueryRow(`SELECT COALESCE(innate_sorcery_rounds,0) FROM characters WHERE id = $1`, charID).Scan(&rounds)
	if rounds > 0 {
		return 1
	}
	return 0
}

// innateSorceryTick counts a round of combat at the end of the Sorcerer's turn.
func innateSorceryTick(charID int) {
	db.Exec(`UPDATE characters SET innate_sorcery_rounds = innate_sorcery_rounds - 1 WHERE id = $1 AND innate_sorcery_rounds > 0`, charID)
}

// magicalCunning regains Pact Magic slots.
func magicalCunning(charID int) string {
	var class string
	var level int
	if db.QueryRow(`SELECT class, level FROM characters WHERE id = $1`, charID).Scan(&class, &level) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "warlock" || level < 2 {
		return "Magical Cunning needs a Warlock of level 2 or higher."
	}
	col := pactSlotColumn(charID) // F132: a single-class Warlock spends its Pact slots from spell_slots_used
	total := 0
	for _, n := range game.SpellSlots("warlock", level) {
		total += n
	}
	regain := 0
	// the once-per-Long-Rest mark and the slots in one transaction, the row locked (resources_2024.go)
	tx, err := db.Begin()
	if err != nil {
		return dbReadMessage("the character")
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE characters SET magical_cunning_used = true WHERE id = $1 AND NOT COALESCE(magical_cunning_used,false)`, charID)
	if err != nil {
		return dbReadMessage("the character")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "You already used Magical Cunning; finish a Long Rest."
	}
	if _, _, err := changeSpentTx(tx, charID, func(sp *spent) error {
		spent := sp.Pact
		if col == "spell_slots_used" {
			spent = sp.Slots
		}
		spentTotal := 0
		for _, n := range spent {
			spentTotal += max(0, n)
		}
		if spentTotal == 0 {
			return &notEnough{"You have all your Pact Magic slots.", 0}
		}
		regain = (total + 1) / 2
		if level >= 20 {
			regain = total // Eldritch Master
		}
		regain = min(regain, spentTotal)
		left := regain
		for k, n := range spent {
			take := min(max(0, n), left)
			spent[k] = n - take
			left -= take
		}
		return nil
	}); err != nil {
		return spendText(err, "Magical Cunning")
	}
	if tx.Commit() != nil {
		return dbReadMessage("the character")
	}
	return fmt.Sprintf("✨ Magical Cunning: after a minute-long rite you regain %d Pact Magic slot(s).", regain)
}

// naturalACBaseFor is the unarmored Armor Class base. A Draconic Sorcerer's Draconic Resilience (2024) is 10 plus the
// Dexterity and Charisma modifiers; the Dexterity part is added by the caller, so the base is 10 + Charisma modifier.
func naturalACBaseFor(charID int, subclass string, level int) int {
	// Unarmored Defense (level 1): a Barbarian adds the Constitution modifier (a Shield still counts); a Monk adds the Wisdom modifier
	// while wearing no armor and wielding no Shield. (The armor calculation uses this base only when no armor is worn.)
	// A multiclass character gains a later class's level 1 features too (a Barbarian 1 / Monk 1 has both Unarmored Defenses) and uses
	// the better one; Draconic Resilience counts from a Sorcerer subclass in any position.
	var class string
	var con, wis int
	var shield bool
	db.QueryRow(`SELECT COALESCE(class,''), con, wis, COALESCE(equipped_shield,false) FROM characters WHERE id = $1`, charID).Scan(&class, &con, &wis, &shield)
	unarmored := 10
	levels := classLevelsOf(charID)
	if levels["barbarian"] > 0 {
		unarmored = maxInt(unarmored, 10+game.Modifier(con))
	}
	if levels["monk"] > 0 && !shield {
		unarmored = maxInt(unarmored, 10+game.Modifier(wis))
	}
	if subs := subclassesByClass(charID); len(levels) > 1 && subs["sorcerer"] == "draconic" {
		subclass, level = "draconic", levels["sorcerer"]
	}
	if strings.ToLower(subclass) == "draconic" && level >= 3 {
		var cha int
		db.QueryRow(`SELECT cha FROM characters WHERE id = $1`, charID).Scan(&cha)
		return maxInt(unarmored, 10+game.Modifier(cha)) // only one base AC calculation: the better one
	}
	return maxInt(unarmored, getNaturalACBase(subclass, level))
}

// dragonWings is the Bonus Action of Draconic Sorcery (level 14): a Fly Speed of 60 feet for 1 hour. One use per Long Rest, or spend
// 3 Sorcery Points to use it again.
func dragonWings(charID int) string {
	var class, sub string
	var level int
	if db.QueryRow(`SELECT class, COALESCE(subclass,''), level FROM characters WHERE id = $1`, charID).Scan(&class, &sub, &level) != nil {
		return "Character not found."
	}
	if strings.ToLower(class) != "sorcerer" || strings.ToLower(sub) != "draconic" || level < 14 {
		return "Dragon Wings needs a Draconic Sorcerer of level 14 or higher."
	}
	pointsMax := classResourceMax(charID, class, level, "sorcery_points", 0)
	again := false
	// the use, or 3 Sorcery Points to use it again: one locked step (resources_2024.go)
	if _, _, err := changeSpent(charID, func(sp *spent) error {
		if sp.Uses["dragon_wings"] == 0 {
			sp.Uses["dragon_wings"] = 1
			return nil
		}
		again = true
		if err := sp.spend("sorcery_points", 3, pointsMax); err != nil {
			return &notEnough{"You already used Dragon Wings; spending 3 Sorcery Points to restore it failed: " + err.Error(), 0}
		}
		return nil
	}); err != nil {
		return spendText(err, "Dragon Wings")
	}
	if again {
		return "🐉 Dragon Wings: you spend 3 Sorcery Points to use it again; draconic wings appear and give you a Fly Speed of 60 feet for 1 hour."
	}
	return "🐉 Dragon Wings: draconic wings appear on your back for 1 hour, giving you a Fly Speed of 60 feet (dismiss them with no action)."
}

// pactSlotColumn: where a Warlock's spent Pact Magic slots are kept: pact_slots_used for a multiclass character, spell_slots_used for a
// single-class Warlock (the cast action spends them there).
func pactSlotColumn(charID int) string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class_levels,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	levels := map[string]int{}
	json.Unmarshal(raw, &levels)
	if len(levels) > 1 {
		return "pact_slots_used"
	}
	return "spell_slots_used"
}

// pactBladeWeapon: a Warlock with the Pact of the Blade invocation attacking with a melee weapon (the conjured or bonded pact weapon)
// uses Charisma for its attack and damage rolls and is proficient with it (SRD 5.2.1; F133).
func pactBladeWeapon(charID int, class string, hasWeapon bool, weapon SRDWeapon) bool {
	if !hasWeapon || weapon.Type == "ranged" {
		return false
	}
	if !strings.EqualFold(class, "warlock") && classLevelsOf(charID)["warlock"] == 0 {
		return false
	}
	return hasPactBoon(charID, "blade")
}

// killEffectNote: what reducing a creature to 0 Hit Points gives the character (Dark One's Blessing), as reply text (F134: only the
// GM's endpoints applied it).
func killEffectNote(charID int) string {
	if k := applyKillEffects(charID); k != nil {
		if m, ok := k["message"].(string); ok {
			return " " + m
		}
	}
	return ""
}

// monsterHPNow: a combatant's current Hit Points in the turn order (1 when it isn't found).
func monsterHPNow(lobbyID, id int) int {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			return asInt(e["hp"])
		}
	}
	return 1
}
