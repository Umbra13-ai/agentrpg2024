package main

// A creature's special and forced movement (the SRD 5.2.1 movement wordings): one owner.
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
	"encoding/json"
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

// forceToken moves the target's token up to feet feet straight away from the mover (toward false) or straight toward it (toward
// true) on the encounter's battle map: the one forced movement of a token (a push, a pull, a Weapon Mastery's Push, a monster's
// hit). A wall, a closed door or another creature stops it; a pull stops next to the mover. It returns how many feet it moved and
// whether there was a map with both tokens; the map's revision changes so stale path submissions are refused.
func forceToken(lobbyID, moverID, targetID, feet int, toward bool) (int, bool) {
	var raw []byte
	if db.QueryRow(`SELECT battle_map FROM combat_state WHERE lobby_id = $1 AND active = true`, lobbyID).Scan(&raw) != nil || len(raw) == 0 {
		return 0, false
	}
	var m encounterMap
	if json.Unmarshal(raw, &m) != nil {
		return 0, false
	}
	var a, b *mapToken
	for i := range m.Tokens {
		switch m.Tokens[i].ID {
		case moverID:
			a = &m.Tokens[i]
		case targetID:
			b = &m.Tokens[i]
		}
	}
	if a == nil || b == nil {
		return 0, false
	}
	sign := func(n int) int {
		switch {
		case n > 0:
			return 1
		case n < 0:
			return -1
		}
		return 0
	}
	dx, dy := sign(b.Position.X-a.Position.X), sign(b.Position.Y-a.Position.Y)
	if toward {
		dx, dy = -dx, -dy
	}
	if dx == 0 && dy == 0 {
		return 0, true
	}
	moved := 0
	for step := 0; step < feet/battleMapStepFeet; step++ {
		if toward && tokenDistanceFeet(*a, *b) <= battleMapStepFeet {
			break // next to the creature that pulls it
		}
		next := mapSquare{b.Position.X + dx, b.Position.Y + dy}
		free := true
		for _, sq := range b.footprint(next) {
			if blockedTile(m.tile(sq)) {
				free = false
			}
			for i := range m.Tokens {
				if m.Tokens[i].ID == b.ID {
					continue
				}
				for _, o := range m.Tokens[i].footprint(m.Tokens[i].Position) {
					if o == sq {
						free = false
					}
				}
			}
		}
		if !free {
			break
		}
		b.Position = next
		moved += battleMapStepFeet
	}
	if moved > 0 {
		m.Revision = mapRevision()
		out, _ := json.Marshal(m)
		db.Exec(`UPDATE combat_state SET battle_map = $1 WHERE lobby_id = $2`, string(out), lobbyID)
	}
	return moved, true
}

// A stat-block hit that moves its target ("If the target is a Medium or smaller creature, the satyr pushes the target up to 10 feet
// straight away from itself." / "... pulls the target up to 15 feet straight toward itself." / "... the devil moves the target up
// to 30 feet straight toward itself."): applied by every hit route through hitAfterDamage; the GM's residual leaves it out.
var hitForcedMoveRe = regexp.MustCompile(`(?i)(?:If (?:the )?target is (?:an? )?(Tiny|Small|Medium|Large|Huge|Gargantuan) or smaller(?: creature)?, )?(?:the|it) [\w' -]*?(pushes|pulls|moves) the target (?:up to )?(\d+) (?:feet|ft\.?) straight (away from|toward) (?:itself|it|the [\w' -]+)(?:\.|$)`)

// stripForcedMoves: the text without the movement sentences hitForcedMove applies.
func stripForcedMoves(text string) string { return hitForcedMoveRe.ReplaceAllString(text, "") }

