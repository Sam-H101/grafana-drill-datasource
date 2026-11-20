package macros

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var mc = Context{
	From:     time.Date(2024, 1, 2, 3, 4, 5, 678e6, time.UTC),
	To:       time.Date(2024, 1, 3, 0, 0, 0, 0, time.FixedZone("x", 3600)), // 2024-01-02 23:00 UTC
	Interval: 5 * time.Minute,
}

func TestExpand(t *testing.T) {
	const from = "TIMESTAMP '2024-01-02 03:04:05.678'"
	const to = "TIMESTAMP '2024-01-02 23:00:00.000'"
	cases := []struct {
		name, in, want string
	}{
		{"no macros", "SELECT 1", "SELECT 1"},
		{"timeFilter", "WHERE $__timeFilter(ts)", "WHERE ts >= " + from + " AND ts <= " + to},
		{"timeFilter nested", "WHERE $__timeFilter(CAST(COALESCE(a, b) AS TIMESTAMP))",
			"WHERE CAST(COALESCE(a, b) AS TIMESTAMP) >= " + from + " AND CAST(COALESCE(a, b) AS TIMESTAMP) <= " + to},
		{"timeFilter backtick col", "WHERE $__timeFilter(`my col`)", "WHERE `my col` >= " + from + " AND `my col` <= " + to},
		{"timeFrom timeTo", "SELECT $__timeFrom(), $__timeTo()", "SELECT " + from + ", " + to},
		{"spaces in parens", "SELECT $__timeFrom( )", "SELECT " + from},
		{"epoch filter", "WHERE $__unixEpochFilter(t)", "WHERE t >= 1704164645 AND t <= 1704236400"},
		{"epoch from to", "$__unixEpochFrom() $__unixEpochTo()", "1704164645 1704236400"},
		{"timeGroup", "SELECT $__timeGroup(ts, '1h')", "SELECT TO_TIMESTAMP(FLOOR(EXTRACT(EPOCH FROM ts) / 3600000.0) * 3600000 / 1000.0)"},
		{"timeGroup unquoted", "SELECT $__timeGroup(ts, 30s)", "SELECT TO_TIMESTAMP(FLOOR(EXTRACT(EPOCH FROM ts) / 30000.0) * 30000 / 1000.0)"},
		{"timeGroup auto interval", "SELECT $__timeGroup(ts, $__interval)", "SELECT TO_TIMESTAMP(FLOOR(EXTRACT(EPOCH FROM ts) / 300000.0) * 300000 / 1000.0)"},
		{"timeGroup nested col with comma", "SELECT $__timeGroup(CAST(COALESCE(a, b) AS TIMESTAMP), '1d')",
			"SELECT TO_TIMESTAMP(FLOOR(EXTRACT(EPOCH FROM CAST(COALESCE(a, b) AS TIMESTAMP)) / 86400000.0) * 86400000 / 1000.0)"},
		{"timeGroupAlias", "SELECT $__timeGroupAlias(ts, 1m)", "SELECT TO_TIMESTAMP(FLOOR(EXTRACT(EPOCH FROM ts) / 60000.0) * 60000 / 1000.0) AS `time`"},
		{"interval vars", "$__interval $__interval_ms", "5m 300000"},
		{"from to ms", "$__from $__to", "1704164645678 1704236400000"},
		{"in string literal", "SELECT '$__timeFilter(x)' FROM t", "SELECT '$__timeFilter(x)' FROM t"},
		{"escaped quote in literal", "SELECT 'it''s $__timeFrom()' x", "SELECT 'it''s $__timeFrom()' x"},
		{"in double-quoted", `SELECT "$__timeFrom()"`, `SELECT "$__timeFrom()"`},
		{"in backticks", "SELECT `$__timeFrom()`", "SELECT `$__timeFrom()`"},
		{"in line comment", "SELECT 1 -- $__timeFilter(x)\nFROM t", "SELECT 1 -- $__timeFilter(x)\nFROM t"},
		{"in block comment", "SELECT /* $__timeFrom() */ 1", "SELECT /* $__timeFrom() */ 1"},
		{"after comment", "/* c */ SELECT $__timeFrom()", "/* c */ SELECT " + from},
		{"string arg with comma and paren", "WHERE $__timeFilter(CONCAT('a,(', b))",
			"WHERE CONCAT('a,(', b) >= " + from + " AND CONCAT('a,(', b) <= " + to},
		{"unknown var left alone", "SELECT $__unknownVar", "SELECT $__unknownVar"},
		{"plain dollar", "SELECT '$' || $x FROM t", "SELECT '$' || $x FROM t"},
		{"two macros", "WHERE $__timeFilter(a) AND $__timeFilter(b)",
			"WHERE a >= " + from + " AND a <= " + to + " AND b >= " + from + " AND b <= " + to},
		{"CRLF line comment", "SELECT 1 -- $__timeFrom()\r\nFROM t WHERE $__timeFilter(a)",
			"SELECT 1 -- $__timeFrom()\r\nFROM t WHERE a >= " + from + " AND a <= " + to},
		{"unterminated comment", "SELECT 1 /* $__timeFrom()", "SELECT 1 /* $__timeFrom()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Expand(c.in, mc)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got.SQL != c.want {
				t.Fatalf("\n got: %s\nwant: %s", got.SQL, c.want)
			}
		})
	}
}

