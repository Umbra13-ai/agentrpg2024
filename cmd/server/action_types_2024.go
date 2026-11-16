package main

// The action types a player can send (HANDOFF items 19 and 29). The server had one list of costs (getActionResourceType's switch),
// another of the words it resolves (knownActionWord), a hand-written table on /commands, a list in skill.md, a list in the API
// reference, my-turn's options and a catch-all "other" that cost nothing and resolved nothing (live, "Tyranny of Dragons": a
// Cleric's Sacred Flame and Divine Spark were sent as "other" because divine_spark was missing from the documented list). Most of
// the 148 "other" turns there were whole in-character turns outside a fight for the GM to narrate, so that stays (Matt, Nov 16):
// "declare" (aliases "other", "free") is described for the GM and costs the action in a fight, and declarationProblem refuses a
// declaration that casts a spell or uses a feature, which have actions of their own.
//
// actionTypes is the one definition. Everything reads it: the request's check (unknown words, "other", a Ready's held action, a
// pending turn's steps, a companion's action), the costs (getActionResourceType), the feature-word group (featureAction), what
// Gaseous Form and an Antimagic Field stop, my-turn's options, the refusal's list of what the character can do, the commands page
// (/commands), the API reference (tools/build_swagger.py reads this file) and skill.md's action list (between its action markers;
// ACTIONS_UPDATE=1 go test ./cmd/server -run TestActionTypesInSkillMd rewrites it). TestActionDispatchIsTheTable checks that the
// dispatch handles exactly these types and TestNoOtherActionWordList that no other list of them is kept by hand.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/agentrpg/agentrpg/game"
)

// The parts of a turn an action type can take (getActionResourceType's answers).
const (
	costAction    = "action"
	costBonus     = "bonus_action"
	costReaction  = "reaction"
	costMovement  = "movement"
	costFree      = "free"      // no action: part of something else, or the free things a turn includes
	costCompanion = "companion" // the companion's own action, taken on your turn: none of yours
)

// costLabels: how the docs name each cost.
var costLabels = map[string]string{
	costAction: "Action", costBonus: "Bonus Action", costReaction: "Reaction", costMovement: "Movement", costFree: "No action",
	costCompanion: "The companion's action (on your turn)",
}

// Where a type is sent (actionType.Send).
const (
	sendAction  = ""          // POST /api/action (and a pending turn's step)
	sendPending = "pending"   // only as a step of a pending turn (POST /api/pending-turn)
	sendRoute   = "route"     // its own request (Route)
	sendFamily  = "companion" // POST /api/action with companion_id (a companion's own action)
)

// The sections the docs group the types in, in order.
var actionSections = []string{
	"SRD actions",
	"In your own words",
	"Moving and the free parts of a turn",
	"Class features",
	"Feats and Epic Boons",
	"Magic items",
	"Spells' own actions",
	"Companions",
}

// actionWho: who has an action type. Empty fields don't limit it; a type with no limit is everyone's.
type actionWho struct {
	Class      string // a class key ("cleric")
	Subclass   string // a subclass slug ("life", "open-hand")
	Level      int    // the class level it comes at
	Feat       string // a feat or Epic Boon slug
	Invocation string // an Eldritch Invocation slug
	Item       string // a magic item the character holds in a hand and is attuned to (its name)
	Effect     string // a spell effect on the character (charBuff kind)
	Companion  string // a companion of the character: "any", "familiar", "draconic-spirit", or a steed's kind ("celestial")
	Choice     string // a subclass choice it needs, "key=value" (Hunter's Prey: hunters_prey=horde_breaker)
}

// actionType: one type of action.
type actionType struct {
	Name    string   // the word sent as "action"
	Aliases []string // other words for it: the SRD 2024 names (Magic is cast, Utilize is use_item) and older spellings
	Title   string   // the name a player reads (my-turn, the commands page)
	Section string   // one of actionSections
	Cost    string   // one of the cost constants; a cast takes what the spell's casting time says
	Fields  []string // the request fields it reads besides "action" (actionFields' JSON names; a route: that request's own)
	What    string   // one line: what it does
	Example string   // a request body (a route: "METHOD /path {body}")
	GM      bool     // the server records the description and the GM rules on it (nothing is rolled); a description is required
	Who     actionWho
	Group   string   // the feature-word group (feature_words_2024.go): attack, cast or use_item
	Tags    []string // what kind of thing it is, for the rules that stop some kinds: attack, spell, object, talk
	Send    string   // where it is sent: sendAction, sendPending, sendRoute or sendFamily
	Route   string   // sendRoute: the request
	Family  bool     // a companion can take it too (POST /api/action with companion_id)
	Beat    bool     // outside a fight it doesn't use up the exploration beat (the GM narrates between actions)
	SRD     bool     // one of the SRD's twelve actions (Attack, Dash ... Utilize), named by the turn reminder
	Uses    string   // the class resource it spends, shown as uses left (my-turn)
	While   string   // shown only while: raging, not_raging, wild_shaped, prone, dying, burning, inspired (holds a Bardic Inspiration die)
}

func (t actionType) takes(field string) bool { return containsStr(t.Fields, field) }
func (t actionType) hasTag(tag string) bool  { return containsStr(t.Tags, tag) }

// sentToAction: the type is sent to POST /api/action (by the character, or by a companion of its).
func (t actionType) sentToAction() bool { return t.Send == sendAction || t.Send == sendFamily }

