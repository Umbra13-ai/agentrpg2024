package main

// Armor put on for the player, and reminders about gear that could be worn (F214, Nov 9).
//
// Auto-equip: outside combat, the best plain armor the character carries is put on when it raises AC. Plain means the character has
// training with it (untrained armor gives Disadvantage on Strength and Dexterity D20 Tests and blocks spellcasting), no Stealth
// Disadvantage, the Strength requirement is met (otherwise Speed drops 10 feet), no attunement and no curse. Donning takes 1, 5 or
// 10 minutes (SRD 5.2.1 Armor), so never in combat. Shields are never put on (a Shield takes a hand). Worn armor that needs attunement
// is never swapped out. Taking armor off (unequip-armor, or equip-armor "none") marks all the armor carried no_auto_equip; swapping to
// other armor marks the armor taken off. Armor that arrives later can still be put on. The table rule auto_equip "off" turns this off.
//
// Reminders: my-turn (outside combat) and GET /api/characters/gear-reminders list carried gear that could be worn and isn't: armor
// that would raise AC but has a drawback or needs attunement, a Shield when a hand is free, and clothing and jewelry (boots, gloves,
// cloaks, rings, amulets ...) whose place is free. POST /api/characters/gear-reminders mutes one item or all reminders.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

func ensureGearReminderColumn() {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS gear_reminders_off BOOLEAN DEFAULT false`)
}

// characterACWith: the AC the character would have in this armor ("" for none) with or without its Shield.
func characterACWith(charID int, armor string, shield bool) int {
	var dex, level int
	var sub sql.NullString
	if db.QueryRow(`SELECT dex, level, subclass FROM characters WHERE id = $1`, charID).Scan(&dex, &level, &sub) != nil {
		return 0
	}
	natural := naturalACBaseFor(charID, sub.String, level)
	natural = robeOfArchmagiBase(charID, armor, natural)
	if strings.EqualFold(armor, equippedArmorName(charID)) {
		natural = mageArmorBase(charID, armor, natural) // the armor worn: donning armor ends Mage Armor (F65)
	} else if armor == "" {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE target_id = $1 AND kind = 'mage_armor'`, charID).Scan(&n)
		if n > 0 {
			natural = maxInt(natural, 13)
		}
	} // armor only being weighed (auto-equip, reminders) leaves Mage Armor alone
	ac := calculateArmorACWithNatural(game.Modifier(dex), armor, shieldCounts(charID, shield), natural)
	ac += magicACBonus(charID, armor, shieldCounts(charID, shield))
	if armor != "" && hasFightingStyle(charID, "defense") {
		ac++
	}
	if floor := buffACFloor(charID); ac < floor {
		ac = floor
	}
	return ac
}

// atWillMageArmorAC: the AC under Mage Armor (13 + Dexterity), for a character who casts it at will (Armor of Shadows).
func atWillMageArmorAC(charID int, shield bool) int {
	var dex, level int
	var sub sql.NullString
	if db.QueryRow(`SELECT dex, level, subclass FROM characters WHERE id = $1`, charID).Scan(&dex, &level, &sub) != nil {
		return 0
	}
	natural := maxInt(naturalACBaseFor(charID, sub.String, level), 13)
	return calculateArmorACWithNatural(game.Modifier(dex), "", shieldCounts(charID, shield), natural) + magicACBonus(charID, "", shieldCounts(charID, shield))
}

// armorValueFor: what equipped_armor stores for an inventory item ("chain-mail" for plain armor, the name for magic armor), its
// armor data, or "" when the item isn't body armor (Shields are not).
func armorValueFor(name string) (string, *ArmorInfo) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || strings.Contains(n, "shield") {
		return "", nil
	}
	value := name
	if isArmorName(name) {
		value = armorSlugFor(name)
	}
	if info, err := getArmorInfo(value); err == nil && info != nil && info.Type != "shield" {
		return value, info
	}
	return "", nil
}

// sameArmor: the inventory item is the armor stored in equipped_armor.
func sameArmor(itemName, equipped string) bool {
	if equipped == "" {
		return false
	}
	v, _ := armorValueFor(itemName)
	return strings.EqualFold(v, equipped) || strings.EqualFold(itemName, equipped) || itemKey(itemName) == itemKey(strings.ReplaceAll(equipped, "-", " "))
}

