package drill

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"syscall"
)

// Result is a completed Drill query. Values are stored column by column, as
// the raw JSON of each cell; a nil entry means the key was absent (null).
type Result struct {
	QueryID   string
	Columns   []string
	Types     []string
	Values    [][]json.RawMessage
	AutoLimit int
}

// NumRows is the number of rows in the result.
func (r *Result) NumRows() int {
	if len(r.Values) == 0 {
		return 0
	}
	return len(r.Values[0])
}

// wireResponse is what decodeResponse extracts besides the rows.
type wireResponse struct {
	result       Result
	queryState   string
	errorMessage string
}

// decodeResponse reads a /query.json response with a streaming decoder, so
// the rows are never held as one big document. See docs/drill-rest-notes.md
// sections 2 to 6 for the shapes handled here.
func decodeResponse(r io.Reader) (*wireResponse, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()

	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	var out wireResponse
	var pending []map[string]json.RawMessage // rows seen before "columns" (not expected)
	haveRows := false

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, wrapDecode(err)
		}
		key, _ := keyTok.(string)
		switch key {
		case "queryId":
			err = dec.Decode(&out.result.QueryID)
		case "columns":
			err = dec.Decode(&out.result.Columns)
		case "metadata":
			err = dec.Decode(&out.result.Types)
		case "attemptedAutoLimit":
			err = dec.Decode(&out.result.AutoLimit)
		case "queryState":
			err = dec.Decode(&out.queryState)
		case "errorMessage":
			err = dec.Decode(&out.errorMessage)
		case "rows":
			haveRows = true
			if out.result.Columns == nil {
				pending, err = decodeRowsAsMaps(dec)
			} else {
				out.result.Values, err = decodeRowsByColumn(dec, out.result.Columns)
			}
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return nil, wrapDecode(err)
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return nil, err
	}

	if pending != nil {
		out.result.Values = columnsFromMaps(pending, out.result.Columns)
	}
	if out.result.Values == nil && out.result.Columns != nil {
		out.result.Values = make([][]json.RawMessage, len(out.result.Columns))
	}
	if haveRows && len(out.result.Types) != len(out.result.Columns) {
		return nil, fmt.Errorf("drill returned %d column names but %d column types", len(out.result.Columns), len(out.result.Types))
	}
	return &out, nil
}

func decodeRowsByColumn(dec *json.Decoder, columns []string) ([][]json.RawMessage, error) {
	if err := expectDelim(dec, '['); err != nil {
		return nil, err
	}
	values := make([][]json.RawMessage, len(columns))
	for dec.More() {
		var row map[string]json.RawMessage
		if err := dec.Decode(&row); err != nil {
			return nil, err
		}
		for i, name := range columns {
			values[i] = append(values[i], nullIfJSONNull(row[name]))
		}
	}
	if err := expectDelim(dec, ']'); err != nil {
		return nil, err
	}
	return values, nil
}

func decodeRowsAsMaps(dec *json.Decoder) ([]map[string]json.RawMessage, error) {
	var rows []map[string]json.RawMessage
	if err := dec.Decode(&rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]json.RawMessage{}
	}
	return rows, nil
}

func columnsFromMaps(rows []map[string]json.RawMessage, columns []string) [][]json.RawMessage {
	values := make([][]json.RawMessage, len(columns))
	for i, name := range columns {
		values[i] = make([]json.RawMessage, len(rows))
		for j, row := range rows {
			values[i][j] = nullIfJSONNull(row[name])
		}
	}
	return values
}

func nullIfJSONNull(v json.RawMessage) json.RawMessage {
	if len(v) == 4 && string(v) == "null" {
		return nil
	}
	return v
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return wrapDecode(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("unexpected Drill response: expected %q", want)
	}
	return nil
}

func wrapDecode(err error) error {
	if errors.Is(err, ErrResponseTooLarge) {
		return ErrResponseTooLarge
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return ErrConnectionClosed
	}
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return fmt.Errorf("drill returned malformed JSON at byte %d", syn.Offset)
	}
	return err
}

// cappedReader returns ErrResponseTooLarge once more than max bytes are read.
type cappedReader struct {
	r   io.Reader
	max int64
	n   int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.n >= c.max {
		// At the cap: only fail if there really is more data.
		var b [1]byte
		if n, err := c.r.Read(b[:]); n == 0 && err != nil {
			return 0, err
		}
		return 0, ErrResponseTooLarge
	}
	if rem := c.max - c.n; int64(len(p)) > rem {
		p = p[:rem]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
