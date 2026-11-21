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
	"encoding/json"
	"fmt"
	"log"
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
	Senses                         string
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
	// "The troll dies only if it starts its turn with 0 Hit Points and doesn't regenerate": at 0 Hit Points it keeps its turn and
	// regenerates; it dies at a turn start at 0 that brings no regeneration (the Acid or Fire block, a form that can't regain Hit Points)
	regenFromZeroRe = regexp.MustCompile(`(?i)dies only if it starts its turn with 0 Hit Points and doesn['’]t regenerate`)
	startAuraRe     = regexp.MustCompile(`(?:any|each) (enemy|creature|Beast or Humanoid)[^.]*?starts its turn (?:in the aura|in an? (\d+)-foot Emanation|within (\d+) feet)`)
	endAuraRe       = regexp.MustCompile(`At the end of each of the [^,]+ turns, each creature( of the [^ ]+ choice)? in an? (\d+)-foot Emanation originating from the [^ ]+ takes (\d+) \(([^)]+)\) ([A-Za-z]+) damage`)
	// saveEndAuraRe: a saving throw aura at the end of the monster's turn ("Constitution Saving Throw: DC 12, each creature in a 5-foot
	// Emanation originating from the manes at the end of the manes's turn": Sickening Vapors)
	// The save opens the trait: one that begins with a condition of its own ("While underwater, the aboleth is surrounded by mucus.
	// Constitution Saving Throw ...") is the GM's, who judges the condition.
	saveEndAuraRe   = regexp.MustCompile(`^[A-Z][a-z]+ Saving Throw: DC \d+, each (?:creature|enemy)( of the [^ ]+ choice)? in an? (\d+)-foot Emanation originating from the [^ ]+ at the end of the [^ ]+ turns?\b`)
	legendaryOnceRe = regexp.MustCompile(`can’t take this action again until the start of its next turn|can't take this action again until the start of its next turn`)
)

func monsterMetaFor(key string) *monsterMeta {
	if strings.HasPrefix(key, "custom:") {
		var raw []byte
		if db.QueryRow(`SELECT definition FROM custom_monsters WHERE key = $1`, key).Scan(&raw) != nil {
			return nil
		}
		var m srdMonster2024
		if json.Unmarshal(raw, &m) != nil {
			return nil
		}
		return metaFromMonster(key, m)
	}
	monsterMetaOnce.Do(func() {
		monsterMetaMap = map[string]*monsterMeta{}
		for _, m := range srdMonsters2024() {
			mm := metaFromMonster(monsterSlug(m.Name), m)
			monsterMetaMap[mm.Key] = mm
		}
	})
	return monsterMetaMap[strings.ToLower(strings.TrimSpace(key))]
}

// ---- per-fight state

type monsterState struct {
	InLair        bool           `json:"in_lair"`
	LRPolicy      string         `json:"legendary_resistance_policy"`
	LRUsed        int            `json:"legendary_resistances_used"`
	LAUsed        int            `json:"legendary_actions_used"`
	LAWindow      string         `json:"-"`
	LAOnce        []string       `json:"-"`
	AurasOff      bool           `json:"auras_off"`
	RechargeSpent []string       `json:"recharge_spent"`
	AuraImmune    []int          `json:"aura_immune"`
	RegenBlocked  bool           `json:"-"`
	DayUses       map[string]int `json:"day_uses"` // the creature's per-day uses, kept across fights (monster_day_uses.go)
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
	s.DayUses = loadDayUses(lobbyID, id)
	json.Unmarshal(spent, &s.RechargeSpent)
	json.Unmarshal(immune, &s.AuraImmune)
	if s.LRPolicy == "" {
		s.LRPolicy = "auto"
	}
	return s
}

