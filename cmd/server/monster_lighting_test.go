package main

import (
	"fmt"
	"testing"
)

func TestPostgresMappedMonsterLightTiers(t *testing.T) {
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
		m.Traits = []srdMonsterEntry{{Name: "Invented Beacon", Text: "The creature sheds Bright Light in a 10-foot radius and Dim Light for an additional 10 feet."}}
	})
	bright := reviewSaveTarget(t, f, "Bright target", -10, "", "")
	dim := reviewSaveTarget(t, f, "Dim target", -10, "", "")
	outside := reviewSaveTarget(t, f, "Outside light", -10, "", "")
	behindWall := reviewSaveTarget(t, f, "Light shadow", -10, "", "")
	body := encounterMap{Grid: []string{"......", "#.....", "......"}, Tokens: []mapToken{
		{ID: f.caster, Name: "Invented Beacon", Position: mapSquare{0, 0}, Size: 1},
		{ID: bright, Name: "Bright target", Position: mapSquare{2, 0}, Size: 1},
		{ID: dim, Name: "Dim target", Position: mapSquare{4, 0}, Size: 1},
		{ID: outside, Name: "Outside light", Position: mapSquare{5, 0}, Size: 1},
		{ID: behindWall, Name: "Light shadow", Position: mapSquare{0, 2}, Size: 1},
	}}
	path := fmt.Sprintf("/api/campaigns/%d/battle-map", f.camp)
	if code, j := pgCall(t, "PUT", path, f.gm, map[string]interface{}{"map": body, "forced_movement": true}); code != 200 {
		t.Fatalf("place light controls: %d %v", code, j)
	}
	if code, j := pgCall(t, "POST", "/api/gm/set-lighting", f.gm, map[string]interface{}{"campaign_id": f.camp, "lighting": "darkness"}); code != 200 {
		t.Fatalf("set ambient lighting: %d %v", code, j)
	}
	for _, tc := range []struct {
		target int
		want   string
	}{{bright, "bright"}, {dim, "dim"}, {outside, "darkness"}, {behindWall, "darkness"}, {f.char, "darkness"}} {
		if got := monsterLightingAt(f.camp, tc.target); got != tc.want {
			t.Errorf("target %d: lighting %s, want %s", tc.target, got, tc.want)
		}
	}
	if code, j := pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, map[string]interface{}{
		"campaign_id": f.camp, "target_id": f.caster, "hp": 0,
	}); code != 200 {
		t.Fatalf("remove creature's light source: %d %v", code, j)
	}
	if got := monsterLightingAt(f.camp, bright); got != "darkness" {
		t.Errorf("former Bright area after defeat: %s", got)
	}
}
