package plugin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/drill"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/models"
)

// longResult is a long-format Drill result: time, label, value.
const longResult = `{"queryId":"q","columns":["time","gender","c"],"metadata":["TIMESTAMP","VARCHAR","BIGINT"],
"rows":[{"time":1704067200000,"gender":"M","c":2},{"time":1704067200000,"gender":"F","c":3},
{"time":1703980800000,"gender":"F","c":1},{"time":null,"gender":"F","c":9}],"queryState":"COMPLETED"}`

type fakeServer struct {
	body     string
	status   int
	lastSQL  atomic.Value
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	delay    time.Duration
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/query.json":
		n := f.inFlight.Add(1)
		defer f.inFlight.Add(-1)
		for {
			m := f.maxSeen.Load()
			if n <= m || f.maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		var req struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.lastSQL.Store(req.Query)
		if f.delay > 0 {
			select {
			case <-time.After(f.delay):
			case <-r.Context().Done():
				return
			}
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.body)
	case "/profiles.json":
		_, _ = io.WriteString(w, `{"runningQueries":[]}`)
	default:
		http.NotFound(w, r)
	}
}

func newTestDS(t *testing.T, f *fakeServer, mutate func(*models.Settings)) *Datasource {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s := &models.Settings{URL: srv.URL, AuthMode: models.AuthNone, Timeout: 5 * time.Second, RowLimit: 100}
	if mutate != nil {
		mutate(s)
	}
	c, err := drill.NewClient(drill.Config{BaseURL: s.URL, AuthMode: drill.AuthMode(s.AuthMode)}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return &Datasource{settings: s, client: c}
}

func dq(refID, js string) backend.DataQuery {
	return backend.DataQuery{
		RefID: refID, JSON: json.RawMessage(js), Interval: time.Minute,
		TimeRange: backend.TimeRange{From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
	}
}

func TestQueryTableWithMacros(t *testing.T) {
	f := &fakeServer{body: longResult}
	ds := newTestDS(t, f, nil)
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{
		dq("A", `{"rawSql":"SELECT * FROM t WHERE $__timeFilter(ts);"}`),
	}})
	r := resp.Responses["A"]
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	fr := r.Frames[0]
	want := "SELECT * FROM t WHERE ts >= TIMESTAMP '2024-01-01 00:00:00.000' AND ts <= TIMESTAMP '2024-01-02 00:00:00.000'"
	if fr.Meta.ExecutedQueryString != want {
		t.Fatalf("executed %q", fr.Meta.ExecutedQueryString)
	}
	if sent := f.lastSQL.Load().(string); !strings.HasSuffix(sent, "*/ "+want) {
		t.Fatalf("sent %q", sent)
	}
	if fr.RefID != "A" || fr.Meta.PreferredVisualization != data.VisTypeTable || len(fr.Fields) != 3 {
		t.Fatalf("frame %+v", fr)
	}
}

func TestQueryTimeSeriesLongToWide(t *testing.T) {
	ds := newTestDS(t, &fakeServer{body: longResult}, nil)
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{
		dq("A", `{"rawSql":"SELECT 1","format":"timeseries","fillMode":"value","fillValue":0}`),
	}})
	r := resp.Responses["A"]
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	fr := r.Frames[0]
	if fr.Meta.PreferredVisualization != data.VisTypeGraph || fr.Meta.ExecutedQueryString != "SELECT 1" {
		t.Fatalf("meta %+v", fr.Meta)
	}
	if fr.TimeSeriesSchema().Type != data.TimeSeriesTypeWide {
		t.Fatalf("not wide: %s", table(fr))
	}
	// time + one field per gender; null-time row dropped; sorted ascending.
	if len(fr.Fields) != 3 {
		t.Fatalf("fields: %s", table(fr))
	}
	if n, _ := fr.RowLen(); n != 2 {
		t.Fatalf("rows: %s", table(fr))
	}
	if first := fr.Fields[0].At(0).(time.Time); !first.Equal(time.UnixMilli(1703980800000)) {
		t.Fatalf("not sorted: %v", first)
	}
	// M had no value at the first time: filled with 0.
	for _, f := range fr.Fields[1:] {
		if f.Labels["gender"] == "M" {
			if v, _ := f.ConcreteAt(0); v != int64(0) && v != float64(0) {
				t.Fatalf("fill: %v", v)
			}
		}
	}
}

func TestQueryTimeSeriesNeedsTime(t *testing.T) {
	ds := newTestDS(t, &fakeServer{body: `{"columns":["a"],"metadata":["INT"],"rows":[{"a":1}],"queryState":"COMPLETED"}`}, nil)
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{dq("A", `{"rawSql":"x","format":"timeseries"}`)}})
	r := resp.Responses["A"]
	if r.Error == nil || !strings.Contains(r.Error.Error(), "time column") || r.ErrorSource != backend.ErrorSourceDownstream {
		t.Fatalf("got %v / %v", r.Error, r.ErrorSource)
	}
}