var cursedArmorWords = []string{"vulnerability", "demon armor", "missile attraction"}

// armorDrawback: why this armor isn't put on automatically ("" when it is plain).
func armorDrawback(charID int, name string, info *ArmorInfo, profs string, str int) string {
	low := strings.ToLower(name)
	for _, w := range cursedArmorWords {
		if strings.Contains(low, w) {
			return "it is cursed"
		}
	}
	switch {
	case !isArmorProficient(profs, info.Type):
		return "you have no training with " + info.Type + " armor (Disadvantage on Strength and Dexterity D20 Tests, no spellcasting)"
	case itemNeedsAttunement(name) || itemNeedsAttunement(baseItemName(name)):
		return "it needs attunement"
	case info.StealthDisadvantage:
		return "it gives Disadvantage on Dexterity (Stealth) checks"
	case info.StrengthRequirement > 0 && str < info.StrengthRequirement:
		return fmt.Sprintf("it needs Strength %d (you have %d; your Speed would drop 10 feet)", info.StrengthRequirement, str)
	}
	return ""
}

// autoEquipArmor puts on the best plain armor the character carries when it raises AC (nil when nothing changed).
func autoEquipArmor(charID int) map[string]interface{} {
	if tableRule(lobbyOfChar(charID), "auto_equip") == "off" {
		return nil
	}
	if _, inCombat, _ := isCharacterInCombat(charID); inCombat {
		return nil
	}
	var profs, wild string
	var str int
	var current sql.NullString
	var shield bool
	if db.QueryRow(`SELECT COALESCE(armor_proficiencies,''), str, equipped_armor, COALESCE(equipped_shield,false), COALESCE(wild_shape_form,'')
		FROM characters WHERE id = $1`, charID).Scan(&profs, &str, &current, &shield, &wild) != nil || wild != "" {
		return nil
	}
	if current.String != "" { // armor the player chose with attunement or a curse stays on
		if ci, _ := getArmorInfo(current.String); ci != nil && armorDrawback(charID, current.String, ci, profs, str) == "it needs attunement" {
			return nil
		}
		if strings.Contains(strings.ToLower(current.String), "vulnerability") {
			return nil
		}
	}
	before := characterACWith(charID, current.String, shield)
	bestAC, bestValue, bestName := before, "", ""
	if current.String == "" && hasInvocation(charID, "armor-of-shadows") {
		bestAC = maxInt(bestAC, atWillMageArmorAC(charID, shield)) // Mage Armor at will: armor must beat 13 + Dexterity
	}
	for _, it := range inventoryOf(charID) {
		name := itemStr(it, "name")
		if itemBool(it, "no_auto_equip") || itemBool(it, "destroyed") || sameArmor(name, current.String) {
			continue
		}
		value, info := armorValueFor(name)
		if info == nil || armorDrawback(charID, name, info, profs, str) != "" {
			continue
		}
		if ac := characterACWith(charID, value, shield); ac > bestAC {
			bestAC, bestValue, bestName = ac, value, name
		}
	}
	if bestValue == "" {
		return nil
	}
	db.Exec(`UPDATE characters SET equipped_armor = $1 WHERE id = $2`, bestValue, charID)
	refreshStoredAC(charID)
	donTime, _ := game.ArmorDonDoffTime(func() string { _, i := armorValueFor(bestName); return i.Type }())
	out := map[string]interface{}{
		"armor": bestName, "ac_before": before, "ac": computeCharacterAC(charID),
		"note": fmt.Sprintf("You put on the %s (%d minutes; you have training with it and it has no drawback). Take it off with POST /api/characters/unequip-armor {\"character_id\": %d, \"armor\": true}; it won't be put back on. The GM can turn this off (table rule auto_equip).", bestName, donTime, charID),
	}
	if current.String != "" {
		out["replaced"] = current.String
	}
	return out
}

// markNoAutoEquip flags the armor the player took off so autoEquipArmor leaves it alone; clearNoAutoEquip when the player puts it on.
func markNoAutoEquip(charID int, armor string, flag bool) {
	if armor == "" {
		return
	}
	inv := inventoryRaw(charID)
	changed := false
	for _, it := range inv {
		if sameArmor(itemStr(it, "name"), armor) && itemBool(it, "no_auto_equip") != flag {
			if flag {
				it["no_auto_equip"] = true
			} else {
				delete(it, "no_auto_equip")
			}
			changed = true
		}
	}
	if changed {
		saveInventoryRaw(charID, inv)
	}
}

