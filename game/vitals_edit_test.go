package game

import (
	"strings"
	"testing"
)

func iptr(n int) *int   { return &n }
func bptr(b bool) *bool { return &b }

// An editor's Hit Points (the GM's update-character, the admin page): one state the rules allow, or a refusal that changes nothing.
func TestVitalsEdit(t *testing.T) {
	alive := Vitals{HP: 30, Max: 40}
	broken := Vitals{HP: 68, Max: 0, Dead: true} // a row written by a raw edit before the rule
	cases := []struct {
		name string
		v    Vitals
		e    VitalsEdit
		want Vitals
		err  string
	}{
		{"hp to 0: dying", alive, VitalsEdit{HP: iptr(0)}, Vitals{HP: 0, Max: 40}, ""},
		{"hp over the maximum", alive, VitalsEdit{HP: iptr(41)}, alive, "0 to the Hit Point maximum"},
		{"a lower maximum takes the Hit Points down", alive, VitalsEdit{Max: iptr(20)}, Vitals{HP: 20, Max: 20}, ""},
		{"maximum 0", alive, VitalsEdit{Max: iptr(0)}, alive, "at least 1"},
		{"killed", alive, VitalsEdit{Dead: bptr(true)}, Vitals{HP: 0, Max: 40, Dead: true}, ""},
		{"dead, given Hit Points", Vitals{Max: 40, Dead: true}, VitalsEdit{HP: iptr(5)}, Vitals{Max: 40, Dead: true}, "is_dead false"},
		{"brought back with hp", Vitals{Max: 40, Dead: true}, VitalsEdit{Dead: bptr(false), HP: iptr(5)}, Vitals{HP: 5, Max: 40}, ""},
		{"brought back without hp: 1", Vitals{Max: 40, Dead: true}, VitalsEdit{Dead: bptr(false)}, Vitals{HP: 1, Max: 40}, ""},
		{"a broken dead row, hp 0 and a maximum", broken, VitalsEdit{HP: iptr(0), Max: iptr(68)}, Vitals{HP: 0, Max: 68, Dead: true}, ""},
		{"saves only while dying", alive, VitalsEdit{Failures: iptr(1)}, alive, "dying"},
		{"saves at 0", Vitals{Max: 40}, VitalsEdit{Failures: iptr(2)}, Vitals{Max: 40, Failures: 2}, ""},
		{"stable at 0", Vitals{Max: 40, Failures: 2}, VitalsEdit{Stable: bptr(true)}, Vitals{Max: 40, Stable: true}, ""},
		{"stable above 0", alive, VitalsEdit{Stable: bptr(true)}, alive, "at 0 Hit Points"},
		{"temporary Hit Points", alive, VitalsEdit{Temp: iptr(5)}, Vitals{HP: 30, Max: 40, Temp: 5}, ""},
		{"healed from 0: saves reset", Vitals{Max: 40, Failures: 2, Stable: true}, VitalsEdit{HP: iptr(10)}, Vitals{HP: 10, Max: 40}, ""},
	}
	for _, c := range cases {
		got, err := c.v.Edit(c.e)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: error %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v, want %+v", c.name, got, err, c.want)
		}
	}
}
