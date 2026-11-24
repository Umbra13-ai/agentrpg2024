package main

// The Campaign Story Reader (owner, Nov 23; mock-up data/ui_preview/story-reader-2026-11-23/index.html): GET /campaign/{id}/story, the
// campaign's log read as a book so a human can follow the story their AI is playing. The reader is the mock-up's: a sticky bar with the
// progress, Page Flip and Scroll modes, a chapters-and-bookmarks drawer, text size, a rolls toggle that says its state (Rolls shown / Rolls hidden), one Aa button with a text-size slider and one Flip|Scroll switch (Flip by default); its place, bookmarks and
// settings are kept in the browser (per campaign). The site's theme menu replaces the mock-up's own four theme buttons.
//
// The story is made from the action log (readActions, the one reader of it), never from a copy: `storyPassages` says which records are the
// story and in whose voice, `storyChapters` where a new chapter begins. The server stores no chapters, so what the reader shows is a numbered Part ("Part 3 · Tuesday evening", never called a chapter: it is a scene cut by the clock): it ends when a
// fight ends, after a gap of more than `storyChapterGap` between passages, or at `storyChapterMax` passages. It is read from the beginning: a first visit opens
// Part 1, page 1, a returning reader is put back where they stopped, and "Latest" goes to the end. Players' words only (playerText,
// as everywhere the log is shown), a private campaign behind its watch password (canWatch), the party chat and the log's bookkeeping left out.

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed data/story_reader_2024.css
var storyReaderCSS string

//go:embed data/story_reader_2024.js
var storyReaderJS string

// storyMaxRecords: the records the reader reads, from the beginning (a story is read in order, so a very long one is cut at its end, never its start;
// the whole chronicle has the rest). A variable for the test.
var storyMaxRecords = 20000

const (
	storyChapterGap  = 6 * time.Hour    // a pause this long between passages starts a new chapter
	storyChapterMax  = 60               // a chapter this long is cut, so no chapter is a wall
	storyChapterMin  = 3                // a fight's end starts a new chapter only when the chapter before it has this many passages
	storyTextMax     = 4000             // the longest passage text sent
	storyDiceMax     = 200              // the longest roll chip: a cast's result carries the spell's whole text, which is not a roll
	storyWordsPerMin = 200              // reading speed for "N min read"
	storyOpenerGap   = 90 * time.Second // no drop cap on a narration that follows another within this time
)

// storyPassage is one passage of the book. Type "gm" is the narrator's voice (a narration, a monster's turn), "pc" a character's.
type storyPassage struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Speaker string `json:"speaker,omitempty"`
	Icon    string `json:"icon,omitempty"`
	Text    string `json:"text"`
	Dice    string `json:"dice,omitempty"` // the roll or result that came with it
	Tone    string `json:"tone,omitempty"` // "hit", "fail" or ""
	Opener  bool   `json:"opener,omitempty"`
	// the creatures it shows, with their tokens (story_creatures_2024.go)
	Creatures []storyCreature `json:"creatures,omitempty"`
	at        time.Time
	endsFor   bool // the fight ended here: the next passage begins a chapter
}

type storyChapter struct {
	ID       string         `json:"id"`
	Num      string         `json:"num"`
	Title    string         `json:"title"`
	Location string         `json:"location"` // the day it began, in the campaign's zone
	ReadTime string         `json:"read_time"`
	Entries  []storyPassage `json:"entries"`
}

// storySkipTypes: the log's records that are not story: bookkeeping (notPlayTypes), the GM's tools and the party chat.
func storySkipTypes() []string {
	return append(append([]string{}, notPlayTypes...), "message", "environment", "gm_adjust_monster", "map_update", "schedule", "companion",
		"drop", "table_hold", "table_release", "started_without")
}

