package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every place that hands the GM a mechanic ("the GM resolves it", "For the GM to apply ...") either lists it as an open item when it lasts
// (monsterHandoverSpell, monsterHandoverEntry, handOverOnFailure: gm_handover_2024.go) or says it is a one-shot (`// gm-one-shot: why`) within a few
// lines of the text. A new such text with neither fails.
func TestEveryGMHandoverIsKindedOrOneShot(t *testing.T) {
	phrases := []string{"GM, not applied automatically", "the GM resolves it", "The GM resolves the spell", "For the GM to apply", "The GM handles the rest", "The GM handles how this comes", "The GM applies what the stat block adds"}
	files, _ := filepath.Glob("monster_*.go") // a hand-over of a monster's mechanic; the schedule texts that say "the GM resolves it" are not
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "gm_handover_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		lines := strings.Split(string(b), "\n")
		for n, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, p := range phrases {
				if !strings.Contains(line, p) {
					continue
				}
				lo, hi := max(0, n-14), min(len(lines), n+4)
				ctx := strings.Join(lines[lo:hi], "\n")
				if !strings.Contains(ctx, "gm-one-shot") && !strings.Contains(ctx, "monsterHandover") && !strings.Contains(ctx, "handOverOnFailure") && !strings.Contains(ctx, "handOverToGM") {
					t.Errorf("%s:%d says %q and neither lists an open item (monsterHandoverSpell / monsterHandoverEntry) nor says `// gm-one-shot: why`", f, n+1, p)
				}
			}
		}
	}
}

