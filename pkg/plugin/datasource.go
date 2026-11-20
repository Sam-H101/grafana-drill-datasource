// Package plugin holds the Grafana SDK handlers. It wires settings, macros and
// the Drill client together.
package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/httpclient"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/samh/apache-drill/pkg/drill"
	"github.com/samh/apache-drill/pkg/models"
)

var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// Datasource is one configured Drill data source. Instances are created per
// settings version by the SDK's instance manager and used concurrently.
type Datasource struct {
	settings *models.Settings
	client   *drill.Client
	// settingsErr is a configuration problem, reported on every request
	// instead of failing instance creation with an opaque error.
	settingsErr error

	cache      metadataCache
	resOnce    sync.Once
	resHandler backend.CallResourceHandler
}

// NewDatasource is the SDK instance factory.
func NewDatasource(ctx context.Context, s backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	settings, err := models.LoadSettings(s)
	if err != nil {
		return &Datasource{settingsErr: err}, nil
	}

	opts, err := s.HTTPClientOptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("http client options: %w", err)
	}
	// Authentication is applied by the Drill client, not by the SDK (which
	// would add Basic auth whenever Grafana's own user field is set).
	opts.BasicAuth = nil
	if opts.Timeouts == nil {
		opts.Timeouts = &httpclient.DefaultTimeoutOptions
	}
	timeouts := *opts.Timeouts
	// Per-query deadlines come from the context; this only backstops them.
	timeouts.Timeout = settings.Timeout + 15*time.Second
	opts.Timeouts = &timeouts

	hc, err := httpclient.New(opts)
	if err != nil {
		return nil, fmt.Errorf("http client: %w", err)
	}
	client, err := drill.NewClient(drill.Config{
		BaseURL:         settings.URL,
		AuthMode:        drill.AuthMode(settings.AuthMode),
		Username:        settings.Username,
		Password:        settings.Password,
		ImpersonateUser: settings.ImpersonateUser,
	}, hc)
	if err != nil {
		return &Datasource{settingsErr: fmt.Errorf("%w: %w", models.ErrInvalidSettings, err)}, nil
	}
	return &Datasource{settings: settings, client: client}, nil
}

// Dispose is called when settings change and a new instance replaces this one.
func (d *Datasource) Dispose() {}
