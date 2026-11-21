package main

// Tests for the rules given owners in the audit's Phase 0 (Nov 15, 2026): a GM command's named characters (characterIDFields,
// gm_campaign_2024.go), the connection's settings (withDBSettings), the client's address (clientIP) and the check for a query
// outside its own transaction (db_guard_2024.go). The suite phase0_rule_test.py drives the same rules over HTTP.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestNamedCharacters(t *testing.T) {
	type inner struct {
		Caster int `json:"caster_id"`
	}
	one := 9
	req := struct {
		inner
		Target    int    `json:"target_id"`
		Targets   []int  `json:"target_ids"`
		Character *int   `json:"character_id"`
		Campaign  int    `json:"campaign_id"` // not a character
		Record    int    `json:"record_id"`   // not a character
		Name      string `json:"give_to"`     // not an int: ignored
		Untagged  int
	}{inner{4}, 5, []int{6, -2, 0, 5}, &one, 77, 78, "x", 79}
	got := namedCharacters(&req)
	sort.Ints(got)
	if want := []int{4, 5, 6, 9}; !equalInts(got, want) {
		t.Fatalf("namedCharacters = %v, want %v (monsters, 0, repeats and other fields left out)", got, want)
	}
	if got := namedCharacters(nil); len(got) != 0 {
		t.Fatalf("nil request: %v", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// notCharacterFields: id fields of GM requests that name something other than a character. Every *_id / *_ids field of a GM
// request must be in characterIDFields or here, so a new field that names a character can't be missed by gmReadAs.
var notCharacterFields = map[string]bool{
	"campaign_id": true, "record_id": true, "deadline_id": true, "object_id": true, "curse_id": true, "combat_id": true,
	"companion_id": true, "agent_id": true, "item_id": true, "shop_id": true, "draft_id": true, "action_id": true,
	"resume_id":  true, // opaque saved combat work id; the resume owner scopes it to the GM campaign
	"monster_id": true, "npc_id": true, "quest_id": true, "map_id": true, "note_id": true, "effect_id": true,
	"attacher_id": true, // the attached creature (usually a monster's combat id), not the acting character
}

func TestEveryGMRequestCharacterFieldIsKnown(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	tag := regexp.MustCompile(`json:"([a-z_]+_ids?)(,[a-z]+)?"`)
	var unknown []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		src := string(b)
		if !strings.Contains(src, "gmRead(") && !strings.Contains(src, "gmReadAs(") {
			continue
		}
		for _, m := range tag.FindAllStringSubmatch(src, -1) {
			if !characterIDFields[m[1]] && !notCharacterFields[m[1]] {
				unknown = append(unknown, f+": "+m[1])
			}
		}
	}
	if len(unknown) > 0 {
		t.Errorf("id fields in files with GM commands that are in neither characterIDFields (gm_campaign_2024.go) nor notCharacterFields (this test): %v", unknown)
	}
}

func TestWithDBSettings(t *testing.T) {
	u := withDBSettings("postgres://u@h:5432/db?sslmode=disable&lock_timeout=2000")
	for _, want := range []string{"sslmode=disable", "timezone=UTC", "statement_timeout=30000", "lock_timeout=2000", "idle_in_transaction_session_timeout=60000"} {
		if !strings.Contains(u, want) {
			t.Errorf("URL form: %q lacks %q", u, want)
		}
	}
	if strings.Contains(u, "lock_timeout=10000") {
		t.Errorf("a setting the URL gives is kept: %q", u)
	}
	kv := withDBSettings("host=/tmp dbname=arpg timezone=Europe/Paris")
	if !strings.Contains(kv, "statement_timeout=30000") || strings.Contains(kv, "timezone=UTC") {
		t.Errorf("key=value form: %q", kv)
	}
}

func TestClientIP(t *testing.T) {
	defer func(v bool) { behindRenderProxy = v }(behindRenderProxy)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.9:5555"
	// as Render passes a request on (seen on staging, Nov 15 2026)
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7, 104.22.93.28, 10.31.159.139")
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	behindRenderProxy = false
	if got := clientIP(r); got != "10.0.0.9" {
		t.Errorf("not behind a proxy: %q, want the connection's address", got)
	}
	behindRenderProxy = true
	if got := clientIP(r); got != "203.0.113.7" {
		t.Errorf("behind Render: %q, want CF-Connecting-IP (not a forwarded entry the client wrote, not Render's own hop)", got)
	}
	r.Header.Set("CF-Connecting-IP", "not-an-address")
	if got := clientIP(r); got != "10.0.0.9" {
		t.Errorf("a malformed header: %q, want the connection's address", got)
	}
}

func TestAuthFailureNeverRefusesBeforeTheLimit(t *testing.T) {
	ip := "198.51.100.77"
	for i := 0; i < maxFailedPerMinute; i++ {
		if err := authFailure(ip, "invalid_token"); err.Error() != "invalid_token" {
			t.Fatalf("failure %d: %v", i+1, err)
		}
	}
	if err := authFailure(ip, "invalid_token"); !strings.HasPrefix(err.Error(), "too_many_failed_attempts") {
		t.Fatalf("after the limit: %v", err)
	}
}

// ---- the check for a query outside its own transaction, on a driver that has transactions

type txFakeDriver struct{}
type txFakeConn struct{}
type txFakeTx struct{}
type txFakeRows struct{ done bool }

func (txFakeDriver) Open(string) (driver.Conn, error)    { return txFakeConn{}, nil }
func (txFakeConn) Prepare(q string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (txFakeConn) Close() error                          { return nil }
func (txFakeConn) Begin() (driver.Tx, error)             { return txFakeTx{}, nil }
func (txFakeTx) Commit() error                           { return nil }
func (txFakeTx) Rollback() error                         { return nil }
func (r *txFakeRows) Columns() []string                  { return []string{"n"} }
func (r *txFakeRows) Close() error                       { return nil }
func (txFakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &txFakeRows{}, nil
}
func (r *txFakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(1)
	return nil
}

var registerTxFake sync.Once

func TestQueryOutsideItsTransactionIsReported(t *testing.T) {
	registerTxFake.Do(func() { sql.Register("txfake-checked", checkedDriver{txFakeDriver{}}) })
	d, err := sql.Open("txfake-checked", "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var reported []string
	old := txOutside
	txOutside = func(q, began, from string) { reported = append(reported, q+" | "+began+" | "+from) }
	defer func() { txOutside = old }()
	var n int
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tx.QueryRow("SELECT 1 -- in the transaction").Scan(&n)
	if len(reported) != 0 {
		t.Fatalf("a query in the transaction was reported: %v", reported)
	}
	d.QueryRow("SELECT 2 -- outside it").Scan(&n)
	if len(reported) != 1 || !strings.Contains(reported[0], "SELECT 2") || !strings.Contains(reported[0], "TestQueryOutsideItsTransactionIsReported") {
		t.Fatalf("the query on another connection while the transaction is open: %v", reported)
	}
	tx.Commit()
	d.QueryRow("SELECT 3 -- after the commit").Scan(&n)
	if len(reported) != 1 {
		t.Fatalf("a query after the commit was reported: %v", reported)
	}
}
