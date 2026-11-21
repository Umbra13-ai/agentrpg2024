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
	"database/sql"
	"errors"
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
	AttackerName       string // a monster attacker (CharID 0): its name
	Total, AC          int
	Nat20, Melee       bool
	DamageType, Weapon string
	HPBefore, HPAfter  int
	// what the hit changed on its target, for taking it back (monster_hit_undo_2024.go)
	EntryBefore, EntryAfter map[string]interface{}
	NewRecords              []int
	GoneRecords             []conditionRecord
	NewEffects              []int
	RegenSetByHit           bool
	RecordsAfter            []int
	// the attack as rolled, for Redirect Attack (the ally becomes the target of the same attack roll): the damage BEFORE the first
	// target's defenses, and the stat-block text for its riders. Empty for a character's attack (its damage is built in attack_target.go).
	Parts      []hitDamagePart
	Crit       bool
	AttackerID int
	Desc       string
	RiderNames []string
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

// resolveMonsterReaction resolves a Reaction entry; handled false lets the generic text resolver take it (a saving throw reaction).
func resolveMonsterReaction(r *http.Request, lobbyID, id int, meta *monsterMeta, entry srdMonsterEntry, target string, targetIDs []int, apply bool) (text string, handled bool, refusal string) {
	name := creatureName(lobbyID, id)
	base := stripUsage(entry.Name)
	key := fmt.Sprintf("%d:%d", lobbyID, id)
	round := currentRound(lobbyID)
	if v, ok := monsterReactionRound.Load(key); ok && v.(int) == round {
		return "", true, fmt.Sprintf("%s has already used its Reaction this round (one a round).", name)
	}
	if cantTakeReactions(lobbyID, id) {
		return "", true, fmt.Sprintf("%s can't take Reactions until the end of its next turn (a hit or failed save gave it that); nothing was used.", name)
	}
	// the per-day count of a "(N/Day)" Reaction is the ledger's (monster_day_uses.go), checked by monsterAction before this runs
	low := strings.ToLower(entry.Text)
	hit, haveHit := thisTurnHitOnMonster(lobbyID, id)
	charName := ""
	if haveHit {
		charName = hit.AttackerName
		if charName == "" {
			charName = getCharacterName(hit.CharID)
		}
	}
	undoIfMiss := func(bonus int, label string) string {
		desc := fmt.Sprintf("%s's attack (%d against AC %d)", charName, hit.Total, hit.AC+bonus)
		if hit.Nat20 {
			return fmt.Sprintf(" %s: +%d AC, but %s was a natural 20: still a hit.", label, bonus, charName)
		}
		if hit.Total >= hit.AC+bonus {
			return fmt.Sprintf(" %s: +%d AC; %s still hits.", label, bonus, desc)
		}
		after, said, ok := undoRecordedHit(lobbyID, id, hit) // everything the hit changed (monster_hit_undo_2024.go)
		lastHitOnMonster.Delete(key)
		if !ok {
			return fmt.Sprintf(" %s: +%d AC; %s is now a miss.%s", label, bonus, desc, said)
		}
		return fmt.Sprintf(" %s: +%d AC; %s is now a miss, its damage undone (%s is back to %d HP).%s", label, bonus, desc, name, after.HP, said)
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
				applyCondition(lobbyID, t, "blinded", base, 2, "", 0)
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
		bv, av := monsterVitals(hit.EntryBefore), monsterVitals(hit.EntryAfter)
		taken := max(bv.HP-av.HP, 0) + max(bv.Temp-av.Temp, 0) // the hit's damage, Temporary Hit Points included
		if hit.EntryBefore == nil || hit.EntryAfter == nil {
			taken = hit.HPBefore - hit.HPAfter
		}
		back, after, ok := refundHitDamage(lobbyID, id, key, hit, cut) // monster_hit_undo_2024.go
		if !ok {
			return "", true, fmt.Sprintf("%s uses %s: RECONCILIATION NEEDED, the target's Hit Points or state changed after the hit, so the reduction (%d) is not applied by the server; the GM settles it.", name, base, cut)
		}
		out = fmt.Sprintf("%s uses %s: the damage is reduced by %d (%d of %d taken back; it has %d HP, %d Temporary).", name, base, cut, back, taken, after.HP, after.Temp)
		if taken > 0 && back >= taken {
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
		_, undone, _ := undoRecordedHit(lobbyID, id, hit) // the hit goes to the ally instead: nothing of it stays on this target (monster_hit_undo_2024.go)
		lastHitOnMonster.Delete(key)
		defer func() { text += undone }()
		allyAC := 0
		for _, e := range combatEntries(lobbyID) {
			if asInt(e["id"]) == ally {
				allyAC = asInt(e["ac"]) + spellACAdjust(lobbyID, ally)
			}
		}
		out = fmt.Sprintf("%s uses %s: it and %s swap places (move the tokens on the battle map) and %s becomes the target.", name, base, creatureName(lobbyID, ally), creatureName(lobbyID, ally))
		// The ally becomes the target of the SAME attack roll: it hits the ally's AC or it misses. The damage is a fresh result against the
		// ally's defenses (resistance, immunity, vulnerability, absorption, Temporary Hit Points), made from the damage rolled before the first
		// target's defenses, never the first target's already-reduced damage; the stat block's hit riders are given to the ally by its rules.
		if hit.Nat20 || hit.Total >= allyAC {
			out += fmt.Sprintf(" %s's attack (%d against AC %d) hits it.", charName, hit.Total, allyAC)
			switch {
			case len(hit.Parts) == 0 || (hit.Crit && !hit.Nat20):
				out += fmt.Sprintf(" [GM, not applied automatically: the damage is not carried over (%s); roll or apply it against %s's own defenses, and give %s the hit's riders%s.]",
					map[bool]string{true: "its critical hit came from the first target's state", false: "a character's attack builds its damage elsewhere"}[len(hit.Parts) > 0],
					creatureName(lobbyID, ally), creatureName(lobbyID, ally), riderListNote(hit.RiderNames))
			default:
				var texts []string
				for _, p := range hit.Parts {
					t, _ := damageMonster(lobbyID, ally, p.Amount, strings.ToLower(p.Type), hit.Crit)
					texts = append(texts, t)
				}
				out += " " + strings.Join(texts, " ") + "."
				if hit.Desc != "" && monsterAlive(lobbyID, ally) {
					if e, ok := parseHitRider(hit.Desc); ok {
						e.SourceID = hit.AttackerID
						if given := e.giveConditions(lobbyID, ally, charName+"'s "+hit.Weapon); len(given) > 0 {
							out += " [" + strings.Join(given, "; ") + "]"
						}
					}
				} else if len(hit.RiderNames) > 0 {
					out += fmt.Sprintf(" [GM: the hit's riders%s are not carried over.]", riderListNote(hit.RiderNames))
				}
			}
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
			monsterWearer := len(targetIDs) > 0 && targetIDs[0] < 0
			if !monsterWearer && target != "" {
				monsterWearer = len(monsterIDsByKeyOrName(lobbyID, target)) > 0
			}
			if monsterWearer { // the +5 AC and the hit take-back are built for a character wearer: a monster wearer is the GM's, never guessed
				return "", true, "The wearer is a monster: the +5 bonus to AC and any hit it would turn into a miss are not automated for a monster. Apply them by hand (the GM adjudicates); nothing was spent."
			}
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
				monsterQueryRow(`SELECT COALESCE(size,'') FROM monsters WHERE slug = $1`, sprintAny(e["monster_key"])).Scan(&size)
			}
		}
	}
	smaller := map[string]string{"large": "Medium", "medium": "Small"}[strings.ToLower(size)]
	if smaller == "" {
		return fmt.Sprintf("!%s can split only while Large or Medium (it is %s).", name, size)
	}
	// both halves in one change of the fight (combat_state_2024.go): the twin right after it, with an id the fight never gave before
	var half, twinID int
	var refusal string
	_, err := changeFight(lobbyID, func(f *fight) error {
		idx, e := f.entry(id)
		if e == nil {
			refusal = "!not in the fight"
			return errNotInFight
		}
		hp := asInt(e["hp"])
		if hp < 10 {
			refusal = fmt.Sprintf("!%s splits only with 10 or more Hit Points (it has %d).", name, hp)
			return errNoChange
		}
		half = hp / 2
		twin := copyEntry(e)
		e["hp"], e["size"] = half, smaller
		twinID = f.newMonsterID()
		twin["id"], twin["hp"], twin["size"], twin["name"] = twinID, half, smaller, name+" (split)"
		if mh := asInt(e["max_hp"]); mh > 0 {
			e["max_hp"], twin["max_hp"] = maxInt(half, 1), maxInt(half, 1)
		}
		f.insertAt(idx+1, twin)
		return nil
	})
	switch {
	case refusal != "":
		return refusal
	case errors.Is(err, sql.ErrNoRows):
		return "!no active fight"
	case err != nil:
		return "!database error"
	}
	return fmt.Sprintf("%s uses %s: it splits into two %s creatures of %d Hit Points each, %s and %s (id %d), both acting on its Initiative.",
		name, base, smaller, half, name, name+" (split)", twinID)
}

// ---- saves: Advantage when fighting it; notes on Magic Resistance and Legendary Resistance

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

// clearMonsterReactionState: monster ids are reused by the next fight, so their last hits go with the fight (a Reaction's per-day count is in the day ledger and stays).
func clearMonsterReactionState(lobbyID int) {
	prefix := fmt.Sprintf("%d:", lobbyID)
	for _, m := range []*sync.Map{&lastHitOnMonster} {
		m.Range(func(k, _ interface{}) bool {
			if strings.HasPrefix(k.(string), prefix) {
				m.Delete(k)
			}
			return true
		})
	}
}

// hitDamagePart is one damage roll of a hit, before the target's resistances, immunities, vulnerabilities, absorption and Temporary Hit Points.
type hitDamagePart struct {
	Amount int
	Type   string
}

func riderListNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return " (" + strings.Join(names, ", ") + ")"
}