func TestQueryErrors(t *testing.T) {
	cases := []struct {
		name   string
		f      *fakeServer
		query  string
		mutate func(*models.Settings)
		want   string
		status backend.Status
	}{
		{"macro error", &fakeServer{body: longResult}, `{"rawSql":"SELECT $__bogus(x)"}`, nil, "$__bogus", backend.StatusBadRequest},
		{"bad query json", &fakeServer{body: longResult}, `{"rawSql":1}`, nil, "invalid query", backend.StatusBadRequest},
		{"auth needed", &fakeServer{status: http.StatusUnauthorized}, `{"rawSql":"x"}`, nil, "requires authentication", backend.StatusUnauthorized},
		{"auth rejected", &fakeServer{status: http.StatusUnauthorized}, `{"rawSql":"x"}`,
			func(s *models.Settings) { s.AuthMode = models.AuthBasic }, "rejected the credentials", backend.StatusUnauthorized},
		{"server error", &fakeServer{status: http.StatusInternalServerError}, `{"rawSql":"x"}`, nil, "HTTP 500", backend.StatusBadGateway},
		{"timeout", &fakeServer{body: longResult, delay: 2 * time.Second}, `{"rawSql":"x"}`,
			func(s *models.Settings) { s.Timeout = 100 * time.Millisecond }, "did not finish within", backend.StatusTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := newTestDS(t, c.f, c.mutate)
			resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{dq("A", c.query)}})
			r := resp.Responses["A"]
			if r.Error == nil || !strings.Contains(r.Error.Error(), c.want) {
				t.Fatalf("error %v, want %q", r.Error, c.want)
			}
			if r.Status != c.status || r.ErrorSource != backend.ErrorSourceDownstream {
				t.Fatalf("status %d source %s", r.Status, r.ErrorSource)
			}
		})
	}
}

func TestQueryEmptySQLIsNotAnError(t *testing.T) {
	ds := newTestDS(t, &fakeServer{body: longResult}, nil)
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{dq("A", `{"rawSql":"  "}`)}})
	if r := resp.Responses["A"]; r.Error != nil || len(r.Frames) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestRowLimitNotice(t *testing.T) {
	ds := newTestDS(t, &fakeServer{body: longResult}, func(s *models.Settings) { s.RowLimit = 4 })
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{dq("A", `{"rawSql":"x"}`)}})
	fr := resp.Responses["A"].Frames[0]
	if fr.Meta == nil || len(fr.Meta.Notices) != 1 || !strings.Contains(fr.Meta.Notices[0].Text, "limited to 4 rows") {
		t.Fatalf("notices %+v", fr.Meta)
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	f := &fakeServer{body: longResult, delay: 50 * time.Millisecond}
	ds := newTestDS(t, f, nil)
	var qs []backend.DataQuery
	for i := 0; i < 12; i++ {
		qs = append(qs, dq(string(rune('A'+i)), `{"rawSql":"x"}`))
	}
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: qs})
	if len(resp.Responses) != 12 {
		t.Fatalf("responses %d", len(resp.Responses))
	}
	if m := f.maxSeen.Load(); m > maxConcurrentQueries || m < 2 {
		t.Fatalf("max concurrent %d", m)
	}
}

func TestSettingsErrorReported(t *testing.T) {
	inst, err := NewDatasource(context.Background(), backend.DataSourceInstanceSettings{URL: "ftp://x", JSONData: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	ds := inst.(*Datasource)
	h, _ := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "Configuration problem") {
		t.Fatalf("%+v", h)
	}
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{dq("A", `{"rawSql":"x"}`)}})
	if resp.Responses["A"].Error == nil {
		t.Fatal("no error")
	}
}

func TestCheckHealth(t *testing.T) {
	ok := newTestDS(t, &fakeServer{body: `{"columns":["version"],"metadata":["VARCHAR"],"rows":[{"version":"1.22.0"}],"queryState":"COMPLETED"}`}, nil)
	h, _ := ok.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if h.Status != backend.HealthStatusOk || h.Message != "Connected to Apache Drill 1.22.0." {
		t.Fatalf("%+v", h)
	}

	down := newTestDS(t, &fakeServer{}, func(s *models.Settings) { s.URL = "http://127.0.0.1:1" })
	down.client, _ = drill.NewClient(drill.Config{BaseURL: "http://127.0.0.1:1"}, nil)
	h, _ = down.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
	if h.Status != backend.HealthStatusError || !strings.Contains(h.Message, "Cannot reach Drill at http://127.0.0.1:1") {
		t.Fatalf("%+v", h)
	}
}

func table(fr *data.Frame) string {
	s, err := fr.StringTable(10, 10)
	if err != nil {
		return err.Error()
	}
	return s
}

func TestDrillDiesMidQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/profiles.json" {
			_, _ = io.WriteString(w, `{"runningQueries":[]}`)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close() // the Drillbit went away before answering
	}))
	defer srv.Close()
	s := &models.Settings{URL: srv.URL, AuthMode: models.AuthNone, Timeout: 5 * time.Second, RowLimit: 100}
	c, _ := drill.NewClient(drill.Config{BaseURL: s.URL}, srv.Client())
	ds := &Datasource{settings: s, client: c}
	resp, _ := ds.QueryData(context.Background(), &backend.QueryDataRequest{Queries: []backend.DataQuery{dq("A", `{"rawSql":"x"}`)}})
	r := resp.Responses["A"]
	if r.Error == nil || !strings.Contains(r.Error.Error(), "closed the connection before answering") || r.ErrorSource != backend.ErrorSourceDownstream {
		t.Fatalf("%v / %s", r.Error, r.ErrorSource)
	}
}

func TestLogSafeDropsQueryText(t *testing.T) {
	msg := "PARSE ERROR: bad\n\nSQL Query: SELECT secret FROM t\n^"
	if got := logSafe(msg); got != "PARSE ERROR: bad" {
		t.Fatalf("got %q", got)
	}
	if got := logSafe(strings.Repeat("x", 400)); len(got) > 304 {
		t.Fatalf("not truncated: %d", len(got))
	}
}
