package main

// Monster Reactions (SRD 5.2.1 stat blocks; F224, Nov 10). The GM chooses when a monster reacts (gm/narrate monster_action with the
// Reaction's name, right after the trigger) and the server resolves it. One Reaction a round per monster; "(N/Day)" counts uses.
//
//	"adds N to its AC against that attack"  Parry (Bandit Captain, Knight, Gladiator, Erinyes, Marilith, Noble, Warrior Veteran),
//	                                        Riposte (and on a miss its attack back), Whirlwind of Sand (any attack roll; it teleports
//	                                        and Blinds the creatures the GM names in target_ids): the character's hit this turn that
//	                                        doesn't beat the new AC becomes a miss and its damage is undone; a natural 20 still hits
//	"casts Counterspell or Shield"          Protective Magic (Mage, Archmage, Lich): Shield (+5 AC, the hit undone if it misses now, +5
//	                                        until the start of its next turn); Counterspell goes through POST /api/gm/counterspell
//	"reduces the damage it takes ... by"    Deflect Missile (Stone Giant): a ranged hit's Bludgeoning, Piercing or Slashing damage
//	"splits into two new"                   Split (Black Pudding, Ochre Jelly): two creatures, the Hit Points shared
//	"swap places ... becomes the target"    Redirect Attack (Goblin Boss): the hit goes to the ally the GM names (target)
//	"the wearer gains a +5 bonus to AC"     Protection (Shield Guardian): the wearer (target) gets +5 AC against this turn's hits
//	"uses <Action>"                         Reflexive Antennae (Rust Monster): that action
//	other Reactions                         their text: a saving throw (Unnerving Gaze) is rolled; movement and areas are the GM's
//
// Also here: the Advantage on the save that Charm Person, Charm Monster and the Dominate spells give a creature the caster's side is
// fighting, and notes for the player when a monster's Magic Resistance or Legendary Resistance changed a save.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

type charHitOnMonster struct {
	Marker             string
	CharID             int
	Total, AC          int
	Nat20, Melee       bool
	DamageType, Weapon string
	HPBefore, HPAfter  int
}

var lastHitOnMonster sync.Map // "lobby:monster" -> charHitOnMonster (the latest character hit this turn)

func noteCharHitOnMonster(lobbyID, monsterID int, h charHitOnMonster) {
	h.Marker = combatTurnMarker(lobbyID)
	lastHitOnMonster.Store(fmt.Sprintf("%d:%d", lobbyID, monsterID), h)
}

func thisTurnHitOnMonster(lobbyID, monsterID int) (charHitOnMonster, bool) {
	v, ok := lastHitOnMonster.Load(fmt.Sprintf("%d:%d", lobbyID, monsterID))
	if !ok {
		return charHitOnMonster{}, false
	}
	h := v.(charHitOnMonster)
	if h.Marker == "" || h.Marker != combatTurnMarker(lobbyID) {
		return charHitOnMonster{}, false
	}
	return h, true
}

func weaponIsMelee(weapon string) bool {
	if weapon == "" || strings.EqualFold(weapon, "unarmed") {
		return true
	}
	if _, w, ok := findWeapon(weapon); ok {
		return w.Type == "melee"
	}
	if _, w, ok := findWeapon(strings.ReplaceAll(mapWeaponKey(0, weapon), "_", " ")); ok {
		return w.Type == "melee"
	}
	return true
}

var (
	reactACRe     = regexp.MustCompile(`adds (\d+) to its AC against (?:that|the) attack`)
	reactReduceRe = regexp.MustCompile(`reduces the damage it takes from the attack by \d+ \((\d+d\d+)(?:\s*\+\s*(\d+))?\)`)
	reactUsesRe   = regexp.MustCompile(`Response: The [\w ]+? uses ([A-Z][\w ]+?)\.`)
	reactRiposte  = regexp.MustCompile(`makes one ([A-Z][\w ]+?) attack against the triggering creature`)
	perDayRe      = regexp.MustCompile(`\((\d+)/Day\)`)
)

var reactionDayUses sync.Map // "lobby:monster:reaction" -> uses this fight

