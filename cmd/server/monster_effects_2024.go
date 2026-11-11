package main

// Monster effects on characters, resolved from the text of the 2024 stat blocks (SRD 5.2.1). Used by Legendary Actions, auras and
// POST /api/gm/narrate monster_action (breath weapons, gazes, spells and the like, which used to get a made-up attack roll).
//	saving throw effect  "Dexterity Saving Throw: DC 17, one creature ... Failure: 22 (4d10) Poison damage, and the target has the
//	                     Frightened condition until the end of its next turn. Success: Half damage." Each target rolls its save
//	                     (characterSave: proficiency, Aura of Protection, Bless, Exhaustion ...); the damage is rolled once; Evasion
//	                     halves or cancels Dexterity save damage; conditions get their duration ("until the start of its next turn",
//	                     "until the end of its next turn", "for 1 minute", a repeated save at the end of each turn); Exhaustion levels
//	                     are added; anything else in the Failure text is shown to the GM.
//	attack               "makes one Rend attack" (or "one Claw or Tail attack": use picks one) -> the stat block's attack.
//	another action       "uses Lightning Strike" -> that action of the stat block.
//	spell                "uses Spellcasting to cast Scorching Ray (level 3 version)" / "casts Fear" -> the spell from the SRD data,
//	                     with the DC and attack bonus of the monster's Spellcasting: spell attacks (rays) against AC, saving throws
//	                     with damage (half on a success when the spell says so) and the condition spells (Fear, Hold Monster ...).
//	damage without save  "each creature within 10 feet ... takes 11 (2d10) Necrotic damage".
//	movement             "moves up to half its Speed", "teleports": a note for the GM to move the token on the battle map.
// Targets are characters (target or target_ids); an Emanation effect with a battle map and no targets reaches the characters in it.

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

var (
	mfxSaveRe     = regexp.MustCompile(`(Strength|Dexterity|Constitution|Intelligence|Wisdom|Charisma) Saving Throw: DC (\d+)`)
	mfxDamageRe   = regexp.MustCompile(`(\d+) \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage`)
	mfxCondRe     = regexp.MustCompile(`has the ((?:[A-Z][a-z]+)(?:(?:, | and | or )[A-Z][a-z]+)*) conditions?`)
	mfxExhaustRe  = regexp.MustCompile(`gains? (\d+) Exhaustion levels?`)
	mfxAttackRe   = regexp.MustCompile(`makes one ([A-Z][A-Za-z’' -]+?)(?: or ([A-Z][A-Za-z’' -]+?))? attack`)
	mfxUsesRe     = regexp.MustCompile(`uses (?:its )?([A-Z][A-Za-z’' -]+?)(?:[.,]| and | to |$)`)
	mfxSpellRe    = regexp.MustCompile(`(?:uses Spellcasting to cast|casts) ([A-Z][A-Za-z’' /-]+?)(?: \(level (\d) version\))?(?: on itself)?(?: twice)?(?:[.,;]| requiring| using| on |$)`)
	mfxSpellDCRe  = regexp.MustCompile(`spell save DC (\d+)`)
	mfxSpellAtkRe = regexp.MustCompile(`([+−-]\d+) to hit with spell attacks`)
	mfxEmanRe     = regexp.MustCompile(`(\d+)\s*-?\s*foot Emanation`)
	mfxDurationRe = regexp.MustCompile(`(?i)(?:until the (?:start|end) of (?:its|the [^ ]+) next turn|for 1 (?:minute|hour)|for 24 hours)`)
	mfxPlainDmgRe = regexp.MustCompile(`takes (\d+) \((\d+d\d+(?:\s*[+−-]\s*\d+)?)\) ([A-Za-z]+) damage`)
	mfxConditions = map[string]bool{"blinded": true, "charmed": true, "deafened": true, "frightened": true, "grappled": true, "incapacitated": true,
		"invisible": true, "paralyzed": true, "petrified": true, "poisoned": true, "prone": true, "restrained": true, "stunned": true, "unconscious": true}
)

func normDice(d string) string {
	return strings.ReplaceAll(strings.ReplaceAll(d, " ", ""), "−", "-")
}

