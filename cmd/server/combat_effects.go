package main

// Short effects that weapon Mastery properties and Barbarian Brutal Strike put on a creature in combat:
//	sap        Disadvantage on its next attack roll (Sap mastery)
//	slow       Speed reduced by 10 feet (Slow mastery); hamstring: by 15 feet (Hamstring Blow)
//	staggering Disadvantage on the next saving throw, and no Opportunity Attacks (Staggering Blow)
//	sundering  the next attack roll by another creature against it gets +5 (Sundering Blow)
// All of them end at the start of the turn of the creature that caused them (a Sap, Slow, Hamstring, Staggering or
// Sundering effect lasts "until the start of your next turn" / "before the start of your next turn"). Sap, Staggering
// and Sundering are also used up by the roll they change. Slow and Hamstring do not stack: the newest replaces the old.
// Players and the GM see the effect names on a monster; the GM also sees what each does.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type combatEffect struct {
	ID       int    `json:"id"`
	TargetID int    `json:"target_id"`
	SourceID int    `json:"source_id"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
}

var effectText = map[string]string{
	"sap":         "Disadvantage on its next attack roll",
	"slow":        "Speed reduced by 10 feet",
	"hamstring":   "Speed reduced by 15 feet",
	"staggering":  "Disadvantage on the next saving throw; can't make Opportunity Attacks",
	"sundering":   "The next attack roll against it by another creature gets a +5 bonus",
	"halved":      "Speed halved",
	"exposed":     "The next attack roll against it has Advantage",
	"addled":      "It can't make Opportunity Attacks until the start of its next turn",
	"dazed":       "On its next turn it can do only one of these: move, take an action, or take a Bonus Action",
	"half_cover":  "Half Cover (Smite of Protection): +2 to AC and Dexterity saving throws",
	"chilled":     "Speed reduced by 10 feet (Frost's Chill)",
	"no_healing":  "Can't regain Hit Points (Chill Touch)",
	"mind_sliver": "Subtracts 1d4 from its next saving throw (Mind Sliver)",
	"wisp_lit":    "Can't benefit from the Invisible condition; sheds Dim Light (Starry Wisp)",
}

func ensureCombatEffectsTable() {
	if db == nil {
		return
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS combat_effects (
		id SERIAL PRIMARY KEY,
		lobby_id INTEGER NOT NULL,
		target_id INTEGER NOT NULL,
		source_id INTEGER NOT NULL,
		kind TEXT NOT NULL,
		text TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS combat_effects_target ON combat_effects (lobby_id, target_id)`)
}

// addCombatEffect puts an effect on a creature; slow and hamstring replace each other (only the newest applies).
func addCombatEffect(lobbyID, targetID, sourceID int, kind string) {
	if kind == "slow" || kind == "hamstring" {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind IN ('slow','hamstring')`, lobbyID, targetID)
	} else {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = $3`, lobbyID, targetID, kind)
	}
	db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text) VALUES ($1,$2,$3,$4,$5)`, lobbyID, targetID, sourceID, kind, effectText[kind])
}

func combatEffectsOn(lobbyID, targetID int) []combatEffect {
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT id, target_id, source_id, kind, text FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 ORDER BY id`, lobbyID, targetID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []combatEffect
	for rows.Next() {
		var e combatEffect
		if rows.Scan(&e.ID, &e.TargetID, &e.SourceID, &e.Kind, &e.Text) == nil {
			out = append(out, e)
		}
	}
	return out
}

// takeCombatEffect uses up an effect on a creature. fromOther, when set, only takes an effect caused by someone else.
func takeCombatEffect(lobbyID, targetID int, kind string, notFromSource int) bool {
	if db == nil {
		return false
	}
	res, err := db.Exec(`DELETE FROM combat_effects WHERE id = (SELECT id FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = $3 AND source_id <> $4 ORDER BY id LIMIT 1)`,
		lobbyID, targetID, kind, notFromSource)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// expireCombatEffectsOf ends the effects a creature caused, at the start of its turn.
func expireCombatEffectsOf(lobbyID, sourceID int) {
	if db == nil {
		return
	}
	db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND source_id = $2`, lobbyID, sourceID)
	db.Exec(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id < 0 AND source = $2`, lobbyID, fmt.Sprintf("Stunning Strike #%d", sourceID)) // Stunned until the start of the Monk's next turn
}

