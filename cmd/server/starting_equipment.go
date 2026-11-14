package main

// Class starting equipment (2024 rules, SRD 5.2.1 "Core ... Traits" tables).
// Each class offers option A (a kit plus a little gold), sometimes B, and a
// gold-only option. Packs become real bags with the SRD contents.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/agentrpg/agentrpg/game"
)

type startItem struct {
	Name  string
	Qty   int
	Place string // "", "worn", "held", "pack", "quiver"; "" = decide by kind
	Focus string // set for spellcasting focuses: "arcane", "divine", "primal", "instrument"
	Note  string
}

type startOption struct {
	Items []startItem
	Gold  int
}

func si(name string, qty int) startItem { return startItem{Name: name, Qty: qty} }

// choiceTool marks the slot filled by the player's chosen instrument or tool.
const choiceTool = "{chosen tool}"

var classStartingEquipment = map[string]map[string]startOption{
	"barbarian": {
		"a": {[]startItem{si("Greataxe", 1), si("Handaxe", 4), si("Explorer's Pack", 1)}, 15},
		"b": {nil, 75},
	},
	"bard": {
		"a": {[]startItem{si("Leather Armor", 1), si("Dagger", 2), {Name: choiceTool, Qty: 1, Focus: "instrument"}, si("Entertainer's Pack", 1)}, 19},
		"b": {nil, 90},
	},
	"cleric": {
		"a": {[]startItem{si("Chain Shirt", 1), si("Shield", 1), si("Mace", 1), {Name: "Holy Symbol, Amulet", Qty: 1, Place: "worn", Focus: "divine"}, si("Priest's Pack", 1)}, 7},
		"b": {nil, 110},
	},
	"druid": {
		"a": {[]startItem{si("Leather Armor", 1), si("Shield", 1), si("Sickle", 1), {Name: "Quarterstaff", Qty: 1, Place: "worn", Focus: "primal", Note: "Druidic Focus"}, si("Explorer's Pack", 1), si("Herbalism Kit", 1)}, 9},
		"b": {nil, 50},
	},
	"fighter": {
		"a": {[]startItem{si("Chain Mail", 1), si("Greatsword", 1), si("Flail", 1), si("Javelin", 8), si("Dungeoneer's Pack", 1)}, 4},
		"b": {[]startItem{si("Studded Leather Armor", 1), si("Scimitar", 1), si("Shortsword", 1), si("Longbow", 1), {Name: "Arrow", Qty: 20, Place: "quiver"}, si("Quiver", 1), si("Dungeoneer's Pack", 1)}, 11},
		"c": {nil, 155},
	},
	"monk": {
		"a": {[]startItem{si("Spear", 1), si("Dagger", 5), {Name: choiceTool, Qty: 1}, si("Explorer's Pack", 1)}, 11},
		"b": {nil, 50},
	},
	"paladin": {
		"a": {[]startItem{si("Chain Mail", 1), si("Shield", 1), si("Longsword", 1), si("Javelin", 6), {Name: "Holy Symbol, Amulet", Qty: 1, Place: "worn", Focus: "divine"}, si("Priest's Pack", 1)}, 9},
		"b": {nil, 150},
	},
	"ranger": {
		"a": {[]startItem{si("Studded Leather Armor", 1), si("Scimitar", 1), si("Shortsword", 1), si("Longbow", 1), {Name: "Arrow", Qty: 20, Place: "quiver"}, si("Quiver", 1), {Name: "Druidic Focus, Sprig of Mistletoe", Qty: 1, Place: "worn", Focus: "primal"}, si("Explorer's Pack", 1)}, 7},
		"b": {nil, 150},
	},
	"rogue": {
		"a": {[]startItem{si("Leather Armor", 1), si("Dagger", 2), si("Shortsword", 1), si("Shortbow", 1), {Name: "Arrow", Qty: 20, Place: "quiver"}, si("Quiver", 1), si("Thieves' Tools", 1), si("Burglar's Pack", 1)}, 8},
		"b": {nil, 100},
	},
	"sorcerer": {
		"a": {[]startItem{si("Spear", 1), si("Dagger", 2), {Name: "Arcane Focus, Crystal", Qty: 1, Place: "worn", Focus: "arcane"}, si("Dungeoneer's Pack", 1)}, 28},
		"b": {nil, 50},
	},
	"warlock": {
		"a": {[]startItem{si("Leather Armor", 1), si("Sickle", 1), si("Dagger", 2), {Name: "Arcane Focus, Orb", Qty: 1, Place: "worn", Focus: "arcane"}, {Name: "Book", Qty: 1, Note: "occult lore"}, si("Scholar's Pack", 1)}, 15},
		"b": {nil, 100},
	},
	"wizard": {
		"a": {[]startItem{si("Dagger", 2), {Name: "Quarterstaff", Qty: 1, Place: "held", Focus: "arcane", Note: "Arcane Focus"}, si("Robe", 1), {Name: "Spellbook", Qty: 1, Focus: "arcane", Note: "Spellcasting Focus"}, si("Scholar's Pack", 1)}, 5},
		"b": {nil, 55},
	},
}