// resolveMonsterReaction resolves a Reaction entry; handled false lets the generic text resolver take it (a saving throw reaction).
func resolveMonsterReaction(r *http.Request, lobbyID, id int, meta *monsterMeta, entry srdMonsterEntry, target string, targetIDs []int, apply bool) (text string, handled bool, refusal string) {
	name := creatureName(lobbyID, id)
	base := stripUsage(entry.Name)
	key := fmt.Sprintf("%d:%d", lobbyID, id)
	round := currentRound(lobbyID)
	if v, ok := monsterReactionRound.Load(key); ok && v.(int) == round {
		return "", true, fmt.Sprintf("%s has already used its Reaction this round (one a round).", name)
	}
	dayKey := key + ":" + strings.ToLower(base)
	if m := perDayRe.FindStringSubmatch(entry.Name); m != nil {
		n, _ := strconv.Atoi(m[1])
		if v, ok := reactionDayUses.Load(dayKey); ok && v.(int) >= n {
			return "", true, fmt.Sprintf("%s has used %s %d times today.", name, base, n)
		}
	}
	low := strings.ToLower(entry.Text)
	hit, haveHit := thisTurnHitOnMonster(lobbyID, id)
	charName := ""
	if haveHit {
		charName = getCharacterName(hit.CharID)
	}
	undoIfMiss := func(bonus int, label string) string {
		desc := fmt.Sprintf("%s's attack (%d against AC %d)", charName, hit.Total, hit.AC+bonus)
		if hit.Nat20 {
			return fmt.Sprintf(" %s: +%d AC, but %s was a natural 20: still a hit.", label, bonus, charName)
		}
		if hit.Total >= hit.AC+bonus {
			return fmt.Sprintf(" %s: +%d AC; %s still hits.", label, bonus, desc)
		}
		setMonsterHP(lobbyID, id, hit.HPBefore)
		lastHitOnMonster.Delete(key)
		return fmt.Sprintf(" %s: +%d AC; %s is now a miss, its damage undone (%s is back to %d HP).", label, bonus, desc, name, hit.HPBefore)
	}
	out := ""
	switch {
	case reactACRe.MatchString(entry.Text):
		if !haveHit {
			return "", true, fmt.Sprintf("%s's %s answers a hit, and no character has hit %s this turn.", name, base, name)
		}
		if strings.Contains(low, "melee attack roll") && !hit.Melee {
			return "", true, fmt.Sprintf("%s's %s answers a melee hit; %s's attack was ranged.", name, base, charName)
		}
		n, _ := strconv.Atoi(reactACRe.FindStringSubmatch(entry.Text)[1])
		out = name + " uses " + base + "." + undoIfMiss(n, base)
		missed := strings.Contains(out, "now a miss")
		if m := reactRiposte.FindStringSubmatch(entry.Text); m != nil && missed && apply {
			if t, _, ok := resolveMonsterAttack(r, lobbyID, name, m[1], charName, true); ok {
				out += " " + t
			}
		}
		if strings.Contains(low, "teleports") {
			out += " It teleports up to 60 feet (move its token on the battle map)."
			for _, t := range targetIDs {
				addMonsterCondition(lobbyID, t, "blinded", base, 2, "", 0)
				out += fmt.Sprintf(" %s is Blinded until the end of %s's next turn.", creatureName(lobbyID, t), name)
			}
			if len(targetIDs) == 0 {
				out += " Creatures of its choice within 5 feet of where it arrives are Blinded until the end of its next turn: name them in target_ids."
			}
		}
	case strings.Contains(low, "casts counterspell or shield"):
		if !haveHit {
			return fmt.Sprintf("%s's %s: no character hit it this turn, so Shield has no trigger. For Counterspell use POST /api/gm/counterspell.", name, base), true, ""
		}
		addSpellEffect(lobbyID, id, id, "Shield", "shield_spell", "", 2, "", 0)
		out = name + " casts Shield (" + base + ")." + undoIfMiss(5, "Shield") + " +5 AC until the start of its next turn."
	case reactReduceRe.MatchString(entry.Text):
		if !haveHit || hit.Melee {
			return "", true, fmt.Sprintf("%s's %s answers a ranged hit, and none hit it this turn.", name, base)
		}
		dt := strings.ToLower(hit.DamageType)
		if dt != "bludgeoning" && dt != "piercing" && dt != "slashing" {
			return "", true, fmt.Sprintf("%s's %s reduces Bludgeoning, Piercing or Slashing damage; this hit was %s.", name, base, hit.DamageType)
		}
		m := reactReduceRe.FindStringSubmatch(entry.Text)
		cut := game.RollDamage(m[1], false)
		if m[2] != "" {
			k, _ := strconv.Atoi(m[2])
			cut += k
		}
		taken := hit.HPBefore - hit.HPAfter
		back := cut
		if back > taken {
			back = taken
		}
		setMonsterHP(lobbyID, id, hit.HPAfter+back)
		out = fmt.Sprintf("%s uses %s: the damage is reduced by %d (%d of %d taken back; it has %d HP).", name, base, cut, back, taken, hit.HPAfter+back)
		if back >= taken {
			out += " The damage is reduced to 0: it can redirect the force (the saving throw in its text; the GM resolves it with monster_action)."
		}
	case strings.Contains(low, "splits into two new"):
		out = splitMonster(lobbyID, id, name, base)
		if strings.HasPrefix(out, "!") {
			return "", true, strings.TrimPrefix(out, "!")
		}
	case strings.Contains(low, "swap places") && strings.Contains(low, "becomes the target"):
		if !haveHit {
			return "", true, fmt.Sprintf("%s's %s answers an attack roll against it, and no character's attack hit it this turn.", name, base)
		}
		ally := 0
		if len(targetIDs) > 0 && targetIDs[0] < 0 {
			ally = targetIDs[0]
		} else if ids := monsterIDsByKeyOrName(lobbyID, target); len(ids) > 0 && ids[0] != id {
			ally = ids[0]
		}
		if ally == 0 {
			return "", true, fmt.Sprintf("Name the ally that takes the attack (target: its name, or target_ids: [its id]); it must be a Small or Medium ally within 5 feet of %s.", name)
		}
		setMonsterHP(lobbyID, id, hit.HPBefore)
		lastHitOnMonster.Delete(key)
		dmg := hit.HPBefore - hit.HPAfter
		allyAC := 0
		for _, e := range combatEntries(lobbyID) {
			if asInt(e["id"]) == ally {
				allyAC = asInt(e["ac"]) + spellACAdjust(lobbyID, ally)
			}
		}
		out = fmt.Sprintf("%s uses %s: it and %s swap places (move the tokens on the battle map) and %s becomes the target.", name, base, creatureName(lobbyID, ally), creatureName(lobbyID, ally))
		if hit.Nat20 || hit.Total >= allyAC {
			t, _ := damageMonster(lobbyID, ally, dmg, strings.ToLower(hit.DamageType))
			out += fmt.Sprintf(" %s's attack (%d against AC %d) hits it: %s.", charName, hit.Total, allyAC, t)
		} else {
			out += fmt.Sprintf(" %s's attack (%d against AC %d) misses it.", charName, hit.Total, allyAC)
		}
	case strings.Contains(low, "the wearer gains a +5 bonus to ac"):
		wearer := 0
		if len(targetIDs) > 0 && targetIDs[0] > 0 {
			wearer = targetIDs[0]
		} else if cid, _, _, ok := characterByNameOrID(lobbyID, target); ok {
			wearer = cid
		}
		if wearer == 0 {
			return "", true, "Name the wearer of the amulet (target or target_ids)."
		}
		out = fmt.Sprintf("%s uses %s for %s:%s", name, base, getCharacterName(wearer), strings.Replace(shieldReaction(wearer), " Shield:", "", 1))
	case reactUsesRe.MatchString(entry.Text):
		act := reactUsesRe.FindStringSubmatch(entry.Text)[1]
		t, ok, why := resolveMonsterActionByName(r, lobbyID, name, act, target, targetIDs, apply, "")
		if why != "" {
			return "", true, why
		}
		if !ok {
			if t2, _, ok2 := resolveMonsterAttack(r, lobbyID, name, act, target, apply); ok2 {
				t = t2
			}
		}
		out = fmt.Sprintf("%s uses %s: %s.", name, base, act) + " " + t
	default:
		return "", false, ""
	}
	monsterReactionRound.Store(key, round)
	if perDayRe.MatchString(entry.Name) {
		n := 0
		if v, ok := reactionDayUses.Load(dayKey); ok {
			n = v.(int)
		}
		reactionDayUses.Store(dayKey, n+1)
	}
	return strings.TrimSpace(out), true, ""
}

