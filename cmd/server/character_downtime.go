package main

// Downtime: crafting nonmagical items by the SRD 5.2.1 ("Crafting Nonmagical Items", "Brewing Potions of Healing" and
// "Scribing Spell Scrolls").
//
// Scribing (activity "scribe", spell): proficiency in Arcana or with Calligrapher's Supplies; the spell prepared on each day of the
// work and its costly Material components at hand (used up when the scroll is done, if the spell uses them up); the time and cost
// of the Spell Scroll Costs table (a cantrip or level 1: 1 day; level 3: 5 days and 150 GP; level 9: 120 days and 50,000 GP),
// the cost paid when the work starts. The scroll uses the scribe's spell save DC and spell attack bonus; a scribed cantrip works
// at the scribe's level.
// The 2014 downtime activities (work, recuperate, training for 250 days, research) have no 2024 rule (neither the Player's
// Handbook nor the Dungeon Master's Guide of 2024 has them) and were removed; the GM grants gold, proficiencies and checks.
//
// Crafting: the item's tool (the Craft line of each tool in the SRD; the GM assigns a tool for anything else) and proficiency
// with it; raw materials worth half the item's purchase cost (rounded down), paid when the work starts; one day of 8 hours per
// 10 GP of the purchase cost (rounded up); the days needn't be consecutive; a helper who is also proficient with the tool divides
// the time (normally one helper). A Potion of Healing takes the Herbalism Kit, 25 GP of raw materials and 1 day.

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/agentrpg/agentrpg/game"
)

// craftEntry is one item of a tool's Craft line: a named item, or a group ("Any Melee weapon (except ...)").
type craftEntry struct {
	tool   string   // canonical tool name, "smith's tools"
	name   string   // a named item, normalised; empty for a group
	group  string   // "melee weapon", "ranged weapon", "medium armor" or "heavy armor"
	except []string // normalised names left out of the group
}

var (
	craftOnce    sync.Once
	craftItems   map[string]catalogRow // itemKey -> the SRD weapon, armor, gear (ammunition included) and tool rows
	craftMagic   map[string]bool       // itemKeys of the magic items (crafted by other rules)
	craftEntries []craftEntry
)

func craftKey(name string) string {
	n := strings.ToLower(strings.TrimSpace(strings.NewReplacer("’", "'").Replace(name)))
	return strings.Join(strings.Fields(n), " ")
}

// uninvert turns the SRD gear table's "Bottle, Glass" into "Glass Bottle".
func uninvert(name string) string {
	if parts := strings.Split(name, ", "); len(parts) == 2 {
		return parts[1] + " " + parts[0]
	}
	return name
}

// ammunitionRows are the types in the SRD Ammunition table (Arrows, Bolts, Firearm Bullets, Sling Bullets, Needles), with the
// amount one purchase gives.
func ammunitionRows() []catalogRow {
	var out []catalogRow
	for _, g := range srdEquipment().Gear.Items {
		if g.Name != "Ammunition" {
			continue
		}
		for _, x := range g.Text {
			m, _ := x.(map[string]interface{})
			table, _ := m["table"].(map[string]interface{})
			rows, _ := table["rows"].([]interface{})
			for i, row := range rows {
				cells, _ := row.([]interface{})
				if i == 0 || len(cells) < 5 {
					continue
				}
				name, _ := cells[0].(string)
				amount, _ := cells[1].(string)
				cost, _ := cells[4].(string)
				n := 1
				fmt.Sscanf(amount, "%d", &n)
				out = append(out, catalogRow{Category: "gear", Slug: itemSlug(uninvert(name)), Name: uninvert(name), Source: "SRD 5.2.1",
					CostText: cost, CostGP: costGP(cost), Data: map[string]interface{}{"amount": n}})
			}
		}
	}
	return out
}

