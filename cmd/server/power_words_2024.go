package main

// Power Word Kill, Power Word Stun and Power Word Heal (SRD 5.2.1), and the Bard's Words of Creation (level 20).
//   Kill (level 9): the target dies if it has 100 Hit Points or fewer; otherwise it takes 12d12 Psychic damage. No saving throw.
//   Stun (level 8): the target has the Stunned condition if it has 150 Hit Points or fewer (it repeats a Constitution save at the end
//     of each of its turns); otherwise its Speed is 0 until the start of the caster's next turn.
//   Heal (level 9): the target regains all its Hit Points; Charmed, Frightened, Paralyzed, Poisoned and Stunned end; a Prone target
//     can use its Reaction to stand up.
//   Words of Creation (Bard 20): Power Word Heal and Power Word Kill are always prepared, and a cast of either can target a second
//     creature within 10 feet of the first (target_ids; on a battle map the distance is checked).
// Before this the server treated Power Word Kill as plain 12d12 Psychic damage, and Stun and Heal as text only.

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var powerWordEnds = []string{"charmed", "frightened", "paralyzed", "poisoned", "stunned"}

// wordsOfCreation: a level 20 Bard casting Power Word Heal or Power Word Kill.
func wordsOfCreation(charID int, spellKey string) bool {
	if spellKey != "power-word-kill" && spellKey != "power-word-heal" {
		return false
	}
	var class string
	var level int
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,0) FROM characters WHERE id = $1`, charID).Scan(&class, &level)
	return strings.EqualFold(class, "bard") && level >= 20
}

func charClassOf(charID int) string {
	var class string
	db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&class)
	return class
}

// wordsOfCreationSpells are the spells Words of Creation keeps prepared for a level 20 Bard.
func wordsOfCreationSpells(class string, level int) []string {
	if strings.EqualFold(class, "bard") && level >= 20 {
		return []string{"power-word-heal", "power-word-kill"}
	}
	return nil
}

// resolvePowerWord applies a Power Word to its target(s); "" when the spell isn't one.
func resolvePowerWord(charID int, spellKey string, saveDC int, description string) string {
	if spellKey != "power-word-kill" && spellKey != "power-word-stun" && spellKey != "power-word-heal" {
		return ""
	}
	lobby := lobbyOfChar(charID)
	var targets []int
	seen := map[int]bool{}
	add := func(id int) {
		if id != 0 && !seen[id] {
			seen[id] = true
			targets = append(targets, id)
		}
	}
	add(attackTargetID(charID))
	for _, id := range spellMonsterTargets(charID) {
		add(id)
	}
	for _, id := range spellCharacterTargets(charID) {
		add(id)
	}
	if len(targets) == 0 && spellKey == "power-word-heal" {
		if id := parseTargetFromDescription(description, charID); id > 0 {
			add(id)
		} else {
			add(charID)
		}
	}
	if len(targets) == 0 {
		return " Name the target (target_id; monsters have negative ids)."
	}
	limit, note := 1, ""
	if wordsOfCreation(charID, spellKey) {
		limit = 2
	}
	if len(targets) > limit {
		note = fmt.Sprintf(" (%s affects %d creature%s; the other targets aren't affected.)", spellTitle(spellKey), limit, map[bool]string{true: "", false: "s"}[limit == 1])
		targets = targets[:limit]
	}
	if len(targets) == 2 {
		if m, ok := activeEncounterMap(lobby); ok {
			a, ok1 := m.token(targets[0])
			b, ok2 := m.token(targets[1])
			if ok1 && ok2 && tokenDistanceFeet(a, b) > 10 {
				note += " Words of Creation: the second creature is more than 10 feet from the first, so only the first is affected."
				targets = targets[:1]
			}
		}
		if len(targets) == 2 {
			note += " Words of Creation: the spell also reaches a second creature within 10 feet of the first."
		}
	}
	var lines []string
	for _, id := range targets {
		lines = append(lines, powerWordOn(lobby, spellKey, id, saveDC))
	}
	return " " + strings.Join(lines, " ") + note
}

func spellTitle(slug string) string {
	if sp, ok := srdSpellsMemory[slug]; ok {
		return sp.Name
	}
	return slug
}

// powerWordOn applies one Power Word to one creature (a monster's negative id or a character).
func powerWordOn(lobby int, spellKey string, id, saveDC int) string {
	var name string
	var hp, maxHP int
	if id < 0 {
		for _, e := range combatEntries(lobby) {
			if asInt(e["id"]) == id {
				name, hp, maxHP = fmt.Sprint(e["name"]), asInt(e["hp"]), asInt(e["max_hp"])
			}
		}
	} else {
		db.QueryRow(`SELECT name, hp, max_hp FROM characters WHERE id = $1`, id).Scan(&name, &hp, &maxHP)
	}
	if name == "" {
		return "The target isn't in this fight."
	}
	if hp <= 0 && spellKey != "power-word-heal" {
		return name + " is already down."
	}
	switch spellKey {
	case "power-word-kill":
		if hp <= 100 {
			if id < 0 {
				setMonsterHP(lobby, id, 0)
			} else {
				db.Exec(`UPDATE characters SET hp = 0, is_dead = true, is_stable = false WHERE id = $1`, id)
				endWildShapeIfDown(id)
			}
			return name + " dies (it had 100 Hit Points or fewer)."
		}
		dmg := game.RollDamage("12d12", false)
		if id < 0 {
			text, _ := damageMonster(lobby, id, dmg, "psychic")
			if text == "" {
				text = fmt.Sprintf("%s takes %d psychic damage.", name, dmg)
			}
			return name + " has more than 100 Hit Points: " + text
		}
		return fmt.Sprintf("%s has more than 100 Hit Points and %s.", name, damageCharacter(id, dmg, "psychic"))
	case "power-word-stun":
		if hp <= 150 {
			addMonsterCondition(lobby, id, "stunned", "Power Word Stun", 0, "con", saveDC)
			return fmt.Sprintf("%s has the Stunned condition (150 Hit Points or fewer); it makes a DC %d Constitution saving throw at the end of each of its turns, ending it on a success.", name, saveDC)
		}
		return name + " has more than 150 Hit Points: its Speed is 0 until the start of your next turn."
	case "power-word-heal":
		ended := []string{}
		for _, c := range powerWordEnds {
			if id < 0 {
				res, _ := db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = $3`, lobby, id, c)
				if n, _ := res.RowsAffected(); n > 0 {
					ended = append(ended, titleCase(c))
				}
			} else {
				had := false
				for _, cur := range getCharConditions(id) {
					had = had || conditionBase(strings.ToLower(cur)) == c
				}
				db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND condition = $3`, lobby, id, c)
				if removeCondition(id, c) || had {
					ended = append(ended, titleCase(c))
				}
			}
		}
		if id < 0 {
			setMonsterHP(lobby, id, maxHP)
		} else {
			_, before, after := healCharacter(id, maxHP)
			defer func() { _ = lifeBond(id, after-before) }() // Life Bond (the steed regains as many)
		}
		text := fmt.Sprintf("%s regains all its Hit Points (%d → %d).", name, hp, maxHP)
		if len(ended) > 0 {
			text += " The " + joinNames(ended) + " condition ends."
		}
		prone := false
		if id > 0 {
			for _, cur := range getCharConditions(id) {
				prone = prone || strings.EqualFold(cur, "prone")
			}
		} else {
			for _, c := range conditionNames(lobby, id) {
				prone = prone || c == "prone"
			}
		}
		if prone {
			text += " It can use its Reaction to stand up from Prone."
		}
		return text
	}
	return ""
}
