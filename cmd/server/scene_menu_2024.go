package main

// my-turn's short view (plan v2.1, S3; playtest S-CALLA-11, S-GM-18, S-ALDEN-CLARITY-01/04): what the character can do NOW in this scene
// (`now`), everything else it can legally send with the reason it isn't first (`more`), and the other things a player does outside a
// turn (`elsewhere`: rest, prepare spells, equip, shop, level-up choices, pick up). The menu orders and explains; it never refuses and
// never hides: now plus more is every type openTypes gives (the tests hold it).
//
// The scene is worked out once (sceneFacts) from what my-turn already knows; what a type needs of the scene is one row of actionNeeds;
// the entries are made from openTypes, the same filter my-turn's long lists read.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// what a type needs of the scene to be the right thing now
const (
	needFight = "fight" // in a fight
	needEnemy = "enemy" // an enemy to act against
	needHurt  = "hurt"  // someone living is below their Hit Point maximum
	needDying = "dying" // an ally at 0 Hit Points who is not stable
)

// sceneNames: the names the menu gives the types people pick between (one name per concept: the table's aliases stay searchable).
// search and study say which skills each is for; declare says what it is not for.
var sceneNames = map[string]string{
	"search":  "Look or notice (search: Insight, Medicine, Perception, Survival)",
	"study":   "Recall or work out (study: Arcana, History, Investigation, Nature, Religion)",
	"declare": "Do something else, or wait (declare; not for casting a spell or using a feature)",
}

// sceneFacts: what the scene is, worked out once for the menu.
type sceneFacts struct {
	InFight    bool
	EnemyIDs   []int // the ids to send as target_id (negative), in the order my-turn lists the enemies
	HurtAllyID []int // living characters below their maximum (not the player's own)
	DyingAlly  []int // characters at 0 Hit Points who are not stable
	SelfHurt   bool
	Label      string // the character's own health label (game.Vitals.Health)
	BeatRound  bool   // outside a fight the story action is the beat (schedule off) rather than the round
	Weapon     string // the weapon in the character's main hand, for the attack example ("" : none)
	Spell      string // a damaging spell the character has (a cantrip first), for the cast example ("": none)
}

// ownGear: the character's weapon in hand and a damaging spell it has, so the menu's examples are its own (S-WREN-1, I-THISTLE-5: a
// Wizard was shown sacred-flame and a longsword it didn't hold).
func (f *sceneFacts) ownGear(charID int) {
	var main string
	db.QueryRow(`SELECT COALESCE(equipped_main_hand,'') FROM characters WHERE id = $1`, charID).Scan(&main)
	if _, w, ok := findWeapon(main); ok {
		f.Weapon = strings.ToLower(w.Name)
	}
	best := ""
	for slug := range spellsHeld(charID) {
		sp, ok := srdSpellsMemory[slug]
		if !ok || (sp.DamageDice == "" && len(sp.DamageAtSlotLevel) == 0 && len(sp.DamageAtCharLevel) == 0) {
			continue
		}
		if best == "" || sp.Level < srdSpellsMemory[best].Level || (sp.Level == srdSpellsMemory[best].Level && slug < best) {
			best = slug
		}
	}
	f.Spell = best
}

// sceneFactsOf reads my-turn's own lists: enemies (each with "id"), party (each with "character_id", "hp", "max_hp", "status").
func sceneFactsOf(inFight bool, enemies, party []map[string]interface{}, hp, maxHP int, label string, beat bool) sceneFacts {
	f := sceneFacts{InFight: inFight, Label: label, SelfHurt: hp < maxHP && label != game.HealthDead, BeatRound: beat}
	for _, e := range enemies {
		f.EnemyIDs = append(f.EnemyIDs, asInt(e["id"]))
	}
	for _, p := range party {
		id := asInt(p["character_id"])
		st, _ := p["status"].(string)
		switch {
		case st == game.HealthDead:
		case st == game.HealthDying:
			f.DyingAlly = append(f.DyingAlly, id)
			f.HurtAllyID = append(f.HurtAllyID, id)
		case asInt(p["hp"]) < asInt(p["max_hp"]):
			f.HurtAllyID = append(f.HurtAllyID, id)
		}
	}
	return f
}

// lacks: "" when the scene has what the need asks for, else why the type isn't first.
func (f sceneFacts) lacks(need string) string {
	switch need {
	case needFight:
		if !f.InFight {
			return "in a fight only"
		}
	case needEnemy:
		if len(f.EnemyIDs) == 0 {
			if f.InFight {
				return "no enemy in sight to act against"
			}
			return "no enemy here: for a fight"
		}
	case needHurt:
		if !f.SelfHurt && len(f.HurtAllyID) == 0 {
			return "nobody is hurt"
		}
	case needDying:
		if len(f.DyingAlly) == 0 {
			return "no ally is at 0 Hit Points"
		}
	}
	return ""
}

