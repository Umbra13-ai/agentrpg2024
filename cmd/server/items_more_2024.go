package main

// Fixes from the magic item step of the coverage plan (level20-battle-tests/s47_items.py, Nov 9 2026; FINDINGS F204-F206):
//	F204 worn items: Swim and Climb Speeds (Ring of Swimming 40, Cloak of the Manta Ray 60, Gloves of Swimming and Climbing and Cloak
//	     of Arachnida equal to the Speed), Darkvision and Truesight (Goggles of Night 60 or +60, Robe of Eyes 120 and 120, Belt of
//	     Dwarvenkind 60), Resistances (Boots of the Winterlands Cold, Cloak of Arachnida Poison, Belt of Dwarvenkind Poison, Dragon Scale
//	     Mail by its dragon), Ring of Warmth (Cold damage 2d8 less), Advantage on checks (Cloak of Elvenkind Stealth; Eyes of the Eagle,
//	     Robe of Eyes, Ioun Stone of Awareness Perception; Eyes of Minute Seeing Investigation), the Ioun Stone of Awareness on
//	     Initiative, saves against Poisoned (Belt of Dwarvenkind, Necklace of Adaptation), Constitution +2 to 20 (Belt of Dwarvenkind,
//	     Ioun Stone of Fortitude)
//	F205 weapons: the Sun Blade deals Radiant damage (+1d8 against Undead); the Staff of Striking spends "N charges" for Nd6 Force
//	F206 use_item: Gem of Brightness, Iron Bands, Rope of Entanglement, Rod of Rulership, Animated Shield, Staff of the Python,
//	     Figurine of Wondrous Power, Bag of Tricks, Efreeti Bottle, Pipes of the Sewers, the Bowl, Brazier, Censer and Stone of the
//	     elementals, Broom and Carpet of Flying, Lantern of Revealing, Ring of X-ray Vision, Immovable Rod. Other items answer with
//	     their SRD text instead of "No game effect resolved"; a charged item with no spell spends a charge.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

func wornItem(charID int, prefix string) bool {
	_, ok := itemActive(charID, prefix, "worn")
	return ok
}

func baseSpeed(charID int) int {
	var sp string
	db.QueryRow(`SELECT COALESCE(species,'') FROM characters WHERE id = $1`, charID).Scan(&sp)
	return getMovementSpeed(sp)
}

// itemSpeeds: the Swim and Climb Speeds worn items give.
func itemSpeeds(charID int) map[string]int {
	out := map[string]int{}
	set := func(k string, v int) {
		if v > out[k] {
			out[k] = v
		}
	}
	sp := baseSpeed(charID)
	if wornItem(charID, "ring of swimming") {
		set("swim", 40)
	}
	if wornItem(charID, "cloak of the manta ray") {
		set("swim", 60)
	}
	if wornItem(charID, "gloves of swimming and climbing") {
		set("swim", sp)
		set("climb", sp)
	}
	if wornItem(charID, "cloak of arachnida") {
		set("climb", sp)
	}
	return out
}

// itemVision: Darkvision and Truesight from worn items, given the character's own.
func itemVision(charID, dark, truesight int) (int, int) {
	if wornItem(charID, "goggles of night") {
		if dark > 0 {
			dark += 60
		} else {
			dark = 60
		}
	}
	if wornItem(charID, "robe of eyes") {
		dark, truesight = max(dark, 120), max(truesight, 120)
	}
	if wornItem(charID, "belt of dwarvenkind") && !isDwarf(charID) {
		dark = max(dark, 60)
	}
	return dark, truesight
}

var dragonScaleType = map[string]string{"black": "acid", "blue": "lightning", "brass": "fire", "bronze": "lightning", "copper": "acid", "gold": "fire",
	"green": "poison", "red": "fire", "silver": "cold", "white": "cold"}

// moreItemResist: Resistances from worn items (F204).
func moreItemResist(charID int, t string) (bool, string) {
	switch t {
	case "cold":
		if wornItem(charID, "boots of the winterlands") {
			return true, "Boots of the Winterlands"
		}
	case "poison":
		if wornItem(charID, "cloak of arachnida") {
			return true, "Cloak of Arachnida"
		}
		if wornItem(charID, "belt of dwarvenkind") && !isDwarf(charID) {
			return true, "Belt of Dwarvenkind"
		}
	}
	armor := strings.ToLower(equippedArmorName(charID))
	if strings.HasPrefix(armor, "dragon scale mail") {
		if _, ok := attunedTo(charID, "dragon scale mail"); ok {
			for color, dt := range dragonScaleType {
				if strings.Contains(armor, color) && dt == t {
					return true, equippedArmorName(charID)
				}
			}
		}
	}
	return false, ""
}

