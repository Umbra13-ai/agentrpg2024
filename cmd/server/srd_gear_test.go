package main

import "testing"

func TestCatalogWeightForGearWeaponsAndArmor(t *testing.T) {
	cases := []struct {
		name string
		want float64
	}{
		{"Backpack", 5},
		{"backpack", 5},
		{"Pouch", 1},
		{"scholar's pack", 22},
		{"Scholars Pack", 22},
		{"Rope", 5},
		{"hempen rope", 5},
		{"common clothes", 4},
		{"Clothes, Fine", 6},
		{"torches", 1},
		{"dagger", 1},
		{"Longsword", 3},
		{"Chain Mail", 55},
		{"leather armor", 10},
		{"shield", 6},
		{"Entertainer's Pack", 58.5},
	}
	for _, c := range cases {
		got, ok := catalogWeightFor(c.name)
		if !ok || got != c.want {
			t.Errorf("catalogWeightFor(%q) = %v, %v; want %v", c.name, got, ok, c.want)
		}
	}
}

func TestCatalogWeightMissingForItemsWithoutSRDWeight(t *testing.T) {
	for _, n := range []string{"quill", "letter with unanswered question", "arcane focus"} {
		if w, ok := catalogWeightFor(n); ok && w > 0 {
			t.Errorf("%q should have no SRD weight, got %v", n, w)
		}
	}
}

func TestFillItemWeightsKeepsExplicitAndFillsMissing(t *testing.T) {
	inv := []map[string]interface{}{
		{"name": "Backpack"},
		{"name": "rope", "weight": 0.0},
		{"name": "crowbar", "weight": 9.0},
		{"name": "quill", "weight": 0.0},
	}
	if !fillItemWeights(inv) {
		t.Fatal("expected changes")
	}
	if inv[0]["weight"] != 5.0 || inv[1]["weight"] != 5.0 {
		t.Errorf("backpack/rope not filled: %v %v", inv[0]["weight"], inv[1]["weight"])
	}
	if inv[2]["weight"] != 9.0 {
		t.Errorf("explicit weight should be kept, got %v", inv[2]["weight"])
	}
	if inv[3]["weight"] != 0.0 {
		t.Errorf("quill has no SRD weight and should stay 0, got %v", inv[3]["weight"])
	}
}
