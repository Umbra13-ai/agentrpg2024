package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rec(id int, typ, actor, desc, res string, at time.Time, char int, byPlayer bool) actionRecord {
	return actionRecord{ID: id, Type: typ, Actor: actor, Description: desc, Result: res, At: at, CharacterID: char, ByPlayer: byPlayer}
}

// The story reader (story_reader_2024.go): which records are the story and in whose voice, and where a chapter begins.
func TestStoryPassagesAndChapters(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	min := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Minute) }
	recs := []actionRecord{
		rec(1, "narration", "GM", "Smoke rises above the hills of Greenest, and a blue wing banks over the keep.", "", min(0), 0, false),
		rec(2, "declare", "Calla", "Calla rides for the keep.", "", min(1), 7, true),
		rec(3, "attack", "Aldren", "Aldren swings at the kobold.", "Axe 21 vs AC 13 - HIT, 9 slashing damage", min(2), 8, true),
		rec(4, "attack", "Aldren", "Aldren swings again.", "Axe 3 vs AC 13 - MISS", min(3), 8, true),
		rec(5, "monster_Bite", "GM", "Kobold: it snaps at Calla.", "Kobold bites Calla: 12 vs AC 15 - HIT", min(4), 0, false),
		rec(6, "combat_end", "GM", "The fight is over. The GM narrates what follows.", "", min(5), 0, false),
		rec(7, "narration", "GM", "Silence falls over the road, broken only by the crackle of the burning town.", "", min(6), 0, false),
		rec(8, "message", "Calla", "ooc: brb", "", min(7), 7, true),         // party chat is not story
		rec(9, "poll", "Calla", "Checked game status", "", min(8), 7, true), // nor is bookkeeping
		rec(10, "environment", "GM", "The area is brightly lit", "Lighting set", min(8), 0, false),
		rec(11, "narration", "GM", "The next morning the party wakes in the keep's cellar.", "", min(60*9), 0, false), // 9 hours later: a new chapter
	}
	ps := storyPassages(recs, map[int]string{7: "☀️", 8: "🪓"})
	// the party chat, the poll and the lighting are not in the story, and the fight's end is a mark, not a passage: 11 records, 7 passages
	if len(ps) != 7 {
		t.Fatalf("passages: %d", len(ps))
	}
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "e1,e2,e3,e4,e5,e7,e11" {
		t.Fatalf("the story is %v", ids)
	}
	byID := map[string]storyPassage{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	if p := byID["e2"]; p.Type != "pc" || p.Speaker != "Calla" || p.Icon != "☀️" || p.Text != "Calla rides for the keep." {
		t.Errorf("a character's words: %+v", p)
	}
	if p := byID["e3"]; p.Dice == "" || p.Tone != "hit" || byID["e4"].Tone != "fail" {
		t.Errorf("a roll's chip: %+v / %+v", p, byID["e4"])
	}
	if p := byID["e5"]; p.Type != "gm" || p.Speaker != "Kobold" || p.Text != "it snaps at Calla." || p.Dice == "" {
		t.Errorf("a monster's turn: %+v", p)
	}
	chs := storyChapters(ps, time.UTC)
	if len(chs) != 3 {
		t.Fatalf("chapters: %d (a fight's end and a nine-hour pause each begin one)", len(chs))
	}
	if len(chs[0].Entries) != 5 || len(chs[1].Entries) != 1 || chs[0].Num != "Chapter I" || chs[2].Num != "Chapter III" {
		t.Errorf("the chapters: %d / %d passages, %q ... %q", len(chs[0].Entries), len(chs[1].Entries), chs[0].Num, chs[2].Num)
	}
	if chs[0].Title != "Monday morning" || chs[2].Title != "Monday evening" || !strings.Contains(chs[0].ReadTime, "5 passages") {
		t.Errorf("a chapter is named by when it began: %q / %q, %q", chs[0].Title, chs[2].Title, chs[0].ReadTime)
	}
	if !chs[0].Entries[0].Opener || chs[0].Entries[1].Opener {
		t.Errorf("the drop cap opens the chapter's first narration only")
	}
	// a long unbroken scene is cut so no chapter is a wall
	var long []storyPassage
	for i := 0; i < storyChapterMax+5; i++ {
		long = append(long, storyPassage{ID: fmt.Sprintf("x%d", i), Type: "gm", Text: "word", at: t0.Add(time.Duration(i) * time.Second)})
	}
	if c := storyChapters(long, time.UTC); len(c) != 2 || len(c[0].Entries) != storyChapterMax {
		t.Errorf("a long scene: %d chapters", len(c))
	}
	if roman(4) != "IV" || roman(14) != "XIV" || roman(1994) != "MCMXCIV" {
		t.Errorf("numerals: %s %s %s", roman(4), roman(14), roman(1994))
	}
}

// GET /campaign/{id}/story reads the real log: the narration and the players' words, not the party chat or the bookkeeping, escaped (a narration
// that holds </script> can't leave its page), and a campaign with no story says so. A private campaign stays behind its password.
func TestPostgresStoryReaderPage(t *testing.T) {
	testPostgres(t)
	gm, toks, camp, chars, _ := pgFightWith(t, "story", 1)
	_ = chars
	get := func(path string) (int, string) {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "10.9.1.1:1234"
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	base := fmt.Sprintf("/campaign/%d/story", camp)
	if c, _ := get("/campaign/99999999/story"); c != 404 {
		t.Errorf("no such campaign: %d", c)
	}
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": `The gate groans open. <script>alert(1)</script> "Welcome," says the keeper.`})
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/messages", camp), toks[0], map[string]interface{}{"message": "ooc: be right back, dinner"})
	c, page := get(base)
	if c != 200 || !strings.Contains(page, "reader-shell") || !strings.Contains(page, "window.__STORY__") {
		t.Fatalf("the reader: %d", c)
	}
	if !strings.Contains(page, "The gate groans open.") {
		t.Errorf("the narration is not in the story")
	}
	if strings.Contains(page, "dinner") {
		t.Errorf("the party chat is in the story")
	}
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Errorf("a narration's markup is on the page as markup")
	}
	// the Live Table and the chronicle link to it
	if _, live := get(fmt.Sprintf("/campaign/%d", camp)); !strings.Contains(live, base) {
		t.Errorf("the Live Table does not link the story")
	}
	if _, log := get(fmt.Sprintf("/campaign/%d/log", camp)); !strings.Contains(log, base) {
		t.Errorf("the chronicle does not link the story")
	}
	// a campaign with nothing in its log
	_, gm2 := pgAgent(t, "story empty gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm2, map[string]interface{}{"name": "Pg Story Empty", "max_players": 2})
	empty := int(j["campaign_id"].(float64))
	if c, page := get(fmt.Sprintf("/campaign/%d/story", empty)); c != 200 || !strings.Contains(page, `"chapters":[]`) || !strings.Contains(page, "The story has not begun yet") {
		t.Errorf("an empty story: %d", c)
	}
	// a private campaign is behind its watch password
	if err := setWatchPassword(camp, "open sesame 1"); err != nil {
		t.Fatal(err)
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/table-rules", camp), gm, map[string]interface{}{"visibility": "private"}); c != 200 {
		t.Fatalf("private: %d %v", c, j)
	}
	if c, page := get(base); strings.Contains(page, "The gate groans open.") {
		t.Errorf("a private campaign's story was served without the password: %d", c)
	}
}
