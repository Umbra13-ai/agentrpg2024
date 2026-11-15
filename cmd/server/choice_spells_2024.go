package main

// The setup steps of the choices in choiceDefs (choices_2024.go) that are built here: the SRD text, the options explained and a
// suggestion in the request. Each is called only through its entry in the table.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

// spellChoice: where a spellcaster's cantrips and prepared spells stand. The "still to choose" line and the setup step both
// read it, so they agree about what is missing.
type spellChoice struct {
	caster       bool                   // the class knows cantrips or prepares spells
	wantCantrips int                    // cantrips still to choose
	maxPrep      int                    // how many level 1+ spells the class prepares
	cantrips     []string               // the cantrips that count against the class's number
	leveled      []string               // the level 1+ spells prepared that count against it
	problems     []string               // what POST /prepare would refuse in the stored list
	known        []string               // known_spells
	prepared     []string               // prepared_spells
	seen         map[string]bool        // every spell in either list
	origin       map[string]originSpell // spells from the background or species
	always       []string               // always-prepared spells (subclass, origin, class features, the Book of Shadows): not counted
	bookShort    int                    // a Wizard's free spellbook spells still to write (SRD 5.2.1 Spellbook)
}

func (sc *spellChoice) done() bool {
	return sc.wantCantrips == 0 && len(sc.leveled) >= sc.maxPrep && len(sc.problems) == 0 && sc.bookShort == 0
}

func spellChoiceState(ch *choiceChar) *spellChoice {
	if ch.spells != nil {
		return ch.spells
	}
	sc := &spellChoice{seen: map[string]bool{}}
	ch.spells = sc
	cl, level := ch.class, ch.level
	var intl, wis, cha int
	var known, prepared []byte
	db.QueryRow(`SELECT intl, wis, cha, COALESCE(known_spells,'[]'), COALESCE(prepared_spells,'[]') FROM characters WHERE id = $1`, ch.id).Scan(&intl, &wis, &cha, &known, &prepared)
	sc.known, sc.prepared = jsonList(known), jsonList(prepared)
	sc.wantCantrips = missingCantrips(ch.id, cl, level)
	sc.maxPrep = game.MaxPreparedSpells(cl, level, intl, wis, cha)
	sc.caster = sc.maxPrep > 0 || sc.wantCantrips > 0 || game.ClassTableInt(cl, level, "Cantrips") > 0
	if !sc.caster {
		return sc
	}
	sc.origin = originSpellsOf(ch.id)
	sc.always = allAlwaysPrepared(ch.id)
	// the prepared list as POST /prepare reads it (a Bard's, Ranger's, Sorcerer's or Warlock's list lives in known_spells)
	prepList := sc.prepared
	if len(classLevelsOf(ch.id)) <= 1 {
		prepList = currentPreparedList(ch.id, cl)
	}
	for _, s := range append(append([]string{}, sc.known...), sc.prepared...) {
		if sc.seen[s] {
			continue
		}
		sc.seen[s] = true
		if _, o := sc.origin[s]; o || containsStr(sc.always, s) {
			continue
		}
		if sp, ok := srdSpellsMemory[s]; ok {
			if sp.Level == 0 {
				sc.cantrips = append(sc.cantrips, s)
			} else if containsStr(prepList, s) {
				sc.leveled = append(sc.leveled, s)
			}
		}
	}
	// a list POST /prepare would refuse (too many, off the class list, too high) keeps the choice open
	sc.problems = preparedListProblems(ch.id)
	if wl := ch.levels["wizard"]; wl > 0 {
		if n := wizardSpellbookTarget(ch.id, wl) - freeSpellbookCount(ch.id, sc.known); n > 0 {
			sc.bookShort = n
		}
	}
	return sc
}

