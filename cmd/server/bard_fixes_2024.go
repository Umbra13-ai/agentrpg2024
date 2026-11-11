package main

// Fixes from Rosie's coverage session (level20-battle-tests/s29_rosie_bard.py, Nov 9 2026; FINDINGS F130-F131):
//	Spell-save items     the GM's saving throw ignored Mantle of Spell Resistance, Robe of the Archmagi and the like against a spell
//	                     (F130, gm_checks.go: from_magic, or "spell" in the description)
//	Rod of Absorption    held and attuned: "absorb level N" (a Reaction) cancels a spell that targets only you and stores N levels
//	                     (50 in its existence); "convert level N" turns stored energy into a spell slot of level 1-5 (F131)

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var rodLevelRe = regexp.MustCompile(`level (\d)`)

func rodOfAbsorption(charID int, it invItem, description string) string {
	if _, ok := attunedTo(charID, "rod of absorption"); !ok {
		return "Cannot use the Rod of Absorption: you must be attuned to it (POST /api/characters/attune). Nothing was spent."
	}
	if itemStr(it, "location") != "held" {
		handHeld(charID, it)
		if itemStr(it, "location") != "held" {
			return "Cannot use the Rod of Absorption: hold it first (equipment/move to \"held\"). Nothing was spent."
		}
	}
	low := strings.ToLower(description)
	n := 0
	if m := rodLevelRe.FindStringSubmatch(low); m != nil {
		n, _ = strconv.Atoi(m[1])
	}
	stored, total := int(itemNum(it, "energy_stored")), int(itemNum(it, "energy_absorbed"))
	save := func() {
		editInventory(charID, func(inv []invItem) []invItem {
			for _, x := range inv {
				if itemStr(x, "id") == itemStr(it, "id") {
					x["energy_stored"], x["energy_absorbed"] = stored, total
				}
			}
			return inv
		})
	}
	switch {
	case strings.Contains(low, "convert"):
		if n < 1 || n > 5 {
			return "Say which slot level to create: \"convert level 3\" (1 to 5). Nothing was spent."
		}
		if stored < n {
			return fmt.Sprintf("The rod holds only %d levels of energy. Nothing was spent.", stored)
		}
		var raw []byte
		db.QueryRow(`SELECT COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
		used := map[string]int{}
		json.Unmarshal(raw, &used)
		used[strconv.Itoa(n)]--
		b, _ := json.Marshal(used)
		db.Exec(`UPDATE characters SET spell_slots_used = $1 WHERE id = $2`, string(b), charID)
		stored -= n
		save()
		return fmt.Sprintf("You draw %d levels of energy from the Rod of Absorption: you have a level %d spell slot to cast a spell you know or have prepared. %d levels stay stored.", n, n, stored)
	default:
		if n < 1 {
			return "Say the level of the spell you absorb: \"absorb level 3\" (a Reaction; only a spell that targets only you and has no area). Nothing was spent."
		}
		if total+n > 50 {
			return fmt.Sprintf("The rod has absorbed %d levels in its existence and can't take %d more (50 at most): it has no effect on this spell.", total, n)
		}
		db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, charID)
		stored += n
		total += n
		save()
		return fmt.Sprintf("The Rod of Absorption drinks the spell (a Reaction): its effect is canceled and its %d levels of energy are stored (%d stored now; %d of 50 absorbed in its existence).", n, stored, total)
	}
}
