package game

import (
	"testing"
)

func TestHasCondition(t *testing.T) {
	tests := []struct {
		name       string
		conditions []string
		check      string
		want       bool
	}{
		{"empty list", []string{}, "blinded", false},
		{"has condition", []string{"blinded", "prone"}, "blinded", true},
		{"case insensitive", []string{"BLINDED"}, "blinded", true},
		{"no match", []string{"prone"}, "blinded", false},
		{"prefixed condition matches base", []string{"charmed:123"}, "charmed", true},
		{"prefixed exact", []string{"frightened:456"}, "frightened", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasCondition(tt.conditions, tt.check)
			if got != tt.want {
				t.Errorf("HasCondition(%v, %q) = %v, want %v", tt.conditions, tt.check, got, tt.want)
			}
		})
	}
}

func TestIsIncapacitated(t *testing.T) {
	tests := []struct {
		name       string
		conditions []string
		want       bool
	}{
		{"no conditions", []string{}, false},
		{"stunned", []string{"stunned"}, true},
		{"paralyzed", []string{"paralyzed"}, true},
		{"unconscious", []string{"unconscious"}, true},
		{"petrified", []string{"petrified"}, true},
		{"incapacitated", []string{"incapacitated"}, true},
		{"prone only", []string{"prone"}, false},
		{"blinded only", []string{"blinded"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsIncapacitated(tt.conditions)
			if got != tt.want {
				t.Errorf("IsIncapacitated(%v) = %v, want %v", tt.conditions, got, tt.want)
			}
		})
	}
}

func TestAutoFailsSave(t *testing.T) {
	tests := []struct {
		name       string
		conditions []string
		ability    string
		want       bool
	}{
		{"no conditions STR", []string{}, "STR", false},
		{"paralyzed STR", []string{"paralyzed"}, "STR", true},
		{"paralyzed DEX", []string{"paralyzed"}, "DEX", true},
		{"paralyzed WIS", []string{"paralyzed"}, "WIS", false},
		{"unconscious STR", []string{"unconscious"}, "STR", true},
		{"stunned DEX", []string{"stunned"}, "DEX", true},
		{"prone STR", []string{"prone"}, "STR", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AutoFailsSave(tt.conditions, tt.ability)
			if got != tt.want {
				t.Errorf("AutoFailsSave(%v, %q) = %v, want %v", tt.conditions, tt.ability, got, tt.want)
			}
		})
	}
}

func TestIsAutoCrit(t *testing.T) {
	tests := []struct {
		name       string
		conditions []string
		want       bool
	}{
		{"no conditions", []string{}, false},
		{"paralyzed", []string{"paralyzed"}, true},
		{"unconscious", []string{"unconscious"}, true},
		{"stunned", []string{"stunned"}, false},
		{"prone", []string{"prone"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAutoCrit(tt.conditions)
			if got != tt.want {
				t.Errorf("IsAutoCrit(%v) = %v, want %v", tt.conditions, got, tt.want)
			}
		})
	}
}

func TestGetSaveDisadvantage(t *testing.T) {
	tests := []struct {
		name       string
		conditions []string
		exhaustion int
		ability    string
		want       bool
	}{
		{"no conditions", []string{}, 0, "DEX", false},
		{"exhaustion 3 is a penalty, not Disadvantage", []string{}, 3, "WIS", false},
		{"restrained DEX", []string{"restrained"}, 0, "DEX", true},
		{"restrained STR", []string{"restrained"}, 0, "STR", false},
		{"exhaustion 2 no disadv", []string{}, 2, "DEX", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetSaveDisadvantage(tt.conditions, tt.exhaustion, tt.ability)
			if got != tt.want {
				t.Errorf("GetSaveDisadvantage(%v, %d, %q) = %v, want %v", tt.conditions, tt.exhaustion, tt.ability, got, tt.want)
			}
		})
	}
}

