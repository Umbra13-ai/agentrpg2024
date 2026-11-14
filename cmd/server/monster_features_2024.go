package main

// Monster features that act on their own timing (SRD 5.2.1 stat blocks): Legendary Actions, Legendary Resistance, lairs, auras,
// Regeneration and Recharge. Designed together: all of them hook into the turn order (turnEffects in condition_turns.go and
// combat/next) and keep their state per monster per fight in combat_monster_state (cleared when a fight starts or ends).
// Legendary Actions ("Legendary Action Uses: 3 (4 in Lair)"). Taken immediately after another creature's turn ends, one at a time,
// never while the monster is Incapacitated; each costs one use; the uses come back at the start of the monster's turn; an action
// that says so can't be taken again until the start of its next turn. The GM chooses (the server never picks for the monster):
//   - POST /api/campaigns/{id}/combat/next {"legendary_actions": [{"combatant_id": -2, "action": "Pounce", "target": "Mara"}]}
//     takes them right after the turn that is ending, before the next creature's turn starts;
//   - POST /api/gm/legendary-action {"combatant_id": -2, "action": "Cloud of Insects", "target_ids": [12]} takes one after the turn
//     that ended last.
//   The combat/next reply lists the legendary options open after the turn that just ended. The server resolves the action's text
//   (monster_effects_2024.go): an attack, another action, a saving throw effect, a spell, damage; movement is left to the GM.
// Legendary Resistance ("(3/Day, or 4/Day in Lair)"). When a monster fails a saving throw the server rolls, it can choose to succeed.
//   Policy per monster (POST /api/gm/monster-features {"legendary_resistance": ...}): "auto" (default) uses it against an effect that
//   gives a condition, or damage that would drop the monster to 0 Hit Points; "always" on every failed save; "manual" never by itself
//   (POST /api/gm/legendary-resistance counts a use, and the GM removes the effect).
// Lairs. The SRD 5.2.1 has no Lair Actions: a monster in its lair has one more Legendary Action use and one more
//   Legendary Resistance (the "in Lair" numbers) and is worth the lair XP. combat/add {"in_lair": true} or gm/monster-features.
// Auras (traits):
//   - at the start of each character's turn: "<Ability> Saving Throw: DC N, any enemy that starts its turn in the aura / in a N-foot
//     Emanation / within N feet" (Pit Fiend Fear Aura, Ghast and Hezrou Stench, Sea Hag Vile Appearance, Gibbering Mouther). A
//     condition "until the start of its next turn" ends then; a success that gives immunity lasts for the rest of the fight.
//   - at the end of the monster's own turn: "each creature in a N-foot Emanation ... takes D damage" (Balor, Fire Elemental with
//     Burning, Remorhaz, Azer Sentinel, Salamander).
//   - Aura of Authority (Hobgoblin Captain): it and its allies within 10 feet have Advantage on attack rolls and saving throws.
//   Who is in an aura: with a battle map, the distance; without one, an aura of 10 feet or less reaches characters in melee range
//   and a larger one every character in the fight. Auras affect characters (the party), not the monster's allies. The GM can switch
//   a monster's auras off (gm/monster-features {"auras": false}).
// Regeneration: "regains N Hit Points at the start of each of its turns" (Troll, Troll Limb, Oni, Shield Guardian), not on the turn
//   after Acid or Fire damage when the trait says so.
// Recharge: an action marked (Recharge X–Y) used through gm/narrate is spent until a d6 at the start of the monster's turn rolls X+.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// ---- stat block data (from the embedded SRD monsters)

type monsterMeta struct {
	Key, Name                      string
	Traits, Actions, BonusActions  []srdMonsterEntry
	Reactions, Legendary           []srdMonsterEntry
	LAUses, LAUsesLair, LR, LRLair int
	XP, XPLair                     int
}

var (
	monsterMetaOnce sync.Once
	monsterMetaMap  map[string]*monsterMeta
	laUsesRe        = regexp.MustCompile(`Legendary Action Uses: (\d+)(?: \((\d+) in Lair\))?`)
	lrRe            = regexp.MustCompile(`Legendary Resistance \((\d+)/Day(?:, or (\d+)/Day in Lair)?\)`)
	rechargeRe      = regexp.MustCompile(`\(Recharge (\d)(?:[–-]6)?\)`)
	regenRe         = regexp.MustCompile(`regains (\d+) Hit Points at the start of each of its turns`)
	regenBlockRe    = regexp.MustCompile(`takes ([A-Z][a-z]+) or ([A-Z][a-z]+) damage, this trait doesn`)
	startAuraRe     = regexp.MustCompile(`(?:any|each) (enemy|creature|Beast or Humanoid)[^.]*?starts its turn (?:in the aura|in an? (\d+)-foot Emanation|within (\d+) feet)`)
	endAuraRe       = regexp.MustCompile(`At the end of each of the [^,]+ turns, each creature( of the [^ ]+ choice)? in an? (\d+)-foot Emanation originating from the [^ ]+ takes (\d+) \(([^)]+)\) ([A-Za-z]+) damage`)
	legendaryOnceRe = regexp.MustCompile(`can’t take this action again until the start of its next turn|can't take this action again until the start of its next turn`)
)

