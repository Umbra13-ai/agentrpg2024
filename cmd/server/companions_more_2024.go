package main

// Companions, part 2 (the gaps left after the first build):
//   Battle map: companions can have tokens (the GM places them; ids from companionIDBase, as in GET /api/campaigns/{id}/combat
//     companions). The owner's player moves one with POST /api/campaigns/{id}/battle-map/preview|move and "companion_id" on the
//     owner's turn (or its own turn with AGENTRPG_FAMILIAR_INITIATIVE=own), using the companion's Speed (its fastest, the map has no
//     height). Leaving a monster's reach is reported for the GM, who resolves the Opportunity Attack with gm/narrate monster_action
//     against the companion (Flyby: none).
//   Monster attacks on companions: gm/narrate monster_action with "target" naming a companion (its name, nickname or combat id) rolls
//     the stat block's attacks against the companion's AC and deals the damage (0 Hit Points: it disappears; a familiar comes back
//     when Find Familiar is cast again).
//   Life Bond (Otherworldly Steed): when the Paladin regains Hit Points from a level 1+ spell, the steed regains as many if it is
//     within 5 feet (on the map; without one, the server assumes it is).
//   Draconic Spirit: Breath Weapon with its Rends (companion action "breath": Dexterity save against the owner's spell save DC, 30-foot
//     Cone, 2d6 damage of the type it resists, chosen with the description: acid, cold, fire, lightning or poison).
//     It is part of the spirit's Multiattack, so it comes once per round besides its Rend attacks.
//   Otherworldly Steed Bonus Actions, each once per Long Rest: Fey Step (Fey: teleport with its rider up to 60 feet), Fell Glare (Fiend:
//     Wisdom save or Frightened until the end of the owner's next turn), Healing Touch (Celestial: a creature within 5 feet regains
//     2d8 + the spell's level). Companion actions "fey_step", "fell_glare", "healing_touch".
//   Animate Objects: the objects count against the spellcasting modifier as Medium or smaller 1, Large 2, Huge 3 (HP 10/20/40; Slam
//     1d4 + 3, 2d6 + 3 + modifier, 2d12 + 3 + modifier; +1d4/1d6/1d12 per slot level above 5). "large" or "huge" in the description.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// companionByNameOrID finds an active companion in the campaign by combat id, id, name or nickname.
func companionByNameOrID(lobbyID int, who string) (companion, bool) {
	who = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(who), "companion "))
	if who == "" {
		return companion{}, false
	}
	n, numErr := strconv.Atoi(who)
	for _, c := range campaignCompanions(lobbyID) {
		if c.Status != "active" || c.HP <= 0 {
			continue
		}
		if numErr == nil && (n == c.CombatID || n == c.ID) {
			return c, true
		}
		if strings.EqualFold(c.Name, who) || (c.Nickname != "" && strings.EqualFold(c.Nickname, who)) {
			return c, true
		}
	}
	return companion{}, false
}

