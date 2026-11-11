package main

// Reminders for players (F216, Nov 9): AI players often forget what they can still do. The server says what is left this turn after each
// action and in my-turn (turn_left: Action, attacks left, Bonus Action options, movement, Reaction options), lists class resources and
// when they come back at the start of a fight and what went unused after it, mentions healing potions when hurt and allies at 0 HP,
// sums up after a fight (Hit Point Dice, what a Short Rest restores, dropped weapons, ammunition to recover), and after a Long Rest says
// that prepared spells can change and which long-lasting spells can be cast. Reminders give facts and commands, never advice.
//
// Each player chooses per kind: "brief" (default), "full" or "off" (GET/POST /api/characters/reminders); "all" sets every kind.
// Kinds: turn, resources, items, rest, prep, after_battle, gear (gear_reminders_2024.go).

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var reminderKinds = []struct{ Key, What string }{
	{"turn", "what is left this turn (Action, attacks, Bonus Action options, movement, Reaction options), after each action and in my-turn; the basic actions on round 1 (every turn in full)"},
	{"resources", "your class resources and spell slots and when they come back, on round 1 (every turn in full); what went unused, after the fight"},
	{"items", "a healing potion when you are at half your Hit Points or less; allies at 0 Hit Points and the ways to help them; Spell Scrolls you can read (full)"},
	{"rest", "after a fight: Hit Points, Hit Point Dice and what a Short Rest restores; after a Long Rest: changing prepared spells"},
	{"prep", "after a Long Rest: long-lasting spells you can cast (no Concentration); Mage Armor not active while you wear no armor"},
	{"after_battle", "after a fight: weapons you dropped, ammunition to recover, allies still down"},
	{"gear", "gear you carry that could be worn (gear_reminders)"},
}

func ensureReminderColumns() {
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS reminder_settings JSONB DEFAULT '{}'`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS after_battle JSONB`)
	db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS fight_start_resources JSONB`)
}

func reminderSettings(charID int) map[string]string {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(reminder_settings,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
	s := map[string]string{}
	json.Unmarshal(raw, &s)
	return s
}

// reminderMode: "brief", "full" or "off" for this kind.
func reminderMode(charID int, kind string) string {
	if kind == "gear" {
		var off bool
		db.QueryRow(`SELECT COALESCE(gear_reminders_off,false) FROM characters WHERE id = $1`, charID).Scan(&off)
		if off {
			return "off"
		}
	}
	s := reminderSettings(charID)
	if m := s[kind]; m != "" {
		return m
	}
	if m := s["all"]; m != "" {
		return m
	}
	return "brief"
}

// ---------------------------------------------------------------- spells, slots and resources

// slotFreeAtOrAbove: an unspent spell slot (or Pact Magic slot) of this level or higher.
func slotFreeAtOrAbove(charID, lvl int) bool {
	var class string
	var level int
	var usedRaw, pactRaw []byte
	if db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1), COALESCE(spell_slots_used,'{}'), COALESCE(pact_slots_used,'{}') FROM characters WHERE id = $1`, charID).
		Scan(&class, &level, &usedRaw, &pactRaw) != nil {
		return false
	}
	used, pact := map[string]int{}, map[string]int{}
	json.Unmarshal(usedRaw, &used)
	json.Unmarshal(pactRaw, &pact)
	for l, n := range characterSpellSlots(charID, class, level) {
		if l >= lvl && n-used[strconv.Itoa(l)] > 0 {
			return true
		}
	}
	for l, n := range characterPactSlots(charID) {
		if l >= lvl && n-pact[strconv.Itoa(l)] > 0 {
			return true
		}
	}
	return false
}

