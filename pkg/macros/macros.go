// Package macros expands Grafana time macros in Drill SQL. It is pure string
// processing: no I/O. Macro names follow Grafana's other SQL data sources.
//
// Macros inside string literals, quoted identifiers and comments are left
// alone. Arguments may contain nested parentheses, quoted commas and strings.
package macros

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
)

// Context is what macros expand against.
type Context struct {
	From, To time.Time
	// Interval is the panel's suggested interval ($__interval).
	Interval time.Duration
}

// Fill is a fill mode requested through $__timeGroup's third argument.
type Fill struct {
	Mode  string // "null", "previous" or "value"
	Value float64
}

// Result is the expanded SQL plus anything the macros asked for.
type Result struct {
	SQL  string
	Fill *Fill
}

// Error is a macro the user wrote wrongly.
type Error struct {
	Macro string
	Msg   string
}

func (e *Error) Error() string { return fmt.Sprintf("macro $__%s: %s", e.Macro, e.Msg) }

const tsLayout = "2006-01-02 15:04:05.000"

// Expand replaces every macro in sql.
func Expand(sql string, mc Context) (*Result, error) {
	res := &Result{}
	var b strings.Builder
	b.Grow(len(sql) + 64)

	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			end := skipQuoted(sql, i, c)
			b.WriteString(sql[i:end])
			i = end
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			end := strings.IndexByte(sql[i:], '\n')
			if end < 0 {
				end = len(sql) - i
			}
			b.WriteString(sql[i : i+end])
			i += end
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				b.WriteString(sql[i:])
				i = len(sql)
			} else {
				b.WriteString(sql[i : i+2+end+2])
				i += 2 + end + 2
			}
		case c == '$' && strings.HasPrefix(sql[i:], "$__"):
			name, nameEnd := readName(sql, i+3)
			if name == "" {
				b.WriteByte(c)
				i++
				continue
			}
			var args []string
			hasParens := nameEnd < len(sql) && sql[nameEnd] == '('
			end := nameEnd
			if hasParens {
				var err error
				args, end, err = readArgs(sql, nameEnd)
				if err != nil {
					return nil, &Error{Macro: name, Msg: err.Error()}
				}
			}
			out, handled, err := expandOne(name, args, hasParens, mc, res)
			if err != nil {
				return nil, err
			}
			if !handled {
				// Not ours (for example a Grafana variable that was left
				// unexpanded): keep the text as written.
				b.WriteString(sql[i:nameEnd])
				i = nameEnd
				continue
			}
			b.WriteString(out)
			i = end
		default:
			b.WriteByte(c)
			i++
		}
	}
	res.SQL = b.String()
	return res, nil
}

