package main

// Who may act on a campaign or a character that a route names in its path: one owner.
//
// Routes such as /api/campaigns/{id}/combat/start or /api/characters/{id}/equip ask one question before they act: may this
// account do this here? The answer is one of four, written once:
//   - requireCampaignGM: the GM of the campaign (it must exist);
//   - requireCampaignMember: the GM, or a player with a character in the campaign;
//   - requireCharacterOwner: the player who owns the character;
//   - requireOwnerOrGM: the owner, or the GM of the campaign the character is in.
//
// A refusal has one shape ({"error", "message"}) and one set of codes: campaign_not_found and character_not_found (404),
// not_gm, not_in_campaign and not_your_character (403). The message names the campaign or character and what was asked.
// Whether the campaign's status allows the action (not started, ended) is the route's own rule; GM commands under /api/gm/*
// pick and check their campaign in gm_campaign_2024.go.

import (
	"database/sql"
	"fmt"
	"net/http"
)

func writeAccessProblem(w http.ResponseWriter, status int, code, msg string) {
	writeJSONStatus(w, status, map[string]interface{}{"error": code, "message": msg})
}

// campaignLookup: what the account is in the campaign: "gm", "player" (it has a character in it) or "" (neither). found is
// false when there is no such campaign; err is a database failure (db_errors_2024.go), which is not "no such campaign".
func campaignLookup(agentID, campaignID int) (role, name string, found bool, err error) {
	var dm sql.NullInt64
	if err = db.QueryRow(`SELECT dm_id, COALESCE(name,'') FROM lobbies WHERE id = $1`, campaignID).Scan(&dm, &name); err != nil {
		if dbFailed(err) {
			return "", "", false, err
		}
		return "", "", false, nil
	}
	if dm.Valid && int(dm.Int64) == agentID {
		return "gm", name, true, nil
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM characters WHERE lobby_id = $1 AND agent_id = $2`, campaignID, agentID).Scan(&n); err != nil {
		return "", name, true, err
	}
	if n > 0 {
		return "player", name, true, nil
	}
	return "", name, true, nil
}

// campaignRole is campaignLookup for a caller that only shows more to the GM (a failure shows the least).
func campaignRole(agentID, campaignID int) (role, name string, found bool) {
	role, name, found, _ = campaignLookup(agentID, campaignID)
	return
}

// requireCampaign: the campaign exists (anyone may read what the route shows); otherwise the 404, or the database failure, is
// written.
func requireCampaign(w http.ResponseWriter, campaignID int) (name string, ok bool) {
	_, name, found, err := campaignLookup(0, campaignID)
	switch {
	case err != nil:
		writeDBError(w, err, fmt.Sprintf("campaign %d", campaignID))
		return "", false
	case !found:
		writeAccessProblem(w, http.StatusNotFound, "campaign_not_found", fmt.Sprintf("There is no campaign %d.", campaignID))
		return "", false
	}
	return name, true
}

func campaignLabel(id int, name string) string {
	if name == "" {
		return fmt.Sprintf("campaign %d", id)
	}
	return fmt.Sprintf("campaign %d (%s)", id, name)
}

// signedIn: the account making the request, or the 401 written.
func signedIn(w http.ResponseWriter, r *http.Request) (int, bool) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return 0, false
	}
	return agentID, true
}

// requireCampaignGM: the signed-in account is the campaign's GM; otherwise the refusal is written and ok is false. what says
// what was asked, in words that follow "can" ("start a fight", "change the table rules").
func requireCampaignGM(w http.ResponseWriter, r *http.Request, campaignID int, what string) (agentID int, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, false
	}
	role, name, found, err := campaignLookup(agentID, campaignID)
	switch {
	case err != nil:
		writeDBError(w, err, fmt.Sprintf("campaign %d", campaignID))
		return 0, false
	case !found:
		writeAccessProblem(w, http.StatusNotFound, "campaign_not_found", fmt.Sprintf("There is no campaign %d.", campaignID))
		return 0, false
	case role != "gm":
		writeAccessProblem(w, http.StatusForbidden, "not_gm", fmt.Sprintf("Only the GM of %s can %s.", campaignLabel(campaignID, name), what))
		return 0, false
	}
	return agentID, gateCampaignRequest(w, r, campaignID, nil)
}

// requireCampaignMember: the signed-in account is the campaign's GM or has a character in it; role is "gm" or "player".
func requireCampaignMember(w http.ResponseWriter, r *http.Request, campaignID int, what string) (agentID int, role string, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, "", false
	}
	role, name, found, err := campaignLookup(agentID, campaignID)
	switch {
	case err != nil:
		writeDBError(w, err, fmt.Sprintf("campaign %d", campaignID))
		return 0, "", false
	case !found:
		writeAccessProblem(w, http.StatusNotFound, "campaign_not_found", fmt.Sprintf("There is no campaign %d.", campaignID))
		return 0, "", false
	case role == "":
		writeAccessProblem(w, http.StatusForbidden, "not_in_campaign", fmt.Sprintf("Only the GM and the players of %s can %s.", campaignLabel(campaignID, name), what))
		return 0, "", false
	}
	return agentID, role, gateCampaignRequest(w, r, campaignID, nil)
}

// characterLookup: the character's owner, name and campaign (0 when in none); found is false when there is no character; err
// is a database failure, which is not "no such character".
func characterLookup(charID int) (owner int, name string, campaign int, found bool, err error) {
	var lobby, own sql.NullInt64
	if err = db.QueryRow(`SELECT agent_id, COALESCE(name,''), lobby_id FROM characters WHERE id = $1`, charID).Scan(&own, &name, &lobby); err != nil {
		if dbFailed(err) {
			return 0, "", 0, false, err
		}
		return 0, "", 0, false, nil
	}
	return int(own.Int64), name, int(lobby.Int64), true, nil
}

// characterAccess is characterLookup for a caller that only decides what to show (a failure shows the least).
func characterAccess(charID int) (owner int, name string, campaign int, found bool) {
	owner, name, campaign, found, _ = characterLookup(charID)
	return
}

// characterFound: the character exists, or its 404 (or the database failure) is written.
func characterFound(w http.ResponseWriter, charID int) (owner int, name string, campaign int, ok bool) {
	owner, name, campaign, found, err := characterLookup(charID)
	switch {
	case err != nil:
		writeDBError(w, err, fmt.Sprintf("character %d", charID))
		return 0, "", 0, false
	case !found:
		noCharacter(w, charID)
		return 0, "", 0, false
	}
	return owner, name, campaign, true
}

// isCharacterGM: the account is the GM of the campaign the character is in.
func isCharacterGM(agentID, charID int) bool {
	_, _, camp, found := characterAccess(charID)
	if !found || camp == 0 {
		return false
	}
	role, _, _ := campaignRole(agentID, camp)
	return role == "gm"
}

// gmOfCampaign: the account is the GM of campaign camp (false for 0, no campaign); err is a database failure.
func gmOfCampaign(agentID, camp int) (bool, error) {
	if camp == 0 {
		return false, nil
	}
	role, _, _, err := campaignLookup(agentID, camp)
	return role == "gm", err
}

func noCharacter(w http.ResponseWriter, charID int) {
	writeAccessProblem(w, http.StatusNotFound, "character_not_found", fmt.Sprintf("There is no character %d.", charID))
}

// requireCharacterOwner: the signed-in account owns the character.
func requireCharacterOwner(w http.ResponseWriter, r *http.Request, charID int, what string) (agentID int, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, false
	}
	owner, name, camp, found := characterFound(w, charID)
	switch {
	case !found:
		return 0, false
	case owner != agentID:
		writeAccessProblem(w, http.StatusForbidden, "not_your_character", fmt.Sprintf("%s isn't yours: only its player can %s.", name, what))
		return 0, false
	}
	return agentID, gateCampaignRequest(w, r, camp, nil)
}

// requireOwnerOrGM: the signed-in account owns the character or is the GM of the campaign it is in.
func requireOwnerOrGM(w http.ResponseWriter, r *http.Request, charID int, what string) (agentID int, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, false
	}
	owner, name, camp, found := characterFound(w, charID)
	if !found {
		return 0, false
	}
	if owner == agentID {
		return agentID, gateCampaignRequest(w, r, camp, nil)
	}
	gm, err := gmOfCampaign(agentID, camp)
	if err != nil {
		writeDBError(w, err, fmt.Sprintf("the campaign of character %d", charID))
		return 0, false
	}
	if gm {
		return agentID, gateCampaignRequest(w, r, camp, nil)
	}
	writeAccessProblem(w, http.StatusForbidden, "not_your_character", fmt.Sprintf("%s isn't yours: only its player or its campaign's GM can %s.", name, what))
	return 0, false
}

// requireCharacterGM: the signed-in account is the GM of the campaign the character is in.
func requireCharacterGM(w http.ResponseWriter, r *http.Request, charID int, what string) (agentID int, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, false
	}
	_, name, camp, found := characterFound(w, charID)
	if !found {
		return 0, false
	}
	gm, err := gmOfCampaign(agentID, camp)
	if err != nil {
		writeDBError(w, err, fmt.Sprintf("the campaign of character %d", charID))
		return 0, false
	}
	if !gm {
		where := "its campaign"
		if camp == 0 {
			where = "a campaign (it is in none)"
		}
		writeAccessProblem(w, http.StatusForbidden, "not_gm", fmt.Sprintf("Only the GM of %s's campaign can %s; %s isn't run by you, or %s.", name, what, name, where))
		return 0, false
	}
	return agentID, gateCampaignRequest(w, r, camp, nil)
}

// namedCharactersIn: every character a player's request names in its body (a positive id; a negative id is a monster, which is
// only ever found in the campaign's own fight) must be in the campaign the request acts in. Any other id is answered 404
// character_not_found, the same reply as an id that doesn't exist, so a request can't act on or probe another campaign's
// characters. It returns false when it has answered. (GM commands get the same rule from gmReadAs and characterIDFields.)
func namedCharactersIn(w http.ResponseWriter, lobbyID int, ids []int) bool {
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		var in bool
		err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM characters WHERE id = $1 AND lobby_id = $2 AND deleted_at IS NULL)`, id, lobbyID).Scan(&in)
		if answerDBFailure(w, err, fmt.Sprintf("character %d", id)) {
			return false
		}
		if !in {
			writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{"error": "character_not_found",
				"message": fmt.Sprintf("Character %d is not in your campaign. Nothing was done.", id)})
			return false
		}
	}
	return true
}
