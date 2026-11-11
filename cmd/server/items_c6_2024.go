package main

// Fixes from the C6 coverage session (level20-battle-tests/s37_c6.py, Nov 9 2026; FINDINGS F175-F182):
//	Pipes of Haunting            a Magic action, 1 charge: creatures of your choice within 30 feet, DC 15 Wis or Frightened for 1 minute (F175)
//	Robe of Scintillating Colors a Magic action, 1 charge, until the end of your next turn: attacks against you have Disadvantage; creatures that
//	                             see it activate make a DC 15 Wis save or are Stunned until it ends (F176)
//	Energy Bow                   no ammunition: an arrow of golden energy deals Force damage (F177)
//	Ring of the Ram              a Magic action, 1-3 charges: a +7 ranged spell attack, 2d10 Force and 5 feet of push per charge (F178)
//	Ring of Shooting Stars       Shooting Stars (1-3 charges: a 15-foot Cube each, DC 15 Dex, 5d4 Radiant) and Lightning Spheres (2 charges)
//	                             through use_item; Faerie Fire, Dancing Lights and Light were already cast with "item" (F179)
//	Grapple and Shove            the player actions "grapple" and "shove" spent the action and did nothing (only the GM's endpoints worked);
//	                             they now replace one attack of the Attack action (F180)
//	Grappler                     Advantage on attack rolls against a creature you have Grappled (F181)
//	Fleet Step                   the reply says Step of the Wind took no Bonus Action (F182)

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func init() {
	noConcentration["scintillating"] = true
	spellEffectLabel["scintillating"] = "Robe of Scintillating Colors"
	spellEffectText["scintillating"] = "Dazzling hues: attacks against the wearer have Disadvantage"
}

// chargedUse spends n charges of the item (inside the inventory edit spendCharges needs) and returns its note or a refusal.
func chargedUse(charID int, it invItem, n int) (string, string) {
	var note, refusal string
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				note, refusal = spendCharges(charID, x, n)
			}
		}
		return inv
	})
	return note, refusal
}

// saveOrDamage: every creature makes the save; damage (half on a success) and a condition on a failure.
func saveOrDamage(lobby int, ids []int, ability string, dc int, dice, dtype, cond, source string, condRounds int, condSave bool) (int, []string) {
	dmg := 0
	if dice != "" {
		dmg = game.RollDamage(dice, false)
	}
	var lines []string
	for _, id := range ids {
		var saved bool
		if id < 0 {
			saved, _ = monsterSave(lobby, id, ability, dc)
		} else {
			saved, _ = characterSave(id, ability, dc)
		}
		line := creatureName(lobby, id) + map[bool]string{true: " saves", false: " fails"}[saved]
		if dmg > 0 {
			x := dmg
			if saved {
				x /= 2
			}
			var res string
			if id < 0 {
				res, _ = damageMonster(lobby, id, x, dtype)
			} else {
				res = damageCharacter(id, x, dtype)
			}
			line += ": " + strings.TrimSpace(res)
		}
		if !saved && cond != "" {
			saveAb, saveDC := "", 0
			if condSave {
				saveAb, saveDC = ability, dc
			}
			addMonsterCondition(lobby, id, cond, source, condRounds, saveAb, saveDC)
			line += " and is " + titleCase(cond)
		}
		lines = append(lines, line+".")
	}
	return dmg, lines
}

func pipesOfHaunting(charID int, it invItem, req itemCastReq) string {
	lobby := lobbyOfChar(charID)
	ids := areaTargets(lobby, charID, 30, req, false)
	if len(ids) == 0 {
		return "Name the creatures of your choice within 30 feet with target_ids. Nothing was spent."
	}
	note, refusal := chargedUse(charID, it, 1)
	if refusal != "" {
		return refusal + " Nothing was spent."
	}
	_, lines := saveOrDamage(lobby, ids, "wis", 15, "", "", "frightened", "Pipes of Haunting", 10, true)
	return "You play the Pipes of Haunting (a Magic action, 1 charge): an eerie, spellbinding tune. DC 15 Wisdom save or Frightened for 1 minute (a save at the end of each of its turns ends it): " +
		strings.Join(lines, " ") + note
}

