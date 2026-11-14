package main

// Fixes from Pip's coverage session (level20-battle-tests/s25_pip_rogue.py, Nov 8 2026; FINDINGS F111-F114):
//	Stone of Good Luck     its +1 was missing from the GM's ability checks (F111, gm_checks.go)
//	Boots of Elvenkind     Advantage on Dexterity (Stealth) checks while worn (F112)
//	Cloak of Displacement  attuned: attack rolls against the wearer have Disadvantage; it stops after the wearer takes damage until the start
//	                       of its next turn, and while its Speed is 0 (F113)
//	Dancing Sword          a Bonus Action tosses it: it flies up to 30 feet and attacks a creature within 5 feet of itself with the wielder's
//	                       attack roll; a Bonus Action moves it and attacks again; after the fourth attack it returns (F114)

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// wearsItem: the item is in the character's usable inventory and not packed in a bag (worn, held, or with no place given).
func wearsItem(charID int, prefix string) bool {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []map[string]interface{}
	jsonUnmarshal(raw, &inv)
	for _, it := range usableInventory(inv) {
		n := strings.ToLower(fmt.Sprint(it["name"]))
		loc := strings.ToLower(fmt.Sprint(it["location"]))
		if strings.HasPrefix(n, prefix) && (loc == "worn" || loc == "held" || loc == "<nil>" || loc == "") {
			return true
		}
	}
	return false
}

// bootsOfElvenkind: Advantage on Dexterity (Stealth) checks.
func bootsOfElvenkind(charID int, skill string) bool {
	return (strings.EqualFold(strings.ReplaceAll(skill, " ", "_"), "stealth") && wearsItem(charID, "boots of elvenkind")) || cloakOfTheBatStealth(charID, skill) // + Cloak of the Bat (F145)
}

var displacementDown sync.Map // charID -> true after it took damage, until the start of its next turn

// cloakOfDisplacement: attack rolls against the wearer have Disadvantage (not after damage until its next turn, nor at Speed 0).
func cloakOfDisplacement(lobby, charID int) bool {
	if charID <= 0 {
		return false
	}
	if _, ok := attunedTo(charID, "cloak of displacement"); !ok {
		return false
	}
	if _, down := displacementDown.Load(charID); down {
		return false
	}
	for _, c := range getCharConditions(charID) {
		switch strings.ToLower(strings.SplitN(c, ":", 2)[0]) {
		case "grappled", "restrained", "paralyzed", "petrified", "stunned", "unconscious":
			return false // Speed 0: the illusion is suppressed
		}
	}
	return true
}

// cloakTakesDamage: the property stops until the start of the wearer's next turn.
func cloakTakesDamage(charID int) {
	if _, ok := attunedTo(charID, "cloak of displacement"); ok {
		displacementDown.Store(charID, true)
	}
}

// ---------------------------------------------------------------- Dancing Sword

func init() {
	noConcentration["dancing_sword"] = true
	spellEffectLabel["dancing_sword"] = "Dancing Sword"
	spellEffectText["dancing_sword"] = "The Dancing Sword hovers and attacks (four attacks, then it returns)"
}

