package drill

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDrill emulates the Drill REST behaviour recorded in
// docs/drill-rest-notes.md, closely enough to test the client.
type fakeDrill struct {
	t        *testing.T
	auth     AuthMode
	user     string
	pass     string
	slow     chan struct{} // if set, /query.json blocks until closed or the client goes away
	response string        // body for /query.json; default employees fixture

	mu         sync.Mutex
	lastBody   wireRequest
	lastHeader http.Header
	running    map[string]string // queryId -> query text
	cancelled  []string
	logins     atomic.Int32
	sessions   map[string]bool
}

func newFake(t *testing.T, auth AuthMode) (*fakeDrill, *httptest.Server) {
	f := &fakeDrill{t: t, auth: auth, user: "grafana", pass: "secret", running: map[string]string{}, sessions: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeDrill) authorized(r *http.Request) bool {
	switch f.auth {
	case AuthBasic:
		u, p, ok := r.BasicAuth()
		return ok && u == f.user && p == f.pass
	case AuthForm:
		c, err := r.Cookie("JSESSIONID")
		f.mu.Lock()
		defer f.mu.Unlock()
		return err == nil && f.sessions[c.Value]
	}
	return true
}

func (f *fakeDrill) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/j_security_check" && r.Method == http.MethodPost:
		_ = r.ParseForm()
		f.logins.Add(1)
		if r.PostForm.Get("j_username") == f.user && r.PostForm.Get("j_password") == f.pass {
			sid := "s" + time.Now().Format("150405.000000000")
			f.mu.Lock()
			f.sessions[sid] = true
			f.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: sid, Path: "/"})
			w.Header().Set("Location", "/;jsessionid="+sid)
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>login page</html>")
		return
	case f.auth != AuthNone && !f.authorized(r):
		w.Header().Set("Location", "/mainLogin?redirect="+r.URL.Path)
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}

	switch {
	case r.URL.Path == "/query.json":
		var body wireRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields() // Drill rejects unknown fields (notes section 1)
		if err := dec.Decode(&body); err != nil {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "Unrecognized field")
			return
		}
		f.mu.Lock()
		f.lastBody = body
		f.lastHeader = r.Header.Clone()
		f.mu.Unlock()
		if f.slow != nil {
			id := "15000000-0000-0000-0000-000000000001"
			f.mu.Lock()
			f.running[id] = body.Query
			f.mu.Unlock()
			select {
			case <-f.slow:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if f.response != "" {
			_, _ = io.WriteString(w, f.response)
			return
		}
		b, _ := os.ReadFile("testdata/employees.json")
		_, _ = w.Write(b)
	case r.URL.Path == "/profiles.json":
		f.mu.Lock()
		var list struct {
			RunningQueries []map[string]string `json:"runningQueries"`
		}
		for id, q := range f.running {
			list.RunningQueries = append(list.RunningQueries, map[string]string{"queryId": id, "query": q})
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	case strings.HasPrefix(r.URL.Path, "/profiles/cancel/"):
		id := strings.TrimPrefix(r.URL.Path, "/profiles/cancel/")
		f.mu.Lock()
		f.cancelled = append(f.cancelled, id)
		delete(f.running, id)
		f.mu.Unlock()
		_, _ = io.WriteString(w, "Cancelled query "+id)
	case strings.HasPrefix(r.URL.Path, "/profiles/") && strings.HasSuffix(r.URL.Path, ".json"):
		b, _ := os.ReadFile("testdata/profile_failed.json")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	default:
		http.NotFound(w, r)
	}
}

func newClient(t *testing.T, srv *httptest.Server, cfg Config) *Client {
	t.Helper()
	cfg.BaseURL = srv.URL
	c, err := NewClient(cfg, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestQuerySendsOnlyKnownFieldsAndTags(t *testing.T) {
	f, srv := newFake(t, AuthNone)
	c := newClient(t, srv, Config{ImpersonateUser: "alice"})
	res, err := c.Query(context.Background(), QueryRequest{SQL: "SELECT 1 ;; ", AutoLimit: 5, DefaultSchema: "cp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.NumRows() != 5 || len(res.Columns) != 3 {
		t.Fatalf("rows=%d cols=%d", res.NumRows(), len(res.Columns))
	}
	if f.lastBody.AutoLimit != 5 || f.lastBody.DefaultSchema != "cp" || f.lastBody.QueryType != "SQL" {
		t.Fatalf("body %+v", f.lastBody)
	}
	if !strings.HasPrefix(f.lastBody.Query, "/* grafana-query:") || !strings.HasSuffix(f.lastBody.Query, "*/ SELECT 1") {
		t.Fatalf("query text %q", f.lastBody.Query)
	}
	if got := f.lastHeader.Get("User-Name"); got != "alice" {
		t.Fatalf("User-Name = %q", got)
	}
}

func TestFailedQueryMessageComesFromProfile(t *testing.T) {
	f, srv := newFake(t, AuthNone)
	b, _ := os.ReadFile("testdata/failed.json")
	f.response = string(b)
	c := newClient(t, srv, Config{})
	_, err := c.Query(context.Background(), QueryRequest{SQL: "SELEC 1"})
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("want QueryError, got %v", err)
	}
	if !strings.HasPrefix(qe.Message, "PARSE ERROR: Non-query expression") {
		t.Fatalf("message %q", qe.Message)
	}
	if strings.Contains(qe.Message, "org.apache") || strings.Contains(qe.Message, "verbose") {
		t.Fatalf("message leaks internals: %q", qe.Message)
	}
}

func TestStripTag(t *testing.T) {
	p := "/* grafana-query:abc */ "
	in := "PARSE ERROR: x\n\nSQL Query: " + p + "SELEC 1\n                        " + strings.Repeat(" ", len(p)) + "^\n"
	want := "PARSE ERROR: x\n\nSQL Query: SELEC 1\n                        ^\n"
	if got := stripTag(in, p); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Caret on a later line of a multi-line query is left alone.
	multi := "SQL Query: " + p + "SELECT\nFRM t\n^"
	if got := stripTag(multi, p); got != "SQL Query: SELECT\nFRM t\n^" {
		t.Fatalf("got %q", got)
	}
	if got := stripTag("no tag here", p); got != "no tag here" {
		t.Fatal(got)
	}
}

func TestErrorMessageField(t *testing.T) {
	f, srv := newFake(t, AuthNone)
	f.response = `{  "errorMessage" : "Query submission failed"}`
	c := newClient(t, srv, Config{})
	_, err := c.Query(context.Background(), QueryRequest{SQL: "SELECT 1"})
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Message != "Query submission failed" {
		t.Fatalf("got %v", err)
	}
}

func TestBasicAuth(t *testing.T) {
	_, srv := newFake(t, AuthBasic)
	ok := newClient(t, srv, Config{AuthMode: AuthBasic, Username: "grafana", Password: "secret"})
	if _, err := ok.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); err != nil {
		t.Fatalf("good credentials: %v", err)
	}
	bad := newClient(t, srv, Config{AuthMode: AuthBasic, Username: "grafana", Password: "wrong"})
	if _, err := bad.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("bad credentials: %v", err)
	}
	none := newClient(t, srv, Config{})
	if _, err := none.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("no credentials: %v", err)
	}
}

