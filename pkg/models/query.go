package models

import (
	"encoding/json"
	"errors"
	"strings"
)

// Format is the shape the query result is returned in.
type Format string

const (
	FormatTable      Format = "table"
	FormatTimeSeries Format = "timeseries"
)

// FillMode controls how gaps are filled when a long result is turned wide.
type FillMode string

const (
	FillNone     FillMode = ""
	FillNull     FillMode = "null"
	FillPrevious FillMode = "previous"
	FillValue    FillMode = "value"
)

// QueryModel is the per-query JSON the frontend sends.
type QueryModel struct {
	RawSQL        string   `json:"rawSql"`
	Format        Format   `json:"format"`
	DefaultSchema string   `json:"defaultSchema"`
	FillMode      FillMode `json:"fillMode"`
	FillValue     float64  `json:"fillValue"`
}

// ErrInvalidQuery wraps query model validation failures.
var ErrInvalidQuery = errors.New("invalid query")

// ParseQuery decodes and validates a query model. An empty format means table.
func ParseQuery(raw json.RawMessage) (*QueryModel, error) {
	var q QueryModel
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, errors.Join(ErrInvalidQuery, errors.New("could not parse the query JSON"))
	}
	q.DefaultSchema = strings.TrimSpace(q.DefaultSchema)
	switch q.Format {
	case "":
		q.Format = FormatTable
	case FormatTable, FormatTimeSeries:
	default:
		return nil, errors.Join(ErrInvalidQuery, errors.New("format must be table or timeseries"))
	}
	switch q.FillMode {
	case FillNone, FillNull, FillPrevious, FillValue:
	default:
		return nil, errors.Join(ErrInvalidQuery, errors.New("fill mode must be null, previous or value"))
	}
	return &q, nil
}