var actionTypes = []actionType{
	// ---------------------------------------------------------------- the SRD's actions
	{Name: "attack", SRD: true, Title: "Attack", Section: "SRD actions", Cost: costAction, Group: "attack", Tags: []string{"attack"}, Family: true,
		Fields:  []string{"target_id", "target", "object_id", "cleave_target_id", "description", "close_range", "ally_adjacent", "features"},
		What:    "Attack with your equipped weapon or an Unarmed Strike: target_id is an enemy (situation.enemies in GET /api/my-turn), object_id an object such as a door. The server rolls and answers hit or miss. With Extra Attack each request is one attack of the Attack action. Improvised weapons, Alchemist's Fire and Acid flasks; \"knock out\" for a nonlethal blow. Words of magic weapons go in the description (\"luck\", \"hurl\", \"2 to AC\").",
		Example: `{"action": "attack", "target_id": -1, "description": "I swing my longsword at the goblin"}`},
	{Name: "cast", SRD: true, Aliases: []string{"magic"}, Title: "Magic (cast a spell)", Section: "SRD actions", Cost: costAction, Group: "cast", Tags: []string{"spell"},
		Fields:  []string{"spell_slug", "slot_level", "target_id", "target_ids", "target", "item", "charges", "description", "features"},
		What:    "The Magic action: cast a spell you have (prepared, a cantrip, or given by a feature, your origin or an item). It takes what its casting time says: a Bonus Action spell the Bonus Action, a Reaction spell (Shield, Counterspell) the Reaction. spell_slug is the exact slug (GET /api/universe/spells), slot_level upcasts, target_id or target_ids aim it; item casts from a wand, staff or Spell Scroll.",
		Example: `{"action": "cast", "spell_slug": "sacred-flame", "target_id": -1}`},
	{Name: "dash", SRD: true, Title: "Dash", Section: "SRD actions", Cost: costAction, Family: true, Fields: []string{"description"},
		What:    "Extra movement equal to your Speed this turn. With Expeditious Retreat it is a Bonus Action (\"bonus\" in the description).",
		Example: `{"action": "dash"}`},
	{Name: "disengage", SRD: true, Title: "Disengage", Section: "SRD actions", Cost: costAction, Family: true, Fields: []string{"description"},
		What:    "Your movement doesn't provoke Opportunity Attacks for the rest of the turn.",
		Example: `{"action": "disengage"}`},
	{Name: "dodge", SRD: true, Title: "Dodge", Section: "SRD actions", Cost: costAction, Family: true, Fields: []string{"description"},
		What:    "Until the start of your next turn, attack rolls against you have Disadvantage and you have Advantage on Dexterity saving throws.",
		Example: `{"action": "dodge"}`},
	{Name: "help", SRD: true, Title: "Help", Section: "SRD actions", Cost: costAction, Family: true, Fields: []string{"target_id", "target_character_id", "description"},
		What:    "Distract an enemy within 5 feet (target_id): the next attack roll by one of your allies against it has Advantage. Or help an ally (target_character_id) with one of your skill or tool proficiencies (named in the description): Advantage on its next check with it. Until the start of your next turn.",
		Example: `{"action": "help", "target_id": -1, "description": "I feint so Aldren can strike"}`},
	{Name: "hide", SRD: true, Title: "Hide", Section: "SRD actions", Cost: costAction, Family: true, Fields: []string{"description"},
		What:    "A DC 15 Dexterity (Stealth) check while Heavily Obscured or behind Three-Quarters or Total Cover; on a success you have the Invisible condition until you're found (the GM can overrule where there is nowhere to hide).",
		Example: `{"action": "hide", "description": "I slip behind the wagon"}`},
	{Name: "influence", SRD: true, Title: "Influence", Section: "SRD actions", Cost: costAction, Fields: []string{"target_id", "description"},
		What:    "Urge a creature to do something: Charisma (Deception, Intimidation, Performance or Persuasion, the default) or Wisdom (Animal Handling). The server rolls; the creature's attitude gives Advantage or Disadvantage; the GM says how it answers. Name the creature in the description or with target_id.",
		Example: `{"action": "influence", "description": "I persuade the guard to let us pass"}`},
	{Name: "ready", SRD: true, Title: "Ready", Section: "SRD actions", Cost: costAction, Fields: []string{"description"},
		What:    "Hold an action for a trigger: \"trigger: ...; action: ...\", where the action is one of these action types (attack, cast, dash, dodge, help, hide, use_item ...) or move. When the trigger happens, POST /api/trigger-readied takes it with your Reaction.",
		Example: `{"action": "ready", "description": "trigger: the first goblin comes through the door; action: attack it with my spear"}`},
	{Name: "search", SRD: true, Title: "Search", Section: "SRD actions", Cost: costAction, Family: true, Fields: []string{"description"},
		What:    "A Wisdom check to discern something: Insight (a creature's state of mind), Medicine (an ailment or cause of death), Perception (a concealed creature or object, the default) or Survival (tracks or food). The server rolls; the GM says what you find.",
		Example: `{"action": "search", "description": "I search the room for hidden doors"}`},
	{Name: "study", SRD: true, Title: "Study", Section: "SRD actions", Cost: costAction, Fields: []string{"description"},
		What:    "An Intelligence check to recall or work something out: Arcana, History, Investigation (the default), Nature or Religion. The server rolls; the GM says what you learn.",
		Example: `{"action": "study", "description": "I study the runes on the door"}`},
	{Name: "use_item", SRD: true, Aliases: []string{"utilize", "drink", "drink_potion", "quaff"}, Title: "Utilize (use an object)", Section: "SRD actions", Cost: costAction, Group: "use_item", Tags: []string{"object"},
		Fields:  []string{"item", "target_character_id", "target_id", "target_ids", "charges", "description"},
		What:    "The Utilize action: use an object that needs an action. With item, the server resolves potions (drinking one, or giving it to target_character_id within 5 feet, is a Bonus Action) and magic items (the Legendary items' words are in skill.md). Anything else (pull a lever, force a stuck door, light a fuse) is described and the GM rules on it. A Spell Scroll or a wand is read with cast and item.",
		Example: `{"action": "use_item", "item": "Potion of Healing"}`},
	{Name: "grapple", Title: "Grapple", Section: "SRD actions", Cost: costAction, Tags: []string{"attack"}, Fields: []string{"target_id", "description"},
		What:    "An Unarmed Strike option, one attack of the Attack action: the target (no more than one size larger, within 5 feet) makes a Strength or Dexterity save against 8 + your Strength modifier + Proficiency Bonus or has the Grappled condition.",
		Example: `{"action": "grapple", "target_id": -2}`},
	{Name: "shove", Title: "Shove", Section: "SRD actions", Cost: costAction, Tags: []string{"attack"}, Fields: []string{"target_id", "description"},
		What:    "An Unarmed Strike option, one attack of the Attack action: the target saves (as for a grapple) or is pushed 5 feet or knocked Prone (\"prone\" or \"push\" in the description).",
		Example: `{"action": "shove", "target_id": -2, "description": "prone"}`},
	{Name: "stabilize", Title: "Stabilize (first aid)", Section: "SRD actions", Cost: costAction, Fields: []string{"target_character_id", "description"},
		What:    "A DC 10 Wisdom (Medicine) check on a dying ally at 0 Hit Points (target_character_id); on a success it is Stable.",
		Example: `{"action": "stabilize", "target_character_id": 12}`},
	{Name: "opportunity_attack", Title: "Opportunity Attack", Section: "SRD actions", Cost: costReaction, Group: "attack", Tags: []string{"attack"}, Fields: []string{"target_id", "spell_slug", "description"},
		What:    "One melee attack with your Reaction when an enemy leaves your reach (GET /api/my-turn says when you can). With the War Caster feat, spell_slug casts a spell at it instead (Reactive Spell).",
		Example: `{"action": "opportunity_attack", "target_id": -1}`},

	// ---------------------------------------------------------------- moving, talking and the free parts of a turn
	{Name: "declare", Aliases: []string{"other", "free"}, Title: "Declare (your own words, the GM rules)", Section: "In your own words", Cost: costAction, GM: true, Fields: []string{"description"},
		What:    "What your character does, in your own words, when no other action type fits: leading the group along a wall, a whispered plan, waiting at a corner, scouting a route. The GM rules on it and narrates; nothing is rolled. Outside a fight it is your turn's story action (with the turn schedule, saved for the GM). In a fight it takes your action (say \"bonus action\" in the description for your Bonus Action) and the GM decides what it achieves. A spell is cast with cast and a feature with its own action: a declaration that casts a spell or uses a feature or its resource (a spell slot, Channel Divinity, Focus Points ...) is refused with the request to send.",
		Example: `{"action": "declare", "description": "I take point along the wall at the children's pace and stop at the last cover before the crossing"}`},
	{Name: "move", Title: "Move", Section: "Moving and the free parts of a turn", Cost: costMovement, Family: true, Fields: []string{"movement_cost", "toward_frightened_source", "description"},
		What:    "Move without a battle map: movement_cost is the feet you use (crawling while Prone costs double). On a battle map use POST /api/campaigns/{id}/battle-map/move instead.",
		Example: `{"action": "move", "movement_cost": 30, "description": "I run to the pillar"}`},
	{Name: "map_move", Title: "Move on the battle map (a pending turn's step)", Section: "Moving and the free parts of a turn", Cost: costMovement, Send: sendPending, Fields: []string{"path", "altitude"},
		What:    "Only as a step of a pending turn (POST /api/pending-turn): the squares you move through in order, as POST /api/campaigns/{id}/battle-map/move takes them.",
		Example: `{"action": "map_move", "path": [{"x": 3, "y": 4}, {"x": 4, "y": 4}]}`},
	{Name: "stand", Title: "Stand up", Section: "Moving and the free parts of a turn", Cost: costMovement, While: "prone",
		What:    "Stand up from Prone: it costs half your Speed.",
		Example: `{"action": "stand"}`},
	{Name: "jump", Title: "Jump", Section: "Moving and the free parts of a turn", Cost: costFree, Fields: []string{"description"},
		What:    "A Long Jump (up to your Strength score in feet) or a High Jump (3 + your Strength modifier), paid from your movement; \"standing\" halves it. The Jump spell: 30 feet for 10 feet of movement.",
		Example: `{"action": "jump", "description": "long jump over the chasm"}`},
	{Name: "speak", Title: "Speak (in character)", Section: "Moving and the free parts of a turn", Cost: costFree, GM: true, Beat: true, Tags: []string{"talk"}, Fields: []string{"description"},
		What:    "Your character's own words, said aloud in the scene: a warning, a taunt, an answer to an NPC. Free (brief speech takes no action) and logged as your character's words for the GM and the party. Persuading someone is Influence; talking out of character is the party chat (POST /api/campaigns/messages).",
		Example: `{"action": "speak", "description": "\"Stay behind me, Millie!\" I shout over the din."}`},
	{Name: "interact", Title: "Interact with one object", Section: "Moving and the free parts of a turn", Cost: costFree, GM: true, Beat: true, Tags: []string{"object"}, Fields: []string{"description"},
		What:    "The one object interaction a turn includes, as part of your movement or action: open a door, pick up a dropped weapon, hand an item to an ally, draw something from a pack. Free; the GM rules on it. A second object, or one that needs an action, is Utilize (use_item).",
		Example: `{"action": "interact", "description": "I open the door to the vestry"}`},
	{Name: "drop", Title: "Drop something", Section: "Moving and the free parts of a turn", Cost: costFree, GM: true, Beat: true, Tags: []string{"object"}, Fields: []string{"description"},
		What:    "Let go of something you hold (no action). Recorded for the GM; to leave an item of your inventory on the ground use POST /api/characters/{id}/equipment/drop.",
		Example: `{"action": "drop", "description": "I drop the torch"}`},
	{Name: "extinguish", Title: "Put out the fire on you", Section: "Moving and the free parts of a turn", Cost: costAction, While: "burning",
		What:    "You are burning: drop Prone and roll to put the fire out.",
		Example: `{"action": "extinguish"}`},
	{Name: "death_save", Title: "Death Saving Throw", Section: "Moving and the free parts of a turn", Cost: costFree, Beat: true, While: "dying",
		What:    "At 0 Hit Points and not Stable: the Death Saving Throw takes no action and is made once a turn, at its start. In a fight the server rolls it when your turn starts; send it yourself only where no fight's turn has rolled it.",
		Example: `{"action": "death_save"}`},
	{Name: "concentration_check", Title: "Concentration check", Section: "Moving and the free parts of a turn", Cost: costFree, Fields: []string{"description"},
		What:    "Roll a Constitution save to keep Concentration after damage the server didn't apply itself (the damage taken first in the description). Damage the server applies rolls it for you.",
		Example: `{"action": "concentration_check", "description": "14 damage from the falling rocks"}`},
	{Name: "offhand_attack", Title: "Light weapon's extra attack", Section: "Moving and the free parts of a turn", Cost: costBonus, Group: "attack", Tags: []string{"attack"}, Fields: []string{"target_id", "description"},
		What:    "After you take the Attack action with a Light weapon, one attack with a different Light weapon in your other hand (no ability modifier to its damage unless negative). With the Nick mastery it is part of the Attack action instead.",
		Example: `{"action": "offhand_attack", "target_id": -1, "description": "dagger"}`},
	{Name: "use_bardic_inspiration", Title: "Use a Bardic Inspiration die", Section: "Moving and the free parts of a turn", Cost: costFree, While: "inspired", Fields: []string{"description"},
		What:    "Add the Bardic Inspiration die a Bard gave you to a D20 Test you failed: the total and the DC in the description.",
		Example: `{"action": "use_bardic_inspiration", "description": "total 11 dc 14"}`},

	// ---------------------------------------------------------------- class features
	{Name: "rage", Title: "Rage", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "barbarian", Level: 1}, Uses: "rage", While: "not_raging",
		What:    "Enter your Rage (not in Heavy armor): Resistance to Bludgeoning, Piercing and Slashing damage, Advantage on Strength checks and saves, the Rage Damage bonus on Strength attacks.",
		Example: `{"action": "rage"}`},
	{Name: "extend_rage", Title: "Extend your Rage", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "barbarian", Level: 1}, While: "raging",
		What:    "Keep your Rage going until the end of your next turn (attacking an enemy or forcing a save extends it too).",
		Example: `{"action": "extend_rage"}`},
	{Name: "frenzy", Aliases: []string{"frenzy_attack"}, Title: "Frenzy (how it works)", Section: "Class features", Cost: costFree, Who: actionWho{Class: "barbarian", Subclass: "berserker", Level: 3},
		What:    "Explains the 2024 Frenzy: while raging, a Reckless Attack's first Strength hit on your turn deals extra d6s equal to your Rage Damage (write \"reckless\" in the attack). There is no extra attack.",
		Example: `{"action": "frenzy"}`},
	{Name: "bardic_inspiration", Title: "Bardic Inspiration", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "bard", Level: 1}, Uses: "bardic_inspiration", Fields: []string{"target_id", "target_ids", "description"},
		What:    "Give an ally within 60 feet a Bardic Inspiration die to add to a D20 Test it fails (name the ally in the description).",
		Example: `{"action": "bardic_inspiration", "description": "on Aldren"}`},
	{Name: "countercharm", Title: "Countercharm", Section: "Class features", Cost: costReaction, Who: actionWho{Class: "bard", Level: 7}, Fields: []string{"description"},
		What:    "When you or a creature within 30 feet fails a save against being Charmed or Frightened, it rerolls with Advantage: the save's bonus and DC in the description.",
		Example: `{"action": "countercharm", "description": "bonus 2 dc 14"}`},
	{Name: "turn_undead", Title: "Turn Undead (Channel Divinity)", Section: "Class features", Cost: costAction, Who: actionWho{Class: "cleric", Level: 2}, Uses: "channel_divinity", Fields: []string{"target_ids", "target_id", "description"},
		What:    "A Magic action: each Undead you choose within 30 feet (target_ids) makes a Wisdom save or is Frightened and Incapacitated for 1 minute.",
		Example: `{"action": "turn_undead", "target_ids": [-1, -2]}`},
	{Name: "divine_spark", Title: "Divine Spark (Channel Divinity)", Section: "Class features", Cost: costAction, Who: actionWho{Class: "cleric", Level: 2}, Uses: "channel_divinity", Fields: []string{"target_id", "description"},
		What:    "A Magic action: 1d8 + your Wisdom modifier (more d8s at levels 7, 13 and 18) to a creature within 30 feet: healing for an ally, or Radiant or Necrotic damage to an enemy (Constitution save for half). Say heal, radiant or necrotic in the description.",
		Example: `{"action": "divine_spark", "target_id": -1, "description": "radiant"}`},
	{Name: "preserve_life", Title: "Preserve Life (Channel Divinity)", Section: "Class features", Cost: costAction, Who: actionWho{Class: "cleric", Subclass: "life", Level: 3}, Uses: "channel_divinity", Fields: []string{"target_ids", "description"},
		What:    "A Magic action: share five times your Cleric level in Hit Points among Bloodied creatures within 30 feet (up to half each one's maximum).",
		Example: `{"action": "preserve_life", "description": "Aldren 8, Millie 7"}`},
	{Name: "wild_shape", Title: "Wild Shape", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "druid", Level: 2}, Uses: "wild_shape", Fields: []string{"description"},
		What:    "Shape-shift into one of your known Beast forms (choose them first: POST /api/characters/{id}/class-options {\"wild_shape_forms\": [...]}); Temporary Hit Points equal to your Druid level.",
		Example: `{"action": "wild_shape", "description": "wolf"}`},
	{Name: "revert_wild_shape", Title: "Leave Wild Shape", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "druid", Level: 2}, While: "wild_shaped",
		What:    "Return to your normal form.",
		Example: `{"action": "revert_wild_shape"}`},
	{Name: "wild_resurgence", Title: "Wild Resurgence", Section: "Class features", Cost: costFree, Who: actionWho{Class: "druid", Level: 5}, Fields: []string{"description"},
		What:    "Once a turn: spend a spell slot for a use of Wild Shape (\"shape\"), or a use of Wild Shape for a level 1 spell slot (\"slot\", once per Long Rest).",
		Example: `{"action": "wild_resurgence", "description": "shape"}`},
	{Name: "lands_aid", Title: "Land's Aid", Section: "Class features", Cost: costAction, Who: actionWho{Class: "druid", Subclass: "land", Level: 3}, Uses: "wild_shape", Fields: []string{"target_id", "description"},
		What:    "A Magic action and a use of Wild Shape: 2d6 Necrotic damage (Constitution save for half) to creatures of your choice in a 10-foot Sphere (target_id), and one creature there regains 2d6 Hit Points (\"heal <name>\").",
		Example: `{"action": "lands_aid", "target_id": -1, "description": "heal Aldren"}`},
	{Name: "natural_recovery", Title: "Natural Recovery", Section: "Class features", Cost: costFree, Who: actionWho{Class: "druid", Subclass: "land", Level: 6}, Fields: []string{"description"},
		What:    "After a Short Rest, recover spell slots whose levels add up to half your Druid level (round up): the slot levels in the description. (A Circle spell cast free: \"natural recovery\" in the cast.)",
		Example: `{"action": "natural_recovery", "description": "3 1"}`},
	{Name: "natures_sanctuary", Title: "Nature's Sanctuary", Section: "Class features", Cost: costAction, Who: actionWho{Class: "druid", Subclass: "land", Level: 14}, Uses: "wild_shape", Fields: []string{"description"},
		What:    "A Magic action and a use of Wild Shape: a 15-foot Cube of spectral trees within 120 feet gives you and your allies Half Cover and your land's Resistance for 1 minute.",
		Example: `{"action": "natures_sanctuary", "description": "around the party"}`},
	{Name: "move_sanctuary", Title: "Move Nature's Sanctuary", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "druid", Subclass: "land", Level: 14}, Fields: []string{"description"},
		What:    "Move your Nature's Sanctuary up to 60 feet.",
		Example: `{"action": "move_sanctuary", "description": "toward the gate"}`},
	{Name: "nature_magician", Title: "Nature Magician", Section: "Class features", Cost: costFree, Who: actionWho{Class: "druid", Level: 20}, Fields: []string{"description"},
		What:    "Turn uses of Wild Shape into a spell slot (each use is 2 slot levels): the number of uses in the description. Once per Long Rest.",
		Example: `{"action": "nature_magician", "description": "2"}`},
	{Name: "second_wind", Title: "Second Wind", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "fighter", Level: 1}, Uses: "second_wind",
		What:    "Regain 1d10 + your Fighter level Hit Points; one use comes back on a Short Rest, all on a Long Rest.",
		Example: `{"action": "second_wind"}`},
	{Name: "action_surge", Title: "Action Surge", Section: "Class features", Cost: costFree, Who: actionWho{Class: "fighter", Level: 2}, Uses: "action_surge",
		What:    "Take one more action this turn (not the Magic action).",
		Example: `{"action": "action_surge"}`},
	{Name: "martial_arts", Title: "Martial Arts strike", Section: "Class features", Cost: costBonus, Group: "attack", Tags: []string{"attack"}, Who: actionWho{Class: "monk", Level: 1}, Fields: []string{"target_id", "description"},
		What:    "After you take the Attack action, one Unarmed Strike with your Martial Arts die.",
		Example: `{"action": "martial_arts", "target_id": -1}`},
	{Name: "flurry_of_blows", Title: "Flurry of Blows", Section: "Class features", Cost: costBonus, Group: "attack", Tags: []string{"attack"}, Who: actionWho{Class: "monk", Level: 2}, Uses: "focus", Fields: []string{"target_id", "description"},
		What:    "1 Focus Point: two Unarmed Strikes (three from level 10). A Warrior of the Open Hand adds \"addle\", \"push\" or \"topple\".",
		Example: `{"action": "flurry_of_blows", "target_id": -1, "description": "topple"}`},
	{Name: "patient_defense", Title: "Patient Defense", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "monk", Level: 2}, Fields: []string{"description"},
		What:    "Take the Disengage action; with \"focus\" (1 Focus Point) Disengage and Dodge.",
		Example: `{"action": "patient_defense", "description": "focus"}`},
	{Name: "step_of_the_wind", Title: "Step of the Wind", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "monk", Level: 2}, Fields: []string{"description"},
		What:    "Take the Dash action; with \"focus\" (1 Focus Point) Disengage and Dash, and your jump distance doubles. Fleet Step (Open Hand 11): right after another Bonus Action, without one of its own.",
		Example: `{"action": "step_of_the_wind", "description": "focus"}`},
	{Name: "stunning_strike", Title: "Stunning Strike", Section: "Class features", Cost: costFree, Who: actionWho{Class: "monk", Level: 5}, Fields: []string{"target_id", "description"},
		What:    "Once a turn when you hit with a Monk weapon or an Unarmed Strike, 1 Focus Point: the target makes a Constitution save or is Stunned. Easiest as the feature word in the attack (\"stunning strike\").",
		Example: `{"action": "stunning_strike", "target_id": -1}`},
	{Name: "self_restoration", Title: "Self-Restoration", Section: "Class features", Cost: costFree, Who: actionWho{Class: "monk", Level: 10}, Fields: []string{"description"},
		What:    "At the end of your turn, end one of the Charmed, Frightened or Poisoned conditions on yourself.",
		Example: `{"action": "self_restoration", "description": "frightened"}`},
	{Name: "disciplined_survivor", Title: "Disciplined Survivor", Section: "Class features", Cost: costFree, Who: actionWho{Class: "monk", Level: 14}, Fields: []string{"description"},
		What:    "1 Focus Point: reroll a saving throw you failed (its bonus and DC in the description).",
		Example: `{"action": "disciplined_survivor", "description": "bonus 8 dc 15"}`},
	{Name: "superior_defense", Title: "Superior Defense", Section: "Class features", Cost: costFree, Who: actionWho{Class: "monk", Level: 18},
		What:    "At the start of your turn, 3 Focus Points: Resistance to every damage type but Force for 1 minute.",
		Example: `{"action": "superior_defense"}`},
	{Name: "quivering_palm", Title: "Quivering Palm", Section: "Class features", Cost: costFree, Who: actionWho{Class: "monk", Subclass: "open-hand", Level: 17}, Fields: []string{"target_id", "description"},
		What:    "\"start\" after an Unarmed Strike hits (4 Focus Points; no action), \"end\" later (an action, or one attack of your Attack action): the target makes a Constitution save, 10d12 Force damage (half on a success). \"end harmlessly\" takes no action.",
		Example: `{"action": "quivering_palm", "target_id": -1, "description": "start"}`},
	{Name: "lay_on_hands", Title: "Lay on Hands", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "paladin", Level: 1}, Uses: "lay_on_hands", Fields: []string{"description"},
		What:    "Restore Hit Points from your pool (five times your Paladin level), or spend 5 to end the Poisoned condition: the amount and the creature in the description.",
		Example: `{"action": "lay_on_hands", "description": "heal Aldren 5"}`},
	{Name: "divine_sense", Title: "Divine Sense (Channel Divinity)", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "paladin", Level: 3}, Uses: "channel_divinity",
		What:    "For 10 minutes you sense Celestials, Fiends and Undead within 60 feet.",
		Example: `{"action": "divine_sense"}`},
	{Name: "sacred_weapon", Title: "Sacred Weapon (Channel Divinity)", Section: "Class features", Cost: costFree, Who: actionWho{Class: "paladin", Subclass: "devotion", Level: 3}, Uses: "channel_divinity", Fields: []string{"target_id", "target_ids", "description"},
		What:    "When you take the Attack action: add your Charisma modifier to attack rolls with the weapon for 10 minutes; it sheds light and can deal Radiant damage.",
		Example: `{"action": "sacred_weapon"}`},
	{Name: "horde_breaker", Title: "Horde Breaker", Section: "Class features", Cost: costFree, Tags: []string{"attack"}, Who: actionWho{Class: "ranger", Subclass: "hunter", Level: 3, Choice: "hunters_prey=horde_breaker"}, Fields: []string{"target_id", "description"},
		What:    "Once on each of your turns after a weapon attack: another attack with the same weapon at a different creature within 5 feet of the first (if Horde Breaker is your Hunter's Prey).",
		Example: `{"action": "horde_breaker", "target_id": -2, "description": "longbow"}`},
	{Name: "tireless", Title: "Tireless", Section: "Class features", Cost: costAction, Who: actionWho{Class: "ranger", Level: 10}, Uses: "tireless",
		What:    "A Magic action: Temporary Hit Points equal to 1d8 + your Wisdom modifier.",
		Example: `{"action": "tireless"}`},
	{Name: "natures_veil", Title: "Nature's Veil", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "ranger", Level: 14}, Uses: "natures_veil",
		What:    "You have the Invisible condition until the end of your next turn.",
		Example: `{"action": "natures_veil"}`},
	{Name: "cunning_action", Title: "Cunning Action", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "rogue", Level: 2}, Fields: []string{"description"},
		What:    "Dash, Disengage or Hide as a Bonus Action (say which). A Thief (Fast Hands) can also make a Sleight of Hand check, use Thieves' Tools or Utilize.",
		Example: `{"action": "cunning_action", "description": "disengage"}`},
	{Name: "steady_aim", Title: "Steady Aim", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "rogue", Level: 3},
		What:    "If you haven't moved this turn: Advantage on your next attack roll this turn; your Speed is 0 until the turn ends.",
		Example: `{"action": "steady_aim"}`},
	{Name: "innate_sorcery", Title: "Innate Sorcery", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "sorcerer", Level: 1}, Uses: "innate_sorcery",
		What:    "For 1 minute: your Sorcerer spells' save DC +1 and Advantage on their attack rolls.",
		Example: `{"action": "innate_sorcery"}`},
	{Name: "dragon_wings", Title: "Dragon Wings", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "sorcerer", Subclass: "draconic", Level: 14},
		What:    "Sprout draconic wings: a Fly Speed of 60 feet for 1 hour.",
		Example: `{"action": "dragon_wings"}`},
	{Name: "magical_cunning", Title: "Magical Cunning", Section: "Class features", Cost: costFree, Who: actionWho{Class: "warlock", Level: 2},
		What:    "A 1-minute rite (not during a fight; it takes no action of a turn): regain expended Pact Magic slots, up to half your maximum (round up; all of them at level 20, Eldritch Master). Once per Long Rest.",
		Example: `{"action": "magical_cunning"}`},
	{Name: "gaze_of_two_minds", Title: "Gaze of Two Minds", Section: "Class features", Cost: costBonus, Who: actionWho{Class: "warlock", Invocation: "gaze-of-two-minds"}, Fields: []string{"description"},
		What:    "Touch a willing ally (named in the description) and perceive through its senses until the end of your next turn; again on a later turn to keep the link.",
		Example: `{"action": "gaze_of_two_minds", "description": "Millie"}`},
	{Name: "gift_of_the_protectors", Title: "Gift of the Protectors", Section: "Class features", Cost: costAction, Who: actionWho{Class: "warlock", Invocation: "gift-of-the-protectors"}, Fields: []string{"description"},
		What:    "Your Book of Shadows' page: \"add <name>\" (writing a name takes the writer's action: in a fight you write your own; another creature writes its own outside a fight) or \"erase <name>\" (a Magic action, touching it). A creature on it that drops to 0 Hit Points drops to 1 instead, once per Long Rest.",
		Example: `{"action": "gift_of_the_protectors", "description": "add Aldren"}`},

	// ---------------------------------------------------------------- feats and Epic Boons
	{Name: "improve_fate", Title: "Improve Fate (Boon of Fate)", Section: "Feats and Epic Boons", Cost: costFree, Who: actionWho{Feat: "boon_of_fate"},
		What:    "Roll 2d4 to add to or subtract from a D20 Test of a creature within 60 feet; once until you roll Initiative or finish a Short or Long Rest.",
		Example: `{"action": "improve_fate"}`},
	{Name: "merge_with_shadows", Title: "Merge with Shadows (Boon of the Night Spirit)", Section: "Feats and Epic Boons", Cost: costBonus, Who: actionWho{Feat: "boon_of_the_night_spirit"},
		What:    "In Dim Light or Darkness: the Invisible condition until you take an action, a Bonus Action or a Reaction.",
		Example: `{"action": "merge_with_shadows"}`},
	{Name: "blink_steps", Title: "Blink Steps (Boon of Dimensional Travel)", Section: "Feats and Epic Boons", Cost: costFree, Who: actionWho{Feat: "boon_of_dimensional_travel"}, Fields: []string{"description"},
		What:    "Right after you take the Attack or the Magic action, teleport up to 30 feet to a space you can see (where, in the description).",
		Example: `{"action": "blink_steps", "description": "to the top of the stairs"}`},

	// ---------------------------------------------------------------- magic items
	{Name: "bonus_attack", Title: "Scimitar of Speed's attack", Section: "Magic items", Cost: costBonus, Group: "attack", Tags: []string{"attack"}, Who: actionWho{Item: "Scimitar of Speed"}, Fields: []string{"target_id", "description"},
		What:    "One attack with the Scimitar of Speed (held and attuned) as a Bonus Action.",
		Example: `{"action": "bonus_attack", "target_id": -1}`},

	// ---------------------------------------------------------------- spells' own actions
	{Name: "spiritual_weapon", Title: "Spiritual Weapon's attack", Section: "Spells' own actions", Cost: costBonus, Tags: []string{"attack", "spell"}, Who: actionWho{Effect: "spiritual_weapon"}, Fields: []string{"target_id", "description"},
		What:    "The spectral weapon moves up to 20 feet and makes a melee spell attack against a creature within 5 feet of it (target_id): 1d8 + your spellcasting modifier Force, +1d8 a slot level above 2.",
		Example: `{"action": "spiritual_weapon", "target_id": -1}`},
	{Name: "flame_blade", Title: "Flame Blade", Section: "Spells' own actions", Cost: costAction, Tags: []string{"attack", "spell"}, Who: actionWho{Effect: "flame_blade"}, Fields: []string{"target_id", "description"},
		What:    "A melee spell attack with the blade (a Magic action, target_id): 3d6 + your spellcasting modifier Fire, +1d6 a slot level above 2. \"evoke\" brings it back (a Bonus Action); \"let go\" is free.",
		Example: `{"action": "flame_blade", "target_id": -1}`},
	{Name: "gaseous_form", Title: "End Gaseous Form", Section: "Spells' own actions", Cost: costAction, Who: actionWho{Effect: "gaseous_form"}, Fields: []string{"description"},
		What:    "\"end\": a Magic action that ends Gaseous Form on yourself.",
		Example: `{"action": "gaseous_form", "description": "end"}`},

	// ---------------------------------------------------------------- companions (a companion's own actions, and commands to a familiar)
	{Name: "breath", Aliases: []string{"breath_weapon"}, Title: "Draconic Spirit's Breath Weapon", Section: "Companions", Cost: costCompanion, Send: sendFamily, Who: actionWho{Companion: "draconic-spirit"}, Fields: []string{"companion_id", "target_ids", "description"},
		What:    "Your Draconic Spirit's Breath Weapon, once a round besides its Rends: the creatures in the 30-foot Cone (target_ids) and the damage type in the description. (A Dragonborn's own Breath Weapon is POST /api/characters/breath-weapon.)",
		Example: `{"action": "breath", "companion_id": 3, "target_ids": [-1, -2], "description": "lightning"}`},
	{Name: "healing_touch", Title: "Steed's Healing Touch", Section: "Companions", Cost: costCompanion, Send: sendFamily, Who: actionWho{Companion: "celestial"}, Fields: []string{"companion_id", "target_id"},
		What:    "A Celestial Otherworldly Steed's Bonus Action, once per Long Rest: an ally it touches (target_id) regains Hit Points.",
		Example: `{"action": "healing_touch", "companion_id": 3, "target_id": 12}`},
	{Name: "fey_step", Title: "Steed's Fey Step", Section: "Companions", Cost: costCompanion, Send: sendFamily, Who: actionWho{Companion: "fey"}, Fields: []string{"companion_id", "description"},
		What:    "A Fey Otherworldly Steed's Bonus Action, once per Long Rest: it teleports up to 60 feet with its rider.",
		Example: `{"action": "fey_step", "companion_id": 3}`},
	{Name: "fell_glare", Title: "Steed's Fell Glare", Section: "Companions", Cost: costCompanion, Send: sendFamily, Who: actionWho{Companion: "fiend"}, Fields: []string{"companion_id", "target_id"},
		What:    "A Fiend Otherworldly Steed's Bonus Action, once per Long Rest: a monster within 60 feet (target_id) makes a Wisdom save or is Frightened.",
		Example: `{"action": "fell_glare", "companion_id": 3, "target_id": -1}`},
	{Name: "dismiss_familiar", Title: "Dismiss your familiar", Section: "Companions", Cost: costAction, Who: actionWho{Companion: "familiar"},
		What:    "A Magic action: send your familiar to its pocket dimension (recall it with recall_familiar, no new casting).",
		Example: `{"action": "dismiss_familiar"}`},
	{Name: "recall_familiar", Title: "Recall your familiar", Section: "Companions", Cost: costAction, Who: actionWho{Companion: "familiar"},
		What:    "A Magic action: your familiar comes back from its pocket dimension and appears within 30 feet.",
		Example: `{"action": "recall_familiar"}`},
	{Name: "dismiss_familiar_forever", Title: "Dismiss your familiar for good", Section: "Companions", Cost: costAction, Who: actionWho{Companion: "familiar"},
		What:    "End your familiar (cast Find Familiar again for a new one).",
		Example: `{"action": "dismiss_familiar_forever"}`},

	// ---------------------------------------------------------------- features with their own request (what they cost is read from here)
	{Name: "font_of_magic", Title: "Font of Magic: create a spell slot", Section: "Class features", Cost: costBonus, Send: sendRoute, Route: "POST /api/characters/flexible-casting",
		Who: actionWho{Class: "sorcerer", Level: 2}, Uses: "sorcery_points", Fields: []string{"character_id", "action", "slot_level"},
		What:    "Turn Sorcery Points into a spell slot (level 1: 2 points, 2: 3, 3: 5, 4: 6, 5: 7).",
		Example: `POST /api/characters/flexible-casting {"character_id": 7, "action": "create_slot", "slot_level": 1}`},
	{Name: "font_of_magic_convert", Title: "Font of Magic: convert a spell slot", Section: "Class features", Cost: costFree, Send: sendRoute, Route: "POST /api/characters/flexible-casting",
		Who: actionWho{Class: "sorcerer", Level: 2}, Uses: "sorcery_points", Fields: []string{"character_id", "action", "slot_level"},
		What:    "Turn a spell slot into Sorcery Points equal to its level.",
		Example: `POST /api/characters/flexible-casting {"character_id": 7, "action": "convert_slot", "slot_level": 1}`},
	{Name: "wholeness_of_body", Title: "Wholeness of Body", Section: "Class features", Cost: costBonus, Send: sendRoute, Route: "POST /api/characters/wholeness-of-body",
		Who: actionWho{Class: "monk", Subclass: "open-hand", Level: 6}, Fields: []string{"character_id"},
		What:    "Roll your Martial Arts die and regain that many Hit Points plus your Wisdom modifier.",
		Example: `POST /api/characters/wholeness-of-body {"character_id": 7}`},
	{Name: "holy_nimbus", Title: "Holy Nimbus", Section: "Class features", Cost: costBonus, Send: sendRoute, Route: "POST /api/characters/holy-nimbus",
		Who: actionWho{Class: "paladin", Subclass: "devotion", Level: 20}, Fields: []string{"character_id"},
		What:    "For 10 minutes your Aura of Protection is Bright Light: Advantage on saves against Fiends and Undead, and Radiant damage to enemies that start their turn in it.",
		Example: `POST /api/characters/holy-nimbus {"character_id": 7}`},
}

