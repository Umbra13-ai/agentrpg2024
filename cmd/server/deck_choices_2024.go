package main

// The Mysterious Deck's cards with a choice or a lasting effect (SRD 5.2.1; F61, Nov 8, 2026). The draw (legendary_items_2024.go) leaves
// the choice cards pending on the deck; the player settles each with use_item:
//   "balance: +dex -str" (or "balance: none")   +2 to one score (22 at most), -2 to another (not one of 5 or lower)
//   "star: wisdom"                              +2 to a score, 24 at most
//   "throne: persuasion"                        proficiency and Expertise in History, Insight, Intimidation or Persuasion
//   "puzzle: intelligence" (or wisdom)          that score drops by the rolled 1d4 + 1 (to 1 at least)
//   "jester: advantage" or "jester: draw"       Advantage on D20 Tests for 72 hours, or two more cards ("draw 2")
// The Fool gives Disadvantage on D20 Tests for 72 hours, the Jester's choice Advantage (attack rolls and saving throws the server rolls;
// 72 hours count as three dawns). The Moon leaves a "Moon card: Wish" entry with 1d3 charges to cast Wish from.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var abilityCols = map[string]string{"str": "str", "strength": "str", "dex": "dex", "dexterity": "dex", "con": "con", "constitution": "con",
	"int": "intl", "intelligence": "intl", "wis": "wis", "wisdom": "wis", "cha": "cha", "charisma": "cha"}

var abilityLabels = map[string]string{"str": "Strength", "dex": "Dexterity", "con": "Constitution", "intl": "Intelligence", "wis": "Wisdom", "cha": "Charisma"}

var deckChoiceRe = regexp.MustCompile(`(?i)^\s*(balance|star|throne|puzzle|jester)\b[:\s]*(.*)$`)

// pendingCards: the choice cards waiting on the deck ("Star", "Puzzle:4").
func pendingCards(it invItem) []string {
	var out []string
	if list, ok := it["pending_cards"].([]interface{}); ok {
		for _, c := range list {
			out = append(out, fmt.Sprint(c))
		}
	}
	return out
}

// deckCardPending: what the draw does with a card that waits for a choice or lasts; ok is false for other cards.
func deckCardPending(charID int, card string) (pending, line string, ok bool) {
	switch card {
	case "Balance":
		return "Balance", " [Choose: use_item \"balance: +dex -str\" (raise one score by 2, to 22 at most, and lower another by 2; not one of 5 or lower), or \"balance: none\".]", true
	case "Star":
		return "Star", " [Choose: use_item \"star: wisdom\" (+2, to 24 at most).]", true
	case "Throne":
		return "Throne", " [Choose: use_item \"throne: persuasion\" (History, Insight, Intimidation or Persuasion: proficiency and Expertise). The keep and its monsters are the GM's.]", true
	case "Puzzle":
		n := game.RollDie(4) + 1
		return "Puzzle:" + strconv.Itoa(n), fmt.Sprintf(" [The reduction is %d: choose use_item \"puzzle: intelligence\" or \"puzzle: wisdom\". You may draw one more card beyond your declared draws (\"draw 1\").]", n), true
	case "Jester":
		return "Jester", " [Choose: use_item \"jester: advantage\" (Advantage on D20 Tests for 72 hours) or \"jester: draw\" (two more cards: then \"draw 2\").]", true
	case "Fool":
		addDeckBoonN(charID, "fool", 3)
		return "", " [Server: Disadvantage on your D20 Tests for 72 hours (three dawns): attack rolls and the saving throws the server rolls. You draw another card; it doesn't count as a declared draw.]", true
	case "Moon":
		n := game.RollDie(3)
		editInventory(charID, func(inv []invItem) []invItem {
			for _, x := range inv { // a second Moon adds to the wishes already there
				if itemStr(x, "name") == "Moon card: Wish" && !itemBool(x, "destroyed") {
					x["charges"] = int(itemNum(x, "charges")) + n
					x["charges_max"] = int(itemNum(x, "charges_max")) + n
					return inv
				}
			}
			return append(inv, invItem{"id": newItemID("w"), "name": "Moon card: Wish", "cast_spells": []interface{}{"wish"}, "charges": n, "charges_max": n, "location": "worn"})
		})
		return "", fmt.Sprintf(" [Server: you can cast Wish %d time(s): {\"action\": \"cast\", \"spell_slug\": \"wish\", \"item\": \"Moon card: Wish\"}.]", n), true
	case "Donjon":
		addDeckBoon(charID, "donjon")
		addMonsterCondition(lobbyOfChar(charID), charID, "incapacitated", "Donjon", 0, "", 0) // entombed: no actions until found (O-4)
		return "", " [Server: you disappear, entombed in suspended animation in an extradimensional sphere; you are Incapacitated and out of the scene until you're found. Everything you wear and carry stays behind. The GM decides where the sphere is; when you're freed, the GM removes the condition (POST /api/gm/conditions, remove incapacitated). You draw no more cards.]", true
	case "Key":
		weapon := keyCardWeapon(charID)
		if charID > 0 && db != nil {
			editInventory(charID, func(inv []invItem) []invItem {
				return append(inv, invItem{"id": newItemID("k"), "name": weapon, "magic_item": "Weapon, +1, +2, or +3", "quantity": 1,
					"note": "The Key card: a Rare magic weapon you're proficient with; the GM may replace it with another Rare or rarer one."})
			})
		}
		return "", fmt.Sprintf(" [Server: a %s (Rare) appears on your person: equip it like any weapon. The GM may choose another Rare or rarer magic weapon you're proficient with instead.]", weapon), true
	case "Flames":
		addDeckBoon(charID, "flames")
		return "", " [Server: recorded on your character: a powerful devil is now your enemy and seeks your ruin until you or it dies. The GM plays the devil.]", true
	case "Rogue":
		addDeckBoon(charID, "rogue")
		return "", " [Server: recorded on your character: a creature the GM chooses is now hostile to you, and you don't know who. Only a Wish spell or divine intervention ends it.]", true
	case "Fates":
		addDeckBoon(charID, "fates")
		return "", " [Server: recorded: tell the GM when you use it to avoid or erase one event, before you die.]", true
	case "Comet":
		addDeckBoon(charID, "comet")
		return "", " [Server: recorded: at your next fight against Hostile creatures, name your foe to the GM; if you reduce it to 0 Hit Points you have Advantage on Death Saving Throws for 1 year.]", true
	case "Sage":
		addDeckBoon(charID, "sage")
		return "", " [Server: recorded: within one year, ask the GM your question; the answer is truthful.]", true
	}
	return "", "", false
}