// hitForcedMove moves the target of a hit as the hit's text says, on the battle map, and answers the clause; without a map the GM
// is told the distance and places the creature.
func hitForcedMove(lobby, attacker, target int, desc string) string {
	if attacker == 0 {
		return ""
	}
	i := strings.Index(desc, "Hit:")
	if i < 0 {
		return ""
	}
	hit := desc[i:]
	if j := strings.Index(hit, "Hit or Miss:"); j >= 0 {
		hit = hit[:j]
	}
	var out []string
	for _, m := range hitForcedMoveRe.FindAllStringSubmatch(hit, -1) {
		if m[1] != "" && !targetAtMostSize(lobby, target, strings.ToLower(m[1])) {
			continue
		}
		feet, _ := strconv.Atoi(m[3])
		toward := strings.EqualFold(m[4], "toward")
		verb := map[bool]string{false: "pushed", true: "pulled"}[toward]
		name := creatureName(lobby, target)
		moved, onMap := forceToken(lobby, attacker, target, feet, toward)
		switch {
		case !onMap:
			out = append(out, fmt.Sprintf("%s is %s up to %d feet straight %s %s (no battle map: the GM places it)", name, verb, feet, strings.ToLower(m[4]), creatureName(lobby, attacker)))
		case moved == 0:
			out = append(out, fmt.Sprintf("%s can't be %s: something is in the way", name, verb))
		default:
			out = append(out, fmt.Sprintf("%s is %s %d feet straight %s %s", name, verb, moved, strings.ToLower(m[4]), creatureName(lobby, attacker)))
		}
	}
	if len(out) == 0 {
		return ""
	}
	return " [" + strings.Join(out, "; ") + "]"
}

// ---- an entry that pulls the creatures the monster holds (SRD 5.2.1 Roper Reel: "The roper pulls each creature Grappled by it up to 30
// feet straight toward it."; Ettercap Reel: "The ettercap pulls one creature within 30 feet of itself that is Restrained by its Web Strand
// up to 25 feet straight toward itself."). The held creatures are read from the condition owner's records (a Grappled or Restrained
// record this monster caused, by identity: SourceID, and for a named effect that record's source text); each is moved through the one
// forced movement owner (forceToken), a pull stops next to the monster; without a map the GM places it.
var entryPullRe = regexp.MustCompile(`(?i)^The [\w '’-]+? pulls (each|one) creature (?:within (\d+) feet of itself )?(?:that is )?(Grappled by it|Restrained by its ([A-Z][\w ’'-]*?)) up to (\d+) feet straight toward (?:it|itself)\.?$`)

// resolveMonsterPull answers a pulling entry (handled false: the text is another kind of entry).
func resolveMonsterPull(r *http.Request, lobby, monsterID int, entryName, text string, targets []int, apply bool) ([]string, bool) {
	m := entryPullRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return nil, false
	}
	cond, via := "grappled", ""
	if m[4] != "" {
		cond, via = "restrained", strings.ToLower(m[4])
	}
	feet, _ := strconv.Atoi(m[5])
	name := creatureName(lobby, monsterID)
	var held []int
	seen := map[int]bool{}
	for _, rec := range findConditionRecords(`lobby_id = $1 AND source_id = $2 AND condition = $3`, lobby, monsterID, cond) {
		if (via == "" || strings.Contains(strings.ToLower(rec.Source), via)) && !seen[rec.TargetID] {
			seen[rec.TargetID] = true
			held = append(held, rec.TargetID)
		}
	}
	if strings.EqualFold(m[1], "one") { // "one creature within N feet": the GM names it, and it must be held that way
		if len(targets) != 1 {
			return []string{fmt.Sprintf("%s: name the one creature it pulls (target). Nothing was moved.", entryName)}, true
		}
		if !seen[targets[0]] {
			return []string{fmt.Sprintf("%s isn't %s by %s's %s, so %s can't pull it. Nothing was moved.", creatureName(lobby, targets[0]), map[string]string{"grappled": "Grappled", "restrained": "Restrained"}[cond], name, via, entryName)}, true
		}
		if n, _ := strconv.Atoi(m[2]); n > 0 {
			if dist, onMap := monsterDistance(lobby, monsterID, targets[0]); onMap && dist > n {
				return []string{fmt.Sprintf("%s is %d feet away: %s reaches only %d feet. Nothing was moved.", creatureName(lobby, targets[0]), dist, entryName, n)}, true
			}
		}
		held = targets
	}
	if len(held) == 0 {
		return []string{fmt.Sprintf("%s uses %s: it holds no creature to pull.", name, entryName)}, true
	}
	var out []string
	for _, tid := range held {
		who := creatureName(lobby, tid)
		if !apply {
			out = append(out, fmt.Sprintf("%s would be pulled up to %d feet straight toward %s (not applied)", who, feet, name))
			continue
		}
		moved, onMap := forceToken(lobby, monsterID, tid, feet, true)
		switch {
		case !onMap:
			out = append(out, fmt.Sprintf("%s is pulled up to %d feet straight toward %s (no battle map: the GM places it)", who, feet, name))
		case moved == 0:
			out = append(out, fmt.Sprintf("%s can't be pulled further: it is already next to %s or something is in the way", who, name))
		default:
			out = append(out, fmt.Sprintf("%s is pulled %d feet straight toward %s", who, moved, name))
		}
	}
	if apply {
		markExecuted(r)
	}
	return []string{fmt.Sprintf("%s uses %s: %s.", name, entryName, strings.Join(out, "; "))}, true
}

