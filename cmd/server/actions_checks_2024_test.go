package main

import "testing"

func TestPickSkill2024Actions(t *testing.T) {
	cases := []struct {
		choices []skillChoice
		def     string
		desc    string
		want    string
	}{
		{searchSkills, "perception", "I search the room for hidden doors", "perception"},
		{searchSkills, "perception", "Is the merchant lying to us?", "insight"},
		{searchSkills, "perception", "I look at the body to see what killed him", "medicine"},
		{searchSkills, "perception", "I look for tracks in the mud", "survival"},
		{searchSkills, "perception", "search with insight", "insight"},
		{studySkills, "investigation", "I study the runes on the door", "arcana"},
		{studySkills, "investigation", "What do I remember about the war of the three crowns?", "history"},
		{studySkills, "investigation", "I study the mechanism of the trap", "investigation"},
		{studySkills, "investigation", "What do I know about this dragon?", "nature"},
		{studySkills, "investigation", "I recall what I know about these undead", "religion"},
		{studySkills, "investigation", "I am aware of something odd here", "investigation"}, // "aware" is not "war"
		{studySkills, "investigation", "I write down the symbols", "investigation"},        // "write" is not "rite"
		{influenceSkills, "persuasion", "I try to convince the guard to let us pass", "persuasion"},
		{influenceSkills, "persuasion", "I threaten the bandit", "intimidation"},
		{influenceSkills, "persuasion", "I tell the troll a joke", "performance"},
		{influenceSkills, "persuasion", "I bluff that we are city watch", "deception"},
		{influenceSkills, "persuasion", "I gently coax the frightened horse", "animal handling"},
		{influenceSkills, "persuasion", "I put on my mask and ask nicely", "persuasion"},
	}
	for _, c := range cases {
		if got := pickSkill(c.desc, c.choices, c.def).skill; got != c.want {
			t.Errorf("%q: got %s, want %s", c.desc, got, c.want)
		}
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
		"I swing the flat of my blade to knock him out":     true,
		"nonlethal punch":                                  true,
		"I attack to subdue the thief":                     true,
		"I attack with my longsword":                       false,
		"cunning strike knock out":                         false, // the Rogue's Cunning Strike effect, not a nonlethal attack
		"cunning strike poison, but I want to take him alive": true,
	} {
		if got := wantsKnockOut(desc); got != want {
			t.Errorf("%q: got %v, want %v", desc, got, want)
		}
	}
}
