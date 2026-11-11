package main

// Eldritch Invocations of the SRD 5.2.1 that need code:
//	Thirsting Blade (5)        Extra Attack with the pact weapon (two attacks); Devouring Blade (12) makes it three
//	Lifedrinker (9)            once per turn, a pact weapon hit deals an extra 1d6 Necrotic, Psychic or Radiant damage (name it in
//	                           the attack; Necrotic by default); "spend a hit die" also rolls one Hit Point Die to regain HP + Con
//	Gift of the Depths (5)     a Swim Speed (underwater attacks) and Water Breathing once per Long Rest without a slot
//	Gift of the Protectors (9) names on a page of the Book of Shadows (`gift_of_the_protectors` action: "add <name>" / "erase <name>");
//	                           a named creature reduced to 0 Hit Points but not killed drops to 1 instead, once per Long Rest
//	Lessons of the First Ones  one Origin feat per copy of the invocation (POST /api/characters/{id}/feat, no ASI points needed)
// Investment of the Chain Master changes the familiar, which the server does not run in combat: the GM applies it.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// pactWeaponAttacks is the number of attacks of the Attack action with the pact weapon (0 = not changed).
func pactWeaponAttacks(charID int, class string) int {
	if (!strings.EqualFold(class, "warlock") && classLevelsOf(charID)["warlock"] == 0) || !hasPactBoon(charID, "blade") { // multiclass too
		return 0
	}
	switch {
	case hasInvocation(charID, "devouring-blade"):
		return 3
	case hasInvocation(charID, "thirsting-blade"):
		return 2
	}
	return 0
}

var lifedrinkerTurn sync.Map // character id -> turn marker

// lifedrinker2024 returns the extra damage of a pact weapon hit and a note (0, "" when it doesn't apply).
func lifedrinker2024(charID int, class string, level int, ranged, crit bool, desc string) (int, string) {
	if !strings.EqualFold(class, "warlock") || level < 9 || ranged || !hasPactBoon(charID, "blade") || !hasInvocation(charID, "lifedrinker") {
		return 0, ""
	}
	turn := combatTurnMarker(lobbyOfChar(charID)) // F190: unique per turn; "round:turn" repeated in the next combat
	if prev, ok := lifedrinkerTurn.Load(charID); ok && prev.(string) == turn && turn != "" {
		return 0, ""
	}
	lifedrinkerTurn.Store(charID, turn)
	onceAttackPending.Store(charID, true) // F147: a miss gives the once-a-turn use back (withdrawHitEffects)
	d := strings.ToLower(desc)
	dtype := "necrotic"
	if strings.Contains(d, "psychic") {
		dtype = "psychic"
	} else if strings.Contains(d, "radiant") {
		dtype = "radiant"
	}
	dmg := game.RollDamage("1d6", crit)
	addRider(charID, dtype, dmg)
	note := fmt.Sprintf(" [Lifedrinker +%d %s]", dmg, dtype)
	if strings.Contains(d, "hit die") || strings.Contains(d, "hit point die") {
		var spent, con int
		db.QueryRow(`SELECT COALESCE(hit_dice_spent,0), con FROM characters WHERE id = $1`, charID).Scan(&spent, &con)
		if spent < level {
			heal := game.RollDie(game.HitDie("warlock")) + game.Modifier(con)
			if heal < 1 {
				heal = 1
			}
			db.Exec(`UPDATE characters SET hit_dice_spent = COALESCE(hit_dice_spent,0) + 1 WHERE id = $1`, charID)
			_, before, after := healCharacter(charID, heal)
			note += fmt.Sprintf(" [a Hit Point Die: +%d HP, %d -> %d]", after-before, before, after)
		} else {
			note += " [no Hit Point Dice left]"
		}
	}
	return dmg, note
}

// protectorsPage is the list of character ids written in a Warlock's Book of Shadows (Gift of the Protectors).
func protectorsPage(warlockID int) []int {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(protectors_page,'[]') FROM characters WHERE id = $1`, warlockID).Scan(&raw)
	var ids []int
	json.Unmarshal(raw, &ids)
	return ids
}

func ensureInvocationColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS protectors_page JSONB DEFAULT '[]'`)
	}
}

// giftOfTheProtectorsAction handles "gift_of_the_protectors add <name>" and "... erase <name>".
func giftOfTheProtectorsAction(charID int, description string) string {
	if !hasInvocation(charID, "gift-of-the-protectors") {
		return "You don't have the Gift of the Protectors invocation."
	}
	var cha, lobby int
	db.QueryRow(`SELECT cha, COALESCE(lobby_id,0) FROM characters WHERE id = $1`, charID).Scan(&cha, &lobby)
	limit := game.Modifier(cha)
	if limit < 1 {
		limit = 1
	}
	d := strings.ToLower(description)
	target := parseTargetFromDescription(description, charID)
	if target <= 0 {
		target = charID
	}
	page := protectorsPage(charID)
	has := false
	var kept []int
	for _, id := range page {
		if id == target {
			has = true
			if strings.Contains(d, "erase") {
				continue
			}
		}
		kept = append(kept, id)
	}
	name := creatureName(lobby, target)
	switch {
	case strings.Contains(d, "erase"):
		if !has {
			return name + " is not written on the page."
		}
		page = kept
	case has:
		return name + " is already written on the page."
	case len(page) >= limit:
		return fmt.Sprintf("The page holds %d names (your Charisma modifier); erase one first.", limit)
	default:
		page = append(page, target)
	}
	b, _ := json.Marshal(page)
	db.Exec(`UPDATE characters SET protectors_page = $1 WHERE id = $2`, string(b), charID)
	var names []string
	for _, id := range page {
		names = append(names, creatureName(lobby, id))
	}
	return "Gift of the Protectors: the page now holds " + strings.Join(names, ", ") + "."
}

// giftOfTheProtectors: a character reduced to 0 Hit Points but not killed outright drops to 1 instead, if a Warlock in its campaign wrote
// its name on the page and the magic has not been used since the Warlock's last Long Rest. It returns the note ("" when it doesn't apply).
func giftOfTheProtectors(charID int) string {
	lobby := lobbyOfChar(charID)
	if lobby == 0 {
		return ""
	}
	rows, err := db.Query(`SELECT id, name FROM characters WHERE lobby_id = $1 AND lower(class) = 'warlock'`, lobby)
	if err != nil {
		return ""
	}
	type w struct {
		id   int
		name string
	}
	var warlocks []w
	for rows.Next() {
		var x w
		if rows.Scan(&x.id, &x.name) == nil {
			warlocks = append(warlocks, x)
		}
	}
	rows.Close()
	for _, x := range warlocks {
		if !hasInvocation(x.id, "gift-of-the-protectors") || classResourcesUsed(x.id)["gift_of_the_protectors"] > 0 {
			continue
		}
		for _, id := range protectorsPage(x.id) {
			if id == charID {
				setUsedResource(x.id, "gift_of_the_protectors", 1)
				db.Exec(`UPDATE characters SET hp = 1 WHERE id = $1`, charID)
				return fmt.Sprintf("Gift of the Protectors: the name in %s's Book of Shadows keeps them at 1 Hit Point.", x.name)
			}
		}
	}
	return ""
}

// lessonsOfTheFirstOnes is how many Origin feats the invocation grants (one per copy) and how many were taken that way.
func lessonsOfTheFirstOnes(charID int) (granted, used int) {
	for _, inv := range getCharacterInvocations(charID) {
		if inv == "lessons-of-the-first-ones" {
			granted++
		}
	}
	used = classResourcesUsed(charID)["lessons_feats"]
	return
}
