package main

// A creature's special and forced movement (SRD 5.2.1 and the 2024 Monster Manual's movement wordings): one owner.
//
//   - Every move a creature makes by itself on the battle map is recorded for the current turn (recordCreatureMove): the GM's
//     map update (a token that moved without forced_movement), a player's path, a companion's path.
//   - A charge ("if the boar moved 20+ feet straight toward it immediately before the hit"): the attack roll owner asks
//     chargeAtRoll for every attack, which spends the recorded move: the first attack after a move gets the move's straight
//     approach toward its target (the distance it closed, when every foot it moved closed it), later attacks get none. Without a
//     battle map the server can't know the distance: the GM says it in the monster_action ("charged": true, chargeSaid), and the
//     reply says so when the GM didn't. hitChargeDesc turns the attack's text for this one hit into what applies (the extra
//     damage as one more amount of the hit, the condition as a plain rider, nothing when it didn't charge), so every route's
//     damage, rider and GM-note readers read the result; an alternative damage amount is the damage owner's condCharged.
//
// Monster names never appear here: each rule is read from the entry's wording.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// creatureMove: the last move a creature made by itself on the current turn (by the turn marker). In memory: a restart forgets it,
// which can only lose a charge (the GM can then say it), never invent one.
type creatureMove struct {
	Marker   string
	From, To mapSquare
	Size     int
	Feet     int  // the distance moved (each square of a path, or the straight line of a GM's token update)
	Said     bool // the GM said the creature charged (no battle map)
	Spent    bool // an attack roll came after it: "immediately before the hit" is over
}

type creatureMoveKey struct{ lobby, id int }

var creatureMoves sync.Map // creatureMoveKey -> creatureMove

// attackCharge: what the current attack of an attacker had of a charge: the straight approach toward its target in feet.
type attackCharge struct {
	Marker string
	Target int
	Feet   int
	Said   bool
}

var attackCharges sync.Map // creatureMoveKey -> attackCharge

// recordCreatureMove records a creature's own move on the battle map (not a forced one, not a teleport).
func recordCreatureMove(lobby, id int, from, to mapSquare, size, feet int) {
	if id == 0 || from == to || feet <= 0 {
		return
	}
	creatureMoves.Store(creatureMoveKey{lobby, id}, creatureMove{Marker: combatTurnMarker(lobby), From: from, To: to, Size: max(size, 1), Feet: feet})
}

// recordMapMoves records every token the GM's map update moved by itself (forced movement and teleports are not moves toward
// anything: the caller passes only an update without forced_movement).
func recordMapMoves(lobby int, before, after encounterMap) {
	for _, old := range before.Tokens {
		now, ok := after.token(old.ID)
		if !ok || now.Position == old.Position {
			continue
		}
		feet := battleMapStepFeet * max(mapAbs(now.Position.X-old.Position.X), mapAbs(now.Position.Y-old.Position.Y))
		recordCreatureMove(lobby, old.ID, old.Position, now.Position, old.Size, feet)
	}
}

// chargeSaidKey marks a monster_action whose GM said the monster charged (moved 20+ feet straight toward the target right before
// the attack): used only without a battle map.
type chargeSaidKey struct{}

func withChargeSaid(r *http.Request, said bool) *http.Request {
	if !said || r == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), chargeSaidKey{}, true))
}

// recordChargeSaid: the GM said this attacker charged; recorded as its move of this turn when there is no battle map (with one, the
// map's record is the truth and the reply says so).
func recordChargeSaid(r *http.Request, lobby, attacker int) string {
	if r == nil || attacker == 0 {
		return ""
	}
	if said, _ := r.Context().Value(chargeSaidKey{}).(bool); !said {
		return ""
	}
	if _, ok := activeEncounterMap(lobby); ok {
		return " [charged: there is a battle map, so the server reads the monster's move from it, not from charged.]"
	}
	creatureMoves.Store(creatureMoveKey{lobby, attacker}, creatureMove{Marker: combatTurnMarker(lobby), Said: true})
	return ""
}

// chargeAtRoll: called by the attack roll owner for every attack. The attacker's recorded move of this turn, if no attack came
// after it yet, is this attack's charge: the feet it closed toward the target when every foot it moved closed them.
func chargeAtRoll(lobby, attacker, target int) {
	if attacker == 0 || db == nil {
		return
	}
	k := creatureMoveKey{lobby, attacker}
	marker := combatTurnMarker(lobby)
	cur := attackCharge{Marker: marker, Target: target}
	if v, ok := creatureMoves.Load(k); ok {
		mv := v.(creatureMove)
		if mv.Marker == marker && !mv.Spent {
			mv.Spent = true
			creatureMoves.Store(k, mv)
			if mv.Said {
				cur.Feet, cur.Said = 1<<20, true
			} else if m, ok := activeEncounterMap(lobby); ok {
				if t, ok := m.token(target); ok {
					from := mapToken{Position: mv.From, Size: mv.Size}
					to := mapToken{Position: mv.To, Size: mv.Size}
					if closed := tokenDistanceFeet(from, t) - tokenDistanceFeet(to, t); closed > 0 && closed == mv.Feet {
						cur.Feet = closed
					}
				}
			}
		}
	}
	attackCharges.Store(k, cur)
}

// chargedFeet: how far the attacker's current attack (the last one rolled) came straight toward this target (0: none).
func chargedFeet(lobby, attacker, target int) int {
	if v, ok := attackCharges.Load(creatureMoveKey{lobby, attacker}); ok {
		c := v.(attackCharge)
		if c.Marker == combatTurnMarker(lobby) && c.Target == target {
			return c.Feet
		}
	}
	return 0
}

