package main

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// a driver whose statements containing "FAIL" fail, standing in for Postgres
type fakeDriver struct{}
type fakeConn struct{}
type fakeStmt struct{ q string }
type fakeResult struct{}
type fakeRows struct{ done bool }

func (fakeDriver) Open(string) (driver.Conn, error)    { return fakeConn{}, nil }
func (fakeConn) Prepare(q string) (driver.Stmt, error) { return fakeStmt{q}, nil }
func (fakeConn) Close() error                          { return nil }
func (fakeConn) Begin() (driver.Tx, error)             { return nil, errors.New("no transactions") }
func (s fakeStmt) Close() error                        { return nil }
func (s fakeStmt) NumInput() int                       { return -1 }
func (fakeResult) LastInsertId() (int64, error)        { return 0, nil }
func (fakeResult) RowsAffected() (int64, error)        { return 1, nil }
func (r *fakeRows) Columns() []string                  { return []string{"n"} }
func (r *fakeRows) Close() error                       { return nil }
func (s fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	if strings.Contains(s.q, "FAIL") {
		return nil, errors.New(`pq: relation "broken" does not exist`)
	}
	return fakeResult{}, nil
}
func (s fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	if strings.Contains(s.q, "FAIL") {
		return nil, errors.New(`pq: relation "broken" does not exist`)
	}
	return &fakeRows{}, nil
}
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(7)
	return nil
}

var registerFake sync.Once

func fakeDB(t *testing.T) *sql.DB {
	registerFake.Do(func() { sql.Register("fake-checked", checkedDriver{fakeDriver{}}) })
	d, err := sql.Open("fake-checked", "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func serve(h http.HandlerFunc) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	guardDB(w, httptest.NewRequest("POST", "/api/x", nil), h)
	return w
}

// A handler that ignores a failed database call can't answer success: the reply becomes 500 database_error.
func TestNoSuccessOverADatabaseFailure(t *testing.T) {
	d := fakeDB(t)
	w := serve(func(w http.ResponseWriter, r *http.Request) {
		d.Exec("UPDATE FAIL SET x = 1") // the error ignored, as ~1,300 calls did
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	})
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"database_error"`) || strings.Contains(w.Body.String(), "success") {
		t.Errorf("an ignored write failure answered %d %s", w.Code, w.Body.String())
	}
	w = serve(func(w http.ResponseWriter, r *http.Request) {
		var n int
		d.QueryRow("SELECT n FROM FAIL").Scan(&n) // a read failure ignored: the reply would show a zero
		w.Write([]byte(`{"count":0}`))
	})
	if w.Code != 500 {
		t.Errorf("an ignored read failure answered %d %s", w.Code, w.Body.String())
	}
}

// A 4xx a handler chose after catching the error on purpose (a name already taken, handledDBError) stays; a 4xx over a failure
// the handler didn't handle ("not found", "no fight") becomes 500; a request with no failure is untouched; a failure in another
// goroutine (the request log) isn't the request's.
func TestDatabaseGuardLeavesTheRestAlone(t *testing.T) {
	d := fakeDB(t)
	w := serve(func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.Exec("INSERT FAIL"); err != nil {
			handledDBError(err)
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"name_taken"}`))
		}
	})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "name_taken") {
		t.Errorf("a handled conflict became %d %s", w.Code, w.Body.String())
	}
	w = serve(func(w http.ResponseWriter, r *http.Request) {
		var active bool
		if d.QueryRow("SELECT active FROM FAIL").Scan(&active) != nil || !active {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"no_active_combat"}`))
		}
	})
	if w.Code != 500 {
		t.Errorf("a 4xx over an unhandled failure stayed %d %s", w.Code, w.Body.String())
	}
	w = serve(func(w http.ResponseWriter, r *http.Request) {
		var n int
		if err := d.QueryRow("SELECT n FROM ok").Scan(&n); err != nil || n != 7 {
			t.Errorf("read through the checked driver: %v %d", err, n)
		}
		w.Header().Set("X-Kept", "yes")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"n":7}`))
	})
	if w.Code != 201 || w.Body.String() != `{"n":7}` || w.Header().Get("X-Kept") != "yes" {
		t.Errorf("a clean request changed: %d %s", w.Code, w.Body.String())
	}
	w = serve(func(w http.ResponseWriter, r *http.Request) {
		done := make(chan bool)
		go func() { d.Exec("INSERT FAIL INTO log"); done <- true }()
		<-done
		w.Write([]byte(`{"ok":true}`))
	})
	if w.Code != 200 {
		t.Errorf("a failure in another goroutine failed the request: %d", w.Code)
	}
	watchMu.Lock()
	left := len(watching)
	watchMu.Unlock()
	if left != 0 {
		t.Errorf("%d requests still watched after they ended", left)
	}
}

// A query's or a write's error is never thrown away when its result is used: rows or a result from a failed call are nil, and
// using them crashed the request (GET /api/characters, release-grapple, the ground's claim and return in the fault test).
func TestNoDiscardedQueryError(t *testing.T) {
	discarded := regexp.MustCompile(`^\s*\w+, _ :?= (db|tx|database)\.(Query|Exec)\(`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, line := range strings.Split(string(b), "\n") {
			if discarded.MatchString(line) {
				t.Errorf("%s:%d throws away a query's error but keeps its result: check the error", f, n+1)
			}
		}
	}
}