func saveMonsterState(lobbyID, id int, s monsterState) error {
	if db == nil {
		return nil
	}
	once, _ := json.Marshal(nonNil(s.LAOnce))
	spent, _ := json.Marshal(nonNil(s.RechargeSpent))
	immune, _ := json.Marshal(s.AuraImmune)
	// Resistance counts belong to monster_resistance_2024.go. A general
	// snapshot cannot restore a use spent after that snapshot was read.
	if s.AuraImmune == nil {
		immune = []byte("[]")
	}
	_, err := db.Exec(`INSERT INTO combat_monster_state (lobby_id, monster_id, in_lair, lr_policy, lr_used, la_used, la_window, la_once, auras_off, recharge_spent, aura_immune, regen_blocked)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (lobby_id, monster_id) DO UPDATE SET in_lair=EXCLUDED.in_lair, lr_policy=EXCLUDED.lr_policy,
			la_used=EXCLUDED.la_used, la_window=EXCLUDED.la_window, la_once=EXCLUDED.la_once, auras_off=EXCLUDED.auras_off,
			recharge_spent=EXCLUDED.recharge_spent, aura_immune=EXCLUDED.aura_immune, regen_blocked=EXCLUDED.regen_blocked`,
		lobbyID, id, s.InLair, s.LRPolicy, 0, s.LAUsed, s.LAWindow, string(once), s.AurasOff, string(spent), string(immune), s.RegenBlocked)
	return err
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
		forgetFightCreatures(lobbyID) // the day counts of linked creatures stay (monster_day_uses.go)
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
	left, spent, err := spendMonsterResistance(lobbyID, id, meta)
	if err != nil {
		log.Printf("Legendary Resistance could not be spent: %v", err)
		return false
	}
	if !spent {
		return false
	}
	line := fmt.Sprintf("🛡️ %s fails the saving throw and chooses to succeed instead (Legendary Resistance).", sprintAny(e["name"]))
	addSaveNote(lobbyID, fmt.Sprintf("[%s fails the save but uses Legendary Resistance to succeed: %d of %d left today.]", sprintAny(e["name"]), left, total))
	recordGMAction(db, lobbyID, "legendary_resistance", line, fmt.Sprintf("%d of %d Legendary Resistance uses left today.", left, total))
	return true
}

// ---- Legendary Actions

type legendaryRequest struct {
	CombatantID      int    `json:"combatant_id"`
	Action           string `json:"action"`      // the Legendary Action to take, by name as the stat block lists it
	ActionName       string `json:"action_name"` // older name of the field
	Target           string `json:"target"`      // the attack's target (a character's name or id)
	TargetIDs        []int  `json:"target_ids"`
	Use              string `json:"use"`               // the chosen attack, spell, or attack damage type
	ApplyDamage      *bool  `json:"apply_damage"`      // defaults to true; false previews without spending this use or closing the window
	ConfirmTargeting bool   `json:"confirm_targeting"` // the GM confirmed required sight or distance the server cannot determine
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
	// An action that calls an attack must not spend its use on an invalid damage choice.
	if match := mfxAttackRe.FindStringSubmatch(act.Text); match != nil {
		attack := match[1]
		if match[2] != "" && strings.EqualFold(strings.TrimSpace(req.Use), match[2]) {
			attack = match[2]
		}
		if problem := preflightMonsterAction(meta, attack, req.Use, nil); problem != "" {
			return nil, "invalid_attack_damage", problem
		}
	}
	if match := mfxUsesRe.FindStringSubmatch(act.Text); match != nil {
		if problem := preflightMonsterAction(meta, strings.TrimSpace(match[1]), req.Use, nil); problem != "" {
			return nil, "invalid_attack_damage", problem
		}
	}
	apply := damageApplies(req.ApplyDamage)
	targets := targetIDsFrom(lobbyID, req.Target, req.TargetIDs)
	// a Legendary Action with its own "(N/Day)" limit: checked and reserved here, counted only when the server executed it
	if r == nil {
		r = &http.Request{} // a saved turn can resume without the original HTTP request
	}
	r, rec := withExecution(r)
	rec.confirmTargeting = req.ConfirmTargeting
	if key, limit := monsterDayUse(act, ""); key != "" {
		if apply {
			if !reserveDayUse(lobbyID, req.CombatantID, key, limit) {
				return nil, "day_limit", dayUseRefusal(loadMonsterState(lobbyID, req.CombatantID), name, act, "", key, limit)
			}
			defer func() {
				if !rec.wasExecuted() {
					releaseDayUse(lobbyID, req.CombatantID, key)
				}
			}()
		} else if why := dayUseRefusal(s, name, act, "", key, limit); why != "" {
			return nil, "day_limit", why
		}
	}
	lines := resolveMonsterEffectText(r, lobbyID, req.CombatantID, meta, act.Name, act.Text, targets, req.Use, apply, 0)
	if rec.targetBlocked {
		text := strings.Join(lines, " ")
		_, private := splitGMNotes(text)
		for _, note := range private {
			addGMNotice(lobbyID, note)
		}
		return nil, "target_ineligible", text
	}
	if !apply {
		text := fmt.Sprintf("Legendary Action preview: %s would use %s. %s", name, act.Name, strings.Join(lines, " "))
		return map[string]interface{}{"combatant": name, "action": act.Name, "result": text, "uses_left": total - s.LAUsed, "uses": total, "preview": true}, "", ""
	}

	s.LAUsed++
	s.LAWindow = window
	if legendaryOnceRe.MatchString(act.Text) {
		s.LAOnce = append(s.LAOnce, act.Name)
	}
	saveMonsterState(lobbyID, req.CombatantID, s)
	text := fmt.Sprintf("⚡ Legendary Action: %s uses %s. %s", name, act.Name, strings.Join(lines, " "))
	public, private := splitGMNotes(text)
	recordGMAction(db, lobbyID, "legendary_action", public, fmt.Sprintf("%d of %d Legendary Action uses left.", total-s.LAUsed, total))
	for _, note := range private {
		addGMNotice(lobbyID, note)
	}
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
	log := expireSourceTurnConditions(lobbyID, id, name, "start")
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
			fromZero := regenFromZeroRe.MatchString(t.Text)
			dies := func(why string) { // it started its turn at 0 and didn't regenerate: it is dead (noTurnFor skips it from now on)
				editCombatant(lobbyID, id, func(e map[string]interface{}) { e["regen_dead"] = true })
				log = append(log, fmt.Sprintf("%s doesn't regenerate (%s) and dies: it started its turn with 0 Hit Points.", name, why))
			}
			switch {
			case fromZero && hp <= 0 && e["regen_dead"] == true:
			case s.RegenBlocked:
				if fromZero && hp <= 0 {
					dies("it took " + regenBlockTypes(t.Text))
				} else {
					log = append(log, fmt.Sprintf("%s doesn't regenerate this turn (it took %s).", name, regenBlockTypes(t.Text)))
				}
				s.RegenBlocked, changed = false, true
			case hp < maxHP && (hp >= 1 || fromZero):
				n, _ := strconv.Atoi(m[1])
				before, after, err := healMonster(lobbyID, id, n) // the monster Hit Point owner (monster_hp_2024.go)
				switch {
				case err != nil:
					log = append(log, fmt.Sprintf("%s's regeneration could not be applied (the server could not read its form's traits).", name))
				case after.HP > before.HP:
					log = append(log, fmt.Sprintf("%s regenerates %d Hit Points (%s).", name, after.HP-before.HP, t.Name))
				case fromZero && hp <= 0:
					dies("it can't regain Hit Points")
				default: // the form forbids regaining Hit Points (a Construct's or Undead's trait)
					log = append(log, fmt.Sprintf("%s doesn't regenerate: it can't regain Hit Points.", name))
				}
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
			if containsInt(s.AuraImmune, id) || effectImmune(lobbyID, id, mid, t.Name) || !inMonsterAura(lobbyID, mid, id, rng) {
				continue
			}
			eff.SourceID, eff.ImmunityName = mid, t.Name
			lines, _ := applySaveEffect(lobbyID, sprintAny(e["name"])+" ("+t.Name+")", eff, []int{id}, true)
			log = append(log, fmt.Sprintf("%s starts its turn in %s's %s: %s", name, sprintAny(e["name"]), t.Name, strings.Join(lines, " ")))
		}
	}
	return log
}

