package main

// Weapon Mastery (2024 rules). Barbarians, Fighters, Paladins, Rangers and
// Rogues unlock the Mastery property of a few kinds of weapon. When one of
// those weapons is used in an attack, the attack result lists what the
// property does; the GM compares the roll with the target's AC and applies it.
//	GET  /api/characters/weapon-masteries?character_id=N    your choices, how many you may have, eligible weapons
//	POST /api/characters/weapon-masteries                   {"character_id": N, "weapon_masteries": ["longsword", ...]}
// After a Long Rest one choice may be swapped. New choices come with levels.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// masteryCount is how many kinds of weapon a class may have mastery with.
func masteryCount(class string, level int) int {
	switch strings.ToLower(class) {
	case "barbarian":
		switch {
		case level >= 10:
			return 4
		case level >= 4:
			return 3
		}
		return 2
	case "fighter":
		switch {
		case level >= 16:
			return 6
		case level >= 10:
			return 5
		case level >= 4:
			return 4
		}
		return 3
	case "paladin", "ranger", "rogue":
		return 2
	}
	return 0
}

// weaponMasteryEligible says whether a class may choose a weapon.
func weaponMasteryEligible(class string, w SRDWeapon) bool {
	if w.Mastery == "" {
		return false
	}
	cat := strings.ToLower(w.Category)
	if cat != "simple" && cat != "martial" {
		return false
	}
	props := strings.ToLower(strings.Join(w.Properties, " "))
	switch strings.ToLower(class) {
	case "barbarian":
		return w.Type == "melee"
	case "fighter":
		return true
	case "paladin", "ranger":
		return true // they are proficient with Simple and Martial weapons
	case "rogue": // Simple weapons, and Martial weapons that have Finesse or Light
		return cat == "simple" || strings.Contains(props, "finesse") || strings.Contains(props, "light")
	}
	return false
}

// masteryChoice is a weapon a class can choose, for listings.
type masteryChoice struct {
	Key     string
	Name    string
	Mastery string
}

