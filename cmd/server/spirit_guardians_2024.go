package main

// Spirit Guardians (SRD 5.2.1): a 15-foot Emanation around the caster for the duration (Concentration, up to 10 minutes). Whenever the
// Emanation enters a creature's space and whenever a creature enters the Emanation or ends its turn there, the creature makes a
// Wisdom saving throw: 3d8 Radiant (or Necrotic) damage on a failure, half on a success, +1d8 for each slot level above 3. A creature
// makes this save only once per turn.
// Here: when the spell is cast, the monsters it is aimed at (target_ids) take it at once (the Emanation appears in their space). After
// that, an enemy takes it at the end of each of its turns inside the Emanation (with a battle map: within 15 feet of the caster;
// without one: the monsters named when the spell was cast), and when the GM moves a monster into it on the map. It ends with the
// caster's Concentration.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type sgDetail struct {
	Dice    string `json:"dice"`
	Type    string `json:"type"`
	DC      int    `json:"dc"`
	Targets []int  `json:"targets"`
}

var sgSavedTurn sync.Map // "lobby:monster" -> turn marker: the save happens only once per turn

func sgTurnMarker(lobbyID int) string {
	if m := combatTurnMarker(lobbyID); m != "" {
		return m
	}
	return "0:0"
}

// registerSpiritGuardians records the Emanation when the spell is cast and notes that its first targets already made their save.
func registerSpiritGuardians(charID int, dice, dtype string, dc int, targets []int) {
	lobby := lobbyOfChar(charID)
	if lobby == 0 {
		return
	}
	b, _ := json.Marshal(sgDetail{Dice: dice, Type: strings.ToLower(dtype), DC: dc, Targets: targets})
	addSpellEffect(lobby, charID, charID, "Spirit Guardians", "spirit_guardians", string(b), 100, "", 0)
	m := sgTurnMarker(lobby)
	for _, t := range targets {
		sgSavedTurn.Store(fmt.Sprintf("%d:%d", lobby, t), m)
	}
}

type sgActive struct {
	Caster int
	D      sgDetail
}

func activeSpiritGuardians(lobbyID int) []sgActive {
	rows, err := db.Query(`SELECT caster_id, detail FROM spell_effects WHERE lobby_id = $1 AND kind = 'spirit_guardians'`, lobbyID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []sgActive
	for rows.Next() {
		var c int
		var raw string
		if rows.Scan(&c, &raw) != nil {
			continue
		}
		var d sgDetail
		json.Unmarshal([]byte(raw), &d)
		out = append(out, sgActive{c, d})
	}
	// an Emanation whose caster lost Concentration is gone
	var kept []sgActive
	for _, a := range out {
		if concentrationHolds(a.Caster, "Spirit Guardians") {
			kept = append(kept, a)
		} else {
			db.Exec(`DELETE FROM spell_effects WHERE lobby_id = $1 AND caster_id = $2 AND kind = 'spirit_guardians'`, lobbyID, a.Caster)
		}
	}
	return kept
}

// inEmanation: the monster is within 15 feet of the caster on the battle map, or (no map) was one of the spell's targets.
func inEmanation(lobbyID int, a sgActive, monsterID int, m *encounterMap) bool {
	if m != nil {
		c, ok1 := m.token(a.Caster)
		t, ok2 := m.token(monsterID)
		if ok1 && ok2 {
			return tokenDistanceFeet(c, t) <= 15
		}
	}
	for _, id := range a.D.Targets {
		if id == monsterID {
			return true
		}
	}
	return false
}

// spiritGuardiansSave makes the monster's save (once per turn) and applies the damage.
func spiritGuardiansSave(lobbyID int, a sgActive, monsterID int, why string) string {
	key := fmt.Sprintf("%d:%d", lobbyID, monsterID)
	mark := sgTurnMarker(lobbyID)
	if v, ok := sgSavedTurn.Load(key); ok && v.(string) == mark {
		return ""
	}
	sgSavedTurn.Store(key, mark)
	name := monsterName(lobbyID, monsterID)
	dmg := game.RollDamage(a.D.Dice, false)
	saved, total := monsterSpellSaveFor(lobbyID, monsterID, "wis", a.D.DC, saveStakes{Damage: dmg})
	how := fmt.Sprintf("fails its Wisdom save (%d vs DC %d)", total, a.D.DC)
	if saved {
		dmg /= 2
		how = fmt.Sprintf("saves (%d vs DC %d)", total, a.D.DC)
	}
	text, _ := damageMonster(lobbyID, monsterID, dmg, a.D.Type)
	return fmt.Sprintf("👼 Spirit Guardians (%s, %s): %s %s. %s", creatureName(lobbyID, a.Caster), why, name, how, text)
}

// spiritGuardiansTurnEnd: a monster that ends its turn inside an Emanation makes the save.
func spiritGuardiansTurnEnd(lobbyID, monsterID int) []string {
	if monsterID >= 0 {
		return nil
	}
	var log []string
	act := activeSpiritGuardians(lobbyID)
	if len(act) == 0 {
		return nil
	}
	var mp *encounterMap
	if m, ok := activeEncounterMap(lobbyID); ok {
		mp = &m
	}
	for _, a := range act {
		if inEmanation(lobbyID, a, monsterID, mp) {
			if line := spiritGuardiansSave(lobbyID, a, monsterID, "it ends its turn in the spirits"); line != "" {
				log = append(log, line)
			}
		}
	}
	return log
}

// spiritGuardiansEnter: a monster the GM moved into an Emanation on the map makes the save (once per turn).
func spiritGuardiansEnter(lobbyID int, before, after encounterMap) []string {
	act := activeSpiritGuardians(lobbyID)
	if len(act) == 0 {
		return nil
	}
	var log []string
	for _, t := range after.Tokens {
		if t.ID >= 0 {
			continue
		}
		for _, a := range act {
			was := inEmanation(lobbyID, a, t.ID, &before)
			now := inEmanation(lobbyID, a, t.ID, &after)
			if now && !was {
				if line := spiritGuardiansSave(lobbyID, a, t.ID, "it enters the spirits"); line != "" {
					log = append(log, line)
				}
			}
		}
	}
	return log
}
