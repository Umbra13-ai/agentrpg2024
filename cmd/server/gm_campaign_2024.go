package main

// Which campaign a GM command is for: one owner.
//
// Every GM command (the /api/gm/* routes, and the GM's reads of the pending-turn queue and the deadlines) finds its campaign
// here, in one of three ways:
//   - the request names it: campaign_id in the body or the query (both and different: id_mismatch);
//   - the command acts on characters (campaignFromTarget): their campaign, which must be the named one if one is named;
//   - neither: the GM's only campaign the command works in, else the only one with a fight going on, else campaign_id is
//     required.
//
// Then it checks the campaign is the GM's and in a status the command works in: active, or also not yet started with
// campaignBeforeStart(). A command never acts on an ended campaign. A problem is answered in one shape (writeGMCampaignProblem):
// not_gm (403), campaign_not_active (403), campaign_id_required (400), character_not_found (404), character_not_in_campaign
// (400), not_in_campaign (400), targets_in_different_campaigns (400).

import (
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/lib/pq"
)

// campaignFromTarget: a GM command that acts on characters finds its campaign from them. Each field is a pointer to the
// request's field that holds a character id (*int) or ids (*[]int); ids of 0 and monsters' (negative) ids are not
// characters and are skipped. The characters must all be in one campaign the GM runs; a campaign_id that is sent must be
// that campaign. With no character named and no campaign_id, gmReadAs returns 0 and the handler says what is missing.
func campaignFromTarget(fields ...interface{}) bodyOpt {
	return func(s *bodySpec) {
		s.byTarget = true
		s.targets = append(s.targets, fields...)
	}
}

// targetsIn: like campaignFromTarget, for a command that may name characters but doesn't have to (a check for the GM's
// monsters, a spell aimed at a monster): with none named, the campaign is picked as for any GM command.
func targetsIn(fields ...interface{}) bodyOpt {
	return func(s *bodySpec) { s.targets = append(s.targets, fields...) }
}

// notGMHint is added to a not_gm refusal: what a player who sent the GM's command can do instead.
func notGMHint(h string) bodyOpt { return func(s *bodySpec) { s.notGM = h } }

// campaignOf: the command acts in a campaign it found from what its path names (a deadline): that campaign must be the GM's,
// and a campaign_id that is sent must be it. what names the thing ("deadline 4") in a refusal.
func campaignOf(id int, what string) bodyOpt {
	return func(s *bodySpec) { s.ofCampaign, s.ofWhat = id, what }
}

// campaignBeforeStart: the command also works in a campaign that has not started (recruiting): setting up shops, the
// table, the party, and the repair tools.
func campaignBeforeStart() bodyOpt { return func(s *bodySpec) { s.beforeStart = true } }

// gmStatuses: the campaign statuses a command works in.
func (s bodySpec) gmStatuses() []string {
	if s.beforeStart {
		return []string{"recruiting", "active"}
	}
	return []string{"active"}
}

func gmSpec(opts []bodyOpt) bodySpec {
	var spec bodySpec
	for _, o := range opts {
		o(&spec)
	}
	return spec
}

// gmCampaignFor picks the campaign a GM command without a body is for: ?campaign_id=N, else the GM's only campaign the
// command works in (see the file comment). Options: campaignBeforeStart().
func gmCampaignFor(agentID int, r *http.Request, opts ...bodyOpt) (int, string, string) {
	want := 0
	if r != nil && r.URL != nil {
		want, _ = strconv.Atoi(r.URL.Query().Get("campaign_id"))
	}
	return gmCampaignPick(agentID, want, gmSpec(opts).gmStatuses()...)
}

