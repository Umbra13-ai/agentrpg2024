package main

// Fixes from the C4 coverage session (level20-battle-tests/s35_c4.py, Nov 9 2026; FINDINGS F150-F158):
//	Magic ammunition       "Ammunition, +2 (Arrows)" is arrows: an archer with only magic arrows was "out of arrows"; its +N goes on the
//	                       attack and damage rolls (F150; asked for with "+2 arrows" or "magic arrow", else used when no plain ammunition is left)
//	Staff of Fire          held and attuned: Resistance to Fire damage (F151); the Frost Brand (held) too
//	Helm of Brilliance     worn and attuned: its gems cast Daylight (opal), Fireball (fire opal), Prismatic Spray (diamond) and Wall of Fire
//	                       (ruby), save DC 18, each using up a gem; a ruby gives Resistance to Fire; "ignite" (a Magic action) sets a
//	                       held weapon ablaze for +1d6 Fire on a hit (F152)
//	Giant Slayer           a hit on a Giant: +2d6 of the weapon's type and a DC 15 Strength save or Prone (F153)
//	Periapt of Health      worn and attuned: a Magic action regains 2d4 + 2 Hit Points once a dawn; Advantage on saves against Poisoned (F154)
//	Wand of Paralysis      held and attuned, use_item with target_id: 1 charge, DC 15 Constitution save or Paralyzed for 1 minute (F155)
//	Elemental Gem          a Utilize action breaks it: the elemental of its gem is summoned as a companion for 1 hour (F156)
//	Potion of Climbing     a Climb Speed equal to the Speed for 1 hour, Advantage on Athletics checks to climb (F157)
//	Mace of Smiting        against a Construct: +3 instead of +1; a 20 adds 7 Bludgeoning (14 against a Construct, which is destroyed at 25
//	                       Hit Points or fewer) (F158)

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

var magicAmmoPlus sync.Map // charID -> the +N of the magic ammunition this attack fires

// wantsMagicAmmo: the attack's description asks for the magic ammunition ("+2 arrows", "magic arrow").
func wantsMagicAmmo(charID int) bool {
	v, _ := attackDescriptions.Load(charID)
	d, _ := v.(string)
	d = strings.ToLower(d)
	return strings.Contains(d, "magic arrow") || strings.Contains(d, "magic bolt") || plusRe.MatchString(d) && (strings.Contains(d, "arrow") || strings.Contains(d, "bolt"))
}

// useMagicAmmo fires one piece of magic ammunition of the type ("arrows", "bolts"...) and remembers its bonus.
func useMagicAmmo(charID int, ammoType string) bool {
	word := strings.TrimSuffix(strings.ToLower(ammoType), "s")
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []map[string]interface{}
	json.Unmarshal(raw, &inv)
	isLost := lostChecker(inv)
	for i, it := range inv {
		n := strings.ToLower(fmt.Sprint(it["name"]))
		if isLost(it) || !strings.HasPrefix(n, "ammunition") || !strings.Contains(n, word) {
			continue
		}
		qty := 1
		if q, ok := it["quantity"].(float64); ok {
			qty = int(q)
		}
		if qty <= 0 {
			continue
		}
		if qty == 1 {
			inv = append(inv[:i], inv[i+1:]...)
		} else {
			inv[i]["quantity"] = float64(qty - 1)
		}
		b, _ := json.Marshal(inv)
		db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, b, charID)
		magicAmmoPlus.Store(charID, itemPlus(n))
		return true
	}
	return false
}

// heldResistance: Resistance from an item that works while held (Staff of Fire, Frost Brand) or worn (the Helm of Brilliance's rubies).
func heldResistance(charID int, dtype string) (bool, string) {
	if !strings.EqualFold(dtype, "fire") {
		return false, ""
	}
	for _, name := range []string{"staff of fire", "frost brand"} {
		if _, ok := itemActive(charID, name, "held"); ok {
			return true, titleCase(name)
		}
	}
	if it, ok := itemActive(charID, "helm of brilliance", "worn"); ok && helmGems(charID, it)["ruby"] > 0 {
		return true, "Helm of Brilliance (a ruby)"
	}
	return false, ""
}

// ---------------------------------------------------------------- Helm of Brilliance