// ---- a stat-block entry's own movement (Teleport, Tree Stride, Bubble Dash, Aquatic Charge, Rampage's move, Ink Cloud's move ...)
//
// When the GM uses such an entry (narrate's monster_action), the monster gets a move allowance for the turn, read from the entry's
// wording: how far (feet, or its Speed, half of it, its Swim, Fly, Climb or Burrow Speed), whether it teleports (no path, no
// Opportunity Attacks, an unoccupied space at the end), whether it provokes none ("without provoking Opportunity Attacks") and
// whether it goes straight toward an enemy (a charge then counts). The GM's next map update that moves the monster spends it
// (takeSpecialMoves): the move doesn't count against its Speed, a teleport or a move without Opportunity Attacks opens none, a
// teleport is not a move toward anything (no charge), and a move beyond the allowance is noted.

type specialMove struct {
	Marker    string
	Entry     string
	Feet      int
	Teleport  bool
	NoOA      bool
	Straight  bool
	SpeedCost int // a jump "by spending 10 feet of movement": what the move costs of its Speed (0: nothing)
}

var specialMoves sync.Map // creatureMoveKey -> specialMove

var (
	teleportUpToRe   = regexp.MustCompile(`(?i)\bteleports?(?:,? along with [^,.]+,)? up to (\d+) (?:feet|ft)`)
	teleportWithinRe = regexp.MustCompile(`(?i)\bteleports? to an unoccupied space within \d+ feet of [^.]*? within (\d+) feet of`)
	moveUpToRe       = regexp.MustCompile(`(?i)\b(?:moves?|swims?|flies|fly) up to (half )?(?:its|their) (Swim |Fly |Climb |Burrow )?Speed(?: or (Swim|Fly|Climb|Burrow) Speed)?`)
	jumpSpendRe      = regexp.MustCompile(`(?i)\bjumps? up to (\d+) (?:feet|ft\.?) by spending (\d+) (?:feet|ft\.?) of (?:its )?movement`)
	noOARe           = regexp.MustCompile(`(?i)without provoking Opportunity Attacks`)
	straightEnemyRe  = regexp.MustCompile(`(?i)straight toward an enemy`)
	// an entry usable only right after the creature damaged a creature that was already Bloodied (Rampage)
	afterBloodiedHitRe = regexp.MustCompile(`(?i)^\s*Immediately after dealing damage to a creature that (?:is|was) already Bloodied\b`)
)

// readSpecialMove reads an entry's movement (ok false: it moves nothing). speed answers a Speed of the monster by kind ("", "swim" ...).
func readSpecialMove(text string, speed func(kind string) int) (specialMove, bool) {
	mv := specialMove{NoOA: noOARe.MatchString(text), Straight: straightEnemyRe.MatchString(text)}
	switch {
	case jumpSpendRe.MatchString(text): // Leap: the jump is part of its move and costs what the text says
		m := jumpSpendRe.FindStringSubmatch(text)
		mv.Feet, _ = strconv.Atoi(m[1])
		mv.SpeedCost, _ = strconv.Atoi(m[2])
	case teleportUpToRe.MatchString(text):
		mv.Feet, _ = strconv.Atoi(teleportUpToRe.FindStringSubmatch(text)[1])
		mv.Teleport, mv.NoOA = true, true
	case teleportWithinRe.MatchString(text):
		mv.Feet, _ = strconv.Atoi(teleportWithinRe.FindStringSubmatch(text)[1])
		mv.Teleport, mv.NoOA = true, true
	case moveUpToRe.MatchString(text):
		m := moveUpToRe.FindStringSubmatch(text)
		mv.Feet = speed(strings.ToLower(strings.TrimSpace(m[2])))
		if m[3] != "" {
			mv.Feet = max(mv.Feet, speed(strings.ToLower(m[3])))
		}
		if m[1] != "" {
			mv.Feet /= 2
		}
	default:
		return specialMove{}, false
	}
	return mv, mv.Feet > 0
}

var speedKindRe = regexp.MustCompile(`(?i)\b(swim|fly|climb|burrow) (\d+) ft`)