// gmCampaignPick picks the campaign: want (0: none named) or the GM's only campaign in the given statuses (default
// active), else the only one with a fight going on. It returns the campaign, or a problem's code and message.
func gmCampaignPick(agentID, want int, statuses ...string) (int, string, string) {
	if len(statuses) == 0 {
		statuses = []string{"active"}
	}
	if want > 0 {
		if code, msg := gmCampaignCheck(agentID, want, statuses); code != "" {
			return 0, code, msg
		}
		return want, "", ""
	}
	rows, err := db.Query(`SELECT id FROM lobbies WHERE dm_id = $1 AND status = ANY($2) ORDER BY id`, agentID, pq.Array(statuses))
	if err != nil {
		log.Printf("database error finding the campaigns of GM %d: %v", agentID, err)
		return 0, dbErrorCode, dbReadMessage("your campaigns")
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			log.Printf("database error finding the campaigns of GM %d: %v", agentID, err)
			return 0, dbErrorCode, dbReadMessage("your campaigns")
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 0, "not_gm", fmt.Sprintf("You are not the GM of any %s campaign.", statusWords(statuses))
	}
	if len(ids) == 1 {
		return ids[0], "", ""
	}
	var fighting []int
	for _, id := range ids {
		var active bool
		err := db.QueryRow(`SELECT COALESCE(active,false) FROM combat_state WHERE lobby_id = $1`, id).Scan(&active)
		if dbFailed(err) {
			log.Printf("database error reading the fight of campaign %d: %v", id, err)
			return 0, dbErrorCode, dbReadMessage(fmt.Sprintf("campaign %d", id))
		}
		if err == nil && active {
			fighting = append(fighting, id)
		}
	}
	if len(fighting) == 1 {
		return fighting[0], "", ""
	}
	return 0, "campaign_id_required", fmt.Sprintf("You run %d %s campaigns (%s): send campaign_id (in the body, or ?campaign_id=N).", len(ids), statusWords(statuses), gmCampaignList(ids))
}

// gmCampaignCheck: is campaign id the GM's, and in one of the statuses the command works in?
func gmCampaignCheck(agentID, id int, statuses []string) (string, string) {
	var dm int
	var status string
	err := db.QueryRow(`SELECT COALESCE(dm_id,0), COALESCE(status,'') FROM lobbies WHERE id = $1`, id).Scan(&dm, &status)
	if dbFailed(err) {
		log.Printf("database error reading campaign %d: %v", id, err)
		return dbErrorCode, dbReadMessage(fmt.Sprintf("campaign %d", id))
	}
	if err != nil || dm != agentID {
		return "not_gm", fmt.Sprintf("You are not the GM of campaign %d.", id)
	}
	for _, s := range statuses {
		if s == status {
			return "", ""
		}
	}
	// F235: the GM's own campaign that isn't started read as "You are not the GM of that campaign"
	msg := fmt.Sprintf("Campaign %d is %s: this command works in a campaign that is %s.", id, status, statusWords(statuses))
	if status == "recruiting" {
		msg += fmt.Sprintf(" Start it with POST /api/campaigns/%d/start.", id)
	}
	return "campaign_not_active", msg
}

// gmTargetCampaign: the one campaign the characters are in, checked as the GM's (want: the campaign the request named, 0
// for none).
func gmTargetCampaign(agentID, want int, chars []int, statuses []string) (int, int, string, string) {
	camp, first := 0, 0
	for _, id := range chars {
		var lobby int
		if err := db.QueryRow(`SELECT COALESCE(lobby_id,0) FROM characters WHERE id = $1`, id).Scan(&lobby); err != nil {
			if dbFailed(err) {
				log.Printf("database error reading character %d: %v", id, err)
				return 0, http.StatusInternalServerError, dbErrorCode, dbReadMessage(fmt.Sprintf("character %d", id))
			}
			return 0, http.StatusNotFound, "character_not_found", fmt.Sprintf("Character %d not found.", id)
		}
		if lobby == 0 {
			return 0, http.StatusBadRequest, "character_not_in_campaign", fmt.Sprintf("Character %d is not in a campaign.", id)
		}
		if camp == 0 {
			camp, first = lobby, id
		} else if lobby != camp {
			return 0, http.StatusBadRequest, "targets_in_different_campaigns", fmt.Sprintf("Character %d is in campaign %d and character %d in campaign %d: one request acts in one campaign.", first, camp, id, lobby)
		}
	}
	return gmCampaignMatch(agentID, want, camp, fmt.Sprintf("Character %d", first), statuses)
}

