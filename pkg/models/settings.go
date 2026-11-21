// Package models parses data source settings and query models. It does no I/O.
package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Sam-H101/grafana-drill-datasource/pkg/sqlguard"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// AuthMode is how the plugin authenticates to Drill's web port.
type AuthMode string

const (
	// AuthNone sends no credentials (Drill with user authentication off).
	AuthNone AuthMode = "none"
	// AuthBasic sends an HTTP Basic header on every request. Drill must list
	// BASIC in drill.exec.http.auth.mechanisms.
	AuthBasic AuthMode = "basic"
	// AuthForm logs in through /j_security_check and reuses the session cookie.
	AuthForm AuthMode = "form"
)

const (
	DefaultTimeoutSeconds = 60
	MaxTimeoutSeconds     = 3600
	DefaultRowLimit       = 10000
	MaxRowLimit           = 1_000_000
)

// Settings is the validated configuration of one data source instance.
type Settings struct {
	// URL is the base URL of Drill's web port, without a trailing slash.
	URL             string
	AuthMode        AuthMode
	Username        string
	Password        string
	ImpersonateUser string
	DefaultSchema   string
	Timeout         time.Duration
	RowLimit        int
	// Statements says which statements other than queries may run. The zero
	// value allows queries only.
	Statements sqlguard.Policy
}

type jsonSettings struct {
	AuthMode        string `json:"authMode"`
	Username        string `json:"username"`
	ImpersonateUser string `json:"impersonateUser"`
	DefaultSchema   string `json:"defaultSchema"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	AutoLimit       int    `json:"autoLimit"`
	// Statement safety: all default to false (blocked).
	AllowDDL           bool `json:"allowDDL"`
	AllowDML           bool `json:"allowDML"`
	AllowOptionChanges bool `json:"allowOptionChanges"`
}

// ErrInvalidSettings wraps every validation failure so callers can report it
// as a configuration problem.
var ErrInvalidSettings = errors.New("invalid data source settings")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSettings, fmt.Sprintf(format, a...))
}

// LoadSettings reads and validates instance settings. The URL is Grafana's
// standard data source URL field; the password is secureJsonData.password.
func LoadSettings(src backend.DataSourceInstanceSettings) (*Settings, error) {
	var js jsonSettings
	if len(src.JSONData) > 0 {
		if err := json.Unmarshal(src.JSONData, &js); err != nil {
			return nil, invalid("could not parse settings JSON")
		}
	}

	baseURL, err := normalizeURL(src.URL)
	if err != nil {
		return nil, err
	}

	s := &Settings{
		URL:             baseURL,
		AuthMode:        AuthMode(strings.TrimSpace(js.AuthMode)),
		Username:        strings.TrimSpace(js.Username),
		Password:        src.DecryptedSecureJSONData["password"],
		ImpersonateUser: strings.TrimSpace(js.ImpersonateUser),
		DefaultSchema:   strings.TrimSpace(js.DefaultSchema),
		Statements: sqlguard.Policy{
			AllowDDL:     js.AllowDDL,
			AllowDML:     js.AllowDML,
			AllowOptions: js.AllowOptionChanges,
		},
	}

	switch s.AuthMode {
	case "":
		s.AuthMode = AuthNone
	case AuthNone, AuthBasic, AuthForm:
	default:
		return nil, invalid("unknown authentication mode %q", js.AuthMode)
	}
	if s.AuthMode != AuthNone {
		if s.Username == "" {
			return nil, invalid("a username is required for %s authentication", s.AuthMode)
		}
		if s.Password == "" {
			return nil, invalid("a password is required for %s authentication", s.AuthMode)
		}
		// Drill ignores the impersonation header when authentication is on.
		s.ImpersonateUser = ""
	}

	switch {
	case js.TimeoutSeconds == 0:
		s.Timeout = DefaultTimeoutSeconds * time.Second
	case js.TimeoutSeconds < 0 || js.TimeoutSeconds > MaxTimeoutSeconds:
		return nil, invalid("query timeout must be between 1 and %d seconds", MaxTimeoutSeconds)
	default:
		s.Timeout = time.Duration(js.TimeoutSeconds) * time.Second
	}

	switch {
	case js.AutoLimit == 0:
		s.RowLimit = DefaultRowLimit
	case js.AutoLimit < 0 || js.AutoLimit > MaxRowLimit:
		return nil, invalid("row limit must be between 1 and %d", MaxRowLimit)
	default:
		s.RowLimit = js.AutoLimit
	}

	return s, nil
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", invalid("the Drill URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", invalid("the Drill URL is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", invalid("the Drill URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", invalid("the Drill URL has no host")
	}
	if u.User != nil {
		// Credentials in the URL would end up in logs and error messages.
		return "", invalid("put credentials in the authentication fields, not in the URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", invalid("the Drill URL must not have a query string or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}
