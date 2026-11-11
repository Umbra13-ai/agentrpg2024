package main

// /commands: one page where players and GMs find every command. The player actions (POST /api/action) are listed here; every
// endpoint comes from the API reference (the same godoc-built swagger.json behind /docs), grouped by who uses it, so the page can't
// fall behind the code.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

type playerActionDoc struct {
	Action, Uses, What string
}

// playerActions are the actions a player sends to POST /api/action {"action": ..., "description": ..., "target_id": ...}.
var playerActions = []playerActionDoc{
	{"attack", "Action", "Attack with your equipped weapon or an Unarmed Strike (target_id: an enemy; object_id: an object such as a door). Improvised weapons (a chair, a bottle), Alchemist's Fire and Acid flasks, \"knock out\" for a nonlethal blow. A named magic weapon (Vorpal Sword, Holy Avenger) is equipped with the weapon it is: equip-weapon with \"base_weapon\"; the Vorpal Sword beheads on a roll of 20. Words in the description: \"2 to AC\" (Defender), \"luck\" (Luck Blade), \"drain life\" or \"paralyze\" (Rod of Lordly Might), \"hurl\" (Hammer of Thunderbolts)."},
	{"cast (or magic)", "Action (or as the spell says)", "Cast a spell: spell_slug, slot_level, target_id or target_ids. A Reaction spell spends the Reaction: Shield right after a monster's hit turns that hit into a miss if +5 AC is enough. A Cone or Line points toward the first target. Damage while concentrating rolls the Concentration save for you."},
	{"dash", "Action", "Extra movement equal to your Speed this turn. Expeditious Retreat: a Bonus Action (\"bonus action dash\")."},
	{"disengage", "Action", "Your movement doesn't provoke Opportunity Attacks this turn."},
	{"dodge", "Action", "Attacks against you have Disadvantage; Advantage on Dexterity saves."},
	{"help", "Action", "target_id: an enemy within 5 feet; the next attack roll by one of your allies against it has Advantage. Or target_character_id (an ally) and one of your skill or tool proficiencies in the description: Advantage on its next check with it. Until the start of your next turn."},
	{"martial_arts", "Bonus Action (Monk)", "An Unarmed Strike with the Martial Arts die against the enemy picked with target_id."},
	{"companion_id + attack, dodge, dash, disengage, help, hide, search, move", "Your companion's turn (on your turn)", "A familiar, steed, summon or pet of yours acts (GET /api/my-turn lists them). Once per round; it doesn't use your action. A familiar attacks only through Pact of the Chain (\"quick attack\" with Investment of the Chain Master) and delivers your touch spells (cast with \"delivered by my familiar\"). Name one with POST /api/companions/name; move its token with companion_id on POST /api/campaigns/{id}/battle-map/preview and /move."},
	{"companion_id + breath", "With its Rends (once per round)", "A Draconic Spirit's Breath Weapon: target_ids (the creatures in the 30-foot Cone) and the type in the description (acid, cold, fire, lightning or poison)."},
	{"companion_id + healing_touch, fey_step, fell_glare", "Bonus Action, once per Long Rest", "An Otherworldly Steed's power by its kind: Healing Touch (Celestial; target_id an ally), Fey Step (Fey), Fell Glare (Fiend; target_id a monster). Life Bond (healing shared with a steed within 5 feet) is automatic."},
	{"dismiss_familiar, recall_familiar, dismiss_familiar_forever", "Magic action", "Send your familiar to its pocket dimension, call it back within 30 feet (no new casting), or end it."},
	{"hide", "Action", "Dexterity (Stealth) DC 15; hidden if there is somewhere to hide (the GM can overrule)."},
	{"influence", "Action", "Persuade, deceive, intimidate, amuse or coax a creature (target_id); its attitude gives Advantage or Disadvantage."},
	{"ready", "Action", "Hold an action for a trigger (\"trigger: ...; action: ...\")."},
	{"search", "Action", "Wisdom check: Insight, Medicine, Perception or Survival."},
	{"study", "Action", "Intelligence check: Arcana, History, Investigation, Nature or Religion."},
	{"use_item (or utilize, drink)", "Action; a potion is a Bonus Action", "Use an object that needs an action. With \"item\": a potion (drink it, or give it to target_character_id within 5 feet). Read a Spell Scroll or cast from a wand (or the Cubic Gate) with the cast action and \"item\". Legendary items: Horn of Valhalla (Berserker spirits), Ring of Djinni Summoning (a Djinni), Iron Flask (target_id; \"release\"), Sphere of Annihilation (\"control\", then \"move\" with target_ids, a Bonus Action), Mysterious Deck (\"draw 3\"; choices like \"star: wisdom\"), Rod of Lordly Might (\"button 1 longsword\", \"terrify\"), Staff of the Magi (\"retributive strike\"), Ring of Elemental Command (\"compel\"), Armor of Invulnerability, Plate Armor of Etherealness, Luck Blade (\"luck\"), Apparatus of the Crab (\"lever 1 up, lever 4 up\"; lever 5 with target_id: the claws attack), Sovereign Glue, Universal Solvent, Well of Many Worlds."},
	{"stabilize", "Action", "DC 10 Wisdom (Medicine) on a dying ally (target_character_id)."},
	{"extinguish", "Action", "Put out the fire on yourself: you drop Prone and roll."},
	{"move", "Movement", "Move (on a battle map use POST /api/campaigns/{id}/battle-map/move; with a Fly Speed add \"altitude\", the feet above the ground)."},
	{"stand", "Half your Speed", "Stand up from Prone."},
	{"gaze_of_two_minds", "Bonus Action (Warlock invocation)", "Touch a willing ally (name in the description) and perceive through its senses until the end of your next turn; again on a later turn to keep the link."},
	{"cast an invocation's spell", "As the spell", "Mask of Many Faces (Disguise Self), Master of Myriad Forms (Alter Self), Misty Visions (Silent Image), Otherworldly Leap (Jump on yourself), Visions of Distant Realms (Arcane Eye), Whispers of the Grave (Speak with Dead): cast with spell_slug, no slot. Gift of the Depths: Water Breathing once per Long Rest without a slot, and a Swim Speed."},
	{"jump", "Movement", "Long Jump (up to your Strength score in feet) or High Jump (3 + Strength modifier); \"standing\" halves it. A Thief of level 3 (Second-Story Work) jumps with Dexterity when it's higher. The Jump spell: once on each of your turns, 30 feet for 10 feet of movement."},
	{"opportunity_attack", "Reaction", "One melee attack when a monster moves out of your reach (GET /api/my-turn shows when you can). With the War Caster feat add \"spell_slug\" to cast a spell at it instead (Reactive Spell: casting time one action, that creature only)."},
	{"spiritual_weapon", "Bonus Action", "Spiritual Weapon's force moves up to 20 feet and makes a melee spell attack against a creature within 5 feet of it (target_id): 1d8 + your spellcasting modifier Force, +1d8 a slot level above 2."},
	{"flame_blade", "Magic action; \"evoke\" a Bonus Action; \"let go\" free", "A melee spell attack with the Flame Blade (target_id, reach 5 feet): 3d6 + your spellcasting modifier Fire, +1d6 a slot level above 2. The blade fills a hand."},
	{"gaseous_form", "Magic action", "\"end\": end Gaseous Form on yourself."},
	{"death_save", "Your turn at 0 HP", "Roll a Death Saving Throw."},
	{"grapple / shove", "One attack of the Attack action", "Unarmed Strike options; the target saves against 8 + Strength modifier + Proficiency Bonus."},
	{"class and species features", "As the feature says", "rage, bardic_inspiration, cunning_action, flurry_of_blows, patient_defense, step_of_the_wind (Fleet Step: right after another Bonus Action, Open Hand 11), lay_on_hands, wild_shape (choose forms first: POST /api/characters/{id}/class-options {\"wild_shape_forms\": [...]}) and revert_wild_shape, second_wind, action_surge, breath weapons, natures_sanctuary, move_sanctuary ... (GET /api/my-turn lists yours under your_options)."},
	{"feature words (any action)", "As the feature", "GET /api/my-turn lists your character's words under your_options.feature_words (also feature_words on the character sheet). Send them as \"features\": [\"sculpt\"] in POST /api/action (an unknown word is refused with your valid words; nothing is spent) or write them in the description (whole words). Advantage can't be claimed in a description; the GM grants it with POST /api/gm/grant-advantage."},
	{"cast with a feature's words", "As the spell", "\"divine intervention\" (Cleric 10: a Cleric spell of level 5 or lower, no slot; Wish at level 20), \"free\" (Favored Enemy, Magic Initiate and other free casts), \"wild companion\", \"overchannel\", \"delivered by my familiar\". Power Word Kill, Stun and Heal resolve by the target's Hit Points; a level 20 Bard (Words of Creation) adds a second target with target_ids."},
	{"level up or multiclass (POST /api/characters/{id}/level-up)", "Between turns", "Take a level the GM's XP allows; where the campaign allows multiclassing (the multiclassing table rule; off by default), {\"class\": \"wizard\"} multiclasses (13 in the primary ability of the new class and of yours: GET /api/characters/multiclass); a first Bard, Ranger or Rogue level takes \"skill\" (or later {\"multiclass_skill\": \"...\"}); each class's subclass with POST /api/characters/subclass. If the GM levels characters automatically, plan it first: {\"class\": \"wizard\", \"plan\": true}."},
}

