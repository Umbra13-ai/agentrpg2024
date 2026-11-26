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
	ps := storyPassages(recs, map[int]storySpeaker{7: {Icon: "☀️"}, 8: {Icon: "🪓", Portrait: "/api/characters/8/portrait?v=1"}})
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
	// each character's token beside its words: a portrait when it has one, its class icon otherwise
	for _, p := range ps {
		if p.Type == "pc" && p.Icon == "🪓" && p.Portrait != "/api/characters/8/portrait?v=1" {
			t.Errorf("character 8's portrait is not its token: %+v", p)
		}
		if p.Type == "pc" && p.Icon == "☀️" && p.Portrait != "" {
			t.Errorf("character 7 has no portrait: %+v", p)
		}
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
	spell := storyPassages([]actionRecord{rec(20, "cast", "Calla", "Calla casts Hunter's Mark.", strings.Repeat("The spell's whole description. ", 30), t0, 7, true)}, nil)
	if len(spell) != 1 || len([]rune(spell[0].Dice)) > storyDiceMax+1 {
		t.Errorf("a roll chip is a roll, not a spell's text: %d characters", len([]rune(spell[0].Dice)))
	}
	said := "Kneeling beside the first unconscious kobold, I search it thoroughly for anything I missed: pouches, coins and insignia."
	if got := rollOnly("🔍 Search (Perception/WIS) (expertise): 11 (d20: 7 + 0 WIS + 4 prof). "+said[:70]+" and I whisper to Calla...", said); got != "🔍 Search (Perception/WIS) (expertise): 11 (d20: 7 + 0 WIS + 4 prof)" {
		t.Errorf("a roll chip holds the roll, not the sentence again: %q", got)
	}
	if got := rollOnly("Axe 21 vs AC 13 - HIT, 9 slashing damage", "Aldren swings at the kobold."); got != "Axe 21 vs AC 13 - HIT, 9 slashing damage" {
		t.Errorf("a result that does not repeat the passage is kept: %q", got)
	}
	if p := byID["e5"]; p.Type != "gm" || p.Speaker != "Kobold" || p.Text != "it snaps at Calla." || p.Dice == "" {
		t.Errorf("a monster's turn: %+v", p)
	}
	chs := storyChapters(ps, time.UTC)
	if len(chs) != 3 {
		t.Fatalf("chapters: %d (a fight's end and a nine-hour pause each begin one)", len(chs))
	}
	if len(chs[0].Entries) != 5 || len(chs[1].Entries) != 1 || chs[0].Num != "Part 1" || chs[2].Num != "Part 3" {
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
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "A second beat, told after the first."})
	c, page := get(base)
	if c != 200 || !strings.Contains(page, "reader-shell") || !strings.Contains(page, "window.__STORY__") {
		t.Fatalf("the reader: %d", c)
	}
	if !strings.Contains(page, "The gate groans open.") {
		t.Errorf("the narration is not in the story")
	}
	// chronological: the first thing narrated comes first, and a story longer than the cap is cut at its end, not its start
	if first, second := strings.Index(page, "The gate groans open."), strings.Index(page, "A second beat"); first < 0 || second < first {
		t.Errorf("the story is not in the order it was told: %d, %d", first, second)
	}
	defer func(n int) { storyMaxRecords = n }(storyMaxRecords)
	storyMaxRecords = 1
	if _, cut := get(base); !strings.Contains(cut, "The gate groans open.") || strings.Contains(cut, "A second beat") || !strings.Contains(cut, "first part of a very long story") {
		t.Errorf("a story longer than the cap keeps its start")
	}
	storyMaxRecords = 20000
	if strings.Contains(page, "dinner") {
		t.Errorf("the party chat is in the story")
	}
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Errorf("a narration's markup is on the page as markup")
	}
	// the Live Table's Chronicle tells the same story (the app shell's pages, from loadStory), never the party chat, and the old feed is gone
	if _, live := get(fmt.Sprintf("/campaign/%d", camp)); !strings.Contains(live, "The gate groans open.") || strings.Contains(live, "dinner") ||
		strings.Contains(live, "activity-feed-list") {
		t.Errorf("the Live Table's Chronicle is not the story (or still carries the old feed)")
	}
	if c, j := get(base + ".json"); c != 200 || !strings.Contains(j, `"inline":true`) || !strings.Contains(j, "The gate groans open.") || strings.Contains(j, "dinner") {
		t.Errorf("story.json: %d", c)
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
	if c, j := get(base + ".json"); strings.Contains(j, "The gate groans open.") {
		t.Errorf("a private campaign's story.json was served without the password: %d", c)
	}
}
