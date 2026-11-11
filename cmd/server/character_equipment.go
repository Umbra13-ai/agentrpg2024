package main

// Equipment by location: worn, held, and inside bags (backpacks, pouches...).
// Every inventory item may carry:
//   id        stable per-character identifier, assigned on first use ("i1", "i2"...)
//   location  "" (not yet sorted) | "worn" | "held" | <id of a container item>
// A container is an inventory item with "type":"container". Containers may
// sit inside other containers (a pouch inside a backpack). A container with
// "lost":true is gone, and so is everything inside it, at any depth. Nothing
// is deleted when a bag is lost, so the loss can be undone if the bag is found.
//   GET  /api/characters/{id}/equipment            public layout
//   POST /api/characters/{id}/equipment/containers add a bag
//   POST /api/characters/{id}/equipment/move       move an item (or part of a stack)
//   POST /api/characters/{id}/equipment/lose       a bag and its contents are lost
//   POST /api/characters/{id}/equipment/recover    a lost bag is found again
// Writes are allowed for the character's player or the GM of its campaign.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// containerKinds holds capacities from the SRD 5.2.1 adventuring gear entries.
var containerKinds = map[string]struct {
	Name     string
	Capacity string
	Lbs      float64
}{
	"backpack":    {"Backpack", "30 pounds, 1 cubic foot", 30},
	"sack":        {"Sack", "30 pounds, 1 cubic foot", 30},
	"basket":      {"Basket", "40 pounds, 2 cubic feet", 40},
	"pouch":       {"Pouch", "6 pounds, one-fifth of a cubic foot", 6},
	"chest":       {"Chest", "12 cubic feet", 0},
	"bucket":      {"Bucket", "half a cubic foot", 0},
	"quiver":      {"Quiver", "20 Arrows", 0},
	"bolt-case":   {"Crossbow Bolt Case", "20 Bolts", 0},
	"scroll-case": {"Map or Scroll Case", "10 sheets of paper", 0},
}

type invItem = map[string]interface{}

