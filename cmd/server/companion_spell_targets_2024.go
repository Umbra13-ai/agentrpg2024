package main

// Spells aimed at a companion (a familiar, steed or summoned creature, combat id from companionIDBase up).
//
// The cast engine keeps two kinds of spell target: a monster (negative id: hurtMonster, monster saves, condition owner) and a
// character (positive id: the characters table). A companion is neither, so before this file a positive companion id was refused
// by namedCharactersIn as "not a character" (a guard, TestPostgresSpellAtCompanionIsRefusedWithoutEffect: without it the reply
// printed damage or a condition that was never stored).
//
// What is supported now, and why: a pure healing spell (Cure Wounds, Healing Word, Mass Cure Wounds, Mass Healing Word, Prayer of
// Healing, Heal, Regenerate). The supplied SRD 5.2.1 text of these names no creature type restriction ("A creature you touch
// regains ...", "A creature of your choice that you can see ..."), so a companion's creature type does not make it ineligible, and the
// Hit Points go through healCompanion (the companion Hit Point owner, monster_hp_2024.go), never above its maximum. Only an ACTIVE
// companion of the caster's own campaign counts (a dismissed or gone one answers the same 404 as an unknown id).
//
// Everything else a cast could do to a companion (damage, saving throws, conditions, buffs, Aid's maximum, Beacon of Hope, Greater
// Restoration, revival) is refused with companion_target_unsupported, nothing spent, and the reply tells the player to ask the
// GM: the engine has no companion branch for those, and printing an outcome it did not store is the one thing it must not do.

import (
	"fmt"
	"net/http"
	"strings"
)

func hasCompanionID(ids []int) bool {
	for _, id := range ids {
		if isCompanionCombatID(id) {
			return true
		}
	}
	return false
}

// companionHealingSpell: the cast resolves through the plain healing branch of resolveAction (damage-free, a Healing entry, not a buff).
func companionHealingSpell(sp SRDSpell, key string) bool {
	return sp.Healing != "" && sp.DamageDice == "" && buffSpells[key].Kind == "" && len(sp.DamageParts) == 0
}

// companionSpellTargetsIn checks the companions a cast names. It returns the ids that are left for namedCharactersIn (the companion
// ids removed) and false when it has answered.
func companionSpellTargetsIn(w http.ResponseWriter, lobbyID int, req actionRequest, ids []int) ([]int, bool) {
	key := req.SpellSlug
	if key == "" {
		key = parseSpellFromDescription(req.Description)
	}
	key = normalizeSpellSlug(key)
	sp, known := srdSpellsMemory[key]
	if !known || !companionHealingSpell(sp, key) {
		name := key
		if known {
			name = sp.Name
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "companion_target_unsupported",
			"message": fmt.Sprintf("%s cannot be applied to a companion by the server (only plain healing spells can be). Nothing was done and nothing was spent.", strings.TrimSpace(name)),
			"hint":    "Ask the GM to adjudicate this spell on the companion, or aim it at a character or monster."})
		return nil, false
	}
	var rest []int
	for _, id := range ids {
		if !isCompanionCombatID(id) {
			rest = append(rest, id)
			continue
		}
		c, ok := loadCompanion(id - companionIDBase)
		if !ok || c.LobbyID != lobbyID || c.Status != "active" {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found",
				"message": fmt.Sprintf("Character %d is not in your campaign. Nothing was done.", id)})
			return nil, false
		}
	}
	return rest, true
}

// healCompanionByCast heals one companion for a healing spell and says what happened.
func healCompanionByCast(charID, combatID, amount int) string {
	c, ok := loadCompanion(combatID - companionIDBase)
	if !ok {
		return fmt.Sprintf("companion %d is gone and regains nothing", combatID)
	}
	var lobby int
	db.QueryRow(`SELECT COALESCE(lobby_id, 0) FROM characters WHERE id = $1`, charID).Scan(&lobby)
	if c.LobbyID != lobby || c.Status != "active" {
		return fmt.Sprintf("%s is not here and regains nothing", companionDisplayName(c))
	}
	got, err := healCompanion(c.ID, amount)
	if err != nil {
		return fmt.Sprintf("%s could not be healed (%v)", companionDisplayName(c), err)
	}
	return fmt.Sprintf("%s regains %d Hit Points (%d → %d)", companionDisplayName(c), got, c.HP, c.HP+got)
}

