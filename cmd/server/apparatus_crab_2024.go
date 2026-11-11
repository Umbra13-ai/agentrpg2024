package main

// Apparatus of the Crab (SRD 5.2.1, Legendary; O-2: it was a page only). A 500-pound iron barrel two Medium or smaller creatures can
// crawl into; its levers make it a giant lobster, a Large object: AC 20; HP 200; Speed 30 ft., Swim 30 ft. (0 with the legs retracted);
// Immunity to Poison and Psychic damage. A creature inside moves up to two levers with a Utilize action:
//   use_item "lever 1 up, lever 4 up"
// Each lever goes back to neutral after use; the item keeps what the levers changed (legs, shutters, claws, light, hatch). Lever 5 makes
// each extended claw attack (+8, reach 5 ft.) the target_id monster: up, 2d6 Bludgeoning; down, the Grappled condition (escape DC 15).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var crabLeverRe = regexp.MustCompile(`(?i)lever\s*(\d{1,2})\s*(up|down)`)

var crabLeverHelp = "levers 1 legs (up extends: Speed 30 ft., Swim 30 ft.; down retracts: 0), 2 forward shutter, 3 side shutters, 4 claws, " +
	"5 claw attacks (up: 2d6 Bludgeoning; down: Grappled, escape DC 15; name the target_id), 6 walk or swim forward (up) or back (down), " +
	"7 turn 90 degrees, 8 light, 9 sink (up) or rise (down) 20 feet in liquid, 10 hatch (up opens, down seals)"

func crabStatus(it invItem) string {
	onOff := func(k, on, off string) string {
		if itemBool(it, k) {
			return on
		}
		return off
	}
	hp := 200
	if v, ok := it["crab_hp"]; ok {
		hp = asInt(v)
	}
	return fmt.Sprintf("The apparatus: %s; %s; %s; %s; %s; light %s. A Large object: AC 20, %d/200 HP, Immunity to Poison and Psychic damage (the GM applies damage to it).",
		onOff("crab_legs", "legs extended (Speed 30 ft., Swim 30 ft.)", "legs retracted (Speed 0)"),
		onOff("crab_claws", "claws extended", "claws retracted"),
		onOff("crab_forward", "forward shutter open", "forward shutter closed"),
		onOff("crab_side", "side shutters open", "side shutters closed"),
		onOff("crab_hatch", "hatch open", "hatch sealed (airtight and watertight)"),
		onOff("crab_light", "on (Bright Light 30 ft., Dim Light 30 ft. more)", "off"), hp)
}