func TestGetAttackDisadvantage(t *testing.T) {
	tests := []struct {
		name              string
		conditions        []string
		exhaustion        int
		frightenedVisible bool
		want              bool
	}{
		{"no conditions", []string{}, 0, false, false},
		{"blinded", []string{"blinded"}, 0, false, true},
		{"poisoned", []string{"poisoned"}, 0, false, true},
		{"prone", []string{"prone"}, 0, false, true},
		{"restrained", []string{"restrained"}, 0, false, true},
		{"exhaustion 3 is a penalty, not Disadvantage", []string{}, 3, false, false},
		{"frightened source visible", []string{"frightened"}, 0, true, true},
		{"frightened source not visible", []string{"frightened"}, 0, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAttackDisadvantage(tt.conditions, tt.exhaustion, tt.frightenedVisible)
			if got != tt.want {
				t.Errorf("GetAttackDisadvantage(%v, %d, %v) = %v, want %v", tt.conditions, tt.exhaustion, tt.frightenedVisible, got, tt.want)
			}
		})
	}
}

func TestGetAttackAdvantage(t *testing.T) {
	tests := []struct {
		name       string
		conditions []string
		isMelee    bool
		within5ft  bool
		want       bool
	}{
		{"no conditions", []string{}, true, true, false},
		{"blinded", []string{"blinded"}, true, true, true},
		{"paralyzed", []string{"paralyzed"}, true, true, true},
		{"stunned", []string{"stunned"}, true, true, true},
		{"unconscious", []string{"unconscious"}, true, true, true},
		{"prone melee within 5ft", []string{"prone"}, true, true, true},
		{"prone melee not within 5ft", []string{"prone"}, true, false, false},
		{"prone ranged", []string{"prone"}, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAttackAdvantage(tt.conditions, tt.isMelee, tt.within5ft)
			if got != tt.want {
				t.Errorf("GetAttackAdvantage(%v, %v, %v) = %v, want %v", tt.conditions, tt.isMelee, tt.within5ft, got, tt.want)
			}
		})
	}
}

func TestGetAttackDisadvantageVsTarget(t *testing.T) {
	tests := []struct {
		name       string
		conditions []string
		isRanged   bool
		within5ft  bool
		want       bool
	}{
		{"no conditions", []string{}, false, false, false},
		{"invisible", []string{"invisible"}, false, false, true},
		{"prone ranged not within 5ft", []string{"prone"}, true, false, true},
		{"prone ranged within 5ft", []string{"prone"}, true, true, false},
		{"prone melee", []string{"prone"}, false, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAttackDisadvantageVsTarget(tt.conditions, tt.isRanged, tt.within5ft)
			if got != tt.want {
				t.Errorf("GetAttackDisadvantageVsTarget(%v, %v, %v) = %v, want %v", tt.conditions, tt.isRanged, tt.within5ft, got, tt.want)
			}
		})
	}
}

func TestExhaustionEffects(t *testing.T) {
	tests := []struct {
		level int
		want  string
	}{
		{0, "No exhaustion"},
		{1, "D20 Tests are reduced by 2; Speed is reduced by 5 feet"},
		{3, "D20 Tests are reduced by 6; Speed is reduced by 15 feet"},
		{6, "DEAD (exhaustion level 6)"},
	}

	for _, tt := range tests {
		got := ExhaustionEffects(tt.level)
		if got != tt.want {
			t.Errorf("ExhaustionEffects(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestExhaustion2024(t *testing.T) {
	if ExhaustionD20Penalty(3) != 6 || ExhaustionSpeedPenalty(3) != 15 || ExhaustionD20Penalty(0) != 0 {
		t.Error("penalty is 2 per level to D20 Tests and 5 feet per level of Speed")
	}
	if SpeedZero("stunned") || !SpeedZero("grappled:12") || !SpeedZero("Unconscious") {
		t.Error("Stunned keeps its Speed; Grappled and Unconscious set it to 0")
	}
}