// monsterSpeedOfKind: the monster's Speed of a kind ("" walking) from its stat block, 0 when it has none.
func monsterSpeedOfKind(lobby, id int, kind string) int {
	if kind == "" {
		s, _ := monsterWalkSpeed(id, lobby)
		return s
	}
	text := formSpeedText(lobby, id) // a Shape-Shift form's Speeds
	if text == "" {
		monsterQueryRow(`SELECT COALESCE(speed_text,'') FROM monsters WHERE slug = $1`, monsterKeyOfCombatant(lobby, id)).Scan(&text)
	}
	for _, m := range speedKindRe.FindAllStringSubmatch(text, -1) {
		if strings.EqualFold(m[1], kind) {
			n, _ := strconv.Atoi(m[2])
			return n
		}
	}
	return 0
}

// grantSpecialMove gives the monster its entry's move for this turn (apply) and answers the GM's note ("" when the entry moves
// nothing).
func grantSpecialMove(lobby, id int, entry, text string, apply bool) string {
	mv, ok := readSpecialMove(text, func(kind string) int { return monsterSpeedOfKind(lobby, id, kind) })
	if !ok {
		return ""
	}
	mv.Marker, mv.Entry = combatTurnMarker(lobby), stripUsage(entry)
	if apply {
		specialMoves.Store(creatureMoveKey{lobby, id}, mv)
	}
	how := fmt.Sprintf("up to %d ft", mv.Feet)
	switch {
	case mv.Teleport:
		how += ", teleporting (no path, no Opportunity Attacks; it ends in an unoccupied space)"
	case mv.NoOA:
		how += " without provoking Opportunity Attacks"
	}
	if mv.Straight {
		how += ", straight toward an enemy"
	}
	if mv.SpeedCost > 0 {
		how = fmt.Sprintf("a jump of up to %d ft costing %d ft of its Speed", mv.Feet, mv.SpeedCost)
	}
	if _, onMap := activeEncounterMap(lobby); !onMap {
		return fmt.Sprintf("Movement (%s): %s; there is no battle map, so the GM places it.", mv.Entry, how)
	}
	counted := "not counted against its Speed"
	if mv.SpeedCost > 0 {
		counted = "counted as its text says"
	}
	return fmt.Sprintf("Movement (%s): %s, "+counted+". Move its token with your next battle map update (PUT /api/campaigns/{id}/battle-map); the server applies this move to it.", mv.Entry, how)
}

// takeSpecialMoves spends the move allowances of the monsters this GM map update moved. It answers the ids whose move provokes no
// Opportunity Attacks, the ids that teleported, the ids whose move doesn't count against their Speed, and the notes.
func takeSpecialMoves(lobby int, before, after encounterMap) (noOA, teleported, notSpeed map[int]bool, notes []string) {
	noOA, teleported, notSpeed = map[int]bool{}, map[int]bool{}, map[int]bool{}
	marker := combatTurnMarker(lobby)
	for _, old := range before.Tokens {
		now, ok := after.token(old.ID)
		if !ok || (now.Position == old.Position && now.Altitude == old.Altitude) {
			continue
		}
		k := creatureMoveKey{lobby, old.ID}
		v, ok := specialMoves.Load(k)
		if !ok {
			continue
		}
		mv := v.(specialMove)
		specialMoves.Delete(k)
		if mv.Marker != marker {
			continue
		}
		notSpeed[old.ID] = true
		if mv.SpeedCost > 0 && currentTurnID(lobby) == old.ID { // a jump costs what its text says, not its distance
			addMonsterMoved(lobby, old.ID, mv.SpeedCost)
		}
		if mv.NoOA {
			noOA[old.ID] = true
		}
		if mv.Teleport {
			teleported[old.ID] = true
		}
		if feet := tokenDistanceFeet(old, mapToken{Position: now.Position, Size: old.Size, Altitude: now.Altitude}); feet > mv.Feet {
			notes = append(notes, fmt.Sprintf("%s moved %d ft with %s, which allows %d ft.", old.Name, feet, mv.Entry, mv.Feet))
		}
	}
	return
}

// lastHit: the last hit a creature made this turn, and whether its target was already Bloodied before it (Rampage's gate).
type lastHit struct {
	Marker         string
	Target         int
	BloodiedBefore bool
}

var lastHits sync.Map // creatureMoveKey -> lastHit

// recordLastHit: called by every hit route after its damage (hitAfterDamage): taken is what the hit took from the target.
func recordLastHit(lobby, attacker, target, taken int) {
	if attacker == 0 || taken <= 0 {
		return
	}
	hp, max := creatureHP(lobby, target)
	lastHits.Store(creatureMoveKey{lobby, attacker}, lastHit{Marker: combatTurnMarker(lobby), Target: target, BloodiedBefore: max > 0 && 2*(hp+taken) <= max})
}