// mfxSection cuts the text after a label up to the next label.
func mfxSection(text, label string, next ...string) string {
	i := strings.Index(text, label)
	if i < 0 {
		return ""
	}
	rest := text[i+len(label):]
	end := len(rest)
	for _, n := range next {
		if j := strings.Index(rest, n); j >= 0 && j < end {
			end = j
		}
	}
	return strings.TrimSpace(rest[:end])
}

type saveEffect struct {
	Ability       string // dex, con ...
	AbilityName   string // Dexterity
	DC            int
	Damage        []damagePart
	HalfOnSuccess bool
	Conditions    []string
	Rounds        int    // condition duration in rounds (0: until removed)
	UntilStart    bool   // "until the start of its next turn"
	EndSave       bool   // repeats the save at the end of each of its turns
	Exhaustion    int    // Exhaustion levels on a failure
	Immunity      bool   // Success: immune to this aura for 24 hours
	Extra         string // Failure text the server doesn't apply (for the GM)
	Emanation     int    // feet, when the targets are in an Emanation from the monster
	IsSpell       bool   // a spell: items that guard against spells work (legendary_more_2024.go)
	SpellLevel    int    // its level (0: a cantrip)
	School        string // its school (Necromancy: the Scarab of Protection)
	SingleTarget  bool   // aimed only at one creature, no area (Spell Absorption, Ring of Spell Turning's Reaction)
	SourceID      int    // the monster it comes from
	FromUndead    bool   // it comes from an Undead (the Scarab of Protection)
}

// parseSaveEffect reads a saving throw effect from a stat block text. ok is false when there is none.
func parseSaveEffect(text string) (saveEffect, bool) {
	m := mfxSaveRe.FindStringSubmatch(text)
	if m == nil {
		return saveEffect{}, false
	}
	e := saveEffect{AbilityName: m[1], Ability: strings.ToLower(m[1][:3])}
	e.DC, _ = strconv.Atoi(m[2])
	fail := mfxSection(text, "Failure:", "Success:", "Failure or Success:", "Second Failure:")
	if fail == "" {
		fail = mfxSection(text, "First Failure:", "Second Failure:", "Success:")
	}
	succ := mfxSection(text, "Success:", "Failure or Success:")
	if strings.Contains(text, "Failure or Success:") && strings.Index(text, "Success:") == strings.Index(text, "Failure or Success:")+len("Failure or ") {
		succ = "" // the only "Success:" is the one of "Failure or Success:"
	}
	for _, d := range mfxDamageRe.FindAllStringSubmatch(fail, -1) {
		e.Damage = append(e.Damage, damagePart{Dice: normDice(d[2]), Type: strings.ToLower(d[3])})
	}
	low := strings.ToLower(succ)
	e.HalfOnSuccess = strings.Contains(low, "half damage") || strings.Contains(low, "half as much damage")
	e.Immunity = strings.Contains(low, "immune to this")
	if c := mfxCondRe.FindStringSubmatch(fail); c != nil {
		for _, name := range regexp.MustCompile(`, | and | or `).Split(c[1], -1) {
			if n := strings.ToLower(strings.TrimSpace(name)); mfxConditions[n] {
				e.Conditions = append(e.Conditions, n)
			}
		}
	}
	lf := strings.ToLower(fail)
	switch {
	case strings.Contains(lf, "until the start of its next turn"):
		e.UntilStart = true
	case strings.Contains(lf, "until the end of its next turn"), strings.Contains(lf, "next turn"):
		e.Rounds = 1
	case strings.Contains(lf, "for 1 minute"):
		e.Rounds = 10
	case strings.Contains(lf, "for 1 hour"):
		e.Rounds = 600
	}
	e.EndSave = strings.Contains(lf, "repeats the save at the end of each of its turns")
	if x := mfxExhaustRe.FindStringSubmatch(fail); x != nil {
		e.Exhaustion, _ = strconv.Atoi(x[1])
	}
	if x := mfxEmanRe.FindStringSubmatch(text); x != nil {
		e.Emanation, _ = strconv.Atoi(x[1])
	}
	// what the server doesn't apply, for the GM: the Failure sentences that are not only damage or a condition
	var extra []string
	for _, s := range strings.Split(fail, ". ") {
		ls := strings.ToLower(s)
		plain := mfxDamageRe.ReplaceAllString(s, "")
		plain = mfxCondRe.ReplaceAllString(plain, "")
		plain = mfxDurationRe.ReplaceAllString(plain, "")
		plain = strings.Trim(strings.NewReplacer("The target", "", "and the target", "", "damage", "", ",", "", ".", "").Replace(plain), " ")
		if strings.Contains(ls, "repeats the save") || strings.Contains(ls, "exhaustion level") || len(plain) < 25 {
			continue
		}
		extra = append(extra, strings.TrimSuffix(strings.TrimSpace(s), "."))
	}
	if both := mfxSection(text, "Failure or Success:"); both != "" {
		extra = append(extra, "Failure or Success: "+strings.TrimSuffix(both, "."))
	}
	e.Extra = strings.Join(extra, ". ")
	return e, true
}