// ---- a monster's hit on a companion: what the hit gives besides damage
//
// monsterAttacksCompanion used to deal the damage and drop everything else a stat-block hit gives (its conditions, its own saving
// throw, a Hit or Miss effect, a sentence the server cannot read). Now, after a hit that landed on a companion that is still active:
//   - the conditions the hit gives (parseHitRider: Prone, Grappled with its escape DC, Stunned until ...) go to the condition owner,
//     which keeps a companion's conditions (condition_owner_2024.go); not when the effect is size-gated (the server does not know a
//     companion's size) and not the speed, Disadvantage or Exhaustion parts, which are named for the GM;
//   - the hit's own saving throw is rolled with the companion's stat block (applySaveEffectOnCompanion, below); a Hit or Miss effect and
//     the sentences nothing here reads (unappliedHitRider) are named for the GM.

// companionHitRidersNote is the GM-facing text for a hit that was only previewed (apply false): nothing is applied, so only the text.
func companionHitRidersNote(a monsterActionRow) string {
	if e, ok := parseHitRider(a.Desc); ok && len(e.Conditions) > 0 {
		return " [preview: the hit would also give " + strings.Join(e.Conditions, ", ") + " to the companion]"
	}
	return ""
}

func companionHitRiders(lobbyID, attackerID int, attackerName string, a monsterActionRow, companionID int, hadAdvantage bool, taken int, dealtByType map[string]int) string {
	cur, ok := loadCompanion(companionID)
	if !ok || cur.Status != "active" || cur.HP <= 0 {
		return " [the companion is gone: the hit's other effects are not applied]"
	}
	source := attackerName + "'s " + stripUsage(a.Name)
	var applied, gm []string
	if e, ok := parseHitRider(a.Desc); ok {
		e.SourceID = attackerID
		switch {
		case e.CondMaxSize != "":
			gm = append(gm, fmt.Sprintf("the hit gives %s only to a target of %s size or smaller: check the companion's size", strings.Join(e.Conditions, ", "), titleCase(e.CondMaxSize)))
			// the attach itself is not size-gated (only the condition is): record it; the recurring damage
			// the attach or grapple would have started stays with the GM together with the gate (the residual
			// was stripped at parse time, before the gate's outcome was known)
			if attached := addAttach(lobbyID, attackerID, cur.CombatID, a.Desc, source, false); attached != "" { // the size gate is the GM's here: the cover is not applied, its suffocating does not start
				applied = append(applied, attached)
			}
			hit := a.Desc
			if i := strings.Index(a.Desc, "Hit:"); i >= 0 {
				hit = a.Desc[i+len("Hit:"):]
			}
			for _, s := range strings.Split(hit, ". ") {
				if spec := readRecurringDamage(s); spec != nil {
					gm = append(gm, strings.Trim(strings.TrimSpace(s), "."))
				}
			}
		default:
			if !(e.CondIfAdvantage && !hadAdvantage) { // "... and the darkmantle had Advantage on the attack roll": without it the clause triggers nothing
				// the same writer as every other target and as a companion's failed save (finishTarget): the conditions, the
				// Restrained a grapple brings, the Speed and roll riders for their durations
				applied = append(applied, e.giveConditions(lobbyID, cur.CombatID, source)...)
			}
			// the attach the hit starts: before the recurring damage, whose gate reads the attach row
			if attached := addAttach(lobbyID, attackerID, cur.CombatID, a.Desc, source, !(e.CondIfAdvantage && !hadAdvantage)); attached != "" {
				applied = append(applied, attached)
			}
			// recurring damage the hit sets up: recorded when its grapple or attach gate holds (a companion's
			// conditions are the condition owner's, and the turn pipeline deals a companion's damage
			// through damageCompanion); when the gate did not hold the sentence goes back to the GM
			if rec, gmNote := addRecurringDamage(lobbyID, attackerID, cur.CombatID, a.Desc, attackerName); rec != "" {
				applied = append(applied, rec)
			} else if gmNote != "" {
				gm = append(gm, gmNote)
			}
		}
	}
	if d := hitAfterDamage(lobbyID, attackerID, cur.CombatID, a.Desc, source, taken, dealtByType); d != "" { // Life Drain, burning (hit_after_damage_2024.go)
		applied = append(applied, strings.Trim(d, " []"))
	}
	if e, ok := parseSaveEffect(a.Desc); ok { // the hit's own saving throw: rolled with the companion's stat block (applySaveEffectOnCompanion)
		e.SourceID = attackerID
		if rl, _ := applySaveEffect(lobbyID, source, e, []int{cur.CombatID}, true); len(rl) > 0 {
			applied = append(applied, strings.Join(rl, " "))
		}
	}
	if r := unappliedHitRider(a.Desc); r != "" {
		if suffocationActive(lobbyID, attackerID, cur.CombatID) { // the cover applied: its suffocating is enforced, not the GM's
			r = dropSuffocatingSentences(r)
		}
		if r != "" {
			gm = append(gm, r)
		}
	}
	out := ""
	if len(applied) > 0 {
		out += " [" + strings.Join(applied, "; ") + "]"
	}
	if len(gm) > 0 {
		out += " [GM, not applied automatically: " + strings.Join(gm, "; ") + "]" // gm-one-shot: a companion's hit rider, told with the hit
	}
	return out + hitOrMissNote(a.Desc, true)
}

