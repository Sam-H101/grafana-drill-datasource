package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const healthTimeout = 15 * time.Second

// CheckHealth backs "Save & test": it runs a trivial query and reports the
// Drill version, or why it could not.
func (d *Datasource) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	if d.settingsErr != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: d.explain(d.settingsErr).message}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	version, err := d.client.Version(ctx)
	if err != nil {
		p := d.explain(err)
		backend.Logger.Warn("Drill health check failed", "status", int(p.status), "error", logSafe(p.message))
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: p.message}, nil
	}
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: fmt.Sprintf("Connected to Apache Drill %s.", version),
	}, nil
}
