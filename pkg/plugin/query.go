package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/drill"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/macros"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/models"
)

// maxConcurrentQueries bounds how many queries of one request run at once.
const maxConcurrentQueries = 5

// QueryData runs the queries of one request concurrently (bounded) and
// returns one response per RefID.
func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	resp := backend.NewQueryDataResponse()
	if d.settingsErr != nil {
		p := d.explain(d.settingsErr)
		for _, q := range req.Queries {
			resp.Responses[q.RefID] = p.response()
		}
		return resp, nil
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxConcurrentQueries)
	)
	for _, q := range req.Queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := d.query(ctx, q)
			mu.Lock()
			resp.Responses[q.RefID] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return resp, nil
}

func (d *Datasource) query(ctx context.Context, q backend.DataQuery) backend.DataResponse {
	qm, err := models.ParseQuery(q.JSON)
	if err != nil {
		return d.explain(err).response()
	}
	if strings.TrimSpace(qm.RawSQL) == "" {
		return backend.DataResponse{}
	}

	expanded, err := macros.Expand(qm.RawSQL, macros.Context{From: q.TimeRange.From, To: q.TimeRange.To, Interval: q.Interval})
	if err != nil {
		return d.explain(err).response()
	}
	sql := drill.CleanSQL(expanded.SQL)
	schema := qm.DefaultSchema
	if schema == "" {
		schema = d.settings.DefaultSchema
	}

	frame, err := d.runSQL(ctx, sql, schema)
	if err != nil {
		p := d.explain(err)
		// Never log SQL above debug level: it can contain sensitive literals.
		backend.Logger.Warn("Drill query failed", "refId", q.RefID, "status", int(p.status), "error", logSafe(p.message))
		backend.Logger.Debug("Failed Drill query", "refId", q.RefID, "sql", sql, "error", p.message)
		return p.response()
	}
	frame.RefID = q.RefID

	if qm.Format == models.FormatTimeSeries {
		fill := fillFromQuery(qm)
		if expanded.Fill != nil {
			fill = toSDKFill(expanded.Fill)
		}
		frame, err = toTimeSeries(frame, fill)
		if err != nil {
			return problem{backend.StatusBadRequest, err.Error(), true}.response()
		}
	}
	return backend.DataResponse{Frames: data.Frames{frame}}
}

// runSQL runs one statement with the data source's timeout and row limit and
// returns a table frame with the executed SQL in its metadata.
func (d *Datasource) runSQL(ctx context.Context, sql, schema string) (*data.Frame, error) {
	ctx, cancel := context.WithTimeout(ctx, d.settings.Timeout)
	defer cancel()

	start := time.Now()
	res, err := d.client.Query(ctx, drill.QueryRequest{SQL: sql, AutoLimit: d.settings.RowLimit, DefaultSchema: schema})
	if err != nil {
		return nil, err
	}
	backend.Logger.Debug("Drill query finished", "queryId", res.QueryID, "rows", res.NumRows(), "elapsed", time.Since(start).String())

	frame := drill.ToFrame("", res)
	frame.SetMeta(mergeMeta(frame.Meta, &data.FrameMeta{
		ExecutedQueryString:    sql,
		PreferredVisualization: data.VisTypeTable,
	}))
	if res.NumRows() >= d.settings.RowLimit {
		frame.AppendNotices(data.Notice{
			Severity: data.NoticeSeverityWarning,
			Text:     fmt.Sprintf("Results are limited to %d rows by the data source's row limit.", d.settings.RowLimit),
		})
	}
	return frame, nil
}

func mergeMeta(existing, add *data.FrameMeta) *data.FrameMeta {
	if existing == nil {
		return add
	}
	existing.ExecutedQueryString = add.ExecutedQueryString
	existing.PreferredVisualization = add.PreferredVisualization
	return existing
}

func fillFromQuery(qm *models.QueryModel) *data.FillMissing {
	switch qm.FillMode {
	case models.FillNull:
		return &data.FillMissing{Mode: data.FillModeNull}
	case models.FillPrevious:
		return &data.FillMissing{Mode: data.FillModePrevious}
	case models.FillValue:
		return &data.FillMissing{Mode: data.FillModeValue, Value: qm.FillValue}
	}
	return nil
}

func toSDKFill(f *macros.Fill) *data.FillMissing {
	switch f.Mode {
	case "previous":
		return &data.FillMissing{Mode: data.FillModePrevious}
	case "value":
		return &data.FillMissing{Mode: data.FillModeValue, Value: f.Value}
	default:
		return &data.FillMissing{Mode: data.FillModeNull}
	}
}

var errNoTimeColumn = errors.New("time series format needs a time column: select a TIMESTAMP or DATE column, for example with $__timeGroupAlias(column, $__interval)")

// toTimeSeries turns a table frame into a time series: it picks the time
// column (one named "time", else the first time-typed one), drops rows with a
// null time, sorts by time and, for long results with label columns, converts
// long to wide (one series per label combination).
func toTimeSeries(frame *data.Frame, fill *data.FillMissing) (*data.Frame, error) {
	timeIdx := -1
	for i, f := range frame.Fields {
		if f.Type().Time() {
			if strings.EqualFold(f.Name, "time") {
				timeIdx = i
				break
			}
			if timeIdx < 0 {
				timeIdx = i
			}
		}
	}
	if timeIdx < 0 {
		return nil, errNoTimeColumn
	}

	n, _ := frame.RowLen()
	times := make([]time.Time, 0, n)
	rows := make([]int, 0, n)
	tf := frame.Fields[timeIdx]
	for i := 0; i < n; i++ {
		if t, ok := tf.ConcreteAt(i); ok {
			times = append(times, t.(time.Time))
			rows = append(rows, i)
		}
	}
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return times[order[a]].Before(times[order[b]]) })

	sorted := data.NewFrame(frame.Name)
	sorted.RefID = frame.RefID
	sortedTimes := make([]time.Time, len(order))
	for i, o := range order {
		sortedTimes[i] = times[o]
	}
	sorted.Fields = append(sorted.Fields, data.NewField(tf.Name, nil, sortedTimes))
	for i, f := range frame.Fields {
		if i == timeIdx {
			continue
		}
		nf := data.NewFieldFromFieldType(f.Type(), len(order))
		nf.Name = f.Name
		nf.Labels = f.Labels
		for j, o := range order {
			nf.Set(j, f.At(rows[o]))
		}
		sorted.Fields = append(sorted.Fields, nf)
	}

	meta := frame.Meta
	out := sorted
	if sorted.TimeSeriesSchema().Type == data.TimeSeriesTypeLong {
		wide, err := data.LongToWide(sorted, fill)
		if err != nil {
			return nil, fmt.Errorf("could not build time series: %w", err)
		}
		out = wide
	}
	out.RefID = frame.RefID
	if meta == nil {
		meta = &data.FrameMeta{}
	}
	meta.PreferredVisualization = data.VisTypeGraph
	out.SetMeta(meta)
	return out, nil
}