// The GM's open items through the whole server: a lasting spell the server doesn't run is listed with its creature and rule, a one-shot is
// not, each end the server can see clears it, snooze hides and returns it, the GM resolves one, a creature's or all, a read changes nothing.
func TestPostgresGMOpenItems(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg open items gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Open Items", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg open items player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Open %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{})
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Open Fiend", "monster_key": "pit-fiend"}, {"name": "Open Fiend Two", "monster_key": "pit-fiend"}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	var fiends []int
	for _, e := range combatEntries(lobby) {
		if id := asInt(e["id"]); id < 0 {
			fiends = append(fiends, id)
		}
	}
	if len(fiends) != 2 {
		t.Fatalf("fiends: %v", fiends)
	}
	a, b := fiends[0], fiends[1]
	path := "/api/gm/open-items"
	list := func(q string) []interface{} {
		t.Helper()
		c, j := pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d%s", path, lobby, q), gm, nil)
		if c != 200 || j["open_items_error"] != nil {
			t.Fatalf("list: %d %v", c, j)
		}
		return asList(j["open_items"])
	}
	count := func() string { // every mutable column of every row, not only how many rows there are
		var sum string
		db.QueryRow(`SELECT COALESCE(md5(string_agg(id::text || ':' || COALESCE(resolved_at::text,'') || ':' || COALESCE(snoozed_until::text,'') || ':' || snooze_round::text || ':' || source_id::text, ',' ORDER BY id)), '')
			FROM gm_open_items WHERE lobby_id = $1`, lobby).Scan(&sum)
		return sum
	}
	closedByServer := func() (n int) {
		db.QueryRow(`SELECT COUNT(*) FROM gm_open_items WHERE lobby_id = $1 AND resolved_by LIKE 'server:%'`, lobby).Scan(&n)
		return
	}

	// a one-shot or unknown spell is never listed; a lasting one is, with its creature and rule
	if monsterHandoverSpell(lobby, a, "detect-magic", nil) || monsterHandoverSpell(lobby, a, "plane-shift", nil) {
		t.Errorf("a one-shot spell was listed")
	}
	if len(list("")) != 0 {
		t.Fatalf("items before any hand-over: %v", list(""))
	}
	if err := startMonsterConcentration(lobby, a, "fly"); err != nil {
		t.Fatalf("concentration: %v", err)
	}
	if !monsterHandoverSpell(lobby, a, "fly", nil) {
		t.Fatalf("Fly was not listed")
	}
	monsterHandoverSpell(lobby, a, "fly", nil) // a repeat refreshes, never duplicates
	items := list("")
	if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0]), "Open Fiend") || !strings.Contains(strings.ToLower(fmt.Sprint(items[0])), "fly") {
		t.Fatalf("Fly item: %v", items)
	}
	// a hit's remainder the server doesn't apply: listed when its wording gives a duration, told in the reply alone when it doesn't
	if got := gmNotApplied(lobby, b, "Test Grasp", "The target is held by the web until the end of its next turn.", []int{a}); !strings.Contains(got, "[GM, not applied automatically:") {
		t.Errorf("the bracket: %q", got)
	}
	gmNotApplied(lobby, b, "Test Cry", "The giant has Advantage on attack rolls until the start of the giant's next turn.", nil) // War Cry's wording
	gmNotApplied(lobby, b, "Test Nip", "The target feels a faint chill.", []int{a})
	if got := fmt.Sprint(list("")); !strings.Contains(got, "Test Grasp") || !strings.Contains(got, "Test Cry") || strings.Contains(got, "Test Nip") {
		t.Errorf("a lasting rider is listed and a one-shot is not: %v", got)
	}
	resolveGMItems(lobby, 0, b, false, "gm")
	// the GM's status carries it and one task line
	_, st := pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", lobby), gm, nil)
	if len(asList(st["open_items"])) != 1 || !strings.Contains(fmt.Sprint(st["gm_tasks"]), "open item") {
		t.Errorf("status: open_items %v tasks %v", st["open_items"], st["gm_tasks"])
	}
	// a read changes no row
	before := count()
	for i := 0; i < 3; i++ {
		list("")
		pgCall(t, "GET", fmt.Sprintf("/api/gm/status?campaign_id=%d", lobby), gm, nil)
	}
	if count() != before {
		t.Errorf("a read changed the table: %s -> %s", before, count())
	}

	// ends the server sees: Concentration, the creature leaving, the fight ending
	// Concentration ending clears the Fly item (the reader asks the Concentration owner)
	endMonsterConcentration(lobby, a, "Open Fiend", "test")
	if got := list(""); len(got) != 0 {
		t.Fatalf("Concentration ended and the item stayed: %v", got)
	}
	if closedByServer() != 1 {
		t.Errorf("the Concentration's end was not written as the item's resolution (%d closed by the server)", closedByServer())
	}
	startMonsterConcentration(lobby, a, "fly")
	monsterHandoverSpell(lobby, a, "fly", nil) // listed again with a new cast
	startMonsterConcentration(lobby, b, "gaseous-form")
	if !monsterHandoverSpell(lobby, b, "gaseous-form", nil) {
		t.Fatalf("Gaseous Form was not listed")
	}
	if len(list("")) != 2 {
		t.Fatalf("two items expected: %v", list(""))
	}
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/remove", lobby), gm, map[string]interface{}{"combatant_id": b}); c != 200 {
		t.Fatalf("remove: %d %v", c, j)
	}
	if got := list(""); len(got) != 1 {
		t.Errorf("removing the creature left %d items: %v", len(got), got)
	}
	if closedByServer() != 2 {
		t.Errorf("removing the creature was not written as its items' resolution (%d closed by the server)", closedByServer())
	}

	// snooze: hidden, and back after the next round
	id := asInt(list("")[0].(map[string]interface{})["id"])
	if c, j := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "item_id": id, "snooze": "next_round"}); c != 200 {
		t.Fatalf("snooze: %d %v", c, j)
	}
	if len(list("")) != 0 || len(list("&all=true")) != 1 {
		t.Errorf("snoozed item: hidden %v / all %v", list(""), list("&all=true"))
	}
	pgExec(t, `UPDATE gm_open_items SET snooze_round = -1, snoozed_until = NOW() - INTERVAL '1 minute' WHERE id = $1`, id)
	if len(list("")) != 1 {
		t.Errorf("a snooze that has run out still hides the item")
	}
	if c, j := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "item_id": id, "snooze": "checkins", "checkins": 2}); c != 200 || len(list("")) != 0 {
		t.Errorf("check-in snooze: %d %v", c, j)
	}
	if c, _ := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "item_id": id, "snooze": "checkins", "checkins": 99}); c != 400 {
		t.Errorf("99 check-ins accepted")
	}
	if c, _ := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "item_id": id, "snooze": "sometime"}); c != 400 {
		t.Errorf("an unknown snooze accepted")
	}

	// the GM adds its own, resolves one, a creature's, all; a resolved item is never listed
	c, j := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "creature_id": a, "text": "Remember the bridge is on fire."})
	if c != 200 || j["item_id"] == nil {
		t.Fatalf("own item: %d %v", c, j)
	}
	own := asInt(j["item_id"])
	if c, j := pgCall(t, "DELETE", path, gm, map[string]interface{}{"campaign_id": lobby, "item_id": own}); c != 200 || asInt(j["resolved"]) != 1 {
		t.Errorf("resolve one: %d %v", c, j)
	}
	if c, _ := pgCall(t, "DELETE", path, gm, map[string]interface{}{"campaign_id": lobby, "item_id": own}); c != 404 {
		t.Errorf("resolving twice: %d", c)
	}
	pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "creature_id": a, "text": "One."})
	pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "creature_id": a, "text": "Two."})
	if c, j := pgCall(t, "DELETE", path, gm, map[string]interface{}{"campaign_id": lobby, "creature_id": a}); c != 200 || asInt(j["resolved"]) < 2 {
		t.Errorf("resolve a creature's: %d %v", c, j)
	}
	pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "text": "Three."})
	if c, _ := pgCall(t, "DELETE", path, gm, map[string]interface{}{"campaign_id": lobby, "all": true}); c != 200 || len(list("&all=true")) != 0 {
		t.Errorf("resolve all left %v", list("&all=true"))
	}
	if c, _ := pgCall(t, "DELETE", path, gm, map[string]interface{}{"campaign_id": lobby}); c != 400 {
		t.Errorf("an empty resolve: %d", c)
	}
	if c, _ := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "bogus": 1}); c != 400 {
		t.Errorf("an unknown field: %d", c)
	}

	// a creature struck down no longer holds what it cast (it is in the fight at 0 Hit Points: the reader asks its Hit Points)
	monsterHandoverSpell(lobby, a, "mage-armor", nil)
	if len(list("")) != 1 {
		t.Fatalf("Mage Armor was not listed")
	}
	setMonsterHP(lobby, a, 0)
	if got := list(""); len(got) != 0 {
		t.Errorf("a monster at 0 Hit Points still holds its Mage Armor item: %v", got)
	}
	setMonsterHP(lobby, a, 1000)

	// a text of multibyte characters is cut on characters, not bytes (a title of 100 accented letters), and a character of another campaign can't be named
	if c, j := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "text": strings.Repeat("é", 100)}); c != 200 {
		t.Errorf("a long text of accented letters: %d %v", c, j)
	}
	if c, _ := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "creature_id": 999999999, "text": "Not in this campaign."}); c != 404 {
		t.Errorf("a character of no campaign named in an item: %d", c)
	}
	pgCall(t, "DELETE", path, gm, map[string]interface{}{"campaign_id": lobby, "all": true})
	monsterHandoverSpell(lobby, a, "mage-armor", nil)

	// the same hand-over made at once is one open item (the unique index and the upsert)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handOverToGM(gmHandover{Lobby: lobby, Source: a, Kind: "gm", Entry: "race", Title: "At once", Detail: "x", EndKind: endNever})
		}()
	}
	wg.Wait()
	var racing int
	db.QueryRow(`SELECT COUNT(*) FROM gm_open_items WHERE lobby_id = $1 AND entry = 'race' AND resolved_at IS NULL`, lobby).Scan(&racing)
	if racing != 1 {
		t.Errorf("8 hand-overs at once made %d open items", racing)
	}
	pgExec(t, `UPDATE gm_open_items SET resolved_at = NOW(), resolved_by = 'test' WHERE lobby_id = $1 AND entry = 'race'`, lobby)
	monsterHandoverSpell(lobby, a, "geas", nil) // lasts past the fight: it lets go of the fight's monster id when the fight ends

	if c, _ := pgCall(t, "GET", fmt.Sprintf("%s?campaign_id=%d", path, lobby), tok, nil); c != 403 {
		t.Errorf("a player read the GM's list: %d", c)
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/end", lobby), gm, map[string]interface{}{}); c != 200 {
		t.Fatalf("combat end: %d", c)
	}
	got := list("")
	if _, named := got[0].(map[string]interface{})["creature"]; len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]), "geas") || named { // the title keeps the caster's name; the item no longer names a creature of the fight
		t.Errorf("after the fight only the Geas stays, with no creature of the fight: %v", got)
	}
	if closedByServer() < 3 {
		t.Errorf("the fight's end was not written as the item's resolution (%d closed by the server)", closedByServer())
	}
	var source int
	db.QueryRow(`SELECT source_id FROM gm_open_items WHERE lobby_id = $1 AND entry = 'geas' AND resolved_at IS NULL`, lobby).Scan(&source)
	if source != 0 {
		t.Errorf("the Geas still points at monster id %d of a fight that is over", source)
	}
	// outside a fight there is no round to wait for: "next_round" waits for the next GM check-in
	geas := asInt(got[0].(map[string]interface{})["id"])
	if c, j := pgCall(t, "POST", path, gm, map[string]interface{}{"campaign_id": lobby, "item_id": geas, "snooze": "next_round"}); c != 200 {
		t.Fatalf("snooze outside a fight: %d %v", c, j)
	}
	var round int
	var until sql.NullTime
	db.QueryRow(`SELECT snooze_round, snoozed_until FROM gm_open_items WHERE id = $1`, geas).Scan(&round, &until)
	if round != 0 || !until.Valid || !until.Time.After(time.Now().Add(-time.Minute)) {
		t.Errorf("a next-round snooze outside a fight: round %d, until %v (a time is wanted, no round)", round, until)
	}
}

