# Changelog

## Unreleased

### Added

- Statement safety: the data source now runs only queries (`SELECT`, `WITH`,
  `VALUES`, `SHOW`, `DESCRIBE`, `EXPLAIN`) unless its settings allow more.
  Three settings allow statements that create or drop objects, change data,
  or change Drill options. The backend enforces this, and the query editor
  warns about a blocked statement as it is typed.

### Changed

- **Behaviour change:** statements other than queries, such as
  `CREATE TABLE AS`, `DROP`, `ALTER SESSION` and `USE`, used to be sent to
  Drill and are now refused by default. Turn on the matching setting under
  Statement safety to run them.

## 0.1.2 (2026-11-21)

### Changed

- Plugin metadata links to the source code, documentation and issue tracker.
- Releases are built unsigned until the plugin is approved for a community
  signature, and carry a build provenance attestation.
- The Go module path matches the source repository.
- The release workflow checks the tag against the plugin version before building.
- Built with Go 1.26.11, which fixes vulnerabilities in the Go standard library.

## 0.1.1 (2026-11-21)

Tagged, but no build was published: the tag did not match the plugin version.

## 0.1.0 (2026-11-20)

First version.

- SQL queries against Apache Drill 1.19 and later through its REST API, from
  Grafana's server.
- Table and time series formats; text columns split time series into one
  series per value, with optional gap filling.
- Time macros: `$__timeFilter`, `$__timeFrom`, `$__timeTo`,
  `$__unixEpochFilter`, `$__timeGroup`, `$__timeGroupAlias`, `$__interval`.
- Template variables (with `__text`/`__value` columns), annotations and
  alerting.
- Editor suggestions for schemas, tables, columns and macros; per-query schema
  override.
- Authentication: none (with optional impersonation), HTTP Basic, form login.
  TLS: custom CA, skip verification, client certificate.
- Queries are cancelled on Drill when they time out or Grafana no longer needs
  them. Configurable row limit and timeout.
