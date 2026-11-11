package main

// Companions: creatures that belong to a character and fight on the party's side (the project owner, 2026-11-08).
//   familiar  Find Familiar (also Pact of the Chain and a Druid's Wild Companion): a CR 0 Beast form, as a Celestial, Fey or Fiend.
//             It stays until it drops to 0 Hit Points or its owner dismisses it forever. As a Magic action the owner can dismiss it to a
//             pocket dimension and, later, call it back within 30 feet without casting the spell again. Casting the spell again while
//             it exists changes its form; after it dropped to 0 HP, casting again brings it back. It doesn't depend on Concentration.
//             Find Familiar: "A familiar can't attack" on its own, but it delivers its owner's touch spells with its Reaction (cast with
//             "delivered by my familiar" in the description; the spell's attack roll is the owner's). Pact of the Chain: the special
//             forms (Imp, Pseudodragon, Quasit, Skeleton, Sphinx of Wonder, Sprite, Venomous Snake), and when the owner takes the
//             Attack action he can forgo one of his attacks so the familiar makes one attack of its own with its Reaction; Investment of
//             the Chain Master adds Quick Attack (a Bonus Action: the familiar takes the Attack action) and Necrotic or Radiant damage.
//   steed     Find Steed: the Otherworldly Steed at the slot level. A new casting replaces it; it disappears at 0 HP or if its owner dies.
//   summon    Summon Dragon (Draconic Spirit), Giant Insect, Animate Objects: the spell's stat block at the slot level. It ends when the
//             owner's Concentration on the spell ends, or at 0 HP.
//   pet       A creature from the monster list that the GM gives a character (POST /api/gm/companion). It stays until it dies or is let go.
// The owner's player commands them: POST /api/action with "companion_id" and attack (target_id: a monster), dodge, dash, disengage,
// help (target_id: a monster the next ally attack against has Advantage), hide, search or move. A companion acts once per round.
//
// RULE DEVIATION (documented in README "Rule deviations"; server setting AGENTRPG_FAMILIAR_INITIATIVE): in the SRD a familiar rolls its
// own Initiative and takes its own turn. Here every companion acts during its owner's turn by default ("owner"), so the players' agents,
// which check for their turn on a schedule, don't wait an extra turn per creature. AGENTRPG_FAMILIAR_INITIATIVE=own gives familiars the
// SRD rule: their own Initiative entry in the turn order, taken by the owner's player. Steeds and summons share the owner's Initiative in
// the SRD as well, and they act on the owner's turn in both settings.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

const companionIDBase = 1000000000 // companions' ids in a turn order (characters are small positive ids, monsters negative)

func isCompanionCombatID(id int) bool { return id >= companionIDBase }

type compAttack struct {
	Name      string `json:"name"`
	Bonus     int    `json:"bonus"`
	Dice      string `json:"dice"`
	Type      string `json:"type"`
	Extra     string `json:"extra,omitempty"`      // extra dice of another type (Giant Insect: 1d4 Poison)
	ExtraType string `json:"extra_type,omitempty"` // that type
	Count     int    `json:"count"`                // attacks per turn (Multiattack)
}

type compTrait struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type companion struct {
	ID            int          `json:"id"`
	OwnerID       int          `json:"owner_id"`
	LobbyID       int          `json:"campaign_id"`
	Kind          string       `json:"kind"`
	Name          string       `json:"name"`
	Source        string       `json:"source"`
	Form          string       `json:"form"`
	CreatureType  string       `json:"creature_type"`
	Status        string       `json:"status"` // active, dismissed (familiar in its pocket dimension), gone
	SlotLevel     int          `json:"slot_level,omitempty"`
	HP            int          `json:"hp"`
	MaxHP         int          `json:"max_hp"`
	AC            int          `json:"ac"`
	Speed         string       `json:"speed"`
	Attacks       []compAttack `json:"attacks"`
	Notes         string       `json:"notes,omitempty"`
	Concentration string       `json:"concentration,omitempty"` // the spell the owner must keep Concentration on
	EndReason     string       `json:"end_reason,omitempty"`
	Nickname      string       `json:"nickname,omitempty"`
	Traits        []compTrait  `json:"traits,omitempty"`
	CombatID      int          `json:"combat_id"`
	lastRound     int
	lastLobby     int
}

var companionsOnce sync.Once

func ensureCompanions() {
	companionsOnce.Do(func() {
		if db == nil {
			return
		}
		db.Exec(`CREATE TABLE IF NOT EXISTS companions (
			id SERIAL PRIMARY KEY, owner_id INTEGER NOT NULL, lobby_id INTEGER, kind TEXT NOT NULL, name TEXT NOT NULL, source TEXT DEFAULT '',
			form TEXT DEFAULT '', creature_type TEXT DEFAULT '', status TEXT DEFAULT 'active', slot_level INTEGER DEFAULT 0,
			hp INTEGER DEFAULT 1, max_hp INTEGER DEFAULT 1, ac INTEGER DEFAULT 10, speed TEXT DEFAULT '', attacks JSONB DEFAULT '[]',
			notes TEXT DEFAULT '', concentration TEXT DEFAULT '', last_acted_round INTEGER DEFAULT 0, last_acted_lobby INTEGER DEFAULT 0,
			end_reason TEXT DEFAULT '', created_at TIMESTAMP DEFAULT NOW(), ended_at TIMESTAMP)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS companions_owner ON companions (owner_id)`)
		db.Exec(`ALTER TABLE companions ADD COLUMN IF NOT EXISTS reaction_round INTEGER DEFAULT 0`)
		db.Exec(`ALTER TABLE companions ADD COLUMN IF NOT EXISTS reaction_lobby INTEGER DEFAULT 0`)
		db.Exec(`ALTER TABLE companions ADD COLUMN IF NOT EXISTS nickname TEXT DEFAULT ''`)  // the name its owner gives it
		db.Exec(`ALTER TABLE companions ADD COLUMN IF NOT EXISTS traits JSONB DEFAULT '[]'`) // the stat block's traits (an owl's Flyby)
		// companions made before traits were stored take their form's traits
		db.Exec(`UPDATE companions c SET traits = m.traits FROM monsters m WHERE m.slug = c.form AND (c.traits IS NULL OR c.traits = '[]'::jsonb) AND m.traits IS NOT NULL`)
	})
}

// familiarInitiativeMode is the server setting: "owner" (default: a familiar acts on its owner's turn) or "own" (the SRD rule).
func familiarInitiativeMode() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTRPG_FAMILIAR_INITIATIVE")), "own") {
		return "own"
	}
	return "owner"
}

const companionCols = `id, owner_id, COALESCE(lobby_id,0), kind, name, COALESCE(source,''), COALESCE(form,''), COALESCE(creature_type,''), COALESCE(status,'active'),
	COALESCE(slot_level,0), hp, max_hp, ac, COALESCE(speed,''), COALESCE(attacks,'[]'), COALESCE(notes,''), COALESCE(concentration,''), COALESCE(end_reason,''),
	COALESCE(last_acted_round,0), COALESCE(last_acted_lobby,0), COALESCE(nickname,''), COALESCE(traits,'[]')`

