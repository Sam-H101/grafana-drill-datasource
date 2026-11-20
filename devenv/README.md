# Development environment

Nothing is installed on the Windows host. Run everything from WSL (`vostok`) in
the repo directory, `/mnt/c/Users/sam/source/repos/grafana-drill-datasource`.

## Toolbox

`devenv/toolbox/Dockerfile` has Node, Go, Mage, golangci-lint and Chromium's
system libraries. `node_modules`, Go caches, the npm cache and Playwright
browsers live in Docker volumes, so nothing lands on the C: drive except build
output in `dist/`.

```sh
devenv/run.sh npm ci                 # install frontend deps (into a volume)
devenv/run.sh npm run build          # frontend
devenv/run.sh mage -v build:linux    # backend
devenv/run.sh npm run typecheck
devenv/run.sh npm run lint
devenv/run.sh npm run test:ci        # frontend unit tests
devenv/run.sh mage test              # backend unit tests
devenv/run.sh golangci-lint run ./...
devenv/run.sh                        # shell
```

## Dev stack

```sh
docker compose up -d --build         # Grafana :3000, Drill :8047, authenticated Drill :8048 (127.0.0.1)
docker compose restart grafana       # pick up a rebuilt backend binary
docker compose logs -f grafana
docker compose down                  # add -v to drop volumes too
```

Both ports bind to localhost only. The dev Grafana grants anonymous Admin and
runs in development mode; never expose it.

## Tests against the stack

```sh
devenv/run.sh npx playwright install chromium   # once, into the volume
devenv/run.sh npm run e2e                       # Playwright, tests/e2e
devenv/run.sh go test -tags integration ./tests/integration/...
```

Inside the toolbox, `GRAFANA_URL` and `DRILL_URL` point at the compose services.

## Failure test

`devenv/app-test-drill-failure.sh` stops the `drill` container while a panel
query is running and checks that Grafana shows a clean error well inside the
query timeout, then restarts Drill. It needs Docker on the host, so it is not
part of `npm run e2e` (the toolbox has no Docker socket, on purpose).

## Exposing the stack on the network

Ports bind to 127.0.0.1 by default. To reach the stack from other machines,
turn off anonymous Admin and set an admin password; the anonymous setting is
baked into the image, so rebuild:

```sh
BIND_ADDRESS=0.0.0.0 ANONYMOUS_AUTH_ENABLED=false \
GRAFANA_ADMIN_PASSWORD='<password>' docker compose up -d --build --wait
```

Drill on 8047 has no authentication and can read files inside its container;
expose it only on a network you trust. The e2e tests log in as admin/admin, so
run them against the default local stack.