// markAllArmorNoAutoEquip: the player took armor off, so none of the armor they carry is put on for them.
func markAllArmorNoAutoEquip(charID int) {
	inv := inventoryRaw(charID)
	for _, it := range inv {
		if _, info := armorValueFor(itemStr(it, "name")); info != nil {
			it["no_auto_equip"] = true
		}
	}
	saveInventoryRaw(charID, inv)
}

func inventoryRaw(charID int) []invItem {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []invItem
	json.Unmarshal(raw, &inv)
	return inv
}

func saveInventoryRaw(charID int, inv []invItem) {
	if b, err := json.Marshal(inv); err == nil {
		db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, string(b), charID)
	}
}

// gearReminders: carried gear that could be worn and isn't (nil when muted or nothing to say).
func gearReminders(charID int) []map[string]interface{} {
	var off bool
	var profs string
	var str int
	var current sql.NullString
	var shield bool
	if db.QueryRow(`SELECT COALESCE(gear_reminders_off,false), COALESCE(armor_proficiencies,''), str, equipped_armor, COALESCE(equipped_shield,false)
		FROM characters WHERE id = $1`, charID).Scan(&off, &profs, &str, &current, &shield) != nil || off {
		return nil
	}
	mute := func(name string) string {
		return fmt.Sprintf("POST /api/characters/gear-reminders {\"character_id\": %d, \"item\": %q, \"mute\": true} (or \"all\": true)", charID, name)
	}
	inv := inventoryOf(charID)
	worn := map[string]bool{}
	for _, it := range inv {
		if itemStr(it, "location") == "worn" {
			if p, lim, _ := bodyPlaceOf(itemStr(it, "name")); lim {
				worn[p] = true
			}
		}
	}
	acNow := characterACWith(charID, current.String, shield)
	if current.String == "" && hasInvocation(charID, "armor-of-shadows") {
		acNow = maxInt(acNow, atWillMageArmorAC(charID, shield)) // Mage Armor at will
	}
	var out []map[string]interface{}
	seen := map[string]bool{}
	attunedN, attuneMax := len(attunedItemNames(charID)), attunementMax(charID)
	sameName := map[string]int{}
	for _, it := range inv {
		sameName[strings.ToLower(itemStr(it, "name"))]++
	}
	for _, it := range inv {
		name := itemStr(it, "name")
		low := strings.ToLower(name)
		if itemBool(it, "remind_off") || itemBool(it, "destroyed") || seen[low] {
			continue
		}
		seen[low] = true
		if value, info := armorValueFor(name); info != nil {
			if sameArmor(name, current.String) || itemBool(it, "no_auto_equip") || !isArmorProficient(profs, info.Type) {
				continue
			}
			ac := characterACWith(charID, value, shield)
			if ac <= acNow {
				continue
			}
			why := armorDrawback(charID, name, info, profs, str)
			if why == "" {
				why = "it would raise your AC; it can be put on outside combat"
			}
			out = append(out, map[string]interface{}{"item": name, "kind": "armor", "ac_now": acNow, "ac_with_it": ac,
				"why_not_worn": why, "how": fmt.Sprintf("POST /api/characters/equip-armor {\"character_id\": %d, \"armor\": %q}", charID, value), "mute": mute(name)})
			continue
		}
		if strings.Contains(low, "shield") && !shield && shieldTrained(profs) {
			mh, oh := equippedWeapons(charID)
			if ok, _ := handsAllow(mh, oh, true); ok {
				out = append(out, map[string]interface{}{"item": name, "kind": "shield", "ac_now": acNow, "ac_with_it": characterACWith(charID, current.String, true),
					"why_not_worn": "a Shield takes a hand (a weapon, a focus or spellcasting may need it)", "how": fmt.Sprintf("POST /api/characters/equip-armor {\"character_id\": %d, \"shield\": true}", charID), "mute": mute(name)})
			}
			continue
		}
		place, limited, _ := bodyPlaceOf(name)
		if place == "" || itemStr(it, "location") == "worn" || (limited && worn[place]) {
			continue
		}
		itemRef := name // two items with the same name (two Robes) need the id, or the server answers ambiguous_item
		if sameName[low] > 1 {
			itemRef = itemStr(it, "id")
		}
		r := map[string]interface{}{"item": name, "kind": "worn", "place": place,
			"how": fmt.Sprintf("POST /api/characters/%d/equipment/move {\"item\": %q, \"to\": \"worn\"}", charID, itemRef), "mute": mute(name)}
		if itemNeedsAttunement(name) || itemNeedsAttunement(baseItemName(name)) {
			if !attunedToName(charID, name) {
				r["attunement"] = fmt.Sprintf("needs attunement (%d of %d used): POST /api/characters/attune during a Short Rest", attunedN, attuneMax)
			}
		}
		out = append(out, r)
	}
	return out
}