func scanCompanion(row interface{ Scan(...interface{}) error }) (companion, error) {
	var c companion
	var att, traits []byte
	err := row.Scan(&c.ID, &c.OwnerID, &c.LobbyID, &c.Kind, &c.Name, &c.Source, &c.Form, &c.CreatureType, &c.Status, &c.SlotLevel, &c.HP, &c.MaxHP,
		&c.AC, &c.Speed, &att, &c.Notes, &c.Concentration, &c.EndReason, &c.lastRound, &c.lastLobby, &c.Nickname, &traits)
	json.Unmarshal(att, &c.Attacks)
	json.Unmarshal(traits, &c.Traits)
	c.CombatID = companionIDBase + c.ID
	return c, err
}

func loadCompanion(id int) (companion, bool) {
	ensureCompanions()
	c, err := scanCompanion(db.QueryRow(`SELECT `+companionCols+` FROM companions WHERE id = $1`, id))
	return c, err == nil
}

// companionsOf lists a character's companions that still exist (active or dismissed), after checking their endings.
func companionsOf(ownerID int) []companion {
	ensureCompanions()
	syncCompanionEndings(ownerID)
	rows, err := db.Query(`SELECT `+companionCols+` FROM companions WHERE owner_id = $1 AND status <> 'gone' ORDER BY id`, ownerID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []companion
	for rows.Next() {
		if c, err := scanCompanion(rows); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// campaignCompanions lists the companions of a campaign's characters (active and dismissed).
func campaignCompanions(lobbyID int) []companion {
	var out []companion
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1`, lobbyID)
	if err != nil {
		return nil
	}
	var ids []int
	for rows.Next() {
		var id int
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		out = append(out, companionsOf(id)...)
	}
	return out
}

func endCompanion(id int, reason string) {
	db.Exec(`UPDATE companions SET status = 'gone', end_reason = $1, ended_at = NOW() WHERE id = $2 AND status <> 'gone'`, reason, id)
}

// syncCompanionEndings ends summons whose Concentration ended and steeds whose owner died.
func syncCompanionEndings(ownerID int) {
	var conc sql.NullString
	var dead bool
	db.QueryRow(`SELECT concentrating_on, COALESCE(is_dead,false) FROM characters WHERE id = $1`, ownerID).Scan(&conc, &dead)
	rows, err := db.Query(`SELECT id, kind, COALESCE(concentration,'') FROM companions WHERE owner_id = $1 AND status <> 'gone'`, ownerID)
	if err != nil {
		return
	}
	type end struct {
		id     int
		reason string
	}
	var ends []end
	for rows.Next() {
		var id int
		var kind, need string
		rows.Scan(&id, &kind, &need)
		if need != "" && !strings.EqualFold(strings.TrimSpace(conc.String), need) {
			ends = append(ends, end{id, "Concentration on " + need + " ended"})
		}
		if kind == "steed" && dead {
			ends = append(ends, end{id, "its owner died"})
		}
	}
	rows.Close()
	for _, e := range ends {
		endCompanion(e.id, e.reason)
	}
}

// casterNumbers: spellcasting ability modifier, spell attack bonus and spell save DC.
func casterNumbers(charID int) (mod, atk, dc int) {
	var class string
	var level, intl, wis, cha int
	db.QueryRow(`SELECT LOWER(class), level, intl, wis, cha FROM characters WHERE id = $1`, charID).Scan(&class, &level, &intl, &wis, &cha)
	switch strings.ToUpper(srdClasses[class].Spellcasting) {
	case "INT":
		mod = game.Modifier(intl)
	case "WIS":
		mod = game.Modifier(wis)
	default:
		mod = game.Modifier(cha)
	}
	atk = mod + game.ProficiencyBonus(level) + magicSpellAttackBonus(charID)
	dc = game.SpellSaveDC(level, mod) + magicSpellDCBonus(charID)
	return
}

func pickWord(desc string, words []string, def string) string {
	low := strings.ToLower(desc)
	for _, w := range words {
		if strings.Contains(low, w) {
			return w
		}
	}
	return def
}

var familiarForms = []string{"bat", "cat", "frog", "hawk", "lizard", "octopus", "owl", "rat", "raven", "spider", "weasel"}

// chainForms: Pact of the Chain's special forms (checked before the normal ones, so "venomous snake" isn't read as "snake").
var chainForms = []string{"pseudodragon", "sphinx of wonder", "venomous snake", "imp", "quasit", "skeleton", "sprite"}

// formAttacks: the attacks in a monster's stat block (the familiar's own attacks, used through Pact of the Chain).
func formAttacks(slug string) []compAttack {
	var raw []byte
	db.QueryRow(`SELECT COALESCE(actions,'[]') FROM monsters WHERE slug = $1`, slug).Scan(&raw)
	var acts []map[string]interface{}
	json.Unmarshal(raw, &acts)
	var out []compAttack
	for _, a := range acts {
		b := asInt(a["attack_bonus"])
		desc := fmt.Sprint(a["desc"])
		if b == 0 || !strings.Contains(desc, "Attack Roll") {
			continue
		}
		at := compAttack{Name: fmt.Sprint(a["name"]), Bonus: b, Dice: fmt.Sprint(a["damage_dice"]), Type: fmt.Sprint(a["damage_type"]), Count: 1}
		if at.Dice == "" || at.Dice == "<nil>" {
			// flat damage ("Hit: 1 Slashing damage"): 1d1 plus the rest
			m := flatHitRe.FindStringSubmatch(desc)
			if m == nil {
				continue
			}
			n := atoiSafe(m[1])
			at.Dice = fmt.Sprintf("1d1+%d", n-1)
			if n <= 1 {
				at.Dice = "1d1"
			}
			at.Type = m[2]
		}
		if m := plusDamageRe.FindStringSubmatch(desc); m != nil { // "plus 7 (2d6) Poison damage"
			at.Extra, at.ExtraType = m[1], m[2]
		}
		out = append(out, at)
	}
	return out
}

var (
	flatHitRe    = regexp.MustCompile(`Hit: (\d+) ([A-Za-z]+) damage`)
	plusDamageRe = regexp.MustCompile(`plus \d+ \((\d+d\d+(?: ?\+ ?\d+)?)\) ([A-Za-z]+) damage`)
)

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// companionFromSpell creates the companion a spell calls (cast resolution in actions.go). It returns the text added to the cast result,
// or "" for other spells.
func companionFromSpell(charID int, spellKey string, slot int, description string) string {
	switch spellKey {
	case "find-familiar", "find-steed", "summon-dragon", "giant-insect", "animate-objects":
	default:
		return ""
	}
	ensureCompanions()
	var lobbyID int
	var owner string
	db.QueryRow(`SELECT COALESCE(lobby_id,0), name FROM characters WHERE id = $1`, charID).Scan(&lobbyID, &owner)
	mod, atk, _ := casterNumbers(charID)
	if slot < 1 {
		slot = map[string]int{"find-familiar": 1, "find-steed": 2, "summon-dragon": 5, "giant-insect": 4, "animate-objects": 5}[spellKey]
	}
	insert := func(c companion) int {
		attacks, _ := json.Marshal(c.Attacks)
		var id int
		db.QueryRow(`INSERT INTO companions (owner_id, lobby_id, kind, name, source, form, creature_type, slot_level, hp, max_hp, ac, speed, attacks, notes, concentration)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10,$11,$12,$13,$14) RETURNING id`,
			charID, lobbyID, c.Kind, c.Name, spellKey, c.Form, c.CreatureType, slot, c.MaxHP, c.AC, c.Speed, string(attacks), c.Notes, c.Concentration).Scan(&id)
		return id
	}
	how := func(id int, name string) string {
		return fmt.Sprintf(" %s is your companion (id %d): command it with POST /api/action {\"companion_id\": %d, \"action\": \"attack\", \"target_id\": ...}.", name, id, id)
	}
	switch spellKey {
	case "find-familiar":
		form := pickWord(description, familiarForms, "owl")
		if hasPactBoon(charID, "chain") {
			if f := pickWord(description, chainForms, ""); f != "" {
				form = strings.ReplaceAll(f, " ", "-")
			}
		}
		ctype := pickWord(description, []string{"celestial", "fey", "fiend"}, "fey")
		var mname, speed string
		var hp, ac int
		if db.QueryRow(`SELECT name, hp, ac, COALESCE(speed_text,'') FROM monsters WHERE slug = $1`, form).Scan(&mname, &hp, &ac, &speed) != nil {
			return " (The familiar's form wasn't found.)"
		}
		name := fmt.Sprintf("%s's %s", owner, mname)
		// One familiar only: casting again while it exists changes its form; after it dropped to 0 HP, the spell brings it back.
		var existing int
		db.QueryRow(`SELECT id FROM companions WHERE owner_id = $1 AND kind = 'familiar' AND status <> 'gone' ORDER BY id DESC LIMIT 1`, charID).Scan(&existing)
		if hasInvocation(charID, "investment-of-the-chain-master") && !strings.Contains(strings.ToLower(speed), "fly") {
			speed += ", Fly 40 ft. (Investment of the Chain Master)"
		}
		attacks, _ := json.Marshal(formAttacks(form))
		if existing > 0 {
			db.Exec(`UPDATE companions SET form = $1, name = $2, creature_type = $3, hp = $4, max_hp = $4, ac = $5, speed = $6, status = 'active', lobby_id = $7, attacks = $9,
				traits = (SELECT COALESCE(traits,'[]') FROM monsters WHERE slug = $1) WHERE id = $8`,
				form, name, ctype, hp, ac, speed, lobbyID, existing, string(attacks))
			addOwnInitiativeEntry(lobbyID, existing)
			return fmt.Sprintf(" Your familiar takes a new form: %s (%s, HP %d, AC %d)."+how(existing, name), mname, ctype, hp, ac)
		}
		notes := "It delivers your touch spells with its Reaction (cast with \"delivered by my familiar\"); you can see through its eyes (Bonus Action). On its own a familiar can't attack."
		if hasPactBoon(charID, "chain") {
			notes += " Pact of the Chain: when you take the Attack action, forgo one of your attacks and it attacks with its Reaction (companion attack)."
			if hasInvocation(charID, "investment-of-the-chain-master") {
				notes += " Investment of the Chain Master: Quick Attack (description \"quick attack\") spends your Bonus Action and it takes the Attack action; say \"radiant\" or \"necrotic\" to change its Bludgeoning, Piercing or Slashing damage."
			}
		}
		id := insert(companion{Kind: "familiar", Name: name, Form: form, CreatureType: ctype, MaxHP: hp, AC: ac, Speed: speed, Attacks: formAttacks(form), Notes: notes})
		db.Exec(`UPDATE companions SET traits = (SELECT COALESCE(traits,'[]') FROM monsters WHERE slug = $1) WHERE id = $2`, form, id)
		addOwnInitiativeEntry(lobbyID, id)
		return fmt.Sprintf(" %s appears (%s, HP %d, AC %d)."+how(id, name), name, ctype, hp, ac)
	case "find-steed":
		ctype := pickWord(description, []string{"celestial", "fey", "fiend"}, "celestial")
		dtype := map[string]string{"celestial": "Radiant", "fey": "Psychic", "fiend": "Necrotic"}[ctype]
		speed := "60 ft."
		if slot >= 4 {
			speed = "60 ft., Fly 60 ft."
		}
		db.Exec(`UPDATE companions SET status = 'gone', end_reason = 'replaced by a new steed', ended_at = NOW() WHERE owner_id = $1 AND kind = 'steed' AND status <> 'gone'`, charID)
		name := fmt.Sprintf("%s's Otherworldly Steed", owner)
		id := insert(companion{Kind: "steed", Name: name, Form: "otherworldly-steed", CreatureType: ctype, MaxHP: 5 + 10*slot, AC: 10 + slot, Speed: speed,
			Attacks: []compAttack{{Name: "Otherworldly Slam", Bonus: atk, Dice: fmt.Sprintf("1d8+%d", slot), Type: dtype, Count: 1}},
			Notes:   "Large. A controlled mount while you ride it (POST /api/characters/mount). Life Bond: it regains the Hit Points you regain from a level 1+ spell when you're within 5 feet (automatic). Once per Long Rest, by its kind: Healing Touch (Celestial), Fey Step (Fey), Fell Glare (Fiend): companion actions healing_touch, fey_step, fell_glare."})
		return fmt.Sprintf(" %s appears (%s, HP %d, AC %d)."+how(id, name), name, ctype, 5+10*slot, 10+slot)
	case "summon-dragon":
		n := slot / 2
		hp := 50 + 10*(slot-5)
		name := fmt.Sprintf("%s's Draconic Spirit", owner)
		id := insert(companion{Kind: "summon", Name: name, Form: "draconic-spirit", CreatureType: "dragon", MaxHP: hp, AC: 14 + slot, Speed: "30 ft., Fly 60 ft., Swim 30 ft.",
			Attacks:       []compAttack{{Name: "Rend", Bonus: atk, Dice: fmt.Sprintf("1d6+%d", 4+slot), Type: "Piercing", Count: n}},
			Notes:         "Breath Weapon (with its Rends, once per round): companion action \"breath\" with target_ids (the creatures in the 30-foot Cone) and the type (acid, cold, fire, lightning or poison): Dexterity save against your spell save DC, 2d6, half on a success.",
			Concentration: "Summon Dragon"})
		return fmt.Sprintf(" %s appears (HP %d, AC %d; %d Rend attacks a turn)."+how(id, name), name, hp, 14+slot, n)
	case "giant-insect":
		n := slot / 2
		hp := 30 + 10*(slot-4)
		form := pickWord(description, []string{"centipede", "spider", "wasp"}, "spider")
		name := fmt.Sprintf("%s's Giant Insect (%s)", owner, form)
		id := insert(companion{Kind: "summon", Name: name, Form: "giant-insect", CreatureType: "beast", MaxHP: hp, AC: 11 + slot, Speed: "40 ft., Climb 40 ft.",
			Attacks:       []compAttack{{Name: "Poison Jab", Bonus: atk, Dice: fmt.Sprintf("1d6+%d", 3+slot), Type: "Piercing", Extra: "1d4", ExtraType: "Poison", Count: n}},
			Concentration: "Giant Insect"})
		return fmt.Sprintf(" %s appears (HP %d, AC %d; %d Poison Jab attacks a turn)."+how(id, name), name, hp, 11+slot, n)
	case "animate-objects":
		budget := mod // Medium or smaller counts as one object, Large as two, Huge as three
		if budget < 1 {
			budget = 1
		}
		size, counts, hp, dice := animatedObjectSize(description, slot, mod)
		count := budget / counts
		if count < 1 {
			size, counts, hp, dice = animatedObjectSize("", slot, mod)
			count = budget
		}
		var parts []string
		for i := 1; i <= count; i++ {
			name := fmt.Sprintf("%s's Animated Object %d", owner, i)
			id := insert(companion{Kind: "summon", Name: name, Form: "animated-object", CreatureType: "construct", MaxHP: hp, AC: 15, Speed: "30 ft.",
				Attacks: []compAttack{{Name: "Slam", Bonus: atk, Dice: dice, Type: "Force", Count: 1}}, Concentration: "Animate Objects",
				Notes: size + " object"})
			parts = append(parts, fmt.Sprintf("%s (id %d)", name, id))
		}
		return fmt.Sprintf(" %d %s objects animate (HP %d, AC 15, Slam %s each; your spellcasting modifier allows %d object(s), a Large counting as two and a Huge as three): %s. Command each with POST /api/action {\"companion_id\": ..., \"action\": \"attack\", \"target_id\": ...}.", count, size, hp, dice, budget, strings.Join(parts, ", "))
	}
	return ""
}

// addOwnInitiativeEntry: with AGENTRPG_FAMILIAR_INITIATIVE=own, a familiar joining a fight gets its own Initiative entry.
func addOwnInitiativeEntry(lobbyID, companionID int) {
	if familiarInitiativeMode() != "own" || lobbyID == 0 {
		return
	}
	c, ok := loadCompanion(companionID)
	if !ok || c.Kind != "familiar" || c.Status != "active" {
		return
	}
	var raw []byte
	var active bool
	if db.QueryRow(`SELECT turn_order, active FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&raw, &active) != nil || !active {
		return
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	for _, e := range entries {
		if asInt(e["id"]) == c.CombatID {
			return
		}
	}
	var dex int
	db.QueryRow(`SELECT COALESCE(dex,10) FROM monsters WHERE slug = $1`, c.Form).Scan(&dex)
	init := game.RollInitiative(game.Modifier(dex), 0)
	entry := map[string]interface{}{"id": c.CombatID, "name": c.Name, "initiative": init, "dex_score": dex, "is_companion": true, "owner_id": c.OwnerID}
	// insert by initiative without moving the current turn
	var idx, cur int
	db.QueryRow(`SELECT current_turn_index FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&cur)
	idx = len(entries)
	for i, e := range entries {
		if asInt(e["initiative"]) < init {
			idx = i
			break
		}
	}
	entries = append(entries[:idx], append([]map[string]interface{}{entry}, entries[idx:]...)...)
	if idx <= cur && len(entries) > 1 {
		cur++
	}
	b, _ := json.Marshal(entries)
	db.Exec(`UPDATE combat_state SET turn_order = $1, current_turn_index = $2 WHERE lobby_id = $3`, b, cur, lobbyID)
}

// familiarTurnEntries: at combat start, with AGENTRPG_FAMILIAR_INITIATIVE=own, the active familiars of the campaign's characters roll
// Initiative (combat_handlers.go adds them to the turn order).
func familiarTurnEntries(lobbyID int) []map[string]interface{} {
	ensureCompanions()
	// a new fight: rounds count from 1 again, so no companion has acted or used its Reaction yet (F50: the Reaction stayed used)
	db.Exec(`UPDATE companions SET last_acted_round = 0 WHERE last_acted_lobby = $1`, lobbyID)
	db.Exec(`UPDATE companions SET reaction_round = 0 WHERE reaction_lobby = $1`, lobbyID)
	if familiarInitiativeMode() != "own" {
		return nil
	}
	var out []map[string]interface{}
	for _, c := range campaignCompanions(lobbyID) {
		if c.Kind != "familiar" || c.Status != "active" {
			continue
		}
		var dex int
		db.QueryRow(`SELECT COALESCE(dex,10) FROM monsters WHERE slug = $1`, c.Form).Scan(&dex)
		out = append(out, map[string]interface{}{"id": c.CombatID, "name": c.Name, "initiative": game.RollInitiative(game.Modifier(dex), 0),
			"dex_score": dex, "is_companion": true, "owner_id": c.OwnerID})
	}
	return out
}

// companionGone: a companion's combat entry counts as down when it is gone or dismissed (combatDeadFn).
func companionGone(combatID int) bool {
	c, ok := loadCompanion(combatID - companionIDBase)
	return !ok || c.Status != "active" || c.HP <= 0
}

// companionTurnOwner: the owner whose player takes the turn now, when the current turn entry is a companion's (own-Initiative familiars).
func companionTurnOwner(combatID int) int {
	c, ok := loadCompanion(combatID - companionIDBase)
	if !ok {
		return 0
	}
	return c.OwnerID
}

// companionView is what the owner and the GM see.
func companionView(c companion) map[string]interface{} {
	v := map[string]interface{}{"id": c.ID, "name": companionDisplayName(c), "nickname": c.Nickname, "kind": c.Kind, "status": c.Status, "hp": c.HP, "max_hp": c.MaxHP, "ac": c.AC,
		"speed": c.Speed, "creature_type": c.CreatureType, "form": c.Form, "attacks": c.Attacks, "combat_id": c.CombatID}
	if c.Notes != "" {
		v["notes"] = c.Notes
	}
	if len(c.Traits) > 0 {
		v["traits"] = c.Traits
	}
	if c.Concentration != "" {
		v["ends_when"] = "your Concentration on " + c.Concentration + " ends, or it drops to 0 Hit Points"
	}
	if c.Kind == "familiar" {
		v["commands"] = "dismiss_familiar (to its pocket dimension), recall_familiar, dismiss_familiar_forever: POST /api/action {\"action\": ..., \"companion_id\": " + fmt.Sprint(c.ID) + "} (a Magic action in combat)"
	}
	return v
}

// combatRoundOf: the campaign's combat round and whose turn it is (0, 0 when there's no fight).
func combatRoundOf(lobbyID int) (round, currentID int, active bool) {
	var raw []byte
	var idx int
	if db.QueryRow(`SELECT COALESCE(round_number,0), current_turn_index, turn_order, active FROM combat_state WHERE lobby_id = $1`, lobbyID).Scan(&round, &idx, &raw, &active) != nil || !active {
		return 0, 0, false
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	if idx >= 0 && idx < len(entries) {
		currentID = asInt(entries[idx]["id"])
	}
	return
}

var companionActions = map[string]bool{"attack": true, "dodge": true, "dash": true, "disengage": true, "help": true, "hide": true, "search": true, "move": true}

// handleCompanionAction runs POST /api/action for a companion ("companion_id") or a familiar command of its owner.
func handleCompanionAction(w http.ResponseWriter, charID, lobbyID, companionID int, action, description string, targetID int, targetIDs ...int) {
	ensureCompanions()
	reply := func(code int, m map[string]interface{}) { writeJSONStatus(w, code, m) }
	var c companion
	ok := false
	if companionID > 0 {
		c, ok = loadCompanion(companionID)
	} else if strings.Contains(action, "familiar") {
		var id int
		db.QueryRow(`SELECT id FROM companions WHERE owner_id = $1 AND kind = 'familiar' AND status <> 'gone' ORDER BY id DESC LIMIT 1`, charID).Scan(&id)
		c, ok = loadCompanion(id)
	}
	if !ok || c.OwnerID != charID {
		reply(400, map[string]interface{}{"success": false, "error": "companion_not_found", "message": "That isn't one of your companions. GET /api/my-turn lists them (companions)."})
		return
	}
	syncCompanionEndings(charID)
	c, _ = loadCompanion(c.ID)
	round, current, inCombat := combatRoundOf(lobbyID)
	var ownerName string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&ownerName)

	// the owner's familiar commands: a Magic action in combat
	if action == "dismiss_familiar" || action == "recall_familiar" || action == "dismiss_familiar_forever" {
		if c.Kind != "familiar" {
			reply(400, map[string]interface{}{"success": false, "error": "not_a_familiar", "message": "Only a familiar can be dismissed and recalled."})
			return
		}
		if inCombat {
			var used bool
			db.QueryRow(`SELECT COALESCE(action_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
			if current != charID {
				reply(400, map[string]interface{}{"success": false, "error": "not_your_turn", "message": "Dismissing or recalling a familiar is a Magic action on your turn."})
				return
			}
			if used {
				reply(400, map[string]interface{}{"success": false, "error": "action_used", "message": "You have already taken your action this turn (dismissing or recalling a familiar is a Magic action)."})
				return
			}
		}
		var text string
		switch action {
		case "dismiss_familiar":
			if c.Status != "active" {
				reply(400, map[string]interface{}{"success": false, "error": "not_present", "message": c.Name + " is already in its pocket dimension."})
				return
			}
			db.Exec(`UPDATE companions SET status = 'dismissed' WHERE id = $1`, c.ID)
			text = fmt.Sprintf("%s dismisses %s to a pocket dimension. Recall it with recall_familiar (a Magic action); no new casting is needed.", ownerName, c.Name)
		case "recall_familiar":
			if c.Status != "dismissed" {
				reply(400, map[string]interface{}{"success": false, "error": "not_dismissed", "message": c.Name + " isn't in its pocket dimension."})
				return
			}
			db.Exec(`UPDATE companions SET status = 'active', lobby_id = $1 WHERE id = $2`, lobbyID, c.ID)
			addOwnInitiativeEntry(lobbyID, c.ID)
			text = fmt.Sprintf("%s calls %s back from its pocket dimension; it appears within 30 feet.", ownerName, c.Name)
		case "dismiss_familiar_forever":
			endCompanion(c.ID, "dismissed forever")
			text = fmt.Sprintf("%s dismisses %s forever.", ownerName, c.Name)
		}
		if inCombat {
			db.Exec(`UPDATE characters SET action_used = true WHERE id = $1`, charID)
		}
		logAction(lobbyID, charID, 0, "companion", text, "")
		reply(200, map[string]interface{}{"success": true, "result": text, "companion": companionView(mustCompanion(c.ID))})
		return
	}

	if action == "breath" || action == "breath_weapon" || action == "fey_step" || action == "fell_glare" || action == "healing_touch" {
		if c.Status != "active" || c.HP <= 0 {
			reply(400, map[string]interface{}{"success": false, "error": "companion_not_present", "message": c.Name + " isn't here (" + c.Status + ")."})
			return
		}
		if inCombat && current != charID {
			reply(400, map[string]interface{}{"success": false, "error": "not_its_turn", "message": c.Name + " acts on your turn."})
			return
		}
		if (action == "breath" || action == "breath_weapon") && c.Form == "draconic-spirit" && !breathAvailable(c, lobbyID) {
			reply(400, map[string]interface{}{"success": false, "error": "companion_already_acted", "message": c.Name + " has already used its Breath Weapon this round (once, with its Multiattack)."})
			return
		}
		text, why := companionSpecialAction(c, charID, lobbyID, action, description, targetID, targetIDs)
		if why != "" {
			reply(400, map[string]interface{}{"success": false, "error": "companion_action_refused", "message": why})
			return
		}
		logAction(lobbyID, charID, 0, "companion", text, "")
		reply(200, map[string]interface{}{"success": true, "result": text, "companion": companionView(mustCompanion(c.ID))})
		return
	}
	if !companionActions[action] {
		reply(400, map[string]interface{}{"success": false, "error": "invalid_companion_action", "message": "A companion can attack, dodge, dash, disengage, help, hide, search or move; a Draconic Spirit also \"breath\"; an Otherworldly Steed \"fey_step\", \"fell_glare\" or \"healing_touch\" by its kind."})
		return
	}
	if c.Status != "active" || c.HP <= 0 {
		reply(400, map[string]interface{}{"success": false, "error": "companion_not_present", "message": c.Name + " isn't here (" + c.Status + ")."})
		return
	}
	if action == "attack" && c.Kind == "familiar" {
		familiarAttack(w, c, charID, lobbyID, round, current, inCombat, description, targetID, ownerName)
		return
	}
	if inCombat {
		ownTurn := familiarInitiativeMode() == "own" && c.Kind == "familiar"
		if (ownTurn && current != c.CombatID) || (!ownTurn && current != charID) {
			msg := fmt.Sprintf("%s acts on your turn.", c.Name)
			if ownTurn {
				msg = fmt.Sprintf("%s acts on its own Initiative (server setting AGENTRPG_FAMILIAR_INITIATIVE=own); wait for its turn.", c.Name)
			}
			reply(400, map[string]interface{}{"success": false, "error": "not_its_turn", "message": msg})
			return
		}
		if c.lastLobby == lobbyID && c.lastRound == round {
			reply(400, map[string]interface{}{"success": false, "error": "companion_already_acted", "message": c.Name + " has already acted this round."})
			return
		}
	}
	var text string
	result := map[string]interface{}{}
	switch action {
	case "attack":
		if len(c.Attacks) == 0 {
			reply(400, map[string]interface{}{"success": false, "error": "cannot_attack", "message": c.Name + " has no attacks."})
			return
		}
		if okT, why := validateAttackTarget(lobbyID, targetID); !okT {
			reply(400, map[string]interface{}{"success": false, "error": "invalid_target", "message": why})
			return
		}
		var lines []string
		for _, a := range c.Attacks {
			for i := 0; i < maxInt(1, a.Count); i++ {
				lines = append(lines, companionAttack(lobbyID, c, a, targetID))
			}
		}
		text = fmt.Sprintf("%s attacks: %s", c.Name, strings.Join(lines, " "))
	case "help":
		if targetID < 0 {
			addCombatEffect(lobbyID, targetID, c.CombatID, "assisted")
			text = fmt.Sprintf("%s takes the Help action: the next attack roll an ally makes against that enemy before %s's next turn has Advantage.", c.Name, c.Name)
		} else {
			text = fmt.Sprintf("%s takes the Help action. %s", c.Name, description)
		}
	default:
		text = fmt.Sprintf("%s takes the %s action. %s", companionDisplayName(c), titleCase(action), description)
		if (action == "move" || action == "dash") && companionHasTrait(c, "Flyby") {
			text += " (Flyby: it doesn't provoke Opportunity Attacks when it flies out of an enemy's reach.)"
		}
	}
	if inCombat {
		db.Exec(`UPDATE companions SET last_acted_round = $1, last_acted_lobby = $2 WHERE id = $3`, round, lobbyID, c.ID)
	}
	logAction(lobbyID, charID, 0, "companion", strings.TrimSpace(text), "")
	result["success"] = true
	result["result"] = strings.TrimSpace(text)
	result["companion"] = companionView(mustCompanion(c.ID))
	reply(200, result)
}

// familiarAttack: a familiar attacks only through Pact of the Chain: with its Reaction when its owner forgoes one attack of the Attack
// action, or (Investment of the Chain Master) taking the Attack action when its owner spends a Bonus Action on Quick Attack.
func familiarAttack(w http.ResponseWriter, c companion, charID, lobbyID, round, current int, inCombat bool, description string, targetID int, ownerName string) {
	reply := func(code int, m map[string]interface{}) { writeJSONStatus(w, code, m) }
	if !hasPactBoon(charID, "chain") {
		reply(400, map[string]interface{}{"success": false, "error": "cannot_attack", "message": "Find Familiar: \"A familiar can't attack, but it can take other actions as normal.\" It can deliver your touch spells with its Reaction: cast the spell with \"delivered by my familiar\" in the description. A Warlock with Pact of the Chain can have it attack."})
		return
	}
	if len(c.Attacks) == 0 {
		reply(400, map[string]interface{}{"success": false, "error": "cannot_attack", "message": c.Name + "'s form has no attack."})
		return
	}
	if !inCombat || current != charID {
		reply(400, map[string]interface{}{"success": false, "error": "not_your_turn", "message": "Your familiar attacks on your turn, when you take the Attack action (Pact of the Chain)."})
		return
	}
	low := strings.ToLower(description)
	quick := hasInvocation(charID, "investment-of-the-chain-master") && strings.Contains(low, "quick")
	var how string
	if quick {
		var bonusUsed bool
		db.QueryRow(`SELECT COALESCE(bonus_action_used,false) FROM characters WHERE id = $1`, charID).Scan(&bonusUsed)
		if bonusUsed {
			reply(400, map[string]interface{}{"success": false, "error": "bonus_action_used", "message": "Quick Attack takes your Bonus Action, and you have used it this turn."})
			return
		}
		db.Exec(`UPDATE characters SET bonus_action_used = true WHERE id = $1`, charID)
		how = "Quick Attack (your Bonus Action): it takes the Attack action"
	} else {
		var reactRound, reactLobby int
		db.QueryRow(`SELECT COALESCE(reaction_round,0), COALESCE(reaction_lobby,0) FROM companions WHERE id = $1`, c.ID).Scan(&reactRound, &reactLobby)
		if reactRound == round && reactLobby == lobbyID {
			reply(400, map[string]interface{}{"success": false, "error": "reaction_used", "message": c.Name + " has already used its Reaction this round."})
			return
		}
		var actionUsed bool
		var remaining sql.NullInt32
		db.QueryRow(`SELECT COALESCE(action_used,false), attacks_remaining FROM characters WHERE id = $1`, charID).Scan(&actionUsed, &remaining)
		if actionUsed && !(remaining.Valid && remaining.Int32 > 0) {
			reply(400, map[string]interface{}{"success": false, "error": "no_attack_to_forgo", "message": "Your familiar attacks when you forgo one of the attacks of your Attack action, and you have none left this turn."})
			return
		}
		consumeAttackAction(charID) // one of the owner's attacks is given up
		db.Exec(`UPDATE companions SET reaction_round = $1, reaction_lobby = $2 WHERE id = $3`, round, lobbyID, c.ID)
		how = fmt.Sprintf("%s forgoes one attack; the familiar attacks with its Reaction (Pact of the Chain)", ownerName)
	}
	if okT, why := validateAttackTarget(lobbyID, targetID); !okT {
		reply(400, map[string]interface{}{"success": false, "error": "invalid_target", "message": why})
		return
	}
	a := c.Attacks[0]
	if hasInvocation(charID, "investment-of-the-chain-master") && (strings.Contains(low, "radiant") || strings.Contains(low, "necrotic")) &&
		(strings.EqualFold(a.Type, "bludgeoning") || strings.EqualFold(a.Type, "piercing") || strings.EqualFold(a.Type, "slashing")) {
		if strings.Contains(low, "radiant") {
			a.Type = "Radiant"
		} else {
			a.Type = "Necrotic"
		}
	}
	text := fmt.Sprintf("%s: %s. %s", c.Name, how, companionAttack(lobbyID, c, a, targetID))
	logAction(lobbyID, charID, 0, "companion", text, "")
	reply(200, map[string]interface{}{"success": true, "result": text, "companion": companionView(mustCompanion(c.ID))})
}

// familiarTouchPending: a touch spell delivered by the familiar, settled after the cast (its Reaction is spent only when the cast works).
var familiarTouchPending sync.Map // character id -> companion id

// familiarTouchCheck: a cast "delivered by my familiar" needs a touch spell and a familiar that is here and has its Reaction.
func familiarTouchCheck(charID, lobbyID int, spellSlug, description string) string {
	low := strings.ToLower(description)
	if !strings.Contains(low, "familiar") || !(strings.Contains(low, "deliver") || strings.Contains(low, "via") || strings.Contains(low, "through")) {
		return ""
	}
	if sp, ok := srdSpellsMemory[spellSlug]; !ok || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(sp.Range)), "touch") {
		return "Only a spell with a range of Touch can be delivered by your familiar."
	}
	ensureCompanions()
	var id int
	db.QueryRow(`SELECT id FROM companions WHERE owner_id = $1 AND kind = 'familiar' AND status = 'active' ORDER BY id DESC LIMIT 1`, charID).Scan(&id)
	if id == 0 {
		return "You have no familiar here to deliver the spell (it must be within 100 feet; recall it from its pocket dimension first)."
	}
	if round, _, inCombat := combatRoundOf(lobbyID); inCombat {
		var rr, rl int
		db.QueryRow(`SELECT COALESCE(reaction_round,0), COALESCE(reaction_lobby,0) FROM companions WHERE id = $1`, id).Scan(&rr, &rl)
		if rr == round && rl == lobbyID {
			return "Your familiar has already used its Reaction this round."
		}
	}
	familiarTouchPending.Store(charID, id)
	return ""
}

