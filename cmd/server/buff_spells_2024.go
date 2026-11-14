package main

// Lasting buff spells (SRD 5.2.1), applied by the server. They used to cast (slot spent, text shown) and change nothing (BUGS O-1;
// level-20 FINDINGS, F71). Each puts a spell effect on its targets (spell_effects), and the hooks elsewhere read it:
//   Aid, Heroes' Feast       Hit Point maximum and current Hit Points up; down again when the spell ends (endItemEffect)
//   Barkskin                 AC at least 17 (computeCharacterAC)
//   Death Ward               the first drop to 0 Hit Points is a drop to 1 instead (handleDamage)
//   Foresight                Advantage on D20 Tests (attacks, saves, checks, death saves); attacks against it have Disadvantage
//   Mirror Image             three duplicates: a hit lands on one on a d6 of 3+ (a die per duplicate)
//   Blur                     attacks against the caster have Disadvantage (not from an attacker with Blindsight or Truesight)
//   Stoneskin                Resistance to Bludgeoning, Piercing and Slashing damage
//   Fire Shield              warm: Resistance to Cold, 2d8 Fire back; chill: Resistance to Fire, 2d8 Cold back (a melee hit within 5 ft)
//   Holy Aura                the caster's allies within 30 ft: Advantage on saves, attacks against them at Disadvantage; a Fiend or
//                            Undead that hits one in melee makes a Constitution save or is Blinded until the end of its next turn
//   Mind Blank               Immunity to Psychic damage and the Charmed condition
//   Freedom of Movement      spells and magical effects can't Paralyze, Restrain or slow it; Difficult Terrain costs nothing extra
//   Protection from Energy   Resistance to the chosen Acid, Cold, Fire, Lightning or Thunder damage
//   Protection from Evil and Good  Aberrations, Celestials, Elementals, Fey, Fiends and Undead attack it with Disadvantage and can't
//                            Charm or Frighten it
//   Protection from Poison   the Poisoned condition ends; Resistance to Poison damage, Advantage on saves against being Poisoned
//   Warding Bond             +1 AC and saves, Resistance to all damage within 60 ft of the caster, who takes the same damage
//   Beacon of Hope           Advantage on Wisdom saves and Death Saving Throws; healing at its maximum
//   Longstrider              Speed +10 feet
//   Enhance Ability          Advantage on ability checks with the chosen ability
//   Invisibility, Greater Invisibility  the Invisible condition (Invisibility ends when the target attacks, casts a spell or deals damage)
//   Magic Weapon             a nonmagical weapon becomes +1 (+2 with a level 3-5 slot, +3 with 6+)
//   Spider Climb, See Invisibility, True Seeing  a Climb Speed; Invisible creatures seen; Truesight 120 feet
//   Sanctuary                an attacker of the warded creature saves (Wisdom) or loses the attack; it ends when the warded creature
//                            attacks, casts a spell or deals damage
//   Divine Favor             weapon hits deal 1d4 Radiant damage more
//   Pass without Trace       +10 to Dexterity (Stealth) checks within 30 ft of the caster
//   Lesser Restoration       ends one condition: Blinded, Deafened, Paralyzed or Poisoned

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type buffSpell struct {
	Kind   string
	Rounds int           // the duration (10 rounds = 1 minute)
	Self   bool          // Range Self: the caster only
	Most   func(int) int // how many creatures at a slot level (nil: one)
}

func onePlusAbove(level int) func(int) int {
	return func(slot int) int { return 1 + maxInt(0, slot-level) }
}

var buffSpells = map[string]buffSpell{
	"aid":                           {"aid", 4800, false, func(int) int { return 3 }},
	"barkskin":                      {"barkskin", 600, false, nil},
	"beacon-of-hope":                {"beacon_of_hope", 10, false, func(int) int { return 99 }},
	"blur":                          {"blur", 10, true, nil},
	"death-ward":                    {"death_ward", 4800, false, nil},
	"divine-favor":                  {"divine_favor", 10, true, nil},
	"enhance-ability":               {"enhance_ability", 600, false, onePlusAbove(2)},
	"fire-shield":                   {"fire_shield", 100, true, nil},
	"foresight":                     {"foresight", 4800, false, nil},
	"freedom-of-movement":           {"freedom_of_movement", 600, false, onePlusAbove(4)},
	"greater-invisibility":          {"greater_invisibility", 10, false, nil},
	"heroes-feast":                  {"heroes_feast", 14400, false, func(int) int { return 13 }}, // up to twelve creatures, and the caster
	"holy-aura":                     {"holy_aura", 10, true, nil},
	"invisibility":                  {"invisibility", 600, false, onePlusAbove(2)},
	"longstrider":                   {"longstrider", 600, false, onePlusAbove(1)},
	"magic-weapon":                  {"magic_weapon", 600, false, nil},
	"mind-blank":                    {"mind_blank", 14400, false, nil},
	"mirror-image":                  {"mirror_image", 10, true, nil},
	"pass-without-trace":            {"pass_without_trace", 600, true, nil},
	"protection-from-energy":        {"protection_from_energy", 600, false, nil},
	"protection-from-evil-and-good": {"protection_from_evil_and_good", 100, false, nil},
	"protection-from-poison":        {"protection_from_poison", 600, false, nil},
	"sanctuary":                     {"sanctuary", 10, false, nil},
	"see-invisibility":              {"see_invisibility", 600, true, nil},
	"spider-climb":                  {"spider_climb", 600, false, onePlusAbove(2)},
	"stoneskin":                     {"stoneskin", 600, false, nil},
	"true-seeing":                   {"true_seeing", 600, false, nil},
	"warding-bond":                  {"warding_bond", 600, false, nil},
}

