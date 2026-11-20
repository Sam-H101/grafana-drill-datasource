package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

type sentResponse struct {
	status int
	body   []byte
}

func (s *sentResponse) Send(r *backend.CallResourceResponse) error {
	s.status, s.body = r.Status, r.Body
	return nil
}

func callResource(t *testing.T, ds *Datasource, path string) sentResponse {
	t.Helper()
	var out sentResponse
	err := ds.CallResource(context.Background(), &backend.CallResourceRequest{Method: http.MethodGet, Path: strings.SplitN(path, "?", 2)[0], URL: path}, &out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestResourceTablesEscapesAndCaches(t *testing.T) {
	f := &fakeServer{body: `{"columns":["name","type"],"metadata":["VARCHAR","VARCHAR"],"rows":[{"name":"t1","type":"TABLE"},{"name":"t2","type":"VIEW"}],"queryState":"COMPLETED"}`}
	ds := newTestDS(t, f, nil)

	r := callResource(t, ds, "tables?schema=dfs.it%27s")
	if r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	var names []string
	if err := json.Unmarshal(r.body, &names); err != nil || len(names) != 2 || names[0] != "t1" {
		t.Fatalf("body %s", r.body)
	}
	sent := f.lastSQL.Load().(string)
	if !strings.Contains(sent, "WHERE TABLE_SCHEMA = 'dfs.it''s'") {
		t.Fatalf("schema not escaped: %s", sent)
	}

	// Second call is served from the cache.
	f.lastSQL.Store("")
	callResource(t, ds, "tables?schema=dfs.it%27s")
	if got := f.lastSQL.Load().(string); got != "" {
		t.Fatalf("cache miss: %s", got)
	}
}

func TestResourceColumnsHaveTypes(t *testing.T) {
	f := &fakeServer{body: `{"columns":["name","type"],"metadata":["VARCHAR","VARCHAR"],"rows":[{"name":"a","type":"INTEGER"}],"queryState":"COMPLETED"}`}
	ds := newTestDS(t, f, nil)
	r := callResource(t, ds, "columns?schema=sys&table=version")
	var cols []Column
	if err := json.Unmarshal(r.body, &cols); err != nil || len(cols) != 1 || cols[0] != (Column{Name: "a", Type: "INTEGER"}) {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestResourceValidation(t *testing.T) {
	ds := newTestDS(t, &fakeServer{body: longResult}, nil)
	for _, p := range []string{"tables", "tables?schema=", "columns?schema=x", "tables?schema=" + strings.Repeat("a", 300)} {
		if r := callResource(t, ds, p); r.status != http.StatusBadRequest {
			t.Errorf("%s: status %d", p, r.status)
		}
	}
	if r := callResource(t, ds, "nope"); r.status != http.StatusNotFound {
		t.Errorf("unknown path: %d", r.status)
	}
}

func TestResourceErrorsAreJSON(t *testing.T) {
	ds := newTestDS(t, &fakeServer{status: http.StatusInternalServerError}, nil)
	r := callResource(t, ds, "schemas")
	var body map[string]string
	if r.status != http.StatusBadGateway || json.Unmarshal(r.body, &body) != nil || !strings.Contains(body["message"], "HTTP 500") {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestMetadataCacheBounded(t *testing.T) {
	var c metadataCache
	for i := 0; i < metadataMaxItems+10; i++ {
		c.put(string(rune(i+1000)), nil)
	}
	if len(c.entries) > metadataMaxItems {
		t.Fatalf("cache grew to %d", len(c.entries))
	}
}
