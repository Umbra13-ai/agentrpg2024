package main

// Obscured areas (SRD 5.2.1 "Heavily Obscured": "You have the Blinded condition while trying to see something in a Heavily Obscured
// space"): the one owner of a space a spell or a stat block fills with fog, ink, smoke or the like. One row of combat_effects (kind
// 'obscured_area', its text the JSON of obscuredArea) per area: where it is, how big, how long it lasts and what ends it.
//   - where: a fixed point on the battle map (a Sphere centered on a point: Fog Cloud), a Cube centered on the creature that made it
//     (an octopus's Ink Cloud: the cube stays where the creature was), or, without a battle map, the creatures the GM or the caster
//     names as inside it (inside: the cast's target_ids, or POST /api/gm/obscured-area). Never assumed: an area the server can't place
//     and that names nobody inside blocks nothing.
//   - how long: rounds from the round it was made (10 a minute), and with its source's Concentration (a character's concentrating_on,
//     a monster's Concentration: endMonsterConcentrationIn deletes it in the ending's transaction). A strong wind or current ends it
//     early: the GM's POST /api/gm/obscured-area {"end": true}.
//   - what it does: areaBlocksSight is the one answer to "an area keeps the viewer from seeing the target" for every reader (the
//     attack roll's unseen-attacker and unseen-target rows in attackSources, a monster action that needs a target it can see): a
//     Heavily Obscured area blocks sight when the viewer or the target is in it or the line between them crosses it, unless the viewer
//     has Blindsight that reaches the target; Magical Darkness (monster_darkness_2024.go: its own rows, pierced by Blindsight,
//     Truesight and Devil's Sight) is the other kind areaBlocksSight reads.
// The spells that make one are rows of obscuringSpells (a character's cast and a monster's cast read it); a stat block entry that makes
// one is read by obscuringTextRe (resolveObscuringText).

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type obscuredArea struct {
	ID            int       `json:"-"`
	Source        int       `json:"-"`
	Label         string    `json:"label"`                   // what made it ("Fog Cloud", "Ink Cloud")
	Heavy         bool      `json:"heavy"`                   // Heavily Obscured (false: Lightly Obscured, which hampers only sight-based checks)
	Shape         string    `json:"shape"`                   // "sphere" (Radius around Center) or "cube" (a Cube of side Radius*2 centered on Center)
	Radius        int       `json:"radius"`                  // feet: a Sphere's radius, half a Cube's side
	Center        mapSquare `json:"center"`                  // the battle map square it is centered on
	CenterSize    int       `json:"center_size,omitempty"`   // a Cube centered on a creature: that creature's footprint (squares a side)
	Placed        bool      `json:"placed"`                  // Center is on the battle map
	Inside        []int     `json:"inside,omitempty"`        // without a battle map: the creatures named as inside it
	Round         int       `json:"round"`                   // the round it was made
	Rounds        int       `json:"rounds"`                  // how long it lasts (0: until its Concentration or the GM ends it)
	Concentration bool      `json:"concentration,omitempty"` // it ends with its source's Concentration on Spell
	Spell         string    `json:"spell,omitempty"`         // the spell its Concentration is on
}

// obscuringSpell: a spell that fills an area with something that obscures it.
type obscuringSpell struct {
	Heavy        bool
	Radius       int // feet, at the spell's own level
	PerSlot      int // more feet for each slot level above it
	Rounds       int // its duration in rounds
	DispersedBy  string
	Shape        string
	CenteredSelf bool
}

// obscuringSpells: the SRD 5.2.1 spells that obscure an area (a row is the spell's own text: radius, duration, what disperses it).
var obscuringSpells = map[string]obscuringSpell{
	"fog-cloud": {Heavy: true, Radius: 20, PerSlot: 20, Rounds: 600, DispersedBy: "a strong wind (such as one created by Gust of Wind)", Shape: "sphere"},
}

// obscuringTextRe: a stat block entry that fills an area: "releases ink that fills a 10-foot Cube centered on itself ... The Cube is
// Heavily Obscured for 1 minute or until a strong current or similar effect disperses it."
var obscuringTextRe = regexp.MustCompile(`(?i)fills a (\d+)-foot(?:-radius)? (Cube) centered on (?:itself|it)\b[^.]*\.(?:[^.]*\.)*?[^.]*\bis (Heavily) Obscured for (\d+) (minute|minutes|hour|hours|round|rounds)\b(?:[^.]*\buntil ([^.]+?)\.)?`)

