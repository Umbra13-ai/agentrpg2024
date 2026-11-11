package main

// F194: Metamagic options that printed a note and spent Sorcery Points but changed nothing (SRD 5.2.1, Sorcerer, Metamagic Options):
//	Careful Spell     up to your Charisma modifier (minimum one) of the characters the spell makes save succeed automatically, and take no
//	                  damage where a success takes half (resolveSpellOnCharacters)
//	Distant Spell     the spell's range doubles; a range of Touch becomes 30 feet (the battle-map range check)
//	Empowered Spell   up to your Charisma modifier (minimum one) of the damage dice are rerolled (the lowest that came up below
//	                  average); the new rolls stand
//	Extended Spell    a duration of 1 minute or longer doubles (24 hours at most); Advantage on saves to keep Concentration on it
//	Heightened Spell  one target (the first named) has Disadvantage on its saves against the spell

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

var (
	carefulLeft      sync.Map // charID -> how many more creatures Careful Spell protects in the cast being resolved
	heightenedTarget sync.Map // charID -> the creature with Disadvantage on its saves against the spell being cast
	extendedCast     sync.Map // charID -> true while a cast with Extended Spell resolves
	extendedConc     sync.Map // charID -> the Concentration spell cast with Extended Spell
)

// careful: the next character the spell makes save is protected by Careful Spell (and counted).
func careful(charID int) bool {
	v, ok := carefulLeft.Load(charID)
	if !ok || v.(int) <= 0 {
		return false
	}
	carefulLeft.Store(charID, v.(int)-1)
	return true
}

func heightenedDisadvantage(charID, id int) bool {
	v, ok := heightenedTarget.Load(charID)
	return ok && v.(int) == id
}

// extendRounds doubles a duration of 1 minute (10 rounds) or longer while an Extended cast resolves; 24 hours at most.
func extendRounds(charID, rounds int) int {
	if _, ok := extendedCast.Load(charID); ok && rounds >= 10 {
		rounds *= 2
		if rounds > 14400 {
			rounds = 14400
		}
	}
	return rounds
}

// extendedConcentration: the character concentrates on a spell cast with Extended Spell (Advantage on the saves to keep it).
func extendedConcentration(charID int) bool {
	v, ok := extendedConc.Load(charID)
	if !ok {
		return false
	}
	var on string
	db.QueryRow(`SELECT COALESCE(concentrating_on,'') FROM characters WHERE id = $1`, charID).Scan(&on)
	return on != "" && strings.EqualFold(strings.TrimSpace(on), strings.TrimSpace(v.(string)))
}

// distantRange: the spell with its range doubled (Touch: 30 feet).
func distantRange(sp SRDSpell) SRDSpell {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(sp.Range)), "touch") {
		sp.Range = "30 feet"
	} else if f := spellRangeFeet(sp.Range); f >= 5 {
		sp.Range = fmt.Sprintf("%d feet", 2*f)
	}
	return sp
}

var diceTermRe = regexp.MustCompile(`([+-]?)\s*(\d+)(?:d(\d+))?`)

// rollEmpowered rolls a damage expression ("8d6", "10d6+40") die by die and rerolls up to n of the dice that came up below their
// average, lowest first; the new rolls stand. It returns the total and a note.
func rollEmpowered(dice string, n int) (int, string) {
	type die struct{ sides, v int }
	var dies []die
	flat := 0
	for _, m := range diceTermRe.FindAllStringSubmatch(strings.ToLower(dice), -1) {
		count, _ := strconv.Atoi(m[2])
		sign := 1
		if m[1] == "-" {
			sign = -1
		}
		if m[3] == "" {
			flat += sign * count
			continue
		}
		sides, _ := strconv.Atoi(m[3])
		for i := 0; i < count; i++ {
			dies = append(dies, die{sides, game.RollDie(sides)})
		}
	}
	order := make([]int, len(dies))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return 2*dies[order[a]].v-dies[order[a]].sides < 2*dies[order[b]].v-dies[order[b]].sides
	})
	var changes []string
	for _, i := range order {
		if len(changes) >= n || 2*dies[i].v >= dies[i].sides+1 {
			break
		}
		old := dies[i].v
		dies[i].v = game.RollDie(dies[i].sides)
		changes = append(changes, fmt.Sprintf("%d→%d", old, dies[i].v))
	}
	total := flat
	for _, d := range dies {
		total += d.v
	}
	if total < 0 {
		total = 0
	}
	if len(changes) == 0 {
		return total, " [Empowered Spell: no die below average to reroll]"
	}
	return total, fmt.Sprintf(" [Empowered Spell: %d dice rerolled (%s)]", len(changes), strings.Join(changes, ", "))
}
