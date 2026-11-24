package main

// The GM's stat block page (statblock_view_2024.go): opened by the signed link the builder's replies give, refused without it or after it
// runs out, every text escaped, each sentence marked as the server's or the GM's.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestStatBlockLinkSignature(t *testing.T) {
	key := "custom:abc"
	exp := time.Now().Add(time.Hour).Unix()
	sig := statBlockSig(key, exp)
	if !statBlockLinkValid(key, fmt.Sprint(exp), sig) {
		t.Error("a fresh link")
	}
	if statBlockLinkValid("custom:other", fmt.Sprint(exp), sig) || statBlockLinkValid(key, fmt.Sprint(exp+1), sig) || statBlockLinkValid(key, fmt.Sprint(exp), "00") {
		t.Error("a link for another key, time or signature")
	}
	old := time.Now().Add(-time.Minute).Unix()
	if statBlockLinkValid(key, fmt.Sprint(old), statBlockSig(key, old)) {
		t.Error("a link that ran out")
	}
}

func TestPostgresStatBlockPage(t *testing.T) {
	testPostgres(t)
	gm, _, camp, _, _ := earlyRulesFixture(t, "Test Page Host", []srdMonsterEntry{{Name: "Tap", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 1 Bludgeoning damage."}})
	m := inventedMonster("Test <b>Mire</b> Hag", "Fey", []srdMonsterEntry{{Name: "Odd Charm", Text: "The hag hums an <i>unsettling</i> tune."}},
		[]srdMonsterEntry{{Name: "Claw", Text: "Melee Attack Roll: +5, reach 5 ft. Hit: 7 (1d8 + 3) Slashing damage."}})
	c, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented page fixture", "monsters": []srdMonster2024{m}})
	if c != 201 {
		t.Fatalf("import: %d %v", c, j)
	}
	entry := j["monsters"].([]interface{})[0].(map[string]interface{})
	key, link := fmt.Sprint(entry["monster_key"]), fmt.Sprint(entry["stat_block_url"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	path := link[strings.Index(link, "/monsters/custom/"):]
	code, body := pgRawGet(t, path, "")
	page := string(body)
	if code != 200 || !strings.Contains(page, "Test &lt;b&gt;Mire&lt;/b&gt; Hag") || strings.Contains(page, "<i>unsettling</i>") ||
		!strings.Contains(page, `class="badge badge-arcane">Server</span> Hit:`) || !strings.Contains(page, `badge-combat">GM</span>`) || !strings.Contains(page, "abilities-3x2") {
		t.Errorf("the page: %d\n%s", code, page)
	}
	if code, _ := pgRawGet(t, strings.Replace(path, "sig=", "sig=00", 1), ""); code != 404 {
		t.Errorf("a wrong signature: %d", code)
	}
	if code, _ := pgRawGet(t, "/monsters/custom/"+key, ""); code != 404 {
		t.Errorf("no link: %d", code)
	}
	// the list gives the link too
	if _, l := pgCall(t, "GET", fmt.Sprintf("/api/gm/compendium/monsters?mine=1&campaign_id=%d", camp), gm, nil); !strings.Contains(fmt.Sprint(l["monsters"]), "stat_block_url:") {
		t.Errorf("the list: %v", l["monsters"])
	}
}
