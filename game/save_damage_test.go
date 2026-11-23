package game

import "testing"

// SaveDamage: half on a success, none on a success for an effect without half damage; Evasion only changes an effect that allows half.
func TestSaveDamageAndEvasion(t *testing.T) {
	for _, c := range []struct {
		saved, half, evasion bool
		want                 int
	}{
		{false, true, false, 20}, {true, true, false, 10},
		{false, false, false, 20}, {true, false, false, 0},
		{true, true, true, 0}, {false, true, true, 10}, // Evasion: none on a success, half on a failure
		{false, false, true, 20}, {true, false, true, 0}, // an effect with no half damage: Evasion changes nothing
	} {
		if got := SaveDamage(20, c.saved, c.half, c.evasion); got != c.want {
			t.Errorf("SaveDamage(20, saved %v, half %v, evasion %v) = %d, want %d", c.saved, c.half, c.evasion, got, c.want)
		}
	}
}
