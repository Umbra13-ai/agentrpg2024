package main

// Named magic weapons (SRD 5.2.1 Magic Items; F49, Nov 8, 2026): Holy Avenger, Vorpal Sword, Luck Blade, Defender, Dragon Slayer,
// Dagger of Venom and the rest. The type line names the weapons each one can be ("Weapon (Glaive, Greatsword, Longsword, or
// Scimitar)"; "Weapon (Any Simple or Martial)"), and the text gives its bonus ("You gain a +3 bonus to attack rolls and damage rolls
// made with this magic weapon"). Before this, a named weapon equipped by its name attacked as an Unarmed Strike: the server found
// no weapon in the name.
//
// The base weapon is chosen when the item is equipped (equip-weapon "base_weapon": "greatsword") and kept on the inventory item
// ("base_weapon"); an item that can be only one weapon (Dagger of Venom, Sun Blade) takes it without asking. Attacks with it use
// the base weapon's damage and properties, the character's proficiency with that weapon, and the item's bonus.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type namedWeapon struct {
	Name    string
	Options []string // srdWeapons keys the item can be; empty when Any is set
	Any     string   // "simple or martial", "melee weapon" ("Weapon (Any Melee Weapon)")
	Bonus   int      // the +N to attack and damage rolls
}

var (
	namedWeaponsOnce sync.Once
	namedWeapons     map[string]*namedWeapon // lower-case item name
	weaponTypeLineRe = regexp.MustCompile(`^Weapon \(([^)]*)\)`)
	namedBonusRe     = regexp.MustCompile(`\+(\d) bonus to attack (?:rolls )?and damage rolls made with this`)
	staffWeaponRe    = regexp.MustCompile(`wielded as a magic Quarterstaff(?: that grants a \+(\d) bonus)?`)
)

func loadNamedWeapons() {
	namedWeaponsOnce.Do(func() {
		namedWeapons = map[string]*namedWeapon{}
		var items []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			TypeLine    string `json:"type_line"`
		}
		json.Unmarshal(srd521MagicItemsJSON, &items)
		for _, it := range items {
			m := weaponTypeLineRe.FindStringSubmatch(it.TypeLine)
			if m == nil && strings.HasPrefix(it.TypeLine, "Staff") { // a staff wielded as a magic Quarterstaff (Staff of the Magi +2)
				if q := staffWeaponRe.FindStringSubmatch(it.Description); q != nil {
					nw := &namedWeapon{Name: it.Name, Options: []string{"quarterstaff"}}
					if q[1] != "" {
						nw.Bonus = int(q[1][0] - '0')
					}
					namedWeapons[strings.ToLower(it.Name)] = nw
				}
				continue
			}
			if m == nil || strings.Contains(it.Name, "+1") { // "Weapon, +1, +2, or +3" is named by its +N ("Longsword +2")
				continue
			}
			nw := &namedWeapon{Name: it.Name}
			if b := namedBonusRe.FindStringSubmatch(it.Description); b != nil {
				nw.Bonus = int(b[1][0] - '0')
			}
			inside := strings.ToLower(strings.TrimSpace(m[1]))
			if strings.HasPrefix(inside, "any ") {
				nw.Any = strings.TrimPrefix(inside, "any ")
				if nw.Any == "ammunition" {
					continue // ammunition isn't held or equipped
				}
			} else {
				for _, part := range strings.Split(strings.ReplaceAll(inside, " or ", ","), ",") {
					if key, _, ok := findWeapon(strings.TrimSpace(part)); ok {
						nw.Options = append(nw.Options, key)
					}
				}
			}
			namedWeapons[strings.ToLower(it.Name)] = nw
		}
	})
}

// namedWeaponFor finds the named magic weapon an item name is ("Vorpal Sword"), or nil.
func namedWeaponFor(name string) *namedWeapon {
	loadNamedWeapons()
	key := strings.ToLower(strings.TrimSpace(name))
	if nw, ok := namedWeapons[key]; ok {
		return nw
	}
	if i := strings.Index(key, " ("); i > 0 { // "Nine Lives Stealer (Longsword)": the base weapon in parentheses (F148)
		return namedWeapons[strings.TrimSpace(key[:i])]
	}
	return nil
}

// allows: the named weapon can be that SRD weapon.
func (nw *namedWeapon) allows(key string) bool {
	w, ok := srdWeapons[key]
	if !ok {
		return false
	}
	switch {
	case len(nw.Options) > 0:
		for _, o := range nw.Options {
			if o == key {
				return true
			}
		}
		return false
	case nw.Any == "melee weapon":
		return w.Type == "melee"
	}
	return true // any Simple or Martial weapon
}