// spellCandidates: the spells the character might cast itself (known, prepared, origin and subclass spells).
func spellCandidates(charID int) []string {
	var known, prepared, choices []byte
	var subclass string
	var level int
	db.QueryRow(`SELECT COALESCE(known_spells,'[]'), COALESCE(prepared_spells,'[]'), COALESCE(subclass,''), COALESCE(level,1), COALESCE(subclass_choices,'{}')
		FROM characters WHERE id = $1`, charID).Scan(&known, &prepared, &subclass, &level, &choices)
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range jsonList(known) {
		add(k)
	}
	for _, k := range jsonList(prepared) {
		add(k)
	}
	for k := range originSpellsOf(charID) {
		add(k)
	}
	sc := map[string]string{}
	json.Unmarshal(choices, &sc)
	for _, k := range getDomainSpells(subclass, level, sc["circle_land"]) {
		add(k)
	}
	sort.Strings(out)
	return out
}

// castableSpellsWhere: names of the spells the character can cast now (a slot left for a leveled spell; one slot a turn) that match.
func castableSpellsWhere(charID int, match func(SRDSpell) bool) []string {
	var out []string
	for _, k := range spellCandidates(charID) {
		sp, ok := srdSpellsMemory[k]
		if !ok || !match(sp) {
			continue
		}
		if ok, _ := spellAvailableNatively(charID, k, ""); !ok {
			continue
		}
		if sp.Level > 0 && (!slotFreeAtOrAbove(charID, sp.Level) || slotAlreadyUsedThisTurn(charID, sp.Name) != "") {
			continue
		}
		out = append(out, sp.Name)
	}
	return out
}

func castingTimeHas(word string) func(SRDSpell) bool {
	return func(sp SRDSpell) bool { return strings.Contains(strings.ToLower(sp.CastingTime), word) }
}

// resourceLines: "Action Surge 1/1 (back on a Short Rest)", spell slots left.
func resourceLines(charID int) []string {
	var class string
	var level, cha int
	var usedRaw []byte
	if db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1), COALESCE(cha,10), COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).
		Scan(&class, &level, &cha, &usedRaw) != nil {
		return nil
	}
	cur := getCurrentClassResources(charID)
	max := characterMaxResources(charID, class, level, game.Modifier(cha))
	var out []string
	for _, d := range characterResourceDefs(charID, class) {
		if max[d.Key] <= 0 || d.Key == "arcane_recovery" || d.Key == "natural_recovery" {
			continue
		}
		when := "a Long Rest"
		if d.RecoverShort {
			when = "a Short Rest"
		}
		out = append(out, fmt.Sprintf("%s %d/%d (back on %s)", d.Name, cur[d.Key], max[d.Key], when))
	}
	used := map[string]int{}
	json.Unmarshal(usedRaw, &used)
	slots := characterSpellSlots(charID, class, level)
	var lv []int
	for l := range slots {
		lv = append(lv, l)
	}
	sort.Ints(lv)
	var parts []string
	for _, l := range lv {
		parts = append(parts, fmt.Sprintf("level %d: %d/%d", l, slots[l]-used[strconv.Itoa(l)], slots[l]))
	}
	if len(parts) > 0 {
		when := "a Long Rest"
		if strings.EqualFold(class, "warlock") && len(classLevelsOf(charID)) <= 1 {
			when = "a Short Rest (Pact Magic)"
		}
		out = append(out, "Spell slots "+strings.Join(parts, ", ")+" (back on "+when+")")
	}
	return out
}

// ---------------------------------------------------------------- items: potions and allies down

func carriedHealingPotions(charID int) []string {
	var out []string
	for _, it := range inventoryOf(charID) {
		n := itemStr(it, "name")
		if strings.Contains(strings.ToLower(n), "potion of") && strings.Contains(strings.ToLower(n), "healing") && !itemBool(it, "destroyed") {
			out = append(out, n)
		}
	}
	return out
}