// monsterAttacksCompanion rolls a monster's chosen attacks against a companion.
func monsterAttacksCompanion(lobbyID, attackerID int, attackerName string, chosen []monsterActionRow, c companion, apply bool) (string, map[string]interface{}) {
	for i, a := range chosen {
		prepared, problem := prepareUnconditionalAttack(a, a.DamageType)
		if problem != "" {
			return problem, map[string]interface{}{"refused": true}
		}
		chosen[i] = prepared
	}

	var lines []string
	total, hits := 0, 0
	for _, a := range chosen {
		cur, _ := loadCompanion(c.ID)
		if cur.Status != "active" || cur.HP <= 0 {
			break
		}
		// the one attack function (attack_2024.go); the companion's own Armor Class is the one it has now
		isMelee := meleeRe.MatchString(a.Desc) && !rangedOnlyRe.MatchString(a.Desc)
		// the battle map decides reach, long range and "within 5 feet" as it does for a character target (monster_attacks.go); without a
		// map (or a token for either side) a melee attack is within 5 feet
		withinFive, longRange := isMelee, false
		if _, inReach := opportunityInReach.Load(fmt.Sprintf("%d:%d", lobbyID, attackerID)); attackerID != 0 && !inReach {
			var refusal string
			if withinFive, longRange, refusal = monsterAttackRange(lobbyID, attackerID, cur.CombatID, a.Desc, a.Name, companionDisplayName(cur), isMelee); refusal != "" {
				lines = append(lines, refusal)
				continue
			}
		}
		atk := attackCtx{lobby: lobbyID, attacker: attackerID, target: cur.CombatID, name: a.Name, bonus: a.AttackBonus, melee: isMelee, weapon: true,
			ac: &cur.AC, statBlockAttack: a.Desc, autoHit: autoHitRe.MatchString(a.Desc), within5: &withinFive, longRange: longRange}
		rolled, out, rollNote := atk.roll()
		if !out.Hit {
			lines = append(lines, rolled.Line(a.Name, out)+rollNote)
			continue
		}
		hits++
		parts := []extraDamage{{Dice: a.DamageDice, Type: a.DamageType}}
		for _, x := range attackExtraDamage(a.Desc) {
			if x.IfAdvantage && !rolled.HasAdvantage() {
				continue // damage "if the attack roll had Advantage": only then
			}
			parts = append(parts, x)
		}
		var said []string
		hpStart := cur.HP
		if fresh, ok := loadCompanion(c.ID); ok {
			hpStart = fresh.HP
		}
		for i, p := range parts {
			flat := p.Flat
			if i == 0 {
				flat = a.FlatDamage
			}
			d := rollStatBlockDamage(p.Dice, flat, out.Critical)
			if d < 0 {
				d = 0
			}
			total += d
			if apply {
				cur, _ = loadCompanion(c.ID)
				if cur.Status == "active" {
					said = append(said, damageCompanion(cur, d, strings.ToLower(p.Type)))
				}
			} else {
				said = append(said, fmt.Sprintf("%d %s damage", d, p.Type))
			}
		}
		line := rolled.Line(a.Name, out) + rollNote + ", " + strings.Join(said, " ")
		if apply {
			taken := 0
			if after, ok := loadCompanion(c.ID); ok {
				taken = hpStart - after.HP
			}
			line += companionHitRiders(lobbyID, attackerID, attackerName, a, c.ID, rolled.HasAdvantage(), taken)
		} else {
			line += companionHitRidersNote(a)
		}
		lines = append(lines, line)
	}
	text := fmt.Sprintf("%s attacks %s. %s.", attackerName, companionDisplayName(c), strings.Join(lines, "; "))
	return text, map[string]interface{}{"attacks": len(chosen), "hits": hits, "damage": total, "applied": apply, "target": c.Name, "companion_id": c.ID}
}

// ---- battle map

var companionSpeedRe = regexp.MustCompile(`(\d+) ft`)

// companionSpeedFeet is a companion's fastest Speed (the map has no height, so a flier moves at its Fly Speed).
func companionSpeedFeet(c companion) int {
	best := 0
	for _, m := range companionSpeedRe.FindAllStringSubmatch(c.Speed, -1) {
		if n, _ := strconv.Atoi(m[1]); n > best {
			best = n
		}
	}
	if best == 0 {
		best = 30
	}
	return best
}

var companionMoved sync.Map // companion id -> "turn marker|feet used"

func companionMoveUsed(id int, marker string) int {
	if v, ok := companionMoved.Load(id); ok {
		parts := strings.SplitN(v.(string), "|", 2)
		if len(parts) == 2 && parts[0] == marker {
			n, _ := strconv.Atoi(parts[1])
			return n
		}
	}
	return 0
}

// activeCompanionCombatIDs: the combat ids of the campaign's companions that are present (they may have battle map tokens).
func activeCompanionCombatIDs(lobbyID int) []int {
	var out []int
	for _, c := range campaignCompanions(lobbyID) {
		if c.Status == "active" && c.HP > 0 {
			out = append(out, c.CombatID)
		}
	}
	return out
}

