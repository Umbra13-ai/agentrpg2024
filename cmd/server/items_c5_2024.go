package main

// Fixes from the C5 coverage session (level20-battle-tests/s36_c5.py, Nov 9 2026; FINDINGS F160-F172):
//	Class languages          Druidic (Druid) and Thieves' Cant (Rogue) were missing from the character sheet's languages (F160)
//	Staff of Thunder and Lightning  Lightning (+2d6 on a melee hit), Thunder (DC 17 Con or Stunned), Lightning Strike (a Line, 9d6),
//	                         Thunderclap (a 60-foot Emanation, 2d6 and Deafened), each once a dawn (F161)
//	Periapt of Proof against Poison  worn and attuned: Immunity to Poison damage and the Poisoned condition (F162)
//	Winged Boots             a Magic action, 1 charge: Fly Speed 30 feet for 1 hour (F163)
//	Attack notes             Great Weapon Fighting's and Dueling's notes reach a targeted attack's reply (F164, like F140)
//	Gloves of Thievery       +5 to Dexterity (Sleight of Hand) checks (F165)
//	Weapons by name          "sword of wounding" didn't find a held "Sword of Wounding (Shortsword)": the attack became an Unarmed Strike;
//	                         a weapon held only in the other hand is used when the main hand is empty (F166)
//	Dagger of Venom          a Bonus Action coats it (once a dawn); the next hit: DC 15 Con or 2d10 Poison and Poisoned for 1 minute (F167)
//	Horn of Blasting         a 30-foot Cone: DC 15 Con, 5d8 Thunder and Deafened (half on a success); 20% to explode (10d6 Force to you) (F168)
//	Ring of Telekinesis      Telekinesis at will (F169)
//	Wand of Wonder           a charge and a d100 roll on the Wand of Wonder Effects table, resolved where the server can (F170)
//	Sword of Wounding        +2d6 Necrotic and a DC 15 Con save or no Hit Points regained for 1 hour (F171)
//	Frost Brand              +1d6 Cold on a hit (F172; Fire Resistance while held is F151)