// spellsChoice: a spellcaster's cantrips and prepared spells, as an entry of choiceDefs.
func spellsChoice() *choiceDef {
	return &choiceDef{key: "spells", feature: "Spells",
		// what the class's Features table gives at a level: cantrips, prepared spells, and a Wizard's spellbook spells
		total: func(class, _ string, level int) int {
			n := game.ClassTableInt(class, level, "Cantrips") + game.ClassTableInt(class, level, "Prepared Spells")
			if class == "wizard" && level >= 1 {
				n += 6 + 2*(level-1)
			}
			return n
		},
		state: func(ch *choiceChar) (int, int) {
			sc := spellChoiceState(ch)
			switch {
			case !sc.caster:
				return 0, 0
			case sc.done():
				return 1, 1
			}
			return 0, 1
		},
		line: func(ch *choiceChar, _, _ int) string {
			sc := spellChoiceState(ch)
			var parts []string
			if sc.wantCantrips > 0 {
				parts = append(parts, fmt.Sprintf("%d cantrip(s) to choose", sc.wantCantrips))
			}
			if sc.maxPrep > 0 && len(sc.leveled) == 0 {
				parts = append(parts, fmt.Sprintf("none of your %d level 1+ spells are prepared", sc.maxPrep))
			} else if len(sc.leveled) < sc.maxPrep {
				parts = append(parts, fmt.Sprintf("%d of your %d level 1+ spells prepared; your level allows more", len(sc.leveled), sc.maxPrep))
			}
			if len(sc.problems) > 0 {
				parts = append(parts, "fix your prepared list: "+strings.Join(sc.problems, "; "))
			}
			if sc.bookShort > 0 {
				parts = append(parts, fmt.Sprintf("%d spell(s) to write in your spellbook (PUT /api/characters/%d/spells {\"add\": [...]})", sc.bookShort, ch.id))
			}
			return strings.Join(parts, "; ")
		},
		gain: func(charID int, class, _ string, level int) string {
			var parts []string
			grew := func(col, one, many string) {
				now, before := game.ClassTableInt(class, level, col), game.ClassTableInt(class, level-1, col)
				switch n := now - before; {
				case n == 1:
					parts = append(parts, fmt.Sprintf("1 more %s (%d in all)", one, now))
				case n > 1:
					parts = append(parts, fmt.Sprintf("%d more %s (%d in all)", n, many, now))
				}
			}
			grew("Cantrips", "cantrip", "cantrips")
			grew("Prepared Spells", "prepared spell", "prepared spells")
			if class == "wizard" {
				parts = append(parts, fmt.Sprintf("2 more spells for your spellbook first (PUT /api/characters/%d/spells {\"add\": [...]})", charID))
			}
			return strings.Join(parts, ", ")
		},
		request: func(id int) choiceRequest {
			return choiceRequest{method: "POST", path: fmt.Sprintf("/api/characters/%d/prepare", id), body: `{"spells": [...]}`, how: "with the whole list, cantrips and spells"}
		},
		always: func(ch *choiceChar) bool { return spellChoiceState(ch).caster },
		step:   spellsStep}
}