var helmSpellGem = map[string]string{"daylight": "opal", "fireball": "fire opal", "prismatic-spray": "diamond", "wall-of-fire": "ruby"}

// helmGems: the gems left in the helm (rolled the first time: 1d10 diamonds, 2d10 rubies, 3d10 fire opals, 4d10 opals).
func helmGems(charID int, it invItem) map[string]int {
	g := map[string]int{}
	if raw, ok := it["gems"].(map[string]interface{}); ok {
		for k, v := range raw {
			g[k] = int(asFloat(v))
		}
		return g
	}
	g = map[string]int{"diamond": game.RollDamage("1d10", false), "ruby": game.RollDamage("2d10", false), "fire opal": game.RollDamage("3d10", false), "opal": game.RollDamage("4d10", false)}
	saveHelmGems(charID, it, g)
	return g
}

func saveHelmGems(charID int, it invItem, g map[string]int) {
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				m := map[string]interface{}{}
				for k, v := range g {
					m[k] = v
				}
				x["gems"] = m
			}
		}
		return inv
	})
}

// helmGemFor: the gem a Helm of Brilliance spell needs, and why it can't be cast ("" when it can).
func helmGemFor(charID int, it invItem, spellKey string) (string, string) {
	gem, ok := helmSpellGem[spellKey]
	if !ok {
		return "", ""
	}
	if helmGems(charID, it)[gem] <= 0 {
		return gem, fmt.Sprintf("The Helm of Brilliance has no %s left. Nothing was spent.", gem)
	}
	return gem, ""
}

// useHelmGem: the gem crumbles as the spell is cast.
func useHelmGem(charID int, it invItem, gem string) string {
	g := helmGems(charID, it)
	g[gem]--
	saveHelmGems(charID, it, g)
	return fmt.Sprintf(" [Helm of Brilliance: a %s is used up (%d diamonds, %d rubies, %d fire opals, %d opals left)]", gem, g["diamond"], g["ruby"], g["fire opal"], g["opal"])
}

func init() {
	noConcentration["helm_flames"] = true
	spellEffectLabel["helm_flames"] = "Helm of Brilliance flames"
	spellEffectText["helm_flames"] = "The held weapon blazes: +1d6 Fire on a hit"
	noConcentration["climb_speed"] = true
	spellEffectLabel["climb_speed"] = "Climb Speed"
	spellEffectText["climb_speed"] = "A Climb Speed equal to the Speed; Advantage on Strength (Athletics) checks to climb"
}

// helmOfBrilliance: "ignite" (a Magic action, a fire opal left) sets the held weapon ablaze; "extinguish" (a Bonus Action) ends it.
func helmOfBrilliance(charID int, it invItem, description string) string {
	if _, ok := itemActive(charID, "helm of brilliance", "worn"); !ok {
		return "Cannot use the Helm of Brilliance: wear it and attune to it first. Nothing was spent."
	}
	lobby := lobbyOfChar(charID)
	if e, on := charBuff(charID, "helm_flames"); on || strings.Contains(strings.ToLower(description), "extinguish") {
		if on {
			db.Exec(`DELETE FROM spell_effects WHERE id = $1`, e.ID)
		}
		return "The flames on your weapon go out."
	}
	g := helmGems(charID, it)
	if g["fire opal"] <= 0 {
		return "The Helm of Brilliance has no fire opal left: it can't set a weapon ablaze. Nothing was spent."
	}
	h := handsOf(charID)
	if h.Main == "" && h.Off == "" {
		return "Hold a weapon first: the Helm of Brilliance sets a weapon you are holding ablaze. Nothing was spent."
	}
	addSpellEffect(lobby, charID, charID, "Helm of Brilliance", "helm_flames", "", 999999, "", 0)
	return fmt.Sprintf("Your weapon bursts into flames (a Magic action; the Helm of Brilliance has %d fire opals): Bright Light 10 feet, Dim Light 10 more, and +1d6 Fire damage on a hit until you extinguish them (a Bonus Action) or drop or stow the weapon. Diamonds: %d, rubies: %d (Resistance to Fire), opals: %d.",
		g["fire opal"], g["diamond"], g["ruby"], g["opal"])
}

