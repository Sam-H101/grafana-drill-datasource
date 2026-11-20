# Changelog

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