// packContents are the SRD 5.2.1 equipment pack contents. Container is the
// bag the pack comes in.
var packContents = map[string]struct {
	Container string
	Kind      string
	Items     []startItem
}{
	"burglar's pack":     {"Backpack", "backpack", []startItem{si("Ball Bearings", 1), si("Bell", 1), si("Candle", 10), si("Crowbar", 1), si("Lantern, Hooded", 1), si("Oil", 7), si("Rations", 5), si("Rope", 1), si("Tinderbox", 1), si("Waterskin", 1)}},
	"diplomat's pack":    {"Chest", "chest", []startItem{si("Clothes, Fine", 1), si("Ink", 1), si("Ink Pen", 5), si("Lamp", 1), si("Case, Map or Scroll", 2), si("Oil", 4), si("Paper", 5), si("Parchment", 5), si("Perfume", 1), si("Tinderbox", 1)}},
	"dungeoneer's pack":  {"Backpack", "backpack", []startItem{si("Caltrops", 1), si("Crowbar", 1), si("Oil", 2), si("Rations", 10), si("Rope", 1), si("Tinderbox", 1), si("Torch", 10), si("Waterskin", 1)}},
	"entertainer's pack": {"Backpack", "backpack", []startItem{si("Bedroll", 1), si("Bell", 1), si("Lantern, Bullseye", 1), si("Costume", 3), si("Mirror", 1), si("Oil", 8), si("Rations", 9), si("Tinderbox", 1), si("Waterskin", 1)}},
	"explorer's pack":    {"Backpack", "backpack", []startItem{si("Bedroll", 1), si("Oil", 2), si("Rations", 10), si("Rope", 1), si("Tinderbox", 1), si("Torch", 10), si("Waterskin", 1)}},
	"priest's pack":      {"Backpack", "backpack", []startItem{si("Blanket", 1), si("Holy Water", 1), si("Lamp", 1), si("Rations", 7), si("Robe", 1), si("Tinderbox", 1)}},
	"scholar's pack":     {"Backpack", "backpack", []startItem{si("Book", 1), si("Ink", 1), si("Ink Pen", 1), si("Lamp", 1), si("Oil", 10), si("Parchment", 10), si("Tinderbox", 1)}},
}

// StartingEquipmentResult is what a character is given.
type StartingEquipmentResult struct {
	Items         []invItem
	Gold          int
	EquippedArmor string // armor table slug
	Shield        bool
	MainHand      string // weapon table key
}