func TestFormAuthReusesAndRenewsSession(t *testing.T) {
	f, srv := newFake(t, AuthForm)
	c := newClient(t, srv, Config{AuthMode: AuthForm, Username: "grafana", Password: "secret"})
	for i := 0; i < 3; i++ {
		if _, err := c.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.logins.Load(); n != 1 {
		t.Fatalf("logins = %d, want 1", n)
	}
	// Expire the session on the server: the client logs in again once.
	f.mu.Lock()
	f.sessions = map[string]bool{}
	f.mu.Unlock()
	if _, err := c.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	if n := f.logins.Load(); n != 2 {
		t.Fatalf("logins = %d, want 2", n)
	}

	bad := newClient(t, srv, Config{AuthMode: AuthForm, Username: "grafana", Password: "nope"})
	if _, err := bad.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("bad form login: %v", err)
	}
}

func TestContextCancelCancelsOnDrill(t *testing.T) {
	f, srv := newFake(t, AuthNone)
	f.slow = make(chan struct{})
	defer close(f.slow)
	c := newClient(t, srv, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.Query(ctx, QueryRequest{SQL: "SELECT slow"})
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cancelled) != 1 || f.cancelled[0] != "15000000-0000-0000-0000-000000000001" {
		t.Fatalf("cancelled = %v", f.cancelled)
	}
}