import (
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// classLanguages: the languages a class teaches (SRD 5.2.1: Druidic at Druid 1, Thieves' Cant at Rogue 1).
func classLanguages(charID int, class string) []string {
	levels := classLevelsOf(charID)
	has := func(c string) bool {
		return strings.EqualFold(class, c) || levels[c] > 0 || levels[strings.Title(c)] > 0
	}
	var out []string
	if has("druid") {
		out = append(out, "Druidic")
	}
	if has("rogue") {
		out = append(out, "Thieves' Cant")
	}
	return out
}

// ---------------------------------------------------------------- Staff of Thunder and Lightning

func init() {
	noConcentration["winged_flight"] = true
	spellEffectLabel["winged_flight"] = "Winged Boots"
	spellEffectText["winged_flight"] = "Fly Speed 30 feet (Winged Boots)"
	noConcentration["venom_coat"] = true
	spellEffectLabel["venom_coat"] = "Dagger of Venom"
	spellEffectText["venom_coat"] = "The blade is coated with poison until a hit"
	noConcentration["no_regain"] = true
	spellEffectLabel["no_regain"] = "Sword of Wounding"
	spellEffectText["no_regain"] = "Can't regain Hit Points (a DC 15 Constitution save at the end of each of its turns ends it)"
}

// thunderLightningHit: "lightning", "thunder" or "thunder and lightning" in the attack's description with the staff (each once a dawn).
func thunderLightningHit(lobbyID, charID, targetID int, monsterKey, name string, crit bool) (int, string) {
	it, ok := itemActive(charID, "staff of thunder and lightning", "held")
	if !ok {
		return 0, ""
	}
	v, _ := attackDescriptions.Load(charID)
	d, _ := v.(string)
	d = strings.ToLower(d)
	both := strings.Contains(d, "thunder and lightning")
	useL := both || hasWord(d, "lightning")
	useT := both || hasWord(d, "thunder")
	key := func(p string) string {
		if both {
			return "tl_both"
		}
		return p
	}
	extra, text := 0, ""
	if useL {
		if dawnUsed(it, key("tl_lightning")) {
			text += " [the staff's Lightning is used until the next dawn]"
		} else {
			x, how := adjustMonsterDamage(monsterKey, "lightning", game.RollDamage("2d6", crit))
			extra += x
			text += fmt.Sprintf(" Staff of Thunder and Lightning: +%d Lightning%s.", x, map[string]string{"resistant": " (it resists)", "immune": " (it is immune)"}[how])
		}
	}
	if useT && !(dawnUsed(it, key("tl_thunder"))) {
		if saved, _ := monsterSave(lobbyID, targetID, "con", 17); saved {
			text += fmt.Sprintf(" Thunder: %s succeeds on the DC 17 Constitution save.", name)
		} else {
			addMonsterCondition(lobbyID, targetID, "stunned", "Staff of Thunder and Lightning", 2, "", 0)
			text += fmt.Sprintf(" Thunder: %s fails the DC 17 Constitution save and is Stunned until the end of your next turn.", name)
		}
	}
	if useL && !dawnUsed(it, key("tl_lightning")) {
		markDawnUsed(charID, it, key("tl_lightning"))
	}
	if useT {
		markDawnUsed(charID, it, key("tl_thunder"))
	}
	return extra, text
}

// thunderLightningStaff: use_item "lightning strike" (a Line: target_id / target_ids) or "thunderclap" (everyone within 60 feet).
func thunderLightningStaff(charID int, it invItem, req itemCastReq, description string) string {
	if _, ok := itemActive(charID, "staff of thunder and lightning", "held"); !ok {
		return "Cannot use the Staff of Thunder and Lightning: hold it and attune to it first. Nothing was spent."
	}
	d := strings.ToLower(description)
	lobby := lobbyOfChar(charID)
	var key, label, ability, dice, dtype, cond string
	var ids []int
	switch {
	case strings.Contains(d, "strike"):
		key, label, ability, dice, dtype = "tl_strike", "Lightning Strike (a Line 5 feet wide and 120 feet long)", "dex", "9d6", "lightning"
		ids = areaTargets(lobby, charID, 120, req, false)
	case strings.Contains(d, "thunderclap"):
		key, label, ability, dice, dtype, cond = "tl_clap", "Thunderclap (every creature within 60 feet)", "con", "2d6", "thunder", "deafened"
		ids = areaTargets(lobby, charID, 60, itemCastReq{TargetIDs: req.TargetIDs, TargetID: req.TargetID}, false)
	default:
		return "Say \"lightning strike\" (with target_id / target_ids in the Line) or \"thunderclap\". Lightning and Thunder work on a melee hit: write \"lightning\" or \"thunder\" in the attack. Nothing was spent."
	}
	if dawnUsed(it, key) {
		return "That property of the Staff of Thunder and Lightning is used until the next dawn. Nothing was spent."
	}
	if len(ids) == 0 {
		return "Name the creatures in the area with target_id / target_ids. Nothing was spent."
	}
	dmg := game.RollDamage(dice, false)
	var lines []string
	for _, id := range ids {
		var saved bool
		if id < 0 {
			saved, _ = monsterSave(lobby, id, ability, 17)
		} else {
			saved, _ = characterSave(id, ability, 17)
		}
		x := dmg
		if saved {
			x /= 2
		}
		var res string
		if id < 0 {
			res, _ = damageMonster(lobby, id, x, dtype)
		} else {
			res = damageCharacter(id, x, dtype)
		}
		line := fmt.Sprintf("%s %s: %s.", creatureName(lobby, id), map[bool]string{true: "saves (half)", false: "fails"}[saved], strings.TrimSpace(res))
		if !saved && cond != "" {
			addMonsterCondition(lobby, id, cond, "Staff of Thunder and Lightning", 10, "", 0)
			line += " Deafened for 1 minute."
		}
		lines = append(lines, line)
	}
	markDawnUsed(charID, it, key)
	return fmt.Sprintf("The staff unleashes its %s (a Magic action, DC 17 %s save, %s = %d %s, half on a success): %s Once a dawn.", label, strings.ToUpper(ability), dice, dmg, titleCase(dtype), strings.Join(lines, " "))
}

// ---------------------------------------------------------------- Periapt of Proof against Poison, Winged Boots, Gloves of Thievery

func periaptProofAgainstPoison(charID int) bool {
	if _, ok := itemActive(charID, "periapt of proof against poison", "worn"); !ok {
		return false
	}
	_, att := attunedTo(charID, "periapt of proof against poison")
	return att
}

func wingedBoots(charID int, it invItem) string {
	if _, ok := itemActive(charID, "winged boots", "worn"); !ok {
		return "Cannot use the Winged Boots: wear them and attune to them first. Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if e, on := charBuff(charID, "winged_flight"); on {
		db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
		return "You stop flying with the Winged Boots (if you are aloft, you descend 30 feet a round until you land)."
	}
	var note, refusal string
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				note, refusal = spendCharges(charID, x, 1)
			}
		}
		return inv
	})
	if refusal != "" {
		return refusal + " Nothing was spent."
	}
	addSpellEffect(lobby, charID, charID, "Winged Boots", "winged_flight", "30", 600, "", 0)
	return "The Winged Boots sprout wings (a Magic action, 1 charge): a Fly Speed of 30 feet for 1 hour (if you are flying when it ends, you descend 30 feet a round until you land)." + note
}