// familiarTouchSettle spends the familiar's Reaction after a successful cast and returns the note for the result.
func familiarTouchSettle(charID, lobbyID int, castWorked bool) string {
	v, ok := familiarTouchPending.LoadAndDelete(charID)
	if !ok || !castWorked {
		return ""
	}
	c, found := loadCompanion(v.(int))
	if !found {
		return ""
	}
	if round, _, inCombat := combatRoundOf(lobbyID); inCombat {
		db.Exec(`UPDATE companions SET reaction_round = $1, reaction_lobby = $2 WHERE id = $3`, round, lobbyID, c.ID)
	}
	return fmt.Sprintf(" %s delivers the touch with its Reaction.", c.Name)
}

// companionDisplayName: the owner's name for it when it has one ("Archimedes, Aldric's Owl"), else its own.
func companionDisplayName(c companion) string {
	if strings.TrimSpace(c.Nickname) != "" {
		return c.Nickname + " (" + c.Name + ")"
	}
	return c.Name
}

func companionHasTrait(c companion, name string) bool {
	for _, t := range c.Traits {
		if strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

// handleCompanionName godoc
// @Summary Name your companion
// @Description The owner gives a familiar, steed, summon or pet a name (up to 40 characters; an empty name clears it). The campaign page and the feed show it, with what the creature is: "Archimedes (Aldric's Owl)".
// @Tags Characters
// @Param request body object{companion_id=int,name=string} true "The companion and its name"
// @Router /companions/name [post]
func handleCompanionName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var req struct {
		CompanionID int    `json:"companion_id"`
		Name        string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	ensureCompanions()
	c, ok := loadCompanion(req.CompanionID)
	var owner int
	if ok {
		db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, c.OwnerID).Scan(&owner)
	}
	if !ok || owner != agentID {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "companion_not_found", "message": "Send the companion_id of one of your companions (GET /api/my-turn lists them)."})
		return
	}
	name := strings.TrimSpace(req.Name)
	if len([]rune(name)) > 40 || strings.ContainsAny(name, "\r\n\t<>") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_name", "message": "A name has at most 40 characters, on one line, without < or >."})
		return
	}
	db.Exec(`UPDATE companions SET nickname = $1 WHERE id = $2`, name, c.ID)
	c.Nickname = name
	if name != "" {
		logAction(c.LobbyID, c.OwnerID, 0, "companion", fmt.Sprintf("%s is now called %s.", c.Name, name), "")
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "companion": companionView(c)})
}

