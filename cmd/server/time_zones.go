package main

// Time zones.
//   Storage: the server's database session uses UTC (withUTC adds timezone=UTC to DATABASE_URL), so NOW() in a "timestamp without time
//     zone" column stores UTC whatever the database's own time zone is. Before this, a database running on another time zone stored its
//     local time, and pages that read the value as UTC showed it hours off.
//   Display: the server's time zone is AGENTRPG_TIMEZONE (an IANA name such as America/New_York; Pacific time when it isn't set).
//     A GM can choose a campaign's own time zone with the time_zone table rule; its pages and log show times in that zone.

import (
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var timeZoneChoices = []struct{ Name, Label string }{
	{"America/New_York", "Eastern (US)"}, {"America/Chicago", "Central (US)"}, {"America/Denver", "Mountain (US)"},
	{"America/Phoenix", "Arizona (US)"}, {"America/Los_Angeles", "Pacific (US)"}, {"America/Anchorage", "Alaska (US)"},
	{"Pacific/Honolulu", "Hawaii (US)"}, {"UTC", "UTC"}, {"Europe/London", "UK"}, {"Europe/Berlin", "Central Europe"},
	{"Asia/Tokyo", "Japan"}, {"Australia/Sydney", "Sydney"},
}

var (
	serverLocOnce sync.Once
	serverLoc     *time.Location
)

// serverLocation is the server's display time zone: AGENTRPG_TIMEZONE, or Pacific time.
func serverLocation() *time.Location {
	serverLocOnce.Do(func() {
		name := strings.TrimSpace(os.Getenv("AGENTRPG_TIMEZONE"))
		if name == "" {
			name = "America/Los_Angeles"
		}
		loc, err := time.LoadLocation(name)
		if err != nil {
			loc, _ = time.LoadLocation("America/Los_Angeles")
		}
		if loc == nil {
			loc = time.UTC
		}
		serverLoc = loc
	})
	return serverLoc
}

// campaignLocation is the campaign's display time zone: its time_zone table rule, or the server's.
func campaignLocation(lobbyID int) *time.Location {
	if v := tableRule(lobbyID, "time_zone"); v != "" && v != "server" {
		if loc, err := time.LoadLocation(v); err == nil {
			return loc
		}
	}
	return serverLocation()
}

// feedTime formats a stored (UTC) time for a campaign's pages: "Jan 2, 15:04 EST".
func feedTime(t time.Time, lobbyID int) string {
	return t.In(campaignLocation(lobbyID)).Format("Jan 2, 15:04 MST")
}

// timeZoneTableRule is the time_zone table rule: the server's zone, or one of timeZoneChoices.
func timeZoneTableRule() tableRuleDef {
	d := tableRuleDef{Key: "time_zone", Title: "Time zone", Default: "server", Options: []tableRuleOption{
		{"server", "The server's time zone", "Times on the campaign page and log use the server's time zone (AGENTRPG_TIMEZONE; Pacific time unless it is set)."},
	}}
	for _, z := range timeZoneChoices {
		d.Options = append(d.Options, tableRuleOption{z.Name, z.Label, "Times on the campaign page and log are shown in " + z.Label + " time (" + z.Name + ")."})
	}
	return d
}

// withUTC adds timezone=UTC to a Postgres URL (lib/pq sends it as a session setting) unless the URL sets a time zone already.
func withUTC(dbURL string) string {
	low := strings.ToLower(dbURL)
	if strings.Contains(low, "timezone=") {
		return dbURL
	}
	if strings.HasPrefix(low, "postgres://") || strings.HasPrefix(low, "postgresql://") {
		u, err := url.Parse(dbURL)
		if err != nil {
			return dbURL
		}
		q := u.Query()
		q.Set("timezone", "UTC")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dbURL + " timezone=UTC" // key=value form
}
