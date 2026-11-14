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

// eligibleMasteryWeapons lists each weapon a class can master once. The same weapon can sit in srdWeapons under two keys (the
// built-in "light_crossbow" and the database slug "light-crossbow"); the map's order is random, so the keys are walked in sorted
// order, weapons are told apart by weaponNameKey, and the built-in key (the name with underscores) is kept when there is one.
// Otherwise a crossbow resolved to a different key from call to call, and two spellings passed as two weapons.
func eligibleMasteryWeapons(class string) []masteryChoice {
	keys := make([]string, 0, len(srdWeapons))
	for k := range srdWeapons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	byName := map[string]int{}
	var out []masteryChoice
	for _, key := range keys {
		w := srdWeapons[key]
		if strings.Contains(w.Name, ",") || !weaponMasteryEligible(class, w) {
			continue
		}
		nk := weaponNameKey(w.Name)
		if i, ok := byName[nk]; ok {
			if key == masteryCanonicalKey(w.Name) {
				out[i].Key = key
			}
			continue
		}
		byName[nk] = len(out)
		out = append(out, masteryChoice{key, titleWords(w.Name), w.Mastery})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// masteryCanonicalKey is the built-in key form of a weapon name: "Light Crossbow" -> "light_crossbow".
func masteryCanonicalKey(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "_")
}

// stripMasterySuffix drops the "(Nick)" the listings show after a weapon: "Dagger (Nick)" -> "Dagger".
func stripMasterySuffix(input string) string {
	s := strings.TrimSpace(input)
	if i := strings.LastIndex(s, "("); i > 0 && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// resolveMasteryPick turns "Longsword", "light crossbow" or "Dagger (Nick)" into the weapon's key.
func resolveMasteryPick(class, input string) (string, bool) {
	input = stripMasterySuffix(input)
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
		// "Dagger (Vex)": a weapon's mastery property is fixed, so a suffix naming another one is refused, not swapped for the real
		// one without a word (Nov 14 audit re-check: the character got Dagger (Nick))
		if s := strings.TrimSpace(p); strings.HasSuffix(s, ")") {
			if i := strings.LastIndex(s, "("); i > 0 {
				if named := strings.TrimSpace(s[i+1 : len(s)-1]); named != "" && !strings.EqualFold(named, srdWeapons[k].Mastery) {
					return nil, originErr("invalid_weapon_mastery", names, "%s has the %s mastery property, not %s: a weapon's mastery is fixed. Send %q", titleWords(srdWeapons[k].Name), srdWeapons[k].Mastery, named, titleWords(srdWeapons[k].Name))
				}
			}
		}
		// the same weapon twice is refused, not merged (two spellings of a crossbow passed as two weapons, or one was dropped)
		nk := weaponNameKey(srdWeapons[k].Name)
		if seen[nk] {
			return nil, originErr("duplicate_weapon_mastery", names, "You chose %s twice: choose %d different weapons for Weapon Mastery", titleWords(srdWeapons[k].Name), max)
		}
		seen[nk] = true
		keys = append(keys, k)
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

// masterySummary: the character's Weapon Mastery choices for its sheet, each with what the property does (Nov 14 tester: the main
// character record showed none, though they were saved).
func masterySummary(charID int) []map[string]string {
	out := []map[string]string{}
	for _, k := range characterMasteries(charID) {
		wp, ok := srdWeapons[k]
		if !ok {
			out = append(out, map[string]string{"weapon": titleWords(strings.ReplaceAll(k, "-", " "))})
			continue
		}
		m := map[string]string{"weapon": titleWords(wp.Name), "mastery": wp.Mastery}
		if t, ok := masteryText[wp.Mastery]; ok {
			m["what_it_does"] = t
		} else if t, ok := masteryTextMore[wp.Mastery]; ok {
			m["what_it_does"] = t
		}
		out = append(out, m)
	}
	return out
}

// masteryTextMore: the two masteries whose effect depends on the attack (masteryNote works the numbers out); the SRD 5.2.1 wording.
var masteryTextMore = map[string]string{
	"Graze":  "If your attack roll with this weapon misses a creature, you can deal damage to that creature equal to the ability modifier you used to make the attack roll. This damage is the same type dealt by the weapon, and the damage can be increased only by increasing the ability modifier.",
	"Topple": "If you hit a creature with this weapon, you can force the creature to make a Constitution saving throw (DC 8 plus the ability modifier used to make the attack roll and your Proficiency Bonus). On a failed save, the creature has the Prone condition.",
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
		// a wrong type or a missing list is refused as such, not answered "swap_not_available" (Nov 14 audit re-check)
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&raw); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": "Send one JSON object: {\"character_id\": N, \"weapon_masteries\": [\"Longsword\", ...]}."})
			return
		}
		if json.Unmarshal(raw["character_id"], &req.CharacterID) != nil { // "3012" as text (Nov 14 review: answered character_not_found)
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_character_id", "message": "character_id must be a number, like 12. Nothing was changed."})
			return
		}
		wm, has := raw["weapon_masteries"]
		if !has || json.Unmarshal(wm, &req.WeaponMasteries) != nil || req.WeaponMasteries == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "weapon_masteries_required", "message": "weapon_masteries must be a list of weapon names, like [\"Longsword\", \"Shortbow\"]. Nothing was changed."})
			return
		}
		for k := range raw {
			if k != "character_id" && k != "weapon_masteries" {
				writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_field", "message": fmt.Sprintf("%q is not a field this request takes (character_id, weapon_masteries). Nothing was changed.", k)})
				return
			}
		}
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
	// before the campaign starts the character is still being made: the masteries can be corrected freely, and the full number is
	// sent (Nov 14 review: a correction was refused as a Long Rest swap, and [] or one weapon was taken as a swap)
	inSetup, _ := shopOpen(req.CharacterID)
	if len(req.WeaponMasteries) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "weapon_masteries_required", "message": fmt.Sprintf("Send weapon_masteries: %d kinds of weapon. Nothing was changed.", max)})
		return
	}
	picks, perr := validateMasteryPicks(class, level, req.WeaponMasteries, inSetup)
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
	if !inSetup && (removed > 1 || (removed == 1 && swapUsed)) {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "swap_not_available",
			"message": "You can change only one Weapon Mastery choice each time you finish a Long Rest."})
		return
	}
	b, _ := json.Marshal(picks)
	db.Exec(`UPDATE characters SET weapon_masteries = $1, mastery_swap_used = $2 WHERE id = $3`, b, (swapUsed || removed == 1) && !inSetup, req.CharacterID) // a correction before the campaign uses no swap
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "weapon_masteries": describe(picks), "allowed": max})
}
