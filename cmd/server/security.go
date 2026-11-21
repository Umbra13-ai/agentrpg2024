package main

// Protections for every request (the security review of 2026-11-07):
//   - the server's timeouts, so slow or half-sent requests can't hold connections open forever (slowloris);
//   - a cap on the request body (2 MB; a portrait upload 9 MB), so a huge body can't exhaust memory;
//   - headers that stop MIME sniffing, framing by other sites and full-address Referers;
//   - per-address limits on sign-ups and on wrong passwords;
//   - guardDB: no request answers success, or any reply but the one database_error shape, when a database call failed under it;
//     a database error's text (it names tables, columns, types and the database's address) goes to the log only
//     (db_guard_2024.go, db_errors_2024.go).

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxRequestBody = 2 << 20 // every request
	maxUploadBody  = 9 << 20 // a portrait upload (the handler itself allows 8 MB)
)

// newServer is the HTTP server with timeouts and the protections around every route.
func newServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           withProtections(http.DefaultServeMux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

// withProtections caps the request body and sets the security headers.
func withProtections(next http.Handler) http.Handler {
	next = roundGate(next) // a game action in a round outside a fight is saved as a submission (round_effects_2024.go)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = overrideMethod(r) // a POST that stands for DELETE, PATCH or PUT (method_override_2024.go)
		limit := int64(maxRequestBody)
		if strings.HasSuffix(r.URL.Path, "/portrait") {
			limit = maxUploadBody
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
		if r.ContentLength > limit {
			h.Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			w.Write([]byte(`{"error":"request_too_large","message":"The request body is too large."}`))
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		r, finishCampaignScope := withCampaignRequestScope(r)
		defer finishCampaignScope()
		guardDB(w, withAuthMemo(r), next) // signed in once per request (auth_and_logging.go); no success over a database failure (db_guard_2024.go)
		if deathTriggersPending.Load() {
			drainDeathTriggers() // a monster that died in this request with a trait that acts on its death (monster_death_trigger_2024.go)
		}
	})
}

// windowLimiter allows a number of events per key in a sliding time window (in memory).
type windowLimiter struct {
	sync.Mutex
	hits map[string][]time.Time
	max  int
	per  time.Duration
}

func newWindowLimiter(max int, per time.Duration) *windowLimiter {
	return &windowLimiter{hits: map[string][]time.Time{}, max: max, per: per}
}

// allow records an event for the key and reports whether it is within the limit.
func (l *windowLimiter) allow(key string) bool {
	l.Lock()
	defer l.Unlock()
	cutoff := time.Now().Add(-l.per)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, time.Now())
	if len(l.hits) > 10000 { // forget addresses with nothing recent, so the map can't grow without bound
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// registerLimiter: new accounts per address per hour.
var registerLimiter = newWindowLimiter(20, time.Hour)
