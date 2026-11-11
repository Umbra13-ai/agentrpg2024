package main

// More Legendary magic items (SRD 5.2.1 Magic Items A-Z; Nov 8, 2026, F61), the effects the second pass found missing:
//   Holy Avenger      +2d10 Radiant against Fiends and Undead; an Emanation (10 feet, 30 at Paladin 17) of Advantage on saves against spells
//   Defender          the first attack each turn can move some of the +3 to Armor Class until the start of the next turn ("defender: 2 to AC")
//   Luck Blade        +1 to saving throws; Luck rerolls one failed D20 Test a dawn; Wish from its 1d3 charges once a dawn
//   Armor of Invulnerability   Resistance to Bludgeoning, Piercing and Slashing; Metal Shell (Immunity for 10 minutes, once a dawn)
//   Plate Armor of Etherealness  the Etherealness spell (until the command word again or the armor comes off), once a dawn
//   Ring of Spell Turning     Advantage on saves against spells; a success against level 7 or lower: no effect; a spell aimed only at you
//                             with no area can be turned back on its caster (its own save DC)
//   Scarab of Protection      +1 AC; Advantage on saves against spells; 12 charges turn a failed save against Necromancy or an Undead's
//                             harmful effect into a success (a Reaction)
//   Rod of Lordly Might       a +3 Mace; buttons (a Bonus Action) make it a flaming Longsword or Shortsword (+2d6 Fire), a +3 Battleaxe or Spear,
//                             a pole or a ram; Drain Life and Paralyze on a hit, Terrify (a Magic action), each once a dawn
//   Staff of the Magi         a +2 Quarterstaff (and the other staves that are magic Quarterstaffs); Advantage on saves against spells;
//                             Spell Absorption (a Reaction against a spell aimed only at you); Retributive Strike
//   Ring of Elemental Command  Elemental Bane, Elemental Compulsion, the plane's Elemental Focus and its spells (save DC 18)
//   Hammer of Thunderbolts    hurled for a charge: a thunderclap (DC 17 Constitution or Stunned); Giant's Bane with an attuned Belt of Giant
//                             Strength or Gauntlets of Ogre Power (Strength +4 to 30; a 20 against a Giant: DC 17 Constitution or die)
//   Sovereign Glue, Universal Solvent (ounces), Well of Many Worlds (a portal; 1d8 hours), the Crystal Balls (their spells)
// Before this the server gave these items their numbers at most (a +3, an armor's AC).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// ---------------------------------------------------------------- helpers

func inventoryOf(charID int) []invItem {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var inv []invItem
	json.Unmarshal(raw, &inv)
	return usableInventory(inv)
}

var itemSuffixRe = regexp.MustCompile(`\s*\([^)]*\)\s*$`)

// baseItemName: "Ring of Elemental Command (Fire)" -> "Ring of Elemental Command".
func baseItemName(name string) string {
	return strings.TrimSpace(itemSuffixRe.ReplaceAllString(plusSuffixRe.ReplaceAllString(name, ""), ""))
}

// carriedItem: the first usable item whose name starts with prefix (not in a lost bag, not destroyed).
func carriedItem(charID int, prefix string) invItem {
	p := strings.ToLower(prefix)
	for _, it := range inventoryOf(charID) {
		if strings.HasPrefix(strings.ToLower(itemStr(it, "name")), p) && !itemBool(it, "destroyed") {
			return it
		}
	}
	return nil
}

func equippedArmorName(charID int) string {
	var a sql.NullString
	db.QueryRow(`SELECT equipped_armor FROM characters WHERE id = $1`, charID).Scan(&a)
	return a.String
}

func heldInHand(charID int, name string) bool {
	m, o := equippedWeapons(charID)
	return strings.EqualFold(m, name) || strings.EqualFold(o, name)
}

// itemActive: the character carries the item, is attuned to it when it needs attunement, and it is where it works: "held" (in hand),
// "worn" (worn, or the equipped armor) or "carried" (anywhere on the character).
func itemActive(charID int, prefix, place string) (invItem, bool) {
	it := carriedItem(charID, prefix)
	if it == nil || itemBool(it, "nonmagical") {
		return nil, false
	}
	name := itemStr(it, "name")
	if itemNeedsAttunement(baseItemName(name)) || itemNeedsAttunement(name) {
		if _, ok := attunedTo(charID, baseItemName(name)); !ok {
			return nil, false
		}
	}
	loc := itemStr(it, "location")
	switch place {
	case "held":
		if loc != "held" && !heldInHand(charID, name) {
			return nil, false
		}
	case "worn":
		if loc != "worn" && loc != "held" && !strings.EqualFold(equippedArmorName(charID), name) {
			return nil, false
		}
	}
	return it, true
}