func clearCombatEffects(lobbyID int) {
	if db != nil {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1`, lobbyID)
	}
}

// combatEffectNames are the effect names everyone can see on a creature.
func combatEffectNames(lobbyID, targetID int) []string {
	var out []string
	for _, e := range combatEffectsOn(lobbyID, targetID) {
		out = append(out, e.Kind)
	}
	return out
}

// combatEffectViews are the effects with their meaning, for the GM.
func combatEffectViews(lobbyID, targetID int) []map[string]interface{} {
	var out []map[string]interface{}
	for _, e := range combatEffectsOn(lobbyID, targetID) {
		out = append(out, map[string]interface{}{"effect": e.Kind, "text": e.Text, "caused_by": e.SourceID})
	}
	return out
}

// monsterIDsByKeyOrName finds the combat ids of monsters that match a name or key given by the GM.
func monsterIDsByKeyOrName(lobbyID int, monster string) []int {
	// F208: the best match first: the exact name, then the monster kind, then numbered copies ("Bandit 2"), then other names that begin
	// with it ("Bandit Captain"); before, the turn order decided and "Bandit" could be the Bandit Captain
	var ranked [4][]int
	key := strings.ToLower(strings.ReplaceAll(monster, " ", "-"))
	for _, c := range combatEntries(lobbyID) {
		if m, _ := c["is_monster"].(bool); !m {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(sprintAny(c["name"])), " ", "-"))
		mk := strings.ToLower(sprintAny(c["monster_key"]))
		id := asInt(c["id"])
		switch {
		case key == name:
			ranked[0] = append(ranked[0], id)
		case key == mk:
			ranked[1] = append(ranked[1], id)
		case strings.HasPrefix(name, key+"-") && strings.Trim(name[len(key)+1:], "0123456789") == "":
			ranked[2] = append(ranked[2], id)
		case strings.HasPrefix(name, key):
			ranked[3] = append(ranked[3], id)
		}
	}
	var out []int
	for _, r := range ranked {
		out = append(out, r...)
	}
	return out
}

func combatEntries(lobbyID int) []map[string]interface{} {
	if db == nil {
		return nil
	}
	var raw []byte
	if db.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw) != nil {
		return nil
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	return entries
}

func sprintAny(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// pushToken moves the target's token up to feet feet straight away from the pusher on the encounter's battle map.
// It returns how many feet it moved and whether there was a map with both tokens. A wall, a closed door or another
// creature stops it. The map's revision changes so stale path submissions are refused.
func pushToken(lobbyID, pusherID, targetID, feet int) (int, bool) {
	var raw []byte
	if db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw) != nil || len(raw) == 0 {
		return 0, false
	}
	var m encounterMap
	if json.Unmarshal(raw, &m) != nil {
		return 0, false
	}
	var a, b *mapToken
	for i := range m.Tokens {
		switch m.Tokens[i].ID {
		case pusherID:
			a = &m.Tokens[i]
		case targetID:
			b = &m.Tokens[i]
		}
	}
	if a == nil || b == nil {
		return 0, false
	}
	sign := func(n int) int {
		switch {
		case n > 0:
			return 1
		case n < 0:
			return -1
		}
		return 0
	}
	dx, dy := sign(b.Position.X-a.Position.X), sign(b.Position.Y-a.Position.Y)
	if dx == 0 && dy == 0 {
		return 0, true
	}
	moved := 0
	for step := 0; step < feet/battleMapStepFeet; step++ {
		next := mapSquare{b.Position.X + dx, b.Position.Y + dy}
		free := true
		for _, sq := range b.footprint(next) {
			if blockedTile(m.tile(sq)) {
				free = false
			}
			for i := range m.Tokens {
				if m.Tokens[i].ID == b.ID {
					continue
				}
				for _, o := range m.Tokens[i].footprint(m.Tokens[i].Position) {
					if o == sq {
						free = false
					}
				}
			}
		}
		if !free {
			break
		}
		b.Position = next
		moved += battleMapStepFeet
	}
	if moved > 0 {
		m.Revision = mapRevision()
		out, _ := json.Marshal(m)
		db.Exec(`UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2`, string(out), lobbyID)
	}
	return moved, true
}

// Damage riders: extra damage of another type that an attack carries (Radiant Strikes and Divine Smite are Radiant,
// Primal Strike is Cold, Fire, Lightning or Thunder). They are part of the damage total of the attack roll code; against a
// monster they are split off so the monster's resistances and immunities apply to each type on its own.
var riderDamage sync.Map // character id -> map[type]amount

func addRider(charID int, dtype string, amount int) {
	if amount <= 0 {
		return
	}
	m := map[string]int{}
	if v, ok := riderDamage.Load(charID); ok {
		for k, n := range v.(map[string]int) {
			m[k] = n
		}
	}
	m[strings.ToLower(dtype)] += amount
	riderDamage.Store(charID, m)
}

func takeRiders(charID int) map[string]int {
	v, ok := riderDamage.LoadAndDelete(charID)
	if !ok {
		return nil
	}
	return v.(map[string]int)
}

// adjustRiders applies a monster's resistances to each rider and says what they did.
func adjustRiders(monsterKey string, riders map[string]int) (int, string) {
	total := 0
	var parts []string
	for dtype, amount := range riders {
		d, how := adjustMonsterDamage(monsterKey, dtype, amount)
		total += d
		switch how {
		case "immune":
			parts = append(parts, fmt.Sprintf("immune to the extra %s damage", dtype))
		case "resistant":
			parts = append(parts, fmt.Sprintf("%d extra %s damage (it resists)", d, dtype))
		default:
			parts = append(parts, fmt.Sprintf("%d extra %s damage", d, dtype))
		}
	}
	if len(parts) == 0 {
		return 0, ""
	}
	return total, " Plus " + strings.Join(parts, ", ") + "."
}