// gmCampaignMatch: the command acts in campaign camp (where what is): it must be the named one (want, 0 for none), the GM's,
// and in a status the command works in.
func gmCampaignMatch(agentID, want, camp int, what string, statuses []string) (int, int, string, string) {
	if want > 0 && want != camp {
		return 0, http.StatusBadRequest, "not_in_campaign", fmt.Sprintf("%s is in campaign %d, not campaign %d.", what, camp, want)
	}
	if code, msg := gmCampaignCheck(agentID, camp, statuses); code != "" {
		return 0, gmProblemStatus(code), code, msg
	}
	return camp, 0, "", ""
}

// characterIDFields: the request fields that name a character (a positive id; a negative id is a monster, which a command only
// finds in its own campaign's fight). Every character a GM command names must be in the one campaign the command acts in, and
// that campaign must be the GM's: gmReadAs reads these fields from every GM request itself, so a handler that doesn't declare
// its targets (campaignFromTarget, targetsIn) can't act on another campaign's characters. A new field that names a character
// is one entry here.
var characterIDFields = map[string]bool{
	"character_id": true, "character_ids": true, "target_id": true, "target_ids": true, "caster_id": true, "source_id": true,
	"attacker_id": true, "defender_id": true, "initiator_id": true, "grappler_id": true, "combatant_id": true,
	"redirect_target_id": true, "sculpt_targets": true, "paladin_id": true, "bard_id": true, "barbarian_id": true,
	"owner_id": true, "give_to": true,
}

// namedCharacters: the character ids (positive) in a decoded GM request's characterIDFields, found by their JSON names in the
// request struct (embedded structs included).
func namedCharacters(v interface{}) []int {
	var ids []int
	add := func(id int64) {
		if id > 0 && !containsInt(ids, int(id)) {
			ids = append(ids, int(id))
		}
	}
	var walk func(rv reflect.Value)
	walk = func(rv reflect.Value) {
		for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
			if rv.IsNil() {
				return
			}
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Struct {
			return
		}
		t := rv.Type()
		for i := 0; i < t.NumField(); i++ {
			f, fv := t.Field(i), rv.Field(i)
			if f.Anonymous {
				walk(fv)
				continue
			}
			if !characterIDFields[strings.Split(f.Tag.Get("json"), ",")[0]] {
				continue
			}
			for fv.Kind() == reflect.Ptr && !fv.IsNil() {
				fv = fv.Elem()
			}
			switch fv.Kind() {
			case reflect.Int, reflect.Int64, reflect.Int32:
				add(fv.Int())
			case reflect.Slice:
				for j := 0; j < fv.Len(); j++ {
					if e := fv.Index(j); e.Kind() == reflect.Int || e.Kind() == reflect.Int64 || e.Kind() == reflect.Int32 {
						add(e.Int())
					}
				}
			}
		}
	}
	walk(reflect.ValueOf(v))
	return ids
}