var buffLabels = map[string][2]string{
	"aid":                           {"Aid", "Hit Point maximum and current Hit Points raised (8 hours)"},
	"barkskin":                      {"Barkskin", "Armor Class at least 17 (1 hour)"},
	"beacon_of_hope":                {"Beacon of Hope", "Advantage on Wisdom saves and Death Saving Throws; healing at its maximum"},
	"blur":                          {"Blur", "Attack rolls against it have Disadvantage (not from Blindsight or Truesight)"},
	"death_ward":                    {"Death Ward", "The first drop to 0 Hit Points is a drop to 1 instead (8 hours)"},
	"divine_favor":                  {"Divine Favor", "Weapon hits deal 1d4 Radiant damage more (1 minute)"},
	"enhance_ability":               {"Enhance Ability", "Advantage on ability checks with the chosen ability"},
	"fire_shield":                   {"Fire Shield", "Resistance (Cold for warm, Fire for chill); 2d8 back at a creature that hits it in melee within 5 feet"},
	"foresight":                     {"Foresight", "Advantage on D20 Tests; attack rolls against it have Disadvantage (8 hours)"},
	"freedom_of_movement":           {"Freedom of Movement", "Spells and magical effects can't Paralyze, Restrain or slow it; Difficult Terrain costs nothing extra; Swim Speed"},
	"greater_invisibility":          {"Greater Invisibility", "Invisible while the caster keeps Concentration"},
	"heroes_feast":                  {"Heroes' Feast", "Hit Point maximum raised; Resistance to Poison; Immunity to Frightened and Poisoned (24 hours)"},
	"holy_aura":                     {"Holy Aura", "Allies within 30 feet: Advantage on saves, attacks against them at Disadvantage"},
	"invisibility":                  {"Invisibility", "Invisible until it attacks, casts a spell or deals damage"},
	"longstrider":                   {"Longstrider", "Speed +10 feet (1 hour)"},
	"magic_weapon":                  {"Magic Weapon", "The weapon is a magic weapon with a bonus to attack and damage rolls (1 hour)"},
	"mind_blank":                    {"Mind Blank", "Immunity to Psychic damage and the Charmed condition (24 hours)"},
	"mirror_image":                  {"Mirror Image", "Illusory duplicates take hits on a d6 of 3+ (one die per duplicate)"},
	"pass_without_trace":            {"Pass without Trace", "+10 to Dexterity (Stealth) checks within 30 feet of the caster"},
	"protection_from_energy":        {"Protection from Energy", "Resistance to the chosen damage type"},
	"protection_from_evil_and_good": {"Protection from Evil and Good", "Aberrations, Celestials, Elementals, Fey, Fiends and Undead attack it with Disadvantage and can't Charm or Frighten it"},
	"protection_from_poison":        {"Protection from Poison", "Resistance to Poison damage; Advantage on saves against being Poisoned (1 hour)"},
	"sanctuary":                     {"Sanctuary", "An attacker makes a Wisdom save or loses the attack; ends when it attacks, casts a spell or deals damage"},
	"see_invisibility":              {"See Invisibility", "Sees Invisible creatures (1 hour)"},
	"spider_climb":                  {"Spider Climb", "Climb Speed equal to its Speed; walks on walls and ceilings"},
	"stoneskin":                     {"Stoneskin", "Resistance to Bludgeoning, Piercing and Slashing damage"},
	"true_seeing":                   {"True Seeing", "Truesight 120 feet (1 hour)"},
	"warding_bond":                  {"Warding Bond", "+1 AC and saves, Resistance to all damage; its caster takes the same damage"},
}

// the effects that last their full duration without the caster's Concentration
var buffNoConcentration = []string{"aid", "barkskin", "death_ward", "divine_favor", "fire_shield", "foresight", "freedom_of_movement",
	"heroes_feast", "longstrider", "magic_weapon", "mind_blank", "mirror_image", "protection_from_poison", "sanctuary",
	"see_invisibility", "true_seeing", "warding_bond"}

func init() {
	for _, k := range buffNoConcentration {
		noConcentration[k] = true
	}
	for k, v := range buffLabels {
		spellEffectLabel[k] = v[0]
		spellEffectText[k] = v[1]
	}
}

// buffEffect is the first effect of a kind on a creature that still holds (duration and Concentration).
func buffEffect(lobbyID, id int, kind string) (activeSpellEffect, bool) {
	if lobbyID == 0 || id == 0 {
		return activeSpellEffect{}, false
	}
	if kind != "antimagic_field" && antimagicCovers(lobbyID, id) {
		return activeSpellEffect{}, false // inside an Antimagic Field spell effects are suppressed (spells_more_2024.go)
	}
	for _, e := range spellEffectsOn(lobbyID, id) {
		if e.Kind == kind {
			return e, true
		}
	}
	return activeSpellEffect{}, false
}

func charBuff(charID int, kind string) (activeSpellEffect, bool) {
	if charID <= 0 {
		return activeSpellEffect{}, false
	}
	return buffEffect(lobbyOfChar(charID), charID, kind)
}

func storedAC(charID int) int {
	var ac int
	db.QueryRow(`SELECT ac FROM characters WHERE id = $1`, charID).Scan(&ac)
	return ac
}

func abilityFromWords(s string) string {
	l := strings.ToLower(s)
	for _, p := range [][2]string{{"strength", "str"}, {"dexterity", "dex"}, {"intelligence", "int"}, {"wisdom", "wis"}, {"charisma", "cha"},
		{" str", "str"}, {" dex", "dex"}, {" int", "int"}, {" wis", "wis"}, {" cha", "cha"}} {
		if strings.Contains(" "+l, p[0]) {
			return p[1]
		}
	}
	return ""
}

func shortAbility(a string) string {
	a = strings.ToLower(strings.TrimSpace(a))
	if len(a) >= 3 {
		a = a[:3]
	}
	if a == "int" || a == "str" || a == "dex" || a == "con" || a == "wis" || a == "cha" {
		return a
	}
	return ""
}