func monsterMetaFor(key string) *monsterMeta {
	monsterMetaOnce.Do(func() {
		monsterMetaMap = map[string]*monsterMeta{}
		for _, m := range srdMonsters2024() {
			mm := &monsterMeta{Key: monsterSlug(m.Name), Name: m.Name, Traits: m.Traits, Actions: m.Actions, BonusActions: m.BonusActions,
				Reactions: m.Reactions, Legendary: m.Legendary, XP: m.XP, XPLair: m.XPLair}
			if x := laUsesRe.FindStringSubmatch(m.LegendaryIn); x != nil {
				mm.LAUses, _ = strconv.Atoi(x[1])
				mm.LAUsesLair = mm.LAUses
				if x[2] != "" {
					mm.LAUsesLair, _ = strconv.Atoi(x[2])
				}
			}
			for _, t := range m.Traits {
				if x := lrRe.FindStringSubmatch(t.Name); x != nil {
					mm.LR, _ = strconv.Atoi(x[1])
					mm.LRLair = mm.LR
					if x[2] != "" {
						mm.LRLair, _ = strconv.Atoi(x[2])
					}
				}
			}
			monsterMetaMap[mm.Key] = mm
		}
	})
	return monsterMetaMap[strings.ToLower(strings.TrimSpace(key))]
}

// ---- per-fight state

type monsterState struct {
	InLair        bool     `json:"in_lair"`
	LRPolicy      string   `json:"legendary_resistance_policy"`
	LRUsed        int      `json:"legendary_resistances_used"`
	LAUsed        int      `json:"legendary_actions_used"`
	LAWindow      string   `json:"-"`
	LAOnce        []string `json:"-"`
	AurasOff      bool     `json:"auras_off"`
	RechargeSpent []string `json:"recharge_spent"`
	AuraImmune    []int    `json:"aura_immune"`
	RegenBlocked  bool     `json:"-"`
}

func loadMonsterState(lobbyID, id int) monsterState {
	s := monsterState{LRPolicy: "auto"}
	if db == nil {
		return s
	}
	var once, spent, immune []byte
	db.QueryRow(`SELECT in_lair, lr_policy, lr_used, la_used, la_window, la_once, auras_off, recharge_spent, aura_immune, regen_blocked
		FROM combat_monster_state WHERE lobby_id = $1 AND monster_id = $2`, lobbyID, id).
		Scan(&s.InLair, &s.LRPolicy, &s.LRUsed, &s.LAUsed, &s.LAWindow, &once, &s.AurasOff, &spent, &immune, &s.RegenBlocked)
	json.Unmarshal(once, &s.LAOnce)
	json.Unmarshal(spent, &s.RechargeSpent)
	json.Unmarshal(immune, &s.AuraImmune)
	if s.LRPolicy == "" {
		s.LRPolicy = "auto"
	}
	return s
}

func saveMonsterState(lobbyID, id int, s monsterState) {
	if db == nil {
		return
	}
	once, _ := json.Marshal(nonNil(s.LAOnce))
	spent, _ := json.Marshal(nonNil(s.RechargeSpent))
	immune, _ := json.Marshal(s.AuraImmune)
	if s.AuraImmune == nil {
		immune = []byte("[]")
	}
	db.Exec(`INSERT INTO combat_monster_state (lobby_id, monster_id, in_lair, lr_policy, lr_used, la_used, la_window, la_once, auras_off, recharge_spent, aura_immune, regen_blocked)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (lobby_id, monster_id) DO UPDATE SET in_lair=EXCLUDED.in_lair, lr_policy=EXCLUDED.lr_policy, lr_used=EXCLUDED.lr_used,
			la_used=EXCLUDED.la_used, la_window=EXCLUDED.la_window, la_once=EXCLUDED.la_once, auras_off=EXCLUDED.auras_off,
			recharge_spent=EXCLUDED.recharge_spent, aura_immune=EXCLUDED.aura_immune, regen_blocked=EXCLUDED.regen_blocked`,
		lobbyID, id, s.InLair, s.LRPolicy, s.LRUsed, s.LAUsed, s.LAWindow, string(once), s.AurasOff, string(spent), string(immune), s.RegenBlocked)
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// clearMonsterStates forgets the monster features of a fight (ids are reused by the next fight).
func clearMonsterStates(lobbyID int) {
	if db != nil {
		db.Exec(`DELETE FROM combat_monster_state WHERE lobby_id = $1`, lobbyID)
	}
	clearMonsterReactionState(lobbyID) // Reaction uses per day and the last hits (monster_reactions_2024.go)
}

// monsterCombatant finds a monster in the fight: its entry, stat block key and data.
func monsterCombatant(lobbyID, id int) (map[string]interface{}, *monsterMeta, bool) {
	if id >= 0 {
		return nil, nil, false
	}
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			return e, monsterMetaFor(sprintAny(e["monster_key"])), true
		}
	}
	return nil, nil, false
}

func legendaryUseTotals(meta *monsterMeta, s monsterState) (la, lr int) {
	if meta == nil {
		return 0, 0
	}
	if s.InLair {
		return meta.LAUsesLair, meta.LRLair
	}
	return meta.LAUses, meta.LR
}

// ---- Legendary Resistance

// saveStakes is what a saving throw decides, for the "auto" Legendary Resistance policy.
type saveStakes struct {
	Condition bool // a failure gives a condition (or worse)
	Damage    int  // the damage of a failure
}

