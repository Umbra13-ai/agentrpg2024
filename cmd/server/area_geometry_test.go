package main

import "testing"

// F58: Cones and Lines point toward the aim and don't reach behind the caster.
func TestConeAndLineGeometry(t *testing.T) {
	caster := mapToken{ID: 1, Position: mapSquare{5, 5}, Size: 1}
	aim := mapToken{ID: -1, Position: mapSquare{8, 5}, Size: 1} // 15 feet east
	at := func(x, y int) mapToken { return mapToken{ID: -2, Position: mapSquare{x, y}, Size: 1} }
	// a 15-foot Cone east: the squares ahead widen; nothing behind or beyond 15 feet
	for _, c := range []struct {
		x, y int
		want bool
	}{{6, 5, true}, {7, 4, true}, {7, 6, true}, {8, 5, true}, {6, 3, false}, {4, 5, false}, {5, 7, false}, {10, 5, false}} {
		if got := tokenInLineOrCone("cone", caster, aim, at(c.x, c.y), 15, 0); got != c.want {
			t.Errorf("15-foot Cone east, square (%d,%d): got %v, want %v", c.x, c.y, got, c.want)
		}
	}
	// a 30-foot, 5-foot-wide Line east: only the row ahead, up to 30 feet
	for _, c := range []struct {
		x, y int
		want bool
	}{{6, 5, true}, {11, 5, true}, {12, 5, false}, {7, 6, false}, {4, 5, false}} {
		if got := tokenInLineOrCone("line", caster, aim, at(c.x, c.y), 30, 5); got != c.want {
			t.Errorf("30-foot Line east, square (%d,%d): got %v, want %v", c.x, c.y, got, c.want)
		}
	}
	// a Huge dragon's Line starts at the edge of its space
	dragon := mapToken{ID: -9, Position: mapSquare{10, 4}, Size: 3}
	west := mapToken{ID: 2, Position: mapSquare{4, 5}, Size: 1}
	if !tokenInLineOrCone("line", dragon, west, at(8, 5), 90, 5) || tokenInLineOrCone("line", dragon, west, at(8, 8), 90, 5) {
		t.Error("the dragon's 90-foot Line west")
	}
	if lineWidthFeet("a 60-foot-long, 10-foot-wide Line") != 10 || lineWidthFeet("a Line 100 feet long") != 5 || lineWidthFeet("a 90-foot-long, 10-footwide Line") != 10 {
		t.Error("lineWidthFeet")
	}
	if !monsterLineRe.MatchString("each creature in a 90-foot-long, 5-footwide Line.") {
		t.Error("the SRD's \"5-footwide\" Line")
	}
}
