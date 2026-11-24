package game

import "testing"

// LaterDisadvantage: a one-d20 roll keeps the lower of two and is judged again; a roll with Disadvantage (or cancelled) stays as it was;
// a roll with Advantage is not the server's to settle.
func TestLaterDisadvantage(t *testing.T) {
	if _, ok := LaterDisadvantage(15, 20, 12, "advantage"); ok {
		t.Error("Advantage then Disadvantage: which d20 counts is not the rule's")
	}
	if o, ok := LaterDisadvantage(15, 20, 12, "disadvantage"); !ok || o.Natural != 15 || o.Total != 20 || !o.Hit || o.Dice[1] != 0 {
		t.Errorf("already Disadvantage: %+v", o)
	}
	for i := 0; i < 200; i++ {
		o, ok := LaterDisadvantage(15, 20, 12, "")
		if !ok || o.Natural > 15 || o.Natural != min(15, o.Dice[1]) || o.Total != 5+o.Natural || o.Mode != "disadvantage" {
			t.Fatalf("one d20: %+v", o)
		}
		if o.Hit != (o.Natural != 1 && o.Total >= 12) || o.Critical {
			t.Fatalf("judged again: %+v", o)
		}
	}
	if o, _ := LaterDisadvantage(20, 25, 30, "disadvantage"); !o.Hit || !o.Critical {
		t.Errorf("a natural 20 hits: %+v", o)
	}
}