// ringOfWarmth: Cold damage is 2d8 less while the ring is worn.
func ringOfWarmth(charID int, dtype string) (int, string) {
	if !strings.EqualFold(dtype, "cold") || !wornItem(charID, "ring of warmth") {
		return 0, ""
	}
	cut := game.RollDamage("2d8", false)
	return cut, fmt.Sprintf("Ring of Warmth: %d less Cold damage", cut)
}

// itemSkillAdvantage: Advantage on a skill check from a worn item.
func itemSkillAdvantage(charID int, skill string) (bool, string) {
	s := strings.ToLower(strings.ReplaceAll(skill, "_", " "))
	switch s {
	case "stealth":
		if wornItem(charID, "cloak of elvenkind") {
			return true, "Cloak of Elvenkind"
		}
	case "perception":
		for _, n := range []string{"eyes of the eagle", "robe of eyes"} {
			if wornItem(charID, n) {
				return true, titleCase(n)
			}
		}
		if iounStone(charID, "awareness") {
			return true, "Ioun Stone of Awareness"
		}
		if sentinelShield(charID) {
			return true, "Sentinel Shield"
		}
	case "investigation":
		if wornItem(charID, "eyes of minute seeing") {
			return true, "Eyes of Minute Seeing (within 1 foot)"
		}
	}
	return false, ""
}

// sentinelShield: holding the Sentinel Shield: Advantage on Initiative rolls and Wisdom (Perception) checks.
func sentinelShield(charID int) bool {
	_, ok := itemActive(charID, "sentinel shield", "held")
	return ok
}

func iounStone(charID int, kind string) bool {
	for _, it := range inventoryOf(charID) {
		n := strings.ToLower(itemStr(it, "name"))
		if strings.HasPrefix(n, "ioun stone") && strings.Contains(n, kind) && itemStr(it, "location") == "worn" {
			if _, ok := attunedTo(charID, itemStr(it, "name")); ok {
				return true
			}
		}
	}
	return false
}

// itemPoisonSaveAdvantage: the Belt of Dwarvenkind (not a dwarf) and the Necklace of Adaptation: Advantage on saves against Poisoned.
func itemPoisonSaveAdvantage(charID int, description string) (bool, string) {
	if !strings.Contains(strings.ToLower(description), "poison") {
		return false, ""
	}
	if wornItem(charID, "necklace of adaptation") {
		return true, "Necklace of Adaptation"
	}
	if wornItem(charID, "belt of dwarvenkind") && !isDwarf(charID) {
		return true, "Belt of Dwarvenkind"
	}
	return false, ""
}

// sunBladeHeld: the attack is with an attuned Sun Blade (its blade deals Radiant damage).
func sunBladeHeld(charID int) bool {
	h := handsOf(charID)
	if !strings.HasPrefix(strings.ToLower(h.Main), "sun blade") {
		return false
	}
	_, ok := attunedTo(charID, "sun blade")
	return ok
}

func sunBladeHit(monsterKey string, crit bool) (int, string) {
	if monsterKind(monsterKey) != "undead" {
		return 0, ""
	}
	d := game.RollDamage("1d8", crit)
	return d, fmt.Sprintf(" Sun Blade: +%d Radiant against an Undead.", d)
}

var chargesWordRe = regexp.MustCompile(`(?i)\b([123])\s+charges?\b`)

// staffOfStrikingHit: "N charges" (up to 3) in the attack: N d6 Force damage more.
func staffOfStrikingHit(charID int, crit bool) (int, string) {
	v, _ := attackDescriptions.Load(charID)
	d, _ := v.(string)
	m := chargesWordRe.FindStringSubmatch(d)
	if m == nil {
		return 0, ""
	}
	n := int(m[1][0] - '0')
	it, ok := itemActive(charID, "staff of striking", "held")
	if !ok {
		return 0, ""
	}
	note, refusal := chargedUse(charID, it, n)
	if refusal != "" {
		return 0, " Staff of Striking: " + refusal
	}
	x := game.RollDamage(fmt.Sprintf("%dd6", n), crit)
	return x, fmt.Sprintf(" Staff of Striking: %d charge(s), +%d Force.%s", n, x, note)
}

