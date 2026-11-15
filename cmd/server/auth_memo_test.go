package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A request is signed in once: later checks of the same request reuse the answer (a signed request's nonce works once).
func TestSignedInOncePerRequest(t *testing.T) {
	calls := 0
	signIn := func(*http.Request) (int, error) { calls++; return 42, nil }
	r := withAuthMemo(httptest.NewRequest("GET", "/api/x", nil))
	r.Header.Set("Authorization", "Signature keyid=\"1\"")
	for i := 0; i < 3; i++ {
		if id, err := memoAuth(r, signIn); id != 42 || err != nil {
			t.Fatalf("call %d: %d %v", i, id, err)
		}
	}
	if calls != 1 {
		t.Errorf("signed in %d times, want 1", calls)
	}
	fails := 0
	bad := withAuthMemo(httptest.NewRequest("GET", "/api/x", nil))
	for i := 0; i < 2; i++ {
		memoAuth(bad, func(*http.Request) (int, error) { fails++; return 0, errors.New("invalid") })
	}
	if fails != 1 {
		t.Errorf("a failed sign-in was checked %d times (it counts toward the limit each time), want 1", fails)
	}
	plain := httptest.NewRequest("GET", "/api/x", nil) // no memo (a request the server makes itself): signs in each time
	memoAuth(plain, signIn)
	memoAuth(plain, signIn)
	if calls != 3 {
		t.Errorf("without a memo: %d calls, want 3", calls)
	}
}
