package drill

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrAuthFailed means Drill refused the credentials, or wants credentials
	// and none were configured.
	ErrAuthFailed = errors.New("drill rejected the credentials")
	// ErrResponseTooLarge means the response exceeded Config.MaxResponseBytes.
	ErrResponseTooLarge = errors.New("drill response is too large; lower the row limit or select fewer columns")
	// ErrCanceled means the query was cancelled, by the plugin or on the Drill side.
	ErrCanceled = errors.New("the query was cancelled")
	// ErrConnectionClosed means Drill closed the connection before the
	// response was complete (for example, the Drillbit stopped).
	ErrConnectionClosed = errors.New("drill closed the connection before the response was complete")
)

// QueryError is a query Drill accepted and then failed (syntax error, unknown
// table, failed cast...). Message is Drill's short error text.
type QueryError struct {
	QueryID string
	Message string
}

func (e *QueryError) Error() string { return e.Message }

// HTTPError is an unexpected HTTP status from Drill.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("drill returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("drill returned HTTP %d: %s", e.StatusCode, e.Body)
}

const maxMessageLen = 2000

// cleanMessage trims a Drill message to something safe to show a user: no Java
// stack frames, bounded length.
func cleanMessage(s string) string {
	var kept []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "at ") || strings.HasPrefix(t, "(org.") || strings.HasPrefix(t, "(java.") ||
			strings.HasPrefix(t, "Caused By") || strings.HasPrefix(t, "org.apache.") || strings.HasPrefix(t, "java.") {
			continue
		}
		kept = append(kept, strings.TrimRight(line, " \t"))
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if len(out) > maxMessageLen {
		out = out[:maxMessageLen] + "…"
	}
	return out
}
