package main

// Protections for every request (the security review of 2026-11-07):
//   - the server's timeouts, so slow or half-sent requests can't hold connections open forever (slowloris);
//   - a cap on the request body (2 MB; a portrait upload 9 MB), so a huge body can't exhaust memory;
//   - headers that stop MIME sniffing, framing by other sites and full-address Referers;
//   - per-address limits on sign-ups and on wrong passwords;
//   - publicError: a database error is logged and reported as "database_error" (its text names tables, columns and types).

import (
	"log"
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r)
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

// publicError is what a client may see of an error: database errors are logged and replaced.
func publicError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "pq: ") || strings.HasPrefix(msg, "sql: ") || strings.Contains(msg, "SQLSTATE") {
		log.Printf("database error: %v", err)
		if strings.Contains(msg, "no rows in result set") {
			return "not_found"
		}
		return "database_error"
	}
	return msg
}
