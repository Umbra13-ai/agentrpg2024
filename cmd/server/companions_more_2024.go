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

// damageCompanion deals damage to a companion and says what happened; at 0 Hit Points it disappears.
func damageCompanion(c companion, dmg int, dtype string) string {
	hp := c.HP - dmg
	if hp <= 0 {
		db.Exec(`UPDATE companions SET hp = 0 WHERE id = $1`, c.ID)
		endCompanion(c.ID, "dropped to 0 Hit Points")
		text := fmt.Sprintf("%s takes %d %s damage and drops to 0 Hit Points: it disappears.", companionDisplayName(c), dmg, dtype)
		if c.Kind == "familiar" {
			text += " It reappears when its owner casts Find Familiar again."
		}
		return text
	}
	db.Exec(`UPDATE companions SET hp = $1 WHERE id = $2`, hp, c.ID)
	return fmt.Sprintf("%s takes %d %s damage (%s).", companionDisplayName(c), dmg, dtype, healthWord(hp, c.MaxHP))
}

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
	var lines []string
	total, hits := 0, 0
	for _, a := range chosen {
		cur, _ := loadCompanion(c.ID)
		if cur.Status != "active" || cur.HP <= 0 {
			break
		}
		adv := attackerID != 0 && auraOfAuthority(lobbyID, attackerID)
		roll := game.RollDie(20)
		if adv {
			if r2 := game.RollDie(20); r2 > roll {
				roll = r2
			}
		}
		toHit := roll + a.AttackBonus
		if roll == 1 || (roll != 20 && toHit < cur.AC) {
			lines = append(lines, fmt.Sprintf("%s: %d vs AC %d - MISS", a.Name, toHit, cur.AC))
			continue
		}
		crit := roll == 20
		hits++
		parts := []extraDamage{{a.DamageDice, a.DamageType}}
		parts = append(parts, attackExtraDamage(a.Desc)...)
		var said []string
		for _, p := range parts {
			d := game.RollDamage(p.Dice, crit)
			if d < 1 {
				d = 1
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
		label := "HIT"
		if crit {
			label = "CRITICAL HIT"
		}
		lines = append(lines, fmt.Sprintf("%s: %d vs AC %d - %s, %s", a.Name, toHit, cur.AC, label, strings.Join(said, " ")))
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
	m.Revision = mapRevision()
	updated, _ := json.Marshal(m)
	if _, err := tx.Exec("UPDATE combat_state SET battle_map=$1 WHERE lobby_id=$2", updated, campaignID); err != nil {
		battleMapError(w, 500, "map_write_failed")
		return
	}
	if _, err := tx.Exec("INSERT INTO actions (lobby_id,character_id,action_type,description,result) VALUES ($1,$2,'map_move',$3,$4)", campaignID, req.CharacterID, companionDisplayName(c)+" moved on the battle map", fmt.Sprintf("%d ft of movement spent", cost)); err != nil || tx.Commit() != nil {
		battleMapError(w, 500, "map_write_failed")
		return
	}
	companionMoved.Store(c.ID, fmt.Sprintf("%s|%d", marker, companionMoveUsed(c.ID, marker)+cost))
	reply := map[string]any{"position": actor.Position, "traversed": traversed, "cost_ft": cost, "movement_remaining_ft": movement - cost, "companion": c.Name, "map": m.playerView(actor)}
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
		hp := c.HP + regained
		if hp > c.MaxHP {
			hp = c.MaxHP
		}
		db.Exec(`UPDATE companions SET hp = $1 WHERE id = $2`, hp, c.ID)
		return fmt.Sprintf(" Life Bond: %s regains %d Hit Points.", companionDisplayName(c), hp-c.HP)
	}
	return ""
}

var breathTypes = []string{"acid", "cold", "fire", "lightning", "poison"}

// companionSpecialAction: Breath Weapon (Draconic Spirit), Fey Step and Fell Glare (Otherworldly Steed). "" when the action isn't one.
func companionSpecialAction(c companion, charID, lobbyID int, action, description string, targetID int, targets []int) (string, string) {
	low := strings.ToLower(description)
	switch action {
	case "breath", "breath_weapon":
		if c.Form != "draconic-spirit" {
			return "", "Only a Draconic Spirit (Summon Dragon) has a Breath Weapon."
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
			saved, total := monsterSave(lobbyID, id, "dex", dc)
			d := dmg
			if saved {
				d /= 2
			}
			text, _ := damageMonster(lobbyID, id, d, dtype)
			lines = append(lines, fmt.Sprintf("%s %s the DC %d Dexterity save (%d): %s", monsterName(lobbyID, id), map[bool]string{true: "succeeds on", false: "fails"}[saved], dc, total, text))
		}
		return fmt.Sprintf("%s exhales %s in a 30-foot Cone (2d6: %d). %s", companionDisplayName(c), titleCase(dtype), dmg, strings.Join(lines, " ")), ""
	case "fey_step", "fell_glare", "healing_touch":
		if c.Kind != "steed" {
			return "", "Only an Otherworldly Steed has Fey Step, Fell Glare and Healing Touch."
		}
		want := map[string]string{"fey_step": "fey", "fell_glare": "fiend", "healing_touch": "celestial"}[action]
		if c.CreatureType != want {
			return "", fmt.Sprintf("%s is a %s steed; %s belongs to a %s steed.", companionDisplayName(c), titleCase(c.CreatureType), titleCase(strings.ReplaceAll(action, "_", " ")), titleCase(want))
		}
		key := "steed_" + action
		var used int
		db.QueryRow(`SELECT COALESCE((class_resources_used->>$2)::int,0) FROM characters WHERE id = $1`, charID, key).Scan(&used)
		if used > 0 {
			return "", "Used already; it comes back when you finish a Long Rest."
		}
		db.Exec(`UPDATE characters SET class_resources_used = jsonb_set(COALESCE(class_resources_used,'{}'), ARRAY[$2], '1') WHERE id = $1`, charID, key)
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
		saved, total := monsterSave(lobbyID, targetID, "wis", dc)
		if saved {
			return fmt.Sprintf("%s fixes %s with a Fell Glare: it succeeds on the DC %d Wisdom save (%d).", companionDisplayName(c), monsterName(lobbyID, targetID), dc, total), ""
		}
		addMonsterCondition(lobbyID, targetID, "frightened", companionDisplayName(c)+" (Fell Glare)", 2, "", 0)
		return fmt.Sprintf("%s fixes %s with a Fell Glare: it fails the DC %d Wisdom save (%d) and has the Frightened condition until the end of your next turn.", companionDisplayName(c), monsterName(lobbyID, targetID), dc, total), ""
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