// The Stage 2 families through narrate's monster_action: a Treant's Animate Trees (a day, or until it dies) and a Wraith's Create Specter (under its
// control) are the GM's and keep going, so they are listed; the list clears when the creature leaves the fight.
func TestPostgresGMOpenItemsFromStatBlockEntries(t *testing.T) {
	testPostgres(t)
	_, gm := pgAgent(t, "pg open entries gm")
	_, j := pgCall(t, "POST", "/api/campaigns", gm, map[string]interface{}{"name": "Pg Open Entries", "max_players": 2})
	lobby := int(j["campaign_id"].(float64))
	pgCall(t, "PATCH", fmt.Sprintf("/api/campaigns/%d/schedule", lobby), gm, map[string]interface{}{"state": "off"})
	_, tok := pgAgent(t, "pg open entries player")
	_, j = pgCall(t, "POST", "/api/characters", tok, pgRogue(fmt.Sprintf("Pg Entries %d", time.Now().UnixNano()%1e9)))
	char := int(j["character_id"].(float64))
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/join", lobby), tok, map[string]interface{}{"character_id": char})
	pgFinish(t, tok, char)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), tok, nil)
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/ready", lobby), gm, nil)
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/start", lobby), gm, nil); c != 200 {
		t.Fatalf("start: %d %v", c, j)
	}
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/start", lobby), gm, map[string]interface{}{})
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Entry Treant", "monster_key": "treant"}, {"name": "Entry Wraith", "monster_key": "wraith"}}}); c != 200 {
		t.Fatalf("add: %d %v", c, j)
	}
	act := func(monster, action string) {
		t.Helper()
		if c, j := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": lobby, "narration": monster + " acts.",
			"monster_action": map[string]interface{}{"monster": monster, "action": action, "target": fmt.Sprint(char)}}); c != 200 {
			t.Fatalf("%s %s: %d %v", monster, action, c, j)
		}
	}
	act("Entry Treant", "Animate Trees")
	// a monster that enters the fight with a trait the GM runs each turn (Barbed Hide) is listed at once, and leaves the list with it
	if c, j := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", lobby), gm, map[string]interface{}{
		"combatants": []map[string]interface{}{{"name": "Entry Devil", "monster_key": "barbed-devil"}}}); c != 200 {
		t.Fatalf("add the devil: %d %v", c, j)
	}
	act("Entry Wraith", "Create Specter")
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/gm/open-items?campaign_id=%d", lobby), gm, nil)
	got := fmt.Sprint(j["open_items"])
	for _, want := range []string{"Animate Trees", "Create Specter", "Barbed Hide"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is not an open item: %v", want, got)
		}
	}
	var wraith int
	for _, e := range combatEntries(lobby) {
		if fmt.Sprint(e["name"]) == "Entry Wraith" {
			wraith = asInt(e["id"])
		}
	}
	if c, _ := pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/remove", lobby), gm, map[string]interface{}{"combatant_id": wraith}); c != 200 {
		t.Fatalf("remove the wraith: %d", c)
	}
	_, j = pgCall(t, "GET", fmt.Sprintf("/api/gm/open-items?campaign_id=%d", lobby), gm, nil)
	if got := fmt.Sprint(j["open_items"]); strings.Contains(got, "Create Specter") || !strings.Contains(got, "Animate Trees") {
		t.Errorf("after the wraith left: %v", got)
	}
}