// characterAC is a character's Armor Class against a monster's attack (Slow, Shield of Faith, Haste, Smite of Protection).
func characterACFor(lobbyID, charID int) int {
	var ac int
	db.QueryRow(`SELECT COALESCE(ac,10) FROM characters WHERE id = $1`, charID).Scan(&ac)
	return ac + spellACAdjust(lobbyID, charID) + halfCoverBonus(lobbyID, charID)
}

func characterNameOf(charID int) string {
	var n string
	db.QueryRow(`SELECT name FROM characters WHERE id = $1`, charID).Scan(&n)
	return n
}

// applySaveEffect makes each target roll the save and applies the result. source names the effect for condition records.
// It returns one line per target; immuneAfter lists the targets that succeeded on an effect that gives immunity.
func applySaveEffect(lobbyID int, source string, e saveEffect, targets []int, apply bool) (lines []string, immuneAfter []int) {
	rolled := map[int]int{}
	for i, p := range e.Damage {
		rolled[i] = game.RollDamage(p.Dice, false)
	}
	anyFailed := false
	for _, tid := range targets {
		if tid <= 0 {
			continue
		}
		name := characterNameOf(tid)
		if why := etherealBlock(lobbyID, tid); why != "" { // Plate Armor of Etherealness
			lines = append(lines, why)
			continue
		}
		if e.IsSpell && e.SingleTarget {
			if absorbed, note := staffAbsorbs(lobbyID, tid, e.SpellLevel); absorbed { // Staff of the Magi
				lines = append(lines, name+": "+note)
				continue
			}
		}
		advNote := ""
		if e.IsSpell {
			spellSaveCtx.Store(tid, true) // items that give Advantage against spells (spellSaveAdvantage)
			if p := spellSaveProtection(lobbyID, tid); p != "" {
				advNote = " with Advantage (" + p + ")"
			}
		}
		for _, c := range e.Conditions {
			if strings.EqualFold(c, "poisoned") {
				poisonSaveCtx.Store(tid, true) // Protection from Poison: Advantage against being Poisoned
			}
		}
		if ok, why := buffSaveAdvantage(lobbyID, tid, e.Ability); ok { // Foresight, Holy Aura, Beacon of Hope (buff_spells_2024.go)
			advNote += " with Advantage (" + why + ")"
		}
		saved, total := characterSave(tid, e.Ability, e.DC)
		spellSaveCtx.Delete(tid)
		poisonSaveCtx.Delete(tid)
		var protect []string
		if !saved {
			if note := scarabPreserves(tid, e.IsSpell && strings.EqualFold(e.School, "necromancy"), e.FromUndead); note != "" {
				saved = true
				protect = append(protect, note)
			}
		}
		anyFailed = anyFailed || !saved
		line := fmt.Sprintf("%s %s the %s saving throw (%d vs DC %d)%s", name, map[bool]string{true: "succeeds on", false: "fails"}[saved], e.AbilityName, total, e.DC, advNote)
		if saved && e.IsSpell && e.SpellLevel <= 7 && ringTurns(tid) { // Ring of Spell Turning: no effect on a success
			line += ": Ring of Spell Turning: the spell has no effect on you."
			if e.SingleTarget {
				line += turnSpellBack(lobbyID, tid, source, e, rolled)
			}
			lines = append(lines, line)
			continue
		}
		var parts []string
		parts = append(parts, protect...)
		evasion := e.Ability == "dex" && e.HalfOnSuccess && hasEvasion(tid)
		for i, p := range e.Damage {
			d := rolled[i]
			switch {
			case saved && (evasion || !e.HalfOnSuccess):
				d = 0
			case saved, evasion: // a success halves; Evasion halves a failure
				d /= 2
			}
			if d <= 0 {
				continue
			}
			if apply {
				parts = append(parts, damageCharacter(tid, d, p.Type))
			} else {
				parts = append(parts, fmt.Sprintf("%d %s damage (not applied)", d, p.Type))
			}
		}
		if evasion {
			parts = append(parts, "Evasion")
		}
		if !saved {
			for _, c := range e.Conditions {
				if imm, why := auraImmune(lobbyID, tid, c); imm {
					parts = append(parts, fmt.Sprintf("not %s (%s)", c, why))
					continue
				}
				if imm, why := buffConditionImmune(lobbyID, tid, c, e.SourceID, e.IsSpell); imm { // Mind Blank, Heroes' Feast ...
					parts = append(parts, fmt.Sprintf("not %s (%s)", c, why))
					continue
				}
				endAbility := ""
				if e.EndSave {
					endAbility = e.Ability
				}
				addMonsterCondition(lobbyID, tid, c, source, e.Rounds, endAbility, e.DC)
				if e.UntilStart {
					db.Exec(`UPDATE condition_records SET notes = 'until_start_of_next_turn' WHERE lobby_id = $1 AND target_id = $2 AND condition = $3 AND source = $4`, lobbyID, tid, c, source)
				}
				parts = append(parts, titleCase(c))
			}
			if e.Exhaustion > 0 {
				parts = append(parts, addExhaustion(tid, e.Exhaustion))
			}
		} else if e.Immunity {
			immuneAfter = append(immuneAfter, tid)
			parts = append(parts, "immune to it for 24 hours")
		}
		if len(parts) > 0 {
			line += ": " + strings.Join(parts, "; ")
		}
		lines = append(lines, line)
	}
	if e.Extra != "" && (anyFailed || strings.HasPrefix(e.Extra, "Failure or Success")) {
		if len(e.Damage) > 0 || len(e.Conditions) > 0 { // the text repeats what was just applied; only the rest is the GM's
			lines = append(lines, "The damage and conditions above are applied; the GM handles the rest of this text (movement, pushes, other effects): "+e.Extra)
		} else {
			lines = append(lines, "For the GM to apply on a failure: "+e.Extra)
		}
	}
	return lines, immuneAfter
}