// choices lists what the named weapon can be, for messages.
func (nw *namedWeapon) choices() string {
	if len(nw.Options) == 0 {
		if nw.Any == "melee weapon" {
			return "any Melee weapon"
		}
		return "any Simple or Martial weapon"
	}
	names := append([]string(nil), nw.Options...)
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// inventoryItemNamed finds an inventory item by name (the first that isn't destroyed).
func inventoryItemNamed(charID int, name string) invItem {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []invItem
	json.Unmarshal(raw, &inv)
	for _, it := range inv {
		if strings.EqualFold(itemStr(it, "name"), strings.TrimSpace(name)) && !itemBool(it, "destroyed") {
			return it
		}
	}
	return nil
}

// heldWeaponKey is the SRD weapon a held item is: "Longsword +2" -> longsword; "Vorpal Sword" -> its base_weapon (or its only
// choice). "" when it isn't a weapon the server knows.
func heldWeaponKey(charID int, held string) string {
	if strings.HasPrefix(strings.ToLower(held), "rod of lordly might") { // its form (legendary_more_2024.go)
		if it := inventoryItemNamed(charID, held); it != nil {
			return rodWeaponKey(it)
		}
		return "mace"
	}
	if k := parseWeaponFromDescription(held); k != "" {
		return k
	}
	nw := namedWeaponFor(held)
	if nw == nil {
		return ""
	}
	if it := inventoryItemNamed(charID, held); it != nil {
		if b := itemStr(it, "base_weapon"); b != "" && nw.allows(b) {
			return b
		}
	}
	if len(nw.Options) == 1 {
		return nw.Options[0]
	}
	return ""
}

// heldMatches: the held item is that weapon (by name or by its base weapon).
func heldMatches(charID int, held, weapon string) bool {
	if held == "" || weapon == "" {
		return false
	}
	if sameWeapon(weapon, held) {
		return true
	}
	k := heldWeaponKey(charID, held)
	return k != "" && sameWeapon(weapon, k)
}

// heldItemFor is the item in a hand that an attack with weaponKey uses: the main hand first, or the off hand (only the off hand
// when offHand is set). "" when neither hand holds it.
func heldItemFor(charID int, weaponKey string, offHand bool) string {
	m, o := equippedWeapons(charID)
	if !offHand && heldMatches(charID, m, weaponKey) {
		return m
	}
	if heldMatches(charID, o, weaponKey) {
		return o
	}
	return ""
}

// namedWeaponInDescription: the description names a held named weapon ("swing the Vorpal Sword"); its base weapon key, or "".
func namedWeaponInDescription(charID int, description string) string {
	d := strings.ToLower(description)
	m, o := equippedWeapons(charID)
	for _, h := range []string{m, o} {
		short := strings.ToLower(h)
		if i := strings.Index(short, " ("); i > 0 {
			short = short[:i] // "sword of wounding" names "Sword of Wounding (Shortsword)" (F166)
		}
		if h != "" && namedWeaponFor(h) != nil && (strings.Contains(d, strings.ToLower(h)) || strings.Contains(d, short)) {
			return heldWeaponKey(charID, h)
		}
	}
	return ""
}

// weaponPlusFor is the magic bonus of the weapon an attack uses: a "+N" in the description, else the held item's "+N" or its
// named-weapon bonus.
func weaponPlusFor(charID int, description, weaponKey string, offHand bool) int {
	if p := itemPlus(description); p > 0 && !offHand {
		return p
	}
	item := heldItemFor(charID, weaponKey, offHand)
	if item == "" {
		return 0
	}
	if strings.HasPrefix(strings.ToLower(item), "rod of lordly might") {
		if it, ok := itemActive(charID, "rod of lordly might", "held"); ok {
			return rodPlus(it)
		}
		return 0
	}
	if p := itemPlus(item); p > 0 {
		return p
	}
	if nw := namedWeaponFor(item); nw != nil {
		if itemNeedsAttunement(nw.Name) {
			if _, ok := attunedTo(charID, nw.Name); !ok {
				return 0 // without attunement, only its nonmagical benefits
			}
		}
		if strings.EqualFold(nw.Name, "Defender") {
			return nw.Bonus - defenderShift(lobbyOfChar(charID), charID) // the part moved to AC this turn
		}
		return nw.Bonus
	}
	return buffWeaponPlus(charID, item) // Magic Weapon on a nonmagical weapon (buff_spells_2024.go)
}

// equipNamedWeapon settles the base weapon of a named magic weapon being equipped: the one asked for, the one it already has, or
// its only choice. It sets "base_weapon" on the item (the caller saves the inventory). key is "" for an item that isn't a named
// weapon; refusal is set when a choice is needed or not allowed.
func equipNamedWeapon(item invItem, asked string) (key string, changed bool, refusal string) {
	if item == nil {
		return "", false, ""
	}
	if strings.HasPrefix(strings.ToLower(itemStr(item, "name")), "rod of lordly might") {
		return rodWeaponKey(item), false, "" // its buttons choose its form (use_item)
	}
	nw := namedWeaponFor(itemStr(item, "name"))
	if nw == nil {
		return "", false, ""
	}
	if asked != "" {
		k, _, ok := findWeapon(asked)
		if !ok || !nw.allows(k) {
			return "", false, fmt.Sprintf("The %s can't be a %s. It can be: %s.", nw.Name, asked, nw.choices())
		}
		return k, itemStr(item, "base_weapon") != k, ""
	}
	if b := itemStr(item, "base_weapon"); b != "" && nw.allows(b) {
		return b, false, ""
	}
	if len(nw.Options) == 1 {
		return nw.Options[0], true, ""
	}
	return "", false, fmt.Sprintf("Which weapon is your %s? Equip it with \"base_weapon\": one of %s (it stays that weapon).", nw.Name, nw.choices())
}

// attackHeldItem: the item the character's current attack uses ("Vorpal Sword"), for the effects resolved on a hit.
var attackHeldItem sync.Map

// ---- Vorpal Sword (SRD 5.2.1): it ignores Resistance to Slashing damage, and a roll of 20 against a creature with a head cuts one
// off. The creature dies if it can't survive without it. It is immune to the beheading if it has Immunity to Slashing damage, has
// or needs no head, or is too big (the GM's call): it takes an extra 30 Slashing damage instead. A creature with Legendary
// Resistance can spend a daily use to keep its head and take the extra damage.
// The server's defaults for the GM's calls: Oozes, Swarms, the four Elementals, the Shambling Mound and the Will-o'-Wisp have no
// head they need; Gargantuan creatures are too big; a creature with a trait about its several heads (the Hydra) loses a head and
// lives.

var headlessMonsters = map[string]bool{"air-elemental": true, "earth-elemental": true, "fire-elemental": true, "water-elemental": true,
	"shambling-mound": true, "will-o-wisp": true}

func vorpalHeld(item string) bool { return strings.EqualFold(strings.TrimSpace(item), "Vorpal Sword") }

// vorpalBehead resolves a Vorpal Sword's roll of 20 against a monster still standing: the damage to add (cut) or a kill, and the
// text for the attacker.
func vorpalBehead(lobbyID, targetID int, monsterKey, name string) (cut int, killed bool, note string) {
	var mtype, size, immune, vuln string
	db.QueryRow(`SELECT COALESCE(type,''), COALESCE(size,''), COALESCE(damage_immunities,''), COALESCE(damage_vulnerabilities,'') FROM monsters WHERE slug = $1`, monsterKey).
		Scan(&mtype, &size, &immune, &vuln)
	extra := func(why string) (int, bool, string) {
		d := 30
		if strings.Contains(strings.ToLower(vuln), "slashing") {
			d = 60
		}
		return d, false, fmt.Sprintf(" Vorpal Sword (a roll of 20): %s, so it takes an extra %d Slashing damage instead.", why, d)
	}
	_, meta, _ := monsterCombatant(lobbyID, targetID)
	switch {
	case strings.Contains(strings.ToLower(immune), "slashing"):
		return 0, false, fmt.Sprintf(" Vorpal Sword (a roll of 20): %s has Immunity to Slashing damage; its head stays on.", name)
	case strings.EqualFold(mtype, "ooze") || strings.HasPrefix(monsterKey, "swarm-") || headlessMonsters[monsterKey]:
		return extra(name + " has no head it needs")
	case strings.EqualFold(size, "gargantuan"):
		return extra(name + " is too big for its head to be cut off")
	}
	if meta != nil {
		for _, t := range meta.Traits {
			if strings.Contains(strings.ToLower(t.Name), "heads") {
				return 0, false, fmt.Sprintf(" Vorpal Sword (a roll of 20): one of %s's heads is cut off (%s: it has more than one).", name, t.Name)
			}
		}
		if meta.LR > 0 {
			s := loadMonsterState(lobbyID, targetID)
			if _, total := legendaryUseTotals(meta, s); s.LRUsed < total {
				s.LRUsed++
				saveMonsterState(lobbyID, targetID, s)
				recordGMAction(db, lobbyID, "legendary_resistance", fmt.Sprintf("🛡️ %s spends a Legendary Resistance use to keep its head (Vorpal Sword).", name),
					fmt.Sprintf("%d of %d Legendary Resistance uses left today.", total-s.LRUsed, total))
				return extra(name + " spends a use of Legendary Resistance to keep its head")
			}
		}
	}
	return 0, true, fmt.Sprintf(" Vorpal Sword (a roll of 20): you cut off %s's head!", name)
}

// namedWeaponNeedsBase: the main hand holds a named weapon whose base weapon was never chosen (equipped before F49). The attack is
// refused with how to choose it, unless the player asks for an unarmed strike.
func namedWeaponNeedsBase(held, description string) string {
	nw := namedWeaponFor(held)
	if nw == nil {
		return ""
	}
	d := strings.ToLower(description)
	for _, w := range []string{"unarmed", "punch", "kick", "headbutt", "fist"} {
		if strings.Contains(d, w) {
			return ""
		}
	}
	return fmt.Sprintf("Cannot attack with your %s yet: which weapon is it? Equip it again with its weapon: POST /api/characters/equip-weapon {\"character_id\": N, \"weapon\": \"%s\", \"base_weapon\": one of %s}. (Say \"unarmed strike\" to fight without it.) No action was spent.", nw.Name, nw.Name, nw.choices())
}