func loadCraftData() {
	craftOnce.Do(func() {
		craftItems, craftMagic = map[string]catalogRow{}, map[string]bool{}
		var rows []catalogRow
		rows = append(rows, weaponRows()...)
		rows = append(rows, armorRows()...)
		rows = append(rows, equipmentRows()...)
		rows = append(rows, ammunitionRows()...)
		for _, r := range rows {
			if r.Name != "Ammunition" {
				craftItems[itemKey(r.Name)] = r
			}
		}
		for _, r := range magicItemRows() {
			craftMagic[itemKey(r.Name)] = true
		}
		for _, r := range rows {
			if r.Category != "tool" {
				continue
			}
			text, _ := r.Data["craft"].(string)
			tool := game.CanonicalToolName(r.Name)
			for _, e := range splitTopLevel(text) {
				craftEntries = append(craftEntries, parseCraftEntry(tool, e))
			}
		}
	})
}

// splitTopLevel splits a Craft line at the commas that aren't inside parentheses.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				if p := strings.TrimSpace(s[start:i]); p != "" {
					out = append(out, p)
				}
				start = i + 1
			}
		}
	}
	if p := strings.TrimSpace(s[start:]); p != "" {
		out = append(out, p)
	}
	return out
}

func parseCraftEntry(tool, e string) craftEntry {
	k := craftKey(e)
	ce := craftEntry{tool: tool}
	base, except := k, ""
	if i := strings.Index(k, " (except "); i >= 0 && strings.HasSuffix(k, ")") {
		base, except = k[:i], k[i+len(" (except "):len(k)-1]
	}
	for _, x := range strings.Split(strings.ReplaceAll(except, " and ", ", "), ",") {
		if x = strings.TrimSpace(x); x != "" {
			ce.except = append(ce.except, x)
		}
	}
	switch base {
	case "any melee weapon", "melee weapons":
		ce.group = "melee weapon"
	case "ranged weapons", "any ranged weapon":
		ce.group = "ranged weapon"
	case "medium armor":
		ce.group = "medium armor"
	case "heavy armor":
		ce.group = "heavy armor"
	default:
		ce.name = base
	}
	return ce
}

// matches reports whether a tool's Craft entry covers a catalog row. Names are compared by itemKey, so "Glass Bottle" is the
// table's "Bottle, Glass" and "Arrows" is "Arrow"; "(except Hide)" leaves out Hide Armor.
func (ce craftEntry) matches(r catalogRow) bool {
	k := itemKey(r.Name)
	for _, x := range ce.except {
		if k == itemKey(x) || k == itemKey(x+" armor") {
			return false
		}
	}
	switch ce.group {
	case "melee weapon", "ranged weapon":
		t, _ := r.Data["type"].(string)
		return r.Category == "weapon" && t+" weapon" == ce.group
	case "medium armor", "heavy armor":
		t, _ := r.Data["armor_type"].(string)
		return r.Category == "armor" && t+" armor" == ce.group
	}
	return k == itemKey(ce.name)
}

// findCraftItem looks an item up in the SRD weapon, armor, gear (ammunition included) and tool tables.
func findCraftItem(name string) (catalogRow, bool) {
	loadCraftData()
	r, ok := craftItems[itemKey(name)]
	return r, ok
}

// craftToolsFor lists the tools whose SRD Craft line covers the item.
func craftToolsFor(r catalogRow) []string {
	loadCraftData()
	var out []string
	for _, ce := range craftEntries {
		if ce.matches(r) && !containsStr(out, ce.tool) {
			out = append(out, ce.tool)
		}
	}
	return out
}

// craftPlan is what crafting one item takes.
type craftPlan struct {
	Item      string   // the item's name, as it goes into the inventory
	Qty       int      // how many one crafting makes (ammunition comes in 20s and so on)
	Tools     []string // the tools that can make it (empty: the GM assigns one)
	CostGP    float64  // purchase cost
	Materials int      // raw materials in GP, rounded down
	Days      int      // days of 8 hours
	Note      string
}