// legendaryResistance: the monster failed a save; it chooses to succeed when it has a use left and its policy says so.
func legendaryResistance(lobbyID, id int, st saveStakes) bool {
	e, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil || meta.LR == 0 {
		return false
	}
	s := loadMonsterState(lobbyID, id)
	_, total := legendaryUseTotals(meta, s)
	if s.LRUsed >= total {
		return false
	}
	switch s.LRPolicy {
	case "manual":
		return false
	case "always":
	default: // auto
		if !st.Condition && !(st.Damage > 0 && st.Damage >= asInt(e["hp"])) {
			return false
		}
	}
	s.LRUsed++
	saveMonsterState(lobbyID, id, s)
	line := fmt.Sprintf("🛡️ %s fails the saving throw and chooses to succeed instead (Legendary Resistance).", sprintAny(e["name"]))
	addSaveNote(lobbyID, fmt.Sprintf("[%s fails the save but uses Legendary Resistance to succeed: %d of %d left today.]", sprintAny(e["name"]), total-s.LRUsed, total))
	recordGMAction(db, lobbyID, "legendary_resistance", line, fmt.Sprintf("%d of %d Legendary Resistance uses left today.", total-s.LRUsed, total))
	return true
}

// ---- Legendary Actions

type legendaryRequest struct {
	CombatantID int    `json:"combatant_id"`
	Action      string `json:"action"`
	ActionName  string `json:"action_name"` // older name of the field
	Target      string `json:"target"`
	TargetIDs   []int  `json:"target_ids"`
	Use         string `json:"use"`          // "makes one Claw or Tail attack": which one
	ApplyDamage *bool  `json:"apply_damage"` // default true
}

