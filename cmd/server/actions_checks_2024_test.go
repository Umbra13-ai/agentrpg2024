package main

import (
	"strings"
	"testing"
)

func TestPickSkill2024Actions(t *testing.T) {
	cases := []struct {
		action string
		def    string
		desc   string
		want   string
	}{
		{"search", "perception", "I search the room for hidden doors", "perception"},
		{"search", "perception", "Is the merchant lying to us?", "insight"},
		{"search", "perception", "I look at the body to see what killed him", "medicine"},
		{"search", "perception", "I look for tracks in the mud", "survival"},
		{"search", "perception", "search with insight", "insight"},
		{"study", "investigation", "I study the runes on the door", "arcana"},
		{"study", "investigation", "What do I remember about the war of the three crowns?", "history"},
		{"study", "investigation", "I study the mechanism of the trap", "investigation"},
		{"study", "investigation", "What do I know about this dragon?", "nature"},
		{"study", "investigation", "I recall what I know about these undead", "religion"},
		{"study", "investigation", "I am aware of something odd here", "investigation"}, // "aware" is not "war"
		{"study", "investigation", "I write down the symbols", "investigation"},         // "write" is not "rite"
		{"influence", "persuasion", "I try to convince the guard to let us pass", "persuasion"},
		{"influence", "persuasion", "I threaten the bandit", "intimidation"},
		{"influence", "persuasion", "I tell the troll a joke", "performance"},
		{"influence", "persuasion", "I bluff that we are city watch", "deception"},
		{"influence", "persuasion", "I gently coax the frightened horse", "animal handling"},
		{"influence", "persuasion", "I put on my mask and ask nicely", "persuasion"},
		{"study", "investigation", "I recall what I know about the Cult of the Dragon and Rezmir", "religion"}, // feedback log 45: the first keyword decides
	}
	for _, c := range cases {
		if got, how, refused := checkSkillFor(c.action, c.desc, ""); got.skill != c.want || how == "" || refused != "" {
			t.Errorf("%q: got %s, want %s", c.desc, got, c.want)
		}
	}
}

// A named skill decides, for every skill of each check action; a skill from another action's table is refused (feedback log 45).
func TestCheckSkillNamed(t *testing.T) {
	for action, tbl := range checkSkills {
		for _, c := range tbl.list {
			got, how, refused := checkSkillFor(action, "a description that names nothing", c.skill)
			if got.skill != c.skill || !strings.Contains(how, "you named") || refused != "" {
				t.Errorf("%s with %s: %s %q %q", action, c.skill, got.skill, how, refused)
			}
		}
	}
	if _, _, refused := checkSkillFor("study", "x", "perception"); !strings.HasPrefix(refused, "Cannot use Study") {
		t.Errorf("a Search skill for Study: %q", refused)
	}
	if got, _, _ := checkSkillFor("study", "x", "Animal Handling"); got.skill != "" {
		t.Errorf("Animal Handling for Study: %v", got)
	}
}

func TestCanonicalAction(t *testing.T) {
	for in, want := range map[string]string{"Magic": "cast", " utilize ": "use_item", "Study": "study", "attack": "attack"} {
		if got := canonicalAction(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestWantsKnockOut(t *testing.T) {
	for desc, want := range map[string]bool{
		"I swing the flat of my blade to knock him out": true,
		"nonlethal punch":                                     true,
		"I attack to subdue the thief":                        true,
		"I attack with my longsword":                          false,
		"cunning strike knock out":                            false, // the Rogue's Cunning Strike effect, not a nonlethal attack
		"cunning strike poison, but I want to take him alive": true,
	} {
		if got := wantsKnockOut(desc); got != want {
			t.Errorf("%q: got %v, want %v", desc, got, want)
		}
	}
}
