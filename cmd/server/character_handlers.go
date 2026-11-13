package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/agentrpg/agentrpg/game"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// handleCharacters godoc
// @Summary List or create characters
// @Description GET: List your characters. POST: Create a new character. Ability scores (before the background increases) come from the Standard Array, a 27-point point buy, or ability_method "random" (the server rolls 4d6 drop lowest six times and assigns them in the order of the scores sent).
// @Tags Characters
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{name=string,class=string,species=string,background=string,str=integer,dex=integer,con=integer,int=integer,wis=integer,cha=integer,ability_method=string} false "Character details (POST only)"
// @Success 200 {object} map[string]interface{} "List of characters or creation result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /characters [get]
// @Router /characters [post]
func handleCharacters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	if r.Method == "GET" {
		rows, _ := db.Query(`
			SELECT id, name, class, species, level, hp, max_hp, ac
			FROM characters WHERE agent_id = $1
		`, agentID)
		defer rows.Close()

		chars := []map[string]interface{}{}
		for rows.Next() {
			var id, level, hp, maxHP, ac int
			var name, class, species string
			rows.Scan(&id, &name, &class, &species, &level, &hp, &maxHP, &ac)
			chars = append(chars, map[string]interface{}{
				"id": id, "name": name, "class": class, "species": species,
				"level": level, "hp": hp, "max_hp": maxHP, "ac": ac,
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"characters": chars})
		return
	}

	if r.Method == "POST" {
		var req struct {
			Name                  string            `json:"name"`
			Class                 string            `json:"class"`
			Species               string            `json:"species"`
			Background            string            `json:"background"`
			Str                   int               `json:"str"`
			Dex                   int               `json:"dex"`
			Con                   int               `json:"con"`
			Int                   int               `json:"int"`
			Wis                   int               `json:"wis"`
			Cha                   int               `json:"cha"`
			SkillProficiencies    []string          `json:"skill_proficiencies"`     // e.g., ["perception", "stealth"]
			ToolProficiencies     []string          `json:"tool_proficiencies"`      // e.g., ["thieves' tools", "herbalism kit"]
			Expertise             []string          `json:"expertise"`               // Rogue only: two skill proficiencies, e.g. ["stealth", "sleight of hand"] (double Proficiency Bonus)
			ExtraLanguages        []string          `json:"extra_languages"`         // exactly two Standard Languages besides Common (2024 rules)
			AbilityIncreases      map[string]int    `json:"ability_increases"`       // from the background: {"int": 2, "wis": 1} or all three at 1
			BackgroundTool        string            `json:"background_tool"`         // Soldier: which Gaming Set
			BackgroundEquipment   string            `json:"background_equipment"`    // "a" (the package, default) or "b" (50 GP)
			SpeciesChoices        map[string]string `json:"species_choices"`         // lineage, ancestry, spellcasting_ability, skill, size, feat ...
			BackgroundFeatChoices *featChoiceInput  `json:"background_feat_choices"` // Magic Initiate or Skilled picks
			SpeciesFeatChoices    *featChoiceInput  `json:"species_feat_choices"`    // the Human's extra Origin feat picks
			WeaponMasteries       []string          `json:"weapon_masteries"`        // Barbarian 2, Fighter 3, Paladin/Ranger/Rogue 2 kinds of weapon
			KnownSpells           []string          `json:"known_spells"`            // e.g., ["fireball", "magic-missile"] - spell slugs character knows
			DraconicAncestry      string            `json:"draconic_ancestry"`       // e.g., "red", "blue" - for Dragonborn breath weapon
			AbilityMethod         string            `json:"ability_method"`          // "standard_array", "point_buy" or "random" (the server rolls); scores before the background increases
			StartingEquipment     string            `json:"starting_equipment"`      // class kit option: "a" (default), "b" or "c" where offered; gold-only options give gold
			Backstory             string            `json:"backstory"`              // the character’s story (the builder asks for 3 to 6 paragraphs)
			Appearance            string            `json:"appearance"`             // optional: how the character looks
			Alignment             string            `json:"alignment"`               // one of the nine alignments (Lawful Good ... Chaotic Evil); a roleplaying guide
			DryRun                bool              `json:"dry_run"` // F234: check everything, create nothing
			ClassOptions          struct {
				DivineOrder string `json:"divine_order"` // Cleric: "protector" or "thaumaturge"
				PrimalOrder string `json:"primal_order"` // Druid: "warden" or "magician"
			} `json:"class_options"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		// F234: no scores at all used to become six 10s (a legal but wasted point buy); every class needs its scores assigned
		if req.Str == 0 && req.Dex == 0 && req.Con == 0 && req.Int == 0 && req.Wis == 0 && req.Cha == 0 && strings.ToLower(strings.TrimSpace(req.AbilityMethod)) != "random" {
			out := map[string]interface{}{"error": "ability_scores_required",
				"message": "Assign your six ability scores (str, dex, con, int, wis, cha) with ability_method standard_array (15, 14, 13, 12, 10, 8), point_buy (27 points) or random. The character builder guides this step by step: POST /api/character-builder."}
			if arr, ok := phbArray[strings.ToLower(req.Class)]; ok {
				out["suggestion"] = map[string]interface{}{"ability_method": "standard_array", "str": arr[0], "dex": arr[1], "con": arr[2], "int": arr[3], "wis": arr[4], "cha": arr[5],
					"source": "the Player's Handbook's Standard Array by Class"}
			}
			json.NewEncoder(w).Encode(out)
			return
		}

		if req.Name == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "name_required"})
			return
		}

		// Check for globally unique character name
		var existingCount int
		db.QueryRow("SELECT COUNT(*) FROM characters WHERE LOWER(name) = LOWER($1)", req.Name).Scan(&existingCount)
		if existingCount > 0 {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_name_taken", "message": "That character name is already in use. Please choose a unique name."})
			return
		}

		if req.Str == 0 {
			req.Str = 10
		}
		if req.Dex == 0 {
			req.Dex = 10
		}
		if req.Con == 0 {
			req.Con = 10
		}
		if req.Int == 0 {
			req.Int = 10
		}
		if req.Wis == 0 {
			req.Wis = 10
		}
		if req.Cha == 0 {
			req.Cha = 10
		}

		// Step 3 of character creation: the Standard Array, point buy or random generation
		base, abilityNote, baseErr := baseAbilityScores(req.AbilityMethod, []int{req.Str, req.Dex, req.Con, req.Int, req.Wis, req.Cha})
		if baseErr != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_ability_scores", "message": baseErr.Error()})
			return
		}
		req.Str, req.Dex, req.Con, req.Int, req.Wis, req.Cha = base[0], base[1], base[2], base[3], base[4], base[5]

		// 2024 rules: species give no ability scores; the background raises them
		speciesKey := normalizeSpeciesSlug(req.Species)
		earlyInc, incErr := checkAbilityIncreases(req.Background, req.AbilityIncreases)
		if incErr != nil {
			writeOriginError(w, incErr)
			return
		}
		req.Str += earlyInc["str"]
		req.Dex += earlyInc["dex"]
		req.Con += earlyInc["con"]
		req.Int += earlyInc["int"]
		req.Wis += earlyInc["wis"]
		req.Cha += earlyInc["cha"]
		if req.Str > 20 || req.Dex > 20 || req.Con > 20 || req.Int > 20 || req.Wis > 20 || req.Cha > 20 {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "ability_score_over_20", "message": "An ability increase cannot raise a score above 20"})
			return
		}

		// Use class hit die from SRD for HP
		classKey := strings.ToLower(req.Class)
		hitDie := 8          // default
		numSkillChoices := 2 // default
		skillChoicesAvailable := map[string]bool{}
		if class, ok := srdClasses[classKey]; ok {
			hitDie = class.HitDie
			// Get skill choices from class (parsed from database at startup)
			var skillChoicesStr string
			var numChoices int
			db.QueryRow(`SELECT COALESCE(skill_choices, ''), COALESCE(num_skill_choices, 2) FROM classes WHERE slug = $1`, classKey).Scan(&skillChoicesStr, &numChoices)
			if numChoices > 0 {
				numSkillChoices = numChoices
			}
			if skillChoicesStr != "" {
				for _, skill := range strings.Split(skillChoicesStr, ",") {
					skillChoicesAvailable[strings.TrimSpace(strings.ToLower(skill))] = true
				}
			}
		}
		hp := hitDie + game.Modifier(req.Con) // Level 1: max hit die + CON mod
		ac := 10 + game.Modifier(req.Dex)

		// F234: a class's skill choices are part of creation (fewer used to be accepted without a word)
		if len(req.SkillProficiencies) < numSkillChoices && len(skillChoicesAvailable) > 0 {
			var opts []string
			for s := range skillChoicesAvailable {
				opts = append(opts, s)
			}
			sort.Strings(opts)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "skill_choices_required",
				"message":       fmt.Sprintf("A %s chooses %d skills from the class's list in skill_proficiencies; you sent %d.", req.Class, numSkillChoices, len(req.SkillProficiencies)),
				"valid_options": opts, "count": numSkillChoices})
			return
		}

		// Validate skill proficiency choices
		skillProfsStr := ""
		if len(req.SkillProficiencies) > 0 {
			// Validate count
			if len(req.SkillProficiencies) > numSkillChoices {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":      "too_many_skills",
					"message":    fmt.Sprintf("Your class allows %d skill proficiencies, you chose %d", numSkillChoices, len(req.SkillProficiencies)),
					"max_skills": numSkillChoices,
				})
				return
			}
			// Validate each skill is available to this class
			validSkills := []string{}
			for _, skill := range req.SkillProficiencies {
				skillLower := strings.ToLower(strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(skill)))
				if len(skillChoicesAvailable) > 0 && !skillChoicesAvailable[skillLower] {
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":   "invalid_skill_choice",
						"message": fmt.Sprintf("'%s' is not available to %s class", skill, req.Class),
						"available_skills": func() []string {
							skills := []string{}
							for s := range skillChoicesAvailable {
								skills = append(skills, s)
							}
							sort.Strings(skills)
							return skills
						}(),
					})
					return
				}
				validSkills = append(validSkills, skillLower)
			}
			skillProfsStr = strings.Join(validSkills, ", ")
		}

		// Tool proficiencies sent here are the class's tool choices (a Bard's three Musical Instruments, a Monk's Artisan's Tools or
		// Musical Instrument); the class's own tools, the background, the species and feats add theirs below. Each is stored under its
		// full name ("musical instrument (lute)", BUGS 45).
		toolProfsStr := game.MergeToolList("", req.ToolProficiencies)
		if picks := game.ParseProficiencyList(toolProfsStr); len(picks) > 0 {
			classTools := game.ProficienciesFor(game.CharacterInput{FirstClass: req.Class}).Tools
			var chosen []string
			for _, t := range picks {
				if !game.HasToolProficiency(classTools, t) {
					chosen = append(chosen, t)
				}
			}
			if len(chosen) > 0 {
				msg := "no_tool_choices_for_this_class"
				if choices := game.ToolChoicesFor(req.Class, nil); len(choices) > 0 {
					_, msg = game.ValidateToolChoices(chosen, choices)
				}
				if msg != "" {
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":           "invalid_tool_proficiencies",
						"message":         "tool_proficiencies is only for the tools your class lets you choose (a Bard's three Musical Instruments, a Monk's Artisan's Tools or Musical Instrument); your class's own tools and those of your background (background_tool), species and feats are added for you. Refused: " + msg,
						"pending_choices": game.ToolChoicesFor(req.Class, nil),
					})
					return
				}
			}
		}

		// Some classes make the player choose tool proficiencies (Bard: three Musical
		// Instruments; Monk: one Artisan's Tools or Musical Instrument).
		if pendingTools := game.PendingToolChoices(game.ToolChoicesFor(req.Class, nil), toolProfsStr); len(pendingTools) > 0 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":           "tool_choice_required",
				"message":         fmt.Sprintf("%s characters choose tool proficiencies at creation.", req.Class),
				"pending_choices": pendingTools,
				"hint":            "Add your choices to tool_proficiencies, for example [\"lute\", \"drum\", \"flute\"].",
			})
			return
		}

		// Level 1 class choices that change proficiencies
		firstWord := func(v string) string { // "thaumaturge (one extra cantrip ...)" means thaumaturge
			if f := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(v)), func(r rune) bool { return r == ' ' || r == '(' || r == ':' || r == ',' }); len(f) > 0 {
				return f[0]
			}
			return ""
		}
		divineOrder := firstWord(req.ClassOptions.DivineOrder)
		primalOrder := firstWord(req.ClassOptions.PrimalOrder)
		req.ClassOptions.DivineOrder, req.ClassOptions.PrimalOrder = divineOrder, primalOrder
		if divineOrder != "" && (strings.ToLower(req.Class) != "cleric" || (divineOrder != "protector" && divineOrder != "thaumaturge")) {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_divine_order", "message": pickMsg(strings.ToLower(req.Class) != "cleric", "Only Clerics choose a divine_order.", fmt.Sprintf("%q is not a Divine Order: choose protector or thaumaturge.", divineOrder))})
			return
		}
		if primalOrder != "" && (strings.ToLower(req.Class) != "druid" || (primalOrder != "warden" && primalOrder != "magician")) {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_primal_order", "message": pickMsg(strings.ToLower(req.Class) != "druid", "Only Druids choose a primal_order.", fmt.Sprintf("%q is not a Primal Order: choose warden or magician.", primalOrder))})
			return
		}

		// Get weapon and armor proficiencies from class
		weaponProfsStr := ""
		armorProfsStr := ""
		if class, ok := srdClasses[classKey]; ok {
			if len(class.WeaponProf) > 0 {
				weaponProfsStr = strings.ToLower(strings.Join(class.WeaponProf, ", "))
			}
			if len(class.ArmorProf) > 0 {
				armorProfsStr = strings.ToLower(strings.Join(class.ArmorProf, ", "))
			}
		}

		// The classes table has no proficiency data, so grant them from the game rules.
		grantedProfs := game.ProficienciesFor(game.CharacterInput{FirstClass: req.Class, DivineOrder: divineOrder, PrimalOrder: primalOrder})
		weaponProfsStr = game.MergeProficiencyList(weaponProfsStr, grantedProfs.Weapons)
		armorProfsStr = game.MergeProficiencyList(armorProfsStr, grantedProfs.Armor)
		toolProfsStr = game.MergeToolList(toolProfsStr, grantedProfs.Tools)

		// Expertise (SRD 5.2.1): a Rogue chooses two of their skill proficiencies at level 1 (checked below, once the background's and
		// species' skills are known). Bards, Rangers and Wizards (Scholar) choose theirs at level 2 with POST /api/characters/{id}/expertise.
		// Expertise is for skills only.
		if len(req.Expertise) > 0 && classKey != "rogue" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "expertise_not_available",
				"message": "Only Rogues choose Expertise at level 1. Bards, Rangers and Wizards choose theirs at level 2 (POST /api/characters/{id}/expertise).",
			})
			return
		}
		if len(req.Expertise) > 2 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "too_many_expertise",
				"message": "A Rogue chooses Expertise in 2 skills at level 1.",
			})
			return
		}

		// Starting gold (simplified: 10gp for all classes)
		startingGold := 10

		// Weapon Mastery (2024): the classes that have it choose their weapons now
		masteryPicks, mErr := validateMasteryPicks(req.Class, 1, req.WeaponMasteries, true)
		if mErr != nil {
			writeOriginError(w, mErr)
			return
		}

		// Origins (2024): background, species, Origin feats and languages
		var heldSkills, heldTools []string
		for _, s := range strings.Split(skillProfsStr, ",") {
			if s = strings.TrimSpace(strings.ToLower(s)); s != "" {
				heldSkills = append(heldSkills, s)
			}
		}
		for _, s := range strings.Split(toolProfsStr, ",") {
			if s = strings.TrimSpace(strings.ToLower(s)); s != "" {
				heldTools = append(heldTools, s)
			}
		}
		origin, oerr := resolveOrigin(originInput{
			Background: req.Background, Species: req.Species, AbilityIncreases: req.AbilityIncreases,
			BackgroundTool: req.BackgroundTool, BackgroundEquipment: req.BackgroundEquipment,
			SpeciesChoices: req.SpeciesChoices, BackgroundFeat: req.BackgroundFeatChoices, SpeciesFeat: req.SpeciesFeatChoices,
			Languages: req.ExtraLanguages, HeldSkills: heldSkills, HeldTools: heldTools,
		})
		if oerr != nil {
			writeOriginError(w, oerr)
			return
		}
		for _, s := range origin.Skills {
			if skillProfsStr == "" {
				skillProfsStr = s
			} else {
				skillProfsStr += ", " + s
			}
		}
		for _, s := range origin.Tools {
			if toolProfsStr == "" {
				toolProfsStr = s
			} else {
				toolProfsStr += ", " + s
			}
		}
		toolProfsStr = game.MergeToolList(toolProfsStr, nil)

		// Rogue Expertise: two of the character's skill proficiencies, from the class, background or species.
		expertiseStr := ""
		if len(req.Expertise) > 0 {
			held := game.ParseProficiencyList(skillProfsStr)
			var chosen []string
			for _, exp := range req.Expertise {
				k := normSkill(exp)
				if !game.IsSkill(k) || !containsStr(held, k) {
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":   "invalid_expertise",
						"message": fmt.Sprintf("'%s' must be one of your skill proficiencies (from your class, background or species); Expertise is for skills.", exp),
					})
					return
				}
				if !containsStr(chosen, k) {
					chosen = append(chosen, k)
				}
			}
			expertiseStr = strings.Join(chosen, ", ")
		}
		languages := origin.Languages
		req.Species = origin.Record.Species.Name
		speciesKey = normalizeSpeciesSlug(req.Species)
		req.Background = game.GetBackground(req.Background).Name
		if a := origin.Record.Species.Choices["ancestry"]; origin.Record.Species.Key == "dragonborn" && a != "" {
			req.DraconicAncestry = a
		}
		hp += origin.Record.Species.HPPerLevel // Dwarven Toughness
		startingGold = origin.Gold
		for _, sl := range origin.KnownSpells {
			req.KnownSpells = append(req.KnownSpells, sl)
		}

		languageProfsStr := strings.Join(languages, ", ")

		// Get darkvision range from species
		darkvisionRange := origin.Record.Species.Darkvision

		// Process known spells
		// Validate that spell slugs exist in SRD
		knownSpellsJSON := []byte("[]")
		var ignoredSpells []string // F233: spell names that aren't SRD spells were dropped without a word
		if len(req.KnownSpells) > 0 {
			validSpells := []string{}
			for _, spellSlug := range req.KnownSpells {
				slugLower := strings.ToLower(strings.TrimSpace(spellSlug))
				// Check if spell exists in SRD
				if _, ok := srdSpellsMemory[slugLower]; ok {
					validSpells = append(validSpells, slugLower)
				} else {
					// Try with dashes instead of spaces
					slugDashed := strings.ReplaceAll(slugLower, " ", "-")
					if _, ok := srdSpellsMemory[slugDashed]; ok {
						validSpells = append(validSpells, slugDashed)
					} else {
						ignoredSpells = append(ignoredSpells, spellSlug)
					}
				}
			}
			knownSpellsJSON, _ = json.Marshal(validSpells)
		}

		// Process draconic ancestry for Dragonborn
		// Determines breath weapon damage type and area shape
		var draconicAncestryStr *string
		if speciesKey == "dragonborn" {
			ancestryLower := strings.ToLower(strings.TrimSpace(req.DraconicAncestry))
			if ancestryLower != "" {
				// Validate it's a valid dragon type
				if _, ok := game.DragonAncestryDamageTypes[ancestryLower]; !ok {
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":         "invalid_draconic_ancestry",
						"message":       fmt.Sprintf("'%s' is not a valid draconic ancestry", req.DraconicAncestry),
						"valid_options": []string{"black", "blue", "brass", "bronze", "copper", "gold", "green", "red", "silver", "white"},
					})
					return
				}
				draconicAncestryStr = &ancestryLower
			}
		}

		// Class starting equipment (2024): the chosen option decides the kit and the gold
		startOption := strings.ToLower(strings.TrimSpace(req.StartingEquipment))
		if startOption == "" {
			startOption = "a"
		}
		classOption, hasClassOption := classStartingEquipment[strings.ToLower(req.Class)][startOption]
		if !hasClassOption {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid_starting_equipment_option",
				"message": fmt.Sprintf("%s offers these starting_equipment options: %s", req.Class, strings.Join(startingOptionKeys(req.Class), ", ")),
			})
			return
		}
		startingGold += classOption.Gold
		var starting *StartingEquipmentResult
		if len(classOption.Items) > 0 {
			sr, serr := buildStartingEquipment(req.Class, startOption, chosenToolForClass(req.Class, toolProfsStr))
			if serr != nil {
				json.NewEncoder(w).Encode(map[string]interface{}{"error": serr.Error()})
				return
			}
			starting = sr
		}

		backstory, appearance := strings.TrimSpace(req.Backstory), strings.TrimSpace(req.Appearance)
		if len([]rune(backstory)) > maxProfileTextRunes || len([]rune(appearance)) > maxProfileTextRunes {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "text_too_long", "message": fmt.Sprintf("The backstory and the appearance can each be %d characters at most.", maxProfileTextRunes)})
			return
		}
		alignment := ""
		if strings.TrimSpace(req.Alignment) != "" {
			if alignment = canonicalPlayerAlignment(req.Alignment); alignment == "" {
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "unknown_alignment", "message": fmt.Sprintf("%q is not an alignment. Choose one of: %s.", req.Alignment, strings.Join(playerAlignments(), ", "))})
				return
			}
		}
		if req.DryRun { // F234: everything checked; nothing created
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "dry_run": true, "would_create": map[string]interface{}{
				"name": req.Name, "class": req.Class, "species": req.Species, "background": req.Background, "alignment": alignment,
				"ability_scores": map[string]int{"str": req.Str, "dex": req.Dex, "con": req.Con, "int": req.Int, "wis": req.Wis, "cha": req.Cha},
				"hp": hp, "skills": skillProfsStr, "tools": toolProfsStr, "languages": languageProfsStr,
				"weapon_masteries": masteryPicks, "expertise": expertiseStr, "starting_equipment": kitText(strings.ToLower(req.Class), startOption)},
				"message": "Everything checks out. Send the same request without dry_run to create the character."})
			return
		}

		var id int
		err := db.QueryRow(`
			INSERT INTO characters (agent_id, name, class, species, background, str, dex, con, intl, wis, cha, hp, max_hp, ac, gold, skill_proficiencies, tool_proficiencies, weapon_proficiencies, armor_proficiencies, expertise, language_proficiencies, darkvision_range, known_spells, draconic_ancestry)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23) RETURNING id
		`, agentID, req.Name, req.Class, req.Species, req.Background, req.Str, req.Dex, req.Con, req.Int, req.Wis, req.Cha, hp, ac, startingGold, skillProfsStr, toolProfsStr, weaponProfsStr, armorProfsStr, expertiseStr, languageProfsStr, darkvisionRange, knownSpellsJSON, draconicAncestryStr).Scan(&id)

		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
			return
		}

		masteryJSON, _ := json.Marshal(masteryPicks)
		db.Exec(`UPDATE characters SET weapon_masteries = $1 WHERE id = $2`, masteryJSON, id)
		if alignment != "" {
			db.Exec(`UPDATE characters SET alignment = $1 WHERE id = $2`, alignment, id)
		}
		if backstory != "" {
			db.Exec(`UPDATE characters SET backstory = $1 WHERE id = $2`, backstory, id)
		}
		if appearance != "" {
			db.Exec(`UPDATE characters SET appearance = $1 WHERE id = $2`, appearance, id)
		}
		originJSON, _ := json.Marshal(origin.Record)
		featsJSON, _ := json.Marshal(origin.FeatSlugs)
		db.Exec(`UPDATE characters SET origin = $1, feats = $2 WHERE id = $3`, originJSON, featsJSON, id)

		// Seed the equipment required for core class features. In particular,
		// casters need a focus to use spells with ordinary material components.
		invItems := []map[string]interface{}{}
		if starting != nil {
			invItems = append(invItems, starting.Items...)
		}
		invItems = append(invItems, startingCharacterInventoryFor(classKey, nil, starting != nil)...)
		if len(origin.Kit) > 0 {
			if bk, berr := buildKit(origin.Kit, 0, origin.KitTool, "b", "background", false); berr == nil {
				invItems = append(invItems, bk.Items...)
			}
		}
		ensureItemIDs(invItems)
		fillItemWeights(invItems)
		if len(invItems) > 0 {
			invJSON, _ := json.Marshal(invItems)
			db.Exec("UPDATE characters SET inventory = $1 WHERE id = $2", invJSON, id)
		}
		armorSlug, mainHand, hasShield := "", "", false
		if starting != nil {
			armorSlug, mainHand, hasShield = starting.EquippedArmor, starting.MainHand, starting.Shield
		}
		if hasShield && mainHand != "" && game.ClassTableInt(classKey, 1, "Cantrips") > 0 {
			mainHand = "" // a caster with a Shield keeps the other hand free for spells with gestures (Nov 13); the setup asks whether to draw the weapon
		}
		db.Exec(`UPDATE characters SET starting_equipment = $1, equipped_armor = NULLIF($2, ''), equipped_shield = $3,
			equipped_main_hand = NULLIF($4, ''), divine_order = NULLIF($5, ''), primal_order = NULLIF($6, '') WHERE id = $7`,
			startOption, armorSlug, hasShield, mainHand, divineOrder, primalOrder, id)
		refreshStoredAC(id) // the starting armor and Shield, Unarmored Defense (the stored AC was 10 + Dexterity until the sheet was opened)
		db.QueryRow(`SELECT ac FROM characters WHERE id = $1`, id).Scan(&ac)

		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "character_id": id, "hp": hp, "ac": ac,
			"species": origin.Record.Species.Name, "size": origin.Record.Species.Size, "speed": origin.Record.Species.Speed,
			"background": origin.Record.Background, "alignment": alignment, "backstory_saved": backstory != "", "portrait_tip": fmt.Sprintf("Give your character a portrait: GET /api/characters/%d/setup has the size an image should be and a prompt you can give an image generator.", id), "feats": origin.FeatSlugs, "languages": origin.Languages,
			"ability_increases": origin.Increases, "background_equipment": origin.Record.BackgroundEquipment,
			"ability_scores": map[string]int{"str": req.Str, "dex": req.Dex, "con": req.Con, "int": req.Int, "wis": req.Wis, "cha": req.Cha}, "ability_method": abilityNote,
			"to_finish": characterToFinish(id), "character_finished": len(characterToFinish(id)) == 0, // F232: what the player still has to choose
			"to_finish_note": "Your character isn't finished until to_finish is empty: GET /api/characters/{id} and /api/my-turn keep showing what is left.",
			"ignored_spells": ignoredSpells,
			"setup": fmt.Sprintf("GET /api/characters/%d/setup guides what is left: spells, the gear in your hands, class choices", id)})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// startingCharacterInventory returns the baseline non-weapon equipment this
// simplified character creator guarantees. Background equipment remains flavor
// equipment; caster focuses are functional equipment and let new spellcasters
// actually use spells with ordinary material components.
func startingCharacterInventory(classKey string, backgroundEquipment []string) []map[string]interface{} {
	return startingCharacterInventoryFor(classKey, backgroundEquipment, false)
}

// startingCharacterInventoryFor is the same, but skips the basic focus when a class kit is given.
func startingCharacterInventoryFor(classKey string, backgroundEquipment []string, classKitGiven bool) []map[string]interface{} {
	items := []map[string]interface{}{}
	// A class kit already includes the spellcasting focus; gold-only starts keep a basic one.
	if focus := startingSpellcastingFocus(classKey); focus != "" && !classKitGiven {
		items = append(items, map[string]interface{}{
			"name":   focus,
			"type":   "focus",
			"source": "class",
		})
	}
	for _, item := range backgroundEquipment {
		items = append(items, map[string]interface{}{
			"name":   item,
			"weight": 0, // Background items are flavor, no weight tracking
			"source": "background",
		})
	}
	return items
}

func startingSpellcastingFocus(classKey string) string {
	switch strings.ToLower(strings.TrimSpace(classKey)) {
	case "bard":
		return "Musical Instrument"
	case "cleric", "paladin":
		return "Holy Symbol"
	case "druid", "ranger":
		return "Druidic Focus"
	case "sorcerer", "warlock", "wizard":
		return "Arcane Focus"
	default:
		return ""
	}
}

// handleCharacterByID godoc
// @Summary Get character sheet
// @Description Returns full character details including stats, modifiers, conditions, and spell slots
// @Tags Characters
// @Produce json
// @Param id path int true "Character ID"
// @Success 200 {object} map[string]interface{} "Character sheet"
// @Failure 404 {object} map[string]interface{} "Character not found"
// @Router /characters/{id} [get]
func handleCharacterByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := strings.TrimPrefix(r.URL.Path, "/api/characters/")
	parts := strings.Split(idStr, "/")
	charID, err := strconv.Atoi(parts[0])
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_character_id"})
		return
	}

	// A character in a private campaign is read only by the campaign's players and GM, or with its watch password (BUGS 12).
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if lobby, ok := canSeeCharacter(r, charID); !ok {
			writeWatchGate(w, r, lobby, false)
			return
		}
	}

	// Handle sub-routes
	if len(parts) > 1 {
		// Who may change the character through these: damage and heal are the GM's (of the character's campaign); rests and cover
		// are the owner's or that GM's. (The server's own code calls the handlers directly, without this check.)
		switch parts[1] {
		case "damage", "heal":
			if !requireCharacterGM(w, r, charID) {
				return
			}
		case "rest", "short-rest", "cover", "setup":
			if !requireOwnerOrGM(w, r, charID) {
				return
			}
		}
		switch parts[1] {
		case "damage":
			handleDamage(w, r, charID)
			return
		case "heal":
			handleHeal(w, r, charID)
			return
		case "conditions":
			handleCharacterConditions(w, r, charID)
			return
		case "rest":
			withRestReminders(handleRest, false)(w, r, charID) // reminders after a Long Rest; clears the after-battle note (F216)
			return
		case "short-rest":
			withRestReminders(handleShortRest, true)(w, r, charID)
			return
		case "cover":
			handleSetCover(w, r, charID)
			return
		case "observations":
			handleCharacterObservations(w, r, charID)
			return
		case "asi":
			handleCharacterASI(w, r, charID)
			return
		case "spells":
			handleCharacterSpells(w, r, charID)
			return
		case "prepare":
			handlePrepareSpells(w, r, charID)
			return
		case "feat":
			handleCharacterFeat(w, r, charID)
			return
		case "expertise":
			handleCharacterExpertise(w, r, charID)
			return
		case "level-up":
			handleCharacterLevelUp(w, r, charID)
			return
		case "use-resource":
			handleUseResource(w, r, charID)
			return
		case "equipment":
			handleCharacterEquipment(w, r, charID, parts[2:])
			return
		case "starting-equipment":
			handleCharacterStartingEquipment(w, r, charID)
			return
		case "tool-proficiencies":
			handleCharacterToolProficiencies(w, r, charID)
			return
		case "class-options":
			handleCharacterClassOptions(w, r, charID)
			return
		case "portrait":
			handleCharacterPortrait(w, r, charID)
			return
		case "profile":
			handleCharacterProfile(w, r, charID)
			return
		case "setup":
			handleCharacterSetup(w, r, charID) // F234: spells, gear in hand, class choices left
			return
		case "shop":
			handleCharacterShop(w, r, charID) // the starting shop (shop_2024.go)
			return
		case "level-up-notices":
			handleLevelUpNotices(w, r, charID) // the server's level-up notices (level_up_notice_2024.go)
			return
		}
	}
	if len(parts) <= 1 && withToFinish(w, r, charID, handleCharacterByID) { // to_finish, character_finished (F232)
		return
	}

	var name, class, species, background string
	var subclassRaw sql.NullString
	var level, hp, maxHP, ac, str, dex, con, intl, wis, cha int
	var tempHP, deathSuccesses, deathFailures, coverBonus, xp, pendingASI int
	var hitDiceSpent, exhaustionLevel int
	var isStable, isDead, hasInspiration bool
	var conditionsJSON, slotsUsedJSON, pactSlotsUsedJSON, classLevelsJSON, knownSpellsJSON, preparedSpellsJSON, featsJSON []byte
	var concentratingOn string
	var gold, copper, silver, electrum, platinum int
	var inventoryJSON []byte
	var skillProfsRaw string
	var toolProfsRaw string
	var weaponProfsRaw string
	var armorProfsRaw string
	var expertiseRaw string
	var languageProfsRaw string
	var equippedArmor sql.NullString
	var equippedShield bool
	var equippedMainHand, equippedOffHand sql.NullString
	var darkvisionRange, blindsightRange, truesightRange int

	var subclassChoicesJSON []byte // For Land druid circle spells
	err = db.QueryRow(`
		SELECT name, class, species, COALESCE(background, ''), COALESCE(subclass, ''), level, hp, max_hp, ac, 
			str, dex, con, intl, wis, cha,
			COALESCE(temp_hp, 0), COALESCE(death_save_successes, 0), COALESCE(death_save_failures, 0),
			COALESCE(is_stable, false), COALESCE(is_dead, false),
			COALESCE(conditions, '[]'), COALESCE(spell_slots_used, '{}'),
			COALESCE(pact_slots_used, '{}'), COALESCE(class_levels, '{}'),
			COALESCE(concentrating_on, ''), COALESCE(cover_bonus, 0), COALESCE(xp, 0),
			COALESCE(gold, 0), COALESCE(copper, 0), COALESCE(silver, 0), 
			COALESCE(electrum, 0), COALESCE(platinum, 0),
			COALESCE(inventory, '[]'), COALESCE(pending_asi, 0),
			COALESCE(hit_dice_spent, 0), COALESCE(exhaustion_level, 0),
			COALESCE(skill_proficiencies, ''), COALESCE(inspiration, false),
			COALESCE(tool_proficiencies, ''),
			COALESCE(weapon_proficiencies, ''), COALESCE(armor_proficiencies, ''),
			COALESCE(expertise, ''), COALESCE(language_proficiencies, ''),
			equipped_armor, COALESCE(equipped_shield, false),
			equipped_main_hand, equipped_off_hand,
			COALESCE(darkvision_range, 0), COALESCE(blindsight_range, 0), COALESCE(truesight_range, 0),
			COALESCE(known_spells, '[]'), COALESCE(prepared_spells, '[]'), COALESCE(feats, '[]'),
			COALESCE(subclass_choices, '{}')
		FROM characters WHERE id = $1
	`, charID).Scan(&name, &class, &species, &background, &subclassRaw, &level, &hp, &maxHP, &ac,
		&str, &dex, &con, &intl, &wis, &cha,
		&tempHP, &deathSuccesses, &deathFailures, &isStable, &isDead,
		&conditionsJSON, &slotsUsedJSON, &pactSlotsUsedJSON, &classLevelsJSON, &concentratingOn, &coverBonus, &xp,
		&gold, &copper, &silver, &electrum, &platinum,
		&inventoryJSON, &pendingASI, &hitDiceSpent, &exhaustionLevel, &skillProfsRaw, &hasInspiration, &toolProfsRaw,
		&weaponProfsRaw, &armorProfsRaw, &expertiseRaw, &languageProfsRaw, &equippedArmor, &equippedShield,
		&equippedMainHand, &equippedOffHand,
		&darkvisionRange, &blindsightRange, &truesightRange, &knownSpellsJSON, &preparedSpellsJSON, &featsJSON,
		&subclassChoicesJSON)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "character_not_found"})
		return
	}

	var conditions []string
	json.Unmarshal(conditionsJSON, &conditions)

	var slotsUsed map[string]int
	json.Unmarshal(slotsUsedJSON, &slotsUsed)

	var inventory []interface{}
	json.Unmarshal(inventoryJSON, &inventory)
	inventory = usableInventoryAny(inventory) // lost bags are not on the sheet

	// Parse pact slots used and class levels for multiclass Warlock support
	var pactSlotsUsed map[string]int
	json.Unmarshal(pactSlotsUsedJSON, &pactSlotsUsed)

	var classLevels map[string]int
	json.Unmarshal(classLevelsJSON, &classLevels)

	// Check for Warlock levels (for pact magic tracking)
	warlockLevel := 0
	isMulticlass := len(classLevels) > 1
	for c, lvl := range classLevels {
		if strings.ToLower(c) == "warlock" {
			warlockLevel = lvl
			break
		}
	}
	// For single-class Warlock, get level from main class
	if !isMulticlass && strings.ToLower(class) == "warlock" {
		warlockLevel = level
	}

	// Get total spell slots for class/level
	totalSlots := characterSpellSlots(charID, class, level)

	// Calculate remaining slots
	remainingSlots := map[string]int{}
	for lvl, total := range totalSlots {
		key := fmt.Sprintf("%d", lvl)
		used := slotsUsed[key]
		remainingSlots[key] = total - used
	}

	// Calculate spell save DC
	classKey := strings.ToLower(class)
	spellMod := 0
	spellAbility := ""
	if c, ok := srdClasses[classKey]; ok && c.Spellcasting != "" {
		spellAbility = c.Spellcasting
		switch c.Spellcasting {
		case "INT":
			spellMod = game.Modifier(intl)
		case "WIS":
			spellMod = game.Modifier(wis)
		case "CHA":
			spellMod = game.Modifier(cha)
		}
	}

	effectiveAC := ac + coverBonus
	xpToNextLevel := getXPForNextLevel(level)

	// Build subclass info
	var subclassInfo interface{}
	subclassSlug := ""
	if subclassRaw.Valid && subclassRaw.String != "" {
		subclassSlug = subclassRaw.String
		if sub, ok := game.AvailableSubclasses[subclassSlug]; ok {
			activeFeatures := characterSubclassFeatures(charID, class, subclassSlug, level)
			featuresInfo := []map[string]interface{}{}
			for _, f := range activeFeatures {
				featuresInfo = append(featuresInfo, map[string]interface{}{
					"name":        f.Name,
					"level":       f.Level,
					"description": f.Description,
				})
			}
			subclassInfo = map[string]interface{}{
				"slug":            subclassSlug,
				"name":            sub.Name,
				"active_features": featuresInfo,
			}
		} else {
			subclassInfo = subclassSlug
		}
	}

	// Build class features info
	classFeaturesList := []map[string]interface{}{}
	activeClassFeats := characterClassFeatures(charID, class, level)
	for _, f := range activeClassFeats {
		classFeaturesList = append(classFeaturesList, map[string]interface{}{
			"name":        f.Name,
			"level":       f.Level,
			"description": f.Description,
		})
	}

	// Calculate effective STR/CON with Primal Champion bonus
	effectiveStr := getEffectiveAbilityScore(str, "str", class, level, classLevels)
	effectiveCon := getEffectiveAbilityScore(con, "con", class, level, classLevels)

	var sheetAlignment string
	db.QueryRow(`SELECT COALESCE(alignment, '') FROM characters WHERE id = $1`, charID).Scan(&sheetAlignment)
	response := map[string]interface{}{
		"id": charID, "name": name, "class": class, "species": species, "alignment": sheetAlignment,
		"subclass":       subclassInfo,
		"class_features": classFeaturesList,
		"species_traits": speciesTraitsOf(species), // F116: the sheet listed no species traits
		"feature_words":  featureWordsFor(charID),  // the words that switch on features in POST /api/action (feature_words_2024.go)
		"background":     background, "level": level,
		"hp": hp, "max_hp": maxHP, "temp_hp": tempHP,
		"ac": ac, "effective_ac": effectiveAC,
		"stats": map[string]int{
			"str": effectiveStr, "dex": dex, "con": effectiveCon,
			"int": intl, "wis": wis, "cha": cha,
		},
		"modifiers": map[string]int{
			"str": game.Modifier(effectiveStr), "dex": game.Modifier(dex), "con": game.Modifier(effectiveCon),
			"int": game.Modifier(intl), "wis": game.Modifier(wis), "cha": game.Modifier(cha),
		},
		"conditions":        conditions,
		"proficiency_bonus": game.ProficiencyBonus(level),
		"skill_proficiencies": func() []string {
			if skillProfsRaw == "" {
				return []string{}
			}
			skills := []string{}
			for _, s := range strings.Split(skillProfsRaw, ",") {
				skills = append(skills, strings.TrimSpace(s))
			}
			return skills
		}(),
		"tool_proficiencies": func() []string {
			if toolProfsRaw == "" {
				return []string{}
			}
			tools := []string{}
			for _, t := range strings.Split(toolProfsRaw, ",") {
				tools = append(tools, strings.TrimSpace(t))
			}
			return tools
		}(),
		"weapon_proficiencies": func() []string {
			if weaponProfsRaw == "" {
				return []string{}
			}
			weapons := []string{}
			for _, w := range strings.Split(weaponProfsRaw, ",") {
				weapons = append(weapons, strings.TrimSpace(w))
			}
			return weapons
		}(),
		"armor_proficiencies": func() []string {
			if armorProfsRaw == "" {
				return []string{}
			}
			armor := []string{}
			for _, a := range strings.Split(armorProfsRaw, ",") {
				armor = append(armor, strings.TrimSpace(a))
			}
			return armor
		}(),
		"expertise": func() []string {
			if expertiseRaw == "" {
				return []string{}
			}
			exp := []string{}
			for _, e := range strings.Split(expertiseRaw, ",") {
				exp = append(exp, strings.TrimSpace(e))
			}
			return exp
		}(),
		"language_proficiencies": func() []string {
			if languageProfsRaw == "" {
				return []string{}
			}
			langs := []string{}
			for _, l := range strings.Split(languageProfsRaw, ",") {
				langs = append(langs, strings.TrimSpace(l))
			}
			for _, cl := range classLanguages(charID, class) { // Druidic, Thieves' Cant (F160, items_c5_2024.go)
				if !strings.Contains(strings.ToLower(languageProfsRaw), strings.ToLower(cl)) {
					langs = append(langs, cl)
				}
			}
			return langs
		}(),
		"xp":               xp,
		"xp_to_next_level": xpToNextLevel - xp,
		"xp_threshold":     xpToNextLevel,
		"currency": map[string]interface{}{
			"cp":          copper,
			"sp":          silver,
			"ep":          electrum,
			"gp":          gold,
			"pp":          platinum,
			"total_in_gp": float64(copper)/100 + float64(silver)/10 + float64(electrum)/2 + float64(gold) + float64(platinum)*10,
		},
		"gold":        gold, // Keep for backwards compatibility
		"inventory":   inventory,
		"pending_asi": pendingASI,
		"hit_dice": map[string]interface{}{
			"die_type":  fmt.Sprintf("d%d", game.HitDie(class)),
			"total":     level,
			"available": level - hitDiceSpent,
			"spent":     hitDiceSpent,
		},
		"exhaustion_level": exhaustionLevel,
		"inspiration":      hasInspiration,
	}

	// Add background feature from game package
	if background != "" {
		bgKey := strings.ToLower(strings.ReplaceAll(background, " ", "_"))
		bgKey = strings.ReplaceAll(bgKey, "-", "_")
		if bg := game.GetBackground(bgKey); bg != nil {
			response["background_feat"] = bg.Feat
		}
	}

	// Add inspiration tip if they have it
	if hasInspiration {
		response["inspiration_tip"] = "You have inspiration! Add use_inspiration:true to any skill check, saving throw, or attack to spend it: a failed d20 is rolled again and the new roll is used."
	}

	// Add ASI prompt if they have points to spend
	if pendingASI > 0 {
		response["asi_available"] = true
		response["asi_message"] = fmt.Sprintf("You have %d ability score improvement points to spend! POST /api/characters/%d/asi with {\"ability\": \"str|dex|con|int|wis|cha\", \"points\": 1-2} OR take a feat (costs 2 points): POST /api/characters/%d/feat with {\"feat\": \"slug\"}", pendingASI, charID, charID)
		response["feat_option"] = fmt.Sprintf("Instead of ability increases, you can take a feat for 2 ASI points. GET /api/characters/%d/feat for available feats.", charID)
	}

	// Add exhaustion effects if exhausted
	if exhaustionLevel > 0 {
		exhaustionEffects := []string{
			fmt.Sprintf("D20 Tests are reduced by %d", game.ExhaustionD20Penalty(exhaustionLevel)),
			fmt.Sprintf("Speed is reduced by %d feet", game.ExhaustionSpeedPenalty(exhaustionLevel)),
		}
		if exhaustionLevel >= 6 {
			exhaustionEffects = append(exhaustionEffects, "DEATH")
		}
		response["exhaustion_effects"] = exhaustionEffects
		response["exhaustion_warning"] = fmt.Sprintf("You have %d level(s) of exhaustion. Take a long rest to reduce by 1.", exhaustionLevel)
	}

	// Petrified weight - show weight info when petrified (weight x10)
	isPetrified := false
	for _, c := range conditions {
		if strings.ToLower(c) == "petrified" {
			isPetrified = true
			break
		}
	}
	if isPetrified {
		weightInfo := getCharacterTotalWeight(charID)
		if weightInfo != nil {
			response["weight"] = weightInfo
		}
	}

	if coverBonus > 0 {
		coverType := "half"
		if coverBonus >= 5 {
			coverType = "three_quarters"
		}
		response["cover"] = coverType
		response["cover_bonus"] = coverBonus
	}

	// Equipment (armor/shield/weapons)
	equipment := map[string]interface{}{
		"armor":     nil,
		"shield":    equippedShield,
		"main_hand": nil,
		"off_hand":  nil,
	}
	if equippedArmor.Valid && equippedArmor.String != "" {
		armorInfo, err := getArmorInfo(equippedArmor.String)
		if err == nil && armorInfo != nil {
			equipment["armor"] = map[string]interface{}{
				"slug":                 equippedArmor.String,
				"type":                 armorInfo.Type,
				"base_ac":              armorInfo.AC,
				"stealth_disadvantage": armorInfo.StealthDisadvantage,
				"strength_requirement": armorInfo.StrengthRequirement,
			}
			if armorInfo.StrengthRequirement > 0 && str < armorInfo.StrengthRequirement {
				equipment["armor_warning"] = fmt.Sprintf("STR requirement not met (need %d, have %d) - speed reduced by 10", armorInfo.StrengthRequirement, str)
			}
		} else {
			equipment["armor"] = equippedArmor.String
		}
	}
	// Add equipped weapons
	if equippedMainHand.Valid && equippedMainHand.String != "" {
		weaponSlug := strings.ToLower(strings.ReplaceAll(equippedMainHand.String, " ", "-"))
		var weaponName string
		var properties sql.NullString
		var damageDice, damageType sql.NullString
		err := db.QueryRow(`SELECT name, properties, damage_dice, damage_type FROM weapons WHERE slug = $1`, weaponSlug).Scan(&weaponName, &properties, &damageDice, &damageType)
		if err == nil {
			equipment["main_hand"] = map[string]interface{}{
				"name":        weaponName,
				"slug":        weaponSlug,
				"properties":  properties.String,
				"damage_dice": damageDice.String,
				"damage_type": damageType.String,
			}
		} else {
			equipment["main_hand"] = equippedMainHand.String
		}
	}
	if equippedOffHand.Valid && equippedOffHand.String != "" {
		weaponSlug := strings.ToLower(strings.ReplaceAll(equippedOffHand.String, " ", "-"))
		var weaponName string
		var properties sql.NullString
		var damageDice, damageType sql.NullString
		err := db.QueryRow(`SELECT name, properties, damage_dice, damage_type FROM weapons WHERE slug = $1`, weaponSlug).Scan(&weaponName, &properties, &damageDice, &damageType)
		if err == nil {
			equipment["off_hand"] = map[string]interface{}{
				"name":        weaponName,
				"slug":        weaponSlug,
				"properties":  properties.String,
				"damage_dice": damageDice.String,
				"damage_type": damageType.String,
			}
		} else {
			equipment["off_hand"] = equippedOffHand.String
		}
	}
	response["equipment"] = equipment
	response["worn_items"] = wornItemsView(charID) // by place: armor, shield, feet, hands, wrists, head, shoulders, fingers, neck ... (F213)
	response["in_melee_range"] = isInMeleeRange(charID)
	if owed := game.PendingToolChoices(game.ToolChoicesFor(class, classLevels), toolProfsRaw); len(owed) > 0 {
		response["pending_choices"] = map[string]interface{}{
			"tool_proficiencies": owed,
			"how":                "POST /api/characters/" + strconv.Itoa(charID) + "/tool-proficiencies with {\"tools\": [...]}",
		}
	}

	// Vision capabilities
	vision := map[string]interface{}{}
	if d := darkvisionBuff(charID); d > darkvisionRange {
		darkvisionRange = d // the Darkvision spell (spells_more_2024.go)
	}
	darkvisionRange, truesightRange = itemVision(charID, darkvisionRange, truesightRange) // Goggles of Night, Robe of Eyes, Belt of Dwarvenkind (F204)
	if darkvisionRange > 0 {
		vision["darkvision"] = darkvisionRange
	}
	if blindsightRange > 0 {
		vision["blindsight"] = blindsightRange
	}
	if sp := itemSpeeds(charID); len(sp) > 0 {
		response["item_speeds"] = sp // Ring of Swimming, Cloak of the Manta Ray, Gloves of Swimming and Climbing, Cloak of Arachnida
	}
	if t := buffTruesight(charID); t > truesightRange {
		truesightRange = t // True Seeing (buff_spells_2024.go)
	}
	if hasInvocation(charID, "witch-sight") && truesightRange < 30 {
		truesightRange = 30 // Witch Sight (F199)
	}
	if truesightRange > 0 {
		vision["truesight"] = truesightRange
	}
	if hasInvocation(charID, "devils-sight") {
		vision["devils_sight"] = 120 // see normally in Dim Light and Darkness, magical or not (F199)
	}
	if len(vision) > 0 {
		response["vision"] = vision
	}

	// Recalculate AC based on equipped armor (in case stored AC is out of sync)
	// Use natural AC base from subclass features (e.g., Draconic Resilience gives 13 + DEX)
	calculatedAC := computeCharacterAC(charID) // armor, Shield, the best unarmored base, magic items, the Defense Fighting Style
	if equippedArmor.String != "" && hasFightingStyle(charID, "defense") {
		response["defense_style_bonus"] = 1
	}
	var wildShapeForm sql.NullString
	if db.QueryRow(`SELECT wild_shape_form FROM characters WHERE id = $1`, charID).Scan(&wildShapeForm) == nil && wildShapeForm.String != "" {
		calculatedAC = ac // in Wild Shape the beast's AC stands
	}

	if calculatedAC != ac {
		response["ac"] = calculatedAC
		response["effective_ac"] = calculatedAC + coverBonus
		// Update stored AC to keep it in sync
		db.Exec(`UPDATE characters SET ac = $1 WHERE id = $2`, calculatedAC, charID)
	}

	// Death save info (only if relevant)
	if hp == 0 && !isDead {
		response["death_saves"] = map[string]interface{}{
			"successes": deathSuccesses,
			"failures":  deathFailures,
			"stable":    isStable,
		}
	}
	if isDead {
		response["is_dead"] = true
	}

	// Spell info (only for casters)
	if len(totalSlots) > 0 {
		response["spell_slots"] = map[string]interface{}{
			"total":     totalSlots,
			"remaining": remainingSlots,
		}
		response["spell_save_dc"] = game.SpellSaveDC(level, spellMod) + magicSpellDCBonus(charID)                // Robe of the Archmagi
		response["spell_attack_bonus"] = spellMod + game.ProficiencyBonus(level) + magicSpellAttackBonus(charID) // Wand of the War Mage, staffs
		response["spellcasting_ability"] = spellAbility
	}

	// Pact Magic slots for multiclass Warlocks
	// Pact slots are tracked separately and recover on short rest
	if isMulticlass && warlockLevel > 0 {
		pactSlots := game.SpellSlots("warlock", warlockLevel)
		pactRemainingSlots := map[string]int{}
		for lvl, total := range pactSlots {
			key := fmt.Sprintf("%d", lvl)
			used := pactSlotsUsed[key]
			pactRemainingSlots[key] = total - used
		}
		response["pact_magic"] = map[string]interface{}{
			"warlock_level": warlockLevel,
			"total":         pactSlots,
			"remaining":     pactRemainingSlots,
			"note":          "Pact Magic slots are separate from regular spell slots. They recover on short rest, not long rest.",
		}
	}

	// Eldritch Invocations for Warlocks
	effectiveWarlockLevel := warlockLevel
	if !isMulticlass && strings.ToLower(class) == "warlock" {
		effectiveWarlockLevel = level
	}
	if effectiveWarlockLevel >= 2 {
		invocations := getCharacterInvocations(charID)
		maxInvocations := game.GetMaxInvocations(effectiveWarlockLevel)
		invocationSpellsUsed := getInvocationSpellsUsed(charID)

		if len(invocations) > 0 {
			invocationsInfo := []map[string]interface{}{}
			for _, slug := range invocations {
				if inv, ok := game.AvailableInvocations[slug]; ok {
					invInfo := map[string]interface{}{
						"slug":        slug,
						"name":        inv.Name,
						"description": inv.Description,
						"mechanics":   inv.Mechanics,
					}
					// Show used status for once-per-rest invocations
					if _, hasOncePerRest := inv.Mechanics["once_per_rest_spell"]; hasOncePerRest {
						invInfo["once_per_rest"] = true
						invInfo["used"] = hasUsedInvocationSpell(charID, slug)
					}
					// Show Eldritch Spear range extension
					if _, hasEldritchSpear := inv.Mechanics["eldritch_spear"]; hasEldritchSpear {
						invInfo["modifies"] = "eldritch-blast"
						invInfo["effect"] = fmt.Sprintf("Range increased by 30 feet per Warlock level: %d feet", 120+eldritchSpearBonus(charID))
					}
					// Show Witch Sight info
					if _, hasWitchSightMech := inv.Mechanics["witch_sight"]; hasWitchSightMech {
						invInfo["passive"] = true
						invInfo["range"] = "30 feet"
						invInfo["effect"] = "You have Truesight with a range of 30 feet (shown under vision)"
					}
					invocationsInfo = append(invocationsInfo, invInfo)
				}
			}
			invocationsResponse := map[string]interface{}{
				"known":           invocationsInfo,
				"known_count":     len(invocations),
				"max_invocations": maxInvocations,
				"can_learn_more":  len(invocations) < maxInvocations,
			}
			// Include once-per-rest usage summary
			if len(invocationSpellsUsed) > 0 {
				invocationsResponse["spells_used_today"] = invocationSpellsUsed
				invocationsResponse["note"] = "Once-per-rest invocation spells reset on long rest."
			}
			// Include at-will spells from invocations
			atWillSpells := getCharacterAtWillSpells(charID)
			if len(atWillSpells) > 0 {
				invocationsResponse["at_will_spells"] = atWillSpells
			}
			response["eldritch_invocations"] = invocationsResponse
		} else if maxInvocations > 0 {
			response["invocations_pending"] = fmt.Sprintf("You can learn %d Eldritch Invocations! GET /api/characters/invocations?character_id=%d to see options.", maxInvocations, charID)
		}
	}

	// Mystic Arcanum for Warlocks level 11+
	if effectiveWarlockLevel >= 11 {
		var arcanumJSON, usedJSON []byte
		db.QueryRow("SELECT COALESCE(mystic_arcanum, '{}'), COALESCE(mystic_arcanum_used, '[]') FROM characters WHERE id = $1", charID).Scan(&arcanumJSON, &usedJSON)
		var arcanum map[string]string
		var usedLevels []int
		json.Unmarshal(arcanumJSON, &arcanum)
		json.Unmarshal(usedJSON, &usedLevels)

		availableLevels := getAvailableMysticArcanumLevels(effectiveWarlockLevel)
		arcanumInfo := []map[string]interface{}{}
		unchosen := []int{}

		for _, spellLvl := range availableLevels {
			spellSlug := arcanum[strconv.Itoa(spellLvl)]
			info := map[string]interface{}{"spell_level": spellLvl}

			if spellSlug != "" {
				var spellName string
				db.QueryRow("SELECT name FROM spells WHERE slug = $1", spellSlug).Scan(&spellName)
				info["spell"] = spellSlug
				info["spell_name"] = spellName
				// Check if used
				used := false
				for _, usedLvl := range usedLevels {
					if usedLvl == spellLvl {
						used = true
						break
					}
				}
				info["used"] = used
				if used {
					info["status"] = "Used (resets on long rest)"
				} else {
					info["status"] = "Available"
				}
			} else {
				info["spell"] = nil
				info["status"] = "Not chosen"
				unchosen = append(unchosen, spellLvl)
			}
			arcanumInfo = append(arcanumInfo, info)
		}

		mysticArcanumResponse := map[string]interface{}{
			"arcanum":     arcanumInfo,
			"note":        "Mystic Arcanum spells can be cast once per long rest without a spell slot. Include 'arcanum' in your cast description.",
			"how_to_cast": "POST /api/action with action='cast', description='cast [spell] using arcanum'",
		}
		if len(unchosen) > 0 {
			mysticArcanumResponse["unchosen_levels"] = unchosen
			mysticArcanumResponse["how_to_choose"] = "POST /api/characters/mystic-arcanum with character_id, spell_level, spell_slug"
		}
		response["mystic_arcanum"] = mysticArcanumResponse
	}

	// Known spells
	var knownSpells []string
	json.Unmarshal(knownSpellsJSON, &knownSpells)
	if len(knownSpells) > 0 {
		// Enrich with spell names and levels for convenience
		knownSpellsInfo := []map[string]interface{}{}
		for _, slug := range knownSpells {
			if spell, ok := srdSpellsMemory[slug]; ok {
				knownSpellsInfo = append(knownSpellsInfo, map[string]interface{}{
					"slug":   slug,
					"name":   spell.Name,
					"level":  spell.Level,
					"school": spell.School,
				})
			} else {
				knownSpellsInfo = append(knownSpellsInfo, map[string]interface{}{
					"slug": slug,
					"name": slug, // fallback
				})
			}
		}
		response["known_spells"] = knownSpellsInfo
		response["known_spells_tip"] = "Use PUT /api/characters/{id}/spells to update your known spells list."
	}

	// Domain/Subclass Spells - always prepared, don't count against limits
	// For Land druids, include Circle Spells based on chosen land type
	if subclassRaw.Valid && subclassRaw.String != "" {
		var subclassChoices map[string]string
		json.Unmarshal(subclassChoicesJSON, &subclassChoices)
		landType := subclassChoices["circle_land"]

		domainSpells := getDomainSpellsWithInfo(subclassRaw.String, level, landType)
		if len(domainSpells) > 0 {
			response["domain_spells"] = domainSpells
			response["domain_spells_note"] = "These spells are always prepared and don't count against your prepared spell limit."
			// Show chosen land type for Land druids
			if subclassRaw.String == "land" && landType != "" {
				response["circle_land"] = landType
			}
		} else if subclassRaw.String == "land" && landType == "" {
			// Prompt Land druid to choose their land type
			response["circle_spells_pending"] = "Use POST /api/characters/subclass-choice to choose your Circle Land type for bonus spells."
		}
	}

	// Prepared Spells - for prepared casters (Cleric, Druid, Paladin, Wizard)
	if game.IsPreparedCaster(class) {
		var preparedSpells []string
		json.Unmarshal(preparedSpellsJSON, &preparedSpells)

		preparedSpellsInfo := []map[string]interface{}{}
		for _, slug := range preparedSpells {
			if spell, ok := srdSpellsMemory[slug]; ok {
				preparedSpellsInfo = append(preparedSpellsInfo, map[string]interface{}{
					"slug":   slug,
					"name":   spell.Name,
					"level":  spell.Level,
					"school": spell.School,
				})
			} else {
				preparedSpellsInfo = append(preparedSpellsInfo, map[string]interface{}{
					"slug": slug,
					"name": slug, // fallback
				})
			}
		}

		maxPrepared := game.MaxPreparedSpells(class, level, intl, wis, cha)
		response["prepared_spells"] = preparedSpellsInfo
		response["prepared_count"] = len(preparedSpells)
		response["max_prepared"] = maxPrepared
		response["slots_remaining"] = maxPrepared - len(preparedSpells)
		response["caster_type"] = "prepared"
		response["prepared_spells_tip"] = fmt.Sprintf("Use POST /api/characters/%d/prepare to change prepared spells (Cleric, Druid, Wizard: after a Long Rest; Paladin, Ranger: one after a Long Rest; Bard, Sorcerer, Warlock: one when you gain a level).", charID)
	}

	// Feats
	var characterFeats []string
	json.Unmarshal(featsJSON, &characterFeats)
	if len(characterFeats) > 0 {
		featsInfo := []map[string]interface{}{}
		for _, slug := range characterFeats {
			if feat, ok := lookupFeat(slug); ok {
				featsInfo = append(featsInfo, map[string]interface{}{
					"slug":     slug,
					"name":     feat.Name,
					"benefits": feat.Benefits,
					"features": feat.Features,
				})
			} else {
				featsInfo = append(featsInfo, map[string]interface{}{
					"slug": slug,
					"name": slug,
				})
			}
		}
		response["feats"] = featsInfo
	}

	// Fighting Styles
	fightingStylesJSON := getFightingStyles(charID)
	if len(fightingStylesJSON) > 0 {
		fightingStylesInfo := []map[string]interface{}{}
		for _, slug := range fightingStylesJSON {
			if style, ok := fightingStyles[slug]; ok {
				fightingStylesInfo = append(fightingStylesInfo, map[string]interface{}{
					"slug":        style.Slug,
					"name":        style.Name,
					"description": style.Description,
					"mechanic":    style.Mechanic,
				})
			}
		}
		response["fighting_styles"] = fightingStylesInfo
	}

	// Concentration
	if concentratingOn != "" {
		response["concentrating_on"] = concentratingOn
	}

	// Class Resources (Focus Points, Rage, Sorcery Points, etc.)
	chaMod := game.Modifier(cha)
	maxResources := characterMaxResources(charID, class, level, chaMod)
	if len(maxResources) > 0 {
		// Get current resource usage
		currentResources := getCurrentClassResources(charID)

		resourceList := []map[string]interface{}{}
		for _, res := range characterResourceDefs(charID, class) {
			if max, ok := maxResources[res.Key]; ok && max > 0 {
				current := currentResources[res.Key]
				resourceList = append(resourceList, map[string]interface{}{
					"name":          res.Name,
					"key":           res.Key,
					"current":       current,
					"max":           max,
					"recover_short": res.RecoverShort,
					"recover_long":  res.RecoverLong,
				})
			}
		}
		response["class_resources"] = resourceList
		response["class_resources_tip"] = "Use POST /api/characters/{id}/use-resource to spend resources."
	}

	// Crafting in progress (downtime, SRD 5.2.1)
	var trainingProgressRaw string
	db.QueryRow(`SELECT COALESCE(training_progress, '{}') FROM characters WHERE id = $1`, charID).Scan(&trainingProgressRaw)
	if crafting := craftingInProgress(trainingProgressRaw); len(crafting) > 0 {
		response["crafting_in_progress"] = crafting
		response["crafting_tip"] = "Go on with POST /api/characters/downtime {\"character_id\": N, \"item\": \"...\", \"days\": N}."
	}

	// Dragonborn Breath Weapon status
	if strings.ToLower(species) == "dragonborn" {
		var breathWeaponUsed bool
		var draconicAncestry sql.NullString
		db.QueryRow("SELECT COALESCE(breath_weapon_used, false), draconic_ancestry FROM characters WHERE id = $1", charID).Scan(&breathWeaponUsed, &draconicAncestry)

		ancestry := ""
		if draconicAncestry.Valid {
			ancestry = draconicAncestry.String
		}

		breathWeaponInfo := map[string]interface{}{
			"available":       !breathWeaponUsed,
			"used_since_rest": breathWeaponUsed,
			"recovery":        "short or long rest",
		}

		if ancestry != "" {
			damageType := game.DragonAncestryDamageTypes[ancestry]
			area := dragonAncestryAreaShapes[ancestry]
			savingThrow := dragonAncestryBreathSavingThrows[ancestry]
			damageDice := getBreathWeaponDamageDice(level)
			dc := 8 + game.Modifier(con) + game.ProficiencyBonus(level)

			breathWeaponInfo["draconic_ancestry"] = ancestry
			breathWeaponInfo["damage_type"] = damageType
			breathWeaponInfo["damage_dice"] = damageDice
			breathWeaponInfo["area"] = area
			breathWeaponInfo["saving_throw"] = savingThrow
			breathWeaponInfo["dc"] = dc
			breathWeaponInfo["dc_formula"] = "8 + CON mod + proficiency"
			breathWeaponInfo["how_to_use"] = "POST /api/characters/breath-weapon with character_id, target_ids, description"
		} else {
			breathWeaponInfo["ancestry_required"] = true
			breathWeaponInfo["set_ancestry"] = "Set ancestry during character creation with draconic_ancestry field"
			breathWeaponInfo["valid_ancestries"] = []string{"black", "blue", "brass", "bronze", "copper", "gold", "green", "red", "silver", "white"}
		}

		response["breath_weapon"] = breathWeaponInfo
	}

	// Orc Relentless Endurance status
	if isOrc(charID) {
		var relentlessUsed bool
		db.QueryRow("SELECT COALESCE(relentless_endurance_used, false) FROM characters WHERE id = $1", charID).Scan(&relentlessUsed)

		relentlessInfo := map[string]interface{}{
			"available":       !relentlessUsed,
			"used_since_rest": relentlessUsed,
			"recovery":        "long rest",
			"description":     "When reduced to 0 HP but not killed outright, you can drop to 1 HP instead",
			"trigger":         "automatic when dropping to 0 HP",
		}

		response["relentless_endurance"] = relentlessInfo
	}

	// Barbarian Relentless Rage status (level 11+)
	if strings.ToLower(class) == "barbarian" && level >= 11 {
		var relentlessUses int
		db.QueryRow("SELECT COALESCE(relentless_rage_uses, 0) FROM characters WHERE id = $1", charID).Scan(&relentlessUses)

		currentDC := 10 + (5 * relentlessUses)
		relentlessRageInfo := map[string]interface{}{
			"current_dc":      currentDC,
			"uses_since_rest": relentlessUses,
			"recovery":        "short or long rest",
			"description":     "When you drop to 0 HP while raging and don't die outright, make a Constitution save (DC 10, +5 each time; back to 10 after a Short or Long Rest): on a success your Hit Points change to twice your Barbarian level.",
			"trigger":         "CON save when dropping to 0 HP while raging",
			"requirement":     "must be raging",
		}

		if relentlessUses > 0 {
			relentlessRageInfo["tip"] = fmt.Sprintf("⚠️ You've used Relentless Rage %d time(s) this rest. Current DC is %d. Take a short or long rest to reset.", relentlessUses, currentDC)
		} else {
			relentlessRageInfo["tip"] = "🔥 Relentless Rage ready! If you drop to 0 HP while raging, you can make a DC 10 CON save to stay at 1 HP instead."
		}

		response["relentless_rage"] = relentlessRageInfo
	}

	// Barbarian Indomitable Might (level 18+)
	if hasIndomitableMight(class, level, classLevels) {
		// Get effective STR score (includes Primal Champion bonus at level 20)
		effectiveStr := getEffectiveAbilityScore(str, "str", class, level, classLevels)
		response["indomitable_might"] = map[string]interface{}{
			"description":    "If your total for a Strength check is less than your Strength score, you can use that score in place of the total.",
			"str_score":      effectiveStr,
			"minimum_result": effectiveStr,
			"affects":        "all Strength-based ability checks",
			"automatic":      true,
			"tip":            fmt.Sprintf("💪 Indomitable Might: Your STR checks can never be lower than %d (your STR score)!", effectiveStr),
		}
	}

	// Gnomish Cunning info
	if isGnome(charID) {
		response["gnome_cunning"] = map[string]interface{}{
			"description": "Advantage on Intelligence, Wisdom and Charisma saving throws",
			"applies_to":  []string{"Intelligence saves", "Wisdom saves", "Charisma saves"},
			"against":     "magic (spells, magical effects)",
			"automatic":   true,
			"tip":         "🧠 Gnomish Cunning: Advantage on Intelligence, Wisdom and Charisma saving throws.",
		}
	}

	// Fey Ancestry info (Elf)
	if hasFeyAncestry(charID) {
		response["fey_ancestry"] = map[string]interface{}{
			"description":            "Advantage on saving throws against being charmed, and magic can't put you to sleep",
			"charm_save_advantage":   true,
			"magical_sleep_immunity": true,
			"automatic":              true,
			"tip":                    "✨ Your Fey Ancestry protects you from charm effects (advantage on saves) and makes you immune to magical sleep like the Sleep spell!",
		}
	}

	// Dwarven Resilience info
	if isDwarf(charID) {
		response["dwarven_resilience"] = map[string]interface{}{
			"description":              "Advantage on saving throws against poison, and resistance against poison damage",
			"poison_save_advantage":    true,
			"poison_damage_resistance": true,
			"automatic":                true,
			"tip":                      "⛏️ Your Dwarven Resilience grants you advantage on poison saves and halves poison damage - your stout constitution is legendary!",
		}
	}

	// Halfling Brave info
	if isHalfling(charID) {
		response["halfling_brave"] = map[string]interface{}{
			"description":               "Advantage on saving throws against being frightened",
			"frightened_save_advantage": true,
			"automatic":                 true,
			"tip":                       "💪 Your Halfling Brave heart grants advantage on saves against being frightened - your courage is legendary despite your size!",
		}
	}

	// Fiend Warlock - Dark One's Own Luck (level 6+)
	if strings.ToLower(class) == "warlock" && level >= 6 {
		if subclassRaw.Valid && strings.ToLower(subclassRaw.String) == "fiend" {
			var darkOnesLuckUsed bool
			db.QueryRow("SELECT COALESCE(dark_ones_luck_used, false) FROM characters WHERE id = $1", charID).Scan(&darkOnesLuckUsed)

			response["dark_ones_luck"] = map[string]interface{}{
				"available":       !darkOnesLuckUsed,
				"used_since_rest": darkOnesLuckUsed,
				"recovery":        "short or long rest",
				"trigger":         "When you make an ability check or saving throw",
				"effect":          "Add d10 to the roll (after rolling, before determining success)",
				"how_to_use":      "Ask GM to call POST /api/gm/dark-ones-luck after you see a roll result",
				"tip":             "🔥 Your patron's dark luck can boost your ability checks and saving throws - call upon it when you need it most!",
			}
		}
	}

	// Fiend Warlock - Fiendish Resilience (level 10+)
	if strings.ToLower(class) == "warlock" && level >= 10 {
		if subclassRaw.Valid && strings.ToLower(subclassRaw.String) == "fiend" {
			var fiendishRes sql.NullString
			db.QueryRow("SELECT fiendish_resilience FROM characters WHERE id = $1", charID).Scan(&fiendishRes)

			currentResistance := ""
			if fiendishRes.Valid {
				currentResistance = fiendishRes.String
			}

			response["fiendish_resilience"] = map[string]interface{}{
				"current_resistance": currentResistance,
				"has_resistance":     currentResistance != "",
				"can_change":         "After a short or long rest",
				"valid_types":        []string{"acid", "cold", "fire", "lightning", "poison", "thunder", "necrotic", "psychic", "bludgeoning", "piercing", "slashing"},
				"excluded_types":     []string{"radiant", "force"},
				"how_to_use":         "POST /api/characters/fiendish-resilience with character_id and damage_type",
				"tip":                "🛡️ Choose a damage type to resist! Change it after resting to adapt to upcoming threats.",
			}

			// Hurl Through Hell (level 14+)
			if level >= 14 {
				var hurlUsed bool
				db.QueryRow("SELECT COALESCE(hurl_through_hell_used, false) FROM characters WHERE id = $1", charID).Scan(&hurlUsed)
				response["hurl_through_hell"] = map[string]interface{}{
					"available":   !hurlUsed,
					"used":        hurlUsed,
					"recovers_on": "long rest",
					"description": "When you hit a creature with an attack, transport them through the lower planes. They return at the end of your next turn. Non-fiends take 10d10 psychic damage.",
					"how_to_use":  "After hitting with an attack, ask GM to call POST /api/gm/hurl-through-hell",
				}
			}
		}
	}

	// Warlock Eldritch Master (level 20)
	warlockLevel = getWarlockLevel(charID)
	if warlockLevel >= 20 {
		var eldritchMasterUsed bool
		db.QueryRow("SELECT COALESCE(eldritch_master_used, false) FROM characters WHERE id = $1", charID).Scan(&eldritchMasterUsed)

		response["eldritch_master"] = map[string]interface{}{
			"available":    !eldritchMasterUsed,
			"used":         eldritchMasterUsed,
			"recovers_on":  "long rest",
			"casting_time": "1 minute",
			"description":  "Spend 1 minute entreating your patron to regain all expended Pact Magic spell slots.",
			"how_to_use":   "POST /api/characters/eldritch-master with character_id",
		}
	}

	// Wizard Signature Spells (level 20)
	wizardLevel := getWizardLevel(charID)
	if wizardLevel >= 20 {
		var signatureSpellsJSON, signatureSpellsUsedJSON []byte
		db.QueryRow(`SELECT COALESCE(signature_spells, '[]'), COALESCE(signature_spells_used, '[]') FROM characters WHERE id = $1`, charID).Scan(&signatureSpellsJSON, &signatureSpellsUsedJSON)

		var signatureSpells []string
		var signatureSpellsUsed []string
		json.Unmarshal(signatureSpellsJSON, &signatureSpells)
		json.Unmarshal(signatureSpellsUsedJSON, &signatureSpellsUsed)

		// Build spell info with availability
		spellInfo := []map[string]interface{}{}
		for _, spell := range signatureSpells {
			used := false
			for _, usedSpell := range signatureSpellsUsed {
				if usedSpell == spell {
					used = true
					break
				}
			}
			// Get spell name
			var spellName string
			db.QueryRow("SELECT name FROM spells WHERE slug = $1", spell).Scan(&spellName)
			if spellName == "" {
				spellName = spell
			}
			spellInfo = append(spellInfo, map[string]interface{}{
				"slug":      spell,
				"name":      spellName,
				"used":      used,
				"available": !used,
			})
		}

		response["signature_spells"] = map[string]interface{}{
			"spells_chosen":        len(signatureSpells),
			"max_spells":           2,
			"needs_choice":         len(signatureSpells) < 2,
			"spells":               spellInfo,
			"free_casts_remaining": 2 - len(signatureSpellsUsed),
			"recovers_on":          "long rest (free casts)",
			"description":          "Two 3rd-level wizard spells you have mastered. Always prepared (don't count against limit), and you can cast each once at 3rd level without expending a spell slot.",
			"how_to_use":           "POST /api/characters/signature-spells with action='cast', spell='spell-slug'",
		}
	}

	// Evocation Wizard Overchannel (level 14+)
	if wizardLevel >= 14 {
		var wizSubclass sql.NullString
		db.QueryRow("SELECT subclass FROM characters WHERE id = $1", charID).Scan(&wizSubclass)
		if wizSubclass.Valid && wizSubclass.String == "evocation" {
			var overchannelUsed bool
			db.QueryRow("SELECT COALESCE(overchannel_used, false) FROM characters WHERE id = $1", charID).Scan(&overchannelUsed)

			response["overchannel"] = map[string]interface{}{
				"available":       true,
				"used":            overchannelUsed,
				"first_use_free":  !overchannelUsed,
				"penalty_warning": "Subsequent uses deal 2d12 necrotic per spell level (cannot be reduced)",
				"valid_spells":    "Wizard spells of 1st through 5th level that deal damage",
				"how_to_use":      "Include 'overchannel' in cast description (e.g., 'overchannel fireball')",
				"recovers_on":     "long rest",
				"description":     "Deal maximum damage with a wizard spell. First use is free; subsequent uses inflict necrotic backlash.",
			}
		}
	}

	// Fighter Indomitable info (level 9+)
	if strings.ToLower(class) == "fighter" && level >= 9 {
		var indomitableUsed int
		db.QueryRow("SELECT COALESCE(indomitable_used, 0) FROM characters WHERE id = $1", charID).Scan(&indomitableUsed)

		maxUses := getIndomitableMaxUses(class, level)
		remaining := maxUses - indomitableUsed
		if remaining < 0 {
			remaining = 0
		}

		response["indomitable"] = map[string]interface{}{
			"uses_remaining": remaining,
			"uses_max":       maxUses,
			"uses_spent":     indomitableUsed,
			"recovery":       "long rest",
			"description":    "When you fail a saving throw, you can reroll it. You must use the new roll.",
			"trigger":        "After failing any saving throw",
			"how_to_use":     "Ask GM to call POST /api/gm/indomitable with your character_id, ability, and dc",
		}

		if remaining > 0 {
			response["indomitable_tip"] = fmt.Sprintf("⚔️ You have %d Indomitable use(s) remaining! If you fail a save, you can reroll it.", remaining)
		} else {
			response["indomitable_tip"] = "⚔️ Indomitable is exhausted for this long rest. Take a long rest to recover."
		}
	}

	// Rogue Stroke of Luck info (level 20)
	rogueLevel := getRogueLevel(charID, class, level)
	if rogueLevel >= 20 {
		var strokeUsed bool
		db.QueryRow("SELECT COALESCE(stroke_of_luck_used, false) FROM characters WHERE id = $1", charID).Scan(&strokeUsed)

		response["stroke_of_luck"] = map[string]interface{}{
			"available":   !strokeUsed,
			"used":        strokeUsed,
			"recovery":    "short or long rest",
			"description": "If your attack misses, you can turn the miss into a hit. Alternatively, if you fail an ability check, you can treat the d20 roll as a 20.",
			"modes": []map[string]string{
				{"mode": "attack", "description": "Turn a missed attack into a hit"},
				{"mode": "ability_check", "description": "Treat a failed ability check d20 as a 20"},
			},
			"how_to_use": "Ask GM to call POST /api/gm/stroke-of-luck with your character_id and mode",
		}

		if !strokeUsed {
			response["stroke_of_luck_tip"] = "🎲 Stroke of Luck available! If you miss an attack or fail an ability check, ask the GM to use it!"
		} else {
			response["stroke_of_luck_tip"] = "🎲 Stroke of Luck used. Take a short or long rest to recover."
		}
	}

	json.NewEncoder(w).Encode(response)
}

// nullStringToInterface converts sql.NullString to interface{} for JSON
func nullStringToInterface(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

// pickMsg returns a when c is true, else b.
func pickMsg(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
