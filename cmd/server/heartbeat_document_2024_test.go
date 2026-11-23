package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Playtest S-GM-19 and I-KESTREL-5: the heartbeat's campaign document is the one summary my-turn reads, bounded, with its time.
func TestHeartbeatDocumentIsTheSummaryMyTurnReads(t *testing.T) {
	long := strings.Repeat("The party walks on. ", 300) + "They make camp on the Elturel road, 150 miles from the city."
	doc := map[string]interface{}{
		"chapter": "Chapter 1: Greenest in Flames", "round": 2, "npcs": []interface{}{map[string]interface{}{"name": "Leosin"}},
		"story_so_far": long, "story_so_far_updated_at": "2026-11-22T20:00:00Z"}
	got := heartbeatDocument(doc, 7)
	for _, stale := range []string{"chapter", "round", "npcs"} {
		if _, has := got[stale]; has {
			t.Errorf("the heartbeat carries the stale field %q", stale)
		}
	}
	raw, _ := json.Marshal(doc)
	mine := parseStorySoFar(raw)
	if !strings.HasSuffix(mine, "150 miles from the city.") || !strings.HasSuffix(got["story_so_far"].(string), "150 miles from the city.") {
		t.Errorf("the heartbeat's summary doesn't end where my-turn's does: %q", got["story_so_far"])
	}
	if n := len(strings.Fields(got["story_so_far"].(string))); n > heartbeatStory+1 || got["story_so_far_cut"] != true || got["story_so_far_words"] != len(strings.Fields(mine)) {
		t.Errorf("not bounded or not marked cut: %d words, %v", n, got["story_so_far_cut"])
	}
	if got["story_so_far_updated_at"] != "2026-11-22T20:00:00Z" || got["current_scene"].(map[string]interface{})["ends_with"] != "They make camp on the Elturel road, 150 miles from the city" {
		t.Errorf("when and where: %v", got)
	}
	if b, _ := json.Marshal(got); len(b) > 2500 {
		t.Errorf("the heartbeat document is %d bytes", len(b))
	}
	// no summary yet: still a valid, honest document
	empty := heartbeatDocument(map[string]interface{}{}, 7)
	if empty["story_so_far"] != "" || !strings.Contains(empty["story_so_far_updated_at"].(string), "unknown") {
		t.Errorf("an empty document: %v", empty)
	}
}