// creatureHP: a creature's Hit Points and maximum (a character, a monster of the fight, a companion).
func creatureHP(lobby, id int) (hp, max int) {
	switch {
	case isCompanionCombatID(id):
		if c, ok := loadCompanion(id - companionIDBase); ok {
			return c.HP, c.MaxHP
		}
	case id > 0:
		db.QueryRow(`SELECT COALESCE(hp,0), COALESCE(max_hp,0) FROM characters WHERE id = $1`, id).Scan(&hp, &max)
	default:
		for _, e := range combatEntries(lobby) {
			if asInt(e["id"]) == id {
				v := monsterVitals(e)
				return v.HP, v.Max
			}
		}
	}
	return
}

// movementEntryRefusal: an entry the monster can't use now because of when it may be used (Rampage: right after it damaged a
// creature that was already Bloodied). "" when it can.
func movementEntryRefusal(lobby, id int, entry srdMonsterEntry) string {
	if !afterBloodiedHitRe.MatchString(entry.Text) {
		return ""
	}
	if v, ok := lastHits.Load(creatureMoveKey{lobby, id}); ok {
		if h := v.(lastHit); h.Marker == combatTurnMarker(lobby) && h.BloodiedBefore {
			return ""
		}
	}
	return fmt.Sprintf("%s's %s works only right after it deals damage to a creature that was already Bloodied (its last hit this turn wasn't one). Nothing was spent.", creatureName(lobby, id), stripUsage(entry.Name))
}

// withoutTeleports: the map update with the teleported tokens back where they were (a teleport is not a move toward anything).
func withoutTeleports(after, before encounterMap, teleported map[int]bool) encounterMap {
	out := after
	out.Tokens = append([]mapToken(nil), after.Tokens...)
	for i, tk := range out.Tokens {
		if teleported[tk.ID] {
			if old, ok := before.token(tk.ID); ok {
				out.Tokens[i].Position = old.Position
			}
		}
	}
	return out
}

// ---- dragging a creature it grapples (SRD 5.2.1, the Grappled condition: "the grappler can drag or carry you when it moves, but
// every foot of movement costs it 1 extra foot unless you are Tiny or two or more sizes smaller than it"). A trait that says it
// "needn't spend extra movement to move a creature it is grappling" (Abduct) removes the cost.
var noDragCostRe = regexp.MustCompile(`(?i)needn['’]t spend extra movement to move a creature it is grappling`)

// dragCost: the creatures the monster dragged in this map update (a creature it grapples whose token moved with it by the same
// displacement) that make its movement cost double, and why ("" when none).
func dragCost(lobby, id int, before, after encounterMap) (double bool, why string) {
	old, ok1 := before.token(id)
	now, ok2 := after.token(id)
	if !ok1 || !ok2 || old.Position == now.Position {
		return false, ""
	}
	dx, dy := now.Position.X-old.Position.X, now.Position.Y-old.Position.Y
	mine := sizeOrder[strings.SplitN(creatureSize(lobby, id), " ", 2)[0]]
	for _, r := range findConditionRecords(`lobby_id = $1 AND condition = 'grappled'`, lobby) {
		if !grappledBy(r, id) {
			continue
		}
		tOld, okA := before.token(r.TargetID)
		tNew, okB := after.token(r.TargetID)
		if !okA || !okB || tNew.Position.X-tOld.Position.X != dx || tNew.Position.Y-tOld.Position.Y != dy {
			continue
		}
		theirs := sizeOrder[strings.SplitN(creatureSize(lobby, r.TargetID), " ", 2)[0]]
		if theirs == 0 || mine-theirs >= 2 {
			continue
		}
		if monsterTraitText(lobby, id, noDragCostRe) != "" {
			return false, ""
		}
		return true, fmt.Sprintf("it drags %s, whom it grapples (each foot costs 2)", tNew.Name)
	}
	return false, ""
}

// ---- falling damage: fallDamageDice d6 Bludgeoning, the one rule of a fall's dice (the GM's falling damage, gm_environment.go).
// fallDamageDice: how many d6 a fall of this many feet deals (1d6 per 10 feet, at least 1 for any fall, at most 20).
func fallDamageDice(feet int) int {
	return min(max(feet/10, 1), 20)
}