// effectImmune: the creature succeeded on this monster's effect that grants immunity (F70).
func effectImmune(lobbyID, targetID, monsterID int, entry string) bool {
	for _, e := range spellEffectsOn(lobbyID, targetID) {
		if e.Kind == "effect_immune" && e.CasterID == monsterID && strings.EqualFold(e.Detail, entry) {
			return true
		}
	}
	return false
}

// charactersInEmanation lists the characters within feet of a monster on the battle map (nil without a map).
func charactersInEmanation(lobbyID, monsterID, feet int) []int {
	m, ok := activeEncounterMap(lobbyID)
	if !ok {
		return nil
	}
	src, ok := m.token(monsterID)
	if !ok {
		return nil
	}
	var out []int
	for _, t := range m.Tokens {
		if t.ID > 0 && tokenDistanceFeet(src, t) <= feet {
			out = append(out, t.ID)
		}
	}
	return out
}

// monsterSpellNumbers reads the spell save DC and attack bonus from an action text or the monster's Spellcasting action.
func monsterSpellNumbers(text string, meta *monsterMeta) (dc, atk int) {
	if m := mfxSpellDCRe.FindStringSubmatch(text); m != nil {
		dc, _ = strconv.Atoi(m[1])
	}
	if m := mfxSpellAtkRe.FindStringSubmatch(text); m != nil {
		atk, _ = strconv.Atoi(strings.ReplaceAll(m[1], "−", "-"))
	}
	if meta != nil && (dc == 0 || atk == 0) {
		for _, a := range meta.Actions {
			if strings.HasPrefix(a.Name, "Spellcasting") {
				d2, a2 := monsterSpellNumbers(a.Text, nil)
				if dc == 0 {
					dc = d2
				}
				if atk == 0 {
					atk = a2
				}
			}
		}
	}
	if atk == 0 && dc > 0 {
		atk = dc - 8 // a stat block's spell attack bonus is its save DC - 8
	}
	return
}

