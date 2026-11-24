package main

// The creatures a story line shows (story_creatures_2024.go): the GM names who appears with a narration (a creature of the fight, an SRD
// monster, a compendium monster), a monster's own turn carries its creature; the story shows each one's token beside the line, larger at its
// first passage of a chapter; my-turn names them; an unknown name refuses the whole request with nothing recorded.

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// storyCreatureFixture: a fight with a raider (its token saved), a compendium NPC with a token, a refused narration that names an unknown
// creature, a narration naming three (one of them twice), the raider's own turn and a last narration naming the NPC.
func storyCreatureFixture(t *testing.T) (gm, player string, camp, hero int) {
	t.Helper()
	gm, player, camp, hero, _ = earlyRulesFixture(t, "Test Story Raider", []srdMonsterEntry{{Name: "Club", Text: "Melee Attack Roll: +4, reach 5 ft. Hit: 2 (1d4) Bludgeoning damage."}})
	raider, ok := fightCreature(camp, "Test Story Raider")
	if !ok || !strings.HasPrefix(raider.Key, "custom:") {
		t.Fatalf("the fight's raider: %+v", raider)
	}
	if err := saveMonsterToken(raider.Key, pngOf(t, 128, 128), "image/png"); err != nil {
		t.Fatal(err)
	}
	// a compendium NPC with a token, named by its name
	npc := inventedMonster("Test Bell Keeper", "Humanoid", nil, []srdMonsterEntry{{Name: "Ring", Text: "The keeper rings the bell."}})
	_, j := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented story fixture", "monsters": []srdMonster2024{npc}})
	npcKey := fmt.Sprint(j["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key = $1`, npcKey) })
	pgCall(t, "POST", "/api/gm/compendium/monsters/token", gm, map[string]interface{}{"campaign_id": camp, "monster_key": npcKey, "image_base64": base64.StdEncoding.EncodeToString(pngOf(t, 128, 128))})

	count := func() int {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM actions WHERE lobby_id = $1`, camp).Scan(&n)
		return n
	}
	before := count()
	if c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "Someone stirs.", "appearing": []string{"Test Story Raider", "Nobody Known"}}); c != 400 || r["error"] != "appearing_unknown" || count() != before {
		t.Fatalf("an unknown name: %d %v (records %d -> %d)", c, r, before, count())
	}
	c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "A goblin, the raider and the bell keeper step out of the fog.",
		"appearing": []string{"goblin-warrior", "#" + fmt.Sprint(monsterIDsByKeyOrName(camp, "Test Story Raider")[0]), "Test Bell Keeper", "goblin warrior"}})
	if c != 200 || !strings.Contains(fmt.Sprint(r["appearing"]), "Goblin Warrior") {
		t.Fatalf("narrate with appearing: %d %v", c, r)
	}
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "The raider swings.",
		"monster_action": map[string]interface{}{"monster": "Test Story Raider", "action": "Club", "target": fmt.Sprint(hero)}})
	pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "They wait.", "appearing": []string{"Test Bell Keeper"}})

	return
}

// The GM's appearing list and a monster's own turn are recorded with the log record (writeActionShowing); my-turn names them.
func TestPostgresNarrateRecordsWhoAppears(t *testing.T) {
	testPostgres(t)
	_, player, camp, hero := storyCreatureFixture(t)
	recs, _ := readActions(actionFilter{lobby: camp, limit: 50})
	var shown [][]shownCreature
	for _, a := range recs {
		if len(a.Creatures) > 0 {
			shown = append(shown, a.Creatures)
		}
	}
	if len(shown) != 3 || len(shown[0]) != 3 || shown[0][0].Key != "goblin-warrior" || !strings.HasPrefix(shown[1][0].Key, "custom:") || shown[2][0].Name != "Test Bell Keeper" {
		t.Errorf("the records' creatures: %+v", shown)
	}
	// the players read who appears
	_, m := pgCall(t, "GET", fmt.Sprintf("/api/my-turn?character_id=%d", hero), player, nil)
	if fmt.Sprint(m["gm_says_appearing"]) != "[Test Bell Keeper]" {
		t.Errorf("my-turn gm_says_appearing: %v (gm_says %v)", m["gm_says_appearing"], m["gm_says"])
	}
}