// planCraft works out the tool, the raw materials and the time for an item (SRD 5.2.1). itemCost is only used for items
// outside the SRD tables (or without a price there). It returns an error code and message when the item can't be crafted.
func planCraft(item string, itemCost float64) (craftPlan, string, string) {
	loadCraftData()
	k := craftKey(item)
	switch {
	case k == "":
		return craftPlan{}, "item_required", "Name the item to craft in item."
	case itemKey(item) == itemKey("Potion of Healing"):
		return craftPlan{Item: "Potion of Healing", Qty: 1, Tools: []string{"herbalism kit"}, CostGP: 50, Materials: 25, Days: 1,
			Note: "Brewing Potions of Healing: Herbalism Kit, 25 GP of raw materials, 1 day (8 hours)."}, "", ""
	case strings.Contains(k, "spell scroll"):
		return craftPlan{}, "use_scribe", "A Spell Scroll is scribed, not crafted: send activity \"scribe\" with spell (a spell you have prepared)."
	}
	p := craftPlan{Qty: 1}
	if r, ok := findCraftItem(item); ok {
		p.Item, p.Tools, p.CostGP = uninvert(r.Name), craftToolsFor(r), r.CostGP
		if n, ok := r.Data["amount"].(int); ok && n > 1 {
			p.Qty = n
		}
	} else if craftMagic[itemKey(item)] {
		return craftPlan{}, "magic_item", "Magic items aren't crafted with these rules; ask your GM."
	} else {
		p.Item = strings.TrimSpace(item)
		p.Note = "Not in the SRD tables: the GM assigns the tool and confirms the cost."
	}
	if itemCost < 0 || itemCost > 1000000 || math.IsNaN(itemCost) || math.IsInf(itemCost, 0) {
		return craftPlan{}, "invalid_item_cost", "item_cost must be between 0 and 1,000,000 GP."
	}
	if p.CostGP <= 0 {
		if itemCost <= 0 {
			return craftPlan{}, "item_cost_required", fmt.Sprintf("%s has no price in the SRD tables: send item_cost (its purchase cost in GP) and tool; the GM confirms them.", p.Item)
		}
		p.CostGP = itemCost
	}
	p.Materials = int(math.Floor(p.CostGP/2 + 1e-9))
	p.Days = int(math.Ceil(p.CostGP/10 - 1e-9))
	if p.Days < 1 {
		p.Days = 1
	}
	return p, "", ""
}

// craftProgress is one item being crafted, kept in characters.training_progress under "craft:<slug>".
type craftProgress struct {
	Item       string `json:"item"`
	Qty        int    `json:"qty,omitempty"`
	Tool       string `json:"tool"`
	Days       int    `json:"days"`        // days of work done, a helper's included
	DaysNeeded int    `json:"days_needed"` // days of work the item takes
	Materials  int    `json:"materials_gp"`
}

// craftingInProgress lists the items a character is crafting, for the sheet.
func craftingInProgress(raw string) []map[string]interface{} {
	var progress map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &progress) != nil {
		return nil
	}
	var out []map[string]interface{}
	for key, v := range progress {
		var cp craftProgress
		if !strings.HasPrefix(key, "craft:") || json.Unmarshal(v, &cp) != nil || cp.DaysNeeded <= 0 {
			continue
		}
		out = append(out, map[string]interface{}{"item": cp.Item, "tool": game.ToolDisplayName(cp.Tool), "days_done": cp.Days, "days_needed": cp.DaysNeeded})
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]["item"]) < fmt.Sprint(out[j]["item"]) })
	return out
}

