package main

// Feature words (Nov 9 2026, the project owner: "are there too many commands for AI to reliably figure out?"). Many class features, invocations,
// Metamagic options and magic items are switched on by a word in an action's description ("fireball, sculpt", "attack, stunning
// strike"). Those words were documented only in skill.md, so an AI player who hadn't read the right line never used the feature.
//
//	featureWordsFor(charID)   the words THIS character can use right now, by action: shown on GET /api/my-turn (your_options.feature_words)
//	                          and GET /api/characters/{id} (feature_words)
//	"features": [...]         POST /api/action takes the words as a list: each is checked against featureWordsFor (an unknown word is
//	                          refused with the valid list) and added to the description, so every feature parser sees it
//	hasWord(text, word)       whole-word matching for the words that could appear in ordinary prose ("free", "force", "careful",
//	                          "sculpt", "signature", "unarmed"...): "a forceful punch" no longer means Force damage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var wordRes sync.Map // word -> *regexp.Regexp

// hasWord: the word (or phrase) appears in the text as whole words, ignoring case.
func hasWord(text, word string) bool {
	v, ok := wordRes.Load(word)
	if !ok {
		v = regexp.MustCompile(`(?i)(^|[^a-z'])` + regexp.QuoteMeta(strings.ToLower(word)) + `($|[^a-z])`)
		wordRes.Store(word, v)
	}
	return v.(*regexp.Regexp).MatchString(text)
}

// featureWord is one word a character can use, the action it goes with, and what it does.
type featureWord struct {
	Word   string `json:"word"`             // write it in the description, or send it in "features"
	Action string `json:"action"`           // cast, attack, use_item, any
	What   string `json:"what"`             // what it does (short)
	Detail string `json:"detail,omitempty"` // the character's own choices (the spells, the options left)
}

type featureCtx struct {
	ID       int
	Class    string
	Sub      string
	Level    int
	Levels   map[string]int
	Choices  map[string]string
	Species  string
	Ancestry string
}

func (c featureCtx) lv(class string) int {
	if n := c.Levels[class]; n > 0 {
		return n
	}
	if strings.EqualFold(c.Class, class) {
		return c.Level
	}
	return 0
}

func (c featureCtx) sub(class, sub string, level int) bool {
	return c.lv(class) >= level && strings.EqualFold(c.Sub, sub)
}

func charJSONList(charID int, col string) []string { // a JSON string-array column of the character
	var raw []byte
	db.QueryRow(`SELECT COALESCE(`+col+`::text,'[]') FROM characters WHERE id = $1`, charID).Scan(&raw)
	var out []string
	json.Unmarshal(raw, &out)
	return out
}