// keyCardWeapon: the Key card's weapon: a +2 version (Rare) of the weapon in the character's hand, or a Dagger +2 (every class is
// proficient with Simple weapons).
func keyCardWeapon(charID int) string {
	if db == nil || charID <= 0 {
		return "Dagger +2"
	}
	var main string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, charID).Scan(&main)
	base := strings.TrimSpace(plusRe.ReplaceAllString(main, ""))
	if base == "" || namedWeaponFor(main) != nil || strings.HasPrefix(strings.ToLower(main), "rod of lordly might") {
		base = "Dagger"
	}
	return base + " +2"
}

// deckEffectText: the lasting Mysterious Deck effects on a character, for its sheet and the GM view.
var deckEffectText = map[string]string{
	"euryale": "Euryale's curse: -2 to saving throws",
	"sun":     "the Sun: 10 Temporary Hit Points at each dawn",
	"donjon":  "the Donjon: entombed in an extradimensional sphere until found (Incapacitated)",
	"void":    "the Void: the soul is trapped in an object the GM chose; the body is Incapacitated",
	"fates":   "the Fates: can avoid or erase one event, once",
	"comet":   "the Comet: defeat the named foe of the next fight for Advantage on Death Saving Throws for a year",
	"sage":    "the Sage: one truthful answer from the GM within a year",
	"flames":  "the Flames: a powerful devil is an enemy",
	"rogue":   "the Rogue: an unknown creature is hostile",
	"fool":    "the Fool: Disadvantage on D20 Tests",
	"jester":  "the Jester: Advantage on D20 Tests",
}

// deckEffects lists a character's lasting Mysterious Deck effects in words (nil when none).
func deckEffects(charID int) []string {
	if charID <= 0 || db == nil {
		return nil
	}
	ensureDeckBoons()
	var b string
	db.QueryRow(`SELECT COALESCE(deck_boons,'') FROM characters WHERE id = $1`, charID).Scan(&b)
	var out []string
	for _, x := range strings.Split(b, ",") {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		key, n := x, ""
		if i := strings.Index(x, ":"); i > 0 {
			key, n = x[:i], x[i+1:]
		}
		t := deckEffectText[key]
		if t == "" {
			t = key
		}
		if n != "" && (key == "fool" || key == "jester") {
			t += fmt.Sprintf(" (%s more dawn(s))", n)
		}
		out = append(out, t)
	}
	return out
}