// handleCharacterDowntime godoc
// @Summary Craft an item or scribe a Spell Scroll in downtime
// @Description activity "scribe" with spell: a Spell Scroll of a spell you have prepared (proficiency in Arcana or with Calligrapher's Supplies; costly Material components at hand; the SRD time and cost by spell level, paid at the start; the scroll uses your spell save DC and attack bonus). Crafting a nonmagical item (SRD 5.2.1): you need the item's tool and proficiency with it, raw materials worth half the item's purchase cost (paid when you start) and one day of 8 hours per 10 GP of its cost, rounded up. The days needn't be consecutive: send days in as many calls as you like; progress is kept. helper_id: one other character in your campaign who is also proficient with the tool divides the time. A Potion of Healing takes the Herbalism Kit, 25 GP and 1 day. An item outside the SRD tables needs tool and item_cost (the GM assigns the tool). Not during combat. The 2014 activities (work, recuperate, train, research) have no 2024 rule: the GM grants gold, proficiencies and checks.
// @Tags Characters
// @Accept json
// @Produce json
// @Security BasicAuth
// @Param request body object{character_id=int,activity=string,item=string,spell=string,days=int,tool=string,item_cost=number,helper_id=int} true "activity: craft (the default) or scribe"
// @Success 200 {object} map[string]interface{} "Crafting progress"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /characters/downtime [post]
func handleCharacterDowntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	var req struct {
		CharacterID int     `json:"character_id"`
		Activity    string  `json:"activity"`
		Item        string  `json:"item"`
		Days        int     `json:"days"`
		Tool        string  `json:"tool"`
		ItemCost    float64 `json:"item_cost"`
		HelperID    int     `json:"helper_id"`
		Spell       string  `json:"spell"` // scribe: the spell to put on the scroll
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	if req.CharacterID == 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "character_id_required"})
		return
	}
	if !requireOwnerOrGM(w, r, req.CharacterID) {
		return
	}
	switch strings.ToLower(strings.TrimSpace(req.Activity)) {
	case "", "craft", "crafting", "brew":
	case "scribe", "scribing":
		if req.Days <= 0 {
			req.Days = 1
		}
		scribeScroll(w, req.CharacterID, req.Spell, min(req.Days, 365))
		return
	case "work", "recuperate", "train", "training", "research":
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_a_2024_activity",
			"message": fmt.Sprintf("The 2024 rules have no %q downtime activity. Ask your GM, who can grant gold, a proficiency or a check.", req.Activity)})
		return
	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "unknown_activity", "available": []string{"craft", "scribe"}})
		return
	}
	if req.Days <= 0 {
		req.Days = 1
	}
	if req.Days > 365 {
		req.Days = 365
	}

	plan, code, msg := planCraft(req.Item, req.ItemCost)
	if code != "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": code, "message": msg})
		return
	}

	var name, toolsRaw string
	var lobby int
	if db.QueryRow(`SELECT name, COALESCE(lobby_id, 0), COALESCE(tool_proficiencies, '') FROM characters WHERE id = $1`, req.CharacterID).
		Scan(&name, &lobby, &toolsRaw) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	var fighting bool
	if lobby > 0 {
		db.QueryRow(`SELECT COALESCE(active, false) FROM combat_state WHERE lobby_id = $1`, lobby).Scan(&fighting)
	}
	if fighting {
		writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "in_combat", "message": "Crafting is downtime: finish the fight first."})
		return
	}

	// The tool: one whose Craft line names the item; for anything else the GM assigns one.
	owned := game.ParseProficiencyList(toolsRaw)
	toolNames := func(list []string) []string {
		out := make([]string, 0, len(list))
		for _, t := range list {
			out = append(out, game.ToolDisplayName(t))
		}
		return out
	}
	tool := ""
	switch {
	case req.Tool != "":
		if len(plan.Tools) > 0 && !game.HasToolProficiency(plan.Tools, req.Tool) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "wrong_tool",
				"message": fmt.Sprintf("%s is made with %s (SRD 5.2.1).", plan.Item, strings.Join(toolNames(plan.Tools), " or ")), "tools": toolNames(plan.Tools)})
			return
		}
		tool = game.CanonicalToolName(req.Tool)
	case len(plan.Tools) == 0:
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "tool_required",
			"message": fmt.Sprintf("No tool in the SRD lists %s: the GM assigns one. Send tool.", plan.Item)})
		return
	default:
		tool = plan.Tools[0]
		for _, t := range plan.Tools {
			if game.HasToolProficiency(owned, t) {
				tool = t
				break
			}
		}
	}
	if !game.HasToolProficiency(owned, tool) {
		need := plan.Tools
		if len(need) == 0 || req.Tool != "" {
			need = []string{tool}
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_proficient",
			"message":    fmt.Sprintf("Crafting %s needs proficiency with %s.", plan.Item, strings.Join(toolNames(need), " or ")),
			"your_tools": toolNames(owned)})
		return
	}

	// A helper: another character of the campaign, also proficient with the tool (normally only one).
	helpers, helperName := 0, ""
	if req.HelperID != 0 {
		var hLobby int
		var hTools string
		if req.HelperID == req.CharacterID || db.QueryRow(`SELECT name, COALESCE(lobby_id, 0), COALESCE(tool_proficiencies, '') FROM characters WHERE id = $1`, req.HelperID).
			Scan(&helperName, &hLobby, &hTools) != nil || hLobby != lobby || lobby == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_helper", "message": "helper_id must be another character in your campaign."})
			return
		}
		if !game.HasToolProficiency(game.ParseProficiencyList(hTools), tool) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "helper_not_proficient",
				"message": fmt.Sprintf("Anyone who helps must also be proficient with %s.", game.ToolDisplayName(tool))})
			return
		}
		helpers = 1
	}

	// Gold, progress and the inventory change together, so two requests at once can't pay twice or finish twice.
	tx, err := db.Begin()
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error"})
		return
	}
	defer tx.Rollback()
	var gold int
	var progressRaw, invRaw []byte
	if err := tx.QueryRow(`SELECT COALESCE(gold, 0), COALESCE(training_progress, '{}'), COALESCE(inventory, '[]') FROM characters WHERE id = $1 FOR UPDATE`, req.CharacterID).
		Scan(&gold, &progressRaw, &invRaw); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error"})
		return
	}
	progress := map[string]json.RawMessage{}
	json.Unmarshal(progressRaw, &progress)
	if progress == nil {
		progress = map[string]json.RawMessage{}
	}
	key := "craft:" + itemSlug(plan.Item)
	var cp craftProgress
	started := false
	if raw, ok := progress[key]; ok && json.Unmarshal(raw, &cp) == nil && cp.DaysNeeded > 0 {
		started = true
	}
	if !started {
		if gold < plan.Materials {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "insufficient_gold",
				"message":   fmt.Sprintf("Crafting %s takes %d GP of raw materials (half its %s GP cost); you have %d GP.", plan.Item, plan.Materials, trimFloat(plan.CostGP), gold),
				"gold_have": gold, "gold_need": plan.Materials})
			return
		}
		gold -= plan.Materials
		cp = craftProgress{Item: plan.Item, Qty: plan.Qty, Tool: tool, DaysNeeded: plan.Days, Materials: plan.Materials}
	}
	cp.Days += req.Days * (1 + helpers)
	done := cp.Days >= cp.DaysNeeded

	var inv []map[string]interface{}
	json.Unmarshal(invRaw, &inv)
	if done {
		delete(progress, key)
		b := make([]byte, 4)
		rand.Read(b)
		item := map[string]interface{}{"id": "c" + hex.EncodeToString(b), "name": cp.Item, "quantity": max(1, cp.Qty), "crafted": true}
		fillItemWeights([]map[string]interface{}{item})
		inv = append(inv, item)
	} else {
		v, _ := json.Marshal(cp)
		progress[key] = v
	}
	progressOut, _ := json.Marshal(progress)
	invOut, _ := json.Marshal(inv)
	if _, err := tx.Exec(`UPDATE characters SET gold = $1, training_progress = $2, inventory = $3 WHERE id = $4`, gold, progressOut, invOut, req.CharacterID); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
		return
	}
	result := fmt.Sprintf("%d of %d days of work", min(cp.Days, cp.DaysNeeded), cp.DaysNeeded)
	if done {
		result = fmt.Sprintf("finished after %d days of work; the item is in the inventory (unsorted)", cp.DaysNeeded)
	}
	var logLobby sql.NullInt64
	if lobby > 0 {
		logLobby = sql.NullInt64{Int64: int64(lobby), Valid: true}
	}
	tx.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'downtime', $3, $4)`,
		logLobby, req.CharacterID, fmt.Sprintf("%s crafts %s with %s for %d day(s)", name, cp.Item, game.ToolDisplayName(cp.Tool), req.Days), result)
	if err := tx.Commit(); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
		return
	}

	resp := map[string]interface{}{
		"success": true, "activity": "craft", "character": name, "item": cp.Item, "tool": game.ToolDisplayName(cp.Tool),
		"days_worked": req.Days, "progress_days": min(cp.Days, cp.DaysNeeded), "days_needed": cp.DaysNeeded, "complete": done, "gold": gold,
	}
	if !started {
		resp["materials_gp"] = plan.Materials
		resp["materials_note"] = "Raw materials worth half the item's purchase cost, paid now. The GM decides whether they can be found."
	}
	if helpers > 0 {
		resp["helper"] = helperName
	}
	if plan.Note != "" {
		resp["note"] = plan.Note
	}
	if done {
		resp["message"] = fmt.Sprintf("%s finished crafting %s. It is in the inventory (unsorted).", name, cp.Item)
	} else {
		left := cp.DaysNeeded - cp.Days
		resp["days_remaining"] = int(math.Ceil(float64(left) / float64(1+helpers)))
		resp["message"] = fmt.Sprintf("%s worked on %s: %d of %d days of work done. Send more days whenever there's time; the days needn't be consecutive.", name, cp.Item, cp.Days, cp.DaysNeeded)
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

// trimFloat writes 15 as "15" and 0.5 as "0.5".
func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".")
}

// scrollScribing is the Spell Scroll Costs table (SRD 5.2.1): days and GP by spell level.
var scrollScribing = map[int][2]int{0: {1, 15}, 1: {1, 25}, 2: {3, 100}, 3: {5, 150}, 4: {10, 1000}, 5: {25, 1500}, 6: {40, 10000}, 7: {50, 12500}, 8: {60, 15000}, 9: {120, 50000}}

// scribeScroll is the downtime activity "scribe": days of work on a Spell Scroll of a spell the character has prepared.
func scribeScroll(w http.ResponseWriter, charID int, spellRef string, days int) {
	slug := spellSlugFromName(spellRef)
	if s, ok := spellByNameLookup(spellRef); ok {
		slug = s
	}
	sp, ok := srdSpellsMemory[slug]
	if !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "spell_required", "message": "Send spell: a spell you have prepared (an exact slug from GET /api/universe/spells)."})
		return
	}
	var name, class, toolsRaw string
	var lobby, level, intl, wis, cha int
	if db.QueryRow(`SELECT name, COALESCE(lobby_id,0), COALESCE(class,''), level, intl, wis, cha, COALESCE(tool_proficiencies,'') FROM characters WHERE id = $1`, charID).
		Scan(&name, &lobby, &class, &level, &intl, &wis, &cha, &toolsRaw) != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	if lobby > 0 {
		var fighting bool
		db.QueryRow(`SELECT COALESCE(active, false) FROM combat_state WHERE lobby_id = $1`, lobby).Scan(&fighting)
		if fighting {
			writeJSONStatus(w, http.StatusConflict, map[string]interface{}{"error": "in_combat", "message": "Scribing is downtime: finish the fight first."})
			return
		}
	}
	arcana, _ := characterSkillProficiency(charID, "arcana")
	if !arcana && !game.HasToolProficiency(game.ParseProficiencyList(toolsRaw), "calligrapher's supplies") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "not_proficient", "message": "Scribing a Spell Scroll needs proficiency in Arcana or with Calligrapher's Supplies."})
		return
	}
	if ok, _ := spellAvailableNatively(charID, slug, ""); !ok {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "spell_not_prepared", "message": fmt.Sprintf("You must have %s prepared on each day you work on the scroll.", sp.Name)})
		return
	}
	var invRaw []byte
	db.QueryRow(`SELECT COALESCE(inventory,'[]') FROM characters WHERE id = $1`, charID).Scan(&invRaw)
	var inv []map[string]interface{}
	json.Unmarshal(invRaw, &inv)
	material := ""
	if sp.MaterialCost > 0 {
		why, found := checkCostlyMaterial(usableInventory(inv), sp.Material, sp.MaterialCost)
		if why != "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "material_required", "message": "You must have the spell's Material components at hand: " + why})
			return
		}
		if sp.MaterialConsumed {
			material = found
		}
	}
	// the scribe's numbers go on the scroll
	mod := 0
	switch strings.ToUpper(srdClasses[strings.ToLower(class)].Spellcasting) {
	case "INT":
		mod = game.Modifier(intl)
	case "WIS":
		mod = game.Modifier(wis)
	case "CHA":
		mod = game.Modifier(cha)
	}
	if m, isOrigin := originSpellMod(charID, slug, strings.ToLower(class), intl, wis, cha, mod); isOrigin {
		mod = m
	}
	dc, attack := game.SpellSaveDC(level, mod), game.ProficiencyBonus(level)+mod
	cost := scrollScribing[sp.Level]

	tx, err := db.Begin()
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error"})
		return
	}
	defer tx.Rollback()
	var gold int
	var progressRaw, inv2 []byte
	if tx.QueryRow(`SELECT COALESCE(gold,0), COALESCE(training_progress,'{}'), COALESCE(inventory,'[]') FROM characters WHERE id = $1 FOR UPDATE`, charID).Scan(&gold, &progressRaw, &inv2) != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "database_error"})
		return
	}
	progress := map[string]json.RawMessage{}
	json.Unmarshal(progressRaw, &progress)
	if progress == nil {
		progress = map[string]json.RawMessage{}
	}
	key := "scribe:" + slug
	var cp craftProgress
	started := false
	if raw, ok := progress[key]; ok && json.Unmarshal(raw, &cp) == nil && cp.DaysNeeded > 0 {
		started = true
	}
	if !started {
		if gold < cost[1] {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "insufficient_gold",
				"message": fmt.Sprintf("A Spell Scroll of a level %d spell costs %d GP to scribe; you have %d GP.", sp.Level, cost[1], gold)})
			return
		}
		gold -= cost[1]
		cp = craftProgress{Item: "Spell Scroll (" + sp.Name + ")", Tool: "scribing", DaysNeeded: cost[0], Materials: cost[1]}
	}
	cp.Days += days
	done := cp.Days >= cp.DaysNeeded
	var items []map[string]interface{}
	json.Unmarshal(inv2, &items)
	if done {
		delete(progress, key)
		scroll := map[string]interface{}{"id": newItemID("s"), "name": cp.Item, "quantity": 1, "type": "scroll", "magic_item": "Spell Scroll",
			"spell": slug, "spell_level": sp.Level, "save_dc": dc, "attack_bonus": attack, "scribed_by": name}
		if sp.Level == 0 {
			scroll["caster_level"] = level // a scribed cantrip works as if cast at the scribe's level
		}
		items = append(items, scroll)
	} else {
		v, _ := json.Marshal(cp)
		progress[key] = v
	}
	pOut, _ := json.Marshal(progress)
	iOut, _ := json.Marshal(items)
	if _, err := tx.Exec(`UPDATE characters SET gold = $1, training_progress = $2, inventory = $3 WHERE id = $4`, gold, pOut, iOut, charID); err != nil || tx.Commit() != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
		return
	}
	if done && material != "" {
		consumeSpellMaterial(charID, material)
	}
	if lobby > 0 {
		db.Exec(`INSERT INTO actions (lobby_id, character_id, action_type, description, result) VALUES ($1, $2, 'downtime', $3, $4)`,
			lobby, charID, fmt.Sprintf("%s scribes a Spell Scroll of %s for %d day(s)", name, sp.Name, days), fmt.Sprintf("%d of %d days", min(cp.Days, cp.DaysNeeded), cp.DaysNeeded))
	}
	resp := map[string]interface{}{"success": true, "activity": "scribe", "item": cp.Item, "days_worked": days, "progress_days": min(cp.Days, cp.DaysNeeded),
		"days_needed": cp.DaysNeeded, "complete": done, "gold": gold}
	if !started {
		resp["cost_gp"] = cost[1]
	}
	if done {
		resp["message"] = fmt.Sprintf("%s finished the %s: it uses save DC %d and attack bonus +%d.", name, cp.Item, dc, attack)
		if material != "" {
			resp["material_used"] = material
		}
	} else {
		resp["message"] = fmt.Sprintf("%s worked on the %s: %d of %d days. Keep the spell prepared and send more days when there's time.", name, cp.Item, cp.Days, cp.DaysNeeded)
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

// spellByNameLookup finds a spell's slug by its name ("Cure Wounds") or slug.
func spellByNameLookup(ref string) (string, bool) {
	loadItemUses()
	if slug, ok := spellByName[strings.ToLower(normQuotes(strings.TrimSpace(ref)))]; ok {
		return slug, true
	}
	return "", false
}