func TestResponseCap(t *testing.T) {
	_, srv := newFake(t, AuthNone)
	c := newClient(t, srv, Config{MaxResponseBytes: 100})
	if _, err := c.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v", err)
	}
	// Exactly at the cap is fine.
	b, _ := os.ReadFile("testdata/employees.json")
	exact := newClient(t, srv, Config{MaxResponseBytes: int64(len(b))})
	if _, err := exact.Query(context.Background(), QueryRequest{SQL: "SELECT 1"}); err != nil {
		t.Fatalf("at cap: %v", err)
	}
}

func TestHTTPErrorsDoNotEchoHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html><body>proxy error</body></html>")
	}))
	defer srv.Close()
	c := newClient(t, srv, Config{})
	_, err := c.Query(context.Background(), QueryRequest{SQL: "SELECT 1"})
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 502 || strings.Contains(he.Error(), "<html>") {
		t.Fatalf("err = %v", err)
	}
}

func TestBadRequestFromDrill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "Unrecognized field \"bogus\"\n at org.apache.drill.Foo(Foo.java:1)")
	}))
	defer srv.Close()
	c := newClient(t, srv, Config{})
	_, err := c.Query(context.Background(), QueryRequest{SQL: "SELECT 1"})
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 400 || strings.Contains(he.Body, "org.apache") {
		t.Fatalf("err = %v", err)
	}
}

func TestVersion(t *testing.T) {
	f, srv := newFake(t, AuthNone)
	f.response = `{"queryId":"q","columns":["version"],"metadata":["VARCHAR"],"rows":[{"version":"1.22.0"}],"queryState":"COMPLETED"}`
	c := newClient(t, srv, Config{})
	v, err := c.Version(context.Background())
	if err != nil || v != "1.22.0" {
		t.Fatalf("v=%q err=%v", v, err)
	}
}

func TestNewClientValidation(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "http://", "::"} {
		if _, err := NewClient(Config{BaseURL: u}, nil); err == nil {
			t.Errorf("NewClient(%q) accepted", u)
		}
	}
	if _, err := NewClient(Config{BaseURL: "http://x", AuthMode: "kerberos"}, nil); err == nil {
		t.Error("unknown auth mode accepted")
	}
}

func TestCleanSQL(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT 1;":        "SELECT 1",
		"  SELECT 1 ; ;\n": "SELECT 1",
		"SELECT ';'":       "SELECT ';'",
		"":                 "",
	} {
		if got := CleanSQL(in); got != want {
			t.Errorf("CleanSQL(%q) = %q", in, got)
		}
	}
}

func TestDecodeMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"truncated":  `{"queryId":"q","columns":["a"],"metadata":["INT"],"rows":[{"a":1}`,
		"not json":   `<html>`,
		"mismatched": `{"columns":["a","b"],"metadata":["INT"],"rows":[],"queryState":"COMPLETED"}`,
	} {
		if _, err := decodeResponse(strings.NewReader(body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestCleanMessageStripsStack(t *testing.T) {
	in := "SYSTEM ERROR: Boom\n\n  (java.lang.IllegalStateException) Boom\n    org.apache.drill.X.y():1\n    java.lang.Thread.run():833\n  Caused By (org.apache.Z) z\n"
	got := cleanMessage(in)
	if got != "SYSTEM ERROR: Boom" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("x", 5000)
	if n := len(cleanMessage(long)); n > maxMessageLen+4 {
		t.Fatalf("not truncated: %d", n)
	}
}
