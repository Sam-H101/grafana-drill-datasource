package sqlguard

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// cases is shared with src/safety.test.ts so the Go guard and the query
// editor's warning cannot drift apart.
type cases struct {
	Classify []struct {
		SQL     string `json:"sql"`
		Kind    Kind   `json:"kind"`
		Keyword string `json:"keyword"`
	} `json:"classify"`
	Multiple []struct {
		SQL      string `json:"sql"`
		Multiple bool   `json:"multiple"`
	} `json:"multiple"`
	Check []struct {
		SQL     string `json:"sql"`
		DDL     bool   `json:"ddl"`
		DML     bool   `json:"dml"`
		Options bool   `json:"options"`
		Allowed bool   `json:"allowed"`
	} `json:"check"`
}

func loadCases(t *testing.T) cases {
	t.Helper()
	raw, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c cases
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Classify) == 0 || len(c.Multiple) == 0 || len(c.Check) == 0 {
		t.Fatal("testdata/cases.json is missing a section")
	}
	return c
}

func TestClassify(t *testing.T) {
	for _, tc := range loadCases(t).Classify {
		kind, keyword := Classify(tc.SQL)
		if kind != tc.Kind || keyword != tc.Keyword {
			t.Errorf("Classify(%q) = %s, %q; want %s, %q", tc.SQL, kind, keyword, tc.Kind, tc.Keyword)
		}
	}
}

func TestHasMultipleStatements(t *testing.T) {
	for _, tc := range loadCases(t).Multiple {
		if got := HasMultipleStatements(tc.SQL); got != tc.Multiple {
			t.Errorf("HasMultipleStatements(%q) = %v, want %v", tc.SQL, got, tc.Multiple)
		}
	}
}

func TestCheck(t *testing.T) {
	for _, tc := range loadCases(t).Check {
		p := Policy{AllowDDL: tc.DDL, AllowDML: tc.DML, AllowOptions: tc.Options}
		err := Check(tc.SQL, p)
		if (err == nil) != tc.Allowed {
			t.Errorf("Check(%q, %+v) = %v, want allowed=%v", tc.SQL, p, err, tc.Allowed)
		}
		var gerr *Error
		if err != nil && !errors.As(err, &gerr) {
			t.Errorf("Check(%q) returned %T, want *Error", tc.SQL, err)
		}
	}
}

func TestErrorMessages(t *testing.T) {
	for sql, want := range map[string]string{
		"DROP TABLE t":              "DROP statements are blocked",
		"INSERT INTO t SELECT 1":    "INSERT statements are blocked",
		"ALTER SYSTEM SET `a` = 1":  "ALTER SYSTEM statements are blocked",
		"CALL p()":                  `"CALL" is not a statement this data source runs`,
		"SELEC 1":                   `"SELEC" is not a statement this data source runs`,
		"1 + 1":                     "This is not a statement this data source runs",
		"SELECT 1; DROP TABLE t":    "Only one statement can be run at a time",
		"/* x */ TRUNCATE TABLE t ": "TRUNCATE statements are blocked",
	} {
		err := Check(sql, Policy{})
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Check(%q) = %v, want message starting %q", sql, err, want)
		}
		// The message is logged above debug level, so it must not echo the SQL
		// beyond the leading keyword.
		if err != nil && strings.Contains(err.Error(), "TABLE t") {
			t.Errorf("Check(%q) message quotes the SQL: %v", sql, err)
		}
	}
}

func TestUnknownKeywordIsTruncatedInMessage(t *testing.T) {
	err := Check(strings.Repeat("A", 500)+" 1", Policy{})
	if err == nil || len(err.Error()) > 400 || strings.Contains(err.Error(), strings.Repeat("A", 40)) {
		t.Errorf("long first word was not truncated: %v", err)
	}
}

// FuzzCheck looks for panics and for the one property that must always hold:
// with a fully restrictive policy, nothing that Classify calls a write passes.
func FuzzCheck(f *testing.F) {
	for _, s := range []string{"SELECT 1", "/* */ DROP TABLE t", "-- x\nSET a = 1", "'", "`", "/*", "(((", ";;", "SELECT ';'; x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		kind, _ := Classify(sql)
		err := Check(sql, Policy{})
		if kind != KindRead && err == nil {
			t.Fatalf("Check allowed %q, classified %s", sql, kind)
		}
		_ = HasMultipleStatements(sql)
	})
}
