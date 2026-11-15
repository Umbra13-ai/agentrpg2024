package main

// No request answers success when the database failed under it: one guarantee for every route.
//
// About 1,300 database calls in the server ignore their error, many inside helpers shared by dozens of routes, and a local fault
// test (Nov 15 2026) showed what that does: with a table broken, routes answered 200 with empty or partly saved results. Rather
// than trust every call site, the database driver itself reports each failure to the request it happened in:
//
//   - the server opens Postgres through "postgres-checked" (checkedDriver): the lib/pq driver, with every Exec, Query, Prepare,
//     Begin and Commit error noted for the goroutine that made it (database/sql runs a call in its caller's goroutine);
//   - withProtections runs each request through guardDB: the request's goroutine is watched, its reply is held until the handler
//     returns, and if a database call failed during it, any reply below 500 (a success, or a 4xx such as "not found" or "no fight"
//     that the server couldn't actually know) is replaced by 500 {"error": "database_error"}, saying that part of the request may
//     have been saved. The failing statements are logged. A handler that catches a failure on purpose and answers it (a name
//     already taken: 409) says so with handledDBError(err), and its reply stays.
//
// Work a request starts in another goroutine (the request log) isn't watched; background workers aren't either: they log.
// A missing row (sql.ErrNoRows) is not a failure and never reaches the driver as one. A NULL scanned into a Go string is not a
// database failure either: Postgres answered; that is the reader's bug (readActions, answerDBFailure).

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/lib/pq"
)

func init() { sql.Register("postgres-checked", checkedDriver{&pq.Driver{}}) }

// ---------------------------------------------------------------- which goroutine is serving which request

type dbWatch struct {
	mu     sync.Mutex
	errors []string
}

var (
	watchMu  sync.Mutex
	watching = map[uint64]*dbWatch{}
)

// goroutineID: the id of the calling goroutine (from its stack header, "goroutine 123 [running]:").
func goroutineID() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		id, _ := strconv.ParseUint(string(b[:i]), 10, 64)
		return id
	}
	return 0
}

// noteDBError records a failed database call for the request the calling goroutine serves (if it serves one).
func noteDBError(err error, what string) {
	if err == nil || errors.Is(err, driver.ErrSkip) || errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	id := goroutineID()
	watchMu.Lock()
	wch := watching[id]
	watchMu.Unlock()
	if wch == nil {
		return
	}
	if len(what) > 160 {
		what = what[:160] + "..."
	}
	wch.mu.Lock()
	wch.errors = append(wch.errors, err.Error()+" | "+strings.Join(strings.Fields(what), " "))
	wch.mu.Unlock()
}

// handledDBError: the calling handler caught this database failure on purpose and answers it itself (a unique name already taken:
// 409), so it doesn't turn the reply into a database_error.
func handledDBError(err error) {
	if err == nil {
		return
	}
	id := goroutineID()
	watchMu.Lock()
	wch := watching[id]
	watchMu.Unlock()
	if wch == nil {
		return
	}
	wch.mu.Lock()
	defer wch.mu.Unlock()
	for i := len(wch.errors) - 1; i >= 0; i-- {
		if strings.HasPrefix(wch.errors[i], err.Error()+" | ") {
			wch.errors = append(wch.errors[:i], wch.errors[i+1:]...)
			return
		}
	}
}

// ---------------------------------------------------------------- the reply, held until the handler returns

