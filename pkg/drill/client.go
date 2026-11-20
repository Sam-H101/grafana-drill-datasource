// Package drill is a small client for Apache Drill's REST API and converts its
// results to Grafana data frames. It knows nothing about Grafana queries or
// macros. Behaviour it relies on is recorded in docs/drill-rest-notes.md.
package drill

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// AuthMode mirrors models.AuthMode without importing it.
type AuthMode string

const (
	AuthNone  AuthMode = "none"
	AuthBasic AuthMode = "basic"
	AuthForm  AuthMode = "form"
)

// DefaultMaxResponseBytes caps a single query response.
const DefaultMaxResponseBytes int64 = 256 << 20

const (
	maxProfileBytes   = 32 << 20
	maxErrorBodyBytes = 64 << 10
	cancelTimeout     = 5 * time.Second
	tagPrefix         = "grafana-query:"
)

// Config configures a Client.
type Config struct {
	BaseURL         string
	AuthMode        AuthMode
	Username        string
	Password        string
	ImpersonateUser string
	// MaxResponseBytes caps a query response; 0 means DefaultMaxResponseBytes.
	MaxResponseBytes int64
}

// Client talks to one Drill web endpoint. It is safe for concurrent use.
type Client struct {
	base *url.URL
	cfg  Config
	http *http.Client

	sessionMu sync.Mutex
	session   []*http.Cookie // form login cookies
}

// QueryRequest is one SQL statement to run.
type QueryRequest struct {
	SQL           string
	AutoLimit     int
	DefaultSchema string
	Options       map[string]string
}

// NewClient builds a client on top of hc (normally from the SDK's httpclient,
// which carries TLS and proxy settings). Redirects are never followed: Drill
// answers bad credentials with a redirect to its login page.
func NewClient(cfg Config, hc *http.Client) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("invalid Drill URL")
	}
	switch cfg.AuthMode {
	case "":
		cfg.AuthMode = AuthNone
	case AuthNone, AuthBasic, AuthForm:
	default:
		return nil, fmt.Errorf("unknown auth mode %q", cfg.AuthMode)
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if hc == nil {
		hc = &http.Client{}
	}
	c := *hc
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: base, cfg: cfg, http: &c}, nil
}

type wireRequest struct {
	QueryType     string            `json:"queryType"`
	Query         string            `json:"query"`
	AutoLimit     int               `json:"autoLimit,omitempty"`
	DefaultSchema string            `json:"defaultSchema,omitempty"`
	Options       map[string]string `json:"options,omitempty"`
}

// Query runs one statement and returns the full result. If ctx ends first,
// the query is also cancelled on the Drill side (closing the connection does
// not stop it there; notes section 10).
func (c *Client) Query(ctx context.Context, req QueryRequest) (*Result, error) {
	sql := CleanSQL(req.SQL)
	if sql == "" {
		return nil, errors.New("the query is empty")
	}
	tag := newTag()
	body, err := json.Marshal(wireRequest{
		QueryType:     "SQL",
		Query:         "/* " + tag + " */ " + sql,
		AutoLimit:     req.AutoLimit,
		DefaultSchema: req.DefaultSchema,
		Options:       req.Options,
	})
	if err != nil {
		return nil, err
	}

	res, err := c.runQuery(ctx, body)
	if err != nil && ctx.Err() != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
		defer cancel()
		_ = c.cancelByTag(cctx, tag)
		return nil, fmt.Errorf("%w: %w", ErrCanceled, ctx.Err())
	}
	var qe *QueryError
	if errors.As(err, &qe) {
		qe.Message = stripTag(qe.Message, "/* "+tag+" */ ")
	}
	return res, err
}

// stripTag removes the cancellation tag from a Drill error message, which
// quotes the submitted SQL, and moves a caret line under it back by the same
// amount so it still points at the error.
func stripTag(msg, prefix string) string {
	if !strings.Contains(msg, prefix) {
		return msg
	}
	lines := strings.Split(msg, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], prefix) {
			continue
		}
		lines[i] = strings.Replace(lines[i], prefix, "", 1)
		if i+1 < len(lines) {
			caret := lines[i+1]
			if strings.TrimSpace(caret) == "^" && strings.HasPrefix(caret, strings.Repeat(" ", len(prefix))) {
				lines[i+1] = caret[len(prefix):]
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (c *Client) runQuery(ctx context.Context, body []byte) (*Result, error) {
	resp, err := c.do(ctx, func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/query.json"), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		return r, nil
	})
	if err != nil {
		return nil, err
	}
	defer drainClose(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "json") {
		// An HTML page here is Drill's login page or a proxy error.
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: "unexpected " + ct + " response"}
	}

	wire, err := decodeResponse(&cappedReader{r: resp.Body, max: c.cfg.MaxResponseBytes})
	if err != nil {
		return nil, err
	}
	switch {
	case wire.errorMessage != "":
		return nil, &QueryError{QueryID: wire.result.QueryID, Message: cleanMessage(wire.errorMessage)}
	case wire.queryState == "COMPLETED":
		return &wire.result, nil
	case wire.queryState == "CANCELED":
		return nil, ErrCanceled
	case wire.queryState == "FAILED":
		return nil, &QueryError{QueryID: wire.result.QueryID, Message: c.failureMessage(ctx, wire.result.QueryID)}
	default:
		return nil, fmt.Errorf("drill returned query state %q", wire.queryState)
	}
}

var queryIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// failureMessage fetches a failed query's error text from its profile, which
// is the only place Drill puts it (notes section 6).
func (c *Client) failureMessage(ctx context.Context, queryID string) string {
	generic := "Drill could not run the query"
	if queryID != "" {
		generic += " (query ID " + queryID + ")"
	}
	if !queryIDPattern.MatchString(queryID) {
		return generic
	}
	// Drill completes the profile shortly after answering, so the first fetch
	// can lack the error; retry briefly with backoff.
	delay := 50 * time.Millisecond
	for attempt := 0; attempt < 6; attempt++ {
		var profile struct {
			Error string `json:"error"`
		}
		err := c.getJSON(ctx, "/profiles/"+url.PathEscape(queryID)+".json", maxProfileBytes, &profile)
		if err == nil && strings.TrimSpace(profile.Error) != "" {
			return cleanMessage(profile.Error)
		}
		if err != nil && !isRetryableProfileErr(err) {
			return generic
		}
		select {
		case <-ctx.Done():
			return generic
		case <-time.After(delay):
		}
		delay *= 2
	}
	return generic
}

// isRetryableProfileErr: profiles that are not written yet can give 404 or 500.
func isRetryableProfileErr(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.StatusCode == http.StatusNotFound || he.StatusCode >= 500)
}

// cancelByTag finds the running query whose text starts with the tag comment
// and cancels it (notes section 10). It retries briefly because a query
// cancelled right after submission may not be listed yet.
func (c *Client) cancelByTag(ctx context.Context, tag string) error {
	prefix := "/* " + tag + " */"
	for attempt := 0; attempt < 3; attempt++ {
		var list struct {
			RunningQueries []struct {
				QueryID string `json:"queryId"`
				Query   string `json:"query"`
			} `json:"runningQueries"`
		}
		if err := c.getJSON(ctx, "/profiles.json", maxProfileBytes, &list); err != nil {
			return err
		}
		for _, q := range list.RunningQueries {
			if strings.HasPrefix(q.Query, prefix) && queryIDPattern.MatchString(q.QueryID) {
				resp, err := c.do(ctx, func() (*http.Request, error) {
					return http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/profiles/cancel/"+url.PathEscape(q.QueryID)), nil)
				})
				if err != nil {
					return err
				}
				drainClose(resp.Body)
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("query not found among running queries")
}

// Version returns the Drillbit's version from sys.version.
func (c *Client) Version(ctx context.Context) (string, error) {
	res, err := c.Query(ctx, QueryRequest{SQL: "SELECT version FROM sys.version", AutoLimit: 1})
	if err != nil {
		return "", err
	}
	if res.NumRows() == 0 || len(res.Values) == 0 || res.Values[0][0] == nil {
		return "", errors.New("sys.version returned no rows")
	}
	var v string
	if err := json.Unmarshal(res.Values[0][0], &v); err != nil {
		return "", errors.New("sys.version returned an unexpected value")
	}
	return v, nil
}

func (c *Client) getJSON(ctx context.Context, path string, limit int64, into any) error {
	resp, err := c.do(ctx, func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(path), nil)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Accept", "application/json")
		return r, nil
	})
	if err != nil {
		return err
	}
	defer drainClose(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(into)
}

// do sends a request with authentication applied. newReq is called again if
// a form session has expired and the request must be retried after login.
func (c *Client) do(ctx context.Context, newReq func() (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		if c.cfg.AuthMode == AuthForm {
			cookies, err := c.formSession(ctx, attempt > 0)
			if err != nil {
				return nil, err
			}
			for _, ck := range cookies {
				req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
			}
		}
		c.applyAuth(req)

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if isLoginRedirect(resp) {
			drainClose(resp.Body)
			if c.cfg.AuthMode == AuthForm && attempt == 0 {
				continue // session expired: log in again once
			}
			return nil, ErrAuthFailed
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			drainClose(resp.Body)
			return nil, ErrAuthFailed
		}
		return resp, nil
	}
}

func (c *Client) applyAuth(req *http.Request) {
	switch c.cfg.AuthMode {
	case AuthBasic:
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	case AuthNone:
		if c.cfg.ImpersonateUser != "" {
			req.Header.Set("User-Name", c.cfg.ImpersonateUser)
		}
	}
}

// formSession returns the session cookies, logging in first if there are none
// or if renew is set.
func (c *Client) formSession(ctx context.Context, renew bool) ([]*http.Cookie, error) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if len(c.session) > 0 && !renew {
		return c.session, nil
	}
	c.session = nil

	form := url.Values{"j_username": {c.cfg.Username}, "j_password": {c.cfg.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/j_security_check"), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp.Body)

	// Success is a redirect away from the login page with a session cookie;
	// failure is a 200 login page (notes section 8).
	if resp.StatusCode < 300 || resp.StatusCode >= 400 || isLoginRedirect(resp) {
		if resp.StatusCode >= 500 {
			return nil, httpError(resp)
		}
		return nil, ErrAuthFailed
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil, ErrAuthFailed
	}
	c.session = cookies
	return cookies, nil
}

func isLoginRedirect(resp *http.Response) bool {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return false
	}
	loc := strings.ToLower(resp.Header.Get("Location"))
	return strings.Contains(loc, "login")
}

func (c *Client) endpoint(path string) string {
	u := *c.base
	u.Path = c.base.Path + path
	return u.String()
}

func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	body := string(b)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") {
		body = "" // don't echo HTML pages into panels
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		body = "unexpected redirect"
	}
	return &HTTPError{StatusCode: resp.StatusCode, Body: cleanMessage(body)}
}

func drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	_ = body.Close()
}

func newTag() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return tagPrefix + hex.EncodeToString(b[:])
}

// CleanSQL trims whitespace and trailing semicolons, which Drill rejects
// (notes section 1).
func CleanSQL(sql string) string {
	s := strings.TrimSpace(sql)
	for strings.HasSuffix(s, ";") {
		s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	}
	return s
}
