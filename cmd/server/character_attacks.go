package main

// Combat lines for the character stat block, in the style of the premade
// level 10 sheets: Armor Class, Speed and Initiative notes, saving throw notes,
// and one attack line per weapon the character carries.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type combatInput struct {
	Class, Subclass, Species string
	Level                    int
	Str, Dex                 int // ability scores
	WeaponProfs              string
	Inventory                []invItem
	EquippedArmor            string
	Shield                   bool
	MainHand, OffHand        string
	BaseSpeed                int
	TraitNames               []string
	ExtraAttacks             int // attacks per Attack action when known (multiclass: the best one class gives); 0 = from Class and Level
}

var rangeInParens = regexp.MustCompile(`\((\d+/\d+)\)`)

func signedBonus(n int) string {
	if n >= 0 {
		return "+" + strconv.Itoa(n)
	}
	return "-" + strconv.Itoa(-n)
}

func diceWithMod(dice string, mod int) string {
	switch {
	case dice == "":
		return ""
	case mod > 0:
		return dice + " + " + strconv.Itoa(mod)
	case mod < 0:
		return dice + " - " + strconv.Itoa(-mod)
	}
	return dice
}

func hasProperty(props []string, name string) bool {
	for _, p := range props {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), name) {
			return true
		}
	}
	return false
}

