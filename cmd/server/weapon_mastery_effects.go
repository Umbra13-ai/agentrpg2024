package main

// Weapon Mastery effects the server applies when a player attacks a monster with target_id (2024 rules):
//	Graze  on a miss the target still takes the ability modifier as damage (applied in attack_target.go).
//	Topple on a hit the server rolls the target's Constitution save (DC 8 + ability modifier + Proficiency Bonus);
//	       on a failure the target gets the Prone condition (a condition record, so the GM and players see it).
//	Vex    on a hit that deals damage the attacker has Advantage on the next attack roll against that target.
//	Sap, Slow  reported in the result text (what the player and the GM must apply); not tracked as events.
//	Push, Cleave, Nick  stay as reminders in the attack result (they need a map position or a second attack).

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

type masteryUse struct {
	Name   string
	Dice   string
	Mod    int
	DC     int
	Melee  bool
	Weapon string
}

var masteryUses sync.Map // character id -> masteryUse of the attack being resolved

// vexMark: the monster the next attack gets Advantage against; it ends when the attacker's next turn ends.
type vexMark struct{ Lobby, Target, TurnsEnded int }

var vexMarks sync.Map // character id -> vexMark

// vexTurnEnded counts the end of a creature's turn; the Advantage ends at the end of the attacker's next turn.
func vexTurnEnded(charID int) {
	if v, ok := vexMarks.Load(charID); ok {
		m := v.(vexMark)
		m.TurnsEnded++
		if m.TurnsEnded >= 2 {
			vexMarks.Delete(charID)
		} else {
			vexMarks.Store(charID, m)
		}
	}
}

// clearVexMarks drops the marks of a campaign when its combat starts or ends.
func clearVexMarks(lobbyID int) {
	vexMarks.Range(func(k, v interface{}) bool {
		if v.(vexMark).Lobby == lobbyID {
			vexMarks.Delete(k)
		}
		return true
	})
}

func characterHasMastery(charID int, weaponKey string, w SRDWeapon) bool {
	if w.Mastery == "" || weaponKey == "" {
		return false
	}
	for _, k := range characterMasteries(charID) {
		if weaponNameKey(k) == weaponNameKey(weaponKey) || k == weaponKey {
			return true
		}
	}
	return false
}

// noteMasteryUse remembers the mastery of the weapon just used, for resolveAttackOnMonster.
func noteMasteryUse(charID int, weaponKey string, w SRDWeapon, abilityMod, level int, desc ...string) {
	if !characterHasMastery(charID, weaponKey, w) {
		return
	}
	mastery := w.Mastery
	if len(desc) > 0 {
		if p := tacticalMasterProperty(charID, desc[0]); p != "" {
			mastery = p // Fighter, Tactical Master: Push, Sap or Slow instead of the weapon's own property
		}
	}
	masteryUses.Store(charID, masteryUse{Name: mastery, Dice: w.Damage, Mod: abilityMod, DC: 8 + abilityMod + game.ProficiencyBonus(level), Melee: w.Type == "melee", Weapon: w.Name})
}

func takeMasteryUse(charID int) (masteryUse, bool) {
	v, ok := masteryUses.LoadAndDelete(charID)
	if !ok {
		return masteryUse{}, false
	}
	return v.(masteryUse), true
}

// vexActive says whether the attack being rolled is against the monster the attacker Vexed.
func vexActive(charID int) bool {
	v, ok := vexMarks.Load(charID)
	if !ok {
		return false
	}
	_, id, tgt := attackedMonster(charID)
	return tgt && id == v.(vexMark).Target
}

// vexUsed spends the Advantage when an attack against the Vexed monster has been resolved.
func vexUsed(charID, targetID int) {
	if v, ok := vexMarks.Load(charID); ok && v.(vexMark).Target == targetID {
		vexMarks.Delete(charID)
	}
}

// monsterConScore reads the Constitution score of a monster from the monsters table.
func monsterConScore(monsterKey string) int {
	var con int
	if monsterKey == "" || db.QueryRow(`SELECT COALESCE(con, 10) FROM monsters WHERE slug = $1`, monsterKey).Scan(&con) != nil {
		return 10
	}
	return con
}