// resolveBuffSpell applies a lasting buff spell (buffSpells) or Lesser Restoration; "" for other spells.
func resolveBuffSpell(charID, lobby int, spell SRDSpell, spellKey string, slotLevel, saveDC int, description string) string {
	if t := resolveMoreSpell(charID, lobby, spell, spellKey, slotLevel, saveDC, description); t != "" {
		return t // Blink, Fly, Polymorph, Antimagic Field ... (spells_more_2024.go)
	}
	if spellKey == "lesser-restoration" {
		return lesserRestoration(charID, lobby, description)
	}
	b, ok := buffSpells[spellKey]
	if !ok {
		return ""
	}
	ids := []int{charID}
	if !b.Self {
		if t := spellCharacterTargets(charID); len(t) > 0 {
			ids = t
		} else if t := parseTargetsFromDescription(description, charID); len(t) > 0 {
			ids = t // every character named (F229)
		}
	}
	most := 1
	if b.Most != nil {
		most = b.Most(slotLevel)
	}
	note := ""
	if len(ids) > most {
		note = fmt.Sprintf(" (only the first %d: the spell affects %d at this slot level)", most, most)
		ids = ids[:most]
	}
	dl := strings.ToLower(description)
	name := func(id int) string { return creatureName(lobby, id) }
	add := func(id int, detail string) {
		addSpellEffect(lobby, id, charID, spell.Name, b.Kind, detail, b.Rounds, "", 0)
	}
	var names []string
	for _, id := range ids {
		names = append(names, name(id))
	}
	who := joinNames(names)
	var out string
	switch spellKey {
	case "aid", "heroes-feast":
		var parts []string
		for _, id := range ids {
			n := 5 * maxInt(1, slotLevel-1)
			if spellKey == "heroes-feast" {
				n = game.RollDamage("2d10", false)
				for _, c := range []string{"frightened", "poisoned"} {
					removeCondition(id, c)
				}
			}
			parts = append(parts, raiseMaxHP(lobby, id, charID, spell.Name, b.Kind, n, b.Rounds))
		}
		out = strings.Join(parts, "; ") + "."
		if spellKey == "heroes-feast" {
			out += " For 24 hours they also have Resistance to Poison damage and Immunity to the Frightened and Poisoned conditions."
		}
	case "barkskin":
		add(ids[0], "")
		refreshStoredAC(ids[0])
		out = fmt.Sprintf("%s has an Armor Class of at least 17 for 1 hour (AC %d now).", who, storedAC(ids[0]))
	case "death-ward":
		add(ids[0], "")
		out = fmt.Sprintf("The first time %s would drop to 0 Hit Points in the next 8 hours, they drop to 1 instead.", who)
	case "foresight":
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND caster_id = $2 AND kind = 'foresight'`, lobby, charID) // cast again: the old one ends
		add(ids[0], "")
		out = fmt.Sprintf("For 8 hours %s has Advantage on D20 Tests, and other creatures have Disadvantage on attack rolls against them.", who)
	case "mirror-image":
		add(charID, "3")
		out = "Three illusory duplicates appear: when a creature hits you with an attack roll, a d6 for each duplicate; on any 3 or higher, a duplicate is hit and destroyed instead of you."
	case "blur":
		add(charID, "")
		out = "Attack rolls against you have Disadvantage while you keep Concentration (not from attackers with Blindsight or Truesight)."
	case "stoneskin":
		add(ids[0], "")
		out = fmt.Sprintf("%s has Resistance to Bludgeoning, Piercing and Slashing damage while you keep Concentration.", who)
	case "fire-shield":
		kind := "warm"
		if strings.Contains(dl, "chill") || strings.Contains(dl, "cold shield") {
			kind = "chill"
		}
		add(charID, kind)
		res, back := "Cold", "Fire"
		if kind == "chill" {
			res, back = "Fire", "Cold"
		}
		out = fmt.Sprintf("A %s shield for 10 minutes: Resistance to %s damage, and a creature within 5 feet that hits you with a melee attack takes 2d8 %s damage.", kind, res, back)
	case "holy-aura":
		add(charID, strconv.Itoa(saveDC))
		out = fmt.Sprintf("Your allies within 30 feet have Advantage on saving throws and attacks against them have Disadvantage while you keep Concentration; a Fiend or Undead that hits one in melee makes a DC %d Constitution save or is Blinded until the end of its next turn.", saveDC)
	case "mind-blank":
		add(ids[0], "")
		removeCondition(ids[0], "charmed")
		out = fmt.Sprintf("For 24 hours %s has Immunity to Psychic damage and the Charmed condition.", who)
	case "freedom-of-movement":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("For 1 hour spells and magical effects can't Paralyze, Restrain or slow %s, Difficult Terrain costs no extra movement, and they have a Swim Speed equal to their Speed.", who)
	case "protection-from-energy":
		t := ""
		for _, x := range []string{"acid", "cold", "fire", "lightning", "thunder"} {
			if strings.Contains(dl, x) {
				t = x
				break
			}
		}
		if t == "" {
			return " Protection from Energy: name the damage type in the description (acid, cold, fire, lightning or thunder); no type was chosen, so the spell has no effect."
		}
		add(ids[0], t)
		out = fmt.Sprintf("%s has Resistance to %s damage while you keep Concentration.", who, titleCase(t))
	case "protection-from-evil-and-good":
		add(ids[0], "")
		out = fmt.Sprintf("Aberrations, Celestials, Elementals, Fey, Fiends and Undead have Disadvantage on attack rolls against %s and can't Charm or Frighten them while you keep Concentration.", who)
	case "protection-from-poison":
		add(ids[0], "")
		ended := ""
		if removeCondition(ids[0], "poisoned") {
			ended = " The Poisoned condition ends."
		}
		out = fmt.Sprintf("For 1 hour %s has Resistance to Poison damage and Advantage on saves against being Poisoned.%s", who, ended)
	case "warding-bond":
		id := ids[0]
		if id == charID {
			return " Warding Bond: touch another creature (target_id); you can't bond with yourself, so the spell has no effect."
		}
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND kind = 'warding_bond' AND (target_id IN ($2, $3) OR caster_id IN ($2, $3))`, lobby, id, charID)
		add(id, "")
		refreshStoredAC(id)
		out = fmt.Sprintf("For 1 hour, while within 60 feet of you, %s has +1 to AC and saving throws and Resistance to all damage; each time they take damage, you take the same amount.", who)
	case "beacon-of-hope":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("%s: Advantage on Wisdom saves and Death Saving Throws, and healing restores its maximum, while you keep Concentration.", who)
	case "longstrider":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("For 1 hour %s has +10 feet of Speed.", who)
	case "enhance-ability":
		ab := abilityFromWords(dl)
		if ab == "" {
			return " Enhance Ability: name the ability in the description (Strength, Dexterity, Intelligence, Wisdom or Charisma); none was chosen, so the spell has no effect."
		}
		for _, id := range ids {
			add(id, ab)
		}
		out = fmt.Sprintf("%s has Advantage on %s checks while you keep Concentration.", who, map[string]string{"str": "Strength", "dex": "Dexterity", "int": "Intelligence", "wis": "Wisdom", "cha": "Charisma"}[ab])
	case "greater-invisibility", "invisibility":
		for _, id := range ids {
			add(id, "")
			addMonsterCondition(lobby, id, "invisible", spell.Name, b.Rounds, "", 0)
			db.Exec(`UPDATE condition_records SET created_by = $1, notes = 'concentration' WHERE lobby_id = $2 AND target_id = $3 AND condition = 'invisible' AND source = $4`,
				charID, lobby, id, spell.Name) // it lasts while the caster keeps Concentration
			if id > 0 {
				syncListFromRecords(id)
			}
		}
		out = fmt.Sprintf("%s has the Invisible condition while you keep Concentration.", who)
		if spellKey == "invisibility" {
			out = fmt.Sprintf("%s has the Invisible condition while you keep Concentration, until they make an attack roll, deal damage or cast a spell.", who)
		}
	case "magic-weapon":
		id := ids[0]
		var weapon string
		db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, id).Scan(&weapon)
		if strings.TrimSpace(weapon) == "" {
			return fmt.Sprintf(" Magic Weapon: %s holds no weapon (equip one first), so the spell has no effect.", who)
		}
		if itemPlus(weapon) > 0 || namedWeaponFor(weapon) != nil || strings.HasPrefix(strings.ToLower(weapon), "rod of lordly might") {
			return fmt.Sprintf(" Magic Weapon: %s is already a magic weapon, so the spell has no effect.", weapon)
		}
		bonus := 1
		if slotLevel >= 6 {
			bonus = 3
		} else if slotLevel >= 3 {
			bonus = 2
		}
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND caster_id = $2 AND kind = 'magic_weapon'`, lobby, charID) // cast again: the old one ends
		add(id, fmt.Sprintf("%d|%s", bonus, weapon))
		out = fmt.Sprintf("%s's %s becomes a magic weapon with +%d to attack and damage rolls for 1 hour.", who, weapon, bonus)
	case "spider-climb":
		for _, id := range ids {
			add(id, "")
		}
		out = fmt.Sprintf("%s can move on walls and ceilings and has a Climb Speed equal to their Speed while you keep Concentration.", who)
	case "see-invisibility":
		add(charID, "")
		out = "For 1 hour you see Invisible creatures as if they were visible (attacks against them lose the Disadvantage, theirs against you the Advantage)."
	case "true-seeing":
		add(ids[0], "")
		out = fmt.Sprintf("For 1 hour %s has Truesight 120 feet.", who)
	case "sanctuary":
		add(ids[0], strconv.Itoa(saveDC))
		out = fmt.Sprintf("For 1 minute a creature that attacks %s makes a DC %d Wisdom save or loses the attack; the ward ends if %s attacks, casts a spell or deals damage. Areas of effect still reach them.", who, saveDC, who)
	case "divine-favor":
		add(charID, "")
		out = "For 1 minute your weapon hits deal 1d4 Radiant damage more."
	case "pass-without-trace":
		add(charID, "")
		out = "You and your allies within 30 feet have +10 to Dexterity (Stealth) checks and leave no tracks while you keep Concentration."
	}
	return " " + strings.TrimSpace(spell.Name+": "+out) + note
}

// raiseMaxHP: Aid and Heroes' Feast raise the Hit Point maximum and current Hit Points. The same spell's effects don't add together:
// the larger stays.
func raiseMaxHP(lobby, id, caster int, spellName, kind string, n, rounds int) string {
	name := creatureName(lobby, id)
	if id <= 0 {
		return name + ": the GM raises its Hit Points"
	}
	old := 0
	for _, e := range spellEffectsOn(lobby, id) {
		if e.Kind == kind {
			old = maxInt(old, atoiSafe(e.Detail))
		}
	}
	if n <= old {
		return fmt.Sprintf("%s keeps the +%d Hit Points they already have from %s (the same spell's effects don't add together)", name, old, spellName)
	}
	db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = $3`, lobby, id, kind)
	addSpellEffect(lobby, id, caster, spellName, kind, strconv.Itoa(n), rounds, "", 0)
	up := n - old
	var before, hp, mx int
	var dead bool
	db.QueryRow(`SELECT hp, COALESCE(is_dead,false) FROM characters WHERE id = $1`, id).Scan(&before, &dead)
	if dead {
		db.Exec(`UPDATE characters SET max_hp = max_hp + $1 WHERE id = $2`, up, id)
		return fmt.Sprintf("%s's Hit Point maximum rises by %d", name, up)
	}
	db.QueryRow(`UPDATE characters SET max_hp = max_hp + $1, hp = GREATEST(hp, 0) + $1 WHERE id = $2 RETURNING hp, max_hp`, up, id).Scan(&hp, &mx)
	if before <= 0 && hp > 0 {
		db.Exec(`UPDATE characters SET death_save_successes = 0, death_save_failures = 0, is_stable = false WHERE id = $1`, id)
		removeCondition(id, "unconscious")
	}
	return fmt.Sprintf("%s's Hit Point maximum and current Hit Points rise by %d (%d/%d)", name, up, hp, mx)
}

