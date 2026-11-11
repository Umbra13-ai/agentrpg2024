package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestNewTokenFormatAndHash(t *testing.T) {
	tok, hash, prefix := newToken()
	if !strings.HasPrefix(tok, tokenPrefix) || len(tok) < 40 {
		t.Fatalf("bad token %q", tok)
	}
	if hash != hashToken(tok) || len(hash) != 64 {
		t.Fatalf("hash mismatch")
	}
	if !strings.HasPrefix(tok, prefix) || strings.Contains(hash, tok) {
		t.Fatalf("bad prefix")
	}
	tok2, _, _ := newToken()
	if tok == tok2 {
		t.Fatalf("tokens must be unique")
	}
}

func TestScopeAllows(t *testing.T) {
	if !scopeAllows(tokenScopeFull, http.MethodPost) {
		t.Error("full scope should allow POST")
	}
	if !scopeAllows(tokenScopeReadOnly, http.MethodGet) || !scopeAllows(tokenScopeReadOnly, http.MethodHead) {
		t.Error("read scope should allow GET and HEAD")
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if scopeAllows(tokenScopeReadOnly, m) {
			t.Errorf("read scope must refuse %s", m)
		}
	}
}

func TestAuthThrottle(t *testing.T) {
	ip := "203.0.113.9"
	if authThrottled(ip) {
		t.Fatal("fresh address should not be throttled")
	}
	for i := 0; i < maxFailedPerMinute; i++ {
		recordAuthFailure(ip)
	}
	if !authThrottled(ip) {
		t.Fatal("address should be throttled after too many failures")
	}
	if authThrottled("203.0.113.10") {
		t.Fatal("other addresses must not be affected")
	}
}
