package drill

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
)

type kind int

const (
	kindString kind = iota
	kindBool
	kindInt
	kindFloat
	kindTime      // DATE, TIMESTAMP: epoch ms, read as UTC
	kindTimeOfDay // TIME: ms since midnight, shown as text
	kindJSON      // MAP, LIST, unknown: raw JSON text
)

// BaseType strips precision and scale from a Drill type name:
// "VARDECIMAL(5, 2)" -> "VARDECIMAL" (notes section 2).
func BaseType(t string) string {
	t = strings.ToUpper(strings.TrimSpace(t))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

func kindOf(drillType string) kind {
	switch t := BaseType(drillType); {
	case t == "BIT" || t == "BOOLEAN":
		return kindBool
	case t == "TINYINT" || t == "SMALLINT" || t == "INT" || t == "INTEGER" || t == "BIGINT" ||
		t == "UINT1" || t == "UINT2" || t == "UINT4" || t == "UINT8":
		return kindInt
	case t == "FLOAT4" || t == "FLOAT8" || t == "FLOAT" || t == "DOUBLE" || t == "REAL" ||
		t == "VARDECIMAL" || strings.HasPrefix(t, "DECIMAL"):
		return kindFloat
	case t == "DATE" || t == "TIMESTAMP" || t == "TIMESTAMPTZ":
		return kindTime
	case t == "TIME" || t == "TIMETZ":
		return kindTimeOfDay
	case t == "VARCHAR" || t == "CHAR" || t == "VAR16CHAR" || t == "VARBINARY" || t == "BINARY" ||
		strings.HasPrefix(t, "INTERVAL"):
		return kindString
	default: // MAP, LIST, DICT, UNION, LATE and anything new
		return kindJSON
	}
}

// IsTimeType reports whether a Drill type converts to a time field.
func IsTimeType(drillType string) bool { return kindOf(drillType) == kindTime }

// ToFrame converts a result to a data frame. Every field is nullable. A value
// that does not parse as its column's type becomes null and adds one warning
// notice per column; it never fails the query.
func ToFrame(name string, res *Result) *data.Frame {
	frame := data.NewFrame(name)
	for i, col := range res.Columns {
		var values []json.RawMessage
		if i < len(res.Values) {
			values = res.Values[i]
		}
		drillType := ""
		if i < len(res.Types) {
			drillType = res.Types[i]
		}
		k := kindOf(drillType)
		// A repeated column is labelled with its element type but carries
		// arrays (notes section 3): keep such columns as JSON text.
		if k != kindJSON && hasComposite(values) {
			k = kindJSON
		}
		field, bad := buildField(col, k, values)
		frame.Fields = append(frame.Fields, field)
		if bad > 0 {
			frame.AppendNotices(data.Notice{
				Severity: data.NoticeSeverityWarning,
				Text:     fmt.Sprintf("Column %q: %d value(s) could not be read as %s and are shown as empty.", col, bad, BaseType(drillType)),
			})
		}
	}
	return frame
}

func hasComposite(values []json.RawMessage) bool {
	for _, v := range values {
		if len(v) > 0 && (v[0] == '[' || v[0] == '{') {
			return true
		}
	}
	return false
}

func buildField(name string, k kind, values []json.RawMessage) (*data.Field, int) {
	n := len(values)
	bad := 0
	switch k {
	case kindBool:
		out := make([]*bool, n)
		for i, v := range values {
			if v == nil {
				continue
			}
			if b, ok := parseBool(v); ok {
				out[i] = &b
			} else {
				bad++
			}
		}
		return data.NewField(name, nil, out), bad
	case kindInt:
		out := make([]*int64, n)
		for i, v := range values {
			if v == nil {
				continue
			}
			if x, ok := parseInt(v); ok {
				out[i] = &x
			} else {
				bad++
			}
		}
		return data.NewField(name, nil, out), bad
	case kindFloat:
		out := make([]*float64, n)
		for i, v := range values {
			if v == nil {
				continue
			}
			if x, ok := parseFloat(v); ok {
				out[i] = &x
			} else {
				bad++
			}
		}
		return data.NewField(name, nil, out), bad
	case kindTime:
		out := make([]*time.Time, n)
		for i, v := range values {
			if v == nil {
				continue
			}
			if t, ok := parseTime(v); ok {
				out[i] = &t
			} else {
				bad++
			}
		}
		return data.NewField(name, nil, out), bad
	case kindTimeOfDay:
		out := make([]*string, n)
		for i, v := range values {
			if v == nil {
				continue
			}
			if s, ok := parseTimeOfDay(v); ok {
				out[i] = &s
			} else {
				bad++
			}
		}
		return data.NewField(name, nil, out), bad
	case kindJSON:
		out := make([]*string, n)
		for i, v := range values {
			if v == nil {
				continue
			}
			s := compactJSON(v)
			out[i] = &s
		}
		return data.NewField(name, nil, out), 0
	default:
		out := make([]*string, n)
		for i, v := range values {
			if v == nil {
				continue
			}
			s := textOf(v)
			out[i] = &s
		}
		return data.NewField(name, nil, out), 0
	}
}

// scalarText returns the text of a JSON scalar: the unquoted string, or the
// literal for numbers and booleans.
func scalarText(v json.RawMessage) (string, bool) {
	if len(v) == 0 {
		return "", false
	}
	if v[0] == '"' {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", false
		}
		return strings.TrimSpace(s), true
	}
	if v[0] == '[' || v[0] == '{' {
		return "", false
	}
	return string(v), true
}

func parseBool(v json.RawMessage) (bool, bool) {
	s, ok := scalarText(v)
	if !ok {
		return false, false
	}
	switch strings.ToLower(s) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}

func parseInt(v json.RawMessage) (int64, bool) {
	s, ok := scalarText(v)
	if !ok {
		return 0, false
	}
	if x, err := strconv.ParseInt(s, 10, 64); err == nil {
		return x, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 {
		return 0, false
	}
	return int64(f), true
}

func parseFloat(v json.RawMessage) (float64, bool) {
	s, ok := scalarText(v)
	if !ok {
		return 0, false
	}
	// strconv accepts "NaN" and "Infinity", which is how Drill sends them.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return f, true // ±Inf for out-of-range decimals
		}
		return 0, false
	}
	return f, true
}

var timeLayouts = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02",
}

func parseTime(v json.RawMessage) (time.Time, bool) {
	if len(v) > 0 && v[0] != '"' {
		ms, ok := parseInt(v)
		if !ok {
			return time.Time{}, false
		}
		return time.UnixMilli(ms).UTC(), true
	}
	s, ok := scalarText(v)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), true
	}
	return time.Time{}, false
}

func parseTimeOfDay(v json.RawMessage) (string, bool) {
	if len(v) > 0 && v[0] != '"' {
		ms, ok := parseInt(v)
		if !ok || ms < 0 || ms >= 24*60*60*1000 {
			return "", false
		}
		return time.UnixMilli(ms).UTC().Format("15:04:05.000"), true
	}
	return scalarText(v)
}

func textOf(v json.RawMessage) string {
	if s, ok := scalarText(v); ok {
		return s
	}
	return compactJSON(v)
}

func compactJSON(v json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		return string(v)
	}
	return buf.String()
}