// lastTurnEnded is the turn after which Legendary Actions may be taken now ("round:index") and whose turn it was.
func lastTurnEnded(lobbyID int) (string, int) {
	var marker string
	var id int
	db.QueryRow(`SELECT COALESCE(last_turn_ended,''), COALESCE(last_turn_ended_id,0) FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&marker, &id)
	return marker, id
}

func setLastTurnEnded(lobbyID int, marker string, id int) {
	db.Exec(`UPDATE combat_state SET last_turn_ended = $1, last_turn_ended_id = $2 WHERE lobby_id = $3`, marker, id, lobbyID)
}

// markCurrentTurnEnded records that the current turn ends (a turn advanced by gm/narrate).
func markCurrentTurnEnded(lobbyID int) {
	var round, idx int
	var raw []byte
	if db.QueryRow(`SELECT COALESCE(round_number,1), COALESCE(current_turn_index,0), turn_order FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&round, &idx, &raw) != nil {
		return
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	if idx >= 0 && idx < len(entries) {
		setLastTurnEnded(lobbyID, fmt.Sprintf("%d:%d", round, idx), asInt(entries[idx]["id"]))
	}
}

func findLegendary(meta *monsterMeta, name string) (srdMonsterEntry, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, a := range meta.Legendary {
		if strings.ToLower(a.Name) == name {
			return a, true
		}
	}
	for _, a := range meta.Legendary {
		if strings.HasPrefix(strings.ToLower(a.Name), name) && name != "" {
			return a, true
		}
	}
	return srdMonsterEntry{}, false
}

// takeLegendaryAction checks and resolves one Legendary Action after the turn identified by window (whose creature was endingID).
func takeLegendaryAction(r *http.Request, lobbyID int, req legendaryRequest, window string, endingID int) (map[string]interface{}, string, string) {
	if req.Action == "" {
		req.Action = req.ActionName
	}
	e, meta, ok := monsterCombatant(lobbyID, req.CombatantID)
	if !ok {
		return nil, "combatant_not_found", fmt.Sprintf("No monster with id %d in this fight.", req.CombatantID)
	}
	name := sprintAny(e["name"])
	if meta == nil || len(meta.Legendary) == 0 {
		return nil, "no_legendary_actions", name + " has no Legendary Actions."
	}
	if asInt(e["hp"]) <= 0 {
		return nil, "defeated", name + " is defeated."
	}
	if monsterIncapacitated(lobbyID, req.CombatantID) {
		return nil, "incapacitated", name + " is Incapacitated and can't take Legendary Actions."
	}
	if window == "" {
		return nil, "no_turn_ended", "Legendary Actions are taken immediately after another creature's turn; no turn has ended in this fight yet."
	}
	if endingID == req.CombatantID {
		return nil, "own_turn", name + " takes Legendary Actions after another creature's turn, not after its own."
	}
	s := loadMonsterState(lobbyID, req.CombatantID)
	total, _ := legendaryUseTotals(meta, s)
	if s.LAWindow == window {
		return nil, "one_at_a_time", name + " already took a Legendary Action after this turn (one at a time, after each other creature's turn)."
	}
	if s.LAUsed >= total {
		return nil, "no_uses_left", fmt.Sprintf("%s has used all %d Legendary Action uses; they come back at the start of its turn.", name, total)
	}
	act, found := findLegendary(meta, req.Action)
	if !found {
		var names []string
		for _, a := range meta.Legendary {
			names = append(names, a.Name)
		}
		return map[string]interface{}{"available": names}, "action_not_found", fmt.Sprintf("%q is not a Legendary Action of %s.", req.Action, name)
	}
	if containsStr(s.LAOnce, act.Name) {
		return nil, "not_again_yet", fmt.Sprintf("%s can't take %s again until the start of its next turn.", name, act.Name)
	}
	apply := req.ApplyDamage == nil || *req.ApplyDamage
	targets := targetIDsFrom(lobbyID, req.Target, req.TargetIDs)
	lines := resolveMonsterEffectText(r, lobbyID, req.CombatantID, meta, act.Name, act.Text, targets, req.Use, apply, 0)
	s.LAUsed++
	s.LAWindow = window
	if legendaryOnceRe.MatchString(act.Text) {
		s.LAOnce = append(s.LAOnce, act.Name)
	}
	saveMonsterState(lobbyID, req.CombatantID, s)
	text := fmt.Sprintf("⚡ Legendary Action: %s uses %s. %s", name, act.Name, strings.Join(lines, " "))
	recordGMAction(db, lobbyID, "legendary_action", text, fmt.Sprintf("%d of %d Legendary Action uses left.", total-s.LAUsed, total))
	return map[string]interface{}{"combatant": name, "action": act.Name, "result": text, "uses_left": total - s.LAUsed, "uses": total}, "", ""
}

// legendaryOptions lists the monsters that may take a Legendary Action now (after the turn in window) and what they can take.
func legendaryOptions(lobbyID int, window string, endingID int) []map[string]interface{} {
	var out []map[string]interface{}
	for _, e := range combatEntries(lobbyID) {
		id := asInt(e["id"])
		meta := monsterMetaFor(sprintAny(e["monster_key"]))
		if id >= 0 || meta == nil || len(meta.Legendary) == 0 || asInt(e["hp"]) <= 0 || id == endingID || monsterIncapacitated(lobbyID, id) {
			continue
		}
		s := loadMonsterState(lobbyID, id)
		total, _ := legendaryUseTotals(meta, s)
		if s.LAUsed >= total || s.LAWindow == window {
			continue
		}
		var acts []map[string]interface{}
		for _, a := range meta.Legendary {
			if !containsStr(s.LAOnce, a.Name) {
				acts = append(acts, map[string]interface{}{"name": a.Name, "text": a.Text})
			}
		}
		out = append(out, map[string]interface{}{"combatant_id": id, "name": e["name"], "uses_left": total - s.LAUsed, "actions": acts,
			"how": fmt.Sprintf(`POST /api/gm/legendary-action {"combatant_id": %d, "action": "<name>", "target": "<character>"} (or in the next combat/next as legendary_actions)`, id)})
	}
	return out
}

// openLegendaryOptions: the Legendary Actions that may be taken now (after the turn that ended last) and a task line for the GM.
// GM status, combat/next, gm/narrate with advance_turn and combat/skip show them, so the GM is told after every turn change.
func openLegendaryOptions(lobbyID int) ([]map[string]interface{}, string) {
	window, endingID := lastTurnEnded(lobbyID)
	if window == "" {
		return nil, ""
	}
	opts := legendaryOptions(lobbyID, window, endingID)
	if len(opts) == 0 {
		return nil, ""
	}
	ended := creatureName(lobbyID, endingID)
	var parts []string
	for _, o := range opts {
		var names []string
		for _, a := range o["actions"].([]map[string]interface{}) {
			names = append(names, fmt.Sprint(a["name"]))
		}
		parts = append(parts, fmt.Sprintf("%v (id %v, %v left: %s)", o["name"], o["combatant_id"], o["uses_left"], strings.Join(names, ", ")))
	}
	task := fmt.Sprintf("⚡ Legendary Action available (optional) after %s's turn: %s. Take it now, before the current creature acts: POST /api/gm/legendary-action {\"combatant_id\": N, \"action\": \"<name>\", \"target\": \"<character>\"}, or put it in the next combat/next as legendary_actions.", ended, strings.Join(parts, "; "))
	return opts, task
}

// ---- turn hooks (called from turnEffects)

// monsterFeaturesTurnStart: the start of a creature's turn. A monster regains its Legendary Action uses, regenerates and rolls its
// recharges; a character makes the saves of the auras it starts its turn in. Conditions "until the start of its next turn" end.
func monsterFeaturesTurnStart(lobbyID, id int, name string) []string {
	if db == nil {
		return nil
	}
	var log []string
	rows, err := db.Query(`DELETE FROM condition_records WHERE lobby_id = $1 AND target_id = $2 AND notes = 'until_start_of_next_turn' RETURNING condition`, lobbyID, id)
	if err == nil {
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				log = append(log, fmt.Sprintf("The %s condition on %s ends (start of its turn).", c, name))
			}
		}
		rows.Close()
		if id > 0 {
			syncListFromRecords(id)
		}
	}
	if id < 0 {
		e, meta, ok := monsterCombatant(lobbyID, id)
		if !ok || meta == nil {
			return log
		}
		s := loadMonsterState(lobbyID, id)
		changed := s.LAUsed > 0 || len(s.LAOnce) > 0
		s.LAUsed, s.LAOnce = 0, nil
		// Regeneration
		for _, t := range meta.Traits {
			m := regenRe.FindStringSubmatch(t.Text)
			if m == nil {
				continue
			}
			hp, maxHP := asInt(e["hp"]), asInt(e["max_hp"])
			switch {
			case s.RegenBlocked:
				log = append(log, fmt.Sprintf("%s doesn't regenerate this turn (it took %s).", name, regenBlockTypes(t.Text)))
				s.RegenBlocked, changed = false, true
			case hp >= 1 && hp < maxHP:
				n, _ := strconv.Atoi(m[1])
				newHP := hp + n
				if newHP > maxHP {
					newHP = maxHP
				}
				setMonsterHP(lobbyID, id, newHP)
				log = append(log, fmt.Sprintf("%s regenerates %d Hit Points (%s).", name, newHP-hp, t.Name))
			}
		}
		// Recharge
		var kept []string
		for _, a := range s.RechargeSpent {
			need := rechargeNeed(meta, a)
			roll := game.RollDie(6)
			if need > 0 && roll >= need {
				log = append(log, fmt.Sprintf("%s's %s recharges (d6: %d).", name, a, roll))
				changed = true
			} else {
				kept = append(kept, a)
			}
		}
		if len(kept) != len(s.RechargeSpent) {
			s.RechargeSpent = kept
		}
		if changed || len(kept) > 0 {
			saveMonsterState(lobbyID, id, s)
		}
		return log
	}
	// a character starts its turn: auras that ask for a save
	for _, e := range combatEntries(lobbyID) {
		mid := asInt(e["id"])
		meta := monsterMetaFor(sprintAny(e["monster_key"]))
		if mid >= 0 || meta == nil || asInt(e["hp"]) <= 0 {
			continue
		}
		s := loadMonsterState(lobbyID, mid)
		if s.AurasOff {
			continue
		}
		for _, t := range meta.Traits {
			m := startAuraRe.FindStringSubmatch(t.Text)
			if m == nil {
				continue
			}
			eff, ok := parseSaveEffect(t.Text)
			if !ok {
				continue
			}
			rng := eff.Emanation
			if m[2] != "" {
				rng, _ = strconv.Atoi(m[2])
			} else if m[3] != "" {
				rng, _ = strconv.Atoi(m[3])
			}
			if strings.Contains(t.Text, "Incapacitated") && monsterIncapacitated(lobbyID, mid) {
				continue
			}
			if containsInt(s.AuraImmune, id) || !inMonsterAura(lobbyID, mid, id, rng) {
				continue
			}
			lines, immune := applySaveEffect(lobbyID, sprintAny(e["name"])+" ("+t.Name+")", eff, []int{id}, true)
			if len(immune) > 0 {
				s.AuraImmune = append(s.AuraImmune, immune...)
				saveMonsterState(lobbyID, mid, s)
			}
			log = append(log, fmt.Sprintf("%s starts its turn in %s's %s: %s", name, sprintAny(e["name"]), t.Name, strings.Join(lines, " ")))
		}
	}
	return log
}