// featureWordsFor lists the feature words the character has, sorted by action then word.
func featureWordsFor(charID int) []featureWord {
	if db == nil {
		return nil
	}
	var ctx featureCtx
	ctx.ID = charID
	var choicesRaw, originRaw []byte
	if db.QueryRow(`SELECT class, COALESCE(subclass,''), level, COALESCE(subclass_choices,'{}'), COALESCE(species,''), COALESCE(origin,'{}') FROM characters WHERE id = $1`, charID).
		Scan(&ctx.Class, &ctx.Sub, &ctx.Level, &choicesRaw, &ctx.Species, &originRaw) != nil {
		return nil
	}
	ctx.Class, ctx.Sub = strings.ToLower(ctx.Class), strings.ToLower(ctx.Sub)
	ctx.Levels = map[string]int{}
	for k, v := range classLevelsOf(charID) {
		ctx.Levels[strings.ToLower(k)] = v
	}
	json.Unmarshal(choicesRaw, &ctx.Choices)
	if rec, ok := loadOriginRecord(originRaw); ok && rec.Species.Key == "goliath" {
		ctx.Ancestry = rec.Species.Choices["ancestry"]
	}
	var out []featureWord
	add := func(word, action, what, detail string) {
		out = append(out, featureWord{word, action, what, detail})
	}

	// everyone
	add("unarmed", "attack", "an Unarmed Strike (a punch or kick) even with a weapon in hand", "")

	// casting, any class
	if ctx.Level > 0 && (len(charJSONList(charID, "prepared_spells")) > 0 || len(charJSONList(charID, "known_spells")) > 0) {
		add("ritual", "cast", "cast a Ritual spell as a Ritual: 10 minutes longer, no spell slot", "")
		add("at level N", "cast", "upcast with a level N slot (or send slot_level)", "")
	}
	if hasSpecificFeat(charID, "magic_initiate") || hasSpecificFeat(charID, "magic-initiate") || strings.Contains(strings.ToLower(ctx.Species), "elf") ||
		strings.Contains(strings.ToLower(ctx.Species), "tiefling") || strings.Contains(strings.ToLower(ctx.Species), "gnome") {
		add("free", "cast", "a species lineage or Magic Initiate spell without a slot (once a Long Rest)", "")
	}

	// class features
	if ctx.lv("cleric") >= 10 {
		add("divine intervention", "cast", "a Cleric spell of level 5 or lower without a slot or Material components (Wish at level 20), once a Long Rest", "")
	}
	if ctx.lv("druid") >= 2 {
		add("wild companion", "cast", "Find Familiar for a use of Wild Shape, no slot or Material component", "")
	}
	if ctx.sub("druid", "land", 6) {
		add("natural recovery", "cast", "one Circle of the Land spell without a slot, once a Long Rest", "")
	}
	if ctx.lv("druid") >= 7 && ctx.Choices["elemental_fury"] != "potent_spellcasting" {
		add("primal strike cold|fire|lightning|thunder", "attack", "Elemental Fury: +1d8 (2d8 at 15) of that type on a weapon hit, once a turn", "")
	}
	if ctx.lv("ranger") >= 1 {
		add("favored enemy", "cast", "Hunter's Mark without a slot (uses = Favored Enemy)", "")
	}
	if ctx.lv("paladin") >= 5 {
		add("faithful steed", "cast", "Find Steed without a slot, once a Long Rest", "")
	}
	if ctx.lv("paladin") >= 1 {
		opts := "poisoned"
		if ctx.lv("paladin") >= 14 {
			opts += ", blinded, charmed, deafened, frightened, paralyzed, stunned (Restoring Touch)"
		}
		add("cure <effect>", "lay_on_hands", "5 points of the pool end a condition (\"cure poisoned on Kestrel\"); a number heals that much", opts)
	}
	if ctx.lv("paladin") >= 2 {
		add("divine smite", "attack", "Divine Smite on the hit (a Bonus Action spell); \"divine smite 3\" for a level 3 slot", "")
		add("paladin's smite", "attack", "Divine Smite at level 1 without a slot, once a Long Rest", "")
	}
	if ctx.lv("warlock") >= 9 {
		add("contact patron", "cast", "Contact Other Plane without a slot; the save succeeds automatically (once a Long Rest)", "")
	}
	if ctx.lv("warlock") >= 11 {
		var arc map[string]string
		var raw []byte
		db.QueryRow(`SELECT COALESCE(mystic_arcanum,'{}') FROM characters WHERE id = $1`, charID).Scan(&raw)
		json.Unmarshal(raw, &arc)
		keys := make([]string, 0, len(arc))
		for k := range arc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var list []string
		for _, k := range keys {
			list = append(list, "level "+k+": "+arc[k])
		}
		add("mystic arcanum", "cast", "an Arcanum spell (levels 6-9) without a slot, each once a Long Rest", strings.Join(list, ", "))
	}
	if ctx.lv("wizard") >= 18 {
		add("spell mastery", "cast", "a Spell Mastery spell at its lowest level without a slot", strings.Join(charJSONList(charID, "spell_mastery"), ", "))
	}
	if ctx.lv("wizard") >= 20 {
		add("signature", "cast", "a Signature Spell at level 3 without a slot, each once a Short or Long Rest", strings.Join(charJSONList(charID, "signature_spells"), ", "))
	}
	if ctx.sub("wizard", "evocation", 3) {
		add("sculpt", "cast", "Sculpt Spells: 1 + the spell's level of the characters in a Wizard Evocation spell's area succeed and take no damage", "")
	}
	if ctx.sub("wizard", "evocation", 14) {
		add("overchannel", "cast", "a level 1-5 Wizard damage spell deals maximum damage (free once a Long Rest; then Necrotic damage to you)", "")
	}
	if ctx.lv("sorcerer") >= 2 {
		mm := charJSONList(charID, "metamagic_choices")
		for _, m := range mm {
			add(m, "cast", "Metamagic: "+strings.Title(m)+" Spell (Sorcery Points)", "")
		}
	}
	if ctx.sub("sorcerer", "draconic", 18) {
		add("dragon companion", "cast", "Summon Dragon without a slot or Material component, once a Long Rest", "")
	}
	if ctx.lv("barbarian") >= 2 {
		add("reckless", "attack", "Reckless Attack: Advantage on Strength attacks this turn; attacks against you have Advantage", "")
	}
	if ctx.lv("barbarian") >= 9 {
		opts := "forceful blow, hamstring blow"
		if ctx.lv("barbarian") >= 13 {
			opts += ", staggering blow, sundering blow"
		}
		add("brutal strike", "attack", "with Reckless Attack: give up the Advantage for +1d10 (2d10 at 17) and an effect", opts)
	}
	if ctx.lv("monk") >= 5 {
		add("stunning strike", "attack", "a Focus Point on a hit: Constitution save or Stunned", "")
	}
	if ctx.lv("monk") >= 6 {
		add("force", "attack", "Empowered Strikes: an Unarmed Strike deals Force damage", "")
	}
	if ctx.sub("monk", "open-hand", 3) || ctx.sub("monk", "open hand", 3) || ctx.sub("monk", "openhand", 3) {
		add("addle|push|topple", "attack", "Open Hand Technique on a Flurry of Blows hit", "")
	}
	if ctx.lv("rogue") >= 5 {
		opts := "poison, trip, withdraw"
		if ctx.lv("rogue") >= 14 {
			opts += ", daze, knock out, obscure"
		}
		if ctx.sub("rogue", "thief", 9) {
			opts += ", stealth attack"
		}
		n := "one effect"
		if ctx.lv("rogue") >= 11 {
			n = "two effects"
		}
		add("cunning strike <effect>", "attack", "trade Sneak Attack dice for "+n, opts)
	}
	if ctx.lv("warlock") >= 1 {
		for _, inv := range []struct{ slug, word, what string }{
			{"eldritch-smite", "eldritch smite", "a Pact slot on a pact weapon hit: +(1 + slot level)d8 Force and Prone"},
			{"lifedrinker", "lifedrinker necrotic|psychic|radiant", "+1d6 of that type on a pact weapon hit, once a turn"},
		} {
			if hasInvocation(charID, inv.slug) {
				add(inv.word, "attack", inv.what, "")
			}
		}
	}
	if ctx.Ancestry != "" {
		add(strings.ToLower(giantBoonName(ctx.Ancestry)), "attack", "Giant Ancestry on a hit (uses = Proficiency Bonus)", "")
	}

	// magic items that take a word
	for _, it := range inventoryOf(charID) {
		n := strings.ToLower(itemStr(it, "name"))
		switch {
		case strings.HasPrefix(n, "scimitar of speed"):
			add("(none)", "bonus_attack", "Scimitar of Speed (attuned, in hand): action bonus_attack, one attack with it as a Bonus Action each turn", "")
		case strings.HasPrefix(n, "necklace of fireballs"):
			add("N beads", "use_item", "Necklace of Fireballs: throw N beads (target_id / target_ids in the Sphere)", "")
		case strings.HasPrefix(n, "rod of absorption"):
			add("absorb level N", "use_item", "Rod of Absorption: a Reaction to absorb a level N spell that targets only you", "")
			add("convert level N", "use_item", "Rod of Absorption: stored energy becomes a level N slot (1-5)", "")
		case strings.HasPrefix(n, "mysterious deck"):
			add("draw N", "use_item", "Mysterious Deck: draw N cards", "")
		case strings.HasPrefix(n, "sphere of annihilation"):
			add("control|move", "use_item", "Sphere of Annihilation", "")
		case strings.HasPrefix(n, "iron flask"):
			add("release", "use_item", "Iron Flask: release the creature inside (or target_id to trap one)", "")
		case strings.HasPrefix(n, "apparatus of the crab"):
			add("lever N up|down", "use_item", "Apparatus of the Crab", "")
		case strings.HasPrefix(n, "flame blade"), strings.HasPrefix(n, "flame tongue"), strings.HasPrefix(n, "boots of speed"), strings.HasPrefix(n, "eversmoking bottle"),
			strings.HasPrefix(n, "wings of flying"), strings.HasPrefix(n, "dancing sword"):
			add("(none)", "use_item", itemStr(it, "name")+": use_item toggles or uses it (the Dancing Sword takes target_id)", "")
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return dedupeFeatureWords(out)
}

func dedupeFeatureWords(in []featureWord) []featureWord {
	seen := map[string]bool{}
	var out []featureWord
	for _, f := range in {
		k := f.Action + "|" + f.Word + "|" + f.What
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}

// featureListToText checks the "features" of an action against the character's feature words and returns the text to add to the
// description, or a refusal that lists the valid words for that action.
func featureListToText(charID int, action string, features []string) (string, string) {
	if len(features) == 0 {
		return "", ""
	}
	have := featureWordsFor(charID)
	var valid []string
	ok := func(f string) bool {
		f = strings.ToLower(strings.TrimSpace(f))
		for _, w := range have {
			if w.Action != action && w.Action != "any" {
				continue
			}
			for _, alt := range strings.Split(w.Word, "|") {
				if featurePattern(strings.TrimSpace(alt)).MatchString(f) {
					return true
				}
			}
		}
		return false
	}
	for _, w := range have {
		if w.Action == action {
			valid = append(valid, w.Word)
		}
	}
	var bad []string
	for _, f := range features {
		if !ok(f) {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		list := "none"
		if len(valid) > 0 {
			list = strings.Join(valid, "; ")
		}
		return "", fmt.Sprintf("Unknown feature word(s) for %s: %s. This character's words for %s: %s (GET /api/my-turn lists them under your_options.feature_words). Nothing was spent.",
			action, strings.Join(bad, ", "), action, list)
	}
	return " [" + strings.Join(features, ", ") + "]", ""
}

// featurePattern: a feature word as a pattern: "N" is a number, "<effect>" any text ("cunning strike <effect>", "absorb level N").
func featurePattern(alt string) *regexp.Regexp {
	p := regexp.QuoteMeta(strings.ToLower(alt))
	p = strings.ReplaceAll(p, "<effect>", ".+")
	p = regexp.MustCompile(`(^|\s)n(\s|$)`).ReplaceAllString(p, `${1}\d+${2}`)
	return regexp.MustCompile("^" + p + "$")
}

// featureAction: the feature-word group of an action (a bonus or reaction cast is a cast; an opportunity attack an attack).
func featureAction(action string) string {
	switch action {
	case "cast", "bonus_cast", "reaction_cast":
		return "cast"
	case "attack", "opportunity_attack", "bonus_attack", "offhand_attack", "flurry_of_blows", "martial_arts", "frenzy_attack":
		return "attack"
	case "use_item", "item_bonus":
		return "use_item"
	}
	return action
}

// @Summary GM: Advantage or Disadvantage on a character's next attack roll
// @Description A GM ruling the server doesn't track itself (an unseen attacker the map doesn't know about, a distracting trick, cover
// broken by the scenery): the character's next attack roll has Advantage (mode "advantage") or Disadvantage ("disadvantage"). Both on
// the same roll cancel, as usual. Players can't claim Advantage in an action's description (F139): the server works it out from the
// conditions, Help, features and this ruling.
// @Tags GM
// @Param request body object{character_id=int,mode=string,reason=string} true "The character, advantage or disadvantage, and why"
// @Router /gm/grant-advantage [post]
func handleGMGrantAdvantage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "POST required"})
		return
	}
	_, lobby, ok := gmCampaignOf(w, r)
	if !ok {
		return
	}
	var req struct {
		CharacterID int    `json:"character_id"`
		Mode        string `json:"mode"`
		Reason      string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if lobbyOfChar(req.CharacterID) != lobby {
		dmBadRequest(w, "character_not_in_campaign", "That character isn't in this campaign.")
		return
	}
	kind := "gm_advantage"
	switch strings.ToLower(strings.TrimSpace(req.Mode)) {
	case "", "advantage":
	case "disadvantage":
		kind = "gm_disadvantage"
	default:
		dmBadRequest(w, "bad_mode", `mode is "advantage" or "disadvantage".`)
		return
	}
	addCombatEffect(lobby, req.CharacterID, -1, kind)
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "character_id": req.CharacterID, "mode": strings.TrimPrefix(kind, "gm_"),
		"message": fmt.Sprintf("%s's next attack roll has %s%s.", characterNameOf(req.CharacterID), strings.Title(strings.TrimPrefix(kind, "gm_")),
			map[bool]string{true: " (" + req.Reason + ")", false: ""}[req.Reason != ""])})
}

func init() {
	effectText["gm_advantage"] = "Advantage on the next attack roll (the GM's ruling)"
	effectText["gm_disadvantage"] = "Disadvantage on the next attack roll (the GM's ruling)"
}