func spellSlugFromName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.NewReplacer("’", "", "'", "", "/", "-", " ", "-").Replace(s)
	return s
}

// resolveMonsterSpell casts a spell from the SRD data at characters.
// use carries the GM's choice where the spell needs one (Command's word: approach, drop, flee, grovel or halt; grovel by default).
func resolveMonsterSpell(lobbyID, monsterID int, monsterName, spellName string, level, dc, atk int, targets []int, selfCast, apply bool, use string) []string {
	if why := monsterCastBlocked(lobbyID, monsterID); why != "" { // Antimagic Field (spells_more_2024.go)
		return []string{fmt.Sprintf("%s tries to cast %s, but %s.", monsterName, spellName, why)}
	}
	spellLevel := level
	if sp, ok := srdSpellsMemory[spellSlugFromName(spellName)]; ok && spellLevel < sp.Level {
		spellLevel = sp.Level
	}
	kept, notes := magicShieldedTargets(lobbyID, monsterID, spellLevel, targets) // Antimagic Field, Globe of Invulnerability
	if len(targets) > 0 && len(kept) == 0 && !selfCast {
		return append([]string{fmt.Sprintf("%s casts %s.", monsterName, spellName)}, notes...)
	}
	return append(resolveMonsterSpellRules(lobbyID, monsterID, monsterName, spellName, level, dc, atk, kept, selfCast, apply, use), notes...)
}