// splitMonster: Split (Black Pudding, Ochre Jelly): while Large or Medium with 10+ Hit Points, two new creatures one size smaller share
// the Hit Points (round down) and act on its Initiative. A string starting with "!" is a refusal.
func splitMonster(lobbyID, id int, name, base string) string {
	var size string
	for _, e := range combatEntries(lobbyID) {
		if asInt(e["id"]) == id {
			size = sprintAny(e["size"])
			if size == "" || size == "<nil>" {
				db.QueryRow(`SELECT COALESCE(size,'') FROM monsters WHERE slug = $1`, sprintAny(e["monster_key"])).Scan(&size)
			}
		}
	}
	smaller := map[string]string{"large": "Medium", "medium": "Small"}[strings.ToLower(size)]
	if smaller == "" {
		return fmt.Sprintf("!%s can split only while Large or Medium (it is %s).", name, size)
	}
	tx, err := db.Begin()
	if err != nil {
		return "!database error"
	}
	defer tx.Rollback()
	var raw []byte
	if tx.QueryRow(`SELECT turn_order FROM combat_state WHERE lobby_id = $1 AND active = true FOR UPDATE`, lobbyID).Scan(&raw) != nil {
		return "!no active fight"
	}
	var entries []map[string]interface{}
	json.Unmarshal(raw, &entries)
	idx, minID := -1, 0
	for i, e := range entries {
		if asInt(e["id"]) == id {
			idx = i
		}
		if asInt(e["id"]) < minID {
			minID = asInt(e["id"])
		}
	}
	if idx < 0 {
		return "!not in the fight"
	}
	hp := asInt(entries[idx]["hp"])
	if hp < 10 {
		return fmt.Sprintf("!%s splits only with 10 or more Hit Points (it has %d).", name, hp)
	}
	half := hp / 2
	twin := map[string]interface{}{}
	for k, v := range entries[idx] {
		twin[k] = v
	}
	entries[idx]["hp"], entries[idx]["size"] = half, smaller
	twin["id"], twin["hp"], twin["size"], twin["name"] = minID-1, half, smaller, name+" (split)"
	if mh := asInt(entries[idx]["max_hp"]); mh > 0 {
		entries[idx]["max_hp"], twin["max_hp"] = maxInt(half, 1), maxInt(half, 1)
	}
	entries = append(entries[:idx+1], append([]map[string]interface{}{twin}, entries[idx+1:]...)...)
	b, _ := json.Marshal(entries)
	if _, err := tx.Exec(`UPDATE combat_state SET turn_order = $1 WHERE lobby_id = $2`, b, lobbyID); err != nil || tx.Commit() != nil {
		return "!database error"
	}
	return fmt.Sprintf("%s uses %s: it splits into two %s creatures of %d Hit Points each, %s and %s (id %d), both acting on its Initiative.",
		name, base, smaller, half, name, name+" (split)", minID-1)
}