// costVariants: how a variant of an action is costed (an economy word, never sent by a player): a spell whose casting time is a
// Bonus Action or a Reaction, an item used with a Bonus Action. Their feature-word group is the action's.
var costVariants = map[string]struct{ cost, group string }{
	"declare_bonus": {costBonus, "declare"},
	"bonus_cast":    {costBonus, "cast"},
	"reaction_cast": {costReaction, "cast"},
	"item_bonus":    {costBonus, "use_item"},
}

// actionCostForms: a feature whose cost depends on what is done with it (PHB 2024), from the request's description, as the economy
// word it is spent as. Quivering Palm: starting it is part of the Unarmed Strike that hit (no action); ending it is an action, or
// one attack forgone when you take the Attack action (spent as an attack of the Attack action); ending it harmlessly takes no
// action.
var actionCostForms = map[string]func(description string) string{
	"declare": func(d string) string { // a declaration takes the action, or the Bonus Action when it says so
		if strings.Contains(strings.ToLower(d), "bonus action") {
			return "declare_bonus"
		}
		return "declare"
	},
	"quivering_palm": func(d string) string {
		low := strings.ToLower(d)
		if strings.Contains(low, "end") && !strings.Contains(low, "harmless") {
			return "attack"
		}
		return "quivering_palm"
	},
}