// buffEnds undoes what an ending effect changed (endItemEffect calls it): the raised Hit Point maximum; the AC (Barkskin, Warding
// Bond).
func buffEnds(kind string, targetID int, detail string) {
	if n := moreSpellEnds(kind, targetID, detail); n != "" && targetID > 0 {
		logAction(lobbyOfChar(targetID), targetID, 0, "spell_effect", n, "") // Polymorph, Fly, Enlarge/Reduce ... end (spells_more_2024.go)
	}
	if targetID <= 0 {
		return
	}
	switch kind {
	case "aid", "heroes_feast":
		if n := atoiSafe(detail); n > 0 {
			db.Exec(`UPDATE characters SET max_hp = GREATEST(1, max_hp - $1), hp = LEAST(hp, GREATEST(1, max_hp - $1)) WHERE id = $2`, n, targetID)
		}
	case "barkskin", "warding_bond":
		refreshStoredAC(targetID)
	}
}

// lesserRestoration ends one condition: Blinded, Deafened, Paralyzed or Poisoned (the one named, else the first the target has).
func lesserRestoration(charID, lobby int, description string) string {
	id := charID
	if t := spellCharacterTargets(charID); len(t) > 0 {
		id = t[0]
	} else if t := parseTargetFromDescription(description, charID); t > 0 {
		id = t
	}
	dl := strings.ToLower(description)
	have := strings.ToLower(strings.Join(getCharConditions(id), " "))
	order := []string{"paralyzed", "poisoned", "blinded", "deafened"}
	pick := ""
	for _, c := range order {
		if strings.Contains(dl, c) && strings.Contains(have, c) {
			pick = c
		}
	}
	if pick == "" {
		for _, c := range order {
			if strings.Contains(have, c) {
				pick = c
				break
			}
		}
	}
	name := creatureName(lobby, id)
	if pick == "" {
		return fmt.Sprintf(" Lesser Restoration: %s has none of Blinded, Deafened, Paralyzed or Poisoned, so nothing ends.", name)
	}
	removeCondition(id, pick)
	db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = $3`, lobby, id, pick)
	syncListFromRecords(id)
	return fmt.Sprintf(" Lesser Restoration: the %s condition on %s ends.", titleCase(pick), name)
}

// ---- reads for the hooks

// buffACFloor: Barkskin keeps the AC at 17 or more.
func buffACFloor(charID int) int {
	if _, ok := charBuff(charID, "barkskin"); ok {
		return 17
	}
	return 0
}

// bondInRange: the Warding Bond holds while the target is within 60 feet of its caster (on the battle map); farther, it ends.
func bondInRange(lobbyID int, e activeSpellEffect) bool {
	if d, known := charDistanceFeet(lobbyID, e.CasterID, e.TargetID); known && d > 60 {
		db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
		return false
	}
	return true
}

// buffACBonus: Warding Bond +1.
func buffACBonus(lobbyID, id int) int {
	if e, ok := buffEffect(lobbyID, id, "warding_bond"); ok && id > 0 && bondInRange(lobbyID, e) {
		return 1
	}
	return 0
}

// buffSaveBonus: Warding Bond +1 to saving throws.
func buffSaveBonus(charID int) int {
	if charID <= 0 {
		return 0
	}
	return buffACBonus(lobbyOfChar(charID), charID)
}

var poisonSaveCtx sync.Map // character id -> true while it saves against an effect that would Poison it (Protection from Poison)

// holyAuraOn: a Holy Aura covers the character (its caster, or an ally within 30 feet on the map); dc is the caster's save DC.
func holyAuraOn(lobbyID, targetID int) (bool, int) {
	if lobbyID == 0 || targetID <= 0 {
		return false, 0
	}
	rows, err := db.Query(`SELECT DISTINCT target_id FROM spell_effects WHERE lobby_id = $1 AND kind = 'holy_aura'`, lobbyID)
	if err != nil {
		return false, 0
	}
	var casters []int
	for rows.Next() {
		var c int
		if rows.Scan(&c) == nil {
			casters = append(casters, c)
		}
	}
	rows.Close()
	for _, c := range casters {
		e, ok := buffEffect(lobbyID, c, "holy_aura")
		if !ok {
			continue
		}
		if c != targetID {
			if d, known := charDistanceFeet(lobbyID, c, targetID); known && d > 30 {
				continue
			}
		}
		return true, atoiSafe(e.Detail)
	}
	return false, 0
}

// passWithoutTraceOn: within 30 feet of a caster of Pass without Trace.
func passWithoutTraceOn(lobbyID, charID int) bool {
	if lobbyID == 0 || charID <= 0 {
		return false
	}
	rows, err := db.Query(`SELECT DISTINCT target_id FROM spell_effects WHERE lobby_id = $1 AND kind = 'pass_without_trace'`, lobbyID)
	if err != nil {
		return false
	}
	var casters []int
	for rows.Next() {
		var c int
		if rows.Scan(&c) == nil {
			casters = append(casters, c)
		}
	}
	rows.Close()
	for _, c := range casters {
		if _, ok := buffEffect(lobbyID, c, "pass_without_trace"); !ok {
			continue
		}
		if c == charID {
			return true
		}
		if d, known := charDistanceFeet(lobbyID, c, charID); !known || d <= 30 {
			return true
		}
	}
	return false
}

// buffStealthBonus: Pass without Trace gives +10 to Dexterity (Stealth) checks.
func buffStealthBonus(charID int) (int, string) {
	if passWithoutTraceOn(lobbyOfChar(charID), charID) {
		return 10, " [Pass without Trace +10]"
	}
	return 0, ""
}

// buffSaveAdvantage: Foresight; a Holy Aura; Beacon of Hope on a Wisdom save; Protection from Poison against being Poisoned.
func buffSaveAdvantage(lobbyID, id int, ability string) (bool, string) {
	if ok, why := moreSaveAdvantage(lobbyID, id, ability); ok {
		return true, why // Gaseous Form, Enlarge (spells_more_2024.go)
	}
	if id <= 0 {
		return false, ""
	}
	if shortAbility(ability) == "dex" && dodgeActive(id) {
		return true, "Dodge" // the Dodge action (dodge_2024.go): Dexterity saving throws with Advantage
	}
	if _, ok := buffEffect(lobbyID, id, "foresight"); ok {
		return true, "Foresight"
	}
	if ok, _ := holyAuraOn(lobbyID, id); ok {
		return true, "Holy Aura"
	}
	if shortAbility(ability) == "wis" {
		if _, ok := buffEffect(lobbyID, id, "beacon_of_hope"); ok {
			return true, "Beacon of Hope"
		}
	}
	if _, ctx := poisonSaveCtx.Load(id); ctx {
		if _, ok := buffEffect(lobbyID, id, "protection_from_poison"); ok {
			return true, "Protection from Poison"
		}
		if dwarvenResilience(id) {
			return true, "Dwarven Resilience" // F81
		}
		if periaptOfHealthPoison(id) {
			return true, "Periapt of Health" // F154
		}
	}
	return false, ""
}

// buffAttackAdvantage: Foresight on the attacker's own attack rolls.
func buffAttackAdvantage(charID int) bool {
	_, ok := charBuff(charID, "foresight")
	return ok
}

// buffCheckAdvantage: Foresight, or Enhance Ability with the check's ability.
func buffCheckAdvantage(charID int, ability string) (bool, string) {
	if charID <= 0 {
		return false, ""
	}
	if ok, why := moreCheckAdvantage(charID, ability); ok {
		return true, why // Enlarge (spells_more_2024.go)
	}
	lobby := lobbyOfChar(charID)
	if antimagicCovers(lobby, charID) {
		return false, ""
	}
	ab := shortAbility(ability)
	for _, e := range spellEffectsOn(lobby, charID) {
		switch {
		case e.Kind == "foresight":
			return true, "Foresight"
		case e.Kind == "enhance_ability" && ab != "" && e.Detail == ab:
			return true, "Enhance Ability"
		}
	}
	return false, ""
}

// skillAbility is the ability a skill uses.
func skillAbility(skill string) string {
	switch strings.ReplaceAll(strings.ToLower(skill), "_", " ") {
	case "athletics":
		return "str"
	case "acrobatics", "sleight of hand", "stealth":
		return "dex"
	case "arcana", "history", "investigation", "nature", "religion":
		return "int"
	case "animal handling", "insight", "medicine", "perception", "survival":
		return "wis"
	case "deception", "intimidation", "performance", "persuasion":
		return "cha"
	}
	return shortAbility(skill)
}

// buffCheckRoll rolls a d20 for an ability check, with Advantage from Foresight or Enhance Ability; note says why.
func buffCheckRoll(charID int, ability string, roll int) (int, string) {
	adv, why := buffCheckAdvantage(charID, ability)
	dis, dwhy := moreCheckDisadvantage(charID, ability) // Reduce (spells_more_2024.go)
	switch {
	case adv && dis:
		return roll, " [Advantage (" + why + ") and Disadvantage (" + dwhy + ") cancel]"
	case adv:
		if b := game.RollDie(20); b > roll {
			roll = b
		}
		return roll, " [Advantage: " + why + "]"
	case dis:
		if b := game.RollDie(20); b < roll {
			roll = b
		}
		return roll, " [Disadvantage: " + dwhy + "]"
	}
	return roll, ""
}

// buffDeathSaveAdvantage: Beacon of Hope and Foresight give Advantage on Death Saving Throws.
func buffDeathSaveAdvantage(charID int) (bool, string) {
	if _, ok := charBuff(charID, "beacon_of_hope"); ok {
		return true, "Beacon of Hope"
	}
	if _, ok := charBuff(charID, "foresight"); ok {
		return true, "Foresight"
	}
	return false, ""
}

// buffSpeedBonus: Longstrider +10 feet.
func buffSpeedBonus(charID int) int {
	if _, ok := charBuff(charID, "longstrider"); ok {
		return 10
	}
	return 0
}

// buffIgnoresDifficult: Freedom of Movement.
func buffIgnoresDifficult(charID int) bool {
	_, ok := charBuff(charID, "freedom_of_movement")
	return ok || ringOfFreeAction(charID) // the Ring of Free Action (F104)
}

// buffSeesInvisible: See Invisibility or True Seeing.
func buffSeesInvisible(charID int) bool {
	if _, ok := charBuff(charID, "see_invisibility"); ok {
		return true
	}
	_, ok := charBuff(charID, "true_seeing")
	return ok
}

// buffTruesight: True Seeing gives Truesight 120 feet.
func buffTruesight(charID int) int {
	if _, ok := charBuff(charID, "true_seeing"); ok {
		return 120
	}
	return 0
}

// buffWeaponPlus: Magic Weapon's bonus for the weapon it was cast on.
func buffWeaponPlus(charID int, weapon string) int {
	e, ok := charBuff(charID, "magic_weapon")
	if !ok {
		return 0
	}
	parts := strings.SplitN(e.Detail, "|", 2)
	if len(parts) == 2 && weapon != "" && !strings.EqualFold(strings.TrimSpace(parts[1]), strings.TrimSpace(weapon)) &&
		!strings.Contains(strings.ToLower(parts[1]), strings.ToLower(weapon)) {
		return 0
	}
	return atoiSafe(parts[0])
}

// divineFavorHit: Divine Favor adds 1d4 Radiant damage to a weapon hit.
func divineFavorHit(charID int, monsterKey, weapon string, crit bool) (int, string) {
	if strings.Contains(strings.ToLower(weapon), "unarmed") {
		return 0, ""
	}
	if _, ok := charBuff(charID, "divine_favor"); !ok {
		return 0, ""
	}
	d, how := adjustMonsterDamage(monsterKey, "radiant", game.RollDamage("1d4", crit))
	return d, fmt.Sprintf(" Divine Favor: +%d Radiant%s.", d, map[string]string{"resistant": " (it resists)", "immune": " (it is immune)", "vulnerable": " (it is vulnerable)"}[how])
}

// buffDamageGuard: Mind Blank (Psychic immunity); Stoneskin, Fire Shield, Protection from Energy, Protection from Poison, Heroes'
// Feast, Warding Bond (resistances).
func buffDamageGuard(charID int, damageType string) (immune, resist bool, why string) {
	t := strings.ToLower(strings.TrimSpace(damageType))
	if charID <= 0 || t == "" {
		return false, false, ""
	}
	if r, why := moreDamageResist(charID, t); r {
		return false, true, why // Gaseous Form (spells_more_2024.go)
	}
	lobby := lobbyOfChar(charID)
	if antimagicCovers(lobby, charID) {
		return false, false, ""
	}
	effs := spellEffectsOn(lobby, charID)
	for _, e := range effs {
		if e.Kind == "mind_blank" && t == "psychic" {
			return true, false, "Mind Blank"
		}
	}
	for _, e := range effs {
		switch e.Kind {
		case "stoneskin":
			if t == "bludgeoning" || t == "piercing" || t == "slashing" {
				return false, true, "Stoneskin"
			}
		case "fire_shield":
			if (e.Detail == "chill" && t == "fire") || (e.Detail != "chill" && t == "cold") {
				return false, true, "Fire Shield"
			}
		case "protection_from_energy":
			if t == e.Detail {
				return false, true, "Protection from Energy"
			}
		case "protection_from_poison", "heroes_feast":
			if t == "poison" {
				return false, true, buffLabels[e.Kind][0]
			}
		case "warding_bond":
			if bondInRange(lobby, e) {
				return false, true, "Warding Bond"
			}
		}
	}
	return false, false, ""
}

var evilGoodTypes = map[string]bool{"aberration": true, "celestial": true, "elemental": true, "fey": true, "fiend": true, "undead": true}

// buffConditionImmune: Mind Blank (Charmed); Heroes' Feast (Frightened, Poisoned); Freedom of Movement (Paralyzed, Restrained from a
// spell or a magical effect); Protection from Evil and Good (Charmed, Frightened from those creature types).
func buffConditionImmune(lobbyID, charID int, cond string, sourceID int, magical bool) (bool, string) {
	if ok, why := moreConditionImmune(lobbyID, charID, cond, sourceID); ok {
		return true, why // Gaseous Form, Magic Circle (spells_more_2024.go)
	}
	if c := strings.ToLower(conditionBase(cond)); magical && (c == "paralyzed" || c == "restrained") && ringOfFreeAction(charID) {
		return true, "Ring of Free Action" // F104: magic can't Paralyze or Restrain the wearer
	}
	if charID <= 0 || antimagicCovers(lobbyID, charID) {
		return false, ""
	}
	c := strings.ToLower(conditionBase(cond))
	for _, e := range spellEffectsOn(lobbyID, charID) {
		switch {
		case e.Kind == "mind_blank" && c == "charmed":
			return true, "Mind Blank"
		case e.Kind == "heroes_feast" && (c == "frightened" || c == "poisoned"):
			return true, "Heroes' Feast"
		case e.Kind == "freedom_of_movement" && magical && (c == "paralyzed" || c == "restrained"):
			return true, "Freedom of Movement"
		case e.Kind == "protection_from_evil_and_good" && (c == "charmed" || c == "frightened") && sourceID < 0 &&
			evilGoodTypes[strings.ToLower(monsterTypeOf(lobbyID, sourceID))]:
			return true, "Protection from Evil and Good"
		}
	}
	return false, ""
}

// monsterSeesThrough: a monster with Blindsight or Truesight isn't fooled by Blur or Mirror Image.
func monsterSeesThrough(lobbyID, monsterID int) bool {
	e, _, ok := monsterCombatant(lobbyID, monsterID)
	if !ok {
		return false
	}
	var senses string
	db.QueryRow(`SELECT COALESCE(senses,'') FROM monsters WHERE slug = $1`, fmt.Sprint(e["monster_key"])).Scan(&senses)
	s := strings.ToLower(senses)
	return strings.Contains(s, "blindsight") || strings.Contains(s, "truesight")
}

// buffAttackDisadvantage: an attack roll against the character has Disadvantage because of what the character has done or has on
// it (the Dodge action, Foresight, Blur, Holy Aura, Protection from Evil and Good ...). Every place that rolls an attack against a
// character asks here: a stat block's attack, a monster's spell attack, an Opportunity Attack (Nov 14: Dodge was read in one of
// them only). monsterID is 0 when the attacker is not a monster in the turn order.
func buffAttackDisadvantage(lobbyID, monsterID, targetID int) (bool, string) {
	if targetID <= 0 {
		return false, ""
	}
	var why []string
	if ok, w := moreAttackDisadvantage(lobbyID, monsterID, targetID); ok {
		why = append(why, w) // Magic Circle (spells_more_2024.go)
	}
	if dodgeActive(targetID) {
		why = append(why, "the target is Dodging") // the Dodge action (dodge_2024.go): every attack roll against the character
	}
	if _, ok := buffEffect(lobbyID, targetID, "foresight"); ok {
		why = append(why, "Foresight")
	}
	if robeDazzle(lobbyID, targetID) {
		why = append(why, "Robe of Scintillating Colors") // F176 (items_c6_2024.go)
	}
	if cloakOfDisplacement(lobbyID, targetID) {
		why = append(why, "Cloak of Displacement") // F113 (rogue_fixes_2024.go)
	}
	if _, ok := buffEffect(lobbyID, targetID, "blur"); ok && (monsterID == 0 || !monsterSeesThrough(lobbyID, monsterID)) {
		why = append(why, "Blur")
	}
	if ok, _ := holyAuraOn(lobbyID, targetID); ok {
		why = append(why, "Holy Aura")
	}
	if _, ok := buffEffect(lobbyID, targetID, "protection_from_evil_and_good"); ok && monsterID < 0 &&
		evilGoodTypes[strings.ToLower(monsterTypeOf(lobbyID, monsterID))] {
		why = append(why, "Protection from Evil and Good")
	}
	return len(why) > 0, strings.Join(why, ", ")
}

// sanctuaryBlocks: an attack on a creature warded by Sanctuary needs a Wisdom save; on a failure the attack is lost.
func sanctuaryBlocks(lobbyID, monsterID int, monsterName string, targetID int) (bool, string) {
	e, ok := buffEffect(lobbyID, targetID, "sanctuary")
	if !ok || monsterID == 0 {
		return false, ""
	}
	dc := atoiSafe(e.Detail)
	saved, total := monsterSave(lobbyID, monsterID, "wis", dc)
	if saved {
		return false, fmt.Sprintf(" [Sanctuary: %s's Wisdom save %d vs DC %d succeeds]", monsterName, total, dc)
	}
	return true, fmt.Sprintf("Sanctuary: %s fails the Wisdom save (%d vs DC %d) and loses the attack (the GM may aim it at another creature)", monsterName, total, dc)
}

// mirrorImageIntercept: a hit on a creature with Mirror Image: a d6 per duplicate; any 3+ and a duplicate takes the hit instead.
func mirrorImageIntercept(lobbyID, monsterID, targetID int, attackerConds []string) (bool, string) {
	e, ok := buffEffect(lobbyID, targetID, "mirror_image")
	if !ok {
		return false, ""
	}
	if monsterID != 0 && monsterSeesThrough(lobbyID, monsterID) {
		return false, ""
	}
	for _, c := range attackerConds {
		if strings.EqualFold(conditionBase(c), "blinded") {
			return false, ""
		}
	}
	n := atoiSafe(e.Detail)
	if n <= 0 {
		db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
		return false, ""
	}
	var rolls []string
	dup := false
	for i := 0; i < n; i++ {
		r := game.RollDie(6)
		rolls = append(rolls, strconv.Itoa(r))
		if r >= 3 {
			dup = true
		}
	}
	if !dup {
		return false, fmt.Sprintf(" [Mirror Image: %s, no 3 or higher]", strings.Join(rolls, ", "))
	}
	n--
	if n == 0 {
		db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
	} else {
		db.Exec(`UPDATE spell_effects SET detail = $1 WHERE id = $2`, strconv.Itoa(n), e.ID)
	}
	left := fmt.Sprintf("%d left", n)
	if n == 0 {
		left = "the spell ends"
	}
	return true, fmt.Sprintf("it strikes an illusory duplicate instead (Mirror Image: %s); the duplicate is destroyed, %s", strings.Join(rolls, ", "), left)
}

// fireShieldBack: a melee hit within 5 feet on a creature with Fire Shield: 2d8 Fire (warm) or Cold (chill) to the attacker.
func fireShieldBack(lobbyID, monsterID int, monsterName string, targetID int) string {
	e, ok := buffEffect(lobbyID, targetID, "fire_shield")
	if !ok || monsterID == 0 {
		return ""
	}
	dtype := "fire"
	if e.Detail == "chill" {
		dtype = "cold"
	}
	text, _ := damageMonster(lobbyID, monsterID, game.RollDamage("2d8", false), dtype)
	if text == "" {
		return ""
	}
	return fmt.Sprintf(" Fire Shield: the flames lash %s: %s", monsterName, text)
}

// holyAuraBlinds: a Fiend or Undead that hits a creature in a Holy Aura with a melee attack makes a Constitution save or is Blinded
// until the end of its next turn.
func holyAuraBlinds(lobbyID, monsterID int, monsterName string, targetID int) string {
	t := strings.ToLower(monsterTypeOf(lobbyID, monsterID))
	if monsterID == 0 || (t != "fiend" && t != "undead") {
		return ""
	}
	ok, dc := holyAuraOn(lobbyID, targetID)
	if !ok {
		return ""
	}
	saved, total := monsterSave(lobbyID, monsterID, "con", dc)
	if saved {
		return fmt.Sprintf(" Holy Aura: %s's Constitution save %d vs DC %d succeeds.", monsterName, total, dc)
	}
	addMonsterCondition(lobbyID, monsterID, "blinded", "Holy Aura", 2, "", 0)
	return fmt.Sprintf(" Holy Aura: %s fails the Constitution save (%d vs DC %d) and is Blinded until the end of its next turn.", monsterName, total, dc)
}

// deathWardHolds: the first drop to 0 Hit Points under Death Ward is a drop to 1, and the spell ends.
func deathWardHolds(charID int) (bool, string) {
	e, ok := charBuff(charID, "death_ward")
	if !ok {
		return false, ""
	}
	db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
	return true, "Death Ward: instead of dropping to 0 Hit Points you drop to 1, and the spell ends."
}

// wardingBondShare: the caster of a Warding Bond takes the same damage as its target (typeless: their own resistances don't apply).
func wardingBondShare(charID, dmg int) string {
	if dmg <= 0 || charID <= 0 {
		return ""
	}
	lobby := lobbyOfChar(charID)
	e, ok := buffEffect(lobby, charID, "warding_bond")
	if !ok || e.CasterID <= 0 || e.CasterID == charID || !bondInRange(lobby, e) {
		return ""
	}
	body, _ := json.Marshal(map[string]interface{}{"damage": dmg, "damage_type": "", "no_bond": true})
	rr := httptest.NewRecorder()
	handleDamage(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)), e.CasterID)
	var out map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &out)
	return fmt.Sprintf(" Warding Bond: %s takes the same %d damage (now %v HP).", getCharacterName(e.CasterID), dmg, out["hp"])
}

// endWardingBondsOf: the bond ends when its caster drops to 0 Hit Points.
func endWardingBondsOf(casterID int) {
	rows, err := db.Query(`DELETE FROM spell_effects WHERE caster_id = $1 AND kind = 'warding_bond' RETURNING target_id`, casterID)
	if err != nil {
		return
	}
	var ts []int
	for rows.Next() {
		var t int
		if rows.Scan(&t) == nil {
			ts = append(ts, t)
		}
	}
	rows.Close()
	for _, t := range ts {
		refreshStoredAC(t)
	}
}

// beaconMaxHealing: Beacon of Hope makes healing restore its maximum.
func beaconMaxHealing(charID int) bool {
	_, ok := charBuff(charID, "beacon_of_hope")
	return ok
}

// endWardsOnOffense: Invisibility and Sanctuary end when their creature attacks, casts a spell or deals damage.
func endWardsOnOffense(charID int) string {
	if charID <= 0 {
		return ""
	}
	lobby := lobbyOfChar(charID)
	var notes []string
	if _, ok := buffEffect(lobby, charID, "sanctuary"); ok {
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'sanctuary'`, lobby, charID)
		notes = append(notes, "Sanctuary")
	}
	res, err := db.Exec(`DELETE FROM condition_records WHERE target_id = $1 AND condition = 'invisible' AND source = 'Invisibility'`, charID)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			db.Exec(`DELETE FROM spell_effects WHERE target_id = $1 AND kind = 'invisibility'`, charID)
			syncListFromRecords(charID)
			notes = append(notes, "Invisibility")
		}
	}
	if len(notes) == 0 {
		return ""
	}
	return " (" + strings.Join(notes, " and ") + " ends.)"
}

