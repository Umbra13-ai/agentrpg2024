package main

// Magic items with effects the server resolves through use_item (SRD 5.2.1 Magic Items A-Z; Nov 8, 2026). Before this, use_item
// on them said "No game effect resolved". POST /api/action {"action": "use_item", "item": "<name>", ...}:
//
//   Horn of Valhalla (held): a Magic action. Berserker spirits appear: Silver 2, Brass 3, Bronze 4, Iron 5 (the type in the item's
//     name, or rolled once on the SRD table and kept). Meeting the horn's requirement (Brass: all Simple weapons; Bronze: Medium
//     armor; Iron: all Martial weapons) makes them Friendly companions; otherwise they attack the blower (hostile monsters in the
//     fight). Then 7 days (7 dawns) before it works again.
//   Ring of Djinni Summoning (worn, attuned): a Magic action. A Djinni companion while the wearer keeps Concentration (up to an
//     hour). After it departs, the ring waits for a dawn (24 hours); if the djinni dies, the ring becomes nonmagical.
//   Iron Flask (held): a Magic action at a monster of the fight ("target_id") native to another plane (a Celestial, Elemental, Fey
//     or Fiend, on the Material Plane): DC 17 Wisdom save, with Advantage if the flask held it before, or it is trapped (out of the
//     fight). "release": it comes out and obeys for 1 hour (a companion).
//   Sphere of Annihilation: "control" (a Magic action within 60 feet): DC 25 Intelligence (Arcana), with Advantage holding or
//     wearing an attuned Talisman of the Sphere; control lasts until the start of your next turn; a failure moves the sphere 10
//     feet toward you. "move" with "target_ids" (a Bonus Action in control; 5 x Intelligence modifier feet, at least 5; "talisman":
//     the Talisman's Magic action, 10 + 10 x the modifier): each creature whose space it enters makes a DC 19 Dexterity save or
//     takes 8d10 Force damage, obliterated at 0 Hit Points.
//   Mysterious Deck: "draw N" declares and draws N cards from the SRD table (13 or 22 cards, rolled once and kept; the Fool and the
//     Jester leave the deck when drawn). Each card's SRD text is given; the server applies the Skull (an Avatar of Death joins the
//     fight, half your Hit Point maximum, attacking only you), Fool (another draw), Void (no more draws), Ruin (your coins are
//     gone), Talons (your magic items disintegrate), Gem (jewelry, or gems with "gems"), Knight (a Knight companion), Euryale (-2
//     to saving throws) and Sun (10 Temporary Hit Points at each dawn). The other cards need the GM or a choice.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// ---------------------------------------------------------------- shared helpers

// updateInventoryItem changes one inventory item (found by id, or by name when it has none).
func updateInventoryItem(charID int, it invItem, fn func(x invItem)) {
	id, name := itemStr(it, "id"), itemStr(it, "name")
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if (id != "" && itemStr(x, "id") == id) || (id == "" && itemStr(x, "name") == name) {
				fn(x)
				break
			}
		}
		return inv
	})
}

var attunementItemsOnce sync.Once
var attunementItems map[string]bool

// itemNeedsAttunement: the SRD item requires attunement.
func itemNeedsAttunement(name string) bool {
	attunementItemsOnce.Do(func() {
		attunementItems = map[string]bool{}
		var items []srdMagicItem
		json.Unmarshal(srd521MagicItemsJSON, &items)
		for _, m := range items {
			attunementItems[strings.ToLower(m.Name)] = m.RequiresAttunement
		}
	})
	return attunementItems[strings.ToLower(strings.TrimSpace(name))]
}

// itemCooldownLeft: dawns still to pass before the item works again.
func itemCooldownLeft(it invItem) int { return asInt(it["dawns_until_ready"]) }

// tickItemCooldown counts a dawn off an item's wait (rechargeAtDawn). It returns a note when the item is ready again.
func tickItemCooldown(charID int, it invItem) string {
	name := itemStr(it, "name")
	clearDawnFlags(it) // once-a-dawn properties come back (legendary_more_2024.go)
	if strings.HasPrefix(strings.ToLower(name), "ring of djinni summoning") && asInt(it["djinni_id"]) > 0 {
		if c, ok := loadCompanion(asInt(it["djinni_id"])); !ok || c.Status == "gone" {
			delete(it, "djinni_id") // the djinni departed before this dawn: 24 hours have passed
			return name + " can summon its djinni again"
		}
	}
	n := itemCooldownLeft(it)
	if n <= 0 {
		return ""
	}
	if n == 1 {
		delete(it, "dawns_until_ready")
		return name + " can be used again"
	}
	it["dawns_until_ready"] = n - 1
	return ""
}

