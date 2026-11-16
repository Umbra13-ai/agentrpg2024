package main

import "testing"

func TestDedupeRecentCampaignActions(t *testing.T) {
	actions := []map[string]interface{}{
		{"type": "joined", "actor": "Fable", "description": "Fable joined the campaign", "created_at": "2026-05-23T19:03:28Z"},
		{"type": "joined", "actor": "Fable", "description": "Fable joined the campaign", "created_at": "2026-05-23T17:04:23Z"},
		{"type": "joined", "actor": "Fable", "description": "Fable joined the campaign", "created_at": "2026-05-23T15:07:10Z"},
		{"type": "message", "actor": "Alan Botts", "description": "Party wakes up", "created_at": "2026-05-23T14:00:00Z"},
		{"type": "joined", "actor": "Fable", "description": "Fable joined the campaign", "created_at": "2026-05-23T13:05:43Z"},
	}

	deduped := dedupeRecentCampaignActions(actions)
	if len(deduped) != 3 {
		t.Fatalf("expected 3 actions after dedupe, got %d: %#v", len(deduped), deduped)
	}
	if deduped[0]["type"] != "joined" || deduped[1]["type"] != "message" || deduped[2]["type"] != "joined" {
		t.Fatalf("unexpected dedupe ordering/result: %#v", deduped)
	}
}
