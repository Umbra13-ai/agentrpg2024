package main

// Battle map rules that need positions (SRD 5.2.1), used when the encounter has a published map:
//	ranged attacks in close combat  a ranged attack roll has Disadvantage while an enemy that can see the attacker and isn't
//	                                Incapacitated is within 5 feet (characters and monsters; spell attacks too)
//	opportunity attacks             a character who moves out of a monster's reach provokes one melee attack, unless it took the
//	                                Disengage action; the monster needs its Reaction (one per round) and must see the character
//	Aura of Courage / Devotion      Paladin level 10 (every Paladin) and Oath of Devotion level 7: allies in the Aura of Protection
//	                                are immune to Frightened / Charmed
//	Smite of Protection             Oath of Devotion level 15: after Divine Smite, allies in the aura have Half Cover (+2 AC and
//	                                Dexterity saves) until the start of the Paladin's next turn
//	Instinctive Pounce              Barbarian level 7: entering a Rage gives half the Speed as extra movement
//	Superior Hunter's Prey          Hunter level 11: once per turn, damage to the creature marked by Hunter's Mark also deals the
//	                                mark's damage to the nearest other visible creature within 30 feet of it

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// ---- close combat

func livingMonsterIDs(lobbyID int) map[int]bool {
	out := map[int]bool{}
	for _, e := range combatEntries(lobbyID) {
		if id := asInt(e["id"]); id < 0 && asInt(e["hp"]) > 0 {
			out[id] = true
		}
	}
	return out
}

func monsterIncapacitated(lobbyID, id int) bool {
	return game.IsIncapacitated(conditionNames(lobbyID, id))
}

// enemyAdjacentOnMap: a monster that can see the character and isn't Incapacitated is within 5 feet of it.
func enemyAdjacentOnMap(lobbyID, charID int) bool {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return false
	}
	me, ok := m.token(charID)
	if !ok {
		return false
	}
	alive := livingMonsterIDs(lobbyID)
	for _, t := range m.Tokens {
		if t.ID >= 0 || !alive[t.ID] || monsterIncapacitated(lobbyID, t.ID) {
			continue
		}
		if tokenDistanceFeet(t, me) <= 5 && m.canSee(t, me) {
			return true
		}
	}
	return false
}

// characterAdjacentToMonster: a conscious character that can see the monster is within 5 feet of it.
func characterAdjacentToMonster(lobbyID, monsterID int) bool {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return false
	}
	mon, ok := m.token(monsterID)
	if !ok {
		return false
	}
	for _, t := range m.Tokens {
		if t.ID <= 0 {
			continue
		}
		var hp int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, t.ID).Scan(&hp)
		if hp <= 0 || isIncapacitated(t.ID) {
			continue
		}
		if tokenDistanceFeet(t, mon) <= 5 && m.canSee(t, mon) {
			return true
		}
	}
	return false
}

// ---- opportunity attacks

var disengagedThisTurn sync.Map   // character id -> true after the Disengage action this turn
var monsterReactionRound sync.Map // "lobby:id" -> the round its Reaction was used

// clearMonsterReactions forgets the monsters' spent Reactions of a campaign when a fight starts or ends (F57: monster ids and round
// numbers start again in each fight, so an Opportunity Attack in round 1 of one fight blocked round 1 of the next).
func clearMonsterReactions(lobbyID int) {
	prefix := fmt.Sprintf("%d:", lobbyID)
	monsterReactionRound.Range(func(k, _ interface{}) bool {
		if s, ok := k.(string); ok && strings.HasPrefix(s, prefix) {
			monsterReactionRound.Delete(k)
		}
		return true
	})
}

var opportunityInReach sync.Map // "lobby:id" -> an Opportunity Attack is being resolved (the target was in reach)

func markDisengaged(charID int) { disengagedThisTurn.Store(charID, true) }

// tookDisengage: the action counts as Disengage (the action, Cunning Action, Patient Defense, Step of the Wind with focus).
func tookDisengage(action, description string) bool {
	d := strings.ToLower(description)
	switch action {
	case "disengage", "patient_defense":
		return true
	case "cunning_action", "step_of_the_wind":
		return strings.Contains(d, "disengage") || (action == "step_of_the_wind" && strings.Contains(d, "focus"))
	}
	return false
}