// durationRounds: "1 minute" -> 10, "1 hour" -> 600 (6 seconds a round).
func durationRounds(n int, unit string) int {
	switch strings.TrimSuffix(strings.ToLower(unit), "s") {
	case "hour":
		return n * 600
	case "minute":
		return n * 10
	}
	return n
}

// obscuredAreas reads the campaign's live obscured areas, sweeping the ones that ended on their own: their time passed, or their
// source's Concentration ended (a character's concentrating_on; a monster's ending deletes its rows itself, or its death).
func obscuredAreas(lobbyID int) []obscuredArea {
	rows, err := db.Query(`SELECT id, source_id, text FROM combat_effects WHERE lobby_id=$1 AND kind='obscured_area' ORDER BY id`, lobbyID)
	if err != nil {
		return nil
	}
	var out []obscuredArea
	var gone []int
	for rows.Next() {
		var a obscuredArea
		var text string
		if rows.Scan(&a.ID, &a.Source, &text) != nil || json.Unmarshal([]byte(text), &a) != nil {
			continue
		}
		out = append(out, a)
	}
	rows.Close()
	if len(out) == 0 {
		return nil
	}
	round := currentRound(lobbyID)
	hp := map[int]int{}
	for _, e := range combatEntries(lobbyID) {
		hp[asInt(e["id"])] = asInt(e["hp"])
	}
	live := out[:0]
	for _, a := range out {
		ended := a.Rounds > 0 && round >= a.Round+a.Rounds
		if a.Concentration {
			if a.Source > 0 && !isCompanionCombatID(a.Source) {
				ended = ended || !concentrationHolds(a.Source, a.Spell)
			} else if h, inFight := hp[a.Source]; inFight && h <= 0 {
				ended = true // a monster at 0 Hit Points has lost its Concentration
			}
		}
		if ended {
			gone = append(gone, a.ID)
			continue
		}
		live = append(live, a)
	}
	for _, id := range gone {
		db.Exec(`DELETE FROM combat_effects WHERE id=$1`, id)
	}
	return live
}

