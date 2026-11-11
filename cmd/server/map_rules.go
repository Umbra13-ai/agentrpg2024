package main

// Rules that use the battle map when the encounter has one (and are skipped when it has none):
//	reach and range   a weapon attack, a spell and a monster attack must reach the target: melee 5 feet (10 with Reach), a thrown or
//	                  ranged weapon up to its long range, a spell up to its range (Touch is 5 feet). The player is told the distance.
//	sight             a target hidden by the GM, or behind a wall, cannot be targeted.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// activeEncounterMap returns the published battle map of the lobby's active combat.
func activeEncounterMap(lobbyID int) (encounterMap, bool) {
	var raw []byte
	if db == nil || db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw) != nil || len(raw) == 0 {
		return encounterMap{}, false
	}
	var m encounterMap
	if json.Unmarshal(raw, &m) != nil || len(m.Grid) == 0 {
		return encounterMap{}, false
	}
	return m, true
}

func (m encounterMap) token(id int) (mapToken, bool) {
	for _, t := range m.Tokens {
		if t.ID == id {
			return t, true
		}
	}
	return mapToken{}, false
}

// distanceFeet is the distance between two tokens in feet: squares counted with diagonals as one, between the nearest squares.
func tokenDistanceFeet(a, b mapToken) int {
	best := -1
	for _, p := range a.footprint(a.Position) {
		for _, q := range b.footprint(b.Position) {
			d := mapAbs(p.X - q.X)
			if e := mapAbs(p.Y - q.Y); e > d {
				d = e
			}
			if best < 0 || d < best {
				best = d
			}
		}
	}
	if v := mapAbs(a.Altitude - b.Altitude); v > best*battleMapStepFeet { // a flying creature: the height counts (spells_more_2024.go)
		return v
	}
	return best * battleMapStepFeet
}

// canSee: some square of the target can be seen from the viewer's square (walls and closed doors block it).
func (m encounterMap) canSee(viewer, target mapToken) bool {
	for _, p := range target.footprint(target.Position) {
		if m.mapVisible(viewer.Position, p) {
			return true
		}
	}
	return false
}

var rangeRe = regexp.MustCompile(`\((\d+)/(\d+)\)`)
var rangeFeetRe = regexp.MustCompile(`(\d+)\s*(?:feet|foot|ft)`)

// weaponReach returns the reach of a melee attack and, for a thrown or ranged weapon, its normal and long range in feet.
func weaponReach(w SRDWeapon, hasWeapon, throwing bool) (reach, normal, long int) {
	reach = 5
	if !hasWeapon {
		return
	}
	for _, p := range w.Properties {
		lp := strings.ToLower(p)
		if lp == "reach" {
			reach = 10
		}
		if m := rangeRe.FindStringSubmatch(lp); m != nil && (strings.HasPrefix(lp, "ammunition") || strings.HasPrefix(lp, "thrown") || strings.HasPrefix(lp, "range")) {
			normal, _ = strconv.Atoi(m[1])
			long, _ = strconv.Atoi(m[2])
			if strings.HasPrefix(lp, "thrown") && !throwing && w.Type == "melee" {
				continue
			}
		}
	}
	if w.Type == "ranged" {
		reach = 0
	}
	return
}

