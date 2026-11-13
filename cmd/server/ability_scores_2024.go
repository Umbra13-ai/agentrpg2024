package main

// Generating ability scores (Player's Handbook chapter 2, step 3; the SRD 5.2.1 character creation rules): the Standard Array
// (15, 14, 13, 12, 10, 8), point buy (27 points, every score 8 to 15) or random generation (4d6, drop the lowest, six times).
// These are the scores before the background's ability increases.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var standardArray = []int{15, 14, 13, 12, 10, 8}

// pointBuyCost is the Ability Score Point Costs table.
var pointBuyCost = map[int]int{8: 0, 9: 1, 10: 2, 11: 3, 12: 4, 13: 5, 14: 7, 15: 9}

var abilityOrder = []string{"str", "dex", "con", "int", "wis", "cha"}

func isStandardArray(s []int) bool {
	c := append([]int(nil), s...)
	sort.Sort(sort.Reverse(sort.IntSlice(c)))
	for i := range c {
		if c[i] != standardArray[i] {
			return false
		}
	}
	return true
}

// pointBuyTotal returns the points the scores cost, or -1 if a score is outside 8 to 15.
func pointBuyTotal(s []int) int {
	total := 0
	for _, v := range s {
		c, ok := pointBuyCost[v]
		if !ok {
			return -1
		}
		total += c
	}
	return total
}

// roll4d6DropLowest rolls four d6 and adds the highest three.
func roll4d6DropLowest() (int, []int) {
	d := []int{game.RollDie(6), game.RollDie(6), game.RollDie(6), game.RollDie(6)}
	s := append([]int(nil), d...)
	sort.Ints(s)
	return s[1] + s[2] + s[3], d
}

// baseAbilityScores checks the six scores against the chosen method (in the order str, dex, con, int, wis, cha). With "random" the
// server rolls the six scores and gives the highest to the ability the player ranked highest (by the scores sent), and so on.
// With no method, the Standard Array and any legal point buy are accepted.
func baseAbilityScores(method string, scores []int) ([]int, string, error) {
	method = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(method, " ", "_")))
	help := "This server uses the Standard Array only: 15, 14, 13, 12, 10 and 8, each used once, in any order (point buy and random rolls are not used). These are the scores before your background's increases."
	switch method {
	case "standard_array", "standard", "array":
		if !isStandardArray(scores) {
			return nil, "", fmt.Errorf("the Standard Array is 15, 14, 13, 12, 10 and 8, one to each ability. %s", help)
		}
		return scores, "Standard Array", nil
	case "point_buy", "points", "random", "roll", "rolled", "4d6":
		return nil, "", fmt.Errorf("ability_method %q is not used on this server. %s", method, help)
	case "":
		if isStandardArray(scores) {
			return scores, "Standard Array", nil
		}
		return nil, "", fmt.Errorf("these scores are not the Standard Array. %s", help)
	}
	return nil, "", fmt.Errorf("unknown ability_method %q. %s", method, help)
}