// companionMapMove: the owner's player previews or moves a companion's token.
func companionMapMove(w http.ResponseWriter, r *http.Request, tx *sql.Tx, campaignID int, m encounterMap, order []int, turn int, started time.Time, req mapRequest, operation string) {
	c, ok := loadCompanion(req.CompanionID)
	if !ok || c.OwnerID != req.CharacterID || c.Status != "active" || c.HP <= 0 {
		battleMapReply(w, 404, map[string]string{"error": "companion_not_found", "message": "That isn't one of your companions here (GET /api/my-turn lists them)."})
		return
	}
	idx := -1
	for i, t := range m.Tokens {
		if t.ID == c.CombatID {
			idx = i
		}
	}
	if idx < 0 {
		battleMapReply(w, 404, map[string]string{"error": "no_companion_token", "message": "Your companion has no token on the map; the GM places it."})
		return
	}
	if r.Method != http.MethodPost || (operation != "preview" && operation != "move") {
		battleMapError(w, 405, "invalid_map_operation")
		return
	}
	cur := 0
	if turn >= 0 && turn < len(order) {
		cur = order[turn]
	}
	if cur != req.CharacterID && cur != c.CombatID {
		battleMapError(w, 409, "not_your_turn")
		return
	}
	actor := m.Tokens[idx]
	view := m.playerView(actor)
	marker := combatTurnMarker(campaignID)
	movement := companionSpeedFeet(c) - companionMoveUsed(c.ID, marker)
	if len(req.Path) == 0 || len(req.Path) > 120 {
		battleMapError(w, 400, "invalid_path_length")
		return
	}
	confirmation := mapPathConfirmation(m.Revision, started, actor.ID, movement, []byte("[]"), req.Path)
	if operation == "move" && req.Confirmation != confirmation {
		battleMapError(w, 409, "stale_path_preview")
		return
	}
	cost := 0
	from := actor.Position
	for i, to := range req.Path {
		step, ok := view.stepCost(actor, from, to, false)
		if !ok || cost+step > movement {
			reason := "not enough movement: this step costs " + strconv.Itoa(step) + " ft and it has " + strconv.Itoa(movement-cost) + " ft left"
			if !ok {
				reason = view.stepProblem(actor, from, to)
			}
			battleMapReply(w, 400, map[string]any{"error": "invalid_path_step", "reason": reason, "step": i + 1, "movement_remaining_ft": movement - cost})
			return
		}
		cost += step
		from = to
	}
	if operation == "preview" {
		battleMapReply(w, 200, map[string]any{"confirmation": confirmation, "start": actor.Position, "path": req.Path, "cost_ft": cost, "movement_remaining_ft": movement - cost, "companion": c.Name})
		return
	}
	startToken := actor
	traversed := []mapSquare{}
	cost = 0
	for _, to := range req.Path {
		step, ok := m.stepCost(actor, actor.Position, to, false)
		if !ok || cost+step > movement {
			break
		}
		actor.Position = to
		traversed = append(traversed, to)
		cost += step
	}
	opportunity := opportunityTriggers(campaignID, m, startToken, traversed)
	m.Tokens[idx] = actor
	carried := carryAttachedTokens(tx, campaignID, &m, actor.ID, actor.Name, startToken.Position, actor.Position, actor.Altitude) // an attached monster moves with it
	m.Revision = mapRevision()
	updated, _ := json.Marshal(m)
	if _, err := tx.Exec("UPDATE combat_state SET battle_map=$1 WHERE lobby_id=$2", updated, campaignID); err != nil {
		battleMapError(w, 500, "map_write_failed")
		return
	}
	if err := writeAction(tx, campaignID, req.CharacterID, "map_move", companionDisplayName(c)+" moved on the battle map", fmt.Sprintf("%d ft of movement spent", cost)); err != nil || tx.Commit() != nil {
		battleMapError(w, 500, "map_write_failed")
		return
	}
	companionMoved.Store(c.ID, fmt.Sprintf("%s|%d", marker, companionMoveUsed(c.ID, marker)+cost))
	reply := map[string]any{"position": actor.Position, "traversed": traversed, "cost_ft": cost, "movement_remaining_ft": movement - cost, "companion": c.Name, "map": m.playerView(actor)}
	if len(carried) > 0 {
		reply["carried"] = carried
	}
	if len(opportunity) > 0 && !companionHasTrait(c, "Flyby") {
		var names []string
		for _, id := range opportunity {
			names = append(names, monsterName(campaignID, id))
		}
		note := fmt.Sprintf("%s left the reach of %s: the GM may take an Opportunity Attack against it (gm/narrate monster_action with target %q).", companionDisplayName(c), joinNames(names), c.Name)
		reply["opportunity_attacks_for_gm"] = note
		recordGMAction(db, campaignID, "opportunity_attack_available", note, note)
	}
	battleMapReply(w, 200, reply)
}

// ---- Life Bond, Breath Weapon, Fey Step, Fell Glare

// lifeBond: the Paladin regained Hit Points from a level 1+ spell; the Otherworldly Steed within 5 feet regains as many.
func lifeBond(charID, regained int) string {
	if regained <= 0 {
		return ""
	}
	for _, c := range companionsOf(charID) {
		if c.Kind != "steed" || c.Status != "active" || c.HP <= 0 || c.HP >= c.MaxHP {
			continue
		}
		if m, ok := activeEncounterMap(c.LobbyID); ok {
			a, ok1 := m.token(charID)
			b, ok2 := m.token(c.CombatID)
			if ok1 && ok2 && tokenDistanceFeet(a, b) > 5 {
				continue
			}
		}
		got, err := healCompanion(c.ID, regained) // the companion Hit Point owner (monster_hp_2024.go)
		if err != nil || got == 0 {
			return ""
		}
		return fmt.Sprintf(" Life Bond: %s regains %d Hit Points.", companionDisplayName(c), got)
	}
	return ""
}

var breathTypes = []string{"acid", "cold", "fire", "lightning", "poison"}