func glovesOfThievery(charID int, skill string) int {
	s := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(skill, " ", "_"), "-", "_"))
	if s != "sleight_of_hand" {
		return 0
	}
	if _, ok := itemActive(charID, "gloves of thievery", "worn"); ok {
		return 5
	}
	return 0
}

// ---------------------------------------------------------------- Dagger of Venom, Sword of Wounding, Frost Brand

func daggerOfVenom(charID int, it invItem) string {
	if dawnUsed(it, "venom") {
		return "The Dagger of Venom can't be coated again until the next dawn. Nothing was spent."
	}
	h := handsOf(charID)
	if !strings.HasPrefix(strings.ToLower(h.Main), "dagger of venom") && !strings.HasPrefix(strings.ToLower(h.Off), "dagger of venom") {
		return "Hold the Dagger of Venom first. Nothing was spent."
	}
	addSpellEffect(lobbyOfChar(charID), charID, charID, "Dagger of Venom", "venom_coat", "", 10, "", 0)
	markDawnUsed(charID, it, "venom")
	return "You coat the blade with poison (a Bonus Action): the next creature it hits within 1 minute makes a DC 15 Constitution save or takes 2d10 Poison damage and has the Poisoned condition for 1 minute. Again after the next dawn."
}

func venomHit(lobbyID, charID, targetID int, monsterKey, name string, crit bool) (int, string) {
	e, on := charBuff(charID, "venom_coat")
	if !on {
		return 0, ""
	}
	db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
	if saved, _ := monsterSave(lobbyID, targetID, "con", 15); saved {
		return 0, fmt.Sprintf(" Dagger of Venom: %s succeeds on the DC 15 Constitution save; the poison is spent.", name)
	}
	d, how := adjustMonsterDamage(monsterKey, "poison", game.RollDamage("2d10", crit))
	addMonsterCondition(lobbyID, targetID, "poisoned", "Dagger of Venom", 10, "", 0)
	return d, fmt.Sprintf(" Dagger of Venom: %s fails the DC 15 Constitution save: +%d Poison%s and the Poisoned condition for 1 minute.", name, d, map[string]string{"resistant": " (it resists)", "immune": " (it is immune)"}[how])
}

