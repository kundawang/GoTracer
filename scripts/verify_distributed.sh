#!/usr/bin/env bash
# End-to-end verification that a distributed master/agent render of the fixed
# triangle scene is byte-identical to a local single-process render. The scene
# spec pins Settings.Seed so every pixel derives the same RNG stream no matter
# which process or patch renders it.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"; kill "${AGENT_PIDS[@]:-}" 2>/dev/null || true' EXIT

# The sandbox routes all HTTP through a proxy; loopback agent traffic must
# bypass it or the master's /status probe gets intercepted.
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
export no_proxy="127.0.0.1,localhost" NO_PROXY="127.0.0.1,localhost"

cd "$ROOT"
go build -o "$WORK/tracer" .
go build -o "$WORK/agent" ./net/agent
go build -o "$WORK/master" ./net/master

PORTS=(19190 19191 19192)
AGENT_PIDS=()
for port in "${PORTS[@]}"; do
	"$WORK/agent" --addr="127.0.0.1:$port" >"$WORK/agent-$port.log" 2>&1 &
	AGENT_PIDS+=("$!")
done

# Wait for every agent's /status endpoint to come up.
for port in "${PORTS[@]}"; do
	for _ in $(seq 1 50); do
		if curl -sf "http://127.0.0.1:$port/status" >/dev/null 2>&1; then
			break
		fi
		sleep 0.1
	done
	curl -sf "http://127.0.0.1:$port/status" >/dev/null
done
{
	printf '[\n'
	for i in "${!PORTS[@]}"; do
		sep=","
		[ "$i" -eq $((${#PORTS[@]} - 1)) ] && sep=""
		printf '  "127.0.0.1:%s"%s\n' "${PORTS[$i]}" "$sep"
	done
	printf ']\n'
} >"$WORK/agents.json"
python3 -c "import json; json.load(open('$WORK/agents.json'))"

python3 - "$ROOT/examples/triangleScene.json" "$WORK/local.json" "$WORK/dist.png" <<'PY'
import json, sys
src, local_path, dist_out = sys.argv[1], sys.argv[2], sys.argv[3]
spec = json.load(open(src))
local = json.loads(json.dumps(spec))
local["Image"]["OutputFile"] = dist_out.replace("dist.png", "local.png")
json.dump(local, open(local_path, "w"))
dist = json.loads(json.dumps(spec))
dist["Image"]["OutputFile"] = dist_out
json.dump(dist, open(dist_out.replace("dist.png", "dist.json"), "w"))
PY

"$WORK/tracer" --spec="$WORK/local.json"
"$WORK/master" --spec="$WORK/dist.json" --agents="$WORK/agents.json"

if cmp -s "$WORK/local.png" "$WORK/dist.png"; then
	echo "PASS: local and master/agent renders are byte-identical ($(stat -c%s "$WORK/local.png") bytes)"
else
	echo "FAIL: local and distributed renders differ" >&2
	exit 1
fi