// castingWords: a description that casts a spell or spends a spell slot.
var castingWords = regexp.MustCompile(`(?i)\b(cast|casts|casting|spell slot)\b`)

// resourceWords: how a description names a resource a feature spends (the Uses keys of the table), where the key alone is an
// ordinary word.
var resourceWords = map[string]string{"focus": "focus point", "rage": "use of rage"}

// declarationProblem is the control on a declaration (declare, "other"): the GM rules on what the character does, but nothing with a
// cost of its own rides along for free. A description that casts a spell, or names a feature the character has from the action
// table's limited ones (Divine Spark, Second Wind ...) or the resource such a feature spends (Channel Divinity, Focus Points ...),
// is refused with the request to send. Everything it reads is the action table. "" when the declaration may go to the GM.
func declarationProblem(charID int, description string) string {
	if castingWords.MatchString(description) {
		return declarationCheck(nil, description)
	}
	return declarationCheck(actionTypesOpenTo(charID), description)
}

// declarationCheck is declarationProblem's rule, given the action types the character has now.
func declarationCheck(open []actionType, description string) string {
	low := strings.ToLower(description)
	if castingWords.MatchString(description) {
		send := `{"action": "cast", "spell_slug": "<slug>"}`
		if slug, _, _ := resolveCastSpellSlug("", description); slug != "" {
			send = fmt.Sprintf(`{"action": "cast", "spell_slug": %q}`, slug)
		}
		return "a spell is cast with the Magic action, which spends its slot and rolls what it does: send " + send + " (a declaration is only for what has no action of its own)."
	}
	for _, t := range open {
		if t.Who == (actionWho{}) || t.Send == sendFamily {
			continue
		}
		names := []string{strings.ToLower(strings.SplitN(t.Title, " (", 2)[0]), strings.ReplaceAll(t.Name, "_", " ")}
		if t.Uses != "" {
			u := strings.ReplaceAll(t.Uses, "_", " ")
			if w, ok := resourceWords[t.Uses]; ok {
				u = w
			}
			names = append(names, u)
		}
		for _, n := range names {
			if len(n) > 3 && regexp.MustCompile(`\b`+regexp.QuoteMeta(n)+`s?\b`).MatchString(low) {
				return fmt.Sprintf("%q is %s, its own action (it spends what it spends and rolls what it does): send %s.", n, t.Title, t.Example)
			}
		}
	}
	return ""
}