var commandGroups = []struct {
	Title string
	Tags  []string
	Note  string
}{
	{"For players: characters", []string{"Characters"}, "Create and manage your character: sheet, equipment, spells, level-up, Ability Score Improvements and feats, class choices."},
	{"For players: playing", []string{"Actions", "Play", "Gameplay", "Exploration", "Conditions"}, "Your turn, actions and what you can do now."},
	{"Campaigns and combat", []string{"Campaigns", "Combat", "Campaign Items"}, "Join a campaign, the turn order, the battle map, table rules, marching order."},
	{"For the GM", []string{"GM"}, "Running the game: checks, saves, rewards, levels, monsters' actions, hazards, travel, objects, attitudes, overruling results."},
	{"Rules and reference", []string{"Universe"}, "The SRD 5.2.1: spells, monsters (players see lore only), items, classes, species, feats, rules glossary."},
	{"Accounts and the server", []string{"Auth", "Core", "System", "Info", "Feedback", "Other"}, "Registration, tokens, server information."},
}

func handleCommandsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var spec struct {
		Paths map[string]map[string]struct {
			Summary     string   `json:"summary"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
	}
	json.Unmarshal(swaggerJSON, &spec)
	type ep struct{ Method, Path, Summary, Desc string }
	byTag := map[string][]ep{}
	for path, ms := range spec.Paths {
		for m, op := range ms {
			tag := "Other"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			byTag[tag] = append(byTag[tag], ep{strings.ToUpper(m), "/api" + path, op.Summary, op.Description})
		}
	}
	var b strings.Builder
	b.WriteString(`<style>.cmd td{vertical-align:top;padding:4px 8px;border-bottom:1px solid rgba(128,128,128,.25)}.cmd code{white-space:nowrap}table.actions{table-layout:fixed;width:100%}table.actions td:first-child code{white-space:normal;overflow-wrap:anywhere}
details.ep{margin:4px 0}details.ep summary{cursor:pointer}details.ep p{margin:4px 0 8px 1.5em;opacity:.85}</style>`)
	b.WriteString(`<h1>📜 Commands</h1><p>Every command in the game, for players and for the GM. Send them to the server with your token (<code>Authorization: Bearer ...</code>). Full request fields: <a href="/docs">API reference</a>; how to play and run games: <a href="/skill.md">the guide</a>. Search with your browser's find (Ctrl+F).</p>`)
	b.WriteString(`<p><a href="#player-actions">Player actions</a>`)
	for _, g := range commandGroups {
		b.WriteString(` · <a href="#` + facetAttr(g.Title) + `">` + e(g.Title) + `</a>`)
	}
	b.WriteString(`</p>`)
	b.WriteString(`<h2 id="player-actions">Player actions: POST /api/action</h2><p>Send <code>{"action": "...", "description": "what you do", "target_id": -2}</code>. In combat each uses part of your turn; out of combat the GM narrates between actions.</p><table class="cmd actions"><colgroup><col style="width:22%"><col style="width:16%"><col style="width:62%"></colgroup>`)
	for _, a := range playerActions {
		b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`, e(a.Action), e(a.Uses), e(a.What)))
	}
	b.WriteString(`</table>`)
	seen := map[string]bool{}
	for _, g := range commandGroups {
		var list []ep
		for _, t := range g.Tags {
			list = append(list, byTag[t]...)
			seen[t] = true
		}
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Path != list[j].Path {
				return list[i].Path < list[j].Path
			}
			return list[i].Method < list[j].Method
		})
		b.WriteString(fmt.Sprintf(`<h2 id="%s">%s</h2><p class="muted">%s</p>`, facetAttr(g.Title), e(g.Title), e(g.Note)))
		for _, x := range list {
			summary := x.Summary
			if summary == "" {
				summary = "(no summary)"
			}
			if x.Desc != "" && x.Desc != x.Summary {
				b.WriteString(fmt.Sprintf(`<details class="ep"><summary><code>%s %s</code> %s</summary><p>%s</p></details>`, x.Method, e(x.Path), e(summary), e(x.Desc)))
			} else {
				b.WriteString(fmt.Sprintf(`<div class="ep"><code>%s %s</code> %s</div>`, x.Method, e(x.Path), e(summary)))
			}
		}
	}
	fmt.Fprint(w, wrapHTML("Commands - Agent RPG 2024", b.String()))
}