// helmFlamesHit: +1d6 Fire on a hit with the blazing weapon.
func helmFlamesHit(charID int, monsterKey string, crit bool) (int, string) {
	if _, on := charBuff(charID, "helm_flames"); !on {
		return 0, ""
	}
	d, how := adjustMonsterDamage(monsterKey, "fire", game.RollDamage("1d6", crit))
	return d, fmt.Sprintf(" Helm of Brilliance flames: +%d Fire%s.", d, map[string]string{"resistant": " (it resists)", "immune": " (it is immune)", "vulnerable": " (it is vulnerable)"}[how])
}

// ---------------------------------------------------------------- weapons

func giantSlayerHit(lobbyID, targetID int, monsterKey, name, heldItem string, crit bool) (int, string) {
	if monsterKind(monsterKey) != "giant" {
		return 0, ""
	}
	dtype := "slashing"
	if w, ok := srdWeapons[baseWeaponOf(heldItem)]; ok {
		dtype = strings.ToLower(w.DamageType)
	}
	d, _ := adjustMonsterDamage(monsterKey, dtype, game.RollDamage("2d6", crit))
	text := fmt.Sprintf(" Giant Slayer: +%d %s against the Giant.", d, titleCase(dtype))
	if saved, _ := monsterSave(lobbyID, targetID, "str", 15); saved {
		text += fmt.Sprintf(" %s keeps its footing (Strength save).", name)
	} else if sizeAtMost(monsterKey, "gargantuan") {
		addMonsterCondition(lobbyID, targetID, "prone", "Giant Slayer", 0, "", 0)
		text += fmt.Sprintf(" %s fails its Strength save and has the Prone condition.", name)
	}
	return d, text
}

// maceOfSmitingAttack: the mace's bonus is +3 against a Construct (+2 more than the +1 already counted).
func maceOfSmitingAttack(charID int, monsterKey string) int {
	v, _ := attackHeldItem.Load(charID)
	h, _ := v.(string)
	if strings.HasPrefix(strings.ToLower(h), "mace of smiting") && monsterKind(monsterKey) == "construct" {
		return 2
	}
	return 0
}

func maceOfSmitingHit(lobbyID, targetID int, monsterKey, name string, natural, hpAfter int) (int, bool, string) {
	construct := monsterKind(monsterKey) == "construct"
	extra, text := 0, ""
	if construct {
		extra, text = 2, " Mace of Smiting: +3 against a Construct."
	}
	if natural == 20 {
		x := 7
		if construct {
			x = 14
		}
		extra += x
		text += fmt.Sprintf(" Mace of Smiting (a 20): +%d Bludgeoning.", x)
		if construct && hpAfter-extra <= 25 {
			return extra, true, text + fmt.Sprintf(" %s has 25 Hit Points or fewer: the Construct is destroyed.", name)
		}
	}
	return extra, false, text
}

// ---------------------------------------------------------------- Periapt of Health, Wand of Paralysis, Elemental Gem

func periaptOfHealthPoison(charID int) bool {
	if _, ok := itemActive(charID, "periapt of health", "worn"); !ok {
		return false
	}
	_, att := attunedTo(charID, "periapt of health")
	return att
}

func periaptOfHealth(charID int, it invItem) string {
	if !periaptOfHealthPoison(charID) {
		return "Cannot use the Periapt of Health: wear it and attune to it first. Nothing was spent."
	}
	if dawnUsed(it, "periapt") {
		return "The Periapt of Health can't do that again until the next dawn. Nothing was spent."
	}
	heal := game.RollDamage("2d4+2", false)
	_, before, after := healCharacter(charID, heal)
	markDawnUsed(charID, it, "periapt")
	return fmt.Sprintf("You draw on the Periapt of Health (a Magic action): you regain %d Hit Points (2d4 + 2): %d -> %d. Again after the next dawn.", after-before, before, after)
}

