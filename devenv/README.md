# Development environment

Everything runs in Docker: you need Docker with Compose v2 and a Bash shell
(Linux, macOS, or WSL on Windows). Node, Go and the other build tools live in a
toolbox container, so nothing else has to be installed. Run the commands below
from the repository root.

## Toolbox

`devenv/toolbox/Dockerfile` has Node, Go, Mage, golangci-lint and Chromium's
system libraries. `node_modules`, Go caches, the npm cache and Playwright
browsers live in Docker volumes; only build output (`dist/`) is written to the
working tree. `devenv/run.sh` runs a command in the toolbox as your own user, so
files it writes belong to you.

```sh
devenv/run.sh npm ci                 # install frontend deps (into a volume)
devenv/run.sh npm run build          # frontend
devenv/run.sh npm run dev            # frontend, rebuild on change
devenv/run.sh mage -v build:linux    # backend for the dev stack
devenv/run.sh mage -v buildAll       # backend for every platform
devenv/run.sh npm run typecheck
devenv/run.sh npm run lint
devenv/run.sh npm run test:ci        # frontend unit tests
devenv/run.sh mage test              # backend unit tests
devenv/run.sh golangci-lint run ./...
devenv/run.sh                        # shell
```

## Dev stack

Build the plugin into `dist/` first, then:

```sh
docker compose up -d --build --wait  # Grafana and three Drills
docker compose restart grafana       # pick up a rebuilt backend binary
docker compose logs -f grafana
docker compose down                  # add -v to drop volumes too
```

| Service | Address | Purpose |
| --- | --- | --- |
| Grafana | http://localhost:3000 | Provisioned data source, demo dashboard and alert rule |
| `drill` | http://localhost:8047 | Drill without authentication |
| `drill-auth` | http://localhost:8048 | Drill with user authentication (Basic and form login) |
| `drill-tls` | https://localhost:8049 | Drill over HTTPS with a CA generated at start |

Inside Docker, Grafana reaches the Drills by service name, for example
`http://drill-auth:8047`. The dev-only credentials and Drill settings are in
`devenv/drill/README.md`. To trust `drill-tls`, export its CA with
`docker compose cp drill-tls:/tls/ca.crt .`.

All ports bind to 127.0.0.1. The dev Grafana grants anonymous Admin; see
[Exposing the stack](#exposing-the-stack-on-the-network) before opening it up.

The versions tested are pinned in `docker-compose.yaml`. Override them with
`GRAFANA_VERSION`, `GRAFANA_IMAGE` (`grafana` or `grafana-enterprise`) and
`DRILL_VERSION`.

## Tests against the stack

```sh
devenv/run.sh npx playwright install chromium   # once, into the volume
devenv/run.sh npm run e2e                       # Playwright, tests/e2e
devenv/run.sh go test -tags integration ./tests/integration/...
```

Inside the toolbox these reach the compose services by name, and the HTTPS
Drill's CA is mounted at `/tls/ca.crt`. To run the integration tests from
elsewhere (as CI does), point them at the published ports with `DRILL_URL`,
`DRILL_AUTH_URL`, `DRILL_TLS_URL` and `DRILL_TLS_CA_FILE`.

Playwright saves a screenshot of each step under `artifacts/app-tests/`, so you
can check what the tests saw.

## Failure test

`devenv/app-test-drill-failure.sh` stops the `drill` container while a panel
query is running and checks that Grafana shows a clean error well inside the
query timeout, then restarts Drill. It needs Docker on the host, so it is not
part of `npm run e2e` (the toolbox has no Docker socket, on purpose).

## Exposing the stack on the network

To reach the stack from other machines, turn off anonymous Admin and set an
admin password. The anonymous setting is built into the image, so rebuild:

```sh
BIND_ADDRESS=0.0.0.0 ANONYMOUS_AUTH_ENABLED=false \
GRAFANA_ADMIN_PASSWORD='<password>' docker compose up -d --build --wait
```

Drill on 8047 has no authentication and can read files inside its container;
expose it only on a network you trust. The e2e tests log in as admin/admin, so
run them against the default local stack.