// monsterMeleeAttack returns the name and reach of a monster's first melee attack.
func monsterMeleeAttack(lobbyID, id int) (string, int) {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(actions,'[]') FROM monsters WHERE slug = $1`, monsterKeyOfCombatant(lobbyID, id)).Scan(&raw)
	var actions []monsterActionRow
	json.Unmarshal(raw, &actions)
	for _, a := range actions {
		if strings.EqualFold(a.Name, "Multiattack") || a.AttackBonus == 0 {
			continue
		}
		if reach, ranged := monsterAttackReach(a.Desc); !ranged {
			return a.Name, reach
		}
	}
	return "", 0
}

func currentRound(lobbyID int) int {
	var r int
	db.QueryRow(`SELECT round_number FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&r)
	return r
}

// opportunityTriggers lists the monsters whose reach the mover leaves along the path (each once).
func opportunityTriggers(lobbyID int, m encounterMap, mover mapToken, path []mapSquare) []int {
	if _, dis := disengagedThisTurn.Load(mover.ID); dis {
		return nil
	}
	alive := livingMonsterIDs(lobbyID)
	round := currentRound(lobbyID)
	var out []int
	seen := map[int]bool{}
	for _, t := range m.Tokens {
		if t.ID >= 0 || !alive[t.ID] || monsterIncapacitated(lobbyID, t.ID) {
			continue
		}
		if r, ok := monsterReactionRound.Load(fmt.Sprintf("%d:%d", lobbyID, t.ID)); ok && r.(int) == round {
			continue // its Reaction is spent this round
		}
		_, reach := monsterMeleeAttack(lobbyID, t.ID)
		if reach == 0 {
			continue
		}
		pos := mover
		for _, step := range path {
			before := tokenDistanceFeet(t, pos) <= reach && m.canSee(t, pos)
			pos.Position = step
			after := tokenDistanceFeet(t, pos) <= reach
			if before && !after && !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, t.ID)
				break
			}
		}
	}
	return out
}

// resolveOpportunityAttacks makes the attacks (after the move was saved) and returns what happened.
func resolveOpportunityAttacks(r *http.Request, lobbyID, charID int, monsters []int) []string {
	var out []string
	round := currentRound(lobbyID)
	var charName string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&charName)
	for _, id := range monsters {
		action, _ := monsterMeleeAttack(lobbyID, id)
		if action == "" {
			continue
		}
		monsterReactionRound.Store(fmt.Sprintf("%d:%d", lobbyID, id), round)
		key := fmt.Sprintf("%d:%d", lobbyID, id)
		opportunityInReach.Store(key, true) // the attack happens just before the character leaves its reach
		text, _, ok := resolveMonsterAttack(r, lobbyID, monsterName(lobbyID, id), action, fmt.Sprint(charID), true)
		opportunityInReach.Delete(key)
		if !ok {
			continue
		}
		line := "Opportunity Attack: " + text
		out = append(out, line)
		recordGMAction(db, lobbyID, "opportunity_attack", line, line)
	}
	return out
}

// ---- auras

// paladinAuras lists the conscious Paladins of the campaign with their level and subclass.
func paladinAuras(lobbyID int) []struct {
	ID, Level int
	Subclass  string
} {
	var out []struct {
		ID, Level int
		Subclass  string
	}
	rows, err := db.Query(`SELECT id, level, COALESCE(subclass,'') FROM characters WHERE lobby_id = $1 AND lower(class) = 'paladin' AND hp > 0 AND COALESCE(is_dead,false) = false`, lobbyID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var p struct {
			ID, Level int
			Subclass  string
		}
		if rows.Scan(&p.ID, &p.Level, &p.Subclass) == nil {
			out = append(out, p)
		}
	}
	return out
}

