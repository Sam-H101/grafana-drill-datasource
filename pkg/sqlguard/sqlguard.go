// Package sqlguard decides whether a SQL statement may be sent to Drill under
// a data source's statement policy. It does no I/O.
//
// The check is an allow-list on the statement's leading keyword: queries are
// always allowed, known kinds of write are allowed only when the policy says
// so, and anything it does not recognise is blocked. It is a guard against
// mistakes and casual misuse, not a substitute for Drill permissions: give
// Grafana a Drill user that can only read what it needs.
//
// src/safety.ts mirrors this file for the query editor's warning; both are
// tested against testdata/cases.json.
package sqlguard

import (
	"fmt"
	"strings"
)

// Kind is the category of a statement.
type Kind string

const (
	// KindRead covers statements that only return data or metadata.
	KindRead Kind = "read"
	// KindDDL creates, alters or drops tables, views, schemas or functions,
	// or rewrites table metadata and statistics.
	KindDDL Kind = "ddl"
	// KindDML inserts, updates or deletes rows.
	KindDML Kind = "dml"
	// KindOptions changes Drill system or session options, or session state.
	KindOptions Kind = "options"
	// KindUnknown is anything else, including text that is not a statement.
	KindUnknown Kind = "unknown"
)

// Policy says which kinds of statement other than reads are allowed.
type Policy struct {
	AllowDDL     bool
	AllowDML     bool
	AllowOptions bool
}

// Unrestricted reports whether the policy allows every kind of statement.
func (p Policy) Unrestricted() bool { return p.AllowDDL && p.AllowDML && p.AllowOptions }

var keywordKinds = map[string]Kind{
	"SELECT": KindRead, "WITH": KindRead, "VALUES": KindRead, "TABLE": KindRead,
	"SHOW": KindRead, "DESCRIBE": KindRead, "DESC": KindRead, "EXPLAIN": KindRead,

	"CREATE": KindDDL, "DROP": KindDDL, "ALTER": KindDDL,
	"REFRESH": KindDDL, "ANALYZE": KindDDL,

	"INSERT": KindDML, "UPDATE": KindDML, "DELETE": KindDML,
	"MERGE": KindDML, "UPSERT": KindDML, "TRUNCATE": KindDML,

	"SET": KindOptions, "RESET": KindOptions, "USE": KindOptions,
}

// Classify returns the kind of a statement and its leading keyword in upper
// case ("" when the text does not start with a word). Leading whitespace,
// comments and opening parentheses are skipped.
func Classify(sql string) (Kind, string) {
	i := skipLeading(sql, 0, true)
	word, next := readWord(sql, i)
	if word == "" {
		return KindUnknown, ""
	}
	keyword := strings.ToUpper(word)
	kind, ok := keywordKinds[keyword]
	if !ok {
		return KindUnknown, keyword
	}
	if keyword == "ALTER" {
		// ALTER SYSTEM and ALTER SESSION change options; other ALTERs are DDL.
		second, _ := readWord(sql, skipLeading(sql, next, false))
		switch strings.ToUpper(second) {
		case "SYSTEM", "SESSION":
			return KindOptions, keyword + " " + strings.ToUpper(second)
		}
	}
	return kind, keyword
}

// HasMultipleStatements reports whether a semicolon outside strings, quoted
// identifiers and comments is followed by more SQL. Drill runs one statement
// per request and rejects such text itself; the guard rejects it first so the
// statements after the first one cannot hide from Classify.
func HasMultipleStatements(sql string) bool {
	for i := 0; i < len(sql); {
		switch c := sql[i]; {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(sql, i, c)
		case isCommentStart(sql, i):
			i = skipComment(sql, i)
		case c == ';':
			if skipLeading(sql, i+1, false) < len(sql) {
				return true
			}
			i++
		default:
			i++
		}
	}
	return false
}