// applySaveEffectOnCompanion rolls a companion's saving throw against e and applies what follows, as for a monster
// (applySaveEffectOnMonster): the save is saveCtx with the bonus of the stat block the companion was made from (saveModifier), the
// damage is damageCompanion (resistance, immunity, vulnerability), the conditions are the condition owner's. Evasion is the stat
// block's trait of that name. Not applied to a companion: Legendary Resistance and Magic Resistance (they read the fight's monsters), a
// condition gated by size (named for the GM), Exhaustion (named for the GM).
func applySaveEffectOnCompanion(lobbyID int, source string, e saveEffect, tid int, rolled map[int]int, apply bool) (line string, failed, immune bool) {
	c, ok := activeCompanionInCampaign(lobbyID, tid) // this campaign's own, before any save, condition or damage
	if !ok {
		return "the companion is gone or not in this campaign: its saving throw is not rolled", false, false
	}
	name := companionDisplayName(c)
	total := 0
	for i := range e.Damage {
		total += rolled[i]
	}
	sv := e.rollForTarget(lobbyID, tid, total, apply)
	saved := sv.Success
	line = sv.Line(name)
	evasion := e.Ability == "dex" && e.HalfOnSuccess && companionHasTrait(c, "Evasion")
	if meta := monsterMetaFor(c.Form); apply && !saved && meta != nil && meta.LR > 0 { // the use count lives in a fight monster's state: not kept for a companion
		// private: a stat block's Legendary Resistance is not for the shared action log; the GM's notices carry it
		addGMNotice(lobbyID, fmt.Sprintf("%s failed a saving throw (%s), and its stat block has Legendary Resistance, which the server does not track for a companion: it may choose to succeed instead.", name, source))
	}
	var parts []string
	dropped := e.dropsToZero(lobbyID, tid, saved)
	if dropped {
		parts = append(parts, e.dropToZero(lobbyID, tid, apply))
	}
	for i, p := range e.Damage {
		d := rolled[i]
		switch {
		case dropped:
			d = 0
		case saved && (evasion || !e.HalfOnSuccess):
			d = 0
		case saved, evasion:
			d /= 2
		}
		if d <= 0 {
			continue
		}
		if !apply {
			parts = append(parts, fmt.Sprintf("%d %s damage (not applied)", d, p.Type))
			continue
		}
		cur, _ := loadCompanion(c.ID)
		if cur.Status != "active" {
			break
		}
		parts = append(parts, damageCompanion(cur, d, strings.ToLower(p.Type)))
		if after, ok := loadCompanion(c.ID); ok && after.HP < cur.HP {
			if drained := e.drainAfterDamage(lobbyID, tid, saved, p.Type, cur.HP-after.HP); drained != "" {
				parts = append(parts, drained)
			}
		}
	}
	if evasion {
		parts = append(parts, "Evasion")
	}
	if cur, _ := loadCompanion(c.ID); apply && cur.Status == "active" {
		var riders []string
		riders, immune = e.finishTarget(lobbyID, tid, source, apply, saved, saveFailureMargin(sv))
		parts = append(parts, riders...)
	}
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, "; ")
	}
	return line, !saved, immune
}