// refreshConcentrationConditions drops the conditions whose caster lost Concentration (Invisibility, Greater Invisibility on a
// character) and resyncs the character's list.
func refreshConcentrationConditions(charID int) {
	if charID <= 0 {
		return
	}
	if lobby := lobbyOfChar(charID); lobby != 0 {
		pruneConcentrationConditions(lobby, charID)
	}
}

// buffSaveAdv is buffSaveAdvantage without the reason (spellSaveAdvantage).
func buffSaveAdv(lobbyID, id int, ability string) bool {
	ok, _ := buffSaveAdvantage(lobbyID, id, ability)
	return ok
}

// dropInvisible removes the Invisible condition from an attacker's list (a target that sees Invisible creatures).
func dropInvisible(conds []string) []string {
	out := conds[:0:0]
	for _, c := range conds {
		if !strings.HasPrefix(strings.ToLower(c), "invisible") {
			out = append(out, c)
		}
	}
	return out
}

// pruneConcentrationByCaster ends the conditions a caster's earlier Concentration spell put on creatures (a new Concentration spell
// replaced it): Invisibility, Greater Invisibility, Hold Person ... Characters' lists follow their records.
func pruneConcentrationByCaster(casterID int) {
	if db == nil || casterID <= 0 {
		return
	}
	rows, err := db.Query(`SELECT id, target_id, source FROM condition_records WHERE created_by = $1 AND notes = 'concentration'`, casterID)
	if err != nil {
		return
	}
	type rec struct {
		id, target int
		source     string
	}
	var drop []rec
	for rows.Next() {
		var r rec
		if rows.Scan(&r.id, &r.target, &r.source) == nil && !concentrationHolds(casterID, r.source) {
			drop = append(drop, r)
		}
	}
	rows.Close()
	synced := map[int]bool{}
	for _, r := range drop {
		db.Exec(`DELETE FROM condition_records WHERE id = $1`, r.id)
		if r.target > 0 && !synced[r.target] {
			synced[r.target] = true
			syncListFromRecords(r.target)
		}
	}
}