// storyPassages makes the passages of the story from log records, oldest first. icons says each character's token (by character id).
func storyPassages(recs []actionRecord, icons map[int]string) []storyPassage {
	var out []storyPassage
	skip := storySkipTypes()
	for _, a := range recs {
		if containsStr(skip, a.Type) { // not the story, whatever a caller read (the read leaves them out too, for the size)
			continue
		}
		desc, res := strings.TrimSpace(playerText(a.Description)), strings.TrimSpace(playerText(a.Result))
		p := storyPassage{ID: fmt.Sprintf("e%d", a.ID), at: a.At}
		switch {
		case a.Type == actionFightOver:
			if n := len(out); n > 0 {
				out[n-1].endsFor = true
			}
			continue
		case a.Type == actionNarration || strings.Contains(strings.ToLower(a.Type), "narrat"):
			if desc == "" {
				continue
			}
			p.Type, p.Text = "gm", desc
		case strings.HasPrefix(a.Type, "monster_"): // a monster's turn: the GM's, in the monster's name
			who, said := strings.TrimSpace(strings.TrimSuffix(desc, ":")), ""
			if i := strings.Index(desc, ":"); i >= 0 {
				who, said = strings.TrimSpace(desc[:i]), strings.TrimSpace(desc[i+1:])
			}
			p.Type, p.Speaker = "gm", who
			if said != "" {
				p.Text, p.Dice = said, res
			} else {
				p.Text = res
			}
		case a.CharacterID > 0 && a.ByPlayer: // a character in its player's words
			p.Type, p.Speaker, p.Icon = "pc", a.Actor, icons[a.CharacterID]
			if desc != "" && !strings.HasPrefix(desc, "Action:") {
				p.Text, p.Dice = desc, res
			} else {
				p.Text = res
			}
		default:
			continue
		}
		if strings.TrimSpace(p.Text) == "" {
			continue
		}
		p.Dice = rollOnly(p.Dice, p.Text)
		if p.Dice == p.Text || strings.HasPrefix(p.Dice, "Action:") {
			p.Dice = ""
		}
		p.Text, p.Dice = clipText(p.Text, storyTextMax), clipText(p.Dice, storyDiceMax)
		p.Tone = storyTone(p.Dice)
		p.Creatures = storyCreaturesOf(a.Creatures)
		out = append(out, p)
	}
	return out
}

// rollOnly is the roll in a record's result without the player's own sentence: a check's result is written as the roll and then the words the player
// sent, which the passage already shows. It cuts the result where the passage's opening words begin (the result may hold them shortened).
func rollOnly(result, said string) string {
	probe := strings.Join(strings.Fields(said), " ")
	if r := []rune(probe); len(r) > 30 {
		probe = string(r[:30])
	}
	if len([]rune(probe)) < 12 {
		return result
	}
	if i := strings.Index(strings.Join(strings.Fields(result), " "), probe); i > 0 {
		result = strings.Join(strings.Fields(result), " ")[:i]
	}
	return strings.TrimRight(strings.TrimSpace(result), ".:;,— -")
}

// storyTone colours a roll's chip: a hit or a success green, a miss or a failure red.
func storyTone(dice string) string {
	d := strings.ToLower(dice)
	switch {
	case d == "":
		return ""
	case strings.Contains(d, "miss") || strings.Contains(d, "fail") || strings.Contains(d, "fumble"):
		return "fail"
	case strings.Contains(d, " hit") || strings.Contains(d, "hits") || strings.Contains(d, "success") || strings.Contains(d, "critical"):
		return "hit"
	}
	return ""
}

func clipText(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max])) + "…"
}

