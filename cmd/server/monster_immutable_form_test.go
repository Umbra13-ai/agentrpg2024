package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostgresImportedImmutableFormBlocksPolymorph(t *testing.T) {
	for _, tc := range []struct {
		name      string
		immutable bool
		reference bool
	}{
		{name: "mutable control"}, {name: "invented immutable", immutable: true}, {name: "SRD Iron Golem", immutable: true, reference: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reference *srdMonster2024
			if tc.reference {
				example := srdIronGolem(t)
				reference = &example
			}
			f := reviewConcentrationSetup(t, func(m *srdMonster2024) {
				if reference != nil {
					*m = *reference
					m.Name = "Review Focus Caster"
					return
				}
				ability := m.Abilities["wis"]
				ability.Save = -10
				m.Abilities["wis"] = ability
				m.Traits = nil
				if tc.immutable {
					m.Traits = []srdMonsterEntry{{Name: "Immutable Form", Text: "This creature can't shape-shift."}}
				}
			})
			// The route consumes a real level-four slot and resolves the named Wolf
			// form. Character creation and progression are separate concerns here.
			pgExec(t, `UPDATE characters SET class='wizard',level=7,intl=20,known_spells='["polymorph"]',prepared_spells='["polymorph"]',spell_slots_used='{}',inventory=$2::jsonb WHERE id=$1`, f.char, `[{"id":"i1","name":"Component Pouch","type":"focus","spellcasting_focus":true}]`)
			if _, err := changeFight(f.camp, func(fight *fight) error {
				i, _ := fight.entry(f.char)
				if i < 0 {
					return fmt.Errorf("no caster in fight")
				}
				fight.Index = i
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := monsterHPNow(f.camp, f.caster)
			oldKey := monsterKeyOfCombatant(f.camp, f.caster)
			code, j := pgCall(t, "POST", "/api/action", f.player, map[string]interface{}{"action": "cast", "spell_slug": "polymorph", "target_id": f.caster, "description": "Cast Polymorph into a Wolf."})
			if code != 200 {
				t.Fatalf("cast: %d %v", code, j)
			}
			result := strings.ToLower(fmt.Sprint(j["result"]))
			if !strings.Contains(result, "polymorph") {
				t.Fatalf("control: did not reach Polymorph: %v", j)
			}
			if tc.immutable {
				if !strings.Contains(result, "cannot shape-shift") || strings.Contains(result, "wisdom save and becomes") || strings.Contains(result, "vs dc") || monsterKeyOfCombatant(f.camp, f.caster) != oldKey || monsterHPNow(f.camp, f.caster) != before || hasSpellEffect(f.camp, f.caster, "polymorph") {
					t.Fatalf("Immutable Form allowed a save or a form change: %v", j)
				}
			} else if monsterKeyOfCombatant(f.camp, f.caster) != "wolf" || !hasSpellEffect(f.camp, f.caster, "polymorph") {
				t.Fatalf("mutable imported control did not change form: %v", j)
			}
		})
	}
}

func srdIronGolem(t *testing.T) srdMonster2024 {
	t.Helper()
	for _, m := range srdMonsters2024() {
		if m.Name == "Iron Golem" {
			return m
		}
	}
	t.Fatal("the SRD 5.2.1 Iron Golem is missing from the catalog")
	return srdMonster2024{}
}

// Exercise the SRD 5.2.1 Iron Golem through the normal damage endpoint. The
// target starts wounded so immunity and genuine absorption are distinguishable.
func TestPostgresSRDIronGolemFireAbsorption(t *testing.T) {
	iron := srdIronGolem(t)
	f := reviewConcentrationSetup(t, func(m *srdMonster2024) { *m = iron; m.Name = "Review Focus Caster" })
	adjust := func(fields map[string]interface{}) string {
		t.Helper()
		fields["campaign_id"], fields["target_id"] = f.camp, f.caster
		code, j := pgCall(t, "POST", "/api/gm/adjust-monster", f.gm, fields)
		if code != 200 {
			t.Fatalf("adjust: %d %v", code, j)
		}
		return fmt.Sprint(j)
	}
	if iron.HP < 140 {
		t.Fatal("control requires maximum HP of at least 140")
	}
	adjust(map[string]interface{}{"hp": 100})
	fire := adjust(map[string]interface{}{"damage": 40, "damage_type": "fire", "apply_resistances": true})
	if monsterHPNow(f.camp, f.caster) != 140 || !strings.Contains(fire, "absorbs") {
		t.Fatalf("wounded fire absorption did not heal 40 HP: %s", fire)
	}
	adjust(map[string]interface{}{"damage": 40, "damage_type": "poison", "apply_resistances": true})
	if monsterHPNow(f.camp, f.caster) != 140 {
		t.Fatal("poison immunity incorrectly healed or hurt the target")
	}
	adjust(map[string]interface{}{"damage": 40, "damage_type": "cold", "apply_resistances": true})
	if monsterHPNow(f.camp, f.caster) != 100 {
		t.Fatal("unprotected cold damage did not apply")
	}
	adjust(map[string]interface{}{"hp": iron.HP - 1})
	adjust(map[string]interface{}{"damage": 40, "damage_type": "fire", "apply_resistances": true})
	if monsterHPNow(f.camp, f.caster) != iron.HP {
		t.Fatal("absorption exceeded or missed maximum HP")
	}
}