// attackRangeProblem says why a weapon attack at targetID cannot be made ("" when it can or when there is no map).
func attackRangeProblem(lobbyID, charID, targetID int, description string) string {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return ""
	}
	a, okA := m.token(charID)
	b, okB := m.token(targetID)
	if !okA || !okB {
		return ""
	}
	if !m.canSee(a, b) || b.Hidden {
		return "You can't see " + b.Name + " from where you stand (a wall is in the way, or it is hidden). Your action was not used."
	}
	key := parseWeaponFromDescription(description)
	if key == "" {
		var held string
		db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, charID).Scan(&held)
		key = mapWeaponKey(charID, held)
	}
	w, hasWeapon := srdWeapons[key]
	throwing := strings.Contains(strings.ToLower(description), "throw") || strings.Contains(strings.ToLower(description), "hurl")
	reach, normal, long := weaponReach(w, hasWeapon, throwing)
	dist := tokenDistanceFeet(a, b)
	name := "an unarmed strike"
	if hasWeapon {
		name = "your " + strings.ToLower(w.Name)
	}
	switch {
	case hasWeapon && w.Type == "ranged":
		if long > 0 && dist > long {
			return fmt.Sprintf("%s is %d ft away, beyond the %d ft long range of %s. Move closer first. Your action was not used.", b.Name, dist, long, name)
		}
	case throwing && long > 0:
		if dist > long {
			return fmt.Sprintf("%s is %d ft away, beyond the %d ft long range of %s. Your action was not used.", b.Name, dist, long, name)
		}
	default:
		if dist > reach {
			return fmt.Sprintf("%s is %d ft away and %s reaches %d ft. Move next to it first (or throw or fire a ranged weapon). Your action was not used.", b.Name, dist, name, reach)
		}
	}
	_ = normal
	return ""
}

// attackAtLongRange: a ranged or thrown weapon attack at a target beyond the weapon's normal range (and within its long range)
// has Disadvantage. It is false without a battle map.
func attackAtLongRange(lobbyID, charID, targetID int, description string) bool {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return false
	}
	a, okA := m.token(charID)
	b, okB := m.token(targetID)
	if !okA || !okB {
		return false
	}
	key := parseWeaponFromDescription(description)
	if key == "" {
		var held string
		db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, charID).Scan(&held)
		key = mapWeaponKey(charID, held)
	}
	w, hasWeapon := srdWeapons[key]
	throwing := strings.Contains(strings.ToLower(description), "throw") || strings.Contains(strings.ToLower(description), "hurl")
	_, normal, _ := weaponReach(w, hasWeapon, throwing)
	return normal > 0 && tokenDistanceFeet(a, b) > normal
}

// monsterAttackRanges reads "range 80/320 ft" from a monster's attack: the normal range (0 when the attack has no long range).
func monsterNormalRange(desc string) int {
	if m := rangeFtRe.FindStringSubmatch(strings.ToLower(desc)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// monsterSeesTarget: on the battle map, a wall or closed door between a monster and a character gives Total Cover (it can't be targeted).
// ok is false without a map.
func monsterSeesTarget(lobbyID, attackerID, targetID int) (sees, ok bool) {
	m, okM := activeEncounterMap(lobbyID)
	if !okM {
		return true, false
	}
	a, okA := m.token(attackerID)
	b, okB := m.token(targetID)
	if !okA || !okB {
		return true, false
	}
	return m.canSee(a, b), true
}

// spellRangeFeet reads a spell's range text: "150 feet", "Touch" (5), "Self" (0), "Sight" (unlimited, -1).
func spellRangeFeet(rangeText string) int {
	l := strings.ToLower(strings.TrimSpace(rangeText))
	switch {
	case l == "" || strings.HasPrefix(l, "self"):
		return 0
	case strings.HasPrefix(l, "touch"):
		return 5
	case strings.HasPrefix(l, "sight") || strings.HasPrefix(l, "unlimited") || strings.HasPrefix(l, "special"):
		return -1
	}
	if m := rangeFeetRe.FindStringSubmatch(l); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if strings.Contains(l, "mile") {
		return -1
	}
	return -1
}

// spellRangeProblem: the same check for a cast aimed at a creature (target_id and target_ids).
func spellRangeProblem(lobbyID, charID int, spell SRDSpell, targets []int) string {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return ""
	}
	limit := spellRangeFeet(spell.Range)
	if limit == 0 && spell.AoEShape == "emanation" {
		return "" // an Emanation surrounds the caster and isn't aimed: nobody needs to stand in it yet (Spirit Guardians; F75)
	}
	if limit == 0 && spell.AoEShape != "" {
		limit = spell.AoESize // a spell with a range of Self that fills an area in front of the caster
	}
	a, okA := m.token(charID)
	if !okA {
		return ""
	}
	for i, id := range targets {
		if i > 0 && spell.AoEShape != "" {
			break // an area spell is aimed at one point; the others are checked against the area itself
		}
		b, okB := m.token(id)
		if !okB {
			continue
		}
		if b.Hidden || !m.canSee(a, b) {
			return "You can't see " + b.Name + " from where you stand (a wall is in the way, or it is hidden). Your action was not used."
		}
		if limit < 0 {
			continue
		}
		if d := tokenDistanceFeet(a, b); d > limit {
			if limit == 0 {
				return fmt.Sprintf("%s has a range of Self; it cannot be aimed at %s. Your action was not used.", spell.Name, b.Name)
			}
			return fmt.Sprintf("%s is %d ft away, beyond the %d ft range of %s. Move closer first. Your action was not used.", b.Name, d, limit, spell.Name)
		}
	}
	return ""
}

var reachFtRe = regexp.MustCompile(`reach (\d+) ft`)
var rangeFtRe = regexp.MustCompile(`range (\d+)/(\d+) ft`)
var rangeSingleRe = regexp.MustCompile(`range (\d+) ft`)

// monsterAttackReach reads "reach 5 ft" or "range 80/320 ft" from an attack's text: the farthest it can hit, and whether it is a ranged attack.
func monsterAttackReach(desc string) (feet int, ranged bool) {
	l := strings.ToLower(desc)
	if m := rangeFtRe.FindStringSubmatch(l); m != nil {
		n, _ := strconv.Atoi(m[2])
		return n, true
	}
	if m := rangeSingleRe.FindStringSubmatch(l); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if m := reachFtRe.FindStringSubmatch(l); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, false
	}
	return 5, false
}

