package main

// Fixes from Mei's coverage session (level20-battle-tests/s24_mei_monk.py, Nov 8 2026; FINDINGS F105-F109):
//	Unarmored Movement     the class table's "+10 ft." read as 0, so no Monk got the Speed (F105, game/class_tables.go)
//	Unarmed Strike         "unarmed" in an attack's description is an Unarmed Strike even with a weapon in hand (F106, actions.go)
//	Boots of Speed         a Bonus Action doubles the Speed (and says Opportunity Attacks against the wearer have Disadvantage); 10
//	                       minutes in all until a Long Rest (F107)
//	Dust of Disappearance  the user and every creature within 10 feet are Invisible for 2d4 minutes; the dust is used up (F108)
//	Oil of Slipperiness    the effect of Freedom of Movement for 8 hours (F109)
//	Empowered Strikes      an Unarmed Strike of the Attack action deals Force damage when the description says "force" (F110)

import (
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

func init() {
	noConcentration["boots_of_speed"] = true
	spellEffectLabel["boots_of_speed"] = "Boots of Speed"
	spellEffectText["boots_of_speed"] = "Speed doubled; Opportunity Attacks against the wearer have Disadvantage"
}

// bootsOfSpeed toggles the boots (a Bonus Action). The time they run is kept on the item: 100 rounds (10 minutes) until the next dawn.
func bootsOfSpeed(charID int, it invItem) string {
	if _, ok := attunedTo(charID, "boots of speed"); !ok {
		return "Cannot use the Boots of Speed: you must be attuned to them (POST /api/characters/attune). Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if e, on := charBuff(charID, "boots_of_speed"); on {
		db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
		left := e.RoundsLeft
		editInventory(charID, func(inv []invItem) []invItem {
			for _, x := range inv {
				if itemStr(x, "id") == itemStr(it, "id") {
					x["speed_rounds_left"] = left
				}
			}
			return inv
		})
		return fmt.Sprintf("You click the heels together again: the Boots of Speed stop (%d rounds of their 10 minutes left until the next dawn).", left)
	}
	if dawnUsed(it, "boots") {
		return "Cannot use the Boots of Speed: their 10 minutes are used up until you finish a Long Rest. Nothing was spent."
	}
	left := int(itemNum(it, "speed_rounds_left"))
	if left <= 0 {
		left = 100
	}
	addSpellEffect(lobby, charID, charID, "Boots of Speed", "boots_of_speed", "", left, "", 0)
	extra := characterSpeedOf(charID) / 2 // the doubled Speed's other half this turn
	if inActiveCombat(lobby) {
		db.Exec(`UPDATE characters SET movement_remaining = COALESCE(movement_remaining,0) + $1 WHERE id = $2`, extra, charID)
	}
	if left <= 1 {
		markDawnUsed(charID, it, "boots")
	}
	return fmt.Sprintf("You click the heels together (a Bonus Action): your Speed is doubled (+%d feet this turn), and any creature that makes an Opportunity Attack against you has Disadvantage. Click them again to stop; %d rounds left of their 10 minutes until the next dawn.", extra, left)
}

// bootsSpeed doubles the Speed while the boots run.
func bootsSpeed(charID, speed int) int {
	if _, on := charBuff(charID, "boots_of_speed"); on {
		return speed * 2
	}
	return speed
}

// dustOfDisappearance: a Utilize action; the user and every creature within 10 feet (the battle map; without one, the user and the
// creatures named in target_ids) are Invisible for 2d4 minutes. Each one's Invisibility ends after it attacks, deals damage or casts.
func dustOfDisappearance(charID int, it invItem, req itemCastReq) string {
	lobby := lobbyOfChar(charID)
	minutes := game.RollDamage("2d4", false)
	ids := []int{charID}
	if m, ok := activeEncounterMap(lobby); ok {
		if me, ok := m.token(charID); ok {
			for _, t := range m.Tokens {
				if t.ID != charID && tokenDistanceFeet(me, t) <= 10 {
					ids = append(ids, t.ID)
				}
			}
		}
	} else {
		ids = append(ids, req.TargetIDs...)
	}
	var names []string
	for _, id := range ids {
		addMonsterCondition(lobby, id, "invisible", "Dust of Disappearance", minutes*10, "", 0)
		names = append(names, creatureName(lobby, id))
	}
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				return takeOne(inv, x)
			}
		}
		return inv
	})
	return fmt.Sprintf("You throw the Dust of Disappearance into the air (a Utilize action; the dust is gone): %s %s Invisible for %d minutes (2d4). Each loses it right after making an attack roll, dealing damage or casting a spell.",
		joinNames(names), map[bool]string{true: "is", false: "are"}[len(names) == 1], minutes)
}

// oilOfSlipperiness: the coated creature gains the effect of Freedom of Movement for 8 hours (applying it takes 10 minutes).
func oilOfSlipperiness(lobby, targetID int, label string) string {
	addSpellEffect(lobby, targetID, targetID, "Oil of Slipperiness", "freedom_of_movement", "", 4800, "", 0)
	return fmt.Sprintf("🧪 %s (10 minutes to apply): the effect of Freedom of Movement for 8 hours: magic can't Paralyze, Restrain or slow the creature, Difficult Terrain costs nothing extra, and it can spend 5 feet of movement to escape nonmagical restraints. (Poured on the ground instead, it makes a 10-foot square slippery: the GM runs it.)", label)
}

// unarmedWanted: the attack's description asks for an Unarmed Strike.
func unarmedWanted(description string) bool {
	d := strings.ToLower(description)
	return hasWord(d, "unarmed") || hasWord(d, "punch") || hasWord(d, "kick") // whole words (feature_words_2024.go)
}

var attackDescriptions sync.Map // charID -> the description of the attack being resolved

// empoweredStrike: a Monk of level 6 chooses Force damage for an Unarmed Strike ("force" in the description).
func empoweredStrike(charID int, o *attackOutcome) {
	if o.Weapon != "unarmed" {
		return
	}
	v, _ := attackDescriptions.Load(charID)
	d, _ := v.(string)
	if !hasWord(d, "force") { // "a forceful punch" is not Force damage
		return
	}
	class, _, level := subclassOf(charID)
	if strings.EqualFold(class, "monk") && level >= 6 {
		o.DamageType = "force"
	}
}