func expandOne(name string, args []string, hasParens bool, mc Context, res *Result) (string, bool, error) {
	need := func(n int) error {
		if !hasParens {
			return &Error{Macro: name, Msg: "missing parentheses"}
		}
		if len(args) != n {
			return &Error{Macro: name, Msg: fmt.Sprintf("expects %d argument(s), got %d", n, len(args))}
		}
		for _, a := range args {
			if a == "" {
				return &Error{Macro: name, Msg: "empty argument"}
			}
		}
		return nil
	}

	switch name {
	case "timeFilter":
		if err := need(1); err != nil {
			return "", true, err
		}
		col := args[0]
		return fmt.Sprintf("%s >= %s AND %s <= %s", col, tsLiteral(mc.From), col, tsLiteral(mc.To)), true, nil
	case "timeFrom":
		if err := need(0); err != nil {
			return "", true, err
		}
		return tsLiteral(mc.From), true, nil
	case "timeTo":
		if err := need(0); err != nil {
			return "", true, err
		}
		return tsLiteral(mc.To), true, nil
	case "unixEpochFilter":
		if err := need(1); err != nil {
			return "", true, err
		}
		col := args[0]
		return fmt.Sprintf("%s >= %d AND %s <= %d", col, mc.From.Unix(), col, mc.To.Unix()), true, nil
	case "unixEpochFrom":
		if err := need(0); err != nil {
			return "", true, err
		}
		return strconv.FormatInt(mc.From.Unix(), 10), true, nil
	case "unixEpochTo":
		if err := need(0); err != nil {
			return "", true, err
		}
		return strconv.FormatInt(mc.To.Unix(), 10), true, nil
	case "timeGroup", "timeGroupAlias":
		if !hasParens || len(args) < 2 || len(args) > 3 {
			return "", true, &Error{Macro: name, Msg: "expects (column, interval) or (column, interval, fill)"}
		}
		if args[0] == "" {
			return "", true, &Error{Macro: name, Msg: "empty column argument"}
		}
		ms, err := intervalMillis(args[1], mc.Interval)
		if err != nil {
			return "", true, &Error{Macro: name, Msg: err.Error()}
		}
		if len(args) == 3 {
			fill, err := parseFill(args[2])
			if err != nil {
				return "", true, &Error{Macro: name, Msg: err.Error()}
			}
			res.Fill = fill
		}
		// EXTRACT(EPOCH ...) gives epoch ms; dividing by a decimal makes FLOOR
		// round pre-1970 values down (integer division truncates toward zero).
		// TIMESTAMPADD is avoided: it overflows past ~24 days.
		expr := fmt.Sprintf("TO_TIMESTAMP(FLOOR(EXTRACT(EPOCH FROM %s) / %d.0) * %d / 1000.0)", args[0], ms, ms)
		if name == "timeGroupAlias" {
			expr += " AS `time`"
		}
		return expr, true, nil
	case "interval":
		if hasParens {
			return "", true, &Error{Macro: name, Msg: "takes no arguments"}
		}
		return gtime.FormatInterval(mc.Interval), true, nil
	case "interval_ms":
		if hasParens {
			return "", true, &Error{Macro: name, Msg: "takes no arguments"}
		}
		return strconv.FormatInt(mc.Interval.Milliseconds(), 10), true, nil
	case "from":
		return strconv.FormatInt(mc.From.UnixMilli(), 10), !hasParens, nil
	case "to":
		return strconv.FormatInt(mc.To.UnixMilli(), 10), !hasParens, nil
	}
	if hasParens {
		return "", true, &Error{Macro: name, Msg: "unknown macro"}
	}
	return "", false, nil
}

// tsLiteral renders a Drill timestamp literal in UTC (docs/drill-rest-notes.md
// section 7: Drill timestamps are read as UTC).
func tsLiteral(t time.Time) string {
	return "TIMESTAMP '" + t.UTC().Format(tsLayout) + "'"
}

func intervalMillis(arg string, auto time.Duration) (int64, error) {
	s := unquote(arg)
	var d time.Duration
	switch s {
	case "$__interval", "auto":
		d = auto
	default:
		var err error
		d, err = gtime.ParseDuration(s)
		if err != nil {
			if ms, convErr := strconv.ParseInt(s, 10, 64); convErr == nil {
				d = time.Duration(ms) * time.Millisecond
			} else {
				return 0, fmt.Errorf("invalid interval %q (use values like 30s, 5m, 1h)", s)
			}
		}
	}
	ms := d.Milliseconds()
	if ms <= 0 {
		return 0, fmt.Errorf("interval must be positive")
	}
	return ms, nil
}

func parseFill(arg string) (*Fill, error) {
	s := unquote(arg)
	switch strings.ToLower(s) {
	case "null":
		return &Fill{Mode: "null"}, nil
	case "previous":
		return &Fill{Mode: "previous"}, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("fill must be NULL, previous or a number, got %q", s)
	}
	return &Fill{Mode: "value", Value: v}, nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func readName(s string, i int) (string, int) {
	j := i
	for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
		j++
	}
	return s[i:j], j
}

// skipQuoted returns the index after the quoted run starting at i. A doubled
// quote character is an escaped quote. An unterminated run ends at len(s).
func skipQuoted(s string, i int, q byte) int {
	j := i + 1
	for j < len(s) {
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return len(s)
}

// readArgs parses "(a, f(b, c), 'x,y')" starting at the '(' at index open.
// It returns trimmed arguments and the index after the closing ')'.
func readArgs(s string, open int) ([]string, int, error) {
	var args []string
	depth := 0
	start := open + 1
	for i := open; i < len(s); {
		switch c := s[i]; c {
		case '\'', '"', '`':
			i = skipQuoted(s, i, c)
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				last := strings.TrimSpace(s[start:i])
				if last != "" || len(args) > 0 {
					args = append(args, last)
				}
				return args, i + 1, nil
			}
		case ',':
			if depth == 1 {
				args = append(args, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
		i++
	}
	return nil, 0, fmt.Errorf("missing closing parenthesis")
}