func propertyRange(props []string, name string) string {
	for _, p := range props {
		lp := strings.ToLower(strings.TrimSpace(p))
		if strings.HasPrefix(lp, name) {
			if m := rangeInParens.FindStringSubmatch(lp); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func versatileDice(props []string) string {
	for _, p := range props {
		lp := strings.ToLower(strings.TrimSpace(p))
		if strings.HasPrefix(lp, "versatile") {
			if i := strings.Index(lp, "("); i >= 0 {
				return strings.TrimSuffix(lp[i+1:], ")")
			}
		}
	}
	return ""
}

// findWeapon resolves an item name to a weapon in the weapon table.
func findWeapon(name string) (string, SRDWeapon, bool) {
	key := weaponNameKey(name)
	if key == "" {
		return "", SRDWeapon{}, false
	}
	slugs := make([]string, 0, len(srdWeapons))
	for slug := range srdWeapons {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		w := srdWeapons[slug]
		if weaponNameKey(w.Name) == key || weaponNameKey(slug) == key {
			return slug, w, true
		}
	}
	return "", SRDWeapon{}, false
}

// isMonkWeapon: SRD 5.2.1 Martial Arts works with Simple Melee weapons and Martial Melee weapons that have the Light property.
func isMonkWeapon(class string, w SRDWeapon) bool {
	if !strings.EqualFold(class, "monk") || w.Type != "melee" {
		return false
	}
	return strings.EqualFold(w.Category, "simple") || hasProperty(w.Properties, "light")
}

func weaponAttackEntry(in combatInput, key string, w SRDWeapon, qty int, strMod, dexMod, prof int) sbEntry {
	ranged := w.Type == "ranged"
	finesse := hasProperty(w.Properties, "finesse")
	thrown := propertyRange(w.Properties, "thrown")
	ammo := propertyRange(w.Properties, "ammunition")

	mod := strMod
	switch {
	case ranged:
		mod = dexMod
	case finesse || isMonkWeapon(in.Class, w):
		if dexMod > strMod {
			mod = dexMod
		}
	}
	toHit := mod
	if isWeaponProficient(in.WeaponProfs, key) {
		toHit += prof
	}

	kind := "Melee Weapon Attack"
	reach := "reach 5 ft."
	if hasProperty(w.Properties, "reach") {
		reach = "reach 10 ft."
	}
	switch {
	case ranged:
		kind = "Ranged Weapon Attack"
		r := ammo
		if r == "" {
			r = thrown
		}
		reach = "range " + r + " ft."
	case thrown != "":
		kind = "Melee or Ranged Weapon Attack"
		reach += " or range " + thrown + " ft."
	}

	dmg := diceWithMod(w.Damage, mod)
	hit := dmg + " " + w.DamageType + " damage"
	if v := versatileDice(w.Properties); v != "" {
		hit += " (" + diceWithMod(v, mod) + " if used with two hands)"
	}
	if strings.EqualFold(in.Class, "barbarian") && !ranged && mod == strMod {
		hit += " (or +" + strconv.Itoa(mod+game.RageDamageBonus(in.Level)) + " if raging)"
	}

	name := w.Name
	if qty > 1 {
		name += " (" + strconv.Itoa(qty) + ")"
	}
	return sbEntry{
		Name:        name,
		Description: kind + ": " + signedBonus(toHit) + " to hit, " + reach + ", one target. Hit: " + hit + ".",
		Kind:        "attack",
	}
}

func unarmedStrikeEntry(in combatInput, strMod, dexMod, prof int) sbEntry {
	mod := strMod
	hit := ""
	if strings.EqualFold(in.Class, "monk") {
		if dexMod > mod {
			mod = dexMod
		}
		hit = diceWithMod("1d"+strconv.Itoa(game.MartialArtsDie(in.Level)), mod) + " bludgeoning damage"
	} else {
		hit = strconv.Itoa(1+mod) + " bludgeoning damage"
	}
	return sbEntry{
		Name:        "Unarmed Strike",
		Description: "Melee Weapon Attack: " + signedBonus(mod+prof) + " to hit, reach 5 ft., one target. Hit: " + hit + ".",
		Kind:        "attack",
	}
}

// addCombatBlock adds attacks and the sheet notes to a stat block map.
func addCombatBlock(sb map[string]interface{}, in combatInput) {
	strMod, dexMod := game.Modifier(in.Str), game.Modifier(in.Dex)
	prof := game.ProficiencyBonus(in.Level)
	class := strings.ToLower(in.Class)
	byID := indexByID(in.Inventory)

	// ---- attacks: carried weapons, then equipped ones not in the pack, then Unarmed Strike
	type haveWeapon struct {
		key string
		w   SRDWeapon
		qty int
	}
	var have []haveWeapon
	seen := map[string]int{}
	add := func(name string, qty int) {
		key, w, ok := findWeapon(name)
		if !ok {
			return
		}
		if i, dup := seen[key]; dup {
			if qty > have[i].qty {
				have[i].qty = qty
			}
			return
		}
		seen[key] = len(have)
		have = append(have, haveWeapon{key, w, qty})
	}
	// Only equipped weapons can attack: the main hand and off hand, if the weapon
	// is not in a lost bag. The quantity comes from the inventory when it is there.
	qtyOf := func(name string) int {
		for _, it := range in.Inventory {
			if !isContainer(it) && !isLost(it, byID) && sameWeapon(itemStr(it, "name"), name) {
				return itemQty(it)
			}
		}
		return 1
	}
	for _, hand := range []string{in.MainHand, in.OffHand} {
		if hand != "" && !allMatchingLost(in.Inventory, hand) {
			add(hand, qtyOf(hand))
		}
	}

	attacks := []sbEntry{}
	for _, h := range have {
		attacks = append(attacks, weaponAttackEntry(in, h.key, h.w, h.qty, strMod, dexMod, prof))
	}
	attacks = append(attacks, unarmedStrikeEntry(in, strMod, dexMod, prof))
	sb["attacks"] = attacks

	n := game.ExtraAttackCount(in.Class, in.Level)
	if in.ExtraAttacks > 0 {
		n = in.ExtraAttacks
	}
	if n > 1 {
		sb["attack_action"] = "You can attack " + attackCountPhrase(n) + " when you take this action, using the following:"
	} else {
		sb["attack_action"] = "You can make one attack when you take this action, using the following:"
	}

	// ---- Armor Class note
	noArmor := in.EquippedArmor == ""
	switch {
	case noArmor && (class == "barbarian" || class == "monk"):
		sb["armor_class_note"] = "Unarmored Defense"
	case noArmor && strings.EqualFold(in.Subclass, "draconic"):
		sb["armor_class_note"] = "Draconic Resilience"
	}

	// ---- Speed note
	speed := in.BaseSpeed
	heavyArmor := false
	if in.EquippedArmor != "" {
		if info, err := getArmorInfo(in.EquippedArmor); err == nil && info != nil {
			heavyArmor = strings.EqualFold(info.Type, "heavy")
		}
	}
	switch {
	case class == "barbarian" && in.Level >= 5 && !heavyArmor:
		speed += 10
		sb["speed_note"] = "Fast Movement"
	case class == "monk" && noArmor && !in.Shield && in.Level >= 2:
		speed += game.UnarmoredMovementBonus(in.Level)
		sb["speed_note"] = "Unarmored Movement"
	}
	sb["speed_feet"] = speed

	// ---- Initiative note
	if class == "barbarian" && in.Level >= 7 {
		sb["initiative_note"] = "advantage (Feral Instinct)"
	}

	// ---- saving throw notes
	notes := []string{}
	has := func(name string) bool {
		for _, t := range in.TraitNames {
			if strings.EqualFold(t, name) {
				return true
			}
		}
		return false
	}
	if has("Gnomish Cunning") {
		notes = append(notes, "Advantage on Intelligence, Wisdom and Charisma saving throws")
	}
	if has("Dwarven Resilience") {
		notes = append(notes, "advantage on saving throws against poison")
	}
	if has("Fey Ancestry") {
		notes = append(notes, "advantage on saving throws against being charmed")
	}
	if has("Brave") {
		notes = append(notes, "advantage on saving throws against being frightened")
	}
	if class == "barbarian" && in.Level >= 2 {
		notes = append(notes, "advantage on Dexterity saving throws against effects you can see (Danger Sense)")
	}
	if class == "monk" && in.Level >= 7 {
		notes = append(notes, "Evasion")
	}
	sb["save_notes"] = notes
}

func attackCountPhrase(n int) string {
	switch n {
	case 2:
		return "twice"
	case 3:
		return "three times"
	case 4:
		return "four times"
	}
	return strconv.Itoa(n) + " times"
}