// ---- saves: Advantage when fighting it; notes on Magic Resistance and Legendary Resistance

var fightingSaveAdvantage sync.Map // lobby -> monster id rolling a save with Advantage because the caster's side is fighting it

// fightingItAdvantage: Charm Person, Charm Monster, Dominate Person/Beast/Monster: "Advantage on the save if you or your allies are
// fighting it" (a creature in the fight).
func fightingItAdvantage(spellKey string) bool {
	switch spellKey {
	case "charm-person", "charm-monster", "dominate-person", "dominate-beast", "dominate-monster":
		return true
	}
	return false
}

type saveNote struct {
	at   time.Time
	text string
}

var pendingSaveNotes sync.Map // lobby -> []saveNote

func addSaveNote(lobbyID int, text string) {
	var list []saveNote
	if v, ok := pendingSaveNotes.Load(lobbyID); ok {
		list = v.([]saveNote)
	}
	pendingSaveNotes.Store(lobbyID, append(list, saveNote{time.Now(), text}))
}

// takeSaveNotes: the notes of the last few seconds (the action that is being answered).
func takeSaveNotes(lobbyID int) string {
	v, ok := pendingSaveNotes.LoadAndDelete(lobbyID)
	if !ok {
		return ""
	}
	out := ""
	for _, n := range v.([]saveNote) {
		if time.Since(n.at) < 15*time.Second {
			out += " " + n.text
		}
	}
	return out
}

func fightingAdvantageFor(lobbyID, id int) bool {
	v, ok := fightingSaveAdvantage.Load(lobbyID)
	return ok && v.(int) == id
}

// clearMonsterReactionState: monster ids are reused by the next fight, so their Reaction uses and last hits go with the fight.
func clearMonsterReactionState(lobbyID int) {
	prefix := fmt.Sprintf("%d:", lobbyID)
	for _, m := range []*sync.Map{&reactionDayUses, &lastHitOnMonster} {
		m.Range(func(k, _ interface{}) bool {
			if strings.HasPrefix(k.(string), prefix) {
				m.Delete(k)
			}
			return true
		})
	}
}
