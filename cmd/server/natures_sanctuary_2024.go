package main

// Nature's Sanctuary (Circle of the Land, level 14; SRD 5.2.1): as a Magic action the Druid expends a use of Wild Shape and makes
// spectral trees and vines appear in a 15-foot Cube on the ground within 120 feet, for 1 minute or until the Druid is Incapacitated or
// dies. The Druid and allies have Half Cover in the area, and allies gain the current Resistance of the Druid's Nature's Ward there.
// As a Bonus Action the Druid can move the Cube up to 60 feet. On the battle map the Cube is 3 x 3 squares around its centre square.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type sanctuaryZone struct {
	Druid    int
	Center   mapSquare
	EndRound int
}

var sanctuaries sync.Map // lobby id -> sanctuaryZone (one per campaign at a time is plenty)

func activeSanctuary(lobbyID int) (sanctuaryZone, bool) {
	v, ok := sanctuaries.Load(lobbyID)
	if !ok {
		return sanctuaryZone{}, false
	}
	z := v.(sanctuaryZone)
	var hp int
	var dead bool
	db.QueryRow(`SELECT hp, COALESCE(is_dead,false) FROM characters WHERE id = $1`, z.Druid).Scan(&hp, &dead)
	if currentRound(lobbyID) >= z.EndRound || dead || hp <= 0 || hasCondition(z.Druid, "incapacitated") || hasCondition(z.Druid, "unconscious") ||
		hasCondition(z.Druid, "stunned") || hasCondition(z.Druid, "paralyzed") || hasCondition(z.Druid, "petrified") {
		sanctuaries.Delete(lobbyID)
		return z, false
	}
	return z, true
}

// inSanctuary: the character is inside the Cube (and is the Druid or one of the Druid's allies: every character is an ally here).
func inSanctuary(lobbyID, charID int) (sanctuaryZone, bool) {
	z, ok := activeSanctuary(lobbyID)
	if !ok || charID <= 0 {
		return z, false
	}
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return z, false
	}
	t, ok := m.token(charID)
	if !ok {
		return z, false
	}
	dx, dy := t.Position.X-z.Center.X, t.Position.Y-z.Center.Y
	return z, dx >= -1 && dx <= 1 && dy >= -1 && dy <= 1
}

// sanctuaryCover: Half Cover (+2 AC and Dexterity saves) inside the Cube.
func sanctuaryCover(lobbyID, charID int) int {
	if _, ok := inSanctuary(lobbyID, charID); ok {
		return 2
	}
	return 0
}

// sanctuaryResists: allies inside the Cube have the Resistance of the Druid's Nature's Ward.
func sanctuaryResists(charID int, dtype string) bool {
	lobby := lobbyOfChar(charID)
	z, ok := inSanctuary(lobby, charID)
	if !ok || z.Druid == charID {
		return false
	}
	return map[string]string{"arid": "fire", "polar": "cold", "temperate": "lightning", "tropical": "poison"}[landTypeOf(z.Druid)] == strings.ToLower(dtype)
}

// parseSquare reads "at 4,7" / "x 4 y 7" from a description; ok is false if there is none.
func parseSquare(description string) (mapSquare, bool) {
	var x, y int
	d := strings.ToLower(description)
	for _, f := range []string{"at %d,%d", "at %d, %d", "to %d,%d", "to %d, %d", "x %d y %d", "x=%d y=%d", "(%d,%d)", "(%d, %d)"} {
		if i := strings.Index(d, strings.SplitN(f, "%", 2)[0]); i >= 0 {
			if n, _ := fmt.Sscanf(d[i:], f, &x, &y); n == 2 {
				return mapSquare{X: x, Y: y}, true
			}
		}
	}
	return mapSquare{}, false
}

// naturesSanctuary is the Magic action (move=false) or the Bonus Action that moves the Cube (move=true).
func naturesSanctuary(charID int, description string, move bool) string {
	class, sub, level := subclassOf(charID)
	if class != "druid" || sub != "land" || level < 14 {
		return "Nature's Sanctuary is a feature of a level 14 Druid of the Circle of the Land."
	}
	lobby := lobbyOfChar(charID)
	m, hasMap := activeEncounterMap(lobby)
	self, onMap := m.token(charID)
	center, given := parseSquare(description)
	if !given && hasMap && onMap {
		center = self.Position
	}
	if hasMap && onMap && tokenDistanceFeet(self, mapToken{Position: center, Size: 1}) > 120 {
		return "The Cube must be on ground within 120 feet of you."
	}
	if move {
		z, ok := activeSanctuary(lobby)
		if !ok || z.Druid != charID {
			return "You have no Nature's Sanctuary to move."
		}
		if !given {
			return "Say where to move the Cube: \"move sanctuary to 6,4\" (map squares; up to 60 feet)."
		}
		if dx, dy := abs(center.X-z.Center.X), abs(center.Y-z.Center.Y); maxInt(dx, dy)*5 > 60 {
			return "You can move the Cube up to 60 feet."
		}
		z.Center = center
		sanctuaries.Store(lobby, z)
		return fmt.Sprintf("🌳 You move the spectral trees and vines: the Cube now centres on square %d,%d.", center.X, center.Y)
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(class_resources_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	used := map[string]int{}
	json.Unmarshal(raw, &used)
	max := game.ClassTableInt("druid", level, "Wild Shape")
	if used["wild_shape"] >= max {
		return fmt.Sprintf("Nature's Sanctuary expends a use of Wild Shape, and you have none left (%d/%d used).", used["wild_shape"], max)
	}
	used["wild_shape"]++
	b, _ := json.Marshal(used)
	db.Exec(`UPDATE characters SET class_resources_used = $1 WHERE id = $2`, b, charID)
	sanctuaries.Store(lobby, sanctuaryZone{Druid: charID, Center: center, EndRound: currentRound(lobby) + 10})
	ward := map[string]string{"arid": "Fire", "polar": "Cold", "temperate": "Lightning", "tropical": "Poison"}[landTypeOf(charID)]
	out := fmt.Sprintf("🌳 Nature's Sanctuary (a Wild Shape use, %d/%d used): spectral trees and vines fill a 15-foot Cube", used["wild_shape"], max)
	if hasMap {
		out += fmt.Sprintf(" centred on square %d,%d", center.X, center.Y)
	}
	out += " for 1 minute. You and your allies have Half Cover there"
	if ward != "" {
		out += fmt.Sprintf(", and your allies have Resistance to %s damage there (Nature's Ward)", ward)
	}
	return out + ". Move it as a Bonus Action: \"move_sanctuary\" with \"to x,y\"."
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
