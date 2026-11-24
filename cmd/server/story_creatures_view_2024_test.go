package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The story and the chronicle show the creatures' tokens beside their lines (story_reader_2024.go, chronicleEntries).
func TestPostgresStoryShowsTheCreatures(t *testing.T) {
	testPostgres(t)
	_, _, camp, _ := storyCreatureFixture(t)
	code, body := pgRawGet(t, fmt.Sprintf("/campaign/%d/story.json", camp), "")
	if code != 200 {
		t.Fatalf("story.json: %d", code)
	}
	var story struct {
		Chapters []storyChapter `json:"chapters"`
	}
	json.Unmarshal(body, &story)
	var shown []storyPassage
	for _, ch := range story.Chapters {
		for _, p := range ch.Entries {
			if len(p.Creatures) > 0 {
				shown = append(shown, p)
			}
		}
	}
	if len(shown) != 3 {
		t.Fatalf("passages with creatures: %d\n%s", len(shown), body)
	}
	first := shown[0].Creatures
	if len(first) != 3 || first[0].Name != "Goblin Warrior" || first[0].Token != "/assets/monster-goblin-warrior.webp" || !first[0].First ||
		!strings.HasPrefix(first[1].Token, "/monster-tokens/") || first[2].Name != "Test Bell Keeper" {
		t.Errorf("the first narration's creatures (one goblin, named twice): %+v", first)
	}
	if turn := shown[1].Creatures; len(turn) != 1 || turn[0].Name != "Test Story Raider" || turn[0].First {
		t.Errorf("the monster's own turn shows it (met already in the chapter: small): %+v", turn)
	}
	if keeper := shown[2].Creatures; len(keeper) != 1 || keeper[0].First {
		t.Errorf("the keeper's second appearance in the chapter is small: %+v", keeper)
	}
	// the chronicle shows them too
	if code, page := pgRawGet(t, fmt.Sprintf("/campaign/%d/log", camp), ""); code != 200 || !strings.Contains(string(page), `alt="Test Bell Keeper" title="Test Bell Keeper"`) {
		t.Errorf("the chronicle: %d", code)
	}
}