// auraImmune: Aura of Courage (Paladin 10, Frightened) and Aura of Devotion (Devotion 7, Charmed) protect the Paladin and allies in
// the Aura of Protection.
func auraImmune(lobbyID, charID int, cond string) (bool, string) {
	base := conditionBase(cond)
	if charID <= 0 || (base != "frightened" && base != "charmed") || lobbyID == 0 {
		return false, ""
	}
	for _, p := range paladinAuras(lobbyID) {
		if isIncapacitated(p.ID) || !auraReaches(lobbyID, p.ID, charID, p.Level) {
			continue
		}
		if base == "frightened" && p.Level >= 10 {
			return true, "Aura of Courage"
		}
		if base == "charmed" && p.Level >= 7 && strings.EqualFold(p.Subclass, "devotion") {
			return true, "Aura of Devotion"
		}
	}
	return false, ""
}

// smiteOfProtection: a Devotion Paladin of level 15 cast Divine Smite: Half Cover for allies in the aura.
func smiteOfProtection(lobbyID, paladinID int) string {
	var level int
	var subclass string
	db.QueryRow(`SELECT level, COALESCE(subclass,'') FROM characters WHERE id = $1`, paladinID).Scan(&level, &subclass)
	if level < 15 || !strings.EqualFold(subclass, "devotion") || lobbyID == 0 {
		return ""
	}
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND hp > 0`, lobbyID)
	if err != nil {
		return ""
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var names []string
	for _, id := range ids {
		if auraReaches(lobbyID, paladinID, id, level) {
			addCombatEffect(lobbyID, id, paladinID, "half_cover")
			names = append(names, creatureName(lobbyID, id))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return " Smite of Protection: " + joinNames(names) + " have Half Cover (+2 AC and Dexterity saves) until the start of your next turn."
}

// halfCoverBonus is the +2 of a Half Cover effect (Smite of Protection) on a character.
func halfCoverBonus(lobbyID, charID int) int {
	if sanctuaryCover(lobbyID, charID) > 0 { // Nature's Sanctuary (natures_sanctuary_2024.go); Half Cover doesn't stack with itself
		return 2
	}
	for _, e := range combatEffectsOn(lobbyID, charID) {
		if e.Kind == "half_cover" {
			return 2
		}
	}
	return 0
}

// ---- Superior Hunter's Prey

var huntersPreyTurn sync.Map // character id -> turn marker

func superiorHuntersPrey(lobbyID, charID, targetID int) string {
	var class, subclass string
	var level int
	db.QueryRow(`SELECT class, COALESCE(subclass,''), level FROM characters WHERE id = $1`, charID).Scan(&class, &subclass, &level)
	if !strings.EqualFold(class, "ranger") || !strings.EqualFold(subclass, "hunter") || level < 11 {
		return ""
	}
	if marked, ok := parseMarkSpellTarget(charID); !ok || marked != targetID {
		return ""
	}
	turn := combatTurnMarker(lobbyID) // F190: unique per turn; "round:turn" repeated in the next combat
	if prev, ok := huntersPreyTurn.Load(charID); ok && prev.(string) == turn && turn != "" {
		return ""
	}
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return ""
	}
	first, okF := m.token(targetID)
	me, okM := m.token(charID)
	if !okF || !okM {
		return ""
	}
	alive := livingMonsterIDs(lobbyID)
	best, bestD := 0, 999
	for _, t := range m.Tokens {
		if t.ID >= 0 || t.ID == targetID || !alive[t.ID] || t.Hidden || !m.canSee(me, t) {
			continue
		}
		if d := tokenDistanceFeet(first, t); d <= 30 && d < bestD {
			best, bestD = t.ID, d
		}
	}
	if best == 0 {
		return ""
	}
	huntersPreyTurn.Store(charID, turn)
	dice := "1d6"
	if level >= 20 {
		dice = "1d10" // Foe Slayer
	}
	text, _ := damageMonster(lobbyID, best, game.RollDamage(dice, false), "force")
	return " Superior Hunter's Prey: " + strings.TrimSpace(text)
}

// parseMarkSpellTarget is the creature the character's Hunter's Mark is on (the stored name may use a curly apostrophe).
func parseMarkSpellTarget(charID int) (int, bool) {
	var conc string
	db.QueryRow(`SELECT COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, charID).Scan(&conc)
	slug, id := parseMarkSpell(conc)
	return id, slug == "hunters-mark" && id != 0
}