// actionTypeIndex: every name and alias, folded by game.Key, to its type.
var actionTypeIndex = func() map[string]int {
	idx := map[string]int{}
	for i, t := range actionTypes {
		idx[game.Key(t.Name)] = i
		for _, a := range t.Aliases {
			idx[game.Key(a)] = i
		}
	}
	return idx
}()

// lookupActionType: the type a word names (a name or an alias, in any case or spelling game.Key folds).
func lookupActionType(word string) (actionType, bool) {
	if i, ok := actionTypeIndex[game.Key(word)]; ok && strings.TrimSpace(word) != "" {
		return actionTypes[i], true
	}
	return actionType{}, false
}

// canonicalAction: the type's own name for a word (Magic is cast, Utilize is use_item); a word that is no type, lower-cased.
func canonicalAction(action string) string {
	if t, ok := lookupActionType(action); ok {
		return t.Name
	}
	return strings.ToLower(strings.TrimSpace(action))
}

// getActionResourceType: what an action type (or a cost variant) takes: action, bonus_action, reaction, movement, free or companion.
// A word that is no type is refused before anything is spent (unknownAction); it is counted as an action here so a caller that
// asks anyway never gets it free.
func getActionResourceType(actionType string) string {
	if v, ok := costVariants[strings.ToLower(strings.TrimSpace(actionType))]; ok {
		return v.cost
	}
	if t, ok := lookupActionType(actionType); ok {
		return t.Cost
	}
	return costAction
}