type heldReply struct {
	w      http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (h *heldReply) Header() http.Header { return h.w.Header() }
func (h *heldReply) WriteHeader(code int) {
	if h.status == 0 {
		h.status = code
	}
}
func (h *heldReply) Write(b []byte) (int, error) {
	if h.status == 0 {
		h.status = http.StatusOK
	}
	return h.body.Write(b)
}

// guardDB serves one request with its database calls watched (see the file comment).
func guardDB(w http.ResponseWriter, r *http.Request, next http.Handler) {
	id := goroutineID()
	wch := &dbWatch{}
	watchMu.Lock()
	watching[id] = wch
	watchMu.Unlock()
	defer func() {
		watchMu.Lock()
		delete(watching, id)
		watchMu.Unlock()
	}()
	held := &heldReply{w: w}
	next.ServeHTTP(held, r)
	if held.status == 0 {
		held.status = http.StatusOK
	}
	wch.mu.Lock()
	failed := append([]string(nil), wch.errors...)
	wch.mu.Unlock()
	if len(failed) > 0 {
		log.Printf("database error during %s %s (reply %d): %s", r.Method, r.URL.Path, held.status, strings.Join(failed, " || "))
	}
	if len(failed) > 0 && held.status < 500 {
		h := w.Header()
		for _, k := range []string{"Content-Length", "Content-Disposition", "Content-Encoding", "ETag", "Last-Modified"} {
			h.Del(k)
		}
		h.Set("Content-Type", "application/json")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"database_error","message":"The server had a database problem while answering (a problem on the server, not with your request). Part of what you asked may have been saved: check before trying again."}`))
		return
	}
	w.WriteHeader(held.status)
	w.Write(held.body.Bytes())
}

// ---------------------------------------------------------------- the driver: lib/pq with every failure noted

type checkedDriver struct{ d driver.Driver }

func (c checkedDriver) Open(name string) (driver.Conn, error) {
	conn, err := c.d.Open(name)
	if err != nil {
		noteDBError(err, "connect")
		return nil, err
	}
	return &checkedConn{conn}, nil
}

type checkedConn struct{ c driver.Conn }

func (c *checkedConn) Prepare(q string) (driver.Stmt, error) {
	s, err := c.c.Prepare(q)
	noteDBError(err, q)
	if err != nil {
		return nil, err
	}
	return &checkedStmt{s, q, c.c}, nil
}

func (c *checkedConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	var s driver.Stmt
	var err error
	if p, ok := c.c.(driver.ConnPrepareContext); ok {
		s, err = p.PrepareContext(ctx, q)
	} else {
		s, err = c.c.Prepare(q)
	}
	noteDBError(err, q)
	if err != nil {
		return nil, err
	}
	return &checkedStmt{s, q, c.c}, nil
}

func (c *checkedConn) Close() error { return c.c.Close() }

func (c *checkedConn) Begin() (driver.Tx, error) { //nolint:staticcheck // driver.Conn requires it
	t, err := c.c.Begin() //nolint:staticcheck
	noteDBError(err, "BEGIN")
	if err != nil {
		return nil, err
	}
	return checkedTx{t}, nil
}

func (c *checkedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	var t driver.Tx
	var err error
	if b, ok := c.c.(driver.ConnBeginTx); ok {
		t, err = b.BeginTx(ctx, opts)
	} else {
		t, err = c.c.Begin() //nolint:staticcheck
	}
	noteDBError(err, "BEGIN")
	if err != nil {
		return nil, err
	}
	return checkedTx{t}, nil
}

func (c *checkedConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.c.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := e.ExecContext(ctx, q, args)
	noteDBError(err, q)
	return res, err
}

func (c *checkedConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.c.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := qc.QueryContext(ctx, q, args)
	noteDBError(err, q)
	return rows, err
}

func (c *checkedConn) Ping(ctx context.Context) error {
	if p, ok := c.c.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *checkedConn) ResetSession(ctx context.Context) error {
	if s, ok := c.c.(driver.SessionResetter); ok {
		return s.ResetSession(ctx)
	}
	return nil
}

func (c *checkedConn) IsValid() bool {
	if v, ok := c.c.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *checkedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if n, ok := c.c.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type checkedStmt struct {
	s    driver.Stmt
	q    string
	conn driver.Conn // its connection: database/sql asks the statement to check an argument first, then no one else
}

func (s *checkedStmt) Close() error  { return s.s.Close() }
func (s *checkedStmt) NumInput() int { return s.s.NumInput() }

func (s *checkedStmt) Exec(args []driver.Value) (driver.Result, error) { //nolint:staticcheck
	res, err := s.s.Exec(args) //nolint:staticcheck
	noteDBError(err, s.q)
	return res, err
}

func (s *checkedStmt) Query(args []driver.Value) (driver.Rows, error) { //nolint:staticcheck
	rows, err := s.s.Query(args) //nolint:staticcheck
	noteDBError(err, s.q)
	return rows, err
}

func (s *checkedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	var res driver.Result
	var err error
	if e, ok := s.s.(driver.StmtExecContext); ok {
		res, err = e.ExecContext(ctx, args)
	} else {
		res, err = s.s.Exec(namedToValues(args)) //nolint:staticcheck
	}
	noteDBError(err, s.q)
	return res, err
}

func (s *checkedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	var rows driver.Rows
	var err error
	if q, ok := s.s.(driver.StmtQueryContext); ok {
		rows, err = q.QueryContext(ctx, args)
	} else {
		rows, err = s.s.Query(namedToValues(args)) //nolint:staticcheck
	}
	noteDBError(err, s.q)
	return rows, err
}

func (s *checkedStmt) CheckNamedValue(nv *driver.NamedValue) error {
	if n, ok := s.s.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(nv)
	}
	if n, ok := s.conn.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func namedToValues(args []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

type checkedTx struct{ t driver.Tx }

func (t checkedTx) Commit() error {
	err := t.t.Commit()
	noteDBError(err, "COMMIT")
	return err
}

func (t checkedTx) Rollback() error { return t.t.Rollback() }