// companionBoxesHTML: the campaign page's row of companion boxes (name, what it is, owner, HP or where it is).
func companionBoxesHTML(lobbyID int) string {
	comps := campaignCompanions(lobbyID)
	if len(comps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="party-boxes-row companion-row"><span class="box-label companion-row-label">Companions</span>`)
	for _, c := range comps {
		var owner string
		db.QueryRow(`SELECT name FROM characters WHERE id = $1`, c.OwnerID).Scan(&owner)
		what := c.Kind
		if c.Kind == "familiar" || c.Kind == "pet" {
			var mname string
			db.QueryRow(`SELECT name FROM monsters WHERE slug = $1`, c.Form).Scan(&mname)
			if mname != "" {
				what = mname + " " + c.Kind
			}
		} else if c.Kind == "steed" {
			what = "Otherworldly Steed"
		} else if c.Kind == "summon" {
			what = titleCase(strings.ReplaceAll(c.Form, "-", " ")) + " (summon)"
		}
		if c.CreatureType != "" && c.Kind != "summon" {
			what += ", " + titleCase(c.CreatureType)
		}
		title := c.Name
		if c.Nickname != "" {
			title = c.Nickname
		}
		status := fmt.Sprintf(`<p class="%s">HP: %d/%d</p>`, healthClass(c.HP, c.MaxHP), c.HP, c.MaxHP)
		cls := "party-box companion-box"
		if c.Status == "dismissed" {
			status = `<p class="class-info">in its pocket dimension</p>`
			cls += " inactive"
		}
		b.WriteString(fmt.Sprintf(`<div class="%s" title="%s"><h4>%s</h4><p class="class-info">%s</p><p class="class-info">of %s</p>%s</div>`,
			cls, html.EscapeString(strings.Join(traitNames(c), ", ")), html.EscapeString(title), html.EscapeString(what), html.EscapeString(owner), status))
	}
	b.WriteString(`</div>`)
	return b.String()
}

func traitNames(c companion) []string {
	var out []string
	for _, t := range c.Traits {
		out = append(out, t.Name+": "+t.Text)
	}
	return out
}

func healthClass(hp, max int) string {
	switch {
	case max > 0 && hp < max/4:
		return "critical"
	case max > 0 && hp < max/2:
		return "wounded"
	}
	return "healthy"
}

// companionOpportunityAttack: the GM's opportunity-attack command aimed at a companion (its combat id or its companion id).
func companionOpportunityAttack(w http.ResponseWriter, targetID int, flewOut bool) bool {
	id := targetID
	if isCompanionCombatID(targetID) {
		id = targetID - companionIDBase
	} else {
		return false
	}
	c, ok := loadCompanion(id)
	if !ok {
		return false
	}
	if companionHasTrait(c, "Flyby") {
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": false, "error": "no_opportunity_attack", "result": companionDisplayName(c) + " has Flyby: it doesn't provoke an Opportunity Attack when it flies out of an enemy's reach."})
		return true
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "result": fmt.Sprintf("%s provokes an Opportunity Attack (AC %d). Roll the monster's attack and apply a hit with POST /api/gm/companion {\"companion_id\": %d, \"damage\": N}.", companionDisplayName(c), c.AC, c.ID)})
	return true
}

func mustCompanion(id int) companion {
	c, _ := loadCompanion(id)
	return c
}

// companionAttack rolls one attack against a monster and applies the damage.
func companionAttack(lobbyID int, c companion, a compAttack, targetID int) string {
	var ac, hp int
	var key, name string
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == targetID {
			ac, hp = asInt(e["ac"]), asInt(e["hp"])
			key, name = fmt.Sprint(e["monster_key"]), fmt.Sprint(e["name"])
		}
	}
	if hp <= 0 {
		return fmt.Sprintf("%s: the target is already down.", a.Name)
	}
	ac += spellACAdjust(lobbyID, targetID)
	adv := takeCombatEffect(lobbyID, targetID, "assisted", c.CombatID)
	d20 := game.RollDie(20)
	if adv {
		if r := game.RollDie(20); r > d20 {
			d20 = r
		}
	}
	total := d20 + a.Bonus
	crit := d20 == 20
	if d20 == 1 || (!crit && total < ac) {
		return fmt.Sprintf("%s: %d to hit (d20 %d + %d)%s. MISS.", a.Name, total, d20, a.Bonus, map[bool]string{true: " with Advantage (Help)", false: ""}[adv])
	}
	dmg := game.RollDamage(a.Dice, crit)
	dmg, note := adjustMonsterDamage(key, strings.ToLower(a.Type), dmg)
	extra := ""
	if a.Extra != "" {
		x := game.RollDamage(a.Extra, crit)
		x, n2 := adjustMonsterDamage(key, strings.ToLower(a.ExtraType), x)
		dmg += x
		extra = fmt.Sprintf(" plus %d %s%s", x, a.ExtraType, n2)
	}
	newHP := hp - dmg
	if newHP < 0 {
		newHP = 0
	}
	setMonsterHP(lobbyID, targetID, newHP)
	wakeText := ""
	if dmg > 0 {
		wakeText = wakeOnDamage(lobbyID, targetID, monsterName(lobbyID, targetID)) // Sleep, Suggestion, Dominate's repeat save (F221)
	}
	critText := ""
	if crit {
		critText = " CRITICAL HIT!"
	}
	return fmt.Sprintf("%s: %d to hit. HIT.%s %d %s damage%s%s to %s (%s).%s", a.Name, total, critText, dmg, a.Type, note, extra, name, healthWord(newHP, maxHPOf(lobbyID, targetID)), wakeText)
}

func maxHPOf(lobbyID, id int) int {
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			return asInt(e["max_hp"])
		}
	}
	return 0
}

// handleGMCompanion godoc
// @Summary Companions: pets, damage, healing (GM)
// @Description Familiars: they deliver their owner's touch spells with their Reaction (cast with "delivered by my familiar"); with Pact of the Chain a familiar attacks when its owner forgoes one attack of the Attack action, or with Quick Attack (Investment of the Chain Master, a Bonus Action). GET ?campaign_id=N lists the campaign's companions (familiars, steeds, summons, pets) with their numbers. POST (GM): give a character a pet from the monster list {"character_id", "monster_key": "mastiff", "name": "Brutus"}; or change a companion {"companion_id", "damage": 7, "damage_type": "slashing"} (at 0 Hit Points it is gone: a familiar comes back when Find Familiar is cast again), {"companion_id", "heal": 5}, {"companion_id", "remove": true}. Players command their companions with POST /api/action {"companion_id": N, "action": "attack", "target_id": -2}; a familiar is dismissed and recalled with the actions dismiss_familiar and recall_familiar. Rule deviation (server setting AGENTRPG_FAMILIAR_INITIATIVE): companions act on their owner's turn; "own" gives a familiar its own Initiative (the SRD rule).
// @Tags GM
// @Param campaign_id query int false "Campaign (GET)"
// @Param request body object{character_id=int,monster_key=string,name=string,companion_id=int,damage=int,damage_type=string,heal=int,remove=bool} false "Pet or change (POST)"
// @Router /gm/companion [get]
// @Router /gm/companion [post]
func handleGMCompanion(w http.ResponseWriter, r *http.Request) {
	ensureCompanions()
	_, lobbyID, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		var list []map[string]interface{}
		for _, c := range campaignCompanions(lobbyID) {
			v := companionView(c)
			v["owner_id"] = c.OwnerID
			list = append(list, v)
		}
		writeJSONStatus(w, 200, map[string]interface{}{"campaign_id": lobbyID, "companions": list, "familiar_initiative": familiarInitiativeMode()})
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		MonsterKey  string `json:"monster_key"`
		Name        string `json:"name"`
		CompanionID int    `json:"companion_id"`
		Damage      int    `json:"damage"`
		DamageType  string `json:"damage_type"`
		Heal        int    `json:"heal"`
		Remove      bool   `json:"remove"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.CompanionID == 0 {
		var charLobby int
		if db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, req.CharacterID).Scan(&charLobby) != nil || charLobby != lobbyID {
			writeJSONStatus(w, 400, map[string]interface{}{"error": "character_required", "message": "Send the character_id of a character in this campaign, or a companion_id."})
			return
		}
		var mname, speed string
		var hp, ac int
		var actions []byte
		if db.QueryRow(`SELECT name, hp, ac, COALESCE(speed_text,''), COALESCE(actions,'[]') FROM monsters WHERE slug = $1`, req.MonsterKey).Scan(&mname, &hp, &ac, &speed, &actions) != nil {
			writeJSONStatus(w, 400, map[string]interface{}{"error": "monster_not_found", "message": "monster_key must be a slug from GET /api/universe/monsters."})
			return
		}
		var acts []map[string]interface{}
		json.Unmarshal(actions, &acts)
		var attacks []compAttack
		for _, a := range acts {
			if b := asInt(a["attack_bonus"]); b > 0 && fmt.Sprint(a["damage_dice"]) != "" {
				attacks = append(attacks, compAttack{Name: fmt.Sprint(a["name"]), Bonus: b, Dice: fmt.Sprint(a["damage_dice"]), Type: fmt.Sprint(a["damage_type"]), Count: 1})
				break
			}
		}
		name := req.Name
		if name == "" {
			name = mname
		}
		att, _ := json.Marshal(attacks)
		var id int
		db.QueryRow(`INSERT INTO companions (owner_id, lobby_id, kind, name, source, form, creature_type, hp, max_hp, ac, speed, attacks)
			VALUES ($1,$2,'pet',$3,'gm',$4,'beast',$5,$5,$6,$7,$8) RETURNING id`, req.CharacterID, lobbyID, name, req.MonsterKey, hp, ac, speed, string(att)).Scan(&id)
		db.Exec(`UPDATE companions SET traits = (SELECT COALESCE(traits,'[]') FROM monsters WHERE slug = $1) WHERE id = $2`, req.MonsterKey, id)
		recordGMAction(db, lobbyID, "companion", fmt.Sprintf("The GM gives a pet: %s (%s)", name, mname), "")
		writeJSONStatus(w, 200, map[string]interface{}{"success": true, "companion": companionView(mustCompanion(id))})
		return
	}
	c, found := loadCompanion(req.CompanionID)
	var owner int
	if found {
		db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, c.OwnerID).Scan(&owner)
	}
	if !found || owner != lobbyID {
		writeJSONStatus(w, 400, map[string]interface{}{"error": "companion_not_found"})
		return
	}
	text := ""
	switch {
	case req.Remove:
		endCompanion(c.ID, "removed by the GM")
		text = c.Name + " is gone (the GM)."
	case req.Damage > 0:
		text = damageCompanion(c, req.Damage, req.DamageType)
	case req.Heal > 0:
		hp := c.HP + req.Heal
		if hp > c.MaxHP {
			hp = c.MaxHP
		}
		db.Exec(`UPDATE companions SET hp = $1 WHERE id = $2`, hp, c.ID)
		text = fmt.Sprintf("%s regains %d Hit Points.", c.Name, hp-c.HP)
	default:
		writeJSONStatus(w, 400, map[string]interface{}{"error": "nothing_to_do", "message": "Send damage, heal or remove."})
		return
	}
	recordGMAction(db, lobbyID, "companion", text, "")
	writeJSONStatus(w, 200, map[string]interface{}{"success": true, "result": text, "companion": companionView(mustCompanion(c.ID))})
}
