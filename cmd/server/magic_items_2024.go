package main

// Magic items that change a character's numbers (SRD 5.2.1 Magic Items):
//   Weapon, +1, +2, or +3 (and Holy Avenger, +3): the bonus to attack and damage rolls with that weapon. The equipped main hand names it
//     ("Longsword +2", "+1 Rapier").
//   Armor, +1, +2, or +3: the bonus to AC (the equipped armor names it: "Plate Armor +1"). Shield, +1, +2, or +3: a "Shield +N" in the
//     inventory while a shield is equipped.
//   Attuned items (attuned_items): Ring of Protection and Cloak of Protection (+1 AC and saving throws), Bracers of Defense (+2 AC with
//     no armor and no Shield), Stone of Good Luck (+1 ability checks and saving throws), Staff of Power (+2 AC, saving throws and spell
//     attack rolls), Staff of the Magi (+2 spell attack rolls), Wand of the War Mage +N (+N spell attack rolls), Robe of the Archmagi
//     (with no armor, AC 15 + Dexterity modifier; +2 spell save DC and spell attack rolls).
//   Ability scores: Belt of Giant Strength (Hill 21, Frost or Stone 23, Fire 25, Cloud 27, Storm 29), Gauntlets of Ogre Power (19),
//     Amulet of Health (Constitution 19) and Headband of Intellect (Intelligence 19) set the score while attuned, if it is lower; the
//     score goes back when the attunement ends.

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var plusRe = regexp.MustCompile(`\+\s*([123])\b`)

// itemPlus is the +1/+2/+3 in an item name, or 0.
func itemPlus(name string) int {
	if m := plusRe.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if strings.Contains(strings.ToLower(name), "holy avenger") {
		return 3
	}
	return 0
}

// stripPlus removes "+N" and "Armor," wording from an armor name so its base armor can be looked up ("Plate Armor +1" -> "plate").
func stripPlus(name string) string {
	n := strings.ToLower(plusRe.ReplaceAllString(name, ""))
	n = strings.NewReplacer(",", "", " armor", "").Replace(n)
	return strings.Join(strings.Fields(n), "-")
}

func attunedItemNames(charID int) []string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(attuned_items,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var out []string
	json.Unmarshal(raw, &out)
	return out
}

func attunedTo(charID int, prefix string) (string, bool) {
	for _, n := range attunedItemNames(charID) {
		if strings.HasPrefix(strings.ToLower(n), strings.ToLower(prefix)) {
			return n, true
		}
	}
	return "", false
}

// magicACBonus is what magic items add to a character's AC on top of armor, shield and Dexterity.
func magicACBonus(charID int, equippedArmor string, shield bool) int {
	_, bonus := magicArmorBase(equippedArmor)
	if bonus == 0 {
		bonus = itemPlus(equippedArmor)
	}
	if shield {
		var raw []byte
		db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
		var inv []map[string]interface{}
		json.Unmarshal(raw, &inv)
		best := 0
		for _, it := range inv {
			n := strings.ToLower(asString(it["name"]))
			if strings.HasPrefix(n, "shield") {
				if p := itemPlus(n); p > best {
					best = p
				}
			}
		}
		bonus += best
		if _, ok := itemActive(charID, "shield of the cavalier", "held"); ok {
			bonus += 2 // Shield of the Cavalier: +2 AC more than a Shield (F204)
		}
	}
	if _, ok := attunedTo(charID, "ring of protection"); ok {
		bonus++
	}
	if _, ok := attunedTo(charID, "cloak of protection"); ok {
		bonus++
	}
	if _, ok := attunedTo(charID, "scarab of protection"); ok {
		bonus++ // Scarab of Protection: Defense
	}
	if _, ok := attunedTo(charID, "staff of power"); ok {
		bonus += 2
	}
	if _, ok := attunedTo(charID, "bracers of defense"); ok && equippedArmor == "" && !shield {
		bonus += 2
	}
	return bonus
}

// robeOfArchmagiBase: with the Robe of the Archmagi attuned and no armor, the base AC is 15 + Dexterity modifier (instead of 10).
func robeOfArchmagiBase(charID int, equippedArmor string, base int) int {
	if _, ok := attunedTo(charID, "robe of the archmagi"); ok && equippedArmor == "" && base < 15 {
		return 15
	}
	return base
}

// magicSaveBonus: Ring and Cloak of Protection +1 each, Stone of Good Luck +1, Staff of Power +2.
func magicSaveBonus(charID int) int {
	b := 0
	for _, p := range []struct {
		name string
		n    int
	}{{"ring of protection", 1}, {"cloak of protection", 1}, {"stone of good luck", 1}, {"staff of power", 2}, {"luck blade", 1}} {
		if _, ok := attunedTo(charID, p.name); ok {
			b += p.n
		}
	}
	return b
}

