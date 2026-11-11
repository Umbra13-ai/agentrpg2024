package main

// The 2024 actions that are ability checks (SRD 5.2.1 Rules Glossary): Search (a Wisdom check: Insight, Medicine, Perception or
// Survival), Study (an Intelligence check: Arcana, History, Investigation, Nature or Religion) and Influence (Charisma: Deception,
// Intimidation, Performance or Persuasion; or Wisdom: Animal Handling). The skill is chosen from the player's description, the
// way the Search and Areas of Knowledge tables and the Influence Checks table pair skills with what is being done.
// Also the 2024 action names Magic (cast a spell or use a magic item or feature) and Utilize (use an object that needs an action).

import (
	"fmt"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// canonicalAction lower-cases an action name and maps the 2024 names onto the commands that carry them out.
func canonicalAction(action string) string {
	a := strings.ToLower(strings.TrimSpace(action))
	switch a {
	case "magic":
		return "cast"
	case "utilize", "drink", "drink_potion", "quaff":
		return "use_item"
	}
	return a
}

type skillChoice struct {
	skill    string
	ability  string
	keywords []string
}

// The Search table: Insight (a creature's state of mind), Medicine (a creature's ailment or cause of death), Perception (a concealed
// creature or object), Survival (tracks or food). Perception is the default.
var searchSkills = []skillChoice{
	{"insight", "wis", []string{"insight", "state of mind", "mood", "lying", " lie", "intent", "motive", "body language", "sincere", "trust"}},
	{"medicine", "wis", []string{"medicine", "ailment", "illness", "disease", "wound", "injur", "cause of death", "what killed", "diagnos", "poisoned"}},
	{"survival", "wis", []string{"survival", "track", "trail", "forage", "food", "spoor", "footprint", "hunt for"}},
	{"perception", "wis", []string{"perception"}},
}

// The Areas of Knowledge table for the Study action. Investigation is the default (clues, traps, ciphers, riddles, gadgetry).
var studySkills = []skillChoice{
	{"arcana", "int", []string{"arcana", "spell", "magic", "eldritch", " rune", "sigil", "plane", "aberration", "construct", "elemental", "fey", "monstrosit", "enchant", "arcane"}},
	{"history", "int", []string{"history", "historic", "ancient", "civilization", " war", "battle of", "kingdom", "empire", "dynasty", "giant", "humanoid", "heraldry", "noble house"}},
	{"nature", "int", []string{"nature", "terrain", "flora", "plant", "weather", "beast", "animal", "dragon", "ooze", "fungus", "herb", "season"}},
	{"religion", "int", []string{"religion", "deity", " god", "goddess", "temple", " rite", "ritual", "holy symbol", "cult", "celestial", "fiend", "undead", "divine", "priest"}},
	{"investigation", "int", []string{"investigation", "investigate", "trap", "cipher", "riddle", "gadget", "mechanism", "clue", "deduce", "analyze"}},
}

// The Influence Checks table. Persuasion is the default.
var influenceSkills = []skillChoice{
	{"deception", "cha", []string{"deception", "deceive", " lie", "bluff", "trick", "con ", "disguise", "pretend", "mislead"}},
	{"intimidation", "cha", []string{"intimidat", "threat", "threaten", "scare", "menace", "cow ", "bully"}},
	{"performance", "cha", []string{"performance", "perform", "amuse", "entertain", "joke", "song", " sing ", "dance", "story"}},
	{"animal handling", "wis", []string{"animal handling", "coax", "calm the", "soothe", "beast", "animal", "horse", "dog", "monstrosity"}},
	{"persuasion", "cha", []string{"persuasion", "persuade", "convince", " ask", "plead", "negotiate", "reason with"}},
}

// pickSkill takes the first skill named outright in the description, then the first whose keywords appear, then the default.
func pickSkill(description string, choices []skillChoice, def string) skillChoice {
	d := " " + strings.ToLower(description) + " "
	for _, c := range choices {
		if strings.Contains(d, c.skill) {
			return c
		}
	}
	for _, c := range choices {
		for _, k := range c.keywords {
			if strings.Contains(d, k) {
				return c
			}
		}
	}
	for _, c := range choices {
		if c.skill == def {
			return c
		}
	}
	return choices[0]
}

// checkBonus is the character's bonus to an ability check with one skill: the ability modifier plus proficiency (doubled by
// Expertise, or half for a Bard's Jack of All Trades), and whether Reliable Talent applies.
func checkBonus(charID int, class string, level int, skill string, abilityScore int) (mod, prof int, profNote string, reliable bool) {
	mod = game.Modifier(abilityScore)
	var profs, expertise string
	db.QueryRow("SELECT COALESCE(skill_proficiencies, ''), COALESCE(expertise, '') FROM characters WHERE id = $1", charID).Scan(&profs, &expertise)
	profs, expertise = strings.ReplaceAll(strings.ToLower(profs), "_", " "), strings.ReplaceAll(strings.ToLower(expertise), "_", " ")
	skill = strings.ReplaceAll(skill, "_", " ")
	proficient := strings.Contains(profs, skill)
	switch {
	case proficient && strings.Contains(expertise, skill):
		prof, profNote = game.ProficiencyBonus(level)*2, " (expertise)"
	case proficient:
		prof, profNote = game.ProficiencyBonus(level), " (proficient)"
	case strings.EqualFold(class, "bard") && level >= 2:
		prof, profNote = game.ProficiencyBonus(level)/2, " (Jack of All Trades)"
	}
	reliable = proficient && strings.EqualFold(class, "rogue") && level >= 11
	if v, ok := wildShapeBeastMod(charID, skill); ok && v > mod+prof {
		mod = v - prof // Wild Shape: the beast's skill modifier when it's higher
	}
	mod += magicCheckBonus(charID)         // Stone of Good Luck
	mod += glovesOfThievery(charID, skill) // F165 (items_c5_2024.go)
	return
}

// characterSkillProficiency reports whether a character is proficient in a skill, and whether they have Expertise in it.
func characterSkillProficiency(charID int, skill string) (proficient, expert bool) {
	var profs, expertise string
	db.QueryRow("SELECT COALESCE(skill_proficiencies, ''), COALESCE(expertise, '') FROM characters WHERE id = $1", charID).Scan(&profs, &expertise)
	k := normSkill(skill)
	for _, p := range game.ParseProficiencyList(profs) {
		if normSkill(p) == k {
			proficient = true
		}
	}
	if proficient {
		for _, e := range game.ParseProficiencyList(expertise) {
			if normSkill(e) == k {
				expert = true
			}
		}
	}
	return
}

// reliableRoll applies Reliable Talent (a d20 roll of 9 or lower counts as 10).
func reliableRoll(roll int, reliable bool) (int, string) {
	if reliable && roll <= 9 {
		return 10, fmt.Sprintf(" (Reliable Talent: %d→10)", roll)
	}
	return roll, ""
}

func titleSkill(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// resolveCheckAction rolls the Search, Study or Influence action.
func resolveCheckAction(action, description string, charID int, class string, level, intl, wis, cha int) string {
	scores := map[string]int{"int": intl, "wis": wis, "cha": cha}
	var c skillChoice
	switch action {
	case "search":
		c = pickSkill(description, searchSkills, "perception")
	case "study":
		c = pickSkill(description, studySkills, "investigation")
	default:
		c = pickSkill(description, influenceSkills, "persuasion")
	}
	ab := strings.ToUpper(c.ability)
	mod, prof, profNote, reliable := checkBonus(charID, class, level, c.skill, scores[c.ability])
	guide, guideNote := guidanceBonus(lobbyOfChar(charID), charID, c.skill)
	bonus := mod + prof + guide
	if action != "influence" {
		d, bAdv := buffCheckRoll(charID, c.ability, game.RollDie(20)) // Foresight, Enhance Ability
		guideNote += bAdv
		if pAdv, pDis, pNote := travelPaceEffect(lobbyOfChar(charID), c.skill); pAdv || pDis { // travel pace on Perception and Survival
			d2 := game.RollDie(20)
			if (pAdv && d2 > d) || (pDis && d2 < d) {
				d = d2
			}
			guideNote += pNote
		}
		roll, rtNote := reliableRoll(d, reliable)
		icon := map[string]string{"search": "🔍 Search", "study": "📚 Study"}[action]
		return fmt.Sprintf("%s (%s/%s)%s: %d (d20: %d%s + %d %s + %d prof%s). %s",
			icon, titleSkill(c.skill), ab, profNote, roll+bonus, roll, rtNote+guideNote, mod, ab, prof, map[bool]string{true: fmt.Sprintf(" + %d Guidance", guide)}[guide != 0], description)
	}
	// Influence: when the GM has recorded the creature's attitude (attitudes_2024.go), it decides Advantage or Disadvantage.
	lobby := lobbyOfChar(charID)
	tid := 0
	if _, mid, ok := attackedMonster(charID); ok {
		tid = mid
	}
	if att, who, ok := attitudeFor(lobby, tid, description); ok {
		d1, d2 := game.RollDie(20), game.RollDie(20)
		d, how := d1, "no Advantage or Disadvantage"
		switch att {
		case "friendly":
			if d2 > d {
				d = d2
			}
			how = fmt.Sprintf("Advantage, d20s %d and %d", d1, d2)
		case "hostile":
			if d2 < d {
				d = d2
			}
			how = fmt.Sprintf("Disadvantage, d20s %d and %d", d1, d2)
		}
		roll, rt := reliableRoll(d, reliable)
		return fmt.Sprintf("🗣️ Influence (%s/%s)%s on %s, who is %s (%s): %d (d20: %d%s + %d %s + %d prof%s). If %s is hesitant, the GM compares the total with DC 15 or its Intelligence score, whichever is higher; a willing creature needs no check and an unwilling one won't comply. %s",
			titleSkill(c.skill), ab, profNote, who, titleCase(att), how, roll+bonus, roll, rt+guideNote, mod, ab, prof, map[bool]string{true: fmt.Sprintf(" + %d Guidance", guide)}[guide != 0], who, description)
	}
	// Attitude unknown to the server: both d20s are rolled and all three totals are reported; the GM uses the one that fits.
	r1, n1 := reliableRoll(game.RollDie(20), reliable)
	r2, n2 := reliableRoll(game.RollDie(20), reliable)
	hi, lo := r1, r2
	if r2 > r1 {
		hi, lo = r2, r1
	}
	return fmt.Sprintf("🗣️ Influence (%s/%s)%s: %d if the creature is Indifferent, %d if Friendly (Advantage), %d if Hostile (Disadvantage) (d20s: %d%s and %d%s, + %d %s + %d prof%s). "+
		"If the creature is hesitant, the GM compares the total with DC 15 or the creature's Intelligence score, whichever is higher; a willing creature needs no check and an unwilling one won't comply. %s",
		titleSkill(c.skill), ab, profNote, r1+bonus, hi+bonus, lo+bonus, r1, n1, r2, n2+guideNote, mod, ab, prof, map[bool]string{true: fmt.Sprintf(" + %d Guidance", guide)}[guide != 0], description)
}