// dispelSpellEffects: Dispel Magic ends each spell on the creature of the slot's level or lower; a higher one needs the caster's
// ability check (d20 + spellcasting modifier against DC 10 + its level). effect, when given, picks one spell by name. Spell effects
// keep the spell's own level, not the slot it was cast with. F76: Dispel Magic only ended the target's own Concentration.
func dispelSpellEffects(lobbyID, targetID, slot, mod int, effect string) (lines []string, anyEnded bool) {
	seen := map[string]bool{}
	for _, e := range spellEffectsOn(lobbyID, targetID) {
		if effect != "" && !strings.EqualFold(e.Spell, effect) {
			continue
		}
		key := fmt.Sprintf("%s#%d", e.Spell, e.CasterID)
		if seen[key] {
			continue
		}
		seen[key] = true
		sp, ok := srdSpellsMemory[spellSlugFromName(e.Spell)]
		if !ok {
			continue // an item's, a species trait's or a monster's effect, not a spell
		}
		ends, note := sp.Level <= slot, ""
		if !ends {
			r := game.RollDie(20)
			ends = r+mod >= 10+sp.Level
			note = fmt.Sprintf(" (ability check %d vs DC %d)", r+mod, 10+sp.Level)
		}
		if !ends {
			lines = append(lines, fmt.Sprintf("%s (level %d) holds%s", e.Spell, sp.Level, note))
			continue
		}
		rows, err := db.Query(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND spell = $3 AND caster_id = $4 RETURNING kind, detail`,
			lobbyID, targetID, e.Spell, e.CasterID)
		if err == nil {
			type ended struct{ kind, detail string }
			var list []ended
			for rows.Next() {
				var x ended
				if rows.Scan(&x.kind, &x.detail) == nil {
					list = append(list, x)
				}
			}
			rows.Close()
			for _, x := range list {
				endItemEffect(x.kind, targetID, x.detail) // Aid's maximum, Barkskin's and Mage Armor's AC go back
			}
		}
		db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND source = $3`, lobbyID, targetID, e.Spell)
		if targetID > 0 {
			syncListFromRecords(targetID)
			refreshStoredAC(targetID)
		}
		lines = append(lines, fmt.Sprintf("%s (level %d) ends%s", e.Spell, sp.Level, note))
		anyEnded = true
	}
	return lines, anyEnded
}
