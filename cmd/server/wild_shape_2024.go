package main

// Wild Shape (Druid level 2, SRD 5.2.1 "Rules While Shape-Shifted"):
//   Known forms: 4 (CR 1/4, no Fly Speed) at level 2, 6 (CR 1/2) at 4, 8 (CR 1, Fly Speed allowed) at 8; after each Long Rest one known form
//     can be replaced. POST /api/characters/{id}/class-options {"wild_shape_forms": ["wolf", ...]} sets them; a form must be known to use it.
//   Hit Points: the druid keeps its own Hit Points and Hit Point Dice and gains Temporary Hit Points equal to its Druid level (actions.go).
//     There is no separate beast Hit Point pool in the 2024 rules: damage takes the Temporary Hit Points, then the druid's Hit Points.
//   Game statistics: the beast's stat block replaces the druid's (Strength, Dexterity, Constitution, AC, attacks); the druid keeps its
//     Intelligence, Wisdom, Charisma, Hit Points, class features, languages, feats and proficiencies. The druid's own scores and AC are
//     kept in wild_shape_saved and put back when the form ends (Hit Points never change with the Constitution swap).
//   Equipment: it merges into the form (default; no effect), is worn ("equipment: worn" in the description: worn rings and cloaks keep
//     working, as the GM allows), or falls in the druid's space ("equipment: fall").
//   Attacks: the beast's attacks from its stat block (Multiattack included); a Primal Strike druid adds 1d8 (2d8 from level 15) Cold, Fire,
//     Lightning or Thunder once per turn.
//   Skills and saves: the druid keeps its skill and saving throw proficiencies; when the stat block's modifier for a skill or save is
//     higher, that one is used (wildShapeHigher: checkBonus, characterSave, saveBonusFor and the GM check and save routes).
//   The form ends after half the Druid level in hours (wild_shape_rounds_left, 600 rounds an hour: counted down at the end of the druid's
//     turns in combat and by passSpellTime outside it), when Wild Shape is used again, with the Incapacitated condition (which 0 Hit Points
//     bring: Unconscious), when the druid dies, or early as a Bonus Action (revert_wild_shape).

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var wildShapeColsOnce sync.Once