func wandOfParalysis(charID int, it invItem, req itemCastReq) string {
	name := itemStr(it, "name")
	if _, ok := attunedTo(charID, "wand of paralysis"); !ok {
		return "Cannot use the " + name + ": you must be attuned to it (POST /api/characters/attune). Nothing was spent."
	}
	handHeld(charID, it)
	if itemStr(it, "location") != "held" {
		return "Cannot use the " + name + ": hold it first (equipment/move to \"held\"). Nothing was spent."
	}
	if req.TargetID == 0 {
		return "Name the creature the ray streaks toward with target_id (within 60 feet). Nothing was spent."
	}
	var note, refusal string
	editInventory(charID, func(inv []invItem) []invItem { // spendCharges changes the item in the inventory the edit saves
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
	who := creatureName(lobby, req.TargetID)
	var saved bool
	if req.TargetID < 0 {
		saved, _ = monsterSave(lobby, req.TargetID, "con", 15)
	} else {
		saved, _ = characterSave(req.TargetID, "con", 15)
	}
	if saved {
		return fmt.Sprintf("A thin blue ray streaks from the wand (a Magic action, 1 charge) to %s, who succeeds on the DC 15 Constitution save.%s", who, note)
	}
	addMonsterCondition(lobby, req.TargetID, "paralyzed", "Wand of Paralysis", 10, "con", 15)
	return fmt.Sprintf("A thin blue ray streaks from the wand (a Magic action, 1 charge): %s fails the DC 15 Constitution save and has the Paralyzed condition for 1 minute (it repeats the save at the end of each of its turns).%s", who, note)
}

var gemElementals = map[string]string{"blue sapphire": "air-elemental", "emerald": "water-elemental", "red corundum": "fire-elemental", "yellow diamond": "earth-elemental"}

func elementalGem(charID int, it invItem) string {
	name := itemStr(it, "name")
	low := strings.ToLower(name)
	slug := ""
	for gem, s := range gemElementals {
		if strings.Contains(low, gem) {
			slug = s
		}
	}
	if slug == "" {
		return "The " + name + " doesn't say its gem (blue sapphire, emerald, red corundum or yellow diamond): the GM decides the elemental. Nothing was spent."
	}
	kind := titleCase(strings.ReplaceAll(slug, "-", " "))
	id, _ := insertItemCompanion(charID, "summon", fmt.Sprintf("%s's %s", getCharacterName(charID), kind), "elemental-gem", slug, "",
		"Summoned by breaking an Elemental Gem: it understands your languages, obeys your commands and takes its turn right after you; it disappears after 1 hour, when it dies, or when you dismiss it (a Bonus Action).", "", 0)
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") {
				return takeOne(inv, x)
			}
		}
		return inv
	})
	return fmt.Sprintf("You break the %s (a Utilize action): a %s appears beside it, obeys your commands for 1 hour and acts right after you (companion id %d: POST /api/action {\"companion_id\": %d, \"action\": \"attack\", \"target_id\": ...}). The gem is no longer magical.", name, kind, id, id)
}

func potionOfClimbing(lobbyID, targetID int, label string) string {
	addSpellEffect(lobbyID, targetID, targetID, "Potion of Climbing", "climb_speed", "", 600, "", 0)
	return fmt.Sprintf("🧪 %s: a Climb Speed equal to the Speed for 1 hour, and Advantage on Strength (Athletics) checks to climb.", label)
}

// useHelmGemInPlace: the cast's gem crumbles (inside the inventory edit that spends the cast).
func useHelmGemInPlace(it invItem, gem string) string {
	g, _ := it["gems"].(map[string]interface{})
	if g == nil {
		return ""
	}
	g[gem] = asFloat(g[gem]) - 1
	return fmt.Sprintf(" [Helm of Brilliance: a %s is used up; %v diamonds, %v rubies, %v fire opals, %v opals left]", gem, g["diamond"], g["ruby"], g["fire opal"], g["opal"])
}

