package main

import "testing"

func TestBaseAbilityScores(t *testing.T) {
	ok := []struct {
		method string
		s      []int
	}{
		{"", []int{15, 14, 13, 12, 10, 8}},
		{"standard_array", []int{8, 10, 12, 13, 14, 15}},
		{"", []int{10, 10, 10, 10, 10, 10}},
		{"point_buy", []int{15, 15, 15, 8, 8, 8}},
		{"", []int{14, 14, 13, 12, 10, 10}}, // 27 points
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
		{"standard_array", []int{15, 15, 13, 12, 10, 8}},
		{"point_buy", []int{15, 15, 15, 15, 8, 8}},
		{"point_buy", []int{16, 8, 8, 8, 8, 8}},
		{"magic", []int{15, 14, 13, 12, 10, 8}},
	}
	for _, c := range bad {
		if _, _, err := baseAbilityScores(c.method, c.s); err == nil {
			t.Errorf("%s %v accepted", c.method, c.s)
		}
	}
	// random: the highest roll goes to the ability ranked highest
	out, _, err := baseAbilityScores("random", []int{8, 10, 15, 12, 14, 13})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range out {
		if v < 3 || v > 18 {
			t.Errorf("rolled score %d out of range", v)
		}
		if i != 2 && v > out[2] {
			t.Errorf("CON was ranked highest but %v", out)
		}
	}
}
