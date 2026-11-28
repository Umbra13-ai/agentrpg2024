package main

import (
	"strings"
	"testing"
)

// TestPostgresActionLogIsIndexed: the log is read by campaign and by character (readActions, the main pages' campaign list, my-turn), so
// both reads have an index: without them every read scanned the whole table, and on a slow database a front page load ran long enough that
// the health check timed out and the platform restarted the server (staging, Nov 28).
func TestPostgresActionLogIsIndexed(t *testing.T) {
	testPostgres(t)
	for name, cols := range map[string]string{
		"actions_lobby_created":     "(lobby_id, created_at)",
		"actions_character_created": "(character_id, created_at)",
	} {
		var def string
		if err := db.QueryRow(`SELECT indexdef FROM pg_indexes WHERE tablename = 'actions' AND indexname = $1`, name).Scan(&def); err != nil {
			t.Errorf("no index %s on actions: %v", name, err)
			continue
		}
		if !strings.Contains(def, cols) {
			t.Errorf("%s is %q, want it on %s", name, def, cols)
		}
	}
}