func combatantAC(lobbyID, id int) int {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			return asInt(e["ac"])
		}
	}
	return 10
}

func itemTargets(req itemCastReq) []int {
	ids := append([]int{}, req.TargetIDs...)
	if req.TargetID != 0 {
		ids = append([]int{req.TargetID}, ids...)
	}
	return ids
}

func summonItem(charID int, it invItem, slug, label, notes string) string {
	id, _ := insertItemCompanion(charID, "summon", getCharacterName(charID)+"'s "+label, itemStr(it, "name"), slug, "", notes, "", 0)
	if id == 0 {
		return "Cannot summon: the " + label + " stat block wasn't found. Nothing was spent."
	}
	return fmt.Sprintf("%s (companion %d). Command it with POST /api/action {\"companion_id\": %d, \"action\": \"attack\", \"target_id\": ...}.", notes, id, id)
}

var figurineCreature = map[string][2]string{"bronze griffon": {"griffon", "Griffon"}, "golden lions": {"lion", "Lion"}, "ivory goats": {"goat", "Goat"},
	"marble elephant": {"elephant", "Elephant"}, "obsidian steed": {"nightmare", "Nightmare"}, "onyx dog": {"mastiff", "Mastiff"},
	"serpentine owl": {"giant-owl", "Giant Owl"}, "silver raven": {"raven", "Raven"}, "ebony fly": {"giant-fly", "Giant Fly"}}

var bagOfTricks = map[string][]string{
	"gray": {"weasel", "giant-rat", "badger", "boar", "panther", "giant-badger", "dire-wolf", "giant-elk"},
	"rust": {"rat", "owl", "mastiff", "goat", "giant-goat", "giant-boar", "lion", "brown-bear"},
	"tan":  {"jackal", "ape", "baboon", "axe-beak", "black-bear", "giant-weasel", "giant-hyena", "tiger"},
}

var elementalVessel = map[string][2]string{"bowl of commanding water elementals": {"water-elemental", "Water Elemental"},
	"brazier of commanding fire elementals": {"fire-elemental", "Fire Elemental"}, "censer of controlling air elementals": {"air-elemental", "Air Elemental"},
	"stone of controlling earth elementals": {"earth-elemental", "Earth Elemental"}}