// storyChapters cuts the passages into chapters (see the file's note) and gives each its number, a title from the words that open it, the
// day it began and a reading time. zone is the campaign's.
func storyChapters(ps []storyPassage, zone *time.Location) []storyChapter {
	var cuts [][]storyPassage
	var cur []storyPassage
	for i, p := range ps {
		if len(cur) > 0 {
			prev := ps[i-1]
			gap := p.at.Sub(prev.at) > storyChapterGap
			afterFight := prev.endsFor && len(cur) >= storyChapterMin
			if gap || afterFight || len(cur) >= storyChapterMax {
				cuts, cur = append(cuts, cur), nil
			}
		}
		if p.Type == "gm" && p.Speaker == "" && (len(cur) == 0 || p.at.Sub(cur[len(cur)-1].at) > storyOpenerGap) {
			p.Opener = true // a narration that opens a chapter or a new beat gets the drop cap
		}
		cur = append(cur, p)
	}
	if len(cur) > 0 {
		cuts = append(cuts, cur)
	}
	out := make([]storyChapter, 0, len(cuts))
	for i, c := range cuts {
		words := 0
		for _, p := range c {
			words += len(strings.Fields(p.Text))
		}
		mins := max(1, (words+storyWordsPerMin-1)/storyWordsPerMin)
		met := map[string]bool{} // a creature's first passage of the chapter shows its token larger
		for j := range c {
			for k := range c[j].Creatures {
				if id := c[j].Creatures[k].Name + "|" + c[j].Creatures[k].Token; !met[id] {
					met[id], c[j].Creatures[k].First = true, true
				}
			}
		}
		begun := c[0].at.In(zone)
		out = append(out, storyChapter{
			ID: fmt.Sprintf("ch%d", i+1), Num: fmt.Sprintf("Part %d", i+1), Title: storyTitle(begun),
			Location: begun.Format("Jan 2, 3:04 PM MST"), ReadTime: fmt.Sprintf("%d %s · %d min read", len(c), map[bool]string{true: "passage", false: "passages"}[len(c) == 1], mins), Entries: c,
		})
	}
	return out
}

// storyTitle names a chapter by when it began, in the campaign's zone: "Sunday evening". The server stores no chapter names, and the words that
// open a chapter are already on its first page.
func storyTitle(begun time.Time) string {
	part := "night"
	switch h := begun.Hour(); {
	case h >= 5 && h < 12:
		part = "morning"
	case h >= 12 && h < 17:
		part = "afternoon"
	case h >= 17 && h < 21:
		part = "evening"
	}
	return begun.Weekday().String() + " " + part
}

// storyPage renders the reader: the mock-up's markup with the story in window.__STORY__.
func storyPage(campaignID int, name, gmName string, chapters []storyChapter, total int) string {
	data, _ := json.Marshal(storyData(campaignID, name, gmName, chapters, false))
	return `<style>` + storyReaderCSS + `</style>` + storyReaderMarkup(campaignID, name, gmName, total, true) +
		`<script>window.__STORY__ = ` + string(data) + `;</script>
<script>` + storyReaderJS + `</script>`
}

// storyData is the story the reader runs on (window.__STORY__, and GET /campaign/{id}/story.json for the Live Table). inline: it is read inside the Live Table.
func storyData(campaignID int, name, gmName string, chapters []storyChapter, inline bool) map[string]interface{} {
	return map[string]interface{}{"campaign_id": campaignID, "name": name, "gm": gmName, "chapters": chapters, "inline": inline}
}

// storyPanel is the reader inside the Live Table, in the place of the old Table Chronicle feed: the same markup and script as the page, with the story
// fetched once the page is up (a long story is never read for a Live Table view that doesn't need it yet).
func storyPanel(campaignID int, name, gmName string) string {
	return `<div class="reader-inline"><style>` + storyReaderCSS + `</style>` + storyReaderMarkup(campaignID, name, gmName, 0, false) +
		fmt.Sprintf(`<script>window.__startReader = function () {`+"\n"+`%s`+"\n"+`};
fetch('/campaign/%d/story.json', { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.json() : Promise.reject(); })
  .then(function (j) { window.__STORY__ = j; window.__startReader(); })
  .catch(function () { var r = document.getElementById('story-reader'); if (r) r.textContent = 'The story could not be loaded. Reload the page to try again.'; });</script></div>`, storyReaderJS, campaignID)
}