func reactionSpent(charID int) bool {
	var used bool
	db.QueryRow(`SELECT COALESCE(reaction_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
	return used
}

func spendReaction(charID int) {
	db.Exec(`UPDATE characters SET reaction_used = true WHERE id = $1`, charID)
}

// dawnUsed: an item property already used since the last dawn ("dawn_used_<key>" on the item; cleared by tickItemCooldown).
func dawnUsed(it invItem, key string) bool { return itemBool(it, "dawn_used_"+key) }

func markDawnUsed(charID int, it invItem, key string) {
	updateInventoryItem(charID, it, func(x invItem) { x["dawn_used_"+key] = true })
}

// clearDawnFlags: a dawn passed (rechargeAtDawn): every "dawn_used_" flag and "once_used_" spell goes.
func clearDawnFlags(it invItem) []string {
	var cleared []string
	for k := range it {
		if strings.HasPrefix(k, "dawn_used_") || strings.HasPrefix(k, "once_used_") {
			delete(it, k)
			cleared = append(cleared, strings.TrimPrefix(strings.TrimPrefix(k, "dawn_used_"), "once_used_"))
		}
	}
	return cleared
}

// monstersWithin: monsters of the fight within feet of a token on the map (all living monsters when there is no map, if allNoMap).
func creaturesWithin(lobbyID, fromID, feet int, wantMonsters, wantCharacters bool) []int {
	m, ok := activeEncounterMap(lobbyID)
	var out []int
	if !ok {
		return nil
	}
	from, okF := m.token(fromID)
	if !okF {
		return nil
	}
	alive := livingMonsterIDs(lobbyID)
	for _, t := range m.Tokens {
		if t.ID == fromID || t.ID >= companionIDBase {
			continue
		}
		if (t.ID < 0 && (!wantMonsters || !alive[t.ID])) || (t.ID > 0 && !wantCharacters) {
			continue
		}
		if tokenDistanceFeet(from, t) <= feet {
			out = append(out, t.ID)
		}
	}
	return out
}

// ---------------------------------------------------------------- item spells: Crystal Balls, the Luck Blade's Wish, the Ring of Elemental Command

var (
	atWillSpellRe   = regexp.MustCompile(`cast ([A-Z][A-Za-z'/ ]+?) \(save DC (\d+)\)`)
	elementalRowRe  = regexp.MustCompile(`(?m)^(Air|Earth|Fire|Water) \| (.+)$`)
	elementalCellRe = regexp.MustCompile(`([A-Z][A-Za-z/' ]+?) \((\d+) charges?\)`)
	elementalSpells map[string][]itemSpell // plane -> its spells (save DC 18)
)

// extendItemUses adds the spells the charge parser can't read (loadItemUses calls it).
func extendItemUses(items []srdMagicItem) {
	elementalSpells = map[string][]itemSpell{}
	for _, m := range items {
		t := normQuotes(m.Description)
		u := itemUses[itemUseKey(m.Name)]
		if u == nil {
			continue
		}
		add := func(s itemSpell) {
			slug, ok := spellByName[strings.ToLower(s.Name)]
			if !ok {
				return
			}
			s.Slug = slug
			for _, have := range u.Spells {
				if have.Slug == slug {
					return
				}
			}
			u.Spells = append(u.Spells, s)
		}
		switch {
		case strings.HasPrefix(m.Name, "Crystal Ball"): // at will; Telepathy's Suggestion once a dawn
			for _, x := range atWillSpellRe.FindAllStringSubmatch(t, -1) {
				dc, _ := strconv.Atoi(x[2])
				add(itemSpell{Name: x[1], DC: dc, Once: x[1] == "Suggestion" && strings.Contains(t, "again until the next dawn")})
			}
		case m.Name == "Luck Blade":
			add(itemSpell{Name: "Wish", Cost: 1, Once: true})
		case m.Name == "Ring of Telekinesis": // F169: Telekinesis at will
			add(itemSpell{Name: "Telekinesis"})
		// F206: "you can cast X from it" items without charges (at will, or once a dawn)
		case m.Name == "Helm of Comprehending Languages":
			add(itemSpell{Name: "Comprehend Languages"})
		case m.Name == "Helm of Telepathy":
			add(itemSpell{Name: "Detect Thoughts", DC: 13, Once: true})
			add(itemSpell{Name: "Suggestion", DC: 13, Once: true})
		case m.Name == "Boots of Levitation":
			add(itemSpell{Name: "Levitate", Note: "on yourself"})
		case m.Name == "Ring of Jumping":
			add(itemSpell{Name: "Jump", Note: "on yourself"})
		case m.Name == "Hat of Disguise":
			add(itemSpell{Name: "Disguise Self", Note: "it ends if the hat is removed"})
		case m.Name == "Cape of the Mountebank":
			add(itemSpell{Name: "Dimension Door", Once: true})
		case m.Name == "Wind Fan":
			add(itemSpell{Name: "Gust of Wind", DC: 13, Note: "each use after the first before the next dawn: a cumulative 20 percent chance the fan tears"})
		case m.Name == "Amulet of the Planes":
			add(itemSpell{Name: "Plane Shift", Note: "a DC 15 Intelligence (Arcana) check; on a failure a random destination"})
		case m.Name == "Sending Stones":
			add(itemSpell{Name: "Sending", Once: true, Note: "the target is the bearer of the other stone"})
		case m.Name == "Candle of Invocation":
			add(itemSpell{Name: "Gate", Once: true, Note: "the candle is used up"})
		case m.Name == "Cloak of Arachnida":
			add(itemSpell{Name: "Web", DC: 13, Once: true, Note: "the web fills twice its normal area"})
		case m.Name == "Robe of Stars": // six stars; each casts the level 5 Magic Missile; 1d6 reappear at dusk
			if u.Max == "" {
				u.Max, u.Regain = "6", "1d6"
			}
			add(itemSpell{Name: "Magic Missile", Cost: 1, Level: 5})
		case m.Name == "Helm of Brilliance": // F152: each spell uses up a gem of its type (items_c4_2024.go)
			for _, s := range []string{"Daylight", "Fireball", "Prismatic Spray", "Wall of Fire"} {
				add(itemSpell{Name: s, DC: 18})
			}
		case m.Name == "Circlet of Blasting": // F126: Scorching Ray (+5 to hit), once a dawn
			add(itemSpell{Name: "Scorching Ray", Once: true, Note: "+5 to hit"})
		case m.Name == "Necklace of Prayer Beads": // F123: each bead casts its spell (a Bonus Action), then rests until the next dawn
			for _, s := range []itemSpell{{Name: "Bless"}, {Name: "Cure Wounds", Level: 2}, {Name: "Lesser Restoration"}, {Name: "Greater Restoration"},
				{Name: "Branding Smite"}, {Name: "Planar Ally"}, {Name: "Wind Walk"}} {
				s.Once = true
				add(s)
			}
		case m.Name == "Ring of Elemental Command":
			for _, row := range elementalRowRe.FindAllStringSubmatch(t, -1) {
				plane := strings.ToLower(row[1])
				for _, c := range elementalCellRe.FindAllStringSubmatch(row[2], -1) {
					if slug, ok := spellByName[strings.ToLower(strings.TrimSpace(c[1]))]; ok {
						n, _ := strconv.Atoi(c[2])
						elementalSpells[plane] = append(elementalSpells[plane], itemSpell{Slug: slug, Name: strings.TrimSpace(c[1]), Cost: n, DC: 18})
					}
				}
			}
		}
	}
}

// customItemUse: what an inventory item casts when it isn't one SRD entry: an entry with "cast_spells" (the Moon card's wishes), or a
// Ring of Elemental Command (the spells of its plane). nil for others.
func customItemUse(it invItem) *magicItemUse {
	if list, ok := it["cast_spells"].([]interface{}); ok {
		u := &magicItemUse{Name: itemStr(it, "name"), Max: strconv.Itoa(int(itemNum(it, "charges_max")))}
		for _, s := range list {
			slug := fmt.Sprint(s)
			name := titleCase(strings.ReplaceAll(slug, "-", " "))
			if sp, ok := srdSpellsMemory[slug]; ok {
				name = sp.Name
			}
			u.Spells = append(u.Spells, itemSpell{Slug: slug, Name: name, Cost: 1})
		}
		return u
	}
	if strings.HasPrefix(strings.ToLower(itemStr(it, "name")), "ring of elemental command") {
		loadItemUses()
		base := itemUses[itemUseKey("Ring of Elemental Command")]
		if base == nil {
			return nil
		}
		u := *base
		u.Spells = elementalSpells[elementalPlaneOf(it)]
		return &u
	}
	return nil
}

var planeWords = []string{"air", "earth", "fire", "water"}

// elementalPlaneOf: the ring's plane, from its name ("(Fire)") or kept on the item; "" when not chosen yet.
func elementalPlaneOf(it invItem) string {
	if p := itemStr(it, "plane"); p != "" {
		return p
	}
	n := strings.ToLower(itemStr(it, "name"))
	for _, p := range planeWords {
		if strings.Contains(n, "("+p+")") || strings.Contains(n, " "+p+" ") {
			return p
		}
	}
	return ""
}

// ensureElementalPlane: "The GM chooses or randomly determines the linked plane": the server rolls once and keeps it.
func ensureElementalPlane(charID int, it invItem) string {
	if p := elementalPlaneOf(it); p != "" {
		return p
	}
	p := planeWords[game.RollDie(4)-1]
	updateInventoryItem(charID, it, func(x invItem) { x["plane"] = p })
	return p
}

// crystalBallNote: what a Crystal Ball adds to its Scrying.
func crystalBallNote(itemName, slug string) string {
	n := strings.ToLower(itemName)
	switch {
	case slug == "scrying" && strings.Contains(n, "true seeing"):
		return " [Crystal Ball of True Seeing: you have Truesight 120 feet centered on the sensor.]"
	case slug == "scrying" && strings.Contains(n, "telepathy"):
		return " [Crystal Ball of Telepathy: you can communicate telepathically with creatures you see within 30 feet of the sensor, and cast Suggestion (save DC 17) through it once a dawn.]"
	case slug == "scrying" && strings.Contains(n, "mind reading"):
		return " [Crystal Ball of Mind Reading: you can cast Detect Thoughts (save DC 17) on creatures you see within 30 feet of the sensor, without Concentration while the Scrying lasts.]"
	}
	return ""
}

// ---------------------------------------------------------------- saving throws against spells

var (
	spellAdvItemsOnce sync.Once
	spellAdvItems     map[string]string // lower-case SRD name -> where it works ("held", "worn", "carried")
	spellSaveCtx      sync.Map          // character id -> true while it saves against a spell
)

func loadSpellAdvItems() {
	spellAdvItemsOnce.Do(func() {
		spellAdvItems = map[string]string{}
		var items []srdMagicItem
		json.Unmarshal(srd521MagicItemsJSON, &items)
		for _, m := range items {
			t := normQuotes(m.Description)
			if !strings.Contains(t, "Advantage on saving throws against spells") || m.Name == "Holy Avenger" {
				continue
			}
			place := "carried"
			switch {
			case strings.Contains(t, "While holding"), strings.Contains(t, "While you hold"), strings.Contains(t, "while holding"):
				place = "held"
			case strings.Contains(t, "While wearing"), strings.Contains(t, "While you wear"), strings.Contains(t, "while you wear"):
				place = "worn"
			}
			spellAdvItems[strings.ToLower(m.Name)] = place
		}
	})
}

// spellSaveProtection names what gives the character Advantage on a save against a spell: an item (Ring of Spell Turning, Scarab of
// Protection, Staff of the Magi, Robe of the Archmagi, Mantle of Spell Resistance) or a Holy Avenger's Emanation. "" for none.
func spellSaveProtection(lobbyID, charID int) string {
	loadSpellAdvItems()
	for _, it := range inventoryOf(charID) {
		base := strings.ToLower(baseItemName(itemStr(it, "name")))
		if place, ok := spellAdvItems[base]; ok {
			if _, active := itemActive(charID, base, place); active {
				return itemStr(it, "name")
			}
		}
	}
	return holyAvengerAura(lobbyID, charID)
}

// holyAvengerAura: a party member holding an attuned Holy Avenger has the character in its Emanation (10 feet; 30 feet with 17 Paladin
// levels). On the battle map the distance counts; without one, the party stands together.
func holyAvengerAura(lobbyID, charID int) string {
	if lobbyID == 0 {
		return ""
	}
	rows, err := db.Query(`SELECT id, name FROM characters WHERE lobby_id = $1 AND hp > 0 AND COALESCE(is_dead,false) = false
		AND (equipped_main_hand ILIKE 'holy avenger%' OR equipped_off_hand ILIKE 'holy avenger%')`, lobbyID)
	if err != nil {
		return ""
	}
	type holder struct {
		id   int
		name string
	}
	var hs []holder
	for rows.Next() {
		var h holder
		if rows.Scan(&h.id, &h.name) == nil {
			hs = append(hs, h)
		}
	}
	rows.Close()
	m, onMap := activeEncounterMap(lobbyID)
	for _, h := range hs {
		if _, ok := attunedTo(h.id, "holy avenger"); !ok {
			continue
		}
		radius := 10
		if classLevelsOf(h.id)["paladin"] >= 17 {
			radius = 30
		}
		if h.id != charID && onMap {
			a, okA := m.token(h.id)
			b, okB := m.token(charID)
			if okA && okB && tokenDistanceFeet(a, b) > radius {
				continue
			}
		}
		return "Holy Avenger (" + h.name + ")"
	}
	return ""
}

// spellSaveItemAdvantage: Advantage from spellSaveProtection while the character saves against a spell (spellSaveAdvantage calls it).
func spellSaveItemAdvantage(lobbyID, id int) bool {
	if id <= 0 {
		return false
	}
	if _, ok := spellSaveCtx.Load(id); !ok {
		return false
	}
	return spellSaveProtection(lobbyID, id) != ""
}

// staffAbsorbs: a spell aimed only at this character, which holds its attuned Staff of the Magi and has its Reaction: the staff cancels
// it and gains charges equal to its level; above 50 it explodes (Retributive Strike).
func staffAbsorbs(lobbyID, charID, level int) (bool, string) {
	it, ok := itemActive(charID, "staff of the magi", "held")
	if !ok || reactionSpent(charID) {
		return false, ""
	}
	cur, max, _ := itemCharges(it)
	if max == 0 {
		max = 50
	}
	spendReaction(charID)
	if cur+level > 50 {
		return true, "Staff of the Magi: Spell Absorption (a Reaction) takes the spell, and its charges go above 50: the staff explodes! " +
			retributiveStrike(lobbyID, charID, it, cur+level, nil)
	}
	updateInventoryItem(charID, it, func(x invItem) { setItemCharges(x, cur+level, max) })
	return true, fmt.Sprintf("Staff of the Magi: Spell Absorption (a Reaction) cancels the spell and gains %d charge(s) (%d of %d).", level, cur+level, max)
}

// ringTurns: the character wears an attuned Ring of Spell Turning.
func ringTurns(charID int) bool {
	_, ok := itemActive(charID, "ring of spell turning", "worn")
	return ok
}

// scarabPreserves: a failed save against a Necromancy spell or an Undead's harmful effect, with an attuned Scarab of Protection that has
// charges and the character's Reaction: the save succeeds instead.
func scarabPreserves(charID int, necromancy, fromUndead bool) string {
	if !necromancy && !fromUndead {
		return ""
	}
	it, ok := itemActive(charID, "scarab of protection", "carried")
	if !ok || reactionSpent(charID) {
		return ""
	}
	cur, max, okC := itemCharges(it)
	if !okC || cur <= 0 {
		return ""
	}
	spendReaction(charID)
	left := cur - 1
	updateInventoryItem(charID, it, func(x invItem) {
		setItemCharges(x, left, max)
		if left == 0 {
			x["destroyed"] = true
		}
	})
	note := fmt.Sprintf("Scarab of Protection (a Reaction): the failed save becomes a success (%d of %d charges left)", left, max)
	if left == 0 {
		note += "; the scarab crumbles into powder"
	}
	return note
}

// turnSpellBack: Ring of Spell Turning's Reaction against a spell aimed only at the wearer, without an area: the caster saves against its
// own DC and takes the effect.
func turnSpellBack(lobbyID, charID int, source string, e saveEffect, rolled map[int]int) string {
	if e.SourceID >= 0 || reactionSpent(charID) {
		return ""
	}
	spendReaction(charID)
	name := creatureName(lobbyID, e.SourceID)
	saved, total := monsterSaveFor(lobbyID, e.SourceID, e.Ability, e.DC, saveStakes{Condition: len(e.Conditions) > 0})
	out := fmt.Sprintf(" Ring of Spell Turning (a Reaction) turns the spell back: %s makes the %s save against its own DC %d: %d, %s", name, e.AbilityName, e.DC, total,
		map[bool]string{true: "a success", false: "a failure"}[saved])
	var parts []string
	for i, p := range e.Damage {
		d := rolled[i]
		if saved {
			if !e.HalfOnSuccess {
				continue
			}
			d /= 2
		}
		txt, _ := damageMonster(lobbyID, e.SourceID, d, p.Type)
		parts = append(parts, strings.TrimSpace(txt))
	}
	if !saved {
		for _, c := range e.Conditions {
			endAbility := ""
			if e.EndSave {
				endAbility = e.Ability
			}
			addMonsterCondition(lobbyID, e.SourceID, c, source, e.Rounds, endAbility, e.DC)
			parts = append(parts, name+" is "+c)
		}
	}
	if len(parts) > 0 {
		out += ": " + strings.Join(parts, "; ")
	}
	return out + "."
}

// ---------------------------------------------------------------- damage: resistances and immunities from items

// itemDamageGuard: Immunity or Resistance an item gives the character against a damage type: Armor of Invulnerability (and its Metal
// Shell), the Ring of Elemental Command's plane, Armor of Resistance and Ring of Resistance with the type in their name.
func itemDamageGuard(charID int, dtype string) (immune, resist bool, why string) {
	t := strings.ToLower(strings.TrimSpace(dtype))
	if t == "" {
		return
	}
	if r, why := moreItemResist(charID, t); r { // Boots of the Winterlands, Cloak of Arachnida, Belt of Dwarvenkind, Dragon Scale Mail (F204)
		return false, true, why
	}
	if t == "poison" && periaptProofAgainstPoison(charID) {
		return true, false, "Periapt of Proof against Poison" // F162 (items_c5_2024.go)
	}
	armor := strings.ToLower(equippedArmorName(charID))
	if (t == "bludgeoning" || t == "piercing" || t == "slashing") && strings.HasPrefix(armor, "armor of invulnerability") {
		if _, ok := attunedTo(charID, "armor of invulnerability"); ok {
			if hasSpellEffect(lobbyOfChar(charID), charID, "metal_shell") {
				return true, false, "Armor of Invulnerability: Metal Shell"
			}
			return false, true, "Armor of Invulnerability"
		}
	}
	if it, ok := itemActive(charID, "ring of elemental command", "worn"); ok {
		switch p := ensureElementalPlane(charID, it); {
		case p == "fire" && t == "fire":
			return true, false, "Ring of Elemental Command (fire)"
		case (p == "air" && t == "lightning") || (p == "earth" && t == "acid"):
			return false, true, "Ring of Elemental Command (" + p + ")"
		}
	}
	if strings.Contains(armor, "resistance") && strings.Contains(armor, t) {
		if _, ok := attunedTo(charID, baseItemName(equippedArmorName(charID))); ok || !itemNeedsAttunement("Armor of Resistance") {
			return false, true, equippedArmorName(charID)
		}
	}
	for _, it := range inventoryOf(charID) {
		n := strings.ToLower(itemStr(it, "name"))
		if strings.HasPrefix(n, "ring of resistance") && strings.Contains(n, t) && itemStr(it, "location") == "worn" {
			if _, ok := attunedTo(charID, itemStr(it, "name")); ok {
				return false, true, itemStr(it, "name")
			}
		}
	}
	return
}

// ---------------------------------------------------------------- Luck Blade

// luckBladeReady: the attuned Luck Blade is on the character, its Luck is unused since the dawn, and the character isn't Incapacitated.
func luckBladeReady(charID int) (invItem, bool) {
	it, ok := itemActive(charID, "luck blade", "carried")
	if !ok || dawnUsed(it, "luck") || isIncapacitated(charID) {
		return nil, false
	}
	return it, true
}

// luckReroll: the Luck Blade's Luck rerolls a failed D20 Test when its owner armed it ("luck" with use_item) or asked for it on this roll.
func luckReroll(charID int, asked bool) bool {
	it, ok := luckBladeReady(charID)
	if !ok || (!asked && !itemBool(it, "luck_armed")) {
		return false
	}
	updateInventoryItem(charID, it, func(x invItem) {
		x["dawn_used_luck"] = true
		delete(x, "luck_armed")
	})
	return true
}

var luckOnAttack sync.Map // character id -> the attack asked for Luck ("luck" in its description)

var attackExtraNote sync.Map // character id -> a note for the attack's reply (Defender's transfer, the hurled hammer's charge)

var luckWordRe = regexp.MustCompile(`(?i)\bluck\b`)

// ---------------------------------------------------------------- Defender

var defenderTransferRe = regexp.MustCompile(`(?i)\b([0-3])\s*(?:to|into|on|onto)\s*(?:my\s+)?(?:ac|armor class)\b|\b(?:ac|armor class)\s*\+?([0-3])\b`)

var defenderFirst sync.Map // character id -> the turn marker of its first Defender attack

// defenderAttack: the first attack with the attuned Defender this turn may move part of its +3 to AC ("2 to AC"). It returns a note.
func defenderAttack(charID int, description string) string {
	lobby := lobbyOfChar(charID)
	marker := combatTurnMarker(lobby)
	if v, ok := defenderFirst.Load(charID); ok && v.(string) == marker && marker != "" {
		return "" // not the first attack this turn
	}
	defenderFirst.Store(charID, marker)
	m := defenderTransferRe.FindStringSubmatch(description)
	if m == nil {
		return ""
	}
	n := atoiSafe(m[1] + m[2])
	if n <= 0 {
		return ""
	}
	addSpellEffect(lobby, charID, charID, "Defender", "defender_ac", strconv.Itoa(n), 2, "", 0)
	return fmt.Sprintf(" [Defender: +%d to AC until the start of your next turn; +%d to attack and damage this turn]", n, 3-n)
}

// defenderShift: the part of the Defender's bonus moved to AC this turn (0 when none).
func defenderShift(lobbyID, charID int) int {
	for _, e := range spellEffectsOn(lobbyID, charID) {
		if e.Kind == "defender_ac" {
			return atoiSafe(e.Detail)
		}
	}
	return 0
}

// ---------------------------------------------------------------- weapon effects on a hit (resolveAttackOnMonster)

var (
	rodPowerOnHit  sync.Map // character id -> "drain" or "paralyze" (the attack asked for it)
	hammerHurled   sync.Map // character id -> true: the Hammer of Thunderbolts was hurled (a charge spent)
	monsterTypeMap = map[string]string{}
	monsterTypeMu  sync.Mutex
)

func monsterKind(key string) string {
	monsterTypeMu.Lock()
	defer monsterTypeMu.Unlock()
	if t, ok := monsterTypeMap[key]; ok {
		return t
	}
	var t string
	db.QueryRow(`SELECT LOWER(COALESCE(type,'')) FROM monsters WHERE slug = $1`, key).Scan(&t)
	monsterTypeMap[key] = t
	return t
}

// weaponHitEffects: what the held item adds to a hit on a monster that is still standing (hp after the hit's damage): extra damage
// (Holy Avenger, the Rod's flaming blade) and the hit's riders (Drain Life, Paralyze, the hurled hammer's thunderclap, Giant's Bane).
// It returns the extra damage dealt, whether the monster dies (Giant's Bane), and the text.
func weaponHitEffects(lobbyID, charID, targetID int, monsterKey, name, heldItem string, crit bool, natural int, hpAfter int) (extra int, dies bool, text string) {
	held := strings.ToLower(heldItem)
	mtype := monsterKind(monsterKey)
	switch {
	case strings.HasPrefix(held, "sun blade"):
		d, note := sunBladeHit(monsterKey, crit) // F205 (items_more_2024.go)
		return d, false, note
	case strings.HasPrefix(held, "staff of striking"):
		d, note := staffOfStrikingHit(charID, crit) // F205
		return d, false, note
	case strings.HasPrefix(held, "flame tongue"):
		d, note := flameTongueHit(charID, monsterKey, heldItem, crit) // F103 (barbarian_fixes_2024.go)
		return d, false, note
	case strings.HasPrefix(held, "staff of thunder and lightning"):
		d, note := thunderLightningHit(lobbyID, charID, targetID, monsterKey, name, crit) // F161 (items_c5_2024.go)
		return d, false, note
	case strings.HasPrefix(held, "sword of wounding"):
		d, note := woundingHit(lobbyID, charID, targetID, monsterKey, name, crit) // F171
		return d, false, note
	case strings.HasPrefix(held, "frost brand"):
		d, note := frostBrandHit(monsterKey, crit) // F172
		return d, false, note
	case strings.HasPrefix(held, "dagger of venom"):
		d, note := venomHit(lobbyID, charID, targetID, monsterKey, name, crit) // F167
		return d, false, note
	case strings.HasPrefix(held, "giant slayer"):
		d, note := giantSlayerHit(lobbyID, targetID, monsterKey, name, heldItem, crit) // F153 (items_c4_2024.go)
		return d, false, note
	case strings.HasPrefix(held, "mace of smiting"):
		return maceOfSmitingHit(lobbyID, targetID, monsterKey, name, natural, hpAfter) // F158
	case strings.HasPrefix(held, "oathbow"):
		d, note := oathbowHit(charID, targetID, monsterKey, crit)
		return d, false, note
	case strings.HasPrefix(held, "vicious weapon"):
		d, note := viciousHit(monsterKey, heldItem, crit) // F143 (ranger_fixes_2024.go)
		return d, false, note
	case strings.HasPrefix(held, "sword of sharpness"):
		d, note := swordOfSharpnessHit(lobbyID, charID, targetID, monsterKey, natural) // F95 (fighter_fixes_2024.go)
		return d, false, note
	case strings.HasPrefix(held, "mace of disruption"):
		return maceOfDisruptionHit(lobbyID, charID, targetID, monsterKey, name, crit, hpAfter) // F86 (cleric_fixes_2024.go)
	case strings.HasPrefix(held, "holy avenger"):
		if _, ok := attunedTo(charID, "holy avenger"); ok && (mtype == "fiend" || mtype == "undead") {
			d, how := adjustMonsterDamage(monsterKey, "radiant", game.RollDamage("2d10", crit))
			extra += d
			text += fmt.Sprintf(" Holy Avenger: +%d Radiant against the %s%s.", d, titleCase(mtype), map[string]string{"resistant": " (it resists)", "immune": " (it is immune)", "vulnerable": " (it is vulnerable)"}[how])
		}
	case strings.HasPrefix(held, "rod of lordly might"):
		it, ok := itemActive(charID, "rod of lordly might", "held")
		if !ok {
			break
		}
		if strings.HasPrefix(rodForm(it), "flame") {
			d, how := adjustMonsterDamage(monsterKey, "fire", game.RollDamage("2d6", crit))
			extra += d
			text += fmt.Sprintf(" The fiery blade: +%d Fire%s.", d, map[string]string{"resistant": " (it resists)", "immune": " (it is immune)", "vulnerable": " (it is vulnerable)"}[how])
		}
		if v, ok := rodPowerOnHit.LoadAndDelete(charID); ok && hpAfter-extra > 0 {
			x, t := rodPower(lobbyID, charID, targetID, name, monsterKey, it, v.(string))
			extra += x
			text += t
		}
	case strings.HasPrefix(held, "hammer of thunderbolts"):
		if _, ok := hammerHurled.LoadAndDelete(charID); ok {
			text += hammerThunderclap(lobbyID, charID, targetID)
		}
		if natural == 20 && mtype == "giant" && giantsBane(charID) && hpAfter > 0 {
			saved, total := monsterSaveFor(lobbyID, targetID, "con", 17, saveStakes{Damage: hpAfter})
			if saved {
				text += fmt.Sprintf(" Giant's Bane (a roll of 20 against a Giant): %s's Constitution save %d against DC 17 succeeds.", name, total)
			} else {
				dies = true
				text += fmt.Sprintf(" Giant's Bane (a roll of 20 against a Giant): %s fails the DC 17 Constitution save (%d) and dies!", name, total)
			}
		}
	}
	return
}

// ---------------------------------------------------------------- Rod of Lordly Might

func rodForm(it invItem) string {
	if f := itemStr(it, "rod_form"); f != "" {
		return f
	}
	return "mace"
}

// rodWeaponKey: the SRD weapon the rod is now ("" for the pole and the ram).
func rodWeaponKey(it invItem) string {
	switch f := rodForm(it); f {
	case "mace", "battleaxe", "spear":
		return f
	case "flame_longsword":
		return "longsword"
	case "flame_shortsword":
		return "shortsword"
	}
	return ""
}

// rodPlus: +3 as a Mace, Battleaxe or Spear; the fiery blade deals +2d6 Fire instead.
func rodPlus(it invItem) int {
	if strings.HasPrefix(rodForm(it), "flame") || rodWeaponKey(it) == "" {
		return 0
	}
	return 3
}

// rodPower resolves Drain Life or Paralyze after a hit. The extra Necrotic damage is returned for the caller to apply (it runs inside the
// attack's transaction on the turn order).
func rodPower(lobbyID, charID, targetID int, name, monsterKey string, it invItem, power string) (int, string) {
	key := map[string]string{"drain": "drain_life", "paralyze": "paralyze"}[power]
	if key == "" {
		return 0, ""
	}
	if dawnUsed(it, key) {
		return 0, fmt.Sprintf(" (%s was already used since the last dawn.)", map[string]string{"drain_life": "Drain Life", "paralyze": "Paralyze"}[key])
	}
	markDawnUsed(charID, it, key)
	if key == "drain_life" {
		dmg := game.RollDamage("4d6", false)
		saved, total := monsterSaveFor(lobbyID, targetID, "con", 17, saveStakes{Damage: dmg})
		if saved {
			return 0, fmt.Sprintf(" Drain Life: %s's Constitution save %d against DC 17 succeeds.", name, total)
		}
		d, _ := adjustMonsterDamage(monsterKey, "necrotic", dmg)
		healed := d / 2
		_, before, after := healCharacter(charID, healed)
		return d, fmt.Sprintf(" Drain Life: %s fails the DC 17 Constitution save (%d) and takes %d extra Necrotic damage; you regain %d Hit Points (%d → %d).", name, total, d, healed, before, after)
	}
	saved, total := monsterSaveFor(lobbyID, targetID, "con", 17, saveStakes{Condition: true})
	if saved {
		return 0, fmt.Sprintf(" Paralyze: %s's Constitution save %d against DC 17 succeeds.", name, total)
	}
	addMonsterCondition(lobbyID, targetID, "paralyzed", "Rod of Lordly Might", 10, "con", 17)
	return 0, fmt.Sprintf(" Paralyze: %s fails the DC 17 Constitution save (%d) and is Paralyzed for 1 minute (it repeats the save at the end of each of its turns).", name, total)
}

var rodButtonRe = regexp.MustCompile(`(?i)button\s*([1-6])`)

func rodOfLordlyMight(charID int, it invItem, req itemCastReq, description string) string {
	if _, ok := itemActive(charID, "rod of lordly might", "held"); !ok {
		return "Cannot use the Rod of Lordly Might: hold it (equip it as a weapon) and be attuned to it. Nothing was spent."
	}
	d := strings.ToLower(description)
	lobby := lobbyOfChar(charID)
	if strings.Contains(d, "terrify") {
		if dawnUsed(it, "terrify") {
			return "Cannot use Terrify: it can't be used again until the next dawn. Nothing was spent."
		}
		markDawnUsed(charID, it, "terrify")
		targets := creaturesWithin(lobby, charID, 30, true, false)
		if len(targets) == 0 { // no map: the monsters the player names
			targets = append([]int{}, req.TargetIDs...)
			if req.TargetID < 0 {
				targets = append(targets, req.TargetID)
			}
		}
		var lines []string
		for _, t := range targets {
			if t >= 0 {
				continue
			}
			n := creatureName(lobby, t)
			saved, total := monsterSaveFor(lobby, t, "wis", 17, saveStakes{Condition: true})
			if saved {
				lines = append(lines, fmt.Sprintf("%s saves (%d)", n, total))
				continue
			}
			addMonsterCondition(lobby, t, "frightened", "Rod of Lordly Might", 10, "wis", 17)
			lines = append(lines, fmt.Sprintf("%s fails (%d) and is Frightened for 1 minute (it repeats the save at the end of its turns)", n, total))
		}
		if len(lines) == 0 {
			return "Terrify (a Magic action): no creature you can see is within 30 feet (on the battle map, or name them with target_ids)."
		}
		return "Terrify (a Magic action): each creature within 30 feet makes a DC 17 Wisdom save: " + strings.Join(lines, "; ") + "."
	}
	m := rodButtonRe.FindStringSubmatch(d)
	if m == nil {
		return "Cannot use the Rod of Lordly Might without saying how: \"button 1 longsword\" (or shortsword), \"button 2\" (Battleaxe), \"button 3\" (Spear), \"button 4\" (pole), \"button 5\" (ram), \"button 6\" (normal), or \"terrify\"; on a hit, add \"drain life\" or \"paralyze\" to the attack. Nothing was spent."
	}
	form := map[string]string{"1": "flame_longsword", "2": "battleaxe", "3": "spear", "4": "pole", "5": "ram", "6": "mace"}[m[1]]
	if m[1] == "1" && strings.Contains(d, "shortsword") {
		form = "flame_shortsword"
	}
	if rodForm(it) == form && m[1] != "6" { // the same button again: back to its normal form
		form = "mace"
	}
	updateInventoryItem(charID, it, func(x invItem) { x["rod_form"] = form })
	return map[string]string{
		"flame_longsword":  "Button 1 (a Bonus Action): a fiery blade sprouts; the rod is a magic Longsword that deals an extra 2d6 Fire damage on a hit (Bright Light 40 feet, Dim Light 40 more).",
		"flame_shortsword": "Button 1 (a Bonus Action): a fiery blade sprouts; the rod is a magic Shortsword that deals an extra 2d6 Fire damage on a hit (Bright Light 40 feet, Dim Light 40 more).",
		"battleaxe":        "Button 2 (a Bonus Action): the rod is a +3 magic Battleaxe.",
		"spear":            "Button 3 (a Bonus Action): the rod is a +3 magic Spear.",
		"pole":             "Button 4 (a Bonus Action): the rod is a climbing pole up to 50 feet long (a ladder bearing 4,000 pounds); it isn't a weapon now.",
		"ram":              "Button 5 (a Bonus Action): the rod is a handheld battering ram: +10 to Strength (Athletics) checks to break through doors, barricades and other barriers; it isn't a weapon now.",
		"mace":             "The rod is in its normal form: a +3 magic Mace (it points north and tells your depth or height).",
	}[form]
}

// ---------------------------------------------------------------- Staff of the Magi: Retributive Strike

// retributiveStrike breaks the staff: a 30-foot Emanation of Force damage. targets are used when there is no battle map.
func retributiveStrike(lobbyID, charID int, it invItem, charges int, targets []int) string {
	if charges < 0 {
		charges = 0
	}
	var out []string
	if game.RollDie(100) <= 50 {
		out = append(out, "You have a 50 percent chance to escape: you instantly travel to a random plane of existence (the GM decides which), avoiding the explosion.")
	} else {
		out = append(out, "You "+strings.Replace(damageCharacter(charID, 16*charges, "force"), "takes", "take", 1)+" (16 times the staff's charges).")
	}
	ids := creaturesWithin(lobbyID, charID, 30, true, true)
	if len(ids) == 0 {
		ids = targets
	}
	for _, id := range ids {
		if id == charID {
			continue
		}
		dmg := 6 * charges
		saved, total := creatureSave(lobbyID, id, "dex", 17)
		if saved {
			dmg /= 2
		}
		what := fmt.Sprintf("%s: Dexterity save %d against DC 17, %s; ", creatureName(lobbyID, id), total, map[bool]string{true: "a success", false: "a failure"}[saved])
		out = append(out, what+damageAny(lobbyID, id, dmg, "force")+".")
	}
	updateInventoryItem(charID, it, func(x invItem) { x["destroyed"] = true })
	db.Exec(`UPDATE characters SET attuned_items = COALESCE((SELECT jsonb_agg(v) FROM jsonb_array_elements(COALESCE(attuned_items,'[]'::jsonb)) v WHERE v::text NOT ILIKE '%staff of the magi%'), '[]'::jsonb) WHERE id = $1`, charID)
	return "Retributive Strike: the Staff of the Magi breaks and explodes in a 30-foot Emanation (" + strconv.Itoa(charges) + " charges). " + strings.Join(out, " ")
}

// ---------------------------------------------------------------- Ring of Elemental Command

// elementalBane: Advantage on the wearer's attacks against an Elemental, Disadvantage on an Elemental's attacks against the wearer.
func elementalBane(charID int, monsterKey string) bool {
	if monsterKind(monsterKey) != "elemental" {
		return false
	}
	_, ok := itemActive(charID, "ring of elemental command", "worn")
	return ok
}

func elementalRingUse(charID int, it invItem, req itemCastReq, description string) string {
	if _, ok := itemActive(charID, "ring of elemental command", "worn"); !ok {
		return "Cannot use the Ring of Elemental Command: wear it and be attuned to it. Nothing was spent."
	}
	plane := ensureElementalPlane(charID, it)
	d := strings.ToLower(description)
	lobby := lobbyOfChar(charID)
	if strings.Contains(d, "compel") || strings.Contains(d, "compulsion") {
		if req.TargetID >= 0 {
			return "Cannot use Elemental Compulsion without a target: target_id an Elemental you see within 60 feet. Nothing was spent."
		}
		e, _, ok := monsterCombatant(lobby, req.TargetID)
		if !ok || monsterKind(fmt.Sprint(e["monster_key"])) != "elemental" {
			return "Cannot use Elemental Compulsion: the target isn't an Elemental. Nothing was spent."
		}
		n := fmt.Sprint(e["name"])
		saved, total := monsterSaveFor(lobby, req.TargetID, "wis", 18, saveStakes{Condition: true})
		if saved {
			return fmt.Sprintf("Elemental Compulsion (a Magic action): %s's Wisdom save %d against DC 18 succeeds.", n, total)
		}
		addMonsterCondition(lobby, req.TargetID, "charmed", "Ring of Elemental Command", 1, "", 0)
		return fmt.Sprintf("Elemental Compulsion (a Magic action): %s fails the DC 18 Wisdom save (%d): it is Charmed until the start of your next turn, and you decide what it does with its move and action on its next turn (the GM runs it so).", n, total)
	}
	focus := map[string]string{
		"air":   "You know Auran, have Resistance to Lightning damage, and have a Fly Speed equal to your Speed (you can hover).",
		"earth": "You know Terran and have Resistance to Acid damage; rubble, rocks and dirt aren't Difficult Terrain for you, and you can move through solid earth or rock as Difficult Terrain.",
		"fire":  "You know Ignan and have Immunity to Fire damage.",
		"water": "You know Aquan, have a Swim Speed of 60 feet, and can breathe underwater.",
	}[plane]
	var names []string
	for _, s := range elementalSpells[plane] {
		names = append(names, fmt.Sprintf("%s (%d)", s.Name, s.Cost))
	}
	return fmt.Sprintf("Ring of Elemental Command (%s). Elemental Bane: Advantage on your attacks against Elementals, and they have Disadvantage on attacks against you. Elemental Focus: %s Compel an Elemental with \"compel\" and target_id (DC 18 Wisdom). Its spells (save DC 18; charges): %s, cast with {\"action\": \"cast\", \"spell_slug\": ..., \"item\": \"%s\"}.",
		plane, focus, strings.Join(names, ", "), itemStr(it, "name"))
}

// elementalSwim: a Ring of Elemental Command (water) gives a Swim Speed of 60 feet.
func elementalSwim(charID int) bool {
	it, ok := itemActive(charID, "ring of elemental command", "worn")
	return ok && ensureElementalPlane(charID, it) == "water"
}

// ---------------------------------------------------------------- Hammer of Thunderbolts

// giantsBane: attuned to the hammer and to a Belt of Giant Strength or Gauntlets of Ogre Power.
func giantsBane(charID int) bool {
	if _, ok := attunedTo(charID, "hammer of thunderbolts"); !ok {
		return false
	}
	_, belt := attunedTo(charID, "belt of giant strength")
	_, gauntlets := attunedTo(charID, "gauntlets of ogre power")
	return belt || gauntlets
}

// refreshMightOfGiants: with Giant's Bane, the Strength the belt or gauntlets give increases by 4 (to 30 at most); without it, it is the
// item's score again. Called after attuning or ending an attunement.
func refreshMightOfGiants(charID int) string {
	best := 0
	for _, n := range attunedItemNames(charID) {
		if col, score := scoreItemFor(n); col == "str" && score > best {
			best = score
		}
	}
	if best == 0 {
		return ""
	}
	want := best
	if giantsBane(charID) {
		want = best + 4
		if want > 30 {
			want = 30
		}
	}
	var cur int
	db.QueryRow(`SELECT str FROM characters WHERE id = $1`, charID).Scan(&cur)
	if cur == want || (cur > want && cur > best+4) {
		return ""
	}
	db.Exec(`UPDATE characters SET str = $1 WHERE id = $2`, want, charID)
	if want > best {
		return fmt.Sprintf(" Might of Giants (Hammer of Thunderbolts): your Strength is %d.", want)
	}
	return fmt.Sprintf(" Your Strength is %d again (no Giant's Bane).", want)
}

// hammerHurl: "hurl" or "throw" with the attuned Hammer of Thunderbolts in hand spends a charge for the thrown attack (20/60 feet).
func hammerHurl(charID int) (bool, string) {
	it, ok := itemActive(charID, "hammer of thunderbolts", "held")
	if !ok {
		return false, ""
	}
	note, refusal := "", ""
	editInventory(charID, func(inv []invItem) []invItem {
		for _, x := range inv {
			if itemStr(x, "id") == itemStr(it, "id") && itemStr(x, "name") == itemStr(it, "name") {
				note, refusal = spendCharges(charID, x, 1)
				break
			}
		}
		return inv
	})
	if refusal != "" {
		return false, "Cannot hurl the Hammer of Thunderbolts: " + refusal + " Nothing was spent."
	}
	hammerHurled.Store(charID, true)
	return true, note
}

// hammerThunderclap: the hurled hammer hit: the target and every creature within 30 feet of it other than the thrower make a DC 17
// Constitution save or are Stunned until the end of the thrower's next turn.
func hammerThunderclap(lobbyID, charID, targetID int) string {
	ids := append([]int{targetID}, creaturesWithin(lobbyID, targetID, 30, true, true)...)
	var lines []string
	seen := map[int]bool{}
	for _, id := range ids {
		if id == charID || seen[id] {
			continue
		}
		seen[id] = true
		saved, total := creatureSave(lobbyID, id, "con", 17)
		n := creatureName(lobbyID, id)
		if saved {
			lines = append(lines, fmt.Sprintf("%s saves (%d)", n, total))
			continue
		}
		addMonsterCondition(lobbyID, id, "stunned", "Hammer of Thunderbolts", 1, "", 0)
		lines = append(lines, fmt.Sprintf("%s fails (%d) and is Stunned until the end of your next turn", n, total))
	}
	return " Thunderclap (audible 300 feet away): DC 17 Constitution saves: " + strings.Join(lines, "; ") + ". The hammer flies back to your hand."
}

// ---------------------------------------------------------------- Armor of Invulnerability, Plate Armor of Etherealness

func armorOfInvulnerability(charID int, it invItem) string {
	if !strings.HasPrefix(strings.ToLower(equippedArmorName(charID)), "armor of invulnerability") {
		return "Cannot use Metal Shell: wear the Armor of Invulnerability (equip it as your armor). Nothing was spent."
	}
	if _, ok := attunedTo(charID, "armor of invulnerability"); !ok {
		return "Cannot use Metal Shell: you must be attuned to the armor. Nothing was spent."
	}
	if dawnUsed(it, "metal_shell") {
		return "Cannot use Metal Shell: it can't be used again until the next dawn. Nothing was spent."
	}
	markDawnUsed(charID, it, "metal_shell")
	addSpellEffect(lobbyOfChar(charID), charID, charID, "Armor of Invulnerability", "metal_shell", "", 100, "", 0)
	return "Metal Shell (a Magic action): you have Immunity to Bludgeoning, Piercing and Slashing damage for 10 minutes or until you take the armor off."
}

// ethereal: the character is on the Ethereal Plane (Plate Armor of Etherealness).
func ethereal(lobbyID, charID int) bool {
	return charID > 0 && hasSpellEffect(lobbyID, charID, "ethereal")
}

func plateOfEtherealness(charID int, it invItem) string {
	lobby := lobbyOfChar(charID)
	if !strings.HasPrefix(strings.ToLower(equippedArmorName(charID)), "plate armor of etherealness") {
		return "Cannot use the armor's command word: wear the Plate Armor of Etherealness (equip it as your armor). Nothing was spent."
	}
	if _, ok := attunedTo(charID, "plate armor of etherealness"); !ok {
		return "Cannot use the armor's command word: you must be attuned to it. Nothing was spent."
	}
	if ethereal(lobby, charID) {
		db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = 'ethereal'`, lobby, charID)
		return "You speak the command word again (a Magic action): Etherealness ends and you return to the Material Plane."
	}
	if dawnUsed(it, "etherealness") {
		return "Cannot use the armor's command word: it can't be used again until the next dawn. Nothing was spent."
	}
	markDawnUsed(charID, it, "etherealness")
	addSpellEffect(lobby, charID, charID, "Etherealness", "ethereal", "", 4800, "", 0)
	return "You speak the command word (a Magic action): you gain the effect of the Etherealness spell. You step into the Border Ethereal: creatures on the Material Plane can't see, target or harm you, and you can't affect them, though you see and hear them (60 feet). Repeat the command word or take the armor off to return."
}

// etherealBlock: why a creature can't be targeted or affected (it is ethereal), or "".
func etherealBlock(lobbyID, charID int) string {
	if ethereal(lobbyID, charID) {
		return creatureName(lobbyID, charID) + " is on the Ethereal Plane (Etherealness): it can't be targeted or affected from the Material Plane."
	}
	return ""
}

// ---------------------------------------------------------------- Sovereign Glue, Universal Solvent, Well of Many Worlds, Luck Blade

var ouncesRe = regexp.MustCompile(`(?i)(\d+)\s*(?:ounces?|oz)\b`)

func ouncesUse(charID int, it invItem, description, what string) string {
	name := itemStr(it, "name")
	left, has := it["ounces"]
	n := 0
	if has {
		n = asInt(left)
	} else {
		n = game.RollDie(6) + 1 // "When found, a container contains 1d6 + 1 ounces."
	}
	want := 1
	if m := ouncesRe.FindStringSubmatch(description); m != nil {
		want = atoiSafe(m[1])
	}
	if want < 1 {
		want = 1
	}
	if n <= 0 {
		return "Cannot use the " + name + ": it is empty. Nothing was spent."
	}
	if want > n {
		return fmt.Sprintf("Cannot use %d ounces of the %s: %d left. Nothing was spent.", want, name, n)
	}
	updateInventoryItem(charID, it, func(x invItem) { x["ounces"] = n - want })
	return fmt.Sprintf("%s (a Utilize action): %d ounce(s), %d left. %s", name, want, n-want, what)
}

func wellOfManyWorlds(charID int, it invItem, description string) string {
	d := strings.ToLower(description)
	if strings.Contains(d, "close") {
		if !itemBool(it, "portal_open") {
			return "Cannot close the Well of Many Worlds: no portal is open. Nothing was spent."
		}
		updateInventoryItem(charID, it, func(x invItem) { delete(x, "portal_open") })
		return "You take hold of the cloth's edges and fold it up (a Magic action): the portal closes."
	}
	if itemBool(it, "portal_open") {
		return "Cannot open another portal: the Well of Many Worlds is open now (\"close\" it first). Nothing was spent."
	}
	if dawnUsed(it, "portal") {
		return "Cannot open a portal yet: once the Well of Many Worlds has opened one, it can't again for 1d8 hours (the server counts the next dawn). Nothing was spent."
	}
	hours := game.RollDie(8)
	updateInventoryItem(charID, it, func(x invItem) {
		x["portal_open"] = true
		x["dawn_used_portal"] = true
		x["portal_hours"] = hours
	})
	return fmt.Sprintf("You unfold the Well of Many Worlds on a solid surface (a Magic action): a two-way, 6-foot circular portal opens to another world or plane, which the GM chooses. It stays open until a creature within 5 feet takes a Magic action to fold it up (\"close\"). It can't open again for %d hours.", hours)
}

func luckBladeUse(charID int, it invItem, description string) string {
	if _, ok := itemActive(charID, "luck blade", "carried"); !ok {
		return "Cannot call on the Luck Blade: be attuned to it and carry it. Nothing was spent."
	}
	if strings.Contains(strings.ToLower(description), "luck") {
		if dawnUsed(it, "luck") {
			return "Cannot call on the Luck Blade's Luck: it can't be used again until the next dawn. Nothing was spent."
		}
		updateInventoryItem(charID, it, func(x invItem) { x["luck_armed"] = true })
		return "The Luck Blade's Luck is ready (no action): the next D20 Test you fail (a saving throw the server rolls, an attack, or an ability check against a DC) is rerolled, and you use the second roll. Or write \"luck\" in an attack's description to reroll that attack if it misses."
	}
	return "The Luck Blade: +1 to attack and damage rolls and to your saving throws. \"luck\" readies its Luck (once a dawn); cast Wish from it with {\"action\": \"cast\", \"spell_slug\": \"wish\", \"item\": \"Luck Blade\"} (a charge, once a dawn)."
}

// ---------------------------------------------------------------- the use_item hook for these items

// legendaryMoreUse resolves use_item for the items above; ok is false for others.
func legendaryMoreUse(charID int, it invItem, req itemCastReq, description string) (string, bool) {
	name := strings.ToLower(itemStr(it, "name"))
	switch {
	case strings.HasPrefix(name, "rod of lordly might"):
		return rodOfLordlyMight(charID, it, req, description), true
	case strings.HasPrefix(name, "staff of the magi") && (strings.Contains(strings.ToLower(description), "retributive") || strings.Contains(strings.ToLower(description), "break")):
		if _, ok := itemActive(charID, "staff of the magi", "held"); !ok {
			return "Cannot break the Staff of the Magi: hold it and be attuned to it. Nothing was spent.", true
		}
		cur, _, _ := itemCharges(it)
		targets := append([]int{}, req.TargetIDs...)
		if req.TargetID != 0 {
			targets = append(targets, req.TargetID)
		}
		return "You break the staff over your knee (a Magic action). " + retributiveStrike(lobbyOfChar(charID), charID, it, cur, targets), true
	case strings.HasPrefix(name, "ring of elemental command"):
		return elementalRingUse(charID, it, req, description), true
	case strings.HasPrefix(name, "armor of invulnerability"):
		return armorOfInvulnerability(charID, it), true
	case strings.HasPrefix(name, "plate armor of etherealness"):
		return plateOfEtherealness(charID, it), true
	case strings.HasPrefix(name, "sovereign glue"):
		return ouncesUse(charID, it, description, "One ounce covers a 1-foot square; the glue sets in 1 minute, and the bond can be broken only by Universal Solvent, Oil of Etherealness or a Wish spell (the GM notes what is glued)."), true
	case strings.HasPrefix(name, "universal solvent"):
		return ouncesUse(charID, it, description, "Each ounce instantly dissolves up to 1 square foot of adhesive it touches, Sovereign Glue included."), true
	case strings.HasPrefix(name, "well of many worlds"):
		return wellOfManyWorlds(charID, it, description), true
	case strings.HasPrefix(name, "luck blade"):
		return luckBladeUse(charID, it, description), true
	case strings.HasPrefix(name, "apparatus of the crab"):
		return apparatusOfTheCrab(charID, it, req, description), true // apparatus_crab_2024.go (O-2)
	}
	return "", false
}

// legendaryMoreEconomy: the action a use_item of these items takes: "item_bonus" (a Rod button), "interact" (no action: the Luck Blade's
// Luck), or "" (the default, an action).
func legendaryMoreEconomy(charID int, ref, description string) string {
	it, _ := itemInUse(charID, ref, description)
	if it == nil {
		return ""
	}
	n, d := strings.ToLower(itemStr(it, "name")), strings.ToLower(description)
	switch {
	case strings.HasPrefix(n, "rod of lordly might") && rodButtonRe.MatchString(d):
		return "item_bonus"
	case strings.HasPrefix(n, "luck blade"):
		return "interact"
	case strings.HasPrefix(n, "flame tongue"):
		return "item_bonus" // the command word is a Bonus Action (F103)
	case strings.HasPrefix(n, "boots of speed"):
		return "item_bonus" // clicking the heels is a Bonus Action (F107)
	case strings.HasPrefix(n, "dancing sword"):
		return "item_bonus" // tossing or moving the sword is a Bonus Action (F114)
	case strings.HasPrefix(n, "ring of invisibility") && invisibleFrom(lobbyOfChar(charID), charID, "Ring of Invisibility"):
		return "item_bonus" // becoming visible again is a Bonus Action (F188)
	case strings.HasPrefix(n, "cloak of invisibility") && invisibleFrom(lobbyOfChar(charID), charID, "Cloak of Invisibility"):
		return "interact" // pulling the hood down takes no action (F187)
	case strings.HasPrefix(n, "gloves of missile snaring"):
		return "reaction_cast" // a Reaction to a ranged hit (F183)
	case strings.HasPrefix(n, "rod of absorption") && strings.Contains(d, "absorb"):
		return "reaction_cast" // absorbing a spell is a Reaction (F131)
	case strings.HasPrefix(n, "rod of absorption"):
		return "interact" // drawing the energy is part of casting the spell
	}
	return ""
}

// monsterSpellFromUse: the GM's "use" names a spell the monster's text offers ("replace one Fireball with Hold Monster (level 7 version)",
// a Lich's Spellcasting list): that spell and its level (0: the spell's own). F61: the GM couldn't pick one before.
func monsterSpellFromUse(text, use string) (string, int, bool) {
	u := strings.TrimSpace(use)
	if u == "" {
		return "", 0, false
	}
	slug := spellSlugFromName(u)
	sp, ok := srdSpellsMemory[slug]
	if !ok {
		return "", 0, false
	}
	low, name := strings.ToLower(normQuotes(text)), strings.ToLower(sp.Name)
	i := strings.Index(low, name)
	if i < 0 {
		return "", 0, false
	}
	lvl := 0
	if m := regexp.MustCompile(`^\s*\(level (\d) version\)`).FindStringSubmatch(low[i+len(name):]); m != nil {
		lvl = atoiSafe(m[1])
	}
	return sp.Name, lvl, true
}