// monsterFeaturesTurnEnd: the end of a monster's turn: auras that deal damage around it.
func monsterFeaturesTurnEnd(lobbyID, id int, name string) []string {
	if id >= 0 {
		return nil
	}
	e, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil || asInt(e["hp"]) <= 0 || loadMonsterState(lobbyID, id).AurasOff {
		return nil
	}
	var log []string
	for _, t := range meta.Traits {
		m := endAuraRe.FindStringSubmatch(t.Text)
		if m == nil {
			continue
		}
		if strings.Contains(t.Text, "Incapacitated") && monsterIncapacitated(lobbyID, id) {
			continue
		}
		rng, _ := strconv.Atoi(m[2])
		dmg := game.RollDamage(normDice(m[4]), false)
		dtype := strings.ToLower(m[5])
		var hit []string
		for _, cid := range charactersInFight(lobbyID) {
			if !inMonsterAura(lobbyID, id, cid, rng) {
				continue
			}
			res := characterNameOf(cid) + " " + damageCharacter(cid, dmg, dtype)
			if strings.Contains(t.Text, "start burning") {
				startBurning(lobbyID, cid, name+" ("+t.Name+")")
				res += ", and starts burning"
			}
			hit = append(hit, res)
		}
		if len(hit) > 0 {
			log = append(log, fmt.Sprintf("🔥 %s's %s: %s.", name, t.Name, strings.Join(hit, "; ")))
		}
	}
	return log
}

