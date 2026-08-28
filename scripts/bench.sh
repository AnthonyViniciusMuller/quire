#!/usr/bin/env bash
# Measures what RNF06 budgets: 200 ms for a synchronization over a stable
# connection.
#
# It measures the call a device actually makes, with a real session, against a
# running node — SyncService.PullOperations, which is the half of a
# synchronization a device waits on. Pushing is measured beside it because the
# two together are what a device does when it reconnects, and a budget that held
# for one and not the other would be a budget nobody meets.
#
# What it deliberately does not measure is a method with no authentication. The
# interceptor chain is part of the latency: every call is verified, logged,
# counted and translated, and a benchmark that skipped the token would be
# measuring a node this repository does not build.
#
# The default target is the compose federation, because that is the one a
# developer has running. `make dev-up` first, or point QUIRE_BENCH_SERVER at
# something else.
set -euo pipefail

repository="$(cd "$(dirname "$0")/.." && pwd)"

server="${QUIRE_BENCH_SERVER:-127.0.0.1:19090}"
ca="${QUIRE_BENCH_CA:-$repository/deploy/docker/certs/quire-a.example.crt.pem}"

# The budget itself, in milliseconds, and the percentile it is read at. RNF06
# states a number and not a percentile, so this is the reading: a budget met at
# the median is a budget a twentieth of a reader's synchronizations miss, and a
# budget read at the maximum is one a single stalled connection fails.
budget_ms="${QUIRE_BENCH_BUDGET_MS:-200}"
percentile="${QUIRE_BENCH_PERCENTILE:-95}"

# Enough calls for the percentile to mean something, and enough concurrency for
# the pool and the interceptors to be doing more than one thing.
total="${QUIRE_BENCH_TOTAL:-2000}"
concurrency="${QUIRE_BENCH_CONCURRENCY:-16}"

ghz="$repository/bin/ghz"
quirectl="$repository/bin/quirectl"

say() { printf '\033[36mbench:\033[0m %s\n' "$*"; }

[ -x "$ghz" ] || { echo "bench: $ghz is missing; run make tools" >&2; exit 1; }
[ -x "$quirectl" ] || { echo "bench: $quirectl is missing; run make build" >&2; exit 1; }
[ -f "$ca" ] || { echo "bench: $ca is missing; run make dev-certs" >&2; exit 1; }

# --- a session ---------------------------------------------------------------

# The reader is registered for this run and left behind, as the end-to-end suite
# leaves its own: the federation is long-lived, and a benchmark that emptied it
# would be one nobody can run twice in an afternoon.
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

state="$work/device.json"
local_name="bench-$(date +%s)-$RANDOM"

export QUIRECTL_PASSWORD="benchmark-only-$RANDOM"

say "registering $local_name on $server"
"$quirectl" --server "$server" --ca "$ca" --state "$state" register \
    --local-name "$local_name" \
    --display-name "A reader of the benchmark" \
    --email "$local_name@example.org" >/dev/null

"$quirectl" --server "$server" --ca "$ca" --state "$state" login \
    --local-name "$local_name" \
    --device-name bench --device-platform bench >/dev/null

token="$(python3 -c '
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    print(json.load(handle)["session"]["access_token"])
' "$state")"

[ -n "$token" ] || { echo "bench: the device holds no access token" >&2; exit 1; }

# --- the measurement ---------------------------------------------------------

# measure runs one method and prints the reading, and reports whether it fits
# inside the budget.
measure() {
    local method="$1" data="$2"
    # A separate statement, because bash expands every word of a `local` before
    # assigning any of them: a report path derived from method on the same line
    # is a path derived from a variable that does not exist yet.
    local report="$work/${method##*.}.json"

    say "$method, $total calls at concurrency $concurrency"

    "$ghz" \
        --proto "$repository/proto/quire/v1/sync.proto" \
        --import-paths "$repository/proto" \
        --call "$method" \
        --cacert "$ca" \
        --metadata "{\"authorization\": \"Bearer $token\"}" \
        --data "$data" \
        --total "$total" \
        --concurrency "$concurrency" \
        --connections 4 \
        --format json \
        "$server" >"$report"

    python3 - "$report" "$budget_ms" "$percentile" <<'PYTHON'
import json
import sys

report, budget, percentile = sys.argv[1], float(sys.argv[2]), int(sys.argv[3])

with open(report, encoding="utf-8") as handle:
    result = json.load(handle)

# ghz reports nanoseconds. The percentile is read off the distribution it
# already computed rather than recomputed here, so that what is printed is what
# ghz would print.
readings = {entry["percentage"]: entry["latency"] / 1e6 for entry in result["latencyDistribution"]}
at = readings.get(percentile)

print(f"  average {result['average'] / 1e6:8.2f} ms")
for percentage in sorted(readings):
    print(f"  p{percentage:<6} {readings[percentage]:8.2f} ms")

failures = sum(count for code, count in result.get("statusCodeDistribution", {}).items() if code != "OK")
if failures:
    print(f"  {failures} of {result['count']} calls failed", file=sys.stderr)
    sys.exit(1)

if at is None:
    print(f"  ghz reported no p{percentile}", file=sys.stderr)
    sys.exit(1)

verdict = "within" if at <= budget else "over"
print(f"  p{percentile} is {at:.2f} ms, {verdict} the {budget:.0f} ms of RNF06")
sys.exit(0 if at <= budget else 1)
PYTHON
}

status=0

# A device that has just been bound asks from the beginning, which is the
# largest answer this call ever gives and therefore the honest one to measure.
measure quire.v1.SyncService.PullOperations '{"after_position": 0, "limit": 100}' || status=1

# And the other half: an empty push, which measures the path and not the work.
# A push carrying operations would be measuring how fast this machine's
# PostgreSQL applies them, which is a different number and not RNF06's.
measure quire.v1.SyncService.PushOperations '{"operations": []}' || status=1

exit "$status"