// insertItemCompanion makes a companion from an SRD stat block (the monsters table): its HP, AC, Speed, attacks (with the
// Multiattack counts) and traits.
func insertItemCompanion(charID int, kind, name, source, slug, ctype, notes, concentration string, hp int) (int, string) {
	ensureCompanions()
	var mname, speed, mtype string
	var mhp, ac int
	if db.QueryRow(`SELECT name, hp, ac, COALESCE(speed_text,''), COALESCE(type,'') FROM monsters WHERE slug = $1`, slug).Scan(&mname, &mhp, &ac, &speed, &mtype) != nil {
		return 0, ""
	}
	if hp <= 0 {
		hp = mhp
	}
	if ctype == "" {
		ctype = mtype
	}
	attacks := formAttacks(slug)
	var raw []byte
	db.QueryRow(`SELECT COALESCE(actions,'[]') FROM monsters WHERE slug = $1`, slug).Scan(&raw)
	var rows []monsterActionRow
	json.Unmarshal(raw, &rows)
	for _, r := range rows {
		if strings.EqualFold(stripUsage(r.Name), "Multiattack") {
			counts := map[string]int{}
			for _, p := range multiattackPlan(r.Desc, rows) {
				counts[p.Name]++
			}
			for i := range attacks {
				if n := counts[attacks[i].Name]; n > 0 {
					attacks[i].Count = n
				}
			}
			// "makes three attacks, using Storm Blade or Storm Bolt in any combination": the first attack, that many times
			if mm := makesNAttacksRe.FindStringSubmatch(strings.ToLower(r.Desc)); mm != nil && len(attacks) > 0 {
				attacks[0].Count = countWords[mm[1]]
			}
		}
	}
	b, _ := json.Marshal(attacks)
	var lobbyID, id int
	db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&lobbyID)
	db.QueryRow(`INSERT INTO companions (owner_id, lobby_id, kind, name, source, form, creature_type, slot_level, hp, max_hp, ac, speed, attacks, notes, concentration, traits)
		VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$8,$9,$10,$11,$12,$13,(SELECT COALESCE(traits,'[]') FROM monsters WHERE slug = $6)) RETURNING id`,
		charID, lobbyID, kind, name, source, slug, ctype, hp, ac, speed, string(b), notes, concentration).Scan(&id)
	return id, mname
}