func woundingHit(lobbyID, charID, targetID int, monsterKey, name string, crit bool) (int, string) {
	d, how := adjustMonsterDamage(monsterKey, "necrotic", game.RollDamage("2d6", crit))
	text := fmt.Sprintf(" Sword of Wounding: +%d Necrotic%s.", d, map[string]string{"resistant": " (it resists)", "immune": " (it is immune)"}[how])
	if saved, _ := monsterSave(lobbyID, targetID, "con", 15); saved {
		return d, text + fmt.Sprintf(" %s succeeds on the DC 15 Constitution save.", name)
	}
	addSpellEffect(lobbyID, targetID, charID, "Sword of Wounding", "no_regain", "", 600, "con", 15)
	return d, text + fmt.Sprintf(" %s fails the DC 15 Constitution save: it can't regain Hit Points for 1 hour (it repeats the save at the end of each of its turns).", name)
}

func frostBrandHit(monsterKey string, crit bool) (int, string) {
	d, how := adjustMonsterDamage(monsterKey, "cold", game.RollDamage("1d6", crit))
	return d, fmt.Sprintf(" Frost Brand: +%d Cold%s.", d, map[string]string{"resistant": " (it resists)", "immune": " (it is immune)", "vulnerable": " (it is vulnerable)"}[how])
}

// ---------------------------------------------------------------- Horn of Blasting

func hornOfBlasting(charID int, it invItem, req itemCastReq) string {
	lobby := lobbyOfChar(charID)
	ids := areaTargets(lobby, charID, 30, req, false)
	dmg := game.RollDamage("5d8", false)
	var lines []string
	for _, id := range ids {
		var saved bool
		if id < 0 {
			saved, _ = monsterSave(lobby, id, "con", 15)
		} else {
			saved, _ = characterSave(id, "con", 15)
		}
		x := dmg
		if saved {
			x /= 2
		}
		var res string
		if id < 0 {
			res, _ = damageMonster(lobby, id, x, "thunder")
		} else {
			res = damageCharacter(id, x, "thunder")
		}
		line := fmt.Sprintf("%s %s: %s.", creatureName(lobby, id), map[bool]string{true: "saves (half)", false: "fails"}[saved], strings.TrimSpace(res))
		if !saved {
			addMonsterCondition(lobby, id, "deafened", "Horn of Blasting", 10, "", 0)
			line += " Deafened for 1 minute."
		}
		lines = append(lines, line)
	}
	text := fmt.Sprintf("You blow the Horn of Blasting (a Magic action): a 30-foot Cone of thunder (DC 15 Constitution save, 5d8 = %d Thunder and Deafened for 1 minute, half and no deafness on a success). %s",
		dmg, strings.Join(lines, " "))
	if len(ids) == 0 {
		text += " (Name the creatures in the Cone with target_ids.)"
	}
	if game.RollDie(100) <= 20 {
		boom := game.RollDamage("10d6", false)
		text += fmt.Sprintf(" The horn explodes (the 20 percent chance)! You take %d Force damage (10d6): %s. The horn is destroyed.", boom, damageCharacter(charID, boom, "force"))
		editInventory(charID, func(inv []invItem) []invItem {
			for _, x := range inv {
				if itemStr(x, "id") == itemStr(it, "id") {
					return takeOne(inv, x)
				}
			}
			return inv
		})
	}
	return text
}

// ---------------------------------------------------------------- Wand of Wonder

var wonderRolls sync.Map // charID -> a forced d100 for tests (never set by players)

