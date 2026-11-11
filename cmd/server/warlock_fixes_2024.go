package main

// Fixes from Morwen's coverage session (level20-battle-tests/s30_morwen_warlock.py, Nov 9 2026; FINDINGS F132-F137):
//	Pact Magic slots       a single-class Warlock spends them from spell_slots_used; Magical Cunning and Eldritch Smite read
//	                       pact_slots_used (F132, sorcerer_warlock_2024.go pactSlotColumn)
//	Pact of the Blade      Charisma for the pact weapon's attack and damage rolls, and proficiency with it (F133, actions.go)
//	Dark One's Blessing    applied by the player's attacks and spells, not only the GM's endpoints (F134)
//	Wings of Flying        a Magic action: Fly Speed 60 feet for 1 hour (F135)
//	Bead of Force          a Magic action: 10-foot Sphere, DC 15 Dex or 5d4 Force and trapped in a sphere of force for 1 minute (F136)
//	Dust of Sneezing and Choking  the user and every creature within 30 feet: DC 15 Con or Incapacitated and suffocating (F137)

import (
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

func wingsOfFlying(charID int, it invItem) string {
	if _, ok := attunedTo(charID, "wings of flying"); !ok {
		return "Cannot use the Wings of Flying: you must be attuned to them (POST /api/characters/attune). Nothing was spent."
	}
	if itemStr(it, "location") != "worn" {
		return "Cannot use the Wings of Flying: wear the cloak first (equipment/move to \"worn\"). Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if e, on := charBuff(charID, "fly"); on && e.Spell == "Wings of Flying" {
		db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
		markDawnUsed(charID, it, "wings")
		return fmt.Sprintf("The wings fold back into a cloak (a Magic action). If you were aloft, you fall. They can't be used again for %d hours (1d12).", game.RollDie(12))
	}
	if dawnUsed(it, "wings") {
		return "Cannot use the Wings of Flying: they are recharging (1d12 hours after they last disappeared; the GM or a Long Rest brings them back). Nothing was spent."
	}
	addSpellEffect(lobby, charID, charID, "Wings of Flying", "fly", "", 600, "", 0)
	return "The cloak becomes a pair of wings (a Magic action): a Fly Speed of 60 feet for 1 hour (use it again to end it early; then 1d12 hours to recharge)."
}

// areaTargets: target_id and target_ids, or the battle map's creatures within feet of the token (centerID) when no ids are given.
func areaTargets(lobby, centerID, feet int, req itemCastReq, includeCenter bool) []int {
	ids := append([]int{}, req.TargetIDs...)
	if req.TargetID != 0 {
		ids = append([]int{req.TargetID}, ids...)
	}
	if len(ids) == 0 {
		if m, ok := activeEncounterMap(lobby); ok {
			if me, ok := m.token(centerID); ok {
				for _, t := range m.Tokens {
					if (t.ID != centerID || includeCenter) && tokenDistanceFeet(me, t) <= feet {
						ids = append(ids, t.ID)
					}
				}
			}
		}
	}
	return ids
}

func beadOfForce(charID int, it invItem, req itemCastReq) string {
	lobby := lobbyOfChar(charID)
	ids := areaTargets(lobby, req.TargetID, 10, req, true)
	if len(ids) == 0 {
		return "Name the creatures in the 10-foot Sphere with target_id / target_ids. Nothing was spent."
	}
	dmg := game.RollDamage("5d4", false)
	var lines []string
	for _, id := range ids {
		saved := false
		if id < 0 {
			saved, _ = monsterSave(lobby, id, "dex", 15)
		} else {
			saved, _ = characterSave(id, "dex", 15)
		}
		if saved {
			lines = append(lines, creatureName(lobby, id)+" saves.")
			continue
		}
		res := ""
		if id < 0 {
			res, _ = damageMonster(lobby, id, dmg, "force")
		} else {
			res = damageCharacter(id, dmg, "force")
		}
		addMonsterCondition(lobby, id, "restrained", "Bead of Force: trapped in a sphere of force", 10, "", 0)
		lines = append(lines, fmt.Sprintf("%s fails: %s, and is trapped in the sphere of force for 1 minute.", creatureName(lobby, id), strings.TrimSpace(res)))
	}
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				return takeOne(inv, x)
			}
		}
		return inv
	})
	return fmt.Sprintf("You throw the Bead of Force (a Magic action): it bursts in a 10-foot-radius Sphere (DC 15 Dexterity save, %d Force damage on a failure, 5d4). %s Nothing can pass the sphere's wall; a trapped creature can push it at half Speed. The bead is gone.",
		dmg, strings.Join(lines, " "))
}