// apparatusOfTheCrab works the levers (use_item on the Apparatus of the Crab).
func apparatusOfTheCrab(charID int, it invItem, req itemCastReq, description string) string {
	moves := crabLeverRe.FindAllStringSubmatch(description, -1)
	if len(moves) == 0 {
		return "Cannot work the Apparatus of the Crab: name up to two levers for one Utilize action, such as use_item \"lever 1 up, lever 4 up\" (" + crabLeverHelp + "). Nothing moved. " + crabStatus(it)
	}
	if len(moves) > 2 {
		return "Cannot move more than two levers with one Utilize action. Nothing moved."
	}
	lobby := lobbyOfChar(charID)
	set := map[string]bool{}
	var out []string
	claws := itemBool(it, "crab_claws")
	legs := itemBool(it, "crab_legs")
	for _, m := range moves {
		n, _ := strconv.Atoi(m[1])
		up := strings.EqualFold(m[2], "up")
		switch n {
		case 1:
			set["crab_legs"], legs = up, up
			out = append(out, map[bool]string{true: "Lever 1: the legs extend; the apparatus can walk and swim (Speed 30 ft., Swim 30 ft.).",
				false: "Lever 1: the legs retract; its Speed and Swim Speed are 0 and it can't benefit from bonuses to speed."}[up])
		case 2:
			set["crab_forward"] = up
			out = append(out, map[bool]string{true: "Lever 2: the forward window shutter opens.", false: "Lever 2: the forward window shutter closes."}[up])
		case 3:
			set["crab_side"] = up
			out = append(out, map[bool]string{true: "Lever 3: the side window shutters open (two per side).", false: "Lever 3: the side window shutters close."}[up])
		case 4:
			set["crab_claws"], claws = up, up
			out = append(out, map[bool]string{true: "Lever 4: two claws extend from the front.", false: "Lever 4: the claws retract."}[up])
		case 5:
			out = append(out, crabClaws(lobby, claws, up, req))
		case 6:
			if !legs {
				out = append(out, "Lever 6: nothing happens: the legs aren't extended (lever 1 up).")
			} else {
				out = append(out, map[bool]string{true: "Lever 6: the apparatus walks or swims forward (up to 30 feet; move its token on the battle map).",
					false: "Lever 6: the apparatus walks or swims backward (up to 30 feet; move its token on the battle map)."}[up])
			}
		case 7:
			if !legs {
				out = append(out, "Lever 7: nothing happens: the legs aren't extended (lever 1 up).")
			} else {
				out = append(out, map[bool]string{true: "Lever 7: the apparatus turns 90 degrees counterclockwise.", false: "Lever 7: the apparatus turns 90 degrees clockwise."}[up])
			}
		case 8:
			set["crab_light"] = up
			out = append(out, map[bool]string{true: "Lever 8: eyelike fixtures shed Bright Light in a 30-foot radius and Dim Light 30 feet beyond.", false: "Lever 8: the light turns off."}[up])
		case 9:
			out = append(out, map[bool]string{true: "Lever 9: the apparatus sinks up to 20 feet if it's in liquid (it can go down to 900 feet; below that, 2d6 Bludgeoning each minute).",
				false: "Lever 9: the apparatus rises up to 20 feet if it's in liquid."}[up])
		case 10:
			set["crab_hatch"] = up
			out = append(out, map[bool]string{true: "Lever 10: the rear hatch unseals and opens.", false: "Lever 10: the rear hatch closes and seals: airtight and watertight, with air for 10 hours divided by the creatures breathing inside."}[up])
		default:
			out = append(out, fmt.Sprintf("Lever %d: there are only ten levers.", n))
		}
	}
	if len(set) > 0 {
		updateInventoryItem(charID, it, func(x invItem) {
			for k, v := range set {
				x[k] = v
			}
		})
		for k, v := range set {
			it[k] = v
		}
	}
	return "Apparatus of the Crab (a Utilize action; each lever returns to neutral): " + strings.Join(out, " ") + " " + crabStatus(it)
}

// crabClaws: lever 5: each extended claw attacks the target monster (+8, reach 5 ft.).
func crabClaws(lobby int, claws, up bool, req itemCastReq) string {
	if !claws {
		return "Lever 5: nothing happens: the claws aren't extended (lever 4 up)."
	}
	target := req.TargetID
	if target == 0 && len(req.TargetIDs) > 0 {
		target = req.TargetIDs[0]
	}
	e, _, ok := monsterCombatant(lobby, target)
	if target >= 0 || !ok {
		return "Lever 5: the claws snap at the air: name the monster in reach as target_id (a negative id from GET /api/my-turn)."
	}
	name, ac := fmt.Sprint(e["name"]), asInt(e["ac"])
	var lines []string
	for i := 1; i <= 2; i++ {
		roll := game.RollDie(20)
		total := roll + 8
		if roll == 1 || (roll != 20 && total < ac) {
			lines = append(lines, fmt.Sprintf("claw %d: %d vs AC %d, a miss", i, total, ac))
			continue
		}
		if up {
			text, _ := damageMonster(lobby, target, game.RollDamage("2d6", roll == 20), "bludgeoning")
			lines = append(lines, fmt.Sprintf("claw %d: %d vs AC %d, a hit: %s", i, total, ac, text))
			continue
		}
		if monsterConditionImmune(lobby, target, "grappled") {
			lines = append(lines, fmt.Sprintf("claw %d: %d vs AC %d, a hit, but %s can't be Grappled", i, total, ac, name))
			continue
		}
		addMonsterCondition(lobby, target, "grappled", "Apparatus of the Crab (escape DC 15)", 0, "", 0)
		lines = append(lines, fmt.Sprintf("claw %d: %d vs AC %d, a hit: %s has the Grappled condition (escape DC 15)", i, total, ac, name))
	}
	return "Lever 5, the claws attack " + name + " (+8, reach 5 ft.): " + strings.Join(lines, "; ") + "."
}