// startingOptionKeys lists a class's options, for error messages.
func startingOptionKeys(class string) []string {
	opts := classStartingEquipment[strings.ToLower(class)]
	keys := []string{}
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func armorSlugFor(name string) string {
	return strings.ToLower(strings.NewReplacer("'", "", " ", "-").Replace(name))
}

func isArmorName(name string) bool {
	switch strings.ToLower(name) {
	case "leather armor", "padded armor", "studded leather armor", "hide armor", "chain shirt", "scale mail",
		"breastplate", "half plate armor", "ring mail", "chain mail", "splint armor", "plate armor":
		return true
	}
	return false
}

// buildStartingEquipment builds the starting items for a class and option.
// chosenTool fills the Bard's instrument or Monk's tool slot.
func buildStartingEquipment(class, option, chosenTool string) (*StartingEquipmentResult, error) {
	opts, ok := classStartingEquipment[strings.ToLower(strings.TrimSpace(class))]
	if !ok {
		return nil, fmt.Errorf("unknown_class")
	}
	option = strings.ToLower(strings.TrimSpace(option))
	if option == "" {
		option = "a"
	}
	opt, ok := opts[option]
	if !ok {
		return nil, fmt.Errorf("invalid_starting_equipment_option")
	}
	return buildKit(opt.Items, opt.Gold, chosenTool, "i", "class", true)
}

// buildKit turns a list of starting items into inventory entries. idPrefix keeps
// item ids unique when two kits (class and background) go into one inventory.
// When equip is false nothing is wielded automatically (background kits).
func buildKit(items []startItem, gold int, chosenTool, idPrefix, source string, equip bool) (*StartingEquipmentResult, error) {
	res := &StartingEquipmentResult{Gold: gold}

	var inv []invItem
	next := 0
	newID := func() string { next++; return fmt.Sprintf("%s%d", idPrefix, next) }
	add := func(name string, qty int, location string, extra map[string]interface{}) invItem {
		it := invItem{"id": newID(), "name": name, "source": source}
		if qty > 1 {
			it["quantity"] = float64(qty)
		}
		if location != "" {
			it["location"] = location
		}
		for k, v := range extra {
			it[k] = v
		}
		inv = append(inv, it)
		return it
	}

	// pass 1: bags (packs, quivers) so other items can go inside
	var packID, quiverID string
	for _, s := range items {
		key := strings.ToLower(s.Name)
		if p, isPack := packContents[key]; isPack {
			c := containerKinds[p.Kind]
			extra := map[string]interface{}{"type": "container", "container_kind": p.Kind, "capacity": c.Capacity, "pack": s.Name}
			if c.Lbs > 0 {
				extra["capacity_lbs"] = c.Lbs
			}
			loc := "worn"
			if p.Kind == "chest" {
				loc = ""
			}
			bag := add(p.Container, 1, loc, extra)
			packID = itemStr(bag, "id")
			load := 0.0
			for _, pi := range p.Items {
				w, _ := catalogWeightFor(pi.Name)
				if c.Lbs > 0 && load+w*float64(pi.Qty) > float64(c.Lbs) {
					add(pi.Name, pi.Qty, "worn", nil) // the pack's contents can weigh more than the bag holds: strapped on or carried
					continue
				}
				load += w * float64(pi.Qty)
				add(pi.Name, pi.Qty, packID, nil)
			}
		}
		if c, isBag := containerKinds[key]; isBag && key != "chest" {
			for n := 0; n < s.Qty || n == 0; n++ {
				extra := map[string]interface{}{"type": "container", "container_kind": key, "capacity": c.Capacity}
				if c.Lbs > 0 {
					extra["capacity_lbs"] = c.Lbs
				}
				bag := add(c.Name, 1, "worn", extra)
				if key == "quiver" && quiverID == "" {
					quiverID = itemStr(bag, "id")
				}
			}
		}
	}

	// pass 2: everything else. The pack holds what fits its weight limit (a Backpack 30 lb); the rest is carried.
	packLoad, packLimit := 0.0, 0.0
	for _, it := range inv {
		if itemStr(it, "id") == packID {
			if v, ok := it["capacity_lbs"].(float64); ok {
				packLimit = v
			} else if v, ok := it["capacity_lbs"].(int); ok {
				packLimit = float64(v)
			}
		}
		if packID != "" && itemStr(it, "location") == packID {
			w, _ := catalogWeightFor(itemStr(it, "name"))
			q := 1.0
			if v, ok := it["quantity"].(float64); ok {
				q = v
			}
			packLoad += w * q
		}
	}
	intoPack := func(name string, qty int) bool {
		if packID == "" {
			return false
		}
		w, _ := catalogWeightFor(name)
		if packLimit > 0 && packLoad+w*float64(qty) > packLimit {
			return false
		}
		packLoad += w * float64(qty)
		return true
	}
	firstWeaponDone := !equip
	for _, s := range items {
		key := strings.ToLower(s.Name)
		if _, isPack := packContents[key]; isPack {
			continue
		}
		if _, isBag := containerKinds[key]; isBag && key != "chest" {
			continue
		}
		name := s.Name
		if name == choiceTool {
			if chosenTool == "" {
				return nil, fmt.Errorf("tool_choice_required")
			}
			name = titleWords(chosenTool)
			if t, ok := game.FindTool(chosenTool); ok {
				name = titleWords(t.Name)
			}
		}
		extra := map[string]interface{}{}
		if s.Focus != "" {
			extra["spellcasting_focus"] = true
			extra["focus_kind"] = s.Focus
		}
		if s.Note != "" {
			extra["note"] = s.Note
		}
		switch {
		case name == "Shield":
			add(name, 1, "worn", extra)
			res.Shield = true
		case isArmorName(name):
			add(name, 1, "worn", extra)
			res.EquippedArmor = armorSlugFor(name)
		case s.Place == "quiver":
			add(name, s.Qty, quiverID, extra)
		case s.Place == "held":
			add(name, s.Qty, "held", extra)
			if wkey, _, isWeapon := findWeapon(name); isWeapon && res.MainHand == "" {
				res.MainHand = wkey
				firstWeaponDone = true
			}
		case s.Place == "worn":
			add(name, s.Qty, "worn", extra)
		default:
			if wkey, w, isWeapon := findWeapon(name); isWeapon {
				switch {
				case !firstWeaponDone && s.Qty == 1 && w.Type == "melee":
					add(name, 1, "held", extra)
					res.MainHand = wkey
					firstWeaponDone = true
				case s.Qty >= 5 && intoPack(name, s.Qty):
					add(name, s.Qty, packID, extra)
				default:
					add(name, s.Qty, "worn", extra)
				}
				break
			}
			if intoPack(name, s.Qty) {
				add(name, s.Qty, packID, extra)
			} else {
				add(name, s.Qty, "worn", extra)
			}
		}
	}
	res.Items = inv
	return res, nil
}

// chosenToolForClass finds the tool or instrument the player picked for the
// class slot (Bard instrument, Monk tool) among their tool proficiencies.
func chosenToolForClass(class, toolProfsCSV string) string {
	var cats []string
	switch strings.ToLower(class) {
	case "bard":
		cats = []string{game.ToolInstrument}
	case "monk":
		cats = []string{game.ToolArtisan, game.ToolInstrument}
	default:
		return ""
	}
	for _, t := range game.ParseProficiencyList(toolProfsCSV) {
		if tool, ok := game.FindTool(t); ok {
			for _, c := range cats {
				if tool.Category == c {
					return tool.Name
				}
			}
		}
	}
	return ""
}

// ---- endpoints ----

// handleCharacterStartingEquipment gives a character created before starting
// equipment existed their class kit (items only, no gold).
//
//	POST /api/characters/{id}/starting-equipment  {"option":"a"}
//
// handleCharacterStartingEquipment godoc
// @Summary Give an older character its class starting equipment
// @Description For a character made before starting equipment existed: adds the class kit (option "a" or "b" of the class's Starting Equipment; items only, no gold). Once per character.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{option=string} true "a or b"
// @Router /characters/{id}/starting-equipment [post]
func handleCharacterStartingEquipment(w http.ResponseWriter, r *http.Request, charID int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if !requireOwnerOrGM(w, r, charID) {
		return
	}
	var req struct {
		Option string `json:"option"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var class, tools, marker string
	if err := db.QueryRow(`SELECT COALESCE(class,''), COALESCE(tool_proficiencies,''), COALESCE(starting_equipment,'') FROM characters WHERE id = $1`, charID).
		Scan(&class, &tools, &marker); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if marker != "" {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "starting_equipment_already_given", "option": marker})
		return
	}
	opt := strings.ToLower(strings.TrimSpace(req.Option))
	if opt == "" {
		opt = "a"
	}
	if o := classStartingEquipment[strings.ToLower(class)][opt]; len(o.Items) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "option_has_no_items", "hint": "Pick an option that includes equipment; the others are gold only.", "options": startingOptionKeys(class)})
		return
	}
	res, err := buildStartingEquipment(class, opt, chosenToolForClass(class, tools))
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": publicError(err), "hint": "Choose your tool proficiency first (POST /api/characters/{id}/tool-proficiencies)."})
		return
	}

	mutateInventory(w, charID, func(inv []invItem) ([]invItem, map[string]interface{}, int, string) {
		// re-number so new items do not clash with existing ids
		base := 0
		for _, it := range inv {
			if id := itemStr(it, "id"); strings.HasPrefix(id, "i") {
				var n int
				fmt.Sscanf(id[1:], "%d", &n)
				if n > base {
					base = n
				}
			}
		}
		remap := map[string]string{}
		for i, it := range res.Items {
			remap[itemStr(it, "id")] = fmt.Sprintf("i%d", base+i+1)
		}
		for _, it := range res.Items {
			it["id"] = remap[itemStr(it, "id")]
			if loc := itemStr(it, "location"); loc != "" {
				if m, ok := remap[loc]; ok {
					it["location"] = m
				}
			}
			inv = append(inv, it)
		}
		inv = removeFocusPlaceholders(inv)
		fillItemWeights(inv)
		return inv, map[string]interface{}{"option": opt, "items_added": len(res.Items)}, 0, ""
	})
	db.Exec(`UPDATE characters SET starting_equipment = $1,
		equipped_armor = COALESCE(equipped_armor, NULLIF($2, '')), equipped_shield = (COALESCE(equipped_shield, false) OR $3),
		equipped_main_hand = COALESCE(equipped_main_hand, NULLIF($4, '')) WHERE id = $5`,
		opt, res.EquippedArmor, res.Shield, res.MainHand, charID)
}

// handleCharacterToolProficiencies shows and sets the tool choices a class asks for.
//
//	GET  /api/characters/{id}/tool-proficiencies   pending choices and options
//	POST /api/characters/{id}/tool-proficiencies   {"tools":["lute","drum","flute"]}
//
// handleCharacterToolProficiencies godoc
// @Summary The tool proficiencies a class lets you choose
// @Description GET shows the tool choices still open (a Bard's three Musical Instruments, a Monk's Artisan's Tools or Musical Instrument) and the options. POST sets them ({"tools": ["lute", "drum", "flute"]}).
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{tools=[]string} false "Tools (POST)"
// @Router /characters/{id}/tool-proficiencies [get]
// @Router /characters/{id}/tool-proficiencies [post]
func handleCharacterToolProficiencies(w http.ResponseWriter, r *http.Request, charID int) {
	var class, tools string
	var classLevelsRaw []byte
	if err := db.QueryRow(`SELECT COALESCE(class,''), COALESCE(class_levels,'{}'), COALESCE(tool_proficiencies,'') FROM characters WHERE id = $1`, charID).
		Scan(&class, &classLevelsRaw, &tools); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	var levels map[string]int
	json.Unmarshal(classLevelsRaw, &levels)
	pending := game.PendingToolChoices(game.ToolChoicesFor(class, levels), tools)

	switch r.Method {
	case http.MethodGet:
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"tool_proficiencies": game.ParseProficiencyList(tools), "pending_choices": pending})
	case http.MethodPost:
		if !requireOwnerOrGM(w, r, charID) {
			return
		}
		var req struct {
			Tools []string `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
		names, msg := game.ValidateToolChoices(req.Tools, pending)
		if msg != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": msg, "pending_choices": pending})
			return
		}
		merged := game.MergeProficiencyList(tools, names)
		if _, err := db.Exec(`UPDATE characters SET tool_proficiencies = $1 WHERE id = $2`, merged, charID); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
			return
		}
		still := game.PendingToolChoices(game.ToolChoicesFor(class, levels), merged)
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "tool_proficiencies": game.ParseProficiencyList(merged), "pending_choices": still})
	default:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}