// magicCheckBonus: Stone of Good Luck +1 to ability checks.
func magicCheckBonus(charID int) int {
	if _, ok := attunedTo(charID, "stone of good luck"); ok {
		return 1
	}
	return 0
}

// magicSpellAttackBonus: Wand of the War Mage +N, Staff of Power +2, Staff of the Magi +2, Robe of the Archmagi +2 (the best of the staffs).
func magicSpellAttackBonus(charID int) int {
	b := talismanSpellAttack(charID) // Talisman of Pure Good / Ultimate Evil (F184, items_c8_2024.go)
	if n, ok := attunedTo(charID, "wand of the war mage"); ok {
		b += itemPlus(n)
	}
	staff := 0
	for _, s := range []string{"staff of power", "staff of the magi"} {
		if _, ok := attunedTo(charID, s); ok {
			staff = 2
		}
	}
	b += staff
	if _, ok := attunedTo(charID, "robe of the archmagi"); ok {
		b += 2
	}
	return b
}

// magicSpellDCBonus: Robe of the Archmagi +2.
func magicSpellDCBonus(charID int) int {
	if _, ok := attunedTo(charID, "robe of the archmagi"); ok {
		return 2
	}
	return 0
}

// scoreItems are the attunement items that set an ability score.
func scoreItemFor(name string) (col string, score int) {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "belt of giant strength"):
		for _, g := range []struct {
			k string
			v int
		}{{"storm", 29}, {"cloud", 27}, {"fire", 25}, {"frost", 23}, {"stone", 23}, {"hill", 21}} {
			if strings.Contains(n, g.k) {
				return "str", g.v
			}
		}
		return "str", 21
	case strings.HasPrefix(n, "gauntlets of ogre power"):
		return "str", 19
	case strings.HasPrefix(n, "amulet of health"):
		return "con", 19
	case strings.HasPrefix(n, "headband of intellect"):
		return "intl", 19
	case strings.HasPrefix(n, "belt of dwarvenkind"), strings.HasPrefix(n, "ioun stone") && strings.Contains(n, "fortitude"):
		return "con", 0 // +2, to a maximum of 20 (F204): worked out from the current score
	}
	return "", 0
}

// applyAttunementScore sets the score an attuned item gives (keeping the old score to restore), or restores it when attunement ends.
func applyAttunementScore(charID int, item string, attuned bool) string {
	col, score := scoreItemFor(item)
	if col == "" {
		return ""
	}
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS item_base_scores JSONB DEFAULT '{}'`)
	var raw []byte
	db.QueryRow(`SELECT COALESCE(item_base_scores,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	base := map[string]int{}
	json.Unmarshal(raw, &base)
	var cur int
	db.QueryRow(`SELECT `+col+` FROM characters WHERE id = $1`, charID).Scan(&cur)
	label := map[string]string{"str": "Strength", "con": "Constitution", "intl": "Intelligence"}[col]
	if score == 0 { // Belt of Dwarvenkind, Ioun Stone of Fortitude: +2 to a maximum of 20
		if old, kept := base[col]; kept {
			score = min(20, old+2)
		} else {
			score = min(20, cur+2)
		}
	}
	if attuned {
		if cur >= score {
			return " Your " + label + " is already " + strconv.Itoa(cur) + ", so it has no effect on the score."
		}
		if _, kept := base[col]; !kept {
			base[col] = cur
		}
		b, _ := json.Marshal(base)
		db.Exec(`UPDATE characters SET `+col+` = $1, item_base_scores = $2 WHERE id = $3`, score, b, charID)
		conHitPoints(charID, col, cur, score)
		return " Your " + label + " is now " + strconv.Itoa(score) + "."
	}
	if old, kept := base[col]; kept {
		delete(base, col)
		b, _ := json.Marshal(base)
		db.Exec(`UPDATE characters SET `+col+` = $1, item_base_scores = $2 WHERE id = $3`, old, b, charID)
		conHitPoints(charID, col, cur, old)
		return " Your " + label + " goes back to " + strconv.Itoa(old) + "."
	}
	return ""
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// conHitPoints: when the Constitution modifier changes, the Hit Point maximum changes by that much for every level.
func conHitPoints(charID int, col string, from, to int) {
	if col != "con" {
		return
	}
	d := game.Modifier(to) - game.Modifier(from)
	if d == 0 {
		return
	}
	db.Exec(`UPDATE characters SET max_hp = GREATEST(1, max_hp + $1 * level), hp = GREATEST(0, LEAST(max_hp + $1 * level, hp + $1 * level)) WHERE id = $2`, d, charID)
}

// minInt is the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
