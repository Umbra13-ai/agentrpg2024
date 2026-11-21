package main

// Whether a player has looked at its campaign since its check-in (playtest, Kestrel's log, Nov 16; feedback log entry 34): a player
// seated in more than one campaign checked one of them on a schedule and missed round 1 of the other. The server can't see an
// agent's scheduler, only its requests, so it shows each campaign's check-in minute, when the player last checked that campaign
// and, while the player still has to send its turn, whether a check-in has passed since then.
//
//   - lastCheckedIn is the one answer to "when did this player last look at this campaign": its requests tied to the campaign
//     (the request log), its character's records in the action log (polls and actions) and its party-chat messages. The heartbeat
//     isn't tied to a campaign, so it doesn't count. campaignReadiness's idle hours and checkInWatch read it.
//   - checkInWatch is the heartbeat's your_check_in for a campaign with the turn schedule.
//
// The server's own lines in the party chat (feedback log entry 35: telling the players that play has opened) are written by
// postServerMessage, the only writer of a message with no agent.

import (
	"database/sql"
	"fmt"
	"time"
)

// serverMessageName: who a line the server writes in the party chat is from.
const serverMessageName = "Agent RPG server"

// postServerMessage: a line from the server in a campaign's party chat (agent_id NULL; every reader of the chat takes that).
func postServerMessage(q execer, lobby int, text string) error {
	_, err := q.Exec(`INSERT INTO campaign_messages (lobby_id, agent_id, agent_name, message, created_at) VALUES ($1, NULL, $2, $3, NOW())`,
		lobby, serverMessageName, text)
	return err
}

// lastCheckedIn: when the player last looked at this campaign, in real time (false: never).
func lastCheckedIn(lobby, agentID, charID int) (time.Time, bool, error) {
	var last time.Time
	seen := func(t sql.NullTime) {
		if t.Valid && t.Time.After(last) {
			last = t.Time
		}
	}
	var req, chat sql.NullTime
	if err := db.QueryRow(`SELECT MAX(created_at) FROM api_logs WHERE agent_id = $1 AND lobby_id = $2`, agentID, lobby).Scan(&req); err != nil {
		return last, false, err
	}
	seen(req)
	if err := db.QueryRow(`SELECT MAX(created_at) FROM campaign_messages WHERE lobby_id = $1 AND agent_id = $2`, lobby, agentID).Scan(&chat); err != nil {
		return last, false, err
	}
	seen(chat)
	if charID > 0 {
		recs, err := readActions(actionFilter{character: charID, withPolls: true, newestFirst: true, limit: 1}) // a look at the game counts
		if err != nil {
			return last, false, err
		}
		if len(recs) > 0 {
			seen(sql.NullTime{Time: recs[0].At, Valid: true})
		}
	}
	return last.UTC(), !last.IsZero(), nil
}

// checkInWatch: the player's check-in in a campaign with the turn schedule: its minute, when it last checked the campaign, and
// whether a check-in has passed since then while its turn is still to send (nil without the schedule or a seat time).
func checkInWatch(lobby, agentID, charID int) (map[string]interface{}, error) {
	in, now, err := loadCheckIn(lobby)
	if err != nil || in.State != scheduleOn || !in.Timed {
		return nil, err
	}
	minute, seated := -1, false
	for _, h := range in.Seats {
		if h.CharacterID == charID {
			if m, ok := in.Sched.seatMinute(h.Seat); ok {
				minute, seated = m, true
			}
		}
	}
	if !seated {
		return nil, nil
	}
	clock, err := clockOf(db, lobby)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{"campaign_id": lobby, "check_in": fmt.Sprintf("%s past every hour (UTC)", minuteText(minute)), "missed": false}
	lastReal, checked, err := lastCheckedIn(lobby, agentID, charID)
	if err != nil {
		return nil, err
	}
	var last time.Time
	if checked {
		last = clock.at(lastReal)
		out["last_checked"] = last
	}
	if in.Round == nil {
		return out, nil
	}
	// the last check-in that has passed in the open round, and whether the player still has a turn to send
	due := nextAt(now, minute)
	if due.After(truncMinute(now)) {
		due = due.Add(-time.Hour)
	}
	if due.Before(truncMinute(in.Round.OpenedAt)) {
		return out, nil
	}
	open := false
	for _, c := range checkInAt(in, now).Characters {
		if c.CharacterID == charID {
			open = roundTurnOpen(c.Status)
		}
	}
	if open && (!checked || last.Before(due)) {
		since := "never checked it"
		if checked {
			since = "last checked it at " + clockText(last)
		}
		out["missed"] = true
		out["message"] = fmt.Sprintf("Round %d is open and your check-in for it passed at %s: you %s. Your turn is still to send: GET /api/my-turn?campaign_id=%d.",
			in.Round.Number, clockText(due), since, lobby)
	}
	return out, nil
}

// checkInWatchOrNil: checkInWatch as a reply field (a database failure in it is answered by guardDB).
func checkInWatchOrNil(lobby, agentID, charID int) interface{} {
	if v, err := checkInWatch(lobby, agentID, charID); err == nil && v != nil {
		return v
	}
	return nil
}
