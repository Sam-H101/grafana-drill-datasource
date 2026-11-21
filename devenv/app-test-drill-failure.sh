#!/usr/bin/env bash
# App test: stop Drill while a panel query is running and check that Grafana
# shows a clean error well before the query timeout. Needs the dev stack up
# (docker compose up -d). Run from the repo root: devenv/app-test-drill-failure.sh
set -uo pipefail
cd "$(dirname "$0")/.."
export TOOLBOX_UID="$(id -u)" TOOLBOX_GID="$(id -g)"

docker compose run --rm -T -e DRILL_KILL_TEST=1 toolbox \
  npx playwright test tests/e2e/hardening.spec.ts -g "stopping mid-query" --reporter=line &
test_pid=$!

# Wait until the slow query is running on Drill, then stop the Drillbit.
for _ in $(seq 1 90); do
  if curl -s http://127.0.0.1:8047/profiles.json | tr -d ' \n' | grep -q '"runningQueries":\[{'; then
    break
  fi
  sleep 1
done
sleep 2
echo "Stopping drill while the query runs"
docker compose kill drill >/dev/null

wait "$test_pid"
rc=$?

echo "Restarting drill"
docker compose up -d drill >/dev/null 2>&1
for _ in $(seq 1 60); do
  [ "$(docker inspect -f '{{.State.Health.Status}}' "$(docker compose ps -q drill)")" = healthy ] && break
  sleep 3
done
exit "$rc"
