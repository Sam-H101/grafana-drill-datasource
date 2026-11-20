# Drill REST API: observed behaviour

Observed 2026-11-19 with `curl` against `apache/drill:1.22.0` (OpenJDK 17,
embedded mode, `TZ=UTC`) and, where noted, `apache/drill:1.19.0`.
The backend's parsing rules in `pkg/drill` cite this file.

## 1. Request shape

`POST /query.json`, `Content-Type: application/json`. The request type
(`QueryWrapper`) accepts exactly seven fields: `queryType`, `query`,
`autoLimit`, `autoLimitRowCount`, `defaultSchema`, `options`, `userName`.

- Any other field is rejected with **HTTP 400, `text/plain`**, body
  `Unrecognized field "bogus" ... not marked as ignorable (7 known properties: ...)`.
  So the client sends only known fields.
- `autoLimit: N` caps the rows returned to exactly N; the response echoes it as
  `attemptedAutoLimit`.
- `defaultSchema: "cp"` works (`SELECT ... FROM \`employee.json\``).
- `options` (map of session option to string value) is accepted and applied.
- A trailing `;` makes the query fail. Two statements separated by `;` fail.
  The client strips trailing semicolons and whitespace.
- A leading `/* comment */` is kept in the query text Drill records, and does
  not interfere with `autoLimit`. A trailing `-- comment` is fine too.

## 2. Response shape

Success, HTTP 200, `application/json`, keys in this order:

```json
{"queryId":"...","columns":["employee_id","full_name"],
 "metadata":["BIGINT","VARCHAR"],"attemptedAutoLimit":0,
 "rows":[{"employee_id":1,"full_name":"Sheri Nowmer"}],
 "queryState":"COMPLETED"}
```

- `columns` and `metadata` are parallel arrays and arrive before `rows`, so the
  types are known before any row is read.
- Column order comes from `columns`; rows are JSON objects keyed by name.
- `metadata` can carry precision: `VARCHAR(65535)`, `VARCHAR(15)`,
  `VARDECIMAL(5, 2)`. Strip everything from `(` before mapping the type.
- `CAST(x AS CHAR(3))` reports `VARCHAR(1)`.

## 3. Value encoding

Identical on 1.19.0 and 1.22.0. Values are native JSON, not strings:

| Drill type | Example value | Notes |
| --- | --- | --- |
| `INT`, `BIGINT` | `1` | `9007199254740993` comes back exact: decode with `json.Number` |
| `FLOAT4`, `FLOAT8` | `1.5` | NaN and infinities are the **strings** `"NaN"`, `"Infinity"` |
| `VARDECIMAL(p, s)` | `1.10` | number literal keeps scale |
| `BIT` | `true` | |
| `VARCHAR` | `"txt"` | standard JSON escaping, UTF-8 |
| `DATE` | `1704153600000` | epoch ms at midnight UTC |
| `TIME` | `47655123` | ms since midnight (13:14:15.123) |
| `TIMESTAMP` | `1704164645678` | epoch ms; pre-1970 values are negative |
| `INTERVALDAY` | `"P1DT7384S"` | ISO-8601 duration string |
| `INTERVALYEAR` | `"P1Y2M"` | ISO-8601 duration string |
| `VARBINARY` | `"YWJj"` | base64 |
| `MAP` | `{"a":1,"b":[1,2]}` | nested JSON object |
| repeated (list) | `[1,2,3]` | **metadata says the element type** (`BIGINT`), value is an array |

`CAST(... AS TINYINT)` and `SMALLINT` are rejected by Drill's planner, so these
types only appear from storage formats such as Parquet. The converter still
maps them.

Older documentation shows every value as a string. Neither tested version does
this, but the converter accepts string encodings of every type as well.

## 4. Nulls and missing keys

A null is emitted as JSON `null` with the key present
(`{"a":1,"b":null}`), for every type tested. The converter still treats a
missing key as null.

## 5. Empty results

Zero rows still return `columns` and `metadata`, with `"rows":[]`. An empty
frame can keep its schema.

## 6. Errors