// resolveDeckChoice settles a pending card from the use_item description. ok is false when the description isn't a choice.
func resolveDeckChoice(charID int, it invItem, description string) (string, bool) {
	m := deckChoiceRe.FindStringSubmatch(description)
	if m == nil {
		return "", false
	}
	card := titleCase(strings.ToLower(m[1]))
	arg := strings.ToLower(strings.TrimSpace(m[2]))
	pend := pendingCards(it)
	idx := -1
	for i, p := range pend {
		if strings.HasPrefix(p, card) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Sprintf("Cannot settle the %s card: you have no %s waiting from the Mysterious Deck. Nothing changed.", card, card), true
	}
	var out string
	switch card {
	case "Balance":
		if strings.Contains(arg, "none") {
			out = "Balance: you leave your ability scores as they are."
			break
		}
		up, down := balanceAbilities(arg)
		if up == "" || down == "" || up == down {
			return "Cannot settle Balance: say which score goes up and which goes down, \"balance: +dex -str\" (or \"balance: none\"). Nothing changed.", true
		}
		cu, cd := abilityScoreCol(charID, up), abilityScoreCol(charID, down)
		if cd <= 5 {
			return fmt.Sprintf("Cannot lower your %s: it is %d (a score of 5 or lower can't be decreased). Nothing changed.", abilityLabels[down], cd), true
		}
		nu := cu + 2
		if nu > 22 {
			nu = 22
		}
		setAbilityScore(charID, up, nu)
		setAbilityScore(charID, down, cd-2)
		out = fmt.Sprintf("Balance: your %s is %d (from %d) and your %s %d (from %d).", abilityLabels[up], nu, cu, abilityLabels[down], cd-2, cd)
	case "Star":
		col := abilityFromText(arg)
		if col == "" {
			return "Cannot settle the Star: name the score, \"star: wisdom\". Nothing changed.", true
		}
		cur := abilityScoreCol(charID, col)
		n := cur + 2
		if n > 24 {
			n = 24
		}
		setAbilityScore(charID, col, n)
		out = fmt.Sprintf("Star: your %s is %d (from %d; 24 at most).", abilityLabels[col], n, cur)
	case "Throne":
		skill := ""
		for _, s := range []string{"history", "insight", "intimidation", "persuasion"} {
			if strings.Contains(arg, s) {
				skill = s
			}
		}
		if skill == "" {
			return "Cannot settle the Throne: choose History, Insight, Intimidation or Persuasion, \"throne: persuasion\". Nothing changed.", true
		}
		var profs, exp string
		db.QueryRow(`SELECT COALESCE(skill_proficiencies,''), COALESCE(expertise,'') FROM characters WHERE id = $1`, charID).Scan(&profs, &exp)
		db.Exec(`UPDATE characters SET skill_proficiencies = $1, expertise = $2 WHERE id = $3`, addListWord(profs, skill), addListWord(exp, skill), charID)
		out = fmt.Sprintf("Throne: you have proficiency and Expertise in %s. You also own a small keep, home to monsters for now (the GM's).", titleCase(skill))
	case "Puzzle":
		n := atoiSafe(strings.TrimPrefix(pend[idx], "Puzzle:"))
		col := abilityFromText(arg)
		if col != "intl" && col != "wis" {
			return "Cannot settle the Puzzle: choose Intelligence or Wisdom, \"puzzle: wisdom\". Nothing changed.", true
		}
		cur := abilityScoreCol(charID, col)
		v := cur - n
		if v < 1 {
			v = 1
		}
		setAbilityScore(charID, col, v)
		out = fmt.Sprintf("Puzzle: your %s drops permanently to %d (from %d).", abilityLabels[col], v, cur)
	case "Jester":
		switch {
		case strings.Contains(arg, "adv"):
			addDeckBoonN(charID, "jester", 3)
			out = "Jester: you have Advantage on D20 Tests for 72 hours (three dawns): attack rolls and the saving throws the server rolls."
		case strings.Contains(arg, "draw"):
			out = "Jester: draw your two extra cards with use_item \"draw 2\"."
		default:
			return "Cannot settle the Jester: say \"jester: advantage\" or \"jester: draw\". Nothing changed.", true
		}
	}
	pend = append(pend[:idx], pend[idx+1:]...)
	updateInventoryItem(charID, it, func(x invItem) { x["pending_cards"] = pend })
	return out, true
}