// handleCharacterClassOptions sets the level 1 class choices that change
// proficiencies (Cleric Divine Order, Druid Primal Order). Settable once.
//
//	POST /api/characters/{id}/class-options  {"divine_order":"protector"}
//
// handleCharacterClassOptions godoc
// @Summary Class choices: Divine Order, Primal Order, land type, Elemental Affinity, Blessed Strikes, Elemental Fury
// @Description Sets one class choice: divine_order (Cleric 1: protector or thaumaturge), primal_order (Druid 1: magician or warden), land_type (Circle of the Land: arid, polar, temperate or tropical; again after a Long Rest), elemental_affinity (Draconic Sorcerer 6: acid, cold, fire, lightning, poison), blessed_strikes (Cleric 7: divine_strike or potent_spellcasting), elemental_fury (Druid 7: primal_strike or potent_spellcasting), wild_shape_forms (Druid 2+: the known Beast forms, 4 at level 2, 6 at 4, 8 at 8 with Fly Speeds allowed; one can be replaced after a Long Rest). The level 1 orders can be set once.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{divine_order=string,primal_order=string,land_type=string,elemental_affinity=string,blessed_strikes=string,elemental_fury=string,wild_shape_forms=[]string,primal_knowledge=string,bonus_proficiencies=[]string} true "The choice (primal_knowledge: Barbarian 3, one Barbarian skill; bonus_proficiencies: College of Lore 3, three skills)"
// @Router /characters/{id}/class-options [post]
func handleCharacterClassOptions(w http.ResponseWriter, r *http.Request, charID int) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if !requireOwnerOrGM(w, r, charID) {
		return
	}
	var req struct {
		DivineOrder string `json:"divine_order"`
		PrimalOrder string `json:"primal_order"`

		LandType          string   `json:"land_type"`           // Circle of the Land: arid, polar, temperate, tropical (again after a Long Rest)
		ElementalAffinity string   `json:"elemental_affinity"`  // Draconic Sorcerer level 6: acid, cold, fire, lightning, poison
		BlessedStrikes    string   `json:"blessed_strikes"`     // Cleric level 7: divine_strike or potent_spellcasting
		ElementalFury     string   `json:"elemental_fury"`      // Druid level 7: primal_strike or potent_spellcasting
		WildShapeForms    []string `json:"wild_shape_forms"`    // Druid: the known Beast forms (4, 6 or 8; one can be replaced after a Long Rest)
		PrimalKnowledge   string   `json:"primal_knowledge"`    // Barbarian 3: one skill from the Barbarian list (levels23_2024.go)
		BonusProfs        []string `json:"bonus_proficiencies"` // College of Lore 3: three skills of your choice
		MagicalDiscover   []string `json:"magical_discoveries"` // College of Lore 6: two Cleric, Druid or Wizard spells (magical_discoveries_2024.go)
		ClassLanguages    []string `json:"class_languages"`     // Deft Explorer (Ranger 2), Thieves' Cant (Rogue 1) (class_languages_2024.go)
	}
	// one choice per request, and a field that isn't one of these is refused (F22: {"divine_order": ..., "primal_order": ...} saved
	// the first and ignored the second without a word)
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sent); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": "Send a JSON object with one class choice. Nothing was changed."})
		return
	}
	known := []string{"divine_order", "primal_order", "land_type", "elemental_affinity", "blessed_strikes", "elemental_fury", "wild_shape_forms", "primal_knowledge", "bonus_proficiencies", "magical_discoveries", "class_languages"}
	var keys []string
	for k := range sent {
		if k == "character_id" {
			continue
		}
		if !containsStr(known, k) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_field",
				"message": fmt.Sprintf("Unknown field %q. Nothing was changed.", k), "valid_fields": known})
			return
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 1 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "one_choice_per_request",
			"message": fmt.Sprintf("Send one class choice per request; you sent %s. Nothing was changed.", strings.Join(keys, " and "))})
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json", "message": publicError(err) + ". Nothing was changed."})
		return
	}
	if req.ClassLanguages != nil {
		if why := setClassLanguages(charID, req.ClassLanguages); why != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_languages", "message": why})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "languages": languagesOf(charID)})
		return
	}
	if req.MagicalDiscover != nil {
		if why := setMagicalDiscoveries(charID, req.MagicalDiscover); why != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_magical_discoveries", "message": why})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "magical_discoveries": magicalDiscoveriesOf(charID),
			"message": "Learned; these two spells are always prepared and don't count against your prepared spells."})
		return
	}
	if req.WildShapeForms != nil {
		if why := setWildShapeForms(charID, req.WildShapeForms); why != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_wild_shape_forms", "message": why})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"success": true, "wild_shape_forms": knownWildShapes(charID)})
		return
	}
	if applySkillChoice(w, charID, req.PrimalKnowledge, req.BonusProfs) {
		return
	}
	if applyClassChoice(w, charID, req.LandType, req.ElementalAffinity, req.BlessedStrikes, req.ElementalFury) {
		return
	}
	var class, divine, primal string
	if err := db.QueryRow(`SELECT COALESCE(class,''), COALESCE(divine_order,''), COALESCE(primal_order,'') FROM characters WHERE id = $1`, charID).
		Scan(&class, &divine, &primal); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	c := strings.ToLower(class)
	if req.DivineOrder != "" && c != "cleric" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_your_class", "message": fmt.Sprintf("Only Clerics choose a Divine Order; a %s has none. Nothing was changed.", titleWords(c))})
		return
	}
	if req.PrimalOrder != "" && c != "druid" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_your_class", "message": fmt.Sprintf("Only Druids choose a Primal Order; a %s has none. Nothing was changed.", titleWords(c))})
		return
	}
	saved := map[string]interface{}{"success": true}
	switch {
	case req.DivineOrder != "" && c == "cleric":
		v := strings.ToLower(strings.TrimSpace(req.DivineOrder))
		if v != "protector" && v != "thaumaturge" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_divine_order", "allowed": []string{"protector", "thaumaturge"}, "message": fmt.Sprintf("%q is not a Divine Order: choose protector or thaumaturge.", req.DivineOrder)})
			return
		}
		if divine != "" {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "already_chosen", "divine_order": divine,
				"message": fmt.Sprintf("You already chose %s: the Divine Order is chosen once, at level 1.", titleWords(divine))})
			return
		}
		db.Exec(`UPDATE characters SET divine_order = $1 WHERE id = $2`, v, charID)
		saved["divine_order"], saved["message"] = v, "Divine Order: "+titleWords(v)+"."
	case req.PrimalOrder != "" && c == "druid":
		v := strings.ToLower(strings.TrimSpace(req.PrimalOrder))
		if v != "warden" && v != "magician" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_primal_order", "allowed": []string{"warden", "magician"}, "message": fmt.Sprintf("%q is not a Primal Order: choose warden or magician.", req.PrimalOrder)})
			return
		}
		if primal != "" {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "already_chosen", "primal_order": primal,
				"message": fmt.Sprintf("You already chose %s: the Primal Order is chosen once, at level 1.", titleWords(primal))})
			return
		}
		db.Exec(`UPDATE characters SET primal_order = $1 WHERE id = $2`, v, charID)
		saved["primal_order"], saved["message"] = v, "Primal Order: "+titleWords(v)+"."
	default:
		msg := "Nothing to set: a Cleric sends divine_order (protector or thaumaturge), a Druid primal_order (warden or magician). Nothing was changed."
		switch {
		case c == "cleric":
			msg = "divine_order is empty or missing: send protector or thaumaturge. Nothing was changed."
		case c == "druid":
			msg = "primal_order is empty or missing: send warden or magician. Nothing was changed."
		case c != "":
			msg = fmt.Sprintf("A %s has no Divine or Primal Order to choose (only a Cleric or a Druid does). Nothing was changed.", titleWords(c))
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "nothing_to_set", "message": msg,
			"hint": "Clerics set divine_order (protector or thaumaturge); Druids set primal_order (warden or magician)."})
		return
	}
	syncCharacterProficiencies(charID)
	writeJSONStatus(w, http.StatusOK, saved)
}