// featureAction: the feature-word group of an action (a bonus or reaction cast is a cast; an opportunity attack an attack).
func featureAction(action string) string {
	if v, ok := costVariants[action]; ok {
		return v.group
	}
	if t, ok := lookupActionType(action); ok && t.Group != "" {
		return t.Group
	}
	return action
}

// ---------------------------------------------------------------- who can take what

// actorFacts: what decides which types a character has now. refusal reads nothing else, so a test can describe a character
// without a database.
type actorFacts struct {
	class, subclass                                     string
	level                                               int
	raging, wildShaped, prone, dying, burning, inspired bool
	choices                                             map[string]string
	// asked only when a type names one: a feat or Epic Boon, an Eldritch Invocation, a spell effect on the character (by slug or
	// kind), whether a magic item is in hand and attuned, the character's companions (not gone)
	hasFeat, hasInvocation, hasEffect func(string) bool
	heldAttuned                       func(item string) (held, attuned bool)
	companions                        func() []companion
}

// featLookup, invocationLookup and effectLookup: what the rule asks about a character's feats (and Epic Boons, by slug),
// Eldritch Invocations and the spell effects on it.
func featLookup(charID int) func(string) bool {
	return func(slug string) bool { return hasSpecificFeat(charID, slug) }
}

func invocationLookup(charID int) func(string) bool {
	return func(slug string) bool { return hasInvocation(charID, slug) }
}

func effectLookup(charID int) func(string) bool {
	return func(kind string) bool { _, ok := charBuff(charID, kind); return ok }
}

// bardicDieHeld: a Bardic Inspiration die is held and hasn't faded (it lasts an hour).
func bardicDieHeld(die int, until *time.Time, now time.Time) bool {
	return die != 0 && (until == nil || !now.After(*until))
}

func actorFactsOf(charID int) actorFacts {
	f := actorFacts{hasFeat: featLookup(charID), hasInvocation: invocationLookup(charID), hasEffect: effectLookup(charID),
		heldAttuned: func(item string) (bool, bool) { return heldAndAttuned(charID, item) }}
	f.class, f.subclass, f.level = subclassOf(charID)
	var hp, die int
	var stable, dead, burning bool
	var form *string
	var dieUntil *time.Time
	var choices []byte
	db.QueryRow(`SELECT COALESCE(hp, 0), COALESCE(is_stable, false), COALESCE(is_dead, false), wild_shape_form, COALESCE(subclass_choices, '{}'),
		COALESCE(bardic_die, 0), bardic_die_until,
		EXISTS (SELECT 1 FROM burning b WHERE b.target_id = characters.id AND b.lobby_id = characters.lobby_id)
		FROM characters WHERE id = $1`, charID).Scan(&hp, &stable, &dead, &form, &choices, &die, &dieUntil, &burning)
	json.Unmarshal(choices, &f.choices)
	f.dying = hp <= 0 && !stable && !dead
	f.wildShaped = form != nil && *form != ""
	f.raging = hasCondition(charID, "raging")
	f.prone = hasCondition(charID, "prone")
	f.burning = burning
	f.inspired = bardicDieHeld(die, dieUntil, time.Now())
	var list []companion
	read := false
	f.companions = func() []companion {
		if !read {
			list, read = companionsOf(charID), true
		}
		return list
	}
	return f
}

// openTo: the character has this type now (refusal is the rule).
func (t actionType) openTo(f *actorFacts) bool { return t.refusal(f) == nil }

// whoRefusal: why a character can't use an action type now: the first thing the table's Who or While asks for that it lacks.
type whoRefusal struct {
	Code    string // wrong_class, level_too_low, wrong_subclass, choice_required, feat_required, invocation_required, item_not_in_hand, not_attuned, effect_required, companion_required, wrong_state
	Message string
}

// text: the refusal's words ("" when there is none), for a resolver that answers in words.
func (r *whoRefusal) text() string {
	if r == nil {
		return ""
	}
	return r.Message
}

// reply: the refusal as a request's answer (400).
func (r *whoRefusal) reply() map[string]interface{} {
	return map[string]interface{}{"success": false, "error": r.Code, "message": r.Message + " Nothing was spent."}
}