func wandOfWonder(charID int, it invItem, req itemCastReq) string {
	if _, ok := attunedTo(charID, "wand of wonder"); !ok {
		return "Cannot use the Wand of Wonder: you must be attuned to it (POST /api/characters/attune). Nothing was spent."
	}
	handHeld(charID, it)
	if itemStr(it, "location") != "held" {
		return "Cannot use the Wand of Wonder: hold it first (equipment/move to \"held\"). Nothing was spent."
	}
	var note, refusal string
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				note, refusal = spendCharges(charID, x, 1)
			}
		}
		return inv
	})
	if refusal != "" {
		return refusal + " Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	roll := game.RollDie(100)
	if v, ok := wonderRolls.LoadAndDelete(charID); ok {
		roll = v.(int)
	}
	targets := append([]int{}, req.TargetIDs...)
	if req.TargetID != 0 {
		targets = append([]int{req.TargetID}, targets...)
	}
	closest := 0
	if len(targets) > 0 {
		closest = targets[0]
	}
	saveAll := func(ability string, dice, dtype string) string {
		dmg := game.RollDamage(dice, false)
		var lines []string
		for _, id := range targets {
			var saved bool
			if id < 0 {
				saved, _ = monsterSave(lobby, id, ability, 15)
			} else {
				saved, _ = characterSave(id, ability, 15)
			}
			x := dmg
			if saved {
				x /= 2
			}
			var res string
			if id < 0 {
				res, _ = damageMonster(lobby, id, x, dtype)
			} else {
				res = damageCharacter(id, x, dtype)
			}
			lines = append(lines, fmt.Sprintf("%s %s: %s.", creatureName(lobby, id), map[bool]string{true: "saves (half)", false: "fails"}[saved], strings.TrimSpace(res)))
		}
		if len(lines) == 0 {
			return fmt.Sprintf(" (%s = %d %s; name the creatures in the area with target_ids.)", dice, dmg, titleCase(dtype))
		}
		return fmt.Sprintf(" (%s = %d %s, DC 15 %s save, half on a success) %s", dice, dmg, titleCase(dtype), strings.ToUpper(ability), strings.Join(lines, " "))
	}
	var effect string
	switch {
	case roll <= 20:
		d10 := game.RollDie(10)
		switch {
		case d10 <= 2:
			effect = "you cast Darkness at the point (a 15-foot Sphere of magical Darkness for 10 minutes; the GM marks it)."
		case d10 <= 4:
			effect = "you cast Faerie Fire at the point (DC 15 Dexterity save or outlined: attacks against it have Advantage; the GM runs it)."
		case d10 <= 6:
			effect = "you cast Fireball at the point:" + saveAll("dex", "8d6", "fire")
		case d10 <= 8:
			effect = "you cast Slow at the point (up to six creatures make a DC 15 Wisdom save; the GM runs it)."
		default:
			effect = "you cast Stinking Cloud at the point (a 20-foot Sphere; DC 15 Constitution save or Poisoned; the GM marks it)."
		}
		effect = fmt.Sprintf("a spell (d10 %d): %s", d10, effect)
	case roll <= 25:
		addMonsterCondition(lobby, charID, "stunned", "Wand of Wonder", 1, "", 0)
		effect = "nothing happens there; instead you are Stunned until the start of your next turn, believing something awesome just happened."
	case roll <= 30:
		effect = "you cast Gust of Wind toward the point (a Line 60 feet long, 10 feet wide; DC 15 Strength save or pushed 15 feet; the GM runs it)."
	case roll <= 35:
		effect = "nothing happens there; instead you take " + damageCharacter(charID, game.RollDie(6), "psychic") + "."
	case roll <= 40:
		effect = "heavy rain falls for 1 minute in a 120-foot-high, 60-foot-radius Cylinder: the area is Lightly Obscured."
	case roll <= 45:
		effect = "600 oversized butterflies fill a 60-foot-high, 30-foot-radius Cylinder for 10 minutes: the area is Heavily Obscured."
	case roll <= 50:
		effect = "you cast Lightning Bolt toward the point:" + saveAll("dex", "8d6", "lightning")
	case roll <= 55:
		effect = fmt.Sprintf("the creature closest to the point (%s) is enlarged as if by Enlarge/Reduce (the GM applies it).", creatureName(lobby, closest))
	case roll <= 60:
		d4 := game.RollDie(4)
		slug := map[int]string{1: "rhinoceros", 2: "elephant", 3: "rat", 4: "rat"}[d4]
		ids := addMonstersToFight(lobby, slug, "Wand of Wonder "+titleCase(slug), 1, 0, nil)
		effect = fmt.Sprintf("a %s appears near the point (d4 %d): not under your control, it acts as it normally would and disappears after 1 hour or at 0 Hit Points%s.", titleCase(slug), d4,
			map[bool]string{true: fmt.Sprintf(" (it joins the fight: id %v)", ids), false: " (no fight is running: the GM places it)"}[len(ids) > 0])
	case roll <= 64:
		effect = "grass covers a 60-foot-radius circle around the point and grows to ten times its size for 1 minute."
	case roll <= 68:
		effect = "an object of the GM's choice within 120 feet of the point disappears into the Ethereal Plane."
	case roll <= 72:
		effect = "nothing happens there; instead you shrink as if Reduced (Enlarge/Reduce) for 1 minute (the GM applies it)."
	case roll <= 77:
		effect = fmt.Sprintf("leaves grow from the creature nearest the point (%s); they fall off after 24 hours.", creatureName(lobby, closest))
	case roll <= 82:
		ids := areaTargets(lobby, charID, 30, itemCastReq{TargetIDs: req.TargetIDs}, false)
		var lines []string
		for _, id := range ids {
			var saved bool
			if id < 0 {
				saved, _ = monsterSave(lobby, id, "con", 15)
			} else {
				saved, _ = characterSave(id, "con", 15)
			}
			if !saved {
				addMonsterCondition(lobby, id, "blinded", "Wand of Wonder", 10, "con", 15)
			}
			lines = append(lines, fmt.Sprintf("%s %s.", creatureName(lobby, id), map[bool]string{true: "saves", false: "is Blinded for 1 minute (a save at the end of each turn)"}[saved]))
		}
		effect = "nothing happens there; instead a burst of shimmering light fills a 30-foot Emanation from you (DC 15 Constitution save or Blinded). " + strings.Join(lines, " ")
	case roll <= 87:
		addMonsterCondition(lobby, charID, "invisible", "Wand of Wonder (Invisibility)", 600, "", 0)
		effect = "nothing happens there; instead you cast Invisibility on yourself (Invisible for up to 1 hour)."
	case roll <= 92:
		gems := game.RollDie(4) * 10
		effect = fmt.Sprintf("a stream of %d gems (1 GP each) shoots in a Line 30 feet long: %d Bludgeoning damage divided among the creatures in it", gems, gems)
		if len(targets) > 0 {
			each := gems / len(targets)
			var lines []string
			for _, id := range targets {
				var res string
				if id < 0 {
					res, _ = damageMonster(lobby, id, each, "bludgeoning")
				} else {
					res = damageCharacter(id, each, "bludgeoning")
				}
				lines = append(lines, creatureName(lobby, id)+": "+strings.TrimSpace(res)+".")
			}
			effect += " " + strings.Join(lines, " ")
		} else {
			effect += "."
		}
	case roll <= 97:
		form := map[int]string{1: "Black Bear", 2: "Giant Wasp", 3: "Frog", 4: "Frog"}[game.RollDie(4)]
		effect = fmt.Sprintf("you cast Polymorph on the creature closest to the point (%s): DC 15 Wisdom save or it becomes a %s (the GM applies it).", creatureName(lobby, closest), form)
	default:
		if closest == 0 {
			effect = "the creature closest to the point makes a DC 15 Constitution save or begins to turn to stone (name it with target_id)."
		} else {
			var saved bool
			if closest < 0 {
				saved, _ = monsterSave(lobby, closest, "con", 15)
			} else {
				saved, _ = characterSave(closest, "con", 15)
			}
			if saved {
				effect = fmt.Sprintf("%s, closest to the point, succeeds on the DC 15 Constitution save against turning to stone.", creatureName(lobby, closest))
			} else {
				addMonsterCondition(lobby, closest, "restrained", "Wand of Wonder (turning to stone)", 2, "", 0)
				effect = fmt.Sprintf("%s, closest to the point, fails the DC 15 Constitution save: it is Restrained and begins to turn to stone; it repeats the save at the end of its next turn (a failure: Petrified until Greater Restoration).", creatureName(lobby, closest))
			}
		}
	}
	return fmt.Sprintf("You wave the Wand of Wonder (a Magic action, 1 charge; d100 = %d): %s%s", roll, effect, note)
}