// monsterDistance is the distance in feet between an attacking monster and a target character on the battle map.
func monsterDistance(lobbyID, attackerID, targetCharID int) (int, bool) {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return 0, false
	}
	a, okA := m.token(attackerID)
	b, okB := m.token(targetCharID)
	if !okA || !okB {
		return 0, false
	}
	return tokenDistanceFeet(a, b), true
}

// charDistanceFeet is the distance in feet between two characters on the battle map of their campaign (known is false without a map).
func charDistanceFeet(lobbyID, aID, bID int) (feet int, known bool) {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return 0, false
	}
	a, okA := m.token(aID)
	b, okB := m.token(bID)
	if !okA || !okB {
		return 0, false
	}
	return tokenDistanceFeet(a, b), true
}

// auraReaches says whether a Paladin's aura (10 feet, 30 feet from level 18) reaches the character. Without a battle map it always does.
func auraReaches(lobbyID, paladinID, charID, paladinLevel int) bool {
	reach := 10
	if paladinLevel >= 18 {
		reach = 30
	}
	if d, known := charDistanceFeet(lobbyID, paladinID, charID); known && d > reach {
		return false
	}
	return true
}

// combatDeadFn tells the initiative order which entries have no turn: dead characters and monsters at 0 Hit Points.
func combatDeadFn(lobbyID int) func(int) bool {
	down := map[int]bool{}
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) < 0 && asInt(e["hp"]) <= 0 {
			down[asInt(e["id"])] = true
		}
	}
	return func(id int) bool {
		if id < 0 {
			return down[id]
		}
		if isCompanionCombatID(id) {
			return companionGone(id) // a familiar with its own Initiative (companions_2024.go)
		}
		return combatCharacterIsDead(id)
	}
}

// pruneDeadTokens removes the tokens of defeated monsters from the battle map.
func pruneDeadTokens(lobbyID int) {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return
	}
	dead := combatDeadFn(lobbyID)
	kept := m.Tokens[:0:0]
	for _, t := range m.Tokens {
		if t.ID < 0 && dead(t.ID) {
			continue
		}
		kept = append(kept, t)
	}
	if len(kept) == len(m.Tokens) {
		return
	}
	m.Tokens = kept
	b, _ := json.Marshal(m)
	db.Exec(`UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2 AND active = true`, string(b), lobbyID)
}