func resolveMonsterSpellRules(lobbyID, monsterID int, monsterName, spellName string, level, dc, atk int, targets []int, selfCast, apply bool, use string) []string {
	slug := spellSlugFromName(spellName)
	sp, ok := srdSpellsMemory[slug]
	if !ok {
		return []string{fmt.Sprintf("%s casts %s (the GM resolves it).", monsterName, spellName)}
	}
	if level < sp.Level {
		level = sp.Level
	}
	head := fmt.Sprintf("%s casts %s", monsterName, sp.Name)
	if level > sp.Level {
		head += fmt.Sprintf(" (level %d)", level)
	}
	if selfCast {
		if slug == "invisibility" {
			addMonsterCondition(lobbyID, monsterID, "invisible", sp.Name, 0, "", 0)
			return []string{head + " on itself: it has the Invisible condition (until it attacks, deals damage or casts a spell, or its Concentration ends)."}
		}
		return []string{head + " on itself."}
	}
	areaAimed := false
	if v, pt := monsterAreaPoint.Load(lobbyID); pt && sp.AoEShape != "" && !isSpellAttack(sp) { // aimed at a point (O-5)
		aim := pointToken(v.(mapSquare))
		targets, _ = monsterSpellArea(lobbyID, monsterID, sp, aim, nil)
		if len(targets) == 0 {
			return []string{head + fmt.Sprintf(" at %s: no character stands in the area.", aim.Name)}
		}
		areaAimed = true
	}
	if len(targets) == 0 {
		return []string{head + ": name the targets (target or target_ids) to resolve it."}
	}
	dice := sp.DamageDice
	if d, ok := sp.DamageAtSlotLevel[strconv.Itoa(level)]; ok && d != "" {
		dice = d
	}
	lines := []string{head + "."}
	if isSpellAttack(sp) {
		count, beamDice, _ := beamPlan(slug, sp.Level, level, 0)
		if count == 0 {
			count, beamDice = 1, dice
		}
		if count == 1 && len(targets) == 1 {
			if absorbed, note := staffAbsorbs(lobbyID, targets[0], level); absorbed {
				return append(lines, characterNameOf(targets[0])+": "+note)
			}
		}
		for i := 0; i < count; i++ {
			tid := targets[i%len(targets)]
			if why := etherealBlock(lobbyID, tid); why != "" {
				lines = append(lines, why)
				continue
			}
			ac := characterACFor(lobbyID, tid)
			roll := game.RollDie(20)
			total := roll + atk
			name := characterNameOf(tid)
			if roll == 1 || (roll != 20 && total < ac) {
				lines = append(lines, fmt.Sprintf("Spell attack at %s: %d vs AC %d - MISS", name, total, ac))
				continue
			}
			dmg := game.RollDamage(normDice(beamDice), roll == 20)
			res := fmt.Sprintf("%d %s damage", dmg, strings.ToLower(sp.DamageType))
			if apply {
				res = damageCharacter(tid, dmg, strings.ToLower(sp.DamageType))
			}
			label := "HIT"
			if roll == 20 {
				label = "CRITICAL HIT"
			}
			lines = append(lines, fmt.Sprintf("Spell attack at %s: %d vs AC %d - %s, %s", name, total, ac, label, res))
		}
		return lines
	}
	if slug == "command" { // Wisdom save; on a failure the target obeys the word on its next turn (the same record as a player's Command)
		word := "grovel"
		if w := commandWordRe.FindString(strings.ToLower(use)); w != "" {
			word = w
		}
		if len(targets) == 1 {
			if absorbed, note := staffAbsorbs(lobbyID, targets[0], level); absorbed {
				return append(lines, characterNameOf(targets[0])+": "+note)
			}
		}
		limit := 1 + level - 1 // one creature, one more per slot level above 1
		if limit < 1 {
			limit = 1
		}
		for i, tid := range targets {
			if i >= limit {
				lines = append(lines, fmt.Sprintf("(Command at level %d reaches %d creature(s); %s isn't affected.)", level, limit, characterNameOf(tid)))
				continue
			}
			name := characterNameOf(tid)
			saved, total := characterSave(tid, "wis", dc)
			if saved {
				lines = append(lines, fmt.Sprintf("%s succeeds on the Wisdom saving throw (%d vs DC %d).", name, total, dc))
				continue
			}
			if apply {
				addSpellEffect(lobbyID, tid, monsterID, "Command", "commanded", word, 1, "", 0)
			}
			lines = append(lines, fmt.Sprintf("%s fails the Wisdom saving throw (%d vs DC %d) and must obey \"%s\": on its next turn it %s.", name, total, dc, titleCase(word), commandText[word]))
		}
		return lines
	}
	if sp.SavingThrow != "" {
		e := saveEffect{Ability: saveAbilityKey(sp.SavingThrow), DC: dc}
		e.AbilityName = map[string]string{"str": "Strength", "dex": "Dexterity", "con": "Constitution", "int": "Intelligence", "wis": "Wisdom", "cha": "Charisma"}[e.Ability]
		if dice != "" {
			e.Damage = []damagePart{{Dice: normDice(dice), Type: strings.ToLower(sp.DamageType)}}
			e.HalfOnSuccess = strings.Contains(strings.ToLower(sp.Description), "half as much damage")
		}
		if cs, ok := conditionSpells[slug]; ok {
			e.Conditions, e.Rounds, e.EndSave = cs.Conditions, cs.Rounds, cs.EndSave
		}
		if len(e.Damage) == 0 && len(e.Conditions) == 0 {
			e.Extra = sp.Name + ": apply the spell's effect on a failed save"
		}
		e.IsSpell, e.SpellLevel, e.School, e.SourceID = true, level, sp.School, monsterID
		e.FromUndead = monsterTypeOf(lobbyID, monsterID) == "undead"
		if sp.AoEShape != "" && !areaAimed { // the spell's area on the battle map (F61)
			kept, note := areaFilter(lobbyID, monsterID, sp, targets)
			if note != "" {
				lines = append(lines, strings.TrimSpace(note))
			}
			if m, ok := activeEncounterMap(lobbyID); ok && len(targets) > 0 {
				if aim, ok := m.token(targets[0]); ok {
					if _, added := monsterSpellArea(lobbyID, monsterID, sp, aim, kept); len(added) > 0 {
						all, _ := monsterSpellArea(lobbyID, monsterID, sp, aim, nil)
						seen := map[int]bool{}
						for _, id := range kept {
							seen[id] = true
						}
						for _, id := range all {
							if !seen[id] {
								kept = append(kept, id)
							}
						}
						lines = append(lines, fmt.Sprintf("Also in the area of %s: %s.", sp.Name, strings.Join(added, ", "))) // O-5
					}
				}
			}
			targets = kept
		}
		e.SingleTarget = sp.AoEShape == "" && len(targets) == 1
		l, _ := applySaveEffect(lobbyID, monsterName+" ("+sp.Name+")", e, targets, apply)
		return append(lines, l...)
	}
	return append(lines, "The GM resolves the spell's effect.")
}

