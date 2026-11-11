package main

import (
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestValidEmail(t *testing.T) {
	for _, ok := range []string{"agent@example.com", "player.two@example.com", " spaced@example.org "} {
		if _, good := validEmail(ok); !good {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "no-at-sign", "Agent <agent@example.com>", "a@b.c, d@e.f", "x@" + string(make([]byte, 260))} {
		if _, good := validEmail(bad); good {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestRecoveryCode(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+-[a-z]+-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := generateRecoveryCode()
		if !re.MatchString(c) {
			t.Fatalf("code %q has the wrong shape", c)
		}
		if seen[c] {
			t.Fatalf("code %q repeated in 200 tries", c)
		}
		seen[c] = true
	}
	if hashCode("ancient-blade-AB23-CD45") != hashCode(" ANCIENT-BLADE-ab23-cd45 ") {
		t.Error("codes compare without case and spaces")
	}
	if hashCode("ancient-blade-AB23-CD45") == hashCode("ancient-blade-AB23-CD46") {
		t.Error("different codes must hash differently")
	}
}

func TestAdminKey(t *testing.T) {
	t.Setenv("ADMIN_KEY", "secret-key")
	r := httptest.NewRequest("POST", "/api/admin/issue-token", nil)
	if adminKeyOK(r) {
		t.Error("no header: refused")
	}
	r.Header.Set("X-Admin-Key", "secret-kez")
	if adminKeyOK(r) {
		t.Error("wrong key: refused")
	}
	r.Header.Set("X-Admin-Key", "secret-key")
	if !adminKeyOK(r) {
		t.Error("right key: accepted")
	}
	t.Setenv("ADMIN_KEY", "")
	r.Header.Set("X-Admin-Key", "")
	if adminKeyOK(r) {
		t.Error("no ADMIN_KEY on the server: always refused")
	}
}