// masteryOnHit applies the mastery property after a hit that left the target alive and returns text for the result.
func masteryOnHit(lobbyID, charID, targetID int, name, monsterKey string, mu masteryUse, damage int) string {
	switch mu.Name {
	case "Topple":
		mod := monsterSaveBonus(monsterKey, "con", monsterConScore(monsterKey))
		roll := game.RollDie(20)
		if roll+mod >= mu.DC {
			return fmt.Sprintf(" Topple: %s resists and stays on its feet.", name)
		}
		for _, c := range conditionNames(lobbyID, targetID) {
			if c == "prone" {
				return fmt.Sprintf(" Topple: %s fails its Constitution save but is already Prone.", name)
			}
		}
		db.Exec(`INSERT INTO condition_records (lobby_id, target_id, condition, source, escapes, notes)
			VALUES ($1, $2, 'prone', $3, '[]', $4)`, lobbyID, targetID, "Topple ("+mu.Weapon+")",
			fmt.Sprintf("Failed the Constitution save against Topple (DC %d).", mu.DC))
		return fmt.Sprintf(" Topple: %s fails its Constitution save and falls Prone.", name)
	case "Vex":
		if damage > 0 {
			vexMarks.Store(charID, vexMark{Lobby: lobbyID, Target: targetID})
			return " Vex: you have Advantage on your next attack roll against it before the end of your next turn."
		}
	case "Sap":
		if damage > 0 {
			addCombatEffect(lobbyID, targetID, charID, "sap")
			return fmt.Sprintf(" Sap: %s has Disadvantage on its next attack roll before the start of your next turn.", name)
		}
	case "Slow":
		if damage > 0 {
			addCombatEffect(lobbyID, targetID, charID, "slow")
			return fmt.Sprintf(" Slow: %s's Speed is reduced by 10 feet until the start of your next turn.", name)
		}
	case "Push":
		return pushText(lobbyID, charID, targetID, name, monsterKey, 10)
	}
	return ""
}

var sizeOrder = map[string]int{"tiny": 0, "small": 1, "medium": 2, "large": 3, "huge": 4, "gargantuan": 5}

// pushText applies Push (10 feet) or Forceful Blow (15 feet): a Large or smaller target is pushed straight away on the
// battle map; without a map the player is told the distance and the GM places the creature.
func pushText(lobbyID, charID, targetID int, name, monsterKey string, feet int) string {
	var size string
	db.QueryRow(`SELECT COALESCE(size,'medium') FROM monsters WHERE slug = $1`, monsterKey).Scan(&size)
	if o, ok := sizeOrder[strings.ToLower(strings.SplitN(size, " ", 2)[0])]; ok && o > sizeOrder["large"] {
		return fmt.Sprintf(" Push: %s is too big to be pushed (Large or smaller only).", name)
	}
	moved, hasMap := pushToken(lobbyID, charID, targetID, feet)
	switch {
	case !hasMap:
		return fmt.Sprintf(" Push: %s is pushed up to %d feet straight away from you (there is no map, so the GM places it).", name, feet)
	case moved == 0:
		return fmt.Sprintf(" Push: %s can't be pushed, something is in the way.", name)
	}
	return fmt.Sprintf(" Push: %s is pushed %d feet straight away from you.", name, moved)
}

func ensureMasteryColumns() {
	if db != nil {
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS nick_used BOOLEAN DEFAULT FALSE`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS cleave_used BOOLEAN DEFAULT FALSE`)
		db.Exec(`ALTER TABLE characters ADD COLUMN IF NOT EXISTS action_surge_this_turn BOOLEAN DEFAULT FALSE`)
	}
}

