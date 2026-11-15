package main

// The server's outgoing mail: one sender for every mail (email verification, account recovery, turn reminders).
//
// Two services can carry it; the first one configured is used:
//   - AgentMail (https://agentmail.to): AGENTMAIL_API_KEY and AGENTMAIL_INBOX (the inbox the mail is sent from, e.g.
//     agentrpg2024@agentmail.to). The sender is that inbox. AGENTMAIL_BASE_URL is optional (default https://api.agentmail.to).
//   - Resend (https://resend.com): RESEND_API_KEY, with EMAIL_FROM as the sender. RESEND_BASE_URL is optional.
//
// With neither, no mail goes out: emailConfigured answers false, the requests that would send mail say so, and a lost
// account is recovered by the operator (POST /api/admin/issue-token).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// errMailNotConfigured: no mail service is set up on this server.
var errMailNotConfigured = errors.New("email_not_configured")

type mailService struct {
	name string
	// request builds the HTTP request that sends one plain text mail.
	request func(to, subject, body string) (*http.Request, error)
}

// mailServiceInUse returns the configured mail service, or nil when there is none.
func mailServiceInUse() *mailService {
	if key, inbox := env("AGENTMAIL_API_KEY"), env("AGENTMAIL_INBOX"); key != "" && inbox != "" {
		return &mailService{name: "AgentMail", request: func(to, subject, body string) (*http.Request, error) {
			return jsonPost(envOr("AGENTMAIL_BASE_URL", "https://api.agentmail.to")+"/v0/inboxes/"+url.PathEscape(inbox)+"/messages/send",
				key, map[string]interface{}{"to": []string{to}, "subject": subject, "text": body})
		}}
	}
	if key := env("RESEND_API_KEY"); key != "" {
		return &mailService{name: "Resend", request: func(to, subject, body string) (*http.Request, error) {
			return jsonPost(resendBaseURL()+"/emails", key,
				map[string]interface{}{"from": emailFrom(), "to": []string{to}, "subject": subject, "text": body})
		}}
	}
	return nil
}

func emailConfigured() bool { return mailServiceInUse() != nil }

// sendMail sends a plain text mail through the configured service. It answers errMailNotConfigured when there is none.
func sendMail(to, subject, body string) error {
	svc := mailServiceInUse()
	if svc == nil {
		log.Println("no mail service configured (AGENTMAIL_API_KEY with AGENTMAIL_INBOX, or RESEND_API_KEY): mail not sent")
		return errMailNotConfigured
	}
	req, err := svc.request(to, subject, body)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		log.Printf("%s: mail to %s failed: %v", svc.name, to, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("%s returned %d: %s", svc.name, resp.StatusCode, string(b))
		return fmt.Errorf("email_send_failed")
	}
	return nil
}

func jsonPost(u, bearer string, payload interface{}) (*http.Request, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", u, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func env(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func envOr(name, fallback string) string {
	if v := strings.TrimRight(env(name), "/"); v != "" {
		return v
	}
	return fallback
}

// resendBaseURL returns RESEND_BASE_URL when set, so a proxy that adds the API key can stand in for api.resend.com.
func resendBaseURL() string { return envOr("RESEND_BASE_URL", "https://api.resend.com") }