// hiddenMonsterIDs are the monsters the GM marked hidden on the battle map.
func hiddenMonsterIDs(lobbyID int) map[int]bool {
	out := map[int]bool{}
	if m, ok := activeEncounterMap(lobbyID); ok {
		for _, t := range m.Tokens {
			if t.Hidden && t.ID < 0 {
				out[t.ID] = true
			}
		}
	}
	return out
}

// sneakAllyOnMap: Sneak Attack without Advantage needs an ally (not Incapacitated) within 5 feet of the target. onMap is false without a battle map.
func sneakAllyOnMap(charID, targetID int) (bool, bool) {
	lobby := lobbyOfChar(charID)
	m, ok := activeEncounterMap(lobby)
	if !ok {
		return false, false
	}
	if _, mid, isMonster := attackedMonster(charID); isMonster {
		targetID = mid
	}
	target, ok := m.token(targetID)
	if !ok {
		return false, false
	}
	for _, t := range m.Tokens {
		if t.ID <= 0 || t.ID == charID || t.ID == targetID {
			continue
		}
		var hp int
		db.QueryRow(`SELECT hp FROM characters WHERE id = $1`, t.ID).Scan(&hp)
		if hp <= 0 || isIncapacitated(t.ID) {
			continue
		}
		if tokensAdjacent(t, target) {
			return true, true
		}
	}
	return false, true
}

var areaNotes sync.Map

func takeAreaNote(charID int) string {
	if v, ok := areaNotes.LoadAndDelete(charID); ok {
		return v.(string)
	}
	return ""
}

// areaFilter keeps the targets that stand inside the spell's area on the battle map. A sphere, cube or cylinder is centered on the first
// target (the point the caster aims at); a cone, line or emanation starts at the caster. It returns the kept ids and a note about the others.
func areaFilter(lobbyID, charID int, spell SRDSpell, targets []int) ([]int, string) {
	m, ok := activeEncounterMap(lobbyID)
	if !ok || spell.AoEShape == "" || spell.AoESize <= 0 || len(targets) == 0 {
		return targets, ""
	}
	caster, okC := m.token(charID)
	center, okT := m.token(targets[0])
	if !okC || !okT {
		return targets, ""
	}
	origin := center
	switch spell.AoEShape {
	case "cone", "line", "emanation":
		origin = caster
	}
	var kept []int
	var dropped []string
	for i, id := range targets {
		t, ok := m.token(id)
		if !ok || (i == 0 && origin.ID == t.ID) {
			kept = append(kept, id)
			continue
		}
		in := tokenDistanceFeet(origin, t) <= spell.AoESize
		if spell.AoEShape == "cone" || spell.AoEShape == "line" { // its direction and width count too (area_geometry_2024.go, F58)
			in = tokenInLineOrCone(spell.AoEShape, caster, center, t, float64(spell.AoESize), lineWidthFeet(spell.Description))
		}
		if in {
			kept = append(kept, id)
		} else {
			dropped = append(dropped, t.Name)
		}
	}
	note := ""
	if len(dropped) > 0 {
		note = fmt.Sprintf(" Outside the area of %s (%s %d ft): %s.", spell.Name, spell.AoEShape, spell.AoESize, strings.Join(dropped, ", "))
	}
	return kept, note
}

// mapWeaponKey: the weapon table key of a held weapon, as the attack resolves it: "Longsword +3", "Maul +2", "Sword of Sharpness
// (Greatsword)", the Oathbow (a Longbow), the Vorpal Sword's base weapon, the Rod's form. F74: the map's reach and range checks looked
// the name up as it was, so these counted as an unarmed strike with a 5-foot reach.
func mapWeaponKey(charID int, held string) string {
	if k := heldWeaponKey(charID, held); k != "" {
		return k
	}
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(plusRe.ReplaceAllString(held, "")), " ", "_"))
}