func eligibleMasteryWeapons(class string) []masteryChoice {
	var out []masteryChoice
	seen := map[string]bool{}
	for key, w := range srdWeapons {
		if strings.Contains(w.Name, ",") || !weaponMasteryEligible(class, w) {
			continue
		}
		name := titleWords(w.Name)
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, masteryChoice{key, name, w.Mastery})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// resolveMasteryPick turns "Longsword" or "light crossbow" into the weapon's key.
func resolveMasteryPick(class, input string) (string, bool) {
	want := weaponNameKey(input)
	for _, c := range eligibleMasteryWeapons(class) {
		if weaponNameKey(c.Name) == want || c.Key == strings.ToLower(strings.ReplaceAll(input, " ", "_")) {
			return c.Key, true
		}
	}
	return "", false
}

// validateMasteryPicks checks a list of weapon choices for a class.
// exact is true at creation, when the full number must be chosen.
func validateMasteryPicks(class string, level int, picks []string, exact bool) ([]string, *originError) {
	max := masteryCount(class, level)
	if max == 0 {
		return nil, nil
	}
	var names []string
	for _, c := range eligibleMasteryWeapons(class) {
		names = append(names, c.Name+" ("+c.Mastery+")")
	}
	var keys []string
	seen := map[string]bool{}
	for _, p := range picks {
		k, ok := resolveMasteryPick(class, p)
		if !ok {
			return nil, originErr("invalid_weapon_mastery", names, "%q is not a weapon a %s can choose for Weapon Mastery", p, titleWords(class))
		}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) > max || (exact && len(keys) != max) {
		return nil, originErr("weapon_masteries_required", names, "A %s at level %d chooses %d kinds of weapon for Weapon Mastery (send weapon_masteries)", titleWords(class), level, max)
	}
	return keys, nil
}

func characterMasteries(charID int) []string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(weapon_masteries, '[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var keys []string
	json.Unmarshal(raw, &keys)
	return keys
}

var masteryText = map[string]string{
	"Cleave": "if this melee attack hits, make another attack roll with the weapon against a second creature within 5 feet of the first that is also in your reach; on a hit it takes the weapon's damage without your ability modifier (unless negative). Once per turn.",
	"Nick":   "the extra attack of the Light property can be made as part of the Attack action instead of as a Bonus Action. Once per turn.",
	"Push":   "on a hit you can push a Large or smaller target up to 10 feet straight away from you.",
	"Sap":    "on a hit the target has Disadvantage on its next attack roll before the start of your next turn.",
	"Slow":   "on a hit that deals damage you can reduce the target's Speed by 10 feet until the start of your next turn (it does not stack).",
	"Vex":    "on a hit that deals damage you have Advantage on your next attack roll against that target before the end of your next turn.",
}

// masteryNote is what the weapon's Mastery property adds to an attack, or "" if
// the character has not unlocked it for this weapon.
func masteryNote(charID int, weaponKey string, w SRDWeapon, abilityMod, level int) string {
	if w.Mastery == "" || weaponKey == "" {
		return ""
	}
	have := false
	for _, k := range characterMasteries(charID) {
		if weaponNameKey(k) == weaponNameKey(weaponKey) || k == weaponKey {
			have = true
		}
	}
	if !have {
		return ""
	}
	if v, ok := masteryUses.Load(charID); ok { // the mastery this attack uses: Tactical Master's Push, Sap or Slow (F212)
		if mu, ok := v.(masteryUse); ok && mu.Name != "" {
			w.Mastery = mu.Name
		}
	}
	switch w.Mastery {
	case "Graze":
		if abilityMod <= 0 {
			return " [Mastery Graze: on a miss the target takes no damage because your ability modifier is not positive]"
		}
		return fmt.Sprintf(" [Mastery Graze: if this attack misses, the target takes %d %s damage (your ability modifier)]", abilityMod, w.DamageType)
	case "Cleave":
		// only a reminder: the second attack happens when the player names a different creature (cleave_target_id)
		return " [Cleave available: add cleave_target_id for a different creature within 5 feet of the target, once per turn]"
	case "Topple":
		return fmt.Sprintf(" [Mastery Topple: on a hit the target makes a Constitution saving throw, DC %d (8 + %d + %d), or has the Prone condition]",
			8+abilityMod+game.ProficiencyBonus(level), abilityMod, game.ProficiencyBonus(level))
	}
	if t, ok := masteryText[w.Mastery]; ok {
		return " [Mastery " + w.Mastery + ": " + t + "]"
	}
	return ""
}

func handleWeaponMasteries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		CharacterID     int      `json:"character_id"`
		WeaponMasteries []string `json:"weapon_masteries"`
	}
	if r.Method == http.MethodGet {
		fmt.Sscanf(r.URL.Query().Get("character_id"), "%d", &req.CharacterID)
	} else if r.Method == http.MethodPost {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req)
	} else {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var owner, level int
	var class string
	var swapUsed bool
	if db.QueryRow(`SELECT agent_id, class, level, COALESCE(mastery_swap_used, false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&owner, &class, &level, &swapUsed) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_owner"})
		return
	}
	max := masteryCount(class, level)
	if max == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_weapon_mastery", "message": titleWords(class) + " does not have the Weapon Mastery feature."})
		return
	}
	current := characterMasteries(req.CharacterID)
	describe := func(keys []string) []map[string]string {
		out := []map[string]string{}
		for _, k := range keys {
			if wp, ok := srdWeapons[k]; ok {
				out = append(out, map[string]string{"weapon": titleWords(wp.Name), "mastery": wp.Mastery})
			}
		}
		return out
	}
	if r.Method == http.MethodGet {
		var elig []map[string]string
		for _, c := range eligibleMasteryWeapons(class) {
			elig = append(elig, map[string]string{"weapon": c.Name, "mastery": c.Mastery})
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"weapon_masteries": describe(current), "allowed": max, "swap_available": !swapUsed,
			"eligible": elig, "how_to_change": "POST {\"character_id\": N, \"weapon_masteries\": [...]}: after a Long Rest you can swap one choice; new slots from levels can be filled freely."})
		return
	}
	picks, perr := validateMasteryPicks(class, level, req.WeaponMasteries, false)
	if perr != nil {
		writeOriginError(w, perr)
		return
	}
	removed := 0
	for _, o := range current {
		if !containsStr(picks, o) {
			removed++
		}
	}
	// slots that were never filled (a new level) can be filled without a swap
	if removed > 1 || (removed == 1 && swapUsed) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "swap_not_available",
			"message": "You can change only one Weapon Mastery choice each time you finish a Long Rest."})
		return
	}
	b, _ := json.Marshal(picks)
	db.Exec(`UPDATE characters SET weapon_masteries = $1, mastery_swap_used = $2 WHERE id = $3`, b, swapUsed || removed == 1, req.CharacterID)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "weapon_masteries": describe(picks), "allowed": max})
}
