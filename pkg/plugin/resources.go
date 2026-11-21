package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/drill"
)

var _ backend.CallResourceHandler = (*Datasource)(nil)

const (
	metadataTTL       = time.Minute
	metadataMaxItems  = 256
	metadataRowLimit  = 5000
	metadataTimeout   = 20 * time.Second
	maxIdentifierSize = 256
)

// CallResource serves schema metadata for the query editor:
//
//	GET /schemas
//	GET /tables?schema=dfs.tmp
//	GET /columns?schema=dfs.tmp&table=t
//
// Each returns a JSON array. File-based schemas often list no tables; an
// empty array is a normal answer.
func (d *Datasource) CallResource(ctx context.Context, req *backend.CallResourceRequest, sender backend.CallResourceResponseSender) error {
	return d.resourceHandler().CallResource(ctx, req, sender)
}

func (d *Datasource) resourceHandler() backend.CallResourceHandler {
	d.resOnce.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /schemas", d.handleSchemas)
		mux.HandleFunc("GET /tables", d.handleTables)
		mux.HandleFunc("GET /columns", d.handleColumns)
		d.resHandler = httpadapter.New(mux)
	})
	return d.resHandler
}

// Column is one entry of the /columns response.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (d *Datasource) handleSchemas(w http.ResponseWriter, r *http.Request) {
	d.serveMetadata(w, r, "schemas",
		"SELECT SCHEMA_NAME AS name, '' AS type FROM INFORMATION_SCHEMA.SCHEMATA ORDER BY SCHEMA_NAME", false)
}

func (d *Datasource) handleTables(w http.ResponseWriter, r *http.Request) {
	schema, ok := identifierParam(w, r, "schema")
	if !ok {
		return
	}
	d.serveMetadata(w, r, "tables\x00"+schema,
		"SELECT TABLE_NAME AS name, TABLE_TYPE AS type FROM INFORMATION_SCHEMA.`TABLES` WHERE TABLE_SCHEMA = "+
			sqlString(schema)+" ORDER BY TABLE_NAME", false)
}

func (d *Datasource) handleColumns(w http.ResponseWriter, r *http.Request) {
	schema, ok := identifierParam(w, r, "schema")
	if !ok {
		return
	}
	table, ok := identifierParam(w, r, "table")
	if !ok {
		return
	}
	d.serveMetadata(w, r, "columns\x00"+schema+"\x00"+table,
		"SELECT COLUMN_NAME AS name, DATA_TYPE AS type FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = "+
			sqlString(schema)+" AND TABLE_NAME = "+sqlString(table)+" ORDER BY ORDINAL_POSITION", true)
}

// serveMetadata runs a metadata query (cached per instance) and writes the
// result as JSON: an array of names, or of {name, type} when withTypes is set.
func (d *Datasource) serveMetadata(w http.ResponseWriter, r *http.Request, key, sql string, withTypes bool) {
	if d.settingsErr != nil {
		writeJSONError(w, http.StatusBadRequest, d.explain(d.settingsErr).message)
		return
	}
	cols, err := d.metadata(r.Context(), key, sql)
	if err != nil {
		p := d.explain(err)
		backend.Logger.Warn("Drill metadata query failed", "error", logSafe(p.message))
		writeJSONError(w, http.StatusBadGateway, p.message)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if withTypes {
		_ = json.NewEncoder(w).Encode(cols)
		return
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	_ = json.NewEncoder(w).Encode(names)
}

type cacheEntry struct {
	at   time.Time
	cols []Column
}

// metadataCache is a small TTL cache, safe for concurrent use.
type metadataCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

func (c *metadataCache) get(key string) ([]Column, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.at) > metadataTTL {
		return nil, false
	}
	return e.cols, true
}

func (c *metadataCache) put(key string, cols []Column) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	if len(c.entries) >= metadataMaxItems {
		// Drop expired entries; if still full, start over.
		for k, e := range c.entries {
			if time.Since(e.at) > metadataTTL {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= metadataMaxItems {
			c.entries = map[string]cacheEntry{}
		}
	}
	c.entries[key] = cacheEntry{at: time.Now(), cols: cols}
}

func (d *Datasource) metadata(ctx context.Context, key, sql string) ([]Column, error) {
	if cols, ok := d.cache.get(key); ok {
		return cols, nil
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	res, err := d.client.Query(ctx, drill.QueryRequest{SQL: sql, AutoLimit: metadataRowLimit})
	if err != nil {
		return nil, err
	}
	cols := make([]Column, 0, res.NumRows())
	for i := 0; i < res.NumRows(); i++ {
		var c Column
		if len(res.Values) > 0 && res.Values[0][i] != nil {
			_ = json.Unmarshal(res.Values[0][i], &c.Name)
		}
		if len(res.Values) > 1 && res.Values[1][i] != nil {
			_ = json.Unmarshal(res.Values[1][i], &c.Type)
		}
		if c.Name != "" {
			cols = append(cols, c)
		}
	}
	d.cache.put(key, cols)
	return cols, nil
}

var errBadIdentifier = errors.New("invalid identifier")

func identifierParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" || len(v) > maxIdentifierSize || strings.ContainsAny(v, "\x00\n\r") {
		writeJSONError(w, http.StatusBadRequest, errBadIdentifier.Error()+": "+name)
		return "", false
	}
	return v, true
}

// sqlString quotes a value as a SQL string literal.
func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}