// resolveMonsterEffectText resolves what a stat block entry says (a Legendary Action, an action without an attack roll).
func resolveMonsterEffectText(r *http.Request, lobbyID, monsterID int, meta *monsterMeta, entryName, text string, targets []int, use string, apply bool, depth int) []string {
	name := creatureName(lobbyID, monsterID)
	var lines []string
	var notes []string
	if strings.Contains(text, "moves up to") || strings.Contains(text, "fly up to") || strings.Contains(text, "teleports") {
		notes = append(notes, "Movement: move its token on the battle map (PUT /api/campaigns/{id}/battle-map) as the text says.")
	}
	// an attack of the stat block
	if m := mfxAttackRe.FindStringSubmatch(text); m != nil {
		atk := m[1]
		if m[2] != "" && use != "" && strings.EqualFold(strings.TrimSpace(use), m[2]) {
			atk = m[2]
		}
		if len(targets) == 0 {
			return append([]string{fmt.Sprintf("%s: name the target of the %s attack (target).", entryName, atk)}, notes...)
		}
		txt, _, ok := resolveMonsterAttack(r, lobbyID, fmt.Sprintf("#%d", monsterID), atk, strconv.Itoa(targets[0]), apply)
		if !ok {
			txt = fmt.Sprintf("%s makes one %s attack (the GM rolls it).", name, atk)
		}
		return append([]string{txt}, notes...)
	}
	// the spell the GM picks with "use" from the ones the text offers (legendary_more_2024.go)
	if spName, lvl, ok := monsterSpellFromUse(text, use); ok && depth == 0 {
		dc, atk := monsterSpellNumbers(text, meta)
		lines = append(lines, resolveMonsterSpell(lobbyID, monsterID, name, spName, lvl, dc, atk, targets, false, apply, use)...)
		return append(lines, notes...)
	}
	// a spell
	if m := mfxSpellRe.FindStringSubmatch(text); m != nil && !strings.Contains(m[1], "Spellcasting") {
		lvl, _ := strconv.Atoi(m[2])
		dc, atk := monsterSpellNumbers(text, meta)
		times := 1
		if strings.Contains(text, " twice") {
			times = 2
		}
		for i := 0; i < times; i++ {
			lines = append(lines, resolveMonsterSpell(lobbyID, monsterID, name, m[1], lvl, dc, atk, targets, strings.Contains(text, "on itself"), apply, use)...)
		}
		return append(lines, notes...)
	}
	// a saving throw effect in the text itself
	if e, ok := parseSaveEffect(text); ok {
		if len(targets) == 0 && e.Emanation > 0 {
			targets = charactersInEmanation(lobbyID, monsterID, e.Emanation)
		}
		if _, pt := monsterAreaPoint.Load(lobbyID); len(targets) == 0 && pt {
			targets, _ = monsterAreaFilter(lobbyID, monsterID, text, nil) // aimed at the GM's point: everyone in the area (O-5)
		}
		if len(targets) == 0 {
			return append([]string{fmt.Sprintf("%s: name the targets (target or target_ids) of the %s saving throw.", entryName, e.AbilityName)}, notes...)
		}
		e.SourceID = monsterID
		e.FromUndead = monsterTypeOf(lobbyID, monsterID) == "undead"           // a harmful effect from an Undead (Scarab of Protection)
		kept, areaNote := monsterAreaFilter(lobbyID, monsterID, text, targets) // a Line, Cone or Sphere on the battle map (F58)
		if areaNote != "" {
			notes = append(notes, areaNote)
		}
		if len(kept) == 0 {
			return append([]string{fmt.Sprintf("%s uses %s; no target stands in the area.", name, entryName)}, notes...)
		}
		// F70: a creature that succeeded on an effect that grants immunity ("immune to this mummy's Dreadful Glare for 24 hours")
		// is skipped; the immunity used to be announced and then forgotten
		var fresh []int
		var already []string
		for _, t := range kept {
			if effectImmune(lobbyID, t, monsterID, entryName) {
				already = append(already, creatureName(lobbyID, t))
				continue
			}
			fresh = append(fresh, t)
		}
		if len(already) > 0 {
			notes = append(notes, fmt.Sprintf("Immune to %s's %s after a successful save (24 hours): %s.", name, entryName, strings.Join(already, ", ")))
		}
		if len(fresh) == 0 {
			return append([]string{fmt.Sprintf("%s uses %s; every target is immune to it.", name, entryName)}, notes...)
		}
		l, immune := applySaveEffect(lobbyID, name+" ("+entryName+")", e, fresh, apply)
		if apply {
			for _, t := range immune {
				addSpellEffect(lobbyID, t, monsterID, entryName, "effect_immune", entryName, 14400, "", 0) // 24 hours; ends with the fight
			}
		}
		lines = append([]string{fmt.Sprintf("%s uses %s.", name, entryName)}, l...)
		return append(lines, notes...)
	}
	// another action of the stat block ("uses Lightning Strike")
	if m := mfxUsesRe.FindStringSubmatch(text); m != nil && meta != nil && depth < 2 {
		for _, a := range append(append([]srdMonsterEntry{}, meta.Actions...), meta.BonusActions...) {
			if strings.EqualFold(stripUsage(a.Name), strings.TrimSpace(m[1])) {
				if isAttackEntry(a.Text) {
					if len(targets) == 0 {
						return append([]string{fmt.Sprintf("%s: name the target of %s (target).", entryName, a.Name)}, notes...)
					}
					txt, _, ok := resolveMonsterAttack(r, lobbyID, fmt.Sprintf("#%d", monsterID), a.Name, strconv.Itoa(targets[0]), apply)
					if ok {
						return append([]string{txt}, notes...)
					}
				}
				return append(resolveMonsterEffectText(r, lobbyID, monsterID, meta, a.Name, a.Text, targets, use, apply, depth+1), notes...)
			}
		}
	}
	// damage without a saving throw
	if m := mfxPlainDmgRe.FindStringSubmatch(text); m != nil {
		if len(targets) == 0 {
			return append([]string{fmt.Sprintf("%s: name the creatures that take the damage (target_ids).", entryName)}, notes...)
		}
		dmg := game.RollDamage(normDice(m[2]), false)
		lines = append(lines, fmt.Sprintf("%s uses %s.", name, entryName))
		for _, tid := range targets {
			if tid > 0 {
				res := fmt.Sprintf("%d %s damage", dmg, strings.ToLower(m[3]))
				if apply {
					res = damageCharacter(tid, dmg, strings.ToLower(m[3]))
				}
				lines = append(lines, characterNameOf(tid)+" "+res)
			}
		}
		return append(lines, notes...)
	}
	return append([]string{fmt.Sprintf("%s uses %s: %s (the GM resolves it).", name, entryName, text)}, notes...)
}

// isAttackEntry: the stat block entry is an attack roll.
func isAttackEntry(text string) bool {
	return strings.Contains(text, "Attack Roll:")
}

// stripUsage drops "(Recharge 5–6)", "(3/Day)" and the like from an entry name.
func stripUsage(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return strings.TrimSpace(name[:i])
	}
	return strings.TrimSpace(name)
}

// targetIDsFrom turns a target (name or id) and target_ids into character ids in the campaign.
func targetIDsFrom(lobbyID int, target string, ids []int) []int {
	var out []int
	seen := map[int]bool{}
	if target != "" {
		if id, _, _, ok := characterByNameOrID(lobbyID, target); ok {
			out = append(out, id)
			seen[id] = true
		}
	}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			var n int
			if db.QueryRow(`SELECT count(*) FROM characters WHERE id = $1 AND lobby_id = $2`, id, lobbyID).Scan(&n) == nil && n > 0 {
				out = append(out, id)
				seen[id] = true
			}
		}
	}
	return out
}