| Case | HTTP | Body |
| --- | --- | --- |
| Syntax error, unknown table, failed cast | 200 | `{"queryId":"...","queryState":"FAILED"}`, **no message** |
| Unknown request field, malformed JSON | 400 `text/plain` | Jackson error text |
| Submission refused (e.g. `userName` with auth on) | 200 | `{ "errorMessage" : "Query submission failed"}` |

The message for a failed query is in its profile: `GET /profiles/{queryId}.json`
returns `error` (short, for example
`PARSE ERROR: Non-query expression encountered in illegal context\n\nSQL Query: SELEC 1\n           ^\n`)
and `verboseError` (with a Java stack trace). The client reports `error` only.
Same on 1.19.0.

With auth on, a user can read their own profiles. Reading another user's
profile returns HTTP 500 `{ 'message' : 'error (unable to serialize profile)' }`.

## 7. Timestamps

Values are epoch milliseconds. `NOW()` in a `TZ=UTC` container returned the
current UTC instant, and `TIMEOFDAY()` reported `... UTC`. Drill's
`TIMESTAMP` has no zone: it holds a wall-clock time and the REST layer encodes
that wall clock as if it were UTC.

Decision: interpret every `DATE`/`TIMESTAMP` value as UTC, and document that
data should be stored in UTC (and Drillbits run in UTC) for correct time
alignment. No time zone setting in this version.

## 8. Authentication

Tested with `devenv/drill/auth` (htpasswd authenticator,
`drill.exec.http.auth.mechanisms: ["FORM", "BASIC"]`).

- No credentials: **307** redirect to `/mainLogin?redirect=%2Fquery.json`.
- HTTP Basic, right password: 200 and the query runs as that user.
- HTTP Basic, wrong password: **307** to `/mainLogin`, not 401.
- Form login: `POST /j_security_check` with `j_username` and `j_password`
  (form-encoded). Success is **303** with a `JSESSIONID` cookie; later requests
  with the cookie run as that user.
- Form login, wrong password: **200** with the login page.
- `GET /status` needs no credentials.
- In embedded mode the sqlline shell must log in too (`drill-embedded -n -p`),
  or the in-process Drillbit shuts down.

Basic needs `BASIC` in `drill.exec.http.auth.mechanisms`; Drill's default when
auth is on is `["FORM"]`. Both modes are kept (`basic`, `form`).

The client never follows redirects. A redirect to a login page, or a 200 HTML
login page after `j_security_check`, means bad credentials.

## 9. Impersonation

Tested with `devenv/drill/impersonation` (auth off,
`drill.exec.impersonation.enabled: true`), checking `SESSION_USER`:

| Request | Result |
| --- | --- |
| nothing | `anonymous` |
| header `User-Name: alice` | `alice` |
| body `"userName":"bob"` | `bob` |
| both | `bob` (body wins) |
| header, impersonation off | `anonymous` (ignored) |

With auth on, `User-Name` is ignored and body `userName` makes submission fail.
The plugin sends the `User-Name` header, only when auth mode is `none`.

## 10. Cancellation

Closing the HTTP connection does **not** stop the query: after a client
timeout it was still `Running` in `GET /profiles.json`.

`GET /profiles/cancel/{queryId}` cancels it at once (body
`Cancelled query ... on locally running node.`, profile state goes to 3,
CANCELED). The query ID is not returned until the query finishes, so the client
prefixes each query with a unique comment `/* grafana-query:<uuid> */`, and on
cancellation looks it up in `runningQueries` of `GET /profiles.json` (whose
`query` field starts with the submitted text, truncated at about 150
characters) and cancels that ID.

## 11. Large results

300,300 rows of three columns: 17.4 MB response, 0.5 s. `autoLimit: 1000` on
the same query returned 1000 rows. The client decodes `rows` with a streaming
decoder, row by row, and has a byte cap.

## Other

- `SELECT * FROM sys.version` returns `version` (`1.22.0`), `commit_id` and
  more: used by the health check.
- `SHOW SCHEMAS` works and honours `autoLimit`.
- Sample data: `cp.\`employee.json\`` (1155 rows). Its `hire_date` and
  `birth_date` are strings like `1994-12-01 00:00:00.0`.
- Cross joins need session option
  `planner.enable_nljoin_for_scalar_only = false`.