var balanceUpRe = regexp.MustCompile(`\+\s*([a-z]+)|([a-z]+)\s*(?:up|\+2)`)
var balanceDownRe = regexp.MustCompile(`-\s*([a-z]+)|([a-z]+)\s*(?:down|-2)`)

func balanceAbilities(arg string) (up, down string) {
	if m := balanceUpRe.FindStringSubmatch(arg); m != nil {
		up = abilityCols[m[1]+m[2]]
	}
	if m := balanceDownRe.FindStringSubmatch(arg); m != nil {
		down = abilityCols[m[1]+m[2]]
	}
	return
}

func abilityFromText(arg string) string {
	for _, w := range strings.FieldsFunc(arg, func(r rune) bool { return r == ' ' || r == ',' || r == ':' }) {
		if c, ok := abilityCols[w]; ok {
			return c
		}
	}
	return ""
}

func abilityScoreCol(charID int, col string) int {
	var v int
	db.QueryRow(`SELECT `+col+` FROM characters WHERE id = $1`, charID).Scan(&v)
	return v
}

// setAbilityScore changes a score; a new Constitution modifier changes the Hit Point maximum by the level for each point of modifier.
func setAbilityScore(charID int, col string, v int) {
	if col == "con" {
		var old, level int
		db.QueryRow(`SELECT con, level FROM characters WHERE id = $1`, charID).Scan(&old, &level)
		if d := (game.Modifier(v) - game.Modifier(old)) * level; d != 0 {
			db.Exec(`UPDATE characters SET max_hp = GREATEST(1, max_hp + $1), hp = GREATEST(0, LEAST(max_hp + $1, hp + $1)) WHERE id = $2`, d, charID)
		}
	}
	db.Exec(`UPDATE characters SET `+col+` = $1 WHERE id = $2`, v, charID)
}

func addListWord(list, word string) string {
	for _, w := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(w), word) {
			return list
		}
	}
	if strings.TrimSpace(list) == "" {
		return word
	}
	return list + ", " + word
}

// ---- lasting boons with a dawn countdown ("fool:3")

func addDeckBoonN(charID int, boon string, dawns int) {
	ensureDeckBoons()
	var b string
	db.QueryRow(`SELECT COALESCE(deck_boons,'') FROM characters WHERE id = $1`, charID).Scan(&b)
	var keep []string
	for _, x := range strings.Split(b, ",") {
		if x != "" && x != boon && !strings.HasPrefix(x, boon+":") {
			keep = append(keep, x)
		}
	}
	keep = append(keep, fmt.Sprintf("%s:%d", boon, dawns))
	db.Exec(`UPDATE characters SET deck_boons = $1 WHERE id = $2`, strings.Join(keep, ","), charID)
}

// deckDawn counts a dawn off the timed boons (the Fool's and the Jester's 72 hours).
func deckDawn(charID int) []string {
	ensureDeckBoons()
	var b string
	db.QueryRow(`SELECT COALESCE(deck_boons,'') FROM characters WHERE id = $1`, charID).Scan(&b)
	if !strings.Contains(b, ":") {
		return nil
	}
	var keep, notes []string
	for _, x := range strings.Split(b, ",") {
		if i := strings.Index(x, ":"); i > 0 {
			n := atoiSafe(x[i+1:]) - 1
			if n <= 0 {
				notes = append(notes, "the "+titleCase(x[:i])+" card's 72 hours are over")
				continue
			}
			x = fmt.Sprintf("%s:%d", x[:i], n)
		}
		if x != "" {
			keep = append(keep, x)
		}
	}
	db.Exec(`UPDATE characters SET deck_boons = $1 WHERE id = $2`, strings.Join(keep, ","), charID)
	return notes
}

// deckD20: the Fool's Disadvantage or the Jester's Advantage on the character's D20 Tests.
func deckD20(charID int) (adv, dis bool) {
	if charID <= 0 {
		return false, false
	}
	return deckBoon(charID, "jester"), deckBoon(charID, "fool")
}

func deckAdvantage(charID int) bool {
	a, _ := deckD20(charID)
	return a
}

func deckDisadvantage(charID int) bool {
	_, d := deckD20(charID)
	return d
}
