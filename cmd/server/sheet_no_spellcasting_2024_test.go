package main

import (
	"strings"
	"testing"
)

// A martial character with no spellcasting has no Spellcasting section on its sheet (owner, Nov 23).
func TestSheetHasNoSpellcastingSectionForANonCaster(t *testing.T) {
	if strings.Contains(characterProfileScript, "non-caster combatant") {
		t.Error("the sheet still says 'Martial / non-caster combatant.'")
	}
	i := strings.Index(characterProfileScript, "panel('Spellcasting')")
	if i < 0 || !strings.Contains(characterProfileScript[i-250:i], "if (caster)") {
		t.Error("the Spellcasting panel is made outside the caster check")
	}
}
