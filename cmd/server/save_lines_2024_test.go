package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Every message about a saving throw is worded by the owner (game.SaveResult: Line, Clause, LineAgainst, ForSaver), which names
// each source of Advantage or Disadvantage, what was added and an automatic result. HANDOFF item 24: creatureSave and the
// monster save wrappers returned only (success, total), so ~60 messages wrote "saves (14 vs DC 15)" themselves and never said why
// a save had Advantage. No helper hands back only the two numbers, and no message writes a save's total against its DC by hand.
func TestNoSaveLineByHand(t *testing.T) {
	byHand := regexp.MustCompile(`Sprintf\(.*(save|Save)[^"]*%[dv][^"]*vs DC|(save|saves|fails)[^"]*\(%d\)`)
	twoNumbers := regexp.MustCompile(`return \w+\.Success, \w+\.Total\b`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if byHand.MatchString(line) {
				t.Errorf("%s:%d writes a saving throw's result by hand: use the SaveResult's Line, Clause, LineAgainst or ForSaver", f, n+1)
			}
			if twoNumbers.MatchString(line) {
				t.Errorf("%s:%d hands back only a save's success and total: return the game.SaveResult", f, n+1)
			}
		}
	}
}

// A save made through creatureSave says why it had Disadvantage or failed automatically, and the players' view keeps those
// words while it hides the numbers.
func TestPostgresCreatureSaveSaysWhy(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg saver")
	c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Saver %d", time.Now().UnixNano()%1e9)))
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	var lobby int
	if err := db.QueryRow(`INSERT INTO lobbies (name, dm_id, status) VALUES ($1, $2, 'active') RETURNING id`, fmt.Sprintf("Saves %d", id), id).Scan(&lobby); err != nil {
		t.Fatal(err)
	}
	pgExec(t, `UPDATE characters SET lobby_id = $1 WHERE id = $2`, lobby, id)

	applyCondition(lobby, id, "restrained", "a net", 0, "", 0)
	sv := creatureSave(lobby, id, "dex", 15)
	line := sv.LineAgainst("Pg Saver")
	if !strings.Contains(line, "with Disadvantage (Restrained)") || !strings.Contains(line, "against DC 15") {
		t.Errorf("a Restrained character's Dexterity save doesn't say why it had Disadvantage: %q", line)
	}
	if seen := playerText(line); strings.Contains(seen, fmt.Sprint(sv.Total)+" vs") || !strings.Contains(seen, "Disadvantage (Restrained)") {
		t.Errorf("the players' view of %q is %q: the numbers go, the reason stays", line, seen)
	}

	applyCondition(lobby, id, "paralyzed", "a spell", 0, "", 0)
	if line := creatureSave(lobby, id, "dex", 15).Line("Pg Saver"); !strings.Contains(line, "fails the Dexterity saving throw automatically (Paralyzed)") {
		t.Errorf("a Paralyzed character's Dexterity save doesn't say it failed automatically: %q", line)
	}
}