// charactersInFight: each character in the turn order once (a Thief's Reflexes second turn is a second entry with the same id;
// companions have ids from companionIDBase and aren't characters).
func charactersInFight(lobbyID int) []int {
	var out []int
	seen := map[int]bool{}
	for _, e := range combatEntries(lobbyID) {
		if id := asInt(e["id"]); id > 0 && id < companionIDBase && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func containsInt(l []int, v int) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// inMonsterAura: is the character within feet of the monster? With a battle map, the distance; without one, an aura of 10 feet or
// less reaches characters in melee range and a larger one everyone in the fight.
func inMonsterAura(lobbyID, monsterID, charID, feet int) bool {
	if m, ok := activeEncounterMap(lobbyID); ok {
		a, ok1 := m.token(monsterID)
		b, ok2 := m.token(charID)
		return ok1 && ok2 && tokenDistanceFeet(a, b) <= feet
	}
	if feet <= 10 {
		return isInMeleeRange(charID)
	}
	return true
}

// auraOfAuthority: a Hobgoblin Captain's Aura of Authority gives the monster Advantage on attack rolls and saving throws.
func auraOfAuthority(lobbyID, monsterID int) bool {
	if monsterID >= 0 {
		return false
	}
	mp, hasMap := activeEncounterMap(lobbyID)
	for _, e := range combatEntries(lobbyID) {
		cid := asInt(e["id"])
		meta := monsterMetaFor(sprintAny(e["monster_key"]))
		if cid >= 0 || meta == nil || asInt(e["hp"]) <= 0 || loadMonsterState(lobbyID, cid).AurasOff {
			continue
		}
		has := false
		for _, t := range meta.Traits {
			if t.Name == "Aura of Authority" {
				has = true
			}
		}
		if !has || monsterIncapacitated(lobbyID, cid) {
			continue
		}
		if cid == monsterID || !hasMap {
			return true
		}
		a, ok1 := mp.token(cid)
		b, ok2 := mp.token(monsterID)
		if ok1 && ok2 && tokenDistanceFeet(a, b) <= 10 {
			return true
		}
	}
	return false
}

func regenBlockTypes(text string) string {
	if m := regenBlockRe.FindStringSubmatch(text); m != nil {
		return m[1] + " or " + m[2] + " damage"
	}
	return "damage that stops it"
}

// noteRegenerationBlock: a monster whose Regeneration stops after Acid or Fire damage took such damage.
func noteRegenerationBlock(lobbyID, id int, dtype string) {
	if id >= 0 || dtype == "" {
		return
	}
	_, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil {
		return
	}
	for _, t := range meta.Traits {
		if m := regenBlockRe.FindStringSubmatch(t.Text); m != nil && (strings.EqualFold(m[1], dtype) || strings.EqualFold(m[2], dtype)) {
			s := loadMonsterState(lobbyID, id)
			if !s.RegenBlocked {
				s.RegenBlocked = true
				saveMonsterState(lobbyID, id, s)
			}
		}
	}
}

// ---- Recharge

func rechargeNeed(meta *monsterMeta, name string) int {
	for _, list := range [][]srdMonsterEntry{meta.Actions, meta.BonusActions, meta.Reactions} {
		for _, a := range list {
			if strings.EqualFold(stripUsage(a.Name), name) {
				if m := rechargeRe.FindStringSubmatch(a.Name); m != nil {
					n, _ := strconv.Atoi(m[1])
					return n
				}
			}
		}
	}
	return 0
}

// findMonsterEntry finds an action, bonus action or reaction of a stat block by name (with or without its usage note).
func findMonsterEntry(meta *monsterMeta, name string) (srdMonsterEntry, string, bool) {
	want := strings.ToLower(stripUsage(name))
	for _, group := range []struct {
		kind string
		list []srdMonsterEntry
	}{{"action", meta.Actions}, {"bonus action", meta.BonusActions}, {"reaction", meta.Reactions}, {"legendary action", meta.Legendary}} {
		for _, a := range group.list {
			if strings.ToLower(stripUsage(a.Name)) == want || strings.ToLower(a.Name) == strings.ToLower(name) {
				return a, group.kind, true
			}
		}
	}
	return srdMonsterEntry{}, "", false
}

// ---- GM guidance and commands

// monsterFeaturesInfo is the GM's view of a monster's features in the fight.
func monsterFeaturesInfo(lobbyID, id int) map[string]interface{} {
	_, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil {
		return nil
	}
	s := loadMonsterState(lobbyID, id)
	la, lr := legendaryUseTotals(meta, s)
	out := map[string]interface{}{"in_lair": s.InLair}
	if la > 0 {
		var acts []string
		for _, a := range meta.Legendary {
			acts = append(acts, a.Name)
		}
		out["legendary_actions"] = map[string]interface{}{"uses": la, "used": s.LAUsed, "uses_left": la - s.LAUsed, "actions": acts,
			"not_again_until_its_turn": nonNil(s.LAOnce)}
	}
	if lr > 0 {
		out["legendary_resistance"] = map[string]interface{}{"per_day": lr, "used": s.LRUsed, "left": lr - s.LRUsed, "policy": s.LRPolicy}
	}
	var auras []string
	for _, t := range meta.Traits {
		if startAuraRe.MatchString(t.Text) || endAuraRe.MatchString(t.Text) || t.Name == "Aura of Authority" || regenRe.MatchString(t.Text) {
			auras = append(auras, t.Name)
		}
	}
	if len(auras) > 0 {
		out["automatic_traits"] = auras
		out["auras_on"] = !s.AurasOff
	}
	if len(s.RechargeSpent) > 0 {
		out["recharging"] = s.RechargeSpent
	}
	if len(out) == 1 && !s.InLair {
		return nil // nothing the server runs on its own for this monster
	}
	if meta.XPLair > 0 {
		xp := meta.XP
		if s.InLair {
			xp = meta.XPLair
		}
		out["xp"] = xp
	}
	return out
}

// handleGMMonsterFeatures godoc
// @Summary A monster's Legendary Actions, Legendary Resistance, lair and auras in the fight
// @Description GET ?combatant_id=N shows them (uses left, policy, automatic traits, recharging actions). POST sets them: in_lair (one more Legendary Action use and Legendary Resistance; the 2024 rules have no Lair Actions), legendary_resistance ("auto": used against effects that give a condition or damage that would drop the monster to 0 HP; "always"; "manual"), auras (false switches the monster's auras, Regeneration aside, off).
// @Tags GM
// @Param combatant_id query int false "Monster id (GET)"
// @Param request body object{combatant_id=int,in_lair=bool,legendary_resistance=string,auras=bool} false "Settings (POST)"
// @Router /gm/monster-features [get]
// @Router /gm/monster-features [post]
func handleGMMonsterFeatures(w http.ResponseWriter, r *http.Request) {
	lobbyID, ok := gmActiveCampaign(w, r)
	if !ok {
		return
	}
	var req struct {
		CombatantID         int    `json:"combatant_id"`
		InLair              *bool  `json:"in_lair"`
		LegendaryResistance string `json:"legendary_resistance"`
		Auras               *bool  `json:"auras"`
	}
	if r.Method == http.MethodPost {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req)
	} else {
		req.CombatantID, _ = strconv.Atoi(r.URL.Query().Get("combatant_id"))
	}
	e, meta, found := monsterCombatant(lobbyID, req.CombatantID)
	if !found {
		var list []map[string]interface{}
		for _, c := range combatEntries(lobbyID) {
			if id := asInt(c["id"]); id < 0 {
				list = append(list, map[string]interface{}{"combatant_id": id, "name": c["name"], "features": monsterFeaturesInfo(lobbyID, id)})
			}
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"monsters": list, "hint": "Give combatant_id to see or change one monster."})
		return
	}
	if r.Method == http.MethodPost {
		s := loadMonsterState(lobbyID, req.CombatantID)
		if req.InLair != nil {
			s.InLair = *req.InLair
		}
		switch strings.ToLower(req.LegendaryResistance) {
		case "":
		case "auto", "always", "manual":
			s.LRPolicy = strings.ToLower(req.LegendaryResistance)
		default:
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_policy", "message": "legendary_resistance is auto, always or manual."})
			return
		}
		if req.Auras != nil {
			s.AurasOff = !*req.Auras
		}
		saveMonsterState(lobbyID, req.CombatantID, s)
	}
	_ = meta
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "combatant_id": req.CombatantID, "name": e["name"], "features": monsterFeaturesInfo(lobbyID, req.CombatantID)})
}