// weaponOfWarning: an attuned Weapon of Warning within its wielder's reach gives the wielder and allies within 30 feet Advantage on
// Initiative (with a battle map the distance is checked; without one, the whole party is close enough).
func weaponOfWarning(lobbyID, id int) bool {
	if id <= 0 {
		return false
	}
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1`, lobbyID)
	if err != nil {
		return false
	}
	var ids []int
	for rows.Next() {
		var cid int
		rows.Scan(&cid)
		ids = append(ids, cid)
	}
	rows.Close()
	m, hasMap := activeEncounterMap(lobbyID)
	for _, cid := range ids {
		it := carriedItem(cid, "weapon of warning")
		if it == nil {
			continue
		}
		if _, ok := attunedTo(cid, itemStr(it, "name")); !ok {
			if _, ok2 := attunedTo(cid, "weapon of warning"); !ok2 {
				continue
			}
		}
		if !hasMap || cid == id {
			return true
		}
		a, okA := m.token(cid)
		b, okB := m.token(id)
		if !okA || !okB || tokenDistanceFeet(a, b) <= 30 {
			return true
		}
	}
	return false
}

// bracersOfArchery: +2 damage with a Longbow or Shortbow while the bracers are worn and attuned.
func bracersOfArchery(charID int, weapon string) (int, string) {
	w := strings.ToLower(weapon)
	if !strings.Contains(w, "longbow") && !strings.Contains(w, "shortbow") {
		return 0, ""
	}
	if _, ok := itemActive(charID, "bracers of archery", "worn"); !ok {
		return 0, ""
	}
	return 2, " Bracers of Archery: +2."
}

var swornEnemy sync.Map // charID -> the Oathbow's sworn enemy (a monster id)

func swornEnemyAlive(charID int) (int, bool) {
	v, ok := swornEnemy.Load(charID)
	if !ok {
		return 0, false
	}
	id := v.(int)
	for _, e := range combatEntries(lobbyOfChar(charID)) {
		if asInt(e["id"]) == id && asInt(e["hp"]) > 0 {
			return id, true
		}
	}
	return id, false
}

// oathbowRoll: "swift death" with the attuned Oathbow names the target as the sworn enemy; attacks against it have Advantage, and while it
// lives the character's attacks with other weapons have Disadvantage.
func oathbowRoll(charID int, weaponKey, description string) (adv, dis bool, note string) {
	held := strings.ToLower(heldItemFor(charID, weaponKey, false))
	_, attuned := attunedTo(charID, "oathbow")
	oath := strings.HasPrefix(held, "oathbow") && attuned
	_, target, hasTarget := attackedMonster(charID)
	sworn, alive := swornEnemyAlive(charID)
	if oath && hasTarget && strings.Contains(strings.ToLower(description), "swift death") && !alive {
		swornEnemy.Store(charID, target)
		sworn, alive = target, true
		note += fmt.Sprintf(" [Oathbow: %s is your sworn enemy]", creatureName(lobbyOfChar(charID), target))
	}
	switch {
	case oath && alive && hasTarget && sworn == target:
		return true, false, note + " [Oathbow: Advantage against your sworn enemy]"
	case !oath && alive:
		return false, true, note + " [Oathbow: Disadvantage with other weapons while your sworn enemy lives]"
	}
	return false, false, note
}

// oathbowHit: +3d6 Piercing against the sworn enemy.
func oathbowHit(charID, targetID int, monsterKey string, crit bool) (int, string) {
	if _, ok := attunedTo(charID, "oathbow"); !ok {
		return 0, ""
	}
	if sworn, alive := swornEnemyAlive(charID); !alive || sworn != targetID {
		return 0, ""
	}
	d, _ := adjustMonsterDamage(monsterKey, "piercing", game.RollDamage("3d6", crit))
	return d, fmt.Sprintf(" Oathbow: +%d Piercing against your sworn enemy.", d)
}

func asFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

// F159: magic containers (an Efficient Quiver, Bag of Holding, Handy Haversack or Portable Hole given as an item) are containers, and what is
// inside their extradimensional space adds no weight.
var extradimensional = []string{"efficient quiver", "bag of holding", "handy haversack", "portable hole"}

func magicContainerName(name string) bool {
	n := strings.ToLower(name)
	for _, p := range extradimensional {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// inExtradimensional: the item sits (at any depth) inside a magic container.
func inExtradimensional(it invItem, inv []invItem) bool {
	loc := itemStr(it, "location")
	for depth := 0; loc != "" && depth < 10; depth++ {
		var parent invItem
		for _, x := range inv {
			if itemStr(x, "id") == loc {
				parent = x
			}
		}
		if parent == nil {
			return false
		}
		if magicContainerName(itemStr(parent, "name")) {
			return true
		}
		loc = itemStr(parent, "location")
	}
	return false
}