// startObscuredArea records an area. One per source and label: a second cast replaces the first.
func startObscuredArea(lobbyID, sourceID int, a obscuredArea) (int, error) {
	a.Round = currentRound(lobbyID)
	b, _ := json.Marshal(a)
	if _, err := db.Exec(`DELETE FROM combat_effects WHERE lobby_id=$1 AND kind='obscured_area' AND source_id=$2 AND text::jsonb->>'label' = $3`,
		lobbyID, sourceID, a.Label); err != nil {
		return 0, err
	}
	var id int
	err := db.QueryRow(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,0,$2,'obscured_area',$3) RETURNING id`,
		lobbyID, sourceID, string(b)).Scan(&id)
	return id, err
}

// areaToken: the area as a token for the battle map's distances (a Sphere's center square; a Cube's center creature's footprint).
func (a obscuredArea) areaToken() mapToken {
	t := pointToken(a.Center)
	if a.CenterSize > 1 {
		t.Size = a.CenterSize
	}
	return t
}

// reachFeet: how far from its center token a square is still in the area. A Cube centered on a creature covers the creature's
// space and the squares within (side - the creature's width)/2 of it, in whole squares; a Sphere its radius.
func (a obscuredArea) reachFeet() int {
	if a.Shape != "cube" {
		return a.Radius
	}
	size := a.CenterSize
	if size < 1 {
		size = 1
	}
	extra := (a.Radius*2 - size*battleMapStepFeet) / 2
	if extra < 0 {
		extra = 0
	}
	return extra / battleMapStepFeet * battleMapStepFeet
}

// covers: the creature is in the area: on the battle map by its token, else by the names of creatures inside it.
func (a obscuredArea) covers(battle encounterMap, hasMap bool, id int) bool {
	if a.Placed && hasMap {
		if t, ok := battle.token(id); ok {
			return tokenDistanceFeet(a.areaToken(), t) <= a.reachFeet()
		}
	}
	for _, in := range a.Inside {
		if in == id {
			return true
		}
	}
	return false
}

// crosses: the straight line between the two creatures passes through the area (the battle map only).
func (a obscuredArea) crosses(battle encounterMap, hasMap bool, viewerID, targetID int) bool {
	if !a.Placed || !hasMap {
		return false
	}
	v, okV := battle.token(viewerID)
	t, okT := battle.token(targetID)
	if !okV || !okT {
		return false
	}
	return segmentCrossesEmanation(v.Position, t.Position, a.Center, a.reachFeet()+battleMapStepFeet/2)
}

// blindsightReaches: the creature has Blindsight that reaches distFeet (the one sense that perceives through fog and ink: SRD 5.2.1
// "Blindsight ... without relying on sight").
func blindsightReaches(lobbyID, creatureID, distFeet int) bool {
	if distFeet < 1 {
		distFeet = 1
	}
	_, blindsight, _ := creatureVision(lobbyID, creatureID)
	return blindsight >= distFeet
}

// areaBlocksSight: an obscured area or Magical Darkness keeps the viewer from seeing the target (the one answer every reader asks).
func areaBlocksSight(lobbyID, viewerID, targetID int) bool {
	if lobbyID <= 0 || viewerID == 0 || targetID == 0 {
		return false
	}
	if magicalDarknessBlocksSight(lobbyID, viewerID, targetID) {
		return true
	}
	areas := obscuredAreas(lobbyID)
	if len(areas) == 0 {
		return false
	}
	battle, hasMap := activeEncounterMap(lobbyID)
	dist := 0
	if hasMap {
		if v, ok := battle.token(viewerID); ok {
			if t, ok := battle.token(targetID); ok {
				dist = tokenDistanceFeet(v, t)
			}
		}
	}
	for _, a := range areas {
		if !a.Heavy {
			continue // Lightly Obscured hampers sight-based checks only (SRD 5.2.1)
		}
		if !a.covers(battle, hasMap, viewerID) && !a.covers(battle, hasMap, targetID) && !a.crosses(battle, hasMap, viewerID, targetID) {
			continue
		}
		if blindsightReaches(lobbyID, viewerID, dist) {
			continue
		}
		return true
	}
	return false
}

// heavilyObscuredAt: the creature stands in a Heavily Obscured area or Magical Darkness (where it can take the Hide action).
func heavilyObscuredAt(lobbyID, creatureID int) bool {
	if magicalDarknessAt(lobbyID, creatureID) {
		return true
	}
	battle, hasMap := activeEncounterMap(lobbyID)
	for _, a := range obscuredAreas(lobbyID) {
		if a.Heavy && a.covers(battle, hasMap, creatureID) {
			return true
		}
	}
	return false
}

// placeArea fills in where an area is: a point on the battle map (the GM's point, else the first target's square, else the
// source's own), or, without a battle map (or nothing on it to center on), the creatures named as inside it.
func placeArea(lobbyID, sourceID int, a *obscuredArea, point *mapSquare, centerOn int, inside []int) {
	if battle, ok := activeEncounterMap(lobbyID); ok {
		switch {
		case point != nil:
			a.Center, a.Placed = *point, true
		case centerOn != 0:
			if t, ok := battle.token(centerOn); ok {
				a.Center, a.Placed = t.Position, true
				if a.Shape == "cube" {
					a.CenterSize = t.Size
				}
			}
		}
	}
	if !a.Placed {
		a.Inside = append([]int(nil), inside...)
	}
}

// areaWhere: how a reply says where the area is.
func areaWhere(lobbyID int, a obscuredArea) string {
	if a.Placed {
		return fmt.Sprintf("centered on the square (%d, %d)", a.Center.X, a.Center.Y)
	}
	if len(a.Inside) == 0 {
		return "with no battle map and nobody named inside it: it blocks no one's sight until the GM names who is in it (POST /api/gm/obscured-area {\"area_id\": N, \"inside\": [ids]})"
	}
	var names []string
	for _, id := range a.Inside {
		names = append(names, creatureName(lobbyID, id))
	}
	return "around " + strings.Join(names, ", ")
}

// castObscuringSpell makes the area a cast of an obscuring spell fills (a character's or a monster's: the same row). point: the
// GM's point for a monster, nil for a character (centered on its target, else itself); inside: the cast's targets, the creatures in
// it without a battle map. handled is false for a spell that isn't a row.
func castObscuringSpell(lobbyID, casterID int, slug, spellName string, level int, point *mapSquare, targets []int, apply bool) (line string, handled bool) {
	sp, ok := obscuringSpells[slug]
	if !ok {
		return "", false
	}
	if base := srdSpellsMemory[slug].Level; level > base && base > 0 {
		sp.Radius += sp.PerSlot * (level - base)
	}
	a := obscuredArea{Label: spellName, Heavy: sp.Heavy, Shape: sp.Shape, Radius: sp.Radius, Rounds: sp.Rounds,
		Concentration: srdSpellsMemory[slug].Concentration, Spell: spellName}
	centerOn := casterID
	if len(targets) > 0 {
		centerOn = targets[0]
	}
	placeArea(lobbyID, casterID, &a, point, centerOn, targets)
	kind := "Lightly"
	if a.Heavy {
		kind = "Heavily"
	}
	what := fmt.Sprintf("a %d-foot-radius Sphere that is %s Obscured, %s", a.Radius, kind, areaWhere(lobbyID, a))
	if !apply {
		return fmt.Sprintf("%s would fill %s. Nothing is established.", spellName, what), true
	}
	if _, err := startObscuredArea(lobbyID, casterID, a); err != nil {
		log.Printf("obscured area: couldn't record %s for %d in campaign %d: %v", spellName, casterID, lobbyID, err)
		return fmt.Sprintf("%s: the area could not be recorded; nothing is established. Try again.", spellName), true
	}
	return fmt.Sprintf("%s fills %s. Creatures in it, or looking through it, can't see what is on the other side (Blindsight still perceives); it lasts while the caster concentrates, or until %s disperses it (the GM: POST /api/gm/obscured-area {\"end\": true}).",
		spellName, what, sp.DispersedBy), true
}

// resolveObscuringText makes the area a stat block entry fills (an octopus's Ink Cloud): a Cube centered on the monster, where it is
// now. The entry's movement ("moves up to its Swim Speed") is the GM's on the battle map.
func resolveObscuringText(r *http.Request, lobbyID, monsterID int, entryName, text string, targets []int, apply bool) []string {
	m := obscuringTextRe.FindStringSubmatch(text)
	size, _ := strconv.Atoi(m[1])
	n, _ := strconv.Atoi(m[4])
	shape := strings.ToLower(m[2])
	a := obscuredArea{Label: entryName, Heavy: strings.EqualFold(m[3], "Heavily"), Rounds: durationRounds(n, m[5])}
	if shape == "cube" {
		a.Shape, a.Radius = "cube", size/2
	} else {
		a.Shape, a.Radius = "sphere", size
	}
	inside := append([]int{monsterID}, targets...)
	placeArea(lobbyID, monsterID, &a, nil, monsterID, inside)
	name := creatureName(lobbyID, monsterID)
	dispersal := strings.TrimSpace(m[6])
	if dispersal == "" {
		dispersal = "the GM ends it"
	}
	kind := "Lightly"
	if a.Heavy {
		kind = "Heavily"
	}
	what := fmt.Sprintf("a %d-foot %s %s Obscured for %s %s, %s", size, titleCase(shape), kind, m[4], m[5], areaWhere(lobbyID, a))
	if !apply {
		return []string{fmt.Sprintf("%s: %s would release %s. Nothing is established.", entryName, name, what)}
	}
	if _, err := startObscuredArea(lobbyID, monsterID, a); err != nil {
		log.Printf("obscured area: couldn't record %s for %d in campaign %d: %v", entryName, monsterID, lobbyID, err)
		return []string{fmt.Sprintf("%s: the area could not be recorded; nothing is established. Try again.", entryName)}
	}
	markExecuted(r) // the server established it: a limited use is counted (monster_day_uses.go)
	line := fmt.Sprintf("%s: %s releases %s, or until %s (the GM ends it then: POST /api/gm/obscured-area {\"end\": true}).", entryName, name, what, dispersal)
	if strings.Contains(strings.ToLower(text), "moves up to") {
		line += " Its movement is the GM's to make on the battle map."
	}
	return []string{line}
}

// creatureOfCampaign: the id is a character in the campaign, one of its active companions, or a creature of its fight.
func creatureOfCampaign(campaignID, id int) bool {
	switch {
	case id == 0:
		return false
	case isCompanionCombatID(id):
		_, ok := activeCompanionInCampaign(campaignID, id)
		return ok
	case id > 0:
		return lobbyOfChar(id) == campaignID
	}
	for _, e := range combatEntries(campaignID) {
		if asInt(e["id"]) == id {
			return true
		}
	}
	return false
}

// spellAreaTargets: every creature a character's cast names (target_id and target_ids: characters, and monsters by their negative
// ids), the creatures an area is centered on or holds without a battle map.
func spellAreaTargets(charID int) []int {
	var out []int
	for _, m := range []interface{ Load(any) (any, bool) }{&attackTargets, &aimedTargets} { // target_id (a monster), target_character_id
		if v, ok := m.Load(charID); ok {
			if id, _ := v.(int); id != 0 && !containsInt(out, id) {
				out = append(out, id)
			}
		}
	}
	if v, ok := castTargets.Load(charID); ok {
		for _, id := range v.([]int) {
			if id != 0 && !containsInt(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// obscuredAreaView: an area as the GM's reply lists it.
func obscuredAreaView(lobbyID int, a obscuredArea) map[string]interface{} {
	v := map[string]interface{}{"area_id": a.ID, "label": a.Label, "heavily_obscured": a.Heavy, "shape": a.Shape, "radius": a.Radius,
		"where": areaWhere(lobbyID, a), "source": creatureName(lobbyID, a.Source)}
	if a.Rounds > 0 {
		v["rounds_left"] = a.Round + a.Rounds - currentRound(lobbyID)
	}
	if a.Concentration {
		v["concentration"] = a.Spell
	}
	return v
}

// handleGMObscuredArea godoc
// @Summary The obscured areas of a fight (GM)
// @Description Lists the campaign's obscured areas (fog, ink: Heavily Obscured spaces made by a spell or a stat block) and lets the GM change one: "end": true ends it (a strong wind or current dispersed it); "inside": [ids] names the creatures in it, for a fight without a battle map (on a battle map the area's square decides). A creature in a Heavily Obscured area, or looking through one, can't see past it: attack rolls and the actions that need a creature it can see read it.
// @Tags GM
// @Accept json
// @Produce json
// @Security BasicAuth
// @Body The area (area_id) and the change; no area_id only lists them
// @Success 200 {object} map[string]interface{} "The areas, after the change"
// @Router /gm/obscured-area [post]
func handleGMObscuredArea(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		AreaID int   `json:"area_id"` // the area to change (GET-like without it: the reply lists the areas)
		End    bool  `json:"end"`     // true: the area ends (a strong wind or current dispersed it)
		Inside []int `json:"inside"`  // the creatures in the area, for a fight without a battle map (characters' ids, monsters' negative ids)
	}
	campaignID, ok := gmReadAs(w, r, agentID, &req)
	if !ok {
		return
	}
	for _, id := range req.Inside { // every creature named is one of this campaign's: a character, a companion, a monster of its fight
		if !creatureOfCampaign(campaignID, id) {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "creature_not_found", "id": id,
				"message": fmt.Sprintf("%d is no character, companion or monster of this campaign's fight. Nothing was changed.", id)})
			return
		}
	}
	note := ""
	if req.AreaID != 0 {
		var text string
		if err := db.QueryRow(`SELECT text FROM combat_effects WHERE id=$1 AND lobby_id=$2 AND kind='obscured_area'`, req.AreaID, campaignID).Scan(&text); err != nil {
			if answerDBFailure(w, err, "the area") {
				return
			}
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "area_not_found",
				"message": "No obscured area with that area_id in this campaign (it may have ended). The reply's areas list the live ones. Nothing was changed."})
			return
		}
		switch {
		case req.End:
			if _, err := db.Exec(`DELETE FROM combat_effects WHERE id=$1`, req.AreaID); err != nil {
				writeDBWriteError(w, err, "the area")
				return
			}
			note = "The area is gone."
		case req.Inside != nil:
			var a obscuredArea
			json.Unmarshal([]byte(text), &a)
			a.Inside = req.Inside
			b, _ := json.Marshal(a)
			if _, err := db.Exec(`UPDATE combat_effects SET text=$1 WHERE id=$2`, string(b), req.AreaID); err != nil {
				writeDBWriteError(w, err, "the area")
				return
			}
			note = "The creatures in the area are recorded."
			if a.Placed {
				note += " On the battle map the area's square decides who is in it; this list counts only for creatures without a token."
			}
		default:
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "change_required",
				"message": `Send "end": true to end the area, or "inside": [ids] to name the creatures in it. Nothing was changed.`})
			return
		}
	}
	areas := []map[string]interface{}{}
	for _, a := range obscuredAreas(campaignID) {
		areas = append(areas, obscuredAreaView(campaignID, a))
	}
	reply := map[string]interface{}{"success": true, "campaign_id": campaignID, "areas": areas}
	if note != "" {
		reply["message"] = note
	}
	writeJSONStatus(w, http.StatusOK, reply)
}