// gmCampaignFor picks the campaign a GM command is for: ?campaign_id=N (it must be one of the GM's active campaigns), else the
// GM's only active campaign, else the only one with a fight going on. A GM who runs several campaigns without a fight in exactly one
// of them must say which (before this, such commands landed in whichever campaign the database returned first).
func gmCampaignFor(agentID int, r *http.Request) (int, string, string) {
	want := 0
	if r != nil {
		want, _ = strconv.Atoi(r.URL.Query().Get("campaign_id"))
		// campaign_id in the JSON body counts too (Nov 14: a GM skill check with {"campaign_id": N} was answered "not_gm:
		// campaign_id_required"); the body is put back for the handler
		if want == 0 && r.Body != nil && r.Method != http.MethodGet {
			if b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)); err == nil {
				r.Body = io.NopCloser(bytes.NewReader(b))
				var probe struct {
					CampaignID int `json:"campaign_id"`
				}
				if json.Unmarshal(b, &probe) == nil && probe.CampaignID > 0 {
					want = probe.CampaignID
				}
			}
		}
	}
	rows, err := db.Query(`SELECT id FROM lobbies WHERE dm_id = $1 AND status = 'active' ORDER BY id`, agentID)
	if err != nil {
		return 0, "campaign_lookup_failed", "The campaign lookup failed."
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if want > 0 && !containsInt(ids, want) {
		var status string
		if db.QueryRow(`SELECT COALESCE(status,'') FROM lobbies WHERE id = $1 AND dm_id = $2`, want, agentID).Scan(&status) == nil {
			// F235: the GM's own campaign that isn't started read as "You are not the GM of that campaign"
			return 0, "campaign_not_active", fmt.Sprintf("Campaign %d is %s, not active: the GM's tools work in an active campaign. Start it with POST /api/campaigns/%d/start.", want, status, want)
		}
		return 0, "not_gm", "You are not the GM of that campaign."
	}
	if len(ids) == 0 {
		return 0, "not_gm", "You are not the GM of any active campaign."
	}
	if want > 0 {
		return want, "", ""
	}
	if len(ids) == 1 {
		return ids[0], "", ""
	}
	var fighting []int
	for _, id := range ids {
		var active bool
		if db.QueryRow(`SELECT COALESCE(active,false) FROM combat_state WHERE lobby_id = $1`, id).Scan(&active) == nil && active {
			fighting = append(fighting, id)
		}
	}
	if len(fighting) == 1 {
		return fighting[0], "", ""
	}
	return 0, "campaign_id_required", fmt.Sprintf("You run %d active campaigns (%v): add ?campaign_id=N to the request.", len(ids), ids)
}

// gmCampaignScan is gmCampaignFor for the handlers that look the campaign up themselves; it returns an error when there is none.
func gmCampaignScan(agentID int, r *http.Request, out *int) error {
	id, code, msg := gmCampaignFor(agentID, r)
	if code != "" {
		return fmt.Errorf("%s: %s", code, msg)
	}
	*out = id
	return nil
}

// gmActiveCampaign finds the campaign of the GM making the request (gmCampaignFor) and writes the error reply when there is none.
func gmActiveCampaign(w http.ResponseWriter, r *http.Request) (int, bool) {
	w.Header().Set("Content-Type", "application/json")
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return 0, false
	}
	id, code, msg := gmCampaignFor(agentID, r)
	if code != "" {
		status := http.StatusForbidden
		if code == "campaign_id_required" {
			status = http.StatusBadRequest
		}
		writeJSONStatus(w, status, map[string]interface{}{"error": code, "message": msg})
		return 0, false
	}
	return id, true
}

// handleGMLegendaryAction godoc
// @Summary Take a Legendary Action
// @Description A monster with Legendary Actions takes one immediately after another creature's turn (the turn that ended last): one at a time, one use each (the uses come back at the start of its turn), not while Incapacitated, and an action that says so not again until the start of its next turn. The server resolves the action's text: an attack (target), a saving throw effect or spell (target or target_ids; an Emanation reaches the characters in it on the battle map), damage, another action of the stat block; movement is for the GM to do on the map. The same request can go in combat/next as legendary_actions to take it before the next turn starts.
// @Tags GM
// @Param request body object{combatant_id=int,action=string,target=string,target_ids=[]int,use=string,apply_damage=bool} true "The Legendary Action"
// @Router /gm/legendary-action [post]
func handleGMLegendaryAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	lobbyID, ok := gmActiveCampaign(w, r)
	if !ok {
		return
	}
	var req legendaryRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	window, endingID := lastTurnEnded(lobbyID)
	res, code, msg := takeLegendaryAction(r, lobbyID, req, window, endingID)
	if code != "" {
		out := map[string]interface{}{"error": code, "message": msg}
		for k, v := range res {
			out[k] = v
		}
		writeJSONStatus(w, http.StatusBadRequest, out)
		return
	}
	res["success"] = true
	writeJSONStatus(w, http.StatusOK, res)
}