// refusal is the rule of who has an action type (HANDOFF item 31, Who step 2, Matt Nov 16): the class, its level and subclass,
// a subclass choice, a feat or Epic Boon, an Eldritch Invocation, a magic item in hand and attuned, a spell effect, a companion,
// and the state it is used in (While). Every feature's resolver asks it (actionRefusal), the turn's options and the refusal of an
// unknown action list what it allows (actionTypesOpenTo), so who has a feature is said once. nil: the character may use it.
func (t actionType) refusal(f *actorFacts) *whoRefusal {
	w, title := t.Who, t.Title
	if title == "" {
		title = t.Name
	}
	// every refusal reads "Cannot use <feature>: <why>.", the form the server treats as a refusal (it spends nothing and uses
	// no exploration beat: hasCharacterActedSinceLastNarration)
	no := func(code, format string, args ...interface{}) *whoRefusal {
		return &whoRefusal{Code: code, Message: "Cannot use " + title + ": " + fmt.Sprintf(format, args...)}
	}
	if w.Class != "" {
		feature := fmt.Sprintf("a %s feature from level %d", titleCase(w.Class), maxInt(w.Level, 1))
		if w.Subclass != "" {
			feature = fmt.Sprintf("a %s %s feature from level %d", subclassTitle(w.Subclass), titleCase(w.Class), maxInt(w.Level, 1))
		}
		switch {
		case f.class != w.Class:
			return no("wrong_class", "it is %s.", feature)
		case f.level < w.Level:
			return no("level_too_low", "it is %s; you are level %d.", feature, f.level)
		case w.Subclass != "" && f.subclass != w.Subclass:
			return no("wrong_subclass", "it is %s.", feature)
		}
	}
	if k, v, ok := strings.Cut(w.Choice, "="); ok && f.choices[k] != v {
		return no("choice_required", "it needs the %s option of %s (POST /api/characters/subclass-choice).", titleCase(strings.ReplaceAll(v, "_", " ")), titleCase(strings.ReplaceAll(k, "_", " ")))
	}
	if w.Feat != "" && !f.hasFeat(w.Feat) {
		return no("feat_required", "it needs the %s feat.", titleCase(strings.ReplaceAll(strings.ReplaceAll(w.Feat, "_", " "), "-", " ")))
	}
	if w.Invocation != "" && !f.hasInvocation(w.Invocation) {
		return no("invocation_required", "it needs the %s Eldritch Invocation.", titleCase(strings.ReplaceAll(w.Invocation, "-", " ")))
	}
	if w.Item != "" {
		switch held, attuned := f.heldAttuned(w.Item); {
		case !held:
			return no("item_not_in_hand", "it needs a %s in hand.", w.Item)
		case !attuned:
			return no("not_attuned", "it needs you attuned to the %s (POST /api/characters/attune).", w.Item)
		}
	}
	if w.Effect != "" && !f.hasEffect(w.Effect) {
		return no("effect_required", "it needs the %s spell in effect on you (cast it first; it lasts while you keep Concentration).", titleCase(strings.ReplaceAll(w.Effect, "_", " ")))
	}
	if w.Companion != "" {
		found := false
		for _, c := range f.companions() {
			found = found || (c.Status != "gone" && companionFits(w.Companion, c))
		}
		if !found {
			return no("companion_required", "it needs %s.", companionKindText(w.Companion))
		}
	}
	if why := whileProblem(t.While, f); why != "" {
		return no("wrong_state", "%s", why)
	}
	return nil
}

// whileProblem: the state a type is used in (While), and what is wrong when the character isn't in it ("" when it is).
func whileProblem(while string, f *actorFacts) string {
	switch {
	case while == "raging" && !f.raging:
		return "you are not raging."
	case while == "not_raging" && f.raging:
		return "you are already raging."
	case while == "wild_shaped" && !f.wildShaped:
		return "you are not in a Wild Shape form."
	case while == "prone" && !f.prone:
		return "you are not Prone."
	case while == "dying" && !f.dying:
		return "you are not dying."
	case while == "burning" && !f.burning:
		return "you are not burning."
	case while == "inspired" && !f.inspired:
		return "you hold no Bardic Inspiration die (a Bard gives one; it fades after an hour)."
	}
	return ""
}

// companionFits: a companion is of the kind a type's Who names: "any", a kind (familiar, steed, summon), a form
// (draconic-spirit), or a steed's creature type (celestial, fey, fiend).
func companionFits(kind string, c companion) bool {
	k := strings.ToLower(kind)
	return k == "any" || c.Kind == k || c.Form == k || (c.Kind == "steed" && strings.EqualFold(c.CreatureType, k))
}

// companionKindText: a companion kind in words.
func companionKindText(kind string) string {
	switch strings.ToLower(kind) {
	case "any":
		return "a companion"
	case "familiar":
		return "a familiar"
	case "draconic-spirit":
		return "a Draconic Spirit (Summon Dragon)"
	case "celestial", "fey", "fiend":
		return "an Otherworldly Steed of the " + titleCase(kind) + " type"
	}
	return "a " + strings.ReplaceAll(kind, "-", " ")
}

// actionRefusal is the one check a feature's resolver makes of who may use it: the action table's Who and While for the character
// now (refusal). nil: it may go on.
func actionRefusal(name string, charID int) *whoRefusal {
	t, ok := lookupActionType(name)
	if !ok {
		return &whoRefusal{Code: "unknown_action", Message: fmt.Sprintf("%q is not an action type.", name)}
	}
	f := actorFactsOf(charID)
	return t.refusal(&f)
}

// companionRefusal: the resolver of a companion's own action checks the companion it was sent for against the type's Who.
func companionRefusal(name string, c companion) *whoRefusal {
	t, ok := lookupActionType(name)
	if !ok || t.Who.Companion == "" || companionFits(t.Who.Companion, c) {
		return nil
	}
	return &whoRefusal{Code: "companion_required", Message: fmt.Sprintf("Cannot use %s: %s is not %s.", t.Title, companionDisplayName(c), companionKindText(t.Who.Companion))}
}

// subclassTitle: a subclass slug as a name ("open-hand": "Open Hand").
func subclassTitle(slug string) string { return titleCase(strings.ReplaceAll(slug, "-", " ")) }

// actionTypesOpenTo: the types a character has now, in the table's order.
func actionTypesOpenTo(charID int) []actionType {
	f := actorFactsOf(charID)
	var out []actionType
	for _, t := range actionTypes {
		if t.openTo(&f) {
			out = append(out, t)
		}
	}
	return out
}

// exampleBody: a type's example as JSON (a route's example: its body after the path).
func (t actionType) exampleBody() map[string]interface{} {
	ex := t.Example
	if i := strings.Index(ex, "{"); i > 0 {
		ex = ex[i:]
	}
	var m map[string]interface{}
	json.Unmarshal([]byte(ex), &m)
	return m
}

// howToSend: the request a type is sent with.
func (t actionType) howToSend() string {
	switch t.Send {
	case sendRoute:
		return t.Route
	case sendPending:
		return "a step of POST /api/pending-turn"
	case sendFamily:
		return "POST /api/action with companion_id"
	}
	return "POST /api/action"
}

// actionOption: one type as the refusal and the docs show it.
func actionOption(t actionType) map[string]interface{} {
	o := map[string]interface{}{"action": t.Name, "costs": costLabels[t.Cost], "what": t.What, "send": t.howToSend(), "example": t.exampleBody()}
	if len(t.Aliases) > 0 {
		o["also"] = t.Aliases
	}
	return o
}

// actionOptionsFor: what the character can send now, each with its request (a refusal's list; my-turn shows them by part of the
// turn).
func actionOptionsFor(charID int) []map[string]interface{} {
	var out []map[string]interface{}
	for _, t := range actionTypesOpenTo(charID) {
		out = append(out, actionOption(t))
	}
	return out
}

// ---------------------------------------------------------------- refusing a word that is no action

// describedHint: how to send what the server doesn't resolve itself.
const describedHint = "Something the server doesn't resolve itself is sent as the action it is, with a description, and the GM rules on it: pulling a lever or forcing a door is use_item (Utilize), persuading or threatening is influence, looking around is search, recalling lore is study, a shouted warning is speak, opening a door on your way is interact. A turn in your own words that fits none of them is declare (\"other\" is accepted): the GM rules on it, and in a fight it takes your action; a spell or a feature is sent with its own action, never declared. Every action type: /commands."

// unknownActionRefusal: the reply to a word that is no action type (400 unknown_action): what the character can send now, each with
// its request. Nothing was spent or changed.
func unknownActionRefusal(charID int, word, where string) map[string]interface{} {
	msg := fmt.Sprintf("%q is not an action.", word)
	if strings.TrimSpace(word) == "" {
		msg = "Name the action."
	}
	if where != "" {
		msg = where + ": " + msg
	}
	return map[string]interface{}{
		"success": false,
		"error":   "unknown_action",
		"message": msg + " " + describedHint + " Nothing was spent.",
		"actions": actionOptionsFor(charID),
	}
}

// actionWordProblem: why a word can't be sent to POST /api/action ("" when it can): no such type (code unknown_action), a spell's
// name (spell_as_action), a type sent elsewhere (a feature's own request, a pending turn's step), or a companion's own action
// sent without companion_id.
func actionWordProblem(word string, companionID int) (code, msg string) {
	t, ok := lookupActionType(word)
	if !ok {
		slug := normalizeSpellSlug(strings.ReplaceAll(word, "_", "-"))
		if sp, isSpell := srdSpellsMemory[slug]; isSpell && word != "" {
			return "spell_as_action", fmt.Sprintf("%s is a spell. Cast it with {\"action\": \"cast\", \"spell_slug\": \"%s\", \"target_id\": ...}. Nothing was spent.", sp.Name, slug)
		}
		return "unknown_action", ""
	}
	switch {
	case t.Send == sendRoute:
		return "use_its_request", fmt.Sprintf("%s has its own request: %s (example: %s). Nothing was spent.", t.Title, t.Route, t.Example)
	case t.Send == sendPending:
		return "pending_turn_step", fmt.Sprintf("%s is a step of a pending turn (POST /api/pending-turn). On your turn, move on the battle map with POST /api/campaigns/{id}/battle-map/move. Nothing was spent.", t.Name)
	case t.Send == sendFamily && companionID == 0:
		return "companion_id_required", fmt.Sprintf("%s is a companion's action: send companion_id (GET /api/my-turn lists your companions). Nothing was spent.", t.Name)
	case companionID != 0 && t.Send != sendFamily && !t.Family && t.Section != "Companions":
		return "invalid_companion_action", companionActionsText()
	}
	return "", ""
}