// monsterFeaturesTurnEnd expires source-timed conditions before the monster's auras act.
func monsterFeaturesTurnEnd(lobbyID, id int, name string) []string {
	ended := expireSourceTurnConditions(lobbyID, id, name, "end")
	if id >= 0 {
		return ended
	}
	return append(ended, monsterAuraTurnEnd(lobbyID, id, name)...)
}

// monsterAuraTurnEnd: the end of a monster's turn: auras that deal damage around it.
func monsterAuraTurnEnd(lobbyID, id int, name string) []string {
	return runMonsterEndAuras(lobbyID, monsterEndAuraCursor{Source: id, Name: name})
}

type monsterEndAuraCursor struct {
	Source, Next int
	Name         string
}

func init() {
	combatResumeHandlers["monster-end-auras"] = func(lobby int, raw json.RawMessage) (map[string]interface{}, error) {
		var cursor monsterEndAuraCursor
		if err := json.Unmarshal(raw, &cursor); err != nil {
			return nil, err
		}
		lines := runMonsterEndAuras(lobby, cursor)
		for _, line := range lines {
			public, private := splitGMNotes(line)
			for _, note := range private {
				addGMNotice(lobby, note)
			}
			if public != "" {
				if err := recordGMAction(db, lobby, "monster_effect", public, public); err != nil {
					return nil, err
				}
			}
		}
		return map[string]interface{}{"result": lines}, nil
	}
}

