package plugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/drill"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/macros"
	"github.com/Sam-H101/grafana-drill-datasource/pkg/models"
)

// problem is an error translated for a user, with the status and error
// source Grafana should record.
type problem struct {
	status     backend.Status
	message    string
	downstream bool
}

// explain turns any error from settings, macros or the Drill client into a
// message for the panel or the health check. Messages never contain
// credentials (the URL may not carry any) or Java stack traces.
func (d *Datasource) explain(err error) problem {
	var (
		qerr    *drill.QueryError
		herr    *drill.HTTPError
		macErr  *macros.Error
		netErr  net.Error
		dnsErr  *net.DNSError
		opErr   *net.OpError
		urlErr  *url.Error
		unkCA   x509.UnknownAuthorityError
		hostErr x509.HostnameError
		certErr x509.CertificateInvalidError
		recErr  tls.RecordHeaderError
		verErr  *tls.CertificateVerificationError
	)
	switch {
	case errors.Is(err, models.ErrInvalidSettings):
		return problem{backend.StatusBadRequest, "Configuration problem: " + strings.TrimPrefix(err.Error(), models.ErrInvalidSettings.Error()+": "), true}
	case errors.Is(err, models.ErrInvalidQuery):
		return problem{backend.StatusBadRequest, strings.ReplaceAll(err.Error(), "\n", ": "), true}
	case errors.As(err, &macErr):
		return problem{backend.StatusBadRequest, macErr.Error(), true}
	case errors.Is(err, drill.ErrAuthFailed):
		if d.settings != nil && d.settings.AuthMode == models.AuthNone {
			return problem{backend.StatusUnauthorized, "Drill requires authentication. Choose an authentication mode and enter a username and password.", true}
		}
		return problem{backend.StatusUnauthorized, "Drill rejected the credentials. Check the username, password and authentication mode (Basic needs BASIC in drill.exec.http.auth.mechanisms).", true}
	case errors.As(err, &qerr):
		return problem{backend.StatusBadRequest, qerr.Message, true}
	case errors.Is(err, context.DeadlineExceeded):
		secs := 0
		if d.settings != nil {
			secs = int(d.settings.Timeout.Seconds())
		}
		return problem{backend.StatusTimeout, fmt.Sprintf("The query did not finish within %d seconds and was cancelled on Drill.", secs), true}
	case errors.Is(err, drill.ErrCanceled), errors.Is(err, context.Canceled):
		return problem{backend.StatusTimeout, "The query was cancelled.", true}
	case errors.Is(err, drill.ErrResponseTooLarge):
		return problem{backend.StatusBadRequest, "The result is too large. Lower the row limit, add a LIMIT or select fewer columns.", true}
	case errors.As(err, &herr):
		return problem{backend.StatusBadGateway, herr.Error(), true}
	case errors.Is(err, drill.ErrConnectionClosed), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return problem{backend.StatusBadGateway, "Drill at " + d.url() + " closed the connection before answering. The Drillbit may have stopped or restarted.", true}
	case errors.As(err, &unkCA), errors.As(err, &hostErr), errors.As(err, &certErr), errors.As(err, &verErr), errors.As(err, &recErr):
		return problem{backend.StatusBadGateway, "TLS error connecting to Drill: " + innermost(err) + ". Check the URL scheme and the TLS settings.", true}
	case strings.Contains(err.Error(), "malformed HTTP response"):
		// Typically TLS alert bytes: an http:// URL pointing at an HTTPS port.
		return problem{backend.StatusBadGateway, "Drill at " + d.url() + " did not answer with HTTP. If Drill serves HTTPS, change the URL to https://.", true}
	case strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return problem{backend.StatusBadGateway, "Drill at " + d.url() + " answered with plain HTTP. Change the URL to http://, or enable HTTPS on Drill.", true}
	case errors.As(err, &dnsErr):
		return problem{backend.StatusBadGateway, "Cannot reach Drill at " + d.url() + ": host not found.", true}
	case errors.As(err, &opErr), errors.As(err, &netErr), errors.As(err, &urlErr):
		return problem{backend.StatusBadGateway, "Cannot reach Drill at " + d.url() + ": " + innermost(err) + ".", true}
	default:
		return problem{backend.StatusInternal, err.Error(), false}
	}
}

// logSafe shortens a user-facing message for logs above debug level: Drill
// error messages quote the SQL ("SQL Query: ..."), which must not be logged
// there, so only the first line is kept.
func logSafe(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

func (d *Datasource) url() string {
	if d.settings == nil {
		return "the configured URL"
	}
	return d.settings.URL
}

// innermost strips url.Error's "Post \"...\":" prefix and similar wrapping.
func innermost(err error) string {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err.Error()
		}
		err = u
	}
}

func (p problem) response() backend.DataResponse {
	src := backend.ErrorSourcePlugin
	if p.downstream {
		src = backend.ErrorSourceDownstream
	}
	return backend.ErrDataResponseWithSource(p.status, src, p.message)
}