// targetIDs: the character ids in the request's target fields.
func (s bodySpec) targetIDs() []int {
	var ids []int
	add := func(id int) {
		if id > 0 && !containsInt(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, f := range s.targets {
		switch p := f.(type) {
		case *int:
			add(*p)
		case *[]int:
			for _, id := range *p {
				add(id)
			}
		}
	}
	return ids
}

// writeGMCampaignProblem answers a GM command whose campaign couldn't be picked.
func writeGMCampaignProblem(w http.ResponseWriter, code, msg string) {
	writeJSONStatus(w, gmProblemStatus(code), map[string]interface{}{"error": code, "message": msg})
}

// gmProblemStatus: the status of a campaign problem: 400 when the request must name the campaign, 500 when the database failed,
// else 403.
func gmProblemStatus(code string) int {
	switch code {
	case "campaign_id_required":
		return http.StatusBadRequest
	case dbErrorCode:
		return http.StatusInternalServerError
	}
	return http.StatusForbidden
}

// gmRead reads a GM command: who sends it, its body (readRequest) and the campaign it is for. campaign_id may be in the body
// or the query, so every GM command takes it and none needs to declare it. On a problem it answers and returns false.
func gmRead(w http.ResponseWriter, r *http.Request, v interface{}, opts ...bodyOpt) (agentID, campaignID int, ok bool) {
	agentID, err := getAgentFromAuth(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		writeAuthError(w, err)
		return 0, 0, false
	}
	campaignID, ok = gmReadAs(w, r, agentID, v, opts...)
	return agentID, campaignID, ok
}

// gmReadAs is gmRead for a handler that has already found the agent. With campaignFromTarget it returns 0 when the request
// names neither a character nor a campaign.
func gmReadAs(w http.ResponseWriter, r *http.Request, agentID int, v interface{}, opts ...bodyOpt) (int, bool) {
	var bodyCampaign int
	if !readRequest(w, r, v, append(opts, alsoField("campaign_id", &bodyCampaign))...) { // the campaign this command is for (or ?campaign_id=); without it, the campaign of the characters it names, else your only active campaign
		return 0, false
	}
	want, _ := strconv.Atoi(r.URL.Query().Get("campaign_id"))
	if want > 0 && bodyCampaign > 0 && want != bodyCampaign {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "id_mismatch",
			"message": fmt.Sprintf("campaign_id is %d in the body and %d in the query: send one. Nothing was changed.", bodyCampaign, want)})
		return 0, false
	}
	if want == 0 {
		want = bodyCampaign
	}
	spec := gmSpec(opts)
	chars := spec.targetIDs()
	for _, id := range namedCharacters(v) { // every character the request names, declared or not (characterIDFields)
		if !containsInt(chars, id) {
			chars = append(chars, id)
		}
	}
	if len(chars) > 0 || spec.ofCampaign > 0 {
		var id, status int
		var code, msg string
		if spec.ofCampaign > 0 {
			id, status, code, msg = gmCampaignMatch(agentID, want, spec.ofCampaign, spec.ofWhat, spec.gmStatuses())
		} else {
			id, status, code, msg = gmTargetCampaign(agentID, want, chars, spec.gmStatuses())
		}
		if code == "not_gm" && spec.notGM != "" {
			msg += " " + spec.notGM
		}
		if code != "" {
			writeJSONStatus(w, status, map[string]interface{}{"error": code, "message": msg + " Nothing was changed."})
			return 0, false
		}
		return id, completeGMRead(w, r, v, id)
	}
	if spec.byTarget && want == 0 {
		return 0, true // no character named: the handler says what is missing
	}
	id, code, msg := gmCampaignPick(agentID, want, spec.gmStatuses()...)
	if code == "not_gm" && spec.notGM != "" {
		msg += " " + spec.notGM
	}
	if code != "" {
		writeGMCampaignProblem(w, code, msg)
		return 0, false
	}
	return id, completeGMRead(w, r, v, id)
}

// gmCampaignList: "4 (The Sunken Keep), 7 (Ashfall)".
func gmCampaignList(ids []int) string {
	var parts []string
	for _, id := range ids {
		var name string
		db.QueryRow(`SELECT COALESCE(name,'') FROM lobbies WHERE id = $1`, id).Scan(&name)
		if name == "" {
			parts = append(parts, strconv.Itoa(id))
		} else {
			parts = append(parts, fmt.Sprintf("%d (%s)", id, name))
		}
	}
	return strings.Join(parts, ", ")
}

func statusWords(statuses []string) string {
	words := map[string]string{"recruiting": "not yet started", "active": "active"}
	var out []string
	for _, s := range statuses {
		if w, ok := words[s]; ok {
			out = append(out, w)
		} else {
			out = append(out, s)
		}
	}
	return strings.Join(out, " or ")
}