func runMonsterEndAuras(lobbyID int, cursor monsterEndAuraCursor) []string {
	id, name := cursor.Source, cursor.Name
	e, meta, ok := monsterCombatant(lobbyID, id)
	if !ok || meta == nil || asInt(e["hp"]) <= 0 || loadMonsterState(lobbyID, id).AurasOff || cursor.Next < 0 || cursor.Next > len(meta.Traits) {
		return nil
	}
	var log []string
	for i := cursor.Next; i < len(meta.Traits); i++ {
		t := meta.Traits[i]
		if sm := saveEndAuraRe.FindStringSubmatch(t.Text); sm != nil {
			log = append(log, endSaveAura(lobbyID, id, name, t, sm)...)
			if combatResolutionPaused(lobbyID) {
				return log
			}
			continue
		}
		m := endAuraRe.FindStringSubmatch(t.Text)
		if m == nil {
			continue
		}
		if strings.Contains(t.Text, "Incapacitated") && monsterIncapacitated(lobbyID, id) {
			continue
		}
		rng, _ := strconv.Atoi(m[2])
		dice := normDice(m[4])
		dtype := strings.ToLower(m[5])
		tail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t.Text), m[0]))
		incapacitatedException := strings.HasPrefix(tail, "unless ") && strings.HasSuffix(tail, " has the Incapacitated condition.")
		if (tail == "" || tail == "." || incapacitatedException) && validStatBlockDamage(dice, dtype, nil) {
			targets, placed, unplaced := monsterEndAuraTargets(lobbyID, id, rng)
			if m[1] != "" { // "each creature of the salamander's choice": its enemies
				targets = chosenAuraTargets(targets)
			}
			if !placed {
				log = append(log, fmt.Sprintf("[GM: %s's %s needs the source and affected creatures placed on the map. No aura damage was rolled.]", name, t.Name))
				continue
			}
			if unplaced {
				log = append(log, fmt.Sprintf("[GM: check any unplaced creatures for %s's %s and resolve their exposure if needed.]", name, t.Name))
			}
			if len(targets) == 0 {
				continue
			}
			next := cursor
			next.Next = i + 1
			restore, err := armCombatFrame(lobbyID, "monster-end-auras", next)
			if err != nil {
				return append(log, "[GM: the aura could not save its continuation. No additional aura effects were resolved.]")
			}
			damage := monsterDirectDamage{Targets: targets, Amount: game.RollDamage(dice, false), Type: dtype}
			lines, _, err := runMonsterDirectDamage(lobbyID, damage)
			restore()
			if len(lines) > 0 {
				log = append(log, fmt.Sprintf("%s's %s: %s", name, t.Name, strings.Join(lines, " ")))
			}
			if err != nil {
				noteDBError(err, "resolving a monster aura")
				return append(log, "[GM: the aura damage sequence could not finish. Review its state.]")
			}
			if combatResolutionPaused(lobbyID) {
				return log
			}
			continue
		}
		// The legacy Burning rider retains its existing character delivery.
		// Its compound action and object handling need separate coverage.
		if !strings.Contains(tail, "start burning") {
			log = append(log, fmt.Sprintf("[GM: resolve the targeting and additional rules of %s's %s as described in the compendium.]", name, t.Name))
			continue
		}
		dmg := game.RollDamage(dice, false)
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

// endSaveAura: a saving throw aura at the end of the monster's turn (saveEndAuraRe): every creature in the Emanation on the battle map
// (of its choice: its enemies, chosenAuraTargets) rolls the save through the save effect owner; a creature immune to it (a success's
// "immune to this manes's Sickening Vapors for 24 hours") is left out.
func endSaveAura(lobbyID, id int, name string, t srdMonsterEntry, sm []string) []string {
	if strings.Contains(t.Text, "Incapacitated condition") && strings.Contains(t.Text, "unless") && monsterIncapacitated(lobbyID, id) {
		return nil
	}
	eff, ok := parseSaveEffect(t.Text)
	if !ok {
		return nil
	}
	rng, _ := strconv.Atoi(sm[2])
	targets, placed, unplaced := monsterEndAuraTargets(lobbyID, id, rng)
	if !placed {
		return []string{fmt.Sprintf("[GM: %s's %s needs the source and affected creatures placed on the map. No saving throw was rolled.]", name, t.Name)}
	}
	if sm[1] != "" {
		targets = chosenAuraTargets(targets)
	}
	var in []int
	for _, tid := range targets {
		if !effectImmune(lobbyID, tid, id, t.Name) {
			in = append(in, tid)
		}
	}
	var log []string
	if unplaced {
		log = append(log, fmt.Sprintf("[GM: check any unplaced creatures for %s's %s and resolve their exposure if needed.]", name, t.Name))
	}
	if len(in) == 0 {
		return log
	}
	eff.SourceID, eff.ImmunityName = id, t.Name
	lines, _ := applySaveEffect(lobbyID, name+" ("+t.Name+")", eff, in, true)
	return append(log, fmt.Sprintf("%s's %s: %s", name, t.Name, strings.Join(lines, " ")))
}