func ensureWildShapeColumns() {
	wildShapeColsOnce.Do(func() {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS wild_shape_saved JSONB`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS wild_shape_known JSONB DEFAULT '[]'`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS wild_shape_swap_ready BOOLEAN DEFAULT FALSE`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS wild_shape_equipment TEXT DEFAULT ''`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS wild_shape_rounds_left INT`)
	})
}

// wildShapeLimits: known forms, maximum CR and whether a Fly Speed is allowed (the Beast Shapes table).
func wildShapeLimits(level int) (known int, maxCR float64, fly bool) {
	switch {
	case level >= 8:
		return 8, 1, true
	case level >= 4:
		return 6, 0.5, false
	default:
		return 4, 0.25, false
	}
}

func crValue(cr string) float64 {
	switch cr {
	case "1/8":
		return 0.125
	case "1/4":
		return 0.25
	case "1/2":
		return 0.5
	}
	f, _ := strconv.ParseFloat(cr, 64)
	return f
}

func knownWildShapes(charID int) []string {
	ensureWildShapeColumns()
	var raw []byte
	db.QueryRow(`SELECT COALESCE(wild_shape_known,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var out []string
	json.Unmarshal(raw, &out)
	return out
}

// setWildShapeForms checks and stores a druid's known forms (class-options "wild_shape_forms"); "" when it worked.
func setWildShapeForms(charID int, forms []string) string {
	ensureWildShapeColumns()
	class, _, level := subclassOf(charID)
	if class != "druid" || level < 2 {
		return "Only a Druid of level 2 or higher knows Wild Shape forms."
	}
	known, maxCR, fly := wildShapeLimits(level)
	if len(forms) > known {
		return fmt.Sprintf("A level %d Druid knows %d Beast forms; this list has %d.", level, known, len(forms))
	}
	seen := map[string]bool{}
	var clean []string
	for _, f := range forms {
		slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(f), " ", "-"))
		if seen[slug] {
			continue
		}
		seen[slug] = true
		var name, mtype, cr, speed string
		if db.QueryRow(`SELECT name, type, cr, COALESCE(speed_text,'') FROM monsters WHERE slug = $1`, slug).Scan(&name, &mtype, &cr, &speed) != nil {
			return fmt.Sprintf("%q isn't in the monster list (GET /api/universe/monsters).", f)
		}
		if !strings.EqualFold(mtype, "beast") {
			return fmt.Sprintf("%s is a %s, not a Beast.", name, mtype)
		}
		if crValue(cr) > maxCR {
			return fmt.Sprintf("%s is CR %s; a level %d Druid's forms are CR %s or lower.", name, cr, level, strconv.FormatFloat(maxCR, 'g', -1, 64))
		}
		if !fly && strings.Contains(strings.ToLower(speed), "fly") {
			return fmt.Sprintf("%s has a Fly Speed; forms with one are allowed from Druid level 8.", name)
		}
		clean = append(clean, slug)
	}
	old := knownWildShapes(charID)
	removed := 0
	for _, o := range old {
		if !seen[o] {
			removed++
		}
	}
	var swap bool
	db.QueryRow(`SELECT COALESCE(wild_shape_swap_ready,false) FROM characters WHERE id = $1`, charID).Scan(&swap)
	if len(old) > 0 && removed > 0 && (!swap || removed > 1) {
		return "After a Long Rest you can replace one of your known forms; you can always add forms up to your number."
	}
	b, _ := json.Marshal(clean)
	db.Exec(`UPDATE characters SET wild_shape_known = $1, wild_shape_swap_ready = CASE WHEN $2 THEN false ELSE wild_shape_swap_ready END WHERE id = $3`,
		string(b), removed > 0, charID)
	return ""
}

// wildShapeRest: a Long Rest lets the druid replace one known form.
func wildShapeRest(charID int, long bool) {
	if long {
		ensureWildShapeColumns()
		db.Exec(`UPDATE characters SET wild_shape_swap_ready = true WHERE id = $1 AND class = 'druid'`, charID)
	}
}

// enterWildShapeStats swaps in the beast's Strength, Dexterity, Constitution and AC, keeping the druid's own to restore.
func enterWildShapeStats(charID int, slug, description string) string {
	ensureWildShapeColumns()
	var str, dex, con, ac int
	db.QueryRow(`SELECT str, dex, con, COALESCE(ac,10) FROM characters WHERE id = $1`, charID).Scan(&str, &dex, &con, &ac)
	saved, _ := json.Marshal(map[string]int{"str": str, "dex": dex, "con": con, "ac": ac})
	var bstr, bdex, bcon, bac int
	db.QueryRow(`SELECT str, dex, con, ac FROM monsters WHERE slug = $1`, slug).Scan(&bstr, &bdex, &bcon, &bac)
	low := strings.ToLower(description)
	equip, note := "merge", "Your equipment merges into the form (no effect while you're in it)."
	switch {
	case strings.Contains(low, "equipment: worn") || strings.Contains(low, "equipment worn") || strings.Contains(low, "wear"):
		equip = "worn"
		note = "Your form wears your rings and cloak (the GM decides what fits); worn magic items keep working."
		for _, it := range []string{"ring of protection", "cloak of protection"} {
			if _, ok := attunedTo(charID, it); ok {
				bac++
			}
		}
	case strings.Contains(low, "equipment: fall") || strings.Contains(low, "drop"):
		equip = "fall"
		note = "Your equipment falls in your space."
	}
	_, _, level := subclassOf(charID)
	hours := level / 2
	if hours < 1 {
		hours = 1
	}
	db.Exec(`UPDATE characters SET wild_shape_saved = $1, str = $2, dex = $3, con = $4, ac = $5, wild_shape_equipment = $6, wild_shape_rounds_left = $7 WHERE id = $8`,
		string(saved), bstr, bdex, bcon, bac, equip, hours*600, charID)
	return note + fmt.Sprintf(" The form lasts up to %d hour%s.", hours, map[bool]string{true: "", false: "s"}[hours == 1])
}

// wildShapeTick counts down the form's time by a number of rounds (600 rounds = 1 hour); when it runs out the form ends (posted to the
// feed). It returns the text, or "" when the form goes on.
func wildShapeTick(charID, rounds int) string {
	ensureWildShapeColumns()
	var left int
	if db.QueryRow(`UPDATE characters SET wild_shape_rounds_left = wild_shape_rounds_left - $2
		WHERE id = $1 AND COALESCE(wild_shape_form,'') <> '' AND wild_shape_rounds_left IS NOT NULL RETURNING wild_shape_rounds_left`,
		charID, rounds).Scan(&left) != nil || left > 0 {
		return ""
	}
	return leaveWildShape(charID, "its time, half the Druid level in hours, is up")
}

// wildShapeTickLobby: time passes outside combat (a rest, travel or the GM's pass-time) for every druid in the campaign.
func wildShapeTickLobby(lobbyID, rounds int) {
	ensureWildShapeColumns()
	rows, err := db.Query(`SELECT id FROM characters WHERE lobby_id = $1 AND COALESCE(wild_shape_form,'') <> ''`, lobbyID)
	if err != nil {
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		wildShapeTick(id, rounds)
	}
}

// wildShapeBeastMod: in Wild Shape, the stat block's modifier for a skill ("perception", "sleight_of_hand") or a save ("save:dex").
func wildShapeBeastMod(charID int, key string) (int, bool) {
	var form string
	if db == nil || db.QueryRow(`SELECT COALESCE(wild_shape_form,'') FROM characters WHERE id = $1`, charID).Scan(&form) != nil || form == "" {
		return 0, false
	}
	if ability, ok := strings.CutPrefix(key, "save:"); ok {
		var raw []byte
		db.QueryRow(`SELECT COALESCE(saves,'{}') FROM monsters WHERE slug = $1`, form).Scan(&raw)
		saves := map[string]int{}
		json.Unmarshal(raw, &saves)
		v, found := saves[ability]
		return v, found
	}
	var skills string
	db.QueryRow(`SELECT COALESCE(skills,'') FROM monsters WHERE slug = $1`, form).Scan(&skills)
	want := strings.ReplaceAll(strings.ToLower(key), "_", " ")
	for _, part := range strings.Split(skills, ",") { // "Perception +3, Stealth +4"
		part = strings.TrimSpace(part)
		i := strings.LastIndexAny(part, "+-")
		if i <= 0 || !strings.EqualFold(strings.TrimSpace(part[:i]), want) {
			continue
		}
		v, err := strconv.Atoi(part[i:])
		return v, err == nil
	}
	return 0, false
}

// wildShapeHigher: "If a skill or saving throw modifier in the Beast's stat block is higher than yours, use the one in the stat block."
func wildShapeHigher(charID int, key string, mod int) int {
	if v, ok := wildShapeBeastMod(charID, key); ok && v > mod {
		return v
	}
	return mod
}

// leaveWildShape ends the form: the druid's own scores and AC come back. reason "" means the druid chose to.
func leaveWildShape(charID int, reason string) string {
	ensureWildShapeColumns()
	var form, name string
	var saved []byte
	db.QueryRow(`SELECT COALESCE(wild_shape_form,''), COALESCE(wild_shape_saved,'null'), name FROM characters WHERE id = $1`, charID).Scan(&form, &saved, &name)
	if form == "" {
		return ""
	}
	var s map[string]int
	json.Unmarshal(saved, &s)
	if s != nil {
		db.Exec(`UPDATE characters SET str = $1, dex = $2, con = $3, ac = $4 WHERE id = $5`, s["str"], s["dex"], s["con"], s["ac"], charID)
	}
	db.Exec(`UPDATE characters SET wild_shape_form = NULL, wild_shape_saved = NULL, wild_shape_rounds_left = NULL WHERE id = $1`, charID)
	var beast string
	db.QueryRow(`SELECT name FROM monsters WHERE slug = $1`, form).Scan(&beast)
	text := fmt.Sprintf("%s leaves the %s form", name, beast)
	if reason != "" {
		text += " (" + reason + ")"
		logAction(lobbyOfChar(charID), charID, 0, "wild_shape", text+".", "")
	}
	return text + "."
}

// endWildShapeIfDown: the form ends when the druid dies or has the Incapacitated condition (0 Hit Points: Unconscious).
func endWildShapeIfDown(charID int) string {
	var form string
	var hp int
	var dead bool
	if db.QueryRow(`SELECT COALESCE(wild_shape_form,''), hp, COALESCE(is_dead,false) FROM characters WHERE id = $1`, charID).Scan(&form, &hp, &dead) != nil || form == "" {
		return ""
	}
	switch {
	case dead:
		return leaveWildShape(charID, "the druid died")
	case hp <= 0:
		return leaveWildShape(charID, "0 Hit Points: Unconscious, so Incapacitated")
	case isIncapacitated(charID):
		return leaveWildShape(charID, "Incapacitated")
	}
	return ""
}

var (
	multiattackCountRe = regexp.MustCompile(`(?i)makes (two|three|four) `)
	multiattackNamedRe = regexp.MustCompile(`(?i)(one|two|three|four) ([A-Za-z][A-Za-z' ]*?) attacks?\b`)
)

var numberWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4}

