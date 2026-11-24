package main

// I-ALDENQA-1: an imported entry that calls the creature he or she is flagged in the import report (the server reads the SRD's "it", so its
// duration is never listed as an open item); an entry in the SRD's wording is not.

import (
	"fmt"
	"strings"
	"testing"
)

func TestImportReportFlagsHeAndShe(t *testing.T) {
	m := inventedMonster("Test Bell Witch", "Fey", []srdMonsterEntry{{Name: "Ghostly", Text: "Until the start of his next turn, the witch has Resistance to all damage."}},
		[]srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 5 (1d6 + 2) Slashing damage."}})
	m.Reactions = []srdMonsterEntry{{Name: "Slip", Text: "Trigger: A creature hits her with an attack roll. Response: She teleports up to 15 feet."}}
	w := fmt.Sprint(customMonsterReport(m, nil, false)["warnings"])
	if !strings.Contains(w, `Ghostly ("his")`) || !strings.Contains(w, `Slip ("her")`) || strings.Contains(w, "Claw") || !strings.Contains(w, "Write it and its") {
		t.Errorf("the warning: %s", w)
	}
	m.Traits[0].Text = "Until the start of its next turn, the witch has Resistance to all damage."
	m.Reactions = nil
	if w := personalPronounWarning(m); w != "" {
		t.Errorf("SRD wording flagged: %s", w)
	}
}