// chosenAuraTargets: the creatures an aura "of its choice" affects: its enemies (the characters and their companions), never the other
// monsters of the fight (its side).
func chosenAuraTargets(targets []int) []int {
	var out []int
	for _, tid := range targets {
		if tid > 0 {
			out = append(out, tid)
		}
	}
	return out
}

func monsterEndAuraTargets(lobby, source, radius int) (targets []int, placed, unplaced bool) {
	battle, ok := activeEncounterMap(lobby)
	if !ok {
		return nil, false, false
	}
	origin, ok := battle.token(source)
	if !ok {
		return nil, false, false
	}
	for _, token := range battle.Tokens {
		if token.ID == source || tokenDistanceFeet(origin, token) > radius || !battle.canSee(origin, token) {
			continue
		}
		if token.ID < 0 && !monsterAlive(lobby, token.ID) {
			continue
		}
		if isCompanionCombatID(token.ID) {
			if _, active := activeCompanionInCampaign(lobby, token.ID); !active {
				continue
			}
		}
		targets = append(targets, token.ID)
	}
	for _, entry := range combatEntries(lobby) {
		id := asInt(entry["id"])
		if id != 0 && id != source {
			if _, known := battle.token(id); !known {
				unplaced = true
				break
			}
		}
	}
	return targets, true, unplaced
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
		if !metaHasTrait(meta, "Aura of Authority") || monsterIncapacitated(lobbyID, cid) {
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

// regeneratesFromZero: the stat block's Regeneration says it dies only if it starts its turn at 0 Hit Points and doesn't regenerate.
func regeneratesFromZero(meta *monsterMeta) bool {
	if meta == nil {
		return false
	}
	for _, t := range meta.Traits {
		if regenRe.MatchString(t.Text) && regenFromZeroRe.MatchString(t.Text) {
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
	if auras := auraTraits(meta); len(auras) > 0 {
		out["automatic_traits"] = auras
		out["auras_on"] = !s.AurasOff
	}
	if len(s.RechargeSpent) > 0 {
		out["recharging"] = s.RechargeSpent
	}
	out["creature_id"] = dayCreatureID(lobbyID, id)
	if len(s.DayUses) > 0 {
		out["day_uses"] = s.DayUses // kept per creature across fights until the GM ends the day
	}
	if len(out) <= 2 && !s.InLair && len(s.DayUses) == 0 {
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
// @Description GET ?combatant_id=N shows them (uses left, policy, automatic traits, recharging actions). POST sets them: in_lair (one more Legendary Action use and Legendary Resistance; the 2024 rules have no Lair Actions), legendary_resistance ("auto": used against effects that give a condition or damage that would drop the monster to 0 HP; "always"; "manual"), auras (false switches the monster's auras, Regeneration aside, off), new_day (true: every creature's per-day uses in the campaign come back; the server does not track days by itself), day_uses (sets this creature's counts by key, 0 gives one back). Per-day uses (\"(1/Day)\", Spellcasting \"2/Day Each\") are kept per creature (creature_id) across fights; GET without combatant_id lists the known creatures. Send spend_recharge with the name of a Recharge action the GM has already resolved to record that use.
// @Tags GM
// @Param combatant_id query int false "Monster id (GET)"
// @Body Settings (POST)
// @Router /gm/monster-features [get]
// @Router /gm/monster-features [post]
func handleGMMonsterFeatures(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CombatantID         int            `json:"combatant_id"`
		InLair              *bool          `json:"in_lair"`              // true: the monster fights in its lair (one more Legendary Action use and Legendary Resistance)
		LegendaryResistance string         `json:"legendary_resistance"` // auto (used against a condition, or damage that would drop it to 0), always or manual
		Auras               *bool          `json:"auras"`                // false switches the monster's auras off (Regeneration aside)
		NewDay              bool           `json:"new_day"`              // a new day begins: every creature's per-day uses in the campaign come back
		DayUses             map[string]int `json:"day_uses"`             // sets this monster's per-day counts by key (action:<name>, spell:<name>, tier:<head>); 0 gives a use back
		SpendRecharge       string         `json:"spend_recharge"`       // records the use of a named Recharge action the GM has already resolved
	}
	_, lobbyID, ok := gmRead(w, r, &req, optionalBody())
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		req.CombatantID, _ = strconv.Atoi(r.URL.Query().Get("combatant_id"))
	}
	e, meta, found := monsterCombatant(lobbyID, req.CombatantID)
	entryName := ""
	if r.Method == http.MethodPost && req.SpendRecharge != "" {
		if !found || meta == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "combatant_not_found", "message": "Give the combatant_id of a monster in this fight."})
			return
		}
		entry, _, known := findMonsterEntry(meta, strings.TrimSpace(req.SpendRecharge))
		entryName = stripUsage(entry.Name)
		if !known || rechargeNeed(meta, entryName) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_recharge_action", "message": "Name a Recharge action, Bonus Action or Reaction on this creature's stat block."})
			return
		}
	}
	if !found {
		if r.Method == http.MethodPost && req.NewDay {
			newDay(lobbyID)
		}
		var list []map[string]interface{}
		for _, c := range combatEntries(lobbyID) {
			if id := asInt(c["id"]); id < 0 {
				list = append(list, map[string]interface{}{"combatant_id": id, "name": c["name"], "features": monsterFeaturesInfo(lobbyID, id)})
			}
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"monsters": list, "creatures": creatureLedger(lobbyID), "hint": "Give combatant_id to see or change one monster. creatures are the campaign's known creatures: give a creature_id in combat/add to link a returning one."})
		return
	}
	if r.Method == http.MethodPost {
		s := loadMonsterState(lobbyID, req.CombatantID)
		if entryName != "" {
			if containsStr(s.RechargeSpent, entryName) {
				writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "recharge_spent", "message": "That use is already spent. It can be used again when it recharges."})
				return
			}
			s.RechargeSpent = append(s.RechargeSpent, entryName)
		}
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
		if len(req.DayUses) > 0 {
			if s.DayUses == nil {
				s.DayUses = map[string]int{}
			}
			for k, v := range req.DayUses {
				if v <= 0 {
					delete(s.DayUses, k)
				} else {
					s.DayUses[k] = v
				}
			}
		}
		if err := saveMonsterState(lobbyID, req.CombatantID, s); err != nil {
			writeDBWriteError(w, err, "the monster's features")
			return
		}
		if req.NewDay {
			newDay(lobbyID)
		}
		if len(req.DayUses) > 0 {
			saveDayUses(lobbyID, req.CombatantID, s.DayUses)
		}
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "combatant_id": req.CombatantID, "name": e["name"], "features": monsterFeaturesInfo(lobbyID, req.CombatantID)})
}