// Error is returned by Check for a statement the policy does not allow.
type Error struct {
	Kind Kind
	// Keyword is the statement's leading keyword, "" if it has none.
	Keyword  string
	Multiple bool
}

// maxEchoedKeyword bounds how much of an unrecognised first word is quoted
// back in an error message (and so in logs).
const maxEchoedKeyword = 32

const settingsHint = "An administrator can change this under Statement safety in the data source settings."

func (e *Error) Error() string {
	if e.Multiple {
		return "Only one statement can be run at a time: remove everything after the first semicolon."
	}
	switch e.Kind {
	case KindDDL:
		return fmt.Sprintf("%s statements are blocked: this data source does not allow creating, altering or dropping tables, views, schemas or functions. %s", e.Keyword, settingsHint)
	case KindDML:
		return fmt.Sprintf("%s statements are blocked: this data source does not allow inserting, updating or deleting data. %s", e.Keyword, settingsHint)
	case KindOptions:
		return fmt.Sprintf("%s statements are blocked: this data source does not allow changing Drill options or session state. %s", e.Keyword, settingsHint)
	}
	// Often a typo in the first word (SELEC), so say what was read and what
	// is expected rather than calling it a blocked statement.
	what := "This is"
	if e.Keyword != "" {
		word := e.Keyword
		if len(word) > maxEchoedKeyword {
			word = word[:maxEchoedKeyword] + "…"
		}
		what = fmt.Sprintf("%q is", word)
	}
	return fmt.Sprintf("%s not a statement this data source runs: it runs queries (SELECT, WITH, VALUES, SHOW, DESCRIBE, EXPLAIN) and the kinds of statement its settings allow. Check the first word of the statement. %s", what, settingsHint)
}

// Check returns nil if the policy allows the statement and an *Error if not.
// An unrestricted policy allows everything, including text Classify does not
// recognise.
func Check(sql string, p Policy) error {
	if p.Unrestricted() {
		return nil
	}
	if HasMultipleStatements(sql) {
		return &Error{Kind: KindUnknown, Multiple: true}
	}
	kind, keyword := Classify(sql)
	allowed := false
	switch kind {
	case KindRead:
		allowed = true
	case KindDDL:
		allowed = p.AllowDDL
	case KindDML:
		allowed = p.AllowDML
	case KindOptions:
		allowed = p.AllowOptions
	}
	if allowed {
		return nil
	}
	return &Error{Kind: kind, Keyword: keyword}
}

// skipLeading returns the index of the first character at or after i that is
// not whitespace or part of a comment (or, with parens, an opening
// parenthesis). Only ASCII whitespace counts: Drill rejects statements that
// start with other space characters, so they are left for Classify to block.
func skipLeading(s string, i int, parens bool) int {
	for i < len(s) {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case parens && c == '(':
			i++
		case isCommentStart(s, i):
			i = skipComment(s, i)
		default:
			return i
		}
	}
	return i
}

// isCommentStart reports whether a comment starts at i. Drill accepts "--"
// and "//" line comments and "/* */" block comments.
func isCommentStart(s string, i int) bool {
	if i+1 >= len(s) {
		return false
	}
	two := s[i : i+2]
	return two == "--" || two == "//" || two == "/*"
}

// skipComment returns the index after the comment starting at i. An
// unterminated comment ends at len(s).
func skipComment(s string, i int) int {
	if s[i:i+2] == "/*" {
		end := strings.Index(s[i+2:], "*/")
		if end < 0 {
			return len(s)
		}
		return i + 2 + end + 2
	}
	end := strings.IndexByte(s[i:], '\n')
	if end < 0 {
		return len(s)
	}
	return i + end + 1
}

// skipQuoted returns the index after the quoted run starting at i. A doubled
// quote character is an escaped quote. An unterminated run ends at len(s).
func skipQuoted(s string, i int, q byte) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(s)
}

func readWord(s string, i int) (string, int) {
	j := i
	for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
		j++
	}
	return s[i:j], j
}