// handleGearReminders godoc
// @Summary Gear that could be worn, and muting the reminders
// @Description GET ?character_id=N lists carried gear that could be worn and isn't: armor that would raise AC (with why it wasn't put on for you: a drawback or attunement), a Shield when a hand is free, and clothing and jewelry (boots, gloves, bracers, hats, cloaks, rings, amulets, belts) whose place is free. my-turn shows the same list outside combat as gear_reminders. POST {"character_id": N, "item": "<name>", "mute": true} stops reminders for that item ("mute": false brings them back); {"character_id": N, "all": true, "mute": true} stops all of them. Plain armor (trained, no Stealth Disadvantage, Strength met, no attunement or curse) that raises AC is put on for you outside combat (auto_equipped on my-turn and give-item; table rule auto_equip). Owner or GM.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param character_id query int false "Character ID (GET)"
// @Param request body object{character_id=int,item=string,all=bool,mute=bool} false "Mute one item or all (POST)"
// @Success 200 {object} map[string]interface{} "Reminders, or the mute applied"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/gear-reminders [get]
// @Router /characters/gear-reminders [post]
func handleGearReminders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		CharacterID int    `json:"character_id"`
		Item        string `json:"item"`
		All         bool   `json:"all"`
		Mute        *bool  `json:"mute"`
	}
	if r.Method == http.MethodGet {
		req.CharacterID, _ = strconv.Atoi(r.URL.Query().Get("character_id"))
	} else if r.Method == http.MethodPost {
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
	} else {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id required"})
		return
	}
	if !requireOwnerOrGM(w, r, req.CharacterID) {
		return
	}
	if r.Method == http.MethodGet {
		var off bool
		db.QueryRow(`SELECT COALESCE(gear_reminders_off,false) FROM characters WHERE id = $1`, req.CharacterID).Scan(&off)
		list := gearReminders(req.CharacterID)
		if list == nil {
			list = []map[string]interface{}{}
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"gear_reminders": list, "all_muted": off, "auto_equip": tableRule(lobbyOfChar(req.CharacterID), "auto_equip") != "off"})
		return
	}
	mute := req.Mute == nil || *req.Mute
	if req.All {
		db.Exec(`UPDATE characters SET gear_reminders_off = $1 WHERE id = $2`, mute, req.CharacterID)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "all_muted": mute})
		return
	}
	if strings.TrimSpace(req.Item) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_or_all_required", "message": "Name an item (\"item\") or send \"all\": true."})
		return
	}
	inv := inventoryRaw(req.CharacterID)
	n := 0
	for _, it := range inv {
		if strings.EqualFold(itemStr(it, "name"), req.Item) || itemStr(it, "id") == req.Item {
			if mute {
				it["remind_off"] = true
			} else {
				delete(it, "remind_off")
			}
			n++
		}
	}
	if n == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "item_not_found", "item": req.Item})
		return
	}
	saveInventoryRaw(req.CharacterID, inv)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "item": req.Item, "muted": mute})
}

func autoEquipTableRule() tableRuleDef {
	return tableRuleDef{Key: "auto_equip", Title: "Armor put on for players", Default: "on", Options: []tableRuleOption{
		{"on", "Put on plain armor", "Outside combat, the best armor a character carries is put on when it raises AC, the character has training with it, it gives no Stealth Disadvantage, its Strength requirement is met and it needs no attunement. Armor the player takes off isn't put back on."},
		{"off", "Players put armor on themselves", "Armor stays in the pack until the player equips it (gear_reminders still lists it)."},
	}}
}

func init() { tableRuleDefs = append(tableRuleDefs, autoEquipTableRule()) }