// Every function that gives a monster a combat id (newMonsterID) also lists the traits the GM runs each turn (listRecurringTraits), so a monster
// added by any route (combat/add, an item's summons, a split) can't enter a fight without its open items.
func TestEveryNewMonsterListsItsTraits(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "combat_state_2024.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, fn := range strings.Split(string(b), "\nfunc ")[1:] {
			if strings.Contains(fn, ".newMonsterID()") && !strings.Contains(fn, "listRecurringTraits(") {
				t.Errorf("%s: func %s gives a monster a combat id without listRecurringTraits", f, strings.SplitN(fn, "(", 2)[0])
			}
		}
	}
}

// Over the 330 SRD stat blocks: the traits listed as open items are exactly the ones the server leaves to the GM that act every turn or on a
// trigger; a standing rule (Training, Magic Rope), a return to life after the fight (Celestial Restoration) and a day-long change (Troll Spawn) are not.
func TestRecurringGMTraitsOverTheCatalog(t *testing.T) {
	got := map[string]bool{}
	for _, m := range srdMonsters2024() {
		for _, tr := range recurringGMTraits(m.Traits) {
			got[m.Name+": "+stripUsage(tr.Name)] = true
		}
	}
	want := []string{"Barbed Devil: Barbed Hide", "Clay Golem: Berserk", "Flesh Golem: Berserk", "Fire Elemental: Fire Form", "Hydra: Multiple Heads",
		"Troll: Loathsome Limbs", "Vampire: Vampire Weakness", "Vampire Spawn: Vampire Weakness", "Water Elemental: Freeze", "Aboleth: Mucus Cloud",
		"Tarrasque: Reflective Carapace", "Marilith: Reactive"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("%s is not listed", w)
		}
		delete(got, w)
	}
	for k := range got {
		t.Errorf("listed but not expected (a standing rule or an applied trait?): %s", k)
	}
}