func itemStr(m invItem, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func itemNum(m invItem, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

func itemQty(m invItem) int {
	if q := int(itemNum(m, "quantity")); q > 0 {
		return q
	}
	return 1
}

func itemBool(m invItem, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func isContainer(m invItem) bool {
	return itemStr(m, "type") == "container" || magicContainerName(itemStr(m, "name")) // + magic containers (F159)
}

// ensureItemIDs gives every item an id and returns true if anything changed.
func ensureItemIDs(inv []invItem) bool {
	max := 0
	used := map[string]bool{}
	for _, it := range inv {
		id := itemStr(it, "id")
		used[id] = true
		if strings.HasPrefix(id, "i") {
			if n, err := strconv.Atoi(id[1:]); err == nil && n > max {
				max = n
			}
		}
	}
	changed := false
	for _, it := range inv {
		if itemStr(it, "id") == "" {
			max++
			it["id"] = fmt.Sprintf("i%d", max)
			changed = true
		}
	}
	return changed
}

func nextItemID(inv []invItem) string {
	max := 0
	for _, it := range inv {
		id := itemStr(it, "id")
		if strings.HasPrefix(id, "i") {
			if n, err := strconv.Atoi(id[1:]); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("i%d", max+1)
}

func indexByID(inv []invItem) map[string]invItem {
	idx := map[string]invItem{}
	for _, it := range inv {
		idx[itemStr(it, "id")] = it
	}
	return idx
}

// isLost reports whether the item, or any bag it is inside, is lost.
func isLost(it invItem, byID map[string]invItem) bool {
	for depth := 0; depth < 10; depth++ {
		if itemBool(it, "lost") {
			return true
		}
		parent, ok := byID[itemStr(it, "location")]
		if !ok {
			return false
		}
		it = parent
	}
	return false
}

// lostChecker returns a function that says whether an inventory item is lost: the
// item, or any bag it is inside, is flagged lost. Gameplay uses it to skip those
// items; the items themselves stay in the data so the GM can decide what happens.
func lostChecker(inv []invItem) func(invItem) bool {
	byID := indexByID(inv)
	return func(it invItem) bool { return isLost(it, byID) }
}

// usableInventory returns the items that are not lost. The item maps are shared
// with the original slice, so a change to one is a change to the stored list;
// do not write this slice back, write the full inventory.
func usableInventory(inv []invItem) []invItem {
	isLostItem := lostChecker(inv)
	out := make([]invItem, 0, len(inv))
	for _, it := range inv {
		if !isLostItem(it) && !itemBool(it, "destroyed") { // a magic item destroyed by its last charge can't be used
			out = append(out, it)
		}
	}
	return out
}

// usableInventoryAny is usableInventory for an inventory decoded as []interface{}.
func usableInventoryAny(inv []interface{}) []interface{} {
	maps := make([]invItem, 0, len(inv))
	for _, v := range inv {
		if m, ok := v.(map[string]interface{}); ok {
			maps = append(maps, m)
		}
	}
	isLostItem := lostChecker(maps)
	out := make([]interface{}, 0, len(inv))
	for _, v := range inv {
		if m, ok := v.(map[string]interface{}); ok && isLostItem(m) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// allMatchingLost reports whether the inventory has at least one item with this
// name and every such item is lost (so the named thing is no longer available).
func allMatchingLost(inv []invItem, name string) bool {
	key := itemKey(name)
	if key == "" {
		return false
	}
	isLostItem := lostChecker(inv)
	found := false
	for _, it := range inv {
		if itemKey(itemStr(it, "name")) != key {
			continue
		}
		if !isLostItem(it) {
			return false
		}
		found = true
	}
	return found
}

// unequipLostGear clears equipped armor, shield and weapons that are inside a
// lost bag, so a character cannot keep using gear that is gone.
func unequipLostGear(charID int) {
	var raw []byte
	var armor, mainHand, offHand sql.NullString
	var shield bool
	if err := db.QueryRow(`SELECT COALESCE(inventory, '[]'), equipped_armor, equipped_main_hand, equipped_off_hand, COALESCE(equipped_shield, false)
		FROM characters WHERE id = $1`, charID).Scan(&raw, &armor, &mainHand, &offHand, &shield); err != nil {
		return
	}
	var inv []invItem
	json.Unmarshal(raw, &inv)
	ensureItemIDs(inv)
	if armor.Valid && allMatchingLost(inv, armor.String) {
		db.Exec(`UPDATE characters SET equipped_armor = NULL WHERE id = $1`, charID)
	}
	if mainHand.Valid && allMatchingLost(inv, mainHand.String) {
		db.Exec(`UPDATE characters SET equipped_main_hand = NULL WHERE id = $1`, charID)
	}
	if offHand.Valid && allMatchingLost(inv, offHand.String) {
		db.Exec(`UPDATE characters SET equipped_off_hand = NULL WHERE id = $1`, charID)
	}
	if shield && allMatchingLost(inv, "shield") {
		db.Exec(`UPDATE characters SET equipped_shield = false WHERE id = $1`, charID)
	}
}

// insideOrSame reports whether candidate is `ancestor` itself or inside it.
func insideOrSame(candidate, ancestor invItem, byID map[string]invItem) bool {
	for depth := 0; depth < 10; depth++ {
		if itemStr(candidate, "id") == itemStr(ancestor, "id") {
			return true
		}
		parent, ok := byID[itemStr(candidate, "location")]
		if !ok {
			return false
		}
		candidate = parent
	}
	return true
}

func normalizeRef(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(s)), " "))
}

// findItem resolves an id or name to one inventory item.
func findItem(inv []invItem, ref string) (invItem, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("item_required")
	}
	for _, it := range inv {
		if itemStr(it, "id") == ref {
			return it, nil
		}
	}
	var matches []invItem
	for _, it := range inv {
		if normalizeRef(itemStr(it, "name")) == normalizeRef(ref) {
			matches = append(matches, it)
		}
	}
	if len(matches) == 0 { // "Horn of Valhalla" for "Horn of Valhalla (Iron)": the name without its parenthetical (F189)
		for _, it := range inv {
			n := itemStr(it, "name")
			if i := strings.Index(n, " ("); i > 0 && normalizeRef(n[:i]) == normalizeRef(ref) {
				matches = append(matches, it)
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		ids := []string{}
		for _, m := range matches {
			ids = append(ids, itemStr(m, "id"))
		}
		return nil, fmt.Errorf("ambiguous_item: use one of these ids: %s", strings.Join(ids, ", "))
	}
	return nil, fmt.Errorf("item_not_found")
}

// ---- layout (what the public page shows) ----

func describeItem(it invItem) map[string]interface{} {
	d := map[string]interface{}{
		"id": itemStr(it, "id"), "name": itemStr(it, "name"), "quantity": itemQty(it),
	}
	if w := itemNum(it, "weight"); w > 0 {
		d["weight_lbs"] = w
	}
	if r := itemStr(it, "lost_reason"); r != "" {
		d["lost_reason"] = r
	}
	if at := itemStr(it, "lost_at"); at != "" {
		d["lost_at"] = at
	}
	// magic items (magic_item_use_2024.go): charges, what the item casts, a Spell Scroll's numbers, a destroyed item
	for _, k := range []string{"charges", "charges_max", "casts", "scroll", "destroyed", "magic_item"} {
		if v, ok := it[k]; ok {
			d[k] = v
		}
	}
	return d
}

// carriedWeight sums the known weights of a container's contents, all depths.
func carriedWeight(container invItem, inv []invItem, byID map[string]invItem) float64 {
	total := 0.0
	for _, it := range inv {
		if itemStr(it, "location") == itemStr(container, "id") {
			total += itemNum(it, "weight") * float64(itemQty(it))
			if isContainer(it) {
				total += carriedWeight(it, inv, byID)
			}
		}
	}
	return total
}

func describeContainer(c invItem, inv []invItem, byID map[string]invItem) map[string]interface{} {
	d := describeItem(c)
	d["kind"] = itemStr(c, "container_kind")
	d["capacity"] = itemStr(c, "capacity")
	if lbs := itemNum(c, "capacity_lbs"); lbs > 0 {
		d["capacity_lbs"] = lbs
		d["carried_lbs"] = carriedWeight(c, inv, byID)
	}
	contents := []map[string]interface{}{}
	for _, it := range inv {
		if itemStr(it, "location") != itemStr(c, "id") {
			continue
		}
		if isContainer(it) {
			contents = append(contents, describeContainer(it, inv, byID))
		} else {
			contents = append(contents, describeItem(it))
		}
	}
	d["contents"] = contents
	return d
}

// buildEquipmentLayout groups an inventory by where each thing is kept.
// equipped* come from the character's existing equipment fields.
func buildEquipmentLayout(inv []invItem, equippedArmor string, shield bool, mainHand, offHand string, includeLost bool) map[string]interface{} {
	byID := indexByID(inv)
	worn := []map[string]interface{}{}
	held := []map[string]interface{}{}
	bags := []map[string]interface{}{}
	unsorted := []map[string]interface{}{}
	lost := []map[string]interface{}{}

	equippedNames := map[string]bool{}
	addEquipped := func(list *[]map[string]interface{}, name, label string) {
		if name == "" || allMatchingLost(inv, name) {
			return
		}
		equippedNames[normalizeRef(name)] = true
		entry := map[string]interface{}{"name": titleWords(name), "quantity": 1, "note": label}
		if w, ok := catalogWeightFor(name); ok {
			entry["weight_lbs"] = w
		}
		*list = append(*list, entry)
	}
	addEquipped(&worn, equippedArmor, "armor")
	if shield {
		shieldEntry := map[string]interface{}{"name": "Shield", "quantity": 1, "note": "shield"}
		if w, ok := catalogWeightFor("shield"); ok {
			shieldEntry["weight_lbs"] = w
		}
		worn = append(worn, shieldEntry)
	}
	addEquipped(&held, mainHand, "main hand")
	addEquipped(&held, offHand, "off hand")

	for _, it := range inv {
		parent := byID[itemStr(it, "location")]
		inBag := parent != nil && isContainer(parent)
		if isLost(it, byID) {
			// Only list the outermost lost thing; its contents ride along inside it.
			if inBag && isLost(parent, byID) {
				continue
			}
			if isContainer(it) {
				lost = append(lost, describeContainer(it, inv, byID))
			} else {
				lost = append(lost, describeItem(it))
			}
			continue
		}
		if inBag {
			continue // shown inside its bag
		}
		loc := itemStr(it, "location")
		if isContainer(it) {
			c := describeContainer(it, inv, byID)
			c["carried"] = map[string]string{"worn": "Worn", "held": "Held"}[loc]
			bags = append(bags, c)
			continue
		}
		switch loc {
		case "worn":
			worn = append(worn, describeItem(it))
		case "held":
			held = append(held, describeItem(it))
		default:
			if equippedNames[normalizeRef(itemStr(it, "name"))] {
				continue // already shown as equipped
			}
			unsorted = append(unsorted, describeItem(it))
		}
	}
	// An equipped armor or weapon that is also an inventory item is shown once.
	dropSynthetic := func(list []map[string]interface{}) []map[string]interface{} {
		have := map[string]bool{}
		for _, e := range list {
			if _, hasID := e["id"]; hasID {
				have[itemKey(e["name"].(string))] = true
			}
		}
		out := make([]map[string]interface{}, 0, len(list))
		for _, e := range list {
			if _, hasID := e["id"]; !hasID && have[itemKey(e["name"].(string))] {
				continue
			}
			out = append(out, e)
		}
		return out
	}
	worn = dropSynthetic(worn)
	held = dropSynthetic(held)
	if !includeLost {
		// Lost bags are not part of the character sheet. Only the GM sees them.
		lost = []map[string]interface{}{}
	}
	return map[string]interface{}{
		"worn": worn, "held": held, "containers": bags, "unsorted": unsorted, "lost": lost,
	}
}

// ---- storage ----

func loadInventoryTx(tx *sql.Tx, charID int) ([]invItem, error) {
	var raw []byte
	if err := tx.QueryRow(`SELECT COALESCE(inventory, '[]') FROM characters WHERE id = $1 FOR UPDATE`, charID).Scan(&raw); err != nil {
		return nil, err
	}
	var inv []invItem
	if err := json.Unmarshal(raw, &inv); err != nil {
		inv = []invItem{}
	}
	if inv == nil {
		inv = []invItem{}
	}
	ensureItemIDs(inv)
	fillItemWeights(inv)
	return inv, nil
}

func saveInventoryTx(tx *sql.Tx, charID int, inv []invItem) error {
	b, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE characters SET inventory = $1 WHERE id = $2`, string(b), charID)
	return err
}

// requireOwnerOrGM allows the character's player or the GM of its campaign.
func requireOwnerOrGM(w http.ResponseWriter, r *http.Request, charID int) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return false
	}
	var owner int
	var dm sql.NullInt64
	if err := db.QueryRow(`
		SELECT c.agent_id, l.dm_id FROM characters c LEFT JOIN lobbies l ON c.lobby_id = l.id WHERE c.id = $1`, charID).
		Scan(&owner, &dm); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return false
	}
	if agentID != owner && !(dm.Valid && int(dm.Int64) == agentID) {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character_or_campaign"})
		return false
	}
	return true
}

// requireCharacterGM lets only the GM of the character's campaign through (and writes the refusal).
func requireCharacterGM(w http.ResponseWriter, r *http.Request, charID int) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return false
	}
	if !isCharacterGM(agentID, charID) {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "gm_only", "message": "Only the GM of this character's campaign can do that."})
		return false
	}
	return true
}

// isCharacterGM reports whether the agent is the GM of the character's campaign.
func isCharacterGM(agentID, charID int) bool {
	var dm sql.NullInt64
	if err := db.QueryRow(`SELECT l.dm_id FROM characters c LEFT JOIN lobbies l ON c.lobby_id = l.id WHERE c.id = $1`, charID).Scan(&dm); err != nil {
		return false
	}
	return dm.Valid && int(dm.Int64) == agentID
}

// requireGMForLoss lets only the GM of the character's campaign lose or recover
// a bag, because what happens to a lost bag is the GM's decision. A character
// that is not in a campaign has no GM, so its player may do it.
func requireGMForLoss(w http.ResponseWriter, r *http.Request, charID int) bool {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return false
	}
	var owner int
	var dm sql.NullInt64
	if err := db.QueryRow(`SELECT c.agent_id, l.dm_id FROM characters c LEFT JOIN lobbies l ON c.lobby_id = l.id WHERE c.id = $1`, charID).Scan(&owner, &dm); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return false
	}
	if dm.Valid {
		if int(dm.Int64) != agentID {
			writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "only_the_gm_can_lose_or_recover_a_bag",
				"hint": "Ask your GM. A lost bag is kept, with everything in it, until the GM decides."})
			return false
		}
		return true
	}
	if owner != agentID {
		writeJSONStatus(w, http.StatusForbidden, map[string]interface{}{"error": "not_your_character"})
		return false
	}
	return true
}

// mutateInventory runs fn on the locked inventory and saves it if fn succeeds.
func mutateInventory(w http.ResponseWriter, charID int, fn func(inv []invItem) ([]invItem, map[string]interface{}, int, string)) {
	tx, err := db.Begin()
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "db_error"})
		return
	}
	defer tx.Rollback()
	inv, err := loadInventoryTx(tx, charID)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
		return
	}
	out, resp, status, errCode := fn(inv)
	if errCode != "" {
		resp["error"] = errCode
		writeJSONStatus(w, status, resp)
		return
	}
	if err := saveInventoryTx(tx, charID, out); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": "save_failed"})
		return
	}
	resp["success"] = true
	writeJSONStatus(w, http.StatusOK, resp)
}

func fail(code string, status int, extra map[string]interface{}) ([]invItem, map[string]interface{}, int, string) {
	if extra == nil {
		extra = map[string]interface{}{}
	}
	return nil, extra, status, code
}

// validDestination checks `to` and returns the normalised location string.
func validDestination(to string, inv []invItem, moving invItem) (string, error) {
	to = strings.TrimSpace(to)
	switch strings.ToLower(to) {
	case "worn", "held":
		return strings.ToLower(to), nil
	case "unsorted", "":
		return "", nil
	}
	byID := indexByID(inv)
	dest, ok := byID[to]
	if !ok {
		// allow a container's name too
		d, err := findItem(inv, to)
		if err != nil {
			return "", fmt.Errorf("destination_not_found")
		}
		dest = d
	}
	if !isContainer(dest) {
		return "", fmt.Errorf("destination_is_not_a_container")
	}
	if itemBool(dest, "lost") || isLost(dest, byID) {
		return "", fmt.Errorf("destination_is_lost")
	}
	if isContainer(moving) && insideOrSame(dest, moving, byID) {
		return "", fmt.Errorf("a_bag_cannot_go_inside_itself")
	}
	return itemStr(dest, "id"), nil
}

// handleCharacterEquipment routes /api/characters/{id}/equipment[/action].
// handleCharacterEquipment godoc
// @Summary Equipment: what is worn, held and in which bag; bags, moving, dropping and picking up items
// @Description GET lists every item with its id and location (worn, held, a bag) and the weight in each bag and in total. POST /equipment/containers adds a bag or other container ({"kind": "backpack"} or {"name": ..., "capacity_lbs": 30, "location": ...}). POST /equipment/move moves an item ({"item": "<id or name>", "to": "<bag id, worn or held>", "quantity": N}; worn: one pair of footwear, gloves or gauntlets, bracers, one item of headwear and one cloak at a time (a second is refused with already_wearing; rings and necklaces have no limit; table rule worn_items); the character sheet's worn_items shows each place; held weapons go through equip-weapon and its hands rule). POST /equipment/drop puts an item on the campaign's ground ({"item": ..., "quantity": N}); POST /equipment/pick-up takes it back ({"dropped_id": N}). POST /equipment/lose and /equipment/recover (GM only) mark a bag lost or found ({"container": ..., "to": ..., "reason": ...}). Owner only, except lose and recover.
// @Tags Characters
// @Param id path int true "Character ID"
// @Param request body object{item=string,to=string,quantity=int,kind=string,name=string,capacity_lbs=number,location=string,dropped_id=int,container=string,reason=string} false "The fields of the action"
// @Router /characters/{id}/equipment [get]
// @Router /characters/{id}/equipment/containers [post]
// @Router /characters/{id}/equipment/move [post]
// @Router /characters/{id}/equipment/drop [post]
// @Router /characters/{id}/equipment/pick-up [post]
// @Router /characters/{id}/equipment/lose [post]
// @Router /characters/{id}/equipment/recover [post]
func handleCharacterEquipment(w http.ResponseWriter, r *http.Request, charID int, rest []string) {
	action := ""
	if len(rest) > 0 {
		action = rest[0]
	}

	if action == "" {
		if r.Method != http.MethodGet {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
			return
		}
		includeLost := false
		if agentID, err := getAgentFromAuth(r); err == nil && isCharacterGM(agentID, charID) {
			includeLost = true
		}
		layout, ok := loadEquipmentLayout(charID, includeLost)
		if !ok {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found"})
			return
		}
		// the player (and the GM) can see what was dropped and not yet picked up
		if agentID, err := getAgentFromAuth(r); err == nil {
			var owner int
			db.QueryRow(`SELECT agent_id FROM characters WHERE id = $1`, charID).Scan(&owner)
			if owner == agentID || isCharacterGM(agentID, charID) {
				layout["dropped_by_you"] = droppedByCharacter(charID)
			}
		}
		writeJSONStatus(w, http.StatusOK, layout)
		return
	}

	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if action == "lose" || action == "recover" {
		if !requireGMForLoss(w, r, charID) {
			return
		}
	} else if !requireOwnerOrGM(w, r, charID) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	switch action {
	case "use-charge": // a charged property that isn't a spell (magic_item_use_2024.go)
		handleItemUseCharge(w, r, charID)
		return
	case "charges": // the GM sets an item's charges
		if !requireCharacterGM(w, r, charID) {
			return
		}
		handleItemSetCharges(w, r, charID)
		return
	case "containers":
		var req struct {
			Name        string  `json:"name"`
			Kind        string  `json:"kind"`
			Capacity    string  `json:"capacity"`
			CapacityLbs float64 `json:"capacity_lbs"`
			Location    string  `json:"location"`
			Weight      float64 `json:"weight"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
		kind := strings.ToLower(strings.TrimSpace(req.Kind))
		known, isKnown := containerKinds[kind]
		name := strings.TrimSpace(req.Name)
		if name == "" && isKnown {
			name = known.Name
		}
		if name == "" || len(name) > 80 {
			kinds := []string{}
			for k := range containerKinds {
				kinds = append(kinds, k)
			}
			sort.Strings(kinds)
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "name_or_kind_required", "kinds": kinds})
			return
		}
		mutateInventory(w, charID, func(inv []invItem) ([]invItem, map[string]interface{}, int, string) {
			item := invItem{"id": nextItemID(inv), "name": name, "type": "container", "quantity": 1}
			if kind != "" {
				item["container_kind"] = kind
			}
			capText, capLbs := req.Capacity, req.CapacityLbs
			if isKnown {
				if capText == "" {
					capText = known.Capacity
				}
				if capLbs == 0 {
					capLbs = known.Lbs
				}
			}
			if capText != "" {
				item["capacity"] = capText
			}
			if capLbs > 0 {
				item["capacity_lbs"] = capLbs
			}
			if req.Weight > 0 {
				item["weight"] = req.Weight
			}
			loc, err := validDestination(req.Location, inv, item)
			if err != nil {
				return fail(err.Error(), http.StatusBadRequest, nil)
			}
			item["location"] = loc
			inv = append(inv, item)
			return inv, map[string]interface{}{"container_id": item["id"], "name": name, "location": loc}, 0, ""
		})

	case "move":
		var req struct {
			Item     string `json:"item"`
			To       string `json:"to"`
			Quantity int    `json:"quantity"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
		mutateInventory(w, charID, func(inv []invItem) ([]invItem, map[string]interface{}, int, string) {
			it, err := findItem(inv, req.Item)
			if err != nil {
				code := err.Error()
				status := http.StatusNotFound
				if strings.HasPrefix(code, "ambiguous_item") {
					status = http.StatusConflict
				}
				return fail(code, status, nil)
			}
			if isLost(it, indexByID(inv)) {
				return fail("item_is_lost", http.StatusConflict, nil)
			}
			loc, err := validDestination(req.To, inv, it)
			if err != nil {
				return fail(err.Error(), http.StatusBadRequest, nil)
			}
			if loc == "held" && !isContainer(it) {
				// a character has two hands: weapons are held by equipping them, anything else counts against the same two hands
				if isWeaponItem(it) { // a named magic weapon too (F174)
					return fail("use_equip_weapon", http.StatusConflict, map[string]interface{}{
						"message": "Weapons are held by equipping them: POST /api/characters/equip-weapon {\"character_id\": N, \"weapon\": \"" + itemStr(it, "name") + "\", \"slot\": \"main_hand\" or \"off_hand\"}. That checks your two hands, proficiency and the once-per-turn weapon change in combat."})
				}
				if used := handsInUse(charID, inv, it); used >= 2 {
					return fail("hands_full", http.StatusConflict, map[string]interface{}{
						"message": "Both of your hands are full. Put something away or drop it first.", "hands_in_use": used})
				}
			}
			if loc == "worn" {
				if why := wornConflict(charID, inv, it); why != "" { // one cloak, one pair of boots ... (F213, worn_items_2024.go)
					return fail("already_wearing", http.StatusConflict, map[string]interface{}{"message": why})
				}
			}
			qty := itemQty(it)
			if req.Quantity > 0 && req.Quantity < qty && !isContainer(it) {
				// split the stack
				it["quantity"] = qty - req.Quantity
				moved := invItem{}
				for k, v := range it {
					moved[k] = v
				}
				moved["id"] = nextItemID(inv)
				moved["quantity"] = req.Quantity
				moved["location"] = loc
				inv = append(inv, moved)
				return inv, map[string]interface{}{"moved": describeItem(moved), "to": loc}, 0, ""
			}
			if req.Quantity > qty {
				return fail("not_enough_in_stack", http.StatusBadRequest, map[string]interface{}{"have": qty})
			}
			it["location"] = loc
			return inv, map[string]interface{}{"moved": describeItem(it), "to": loc}, 0, ""
		})

	case "drop":
		handleCharacterDrop(w, r, charID)

	case "pick-up":
		handleCharacterPickUp(w, r, charID)

	case "lose", "recover":
		if action == "lose" {
			defer unequipLostGear(charID)
		}
		var req struct {
			Container string `json:"container"`
			To        string `json:"to"`
			Reason    string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
			return
		}
		mutateInventory(w, charID, func(inv []invItem) ([]invItem, map[string]interface{}, int, string) {
			c, err := findItem(inv, req.Container)
			if err != nil {
				return fail(err.Error(), http.StatusNotFound, nil)
			}
			if !isContainer(c) {
				return fail("not_a_container", http.StatusBadRequest, nil)
			}
			byID := indexByID(inv)
			if action == "lose" {
				if itemBool(c, "lost") {
					return fail("already_lost", http.StatusConflict, nil)
				}
				c["lost"] = true
				c["lost_at"] = time.Now().UTC().Format(time.RFC3339)
				if r := strings.TrimSpace(req.Reason); r != "" {
					c["lost_reason"] = r
				}
				return inv, map[string]interface{}{
					"lost": describeContainer(c, inv, byID),
					"note": "The bag and everything inside it are flagged lost. Nothing is deleted: it is hidden from the character sheet and unusable until the GM recovers it.",
				}, 0, ""
			}
			if !itemBool(c, "lost") {
				return fail("not_lost", http.StatusConflict, nil)
			}
			delete(c, "lost")
			loc, err := validDestination(req.To, inv, c)
			if err != nil {
				c["lost"] = true
				return fail(err.Error(), http.StatusBadRequest, nil)
			}
			delete(c, "lost_at")
			delete(c, "lost_reason")
			c["location"] = loc
			return inv, map[string]interface{}{"recovered": describeContainer(c, inv, byID), "to": loc}, 0, ""
		})

	default:
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "unknown_equipment_action",
			"actions": []string{"containers", "move", "lose", "recover"}})
	}
}

// loadEquipmentLayout reads a character's inventory and returns its layout.
func loadEquipmentLayout(charID int, includeLost bool) (map[string]interface{}, bool) {
	var raw []byte
	var armor, mainHand, offHand sql.NullString
	var shield bool
	if err := db.QueryRow(`
		SELECT COALESCE(inventory, '[]'), equipped_armor, COALESCE(equipped_shield, false), equipped_main_hand, equipped_off_hand
		FROM characters WHERE id = $1`, charID).Scan(&raw, &armor, &shield, &mainHand, &offHand); err != nil {
		return nil, false
	}
	var inv []invItem
	json.Unmarshal(raw, &inv)
	if inv == nil {
		inv = []invItem{}
	}
	ensureItemIDs(inv) // ids shown here match what the owner will get on first write
	fillItemWeights(inv)
	annotateCharges(inv) // magic items: charges (full until first used), what they cast; Spell Scrolls: spell, DC, attack bonus
	return buildEquipmentLayout(inv, armor.String, shield, mainHand.String, offHand.String, includeLost), true
}

// handsInUse counts the hands that are busy: equipped weapons (a two-handed one takes both), a shield,
// and other items held in the hand. The item being moved is not counted.
func handsInUse(charID int, inv []invItem, moving invItem) int {
	main, off := equippedWeapons(charID)
	hands := 0
	if main != "" {
		hands++
		if isTwoHandedWeapon(main) {
			hands++
		}
	}
	if off != "" {
		hands++
	}
	if shieldInHand(charID) {
		hands++
	}
	for _, it := range inv {
		if itemStr(it, "id") == itemStr(moving, "id") || isContainer(it) || itemStr(it, "location") != "held" {
			continue
		}
		if isWeaponItem(it) { // a named magic weapon too: "Dagger of Venom" was counted twice (F174)
			continue // counted above through the equipped weapons
		}
		hands++
	}
	return hands
}

// isWeaponItem: an inventory item that is a weapon: an SRD weapon by name ("Longsword +2") or a named magic weapon ("Dagger of Venom",
// "Sword of Wounding (Shortsword)").
func isWeaponItem(it invItem) bool {
	if _, _, isWeapon := findWeapon(itemStr(it, "name")); isWeapon {
		return true
	}
	return namedWeaponFor(itemStr(it, "name")) != nil
}