func TestExpandErrors(t *testing.T) {
	cases := []struct {
		name, in, macro, msg string
	}{
		{"unknown macro", "SELECT $__nope(x)", "nope", "unknown macro"},
		{"missing close", "WHERE $__timeFilter(ts", "timeFilter", "missing closing parenthesis"},
		{"too many args", "WHERE $__timeFilter(a, b)", "timeFilter", "expects 1"},
		{"empty arg", "WHERE $__timeFilter()", "timeFilter", "expects 1"},
		{"no parens", "WHERE $__timeFilter", "timeFilter", "missing parentheses"},
		{"timeFrom with arg", "SELECT $__timeFrom(x)", "timeFrom", "expects 0"},
		{"bad interval", "SELECT $__timeGroup(ts, 'soon')", "timeGroup", "invalid interval"},
		{"zero interval", "SELECT $__timeGroup(ts, 0s)", "timeGroup", "positive"},
		{"timeGroup one arg", "SELECT $__timeGroup(ts)", "timeGroup", "expects"},
		{"bad fill", "SELECT $__timeGroup(ts, 1m, maybe)", "timeGroup", "fill must be"},
		{"interval with parens", "SELECT $__interval(1)", "interval", "no arguments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Expand(c.in, mc)
			var me *Error
			if !errors.As(err, &me) {
				t.Fatalf("want *Error, got %v", err)
			}
			if me.Macro != c.macro || !strings.Contains(me.Msg, c.msg) {
				t.Fatalf("got %q / %q", me.Macro, me.Msg)
			}
			if !strings.Contains(err.Error(), "$__"+c.macro) {
				t.Fatalf("message does not name the macro: %s", err)
			}
		})
	}
}

func TestTimeGroupFill(t *testing.T) {
	for in, want := range map[string]Fill{
		"$__timeGroup(ts, 1m, NULL)":       {Mode: "null"},
		"$__timeGroup(ts, 1m, previous)":   {Mode: "previous"},
		"$__timeGroup(ts, 1m, 0)":          {Mode: "value", Value: 0},
		"$__timeGroupAlias(ts, 1m, '2.5')": {Mode: "value", Value: 2.5},
	} {
		res, err := Expand(in, mc)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if res.Fill == nil || *res.Fill != want {
			t.Errorf("%s: fill %+v, want %+v", in, res.Fill, want)
		}
	}
	res, _ := Expand("$__timeGroup(ts, 1m)", mc)
	if res.Fill != nil {
		t.Errorf("unexpected fill %+v", res.Fill)
	}
}
