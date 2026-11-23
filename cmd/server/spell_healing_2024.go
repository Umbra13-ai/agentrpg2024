package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// spell_healing_2024.go: the one reading of how much a character's healing spell heals: the spell's dice (spellHealDice), the spellcasting
// modifier where the spell adds it, Supreme Healing (the highest number on each die), Disciple of Life (2 + the slot's level) and Blessed Healer.
// The ordinary healing path (actions.go) and Regenerate (spells_more_2024.go, which also starts its per-turn healing) both call it (FUNC-04: Regenerate
// rolled its own dice and ignored the Life Cleric's features).

// healingOutcome: what a cast heals and what to say about it.
type healingOutcome struct {
	Heal, Max int    // the amount, and the amount at its maximum (Beacon of Hope)
	Info      string // " (Supreme Healing + Disciple of Life: +9)"
	Blessed   string // " Blessed Healer: you also heal 5 HP!"
}

func casterHealing(charID int, spell SRDSpell, slotLevel, level, spellMod int, subclass, description string) healingOutcome {
	// Life Domain Cleric healing bonuses
	bonusInfo := ""
	heal := 0
	subclassSlug := subclass
	healDice, addsMod := spellHealDice(spell, slotLevel) // the healing at this slot level and whether the modifier is added

	// the spellcasting ability modifier only where the spell says so (Cure Wounds, Healing Word; not Heal or Regenerate)
	healMod := 0
	if addsMod {
		healMod = spellMod
	}
	if flat, err := strconv.Atoi(strings.TrimSpace(healDice)); err == nil {
		// a fixed amount: Heal restores 70 Hit Points (80, 90, 100 with higher slots: heal_at_slot_level); no dice to roll or maximize
		heal = flat
	} else if hasSubclassFeature(subclassSlug, level, "supreme_healing") {
		// Supreme Healing (Life Domain level 17): the highest number on each die instead of rolling
		heal = game.RollDamageMax(healDice) + game.FlatBonus(healDice) + healMod
		bonusInfo = " (Supreme Healing: max dice)"
	} else {
		heal = game.RollDamage(healDice, false) + healMod
	}
	healMax := heal // Beacon of Hope: healing at its maximum (applySpellHealing)
	if _, err := strconv.Atoi(strings.TrimSpace(healDice)); err != nil {
		healMax = game.RollDamageMax(healDice) + game.FlatBonus(healDice) + healMod
	}

	// Check for Disciple of Life (Life Domain level 1) - add 2 + spell level bonus
	// Only applies to spells of 1st level or higher
	if slotLevel >= 1 && hasSubclassFeature(subclassSlug, level, "bonus_healing") {
		bonusHealing := 2 + slotLevel
		heal += bonusHealing
		healMax += bonusHealing
		if bonusInfo != "" {
			bonusInfo = fmt.Sprintf(" (Supreme Healing + Disciple of Life: +%d)", bonusHealing)
		} else {
			bonusInfo = fmt.Sprintf(" (Disciple of Life: +%d)", bonusHealing)
		}
	}

	// Blessed Healer (Life Domain level 6) - heal self when healing others
	// When you cast a healing spell on a creature other than yourself, you regain 2 + spell level HP
	blessedHealerInfo := ""
	if slotLevel >= 1 && hasSubclassFeature(subclassSlug, level, "blessed_healer") {
		// another creature healed: the same aim the healing reads (spellAllyAim, living_target_2024.go: F83, I-CALLA-24)
		aim := spellAllyAim(charID, description)
		healingSelf := !aim.Others && len(aim.Combatants) == 0
		for _, t := range aim.IDs {
			if t != charID {
				healingSelf = false
			}
		}

		if !healingSelf {
			// Healing another creature - heal self too
			if h, err := healHP(charID, 2+slotLevel); err == nil && h.Healed > 0 {
				blessedHealerInfo = fmt.Sprintf(" Blessed Healer: you also heal %d HP!", h.Healed)
			}
		}
	}
	return healingOutcome{Heal: heal, Max: healMax, Info: bonusInfo, Blessed: blessedHealerInfo}
}