// beastAttackPlan: the attacks a beast makes with the Attack action: its Multiattack ("one Bite attack and one Claw attack", "two
// Bite attacks") or one attack. A named attack in the description is used alone when there is no Multiattack.
func beastAttackPlan(attacks []compAttack, acts []map[string]interface{}, low string) []compAttack {
	byName := func(n string) (compAttack, bool) {
		for _, a := range attacks {
			if strings.EqualFold(a.Name, strings.TrimSpace(n)) {
				return a, true
			}
		}
		return compAttack{}, false
	}
	for _, a := range acts {
		if !strings.EqualFold(fmt.Sprint(a["name"]), "Multiattack") {
			continue
		}
		desc := fmt.Sprint(a["desc"])
		var plan []compAttack
		for _, m := range multiattackNamedRe.FindAllStringSubmatch(desc, -1) {
			if at, ok := byName(m[2]); ok {
				for i := 0; i < numberWords[strings.ToLower(m[1])]; i++ {
					plan = append(plan, at)
				}
			}
		}
		if len(plan) > 0 {
			return plan
		}
		if m := multiattackCountRe.FindStringSubmatch(desc); m != nil {
			for i := 0; i < numberWords[strings.ToLower(m[1])]; i++ {
				plan = append(plan, attacks[0])
			}
			return plan
		}
	}
	pick := attacks[0]
	for _, a := range attacks {
		if strings.Contains(low, strings.ToLower(a.Name)) {
			pick = a
		}
	}
	return []compAttack{pick}
}

