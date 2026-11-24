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
	"strconv"
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
	"disengaged":  "Took the Disengage action: its movement doesn't provoke Opportunity Attacks this turn",
}

// addCombatEffect puts an effect on a creature; slow and hamstring replace each other (only the newest applies).
func addCombatEffect(lobbyID, targetID, sourceID int, kind string) {
	addCombatEffectUntil(lobbyID, targetID, sourceID, kind, "")
}

// addCombatEffectUntil adds an effect that ends at a turn boundary its text names (ends): "" the start of the source's next turn (the default),
// "source_end" the end of the source's next turn, "target_start" / "target_end" the start or end of the target's next turn. A turn end named while
// that creature's own turn is running is its next one, not this one (the "_next" form, moved on at this turn's end by expireCombatEffectsAtTurnEnd).
func addCombatEffectUntil(lobbyID, targetID, sourceID int, kind, ends string) {
	switch {
	case ends == "source_end" && currentTurnID(lobbyID) == sourceID:
		ends = "source_end_next"
	case ends == "target_end" && currentTurnID(lobbyID) == targetID:
		ends = "target_end_next"
	}
	if kind == "slow" || kind == "hamstring" {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind IN ('slow','hamstring')`, lobbyID, targetID)
	} else {
		db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND kind = $3`, lobbyID, targetID, kind)
	}
	db.Exec(`INSERT INTO combat_effects (lobby_id, target_id, source_id, kind, text, ends) VALUES ($1,$2,$3,$4,$5,$6)`, lobbyID, targetID, sourceID, kind, effectText[kind], ends)
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

// expireCombatEffectsOf ends the effects a creature caused, at the start of its turn. The exceptions have
// their own lifetimes: recurring_damage and attached last while their gate (the grapple or the attach)
// lasts, and their owner removes them when the gate ends (monster_recurring_damage_2024.go).
func expireCombatEffectsOf(lobbyID, sourceID int) {
	if db == nil {
		return
	}
	// the attach kinds, its suffocating, the swallow and a condition's lasting effects (gate_condition) outlive a turn boundary: their
	// owners end them
	db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND source_id = $2 AND kind NOT IN ('recurring_damage','attached','suffocating','swallowed','shape_form','ablaze','later_damage') AND gate_condition = '' AND ends = ''`, lobbyID, sourceID)
	db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND target_id = $2 AND ends = 'target_start'`, lobbyID, sourceID) // "until the start of its next turn" (Shocking Grasp)
	endConditionsFromSource(lobbyID, fmt.Sprintf("Stunning Strike #%d", sourceID))                                            // Stunned until the start of the Monk's next turn
}

// expireCombatEffectsAtTurnEnd ends the effects that last to the end of this creature's next turn (as source or as target), and makes the ones named
// during this turn ("_next") last to the end of its next one (a turn-end hook, turn_advance_2024.go).
func expireCombatEffectsAtTurnEnd(lobbyID, id int) {
	if db == nil || id == 0 {
		return
	}
	db.Exec(`DELETE FROM combat_effects WHERE lobby_id = $1 AND ((source_id = $2 AND ends = 'source_end') OR (target_id = $2 AND ends = 'target_end'))`, lobbyID, id)
	db.Exec(`UPDATE combat_effects SET ends = 'source_end' WHERE lobby_id = $1 AND source_id = $2 AND ends = 'source_end_next'`, lobbyID, id)
	db.Exec(`UPDATE combat_effects SET ends = 'target_end' WHERE lobby_id = $1 AND target_id = $2 AND ends = 'target_end_next'`, lobbyID, id)
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
	if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(monster), "#")); err == nil && strings.HasPrefix(strings.TrimSpace(monster), "#") {
		for _, c := range combatEntries(lobbyID) { // "#-3": one particular monster of the fight, by its combat id (every caller reads it)
			if m, _ := c["is_monster"].(bool); m && asInt(c["id"]) == n {
				return []int{n}
			}
		}
		return nil
	}
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

// pushToken moves the target's token up to feet feet straight away from the pusher on the encounter's battle map (forceToken,
// creature_movement_2024.go). It returns how many feet it moved and whether there was a map with both tokens.
func pushToken(lobbyID, pusherID, targetID, feet int) (int, bool) {
	return forceToken(lobbyID, pusherID, targetID, feet, false)
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
func adjustRiders(monsterKey string, riders map[string]int) (int, string, map[string]int) {
	total := 0
	amounts := make(map[string]int, len(riders)+2)
	var parts []string
	for dtype, amount := range riders {
		d, how := adjustMonsterDamage(monsterKey, dtype, amount)
		amounts[dtype] = d
		total += d
		switch how {
		case "immune":
			absorbs := false
			if amount > 0 { // an absorption trait turns the rider's damage into Hit Points, unless the form forbids regaining them
				heals, aerr := absorptionHealsHP(db, monsterKey, dtype)
				absorbs = aerr == nil && heals // an unreadable catalog answers "not absorbed" (never a silent regain)
			}
			if absorbs {
				total -= amount // negative: the caller's hp -= total heals; the caller caps at the maximum
				parts = append(parts, fmt.Sprintf("it absorbs the extra %d %s damage and regains that many Hit Points", amount, dtype))
				break
			}
			parts = append(parts, fmt.Sprintf("immune to the extra %s damage", dtype))
		case "resistant":
			parts = append(parts, fmt.Sprintf("%d extra %s damage (it resists)", d, dtype))
		default:
			parts = append(parts, fmt.Sprintf("%d extra %s damage", d, dtype))
		}
	}
	if len(parts) == 0 {
		return 0, "", amounts
	}
	return total, " Plus " + strings.Join(parts, ", ") + ".", amounts
}