// spellsStep: the cantrips and spells a caster can choose, each explained, with a list that fits in the request.
func spellsStep(ch *choiceChar) *setupStep {
	sc := spellChoiceState(ch)
	if !sc.caster {
		return nil
	}
	cl, level := ch.class, ch.level
	wantCantrips := sc.wantCantrips
	cantrips, leveled := append([]string{}, sc.cantrips...), append([]string{}, sc.leveled...)
	origin, always := sc.origin, sc.always
	if len(sc.problems) > 0 {
		maxSlot := maxSpellLevelFor(ch.id, cl, level)
		valid := func(cap int, list []string) []string {
			var out []string
			for _, x := range list {
				if sp, ok := srdSpellsMemory[x]; ok && len(out) < cap && onClassList(x, cl) && sp.Level <= maxSlot {
					out = append(out, x)
				}
			}
			return out
		}
		cMax := game.ClassTableInt(cl, level, "Cantrips") + orderExtraCantrips(ch.id)
		cantrips = valid(cMax, cantrips)
		leveled = valid(sc.maxPrep, leveled)
		if need := cMax - len(cantrips); need > wantCantrips {
			wantCantrips = need
		}
	}
	// the spells the character already has aren't options (spellsHeld), except a Wizard's spellbook spells not yet prepared:
	// those are what a Wizard prepares from
	var bookUnprepared []string
	if cl == "wizard" {
		for _, s := range sc.known {
			_, fromOrigin := origin[s]
			if sp, ok := srdSpellsMemory[s]; ok && sp.Level > 0 && !fromOrigin && !containsStr(always, s) && !containsStr(sc.prepared, s) {
				bookUnprepared = append(bookUnprepared, s)
			}
		}
	}
	avoid := spellsHeld(ch.id, bookUnprepared...)
	avoidBase := map[string]bool{}
	for k, v := range avoid {
		avoidBase[k] = v
	}
	var newC []string
	for _, c := range cantripPrefs[cl] {
		if len(newC) < wantCantrips && !avoid[c] && onClassList(c, cl) {
			newC = append(newC, c)
			avoid[c] = true
		}
	}
	for _, c := range getClassSpellList(cl) {
		if sp, ok := srdSpellsMemory[c]; ok && sp.Level == 0 && len(newC) < wantCantrips && !avoid[c] {
			newC = append(newC, c)
			avoid[c] = true
		}
	}
	spellsDone := wantCantrips == 0 && len(leveled) >= sc.maxPrep && len(sc.problems) == 0 && sc.bookShort == 0
	maxLvl := (level + 1) / 2 // full casters and Pact Magic: level 2 spells from level 3
	if cl == "paladin" || cl == "ranger" {
		maxLvl = (level + 3) / 4
	}
	if maxLvl > 5 {
		maxLvl = 5
	}
	var pick []string
	for _, list := range [][]string{spell2Prefs[cl], spellPrefs[cl], getClassSpellList(cl)} { // the suggestions first, then the class list
		for _, s := range list {
			if len(leveled)+len(pick) < sc.maxPrep && !avoid[s] && onClassList(s, cl) && (cl != "wizard" || inSpellbookStrict(ch.id, s)) { // a Wizard prepares only from the spellbook (Nov 13: the step suggested Burning Hands, not in the book, and every try failed)
				if sp, ok := srdSpellsMemory[s]; ok && sp.Level >= 1 && sp.Level <= maxLvl {
					pick = append(pick, s)
					avoid[s] = true
				}
			}
		}
	}
	st := setupStep{Key: "spells", Title: "Choose your cantrips and prepare your spells",
		Why: fmt.Sprintf("You know %d cantrips (%d more to choose) and prepare %d level 1+ spells (%d prepared now). Cantrips and spells must be on your class's list: GET /api/universe/class-spells/%s. Origin spells (Magic Initiate, lineage) are extra.", len(cantrips)+wantCantrips, wantCantrips, sc.maxPrep, len(leveled), cl)}
	if len(sc.problems) > 0 {
		st.Problem = "Your prepared list can't stay as it is: " + strings.Join(sc.problems, "; ") + ". The request sends a list that fits."
	}
	if !spellsDone {
		all := append(append(append([]string{}, cantrips...), newC...), leveled...)
		all = append(all, pick...)
		suggestedSet := map[string]bool{}
		for _, x := range all {
			suggestedSet[x] = true
		}
		classList := getClassSpellList(cl)
		if cl == "bard" && level >= 10 { // Magical Secrets: the Cleric, Druid and Wizard lists count as Bard spells (SRD 5.2.1)
			seenL := map[string]bool{}
			for _, x := range classList {
				seenL[x] = true
			}
			for _, other := range []string{"cleric", "druid", "wizard"} {
				for _, x := range getClassSpellList(other) {
					if !seenL[x] {
						seenL[x] = true
						classList = append(classList, x)
					}
				}
			}
			st.Why += " Magical Secrets: " + classFeatureText("bard", "Magical Secrets") + " The options below include those lists."
		}
		var originList []string
		for o := range origin {
			originList = append(originList, o)
		}
		sort.Strings(originList)
		have := map[string]interface{}{"cantrips": cantrips, "prepared_level_1_plus": leveled, "always_prepared_extra (Magic Initiate, lineage)": originList}
		if fa := featureAlwaysPreparedInfo(ch.id); len(fa) > 0 {
			have["always_prepared_by_class_features"] = fa // Druidic's Speak with Animals, Favored Enemy's Hunter's Mark ... (F11)
		}
		st.Choices = map[string]interface{}{
			"how_to_read_this": "Each option says what the spell does, how it is cast and whether it needs concentration. suggested: true marks the picks in the request body, which are the server's suggestion. Send your cantrips and spells together in one list: POST /api/characters/N/prepare {\"spells\": [...]}.",
			"already_have":     have,
			"cantrips":         map[string]interface{}{"choose_this_many_more": wantCantrips, "options": spellOptions(classList, 0, 0, avoidBase, suggestedSet)},
			"level_1_plus_spells": map[string]interface{}{"prepare_up_to": sc.maxPrep, "already_prepared": len(leveled),
				"highest_spell_level_you_can_cast": maxLvl, "you_can_change_prepared_spells": "after a Long Rest",
				"options": spellOptions(classList, 1, maxLvl, avoidBase, suggestedSet)},
		}
		if cl == "wizard" {
			// origin spells (Magic Initiate) and copied spells aren't among the free spellbook spells (F14)
			book := freeSpellbookCount(ch.id, sc.known)
			var add []string
			target := wizardSpellbookTarget(ch.id, level) // 6 spells at level 1, two more each level (SRD 5.2.1 Spellbook)
			// the whole Wizard list after the preferred spells, highest level first (Nov 13: the preferred lists ran out at level 7)
			var fullList []string
			for lv := maxLvl; lv >= 1; lv-- {
				for _, k := range getClassSpellList("wizard") {
					if sp, ok := srdSpellsMemory[k]; ok && sp.Level == lv {
						fullList = append(fullList, k)
					}
				}
			}
			if book < target {
				// an Evoker's Evocation Savant spells first: only Evocation spells fill them (lowest level first, for the two
				// of level 2 or lower)
				if _, _, open := savantFit(ch.id, level, sc.known); open > 0 {
					for lv := 1; lv <= maxLvl; lv++ {
						for _, k := range getClassSpellList("wizard") {
							sp, ok := srdSpellsMemory[k]
							if !ok || sp.Level != lv || !strings.EqualFold(sp.School, "evocation") || containsStr(sc.known, k) || containsStr(add, k) || len(add) >= target-book {
								continue
							}
							if _, _, o2 := savantFit(ch.id, level, append(append([]string{}, sc.known...), append(add, k)...)); o2 < open {
								add = append(add, k)
								open = o2
							}
						}
					}
				}
				for _, list := range [][]string{spell2Prefs["wizard"], spellPrefs["wizard"], fullList} {
					for _, s := range list {
						if sp, ok := srdSpellsMemory[s]; ok && len(add) < target-book && sp.Level >= 1 && sp.Level <= maxLvl && onClassList(s, "wizard") && !containsStr(sc.known, s) && !containsStr(add, s) {
							add = append(add, s)
						}
					}
				}
			}
			add = append(add, newC...)
			st.Why += " A Wizard writes spells in the spellbook (6 at level 1, two more each level: the first request), then prepares from it (the second)."
			if st.Choices != nil {
				st.Choices["wizard_spellbook"] = map[string]interface{}{"spells_in_your_spellbook_now": book, "spellbook_should_hold": target, "write_this_many_more": target - book,
					"note": "A Wizard first writes spells into the spellbook (PUT /api/characters/N/spells {\"add\": [...]}), then prepares spells from the book (POST /api/characters/N/prepare). Choose the spellbook spells from level_1_plus_spells.options."}
			}
			if len(add) > 0 {
				st.Request = req("PUT", fmt.Sprintf("/api/characters/%d/spells", ch.id), map[string]interface{}{"add": add})
				var prep []string
				for _, s := range append(append([]string{}, cantrips...), newC...) {
					prep = append(prep, s)
				}
				n := 0
				for _, s := range append(append([]string{}, leveled...), add...) {
					if sp, ok := srdSpellsMemory[s]; ok && sp.Level > 0 && n < sc.maxPrep && !containsStr(prep, s) {
						prep = append(prep, s)
						n++
					}
				}
				st.Then = &setupStep{Key: "prepare", Title: "Prepare spells from your spellbook", Request: req("POST", fmt.Sprintf("/api/characters/%d/prepare", ch.id), map[string]interface{}{"spells": prep})}
			} else {
				st.Request = req("POST", fmt.Sprintf("/api/characters/%d/prepare", ch.id), map[string]interface{}{"spells": all})
			}
		} else {
			st.Request = req("POST", fmt.Sprintf("/api/characters/%d/prepare", ch.id), map[string]interface{}{"spells": all})
			st.Why += " Send your cantrips and spells together in one list."
		}
	}
	return &st
}
