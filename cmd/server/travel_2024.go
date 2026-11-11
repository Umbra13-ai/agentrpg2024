package main

// Travel (SRD 5.2.1 "Playing the Game: Travel" and "Gameplay Toolbox: Travel Pace"): Fast, Normal and Slow paces and their effects on
// checks, the maximum pace by terrain (a good road raises it one step), mounts at double distance for 1 hour, land vehicles at a
// normal pace, waterborne and airborne vehicles at their own speed (up to 24 hours a day), special movement (miles per hour = Speed
// / 10), and Extended Travel past 8 hours a day (Constitution saves against Exhaustion).

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var paceMPH = map[string]float64{"fast": 4, "normal": 3, "slow": 2}
var paceMilesPerDay = map[string]float64{"fast": 30, "normal": 24, "slow": 18}
var paceRank = map[string]int{"slow": 0, "normal": 1, "fast": 2}
var paceByRank = []string{"slow", "normal", "fast"}

type travelTerrain struct {
	MaxPace              string
	Encounter            string // encounter distance dice (× 10 feet)
	Forage, Navigate, Search int
}

// The Travel Terrain table.
var travelTerrains = map[string]travelTerrain{
	"arctic":     {"fast", "6d6", 20, 10, 10},
	"coastal":    {"normal", "2d10", 10, 5, 15},
	"desert":     {"normal", "6d6", 20, 10, 10},
	"forest":     {"normal", "2d8", 10, 15, 15},
	"grassland":  {"fast", "6d6", 15, 5, 15},
	"hill":       {"normal", "2d10", 15, 10, 15},
	"mountain":   {"slow", "4d10", 20, 15, 20},
	"swamp":      {"slow", "2d8", 10, 15, 20},
	"underdark":  {"normal", "2d6", 20, 10, 20},
	"urban":      {"normal", "2d6", 20, 15, 15},
	"waterborne": {"", "6d6", 15, 10, 15},
}

// The Airborne and Waterborne Vehicles table (speed in miles per hour).
var vehicleMPH = map[string]float64{"airship": 8, "galley": 4, "keelboat": 1, "longship": 3, "rowboat": 1.5, "sailing ship": 2, "warship": 2.5}

var landVehicles = map[string]bool{"carriage": true, "cart": true, "chariot": true, "sled": true, "wagon": true}

// travelPaceEffect: the current travel pace's effect on a check with a skill (Fast: Disadvantage on Perception, Survival and Stealth;
// Normal: Disadvantage on Stealth; Slow: Advantage on Perception and Survival).
func travelPaceEffect(lobbyID int, skill string) (adv, dis bool, note string) {
	if db == nil || lobbyID == 0 {
		return
	}
	var pace string
	db.QueryRow(`SELECT COALESCE(travel_pace,'') FROM lobbies WHERE id = $1`, lobbyID).Scan(&pace)
	skill = strings.ReplaceAll(strings.ToLower(skill), "_", " ")
	look := skill == "perception" || skill == "survival"
	switch {
	case pace == "fast" && (look || skill == "stealth"):
		return false, true, " [Fast travel pace: Disadvantage]"
	case pace == "normal" && skill == "stealth":
		return false, true, " [Normal travel pace: Disadvantage on Stealth]"
	case pace == "slow" && look:
		return true, false, " [Slow travel pace: Advantage]"
	}
	return
}

func setTravelPace(lobbyID int, pace string) {
	db.Exec(`UPDATE lobbies SET travel_pace = $1 WHERE id = $2`, pace, lobbyID)
}

func fmtMiles(m float64) string {
	if m == math.Trunc(m) {
		return fmt.Sprintf("%.0f", m)
	}
	return fmt.Sprintf("%.1f", m)
}

func fmtHours(h float64) string {
	whole := int(h)
	mins := int(math.Round((h - float64(whole)) * 60))
	if mins == 60 {
		whole, mins = whole+1, 0
	}
	switch {
	case whole == 0:
		return fmt.Sprintf("%d minutes", mins)
	case mins == 0:
		return fmt.Sprintf("%d hours", whole)
	}
	return fmt.Sprintf("%d hours %d minutes", whole, mins)
}