// addMonstersToFight adds monsters of one stat block to the active fight (one shared Initiative roll), keeping the current turn.
// extra fields go on each entry. It returns the new combatant ids.
func addMonstersToFight(lobbyID int, slug, name string, n, hp int, extra map[string]interface{}) []int {
	var raw []byte
	var idx int
	var active bool
	if db.QueryRow(`SELECT turn_order, current_turn_index, active FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw, &idx, &active) != nil || !active {
		return nil
	}
	var entries []map[string]interface{}
	if json.Unmarshal(raw, &entries) != nil || len(entries) == 0 {
		return nil
	}
	cur := entries[0]
	if idx >= 0 && idx < len(entries) {
		cur = entries[idx]
	}
	var dex, mhp, ac int
	if db.QueryRow(`SELECT COALESCE(dex,10), COALESCE(hp,10), COALESCE(ac,10) FROM monsters WHERE slug = $1`, slug).Scan(&dex, &mhp, &ac) != nil {
		return nil
	}
	if hp <= 0 {
		hp = mhp
	}
	minID := 0
	for _, e := range entries {
		if id := asInt(e["id"]); id < minID {
			minID = id
		}
	}
	init := game.RollDie(20) + game.Modifier(dex)
	var ids []int
	for i := 1; i <= n; i++ {
		minID--
		label := name
		if n > 1 {
			label = fmt.Sprintf("%s %d", name, i)
		}
		e := map[string]interface{}{"id": minID, "name": label, "initiative": init, "dex_score": dex, "is_monster": true, "monster_key": slug, "hp": hp, "max_hp": hp, "ac": ac}
		for k, v := range extra {
			e[k] = v
		}
		entries = append(entries, e)
		ids = append(ids, minID)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if asInt(entries[i]["initiative"]) != asInt(entries[j]["initiative"]) {
			return asInt(entries[i]["initiative"]) > asInt(entries[j]["initiative"])
		}
		return asInt(entries[i]["dex_score"]) > asInt(entries[j]["dex_score"])
	})
	newIdx := 0
	for i, e := range entries {
		if asInt(e["id"]) == asInt(cur["id"]) && fmt.Sprint(e["is_thiefs_reflexes_turn"]) == fmt.Sprint(cur["is_thiefs_reflexes_turn"]) {
			newIdx = i
			break
		}
	}
	b, _ := json.Marshal(entries)
	db.Exec(`UPDATE combat_state SET turn_order = $1, current_turn_index = $2 WHERE lobby_id = $3`, b, newIdx, lobbyID)
	return ids
}

// removeMonsterFromFight takes a monster out of the turn order (trapped in an Iron Flask), keeping the current turn.
func removeMonsterFromFight(lobbyID, id int) bool {
	var raw []byte
	var idx int
	if db.QueryRow(`SELECT turn_order, current_turn_index FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw, &idx) != nil {
		return false
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	out := entries[:0:0]
	removed := false
	for i, e := range entries {
		if asInt(e["id"]) == id {
			removed = true
			if i < idx {
				idx--
			}
			continue
		}
		out = append(out, e)
	}
	if !removed || len(out) == 0 {
		return false
	}
	if idx >= len(out) {
		idx = 0
	}
	b, _ := json.Marshal(out)
	db.Exec(`UPDATE combat_state SET turn_order = $1, current_turn_index = $2 WHERE lobby_id = $3`, b, idx, lobbyID)
	return true
}

// ---------------------------------------------------------------- the use_item hook

// legendaryItemUse resolves use_item for the items above; ok is false for other items.
func legendaryItemUse(charID int, it invItem, req itemCastReq, description string) (string, bool) {
	name := strings.ToLower(itemStr(it, "name"))
	var out string
	switch {
	case strings.HasPrefix(name, "horn of valhalla"):
		out = hornOfValhalla(charID, it)
	case strings.HasPrefix(name, "ring of djinni summoning"):
		out = djinniRing(charID, it)
	case strings.HasPrefix(name, "iron flask"):
		out = ironFlask(charID, it, req, description)
	case strings.HasPrefix(name, "sphere of annihilation"):
		out = sphereOfAnnihilation(charID, it, req, description)
	case strings.HasPrefix(name, "mysterious deck"):
		out = mysteriousDeck(charID, it, description)
	default:
		return "", false
	}
	if lobby := lobbyOfChar(charID); lobby != 0 && !strings.HasPrefix(out, "Cannot") {
		recordGMAction(db, lobby, "magic_item", fmt.Sprintf("✨ %s uses the %s.", getCharacterName(charID), itemStr(it, "name")), out)
	}
	return out, true
}

// legendaryItemBonusAction: the use is a Bonus Action (moving a controlled Sphere of Annihilation without the Talisman).
func legendaryItemBonusAction(charID int, ref, description string) bool {
	it, _ := itemInUse(charID, ref, description)
	if it == nil || !strings.HasPrefix(strings.ToLower(itemStr(it, "name")), "sphere of annihilation") {
		return false
	}
	d := strings.ToLower(description)
	return strings.Contains(d, "move") && !strings.Contains(d, "talisman")
}

func needAttunedHere(charID int, it invItem, place string) string {
	name := itemStr(it, "name")
	if itemBool(it, "nonmagical") {
		return "Cannot use the " + name + ": it is nonmagical now. Nothing happened."
	}
	if itemNeedsAttunement(name) {
		if _, ok := attunedTo(charID, name); !ok {
			return fmt.Sprintf("Cannot use the %s: you must be attuned to it (POST /api/characters/attune). Nothing was spent.", name)
		}
	}
	switch place {
	case "held":
		if itemStr(it, "location") != "held" {
			return fmt.Sprintf("Cannot use the %s: hold it first (POST /api/characters/{id}/equipment/move {\"item\": \"%s\", \"to\": \"held\"}). Nothing was spent.", name, name)
		}
	case "worn":
		if loc := itemStr(it, "location"); loc != "worn" && loc != "held" {
			return fmt.Sprintf("Cannot use the %s: put it on first (equipment/move to \"worn\"). Nothing was spent.", name)
		}
	case "out":
		if msg := itemPlace(it, nil); msg != "" {
			return "Cannot use it: " + msg + " Nothing was spent."
		}
	}
	return ""
}

// ---------------------------------------------------------------- Horn of Valhalla

var hornTypes = []struct {
	Name    string
	Spirits int
	Need    string
}{{"Silver", 2, ""}, {"Brass", 3, "proficiency with all Simple weapons"}, {"Bronze", 4, "training with all Medium armor"}, {"Iron", 5, "proficiency with all Martial weapons"}}

func hornOfValhalla(charID int, it invItem) string {
	if why := needAttunedHere(charID, it, "held"); why != "" {
		return why
	}
	name := itemStr(it, "name")
	if n := itemCooldownLeft(it); n > 0 {
		return fmt.Sprintf("Cannot blow the %s: once used, it can't be used again until 7 days have passed (%d more dawn(s)). Nothing was spent.", name, n)
	}
	kind := itemStr(it, "horn_type")
	for _, h := range hornTypes {
		if kind == "" && strings.Contains(strings.ToLower(name), strings.ToLower(h.Name)) {
			kind = h.Name
		}
	}
	rolled := ""
	if kind == "" { // the GM chooses or rolls; the server rolls once and keeps it
		d := game.RollDie(100)
		switch {
		case d <= 40:
			kind = "Silver"
		case d <= 75:
			kind = "Brass"
		case d <= 90:
			kind = "Bronze"
		default:
			kind = "Iron"
		}
		rolled = fmt.Sprintf(" (its type, rolled on the SRD table: %d, %s)", d, kind)
	}
	var spirits int
	var need string
	for _, h := range hornTypes {
		if h.Name == kind {
			spirits, need = h.Spirits, h.Need
		}
	}
	var weapons, armor string
	db.QueryRow(`SELECT LOWER(COALESCE(weapon_proficiencies,'')), LOWER(COALESCE(armor_proficiencies,'')) FROM characters WHERE id = $1`, charID).Scan(&weapons, &armor)
	meets := map[string]bool{"Silver": true, "Brass": strings.Contains(weapons, "simple"), "Bronze": strings.Contains(armor, "medium"), "Iron": strings.Contains(weapons, "martial")}[kind]
	updateInventoryItem(charID, it, func(x invItem) {
		x["horn_type"] = kind
		x["dawns_until_ready"] = 7
	})
	lobby := lobbyOfChar(charID)
	out := fmt.Sprintf("You blow the %s%s. %d warrior spirits from Ysgard appear within 60 feet (Berserker stat block; Immunity to the Charmed and Frightened conditions; they return after 1 hour or at 0 Hit Points).", name, rolled, spirits)
	if !meets {
		out += fmt.Sprintf(" You don't meet the %s horn's requirement (%s): the spirits attack you!", kind, need)
		if ids := addMonstersToFight(lobby, "berserker", "Valhalla Spirit", spirits, 0, map[string]interface{}{"hostile_to": charID}); len(ids) > 0 {
			out += fmt.Sprintf(" They join the fight as enemies (ids %v).", ids)
		} else {
			out += " (No fight is running: the GM starts one and adds them, monster_key \"berserker\".)"
		}
		return out + " The horn can't be used again for 7 days."
	}
	var made []string
	for i := 1; i <= spirits; i++ {
		id, _ := insertItemCompanion(charID, "summon", fmt.Sprintf("%s's Valhalla Spirit %d", getCharacterName(charID), i), "horn-of-valhalla", "berserker", "",
			"A warrior spirit from Ysgard (Horn of Valhalla): Friendly to you and your allies; it follows your commands. Immunity to the Charmed and Frightened conditions. It returns to Ysgard after 1 hour or when it drops to 0 Hit Points.", "", 0)
		if id > 0 {
			made = append(made, fmt.Sprintf("id %d", id))
		}
	}
	return out + fmt.Sprintf(" You meet its requirement: they are Friendly and follow your commands (companions %s; command each with POST /api/action {\"companion_id\": ..., \"action\": \"attack\", \"target_id\": ...}). The horn can't be used again for 7 days.", strings.Join(made, ", "))
}

// ---------------------------------------------------------------- Ring of Djinni Summoning

func djinniRing(charID int, it invItem) string {
	if why := needAttunedHere(charID, it, "worn"); why != "" {
		return why
	}
	name := itemStr(it, "name")
	if id := asInt(it["djinni_id"]); id > 0 {
		c, ok := loadCompanion(id)
		switch {
		case ok && c.Status != "gone":
			return fmt.Sprintf("Cannot summon: your djinni is already here (companion %d). Nothing was spent.", id)
		case ok && strings.Contains(c.EndReason, "0 Hit Points"):
			updateInventoryItem(charID, it, func(x invItem) { x["nonmagical"] = true })
			return "Cannot summon: the djinni died, and the " + name + " is nonmagical now."
		default:
			return "Cannot summon: after the djinni departs, it can't be summoned again for 24 hours (the next dawn). Nothing was spent."
		}
	}
	db.Exec(`UPDATE characters SET concentrating_on = $1 WHERE id = $2`, "Ring of Djinni Summoning", charID) // any other Concentration ends
	id, _ := insertItemCompanion(charID, "summon", getCharacterName(charID)+"'s Djinni", "ring-of-djinni-summoning", "djinni", "",
		"Summoned with the Ring of Djinni Summoning: Friendly to you and your allies, it obeys your commands; without commands it defends itself but takes no other actions. It stays while you keep Concentration (up to 1 hour) or until it drops to 0 Hit Points. If it dies, the ring becomes nonmagical.", "Ring of Djinni Summoning", 0)
	if id == 0 {
		return "Cannot summon: the Djinni stat block wasn't found. Nothing was spent."
	}
	updateInventoryItem(charID, it, func(x invItem) { x["djinni_id"] = id })
	return fmt.Sprintf("You call on the %s: a Djinni from the Elemental Plane of Air appears within 120 feet (companion %d). It obeys your commands while you keep Concentration (up to 1 hour); command it with POST /api/action {\"companion_id\": %d, \"action\": \"attack\", \"target_id\": ...}. After it departs, it can't be summoned again for 24 hours.", name, id, id)
}

// ---------------------------------------------------------------- Iron Flask

var otherPlaneTypes = map[string]bool{"celestial": true, "elemental": true, "fey": true, "fiend": true}

func ironFlask(charID int, it invItem, req itemCastReq, description string) string {
	if why := needAttunedHere(charID, it, "held"); why != "" {
		return why
	}
	name := itemStr(it, "name")
	contains, _ := it["contains"].(map[string]interface{})
	lobby := lobbyOfChar(charID)
	if strings.Contains(strings.ToLower(description), "release") {
		if contains == nil {
			return "The " + name + " is empty: there is nothing to release. (Your Magic action is spent removing the stopper.)"
		}
		key := fmt.Sprint(contains["monster_key"])
		cname := fmt.Sprint(contains["name"])
		id, mname := insertItemCompanion(charID, "summon", fmt.Sprintf("%s (from %s's Iron Flask)", cname, getCharacterName(charID)), "iron-flask", key, "",
			"Released from the Iron Flask: it obeys your commands for 1 hour, understanding them in any language. With no command, or a command likely to bring its death or imprisonment, it defends itself but otherwise takes no actions. Afterwards it acts by its normal disposition and alignment.", "", asInt(contains["hp"]))
		updateInventoryItem(charID, it, func(x invItem) { delete(x, "contains") })
		if id == 0 {
			return fmt.Sprintf("You release %s from the %s; it obeys your commands for 1 hour (the GM runs it: its stat block wasn't found).", cname, name)
		}
		return fmt.Sprintf("You remove the stopper: %s (%s) emerges and obeys your commands for 1 hour (companion %d; command it with POST /api/action {\"companion_id\": %d, ...}).", cname, mname, id, id)
	}
	if contains != nil {
		return fmt.Sprintf("Cannot trap anything: the %s already holds a creature (it holds one at a time; release it with \"release\"). Nothing was spent.", name)
	}
	target := req.TargetID
	if target >= 0 {
		return fmt.Sprintf("Cannot use the %s that way: name the creature with \"target_id\" (a monster of the fight within 60 feet, from GET /api/my-turn), or say \"release\". Nothing was spent.", name)
	}
	e, _, ok := monsterCombatant(lobby, target)
	if !ok || asInt(e["hp"]) <= 0 {
		return "Cannot use the " + name + " on that target: it isn't a creature standing in this fight. Nothing was spent."
	}
	mname, key := fmt.Sprint(e["name"]), fmt.Sprint(e["monster_key"])
	var mtype string
	db.QueryRow(`SELECT LOWER(COALESCE(type,'')) FROM monsters WHERE slug = $1`, key).Scan(&mtype)
	if !otherPlaneTypes[mtype] {
		return fmt.Sprintf("You aim the %s at %s, but it is native to this plane of existence (a %s): nothing happens.", name, mname, mtype)
	}
	before := false
	if list, ok := it["trapped_before"].([]interface{}); ok {
		for _, k := range list {
			if fmt.Sprint(k) == key {
				before = true
			}
		}
	}
	saved, total := monsterSaveRoll(lobby, target, "wis", 17)
	adv := ""
	if before && !saved { // Advantage: it saves if either d20 succeeds
		saved, total = monsterSaveRoll(lobby, target, "wis", 17)
		adv = " with Advantage (it was trapped in the flask before)"
	}
	if !saved && legendaryResistance(lobby, target, saveStakes{Condition: true}) {
		saved = true
		adv += " (Legendary Resistance)"
	}
	if saved {
		return fmt.Sprintf("You aim the %s at %s: Wisdom save %d%s against DC 17, a success. It resists.", name, mname, total, adv)
	}
	removeMonsterFromFight(lobby, target)
	updateInventoryItem(charID, it, func(x invItem) {
		x["contains"] = map[string]interface{}{"name": mname, "monster_key": key, "hp": asInt(e["hp"]), "max_hp": asInt(e["max_hp"])}
		list, _ := x["trapped_before"].([]interface{})
		x["trapped_before"] = append(list, key)
	})
	return fmt.Sprintf("You aim the %s at %s: Wisdom save %d%s against DC 17, a failure. %s is drawn into the flask and trapped (it leaves the fight; it doesn't age, breathe, eat or drink). Release it with {\"action\": \"use_item\", \"item\": \"%s\", \"description\": \"release\"}: it then obeys you for 1 hour.", name, mname, total, adv, mname, name)
}

// ---------------------------------------------------------------- Sphere of Annihilation and the Talisman of the Sphere

func talismanOfTheSphere(charID int) bool {
	it := inventoryItemNamed(charID, "Talisman of the Sphere")
	if it == nil {
		return false
	}
	_, attuned := attunedTo(charID, "Talisman of the Sphere")
	loc := itemStr(it, "location")
	return attuned && (loc == "held" || loc == "worn")
}

func sphereOfAnnihilation(charID int, it invItem, req itemCastReq, description string) string {
	name := itemStr(it, "name")
	d := strings.ToLower(description)
	lobby := lobbyOfChar(charID)
	round, _, inFight := combatRoundOf(lobby)
	var class string
	var level, intl int
	db.QueryRow(`SELECT LOWER(class), level, intl FROM characters WHERE id = $1`, charID).Scan(&class, &level, &intl)
	intMod := game.Modifier(intl)
	talisman := talismanOfTheSphere(charID)
	marker := combatTurnMarker(lobby) // this turn of this fight ("" out of combat)
	if strings.Contains(d, "move") {
		mine := asInt(it["controlled_by"]) == charID
		ctlMarker := itemStr(it, "control_marker")
		inControl := mine && ctlMarker == marker // control lasts until the start of your next turn: a Bonus Action move is this turn
		if !inControl && mine && inFight && ctlMarker != "" && strings.Contains(d, "talisman") {
			_, cur, _ := combatRoundOf(lobby)
			inControl = cur == charID && round == asInt(it["control_round"])+1 // the Talisman: you start your next turn in control
		}
		if !inControl {
			return "Cannot move the " + name + ": you don't control it (use_item with \"control\" first: a Magic action and a DC 25 Intelligence (Arcana) check). Nothing was spent."
		}
		feet := 5 * intMod
		if feet < 5 {
			feet = 5
		}
		how := "a Bonus Action"
		if strings.Contains(d, "talisman") {
			if !talisman {
				return "Cannot use the Talisman's movement: hold or wear an attuned Talisman of the Sphere. Nothing was spent."
			}
			feet, how = 10+10*intMod, "the Talisman of the Sphere's Magic action (not in a straight line)"
		}
		out := fmt.Sprintf("You move the %s up to %d feet (%s).", name, feet, how)
		targets := append([]int{}, req.TargetIDs...)
		if req.TargetID < 0 {
			targets = append([]int{req.TargetID}, targets...)
		}
		if len(targets) == 0 {
			return out + " No creature is in its path."
		}
		for _, t := range targets {
			e, _, ok := monsterCombatant(lobby, t)
			if !ok || asInt(e["hp"]) <= 0 {
				continue
			}
			mname := fmt.Sprint(e["name"])
			dmg := game.RollDamage("8d10", false)
			saved, total := monsterSaveFor(lobby, t, "dex", 19, saveStakes{Damage: dmg})
			if saved {
				out += fmt.Sprintf(" %s: Dexterity save %d against DC 19, it dodges the sphere.", mname, total)
				continue
			}
			got, _ := adjustMonsterDamage(fmt.Sprint(e["monster_key"]), "force", dmg)
			hp := asInt(e["hp"]) - got
			if hp < 0 {
				hp = 0
			}
			setMonsterHP(lobby, t, hp)
			out += fmt.Sprintf(" %s: Dexterity save %d against DC 19, a failure: the sphere touches it for %d Force damage", mname, total, got)
			if hp == 0 {
				out += ", and it is obliterated (its possessions stay behind; no other remains)."
			} else {
				out += "."
			}
		}
		return out
	}
	if !strings.Contains(d, "control") {
		return fmt.Sprintf("Cannot use the %s without saying how: \"control\" (a Magic action within 60 feet: DC 25 Intelligence (Arcana)), then \"move\" with \"target_ids\" (a Bonus Action, or \"move talisman\"). Anything that touches it takes 8d10 Force damage. Nothing was spent.", name)
	}
	mod, prof, _, _ := checkBonus(charID, class, level, "arcana", intl)
	r1, r2 := game.RollDie(20), game.RollDie(20)
	roll, how := r1, ""
	if talisman {
		if r2 > r1 {
			roll = r2
		}
		how = fmt.Sprintf(" with Advantage (Talisman of the Sphere: %d, %d)", r1, r2)
	}
	total := roll + mod + prof - game.ExhaustionD20Penalty(exhaustionLevelOf(charID))
	if total < 25 {
		return fmt.Sprintf("Intelligence (Arcana) check%s: %d against DC 25, a failure. The %s moves 10 feet toward you in a straight line.", how, total, name)
	}
	updateInventoryItem(charID, it, func(x invItem) {
		x["controlled_by"], x["control_round"], x["control_marker"] = charID, round, marker
	})
	return fmt.Sprintf("Intelligence (Arcana) check%s: %d against DC 25, a success. You control the %s until the start of your next turn (anyone else loses control). Move it with {\"action\": \"use_item\", \"item\": \"%s\", \"description\": \"move\", \"target_ids\": [...]} (a Bonus Action: up to %d feet).", how, total, name, name, maxInt(5, 5*intMod))
}

// ---------------------------------------------------------------- Mysterious Deck and the Avatar of Death

type deckCard struct {
	Name       string
	Lo13, Hi13 int // 0 when not in the 13-card deck
	Lo22, Hi22 int
}

var deckCards = []deckCard{{"Balance", 0, 0, 1, 5}, {"Comet", 0, 0, 6, 10}, {"Donjon", 0, 0, 11, 14}, {"Euryale", 1, 8, 15, 18}, {"Fates", 0, 0, 19, 23},
	{"Flames", 9, 16, 24, 27}, {"Fool", 0, 0, 28, 31}, {"Gem", 0, 0, 32, 36}, {"Jester", 17, 24, 37, 41}, {"Key", 25, 32, 42, 46}, {"Knight", 33, 40, 47, 51},
	{"Moon", 41, 48, 52, 56}, {"Puzzle", 0, 0, 57, 60}, {"Rogue", 49, 56, 61, 64}, {"Ruin", 57, 64, 65, 68}, {"Sage", 0, 0, 69, 73}, {"Skull", 65, 72, 74, 77},
	{"Star", 73, 80, 78, 82}, {"Sun", 81, 88, 83, 87}, {"Talons", 0, 0, 88, 91}, {"Throne", 89, 96, 92, 96}, {"Void", 97, 100, 97, 100}}

var cardTextOnce sync.Once
var cardText map[string]string

func deckCardText(card string) string {
	cardTextOnce.Do(func() {
		cardText = map[string]string{}
		var items []srdMagicItem
		json.Unmarshal(srd521MagicItemsJSON, &items)
		for _, m := range items {
			if m.Name != "Mysterious Deck" {
				continue
			}
			for _, c := range deckCards {
				if mm := regexp.MustCompile(`\n` + c.Name + `\. ([^\n]*)`).FindStringSubmatch(m.Description); mm != nil {
					cardText[c.Name] = mm[1]
				}
			}
		}
	})
	return cardText[card]
}

func drawDeckCard(size int, removed map[string]bool) string {
	for tries := 0; tries < 50; tries++ {
		d := game.RollDie(100)
		for _, c := range deckCards {
			lo, hi := c.Lo13, c.Hi13
			if size == 22 {
				lo, hi = c.Lo22, c.Hi22
			}
			if lo > 0 && d >= lo && d <= hi {
				if removed[c.Name] {
					break // the Fool or the Jester left the deck: reroll
				}
				return c.Name
			}
		}
	}
	var left []string // many cards gone: draw from what's left
	for _, c := range deckCards {
		lo := c.Lo13
		if size == 22 {
			lo = c.Lo22
		}
		if lo > 0 && !removed[c.Name] {
			left = append(left, c.Name)
		}
	}
	if len(left) > 0 {
		return left[game.RollDie(len(left))-1]
	}
	return "Balance"
}

var countWordRe = regexp.MustCompile(`\b(\d+|one|two|three|four|five|six)\b`)

// makesNAttacksRe: a Multiattack that names a number of attacks but not which ("makes three attacks, using ...").
var makesNAttacksRe = regexp.MustCompile(`makes (one|two|three|four|five|six) attacks`)

// autoHitRe: an attack that hits without a roll ("Melee Attack Roll: Automatic hit", the Avatar of Death's Reaping Scythe).
var autoHitRe = regexp.MustCompile(`(?i)Attack Roll: Automatic hit`)

func mysteriousDeck(charID int, it invItem, description string) string {
	if why := needAttunedHere(charID, it, "out"); why != "" {
		return why
	}
	name := itemStr(it, "name")
	if out, ok := resolveDeckChoice(charID, it, description); ok { // "star: wisdom", "balance: +dex -str" ... (deck_choices_2024.go)
		return out
	}
	d := strings.ToLower(description)
	if !strings.Contains(d, "draw") {
		return fmt.Sprintf("Cannot use the %s without declaring the draw: say \"draw N\" (how many cards; you must declare it before drawing). Nothing was drawn.", name)
	}
	n := 1
	if m := countWordRe.FindStringSubmatch(d); m != nil {
		if v, ok := countWords[m[1]]; ok {
			n = v
		} else {
			n = atoiSafe(m[1])
		}
	}
	if n < 1 || n > 22 {
		return "Cannot draw that many: declare between 1 and 22 cards. Nothing was drawn."
	}
	size := asInt(it["deck_cards"])
	sizeNote := ""
	if size != 13 && size != 22 {
		size = 13
		if game.RollDie(100) > 75 { // most (75 percent) of these decks have thirteen cards
			size = 22
		}
		sizeNote = fmt.Sprintf(" (this deck has %d cards)", size)
	}
	removed := map[string]bool{}
	if list, ok := it["removed_cards"].([]interface{}); ok {
		for _, c := range list {
			removed[fmt.Sprint(c)] = true
		}
	}
	lobby := lobbyOfChar(charID)
	owner := getCharacterName(charID)
	var lines, drawn, newPending []string
	extra := 0
	stopped := false
	for i := 0; i < n+extra && !stopped; i++ {
		card := drawDeckCard(size, removed)
		drawn = append(drawn, card)
		line := fmt.Sprintf("%d. %s: %s", len(drawn), card, deckCardText(card))
		switch card {
		case "Fool", "Jester":
			removed[card] = true
			if card == "Fool" {
				extra++
			}
			p, l, _ := deckCardPending(charID, card)
			line += l
			if p != "" {
				newPending = append(newPending, p)
			}
		case "Void":
			stopped = true
			addDeckBoon(charID, "void")
			addMonsterCondition(lobby, charID, "incapacitated", "the Void", 0, "", 0) // the body, while the soul is trapped (O-4)
			line += " [Server: you draw no more cards. Your soul is held in an object in a place the GM chooses, guarded by powerful beings; your body is Incapacitated until the soul returns (the GM then removes the condition). A Wish spell can't return it but reveals where it is.]"
		case "Skull":
			var maxHP int
			db.QueryRow(`SELECT max_hp FROM characters WHERE id = $1`, charID).Scan(&maxHP)
			ensureAvatarOfDeath()
			if ids := addMonstersToFight(lobby, "avatar-of-death", "Avatar of Death ("+owner+")", 1, maxHP/2, map[string]interface{}{"summoner_id": charID}); len(ids) > 0 {
				line += fmt.Sprintf(" [Server: the Avatar of Death joins the fight (id %d, %d Hit Points); it attacks only you. If an ally damages it, the GM adds that ally's own avatar: combat/add monster_key \"avatar-of-death\" with hp half that ally's Hit Point maximum.]", ids[0], maxHP/2)
			} else {
				line += fmt.Sprintf(" [Server: no fight is running; the GM starts one and adds it: combat/add {\"monster_key\": \"avatar-of-death\", \"hp\": %d}.]", maxHP/2)
			}
		case "Ruin":
			db.Exec(`UPDATE characters SET gold = 0 WHERE id = $1`, charID)
			line += " [Server: your coins are gone (gold set to 0). The GM removes the rest of your wealth that isn't magic.]"
		case "Talons":
			var gone []string
			editInventory(charID, func(inv []invItem) []invItem {
				for _, x := range inv {
					if itemStr(x, "magic_item") != "" && !itemBool(x, "destroyed") {
						x["destroyed"] = true
						gone = append(gone, itemStr(x, "name"))
					}
				}
				return inv
			})
			db.Exec(`UPDATE characters SET attuned_items = '[]' WHERE id = $1`, charID)
			line += fmt.Sprintf(" [Server: these disintegrate: %s.]", strings.Join(gone, ", "))
			stopped = true // the deck was one of them
		case "Gem":
			item, qty := "Jewelry (2,000 GP)", 25
			if strings.Contains(d, "gems") {
				item, qty = "Gem (1,000 GP)", 50
			}
			editInventory(charID, func(inv []invItem) []invItem {
				return append(inv, invItem{"id": newItemID("g"), "name": item, "quantity": qty})
			})
			line += fmt.Sprintf(" [Server: %d × %s added to your inventory (say \"gems\" for the fifty gems instead).]", qty, item)
		case "Knight":
			if id, _ := insertItemCompanion(charID, "summon", owner+"'s Knight", "mysterious-deck", "knight", "", "The Knight of the Mysterious Deck: it shares your alignment and serves you loyally until death. Work with the GM on its name and story.", "", 0); id > 0 {
				line += fmt.Sprintf(" [Server: the Knight is your companion (id %d).]", id)
			}
		case "Euryale":
			addDeckBoon(charID, "euryale")
			line += " [Server: -2 to your saving throws while cursed.]"
		case "Sun":
			addDeckBoon(charID, "sun")
			line += " [Server: 10 Temporary Hit Points at each dawn from now on; the GM chooses the magic item.]"
		case "Donjon":
			stopped = true // "You draw no more cards."
			_, l, _ := deckCardPending(charID, card)
			line += l
		default: // Balance, Star, Throne, Puzzle, Moon, Key, Flames, Rogue, Fates, Comet, Sage (deck_choices_2024.go)
			if p, l, ok := deckCardPending(charID, card); ok {
				line += l
				if p != "" {
					newPending = append(newPending, p)
				}
			} else {
				line += " [The GM resolves this card.]"
			}
		}
		lines = append(lines, line)
	}
	updateInventoryItem(charID, it, func(x invItem) {
		x["deck_cards"] = size
		if len(newPending) > 0 {
			x["pending_cards"] = append(pendingCards(x), newPending...)
		}
		var list []string
		for c := range removed {
			list = append(list, c)
		}
		sort.Strings(list)
		x["removed_cards"] = list
		prev, _ := x["drawn"].([]interface{})
		for _, c := range drawn {
			prev = append(prev, c)
		}
		x["drawn"] = prev
	})
	return fmt.Sprintf("You declare %d draw(s) from the %s%s and draw: %s", n, name, sizeNote, strings.Join(lines, " "))
}

// deck_boons: lasting effects of Mysterious Deck cards on a character ("euryale": -2 to saving throws; "sun": 10 Temporary Hit
// Points at each dawn).
var deckBoonsOnce sync.Once

func ensureDeckBoons() {
	deckBoonsOnce.Do(func() {
		if db != nil {
			db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS deck_boons TEXT DEFAULT ''`)
		}
	})
}

func addDeckBoon(charID int, boon string) {
	ensureDeckBoons()
	if !deckBoon(charID, boon) {
		db.Exec(`UPDATE characters SET deck_boons = TRIM(BOTH ',' FROM COALESCE(deck_boons,'') || ',' || $1) WHERE id = $2`, boon, charID)
	}
}

func deckBoon(charID int, boon string) bool {
	if charID <= 0 || db == nil {
		return false
	}
	ensureDeckBoons()
	var b string
	db.QueryRow(`SELECT COALESCE(deck_boons,'') FROM characters WHERE id = $1`, charID).Scan(&b)
	for _, x := range strings.Split(b, ",") {
		if x == boon || strings.HasPrefix(x, boon+":") { // "fool:3": a boon with dawns left (deck_choices_2024.go)
			return true
		}
	}
	return false
}

// sunCardDawn: the Sun card's 10 Temporary Hit Points at dawn (they don't stack with others: the higher stays).
func sunCardDawn(charID int) string {
	if !deckBoon(charID, "sun") {
		return ""
	}
	db.Exec(`UPDATE characters SET temp_hp = GREATEST(COALESCE(temp_hp,0), 10) WHERE id = $1`, charID)
	return "the Sun card gives 10 Temporary Hit Points"
}

var avatarOnce sync.Once

// ensureAvatarOfDeath adds the Avatar of Death (the Mysterious Deck's stat block in SRD 5.2.1) to the monsters table. Its Hit
// Points are half its summoner's Hit Point maximum (set when it joins a fight), and its Reaping Scythe hits automatically; it
// makes as many as half its summoner's Proficiency Bonus, rounded up.
func ensureAvatarOfDeath() {
	avatarOnce.Do(func() {
		if db == nil {
			return
		}
		actions := `[{"name": "Multiattack", "desc": "The avatar makes a number of Reaping Scythe attacks equal to half the summoner’s Proficiency Bonus (rounded up).", "damage_dice": "", "damage_type": "", "attack_bonus": 0},
			{"name": "Reaping Scythe", "desc": "Melee Attack Roll: Automatic hit, reach 5 ft. Hit: 7 (1d8 + 3) Slashing damage plus 4 (1d8) Necrotic damage.", "damage_dice": "1d8+3", "damage_type": "Slashing", "attack_bonus": 0}]`
		traits := `[{"name": "Incorporeal Movement", "text": "The avatar can move through other creatures and objects as if they were Difficult Terrain. It takes 5 (1d10) Force damage if it ends its turn inside an object."}]`
		db.Exec(`INSERT INTO monsters (slug, name, size, type, ac, hp, hit_dice, speed, str, dex, con, intl, wis, cha, cr, xp, actions, source, damage_immunities,
			condition_immunities, ruleset, alignment, speed_text, initiative_mod, saves, senses, languages, traits, description)
			VALUES ('avatar-of-death', 'Avatar of Death', 'medium', 'undead', 20, 1, '', 60, 16, 16, 16, 16, 16, 16, '0', 0, $1::jsonb, 'srd521', 'necrotic, poison',
			'charmed, exhaustion, frightened, paralyzed, petrified, poisoned, unconscious', '2024', 'Neutral Evil', '60 ft., Fly 60 ft. (hover)', 3,
			'{"str": 3, "dex": 3, "con": 3, "int": 3, "wis": 3, "cha": 3}'::jsonb, 'Truesight 60 ft.; Passive Perception 13', 'All languages known to its summoner', $2::jsonb,
			'Summoned by the Skull card of a Mysterious Deck (SRD 5.2.1). HP: half the Hit Point maximum of its summoner. CR None (XP 0; PB equals its summoner’s). It targets only its summoner with its attacks and disappears when it drops to 0 Hit Points or its summoner dies.')
			ON CONFLICT (slug) DO NOTHING`, actions, traits)
	})
}

// avatarAttacks: the Avatar of Death's Multiattack is half its summoner's Proficiency Bonus (rounded up) Reaping Scythes, and it
// attacks only its summoner. why is set when the target isn't the summoner.
func avatarAttacks(campaignID, attackerID, targetID int, scythe monsterActionRow) (plan []monsterActionRow, why string) {
	summoner := 0
	var name string
	for _, e := range combatEntries(campaignID) {
		if asInt(e["id"]) == attackerID {
			summoner, name = asInt(e["summoner_id"]), fmt.Sprint(e["name"])
		}
	}
	if summoner == 0 {
		return []monsterActionRow{scythe}, ""
	}
	if targetID != summoner {
		return nil, fmt.Sprintf("%s targets only %s with its attacks.", name, getCharacterName(summoner))
	}
	var level int
	db.QueryRow(`SELECT level FROM characters WHERE id = $1`, summoner).Scan(&level)
	n := (game.ProficiencyBonus(level) + 1) / 2
	for i := 0; i < n; i++ {
		plan = append(plan, scythe)
	}
	return plan, ""
}