var targetIDRe = regexp.MustCompile(`"target_id": -(\d+)`)

// realTarget: an example's placeholder target ("target_id": -1) made one this scene has: the first enemy, or the second (-2) when there is one.
func (f sceneFacts) realTarget(use string) string {
	if f.Spell != "" {
		use = strings.Replace(use, `"spell_slug": "sacred-flame"`, fmt.Sprintf(`"spell_slug": %q`, f.Spell), 1)
	}
	if f.Weapon != "" {
		use = strings.Replace(use, "my longsword", "my "+f.Weapon, 1)
	}
	if len(f.EnemyIDs) == 0 {
		return use
	}
	use = targetIDRe.ReplaceAllStringFunc(use, func(m string) string {
		n, _ := strconv.Atoi(targetIDRe.FindStringSubmatch(m)[1])
		n = max(n, 1)
		return fmt.Sprintf(`"target_id": %d`, f.EnemyIDs[min(n-1, len(f.EnemyIDs)-1)])
	})
	if len(f.DyingAlly) > 0 {
		use = strings.Replace(use, `"target_character_id": 12`, fmt.Sprintf(`"target_character_id": %d`, f.DyingAlly[0]), 1)
	}
	return use
}

// costWord: the part of the turn a type takes, as a player reads it.
func costWord(cost string) string {
	switch cost {
	case costAction:
		return "Action"
	case costBonus:
		return "Bonus Action"
	case costReaction:
		return "Reaction"
	case costMovement:
		return "movement"
	case costCompanion:
		return "your companion's action"
	}
	return "free"
}

// sceneMenu: the character's types for this scene, made from openTypes. A character at 0 Hit Points or dead has only what its state
// allows first (a Death Saving Throw, a feature that works while dying); everything else is in more with that reason.
func sceneMenu(charID int, f sceneFacts) (now, more []map[string]interface{}) {
	for _, o := range openTypes(charID) {
		why := f.lacks(o.t.Needs)
		switch {
		case f.Label == game.HealthDead:
			why = "your character has died"
		case game.AtZero(f.Label) && o.t.While != "dying" && o.t.Name != "death_save":
			why = "you are at 0 Hit Points"
		case o.t.Cost == costReaction && why == "":
			why = "a Reaction: when its trigger happens, not on your turn"
		case o.t.Cost == costCompanion && why == "":
			why = "your companion's own action: send it with companion_id"
		}
		name := o.t.Title
		if n, ok := sceneNames[o.t.Name]; ok {
			name = n
		}
		e := map[string]interface{}{"name": name, "action": o.t.Name, "cost": costWord(o.t.Cost)}
		if o.max > 0 {
			e["uses_left"], e["uses_max"] = o.left, o.max
		}
		if why != "" {
			e["why_not_first"] = why
			more = append(more, e)
			continue
		}
		e["use"] = f.realTarget(o.use)
		if o.t.Name == "declare" {
			e["or_wait"] = `{"action": "wait", "description": "I wait here and keep watch"}` // a wait is a sent turn: no stand-in is taken for it (S-CALLA-5)
		}
		if !f.InFight && !o.t.Beat && (o.t.Cost == costAction || o.t.Cost == costBonus) {
			if f.BeatRound {
				e["uses_story_action"] = "uses your story action this beat"
			} else {
				e["uses_story_action"] = "uses this round's turn"
			}
		}
		now = append(now, e)
	}
	if !f.InFight { // outside a fight the exploration and social types lead
		rank := func(m map[string]interface{}) int {
			if t, ok := lookupActionType(fmt.Sprint(m["action"])); ok && t.Explore > 0 {
				return t.Explore
			}
			return 1000
		}
		stableSortMaps(now, rank)
	}
	return now, more
}