// alliesDown: party members at 0 Hit Points who are neither dead nor Stable.
func alliesDown(charID int) []string {
	var out []string
	rows, err := db.Query(`SELECT name FROM characters WHERE lobby_id = (SELECT lobby_id FROM characters WHERE id = $1) AND id <> $1
		AND hp <= 0 AND NOT COALESCE(is_dead,false) AND NOT COALESCE(is_stable,false) ORDER BY id`, charID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func itemReminders(charID, hp, maxHP int, full bool) map[string]interface{} {
	out := map[string]interface{}{}
	if down := alliesDown(charID); len(down) > 0 {
		ways := "a healing spell, giving them a Potion of Healing (Bonus Action), Spare the Dying, or the stabilize action (DC 10 Wisdom (Medicine) check)"
		out["allies_down"] = fmt.Sprintf("%s at 0 Hit Points: %s.", strings.Join(down, ", "), ways)
	}
	if maxHP > 0 && hp*2 <= maxHP {
		if p := carriedHealingPotions(charID); len(p) > 0 {
			out["healing_potions"] = fmt.Sprintf("You are at %d/%d Hit Points and carry %s (drinking one is a Bonus Action).", hp, maxHP, strings.Join(p, ", "))
		}
	}
	if full {
		var scrolls []string
		for _, it := range inventoryOf(charID) {
			if strings.HasPrefix(strings.ToLower(itemStr(it, "name")), "spell scroll") {
				scrolls = append(scrolls, itemStr(it, "name"))
			}
		}
		if len(scrolls) > 0 {
			out["spell_scrolls"] = scrolls
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------- turn_left

var basicActions2024 = "Attack, Dash, Disengage, Dodge, Help, Hide, Influence, Magic, Ready, Search, Study, Utilize"

// turnLeft: what the character can still do this turn (nil when it isn't their turn in combat, or turn reminders are off).
func turnLeft(charID int) map[string]interface{} {
	mode := reminderMode(charID, "turn")
	if mode == "off" {
		return nil
	}
	var class, subclass string
	var level, hp, maxHP, movement, lobby, round int
	var actionUsed, bonusUsed, reactionUsed, hordeUsed bool
	var attacks sql.NullInt32
	var condRaw, choicesRaw []byte
	if db.QueryRow(`SELECT COALESCE(class,''), COALESCE(subclass,''), COALESCE(level,1), hp, max_hp, COALESCE(movement_remaining,0), COALESCE(lobby_id,0),
		COALESCE(action_used,false), COALESCE(bonus_action_used,false), COALESCE(reaction_used,false), COALESCE(horde_breaker_used,false), attacks_remaining,
		COALESCE(conditions,'[]'), COALESCE(subclass_choices,'{}') FROM characters WHERE id = $1`, charID).
		Scan(&class, &subclass, &level, &hp, &maxHP, &movement, &lobby, &actionUsed, &bonusUsed, &reactionUsed, &hordeUsed, &attacks, &condRaw, &choicesRaw) != nil {
		return nil
	}
	if lobby == 0 || currentTurnID(lobby) != charID || hp <= 0 {
		return nil
	}
	db.QueryRow(`SELECT COALESCE(round_number,1) FROM combat_state WHERE lobby_id = $1 AND active`, lobby).Scan(&round)
	var conditions []string
	json.Unmarshal(condRaw, &conditions)
	choices := map[string]string{}
	json.Unmarshal(choicesRaw, &choices)
	out := map[string]interface{}{}
	switch {
	case !actionUsed:
		out["action"] = "available"
	case attacks.Valid && attacks.Int32 > 0:
		out["action"] = fmt.Sprintf("Attack action: %d more attack(s)", attacks.Int32)
	default:
		out["action"] = "used"
	}
	if bonusUsed {
		out["bonus_action"] = "used"
	} else if opts := bonusOptions(charID, class, subclass, level, actionUsed, conditions, choices, hordeUsed, hp, maxHP); len(opts) > 0 {
		out["bonus_action"] = opts
	} else {
		out["bonus_action"] = "available (nothing you have uses it right now)"
	}
	out["movement_ft"] = movement
	if reactionUsed {
		out["reaction"] = "used"
	} else {
		rx := map[string]interface{}{"available": true, "options": reactionOptions(charID, class, level),
			"note": fmt.Sprintf("A Reaction is taken when its trigger happens, often on another creature's turn. Set standing orders so the server takes it for you: POST /api/characters/reaction-orders {\"character_id\": %d, \"order\": \"shield\", \"on\": true}.", charID)}
		if on := activeOrderNames(charID); len(on) > 0 {
			rx["standing_orders"] = on
		}
		out["reaction"] = rx
	}
	if !actionUsed && (mode == "full" || round <= 1) {
		out["actions"] = basicActions2024 + " (POST /api/action)"
	}
	if rm := reminderMode(charID, "resources"); rm != "off" && (rm == "full" || mode == "full" || round <= 1) {
		if rl := resourceLines(charID); len(rl) > 0 {
			out["resources"] = rl
		}
	}
	if im := reminderMode(charID, "items"); im != "off" {
		if ir := itemReminders(charID, hp, maxHP, im == "full"); ir != nil {
			out["items"] = ir
		}
	}
	return out
}

func bonusOptions(charID int, class, subclass string, level int, actionUsed bool, conditions []string, choices map[string]string, hordeUsed bool, hp, maxHP int) []string {
	cur := getCurrentClassResources(charID)
	needs := map[string]string{"rage": "rage", "second_wind": "second_wind", "flurry_of_blows": "focus"}
	var out []string
	for _, b := range buildBonusActions(strings.ToLower(class), actionUsed, false, conditions, subclass, level, choices, hordeUsed) {
		name := fmt.Sprint(b["name"])
		if av, ok := b["available"].(bool); ok && !av {
			continue
		}
		if key, ok := needs[name]; ok && cur[key] <= 0 {
			continue
		}
		if name == "offhand_attack" {
			if mh, oh := equippedWeapons(charID); mh == "" || oh == "" {
				continue
			}
		}
		out = append(out, name)
	}
	if strings.EqualFold(class, "bard") && cur["bardic_inspiration"] > 0 {
		out = append(out, "Bardic Inspiration")
	}
	// the class features my-turn lists (turn_options_2024.go): Second Wind, Lay on Hands, Innate Sorcery, Steady Aim, Wild Shape ...
	_, featBonus, _ := turnOptions2024(charID, class, subclass, level, containsStr(conditions, "raging"), false)
	for _, fb := range featBonus {
		name := fmt.Sprint(fb["name"])
		if strings.Contains(fmt.Sprint(fb["description"]), "(0 of ") || name == "Bardic Inspiration" || name == "Martial Arts strike" && !actionUsed {
			continue
		}
		if strings.HasPrefix(name, "Second Wind") {
			name = "second_wind"
		}
		out = append(out, name)
	}
	for _, s := range castableSpellsWhere(charID, castingTimeHas("bonus action")) {
		out = append(out, "cast "+s)
	}
	if maxHP > 0 && hp*2 <= maxHP && reminderMode(charID, "items") != "off" {
		for _, p := range carriedHealingPotions(charID) {
			out = append(out, "drink "+p)
			break
		}
	}
	return out
}

func reactionOptions(charID int, class string, level int) []string {
	out := []string{"Opportunity Attack (an enemy leaves your reach)"}
	c := strings.ToLower(class)
	levels := classLevelsOf(charID)
	if c == "rogue" && level >= 5 || levels["rogue"] >= 5 {
		out = append(out, "Uncanny Dodge (halve an attack's damage)")
	}
	if c == "monk" && level >= 3 || levels["monk"] >= 3 {
		out = append(out, "Deflect Attacks")
	}
	for _, s := range castableSpellsWhere(charID, castingTimeHas("reaction")) {
		out = append(out, "cast "+s)
	}
	return out
}

// withTurnReminders adds turn_left to a successful action reply while it is still the character's turn.
func withTurnReminders(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		augmentJSON(w, func(rw http.ResponseWriter) { h(rw, r) }, func(m map[string]interface{}) {
			var req struct {
				CharacterID int `json:"character_id"`
				CampaignID  int `json:"campaign_id"`
				CompanionID int `json:"companion_id"`
			}
			json.Unmarshal(body, &req)
			agentID, err := getAgentFromAuth(r)
			if err != nil || req.CompanionID != 0 {
				return
			}
			var charID int
			if db.QueryRow(`SELECT c.id FROM characters c JOIN lobbies l ON c.lobby_id = l.id
				WHERE c.agent_id = $1 AND l.status = 'active' AND ($2 = 0 OR c.id = $2) AND ($3 = 0 OR c.lobby_id = $3)
				ORDER BY l.id DESC, c.id DESC LIMIT 1`, agentID, pickCharacterID(r, req.CharacterID), pickCampaignID(r, req.CampaignID)).Scan(&charID) != nil {
				return
			}
			if n := takeSaveNotes(lobbyOfChar(charID)); n != "" {
				if res, ok := m["result"].(string); ok {
					m["result"] = res + n // Magic Resistance, Legendary Resistance (monster_reactions_2024.go)
				} else {
					m["save_notes"] = strings.TrimSpace(n)
				}
			}
			if t := turnLeft(charID); t != nil {
				m["turn_left"] = t
			}
		})
	}
}

// augmentJSON runs a handler into a recorder; a 200 JSON object reply without "error" goes through add before it is written.
func augmentJSON(w http.ResponseWriter, run func(http.ResponseWriter), add func(map[string]interface{})) {
	rec := httptest.NewRecorder()
	run(rec)
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	out := rec.Body.Bytes()
	if rec.Code == http.StatusOK {
		var m map[string]interface{}
		if json.Unmarshal(out, &m) == nil && m != nil && m["error"] == nil {
			add(m)
			if b, err := json.Marshal(m); err == nil {
				out = append(b, '\n')
			}
		}
	}
	w.WriteHeader(rec.Code)
	w.Write(out)
}

// ---------------------------------------------------------------- fights: start and end

// fightCombatantIDs: the characters in the current turn order.
func fightCombatantIDs(lobbyID int) []int {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(turn_order,'[]') FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw)
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	var ids []int
	for _, e := range entries {
		if id := asInt(e["id"]); id > 0 {
			if mon, _ := e["is_monster"].(bool); !mon {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// fightStarted: each character's resources are noted (for "unused this fight") and the last fight's summary is cleared.
func fightStarted(lobbyID int) {
	for _, id := range fightCombatantIDs(lobbyID) {
		b, _ := json.Marshal(getCurrentClassResources(id))
		db.Exec(`UPDATE characters SET fight_start_resources = $1, after_battle = NULL WHERE id = $2`, string(b), id)
	}
}

// fightEnded: each character who fought gets an after-battle note (what went unused); the GM's reply gets the party's state.
func fightEnded(ids []int) []map[string]interface{} {
	var party []map[string]interface{}
	for _, id := range ids {
		var raw []byte
		var name string
		var hp, maxHP, level, spent int
		db.QueryRow(`SELECT name, hp, max_hp, COALESCE(level,1), COALESCE(hit_dice_spent,0), COALESCE(fight_start_resources,'{}') FROM characters WHERE id = $1`, id).
			Scan(&name, &hp, &maxHP, &level, &spent, &raw)
		start := map[string]int{}
		json.Unmarshal(raw, &start)
		cur := getCurrentClassResources(id)
		var unused []string
		var class string
		db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, id).Scan(&class)
		for _, d := range characterResourceDefs(id, class) {
			if start[d.Key] > 0 && cur[d.Key] >= start[d.Key] && d.Key != "arcane_recovery" && d.Key != "natural_recovery" {
				when := "a Long Rest"
				if d.RecoverShort {
					when = "a Short Rest"
				}
				unused = append(unused, fmt.Sprintf("%s (back on %s)", d.Name, when))
			}
		}
		note, _ := json.Marshal(map[string]interface{}{"unused": unused})
		db.Exec(`UPDATE characters SET after_battle = $1 WHERE id = $2`, string(note), id)
		party = append(party, map[string]interface{}{"character_id": id, "name": name, "hp": fmt.Sprintf("%d/%d", hp, maxHP), "hit_dice_left": maxInt(level-spent, 0)})
	}
	return party
}

// shortRestRestores: what a Short Rest would bring back now.
func shortRestRestores(charID int) []string {
	var class string
	var level, cha, spent, hp, maxHP int
	var usedRaw []byte
	db.QueryRow(`SELECT COALESCE(class,''), COALESCE(level,1), COALESCE(cha,10), COALESCE(hit_dice_spent,0), hp, max_hp, COALESCE(spell_slots_used,'{}') FROM characters WHERE id = $1`, charID).
		Scan(&class, &level, &cha, &spent, &hp, &maxHP, &usedRaw)
	var out []string
	if hp < maxHP && level-spent > 0 {
		out = append(out, fmt.Sprintf("Hit Points by spending Hit Point Dice (%d left)", level-spent))
	}
	cur := getCurrentClassResources(charID)
	max := characterMaxResources(charID, class, level, game.Modifier(cha))
	for _, d := range characterResourceDefs(charID, class) {
		if d.RecoverShort && cur[d.Key] < max[d.Key] {
			out = append(out, d.Name)
		}
	}
	if strings.EqualFold(class, "warlock") && len(classLevelsOf(charID)) <= 1 && string(usedRaw) != "{}" && !slotsAllFree(usedRaw) {
		out = append(out, "Pact Magic spell slots")
	}
	return out
}

func slotsAllFree(raw []byte) bool {
	m := map[string]int{}
	json.Unmarshal(raw, &m)
	for _, v := range m {
		if v > 0 {
			return false
		}
	}
	return true
}

// afterBattleReminders: shown in my-turn outside combat until the character rests or the next fight starts.
func afterBattleReminders(charID int) map[string]interface{} {
	var raw []byte
	var hp, maxHP, ammo, lobby int
	if db.QueryRow(`SELECT after_battle, hp, max_hp, COALESCE(ammo_used_since_rest,0), COALESCE(lobby_id,0) FROM characters WHERE id = $1 AND after_battle IS NOT NULL`, charID).
		Scan(&raw, &hp, &maxHP, &ammo, &lobby) != nil {
		return nil
	}
	note := struct {
		Unused []string `json:"unused"`
	}{}
	json.Unmarshal(raw, &note)
	out := map[string]interface{}{}
	if reminderMode(charID, "rest") != "off" {
		rest := map[string]interface{}{"hp": fmt.Sprintf("%d/%d", hp, maxHP),
			"long_rest": fmt.Sprintf("8 hours, when the GM allows it (the GM keeps time): all Hit Points, spell slots and half your Hit Point Dice. POST /api/characters/%d/rest", charID)}
		if sr := shortRestRestores(charID); len(sr) > 0 {
			rest["short_rest"] = fmt.Sprintf("1 hour: %s. POST /api/characters/%d/short-rest {\"hit_dice\": N}", strings.Join(sr, ", "), charID)
		}
		out["rest"] = rest
	}
	if reminderMode(charID, "resources") != "off" && len(note.Unused) > 0 {
		out["unused_this_fight"] = note.Unused
	}
	if reminderMode(charID, "after_battle") != "off" {
		if rows, err := db.Query(`SELECT id, name, quantity FROM dropped_items WHERE lobby_id = $1 AND dropped_by = $2 AND taken_by IS NULL ORDER BY id`, lobby, charID); err == nil {
			var dropped []string
			for rows.Next() {
				var id, q int
				var n string
				rows.Scan(&id, &n, &q)
				dropped = append(dropped, fmt.Sprintf("%s x%d (dropped_id %d)", n, q, id))
			}
			rows.Close()
			if len(dropped) > 0 {
				out["dropped_items"] = fmt.Sprintf("On the ground: %s. Pick up with POST /api/characters/%d/equipment/pick-up {\"dropped_id\": N}.", strings.Join(dropped, ", "), charID)
			}
		}
		if ammo > 0 {
			out["ammunition"] = fmt.Sprintf("You used %d pieces of ammunition; after a fight, a minute of searching recovers half of them (the GM: POST /api/gm/recover-ammo).", ammo)
		}
		if down := alliesDown(charID); len(down) > 0 {
			out["allies_down"] = strings.Join(down, ", ") + " at 0 Hit Points."
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------- rests

// preparedSpellChange: what the class may change after a Long Rest (2024 rules).
func preparedSpellChange(charID int, class string) string {
	levels := classLevelsOf(charID)
	has := func(c string) bool { return strings.EqualFold(class, c) || levels[c] > 0 }
	switch {
	case has("cleric") || has("druid") || has("wizard"):
		return fmt.Sprintf("You can change your prepared spells now (Cleric, Druid, Wizard: the whole list after a Long Rest): POST /api/characters/%d/prepare.", charID)
	case has("paladin") || has("ranger"):
		return fmt.Sprintf("You can replace one prepared spell now (after a Long Rest): POST /api/characters/%d/prepare.", charID)
	}
	return ""
}

// longLastingSpells: spells of an hour or more without Concentration the character can cast now.
func longLastingSpells(charID int) []string {
	names := castableSpellsWhere(charID, func(sp SRDSpell) bool {
		d := strings.ToLower(sp.Duration)
		return sp.Level > 0 && !sp.Concentration && strings.Contains(d, "hour") && !strings.Contains(strings.ToLower(sp.CastingTime), "reaction")
	})
	var out []string
	for _, n := range names {
		for _, sp := range srdSpellsMemory {
			if sp.Name == n {
				out = append(out, fmt.Sprintf("%s (%s)", n, sp.Duration))
				break
			}
		}
	}
	return out
}

// restReminders: added to a rest reply; a rest also clears the after-battle note.
func restReminders(charID int, long bool) map[string]interface{} {
	db.Exec(`UPDATE characters SET after_battle = NULL WHERE id = $1`, charID)
	if !long {
		return nil
	}
	var class string
	db.QueryRow(`SELECT COALESCE(class,'') FROM characters WHERE id = $1`, charID).Scan(&class)
	out := map[string]interface{}{}
	if reminderMode(charID, "rest") != "off" {
		if p := preparedSpellChange(charID, class); p != "" {
			out["prepared_spells"] = p
		}
	}
	if reminderMode(charID, "prep") != "off" {
		if l := longLastingSpells(charID); len(l) > 0 {
			out["long_lasting_spells"] = l
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// withRestReminders adds reminders to a successful rest reply.
func withRestReminders(h func(http.ResponseWriter, *http.Request, int), forceShort bool) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, r *http.Request, charID int) {
		if r.Method != http.MethodPost {
			h(w, r, charID)
			return
		}
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<16))
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		var kind struct {
			Type string `json:"type"`
		}
		json.Unmarshal(body, &kind)
		long := !forceShort && !strings.EqualFold(strings.TrimSpace(kind.Type), "short")
		augmentJSON(w, func(rw http.ResponseWriter) { h(rw, r, charID) }, func(m map[string]interface{}) {
			if rr := restReminders(charID, long); rr != nil {
				m["reminders"] = rr
			}
		})
	}
}

// prepReminders (my-turn outside combat): Mage Armor known and not active while no armor is worn.
func prepReminders(charID int) map[string]interface{} {
	if reminderMode(charID, "prep") == "off" || equippedArmorName(charID) != "" {
		return nil
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM spell_effects WHERE target_id = $1 AND kind = 'mage_armor'`, charID).Scan(&n)
	if n > 0 || len(castableSpellsWhere(charID, func(sp SRDSpell) bool { return sp.Name == "Mage Armor" })) == 0 {
		return nil
	}
	return map[string]interface{}{"mage_armor": "You can cast Mage Armor (8 hours: AC 13 + your Dexterity modifier while you wear no armor) and aren't under it."}
}

// myTurnReminders: the reminders block of my-turn.
func myTurnReminders(charID int, inCombat, isMyTurn bool) map[string]interface{} {
	out := map[string]interface{}{}
	if inCombat {
		if isMyTurn {
			if t := turnLeft(charID); t != nil {
				out["turn_left"] = t
			}
		}
	} else {
		if ab := afterBattleReminders(charID); ab != nil {
			out["after_battle"] = ab
		}
		if pr := prepReminders(charID); pr != nil {
			out["prep"] = pr
		}
		if cn := conditionTimeNote(charID); cn != "" {
			out["conditions"] = cn // a repeated save outside combat (F227)
		}
	}
	if len(out) == 0 {
		return nil
	}
	out["settings"] = fmt.Sprintf("brief, full or off per kind: POST /api/characters/reminders {\"character_id\": %d, \"kind\": \"all\", \"mode\": \"off\"}", charID)
	return out
}

// handleReminders godoc
// @Summary Reminder settings: what the server reminds you of
// @Description Reminders give facts and commands, never advice. Kinds: turn (what is left this turn after each action and in my-turn: Action, attacks, Bonus Action options, movement, Reaction options; the basic actions on round 1), resources (class resources and spell slots with when they come back on round 1; what went unused after the fight), items (a healing potion when at half Hit Points or less; allies at 0 Hit Points), rest (after a fight: Hit Points, Hit Point Dice, what a Short Rest restores; after a Long Rest: changing prepared spells), prep (after a Long Rest: long-lasting spells without Concentration; Mage Armor not active), after_battle (dropped weapons, ammunition to recover, allies down), gear (gear_reminders). Each is "brief" (default), "full" (more every turn) or "off". GET ?character_id=N shows the settings; POST {"character_id": N, "kind": "turn" or "all", "mode": "brief"|"full"|"off"} sets them. Owner or GM.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param character_id query int false "Character ID (GET)"
// @Param request body object{character_id=int,kind=string,mode=string} false "A kind (or all) and its mode (POST)"
// @Success 200 {object} map[string]interface{} "The settings"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/reminders [get]
// @Router /characters/reminders [post]
func handleReminders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		CharacterID int    `json:"character_id"`
		Kind        string `json:"kind"`
		Mode        string `json:"mode"`
	}
	switch r.Method {
	case http.MethodGet:
		req.CharacterID, _ = strconv.Atoi(r.URL.Query().Get("character_id"))
	case http.MethodPost:
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id required"})
		return
	}
	if !requireOwnerOrGM(w, r, req.CharacterID) {
		return
	}
	view := func() map[string]interface{} {
		kinds := []map[string]interface{}{}
		for _, k := range reminderKinds {
			kinds = append(kinds, map[string]interface{}{"kind": k.Key, "mode": reminderMode(req.CharacterID, k.Key), "what": k.What})
		}
		return map[string]interface{}{"character_id": req.CharacterID, "reminders": kinds,
			"how": fmt.Sprintf("POST /api/characters/reminders {\"character_id\": %d, \"kind\": \"turn\" (or \"all\"), \"mode\": \"brief\" | \"full\" | \"off\"}", req.CharacterID)}
	}
	if r.Method == http.MethodGet {
		writeJSONStatus(w, http.StatusOK, view())
		return
	}
	kind, mode := strings.ToLower(strings.TrimSpace(req.Kind)), strings.ToLower(strings.TrimSpace(req.Mode))
	if mode != "brief" && mode != "full" && mode != "off" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "bad_mode", "message": "mode is brief, full or off."})
		return
	}
	valid := kind == "all"
	for _, k := range reminderKinds {
		valid = valid || k.Key == kind
	}
	if !valid {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "bad_kind", "message": "kind is all, turn, resources, items, rest, prep, after_battle or gear."})
		return
	}
	s := reminderSettings(req.CharacterID)
	if kind == "all" {
		s = map[string]string{"all": mode}
	} else {
		s[kind] = mode
	}
	b, _ := json.Marshal(s)
	db.Exec(`UPDATE characters SET reminder_settings = $1 WHERE id = $2`, string(b), req.CharacterID)
	if kind == "all" || kind == "gear" {
		db.Exec(`UPDATE characters SET gear_reminders_off = $1 WHERE id = $2`, mode == "off", req.CharacterID)
	}
	out := view()
	out["success"] = true
	writeJSONStatus(w, http.StatusOK, out)
}
