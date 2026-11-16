package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHitDiceSuggestion(t *testing.T) {
	if n, avg := hitDiceSuggestion(10, 30, 5, 10, 2); n != 3 || avg != 7.5 {
		t.Errorf("20 missing with d10+2 (7.5 each): 3 dice, got %d (%.1f)", n, avg)
	}
	if n, _ := hitDiceSuggestion(10, 30, 2, 10, 2); n != 2 {
		t.Error("never more than are left")
	}
	if n, _ := hitDiceSuggestion(30, 30, 5, 10, 2); n != 0 {
		t.Error("none at full Hit Points")
	}
	if _, avg := hitDiceSuggestion(1, 30, 5, 6, -4); avg != 1 {
		t.Error("each die heals at least 1")
	}
}

// A Short Rest through the server (SRD 5.2.1): its guide says how to spend Hit Point Dice; the rest spends what it is sent; more can
// be spent one at a time right after it, deciding after each roll; not once something else has happened; not at 0 Hit Points.
func TestPostgresShortRestHitDice(t *testing.T) {
	testPostgres(t)
	_, tok := pgAgent(t, "pg rester")
	c, j := pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Rester %d", time.Now().UnixNano()%1e9)))
	if c != 200 {
		t.Fatalf("create: %d %v", c, j)
	}
	id := int(j["character_id"].(float64))
	pgExec(t, `UPDATE characters SET level = 5, max_hp = 40, hp = 10, hit_dice_spent = 0 WHERE id = $1`, id)
	base := fmt.Sprintf("/api/characters/%d", id)

	_, j = pgCall(t, "GET", base+"/short-rest", tok, nil)
	rest, _ := j["rest"].(map[string]interface{})
	short, _ := rest["short_rest"].(map[string]interface{})
	dice, _ := short["hit_dice"].(map[string]interface{})
	if dice["left"] != float64(5) || dice["suggested"] == float64(0) || short["how"] == nil {
		t.Fatalf("the rest guide says how many Hit Point Dice are left, a suggestion and how to spend them: %v", rest)
	}
	if c, j := pgCall(t, "POST", base+"/hit-dice", tok, map[string]interface{}{"count": 1}); c != 409 || j["error"] != "not_after_short_rest" {
		t.Errorf("Hit Point Dice are spent when a Short Rest finishes, not any time: %d %v", c, j)
	}
	c, j = pgCall(t, "POST", base+"/short-rest", tok, map[string]interface{}{"hit_dice": 1})
	if c != 200 || j["hit_dice_spent"] != float64(1) || j["hit_dice_remaining"] != float64(4) {
		t.Fatalf("the rest spends the dice it is sent: %d %v", c, j)
	}
	for i := 0; i < 2; i++ {
		c, j = pgCall(t, "POST", base+"/hit-dice", tok, map[string]interface{}{"count": 1})
		if c != 200 || j["hit_dice_remaining"] != float64(3-i) || len(j["rolls"].([]interface{})) != 1 {
			t.Fatalf("one more die right after the rest, deciding after each roll (%d): %d %v", i, c, j)
		}
	}
	if c, j := pgCall(t, "POST", base+"/hit-dice", tok, map[string]interface{}{"count": 9}); c != 400 || j["error"] != "not_enough_hit_dice" {
		t.Errorf("never more than are left: %d %v", c, j)
	}
	writeAction(db, 0, id, "search", "I look around", "")
	if c, j := pgCall(t, "POST", base+"/hit-dice", tok, nil); c != 409 {
		t.Errorf("once something else has happened, the rest is over: %d %v", c, j)
	}
	pgExec(t, `UPDATE characters SET hp = 0 WHERE id = $1`, id)
	if c, j := pgCall(t, "POST", base+"/short-rest", tok, map[string]interface{}{}); c != 409 || j["error"] != "needs_1_hit_point" {
		t.Errorf("a Short Rest needs at least 1 Hit Point: %d %v", c, j)
	}
}

// An agent learns how to keep its token before it registers (Matt, Nov 16: agents on the test server weren't ready to save it):
// the start says so ahead of the register request everywhere it is shown, and GET /api/register says it too.
func TestTokenPreparationComesBeforeRegistering(t *testing.T) {
	for name, text := range map[string]string{"skill.md / llms.txt": startText(nil), "home page": startHTML()} {
		before, reg := strings.Index(text, "get ready to save your token"), strings.Index(text, "/api/register")
		if before < 0 || reg < 0 || before > reg {
			t.Errorf("%s: the token preparation comes before the register request (%d, %d)", name, before, reg)
		}
	}
	if startJSON()["before_register"] != beforeRegisterNote {
		t.Error("GET /api/ says what to know before registering")
	}
	rec := httptest.NewRecorder()
	handleRegister(rec, httptest.NewRequest(http.MethodGet, "/api/register", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "get ready to save your token") {
		t.Errorf("GET /api/register says how to prepare: %d %s", rec.Code, rec.Body.String())
	}
}
