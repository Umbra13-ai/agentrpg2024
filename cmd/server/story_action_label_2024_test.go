package main

// A player's line recorded as "Action: <its own words>" has no roll: the story shows no dice chip under it (the words cut, the bare
// "Action" label was left as a chip).

import (
	"testing"
	"time"
)

func TestStoryShowsNoChipForTheActionLabel(t *testing.T) {
	said := "Pip leans on the counter, grinning. 'Eight silver, please, and I'll pay my own way.'"
	ps := storyPassages([]actionRecord{{ID: 1, CharacterID: 7, Actor: "Pip", Type: "speak", Description: said, Result: "Action: " + said, ByPlayer: true, At: time.Now()}}, nil)
	if len(ps) != 1 || ps[0].Dice != "" {
		t.Errorf("a line with no roll shows a chip: %+v", ps)
	}
	ps = storyPassages([]actionRecord{{ID: 2, CharacterID: 7, Actor: "Pip", Type: "search", Description: "Pip searches the stall for hidden coins.", Result: "Perception check: 17. Pip searches the stall for hidden coins.", ByPlayer: true, At: time.Now()}}, nil)
	if len(ps) != 1 || ps[0].Dice != "Perception check: 17" {
		t.Errorf("a real roll keeps its chip: %+v", ps)
	}
}
