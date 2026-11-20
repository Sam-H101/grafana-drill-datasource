package models

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func inst(url, jsonData string, secure map[string]string) backend.DataSourceInstanceSettings {
	return backend.DataSourceInstanceSettings{URL: url, JSONData: json.RawMessage(jsonData), DecryptedSecureJSONData: secure}
}

func TestLoadSettingsDefaults(t *testing.T) {
	s, err := LoadSettings(inst(" http://drill:8047/ ", `{}`, nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "http://drill:8047" || s.AuthMode != AuthNone || s.Timeout != 60*time.Second || s.RowLimit != DefaultRowLimit {
		t.Fatalf("%+v", s)
	}
}

func TestLoadSettingsFull(t *testing.T) {
	s, err := LoadSettings(inst("https://drill.example.com/proxy/drill/",
		`{"authMode":"form","username":" grafana ","impersonateUser":"x","defaultSchema":"dfs.tmp","timeoutSeconds":5,"autoLimit":7}`,
		map[string]string{"password": "pw"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{URL: "https://drill.example.com/proxy/drill", AuthMode: AuthForm, Username: "grafana", Password: "pw",
		ImpersonateUser: "", DefaultSchema: "dfs.tmp", Timeout: 5 * time.Second, RowLimit: 7}
	if *s != want {
		t.Fatalf("got %+v\nwant %+v", *s, want)
	}
}

func TestLoadSettingsKeepsImpersonationWithoutAuth(t *testing.T) {
	s, err := LoadSettings(inst("http://d:8047", `{"impersonateUser":" alice "}`, nil))
	if err != nil || s.ImpersonateUser != "alice" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestLoadSettingsInvalid(t *testing.T) {
	cases := []struct {
		name, url, json string
		secure          map[string]string
		msg             string
	}{
		{"no url", "", `{}`, nil, "URL is required"},
		{"bad scheme", "ftp://d", `{}`, nil, "http:// or https://"},
		{"no host", "http://", `{}`, nil, "no host"},
		{"creds in url", "http://u:p@d:8047", `{}`, nil, "not in the URL"},
		{"query string", "http://d:8047?x=1", `{}`, nil, "query string"},
		{"bad json", "http://d", `{`, nil, "parse settings"},
		{"bad mode", "http://d", `{"authMode":"kerberos"}`, nil, "unknown authentication mode"},
		{"basic no user", "http://d", `{"authMode":"basic"}`, map[string]string{"password": "x"}, "username is required"},
		{"basic no password", "http://d", `{"authMode":"basic","username":"u"}`, nil, "password is required"},
		{"timeout negative", "http://d", `{"timeoutSeconds":-1}`, nil, "timeout"},
		{"timeout too big", "http://d", `{"timeoutSeconds":99999}`, nil, "timeout"},
		{"limit negative", "http://d", `{"autoLimit":-5}`, nil, "row limit"},
		{"limit too big", "http://d", `{"autoLimit":2000000}`, nil, "row limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadSettings(inst(c.url, c.json, c.secure))
			if !errors.Is(err, ErrInvalidSettings) || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "u:p") {
				t.Fatalf("credentials leaked in error: %v", err)
			}
		})
	}
}

func TestParseQuery(t *testing.T) {
	q, err := ParseQuery(json.RawMessage(`{"rawSql":"SELECT 1","defaultSchema":" cp "}`))
	if err != nil || q.Format != FormatTable || q.DefaultSchema != "cp" {
		t.Fatalf("%+v %v", q, err)
	}
	q, err = ParseQuery(json.RawMessage(`{"rawSql":"x","format":"timeseries","fillMode":"value","fillValue":3}`))
	if err != nil || q.Format != FormatTimeSeries || q.FillMode != FillValue || q.FillValue != 3 {
		t.Fatalf("%+v %v", q, err)
	}
	for _, bad := range []string{`{`, `{"format":"heatmap"}`, `{"fillMode":"linear"}`} {
		if _, err := ParseQuery(json.RawMessage(bad)); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}