// The charge wordings: a sentence of its own ("If the target is a Medium or smaller creature and the boar moved 20+ feet straight
// toward it immediately before the hit, the target takes an extra 3 (1d6) Piercing damage." / "..., the target has the Prone
// condition.") and an alternative amount ("..., or 11 (2d8 + 2) Bludgeoning damage if the seahorse moved 20+ feet straight toward
// the target immediately before the hit.", the damage owner's condCharged).
var (
	chargeSentenceRe = regexp.MustCompile(`(?i)If (?:the )?target is (?:an? )?(Tiny|Small|Medium|Large|Huge|Gargantuan) or smaller(?: creature)? and (?:the|it) [\w' -]*?moved (\d+)\+? (?:feet|ft\.?) straight toward (?:it|the target|them) immediately before the hit, (?:the target|it) (?:takes an extra (\d+)(?: \(([^)]+)\))? (\w+) damage(?: and has the (\w+) condition)?|has the (\w+) condition)(?:, and ([^.]+))?\.`)
	chargeAltRe      = regexp.MustCompile(`(?i)^if (?:the|it) [\w' -]*?moved (\d+)\+? (?:feet|ft\.?) straight toward (?:it|the target|them) immediately before the hit$`)
	chargeAnyRe      = regexp.MustCompile(`(?i)moved (\d+)\+? (?:feet|ft\.?) straight toward`)
)

// chargeAltFeet: the distance an alternative amount's charge condition asks for (ok false: it is no charge condition).
func chargeAltFeet(cond string) (int, bool) {
	m := chargeAltRe.FindStringSubmatch(strings.TrimSpace(strings.Trim(strings.TrimSpace(cond), ".,;—–-")))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

// hitChargeDesc: the attack's text for this hit, with each charge sentence turned into what applies: the extra damage as one more
// amount of the Hit sentence ("plus 3 (1d6) Piercing damage"), the condition as a plain rider ("The target has the Prone
// condition."), nothing when the attacker didn't charge or the target is too big. note: what the GM is told (no battle map and
// no "charged").
func hitChargeDesc(lobby, attacker, target int, desc string) (string, string) {
	if !chargeAnyRe.MatchString(desc) {
		return desc, ""
	}
	_, hasMap := activeEncounterMap(lobby)
	return chargeRewrite(desc, chargedFeet(lobby, attacker, target), hasMap, func(size string) bool { return targetAtMostSize(lobby, target, size) })
}

// chargeRewrite is hitChargeDesc's reading of the text: feet is the attack's straight approach, fits says whether the target is of
// a size or smaller.
func chargeRewrite(desc string, feet int, hasMap bool, fits func(size string) bool) (string, string) {
	var notes []string
	unknown := func(n int) {
		if !hasMap && feet == 0 {
			notes = append(notes, fmt.Sprintf("its charge effect needs %d+ feet of movement straight toward the target right before the hit; there is no battle map, so send \"charged\": true in the monster_action when it did", n))
		}
	}
	extra := ""
	desc = chargeSentenceRe.ReplaceAllStringFunc(desc, func(s string) string {
		m := chargeSentenceRe.FindStringSubmatch(s)
		n, _ := strconv.Atoi(m[2])
		size := strings.ToLower(m[1])
		if feet < n || !fits(size) {
			unknown(n)
			return ""
		}
		out := ""
		if m[5] != "" { // the extra damage: one more amount of the hit
			if m[4] != "" {
				extra += fmt.Sprintf(" plus %s (%s) %s damage", m[3], m[4], m[5])
			} else {
				extra += fmt.Sprintf(" plus %s %s damage", m[3], m[5])
			}
		}
		if cond := m[6] + m[7]; cond != "" { // the condition: a plain rider the hit's condition reader gives
			out = fmt.Sprintf("The target has the %s condition.", titleCase(strings.ToLower(cond)))
		}
		if m[8] != "" { // what follows ("and the allosaurus can make one Bite attack against it"): the GM's, as its own sentence
			rest := strings.TrimSpace(m[8])
			out += " " + strings.ToUpper(rest[:1]) + rest[1:] + "."
		}
		return out
	})
	if extra != "" {
		if i := strings.Index(desc, "Hit:"); i >= 0 {
			if loc := hitSentenceEnd.FindStringIndex(desc[i:]); loc != nil {
				at := i + loc[0]
				desc = desc[:at] + extra + desc[at:]
			}
		}
	}
	// an alternative amount's charge (condCharged): the damage owner picks it; only the missing knowledge is the GM's
	for _, alt := range parseMonsterAttack("", desc).Alts {
		if alt.Cond == condCharged {
			if n, _ := chargeAltFeet(alt.Text); feet < n {
				unknown(n)
			}
		}
	}
	desc = strings.Join(strings.Fields(desc), " ")
	if len(notes) == 0 {
		return desc, ""
	}
	return desc, " [GM: " + strings.Join(notes, "; ") + ".]"
}

// targetAtMostSize: the target is of this size or smaller (a size the server doesn't know counts as Medium).
func targetAtMostSize(lobby, target int, size string) bool {
	max, ok := sizeOrder[size]
	if !ok {
		return true
	}
	theirs, ok := sizeOrder[strings.ToLower(strings.SplitN(creatureSize(lobby, target), " ", 2)[0])]
	if !ok {
		theirs = sizeOrder["medium"]
	}
	return theirs <= max
}
