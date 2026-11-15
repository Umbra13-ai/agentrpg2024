package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type sentMail struct {
	path, auth string
	body       map[string]interface{}
}

// mailStandIn records every request it gets, as AgentMail or Resend would receive them.
func mailStandIn(t *testing.T) (*httptest.Server, *[]sentMail) {
	var got []sentMail
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = append(got, sentMail{r.URL.EscapedPath(), r.Header.Get("Authorization"), b})
		w.Write([]byte(`{"message_id":"m1"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func clearMailEnv(t *testing.T) {
	for _, k := range []string{"AGENTMAIL_API_KEY", "AGENTMAIL_INBOX", "AGENTMAIL_BASE_URL", "RESEND_API_KEY", "RESEND_BASE_URL", "EMAIL_FROM"} {
		t.Setenv(k, "")
	}
}

// Every mail the server sends (verification, recovery, turn reminder) goes through sendMail, whichever service is set up.
func TestMailEveryPathEveryService(t *testing.T) {
	paths := map[string]func() error{
		"verification": func() error { return sendVerificationEmail("player@example.com", "123456") },
		"recovery": func() error {
			return sendMail("player@example.com", "Agent RPG 2024 account recovery", recoveryMail("123456"))
		},
		"nudge": func() error { return sendNudgeEmail("player@example.com", "Calla", "Tyranny", "Your turn.") },
	}
	for name, send := range paths {
		t.Run("agentmail/"+name, func(t *testing.T) {
			clearMailEnv(t)
			srv, got := mailStandIn(t)
			t.Setenv("AGENTMAIL_API_KEY", "am_test")
			t.Setenv("AGENTMAIL_INBOX", "agentrpg2024@agentmail.to")
			t.Setenv("AGENTMAIL_BASE_URL", srv.URL)
			t.Setenv("RESEND_API_KEY", "re_test") // AgentMail comes first when both are set
			if !emailConfigured() {
				t.Fatal("emailConfigured false with AgentMail set")
			}
			if err := send(); err != nil {
				t.Fatal(err)
			}
			if len(*got) != 1 {
				t.Fatalf("%d requests, want 1", len(*got))
			}
			m := (*got)[0]
			if m.path != "/v0/inboxes/agentrpg2024@agentmail.to/messages/send" || m.auth != "Bearer am_test" {
				t.Fatalf("sent to %s with %q", m.path, m.auth)
			}
			if to, _ := m.body["to"].([]interface{}); len(to) != 1 || to[0] != "player@example.com" || m.body["subject"] == "" || m.body["text"] == "" {
				t.Fatalf("body %v", m.body)
			}
			if _, has := m.body["from"]; has {
				t.Fatal("AgentMail sends from the inbox; no from field")
			}
		})
		t.Run("resend/"+name, func(t *testing.T) {
			clearMailEnv(t)
			srv, got := mailStandIn(t)
			t.Setenv("RESEND_API_KEY", "re_test")
			t.Setenv("RESEND_BASE_URL", srv.URL)
			t.Setenv("EMAIL_FROM", "Agent RPG 2024 <noreply@example.org>")
			if err := send(); err != nil {
				t.Fatal(err)
			}
			if len(*got) != 1 || (*got)[0].path != "/emails" || (*got)[0].auth != "Bearer re_test" || (*got)[0].body["from"] != "Agent RPG 2024 <noreply@example.org>" {
				t.Fatalf("got %+v", *got)
			}
		})
		t.Run("none/"+name, func(t *testing.T) {
			clearMailEnv(t)
			t.Setenv("AGENTMAIL_API_KEY", "am_test") // a key without an inbox is not a mail service
			if emailConfigured() {
				t.Fatal("emailConfigured true with no service")
			}
			err := send()
			if name == "recovery" && err != errMailNotConfigured {
				t.Fatalf("recovery: %v, want errMailNotConfigured", err)
			}
			if name != "recovery" && err != nil {
				t.Fatalf("%s: %v, want nil (nothing to send)", name, err)
			}
		})
	}
}

func TestMailServiceErrorIsReported(t *testing.T) {
	clearMailEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"name":"ForbiddenError"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("AGENTMAIL_API_KEY", "am_test")
	t.Setenv("AGENTMAIL_INBOX", "agentrpg2024@agentmail.to")
	t.Setenv("AGENTMAIL_BASE_URL", srv.URL)
	if err := sendMail("a@example.com", "s", "b"); err == nil || !strings.Contains(err.Error(), "email_send_failed") {
		t.Fatalf("err %v", err)
	}
}
