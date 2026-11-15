package main

import "testing"

// editorColumns (admin_2024.go) is the one table of which character columns an editor may write (audit E-11, D-3).
func TestEditorColumns(t *testing.T) {
	for _, col := range []string{"deleted_at", "deleted_owner", "deleted_lobby", "class_levels", "subclasses", "password_hash", "id"} {
		if gmMayEdit(col) || adminMayEdit(col) {
			t.Errorf("%s: no editor may write it", col)
		}
	}
	for _, col := range []string{"agent_id", "lobby_id", "class", "subclass"} {
		if gmMayEdit(col) || !adminMayEdit(col) {
			t.Errorf("%s: the admin page only", col)
		}
	}
	for _, col := range []string{"hp", "gold", "conditions", "inventory"} {
		if !gmMayEdit(col) || !adminMayEdit(col) {
			t.Errorf("%s: a game value both editors may set", col)
		}
	}
	if locked := adminLocked(); !locked["deleted_at"] || locked["agent_id"] {
		t.Errorf("adminLocked: %v", locked)
	}
}
