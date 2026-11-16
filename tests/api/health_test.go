// Package api provides API endpoint tests for Agent RPG 2024.
// These tests are read-only checks against a running server named by AGENTRPG_URL (for example http://localhost:8080);
// without it they are skipped. (They used to run whenever TEST_DATABASE_URL was set, which says nothing about a server
// running, so they failed in CI. The server's own tests against Postgres are in cmd/server/postgres_integration_test.go.)
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestHealthEndpoint verifies the /health endpoint returns 200 OK
func TestHealthEndpoint(t *testing.T) {
	skipWithoutServer(t)
	resp, err := http.Get(baseURL() + "/health")
	if err != nil {
		t.Fatalf("Failed to GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health returned status %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestVersionEndpoint verifies the /api/version endpoint returns version info
func TestVersionEndpoint(t *testing.T) {
	skipWithoutServer(t)
	resp, err := http.Get(baseURL() + "/api/version")
	if err != nil {
		t.Fatalf("Failed to GET /api/version: %v", err)
	}
	defer resp.Body.Close()

	// Note: Production may not have /api/version if outdated
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("Skipping: /api/version not available (production outdated)")
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/version returned status %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var version struct {
		Version   string `json:"version"`
		BuildTime string `json:"build_time,omitempty"`
		StartedAt string `json:"started_at,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		t.Errorf("Failed to decode version response: %v", err)
	}
	if version.Version == "" {
		t.Error("Version response missing 'version' field")
	}
}

// mockHandler is a simple handler for testing
func mockHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// TestMockServer demonstrates local test server pattern
func TestMockServer(t *testing.T) {
	// Create a test server with a mock handler
	server := httptest.NewServer(http.HandlerFunc(mockHandler))
	defer server.Close()

	// Test the mock server
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("Failed to GET mock server: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Mock server returned status %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// skipWithoutServer skips a test that needs a running server when AGENTRPG_URL names none.
func skipWithoutServer(t *testing.T) {
	if os.Getenv("AGENTRPG_URL") == "" {
		t.Skip("Skipping: AGENTRPG_URL (a running server) not set")
	}
}

// baseURL is the server the endpoint tests talk to: AGENTRPG_URL.
func baseURL() string { return os.Getenv("AGENTRPG_URL") }
