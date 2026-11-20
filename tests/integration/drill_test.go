//go:build integration

// Package integration runs the backend against live Drill containers.
//
//	docker compose up -d drill drill-auth
//	devenv/run.sh go test -tags integration -count=1 ./tests/integration/...
//
// DRILL_URL and DRILL_AUTH_URL override the endpoints (defaults suit the
// toolbox container).
package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/samh/apache-drill/pkg/drill"
	"github.com/samh/apache-drill/pkg/plugin"
)

// Dev-only credentials from devenv/drill/README.md.
const (
	authUser = "grafana"
	authPass = "grafana-dev-password"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func drillURL() string     { return env("DRILL_URL", "http://drill:8047") }
func drillAuthURL() string { return env("DRILL_AUTH_URL", "http://drill-auth:8047") }

func client(t *testing.T, cfg drill.Config) *drill.Client {
	t.Helper()
	if cfg.BaseURL == "" {
		cfg.BaseURL = drillURL()
	}
	c, err := drill.NewClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func query(t *testing.T, c *drill.Client, sql string) *data.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.Query(ctx, drill.QueryRequest{SQL: sql, AutoLimit: 1000})
	if err != nil {
		t.Fatalf("query failed: %v\n%s", err, sql)
	}
	return drill.ToFrame("", res)
}

func value(t *testing.T, fr *data.Frame, name string) (any, bool) {
	t.Helper()
	f, idx := fr.FieldByName(name)
	if idx < 0 {
		t.Fatalf("no field %q", name)
	}
	return f.ConcreteAt(0)
}

func TestEveryType(t *testing.T) {
	fr := query(t, client(t, drill.Config{}), `SELECT
		CAST(1 AS INT) i, CAST(9007199254740993 AS BIGINT) b, CAST(1.5 AS FLOAT) f4, CAST(2.25 AS DOUBLE) f8,
		CAST('NaN' AS DOUBLE) nan, CAST(1.10 AS DECIMAL(5,2)) dec, true bt, 'héllo' v,
		CAST('2024-01-02' AS DATE) d, CAST('13:14:15.123' AS TIME) t,
		CAST('2024-01-02 03:04:05.678' AS TIMESTAMP) ts, CAST('1960-05-06 07:08:09.010' AS TIMESTAMP) old,
		INTERVAL '1 2:03:04' DAY TO SECOND ids, INTERVAL '1-2' YEAR TO MONTH iym, CAST('abc' AS VARBINARY) vb,
		CONVERT_FROM('{"a":1,"b":[1,2]}', 'JSON') m, CONVERT_FROM('[1,2,3]', 'JSON') lst
		FROM (VALUES(1));`)
	if fr.Meta != nil && len(fr.Meta.Notices) > 0 {
		t.Fatalf("notices: %+v", fr.Meta.Notices)
	}
	want := map[string]any{
		"i": int64(1), "b": int64(9007199254740993), "f4": 1.5, "f8": 2.25, "dec": 1.10, "bt": true, "v": "héllo",
		"d":   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		"t":   "13:14:15.123",
		"ts":  time.Date(2024, 1, 2, 3, 4, 5, 678e6, time.UTC),
		"old": time.Date(1960, 5, 6, 7, 8, 9, 10e6, time.UTC),
		"ids": "P1DT7384S", "iym": "P1Y2M", "vb": "YWJj",
		"m": `{"a":1,"b":[1,2]}`, "lst": `[1,2,3]`,
	}
	for name, w := range want {
		if got, _ := value(t, fr, name); got != w {
			t.Errorf("%s = %#v, want %#v", name, got, w)
		}
	}
	if got, _ := value(t, fr, "nan"); !math.IsNaN(got.(float64)) {
		t.Errorf("nan = %v", got)
	}
}

func TestNullsAndEmpty(t *testing.T) {
	c := client(t, drill.Config{})
	fr := query(t, c, `SELECT CAST(NULL AS INT) i, CAST(NULL AS VARCHAR) v, CAST(NULL AS TIMESTAMP) ts,
		CAST(NULL AS DOUBLE) d, CAST(NULL AS BOOLEAN) bo, CAST(NULL AS DATE) dt FROM (VALUES(1))`)
	for _, f := range fr.Fields {
		if _, ok := f.ConcreteAt(0); ok || !f.Nullable() {
			t.Errorf("%s: want nullable null", f.Name)
		}
	}
	empty := query(t, c, "SELECT employee_id, full_name FROM cp.`employee.json` WHERE 1=0")
	if n, _ := empty.RowLen(); n != 0 || len(empty.Fields) != 2 {
		t.Fatalf("empty: rows=%d fields=%d", n, len(empty.Fields))
	}
}

func TestAutoLimitAndDefaultSchema(t *testing.T) {
	c := client(t, drill.Config{})
	res, err := c.Query(context.Background(), drill.QueryRequest{SQL: "SELECT employee_id FROM `employee.json`", AutoLimit: 7, DefaultSchema: "cp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.NumRows() != 7 {
		t.Fatalf("rows = %d", res.NumRows())
	}
}

func TestQueryErrors(t *testing.T) {
	c := client(t, drill.Config{})
	for sql, want := range map[string]string{
		"SELEC 1":                        "PARSE ERROR",
		"SELECT * FROM dfs.tmp.`nope.x`": "VALIDATION ERROR",
	} {
		_, err := c.Query(context.Background(), drill.QueryRequest{SQL: sql})
		var qe *drill.QueryError
		if !errors.As(err, &qe) || !strings.Contains(qe.Message, want) {
			t.Errorf("%s: %v", sql, err)
		}
		if qe != nil && strings.Contains(qe.Message, "grafana-query") {
			t.Errorf("%s: message shows the internal tag: %s", sql, qe.Message)
		}
		if qe != nil && strings.Contains(qe.Message, "org.apache.drill") {
			t.Errorf("%s: message leaks a stack trace: %s", sql, qe.Message)
		}
	}
}

func TestAuthentication(t *testing.T) {
	base := drill.Config{BaseURL: drillAuthURL(), Username: authUser}
	for _, mode := range []drill.AuthMode{drill.AuthBasic, drill.AuthForm} {
		t.Run(string(mode), func(t *testing.T) {
			good := base
			good.AuthMode, good.Password = mode, authPass
			fr := query(t, client(t, good), "SELECT SESSION_USER u FROM (VALUES(1))")
			if u, _ := value(t, fr, "u"); u != authUser {
				t.Fatalf("session user %v", u)
			}

			bad := base
			bad.AuthMode, bad.Password = mode, "wrong"
			if _, err := client(t, bad).Query(context.Background(), drill.QueryRequest{SQL: "SELECT 1 FROM (VALUES(1))"}); !errors.Is(err, drill.ErrAuthFailed) {
				t.Fatalf("bad password: %v", err)
			}
		})
	}
	none := client(t, drill.Config{BaseURL: drillAuthURL()})
	if _, err := none.Query(context.Background(), drill.QueryRequest{SQL: "SELECT 1 FROM (VALUES(1))"}); !errors.Is(err, drill.ErrAuthFailed) {
		t.Fatalf("no credentials: %v", err)
	}
}

func TestFailedQueryMessageWithAuth(t *testing.T) {
	c := client(t, drill.Config{BaseURL: drillAuthURL(), AuthMode: drill.AuthForm, Username: authUser, Password: authPass})
	_, err := c.Query(context.Background(), drill.QueryRequest{SQL: "SELEC 1"})
	var qe *drill.QueryError
	if !errors.As(err, &qe) || !strings.Contains(qe.Message, "PARSE ERROR") {
		t.Fatalf("%v", err)
	}
}

const slowSQL = "SELECT COUNT(*) FROM cp.`employee.json` a, cp.`employee.json` b, cp.`employee.json` c WHERE a.salary + b.salary > c.salary"

// TestCancellation checks that a client-side timeout also stops the query on
// Drill (docs/drill-rest-notes.md section 10).
func TestCancellation(t *testing.T) {
	for _, cfg := range []drill.Config{
		{BaseURL: drillURL()},
		{BaseURL: drillAuthURL(), AuthMode: drill.AuthBasic, Username: authUser, Password: authPass},
	} {
		t.Run(string(cfg.AuthMode), func(t *testing.T) {
			c := client(t, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			start := time.Now()
			_, err := c.Query(ctx, drill.QueryRequest{SQL: slowSQL, Options: map[string]string{"planner.enable_nljoin_for_scalar_only": "false"}})
			if !errors.Is(err, drill.ErrCanceled) {
				t.Fatalf("err = %v", err)
			}
			if el := time.Since(start); el > 10*time.Second {
				t.Fatalf("took %s", el)
			}
			// Drill must no longer be running it.
			time.Sleep(time.Second)
			if running := runningQueries(t, cfg); strings.Contains(running, "a.salary + b.salary") {
				t.Fatalf("query still running on Drill: %s", running)
			}
		})
	}
}

// runningQueries returns the runningQueries section of /profiles.json as text.
func runningQueries(t *testing.T, cfg drill.Config) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, cfg.BaseURL+"/profiles.json", nil)
	if cfg.AuthMode == drill.AuthBasic {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		RunningQueries json.RawMessage `json:"runningQueries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("profiles.json: %v", err)
	}
	return string(body.RunningQueries)
}

func TestHealthCheckThroughPlugin(t *testing.T) {
	cases := []struct {
		name, url, json, password, want string
		ok                              bool
	}{
		{"ok", drillURL(), `{}`, "", "Connected to Apache Drill 1.", true},
		{"auth ok", drillAuthURL(), `{"authMode":"basic","username":"grafana"}`, authPass, "Connected to Apache Drill", true},
		{"wrong password", drillAuthURL(), `{"authMode":"form","username":"grafana"}`, "nope", "rejected the credentials", false},
		{"auth missing", drillAuthURL(), `{}`, "", "requires authentication", false},
		{"unreachable", "http://127.0.0.1:1", `{}`, "", "Cannot reach Drill at http://127.0.0.1:1", false},
		{"no such host", "http://no-such-drill.invalid:8047", `{}`, "", "host not found", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := backend.DataSourceInstanceSettings{URL: c.url, JSONData: json.RawMessage(c.json), DecryptedSecureJSONData: map[string]string{"password": c.password}}
			inst, err := plugin.NewDatasource(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			res, _ := inst.(*plugin.Datasource).CheckHealth(context.Background(), &backend.CheckHealthRequest{})
			if (res.Status == backend.HealthStatusOk) != c.ok || !strings.Contains(res.Message, c.want) {
				t.Fatalf("status=%v message=%q", res.Status, res.Message)
			}
			if strings.Contains(res.Message, c.password) && c.password != "" {
				t.Fatalf("message contains the password")
			}
		})
	}
}

func drillTLSURL() string { return env("DRILL_TLS_URL", "https://drill-tls:8047") }

// TestTLS checks the SDK TLS options end to end against drill-tls, whose CA
// certificate is generated at container start (devenv/drill/tls).
func TestTLS(t *testing.T) {
	caFile := env("DRILL_TLS_CA_FILE", "/tls/ca.crt")
	ca, err := os.ReadFile(caFile)
	if err != nil {
		t.Skipf("no CA certificate at %s (set DRILL_TLS_CA_FILE): %v", caFile, err)
	}
	cases := []struct {
		name, url, json string
		secure          map[string]string
		ok              bool
		want            string
	}{
		{"untrusted certificate", drillTLSURL(), `{}`, nil, false, "TLS error connecting to Drill"},
		{"custom CA", drillTLSURL(), `{"tlsAuthWithCACert":true}`, map[string]string{"tlsCACert": string(ca)}, true, "Connected to Apache Drill"},
		{"skip verify", drillTLSURL(), `{"tlsSkipVerify":true}`, nil, true, "Connected to Apache Drill"},
		{"wrong CA", drillTLSURL(), `{"tlsAuthWithCACert":true}`, map[string]string{"tlsCACert": wrongCA(t)}, false, "TLS error"},
		{"http to https port", strings.Replace(drillTLSURL(), "https://", "http://", 1), `{}`, nil, false, "change the URL to https://"},
		{"https to http port", strings.Replace(drillURL(), "http://", "https://", 1), `{}`, nil, false, "answered with plain HTTP"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := backend.DataSourceInstanceSettings{URL: c.url, JSONData: json.RawMessage(c.json), DecryptedSecureJSONData: c.secure}
			inst, err := plugin.NewDatasource(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			res, _ := inst.(*plugin.Datasource).CheckHealth(context.Background(), &backend.CheckHealthRequest{})
			t.Logf("message: %s", res.Message)
			if (res.Status == backend.HealthStatusOk) != c.ok || !strings.Contains(res.Message, c.want) {
				t.Fatalf("status=%v message=%q", res.Status, res.Message)
			}
		})
	}
}

// wrongCA returns an unrelated, freshly generated self-signed CA certificate.
func wrongCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
