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
	help := "Generate the scores with the Standard Array (15, 14, 13, 12, 10, 8 in any order), point buy (27 points; every score 8 to 15; costs 8:0 9:1 10:2 11:3 12:4 13:5 14:7 15:9) or \"ability_method\": \"random\" (the server rolls 4d6, drops the lowest, six times, and gives the highest roll to your highest score, and so on). These are the scores before your background's increases."
	switch method {
	case "standard_array", "standard", "array":
		if !isStandardArray(scores) {
			return nil, "", fmt.Errorf("the Standard Array is 15, 14, 13, 12, 10 and 8, one to each ability. %s", help)
		}
		return scores, "Standard Array", nil
	case "point_buy", "points":
		t := pointBuyTotal(scores)
		if t < 0 {
			return nil, "", fmt.Errorf("with point buy every score is 8 to 15. %s", help)
		}
		if t > 27 {
			return nil, "", fmt.Errorf("those scores cost %d points; point buy has 27. %s", t, help)
		}
		return scores, fmt.Sprintf("point buy (%d of 27 points)", t), nil
	case "random", "roll", "rolled", "4d6":
		rank := []int{0, 1, 2, 3, 4, 5}
		sort.SliceStable(rank, func(a, b int) bool { return scores[rank[a]] > scores[rank[b]] })
		rolls := make([]int, 6)
		var detail []string
		for i := range rolls {
			v, d := roll4d6DropLowest()
			rolls[i] = v
			detail = append(detail, fmt.Sprintf("%v=%d", d, v))
		}
		sorted := append([]int(nil), rolls...)
		sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
		out := make([]int, 6)
		for i, ab := range rank {
			out[ab] = sorted[i]
		}
		var assigned []string
		for i, ab := range abilityOrder {
			assigned = append(assigned, fmt.Sprintf("%s %d", strings.ToUpper(ab), out[i]))
		}
		return out, "rolled 4d6 drop lowest: " + strings.Join(detail, ", ") + " → " + strings.Join(assigned, ", "), nil
	case "":
		if isStandardArray(scores) {
			return scores, "Standard Array", nil
		}
		if t := pointBuyTotal(scores); t >= 0 && t <= 27 {
			return scores, fmt.Sprintf("point buy (%d of 27 points)", t), nil
		}
		return nil, "", fmt.Errorf("these scores are neither the Standard Array nor a legal point buy. %s", help)
	}
	return nil, "", fmt.Errorf("unknown ability_method %q. %s", method, help)
}