// genericFocusNames are the placeholder focus items characters were given before
// class kits existed.
var genericFocusNames = map[string]bool{
	"arcane focus": true, "holy symbol": true, "druidic focus": true, "musical instrument": true,
}

// removeFocusPlaceholders drops the generic focus placeholder items. Used once a
// real kit (with its own focus) has been given.
func removeFocusPlaceholders(inv []invItem) []invItem {
	out := make([]invItem, 0, len(inv))
	for _, it := range inv {
		if itemStr(it, "type") == "focus" && itemStr(it, "source") == "class" && genericFocusNames[strings.ToLower(itemStr(it, "name"))] {
			continue
		}
		out = append(out, it)
	}
	return out
}

// cleanFocusPlaceholders runs at startup: characters who already have a class
// kit with a real focus no longer need the generic placeholder.
func cleanFocusPlaceholders() {
	rows, err := db.Query(`SELECT id, COALESCE(class,''), COALESCE(starting_equipment,''), COALESCE(inventory,'[]') FROM characters WHERE COALESCE(starting_equipment,'') <> ''`)
	if err != nil {
		return
	}
	type rec struct {
		id            int
		class, option string
		raw           []byte
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if rows.Scan(&r.id, &r.class, &r.option, &r.raw) == nil {
			recs = append(recs, r)
		}
	}
	rows.Close()
	for _, r := range recs {
		if len(classStartingEquipment[strings.ToLower(r.class)][r.option].Items) == 0 {
			continue // gold-only start keeps its basic focus
		}
		var inv []invItem
		if json.Unmarshal(r.raw, &inv) != nil {
			continue
		}
		cleaned := removeFocusPlaceholders(inv)
		if len(cleaned) != len(inv) {
			b, _ := json.Marshal(cleaned)
			db.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, string(b), r.id)
		}
	}
}