// wildShapeAttack: an attack in beast form uses the beast's attacks (its Multiattack too) against the target.
func wildShapeAttack(charID int, description string) string {
	var form string
	db.QueryRow(`SELECT COALESCE(wild_shape_form,'') FROM characters WHERE id = $1`, charID).Scan(&form)
	attacks := formAttacks(form)
	if len(attacks) == 0 {
		return "Your form has no attack in its stat block."
	}
	var raw []byte
	db.QueryRow(`SELECT COALESCE(actions,'[]') FROM monsters WHERE slug = $1`, form).Scan(&raw)
	var acts []map[string]interface{}
	json.Unmarshal(raw, &acts)
	low := strings.ToLower(description)
	plan := beastAttackPlan(attacks, acts, low)
	lobbyID := lobbyOfChar(charID)
	targetID := attackTargetID(charID)
	if ok, why := validateAttackTarget(lobbyID, targetID); !ok {
		return "Cannot attack: " + why
	}
	var name string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&name)
	fake := companion{Name: name, CombatID: charID}
	var lines []string
	for i, a := range plan {
		if i == 0 {
			if extra, typ := primalStrikeDice(charID, low); extra != "" {
				if a.Extra == "" {
					a.Extra, a.ExtraType = extra, typ
				}
			}
		}
		lines = append(lines, companionAttack(lobbyID, fake, a, targetID))
	}
	return fmt.Sprintf("🐾 In Wild Shape, %s attacks: %s", name, strings.Join(lines, " "))
}

// primalStrikeDice: Elemental Fury's Primal Strike (once per turn): 1d8, or 2d8 from level 15, of Cold, Fire, Lightning or Thunder.
func primalStrikeDice(charID int, low string) (string, string) {
	var raw []byte
	var level int
	db.QueryRow(`SELECT COALESCE(subclass_choices,'{}'), level FROM characters WHERE id = $1 AND class = 'druid'`, charID).Scan(&raw, &level)
	var choices map[string]string
	json.Unmarshal(raw, &choices)
	if choices["elemental_fury"] != "primal_strike" || level < 7 {
		return "", ""
	}
	typ := "Fire"
	for _, t := range []string{"cold", "lightning", "thunder", "fire"} {
		if strings.Contains(low, t) {
			typ = titleCase(t)
			break
		}
	}
	if level >= 15 {
		return "2d8", typ
	}
	return "1d8", typ
}