// moreItemUse handles the use_item of the items above (F206).
func moreItemUse(charID int, it invItem, req itemCastReq, description string) (string, bool) {
	name := itemStr(it, "name")
	n := strings.ToLower(name)
	lobby := lobbyOfChar(charID)
	d := strings.ToLower(description)
	targets := itemTargets(req)
	switch {
	case strings.HasPrefix(n, "gem of brightness"):
		switch {
		case strings.Contains(d, "flare") || strings.Contains(d, "cone"):
			note, refusal := chargedUse(charID, it, 5)
			if refusal != "" {
				return refusal + " Nothing was spent.", true
			}
			_, lines := saveOrDamage(lobby, targets, "con", 15, "", "", "blinded", "Gem of Brightness", 10, true)
			return "The gem flares in a 30-foot Cone (a Magic action, 5 charges): each creature makes a DC 15 Constitution save or is Blinded for 1 minute (save again at the end of each of its turns). " + strings.Join(lines, " ") + note, true
		case strings.Contains(d, "beam") || strings.Contains(d, "ray"):
			if len(targets) == 0 {
				return "Gem of Brightness: name the creature for the beam (target_id). Nothing was spent.", true
			}
			note, refusal := chargedUse(charID, it, 1)
			if refusal != "" {
				return refusal + " Nothing was spent.", true
			}
			_, lines := saveOrDamage(lobby, targets[:1], "con", 15, "", "", "blinded", "Gem of Brightness", 10, true)
			return "A brilliant beam of light (a Magic action, 1 charge): a DC 15 Constitution save or Blinded for 1 minute (save again at the end of each of its turns). " + strings.Join(lines, " ") + note, true
		}
		return "The gem sheds Bright Light in a 30-foot radius and Dim Light for 30 more feet (a Magic action, no charge). Say \"beam\" with a target_id (1 charge) or \"flare\" with target_ids (5 charges) for the other command words.", true
	case strings.HasPrefix(n, "iron bands"):
		if len(targets) == 0 {
			return "Iron Bands: name the Huge or smaller creature within 60 feet (target_id). Nothing was spent.", true
		}
		t := targets[0]
		if t < 0 && !sizeAtMost(monsterKeyOfCombatant(lobby, t), "huge") {
			return "Iron Bands work on a Huge or smaller creature. Nothing was spent.", true
		}
		var dex, level int
		db.QueryRow(`SELECT dex, level FROM characters WHERE id = $1`, charID).Scan(&dex, &level)
		roll := game.RollDie(20)
		total := roll + game.Modifier(dex) + game.ProficiencyBonus(level)
		if roll == 1 || (roll != 20 && total < combatantAC(lobby, t)) {
			return fmt.Sprintf("You throw the Iron Bands (a Magic action): a ranged attack roll of %d misses %s; the bands contract into a sphere again.", total, creatureName(lobby, t)), true
		}
		addMonsterCondition(lobby, t, "restrained", "Iron Bands", 0, "", 0)
		return fmt.Sprintf("You throw the Iron Bands (a Magic action): a ranged attack roll of %d hits %s, which is Restrained until you release it (a Bonus Action). A creature that can touch the bands can take an action for a DC 20 Strength (Athletics) check to break them (the item is destroyed).", total, creatureName(lobby, t)), true
	case strings.HasPrefix(n, "rope of entanglement"):
		if len(targets) == 0 {
			return "Rope of Entanglement: name the creature within 20 feet (target_id). Nothing was spent.", true
		}
		_, lines := saveOrDamage(lobby, targets[:1], "dex", 15, "", "", "restrained", "Rope of Entanglement", 0, false)
		return "The rope darts out (a Magic action): a DC 15 Dexterity save or Restrained (escape: a DC 15 Strength (Athletics) or Dexterity (Acrobatics) check as an action; the rope has AC 20 and 20 HP). " + strings.Join(lines, " "), true
	case strings.HasPrefix(n, "rod of rulership"):
		if dawnUsed(it, "rulership") {
			return "The Rod of Rulership can't command obedience again until the next dawn. Nothing was spent.", true
		}
		if len(targets) == 0 {
			return "Rod of Rulership: name the creatures within 120 feet (target_id / target_ids). Nothing was spent.", true
		}
		updateInventoryItem(charID, it, func(x invItem) { x["dawn_used_rulership"] = true })
		_, lines := saveOrDamage(lobby, targets, "wis", 15, "", "", "charmed", "Rod of Rulership", 4800, false)
		return "You present the rod (a Magic action, once a dawn): each target makes a DC 15 Wisdom save or is Charmed for 8 hours, regarding you as its trusted leader (harm or a command against its nature ends it). " + strings.Join(lines, " "), true
	case strings.HasPrefix(n, "animated shield"):
		addSpellEffect(lobby, charID, charID, "Animated Shield", "animated_shield", "", 10, "", 0)
		return "The Shield animates (a Bonus Action): it hovers in your space and protects you as if you were wielding it, leaving your hands free, for 1 minute (a Bonus Action ends it; it falls if you die or are Incapacitated).", true
	case strings.HasPrefix(n, "staff of the python"):
		return summonItem(charID, it, "giant-constrictor-snake", "Giant Constrictor Snake", "You throw the staff (a Magic action) and it becomes a Giant Constrictor Snake within 10 feet, under your control (it acts right after you; a Bonus Action turns it back into the staff; at 0 Hit Points the staff is destroyed)"), true
	case strings.HasPrefix(n, "figurine of wondrous power"):
		for k, v := range figurineCreature {
			if strings.Contains(n, k) {
				return summonItem(charID, it, v[0], v[1], "You throw the figurine (a Magic action) and it becomes a "+v[1]+", Friendly to you, obeying your commands and acting right after you"), true
			}
		}
		return "Name the figurine's kind in its name (for example Figurine of Wondrous Power (Bronze Griffon)). Nothing was spent.", true
	case strings.HasPrefix(n, "bag of tricks"):
		color := "gray"
		for c := range bagOfTricks {
			if strings.Contains(n, c) {
				color = c
			}
		}
		if asInt(it["tricks_used"]) >= 3 {
			return "Three fuzzy objects have been pulled from the bag: it can't be used again until the next dawn. Nothing was spent.", true
		}
		slug := bagOfTricks[color][game.RollDie(8)-1]
		updateInventoryItem(charID, it, func(x invItem) { x["tricks_used"] = asInt(x["tricks_used"]) + 1 })
		return summonItem(charID, it, slug, titleCase(strings.ReplaceAll(slug, "-", " ")), "You pull a fuzzy object from the bag and throw it (a Magic action): it becomes a "+titleCase(strings.ReplaceAll(slug, "-", " "))+" (the d8 table of the "+color+" bag), Friendly to you until the next dawn or 0 Hit Points; a Bonus Action commands it"), true
	case strings.HasPrefix(n, "efreeti bottle"):
		roll := game.RollDie(10)
		switch {
		case roll == 1:
			return "You open the bottle (a Magic action). The efreeti attacks you (d10: 1): the GM adds an Efreeti to the fight; after 5 rounds it disappears and the bottle loses its magic.", true
		case roll <= 9:
			return summonItem(charID, it, "efreeti", "Efreeti", fmt.Sprintf("You open the bottle (a Magic action). The efreeti obeys your commands for 1 hour (d10: %d), then returns to the bottle", roll)), true
		}
		return "You open the bottle (a Magic action). The efreeti can cast Wish three times for you (d10: 10); it disappears when the third wish is granted or after 1 hour, and the bottle loses its magic.", true
	case strings.HasPrefix(n, "pipes of the sewers"):
		cnt := 1
		if m := chargesWordRe.FindStringSubmatch(description); m != nil {
			cnt = int(m[1][0] - '0')
		}
		note, refusal := chargedUse(charID, it, cnt)
		if refusal != "" {
			return refusal + " Nothing was spent.", true
		}
		var out []string
		for i := 0; i < cnt; i++ {
			out = append(out, summonItem(charID, it, "swarm-of-rats", "Swarm of Rats", "A Swarm of Rats answers the music"))
		}
		return fmt.Sprintf("You play the pipes (a Magic action) and spend %d charge(s) (a Bonus Action): %s%s", cnt, strings.Join(out, " "), note), true
	case strings.HasPrefix(n, "broom of flying"):
		addSpellEffect(lobby, charID, charID, "Broom of Flying", "fly", "50", 600, "", 0)
		return "The broom hovers beneath you (a Magic action): Fly Speed 50 feet (30 feet carrying over 200 pounds) while you ride it.", true
	case strings.HasPrefix(n, "carpet of flying"):
		speed := 80 // 3 ft. by 5 ft. (the SRD table: 4 by 6 60, 5 by 7 40, 6 by 9 30)
		for k, v := range map[string]int{"(4 ft": 60, "(5 ft": 40, "(6 ft": 30} {
			if strings.Contains(n, k) {
				speed = v
			}
		}
		addSpellEffect(lobby, charID, charID, "Carpet of Flying", "fly", fmt.Sprint(speed), 600, "", 0)
		return fmt.Sprintf("The carpet hovers and flies (a Magic action, its command word): Fly Speed %d feet (halved over its capacity) while you are within 30 feet of it.", speed), true
	case strings.HasPrefix(n, "lantern of revealing"):
		return "The lantern is lit (6 hours a pint of oil): Bright Light 30 feet and Dim Light 30 more; Invisible creatures and objects in its Bright Light are visible. A Utilize action lowers the hood (Dim Light, 5 feet).", true
	case strings.HasPrefix(n, "ring of x-ray vision"):
		if !wornItem(charID, "ring of x-ray vision") {
			return "Wear and attune to the Ring of X-ray Vision first. Nothing was spent.", true
		}
		out := "X-ray vision (a Magic action) for 1 minute: solid objects within 30 feet are transparent to you (through 1 foot of stone, 1 inch of metal, 3 feet of wood or dirt; lead blocks it)."
		if itemBool(it, "used_since_rest") {
			if ok, total := characterSave(charID, "con", 15); !ok {
				db.Exec(`UPDATE characters SET exhaustion_level = LEAST(COALESCE(exhaustion_level,0) + 1, 6) WHERE id = $1`, charID)
				out += fmt.Sprintf(" Used again before a Long Rest: Constitution save %d vs DC 15 fails, 1 Exhaustion level.", total)
			} else {
				out += fmt.Sprintf(" Used again before a Long Rest: Constitution save %d vs DC 15 succeeds.", total)
			}
		}
		updateInventoryItem(charID, it, func(x invItem) { x["used_since_rest"] = true })
		return out, true
	case strings.HasPrefix(n, "cube of force"):
		face, cost := 0, 0
		for i, cst := range []int{1, 2, 3, 4, 5} {
			if strings.Contains(d, fmt.Sprintf("face %d", i+1)) {
				face, cost = i+1, cst
			}
		}
		if face == 0 {
			return "Cube of Force: name the face (\"face 1\": gases, wind and fog can't pass, 1 charge; 2: nonliving matter, 2; 3: living matter, 3; 4: spell effects, 4; 5: nothing passes, 5; 6 deactivates it). Nothing was spent.", true
		}
		note, refusal := chargedUse(charID, it, cost)
		if refusal != "" {
			return refusal + " Nothing was spent.", true
		}
		return fmt.Sprintf("You press face %d (a Magic action, %d charge(s)): a 15-foot Cube of force around you blocks %s for 1 minute.%s", face, cost,
			[]string{"gases, wind and fog", "nonliving matter (walls of other matter can't cross)", "living matter", "spell effects", "everything"}[face-1], note), true
	case strings.HasPrefix(n, "immovable rod"):
		fixed := !itemBool(it, "fixed")
		updateInventoryItem(charID, it, func(x invItem) { x["fixed"] = fixed })
		if fixed {
			return "You press the button (a Utilize action): the rod is fixed in place and doesn't move, even against gravity (it holds up to 8,000 pounds; a DC 30 Strength (Athletics) check moves it 10 feet).", true
		}
		return "You press the button again (a Utilize action): the rod can move.", true
	}
	for k, v := range elementalVessel {
		if strings.HasPrefix(n, k) {
			if dawnUsed(it, "elemental") {
				return "The " + name + " can't be used this way again until the next dawn. Nothing was spent.", true
			}
			updateInventoryItem(charID, it, func(x invItem) { x["dawn_used_elemental"] = true })
			return summonItem(charID, it, v[0], v[1], "You summon a "+v[1]+" (a Magic action, once a dawn): it understands your languages, obeys your commands and acts right after you; it disappears after 1 hour, at 0 Hit Points or when dismissed (a Bonus Action)"), true
		}
	}
	return "", false
}

