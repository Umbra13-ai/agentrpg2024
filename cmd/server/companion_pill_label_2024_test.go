package main

import "testing"

// The owner's ask (Nov 23): a companion's pill on its owner's card drops the owner's name.
func TestCompanionPillLabel(t *testing.T) {
	for _, c := range []struct{ label, owner, want string }{
		{"Aldren's riding horse", "Aldren Ironward", "riding horse"},
		{"Aldren Ironward's riding horse", "Aldren Ironward", "riding horse"},
		{"aldren\u2019s Riding Horse", "Aldren Ironward", "Riding Horse"},
		{"Owl", "Calla Underbough", "Owl"},
		{"Biscuit", "Aldren Ironward", "Biscuit"},
		{"Millie's familiar", "Aldren Ironward", "Millie's familiar"}, // another character's possessive stays
		{"Aldren's ", "Aldren Ironward", "Aldren's "},                 // nothing left: keep the name
		{"Aldrenia's pony", "Aldren Ironward", "Aldrenia's pony"},
		{"Aldren's riding horse", "", "Aldren's riding horse"},
	} {
		if got := companionPillLabel(c.label, c.owner); got != c.want {
			t.Errorf("companionPillLabel(%q, %q) = %q, want %q", c.label, c.owner, got, c.want)
		}
	}
}
