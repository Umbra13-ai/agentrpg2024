package main

import (
	"fmt"
	"strings"
	"testing"
)

// The state of an item a character carries (item_state_2024.go), with invented creatures that use the published sentences:
// a hit that corrodes the worn armor (-1 AC each time, destroyed at AC 10), a saving throw effect on "one nonmagical metal object"
// (armor first, else the weapon in hand; a weapon with no metal is spared), a trait that corrodes the weapon that hits it (-1 to
// attack rolls, read by the attack bonus) and destroys the ammunition, and Mending, which removes the penalty.
func TestPostgresItemCorrosion(t *testing.T) {
	testPostgres(t)
	gm, player, camp, charID, _, _ := companionReactionFixture(t)
	m := compendiumFixture()
	m.Name, m.Type, m.Legendary, m.LegendaryIn, m.DmgImmune, m.Resistances = "Acid Blob", "Ooze", nil, "", "", ""
	m.Traits = []srdMonsterEntry{{Name: "Corroding Body", Text: "Nonmagical ammunition is destroyed immediately after hitting the blob and dealing any damage. Any nonmagical weapon takes a cumulative -1 penalty to attack rolls immediately after dealing damage to the blob and coming into contact with it. The weapon is destroyed if the penalty reaches -5. The penalty can be removed by casting the Mending spell on the weapon."}}
	m.Actions = []srdMonsterEntry{
		{Name: "Pseudopod", Text: "Melee Attack Roll: +60, reach 5 ft. Hit: 1 (1d1) Acid damage. Nonmagical armor worn by the target takes a -1 penalty to the AC it offers. The armor is destroyed if the penalty reduces its AC to 10. The penalty can be removed by casting the Mending spell on the armor."},
		{Name: "Feelers", Text: "The blob targets one nonmagical metal object—armor or a weapon—worn or carried by a creature within 5 feet of itself. Dexterity Saving Throw: DC 40, the creature with the object. Failure: The object takes a -1 penalty to the AC it offers (armor) or to its attack rolls (weapon). Armor is destroyed if the penalty reduces its AC to 10, and a weapon is destroyed if its penalty reaches -5. The penalty can be removed by casting the Mending spell on the armor or weapon."},
	}
	status, imported := pgCall(t, "POST", "/api/gm/compendium/monsters", gm, map[string]interface{}{"campaign_id": camp, "source_note": "invented corrosion fixture", "monsters": []srdMonster2024{m}})
	if status != 201 {
		t.Fatalf("import: %d %v", status, imported)
	}
	key := fmt.Sprint(imported["monsters"].([]interface{})[0].(map[string]interface{})["monster_key"])
	t.Cleanup(func() { db.Exec(`DELETE FROM custom_monsters WHERE key=$1`, key) })
	pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/add", camp), gm, map[string]interface{}{"combatants": []map[string]interface{}{
		{"name": "Acid Blob", "monster_key": key, "hp": 5000, "ac": 1, "initiative": -800}}})
	blob := monsterIDsByKeyOrName(camp, "Acid Blob")[0]
	give := func(name string) {
		editInventory(charID, func(inv []invItem) []invItem {
			return append(inv, invItem{"id": "t-" + strings.ReplaceAll(strings.ToLower(name), " ", "-"), "name": name, "quantity": float64(1)})
		})
	}
	corrosion := func(name string) int { return itemCorrosion(carriedItem(charID, name)) }
	act := func(action string) string {
		t.Helper()
		for n := 0; n < 20; n++ {
			c, r := pgCall(t, "POST", "/api/gm/narrate", gm, map[string]interface{}{"campaign_id": camp, "narration": "x",
				"monster_action": map[string]interface{}{"monster": "Acid Blob", "action": action, "target": fmt.Sprint(charID), "target_ids": []int{charID}}})
			res := fmt.Sprint(r["monster_action_result"])
			if c == 200 && (action != "Pseudopod" || strings.Contains(res, "HIT")) {
				return res
			}
		}
		t.Fatalf("%s never landed", action)
		return ""
	}

	// worn Chain Mail (AC 16): -1 to the AC it offers on a hit, the stored AC follows
	give("Chain Mail")
	pgExec(t, `UPDATE characters SET equipped_armor = 'Chain Mail', equipped_shield = false WHERE id = $1`, charID)
	refreshStoredAC(charID)
	acBefore := computeCharacterAC(charID)
	res := act("Pseudopod")
	if corrosion("Chain Mail") != 1 || computeCharacterAC(charID) != acBefore-1 || !strings.Contains(res, "corrodes") || strings.Contains(res, "penalty to the AC it offers") {
		t.Fatalf("armor on a hit: corrosion %d, AC %d -> %d: %s", corrosion("Chain Mail"), acBefore, computeCharacterAC(charID), res)
	}
	// Leather Armor (AC 11): one point brings it to 10, destroyed, taken off
	give("Padded Armor")
	pgExec(t, `UPDATE characters SET equipped_armor = 'Padded Armor' WHERE id = $1`, charID)
	res = act("Pseudopod")
	if carriedItem(charID, "Padded Armor") != nil || equippedArmorName(charID) != "" || !strings.Contains(res, "destroyed") {
		t.Fatalf("armor at AC 10: still carried %v, worn %q: %s", carriedItem(charID, "Padded Armor") != nil, equippedArmorName(charID), res)
	}

	// a saving throw effect on a metal object: no armor worn, the Shortsword in hand corrodes; a Club (no metal) is spared
	changeHands(charID, `UPDATE characters SET equipped_main_hand = 'Shortsword', equipped_off_hand = NULL WHERE id = $1`, charID)
	attackBefore := characterAttackBonusWith(t, charID, "shortsword")
	res = act("Feelers")
	if corrosion("Shortsword") != 1 || strings.Contains(res, "The object takes a -1 penalty") {
		t.Fatalf("the object effect: corrosion %d: %s", corrosion("Shortsword"), res)
	}
	if after := characterAttackBonusWith(t, charID, "shortsword"); after != attackBefore-1 {
		t.Errorf("the attack bonus with the corroded Shortsword: %d -> %d", attackBefore, after)
	}
	give("Club")
	changeHands(charID, `UPDATE characters SET equipped_main_hand = 'Club' WHERE id = $1`, charID)
	if res = act("Feelers"); corrosion("Club") != 0 || !strings.Contains(res, "no metal") {
		t.Errorf("a Club has no metal: corrosion %d: %s", corrosion("Club"), res)
	}

	// Corrosive Form: the Shortsword that hits the blob corrodes again
	changeHands(charID, `UPDATE characters SET equipped_main_hand = 'Shortsword' WHERE id = $1`, charID)
	hitWith := func() string {
		t.Helper()
		for n := 0; n < 20; n++ {
			pgExec(t, `UPDATE characters SET action_used = false, attacks_remaining = NULL WHERE id = $1`, charID)
			resetTurnFlags(charID, 30)
			_, r := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"character_id": charID, "action": "attack", "target_id": blob, "description": "I stab the blob with my Shortsword"})
			if res := fmt.Sprint(r["result"]); strings.Contains(res, "HIT") {
				return res
			}
		}
		t.Fatalf("no hit on the blob")
		return ""
	}
	res = hitWith()
	if corrosion("Shortsword") != 2 || !strings.Contains(res, "corrodes") {
		t.Fatalf("Corrosive Form: corrosion %d: %s", corrosion("Shortsword"), res)
	}

	// Mending removes it
	if line := mendItem(charID, "mend my shortsword"); corrosion("Shortsword") != 0 || !strings.Contains(line, "Shortsword") {
		t.Errorf("Mending: corrosion %d: %s", corrosion("Shortsword"), line)
	}
	if line := mendItem(charID, "mend my chain mail"); corrosion("Chain Mail") != 0 || !strings.Contains(line, "Chain Mail") {
		t.Errorf("Mending the armor: corrosion %d: %s", corrosion("Chain Mail"), line)
	}
}