// handleGMLegendaryResistance godoc
// @Summary Use Legendary Resistance by hand
// @Description For a monster whose policy is manual (or to spend one the server didn't): counts one use of its Legendary Resistance (X/Day, one more in its lair) after it failed a saving throw. The server doesn't undo the effect: remove the condition or give back the damage with the GM tools. With the auto or always policy the server uses Legendary Resistance by itself when the monster fails a save it rolls.
// @Tags GM
// @Param request body object{combatant_id=int} true "Monster id"
// @Router /gm/legendary-resistance [post]
func handleGMLegendaryResistance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	lobbyID, ok := gmActiveCampaign(w, r)
	if !ok {
		return
	}
	var req struct {
		CombatantID int `json:"combatant_id"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req)
	e, meta, found := monsterCombatant(lobbyID, req.CombatantID)
	if !found {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "combatant_not_found"})
		return
	}
	s := loadMonsterState(lobbyID, req.CombatantID)
	_, total := legendaryUseTotals(meta, s)
	if total == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_legendary_resistance", "message": fmt.Sprintf("%s has no Legendary Resistance.", e["name"])})
		return
	}
	if s.LRUsed >= total {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "no_uses_left", "message": fmt.Sprintf("%s has used all %d uses today.", e["name"], total)})
		return
	}
	s.LRUsed++
	saveMonsterState(lobbyID, req.CombatantID, s)
	line := fmt.Sprintf("🛡️ %s fails the saving throw and chooses to succeed instead (Legendary Resistance).", e["name"])
	recordGMAction(db, lobbyID, "legendary_resistance", line, fmt.Sprintf("%d of %d left today.", total-s.LRUsed, total))
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": line, "left": total - s.LRUsed, "per_day": total,
		"note": "Undo the failed save's effect yourself (remove the condition, give back damage)."})
}

// ---- gm/narrate: monster actions without an attack roll, and Recharge

// resolveMonsterActionByNameAt is resolveMonsterActionByName with an area aimed at a grid square ([x, y], or nil): O-5.
func resolveMonsterActionByNameAt(r *http.Request, lobbyID int, point []int, monster, action, target string, targetIDs []int, apply bool, use string) (string, bool, string) {
	if len(point) == 2 {
		monsterAreaPoint.Store(lobbyID, mapSquare{X: point[0], Y: point[1]})
		defer monsterAreaPoint.Delete(lobbyID)
	}
	return resolveMonsterActionByName(r, lobbyID, monster, action, target, targetIDs, apply, use)
}

// resolveMonsterActionByName resolves a stat block action that is not a plain attack (a saving throw effect, a spell, a Recharge
// action) for gm/narrate. handled is false when the action is an attack (the attack code handles it) or unknown.
// use is the GM's choice where the entry needs one (Command's word; Claw or Tail).
func resolveMonsterActionByName(r *http.Request, lobbyID int, monster, action, target string, targetIDs []int, apply bool, use string) (string, bool, string) {
	ids := monsterIDsByKeyOrName(lobbyID, monster)
	id := 0
	entries := combatEntries(lobbyID)
	for _, x := range ids { // the best match first (F208)
		for _, c := range entries {
			if asInt(c["id"]) == x && asInt(c["hp"]) > 0 && id == 0 {
				id = x
			}
		}
	}
	if id == 0 {
		return "", false, ""
	}
	_, meta, _ := monsterCombatant(lobbyID, id)
	if meta == nil {
		return "", false, ""
	}
	entry, kind, ok := findMonsterEntry(meta, action)
	if !ok {
		return "", false, ""
	}
	s := loadMonsterState(lobbyID, id)
	base := stripUsage(entry.Name)
	recharge := rechargeRe.MatchString(entry.Name)
	if recharge && containsStr(s.RechargeSpent, base) {
		return "", true, fmt.Sprintf("%s's %s hasn't recharged yet (it rolls at the start of each of its turns).", creatureName(lobbyID, id), base)
	}
	if monsterIncapacitated(lobbyID, id) {
		return "", true, creatureName(lobbyID, id) + " is Incapacitated and can't act."
	}
	if kind == "reaction" { // Parry, Protective Magic, Split ... (monster_reactions_2024.go, F224)
		text, handled, why := resolveMonsterReaction(r, lobbyID, id, meta, entry, target, targetIDs, apply)
		if why != "" {
			return "", true, why
		}
		if recharge {
			s = loadMonsterState(lobbyID, id)
			s.RechargeSpent = append(s.RechargeSpent, base)
			saveMonsterState(lobbyID, id, s)
		}
		if handled {
			return text, true, ""
		}
		monsterReactionRound.Store(fmt.Sprintf("%d:%d", lobbyID, id), currentRound(lobbyID)) // a saving throw reaction: its text below
	}
	if isAttackEntry(entry.Text) || strings.EqualFold(base, "Multiattack") {
		if recharge {
			s.RechargeSpent = append(s.RechargeSpent, base)
			saveMonsterState(lobbyID, id, s)
		}
		return "", false, "" // the attack code resolves it
	}
	targets := targetIDsFrom(lobbyID, target, targetIDs)
	lines := resolveMonsterEffectText(r, lobbyID, id, meta, entry.Name, entry.Text, targets, use, apply, 0)
	if recharge {
		s.RechargeSpent = append(s.RechargeSpent, base)
		saveMonsterState(lobbyID, id, s)
	}
	return strings.Join(lines, " "), true, ""
}