// handleGMLegendaryAction godoc
// @Summary Take a Legendary Action
// @Description A monster with Legendary Actions takes one immediately after another creature's turn (the turn that ended last): one at a time, one use each (the uses come back at the start of its turn), not while Incapacitated, and an action that says so not again until the start of its next turn. The server resolves the action's text: an attack (target), a saving throw effect or spell (target or target_ids; an Emanation reaches the characters in it on the battle map), damage, another action of the stat block; movement is for the GM to do on the map. Set apply_damage=false to preview while keeping the use and timing window available. Set confirm_targeting=true after verifying required sight or distance the server cannot determine. The same request can go in combat/next as legendary_actions to take it before the next turn starts.
// @Tags GM
// @Body The Legendary Action
// @Router /gm/legendary-action [post]
func handleGMLegendaryAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req legendaryRequest
	_, lobbyID, ok := gmRead(w, r, &req)
	if !ok {
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
// @Body Monster id
// @Router /gm/legendary-resistance [post]
func handleGMLegendaryResistance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		CombatantID   int    `json:"combatant_id"`   // the monster's negative combat id
		DecisionToken string `json:"decision_token"` // required when answering a new pending Concentration decision: token from its private notice
	}
	_, lobbyID, ok := gmRead(w, r, &req, optionalBody())
	if !ok {
		return
	}
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
	if req.DecisionToken != "" || monsterConcentrationPending(lobbyID, req.CombatantID) != "" { // the use answers the pending Concentration decision (monster_concentration_2024.go)
		msg, code := resolveMonsterConcentration(lobbyID, req.CombatantID, "legendary_resistance", req.DecisionToken)
		if code == dbErrorCode {
			writeDBWriteError(w, nil, "the Concentration decision")
			return
		}
		if code != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": msg, "left": total - s.LRUsed - 1, "per_day": total})
		return
	}
	left, spent, err := spendMonsterResistance(lobbyID, req.CombatantID, meta)
	if err != nil {
		writeDBWriteError(w, err, "Legendary Resistance")
		return
	}
	if !spent {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "no_uses_left", "message": "No Legendary Resistance use is left."})
		return
	}
	line := fmt.Sprintf("🛡️ %s fails the saving throw and chooses to succeed instead (Legendary Resistance).", e["name"])
	recordGMAction(db, lobbyID, "legendary_resistance", line, fmt.Sprintf("%d of %d left today.", left, total))
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "message": line, "left": left, "per_day": total,
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