func robeOfScintillatingColors(charID int, it invItem, req itemCastReq) string {
	if _, ok := itemActive(charID, "robe of scintillating colors", "worn"); !ok {
		return "Cannot use the Robe of Scintillating Colors: wear it and attune to it first. Nothing was spent."
	}
	note, refusal := chargedUse(charID, it, 1)
	if refusal != "" {
		return refusal + " Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	addSpellEffect(lobby, charID, charID, "Robe of Scintillating Colors", "scintillating", "", 2, "", 0)
	ids := areaTargets(lobby, charID, 30, req, false)
	_, lines := saveOrDamage(lobby, ids, "wis", 15, "", "", "stunned", "Robe of Scintillating Colors", 2, false)
	text := "The robe blazes with dazzling hues until the end of your next turn (a Magic action, 1 charge): Bright Light 30 feet, Dim Light 30 more, and creatures that can see you have Disadvantage on attack rolls against you."
	if len(lines) > 0 {
		text += " Creatures in the Bright Light that see it activate (DC 15 Wisdom save or Stunned until it ends): " + strings.Join(lines, " ")
	}
	return text + note
}

// robeDazzle: attacks against a creature whose robe shines have Disadvantage.
func robeDazzle(lobbyID, targetID int) bool {
	if targetID <= 0 {
		return false
	}
	_, on := buffEffect(lobbyID, targetID, "scintillating")
	return on
}

// energyBowShot: the attack is with the attuned Energy Bow (no ammunition; Force damage).
func energyBowShot(charID int, weaponKey string) bool {
	held := strings.ToLower(heldItemFor(charID, weaponKey, false))
	if !strings.HasPrefix(held, "energy bow") {
		return false
	}
	_, ok := attunedTo(charID, "energy bow")
	return ok
}

func ringOfTheRam(charID int, it invItem, req itemCastReq, description string) string {
	if _, ok := itemActive(charID, "ring of the ram", "worn"); !ok {
		return "Cannot use the Ring of the Ram: wear it and attune to it first. Nothing was spent."
	}
	if req.TargetID == 0 {
		return "Name the creature within 60 feet with target_id (and \"N charges\", 1 to 3). Nothing was spent."
	}
	n := 1
	if m := beadsRe.FindStringSubmatch(strings.ReplaceAll(strings.ToLower(description), "charges", "beads")); m != nil {
		fmt.Sscanf(m[1], "%d", &n)
	}
	if n < 1 || n > 3 {
		n = 1
	}
	note, refusal := chargedUse(charID, it, n)
	if refusal != "" {
		return refusal + " Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	ac := 10
	for _, e := range combatEntries(lobby) {
		if asInt(e["id"]) == req.TargetID {
			ac = asInt(e["ac"])
		}
	}
	if req.TargetID > 0 {
		db.QueryRow(`SELECT ac FROM characters WHERE id = $1`, req.TargetID).Scan(&ac)
	}
	d20 := game.RollDie(20)
	total := d20 + 7
	who := creatureName(lobby, req.TargetID)
	if d20 == 1 || (d20 != 20 && total < ac) {
		return fmt.Sprintf("A spectral ram's head charges from the ring (a Magic action, %d charge%s): %d to hit (+7) against %s - MISS.%s", n, map[bool]string{true: "", false: "s"}[n == 1], total, who, note)
	}
	dmg := game.RollDamage(fmt.Sprintf("%dd10", 2*n), d20 == 20)
	var res string
	if req.TargetID < 0 {
		res, _ = damageMonster(lobby, req.TargetID, dmg, "force")
	} else {
		res = damageCharacter(req.TargetID, dmg, "force")
	}
	moved, onMap := pushToken(lobby, charID, req.TargetID, 5*n)
	push := fmt.Sprintf(" It is pushed %d feet away.", 5*n)
	if onMap {
		push = fmt.Sprintf(" It is pushed %d feet away.", moved)
	}
	return fmt.Sprintf("A spectral ram's head charges from the ring (a Magic action, %d charge%s): %d to hit (+7) - HIT, %dd10 = %d Force. %s.%s%s", n, map[bool]string{true: "", false: "s"}[n == 1],
		total, 2*n, dmg, strings.TrimSpace(res), push, note)
}

func ringOfShootingStars(charID int, it invItem, req itemCastReq, description string) string {
	if _, ok := itemActive(charID, "ring of shooting stars", "worn"); !ok {
		return "Cannot use the Ring of Shooting Stars: wear it and attune to it first. Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	d := strings.ToLower(description)
	ids := append([]int{}, req.TargetIDs...)
	if req.TargetID != 0 {
		ids = append([]int{req.TargetID}, ids...)
	}
	switch {
	case strings.Contains(d, "lightning") || strings.Contains(d, "sphere"):
		note, refusal := chargedUse(charID, it, 2)
		if refusal != "" {
			return refusal + " Nothing was spent."
		}
		spheres := 4
		for k, v := range map[string]int{"1 sphere": 1, "one sphere": 1, "2 spheres": 2, "two spheres": 2, "3 spheres": 3, "three spheres": 3} {
			if strings.Contains(d, k) {
				spheres = v
			}
		}
		dice := map[int]string{1: "4d12", 2: "5d4", 3: "2d6", 4: "2d4"}[spheres]
		text := fmt.Sprintf("You create %d sphere%s of lightning within 120 feet (a Magic action, 2 charges; Concentration up to 1 minute; a Bonus Action moves each 30 feet). The first time a sphere comes within 5 feet of a creature, it discharges: DC 15 Dexterity save, %s Lightning (half on a success).",
			spheres, map[bool]string{true: "", false: "s"}[spheres == 1], dice)
		if len(ids) > 0 {
			_, lines := saveOrDamage(lobby, ids, "dex", 15, dice, "lightning", "", "", 0, false)
			text += " Discharges now: " + strings.Join(lines, " ")
		}
		return text + note
	default:
		n := 1
		for k, v := range map[string]int{"2 charges": 2, "two charges": 2, "3 charges": 3, "three charges": 3, "2 motes": 2, "3 motes": 3} {
			if strings.Contains(d, k) {
				n = v
			}
		}
		note, refusal := chargedUse(charID, it, n)
		if refusal != "" {
			return refusal + " Nothing was spent."
		}
		text := fmt.Sprintf("You launch %d glowing mote%s of light from the ring (a Magic action, %d charge%s): each showers a 15-foot Cube in sparks (DC 15 Dexterity save, 5d4 Radiant, half on a success).",
			n, map[bool]string{true: "", false: "s"}[n == 1], n, map[bool]string{true: "", false: "s"}[n == 1])
		if len(ids) == 0 {
			return text + " (Name the creatures in the Cubes with target_ids.)" + note
		}
		for i := 0; i < n; i++ {
			_, lines := saveOrDamage(lobby, ids, "dex", 15, "5d4", "radiant", "", "", 0, false)
			text += fmt.Sprintf(" Mote %d: %s", i+1, strings.Join(lines, " "))
		}
		return text + note
	}
}

// ---------------------------------------------------------------- Grapple, Shove, Grappler

// resolveUnarmedOption: Grapple or Shove (SRD 5.2.1 Unarmed Strike options): the target (no more than one size larger, within 5 feet)
// makes a Strength or Dexterity save (the better) against 8 + Strength modifier + Proficiency Bonus.
func resolveUnarmedOption(lobby, attackerID, targetID int, kind, effect string) (string, bool, map[string]interface{}) {
	aName, tName := creatureName(lobby, attackerID), creatureName(lobby, targetID)
	aStr, _, aPB, aSize, _ := creatureStats(lobby, attackerID)
	_, _, _, tSize, _ := creatureStats(lobby, targetID)
	if tooBigToGrab(aSize, tSize) {
		return fmt.Sprintf("%s (%s) is more than one size larger than %s (%s): it can't be %sd.", tName, tSize, aName, aSize, kind), false, nil
	}
	if d, known := creatureDistance(lobby, attackerID, targetID); known && d > 5 {
		return fmt.Sprintf("%s is %d feet away; an Unarmed Strike reaches 5 feet.", tName, d), false, nil
	}
	dc := 8 + game.Modifier(aStr) + aPB
	saved, total, ability := unarmedOptionSave(lobby, targetID, dc)
	result := fmt.Sprintf("%s tries to %s %s: %s saving throw %d vs DC %d, ", aName, kind, tName, ability, total, dc)
	out := map[string]interface{}{"save": total, "save_ability": ability, "dc": dc}
	switch {
	case saved:
		result += "success. Nothing happens."
	case kind == "grapple":
		esc, _ := jsonMarshalString([]conditionEscape{
			{Label: "Strength (Athletics) check", Ability: "Strength", Skill: "Athletics", DC: dc},
			{Label: "Dexterity (Acrobatics) check", Ability: "Dexterity", Skill: "Acrobatics", DC: dc},
		})
		db.Exec(`INSERT INTO condition_records (lobby_id, target_id, condition, source, escapes, duration_rounds, notes) VALUES ($1,$2,'grappled',$3,$4,0,$5)`,
			lobby, targetID, grappleSource(attackerID), esc, fmt.Sprintf("Grappled by %s; escape DC %d", aName, dc))
		if targetID > 0 {
			syncListFromRecords(targetID)
		}
		result += fmt.Sprintf("%s. %s has the Grappled condition (escape DC %d: an action for a Strength (Athletics) or Dexterity (Acrobatics) check).", failWord(total, dc), tName, dc)
		out["escape_dc"] = dc
	case effect == "prone":
		applyCondition(lobby, targetID, "prone", "Shove", 0, "", 0)
		result += fmt.Sprintf("failure. %s has the Prone condition.", tName)
	default:
		moved, onMap := pushToken(lobby, attackerID, targetID, 5)
		if onMap {
			result += fmt.Sprintf("failure. %s is pushed %d feet away.", tName, moved)
		} else {
			result += fmt.Sprintf("failure. %s is pushed 5 feet away.", tName)
		}
	}
	out["success"], out["result"] = !saved, result
	return result, !saved, out
}

// playerUnarmedOption: the player's "grapple" or "shove" action against target_id (one attack of the Attack action).
func playerUnarmedOption(charID int, kind, description string) string {
	lobby, target, ok := attackedMonster(charID)
	if !ok {
		return fmt.Sprintf("Name the creature to %s with target_id. Nothing was spent.", kind)
	}
	effect := "push"
	if strings.Contains(strings.ToLower(description), "prone") {
		effect = "prone"
	}
	result, _, _ := resolveUnarmedOption(lobby, charID, target, kind, effect)
	return result
}

// grapplerAdvantage: the Grappler feat's Attack Advantage against a creature the attacker has Grappled.
func grapplerAdvantage(charID int) bool {
	if !hasSpecificFeat(charID, "grappler") {
		return false
	}
	lobby, target, ok := attackedMonster(charID)
	if !ok {
		return false
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = 'grappled' AND source = $3`, lobby, target, grappleSource(charID)).Scan(&n)
	return n > 0
}

func jsonMarshalString(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// energyBowHeld: the attack being resolved is with the Energy Bow (its arrows deal Force damage).
func energyBowHeld(charID int) bool {
	v, _ := attackHeldItem.Load(charID)
	h, _ := v.(string)
	return strings.HasPrefix(strings.ToLower(h), "energy bow")
}

// glovesOfMissileSnaring (F183): a Reaction when a ranged or thrown weapon attack hits you, with a free hand: the damage is reduced by
// 1d10 + Dexterity modifier (the latest such hit this turn); reduced to 0, you can catch the missile.
func glovesOfMissileSnaring(charID int) string {
	if _, ok := itemActive(charID, "gloves of missile snaring", "worn"); !ok {
		return "Cannot use the Gloves of Missile Snaring: wear them and attune to them first. Nothing was spent."
	}
	if handsOf(charID).free() == 0 {
		return "The Gloves of Missile Snaring need a free hand. Nothing was spent."
	}
	v, ok := monsterHitsOn.Load(charID)
	if !ok {
		return "No ranged or thrown attack has hit you this turn: nothing to snare. Nothing was spent."
	}
	th := v.(turnHits)
	if th.Marker == "" || th.Marker != combatTurnMarker(th.Lobby) {
		return "No ranged or thrown attack has hit you this turn: nothing to snare. Nothing was spent."
	}
	idx := -1
	for i := len(th.Hits) - 1; i >= 0; i-- {
		if th.Hits[i].Ranged && !th.Hits[i].Snared {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "No ranged or thrown attack has hit you this turn: nothing to snare. Nothing was spent."
	}
	h := th.Hits[idx]
	var dex int
	db.QueryRow(`SELECT dex FROM characters WHERE id = $1`, charID).Scan(&dex)
	d10 := game.RollDie(10)
	reduce := d10 + game.Modifier(dex)
	if reduce < 0 {
		reduce = 0
	}
	lost := (h.HPBefore - h.HPAfter) + (h.TempBefore - h.TempAfter)
	back := reduce
	if back > lost {
		back = lost
	}
	var hp, temp int
	db.QueryRow(`SELECT hp, COALESCE(temp_hp,0) FROM characters WHERE id = $1`, charID).Scan(&hp, &temp)
	toHP := back
	if toHP > h.HPBefore-h.HPAfter {
		toHP = h.HPBefore - h.HPAfter
	}
	db.Exec(`UPDATE characters SET hp = hp + $1, temp_hp = COALESCE(temp_hp,0) + $2 WHERE id = $3`, toHP, back-toHP, charID)
	th.Hits[idx].Snared = true
	monsterHitsOn.Store(charID, th)
	text := fmt.Sprintf("You snare the missile (a Reaction): the damage of %s's %s is reduced by %d (1d10 %d + Dex %+d); %d Hit Points back.", h.Attacker, h.Attack, reduce, d10, game.Modifier(dex), back)
	if reduce >= lost {
		text += " That brings it to 0: you catch the ammunition or weapon if it fits in your hand."
	}
	return text
}