// itemSRDText is the first part of an item's SRD description, for the items whose effect the GM applies in the world.
func itemSRDText(name string) string {
	var items []srdMagicItem
	json.Unmarshal(srd521MagicItemsJSON, &items)
	base := strings.ToLower(baseItemName(name))
	for _, m := range items {
		if strings.EqualFold(m.Name, name) || strings.ToLower(m.Name) == base {
			t := strings.TrimSpace(m.Description)
			if len(t) > 600 {
				t = t[:600]
				if i := strings.LastIndex(t, ". "); i > 200 {
					t = t[:i+1]
				}
			}
			return t
		}
	}
	return ""
}

var giantFlyOnce sync.Once

// ensureGiantFly adds the Figurine of Wondrous Power's Giant Fly (the Ebony Fly; SRD 5.2.1 stat block in the item) to the monsters (F207).
func ensureGiantFly() {
	giantFlyOnce.Do(func() {
		if db == nil {
			return
		}
		db.Exec(`INSERT INTO monsters (slug, name, size, type, ac, hp, hit_dice, speed, str, dex, con, intl, wis, cha, cr, xp, actions, source, ruleset, alignment,
			speed_text, initiative_mod, saves, senses, languages, traits, description)
			VALUES ('giant-fly', 'Giant Fly', 'large', 'beast', 11, 19, '3d10+3', 30, 14, 13, 13, 2, 10, 3, '0', 0, '[]'::jsonb, 'srd521', '2024', 'Unaligned',
			'30 ft., Fly 60 ft.', 1, '{"str": 2, "dex": 1, "con": 1, "int": -4, "wis": 0, "cha": -4}'::jsonb, 'Darkvision 60 ft.; Passive Perception 10', 'None', '[]'::jsonb,
			'The Ebony Fly of a Figurine of Wondrous Power (SRD 5.2.1): a Giant Fly that can be ridden as a mount (it can be used three times; then it rests 2 days).')
			ON CONFLICT (slug) DO NOTHING`)
	})
}