// companionSpecialAction: Breath Weapon (Draconic Spirit), Fey Step and Fell Glare (Otherworldly Steed). "" when the action isn't one.
func companionSpecialAction(c companion, charID, lobbyID int, action, description string, targetID int, targets []int) (string, string) {
	low := strings.ToLower(description)
	switch action {
	case "breath", "breath_weapon":
		if why := companionRefusal(action, c).text(); why != "" { // the action table's Who (action_types_2024.go)
			return "", why
		}
		_, _, dc := casterNumbers(charID)
		dtype := pickWord(low, breathTypes, "fire")
		ids := targets
		if len(ids) == 0 && targetID != 0 {
			ids = []int{targetID}
		}
		if len(ids) == 0 {
			return "", "Name the creatures in the 30-foot Cone (target_ids; monsters have negative ids)."
		}
		dmg := game.RollDamage("2d6", false)
		var lines []string
		for _, id := range ids {
			if id >= 0 {
				continue
			}
			sv := creatureSave(lobbyID, id, "dex", dc)
			d := dmg
			if sv.Success {
				d /= 2
			}
			text, _ := damageMonster(lobbyID, id, d, dtype)
			lines = append(lines, sv.LineAgainst(monsterName(lobbyID, id))+": "+text)
		}
		return fmt.Sprintf("%s exhales %s in a 30-foot Cone (2d6: %d). %s", companionDisplayName(c), titleCase(dtype), dmg, strings.Join(lines, " ")), ""
	case "fey_step", "fell_glare", "healing_touch":
		if why := companionRefusal(action, c).text(); why != "" {
			return "", why
		}
		key := "steed_" + action
		if err := useOnce(charID, key, "Used already; it comes back when you finish a Long Rest."); err != nil { // resources_2024.go
			return "", spendText(err, action)
		}
		if action == "fey_step" {
			return fmt.Sprintf("%s uses Fey Step (a Bonus Action): it teleports, with its rider, to an unoccupied space within 60 feet that it can see (move its token on the map).", companionDisplayName(c)), ""
		}
		if action == "healing_touch" {
			who := targetID
			if who <= 0 || who >= companionIDBase {
				who = charID
			}
			heal := game.RollDamage("2d8", false) + c.SlotLevel
			name, before, after := healCharacter(who, heal)
			return fmt.Sprintf("%s uses Healing Touch (a Bonus Action): %s regains %d Hit Points (%d → %d; 2d8 + %d).", companionDisplayName(c), name, after-before, before, after, c.SlotLevel), ""
		}
		if targetID >= 0 {
			return "", "Fell Glare targets one creature within 60 feet (target_id: a monster's negative id)."
		}
		_, _, dc := casterNumbers(charID)
		sv := creatureSave(lobbyID, targetID, "wis", dc)
		if sv.Success {
			return fmt.Sprintf("%s fixes %s with a Fell Glare: %s.", companionDisplayName(c), monsterName(lobbyID, targetID), sv.LineAgainst("it")), ""
		}
		applyCondition(lobbyID, targetID, "frightened", companionDisplayName(c)+" (Fell Glare)", 2, "", 0)
		return fmt.Sprintf("%s fixes %s with a Fell Glare: %s and has the Frightened condition until the end of your next turn.", companionDisplayName(c), monsterName(lobbyID, targetID), sv.LineAgainst("it")), ""
	}
	return "", ""
}

// animatedObjectSize: the size the caster picks ("large", "huge"), what it counts as, its Hit Points and its Slam at the slot level.
func animatedObjectSize(description string, slot, mod int) (size string, counts, hp int, slam string) {
	low := strings.ToLower(description)
	up := slot - 5
	if up < 0 {
		up = 0
	}
	switch {
	case strings.Contains(low, "huge"):
		return "Huge", 3, 40, fmt.Sprintf("%dd12+%d", 2+up, 3+mod)
	case strings.Contains(low, "large"):
		return "Large", 2, 20, fmt.Sprintf("%dd6+%d", 2+up, 3+mod)
	}
	return "Medium", 1, 10, fmt.Sprintf("%dd4+3", 1+up)
}

var breathRound sync.Map // companion id -> round marker of its last Breath Weapon

// breathAvailable: a Draconic Spirit breathes once per round (its Multiattack), besides its Rends.
func breathAvailable(c companion, lobbyID int) bool {
	m := combatTurnMarker(lobbyID)
	round, _, active := combatRoundOf(lobbyID)
	if !active || m == "" {
		return true
	}
	key := fmt.Sprintf("%d:%d", lobbyID, round)
	if v, ok := breathRound.Load(c.ID); ok && v.(string) == key {
		return false
	}
	breathRound.Store(c.ID, key)
	return true
}
