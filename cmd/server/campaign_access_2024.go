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

// campaignRole: what the account is in the campaign: "gm", "player" (it has a character in it) or "" (neither). found is
// false when there is no such campaign.
func campaignRole(agentID, campaignID int) (role, name string, found bool) {
	var dm sql.NullInt64
	if db.QueryRow(`SELECT dm_id, COALESCE(name,'') FROM lobbies WHERE id = $1`, campaignID).Scan(&dm, &name) != nil {
		return "", "", false
	}
	if dm.Valid && int(dm.Int64) == agentID {
		return "gm", name, true
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM characters WHERE lobby_id = $1 AND agent_id = $2`, campaignID, agentID).Scan(&n)
	if n > 0 {
		return "player", name, true
	}
	return "", name, true
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
	role, name, found := campaignRole(agentID, campaignID)
	switch {
	case !found:
		writeAccessProblem(w, http.StatusNotFound, "campaign_not_found", fmt.Sprintf("There is no campaign %d.", campaignID))
		return 0, false
	case role != "gm":
		writeAccessProblem(w, http.StatusForbidden, "not_gm", fmt.Sprintf("Only the GM of %s can %s.", campaignLabel(campaignID, name), what))
		return 0, false
	}
	return agentID, true
}

// requireCampaignMember: the signed-in account is the campaign's GM or has a character in it; role is "gm" or "player".
func requireCampaignMember(w http.ResponseWriter, r *http.Request, campaignID int, what string) (agentID int, role string, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, "", false
	}
	role, name, found := campaignRole(agentID, campaignID)
	switch {
	case !found:
		writeAccessProblem(w, http.StatusNotFound, "campaign_not_found", fmt.Sprintf("There is no campaign %d.", campaignID))
		return 0, "", false
	case role == "":
		writeAccessProblem(w, http.StatusForbidden, "not_in_campaign", fmt.Sprintf("Only the GM and the players of %s can %s.", campaignLabel(campaignID, name), what))
		return 0, "", false
	}
	return agentID, role, true
}

// characterAccess: the character's owner, name and campaign (0 when in none); found is false when there is no character.
func characterAccess(charID int) (owner int, name string, campaign int, found bool) {
	var lobby sql.NullInt64
	if db.QueryRow(`SELECT agent_id, name, lobby_id FROM characters WHERE id = $1`, charID).Scan(&owner, &name, &lobby) != nil {
		return 0, "", 0, false
	}
	return owner, name, int(lobby.Int64), true
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

func noCharacter(w http.ResponseWriter, charID int) {
	writeAccessProblem(w, http.StatusNotFound, "character_not_found", fmt.Sprintf("There is no character %d.", charID))
}

// requireCharacterOwner: the signed-in account owns the character.
func requireCharacterOwner(w http.ResponseWriter, r *http.Request, charID int, what string) (agentID int, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, false
	}
	owner, name, _, found := characterAccess(charID)
	switch {
	case !found:
		noCharacter(w, charID)
		return 0, false
	case owner != agentID:
		writeAccessProblem(w, http.StatusForbidden, "not_your_character", fmt.Sprintf("%s isn't yours: only its player can %s.", name, what))
		return 0, false
	}
	return agentID, true
}

// requireOwnerOrGM: the signed-in account owns the character or is the GM of the campaign it is in.
func requireOwnerOrGM(w http.ResponseWriter, r *http.Request, charID int, what string) (agentID int, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, false
	}
	owner, name, _, found := characterAccess(charID)
	if !found {
		noCharacter(w, charID)
		return 0, false
	}
	if owner == agentID || isCharacterGM(agentID, charID) {
		return agentID, true
	}
	writeAccessProblem(w, http.StatusForbidden, "not_your_character", fmt.Sprintf("%s isn't yours: only its player or its campaign's GM can %s.", name, what))
	return 0, false
}

// requireCharacterGM: the signed-in account is the GM of the campaign the character is in.
func requireCharacterGM(w http.ResponseWriter, r *http.Request, charID int, what string) (agentID int, ok bool) {
	if agentID, ok = signedIn(w, r); !ok {
		return 0, false
	}
	_, name, camp, found := characterAccess(charID)
	if !found {
		noCharacter(w, charID)
		return 0, false
	}
	if !isCharacterGM(agentID, charID) {
		where := "its campaign"
		if camp == 0 {
			where = "a campaign (it is in none)"
		}
		writeAccessProblem(w, http.StatusForbidden, "not_gm", fmt.Sprintf("Only the GM of %s's campaign can %s; %s isn't run by you, or %s.", name, what, name, where))
		return 0, false
	}
	return agentID, true
}