var dustImmuneTypes = map[string]bool{"construct": true, "elemental": true, "ooze": true, "plant": true, "undead": true}

func dustOfSneezing(charID int, it invItem, req itemCastReq) string {
	lobby := lobbyOfChar(charID)
	ids := areaTargets(lobby, charID, 30, itemCastReq{TargetIDs: req.TargetIDs}, true)
	have := map[int]bool{}
	for _, id := range ids {
		have[id] = true
	}
	if !have[charID] {
		ids = append([]int{charID}, ids...)
	}
	var lines []string
	for _, id := range ids {
		if id < 0 && dustImmuneTypes[strings.ToLower(monsterTypeOf(lobby, id))] {
			lines = append(lines, creatureName(lobby, id)+" succeeds automatically.")
			continue
		}
		saved := false
		if id < 0 {
			saved, _ = monsterSave(lobby, id, "con", 15)
		} else {
			saved, _ = characterSave(id, "con", 15)
		}
		if saved {
			lines = append(lines, creatureName(lobby, id)+" saves.")
			continue
		}
		addMonsterCondition(lobby, id, "incapacitated", "Dust of Sneezing and Choking (suffocating)", 10, "con", 15)
		lines = append(lines, creatureName(lobby, id)+" fails: Incapacitated and suffocating (a DC 15 Con save at the end of each of its turns ends it).")
	}
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				return takeOne(inv, x)
			}
		}
		return inv
	})
	return "You throw the dust into the air (a Utilize action; it is used up): you and every creature within 30 feet make a DC 15 Constitution save (Constructs, Elementals, Oozes, Plants and Undead succeed). " + strings.Join(lines, " ")
}

// fumbleHitRiders: a natural 1 turned into a hit (Peerless Aim) rolled none of the on-hit extras; the Warlock's are added here (F138).
func fumbleHitRiders(charID int, weaponName string) (int, string) {
	class, _, level := subclassOf(charID)
	v, _ := attackDescriptions.Load(charID)
	desc, _ := v.(string)
	ranged := false
	if w, ok := srdWeapons[strings.ToLower(baseWeaponOf(weaponName+" ("+weaponName+")"))]; ok {
		ranged = w.Type == "ranged"
	}
	total, notes := 0, ""
	if d, n := lifedrinker2024(charID, class, level, ranged, false, desc); d > 0 {
		total, notes = total+d, notes+n
	}
	if d, n := eldritchSmite(charID, class, level, strings.ToLower(desc), false); n != "" {
		total, notes = total+d, notes+n
	}
	return total, notes
}

// F147: Lifedrinker and Primal Strike marked their once-a-turn use when the damage was rolled, before the hit was known: a miss
// used them up for the turn. The use now waits for the hit (like Frenzy).
var onceAttackPending, primalPending sync.Map // charID -> true while the attack that used Lifedrinker / Primal Strike is unresolved

func refundOnceAttack(charID int) {
	if _, ok := onceAttackPending.LoadAndDelete(charID); ok {
		lifedrinkerTurn.Delete(charID)
	}
	if _, ok := primalPending.LoadAndDelete(charID); ok {
		db.Exec(`UPDATE characters SET primal_strike_used = false WHERE id = $1`, charID)
	}
}

func keepOnceAttack(charID int) {
	onceAttackPending.Delete(charID)
	primalPending.Delete(charID)
}
