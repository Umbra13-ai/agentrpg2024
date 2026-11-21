package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Imported companion attacks use the same rider parser as monsters. With its
// own initiative entry a companion's source anchor must expire on THAT turn,
// even when the hit happened as a Reaction on its owner's turn.
func TestPostgresCompanionSourceTurnRiderExpires(t *testing.T) {
	testPostgres(t)
	t.Setenv("AGENTRPG_FAMILIAR_INITIATIVE", "own")
	gm, player, camp, char, petID, enemy := companionReactionFixture(t)
	pet, ok := loadCompanion(petID)
	if !ok {
		t.Fatal("missing companion")
	}
	amount := 1
	attacks := []compAttack{{Name: "Timing Strike", Bonus: 30, FlatDamage: &amount, Type: "Force", StatBlockText: "Melee Attack Roll: Automatic hit, reach 5 ft. Hit: 1 Force damage, and the target has the Frightened condition until the end of the creature's next turn."}}
	raw, err := json.Marshal(attacks)
	if err != nil {
		t.Fatal(err)
	}
	// Component setup models an attacking Pact familiar. Familiar creation and
	// legal form selection are separate tests; this asserts its combat path.
	pgExec(t, `UPDATE companions SET kind='familiar',attacks=$1::jsonb WHERE id=$2`, string(raw), petID)
	pgExec(t, `UPDATE characters SET eldritch_invocations='["pact-of-the-chain"]',action_used=false,attacks_remaining=NULL WHERE id=$1`, char)
	addOwnInitiativeEntry(camp, petID)
	_, err = changeFight(camp, func(f *fight) error {
		i, _ := f.entry(char)
		if i < 0 {
			t.Fatal("owner missing")
		}
		f.Index = i
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	code, j := pgCall(t, "POST", "/api/action", player, map[string]interface{}{"companion_id": petID, "action": "attack", "target_id": enemy})
	if code != 200 {
		t.Fatalf("reaction attack: %d %v", code, j)
	}
	recs := loadConditionRecords(camp, enemy)
	found := false
	for _, r := range recs {
		if r.Condition == "frightened" && r.SourceID == pet.CombatID && r.Notes == notesUntilEndOfSourceTurn {
			found = true
		}
	}
	if !found {
		t.Fatalf("source rider not stored with identity: %+v %v", recs, j)
	}
	if notes := conditionOutlastsSourceNotices(camp, enemy, "Opponent"); len(notes) != 0 {
		t.Fatalf("living companion with its own turn reported absent: %v", notes)
	}
	// End an unrelated turn first, then the companion's actual next turn. Setting
	// the slot is fixture setup; effects/advance run through the authenticated route.
	path := fmt.Sprintf("/api/campaigns/%d/combat/next", camp)
	if code, j = pgCall(t, "POST", path, gm, map[string]interface{}{}); code != 200 {
		t.Fatalf("owner turn end: %d %v", code, j)
	}
	if !hasCondMarker(camp, enemy, "frightened") {
		t.Fatal("owner's turn ended the companion's source rider")
	}
	_, err = changeFight(camp, func(f *fight) error {
		i, _ := f.entry(pet.CombatID)
		if i < 0 {
			t.Fatal("companion entry missing")
		}
		f.Index = i
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, j = pgCall(t, "POST", path, gm, map[string]interface{}{}); code != 200 {
		t.Fatalf("companion turn end: %d %v", code, j)
	}
	if hasCondMarker(camp, enemy, "frightened") {
		t.Fatal("source rider survived the companion's next turn end")
	}
}

func TestPostgresCharacterSourceTurnAnchor(t *testing.T) {
	testPostgres(t)
	for _, edge := range []string{"start", "end"} {
		t.Run(edge, func(t *testing.T) {
			gm, _, camp, char, _, enemy := companionReactionFixture(t)
			// A character's imported form can supply the same anchored rider. This
			// component creates it through the shared parser/condition owner; the route
			// below proves source timing, not form construction or the preceding hit.
			text := "Hit: 1 Force damage, and the target has the Frightened condition until the " + edge + " of the creature's next turn."
			e, ok := parseHitRider(text)
			if !ok {
				t.Fatal("fixture rider was not parsed")
			}
			e.SourceID = char
			_, err := changeFight(camp, func(f *fight) error { i, _ := f.entry(char); f.Index = i; return nil })
			if err != nil {
				t.Fatal(err)
			}
			e.giveConditions(camp, enemy, "Invented form's Timing Strike")
			if !hasCondMarker(camp, enemy, "frightened") {
				t.Fatal("control: rider missing")
			}
			if notes := conditionOutlastsSourceNotices(camp, enemy, "Opponent"); len(notes) != 0 {
				t.Fatalf("living character source reported absent: %v", notes)
			}
			path := fmt.Sprintf("/api/campaigns/%d/combat/next", camp)
			next := func() {
				t.Helper()
				if code, j := pgCall(t, "POST", path, gm, map[string]interface{}{}); code != 200 {
					t.Fatalf("next: %d %v", code, j)
				}
			}
			next() // The source's current turn does not end its "next turn" rider.
			if !hasCondMarker(camp, enemy, "frightened") {
				t.Fatal("source rider ended during its current turn")
			}
			for i := 0; currentTurnID(camp) != char; i++ {
				if i > 6 {
					t.Fatal("source turn not reached")
				}
				next()
			}
			if edge == "start" {
				if hasCondMarker(camp, enemy, "frightened") {
					t.Fatal("source-start rider survived its source's next turn start")
				}
				return
			}
			if !hasCondMarker(camp, enemy, "frightened") {
				t.Fatal("end rider ended on source start")
			}
			next()
			if hasCondMarker(camp, enemy, "frightened") {
				t.Fatal("source-end rider survived its source's next turn end")
			}
		})
	}
}

func TestPostgresCompanionWithoutOwnTurnGetsTimingHandoff(t *testing.T) {
	testPostgres(t)
	t.Setenv("AGENTRPG_FAMILIAR_INITIATIVE", "owner")
	gm, player, camp, char, petID, enemy := companionReactionFixture(t)
	pet, _ := loadCompanion(petID)
	r := addCondition(camp, enemy, condAdd{Condition: "frightened", Source: "Invented absent-turn probe", SourceID: pet.CombatID, Notes: notesUntilEndOfSourceTurn})
	if r.Err != nil || !r.Added {
		t.Fatalf("rider: %+v", r)
	}
	code, j := pgCall(t, "GET", fmt.Sprintf("/api/gm/notices?campaign_id=%d", camp), gm, nil)
	if code != 200 || !strings.Contains(fmt.Sprint(j), "individual turn") {
		t.Fatalf("missing immediate timing handoff: %d %v", code, j)
	}
	// The supplied texts do not resolve this server-specific initiative setting;
	// no guess that the owner's turn is the companion's turn is permitted.
	_, err := changeFight(camp, func(f *fight) error { i, _ := f.entry(char); f.Index = i; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if code, j = pgCall(t, "POST", fmt.Sprintf("/api/campaigns/%d/combat/next", camp), gm, map[string]interface{}{}); code != 200 {
		t.Fatalf("owner turn: %d %v", code, j)
	}
	if !hasCondMarker(camp, enemy, "frightened") {
		t.Fatal("owner turn silently shortened companion anchor")
	}
	if code, j = pgCall(t, "GET", fmt.Sprintf("/api/campaigns/%d", camp), player, nil); code != 200 || strings.Contains(fmt.Sprint(j), "individual turn") {
		t.Fatalf("private timing notice in campaign: %d %v", code, j)
	}
}
