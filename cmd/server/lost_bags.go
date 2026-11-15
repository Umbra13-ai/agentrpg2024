package main

// Lost bags are flagged, never deleted. A lost bag, and everything inside it,
// is hidden from character sheets and cannot be used. The GM sees them here and
// decides what happens: recover the bag, or play out what became of it.
//	GET /api/campaigns/{id}/lost-bags    GM only: every lost bag in the campaign
//	POST /api/characters/{id}/equipment/lose     GM only: {"container": "...", "reason": "..."}
//	POST /api/characters/{id}/equipment/recover  GM only: {"container": "...", "to": "worn"}

import (
	"encoding/json"
	"net/http"
)

// handleCampaignLostBags godoc
// @Summary The lost bags of a campaign (GM)
// @Description GM only: the bags marked lost in the campaign, with their contents. A lost bag is hidden from the sheets and can't be used; the GM marks bags lost or found with POST /characters/{id}/equipment/lose and /recover.
// @Tags Campaigns
// @Param id path int true "Campaign ID"
// @Router /campaigns/{id}/lost-bags [get]
func handleCampaignLostBags(w http.ResponseWriter, r *http.Request, campaignID int) {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	if _, ok := requireCampaignGM(w, r, campaignID, "see its lost bags"); !ok {
		return
	}

	rows, err := db.Query(`SELECT id, name, COALESCE(inventory, '[]') FROM characters WHERE lobby_id = $1 ORDER BY id`, campaignID)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]interface{}{"error": dbErrorCode})
		return
	}
	defer rows.Close()

	out := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var name string
		var raw []byte
		if rows.Scan(&id, &name, &raw) != nil {
			continue
		}
		var inv []invItem
		json.Unmarshal(raw, &inv)
		ensureItemIDs(inv)
		lost := lostBags(inv)
		if len(lost) == 0 {
			continue
		}
		out = append(out, map[string]interface{}{"character_id": id, "character": name, "lost_bags": lost})
	}
	writeJSONStatus(w, http.StatusOK, map[string]interface{}{
		"campaign_id": campaignID,
		"characters":  out,
		"note":        "Lost bags are kept with everything inside. Recover one with POST /api/characters/{id}/equipment/recover.",
	})
}

// lostBags returns the outermost lost bags in an inventory, with contents.
func lostBags(inv []invItem) []map[string]interface{} {
	byID := indexByID(inv)
	out := []map[string]interface{}{}
	for _, it := range inv {
		if !isContainer(it) || !itemBool(it, "lost") {
			continue
		}
		if parent, ok := byID[itemStr(it, "location")]; ok && isLost(parent, byID) {
			continue // listed inside its lost parent
		}
		out = append(out, describeContainer(it, inv, byID))
	}
	return out
}