// nickAvailable says whether the off-hand attack with this weapon is the Nick extra attack: a Light weapon whose
// Nick mastery the character has, once per turn. It then is made as part of the Attack action, not with the Bonus Action.
func nickAvailable(charID int, description string) bool {
	key := parseWeaponFromDescription(description)
	w, ok := srdWeapons[key]
	if !ok || w.Mastery != "Nick" || !characterHasMastery(charID, key, w) {
		return false
	}
	var used bool
	db.QueryRow(`SELECT COALESCE(nick_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
	return !used
}

func markNickUsed(charID int) {
	db.Exec(`UPDATE characters SET nick_used = true WHERE id = $1`, charID)
}

// cleaveAttack is the second attack of the Cleave mastery: after a melee hit, one attack against another creature
// within 5 feet of the first. It uses the same attack bonus, the weapon's damage dice without the ability modifier
// (unless negative), and works once per turn.
func cleaveAttack(lobbyID, charID, first, second int, o attackOutcome, mu masteryUse) string {
	var used bool
	db.QueryRow(`SELECT COALESCE(cleave_used,false) FROM characters WHERE id = $1`, charID).Scan(&used)
	if used {
		return " Cleave: you already used Cleave this turn."
	}
	if second == first {
		return " Cleave: pick a different creature."
	}
	if ok, why := validateAttackTarget(lobbyID, second); !ok {
		return " Cleave: " + why
	}
	if !mu.Melee {
		return " Cleave: the weapon must be a melee weapon."
	}
	// with a battle map the second creature must be within 5 feet of the first
	if raw := battleMapTokens(lobbyID); raw != nil {
		var a, b *mapToken
		for i := range raw {
			switch raw[i].ID {
			case first:
				a = &raw[i]
			case second:
				b = &raw[i]
			}
		}
		if a != nil && b != nil && !tokensAdjacent(*a, *b) {
			return " Cleave: that creature is not within 5 feet of the first one."
		}
	}
	db.Exec(`UPDATE characters SET cleave_used = true WHERE id = $1`, charID)
	roll := game.RollDie(20)
	bonus := o.Total - o.Natural
	crit := roll == 20
	extra := attackOutcome{Weapon: o.Weapon, Total: roll + bonus, Natural: roll, Crit: crit, Fumble: roll == 1, DamageType: o.DamageType, RollInfo: ""}
	mod := 0
	if mu.Mod < 0 {
		mod = mu.Mod // the ability modifier counts only when it is negative
	}
	extra.Damage = game.RollDamage(mu.Dice, crit) + mod
	if extra.Damage < 0 {
		extra.Damage = 0
	}
	text, _, err := resolveAttackOnMonster(lobbyID, charID, second, extra)
	if err != nil {
		return " Cleave: the second attack could not be resolved."
	}
	return " Cleave: " + text
}

func battleMapTokens(lobbyID int) []mapToken {
	var raw []byte
	if db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw) != nil || len(raw) == 0 {
		return nil
	}
	var m encounterMap
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m.Tokens
}

// resetTurnFlags gives a character their turn back: action, Bonus Action, reaction, movement, the attacks of the Attack
// action (Extra Attack), and the once-per-turn features (Sneak Attack, Horde Breaker, Foe Slayer, Frenzy, Nick, Cleave).
// Every way a turn can start (the GM's next turn, an automatic advance, a skipped turn, a narrated advance) uses it;
// before, only one of them reset the once-per-turn flags and none reset the attacks of the Attack action.
func resetTurnFlags(charID, speed int) {
	endDodge(charID) // Dodge lasts until the start of your next turn (dodge_2024.go)
	// the basic resources first, so a database that lacks a newer column still gets its turn back
	db.Exec(`UPDATE characters SET action_used = false, bonus_action_used = false, weapon_interaction_used = false,
		movement_remaining = $1, reaction_used = false WHERE id = $2`, speed, charID)
	// Champion level 10, Heroic Warrior: in combat you can give yourself Heroic Inspiration whenever you start your turn without it
	db.Exec(`UPDATE characters SET inspiration = true WHERE id = $1 AND class = 'fighter' AND subclass = 'champion' AND level >= 10 AND COALESCE(inspiration,false) = false`, charID)
	clearUsedKeys(charID, "peerless_aim", "apotheosis", "eldritch_smite") // Boon of Combat Prowess returns at the start of your turn
	for _, set := range []string{
		"bonus_action_spell_cast = false",
		"attacks_remaining = NULL",
		"horde_breaker_used = false, sneak_attack_used = false",
		"frenzy_used = false, nick_used = false, cleave_used = false, action_surge_this_turn = false, steady_aim = false, stun_used = false, primal_strike_used = false",
		"savage_attacker_used = false",
	} {
		db.Exec("UPDATE characters SET "+set+" WHERE id = $1", charID)
	}
	spellTurnStart(charID) // Slow, Haste, Heroism
}