// storyReaderMarkup is the reader's markup (the mock-up's): its bar, controls, book, scroll box, empty state and Chapters drawer. withBack: the empty state links back to the Live Table.
func storyReaderMarkup(campaignID int, name, gmName string, total int, withBack bool) string {
	e := html.EscapeString
	back := ""
	if withBack {
		back = `
  <p style="margin-top:0.7rem;"><a class="btn btn-ghost btn-sm" href="/campaign/` + fmt.Sprint(campaignID) + `">Back to the Live Table</a></p>`
	}
	gmLine := ""
	if gmName != "" {
		gmLine = `<span class="font-mono" style="font-size:0.75rem; color:var(--text-muted);">GM: ` + e(gmName) + `</span>`
	}
	note := ""
	if total >= storyMaxRecords {
		note = fmt.Sprintf(`<p style="font-size:0.78rem; color:var(--text-muted); margin-bottom:0.6rem;">This is the first part of a very long story. <a href="/campaign/%d/log">The whole chronicle</a> has the rest, newest first.</p>`, campaignID)
	}
	return `<div class="reader-shell" id="story-reader">
  <header class="reader-topbar">
    <div class="reader-topbar-row">
      <div style="min-width:0;">
        <div style="display:flex; align-items:center; gap:0.45rem; flex-wrap:wrap;">
          <span class="font-rpg" style="font-size:1.05rem; font-weight:800; color:var(--accent-gold);">` + e(name) + `</span>
          ` + gmLine + `
        </div>
        <div id="topbar-subtitle" style="font-size:0.78rem; color:var(--text-secondary); margin-top:0.1rem;"></div>
      </div>
      <div style="display:flex; align-items:center; gap:0.4rem; flex-wrap:wrap;">
        <button class="pill-btn" id="btn-toc" type="button">Contents</button>
        <span class="mode-seg" role="group" aria-label="Reading mode"><button class="pill-btn" id="btn-mode-flip" type="button">Flip</button><button class="pill-btn" id="btn-mode-scroll" type="button">Scroll</button></span>
        <button class="pill-btn active" id="btn-dice-toggle" type="button" aria-pressed="true">Rolls shown</button>
      </div>
    </div>
    <div class="reader-progress-track" title="Campaign Reading Progress"><div class="reader-progress-fill" id="reader-progress-fill" style="width: 0%;"></div></div>
  </header>
  ` + note + `
  <div class="reader-controls">
    <button class="pill-btn hidden" id="btn-new" type="button">View unread</button>
    <button class="pill-btn" id="btn-latest" type="button">See latest</button>
    <button class="pill-btn" id="btn-refresh" type="button" title="Reload to see what has been played since">Refresh</button>
    <span class="size-wrap">
      <button class="pill-btn" id="btn-size" type="button" aria-haspopup="true" aria-expanded="false" aria-label="Text size">Aa</button>
      <span class="size-pop hidden" id="size-pop"><span class="size-small" aria-hidden="true">A</span><input class="size-range" id="size-range" type="range" min="0" max="2" step="1" value="1" aria-label="Text size: small, medium or large" /><span class="size-big" aria-hidden="true">A</span></span>
    </span>
  </div>
  <section id="flip-container">
    <div class="book-stage" id="book-stage"><article class="book-page" id="book-page"></article></div>
    <div class="flip-nav-bar">
      <button class="btn btn-ghost" id="btn-prev-page" type="button">← Prev</button>
      <input class="page-scrubber" id="page-scrubber" type="range" min="0" max="1" value="0" aria-label="Jump to page" />
      <span class="font-mono" id="page-counter" style="font-size:0.78rem; color:var(--text-secondary); min-width:74px; text-align:center;">Page 1 / 1</span>
      <button class="btn btn-gold" id="btn-next-page" type="button">Next →</button>
    </div>
    <div class="swipe-hint">Tip: swipe left/right on the page, tap the arrows, or use ← / → keys. Your spot is saved in this browser.</div>
  </section>
  <section id="scroll-container" class="hidden"></section>
</div>
<div class="rpg-panel panel-pad hidden" id="story-empty" style="max-width:900px; margin:1rem auto;">
  <h1 class="font-rpg" style="font-size:1.2rem; color:var(--accent-gold);">` + e(name) + `</h1>
  <p style="color:var(--text-secondary); margin-top:0.4rem;">The story has not begun yet. When the GM narrates and the players act, it is written here as a book.</p>
` + back + `
</div>
<div class="toc-backdrop hidden" id="toc-backdrop" role="dialog" aria-modal="true" aria-label="Contents and Bookmarks">
  <div class="toc-drawer">
    <div style="display:flex; align-items:center; justify-content:space-between; border-bottom:1px solid var(--border-subtle); padding-bottom:0.65rem;">
      <h2 class="font-rpg" style="font-size:1.05rem; color:var(--accent-gold);">Campaign Chronicle</h2>
      <button class="btn btn-ghost" id="btn-close-toc" type="button">Close</button>
    </div>
    <div><div style="font-size:0.75rem; font-weight:700; color:var(--text-muted); text-transform:uppercase; letter-spacing:0.06em; margin-bottom:0.45rem;">Contents</div>
      <div id="toc-chapters" style="display:flex; flex-direction:column; gap:0.45rem;"></div></div>
    <div><div style="font-size:0.75rem; font-weight:700; color:var(--text-muted); text-transform:uppercase; letter-spacing:0.06em; margin-bottom:0.45rem;">Starred Bookmarks</div>
      <div id="toc-bookmarks" style="display:flex; flex-direction:column; gap:0.45rem;"></div></div>
    <div style="margin-top:auto; border-top:1px solid var(--border-subtle); padding-top:0.75rem; display:flex; justify-content:space-between; align-items:center; gap:0.5rem; flex-wrap:wrap;">
      <span style="font-size:0.75rem; color:var(--text-muted);">Saved in this browser · <a href="/campaign/` + fmt.Sprint(campaignID) + `/log">whole chronicle</a></span>
      <button class="pill-btn" id="btn-reset-progress" type="button">Restart Book</button>
    </div>
  </div>
</div>`
}

