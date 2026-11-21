# Apache Drill data source for Grafana

Query [Apache Drill](https://drill.apache.org/) with SQL from Grafana: tables,
time series, template variables, annotations and alerting. The plugin talks to
Drill's REST API from Grafana's server, so credentials never reach the browser
and alert rules work.

## Requirements

- Grafana 13.2 or later.
- Apache Drill 1.19 or later, with its web port (8047 by default) reachable
  from the Grafana server. Embedded and distributed Drill both work.

## Configuration

| Setting | Notes |
| --- | --- |
| URL | Drill's web port, for example `https://drill.example.com:8047`. A path prefix is allowed (reverse proxies). Do not put credentials in the URL. |
| Authentication mode | **None**, **Basic** or **Form login**, see below. |
| Username, Password | For Basic and Form login. The password is stored encrypted and never sent back to the browser. |
| Impersonate user | Only with **None**: queries run as this Drill user. Drill needs `drill.exec.impersonation.enabled: true`. |
| TLS | Skip verification (testing only), a custom CA certificate, and a client certificate and key. |
| Statement safety | Three switches, all off by default, for the statements that are not queries. See [Statement safety](#statement-safety). |
| Default schema | Schema for unqualified table names, for example `dfs.tmp`. Each query can override it. |
| Query timeout | Seconds, 1 to 3600, default 60. A query that runs longer is cancelled on Drill. |
| Row limit | 1 to 1,000,000, default 10,000. Drill stops after this many rows, and the panel shows a notice when the limit is reached. |

**Save & test** runs a trivial query and reports the Drill version, or a
specific reason it could not connect: unreachable host, TLS problem, wrong
URL scheme, rejected credentials, or authentication required.

### Authentication

- **None**: Drill has user authentication turned off.
- **Basic**: sends an HTTP Basic header. Drill must list `BASIC` in its HTTP
  mechanisms; when user authentication is on, Drill's default is `FORM` only:

  ```hocon
  drill.exec.http.auth.mechanisms: ["FORM", "BASIC"]
  ```

- **Form login**: logs in the way Drill's web UI does and reuses the session.
  Works with Drill's default settings.

With Basic or Form login over plain `http://`, the password crosses the
network unencrypted; the configuration page warns about this. Use `https://`.

### Provisioning

```yaml
apiVersion: 1
datasources:
  - name: Apache Drill
    type: smartquinoa1504-apachedrill-datasource
    access: proxy
    url: https://drill.example.com:8047
    jsonData:
      authMode: basic # none | basic | form
      username: grafana
      defaultSchema: dfs.tmp
      timeoutSeconds: 60
      autoLimit: 10000
      tlsAuthWithCACert: true
    secureJsonData:
      password: $DRILL_PASSWORD
      tlsCACert: $DRILL_CA_CERT
```

### Statement safety

By default the data source only runs queries: `SELECT`, `WITH`, `VALUES`,
`SHOW`, `DESCRIBE` and `EXPLAIN`. Every other statement is refused by the
plugin's backend before it reaches Drill, and the query editor warns about it
while you type. Three settings allow more:

| Setting | Allows |
| --- | --- |
| Allow creating and dropping objects | `CREATE`, `DROP`, `ALTER`, `REFRESH`, `ANALYZE` |
| Allow changing data | `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `TRUNCATE` |
| Allow changing Drill options | `ALTER SYSTEM`, `ALTER SESSION`, `SET`, `RESET`, `USE` |

- The check looks at the statement's first keyword, after comments and
  opening parentheses, so a query that only mentions these words in a string,
  comment or column name is not affected.
- Statements the plugin does not recognise are refused unless all three
  settings are on.
- With any setting off, text with more than one statement is refused.
- In provisioning, the keys are `allowDDL`, `allowDML` and
  `allowOptionChanges` under `jsonData`.

This protects against mistakes and casual misuse. It does not replace Drill's
own permissions: see [Security](#security).

## Writing queries

Write Drill SQL in the editor and press **Ctrl+Enter** (or **Run query**) to
run it. The editor suggests schemas, tables, columns and macros. A trailing
semicolon is removed; one statement per query.

- **Format: Table** returns the result as is.
- **Format: Time series** needs a `TIMESTAMP` or `DATE` column: the one named
  `time`, otherwise the first one. Rows are sorted by time. Text columns split
  the result into one series per value (for example one series per gender);
  **Fill** controls gaps between them.

```sql
SELECT $__timeGroupAlias(CAST(hire_date AS TIMESTAMP), $__interval),
       gender,
       COUNT(*) AS hires
FROM cp.`employee.json`
WHERE $__timeFilter(CAST(hire_date AS TIMESTAMP))
GROUP BY 1, 2
ORDER BY 1
```

The query inspector shows the SQL that was sent, with every macro expanded.

### Macros

| Macro | Expands to |
| --- | --- |
| `$__timeFilter(col)` | `col >= TIMESTAMP '<from>' AND col <= TIMESTAMP '<to>'` |
| `$__timeFrom()`, `$__timeTo()` | The dashboard range bounds as `TIMESTAMP` literals |
| `$__unixEpochFilter(col)` | Range predicate on a column of epoch seconds |
| `$__unixEpochFrom()`, `$__unixEpochTo()` | The range bounds as epoch seconds |
| `$__timeGroup(col, 5m)` | `col` rounded down to 5-minute buckets |
| `$__timeGroup(col, 5m, fill)` | The same, with gaps filled by `NULL`, `previous` or a number |
| `$__timeGroupAlias(col, 5m)` | `$__timeGroup(...) AS \`time\`` |
| `$__interval`, `$__interval_ms` | The panel interval, for example `30s` or `30000` |

Macros inside string literals, quoted identifiers and comments are left alone.
A misspelt macro is reported by name before the query reaches Drill.

### Time zones

Drill's `TIMESTAMP` type has no time zone. The plugin reads every `DATE` and
`TIMESTAMP` value as **UTC**, and the macros write UTC literals. Store
timestamps in UTC and run Drillbits in UTC for times to line up with the
dashboard.

### Errors, timeouts and cancellation

Drill's error message is shown in the panel, for example
`PARSE ERROR: ... SQL Query: SELECT 1 FRM t ^`. When a query passes the timeout, or
Grafana no longer needs it (the dashboard was closed or the time range
changed), the plugin cancels it on Drill as well, so it stops using Drill's
resources.

## Template variables

Create a variable of type **Query**, choose this data source and write SQL. The
first column gives the values:

```sql
SELECT DISTINCT gender FROM cp.`employee.json` ORDER BY 1
```

To show a label that differs from the value, name the columns `__text` and
`__value`:

```sql
SELECT department_id AS __value, department_description AS __text
FROM cp.`department.json`
```

Use variables in queries like this:

- A single-value variable is inserted as written, so quote it yourself when it
  is text: `WHERE gender = '$gender'`.
- A multi-value or **All** variable becomes a list of quoted values, with
  quotes inside values escaped: `WHERE gender IN ($gender)` becomes
  `WHERE gender IN ('F','M')`. Numbers are not quoted.

## Annotations

Add an annotation query on this data source. Return a `time` column, and
optionally `timeEnd`, `text` and `tags`:

```sql
SELECT CAST(hire_date AS TIMESTAMP) AS `time`, full_name AS text, position_title AS tags
FROM cp.`employee.json`
WHERE management_role = 'Senior Management' AND $__timeFilter(CAST(hire_date AS TIMESTAMP))
```

## Alerting

Alert rules can query Drill. Return a time series, or a table with a numeric
column, for example `SELECT COUNT(*) AS errors FROM ...`. Dashboard template
variables are not available in alert rules (a Grafana rule), but the time
macros and `$__interval` are.

## Security

- Anyone who can query this data source can run any query that the configured
  Drill user is allowed to run, including from Explore. Give Grafana a Drill
  user with read access to only the data it needs, and use Grafana's data
  source permissions to limit who can query it.
- Statements that change objects, data or Drill options are refused unless
  the data source's [statement safety](#statement-safety) settings allow them.
  Treat this as a second line of defence behind the Drill user's permissions,
  not as the only one.
- Credentials and TLS keys are stored in Grafana's encrypted secure settings
  and used only by the plugin's backend.
- The plugin does not log SQL text above debug level. Grafana's own request
  logging records error messages, and Drill's error messages quote the query.

## Known limitations

- `DECIMAL` values are converted to 64-bit floating point, so digits beyond
  about 15 significant figures are lost.
- `TIME` values are shown as text (`13:14:15.123`). `MAP` and list values are
  shown as JSON text. `INTERVAL` values are shown as ISO-8601 text (`P1DT2H`).
- File-based schemas (such as `dfs`) usually list no tables, so the editor
  cannot suggest table names there; queries still work.
- Client certificates (mutual TLS) are passed to Grafana's HTTP client but
  have not been tested against a Drill deployment.
- SQL only: there is no visual query builder.

## Feedback and contributing

Report bugs and request features in the
[issue tracker](https://github.com/Sam-H101/grafana-drill-datasource/issues). To build or change the
plugin, see [CONTRIBUTING.md](https://github.com/Sam-H101/grafana-drill-datasource/blob/master/CONTRIBUTING.md).

## Licence

Apache License 2.0. Apache Drill and Apache are trademarks of the Apache
Software Foundation; this plugin is not affiliated with or endorsed by it.
