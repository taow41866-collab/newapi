#!/bin/sh
set -eu
cd /mnt/f/Projects/newapi
runtime="$PWD/build/model-routing/redis-runtime"
export LD_LIBRARY_PATH="$runtime/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
server="$runtime/usr/bin/redis-server"
cli="$runtime/usr/bin/redis-cli"
# Refuse to reuse or shut down an existing listener.
if "$cli" -h 127.0.0.1 -p 16391 ping >/dev/null 2>&1; then
  echo 'Port 16391 already serves Redis; refusing to reuse it.' >&2
  exit 1
fi
"$server" --bind 127.0.0.1 --port 16391 --save '' --appendonly no --daemonize no >build/model-routing/redis-test.log 2>&1 &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true' EXIT INT TERM
ready=0
for attempt in 1 2 3 4 5; do
  kill -0 "$server_pid"
  if "$cli" -h 127.0.0.1 -p 16391 ping >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
[ "$ready" = 1 ] || exit 1
MODEL_ROUTE_TEST_REDIS=127.0.0.1:16391 ./build/model-routing/engine.test -test.run '^TestModelRoutingRealRedisProcesses$' -test.v -test.timeout 90s
[ -x ./build/model-routing/gateway-redis.test ] || { echo 'Compile gateway-redis.test before running acceptance' >&2; exit 1; }
cd controller
MODEL_ROUTE_TEST_REDIS=127.0.0.1:16391 MODEL_ROUTE_ALLOW_REDIS_PAUSE=1 ../build/model-routing/gateway-redis.test -test.run '^TestModelRoutingTwoGatewayProcesses$' -test.v -test.timeout 90s
MODEL_ROUTE_TEST_REDIS=127.0.0.1:16391 MODEL_ROUTE_PERF_REPORT=/mnt/f/Projects/newapi/build/model-routing/performance.json ../build/model-routing/gateway-redis.test -test.run '^TestModelRoutingGatewayPerformance$' -test.v -test.timeout 140s
