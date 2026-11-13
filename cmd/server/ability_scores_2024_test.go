package main

import "testing"

// The server uses the Standard Array only (Nov 12): 15, 14, 13, 12, 10 and 8 once each, in any order.
func TestBaseAbilityScores(t *testing.T) {
	ok := []struct {
		method string
		s      []int
	}{
		{"", []int{15, 14, 13, 12, 10, 8}},
		{"standard_array", []int{8, 10, 12, 13, 14, 15}},
		{"standard_array", []int{13, 8, 15, 10, 12, 14}},
	}
	for _, c := range ok {
		if _, _, err := baseAbilityScores(c.method, c.s); err != nil {
			t.Errorf("%s %v refused: %v", c.method, c.s, err)
		}
	}
	bad := []struct {
		method string
		s      []int
	}{
		{"", []int{20, 20, 20, 20, 20, 20}},
		{"", []int{10, 10, 10, 10, 10, 10}},
		{"standard_array", []int{15, 15, 13, 12, 10, 8}},
		{"point_buy", []int{15, 15, 15, 8, 8, 8}},
		{"point_buy", []int{15, 14, 13, 12, 10, 8}},
		{"random", []int{15, 14, 13, 12, 10, 8}},
		{"magic", []int{15, 14, 13, 12, 10, 8}},
	}
	for _, c := range bad {
		if _, _, err := baseAbilityScores(c.method, c.s); err == nil {
			t.Errorf("%s %v accepted", c.method, c.s)
		}
	}
}