// monsterEntryRefusal: why the monster can't use this entry now (a Recharge entry not recharged, the monster Incapacitated), or "".
func monsterEntryRefusal(lobbyID, id int, entry srdMonsterEntry) string {
	base := stripUsage(entry.Name)
	if rechargeRe.MatchString(entry.Name) && containsStr(loadMonsterState(lobbyID, id).RechargeSpent, base) {
		return fmt.Sprintf("%s's %s hasn't recharged yet (it rolls at the start of each of its turns).", creatureName(lobbyID, id), base)
	}
	if monsterIncapacitated(lobbyID, id) {
		return creatureName(lobbyID, id) + " is Incapacitated and can't act."
	}
	if formGateRe.MatchString(entry.Name) { // "(Wolf or Hybrid Form Only)" in another form (monster_shape_shift_2024.go)
		_, meta, _ := monsterCombatant(lobbyID, id)
		return formRefusal(lobbyID, id, meta, entry.Name)
	}
	return movementEntryRefusal(lobbyID, id, entry) // when it may be used (Rampage: creature_movement_2024.go)
}

// resolveMonsterActionByName resolves a stat block action that is not a plain attack (a saving throw effect, a spell, a Recharge
// action) for gm/narrate. handled is false when the action is an attack (the attack code handles it) or unknown.
// use is the GM's choice where the entry needs one (Command's word; Claw or Tail).
func resolveMonsterActionByName(r *http.Request, lobbyID int, monster, action, target string, targetIDs []int, apply bool, use string) (string, bool, string) {
	ids := monsterIDsByKeyOrName(lobbyID, monster)
	if n, err := strconv.Atoi(strings.TrimPrefix(monster, "#")); err == nil && strings.HasPrefix(monster, "#") {
		ids = []int{n} // one particular monster of the fight, resolveMonsterAttack's convention: the same action, addressed by combat id, must resolve the same way
	}
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
	if why := monsterEntryRefusal(lobbyID, id, entry); why != "" {
		return "", true, why
	}
	if why := replaceRefusal(entry, use); why != "" { // a "use" the entry's replacement sentence doesn't offer (monster_replace_2024.go)
		return "", true, why
	}
	if why := shapeShiftUseRefusal(entry.Name, entry.Text, use); why != "" { // a Shape-Shift form it doesn't offer (monster_shape_shift_2024.go)
		return "", true, why
	}
	s := loadMonsterState(lobbyID, id)
	base := stripUsage(entry.Name)
	recharge := rechargeRe.MatchString(entry.Name)
	if recharge && containsStr(s.RechargeSpent, base) {
		name := creatureName(lobbyID, id)
		return "", true, refusalAudiences(fmt.Sprintf("%s's %s is not available. Nothing was rolled.", name, base), fmt.Sprintf("%s's %s hasn't recharged yet (it rolls at the start of each of its turns)", name, base))
	}
	if monsterIncapacitated(lobbyID, id) {
		return "", true, creatureName(lobbyID, id) + " is Incapacitated and can't act."
	}
	if rule, ok := replaceRuleOf(entry.Text); ok && strings.EqualFold(base, "Multiattack") {
		if o, ok := rule.option(use); ok { // the attacks less the replaced ones, then the option
			text, why := resolveMultiattackReplacement(r, lobbyID, id, meta, monster, rule, o, target, targetIDs, apply)
			if why != "" {
				return "", true, why
			}
			return text, true, ""
		}
	}
	// "Failure or Success: The dragon can't take this action again until the start of its next turn" (a breath that lingers): the
	// use is kept in the same list as a Legendary Action's (LAOnce, cleared at the start of the monster's turn)
	once := kind != "legendary action" && legendaryOnceRe.MatchString(entry.Text)
	if once && containsStr(s.LAOnce, entry.Name) {
		return "", true, fmt.Sprintf("%s can't take %s again until the start of its next turn. Nothing was rolled.", creatureName(lobbyID, id), base)
	}
	dayKey, dayLimit := "", 0
	attackEntry := isAttackEntry(entry.Text) || strings.EqualFold(base, "Multiattack") // the attack code checks and counts an attack's day use itself (resolveMonsterAttack)
	reserved, settled := false, false
	if dayKey, dayLimit = monsterDayUse(entry, use); dayKey != "" && !attackEntry {
		// the limit is checked and the use reserved in one step in the database (two requests cannot both take the last use); a use that
		// is not executed (hand-off, refusal, rejected target) gives the reservation back below. A preview only reads the count.
		if apply {
			if !reserveDayUse(lobbyID, id, dayKey, dayLimit) {
				return "", true, dayUseRefusal(loadMonsterState(lobbyID, id), creatureName(lobbyID, id), entry, use, dayKey, dayLimit)
			}
			reserved = true
		} else if why := dayUseRefusal(s, creatureName(lobbyID, id), entry, use, dayKey, dayLimit); why != "" {
			return "", true, why
		}
		defer func() {
			if reserved && !settled {
				releaseDayUse(lobbyID, id, dayKey)
			}
		}()
	}
	// spendDay settles a use the server executed (the reservation stays). apply_damage false is a preview: nothing is counted. An actual
	// miss, a save that succeeds, immunity or no damage still counts: they are executed. A hand-off to the GM (the reply only tells the GM
	// what to do) is not a confirmed use: it says so and gives the reservation back.
	r, rec := ensureExecution(r)
	rec.dayHandled = true
	spendDay := func(executed bool) string {
		if dayKey == "" || attackEntry {
			return ""
		}
		if !apply {
			return "Not counted as a day use: apply_damage is false (a preview)."
		}
		if !executed {
			return fmt.Sprintf("Not counted as a day use: the server executed nothing (it is the GM's to resolve). When it happens, count it with POST /api/gm/monster-features day_uses {\"%s\": n}.", dayKey)
		}
		settled = true
		return dayUseCounted(creatureName(lobbyID, id), dayKey, loadDayUses(lobbyID, id)[dayKey], dayLimit)
	}
	if kind == "reaction" { // Parry, Protective Magic, Split ... (monster_reactions_2024.go, F224)
		text, handled, why := resolveMonsterReaction(r, lobbyID, id, meta, entry, target, targetIDs, apply)
		if why != "" {
			return "", true, why
		}
		if handled && apply && recharge {
			s = loadMonsterState(lobbyID, id)
			s.RechargeSpent = append(s.RechargeSpent, base)
			saveMonsterState(lobbyID, id, s)
		}
		if handled {
			// a reaction that found no trigger ("no character hit it this turn") is answered but not used: it is counted only when it took effect
			used := false
			if v, ok := monsterReactionRound.Load(fmt.Sprintf("%d:%d", lobbyID, id)); ok && v.(int) == currentRound(lobbyID) {
				used = true
			}
			if note := spendDay(used); note != "" {
				text = strings.TrimSpace(text + " " + note)
			}
			return text, true, ""
		}
	}
	if isAttackEntry(entry.Text) || strings.EqualFold(base, "Multiattack") {
		if problem := preflightMonsterAction(meta, base, use, multiattackDeclaredIn(r)); problem != "" {
			return "", true, problem
		}

		if apply && recharge {
			s.RechargeSpent = append(s.RechargeSpent, base)
			saveMonsterState(lobbyID, id, s)
		}
		return "", false, "" // the attack code resolves it
	}
	targets := targetIDsFrom(lobbyID, target, targetIDs)
	lines := resolveMonsterEffectText(r, lobbyID, id, meta, entry.Name, entry.Text, targets, use, apply, 0)
	if rec.targetBlocked {
		return strings.Join(lines, " "), true, ""
	}
	if kind == "reaction" && apply {
		monsterReactionRound.Store(fmt.Sprintf("%d:%d", lobbyID, id), currentRound(lobbyID))
	}
	if joined := strings.Join(lines, " "); !strings.Contains(joined, "Nothing was rolled") { // a refusal does not use it up
		if note := spendDay(rec.wasExecuted()); note != "" {
			lines = append(lines, note)
		}
	}
	s = loadMonsterState(lobbyID, id)
	if apply && (recharge || once) {
		if apply && recharge {
			s.RechargeSpent = append(s.RechargeSpent, base)
		}
		if once {
			s.LAOnce = append(s.LAOnce, entry.Name)
		}
		saveMonsterState(lobbyID, id, s)
	}
	return strings.Join(lines, " "), true, ""
}

func metaFromMonster(key string, m srdMonster2024) *monsterMeta {
	mm := &monsterMeta{Key: key, Name: m.Name, Senses: m.Senses, Traits: m.Traits, Actions: m.Actions, BonusActions: m.BonusActions,
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
	return mm
}