// @Summary Travel
// @Description Overland or water travel by the SRD 5.2.1 travel rules. Send miles (how far) or hours (how long; or days, 8 hours of travel each), a pace (fast, normal, slow), and optionally terrain (arctic, coastal, desert, forest, grassland, hill, mountain, swamp, underdark, urban, waterborne), road=true for a good road (raises the terrain's maximum pace one step), mode (foot, mounted, a land vehicle: cart, carriage, chariot, sled, wagon; or a ship: airship, galley, keelboat, longship, rowboat, sailing ship, warship), gallop=true (mounts move twice as far for the first hour, then need a rest), downstream=true (Keelboat or Rowboat: +3 mph), speed (special movement such as flying: miles per hour = Speed / 10; flying ignores terrain), hours_per_day (default 8; ships up to 24). The reply gives distance, time, the terrain's encounter distance, foraging, navigation and search DCs. Time passes (spells end, a Stable character can recover) unless pass_time=false. Each hour past 8 in a day: every character makes a Constitution save (DC 10 + hours past 8) or gains 1 Exhaustion level (not aboard a ship). The pace stays in effect for checks (Fast: Disadvantage on Perception, Survival and Stealth; Normal: Disadvantage on Stealth; Slow: Advantage on Perception and Survival) until {"stop": true} or combat starts. Refused during combat.
// @Tags GM
// @Param request body object{campaign_id=int,miles=number,hours=number,days=number,pace=string,terrain=string,road=bool,mode=string,gallop=bool,downstream=bool,speed=int,hours_per_day=number,pass_time=bool,stop=bool} true "Journey"
// @Router /gm/travel [post]
func handleGMTravel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Miles       float64 `json:"miles"`
		Hours       float64 `json:"hours"`
		Days        float64 `json:"days"`
		Pace        string  `json:"pace"`
		Terrain     string  `json:"terrain"`
		Road        bool    `json:"road"`
		Mode        string  `json:"mode"`
		Gallop      bool    `json:"gallop"`
		Downstream  bool    `json:"downstream"`
		Speed       int     `json:"speed"`
		HoursPerDay float64 `json:"hours_per_day"`
		PassTime    *bool   `json:"pass_time"`
		Stop        bool    `json:"stop"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Stop {
		setTravelPace(lobby, "")
		recordGMAction(db, lobby, "travel", "The journey ends; travel pace no longer affects checks.", "")
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": "Travel stopped: the pace no longer affects checks."})
		return
	}
	var active bool
	db.QueryRow(`SELECT COALESCE(active,false) FROM combat_state WHERE lobby_id = $1`, lobby).Scan(&active)
	if active {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "combat_active", "message": "End combat before travelling."})
		return
	}
	pace := strings.ToLower(strings.TrimSpace(req.Pace))
	if pace == "" {
		pace = "normal"
	}
	if _, ok := paceMPH[pace]; !ok {
		dmBadRequest(w, "invalid_pace", "pace must be fast, normal or slow")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" || mode == "walking" || mode == "on foot" {
		mode = "foot"
	}
	if mode == "horse" || mode == "riding" || mode == "mount" {
		mode = "mounted"
	}
	terrainKey := strings.ToLower(strings.TrimSpace(req.Terrain))
	terrain, hasTerrain := travelTerrains[terrainKey]
	if terrainKey != "" && !hasTerrain {
		dmBadRequest(w, "unknown_terrain", "terrain must be arctic, coastal, desert, forest, grassland, hill, mountain, swamp, underdark, urban or waterborne")
		return
	}
	var notes []string
	ship, isShip := vehicleMPH[mode]
	if mode != "foot" && mode != "mounted" && !isShip && !landVehicles[mode] {
		dmBadRequest(w, "unknown_mode", "mode must be foot, mounted, a land vehicle (cart, carriage, chariot, sled, wagon) or a ship (airship, galley, keelboat, longship, rowboat, sailing ship, warship)")
		return
	}
	hoursPerDay := req.HoursPerDay
	if hoursPerDay <= 0 {
		hoursPerDay = 8
	}
	var mph float64
	effectPace := pace
	switch {
	case isShip:
		mph = ship
		effectPace = "" // characters aboard a vessel don't choose a travel pace
		if req.Downstream && (mode == "keelboat" || mode == "rowboat") {
			mph += 3
			notes = append(notes, "Going downstream adds the current (3 mph).")
		}
		if req.HoursPerDay <= 0 && mode != "rowboat" && mode != "keelboat" {
			notes = append(notes, "With a full crew working in shifts, a ship can travel up to 24 hours a day (send hours_per_day).")
		}
	case req.Speed > 0:
		// Special movement: miles per hour = Speed / 10; Fast is a third more, Slow two thirds
		mph = float64(req.Speed) / 10
		switch pace {
		case "fast":
			mph = math.Floor(mph*hoursPerDay*4/3) / hoursPerDay
		case "slow":
			mph = math.Floor(mph*hoursPerDay*2/3) / hoursPerDay
		}
		notes = append(notes, fmt.Sprintf("Special movement: Speed %d feet is %s miles per hour at a Normal pace.", req.Speed, fmtMiles(float64(req.Speed)/10)))
	default:
		if hasTerrain && terrain.MaxPace != "" {
			max := paceRank[terrain.MaxPace]
			if req.Road && max < 2 {
				max++
				notes = append(notes, "A good road raises the maximum pace one step.")
			}
			if paceRank[pace] > max {
				pace, effectPace = paceByRank[max], paceByRank[max]
				notes = append(notes, fmt.Sprintf("%s terrain allows at most a %s pace.", titleCase(terrainKey), pace))
			}
			if terrainKey == "arctic" && pace == "fast" {
				notes = append(notes, "A Fast pace in Arctic terrain needs suitable equipment such as skis.")
			}
		}
		mph = paceMPH[pace]
		if landVehicles[mode] {
			notes = append(notes, fmt.Sprintf("A %s travels at the group's pace.", mode))
		}
	}

	// Distance and time
	hours := req.Hours + req.Days*hoursPerDay
	var miles float64
	galloped := mode == "mounted" && req.Gallop
	distFor := func(h float64) float64 {
		if galloped {
			fast := math.Min(h, 1)
			return fast*mph*2 + (h-fast)*mph
		}
		return h * mph
	}
	switch {
	case req.Miles > 0:
		miles = req.Miles
		if galloped && miles > 2*mph {
			hours = 1 + (miles-2*mph)/mph
		} else if galloped {
			hours = miles / (2 * mph)
		} else {
			hours = miles / mph
		}
	case hours > 0:
		miles = distFor(hours)
	default:
		dmBadRequest(w, "distance_or_time_required", "Send miles, hours or days.")
		return
	}
	if galloped {
		notes = append(notes, "The mounts moved at double distance for the first hour; they need a Short or Long Rest before they can do it again.")
	}
	days := math.Floor(hours / hoursPerDay)
	rest := hours - days*hoursPerDay
	timeText := fmtHours(hours)
	if hours > hoursPerDay && rest < 0.01 {
		timeText = fmt.Sprintf("%s of travel (%.0f days of %s hours)", fmtHours(hours), days, fmtMiles(hoursPerDay))
	} else if hours > hoursPerDay {
		timeText = fmt.Sprintf("%s of travel (%.0f day(s) of %s hours, then %s)", fmtHours(hours), days, fmtMiles(hoursPerDay), fmtHours(rest))
	}
	out := map[string]interface{}{"success": true, "miles": math.Round(miles*10) / 10, "hours": math.Round(hours*100) / 100, "mph": mph, "mode": mode}
	if effectPace != "" {
		out["pace"] = effectPace
	}
	var lines []string
	how := map[string]string{"foot": "on foot", "mounted": "mounted"}[mode]
	if how == "" {
		how = "by " + mode
	}
	if req.Speed > 0 && !isShip {
		how = fmt.Sprintf("with special movement (Speed %d feet)", req.Speed)
	}
	if effectPace != "" {
		lines = append(lines, fmt.Sprintf("Travel %s at a %s pace: %s miles in %s.", how, effectPace, fmtMiles(math.Round(miles*10)/10), timeText))
	} else {
		lines = append(lines, fmt.Sprintf("Travel %s at %s mph: %s miles in %s.", how, fmtMiles(mph), fmtMiles(math.Round(miles*10)/10), timeText))
	}
	if !isShip && req.Speed == 0 {
		lines = append(lines, fmt.Sprintf("(A %s pace covers %s miles in a day of 8 hours.)", pace, fmtMiles(paceMilesPerDay[pace])))
	}
	if hasTerrain {
		out["terrain"] = map[string]interface{}{"name": terrainKey, "max_pace": terrain.MaxPace, "encounter_distance": terrain.Encounter + " x 10 feet",
			"foraging_dc": terrain.Forage, "navigation_dc": terrain.Navigate, "search_dc": terrain.Search}
		lines = append(lines, fmt.Sprintf("%s: encounter distance %s x 10 feet (rolled now: %d feet); foraging DC %d, navigation DC %d, search DC %d.",
			titleCase(terrainKey), terrain.Encounter, game.RollDamage(terrain.Encounter, false)*10, terrain.Forage, terrain.Navigate, terrain.Search))
	}
	lines = append(lines, notes...)
	if mo := marchingOrderNames(lobby); len(mo) > 0 {
		lines = append(lines, "Marching order (front to back): "+strings.Join(mo, ", ")+".")
	}

	// Extended Travel: each hour past 8 in a day, a Constitution save (DC 10 + 1 per hour past 8) or 1 Exhaustion level
	if !isShip && hoursPerDay > 8 {
		fullDays := int(math.Ceil(hours / hoursPerDay))
		var log []string
		rows, err := db.Query(`SELECT id, name FROM characters WHERE lobby_id = $1 AND NOT COALESCE(is_dead,false) AND hp > 0 ORDER BY id`, lobby)
		if err == nil {
			type pc struct {
				id   int
				name string
			}
			var pcs []pc
			for rows.Next() {
				var p pc
				if rows.Scan(&p.id, &p.name) == nil {
					pcs = append(pcs, p)
				}
			}
			rows.Close()
			for d := 0; d < fullDays; d++ {
				dayHours := math.Min(hoursPerDay, hours-float64(d)*hoursPerDay)
				for extra := 1; float64(8+extra) <= dayHours+0.0001; extra++ {
					for _, p := range pcs {
						saved, total := creatureSave(lobby, p.id, "con", 10+extra)
						if !saved {
							log = append(log, fmt.Sprintf("%s (day %d, hour %d): Constitution save %d vs DC %d, failure: %s", p.name, d+1, 8+extra, total, 10+extra, addExhaustion(p.id, 1)))
						}
					}
				}
			}
		}
		if len(log) > 0 {
			out["extended_travel"] = log
			lines = append(lines, "Extended travel: "+strings.Join(log, "; "))
		} else {
			lines = append(lines, "Extended travel: every character kept going without tiring.")
		}
	}

	// Time passes
	if req.PassTime == nil || *req.PassTime {
		ended := passSpellTime(lobby, int(math.Round(hours*600)))
		if len(ended) > 0 {
			out["ended"] = ended
			lines = append(lines, strings.Join(ended, " "))
		}
	}
	setTravelPace(lobby, effectPace)
	if effectPace != "" {
		lines = append(lines, map[string]string{
			"fast":   "Fast pace: Disadvantage on Wisdom (Perception or Survival) and Dexterity (Stealth) checks while travelling.",
			"normal": "Normal pace: Disadvantage on Dexterity (Stealth) checks while travelling.",
			"slow":   "Slow pace: Advantage on Wisdom (Perception or Survival) checks while travelling.",
		}[effectPace])
	}
	result := strings.Join(lines, " ")
	out["result"] = result
	recordGMAction(db, lobby, "travel", result, result)
	writeJSONStatus(w, 200, out)
}