// stableSortMaps: a stable sort of the entries by rank (the table's order among equals).
func stableSortMaps(list []map[string]interface{}, rank func(map[string]interface{}) int) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && rank(list[j]) < rank(list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// elsewhereTask: one thing a player does outside a turn. The request's effect under the turn schedule is read from routeEffects
// (effectOf), the one table of what a request is, never written here.
type elsewhereTask struct {
	Group, Title, What, Method, Path, Body string
	When                                   func(c elsewhereScene) bool
}

type elsewhereScene struct {
	CharID, Lobby int
	Class         string
	Level         int
	Caster        bool
	InFight       bool // a rest cannot be taken in a fight
	Schedule      bool
	ShopOpen      bool
	ChoicesOpen   bool
	LeftBehind    bool
	LongRestHours int64 // story hours until another Long Rest is allowed (0: allowed)
}

var elsewhereTasks = []elsewhereTask{
	{Group: "resting", Title: "Long Rest", Method: "POST", Path: "/api/characters/{id}/rest", Body: `{}`,
		What: "8 hours: all Hit Points, spell slots and class resources, and half your Hit Point Dice",
		When: func(c elsewhereScene) bool { return !c.InFight }},
	{Group: "resting", Title: "Short Rest", Method: "POST", Path: "/api/characters/{id}/short-rest", Body: `{"hit_dice": 1}`,
		What: "1 hour: spend Hit Point Dice to heal; some features come back",
		When: func(c elsewhereScene) bool { return !c.InFight }},
	{Group: "spells", Title: "Prepare spells", Method: "POST", Path: "/api/characters/{id}/prepare", Body: `{"spells": ["..."]}`,
		What: "change your prepared spells and cantrips (the rules say when: after a Long Rest, or on a level); GET the same path lists what you have and your limit",
		When: func(c elsewhereScene) bool { return c.Caster }},
	{Group: "gear", Title: "Draw or stow a weapon", Method: "POST", Path: "/api/characters/equip-weapon", Body: `{"character_id": {id}, "weapon": "<name>", "slot": "main_hand"}`,
		What: "put a weapon in a hand (POST /api/characters/unequip-weapon puts it away); GET /api/characters/{id}/equipment shows your hands"},
	{Group: "shopping", Title: "Buy or sell (a shop is open)", Method: "GET", Path: "/api/campaigns/{lobby}/shops", Body: "",
		What: "the GM has opened a shop: my-turn's shops_open lists it and the request that shows what it sells",
		When: func(c elsewhereScene) bool { return c.ShopOpen }},
	{Group: "choices", Title: "Finish your character's choices", Method: "GET", Path: "/api/characters/{id}/setup", Body: "",
		What: "a level-up or setup choice is still open: each step comes with the exact request to send",
		When: func(c elsewhereScene) bool { return c.ChoicesOpen }},
	{Group: "gear", Title: "Pick up what you left behind", Method: "POST", Path: "/api/characters/{id}/equipment/pick-up", Body: `{"dropped_id": 1}`,
		What: "left_behind lists the items and where they are; the GM can veto a pick-up that doesn't fit the story",
		When: func(c elsewhereScene) bool { return c.LeftBehind }},
}

// decidedBy: who settles a request, from its effect under the turn schedule (effectOf: round_effects_2024.go).
func decidedBy(method, path string, schedule bool) (decided string, usesTurn bool) {
	e, _, ok := effectOf(method, strings.ReplaceAll(strings.ReplaceAll(path, "{id}", "1"), "{lobby}", "1"))
	switch {
	case !ok || e.Effect == effectImmediate || method == "GET":
		return "you: applied at once", false
	case schedule:
		return "the GM: saved as your submission for the round and resolved by the GM", true
	default:
		return "you: applied at once, and the GM sees it in the log", e.Effect == effectSubmission
	}
}

// elsewhere: the tasks of this character outside its turn, each with its request, who decides and whether it uses the turn.
func elsewhere(c elsewhereScene) []map[string]interface{} {
	var out []map[string]interface{}
	for _, t := range elsewhereTasks {
		if t.When != nil && !t.When(c) {
			continue
		}
		path := strings.ReplaceAll(strings.ReplaceAll(t.Path, "{id}", fmt.Sprint(c.CharID)), "{lobby}", fmt.Sprint(c.Lobby))
		req := t.Method + " " + path
		if t.Body != "" {
			req += " " + strings.ReplaceAll(t.Body, "{id}", fmt.Sprint(c.CharID))
		}
		decided, uses := decidedBy(t.Method, t.Path, c.Schedule)
		e := map[string]interface{}{"group": t.Group, "title": t.Title, "request": req, "what": t.What, "decided_by": decided}
		if uses && c.Schedule {
			e["uses_turn"] = "uses this round's turn"
		}
		if t.Title == "Long Rest" {
			if c.LongRestHours > 0 {
				e["story_hours_until_allowed"] = c.LongRestHours
				e["note"] = fmt.Sprintf("you decide when your character rests; the story clock says %d more hours until another Long Rest is allowed (the GM passes time), and the GM sees the rest in the log", c.LongRestHours)
			} else {
				e["note"] = "you decide when your character rests; the story clock allows a Long Rest, and the GM sees it in the log"
			}
		}
		out = append(out, e)
	}
	return out
}