// "lob" is a throw only as a word: "the blob" names no throw (the attack above was made improvised and thrown before).
func TestLobIsAWholeWord(t *testing.T) {
	_, w, _ := findWeapon("shortsword")
	if _, improvised := improvisedWeapon("I stab the blob with my Shortsword", "shortsword", w, true); improvised {
		t.Error("\"the blob\" made the Shortsword thrown")
	}
	if iw, improvised := improvisedWeapon("I lob my Shortsword at it", "shortsword", w, true); !improvised || !strings.Contains(iw.Name, "thrown") {
		t.Error("\"lob\" is a throw")
	}
}

// characterAttackBonusWith: the attack bonus the weapon attack owner gives the character with the weapon in hand.
func characterAttackBonusWith(t *testing.T, charID int, weaponKey string) int {
	t.Helper()
	key, w, ok := findWeapon(weaponKey)
	if !ok {
		t.Fatalf("no weapon %s", weaponKey)
	}
	var class string
	var level, str, dex int
	db.QueryRow(`SELECT class, level, str, dex FROM characters WHERE id = $1`, charID).Scan(&class, &level, &str, &dex)
	return weaponAttackMods(weaponUse{charID: charID, class: class, level: level, str: str, dex: dex, key: key, weapon: w, hasWeapon: true}).Attack
}
