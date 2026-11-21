# Testing the Apache Drill data source

How to try the plugin from scratch. No external service or account is needed:
Apache Drill runs in one container and ships with sample data
(``cp.`employee.json` ``), so every query below works offline.

There are two ways to get a test environment:

- [Quick setup](#quick-setup): a released build, one Drill and one Grafana
  container. About two minutes.
- [Full environment](#full-environment): the repository's Docker Compose stack,
  with a provisioned data source, a demo dashboard, an alert rule, and Drill
  servers for testing authentication and TLS.

## Quick setup

Requires Docker, `curl` and `unzip`.

```sh
# The plugin, from the latest release
curl -L -o plugin.zip https://github.com/Sam-H101/grafana-drill-datasource/releases/download/v0.1.2/smartquinoa1504-apachedrill-datasource-0.1.2.zip
unzip plugin.zip -d plugins

# Drill and Grafana on one network
docker network create drill-test
docker run -d --name drill --network drill-test -p 8047:8047 -t \
  -e JAVA_TOOL_OPTIONS=-XX:-UseContainerSupport apache/drill:1.22.0
docker run -d --name grafana --network drill-test -p 3000:3000 \
  -v "$PWD/plugins:/var/lib/grafana/plugins" \
  -e GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=smartquinoa1504-apachedrill-datasource \
  grafana/grafana:13.3.1
```

- `-t` is required: the Drill image runs an interactive shell, which exits
  without a TTY.
- `JAVA_TOOL_OPTIONS` avoids a crash in the image's Java 17.0.2 on hosts with
  cgroup v2 and a recent kernel.
- Drill takes about 20 seconds to start; it is ready when
  http://localhost:8047 loads.

Open http://localhost:3000 (admin / admin), go to **Connections > Data sources
> Add new data source > Apache Drill**, and set:

| Setting | Value |
| --- | --- |
| URL | `http://drill:8047` |
| Authentication mode | None |

**Save & test** reports `Connected to Apache Drill 1.22.0.`

To remove everything afterwards:

```sh
docker rm -f drill grafana && docker network rm drill-test
```

## What to try

Use **Explore** with the time range **1994-01-01 to 1999-01-01** (the sample
data's hire dates).

**Time series.** Set **Format** to **Time series**. The result has one series
per gender:

```sql
SELECT $__timeGroupAlias(CAST(hire_date AS TIMESTAMP), $__interval), gender, COUNT(*) AS hires
FROM cp.`employee.json`
WHERE $__timeFilter(CAST(hire_date AS TIMESTAMP))
GROUP BY 1, 2
ORDER BY 1
```

The query inspector shows the SQL that was sent, with the macros expanded.

**Table.** Set **Format** to **Table**:

```sql
SELECT employee_id, full_name, position_title, hire_date
FROM cp.`employee.json`
LIMIT 20
```

**Errors.** `SELECT 1 FRM t` shows Drill's parse error in the panel. A misspelt macro
such as `$__timeFilterr(hire_date)` is reported by name without reaching Drill.

**Statement safety.** `DROP TABLE IF EXISTS dfs.tmp.nothing` is refused with
"DROP statements are blocked", and the editor shows a warning before the
statement is run. Turning on **Allow creating and dropping objects** in the
data source settings lets it through; Drill then answers that the table was
not found.

**Editor.** Typing after `FROM ` suggests schemas and tables; `$__` suggests
macros. **Ctrl+Enter** runs the query.

**Template variable.** In a dashboard, add a variable of type **Query** on this
data source:

```sql
SELECT DISTINCT gender FROM cp.`employee.json` ORDER BY 1
```

Enable **Multi-value** and use it as `WHERE gender IN ($gender)`.

**Annotation.** Add an annotation query on this data source:

```sql
SELECT CAST(hire_date AS TIMESTAMP) AS `time`, full_name AS text
FROM cp.`employee.json`
WHERE management_role = 'Senior Management'
  AND $__timeFilter(CAST(hire_date AS TIMESTAMP))
```

**Alert rule.** A rule on this query evaluates on Grafana's server:

```sql
SELECT COUNT(*) AS employees FROM cp.`employee.json`
```

## Full environment

The repository's `docker-compose.yaml` starts Grafana with the plugin and
three Drill servers. It needs Docker with Compose v2 and a Bash shell; the
build tools run in a container, so nothing else has to be installed.

```sh
git clone https://github.com/Sam-H101/grafana-drill-datasource.git
cd grafana-drill-datasource
devenv/run.sh npm ci
devenv/run.sh npm run build
devenv/run.sh mage -v build:linux
docker compose up -d --build --wait
```

Open http://localhost:3000. No login is needed; this development setup gives
anonymous users the Admin role, and binds to localhost only.

Already provisioned:

- Data source **Apache Drill**, pointing at the Drill without authentication.
- Dashboard **Apache Drill demo**: a time series panel, a table and a stat
  panel, two template variables (one multi-value, one with separate label and
  value columns) and an annotation query.
- Alert rule **Drill sample data is present**.

The other two Drill servers are for testing authentication and TLS. Add a data
source for each, using the URL as Grafana sees it:

| What to test | URL | Settings |
| --- | --- | --- |
| HTTP Basic | `http://drill-auth:8047` | Authentication **Basic**, user `grafana`, password `grafana-dev-password` |
| Form login | `http://drill-auth:8047` | Authentication **Form login**, same user and password |
| Wrong password | `http://drill-auth:8047` | Any other password: Save & test reports that Drill rejected the credentials |
| No credentials | `http://drill-auth:8047` | Authentication **None**: Save & test reports that Drill requires authentication |
| HTTPS, untrusted | `https://drill-tls:8047` | Save & test reports an unknown certificate authority |
| HTTPS, custom CA | `https://drill-tls:8047` | Turn on **Custom CA certificate** and paste the CA |

The password above is for this test stack only and is public on purpose. Print
the CA certificate, generated when the stack first starts, with:

```sh
docker compose exec drill-tls cat /tls/ca.crt
```

Stop and remove the stack with `docker compose down -v`.

## Automated tests

The same stack runs the automated tests, as CI does on every commit against
the supported Grafana versions:

```sh
devenv/run.sh npx playwright install chromium   # once
devenv/run.sh npm run e2e                                          # browser tests
devenv/run.sh go test -tags integration ./tests/integration/...    # backend against Drill
```

More about the development environment is in
[devenv/README.md](../devenv/README.md).
