package drill

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
)

func loadFixture(t *testing.T, name string) *Result {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w, err := decodeResponse(f)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &w.result
}

func field(t *testing.T, fr *data.Frame, name string) *data.Field {
	t.Helper()
	f, idx := fr.FieldByName(name)
	if idx < 0 {
		t.Fatalf("no field %q", name)
	}
	return f
}

func TestToFrameAllTypes(t *testing.T) {
	// testdata/types.json is a real Drill 1.22.0 response (notes section 3).
	fr := ToFrame("", loadFixture(t, "types.json"))
	if n, _ := fr.RowLen(); n != 1 {
		t.Fatalf("rows = %d", n)
	}
	if fr.Meta != nil && len(fr.Meta.Notices) > 0 {
		t.Fatalf("unexpected notices: %+v", fr.Meta.Notices)
	}

	wantTypes := map[string]data.FieldType{
		"i": data.FieldTypeNullableInt64, "b": data.FieldTypeNullableInt64,
		"f4": data.FieldTypeNullableFloat64, "f8": data.FieldTypeNullableFloat64,
		"nan": data.FieldTypeNullableFloat64, "ninf": data.FieldTypeNullableFloat64,
		"dec": data.FieldTypeNullableFloat64, "bt": data.FieldTypeNullableBool,
		"ch": data.FieldTypeNullableString, "v": data.FieldTypeNullableString,
		"d": data.FieldTypeNullableTime, "t": data.FieldTypeNullableString,
		"ts": data.FieldTypeNullableTime, "old": data.FieldTypeNullableTime,
		"ids": data.FieldTypeNullableString, "iym": data.FieldTypeNullableString,
		"vb": data.FieldTypeNullableString, "m": data.FieldTypeNullableString,
		"lst": data.FieldTypeNullableString,
	}
	for name, want := range wantTypes {
		if got := field(t, fr, name).Type(); got != want {
			t.Errorf("%s: type %s, want %s", name, got, want)
		}
	}

	val := func(name string) any {
		v, _ := field(t, fr, name).ConcreteAt(0)
		return v
	}
	checks := map[string]any{
		"i":   int64(1),
		"b":   int64(9007199254740993), // exact beyond 2^53
		"f4":  1.5,
		"f8":  2.25,
		"dec": 1.10,
		"bt":  true,
		"ch":  "x",
		"v":   `quote"and\backé`,
		"d":   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		"t":   "13:14:15.123",
		"ts":  time.Date(2024, 1, 2, 3, 4, 5, 678e6, time.UTC),
		"old": time.Date(1960, 5, 6, 7, 8, 9, 10e6, time.UTC),
		"ids": "P1DT7384S",
		"iym": "P1Y2M",
		"vb":  "YWJj",
		"m":   `{"a":1,"b":[1,2]}`,
		"lst": `[1,2,3]`, // labelled BIGINT by Drill, carries an array
	}
	for name, want := range checks {
		if got := val(name); got != want {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	if v := val("nan").(float64); !math.IsNaN(v) {
		t.Errorf("nan = %v", v)
	}
	if v := val("ninf").(float64); !math.IsInf(v, -1) {
		t.Errorf("ninf = %v", v)
	}
}

func TestToFrameNullsAndEmpty(t *testing.T) {
	fr := ToFrame("", loadFixture(t, "nulls.json"))
	for _, f := range fr.Fields {
		if !f.Nullable() {
			t.Errorf("%s not nullable", f.Name)
		}
		if _, ok := f.ConcreteAt(0); ok {
			t.Errorf("%s should be null", f.Name)
		}
	}
	if got := field(t, fr, "ts").Type(); got != data.FieldTypeNullableTime {
		t.Errorf("null timestamp column type %s", got)
	}

	empty := ToFrame("", loadFixture(t, "empty.json"))
	if len(empty.Fields) != 2 {
		t.Fatalf("empty result lost its schema: %d fields", len(empty.Fields))
	}
	if n, _ := empty.RowLen(); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	if empty.Fields[0].Type() != data.FieldTypeNullableInt64 || empty.Fields[1].Type() != data.FieldTypeNullableString {
		t.Fatalf("empty types: %s %s", empty.Fields[0].Type(), empty.Fields[1].Type())
	}
}

func TestToFrameStringEncodingsAndBadValues(t *testing.T) {
	// Older Drill documentation shows every value as a string; accept that,
	// and turn unparseable values into nulls with one notice per column.
	body := `{"queryId":"q","columns":["n","f","b","ts","d","t","missing"],
	"metadata":["BIGINT","FLOAT8","BIT","TIMESTAMP","DATE","TIME","INT"],
	"rows":[
	 {"n":"42","f":"1.5","b":"true","ts":"2024-01-02 03:04:05.678","d":"2024-01-02","t":"13:14:15"},
	 {"n":"x","f":"oops","b":"maybe","ts":"not a time","d":1704153600000,"t":99999999999,"missing":null}
	],"queryState":"COMPLETED"}`
	w, err := decodeResponse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	fr := ToFrame("", &w.result)

	row0 := map[string]any{
		"n": int64(42), "f": 1.5, "b": true,
		"ts": time.Date(2024, 1, 2, 3, 4, 5, 678e6, time.UTC),
		"d":  time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		"t":  "13:14:15",
	}
	for name, want := range row0 {
		if got, _ := field(t, fr, name).ConcreteAt(0); got != want {
			t.Errorf("row0 %s = %#v, want %#v", name, got, want)
		}
	}
	for _, name := range []string{"n", "f", "b", "ts", "t", "missing"} {
		if v, ok := field(t, fr, name).ConcreteAt(1); ok {
			t.Errorf("row1 %s = %#v, want null", name, v)
		}
	}
	if fr.Meta == nil || len(fr.Meta.Notices) != 5 {
		t.Fatalf("want 5 notices (n, f, b, ts, t), got %+v", fr.Meta)
	}
}

func TestBaseType(t *testing.T) {
	for in, want := range map[string]string{
		"VARDECIMAL(5, 2)": "VARDECIMAL",
		"VARCHAR(65535)":   "VARCHAR",
		" bigint ":         "BIGINT",
		"TIMESTAMP":        "TIMESTAMP",
	} {
		if got := BaseType(in); got != want {
			t.Errorf("BaseType(%q) = %q", in, got)
		}
	}
	if !IsTimeType("DATE") || IsTimeType("TIME") {
		t.Error("IsTimeType")
	}
}