// dancingSword: toss it (or move the hovering sword) and attack the target (target_id) with the wielder's attack roll.
func dancingSword(charID int, it invItem, req itemCastReq) string {
	name := itemStr(it, "name")
	if _, ok := attunedTo(charID, "dancing sword"); !ok {
		return "Cannot use the " + name + ": you must be attuned to it (POST /api/characters/attune). Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if req.TargetID >= 0 { // a monster's target_id is negative
		return "Cannot send the " + name + ": name the creature it attacks with target_id. Nothing was spent."
	}
	e, hovering := charBuff(charID, "dancing_sword")
	if !hovering {
		h := handsOf(charID)
		if !strings.EqualFold(h.Main, name) && !strings.EqualFold(h.Off, name) {
			return "Cannot toss the " + name + ": hold it first (equip-weapon). Nothing was spent."
		}
	}
	if m, ok := activeEncounterMap(lobby); ok {
		me, ok1 := m.token(charID)
		if t, ok2 := m.token(req.TargetID); ok1 && ok2 && tokenDistanceFeet(me, t) > 35 {
			return fmt.Sprintf("Cannot send the %s: %s is %d ft away; the sword flies up to 30 feet within 30 feet of you and attacks a creature within 5 feet of itself. Nothing was spent.", name, t.Name, tokenDistanceFeet(me, t))
		}
	}
	base := baseWeaponOf(name)
	w, okw := srdWeapons[base]
	dice, dtype := "1d8", "slashing"
	if okw {
		dice, dtype = w.Damage, strings.ToLower(w.DamageType)
	}
	var level, str, dex int
	db.QueryRow(`SELECT level, str, dex FROM characters WHERE id = $1`, charID).Scan(&level, &str, &dex)
	mod := game.Modifier(str)
	if okw && containsProperty(w.Properties, "finesse") && game.Modifier(dex) > mod {
		mod = game.Modifier(dex)
	}
	var ac int
	var tname string
	for _, en := range combatEntries(lobby) {
		if asInt(en["id"]) == req.TargetID {
			ac, tname = asInt(en["ac"]), fmt.Sprint(en["name"])
		}
	}
	if tname == "" {
		return "Cannot send the " + name + ": that creature isn't in the fight. Nothing was spent."
	}
	attacks := 0
	if hovering {
		attacks = atoiSafe(e.Detail)
	} else {
		db.Exec(`UPDATE characters SET equipped_main_hand = NULL WHERE id = $1 AND equipped_main_hand = $2`, charID, name)
	}
	attacks++
	nat := game.RollDie(20)
	total := nat + mod + game.ProficiencyBonus(level)
	text := fmt.Sprintf("The %s flies and attacks %s (attack roll %d)", name, tname, total)
	if nat == 20 || (nat != 1 && total >= ac) {
		dmg := game.RollDamage(dice, nat == 20) + mod
		if dmg < 1 {
			dmg = 1
		}
		res, _ := damageMonster(lobby, req.TargetID, dmg, dtype)
		text += fmt.Sprintf(" - %s! %s.", map[bool]string{true: "CRITICAL HIT", false: "HIT"}[nat == 20], strings.TrimRight(strings.TrimSpace(res), "."))
	} else {
		text += " - MISS."
	}
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'dancing_sword'`, lobby, charID)
	if attacks >= 4 {
		db.Exec(`UPDATE characters SET equipped_main_hand = $1 WHERE id = $2 AND COALESCE(equipped_main_hand,'') = ''`, name, charID)
		return text + " That was its fourth attack: it flies back to your hand (or falls in your space if no hand is free)."
	}
	addSpellEffect(lobby, charID, charID, "Dancing Sword", "dancing_sword", strconv.Itoa(attacks), 999999, "", 0)
	return text + fmt.Sprintf(" It hovers (%d of 4 attacks); a Bonus Action moves it up to 30 feet and attacks again.", attacks)
}

// attunementNotNeeded: an SRD magic item that doesn't require attunement (unknown or homebrew items are not refused).
func attunementNotNeeded(item string) bool {
	var items []srdMagicItem
	if json.Unmarshal(srd521MagicItemsJSON, &items) != nil {
		return false
	}
	for _, m := range items {
		if strings.EqualFold(m.Name, strings.TrimSpace(item)) {
			return !m.RequiresAttunement
		}
	}
	return false
}

// isSRDMagicItem: whether a name is an SRD 5.2.1 magic item (attunement needs one).
func isSRDMagicItem(item string) bool {
	var items []srdMagicItem
	if json.Unmarshal(srd521MagicItemsJSON, &items) != nil {
		return true // can't check: don't refuse
	}
	for _, m := range items {
		if strings.EqualFold(m.Name, strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

// baseWeaponOf: "Dancing Sword (Rapier)" -> "rapier".
func baseWeaponOf(name string) string {
	if i := strings.Index(name, "("); i >= 0 {
		if j := strings.Index(name[i:], ")"); j > 0 {
			return strings.ToLower(strings.TrimSpace(name[i+1 : i+j]))
		}
	}
	return "longsword"
}