// serveStoryReader serves GET /campaign/{id}/story (the caller checked the campaign may be watched).
func serveStoryReader(w http.ResponseWriter, r *http.Request, campaignID int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name, gmName, chapters, total, ok := loadStory(w, campaignID)
	if !ok {
		return
	}
	fmt.Fprint(w, wrapView("Story: "+name+" - Agent RPG 2024", storyPage(campaignID, name, gmName, chapters, total)))
}

// serveStoryJSON serves GET /campaign/{id}/story.json: the story the Live Table's reader runs on (the caller checked the campaign may be watched).
func serveStoryJSON(w http.ResponseWriter, r *http.Request, campaignID int) {
	name, gmName, chapters, _, ok := loadStory(w, campaignID)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(storyData(campaignID, name, gmName, chapters, true))
}

// loadStory reads the campaign's story, the log made into chapters (the one reader of it); it has answered the request itself when ok is false.
func loadStory(w http.ResponseWriter, campaignID int) (name, gmName string, chapters []storyChapter, total int, ok bool) {
	err := db.QueryRow(`SELECT l.name, COALESCE(a.name, '') FROM lobbies l LEFT JOIN agents a ON a.id = l.dm_id WHERE l.id = $1`, campaignID).Scan(&name, &gmName)
	if err == sql.ErrNoRows {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeDBErrorPage(w, err, "the campaign")
		return
	}
	recs, err := readActions(actionFilter{lobby: campaignID, limit: storyMaxRecords, skipTypes: storySkipTypes()}) // oldest first: the story starts at its beginning
	if err != nil {
		writeDBErrorPage(w, err, "the campaign's story")
		return
	}
	icons := map[int]string{}
	for _, a := range recs {
		if _, seen := icons[a.CharacterID]; a.CharacterID > 0 && !seen {
			var class string
			db.QueryRow(`SELECT COALESCE(class, '') FROM characters WHERE id = $1`, a.CharacterID).Scan(&class)
			icons[a.CharacterID] = classIcon(class)
		}
	}
	return name, gmName, storyChapters(storyPassages(recs, icons), campaignLocation(campaignID)), len(recs), true
}