// companionActionsText: what a companion can do, from the table.
func companionActionsText() string {
	var common, own []string
	for _, t := range actionTypes {
		switch {
		case t.Family:
			common = append(common, t.Name)
		case t.Send == sendFamily:
			own = append(own, fmt.Sprintf("%s (%s)", t.Name, t.Title))
		}
	}
	return "A companion can " + strings.Join(common, ", ") + "; by its kind also " + strings.Join(own, ", ") + ". Nothing was spent."
}

// companionCanTake: a companion may be sent this type (companion_id).
func companionCanTake(action string) bool {
	t, ok := lookupActionType(action)
	return ok && (t.Family || t.Send == sendFamily)
}

// ---------------------------------------------------------------- Ready

// readiedAction: what a Ready holds, from "trigger: ...; action: ..." (or a description that names an action type): the trigger,
// the type and its description. ok is false when it names none (refused: a Ready must hold a real action).
func readiedAction(description string) (trigger, action, desc string, ok bool) {
	trigger, desc = strings.TrimSpace(description), strings.TrimSpace(description)
	low := strings.ToLower(description)
	if i := strings.Index(low, "trigger:"); i >= 0 {
		rest := description[i+len("trigger:"):]
		parts := strings.SplitN(rest, ";", 2)
		trigger = strings.TrimSpace(parts[0])
		desc = ""
		if len(parts) == 2 {
			desc = strings.TrimSpace(parts[1])
		}
	}
	if j := strings.Index(strings.ToLower(desc), "action:"); j >= 0 {
		desc = strings.TrimSpace(desc[j+len("action:"):])
	}
	for _, w := range strings.FieldsFunc(desc, func(r rune) bool { return r == ' ' || r == ',' || r == '.' || r == ':' }) {
		if t, found := lookupActionType(w); found && readyable(t) {
			return trigger, t.Name, desc, true
		}
	}
	return trigger, "", desc, false
}

// readyable: a Ready holds an action (or a move); not another Ready, not a declaration (a Ready names the action it holds), and
// not a feature's own request or a companion's action.
func readyable(t actionType) bool {
	return t.Send == sendAction && t.Name != "ready" && t.Name != "declare" && (t.Cost == costAction || t.Cost == costMovement)
}

// readiedActionRefusal: a Ready that names no action it can hold (400 unknown_action), with the actions it can hold.
func readiedActionRefusal(charID int) map[string]interface{} {
	var can []map[string]interface{}
	for _, t := range actionTypesOpenTo(charID) {
		if readyable(t) {
			can = append(can, actionOption(t))
		}
	}
	return map[string]interface{}{
		"success": false,
		"error":   "unknown_action",
		"message": "A Ready holds one action: describe it as \"trigger: <what happens>; action: <an action type> ...\", for example \"trigger: the first goblin comes through the door; action: attack it with my spear\". " + describedHint + " Nothing was spent.",
		"actions": can,
	}
}

// ---------------------------------------------------------------- my-turn's options and reminders

// turnOptions: the types the character has now, as my-turn lists them by part of the turn: actions, Bonus Actions and the things
// that take none, each with the request to send and the uses left. A companion's own actions are listed with the companion.
func turnOptions(charID int) (actions, bonus, free []map[string]interface{}) {
	f := actorFactsOf(charID)
	for _, t := range actionTypes {
		if t.Send == sendFamily || t.Send == sendPending || !t.openTo(&f) {
			continue
		}
		what := t.What
		if t.Uses != "" {
			if l, m := resourceLeft(charID, f.class, f.level, t.Uses); m > 0 {
				what += fmt.Sprintf(" (%d of %d left)", l, m)
			}
		}
		use := t.Example
		if t.Send == sendRoute {
			use = strings.Replace(use, `"character_id": 7`, fmt.Sprintf(`"character_id": %d`, charID), 1)
		}
		o := map[string]interface{}{"name": t.Title, "action": t.Name, "use": use, "description": what}
		switch t.Cost {
		case costAction:
			actions = append(actions, o)
		case costBonus:
			bonus = append(bonus, o)
		case costFree, costMovement:
			free = append(free, o)
		}
	}
	return
}

// srdActionTitles: the SRD's actions by the names the rules give them ("Attack, Dash, ... Utilize"), for the turn reminder.
func srdActionTitles() string {
	var out []string
	for _, t := range actionTypes {
		if t.SRD {
			out = append(out, strings.SplitN(t.Title, " (", 2)[0])
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// ---------------------------------------------------------------- the docs

// actionTypesBySection: the types each section holds, in the table's order.
func actionTypesBySection() map[string][]actionType {
	out := map[string][]actionType{}
	for _, t := range actionTypes {
		out[t.Section] = append(out[t.Section], t)
	}
	return out
}

// whoText: who has a type, for the docs.
func (t actionType) whoText() string {
	w := t.Who
	var parts []string
	if w.Class != "" {
		s := titleCase(w.Class)
		if w.Subclass != "" {
			s += " (" + titleCase(strings.ReplaceAll(w.Subclass, "-", " ")) + ")"
		}
		if w.Level > 1 {
			s += fmt.Sprintf(" %d", w.Level)
		}
		parts = append(parts, s)
	}
	if w.Feat != "" {
		parts = append(parts, titleCase(strings.ReplaceAll(w.Feat, "_", " ")))
	}
	if w.Invocation != "" {
		parts = append(parts, "invocation "+titleCase(strings.ReplaceAll(w.Invocation, "-", " ")))
	}
	if w.Item != "" {
		parts = append(parts, w.Item)
	}
	if w.Effect != "" {
		parts = append(parts, "while the spell lasts")
	}
	if w.Companion != "" {
		parts = append(parts, "with a companion ("+w.Companion+")")
	}
	return strings.Join(parts, ", ")
}

// skill.md's action list sits between these markers (TestActionTypesInSkillMd).
const (
	actionsBegin = "<!-- actions: written by the server (cmd/server/action_types_2024.go); change it there -->"
	actionsEnd   = "<!-- end of actions -->"
)

// actionTypesMarkdown: skill.md's action list (between its action markers), from the table.
func actionTypesMarkdown() string {
	var b strings.Builder
	b.WriteString("Every action type the server takes, from its one table (the same list is on /commands and in the API reference). " +
		"Send `{\"action\": \"<type>\", ...}` to `POST /api/action`; `GET /api/my-turn` lists the ones your character has now, with uses left. " +
		"A word that is not here is refused with `unknown_action` and the list of what you can send; nothing is spent. " +
		"An action the rules refuse (a feature you don't have, a use already spent, the wrong moment) answers 409 `action_refused` with `message` and `result` starting \"Cannot ...\": nothing is spent and it doesn't use your exploration beat. " +
		describedHint + "\n")
	bySection := actionTypesBySection()
	for _, sec := range actionSections {
		b.WriteString("\n**" + sec + "**\n\n")
		for _, t := range bySection[sec] {
			name := "`" + t.Name + "`"
			for _, a := range t.Aliases {
				name += " (or `" + a + "`)"
			}
			cost := costLabels[t.Cost]
			if who := t.whoText(); who != "" {
				cost += "; " + who
			}
			if t.Send == sendRoute || t.Send == sendPending {
				cost += "; sent as " + t.howToSend()
			}
			if t.Family {
				cost += "; a companion can take it too"
			}
			fields := "none"
			if len(t.Fields) > 0 {
				fields = strings.Join(t.Fields, ", ")
			}
			b.WriteString(fmt.Sprintf("- %s (%s): %s Fields: %s. Example: `%s`\n", name, cost, t.What, fields, t.Example))
		}
	}
	return b.String()
}

// actionTypesHTML: the commands page's table of action types, from the table.
func actionTypesHTML() string {
	var b strings.Builder
	bySection := actionTypesBySection()
	for _, sec := range actionSections {
		b.WriteString(`<tr><th colspan="3" style="text-align:left;padding-top:10px">` + e(sec) + `</th></tr>`)
		for _, t := range bySection[sec] {
			names := `<code>` + e(t.Name) + `</code>`
			for _, a := range t.Aliases {
				names += ` (or <code>` + e(a) + `</code>)`
			}
			cost := costLabels[t.Cost]
			if who := t.whoText(); who != "" {
				cost += "; " + who
			}
			if t.Send != sendAction {
				cost += "; sent as " + t.howToSend()
			}
			if t.Family {
				cost += "; a companion can take it too"
			}
			fields := ""
			if len(t.Fields) > 0 {
				fields = " Fields: " + strings.Join(t.Fields, ", ") + "."
			}
			b.WriteString(fmt.Sprintf(`<tr data-action="%s"><td>%s</td><td>%s</td><td>%s%s<br><code class="ex">%s</code></td></tr>`,
				e(t.Name), names, e(cost), e(t.What), e(fields), e(t.Example)))
		}
	}
	return b.String()
}
